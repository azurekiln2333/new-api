# R24 minimal schema, unique claim and checked projection contract

## Status and boundary

- Owner: p5. Exclusive output: this existing-task research file; no production implementation or activation approval.
- Read `.agents/rules/billing.md` **in full**, child PRD/design/implementation plan, p2 inventory, pJ write/backfill proposal and p4 architecture review before this proposal. Inspected the actual audit outbox, budget insertion/locking, usage readers and installed GORM MySQL conflict builder.
- This closes the *proposed storage/claim shape*, not the still-open source-finalization, historical-fence, collation/query-parity or deployment gates. No local checks, database probes, formatting, production operations or Git/index writes. All engine claims below are designs requiring hosted verification.
- Primary `DB` only. Seven core additive tables plus one proposed writer-epoch control table: finalized events with optional receipt state, hourly projection, channel lifetime, channel/model lifetime, task sequence, source registry, partition manifest/checkpoint and `usage_writer_epochs`. Frozen existing `quota_data` remains the independent legacy dashboard baseline; it is not copied into channel lifetime totals.
- **No mandatory pre-dispatch receipt or per-request admission write is proposed.** The minimal source path prepares already-finalized numbers directly, in line with pJ section 4.2. Optional `open`/Begin support below is an orthogonal lifecycle capability only; adopting it needs root's explicit source decision and does not recover lost numeric output. The once-per-serving-writer epoch proposal is conservative loss detection, not an event-recovery claim or a settled pre-dispatch API.
- Gross reported consume occurrences remain distinct from refunds and from successful funding. Neither preparing nor replaying statistics may call settlement, funding, token, cache, subscription or budget charging again.

## 1. Portable types, hashes and bounds

### 1.1 DDL/GORM rules

Use explicit signed `BIGINT` / Go `int64` for IDs, sequences, seconds and counters, `SMALLINT` / `int16` for enums, and `CHAR(64)` for lowercase SHA-256 hex keys. Opaque source/epoch IDs are exactly 32 lowercase ASCII hex characters in `CHAR(32)`. All generated numeric primary IDs use `gorm:"primaryKey;autoIncrement"`; never handwrite SERIAL/AUTO_INCREMENT. Do not give indexed digest columns platform-dependent default lengths.

Use `gorm:"type:bigint;not null"`, `gorm:"type:smallint;not null"`, `gorm:"type:char(64);not null"` and `gorm:"type:text;not null"` as appropriate. Store every initial numeric value explicitly; no boolean defaults, JSON columns, generated columns, partial indexes, expression indexes, CHECK-constraint enforcement assumption, CTEs or RETURNING dependency. Do not add automatic update-time hooks to claim rows. In particular an ignored duplicate must not change its timestamps or immutable fields. All new models are primary-only; no FK to deletable users/tokens/channels/tasks or LOG_DB.

A representative key shape is:

```go
type UsageEvent struct {
    ID       int64  `gorm:"primaryKey;autoIncrement"`
    EventKey string `gorm:"type:char(64);not null;uniqueIndex:ux_usage_event_key"`
    // Remaining columns are specified below; input ID is always zero on INSERT.
}
```

At worst utf8mb4, a 64-character digest index occupies 256 character bytes; digest + two BIGINTs is 272, below even the older 767-byte InnoDB key limit. Do not add full historical strings to a composite index. This bound is not a migration pass. All hash inputs/outputs are validated in Go, including when a database collation is case-insensitive or CHAR pads trailing spaces.

### 1.2 Canonical encoding and collision verification

Use independent domains `usage-event-key/v1`, `usage-payload/v1`, `usage-hour-key/v1`, `usage-channel-model-key/v1`, `usage-task-key/v1` and `usage-partition-key/v1`. Encode fields in the specified order as: version/domain; field identifier; explicit known/unknown marker; type; unsigned 32-bit big-endian byte length; exact bytes. Integers use fixed-width signed two's-complement big-endian bytes; strings use their exact UTF-8 bytes. An unknown value is not an empty known string or a known zero. No separator concatenation, map iteration, JSON serialization, trimming, Unicode normalization, case folding, pricing lookup or model remapping determines identity.

