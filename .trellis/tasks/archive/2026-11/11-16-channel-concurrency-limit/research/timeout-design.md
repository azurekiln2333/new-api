# Upstream first-body-byte timeout design

Research date: 2026-11-16. Read-only product research; the billing rules were read in full. No product or Git changes.

## Recommended contract (coordinator accepted)

- Global option `UpstreamFirstByteTimeoutSeconds`: nonnegative integer seconds, default `0` to preserve existing environment timeouts.
- Channel setting `upstream_first_byte_timeout_seconds`: `*int`, absent/null means inherit global, explicit `0` disables this new deadline for the channel, positive seconds override global.
- The timer starts when the outbound HTTP attempt begins, including connection/request upload and response-header wait; it ends when the first response-body byte arrives. Headers alone do not stop it.
- A normal empty response ending in EOF also completes the wait. This is a first-byte deadline, **not first token/content**, total response duration, or stream-idle timeout. An upstream SSE comment/body byte counts. Existing `RELAY_TIMEOUT`, `RELAY_RESPONSE_HEADER_TIMEOUT`, and `STREAMING_TIMEOUT` remain separate limits, so explicit zero does not disable those existing deployment limits.
- If timeout wins, cancel and close the current upstream attempt before attempting another eligible channel. Once the first byte wins, remove this timer while keeping parent/client cancellation connected for the remaining body lifetime.

## Evidence and necessary seams

1. `service/http_client.go:112-127` applies only `Transport.ResponseHeaderTimeout`; a response with headers and no body defeats it. `newRelayHTTPClient` at 132 applies `common.RelayTimeout` as a whole-client timeout, which would cut long streams and must not become this setting.
2. `relay/channel/api_request.go:329,357` currently builds ordinary JSON and multipart requests with `http.NewRequest`, without `c.Request.Context()`. `doRequest` at 459 does not repair that omission. These paths must use `http.NewRequestWithContext` (or one well-defined central binding preserving any existing request context); otherwise client cancellation is lost. `DoTaskApiRequest` already uses `newTaskAPIRequest` with context at 553. AWS Mantle also already binds its context (`relay/channel/aws/mantle.go:99`).
3. `relay/channel/api_request.go:477-486` starts an SSE keepalive before `relayClient.Do`; `controller/relay.go:154-157` starts another outer Claude keepalive. The first ping commits HTTP 200. `service/relay_error.go:46` then stops every retry with `response_started`. Both early keepalive paths must be suppressed while an enabled first-byte deadline is pending. Stream-scanner keepalives can continue after the outbound first-byte gate succeeds.
4. `relay/helper/stream_scanner.go:173-190,341-353` closes the body and joins scanner/data/ping goroutines. Preserve this shutdown discipline. Its idle timer resets on every scanned line (`297`), and its first-response metric increments only for nonempty `data:` lines (`309-311`); neither is identical to a transport first-byte deadline.
5. Avoid a controller-only timer around the full helper: `relay/channel/openai/relay_responses.go:76-104` returns `accumulator.Finish(), nil` even when the scanner timed out before useful data. `relay/responses_handler.go:77-103` then settles. Making timeout fail before `DoResponse` is entered avoids a new billing/refund rewrite and a retry after an already-settled empty attempt.
6. `relay/request_billing.go:22-24,70-81` explicitly keeps one pre-consume session through retries and refunds only the final failure. Keep that contract; do not pre-consume/refund every timeout attempt.

## Smallest implementation boundary

Use a transport decorator around the final direct/proxy/sharded `http.RoundTripper`, enabled only when the resolved first-byte timeout is positive:

1. Derive a child cancellation context from `req.Context()` and start the timer. Clone the request onto the child context, call the underlying `RoundTrip`, and synchronously read exactly one body byte before returning the response to the adaptor. No handler goroutine should outlive an aborted attempt.
2. On timeout, cancel with a distinguishable cause and close any body. Return a typed timeout error which preserves its identity through `types.NewError` / `types.NewOpenAIError` (`relaykit/types/error.go:254-294` already preserves nested NewAPIError).
3. On first byte, stop the timer with an explicit synchronized winner between expiry and success. Do **not** defer unconditional cancellation in `RoundTrip`, because it would abort the successful stream immediately.
4. Return a body wrapper that replays the single buffered byte, delegates all later reads, and cancels/releases exactly once at EOF/error/Close. Preserve `CloseIdleConnections` forwarding through the decorator. Handle `http.NoBody` and empty EOF without inventing a timeout.
5. Include resolved seconds in `HTTPTransportPolicy.cacheKeyPart` so different channel overrides cannot reuse a client with the wrong timer (`service/http_client.go:143-145,351-368`; `service/http_transport_policy.go:15,92`). Resolve the global value from the request-policy/runtime setting on each client selection, not from one stale startup-only client.
6. Gate the two pre-response keepalive sites on the actual selected channel's effective setting. The outer Claude keepalive currently precedes the retry loop, so move that decision into the selected-attempt lifetime or record a request-level suppression flag before it can start. A timed request must not accidentally inherit an outer pinger from an earlier attempt.

