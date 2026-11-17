# HTTP Responses 403/404 failover findings

Research date: 2026-11-16. Read-only product inspection; production settings and request logs were not available to this researcher.

## What the code proves

- `setting/operation_setting/status_code_ranges.go:21-28` already includes 403 and 404 in the default 401-407 retry interval. 504 and 524 are different: lines 31-34 permanently exclude them, including when an administrator puts them in the configured range.
- `relay/responses_handler.go:56-70` sends any non-200 HTTP response through `service.RelayErrorHandler`, applies the channel status mapping, and returns before writing a response to the client. It does not apply SkipRetry to an ordinary upstream 403/404.
- `service/error.go:87-133` retains the actual HTTP status. Invalid JSON/plain text uses `BadResponseStatusCode`, not `BadResponseBody`; structured OpenAI error bodies use `WithOpenAIError`. Therefore malformed 403/404 bodies are not automatically blocked by the BadResponseBody exclusion.
- `common/constants.go:145` and the frontend Request Policies defaults use `RetryTimes = 0`. `controller/relay.go:177` bounds attempts by that setting and `service/relay_error.go:65-66` stops after the first upstream failure with `attempt_budget_exhausted`. A normal, unpinned request with a positive budget and the default ranges can already retry 403/404.
- The other explicit stops in `service/relay_error.go:46-81` are response_started, strict_session, pinned_channel, non_retryable_error, system_retry_exclusion, and status_not_retryable. Channel `status_code_mapping` is applied before this decision (`relay/responses_handler.go:69`; `service/error.go:140-162`) and can change a retryable upstream status into a non-retryable one.
- Legacy affinity rules with no SessionMode and SkipRetryOnFailure=true remain strict (`service/request_policy.go:154-169`). New default rules use inherit, whose empty global default resolves to prefer. Thus an upgraded installation's persisted affinity rules may differ from current defaults.
- `RecordChannelFailure` records failed channel IDs independently of the retry-exclusion switch (`service/channel_affinity.go:668-689`). `selectRetryChannel` only uses those exclusions when RetryAvoidFailedChannels is enabled (`service/channel_select.go:239-251`), and returns to random selection when untried candidates are exhausted. With exclusion disabled, a retry may hit the same bad upstream; with exclusion enabled, candidates are tried first but exhausted requests may repeat failed channels. Cross-group routing similarly searches untried candidates first only when both cross-group retry and failed-channel avoidance are enabled (`service/channel_select.go:139-150`).
- Failure events already contain actual decision reasons and error origin (`service/request_policy.go:103-132`). The admin log's `admin_info.request_policy` and `use_channel` values can distinguish no retry, repeated same channel, or all candidates failing. The user's client errors alone do not establish which case occurred in production.

## Local authorization boundary

The project rejects the caller's own disallowed/deprecated API-token group in `middleware/auth.go:624-634`, before relay selection. Such a 403 must not enter channel failover. The repository's localized text is `分组 {{.Group}} 已被弃用`, not the user's exact `API Key 所属分组已停用`; the text suggests an upstream-origin error but is not sufficient evidence by itself. Keep error origin and local SkipRetry flags; do not globally retry every 403 based on text/status alone.

## Minimal implementation direction

1. Do not add duplicate 403/404 status rules or broadly remove SkipRetry. Add focused regression evidence through the real upstream HTTP error wrapper, retry decision, and alternate channel selection.
2. Resolve the effective retry budget in the plan. Changing only the retry status list cannot help an installation with RetryTimes=0. Preserve an explicit administrator disable unless the approved product contract changes it; make the timeout/failover UI clearly show when a zero retry budget prevents switching. If auto failover is intended immediately after upgrade, specify migration/default policy separately, rather than silently assuming a positive runtime value.
3. If a hard no-repeat failover contract is approved, keep request-scoped exclusions and return no candidate when all eligible channels are exhausted. Do not mutate persistent channel availability merely because one model disappeared; other models on that channel can still work. Preserve model/group/path/filter constraints, explicit pins, and the already-started response boundary.
4. Remove the 504/524 permanent exclusion as part of the approved timeout work, and include them in defaults; account for existing persisted range strings when deciding upgrade semantics.
5. Obtain an existing affected request's policy decision record before claiming the exact production cause. No credentials or upstream probes are needed to reproduce the code contracts locally.

## Regression locations

- Extend `service/relay_error_test.go:195` (`TestDecideRelayRetryReasons`) using responses parsed by `RelayErrorHandler`: structured 404 model-removed message; structured 403 upstream-group-disabled message; plain-text 403/404; positive/zero budgets; local SkipRetry 403; response already written; pinned/strict session behavior. This can cover parser-to-decision behavior in one existing file.
- Extend the existing `controller/channel_pin_retry_test.go:372` (`TestAffinityFailoverDatabaseMatrix`) for channel A returning the above statuses and eligible B selected under cache enabled/disabled. Existing lines 423-444 verify peer priority and untried-candidate ordering. Add exhausted-candidate behavior only if the no-repeat contract changes.
- `controller/relay_stream_test.go:95` and `:187` contain keepalive retry and fast HTTP failure fixtures relevant to timeout changes. HTTP Responses 403/404 fail before adaptor stream processing, so do not conflate them with a failed event after HTTP 200 streaming has started.

No product code, test files, runtime settings, or git state changed; no tests executed in this research pass.
