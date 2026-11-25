
## 8. Implementation step — recheck candidates after queue failure

- Added request-capacity exclusions as a distinct list and taught channel selection to honor them. Source re-read validated the field references; the initial fast-path also avoids skipping exclusions.
- Preparing a bounded single direct admission recheck after queue full/timeout/unavailable: only for unpinned/non-strict requests with active context/uncommitted response. It will clear then restore prior capacity rejections while the selection API applies the same model/group/filter/key rules; it won't re-enter waiting, and keeps prior rejected channels excluded after a different channel admits. If none admits, it returns the original queue error.
- Review found probe renewal must not piggyback the capacity lease deadline: added `probeExpiry` tracking to leases, and queued Redis grant initialization now records both TTLs. Probe renewal uses its own margin/timeout, logs only its channel/error class, and only confirmed renewals extend expiry. Existing grant from local queue has no Redis probe and is unaffected.
- Fixed a discovered incorrect receiver reference in Redis acquisition error logging (`channelID` is the acquisition argument); no tests or tools that execute code were run.

## 9. Resume checkpoint (pi-cap, item #2)

- Re-read `AGENTS.md`, parent PRD, queue and current diffs; item #2 remains owned by pi-cap. No validation commands executed; no git writes.
- Existing progress is incomplete: `controller/relay.go` lost the closing braces and `channelWaitSelection` declaration; acquisition references an undefined cleanup timeout. The queue-failure recheck currently misses the normal selection-exhaustion path. Renewal uses a separate probe deadline but must bound both commands by the earliest live lease deadline, including retry sleep.
- Scope: finish only capacity admission/failover and lease safety in `service/channel_capacity.go`, `service/channel_wait.go`, `service/request_policy.go`, `service/channel_select.go`, `controller/relay.go`, existing capacity/wait/relay tests, Redis pool initialization and redacted log translation. Preserve all unrelated worker changes; no DB/schema/billing changes. All executable checks remain GitHub-only.
- Next: trace existing selectors, queue grants and tests, repair admission traversal using the normal selection API without retry-budget increments, then add focused regressions and self-contained cloud handoff.

## 10. Admission/renewal implementation checkpoint

- Repaired incomplete relay declaration and cleanup timeout. Capacity-unavailable now excludes only the failed channel, traverses normal eligible candidates, leaves health/upstream retry budget untouched, preserves pins/strict sessions/cancel/output stops and returns the original local capacity error when no candidate admits.
- Centralized the queue exit in `getChannelWithCapacity`: one bounded final normal selection pass after queue-full/timeout/unavailable; request exclusions restored on exit and no second queue/deadline reset. Initial middleware shortcut is now first-attempt-only so a recheck cannot reuse stale settings.
- Confirmed lease/probe expiry is measured from command start, never response receipt. Both renewal commands and the retry timer use the earliest confirmed ownership deadline minus a safety margin; unknown renewals never extend it, closed clients stop immediately. Redis probe Lua refuses to resurrect expired ownership.
- Redis pool no longer forces ten connections when unset; retains explicit URL sizing/env override. No tests/build/format/DB commands run. Next: focused real Redis renewal fault fixtures and actual routing regressions, then final source review and report.

## 11. Regression fixture checkpoint

- Extended existing `TestChannelCapacityAdmissionRestrictions` across saturated/unavailable/queue-full/wait-timeout with each pin/strict/output/cancel boundary and upstream-budget assertion.
- Added real-Redis transient/lost-reply renewal, confirmed recovery, pre-expiry stop and expired-probe cases under `TestChannelCapacityRedis`; no sleeps or synthetic performance claims. Added controller `TestChannelCapacityFailover` cache on/off actual selection scenarios including zero-budget A→B, all-unavailable error retention and pin/strict/goal/budget/failed exclusions.
- Sent CI fixture/selector handoff to pi-web (`w5D:p6`); it will add explicit SQLite/MySQL/PostgreSQL selector plus existing real-Redis race coverage. New log includes only channel ID/error class, with Chinese server-log translation.
- Next: finish auto-group unavailable fixture, exercise queue-failure recheck deterministically, final source review, report exact files/commands still pending hosted validation.

## 12. Production hotfix audit checkpoint

