# Dashboard node links and ranking details
## Goal
API ordering and node binding/resource details, homepage avatar navigation, admin guide visibility and masked model usage drawers
## Requirements
Own R7 R8 R9 from ../11-24-console-responsiveness-diagnostics/prd.md. The parent's latest user clarifications are authoritative.
User clarified ALL users see CPU/memory/storage in existing overview API cards, configurable ordering and wider API area. Existing system settings binds instances to API domains; ONE instance can bind MULTIPLE configured domains. Click API item opens safe filtered node detail. Preserve complete root-only node API; public projection only approved resource/state fields, no hostname/internal addresses/sync errors. Explicit unknown/stale/disabled monitoring states. Homepage avatar desktop/mobile direct dashboard; console menu unchanged; hide admin overview onboarding all entry/reminder states, preserve installer. LLM ranking model opens existing model-square-style drawer with usage and user ranking, not pricing link. Server masking: admins full names all users; ordinary viewers full self name and masked others. No user IDs/real names in masked payload/cache. Reuse time window/auth/model filters. router/api-router.go/model/main.go owned pi-billing: send exact additions; model/log.go shared audit ownership use separate model ranking helper. Send all locale keys to pi-ui.
## Acceptance
User-visible behavior matches parent contracts; no auth/billing/data loss regressions. Extend existing tests but RUN NOTHING LOCALLY. All executable validation in GitHub Actions; DB changes require real three-engine/fresh-upgrade matrix.
## Authorization
User explicitly ordered four Pi workers to implement immediately, check in cloud, commit/push/tag/CI-CD and merge main only after green. Root owns all Git publication.
