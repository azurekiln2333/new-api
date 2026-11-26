# Logging Guidelines

## Original relay request diagnostics

### 1. Scope / Trigger

Apply this contract to authenticated inference capture, consume/error/refund log
writing, asynchronous task provenance, administrator detail reads and retention.
It does not authorize request bodies in audit events or process logs.

### 2. Signatures

- `common.BeginRequestContentCapture(c) bool` installs one authenticated POST
  owner before protocol preparation or plugins. `common.RequestContent(c)`
  returns detached original text independent of `BodyStorage` replacement.
- `model.WithLogRequestDetail(ctx, snapshot, writeLog)` links a log with bounded
  diagnostics. `model.InsertTaskWithRequestContent(ctx, task, snapshot, ...)`
  must insert the authoritative accepted task exactly once.
- `GET /api/log/request/:ref` uses `DisableCache()` before `AdminAuth()` plus the
  existing PAT `log:read` rule. Success data is
  `{ref,request_id,submit_node_name,status,captured_bytes,truncated,payload}`;
  `payload` is an object, not a JSON-encoded string.
- Payload rows live only in `LOG_DB.log_request_details`. Primary
  `DB.log_request_content_protections` and `TaskPrivateData.RequestContent` hold
  references and coordination metadata, never payload bytes.
- `LogRequestPayload.Value() (driver.Value, error)` exposes native string bytes
  to database drivers; `(*LogRequestPayload).Scan(any) error` accepts strings,
  byte slices and SQL NULL. Keep the dialect-specific text column definition.
  ClickHouse's named-value path does not perform the default `database/sql`
  conversion of an arbitrary named string.

### 3. Contracts

- Capture only `messages`, `prompt`, `input`, `instructions`, `system`, `contents`
  and `systemInstruction`, including whitelisted multipart text fields. Keep
  roles/text structure; omit credentials, file bytes and signed media URLs.
- Capture defaults to enabled with a 262144-byte limit. Persisted
  `log_setting.request_content_enabled` / `request_content_max_bytes` supply
  cluster defaults; explicitly set `REQUEST_CONTENT_LOG_ENABLED` /
  `REQUEST_CONTENT_LOG_MAX_BYTES` override them on each node. Bounds remain
  256–134217728 bytes and the server body limit. The byte budget includes escaped
  JSON overhead. Truncation leaves valid UTF-8 and JSON; rejected unread bodies
  remain unread.
- `RequestContentSnapshot.Payload` has `json:"-"`. Only the authorized detail DTO
  serializes it. Ordinary logs, lists/exports, consume parameters and process
  logs must not carry this payload.
- `other.admin_info.node_name` is the instance executing that log operation.
  `submit_node_name` is frozen ingress provenance. A US settlement does not
  inherit HK as its processing node; historical task nodes are submission-only.
- A body read requires an exact surviving consume/error/refund link matching
  ref, server request ID and user. A random ref or displayed list ID is not
  authorization. Self/token projections omit `admin_info`.
- Same-store detail/link operations share one transaction. Supported configured
  SQLite `local` aliases resolve to `SQLitePath` and reuse the primary handle in
  `InitLogDB`; a second pool cannot write that file while its writer lock is held.
- Independent stores commit `write_pending`, then `link_pending` before a remote
  log insert. Holder/generation checks and exact `link_id` fence unknown outcomes.
  An older retry's surviving ref does not prove the pending insert committed.
  Pending deletes and retired refs cannot be reused; late bytes are swept.
- Cleanup checks renewable ingress leases, surviving logs and durable task
  liveness, including terminal `BudgetPending` and unresolved refunds, using the
  primary transaction. Storage errors never mean no references.
- `log_setting.retention_days` defaults to 0 (keep forever). An absent
  `log_setting.request_content_retention_days` defaults to 3 days; explicitly
  persisted 0 remains keep-forever, and positive overrides remain unchanged.
  Both retain whole-day bounds 0–36500. Master-only scheduled usage cleanup selects bounded
  relational ID pages; content expiry preserves usage rows and active task/writer
  protections. `expire_pending` / `retired` fences reject reads and delayed writes
  even while an old usage link survives. Never treat age alone as permission to
  remove an uncertain pending operation.
