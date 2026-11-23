# Request-log diagnostics integration and verification

Scope: approved authenticated relay snapshots, protected primary/log-store links,
private task provenance, actual processing node and administrator detail endpoint.
Capture occurs at the first reusable original-body read, before protocol/plugin
rewrites. No wallet/token/subscription/pricing changes. Billing/root own shared
runtime task and route integrations; pi-ui owns all frontend changes.

## Public contract

`GET /api/log/request/:ref`, existing admin/root auth plus PAT `log:read`.
Success `data` is
`{ref,request_id,submit_node_name,status,captured_bytes,truncated,payload}`;
`payload` is a JSON object. Unknown, malformed and unlinked refs return an empty
404. `DisableCache` runs before auth; success and denial responses are `no-store`.

Only `other.admin_info` holds `node_name` (processor), `submit_node_name` (ingress)
and `request_content:{ref?,status,captured_bytes,truncated}`. Ordinary log rows,
consume parameters, list/export reads and process logs must never contain the
captured payload. Status vocabulary remains `recorded`, `truncated`, `no_content`,
`unsupported`, `invalid`, `unavailable`, `disabled`, `storage_failed`.

## Safety boundaries

- Main coordination uses holder/generation fencing and renewable ingress writer
  leases. Detail bytes reside only in actual `LOG_DB`.
- Same-store detail/log/task insertion uses one transaction. Configured SQLite
  `local` aliases reuse the main handle when the main store is SQLite; actual
  configured startup is covered, not merely pointer-identity tests.
- Independent writes persist `write_pending`, then `link_pending` before the
  remote log callback. Recovery needs its exact `link_id`; an earlier retry's
  shared ref is not sufficient proof. Lease expiry is never proof of rollback.
- Unknown link outcomes without exact evidence stay fenced. Unknown deletes are
  recovered to retired refs, never reused; late bytes are swept. This deliberate
  conservative boundary is not a claim that every uncertain case auto-resolves.
- A diagnostic failure before accepted-task insertion falls back exactly once
  with persisted `storage_failed` provenance. An attempted task insertion/commit
  error is returned without a second potentially duplicate insert.
- Task liveness checks run through primary DB, including terminal `BudgetPending`
  and unresolved refunds. Log-store ledgers are never read for task completion.
- Final `ErrOptionWithNoRecordErrorLog` must set the exclusion marker in ordinary
  Relay and the Responses runner before middleware fallback. Both integrations
  are present and the no-record regression passes.

## Verification handoff

`report.md` records actual commands/results, matrix versions, fixture repairs,
race evidence, skips and residual integration failure. The expanded matrix runs
fresh and unchanged released-log upgrades twice for shared/independent SQLite,
MySQL and PostgreSQL; the released `Log` schema/tags were compared to
`custom-20261118-9b21c12`. It is not a whole-product release-database migration.
External migration cases create/drop only their own random disposable databases.

CI selector remains:

```text
go test -p 1 -count=1 -v ./model -run '^TestLogRequestDetailStorageDatabaseMatrix$'
go test -race -p 1 -count=1 ./model -run '^TestLogRequestDetail'
```

Use actual `TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, `TEST_CLICKHOUSE_DSN`. Missing
DSNs explicitly skip; configured connection errors fail. ClickHouse's primary
store is separate SQLite. Local relational/race checks passed; ClickHouse is
unavailable locally and remains a required external gate, including native TTL,
duplicate fault writes and mutation recovery. A CI service declaration alone is
not a passing result.
