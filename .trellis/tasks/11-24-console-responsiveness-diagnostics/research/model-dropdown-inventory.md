# Model-selection display ordering and direct key import — source inventory

## Status and exact boundary

- Owner: p5. Active task: `.trellis/tasks/11-24-console-responsiveness-diagnostics`.
- **Only this research file is owned/written.** Production/test/shared utility ownership is not yet assigned; the slices below require root's exact-path ACK. No tests/builds/typechecks/formatter/browser/DB probes or Git/index operations were run. R24 `schema-claim-contract.md` remains frozen and untouched.
- Read the parent PRD/design/implementation plan, `web/AGENTS.md`, project `shadcn-ui` skill/components.json and `.trellis/spec/frontend/model-selection.md`. Read actual selector implementations, their call sites and existing regression fixtures. Key-import review also consulted the local OWASP Authentication and Session Management guidance; this is not a complete security assessment or ASVS compliance claim.
- User requirement: natural A–Z **display options everywhere model selection is offered**, not only CC Switch. Preserve current selection, grouping, search, permission/source scope and stored catalog/manual order. The keys page also needs a directly visible import-config icon for the existing CC Switch action rather than requiring the secondary menu.

## 1. Existing reusable ordering and component behavior

### Already correct shared model selectors

`web/src/components/model-group-selector.tsx:79–90` already defines:

- `Intl.Collator('en', { numeric: true, sensitivity: 'base' })`.
- Display-label comparison followed by lexical **raw model ID** tie-breaking.
- `ModelGroupSelector:637` sorts a copied model array before filtering.
- `ModelSelector:214–232` groups into newly allocated arrays, sorts each category's options and retains category insertion order. Both mobile Drawer and desktop Popover render those same sorted candidates. Model/Group selections and the group/channel navigation lists are not re-sorted or reset.

Actual production coverage:

- Playground: `features/playground/components/input/playground-input-controls.tsx:82` uses `ModelGroupSelector`; `hooks/use-playground-options.ts` supplies group- or authorized-channel-specific models. **No ordering fix needed in its API/query/state layer.** Do not sort source models there: default selection currently uses source order.
- Fingerprint: `features/service-status/fingerprint.tsx:494` uses `ModelSelector`. Its channel-model source deliberately deduplicates without changing alias text and repairs only a model no longer served. **No ordering fix needed**, and no duplicate pM diagnostic/source tests are requested.

The comparator is **private**, not an exported shared utility. Repository source search found no equivalent exported deterministic model-option comparator in `web/src/lib/`. Existing `features/pricing/lib/filters.ts:195` `sortModels` is a *pricing-list* helper supporting selectable price orders; it is not suitable as a dropdown utility or a reason to alter pricing/ranking defaults.

### Generic primitives intentionally preserve caller order

- `components/ui/combobox.tsx:101–176` passes `props.options` directly into Base UI. No alphabetical sort occurs. Selected value lookup is independent of the options' order.
- `components/ui/combobox-input.tsx:91–106` filters the given options without sorting; used by editable mapping/model-value rows.
- `components/multi-select.tsx:186–195` builds display items from options, then appends selected-only/custom values. Its controlled `value={props.selected}` and selected chips preserve selection order. Sorting only the provided options does **not** sort appended selected-only values.
- Do **not** globally sort Combobox, Select, MultiSelect, generic plugin enums or DataTable menus. They also render Auto failover priority, groups, units, protocols, plugin order and manual business order. Those sequences are not model names and must not change.
- `components/ai-elements/prompt-input.tsx:1207–1264` exports `PromptInputModelSelect*` wrappers around generic Select. No production consumer was found outside that defining file; no currently visible model list to fix there.

## 2. All actual dropdown/autocomplete bypasses found

Paths in this table are relative to `web/src/`. Lines refer to inspected source and can move during peer integration. Every row was read at its actual model option construction/control, not inferred from a filename containing “model”.