- `fork/main` resolves to `53a71f88d5b568847cd7233a08f934824dcb22d3`. Read-only `git diff fork/main HEAD --` is empty for all ten item #2 source/test paths and the existing routing/WS test fixture; they are identical on the released base and current branch HEAD. No cherry-pick of `9fb3d5b0f..9329486b8` is needed for these paths.
- Added real queue-deadline regression in the existing controller test: expired queue with a subsequently released slot succeeds via one final normal pass; still-busy exhaustion returns original timeout; a pin never reselects. Existing cache on/off coverage applies. No sleeps or local execution.
- Auto-group fixture now runs saturated and coordinator-unavailable cases for retry budgets 0 and 2, proving same-group traversal, then later group, without an upstream retry increment.
- Root notified source extraction appears independent; final Hotfix manifest, exact changed hunks and hosted selectors follow after final read-only diff review. No locale JSON/script touched; no stage/commit/push.

## Final implementation result — item #2

Source and regression fixtures are ready for integration. **No test, build, vet, lint, gofmt, formatter, typecheck, Go/Bun/Node command or database probe was run locally. Nothing was staged, committed or pushed. Hosted verification is still pending; this is not a claim that CI passed or production is fixed.**

### What failed / what changed

- Released code returned immediately for Redis coordination errors; only a saturated channel could be traversed. A healthy alternate therefore was never considered after `channel_capacity_unavailable`. The new request-local `CapacityRejectedChannelIDs` excludes every rejected capacity candidate independently of ordinary upstream failed-channel avoidance. The existing selector still enforces groups, models, filters, keys and budgets; pins/strict sessions/client cancellation/committed output remain stop boundaries. No upstream retry is spent and no channel health failure is recorded.
- When all candidates are limited and coordination cannot be confirmed, admission remains fail-closed with the actual capacity error. **There is no process-local fallback when Redis is configured, no automatic limit disable, and no route around a pin.** Unlimited channels retain their existing best-effort telemetry behavior; they are not made unlimited by this fix.
- After queue full/timeout/unavailable, an otherwise retryable request gets one final normal selection/admission pass. Earlier capacity exclusions are temporarily released and restored on exit; upstream-busy/budget/failed exclusions remain. The pass never waits again or resets the queue's fixed deadline. An explicit queue-exit recheck flag prevents stale settings reuse during that pass without changing preparation-retry snapshot behavior (corrected in Hotfix CI round 1 below).
- Redis pool initialization no longer overwrites URL/default sizing with a hard ten connections when `REDIS_POOL_SIZE` is unset. The library's CPU-scaled default applies; explicit URL sizing and env override remain. The pinned go-redis v8.11.5 implementation was read to confirm defaults and context-deadline behavior. This is a source-proven saturation risk, **not a measured diagnosis of the live production Redis failure**.
- Limited admission/renewal uses bounded `CHANNEL_CAPACITY_ADMISSION_TIMEOUT_MS` (default 3000, clamped 250..10000); ambiguous admission cleanup has an independent 250ms budget. Owner/probe deadlines are conservative command-start + 30s, not response-time + 30s. A transient renewal error can retry only before the last confirmed ownership deadline minus a 1s margin; neither an error nor a lost reply extends that deadline. Missing/expired owners and closed clients still stop immediately. Both renewal commands and the timer honor the earlier capacity/probe deadline. Expired Redis probe ownership cannot be resurrected by renewal.
- Rate-limited coordinator warnings contain only channel ID and one error class (`timeout`, `pool_timeout`, `network`, `redis_server`, `redis_command`); no raw Redis error, endpoint, password, command arguments, key or owner token is logged. Added the corresponding Chinese server-log translation. Existing client-facing messages/translations are unchanged.

### Focused regression fixtures (written, not executed)

