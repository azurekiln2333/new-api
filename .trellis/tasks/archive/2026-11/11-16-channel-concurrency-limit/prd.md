# Channel concurrency, response timeout and upstream failover

## Goal

Allow administrators to enforce a shared concurrent-request limit for each channel and configure how long to wait for an upstream response before selecting another eligible channel. Correct failover for upstream timeout, unavailable-model and disabled-upstream-group errors.

## Requirements

- Provide an administrator-configurable concurrency limit for an individual channel; a setting of 2 must prevent a third simultaneous upstream request through that channel. All keys in the same channel share this total, as confirmed by the user ("整个渠道2").
- Treat concurrency as active upstream requests, including the full lifetime of streaming responses, rather than requests per minute.
- Release occupied slots after success, error or cancellation without leaking capacity or allowing overlapping attempts past the limit.
- Preserve existing behavior for channels where no concurrency limit is configured.
- User selected option 1: when a channel is at capacity, try another eligible channel; return HTTP 429 if all eligible channels are at capacity. Do not introduce a waiting queue, and do not classify local saturation as an upstream failure or a reason to disable the channel.
- Add a global upstream-response waiting timeout in seconds. Each channel chooses either inheritance of that global value or an explicit custom value, including zero to disable the additional timeout.
- Bound the wait for the first upstream response-body byte, including a response whose headers arrive but whose body stalls. Once response-body delivery starts, preserve existing stream idle and lifetime controls.
- A timed-out attempt must be canceled before trying another eligible channel, and its concurrency slot must not leak.
- Support retrying upstream HTTP 504/524 instead of unconditionally excluding them. Upstream 403 (for example a disabled upstream key group) and 404 (for example a removed upstream model) must be able to select a different eligible channel when retry policy permits.
- Preserve retry budgets, explicit channel pins, strict sessions and the prohibition on switching after client-visible response output. Local authentication, group authorization and request-validation rejection must not be treated as upstream errors.
- Surface the effective timeout and zero-retry behavior clearly in settings. Do not silently rewrite an administrator's saved policy.
- User requested Pi delegation, approved the final integrated plan and authorized standard Conventional Commits, push, tag and CI/CD on 2026-11-16. Task is in progress; release follows completed verification.

## Acceptance Criteria

- [x] With one channel capped at 2, concurrent clients cannot cause more than two active upstream requests on that channel.
- [x] Streams retain capacity until completion or termination, and subsequent requests can use released capacity.
- [x] Failures, cancellation and retries neither leak capacity nor incorrectly mark a healthy saturated channel unhealthy.
- [x] Limits can be configured through the existing channel administration interface and survive reload.
- [x] With channel A full and eligible channel B available, the request uses B without exceeding A's limit; when all eligible channels are full, return HTTP 429 without waiting or issuing another upstream request.
- [x] Capacity rejection does not disable the channel or increment upstream-failure health state.
- [x] Global timeout changes affect inheriting channels; custom values remain effective after global changes and after save/reload. Inheritance and an explicit custom zero remain distinct.
- [x] A stalled upstream, including headers with no body, is canceled at the configured first-byte timeout and another eligible channel succeeds within the configured retry budget.
- [x] A productive long stream continues beyond the first-byte timeout; cancellation and timeouts cannot settle an empty failed attempt as a successful generation.
- [x] With retry enabled and failed-channel avoidance enabled, controlled upstream 403, 404, 504 and 524 fixtures fail over to a distinct eligible channel; all attempted channels exhausted ends without reselecting a known failed candidate in that request. Explicitly disabled avoidance retains its documented behavior.
- [x] Retry budget zero, strict sessions, fixed-channel routing, local authorization errors and output already started retain their explicit stop boundaries and report the decision reason.
- [x] Configuration round trips are verified against real SQLite, MySQL and PostgreSQL. Shared Redis admission and local admission are verified separately.
- [x] Focused backend/frontend regression checks, independent relaykit build and production frontend build pass before Conventional Commit, push, release tag and CI/CD verification.

## Scope boundaries

- Concurrency counts active gateway-to-upstream attempts, including the full streaming response. It does not count an asynchronous remote job after its submission response completes.
- A shared Redis deployment coordinates admission across nodes; without Redis the documented limit is per gateway process. Counters do not coordinate independently configured channels pointing at the same remote account.
- No waiting queue, automatic deletion of models or credentials, or automatic bypass of local permissions.
- First-byte timeout applies to HTTP relay attempts, including channel-selected provider SDK HTTP clients. WebSocket message/handshake deadlines and remote asynchronous-job execution duration are outside this HTTP timeout feature. Admission coverage of those entry points is recorded separately in the technical design.
- Production policy values and policy-decision logs have not been inspected. The reported 403/404 incident's exact stop reason is not yet established from source alone.

## Evidence and constraints

- Starting commit is 43bf1a563 on custom/rc45-stats with a clean working tree; task targets main.
- Relevant earlier work includes channel failover and request rate limits. Research their contracts before choosing the admission point.
- Initial research is recorded in `research/initial-findings.md`: settings can extend existing channel JSON, but no channel request concurrency semaphore currently exists; occupancy must survive channel cache rebuilds.
- Existing strict sessions, fixed-channel routing and first-output retry boundaries constrain saturation handling. In particular, capacity admission should precede Claude SSE keepalive.
- `setting/operation_setting/status_code_ranges.go:32` permanently excludes 504/524, including when a configured retry range contains them.
- `common/constants.go:145` initializes RetryTimes to zero. `service/relay_error.go:46-79` stops on output, strict session, pin, skip-retry errors, empty attempt budget or retry exclusions. Default ranges already include 403/404; these codes are not themselves a proven incident cause.
- `service/channel_select.go:239-253` can return to random selection after untried candidates are exhausted; the new per-request admission/failure exclusions need an explicit exhaustion outcome.
- Changes affecting storage must respect the project's real SQLite/MySQL/PostgreSQL verification matrix.
- Do not alter protected project identity, unrelated features, or task plugin documentation.
