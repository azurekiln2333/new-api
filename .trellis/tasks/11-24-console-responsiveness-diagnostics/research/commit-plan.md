# Per-item commit plan — queue #11

## Status and authority

**STRUCTURE READY; FINAL PATCHES WAIT FOR ROOT `REFRESH`.** This replaces the former seven-commit, area-based plan. No part of that old plan's “safe to stage” advice remains valid while workers are editing.

Task: `.trellis/tasks/11-24-console-responsiveness-diagnostics`; worker: `pi-commit`; integrator: Claude, `w5D:p1`. Only root stages, commits, pushes, merges, or releases. This worker uses read-only Git inspection and writes packaging/task artifacts only. No local tests, builds, vet, lint, typecheck, formatting, Go/Bun/Node commands, or database probes. Validation is exclusively GitHub Actions; no validation success is claimed here.

Initial inspection on **2026-11-25** used HEAD `9329486b8060ad6c0ae6c68c40a723b65e8c105e`: 162 tracked changed paths, 5270 insertions / 1275 deletions; index empty. Counts are provisional, exclude untracked contents, and are not a final source freeze. Other workers have since added paths.

## Commit/patch manifest

One requirement or independently observable bug per commit; tests and translations accompany their owning change. Patch numbers are stable identifiers, **not dependency order**. Every final patch will be named `research/commit-patches/NN-<slug>.patch` and contain only its assigned delta. No empty placeholder `.patch` files are generated.

| Patch | Conventional Commit subject | Scope / source of truth |
|---|---|---|
| `01-r12-nonblocking-mutations.patch` | `perf(console): release saved mutations and publish channels locally` | R12: targeted routing publication, lazy alias invalidation, channel-priority UI, invitation caches, non-blocking model mutations |
| `02-r11-rejected-log-identity.patch` | `fix(logs): preserve rejected models without fabricated channels` | R11 parsed model before selection; desktop/mobile honest placeholders |
| `03-r13-inline-quota.patch` | `feat(users): confirm inline quota adjustments with safe deltas` | R13 existing quota API, duplicate-click lock, matching-user reconciliation |
| `04-r14-subscription-balance.patch` | `feat(users): expose recorded subscription balance spending` | R14 page-scoped read-only aggregate of recorded charged quota; separate list projection |
| `05-r15-drawer-identity.patch` | `fix(drawers): fence record changes and stale detail responses` | R15 user/API-key/model drawer identity and loading gates; sibling audit report |
| `06-r16-settings-batch.patch` | `feat(settings): save ordinary sections in one audited batch` | R16 endpoint, portable checked upserts, one transaction/audit/sync, specialized-path preservation, section call sites |
| `07-r17-storage-retention.patch` | `feat(logs): add opt-in bounded storage retention` | R17 in-flight worker #6; source is its final report/code, not current partial implementation |
| `08-r18-tooltip-contrast.patch` | `fix(ui): use readable tooltip surfaces in both themes` | R18 shared tooltip/kbd surface + storage/retry-chain regressions |
| `09-r19-update-source.patch` | `feat(updates): support configurable release repositories` | R19 upstream default preserved, repository validation, custom-tag comparison, repository-isolated state/tests |
| `10-r20-invitation-registration.patch` | `fix(auth): separate required invitation codes from referral links` | R20 server-side paths, persistent defaults, explicit settings/status/UI, bypass regressions |
| `11-console-forwarding.patch` | `feat(console): forward worker requests through verified master ingress` | Forwarding core + auth/passkey/origin integration + all forwarding regressions together; workers #1/#5 |
| `12-capacity-failover.patch` | `fix(relay): fail over when channel capacity cannot admit` | Capacity admission/coordinator errors, candidate exclusions/wait behavior, Redis change and regressions; worker #2. Separate hotfix is being shipped via `hotfix/channel-capacity-failover`, PR #1 into `main`; re-evaluate at REFRESH and record an explicit no-op if already incorporated. |
| `13-audit-outbox.patch` | `fix(audit): persist middleware events in the durable outbox` | Durable prepared metadata, actor snapshots, event delivery and real dialect/ClickHouse test fixtures |
| `14-notification-timeline.patch` | `fix(notifications): open the enabled timeline tab first` | Notification tab ordering/default and disabled-timeline fallback |
| `15-option-batch-pat-scope.patch` | `fix(authz): authorize option batches with the write scope` | Explicit PAT route rule and regression test only; R16 owns the endpoint |
| `16-ci-wiring.patch` | `ci: wire focused console regressions into hosted checks` | Final selectors for every new/changed test, existing hosted formatter diagnostics; worker #10 finishes last |
| `17-task-bookkeeping.patch` | `chore(tasks): record console work and reopened task history` | `.trellis` task/research records, queue/progress, archive-to-active move; excludes dumps and package outputs below |

