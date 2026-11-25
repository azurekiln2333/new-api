# Four existing Pi workers, 2026-11-24
User explicitly authorized immediate four-Pi implementation and root commit/push/tag/CI-CD after hosted checks. This new assignment replaces all archived task ownership.
NO LOCAL VALIDATION OF ANY KIND. All tests, builds, vet, lint, typechecks, gofmt checks, DB probes and benchmarks run ONLY IN GITHUB ACTIONS.
| Worker | Herdr pane (verified cwd new-api) | Child |
|---|---|---|
| pi-keys | w4C:p9 | .trellis/tasks/11-24-channel-loading-save-performance |
| pi-billing | w4C:pJ | .trellis/tasks/11-24-management-mutations-code-batches |
| pi-ui | w4C:p12 | .trellis/tasks/11-24-channel-diagnostics-log-presentation |
| pi-logs | w4C:p11 | .trellis/tasks/11-24-dashboard-node-ranking-details |
Root session environment has stale Herdr pane values. Use ONLY these explicitly discovered new-api pane IDs; never --current or focused pane to select peers.
Read parent design Ownership plus assigned child design. No staging/committing/pushing or detached workers. Communicate through existing Herdr agent prompt/read, coordinate shared API/schema changes with owner.
Root owns branch/CI/spec/integration. Workers may write report.md and research notes in OWN child only. Source edits must be saved coherently. Acknowledgement must explicitly confirm NO LOCAL TESTS OR COMPILATION.

## 2026-11-25 update (root: Claude, pane w4C:p17)
Old codex root pane w4C:p16 is gone. Four more Pi workers were added; the original four run gpt-6.1-sol and the new four run deepseek-v4.1-flash (user instruction — after `/new`, restore the model before handoff).
| Worker | Pane | Model | Assignment |
|---|---|---|---|
| pi-billing | w4C:pJ | gpt-6.1-sol | Lead: console forwarding core (child 11-25-console-forward-to-master, implement.md steps 2-3) |
| pi-fwd-auth | w4C:p19 | deepseek-v4.1-flash | Forwarding auth integration: middleware/auth_origin.go, service/passkey/service.go + their tests |
| pi-fwd-sync | w4C:p1A | deepseek-v4.1-flash | Channel-disable socket close verification, master-process-local route list |
| pi-fwd-test | w4C:p1B | deepseek-v4.1-flash | Forwarding regression tests in middleware/trusted_proxies_test.go |
| pi-review | w4C:p1C | deepseek-v4.1-flash | Pre-CI static review of the whole branch diff + last CI failures |
| pi-keys | w4C:p9 | gpt-6.1-sol | R14 subscription balance-redemption column (ManageMultiKeys done) |
| pi-logs | w4C:p11 | gpt-6.1-sol | R16 settings section batch save |
| pi-ui | w4C:p12 | gpt-6.1-sol | R15 stale edit drawer, then R13 inline quota edit |
Release order: forwarding first (user priority), together with whatever else is frozen and green at that point.
| pi-newapi-ci | w4C:p1D | user-chosen | Owns .github/workflows/ci.yml selector wiring + post-push CI failure triage |
| pi-drawers | w4C:p1E | user-chosen | Finish R15 (+ sibling drawers) and R13 |
| pi-web-review | w4C:p1F | user-chosen | Static pre-CI review of web/ diff |
| pi-commit-plan | w4C:p1G | user-chosen | Commit grouping plan in research/commit-plan.md |
## Frozen integration ownership — 2026-11-25 snapshot

This supersedes the prior p2/p4/p6 assignments above for final integration and clean-worktree handoff: **root alone owns the Git index and all staging/commits/pushes; p2 owns formatting follow-up; p4 owns CI workflow/failure wiring; p6 owns frontend failure triage.** Do not stage, commit, push or alter another owner's source. Historical worker reports remain as authored; the parent integration report records the later CI snapshot. The moved generated artifacts remain outside the repository with `move-manifest.json`; the incident report remains preserved and unresolved. No further SSO/Passkey investigation is part of this handoff.

