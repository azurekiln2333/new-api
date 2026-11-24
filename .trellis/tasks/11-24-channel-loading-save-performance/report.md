# pi-keys report — 11-24-channel-loading-save-performance (FROZEN, source-only)

Validation banned locally: nothing here was built, tested, vetted, linted, typechecked, formatted or run.
Cloud GitHub Actions is the only validator. Nothing is staged, committed or pushed.

## Exact changed files (this pass)

1. `model/channel_cache.go`
   - `InitChannelCache` no-cache branch delegates to the guarded helper (was publishing the bound index with no generation capture/check).
   - `refreshUserBoundChannelIndex()` now captures `ChannelCacheGeneration()` before reading and, under `channelSyncLock`, refuses to publish if the generation advanced.
   - `CacheUpdateChannel` now publishes the bound index via `publishUserBoundChannelIndexForChannel` **while still holding** `channelSyncLock` (previously it released the lock and then rebuilt the whole index, which let an older reload interleave).
   - Removed the now-dead `rebuildUserBoundChannelIndex` (duplicate of `buildUserBoundChannelIndex` that published unguarded).
   - Added `UserBoundChannelIDs(userId)` — exported read of the published index (used by the regression test, no DB read).
2. `model/channel_publication.go`
   - `loadChannelRoutingSnapshot` sets the batched marker on the session context, so `AfterFind` no longer runs its own per-row allocation read before the explicit projection (removes a redundant query per published row).
   - `UpdateChannelConfigAtomic` drops the unused pre-transaction `origin` read + projection (two queries removed from every save).
   - New `publishUserBoundChannelIndexForChannel(channel)` and `removeChannelFromUserBoundIndex(id)`: targeted in-place bound-index updates replacing full-map rebuilds.
   - `publishChannelSnapshot` / `publishChannelBatch` / `CacheRemoveChannel` all use the targeted update; the no-cache branch publishes under `channelSyncLock` + generation bump instead of doing a full-table rescan.
   - **All three of those functions call `InvalidatePricingCache()` and `rebuildTaskAliasView()` on the `MemoryCacheEnabled == false` path too**, always *after* releasing `channelSyncLock`. `CacheRemoveChannel` calls them unconditionally (both modes need them, so there is no mode branch there). This preserves the old `InitChannelCache` no-cache behaviour, which explicitly did both. They must stay outside the lock: `InvalidatePricingCache` takes `updatePricingLock`, which nests `channelSyncLock.RLock`, so holding `channelSyncLock` would be an AB-BA deadlock.
3. `model/channel.go`
   - New `ChannelTagSearchSubquery(ctx, keyword, group, model)` — expresses the tag-search match set as a subquery with the same conditions `searchTags` uses.
   - New `ChannelTagSearchQuery(ctx, keyword, group, model, statusFilter, typeFilter)` — the tag-mode listing query (`tag IN (subquery)`, plus group/status/type filters). Uses a bare `IN (?)` so the SQL is identical on SQLite/MySQL/PostgreSQL.
4. `controller/channel.go`
   - `SearchChannels` tag mode rewritten: `total`/`type_counts`/page now all come from SQL over the tag subquery, instead of expanding every matching tag into channel rows in memory. Contract preserved (total and `type_counts` after status, before type narrowing).
   - Non-tag and tag branches share the offset clamp (`math.MaxInt32`) and the same `ListChannelsPage`.
   - `SearchChannels` `page_size` is capped at 100 (`maxChannelPageSize`, line ~582) **before** the offset math, matching `/options` and `common.GetPageQuery`'s existing 100-row bound. Previously an arbitrary `page_size` reached `ListChannelsPage`. Order is: parse → default when <= 0 → cap at 100 → offset clamp → `startIdx`.
   - `GetAllChannels` tag mode: tag list still paged (groups never truncated), but the per-tag reads coalesced into one `tag IN (?)` query and regrouped in tag-page order. (`GetAllChannels` was already bounded by `common.GetPageQuery`, which caps at 100.)
5. `controller/channel_listing_test.go`
   - Added `TestChannelSearchTagModePagesInSQL`.
   - Added `TestChannelSearchPageSizeIsCapped` (140 matching rows, `page_size=100000` ⇒ 100 items, and asserts the query itself did not read all matching rows).
6. `controller/channel_publication_test.go`
   - Added `TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot` (barrier-driven; asserts a positive control before the interleave, then that the removed binding is not resurrected).
   - Removed the unused `strconv` import.

## Contract notes

- `/api/channel/search` response shape unchanged: `{items, total, type_counts}`.
- Tag mode still returns whole tag groups in `GetAllChannels`; no group is ever truncated.
- Lock order is one-way: `channelSyncLock` → `userBoundChannelsLock` on the write path; readers take `userBoundChannelsLock` alone and release it before calling `CacheGetChannel` (which takes `channelSyncLock.RLock`).
- `InvalidatePricingCache` is never called while holding `channelSyncLock` (AB-BA deadlock with `GetPricing`).

## Caveats (read before CI)

1. **`SearchTagsWithContext` / `searchTags` are now unused by request paths** (kept as exported/adminuth model API). If vet/staticcheck runs with `unused` on unexported symbols, `searchTags` is still referenced by the exported wrappers, so it compiles.
2. **Tag subquery dialect risk is the main thing for CI to settle.** The outer query uses `tag IN (?)` with a `*gorm.DB` subquery and reuses `commonKeyCol` / backtick-vs-double-quote column names in the subquery. Verified by reading only; needs the three-database matrix.
3. **`channel_info` JSON comparison in the new test** uses `model.ChannelInfo` implements `driver.Valuer`, so `Update("channel_info", …)` serializes on all three dialects — also unvalidated.
4. **`TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot` depends on `GetChannelById` honouring the memory-cache-off path** (`CacheGetChannel` → DB) and on the barrier callback firing on the `channels` table read. If GORM changes callback ordering this test is the first to notice.
5. **Batch create returns caller IDs** by inserting `&channels[i]` pointer chunks (a `slices.Chunk` value chunk silently dropped generated IDs — fixed earlier).
6. **Delete is intentionally unchanged**: `Channel.Delete()` + `recordManageAudit` (durable, post-commit). Full atomic delete with allocation retirement in one tx is out of scope for this pass.
7. `publishUserBoundChannelIndex` remains used by the two full-reload branches only.
8. **`page_size` cap is 100 for both `/api/channel/search` and `/api/channel/options`**, and `GetAllChannels` inherits the same 100 from `common.GetPageQuery`. A client that previously requested more than 100 rows from search will now get 100 plus a truthful `total`, so it must page.

## CI selectors (already wired by root)

`TestChannelSearch|TestGetChannelOptions|TestChannelCreate|TestChannelUpdate|TestChannelCacheReload|TestChannelBoundIndex`

New names this pass: `TestChannelSearchTagModePagesInSQL`, `TestChannelSearchPageSizeIsCapped`, `TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot`.
