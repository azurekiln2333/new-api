# R18 / R19 independent review and frontend completion

Reviewer: `/root/r2_finish`, 2026-11-20. Backend repair owner: `/root/r8_queue`.
Root owns staging, commits, locale writes, integration CI and publication.

Root exported the R18-only staged tree `8b2faf9093d048773843991374ddb53826c8e94e`
to `D:/AppData/Local/Temp/new-api-verify-r18`. The isolated random-rotation/cache/
audit suites passed in model (9.489s) and controller (3.369s), independently of
parallel R7/R8/R19/R22 working-tree changes. CI includes per-dialect rotation
tests and the existing real database/Redis matrix. Nine R18 UI keys were merged
into all seven locales with the required script and sync workflow.

## Frontend completed

- R18 rotates the same selected token through the shared `ConfirmDialog`,
  `SecureVerificationDialog` and `CopyButton`. The proof is bound to the selected
  token ID and the request is marked `singleUseAuthorization` so authentication
  refresh cannot replay a consumed proof. Cancellation issues no mutation; a
  rejected mutation stays open and requires a fresh proof on retry.
- Replacement credentials are held only in modal memory and cleared on close.
  The provider revokes cached reveals before submission and again on success.
  Per-token epochs reject older single and batch responses. Synchronous cache
  invalidation also protects a copy issued before React rerenders. An old request
  cannot clear the replacement request's pending/loading state. Mixed cached /
  uncached batches exclude a token rotated while their request is pending.
- R19 adds an optional password input on create and rotate. Empty values retain
  secure random generation. Custom creation is exactly one key and uses the
  `token.key.custom_create` proof. The input is sent as typed; neither UI path
  trims a secret. Creation rejects malformed length/character/whitespace input
  locally and the backend remains authoritative for strength and uniqueness.
  Duplicate errors keep the create form editable, with a field error. A retry
  obtains a fresh proof. Custom input clears after successful creation or close.
- Editing ordinary token settings never sends a custom secret. Random batch
  creation retains its existing payload / behavior.
- Component reuse: existing `ConfirmDialog`, `CopyButton`, form fields, drawer
  layout and `SecureVerificationDialog` cover all added interactions. No shared
  widget or third-party dependency was introduced.

## Frontend verification

Toolchain: portable Bun in `D:/AppData/Local/Temp/new-api-build-tools/bun-windows-x64`,
Node in `D:/ProgramFiles/nodejs`; commands run from `web/`.

1. Added the cache regressions first. Before the fix, 3 of 4 tests failed with
   stale copied credentials / incorrect pending state; after the fix all pass.
2. Added custom-create interaction regressions first; all 3 initially failed
   because the field/flow did not exist. Added duplicate retry coverage after
   implementation.
3. `bun run test src/features/keys --maxWorkers=2`: **PASS**, 10 files / 87 tests
   on 2026-11-20 12:09 (22.98 seconds). Includes rotation success/copy/close,
   proof cancellation and fresh-proof retry, custom rotation exact raw input,
   create verification/single count/whitespace/duplicate retry, and existing
   listing, group, quota, expiry and connection-info behavior.
4. `bun check -b`: **PASS**, 1,590 files / 2 projects, latest 12:14.
5. Scoped `bun x --no-install oxlint -c .oxlintrc.json` on all 13 changed
   frontend files: **PASS**. A pre-existing type-only import in the changed
   `keys/constants.ts` was fixed. Broad keys-directory lint also finds two
   pre-existing import errors in untouched `data-table-bulk-actions.tsx`; no
   claim of repository-wide lint success.
6. Scoped protected-header-preserving formatting applied; `git diff --check`
   on owned files **PASS**. No broad formatter was run.

## Backend findings handed to repair owner

- The previous 10-second Redis fence allowed an arbitrarily delayed old database
  snapshot to receive a fresh signed cache expiry and republish a revoked key.
  `/root/r8_queue` is implementing a generation-guarded cache publication path
  with row-lock protection for cache misses, preserving the existing no-database
  cache-hit contract. Final independent tests are pending below.
- Secret-bearing GORM writes could log the full SQL before duplicate-error
  sanitization. Repair owner applies scoped `logger.Discard` and value-free
  driver errors; this was reproduced by the initial controller test run.
