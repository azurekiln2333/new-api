# Second security review — forwarding core (pi-fdw-sync)

Active task: `.trellis/tasks/11-25-console-forward-to-master`.
Reviewer: **pi-fdw-sync**. Core owner: **pi-billing** (w4C:pJ). Integrator: Claude root (w4C:p17).

Inspection only. No source edits, no tests, builds, vet, gofmt, typecheck or git commands were run.
Revision reviewed: `middleware/console_forward.go` (20 659 bytes — **the file moved under this review**;
line numbers below are against that revision, re-check before applying a fix), `common/console_forward.go`,
`middleware/request-id.go`, `middleware/auth_origin.go`, `service/passkey/service.go`,
`oauth/linuxdo.go`, `controller/video_proxy.go`, `common/session_cookie.go`, `main.go:216-230`,
`router/api-router.go`. Criteria: design §4, §6, §11 items 2,3,4,6,8,10,11.

> **This file supersedes the 05:25 revision.** The core changed twice after that review (05:31 and 05:33),
> and one item regressed: `consoleForwardErrorClass` was made meaningful and is now back to returning
> `"invalid"` for everything. Verify each line number before fixing — the file is being edited live.

Severity: **P0** build-breaking / control defeat · **P1** binding-decision violation or real
security/fidelity defect · **P2** bounded correctness issue · **P3** hygiene.

---

## Fixed since the previous pass (do not re-raise)

| Was | Now |
|---|---|
| P0-1 type name mismatch (`consoleForwardVerifier` vs `consoleIngressVerifier`) → no compile | Closed. Struct is `type consoleForwardVerifier struct` (`:199`) with methods `:205`, `:214`, `:258`; construction `:66` matches. |
| P1-5 `consoleForwardVerifier` lacking `mu` | Closed. `mu sync.Mutex` at `:201`, held in `reserveNonce` (`:259-260`). |
| Node reuse of `proxyTemplate` copy | Correct: `proxy := *proxyTemplate` (`:169`) per request, `ErrorHandler` set per request; no data race on the shared template. |
| `ConsoleIngress` with `ConsoleForwardSettings == nil` | Improved: the nil-config branch now calls `stripConsoleForwardHeaders(c.Request.Header)` before `c.Next()` instead of passing reserved headers through. |

> **Regression:** the error-classification fix did not survive the 05:33 edit — see **P1-5** below, which
> is outstanding again.

---

## P0

None outstanding in the current revision.

---

## P1 — fix before CI is called green

### P1-1 (§4) Forwarded master rebuilds OAuth `redirect_uri` with `http`, breaking OAuth login on any HTTPS deployment

`applyVerifiedConsoleIdentity` (`:500-506`) sets `request.Host` and `request.URL.Scheme` from the signed
envelope but **never** gives downstream `r.TLS` any meaning, and it strips `X-Forwarded-Proto`.
`oauth/linuxdo.go:58-62` computes:

```go
scheme := "http"
if c.Request.TLS != nil { scheme = "https" }
redirectURI := fmt.Sprintf("%s://%s/api/oauth/linuxdo", scheme, c.Request.Host)
```

