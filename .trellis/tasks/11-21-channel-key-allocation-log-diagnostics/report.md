# Integration and release evidence

Status (2026-11-24): local integration checks pass for R1–R9 and role-scoped
model selection. Cache publication is repaired in `c31cbc692`; Messages routing,
permission/dropdown regressions and all seven translations are frozen and ready
for the feature commit. Root vet/build, affected three-engine and race checks,
227 frontend regressions, whole-web typecheck, scoped lint/format, i18n validation
and production build pass. Exact-SHA fork CI and the release workflow remain
required; no new tag or release is claimed yet.

Historical hosted evidence: `2e1c7e161a7ae1daa45782ab9745b3b03389b2f3` run
`45189185651` passed backend checks and actual ClickHouse storage, then failed
the MySQL cache-routing selector now repaired. Initial `dc9778764419ba8f16f6b034aadd4f52b4d0d7a8`
run `45181916416` failed backend fixtures and ClickHouse storage. Those failures
and their repairs are retained below; earlier hosted passes do not replace CI
on the new exact commit.

### Cache-status blocker repaired, 2026-11-23

The earlier worker explanation that an inactive `InitChannelCache` call clears
the id-keyed cache is withdrawn: that branch leaves those maps untouched. The
fixture now explicitly reloads with caching enabled and initializes the fixed
`channel_key_entries` table, which does not honor its table prefix. Root seeded
an unrelated allocation row before the first MySQL run to verify isolation.

Before the production repair, exercising the authoritative allocation path caused
both SQLite and MySQL to fail 15 replacement/cap cases in
`TestUpdateChannelStatusCheckedCacheRouting`. The allocation status path calls
`MutateChannelKeyEntries`, whose unconditional post-commit cache reload replaces
newer cached configuration. The legacy guarded status-only publication is not
used. The failure established a production regression and the allocation-table
fixture and original assertions were retained. Original failure evidence:
`root-cache-fixture-sqlite.log`, `root-cache-seed-mysql.log`,
`root-cache-first-mysql.log`.

The repair separates the locked allocation transaction from cache publication.
Delayed health results compare source configuration and allocation identity/revision
before publishing status, while administrator writes retain full refresh. Newer
cached configuration and polling cursor survive; valid re-enables restore ordered
public routing immediately. Pricing invalidation and task-alias refresh remain.
Already-migrated no-op callbacks do not publish a cache snapshot. The remote-writer
lock fixture now changes authoritative allocation rows rather than their obsolete
channel projection.

All original 17 cache cases were retained and expanded with allocation identity,
scope/revision, no-op, re-enable and alias cases. Final command:
`GOWORK=off go test -race -p 1 -count=1 ./model -run
'TestScheduledChannel|TestUpdateChannelStatus|TestUpdateChannelAllocationStatus|TestBatchSetChannelTag'`.
With `TEST_TASK_DB_DIALECT` and isolated `newapi_billing` external DSNs, results:
SQLite 3.50.4 PASS 9.411 s; MySQL 8.4.13 PASS 21.479 s; PostgreSQL 16.16 PASS
15.412 s, no DATA RACE. Full SQLite model PASS 16.564 s; model vet/build and format
checks pass. Logs: `check-status-race-{sqlite,mysql,postgres}.log` and
`check-status-model-full.log`. Source review found no further regression in this
repair. New exact-SHA hosted CI is still required for release.

Root also ran the existing Playground channel authorization/routing and
fingerprint job ownership selectors on SQLite: PASS, controller 1.184 s,
`root-channel-permission-check.log`. Real session/PAT/model-token router coverage
now supplements that handler evidence: the three `TestAdminChannel` tests pass,
full router PASS 20.289 s and focused race PASS 13.720 s. Ordinary users can read
eligible groups/models but cannot list channels, run channel tests/fingerprints,
or forge a Playground pin. PAT scope cannot exceed the account's authority;
read-only administrator PATs cannot operate fingerprint jobs, while an operate
PAT reaches the handler. Model tokens cannot authenticate panel routes. Logs:
`router-authz-focused.log`, `router-full-final.log`, `router-race-final.log`.

## Delivered behavior

| Requirement | Implementation and retained verification |
| --- | --- |
| R1 / R5 | Stable allocation IDs preserve duplicate credentials, separate user bindings and model scopes. Bound access precedes public grouping; both model lists retain token restrictions. Identity, alias routing and three-row scope edits passed SQLite, MySQL and PostgreSQL. |
| R2 / R4 | Channel and allocation budgets reserve together and record actual usage independently of upstream balance queries. Limits and resets preserve holds. Wallet/subscription and token charges still apply. Real three-engine accounting, task and migration checks passed. |
| R3 | Preview/apply dedup compares effective restrictions and budget configuration, conserves usage, checks revisions, and skips active reservations/tasks. Identical-key legacy task protection passed all three engines. |
| R6 / R7 | Original prompt/messages and supported text fields are captured before rewriting; payloads are separately stored and fetched through authorized administrator details. Lists/details carry processing node and asynchronous submit-node provenance. Shared/independent storage, authorization, failure, cleanup and race tests passed locally; actual ClickHouse fresh/released-upgrade storage passed in run 45189185651. |
| R8 | Registration, refresh, logout and panel mutations use separate static IP scopes; all login alternatives retain the existing login bucket. Aggregate API protection remains. Structured 429 and the shared frontend mapper show translated errors. Full middleware/router and focused race checks passed. |
| R9 | Explicit Messages declaration, existing Claude conversion and preserved OpenAI credentials are implemented. Save/diagnostic regressions pass three engines and SQLite race. Successful Chat/Claude/HTTP Responses JSON/SSE, tools, cache usage, exact charges and WebSocket support boundaries pass normal/race tests. |
| Model selection and permissions | Administrator fingerprint models come from the selected channel; ordinary-user models come from the eligible selected group. Real router permissions and frontend account/privilege transitions pass; stale channel/job results cannot reappear after identity changes. |

