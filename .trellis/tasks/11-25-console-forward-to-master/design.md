# Design — console forwarding to us01 (review gate; not implemented)

Active task: `.trellis/tasks/11-25-console-forward-to-master`, parent
`11-24-console-responsiveness-diagnostics`. Read full PRD F1–F7. This document and
implement.md are the ONLY changes authorized in this step. Audit changes remain
frozen. No executable/local validation or production source changes performed.

## 1. Boundary and source facts

Behavior gap: an HK console request currently executes multiple sequential US
Redis/SQL calls. Forward one HTTP exchange to us01 before those calls; execute
unchanged authentication, permissions, handlers, audit and cache publication there.
Relay admission, provider calls and settlement stay on the selected worker.

Use stdlib `httputil.ReverseProxy`, not the provider relay clients, plugin dispatcher,
or browser redirects. No browser-visible origin change, auth bypass, new schema,
provider DTO inspection, billing-field parsing, credential persistence or DNS change.
WireGuard is the encrypted transport boundary even for internal HTTP URLs.

Inspected sources: main.go:215–232; router/main.go:15–21; router/api-router.go;
router/channel-router.go; router/dashboard.go; router/relay-router.go;
router/task-router.go; router/video-router.go; router/task-plugin-protocol-router.go;
router/plugin-router.go; router/web-router.go; middleware/auth_origin.go;
common/trusted_proxies.go; oauth/callback.go; service/passkey/service.go;
service/auth_session.go; setting/system_setting/{oauth,passkey,cors}.go.

## 2. Exact routing policy

Match decoded URL.Path on segment boundaries, preserve original RawPath/query on
forward. Do not use substring `/api` (must not match `/apix`). Reject ambiguous
encoded slash/backslash/dot-segment paths in forwarding scope rather than send a
request whose scope differs between worker/master. No client-controlled target.
All HTTP methods, including OPTIONS, participate; unknown `/api/...` is forwarded
and gets the master's normal 404/405, not a local frontend/plugin response.

### Forward

**`/api` and `/api/` descendants, all methods, EXCEPT the exact health exception
below.** This is an exact prefix rule, not a hand-maintained list of individual
handlers. It includes every route registered by SetApiRouter and its nested
registerChannelRoutes/registerAuthzRoutes; additions inherit forwarding.
Inventory of current families/standalone paths (api-router.go:27–480):

- `/api/setup` GET/POST (installation state remains authoritative on master).
- `/api/status`, `/api/status/instances`, `/api/uptime/status`, `/api/official-status`.
- `/api/models` plus model-management descendants; `/api/notice`, `/api/user-agreement`,
  `/api/privacy-policy`, `/api/about`, `/api/home_page_content`, `/api/pricing`,
  `/api/perf-metrics` (empty and `/summary`), `/api/rankings` and `/model-usage`.
- `/api/verification`, `/api/reset_password`, `/api/user/reset`.
- `/api/oauth/state`, `/api/oauth/email/bind/{start,resend}` and `/bind`,
  `/api/oauth/wechat` GET/POST and `/bind`, `/api/oauth/telegram/login`,
  `/api/oauth/telegram/bind/start`, `/api/oauth/telegram/bind/:flow_token`,
  `/api/oauth/:provider` (api-router.go:56–68).
- `/api/verify` and `/methods`; ALL `/api/user` routes including auth refresh/logout,
  login/encryption-key, login/2fa/verify/passkey begin/finish, passkey/login begin/finish,
  registration, self profile/settings, sessions/revocation, access_tokens,
  passkey registration/verification/delete, 2FA, bindings and admin user management
  (api-router.go:82–187). Password encryption key retrieval AND login now reach the
  same process, avoiding mismatched process-local key assumptions.
- `/api/subscription` including purchase/admin/reset and epay notify/return;
  `/api/user/epay/notify`; `/api/{stripe,creem,waffo}/webhook`,
  `/api/waffo-pancake/webhook/:env`. Server callbacks under /api are included: forward
  exact signature-bearing body and headers; never reconstruct payloads.
