# Admin/channel/users/sidebar UI requirements — source trace, reuse, tests

Research only. No product edits. Each requirement below is sized for its own commit; root owns commit sequencing. Section R labels in this research are local labels; parent PRD R1-R25 is the authoritative requirement map.

Root review corrections: current formatCompactNumber with en and maximumFractionDigits:1 formats 318,574,217 as 318.6M, not 320M; use the actual formatter output in tests. Any added database sort/query behavior requires the real three-engine matrix, even without an index/schema change. Client cards must reuse the existing deliberate key/connection confirmation, not automatically transfer a credential from page render. Parent design.md supersedes differing proposals below.

Sources read: `AGENTS.md`, `web/AGENTS.md`, `.agents/skills/shadcn-ui/SKILL.md` + `vendor/shadcn/rules/{base-vs-radix,composition,forms,styling,icons}.md`, `.agents/rules/billing.md` (token-count surfaces only; no billing arithmetic is touched by any item here).

Reuse rules that govern all items (`web/AGENTS.md` 3.3): search `web/src/components/` first, prefer project business components, and record any capability gap when a new component is added. shadcn rules: this project uses Base UI, so composition uses `render`, not `asChild` (`vendor/shadcn/rules/base-vs-radix.md`).

---

## R1. Channel card layout: Priority/Weight side by side, compact response row, Used/Used tokens same row, groups removed

### Current structure

`web/src/features/channels/components/channel-card.tsx:170-206` renders one `<dl>`:

```
grid grid-flow-col grid-cols-[minmax(0,1fr)_auto_auto] grid-rows-2 gap-x-4 gap-y-2
```

With `grid-flow-col` + `grid-rows-2`, items fill column-by-column, so today the pairs are stacked:

| column | row 1 | row 2 |
| --- | --- | --- |
| 1 | Used / Remaining (`:172-180`) | Used tokens (`:181-187`) |
| 2 | Priority (`:188-191`) | Weight (`:192-195`) |
| 3 | Response (`:196-201`) | Last Tested (`:202-207`) |

Groups are rendered above it at `:146-163` via `GroupBadge` (`@/components/group-badge`), label masked when `!sensitiveVisible`.

Cells come from `flexRender` over the shared column definitions (`:71-91`), i.e. `web/src/features/channels/components/channels-columns.tsx` (`priority` at `:1221`, `weight`, `balance`/`used_tokens`, `response_time`, `test_time`).

### Change shape (no new components)

- Remove the groups block (`:146-163`) and its `parseGroupsList`/`GroupBadge` imports; keep `-` placeholder out of the card (it only existed for empty groups).
- Restructure `<dl>` to explicit rows instead of `grid-flow-col` + implicit column fill:
  - row 1: `Used / Remaining` and `Used tokens` side by side;
  - row 2: `Priority` and `Weight` side by side (this is the actual "restore side by side" ask — they are currently stacked);
  - row 3: response metrics as one compact row (`Response`, `Last Tested`, joined inline, smaller type).
- Keep `StatusBadgeTypeContext.Provider value='text'` (`:168`) and the `[&_[data-slot=status-badge]]` overrides so cell badges stay compact.
- `ChannelCard` is `memo`-wrapped (`:213`); layout changes are internal, so the memo contract is unchanged. No prop change needed for R1.

### Test impact (must be updated in the same commit)

`web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`:
- `:181-186` asserts `card.textContent` contains `-` for a channel with no usage — that assertion exists because of the groups placeholder; re-point it at the Used/Remaining cell.
- `:196` clicks `within(card).getByText('Used tokens')` to open details; label text must remain or the test must target the new cell.
- Add: Priority and Weight cells are siblings in one row (assert shared row container / ordering, not Tailwind class strings — `web/AGENTS.md` 3.14 forbids class-string snapshots).
- Add: no group badges rendered in the card (`queryByText('vip')` absent) while the details drawer still shows group models (R2).

Also check `web/src/features/channels/components/__tests__/channel-configuration.test.tsx` and `priority-order.test.tsx` for `GroupBadge`-in-card assumptions before editing.

Files owned: `web/src/features/channels/components/channel-card.tsx`, `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`.

---

