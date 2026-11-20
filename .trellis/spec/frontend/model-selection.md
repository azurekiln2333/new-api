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
