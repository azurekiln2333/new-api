# Database Guidelines

> Database patterns and conventions for this project.

---

## Overview

<!--
Document your project's database conventions here.

Questions to answer:
- What ORM/query library do you use?
- How are migrations managed?
- What are the naming conventions for tables/columns?
- How do you handle transactions?
-->

(To be filled by the team)

---

## Query Patterns

<!-- How should queries be written? Batch operations? -->

(To be filled by the team)

---

## Migrations

<!-- How to create and run migrations -->

(To be filled by the team)

---

## Naming Conventions

<!-- Table names, column names, index names -->

(To be filled by the team)

---

## Per-key and per-group channel configuration (channel key scoping)

Channel model scopes intersect with `channels.models`. After allocation migration,
`channel_key_entries` is authoritative for per-key identity, binding, status and
model scope. Key-indexed fields in `channel_info` are compatibility projections;
group model policy remains in `channels.settings`.

| Config | Struct | Column | Contract |
|---|---|---|---|
| allocation -> models | `model.ChannelKeyEntry.ScopeMode` / `Models` | `channel_key_entries` | `all` serves channel models; `allowlist` intersects models, including an explicitly empty scope |
| allocation -> user id | `model.ChannelKeyEntry.BoundUserId` | `channel_key_entries` | 0 = public; a bound allocation is exclusive to that user |
| group -> models | `relaykit/dto.ChannelOtherSettings.GroupModels` | `channels.settings` | missing/empty = the group sees every channel model |

Rules that keep this consistent:

- **Allocation identity survives duplicate credentials and reordering.** Migrate
  legacy entries by their original position exactly once. Thereafter use stable
  allocation IDs and revisions for mutation, selection and asynchronous writeback.
  Never remap duplicate entries by credential text or infer identity from the
  current array index. Refresh projected maps from the authoritative rows.
- **Fixtures must mutate the same authoritative state as production.** Once
  allocation rows exist, raw updates to `channels.key` or the key maps in
  `channel_info` do not rotate, reorder, bind or disable an allocation. Use
  `UpdateChannelLocked` or `MutateChannelKeyEntries`, then read back the channel.
  Keep stale source snapshots unchanged when testing delayed-result rejection;
  replacing the snapshot would remove the concurrency assertion.
  Reusable-connection fixtures must establish allocation rows before their first
  request, using the public channel insertion path. A raw channel insert can let
  the first polling update migrate its identity after the socket already captured
  it, correctly causing the next turn to reject that stale credential identity.
  Explicitly migrate the allocation table on every fixture engine; do not rely
  on schema left behind by an earlier external-database test.
- **Delayed allocation status writes retain guarded cache publication.** Compare
  the pre-write credential/configuration and allocation IDs/revisions under the
  cache lock, then publish only committed status metadata and revisions. Preserve
  newer cached settings and polling state; an admission-cap-only change is allowed.
  A full configuration reload after a health result can overwrite a newer cache
  replacement even when the database write was correctly guarded. Administrative
  mutations retain their full refresh after an actual write; a no-op is not a
  reason to reload. Re-enabling must immediately restore priority-ordered routes.
- **Fixture table prefixes do not isolate explicit TableName models.** Initialize
  and clean fixed-name allocation tables in the assigned disposable database before
  the first test, as well as between tests. `InitChannelCache` with caching disabled
  does not clear its ID/group routing maps. Load the newly installed fixture while
  caching is enabled when the test depends on that cache.
- **One derivation for `group x model`.** `Channel.channelAbilities()` in `model/ability.go`
  is the only place that turns a channel into abilities, and `model.InitChannelCache()`
  builds the in-memory routing table from the same inputs (`PublicModels()` and
  `GroupModels`). Changing only the abilities table leaves `MemoryCacheEnabled` routing
  (the default) wrong, so both must be updated together.