## R2. Details drawer: show group-accessible models (replacing card groups)

### Data already available client-side — no server change

- `Channel.group` (comma list) and `Channel.models` (comma list): `web/src/features/channels/types.ts` channel schema.
- Per-group visibility lives in the channel `settings` JSON: `relaykit/dto/channel_settings.go:168-171` (`GroupModels map[string][]string`, key `group_models`); exposed to the client as `Channel.settings` (`model/channel.go:62`, `json:"settings"`).
- Parsers already exist: `web/src/features/channels/lib/channel-group-models.ts:36-48` (`parseGroupModels`) and `:55-75` (`pruneGroupModels`).
- Server truth to mirror exactly: `model/ability.go:245-270` (`filterGroupModels`, used by `model/channel_cache.go:195-198`) — **a missing or empty entry means every channel model is visible**. The drawer must render "all models" for such groups, never "none".

### Change shape

- Add a section inside the drawer body next to the usage body: `web/src/features/channels/components/drawers/channel-details-drawer.tsx:110` currently sets `body = <ChannelUsageSummary .../>`; render the new section as a sibling so the drawer owns composition and `ChannelUsageSummary` keeps its single responsibility.
- Build the mapping with `parseGroupModels(channel.settings)` then `pruneGroupModels(map, parseGroupsList(channel.group), parseModelsList(channel.models))` — reuse `parseGroupsList` (`web/src/features/channels/lib`, already imported by the card) and the existing model-list parser in the same lib.
- Render as a definition list of group → model badges, reusing `GroupBadge` for the group label and plain text/badges for models; long lists need a scroll cap (`max-h-* overflow-y-auto`) rather than truncation.
- Empty state: channel with no groups or no models shows a single muted `-`.

### Discrepancies to flag

- `group_models` may contain stale model names; server prunes them during ability sync (`pruneGroupModels` mirrors this) — display must intersect, or the drawer will claim a model is visible when routing says otherwise.
- Group visibility is per-group; the drawer must not imply that the *requesting admin's* group is special.

### Tests

Extend `channel-details-entry.test.tsx` (same file, it already renders the drawer probe) — but the real drawer is not mounted there. Prefer adding cases to `web/src/features/channels/components/__tests__/channel-details-drawer.test.tsx` (already exists):
1. Group with an explicit `group_models` entry shows only those models;
2. Group without an entry shows every channel model;
3. Stale model in `group_models` is not displayed;
4. Channel without groups shows the empty state.

Files owned: `channel-details-drawer.tsx` (+ a small `group-models-section.tsx` in the same `drawers/` folder if the section exceeds ~80 lines), `channel-details-drawer.test.tsx`.

---

## R3. Exact + compact token counts everywhere (`318,574,217 (320M / 3.2亿)`)

### Existing helpers and the discrepancy

- `formatNumber(value, locales)` — exact, 2 fraction digits: `web/src/lib/format.ts:36-44`.
- `formatCompactNumber(value, locales)` — `notation: 'compact'`: `web/src/lib/format.ts:64-74`. With `zh-CN` this yields `3.2亿`; with `en` it yields `320M` — exactly the requested pair, so **no new formatter is needed**.
- `formatTokens` — `web/src/lib/format.ts:230-236` hand-rolls K/M and cannot produce `亿`. **This is the discrepancy**: it must not be used for the requested surfaces. Audit its call sites and migrate them or leave them explicitly out of scope (do not silently leave two conventions on one screen).
- Locale discipline (`web/AGENTS.md` 3.1, lint `project/intl-locale`): every call passes `toIntlLocale(i18n.resolvedLanguage || i18n.language)`; the established pattern is `channel-usage-summary.tsx:34`.

### Surfaces in scope

| Surface | File:line |
| --- | --- |
| Card "Used tokens" | `channel-card.tsx:181-187` (via column cell) |
| Drawer overview metrics | `channel-usage-summary.tsx:78-98` |
| Drawer model usage table | `channel-usage-summary.tsx:203-204` (`ModelUsageTable`) |
| Users used-balance column (R4) | `web/src/features/users/components/user-quota-cell.tsx:52-56` (tokens currency mode) |
| Usage log token columns | `web/src/features/usage-logs/components/*` (audit separately; same helper) |

