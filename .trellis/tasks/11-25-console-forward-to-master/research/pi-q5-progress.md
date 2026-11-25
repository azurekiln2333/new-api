# pi-q5 checkpoint — forwarding regression tests

## Step 1 — orientation (2026-11-25)
- Assigned/claimed queue item #5 under existing in-progress parent `.trellis/tasks/11-24-console-responsiveness-diagnostics`; no new task is needed.
- Read root AGENTS.md, parent queue.md/prd.md, and existing research/fwd-test.md. Existing test additions are 811 lines in `middleware/trusted_proxies_test.go`; inspect and extend rather than replace.
- Asked core owner pi-fwd (w5D:p2) through herdr for final function/type names and behavior deltas before assertions against in-progress core.
- Boundary: only forwarding regression cases in existing `middleware/trusted_proxies_test.go`, corresponding selector/coverage handoff in research/fwd-test.md, this progress file, and queue item status. No forwarding/auth production changes; no capacity files; preserve all other workers' changes.
- Validation constraint: no local tests/build/vet/lint/gofmt/format/typecheck/runtime validation or DB probes; only read-only source/diff inspection. GitHub Actions must execute tests.
- Next: read Trellis backend guidelines and forwarding child artifacts, inspect existing tests/core and diff; then fill concrete missing cases after coordination.

## Step 2 — source review / coordinated contract
- Read child PRD/design/implement.md, relevant backend/guides, existing test diff and forwarding core/config/request-ID/body-limit source. Existing task artifacts already authorize implementation; `task.py current` has no pointer but no activation/task creation is needed for this delegated existing item.
- Core owner confirms retained APIs/helpers and exact method-specific 503 fallback set; process-local routes must reach a healthy master. Core is fixing map lookup, raw query, wire content-length/no-replay framing, original body lifetime, private processing response header, and mid-stream abort handling.
- Concrete existing test defects: fixture CIDR 10.100.0.0/24 excludes signed socket peer 10.100.3.7; replay test resubmits request already mutated/stripped by ingress; GET reuse test never warms the connection; recorder-based SSE checks may observe header-only initial flush/coalesced events.
- Planned tests stay in existing file: fix these fixtures; expand HMAC tamper/proxy trust and path/scope fidelity; prove real pooled reuse/no replay; real-client gated SSE, truncation, cancellation; streaming multipart/master 413; fallback body unread/open; truthful request/provenance/header stripping.
- CI owner pi-web (w5D:p6) notified: one middleware file, prefix selector unchanged, no external env/databases/dialects required. No tests/formatters or runtime validation executed.
- Read OWASP Authentication, Session Management, CSRF guidance and ASVS 5.0.0 official release for test controls (transport provenance is not auth; credential/origin binding, exact trust boundary, anti-replay, unchanged cookies, no secret logs). No authentication implementation changes or application-compliance claim.

## Step 3 — first targeted test changes saved
- Fixed signed-peer CIDR, deterministic TRUSTED_PROXIES env, ingress router trust baseline, and pristine-envelope replay clone.
- Added signed proxy-alias stripping/peer provenance checks and table-driven method/path/query/credential/origin/content-header/metadata tampering rejection.
- Expanded healthy-master route scope to fingerprint poll/cancel, stream, unknown API descendants, local SPA OAuth callbacks and playground selector.
- Replaced racy flush-count assumption with real-client gated SSE (master cannot emit event two until client has read event one).
- Replaced misleading reuse test with successful warm-up, GotConn.Reused assertion and same-peer proof for POST/GET/DELETE post-send failures; next source review will add explicit mutex for peer capture.
- Core owner confirms final proof signing occurs after hop-header cleanup, raw queries/trailers preserved, no-replay empty bodies signed with wire length -1, no-fallback after any obtained connection, sentinel-only stream abort recovery. Tests target these contracts next.
- No local validation or formatter execution; user received concise in-progress reply when asked.

