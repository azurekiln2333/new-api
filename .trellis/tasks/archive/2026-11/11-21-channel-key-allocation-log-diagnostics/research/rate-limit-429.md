# Panel invitation/register and API 429 investigation and fix boundary

Active parent: `.trellis/tasks/11-21-channel-key-allocation-log-diagnostics`.
Worker: pi-logs. Root: Herdr `w4C:p16`. The user clarified that the affected
traffic is **panel invitation registration and `/api` calls**, not `/v1`
inference. Research was read-only; root subsequently granted the exact backend
scope below. No new user diagnostic request or permission prompt is required.

## Evidence before changes

### Proven code path

1. Axios constructs `Request failed with status code 429` in
   `web/node_modules/axios/lib/core/settle.js:20` (root verified the literal).
2. `web/src/features/auth/sign-up/components/sign-up-form.tsx:onSubmit` calls
   `features/auth/api.ts:register`, posting `invitation_code` to
   `POST /api/user/register`. Invitation registration has no separate public
   endpoint or limiter. `controller/user.go:Register` validates/consumes the
   invitation only after route middleware succeeds.
3. `router/api-router.go` applies `GlobalAPIRateLimit()` to **every `/api` route**,
   before authentication: per-ClientIP GA, default **360 requests/180 seconds**.
4. Registration, password login, MFA/passkey entry, OAuth state/callback,
   refresh/logout, ratio configuration and many panel mutations all call the
   original `CriticalRateLimit()`, which uses **one CT key per ClientIP**, default
   **20 requests/1200 seconds**. It is not a per-session or per-account limit,
   nor a concurrency cap. Every attempted request counts before business logic,
   successful, invalid, anonymous refresh and failed login alike.
5. `middleware/rate-limit.go` returns a bodyless 429 with `Retry-After`. Without
   `message_key`/message the shared frontend error resolver can fall back to the
   Axios error's generic text. This explains a source of the observed literal,
   not an assertion that a browser/network trace was captured.

Consequences proven by construction: several users/tabs sharing one NAT/proxy
IP share CT, and unrelated panel critical actions/refreshes can exhaust the
registration allowance. A valid invitation does not bypass CT. Ordinary `/api`
requests can independently exhaust the broader GA allowance. Lowering model
request limits or changing invitations cannot repair these shared namespaces.

### Algorithms, topology and bounds

- Redis: one Lua `INCR` + `EXPIRE` + decision, first-request anchored fixed
  window; attempts over the limit also increment but do not normally extend TTL.
  Missing TTL is repaired. Keys are `rateLimit:v2:ip:<mark>:<ClientIP>`.
- In memory: process-wide synchronized sliding window of admitted requests,
  key `<mark><ClientIP>`, second timestamps, idle LRU cleanup. Rejects do not
  append; Retry-After conservatively uses the whole configured window.
- Redis nodes using the same Redis logical database/namespace share counts.
  Nodes using different stores do not. Memory counters are per process, so a
  load balancer can make symptoms intermittent; changing to memory is not a
  safe correction for distributed anti-abuse protection.
- Both APIs preserve fixed configured max/window semantics. Generic Redis
  storage errors fail with 500, not 429. Email-verification Redis error alone
  falls back to a local counter.
- `EmailVerificationRateLimit`: EV/IP **2 requests/30 seconds**, explicit JSON
  error; registration email code delivery remains separately protected.
- `UserCriticalRateLimit`: verified context user ID and static business mark,
  only after authentication. It does not replace anonymous/IP protections.

### Account-attempt review and security gap

`controller/user.go:Login` / `model/user.go:ValidateAndFill` do **not** provide a
primary-password per-account failed-attempt counter in the reviewed path.
Existing session issuance quota (default **100 per user/24h**, active limit50)
counts successfully created sessions, not bad password guesses. Its 429 is a
separate structured `AUTH_SESSION_ISSUANCE_LIMIT`, while active-limit failure
is409. MFA has durable failed-code protection (5 failures/300s), email binding
has its own max5 attempts and resend policy. Turnstile remains defense in depth,
not a substitute for throttling.

Therefore this narrow patch must **not** remove anonymous IP limits, make them
session-only, use submitted username/invitation as authority, or claim complete
credential-stuffing protection. Account-aware password-guess defense remains an
explicit pre-existing gap for root's security backlog; do not expand this fix
into credential parsing/session redesign.

### Proxy, fallback and frontend hypotheses

