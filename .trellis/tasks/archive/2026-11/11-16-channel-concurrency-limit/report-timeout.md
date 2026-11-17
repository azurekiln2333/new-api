# Timeout implementation report

Files: service/upstream_first_byte_timeout.go (new production file), service/http_client.go, service/http_transport_policy.go, service/http_client_transport_test.go, relay/channel/api_request.go, relay/channel/api_request_test.go.

Implemented synchronous first response-body-byte gating, replay, empty EOF/NoBody success, synchronized timeout winner, typed 504 cancellation before handler/billing, retained parent cancellation, body close before failure returns, no background body-reading goroutine, 101 no pre-read plus preserved io.Writer, CloseIdleConnections forwarding. Final direct/proxy/sharded clients include resolved seconds in cache keys. Ordinary/form requests inherit parent context. API pre-response SSE ping is suppressed and its selected timeout is frozen with the client for this attempt. Existing total/header/idle limits are unchanged.

The timeout cause implements RetryableError(false) for AWS internal retries, without gateway SkipRetry. ErrOptionWithHideErrMsg would erase the cause; dedicated timeout errors bypass that mask using their safe message. Exported resolver/typed classifier match the agreed contracts.

Passed:
- gofmt on all six assigned files.
- go test ./service ./relay/channel -run 'Test(UpstreamFirstByte|APIAndFormRequestsInheritClientCancellation|FirstByteTimeoutDoesNotCommitEarlySSEKeepAlive)' -count=1
- go test ./service ./relay/channel -run 'Test(HTTPClientCache|ResetProxyClientCache|InvalidateProxyClient|NormalizeHTTPTransportPolicy|UpstreamFirstByte|ForcedHTTP1|ShardedRoundTripper|ChannelTLS|APIAndFormRequestsInheritClientCancellation|FirstByteTimeoutDoesNotCommitEarlySSEKeepAlive|NewTaskAPIRequest)' -count=1
- After freezing per-attempt resolution: go test ./relay/channel -run 'Test(APIAndFormRequestsInheritClientCancellation|FirstByteTimeoutDoesNotCommitEarlySSEKeepAlive)' -count=1
- git diff --check for assigned tracked files.

Deterministic tests use testing/synctest virtual time and a real HTTP transport over net.Pipe. Cases include no headers/headers-only stalls, exact byte replay, long streams, cancellation before/after first byte, timeout winning over a late buffered byte, body Close, empty EOF, 101 writes, SDK actual InvokeModel single-send timeout plus ordinary503 retry control, global snapshot changes selecting correct cached clients, overrides and explicit0, and no early SSE commitment.

Integration observations: full go test ./service ./relay/channel -count=1 passed channel but service failed the old TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups exhausted-candidate nonnil assertions at line158, reported to capacity worker. Local race command blocked because CGO is disabled; Linux CI must cover races. Outer controller pinger/failover and final-response timeout localization belong to capacity worker; no Git writes. Main subsequently assigned this worker frontend implementation as a continuation.

## Final review correction

The first-byte timer is now explicitly selected with `service.GetRelayHttpClientWithProxySettings`. Generic `GetHttpClientWithProxySettings` and `NormalizeHTTPTransportPolicy` retain only connection settings, so global/channel timeouts do not affect background plugin polling, `utils.fetch`, artifact fetches, model discovery or balance/status queries. Ordinary requests, administrator tests and plugin submissions use the explicit client via `channel.doRequest`; AWS SDK and Midjourney submissions explicitly select it too. The effective timeout remains in the shared cache key and is frozen for ordinary attempt logging/keepalive decisions.

Jimeng submission and Coze status/message queries now use `http.NewRequestWithContext(c.Request.Context(), ...)`, preserving client/lease cancellation. No plugin detached-context block was edited.

Additional files: `relay/channel/aws/relay-aws.go`, `relay/channel/jimeng/adaptor.go`, `relay/channel/coze/relay-coze.go`, one client-selection line in `service/midjourney.go`, and regressions appended to the existing `controller/relay_stream_test.go` with capacity worker's coordination.

