# Channel failover: local capacity saturation vs upstream HTTP 429

Scope: request-side channel selection, retry decision, per-request exclusions, affinity gates, and the usage-log "Native format" label. Streaming internals and limiter internals are owned by other agents and are not covered here.

Everything under **Known code behavior** is read from the current checkout. Everything under **Unverified deployment state** is an inference about a running instance and was not checked.

## Known code behavior

### Attempt loop and budget

- `controller/relay.go:203-296`: `RetryParam{Retry: 0}`, loop condition `retryParam.GetRetry() <= common.RetryTimes`, one `getChannelWithCapacity` + one attempt per iteration.
- Default budget: `common/constants.go:145` `var RetryTimes = 2`, i.e. three attempts total (initial + 2 retries).
- The retry decision receives the *remaining* budget: `service.DecideRelayRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry())` (`controller/relay.go:288`), and `service/relay_error.go:75-77` stops with `attempt_budget_exhausted` when it reaches 0.
- `AppendUsedChannel` (`controller/relay.go:216`) records every attempted channel, so the retry log line and the consume log's `admin_info.use_channel` trail show real failover.

### First attempt vs retries

- `getChannel` (`controller/relay.go:358-390`) returns the middleware pre-selected channel only when `info.ChannelMeta == nil` **and** `len(SaturatedChannelIDs) == 0`. Any later iteration re-selects through `service.CacheGetRandomSatisfiedChannel`.
- Consequence: a first-attempt capacity rejection flips the request into real re-selection on the same retry index.

### Local capacity saturation

- Gate: the channel must have `MaxConcurrentRequests > 0`. Unlimited channels return a no-op lease before any counter (`service/channel_capacity.go:135-141`), so an unconfigured cap can never produce a local saturation error.
- Errors: `ErrorCodeChannelCapacitySaturated` (local 429) and `ErrorCodeChannelCapacityUnavailable` (503, e.g. Redis coordinator unreachable), both created with `ErrOptionWithSkipRetry` (`service/channel_capacity.go:124-132`).
- `RetryChannelAdmission` (`service/channel_capacity.go:239-264`) is the only traversal path and applies **only** to `ChannelCapacitySaturated`. All of these must hold: request context alive, not pinned (`GetChannelConstraints(...).ResolvedPin()`), no affinity skip (`ShouldSkipRetryAfterChannelAffinityFailure`), `RequestPolicyState.SessionMode != "strict"`, `!c.Writer.Written()`.
- On traversal it appends the channel to `RequestPolicyState.SaturatedChannelIDs` (per-request list), restores the auto-group retry snapshot so a busy candidate does not consume the retry budget, and records a policy event with `ErrorSource: "local"`, `Health: "unchanged"`.
- It never touches `FailedChannelIDs` and never marks the channel unhealthy.
- `ChannelCapacityUnavailable` never traverses: `getChannelWithCapacity` returns it to the client (`controller/relay.go:412-414`).
- No time-based cooldown exists for saturation. The exclusion lives only in `SaturatedChannelIDs` for the current request.

### Upstream HTTP 429

- There is no 429-specific branch in the relay path. The upstream status reaches `service.DecideRelayRetry` (`service/relay_error.go:38-85`) as a normal upstream error.
- Default retry ranges (`setting/operation_setting/status_code_ranges.go:21-27`) are `100-199, 300-399, 401-407, 409-499, 500-599`, so **429 is retryable by default** via `retry_status_matched`.
- Stops that override that: response already started, client cancelled, pinned channel, affinity strict session, `IsAlwaysSkipRetryCode` (`ErrorCodeBadResponseBody`), `IsSkipRetryError`, exhausted budget, non-retryable status.
- `IsChannelFailure` (`service/relay_error.go:22-35`) covers upstream status/transport errors, so a 429 attempt calls `RecordChannelFailure` (`service/relay_error.go:57-59`).

### Failed-channel exclusion (the decisive switch)

