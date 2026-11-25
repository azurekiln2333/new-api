# Channel save/list round trips — current working-tree source investigation

Date: 2026-11-25. Requested by root Claude (`w4C:p17`), parent `11-24-console-responsiveness-diagnostics`.

Read-only investigation: no production/test source was edited, no Git command, local test/build/lint/typecheck/formatter, benchmark or DB/Redis probe was executed for this investigation. This Markdown artifact is the only write. Counts below are source call counts on successful paths, **not measured latency**. The supplied HK→us01 160–320 ms RTT is deployment context, not a measurement made here. Driver protocol exchanges, connection establishment, retries, concurrent mutations and query execution time are not inferred from ORM call counts.

## A. Channel edit → save → spinner/close

### Save critical path

1. Form submits through `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx:5664`; `onSubmit` begins at `:1872`.
2. Local validation does not issue HTTP. Conditional human confirmations are awaited **before** saving: new risky status-code redirects at `:1946`, missing model mappings at `:1994`. These are dialog promises, not GETs.
3. `await channelMutation.mutateAsync(data)` at `:2014` → `web/src/features/channels/hooks/use-channel-mutate-form.ts:101` awaits `updateChannel(...)`.
4. `web/src/features/channels/api.ts:327-336` sends **one `PUT /api/channel/`**, body `{id,...data}`. `singleUseAuthorization` is conditional on `key_mode === 'append'` (`:334`). No separate model, key, allocation, test, or budget save is invoked by this submit path. Expected allocation revision is included in this PUT, not fetched on submit (`use-channel-mutate-form.ts:101-106`).
5. Mutation `onSuccess` (`use-channel-mutate-form.ts:124-127`) shows toast and calls `handleSuccess`. `channel-mutate-drawer.tsx:1736-1745` starts invalidation of `['channels','list']` and, for a channel id, `['channels','detail',id]`, **without awaiting or returning either promise**, then invokes `onOpenChange(false)` and `setOpen(null)`.
6. `isSubmitting = channelMutation.isPending || form.formState.isSubmitting` (`:1869`); submit button disables/spins at `:5702-5708`. The submit promise awaits the PUT/mutation callbacks, **not completion of those background GETs**. Failed PUT leaves the draft open (`:2022-2023`). There is no awaited list invalidation after successful edit in this current source.
7. Additional shared mutation callback: `web/src/lib/query-client.ts:50-54` non-awaited invalidation of `['node-sync']`. On this page its root-only active observer can issue `GET /api/system-info/sync`; this is also not part of the save wait.

### Background reads caused by success

- Active cached list/search queries under `channelsQueryKeys.lists()` refetch, not necessarily just one if another matching observer exists. In ordinary one-table view this is one list/search request.
- Active detail observers under the detail prefix can refetch while closure/unmount is being scheduled. The direct detail query is `GET /api/channel/:id` (`api.ts:283-285`; drawer `:659-664`). Plugin credential and multi-key-status query keys are descendants of the detail prefix (`lib/channel-actions.ts:66-67,79-80`), so **if active** they can also be invalidated. Disabled/unmounted queries do not imply an obligatory HTTP fetch.
- Budget, usage and concurrency keys are sibling prefixes (`lib/channel-actions.ts:61-78`), not list descendants; saving does **not** explicitly invalidate them here. A changed visible id set can trigger their new query keys after the refreshed list arrives; concurrency also has its own timer.
- There is no cache patch of the saved channel in this hook: it discards the update response's channel payload and returns only a message key (`use-channel-mutate-form.ts:109`). Thus visible list reconciliation still depends on that background list request, although closing does not.

### Opening the editor is a separate fan-out, not a submit sequence

The following independent queries can run concurrently on opening, subject to cache staleness/deduplication:

