# Channel coordination and publication

## 1. Scope / trigger

Apply when changing Redis channel admission, queue retries, or cross-node channel
cache publication. A lost response may follow a committed Redis mutation.

## 2. Signatures

- `model.ReloadChannelCache(ctx context.Context) error` publishes a complete
  routing snapshot or the bound-user index on nodes without routing cache.
- The channel sync callback returns that error before a master scans plugins.
- Admission and queue operations use the existing bounded admission timeout.

## 3. Contracts

Registration replay with the same ticket preserves its original deadline and
FIFO position. Grant replay uses a short-lived owner-bound receipt, returning a
grant only while its original slot and optional probe remain owned and live.
Replay never renews a lease or resurrects an expired grant. Cancellation and
ambiguous-result cleanup remove only that attempt's owner and receipt.

A failed database load, allocation projection, ability load, or stale generation
leaves the published snapshot intact and returns an error. Sync does not advance
its applied revision until publication succeeds; recovery retries the same
revision. Cache-disabled followers still refresh user bindings. Only masters
scan scheduled plugins after successful publication.

## 4. Validation and error matrix

| Condition | Required behavior |
| --- | --- |
| Redis executes a mutation but reply is lost | Same-owner replay stays idempotent |
| Receipt's slot or probe has expired | Do not reconstruct or extend the grant |
| Registration repeats | Preserve queue order and original deadline |
| Channel load fails or is superseded | Keep old snapshot and unapplied revision |
| Failure recovers without another topic bump | Publish, then acknowledge pending revision |

## 5. Good / base / bad cases

Good: a socket EOF after a granted Lua script replays the same live reservation.
Base: an ordinary request acquires and releases its own slot.
Bad: replaying registration extends a deadline, or a failed cache reload reports
success and permanently skips the pending channel update.

## 6. Tests required

Use real Redis for `TestChannelCapacityRedis`,
`TestChannelCapacityCoordinationDiagnostics` and `TestChannelWaitRedis`, including
the socket EOF fixture and owner-specific cleanup. Run the existing admission and
waiting race selector. Run
`TestNodeSyncChannelReloadFailureKeepsRevisionUntilPublication` with
`TEST_TASK_DB_DIALECT=sqlite|mysql|postgres`; each top-level test must pass without
skips. Retain existing three-database cache/bound-index stale snapshot tests.

## 7. Wrong vs correct

Wrong: disabling Redis errors or claiming a cache attempt proves synchronization.
Correct: keep admission fail-closed, classify safe diagnostic stages, preserve
owner identity across retries, and acknowledge only completed publication.
