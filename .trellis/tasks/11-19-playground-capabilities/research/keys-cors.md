# Research: API key reset/regenerate + CORS credential wildcard

Scope: research only, no product edits. Two independent deliverables (intended as two separate commits later).
Evidence is from this checkout (branch `merge/upstream`); deployed configuration is explicitly marked unverified.

OWASP basis consulted this session (current published text, no secrets involved):
- ASVS 5.0 §3.4.2 (CORS: `Access-Control-Allow-Origin` must be a fixed value or allowlist entry; wildcard only when `Access-Control-Allow-Credentials` is not `true`), §3.5.1-§3.5.3 (CSRF / preflight / non-safe methods), §4.4.2-§4.4.4 (cross-origin endpoint allowlists), §6.2.3 (credential change requires current credential), §6.2.10 (no forced periodic rotation), §6.3.7 (notify user after credential reset), §6.5.6 (revoke affected authenticators), §7.4.1 and §7.4.3 (terminate/invalidate sessions after a credential change).
- OWASP Authentication Cheat Sheet, Session Management Cheat Sheet (`SameSite` is defense in depth, not a CSRF-token replacement), Secrets Management Cheat Sheet §2.7 (creation / rotation / revocation / expiration lifecycle), REST Security Cheat Sheet (access control at every endpoint; API keys as credentials).

---

## Part A — CORS wildcard with credentials

### Current behavior (known)

`middleware/cors.go` (entire file) builds a permissive config and mounts it as global middleware:

```go
config := cors.DefaultConfig()   // gin-contrib/cors v1.7.2 (go.mod)
config.AllowAllOrigins = true
config.AllowCredentials = true
config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
config.AllowHeaders = []string{"*"}
return cors.New(config)
```

Mount points:
- `router/relay-router.go:16` — `router.Use(middleware.CORS())` (all relay `/v1/*` and `/pg/*` routes).
- `router/dashboard.go:15` — dashboard route group.
- `router/api-router.go:316` — usage route.
- `router/api-router.go:387` — log route.

Not covered: `SetApiRouter` (`router/api-router.go:17-23`) installs RouteTag / gzip / `AccessTokenAudit` / `BodyStorageCleanup` / `GlobalAPIRateLimit` and **no CORS**, so `/api/token*` management endpoints are not CORS-enabled (a browser console must be same-origin to manage keys). That is the current accidental protection, not a designed control.

Root verification correction: the installed gin-contrib/cors v1.7.2 `utils.go` was read at `C:/Users/lichengyu/go/pkg/mod/github.com/gin-contrib/cors@v1.7.2/utils.go`. Both generateNormalHeaders and generatePreflightHeaders emit literal `Access-Control-Allow-Origin: *` when AllowAllOrigins is true, and independently emit `Access-Control-Allow-Credentials: true`. The earlier claim that this configuration reflects every Origin and proves credentialed cross-origin reads was incorrect. Preserve the user's original finding: the wildcard/credentials combination is invalid for browser credentialed reads. Verify actual deployed headers separately; do not claim an exploit from the code configuration alone.

### Existing origin infrastructure to reuse (do not invent domains)

- `common.NormalizeOrigin` (`common/session_cookie.go:14-45`) — canonical scheme/host/effective-port; rejects empty, `null`, wildcards, userinfo, query, fragment, non-root paths.
- `SESSION_COOKIE_TRUSTED_URL` → `common.SessionCookieTrustedURLs`, loaded by `InitSessionCookieSettings` (`common/session_cookie.go`); HTTPS-only and requires `SESSION_COOKIE_SECURE=true`.
- `oauth.allowed_origins` → `setting/system_setting/oauth.go`: `OAuthAllowedOriginsOption`, `NormalizeOAuthOrigin` (exact HTTPS, or HTTP loopback), `ParseOAuthAllowedOrigins`, `IsOAuthOriginAllowed` (compares against the canonical `ServerAddress` origin plus the allowlist).
- `middleware/auth_origin.go`: `SessionCookieOriginGuard`, `requestBrowserOrigin`, `isAllowedSessionOrigin` (request Host origin + `SessionCookieTrustedURLs` + `oauth.allowed_origins`), constant-time comparison.
- `ServerAddress` (`common/constants.go`) is the canonical deployment origin source.

