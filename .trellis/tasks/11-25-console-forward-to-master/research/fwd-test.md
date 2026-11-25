# Forwarding regression tests — pi-fwd-test

Owner: pi-q5 (pane w5D:p7), resumed from pi-fwd-test. Integrator: Claude (w5D:p1). Core owner: pi-fwd (w5D:p2). Final source handoff: 2026-11-25; execution remains GitHub Actions only.

All cases live in the EXISTING `middleware/trusted_proxies_test.go` (package `middleware`).
No new test files were created. `common/` has no trusted-proxy/config test file, so per
AGENTS.md "do not scatter tests" the `LoadConsoleForwardConfig` cases live in the same file.

No local tests/build/vet/lint/gofmt/typecheck/format/runtime validation or DB probes were run (task rule: GitHub Actions only). Read-only git diff inspected; no staging, commit, push, reset, checkout, stash or apply. No CI, locale/script, production or capacity files edited by this worker.

## Verified test-facing surface (read from source, not assumed)

`common/console_forward.go`:
- `ConsoleForwardConfig{MasterURL, Secret, TrustedPeers, IngressTrustedPeers, PublicOrigins, DialTimeoutMillis, HeaderTimeoutSecond, RequestTimeoutSecond}`
- `var ConsoleForwardSettings *ConsoleForwardConfig`
- `LoadConsoleForwardConfig() (*ConsoleForwardConfig, error)`
- `VerifiedConsoleIngress(*http.Request) (ConsoleIngressIdentity, bool)`, `WithConsoleIngressIdentity`, `EffectiveRequestScheme`
- `var IsMasterNode bool`, `var NodeName` are plain package vars, so tests set and restore them.

`middleware/console_forward.go`:
- `ConsoleIngress() gin.HandlerFunc` — master-side verifier; pass-through when settings are nil.
- `ConsoleForwarder() gin.HandlerFunc` — worker-side; pass-through when settings nil / no MasterURL / master node.
- `addConsoleForwardEnvelope(*http.Request, common.ConsoleIngressIdentity, []byte) error` — mints a valid envelope, so tests never re-implement the HMAC canonical encoding.
- `consoleForwardHeaderValues`, `consoleForwardProof` — used to re-sign a deliberately stale/future timestamp (valid HMAC, expired window).
- Reserved header constants `consoleForwardProofHeader`, `consoleForwardTimestampHeader`, `consoleForwardRequestIDHeader`.
- Behaviour constants asserted: 403 invalid envelope, 508 loop, 400 ambiguous path, 503 master-local job, 502 post-send.

### Real constraints the tests had to respect

1. `consoleWorkerIdentity` takes `RequestID` from `c.GetString(common.RequestIdKey)`, NOT from a raw
   request header. `consoleForwardHeaderValues` rejects empty values, so a forwarder mounted without
   `RequestId()` produces no envelope and silently skips forwarding. Every forwarder engine therefore
   mounts `engine.Use(RequestId(), ConsoleForwarder())`, matching production order (§11 item 6).
2. `applyVerifiedConsoleIdentity` rewrites `RemoteAddr` to the signed client IP and strips
   XFF/X-Real-IP/Forwarded/CF-Connecting-IP, so the ingress probe reads `c.ClientIP()` after the
   middleware, asserting the signed IP wins.

## Cases written (design §10 table)