- `/api/option`, `/api/custom-oauth-provider`, `/api/performance`, `/api/ratio_sync`,
  `/api/plugin/task`, `/api/task_plugin_options`, `/api/request_plugin`.
- `/api/channel` (ALL channel-router.go registrations, including uploads, probe jobs,
  `/plugin_login/callback`, `/:id/plugin_login`, `/:id/plugin_login/continue`,
  `/ollama/pull/stream`,
  `/upstream_updates/...`); `/api/authz` registrations from authz-router.go.
- `/api/token`, `/api/usage/token`, `/api/invitation`, `/api/redemption`,
  `/api/audit`, `/api/log`, `/api/system-task`, `/api/system-info`, `/api/data`,
  `/api/group`, `/api/prefill_group`, `/api/mj`, `/api/task`, `/api/vendors`,
  `/api/deployments` (including log/container requests).

This also centralizes node-local console controls such as `/api/performance/gc`,
log-file viewing and local plugin/runtime diagnostics: they act on **us01**, not HK.
That is an important product consequence of F2's entire /api scope, not silently
preserved worker-local semantics. Root must acknowledge before implementation;
future per-node controls need explicit node-target APIs, not forwarding exemptions.

### Keep local

- **GET `/api/status/test`**: only dedicated health/test route found, registered
  api-router.go:33, handler misc.go:25–37 explicitly PingDB and local HTTP stats.
  Keep its existing AdminAuth/limiter and DB health behavior; health exception is
  outside zero-worker-I/O acceptance for forwarded console routes. No invented
  `/health`, `/ready`, `/ping` routes; unmatched paths keep existing behavior.
  `/api/status` is console configuration (misc.go:41 onward), NOT a simple liveness
  endpoint: forward it, including start_time of the processing master.
- `/v1` and descendants, `/v1beta` and descendants (including OpenAI-compatible
  `/v1beta/openai/models`); `/mj` and `/:mode/mj` descendants; `/pg` descendants.
  All relay HTTP/WebSocket/SSE/upload/artifact paths stay local
  (relay-router.go:22–244, task-router.go:15–34, video-router.go:11–31).
- Both GET `/pg/channels` (admin selector) and POST `/pg/chat/completions` stay local
  under the explicit playground exclusion, despite selector being a shared read
  (relay-router.go:65–70). This is a narrow explicit F2 exception.
- Legacy token billing `/dashboard/billing/{subscription,usage}` and
  `/v1/dashboard/billing/{subscription,usage}` stay local: TokenAuth SDK-compatible
  relay-facing billing endpoints, not dashboard-session APIs (dashboard.go:10–21).
- Registry-backed task protocol routes (task-plugin-protocol-router.go:13–24)
  remain local, as do dynamic native/plugin dispatcher routes. No blanket plugin
  proxying; only `/api/plugin/...` console management is forwarded. Do not modify
  the plugin runtime/contract in this task.
- `/`, `/assets/...`, JS/CSS/favicon and every SPA navigation route remain local
  (web-router.go:21–49). `/oauth/:provider` is a frontend callback route, NOT a Go
  auth handler: web/src/routes/oauth/$provider.tsx submits GET `/api/oauth/:provider`.
  `/oauth/plugin/:key/callback` likewise renders SPA, then POSTs
  `/api/channel/plugin_login/callback`; service/provider_plugin_login.go:103–106
  generates this navigation URL. Keep both SPA GETs local, forward their actual
  backend API exchanges. `/user/reset` is likewise frontend navigation.
- Existing pprof listener (main.go:201 onward, separate listener), static paths,
  custom unmatched frontend routes are not forwarded.

### Streaming / polling / WebSocket evidence

- POST `/api/channel/ollama/pull/stream` is SSE, registered channel-router.go:115;
  channel.go:3251–3286 sets text/event-stream and Flushes progress. Forward with
  immediate flushing; proxy after headers cannot replace an interrupted body by JSON.
