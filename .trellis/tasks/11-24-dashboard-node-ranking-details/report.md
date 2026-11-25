# 11-24-dashboard-node-ranking-details — pi-logs report

Owner: pi-logs (`w4C:p11`). Parent: `.trellis/tasks/11-24-console-responsiveness-diagnostics`.
Branch: `fix/console-responsiveness-20261124`. Status: **source frozen**, pending root CI.

## Scope delivered

### R7 — instance resources on the dashboard API cards

- **Public DTO** (`model/instance_resource.go`) is allowlist-only:
  `key`, `name`, `status`, `domains`, `resources{cpu_percent,memory_percent,storage_percent}`,
  `observed_at`, `stale`, `disabled`, plus envelope `generated_at` and a top-level
  `monitoring: enabled|disabled|unknown`.
- **Top-level monitoring** aggregates the sampling nodes' own recorded state:
  `unknown` when no instance recorded a state, `disabled` if any reporting instance
  had monitoring off, otherwise `enabled`. The reading process's configuration is
  never consulted.
- **Per-resource validity** comes from the sampler itself
  (`common/system_monitor.go` `CPUSampled`/`MemorySampled`/`SampledEnabled`,
  `service/system_instance.go` `Available` per resource + `Monitoring` pointer):
  explicit `available:false` → unknown; `available:true` keeps a legitimate `0%`;
  legacy payloads without the flag fall back to positive-only; storage can be known
  while CPU/memory are unknown.
- **Identity safety**: no node name, hostname, stored node address/URL, sync error,
  path, pid or OS string can reach the public payload. `domains`/`name` come only
  from configured console API settings (already public via `/api/status`).
  `node_name` is stripped from the public `/api/status` projection
  (`console_setting.GetPublicApiInfo`).
- **Multi-domain → one instance** preserved; the group key is the smallest persistent
  configured entry id, so it survives reordering.
- **UI**: `api-info-panel.tsx` / `api-info-item.tsx` show CPU/memory/storage for all
  signed-in users on a wrapping second row that stays visible on narrow screens,
  with explicit `Unknown` / `Stale` / `Disabled` states, and open a filtered node
  detail. Detail selection is a positional UI identity and the instance is derived on
  every render, so the drawer follows the 30s refetch instead of freezing a snapshot.
- **Settings**: `api-info-section.tsx` binds each entry to an instance via an
  optional `node_name`, supports per-entry reordering, and preserves ids. Legacy rows
  saved before ids existed receive **distinct** draft ids above the current maximum at
  parse time and are persisted on save, so one edit/delete/reorder cannot corrupt
  several rows.

### R8 — homepage avatar and admin onboarding

- Signed-in avatar (desktop and mobile) in `public-header.tsx` links straight to
  `/dashboard`; the console menu is unchanged.
- Admin overview onboarding is hidden in all entry/reminder states
  (`overview-dashboard.tsx` `showSetupGuide = !isAdmin`); the installer is preserved.

### R9 — model usage drawer from the ranking list

- Clicking a model in the leaderboard or in the movers/droppers list opens the model
  usage drawer (`entity-links.tsx` `onOpenModel`, wired through
  `model-leaderboard.tsx`, `models-section.tsx`, `pulse-section.tsx`, `index.tsx`).
  It never navigates to pricing.
- Server masking (`model/ranking_viewer.go`): admins see every live name; ordinary
  viewers see only their own; anonymous viewers see none. Masked rows emit the fixed
  placeholder `***` and omit `user_id` entirely. **A deleted account keeps only the
  generic label `Deleted user` and no id**, even for an admin.
- Totals aggregate **all** users for the model, independent of the bounded
  leaderboard (`GetModelUsageTotals`), so a popular model is not undercounted.
- `Cache-Control: no-store` on the viewer-scoped response; query key includes viewer
  identity and role.
- Quota is rendered with the shared currency formatter in both the totals and each
  ranked user row (tokens/requests/quota), never with the token-count formatter.

## Route contract

- `GET /api/rankings/model-usage` → `middleware.DisableCache(), middleware.HeaderNavModulePublicOrUserAuth("rankings"), controller.GetRankingModelUsage`.
  Module disabled ⇒ anonymous is challenged (401), authenticated viewers keep working.
- `GET /api/status/instances` → `middleware.DisableCache(), middleware.UserAuth(), controller.GetInstanceResourceSummary`.
- `GET /api/status` keeps using `GetPublicApiInfo` (no `node_name`).
- The existing root-only `/api/system-info/instances` is untouched and remains the
  full node API.

## Tests written (never executed locally — root runs CI)

- `model/instance_resource_test.go` — public JSON allowlist (exact key sets, no node
  identity leakage), legitimate 0%, legacy positive-only fallback, storage-known
  while CPU/memory unknown, stale, multi-domain, unbound, sampling-node monitoring
  state, plus ranking masking including the deleted account carrying no id.
- `router/access_token_scope_test.go` (extended, existing fixture with real
  `SetApiRouter`) — ranking masking per viewer with `no-store`, required `model` /
  period validation, disabled-module 401-vs-200 policy, and `/api/status/instances`
  authentication plus privacy.
