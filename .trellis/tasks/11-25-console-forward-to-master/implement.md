# Console forwarding implementation / resume checkpoint

## Authority and constraints

Active task: `.trellis/tasks/11-25-console-forward-to-master` (parent `.trellis/tasks/11-24-console-responsiveness-diagnostics`); current worker **pi-fwd** in w5D:p2, integrator Claude w5D:p1. Resumed from pi-billing's progress on 2026-11-25. User explicitly requested implementation of this existing task; no new task is created. Binding authority: design section 11 overrides earlier design proposals (especially pooled connections, fallback and secure-cookie compatibility).

NO local tests, builds, vet, lint, typecheck, gofmt, format checks or database/network probes. Validation runs only in GitHub Actions. No staging, commits or pushes. Preserve other workers' dirty edits. Checkpoint this file after every completed step.

## Change boundary and ownership

Gap: worker `/api` processing incurs serial remote datastore operations. Install a no-datastore authenticated ingress verifier, request-ID propagation and streaming HTTP forwarder before existing API middleware; master executes unchanged handlers and authorization. No relay/body parsing, pricing, usage, database/schema or plugin-runtime changes.

Owned files per design section 11 item 11:
- `common/console_forward.go` (new): startup configuration, typed verified request provenance/effective scheme.
- `middleware/console_forward.go` (new): exact scope, signed ingress trust, replay guard, pooled HTTP/1.1 transport, no-replay/fallback/streaming and safe provenance logs.
- `main.go`: only forwarding setup/mount lines, plus scoped HTTP idle-timeout if needed for the reviewed pool contract.
- `middleware/request-id.go`: honor only verified signed request ID.
- `middleware/auth_origin.go`, `service/passkey/service.go`: reassigned by root to **pi-fwd-auth**; this worker must not edit them.
- Existing tests `middleware/trusted_proxies_test.go` and `middleware/auth_origin_test.go`: reassigned by root to **pi-fwd-test**; this worker must not edit tests.
- `research/sync-and-local-routes.md`: **pi-fwd-sync**, read-only handoff for core route policy and report.
- Task artifacts: this file, `report.md`, task context manifests/status as required for activation.

DO NOT edit `router/api-router.go`, `.github/workflows/ci.yml`, other worker files, or shared auth/session/permission behavior.

## Ordered execution checklist

- [x] 1. Read full PRD/design including binding section 11, AGENTS, relevant backend/guides, owned-file baseline diff and OWASP Authentication/Session/CSRF/OAuth/MFA/Passkey guidance. Write this ordered plan and ownership/rollback before code.
  - Resume baseline: `implement.md` did not exist; no baseline diff in owned existing Go files. Task directory was untracked. Source confirms cross-node socket close: `main.go:109`, `pkg/wsmanager/wsmanager.go:105-110,130-147`, `service/ws_close.go:5-12`. Fingerprint jobs live in process memory (`service/channel_fingerprint.go:146-150,256-276`); plugin login flow state is DB-backed (`service/provider_plugin_login.go:330-354`); stream handler is `POST /api/channel/ollama/pull/stream` (`router/channel-router.go:115`, `controller/channel.go:3263+`).
  - Security controls: signed peer+HMAC metadata is provenance, never user authentication; exact public origins; no Origin synthesis; existing cookie/state/PKCE/challenge/session/passkey guards stay authoritative; bounded nonce replay window; no credential/proof logging; encrypted WireGuard deployment boundary. Read OWASP Authentication, Session Management, CSRF Prevention, OAuth2, MFA and Passkey cheat sheets and ASVS 5.0.0 V3/V6/V7/V10/V12 complete chapter sources. Applicable ASVS references include V3.3.1-3, V3.5.1-3, V6.3.1/4, V6.5.1/3/5, V7.2.1-4, V7.4.1-2, V10.1.2, V10.2.1/2, V10.4.1-6, V10.5.1, V12.3.1-5. These are an implementation checklist, not a whole-application compliance claim.
