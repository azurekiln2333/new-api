# Frontend statistics inventory — R24

## Status and scope

- Research-only deliverable for `.trellis/tasks/11-26-durable-usage-statistics`; no R24 production source changes are included.
- This inventories browser-visible numeric usage, request-count, cache-token, rankings, channel-usage, budget, account and performance statistics; their callers, filters, permission scope, client query keys and cache identity behavior.
- Values described as log-backed are observations of the inspected frontend API contracts; the backend-statistics inventory is the authority for exact SQL and source-write semantics.
- No local tests, builds, browser validation, production access, settings changes or Git operations were performed for this inventory. This document does not claim statistics are complete, durable or independent of logs.

## Surface map

| Surface | Frontend entry / rendering | API and aggregation inputs | Scope and displayed measures |
| --- | --- | --- | --- |
| Overview usage cards | `web/src/features/dashboard/components/overview/summary-cards.tsx`; config in `web/src/features/dashboard/hooks/use-dashboard-config.tsx` | Current account fields in `auth.user`; rolling-24h `getUserQuotaDates` → `/api/data/self` → client totals/buckets | Self-only historical `used_quota`, `request_count`, remaining quota, 24h consumed quota/tokens, runway/health; 12-bucket quota/request/token sparklines. The displayed balance fields are account state, not usage-log totals. |
| Model call stat cards | `web/src/features/dashboard/components/models/log-stat-cards.tsx`; `useModelStatCardsConfig` | `getUserQuotaDates(buildQueryParams(...), isAdmin)` → `/api/data` for role ≥ ADMIN, otherwise `/api/data/self`; `calculateDashboardStats` in `web/src/features/dashboard/lib/stats.ts` | Filtered interval sum of quota, consume count, token-used, cache-read and cache-write; average RPM/TPM divides count/token sums by the selected time range in minutes. |
| Model charts | `consumption-distribution-chart.tsx`, `model-charts.tsx`, `lib/charts.ts` | Reuses `QuotaDataItem[]` loaded by `LogStatCards`; no additional usage endpoint | Quota/tokens metric toggle; quota-distribution bar/area by time/model; model call-count pie/ranking/trend. `created_at` is bucketed client-side by hour/day/week; totals and tooltips sum returned rows. |
| User ranking/trend charts | `components/users/user-charts.tsx`, `lib/charts.ts` | Admin-only `getUserQuotaDataByUsers` → `GET /api/data/users`; user aggregates processed client-side by username | Selected rolling period, granularity, top 5/10/20/50 and quota/tokens metric; user rank/trend, with user labels returned by the endpoint. R9 separately added live ADMIN+ gating, viewer/role query dimensions and AbortSignal forwarding; no chart arithmetic changed. |
| Flow/Sankey | `components/flow/flow-charts.tsx`, `lib/flow.ts` | `getFlowQuotaDates` → `/api/data/flow` for admin/root, `/api/data/flow/self` otherwise | Selected time/custom range and optional username; quota/tokens/requests; user, model, group and channel dimensions for admin; root adds node/token dimensions. `flowRole` participates in the query key. Sensitive-label toggle changes rendered labels, not server data or node IDs. |
| Public rankings | `features/rankings/index.tsx`, Models/Market Share/Pulse sections | `GET /api/rankings?period=...`; `useRankings` | Today/week/month/year model and vendor tokens, cache-read/write totals, shares, growth, movers/droppers and historical series. Model total-token labels explicitly note new requests count cached tokens once while historical totals may exclude cached tokens. No usernames or user IDs occur in this snapshot. |
| Per-model usage drawer | `components/model-usage-drawer.tsx` | `GET /api/rankings/model-usage?model=...&period=...`; viewer-safe server projection | Model requests/tokens/quota totals and top-20 per-user rows. Server owns names/IDs masking; ADMIN+ROOT get full names, ordinary viewer gets own full name/other current usernames partial, anonymous gets `***`; masked IDs absent. Response uses no-store and the route disables server cache. |
| Channel table and details | `features/channels/components/channels-table.tsx`, `channel-usage-context.tsx`, `drawers/channel-usage-summary.tsx` | `GET /api/channel/usage?ids=...` and `GET /api/channel/:id/usage` | All-time consume-log request count, prompt/completion/total tokens and quota; detail adds model breakdown, current model-enabled state and last-used timestamp. The table batch queries visible channel IDs; detail query is keyed by selected channel ID. |
| Common usage-log statistics | `features/usage-logs/components/common-logs-stats.tsx` | `GET /api/log/stat` or `/api/log/self/stat`, built by `buildApiParams` | Filtered gross consume quota, last-60-second RPM/TPM, cache-read/input totals and derived cache-hit rate. Filter dimensions follow common usage-log URL state and match mode. These are numeric cards, distinct from the retained rows and pagination count. |
| Retained log rows / pagination | `usage-logs-table.tsx`, mobile list/card and export dialog | `/api/log`, `/api/log/self`, category-specific log endpoints and exports | Actual retained matching rows, pagination totals and export counts. These describe available log records, not lifetime business totals, and may decrease with retention/deletion. |
| Account/user and key quota panels | User table/detail and key table quota cells | User/key management APIs and current primary-account records | `users.used_quota`, `users.request_count`, key `used_quota`/`remain_quota`, plus displayed remaining/limit. These are current account/balance/accounting values, not model/time usage distributions. |
| Channel budget panels | `channel-budget-cell.tsx`, quota dialogs and multi-key budget views | Channel budget APIs, keyed by channel/allocation | Period/lifetime used quota, configured limit, reserved amount, remaining quota and reset epoch. This is an independent budget ledger with its own enablement and reset semantics, not a substitute for usage history. |
| Performance panels | Overview `performance-health-panel.tsx`; Models `performance-overview.tsx` | `/api/perf-metrics/summary?hours=24` | Completed performance metric buckets: success rate, latency, throughput and top models. This is a separate performance domain/collector and retention policy, not a request-consumption aggregate. |

