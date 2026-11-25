# R24 write/backfill architecture — source-only proposal

## Status, boundary and recommendation

- Owner: pJ. Exclusive output: this file. Planning input for `.trellis/tasks/11-26-durable-usage-statistics`; not implementation approval.
- Read `.agents/rules/billing.md` in full and `research/backend-statistics-inventory.md`. All findings are source-only. No production edits, local checks/builds/formatters, database access, settings changes, cleanup, or Git/index writes were performed for this research.
- **Smallest viable direction:** a thin, immutable, primary-DB numeric event store, with durable prepare, idempotent hourly/channel-model projections and explicit migration/coverage state. Keep exact event seconds and filter dimensions in the facts; an hourly table alone is insufficient. Keep historical `quota_data` as a separate provenance baseline, not an additive backfill target.
- Preserve existing **gross consume-event** statistics. These are not an authoritative net billing ledger: a positive task adjustment is another consume event; a refund does not subtract from current consume summaries. Funding, token balances, budgets and existing account counters remain their own authorities.
- Do not solve this by making every statistics write replay `BillingSession.Settle`, refund, token, cache or budget operations. Statistics replay must perform **numeric publication only**.
- A log-writer-only hook after business commit cannot provide crash-safe source capture. Source transaction seams and the currently non-transactional synchronous paths must be distinguished below. Storage-failure policy is an explicit activation decision, not something a worker should invent while implementing.

## 1. Observable contract to preserve

The companion inventory is the endpoint/filter source of truth. In particular:

1. Channel cards/details count consume **rows/events**, sum prompt and completion separately, use their raw sum for total tokens, and need exact `MAX(created_at)`. Preserve the current empty-model exclusion when building detail totals.
2. `/api/log/stat` and `/self/stat` retain their full server-scoped `LogFilter`: historical username/token name, model/group, channel, request and upstream request IDs, literal substring default, existing exact-mode model/username wildcard rule, and inclusive exact-second boundaries. Their type is always consume regardless of requested list type.
3. RPM/TPM cover the last 60 seconds intersected with all requested filters. TPM uses raw prompt+completion, not ranking token semantics.
4. Dashboard/rankings `token_used` is prompt+completion plus nonnegative cache read/write **only for explicit Anthropic semantics**. Cache-hit denominator is prompt plus those Anthropic caches; it excludes completion. Store these ingredients separately. Do not add cache-write breakdowns again or infer semantics from model/pricing.
5. Existing `quota_data` time filtering applies to hour-start rows. Moving its readers to exact-event filtering is a separate public behavior change; the minimal replacement retains their hour-bucket predicate and ranking period/bucket rules. Log-stat readers remain exact-second.
6. Positive async adjustment adds count=1 and its positive quota, generally zero normal tokens. Refund events hold a positive refund magnitude but are excluded from existing consume totals. Logical-task count and `users.request_count` are not replacements for this event count.
7. Diagnostics (`controller/channel-test.go`) and successful violation-fee reporting also contribute consume events today. Zero-token and zero-quota consume events are valid; positive-token ranking HAVING remains a reader rule.
8. Retained-log lists/counts/exports, physical storage totals, cleanup progress, and surviving-log request-content authorization remain attached to actual logs. A durable usage fact must never grant access to deleted request bodies.

## 2. Proposed data boundary

Names below are proposed contracts, not existing APIs or approved schemas.

### 2.1 Primary `usage_events`: canonical numeric facts

One row represents one **reported consume/refund occurrence**, not one request ID. Its minimum payload is:

- Internal primary key; versioned unique `event_key`; source kind/namespace; source operation identity and operation sequence where applicable; immutable payload digest for replay/conflict checking.
- Event kind (`consume`, `refund`) and source category (`relay`, `task_submit`, `task_adjust`, `diagnostic`, `violation_fee`, `legacy_log`). Category is not a new filter requirement but makes provenance auditable.
- Exact `occurred_at` seconds, separate insertion time and collection epoch. Late task completion belongs to its adjustment/reporting time, not its original submission hour.
- Stable user/token/channel IDs plus the **historical** username, token name, model and group used by reporting. Keep request/upstream request IDs as filter dimensions, never as the unique key.
- Writer node and submission/export node when available. Existing async log writer node and exported task node can differ; preserve the relevant value for each projection. Unknown old values remain unknown.
- Nonnegative quota magnitude; prompt and completion tokens; normalized cache-read/cache-write totals; explicit usage semantic; optional explicitly sourced total-input value plus known/unknown provenance. Do not conflate absence with a verified zero.
- Projection eligibility/provenance: live events can feed both current families; imported legacy log facts feed log/channel-summary parity, not automatically the preserved legacy dashboard baseline.

Use checked 64-bit aggregate arithmetic and deliberate cross-dialect BIGINT storage. Do not change single-request quota validation, saturation or pricing. Long string dimensions should not become a MySQL-5.7-incompatible giant composite unique index. Use a versioned length-framed canonical tuple/digest for a bounded bucket key, retain full dimensions, and verify payload equality on a key collision. Do not derive a source namespace from a DSN or record credentials/bodies in this store.

`event_key` uniqueness is authoritative. Replaying the same key with the same immutable payload is a no-op. The same key with different quota/dimensions/tokens is a conflict, not an overwrite or a second increment. Avoid treating driver-specific affected-row counts as the sole proof of first insertion.

### 2.2 Primary projections and durable pending state

Use three projections rather than rereading every event or every historical model row for channel cards:

- `usage_hours`: existing dashboard dimensions (hour, user ID/name, model, group, token ID, channel ID and export node), plus projection version/lineage.
- `usage_channels`: one lifetime row per channel/projection version with consume count, gross quota, raw prompt/completion and exact maximum consume event time.
- `usage_channel_models`: channel ID + historical model, the same measures and exact maximum consume event time. Keep an empty-model row for parity; the detail reader retains its current exclusion of empty model names. The standalone channel reader uses `usage_channels`, not a scan/group of all model rows.