- Probe/system-task/plugin-login polling uses ordinary bounded HTTP responses;
  do not background or merge polls. Forward every in-scope poll individually.
- Deployment logs are forwarded like other /api responses, regardless of future
  chunking/long-poll implementation. No body-buffer assumptions.
- No console WebSocket upgrade handler found in /api registrations. Known upgrades
  are GET `/v1/responses` and `/v1/realtime` (relay-router.go:79,86), both local.
  Still use ReverseProxy upgrade support rather than globally rejecting future
  in-scope upgrades; proof is validated during handshake before any hijack.

## 3. Mounting and zero worker datastore work

Initialize configuration once at startup, without datastore reads. Install the
forward/trust middleware globally immediately **after RequestId** at main.go:223,
BEFORE Version/I18n/SetUpLogger and BEFORE any route registration. PanicRecover
remains outermost. Existing earlier steps are local only. Successful forwarding
serves response then c.Abort(): no API gzip, AccessTokenAudit (DB), GlobalAPIRateLimit
(Redis), auth/session/permission, plugin dispatcher or handler executes on worker.
Do not mount on `/api` after AccessTokenAudit; that would already pay PAT DB work.
Because Gin routes have already been selected, c.FullPath is available but prefix
policy remains authoritative for unknown/OPTIONS requests.

The forwarding middleware logs one bounded safe worker access record itself because
it aborts before existing Gin logger. On master it installs verified provenance and
then continues through existing logger/auth handlers. No extra request/audit event
is fabricated on the worker; authentication/security audit occurs on master once.
RequestId middleware must honor a request ID only when early peer/proof verification
succeeded, otherwise generate locally. Resolve ordering by parsing/verifying proof
in a no-I/O preflight helper consumed by RequestId and forward middleware, or install
one unified ingress identity middleware directly before RequestId. Root review:
prefer unified ingress verifier → RequestId → forwarder, no duplicate validation.

## 4. Client IP and forwarded trust

### Today

main.go:216 calls middleware.ConfigureTrustedProxies → common.ConfigureTrustedProxies.
TRUSTED_PROXIES defaults loopback + 10/8,172.16/12,192.168/16,fc00::/7;
`none` disables trust (common/trusted_proxies.go:11–53). Gin v1.9.1 ClientIP
(context.go:771–805) uses socket RemoteIP then X-Forwarded-For/X-Real-IP only for
trusted peers; engine TrustedPlatform is not configured by this project.
**CF-Connecting-IP is not consumed explicitly today.** A Cloudflare-facing reverse
proxy must already normalize XFF or include correct trusted CF edges; direct public
CF addresses aren't in the private default set. Never blindly switch to trusting CF
header from all peers. c.ClientIP drives rate limits, Turnstile remoteip, PAT touch,
login/session/audit IP, relay IP restrictions (middleware/rate-limit.go:113,
turnstile-check.go:27, auth.go:357/512/610, audit.go:146).

### Proposed trusted envelope

Separate new trust config from TRUSTED_PROXIES. No implicit private-network trust.
Worker derives IP through its existing configured Gin ClientIP policy, NOT arbitrary
CF-Connecting-IP. Public ingress reverse proxy must strip incoming XFF/X-Real-IP/
CF-Connecting-IP and set normalized values, with narrow TRUSTED_PROXIES. Worker
accepts X-Forwarded-Proto only from that configured ingress peer, one exact http/https
value; otherwise derive TLS. Validate public Host syntactically and against new
public-origin allowlist; never use header to select backend target.

