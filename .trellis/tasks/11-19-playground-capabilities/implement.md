# Execution, ownership and per-item commits

Implementation approved after final review. User subsequently removed server verification; execute R25 through verified GitHub CI/tag/release/image publication. Do not wait for server connection details or claim an observed server rollout. R3 may precede R2; the later selector mode change must retain the shared sort contract.

Current state: product implementation and independent review are complete; exact-commit GitHub CI 44739249885 passed at a9b37b493. The approved main publication completed as custom-20261120-a9b37b4, verified by workflow 44742578933 and artifact checks. The user authorized commits, push, tags and CI/CD, and later removed server verification; no server connection details are required.

## Four existing pi lanes

| Pane | Primary ownership | Review counterpart |
| --- | --- | --- |
| w4C:p9 | Relay admission/counting/queue, failover/stream handling, conversion logs; later goal policy integration | pK reviews protocols and accounting |
| w4C:pJ | Channel cards/details/endpoints UI, playground navigation, sidebar and access cards; users sort UI | pC reviews permissions/UI contracts |
| w4C:pC | Key lifecycle, CORS, LinuxDO avatar, security checks; frontend locale changes for each individual item | pJ reviews flow; p9 reviews server paths |
| w4C:pK | Endpoint resolver/converters, playground search integration, price sync performance, official status/fingerprint SDK | p9 reviews server paths; pJ reviews UI |

These are sequential lanes, not permission to touch the same files simultaneously. Root assigns a concrete file lease per item and serializes shared controller/relay.go, router/api-router.go, model/channel.go, dto/channel_settings.go, channel form/drawer, sidebar and locale edits. Price sync may use pC after auth tasks; swapping ownership must be explicit. Native research workers only provide read-only findings.

## Child and commit map

| Requirement | Owning child | Planned commit subject | Dependencies |
| --- | --- | --- | --- |
| R1 | 11-19-playground-web-search | feat(playground): enable hosted web search and sources | R4 cross-protocol tests |
| R2 | 11-19-playground-model-navigation | feat(playground): switch group and channel navigation | existing pin security |
| R3 | 11-19-playground-model-navigation | fix(playground): sort models naturally by name | integrate selector sequentially after R2 |
| R4 | 11-19-playground-responses | feat(channels): configure supported upstream endpoints | shared DTO contract |
| R5 | 11-20-conversion-logs | fix(logs): retain request conversion metadata on failures | verify R4 integration |
| R6 | 11-20-channel-concurrency | feat(channels): show shared live concurrency metrics | one lease lifecycle |
| R7 | 11-20-channel-failover | fix(relay): fail over on early upstream concurrency errors | R4 compatible streams |
| R8 | 11-20-channel-wait-queue | feat(relay): queue requests while eligible channels are busy | R6 lifecycle, R7 early errors |
| R9 | 11-20-channel-card-usability | fix(channels): keep priority Enter from opening details | independent |
| R10 | 11-20-channel-card-usability | fix(channels): restore compact paired card metrics | R6 slot; R9 tests preserved |
| R11 | 11-20-channel-card-usability | feat(channels): show group model access in details | sequential card edits after R10 |
| R12 | 11-20-token-count-display | feat(ui): show exact and compact token counts | shared formatter contract |
| R13 | 11-20-price-sync-performance | perf(pricing): batch sync preparation and page previews | existing atomic pricing API |
| R14 | 11-20-user-usage-sort | feat(users): sort by a separate used balance column | R12 tokens mode, DB matrix |
| R15 | 11-20-console-navigation | feat(nav): group all four logs in a collapsible menu | preserve role/module gates |
| R16 | 11-20-console-navigation | feat(clients): add access configuration cards | sequential sidebar after R15 |
| R17 | 11-20-channel-goal-policy | feat(channels): filter recognized Codex goal turns | R4 settings, R8 eligibility |
| R18 | 11-20-api-key-lifecycle | feat(keys): regenerate existing API credentials | security/DB checks |
| R19 | 11-20-api-key-lifecycle | feat(keys): accept validated custom API secrets | R18 atomic mutation/revocation |
| R20 | 11-20-cors-allowlist | fix(cors): enforce explicit allowed origins | deployment site origin |
| R21 | 11-20-official-service-status | feat(status): show official provider status | R15-R16 shared sidebar handoff |
| R22 | 11-20-channel-fingerprint | feat(channels): run local model fingerprint checks | R4,R6,R8,R21 |
| R23 | 11-20-linuxdo-login-avatar | fix(oauth): synchronize LinuxDO avatar on login | verified provider claim, auth/DB |
| R24 | 11-20-console-navigation | feat(nav): group invitation and redemption codes | sequential sidebar edits |
| R25 | 11-20-verified-release | chore(release): record verified build and deployment | every product requirement checked |