Store consume-event count, gross quota, raw prompt/completion, ranking-token ingredients/totals and cache ingredients distinctly. Exact timestamps, historical text filtering and high-cardinality request IDs stay in `usage_events`; do not promise an hourly row can answer those queries.

**Chosen protocol:** `PrepareUsageEvent[Tx]` commits the immutable numeric obligation first, even if projection is unavailable. `ApplyUsageEvent[Tx]` updates every eligible projection and its event-applied marker in the **same primary transaction**. The source transaction can prepare and apply together when appropriate, but a failed projection must never erase the only durable prepared payload. A bounded worker retries only prepared statistics.

Projector transaction: `lock event -> verify source-ready and payload -> check projection version -> ensure/lock hourly, channel lifetime and channel/model buckets in deterministic order -> checked additions / exact max timestamp -> mark applied -> commit`, all through its `tx`. Failure rolls back all bucket mutations and leaves the canonical prepared fact pending. Duplicate workers observe the applied marker and do nothing. Use `lockForUpdate(tx)`; preserve the supported SQLite transaction/writer-lock behavior.

Never use only `MAX(event_id)` as completion: concurrent transactions can allocate IDs and commit out of order. The worker revisits unapplied rows. Do not publish aggregate state in Redis before SQL commit.

Initial readers:

- Log-stat summaries and `SumUsedToken` query source-ready durable consume facts, preserving exact time/text predicates and prompt/completion semantics.
- Channel cards fetch the requested one-row lifetime channel projections; details fetch that channel's model projections plus current catalog/abilities. Do not group lifetime event history on either route. p5's read contract may add an indexed **bounded** pending tail in the same SQL statement snapshot; beyond that bound return explicit pending/unavailable coverage, never a partial total or an unbounded event-history scan. Log statistics can read ready facts while projection retries are pending.
- Dashboard/flow/rankings query hourly projections plus their unapplied ready-fact tail and the disjoint legacy baseline in section 6, preserving existing grouping/masking/name-resolution behavior.
- Applied buckets plus pending tail are evaluated in **one SQL statement snapshot**, for example a derived `UNION ALL` of bucket measures and grouped ready facts with `applied_version != active_version`, followed by outer grouping. A projector commit is seen as either pending contribution or applied contribution, not both/neither. Two unrelated reads under PostgreSQL READ COMMITTED are insufficient. Portable derived tables avoid requiring MySQL-8 CTEs/window functions.
- A new projection version builds separate buckets and receipt state; do not reset a marker and add into old buckets. The first implementation needs only one active version; concurrent rebuild versions would require explicit per-event/version receipts.
- Retain facts unless a separately approved compaction proves it preserves every existing exact-time/text/request filter. A 60-second hot ring plus old hourly totals is not equivalent.

No volatile flush is a correctness dependency. Source-pending conditional candidates are not part of the ready-fact tail and never auto-promoted by the projector.

### 2.3 Primary migration/coverage state

A durable migration record identifies source namespace, schema/normalization version, collection epoch, legacy-writer fence, source manifest/high-watermark, partition checkpoints, imported count/sums/digests, verification status, and reader cutover state. Progress is committed with imported facts/projections in each batch.

Keep coverage separate for (a) retained-log-derived historical facts, (b) preserved historical dashboard baselines, and (c) post-fence live source capture. A completed scan of available rows is not proof that deleted/log-disabled earlier history ever existed.

### 2.4 Exact schema/claim contract ownership

p5 owns the exact schema/index/claim/checked-apply contract in `research/schema-claim-contract.md`. That reviewed proposal supersedes mechanical table/key sketches in this file. The source API below requires only a stable fact key, immutable numeric input, source readiness distinct from applied status, per-task transition claim identity, durable pending payload, three projections (hourly, channel lifetime, channel/model lifetime), and migration/coverage state.

Source-side constraints for that contract: no body/credential/arbitrary log JSON payload; preserve historical string bytes rather than silently truncate; compare full canonical dimensions on digest conflicts; signed-64-bit checked numeric measures; exact event time frozen once; duplicate keys with changed numeric payload are visible conflicts. The source boundary does not depend on MySQL affected-row quirks, a process mutex, budget activation, or optional request-content capture.


## 3. Event identity: beyond RequestID

| Source | Recommended identity | Important distinction |
| --- | --- | --- |
| Synchronous relay/audio/WebSocket reporting | Server-generated reporting-occurrence ID, allocated once and retained by the finalization object; channel/attempt and turn/usage sequence distinguish legitimate multiple reports | Do not generate a fresh ID on every persistence retry; do not reuse a single request ID for multiple WebSocket turns or a separate fee |
| Diagnostics | Diagnostic run/attempt reporting-occurrence ID | Automatic retry may reuse a pinned credential but must not collapse two actual reportable consume occurrences |
| Violation fee | Separate fee operation identity derived from/stored with that fee occurrence | It can share request ID with a failed/refunded normal request |
| Async submission | Server-generated submit-occurrence key, persisted and associated with the winning primary task insert | Public task ID alone is not sufficient; a streamed report without a persisted task still retains its preallocated occurrence key |
| Async adjustment/refund | Primary task identity + durable monotonic statistics/charge-operation sequence and event kind | Repeated polling is no new event; a later legitimate positive delta is. Target quota alone is not an operation ID |
| Relational historical log | Persisted log-source namespace + actual row primary key | IDs are local to a store. Display ordinals and DSN text are not identities |
| ClickHouse legacy log | Source namespace + canonical numeric/dimension tuple digest + occurrence ordinal within that tuple's frozen multiplicity | `id=0`, request ID or a tuple hash alone would collapse legitimate rows |

**Mandatory live linkage:** every optional consume/refund log written after activation carries its canonical `usage_event_key` and normalization version in admin-only metadata. Allocate the occurrence key before prepare; reuse it for prepare retries, source confirmation, log delivery and any later recovery. Missing linkage after activation is an invariant/coverage error, not permission to generate a fresh legacy identity. Existing non-admin sanitization must remove this metadata; no public DTO or content-read authority is added.