| Area | Test |
|---|---|
| Config unset ⇒ disabled | `TestLoadConsoleForwardConfigUnsetKeepsLocalBehavior` |
| Partial/invalid config fails startup (short secret, missing peers/origins, bad CIDR, non-HTTPS origin, target with path/user/query/public/loopback/no-port, master+target, timeout bounds; final deltas below) | `TestLoadConsoleForwardConfigRejectsPartialAndInvalidConfig` |
| Bounds applied + default 2000/120/300 and overrides honoured | `TestLoadConsoleForwardConfigAppliesBoundedTimeouts` |
| Master never forwards | `TestLoadConsoleForwardConfigMasterNeverForwards` |
| No proof ⇒ not verified | `TestVerifiedConsoleIngressRequiresProof` |
| Scheme: real TLS, never raw X-Forwarded-Proto, signed scheme only when Forwarded | `TestEffectiveRequestSchemeUsesSignedSchemeOnlyWhenForwarded` |
| Peer CIDR AND HMAC; tampered/stale/future/replayed proof; spoofed headers from public peer; origin outside allowlist; signed loop guard (508 on worker) | `TestConsoleIngressRequiresTrustedPeerAndValidProof` |
| Scope: `/api`+`/api` descendants+OPTIONS forwarded; GET `/api/status/test`, `/v1`, `/v1beta`, `/pg`, `/mj`, `/dashboard/billing`, `/assets`, `/apix`, `/` local | `TestConsoleForwarderScope` |
| Ambiguous encoded path ⇒ 400, never forwarded | `TestConsoleForwarderRejectsAmbiguousEncodedPath` |
| Zero worker datastore middleware entered | `TestConsoleForwarderSkipsWorkerDatastoreMiddleware` |
| Body/query/method/Authorization/Cookie fidelity, status, repeated Set-Cookie verbatim | `TestConsoleForwarderPreservesRequestAndResponse` |
| SSE incremental flush (real client reads first event before master may emit second) | `TestConsoleForwarderFlushesStreamingResponses` |
| Fresh dial failure ⇒ local chain once with unread body | `TestConsoleForwarderFallsBackLocallyOnFreshDialFailure` |
| Master-local job route ⇒ 503, no local execution | `TestConsoleForwarderReturns503ForMasterLocalJobs` |
| Post-send failure ⇒ single 502, master executed at most once, incl. bodiless GET | `TestConsoleForwarderNeverReplaysAfterSend` (4 actual reused-connection cases) |
| Request id propagates worker⇒master and equals the id returned to the browser | `TestConsoleForwarderPropagatesRequestID` |
| Settings nil ⇒ today's behaviour unchanged | `TestConsoleForwarderSkippedWithoutSettings` |

## CI selectors for root to wire into `.github/workflows/ci.yml`

```bash
go test ./middleware/ -run 'TestConfigureTrustedProxies' -count=1
go test ./middleware/ -run 'TestLoadConsoleForwardConfig' -count=1
go test ./middleware/ -run 'TestVerifiedConsoleIngress' -count=1
go test ./middleware/ -run 'TestEffectiveRequestScheme' -count=1
go test ./middleware/ -run 'TestConsoleIngress' -count=1
go test ./middleware/ -run 'TestConsoleForwarder' -count=1
```

Single combined selector:

```bash
go test ./middleware/ -run 'TestConfigureTrustedProxies|TestLoadConsoleForwardConfig|TestVerifiedConsoleIngress|TestEffectiveRequestScheme|TestConsoleIngress|TestConsoleForwarder' -count=1
```

`-count=1` is deliberate: the nonce replay guard is per-middleware-instance in-memory state.

## Final item #5 additions / corrections

