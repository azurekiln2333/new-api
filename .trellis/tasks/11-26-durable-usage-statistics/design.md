# Durable usage statistics design

## Status

Root accepted the first storage foundation on 2026-11-26 after p4's frozen architecture review. Product scope is fixed in prd.md; software implementation proceeds in bounded slices. This document overrides conflicting proposals in frozen research. Foundation authorization is not production capture/backfill activation, reader cutover, database compatibility, cleanup readiness or a hosted pass.

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

## Accepted primary storage foundation

The normative field/type/codec contract is `research/schema-claim-contract.md` sections 1, 2.1-2.3, 3 and 4, with the overrides below. Preserve complete typed identity/payload comparisons and explicit knownness. The initial four primary-only tables are `usage_events`, `usage_hours`, `usage_channels` and `usage_channel_models`. No foreign keys to deletable entities or LOG_DB are introduced. Existing log readers remain active until their separate cutover slice.

The first writer owns exactly new `model/usage_event.go`, new `model/usage_event_apply.go`, the primary migration call in `model/main.go`, one consolidated new `model/usage_event_test.go`, and existing `model/migration_dialector_test.go`. Other owners must preserve these files. No source hooks, background scheduler, reader/API changes, imports/backfill, cleanup, epoch lifecycle, source registry, partition table or task sequence table belong to this slice.

### API and lifecycle

Publish exact Go signatures before source integration, following this behavior:

- `PrepareUsageEvent[Tx]` accepts an already-finalized immutable identity and numeric payload plus `source_pending` or `ready` disposition. It returns a receipt bound to the canonical event key, full source identity and payload digest. Tx operations use only the caller's transaction, never a global connection or nested independent commit.
- Explicit source-owner confirmation can move the same frozen pending/unknown occurrence to ready or a proven not-reported outcome. Repeated Prepare cannot promote it. A receipt attests prepared statistics, not successful finance or projection.
- `ApplyUsageEvent[Tx]` applies only source-ready facts to projection version 1. An already-applied replay is a no-op; pending, unknown and not-reported facts add nothing. Invalid identity/payload/version is a fixed conflict/error.
- Bounded due-ready/unapplied recovery retries only Apply. It never confirms source outcomes or calls funding, settlement, refund, token, quota-cache or subscription logic. Retry metadata uses a closed safe error category and cannot destroy the fact.
- No `BeginUsageOccurrence`, open receipt creation or pre-dispatch SQL write is required or implemented. The full finalized event fields remain available for later source bindings, without claiming an implemented epoch-registration contract.

Claim uses insert-do-nothing followed by `lockForUpdate(tx)` and full stored identity/payload comparison. Neither RowsAffected, generated ID nor LastInsertID establishes ownership. SQLite obtains the existing no-row writer reservation before any read; busy/snapshot/serialization failures roll back the whole attempt. A committed-but-unacknowledged operation retries the same identity, never a new UID.

Projection lock order is **event -> channel lifetime -> channel/model lifetime -> hour**. Create-if-absent and lock each applicable target, compare its complete dimensions after hash conflict, check every signed-int64 addition before updating, and atomically commit all eligible projections plus the applied marker. There is no SQL arithmetic that can silently overflow or a partial successful marker. Last-used is max under lock. Refunds keep positive magnitudes by kind and do not subtract from existing gross consume projections. Statistics never replay money.

### Indexes and text compatibility

Keep the draft's fact/time/self/channel indexes, unique event key, dimension keys and channel tail. Keep retry drain `(source_state, applied_version, next_attempt_at, id)`. Add global pending time-tail `(source_state, applied_version, kind, occurred_at, id)`; add a user-leading tail only when a later reader actually needs it. Reader tails will use the 1,001-row sentinel and fail not-ready rather than return partial totals.

Native request/upstream exact SQL text predicates remain authoritative. Byte digests must not exclude case/trailing-space matches unless byte equivalence is proven for each dialect/collation; checking text after an excluding hash is insufficient. The declared UTF-8 byte limits are accepted: reject oversize, invalid UTF-8 and unsupported NUL as an explicit uncovered occurrence, preserve source evidence, and never truncate or invent dimensions. This is a statistics-store compatibility boundary, not additional relay input rejection.

### Foundation validation

The consolidated real-engine suite covers identical replay, first-claim concurrency, full identity/payload conflict, pending-versus-ready confirmation, all-three-projection rollback/recovery, overflow with no partial write, knownness and timestamp maxima. Existing schema-stability tests exercise the actual additive migration, named indexes/order, second-run no-op and preservation of existing Log/QuotaData rows.

Hosted validation must use real SQLite/MySQL/PostgreSQL, fresh schema and the actual latest released schema/data upgrade twice. Source inspection, fixture definitions and unrelated prior database passes do not establish compatibility. No local executable checks are authorized. The foundation alone cannot satisfy the complete PRD retention guarantee.

## Accepted integration and cleanup decisions

Durability begins only after finalized numeric Prepare commits. On failure before that boundary, existing source behavior proceeds once; lost quantities without a surviving fact/keyed log/source payload remain irrecoverable and explicitly unknown. Never synthesize zero, current-price reconstruction or finance replay. First source-owner confirmation records the observed gross report outcome, not finance atomicity. Midjourney effective token identity and all numeric payload fields must be fixed before Prepare.

Frozen legacy quota_data is a disjoint dashboard baseline. Historical log imports feed exact facts/channel projections and do not automatically feed dashboard hours. New live events use their reviewed post-fence lineage. Unknown overlap remains unavailable until proved; no max/subtraction residual is exact history.

Relational cleanup may delete a retained row only after stable namespace+row ID or keyed live occurrence linkage, complete immutable payload comparison, ready source state, every applicable projection committed, and verified manifest/checkpoint. Delete only bounded exact verified IDs. Unresolved candidates keep their own rows. Unrelated active upgraded writer epochs need not seal before deleting a proven row.

ClickHouse's broad synchronous `<cutoff` mutation requires complete physical multiset/manifest coverage and a narrow no-late-write fence for that predicate until mutation completes. Otherwise keep deletion paused or implement a bounded manifest-aligned mutation. Native TTL is a separate operational gate. Row proof does not establish whole-history exactness, active-prefix completeness or automatic rolling-24-hour cleanup.

Capacity is a production activation/backfill gate, not a software implementation blocker. On an authorized representative non-production copy measure row+indexes+projection bytes, peak migration/WAL/index-build space and live growth; provision measured peak plus an explicit reserve before backfill. The draft's approximately 27 KiB maximum text per row times 1.68M rows is approximately 46 GB text-only at maxima, not a typical-size estimate. No saving or immediate PostgreSQL OS-space reclamation is claimed.

## Later slice gates and rejected alternatives

Before those separate edits, root assigns the exact finalized source seams (including conditional task CAS/fee/MJ outcomes), stable source/partition/task identity and evidence protocol, recovery scheduling, bounded backfill/checkpoint/multiplicity, all-reader filter/privacy parity, and application cleanup guards. Any epoch lifecycle is a separate reviewed design; the foundation does not create or depend on it. Missing pre-Prepare evidence remains a coverage gap even if coarse epoch loss detection is later added.

Rejected: mandatory per-dispatch Begin as a hidden latency cost; logs-only reconstruction after deletion; grouping all lifetime facts on every channel request; hash-based deduplication that drops legitimate multiplicity; treating successful AutoMigrate as cutover; requiring unrelated live epochs to seal before row-proven relational cleanup; skipping coding until production disk measurements exist; claiming the foundation passes full end-to-end statistics acceptance.