- `main.go` installs `ConfigureTrustedProxies`; default trust is loopback,
  RFC1918 and IPv6 ULA. Explicit TRUSTED_PROXIES replaces defaults; `none` ignores
  forwarded headers. A public proxy absent from the trusted chain can collapse
  users to its peer address. Missing or improperly overwritten forwarding can
  similarly collapse IPs. Trust-all is not a safe remedy.
- Correctly registered `/api` calls do not traverse `GlobalWebRateLimit`.
  Unknown/mis-prefixed paths reach the web fallback and consume GW before 404;
  existing frontend assets use a separate, disabled-by-default static bucket.
- Frontend refresh is singleflight per tab and serialized by browser locks,
  but a fresh request can be triggered after401/bootstrap.429 is transient and
  does not itself clear sessions or cause an automatic refresh retry loop.
  Multiple tabs/users behind an IP still spend the same original CT allowance.
- Production QueryClient retries queries on429 (up to its configured attempt
  bound); it does not exclude429 or honor Retry-After in the reviewed callback.
  This can amplify GA traffic but is not proof of the deployed incident cause.
- No production proxy/env/browser/Redis data has been supplied. Do not state that
  all users were one IP, that Redis is broken, or that a specific online bucket
  was exhausted. Root requested code-first investigation, not another user
  diagnostic round.

## Approved minimal implementation design (persisted before code edits)

### Boundary and files

- `middleware/rate-limit.go`: introduce **closed, server-defined critical scopes**
  and structured429; retain the current algorithms and max/window values.
- `router/api-router.go`: bind real routes to stable login, registration,
  session-refresh, session-logout or panel scopes. Preserve auth/origin/body-size/
  Turnstile/proof middleware order and all peer route edits.
- `middleware/rate_limit_test.go`: extend existing regressions, no scattered new
  test files.
- Existing `router/access_token_scope_test.go`: add compact real-router alias
  regressions (test-only claim sent to root). Exercise early no-DB controller
  rejections rather than persisting users/auth flows. No DB mutations needed.
- pi-ui owns frontend error handling/locale edits. Reuse existing seven-language
  key **`Too many requests`**, sent to root/pi-ui; no new translation key needed.

### Counter contract

Five stable business scopes, not a key per URL, path parameter, invitation,
username, header, cookie or session. All remain **IP-keyed**, bounded and
independent; caller identity is not inferred from unverified claims.

| Scope | Grouping |
| --- | --- |
| login | Password, MFA, passkey begin/finish variants, OAuth state and login/callback aliases; recovery alternatives remain grouped here |
| registration | Password + invitation registration share the same route/scope |
| session-refresh | Refresh alone, origin guard unchanged |
| session-logout | Logout alone, origin guard unchanged |
| panel | Remaining critical panel/security/key/payment actions; existing verified-user controls remain additional |

`CriticalRateLimit()` becomes the compatibility **panel** entry point;
`CriticalRateLimitFor(scope)` only accepts a closed compile-time scope enum.
All login aliases reuse the existing **CT** mark so rollout does not reset the
live anonymous login anti-brute-force bucket. New scopes use stable bounded
CT-prefixed marks. Old nodes during rolling deployment may still impose the
legacy shared constraint; synchronized rollout makes isolation effective.
Do not use arbitrary `c.FullPath()` as a scope or dynamically trust OAuth intent
from its body to obtain another allowance.

The GA aggregate remains unchanged and continues to bound all panel API traffic
from one IP. The patch intentionally removes accidental CT cross-business
coupling, not the operator's broader abuse ceiling or real per-IP sharing.

### Error contract

429 remains429; preserve Redis remaining-TTL Retry-After / conservative memory
Retry-After. Return `success:false`, `code:RATE_LIMIT_EXCEEDED`,
`message_key:"Too many requests"`, English `message`. No IP, user ID, raw token,
invitation, submitted credentials or internal scope/key in response. Use existing
`common.ApiErrorStatus`/`common.NewMessage`; do not add raw JSON encoders.

### Regression plan

1. Memory + Redis: exhausting registration does not block login/refresh/logout/
   panel; exhausting refresh does not block registration; same scope remains
   enforced at exact configured threshold/window with structured JSON and hint.
2. Same IP, different pretend cookies/headers/sessions: cannot rotate anonymous
   scope quota. Different ClientIP can use an independent allowance.
3. Redis: separate router instances sharing one store share the same scope;
   aliases do not mint a second allowance, expiry retains current semantics.