- `service/channel_capacity_test.go`: existing `TestChannelCapacityAdmissionRestrictions` covers all four capacity codes, pins, strict sessions, cancellation, committed output, repeat exclusion and unchanged retry budget. Existing `TestChannelCapacityRedis` now also covers transient command loss, a write with lost reply, confirmed renewal recovery, stop before TTL reuse, successor-safe cleanup, an earlier probe deadline and expired probe Lua refusal. These use real Redis for ownership and a deterministic fault hook; no timing comparisons/stress loops/sleeps.
- `service/channel_select_auto_groups_test.go`: existing `TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups` now runs saturated and unavailable cases at retry budgets 0 and 2, checking same-group peers/lower priority before later groups and unchanged spent retries/affinity offset.
- `controller/channel_pin_retry_test.go`: added `TestChannelCapacityFailover` with cache on/off: real A→B selection at retry budget 0, unavailable→unlimited alternate, all-limited error retention, pin/strict/goal/budget/failed exclusions, failed-avoidance off, expired queue followed by a released slot, still-busy original timeout, and pinned timeout without reselection. Uses the existing real SQLite/MySQL/PostgreSQL fixture. Existing `TestChannelWaitRouting` remains unchanged and must rerun for queued grants, policy revalidation, no billing before admission, one productive settlement and cancellation cleanup.
- Source reviewed changed call sites, lease initializations (direct + queued), Redis Lua ownership, retry/exclusion flow, test setup/cleanup and baseline dependencies. Root/continuous Go reviewer and GitHub Actions remain the independent review/validation gate.

## Hotfix

### Can it ship alone on released fork/main?

**HOTFIX-READY for isolated source extraction; release remains blocked on hosted checks of the exact extracted SHA.** Baseline checked: `fork/main = 53a71f88d5b568847cd7233a08f934824dcb22d3`. Read-only `git diff fork/main HEAD -- <the ten paths below>` produced no output: the released files and branch HEAD are identical for every hotfix path. The prerequisites (request-policy exclusions, auto-group retry snapshots, capacity/wait coordinator, goal/pin restrictions, existing SQLite/MySQL/PostgreSQL test fixture, DTO/error IDs, logger/LogText and go-redis version) already exist on fork/main. **No commit from `9fb3d5b0f..9329486b8`, no other worker's uncommitted change, no schema/dependency migration and no frontend/locale file is required.**

The branch-only channel read/batch-projection edits in `model/channel.go`, `model/channel_key_entry.go`/cache and management APIs are not dependencies: this fix calls pre-existing APIs and does not call the new projection APIs. Do not include those unrelated edits. The new controller fixture uses the existing `setupResponsesWSRequestTest` from `controller/responses_websocket_test.go`, which is unchanged between fork/main and branch HEAD; do not extract that file. `service/channel_wait_test.go` is unchanged and is a validation input, not a patch path.

### Exact source/test manifest and hunk ownership

All current differences against fork/main in these ten files belong to #2; extract them together. Symbol/hunk boundaries are authoritative if line numbers move:

| File | Exact #2 hunks |
| --- | --- |
| `common/redis.go` | `InitRedisClient`: replace forced `PoolSize=10` default with conditional explicit env override, retaining URL/library sizing |
| `common/log_text.zh-CN.json` | Only the added `channel capacity Redis coordination failed: channel_id=%d error_class=%s` English-key/Chinese-value entry |
| `controller/relay.go` | `getChannel` capacity-exclusion/explicit final-recheck guard (round 1 replaces the original overly broad first-attempt guard); all changes to `getChannelWithCapacity` (error traversal, one queue-exit recheck and exclusion restoration); no other relay/billing changes |
| `controller/channel_pin_retry_test.go` | Added `relaycommon` import plus the complete new `TestChannelCapacityFailover`; existing tests unchanged |
| `service/channel_capacity.go` | Added `net`/`logger` imports; admission timeout/cleanup/safety/log constants and error-class/rate-limit helpers; lease expiry/probe/retry atomics; `acquireRedisChannelCapacity`, new `renewalDeadline`/`retryRenewal`, `renew`/`maintain`, `RetryChannelAdmission`; no unlimited telemetry/billing arithmetic changes |
| `service/channel_capacity_test.go` | Added atomic import; expanded `TestChannelCapacityAdmissionRestrictions`; fault-hook type/interface methods; two added real-Redis renewal/probe subtests inside `TestChannelCapacityRedis` |
| `service/channel_select.go` | Single `SelectChannelForRequest` guard addition for `CapacityRejectedChannelIDs` |
| `service/channel_select_auto_groups_test.go` | Existing `TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups` capacity table now covers saturated + unavailable; associated indentation/assertion wording only |
| `service/channel_wait.go` | `dispatchRedisLocked` pipeline-start timestamp and capacity/probe deadline initialization; `channelWaitProbeRenewScript` expired `probe_until` guard |
| `service/request_policy.go` | `RequestPolicyState.CapacityRejectedChannelIDs` plus alignment, and inclusion in `ChannelSelectionExclusions` |

