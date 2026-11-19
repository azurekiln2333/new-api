# Channel endpoint setting, card keyboard bug, live metrics UI

Research only. No product code changed. No implementation authorised.

Scope: (1) per-channel upstream endpoint declaration UI/precedence, (2) Priority inline-input Enter regression, (3) list + details live-concurrency UI with one shared cache and batch page subscriptions. Backend metrics transport is covered by the p9 task and is not re-researched here.

---

## 1. Channel endpoint setting: existing mechanism and precedence

### 1.1 The conversion machinery already exists

- Chat client -> Responses upstream: `relay/compatible_handler.go:79-92` calls `textRequestViaResponses` when
  `service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName)`.
- Claude client -> Responses upstream: `relay/claude_handler.go:77-88`, same predicate, same function.
- Conversion body: `relay/chat_completions_via_responses.go:76-121` -> `service.ConvertRequest(c, info, types.RelayFormatOpenAIResponses, request)`;
  converter IDs live in `relaykit/relayconvert/text_converter_registry.go:116` (chat->responses) and `:167` (claude->responses).
- Response return path: `relay/channel/openai/chat_via_responses.go:23` (JSON), `:58` (buffered SSE->JSON), `:172` (SSE->SSE);
  usage normalisation and fallback estimation at `:157-166`, `:300-320`.
- Playground inherits all of it: `router/relay-router.go:67-71` -> `controller/playground.go:47-56` -> `Relay(c, RelayFormatOpenAI)` ->
  same `compatible_handler` branch. No playground-specific change needed.

### 1.2 Today the declaration is global, not per channel

- `setting/model_setting/global.go:13-36`: `ChatCompletionsToResponsesPolicy{Enabled, AllChannels, ChannelIDs, ChannelTypes, ModelPatterns}`;
  `IsChannelEnabled` matches channel id or channel type exactly.
- `service/openai_chat_responses_mode.go:40-52`: host-side matcher, regex cache, `ShouldChatCompletionsUseResponsesGlobal`.
- Defaults: `setting/model_setting/global.go:61-64` (`Enabled: false`, `AllChannels: true`).

### 1.3 "Supported upstream endpoint" today is display metadata only

- `common/endpoint_type.go:6-69` `GetEndpointTypesByChannelType`: derived from channel type + model name
  (`IsOpenAIResponseOnlyModel`, `common/model.go:35-42`). OpenAI-type channels default to `EndpointTypeOpenAI`.
- Consumed only by pricing/model listing: `model/pricing.go:116-140`, `:369`; surfaced by `controller/pricing.go:73`.
- Routing ignores it for ordinary channels: `dto.FilterRequestPath` applies only to AdvancedCustom channels,
  `model/channel_constraint.go:135-142`. Therefore declaring a normal OpenAI channel "Responses-only" today has **no routing effect**.

### 1.4 AdvancedCustom is not the reuse target

- AC converters are path-bound: `relaykit/dto/channel_settings.go:207-230` + `validateAdvancedCustomConverterPath` (`:492-520`).
  `openai_chat_completions_to_openai_responses` is valid only on `incoming_path=/v1/chat/completions` of an AC route.
- AC requires routes/models/auth/upstream_path maintenance and a different channel type; over-scoped for one switch.

### 1.5 Minimal design (for the final plan; not implemented)

- Add `ChannelSettings.UpstreamChatProtocol string` (`""|"chat"|"responses"`) in `relaykit/dto/channel_settings.go`
  (same file as `MaxConcurrentRequests` `:14`, `ValidateHTTPTransport` `:97`).
- Validation in `model/channel.go:1324 ValidateSettings` (same place as `ValidateHTTPTransport` `:1335`, `ValidateRequestLimits` `:1338`,
  `ValidateToolLossPolicy` `:1348`):
  - reject on AdvancedCustom types (routes already decide protocol);
  - reject together with `pass_through_body_enabled` (raw chat JSON would be posted to `/v1/responses`);
  - reject values outside the three literals.