Master accepts internal envelope only if BOTH socket peer IP (raw RemoteAddr,
not c.ClientIP) is in CONSOLE_FORWARD_TRUSTED_CIDRS AND HMAC is valid. Also require
public scheme/host matches configured exact public origins and entry node is bounded.
Use HMAC-SHA256, constant-time compare, secret >=32 random bytes. Headers:
`X-New-Api-Console-{Version,Timestamp,Nonce,Entry-Node,Client-IP,Scheme,Host,Request-ID,Proof}`.
Canonical encoding: length-prefixed fields, version=1, timestamp Unix seconds,
128-bit random nonce, uppercase method, exact escaped path + raw query, normalized
public scheme/authority, canonical client IP, bounded entry node and request ID,
SHA256 of Authorization and Cookie and Origin/Referer plus Content-Type,
Content-Encoding and declared Content-Length. Never log these hashes/proof/secret.
Binding credentials prevents transplant to another authenticated request. No body
pre-read/hash: body remains streaming; WireGuard authenticated encryption protects
body integrity. A public hop without WireGuard is NOT an acceptable deployment.

Timestamp tolerance 30s. A bounded master in-memory nonce registry rejects reuse
for 60s (maximum ±30s freshness); reserve atomically only after valid HMAC. Bound
entry count and reject overload, never evict a fresh nonce to accept replay. No
Redis proof check (would defeat worker-zero-I/O goal unnecessarily; master could
use Redis but not needed with one master). Master restart loses replay registry;
remaining <=30s envelope window is documented, authentication challenge/proof
single-use still enforced by existing handlers. Stronger durable replay protection
is a review option, not a claim already satisfied. Shared secret authenticates
cluster membership; a compromised authorized worker can assert a client identity.

Strip all inbound reserved console headers on workers before creating proof. A
signed envelope on a non-master worker is a misroute: 508/502, never forward again.
An unsigned client-supplied marker must not stop legitimate worker forwarding.
On master, malformed/invalid envelope from a configured internal peer fails closed
(403, no handler); ordinary direct requests outside this protocol ignore reserved
headers and keep today's ingress policy. Never fall back to trusting spoofed proof
headers through generic TRUSTED_PROXIES.

For a valid master envelope, record actual internal socket peer in typed request
context, then present the signed client endpoint to Gin ClientIP (normalized
RemoteAddr = signed IP:0), clear XFF/X-Real-IP/Forwarded/CF-Connecting-IP, set only
verified external scheme/public Host in typed context + URL.Scheme, and clear raw
proxy scheme aliases. Changing RemoteAddr here is explicit normalization AFTER
proof, not treating user headers as socket facts. Signed metadata is not an auth
identity: still run every existing auth/role/status/scope/proof guard.

## 5. Origin, CSRF, cookies, OAuth, WebAuthn

- Preserve browser Origin, Referer, Sec-Fetch-* unchanged, including missing/null.
  Never synthesize Origin from Host to make a rejected request pass.
- Refresh/logout use SessionCookieOriginGuard (api-router.go:84–85;
  auth_origin.go:18–94): in secure mode it compares Origin/Referer with TLS+Host,
  SESSION_COOKIE_TRUSTED_URL, or configured OAuth HTTPS origins. It currently does
  NOT trust raw X-Forwarded-Proto; existing negative test pins this.
  Add shared effective-request-origin helper: only verified envelope can override
  scheme; otherwise existing TLS logic. Update guard to use it, not fake Request.TLS
  (TLS=nil for internal HTTP truthfully). Preserve exact allowlists/foreign-origin
  rejection and missing/null rejection. No widening CORS.
- Header Authorization + server-issued security proofs remain intact. Bearer writes
  do not become cookie-authenticated by proxying. Secure cookie mode is deployment
  prerequisite: SESSION_COOKIE_SECURE=true and exact SESSION_COOKIE_TRUSTED_URL
  covering all public console domains (common/session_cookie.go:45–82). Current
  default false is pre-existing insecure-local mode, not a forwarding default.
- Refresh cookie is HttpOnly, Path=/api/user/auth, SameSiteStrict, Secure from
  common.SessionCookieSecure, **no Domain** (host-only); hint cookie likewise
  host-only (service/auth_session.go:325–400). Proxy returns every Set-Cookie verbatim,
  no Domain/Path/SameSite rewriting; browser stores under worker's public host.
  Public HTTPS never becomes insecure merely because WireGuard backend uses HTTP.
