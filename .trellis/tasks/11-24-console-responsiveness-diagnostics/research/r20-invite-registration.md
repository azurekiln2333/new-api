# R20 — invitation-code registration security (pi-auth)

## Checkpoint 1 — resumed 2026-11-25
- Queue #3 is already CLAIMED by pi-auth. Parent PRD authorizes immediate implementation; no task creation or extra approval needed. Parent status is in_progress; `task.py current` has no workspace pointer, so do not overwrite another worker's task context.
- Read root AGENTS.md, parent PRD and queue. No previous R20 progress file existed. Read-only `git diff` inspected for common/constants.go, model/invitation.go, model/user.go, model/option.go, controller/option.go, controller/oauth.go, controller/user.go and controller/misc.go.
- Existing work already separates `RegisterAffiliateLinkEnabled` (new, default true) from existing `InviteRegisterEnabled` (default false), publishes the public flag, checks the affiliate switch in password/OAuth attribution, and stores/parses the option. These changes predate this resume and will be continued, not rewritten. `controller/option.go` also contains unrelated batch-setting work; `model/user.go` contains R14 work, both must remain intact.
- Existing transactional `InsertUserWithInvitation` requires a real single-use code regardless of affiliate code. Existing controller/invitation_test.go covers several registration paths; inspect before extending.
- Next: read current OWASP Authentication, Session Management, OAuth/CSRF and ASVS guidance; relevant Trellis specs/artifacts; trace all registration writers; finish settings UI and focused bypass regressions as needed.
- Validation policy: NO local tests/builds/vet/lint/gofmt/typecheck/format/DB probes or Go/Bun/Node commands. Only the explicitly permitted locale add-missing-keys script workflow may run. GitHub Actions must run all validation. No staging/commit/push/checkout/reset/stash.

## Checkpoint 2 — security references and registration writers traced
- Read OWASP Authentication, Session Management, OAuth 2.0 and CSRF Prevention cheat sheets from the official CheatSheetSeries source, including full truncated tails. Read ASVS v5.0.0 V2, V6, V7, V10 and V16. Applicable controls: v5.0.0-2.2.1/2.2.2 (server validation), 2.3.1/2.3.3/2.3.4 (no skipped entitlement step, atomic and single-use claims), 6.1.3/6.3.4 (all authentication pathways), 10.1.2/10.2.1 (transaction-bound OAuth state), 16.2.5/16.5.1/16.5.3 (no secrets, fail closed). Existing session verification/rotation is not being redesigned.
- Read parent design/implementation artifacts, Trellis backend/frontend indexes, relevant quality/database and shared reuse/cross-layer guidance; web/AGENTS.md, shadcn-ui and i18n skills.
- All anonymous account writers found: password/email `Register`, standard/custom OAuth `findOrCreateOAuthUser`, and `WeChatAuth`. All already use `InsertUserWithInvitation`. Telegram uses shared OAuth; legacy widget endpoints are tombstones. Passkey enrollment is under authenticated `/user/self`, login only reads existing credentials. Admin `AddUser` is privileged provisioning, not self-registration. Must record these distinctions explicitly.
- No proven affiliate-code bypass found in the current tree so far; do not invent one or alter existing registration mode semantics. Missing functionality is the new affiliate switch's settings UI/types/tests and complete validation/bypass regression evidence.
- Next changes: extend existing controller/invitation_test.go (affiliate code cannot substitute for invitation; disabled attribution; invalid setting values), preserve existing atomic/expiry/replay tests; expose the independent switch in existing BasicAuthSection and its defaults/registry/types; complete all seven locales via sanctioned script. Preserve unrelated batch-save changes. GitHub Actions execution remains pending.