### Additional existing deltas that cannot be hidden in those 17 items

These were found by inspecting the actual HEAD diff. Root explicitly approved these four individual per-item follow-up groups and shared-file hunk splitting on 2026-11-25. They are not an “all web” bucket; none may disappear from coverage.

| Patch | Conventional Commit subject | Observed residual delta |
|---|---|---|
| `18-r3-code-export-regressions.patch` | `fix(codes): align R3 batch callbacks and server-backed exports` | Shared export formatting/conditional-render cleanup and invitation export/callback tests |
| `19-r4-channel-layout-regressions.patch` | `fix(channels): align R4 metric layout regression expectations` | Existing card/budget formatting and test corrections for concurrency/quota/last-tested layout |
| `20-r8-node-detail-identity.patch` | `fix(dashboard): preserve R8 legacy route identity across reordering` | Public legacy entries use content identity, not list position; matching regression and nearby formatting |
| `21-r9-model-ranking-regressions.patch` | `fix(rankings): align R9 model drawer rendering and regressions` | Compact token expectations, dialog-scoped pricing assertion, existing conditional/render/import cleanup |

## Application order and commit boundaries

Planned dependency order: **13 → 01 → 02 → 04 → 05 → 03 → 06 → 15 → 07 → 08 → 09 → 10 → 11 → 12 → 14 → 18 → 19 → 20 → 21 → 16 → 17**.

- `03` builds on `05`'s matching-user/readiness baseline; do not smuggle R15's whole reset effect into R13.
- `06` introduces shared settings infrastructure. `07`, `09`, and `10` add their own validations, option fields and status-related keys afterward. Their shared-file parent states include `06`.
- `15` follows `06`: route existence and PAT authorization are explicit separate items. Do not publish an intermediate series state as a release.
- `11` is indivisible across forwarding, trusted ingress, browser origin, request ID, OAuth redirect, passkey scheme/host, video self-URL and their regression tests. It must not depend on a separate generic “auth hardening” commit.
- `16` is last code/config change; `17` is last source-record change. Hosted checks must target the final integrated SHA. A commit split is not evidence that each intermediate commit was executed or validated.
- Root reports capacity queue item #2 (patch `12`, not R11's patch `02`) is being shipped separately on `hotfix/channel-capacity-failover`, PR #1 into `main`. Keep its independent group. At REFRESH, re-read the current base/diff after root merges `main` back; if the hotfix is already present and has no remaining eligible delta, record `12` as an incorporated no-op with evidence, emit no empty patch, and never reintroduce or duplicate the hotfix.

## Concrete path / hunk ownership

`research/commit-patches/path-inventory.tsv` is reserved for the machine-readable path inventory from read-only status/diff; it has not been emitted yet. A `SPLIT` row is **not** permission to put the whole file into each patch. The following named boundaries control shared files; re-read their final diff after REFRESH.

### 01 — R12

