# Implementation and verification plan

Task is in progress after final review approval. User has authorized Conventional Commit, push, tag and CI/CD after verified implementation.

## Ordered work

1. [x] Finish research and cross-check public fields/timeout semantics in PRD/design. User approved the final integrated scope on 2026-11-16; `task.py start` changed planning to in_progress.
2. [x] Freeze shared DTO and service admission/timeout interfaces. Dispatched Pi settings/UI, timeout and capacity/lifecycle workers with exclusive ownership. Default Google credentials failed before tool activity; restarted using the previously verified configured Pi newapi/deepseek-v4.1-flash route and observed real file-read/tool calls from all three.
3. [x] Settings worker: add optional channel cap/first-byte timeout, global request-policy option, safe validation and persistence, default retry budget proposal, exact legacy-default retry-range upgrade if accepted. Preserve explicit zero and unknown existing settings fields according to current contracts.
4. [x] UI worker: channel fields and inherit/custom selector, global timeout and retry-zero explanation, serializer/defaults/loading/configuration state/errors, seven locales, focused form regressions. Reuse existing shared UI and sensitive-write fieldset.
5. [x] Relay worker: implement local/Redis admission and idempotent lifecycle, request-scoped capacity exclusions, alternate selection, bounded first-byte wait with actual upstream context, ping boundary, 504/524 eligibility, upstream 403/404 fixtures and explicit stop boundaries. Cover ordinary HTTP, SDK paths, channel tests, and submission boundaries named in the design; report any uncovered path before claiming channel-wide enforcement.
6. [x] Main integrates only after workers report their files and checks; inspect the diff for billing/accounting, auth-origin, cancellation and shared-cache invariants. Read required billing guide in full before reviewing billing-adjacent exits. No refactor of billing formulas or authentication behavior.
7. [x] Independent Trellis check worker reviews implementation against artifacts and verifies deterministic failure/success cases. Mechanical fixes stay within task scope; rerun only affected checks.
8. [x] Record actual validation results and database/Redis versions. Resolve all required blockers before release. Update the relevant local Trellis spec with the attempt lifetime and retry boundary.
9. [x] Main reviews status, stages only owned product changes, creates Conventional Commit(s), checks `fork/main` ancestry and pushes the approved commit without rewriting history. Follow `docker-fork.yml` through completion and verify its generated `custom-YYYYMMDD-sha` tag, Release assets and image publication. Archive/journal only after all acceptance criteria are complete.

## Test matrix

| Area | Observable cases | Existing location |
|---|---|---|
| Whole-channel cap | Two active requests, third routed to B, all busy 429; multiple keys share count; cancellation/error/close frees capacity; cache reload does not reset count | New admission-service test file (only new Go test file), relevant existing controller fixtures |
| Redis | Atomic admission across independent clients, owner-specific release, renewal/expiry/fail-closed behavior | Same admission test file with isolated real Redis |
| Timeout | Global inheritance/custom/explicit0, no headers, headers-only body stall, preserved first byte, long productive stream, cancellation, no empty success settlement, AWS SDK does not internally repeat the typed timeout | `controller/relay_stream_test.go`, existing outbound request and AWS tests |
| Failover | Structured/plain 403/404, 504/524, A->B, no-repeat exhaustion when enabled; zero budget, strict, pin, local auth and started output stop | `service/relay_error_test.go`, `controller/channel_pin_retry_test.go`, existing status range tests |
| Persistence | Cap/timeout nil/zero/positive round trip and options restart on all three real DBs; exact legacy default vs custom policy | `model/channel_settings_test.go`, existing request-policy tests |
| Frontend | Save/load, inheritance display, field validation, configured status, permission lock, zero retry explanation | Existing channel configuration and Request Policies test files |

## Validation commands and evidence

Executed results are recorded in `verification.md`, worker reports and named test logs. External database/Redis/race acceptance remains gated by the release CI until actual results are recorded.

- `gofmt` on modified Go files, `git diff --check`.
- Root focused packages: `go test ./service ./controller ./model ./setting/operation_setting ./relay/channel ./relay/helper` with narrowed test patterns during iteration; run relevant complete packages for final check.
- Redis tests run with an explicitly configured isolated Redis instance; process-local tests must not stand in for Redis checks.
- Database matrix uses `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN` and the existing SQLite fixtures. Never print credential values. Record exact engine versions from test results and treat skipped dialects as blockers.
- Root `go build ./...`; inside `relaykit/`, `GOWORK=off go build ./...` using that module's declared Go compatibility.
- From `web/`: `bun run test` with the affected channel/settings test paths; `bun run lint`; `bun run typecheck`; `bun run i18n:sync`; required format/header checks on changed files; `bun run build`.
- Read and use the actual release workflow triggers and required branch/tag convention immediately before pushing. Use an observed GitHub run ID to track success; do not infer CI completion from a successful git push.

## Review/rollback points

Work commit `60da6a51296038ed9ad47d60d191daf078423123` (`feat(channel): add concurrency limits and upstream timeout failover`) was safely fast-forward pushed to `fork/main` on 2026-11-16. Release workflow run `43871023999` is in progress; generated tag and publication remain pending until observed. Local final quality gate is recorded in `final-check.md`.

First run failed only because the controller's PostgreSQL fixture requires a loopback URL, while the initial workflow used keyword DSN syntax. Follow-up `673c2b8f8a7c53bb2049a9b728275dff47277f7c` (`fix(ci): use loopback PostgreSQL URL for relay policy tests`) was pushed. In run `43879108713`, all backend/frontend/real-database/Redis/race checks passed. AMD64 and ARM64 image builds passed; multi-platform publication and downloads are being followed to completion. See `verification.md` for exact version and command evidence.

- Before activation: final scope review, all manifests contain real paths, no user-owned blocker remains.
- Before integration: no overlapping worker ownership, no untracked unrelated edits staged.
- Before release: all acceptance checks passed; exact persisted policy semantics and SDK/WebSocket coverage reviewed; no skipped database/Redis requirement hidden.
- Revert the feature commit or deploy the prior tag if necessary. Existing settings are stored as optional JSON/option values, with no schema migration or destructive conversion.
