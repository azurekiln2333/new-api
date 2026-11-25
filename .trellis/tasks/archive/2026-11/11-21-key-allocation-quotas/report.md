# pi-keys child report — Key allocations, quotas and dedup (11-21-key-allocation-quotas)

## Verified final behaviour (test set, all three engines)

Test set: `TestChannelAllocationsDedupSkipsRunningTasksWithoutAllocationId | TestChannelAllocationsRoutingAliasScope | TestMultiKeyThreeRowModelScopeEditUsesFreshRevisions`
Command: `GOWORK=off <go> test -count=1 -v -run '<set>' ./controller/`

| Engine | Result | Log |
| --- | --- | --- |
| SQLite 3.50.4 (`TEST_CHANNEL_SQL_DSN` unset) | PASS `ok 2.574s` exit 0 | `D:/AppData/Local/Temp/new-api-pi-20261121/keys-sqlite.log` |
| MySQL 8.4.13 (`root@tcp(127.0.0.1:23306)/newapi_keys?charset=utf8mb4&parseTime=True&loc=Local`) | PASS `ok 0.740s` exit 0 | `…/keys-mysql.log` |
| PostgreSQL 16.16 (`postgres://postgres@127.0.0.1:25432/newapi_keys?sslmode=disable`) | PASS `ok 2.310s` exit 0 | `…/keys-postgres.log` |

Behaviour pinned:
- Allocation liveness: a reservation pending at submission, a task carrying `PrivateData.AllocationId`, and a legacy task (`AllocationId == 0`) with the identical credential all keep the allocation out of dedup until terminal; the legacy case blocks every live duplicate of that text.
- Dedup via API: preview then apply, merge into the smallest id, `PeriodUsedQuota` conserved (4), appended credential kept as its own row, only a terminal task releases the ambiguity.
- Routing scope: the aliased request (`qwen3-max@thinking:on@temperature:0.2`) is admitted when the channel lists the normalized name (via `RoutingMatchModelName`) and when it lists the alias; the Key allowlist stores the normalized name and admits that same aliased request; unlisted models are refused; an unrestricted Key serves every channel model the group allows.
- `set_key_models` on a three-row channel: rev 1 from `get_key_status` succeeds and moves only that row; a replay with the stale rev returns 409 and writes nothing; the fresh rev applies the empty explicit allowlist (that key serves nothing, siblings unaffected).

## Fixture repair (controller/channel_multi_key_test.go and one product defect)

Root log `root-final-main-packages.log` had three failing tests; all now pass on all three engines with assertions unchanged:
- `TestMultiKeyEnableRestoresOnlyExhaustedChannels`: uses `setupChannelCreateTest` (activation `Option` row + full `AutoMigrate` + root user); the bare `InitDB` that hit `no such table: options` is gone. Each management request now reads `model.GetChannelById(channel.Id, true)` immediately before it and sends that stored `ChannelInfo.KeyEntryRevision` as `ExpectedRevision` (no cache speculation); the `KeyIndex` legacy path is unchanged.
- `TestMultiKeyKeyScopeSelection`: runs on the `setupChannelCreateTest` schema with `CHANNEL_BUDGET_ENABLED=false` — pure selection, no allocation rows.
- `TestDistributeRoutesBoundKeyAndFallsBackWhenNoKeyServes`: same schema, `MemoryCacheEnabled=true` kept locally; each of the three fixed fixture channels is deleted before `Create`, then `AddAbilities`, with `t.Cleanup(channel.Delete)`.

Product defect fixed (`controller/channel_key_allocations.go`): the allocation action handler updated entry statuses without reproducing the legacy channel transitions, so `enable_key`/`enable_all_keys` never restored an exhausted channel. New `applyManagedChannelStatus` runs inside the same locked `MutateChannelKeyEntries` callback for `enable_key`, `disable_key`, `enable_all_keys` and `disable_all_keys`. It mirrors `disableMultiKeyChannelIfUnavailable`: when no enabled entry remains it returns without touching status or reason unless the channel was enabled, and only that enabled -> `manually_disabled` transition writes `status_reason = All keys are disabled` plus `status_time`; enabling restores only when the stored reason is exactly that value and the status is manual or automatic. A manual or unrelated automatic disable therefore keeps its status and reason. No model file changed.

Final focused matrix, six tests per engine, `-count=1`: `TestMultiKeyEnableRestoresOnlyExhaustedChannels`, `TestMultiKeyKeyScopeSelection`, `TestDistributeRoutesBoundKeyAndFallsBackWhenNoKeyServes`, `TestChannelAllocationsDedupSkipsRunningTasksWithoutAllocationId`, `TestChannelAllocationsRoutingAliasScope`, `TestMultiKeyThreeRowModelScopeEditUsesFreshRevisions`.

| Engine | Result | Log |
| --- | --- | --- |
| SQLite 3.50.4 | all six PASS, `ok 11.537s` exit 0 | `…/six-sqlite.log` |
| MySQL 8.4.13 | all six PASS, `ok 8.950s` exit 0 | `…/six-mysql.log` |
| PostgreSQL 16.16 | all six PASS, `ok 6.972s` exit 0 | `…/six-postgres.log` |

`go vet ./controller/` exit 0 and `gofmt -l` clean on both owned files before and after the runs.

## Limits (unverified or out of scope)

- No fix attempted outside the two files above; `TestMultiKeyEnableRestoresOnlyExhaustedChannels` passed on the first run after the revision fetch and the channel-status helper, so no further test adaptation was needed.
- ClickHouse was never exercised (not available here; not a pass).
- Dedup/scope behaviour was verified only through the fixtures above; no cross-node (multiple process) reconciliation was run.
- Read-only review findings RB-1..RB-3 and the legacy limitations are recorded in `research/release-review.md`; they are reported, not fixed by this child (RB-1 remains a model-layer merge path owned by another scope).
