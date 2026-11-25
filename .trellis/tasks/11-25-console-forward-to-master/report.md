# Console forwarding core — queue #1 handoff

Owner: **pi-fwd**, herdr w5D:p2. Integrator: Claude w5D:p1. Date: 2026-11-25.
Task: `.trellis/tasks/11-25-console-forward-to-master`, parent
`.trellis/tasks/11-24-console-responsiveness-diagnostics`.

**Implementation/source handoff only. No local tests, builds, vet, lint, typecheck,
formatters, database probes, commits or publication ran. GitHub Actions must validate
this exact integrated revision before release. No production deployment performed.**

## 1. Changed files and ownership

Core owned by this worker (continued existing dirty drafts, not rewritten):

| File | Change |
|---|---|
| `common/console_forward.go` | Immutable startup config, fixed private-IP target validation, timeout bounds, typed verified ingress and effective public scheme |
| `common/init.go` | Load config after role/node identity, before any datastore initialization; malformed partial configuration is fatal |
| `middleware/console_forward.go` | Signed ingress, nonce replay guard, prefix forwarding, pooled/no-replay transport, safe fallback, streaming/cancellation/trailers, loop protection and provenance logs |
| `middleware/request-id.go` | Reuse request ID only from verified ingress; public request IDs remain locally generated |
| `main.go` | Ingress → request ID → forwarder before datastore middleware; warning for implicit broad TRUSTED_PROXIES; configured master's idle timeout 90s, worker pool idle 45s |
| Task `implement.md`, `report.md`, `research/deploy-runbook.md` | Checkpoints and corrected operational handoff |

Existing helper-owned integration reviewed, **not edited by pi-fwd**:
`middleware/auth_origin.go`, `service/passkey/service.go`, `oauth/linuxdo.go`,
`controller/video_proxy.go`, `middleware/auth_origin_test.go`,
`service/passkey/service_test.go`. They use verified public scheme/host where needed;
LinuxDO token-exchange redirect_uri no longer degrades to internal HTTP.

