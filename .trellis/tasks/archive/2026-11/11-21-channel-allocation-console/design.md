# Frontend design

Backend child designs remain the semantic contract; parent dispatch.md is the authoritative ownership map. pi-ui alone edits web/.

Read web/AGENTS.md and shadcn-ui/i18n-translate/vercel-react-best-practices skills first. Locate existing multi-key manager, channel row actions, shared ConfirmDialog/CopyButton/QuotaDetailsPopover, log schema/details and mobile cards. Compose those components and extend compatible props instead of introducing replacements.

Coordinate endpoint names, entry ID/revision, budget DTO and request-detail metadata with backend workers through key-quota-log-1121. Use feature API/types/query patterns and invalidate existing query keys after changes. Do not fabricate a completed endpoint or permanently duplicate contract types. Treat monetary inputs with existing quota conversion and currency formatting semantics.

Limits and reset are separate actions. Reset confirmation describes usage reset without wallet refunds. Dedup uses a server preview, expected versions and an explicit confirm; report skipped busy entries. Public and bound allocations are distinguishable. Detail content is fetched only while the relevant authorized dialog is open. Never cache it in list records or console logs.

Test user-visible editing/reset/dedup, version conflicts, unavailable upstream balance, successful/failed detail capture, role visibility, unavailable historical bodies, truncation and node provenance with existing fixtures where possible.