- Event key: source namespace + source-kind-specific immutable occurrence identity. Live identity is an occurrence UID, scoped by task identity where applicable. Relational legacy identity is namespace + real row ID. Frozen CH legacy identity is namespace + canonical source tuple digest + occurrence ordinal. **Do not include a fresh import-run ID, checkpoint, arrival time or mutable current quota.** A restarted import cannot get new identities for the same rows.
- Collection epoch, live writer epoch/activation generation, exact seconds, kind, task sequence, all historical dimensions, numeric ingredients, known-mask and projection eligibility belong to the immutable final payload. A replay must retain its original epochs/generation, even after restart. Epoch is not a way to bypass a prior event-key conflict.
- Hour key: projection version + lineage/epoch + hour start + user ID + historical username + historical model + group + token ID + channel ID + export-node value/knownness. Token name, request IDs and writer node stay in facts, not this key.
- Channel/model key: projection version + channel ID + full model value/knownness. Channel lifetime uses its numeric channel ID directly.

For **every** digest conflict, compare the complete stored source identity and immutable typed payload or complete projection dimension tuple byte-for-byte in Go. Digest equality alone is not equality proof. Reconstruct the canonical bytes from stored fields; do not store a second serialized payload copy per event. Same event key/same payload is replay; same key/different source or payload is a visible conflict, never overwrite, merge or salt-and-retry. A CH group/checkpoint also retains its full current tuple for collision checking before assigning ordinals.

### 1.3 Explicit text-byte limits

All historical filter strings use TEXT with no full-string index. The proposed maximum UTF-8 **bytes**, not runes, are:

| Column | Maximum bytes |
| --- | ---: |
| `username`, `token_name`, `writer_node`, `export_node` | 1,024 each |
| `model_name` | 16,384 |
| `group_name` | 4,096 |
| `request_id` | 256, accommodating existing varchar(64) character values |
| `upstream_request_id` | 512, accommodating existing varchar(128) character values |
| `source_operation_ref` | 2,048 |
| Full canonical event/source tuple | 32,768 |
| Partition's one current cursor tuple, base64 canonical encoding | 45,056 |
| Operator-safe error category | varchar(48), closed enum values only |

These limits fit MySQL TEXT per value and prevent an unbounded metadata archive. They are **not** permission to truncate existing records or introduce new upstream request validation. Oversize, invalid UTF-8 or PostgreSQL-unrepresentable NUL text makes statistics preparation/import fail visibly and leaves the occurrence/source partition uncovered. Preserve the source; do not mark it imported, invent empty dimensions or claim global completion. Root must accept that compatibility policy before activation; if actual historical data exceeds it, an explicitly reviewed wider/dictionary representation is needed, not an automatic lossy conversion. Bodies, IPs, content, latency, credentials, arbitrary `other` and audit parameters are forbidden.

## 2. Exact minimal tables and indexes

All stated keys/indexes are required; do not let AutoMigrate infer additional string indexes. Names below are proposed public database names, not files to create during research.

### 2.1 `usage_events`: finalized numeric fact and optional occurrence receipt

