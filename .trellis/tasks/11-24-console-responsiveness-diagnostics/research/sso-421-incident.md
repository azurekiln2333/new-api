# SSO 421 incident handoff — 2026-11-25

**Status: SSO 421 reproduced; user confirms primary Passkey FINISH returns server JSON `SECURITY_VERIFICATION_FAILED` after credential/fingerprint confirmation. Neither incident is fixed; cause unresolved.** User reports the preceding release worked and declines infrastructure access. Preserve the regression hypothesis; do not assert the two failures are independent. After the original SSO probes stopped, root authorized one normal unauthenticated Passkey BEGIN and one replacement after a response-reporting error; safe replacement evidence is below. No further probes, FINISH/replay, credentials, SSH/configuration changes, login-control weakening or access requests. No staging/commits, rollback or local checks by this worker. Shared locales untouched; **incident source-file needs: none**.

## Immediate existing administrator alternatives

Public `/api/status` returned HTTP 200 with `x-new-api-version: custom-20261125-271b89f` and these non-secret flags:

- `password_login_enabled=false`: no password/reset workaround recommended.
- `passkey_login=true`, RP/RP list `newapi.azurekiln.cn`, `passkey_allow_insecure=false`.
- `linuxdo_oauth=true`, minimum trust 2; WeChat disabled.

Released `271b89fcf` route `web/src/routes/(auth)/sign-in.tsx` serves **https://newapi.azurekiln.cn/sign-in**. Its released `UserAuthForm` shows **Sign in with Passkey**, independently of SSO/password availability. **Correction to the initial alternative-path advice:** the user confirms a previously enrolled Passkey also fails after credential/fingerprint confirmation; the enabled flag and successful diagnostic BEGIN do not establish usable administrator access. An already-valid administrator refresh session or previously linked LinuxDO identity are existing, conditional routes only, not verified working incident workarounds. No new OAuth account is implied to be an administrator.

## Passkey BEGIN evidence and source trace

One initial BEGIN received a response, but the local safe-output serializer errored before reporting status/body; it was not replayed. Root explicitly authorized one replacement. No returned challenge/flow token was logged, retained in the report or submitted to FINISH.

- Request: **POST https://newapi.azurekiln.cn/api/user/passkey/login/begin**, JSON `{}`, `Origin: https://newapi.azurekiln.cn`, `Referer: https://newapi.azurekiln.cn/sign-in`, Accept JSON, Cache-Control `no-cache, no-store`; normal certificate validation, cookie-enabled empty jar. Released source requires no preflight session/CSRF token for this anonymous primary-login BEGIN; no guard was relaxed.
- Result: **HTTP 200, success=true, message="", 4,167 ms**, HTTP/1.1. Local completion **2026-11-25 12:10:10.258 UTC / 20:10:10.258 Asia/Shanghai**. Response Date **12:10:12 UTC / 20:10:12 Asia/Shanghai**; these are separate clocks, not a derived server duration.
- Safe data: fields `expires_at`, `flow_token`, `options`, `rp_ids`; flow token/challenge present (values excluded); `rpId`/`rp_ids=newapi.azurekiln.cn`, `userVerification=required`, timeout `120000` ms, no allowCredentials (discoverable login).
- Headers: `x-new-api-version=custom-20261125-271b89f`, Content-Type `application/json; charset=utf-8`, Cache-Control `no-store, must-revalidate, no-cache, max-age=0, private`, Server `cloudflare`, CF-Cache-Status `DYNAMIC`, CF-Ray `a6008c7ceef62637-HKG`, `x-oneapi-request-id=202611251210113893931928268d9d6rnQhcMbm`.
- Request Cookie names: **none**. Response Set-Cookie names: **none**. No FINISH request, assertion/private key, credential/account enumeration or login was attempted.

Released client sequence is `beginPasskeyLogin` (POST above, `skipAuthRefresh=true`) → `requestPasskeyAssertion` converts challenge and awaits `navigator.credentials.get` → builds assertion → **POST /api/user/passkey/login/finish** with flow token and credential → applies login result. `/api/user/login/passkey/begin` is a different, secondary-verification path, not the primary Sign in with Passkey button.