- Resolution: extend `service/openai_chat_responses_mode.go` with
  `ShouldChatCompletionsUseResponses(info)` = channel setting OR existing global policy; replace the two call sites
  (`relay/compatible_handler.go:82`, `relay/claude_handler.go:79`). Global policy stays as fallback for backwards compatibility.
- Precedence (explicit, must be documented in the setting help text):
  1. `pass_through_body_enabled` / global `PassThroughRequestEnabled` -> no conversion (existing order at `compatible_handler.go:79-81`);
     so the setting is rejected at save time rather than silently overridden.
  2. channel `responses` -> convert.
  3. global policy -> convert.
  4. default -> native chat upstream.
- Inbound vs upstream distinction must be stated in UI copy: the client protocol (`/pg/chat/completions`, `/v1/messages`) is never rewritten;
  only the outbound protocol changes. A Responses-only channel must not reject chat inbound.
- `force_format` is orthogonal: it only reshapes the response body (`relay/channel/openai/relay-openai.go:304`).

### 1.6 UI location

- Schema/serialisation: `web/src/features/channels/lib/channel-form.ts:284-296` (schema), `:508-517` (defaults), `:596-606` (parse),
  `:756-766` (serialise).
- Control: `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx:2193-2214` is the existing `force_format` Switch block —
  same region for a three-state Select. i18n keys go to all seven locale files (`web/src/i18n/locales/*.json`), e.g. existing
  `"Force format response to OpenAI standard (OpenAI channel only)"` at `web/src/i18n/locales/en.json:3091`.

### 1.7 Compatibility and DB scope

- `Channel.Setting` is `*string` with `gorm:"type:text"` (`model/channel.go:55`): a new JSON field needs **no migration and no new column**.
- Backwards compatibility: absent key parses to `""` -> today's behaviour. Old JSON keeps parsing because unknown keys are ignored.
- DB verification therefore reduces to: upgrade parse of representative old `setting` JSON, save/reload idempotency, and rejection cases.
  No three-database `AutoMigrate` matrix is warranted unless a real column is added (not proposed).
- Billing: conversion reuses `PostTextConsumeQuota` (`relay/chat_completions_via_responses.go:93-121`) and existing usage fallbacks;
  `max_tokens` is already bounded at ingress by `relay/helper/valid_request.go:122-130` and read for pre-consume at
  `relay/request_billing.go:28-33`. No new multiplier, no image/task settlement path touched.

---

## 2. Priority inline-input Enter opens channel details: exact regression

### 2.1 Cause

- Card body is an activation target: `web/src/features/channels/components/channel-card.tsx:117-121` sets
  `role={interactive ? 'button' : undefined}`, `tabIndex`, `data-activates-channel-details`.
- Keyboard handler lacks the click handler's guard: `web/src/features/channels/components/channels-table.tsx:443-456`
  ```ts
  const card = (event.target as HTMLElement).closest('[role=button][data-activates-channel-details]')
  if (!card) return
  event.preventDefault(); ... setOpen('channel-details')
  ```
  From any descendant (including the Priority input) `closest` resolves to the card, so Enter opens the drawer.
- The click handler in the same file has the correct guard: `:422-431`
  ```ts
  const interactiveTarget = target.closest(ROW_INTERACTIVE_SELECTOR)   // includes `input`
  if (interactiveTarget && !interactiveTarget.hasAttribute('data-activates-channel-details')) return
  ```
  Selector definition: `:114-115`.
- Trigger: `web/src/features/channels/components/numeric-spinner-input.tsx:121-127`
  ```ts
  if (e.key === 'Enter') { e.preventDefault(); inputRef.current?.blur() }
  ```
  `preventDefault()` does not stop propagation. Blur commits (`commitValue` `:99-112`) -> `onChange` -> update mutation, and the same
  keydown keeps bubbling to the region handler -> drawer opens. Both effects fire: priority saved **and** details opened.
