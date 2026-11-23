# Integration and release evidence

Status: commit `dc9778764419ba8f16f6b034aadd4f52b4d0d7a8` was pushed to fork
`merge/upstream`. Exact-SHA CI run `45181916416` failed in backend fixtures and
ClickHouse detail storage; frontend checks passed. Follow-up repairs have passed
the affected local suites, race checks and root vet/build. The repaired commit
still requires a fresh hosted run. No tag or release is claimed yet.

## Delivered behavior

| Requirement | Implementation and retained verification |
| --- | --- |
| R1 / R5 | Stable allocation IDs preserve duplicate credentials, separate user bindings and model scopes. Bound access precedes public grouping; both model lists retain token restrictions. Identity, alias routing and three-row scope edits passed SQLite, MySQL and PostgreSQL. |
| R2 / R4 | Channel and allocation budgets reserve together and record actual usage independently of upstream balance queries. Limits and resets preserve holds. Wallet/subscription and token charges still apply. Real three-engine accounting, task and migration checks passed. |
| R3 | Preview/apply dedup compares effective restrictions and budget configuration, conserves usage, checks revisions, and skips active reservations/tasks. Identical-key legacy task protection passed all three engines. |
| R6 / R7 | Original prompt/messages and supported text fields are captured before rewriting; payloads are separately stored and fetched through authorized administrator details. Lists/details carry processing node and asynchronous submit-node provenance. Shared/independent storage, authorization, failure, cleanup and race tests passed locally; ClickHouse remains a hosted gate. |
| R8 | Registration, refresh, logout and panel mutations use separate static IP scopes; all login alternatives retain the existing login bucket. Aggregate API protection remains. Structured 429 and the shared frontend mapper show translated errors. Full middleware/router and focused race checks passed. |

## Final integration repairs

- Accepted task insertion, funding/token adjustment, content association and
  desired ledger settlement commit in one primary transaction. Ledger replay
  never repeats funding. Actual cost above a subscription estimate is recorded.
- Tests inject a real successful SQL commit followed by a lost acknowledgement,
  an unreadable outcome, and an actual rollback. Confirmed success resumes once;
  unknown outcome preserves charges and holds. Recovery is bounded and scoped
  by user/channel plus stable allocation/reservation identity.
- Token-cache publication uses the credential actually charged. Deleted or
  rotated credentials cannot receive a stale-key cache delta.
- Image preparation sends keepalive before token counting can block on media.
  Budget candidate fallback does not spend an upstream retry or cross a pin or
  strict affinity. Responses socket reuse retains its original allocation.
- Startup checks the batch environment before activation because the runtime
  batch flag is assigned after database initialization.
- Channel deletion remains idempotent, retires allocations, removes abilities,
  and supports late settlement through retained accounting rows. The public
  delete regression passed; the updated real three-engine budget matrix passed
  in 7.889 s (`root-delete-matrix.log`).
- Key enable/disable operations now update the channel exhaustion reason and
  abilities in the same locked mutation. Only channels disabled for exhausted
  keys are restored; unrelated manual/automatic disable reasons are preserved.
- Copying a plugin channel remaps explicit key scope modes with the retained
  credentials. An empty allowlist remains deny-all, and removed credentials
  cannot leave a stale scope on a surviving public key.
- Legacy fixtures now initialize actual inactive accounting schema and mutate
  authoritative allocation state through public locked APIs. Diagnostic source
  replacement still verifies rejection of the old snapshot. Production storage
  errors continue to fail closed.

## Executed validation

Toolchain: Go 1.26.10 (`GOWORK=off`), Bun 1.4.4. Real engines: SQLite 3.50.4,
MySQL 8.4.13, PostgreSQL 16.16. Each worker uses a separate disposable database;
root uses `newapi_matrix`. Logs live outside the repository in the task runtime
directory. No production database was used.

