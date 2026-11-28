# Durable usage statistics foundation

## 1. Scope / Trigger

Use this contract when recording finalized usage facts or projecting them in the
primary database. `usage_events`, `usage_hours`, `usage_channels` and
`usage_channel_models` are additive primary-only tables. The original foundation
contract below also applies to the source, reader and transition integration
described in section 8. Migration creates schema; it does not activate readers.

## 2. Signatures

```go
PrepareUsageEvent(ctx context.Context, input UsageEventInput, initialState UsageEventSourceState) (UsageEventReceipt, error)
PrepareUsageEventTx(tx *gorm.DB, input UsageEventInput, initialState UsageEventSourceState) (UsageEventReceipt, error)
ConfirmUsageEvent(ctx context.Context, receipt UsageEventReceipt, outcome UsageEventOutcome) error
ConfirmUsageEventTx(tx *gorm.DB, receipt UsageEventReceipt, outcome UsageEventOutcome) error
ApplyUsageEvent(ctx context.Context, receipt UsageEventReceipt) error
ApplyUsageEventTx(tx *gorm.DB, receipt UsageEventReceipt) error
RecoverUsageEventProjections(ctx context.Context, limit int) (int, error)
```

`UsageEventOutcome` contains `State` and `EvidenceRef`. The receipt binds canonical
event identity and immutable payload digest. Tx variants use the supplied
transaction throughout; callers own its commit/rollback.

## 3. Contracts

The input is already finalized: stable source identity, event second, historical
filter dimensions, numeric facts, semantic/knownness bits and projection
eligibility. Request ID alone is not an event key. Unknown values differ from
known zero/empty values. Bodies, prompts, credentials and arbitrary metadata do
not belong here. Stored epoch/generation fields are immutable dimensions, not an
implemented lifecycle or a completeness proof.

Prepare accepts pending or ready source disposition and compares the complete
stored identity/payload after insert-or-observe. Replaying Prepare cannot promote
an existing source state. Confirm records only explicit source-owner outcomes;
projection retry cannot infer that billing or a source operation committed.

Apply locks event, channel, channel/model and hourly projections in that order.
Eligible projection increments and the applied-version marker commit atomically;
all additions are checked. Gross consume counts/quota do not decrease for refund
events. Preserve raw prompt/completion and separately derived ranking/cache/input
semantics. Discard attempted-insert IDs before authoritative projection reads.

Recovery selects at most 100 due ready/unapplied rows, invokes Apply and records
bounded retry delay with fixed failure categories. Its returned count means
successful Apply calls, not global coverage. It never resolves pending/unknown
source outcomes or replays wallet/token effects.

## 4. Validation & Error Matrix

Any error returned by a Tx variant requires the caller to roll back the entire
supplied transaction; the caller must never commit a partial attempt. Non-Tx
wrappers own their transaction and roll it back on error. The rollback guarantees
below depend on this caller contract.

| Condition | Result |
| --- | --- |
| Same canonical identity and finalized payload | Idempotent receipt/replay |
| Supplied event key disagrees with identity | Identity conflict |
| Same event identity with changed payload | Payload conflict |
| Apply before source ready | Not-ready error; no projection writes |
| Confirm conflicts with recorded outcome | State conflict |
| Counter overflow or dimension mismatch | Whole transaction rolls back |
| Failure before final projection or marker | No partial committed totals |
| Projection retry after successful apply | No double increment |

## 5. Good / Base / Bad Cases

Good: retain an occurrence ID across retries and confirm from authoritative
source evidence. Base: Prepare a finalized ready fact and Apply it once or many
times with identical totals. Bad: reconstruct event identity from RequestID,
promote pending facts during recovery, or treat missing history as verified zero.

## 6. Tests Required

`TestUsageEventFoundationDatabaseMatrix` covers immutable replay/conflict,
source outcomes, projection failure rollback, overflow, token semantics,
concurrency and caller transaction rollback on real SQLite/MySQL/PostgreSQL.
`TestUsageEventInputBoundsAndIdentity` protects canonical identity/bounds.
The schema-stability test protects fresh/repeated migration, preserved legacy
rows and index order. Select it through the dialect subtests:

```sh
go test -p 1 -count=1 -v ./model -run '^TestMigrationSchemaStability$/^(sqlite|mysql|postgres)$/^usage_statistics_foundation$'
```

The hosted released probe must also migrate the actual released schema twice.
Record exact SHA/engine evidence; source acceptance is not database compatibility
or retention readiness.

## 7. Wrong vs Correct

Wrong: interpret a successful AutoMigrate or an empty retry page as complete
statistics, then delete logs. Correct: keep source integration, legacy overlap,
reader parity, verified row coverage and cleanup gates separate. Durability starts
after finalized Prepare commits; missing pre-Prepare evidence remains unknown.

## 8. Legacy transition and reader ownership

### Scope / Trigger

Use this contract when exporting live reports into legacy `quota_data`, switching
business readers, importing retained logs or deleting covered numeric logs.
Existing log and dashboard readers remain active until verified activation.