| Surface | Exact source/control | Current order / narrow correction |
| --- | --- | --- |
| CC Switch primary + Haiku/Sonnet/Opus choices for Claude; primary for Codex/Gemini | `features/keys/components/dialogs/cc-switch-dialog.tsx:112–115,203–220` — `modelOptions` -> shared `Combobox` | Maps `/api/user/models` response directly; **unsorted**. Sort a copied display option array with the existing fixed-locale policy. Do not reorder query data, reset `models` or change selected model/app while sorting. |
| API-key Model Limits | `features/keys/components/api-keys-mutate-drawer.tsx:182,833–850` — `models.map` -> `MultiSelect` | **Unsorted** user models; selected-only historical/custom model limits are appended by MultiSelect. Need display ordering covering the complete offered item set while retaining form/selected value order and empty=all semantics. Group/Auto-order controls in this drawer remain untouched. |
| Channel main Models dropdown | `features/channels/components/drawers/channel-mutate-drawer.tsx:1165–1188,3661–3672` — catalog + currentModelsArray + plugin extension Set -> `MultiSelect` | **Unsorted insertion order**. Sort only display candidates, preserve redirects/hints/icons, current catalog and selected/chip order. Do not sort `currentModelsArray`, discovered source arrays, parsing helpers or payloads. |
| Channel Model Visibility by Group | Same drawer `:4045–4057` — `currentModelsArray.map` -> `MultiSelect` | **Unsorted** and bypasses `modelOptions`; fixing only the main Models field misses this one. Each group's selected subset, empty=all and existing group ordering remain unchanged. |
| Tag edit “Add from available models” | `features/channels/components/dialogs/edit-tag-dialog.tsx:90–91,302–316` — `availableModels.map` -> `Combobox` | **Unsorted** global model API order; sorted display options only. Preserve selected badges and explicit add/save behavior. `tag-batch-edit-dialog.tsx` is different: its models field is a textarea, not a dropdown; its MultiSelect is groups only. |
| Multi-key Allowed Models editor | `features/channels/components/dialogs/multi-key-models-editor.tsx:103–109,166–178` — channel+upstream Set -> `MultiSelect` | **Unsorted**. Include selected-only values in display ordering without sorting `models` saved allowlist. Preserve stable allocation identity/revision, explicit empty allowlist deny-all and request-generation conflict guards. Upstream button list `:188–216` is another presentation surface, not the dropdown. |
| Redirect/mapping request and upstream model autocomplete | `features/channels/components/model-mapping-editor.tsx:116–130,472–497` — source/target option maps -> `ComboboxInput` | Both **unsorted**. Sort copied suggestion arrays, **not** mapping rows or serialized mapping keys. The component is reused by `model-redirect-panel.tsx:104–105` and the channel drawer `:2904–2905,5733–5738`, so fix in the editor once, not every caller. |
| Request-plugin selected-model scope | `features/task-plugins/components/request-plugin-instance-editor.tsx:536–557` — props.models + selected models Set -> `MultiSelect` | **Unsorted**, provider icons retained. Do not reorder group scope cards, explicit selected values or plugin processing order. Existing plugin-owned fixed enum schemas are not automatically model catalogs. |
| Per-model plugin configuration rows (shared by channel/plugin forms) | `features/task-plugins/components/request-plugin-config-model-values.tsx:76–89,147–160` — unique props.models + icons, taken-model exclusion -> `ComboboxInput` | **Unsorted**. Sort display suggestions before taken-model filtering; keep actual row sequence, per-model values, custom input and duplicate validation. Shared via `channels/components/drawers/channel-plugin-config-fields.tsx:77` and plugin config form. |
| Metadata rule's matched-model pricing selection | `features/models/components/drawers/model-mutate-drawer.tsx:677–695` — `savedModel.matched_models.map` -> `Combobox` | **Unsorted** local options. Sort copied suggestions only; preserve `pricingName`, unsaved-pricing confirmation and matched model/catalog source order. Other Comboboxes in this file are vendor/endpoint templates, not models. |

This is **10 surface families / 9 production files** with actual model-dropdown options outside the already-correct selector component. A CCS-only patch would leave nine families unchanged. No source edit is authorized by this inventory.

## 3. Non-dropdown model-selection lists: do not mistake them for covered dropdowns

The following adjacent pick lists are real selection surfaces, but use checkbox/button/table layouts rather than dropdowns. Root should explicitly assign/confirm their presentation-only scope rather than silently declare “all model lists sorted” after fixing section 2.