- The Priority cell is rendered into the card via `channel-card.tsx:191` (`priorityCell`), column `channels-columns.tsx:1221`
  (`accessorKey: 'priority'`).

### 2.2 Minimal fix

In `handleCardKeyDown`, apply the same predicate before resolving the card:
```ts
const interactiveTarget = (event.target as HTMLElement).closest(ROW_INTERACTIVE_SELECTOR)
if (interactiveTarget && !interactiveTarget.hasAttribute('data-activates-channel-details')) return
```
The card itself carries `data-activates-channel-details`, so focusing the card body still opens. Optional hardening (not the sole fix):
`numeric-spinner-input.tsx` `handleKeyDown` adds `e.stopPropagation()` for Enter.

### 2.3 Required regression tests (extend the existing file, do not add a new one)

`web/src/features/channels/components/__tests__/channel-details-entry.test.tsx` currently covers card click (`:190-206`),
inner-button click (`:208-220`), and name-button keyboard (`:222-234`) — it has **no** case for a keyboard event originating inside the card, which is
why this shipped. Add:

1. Focus the card's Priority input, type a value, press `Enter`: assert the update request fired **and**
   `Dialog state` does not contain `channel-details`.
2. Focus the card body, press `Enter`: assert details still open (guards the new condition against over-filtering).

Existing key-event patterns to copy: `web/src/features/channels/components/__tests__/priority-order.test.tsx:265-336`.

Files owned: `web/src/features/channels/components/channels-table.tsx`,
`web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`;
plus `numeric-spinner-input.tsx` (+ its cell/column test) only if the hardening is taken.

---

## 3. Live concurrency on list cards AND details, one shared cache, batch subscriptions

### 3.1 Current data path (historical aggregates, no polling)

- Batch list: `useChannelUsageSummaries` (`web/src/features/channels/hooks/use-channel-usage.ts:32-56`) -> `GET /api/channel/usage?ids=`
  (`web/src/features/channels/api.ts:98-111`), deduped+sorted ids, `placeholderData` keep-previous, `meta.errorToast:false`.
- Distribution: `web/src/features/channels/components/channels-table.tsx:356-357` -> `ChannelUsageContext.Provider` `:583`;
  consumer `channel-usage-context.tsx:35-47`; card slots rendered at `channel-card.tsx:170-206` (`Used / Remaining`, `Used tokens`).
- Details: `useChannelUsageDetail(channelId)` (`use-channel-usage.ts:59-68`) -> `GET /api/channel/<id>/usage`;
  called from `web/src/features/channels/components/drawers/channel-details-drawer.tsx:93-94`, gated by `props.open` (`:91`),
  rendered by `channel-usage-summary.tsx:78-98`, `:203-204`.
- Source data is consume-log aggregates: `model/channel_usage.go:41-88`, controller `controller/channel_usage.go:18`, `:59`.
  **Neither hook polls**: no `refetchInterval` anywhere in `web/src/features/channels`.
- Query keys: `web/src/features/channels/lib/channel-actions.ts:52-70` (`usage()`, `usageSummaries(ids)`, `usageDetail(id)`).

### 3.2 Where live concurrency actually exists

- `service/channel_capacity.go:48-49` `localChannelCapacity{active map[int]int}` — per-process in-flight counts,
  incremented at `:157-163`, decremented at `:125-129`.
- Redis: sorted set `new-api:channel-capacity:<id>` (`:174`), membership cardinality is the in-flight count, lease TTL 30s (`:22`),
  acquire/renew Lua scripts `:28-45`.
- **Only maintained when `max_concurrent_requests > 0`**: `:150-154` returns a lease without counting when unset.
- No HTTP endpoint exposes it today. Backend transport/semantics are owned by the p9 task — not re-researched here.
  This section covers only the UI contract and cache wiring.

### 3.3 UI integration contract (single cache)