**pi-q5** owns regression additions to existing `middleware/trusted_proxies_test.go`
(queue #5). **pi-web** owns CI selectors (#10). **pi-auth** confirmed both new error
keys (`console master unavailable`, `invalid console forwarding envelope`) were
added through the approved script in all seven locales and `STATIC_I18N_KEYS`.
No locale/script/static-key writes were made by this worker.

No relay, billing quantities, pricing, database schema/queries, plugin runtime,
router permissions, generic panic recovery or protected project attribution changed.

## 2. Final request/response contract

### Scope and worker datastore count

Forward every method under decoded `/api` or `/api/…`, including unknown routes and
OPTIONS. The **only health exception is GET `/api/status/test`**. POST to that path
still goes to the master's normal router. Reject ambiguous escaped slashes,
backslashes and dot-segment paths in scope with 400.

Relay `/v1`, `/v1beta`, `/mj`, `/pg`, SDK billing `/dashboard/billing`, task protocol,
SPA OAuth callback pages and static assets stay local. No Go auth handler was found
outside `/api`; SPA `/oauth/:provider` invokes `/api/oauth/:provider`.

Mount order is `PanicRecover → ConsoleIngress → RequestId → ConsoleForwarder`, before
Version/I18n/access logger and API-group access-token audit, rate limiters, session,
permission and handlers (`main.go`, `router/api-router.go`). Successful forwarding
aborts the remaining worker chain. **Source-derived request-path count: zero
worker SQL calls and zero worker Redis calls for a successfully forwarded request.**
This excludes startup/background work and the explicitly local health check.
Master executes the unchanged handler/middleware datastore calls next to DB/Redis.
This is one console HTTP exchange, **not a measured latency or a claim of one TCP RTT**.

### Identity/trust

Master requires both the raw socket peer in `CONSOLE_FORWARD_TRUSTED_CIDRS` and
HMAC-SHA256 proof. Envelope binds version, timestamp, random nonce, entry node,
client IP, public scheme/host, request ID, wire content length, method, escaped path,
raw query, and SHA-256 hashes of Authorization/Cookie/Origin/Referer/Content-Type/
Content-Encoding. Constant-time proof comparison; 30s clock skew, 60s nonce retention,
100,000-entry fail-closed registry. Sweeping is at most once per second, not O(n)
per request. No body pre-read/hash: **WireGuard protects body confidentiality/integrity**.

Only verified ingress replaces RemoteAddr/Host/public scheme; generic proxy headers
and private request markers are stripped afterward. This metadata is **not user
authentication**: all ordinary auth/authorization/proof/CSRF checks still run on master.
Direct traffic with public spoofed console markers has them stripped and follows
the normal scope policy; markers cannot force worker-local execution.

A validly signed envelope on any worker returns 508, even if the incoming peer is
not listed (needed for accidental self/worker targets). This HMAC-only check grants
no provenance and consumes no nonce. Invalid public markers cannot trigger that
loop rejection; malformed metadata from an explicitly trusted peer fails 403.

Worker client IP still comes from the existing configured Gin ClientIP policy.
X-Forwarded-Proto is read only from a configured ingress peer and only as one exact
http/https value. No new CF-Connecting-IP or Cloudflare range trust was introduced.

### Fidelity, pooling, failure and streaming

Dedicated HTTP/1.1 keep-alive transport, Proxy=nil, compression disabled, pool idle
45s, max idle 100/32-per-host, TLS handshake 3s, dial default2s, header default120s,
ordinary total default300s. Internal HTTPS validates certificates normally (fixed-IP
SAN must match). No http.Client redirect following.

Preserve raw query even when stdlib Rewrite would otherwise clean it, escaped path,
body bytes/encoding, end-to-end headers, request/response trailers, response status
and repeated Set-Cookie. Re-sign after stdlib hop-header cleanup so final transmitted
credential headers agree with proof. Share request trailer maps through the clones
because trailer values arrive while reading the original body. Do not buffer uploads
or impose a new universal body cap; master's existing body limit/parsers stay in
charge (some `/api` routes have no universal 128MiB cap; do not claim otherwise).

**No-replay detail:** read Go1.26 source `Request.isReplayable`, `outgoingLength` and
`persistConn.shouldRetryRequest`. A non-nil empty reader with ContentLength=0 actually
has outgoingLength=-1, not wire length0. Empty/unknown-length outbound bodies use an
explicit single-use chunked/-1 body **before signing**; received ContentLength matches
the envelope and payload bytes remain unchanged. GetBody is nil. This defeats both
isReplayable and the zero-length/nothingWritten retry branch. This corrects the
older review's inaccurate framing claim; no custom stdlib or per-request preconnect.

Only a fixed-IP `net.OpError{Op:"dial"}` **before any GotConn** can fall back. The
original body remains unread/open because only outbound close is neutralized.
Never fall back on stale pooled connections, TLS/header/total timeout after dial,
partial write/read, EOF, response errors, redirects or a canceled caller. Ordinary
fresh-dial fallback runs the local chain once, truthful but slow.

On proven pre-send failure these routes instead return honest **503**, never
not-found/restarted master jobs:

- GET `/api/channel/:id/fingerprint/:job`
- POST `/api/channel/:id/fingerprint/:job/cancel`
- POST `/api/channel/ollama/pull/stream`

Healthy requests to these routes **do forward**. Fingerprint start, plugin-login
DB flows, nonstream Ollama pull and shared system-task polling may fall back normally
(source evidence: `research/sync-and-local-routes.md` B1–B3).

Immediate flush (`FlushInterval=-1`). Explicit no-total-deadline routes: POST Ollama
pull stream, GET `/api/log/export`, GET `/api/log/self/export` (existing NDJSON stream
in `controller/log.go:148–220`); WebSocket upgrades are also exempt. Ordinary job
polls are individually bounded. Add any future streaming console route to the table.

Post-send failures return 502 **only before response headers**. Root's explicit
additional decision: catch only `http.ErrAbortHandler` around proxy.ServeHTTP, rethrow
other panics, never alter the main/Gin recovery chain. For a truncated response,
unwrap Gin before capability detection (Gin advertises Hijacker on HTTP/2 too),
hijack+close HTTP/1 client; otherwise abort without writing a body and log the
no-hijack class. Never append JSON/500 or dump request headers. HTTP/2/no-Hijacker
abort cannot promise a transport reset; this is a documented limitation accepted by
root's requested policy, not a successful-completion claim.

### Logs

Both ends log bounded quoted entry/processing node and request ID. Master ingress
returns private `X-New-Api-Console-Processing-Node`; worker consumes and strips it
before the browser. Missing marker yields **unknown**, never labels the entry worker
as processing master. Local fallback logs the actual worker and forwarded=false.
Static API family labels only; unknown families collapse to `/api`, preventing
path-parameter/query/credential leakage. Existing consume/audit processing-node
fields remain untouched.

## 3. Security review findings disposition

Independent source-only review by **pi-rev-go** (#12) reported no concrete
compile/type/race/lifetime blocker in the current core. Its remaining route-literal
alignment suggestions were applied with targeted text edits; its earlier common/
header-block alignment edits were retained. This is **not a compiler, race detector
or formatter result**; hosted output remains authoritative.

`research/fwd-security-review.md` is an earlier moving-target review. Final status:

| Finding | Source disposition |
|---|---|
| Verifier/proxyTemplate blockers | Existing prior fixes retained; streaming map lookup compile blocker also corrected |
| P1-1 OAuth effective scheme | Helper's LinuxDO/video changes already present and reviewed |
| P1-2 no-body replay/length | Both retry predicates blocked; chunked/-1 signed length now agrees with real wire contract |
| P1-3 pooled re-dial fallback | Any acquired connection disallows fallback, not merely the last Reused bit |
| P1-4 loop order/nonce | Valid-only worker loop rejection before provenance/nonce, including unlisted self/worker peers |
| P1-5 rejection class | Bounded sentinel error class retained, no secret/proof logging |
| P2-0 ingress trust | Explicit narrow TRUSTED_PROXIES deployment requirement and startup warning |
| P2-1 stream policy | Named method/path table; added existing NDJSON exports |
| P2-2 process-local route policy | Explicit patterns; 503 only after fresh-dial failure, never healthy-master refusal |
| P3 nonce scan/log labels | Coarse sweep; static family log labels; dead draft helpers absent |
| Formatter/check claims | **Not run; hosted formatting/compiler/tests remain a release gate** |

## 4. Consistency/socket-close evidence

Existing write handlers/publication stay on master; no new cross-node ACK delay.
`main.go:121–141` registers existing options/channels/authz/plugins/OAuth/models/plan
sync topics; `service/node_sync.go:20` existing tick is3s. Legacy option/channel/
authz sync still uses `common.SyncFrequency`. No new cache format or staleness
promise was introduced.

Existing disable closure is sufficient: `controller/channel.go` status toggle →
`closeActiveChannelWebSockets` → `service/ws_close.go:8–12` →
`pkg/wsmanager/wsmanager.go:105–111` local close + awaited Redis publication.
Every node starts the subscriber unconditionally (`main.go:109`); remote events
close that node's registered sockets (`pkg/wsmanager/wsmanager.go:156–176`). Relay
socket registration stays local (`relay/websocket.go:48`,
`relay/responses_websocket.go:1058`). Full evidence is in
`research/sync-and-local-routes.md` A.

Limits unchanged: Redis must be enabled and the DB index/topic must agree; delivery
failures are logged, not durable guaranteed delivery. Only registered realtime/
responses WebSockets are closed; existing in-flight SSE relay requests are not.

Node-local `/api/performance` GC/cache/log-file controls and runtime diagnostics now
act on **us01**, as root accepted in design §11.1. Shared instance/status registries
still describe the cluster; pprof listener and all relay stay local.

## 5. Deployment examples (NOT executed)

Replace **all** `{{…}}` using inventory. Do not publish secrets. Use a cryptographically
random 32+byte secret (for example a securely generated 64-character hex value),
identical on all nodes; effective trimmed byte length must be at least32.

Shared values on us01/us03/hk01/hk02/hk03:

```yaml
CONSOLE_FORWARD_SECRET: "{{SHARED_RANDOM_SECRET}}"
CONSOLE_FORWARD_PUBLIC_ORIGINS: "https://{{DOMAIN_MAIN}},https://{{DOMAIN_US01}},https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
# Optional defaults shown, bounded at startup:
CONSOLE_FORWARD_DIAL_TIMEOUT_MS: "2000"
CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS: "120"
CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS: "300"
```

Include only actual controlled origins (omit a nonexistent us01 domain rather than
leave a placeholder). Include the main Cloudflare alias as well as every worker
domain; scheme/host/effective port matches are exact. The master target must use a
fixed RFC1918/ULA private IP with explicit TCP port; no DNS, credentials, path,
query or fragment. Target and incoming-peer allowlist are separate concepts.

Add per node:

```yaml
# us01 (master): NO CONSOLE_FORWARD_MASTER_URL
NODE_TYPE: master
NODE_NAME: us01
CONSOLE_FORWARD_TRUSTED_CIDRS: "{{HK01_SOURCE_SEEN_BY_US01}}/32,{{US03_SOURCE_SEEN_BY_US01}}/32"
TRUSTED_PROXIES: "{{US01_INGRESS_PROXY_CIDR}}"  # or none if no ingress proxy

# us03
NODE_TYPE: slave
NODE_NAME: us03
CONSOLE_FORWARD_MASTER_URL: "http://{{US01_WG_IP}}:3000"
CONSOLE_FORWARD_TRUSTED_CIDRS: "{{US01_WG_IP}}/32"
TRUSTED_PROXIES: "{{US03_INGRESS_PROXY_CIDR}}"

# hk01
NODE_TYPE: slave
NODE_NAME: hk01
CONSOLE_FORWARD_MASTER_URL: "http://{{US01_WG_IP}}:3000"
CONSOLE_FORWARD_TRUSTED_CIDRS: "{{US01_WG_IP}}/32"
TRUSTED_PROXIES: "{{HK01_INGRESS_PROXY_CIDR}}"

# hk02 (through hk01's raw stream listener, not hk01's application)
NODE_TYPE: slave
NODE_NAME: hk02
CONSOLE_FORWARD_MASTER_URL: "http://{{HK01_WG_IP}}:13000"
CONSOLE_FORWARD_TRUSTED_CIDRS: "{{HK01_WG_IP}}/32"
TRUSTED_PROXIES: "{{HK02_INGRESS_PROXY_CIDR}}"

# hk03
NODE_TYPE: slave
NODE_NAME: hk03
CONSOLE_FORWARD_MASTER_URL: "http://{{HK01_WG_IP}}:13000"
CONSOLE_FORWARD_TRUSTED_CIDRS: "{{HK01_WG_IP}}/32"
TRUSTED_PROXIES: "{{HK03_INGRESS_PROXY_CIDR}}"
```

`NODE_NAME` is the actual variable (`common/node_identity.go:12–23`), **not** the
older draft's NEW_API_NODE_NAME. Worker trusted CIDRs cover incoming envelope peers;
they do not authorize backend targets or grant generic proxy trust. No implicit
RFC1918 trust exists for envelopes. Never keep broad default TRUSTED_PROXIES on a
private socket reachable by untrusted clients; ingress must strip/overwrite forged
XFF/X-Real-IP/CF/proto headers. Cloudflare IP policy itself is unchanged.

hk01 raw nginx stream example (top-level nginx context; not an HTTP /api proxy):

```nginx
stream {
    server {
        listen {{HK01_WG_IP}}:13000;
        allow {{HK02_WG_IP}};
        allow {{HK03_WG_IP}};
        deny all;
        proxy_pass {{US01_WG_IP}}:3000;
        proxy_connect_timeout 5s;
        proxy_timeout 1h; # inactivity timeout; set for the longest supported silent stream
        proxy_socket_keepalive on;
    }
}
```

Only WireGuard peers/firewall may reach it/us01:3000. Do not add PROXY protocol:
application peer verification expects actual socket addresses. Master sees hk02/
hk03 as hk01, while their signed EntryNode remains hk02/hk03. Ensure us01 can reach
all upstreams/local services referenced by management operations after centralization.
Ensure every internal HTTP hop, including hk02→hk01→us01, is encrypted by WireGuard.

Recommended production secure-cookie config on all nodes:

```yaml
SESSION_COOKIE_SECURE: "true"
SESSION_COOKIE_TRUSTED_URL: "https://{{DOMAIN_MAIN}},https://{{DOMAIN_US01}},https://{{DOMAIN_US03}},https://{{DOMAIN_HK01}},https://{{DOMAIN_HK02}},https://{{DOMAIN_HK03}}"
```

Both false/true remain compatible per root design §11.7; false is not a security
endorsement. Keep shared session/auth key material consistent for local fallback.
`ServerAddress` remains the canonical **public DB-backed option**, not internal
master URL (no SERVER_ADDRESS env override assumed). OAuth allowed_origins and
provider-registered callbacks must cover supported public domains. Explicit passkey
RPID/origins remain authoritative; unrelated domains cannot share credentials by
proxying. Set-Cookie is host-only as existing handlers decide; no rewrite.

### Rollout / rollback / rotation

1. Hosted CI/security gates pass on exact integrated SHA; fill real origins/peers.
2. Enable master verifier first (secret/peers/origins, no target), then one direct
   worker; enable hk01 raw stream before hk02/hk03. Verify auth ceremonies per domain,
   channel save/cache convergence, NDJSON/SSE and local relay. No rollout executed here.
3. Correlate worker `console forwarding complete` and master `console ingress` with
   identical quoted request_id, entry_node and processing_node. Missing master marker
   is unknown, not proof of a successful trusted master rollout.
4. **Rollback workers before master:** unset **every** CONSOLE_FORWARD_* variable on
   each worker, restart, keep NODE_TYPE/NODE_NAME/TRUSTED_PROXIES and auth keys.
   Unsetting only the target is invalid partial config and prevents startup.
5. Only then remove hk01 stream and master verifier config. No DB/schema rollback.
6. Secret rotation: disable forwarding on all workers using step4, keep master verifier
   until worker drain/restart completes, replace master secret, then enable workers
   with the new secret one by one. A rolling key mismatch yields 403 (never local
   replay); do not claim clock-skew tolerance makes mismatched keys compatible.

## 6. OWASP references and unresolved assurance

Read applicable OWASP Authentication, Session Management, CSRF Prevention, OAuth2 and
Passkey Security cheat sheets; ASVS **5.0.0** tagged chapters V3/V6/V7/V10/V12.
References: `https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html`,
`Session_Management_Cheat_Sheet.html`,
`Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html`, `OAuth2_Cheat_Sheet.html`,
`Passkey_Security_Cheat_Sheet.html`; ASVS sources under
`https://github.com/OWASP/ASVS/tree/v5.0.0/5.0/en`.

Applicable retained controls include ASVS3.3.1–4 (cookie transport/scope/HttpOnly),
3.5.1–3 (origin/CSRF and method protections), 6.3.1/6.3.4 (abuse protection and all
login pathways), 7.2.1/7.2.4 and7.4.1–2 (server session validation/rotation/revocation),
10.1.2/10.2.1–2 (transaction/browser-bound OAuth state/PKCE), and12.3.1–5 (encrypted
and authenticated internal communication). The proxy must preserve these controls;
it is not a replacement for them and is **not whole-application ASVS compliance**.

Unresolved/release-blocking verification: full login/refresh/logout/OAuth/passkey
ceremonies through actual public domains, hosted regression/format/build/race checks,
network/firewall/WireGuard/source-address inventory. Existing direct passkey
scheme auto-detection still trusts raw forwarded headers (helper's recorded
pre-existing risk; explicit origins/RPID mitigate it). Secure-cookie false mode does
not satisfy ASVS secure-cookie recommendations. In-memory nonce state is lost on
master restart: up to30s remaining envelope replay window accepted by root; handler
single-use state/challenges remain mandatory. Authorized compromised worker/shared
secret holder can assert client provenance. No durable replay or multi-master
nonce-coordination guarantee is claimed.

## 7. Hosted-only validation handoff

Queue #5 final test checklist is in `research/fwd-test.md`; pi-q5 authored real
pooled warm-up/reuse proof, empty+nonempty no-replay, live gated SSE/multipart,
truncation through PanicRecover, cancellation, trailers, real received -1 length/
proof verification, trusted-ingress negative cases and provenance/secret-redaction.
These are source-authored cases, **not executed results**. pi-q5 marked queue #5
DONE and finalized `research/fwd-test.md`; final fixture fixes include `t.Context()`
on public requests to avoid Gin1.9.1 CloseNotify fallback on httptest recorders,
and draining fake-master bodies. Existing selector prefixes cover all additions;
its fixture guidance is `-p 1 -count=1`, no t.Parallel, no external DB/Redis envs.
Original tests had fixture CIDR mismatch, reused a request mutated by ingress,
never really reused a connection and could coalesce SSE; those corrections are
owned by pi-q5, not silently counted as passing here.

Forwarding has no DB query/schema path. Pure forwarding fixtures use httptest and
explicit globals/env, no external datastore; unrelated DB-behavior changes in the
parent still require SQLite/MySQL/PostgreSQL and affected ClickHouse hosted matrix.
No relaykit/tokenkit file changed.

Commands for **GitHub Actions only** (not run locally):

```sh
go test ./middleware/ -run 'TestConfigureTrustedProxies|TestLoadConsoleForwardConfig|TestVerifiedConsoleIngress|TestEffectiveRequestScheme|TestConsoleIngress|TestConsoleForwarder|TestSessionCookieOriginGuard' -count=1
go test -race ./middleware/ -run 'TestConsoleIngress|TestConsoleForwarder' -count=1
go test ./service/passkey/ -run 'TestPasskeyOriginUsesVerifiedConsoleIngressOnly' -count=1
```

Also keep existing full backend formatting/vet/build gates and existing router
permission/auth-flow regressions; forwarding tests do not substitute for the live
provider/browser ceremonies. Exact final selectors go to pi-web #10. **No check
above is reported as passed.** Root owns commit/push/release and precise patch grouping.