- OAuth callback URI for Generic/OIDC/Discord comes from exact allowlisted Origin
  (oauth/callback.go:33–51); request Host/XFH do NOT override it. Preserve Origin.
  Flow stores redirect URI/state/PKCE/browser binding; exchange uses stored URI
  (controller/oauth.go:77–83,198–217), never proxy query values. Include every public
  domain in oauth.allowed_origins and provider-registered callback URLs where those
  providers support multiple URIs. Fixed callback providers/canonical fallback keep
  ServerAddress, not automatically rewritten per request.
- `/oauth/:provider` and plugin callback are local SPA GETs; API exchange forwarded.
  OAuth flow cookies are host-only Lax, HttpOnly, Secure for HTTPS and __Host- prefix
  (oauth/callback.go:66–79). Do not rename or merge flow cookies.
- WebAuthn settings snapshot fills default Origins/RPID from **ServerAddress**
  (system_setting/passkey.go:154–166); service/passkey/service.go:28–58 enforces one
  selected configured RP ID and filters origins; detectScheme at :237–256 currently
  reads raw X-Forwarded-Proto first. For verified envelope use effective scheme;
  remove reliance on unverified forwarding headers for console-forward traffic.
  Public Host must remain worker domain, never WireGuard host. Explicit configured
  RP origins/RPID/legacy RPID allowlists remain authoritative. Distinct unrelated
  node domains do not magically share a passkey credential; configure supported
  RP IDs/origins or use existing credential-domain selection. No try-all-RP bypass,
  weakened challenge expiry, relaxed origin or AllowInsecureOrigin change.
- ServerAddress is DB-backed option (model/option.go:598), default localhost
  (system_setting_old.go:3), not a request-by-request SERVER_ADDRESS env override
  found in current Go source. Used for reset-email links (misc.go:265), plugin-login
  callback (provider_plugin_login.go:106), fixed OAuth/return paths and task URLs.
  Keep canonical public site; NEVER replace with internal master URL. Per-node
  dynamic links not already supported are a product limitation, not auto-fixed here.

## 6. Transport, streaming, failures and loop safety

Dedicated Transport: Proxy=nil (never HTTP_PROXY env for internal credentials),
DisableCompression=true, no redirects (ReverseProxy not http.Client follow),
HTTP/1.1 to internal master initially, keepalives on; target static URL no userinfo,
query/fragment/path prefix, fixed IP in configured internal CIDRs (no DNS rebinding).
Allow http only on explicitly internal address, https validates cert normally.
Strip hop-by-hop headers via stdlib proxy, preserve end-to-end Authorization/Cookie,
request RawQuery/path, content encodings, trailers and repeated Set-Cookie. Preserve
public request Host while URL.Host chooses socket; don't add unverified XFF.
ReverseProxy Rewrite sets target then restores public Host; use signed metadata.
Disable automatic transport retries with a single-use/nonreplayable outbound body,
no GetBody, and explicit transport behavior for GET/idempotent stale connections;
implementation must prove no post-send resubmission, including zero-body requests.

**Safer F5 architecture:** acquire/dial a master connection BEFORE passing the real
request to ReverseProxy. Dial/TLS failure before writing any HTTP bytes: log safe
fallback reason, run local chain with ORIGINAL unread body and sanitized ingress
headers. One preconnected connection is handed to one RoundTripper operation;
no re-dial/retry after handoff. A successful TCP dial is NOT proof request wasn't
processed if subsequent write/read fails. Once handoff can send headers, every
error is conservatively post-send: 502 if headers unsent, abort stream/close if
already sent. Never fallback on 4xx/5xx response, timeout after handoff, EOF,
partial write, lost COMMIT ACK, upstream redirect, or `WroteRequest` callback error.
`httptrace.WroteRequest` alone cannot prove zero bytes on partial writes.
Connection pooling can be added only with an equally explicit no-retry contract;
review tradeoff: preconnect per request adds intra-WG connection setup, still avoids
sequential cross-region datastore calls. Do not claim 1 TCP RTT total; requirement
means one application HTTP exchange, not no network handshake.