## Checkpoint 3 — implementation and focused regressions written (not executed)
- Completed missing boolean validation for `RegisterAffiliateLinkEnabled` in the shared option controller validator; model-level and bulk validation already reject invalid values. Password registration now skips affiliate lookup entirely when disabled or absent (0 DB round trips instead of an unnecessary referral lookup), while the transactional invitation gate remains unchanged.
- Existing invitation fixture now initializes/restores the affiliate flag. Extended controller/invitation_test.go with `TestInvitationAffiliateLinkNeverReplacesCode`: password, verified-email, real GitHub/Discord/OIDC/LinuxDO providers and custom OAuth; each under enabled/disabled referral attribution; affiliate-only, affiliate-as-code, valid code, replay, no rejected account writes, correct consumed-user linkage. Added `TestInvitationAffiliateSettingPersistsBeforeTakingEffect` for persistence, invalid booleans and failed-storage fail-closed behavior.
- Existing UI composition reused: BasicAuthSection's SettingsSwitchItem/Content + FormField/Switch (no new business widget). Added explicit `Require an invitation code` and independent `Allow referral links`, passed default true through existing settings registry/types, typed the additive public flag, and invalidated public status when registration/referral switches change. Existing batch save preserved.
- Source audit correction: Telegram OAuth is stricter than generic auto-registration: `findOrCreateOAuthUser` returns `ErrTelegramAccountNotBound` for unknown Telegram users and never provisions one. Passkey similarly never provisions a user. Do not enable new registration paths just to test a policy.
- Next: improve callback/referral adversarial cases and login-only endpoint evidence, UI regression and seven translations via sanctioned add-missing-keys workflow. All test/build/format/matrix results remain pending GHA.

## Checkpoint 4 — endpoint and UI evidence ready; locale insertion next
- Extended existing OAuth callback test with affiliate-only stored payload + a valid code added only to callback query; it must fail and keep both codes unused. Existing stored-code/tampered-state cases preserved. WeChat test now rejects a public affiliate string in `invitation_code` as well as missing invitation, before valid registration and existing-user login.
- Added `TestInvitationLoginOnlyMethodsCannotProvision`: unbound Telegram remains denied even with a valid invitation; anonymous Passkey register begin/finish fail; retired Telegram widget endpoint remains 410; no extra account or code consumption.
- New UI regression file `web/src/features/system-settings/auth/__tests__/basic-auth-settings.test.tsx` uses actual BasicAuthSection and batch API: independently keyboard-toggle either switch and assert only its option is sent; assert accessible referral explanation.
- pi-web acknowledged no overlapping auth/locale writes and owns queue #10. Sent exact TestInvitation* selector/dialects/fixture envs, focused atomic/affiliate CGO race selector and new web path. All remain unexecuted locally by policy.
- Correction to checkpoint 2 shorthand: authenticated Passkey routes are `/api/user/passkey/register/*`, under the middleware.UserAuth self route group; administrative provisioning handler is `CreateUser`.

## Checkpoint 5 — locale integration completed; source review in progress
- Added 3 R20 UI keys across all 7 locales via the sanctioned `web/scripts/add-missing-keys.mjs` workflow. Also delivered peers' exact backend keys: R19 `Enter the repository as owner/repo, for example owner/new-api.` and forwarding `console master unavailable` / `invalid console forwarding envelope`, all registered in `STATIC_I18N_KEYS`.
- Script reported 6 translations per locale, 42 total. `bun run i18n:sync` was attempted but Bun is unavailable on PATH; ran the exact package script equivalent `node scripts/sync-i18n.mjs` successfully within the permitted locale-edit exception. Read the resulting sync report: every locale has missingCount=0 and extrasCount=0 (pre-existing untranslated-count warnings remain, unrelated to these keys). No test/build/lint/typecheck/format/DB commands were run.
- A first temporary script disappeared before execution; recreated it from owned content without touching anyone else's code. Final script removed after successful insertion/sync; released exclusive locale reservation to R17 pi-q6 in p8. R19 p9 and forwarding p2 were notified their requested keys are delivered.
- Read final owned source diffs and tests using Trellis quality checklist. Cross-layer wiring checked: persisted boolean -> runtime global -> registration attribution and public flag -> auth settings schema/default/registry -> independent batch save. No proven invitation bypass in current source; no new auth route introduced, no database schema or invitation claim logic changed by this item.

