# Backend statistics inventory — R24

## Status and scope

- Research owner: p2. Exclusive output: this file.
- Active task: `.trellis/tasks/11-26-durable-usage-statistics`, child of the console-responsiveness task.
- Source inspection only, following the explicit R24 assignment. `.agents/rules/billing.md` was read in full before inspecting quota/accounting fields. No production changes, local executable validation, database access, production probes, settings changes, deletion, or Git/index writes were performed for this inventory.
- The child is still in planning. Its PRD is a scaffold; `design.md` / `implement.md` were not present at inventory time. Findings below are inputs to root/pJ's architecture, not approval of a particular implementation.
- Scope is **all business numeric statistics independent of retained usage logs**, including log-page numeric summary cards and model/channel totals. Actual retained-log lists, their pagination counts, exports and storage/cleanup facts remain log-backed by design.
- Inspected production Go call sites of `Table("logs")`, `Model(&Log{})`, `LOG_DB`, SQL aggregate expressions and callers; followed existing `quota_data`, performance and account-counter alternatives. File/line anchors describe the inspected source and can move during integration.

## 1. Direct usage-log aggregate inventory

The identified production usage-log aggregates are in `model/log.go` and `model/channel_usage.go`. Request-content association scans are authorization, not statistics. Audit-log aggregates are a different table/domain.

| Source / caller | Endpoint / access | Dimensions and time semantics | Measures / retention consequence |
| --- | --- | --- | --- |
| `model/channel_usage.go:41`, `GetChannelUsages`; `controller/channel_usage.go:18` | `GET /api/channel/usage?ids=...`; channel read permission | Deduplicated positive channel IDs, capped at 200 by controller; consume type only; group by channel ID; **all time**, no time filter | `COUNT(*)`, `SUM(prompt_tokens)`, `SUM(completion_tokens)`, `SUM(quota)`; `total_tokens = prompt + completion`. Channels without rows omitted. Deleting logs shrinks card/channel statistics. |
| `model/channel_usage.go:65`, `GetChannelUsageDetail`; `controller/channel_usage.go:44` | `GET /api/channel/:id/usage`; channel read permission | One existing channel, consume only, group by model; **all time**. Union historical nonempty model names with current configured models; current enabled status comes from `DB.abilities`, not log retention | Same counts/sums plus exact `MAX(created_at)` as `last_used_at`; channel totals built from per-model rows. Empty model names are skipped when constructing detail totals. Models sorted by raw total tokens then name. Log pruning loses historical model contributions and last-used times. |
| `model/log.go:825`, `SumUsedQuota`; `controller/log.go:236`, `GetLogsStat` | `GET /api/log/stat`; administrator | Full administrator `LogFilter` below; ignore requested list type and force consume type | `SUM(quota)` over selected range, rolling-60-second count/token rate intersecting that range, cache-read/input totals. All are business summaries and must move off logs. |
| Same model; `controller/log.go:250`, `GetLogsSelfStat` | `GET /api/log/self/stat`; authenticated user, forced current user ID | Self `LogFilter`; ordinary users additionally constrained by configured maximum query range | Same numeric cards; cannot use retained-row pagination total as their long-term source. |
| `model/log.go:922`, `SumUsedToken` | Exported model helper; **no production caller found** in repository search | Exact username, token name and model; optional inclusive start/end; always consume type despite unused `logType` argument | `SUM(prompt_tokens)+SUM(completion_tokens)`. Not a live endpoint but a residual log-backed business aggregate; migrate/remove deliberately, not overlook. |
| `model/log.go:654`, `GetAllLogs` | `GET /api/log/`; administrator | All requested list filters and requested type, then page/offset | `COUNT` is **retained matching rows for pagination**. Keep log-backed; it should decrease when rows disappear. |
| `model/log.go:720`, `GetUserLogs` | `GET /api/log/self`; user ID forced; ordinary-user maximum range | Self list filters; count query adds `Limit(10000)` (do not infer cross-dialect effective count-capping behavior without execution) | Retained-row pagination count, not persistent historical request total. Keep log-backed. |
| `model/log.go:943`, `CountOldLog` | System-task cleanup progress and ClickHouse deletion pre-count | All log types with `created_at < cutoff` | Maintenance count. Keep log-backed; not a business-usage statistic. |

