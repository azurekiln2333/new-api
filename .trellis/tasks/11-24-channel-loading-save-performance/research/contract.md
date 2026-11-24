# pi-keys interface contract (2026-11-24)

No local validation was executed. All checks run in GitHub Actions.

## 1. New endpoint: `GET /api/channel/options` (permission `authz.ChannelRead`)

Lightweight, admin-only dropdown projection. Bounded query: one `SELECT id, name, models`
(no key, no channel_info hydration, no per-channel allocation query).

Query params:

- `p` — 1-based page, default `1`
- `page_size` — default `30`, hard cap `100` (a larger value is clamped, never rejected)
- `keyword` — optional; narrows by `id`, `name` or `models` for a searchable dropdown
- `ids` — optional comma-separated channel ids to hydrate in addition to the page,
  so a currently-selected channel outside the page window still resolves.
  Deduplicated and **capped at 100 ids**; extra ids are ignored. Combined with the
  capped page, the response is bounded at page + 100 rows.

Response (`common.ApiSuccess` data):

```json
{
  "items": [
    {"id": 12, "name": "openai-main", "models": ["gpt-4o", "gpt-4o-mini"]}
  ],
  "page": 1,
  "page_size": 30,
  "total": 57,
  "has_more": true
}
```

- `id`: int channel id
- `name`: channel name
- `models`: array of the channel's configured model names, split on `,`; may be `[]`
- `total`: number of channels matching `keyword` (ignores `p`/`ids`)
- `has_more`: true when more matching rows exist after this page
- Ordered by `id desc` (stable for the fingerprint dropdown).
- `items` is the page plus any `ids=` rows the page did not already contain (appended).

The endpoint is bounded by design: it never returns every channel in one response.
For a searchable dropdown, pass `keyword` and page with `p` while `has_more` is true,
and pass the current selection via `ids=`.

Handler: `controller.GetChannelOptions`.
Route: `router/channel-router.go` `channelPermissionRoutes`
`{GET, "/options", authz.ChannelRead, controller.GetChannelOptions}`.

pi-ui: replace `/api/channel/search?...&page_size=30` dropdown population with
`GET /api/channel/options`; keep existing channel selection semantics and
`/api/channel/:id/fingerprint` calls unchanged. No new locale keys required.

## 2. `GET /api/channel/search` — now SQL-paginated

Same request params (`keyword`, `group`, `model`, `status`, `type`, `sort_by`,
`sort_order`, `id_sort`, `p`, `page_size`, `tag_mode`). Response shape unchanged:
`{items, total, type_counts}` with `items` already sliced to the requested page.

Behavior change: `total` is now the SQL `COUNT(*)` of the filtered set (previously
the pre-pagination in-memory length) and `type_counts` is computed by SQL `GROUP BY type`
over the same filter, **respecting the requested `status` filter**. `tag_mode=true`
stays in-memory/tag-based: `total` and `type_counts` describe the expanded tag set
after status filtering (before type narrowing).

## 3. Batched allocation projection (`model`)

`func ProjectChannelKeyEntries(tx *gorm.DB, ctx context.Context, channels []*Channel, omitKey bool) error`
— loads all active `channel_key_entries` for the given channels in bounded chunks
(`channel_key_entry_projection_chunk = 200` per `IN (...)` query) and applies the same
projection as `AfterFind`. `AfterFind` defers to it when the query context carries
`WithBatchedChannelKeyEntries(ctx)`, so a page/cache reload is a small constant number
of queries per chunk, not one per channel.

Eliminates per-channel reads in `InitChannelCache` and `GetAllChannels`/`SearchChannels`.

## 4. Cancellation

Every new/changed DB read derives from `c.Request.Context()` (or the request-bound
tx), so a cancelled client request cancels the query.

## 5. Cache publication (R2 channel part)

Channel write path publishes a complete local route/binding snapshot
(`InitChannelCache`) before responding, and peers are notified through the existing
`BumpSyncTopic(SyncTopicChannels)` background sync. `CacheUpdateChannel` alone is
never used to replace the full routing table.
