# Model selector ordering

## Ranking viewer labels and privileged chart cache

`GET /api/rankings/model-usage` projects names on the server through
`model.BuildUserRankingView`. ADMIN and ROOT see full current names, a signed-in
ordinary user sees their own full name and partial names for others, and an
anonymous viewer sees `***`. Partial masking operates on Unicode code points:
one becomes `*`, two become first + `***`, and three or more become first +
`***` + last. Missing non-self names stay `***`; masked rows omit `user_id`.
Resolve names once for the bounded top-20 leaderboard, never fetch a full user
catalog or return hidden real-name metadata. Responses remain no-store.

The separate administrator dashboard user chart requires live ADMIN-or-higher
authorization. Its query key is `['dashboard', 'user-quota', timeRange, userId,
role]`, its API consumes AbortSignal, and it immediately hides cached chart data
after demotion/logout. A disabled query alone is insufficient because cached
results can remain available.

Existing instance-resource/router ranking tests cover role/self/anonymous,
Unicode masks and omitted IDs. Existing model-usage-drawer and dashboard chart
tests cover identity/role cache isolation and cancellation. Prefix QueryCache
lookups must explicitly pass `exact: false`; full-key lookups use `exact: true`.
Do not remask names in the frontend or infer administrative capability from a
chosen row filter.

## Model option order

`ModelGroupSelector` sorts a copy of model options by display name before
filtering. Use `Intl.Collator('en', { numeric: true, sensitivity: 'base' })`
and compare raw model IDs lexically when names collate equally. Fixed valid
locale makes the order deterministic across interface languages. The
standalone `ModelSelector` preserves category order and sorts each newly
allocated category list with the same comparator.

The shared `@/lib/model-options` exports `compareModelOptions({ label, value },
{ label, value })` and `compareModelNames(string, string)`. Model callers reuse
these helpers; generic selectors preserve their existing ordering by default.
`MultiSelect.compareOptions` is opt-in and sorts a fresh candidate list after
merging offered and selected-only values. Missing labels fall back to the raw ID.
Selected chips and emitted values keep controlled selection order; a create
candidate remains last. API-key model limits opt in, while group and Auto options
keep their business order. CC Switch sorts copied model lists and retains a valid
selection when the catalog refreshes.

The visible API-key row import button invokes the same explicit `resolveRealKey`
and CC Switch dialog path as the menu action. Rendering/hover never reveals a key;
pending reveal disables repeated activation. Credentials remain out of DOM and
browser storage; the existing external URI is constructed only by explicit dialog
submission. Provider identity changes must invalidate an older reveal result.

Never sort caller-owned arrays in place or change the selected model as a
side effect of display sorting. Desktop popover and mobile drawer use the
same sorted data. `model-group-selector/__tests__/sorting.test.tsx` covers
numeric names, equivalent names with distinct IDs, filtering, immutable
inputs and preserved selection in both viewport modes. The existing MultiSelect,
API-key limits/listing and CC Switch suites also cover selected-only values,
unchanged chip/payload order, default generic order, explicit reveal, stale results
and failure states. Future channel/group navigation must retain these invariants.

Mounted-query regressions must await the observable option list after
`QueryClient.setQueryData`: observer notifications are scheduled, so a synchronous
`act` around the cache mutation does not guarantee updated props have rendered.
Use `waitFor` with the complete expected list; retain source-array, chip and saved
payload assertions rather than accepting the stale subset.

Request-plugin model scope opts into the same display comparator, including saved
values absent from the offered catalog. Model-value autocomplete and matched
pricing-model choices sort newly allocated suggestions only. Empty model scope
continues to mean All models; an unfinished blank `modelValues` row is omitted by
the existing parser and produces `{}`, not an empty named map. Sorting must not
reorder model-value rows, emitted config, selected chips or dirty-pricing dialogs.

In forms with both group and model pickers, scope chip assertions to each
picker's own `combobox-chips` ancestor. A dialog-wide chip count cannot identify
which selection was preserved. Before a real Save click, an unresolved submit
regression may assert selected scope radios, exact group/model chips, displayed
configuration, visible errors and the associated native form's validity. Keep
the exactly-once callback and full ordered payload assertion; diagnostic
assertions do not establish the failure's cause or prove it fixed. Compare the
complete optional saved config object instead of dereferencing an optional map
or adding a non-null assertion solely to satisfy TypeScript.

