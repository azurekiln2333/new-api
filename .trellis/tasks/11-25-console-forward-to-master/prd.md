# Forward console API from worker nodes to master

## Goal

Production: PostgreSQL and Redis run on us01 (master). us03, hk01, hk02, hk03 are workers, each with its own public domain (main console domain is Cloudflare-proxied to an HK node). A console request processed on a worker pays one cross-region round trip for every sequential DB/Redis call (prod SLOW SQL logs: 320–1400ms per statement; warm middleware alone is 4 serial Redis calls before the handler, see `../11-24-management-mutations-code-batches/research/middleware-round-trips.md`).

Workers forward console HTTP requests over the existing WireGuard network to the master's new-api, which is co-located with the data, so each console request crosses regions once. Relay traffic (`/v1` and other model/task relay routes) stays on the worker. The user chose this over changing DNS because every node domain must give a fast console.

User authorized implementation, cloud-only validation, commit, push and the existing release workflow on 2026-11-25. NO LOCAL TESTS, BUILDS, VET, LINT, TYPECHECKS, GOFMT CHECKS, DATABASE PROBES OR EXECUTABLE VALIDATION.

## Requirements

| ID | Observable requirement |
|---|---|
| F1 | Opt-in per node by environment: a master base URL on the internal network (e.g. `http://10.100.2.1:3000`) plus a shared secret. Unset means today's behavior. A master never forwards, and a forwarded request is never forwarded again (loop guard). |
| F2 | Scope: every HTTP route the web console calls that reads or writes shared state (the `/api` group and any console/auth/OAuth routes outside it, enumerated from source in design). Model/task relay routes, playground relay, static frontend assets and health checks stay local. Forwarding runs before any worker middleware that performs DB/Redis I/O, so a forwarded request performs zero DB/Redis calls on the worker. |
| F3 | Transparent: method, path, query, streamed body, Cookie/Authorization and other end-to-end headers reach master; status, headers and Set-Cookie return unchanged. Streaming responses flush immediately. WebSocket or long-poll routes in scope are either forwarded correctly or explicitly excluded with evidence. Upload size limits are unchanged. |
| F4 | Identity and trust: master uses the real client IP, scheme and public host only when the request comes from a configured internal source AND carries a valid shared-secret proof; otherwise forwarded headers are ignored. Rate limiting, login/registration abuse protection, audit IP, session behavior, refresh/logout Origin validation, CSRF checks, OAuth/OIDC redirect URIs, WebAuthn origin/RP ID and cookie attributes behave as if the browser had reached that worker's public domain directly. No security control is weakened (read OWASP Authentication, Session Management, CSRF and relevant OAuth/WebAuthn guidance and ASVS first, per AGENTS.md). |
| F5 | Failure: if the master cannot be reached before any request bytes are sent, the worker processes the request locally (correct but slow) and logs it. After request bytes are sent, a failure returns a clear 502 and never replays the request. Timeouts are bounded except for streaming responses. |
| F6 | Observability: the request id propagates; logs on both nodes record entry node and processing node; existing log "processing node" semantics stay truthful. |
| F7 | Consistency: forwarded writes go through master's existing cache publication and sync topic; worker relay caches converge through the existing sync mechanism; no new staleness for relay beyond today's sync interval. |

## Acceptance Criteria

- [ ] Worker with forwarding configured serves a console request with zero worker DB/Redis calls; relay routes are not forwarded.
- [ ] Without forwarding configured, behavior is unchanged.
- [ ] Spoofed forwarding headers (missing/invalid proof or non-internal source) are ignored; valid proof yields the real client IP for rate limiting and audit.
- [ ] Login, refresh, logout, OAuth callback, passkey and cookie flows work through a worker domain; Origin/CSRF validation still rejects foreign origins.
- [ ] Streaming console responses flush incrementally through the forwarder.
- [ ] Dial failure falls back locally; post-send failure returns 502 without replay; loop guard prevents re-forwarding.
- [ ] Regression tests extend existing middleware/router test files; all validation runs in GitHub Actions only.
- [ ] Deployment notes (env vars per node) are given in the task report, not in a new `docs/` file.

## Out of scope

Centralized relay admission/settlement for `/v1` (separate, billing-gated phase). DNS/Cloudflare changes. Schema changes.