The importer must verify/link a keyed log to that primary fact, not create `legacy:<row id>` as a second fact. If the primary fact is missing after an uncertain prepare, keyed log evidence may recover it only with its frozen numeric payload/version and source registration verified; otherwise mark a coverage conflict. ClickHouse duplicate delivery of one keyed log never creates another business event. Log-disabled facts remain valid without any log. Retain task operation identity independently of budget activation and RequestContent capture.

For async non-budget tasks a small durable operation sequence/receipt is required. It may be a task statistics revision or a separate receipt row guarded by the existing task row lock; it must not be a process counter, timestamp, `MAX(quota)`, or mutable JSON equality guess. Prefer one explicit task-operation receipt contract over adding unrelated dedupe systems at every layer.

## 4. Source seams and durability: do not disguise a post-commit gap

### 4.1 Fixed source API: finalized numeric prepare, then statistics-only apply

The smallest source API lives in the root `model` module; it does not change `FundingSource`, `BillingSettler`, relaykit or tokenkit. These are proposed signatures for implementation review, not existing methods:

```go
PrepareUsageEvent(ctx context.Context, input UsageEventInput, state UsageSourceState) (UsageEventReceipt, error)
PrepareUsageEventTx(tx *gorm.DB, input UsageEventInput, state UsageSourceState) (UsageEventReceipt, error)
ConfirmUsageEvent(ctx context.Context, receipt UsageEventReceipt, outcome UsageSourceOutcome) error
ConfirmUsageEventTx(tx *gorm.DB, receipt UsageEventReceipt, outcome UsageSourceOutcome) error
ApplyUsageEvent(ctx context.Context, receipt UsageEventReceipt) error
```

- `UsageEventInput` contains the stable occurrence key/source/epoch, event kind and frozen event second, IDs and necessary historical strings, request/upstream IDs, separately normalized numeric ingredients, known/unknown flags, and projection-family eligibility. It contains **no** `PriceData`, `OtherRatios`, request body, `LogOther` map, credential or settlement callback. Existing settlement already supplies the numbers; preparation never recalculates prices or usage.
- `UsageEventReceipt` contains the canonical key and immutable payload digest, plus source operation identity needed to recover the same occurrence. It is a value handed through the actual caller, not a current-request-context singleton: one request can have submission, adjustment, refund, fee or several realtime-turn receipts.
- Initial source state is `ready` when that source has already decided a gross reporting occurrence exists, or `source_pending` when the final numeric candidate is known but the existing conditional source outcome is not. Both persist the full frozen numeric payload. Repeated prepare must **not** auto-promote an existing pending/unknown row merely because a caller passes `ready`; explicit caller-owned `ConfirmUsageEvent` and source evidence are required. Confirm may move only a matching pending receipt to `ready`, `not_reported` or `source_unknown`; it cannot replace numbers or silently turn a conflicting payload into a new key. Ready cannot later become not-reported as a substitute for a refund.
- `ready` and `applied` are separate states. The projector sees only ready facts; it updates hourly + channel lifetime + channel/model lifetime projections and its applied marker together. `source_pending`/`source_unknown` are never auto-promoted by age, lease expiry, restart or current task quota.
- `PrepareUsageEventTx` uses only the supplied `tx`. Async callers commit source CAS/receipt/numeric obligation together. `PrepareUsageEvent` supplies the stats-only transaction for synchronous reporters. The caller attempts apply after confirmed prepare, but apply failure cannot discard the prepared payload or trigger finance replay.
- Duplicate prepare/confirm/apply with the same receipt and payload is safe; same key/different immutable payload or inconsistent terminal outcome is an invariant error. The schema/claim owner specifies the unique insert/authoritative-lock mechanics.
- Freeze event time once when the reporting occurrence is finalized and pass that time through optional log output; never call `now` again on retry, move an adjustment to its submission time, or infer historical time from flush time. New keyed log rows and numeric facts use this identical reporting second. Imported history keeps the stored old second exactly.

### 4.2 Synchronous seams and failure ownership

Source evidence:

- `service/text_quota.go:PostTextConsumeQuota` finalizes usage, updates usage counters, calls `SettleBilling`, logs settlement errors, and still calls `RecordConsumeLog` with the intended quota.
- `service/quota.go:PostWssConsumeQuota` and `PostAudioConsumeQuota` have the same distinct reporting/settlement phases. Multiple realtime reports can share one request ID.
- `BillingSession.Settle` commits funding before possible token failure; `delta == 0` can finish without another SQL funding mutation. Its settled flags are process memory, not statistics receipts.
- `WalletFunding.Refund` explicitly cannot be retried safely. `postConsumeQuotaWithResult` exposes funding-applied before token-applied. A statistics worker never calls these methods to repair numbers.

**Concrete normal-report hook:** in `PostTextConsumeQuota`, after usage/tiered/tool calculations and the log-model normalization are final but before used-quota side effects/`SettleBilling`, build `UsageEventInput` from that finalized summary and call `PrepareUsageEvent(..., ready)`. Move only the tiny log-model normalization/frozen numeric-input construction above that boundary; do not move or rerun pricing or change monetary ordering. The existing later `RecordConsumeLog` receives the resulting receipt/time; it does not create the canonical occurrence. Apply the same pattern after final zero-usage/fixed-price quota decision in `PostAudioConsumeQuota` / `PostWssConsumeQuota`.

These are **gross observed/reporting obligations**, not proof that wallet and token both committed: the existing normal reporter emits a consume event even on a settlement error. A failed `SettleBilling` does not cancel this ready observation. Keeping this distinction avoids a billing rewrite.