### Component decision (must be justified per `web/AGENTS.md` 3.3)

Composition "exact, then compact in parentheses" repeats on ≥4 surfaces, which is the threshold where a shared component beats copy-paste. Proposal: `web/src/components/token-count.tsx` exposing `TokenCount({ value, className })`, internally `formatNumber` + `formatCompactNumber` with the current locale. Capability gap to record in the change summary: `formatNumber`/`formatCompactNumber` are pure string helpers with no shared JSX or title/aria contract, so each site would otherwise re-implement the same markup and the exact/compact pairing.

Rules for the component:
- Show compact only when it differs from the exact value (`< 1000` shows the exact number once);
- `title`/`aria-label` carries the exact value for screen readers; the visible text is exact + compact;
- `tabular-nums` and no wrapping in table cells;
- no currency logic inside it (quota stays with `@/lib/currency`).

### Tests

- New `web/src/components/__tests__/token-count.test.tsx` (component module test directory rule, `web/AGENTS.md` 3.14): en → `318,574,217 (320M)`; zh-CN → `318,574,217 (3.2亿)`; `999` → exact only; `null` → `-`; language switch re-renders (i18n contract).
- Extend `channel-details-entry.test.tsx` / `channel-details-drawer.test.tsx` to assert the card and drawer show the same exact+compact pair.
- Update `quota-display.test.tsx` (`:70`, `:271`) if the users cell changes shape.

Files owned: `web/src/components/token-count.tsx`, `web/src/components/__tests__/token-count.test.tsx`, plus the five call sites above.

---

## R4. Users table: separate sortable "Used balance" column with correct global pagination

### Current state

- One `quota` column renders both numbers: `web/src/features/users/components/users-columns.tsx:186-196` → `UserQuotaCell` (`user-quota-cell.tsx:52-79`), which stacks remaining and `Used amount` and wraps them in `QuotaDetailsPopover` (`@/components/quota-details-popover`).
- Sorting is **server-side**: `users-table.tsx:210-213` sets `manualPagination/manualFiltering/manualSorting`, `totalCount` at `:214`; `sortParams` (`:118-124`) is part of the query key (`:143`) and the request (`:152-155`).
- Sorting resets to the first page: `handleSortingChange` `:126-131` — the new column must keep this behaviour or pagination breaks.

### Required server change (blocking)

`model/user.go:22-29` `userSortColumns` has no `used_quota` entry, and `NewUserSortOptions` (`:36-50`) silently falls back to `id desc` for unknown keys. Without adding it, a sortable header would appear to work while the server ignores it — a silent wrong-result bug.

- Add `"used_quota": "used_quota"` to `model/user.go:22-29`. Both list and search paths use the same options (`controller/user.go:352`, `:382`), so one entry covers both.
- Frontend union: `web/src/features/users/types.ts:84` area (`UserSortBy`) must include `'used_quota'`.
- Column definition: split `quota` into `quota` (Available Balance, keeps `QuotaDetailsPopover` via `UserQuotaCell`) and a new `used_quota` column with `accessorKey: 'used_quota'`, `enableSorting` default true, `meta.mobileOrder` set alongside the existing `:195`.
- Reuse: `formatQuotaWithCurrency` / `getCurrencyDisplay` (`@/lib/currency`); in tokens display mode route through `TokenCount` (R3) so both columns obey one convention.

### Correct pagination requirements

- `manualSorting` already true; nothing to change in `use-data-table` (`web/src/components/data-table/hooks/use-data-table.ts:295-296` disables client row models when manual).
- Keep `handleSortingChange` page reset; add a regression asserting the request after sorting carries `sort_by=used_quota&sort_order=…&p=1`.
- `ensurePageInRange` (`:215`) already guards out-of-range pages after a refetch.

### DB note

`used_quota` is an existing column (`users` model); this is an ORDER BY only — no migration, no schema change. It is likely unindexed, so sorting is a full scan on very large tables: report it, and do **not** add an index without the three-database verification matrix required by `AGENTS.md`.

### Tests