## Step 4 — missing transport/trust cases saved
- Added real-client truncated SSE with outer PanicRecover and no-Hijacker writer regression, disconnect/cancellation propagation, deferred-producer 2MiB multipart stream, master AnonymousRequestBodyLimit 413, HTTP failure/redirect fidelity, XFP ingress boundary, valid-unlisted-worker loop guard vs spoofed/bad proof.
- Fresh-dial fallback now checks tracked original body identity, zero prior reads and no close. Healthy master scope and method-specific 503 vs shared/fresh-job fallback cases included.
- Reuse cases now cover nonempty POST plus empty POST/GET/DELETE with synchronized peer capture. Fidelity checks cover escaped path, raw semicolon/bad escape query, host/origin/referer/fetch/content headers, hop-header stripping and request/response trailers.
- Request-ID test verifies an actual wire envelope before echoing signed ID; private processing response header stripped. Added truthful us01/unknown logs and secret/nonce/proof redaction, proof recomputation after Connection-nominated credential removal, master-never-forwards even with programmatic target.
- No production source changes; no CI/locale/script files touched. Next: source-only full readback and focused test-lifetime/race review; resolve report coverage and mark item DONE, notify root/core/CI.

## Step 5 — source-only readback and handoff
- Read back complete test file in bounded ranges; checked imports, fixture cleanup, channel synchronization, private API call signatures and matching final core. Synchronized peer capture and log buffer; removed obsolete flush-count state. Added non-sentinel panic propagation and post-send header-timeout regressions; explicit empty-wire proof checks require received length -1 and zero bytes.
- Added final core deltas: GET-only health exception (POST forwards), log-export scopes and target dangling separator/escaped-root/short-padded-secret validation.
- Source reviewer pi-rev-go (w5D:pD) asked for a focused read-only review; no executable validation run. Hosted test/race/formatter results remain pending. CI owner confirms every test prefix is already selected.
- Updated research/fwd-test.md with final exact coverage, selector and fixtures (no added env/DB/Redis/dialect; `-p 1 -count=1`, no t.Parallel) and security references/remaining auth-ceremony owner boundary.
- Exact-edit misses failed closed; affected text read back before retry; no partial writes or broad file replacements.
- No protected identity, locales/script, core production, CI or capacity edits. No write git operations. Parent spec/commit/publication are root-owned; test-fixture lessons captured in fwd-test.md rather than racing shared specs.

## Step 6 — FINAL source handoff
- Found and corrected a shared Gin/httptest fixture hazard by reading installed Gin response_writer.go: ReverseProxy may invoke Gin CloseNotify when a request has no cancellable context, but httptest.ResponseRecorder does not implement CloseNotifier. `consolePublicRequest(t, ...)` now uses `t.Context()` consistently; the no-Hijacker case has its own cancellable context. This avoids an unrelated panic obscuring every transport assertion. All call sites source-searched after signature change.
- Log fake master now drains body; multipart producer shutdown is bounded and pipe reader closes on request completion. No local execution.
- Full-wire empty proof case checks actual received ContentLength=-1 and zero payload bytes. Current quoted provenance logs/private response header semantics align with core. No pending API changes from pi-fwd.
- Public OWASP Authentication, Session Management and CSRF sources plus stable ASVS download page re-opened; stable release 5.0.0 confirmed. Scoped controls/references and unverified auth-ceremony boundary recorded in fwd-test.md.
- FINAL CI handoff sent to pi-web, acknowledged: same six prefixes, no new external env/DB/Redis/dialect setup, no parallel global fixtures, -p 1 -count=1 plus hosted race. Hosted tests/race/formatters remain NOT EXECUTED here and PENDING.
- Complete owned outputs: `middleware/trusted_proxies_test.go`, `research/fwd-test.md`, this checkpoint, parent queue item #5. Next OPEN work is reserved/claimed by other workers; do not take #10 from pi-web.