## Final handoff — worker implementation delivered, hosted validation REQUIRED

### Result
No affiliate-link bypass was demonstrated in the current source tree. Every anonymous account writer already routes through the transactional invitation gate. This item completes the missing independent affiliate-link switch and adds adversarial coverage rather than inventing a new registration policy or changing existing defaults. `DONE` in the queue means source implementation/review handoff only, **not** verified ASVS compliance, completed security assessment, or database compatibility.

### Registration path inventory (server enforcement)
| Entry point | Account creation and entitlement | Referral behavior |
| --- | --- | --- |
| `POST /api/user/register` | `RegisterEnabled` AND `PasswordRegisterEnabled`; optional email verification; `InsertUserWithInvitation` in account transaction | `aff_code` only looks up inviter if the new affiliate switch is enabled; never supplies `invitation_code` |
| Standard GitHub, Discord, OIDC, LinuxDO callbacks | `HandleOAuth` consumes provider/intent-matched auth flow; `findOrCreateOAuthUser` requires open OR invite registration; real `InvitationCode` comes only from stored flow payload; user + binding + invitation are one transaction | Separate stored `AffiliateCode`, ignored when disabled |
| Custom OAuth/SSO callback | Same `HandleOAuth` and `findOrCreateOAuthUser`; binding failure rolls account and invitation back | Same separate attribution switch |
| `GET/POST /api/oauth/wechat` | New users use the same transactional gate. Legacy GET has no invitation payload and cannot register when a code is required; POST carries `invitation_code` | No existing affiliate attribution here, preserved |
| Telegram OAuth | Existing bound accounts only; unknown Telegram identity returns `ErrTelegramAccountNotBound` before any provisioning, even with a valid invitation | No automatic registration added |
| Retired Telegram widget routes | `TelegramLegacyAuth` returns 410 | Cannot provision |
| Passkey register begin/finish | `/api/user/passkey/register/*` is under `middleware.UserAuth`; handlers also require an existing authenticated user and scoped proof | Credential enrollment, not account registration |
| Passkey login, MFA, recovery, email bind | Operate on existing accounts; no account insert call found | No alternate provisioning path |
| Administrative `CreateUser`, initial installer | Privileged provisioning / one-time bootstrap, not anonymous self-registration; existing authorization boundaries preserved | Unchanged |

### Stored/public/UI contract
- Existing `InviteRegisterEnabled` remains the stored requirement flag, default false. Existing `RegisterEnabled`/`PasswordRegisterEnabled` semantics remain unchanged, including invitation-only OAuth/SSO/WeChat while open registration is off.
- Additive `RegisterAffiliateLinkEnabled` defaults true so historical attribution is preserved on upgrade. False disables *new registration attribution and resulting referral rewards*, not historical reward balances. No option-key rename, database column, migration, price or accounting change.
- Public status adds optional `affiliate_link_enabled`; existing `invite_register_enabled` is unchanged.
- Both settings APIs reject non-boolean option values. Writes must succeed before publishing the new runtime value. Disabling referrals cannot relax the required-code gate.
- Existing `BasicAuthSection` composes existing `SettingsSwitchItem`, `SettingsSwitchContent`, `FormField` and `Switch`; no replacement widget or custom interaction. `Require an invitation code` and `Allow referral links` are independent controls using the pre-existing batch save. Public status refreshes after registration/referral changes.
- Source round-trip change: password registration now performs **zero** affiliate lookup statements when the field is empty or the affiliate switch is false; a supplied affiliate while enabled retains one lookup. No latency claim.

