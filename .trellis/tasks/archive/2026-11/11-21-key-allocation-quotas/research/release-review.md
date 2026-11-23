# pi-keys read-only release review: duplicate identity / dedup / user binding / model permission

Scope: `model/channel_key_entry.go`, `model/channel_budget.go`, `model/channel_budget_store.go`, `controller/channel_key_allocations.go`, plus the readers in `model/channel.go` and `model/channel_satisfy.go`. No code, tests, DB or commits were touched.

## Release-blocking defects

**RB-1 · Binding/scope edits on a new or fresh-scanned row are silently dropped and contradicted by the response.**
`UpdateChannelLocked` (`model/channel.go` ~947-964) applies `MultiKeyUsers` / `MultiKeyModels` from the request to the **stored** entry set via `syncKeyEntryAttributesFromProjection(entries, &channel)` and then `persistKeyEntries(..., entries, nil)`, which rebuilds `MultiKeyUsers`/`MultiKeyModels` from the persisted entries. `slice` lookups outside the old range simply write nothing, and `persistKeyEntries` → `applyKeyEntryProjection` resets the projection, so the stale index is discarded. API behaviour today: `PUT /api/channel` carrying `multi_key_users`/`multi_key_models`, or `{"key":"", ...}` (which also skips the `ErrChannelKeyChanged` guard), answers 200 while the binding/scope is not stored. This is exactly the mutation shape a client needs after an append (new row beyond the previously stored index), i.e. it directly blocks attributing an appended duplicate. Status: **blocking** for release, because the response is a false success on a permission-relevant field; fix is in the model merge path (owned elsewhere) or the API must reject indices outside the stored range.

**RB-2 · Every 409 from Key management carries one opaque code.**
`channelKeyManageError` (`controller/channel_key_allocations.go` ~170) maps `ErrChannelKeyEntryRevisionStale`, `ErrChannelKeyEntriesAmbiguous`, `ErrBudgetStaleVersion` and `ErrBudgetAttemptConflict` to `channel_key_revision_conflict`. A stale `expected_rev`, an ambiguous duplicate credential, and a lost budget race are therefore indistinguishable to the console. This already cost a full debugging cycle during this task. Classification: **release-blocking only if the console must distinguish them**; otherwise it is a documented limitation, but dedup (which can return `ErrBudgetAttemptConflict` from `MergeChannelBudgetAccountsTx`) then reports "revision conflict" during normal contention.

**RB-3 · Dedup silently produces nothing when every member of a group is busy.**
`channelKeyDedupPreview` adds a group to the plan whenever `len(group) >= 2`; when all members are skipped, `EntryIDs` is empty, `SurvivorID` is `""`, and such a group stays in `plan.Groups` with `applied=true`, `merged_groups=0`. A client that renders "N groups found, 0 merged" cannot tell "nothing to do" from "everything was busy". Also `preview.SurvivorID = preview.EntryIDs[0]` picks the smallest ID of the **unskipped** set while the group's merged totals (`MergedPeriodUsed`/`MergedLifetimeUsed`) are accumulated over the unskipped entries only — the response therefore advertises a merge that will never include the skipped rows and will not repeat later without a new preview. Status: **blocking for the console contract**, not for data integrity.

## New-defect check (behaviour introduced by this task's production edits)

- `budgetLookupAllocation`: rejects an empty `ModelName`, an unknown/foreign/retired/stale allocation, a bound-to-another-user allocation, a model outside the channel list, a `scope_mode` outside `all`/`allowlist`, and (public keys only) a model outside the group whitelist. No bypass found: bound keys skip the public group check exactly as `ModelsAvailableToUserGroups` does (`owner == userID` path), and the exclusion is the binding itself.
- Channel, Key allowlist and group whitelist share `budgetScopeServesModel` (exact name, else `RoutingMatchModelName` alias). Because both the request name and the stored entries pass through the same function, no scope accepts a model the other two reject in the alias direction. Verified against `KeyServesModel`, `ModelsAvailableToUserGroups`, `GetNextEnabledKeyFor` and `ChannelSatisfy`: key selection still filters by `MultiKeyUsers`/`KeyServesModel`/budget before the ledger re-validates, so the ledger is a second, not the only, gate.
- `HasLiveChannelBudgetReservationsTx`: pending/open reservations, linked live tasks, the id-carrying in-progress task and the legacy identical-credential rule are all fail-closed; errors are returned, never swallowed. No new false-merge path found.

## Legacy limitations (pre-existing, not introduced here, recommended as documented limits)

1. Legacy text-only callers (`KeyIndex` path) still cannot disambiguate duplicates: `channelKeyRequestIndex` refuses the whole request with `ErrChannelKeyEntriesAmbiguous` → generic 409. Correct, but the console must use `KeyEntryID` + `ExpectedRev`.
2. `planKeyEdit` refuses reorder/removal of duplicate text (`hasPrefixKeys` accepts appends only), so a duplicate can be removed only through dedup or `delete_key` by ID.
3. Empty middle lines are kept for index compatibility and force `manually_disabled` in `persistKeyEntries`; they still occupy a position and a budget view row (`ReadChannelBudgetViews` fabricates a default view for ids with no account row).
4. Legacy-key liveness compares raw credential text (`p.Key == entry.Key`); a rotated credential removes the block, and a plugin credential rotated outside the row keeps it.
5. Budget checks are channel-scope fan-out: if `ReadChannelBudget(ctx, BudgetScopeChannel, id)` fails, `ChannelBudgetAllows` errors and `UserBoundModels`/`HasAvailableAllocationBudget` degrade to "no models"/"unavailable" for all keys of the channel — fail-closed but channel-wide.
6. Dedup liveness is O(live tasks) per allocation inside the preview transaction, holding the channel lock.
7. `set_key_models` validates names against the channel's own `GetModels()` only, so a Key allowlist can never hold a name the channel does not serve; an allowlist written for one channel is not translated to another channel's mapping.

## Evidence commands (read-only, no execution)

- `planKeyEdit`, `persistKeyEntries`, `applyKeyEntryProjection`, `MutateChannelKeyEntries` — `model/channel_key_entry.go`.
- `GetNextEnabledKeyFor`, `KeyServesModel`, `UserBoundModels`, `PublicModels`, `UpdateChannelLocked` — `model/channel.go`.
- `ChannelSatisfy` bound vs public union — `model/channel_satisfy.go`.
- `channelKeyDedupPreview`, `handleChannelKeyAllocationAction`, `channelKeyRequestIndex`, `channelKeyManageError` — `controller/channel_key_allocations.go`.
- `MergeChannelBudgetAccountsTx` (epoch checks, live check, limit equality), `ChannelBudgetAllows`, `ReadChannelBudgetViews` — `model/channel_budget_store.go`.
- `budgetLookupAllocation`, `budgetScopeServesModel` — `model/channel_budget.go`.
