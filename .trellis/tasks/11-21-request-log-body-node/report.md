# Request body / node diagnostics — pi-logs handoff

## CURRENT STATUS (2026-11-23, latest) — code frozen, accepted for integration

Everything below this section is a **historical checkpoint**; do not read it as
current blockers. Root owns integration, staging, commits, tags, release and CI.
No file was staged or committed by pi-logs.

Sections 84 onward (`Delivered behavior`, `Source ownership / integration`,
`Executed validation`, `Security references and checks`, `Remaining gates`,
`Subsequent integration quality handoff`, `R8 panel 429 backend checkpoint`)
record the state at the time they were written. Where they list a blocker or a
pending run, this top section supersedes them.

### Latest work: channel-copy key-scope fix (production + tests)

- `service/provider_plugin.go` `DropPluginCredentials`: `MultiKeyScopeModes` is
  remapped with the same position shift as `MultiKeyModels`/`MultiKeyUsers` and
  assigned **unconditionally**, so no stale mode survives at a dropped index and
  no surviving key inherits another key's scope.
- `controller/channel.go` `CopyChannel`: clones `MultiKeyScopeModes` with
  `maps.Clone` so the unconditional remap cannot mutate the source channel.
- Proven defect fixed: an explicit empty allowlist on a retained static key
  (index 1 → 0) became unrestricted after copy, i.e. the copied key could serve
  any model; the opposite ordering could deny an unrelated surviving key.
- Tests (existing files only): `TestCopyChannelDropsCredentials` now seeds the
  scope through the public locked mutation `model.MutateChannelKeyEntries`
  instead of a raw `channel_info` update, keeps every prior assertion, and adds
  the empty-allowlist copy case. `TestProviderChannelManagementDatabaseMatrix`
  carries both orderings on real engines.

### Real database matrix (this fix)

SQLite 3.50.4 / MySQL 8.4.13 / PostgreSQL 16.16 against the disposable
`newapi_logs` DSNs: matrix + copy PASS (`ok 3.97s`, per-engine PASS lines), full
granted provider/plugin selector `ok controller 8.826s`, `./service`
`TestDropPluginCredentials`/`TestProvider*` `ok 0.082s`, `go vet` and `gofmt`
clean. Logs: `pi-logs-matrix-3db.log`, `pi-logs-provider-final-3db.log`,
`pi-logs-copy-fix.log`, `pi-logs-service-copy.log`.
Fixture repairs in the same assignment: `setupTaskPluginBindChannelTest`
(`CHANNEL_BUDGET_ENABLED=false`, Option + key/budget tables),
`plugin_native_e2e_test.go` (4 × `&model.Option{}`), `model_management_test.go`
(`&model.ChannelKeyEntry{}`, granted).

### R8 panel 429 backend — closed

Full `./middleware ./router` PASS (6.471s / 9.251s) and focused `-race` PASS
(4.497s / 4.846s) with the real MySQL/PostgreSQL log DSNs; `go vet` exit 0.
Details and commands are in `research/rate-limit-429.md`
("Backend execution results — final").

### Realtime active-ledger guard — closed

`TestChannelBudgetRealtimeRolloutGuard` + HTTPAdmission + ResponsesWSReuse PASS on
SQLite, MySQL 8.4.13 and PostgreSQL 16.16, and PASS under `-race` on SQLite
(`ok 8.828s`, no data race). Durable-active, flag-off, marker-read-error and
unlimited-account cases refuse locally before the upgrade/dial; inactive ledger
keeps the existing session.

### Superseded earlier blockers

- Router "needs login conditionally" failure: superseded by the full
  `./router` PASS above.
- Earlier `controller` build errors in `service/task_billing.go`
  (`ClaimSubmissionChargePublish`, `ApplySubmissionChannelBudgetSettlement`
  arity) and the transient `undefined: math`: peer in-flight edits, all resolved.
- Image keepalive regression (`TestRelayKeepAliveDuringImagePreparation`):
  repaired and passing; keepalive now starts before billing preparation.
- Realtime guard test's earlier SQLite-only PASS: superseded by the 3-engine
  and race runs above.
