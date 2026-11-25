# CI wiring — pi-web queue #10 refresh

Current owner: pi-web (`w5D:p6`), continuing the previous pi-ci selector work below. Owns `.github/workflows/ci.yml`; preserves all earlier uncommitted edits. Finalization waits for items 1–9 (especially R17's in-flight tests).
Rules: NO local tests/builds/vet/lint/gofmt/typecheck/bun/go/node/DB probes. No git stage/commit/push.
Checkpointed after each step.

## Step 1 — selector harvest (DONE)

The backend job ("Test root, relaykit, and tokenkit modules", `make test`) runs the **entire**
root/relaykit/tokenkit module, so every new or changed Go test file executes there regardless of
selector. The integration job runs `./model ./service ./controller` in full, but reaches `./middleware`,
`./router` and `./service/passkey` only through the narrow `-run` filters below — that is where missing
selectors actually hide tests.

| Worker | Tests | Package | DB | CI path | Status |
| --- | --- | --- | --- | --- | --- |
| pi-keys | `TestChannelSearchTagModePagesInSQL`, `TestChannelSearchPageSizeIsCapped` | controller | yes | "Verify bounded channel reads…" (`TestChannelSearch` prefix) | covered |
| pi-keys | `TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot` | controller | yes | same step (`TestChannelBoundIndex` prefix) | covered |
| pi-keys | `TestManagedKeyAllocationPublishesOnlyChangedChannel` | controller | yes | "Verify allocation identity…" (added) | added |
| pi-keys | R14 subscription balance-redemption projection (`GetSubscriptionBalanceUsedQuota`, user-list aggregate) — **no test functions exist in the tree yet** | model/controller | yes | full `./model ./controller` package run in the integration step | watch: add the real test names when written |
| pi-logs | `TestRankingModelUsageRequiresModelAndHonoursModulePolicy` | router | no | race step (`TestRankingModelUsage` prefix) | covered |
| pi-logs | `TestStatusInstances*` | router | no | race step prefix | covered |
| pi-logs | `TestOptionBatchAccessTokenScopes`, `TestAccessTokenRouteRulesCoverEveryDashboardRoute` | router | no | race step (`TestOptionBatch`, `TestAccessTokenRouteRules`) | added |
| pi-logs R16 | `TestUpdateOptionPersistsAcrossDialects`, `TestUpdateOptionReportsWriteFailure`, `TestUpdateOptionsBulkIsAtomic`, `TestOrdinaryOptionsBatch*` | model | yes (loops all 3 engines via `TEST_MYSQL_DSN`/`TEST_POSTGRES_DSN`) | new step "Verify option batch writes…" | added |
| pi-logs R16 | `TestUpdateOptionRejectsNegativeClaudeDefaultMaxTokens`, `TestUpdateOptionRejectsInvalidGeminiSafetyThreshold`, `TestUpdateOptionsBatch*` | controller | yes | same step, `TEST_SECURITY_DIALECT=sqlite|mysql|postgres` | added |
| pi-logs R16 | `web/src/features/system-settings/**` vitest files | web | n/a | frontend `Test` step (`bun run test`, whole suite) | covered |
| pi-billing | `TestManagementMutationOutboxDatabaseMatrix`, `TestConcurrentCodeBatchSubmitCollapsesToOneBatch`, `TestAuditOutboxReplayAfterLostAcknowledgement`, `TestAuditOutboxClickHouseDelivery`, `TestCodeOperationRecoveryRequiresAuthorization`, `TestCodeRequestDigestIsUnambiguous`, `TestCodeExportContractsAcrossDatabase`, `TestRecordManagementAuditFallsBackWithSameEventIdentity` | model | yes | "Verify management operation migrations…" exact set + race step | covered |
| pi-billing | `TestManagementCodeCreateReplaySurvivesExpiry`, `TestInvitationCreationValidationAndSafeAudit`, `TestRedemptionBatch` | controller | yes | "Verify code creation replay…" three-engine loop | covered |
| pi-billing / pi-fwd-test / pi-fwd-auth | `TestConfigureTrustedProxies*`, `TestLoadConsoleForwardConfig*`, `TestVerifiedConsoleIngress*`, `TestEffectiveRequestScheme*`, `TestConsoleIngress*`, `TestConsoleForwarder*`, `TestSessionCookieOriginGuard*`, `TestPasskeyOrigin*` | middleware, service/passkey | no (in-process httptest) | new step "Check console forwarding, ingress proof and origin guards" (`./middleware ./service/passkey ./router`, `-race`) | added |
| pi-fwd-sync | master-process-local route list / channel-disable socket close | controller, service | partial | full package runs in the integration step | covered |
| pi-ui / pi-drawers / pi-keys / pi-logs | all `web/src/**/__tests__/*.test.tsx`, plus hosted format/typecheck/lint/build | web | n/a | frontend job | covered |

pi-ui Go side (`service/channel_fingerprint*.go`, controller fingerprint tests) is covered by the existing
`TestFingerprintDiagnostic` / `TestFingerprintResponsesOnlyChannelProbe` selectors and package-wide runs.

## Step 2 — ci.yml edits (DONE)

Job structure, step names other than the new one, services and env are unchanged. Four minimal edits:

1. "Verify allocation identity and quota management on MySQL and PostgreSQL": added
   `TestManagedKeyAllocation` (anchored alternation now `^(TestChannelAllocations|TestMultiKeyThreeRowModelScopeEditUsesFreshRevisions|TestManagedKeyAllocation)`).
2. New step "Verify option batch writes and management settings on SQLite, MySQL and PostgreSQL"
   (placed after it, before the budget race step): the R16 model selectors run once (they loop all three
   engines themselves and skip without real DSNs), the controller selectors loop
   `TEST_SECURITY_DIALECT=sqlite|mysql|postgres`.
3. "Check declared text endpoints and administrator channel boundaries": added
   `TestOptionBatch|TestAccessTokenRouteRules` to the `./relay ./router` race selector.
4. New step "Check console forwarding, ingress proof and origin guards": `./middleware ./service/passkey ./router`
   with `-race`, selector
   `TestConfigureTrustedProxies|TestLoadConsoleForwardConfig|TestVerifiedConsoleIngress|TestEffectiveRequestScheme|TestConsoleIngress|TestConsoleForwarder|TestSessionCookieOriginGuard|TestPasskeyOrigin`.
   Verified every listed test function exists in the current tree; no t.Parallel fixtures, so one process is safe.

Rejected: an earlier draft selected `TestUserListSubscriptionBalance`, which exists nowhere in the tree —
removed rather than left as an always-empty selector.

## Refresh checkpoint 1 — pi-web owns queue #10 (2026-11-25)
- Queue #4 implementation handed off in `11-24-channel-loading-save-performance/research/r18-tooltips.md`; #10 is now CLAIMED by pi-web, with **finalization after items 1–9**. Other OPEN items were already claimed by the new workers, so no ownership taken from peers.
- Read the complete existing workflow and its uncommitted diff before any edit. Preserve earlier formatter-diagnostics change and all prior selector additions.
- Confirmed frontend `Test` executes the entire Vitest suite; changed TS/TSX files already get hosted lint/format/typecheck/build. R18 shared/storage/retry tests need no selective test-run workaround, but should join the explicit updated-console lint list for traceability.
- Remaining selector harvest is source-only: read final named tests and fixture environment variables, extend narrow middleware/router/race/three-dialect selectors where new tests would otherwise be skipped, and record coverage per queue item. Do not invent nonexistent test names or assert matrices that fixtures skip.
- R18 tests: new `web/src/components/ui/__tests__/tooltip-contrast.test.tsx`, extended `web/src/features/system-info/__tests__/system-instances-panel.test.tsx`, extended `web/src/features/usage-logs/components/__tests__/mobile-card.test.tsx`.
- All local executable validation remains forbidden and unrun. Pending: ask current owners for final paths/names + env needs; inspect Makefile and backend integration fixtures; edit workflow only after actual names exist; re-harvest once queue 1–9 closes.

## Refresh checkpoint 2 — owner handoffs / fixture audit
- `Makefile:test` enumerates every root subpackage (main package excluded for embed) and independently tests relaykit/tokenkit; Vitest includes all `src/**/*.{test,spec}.{ts,tsx}`. Broad suite coverage is preserved, not replaced with selected tests.
- #1/#5 forwarding: existing prefixes cover FINAL core handoff and planned additions (`TestConfigureTrustedProxies|TestLoadConsoleForwardConfig|TestVerifiedConsoleIngress|TestEffectiveRequestScheme|TestConsoleIngress|TestConsoleForwarder|TestSessionCookieOriginGuard`, plus `./service/passkey` `TestPasskeyOrigin*`). No DB env; `-race -p 1 -count=1` with CGO; globals forbid parallel fixture execution. Await #5 final additions.
- #2 capacity handoff: add `TestChannelCapacityFailover*` to per-turn MySQL/PostgreSQL selector (responses primary/log DSNs), explicitly exercise SQLite too; `TestChannelCapacity*` Redis tests already covered with `TEST_REDIS_ADDR`. Add existing extended `TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups` to admission race selection. New failover test name not yet in tree at this checkpoint; wait before wiring it.
- #3 R20 FINAL selector plan: all `controller/invitation_test.go` `TestInvitation*` under `TEST_SECURITY_DIALECT=sqlite|mysql|postgres` with existing engine DSNs; extend current code-creation matrix selector rather than duplicate the creation tests. Add focused `TestInvitationAtomicRegistration|TestInvitationAffiliate` race step. New frontend `auth/__tests__/basic-auth-settings.test.tsx` still being completed.
- #6 R17 in progress; await exact cleanup/storage test names and ClickHouse fixtures. Routed locale ownership question to pi-auth; pi-web has no locale writes.
- #7 R19 current setting tests `TestUpdateSource*`/`TestValidateUpdateSourceRepository` are included in broad root run; frontend release/update-checking tests in full Vitest. Await final handoff.
- #8/#9 owners requested final test paths; no reply yet. No workflow edits by pi-web or local executable validation so far.

## Refresh checkpoint 3 — initial workflow refresh applied (not final)
- After source names existed, expanded current invitation/code replay matrix to `TestInvitation*` across SQLite/MySQL/PostgreSQL and added `TestInvitationAtomicRegistration|TestInvitationAffiliate` CGO race step, `-p 1 -count=1`.
- Expanded responses goal/waiting step to explicit SQLite + MySQL + PostgreSQL and added actual `TestChannelCapacityFailover`; added `RetryAvoidsFailedChannelsAcrossPrioritiesAndGroups` to existing admission race selector. Redis env stays job-wide.
- Added final R13/R15 `users/.../permissions.test.tsx`, R18 storage panel test, R20 basic-auth test, and R19 releases/update-checking tests to explicit console lint list. The shared UI tooltip file/test are covered by full Vitest + TS + formatting (UI path is ignored by repository oxlint config, so do not pretend explicit linting there runs).
- #8 FINAL: only permissions.test.tsx extended, no Go/DB/fixture changes; corrected handoff explicitly removed the closed-inert case, which is not individually selected anywhere.
- #1 FINAL prefixes unchanged; #5 awaiting final test additions. #3 backend prefixes and frontend test file now exist. #2 failover prefix exists. #6 still implementing; do not mark #10 DONE before final R17 fixture names exist.
- Locale ownership repeatedly clarified to peers: pi-auth owns sanctioned insertion script/seven locales and is coordinating R19/R17/forwarding error keys; pi-web has no locale writes.
- Read full billing rules as source-only CI harvest reaches capacity/budget accounting fixtures; no billing code or assertions modified. No local validation/parser/test/build/lint/format command run. All earlier workflow edits remain intact.

## Refresh checkpoint 4 — R17 fixture contract, capacity final
- #2 FINAL acknowledged: unchanged `TestChannelWait*` must rerun for queue lease changes (already in admission race), new `TestChannelCapacityFailover` + `TestChannelCapacity*` Redis + extended auto-group test all covered by current selectors and responses 3-DB loop.
- R17 owner fixture contract received, implementation still in flight: `model/log_format_test.go` new `TestLogRequestDetailRetentionDatabaseMatrix` uses existing `runRequestLogStorageMatrix`: temp SQLite; loopback `TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, `TEST_CLICKHOUSE_DSN`, create/drop-database privilege; relational shared/independent + fresh/released upgrade; ClickHouse independent store with SQLite primary. Existing CI root/admin loopback services satisfy those fixture inputs.
- Planned R17 actual names (not yet in tree when searched): service `TestSystemTaskLogRetention`, router `TestPersonalAccessTokenLogStorageScope`; common existing `TestRequestContentCapture*`, model existing `TestOrdinaryOptionsBatch*`, frontend existing maintenance log-settings test extended. Will add names only once source exists, preserving full integration and log-content race matrix.
- No workflow/run/tool installation or validation locally. #10 remains CLAIMED/finalize last; #1/#3/#5/#6/#7/#9 are still in flight in queue, #8 DONE.

## Refresh checkpoint 5 — R20 final confirmed; R18 cross-review resolved
- #3 final source manifest confirmed by pi-auth: existing `controller/invitation_test.go` `TestInvitation*` (including login-only refusal, callback-bound server payload, custom OAuth/WeChat) plus new `auth/__tests__/basic-auth-settings.test.tsx`, already covered by applied workflow matrix/race/full frontend suite. No new backend test file/env beyond the documented security dialect fixture.
- Added existing R19 update-source form + R17 log-maintenance form/test to explicit console lint list; full changed-file checks and full Vitest remain authoritative.
- Existing service system-task tests use package SQLite `TestMain` (not a hidden per-dialect service switch). R17 model storage matrix, not that service fixture, provides MySQL/PostgreSQL/ClickHouse coverage. Do not claim a cross-engine scheduler test based on setting an ignored env var.
- R18 source-test review evidence: installed Testing Library `getNodeText` reads direct text; `within(popup)` correctly finds descendant `kbd`. No unsupported test rewrites. Header, path and popup-token assertions present; runtime results still pending cloud.
- Still pending final source names/handshake for R17 and finished queue status, plus #5 final additions. No local validation run.

## Refresh checkpoint 6 — ready except in-flight R17/final handshakes
- Explicit R19 `./setting/system_setting -run 'TestUpdateSource|TestValidateUpdateSourceRepository'` step added after source confirmed every prefix; no DB env needed (tests isolate environment via `t.Setenv`).
- Final matrix so far: #1/#5 trust/forwarding/origin/passkey all existing race prefixes; #2 capacity/wait/failover/auto-group race plus explicit response-store 3-DB run; #3 invitation full 3-DB plus atomic/affiliate race; #4 full frontend tooltip/storage/retry tests; #7 setting + full frontend release/update tests; #8 full frontend permission/quota tests; #9 full package/full Vitest plus earlier focused corrected regressions.
- Remaining R17 named tests still absent from source at last harvest: `TestLogRequestDetailRetentionDatabaseMatrix`, `TestSystemTaskLogRetention`, `TestPersonalAccessTokenLogStorageScope`. Do not silently insert selectors matching no tests. Required inputs and intended existing fixtures are recorded in checkpoint 4. #10 stays CLAIMED/finalize last until these exist and owner confirms final.
- Final integration service images already declared: MySQL 8.4, PostgreSQL 16, Redis 7.4, ClickHouse 25.8. Workflow reports actual engine/server versions; SQLite fixtures log `sqlite_version()`. **No actual versions/results are available before hosted run.** Root must retain their exact emitted versions and commands; pinned image names alone are not completed verification.
- Public source reads, read-only git diff and task/peer coordination only. No local tests/builds/vet/lint/typecheck/format/Go/Bun/Node/DB probes/parser validation. No git write/stage/commit/push. Root owns final source freeze/CI execution/publication.

## Refresh checkpoint 7 — R19 FINAL manifest applied
- R19 owner confirmed final unchanged paths: `setting/system_setting/update_source_test.go` (five `TestUpdateSource*`/`TestValidateUpdateSourceRepository` cases), frontend `system-update/__tests__/{releases.test.ts,update-checking.test.tsx}`. No DB/Redis/network/extra fixture env; Go isolates `NEW_API_UPDATE_SOURCE_REPOSITORY` with `t.Setenv`, frontend fetch/API calls mocked.
- Added `src/features/system-update/store.ts` to explicit console lint list as requested; maintenance update-checker form was already added. Repository snapshot isolation/custom-tag ordering regressions remain in full Vitest suite.
- Re-read live workflow diff, queue and CI report before editing; all earlier updates preserved. R17 named tests still not in source at this checkpoint; final focused retention selectors remain blocked on owner writes rather than inserting no-op selectors. No local validation run.

## Refresh checkpoint 8 — R17 actual selectors wired
- Source now contains all announced R17 tests: `model/log_format_test.go:TestLogRequestDetailRetentionDatabaseMatrix`, `service/system_task_test.go:TestSystemTaskLogRetention`, `router/access_token_scope_test.go:TestPersonalAccessTokenLogStorageScope`, and `common/body_storage_test.go:TestRequestContentCapturePersistedSettingsAndEnvironment` (existing capture prefix). Read fixtures to confirm inputs before editing workflow.
- Added explicit model retention matrix step using existing real SQLite/MySQL/PostgreSQL/ClickHouse fixtures and job DSNs. Added CGO race step over `./common ./service ./router` for capture settings, scheduler and root/PAT storage-access contract. `-p 1 -count=1` retained; no pretend parallelism of process-wide fixtures.
- Existing model `TestLogRequestDetail` race selector already matches the new retention test and runs against the same actual log stores, while broad integration/full backend suites remain. Existing `TestOrdinaryOptionsBatch` matrix picks up added log-setting validation cases.
- Retention test currently iterates fresh/upgrade fixture labels, but hosted results and exact coverage must come from the owner/CI, not this selector handoff. Authoritative released startup migration step and existing storage matrix are preserved.
- Finalization pending #1/#5/#6/#9 owner DONE/FINAL confirmations and any last source changes. #2/#3/#4/#7/#8 DONE. No local validation run.

## Refresh checkpoint 9 — full current source inventory harvested
- Re-read full workflow diff and all tracked/untracked changed test paths; current new files are model option-upsert, passkey origin, update-source settings, shared tooltip contrast, and basic-auth settings tests. All are reached by broad Go/Vitest suites and the relevant targeted steps. No new isolated middleware/router test file was missed.
- #5 final `research/fwd-test.md` now records every added transport case and confirms unchanged forwarding prefixes; current source includes pooled replay, cancellation, multipart streaming, master limits/failures, provenance logging, hop cleanup, and panic/truncation cases. Existing console race selector covers all; no new DB envs.
- #3 queue DONE confirmed; #7 DONE confirmed. Remaining queue #1/#5/#6/#9 still not all marked DONE at this checkpoint; selector changes are ready for final re-harvest after their last edits. R17's announced actual tests are wired, with model retention matrices and scheduler/PAT/capture race coverage.
- Source review observation sent to R17 owner: `upgrade` parameter was logged but not branched in the new retention test at harvest, so its labels alone cannot prove a released-schema upgrade; keep released migration/storage fixtures authoritative and report precise limitations or seed baseline. No peer tests changed by pi-web.
- Latest diff is source-only, no local validation and no git publication. #10 stays CLAIMED/finalize-last, not a passing hosted result.

## Refresh checkpoint 10 — all per-item test manifests FINAL
- R17 owner confirmed FINAL test selectors and no new fixture envs: model retention matrix, common persisted-capture/environment case, SQLite-only service scheduler, router storage PAT scope, existing option-upsert invalid-member validation, and extended maintenance frontend test. Applied workflow already covers them all; no additional edit required after checkpoint 8.
- Queue #2/#3/#4/#7/#8 are DONE. #1/#5/#6/#9 still await their own report/locale/source-review closeout; #10 remains finalize-last, even though every announced test selector is now saved. No false DONE or hosted pass claim.
- No OPEN item available to claim. Next action on resume: read queue/progress + workflow diff again; if 1–9 are DONE (or root explicitly authorizes final selector freeze), harvest any last new test names, add only missing actual selectors, write final per-item coverage table, mark #10 DONE and notify root/patch owner. Existing owned source/CI edits are preserved.

## Refresh checkpoint 11 — forwarding FINAL acknowledged
- #5 owner explicitly confirmed FINAL only `middleware/trusted_proxies_test.go`, same six prefixes, self-managed `t.Setenv`/cleanup, no new DSNs/services and no `t.Parallel`; saved console race step already covers all tests. Final fixture doc read back and tracked/untracked test inventory re-harvested; no missing test path discovered.
- #10 selector coverage is ready as listed below. Queue still has #1/#5/#6/#9 closeouts in flight; final marker waits for their DONE status/root freeze, not any unrun local command.

### Selector-ready per-item coverage (hosted results pending)
| Queue item | Final test coverage in workflow | Fixture requirements |
|---|---|---|
| #1 + #5 forwarding | Broad backend suite; explicit `./middleware ./service/passkey ./router` CGO race selector `TestConfigureTrustedProxies|TestLoadConsoleForwardConfig|TestVerifiedConsoleIngress|TestEffectiveRequestScheme|TestConsoleIngress|TestConsoleForwarder|TestSessionCookieOriginGuard|TestPasskeyOrigin` | Loopback httptest servers; test-owned role/config/env, no external DB |
| #2 capacity | `TestChannelCapacityFailover` in explicit responses SQLite/MySQL/PostgreSQL loop; admission race includes `ChannelCapacity|ChannelWait|RetryAvoidsFailedChannelsAcrossPrioritiesAndGroups`; broad backend/integration suites | Empty responses DSNs = SQLite; `TEST_RESPONSES_SQL_DSN` + `TEST_RESPONSES_LOG_SQL_DSN` for other engines; job `TEST_REDIS_ADDR` |
| #3 invitation policy | All `TestInvitation*` in security SQLite/MySQL/PostgreSQL loop; atomic/affiliate CGO race; full Vitest basic-auth settings case | `TEST_SECURITY_DIALECT`, existing MySQL/PostgreSQL DSNs; frontend jsdom |
| #4 tooltip contrast | Full Vitest discovers new shared tooltip and extended existing system-info/mobile-card tests; full TS/format gates | jsdom; source-paired palette checks, no invented computed-color browser result |
| #6 retention | Focused `TestLogRequestDetailRetentionDatabaseMatrix`; existing log-detail CGO race includes it; common capture/service scheduler/router storage scope CGO race; ordinary options validation matrix; full maintenance Vitest | Temp SQLite + relational shared/independent + separate ClickHouse matrix; loopback admin DSNs for create/drop databases; scheduler fixture is SQLite-only |
| #7 update source | Explicit `./setting/system_setting` `TestUpdateSource|TestValidateUpdateSourceRepository`; full frontend releases/update-checking suite | Test-owned env, frontend fetch/API mocks, no live GitHub/DB |
| #8 drawer/quota | Full Vitest `users/components/__tests__/permissions.test.tsx` and existing sibling tests; explicit console lint list | Query client/auth store fixtures; no new backend/DB fixture |
| #9 existing findings | Full Go and Vitest suites, changed-file formatting/lint/TS/build and preserved focused tests for earlier regressions | Queue owner confirmed DONE, no new tests or selectors |

## Refresh checkpoint 12 — only R17 closeout remains
- Queue now records #1 and #5 forwarding DONE with final source reports; #9 DONE explicitly says no new tests or selectors. Existing saved workflow covers them all, no further change required.
- #6 R17 alone remains CLAIMED while locale integration/source review finishes. Its final test manifest is already wired; wait for its DONE or root's explicit final freeze before marking #10.
- No local validation or Git writes run. No OPEN queue item to claim; root and R17 owner have complete selector/fixture handoff above.

## Refresh checkpoint 13 — hotfix round-one follow-up harvested
- Capacity owner reported an additional snapshot retry case inside existing `TestChannelCapacityFailover` and root-authorized cleanup synchronization in existing `controller/relay_task_plugin_test.go:TestLedgerTaskSubmissionCommitOutcomes`. Both names exist in current source; neither adds a new prefix or fixture env.
- No workflow change needed: explicit response-store SQLite/MySQL/PostgreSQL selection includes `TestChannelCapacityFailover`; existing task submission matrix includes `TestLedgerTaskSubmission` on MySQL/PostgreSQL, broad suites provide SQLite, and admission race already includes `LedgerTaskSubmission`. The owner's optional separate three-dialect ledger race was not added redundantly; current standard matrix plus existing race gate remain intact.
- This is a source handoff only. pi-web did not run/read a new hosted result or local command and does not claim the hotfix round passes. Root owns isolated hotfix publication and separate channel/test commits.
- #6 R17 remains the only preceding queue item not DONE; #10 remains CLAIMED/finalize-last with every announced actual test selector covered.

## Refresh checkpoint 14 — root-authorized plugin-login flake (separate item)
- While #10 waits for R17 closeout, root assigned a test-only redirect BroadcastChannel scheduling flake. `web/src/features/channels/components/__tests__/plugin-login-dialog.test.tsx` now awaits the exact `onDone` one-call expectation with existing Testing Library `waitFor` after sender cleanup; visible success, real messages and channel mismatch input preserved. No production/hook/auth changes, sleeps, widened timeouts or weakened assertion.
- Root requested separate commit item and progress under `11-24-dashboard-node-ranking-details/research/r17-retention.md` heading **Flaky plugin-login test**; source review/handoff saved there. Full Vitest already discovers this existing test, and its path now joins explicit console lint list. No additional test prefix/env/DB needed.
- No local validation or Git mutations; side-item source delivered to root. #10 remains finalize-last pending R17 DONE, with all current actual selectors saved.

## Previous worker's post-push triage note

Blocked on root's push. When root says the run has started I will read run `fix/console-responsiveness-20261124`
jobs, annotations and step logs on `azurekiln2333/new-api` (public GitHub API via curl), map each failure to
`file:line` + owning worker per `dispatch.md`/`research/ci-wiring.md`, and send each owner one precise herdr note.