On the master, `r.TLS == nil` for a forwarded request, so the token exchange sends
`redirect_uri=http://<public-host>/api/oauth/linuxdo` while the browser used `https://…`. The provider
rejects the mismatch and OAuth login fails for every console user that is forwarded. This is the exact
class of artifact design §4 says must not leak ("the verified envelope is the only override, internal
transport artifacts must not surface").

Evidence: `middleware/console_forward.go:500-506`, `oauth/linuxdo.go:58-62`.
Fix: route this computation (and any other `r.TLS` read on the `/api` path) through
`common.EffectiveRequestScheme(c.Request)`. `middleware/auth_origin.go:67` and
`service/passkey/service.go:256` already do; `oauth/linuxdo.go` does not. `controller/video_proxy.go:576`
is a second instance (host-matching only, lower impact) — it also reads `X-Forwarded-Proto`, which the
master has stripped, so it degrades to `http` as well.

### P1-2 (§11.2) The no-replay guard's condition is wrong — `Body == nil` is never true for a server request, so the guard is dead code and bodiless requests stay replayable

`consoleNoReplayTransport.RoundTrip` (`:400-409`) and the worker path (`:148-153`) both test
`outbound.Body == nil` before substituting a single-use reader. `net/http`'s server sets
`Request.Body = http.NoBody` (a **non-nil** zero reader) for every bodiless request, and
`Request.Clone` copies that pointer, so `Body == nil` is false for essentially every forwarded console
request. The branch is unreachable: the signed envelope's content-length is computed from the real
`ContentLength` (`:327`), nothing is rewritten to chunked, and — critically — nothing is done to stop
replay.

Why that matters for §11.2 ("all retries disabled, prove ≤1 master execution"):
`GetBody = nil` (`:153`, `:402`) does prevent `net/http` from re-sending a request that **has** a body,
because `Request.isReplayable` is false when `GetBody` is nil. But for a bodiless request
`Body == http.NoBody`, `isReplayable()` returns `true` for `GET`/`HEAD`/`OPTIONS`/`TRACE`
unconditionally, so `persistConn.shouldRetryRequest` will still retry a forwarded console `GET` on a
**reused** connection when the master closes it (`errServerClosedIdle`, `nothingWrittenError`,
`transportReadFromServerError`). `transportReadFromServerError` in particular means the master read the
request and then dropped the connection — it may already have executed the write. If the retry's fresh
dial then fails, `isFreshDialFailure` returns true and the worker executes the request locally: two
executions of one console action.

Evidence: `middleware/console_forward.go:148-153`, `:400-409`, `:327`, `:226-229`.
Fix: match the actual sentinel — `if outbound.Body == nil || outbound.Body == http.NoBody { outbound.Body =
io.NopCloser(strings.NewReader("")) }` — and **do not** touch `ContentLength` or `TransferEncoding`, so
the request still goes out with `Content-Length: 0` and stays consistent with the signed envelope while
becoming non-replayable. That, plus P1-3's `GotConn.Reused` gate, is what closes §11.2/§11.3.

Secondary point, still valid: `verify` only rejects `contentLength < -1` (`:226-229`) and never compares
it with the received body, so the signed length detects no truncation or padding. Either enforce it
(`request.ContentLength` must equal the envelope value) or drop the field so it does not imply integrity
it does not provide.

### P1-3 (§11.3) `isFreshDialFailure` accepts a dial that only happened **because net/http retried a dead pooled connection** — that is not a pre-send failure

Design §11.3 allows local fallback only when a **fresh** connection fails at `dial`. The predicate
(`:540-545`) unwraps to `*net.OpError` and accepts any `Op == "dial"`. But `net/http` only retries a
request when the connection was **reused** (`persistConn.isReused()`), and after such a retry the
transport dials a new connection; if that dial fails, the worker sees a plain dial `*net.OpError` and
falls back locally (`:180-188`) even though the master may already have received and executed the
first attempt. The error type cannot distinguish "never reached the master" from "reached it, then the
connection dropped, then the retry's dial failed".

Evidence: `middleware/console_forward.go:540-545`, `:180-188`, `:400-409`.
Fix: attach `httptrace.ClientTrace` to the outbound request and record `GotConnInfo.Reused`; permit
fallback only when the recorded connection was **not** reused **and** the error is a dial op. Everything
else → 502 (or abort the stream), as §11.3 already requires for post-send failures. Combined with P1-2
(which makes bodiless requests non-replayable so `net/http` stops retrying them at all), this closes the
double-execution window.

### P1-5 (§4) *(regressed)* `consoleForwardErrorClass` again collapses every failure to `"invalid"`

`verify` (`:214-256`) returns distinguishable sentinel strings (`proof`, `timestamp`, `content_length`,
`client_ip`, `bounds`, `scheme`, `origin`, `nonce`, `replay`), but `consoleForwardErrorClass` (`:547-552`)
returns `"invalid"` for every non-nil error and only `"unknown"` for nil. The rejection log at `:87`
therefore prints `class=invalid` for a clock-skew node, an active replay attempt and a hostile scanner
alike, so a replay attack cannot be distinguished from misconfiguration in the audit trail.
Evidence: `middleware/console_forward.go:87`, `:547-552`. Fix: `return err.Error()` (the sentinel strings
are already bounded and contain no secret material).

### P1-4 (§3) Loop guard runs **after** the nonce is reserved, so a misrouted-but-validly-signed request burns nonce capacity and reports `replay` instead of `loop`

`verify` reserves the nonce at `:246-248`; the caller checks `common.IsMasterNode` at `:91-95`. On a
worker, a correctly signed envelope (only producible by a misconfigured node) is recorded as consumed
before the 508 is returned, so:
* the registry (bounded at 100 000, TTL 60 s) can be exhausted by re-sending the same signed request —
  each attempt burns a slot only if the nonce differs, but every legitimate concurrent request now
  competes with junk;
* retrying the identical request is answered `replay`, not `loop`, which hides the actual
  misconfiguration in the logs.

Design §6/§11.6 require loop detection to be the first, cheapest rejection.
Evidence: `middleware/console_forward.go:85-95`, `:246-248`.
Fix: check the node role in `ConsoleIngress` before calling `verify`, or pass the role into `verify` and
return `loop` before `reserveNonce`.

---

## P2

### P2-0 (§4) The worker's forwarded client IP is `c.ClientIP()`, which trusts `TRUSTED_PROXIES`, and the default trusted set is every private range

`consoleWorkerIdentity` (`:450-465`) signs `net.ParseIP(c.ClientIP())` into the envelope, and the master
uses that signed value as the real client (`request.RemoteAddr` at `:501`). `ClientIP()` resolves
`X-Forwarded-For` only for peers listed in gin's trusted proxies, and when `TRUSTED_PROXIES` is unset the
default is `127.0.0.0/8`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`
(`common/trusted_proxies.go:11-19`). The internal console-forwarding hop is itself a private IP, so a
console request arriving *from another worker* can carry a caller-chosen `X-Forwarded-For` that the entry
node trusts and then signs, and the master will believe it. The same default makes every WireGuard/LAN
peer a trusted proxy for `X-Forwarded-Proto` in `consoleWorkerIdentity` (`:458-463`) and in
`consoleForwardIngressPeerTrusted`.

This is pre-existing gin/`TRUSTED_PROXIES` behavior, not a forwarding bug — but the forwarding boundary
now *launders* it across nodes and attaches it to a signature, so it deserves an explicit deployment
requirement rather than silent inheritance.
Evidence: `middleware/console_forward.go:450-465`, `:501`, `common/trusted_proxies.go:11-19`.
Action (mostly docs, in `research/deploy-runbook.md`, plus a startup warning): require explicit
`TRUSTED_PROXIES` in any multi-node deployment, and state that a node reachable by an untrusted client on
a private address must never keep the default set. If you want it enforced, log a warning at startup when
forwarding is configured and `TRUSTED_PROXIES` is empty (defaults in effect).

### P2-1 (§6) Streaming is exempted by one exact path, everything else gets a hard total deadline
`consoleStreamingRoute` (`:467-469`) matches only `/api/channel/ollama/pull/stream`; the 30 s-style
`context.WithTimeout` at `:164-168` is applied to every other forwarded route. Any future SSE/long-poll
console endpoint added under `/api` will be silently truncated at the request timeout with no
`ErrorHandler` signal (the response has already started streaming, so `proxyErr` handling at `:190-195`
aborts without a body). Suggest a small explicit allow-list/set that is easy to extend, and a comment
requiring the author of any new streaming console route to register it.

### P2-2 (§11.4) Process-local route matcher is positional, not a route table
`consoleProcessLocalRoute` (`:439-448`) hard-codes segment counts and names. It correctly covers
`GET /api/channel/:id/fingerprint/:job` and `POST …/cancel` and `POST /api/channel/ollama/pull/stream`,
but it will silently stop matching if a route gains a prefix/version segment, and it cannot be
grepped against `router/`. A missed entry here means a process-local route returns the master's 404
instead of the honest 503 `console master unavailable`, which is a user-visible wrong error.
Recommend deriving it from an explicit `map[string]struct{}{"GET /api/…":{}}` keyed by
`method + " " + decoded path pattern`.

### P2-3 Reserved-header stripping is correct but the "has reserved headers" gate is header-existence only
`ConsoleIngress` (`:71-83`) trusts `consoleForwardHasReservedHeaders` (`:524-529`) to decide whether any
stripping/verification is needed. A trusted peer that sends **a subset** of reserved headers (e.g. only
`X-New-Api-Console-Proof`) hits `verify`, fails on the first missing header, and is 403'd — fine. But a
trusted peer that sends *none* is treated as an ordinary request and skips verification, which is
intended. The residual risk is only that a future reserved header added to one list and not the other
(`consoleForwardReservedHeaders` vs the `consoleForwardHeaders` struct in `consoleForwardHeaderValues`)
would be unverified-but-forwarded. Both lists currently agree (10 headers each); keep them derived from
one source.

---

## P3

* `boundedHeader` (`:555-558`) has no callers at any revision — dead code; remove.
* `safeConsoleRoute` (`:560-564`) collapses every console route to `"/api"`, so `route=` in the
  forwarding logs is a constant and cannot be used to find the failing endpoint. Log the first two
  path segments (or a bounded, sanitised path) instead.
* `reserveNonce` sweeps the entire map on every request (`:261-265`); at the 100 k cap with a 60 s TTL
  that is a full-map iteration per forwarded request. Acceptable at current scale, but a periodic
  janitor goroutine or a coarse time-bucketed map would remove the per-request O(n).
* `requestWithProofPlaceholder` (`:336-339`) mutates the real request to hold `"placeholder"` before
  computing the proof; it works because the proof header is `Set` again at `:332`, but a caller reading
  the header mid-call would see the placeholder. Prefer passing the placeholder value into
  `consoleForwardProof` directly.
* Two helpers do the same job: `stripConsoleForwardHeadersFromRequest` / `outboundFallbackRequest`
  (`:531-538`) is just `stripConsoleForwardHeaders(request.Header)` wrapped twice. Inline it.
* Alignment/gofmt **(CI-blocking)**: the const block (`:26-43`) and dozens of one-line `if` bodies
  (`:280-301`, `:426`, `:457`, `:485`, `:509`, `:513`, `:525`, `:542`, `:556`) are not gofmt-aligned, and
  there is a double blank line before `boundedHeader` (`:553-554`). The hosted formatter job will fail.

---

## Deployment / config observations (not code defects)

* `CONSOLE_FORWARD_SECRET` is validated as ≥ 32 bytes but has no minimum-entropy or rotation story. It is
  a shared symmetric key across every node; a leaked value lets any node forge envelopes for any other.
  Document rotation (change on all nodes, restart together — 30 s skew means a rolling restart is safe for
  reads but not for writes) in `research/deploy-runbook.md`.
* `CONSOLE_FORWARD_MASTER_URL` must be a fixed private IP (`common/console_forward.go`), which is good —
  it removes DNS rebinding and hostname-trust questions entirely, and it is why the TLS/host mismatch
  class of bug cannot happen on the transport itself.
* `PublicOrigins` requires `https://` unless loopback — correct, and it is checked on both ends (worker
  before forwarding `:141`, master inside `verify` `:240`).