| HTTP | trigger/source |
| --- | --- |
| `GET /api/channel/default_base_urls` | open; drawer `:644-650`, API `:303-310`; five-minute staleTime |
| `GET /api/channel/:id` | open + editing + id; drawer `:659-664` |
| `GET /api/group/` | open and provider picker hidden; drawer `:667-671`, users/api.ts:210-212; same `['groups']` query as table |
| `GET /api/channel/models` | open and picker hidden; drawer `:674-678`, api.ts:1183-1189 |
| `GET /api/prefill_group?type=model` | same condition; drawer `:681-685`, api.ts:1241 |
| `GET /api/task_plugin_options` | open + bind permission; drawer `:902-907`, api.ts:206-210; shares task-plugin-options key with row consumers |
| `GET /api/channel/:id/plugin_credentials` | existing login-plugin panel mounted; panel `drawers/plugin-credential-panel.tsx:63-68`; not normal static-key channel |
| `GET /api/request_plugin/instances` and plugin-options query | bind permission + request section visited; `channel-request-plugins-field.tsx:51-60`, task-plugins/api.ts:213-225. `channel-configuration.tsx:99-103,176-184` retains visited panels. Two query keys may target `/api/task_plugin_options`; HTTP wrapper deduplicates only overlapping unsignalled GETs (`lib/http-client.ts:61-76`). |

Key disclosure is user-triggered: verification then `POST /api/channel/:id/key`, `hooks/use-channel-key-disclosure.ts:55-74`, api.ts:498-509; no automatic key disclosure on save. Upstream model discovery only changes local discovery state in its effect, and performs HTTP in explicit `fetch` (`hooks/use-channel-model-discovery.ts:47-67`), not on save. Codex credential refresh and plugin login are separate explicit actions. These must not be counted as unconditional save requests.

## B. Channel list page HTTP graph

### Cold successful page, no dialogs open

Independent initial observers (parallel; there is no await chain among them):

1. `GET /api/channel/ops`: `channels/index.tsx:46-51`, api.ts:294-300. Five-minute staleTime, retry false. It does not load models or rows.
2. `GET /api/group/`: `components/channels-table.tsx:256-259` → users/api.ts:210-212. `TagBatchEditDialog` is mounted even when closed (`channels-dialogs.tsx:118-123`) and also subscribes to `['groups']` without an open guard (`dialogs/tag-batch-edit-dialog.tsx:69-72`); same query key means this is **not a second independent groups request**.
3. Exactly one of:
   - `GET /api/channel` for ordinary page/filter/sort (`channels-table.tsx:272-345`, api.ts:248-253).
   - `GET /api/channel/search` if keyword or model filter is nonempty (`channels-table.tsx:231,295-322`, api.ts:259-264).
   Status/group/type filters alone use list. Type counts and tag information come in this response; no separate type-count or tags request is made by the table.
4. Root-only: `GET /api/system-info/sync`; `channels-primary-buttons.tsx:113` mounts `NodeSyncIndicator`; `system-info/hooks/use-node-sync.ts:36-56` enables only super-admin, polls every 3 s while updating / 30 s idle, refetches on focus. API path at `system-info/api.ts:33`.

After a nonempty list supplies ids, three independent query families start (parallel with each other, **dependent on list ids**):

5. `GET /api/channel/usage?ids=...`: `channels-table.tsx:365-372`; `hooks/use-channel-usage.ts:32-45`; api.ts:117-130. Deduplicated/sorted ids, one HTTP request; server truncates accepted ids at 200 (`controller/channel_usage.go:14-34`). **Unlike budget/concurrency, usage is not split into chunks by the frontend**; this is relevant for tag expansion >200 channels.
6. `GET /api/channel/quota?ids=...`: table `:374-391`; api.ts:964-993. Real ids exclude tag aggregate rows. `ceil(N/200)` requests launched **in parallel** via `Promise.all`, not per channel. Retry false.
7. `GET /api/channel/concurrency?ids=...`: table `:406-419` publishes real visible ids; provider `:140` owns one subscription; hook `use-channel-concurrency.ts:49-70`. `ceil(N/200)` chunks awaited **sequentially within this query**, every 5 s while visible; no per-row timers. API `:144-155`.

Conditional after rows render: any plugin channel badge/action that has a plugin key and viewer bind permission subscribes to the shared `['task-plugin-options']` query (`channel-type-badge.tsx:63-66`, `data-table-row-actions.tsx:90-91`, `channels-columns.tsx:136,360`, `hooks/use-task-plugin-option.ts:48-55`). This is one shared options query, **not one HTTP request per row**. Ordinary built-in channel rows don't enable it.

Default page sizes: desktop **20**, mobile **10** (`channels/constants.ts:396`; `channels-table.tsx:170`). With no reusable cache, nonempty ordinary built-in desktop page: **6 page-owned GETs for an admin, 7 for root** (ops + groups + list + usage + quota + concurrency + conditional root sync). Optional plugin options adds one. This excludes already-mounted application-shell requests, static assets, retries, auth refresh and user-triggered dialogs. Empty page has no id-dependent three-family reads.