`web/src/features/users/components/__tests__/`:
1. Used balance header sorts and the request resets to page 1;
2. Sorting preserves `totalCount`-based page count (global pagination, not page-local);
3. Tokens currency mode renders exact+compact in the used column.
Backend: extend `model/user_test.go` (or the existing sort test file) with `NewUserSortOptions("used_quota","asc")` → `SortBy=used_quota`; and a list query assertion that ordering applies.

Files owned: `model/user.go`, `web/src/features/users/types.ts`, `users-columns.tsx`, `users-table.tsx`, `user-quota-cell.tsx` (only if the popover moves), plus the users tests.

---

## R5. Sidebar: fold the FOUR logs into one "Logs" group, group the two code pages, rename Chat group to "Access configuration" with a client-card landing page

### Current navigation

`web/src/hooks/use-sidebar-data.ts`:
- group `chat` (`:58-73`): `Playground` link + `Chat` item of `type: 'chat-presets'`;
- group `general` (`:74-111`): `Usage Logs` (`/usage-logs/common`), `Audit Logs` (`/usage-logs/audit`), `Task Logs` (`/usage-logs/task`, which today also carries `activeUrls: ['/usage-logs/drawing']` and `configUrls: ['/usage-logs/drawing', '/usage-logs/task']`);
- group `admin` (`:133-197`): `Invitation codes` (`/invitation-codes`, `requiredRole: ROLE.ADMIN`) and `Redemption Codes` (`/redemption-codes`, no per-item role) as two separate top-level items.

### Folding logs — four children, same collapsible style as system settings

- `NavCollapsible` already exists: `web/src/components/layout/types.ts:44-49` (`items:` nesting), rendered by `web/src/components/layout/components/nav-group.tsx:77`.
- "Same expandable style as system settings": `web/src/components/layout/config/system-settings.config.ts:41-90` nests leaf items under collapsible parents (`items: getSiteSectionNavItems(t)` etc.). Reuse exactly that shape — one parent row that expands into child rows. The settings **nested sidebar view** (`resolveSidebarView`, `sidebar-view-registry.ts:32-38`) is a separate mechanism and is **not** required here: `/usage-logs` keeps the root sidebar, only the group becomes collapsible.
- Change: replace the three flat `general` items with one collapsible titled `Logs` (icon `FileText`/`ScrollText`) whose children are **four** entries:
  1. `Usage Logs` → `/usage-logs/common` (module `log`);
  2. `Audit Logs` → `/usage-logs/audit` (module `audit`; standalone route `web/src/routes/_authenticated/usage-logs/audit.tsx`, not a `$section` value);
  3. `Drawing Logs` → `/usage-logs/drawing` (module `midjourney`) — **currently missing from the sidebar**: drawing is reachable only through the in-page section nav (`web/src/features/usage-logs/section-registry.tsx:31-34`, id `drawing`, title `Drawing Logs`) and the Task Logs `activeUrls` hack (`use-sidebar-data.ts:105`). Add it as a real child and drop `/usage-logs/drawing` from Task Logs' `activeUrls`/`configUrls` so the active highlight becomes truthful;
  4. `Task Logs` → `/usage-logs/task` (module `task`).
- Per-child module gating is preserved by the existing filter chain (verified): `URL_TO_CONFIG_MAP` maps the four paths to `log`/`audit`/`midjourney`/`task` (`web/src/hooks/use-sidebar-config.ts:107-111`); `isNavItemVisible` shows a collapsible parent when `items.some(isModuleEnabled)` (`use-sidebar-config.ts:231-233`) and `filterNavItems` filters that collapsible's children individually (`:242-262`). So disabling `midjourney` hides only Drawing Logs, and the `Logs` parent disappears only when all four modules are off. No new gating code.
- Route guards unchanged: `web/src/routes/_authenticated/usage-logs/$section.tsx:55-67` validates `common|drawing|task`, `audit.tsx` is its own route; the sidebar only changes presentation.
- Test consequence: existing `sidebar-config.test.tsx` cases that treat Task Logs as active for `/usage-logs/drawing` must move to the new Drawing Logs child.

### Grouping the two code pages — separate pages preserved

