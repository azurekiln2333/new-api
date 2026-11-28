# Implementation and hosted validation plan

## Constraints and ownership

- Existing Pi agents implement/review; root coordinates artifacts, final ownership, Git and hosted runs.
- All local tests, builds, formatters, lint/typecheck, database probes and executable validation are prohibited. Source reads/edits, task bookkeeping, Git and remote Actions inspection are allowed.
- Workers are sharing the workspace. Preserve peers' saved edits; no file ownership expansion without root coordination. Never stage/commit/push from a worker.
- Read AGENTS.md and `.agents/rules/billing.md` in full before accounting work; frontend work additionally reads web/AGENTS.md and the project UI/i18n skills when relevant.
- No production deletion/settings/compaction. Parent remains the root coordination task; this child is activated for the accepted foundation.

## Research and design gate

- [x] p2: `research/backend-statistics-inventory.md` — endpoint, filter, event, source and coverage inventory.
- [x] pJ: `research/write-backfill-architecture.md` — concrete source/API proposal saved; residual gates remain explicit.
- [x] p5: `research/schema-claim-contract.md` — schema/claim proposal saved; index/collation/capacity addenda remain pending.
- [x] p6: `research/frontend-statistics-inventory.md` — every dashboard/ranking/model/log-summary UI reader and cache/access boundary.
- [x] p4: `research/architecture-review.md` — independent failure/overlap/cross-store pressure test, frozen.
- [x] Root converges foundation PRD/design, records accepted cleanup/index/text/capacity clauses and scopes later integration gates separately.
- [x] Curated implement/check JSONLs include frozen research and final root overrides. Existing user authorization covers foundation activation.

## First implementation ownership — 2026-11-26

w5D:p4 owns only new `model/usage_event.go`, new `model/usage_event_apply.go`, the primary migration call in `model/main.go`, new consolidated `model/usage_event_test.go`, and existing `model/migration_dialector_test.go`. Read curated context, PRD, design and this plan before edits. Publish exported model signatures early for the next source owner. Freeze and read back before independent review. Do not edit source callers, log writers, readers, scheduler, backfill, cleanup, UI/locales or peer-owned files. Foundation source review will be assigned to a different existing Pi worker; no local checks or Git are authorized to workers.

Root owns exact-SHA hosted workflow coverage including fresh and latest-release migration twice. A passing foundation run is storage evidence only, not whole-task completion or permission to resume cleanup. Next source/read/backfill owners are assigned after the model API is frozen; deployment footprint proof is required before operational activation, not before writing software.

## Ordered delivery

1. Define durable numeric event/projection/coverage schema and APIs. Extend the suitable existing migration/database fixtures; test fresh and latest-release upgrade twice in Actions.
2. Integrate synchronous and asynchronous event writes at the approved durable business boundaries. Keep billing arithmetic and existing CAS/retry semantics intact; cover persistence failure and ambiguous acknowledgement.
3. Implement bounded resumable backfill and explicit legacy coverage transition. Prevent application cleanup from racing uncovered source rows. Exercise same/separate stores and ClickHouse.
4. Migrate all business readers identified in the inventory. Preserve DTOs/filter semantics/privacy where possible. Update frontend only for required coverage/error contracts, shared components and all seven locales.
5. Add end-to-end database assertions that record values, delete source logs, re-read every business surface and replay backfill/events without change or duplication.
6. Independent source review for transactions/identity, overlap/cutover, read parity and access. Resolve concrete findings and freeze owned files before root capture.
7. Root updates specs, commits a coherent snapshot, pushes and runs hosted checks. Fix observed exact-SHA failures through bounded edits; never substitute local checks.

## Hosted gates

Use the existing `.github/workflows/ci.yml` real-engine infrastructure. Final concrete selectors are recorded after test ownership is set; no invented passing test names.

- Root Go formatting/vet/build/test; independent relaykit/tokenkit checks if affected.
- Frontend formatting/typecheck/lints/tests/build if affected.
- Real SQLite, MySQL and PostgreSQL: fresh schema, latest released schema/data upgrade, startup twice, constraints/unique keys and representative data retained.
- Same/separate relational LOG_DB and real ClickHouse: source identity, duplicate/multiple legitimate events, late inserts, interrupted backfill, TTL prerequisites and cleanup readiness.
- Focused race/deterministic concurrent cases for event insert/apply, projections, checkpoints, task CAS and cleanup. No sleep-based synthetic stress tests.
- Required behavioral cases: logs/export off, crash/replay, write/apply failure, raw/Anthropic tokens, zero-token fees, diagnostics, repeated request ID with distinct adjustments/refunds, second-edge filters, historical names/deleted entities and viewer privacy.
- Exact API outcome before/after actual log deletion and second backfill must match for all covered statistics. Log rows/count/export correctly shrink independently.