### Signatures and schema

```go
ActivateUsageStatisticsLineage(ctx context.Context, lineage UsageReadLineage, expected UsageLegacyQuotaDataBaseline) error
InstallUsageReadLineage(ctx context.Context, lineage UsageReadLineage, proof UsageStatisticsLineageProof) error
```

`UsageStatisticsLineageProof.ExpectedBaseline` is the exact inspected baseline.
Public Install delegates to the production activation proof and rejects replay.
Activate permits an identical already-installed lineage after cleanup, comparing
the complete immutable lineage without requiring deleted source rows to reappear.
Marker-only setup is private to model `_test.go` fixtures and cannot activate a
production installation.

Three additional primary tables record ownership: permanent row 1 in
`usage_statistics_transitions`, immutable event-key acknowledgements in
`usage_legacy_acknowledgements`, and exact hourly baseline ownership in
`usage_legacy_hours`. Migrations preserve existing logs and quota rows, create no
foreign keys and perform no startup backfill.

### Contracts

Tracked legacy export serializes on the transition anchor. Applying the ready
event, incrementing the actual legacy quota row, adding its exact ownership hour
and recording its acknowledgement commit in one primary transaction. Retry keeps
the same occurrence and full payload; anonymous pending buffers block activation.
New source reports use generation 1 for this ownership protocol. Canonical
generation 0 history is ambiguous and prevents activation rather than being
silently dropped or guessed.

Before installing the global reader marker, activation reinspects the baseline
and verifies an imported checkpoint matching namespace, epoch and fence. It
checks every retained consume/refund row is within the checkpoint time/ID extent,
then replays its exact row count, digest and linked ready/applied payloads. An
empty bounded checkpoint cannot hide retained history elsewhere.

The caller must hold an external old-writer, old-buffer, source-mutation and TTL
fence throughout source verification and marker commit. The digest records this
operator prerequisite; the API does not acquire or enforce a distributed fence.
Separate LOG_DB reads are not a cross-database snapshot. No startup or automatic
operation is authorized to manufacture this prerequisite.

Postcutover dashboards combine frozen legacy baseline, live applied hours minus
the exact acknowledgement-owned hours, and the snapshot-consistent ready tail.
Historical log imports do not double-add overlapping dashboard totals. Coverage
remains partial/unknown where historical evidence is unavailable.

A reader choosing the legacy path materializes every component, rereads the
monotonic lineage marker and retries through the durable reader if activation
occurred. This protects in-flight legacy reads against subsequent covered-row
cleanup. Channel detail excludes empty-model entries on both paths.

`cmd/usage-statistics` exposes bounded inspect/import/resume and explicit activate
operations. The operator supplies stable source identity, manifest and baseline
proof. Invoking the command may initialize/migrate databases; it is not a dry run
or permission to perform production cutover. Native TTL and external SQL deletion
remain separate operational controls.

### Validation and error matrix

| Condition | Result |
| --- | --- |
| Missing/mismatched checkpoint or changed baseline | Not ready; no marker |
| Retained numeric row outside imported extent or changed payload | Not ready; no marker |
| Anonymous pending buffer or ambiguous canonical generation 0 | Retain history; no activation |
| Failure during mirror/ack/hour transaction | All related primary writes roll back |
| Public Install called after installation | Reject public replay |
| Same complete lineage replayed through Activate | Idempotent after source cleanup |
| Marker changes while legacy result is materialized | Discard legacy result and retry durable |
| Linked log has conflicting present display metadata | Retain row |
| Optional display metadata absent but typed fact exactly matches | Absence alone does not block proof |

### Good / Base / Bad cases

Good: freeze the sources operationally, inspect and import the complete retained
set, verify the baseline and activate under the same fence. Base: before activation
continue legacy reads and retain unproved numeric rows. Bad: use a bounded empty
checkpoint as whole-store proof, install a marker directly, or infer exact history
from aggregate subtraction without event ownership.

### Required tests

`TestUsageStatisticsTransitionDatabaseMatrix` exercises the actual emitter,
mirror ownership, same-hour/off-switch/tail behavior and before/after deletion
equality across SQLite/MySQL/PostgreSQL. Its retained-extent and reader-barrier
subtests cover time bounds, ID watermark, payload mutation, all legacy read
wrappers and empty-model parity. Require the same matrix under the race detector.
`TestUsageStatisticsTransitionRejectsAmbiguousHistoryAndBuffers` protects unsafe
activation states. `TestInstallUsageReadLineageRequiresRetainedSetProof` protects
the exported entry point against missing and incomplete retained-set evidence.
Hosted verification must reject missing, skipped or repeated required passes.
Fresh and actual released-schema migration twice must preserve the three new
tables' rows, columns, indexes and primary keys alongside existing history.

### Wrong vs correct

Wrong: treat an installed marker, successful migration or source-only review as
cleanup readiness. Correct: independently verify the exact integrated SHA on real
engines, retain unresolved rows, and require the operator-held source/TTL fence
and operational rollout prerequisites before production activation or cleanup.