Routes: `router/channel-router.go:69,76`; `router/api-router.go:365-399`.

### Log numeric filter contract

`controller/log.go:50` builds `model.LogFilter`; `model/log.go:606-651` applies it.

- Administrator summary accepts model, historical username, token name, group, channel ID, request ID, upstream request ID, optional start/end and match mode.
- Self summary forces authenticated user ID. Username/channel query parameters are not accepted as self filters.
- Start/end compare exact stored event seconds with `>=` / `<=`; zero means omit that bound. `SumUsedQuota` discards the requested type and uses consume rows regardless of the list type filter.
- Default text mode is **literal substring** matching, escaping LIKE metacharacters. Exact mode compares text by equality, except historical model/username `%` inputs retain their wildcard-pattern rule and validation. Preserve or explicitly redesign this public contract; a new exact-only aggregate endpoint is not a transparent replacement.
- Group is stored as a reserved-word SQL column; existing log dialect quoting differs from primary DB quoting. Source migrations must use the right store's dialect helpers.
- RPM = number of matching consume **events** created in the last 60 seconds, intersected with explicit start/end and other filters. TPM = their raw prompt+completion tokens. No hourly-only rollup can reproduce arbitrary-second edges or that moving window exactly.
- Summary quota/cache/input cards cover the full matching time range, not just the displayed page or the last minute.
- Cache aggregation is currently best effort: SQL metadata-query failure logs an error and returns quota/rate with zero cache fields. Changing its data source should retain an honest availability policy rather than silently interpreting unavailable history as verified zero.

### Token-semantic distinctions that must survive migration

`model/log.go:477-504,884-919`; `service/text_quota.go:470-561`:

1. Channel totals and `SumUsedToken` use **raw prompt + completion**.
2. Log-page TPM likewise uses raw prompt + completion.
3. `quota_data.token_used` starts with prompt + completion and adds nonnegative cache-read + normalized cache-write tokens **only when `other.usage_semantic == "anthropic"`**. OpenAI semantics already include cache categories in prompt tokens; adding again double-counts.
4. Log cache-hit denominator is prompt plus Anthropic cache read/write; numerator is nonnegative cache-read. It is not total prompt+completion.
5. Settlement writes normalized cache totals and may also expose an explicitly sourced `input_tokens_total`. Existing log summary SQL specifically uses the semantic rule above, not that field. Do not silently change a denominator when moving storage.
6. Cache-write 5m/1h breakdown must not be added again to its normalized total. Do not derive quantities by prices, model name, request endpoint or current pricing configuration.

A single undifferentiated `total_tokens` column is insufficient to preserve all these surfaces.

## 2. Existing primary `quota_data` readers

`QuotaData` is migrated on primary `DB` (`model/main.go:362`), not on independently configured `LOG_DB`. Its current dimensions are user ID, historical username, model name, **hour-start timestamp**, group, token ID, channel ID and node name. Measures are count, quota, token-used total, cache-read and cache-creation tokens (`model/usedata.go:13-43`).

| Endpoint / source | Access, filters, dimensions | Existing measures / limits |
| --- | --- | --- |
| `GET /api/data/`; `controller/usedata.go:31`, `model/usedata.go:183` | Admin; inclusive supplied start/end; optional exact username. Without username group by model/hour; with username group by user ID/name/model/hour | Sums count/quota/token-used/cache read/cache creation. Input timestamps parsed permissively; current hour-start representation means partial-hour boundaries are not exact event boundaries. |
| `GET /api/data/users`; controller `:48`, model `:173` | Admin; inclusive start/end; group by historical username/hour | Same sums. Username-only grouping can combine/split identity history differently from stable user-ID rankings. |
| `GET /api/data/self`; controller `:63`, model `:162` | Current user ID; supplied inclusive start/end with ordinary-user maximum range restriction | Group by user ID/name/model/hour, same sums. |
| `GET /api/data/flow`; controller `:85`, `model/usedata_flow.go:25` | Admin/root; requires positive ordered start/end; optional exact username; excludes `use_group = ''` | Admin groups user ID/name/group/model/channel; root additionally node and token ID. Sums count/quota/token-used, quota-descending order. Resolve current channel/token names from primary metadata. |
| `GET /api/data/flow/self`; controller `:104` | Current user; positive ordered start/end and maximum range; nonempty group only | Group by token ID/group/model. No other users, channel IDs or node data. |
| `GET /api/rankings`; `controller/rankings.go`, `service/rankings.go:150-257`, `model/usedata_rankings.go:67,80` | Header-module policy; rolling today=24h, week=7d, month=30d, year=365d; current and preceding equal-length periods, inclusive bounds; exclude empty model, positive summed tokens only | Model/vendor totals, shares, growth/movers, token history; model top20, history top10 and vendor grouping. Daily/hourly/weekly display buckets per period. Five-minute service cache; vendor metadata comes from current pricing metadata, not logs. |
| `GET /api/rankings/model-usage`; `controller/ranking_viewer.go`, `model/usedata_rankings.go:29,57` | Same period definitions; exact selected model; no shared response caching; server masks other users for ordinary/anonymous viewers | Full model request/token/quota totals over all users; user leaderboard top20 (model helper cap200), stable user-ID grouping, tokens descending then ID, positive token sums only. Resolve current names only for authorized visible IDs. |