- Custom create's existing success test lacked its new scoped proof; the initial
  controller run failed only at `TestAddTokenAcceptsAValidatedCustomKey` with
  `Security verification is no longer valid. Please verify again.` Owner repairs
  the proof fixture.
- Earlier raw-key trimming, dialect quoting, cache invalidation error handling,
  session-only rotation and custom-create proof requirements were identified by
  root and addressed in the shared backend implementation.

## Final independent backend review

The repair owner has completed the generation-guarded cache protocol. The
reviewer traced generation capture before the database snapshot, generation
expiry/deletion, Redis state loss, delayed publication, rotation rollback and
invalidation failure. A Redis invalidation error aborts rotation before the SQL
write; an unavailable generation permits an uncached database read only. Cache
hits retain their existing no-database behavior. Cache-miss publication remains
inside the same row lock used by rotation, and the Lua publication checks the
captured generation atomically with the fence.

Final review found an additional concrete SQLite configuration defect: a custom
`SQLITE_PATH` containing a query string can omit `_txlock=immediate`, while
SQLite's shared `lockForUpdate` has no row lock. The backend owner reproduced a
revoked-key read in the deferred-transaction matrix before the fix. Both
credential transactions now call `lockTokenCredentialSnapshot` before their
first SELECT. Its zero-row SQLite write acquires the writer lock without
changing token data; MySQL/PostgreSQL retain `lockForUpdate`. The fixed path no
longer depends on SQLite DSN transaction mode.

Independent final commands using `GOMAXPROCS=2` and portable Go:

- `go test -p 1 ./controller -run 'TestNormalizeCustomTokenKey|TestRotateTokenKey|TestAddToken.*CustomKey|TestTokenKeyUniquenessErrorsStayValueFree' -count=1`:
  **PASS**, 2.344s. Secret-bearing SQL output seen before the scoped logger fix
  is absent after it. Ownership/proof/replay/PAT cases pass.
- `go test -p 1 ./model -run 'TestToken(Key|Auto|Allow|Update)|TestAuthCacheIntegrityDatabaseMatrix|Test.*QuotaReserv' -count=1`:
  **PASS**, 0.145s, before the final SQLite deferred fix.
- After that fix, `go test -p 1 ./model -run '^TestTokenKeyLifecycleDatabaseMatrix$|^TestTokenKeyRotationRejectsDelayedCacheSnapshot$|^TestAuthCacheIntegrityDatabaseMatrix$' -count=1 -v`:
  **PASS**, 0.206s. Real SQLite **3.50.4**, both `immediate` and `deferred`;
  delayed/expired/flushed generations, cache-hit database outage, permission
  integrity and concurrent quota preservation pass. Redis is miniredis here.
  MySQL/PostgreSQL explicitly skipped because their DSNs were absent; this is
  not a three-engine or real-Redis pass.

No additional concrete defect remains in the reviewed implementation. External
engine/Redis CI, translations and publication remain root-owned release gates.

## Commit split / locale handoff

R18-only frontend snapshots are saved under
`D:/AppData/Local/Temp/new-api-r18-frontend`; `files.txt` contains the 10 repository
relative paths. This snapshot is independently typechecked and has 7 R18 tests.
Root may stage snapshot contents without resetting the final working tree, then
stage the remaining final diff as R19. Root must coordinate the matching backend
R18/R19 split with `/root/r8_queue`.

R18 frontend files: API rotate function, rotation dialog, reveal provider,
dialogs/menu wiring, dialog type, success constant, rotation proof type, and the
two new reveal/rotation test files.

R19 frontend files: custom-create API options, custom-create proof union member,
form schema/defaults, create drawer and existing drawer test extensions; custom
rotation input/API argument and one extra rotation test.

All 14 frontend source keys have seven-language data in
`D:/AppData/Local/Temp/new-api-r18-r19-ui-translations.json` in `{locale:{key:value}}`
format. First nine keys belong to R18; last five belong to R19. Root must merge
with `add-missing-keys.mjs` and run `bun run i18n:sync`. Locale files were not
edited by this reviewer. Backend error keys must also be included by root.

## Pending gates

- Backend owner release and independent local tests / vet are complete; the
  final model-only reviewer vet after the SQLite fix also passed.