**Superseding hard user evidence:** the user identifies **`/api/user/passkey/login/finish`** and provides the server response `{"code":"SECURITY_VERIFICATION_FAILED","message":"Verification failed. Please try again.","message_key":"Verification failed. Please try again.","success":false}` after selecting the credential/fingerprint. This is an application verification rejection, not just a generic browser NetworkError. Its HTTP status/request id/serving node were not supplied; released mapping normally uses HTTP 200 for this business error, but that is a source expectation, not an observed user response status.

### Exact released FINISH return-site narrowing

`controller/passkey.go:320–420` is the primary FINISH. `controller/secure_verification.go:87–88` maps `service.ErrVerificationFailed` **or any `*protocol.Error`** to this code. The primary path can reach that mapping from:

1. **Assertion parse** at `passkey.go:331–334`: library protocol parse/type/base64/authenticator-data failures (some plain JSON errors instead map internal error).
2. **`ValidatePasskeyLogin`** at `passkey.go:391–394`: dependency `go-webauthn/webauthn v0.14.0` requires discoverable flow/nonnull userHandle, wraps the application's credential/user lookup callback failures in `protocol.ErrBadRequest`, and then verifies credential/userHandle ownership, challenge, signed origin, RP-ID hash, UP/UV, public key/signature and backup flags. The callback at `passkey.go:357–388` queries credential/user from the primary DB; missing credential, DB lookup failure, stored nonempty credential RP mismatch (`:364–365`), disabled user or numeric userHandle mismatch can thus be wrapped into the same generic protocol code. Do not infer any particular one from the response alone.

Other stages have **different expected released codes**:

- `PopSessionDataFlow` (`passkey.go:337`) consumes the primary-DB flow; missing/expired/replayed flow or mismatch of cross-node `SessionSecret` (HMAC lookup in `model/auth_flow.go:144–145`) yields **AUTH_FLOW_INVALID**, not the supplied code. Stored WebAuthn session expiry is checked in `service/passkey/session.go:75–79` (2-minute ceremony; 5-minute flow envelope). No retry of consumed FINISH is safe.
- `BuildWebAuthnForRPID` (`:346`) unavailable request Origin/RP configuration yields **PASSKEY_RP_ID_UNAVAILABLE**; a different **signed clientData origin** during protocol verification still yields the supplied verification code.
- Missing required UV in stored session yields **AUTH_FLOW_INVALID** (`:352–354`).
- Post-success credential-state update (`:414`) and login session issuance (`:420`) normally use PASSKEY/AUTH/internal codes, not this protocol verification code. Login-session Redis cache publication is **after** protocol verification; no evidence currently implicates it in this specific response. Rate-limit Redis failure before controller is HTTP 500 / limit is 429, not this code.

Released `controller/secure_verification.go:125–131` already logs only `passkey verification rejected: code=<fixed> reason=<allowlisted protocol.Type> rp_id=<public RP>` with request context. Safe categories include `invalid_request`, `parse_error`, `auth_data`, `verification_error`, `invalid_signature`; user lookup failures are wrapped as `invalid_request`, and **v0.14.0 groups challenge and signed-origin mismatch under `verification_error`**, so even this reason is not a full cause. `rp_id` is set only after flow consumption and successful WebAuthn construction, which can help locate an early parse rejection without exposing data.

The smallest outstanding evidence is the **existing failed FINISH's response request id, serving node if already available, and fixed logged reason/RP**—no new retry, infrastructure access request or raw error/DevInfo/body. Raw protocol DevInfo can contain challenge material and must not be shared. Root cannot access the user's browser native-control pipe; do not imply the full failure was directly observed by this worker.

Safe existing-log interpretation for this exact FINISH (not new diagnostics or a request for infrastructure access):

