# Channel loading and save path performance
## Goal
SQL pagination and batched allocation projection with safe local channel cache publication and background node sync
## Requirements
Own R1 and the channel part of R2 from ../11-24-console-responsiveness-diagnostics/prd.md. The parent's latest user clarifications are authoritative.
Implement lightweight admin-only options with bounded SQL and id/name/models; SQL pagination/filter/type-count compatibility; batched authoritative key projection for management/cache loads. Request cancellation must reach DB. Eliminate per-channel reads from full reload. Make channel local route publication complete and revision-safe before response, propagate peers through existing background sync; do not replace full refresh with incomplete CacheUpdateChannel. Publish option endpoint DTO to pi-ui. Shared audit changes owned pi-billing. Do not edit web or frontend locales.
## Acceptance
User-visible behavior matches parent contracts; no auth/billing/data loss regressions. Extend existing tests but RUN NOTHING LOCALLY. All executable validation in GitHub Actions; DB changes require real three-engine/fresh-upgrade matrix.
## Authorization
User explicitly ordered four Pi workers to implement immediately, check in cloud, commit/push/tag/CI-CD and merge main only after green. Root owns all Git publication.