- Real MySQL and PostgreSQL persistence and uniqueness / rotation checks, plus
  real Redis cache race checks remain pending. Real SQLite 3.50.4 passed in both
  transaction modes. No MySQL/PostgreSQL/Redis daemon is available locally to
  this reviewer. Miniredis is not a substitute for the real Redis gate.
- Locale merge/sync, root independent review, focused commits and exact-commit CI.
- No repository-wide security or database compatibility claim is made here.

## Backend completion and repair verification

Owner: `/root/r8_queue`, final local check 2026-11-20. The frontend findings above
are repaired and the final backend is released for root staging and independent
review.

- R18 adds `POST /api/token/:id/key/rotate`. It is browser-session-only,
  owner-scoped, rate-limited and cache-disabled. The single-use security proof
  uses `token.key.rotate` and the exact `token_id`. The transaction changes only
  the credential and returns the replacement plus the masked existing row.
  Audit records contain the operation and token identity, never the secret.
- R19 accepts an optional raw `key` on create and rotate. Custom creation uses
  browser-session-only proof `token.key.custom_create`; random creation retains
  PAT access. The server strips only an optional leading `sk-`, permits 32-128
  ASCII letters/digits, and rejects low-diversity, obvious ordered, repeated,
  and weak-word patterns. It never trims whitespace. The unique index is the
  final race guard and duplicate failures do not change the current credential.
- Secret-bearing queries, insertion and rotation suppress SQL traces locally
  with `logger.Discard`. Driver errors become value-free stable messages,
  including errors whose raw payload contains the secret. `DB.Debug()` and
  forced driver errors are regression-tested.
- The old cache-fence race is repaired by capturing a generation nonce before
  the database snapshot and keeping the credential lock through publication.
  Invalidation atomically removes hash and generation. Delayed snapshots fail
  after fence expiry, generation expiry/replacement, or Redis state loss. Hot
  verified cache reads retain the no-database contract; live Redis quota values
  survive metadata repair.
- Independent review found a custom SQLite deferred-transaction DSN bypass of
  the initial lock assumption. The new deferred-DSN regression failed before
  the repair because the old-key reader succeeded. Credential transactions now
  take a SQLite writer lock with a zero-row GORM update before any read; default
  immediate and custom deferred modes both pass. No database rows or counters
  are changed by that lock operation. MySQL/PostgreSQL retain `lockForUpdate`.

Commands (portable Go toolchain, `GOMAXPROCS=2`, from repository root):

```text
go test -p 1 ./model ./controller -run 'TestToken.*Cache|TestTokenKey|TestAuthCacheIntegrityDatabaseMatrix|TestAuthCacheKeysShareClusterHashTag|TestNormalizeCustomTokenKey|TestRotateTokenKey|TestAddToken.*CustomKey|TestAPITokenAuditDatabaseMatrix' -count=1 -timeout 90s
PASS: model 5.352s; controller 5.252s

go test -p 1 -overlay D:/AppData/Local/Temp/new-api-r18-backend/overlay.json ./model ./controller -run 'TestToken.*Cache|TestTokenKey|TestAuthCacheIntegrityDatabaseMatrix|TestRotateTokenKey|TestAPITokenAuditDatabaseMatrix' -count=1 -timeout 90s
PASS: R18-only model 5.354s; controller 4.359s

go vet -p 1 ./model ./controller ./middleware ./router ./service
PASS (the earlier unrelated fingerprint mutex-copy findings are repaired)

git diff --check -- <all 12 owned backend paths and database-guidelines.md>
PASS
```

Real local database used: SQLite **3.50.4**, file-backed WAL, 30-second busy
timeout, immediate and deferred transaction modes. MySQL and PostgreSQL were
explicitly skipped because their DSNs are unavailable. Redis tests used
miniredis; real Redis remains a CI integration gate. No schema/tag changes are
introduced, so fresh/released-schema migration changes are not part of R18/R19.