Record exact final SHA, database versions, commands, result artifacts and any skip. A source review or three-engine test definition is not an executed pass.

## Rollout handoff

Separate software verification from deployment readiness. Provide the all-writer migration order, old-buffer treatment, backfill/readiness/coverage procedure, native TTL prerequisite, backup reference and rollback limits. Inform the user when the implementation and required hosted checks pass; do not perform their later 24-hour production cleanup from this session.

## Foundation source disposition — 2026-11-26

The exact five model paths are saved/frozen and independently SOURCE ACCEPTED by
native `r24_foundation_review`, with no concrete findings. Saved selectors are
`TestUsageEventFoundationDatabaseMatrix`, `TestUsageEventInputBoundsAndIdentity`
and `TestMigrationSchemaStability/usage_statistics_foundation`. No compiler,
database, race or released-upgrade validation ran locally. Root's latest-release
probe/workflow changes receive their separate source review before capture.

The exported API is Prepare/Confirm/Apply with context and Tx variants, plus
bounded `RecoverUsageEventProjections`. Prepare takes finalized facts and
pending/ready state; Confirm takes `UsageEventOutcome{State, EvidenceRef}`. This
does not authorize source promotion during recovery or claim source integration.
The original foundation writer now performs read-only source-integration planning;
all five saved paths stay frozen. Proposed caller/test paths require root handoff
before implementation, especially the R34-owned violation-fee normalization seam.

## Priority integration lanes — 2026-11-27

The user explicitly asked whether statistics independent of logs were complete.
Root confirmed they are not complete, merged or released, and raised this task's
priority. The foundation-only ownership above is historical; the following
explicit assignments now authorize the remaining implementation in the isolated
`D:/My2KProjects/new-api-statistics-20261127` worktree. No production cleanup or
local executable validation is authorized.

- w5D:p4 owns live source capture and optional-log binding, including the saved
  service/controller/relay seams, new `model/usage_source.go`, `model/log.go`,
  `controller/system_task_handlers.go`, `model/usage_event_test.go` and
  `service/text_quota_test.go`. It integrates final reader calls into the two
  log-summary helpers and preserves the independently reviewed R33 LogFilter
  scope semantics. Optional log publication is never the numeric authority.
- Native `hotfix_key_actions` owns new `model/usage_event_read.go`, existing
  `model/channel_usage.go`, `model/usedata.go`, `model/usedata_flow.go`,
  `model/usedata_rankings.go` and suitable existing reader suites. All business
  readers must migrate; retained log rows/count/export remain log-backed.
- w6E:p5 owns bounded historical backfill and coverage, starting with new
  `model/usage_event_backfill.go` and an announced existing source regression
  suite. It publishes coverage/lineage APIs for the reader lane. The frozen
  legacy dashboard baseline must be disjoint from live hours; historical log
  import cannot blindly add overlapping dashboard totals or drop multiplicity.
- Native `finish_r33` reviews only the held root CI/released-upgrade probe and
  foundation migration dependencies while source/read/backfill remain unfrozen.
  Root alone edits coordination records, integrates reviewed deltas, owns Git,
  and runs hosted validation.

Final acceptance still requires actual before/after log-deletion equality for
channel/model/dashboard/flow/ranking/log-summary results, live collection with
logs/export disabled, idempotent replay/backfill and exact-SHA real-engine
validation. Partial source delivery is not this product guarantee.

## Transition review and capture boundary — 2026-11-28

p2 independently conditionally accepted the retained-set extent proof, monotonic
reader retry, empty-model parity and proof-bearing public Install API. The
transition anchor still commits Apply, actual quota_data mirror, exact owned hour
and immutable acknowledgement together. Private synthetic marker seeding exists
only in model test fixtures; service fixtures use a real imported checkpoint.

This acceptance assumes the documented operator-held old-writer/buffer/source/TTL
fence through verification and marker commit. It is not runtime enforcement,
database compatibility, production activation or cleanup authorization. The
released-probe/workflow source review by p6 found no actionable issue in its scope.

Root added the public Install regression to the hosted required-pass verifier.
The transition real-engine matrix and its race run remain mandatory, alongside
source/read/backfill/retention and released-upgrade checks. No local executable
validation was run. The earlier integration-readback research is a historical
finding list; resolved gates are described by the current code and updated
usage-statistics-foundation spec, not by that earlier mutable-tree snapshot.

Remaining operational limits include missing pre-Prepare evidence, externally
managed source/TTL fencing and the bounded oldest-page cleanup scan, which can
retain later proven rows behind an unproved prefix. None permits dropping rows
or claiming complete history. Production cleanup remains paused.
