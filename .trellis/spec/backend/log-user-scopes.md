# Administrator log row scopes

Administrator list, count, numeric summary and export share `model.LogFilter`.
The `user_scope` query has exactly `all`, `self` and `others`; omission means
`all`. Explicit empty, unknown, repeated or malformed values return HTTP 400.
The subject ID comes only from authenticated context. Ordinary `/self` handlers
always constrain that authenticated ID and ignore administrator scope input.

Viewer role/tier controls JSON projection, CSV fields, column visibility and
saved column preferences. Selecting administrator `self` must not change the
viewer to the ordinary-user tier or switch to `/self` endpoints. All three
administrator row scopes keep the same administrative projection.

Frontend request identity includes the live viewer, role, scope and filters.
AbortSignal reaches the transport, bypassing the shared GET deduplication that
cannot isolate cancellation. A role change within the same session must hide
retained administrator data and cancel prior requests.

Channel-name selection requires channel-read permission and uses bounded channel
options. Persist only the stable numeric channel ID. Without permission retain
the numeric input. Hydrating a selected label and searching keyword candidates
are separate requests; a selected item must not replace keyword results. Mask
labels, selected input and titles consistently for the viewer tier.

Regression coverage belongs in the existing log filter/export suites and
usage-log access, group-filter, full-table billing-source-visibility, summary
and export-dialog suites. These scopes do not implement durable statistics:
R24 separately changes numeric storage/readers while preserving this predicate.
