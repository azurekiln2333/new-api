# Channel budgets and task accounting — integrated handoff

This report replaces the earlier worker checkpoint. The parent task owns final
acceptance and release. No cloud pass for this working tree is claimed here.

## Behavior

- Manual channel budgets are independent of upstream balance queries. Channel
  and stable key-allocation accounts reserve together before dispatch, record
  actual usage, preserve in-flight holds when limits change or usage resets,
  and retain historical accounting when allocations merge or retire.
- Wallet/subscription and API-token charges still apply. Accepted task costs
  above the estimate are recorded, including subscription overage.
- One primary-database transaction persists the task, its diagnostics link,
  funding/token delta and desired ledger settlement. The ledger move is
  separately replayable and never repeats funding or cache publication.
- A lost commit acknowledgement is resolved with bounded, owner/channel-scoped
  reads and full allocation/reservation identity checks. A proven commit follows
  the ordinary success path once. A proven rollback refunds and releases the
  hold. An unreadable outcome preserves funding and the hold for reconciliation.
- Cache publication occurs in the controller after a confirmed commit;
  `FinalizeTaskBudgetSubmission` is ledger-only. SQL and Redis do not have atomic
  delivery: a process failure or unreadable outcome can leave a cache projection
  stale. The implementation does not promise exactly-once cross-store delivery
  and does not evict quota caches that may have pending deltas.
- Token cache publication uses the credential read from the token actually
  charged. Deleting the token suppresses its cache delta; rotation targets the
  current credential.
- Image-preparation keepalive is restored before token counting downloads media.

## Rollout

`CHANNEL_BUDGET_ENABLED` defaults to false. Upgrade and drain every old node and
pending batch before activation. Active accounting requires
`BATCH_UPDATE_ENABLED=false`; both startup configuration and runtime admission
must enforce this. A durable activation marker cannot be bypassed by an old
process flag. Continuous `/v1/realtime` returns a local 503 before upgrade while
accounting is active; HTTP and Responses WebSocket remain supported. This is a
bounded transport restriction, not a repair of legacy Realtime accounting.

## Verification

Go 1.26.10, `GOWORK=off`. Real engines: SQLite 3.50.4, MySQL 8.4.13,
PostgreSQL 16.16. External task tests use `TEST_TASK_DB_DIALECT` together with
`TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`, on isolated test databases.

```text
go test -p 1 -count=1 -v ./controller -run '^(TestLedgerTaskSubmission.*|TestLegacyTaskSubmission.*|TestTaskSubmissionCommitsLedgerChargeAtomically|TestExecuteTaskSubmission.*|TestImmediateTaskSettlementDatabase)$'
go test -p 1 -count=1 -v ./model -run '^TestChannelBudgetDatabaseMatrix$'
```

Latest task selector: SQLite 0.308 s (ledger/legacy/atomic subset), MySQL
16.440 s and PostgreSQL 9.097 s (full selector above), all PASS.
Logs: `root-task-final-{sqlite,mysql,postgres}.log` in the task runtime directory.
The model matrix covers activation, batch refusal, single-connection transaction
behavior, rollback visibility, reservation/settlement/reset/dedup and durable
reconciliation. Startup environment-order regression is tracked by the parent.

Task regressions exercise an actual successful SQL commit followed by a returned
error, actual rollback, unreadable outcome, same-public-ID collisions, stale
allocation identity, subscription overage, deleted/rotated tokens, replay and
explicit recorded/disabled/failed request-content states. Recorded content is
read from its real payload and coordination rows; failure drops only the test
payload table and still records exactly one task charge.

Released `de5c967` databases were upgraded through actual initialization twice
on each engine; preserved data/indices are recorded in the parent's upgrade
evidence. Final package/race results and exact release SHA belong in the parent
report. ClickHouse is a mandatory hosted diagnostics gate, unavailable locally.

Final root task race selector (ledger/legacy/atomic submission and image-prep
keepalive) passed on SQLite in 6.868 s with no DATA RACE:
`go test -race -p 1 -count=1 -v ./controller -run '^(TestLedgerTaskSubmission.*|TestLegacyTaskSubmission.*|TestTaskSubmissionCommitsLedgerChargeAtomically|TestRelayKeepAliveDuringImagePreparation)$'`.
Whole root-module `go vet ./...` and `go build ./...` also passed. The final
startup env=true/runtime=false activation regression passes all three engines;
its verbose model matrix finished in 8.216 s. No task schema changed in this
last review.