| Columns | Types / contract |
| --- | --- |
| `id`, `event_key`, `key_version`, `payload_digest` | generated BIGINT PK; CHAR(64) unique key; SMALLINT=1; CHAR(64), empty only before a final payload exists |
| `source_namespace`, `collection_epoch`, `writer_epoch`, `activation_generation`, `source_kind`, `occurrence_uid`, `source_operation_ref` | CHAR(32), CHAR(32), CHAR(32) live writer epoch (empty only for imported history), BIGINT generation, SMALLINT, CHAR(32) or empty for deterministic legacy identities, bounded TEXT |
| `source_row_id`, `source_tuple_digest`, `source_ordinal` | BIGINT, CHAR(64) or empty, BIGINT; only the source-kind-appropriate fields are set; none are public log display IDs |
| `task_primary_id`, `task_operation_sequence` | BIGINT; zero for non-task occurrences; allocated sequence is immutable once bound |
| `source_state`, `kind`, `eligibility`, `known_mask` | SMALLINT state/kind; BIGINT bitsets; flags explicitly describe unknown values and allowed projection families |
| `occurred_at`, `prepared_at`, `opened_at`, `updated_at`, `applied_at` | BIGINT seconds; only prepared/finalized payload has authoritative occurred_at; retry timestamps do not change it |
| `user_id`, `token_id`, `channel_id` | BIGINT, historical numeric identity, no FK |
| `username`, `token_name`, `model_name`, `group_name`, `request_id`, `upstream_request_id`, `writer_node`, `export_node` | bounded TEXT, exact historical values |
| `request_digest`, `upstream_request_digest` | CHAR(64), canonical knownness/string digest; exact lookups additionally verify the full text predicate |
| `prompt_tokens`, `completion_tokens`, `cache_read_tokens`, `cache_write_tokens`, `reported_input_tokens`, `quota_magnitude` | BIGINT; finalized immutable ingredients; usage semantic is a separate SMALLINT (`unknown`, `openai`, `anthropic`, `other-explicit`) |
| `applied_version`, `next_attempt_at`, `attempts`, `error_code` | BIGINT version=0 until applied; BIGINT retry seconds/count; varchar(48) safe category; state does not become ready just because a retry time elapsed |

State enum: `open` (no final numbers), `source_pending` (frozen candidate, source outcome unconfirmed), `ready` (caller has confirmed a reportable occurrence), `not_reported` (known non-reportable outcome), `source_unknown` (unresolved outcome). Only ready rows contribute. Projection completion is `applied_version=1`, not a new source state. Open/unknown rows remain coverage incidents; timeout alone cannot cancel them. Same final payload may move pending/unknown to ready only through an explicit source-owner confirmation carrying durable evidence. A different payload requires conflict handling, not mutation.

Required indexes (column order is deliberate):

- `ux_usage_event_key(event_key)`.
- `ix_usage_event_drain(source_state, applied_version, next_attempt_at, id)`.
- `ix_usage_event_time(source_state, kind, occurred_at, id)`.
- `ix_usage_event_self(user_id, source_state, kind, occurred_at, id)`.
- `ix_usage_event_channel(channel_id, source_state, kind, occurred_at, id)`.
- `ix_usage_event_tail(channel_id, source_state, applied_version, kind, id)`.
- `ix_usage_event_request(request_digest, source_state, kind, occurred_at)` and corresponding upstream digest index.
- `ix_usage_event_task(task_primary_id, task_operation_sequence)` for operation recovery; uniqueness is enforced by the locked sequence protocol, not by inserting another conflict-swallowing constraint.

Time/self/channel indexes serve exact-second filters; the drain/tail indexes avoid searching applied history. Literal substring matching of model/name/group/request IDs still scans matching time/user candidates. Normal B-trees do not solve substring searches. An administrator's unbounded historical query remains potentially large; do not silently impose a new time limit or promise constant cost. Port existing LIKE/wildcard escaping with **primary**, not LOG_DB, dialect rules. Cross-store collation differences must pass explicit case/Unicode/trailing-space parity fixtures before reader cutover.

### 2.2 `usage_hours`

- Generated BIGINT `id`; SMALLINT/BIGINT `projection_version=1`; CHAR(64) `dimension_key`.
- Retain the complete hour-key tuple from 1.2 as typed columns, using the same string bounds. `hour_start` is the existing hour-floor semantics, not an exact-event timestamp range approximation.
- BIGINT counters: `consume_count`, `gross_quota`, `prompt_tokens`, `completion_tokens`, `ranking_tokens`, `cache_read_tokens`, `cache_write_tokens`, `input_denominator`, plus `cache_unknown_count` and `semantic_unknown_count` for honest availability.
- Required indexes: unique `ux_usage_hour_key(projection_version, dimension_key)`; `ix_usage_hour_time(projection_version, hour_start, id)`; `ix_usage_hour_self(user_id, projection_version, hour_start, id)`.
- Refunds do not create consume-hour contributions. Legacy-log imports do **not** feed the dashboard family automatically. Frozen legacy quota_data is a disjoint lineage merged only by dashboard readers. No inferred residual or blind addition of imported history.