Channel, per-group and per-Key model limits opt into `compareModelOptions` on the
existing `MultiSelect`. Tag model choices and both mapping autocomplete fields
sort newly mapped candidates only. Preserve channel catalogs, selected chips,
group order, mapping row/JSON order and saved allowlists. Per-Key All models and
explicit empty deny-all remain distinct; sorting must not change stable Key
identity/revision or discovery preview/apply guards. Existing channel
configuration, per-Key scope and mapping suites assert display order alongside
unchanged payload order and frozen input arrays.

## Playground channel visibility

`canSelectPlaygroundChannel(user)` requires both the administrator role and
`channel.read`; custom permissions alone never expose manual channels. Reuse
that predicate in the options hook and the config state owner. Query keys
include identity, role, permission result and group, and consume the query's
abort signal. A disabled query can retain cached data, so mask returned options
explicitly and never derive ordinary-user UI from that cache.

Pins are ephemeral and excluded from persisted config. Clear them synchronously
on identity or privilege changes, before rendering a selector or constructing a
request; reject ordinary-user pin assignment. An invalid pin belonging to the
same authorized administrator remains visibly unavailable rather than silently
changing their chosen routing. The server independently rejects ordinary-user
list/pin requests and checks group/model/key eligibility for administrators.

The existing playground options/state suites cover cached-channel hiding,
custom grants, account switches, demotion, no pin resurrection and omission
from the built request. `TestPlaygroundChannelNavigationAuthorizationAndRouting`
covers forged list/pin requests and metadata-free denials.

## Fingerprint and group model sources

### 1. Scope / Trigger

Apply when showing diagnostic model choices or changing account, role, channel or
group while a model selector remains mounted.

### 2. Signatures

Use the shared `ModelSelector` for the administrator fingerprint page. Candidates
come from the selected channel's configured model IDs. Ordinary Playground models
come from `/api/user/models?group=...` and eligible groups from
`/api/user/self/groups`; do not fetch administrator channels to derive them.

### 3. Contracts

Fingerprint controls, channel metadata and polling require current administrator
authorization. A route-entry check alone cannot protect cached data after demotion
or account changes. Key queries by the authorized identity and explicitly mask
cached results when access is lost; stop protected polling and clear stale selections.
Keep stateful fingerprint controls behind a live authorization boundary keyed by
account identity and permissions. Unmounting the previous identity must discard its
selected channel/model, job and late mutation results. Query cancellation must
consume the supplied AbortSignal.
Channel management list and search query functions likewise pass that signal to
`getChannels(params, signal)` and `searchChannels(params, signal)`, so navigating
away or replacing a query cancels the obsolete HTTP work. Fingerprint channel
options use the bounded options endpoint with selected-ID hydration, debounce
typing, and validate `success` before treating a response as a list. A business
error must expose retry through the shared error component, not appear empty.
Deduplicate candidate IDs without changing aliases. A selected model must remain in
the current source before Start is enabled or a request is built.

`supported_endpoints` describes upstream capabilities, not client protocols. Preserve
all valid combinations in the channel editor. Messages-only still accepts Chat and
HTTP Responses diagnostic clients through conversion; compact requires Responses.
Diagnostic key selection must honor `scope_mode`: an explicitly empty `allowlist`
serves no model. Selecting a specific Key filters the visible/testable model list
to that Key's scope. Reset hidden selections and pagination when switching Keys;
never silently send an explicit-Key test through a different Key. Automatic
selection remains available as its own explicit mode. The channel-test dialog
defaults to streaming on open, still prohibits it on unsupported endpoints, and
uses one model-list scroll region rather than nested dialog/body/list scrollers.
Keep explicit selection tied to a stable allocation ID and configuration revision,
not only its mutable array position. Send that identity to the manual test API;
reject an unavailable or revised allocation before dispatch and after capacity
admission. Existing index-only clients remain compatible. Clear or scope diagnostic
results when changing Keys so overlapping model names cannot reuse another Key's
failure or drive Delete failed models from stale results.