| Failure point | Exact owner/action | Prohibited inference |
| --- | --- | --- |
| Final numeric prepare fails after upstream usage | The source caller retains the same input/key, retries only prepare under a bounded context independent of client cancellation, and surfaces a statistics-persistence incident. Existing monetary success/error policy proceeds once; this contract does not block/replay settlement or rewrite an already-streamed response. The source window cannot be certified complete | Do not claim committed statistics, switch to a new key, invent zero or rerun `SettleBilling` |
| Prepare acknowledgement is unknown | Read the same key/digest on primary storage; matching durable pending/ready state proves prepare. If storage is unavailable, keep outcome unknown and retry numeric persistence only | A returned SQL error is not proof of rollback; optional log presence alone is not proof of numeric commit |
| Prepare committed, immediate projection fails | Prepared payload is durable; worker/caller retries apply. Use pending-aware numeric reads or an explicit unavailable/pending result, never a falsely complete rollup | No debit/refund/token/cache/budget replay |
| Optional log publication fails after prepare | Keep committed fact/receipt. If log delivery is retried it retains the same key/time. Required statistics and reporting plugins do not depend on `LogConsumeEnabled` / `DataExportEnabled` | Do not roll back usage, create a replacement occurrence or block a fact merely because no log exists |
| Process dies **before finalized numbers were durably prepared** | Exact quantities may be unrecoverable. The source/writer window is unknown, not committed statistics. With primary unavailable and logging/export off there may be no durable per-event gap marker or payload at all | Do not describe an open receipt, an in-memory error flag or lost output as an exactly recoverable event |
| Process dies after prepare | Prepared full numeric payload survives. Resume only statistics apply/linkage from the same receipt | No need to repeat upstream work or money operations |

**Explicit recovery boundary:** this contract covers finalized numeric observations **only after durable prepare**. A predispatch receipt contains no final usage and cannot make earlier lost output recoverable. The minimal implementation does not add a mandatory primary round trip before every upstream request solely to claim otherwise. Existing billing proceeds under its existing policy if statistics preparation fails; therefore an unknown pre-prepare window is a real residual, not just projector lag. With primary down, a promise to write an incident to that same primary is not durable recovery.

**Recommendation: include a once-per-serving-writer durable epoch in coverage state**, not a per-dispatch database receipt. It is a coarse fail-closed loss-detection/control record, not a durable copy of usage and not an exact-recovery mechanism. Root still owns acceptance of this proposal. Merely adding its table is insufficient: safe sealing requires the explicit lifecycle contract below, which is not claimed to exist or be verified in the current source.

1. Persist an open epoch bound to writer/process instance, source roles and activation generation **before** that writer can start covered relay/task/diagnostic/fee work. Registering only the HTTP server is insufficient if background task/report writers already run. A process that cannot register cannot claim covered serving; an unregistered/old writer makes deployment completeness false.
2. Every admitted work item that can later produce a reporting occurrence joins that process's in-memory epoch work/finalizer tracking **before** it can run or detach. This includes upstream retries, realtime sessions/turns, background polls, fee followups and delayed callbacks. A callback that creates another callback transfers/tracks ownership before its parent completes. Detached work cannot escape the drain barrier.
3. A prepare failure, uncertain source outcome or unfinished numeric payload sets a sticky incomplete latch/pending item. It is cleared only by proving the same occurrence's durable prepare or known-no-report outcome; a queue length, heartbeat, successful later request or timeout does not clear it. If the process dies, all this memory is lost but its durable epoch remains open, conservatively unknown.
4. To seal, first fence **all** admissions for that writer/source generation, then wait for its tracked upstream work/finalizers and numeric retries to finish. Verify no dirty/unknown item remains and all prepared obligations are committed and applied/verified. Commit the sealed state while the admission fence stays closed. An ambiguous seal acknowledgement is resolved by reading that same epoch; never resume on the assumption that the seal failed.
5. A sealed generation cannot admit or publish new source work. Resuming service requires a newly committed open epoch first. Existing callbacks cannot be rebound to that new epoch to hide incompleteness of the old one. Restart, lease/heartbeat expiry and another process draining the prepared-event queue **never** seal a crashed epoch.

**Can it be sealed safely without durable per-request markers?** Only for a **clean, closed-world controlled drain** under the above in-process lifecycle barrier. The clean process can attest that every admitted source callback reached a known durable outcome before it sealed. Durable per-request markers are not required for that specific attestation: a crash before it can attest leaves the durable open epoch unknown instead. However, SQL cannot infer that attestation from the visible event rows, a watermark or an empty projector queue. Without comprehensive lifecycle tracking/fencing, automatic sealing is unsafe and the epoch must remain open. Current research has not implemented or validated that tracking.

A crash, an untracked detached writer, a primary outage with lost numeric payload, or a failed/uncertain finalization makes the epoch unknown and blocks relevant coverage/deletion readiness; the epoch does not repair the quantities. This deliberately sacrifices automatic cleanup progress for honesty. It cannot certify arbitrary time prefixes of an active epoch or promise unattended rolling 24-hour cleanup without an additional reviewed coverage protocol. A planned clean drain may seal a whole generation and open the next while upgraded traffic resumes. Late finalizations are bounded by that actual drain/fence, never an assumed lease timeout. This is a deployment/design prerequisite; it performs no production pause or setting change now.

Unknown epochs/windows remain a **hard coverage/deletion gate** until trustworthy keyed numeric source evidence resolves their relevant coverage. If lost quantities cannot be recovered, keep the gap explicit and do not call the whole history exact or cleanup-ready. Account totals/current prices cannot repair it, and operator acknowledgement alone is not recovery. An open marker identifies possible loss but never recovers absent numeric output.

This limitation must remain in implementation/rollout handoff. A normal committed numeric obligation is replayable; every possible already-forwarded upstream response is not. This is consistent with preserving billing outcomes and not building a new finance ledger.

**Conditional source hooks:** `ChargeViolationFeeIfNeeded` prepares a source-pending numeric candidate before `postConsumeQuotaWithResult`; it confirms ready only on the same full success branch that currently records the fee (error means not-reported, even when funding partially applied). A crash between monetary outcome and confirmation is source-unknown, not permission to promote or re-debit. Legacy Midjourney reports only when `billingApplied` is true; token ID can become zero on partial token failure, so its final `UsageEventInput` is built from that returned/persisted effective reporting state immediately before optional logging. Existing task billing markers/occurrence identity can identify an unresolved operation, but they are not proof that a split monetary outcome committed. Do not treat `billingErr == nil` as the MJ reporting predicate or change which partial outcomes count.