4. Actual SetApiRouter: all registered login/passkey/OAuth aliases share login
   scope, malformed invitation registration remains registration, refresh/logout
   independent. Controllers return early on missing input/disabled feature, so
   no DB writes or usable auth tokens enter fixtures.
5. Existing concurrency/TTL/failure/static-bucket and trusted-proxy regression
   selectors remain green; run focused memory/Redis tests and race checks.
6. Preserve HTTP401/403 auth failures and no role/scope bypass. No `/v1` edits,
   pricing/accounting changes, dependency upgrades or database matrix required.

## OWASP references actually read

- Current official Authentication and Session Management Cheat Sheets, downloaded
  from OWASP CheatSheetSeries and fully read. Root's corresponding official HTML
  snapshots also available locally at `root-owasp-authentication.html` and
  `root-owasp-session.html`; no repeated remote fetch is required.
- Root's official ASVS HTML identifies stable **5.0.0**. Tagged CSV controls read:
  **V6.1.1**, **V6.3.1** (document/retain anti-automation, avoid malicious lockout),
  **V6.6.3** (OOB brute-force), **V7.2.1/V7.2.4** (server auth/session boundaries),
  **V15.3.4** (trusted original IP for limits), **V16.2.5** (no credential logs).
- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- https://asvs.owasp.org/
- https://raw.githubusercontent.com/OWASP/ASVS/v5.0.0/5.0/docs_en/OWASP_Application_Security_Verification_Standard_5.0.0_en.csv

This fixes a bounded cross-scope bug; no whole-ASVS compliance claim. Actual
implementation/test results will be appended after execution, not inferred from
this design.

## Root frontend verification (2026-11-23)

The UI worker hit repeated provider 403/503 errors; root explicitly took its
three-file mapper/test scope, preserving all prior UI work. The shared mapper
now translates an otherwise bodyless/HTML HTTP 429 to the existing seven-locale
`Too many requests` key, after specific server message/code and safe
AuthOperationError handling. No auth/session protocol or locale value changed.

Before the one-line mapper fix, the new regressions failed 3/40 assertions
(raw Axios text instead of a useful localized message). After the fix, all
52 tests in the mapper, notifications, auth API and invitation registration
suites passed. Tests retain specific session issuance errors, specific backend
messages, safe authentication wrappers, and Chinese localization.

Bun 1.4.4 from the existing portable toolchain; commands from web/:

- bun run test src/lib/server-error-message.test.ts src/lib/__tests__/server-error-notifications.test.ts src/features/auth/api.test.ts src/features/auth/sign-up/__tests__/registration-modes.test.tsx
- bun check -b (1,600 files / 2 projects, no errors)
- bun x --no-install oxlint -c .oxlintrc.json on those three changed mapper/test paths (no errors)
- bun run build (passed)
- bun run i18n:sync (passed; existing key is present in all seven locales)

Evidence in the external runtime directory: root-panel429-web-before.log,
root-panel429-web-after.log, root-panel429-web-build.log,
root-panel429-web-typecheck.log, root-panel429-i18n.log.

## Backend implementation checkpoint — paused for shared compilation

The approved changes are saved in exactly these R8 code/test files:

- `middleware/rate-limit.go`: closed `CriticalRateLimitScope` enum and
  `CriticalRateLimitFor`, preserving legacy `CT` login counts and configured
  thresholds/windows. The compatibility `CriticalRateLimit()` now uses the
  separate panel scope. Generic429 responses carry `RATE_LIMIT_EXCEEDED`,
  `message_key: "Too many requests"` and the existing Retry-After value.
- `router/api-router.go`: registration, refresh, logout and panel are separated;
  all password/MFA/passkey/OAuth login and recovery alternatives share login.
  Existing global GA, authenticated user limits, origin/auth/body/Turnstile
  ordering and peer route integrations are retained.
- `middleware/rate_limit_test.go`: memory/Redis scope separation, exact caps,
  same-IP claims do not reset limits, separate-engine shared counts, aggregate
  GA protection, and preservation of an already exhausted Redis login window.
- `router/access_token_scope_test.go`: actual `SetApiRouter` login-alias,
  invitation-registration/session independence and GA aggregate regressions.
  They deliberately use early input errors/no-cookie operations, with no DB
  setup, writes or usable credentials.

Executed focused middleware command:

```text
GOWORK=off go test -p 1 -count=1 -v ./middleware -run '^(TestCriticalRateLimit.*|TestRedis.*|TestGlobalWebRateLimit.*|TestConfigureTrustedProxies.*)$'
```