Per-Key upstream discovery is read-only. Its editor can select newly discovered
models outside the current channel catalog, then explicitly Save. The server
validates Key identity/revision and additively merges the selected allowlist into
the existing channel catalog in the same write as the Key scope. Preserve manual
models, other Keys and existing ordering; an empty allowlist remains deny-all and
never shrinks the catalog. Consume the successful response's authoritative
`channel_models` so reopening the editor sees the committed options. Never replace
this operation with an implicit all-Key fetch or a second channel update.
Invalidate in-flight discovery generations when the editor unmounts or its channel,
Key ID/index or revision changes. Guard success, rejection, conflict callbacks and
loading cleanup; an obsolete conflict must never reload the previous channel into
the current manager.

### 4. Validation & Error Matrix

| Transition | Required behavior |
| --- | --- |
| Administrator changes channel | Keep only a model valid for the new channel |
| Ordinary user changes group | Fetch authorized group models; clear invalid selection |
| Administrator loses privilege or identity changes | Hide cached channel metadata, stop protected queries/polling |
| Messages-only OpenAI channel | Offer supported converted client protocols |
| Ordinary user forges a channel/fingerprint request | Server independently denies access |

### 5. Good / Base / Bad Cases

Good: channel aliases appear unchanged in a searchable model dropdown. Base: group
selection retains user/token restrictions. Bad: a disabled query still exposes an
old administrator's cached channel list.

### 6. Tests Required

Extend the existing fingerprint, Playground options/state, channel-configuration
and channel-test-dialog suites for source changes, invalid selections, demotion,
cached data masking and capability combinations. Real router tests must exercise
sessions, scoped PATs and model tokens; injected role context is only handler evidence.

### 7. Wrong vs Correct

Wrong: treat a hidden page link or disabled query as authorization. Correct: gate
current rendering and requests, mask cached results, and retain server route checks.

## Group level and manual visibility settings

### 1. Scope / Trigger

Apply when editing group levels, manual grants or visibility previews, including
settings that arrive after an editor has mounted.

### 2. Signatures

`service.ResolveUserUsableGroups(userGroup, manual, specialSettings, levels)` is
shared by runtime permission checks and administrator `POST /api/group/preview`.
`GroupLevels` stores group names mapped to integers; `UserUsableGroups` stores
global grants; `GroupSpecialUsableGroup` stores per-user-group additions/removals.

### 3. Contracts

Larger numbers mean higher levels: 0 < 1 < 2 < 3 < 4. A ranked group inherits
existing ranked groups at or below its level. Global grants remain additive;
setting levels never deletes stored manual configuration. Explicit `+:`/plain
additions apply after inheritance, then `-:` removals win over both inherited
and explicit additions. The existing mandatory own-user-group grant remains.
Preview must resolve the current drafts through the same server resolver.

Manual editors must synchronize genuinely new incoming settings. A parent echo
of an editor's own serialized draft must preserve local row identities and
unfinished rows. Keep the manual controls discoverable next to level controls.
The parent group form must compare normalized saved group values before resetting;
refetching equal group settings after saving an unrelated option such as levels
must not discard unsaved visibility changes merely because object identity changed.

### 4. Validation & Error Matrix

| Situation | Required behavior |
| --- | --- |
| Lower-level user and higher-level target | No inheritance; an explicit grant may still allow it |
| Explicitly hidden inherited target | Hidden, except the mandatory own-user-group rule |
| All targets globally granted | Preview includes them; explain global grants instead of erasing them |
| Settings arrive after mounting | Existing manual rules appear without overwriting their values |
| Invalid level draft | Reject save/preview; retain the previous permission settings |

### 5. Good / Base / Bad Cases

Good: a level-2 user inherits levels 0–2, while a manual hide removes a target.
Base: an unranked group keeps manual behavior. Bad: hiding first and inheriting
afterward silently restores the forbidden target.

### 6. Tests Required

Extend `service/group_auto_groups_test.go` for numeric ordering, explicit grants,
hide precedence, conflicting add/hide entries and the mandatory own group. Extend
the existing frontend group settings suite for delayed props, local draft echoes,
manual-control visibility and preview payload preservation.

### 7. Wrong vs Correct

Wrong: initialize editable rules from props once and later serialize that stale
empty list. Correct: reconcile external settings changes while preserving local
edits when the parent merely echoes the same serialized draft.