### 2.3 `usage_channels` and `usage_channel_models`

- `usage_channels`: composite PK `(projection_version BIGINT, channel_id BIGINT)`; BIGINT `consume_count`, `gross_quota`, `prompt_tokens`, `completion_tokens`, `last_used_at`.
- `usage_channel_models`: generated BIGINT `id`; BIGINT version/channel; CHAR(64) model key; full model TEXT plus knownness; the same five counters/timestamp. Unique `ux_usage_channel_model(projection_version, channel_id, model_key)`; nonunique `ix_usage_channel_models(projection_version, channel_id, id)`.
- Every eligible consume occurrence updates **both** lifetime tables, even with empty model; refunds update neither. `last_used_at` is `max(stored, occurred_at)` while locked, never last-writer-wins.
- Batch channel cards fetch at most one lifetime row per selected channel (existing controller cap 200). Detail fetches only that channel's model rows, unions current configured/ability data and deliberately excludes empty-model rows from detail totals. Standalone channel total retains empty-model contributions. Detail folds its model rows with checked arithmetic; it cannot substitute the channel total without preserving that distinction.
- Do not implement all-time channel readers as `GROUP BY usage_events` or sum every historical hourly bucket on each request.

### 2.4 `usage_task_sequences`

Composite task identity is represented by CHAR(64) `task_key` PK, hashing source namespace + actual primary task ID. Retain CHAR(32) namespace, BIGINT task ID, BIGINT `last_sequence`, CHAR(64) `active_event_key` (empty when none), BIGINT update seconds. Full identity is verified on conflict. No FK or private task JSON field.

The source task lock/CAS remains authoritative. Under that lock, ensure/lock this row; a *new winning reporting operation* receives checked `last_sequence+1`, binds a stable occurrence UID/event row, and commits sequence + candidate + source CAS together. A retry resumes the saved active event or already-winning task transition; it must not increment the sequence merely because a callback ran. Same task/quota does not identify an operation. If the source seam cannot durably identify a winning transition, this table does not magically fix it: its coverage stays disabled. Keep an unresolved active operation fenced rather than re-executing non-idempotent finance.

### 2.5 `usage_sources` and `usage_partitions`

- `usage_sources`: CHAR(32) namespace PK; SMALLINT store kind; CHAR(32) lineage/collection epoch; BIGINT normalization/projection versions and monotonically checked `activation_generation`; BIGINT allowed-source-role mask; CHAR(64) expected writer-roster/fence and frozen-dashboard manifest digests; SMALLINT activation phase; BIGINT fence/verification seconds; varchar(48) safe failure category. No DSN, credentials or hostname-derived identity. Source restore/clone must explicitly retain or fork lineage; it cannot silently reimport under a random namespace.
- `usage_partitions`: CHAR(64) partition-key PK; retained namespace, immutable CHAR(64) manifest digest, SMALLINT coverage family (`historical-log`, `legacy-dashboard`, `live-source`), BIGINT inclusive/exclusive time/ID bounds and normalization version; SMALLINT phase; BIGINT checkpoint revision, last relational ID, current CH occurrence ordinal and multiplicity; CHAR(64) current tuple digest plus bounded base64 full cursor tuple; BIGINT expected/imported counts and separately checked prompt/completion/quota/cache totals; CHAR(64) rolling verification digest; BIGINT updated/verified seconds; varchar(48) safe failure category.
- Required partition index `(source_namespace, coverage_family, phase, range_start)`; no wide string unique key. Partition-key collision checks compare all retained immutable manifest coordinates. Primary reads/writes verify the expected manifest and checkpoint revision under lock; they never reset progress to accept a changed source.
- Keep source manifest/source count provenance distinct from primary applied readiness. A committed import checkpoint proves the numeric obligation was prepared, not necessarily projected. Cleanup requires both verified source import and no unresolved applicable events, ready-pending projections or source receipts, plus reader/writer/TTL gates from the architecture owner.