Whole-file deltas: `controller/channel.go`, `controller/channel_audit.go` (existing alignment-only hunk), `controller/channel_priority.go`, `controller/channel_multi_key_test.go`, `model/channel_key_entry.go`, `model/channel_priority.go`, `model/channel_publication.go`, `model/task_model_alias.go`; channel priority hook/table and priority-order tests; invitation editor/cache patching in `web/src/features/invitation-codes/{components/invitation-editor.tsx,index.tsx}`; model delete/sync dialogs.

Shared settings hook: single-field committed-cache patch and non-refetch stale marking in `use-update-option.ts` belong here, not the later batch API branch. Shared invitation management tests: any final added mutation/cache responsiveness cases belong here; old callback/export expectation corrections go to `18`.

### 02 — R11

`middleware/distributor.go`, `controller/relay_error_log_test.go`, `web/src/features/usage-logs/components/{columns/common-logs-columns.tsx,common-log-mobile-card.tsx,common-log-mobile-row.tsx}`. In `components/__tests__/mobile-card.test.tsx`, own absent/preserved model/channel assertions and the `columnId` fixture extension used by those tests. R18 owns only its desktop retry-hover case; the `columnId` fixture extension and rejected-model/channel cases remain exclusively R11, as confirmed by the R18 owner.

### 03 / 05 — user drawer split

Shared `web/src/features/users/components/users-mutate-drawer.tsx`:
- `05`: `useLayoutEffect`, row/detail identity guards, active target, readiness/loading/error state, permission baseline reset, matching response fence, close/reset, load retry, save gate.
- `03`: editable-quota imports/API, removal of `UserQuotaDialog`/Pencil, loaded quota baseline and its integrations into the existing reset effect, quota in-flight lock/state, delta-confirm handler, editable numeric field, Confirm button and quota-specific disabling.
- Combined imports, reset/readiness effects, `inert`/save `disabled` expressions must be synthesized as cumulative intermediate text so neither patch refers to symbols introduced only by the other later patch. Every final changed line is covered exactly once in the resulting sequence.

Shared `components/__tests__/permissions.test.tsx`: stale A/B response, permission baseline and reopen freshness cases to `05`; safe delta/concurrent consumption/double Confirm cases to `03`. Other sibling drawer changes (`keys/.../api-keys-mutate-drawer.tsx`, `models/.../model-mutate-drawer.tsx`) to `05`. Existing redemption drawer's redundant fragment/indentation-only residual belongs to its R15 follow-up, with no content rewrite.

### 04 / 10 — user controller

`04`: `model/subscription.go` aggregate/parser, `model/user.go` list projection, `controller/user.go` `GetAllUsers`/`SearchUsers` enrichment hunks, final R14 regression paths, all seven locales' `Subscription Balance Used` key. UI column may already exist at HEAD; do not claim its current lack of a new diff is missing coverage or regenerate it.

`10`: `controller/user.go` `Register` affiliate-attribution hunk, `common/constants.go`, `controller/oauth.go`, `model/invitation.go`, invitation regressions, relevant status/auth/settings types and UI. Final authoritative boundary: `research/r20-invite-registration.md`, “Exact implementation / patch ownership”. Include `controller/invitation_test.go` affiliate fixture setup/restore, new `TestInvitationAffiliateLinkNeverReplacesCode`, `TestInvitationAffiliateSettingPersistsBeforeTakingEffect`, `TestInvitationLoginOnlyMethodsCannotProvision`, plus existing OAuth callback/WeChat extensions. Include new **UNTRACKED** `web/src/features/system-settings/auth/__tests__/basic-auth-settings.test.tsx`; plain `git diff` will not include it, so capture its frozen file contents explicitly at REFRESH. Keep unrelated R14/R16/R19 hunks out. The owner reports implementation DONE, with hosted validation still required.

### 06 / 07 / 09 / 10 — options and settings