- **Abilities describe public keys only.** A channel whose keys are all bound to users
  produces no abilities at all; bound-key access is a separate path
  (`model.GetUserBoundChannel`) that runs before group routing and before session
  affinity, and it must never write affinity state another user could hit.
- **Key enable status is not part of abilities.** A temporarily disabled key must not
  change the routing table; the per-request key selection reports the miss instead.
- **"No key serves this request" is not a channel error.** Use
  `types.ErrorCodeChannelKeyScopeUnsatisfied` (503, no `channel:` prefix) so the channel is
  not auto-disabled; the distributor then falls back to the next candidate channel instead
  of forwarding with an empty key.

---

## Allocation identity and local budgets

### 1. Scope / Trigger

Changes to key management, channel selection, model visibility, local quota,
attempt settlement or asynchronous task accounting use this contract.

### 2. Signatures

- `ChannelKeyEntry.Id` is the stable allocation ID; API IDs are decimal strings.
- `POST /api/channel/multi_key/manage` reads `get_key_status.data.revision` and
  per-row `id`, `rev`, `scope_mode` and `quota`.
- Per-row writes carry `key_entry_id` and `expected_rev`; list writes carry
  `expected_revision`. Budget mutations also carry `expected_config_version`.
- Channel quota routes are `GET /api/channel/quota?ids=...`,
  `GET/PUT /api/channel/:id/quota`, and `POST /api/channel/:id/quota/reset`.
- `ActiveChannelBudgetTx(tx *gorm.DB) (bool, error)` reads activation through the
  caller's connection. `newBudgetView(account, active)` is a pure projection.
- `InsertTaskWithSubmissionCharge` commits the task, funding/token adjustment,
  request-detail linkage and desired ledger settlement together. The caller
  publishes the committed cache delta; `FinalizeTaskBudgetSubmission` only
  replays the durable ledger intent.

### 3. Contracts

- Duplicate static credentials may have separate public/user bindings and budgets.
  Managed credential refresh identities retain their separate uniqueness rules.
- Bound access precedes public-group routing, retains token model restrictions
  and normal billing group, and must agree with both model-list APIs.
- Wallet/subscription and API-token charging remain authoritative. Local channel
  and allocation budgets add admission limits; they never replace those charges.
- Reserve both budgets before upstream dispatch. Record actual usage, including
  excess above the estimate. Editing limits preserves usage and live holds.
  Reset changes the accounting epoch, preserves holds and never refunds users.
- Dedup is explicit preview/apply. Compare every effective restriction and quota
  configuration, conserve historical usage, and skip active reservations/tasks.
- Key enable/disable writes must retain channel availability semantics: only
  exhaustion of every key sets the all-keys-disabled reason, and enabling a key
  restores a channel only when that reason caused its disable. Preserve a later
  manual channel disable. Persist entry state, channel status/reason and abilities
  together, including bulk actions with the current list revision.
- Removing plugin credentials from a channel copy remaps every per-key field,
  including explicit scope mode. An empty `allowlist` must remain deny-all after
  its position changes; an empty model slice alone cannot represent that rule.
- Channel deletion is idempotent and retires allocations before removing the
  channel and abilities. Retained account rows support late task reconciliation
  after deletion. A missing channel is not a missing-accounting-store success.
- `CHANNEL_BUDGET_ENABLED` defaults to false. The durable activation marker is
  `options[internal.channel_budget.active_v1]`. Upgrade all nodes, pause and drain
  legacy requests, then activate consistently. An activated database cannot be
  bypassed with a false process flag. Database errors are not unlimited quota.
- Active accounting requires `BATCH_UPDATE_ENABLED=false` on every serving
  node, checked at startup and before admission even after activation is cached.
  Startup checks must read the environment as well as the runtime flag: main
  initializes the database before it starts the batch updater and sets that flag.
  Stop and drain old nodes and their pending batch updates before activation:
  an in-memory queued pre-consume is not durable funding for a task transaction.
  Feature-off installations retain their existing batch behavior.