Putting the first-byte gate in the client transport rather than only `channel.doRequest` also covers AWS SDK requests: `relay/channel/aws/relay-aws.go:64-86` supplies `GetHttpClientWithProxySettings` to Bedrock. SDK invocation occurs later in `awsHandler` / `awsStreamHandler` (`241,271`), so a controller hook around `Adaptor.DoRequest` alone misses it. SDK-internal retries may consume several first-byte windows; their budget must be explicit in validation and should not be misrepresented as one overall user-request deadline.

## Scope boundaries

- Covers standard HTTP JSON, multipart, Claude, Gemini, Responses, proxy/sharded clients, and AWS SDK HTTP requests that use the channel-selected client.
- WebSocket handshakes use `websocket.Dialer` directly (`relay/channel/api_request.go:420`) and need a separate handshake deadline if included; a first-body-byte definition does not apply to an upgraded WebSocket. Existing Responses WebSocket downstream upgrade also limits transparent failover. Do not claim these are covered by an HTTP transport decorator.
- Asynchronous task submission may use the shared HTTP client, but remote queued/running task duration and polling are not first-byte wait. Task plugin code intentionally detaches cancellation in some paths (`relay/channel/task/jsplugin/adaptor.go:554-581`); do not override that policy accidentally or claim a submitted job was canceled remotely.
- Direct auxiliary provider calls using `GetHttpClient()` or their own client do not automatically gain channel overrides. Inventory them only if those operations are in the agreed scope.

## Focused regression plan

Extend `service/http_client_transport_test.go` for the first-byte transport contract with deterministic controllable RoundTripper/body fixtures: no headers; headers then blocked body; exact first-byte replay; empty EOF; parent cancellation; successful first byte followed by a body that remains open beyond timer trigger; expiry/first-byte race; Close cleanup; distinct settings across cached clients. Trigger a controllable timer/cancel signal rather than asserting elapsed wall time.

Extend the existing `relay/channel/api_request_test.go` cancellation case for ordinary/form paths and the relevant existing keepalive test in `relay/helper/stream_scanner_test.go` for no pre-byte commitment under enabled timeout. At integration level, assert A times out, upstream A is canceled/closed, B succeeds, no bytes from A reach the client, and one final charge; when all fail, the original pre-consume refunds once. Retain tests proving response-started, pinned-channel and strict-session boundaries.

The separate failover work must remove the unconditional 504/524 exclusion and preserve origin-aware error handling. The new timeout should appear in policy decision logs as an upstream first-byte timeout, not client cancellation or local saturation.

## AWS SDK retry follow-up

Verified against the exact installed dependencies from `go.mod:11-14`: `aws-sdk-go-v2 v1.41.5`, `service/bedrockruntime v1.50.4`, and `smithy-go v1.24.2`. Their source is available under `C:/Users/lichengyu/go/pkg/mod/github.com/aws/`.

The narrowest way to prevent this new deadline from resetting inside one SDK invocation is for the typed first-byte timeout error to implement `RetryableError() bool { return false }`. This is an AWS retry-classification interface, not the gateway's SkipRetry flag:

- `aws-sdk-go-v2@v1.41.5/aws/retry/standard.go:79`: the standard default checks run `NoRetryCanceledError`, then `RetryableError`, then `RetryableConnectionError`, then HTTP/error-code checks. `DefaultMaxAttempts` is 3.
- `aws-sdk-go-v2@v1.41.5/aws/retry/retryable_error.go:26-32`: the first non-unknown result wins. At `50-58`, `RetryableError` calls `errors.As` for `interface { RetryableError() bool }` and returns false immediately when this typed error says false.
- `smithy-go@v1.24.2/transport/http/client.go:102,142`: the client wraps HTTP errors as `RequestSendError` and supports `Unwrap`. Although that wrapper claims `ConnectionError() == true`, the earlier RetryableError classifier finds the nested timeout first. The typed cause must survive the HTTP client's `url.Error` and gateway `NewAPIError` wrapping.
- The transport must cancel only its derived attempt context. If it canceled the SDK's parent context instead, Smithy `client.go:105-111` would replace the error with `smithy.CanceledError`, and `newAwsInvokeError` in `relay/channel/aws/relay-aws.go:51-61` would classify it as client cancellation and set gateway SkipRetry.
- `aws-sdk-go-v2@v1.41.5/aws/retry/middleware.go:253-257` immediately returns a non-retryable SDK error. The host can then classify the typed first-byte timeout and select the next channel. Leave gateway retry budget and pinned/response-started conditions intact.

All unrelated SDK behavior stays with the resolved standard/adaptive retryer. Do not set `aws.NopRetryer{}` or `MaxAttempts=1` globally. If an explicit wrapper is preferred, `bedrockruntime.New` resolves `Options.Retryer` before running option functions (`api_client.go:204,226`), so a constructor option can wrap that resolved retryer and override only `IsErrorRetryable`; preserve/delegate `aws.RetryerV2` methods. The structural error interface above is smaller and works with the existing default retryers in both credential forms of `newAwsClient`.

Extend `relay/channel/aws/relay_aws_test.go` with a fake HTTP client or controllable local server that returns the nested typed timeout and count outbound sends through a real `InvokeModel` call: exactly one SDK HTTP attempt, typed error retained, parent context healthy, host error remains retryable. Include a control for an ordinary retryable error to show existing SDK behavior remains available; use a zero-backoff test retryer rather than sleeping. No product code was changed during this research.