- `controller/option.go`: R16 validation extraction/batch endpoint to `06`; only invitation/referral-specific accepted-key/boolean rules and final R20 guards to `10`; any retention-specific root/access checks to `07`.
- `model/option.go`: R16 checked upserts, shared validation, transaction/publication helpers to `06`; log-storage validator to `07`; update-source validator to `09`; affiliate initialization/boolean validation/persistence/map publication to `10`. Shared imports and boolean-expression edits must target cumulative states.
- `controller/misc.go`: `update_source_repository` to `09`; affiliate-link status to `10`; any final root-only storage metadata must remain `07`, not public status by assumption.
- `web/src/features/system-settings/hooks/use-update-option.ts`: single-field cache improvement to `01`; batch partition/API/error/caching to `06`; update-source status key to `09`; registration/referral status keys to `10`.
- `web/src/features/system-settings/types.ts`: batch response error fields to `06`; retention fields to `07`; update-repository type to `09`; affiliate setting to `10`.
- `auth/basic-auth-section.tsx`: ordinary-array submission to `06`; schema, label and referral switch to `10`. `auth/index.tsx`/`auth/section-registry.tsx` affiliate defaults to `10`.
- Ordinary section submit-loop changes and matching tests across system-settings go to `06`, including log-settings export/task-retention tests; later new log-storage fields and tests go to `07`.
- `setting/operation_setting/payment_setting.go`: proposed-section validation to `06`, not R20 or a new pricing feature. Existing pricing/passkey/request-policy specialized APIs stay on their own paths.
- `router/api-router.go`: batch endpoint registration to `06`. `middleware/access_token_routes.go` and `router/access_token_scope_test.go` batch-scope regression to `15`.

### 07 / 09 / 11 / 12 / 13 / 14 — exclusive paths

- `07`: `common/request_content.go`, `model/log_request_detail.go`, `service/system_task.go`, `setting/operation_setting/log_setting.go`, and final retention-specific reclaimer/storage-summary/settings/UI/test paths from `.trellis/tasks/11-24-dashboard-node-ranking-details/research/r17-retention.md`. `/api/option/log_storage` routing and its `option:read` PAT rule/test belong here, not option-batch PAT patch `15`. R17 locale substep is owner-reported finalized (13 keys × 7 plus two static bounds messages); full feature/hosted validation must still be confirmed by its owner. Do not treat partial source as acceptance evidence.
- `09` (R19 owner-confirmed): `setting/system_setting/update_source{,_test}.go`; `web/src/features/system-update/{releases.ts,use-system-update.ts,api.ts,system-update-dialog.tsx,store.ts,__tests__/releases.test.ts,__tests__/update-checking.test.tsx}`; `web/src/features/system-settings/maintenance/update-checker-section.tsx` and operations registration/defaults. **`store.ts` is mandatory**: repository field and legacy-upstream migration belong in this same R19 commit. Shared option/status/settings hunks follow `research/r19-update-source.md` and the split rules above; re-read final diff at REFRESH.
- `11` (owner-confirmed handoff `.trellis/tasks/11-25-console-forward-to-master/report.md`): new `common/console_forward.go` and `middleware/console_forward.go`; only forwarding config load in `common/init.go`; `middleware/request-id.go`; `main.go` forwarding mount, trusted-ingress warning, configured-master 90-second idle timeout. Include all helper integration in `middleware/{auth_origin.go,auth_origin_test.go}`, `oauth/linuxdo.go`, `controller/video_proxy.go`, `service/passkey/{service.go,service_test.go}`, plus pi-q5 regressions in `middleware/trusted_proxies_test.go`. Include two forwarding-error keys in all seven locales and their two `web/src/i18n/static-keys.ts` registrations in this **same** commit. Inspect any shared `common/log_text.zh-CN.json` producer messages for the matching forwarding allocation. Never omit new/untracked files or split helpers/tests into another auth commit. Source handoff is not hosted validation; still wait for root REFRESH.
- `12`: `common/redis.go`, `controller/relay.go`, `controller/channel_pin_retry_test.go`, `service/{channel_capacity.go,channel_capacity_test.go,channel_select.go,channel_wait.go,request_policy.go}` and any final capacity regression paths. No edits to any of them by this worker.
- `13`: `middleware/audit.go`, `model/{audit_log.go,audit_outbox.go,audit_outbox_record.go,audit_outbox_worker.go,code_operation_outbox_test.go}`, `controller/{access_token_audit_test.go,token_test.go}`. Tests and legacy metadata compatibility remain with outbox delivery.
- `14`: `web/src/components/notification-popover.tsx`, `web/src/hooks/use-notifications.ts` and any final dedicated notification regression.

