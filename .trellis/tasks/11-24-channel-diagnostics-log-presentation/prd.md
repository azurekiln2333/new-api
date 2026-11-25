# Channel diagnostics and log presentation
## Goal
Channel metric layout and local budget placement, fingerprint prompt/output cards and useful-first log columns
## Requirements
Own R4 R5 R6 and all seven-locale integration from ../11-24-console-responsiveness-diagnostics/prd.md. The parent's latest user clarifications are authoritative.
Card exact order (2026-11-26 clarification): remove the entire card type slot, put name/ID first on two lines; concurrency0/limit plus icons; usedTokens, billed channel.used_quota / configured local limit, response and relative lastTest; priority/weight. Configured local total requires both ledger and budget enabled; known ledger-off/disabled/unconfigured budget total is0; loading/unavailable denominator stays explicit. Response and lastTest labels/values must not clip. Reuse the quota formatter and keep upstream details separate. Preserve privacy, aggregate rows and table type display. Fingerprint light options contract from keys; show actual three prompt/output cards and per-probe state, preserve scores/cancel/bounds/admin auth. Native 1-3 count control must not create extra requests; unrun cards explicit. Extend bounded memory snapshot with actual prompt/output. Logs order time,user,model,stream,Token usage,duration,cost,channel,node then others. Central i18n owner for peer keys; read i18n/shadcn/React skills and billing.md before quota work.
## Acceptance
User-visible behavior matches parent contracts; no auth/billing/data loss regressions. Extend existing tests but RUN NOTHING LOCALLY. All executable validation in GitHub Actions; DB changes require real three-engine/fresh-upgrade matrix.
## Authorization
User explicitly ordered four Pi workers to implement immediately, check in cloud, commit/push/tag/CI-CD and merge main only after green. Root owns all Git publication.