No per-row HTTP N+1 was found on the ordinary list/card render path: token cells consume `ChannelUsageContext`, quota cells consume `ChannelBudgetContext`, concurrency cells consume provider data. Test/response timestamps are fields of the list response (`channels-columns.tsx:1301-1305`, card `:87`), not automatic `/test/:id` requests. There is no automatic per-row allocation/key endpoint: allocation compatibility state is in the list JSON. Detail, quota editor, key manager, inference, test, balance and tag-model dialogs are explicitly gated by open/action state (`channels-dialogs.tsx:40-130`; e.g. test keys `channel-test-dialog.tsx:466-474`, single-key manager budget `multi-key-manage-dialog.tsx:163-166`).

### Refresh and post-save differences

- Refresh button (`channels-table.tsx:773-775`) calls `refetch()`, `refetchUsage()`, `budgetQuery.refetch()` consecutively with `void`: these **three families overlap**, rather than await each other. It does not explicitly refetch groups, ops or concurrency; concurrency continues its timer. If returned list ids change, new id-dependent keys can additionally fetch after the list resolves.
- Save invalidates the list family and detail prefix as described in A, **not** all channels keys. No unconditional usage/quota fetch on same ids follows solely from list invalidation.
- `placeholderData` retains prior list data (`channels-table.tsx:348`); background query activity drives the refresh indicator but isn't initial `isLoading`. A returned list may be slow without keeping the successfully saved drawer open.
- Shared client defaults (`lib/query-client.ts:40-47`): focus refetch off, staleTime 10 s; production failures may retry under its callback. Node sync overrides focus behavior. Actual wire counts can therefore exceed successful-path counts on failure, mount/reconnect, or auth expiry. On HTTP 401 the interceptor can await auth refresh (`lib/http-client.ts:101-104`); this is conditional, not an intrinsic channel-list request.

## C. Backend list/search DB and Redis order

### Controller/model body: ordinary list

Routes: `router/channel-router.go:42,60-61`: AdminAuth + ChannelRead permission, GET `/` → `controller.GetAllChannels`, `/search` → `SearchChannels`.

For `GET /api/channel`, `tag_mode=false`, successful response, in strict source order:

1. Build filters in memory (`controller/channel.go:182-190`, group predicate `model/channel.go:214`; no DB execution).
2. `COUNT(*)` matching group/status/type: controller `:444` → `model/channel.go:1031-1038` (`Count` at **1036**).
3. Ordered page SELECT from `channels`, omitting `key`, LIMIT/OFFSET: model `:1042-1047` (`Find` at **1046**). Context includes `WithBatchedChannelKeyEntries`.
4. `Channel.AfterFind` runs on hydrated channel objects but returns immediately because of that context (`model/channel_key_entry.go:289-294`): **zero per-row SELECTs**. It does not run the single-channel `loadActiveChannelKeyEntries` at `:298` on this path.
5. `ProjectChannelKeyEntries` (`model/channel.go:1052`) selects all active `channel_key_entries` for migrated channels in chunks of **200 channel ids**, ordered `channel_id,position,id` (`model/channel_key_entry.go:23,32-62`). Only channels with nonzero `KeyEntryRevision` enter ids (`:36`); no migrated rows means no query. Chunk loop is sequential. It loads credential rows (including keys) for the in-memory compatibility projection, then clears `channel.Key` again for omitted-key readers (`:65-73`). `applyKeyEntryProjection` loops through entries/JSON (`:217-280`), **no account/quota SQL or Redis there**.
6. `clearChannelInfo` for each returned row (`controller/channel.go:454-456` → `:75-81`): balance detail parsed from already loaded `other_info`, multi-key disable details cleared. `model/channel_balance.go:24-37` is JSON processing only, no upstream call or DB query.
7. One `SELECT type, count(*) ... GROUP BY type` with same group/status, intentionally no selected-type narrowing: controller **460** → `model/channel.go:1062-1069`, `Find` at **1068**. Projection is into aggregate row structs, not Channel, so no allocation hook.
8. Serialize `items,total,page,page_size,type_counts` (`controller/channel.go:466-472`). **No Redis command occurs in this controller/model list body. No local-budget account query occurs here.**