| Check | Result |
| --- | --- |
| Full model / service with external database DSNs | PASS, 36.945 s / 15.658 s |
| Task ledger/legacy/atomic and immediate-settlement selector | PASS, SQLite subset 0.308 s; full MySQL 16.440 s, PostgreSQL 9.097 s |
| Task ledger/legacy/atomic plus image preparation race | PASS, SQLite 6.868 s |
| Startup prerequisite matrix / race | PASS, three engines 8.216 s / 9.494 s |
| Six key management/selection/identity/liveness/alias regressions | PASS, SQLite 11.537 s, MySQL 8.950 s, PostgreSQL 6.972 s |
| Plugin copy scope persistence and provider matrix | PASS, actual three-engine matrix 3.970 s; full granted controller selector 8.826 s; service selector 0.082 s |
| Diagnostic readmission/fingerprint/model-discovery fixtures | PASS, SQLite 3.776 s, MySQL 7.200 s, PostgreSQL 2.872 s |
| Provider cancellation and streaming keepalive fixtures | PASS, 5.788 s; race 7.280 s |
| Balance refresh source/replacement matrix | PASS, real SQLite/MySQL/PostgreSQL under race, 10.647 s; original assertions retained |
| Complete controller package after final repairs | PASS, 369.654 s, real external DSNs configured; `root-controller-final.log` |
| HTTP budget admission / Realtime guard / Responses reuse | PASS, SQLite 5.417 s, MySQL 1.329 s, PostgreSQL 5.060 s; race 8.828 s |
| R8 full middleware / router | PASS, 6.471 s / 9.251 s; race 4.497 s / 4.846 s |
| Released `de5c967` database upgrades | PASS, actual initialization twice on all three engines; preserved data, identities and indexes |
| Frontend affected suites / typecheck / scoped lint / production build | PASS, 298 tests in 13 suites; additional 429 mapper/notification coverage 52 tests |
| Complete frontend suite | PASS, 281 files / 3407 tests, 1459.83 s, `bun run test --maxWorkers=1` |
| Earlier remaining-81-package check | 44 PASS, 36 without tests; pre-existing Windows HTTP/2 GOAWAY failure. Superseded for relay/e2e by the final hosted admission-fixture failures below |
| Independent relaykit / tokenkit | Tests and builds PASS with `GOWORK=off` |
| Root vet / build on the integrated tree | PASS, `go vet ./...` and `go build ./...`; latest fixture gofmt/diff checks clean |
| Hosted-failure repair: complete root relay package | PASS, normal 0.479 s / race 7.908 s; individual conversion selectors and vet passed; `relay-full.log`, `relay-race.log`, `relay-anchored.log` |
| Hosted-failure repair: complete e2e package | PASS, normal 5.757 s / race 7.039 s; document-plugin selector and vet passed; `e2e-full.log`, `e2e-race.log` |
| Hosted-failure repair: AWS / task-jsplugin packages | PASS, normal 5.746 s / 12.638 s, race 6.882 s / 9.567 s; individual original-failure selectors and vet passed; `subpkg-after.log`, `subpkg-race.log`, `subpkg-anchored.log` |
| Hosted-failure repair: channel / advancedcustom packages | PASS, full normal 5.742 s / 0.174 s, affected channel race 6.885 s / full advancedcustom race 12.413 s; original-failure selectors preserved |
| ClickHouse payload conversion repair, local verification | Installed driver reproduction and regression PASS; real relational storage matrix PASS 16.680 s, full model PASS 32.472 s, focused race PASS 12.833 s; actual ClickHouse remains the hosted gate |
| Follow-up integrated root vet / build | PASS, `ci-followup-root-vet.log`, `ci-followup-root-build.log` |

The earlier full controller failures (`root-final-main-packages.log`) are
superseded by the focused repairs above. The subsequent full run finished in
394.779 s (`root-controller-integrated.log`) with only the balance-refresh matrix
failing. Its test fixture wrote obsolete key projections after credential refresh
created authoritative allocation rows. Public locked mutations now simulate the
replacement, scope change, reorder and disable operations; all original balance,
source identity and status assertions pass on three engines under race
(`balance-matrix-3db-race.log`). The fresh complete controller rerun passed in
369.654 s (`root-controller-final.log`).
The complete frontend suite passed serially in
`root-web-full-serial.log` after the initial parallel attempt exhausted local
Windows memory.