---

## Passed checks (verified in this revision)

* **HMAC canonical form** (`consoleForwardProof`, `:341-378`) binds, in order and with big-endian
  length prefixes: version, timestamp, nonce, entry node, client IP, scheme, host, request ID,
  content-length, uppercase method, escaped path (preferring `RawPath`), **raw query**, and SHA-256
  hashes of `Authorization`, `Cookie`, `Origin`, `Referer`, `Content-Type`, `Content-Encoding`.
  Method, path, raw query and the credential/origin hashes are all bound. Ambiguous paths are refused
  before forwarding (`ambiguousConsolePath`, `:424-437`, rejects `%2f`, `%5c`, `%2e`, `.`/`..`, `\`).
* **Constant-time comparison** — `hmac.Equal` at `:219`.
* **Peer identity from the socket** — `consoleForwardPeerIP` uses `net.SplitHostPort(request.RemoteAddr)`
  (`:492-498`), never `ClientIP()`, and is used for both `peerTrusted` (`:205-212`) and the ingress
  trusted-peer check (`:471-482`). Public clients with reserved headers get them stripped (`:80`).
* **Timestamp window** — rejected when `time.Since` **or** `time.Until` exceeds
  `consoleForwardClockSkew` = 30 s (`:40`, `:222-225`).
* **Nonce** — reserved only after the HMAC, timestamp, bounds, scheme, origin and nonce-format checks
  (`:246-248`); must be `base64.RawURLEncoding` and ≥ 20 chars; TTL 60 s; registry bounded at 100 000
  and **fails closed** when full (`:266-268`).
* **Reserved header stripping** — worker strips before signing (`:119`, `stripConsoleForwardHeaders`)
  and re-adds only the reserved set from `In` (`:121`, `:518-522`); master strips for untrusted peers
  (`:80`) and in `applyVerifiedConsoleIdentity` (`:504`, plus `stripUntrustedProxyHeaders` `:512-516`
  removes `X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `CF-Connecting-IP`, `X-Forwarded-Host/Proto/Protocol`).