**Passed**, 3.945s (`pi-logs-r8-middleware-focused.log` in the external runtime
folder). This includes the existing Redis atomic concurrency/TTL/failure policy,
email-verification response, web/static-bucket and trusted-proxy assertions.
A later fixture-only change gives the different-IP assertion its own generated
address for repeated-run isolation; final verification must include that saved
version too.

The first actual-router focused command did **not execute tests**, because its
shared dependency was in the middle of a peer edit:

```text
GOWORK=off go test -p 1 -count=1 -v ./router -run '^(TestPanelCriticalRateLimit.*|TestAccessTokenRouteRulesCoverEveryDashboardRoute)$'
```

Build errors captured in `pi-logs-r8-router-focused.log`:

- `service/task_billing.go:174: undefined: model.ClaimSubmissionChargePublish`
- `service/task_billing.go:182: assignment mismatch: 2 variables but model.ApplySubmissionChannelBudgetSettlement returns 1 value`

These are outside pi-logs' granted files and were sent to root/runtime owner;
no temporary billing fix or signature shim was introduced. Root explicitly
requested **pause router reruns until compile-ready**. Full middleware/router,
focused actual-router execution, rate-limit race checks and final vet remain
pending; do not call the R8 backend fully verified from the earlier middleware
pass. Latest four saved files pass `git diff --check` and `gofmt -l` is empty.

Root owns the new R8 executable contract in existing
`.trellis/spec/backend/error-handling.md`; pi-logs will not edit that spec.
No production database access, `/v1` change, staging, commit, new worker or
additional external-guidance fetch was performed for this checkpoint.

## Backend execution results — final (provider restored, compile-ready)

After root independently verified `GOWORK=off go build ./...` and
`go vet ./controller ./service ./model ./router ./middleware` exit 0, the paused
runs were executed against the shared compile-ready tree.

```text
GOWORK=off go test -p 1 -count=1 ./middleware ./router
  -> ok middleware 6.471s ; ok router 9.251s          (full packages, exit 0)
GOWORK=off go test -race -p 1 -count=1 -v ./middleware ./router \
  -run '^(TestCriticalRateLimit.*|TestRedis.*|TestGlobalWebRateLimit.*|
         TestConfigureTrustedProxies.*|TestPanelCriticalRateLimit.*|
         TestAccessTokenRouteRulesCoverEveryDashboardRoute)$'
  -> ok middleware 4.497s ; ok router 4.846s           (exit 0)
GOWORK=off go vet ./middleware ./router -> exit 0
```

Environment for the full run: `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN` pointed
at the disposable `newapi_logs` database (loopback 23306 / 25432) so any
external-DB fixture in the two packages exercised real MySQL/PostgreSQL SQLite
and PostgreSQL/MySQL paths, not only SQLite.

Actual-router assertions observed as `--- PASS` in the focused run, both
`redis=false` and `redis=true` subtests:

- `TestPanelCriticalRateLimitLoginAliasesShareAllowance`
- `TestPanelCriticalRateLimitRegistrationAndSessionsAreIndependent`
- `TestPanelCriticalRateLimitScopesStillShareGlobalAPICeiling`
- `TestAccessTokenRouteRulesCoverEveryDashboardRoute`

Together with the middleware package, this confirms login aliases share one
allowance, registration/refresh/logout/panel are independent, the global GA
ceiling is still shared, and the static route/scope table stays consistent.

An earlier focused run (`pi-logs-r8-focused-ready.log`) exited 1 while every
assertion passed: `vet.exe: model\channel_budget_adjust.go:346:28: undefined:
math` — a peer file mid-edit in the shared tree, not an R8 file. The clean full
and race runs above supersede it. The two build errors in the earlier paused
checkpoint (`ClaimSubmissionChargePublish`, `ApplySubmissionChannelBudgetSettlement`
arity) are likewise resolved in the current tree.

Formatting: `gofmt -l` empty and `git diff --check` clean for all four owned
files. Logs: `pi-logs-r8-full-middleware-router.log`,
`pi-logs-r8-race.log`, `pi-logs-r8-focused-ready.log`.

Not verified and not claimed: ClickHouse-backed paths (no local ClickHouse),
and no whole-ASVS compliance statement. R8 closes one bounded cross-scope
bug; the pre-existing absence of a per-account password-guess counter remains a
separate gap.
