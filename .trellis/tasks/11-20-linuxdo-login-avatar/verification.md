# R23 independent review — 2026-11-20

Reviewer: `/root/r2_finish`. Root owns staging, requirement commit, external database CI and release. No credentials were used or recorded by this review.

## Result and implementation boundary

LinuxDO reads the documented `avatar_template` claim using tolerant raw JSON decoding. Only an already complete absolute HTTPS address is accepted through `NormalizeAvatarURL`. Missing, null, blank, malformed, relative, protocol-relative and unresolved template values preserve the previous avatar and do not reject otherwise valid authentication. No gateway image fetch is added.

The primary Linux DO wiki source at commit `26cd5730d92b5c250bddf75c154c33fe371939c7`, `content/Community/LinuxDoConnect.mdx`, confirms the claim name and the userinfo endpoint but contains no concrete value establishing the relative base host or placeholder syntax. Discourse source has `{size}` templates, but that is not evidence for the LinuxDO Connect response contract. Root explicitly approved narrowing the implementation to complete HTTPS addresses. Relative path resolution against `connect.linux.do` and `{size}` replacement were removed. Extending template support requires a verified provider fixture or source contract; the current change does not claim to support those unresolved forms.

The provider avatar decision is captured on the server after primary provider authentication. It is applied only after successful session/auth-bundle issuance. MFA and Passkey retain that decision in the server-owned AuthFlow payload; rejected, banned, stale, expired or consumed flows do not write an avatar. Bind, refresh and profile reads do not synchronize. Avatar persistence failure cannot undo a successful login. `UpdateUserSetting` calls `PublishUserAuthCache`, preserving the existing cache publication path.

## Findings fixed

- `oauth/avatar.go`: both avatar entry points now honor `SupportsAvatar() == false`, instead of checking only whether the capability interface exists. A regression failed before the change and passed afterward.
- `oauth/linuxdo.go`: removed unsupported relative host and template guesses. Both existing response decoders now use `common.DecodeJson`, as required by the host JSON wrapper convention.
- `oauth/avatar_test.go`: added actual GetUserInfo mapping of a complete HTTPS claim; missing/null/blank/wrong-type and unresolved claim cases; explicit preservation of a stored avatar for unsupported URLs; disabled provider capability regression. Earlier tests that asserted the invented Connect-host template were replaced.
- `controller/auth_flow_test.go`: added actual GORM setting-write failure while verifying successful issued login, consumed MFA-flow replay nonmutation, and a real signed Passkey completion that ignores a client-injected pending avatar. Wrong OTP input uses deterministic `invalid`, not a potentially valid six-digit code.

## File ownership / handoff

R23 production changes span `oauth/avatar.go`, `oauth/linuxdo.go`, `oauth/provider.go`, `service/login_verification.go`, `controller/oauth.go`, `controller/user.go`, `controller/login_verification.go`, `controller/passkey.go`, and `controller/wechat.go`.

Tests are in existing `oauth/avatar_test.go`, `controller/auth_flow_test.go`, and `controller/auth_session_test.go`. The controller auth fixture supports `TEST_SECURITY_DIALECT` and reuses the isolated, loopback-guarded real database fixture. No schema column or migration was introduced by this requirement. Shared files must be staged by owned hunks rather than wholesale if parallel work is present.

## Verification

Portable Go: `D:/AppData/Local/Temp/new-api-build-tools/go/bin/go.exe`; `GOMAXPROCS=2`; all commands run from repository root with `-p 1`.

Passed before the final normalization restriction:

```text
go test -p 1 ./oauth ./service ./controller -run 'Test.*(Avatar|LinuxDO|LoginVerification|LoginCode)|TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful' -count=1
go test -p 1 ./oauth ./controller -run 'Test.*(Avatar|LinuxDO)|TestSecurityLogin|TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful' -count=1
go test -p 1 ./oauth -count=1
```

The second invocation passed with the reviewer-added MFA replay, real signed Passkey and failed avatar DB write cases. The service package had no tests matching the first expression.

After the final restriction:

- `go test -p 1 ./oauth ./controller -run 'Test.*(Avatar|LinuxDO)|TestSecurityLogin|TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful' -count=1`: OAuth passed (5.448s); controller compilation was temporarily blocked by parallel R22 edits in `controller/channel_test_request_test.go` (unused imports). Do not infer a final controller pass from that invocation.
- `go vet -p 1 ./oauth ./service`: passed.
- Scoped `git diff --check`: passed.
- Modified Go files were formatted with the portable `gofmt`.
- R23 introduces no frontend change; frontend lint/typecheck are not applicable to this requirement.

Full `go vet -p 1 ./oauth ./service ./controller` was initially blocked by concurrent R9 stream code, then by R22 `fingerprintBoundedAdaptor` editing. Re-run after those owners release their changes; do not treat these transient failures as passed checks.

Final re-run after the concurrent compilation blockers cleared:

- `go test -p 1 ./oauth ./controller -run 'Test.*(Avatar|LinuxDO)|TestSecurityLogin|TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful' -count=1`: **passed** (OAuth 5.003s; controller 17.855s).
- `go vet -p 1 ./oauth ./service ./controller`: **passed**.

The final re-run supersedes the transient controller compilation blockers above. External engine verification below remains pending.

## External database gate

Local checks use real SQLite. This workstation has no available MySQL/PostgreSQL instances, so external compatibility remains pending; no all-database compatibility claim is made.

For each real MySQL and PostgreSQL CI engine:

```text
TEST_SECURITY_DIALECT=mysql|postgres
TEST_MYSQL_DSN=<test-owned-loopback-dsn> | TEST_POSTGRES_DSN=<test-owned-loopback-dsn>
go test -p 1 ./controller -run 'TestOAuthLogin.*Avatar|TestUserSettingsPreserveProviderAvatar|TestSecurityLogin' -count=1
```

Record exact engine versions and results in the root release ledger. The isolated fixture migrates AuthFlow, User, UserSession, AuditLog, TwoFA and PasskeyCredential; missing or failed configured databases must not be silently counted as passing.

## Security guidance and scope

Reviewed official OWASP Authentication, Session Management, OAuth2 and applicable MFA guidance from the locally retrieved CheatSheetSeries commit `c2a1e3e32cec12f27be0a3b1e771ce51288a363e` (2026-11-19). Latest stable ASVS was verified as **5.0.0** (`v5.0.0_release`, 2025-05-30); the newer `latest` release is bleeding edge, not stable.

Applicable ASVS references checked: 6.3.4 (consistent authentication assurance), 6.5.1 (single use), 6.5.5 (bounded validity), 6.8.1 (provider-bound identity), 7.2.1 (trusted server verification), 7.2.4 (new authenticated session), and 10.2.1 (OAuth state/PKCE CSRF protection). This change preserves existing provider-state/session checks and adds server-owned post-authentication avatar decisions; it does not claim complete application-wide ASVS compliance. Tests cover failed session issuance, failed/stale/expired/replayed verification, client injection, and nonfatal avatar persistence failure. No avatar/credential/session values were added to audit output.

## Spec synchronization recommendation

Retain the post-completed-authentication-only avatar write and unverified-claim preservation contracts in the parent security/spec handoff. No new standalone spec was created while root coordinates parallel spec updates.