- Frontend: `api-info-panel.test.tsx` (non-blocking resources, bindings, legitimate
  0%, click-to-detail, disabled state, refetch freshness), `model-usage-drawer.test.tsx`
  (masked identities preserved, no pricing link, error+retry, per-user quota),
  `api-info-section.test.tsx` (multi-domain binding, reorder keeps ids, two legacy
  rows get distinct ids with single-row edit/delete, unbound display).

## R16 — section settings batch (source complete; hosted validation pending)

- Added `PUT /api/option/batch` next to the existing root-only PUT. Root integrated its exact `option:write` PAT route declaration; this worker did not edit middleware. Existing single PUT remains supported.
- Payload: `{ options: [{ key, value }, ...] }`, 1–100 unique nonblank keys, primitive values only. Every member uses the same controller validator as single PUT and the model validators; dependent OAuth/Turnstile/Resend/Epay values are checked against the complete proposed section. Error responses include the failing `key`; database errors do not echo secret values.
- Ordinary settings commit in one transaction: K checked upserts, one options sync task and one secret-free `option.update` outbox event naming keys. A failed member/write/sync/audit enqueue rolls back all ordinary changes. Runtime publication follows commit under one OptionMap lock. SQL logging is discarded for this credential-bearing path.
- Passkey domains/ServerAddress, model pricing and request-policy keys are rejected by the ordinary batch endpoint. Frontend preserves their existing specialized model/security flows through the compatibility PUT or already-dedicated confirmation/pricing/policy mutation; they are intentionally NOT atomic with the ordinary subset of a mixed save.
- Converted every identified per-section awaited single-key loop in system settings to one array mutation. The hook makes ONE request for ordinary changes, keeps single-key controls compatible, patches saved ordinary cached values, and invalidates without awaiting unrelated refetches. Preserved the earlier pi-ui single-setting cache hunk.
- Two new message keys are present in all seven locale source files (pi-ui integration): `Could not save {{key}}: {{error}}`; `This setting must be saved through its dedicated settings API`. Script normalization/validation remains a cloud gate.
- Source-derived normal ordinary batch handler cost: BEGIN + K upserts + sync INSERT + audit INSERT + COMMIT = **K+4 primary database operations**, versus 9K for K sequential old PUTs; excludes shared auth/audit middleware and protocol overhead, not a latency measurement.

### R16 tests and hosted CI selectors (NOT executed locally)

- Existing `model/option_upsert_test.go`: checked single-key insert/overwrite/failure; legacy bulk atomicity; new `TestOrdinaryOptionsBatchDatabaseMatrix` covers success + option/sync/audit rollback on sqlite/mysql/postgres, and `TestOrdinaryOptionsBatchValidationDoesNotPublish` covers member and specialized-key rejection. Requires `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN`; a missing DSN skip is not a compatibility pass. Fixtures print exact engine versions in hosted logs.
- Existing `controller/option_claude_test.go`: shared per-key validation; batch dependency save; one value-free audit and one sync; single PUT compatibility; audit failure rollback. Run with `TEST_SECURITY_DIALECT=sqlite|mysql|postgres` against actual engines.
- Existing `router/access_token_scope_test.go`: only the new `TestOptionBatchAccessTokenScopes` is worker-owned. Exercises actual route RootAuth/PAT middleware, root option:write success, option:read failure, anonymous/ordinary-role failure, and role-downgrade failure without mutation.
- Existing frontend files updated: bot protection (one-request section save, hook cache patch, key-specific failure/no success, special-key partition); OAuth; pre-consume; email/notifications; Epay; log maintenance; group settings. Existing passkey/pricing/policy suites remain regression gates.

Commands for **pi-newapi-ci/root GitHub Actions only**:

```sh
go test -p 1 -count=1 -v ./model -run 'TestUpdateOptionPersistsAcrossDialects|TestUpdateOptionReportsWriteFailure|TestUpdateOptionsBulkIsAtomic|TestOrdinaryOptionsBatch'
for dialect in sqlite mysql postgres; do
  TEST_SECURITY_DIALECT=$dialect go test -p 1 -count=1 -v ./controller -run 'TestUpdateOptionRejectsNegativeClaudeDefaultMaxTokens|TestUpdateOptionRejectsInvalidGeminiSafetyThreshold|TestUpdateOptionsBatch'
done
go test -p 1 -count=1 -v ./router -run 'TestOptionBatchAccessTokenScopes|TestAccessTokenRouteRulesCoverEveryDashboardRoute'
cd web
bun run test -- src/features/system-settings
```

Also run cloud formatting, typecheck/lint, root build and affected race gates. No schema migration was introduced by R16; real three-engine results and exact versions are still REQUIRED before a database-compatibility/completion claim. No local tests/builds/vet/lint/typecheck/gofmt/format/DB probes or Git publication were performed.

## R17 — retention, request-content lifecycle and storage summary (source complete; hosted verification pending)