### 2.6 `usage_writer_epochs`: bounded coarse loss-detection record (proposal)

Add one control row per serving writer/process generation, **not per request**:

- `writer_epoch` CHAR(32) PK, opaque immutable server-generated UID; `source_namespace` / `collection_epoch` / `writer_instance` CHAR(32); BIGINT `activation_generation` / `source_role_mask`; SMALLINT `phase` (`open`, `clean_sealed`, `unknown`); BIGINT `opened_at`, `sealed_at`; CHAR(32) `seal_uid` (empty before a seal); BIGINT `seal_revision`; varchar(48) safe `error_code`.
- Required index `ix_usage_writer_generation(source_namespace, activation_generation, phase)`; optional opened-time paging uses `(phase, opened_at, writer_epoch)` only if needed. Full registered identity/role/generation is verified after insert-first conflict/locked read; no DSN, hostname, payload or per-request list in this row.
- Source activation generation is locked/read from `usage_sources`; epoch registration atomically checks that exact generation/role assignment and commits `open` before *any* corresponding relay/task/diagnostic/fee source work starts. All live prepared inputs retain this writer_epoch/generation. Stats-only apply/replay of an already-prepared old fact retains its old binding; it cannot manufacture new source work or move an unknown prior epoch into the current generation.
- Clean seal locks/rechecks the source generation plus the exact epoch, compares a stable seal_uid/revision on acknowledgement retry, and requires pJ's **admissions-fenced, closed-world in-process drain** of every detached callback/finalizer and sticky unresolved item. The same transaction changes only that epoch to clean_sealed and records the accepted seal attestation. A clean seal is not inferred from SQL row counts, empty ready queues, heartbeat/lease expiry or another process's recovery. Every new serving generation needs its own committed open row. Unknown is irreversible by ordinary timeout/restart; trustworthy same-occurrence evidence plus explicit coverage review is required to resolve a gap.
- The row has bounded metadata but retained epochs grow with deployments. Do not prune unverified epochs or use this table as a per-event archive. The operator-visible state distinguishes active/open from clean_sealed and crashed/unknown; loss of the process before durable seal conservatively leaves open/unknown.
- **Lifecycle gate remains unverified:** the table alone cannot prove detached work was tracked, recover numbers lost before Prepare, certify a time prefix of an active epoch, or authorize unattended rolling 24-hour usage-log deletion. Root must accept/validate the actual comprehensive registration/fence/drain implementation. If primary is unavailable when final numbers are lost, this row supplies only possible-loss evidence, never those quantities. No mandatory per-dispatch Begin/SQL call is introduced by this proposal.

## 3. Claim first, then prepare/apply: concrete cross-dialect protocol

### 3.1 Supported operations

Proposed conceptual APIs, not new Go files:

- `BeginUsageOccurrence[Tx](sourceIdentity) -> receipt`: **optional, not invoked by the minimal source path**. If separately selected by root, it commits an `open` lifecycle row without final usage. It is neither a mandatory upstream-admission hook nor loss recovery. Finalized Prepare works without it and creates a fully populated finalized row directly.
- `PrepareUsageEvent[Tx](identity, immutableFinalPayload, sourceDisposition) -> receipt`: fills final payload **once** or verifies exact replay. Ready is allowed only for an already-confirmed reportable occurrence; conditional task/fee/MJ candidates stay source_pending.
- `ConfirmUsageEventReadyTx(receipt, sourceEvidence)` / `FinishUsageOccurrenceNotReportedTx(...)`: source-owner-only transitions under the row lock. Projector has no authority to make these decisions. A finance error is not automatically not_reported.
- `ApplyUsageEvent[Tx](receipt, version=1)`: statistics-only projection; no source or finance replay.

Receipt contains event key, bound source identity and final payload digest when available. It is not proof that projections or finance succeeded. Exported Tx variants use only the caller's tx and never query global DB/cache or open an independent transaction.

### 3.2 Insert-then-authoritative-lock, no affected-row ownership test

For an event key, zero-value generated ID input:

1. Fully validate bounded typed identity/payload before insert. The minimal call starts with finalized numbers; it does not require an earlier Begin/open row. Begin a primary transaction. On SQLite obtain its writer reservation **before any read**, using the project's no-row update pattern on a stats table (`UPDATE ... SET id=id WHERE 1=0` through GORM); inherited source transactions must already have done this before reading. Busy/snapshot failure rolls back/retries the whole stats transaction.
2. Execute GORM `Create` with `clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}`. It proposes the immutable identity/open receipt or finalized candidate. Do **not** preselect absence. Discard driver RowsAffected, inserted ID, LastInsertID and any “created” boolean.
3. Select the row by event_key using `lockForUpdate(tx)`. MySQL/PostgreSQL take a real row lock; SQLite retains its single-writer reservation. This locked persisted row, not the attempted insert object, is authoritative.
4. Compare full source binding. If payload already exists, compare every final field and digest; same payload is replay, different payload returns a fixed conflict error. If open, attach final numbers exactly once under the lock. An existing source_pending/unknown candidate is not promoted by a generic repeated Prepare.
5. Decide only from the persisted locked source state + applied version whether to prepare, confirm, apply or no-op. Concurrent first insertions serialize at the unique key; the loser cannot increment a projection just because its INSERT returned nil.

Dialect shape:

- PostgreSQL >=9.6: INSERT ON CONFLICT(event_key) DO NOTHING, then SELECT FOR UPDATE. A concurrent uncommitted conflicting key is waited out; the next locked statement sees committed state at READ COMMITTED. Do not catch a plain duplicate violation inside an already-aborted transaction and continue. Higher isolation serialization failures retry the entire transaction.
- MySQL >=5.7.8/InnoDB: installed gorm mysql v1.5.7 maps DoNothing to `ON DUPLICATE KEY UPDATE id=id`. There is no INSERT IGNORE. The subsequent locking current read sees the committed row even at default REPEATABLE READ. Generated ID input is zero and event_key is the only non-PK unique constraint, so no unrelated unique conflict can be mistaken for this event. Verify the exact full binding regardless. Matched-row vs changed-row connection modes do not affect correctness.
- SQLite: OnConflict with uniqueness plus the writer reservation; no FOR UPDATE syntax. A loser may receive SQLITE_BUSY and retry after rollback; it cannot commit independent increments from an old read snapshot. Never copy the legacy `gorm:query_option` locking pattern.

Apply the same insert-then-locked-read pattern to projection/sequence/partition row creation. Each has only its declared natural unique key plus an auto-generated PK where applicable; always compare the full tuple on hash conflict. No bucket counters change in an upsert expression.

### 3.3 Atomic apply and lock order

Prepare-final-numbers is independently durable when the source needs projection failure to leave recoverable numbers. After that commit, a separate Apply transaction:

1. Lock the existing event as above; verify full receipt/payload and source ready. Applied version 1 => no-op. Open/pending/unknown/not_reported => no contributions. Invalid state/version => safe conflict/coverage failure.
2. Determine eligible targets from immutable payload. Canonical per-event order is: `usage_channels` numeric key, `usage_channel_models` key, `usage_hours` key. Insert-if-absent and lock in that order, compare all dimensions. Do not acquire task/source finance locks from the projector. Source tx variants acquire existing source/task/account locks first, then stats receipt, then the same bucket order; no reverse callback is allowed.
3. Check *all* arithmetic in Go against locked counters before mutating. Write explicit resulting values with GORM map Updates; do not use struct Updates that drops zero. All three eligible projections and event.applied_version/applied_at commit in **one primary transaction**. A failure rolls back all increments/markers; the previously prepared event remains ready/unapplied.
4. Bounded worker candidates use the drain index. After an apply error, a separate small transaction may store safe retry/error metadata, rechecking that the event is still unapplied; failure to store that metadata does not erase the prepared event.

A source transaction may call Prepare+Apply together only where failure rolls back its source transition/obligation too, or with an approved savepoint arrangement that retains the prepared fact after projection rollback. Do not claim a rolled-back in-tx prepare remains durable. Minimal path uses one event per Apply transaction. A future multi-event batch must globally sort *all* event/bucket locks or retry deadlocks as one transaction; holding arbitrary per-event bucket orders across a batch is not sufficient.