- `RecordChannelFailure` (`service/channel_affinity.go:668-686`) always appends the current channel to `RequestPolicyState.FailedChannelIDs`, then separately maintains the affinity failure counter.
- `ChannelSelectionExclusions` (`service/request_policy.go`) = `SaturatedChannelIDs` **always** + `FailedChannelIDs` **only when** `ChannelAffinitySetting.RetryAvoidFailedChannels` is true.
- Code default is `true` (`setting/operation_setting/channel_affinity_setting.go:117-127`, alongside `Enabled: true`, `FailureThreshold: 1`, `SwitchOnSuccess: true`, `KeepOnChannelDisabled: false`, `MaxEntries: 100000`, `DefaultTTLSeconds: 3600`).
- With exclusions present, `selectRetryChannel` (`service/channel_select.go:213-227`) uses `model.GetUntriedSatisfiedChannel` and returns `nil` when every candidate is excluded — **no fallback to the failed channel**. The request then ends as "no available channel", not as a same-channel retry.
- With `RetryAvoidFailedChannels = false`, exclusions hold only locally saturated IDs, so `model.GetRandomSatisfiedChannel` runs (`model/channel_cache.go:294-331`): the retry index picks a descending priority tier (clamped to the last tier) and then weighted-random inside it. The same channel can therefore be picked again, most easily when all candidates share one priority.
- `middleware/distributor.go:149-168` also appends to `FailedChannelIDs`, but only for `ErrorCodeChannelKeyScopeUnsatisfied` candidates during initial selection.

### Affinity gates

- Affinity applies to the first attempt only (`service/channel_select.go:290-340`); retries keep ordinary group routing.
- `ShouldSkipRetryAfterChannelAffinityFailure` returns `meta.SkipRetry`, set from `state.SessionMode == "strict"` (`service/channel_affinity.go:618, 648-663`); a strict binding stops retry with reason `strict_session` (`service/relay_error.go:56-62`).
- `EffectiveSessionMode` (`service/request_policy.go`): `"inherit"` → global `SessionMode` when set, otherwise `"prefer"`; a rule with an empty `SessionMode` and `SkipRetryOnFailure: true` resolves to `"strict"`.
- Default rules are `"codex cli trace"` (`^gpt-.*$`, `/v1/responses`, `SessionMode: "inherit"`, `SkipRetryOnFailure: true`) and `"claude cli trace"` (`^claude-.*$`, `/v1/messages`, same mode). Because the global default `SessionMode` is empty, both resolve to `"prefer"`, so the rule's `SkipRetryOnFailure` does not block retries by default.
- The playground path `/pg/chat/completions` matches neither default rule, so affinity is not even bound for playground requests unless an administrator added a matching rule.
- Pins: `PinRetrySameChannel` (origin task) and `PinRetrySingleAttempt` (token) stop failover both in `DecideRelayRetry` (`pinned_channel`) and in `RetryChannelAdmission`.
- Disable behavior: `ShouldDisableChannel` (`service/channel.go:65-92`) disables on channel-source errors, `AutomaticDisableStatusCodeRanges` (default `{401}`) or disable keywords. A 429 does not disable a channel, so a persistently throttling channel stays in rotation.
- There is no per-channel cooldown state anywhere in the Go code (no `cooldown` identifier). Affinity unbinding after `FailureThreshold` (default 1) affects the *next* request's selection, never the retries of the failing one.

### Missing "Native format" metadata on error logs

- The consume-log path writes the chain: `appendRequestConversionChain` (`service/log_info_generate.go:251-277`), called from `GenerateTextOtherInfo` at `:131`, with the chain seeded by the inbound format (`relay/common/relay_info.go:776-787`, reset per attempt at `:281-290`, appended at `:789-807`).
- The **error-log** path does not: `RecordRelayErrorLog` (`service/relay_error.go:126-160`) writes only `request_path`, `error_type`, `error_code`, `status_code`, then admin info, plugin record, response model and task audit. It never calls `appendRequestConversionChain` or `appendFinalRequestFormat`.
- Frontend rendering (`web/src/features/usage-logs/components/dialogs/details-dialog.tsx:665-677`) treats a chain of length `<= 1` as `t('Native format')`, and the section is shown whenever `request_path` exists.
- Net effect: every failed request — including the 429 and local-saturation rows an administrator would inspect while debugging "stuck on one channel" — claims `Native format` regardless of the real conversion path. This is a metadata gap, not a conversion-detection failure.
- Secondary risk in the same label: `AppendRequestConversion` seeds an empty chain with the *target* format (`relay/common/relay_info.go:798-801`), so a genuinely converted single hop would also render as `Native format` if the inbound seed were ever missing (`relay_info.go:780-783` no-ops when `RelayFormat == ""`).
- `/v1/messages` → [OI] is instrumented: `relay/channel/openai/adaptor.go:73` → `service/request_converter.go:26-32` → `relaykit/relayconvert/request_registry.go:281-283`, plus the handler net at `relay/claude_handler.go:99` via `relay/common/request_conversion.go:19-26`. Only the passthrough branch (`claude_handler.go:91-98`) records nothing, which is accurate.