R9 save/diagnostic command:
`GOWORK=off go test -p 1 -count=1 ./controller -run
'^(TestChannelSupportedEndpointsPersistThroughCreateAndUpdate|TestFingerprintResponsesOnlyChannelProbe)$'`.
Messages cases were added to the existing suites, retaining the CI selectors.
SQLite 3.50.4 PASS 7.861 s; MySQL 8.4.13 PASS 2.009 s; PostgreSQL 16.16 PASS
2.143 s (`TEST_CHANNEL_SQL_DSN`, isolated `newapi_matrix`). SQLite `-race` PASS
5.395 s. Actual Chat, native Claude and HTTP Responses fingerprint probes reach
`/v1/messages` with channel Bearer/version, a Claude body, one system prompt and
max_tokens bounded to 300 despite a 9000 override; response text and one usage log
are asserted. DTO endpoint tests PASS 1.342 s and independent relaykit build PASS
with `GOWORK=off`. Controller vet and owned formatting/diff checks pass. Logs:
`messages-contract-{sqlite,mysql,postgres,dto,vet,race}.log` and
`messages-relaykit-build.log`.

R9 successful relay verification:
`GOWORK=off go test -p 1 -count=1 ./relay -run
'TestChannelSupportedEndpoints|TestChannelEndpoint|TestMessagesEndpoint'`
PASS 3.112 s; focused race PASS 6.884 s. Full relay/OpenAI/Claude packages PASS
3.676 s / 6.174 s / 5.727 s. Each of six Chat/Claude/HTTP Responses JSON/SSE
paths asserts tools and terminal output, cache-read 5 and cache-write 3, real
preconsume 100 followed by final wallet/token charge 78, user/channel usage 78,
and exactly one consume log. Configuration cases preserve/prepend the prompt
exactly once, beta headers/query, version and channel authentication overrides;
retry clears the prior Messages target. The native Claude error path now retains
upstream Retry-After consistently with Chat and Responses. Messages-only
Responses WebSocket returns a local 400 with zero upstream dials, restored
balances and no consume log; adding Responses permits one dial and one charge.
Independent relaykit build and DTO/converter tests pass; vet/format/diff checks
pass. Logs: `messages-focused.log`, `messages-packages.log`, `messages-race.log`,
`messages-relaykit.log`. The success/accounting fixtures use SQLite; the separate
save/diagnostic matrix above provides the real three-engine persistence evidence.

Frontend final regression command runs the four changed fingerprint/channel
configuration/endpoint/key-selection suites, three shared model-selector suites,
and the two Playground options/state suites with `bun run test --maxWorkers=1`.
PASS 9 files / 227 tests, 177.62 s, `ui-contract-tests-final.log`. Assertions cover
channel aliases/deduplication/source changes/empty choices, all eight capability
sets, Messages HTTP Responses JSON and SSE, explicit empty key allowlist fallback,
and unrestricted-key pinning. React committed-render checks reject old administrator
metadata on identity or privilege changes; pending searches, polls and mutation
results cannot restore the old job. Shared selector disabled propagation is fixed
for desktop/mobile composition. These component tests supplement the independent
server router tests and do not replace them. Final whole-web typecheck passes
(1,600 files / two projects); scoped lint passes for 13 modified TS/TSX files,
format checks pass for 20 source/locale files, and production build passes
(6.70 s build, 10.13 s wall). Seven locales each add the same ten final keys;
dynamic capability/validation keys are registered. i18n sync/validation and
web diff checks pass; the temporary translation script is removed. Logs:
`ui-{i18n-sync,i18n-validation,typecheck,lint,format,build}-final.log`.

The final reviewer ran `GOWORK=off go vet ./...` and `go build ./...` after the
runtime owners froze: both exit 0, logs `check-integration-root-vet.log` and
`check-integration-root-build.log`. The bounded UI authorization checks reference
OWASP ASVS 5.0.0 V8.2.1 and V8.3.2; real server tests provide separate evidence
for V8.3.1. No whole-ASVS compliance claim is made.

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
while SQLite/MySQL/PostgreSQL and full controller passed in that job. This was a
release blocker; the follow-up repair and hosted validation below supersede that
failure without skipping the storage gate.

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
the relational passes substitute for a hosted ClickHouse run. Follow-up run
`45189185651` subsequently passed both ClickHouse independent-store cases
(`fresh` and `released_upgrade`), including cleanup during link commit and
metadata-only tasks, on 25.8.33.6. Full backend checks also passed. The integration
job progressed to the explicit MySQL `TestUpdateChannelStatusCheckedCacheRouting`
selector, where 16 subtests failed after the first case. Later integration/race
steps were not executed in that run; release acceptance remains pending.

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