- Provider/plugin fixture cascade (`no such table: options`,
  `channel_key_entries`): repaired as listed above; the remaining
  `TestProviderRequestsInheritAttemptCancellation` case was reassigned to the
  billing worker, not a diagnostics defect.

### Still true (unchanged limits)

- Retained request-content diagnostics evidence stands: capture, independent
  log-store atomicity/fencing, cleanup and the admin detail API, with SQLite /
  MySQL / PostgreSQL matrices PASS and the request-log race suite PASS.
- **ClickHouse remains unverified locally** (no local ClickHouse service); the
  guarded matrix selector is documented and gated behind CI
  (`TEST_CLICKHOUSE_DSN`, ClickHouse 25.8 job). No ClickHouse or whole-ASVS
  compliance claim is made.
- R8 closes one bounded cross-scope rate-limit bug; the pre-existing absence of
  a per-account password-guess counter remains a separate gap.

---

## Historical checkpoint 1 — original handoff

Implementation and focused verification are ready for integration review. **This
is not whole-task acceptance or a release claim**: local ClickHouse execution is
blocked and the latest full router check has the one peer-owned failure below.
Root owns integration, staging, commits, tags, release, CI and final acceptance.
No files were staged or committed by pi-logs.

## Delivered behavior

- Authenticated POST inference capture freezes original prompt/message fields at
  the first reusable-body read before protocol/plugin replacement. Snapshots own
  their bounded bytes after memory/file storage is replaced or closed. Capture
  preserves roles/text structure, skips unrelated/nested credential metadata,
  masks structured media URLs/inline media and ignores multipart file parts.
- `REQUEST_CONTENT_LOG_ENABLED=true` by default; byte limit defaults to 262144,
  is bounded by the existing body cap and includes escaped UTF-8/JSON overhead.
  Explicit recorded/truncated/no-content/invalid/unsupported/unavailable/disabled/
  storage-failed states remain accurate. Unread rejected bodies are not read.
- Prompt bytes reside only in actual `LOG_DB.log_request_details`. Primary
  protection rows, task private metadata and `other.admin_info` carry lightweight
  provenance/references only. Snapshot payload is excluded from ordinary JSON
  serialization. SQL logging is discarded around sensitive persistence/read I/O.
- `node_name` is the actual processing instance. Ingress `submit_node_name` stays
  distinct through task settlement/refund, including HK submission / US refund.
- Same-store detail/link writes share one transaction. Independent-store writes
  use committed pending phases, holder/generation fencing, renewable ingress
  writer leases and exact remote operation `link_id` recovery evidence.
- Accepted tasks still insert exactly once when optional diagnostics fail before
  insertion; persisted metadata says `storage_failed`. After an authoritative
  insert/commit is attempted, its error is returned without a potentially
  duplicate retry. Primary task state, including `BudgetPending` and unresolved
  refunds, protects the payload; no log-store ledger guesses are used.
- Cleanup is bounded and rotates candidates, rechecks live logs/requests/tasks,
  and delays on storage errors. Last-reference loss denies reads before physical
  reclamation. Unknown link outcomes stay fenced without exact evidence; unknown
  deletes retain retired fences and sweep late payloads. This is conservative
  recovery, not a promise that every uncertain link auto-resolves.
- Admin/root detail API `GET /api/log/request/:ref` enforces existing server auth,
  PAT `log:read`, exact live consume/error/refund association and `no-store`
  before authentication. Malformed/unknown/unlinked/cleaned refs return an empty
  404. Ordinary self/token lists and existing exports never carry the payload.
- Middleware supplements final authenticated rejections only when not already
  logged or explicitly excluded. Root/billing's final-error exclusion markers in
  `controller/relay.go` and `controller/responses_websocket.go` preserve
  `ErrOptionWithNoRecordErrorLog`; a regression first reproduced then verified it.
- `service/relay_error.go` captures the attempt's stable `ChannelAllocation`
  before asynchronous disable work instead of re-reading a recycled Gin context.

## Source ownership / integration

Primary implementation and regression files:

- `common/request_content.go`, `common/gin.go`, `common/body_storage_test.go`
- `model/log_request_detail.go`, `model/log.go`, `model/log_format_test.go`
- `controller/log.go`, `controller/log_export_test.go`,
  `controller/relay_error_log_test.go`