### 08 / 18 / 19 / 20 / 21 — presentation/test follow-ups

- `08` (R18 owner-confirmed final boundary): all deltas in `web/src/components/ui/{tooltip.tsx,kbd.tsx,__tests__/tooltip-contrast.test.tsx}`; **only** the added storage-hover test in `web/src/features/system-info/__tests__/system-instances-panel.test.tsx`; **only** the added desktop retry-hover test in `web/src/features/usage-logs/components/__tests__/mobile-card.test.tsx`. Preserve the owner's corrected Base UI assertions (no assumed implicit role) and hover on the visible `CNBNew` label, not its nested copy badge. Do not pull R11's `columnId` fixture or rejected-model/channel cases into this patch.
- `18`: three modified `web/src/components/code-export/**` files and invitation management callback/export assertion corrections.
- `19`: channel card/budget-cell/table-refresh formatting and channel-details layout expectation corrections. R12 owns behavioral priority refresh changes instead.
- `20`: dashboard overview API item/panel, API panel test and resource hook; explicit legacy-route identity bug, not arbitrary bundling.
- `21`: rankings drawer/index and model-usage test residuals.

### Shared translations and server-log catalog

Split locale hunks by English key across **all seven** locale files, preserving the worker-provided translations. Root reserved the seven locale JSON files and `web/scripts/add-missing-keys.mjs` to the single locale writer, pi-auth (`w5D:p5`); pi-auth's final handoff reports the helper removed and locale ownership released to pi-q6/p8 for R17. This packaging worker never edits, deletes, recreates, or runs either the helper or locale JSON. The helper remains excluded even if recreated temporarily; do not capture an interim script. Needed keys/translations go to the designated locale writer. Patch extraction remains read-only and never rewrites locale source files. No locale generation/translation is performed by this packaging worker. R14 subscription label → `04`; R16 key-specific batch errors/specialized-path errors → `06`; update-source strings → `09`; new retention strings → `07`; invitation/referral strings → `10`; any added capacity reason → `12`; audit messages → `13`.

Owner-confirmed exact keys: `10` owns `Require an invitation code`, `Allow referral links`, and `Referral links attribute the inviter and rewards. They never replace a required invitation code.`; `09` owns backend error `Enter the repository as owner/repo, for example owner/new-api.`; `11` owns `console master unavailable` and `invalid console forwarding envelope`. In shared `web/src/i18n/static-keys.ts`, those three backend-error registrations split one to `09` and two to `11`, none to R20. R17 subsequently adds **two more** static registrations to `07`: `Retention must be a whole number of days between 0 and 36500` and `Request content limit must be between 256 and 134217728 bytes`. All **13** R17 locale keys (91 translations) belong to `07`; their exact supplied set is documented in the R17 locale-completion section. Current owner-reported aggregate locale delta is 29 added keys per file (16 earlier peer + 13 R17), no deletions; re-read final deltas at REFRESH. The locale writer reports the temporary helper removed. Do not capture any interim helper or infer an authorization to REFRESH from locale completion. UI keys remain discovered at their call sites.

`common/log_text.zh-CN.json` is similarly split by the message's producing change, not grouped with CI. The JSON spelling normalization `footer.new\\u0061pi.projectAttributionSuffix` → `footer.newapi.projectAttributionSuffix` has the same decoded key and unchanged attribution value; preserve all project/author attribution and assign that existing serializer-only residual once to the first locale-bearing patch (`04`). No protected project information is removed, renamed or replaced.

