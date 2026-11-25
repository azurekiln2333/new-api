# pi-fwd-auth — design §5 auth integration progress

Worker: pi-fwd-auth (herdr w4C). Owner of core: pi-billing (w4C:pJ). Integrator: Claude (w4C:p17).
No local tests/builds/vet/lint/gofmt/typecheck/DB probes. No git stage/commit/push. Validation is CI-only.

## OWASP references reviewed for this slice (2026-11-25)

- Session Management Cheat Sheet: session cookies must be bound to the origin that
  set them; origin/CSRF validation for state-changing session endpoints must be
  server side, use exact origin comparison (no prefix/suffix/wildcard), and must not
  be satisfied by attacker-controllable proxy headers.
- CSRF Prevention Cheat Sheet: Origin/Referer verification requires an exact match on
  scheme+host+port; "null" or absent Origin must fail closed; never synthesize
  Origin from Host.
- ASVS 5.0.0 chapter-level (no unverified requirement IDs asserted): V3 (browser
  security, origin checks), V6 (authentication), V7 (session management),
  V10 (OAuth/OIDC), V12 (secure communications).
- MFA/Passkey guidance: WebAuthn RP ID and origin allowlists stay authoritative and
  server-controlled; the effective page origin must reflect what the user's browser
  actually used, never a proxy-injected header.

## Changes made (owned files only)

1. `middleware/auth_origin.go`
   - Added `effectiveRequestOrigin(*http.Request) string`: scheme from
     `common.EffectiveRequestScheme` (verified envelope only; otherwise TLS),
     host from the verified envelope `identity.Host` when
     `VerifiedConsoleIngress` ok && `Forwarded`, else `request.Host`.
   - `isAllowedSessionOrigin` now compares against that helper instead of
     `request.TLS != nil` + `request.Host`.
   - Unchanged: `SessionCookieSecure` gate, `requestBrowserOrigin` (single Origin,
     no comma list, NormalizeOrigin; Referer fallback), exact constant-time compare,
     `SessionCookieTrustedURLs` exact allowlist, OAuth `https://` origin allowlist,
     missing/null/foreign rejection, no CORS headers.
   - Never reads `X-Forwarded-Proto`/`X-Forwarded-Host`; never rewrites `request.TLS`.

2. `service/passkey/service.go`
   - `detectScheme`: verified envelope wins via `common.EffectiveRequestScheme`;
     raw `X-Forwarded-Proto`/`X-Forwarded-Protocol` are skipped entirely for
     forwarded requests.
   - New `passkeyRequestHost`: verified envelope `Host` wins, else `r.Host`.
     `resolveOrigins` now uses it for the HTTPS enforcement message, and the
     `host == ""` check, so the public worker domain (not the WireGuard master
     address) is used. RP ID/origin allowlist logic untouched.
   - `AllowInsecureOrigin` semantics unchanged.

3. Tests (authored, not executed)
   - `middleware/auth_origin_test.go`: new `TestSessionCookieOriginGuardUsesVerifiedConsoleIngress`
     (verified public HTTPS over internal HTTP accepted, verified Referer accepted,
     foreign/null/missing/scheme-mismatch rejected, unverified identity does not lift
     transport origin). Existing `TestSessionCookieOriginGuardDoesNotTrustForwardedProtoFromClient`
     kept unchanged.
   - `service/passkey/service_test.go` (new file, package-internal style):
     `TestPasskeyOriginUsesVerifiedConsoleIngressOnly` — verified ingress yields
     `https://panel.example.com`, spoofed `X-Forwarded-Proto: http` cannot override it,
     unverified identity keeps plain transport origin, direct requests unchanged.

## Residual risks

1. **Passkey auto-detect origin trusts raw forwarding headers for DIRECT requests.**
   `service/passkey/service.go` `detectScheme` reads `X-Forwarded-Proto` /
   `X-Forwarded-Protocol` before `r.TLS` for **direct (non-forwarded)** requests. This
   is pre-existing behavior, deliberately left unchanged (accepted by root in this
   task). For console-forward traffic it is bypassed: a verified envelope's scheme
   wins and raw forwarding headers are never consulted.
   - Impact: on a direct plain-HTTP request an attacker-supplied
     `X-Forwarded-Proto: https` can influence only the *auto-detected* WebAuthn origin
     used for `RPOrigins`; an actual Origin mismatch still fails in
     `BuildWebAuthnForRPID` via `protocol.IsOriginInHaystack`.
   - Mitigation in production: configure explicit `passkey.origins` / `passkey.rp_id`
     (those settings are authoritative and ignore `detectScheme` entirely).
   - Tracking: pre-existing, no existing test pins this; recorded here so a future
     hardening task can remove raw-header trust for direct traffic and add a
     negative test.

## Reported pre-existing behavior (NOT changed, per instruction)

- `service/passkey/service.go` `detectScheme` still reads raw `X-Forwarded-Proto` /
  `X-Forwarded-Protocol` for **direct** (non-forwarded) requests. This is an existing
  unverified-header trust for the passkey auto-detect origin; it is now bypassed for
  console-forward traffic only. Changing it for direct traffic would alter existing
  reverse-proxy deployments (and there is no existing test pinning it), so it is
  reported to root rather than silently changed. Note `detectScheme` orders raw XFP
  before `r.TLS`, so a legacy direct deployment behind an HTTPS-terminating proxy
  still auto-detects `https`; the residual risk is an attacker-supplied XFP on a
  direct plain-HTTP request, which only affects the auto-detected origin used for
  WebAuthn verification (an Origin mismatch still fails in `BuildWebAuthnForRPID`
  via `protocol.IsOriginInHaystack`).
- Explicitly configured `passkey.origins` / `passkey.rp_id` / legacy RPIDs remain
  authoritative, so production nodes with explicit settings are unaffected either way.

## Open items for the core owner (pi-billing)

- No new core helper is required for this slice; only `VerifiedConsoleIngress`,
  `WithConsoleIngressIdentity`, and `EffectiveRequestScheme` are used.
- Note: `middleware/console_forward.go` currently contains pre-existing compile-level
  issues observed while inspecting (e.g. `&consoleForwardVerifier{...}` assigned to a
  `*consoleIngressVerifier` field and `proxyTemplate` referenced but not defined in
  the closure path). Not my files — reported, not modified.

## Status

Step 4 (auth integration only) implemented. Tests authored but NOT run (CI only).
No git operations performed.
