# Design and boundaries
## Evidence
- Fingerprint requests page_size=30 but model.SearchChannels loads every match then controller slices. AfterFind adds one allocation SQL per migrated channel. Normal page listing/cache reload also trigger per-row queries.
- Channel Update synchronously reloads full cache and records audit before replying. BumpSyncTopic only records a task; it does not wait for other-node ACK.
- Invitation creates one transaction then synchronously resolves audit actor/cache and writes LOG_DB. Redemption creates up to100 rows sequentially then audits. Frontend create dialogs already close immediately after HTTP success.
- Probe DTO omits actual prompt/reply; generator/result already contain them. API Info uses configured array order and public status; instance collector already samples resources but full instance API is root-only.
## Architecture
1. Channel owner: bounded SQL queries and batch allocation projection for management and cache reads; lightweight authorized channel options DTO. Preserve authoritative entries elsewhere. Context flows into DB. Publish local route/binding state with revision/order safety, retain existing background sync.
2. Mutation owner: audit slow post-commit work, batch redemption writes, return committed result promptly; durable recoverable operation/batch identity for code creation so response loss/retry does not create a new batch. Reuse existing durable work mechanism where suitable; publish exact schema/interface before shared integration. Never background required auth/cache revocation or discard audit on process exit. Audit actor comes from authenticated request snapshot.
3. UI owner: use lightweight options API contract from keys; preserve identity-keyed caching/AbortSignal. Local budget remaining/limit uses quota formatter, actual Tokens stay log-based. Relative time reuses existing helper. Probe strings copied into existing bounded job snapshots and rendered in shared Card/CopyButton.
4. Dashboard owner: stable instance node_name binding can be referenced by multiple configured API entries; persist ordering in existing API settings. Batch resource projection exposes only configured instances' approved CPU/memory/storage/status/timestamps. Full root instance API permissions remain. Track sample validity, never equate unavailable to0.
5. Ranking: extend existing aggregation for selected model/time window with bounded users; filter names/identity at server according to viewer. Reuse existing model detail drawer shell, usage formats and model authorization. Never share privileged DTO caches across identities.
## Ownership
- pi-keys: model/channel*.go except fingerprint/budget accounting arithmetic; controller/channel.go; router/channel-router.go; channel read/cache/status tests.
- pi-billing: controller/audit.go, controller/invitation.go, controller/redemption.go, related model audit/invitation/redemption/new operation files; model/main.go; router/api-router.go; management mutation/code frontend and their shared components. model/log.go only audit-related functions; coordinate rankings changes through new helper file.
- pi-ui: web/src/features/channels, service-status/fingerprint*, usage-logs common columns; service/channel_fingerprint*.go; controller fingerprint tests; ALL locale JSON/static-key changes.
- pi-logs: dashboard, rankings, public-header/avatar homepage use, system settings/API info, system-info resource views; related instance/usage aggregation/controllers. router/api-router.go additions go through pi-billing; model/main.go too.
- Root: parent plans/spec changes, CI workflow, cross-worker contract review, commits/release. No overlapping edits without explicit handoff.
## Rollout
Preserve old clients/settings with optional additive fields. No domains infer traffic behavior. Validate schema/locking in cloud across engines. Revert independent UI surfaces or service contracts coherently; never remove committed budgets/audits/created codes during rollback.