Verification:
- `go test ./service ./relay/channel ./controller -run 'Test(UpstreamFirstByte|APIAndFormRequestsInheritClientCancellation|FirstByteTimeoutDoesNotCommitEarlySSEKeepAlive|ProviderRequestsInheritAttemptCancellation)' -count=1` passed all three packages. New deterministic real-HTTP test proves the same delayed response succeeds on a generic channel client and times out on a relay client; cache tests cover global inheritance, channel overrides and legacy proxy-client exclusion. Provider tests cover cancellation at Jimeng submission and both Coze query endpoints.
- `go build ./...` passed.
- `go vet ./service ./relay/channel/... ./controller` passed.
- `go test ./service ./relay/channel ./relay/channel/aws ./relay/channel/task/jsplugin -count=1` passed service, AWS and JS plugin packages. Channel package initially hit a Windows socket-abort in existing `TestUpstreamGetBody_HTTP2RetryAfterGracefulGoAway_PassThrough`; the test uses its own HTTP/2 client and local TCP server, outside this changed client selection. Its isolated rerun and then `go test ./relay/channel -count=1` both passed (full package 0.070s). No unrelated test/product edits were made for this transient result.
- `gofmt` on changed Go files and `git diff --check` passed.

## macOS runner fallback (release follow-up)

The coordinator reported the first ARM64 macOS release job canceled after 15 minutes with an explicit runner-capacity error and its retry still queued. At the coordinator's authorization, the only release configuration edit is `.github/workflows/release.yml`: select `macos-15-intel`; set `GOOS=darwin`, `GOARCH=arm64`, `CGO_ENABLED=1`, and `CC=clang` only on its backend-build step; inspect the result with `file` and `go version -m` and require `lipo -archs` to equal exactly `arm64`. Frontend tooling and Go installation continue to run natively on the Intel host. The PGO input, linker flags, version, output filename, checksum and upload rules are preserved.

Evidence checked on 2026-11-16:

- GitHub's current [runner image table](https://github.com/actions/runner-images/blob/main/README.md) lists `macos-15-intel` as x64 and `macos-latest` as ARM64. Its [macOS 15 image inventory](https://github.com/actions/runner-images/blob/main/images/macos/macos-15-Readme.md) includes Xcode 16.4 and the macOS SDK.
- [Go cgo documentation](https://pkg.go.dev/cmd/cgo) requires explicitly enabling cgo and selecting the C compiler when cross-compiling. The exact [Go 1.26.10 compiler-driver source](https://github.com/golang/go/blob/go1.26.10/src/cmd/go/internal/work/exec.go) (`gccArchArgs`) and [linker source](https://github.com/golang/go/blob/go1.26.10/src/cmd/link/internal/ld/lib.go) (`hostlinkArchArgs`) automatically pass `-arch arm64` for darwin/arm64. No duplicate CGO architecture flags or external linker override is needed.
- Apple's [Building a universal macOS binary](https://developer.apple.com/documentation/apple-silicon/building-a-universal-macos-binary) documents building the ARM64 slice on an Intel Mac, while noting that Intel cannot run that slice.
- The pinned `moejs v0.1.0-alpha.7` is pure Go (verified in its module README and source). `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go list -deps` identified cgo only in `runtime/cgo`, gopsutil CPU/memory, and Prometheus's process collector; there is no moejs native archive to replace.

Checks passed: `actionlint -shellcheck= -pyflakes= .github/workflows/release.yml` (v1.7.12) and `git diff --check`. Actual Darwin cross-build and `file`/`lipo`/build-info output remain to be observed in CI. This preserves ARM64 architecture and release naming, not bit-for-bit output across different SDKs. Native ARM64 runtime execution cannot be checked on the Intel runner. No Git commit, push or tag mutation was performed by this worker.
