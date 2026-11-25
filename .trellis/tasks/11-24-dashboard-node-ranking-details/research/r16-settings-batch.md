# R16 settings section batch — design
1. Preserve PUT /api/option/ for existing callers.
2. Add PUT /api/option/batch under the same root/secure-verification route group.
3. Accept an ordered array of key/value changes with bounded size and unique keys.
4. Reuse the single-key controller validation before any batch persistence.
5. Return the failing key separately from the safe validation message.
6. Reject passkey-domain, pricing and request-policy keys in ordinary batches.
7. Keep those keys on existing specialized calls, including confirmation/preview.
8. Validate ordinary dependent settings against the complete proposed batch.
9. Commit ordinary changes with checked upserts in one transaction.
10. Publish local values only after commit and emit one options sync obligation.
11. Record one audit event containing changed key names, never values.
12. A shared section-save hook partitions specialized calls from ordinary changes.
13. Patch every saved ordinary key in the existing system-options query cache.
14. Extend existing regression files and select the database matrix in hosted CI.
15. Source-only implementation; all executable validation remains root GitHub Actions.

## Session checkpoint — 2026-11-25

- Read the root `AGENTS.md`, this design note, `other-mutation-round-trips.md` section 1, the child PRD/report, and the parent PRD R16 requirement.
- Inspected the current dirty diffs in `controller/option.go`, `model/option.go`, `model/option_upsert_test.go`, and `web/src/features/system-settings/`. Preserve prior changes, especially the earlier `use-update-option.ts` cache-update hunk.
- Prior implementation already has controller per-key validation extraction and checked ordinary-option upserts/bulk transaction work, but no batch HTTP route/frontend section batching/audit implementation has been confirmed yet.
- Further inspection: the `/api/option` route is under `RootAuth`; the existing single PUT remains in place for compatibility. Dedicated request-policy PATCH exists, model-pricing APIs exist, and passkey domains use the preview/confirmation endpoint.
- `model.UpdateOptionsBulk` already performs checked upserts in one transaction, updates the in-memory values after commit, and emits one options sync bump. It must not be used to route passkey, pricing, or request-policy keys through the ordinary batch endpoint.
- The shared `recordManageAudit` path enqueues one durable outbox event using the existing `option.update` action. Batch audit should contain only changed key names, never values.
- Frontend settings contains many sequential `updateOption.mutateAsync` loops. A shared batch mutation/hook can cover ordinary sections, while request-policy (`useSavePolicy`), pricing mutations, and passkey-domain confirmation remain specialized.
- Controller per-key validation currently writes errors directly to Gin context. The batch route needs an isolated way to attach the failing key to the current validation result. Validation also calls `ratio_setting.Update*ByJSONString` for some keys, which can mutate runtime state before persistence; investigate how to avoid partial runtime mutation before implementation.
- Existing test choices: extend `controller/option_claude_test.go` or `controller/option_gemini_test.go` with focused API validation coverage, and extend untracked `model/option_upsert_test.go` for batch atomicity/sync behavior. Do not add another test file.
- No local validation or database probes will be run; no Git publication.

## Implementation checkpoint — backend and shared frontend mutation

- Refactored `validateOptionUpdate` into an error-returning shared validator, retaining single PUT error/code handling. Dependency checks evaluate the complete proposed batch; ratio validation parses a temporary map instead of mutating runtime state.
- Added `UpdateOptionsBatch` with bounded, unique primitive-valued options; rejects passkey-domain/pricing/request-policy keys, returns `key` on member errors, and prepares one value-free `option.update` audit event.
- Added `model.UpdateOrdinaryOptionsBatch`: prevalidates every member, commits checked upserts + one sync-task INSERT + one audit outbox INSERT in ONE transaction, then publishes the section under one OptionMap lock. Split existing map publisher into locked wrapper/internal implementation; existing callers retain their locking behavior.
- Added the `/batch` PUT beside the existing single PUT in `router/api-router.go` (one-line route-only change; no main.go/middleware/workflow edits).
- Added `updateSystemOptionsBatch` and extended the existing mutation hook to accept an array, partition specialized keys, send one ordinary batch, patch ordinary cached values without blocking refetch, and present the failing key. Preserved earlier pi-ui single-setting cache patch.
- Converted all identified settings section `updateOption.mutateAsync` loops (auth, general, content, integrations, maintenance, model settings and request limits) into one array mutation per save. Existing single-key controls and dedicated policy/pricing/domain saves remain unchanged.
- Sent root the route-only change and the **required middleware scope registration**: `PUT /api/option/batch` → `accessTokenScopeRule("option:write")`. This worker will not edit middleware; until root integrates that mapping, PAT requests correctly fail closed but parity is incomplete.
- Sent pi-ui two new exact keys for all seven locales via the sanctioned script workflow: `Could not save {{key}}: {{error}}` and `This setting must be saved through its dedicated settings API`. Locale integration/CI normalization remains pi-ui/root-owned.
- Extended existing `model/option_upsert_test.go` with ordinary batch three-dialect success and write/sync/audit rollback cases, plus prevalidation rejection. Added silent fixture logging and audit-event cleanup; source-only, never run.
- Extended existing `controller/option_claude_test.go` with per-key rejection, complete proposed Turnstile dependency batch, value-free single audit/sync, compatibility single PUT and audit-failure rollback cases. Source-only, never run. Review still needed: correct user-login option prefix and preserve legacy scalar string normalization (normalization now restored).
- Root confirmed middleware route scope mapping is added. Added ONLY new `TestOptionBatchAccessTokenScopes` in existing `router/access_token_scope_test.go`: root option:write PAT succeeds; option:read, ordinary session, anonymous and root-role downgrade reject before mutation. Existing router fixture reused without modifying it.
- Updated existing frontend request assertions in OAuth, bot protection, pre-consume, email/notifications, Epay, log settings and group settings to the one-batch request contract. Corrected the protected per-user preference test key to `LoginVerificationUser:1`.
- Current remaining closeout: add focused hook cache/failure/mixed-special regressions in an existing test file; finish source-only readback, confirm seven-locale integration, and append the R16 handoff plus hosted CI selectors to child report. No local validation or Git commands that mutate repository state have run.
