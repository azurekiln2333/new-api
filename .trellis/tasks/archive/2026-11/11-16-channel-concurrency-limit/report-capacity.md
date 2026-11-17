# Capacity implementation handoff

## Behavior

Implemented channel-ID admission shared across all keys and cache refreshes. Local limits use a mutex-protected count; configured Redis uses atomic owner-token leases with server time, renewal and fail-closed503. Saturation is a local429, never an upstream health failure. Busy candidate traversal does not consume upstream retry attempts, respects pins/strict sessions and hard-excludes previously busy candidates. Failed-channel avoidance now returns exhaustion instead of randomly reusing a failed channel, and exhaustion preserves the latest useful upstream error.

Slots cover ordinary HTTP full response/stream, administrator tests, async submission HTTP lifetime, Realtime upstream sockets and reusable Responses upstream sockets. All paths release before retry or after actual cleanup; async remote jobs and background polling are not counted. Native SSE submissions retain their existing client-disconnect/settlement behavior while renewing their slot until completion; coordinator loss cancels even detached outbound work.

Added typed first-body-byte timeout retry handling after existing stop boundaries, final error translation, and configured status-range handling for task retries. Early Claude keepalive is inside admission and suppressed while the bounded first-byte wait can still fail over. Pre-consumption remains once per request with existing settlement/refund semantics.

Independent review fixes: client cancellation now bypasses channel-disable/error-log handling and records a local cancellation policy event; canceled partial streams do not record channel failure. Auto-group admission restores the actual selected group after a busy candidate, including the middleware's initial pre-advance at RetryTimes=0, so available same-group candidates are not skipped.

Final follow-up also restores the candidate's spent retry count, pending retry reset and affinity offset when capacity rejects it. The snapshot is refreshed for each selected channel and only restored for a matching channel in auto-group scope; successful admission retains existing cross-group retry semantics. Unlimited admission explicitly masks a previous detached attempt's lease marker, so limited A to unlimited B failover cannot attach B to the released A lease.

## Files owned

- New service/channel_capacity.go and service/channel_capacity_test.go (the task's sole new Go test file).
- service/request_policy.go, channel_select.go, relay_error.go, channel.go; existing channel_select_auto_groups_test.go and relay_error_test.go.
- controller/relay.go, channel-test.go, relay_stream_test.go, relay_task_plugin_test.go and responses_websocket_test.go.
- relay/responses_websocket.go, responses_websocket_test.go, websocket.go; narrow detached-context integration in relay/channel/task/jsplugin/adaptor.go.
- i18n/keys.go and i18n/locales/{en,zh-CN,zh-TW}.yaml.
- This progress/report artifact. No commit, push or tag performed; main owns release.

## Validation

- gofmt on all owned modified Go files; git diff --check passed.
- Earlier full `go test -p 1 ./service ./relay -count=1`: passed service26.546s and relay5.730s before final detached-context adjustment.
- Focused service/controller/relay checks passed before the last stream fixture addition. The new fixture initially failed because i18n setup was missing and its fallback returned JSON to a stream request; fixed fixture initialization, bounded cleanup and valid SSE response. These were test setup failures.
- Full command: `go test -p 1 ./service ./controller ./relay ./relay/channel/task/jsplugin -count=1 -timeout 180s`; log capacity-final-check.log. Service passed17.253s, relay passed0.274s, jsplugin passed1.375s. Controller hit the total3-minute limit during existing security enrollment coverage and exposed two assertions: response_started precedence and a single-channel metrics fixture relying on retry reuse. Both were fixed; main owns the final controller rerun with a10-minute allowance.
- Final lifecycle check: `go test -p 1 ./controller -run 'TestChannelCapacityStreams|TestExecuteTaskSubmissionCallerCancellation|TestResponsesWebSocketReusesConnection' -count=1 -timeout 45s` passed; log capacity-final-lifecycle-check.log.
- Review regression command: `go test -p 1 ./service ./controller -run 'TestRetryAvoidsFailedChannels|TestRequestPolicyEventsReachLogAdminInfo|TestRespondTaskSubmissionErrorWithoutCause|TestResponsesHTTPHealthCountsFinalResult|TestChannelCapacityStreams|TestExecuteTaskSubmissionCallerCancellation|TestResponsesWebSocketReusesConnection' -count=1 -timeout 60s` passed service5.130s/controller2.737s. Log capacity-review-check.log. This includes all final production changes and both full-controller assertion fixes.
- Final follow-up command: `go test -p 1 ./service ./controller ./relay -run 'ChannelCapacity|TestRetryAvoidsFailedChannels|TestRequestPolicyEventsReachLogAdminInfo|TestExecuteTaskSubmissionCallerCancellation|TestResponsesWebSocketReusesConnection' -count=1 -timeout 60s` passed service5.177s/controller1.195s/relay5.606s. Log capacity-final-boundaries-check.log. After adding the explicit snapshot channel-ID match, `go test -p 1 ./service -run 'ChannelCapacity|TestRetryAvoidsFailedChannels' -count=1 -timeout 45s` passed (capacity-final-selection-check.log).

## Required external checks / limits

TEST_REDIS_ADDR was unset locally; real Redis verification is skipped locally and must pass in CI. The integration test fails if an explicitly configured Redis server cannot be reached and records redis_version. Controller stream tests use miniredis for integration only, not as a substitute for real Redis. Main owns the real SQLite/MySQL/PostgreSQL matrix and CI service provisioning.

The cap bounds gateway admission while leases are valid; network partitions/process pauses cannot prove that a remote provider immediately stopped processing. Existing shared configuration refresh delays apply. WebSocket first-body-byte deadlines and asynchronous remote-job lifetime remain outside the timeout/count scope as approved.

Final reviewer identified missing request-context propagation in Jimeng and Coze adaptor calls and overly broad timeout client selection for background requests. Main/timeout worker owns those fixes and their checks; they are not claimed completed by this capacity report.