Dial timeout 2s, TLS handshake 3s, response-header timeout 120s, ordinary total
request deadline 300s; explicitly mark SSE/poll/upgrade routes for no total deadline
once streaming established. Do not timeout a legitimate upload based on an arbitrary
buffer read. Default ReverseProxy FlushInterval=-1 (flush every write), propagate
cancel/disconnect to master, no body buffering/gzip on worker. Existing master gzip
must preserve SSE flush behavior in tests; worker must not double-compress. For
incomplete streams, return transport failure by terminating rather than inventing
success or appending invalid JSON to SSE/file downloads.

Upload limits: master retains route-specific AnonymousRequestBodyLimit (default
512KiB; common/init.go:194, middleware/request_body_limit.go) and existing parsers.
MAX_REQUEST_BODY_MB defaults128 (common/init.go:192), but global decompression
middleware is registered inside SetRelayRouter AFTER SetApiRouter, so **do not
assume all existing /api uploads already have that global128MiB limit**. Forwarder
must not impose a new universal body cap or read multipart into RAM. Keep request
ContentLength/encoding and master rejection unchanged; explicitly document currently
uncapped routes. Add proof/header length caps, not a replacement upload policy.

Master never forwards even if target configured. Workers reject a VALID forwarded
envelope arriving on a worker; no trusted hop count >1. Invalid spoofed marker
cannot induce a forwarding loop or backend bypass. Target accidentally self/worker
must fail startup where recognizable and request-time loop guard otherwise.

## 7. Configuration (proposed; review required)

| Variable | Default | Meaning |
|---|---|---|
| CONSOLE_FORWARD_MASTER_URL | empty | worker opt-in; e.g. http://10.100.2.1:3000; no path/query/auth |
| CONSOLE_FORWARD_SECRET | empty | >=32 random bytes; present on workers and master; never logs/status API |
| CONSOLE_FORWARD_TRUSTED_CIDRS | empty | exact WG /32 or /128 peers trusted for signed envelopes; no RFC1918 implicit default |
| CONSOLE_FORWARD_PUBLIC_ORIGINS | empty | comma-separated exact HTTPS worker domains allowed in signed public host/scheme; include explicit loopback only in tests/dev |
| CONSOLE_FORWARD_DIAL_TIMEOUT_MS | 2000 | bounded positive maximum (review bound10000) |
| CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS | 120 | positive bounded (review max600) |
| CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS | 300 | nonstream total; bounded max3600 |

Fixed security constants: proof version1, skew30s, nonceTTL60s, max nonce registry
100000 (fail closed on capacity), bounded request ID/entry node128 bytes; no env to
turn off HMAC/origin checks. Entry node uses existing configured node identity.
Malformed partial configuration fails startup rather than silently disabling trust.
Unset all = existing behavior. Master requires secret+CIDRs+origins but no target;
use existing common.IsMasterNode as forwarding prohibition. WG source CIDRs on
worker target allowlist and master peer allowlist may need separate target predicate;
implementation must not interpret broad user-submitted DNS as internal IP.

us01: secret, CIDRs(us03/hk01/hk02/hk03 WG source addresses), public origins,
master role, NO target. Workers: same secret/public origins and target us01;
configure narrow ingress TRUSTED_PROXIES independently. Session cookie secret and
other existing shared auth key material must match across nodes for local fallback;
forwarding never copies private keys into headers. Rollout blocked if fallback auth
secrets/settings differ or master can't reach configured upstream/internal services.

## 8. Observability and consistency

Propagate one verified request ID worker→master→response. Generate local ID for
untrusted direct request; never accept arbitrary public request ID as proof.
Safe log fields: request_id, method, route (no raw query/code/state), entry_node,
processing_node, forwarded=true/false, failure_class, status. No credential/proof,
OAuth code, cookie/header hashes, raw body or secret. Worker log entry=HK process=us01
when successfully proxied; processing=worker on pre-send fallback. Master log same
entry plus actual local processing node. Existing AuditOther/consume processing-node
semantics must not be overwritten by ingress node: use separate fields/context,
no schema changes. Relay calls remain local with existing logs.