- Added root-managed usage-log and independent request-content retention controls. Both retention days default to zero; capture remains enabled at the existing default with the existing 256 KiB default byte limit. Shared validation applies to single and batch option writes; accepted retention bounds are 0–36500 days and capture bytes 256–134217728. Existing request-content environment variables remain explicit per-node overrides, and the storage summary reports effective values plus override flags.
- Scheduled usage-log cleanup uses the existing master-only system-task handler and lease/dedup lifecycle. Manual cleanup remains supported. Scheduled batches remain bounded at 100, reevaluate setting/master state before deletion, and stop when retention is disabled. The valid 36500-day setting is clamped to a positive Unix cutoff for the existing task timestamp contract. Relational deletion materializes at most one bounded, ordered ID page before DELETE (PostgreSQL-safe and avoiding MySQL LIMIT-in-subquery); ClickHouse retains one synchronous time-cutoff mutation per run.
- Request-content expiry reuses the existing writer/task references, operation generations, uncertain cross-store fences, and separate log-store delete path. Expiration independently removes old detail payloads while retaining usage-log rows. `expire_pending` / `retired` fences prevent delayed snapshots from resurrecting content; active task references, live writers, and uncertain operations remain protected. No content schema/codec or consume-log billing arithmetic changed.
- Added a root-only, `no-store` storage-summary route (`GET /api/option/log_storage`, PAT `option:read`) with a two-second metadata-query budget. PostgreSQL uses relation metadata; MySQL uses `information_schema` estimates; ClickHouse uses active-part metadata; SQLite explicitly returns unknown instead of a database-wide or payload scan. Query/permission failures also remain unknown. No compression ratio or production storage measurement is asserted. The UI explains PostgreSQL space reuse and that VACUUM FULL / pg_repack is never automatic.
- Log Maintenance UI adds the default-zero retention fields, capture toggle/limit and metadata state to its existing one-batch save. The existing `web/src/features/system-settings/maintenance/__tests__/log-settings.test.tsx` covers loading without blocking save, known/zero/estimated/unknown metadata, environment override state, request failure, default/zero values, byte bounds, invalid retention/byte inputs, and the submitted batch payload. All 13 R17 translation keys are present in en, zh, zh-TW, fr, ja, ru and vi (read-only source confirmation; no locale writes in this item).
- Changed files: `common/request_content.go`, `common/body_storage_test.go`, `setting/operation_setting/log_setting.go`, `model/option.go`, `model/log.go`, `model/log_request_detail.go`, `model/log_storage.go`, `model/log_format_test.go`, `service/system_task.go`, `service/system_task_test.go`, `service/log_request_content_cleanup.go`, `controller/log_storage.go`, `router/api-router.go`, `router/access_token_scope_test.go`, `middleware/access_token_routes.go`, `web/src/features/system-settings/types.ts`, `web/src/features/system-settings/operations/index.tsx`, `web/src/features/system-settings/operations/section-registry.tsx`, `web/src/features/system-settings/maintenance/log-settings-section.tsx`, `web/src/features/system-settings/maintenance/log-storage-summary.tsx`, `web/src/features/system-settings/maintenance/__tests__/log-settings.test.tsx`, `web/src/i18n/static-keys.ts`, and all seven `web/src/i18n/locales/{en,zh,zh-TW,fr,ja,ru,vi}.json` files (locale contribution was integrated by the reserved writer; key presence checked read-only here). Shared files contain concurrent R16/other workers' changes; do not attribute their full diffs to R17.

### R17 CI selectors and validation status

- Hosted Go selectors: `TestLogRequestDetailRetentionDatabaseMatrix` (`./model`); `TestSystemTaskLogRetention` (`./service`); `TestPersonalAccessTokenLogStorageScope` (`./router`). The retention matrix additionally exercises actual SQLite/MySQL/PostgreSQL/ClickHouse log stores, fresh and released-upgrade fixtures, and separate-store cases; hosted DSNs/versions/results remain required. Capture settings/environment selector: `TestRequestContentCapture` (`./common`). Exact configured workflow runs are in `.github/workflows/ci.yml` around lines 440–446.
- Hosted frontend test file: `web/src/features/system-settings/maintenance/__tests__/log-settings.test.tsx` (also the existing full Vitest run). Changed frontend-file lint, typecheck and formatter gates are in CI. No local test, build, vet, lint, typecheck, formatter, Go/Bun/Node command or DB probe was run; no hosted result/version is claimed. Database compatibility and overall validation remain pending GitHub Actions.
- Keep work-queue item #14’s plugin-login redirect callback wait as its own test-only commit (`test(channels): await plugin login completion notification`); it is independent of R17 and must not be folded into the retention commit.

## Known gaps

- No page-level ranking wiring test (that `ModelLink`/`MoverRow` open the drawer).
- No test for the 30s interval timer itself or AbortSignal cancellation.
- No test for same-session role downgrade invalidating the ranking query key
  (backend masking is covered; client cache-key behaviour is not).
- Backend router tests depend on the shared fixture continuing to call `SetApiRouter`.
- No local build/test/vet/gofmt/format/DB verification was performed: standing ban for
  this worker. All validation is GitHub Actions, run by root.