- `middleware/request_log.go`, authenticated relay hook in `middleware/auth.go`,
  Playground hook in `router/relay-router.go`
- `service/relay_error.go`, `service/log_request_content_cleanup.go`, cleanup
  startup in `main.go`

Shared task/route/settlement integration is present in billing/root-owned files;
no wallet/subscription/token charging or pricing logic was changed by pi-logs.
The startup reconciliation call in `main.go` was explicitly requested by billing.

### Proven same-file SQLite alias fix

Review P1 was reproduced through actual `SQL_DSN=local`, `LOG_SQL_DSN=local`,
`InitDB` / `InitLogDB`, real migration twice and detail/log insertion. Before the
fix, `TestLogRequestDetailConfiguredSQLiteAlias` failed after 2.32 seconds with
`storage_failed`: two pools attempted to write the same locked SQLite file.

Root temporarily granted exclusive `InitLogDB` ownership. The minimal patch
reuses `DB` when main is SQLite and log DSN starts with the chooser's `local`
prefix. An unset log DSN also uses the same-store path. MySQL/PostgreSQL primary
with local SQLite logs remains independent. Regression passed after the patch
(0.28 seconds) and in final normal/race selectors. Later ownership transfer back
to root/billing was acknowledged; their other `model/main.go` hunks were kept.

### Additional explicitly assigned fixture repairs

- `controller/model_management_test.go`: create `Option` before follower startup.
- `controller/responses_websocket_test.go`: preseed options using the configured
  SQLite/MySQL/PostgreSQL driver and initialize i18n in shared fixture setup.
- `pkg/perf_metrics/metrics_test.go`: preseed options before follower startup.
- `router/relay_router_test.go`: shared fixture seeds options, creates allocation
  schema, registers failure-safe cleanup and restores prior DB/log handles.
- `router/video_router_test.go`: first independent test reuses that shared
  fixture; second test already did. Original behavior assertions retained.
- `middleware/task_plugin_test.go`: preseed options before actual `InitDB` (which
  also sets reserved-column quoting), create allocation schema, clean handles,
  and isolate external engines with disposable databases because the allocation
  table has a fixed name and ignores a GORM table prefix.
- `middleware/task_plugin_origin_task_test.go`: create options/allocation schema,
  insert fixture channels through real channel insertion and close their pool.

Production fail-closed activation was never weakened to repair a fixture.
Frontend, provider/key controller fixtures and production allocation/charging
logic stay with their owning peers.

## Executed validation

Toolchain: Go **1.26.10**, `GOWORK=off`; Windows amd64. For race checks:
`CGO_ENABLED=1`,
`CC=D:/ProgramFiles/w64devkit-extract/w64devkit/bin/gcc.exe`, compiler directory
prepended to PATH. All output logs listed below live in
`D:/AppData/Local/Temp/new-api-pi-20261121/`.

Configured disposable worker service targets (no passwords):

```powershell
$env:TEST_MYSQL_DSN = 'root@tcp(127.0.0.1:23306)/newapi_logs?charset=utf8mb4&parseTime=True&loc=Local'
$env:TEST_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:25432/newapi_logs?sslmode=disable'
$env:TEST_PERF_MYSQL_DSN = $env:TEST_MYSQL_DSN
$env:TEST_PERF_POSTGRES_DSN = $env:TEST_POSTGRES_DSN
```

### Main diagnostics pass

```text
go test -p 1 -v -count=1 ./common ./model ./controller -run '^(TestRequestContentCapture.*|TestLogRequestDetail.*|TestRequestContentRelayLogLifecycle|TestLogRequestContentAuthorizationAndLinks|TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata)$'
```

**Passed** (`pi-logs-final-focused.log`): common 0.065s, model 15.837s,
controller 5.991s.

Actual log stores: **SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16** (Visual C++
build 1944, 64-bit). Each relational engine executes shared and independently
configured stores, each fresh and released-log upgrade, for **four cases per
engine**. Every case runs actual `migrateLOGDB()` twice. Released `Log` schema/
tags were compared with `custom-20261118-9b21c12`; its representative row and all
prior log indexes survive. This is verification of the changed log schema path,
not an assertion that the entire released product database was imported.
Migration cases create and drop only their own disposable external databases.

