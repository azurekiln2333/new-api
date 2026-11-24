# Console responsiveness and diagnostics — integration report

Status: implementation integrated on `fix/console-responsiveness-20261124`; GitHub Actions validation, commit, push, merge, and release workflow are pending. No local executable validation has been run, by instruction.

## Delivered scope

- **R1 — channel loading:** bounded, paginated channel search/options; SQL-backed tag filtering and counts; batched key-entry projection; guarded cache publication; atomic channel create/update publication and audit enqueue. Channel names and sensitive channel data remain behind existing admin permissions.
- **R2/R3 — management mutations and code batches:** durable audit outbox with retry leases; atomic invitation/redemption batch creation with retry identity; actor-bound recovery and history/export APIs; shared frontend export flow for current selection, filtered results, and historical batches. Redemption claim-limit batch identity remains separate from operation retry identity.
- **R4/R5/R6 — channel diagnostics and logs:** channel cards show local budget in the quota region and the requested metric order with relative last-test time; fingerprint results display three prompt/output cards while retaining the admin gate and cancellation; common log columns use the requested priority order. All seven locale files were updated.
- **R7/R8/R9 — dashboard and rankings:** safe instance-resource summary for authenticated users; API entries can bind to one or more configured domains and retain stable IDs; homepage avatar navigates to the console and admin onboarding is hidden; model ranking opens a usage drawer with viewer-specific server-side identity masking and per-user quota.

## Validation and release policy

All checks must run in GitHub Actions. The CI workflow now includes root and independent-module build/vet, the existing full database integration job, focused SQLite/MySQL/PostgreSQL channel and code-operation selectors, race selectors, frontend typecheck/format/lint/tests/build, and changed-Go formatting. The management migration test uses fresh and upgrade-shaped fixtures on SQLite, MySQL, and PostgreSQL. A separate startup migration against a database made by released commit `53a71f88d5b568847cd7233a08f934824dcb22d3` is still required before claiming released-schema upgrade compatibility.

No validation result is claimed in this report until the Actions run for the pushed commit completes. The audit outbox guarantees atomic mutation-plus-audit only on code batch create and channel create/update transaction paths. Other management mutations enqueue after business commit; they retain a post-commit crash window. Failed enqueue has a bounded direct sink fallback, but simultaneous failure of both paths cannot undo an already committed nontransactional mutation. Delivery is at-least-once; ClickHouse has no event-id uniqueness guarantee.

After the exact branch SHA is green, merge it into `main` and allow the existing `docker-fork.yml` workflow to build and publish the release. Do not create a manual tag or bypass that workflow.

## Reports by owner

- Channel loading and cache publication: `../11-24-channel-loading-save-performance/report.md`
- Channel diagnostics, card presentation, and logs: `../11-24-channel-diagnostics-log-presentation/report.md`
- Dashboard instances and rankings: `../11-24-dashboard-node-ranking-details/report.md`
- Management mutations and code batches: `../11-24-management-mutations-code-batches/report.md`
