# Design
Read parent design.md and research/source-findings.md first.
## Ownership
pi-keys exclusively owns model/channel*.go (preserve budget accounting), controller/channel.go, router/channel-router.go, related existing tests. You are not alone; preserve peer edits and request handoff before overlapping changes.
## Behavior
Implement lightweight admin-only options with bounded SQL and id/name/models; SQL pagination/filter/type-count compatibility; batched authoritative key projection for management/cache loads. Request cancellation must reach DB. Eliminate per-channel reads from full reload. Make channel local route publication complete and revision-safe before response, propagate peers through existing background sync; do not replace full refresh with incomplete CacheUpdateChannel. Publish option endpoint DTO to pi-ui. Shared audit changes owned pi-billing. Do not edit web or frontend locales.
## Compatibility
Preserve existing auth scopes, revision/CAS, routing, log/audit durability and quota units. Add optional compatible DTO fields; avoid unbounded queries. Publish exact interface early. Unsafe or materially wider choices must be sent to root before editing that portion; continue independent owned work.