- Requirement: Invitation codes and Redemption codes become one collapsible menu; their pages stay separate.
- Change: under group `admin`, wrap the two existing items in one collapsible (title e.g. `Codes` / `Invitation & Redemption`) with children pointing at the unchanged URLs `/invitation-codes` and `/redemption-codes`.
- Modules stay distinct: `invitation` and `redemption` (`use-sidebar-config.ts:120-121`), so admin/user visibility keeps working per child through the same `filterNavItems`/`isNavItemVisible` path as the Logs group.
- **Correctness note — role filtering does not recurse:** `useSidebarView` applies `requiredRole` only to top-level group items (`web/src/hooks/use-sidebar-view.ts:71-75`); children inside `items:` are filtered by module config only. Today `Invitation codes` carries `requiredRole: ROLE.ADMIN` (`use-sidebar-data.ts:152-157`) while `Redemption Codes` does not (`:158-162`), both protected by the `admin` group id gate (`use-sidebar-view.ts:71-73`) and each route's own guard (`invitation-codes/index.tsx:26-29`, `redemption-codes/index.tsx:33-36` — both redirect non-admins to `/403`). To keep the sidebar honest after grouping, put `requiredRole: ROLE.ADMIN` on the collapsible parent; do not rely on child-level `requiredRole` or on module config alone. Note that the `Redemption Codes` child's missing `requiredRole` becomes unreachable metadata once nested — set it on the parent and drop or keep the children's own `requiredRole` consistently.
- No route, module-id or page changes; only the nav tree shape.

### Renaming the Chat group and adding the landing page

- Group id `chat` and title `t('Chat')` at `use-sidebar-data.ts:58-59`. Renaming the **title** is safe; renaming the **id** is not free: `isSidebarModuleEnabled('chat', 'playground')` gates the playground route (`web/src/routes/_authenticated/playground/index.tsx:27`) and the module id is registered in `web/src/features/system-settings/maintenance/config.ts:59` and rendered by `sidebar-modules-section.tsx:91`. Keep the id `chat`, change only the title, or migrate all three together in one commit.
- Landing page data and connect flow already exist — **no server change**:
  - presets: `useChatPresets` (`web/src/features/chat/hooks/use-chat-presets.ts:66-82`), sourced from server `status.chats` (`:38-64`);
  - link shape: `ChatPreset { id, name, url, type }` (`web/src/features/chat/lib/chat-links.ts:23-27`), `detectChatLinkType` (`:77`), `chatLinkRequiresApiKey` (`:87-93`), `resolveChatUrl` (`:158-190`);
  - key acquisition: `fetchActiveChatKey` (`use-active-chat-key.ts`), used by `chat-presets-item.tsx:180-215` and by `/chat2link` (`web/src/routes/_authenticated/chat2link.tsx:30-70`).
- Card page: new route `web/src/routes/_authenticated/access/index.tsx` (or `/clients`) composing `Card` (`web/src/components/ui/card.tsx` exists) with the same handler as `chat-presets-item.tsx:180-215`: `web` presets navigate to `/chat/$chatId`, others require the active key then `window.open(resolveChatUrl(...), '_blank', 'noopener')`. Reuse `CopyButton` (`web/src/components/copy-button.tsx` exists) for the resolved URL when the admin wants to copy instead of open.
- Sidebar entry: point the renamed group's first item at the landing page and keep `Playground` as a sibling.

### Discrepancies / decisions to confirm

- `ChatPreset` carries only `id/name/url/type` — there is **no icon, description or protocol field**. Cards with icons/descriptions require either a config schema extension (`status.chats` parsing in `chat-links.ts:118-140`) or a client-side static map. This is the only place where a server/config change may be needed; flag before designing the card visuals.
- `fluent` presets are filtered out of the sidebar (`chat-presets-item.tsx:174-177`); decide whether the landing page shows them.
- `/chat2link` currently redirects to the first `web` preset; the new landing page must not duplicate that redirect behaviour.

### Tests

- `web/src/hooks/__tests__/sidebar-config.test.tsx` already asserts role/module filtering (`:267-312`) — extend it:
  1. `Logs` renders as one collapsible with exactly four children (Usage, Audit, Drawing, Task);
  2. disabling `midjourney` hides Drawing Logs but keeps the parent and the other three;
  3. disabling all four hides the parent;
  4. `/usage-logs/drawing` marks **Drawing Logs** active (not Task Logs);
  5. `Codes` renders as one collapsible with the two children, and each still navigates to its own page;
  6. non-admin role does not see the `Codes` parent.
