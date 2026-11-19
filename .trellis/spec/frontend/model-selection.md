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