| Existing fixed reason/RP | What it narrows; what it does not prove |
|---|---|
| `invalid_request` / `parse_error`, blank `rp_id` | Protocol parsing precedes setting `passkey_rp_id`; malformed assertion/type/authenticator-data is the relevant stage, not Redis session issuance |
| `invalid_request`, nonempty `rp_id` | Discoverable-userHandle, credential lookup/stored RP binding, user ownership or backup-flag checks; library wraps callback errors, so this alone cannot distinguish missing credential from a primary-DB failure |
| `verification_error` | Ceremony/challenge/signed-origin/top-origin **or RP hash/UP/UV** validation; this library groups them, so the category alone cannot pick one. Fingerprint confirmation does not prove the signed UV flag passed verification |
| `invalid_signature` | Stored public-key parsing or assertion signature verification, not flow-token lookup; never remove the signature check |
| `invalid_key_type` / `unsupported_key_algorithm` | Key/algorithm verification branch, not a dependency upgrade in this release range |
| `AUTH_FLOW_INVALID` (different code) | Missing/replayed/expired flow, cross-node HMAC secret mismatch or stored session expiry; not the user's supplied code |

No concrete source regression was found in this released verification path. `go.mod`, `go.sum`, controller/error mapper, Passkey service/session/user wrapper, credential model, auth-flow model, Passkey settings, browser API/assertion/conversion files all have identical old/current Git blobs. In particular, the WebAuthn dependency remains **v0.14.0**. Counter rollback in that dependency sets `CloneWarning`; it does not itself return this verification failure, so a counter-reset workaround is unsupported. Runtime state/configuration/credential differences remain unproven hypotheses; do not erase/re-enroll credentials, disable checks or claim a fix from source equality.

Redis comparison: old `common/redis.go` always sets PoolSize from `REDIS_POOL_SIZE`, fallback 10; new only overrides when that environment variable is nonempty, preserving URL/library settings otherwise. Same go-redis v8.11.5 dependency. This is a process-wide shared-client runtime difference, not an auth-flow schema/secret change; actual live pool settings are unknown. BEGIN crosses configured global API and critical-login rate limit middleware, which use shared Redis when enabled. Passkey `CreateSessionDataFlow` → `CreateAuthFlow` stores challenge/session payload in the **primary database**, not Redis. FINISH consumes that DB flow, validates RP/origin/signature/UV/account, updates assertion state, then creates a login session. Session creation reads user Redis cache (DB fallback on normal misses/read failures), persists DB session, and attempts session-cache publication (ordinary publication errors log; version-fence/inactive checks remain). Thus shared Redis contention can affect admission or later session/cache latency, but this successful BEGIN cannot rule out intermittent/FINISH/runtime effects or prove Redis caused either failure. Do not disable rate limits, version fences, signature/UV checks or TLS verification.

## Redacted public SSO incident evidence

Public status identifies custom provider `AzureKilnSSO`, slug `azurekiln-sso`, public client id `newapi`, endpoint `https://sso.azurekiln.cn/oauth2/authorize`, scopes `openid profile email`. The user confirmed that failure path and callback `https://newapi.azurekiln.cn/oauth/azurekiln-sso`.

Only public query parameters `client_id`, `redirect_uri`, `response_type=code`, `scope` were used. **No OAuth state/code, cookies, credentials or tokens were sent/replayed.** Redirect following was disabled; no token exchange/callback invoked.

All dates below are **November 25, 2026**; Asia/Shanghai is UTC+08:00.

| UTC | Asia/Shanghai | Credential-free observation |
|---|---|---|
| 11:51:32–33 | 19:51:32–33 | SSO `/` and `/oauth2/authorize` without query both return 421; no Location; HTML title `421 Misdirected Request`; Server `cloudflare` |
| 11:52:41 | 19:52:41 | Public authorization query, fresh HTTP/1.1: 421, no Location, nginx HTML marker; CF-Ray `a60072d459e28542-HKG` |
| 11:52:42 | 19:52:42 | Same public query, HTTP/2: 421, no Location, nginx marker; CF-Ray `a60072db2a2a04ac-HKG` |
| 11:54:48–50 | 19:54:48–50 | Fresh TLS connection, SNI **and** Host `sso.azurekiln.cn`, ALPN HTTP/1.1, default certificate validation succeeds; same query returns 421, no Location, `cf-cache-status: DYNAMIC`, nginx marker; CF-Ray `a60075ff0b057a53-HKG` |

The validated leaf CN/SAN was `sso.azurekiln.cn`, issuer Cloudflare TLS Issuing ECC CA 4, validity 2026-10-30 through 2027-01-28; TLS 1.2. NewAPI public status remained 200/version above.