Writes still use existing master atomic publication and sync topics:
model/channel_publication.go:361/457/567, main.go:121–139 registrations,
service/node_sync.go:20 (3s tick); channel legacy sync and authz sync still use
common.SyncFrequency (main.go:108,148). No worker waits for cross-node ACK. Do not
invalidate unrelated caches or claim zero staleness. Local fallback retains the
same current publication path; forwarded writes do not bypass master validation.
Process-local jobs need explicit handling: fingerprint job snapshots, plugin-login
poll state and caches started on master must stay there for subsequent polling.
A pre-dial fallback poll on worker may return not-found for a master-local job;
never silently restart an expensive probe. Root review must decide whether these
read polls may return an honest unavailable result instead of generic local fallback.
Dynamic plugin endpoints must not hijack API or SPA callback routing; prefix scope
is decided before the dynamic dispatcher.
Potential local-only side effect: closing active worker WebSockets on channel
disable currently occurs in request process; moving to master must rely on existing
cross-node mechanisms. Root must confirm the existing sync actually closes those
worker sockets, or record F7 blocker rather than silently claim equivalence.

## 9. Security references / approval gaps

Read OWASP Authentication, Session Management, CSRF Prevention, OAuth2, Multifactor
Authentication (Passkeys/FIDO2 origin binding) Cheat Sheets on2026-11-25:
https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/OAuth2_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html
Read ASVS5.0.0 published source chapters V3 (browser), V6 (authentication), V7
(session), V10 (OAuth/OIDC), V12 (secure communications), from v5.0.0 tag of
OWASP/ASVS. References at chapter level; no unverified requirement IDs asserted.
Controls: trusted proxy target-origin resolution; exact origin checks; no null/
missing bypass; host-only secure cookies; preserve single-use state/PKCE/challenges;
TLS/WG confidentiality; bounded signed metadata and anti-replay; no credential logs.
Design is NOT a compliance claim. Remaining review decisions: health exception,
node-local admin actions meaning, stream timeout classes, signed-envelope replay
restart window, per-request dial versus pooling, worker WebSocket close convergence.

## 10. Cloud-only tests in existing files

Primary transport/integration table cases in `middleware/trusted_proxies_test.go`
(existing trust-boundary test home) with httptest master/worker handlers:
- configured/off/master/local routes; all API families/health exception; SPA callbacks,
  playground, dynamic relay scope unchanged; zero local datastore middleware entered.
- peer CIDR+HMAC both required; stale/future/bad/replayed proof; spoofed CF/XFF/XFH/
  scheme; changed path/query/credential/proof; invalid public host; signed loop guard.
- exact body/query/header/trailer/status/repeated Set-Cookie fidelity; no credentials
  in logs; multipart large streaming not pre-read; 413 from real master limit.
- injected dial failure keeps original unread body and invokes local handler once;
  partial write/EOF/timeouts invoke master at most once and never local fallback;
  GET/no-body automatic-retry guard; cancellation and nonbuffered SSE flush.
- signed request ID/entry/processing identity with master permission checks intact.

Extend `middleware/auth_origin_test.go` for verified public HTTPS through WG HTTP,
foreign/null/missing Origin rejection and raw XFP still rejected. Reuse existing
`controller/auth_flow_test.go` / `controller/security_enrollment_test.go` fixtures
only where actual session/cookie/OAuth/passkey ceremony regression is not expressible
in the primary table; no new scattered fixture families. Existing router
`router/channel_router_test.go` / `router/access_token_scope_test.go` can assert real
master guards remain effective through forwarded route. Root assigns exact test
file ownership before edits. No test files created in this design step.

STOP: root must review artifacts and close decisions before implementation.