### Exact implementation / patch ownership
R20 hunks (preserve all other uncommitted edits in these files):
- `common/constants.go`: new affiliate flag + explanation (already present at resume).
- `model/option.go`: only affiliate default entry, strict-boolean allowlist additions, bulk/checked persistence routing, runtime switch case (partial R20 existed at resume; unrelated R16 batch/upsert/R17/R19 hunks are peers').
- `model/invitation.go`: explanatory contract comment only; existing conditional claim/transaction algorithm is unchanged.
- `controller/user.go`: first registration affiliate-lookup hunk only; R14 user-list aggregation is unrelated.
- `controller/oauth.go`: affiliate-enabled condition only.
- `controller/misc.go`: `affiliate_link_enabled` only; update-source field is R19.
- `controller/option.go`: strict-boolean validation addition only; batch-save refactor is R16.
- `controller/invitation_test.go`: fixture affiliate initialization/restoration; three new tests and existing OAuth callback/WeChat extensions.
- `web/src/features/auth/types.ts`: optional additive public flag in both status shapes.
- `web/src/features/system-settings/types.ts`: affiliate boolean only.
- `web/src/features/system-settings/auth/{basic-auth-section.tsx,index.tsx,section-registry.tsx}`: field/control/default/prop wiring only; prior batch submit change belongs to R16.
- `web/src/features/system-settings/hooks/use-update-option.ts`: only three registration/referral status-refresh keys.
- New `web/src/features/system-settings/auth/__tests__/basic-auth-settings.test.tsx`.
- Seven locale files: R20 three keys `Require an invitation code`, `Allow referral links`, and `Referral links attribute the inviter and rewards. They never replace a required invitation code.`
Peer-delivered locale hunks: R19 exact error `Enter the repository as owner/repo, for example owner/new-api.`; forwarding errors `console master unavailable` / `invalid console forwarding envelope`; their three static registrations in `web/src/i18n/static-keys.ts` belong with those features, not R20.

### Focused regression matrix
| Test / file | Observable protection |
| --- | --- |
| `TestInvitationAffiliateLinkNeverReplacesCode` | Password, verified email, GitHub, Discord, OIDC, LinuxDO, custom OAuth; affiliate enabled/disabled; referral-only and referral-as-code fail; valid code succeeds once; replay fails; no rejected account row; correct attribution and used-user linkage |
| `TestInvitationAffiliateSettingPersistsBeforeTakingEffect` | Persisted false flag, unchanged required-code flag, invalid values fail, storage failure cannot re-enable referral attribution |
| `TestInvitationLoginOnlyMethodsCannotProvision` | Unbound Telegram denied even with valid invite; anonymous Passkey enrollment rejected; Telegram widget remains 410; account count/code unchanged |
| `TestInvitationOAuthCallbackUsesServerPayload` (extended) | Missing stored invitation cannot be repaired using callback query or affiliate code; altered state fails; only stored valid code consumed |
| `TestInvitationCustomOAuthAndWeChat` (extended) | WeChat ignores affiliate fields as entitlement; rejects affiliate string as invite; existing-user login still works |
| Existing `TestInvitationAtomicRegistration` | Disabled/expired/used code failure, concurrent single winner, rollback/retry atomically preserves code |
| New `basic-auth-settings.test.tsx` | Accessible independently keyboard-toggled switches; only selected key sent through existing batch API |

### Validation and blockers
- **Executed only permitted locale workflow**: `node scripts/add-missing-keys.mjs` from `web/` inserted 6 keys per locale (42 translations); `bun run i18n:sync` could not start because Bun is unavailable on PATH, so ran its exact package-script equivalent `node scripts/sync-i18n.mjs` successfully. Read sync report: all seven locales missingCount=0 / extrasCount=0. Unrelated pre-existing untranslated-count warnings remain. Temporary script deleted; locale lock explicitly released to pi-q6/p8.
- **No local tests, build, vet, lint, typecheck, format/gofmt, database probes, Go commands, dependency installation or Git mutations were run.** Source inspection and read-only diff only; no tests are reported passing.
- pi-web confirmed `.github/workflows/ci.yml` now selects `TestInvitation` across SQLite/MySQL/PostgreSQL and focused atomic/affiliate race, plus new BasicAuth file in full Vitest and explicit lint scope.
- Required hosted backend commands: for each `DIALECT=sqlite/mysql/postgres`, `TEST_SECURITY_DIALECT=$DIALECT go test -p 1 -count=1 -v ./controller -run 'TestInvitation'`; existing fixture also requires `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN` for external engines. It creates isolated DBs and logs engine versions. Focused race: `CGO_ENABLED=1 go test -race -p 1 -count=1 -v ./controller -run 'TestInvitationAtomicRegistration|TestInvitationAffiliate'` (SQLite default).
- Required hosted web: run new BasicAuth test, existing sign-up registration-modes test and full affected checks/typecheck/build/format gates. Only GitHub Actions may execute them.
- Exact database versions/results and latest green SHA are **not available yet**; root must record GHA fixture output before declaring database/security verification complete. This item adds no schema migration, independent-module change or ClickHouse path.

### OWASP references and unresolved scope
Read official [Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html), [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html), [OAuth 2.0](https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html), [CSRF Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html), and [Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) guidance. Reviewed stable ASVS **5.0.0** V2/V6/V7/V10/V16: relevant IDs `2.2.1`, `2.2.2`, `2.3.1`, `2.3.3`, `2.3.4`, `6.1.3`, `6.3.4`, `10.1.2`, `10.2.1`, `16.2.5`, `16.5.1`, `16.5.3`. These references constrain the bounded change; they are not a claim of ASVS certification/compliance.

Pre-existing broader gaps observed and explicitly **not remediated/verified** here: password registration/email-verification responses disclose username/email existence; primary-password per-account failed-attempt controls remain a separately documented gap; `recordLoginAudit` currently covers successful logins only and registration failure security events lack a centralized audit. No credential verification/session lifecycle changes or logging of passwords/invitation codes/tokens were introduced. Root was notified; broader authentication hardening requires separately scoped work. Hosted execution is still an unresolved verification gate for every affected requirement.

### Code-spec handoff for root (root owns spec edits)
1. **Scope/trigger**: all new anonymous account writers and referral-related settings must distinguish registration entitlement from attribution.
2. **Signatures**: `InsertUserWithInvitation(tx *gorm.DB, user *User, inviterID int, code string) error`; `invitation_code` versus `aff_code`/`oauthFlowPayload.AffiliateCode`; `InviteRegisterEnabled` versus `RegisterAffiliateLinkEnabled`.
3. **Contracts**: mandatory invitation is consumed transactionally with account/binding; affiliate never substitutes; affiliate default true, invite default false; OAuth entitlement comes only from server flow payload; login-only paths never silently become provisioning.
4. **Validation/error matrix**: missing/invalid/disabled/expired/used invite -> generic `ErrInvitationInvalid`; rollback restores code; invalid setting -> no write/publication; failed option persistence -> prior runtime policy retained.
5. **Good/base/bad**: good valid one-use code + optional referral; base open registration unchanged when invitation requirement is off; bad treating a reusable affiliate URL as invitation proof.
6. **Tests required**: existing `TestInvitation*` matrix plus independent settings control regression, all three primary DB engines and focused race.
7. **Wrong/correct**: wrong `invitationCode = affCode` or bypassing gate for SSO; correct pass separate fields to the shared transactional invitation writer. Keep admin/bootstrap provisioning explicit and privileged.

## Change boundary
Finish R20 only: close proven server-side registration-policy bypasses, preserve existing option keys/defaults, make affiliate attribution independently configurable and explicit, add focused regression cases to the existing invitation test file, reuse existing settings UI. Do not change credential verification, billing, administrator provisioning, or peers' unrelated edits.
