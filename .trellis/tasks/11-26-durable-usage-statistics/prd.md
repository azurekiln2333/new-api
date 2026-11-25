# Durable statistics independent of usage log retention

## Goal and authorization

Deleting usage logs must not reduce business statistics, model token totals, channel totals, dashboards or rankings. Persist the numeric facts and necessary dimensions in the primary database, independent of optional diagnostic logs, and recover the available history without double counting.

The user explicitly requested all statistics, a Trellis plan before implementation, existing Pi implementation/review, and hosted-only validation. This child implements R24 of the parent task. Existing authorization covers implementation and the normal commit/push/review process once the technical design passes review; no repeated process approval is needed. The child remains planning until the architecture issues in design.md are resolved.

## Confirmed background

- Source inventory: `research/backend-statistics-inventory.md`. Direct business aggregates of logs are in `model/channel_usage.go:41,65` and `model/log.go:825,922`. They affect channel cards/details, model breakdowns, last-used time and log summary cards.
- `quota_data` already stores dashboard/flow/ranking/model-drawer data, but its collection depends on logging/export switches, a process-local five-minute buffer and fallible non-atomic flushes. Merely redirecting channel reads to this table does not satisfy durability.
- Historical `quota_data` and surviving logs overlap without event provenance. Some older logs may already be deleted. Existing aggregate history must not be discarded or added twice.
- A separate production investigation reported about 1.68 million logs / 2.7 GB and a full 20 GB data volume. It reported a request-content backup and TRUNCATE outside this task; backup location is `/home/azurekiln223/pgexport/`. These are supplied observations, not production access by this session.
- The user wants to retain logs for 24 hours after this work passes. Until then, usage-log cleanup remains paused; this session performs no production deletion, settings change or compaction.

## Requirements

| ID | Observable contract |
| --- | --- |
| S1 | Inventory every statistics/ranking/model-token surface and its actual source. Business usage totals must read durable primary-database statistics. Independently stored wallet, subscription, budget, check-in and performance domains retain their existing authority. |
| S2 | Channel totals, channel/model breakdowns and last-used timestamps remain unchanged when their logs are removed. Dashboard charts, flows, public totals and model/user rankings have the same independence. |
| S3 | Log-page numeric summary cards also survive log removal. Preserve current user scoping, time boundaries, text/request filters, gross consume quota, cache/input semantics and recent 60-second rates. Actual log rows, pagination counts and export counts describe retained logs and may decrease. |
| S4 | New statistics are collected with consume logging, data export and request-content capture individually or jointly disabled. A crash, retry or multi-node concurrency cannot silently lose or double-apply a committed statistics event. Failed persistence must remain recoverable and visible. |
| S5 | Preserve distinct prompt/completion, raw total, Anthropic-normalized usage, cache-read/write and input measures. Preserve consume-event count rather than silently changing it to logical-request count. Refunds and adjustments retain their distinct event kinds and current displayed gross-consume semantics. |
| S6 | Backfill surviving historical records in bounded, resumable and idempotent batches while new traffic continues. Re-running after success or interruption must not add the same event twice. Historical names, model mappings, times and node dimensions are not recomputed from current settings. |
| S7 | Preserve known older aggregate history with explicit coverage provenance. Missing/deleted history and fields unavailable in legacy aggregates are not invented, treated as verified zero or claimed to be reconstructable. Unknown overlap must be resolved before publishing a combined total. |
| S8 | Persist numeric measures and only the dimensions required by existing queries and event identity. Do not copy request/response bodies, prompts, credentials, arbitrary `other` JSON or audit payloads into the statistics store. Bound row/index growth and avoid a second raw-log archive. |
| S9 | Application cleanup cannot erase source records before their statistics are covered. Separately configured relational/ClickHouse logs, native ClickHouse TTL, rolling old writers and old in-memory buffers have an explicit cutover procedure. Do not claim an application gate prevents external SQL deletion or native TTL. |
| S10 | Primary SQLite, MySQL >= 5.7.8 and PostgreSQL >= 9.6 remain supported. Migration/startup is idempotent on fresh and representative latest-release databases; cover separate relational and ClickHouse source stores. |
| S11 | Existing server access boundaries, ADMIN/ROOT and self visibility, ordinary-user partial masks, cache identity and no-secret projections remain intact. Statistical persistence grants no new access to surviving request content. |

## Acceptance criteria

- [ ] The endpoint/UI inventory maps every usage total and ranking to an independent source, and identifies actual retained-log/storage operations explicitly.
- [ ] Hosted integration fixtures record numeric events, capture every affected summary/ranking/channel/model result, delete all associated logs, and obtain the same results afterward. Retained-row lists correctly become empty.
- [ ] Collection works with logging/export/content disabled, cross-node duplicate delivery, failed writes/projections, process restart and replay. No successful event is applied twice; failures leave durable recovery state.
- [ ] Mixed raw/Anthropic usage, zero-token fees, diagnostics, asynchronous submit/positive adjustment/refund and repeated request IDs preserve their existing measures and event cardinality.
- [ ] Inclusive second boundaries, recent 60-second intersections, fuzzy/exact filters, historical names, renamed/deleted entities and server self scoping remain correct.
- [ ] Historical backfill is repeatable after interrupted reads/commits, interleaved live writes and late events. Legacy aggregate overlap and missing coverage are explicit and tested; neither blind addition nor destructive replacement is accepted.
- [ ] Cleanup readiness is false while source coverage or required delivery is incomplete. Native ClickHouse TTL prerequisites and outside-system deletion limits are documented accurately.
- [ ] Real SQLite/MySQL/PostgreSQL fresh and released-upgrade startup twice, separate-log-store cases, ClickHouse source identity/multiplicity and focused race tests pass on the final exact hosted SHA. Record versions, commands and results; skips are not passes.
- [ ] User receives a clear completion/readiness statement before returning to the separate production cleanup session. No completion or cleanup-ready claim while any required gate remains open.

## Scope limits

No production access or cleanup, no pricing/reward arithmetic changes, no arbitrary wallet/accounting redesign, no body backfill, and no request-content sweeper throughput change. Integrating numeric events with existing business transaction boundaries is in scope where needed for durability; altering monetary outcomes is not. Independently retained performance/audit/storage facts do not become lifetime usage totals.

## Technical decisions still under review

The requested product scope is settled. Event enqueue boundaries, historical overlap reconciliation, ClickHouse identity and the all-writer cutover must pass architecture review before implementation. Their concrete design and rejected alternatives belong in design.md, not implicit coding choices.