On ambiguous prepare/apply COMMIT acknowledgement, query/retry the **same** immutable identity and payload on a new bounded stats transaction. It either finds applied, finds ready/unapplied, or reinserts after verified rollback. Never generate a new UID or replay finance. If primary state cannot be read, leave outcome unknown/coverage closed, not successful. Optional consume/refund log delivery must carry the canonical fact key in admin-only metadata; sink duplicates are links to this fact, not new facts.

## 4. Checked int64 arithmetic and knownness

New canonical finalized magnitudes are nonnegative, with consume count exactly one per consume occurrence; refunds store their positive magnitude separately by kind and add zero to existing gross consume projections. Keep single-request pricing/quota caps/conversions unchanged. Legacy negative or out-of-range anomalies are a partition failure, not a silent clamp, reinterpretation or accepted negative charge.

For every nonnegative counter addition, before computing a+b:

```text
if a < 0 or b < 0 or a > MaxInt64 - b: return statistics_overflow
result = a + b
```

Check sequence/revision/attempt increments the same way. For any future intentionally signed delta, first branch on sign and compare against MaxInt64-b / MinInt64-b without negating MinInt64. That is not permission to subtract refunds from current gross metrics.

Derive per-event raw tokens by checked prompt+completion. Derive ranking tokens/input denominator using the existing **explicit** Anthropic semantic and separately checked cache components; never add the cache-write sub-breakdowns twice. Unknown cache/semantic/input flags remain unknown even where legacy compatibility numerics use zero; rollups count unknown contributions and cannot advertise an exact cache-hit rate from them. reported_input_tokens is retained separately, not substituted for the current cache-hit denominator.

No SQL `counter + delta`, GREATEST/scalar-MAX portability trick or SQLite overflowing numeric expression performs accumulation. `last_used_at=max(...)` is selected in Go under the same lock. Overflow rolls back every target and marker; leave the prepared fact unresolved and block completeness/cleanup. Do not clamp aggregate history, rewrite the billed quota or drop a huge event to make publication succeed.

Read-side reductions also require checked addition. Channel detail folds only its model projection rows; hourly/ranking reducers can fold selected projection rows; exact-filter summaries fold matching facts. Do not assume SQL SUM/decoder casts or prompt+completion SQL expressions are signed-int64-safe across engines. A wider SQL SUM result or a range too large for an API integer must fail explicitly, never wrap or become a float and claim parity.

## 5. Prepared-but-unapplied visibility without lifetime event scans

A durable ready event may wait for projection; it must not disappear. Suggested initial active-version readers use a single SQL statement/snapshot yielding `projection rows UNION ALL ready/unapplied contribution rows`, followed by checked Go reduction. The two branches share the same query snapshot. An apply commit is observed either as the event tail or as its applied bucket, not both/neither. Two unrelated READ COMMITTED queries are not equivalent.

- For channel readers, use the channel-tail index with exact `source_state=ready AND applied_version=0 AND kind=consume` and requested channel ID(s), not `applied_version != current` over all event history.
- Bound the per-response ready tail to **1,001 rows** (limit 1,000 + sentinel) inside a portable derived subquery. Inspect the entire statement result before returning totals. More than 1,000 matching pending events returns explicit `statistics_projection_pending`/not-ready, never a partial numeric total or fallback to logs after cutover. A global dashboard tail uses the drain/state index and requested hour/time coordinates with the same sentinel rule.
- Each tail row includes the immutable ingredients and scope tags necessary to contribute to channel + model buckets; compute/validate deltas with the same checked reducer as apply. Refund/not-reported/source-pending rows are not silently counted. Coverage metadata distinguishes unknown source receipts from a verified zero.
- Applied projection rows are not limited by the tail sentinel. Channel cards read selected one-row lifetime totals; detail reads only that channel's model rows. The tail scans only indexed unapplied work, not all events or historical hours. A large model catalog remains real model-row work, not an event-history scan.
- Required event index `ix_usage_event_writer(writer_epoch, source_state, applied_version, id)` additionally bounds pending/source-state inspection for an epoch. This is not a proof that no unprepared occurrence was lost; the writer lifecycle attestation remains necessary.
- For a multi-response request, return neither branch's partial result on overflow/tail-limit/query error. Complete historical coverage/lineage and the applicable writer-epoch state must also be satisfied; a fast tail is not evidence that import or lifecycle tracking is complete.