- The initial ledger rollout rejects `/v1/realtime` before WebSocket upgrade
  and upstream dial while accounting is active. Its ongoing automatic work
  lacks per-turn ledger admission. HTTP and Responses WebSocket support remain
  in scope; this restriction does not claim to fix legacy Realtime accounting.
- Accepted task costs may exceed their estimate. Persist the actual funding,
  token and ledger target without treating settlement as fresh admission.
  An uncertain commit acknowledgement must not trigger either deferred funding
  refund or reservation release. Recovery reads must be context-bound and
  validate the stored task and reservation against the expected user, channel,
  allocation ID/revision and task ID before claiming a committed result.
  Scope recovery queries by expected user and channel, and reject duplicate
  matching task IDs. A verified commit completes the normal success path once;
  a verified rollback refunds funding and releases its ledger hold. Token-cache
  deltas use the credential actually charged, never a deleted token's old key.
- Calling a cache publisher once is not atomic delivery across SQL and Redis.
  Ledger reconciliation must never replay funding or cache deltas. Do not evict
  quota caches and refill from a database that may lag pending batch or
  asynchronous changes.
- Transaction code uses its `tx` for every query and shared `lockForUpdate` for
  row locks. A helper called in a transaction must not query global `DB` or cache
  activation observed only inside an uncommitted transaction.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Stale allocation/list/quota revision | Conflict without partial mutation |
| Inactive ledger, budget-dependent mutation | Explicit rejection |
| Activation lookup/storage failure | Fail closed; never infer unlimited capacity |
| Budget exhausted | Reject candidate without disabling its upstream credential |
| Reset while request is in flight | Keep hold; first settlement uses the current epoch |
| Refund of an older settled epoch | Adjust historical total without crediting the new period |
| Uncertain accounting outcome | Preserve durable reconciliation state without replaying wallet/token effects |
| Active ledger with batching enabled | Reject startup/admission; never count queued pre-consume as durable |
| Active ledger with Realtime upgrade | Local 503 before 101/upstream dial, including unlimited accounts |
| Accepted task actual exceeds estimate | Record full actual cost, conserve funding/token/ledger totals |

### 5. Good / Base / Bad Cases

- Good: two identical credential strings retain independent owners and stable IDs
  after reorder, reload, model discovery and delayed status updates.
- Base: an existing unique public key retains its original scopes and usage.
- Bad: deleting the first duplicate moves its binding onto the remaining row.
- Bad: copying a channel drops an explicit empty scope and publishes its key
  for every model, or enabling a key overrides a manual channel disable.

### 6. Tests Required

Run `TestChannelAllocationsIdentityAndBudgetRouting`,
`TestChannelAllocationsDedupConservesUsageAndConflicts`, existing key-scope/model
visibility regressions, and `TestChannelBudgetDatabaseMatrix` on real SQLite,
MySQL and PostgreSQL. The latter must cover a single-connection transaction and
rollback of a transaction-local activation marker. Exercise actual relay and
task callers for retry, fee-bearing failures, token-write failures after funding,
late settlement, refunds and reconciliation. Schema acceptance includes fresh
startup and upgrade from the actual previous release, twice each.
Run `TestLedgerTaskSubmission` through the real submission path, including a
test connection whose SQL commit succeeds before acknowledgement fails. Verify
both defers preserve the charge, replay cannot double debit, and subscription
over-estimates remain recorded. `TestChannelBudgetRealtimeRolloutGuard` must
exercise actual upgrade requests, durable activation with a false process flag,
storage failure, zero upstream calls on rejection and unchanged inactive flow.

### 7. Wrong vs Correct

Wrong: `transaction -> newBudgetView -> global DB query`, which deadlocks when
the transaction holds the only connection.

Correct: `transaction -> ActiveChannelBudgetTx(tx) -> newBudgetView(account, active)`;
publish activation to process cache only from a committed database observation.