Bookkeeping output is this file and the single item #2 `DONE` queue cell; it is not needed in a production binary. No CI YAML file was edited by pi-cap. The current shared `.github/workflows/ci.yml` includes many unrelated integration changes; **do not copy it wholesale for a hotfix**. Retain the released workflow and add only the scoped selectors/trigger needed below (pi-web owns shared CI wiring).

Reproducible read-only extraction inspection (not executable validation):

```text
git diff fork/main HEAD -- common/redis.go common/log_text.zh-CN.json controller/relay.go controller/channel_pin_retry_test.go service/channel_capacity.go service/channel_capacity_test.go service/channel_select.go service/channel_select_auto_groups_test.go service/channel_wait.go service/request_policy.go
git diff fork/main -- common/redis.go common/log_text.zh-CN.json controller/relay.go controller/channel_pin_retry_test.go service/channel_capacity.go service/channel_capacity_test.go service/channel_select.go service/channel_select_auto_groups_test.go service/channel_wait.go service/request_policy.go
```

First command is empty at handoff; second is the isolated #2 source/test patch. Root owns extracting, committing and publishing it. Keep old and new nodes under the existing distributed Redis protocol; no key/DTO/DB format change. Rolling back requires reverting this coherent manifest, not disabling configured concurrency limits.

### Exact GitHub-only CI selectors and fixture requirements

1. Real Redis + race, with `TEST_REDIS_ADDR=127.0.0.1:6379`, `CGO_ENABLED=1`, `GOWORK=off`:

```sh
go test -race -p 1 -count=1 -v ./service ./controller -run '^(TestChannelCapacity.*|TestChannelWait.*|TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups)$'
```

2. Explicit SQLite/MySQL/PostgreSQL routing matrix (real engines; `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN` from existing Actions services; empty DSN is the existing real SQLite fixture). Run sequentially because fixtures are process-global:

```sh
for DSN in "" "$TEST_MYSQL_DSN" "$TEST_POSTGRES_DSN"; do
  TEST_RESPONSES_SQL_DSN="$DSN" TEST_RESPONSES_LOG_SQL_DSN="$DSN" go test -p 1 -count=1 -v ./controller -run '^(TestChannelCapacityFailover|TestChannelWaitRouting|TestRequestPolicyUpstreamHTTPFailover)$'
done
```

3. Keep existing root-module vet/build/test and hosted gofmt/format gate for the exact extracted SHA. No new relaykit/tokenkit APIs, packages, dependencies or schema were changed. No ClickHouse or new authentication/middleware/router/passkey tests are required by #2 itself; this is not R17.

Pi-web acknowledged FINAL selector handoff and explicitly added SQLite to the shared responses matrix plus the existing `ChannelCapacity|ChannelWait|RetryAvoidsFailedChannelsAcrossPrioritiesAndGroups` race selection. The released workflow already has Redis + three-engine services and a broad race regex matching the new `TestChannelCapacityFailover`; the explicit matrix selector still needs this new test name. Released workflow push trigger only includes `merge/upstream`: root must use its supported PR/workflow-call path or a narrowly added hotfix trigger, not assume an arbitrary branch push runs CI.

## Hotfix CI round 1

- Follow-up status (remote read): root re-synced/published PR #1 head `9a49c461c9339ec863a93d09b5acddd64eee32b6`; [Actions rerun 45580193625](https://github.com/azurekiln2333/new-api/actions/runs/45580193625) currently has backend, frontend and integration jobs in progress. No result claimed yet; pi-cap did not publish or execute local checks.

### Investigation checkpoint