R25 only commits real release/workflow/verification changes as needed; do not create an empty commit or hand-edit generated versions solely to tick a box. A planning-only commit is separate from all product items.

## Ordered work

1. Finish research and requirement convergence; record final review/defaults and obtain the required final-plan response. Preserve current baseline diffs and other active tasks.
2. Load trellis-before-dev/specs in every lane; shadcn-ui and React skills for frontend; i18n skill before any new text/locale edits; full billing rules for affected pricing/usage; current OWASP for keys/CORS/avatar/authorization.
3. Implement small independent fixes first (R9, R13, R20) while the shared endpoint/queue contracts are finalized. Activate each owning child only under the approved parent plan. No worker touches the Git index.
4. Land R4 settings/resolver, R6 accounting/metrics, R7 stream failure contracts, then R8 queue. Native and converted stream scenarios must agree on the response state and billing lifecycle.
5. Complete playground features and all UI/navigation items with per-item locale handoff/commit; do not accumulate unrelated changes in one file before committing the earlier requirement.
6. Implement and verify key/goal/avatar/status/fingerprint work under their own scopes. Confirm the LinuxDO upstream claim and goal detection limits before shipping labels.
7. Each pi runs relevant checks and returns changed paths, actual commands/results, known limits and commit-ready diff. Another lane reviews, then root inspects the complete cross-feature behavior and committed-tree consistency.
8. Root commits only each item's owned hunks. Verify diff of the resulting commit and record SHA in report.md. Any pre-existing build/workflow dependency is separately resolved by its owning task; never silently stage it with a feature.
9. Run integrated release checks on the exact final committed source. Push integration branch, wait for exact-SHA CI, then fast-forward fork/main and watch complete release workflow. Resume failed pi turns with continue after checking state; do not restart completed tasks.
10. Verify tag/assets/image digest, then actual target server image/version/health. Update report and journal; archive only truly completed tasks. Commit post-release bookkeeping on merge/upstream and push only that integration branch, not main; keep the deployed product SHA distinct from the report SHA so bookkeeping does not trigger another release. Missing tests or unknown server state remain explicitly incomplete.

## Verification

- Frontend: locate existing Bun tooling; bun run test -- <affected existing suites>, bun run typecheck, changed-file lint/format, i18n checks, bun run build. For final release use the workflow's frozen lockfile install/full suites. Browser checks include desktop/mobile/themes, keyboard/focus, large price previews and live-metric polling.
- Backend: gofmt, focused deterministic tests extended in existing appropriate files, root go test ./..., go vet ./..., go build ./... for final release. Capture resource/tooling blockers rather than claiming a pass.
- relaykit/tokenkit: GOWORK=off go build ./... inside every affected independent module; release contract checks both modules.
- Real infrastructure: SQLite/MySQL/PostgreSQL for changed query/persistence behavior (keys, sorting, settings, avatar), schema upgrade/restart idempotency if schema changes; real Redis parity for counts, cancellation, queue wakeup and lease expiry. Record exact engine versions.
- Security: ownership/role/CSRF/re-authentication, invalid/duplicate credentials, old-key revocation under cache and races, permitted/denied/null CORS origins, avatar URL handling; no secret audit payloads.
- Relay: HTTP429, early 200 error, premature EOF, silent/heartbeat-only upstream, no duplicate partial answers, client cancel, bounded waiting/full queues, eligible candidate changes, pinned/strict behavior, detached lease ownership and actual usage/refund.
- Pricing: mixed multi-model payload, exact expressions/zero/provider overrides/frozen exchange rates, all-page selection, duplicate confirmation prevention, stale-version errors, canceled/stale preparation jobs. Profile 1,000/5,000 models without timing assertions in unit tests.
- Release: exact SHA/tag/assets/GHCR manifest and server application version, not merely HTTP success or push output.

## Known planning prerequisites

Server SSH/connector target, deployment directory and site's public origin are requested. Tool shell currently lacks Bun, gh and docker on PATH; resolve existing tool locations/hosted checks without silently skipping gates. LinuxDO avatar source and official feed contracts require concrete source fixtures. Full goal activity cannot be inferred from an unmarked older/manual request; approved behavior must describe recognized goal-triggered turns.

No product tests have run yet. Research and task-manifest validation are not implementation verification.
