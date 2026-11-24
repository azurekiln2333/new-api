# Console responsiveness and diagnostics — integration report

Status (2026-11-24): implementation pushed on `fix/console-responsiveness-20261124`; CI run `45425740952` for `afbdc4c92b91f38af40ad9af4b1db21067f6d5fa` failed. Merge and release remain pending. No local executable validation has been run, by instruction.

This run passed frontend typechecking, backend vet/builds and the real released-baseline startup migration step across SQLite, MySQL and PostgreSQL, including both repeated current-version starts. Hosted version evidence: SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16, Redis 7.4.12 and ClickHouse 25.8.33.6. The backend service package passed in 16.101s. Remaining observed failures are four frontend formatting files, ClickHouse audit-outbox delivery, channel concurrency/cache test fixtures and a nil OptionMap assignment in the ranking policy fixture. Source repairs are in progress; passing startup migration alone does not establish that the remaining database contracts pass.

Earlier run `45404921876` passed frontend typechecking, changed-file formatting/lint, backend vet and the root/independent-module builds. Its explicit console lint step found nested ternaries in `fingerprint.tsx` and a redundant fragment in `channel-budget-cell.tsx`. Those source corrections are committed. Failure-context annotations and log artifacts added for the next run exposed the backend causes listed above while retaining the test commands and failure exit codes.

Source corrections include asynchronous audit assertions that drain the durable outbox, cancellation of obsolete channel-list requests, fingerprint search debouncing and retryable business errors, and group visibility regression fixes. Group order is numerically ascending; existing global grants are additive. The manual editor synchronizes loaded settings, and explicit hidden rules run after level inheritance. These changes are not accepted until hosted checks pass.

## Delivered scope

- **R1 — channel loading:** bounded, paginated channel search/options; SQL-backed tag filtering and counts; batched key-entry projection; guarded cache publication; atomic channel create/update publication and audit enqueue. Channel names and sensitive channel data remain behind existing admin permissions.
- **R2/R3 — management mutations and code batches:** durable audit outbox with retry leases; atomic invitation/redemption batch creation with retry identity; actor-bound recovery and history/export APIs; shared frontend export flow for current selection, filtered results, and historical batches. Redemption claim-limit batch identity remains separate from operation retry identity.
- **R4/R5/R6 — channel diagnostics and logs:** channel cards show local budget in the quota region and the requested metric order with relative last-test time; fingerprint results display three prompt/output cards while retaining the admin gate and cancellation; common log columns use the requested priority order. All seven locale files were updated.
- **R7/R8/R9 — dashboard and rankings:** safe instance-resource summary for authenticated users; API entries can bind to one or more configured domains and retain stable IDs; homepage avatar navigates to the console and admin onboarding is hidden; model ranking opens a usage drawer with viewer-specific server-side identity masking and per-user quota.
- **R10 — group visibility:** numeric level direction is explained explicitly; global grants remain visible and editable through the existing pricing controls. Manual rules appear before levels, update when settings arrive, and retain unfinished rows on parent echoes. Explicit hides win over inherited/manual grants while preserving the existing mandatory own-group rule. Runtime and preview share the same resolver; existing service/frontend suites contain the regression cases.

R10 authorization review consulted the OWASP Authorization Cheat Sheet (deny by default and validate permissions on every request) and ASVS 5.0.0. Permission resolution stays server-side; authentication/session flows and stored configuration are unchanged. The relevant regression sources cover inherited denial, conflicting grants, mandatory own-group access and manual grants. Runtime verification remains pending in Actions; no security-compliance claim is made.

## Validation and release policy

All checks must run in GitHub Actions. The CI workflow includes root and independent-module build/vet, the existing full database integration job, focused SQLite/MySQL/PostgreSQL channel and code-operation selectors, race selectors, frontend typecheck/format/lint/tests/build, and changed-Go formatting. The management migration test uses fresh and upgrade-shaped fixtures on SQLite, MySQL, and PostgreSQL. The separate hosted gate invoked the real startup migration at released commit `53a71f88d5b568847cd7233a08f934824dcb22d3`, seeded legacy data, and invoked current startup twice against each isolated engine. This specific gate passed for `afbdc4c92`; it covers primary startup migration and its asserted preserved data/indexes/uniqueness, not separate log-database startup or all remaining database behavior.

Only the specific hosted results recorded above are claimed; overall validation remains failed. Formatting and changed-file lint must cover the complete branch diff, so a subsequent correction commit cannot hide an earlier failed file. Diagnostic annotations retain nonzero exit status and full log artifacts; independent frontend checks continue after a formatting failure to expose all failures in one run. The audit outbox guarantees atomic mutation-plus-audit only on code batch create and channel create/update transaction paths. Other management mutations enqueue after business commit; they retain a post-commit crash window. Failed enqueue has a bounded direct sink fallback, but simultaneous failure of both paths cannot undo an already committed nontransactional mutation. Delivery is at-least-once; ClickHouse has no event-id uniqueness guarantee.

After the exact branch SHA is green, merge it into `main` and allow the existing `docker-fork.yml` workflow to build and publish the release. Do not create a manual tag or bypass that workflow.

## Reports by owner

- Channel loading and cache publication: `../11-24-channel-loading-save-performance/report.md`
- Channel diagnostics, card presentation, and logs: `../11-24-channel-diagnostics-log-presentation/report.md`
- Dashboard instances and rankings: `../11-24-dashboard-node-ranking-details/report.md`
- Management mutations and code batches: `../11-24-management-mutations-code-batches/report.md`