---

## API credential rotation and Redis publication

### 1. Scope / Trigger

Apply this contract when changing a token credential, its authentication cache,
or the database/cache ordering used by token reads. A revoked credential must
not become valid again when a delayed reader publishes an older snapshot.

### 2. Signatures

- `GetTokenByKey(key string, fromDB bool) (*Token, error)` in `model/token.go`.
- `lockTokenCredentialSnapshot(tx *gorm.DB) (*gorm.DB, error)` locks before reading.
- `RotateTokenKey(id, userId int) (*Token, error)` changes the existing row's key.
- `RotateTokenKeyWithSecret(id, userId int, customKey string)` accepts an optional
  validated custom value; `NormalizeCustomTokenKey` strips only a leading `sk-`.
- `tokenCacheReadGeneration(key string) (string, error)` captures a nonce before
  the database read; `cacheInitToken(token Token, generation string)` publishes
  only if that same generation is still current.
- `invalidateTokenCacheForMutation(key string) error` atomically fences the old
  credential and deletes both its hash and generation in one Redis Lua script.

### 3. Contracts

- A verified Redis cache hit performs no database query. Redis quota counters
  remain authoritative during cache repair; publishing metadata must not replace
  an existing valid integer counter with a lagging database value.
- Cache misses obtain the generation before the database snapshot and retain the
  database lock until publication finishes. MySQL/PostgreSQL use `lockForUpdate`.
  SQLite first issues `UPDATE tokens SET id = id WHERE 1 = 0` through GORM to
  acquire its writer lock without changing rows. Do not rely on `_txlock` in
  `SQLITE_PATH`: custom DSNs may select deferred transactions.
- The generation is a random, expiring nonce. Rotation, expiration, and Redis
  restart/flush invalidate old publishers. The short fence suppresses ordinary
  refill; its lifetime is not the credential revocation safety boundary.
- Cache, fence and generation keys must share the Redis cluster slot. Never put
  a usable credential in a Redis key, audit payload, or SQL log.
- Rotation checks ownership inside the locked transaction and changes only the
  key. Its cache invalidation must succeed before the database update. Credential
  queries and writes use a scoped `logger.Discard`, including when DB.Debug is on.
- Custom values are 32–128 ASCII letters/digits with at least eight distinct
  characters; predictable sequences, short repetitions and common weak prefixes
  are rejected. Never trim a secret. The existing unique index is authoritative
  across users, including concurrent inserts and rotations.
- Custom creation requires the expiring, single-use browser-session proof
  `token.key.custom_create`. By the explicitly approved product policy on
  2026-11-20, rotation uses the authenticated browser session without an extra
  security-proof challenge. Both operations enforce ownership, browser-origin
  protection, rate limiting and secret-free audit on the server. Personal access
  tokens cannot authorize either operation; ordinary random creation retains its
  existing PAT behavior. Do not describe this policy as full ASVS compliance.

### 4. Validation & Error Matrix

| Condition | Result |
| --- | --- |
| Verified hot cache, database unavailable | Cached token is returned |
| Cache miss with Redis unavailable or active fence | Read database, do not publish |
| Captured generation differs or fence is active at publication | Skip publication |
| Rotation cannot invalidate Redis | Roll back, return a value-free database error |
| Old key queried after committed rotation | Not found; no old hash is republished |
| SQL/driver error includes a key value | Return a stable error; do not log raw SQL/error |
| Custom key is invalid, unchanged, or already in use | Value-free error; existing credential is unchanged |
| Custom creation lacks a valid scoped session proof | Reject before credential mutation |
| Rotation uses a valid owner browser session without an extra proof | Regenerate once and revoke the old credential |
| Rotation uses a PAT or another user's browser session | Reject before credential mutation |

### 5. Good / Base / Bad Cases

- Good: a cache-miss reader and rotation serialize across the snapshot and cache
  write, including when Redis loses its generation during the rotation.
