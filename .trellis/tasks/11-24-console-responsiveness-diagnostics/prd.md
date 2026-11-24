# Console responsiveness and diagnostics
## Goal and authorization
Make channel reads and management writes responsive, unify batch-code operations, and expose useful diagnostic/usage information in existing console surfaces.
The user explicitly authorized task planning, immediate implementation by FOUR existing Pi workers, review, commit, push, tag and CI/CD on 2026-11-24. No additional planning approval is required. No direct production deployment.
**NO LOCAL TESTS, BUILDS, VET, LINT, TYPECHECKS, GOFMT CHECKS, DATABASE PROBES, BENCHMARKS OR EXECUTABLE VALIDATION. All validation runs only in GitHub Actions.**
## Requirements
| ID | Observable requirement | Owner |
|---|---|---|
| R1 | Refresh/open channel management and fingerprint channel dropdown without full-table/per-row query amplification; correct SQL pagination/filter totals, authoritative allocation state and cancellation | pi-keys |
| R2 | Management create/save must not appear frozen. Persisted success closes dialog and updates visible state promptly; background propagation/refetch does not block it. Pending/error/unknown outcomes are visible; retry cannot silently duplicate code batches. Preserve durable audit and immediate local routing correctness | pi-billing + pi-keys |
| R3 | Invitation and registration/redemption code management share selection/export/action patterns: entire newly created batch downloadable/copyable, historical batch recoverable, current-page and cross-page filtered selection/export supported, row actions in one menu; preserve distinct business semantics | pi-billing |
| R4 | Channel name/ID retain two lines; concurrency 0/limit plus icons next; used Tokens, remaining/total, response and last-test follow; priority/weight last. Configured local budget occupies existing quota area, not a separate row. Last-test uses relative time | pi-ui |
| R5 | Fingerprint shows three prompt/output cards below controls, actual prompts and actual replies, live queued/running/complete/error states; preserve score/cancel/limits, admin-only access, do not launch extra paid probes | pi-ui |
| R6 | Common log columns: time, user, model, stream, Token usage, duration, cost, channel, processing node; remaining fields afterward | pi-ui |
| R7 | Public homepage signed-in avatar navigates directly to console; console avatar opens menu. Hide overview onboarding guide for admins, including entry/reminder; preserve initial installer | pi-logs |
| R8 | Existing overview API cards are wider, order configurable, bound to instance through existing system settings; one instance supports multiple API domains. Everyone sees CPU/memory/storage in original cards. Clicking API item opens filtered node details, with unknown/stale states | pi-logs |
| R9 | LLM model ranking click opens model-square-style side drawer instead of price navigation, with model usage and per-model user usage ranking. Admin sees full names; ordinary user only own full name, others masked SERVER-SIDE | pi-logs |
| R10 | Group levels increase numerically (0 < 1 < 2 < 3 < 4); preview matches runtime permissions. Preserve global/manual grants, apply explicit hidden rules after level inheritance, and keep the manual editor synchronized with loaded settings without deleting existing rules | group_visibility_repair |
## Constraints and acceptance
- Reuse existing business components, request APIs, quota formatters and node collectors. Seven frontend locales; do not change billing math or fabricate upstream totals/metrics.
- Original admin-only channels/fingerprint boundaries remain. Resource public projection excludes hostname/private addresses/process secrets/sync errors. Rankings never leak real others' names/identifiers in raw payload/cache.
- Durable writes and audit cannot be replaced by a fire-and-forget goroutine or fake success. Credential revocation/permission state must be immediately correct on saving node.
- Data behavior changes require GitHub-hosted SQLite/MySQL/PostgreSQL matrix; migration changes require fresh + released upgrade twice. Log-store changes additionally exercise ClickHouse.
- Extend meaningful existing tests; all execution cloud-only. No reported latency numbers without measured evidence.
- Unrelated workspace edits remain untouched; workers never stage/commit/push. Root reviews, checks cloud, commits and publishes exact tested SHA using existing fork workflow.
## Exclusions
No production deploy, provider billing/pricing changes, global security weakening or broad cache bypass. API domain binding is display association, not a traffic-routing change.