- Corrected signed socket-peer fixture to `10.100.3.0/24` (the original `10.100.0.0/24` did not include the signed peer). Replay uses an envelope cloned BEFORE ingress mutates its RemoteAddr, URL and headers. Config fixtures set `TRUSTED_PROXIES=none`; test routers set explicit proxy trust where relevant. Public request fixtures use `t.Context()` to model real server cancellation and avoid Gin's unsupported CloseNotify fallback on httptest.ResponseRecorder.
- `TestConsoleIngressRequiresTrustedPeerAndValidProof`: signed client IP/host beat spoofed XFF/X-Real-IP/Forwarded/CF/scheme aliases; actual peer IP retained; all private/proxy aliases stripped; verified request ID survives ingress -> RequestId order.
- `TestConsoleIngressWorkerLoopGuardRequiresValidProof`: valid envelope arriving from an unlisted worker socket gets 508 without granting provenance; forged public markers are stripped and continue normally; configured peer with bad proof gets 403, not a false loop.
- `TestConsoleIngressRejectsTamperedRequestBindings`: changed method/path/query, Authorization/Cookie/Origin/Referer/content headers, declared length, entry/request IDs, duplicated/missing proof and oversized metadata fail before handlers.
- `TestConsoleForwarderScope`: healthy master receives process-local poll/cancel/stream and log exports, unknown API descendants and POST health path; ONLY GET health exception, relay/playground/billing/SPA callbacks/static stay local.
- `TestConsoleForwarderRejectsAmbiguousEncodedPath`: encoded slash/dot/backslash and decoded dot segments return 400 without contacting master.
- `TestConsoleForwarderPreservesRequestAndResponse`: exact escaped path and raw query (semicolon and invalid escape included), body, method, public Host, credentials/origin/fetch/content headers, request/response trailers, repeated Set-Cookie unchanged; hop/proxy aliases stripped.
- `TestConsoleForwarderFlushesStreamingResponses`: real worker client and outer PanicRecover; master blocks before event two until client reads event one. No sleeps or timing-performance assertions.
- `TestConsoleForwarderFallsBackLocallyOnFreshDialFailure`: tracked original request body is identical, unread and open when local fallback starts; exactly one local execution.
- `TestConsoleForwarderReturns503ForMasterLocalJobs`: exact method/path fallback table covers fingerprint poll/cancel and SSE 503; fresh fingerprint starts, DB-backed plugin-login routes, nonstream pull and wrong-method neighbors may fall back exactly once.
- `TestConsoleForwarderNeverReplaysAfterSend`: successful warm-up, explicit GotConn.Reused=true and same TCP peer establish actual pooled reuse. Empty POST/GET/DELETE and nonempty POST each execute master once, return 502 and never execute worker locally.
- `TestConsoleForwarderPropagatesRequestID`: real empty GET goes through worker ReverseProxy/transport to an HTTP server running the actual wire-envelope verifier; it additionally requires zero body bytes and received ContentLength=-1. Signed request ID matches browser ID; private processing-node header never reaches browser.
- `TestConsoleForwarderTruncatedStreamDoesNotBecomeSuccess`: real HTTP client observes read error after master's deliberately truncated 200 response; outer PanicRecover never appends 500 JSON; master executed once. Companion no-Hijacker writer case cannot append panic JSON either.
- `TestConsoleForwarderRethrowsNonAbortPanic`: a non-sentinel writer panic reaches outer recovery; only http.ErrAbortHandler may be handled internally.
- `TestConsoleForwarderHeaderTimeoutNeverFallsBack`: master receives request and withholds headers; bounded header timeout returns 502 without local replay.
- `TestConsoleForwarderPropagatesCancellation`: explicit context cancellation reaches master and stops worker proxy without fallback.
- `TestConsoleForwarderStreamsMultipartWithoutPrereading`: 2MiB multipart producer waits until master receives headers before producing any body bytes, detecting accidental pre-read/buffering.
- `TestConsoleForwarderPreservesMasterBodyLimit`: actual AnonymousRequestBodyLimit on master returns 413, never invokes handler or worker fallback.
- `TestConsoleForwarderTrustsSchemeOnlyFromConfiguredIngress`: XFP from narrow ingress trust works, public/multi/comma spoof does not manufacture an allowlisted HTTPS origin; existing Gin XFF policy supplies IP, never CF alias.
- `TestConsoleForwarderLogsProvenanceWithoutCredentials`: quoted hk01 entry + us01/unknown processing labels; Authorization, Cookie, body, query code/state, secret, nonce and proof never logged; private response marker stripped.
- `TestConsoleForwarderSignsAfterHopHeaderCleanup`: Connection-nominated Authorization is removed and final proof still verifies; master returns 401 for absent credential (provenance is NOT authentication), worker does not bypass it.
- `TestConsoleForwarderMasterNeverForwards` / `TestConsoleForwarderPreservesMasterFailureResponses`: even programmatically supplied target cannot forward master; master 401/403/500/302 and Location/body pass verbatim, no redirects followed or local retries.
- Config rejection table also pins whitespace-padded short secret, dangling target `?`/`#` and escaped-root URL rejection.

## FINAL CI fixtures / selectors for item #10

Only changed test file: `middleware/trusted_proxies_test.go`. Existing combined selector above covers every addition; no new prefix is needed. Run standard and race middleware selections in hosted CI with `-p 1 -count=1`, and do not add `t.Parallel`: role/config/env and request-limit fixtures are process globals, restored by cleanup. No DB, Redis, MySQL/PostgreSQL/SQLite/ClickHouse services or external fixture envs are needed. HTTP/TCP servers are loopback httptest fixtures when CI executes the tests. Existing package TestMain, if any, retains its normal prerequisites; this worker introduced none.

Auth-origin/session/OAuth/passkey ceremony and production-router guards remain separate owner coverage, not falsely claimed by these transport fixtures. The empty-wire test uses the real verifier directly on a real HTTP master hop because two simultaneous public middlewares cannot honestly have different roles in one process-wide IsMasterNode global; the public master ingress path is tested independently.

OWASP controls consulted: Authentication and Session Management Cheat Sheets (credential transport/logging/cookie preservation), CSRF Prevention Cheat Sheet (Origin/Referer and proxy trust). Official ASVS stable release checked as 5.0.0; chapter-level context V3/V6/V7/V12, no unverified requirement IDs or application-wide compliance claim. Changes are tests only. Hosted execution, race results and hosted formatter output are PENDING; source inspection is not a passing validation result.