## Detailed data flow and dimensions

### 1. Overview cards and mini-trends

`OverviewDashboard` renders `SummaryCards`. Four card values are built from the authenticated user object: historical consumed quota (`used_quota`), lifetime request count, current quota/remaining balance and derived runway. The 24-hour series comes from `computeTimeRange(1)` and `getUserQuotaDates` with `default_time: 'hour'`; the API helper defaults to `/api/data/self` because this caller does not set `isAdmin`.

The summary component sums `quota`, `count` and `token_used` from returned buckets into 12 local sparkline buckets. It reconstructs a historical balance line by walking recent quota backward from current balance. No independent stats endpoint is used for that calculation. Query key: `['dashboard','overview','summary-sparklines', start_timestamp, end_timestamp]`; it does not embed user ID/role. The root application clears the React Query cache when the auth session SID changes, but a same-session `setUser` role/profile refresh does not clear the whole cache. The personal balance and request-count cards read the Zustand auth user directly.

Overview onboarding completion uses the same auth-user `request_count` and balances; it is an account/setup status signal, not a retained-log row count.

### 2. Model call analytics

`LogStatCards` is the network owner for the model section. It derives time params from persisted chart preferences plus the model filter dialog (start/end, selected granularity, optional username). Its effect depends on filters, whether the current role meets ADMIN, and the parent update callback; an `AbortController` guards state updates on cleanup, but the API request itself does not receive that signal. On success it runs `calculateDashboardStats` and returns the same data to the chart components through `Dashboard` state.

Displayed stat cards are:

- Total Count (the summed `count` measure);
- Total Quota;
- Total Tokens (`token_used`);
- Cache Read and Cache Write;
- Average RPM and Average TPM, computed from count/token sums divided by requested time-range minutes.

The range/username params are produced by `buildQueryParams`; endpoint selection is role-based: self for users, all-user `/api/data` for ADMIN and ROOT. Quota-data is hourly, and client chart granularity further groups those rows. It is not an exact-second event view.

`processChartData` sums each row by model/time. The distribution chart presents quota or token metric; Model Call Analytics uses call count for pie, model ranking and trend. Cache values are stat cards, not separately charted here. Its chart data is derived from the same endpoint response and does not have its own React Query cache key.

### 3. User analytics

`UserCharts` fetches only for an authenticated role at or above `ROLE.ADMIN`; other roles neither issue `/api/data/users` nor render the component's charts. Its query key is `['dashboard','user-quota', timeRange, userId|'anon', role|0]`. The query function forwards React Query's abort signal through `getUserQuotaDataByUsers` to Axios. This is viewer-sensitive all-user content. R9's dashboard correction adds the live-admin display gate to avoid showing retained prior data after demotion; it does not broaden server permissions.

The API rows contain current/historical usernames, model, hourly timestamp and quota/count/token totals. The client groups across rows by username for ranking and by username/time for trend. Ranking total subtext is the sum of the selected top-N rows, not necessarily a global total when there are more users than the selected limit. The user section's selected period, granularity and top-user limit are parent-held in `Dashboard` state and survive section switching; token/quota metric is shared with model charts via local storage.