- `model.ExpireLogRequestDetails(ctx, cutoff, limit)` uses an exclusive creation
  cutoff and existing cross-store fencing. `GET /api/option/log_storage` is
  root-only plus PAT `option:read`, with `no-store`; response data includes
  `tables[{name,database,bytes,estimated}]`, effective `capture_enabled` /
  `capture_max_bytes` and their override booleans. Unavailable bytes are null,
  never fabricated zero; SQLite must not scan payloads for per-table sizes.
  PostgreSQL deletion only makes space reusable; VACUUM FULL / pg_repack is a
  separate authorized operational action, never automatic application cleanup.
- Stored request payloads may use `newapi-request-content:gzip:v1:` followed by
  base64 of a gzip BestSpeed stream. `REQUEST_CONTENT_LOG_COMPRESSION_ENABLED`
  defaults to true and controls new encoding only. Try payloads at least 4096
  bytes and keep the encoded form only when the complete marker/base64 value
  saves at least one eighth of the original bytes. `captured_bytes`, truncation
  and immutable request snapshots always describe the original JSON.
- Decode only in `GetLogRequestDetail` after the surviving-link, user/request
  identity and protection/tombstone checks. Reading is independent of the write
  flag and today's capture limit. Enforce the historical 128 MiB bound, strict
  base64, one gzip member, at most captured_bytes+1 decoded bytes, exact original
  length, checksum/end-of-stream/no trailing data and a valid JSON object.
  Legacy plain JSON and historical captured_bytes=0 remain readable. Corruption
  returns the fixed value-free storage error through the existing empty404;
  never expose the encoded envelope, partial body or raw codec error.
- Compression rollout requires all readers to support the decoder. Set the
  compression flag false on initial upgraded nodes during a rolling upgrade,
  then enable after all readers are compatible. An older binary cannot read
  compressed rows; false does not convert them. Roll back only to a compatible
  decoder build unless a separately authorized conversion/restore is performed.
  PostgreSQL TOAST and ClickHouse already have engine compression: application
  byte savings are not evidence of a specific physical-disk saving.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Anonymous/model token/common user/PAT without scope | Existing authentication/authorization denial; no body; `no-store` |
| Malformed, unknown, unlinked or cleaned reference | Empty 404, even before physical cleanup |
| Detail preparation fails before log/task callback | Business insert still runs; persisted metadata is `storage_failed` |
| Accepted task insertion or commit was attempted and fails | Return its error; never attempt a second authoritative insert |
| Final API error has `ErrOptionWithNoRecordErrorLog` | Mark `request_error_log_excluded` at the final boundary; fallback cannot recreate it |
| Independent remote insert/delete has uncertain outcome | Keep pending/retired fencing; lease expiry alone is not proof of rollback |
| Metadata-only later caller finds no original detail | Do not synthesize an empty recorded body |
| Retention days outside 0–36500, fractional days, non-boolean capture or bytes outside 256–134217728 | Reject single/batch settings before persistence/publication |
| Expired/retired content with a surviving usage link or delayed remote bytes | Empty 404; keep the value-free tombstone and reclaim late bytes safely |
| Storage metadata unavailable or not cheaply supported | Return null bytes; no payload/count scan and no invented empty table |
| Absent content retention / explicit stored 0 | Use 3 days / preserve keep-forever respectively |
| Corrupt, oversized, unknown-version or multi-member compressed payload | Fixed value-free storage failure; no envelope or partial JSON response |
| Compression flag becomes false after encoded history exists | New writes stay plain; existing encoded history still reads correctly |

### 5. Good / Base / Bad Cases

- Good: retry error and later success share one immutable snapshot; deleting one
  retry preserves the success-linked body. HK submission and US refund retain
  distinct submission/processing nodes.
- Base: old logs remain readable summaries without invented content or nodes;
  explicit zero retention leaves their history untouched.
- Good: the absent content setting uses three days; unpinned payloads
  expire independently while logs remain and active tasks retain their payload.
- Bad: serialize a snapshot into consume parameters, fetch by display ID, overwrite
  an explicit zero on upgrade, or expire an uncertain fence merely because time passed.

### 6. Tests Required

- `TestRequestContentCapture.*`: memory/file storage, actual replacement,
  escaped UTF-8 byte budget, nested secrets/media, multipart and status states.