## 11. Root review decisions (2026-11-25, Claude root, binding for implementation)

1. **Scope**: forward the entire `/api` prefix as designed; `/api/status/test` stays local; `/pg/*`, relay, task-protocol, SPA and static stay local. Node-local console controls (`/api/performance/gc`, log-file viewing, local runtime diagnostics) act on the master after forwarding. Accepted for v1 and recorded in the report; no per-node exemption list.
2. **Connection reuse, NOT per-request preconnect**: a per-request dial adds one cross-region TCP round trip to every console request and defeats the goal. Use a dedicated pooled keep-alive `http.Transport` (HTTP/1.1, `Proxy=nil`, `DisableCompression=true`, bounded `MaxIdleConnsPerHost`, worker `IdleConnTimeout` shorter than the master server idle timeout). Disable ALL automatic transport retries: the outbound request must never be replayable (non-nil body with `GetBody=nil`, including bodiless requests, so `isReplayable`/`outgoingLength` cannot trigger a resend) — prove it with a test that a reused connection closed by the master yields at most one master execution.
3. **Fallback rule**: local fallback ONLY for errors proven to occur before any request byte is written: dial/connect errors on a fresh connection (`*net.OpError` with `Op == "dial"`, DNS-free fixed IP) and the configured dial timeout. Everything else after RoundTrip starts writing → 502 if headers unsent, abort stream if sent. A stale pooled-connection failure is a 502, not a fallback (frontend GET retries cover reads).
4. **Master-process-local job routes** (fingerprint/probe job status, plugin-login polls, `ollama/pull/stream`, any route whose state lives only in the processing process — enumerate from source): on pre-send fallback return an honest 503 "console master unavailable" instead of local execution, so a worker never answers not-found for or restarts a master-local job.
5. **Replay window**: in-memory nonce registry with 30s skew / 60s TTL accepted; the ≤30s window after master restart is documented. No Redis/DB proof check.
6. **Ingress order**: unified ingress verifier → RequestId → forwarder, mounted immediately after PanicRecover as designed; no duplicate validation.
7. **Secure-cookie mode is NOT a prerequisite**: forwarding must work identically with `SESSION_COOKIE_SECURE` false or true; the report recommends enabling it with `SESSION_COOKIE_TRUSTED_URL` covering every node domain, but rollout is not blocked on it.
8. **Topology**: hk02/hk03 cannot reach us01 directly; their target is an hk01 WireGuard address that TCP-stream-proxies to us01:3000. The master URL / internal-target check must accept any configured fixed internal IP:port (not only the master's own address), and `CONSOLE_FORWARD_TRUSTED_CIDRS` on the master lists the socket peers it actually sees (hk01's address for hk01/hk02/hk03, us03's address). Entry node identity comes only from the signed envelope.
9. **WebSocket close on channel disable**: verify from source whether the existing status-toggle Redis PUBLISH (see `11-24-dashboard-node-ranking-details/research/other-mutation-round-trips.md`, "disable-only 1 Redis PUBLISH") reaches every node's subscriber and closes that node's active relay sockets. If yes, rely on it and cite file:line in the report. If not, implement the close through that existing publish/subscribe path; do not invent a new transport.
10. **Client IP**: keep the existing worker `ClientIP` policy as designed; do NOT add Cloudflare ranges or `CF-Connecting-IP` trust in this task. Root will raise the Cloudflare client-IP question with the user separately.
11. **Ownership**: pi-billing owns the new forwarding/ingress middleware file(s), the mount lines in `main.go`, config parsing in `common/`, `middleware/request-id.go`, the effective-origin helper in `middleware/auth_origin.go`, `service/passkey/service.go` scheme detection, and tests in `middleware/trusted_proxies_test.go` and `middleware/auth_origin_test.go` (plus router guard assertions in `router/access_token_scope_test.go` only if needed). Do not edit `router/api-router.go` option lines (pi-logs) or `.github/workflows/ci.yml` (root wires CI selectors from the report).
