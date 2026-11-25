# Durable usage statistics design

## Status

Planning draft owned by root. Product scope is fixed in prd.md. The durable write/backfill proposal from pJ and adversarial review from p4 must resolve the open technical contracts below before production implementation. No compatibility or completed-migration claim is implied by this draft.

## Boundary and existing mechanisms

The behavior gap is at collection and persistence, as well as reads: current logs are optional/retained and quota_data has a lossy memory flush. Authoritative implementation belongs in primary `model/` transaction code, the existing consume/task settlement call sites, and the existing statistics readers. A frontend cache or periodic scan of retained logs cannot provide durability.

Reuse existing quota/event/task transaction mechanisms when their identity and commit semantics fit. New persistent numeric state is justified by missing durable source identity and the inability of hourly quota_data to answer exact-second/text filters. Keep log content lifecycle, billing arithmetic, permissions and route shape unchanged.

## Fixed data and read contracts

1. Numeric facts live on primary DB, never solely in LOG_DB or memory. Optional log publication cannot be the authority for committing them. No distributed transaction across DB/LOG_DB is assumed.
2. A fact has a server-generated or authoritative business-transition identity, kind, event second, numeric measures and necessary historical dimensions. RequestID alone is not identity: one task/request can legitimately submit, adjust and refund.
3. Preserve event measures separately: prompt, completion, cache read, normalized cache write, input denominator, gross consume quota, refund amount and consume-event count. Existing views keep their present semantics; any net metric must be named separately.
4. Durable per-event state supports current exact-second and text/request filters. Aggregated projections support channel/model lifetime totals and existing hourly dashboards/rankings, avoiding full-log scans. Aggregation and the event-applied marker commit together in one primary transaction.
5. Replay is idempotent. A durable fact awaiting projection remains pending across restart; projection failures never clear its only source. Concurrent delivery either inserts once or observes an existing committed event.
6. Store no bodies/prompts/credentials/arbitrary metadata. Historical username/token/model/group/node/request IDs are allowed only because current public query contracts require them; use bounded fields and measured indexes. Do not inflate every aggregate row with unrelated metadata.
7. Log pagination/export/storage and request-body surviving-link checks continue to use actual retained logs. Numeric persistence cannot authorize a request-content read.

## Readers to migrate

| Surface | Required source and preserved behavior |
| --- | --- |
| `/api/channel/usage` | Durable channel consume projections; raw prompt+completion and gross quota/count. |
| `/api/channel/:id/usage` | Durable channel/model projections plus current catalog/abilities; historical last-used time. |
| `/api/log/stat`, `/api/log/self/stat` | Numeric facts/projections with the existing complete filter semantics; server self range/access guards. |
| Dashboard `/api/data`, flow, rankings/model drawer | Durable projections with a defined legacy coverage transition; keep existing cache-inclusive token semantics. |
| `SumUsedToken` | Deliberately migrate the remaining exported helper or remove after proving no compatibility need; do not leave a hidden log aggregate. |
| Existing independent account/budget/subscription/performance values | Existing authoritative stores; inventory and test independence without replacing their business semantics. |

## Historical transition

Preserve legacy quota_data as an immutable baseline/provenance source until overlap is resolved. Blindly adding log backfill to those rows double-counts; replacing all rows loses older history. The final proposal must select one authoritative coverage source per measure/dimension/time segment, preserve irrecoverable aggregate-only history and expose exact/partial coverage where dimensions are absent. No residual computed by an unexplained max/subtraction heuristic may be presented as exact.

Relational source IDs may support checkpoints only with a stable database/source identity. ClickHouse id=0 and synthetic display ordinals cannot. Historical identical rows can be distinct legitimate events, so content hash alone cannot discard multiplicity. The proposal must specify a repeatable source boundary and multiplicity policy, including late/duplicate remote publication and ambiguous acknowledgements.

Backfill batches commit imported facts, apply state and checkpoint atomically on the primary DB. Independent source reads can be retried; an uncertain primary commit is recovered by event identity/checkpoint. No startup full-table scan or unbounded in-memory import.

## Cutover and cleanup

Schema creation is additive and idempotent. Reader switching follows verified coverage, never just successful AutoMigrate. All serving writers must support the new event contract before a deployment claims complete coverage; old quota_data buffers and old writers cannot remain silently active.

Keep the production usage-log cleanup pause. The implementation must guard application cleanup against uncovered numeric source rows. Native ClickHouse TTL is separate: record the required pause/coverage procedure before backfill, and do not silently modify production settings. After final hosted validation, operational backup/cutover verification remains distinct from software test success.

Rollback preserves numeric facts/provenance and source logs until a compatible reader is available. After source logs are removed, rollback to a logs-only reader cannot preserve statistics; document this explicitly rather than suggesting a feature flag recreates deleted history.

## Open architecture gates

- Exact minimal schema, unique keys and indexes with SQLite/MySQL5.7/PostgreSQL9.6-compatible operations and numeric overflow behavior.
- Exact synchronous settlement seams and error/recovery ownership. Current funding/token commits are split and log hooks can run after failed settlement; do not mislabel gross observed usage as accounting-exact.
- Exact asynchronous submit/terminal CAS enqueue identity, including distinct positive adjustment/refund events and duplicate callbacks.
- Legacy quota_data overlap reconciliation without inventing unknowable dimensions or double counting.
- ClickHouse snapshot/multiplicity/cursor contract and cross-store late-event reconciliation.
- Transition readiness signal and application cleanup condition, including old writers/native TTL.

These gates are implementation blockers, not reasons to ask the user to repeat the already authorized scope. Root resolves them using pJ research and p4 review, then writes the final executable contract here.