- One query key family, reused by list and drawer so the drawer is a cache hit, not a second source:
  ```
  channelsQueryKeys.concurrency()                    // ['channels','concurrency']
  channelsQueryKeys.concurrencySummaries(ids)        // [...,'summaries', sortedIds]
  channelsQueryKeys.concurrencyDetail(id)            // [...,'detail', id]
  ```
  Mirror the existing `usage()`/`usageSummaries`/`usageDetail` shape (`channel-actions.ts:63-67`).
- Batch subscription is one request per visible page: derive ids from `pageChannelIds` (already used at `channels-table.tsx:356`),
  dedupe+sort exactly like `use-channel-usage.ts:33-36` so an identical row set cannot trigger a second fetch.
- Detail must read the same cache: derive the drawer's data from `concurrencySummaries` when the id is present, and only fall back to
  `concurrencyDetail(id)` for ids outside the page set. `gcTime: 0` on the detail query is acceptable precisely because it is a fallback.
- Polling policy (copy the established pattern from `web/src/features/channels/components/dialogs/inference-status-dialog.tsx:109-112`):
  `refetchInterval: 5000`, `refetchIntervalInBackground: false`, `refetchOnWindowFocus: false`, `retry: false`,
  `placeholderData: keepPrevious`, `meta: { errorToast: false }`.
- Pause conditions: hidden tab (React Query default with `refetchIntervalInBackground:false`), no visible ids
  (`enabled: uniqueIds.length > 0`, as `use-channel-usage.ts:42`), drawer closed (`channelId === null`, as `channel-details-drawer.tsx:91`).
- Rendering: add a `Concurrency` slot to the card `<dl>` in `channel-card.tsx:170-206` and a line in `channel-usage-summary.tsx`
  so both surfaces show the same number from the same cache entry. Numbers go through `@/lib/format` `formatNumber` with
  `toIntlLocale` (`channel-usage-summary.tsx:34`, `:78-98`), per `web/AGENTS.md` 3.1.
- Availability: unavailable/unlimited channels must render as unavailable, not `0`. The drawer/detail must not imply cluster-wide truth when
  the counter is per-process.

### 3.4 Tests

- `web/src/features/channels/hooks/__tests__/use-channel-usage.test.tsx` already mocks per-URL responses and asserts key behaviour
  (`:93-99`, `:110-115`, `:126`). Extend it:
  1. one request for a duplicated/unordered id list;
  2. list and detail resolve from the same cache entry (assert a single GET; open the drawer without a second call);
  3. polling enabled at 5s and disabled in background (fake timers, no sleeps);
  4. drawer closed -> no detail request.
- `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`: card shows the concurrency slot; drawer shows the same value.
- No new test files; both files already own these responsibilities.

### 3.5 Open points for the final plan

- If the counter is per-process without Redis, label it (e.g. "this node") or return an explicit `scope` field; do not silently present it as global.
- Unlimited channels: hide the row rather than display `0 / ∞`.

---

## Files this research assigns (for the final plan, not yet authorised)

| Area | Files |
| --- | --- |
| Endpoint setting backend | `relaykit/dto/channel_settings.go`, `model/channel.go`, `service/openai_chat_responses_mode.go`, `relay/compatible_handler.go`, `relay/claude_handler.go` |
| Endpoint setting tests | `relay/chat_completions_via_responses_test.go`, `relaykit/dto/channel_settings_test.go` |
| Endpoint setting UI | `web/src/features/channels/lib/channel-form.ts`, `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx`, 7 locale files |
| Enter bug | `web/src/features/channels/components/channels-table.tsx`, `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx` |
| Metrics UI | `web/src/features/channels/hooks/use-channel-usage.ts`, `web/src/features/channels/api.ts`, `web/src/features/channels/lib/channel-actions.ts`, `web/src/features/channels/components/channels-table.tsx`, `web/src/features/channels/components/channel-card.tsx`, `web/src/features/channels/components/channel-usage-context.tsx`, `web/src/features/channels/components/drawers/channel-details-drawer.tsx`, `web/src/features/channels/components/drawers/channel-usage-summary.tsx`, `web/src/features/channels/hooks/__tests__/use-channel-usage.test.tsx` |