Required CI additions: run `TestTokenKeyLifecycleDatabaseMatrix` with real
`TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, and `TEST_REDIS_ADDR`. It covers rotation
with concurrent cache refill after Redis loss and concurrent duplicate custom
inserts. Also run the controller custom/rotation suite under each configured
`TEST_SECURITY_DIALECT` (`mysql`, `postgres`):

```text
go test -p 1 -count=1 -v ./controller -run 'TestNormalizeCustomTokenKey|TestRotateTokenKey|TestAddToken.*CustomKey|TestTokenKeyUniquenessErrorsStayValueFree'
```

Backend R18-only snapshots are in
`D:/AppData/Local/Temp/new-api-r18-backend`; `files.txt` lists the 12 repository
paths. The snapshots contain random rotation, cache/security repairs and their
tests. Stage these blobs plus the API credential section of
`.trellis/spec/backend/database-guidelines.md` for R18, then stage the remaining
working-tree diff for R19. Root alone stages/commits; the final working tree was
never overwritten to produce the split. Backend error keys listed below belong
to R19 and must be merged into all seven frontend locales:

```text
This API key is already in use
The new API key must differ from the current one
The API key must be {{min}}-{{max}} characters after the optional prefix
The API key may only contain ASCII letters and digits
The API key is too easy to guess; use at least {{count}} different characters
The API key is a predictable sequence; choose an unrelated value
The API key repeats too short a pattern; choose an unrelated value
The API key contains a common easy-to-guess word
```

Security references: task research `../11-19-playground-capabilities/research/keys-cors.md`
records OWASP ASVS **5.0** credential change/revocation and CSRF controls
(6.2.3, 6.5.6, 3.5.1-3.5.3); Authentication and Session Management Cheat Sheets
were read from the OWASP checkout pinned at
`c2a1e3e32cec12f27be0a3b1e771ce51288a363e`. Applied controls are server ownership,
session-only sensitive operations, scoped proof expiry/single use, old-key
revocation and secret-free auditing. Tests cover foreign ownership, proof
scope/token mismatch, expiration, replay, PAT bypass, whitespace/weak values,
collisions and Redis failure. This is a record of the controls verified for this
change, not an assertion of application-wide ASVS compliance.

## Record closure - commit state supersedes pending gates (2026-11-20)

This section closes the record. It supersedes the locale and CI statements above;
nothing in the implementation changed after the release.

**Commits.** R18 regeneration is `3254adb2c` (`feat(keys): regenerate credentials
with immediate cache revocation`) and R19 is `e19368c54` (`feat(keys): allow
validated custom API credentials`). Root pushed both and reported the R19
pipeline fully green on the cloud CI. That pipeline result is root's report; this
record does not re-derive it.

**Locales are no longer pending.** The earlier paragraphs that told root to merge
`D:/AppData/Local/Temp/new-api-r18-r19-ui-translations.json`, run
`add-missing-keys.mjs`, and "include" the backend error keys are historical. Read
back from `e19368c54`, all 14 R18/R19 UI keys and all 8 API-credential error keys
are present in each of the seven locale files (en, zh, zh-TW, fr, ja, ru, vi).
No locale merge or sync step remains for this task.

**Verification timing.** The local checks described above (isolated model and
controller suites on real SQLite 3.50.4 in both transaction modes, frontend
typecheck, seven R18 tests, miniredis coverage) ran before the user prohibited
local test/build/lint/browser runs. After that instruction this lane executed no
local command of that kind, so the final state of this task is verified by the
cloud CI pipeline only. MySQL, PostgreSQL and real Redis coverage likewise exists
only through CI; no local daemon was ever used for those gates.

**Ownership.** No production or locale file was edited during this record
closure. Root owns staging, commits and publication. The three residual R22
service defects were handed to `r2_finish` and are not addressed here.

## User-requested rotation/dialog corrections

Commit `153c599c9` removes the additional security-proof prompt for owner key
rotation under the explicitly approved authenticated-session policy. The existing
server ownership, session-only and origin checks, credential revocation and
secret-free auditing remain enforced. Custom creation retains its proof flow.
Commit `5c5a1353c` wraps long replacement keys and adapts the result dialog to
the viewport. Selector correction `e3a278062` uses the shared alertdialog role.
Cloud CI `44728516165` passed all jobs at `2fcaab4c6`, including these corrections
and the real database/Redis checks. This supersedes the earlier universal
security-proof wording for regeneration, and is not a full ASVS compliance claim.
