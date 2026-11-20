# Worker 1 — generation diagnostics handoff

**Frozen for root/pi 2 review and exact-SHA cloud validation.** Only source review and the approved `gofmt -w` were performed. No local tests, builds, vet, lint, typecheck, browser checks, dependency installs, upstream probes, staging, commits, or pushes.

## Exact file list

- `controller/channel-test.go`
- `controller/channel_test_request_test.go`
- `relay/diagnostic_text.go`
- `relay/chat_completions_via_responses.go`
- `relay/claude_handler.go`
- `relay/compatible_handler.go`
- `relay/gemini_handler.go`
- `relay/responses_request.go`
- `relay/requestplugin/relay.go`
- `web/src/features/channels/components/dialogs/channel-test-dialog.tsx`
- `web/src/features/channels/components/__tests__/channel-test-dialog-endpoints.test.tsx`
- `.trellis/tasks/11-21-channel-request-policy/worker-1.md`

Root's cancellation adjustment in `controller/relay_stream_test.go` is present and was not edited by worker 1. No fingerprint orchestrator, service admission, CI, locale, schema, or dependency files edited here.

## Behavior and read-back

- Manual, browser batch, scheduled, and fingerprint tests share `testChannel`/`runChannelTest`. The existing directional resolver handles all text requests, including ordinary Auto on Responses-only channels and both-endpoint channels with a global Responses preference. Explicit Responses are not downgraded to Chat; Gemini is not upgraded to Responses. Unsupported directions and passthrough conflicts fail before dispatch. Embedding, rerank, image, and provider-plugin routes are still handled by their selected adaptors. The dialog filters incompatible explicit inputs but sends no endpoint override for Auto.
- `InitChannelDiagnostic` is called once per attempt, before model/suffix preparation and its Request hook. It clears busy evidence and `Retry-After` and binds the request plugin plan. Shared preparation preserves outbound/parameter/finalizer/egress ordering. Converted Chat retains outbound and parameter overrides in Chat form before conversion. Both native and converted Responses invoke `ResponsesRequestFinalizer` before egress; the fingerprint budget wrapper forwards that optional interface if present. Static header overrides and `{api_key}` are supported; browser header passthrough remains disabled, and adaptor `DoResponse` still owns response formatting.
- Ordinary Chat retains its existing post-conversion prompt behavior. Diagnostics apply the prompt to the synthetic protocol input before conversion so it survives providers that convert Chat into another shape. Native non-Codex Responses diagnostics put it in synthetic `instructions` without changing ordinary Responses callers. Converted Chat/Messages-to-Responses skip host-side prompt insertion for Codex because its adaptor already sets or prefixes `instructions`. Text passthrough uses the synthetic body without prompt/body mutation, but still runs passthrough egress. Codex fingerprint rejection remains intentional.
- One fixed-channel admission owner spans cap changes and at most one upstream-busy replay. After a grant, reload the stored channel and recheck the model, credential scope, and cap; release/reacquire if the cap changed. Use the lease context through response cleanup. Manual named keys remain eligible for disabled-key/model recovery; the busy replay holds the actual original key and model, not a fresh automatic choice. Background work uses the internal queue identity. Cancellation, local saturation, and a remaining zero-work busy result do not count as unhealthy; queue time does not inflate the health latency threshold. Only a validated response can mark the upstream available.
- Native and converted HTTP refusals share the diagnostic classifier before status remapping. Only a recognized concurrency refusal with no output/usage evidence, no response write, and no known usage can replay. Generic 429, quota, transport, and SSE failures do not trigger a replay. The diagnostic controller maps the model once and owns the only settlement: report a failed/truncated reply as failed, but retain and log nonzero usage returned by the adaptor exactly once. Zero-work refusals and canceled requests without known usage do not generate settle logs.

## Authored regressions — not run

- `TestChannelDiagnosticEndpointPolicy`: manual/scheduled wire paths, Chat/global preference behavior, invalid directions and passthrough conflicts, one settlement on success.
- `TestChannelDiagnosticRequestPolicy`: bound Request/Outbound/Egress hooks, mapped model, prompt, parameter override precedence, static headers, no browser passthrough.
- `TestChannelDiagnosticBusyRetryAndProductiveFailure`: native and converted HTTP retry on the same key, no replay for generic/quota or usage-bearing refusals, native/converted productive `response.failed` and truncated SSE return failure and record the observed usage once.
- `TestChannelDiagnosticReadmissionUsesCurrentCapAndPolicy`: real post-grant DB read, stale lease released when cap drops, cancel or send with refreshed key/header/endpoint after capacity becomes available. Uses channel synchronization, not sleeps.
- `TestFingerprintDiagnosticReloadsPolicyBetweenProbes`: key/headers/endpoint change between samples, disabled-channel diagnostics, no dispatch after removal/deletion. Extended `TestFingerprintResponsesOnlyChannelProbe` with one system prompt on the wire; retains output cap, cancellation, and log assertions.
- `channel-test-dialog-endpoints.test.tsx`: Auto sends no override, explicit unsupported choices are absent, undeclared/plugin options still work.

Cloud selectors: `go test -p 1 -count=1 -v ./controller -run 'TestChannelDiagnostic|TestFingerprintDiagnostic|TestFingerprintResponsesOnlyChannelProbe|TestChannelTestOpenAIChatCompatibility|TestChannelTestKeySelection'`; run the DB-backed subset against SQLite and the actual MySQL/PostgreSQL `TEST_CHANNEL_SQL_DSN` fixtures. Root's workflow already includes the new selectors in the dialect/race jobs. For shared relay prep: `go test -p 1 -count=1 ./relay -run 'TestChannelEndpointResponsesConversionPreservesClientFormat|TestPrepareResponsesRequest'`. From `web/`: `bun run test src/features/channels/components/__tests__/channel-test-dialog-endpoints.test.tsx src/features/channels/components/__tests__/channel-test-key-selection.test.tsx`.

## Remaining review boundary

The productive-stream accounting fix covers usage returned by response handlers. A concrete separate case for root review: when a converted non-stream test receives unsolicited SSE, existing `OaiResponsesToChatBufferedStreamHandler` can discard partial usage internally on `response.failed` or a scan error before the controller sees it. This path is not eligible for replay, but the controller cannot settle usage the handler does not return. A raw non-2xx error envelope can likewise prevent replay without producing trusted usage. Neither behavior was broadened in this closing pass. No integrated cloud result is claimed for this patch.
