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

Channel-level routing configuration that needs no schema change lives in two existing
JSON columns. Both are whitelists intersected with `channels.models`, so stale names
are ignored instead of erroring.

| Config | Struct | Column | Contract |
|---|---|---|---|
| key index -> models | `model.ChannelInfo.MultiKeyModels` | `channels.channel_info` | missing/empty = the key serves every channel model |
| key index -> user id | `model.ChannelInfo.MultiKeyUsers` | `channels.channel_info` | missing/0 = public key; a bound key is exclusive to that user |
| group -> models | `relaykit/dto.ChannelOtherSettings.GroupModels` | `channels.settings` | missing/empty = the group sees every channel model |

Rules that keep this consistent:

- **Key-indexed maps follow key contents, not indexes.** Any path that changes the key
  list (`Channel.Update`, `UpdateChannel` key append/replace, `ManageMultiKeys` delete
  actions) must remap the per-key maps by "old key string -> new index"
  (`Channel.remapKeyScopes`). A map keyed by index alone silently moves a binding onto a
  neighbouring key after a delete.
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

## Common Mistakes

<!-- Database-related mistakes your team has made -->

(To be filled by the team)