| Surface | Readback | Ordering / action boundary |
| --- | --- | --- |
| Configure Models, Fetch Models, upstream update Add/Remove, mapping batch picker, channel inline discovery | Shared `features/channels/components/upstream-model-selection.tsx:46–52,197–252,344–415`; wrappers `dialogs/configure-models-dialog.tsx`, `fetch-models-dialog.tsx`, `upstream-update-dialog.tsx`, `model-mapping-batch-dialog.tsx`, drawer `:3829` | Only **category names** are sorted; `categorizeModels` (`lib/model-categories.ts:26`) preserves incoming model order. Individual candidates remain unsorted within each category. A central presentation sort in `ModelCategory`/new category array covers all wrappers. Do not sort the categorized source lists used for bulk `onChange`, initialModels, or save payloads just to display A–Z. Preserve tab/category order and manual selection order. |
| Multi-key “Models this key serves upstream” buttons | `dialogs/multi-key-models-editor.tsx:188–216` | Source order; sort a rendering copy if included. Do not change the discovery response, selected allowlist or committed `channel_models`. |
| Channel test list/table | `dialogs/channel-test-dialog.tsx:629–675` | Source catalog order, then key-scope/search filters, then table data; not a model dropdown (its Comboboxes are **Key** and **Endpoint**). If owner changes display order, sort the rendered table list, not selected/batch queue/catalog. **pM diagnostics ownership; do not duplicate or edit here.** |
| Ollama local models | `dialogs/ollama-models-dialog.tsx:92–96,517+`; `lib/ollama-utils.ts:95` | Filter preserves live/provider order; normalization also has no sort. Optional presentation-only sort before rendering, not initial selection/replace payload or pull/delete state. No dedicated existing Ollama UI fixture found. |
| Metadata sync wizard | `features/models/components/dialogs/sync-wizard-dialog.tsx:159–190` | Current semantic grouping is syncable first, skipped after, with pagination. If extended to this list, sort **within** those groups and before pagination, not selection/preview/CAS record_version arrays. Keep eligibility and hidden-selection counters. |
| Missing Models dialog | `features/models/components/dialogs/missing-models-dialog.tsx:72,90–115` | Local response filter/pagination, unsorted. Broader list request could sort a display copy before pagination; not a dropdown. |

Read-only lists (model connection badges, inference status snapshots, plugin metadata model summaries) and pricing/ranking tables with their own user-selected/metric sorting are not dropdown defects. **p6 ranking order/cache work remains separate**. No ranking/pricing policy or protocol/usage fields need to change for this request.

## 4. Minimal reuse/design recommendation (needs file-boundary ACK)

1. Reuse the existing exact policy from `model-group-selector.tsx`, not a new locale mapping or an arbitrary `.sort()` at each call site. The clean small reusable boundary would be one frontend-only model-display utility (for example `lib/model-options.ts`, path **not yet authorized**) exporting the string/label+ID comparator and a copied sort. Move the current private comparator there without changing shared selector behavior. Avoid importing the entire React selector module from otherwise lightweight feature utilities just to obtain a comparator.
2. Keep generic Combobox/Select ordering unchanged. At single-model callers sort only an allocated display array.
3. MultiSelect needs particular care: its appended selected-only/custom items must be included in display sorting, while `value`, chips and emitted selected arrays retain original order. A compatible **generic opt-in comparator prop** (default absent/current behavior) can express that at the existing MultiSelect item-construction boundary. The model comparator is supplied only by actual model callers. Alternatively each model caller must merge all selected-only candidates into its sorted options, leaving the generic component untouched; incomplete merges must not leave a tail of unsorted visible choices. Root should choose one consistent approach, not globally sort all MultiSelect consumers.
4. Every list/filter rerender must keep current selections, draft rows, groups, disabled entries, custom values, scope denials and current placeholder/empty/error/keyboard behavior. Sorting must not call onChange or choose the alphabetically first model by itself. The existing API/query return order remains unchanged (e.g. overview request examples currently use `modelsQuery.data[0]`).
5. Preserve group/category order and sort only options within each category where grouping exists. Keep natural numbers (`model-2` before `model-10`) and raw-ID lexical ties consistent with the already documented selector contract. Do not rewrite stored comma-separated model strings, mappings, allowlists, channel catalog union order or Auto routing priorities.

## 5. Direct visible keys-page import icon: exact existing action and secret boundary

### Current path

`features/keys/components/data-table-row-actions.tsx:251–271` currently hides CC Switch in `DataTableRowActionMenu`. Its click does:

1. `await resolveRealKey(apiKey.id)`; stop on null/error.
2. `setResolvedKey(realKey)`, `setCurrentRow(apiKey)`, `setOpen('cc-switch')`.
3. `api-keys-dialogs.tsx:36–41` renders the **existing** `CCSwitchDialog` using `resolvedKey`.
4. Dialog submission, not row render, constructs the existing `ccswitch://v1/import?...` provider URI; Claude/Gemini use base endpoint, Codex adds `/v1`; chosen primary/family model fields are carried. Only explicit `Open CC Switch` launches it. Do not print or persist that URI: it includes the API credential by protocol design.

`api-keys-columns.tsx:230–232` and mobile `api-keys-table.tsx:175` already reuse this same `DataTableRowActions`. One directly visible **Tooltip + ghost icon-sm Button**, alongside status/edit and before the existing overflow menu, exposes the import action for desktop/mobile without changing table/card components. `ArrowRightLeft` already represents CC Switch and is imported. Reuse `t('Import to CC Switch')` for accessible name/tooltip (already a seven-locale/static key) instead of inventing new copy or a second dialog.

Keep the action handler shared if overflow entry is retained; do not duplicate two credential-fetch implementations. Use the existing loading state (`loadingKeys[id]` / `isRealKeyLoading`) to disable repeat invocation/show progress. No URI/href, key text, credential-bearing data attribute, tooltip, DOM output or eager resolve on hover/mount. A direct icon click should only resolve/open the dialog, not auto-launch an external app.

### Existing server/provider route, not a new credential bypass

- Provider `api-keys-provider.tsx:100–147` resolves real keys through `fetchTokenKey`, caches in component memory, deduplicates in-flight requests and rejects stale reveal results using per-token rotation epochs. Do not replace it with `row.key`, which is a masked listing field.
- `features/keys/api.ts:124–130`: POST `/api/token/:id/key` through the existing authenticated API instance.
- Router `router/api-router.go:307–321`: UserAuth, TokenOperationAudit, CriticalRateLimit, DisableCache; PAT route map retains `api_key:reveal` scope (`middleware/access_token_routes.go:233`). Handler `controller/token.go:193–211` resolves `GetTokenByIds(id, currentUserId)` and marks secret-free audit success. Keep all of these server checks; frontend visibility is not authorization.
- No backend/auth policy change is necessary to expose the existing action. Do not weaken refresh/CSRF/scope/ownership/rate/no-store gates, add an unprotected export, log a real key or put credentials in browser persistent storage. Cached reveal/account-lifecycle behavior is existing wider scope, not newly certified here. The custom import protocol intentionally passes a credential to the explicitly selected local app; the new icon must not broaden recipients/transport.
- Relevant guidance read: OWASP Authentication Cheat Sheet (sensitive operations/server enforcement) and Session Management Cheat Sheet (no sensitive credentials in persistent web storage, URLs/logs/cache). Existing custom-URI credential transfer is retained, not claimed compliant with every ASVS control. Regression execution is still hosted-only.

### Exact smallest production/test slice to assign

- Production: `web/src/features/keys/components/data-table-row-actions.tsx` only for the visible import icon/reused action.
- Existing regression: `web/src/features/keys/components/__tests__/api-key-listing.test.tsx` already renders real row actions in the table/mobile fixture and proves no full key fetch on status/menu open plus explicit-copy success/denial. Extend it for icon visible before menu, accessible keyboard click, **zero fetch on render/hover**, same `/key` on click only, failure leaves dialog/external launch closed, success opens current-key CC dialog, repeated click/loading and no secret text/URI leak. No new credentials or live calls.
- Existing dialog regression: `dialogs/__tests__/cc-switch-dialog.test.tsx` covers app-specific model controls, portal clipping, keyboard/filter/Escape, empty/open-query update and provider name edit. Extend for sorted choices/unchanged selected value; no need for a new CC dialog implementation.
- Existing provider regression: `components/__tests__/api-key-reveal-cache.test.tsx` protects rotation-inflight credential invalidation; preserve it rather than changing provider behavior for an icon.

## 6. Existing meaningful ordering/selection regressions and exact gaps

