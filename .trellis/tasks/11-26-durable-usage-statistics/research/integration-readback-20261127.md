# Research: Durable statistics integration release blockers

- Query: Audit the saved R24 integration and identify the smallest remaining path to statistics that persist independently of optional logs and survive retention.
- Scope: internal, read-only source and saved test/CI definitions; one research artifact only
- Date: 2026-11-27
- Worktree: `D:/My2KProjects/new-api-statistics-20261127`
- Reviewer: `/root/statistics_finish_audit`
- Disposition: **NOT complete / NOT release-ready.** Foundation is already accepted separately; this audit does not reopen its design or claim executable validation.

## Findings

### 1. Partial statistics are currently returned without coverage information

`model/usage_event_read.go:14-17` explicitly requires serving callers to attach coverage. The implementations at lines 117, 197, and 271 call `GetUsageStatisticsCoverage` but discard the returned view, retaining only an error. `GetUsageStatisticsCoverage` deliberately permits partial subtotals; its default is partial/unknown, not an error (`model/usage_event_coverage.go:250-266`).

The serving contracts remain the old numeric shape:

- `controller/usedata.go:30-131` returns `data: dates` for dashboard, users and flow without coverage.
- `controller/channel_usage.go:35-40,59-64` returns items/detail without coverage; `model/channel_usage.go:11-37` contains numeric fields only.
- `controller/log.go:237-286` returns the five old summary fields; `model/log.go:872-877` has no coverage member.
- `service/rankings.go:24-32` / `controller/rankings.go` and `controller/ranking_viewer.go:69-90` have no coverage response.
- A source search found no coverage consumer in `web/src`.

This is an observable release blocker: after upgrade, an incomplete or empty subtotal is indistinguishable from complete historical totals or a verified zero. Existing valid partial facts should remain available, but their status must travel through the response/cache and render visibly.

### 2. Historical activation/backfill has no runnable production entry point

`GetUsageReadLineage` intentionally defaults to canonical live-only membership when row 1 is absent (`model/usage_event_coverage.go:167-185`). It omits legacy `quota_data`; `readUsageDashboard` includes that table only when `IncludeLegacyQuotaData` is true. Therefore starting this saved tree on an existing installation switches dashboard/flow/rankings to newly captured facts while hiding old aggregate history until an explicit lineage is installed.

Non-test call-site search found no caller of `InstallUsageReadLineage`, `InspectUsageEventBackfillBatch` (except within `RunUsageEventBackfillBatch`), `RunUsageEventBackfillBatch`, or `AttestUsageStatisticsCoverage` outside their definitions. Startup migrates and validates membership (`model/main.go:401-410`) but cannot establish a real old-writer fence, immutable baseline or source manifest. The helpers are library implementation, not an operational backfill/cutover workflow.

`InstallUsageReadLineage` explicitly must not be auto-called at startup: it records external proof only (`model/usage_event_coverage.go:130-134`). Keep that contract. Provide an actual bounded/resumable operator entry point and recorded procedure, preserving old dashboard history and disjoint membership. Do not solve this by fabricating a manifest or blindly adding legacy totals and imported log totals.

The existing legacy writer can update rows already below a maximum ID: `SaveQuotaDataCache` / `increaseQuotaData`, `model/usedata.go:109-149`, find a dimension row then increment it. A max-ID cutoff alone does not freeze a mutable baseline. The activation procedure must account for all old writers and buffers; `UsageReport == nil` still reaches legacy export (`model/log.go:488,581`). An installed manifest is evidence, not an enforcement lock.

### 3. Application retention still deletes uncovered rows

`model/log.go:933-980` has no numeric-proof check. The relational branch takes at most 100 IDs then deletes them. The ClickHouse branch performs one full `created_at < cutoff` synchronous mutation. Both can erase the sole surviving historical source before import/link verification. `service/system_task.go:418` calls that function from the existing manual/scheduled cleanup runner.

Native ClickHouse TTL remains independently controlled by `LOG_SQL_CLICKHOUSE_TTL_DAYS`, `model/main.go:460-480`. An application proof gate cannot fence native TTL or external SQL deletion. Production cleanup remains paused by task scope; no production settings were inspected or changed.

For the software closure, guard relational deletion with exact source namespace + row ID or linked occurrence, immutable payload equality, ready state, applied projections and verified checkpoint. Delete only proven IDs. For broad ClickHouse mutation, require the accepted whole-predicate multiset proof and no-late-write fence; otherwise fail closed. Whole-history `Complete` is **not** the correct replacement for per-row proof and unrelated open writer epochs must not block a proven row.

### 4. Hosted source matrices currently skip MySQL/PostgreSQL

`TestRelayUsageReportsDatabaseMatrix` and `TestTaskUsageReportsDatabaseMatrix` both use `runQuotaReportingDatabaseMatrix`. Its external fixtures read `TEST_FIXED_MYSQL_DSN`, `TEST_FIXED_POSTGRES_DSN` and optional separate-log variants, and skip absent variables (`service/text_quota_test.go:45-88`).