### 4. Flow analytics

`FlowCharts` builds API params from the model time/username filters and saved default range. It derives role classes (`user`, `admin`, `root`) from current auth state and requests self or admin flow endpoint. Query key: `['dashboard','flow', flowQueryParams, flowRole]`; role is represented, user ID is not. Auth SID changes clear the cache. Admin/root dimension differences are in the backend projection; the client adds chart-only metric selection, top-node limit, overflow behavior, selected node/user filters and highlighting. Sensitive display masking is applied to labels while retaining node IDs to preserve graph shape; it is not an identity authorization boundary.

Flow summaries are derived from the currently fetched rows after explicit filters. They do not represent an all-time counter. Current filters/time range and admin/root dimension policy must remain aligned when a durable source replaces quota-data rows.

### 5. Rankings and model token totals

`useRankings(period)` uses query key `['rankings', period]`, stale time five minutes, and no viewer identity because the public snapshot contains model/vendor aggregates and no usernames. The page selects one of today/week/month/year and passes the same period through model leaderboard, vendor share/history, movers/droppers and drawer.

Clicking a model opens `ModelUsageDrawer`; it does not navigate to pricing. `useRankingModelUsage` uses full key `['rankings','model-usage', modelName, period, viewerId|'anon', viewerRole|0]`, stale time one minute, and passes an AbortSignal. The API response is user-scoped, and both HTTP `Cache-Control: no-store` and route-level cache disable are used. Names and IDs are projected by the backend. The user table shows numeric usage even for partially masked labels; masking is identity-only.

The drawer model totals cover all users while its user ranking is bounded top-20. The independent public rankings snapshot has top models and time series, not per-user rows. The current data source, token/cache semantic caveat and retained-log independence are documented in the UI and in `research/backend-statistics-inventory.md`.

### 6. Channel usage

`useChannelUsageSummaries(ids)` sorts/deduplicates visible IDs and keys the query by `['channels','usage','summaries', ids]`; one batch request populates channel cards/table. `useChannelUsageDetail(channelId)` keys separately by selected channel. Both consume signal and avoid focus refetch. The displayed data is all-time consume-log aggregation, not date-filtered dashboard data. The detail table merges historic model names with current model status and has `last_used_at`. Channels lacking aggregate rows may be absent and UI defaults show `-` / empty state.

Channel quota budgets shown in the same product area are distinct: period/lifetime budget usage and limits are from the budget ledger endpoints/types, not the all-time usage summary endpoint. Do not conflate `period_used_quota`, `lifetime_used_quota`, current channel usage quota, upstream balance, or configured limit.

### 7. Usage-log summary and retained rows

`CommonLogsStats` builds `GetLogStatsParams` from route search (time, log type, model, token, group, exact/fuzzy match, request IDs; plus admin-only channel/username). `useLogsViewScope` derives effective `self`, `admin` or `root` access from role and all/self selection. It calls `/api/log/stat` for an all-admin view and `/api/log/self/stat` for self scope. Query key is `['usage-logs-stats', isAdminView, searchParams]`, placeholder data retains the previous result while filters transition. It does not contain an explicit user ID; root `RootComponent` clears query cache on session SID change. Scope/filter state must not be mistaken for pagination.

Cards are Usage (gross quota, optionally hidden by the sensitive-display UI toggle), RPM, TPM and cache-hit rate; cache-read and input-token values are shown in the details popover. The cache rate is derived from the two totals supplied by the API. This summary is currently backed by matching consume log rows and can shrink when those rows are deleted; it is separate from the log table's retained-row count.

`UsageLogsTable` query key includes category, effective access tier, page index/size, column filters, URL search and translation function. Its data and `totalCount` are actual matching rows retained by log storage. Exports likewise stream log rows and return exported-record count. These are intentionally log-retention semantics, not durable lifetime statistics.

### 8. Other usage-adjacent counters

- User management table/profile returns current `used_quota`, `request_count`, quota and status fields from user APIs. These current account aggregates survive ordinary log deletion but lack model/time/group dimensions and are not replacements for model analytics.
- API-key quota cell displays current used/remain/total quota and remaining percentage. It is a token/account balance representation, not request count or historical model tokens.
- Channel quota ledger entries expose their own period and lifetime usage/limit/reservation/reset state; they have separate ledger activation and lifecycle.
- Performance metrics have dedicated APIs, query key `['perf-metrics-summary', 24]`, and performance-specific collection/retention. Preserve them as a separate authority; do not turn request logs into latency/success totals.

