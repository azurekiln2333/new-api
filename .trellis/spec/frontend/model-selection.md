# Model selector ordering

`ModelGroupSelector` sorts a copy of model options by display name before
filtering. Use `Intl.Collator('en', { numeric: true, sensitivity: 'base' })`
and compare raw model IDs lexically when names collate equally. Fixed valid
locale makes the order deterministic across interface languages. The
standalone `ModelSelector` preserves category order and sorts each newly
allocated category list with the same comparator.

Never sort caller-owned arrays in place or change the selected model as a
side effect of display sorting. Desktop popover and mobile drawer use the
same sorted data. `model-group-selector/__tests__/sorting.test.tsx` covers
numeric names, equivalent names with distinct IDs, filtering, immutable
inputs and preserved selection in both viewport modes. Future channel/group
navigation must retain these invariants.

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
serves no model. If the chosen key cannot serve the model, retain the existing
automatic-key fallback rather than sending that key's index.

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