| Existing test path under `web/src/` | Already protects / minimal extension |
| --- | --- |
| `components/model-group-selector/__tests__/sorting.test.tsx` | Desktop/mobile numeric sorting, raw-ID ties, frozen caller array, unchanged selection/callback, filter order and group/channel navigation. Preserve these while extracting/reusing comparator. Add explicit grouped-category preservation if touched. |
| `features/keys/components/dialogs/__tests__/cc-switch-dialog.test.tsx` | Real Combobox behavior for all apps + asynchronous options; add scrambled natural-name fixture and assert display order/selection preservation. |
| `features/keys/components/__tests__/api-keys-mutate-drawer.test.tsx` | Real drawer fixtures, Auto priority selection/mode, current-target reset/expiry and payloads. Add a focused model-limits dropdown assertion preserving selected/payload order; do not alter Auto expectations. |
| `features/channels/components/__tests__/channel-configuration.test.tsx` | Main model source/manual drafts/discovery, plugin selection Apply/Cancel, provider switching, scope/redirect/source changes and per-group visibility save. Extend existing relevant cases rather than copying a fixture for each drawer call site. |
| `features/channels/components/__tests__/multi-key-key-scope.test.tsx` | Explicit empty allowlist, stable identity, selected per-key scope, read-only discovery + explicit Save + catalog union. Extend ordering assertion without changing expected stored model order. Existing p2/pH ownership requires ACK. |
| `features/channels/components/__tests__/model-mapping-editor.test.tsx` | Real source/target suggestions and focus, custom drafts, language changes, no spurious edits; add sorted suggestions while keeping row/JSON order. |
| `features/channels/components/__tests__/upstream-model-selection.test.tsx` | Categories, removed-model persistence, search/select-all, save selection order, fetched empty/nonempty; suitable central optional non-dropdown list order coverage. |
| `features/channels/components/__tests__/channel-plugin-config-fields.test.tsx:117–143` | IMPORTANT current assertion explicitly expects input-order options `[gpt-5, claude-opus-4-7, custom-model]` and icons by numeric index. Sorting changes this expected display; update to natural order and identify icons by model option identity, not their old position. Keep saved per-model value/row/duplicate coverage. This tests shared RequestPluginConfigModelValues through the actual host form. |
| `features/task-plugins/__tests__/request-plugin-config-form.test.tsx` | Real instance editor scope/form/save fixture; extend chosen-model scope popup ordering, keeping selected row/group values. No plugin runtime or metadata contract changes. |
| `features/models/__tests__/model-listing.test.tsx` | Metadata create/edit + pricing drawer interactions; can extend with matched_models selection fixture to prove sorted choices and preserved dirty-price confirmation. Do not touch pricing math. |
| `components/ui/__tests__/combobox.test.tsx` | Generic search/selection/focus/icons; preserve caller-order behavior rather than turning it into a globally sorted control. If an opt-in MultiSelect comparator is chosen, a compact behavioral fixture can extend the existing shared component test scope with root approval; no new test scatter by default. |

No current test execution or coverage pass is claimed. Root must include the exact changed test paths in hosted checks; the current workflow has broad frontend checks but its selected-file list must be read/reconciled at final capture. No local i18n sync/format/test may be run under this task's explicit restriction.

## 7. Proposed independent implementation boundaries for root ACK

1. **Shared policy:** private comparator extraction/reuse + selected-only MultiSelect display strategy + existing shared sorting regression. Exact utility/new-file permission and MultiSelect comparator API must be chosen before edits.
2. **Keys:** existing CC dialog sorting + model-limits display sorting + row import icon + existing keys tests. Provider/API/auth/query behavior stays unchanged.
3. **Channels:** main/per-group dropdowns, tag autocomplete, multi-key editor, mapping suggestions; shared UpstreamModelSelection presentation if root includes checkbox lists. Existing source/scope/selection/save contracts and peer owners remain authoritative.
4. **Plugin/model metadata bypasses:** request-plugin scope/per-model rows, matched-model pricing combobox, their existing host tests. No plugin description/price/runtime/protocol work.
5. **Reserved peers:** pM channel-test list/diagnostic tests and p6 ranking remain theirs. They can receive an exact comparator/display-only contract if that broader list requirement is confirmed. Do not start an independent duplicate patch.

This inventory is ready for root planning/ownership acknowledgment; it is not a statement that all lists are fixed or any executable validation passed.
