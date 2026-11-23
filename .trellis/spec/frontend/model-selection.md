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