Deleting logs does not directly delete `quota_data`, but **that does not make the current collection path a complete or reliable long-term source**.

## 3. Existing aggregate write-path gaps

### Flags and durability

- `RecordConsumeLog` (`model/log.go:425`) stores report-hook parameters before checking `LogConsumeEnabled`, then returns early when disabled. `LogQuotaData` is later inside `DataExportEnabled`. Thus disabling either switch prevents dashboard/ranking collection even if billing settlement succeeds.
- `RecordTaskBillingLog` (`model/log.go:524`) has the same consume-log switch and calls `LogQuotaData` only for consume events with export enabled. Refund logs are not folded into these aggregates.
- A failed `createRequestLog` is logged but does **not** stop the subsequent `LogQuotaData` call. Logs and exported statistics can diverge in either direction; log presence is not a reliable marker that the aggregate was persisted.
- `LogQuotaData` (`model/usedata.go:84`) only updates a process-local map under a mutex and floors time to an hour. `UpdateQuotaData` flushes only while `DataExportEnabled`, with a default five-minute interval (`common/constants.go:28-29`, `model/usedata.go:45`). Every node has independent buffered state.
- `SaveQuotaDataCache` (`model/usedata.go:108`) performs lookup then update/create without a durable event identity, multi-column unique dimension key or enclosing per-batch transaction. Lookup/create errors are ignored; update errors are logged; the map is cleared after the loop regardless. Cross-node duplicate dimensions, ambiguous acknowledgements and lost buffered writes need an explicit design; this is not an exactly-once accumulator.
- Graceful shutdown calls flush only if export is enabled (`main.go:278`). A crash/forced exit can lose unflushed statistics; disabling export can strand a prior buffer. No retry/coverage ledger was found for those buffered values.
- Integer accumulation uses existing `int` count/quota/token fields. New durable accounting/aggregation must choose checked numeric bounds and cross-dialect storage deliberately; widening a total is not permission to weaken billing invariants.

### Consume event versus logical request and refund semantics

Source: `service/task_billing.go:26,397,472`, `service/midjourney.go:120`, `service/violation_fee.go:135`, `controller/channel-test.go` consume-log call.

- Current `COUNT(*)` / `QuotaData.Count=1` count **consume events**, not necessarily unique ingress requests or tasks.
- Task submission records consumption, and a positive later quota adjustment records a second consume event. `RecordTaskBillingLog` adds one aggregate count for that adjustment. `users.request_count` increments on submission instead, so the two concepts are not interchangeable.
- Negative task adjustment/refund stores a **positive** refund amount with `LogTypeRefund`; current consume-only log summaries and `quota_data` exclude it. User/channel/token accounting counters are decremented separately. Therefore current consume summaries are gross consume-event totals, not guaranteed net billed balance usage.
- Violation fees create a consume event with quota and zero normal tokens; zero-token task/fixed-price consumption is valid. Rank queries with positive-token HAVING omit zero-token users/models even if their quota is nonzero.
- Normal text/audio paths can record a zero-usage consume row even when they skip user/channel usage-counter increments. Do not force those counters to equal log-event count.
- Explicit channel diagnostics also call `RecordConsumeLog` and contribute to existing log/export totals. Determine whether R24 retains this behavior rather than silently dropping them.
- Several legitimate events can share one `request_id`. Deduplicating all new events solely by request ID would discard task adjustments or additional charged events. Backfill/live dedup requires an explicit event/adjustment identity.
- Request processing node is recorded in `other.admin_info.node_name` by `writeRequestLog`; async export may use frozen task `NodeName`. Submission and processing-node semantics must not be conflated. Legacy rows may have neither.
- Model names are the model stored by settlement/logging; some gizmo names are deliberately normalized to wildcard forms in `service/text_quota.go`. Reapplying current model mappings during backfill would rewrite historical dimensions.