Before first cutover, keeping old readers with an explicit migration-not-ready status is acceptable until projections and manifests catch up. After source logs can be deleted or log-disabled traffic is accepted, logs-only fallback is not a valid completeness strategy. Root chooses response/error/coverage shape before activation; this research does not silently change API behavior.

## 6. Checkpoint and collision/concurrency acceptance

Read source batches outside the primary transaction only under pJ's immutable namespace/manifest fence. On primary, ensure/lock the partition by digest, verify its complete immutable coordinates and expected checkpoint revision, prepare deterministic events, then advance prepared-count/checkpoint/digest in the **same transaction**. Candidate reads can race; the locked recheck rejects stale batches. No affected-row CAS result or MAX(event_id) proves completion.

If import applies within that transaction, its bucket/marker changes commit with the checkpoint and all arithmetic is checked. If it commits prepare only, projector state remains pending and the partition cannot become cleanup-ready until every applicable prepared row is applied/verified. Projector never acquires partition/source locks after bucket locks. Cross-store source reads/primary commit are not a distributed transaction.

Required hosted deterministic cases for this contract, inside suitable existing fixtures where practical:

1. Two connections first-insert the same key; same payload increments each eligible projection once; conflicting payload/dimension refuses without any partial counter/marker. Repeat with MySQL changed/matched row modes if fixture supports them.
2. Lost acknowledgement after an actual prepare/apply commit; replay same identity returns original state, no new occurrence or finance side effects.
3. Fail after one bucket update, before marker, and at COMMIT; compare all three lifetime/hourly projections and durable pending row, then retry.
4. Force a digest collision through the codec boundary in a test; different full source/dimension bytes must conflict, including equal-collation but byte-distinct strings.
5. MaxInt64 boundary, one-above overflow, derived-token overflow, task sequence overflow and out-of-order last-used events; no wrap, float, partial marker or incorrect max.
6. Reader interleaved with projector commit sees one contribution using the actual single-statement shape; 1,001 pending sentinel returns not-ready, never a plausible truncated total. Channel/model lifetime query is verified behaviorally without an all-event scan assumption.
7. Relational and CH cursor/checkpoint interruption, changed manifest/namespace, same CH tuple multiple ordinals, source strings beyond bounds and unknown semantic flags all retain honest coverage.
8. Real SQLite/MySQL/PostgreSQL fresh + actual latest-release upgrade, startup/migrate twice; exact indexes/types/uniqueness preserved, separate source relational/ClickHouse cases. Record exact supported engine versions/commands/final SHA. No source argument or skipped DSN substitutes for this matrix.

## 7. Decisions left with root/pJ/p4

This proposal deliberately does not approve a post-settlement logging hook as durable collection or mandate Begin before dispatch. Before activation, root still must settle finalized-prepare failure ownership, no-budget/batch-funded/fee/MJ source seams, winning task-operation evidence, the once-per-writer open/clean-sealed epoch lifecycle (if adopted) and all-writer drain, historical dashboard lineage, exact filter/collation compatibility, text-bound policy, capacity measurements and native TTL protections. An unclean writer epoch or optional open receipt without final numeric evidence identifies a possible **unrecoverable coverage incident**, not a replayable numeric fact. Keep cleanup paused and do not claim funding-atomic statistics or database compatibility from this draft.

Schema-derived storage is not free: worst-bound text alone is roughly 27 KiB/event before row/index overhead, and ordinary facts may be much smaller. The declared secondary event indexes plus two lifetime projections and hourly dimension cardinality require hosted representative non-production size/plan measurements before any disk-saving claim. No estimate from the supplied production log size proves a saving.