### Exact existing tests

- `service/channel_capacity_test.go:152` `TestChannelCapacityAdmissionRestrictions` — gates for ordinary busy candidate (traverses), same-channel task pin, single-attempt token pin, strict session, response started, client cancelled; asserts a candidate is not traversed twice, `FailedChannelIDs` stays empty, and `ChannelCapacityUnavailable` never traverses.
- `service/channel_capacity_test.go` also holds local lifecycle, coordination-failure/health, detached-submission and real-Redis (`TEST_REDIS_ADDR`) cases.
- `controller/channel_pin_retry_test.go:35` `TestRequestPolicyUpstreamHTTPFailover` — failover to a second channel for 404/403/504/524/first-body-byte timeout, plus exhaustion, saved-zero budget, pinned channel and strict session. It sets retry codes to `401-407,500-599`, so **429 is intentionally not covered**.
- `service/relay_error_test.go` `TestDecideRelayRetryReasons` — decision reason coverage; `ShouldRetryRelayErrorHonorsChannelPinOnChannelError`, `ShouldDisableChannelIgnoresLocalErrors`, `RequestPolicyEventsReachLogAdminInfo`.
- `service/channel_select_auto_groups_test.go` and `router/provider_plugin_relay_test.go` toggle `RetryAvoidFailedChannels = false`, which is the closest existing coverage of same-channel reuse.
- Gaps: no test for 429 failover under the default ranges; no test asserting a failed channel is not re-picked when avoidance is on versus allowed when it is off; no test for capacity saturation followed by an upstream 429 on the next candidate; no test for `request_conversion` on the error-log path.

## Unverified deployment state

Not checked, and each can independently produce "stuck on the same channel":

- Whether the live `channel_affinity_setting` option has `retry_avoid_failed_channels` true. The code default is true, but the value is loaded from the options store, and a stored document that omits the key can leave the field false.
- Whether the live global `SessionMode` or a custom affinity rule resolves to `strict` for the playground path.
- Whether `RetryTimes` was changed from 2, and whether `AutomaticRetryStatusCodeRanges` still contains 409-499.
- Whether the affected model has only one eligible channel in the requested group, in which case the exclusion logic correctly returns "no available channel" instead of retrying elsewhere.
- Whether any adaptor converts this upstream's 429 into a skip-retry error; no such mapping was found in the shared relay path, but per-channel adaptors were not audited.

## Proposal (planning only, no implementation)

1. Error-log metadata: add the conversion chain to `RecordRelayErrorLog` by reusing `appendRequestConversionChain` and `appendFinalRequestFormat`, so failed rows carry the same `request_conversion` / `claude` fields as consume rows. This is the smallest fix for the misleading `Native format` label and needs no frontend change.
2. Distinguish "unknown" from "native" in `details-dialog.tsx`: render nothing, or an explicit unknown state, when `request_conversion` is absent, instead of `t('Native format')`.
3. Make the 429 path observable before changing routing: assert in tests that a 429 under the default ranges fails over, that the failed channel is excluded while `RetryAvoidFailedChannels` is true, and that it may be re-picked when false. Extend `TestRequestPolicyUpstreamHTTPFailover` rather than adding a new file, and cover the capacity-then-429 sequence in `channel_capacity_test.go`.
4. Do not add a time-based cooldown in this task. Nothing in the current design provides one, and introducing one changes routing semantics for every channel; the reported symptom is explained by the exclusion switch and single-channel groups.
5. Verify the deployment questions above from the running instance's options before concluding the code is at fault.
