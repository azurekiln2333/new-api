# Design
Read parent design.md and research/source-findings.md first.
## Ownership
pi-ui exclusively owns web channels, service-status fingerprint, usage-logs common columns, service/channel_fingerprint*.go, related existing tests, ALL locale JSON and static key manifests. You are not alone; preserve peer edits and request handoff before overlapping changes.
## Behavior
Card exact order: unchanged name/ID two lines; concurrency0/limit plus icons; usedTokens remaining/total response relative lastTest; priority/weight. Local enabled budget replaces separate budget row in existing quota region using service remaining/limit and quota formatter; upstream remains distinct in details. Preserve loading/unavailable/inactive ledger/privacy semantics. Fingerprint light options contract from keys; show actual three prompt/output cards and per-probe state, preserve scores/cancel/bounds/admin auth. Native 1-3 count control must not create extra requests; unrun cards explicit. Extend bounded memory snapshot with actual prompt/output. Logs order time,user,model,stream,Token usage,duration,cost,channel,node then others. Central i18n owner for peer keys; read i18n/shadcn/React skills and billing.md before quota work.
## Compatibility
Preserve existing auth scopes, revision/CAS, routing, log/audit durability and quota units. Add optional compatible DTO fields; avoid unbounded queries. Publish exact interface early. Unsafe or materially wider choices must be sent to root before editing that portion; continue independent owned work.