### Missing dimensions and legacy uncertainty

Current `quota_data` lacks separate prompt/completion/input totals, exact event seconds, exact last-used event time, historical token name, request/upstream request IDs and source-event coverage. It therefore cannot replace the current log summary filters as-is. Current names resolved from deleted/renamed token/channel tables do not reconstruct historical text filters.

Older aggregate rows can have default zero/empty newly added dimensions or cache fields. Empty group rows are excluded from flow. Do not infer complete historical dimensions from a successfully migrated table.

## 4. Numeric stores already independent of usage logs

These are existing alternatives, not substitutes for all requested dimensions:

- **`users.used_quota` / `users.request_count`**: primary accounting counters (`model/user.go:1503-1541`), exposed by user self/admin records (`controller/user.go:555-556`). Optional batch mode buffers them. They survive log deletion but cannot recover token quantities, model/group/time splits, historical text filters or all consume-event counts.
- **`channels.used_quota`**: primary `int64` billed-usage counter (`model/channel.go:1993`), used by channel quota/card presentation. Task refunds/adjustments update it independently. It is not the raw token total or an event histogram.
- **`tokens.used_quota` / `remain_quota`**: primary token accounting (`model/token.go:673-731`); compatibility billing endpoints and `GET /api/usage/token/` use these or user totals. Those endpoints are not log-derived. Deleting/renaming a token changes what metadata can be resolved; it does not reconstruct lost historical statistics.
- **Local channel/allocation budget tables**: independent admission/settlement accounting with period/lifetime measures and reset epochs. They are gated by ledger activation and have different semantics; never replace wallet/subscription authority or historic usage facts with a budget value.
- **`perf_metrics`**: primary model/group/time-bucket request/success/latency/TTFT/output/generation metrics (`model/perf_metric.go`, `pkg/perf_metrics`). `GET /api/perf-metrics` and `/summary` read persistent completed buckets plus local hot buckets. Collection has its own Enabled switch; configurable retention defaults to zero and independently deletes old performance buckets. Retry flush restores failed drained counters, but current hot state is not a durable billing ledger. Already independent of log retention; preserve this domain rather than deriving success-rate/latency from consume logs.
- Subscription balance-spending totals use subscription order/charged-quota records; top-up/check-in statistics use their own business tables. They are not `logs` aggregates. No new billing write rule is approved by this inventory.

## 5. What must remain actual log/storage operations

- `GetAllLogs` / `GetUserLogs` retained-row counts and pages; `GetLogByTokenId` recent rows; actual JSON/NDJSON log exports and their exported-record counts.
- `CountOldLog`, deletion task remaining/progress, `DeleteOldLogBatch`; these describe rows eligible for removal, not lifetime requests.
- `model/log_request_detail.go`, `hasLogRequestDetailReference`: exact surviving consume/error/refund association required before privileged body reads and reclamation. A numeric aggregate must **not** substitute for this authorization/liveness proof.
- `GetLogStorageTables` (`model/log_storage.go`): database metadata bytes, including ClickHouse `system.parts SUM(bytes_on_disk)`. Physical storage facts should change after cleanup; not a usage total.
- Audit-log pagination and outbox delivery-presence counts (`model/audit_log.go`) are audit/storage facts, not request consumption statistics.
- System health/concurrency/cache statistics come from their operational collectors, not request logs. Persistent usage design must not fabricate these from billing facts.

## 6. Separate stores, ClickHouse and cleanup/backfill ordering

