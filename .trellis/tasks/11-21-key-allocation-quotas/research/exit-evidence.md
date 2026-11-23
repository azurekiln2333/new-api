# pi-keys final results (2026-11-23, narrow test-only assignment)

## Command and results (executed, unfiltered logs)

Test set: `TestChannelAllocationsDedupSkipsRunningTasksWithoutAllocationId|TestChannelAllocationsRoutingAliasScope|TestMultiKeyThreeRowModelScopeEditUsesFreshRevisions`
Runner: `D:/AppData/Local/Temp/new-api-pi-20261121/tools/go/bin/go.exe test -count=1 -v -run <set> ./controller/` with `GOWORK=off`.

| Engine | DSN used | Result | Log |
| --- | --- | --- | --- |
| SQLite 3.50.4 | (unset) | `ok … 2.574s` exit 0 | `D:/AppData/Local/Temp/new-api-pi-20261121/keys-sqlite.log` |
| MySQL 8.4.13 | `root@tcp(127.0.0.1:23306)/newapi_keys?charset=utf8mb4&parseTime=True&loc=Local` | `ok … 0.740s` exit 0 | `…/keys-mysql.log` |
| PostgreSQL 16.16 | `postgres://postgres@127.0.0.1:25432/newapi_keys?sslmode=disable` | `ok … 2.310s` exit 0 | `…/keys-postgres.log` |

All three tests and both alias subtests PASS on every engine; `go vet ./controller/` exit 0 before the runs. No product change was needed, so no expectation was adjusted.

## Read-only review of my production scope

`model/channel_budget.go` — `budgetLookupAllocation`
- Requires channel, allocation, revision and user; rejects an empty model name (previously an empty model would fall through the scope switches).
- Locks the allocation row with `lockForUpdate` and checks channel ownership, retirement, enabled status, exact `ConfigRevision`, and bound-user ownership.
- Channel, Key-allowlist and group-whitelist matching now share `budgetScopeServesModel` (exact name, or the name's `RoutingMatchModelName` alias). An explicit empty allowlist still returns false, i.e. denies every model; `scope_mode` defaults of `all`/`""` stay unrestricted.
- Bound keys skip the public group check (their exclusivity is the check), consistent with the existing routing rule; unbound keys must be in the channel group and inside that group's whitelist.
- Observations: the group whitelist is applied only for public keys, matching `ModelsAvailableToUserGroups`; a group whitelist entry that is itself the alias form is accepted because both sides go through the same helper. No unreserve/limit semantics touched.

`model/channel_budget_store.go` — `HasLiveChannelBudgetReservationsTx`
- Live reservation detection unchanged (pending/open phases, or reconcile_pending).
- Linked live task detection by `task_id IN (reservations.task_id)` unchanged.
- New: an in-progress task whose `PrivateData.AllocationId` equals the allocation blocks dedup even when the ledger settled the reservation at submission.
- New: a legacy task (`AllocationId == 0`) whose stored `Key` equals the allocation credential blocks **every** live allocation with that identical text — deliberately conservative, no later-ID heuristic, no test-only export, and DB errors are returned rather than swallowed.
- Batching (100 rows, `id > lastID`) is dialect-neutral; the query uses GORM only.

Residual risks I did not resolve (out of my change scope, reported not fixed): the legacy-key path compares raw credential text, so a rotated credential makes the block disappear; and the liveness probe stays O(live tasks) per allocation inside the dedup preview.