First hosted run `45181916416` exercised the committed final admission guards:
the e2e/relay/adaptor fixtures lacking an initialized inactive ledger now fail
closed before upstream calls. These earlier package results are stale for that
final runtime change. Fixture repairs must preserve admission behavior and all
original assertions. The real ClickHouse server is 25.8.33.6; both independent
fresh and released-upgrade detail-storage cases failed with storage errors,
while SQLite/MySQL/PostgreSQL and full controller passed in that job. This is a
release blocker under investigation, not a skipped or accepted storage gate.

The follow-up repairs give database-free relay/adaptor tests a real temporary
SQLite `Option` table with no activation marker through
`internal/testutil.UseInactiveChannelBudget(t)`. Existing database fixtures add
`Option` to their own migration list. They explicitly disable the process flag;
production admission checks and all original assertions remain unchanged.
Full affected packages and focused race checks now pass as recorded above.

The ClickHouse failure was reproduced against the installed
`clickhouse-go/v2@v2.46.0` String converter: a named string without `driver.Valuer`
is rejected, while native strings and a value implementing that interface pass.
`LogRequestPayload` now implements `Value` and `Scan`, preserving JSON bytes and
existing dialect-specific column types. The regression exercises the real
converter with empty, ASCII and non-ASCII payloads; the relational matrix still
covers fresh and released-upgrade stores with migration twice. Commands:
`GOWORK=off go test -p 1 -count=1 ./model` with both real relational DSNs, and
`go test -race -p 1 -count=1 ./model -run
'TestLogRequestPayloadIsAStringDriverValue|TestLogRequestDetail|TestLogRequestContent|TestCleanupLogRequestDetails|TestRecoverLogRequestContent'`.
`TEST_CLICKHOUSE_DSN` was unset locally; neither the conversion regression nor
the relational passes substitute for the pending hosted ClickHouse run.

The console owner's final source review disposed of the three key-review
questions: RB1 cited `UpdateChannelOtherInfoLocked`, which the append/stable-ID
binding path does not use; RB2's shared 409 intentionally requires reloading;
RB3 is handled by displaying skipped rows and enabling apply only for groups
with at least two eligible entries. These do not require additional product
changes. See the console child report's final integration review.

Exact commands and detailed assertions are retained in the four child reports,
`research/rate-limit-429.md`, `research/runtime-review.md`, and the diagnostics
child's `research/integration-quality.md`.

## Rollout limits and pending release gates

- `CHANNEL_BUDGET_ENABLED` defaults to false. Upgrade every node and drain old
  requests/batches before activating; all serving nodes must have
  `BATCH_UPDATE_ENABLED=false`. Durable activation cannot be bypassed by a false
  process flag.
- Active accounting rejects continuous `/v1/realtime` locally before 101 or an
  upstream dial. HTTP and Responses WebSocket are supported. This does not fix
  legacy Realtime cumulative billing or claim full continuous-work accounting.
- SQL/Redis cache delivery is not atomic. Cache publication occurs once in the
  normal controller success path, but process failure can leave a stale cache.
  Ledger reconciliation does not replay funding or cache deltas.
- The 429 fix remains IP-based, with separate business scopes and an aggregate
  ceiling. The production proxy/ClientIP configuration is unverified; the lack
  of a primary-password per-account guessing counter predates this change.
- Full frontend lint has existing errors outside task paths (135 errors,
  60 warnings at the retained check). Scoped task lint passes. No unrelated
  formatting sweep was performed.
- Local ClickHouse is unavailable. The existing hosted integration job now
  supplies ClickHouse 25.8 and selects actual log storage/read/cleanup and race
  tests. Its executed result on the exact committed SHA is required; configured
  YAML is not test evidence. Native TTL/fault-injection coverage limitations
  remain documented by the diagnostics child.
- Cross-region latency and a five-node load simulation were not performed.
  Active accounting adds primary-database transactions; this work does not
  promise reduced HK-to-US database latency.

After local integration acceptance, commit only task-owned paths, run the fork
CI on that exact SHA, and use the existing main release workflow to build
AMD64/ARM64 images, binaries, checksums and the custom tag. No direct production
deployment is authorized or performed.