Assertions include >64 KiB immutable body, metadata-only reuse, retry deletion
with surviving success, denial before physical cleanup, missing-body refusal,
terminal pending-task protection and **deterministic cleanup during log commit**.
Additional real separate SQLite-file tests cover late exact operation recovery,
older-retry rejection, stale generations, actual remote deletion with lost
acknowledgement, retired late inserts, request-lease release, diagnostic task
fallback and authoritative task insertion failure without duplicate insertion.

### Race pass

```text
go test -race -p 1 -count=1 -v ./model -run '^TestLogRequestDetail'
```

**Passed**, 16.691s (`pi-logs-race.log`), with real MySQL/PostgreSQL configured.
Includes expanded shared/independent fresh/upgrade matrix, interleavings,
configured SQLite alias and independent recovery/task fallback. No race report.
**ClickHouse explicitly skipped** because `TEST_CLICKHOUSE_DSN` is unset locally.

### Final no-record / Responses per-turn pass

After root/billing added both final-error exclusion markers:

```text
go test -p 1 -count=1 -v ./controller -run '^(TestResponsesWSRequestRunnerRequestContentDiagnostics|TestRequestContentRelayLogLifecycle|TestLogRequestContentAuthorizationAndLinks)$'
```

**Passed**, 17.387s (`pi-logs-no-record.log`). Two successful response.create
turns have separate snapshots and exact original text; the excluded failed turn
produces neither an error log nor a stored body. Auth/PAT/association/no-store and
HTTP retry/final-rejection/node cases passed again. Prior to markers, the same
new regression failed with three logs instead of two and a stored excluded body.

### Initial three reserved fixture passes

```text
go test -p 1 -count=1 -v ./controller ./pkg/perf_metrics -run '^(TestModel(PricingConversion|Management|Deletion)DatabaseMatrix|TestVendorManagementDatabaseMatrix|TestSharedModelPluginPricingDatabaseMatrix|TestResponsesWSRequestRunner.*|TestPerformanceAggregationAndFlush)$'
```

**SQLite passed**, including the four existing Responses runner tests
(`pi-logs-fixture-sqlite.log`). A focused first run exposed missing test-local
i18n initialization; adding it to the shared fixture fixed the isolated selector.

```text
go test -p 1 -v -count=1 ./controller ./pkg/perf_metrics -run '^(TestModel(PricingConversion|Management|Deletion)DatabaseMatrix|TestVendorManagementDatabaseMatrix|TestSharedModelPluginPricingDatabaseMatrix|TestPerformanceAggregationAndFlush)$'
```

**All five controller matrix tests and perf aggregation passed SQLite/MySQL/
PostgreSQL** (`pi-logs-fixture-relational.log`). Responses external-engine
fixtures were preserved but not executed by these commands.

### New middleware fixture matrix and full package pass

```text
go test -p 1 -count=1 -v ./middleware -run '^(TestRouteTaskPluginEndpointUsesSelectedChannel|TestDistributeHonorsOriginTaskChannelPin|TestDistributeTokenPinBeatsOriginPin|TestDistributePinViolatingIdentityFilterErrors)$'
```

**Passed**, 24.941s (`pi-logs-middleware-fixture-matrix.log`), real SQLite/MySQL/
PostgreSQL; original routing assertions passed with memory cache both off/on.

```text
go test -p 1 -count=1 ./middleware ./router
```

Latest result (`pi-logs-fixtures-router-middleware-final.log`):

- **middleware passed**, 17.280s, with real MySQL/PostgreSQL DSNs configured.
- **router failed**, 16.282s, **only**
  `TestNeedsLoginCredentialFailsWithoutReachingTheUpstream/auto-disable_true`
  and `/auto-disable_false`, `router/provider_plugin_relay_test.go:621`:
  expected `Plugin credential needs a new login`, actual `All keys are disabled`.
  This peer-owned provider/allocation regression was sent to pi-keys and root;
  its assertions were not weakened. Prior missing-options/allocation fixture and
  temp-handle cleanup failures no longer occur.

### Formatting and static checks

- All pi-logs modified Go files are gofmt-clean (`gofmt -l` returned no paths).
- Targeted `git diff --check` passed, including new production files/spec.
- `go vet ./common ./model ./service ./controller ./middleware ./router ./pkg/perf_metrics`
  **passed**, empty output (`pi-logs-vet.log`).