For diagnostics, prepare the finalized selected-key report in the actual `controller/channel-test.go` reporting branch, before `RecordConsumeLog`, with its own occurrence key; do not introduce a funding call. `RecordConsumeLog` and `RecordTaskBillingLog` become optional render/delivery adapters for the supplied receipt and frozen time, maintaining plugin context and mandated admin-only fact linkage even if logs are disabled. Remove their active-epoch legacy `LogQuotaData` collection so each new occurrence is not exported twice; old nodes/buffers are handled by the rollout fence.

### 4.3 Existing committed async seams

| Exact existing method | What already commits together | Proposed numeric integration |
| --- | --- | --- |
| `model.InsertTaskWithSubmissionCharge` in `model/log_request_detail.go` | Primary task insert inside request-content fence/link callback; optional tracked submission charge | Pass the prepared submission input into the primary insert callback. Tracked insert prepares it ready on the same `tx`; untracked insert prepares it source-pending. Capture still runs with body capture off/failed/no snapshot |
| `model.InsertTaskWithBudgetSubmit` / `insertTaskWithBudgetSubmit` in `model/channel_budget_adjust.go` | For a linked reservation: task, funding delta, token delta, durable desired budget revision | Prepare the submission fact on the same `tx`, before commit; apply in that tx when available or leave durable pending apply. The event covers total reported submission quota, not merely funding delta; pre-consumption can make delta zero |
| `model.QueueTaskBudgetSettlement` / `queueTaskBudgetSettlement` | Tracked funding adjustment, task quota and desired budget revision; token handling differs by caller | Append the winning adjustment/refund occurrence in that same transaction. No post-commit log function owns creation of the canonical event |
| `model.CommitTaskBudgetTerminalTransition` | Winning terminal CAS, tracked funding/token change, task quota and desired ledger target | Commit fact only for the winning transition with nonzero adjustment (submission count remains separate). Replayed terminal polling must not increment again |
| `service/task_billing.go:recordTaskQuotaRefund` / `recordTaskQuotaAdjustment` | Currently post-commit counters and logs | Render/deliver the already-committed occurrence; do not create a second event on restart or during budget reconciliation |
| `service/task_polling.go:commitTaskTerminal` | Selects tracked versus legacy terminal handling | Ensure the receipt travels through the actual winning source path, not just a helper test |

`FinalizeTaskBudgetSubmission`, `ApplyQueuedChannelBudgetSettlement` and `service/budget_attempt.go:settleAttemptChannelBudget` reconcile local budgets after funding. They must not become the source of another consume fact or rerun funding on publication retry.

### 4.4 Exact no-budget task CAS and source-confirmation contract

Do **not** make default/no-budget paths use local-budget accounting, and do not move their wallet/subscription/token/cache operations into a new finance transaction just to publish statistics.

1. **Submission:** `controller/relay.go:insertSubmittedTask` allocates/fixes the submit occurrence and final numeric input from `result.Quota`, frozen names/group/model/node and current request identity. Extend `model.InsertTaskWithSubmissionCharge`'s primary insert callback to prepare this obligation in the same task-insert transaction for both tracked and plain branches; the existing body-content callback remains optional. `TaskBudgetInsertResult` or a separate task-insert result carries the numeric receipt without changing which branch reports committed funding. Never use a non-nil numeric receipt to masquerade as a non-nil committed financial charge.
2. Tracked submission confirms ready with the existing committed charge. Untracked submission stays source-pending until the existing `SettleBilling` success branch in `controller/relay.go`; confirm ready there, before `LogTaskConsumption`. A settlement-error return is source-unknown unless the same operation's known no-report outcome is proven; it is not an automatic terminal not-reported signal. A crash/uncertain acknowledgement leaves source-pending/unknown and blocks completeness; no worker calls `SettleBilling` again. For streamed accepted output with failed insert, prepare a standalone source-pending occurrence **using the key already allocated before the insert attempt**, before that branch's settlement, and reuse its receipt on the existing report-on-success path. The absent task row must not cause a new event identity.
3. **Terminal transition:** add `TaskUsageTransition` containing the preallocated operation key, expected primary task ID/status/quota, finalized target quota, and optional immutable consume/refund candidate. Extend/introduce `CommitTaskTerminalWithUsage(ctx, task, previous, transition)` for the no-budget branch. Inside one primary transaction lock the task row, verify the expected status/quota and winning CAS, allocate/persist its durable per-task statistics sequence, update the terminal task fields, and call `PrepareUsageEventTx(..., source_pending)` only for the reportable nonzero delta candidate. A losing CAS inserts no event; a zero delta inserts no consume/refund event. Do not use `Save`, which can insert on a failed CAS. An uncertain commit recovers by operation key plus expected owner/task/transition and payload, not by rerunning a terminal money callback.
4. **Plan once before CAS:** `service/task_polling.go:commitTaskTerminal` computes the completion target/refund decision once through its existing `resolveTaskCompletionBilling` rules before selecting tracked/no-budget commit. Preserve the existing failure-refund eligibility and tiered/adaptor precedence. Hand the frozen transition/receipt to `finalizeTerminalTask`; do not recalculate the target or normalize a second usage from pricing during retry. Only the CAS winner reaches existing monetary finalization. Existing `perfmetrics.RecordTaskResult` / reporting hooks remain their own domain.
5. **After that CAS, preserve existing money order:** the no-budget winner still uses `settleTaskFunding` / `taskAdjustFunding`, token adjustment and current counters under their existing policy. Once funding adjustment succeeds and the current path is about to call `recordTaskQuotaAdjustment` or `recordTaskQuotaRefund`, confirm the matching receipt ready and apply statistics before optional log rendering. A funding error means not-reported for that attempted occurrence when proven; partial/ambiguous finance state means source-unknown. Do not declare all terminal CAS candidates ready merely because the task became terminal. Token adjustment errors retain their current reporting behavior (these helpers log and continue), unlike fee submission's full-success predicate.
6. **Direct late/manual adjustments/refunds:** `RecalculateTaskQuota` / `RefundTaskQuota` can run outside terminal polling. Before their non-idempotent source call, lock/verify the task's expected quota/statistics sequence, allocate a new durable operation identity and prepare the numeric candidate. Retry persistence with that same receipt; a later legitimate delta uses a later sequence. A pending/unknown previous operation is a conflict requiring source resolution, not authorization to allocate another identity and repeat its debit. Known no-report/zero-delta is not a consume event. This is a statistics source guard; it does not turn old non-idempotent funding into a replayable finance ledger.
7. `recordTaskQuotaAdjustment` / `recordTaskQuotaRefund` take the receipt (and frozen reporting values) produced by the actual winner. They publish optional logs/counters according to existing behavior, not re-enqueue canonical facts. Budget reconciliation likewise never creates another occurrence.