- `TestLogRequestDetailStorageDatabaseMatrix`: real SQLite/MySQL/PostgreSQL,
  shared/independent stores, fresh/unchanged released-log schema, migration twice,
  >64 KiB payload, surviving retries, task liveness, cleanup during publication.
  Missing DSNs explicitly skip; configured connection failures must fail.
- `TestLogRequestPayloadIsAStringDriverValue`: the installed ClickHouse String
  converter must accept the actual payload type and preserve its bytes. This
  local driver regression complements the real server storage matrix.
- `TestLogRequestDetailPayloadCodec`: beneficial compression, tiny/incompressible
  fallback, flag-independent decode and all bounded corruption failures.
  The real storage matrix also inspects the raw stored column and reads both
  legacy/new history after lowering today's capture cap. Driver coverage includes
  an actual compressed string. Test byte ratios are fixture-only observations.
- `TestUpdateOptionPersistsAcrossDialects`: absent content retention is3; persist0,
  reset the live field to3, then reload0 from actual SQLite/MySQL/PostgreSQL.
- `TestLogRequestDetailConfiguredSQLiteAlias`: actual configured
  `SQL_DSN=local` / `LOG_SQL_DSN=local` startup and persisted payload/link.
- `TestLogRequestDetailIndependentStoreRecovery` and
  `TestLogRequestDetailWriterLeaseAndTaskFallback`: exact late links, lost delete
  acknowledgement after actual SQL, retired late writes, lease release and task
  fallback without duplicate authoritative insertion.
- `TestLogRequestContentAuthorizationAndLinks`,
  `TestRequestContentRelayLogLifecycle` and
  `TestResponsesWSRequestRunnerRequestContentDiagnostics`: server permissions,
  no-store on denials, associations, original prompts, node provenance, per-turn
  snapshots and final no-record exclusions.
- `TestLogRequestDetailRetentionDatabaseMatrix`: actual relational/ClickHouse
  stores, repeated migration and legacy preservation, exclusive cutoff,
  writer/task/pending protection, surviving log, delayed-write tombstone,
  idempotency, bounded repeated relational deletion and honest metadata.
- `TestRequestContentCapturePersistedSettingsAndEnvironment`,
  `TestSystemTaskLogRetention` and `TestPersonalAccessTokenLogStorageScope`:
  explicit environment precedence, keep-forever/follower-off scheduling,
  disable-at-next-batch and root/scope denial. Scheduler fixture is SQLite-only;
  it is not a replacement for the real storage matrix.
- These are required assertion points, not passing-results claims. Run the
  current workflow on the final integrated SHA; source-authored or skipped
  fixtures do not establish compatibility.
- Run `go test -race -p 1 -count=1 ./model -run '^TestLogRequestDetail'` with real
  relational DSNs. ClickHouse requires actual `TEST_CLICKHOUSE_DSN` execution;
  a skip or relational pass does not verify native TTL, duplicate/nontransactional
  writes or synchronous mutations.

### 7. Wrong vs Correct

Wrong: hold a SQLite primary writer transaction, then insert the body through
another pool to that file; or clear `link_pending` because any retry shares its ref.

Correct: reuse the same-store transaction/handle; for independent stores retain
operation identity and require its exact link before releasing the fence.

Wrong: enable compressed writes while an old reader is still serving, or use
today's smaller capture cap to reject valid historical data.

Correct: stage all-reader decoding first and keep a fixed historical read bound;
toggle new writes independently from decoding and from retention.

Wrong: weaken production activation/storage checks to repair a fresh fixture.

Correct: seed required `Option`/allocation schema before follower `InitDB`, use
isolated external databases for fixed-name allocation tables, and close fixture
handles in cleanup. Keep the production fail-closed check unchanged.

Relay/adaptor tests without their own database use
`internal/testutil.UseInactiveChannelBudget(t)`. It creates a temporary SQLite
`Option` table with no durable activation marker, sets
`CHANNEL_BUDGET_ENABLED=false`, and restores both database handles and dialects
in cleanup. Tests that already own a database add `Option` to that fixture
instead. A false process flag alone cannot replace the activation read. Do not
set `IsChannelTest` or change runtime admission to make these fixtures pass.