- No relaykit/tokenkit changes; no independent module API/build claim required.
- No full repository test/build/release claim is made by this worker.

## Security references and checks

Applicable guidance was retrieved/read during implementation:

- OWASP Authentication Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- Session Management:
  https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- Authorization:
  https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html
- Logging:
  https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html
- **ASVS 5.0.0** tagged English CSV, controls reviewed for this scope:
  V8.2.1–V8.2.3, V16.2.1/V16.2.5, V16.4.1/V16.4.2.

Actual regressions reject anonymous/common-owner/model-token/unscoped-PAT reads,
allow authorized admin/root/scoped PAT reads, enforce exact live association and
no-store on denials, and omit credentials/media/private body from ordinary log
metadata. No login/session/credential-flow redesign or whole-ASVS compliance
claim is made. Frontend inert rendering remains pi-ui's verification boundary.

## Remaining gates / operational limits

1. **ClickHouse is unavailable locally** (WSL virtualization blocker). Root added
   a real ClickHouse 25.8 service/DSN to CI, but no executed CI result was supplied
   to this worker. Native TTL, repeated nontransactional/fault writes and mutation
   recovery still need actual ClickHouse verification. Declaration is not proof.
2. Full router run retains the exact peer-owned needs-login failure above.
3. No whole-product latest-release database import/upgrade was performed here;
   the changed log path uses the unchanged released-log schema and verifies
   repeated migration/data/index preservation. Root owns broader schema rollout.
4. Uncertain remote links with no exact evidence deliberately remain fenced;
   expired leases alone never clear them. Retired fences are lightweight durable
   safety metadata rather than a promise of automatic garbage collection.
5. Normal same-store writes use one short primary transaction; independent logs
   use three short primary transaction phases; accepted independent tasks use
   two. Renewals start only after first successful link, every 100s per live ref,
   with bounded release. No latency benchmark or cross-region throughput claim.

Executable contracts are recorded in `.trellis/spec/backend/logging-guidelines.md`
and linked from the existing backend index. Public handoff details are in
`research/implementation-handoff.md`.

## Subsequent integration quality handoff

Root's later read-only remaining-suite assignment is recorded in
`research/integration-quality.md`. It reports 81 root-module package paths outside
controller/model/service/router/middleware: 44 passed, 36 no tests, only
relay/channel graceful GOAWAY failed. HTTP/2 Windows abort failures reproduce on
exact `47a5639c5` baseline; no task-caused fixture edit was made. The image
preparation keepalive target fails in the current tree but passes on baseline,
with its runtime ordering regression handed to root/billing. Independent
relaykit/tokenkit tests/builds and remaining-package vet pass. Exact ClickHouse
25.8 CI job, SHA requirement, guarded subtest selectors and unverified coverage
limits are documented there. No production/fixture/workflow edits or commits
were made for that read-only assignment.

## R8 panel 429 backend checkpoint (2026-11-23)

Root subsequently granted a focused R8 implementation. Saved changes are limited
to `middleware/rate-limit.go`, `middleware/rate_limit_test.go`,
`router/api-router.go` and `router/access_token_scope_test.go`: closed login/
registration/refresh/logout/panel IP scopes, preserved global GA ceiling and
legacy login counts, structured translated generic429 plus Retry-After, and
memory/Redis/actual-route regressions. No raw cookie/header/session identity is
trusted, no anonymous protection is removed, and specific existing429 errors
remain separate. Root/pi-ui own frontend handling and root owns the R8 spec.

Focused middleware/proxy/Redis selectors passed (3.945s). Actual router tests
were blocked before execution by peer billing signatures (`ClaimSubmissionChargePublish`
undefined; `ApplySubmissionChannelBudgetSettlement` return arity mismatch), not
by an observed limiter assertion failure. Root requested pausing router reruns
until explicit compile-ready; full middleware/router, actual aliases, race and
final vet remain pending. No full backend completion/freeze claim at this
checkpoint. Exact commands, evidence, security references, limitations and saved
test boundary are in parent `research/rate-limit-429.md`. Nothing staged or
committed; no database access or `/v1` changes for this R8 patch.