Live deployment values are **unverified** (research only): which of `ServerAddress`, `SESSION_COOKIE_TRUSTED_URL`, `oauth.allowed_origins` are set on the running instance, and whether the console is served same-origin as the API.

### Fix design (proposed, not implemented)

1. Split the middleware into two behaviors instead of one permissive config:
   - `CORSRelay()` for `/v1/*`, `/pg/*`: `AllowAllOrigins = true` **with `AllowCredentials = false`** (API usage is `Authorization: Bearer`, no cookies). Wildcard without credentials is ASVS-compliant and preserves third-party browser clients.
   - `CORSConsole()` for dashboard / usage / log: exact allowlist only, `AllowCredentials = true`, `Access-Control-Allow-Origin` set to the matched canonical origin (never `*`, never reflected unvalidated `Origin`), `Vary: Origin` always.
2. Build the console allowlist at startup from `ServerAddress` + `SessionCookieTrustedURLs` + `oauth.allowed_origins`, canonicalized with `common.NormalizeOrigin`. Fail closed for credentialed routes when the allowlist is empty (no ACAO header, request still executes same-origin).
3. Replace `AllowHeaders: {"*"}` with the explicit set the console actually sends (`Authorization`, `Content-Type`, `X-Security-Proof`, `New-Api-User`, cache-bypass header, etc.) so preflight cannot authorize arbitrary headers.
4. Keep `AllowMethods` as is (OPTIONS included); do not add `*`.
5. Do **not** reflect `Origin` on mismatch and do not add wildcard subdomains.
6. No new persisted setting is required if the three existing sources suffice; a new option would be a product decision (and would require the DB/option checks below).

### Required checks / tests

- Middleware unit tests (extend existing `middleware` tests): allowed origin gets ACAO echo + ACAC true + `Vary: Origin`; unknown origin gets no ACAO; relay path gets `*` + no ACAC; preflight OPTIONS with disallowed origin fails; empty allowlist fails closed.
- Frontend/console regression: session-cookie login, token list, usage and log pages still work cross-origin when configured, and same-origin when not.
- No schema change → no three-database matrix needed unless a new option row is introduced (option storage is a plain key/value table; still verify on SQLite/MySQL/PostgreSQL if added).

### Blockers / unverified

- Which console origin(s) are actually deployed (must be read from configuration, not guessed).
- Product decision: keep relay wildcard-without-credentials, or force an allowlist for relay too (breaks unlisted browser clients).
- Whether any dashboard consumer relies on credentialed cross-origin calls today.

---

## Part B — API key reset/regenerate on an existing key

### Current behavior (known)

- Model: `model/token.go:17` `Key string \`gorm:"type:varchar(128);uniqueIndex"\`` — unique index, 128 chars.
- Creation only: `controller/token.go:299` `AddToken` → `common.GenerateKey()` → `GenerateRandomCharsKey(48)` (`common/utils.go:229-232`). The generated key is stored and **not** returned in the create response.
- No reset path: `controller/token.go:402` `UpdateToken` never touches `Key`, and `model/token.go:314-322` `Token.Update()` selects only `name,status,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,group,cross_group_retry,auto_groups,allow_user_quota_query` — `key` is excluded. There is no custom-secret input anywhere.
- Reveal: `controller/token.go:190` `GetTokenKey` returns the full key; route `POST /api/token/:id/key` (`router/api-router.go:299-312`) is guarded by `UserAuth` + `TokenOperationAudit` + `CriticalRateLimit` + `DisableCache`, with access-token scope `api_key:reveal` (`middleware/access_token_routes.go:224-234`). **No step-up proof** — contrast `middleware/secure_verification.go` `SecureVerificationRequired()` used for channel key disclosure.
- Ownership: `model.GetTokenByIds(id, userId)` (used by update/delete/reveal paths); no admin override in these handlers.
- Cache: `invalidateTokenCacheForMutation(token.Key)` sets a short fence key then deletes the Redis token cache entry (`model/token.go`, called from `Update`, `SelectUpdate`, `Delete`, and the batch paths ~`:515`). `GetTokenByKey` (`model/token.go:284`) reads Redis first, then DB. With Redis disabled the cache layer is a no-op.
- Audit: `tokenAuditParams(c)` + `ContextKeyTokenAuditSucceeded`, changed-field tracking; audit payloads must never include the secret value.
- CSRF posture: no CSRF-token middleware exists in the repo; the dashboard session cookie is `SameSite=Lax` (asserted by an existing router test — confirm the exact setter at implementation time). `SessionCookieOriginGuard` only covers cookie-auth refresh/logout when secure mode is on. So a new state-changing endpoint must not rely on SameSite alone.