## Patch generation contract — run only after REFRESH

1. Root tells this worker `REFRESH` after code workers/review/CI wiring settle. Capture current HEAD, empty/nonempty index status, and exact tracked/untracked source inventory again. Do not assume the initial counts or HEAD are current.
2. Obtain a **read-only source snapshot** outside the repository, including `git diff HEAD --binary --full-index --no-renames` (tracked) and eligible untracked contents with native file modes. Do not stage, commit, write Git objects, create an alternate index/worktree, or apply patches. Never modify peer-owned files.
3. Partition the frozen delta into the manifest's cumulative states. Use read-only `git diff --no-index --binary` between temporary source representations where a shared hunk needs an intermediate state; normalize only patch path headers, not source content. New files need `/dev/null`/new-file-mode entries; deletions and rename/moves must preserve both sides. Preserve final line endings according to Git's source representation.
4. Emit one numbered patch per actual nonempty requirement/bug. Record filenames, dependency order, source paths/hunks, SHA-256, and byte counts. Never silently invent a patch for an already-committed requirement with no remaining delta; document that state.
5. Account for **every included tracked delta and every included untracked file** once. Reconstruct final cumulative source images in temporary memory/files (not the repository), compare their Git-canonical bytes to the frozen snapshot, and list any uncovered/overlapping delta. This is packaging accounting, not test/build/format validation. No `git apply`, including `--check`.
6. Before declaring final, re-read live hashes/HEAD/index. If workers changed a source during capture or afterward, mark the bundle stale, preserve their edits, and ask root for a fresh freeze/REFRESH. Do not claim exact coverage from a moving tree.

## Scope exclusions and self-reference boundary

“Patches equal the full uncommitted diff” means the frozen **eligible source delta**, including untracked source/task files; plain `git diff` alone misses untracked files. Explicit user exclusions:

- `node_modules/` (any occurrence).
- `web/scripts/add-missing-keys.mjs`.
- `research/**/*.json` and `research/**/*.zip` raw dumps (do not accidentally exclude ordinary task `task.json` outside research).

Root explicitly requires `research/commit-patches/**` **out of coverage AND out of every commit**. It is a root packaging artifact, including the inventory/manifest/coverage files stored there; a patch cannot contain its own final bytes/hash or inventory. `research/commit-plan.md` and `research/commit-plan-progress.md` remain eligible `17` bookkeeping: finalize their source-snapshot text before freezing/packaging, store subsequent bundle hashes/accounting under excluded `commit-patches/`, and do not silently omit them. If they change after freezing, the bundle is stale until refreshed. No excluded file is deleted or modified by this worker.

Other scratch helpers (`research/_web_*.cjs`, archived dispatch helpers/runtime records) remain eligible bookkeeping unless root explicitly excludes them; the obsolete plan's discretionary omissions are not silently inherited. Surface them for root's decision rather than losing them.

## Bookkeeping move and completion gates

The deleted tracked `.trellis/tasks/archive/2026-11/11-18-finish-workspace-changes/**` and new `.trellis/tasks/11-18-finish-workspace-changes/**` are the two sides of the already-investigated intentional reopened-task move. Include both in `17`, not just the new directory. New research records and each final worker report are also `17` subject to explicit dump exclusions.

Queue #11 stays **CLAIMED** while waiting for REFRESH; a prepared plan is not a completed final patch bundle. The R18 owner now owns queue #10 (CI wiring), which is pending R17 tests; this handoff is explicitly **not** REFRESH authorization. On successful final coverage, write the final progress/coverage record, mark only #11 DONE with output `research/commit-plan.md`, then inspect/claim the next OPEN queue item from the top. Root owns source commits; no git add/commit/push/reset/checkout/stash/apply is used here.

Commit subjects/trailers: Conventional Commits; no attribution trailers or generated-by lines. This is not a PR and does not change project attribution policy.