For `GET /api/channel/search`, successful non-tag path: **type_counts aggregate first** (`controller/channel.go:617`), then matching count + page + batched entries (`:622`, same `ListChannelsPage`). Keyword/id/name/key/base_url/model/group filters are SQL predicates (`model/channel.go:1002-1025`), not additional queries.

### Exact body formula and default count

Let P = returned ordinary-page channel count; M ≤ P = channels with nonzero allocation revision; K = active keys per migrated channel (or use actual sum E). Define chunk(0)=0, otherwise chunk(M)=ceil(M/200).

- Ordinary list/search body SQL calls: **Q = 3 + chunk(M)**.
- DB rows hydrated: at most P channels + E allocation rows (uniform K ⇒ E=M×K), plus count/type-count scalar results. **K affects rows/CPU/payload, not the number of allocation SELECTs**. This is not `P×K` network requests.
- Default desktop P=20: **4 SQL calls** if at least one/all channels are migrated; **3** if M=0. At P=20 all migrated uniform K ⇒ up to 20K allocation rows in **one** SELECT. Mobile P=10 gives the same 4/3 call count.
- Request controller count and page are not wrapped in an explicit transaction here; no BEGIN/COMMIT added by this read body. Counts are ORM execution sites, not guarantees of snapshot consistency across concurrent writes.

### Tag-mode conditionals (do not substitute ordinary P)

- Normal list with `tag_mode=true`: select DISTINCT page of tags (`controller/channel.go:393` → `model/channel.go:2025-2034`), count DISTINCT nonempty tags (`controller:400` → `model:2284-2287`), then **one coalesced SELECT for all selected tags** (`controller:420`), chunked entry projection (`:425`), then type_counts (`:460`). With N underlying channel rows / M migrated rows and nonempty selected tags, **4 + chunk(M)** SQL calls. Twenty tags can expand to arbitrarily more than 20 channels; N and K cannot be inferred from tag page size alone. No requested tags ⇒ skips channel/entry queries (3 aggregate/tag queries remain).
- Search tag mode: type_counts first (`controller:601`), then ListChannelsPage (`:606`). Tag matching is an SQL subquery (`model/channel.go:1083-1120`) embedded in aggregate/page queries, **not an extra client/DB round trip**. SQL count remains **3 + chunk(M)** for returned channel page.

### Separate id-dependent requests (not hidden queries in list endpoint)

- `/api/channel/quota`: `controller/channel_quota.go:56-81` → `model/channel_budget_store.go:84-96` → `ReadChannelBudgetViews:53-80`. If activation already cached true, one SELECT of `channel_budget_accounts WHERE scope_type='channel' AND scope_id IN (...)` (`:75`). Otherwise first a marker SELECT from `options` (`channel_budget_activation.go:19-40`), then account SELECT. Inactive marker is not remembered as true, so later calls can repeat marker SELECT. No key-budget account loop or Redis in this read. For N≤200: **1 or 2 SQL** beyond list; for parallel chunks each independently follows activation state.
- `/api/channel/usage`: `controller/channel_usage.go:18-40` → `model/channel_usage.go:41-51`: **one LOG_DB grouped aggregate** over consume logs for submitted ids (no date bound in this query), not one per channel. No Redis in body. It is separate from the primary DB and could have a different engine/configuration; current deployment location was not probed.
- `/api/channel/concurrency`: `controller/channel_concurrency.go:57` → `model/channel.go:2228-2231`: **one SELECT id,setting WHERE id IN (...)**. No `channel_info` selected, so AfterFind sees revision zero and does not load entries. Then controller `:82` → `service/channel_capacity_metrics.go:38-70,77-123`: Redis enabled ⇒ **N pipelined EVALSHA commands in one pipeline Exec** (`:82-84`), each script executes server-side TIME + two ZCOUNTs (`service/channel_capacity.go:103-109`). Internal Lua calls are not separate WAN round trips. NOSCRIPT ⇒ SCRIPT LOAD and one whole-pipeline retry (`metrics:118-120`); Redis disabled ⇒ local map snapshot, zero Redis. Default N=20: 1 SQL + 20 script commands in 1 usual coordinator pipeline RTT; this is Redis command fan-out, **not 20 sequential WAN waits**.

### Middleware overhead before body (important with remote Redis)

The body count above deliberately excludes authentication, but the real list HTTP request traverses these first:

1. Global API limiter registered `router/api-router.go:24`. If `GlobalApiRateLimitEnable` and Redis enabled: one **EVAL** (`middleware/rate-limit.go:183-187,149-152,111-117,80-86`). Script performs INCR/EXPIRE/TTL internally (`:24-31`); one WAN command, not three. Otherwise zero Redis for this limiter.
2. `AdminAuth` (`router/channel-router.go:42`, `middleware/auth.go:52-56,138`) resolves dashboard credential (`auth.go:194-220`). On ordinary signed-in browser JWT: local token parse, then `service.ValidateLoginSession` (`service/auth_session.go:130-151`).
3. Healthy warm Redis browser-session path, **sequential** before controller:
   - **HGETALL session**: `model/user_session.go:225-228,266-268` → `common/redis.go:161-167`.
   - **HGETALL user**: `auth_session.go:143` → `model/user_cache.go:100-104,129-135` → same Redis wrapper.
   - **MGET user pending/committed auth-version fences**: `user_cache.go:146` → `model/user_auth_cache.go:108-112`.
   No DB read on that warm path. They are not pipelined with each other or with list SQL. Warm browser request therefore has **3 serial Redis commands**, or **4 with global Redis API limiter**, before the `3+chunk(M)` serial SQL operations.
4. Permission check: `middleware/auth.go:427-435` → `service/authz/resolver.go:10-34`. Roles derived from system role (`authz/assignment.go:7-16`); current enforcer is held in memory (`enforcer.go:63-67`). No per-request permission DB query in this path. Policy sync elsewhere is not a list-request call.
5. Browser GET does not generate fallback admin-write audit: `middleware/audit.go:111-115` returns nil for GET. Browser JWT does not create PAT audit context.

Thus ordinary migrated desktop page, warm browser Redis session: **4 body SQL + 3 auth Redis = 7 serial execution sites**; **8 if global Redis API rate limiting enabled**. This is a call graph, not a measured elapsed-time claim. No site in this formula depends on K except allocation result volume.

Cache/credential conditionals, not included in warm count:

- Session Redis miss/invalid snapshot/read failure ⇒ SELECT `user_sessions` (`model/user_session.go:208-215,235`), then synchronous cache **EVAL** at `:249-250,356`; expiry/revocation fences may reject or force authoritative recheck (`:252-255`, `confirmUserSessionActiveSnapshot:380`). Stop-on-error branches don't reach list body.
- User Redis miss/schema/MAC/fence failure ⇒ SELECT user (`user_cache.go:110`, `user.go:538-548`), then **MGET** fence (`user_cache:115`) and cache-fill **EVAL** (`:119` → `user_auth_cache.go:93`). If hash was valid enough to reach a failed MGET before fallback, that earlier MGET is an additional attempted command. Redis disabled skips Redis and takes authoritative user/session SQL reads.
- PAT rather than browser JWT ⇒ `middleware/auth.go:255-272` reads token by hash (`model/user_access_token.go:85-90`), then same user-cache/fence path; GET audit after handler goes through `middleware/audit.go:344-372` → `model/audit_log.go:76-87,167-192` (LOG_DB write). It must not be folded into the browser-session formula. Legacy token and invalid/expired credentials follow their own reject/audit branches.
- Server/client retries, TLS/connection setup, timeouts, and actual cache contents are not established by source-only review. No blanket exact cold-network RTT number is asserted.

## Findings for root

- Current editor closes after its one PUT; two explicit invalidation promises are not awaited. Slow PUT and slow subsequent list GET are separate stages in this source.
- Ordinary list is already SQL-paginated and batches allocation projection; **no per-channel HTTP or SQL allocation N+1** on this path. It still transfers/decodes all active allocation rows for returned migrated channels (M×K rows).
- List request has three sequential baseline SQL operations plus one projection chunk at default P=20, and warm browser auth adds three serial remote Redis commands (plus optional limiter). This is the concrete serial round-trip chain to compare against production traces, not evidence that any specific stage is the measured bottleneck.
- List-dependent quota/usage/concurrency reads fan out after ids arrive; refresh button overlaps three families. Concurrency uses pipelined per-id Redis scripts; budget activation can add a marker read; usage aggregates LOG_DB without a time bound. All are distinct from list body.
- No production source fixes or benchmarks were made; all prior dirty source edits were preserved.