- Main DB supports SQLite/MySQL/PostgreSQL. `LOG_DB` can be the same handle, a separate relational database or ClickHouse (`model/main.go:240-262,422-519`). Never assume a SQL transaction or join spans main/log stores.
- Relational logs have real row IDs. ClickHouse `logs` uses MergeTree with `id Int64 DEFAULT 0`, partition/order by created time/request ID. Response list IDs are reassigned display ordinals. Neither CH `id` nor displayed IDs are safe backfill identities/cursors.
- Request IDs are not unique per consume event. Historical missing IDs, duplicate ClickHouse inserts and same-request task adjustments need an explicit deterministic provenance/idempotency policy. Do not silently merge legitimate charges or double-count retries.
- Scheduled/manual cleanup reaches `CountOldLog` / `DeleteOldLogBatch` via `service/system_task.go:354-461`. Relational deletion materializes bounded IDs first; ClickHouse deletes all qualifying rows in one synchronous time-cutoff mutation. A durable-statistics prerequisite belongs ahead of any destructive step that could erase needed source rows.
- **Native ClickHouse TTL is an independent deletion path**: `LOG_SQL_CLICKHOUSE_TTL_DAYS` is applied during log migration through `syncClickHouseLogTTL` (`model/main.go:443-508`). Gating only the application cleanup task does not prevent native expiry. Architecture/rollout must address it without assuming permission to alter production settings.
- Independent request-content expiration removes bodies, not the numeric fields in usage logs. It must still retain active-task/writer/pending protections. Compression has no role as a replacement for numeric statistics or backfill coverage.
- Existing `quota_data` overlaps surviving logs without durable provenance. Blind additive historical backfill into the same totals will double-count an unknown overlap; replacing those rows can erase already-pruned history. Establish explicit source coverage and transition semantics first.
- Once logs were deleted or consume logging/export was disabled, account totals alone cannot recover historical per-model/user/group/token/time values or token semantics. Preserve known older aggregates with clearly bounded coverage; report irrecoverable gaps, do not fabricate exact history or mark migration globally complete.
- Snapshot high-water marks must account for concurrent new consumption, late task adjustments, cross-store acknowledgement loss, restart/retry, and deletion racing the scan. A time cutoff alone is not proof that every older event has arrived.

## 7. Architecture and hosted acceptance inputs

The following requirements are derived from existing behavior, not an implementation prescription:

1. Persist numeric facts/aggregates independently of `LogConsumeEnabled`, `DataExportEnabled` and optional body capture. Keep opt-out logging from disabling required statistics.
2. Preserve all reader measures/dimensions above, including exact-second/log-text filtering or an explicitly approved contract change. Keep server user scoping and ranking identity masking.
3. Decide and document separate consume-event count, logical-request count, gross consumption, refund and net usage measures. Do not mutate wallet/subscription/token billing to make a statistics test pass.
4. Keep prompt/completion/cache/input totals distinguishable; normalize once using settlement semantics and retained numeric metadata, never pricing inference.
5. Ensure committed numeric writes survive restart and replay idempotently, including async adjustments. Counter updates, source identities and publication/flush failure must have an explicit durable recovery rule.
6. Backfill surviving historical numeric rows in bounded resumable passes, with coverage/dedup that does not add known legacy aggregates twice or discard irrecoverable preexisting history.
7. Prevent application cleanup from deleting unrepresented source data; cover independent native ClickHouse TTL operationally. Storage errors/incomplete backfill must not be treated as complete coverage.
8. Keep actual log/list/storage facts and request-content authorization attached to surviving logs, not to aggregate existence.
9. Hosted tests must prove summary/channel/model/ranking/dashboard outputs remain unchanged after actual log deletion, then repeat backfill/recovery without double counting; include logs/export-disabled collection, refund/adjustment/zero-token events, semantic cache cases, second-edge filters, rename/deletion and masked viewers.
10. Database/schema changes require actual SQLite/MySQL/PostgreSQL fresh and released-version upgrade checks, startup twice, plus separate ClickHouse log-store backfill/cleanup/duplicate/TTL behavior. No mocks, source review, skipped DSNs or a single-engine pass establish that matrix. Final handoff needs exact versions, commands and final integrated SHA.

## Handoff

Early concrete findings were sent to root and pJ (write/backfill architecture owner). This file consolidates them without another broad discovery pass. No R24 production implementation has started in p2's scope. Compression remains a separately reviewed/frozen two-file change; retention/default work is separately owned by p4.