* **Loop guard exists on both sides** — master never forwards (`:108` returns early when
  `common.IsMasterNode`), worker/monitor rejects a signed envelope with 508 (`:91-95`). *Ordering is
  P1-4.*
* **Transport tuning** — HTTP/1.1 only (`ForceAttemptHTTP2: false`), bounded pool
  (100 / 32 per host), `IdleConnTimeout` 45 s, `ResponseHeaderTimeout`, 3 s TLS handshake, dial timeout
  from config, `FlushInterval: -1` for streaming writes, `DisableCompression: true`.
* **Bodied requests are not replayed** — `GetBody` is explicitly cleared (`:153`, `:402`), which makes
  `Request.isReplayable` false for a request carrying a body, so `net/http` cannot re-send a `POST`/`PUT`.
  *(Bodiless `GET`/`DELETE` and the dead `Body == nil` guard are P1-2; the pooled re-dial fallback is P1-3.)*
* **No secret or proof in logs** — every forwarding log line logs identity node names, request id,
  method, `safeConsoleRoute` and status only (`:87`, `:142`, `:159`, `:176`, `:181`, `:190`, `:99`).
  The proof, nonce and secret are never logged. The 402/403/502/503 bodies are fixed strings.
* **Fallback only on dial errors, process-local routes get 503** — `isFreshDialFailure` (`:540-545`) is
  the only path that continues locally (`:180-188`); process-local routes return
  `503 {"message":"console master unavailable"}` (`:183`) instead of a master 404. *(Breadth is P1-3.)*
* **Config validation** — `common/console_forward.go` rejects a secret < 32 bytes at
  `addConsoleForwardEnvelope` (`:305`) and loads settings fatally at startup (`common/init.go:92-97`).
* **Public-origin gate** — `validConsolePublicOrigin` (`:484-490`) normalises through
  `common.NormalizeOrigin` and requires an exact match in `config.PublicOrigins`; checked on both the
  worker before forwarding (`:141`) and the master inside `verify` (`:240`).
* **Ambiguous-path refusal** happens *before* any forwarding (`:125-128` → 400), so a path-trick request
  never reaches the master.

---

## Recommended fix order

1. **P1-1** OAuth `redirect_uri` scheme — user-visible auth breakage on every HTTPS master.
2. **P1-3** fallback on pooled re-dial — the only remaining double-execution window.
3. **P1-2** bodiless chunked rewrite / signed length not enforced.
4. **P1-5** error class propagation (one-line fix, restores replay visibility).
5. **P1-4** loop guard before nonce reservation.
6. **P2-0/P2-1/P2-2** `TRUSTED_PROXIES` deployment requirement, streaming allow-list, process-local
   route table.
7. **P3** gofmt (CI-blocking), dead `boundedHeader`, `safeConsoleRoute`.