- Base: ordinary verified cache hits retain the existing fast path.
- Bad: a reader holds an old snapshot for longer than the fence and signs it
  with a fresh cache expiry after rotation.

### 6. Tests Required

`model/token_auto_groups_cache_test.go` covers delayed snapshots after fence
expiry, generation expiry/replacement, Redis flush, unchanged quota counters,
and rotation concurrent with Redis loss and a new reader. Run the lifecycle
matrix with SQLite immediate and deferred DSNs plus real MySQL/PostgreSQL and
Redis (`TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, `TEST_REDIS_ADDR`). Missing external
services are explicit skipped gates, not database compatibility evidence.

Keep `TestAuthCacheIntegrityDatabaseMatrix`'s no-database cache-hit assertion and
`TestAuthCacheKeysShareClusterHashTag`. Controller tests cover owner checks,
session-only rotation, scoped single-use custom-creation proofs, and value-free
SQL logs under DB.Debug.
Run `TestNormalizeCustomTokenKey`, `TestRotateTokenKey`, `TestAddToken.*CustomKey`
and `TestTokenKeyUniquenessErrorsStayValueFree` with each
`TEST_SECURITY_DIALECT=mysql|postgres`; the default fixture covers SQLite.

### 7. Wrong vs Correct

Wrong: `read DB -> wait -> fence expires -> cacheInitToken(oldSnapshot)`.

Correct: `capture generation -> lock credential snapshot -> read DB -> publish
with captured generation -> commit`. Rotation takes the same lock and invalidates
the generation before replacing the credential.

---

## Channel configuration patch persistence

### 1. Scope / Trigger

Changes to the channel editor or its locked-row merge must preserve editable
configuration through the HTTP response, database and published routing cache.

### 2. Signatures

- `PUT /api/channel/` decodes `PatchChannel` and validates before mutation.
- `applyChannelConfigEdit(request *PatchChannel, stored *model.Channel)` merges
  onto the freshly locked row inside `UpdateChannelConfigAtomic`.
- `updateAuditParams(stored, baseline *model.Channel)` records changed names.

### 3. Contracts

The optional string field `model_mapping` must participate in the merge.
Omitted or JSON-null values preserve the locked value; a provided empty string
clears it. Preserve mapping source and target
casing: `deepseek-r1` mapped to `DeepSeek-R1` is a valid exact-case redirect.
Persist through the existing transaction/publication path; do not replace the
locked row with the request or a pre-transaction snapshot. Audit only changed
field names, never override values or credentials.
Do not restore other missing setters without reviewing their authorization:
sensitive changes must be authorized against the actual locked delta. A stale
request equal to the earlier snapshot must not overwrite a concurrent sensitive
edit after bypassing a change-based permission check.

### 4. Validation & Error Matrix

| Input | Required result |
| --- | --- |
| Mapping omitted or null | Retain its freshly locked value |
| Mapping is an empty string or empty object string | Clear the mapping and publish the clear |
| Valid replacement mapping | Response, database and local cache agree |
| Existing permission, validation or revision guard rejects the edit | Preserve stored configuration |

### 5. Good / Base / Bad Cases

- Good: editing an existing mapping changes the actual upstream DTO model.
- Base: renaming a channel without mapping fields retains its mapping.
- Bad: returning success after copying only scalar fields leaves the old mapping
  in both the database and cache.

### 6. Tests Required

`TestChannelUpdatePersistsModelMappingAndPublishesCache` in
`controller/channel_create_test.go` covers real PUT add/edit/clear/omitted/null
cases, response, database, enabled cache, audit and mapped DTO behavior with
exact target casing. `TestChannelUpdate`-prefixed regressions run in
the existing `TEST_CHANNEL_SQL_DSN` SQLite/MySQL/PostgreSQL CI loop. A local
cache assertion does not prove cross-node publication or deployed behavior.

### 7. Wrong vs Correct

Wrong: accepting `model_mapping` into `candidate` but never assigning it to the
locked `stored` channel before persistence.

Correct: `if candidate.ModelMapping != nil { stored.ModelMapping = candidate.ModelMapping }`,
then retain the existing validated transaction and cache publication.

---

## Multi-Key status reads and mapped capabilities

### 1. Scope / Trigger

Key-list performance changes must preserve a coherent configuration/allocation
snapshot. Mapping fixes must also repair persisted routing for existing channels
when in-memory routing is disabled.

### 2. Signatures

- `ReadChannelKeyEntriesSnapshot(ctx, channelID)` reads authoritative allocations.
- `ReadChannelBudgetViewsWithActivation(ctx, scope, ids)` returns views and the
  same activation observation used to construct them.
- `RepairChannelMappedKeyAbilities(ctx)` runs after `MigrateChannelKeyEntries`.
- `POST /api/channel/multi_key/manage` with `action=get_key_status` caps
  `page_size` at 100 and enriches only the selected page.

### 3. Contracts

Read channel and allocation rows in one short transaction, using the owning row
lock on MySQL/PostgreSQL and a read snapshot on SQLite. Keep the transaction out
of budget and username enrichment, and do not hold the process polling mutex
over those reads. Only an unmigrated channel may trigger allocation migration.
Read page budgets once and bound usernames in one batch. Activation/account
errors remain errors; username display lookup remains best-effort. Cancellation
must propagate through database reads and must not return a successful list.

The request model remains the public authorization identity. A Key allowlist
may match that exact identity or its final configured upstream mapping target;
mapping does not add the upstream-only name to the public catalog or relax
token/group rules. Empty explicit allowlists deny all. Budget admission must use
the same capability rule. Preserve exact spelling through the mapping chain.

Startup repair scans mapped channel IDs in bounded batches, locks/reloads each
affected channel with authoritative allocations, and updates only its derived
abilities if they differ. Retain configuration, allocation IDs/revisions,
budgets and unrelated channels. A second startup must perform no extra ability
rewrite. Do not use a global abilities-table wipe for this upgrade.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Migrated status read | No allocation reconciliation or publication |
| Requested page exceeds 100 rows | Return a page of at most 100 |
| Activation/account read fails | Error, never invented unlimited quota |
| Username lookup fails | Keep authoritative row with empty display name |
| Key allows `DeepSeek-R1`, channel maps `deepseek-r1` to it | Public alias is eligible |
| Key allowlist is empty, unrelated, or bound to another user | Preserve denial |
| Existing uncached channel lacks its mapped alias ability | Repair on startup |

### 5. Good / Base / Bad Cases

Good: derive the Key preview and quota for the requested page while retaining
whole-channel status counts. Base: an unscoped Key still serves the channel's
authorized catalog. Bad: iterate username reads per row, or repair only the
in-memory map and leave uncached upgraded installations unable to route.

### 6. Tests Required

`TestMultiKeyStatusRead*` covers query amplification, page/filter bounds, stable
identity, stale compatibility JSON, activation/account failures, legacy migration
and cancellation. `TestChannelAllocationsMappedModelScope` covers request identity,
exact upstream spelling, cached/uncached routing and budget admission.
`TestRepairChannelMappedKeyAbilitiesUpgrade` seeds a missing old alias, executes
the real migration twice and checks routing plus unchanged configuration/budgets.
Run each affected database contract on real SQLite, MySQL and PostgreSQL in CI.
Source query counts are not production latency measurements.

### 7. Wrong vs Correct

Wrong: match Key scope only against the public alias, then require the user to
resave every old channel to restore database-backed routing.

Correct: share the mapped capability predicate with budget admission and repair
only affected derived abilities during the existing startup migration.

---

## Common Mistakes

<!-- Database-related mistakes your team has made -->

(To be filled by the team)