The current `.github/workflows/ci.yml:164-169` sets only `TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, `TEST_CLICKHOUSE_DSN`, and Redis. No `TEST_FIXED_*` alias exists in that workflow. The all-package integration command at line 409 will discover the new tests but skip their MySQL/PostgreSQL source behavior branches. Root must bind the existing fixture aliases and require the named branches to pass; a green package result alone is insufficient evidence.

The explicit JSON branch verifier at `.github/workflows/ci.yml:522-591` currently covers foundation schema and foundation race cases only. Extend evidence to source/reader/backfill integration once frozen. The released migration probe already enumerates all eight statistics/coverage/backfill tables (`.github/scripts/released_migration_probe.go:368-398`) and preserves released log/quota rows. That is saved verification code, not an executed migration result for this integration.

### 5. Backfill behavioral tests are still too narrow for the required contract

The sole saved `RunUsageEventBackfillBatch` test call is `model/log_filter_test.go:203`, after a read-only manifest pass. It imports one small page and asserts retained verification/cache semantic results. Searches found no existing regression calling the batch function for interrupted commit/retry, repeated completed import, checkpoint conflict, concurrent importers, late source mutation, or duplicate ClickHouse tuple multiplicity.

The implementation has bounded 100-row import and checkpoint revision checks (`model/usage_event_backfill.go:564-689`), but those branches are not proven by the saved test above. Extend that existing suitable suite rather than scattering new test files. Include same/separate relational sources and ClickHouse, and assert exact observable totals/checkpoints after recovery and replay.

### 6. Statistics branch still needs current R33 integration

In this saved worktree `controller/log.go:50` still returns a plain `model.LogFilter` and has no `user_scope` parsing, while the new fact reader and model filter support the three scopes. This is a branch integration dependency with root's R33 delivery, not authorization to overwrite that owner. Import the final accepted parser/frontend scope contract before final capture, then extend numeric-summary regressions against the resulting controller API.

## Existing saved work that should be preserved

- Live source reporting is outside optional log/export switches. `PrepareRelayUsageReport` freezes historical dimensions and an occurrence; `prepareUsageSourceReport`/`confirmUsageSourceReport` retry the same identity under bounded contexts (`service/usage_statistics.go:20-87,209-258`). The old log writer returns early when consume logs are disabled (`model/log.go:429`), but source call sites already prepare/confirm before invoking it.
- Current source inventory includes text, realtime/audio, diagnostic channel test, violation fee, task submission/adjustment/refund and Midjourney consume/refund seams. `rg` found all current `RecordConsumeLog` / `RecordTaskBillingLog` production callers passing reports in the changed paths. This is call-site coverage, not proof of every runtime outcome.
- `controller/system_task_handlers.go:26,33-51` registers a 30-second ready-projection recovery task, capped at 100 per run. Pending/unknown sources are not promoted; it does not replay money. Recovery is wired, not missing.
- Channel totals/model breakdown use primary lifetime projections plus a ready pending tail in one statement (`model/usage_event_read.go:196-235`). Dashboard/flow/rankings use frozen legacy + qualified live hours + ready tail (`:270 onward`). Dashboard rows filter projection version, source namespace and collection epoch. The canonical namespace/epoch in `model/usage_source.go:13-20` identifies the codec; it is not writer registration or completeness evidence.
- `TestUsageStatisticsReadersDatabaseMatrix` covers dashboard/rankings/flow/log-summary equality after deleting source logs and replaying projections (`model/usedata_flow_test.go:271-290`), namespace separation, partial intervals, unknown measures/ownership and pending-tail overflow. Channel tests also compare totals after actual relational/ClickHouse deletion and recovery (`model/channel_usage_test.go:229-248`). These authored model tests should be retained. They do not test the missing API coverage propagation or guarded cleanup path.

## Parallel ownership handoff proposals

These are concrete candidate groups for root to hand back, not edits or implicit scope expansion by this researcher. Existing source owners must freeze before shared-file transfer.

| Lane | Exact existing files | Responsibility/dependency |
| --- | --- | --- |
| A: activation/backfill/coverage | `model/usage_event_coverage.go`, `model/usage_event_backfill.go`, `model/usedata.go`, `model/main.go`, `model/log_filter_test.go` | Publish operational activation/import contract and exact-row proof API; root must explicitly choose/name the runnable control-plane entry point because none exists today. Freeze/drain old baseline correctly; add resumability/multiplicity cases. |
| B: API coverage | `controller/usedata.go`, `controller/channel_usage.go`, `controller/log.go`, `controller/rankings.go`, `controller/ranking_viewer.go`, `service/rankings.go`; tests `controller/usedata_flow_test.go`, `controller/log_export_test.go`, `service/rankings_test.go` | Carry coverage alongside the same numeric scope/range. Ensure ranking caches retain matching coverage. Preserve R33 authenticated scope and viewer projection. Uses existing coverage API, does not redesign fact storage. |
| C: cleanup | `model/log.go` only `DeleteOldLogBatch`, `service/system_task.go`; tests `model/clickhouse_log_test.go`, `service/system_task_test.go`, `controller/system_task_test.go` | Depends on A's proof API. Serial ownership for `model/log.go` after source lane freezes. Keep unproved rows, fail closed on uncertified CH range. Native TTL remains explicit operational prerequisite. |
| D: hosted verification | `.github/workflows/ci.yml`, `.github/scripts/released_migration_probe.go` | Root-owned alias/required-branch evidence, source capture, exact-SHA Actions and artifacts. Preserve fresh + actual latest-release upgrade twice coverage. |

Frontend can proceed in parallel with B after a small agreed `coverage` response contract. Exact existing touchpoints found:

- Dashboard/flow: `web/src/features/dashboard/{api.ts,types.ts}`, `components/overview/summary-cards.tsx`, `components/models/log-stat-cards.tsx`, `components/users/user-charts.tsx`, `components/flow/flow-charts.tsx`.
- Channels: `web/src/features/channels/{api.ts,types.ts}`, `hooks/use-channel-usage.ts`, `components/channel-usage-context.tsx`, `components/channels-table.tsx`, `components/drawers/channel-usage-summary.tsx`; existing `hooks/__tests__/use-channel-usage.test.tsx`.
- Rankings: `web/src/features/rankings/{api.ts,types.ts,index.tsx}`, `hooks/use-rankings.ts`, `components/model-usage-drawer.tsx`; existing `__tests__/model-usage-drawer.test.tsx`, `__tests__/cache-tokens.test.tsx`.
- Log summary: `web/src/features/usage-logs/{api.ts,types.ts}`, `components/common-logs-stats.tsx`, existing `components/__tests__/common-logs-stats.test.tsx`.

These are data/render entry points, not a statement that every file must change. Reader DTO coverage should not be dropped by `select`/mapping functions. Root should choose one reusable display surface following `web/AGENTS.md` and UI/i18n skills; any new copy belongs to the separate locale owner. This audit does not authorize locale writes.

## Minimum complete release sequence

1. Freeze and capture source/read/backfill code after the above blockers are resolved; incorporate R33 without stale overwrite.
2. Supply an actual resumable activation/backfill entry point and procedure that preserves legacy data, validates disjoint membership, handles old buffers/writers and clearly identifies partial history. Do not equate additive migration with cutover.
3. Return/render coverage for every numerical surface, including cached rankings and empty results. Keep optional list/count/export log-backed.
4. Guard cleanup as above; keep production cleanup paused until operational source/TTL/backup/capacity conditions are met. Software release alone does not authorize deletion.
5. Run exact captured SHA in Actions: root formatting/vet/build/tests, source and reader matrices, all backfill cases, deterministic concurrency, actual log-deletion equality, fresh and actual released-upgrade startup twice for SQLite/MySQL/PostgreSQL, and separate relational/ClickHouse source cases. Record versions/results/skips.

Existing selectors (saved, not run): `TestUsageEventFoundationDatabaseMatrix`, `TestUsageEventInputBoundsAndIdentity`, `TestMigrationSchemaStability/^(sqlite|mysql|postgres)$/usage_statistics_foundation`, `TestRelayUsageReportsDatabaseMatrix`, `TestTaskUsageReportsDatabaseMatrix`, `TestUsageStatisticsReadersDatabaseMatrix`, `TestChannelUsageDurableReadBoundaries`, `TestChannelUsageSumsConsumeLogsPerChannel`, `TestChannelUsageDetailMergesConfiguredAndLoggedModels`, `TestSumUsedQuotaCountsCacheTokensByUsageSemantics`. New required backfill/coverage/cleanup cases should be published by their owner after save, not invented as passed selectors.

## Related specs and references

- Read `.trellis/workflow.md`; task `prd.md`, `design.md`, `implement.md`; `.trellis/spec/backend/usage-statistics-foundation.md`; prior `research/architecture-review.md`.
- Full mandatory `.agents/rules/billing.md` was read before source review. No fee/quota arithmetic or finance replay changes proposed.
- No external claims or library recommendations were needed. No external research was performed.

## Caveats / Not Found

- No code/test/spec edits, Git/index actions, local validation, database/browser operations, E: access, locale writes or production cleanup performed. Only this research artifact was written.
- Review observes mutable saved files, not a frozen captured commit. Root owns source equality, exact SHA, merging, publishing and hosted evidence. Any later owner edits require focused reread.
- Pre-Prepare loss without retained evidence remains unrecoverable; partial status must remain honest. Static namespace/epoch, successful recovery, retained-row manifests and empty queues do not prove all-writer completeness.
- No claim of database compatibility, complete-R24 acceptance, production readiness or deployed behavior follows from this audit.
