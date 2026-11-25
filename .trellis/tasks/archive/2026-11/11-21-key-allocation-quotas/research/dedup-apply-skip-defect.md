# Defect: dedup apply merges allocations skipped by the preview (pi-keys, not owned file)

Observed while running `TestChannelAllocationsDedupSkipsRunningTasksWithoutAllocationId`
(SQLite, real controller API `POST /api/channel/multi_key/manage`, action `deduplicate_keys`).

## Reproduction evidence

Three-step sequence on a channel with 2 identical credentials, one settled reservation
whose async task is still running (legacy `PrivateData.AllocationId == 0`):

1. preview -> `groups[0] = {survivor_id:"1", entry_ids:["1"], skipped:[{id:"2", reason:"Key allocation has active requests or tasks"}]}` (correct).
2. apply that preview -> rows preserved (correct: `moved_into` only covers `entry_ids[1:]`, which is empty for one id).
3. append a third identical credential (`PUT /api/channel`, `key_mode:append`) -> next preview reports
   `revision:5`, `groups[0] = {survivor_id:"", entry_ids:[], skipped:[id:1, id:2]}`, i.e. the appended
   row is missing from the group but the list still has 3 rows after the HTTP 200 append.

## Suspected code

`controller/channel_key_allocations.go` (owned by pi-keys per dispatch) `channelKeyDedupPreview` +
apply loop:
- apply iterates `preview.Groups` and merges `group.EntryIDs[1:]` without subtracting
  `group.Skipped`, so a skipped id that the preview still lists would be retired.
- the group built for the appended row appears empty while `preview.SurvivorID`/`EntryIDs` are
  derived from `EntryIDs[0]`, so a group of only-skipped rows renders an empty entry list.

Either way the preview/apply pair can disagree with what the list shows. Confirming with a
targeted Debug log on the entries slice passed to the preview before changing anything.

Owner: whoever owns `controller/channel_key_allocations.go` in the current dispatch (pi-keys).
