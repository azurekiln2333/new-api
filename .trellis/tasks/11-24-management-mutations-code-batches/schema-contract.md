# Management create identity and audit outbox

Status: source implementation and regression source are present. No local build,
test, vet, formatter, database probe, or migration execution has been run. Cloud
validation remains pending.

## Storage

Both additive tables are registered in the primary database `AutoMigrate` path in
`model/main.go`. Neither table is stored in `LOG_DB`; no foreign key links them.

`management_operation_batches` stores one unique `operation_id` per create retry
identity, plus `operation_type`, `state`, `request_digest`, actor user id and role,
request id, creation timestamp, and `result_json`. `result_json` contains the
generated keys and optional claim-limit `creation_batch_id` so an authorized retry can
recover after a lost response. The invitation/redemption rows remain the business
records, so the keys have more than one durable representation. The JSON copy is
only returned through actor-, type-, and request-bound replay/export paths and is
never written to audit payloads or logs.

`audit_event_outbox` stores the stable event id; category/action; actor id, username,
role and auth method; non-bearer token reference; IP, user agent, method, route,
status, success, request id, event timestamp, content and bounded structured params;
attempt count, next-attempt timestamp, lease token/expiry, delivered timestamp, and
a short safe error category. Raw request/response bodies, created code values,
channel credentials, passwords, verification codes, and usable PAT bearer strings
are excluded.

## Code-create retry contract

`POST /api/invitation/` and `POST /api/redemption/` accept an optional
`operation_id` query parameter. Both return `operation_id`, `replayed`, and
`batch_id` additively alongside the existing response fields. When the caller omits
the id, the server generates one and returns it, but the caller had no prior
identity with which to promise retry convergence.

`CreateCodeBatch` commits the code rows, operation identity, result and audit
outbox event in one primary-database transaction. A retry with the same operation
id, actor, operation type, and normalized request digest returns the original keys
and marks `replayed=true`; it does not create another batch or audit event. An
actor, operation type, or request mismatch is rejected with the same opaque HTTP
409 response. An invalid fresh expiration is checked only while building a new
batch, so an otherwise identical committed retry still recovers after its
expiration time passes. The existing redemption `batch_id` retains its claim-limit
meaning and is not used as the retry identity.

Batch history returns summaries only; it does not expose keys. A historical export
requires the creating actor and matching code type. Exports accept explicit ids,
one operation id, or current keyword/status filters across all pages. Explicit
empty/malformed id selections fail instead of silently broadening to a filtered
export. Results are bounded to 5,000 rows; operation history pages are bounded to
100 rows.

## Audit transaction and delivery behavior

Code creates and channel create/update mutations that already own a primary
transaction insert their audit row into `audit_event_outbox` within that same
transaction. Failure to enqueue rolls the business mutation back for those paths.
Channel deletion and other management operations that do not share a transaction
enqueue after the business commit. Those paths keep a post-commit crash window; they
must not be described as transactionally atomic with their audit row. If an
out-of-transaction enqueue fails, the handler attempts a bounded direct write to
the configured log sink using the same event identity and full safe action metadata.
If both primary outbox enqueue and the direct sink fallback fail, the request path
reports the audit failure to server logs, while the already-committed business
mutation remains committed.

The background drain runs on every node. Per-row leases coordinate concurrent
workers. Failed deliveries remain in the primary outbox and retry indefinitely
with bounded exponential backoff (first retry 10 seconds, cap 600 seconds); there
is no terminal attempt ceiling or deletion policy. Delivery is at-least-once, not
exactly-once. Relational `audit_logs.event_id` uniqueness absorbs duplicate
delivery after a lost acknowledgement. ClickHouse has no corresponding unique key,
so a lost acknowledgement can produce a duplicate event there.

The request snapshot is a value object. It records actor id/name/role, auth method,
hashed token reference where applicable, IP, user agent, HTTP method/route/status,
and request id. Transactions and workers do not retain a Gin context or perform an
actor-name lookup during delivery.

## Regression and migration evidence required from GitHub Actions

The focused model selector is:

```text
^(TestManagementMutationOutboxDatabaseMatrix|TestConcurrentCodeBatchSubmitCollapsesToOneBatch|TestAuditOutboxReplayAfterLostAcknowledgement|TestAuditOutboxClickHouseDelivery|TestCodeOperationRecoveryRequiresAuthorization|TestCodeRequestDigestIsUnambiguous|TestCodeExportContractsAcrossDatabase|TestRecordManagementAuditFallsBackWithSameEventIdentity)$
```

The source matrix covers fresh and upgrade-shaped SQLite/MySQL/PostgreSQL fixtures,
running `AutoMigrate` twice, preserving existing redemption data/indexes, code
retry and uniqueness, and actor/type-bound historical export plus visible-list vs
export filter parity. The upgrade-shaped model fixture is assembled from released
field/tag definitions; it does not boot the released binary. CI still needs a
separate startup migration check against a database created by the released
baseline `53a71f88d5b568847cd7233a08f934824dcb22d3` (`custom-20261124-53a71f8`),
run twice per engine, with existing data/index/constraint preservation recorded.

`TestConcurrentCodeBatchSubmitCollapsesToOneBatch` exercises simultaneous submits
on MySQL and PostgreSQL. `TestAuditOutboxReplayAfterLostAcknowledgement` exercises
relational duplicate absorption. `TestAuditOutboxClickHouseDelivery` is an
additional gate when `TEST_CLICKHOUSE_DSN` is configured; it checks event identity
and delivery, not deduplication.

The hosted relational fixtures use `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN`;
SQLite uses temporary files. The optional ClickHouse sink test uses
`TEST_CLICKHOUSE_DSN`. Required engine versions, exact CI commands, and outcomes
must be recorded after cloud execution; source presence alone is not a database
compatibility claim.