### Security requirements for the new endpoint

| Concern | Requirement |
| --- | --- |
| Ownership | `GetTokenByIds(id, userId)`; other users' IDs must return not-found, never 403-with-existence. |
| Authentication | Browser session (`UserAuth`) and, if PAT is allowed, scope `api_key:write`; reset invalidates a credential, so require step-up. |
| Step-up | Reuse `RequireSecurityProof` + `X-Security-Proof` with a new operation scope (e.g. API-key reset), consumed via `service.ConsumeOperationProof`; this also supplies CSRF resistance because a cross-site form cannot set the header. |
| CSRF | Custom header + same-origin check (`requestBrowserOrigin` / `NormalizeOrigin` comparison) in addition to `SameSite=Lax`; do not treat SameSite as the control (Session Management Cheat Sheet). |
| Custom secret | Explicit opt-in; default is a freshly generated 48-char key. Bound to the column: ≤128 chars, no whitespace/control chars, printable ASCII, minimum length/entropy (≥16 chars recommended), reject a value equal to the current key only if the caller did not intend a no-op. |
| Collision | Unique index is the authoritative backstop; also pre-check existence for a clear error. Handle duplicate-key errors via GORM `ErrDuplicatedKey` (verify dialect error mapping) and retry generation for the random path; for a custom secret return a validation error. |
| Atomicity | Single-row `UPDATE token SET key = ? WHERE id = ? AND user_id = ?`; check `RowsAffected`; never write other columns; no multi-step read-modify-write that can interleave. |
| Cache | Invalidate by the **old** key (fence + DEL) before/around the update, as `invalidateTokenCacheForMutation` does; the new key has no cache entry. Verify concurrent requests holding the old key stop working after the reset. |
| Response | Return the new key once in the reset response (or keep the separate reveal endpoint). Never log it; audit only `changed_fields: ["key"]` and token ID/name. |
| Notification | ASVS §6.3.7: notify the user that the key was reset (existing notification/email infrastructure), without the secret. |
| Errors | Backend messages via `common.NewMessage`/`ApiErrorT`; all new frontend strings added to all seven locale files. |
| Rate limit | `CriticalRateLimit` + `DisableCache`, same as reveal. |

Note on ASVS §6.2.10: no forced periodic rotation. A user-initiated reset is fine; do not add expiry-based forced rotation.

### Required database verification (AGENTS.md gate)

The change is data-level, not schema-level (`varchar(128) uniqueIndex` already exists), so no `AutoMigrate` change is expected. Still required before completion:
- Real SQLite, MySQL ≥5.7.8, PostgreSQL ≥9.6: reset UPDATE succeeds, `RowsAffected` behaves, and a duplicate custom secret is rejected by the unique index on each dialect.
- Fresh database and upgrade-from-released-schema runs (startup twice) if any model tag changes; otherwise record that no schema change was made.
- Verify the Redis fence/DEL path and the no-Redis path.

### Blockers / open decisions

- Whether custom secrets are user-facing (UI checkbox) or admin-only; minimum length/entropy policy.
- Whether step-up is mandatory for every reset or only for custom secrets (recommend mandatory: it revokes a live credential).
- Whether the reset response returns the secret or reuses `POST /:id/key`.
- Deployment settings unverified: Redis enabled, `CriticalRateLimit` values, whether PATs are allowed to call key endpoints.

### Commit split (for the later root commits)

1. CORS: middleware split + allowlist wiring + middleware tests.
2. API key: reset endpoint + step-up scope + cache invalidation + frontend action/dialog + i18n + tests.