**Mandatory source-state rule:** a no-budget or batch-backed settlement error is not enough evidence for a durable `not_reported` closure. It may follow a split/queued monetary mutation or an uncertain acknowledgement. Retain `source_unknown`/blocked coverage unless the caller proves the same operation never became reportable; no retry worker may re-debit to find out. `not_reported` is reserved for a known no-report outcome, not a generic error return. If primary storage also prevents that state update, the existing pending candidate/writer epoch remains unresolved. This tightens, rather than changes, the existing money policy.

This is the precise **small** no-budget change: CAS + immutable numeric candidate + operation identity commit together, and the existing conditional report confirms eligibility separately. It guarantees durable numeric payload and at-most-once statistical projection for every prepared/confirmed occurrence. It does **not** claim wallet/token atomicity or exact recovery of a crash between a legacy monetary call and source confirmation. Those cases stay source-unknown unless trustworthy keyed source evidence proves the same occurrence; never promote from current `task.Quota`/status alone. Batch-enabled finance retains its current queued behavior; a ready fact means gross reporting success, not a durable funding commit. No blanket batching prohibition is required merely to collect gross reports.

Legacy Midjourney uses the same source receipt/linkage principle with its existing `billingApplied` and effective token-ID result (section 4.2). If root later requires all conditional money outcomes to be automatically recoverable, that needs a separately reviewed narrow transaction outcome receipt at those monetary mutations. It is not a prerequisite to correctly labeling/retaining the explicit unknown state, and is not silently included in R24.

### 4.5 Proposed implementation file slices (ownership to be assigned by root)

| Slice | Files and exact source seams | Necessary change / explicit non-change |
| --- | --- | --- |
| Numeric source API / apply | New root `model/usage_event.go` and the schema/claim-owner's focused projection/coverage files; `model/main.go` | `UsageEventInput`/receipt/source-state/prepare/confirm/apply contracts and additive migrations. No root JSON bypass or independent-module dependency |
| Normal sync reports | `service/text_quota.go:PostTextConsumeQuota`; `service/quota.go:PostAudioConsumeQuota`, `PostWssConsumeQuota` | Finalized numeric prepare ahead of existing money/reporting side effects; same receipt/time passed onward. No pricing, counter-count or settlement outcome changes |
| Optional log adapters / old export | `model/log.go:RecordConsumeLog`, `RecordTaskBillingLog`, admin metadata sanitization; `model/usedata.go` active-epoch writer fence | Receipt/time accepted and mandatory keyed admin-only linkage; optional log/export flags never gate prepare. Stop new legacy cache collection after fenced cutover. Retained-log list/auth/export unchanged |
| Async submission | `controller/relay.go:insertSubmittedTask` and its actual success/error/streamed branches; `model/log_request_detail.go:InsertTaskWithSubmissionCharge`; `model/channel_budget_adjust.go:InsertTaskWithBudgetSubmit`; `service/task_billing.go:LogTaskConsumption` | Task insert + numeric obligation atomic for tracked and plain branches; pass separate numeric receipt versus committed charge. Do not rename a plain stats receipt into proof of finance |
| Async terminal/direct adjustment | `service/task_polling.go:commitTaskTerminal`, `finalizeTerminalTask`; `model/task.go` or one focused task-usage transaction file; `model/channel_budget_adjust.go:queueTaskBudgetSettlement`; `service/task_billing.go:settleTaskFunding`, `RecalculateTaskQuota`, `RefundTaskQuota`, both record helpers | Winning CAS/operation guard + candidate; tracked ready in its existing tx, no-budget conditional confirmation. Existing monetary order and fee/refund math stay intact |
| Conditional reports | `service/violation_fee.go:ChargeViolationFeeIfNeeded`; `service/midjourney.go` billing/refund helpers; `relay/mjproxy_handler.go` two consume-report branches; `controller/channel-test.go` consume-report branch only | Explicit per-occurrence receipt/predicate/time. No diagnostics selection/layout changes; those are already accepted/frozen. No new debit/refund policy |
| Import/read/cleanup | Bounded model backfill module; `model/log.go` business summary methods only; `model/channel_usage.go`; `model/usedata*.go`; `service/system_task.go`, `model.DeleteOldLogBatch` | Remaining sections define backfill/reader-lineage/cleanup contracts. Actual log pagination/storage/content association stays log-backed |

Do not scatter one feature's new regression fixtures across each file/layer. Root should assign a focused existing fixture plus at most one new consolidated test file under the backend test-quality rule. Actual test names/hosted selectors are assigned after source owners are set; none are invented as passing here.


## 5. Historical backfill: finite, idempotent, resumable

### 5.1 Establish a real collection fence

A timestamp alone is not sufficient: old writers can finalize late; relational IDs can be allocated before an uncommitted transaction; ClickHouse has no reliable per-row ID here.

Recommended rollout barrier:

1. Install compatible primary schemas and dormant source capture on **all** gateway/task/diagnostic/fee writers. Do not backfill or activate deletion while old nodes can still produce unidentified rows.
2. With operator approval, inhibit application usage-log deletion and suspend/neutralize **existing native ClickHouse TTL** for the source window, or take an immutable numeric snapshot before it can expire. This research changes no setting. An application flag is not protection from engine TTL.
3. Pause/drain legacy in-flight finalizers and task polls, flush/freeze existing `quota_data` buffers from all old nodes, and drain log delivery/transactions. The inability to prove this drain is a blocker, not a reason to use wall-clock time as proof.
4. Record the fence/collection epoch, immutable legacy source manifest and frozen legacy `quota_data` baseline. Resume only upgraded writers with event keys and source receipts. Late task adjustments now create new live occurrences even for pre-fence tasks.
5. Backfill legacy source rows while live identified writes continue. Imported legacy facts and live facts have disjoint identities; any source row already carrying a fact key is linked/verified, never counted again.

No stop-the-world scan of the entire history is required. The short barrier establishes the immutable historical set; bounded background import may continue afterward. If operators cannot provide a drain/snapshot/TTL fence, an online migration requires a stronger CDC/snapshot protocol and is not this smallest version.

### 5.2 Relational logs

After the drain establishes that no old transaction can still commit, record the source's real row-ID high-watermark H. Scan `id > checkpoint AND id <= H`, ordered by ID, in bounded batches from the **log** connection. On primary DB, prepare/verify each source-keyed fact and advance the import checkpoint in the same transaction. Projection may apply in that transaction or separately; import-complete and applied/verified-complete are distinct durable states, and cutover/cleanup requires both.

- A crash before the primary batch commit retries it; a crash after an ambiguous acknowledgement rereads its durable checkpoint/keys. Never add totals again blindly.
- No transaction spans two databases. The source snapshot/fence prevents deletion or mutation while the target commits idempotently.
- Sparse/deleted IDs are normal. Verify actual source row counts and numeric sums, not `H - start`.
- Include retained numeric metadata needed for cache semantics, historical filters and node provenance, not bodies/content. Parse with the existing JSON wrappers in any later implementation.
- Missing/invalid old metadata is recorded as unknown coverage. Do not reinterpret it from current token names, pricing or provider assumptions.

### 5.3 ClickHouse `id=0` and multiplicity

`model/main.go:clickHouseLogCreateTableSQL` declares `id Int64 DEFAULT 0`; lists assign display IDs. Neither is usable for identity or keyset progress. The MergeTree order `(created_at, request_id)` is not unique and multiple legitimate consume rows can share both.

For the **frozen legacy set only**, construct a canonical tuple of every retained numeric/filter/provenance field used by the fact (exact seconds, kind, IDs and historical strings, request IDs, prompt/completion/quota, normalized cache ingredients with known/unknown flags, node values). Group exact tuples and obtain multiplicity N. Preserve byte-distinct strings; do not let a case-insensitive collation merge groups in target staging. Use a versioned length-framed tuple/digest in the importer.

Import N occurrences with deterministic keys `legacy-ch:<source namespace>:<tuple digest>:1..N`. Retain the tuple and multiplicity/digest manifest for collision/replay validation. Bounded chunks within a large group can commit the last ordinal with the target transaction; restart continues exactly there. Partition by fixed event-time ranges and a stable tuple keyset/group manifest, not `OFFSET` over mutable physical CH rows or unstable part/row positions.

- Two indistinguishable rows still contribute count=2 and twice their quota/tokens, matching current physical-row aggregate semantics. A hash-only DISTINCT would incorrectly erase multiplicity.
- Whether identical historical rows were legitimate reporting events or duplicated transport delivery cannot be inferred after the fact. Preserve retained-row semantics and label this limitation; do not claim exactly-once historical *logical* charging.
- A resumed run uses the same source namespace, tuple normalization and occurrence ordinals. A new random migration ID must not make the same rows count again. Source replacement/restore needs an explicit lineage decision, not silently reusing or changing namespaces.
- Optional event-keyed **new** CH rows are not part of this multiplicity import; link them to primary facts even when CH delivered duplicates. This intentionally prevents future duplicate sink delivery from becoming a second business event.
- Source TTL/deletion/late legacy writes invalidate the manifest. Halt verification/cutover; do not shrink already-imported facts to fit a moving source or call a partial partition complete.

This grouping/manifest step is a correctness cost of the current CH schema. A future unique event column helps live delivery but cannot retrospectively identify old rows.

## 6. Legacy `quota_data` overlap: preserve two lineages, never guess a residual

Existing `quota_data` has no event coverage; surviving logs may overlap it fully, partially or not at all, and the two may differ by node/time/cache/flags. It can also contain history whose logs no longer exist.

**Recommended minimal compatibility policy:**

- Freeze/copy legacy `quota_data` into an immutable **dashboard baseline** with its existing dimension/hour sums and source provenance. Preserve all known numeric values, including older zero/empty dimensions; do not pretend they are event-level history.
- Newly captured post-fence live facts feed the new dashboard hourly projection. Its source is disjoint by collection epoch/receipt, not merely `created_at >= cutover`.
- Dashboard/flow/rankings read `legacy dashboard baseline + new live dashboard projection` with existing predicates. Historical log backfill is **not additionally added** to that baseline.
- Log-summary/channel surfaces read imported historical log facts + new live facts; they previously did not read `quota_data`. Keeping their historic coverage distinct preserves each surface's prior meaning without double counting.
- Keep imported legacy facts available for exact historical log filters even if their dashboard-eligibility is false. One canonical fact can have explicit projection-family eligibility; two independent gross total tables do not imply net/gross reconciliation.

This policy preserves existing known dashboard totals and makes future collection durable. It does **not** claim to reconstruct missing historical dashboard export while export/logging was disabled. If the product instead wants to replace a historical baseline interval with reconstructed log-derived values, do so only for an explicitly approved whole covered partition after reconciliation, retaining the old baseline and a reversible provenance choice. The new values can legitimately differ from old dashboard totals; do not call that transparent parity.