- [x] 2. Implement configuration, typed provenance and startup validation. Root activated the existing task; do not run task.py start again. `common/console_forward.go` and `common/init.go` now parse config before datastore startup. Fixed private-IP targets support hk01's TCP stream hop; ingress TRUSTED_PROXIES and signed-envelope peers remain separate.
- [x] 3. Implement unified verifier -> RequestId -> pooled forwarder and mount before datastore middleware. Core fixes saved: healthy process-local requests now forward; exact method/path 503 set applies only to dial fallback; streaming lookup compiles; request raw query/escaped path/trailers survive ReverseProxy; original body remains open on dial fallback; no-replay single-use body guards both stdlib retry predicates; logs learn actual processing node from a stripped private response header (never label worker as master). Root's extra decision implemented inside the forwarder: catch only ErrAbortHandler, unwrap Gin writer to avoid HTTP/2 Hijack panic, close HTTP/1 client or abort without JSON. Other panics rethrow; recovery chain unchanged. Final source review/config/i18n/report remains below.
- [x] 4. Integration source review: existing auth-helper changes in auth_origin.go, service/passkey/service.go, oauth/linuxdo.go and controller/video_proxy.go all consult verified public scheme/host; raw-header trust remains only in pre-existing direct passkey behavior (recorded risk, not silently changed). No edits to helper-owned files. pi-fwd-sync confirms existing Redis socket-close propagation; exact B1/B3 fallback policy applied.
- [x] 5. Test handoff: **pi-q5** owns middleware/trusted_proxies_test.go under parent queue #5. Sent unchanged public APIs, final method-specific fallback and streaming semantics, actual empty-body framing, pooled-reuse fixture fixes, pristine replay clone, truncation/hijack behavior, trailer/header proof fidelity and no-local-validation rules. Existing auth-origin/passkey tests remain helper-owned. **pi-web** owns CI #10 and has all selectors. **pi-auth** inserted both forwarding error keys in seven locales/static keys through its exclusive script workflow; this worker made no locale/script changes.
- [x] 6. Source-only review and handoff report written at `report.md`: full changed-file boundaries, failure/trust/framing contracts, source-derived zero worker request-path SQL/Redis count, exact hosted selectors, five-node env examples, hk01 TCP stream, auth/OWASP references, sync/socket evidence and explicit unrun gates. Corrected draft runbook's NODE_NAME, ingress-peer semantics, spoofed-marker behavior and all-CONSOLE_FORWARD_* rollback. Independent source review requested from pi-rev-go; final test implementation remains queue #5 (pi-q5). No local validation or publication.

## Rollback

1. Runtime rollback: unset all `CONSOLE_FORWARD_*` variables on every node and restart; retain current NODE_TYPE/NODE_NAME, ingress TRUSTED_PROXIES and auth secrets. Return to existing local console execution; relay is never forwarded.
2. Roll back workers before removing master verifier/secret. Never leave a worker sending signed requests to a master with ingress verification disabled (wrong IP/origin semantics).
3. Remove hk01 stream listener only after hk02/hk03 forwarding is disabled. Restore prior firewall configuration through deployment change control.
4. Source rollback is only this worker's precise diff/new files and task artifacts, coordinated by root. Never restore entire shared files or revert other workers' changes.
5. No DB migration, data rollback or cache format change. Existing sync/publication path remains authoritative.

## Latest checkpoint

Steps 3–6 complete as **source handoff only**, report.md authoritative; queue #1 DONE. Independent pi-rev-go source review reported no concrete compile/type/race/lifetime blocker, only inherited alignment; its manual header/common edits retained and short route-literal suggestions applied. Late pi-rev-go correction: removed one space on each shorter process-local-table row (9/11 after colon), longest row unchanged; streaming table unchanged. Pure targeted text edit, not a formatter run. No compiler/formatter/race result is claimed. pi-q5 finalized queue #5 DONE in research/fwd-test.md: full-wire empty GET proof + chunked/-1 semantics, real pooled warm-up/truncation cases, all public fixtures use t.Context() to avoid unsupported Gin CloseNotify recorder fallback, fake-master body drain fixed. Source authored only; hosted test/race/formatter all PENDING. No test/core edits by pi-fwd for this confirmation. pi-auth seven-locale/static-key confirmation rechecked by reading all seven entries. Root/CI/patch owner received final report/selectors/boundaries. Queue re-read: every other unfinished item is claimed by a peer; nothing OPEN to take. No local validation, DB probes, publication or forbidden git changes. All release/hosted/live auth gates remain explicit in report.md.