**Known:** the observed failed hop is the SSO authorization hostname before any redirect/callback. Fresh single-host HTTP/1.1 reproduction means browser HTTP/2 coalescing alone does not explain it. **Unknown:** which edge/origin component generated 421, its received Host/SNI and selected route, and whether deployment/runtime configuration changed. Cloudflare headers/nginx HTML do not establish cause or disprove a release-triggered operational regression.

## Exact release/blob comparison

Git source inspection compared:

- Previous: `53a71f88d5b568847cd7233a08f934824dcb22d3`
- Current: `271b89fcff4f6cfeedcef0bb4bb81bb630c17d57`

`git diff 53a71f88d..271b89fcf` touches only 11 capacity/relay files: `common/{log_text.zh-CN.json,redis.go}`, `controller/{channel_pin_retry_test.go,relay.go,relay_task_plugin_test.go}`, `service/{channel_capacity.go,channel_capacity_test.go,channel_select.go,channel_select_auto_groups_test.go,channel_wait.go,request_policy.go}`. No auth/frontend/router/proxy/deployment/workflow file changes occur in this range. Redis default pool sizing changes globally, so indirect shared-resource/runtime effects remain unexcluded.

Each following path has **exactly the same blob in both releases**:

| Path | Identical old/current blob |
|---|---|
| `web/src/features/auth/hooks/use-oauth-login.ts` | `4215ffef884b07e62ee319a347b3f74876816ed1` |
| `web/src/lib/oauth.ts` | `5c4066eb0e34d2cd44e5daa8c974528f6e95cf65` |
| `controller/oauth.go` | `c30aa3f90b83d0689a0145262b15102d3a52b46a` |
| `web/src/features/auth/sign-in/components/user-auth-form.tsx` | `8ee1d9d19d1465f7a37694ddc77fab4c1182e5d3` |

Additional Passkey paths also have identical blobs in the two releases: `controller/passkey.go` → `0cfc530fe8e035cc304571cea76cf907f1371bc4`; `service/passkey/service.go` → `54334e095f185c7e7ee323c2d19f77607dfe82c2`; `service/passkey/session.go` → `e3aec116f79f5fa15b7e7beb851ffa8a0bdc5366`; `model/auth_flow.go` → `6158998fc78330c3b7dcb69b17692543d49d8b6d`; `middleware/rate-limit.go` → `2587d38f08c761f0ea089d0dfad1e7fadaf08978`; frontend Passkey `api.ts` → `a5cbac871f45e87f76ad5d5e914c07e46de8d34a`, `assertion.ts` → `eb430149d2d5b569571c26524b466d1dfdf329d1`; `web/src/lib/secure-verification.ts` → `a54f84aae841a521db6013024f72fe7663fc1c82`.

The released custom-provider handler constructs the supplied public endpoint/query shape, callback from `window.location.origin + /oauth/ + provider.slug`, fresh server state and configured scopes, then navigates the browser. This construction is unchanged. The later workspace forwarding/R20 commits are outside this deployed old/current range and do not explain it on source alone. Source equality does not prove equal running images/configuration and does not resolve the reported previous-release success.

## Fix boundary and integration

No application patch is justified by current evidence; **no claim fixed**. The smallest missing origin evidence, recorded for future use only, is the redacted matching SSO vhost/upstream Host/SNI directives (or this hostname's Cloudflare/Tunnel overrides) plus the one access/error event around **11:54:50 UTC / 19:54:50 Asia/Shanghai**, CF-Ray `a60075ff0b057a53-HKG`, showing selected route/upstream status. Do not request again or acquire it without authorization. Preserve TLS verification, host checks, state/redirect validation and all authentication protections.

Official OWASP Authentication/Session Management/OAuth2 guidance and ASVS **5.0.0** were consulted. Relevant boundaries: 10.1.2/10.2.1 (session/transaction binding and CSRF), 10.4.1 (exact redirect allowlist), 12.2.1–12.2.2/12.3.2 (TLS/trust verification). This is not an ASVS compliance or functional-test-pass claim. Verification remains GitHub Actions only; no local checks executed. Other feature commits continue without an incident-wide block. Worker can assist p2 with read-only inventory/hunk attribution on request.