- Remote evidence: run `45575163448`, PR #1 head `6c11f3bb621d3b2565d145dbd49d6bfe3c61d8a9`; backend vet/build/tests and frontend checks passed, integration failed only at `Check admission, waiting, streaming and timeout races`.
- Read failed logs through the authenticated GitHub CLI and check-run `139071180675` annotations (one generic exit-code annotation, `.github:2649`). Full log shows exactly two failing tests: `TestRelayKeepAliveDuringImagePreparation` (unexpected EOF following nil-pointer panic) and `TestLedgerTaskSubmissionCommitOutcomes/known_rollback_refunds_and_releases` (race on fixture-global Redis flag versus async quota-cache update). Service capacity/wait race package passed.
- Comparing both failure call paths against released baseline before attributing. No code changed at the initial investigation checkpoint; no local validation or git writes.

### Fix checkpoint (two separately scoped commits)

1. Confirmed #2 regression: the added `state.Attempts == 0` middleware shortcut guard incorrectly treated every pre-upstream preparation retry as a fresh-selection requirement. Replaced it with an explicit `recheckCapacity` argument used only for the final queue-exit recheck. Added the pre-`ChannelMeta` preparation-retry case to existing `TestChannelCapacityFailover`.
2. Confirmed pre-existing race: `newLedgerSubmissionFixture` teardown restored `common.RedisEnabled` while `IncreaseUserQuota`'s nested gopool cache task still read it. Root explicitly authorized one extra test-only path, `controller/relay_task_plugin_test.go`. Re-read `.agents/rules/billing.md` in full before editing; added a bounded default-pool drain before fixture-global restoration. Production/model/billing code and charge/refund behavior remain untouched.

### Final attribution and separate fixes