## Permission and cache-identity summary

| Frontend query | Key dimensions | Viewer handling | Identity/cache notes |
| --- | --- | --- | --- |
| Overview 24h self series | start/end timestamps | `/api/data/self` | No user ID in key; auth-session SID change clears QueryClient. |
| Model stat cards and charts | Local effect on filters + `isAdmin` | `/api/data` only ADMIN+, otherwise `/api/data/self` | Not a React Query cache; stale async state is guarded by local AbortController, though API call does not consume it. |
| User charts | time range + user ID + role | query enabled/rendered only for ADMIN+ | Viewer/role separated; signal forwarded; demotion immediately hides old chart output. |
| Flow | API params + `user/admin/root` role | self for user, admin projection for ADMIN+, root-only extras | Role changes partition data; no user ID key. Auth SID change clears client cache. |
| Public rankings snapshot | period | Public/module-gated endpoint; no username fields | Shared by design because only model/vendor stats are returned. |
| Model usage drawer | model + period + user ID + role | Server masks identities; no-store/DisableCache | Identity and role separate caches; request supports abort. |
| Channel usage batch/detail | visible channel-ID list or channel ID | Channel route authorization | No viewer ID in key; auth-session change clears client cache. |
| Usage-log summary | effective all/self boolean + route search filters | User self endpoint or admin endpoint | No user ID in key; auth-session change clears client cache; `isAdminView` partitions scope. |
| Usage-log rows | category + self/admin/root view access + pagination + filters/search | Role/scope-aware endpoints | Access-tier key prevents all/self/root row reuse; SID change clears client cache. |
| Performance summary | hours window | Rendered in admin-only dashboard areas | No user ID in key; separate operational metric domain. |

## Inventory implications for R24 design

1. A durable replacement needs to cover dashboard quota/count/token/cache fields, user chart labels and grouping, flow dimensions, model/vendor ranking totals and time buckets, model drawer user aggregates, channel/model all-time totals and last-used, and the numeric usage-log summary filter contract. One hourly chart-only aggregate cannot answer all of these with their current semantics.
2. Preserve user scoping and the ADMIN/ROOT distinctions. In particular, frontend display masks are not authorization; identity is masked by the server for the model drawer, while dashboard user rankings currently rely on an admin-only endpoint.
3. Keep retained-log pagination totals, export counts, log details and surviving request-content associations on retained logs. Removing log rows may reduce those; it must not imply business-history loss for independent statistics.
4. Preserve numeric distinctions across interfaces: gross quota vs current balance, raw/model `token_used` vs log TPM, cache read/write vs input denominator, request/consume-event counts vs unique logical requests, all-time channel summaries vs filtered dashboard ranges, and performance/budget/account counters vs usage history.
5. Query keys and invalidation are UI cache policy, not durable storage. Existing SID-based cache clearing is in `web/src/routes/__root.tsx`; viewer/role-aware query keys exist for the sensitive user-chart and model-usage surfaces. Preserve or improve equivalent isolation if permissions or payload shape change.

## Source anchors

- Dashboard: `features/dashboard/components/overview/{summary-cards,overview-dashboard}.tsx`, `components/models/{log-stat-cards,consumption-distribution-chart,model-charts}.tsx`, `components/users/user-charts.tsx`, `components/flow/flow-charts.tsx`, `api.ts`, `lib/{stats,charts,flow,filters}.ts`, `hooks/use-dashboard-config.tsx`.
- Rankings: `features/rankings/{api.ts,index.tsx,types.ts}`, `hooks/use-rankings.ts`, `components/{models-section,model-leaderboard,market-share-section,pulse-section,model-usage-drawer}.tsx`.
- Usage logs: `features/usage-logs/api.ts`, `types.ts`, `components/{common-logs-stats,common-logs-filter-bar,usage-logs-table,usage-logs-provider}.tsx`, `lib/utils.ts`.
- Channel/user/key and performance values: `features/channels/hooks/use-channel-usage.ts`, `components/drawers/channel-usage-summary.tsx`, `features/users/components/{users-table,users-columns}.tsx`, `features/keys/components/api-key-quota-cell.tsx`, `features/performance-metrics/api.ts`.
- Auth cache lifecycle: `web/src/routes/__root.tsx` clears QueryClient when session SID changes.
- Backend route/aggregate source details and log deletion semantics: `.trellis/tasks/11-26-durable-usage-statistics/research/backend-statistics-inventory.md`.