Never compute `max(old, backfilled)`, subtract totals to guess the missing overlap, add both, or delete all baseline rows to make a test agree. Without source identity none proves correct event membership. Account balances/counters cannot fill missing historical model/token/time/cache dimensions.

## 7. Reader cutover, cleanup gate and rollback

### Rollout order

1. Agree gross-event/failure/coverage contracts and reader lineage policy; finish child PRD/design/implementation plan before code.
2. Add schemas and dormant readers/capture; verify real primary DB migrations and released-version upgrades. Schema availability is not an activation marker.
3. Upgrade all writers, establish the operator-approved barrier and protect the source/TTL; activate live capture and freeze baseline/manifest.
4. Import in bounded batches; compare complete source manifests and surface-specific outputs. Incomplete/unknown history is returned/documented as such rather than numeric zero presented as complete.
5. Switch all business numeric readers to durable sources together with cache-version invalidation (including ranking cache). Leave actual log pagination/export/storage/authorization unchanged.
6. Prove numeric invariance after actual log deletion in hosted disposable stores. Only then allow application cleanup of verified source partitions and permit an operator-approved native TTL policy.

### Cleanup prerequisite

The central prerequisite must be used by `service/system_task.go:runLogCleanupTask` and the destructive `model.DeleteOldLogBatch` entry point, not merely the button/controller. For a requested deletion source/range, require:

- recognized schema/source lineage and all-writer activation;
- immutable legacy manifest imported and verified for every affected partition;
- the applicable business readers cut over; no dependency on deleted log bytes remains;
- no unresolved live receipts/coverage incidents whose only recovery source is inside the deletion range;
- successful target commit/projection verification. Storage error, unsupported source or missing checkpoint fails closed.

Do not make actual request-body retention depend on numeric totals. Its own protection/authorization rules remain separate; body expiry is not deletion of usage facts. Conversely a body-only cleanup need not wait for numeric backfill when it cannot erase required numeric source fields.

Native CH TTL does not call the application prerequisite. Deployment must either remove/suspend it with explicit operator approval until coverage is verified, or protect an immutable numeric snapshot before any expiry. Check `syncClickHouseLogTTL` migration/startup behavior so a restarted node cannot silently reinstall an unsafe TTL mid-backfill. No worker is authorized to alter production TTL by this document.

### Rollback

Before log deletion: return numeric readers to their old sources only with an explicit coverage warning for post-fence log-disabled facts; keep primary facts/baseline/progress. A feature flag must not stop required capture while upgraded sources continue serving.

After log deletion: rolling back to readers that aggregate retained logs is **not a valid numeric rollback**. Keep a decoder/reader-capable version and durable schemas, or restore a verified source snapshot. Do not drop facts or reset projection/checkpoints as an ordinary rollback. On projection defect rebuild a separate version from facts and the frozen baseline, verify, then swap; never re-charge finance.

## 8. Hosted acceptance and explicit unresolved decisions

Root owns all executable validation and exact-SHA evidence. Source review is not a pass.

Required hosted scenarios include:

- Real SQLite/MySQL/PostgreSQL, both fresh schemas and upgrades from the latest released version; startup/migration twice. Exercise separate relational LOG_DB and real ClickHouse source/TTL/multiplicity behavior. Record exact versions and commands, including supported-minimum coverage where version-specific behavior is introduced.
- Consume logging off, export off, process restart, two nodes, ambiguous commit acknowledgement, duplicate event-key retry and mismatched-payload rejection. No in-memory buffer may be the only accepted fact.
- Genuine source callers: sync text/audio/realtime reports, zero-usage rows, diagnostics, fee failures/partial outcomes, task submission/positive adjustment/refund, winning/losing terminal polls, no-budget tasks and late pre-fence task completion. Assert count/gross/token semantics rather than equalizing them with user counters.
- Prompt/completion/raw TPM versus Anthropic ranking tokens/cache denominator; explicit input known/unknown; exact-second edges and 60-second intersections; historical text filters, renamed/deleted metadata, server-scoped users and masked ranking viewers.
- Stop/resume each backfill transaction edge with concurrent live writes. Relational sparse IDs; CH all-zero IDs, same request ID across legitimate events, identical tuple multiplicity, new-event CH delivery duplicates and a huge group split across chunks. Rerun import after success without changing totals.
- Frozen legacy dashboard history that overlaps logs, partially overlaps, has no surviving logs, and has missing dimensions. Verify baseline+new remains stable and imported historical log facts are not added a second time.
- Compare each durable business surface before/after actual usage-log deletion; retained-row counts/storage may change. Reject cleanup on incomplete backfill, failed projection, unknown source, pending receipts, reader not cut over, and native TTL not protected.

**Convergence status / remaining acceptance gates:**

- Root accepted gross-event (not finance-ledger) semantics, mandatory per-occurrence/log linkage, separate channel and channel/model lifetime rollups, primary prepare/apply, and the disjoint legacy dashboard/log baseline policy. They are decisions, not another request for product approval.
- p5 owns exact schema/index/first-claim/checked-apply mechanics; root/p4 reconcile that document with this source API before production file assignment.
- Finalized-prepare durability is explicit; pre-prepare loss with billing proceeding is not recoverable by a projector. The recommended once-per-writer open epoch/clean-drain protocol detects possible loss conservatively, but lifecycle completeness/sealing remains an implementation/review gate, not a proven existing mechanism.
- No-budget task CAS + candidate + source-confirmation paths are specified above. Unknown split/batch-backed outcomes stay excluded and block coverage; budget activation or generic monetary replay is not introduced.
- The all-writer drain, source snapshot/manifest and independent native-TTL protection require deployment/operator action later; this research performs none. Capacity/row-index envelope, real-database migration, source failure tests and final exact-SHA hosted verification remain required. No cleanup-ready or exact whole-history claim is made.

The file is READY for bounded architecture critique and root planning integration. These remaining gates are not permission for broad billing refactoring. Numeric source completeness and deletion readiness stay false for any unresolved source/coverage range.