- New `web/src/features/chat/components/__tests__/access-cards.test.tsx` (module-scoped directory per `web/AGENTS.md` 3.14): web preset navigates; key-required preset fetches the key and opens the resolved URL; key fetch failure surfaces `handleServerError` and does not open a window; `fluent` hidden per decision.
- i18n: `Access configuration`, `Logs`, `Drawing Logs`, `Codes` (or the chosen label), card labels and any new hint text added to all seven locale files (`web/src/i18n/locales/*.json`), following the `i18n-translate` skill. `Drawing Logs` already exists as a key (`section-registry.tsx:33`); reuse it rather than inventing a second label.

Files owned: `web/src/hooks/use-sidebar-data.ts`, `web/src/routes/_authenticated/access/index.tsx`, `web/src/features/chat/components/access-cards.tsx`, `web/src/features/chat/components/__tests__/access-cards.test.tsx`, `web/src/hooks/__tests__/sidebar-config.test.tsx`, 7 locale files. No changes under `web/src/features/usage-logs/`, `web/src/features/invitation-codes/`, `web/src/features/redemption-codes/`, or the `usage-logs` routes — pages and guards are untouched.

---

## Cross-cutting: commit separation (root owns sequencing)

| # | Requirement | Suggested commit scope |
| --- | --- | --- |
| 1 | Enter-in-priority-opens-details regression | `channels-table.tsx` + `channel-details-entry.test.tsx` (isolated bugfix, no layout churn) |
| 2 | Card layout (Priority/Weight row, compact response, Used row, groups removed) | `channel-card.tsx` + card tests |
| 3 | Details group-accessible models | `channel-details-drawer.tsx` (+ section component) + drawer tests |
| 4 | Exact+compact token counts | `token-count.tsx` + test, then call sites (can split call sites per surface) |
| 5 | Users Used balance column | `model/user.go` (sort key) + users columns/table/tests — **backend change, review separately** |
| 6 | Sidebar Logs fold (4 children) | `use-sidebar-data.ts` + `sidebar-config.test.tsx` + `Logs`/`Drawing Logs` locale keys |
| 7 | Codes group (2 children) | `use-sidebar-data.ts` + `sidebar-config.test.tsx` + `Codes` locale key — independent of item 6, keep separate |
| 8 | Access configuration rename + landing page | route, cards component, tests, locales |

Each commit must run its own affected checks (`bun run lint`, `bun check -b`, affected vitest files; `gofmt`/`go build` for item 5) so a failing item does not block the rest.

## Server-change summary (only two items need backend work)

1. **R4 (blocking)**: add `used_quota` to `model/user.go:22-29` `userSortColumns` — otherwise the new sortable column silently no-ops. No schema change; ORDER BY only; report the missing index rather than adding one.
2. **R5 (conditional)**: only if client cards need icons/descriptions — extend the `status.chats` preset schema. Not needed for click-to-connect itself.

Everything else (card layout, drawer group models, token formatting, sidebar Logs fold, Codes group, landing page) is frontend-only and reuses existing components, helpers and APIs. Neither sidebar grouping changes a URL, a route guard, or a `sidebar_modules` id.

## Open questions for the final plan

1. R3: adopt a shared `TokenCount` component (≥4 surfaces) or keep per-site composition? The reuse rule requires the capability-gap justification either way.
2. R4: after splitting, does the Available Balance column keep the `QuotaDetailsPopover` (showing used again) or become plain remaining?
3. R5: rename only the group title (keep id `chat`) or migrate the module id through `sidebar-modules` config and route gates?
4. R5: does the landing page show `fluent` presets, and do cards need icons/descriptions (server schema extension)?
5. R5: should the Logs collapsible live in the `general` group (replacing three items) or become its own top-level group? Same question for `Codes` inside `admin`.
6. R5: label for the codes group — `Codes`, `Invitation & Redemption`, or reuse `Redemption Codes`; and does the group belong above or below `Users`?