Evidence: [Actions run 45575163448](https://github.com/azurekiln2333/new-api/actions/runs/45575163448), head `6c11f3bb621d3b2565d145dbd49d6bfe3c61d8a9`; failed integration job/check-run `139071180675`. Read `gh run view ... --log-failed`, the job log and `gh api repos/azurekiln2333/new-api/check-runs/139071180675/annotations`. The annotation is only `Process completed with exit code 1` at `.github:2649`; detailed attribution comes from the log, not an invented annotation. Exactly two test failures / one race report were present. Backend vet/build/test and frontend jobs passed; the service race package passed (`service 2.461s`), while the controller race package failed. Nonfatal fixture `system_tasks`-missing logs are not extra failed tests.

| Failure | Attribution and exact cause | Fix / regression |
| --- | --- | --- |
| `TestRelayKeepAliveDuringImagePreparation`, `controller/relay_stream_test.go:291`: unexpected EOF after image-fetch 502 and nil-DB panic | **Caused by #2.** Image preparation can fail before `InitChannelMeta` initializes metadata. The initial #2 `state.Attempts == 0` guard then forced every subsequent preparation attempt through DB selection, whereas the released code reused the middleware-selected snapshot while `ChannelMeta` was nil. This existing snapshot-only test deliberately has no channel DB fixture. Log stack: `model.GetChannel` `ability.go:116` → `selectRetryChannel` → `getChannel` `relay.go:471` → `getChannelWithCapacity` `relay.go:509`. A plain attempt counter was not a valid proxy for a final capacity recheck. | `controller/relay.go:453-458`: pass explicit `recheckCapacity bool`, preserving the snapshot for ordinary pre-metadata preparation retries; `controller/relay.go:512`: set it only while `waitFailure != nil` for the one final queue-exit pass. `controller/channel_pin_retry_test.go:504-527`: added a compact pre-metadata retry regression under existing `TestChannelCapacityFailover`, with nonzero attempt/retry counts and no channel table; asserts the same selected channel, active lease and unchanged upstream retry count. Existing final-queue-recheck cases still enforce fresh reselection. |
| `TestLedgerTaskSubmissionCommitOutcomes/known_rollback_refunds_and_releases`: race detected | **Pre-existing on released main.** Race write was the fixture restoring `common.RedisEnabled` at original `controller/relay_task_plugin_test.go:827`; read was `model.cacheIncrUserQuota` `user_cache.go:160`, spawned inside `IncreaseUserQuota` at original `user.go:1360-1361`, itself spawned by the asynchronous `BillingSession.Refund` worker. Waiting until user/token SQL balances were restored did not wait for that nested cache task. Read-only `git diff 53a71f88d 6c11f3bb6 -- controller/relay_task_plugin_test.go service/task_billing.go service/budget_attempt.go service/billing_session.go service/funding_source.go model/user.go model/user_cache.go` is empty. The ledger fixture locks its channel and bypasses capacity admission; no new #2 capacity code caused this race. | Root expressly authorized one additional **test-only** path. `controller/relay_task_plugin_test.go:27`: import existing dependency `gopool`; `controller/relay_task_plugin_test.go:826-831`: bounded 5s completion wait for `gopool.WorkerCount() == 0` before restoring globals, DB handles and database types. Read pinned gopool v0.1.3 source: parent workers remain counted while scheduling child tasks, and decrement only after returning and finding the queue empty; zero synchronizes after nested cache work, unlike polling persisted balances. The default-pool wait is restricted to this nonparallel fixture cleanup and fails visibly if work does not drain. |

**Billing gate:** re-read `.agents/rules/billing.md` in full before the authorized ledger-fixture edit. **No production/model/billing code, quota arithmetic, refund timing/amount, settlement rule or existing accounting assertion was changed for the pre-existing race.** The only production edit this round is the scoped channel snapshot/recheck predicate fix. No plugin API/runtime was changed. No `go.mod`/`go.sum` change: gopool is already a production dependency.

### Commit / re-sync boundary for root

- `fix(channel)` follow-up: take only `git diff 6c11f3bb6 -- controller/relay.go controller/channel_pin_retry_test.go` (explicit recheck flag + one regression subtest).
- Separate `test(relay)` follow-up: take only `git diff 6c11f3bb6 -- controller/relay_task_plugin_test.go` (one import + six-line cleanup synchronization). This is the **single authorized eleventh path** beyond the original ten-file hotfix manifest, not a new production dependency.
- All edits are in `D:\My2KProjects\new-api`; hotfix worktree left untouched for root to re-sync. No stage/commit/push/checkout/reset/stash or other git write was performed. No peer changes were reverted. Source diff readback confirmed no other round-1 source paths changed by pi-cap.

### Hosted rerun selectors (not executed locally)

Keep all existing CI selectors and add/retain these focused checks against the exact re-synced SHA:

```sh
go test -race -p 1 -count=1 -v ./controller -run '^(TestChannelCapacityFailover|TestRelayKeepAliveDuringImagePreparation|TestLedgerTaskSubmissionCommitOutcomes)$'
go test -race -p 1 -count=1 -v ./service ./controller -run '^(TestChannelCapacity.*|TestChannelWait.*|TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups)$'
for DIALECT in sqlite mysql postgres; do
  TEST_TASK_DB_DIALECT="$DIALECT" go test -race -p 1 -count=1 -v ./controller -run '^TestLedgerTaskSubmissionCommitOutcomes$'
done
```

Fixtures: real Redis `TEST_REDIS_ADDR`; MySQL/PostgreSQL use existing `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`; default or `TEST_TASK_DB_DIALECT=sqlite` is real SQLite. Keep the earlier explicit `TEST_RESPONSES_SQL_DSN`/`TEST_RESPONSES_LOG_SQL_DSN` three-engine routing matrix and complete existing admission/streaming race step. No new test prefix: the new snapshot case is inside `TestChannelCapacityFailover`, and the fixture fix is covered by existing `TestLedgerTaskSubmission*` tests. No formatter or executable validation has been run locally. The original run reported SQLite 3.50.4, MySQL 8.4.13 and Redis 7.4.12 in viewed logs; this follow-up has **no hosted result yet**, so success remains unverified until the rerun.

### Prevention / source review

Root cause was an overbroad proxy condition: request-policy attempt count records selections, not whether metadata exists or whether the queue requested a final refresh. The explicit local flag now expresses exactly the latter. Regression coverage directly checks both snapshot reuse and final-capacity fresh selection. The unrelated fixture race was a completion-boundary error: committed accounting values do not imply all asynchronous cache tasks are done. Teardown now waits on the actual worker lifecycle before mutating shared flags. Scope remains the two original #2 files plus the one expressly authorized test fixture; no spec/production/billing scope expansion was made.

## Hotfix CI round 2

### Investigation checkpoint

- Read remote run `45580193625` failed logs and annotations (`139088006104` integration, `139088006149` backend). Annotation requests initially returned no entries; a paginated re-read returned one generic process-exit annotation for each check (`.github:21488` exit 1; `.github:5764` exit 2). Both failing steps report only `relay_task_plugin_test.go:830` cleanup condition timeouts, across every fixture using the round-1 default-pool drain. No capacity selection/renewal or snapshot regression assertion failed in those logs.
- Attribution: **caused by the round-1 test-only follow-up**, not a pre-existing flake. Waiting for the process-wide default pool to reach zero accidentally waits for unrelated long-running workers present in the full suite. A focused race subset can lack those workers, so the original source review was insufficient. Do not extend the timeout or remove race coverage.
- `9a49c461c` matches the current worktree on the eleven hotfix source/test paths. Re-read `.agents/rules/billing.md` in full before revising the already-authorized `controller/relay_task_plugin_test.go` fixture. Scope remains test-only completion synchronization; production/model/billing behavior untouched.
- Next: isolate the actual fixture's asynchronous refund/cache completion rather than drain the entire shared executor, source-review that boundary, then document each failing test and exact hosted rerun selectors. No local executable validation or git writes.

### Implementation checkpoint

- Removed the global `gopool.WorkerCount()==0` dependency from `newLedgerSubmissionFixture`, returning other ledger scenarios to their original Redis-disabled fixture. Source identified a concrete non-terminating unrelated worker: `service/notify-limit.go:31-45` starts its hourly cleanup inside the shared default gopool, so a full-suite-wide zero count is not a valid completion barrier.
- Only the rollback-refund subcase now opens an isolated miniredis test cache **after real DB pre-consumption**, enables its go-redis client, and observes the actual positive 300-quota user/token cache Lua command completions via a test hook. No fake billing worker or production seam: real `BillingSession.Refund`, `IncreaseUserQuota`, token refund and SQL transactions still execute. Empty cache returns the ordinary guarded-cache-miss result, never a fabricated accounting write.
- The case waits for both atomic completion flags plus the original real user/token balances before resetting `model.DB` in the test body; cleanup repeats the same fixture-specific barrier before restoring `common.RDB` / `RedisEnabled` or closing the cache. This orders the shared-global writes after the actual cache reads without requiring unrelated worker termination. Replaced duplicate SQL-balance polling with exact assertions after this barrier.
- All round-2 code is confined to the already-authorized `controller/relay_task_plugin_test.go`; no additional production/model/billing/spec/CI/locale edits. Source readback complete; hosted rerun remains pending.

### Final failure classification

Both failed jobs ([run 45580193625](https://github.com/azurekiln2333/new-api/actions/runs/45580193625), head `9a49c461c9339ec863a93d09b5acddd64eee32b6`) have the **same 14 failing fixture instances** below. Each is caused by my round-1 **test-only global-pool drain**, not a newly observed production capacity error and not a pre-existing flake. There are 17 `--- FAIL` lines per job because parent table-test failures are also printed. No independent failing assertion was found:

| Failed test / subcases | Cause | Round-2 fix |
| --- | --- | --- |
| `TestLedgerTaskSubmissionChargesUpwardActual` | Shared fixture cleanup waited on unrelated permanent default-pool work | Remove global drain in `newLedgerSubmissionFixture`; preserve original exact ledger/accounting assertions |
| `TestLedgerTaskSubmissionRecordsSubscriptionOverage` | Same cleanup timeout | Same; production subscription/billing behavior unchanged |
| `TestLedgerTaskSubmissionCommitOutcomes/{committed_with_lost_acknowledgement,committed_with_unreadable_outcome,known_rollback_refunds_and_releases}` | Same cleanup timeout; the rollback subcase also needs the original race's **actual** nested cache completion synchronized | Remove shared drain; rollback alone uses `ledgerRefundCacheCompletion` and isolated miniredis to observe both refund cache calls plus committed SQL balances before shared globals are restored |
| `TestLedgerTaskSubmissionOutcomeIdentity` | Same cleanup timeout | Remove shared drain; outcome-identity assertions unchanged |
| `TestLedgerTaskSubmissionUsesCurrentTokenCredential/{deleted=false,deleted=true}` | Same cleanup timeout | Remove shared drain; credential/current-token assertions unchanged |
| `TestLedgerTaskSubmissionFinalizeIsReplaySafe` | Same cleanup timeout | Remove shared drain; exactly-once finalization assertions unchanged |
| `TestLegacyTaskSubmissionKeepsSessionPath` | Same cleanup timeout | Remove shared drain; legacy session assertions unchanged |
| `TestLedgerTaskSubmissionSnapshotStates/{nil,disabled,recorded,storage-failed}` | Same cleanup timeout | Remove shared drain; content durability/charge assertions unchanged |

The round-1 channel fix is confirmed by the integration log in this run: `TestChannelCapacityFailover` including all snapshot/cache/queue cases passed (8.14s), and `TestRelayKeepAliveDuringImagePreparation` passed (2.01s). The subsequent race/matrix steps were skipped after the early fixture failures, so this is **not** evidence that the complete hotfix passed. Main's last green release run `45230816749` on unchanged base `53a71f88d` includes successful backend and full database integration jobs; it is consistent with the new test drain causing this regression, not a timing flake to rerun without a fix.

### Root re-sync and cloud validation

- Take only `git diff 9a49c461c -- controller/relay_task_plugin_test.go` for round 2. The file swaps the gopool import for already-existing miniredis/go-redis test dependencies, removes the default-pool wait, and adds scoped command-completion tracking only in the real rollback-refund regression. Exact locations after final source readback: `newLedgerSubmissionFixture` restored at `:814-832`; observer `ledgerRefundCacheCompletion` and hook callbacks at `:1034-1069`; bounded per-fixture SQL/cache completion wait at `:1072-1085`; rollback-only client/hook and LIFO cleanup at `:1126-1146`; barrier before DB-handle restoration at `:1147-1150`; existing rollback balance assertions at `:1175-1177`. No module/dependency-file change.
- Full `.agents/rules/billing.md` read was repeated in round 2. **Billing production code, arithmetic, pre-consume, durable charge, refund amount/timing/idempotency, token semantics and schema are untouched.** This is test fixture synchronization only; all existing business assertions remain, with the rollback balance check made exact after the stronger completion barrier.
- Run the full existing controller/root suite as well as focused race tests in GitHub; testing only a narrow subset is precisely what could miss the process-wide-pool problem. Retain existing full CI and add/retain, with real external engines configured:

```sh
go test -p 1 -count=1 -v ./controller
go test -race -p 1 -count=1 -v ./controller -run '^(TestLedgerTaskSubmission.*|TestLegacyTaskSubmissionKeepsSessionPath|TestChannelCapacityFailover|TestRelayKeepAliveDuringImagePreparation)$'
for DIALECT in sqlite mysql postgres; do
  TEST_TASK_DB_DIALECT="$DIALECT" go test -race -p 1 -count=1 -v ./controller -run '^(TestLedgerTaskSubmission.*|TestLegacyTaskSubmissionKeepsSessionPath)$'
done
```

Existing `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN` supply real databases; SQLite is the existing in-memory real database fixture. The cache completion observer uses isolated miniredis only for test scheduling; existing real Redis admission/renewal tests still require `TEST_REDIS_ADDR` and must remain. No new prefix, new DB schema, executable local check, git write or direct deploy. **No round-2 follow-up CI result is claimed yet.**

### Retrospective correction

The round-1 claim that shared-pool zero was a useful fixture barrier was incomplete: zero would imply finished work, but unrelated infinite tasks mean it is not reachable in the full suite. The fix is not a longer wait, relaxed assertion or race exclusion. It binds completion to this fixture's observable cache operations and real refund balances. Future test teardown must observe owned asynchronous effects rather than drain a process-wide executor containing background services.

### Remaining evidence / rollout caution

Actual hosted run URLs, exact engine versions, formatter results and test results are **pending root/Actions**, not inferred from source review. No production Redis pool/timeout/network/server-error class has yet been observed in this session. The warning will distinguish those after rollout. A real coordinator outage still properly rejects all limited candidates; this fix avoids a needless failure when another eligible channel can admit and tolerates only bounded transient renewal loss, never a guessed lease extension. No direct production deployment was performed.
