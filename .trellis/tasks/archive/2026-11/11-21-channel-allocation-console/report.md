# Channel allocation and diagnostics console — frontend handoff

Worker: `pi-ui`. Parent: `11-21-channel-key-allocation-log-diagnostics`. Workspace: `D:/My2KProjects/new-api`, branch `merge/upstream`, baseline `47a5639c5`. Frontend handoff and independent integration review, including the three routing fixes reassigned by root. No commits, tags, pushes, or deployments by this worker.

## Delivered and cross-checked

- **Allocations.** Manage Keys distinguishes identical static credentials by decimal-string allocation ID, with masked key previews and an explicit public or bound owner. The Add allocation action uses `PUT /api/channel/` with `key_mode: 'append'`; converting an eligible single channel adds `to_multi_key: true`, keeps the original allocation, and loads the server's status rows. The console does not de-duplicate submitted keys. Managed login workflows stay separate; the existing raw replacement path does not override the server's ambiguity rejection. `POST /api/channel/multi_key/manage` status supplies `revision`, `quota_supported`, and each row's `id`, `rev`, `scope_mode`, and quota. Entry changes send `key_entry_id` and `expected_rev`; set-wide changes use `expected_revision`. An explicitly empty allowlist denies all models. The existing user model views still use the backend's eligible model lists; the frontend does not synthesize permissions.
- **Budget management.** Per-allocation limit/reset uses a separate `expected_config_version`. Channel budget reads and writes use `GET/PUT /api/channel/:id/quota`, with reset at `POST /api/channel/:id/quota/reset`. The table and cards load `GET /api/channel/quota?ids=...` in batches of at most 200 IDs and read `data.items`. Local Budget is separate from upstream Balance and works without an upstream balance endpoint. The UI distinguishes an inactive ledger from an active unlimited account, and shows period usage, reservations, remaining capacity and lifetime usage. Inputs are raw integer site quota units, with a display-unit preview. Reset is separately confirmed; an explicit retry of the same confirmation reuses its operation ID and version. Budget CAS conflicts reload without replaying the write. These are additional limits, not wallet credit: wallet/subscription charges and API-token quota deductions remain unchanged.
- **Deduplication.** Only explicit Apply submits the server's reviewed `preview_token` and revision. Preview shows survivor IDs, projected period/lifetime usage, capacity, and skipped busy allocations. A stale preview requires a fresh preview and confirmation. The frontend does not infer equivalence from masked key text.
- **Original requests and nodes.** Authorized open details fetch `GET /api/log/request/:ref` only for a retrievable reference. Closing unmounts the observer, aborts in-flight reads and drops the payload from the query cache; list rows never preload it. The console renders bounded payload fields as text/structured code with existing copy controls, distinct capture/error/legacy states, and truncation notice. Desktop and mobile list views read the processor from `admin_info.node_name`. Details show processing and submit nodes separately; `root_info.node_name` is only a legacy submit fallback for root. Backend remains responsible for admin/PAT `log:read` authorization and `Cache-Control: no-store`.
- **Reuse.** Composes existing `Dialog`, `ConfirmDialog`, `QuotaDetailsPopover`, `LoadingState`, `ErrorState`, `CopyButton` and site quota formatters. No new shared dialog, clipboard or billing implementation.
- **Localization.** 89 new keys across `en`, `zh`, `zh-TW`, `fr`, `ja`, `ru`, `vi`, including fixed backend messages. Dynamic keys are registered in `web/src/i18n/static-keys.ts`. Only the mandated temporary `scripts/add-missing-keys.mjs` wrote translations before `bun run i18n:sync`; the script was removed afterward.

## Verified commands and results

Run from `web/` using Bun **1.4.4** at `D:/AppData/Local/Temp/new-api-pi-20261121/tools/bun-windows-x64/bun.exe` and Node **24.15.0**:

| Command | Result |
| --- | --- |
| `bun run typecheck` (`bun check -b`) | Pass: 1,600 files across 2 projects, no errors. |
| `bun run test --maxWorkers=1 <13 paths below>` | Pass: 298 tests, 13 files; 170.06 s. The default-parallel preliminary invocation hit a Node heap error on the shared host; serial workers passed. Non-fatal jsdom `Window.scrollTo` notices. |
| `./node_modules/.bin/oxlint.exe -c .oxlintrc.json --threads=1 <37 scoped paths below>` | Pass: no diagnostics. |
| `./node_modules/.bin/oxfmt.exe -c .oxfmtrc.json --threads=1 --check <37 scoped paths below>` | Pass: all 37 formatted. Applied oxfmt to owned source bodies while preserving license headers byte for byte; no repo-wide formatter writes. |
| `bun run lint` | Exit 1: **135 errors and 60 warnings outside task paths**, zero errors in the scoped paths. Examples: `src/lib/utils.ts`, `src/components/copy-button.tsx`, `src/features/system-settings/integrations/utils.ts`. Left unrelated files untouched. Full output: `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-full-lint.txt`. |
| `node scripts/add-missing-keys.mjs` then `bun run i18n:sync` | Pass; sync reports zero missing/extras in all seven locale files. Follow-up scan checked 746 keys from changed sources/new entries across all locales, with no missing key or placeholder mismatch and existing translations unchanged. Pre-existing untranslated report entries: ja 6, ru 7, zh 5; none are from the 89 additions. |
| `$env:RAYON_NUM_THREADS='2'; bun run build` | Exit 0 with Rsbuild 2.2.0; built in 8.26 s. `dist/index.html` emitted; 1,857 files, build total 68,425.0 kB / 21,027.2 kB gzip. Generated tracked route tree remained unchanged. Build output: `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-build.txt`. |
| `git diff --check -- web` (repo root) | Pass. |

Focused regression command (PowerShell, from `web/`):

```powershell
bun run test --maxWorkers=1 `
  src/features/channels/components/__tests__/channel-details-entry.test.tsx `
  src/features/channels/components/__tests__/channel-quota-dialog.test.tsx `
  src/features/channels/components/__tests__/channel-configuration.test.tsx `
  src/features/channels/components/__tests__/channel-test-key-selection.test.tsx `
  src/features/channels/components/__tests__/multi-key-key-scope.test.tsx `
  src/features/channels/components/__tests__/multi-key-layout.test.tsx `
  src/features/channels/components/__tests__/multi-key-manage-dialog.test.tsx `
  src/features/channels/hooks/__tests__/use-channel-usage.test.tsx `
  src/features/usage-logs/components/__tests__/billing-source-visibility.test.tsx `
  src/features/usage-logs/components/__tests__/detail-preview.test.tsx `
  src/features/usage-logs/components/__tests__/mobile-card.test.tsx `
  src/features/usage-logs/components/__tests__/reject-reason.test.tsx `
  src/lib/__tests__/server-error-notifications.test.ts
```

Coverage includes IDs above the JS-safe integer range, duplicate append/single conversion, explicit empty allowlists, token-bound dedup and busy skips, local quota CAS and reset retry, inactive ledger and permission gates, one batched page read, authorization/open-only detail fetch for success and failure logs, plain-text rendering, cache eviction on close, truncation, expired refs, and mobile node provenance. Tests mock the HTTP boundary; they do not stand in for live backend or storage tests.

## Parent integration status and remaining gates

- No outstanding frontend DTO mismatch seen against the checked-in handlers/route registry. `pi-keys` reported identity/dedup/legacy-scope tests passed on SQLite 3.50.4, MySQL 8.4.13 and PostgreSQL 16.16. The dispatcher reported the activation deadlock fixed and the full three-database budget matrix passed. The earlier activation/view-read blocker is superseded by that report, not a remaining UI issue.
- **ClickHouse request-detail storage/read/cleanup is not locally verified.** No local ClickHouse service is available and WSL virtualization cannot start in this environment. The parent CI gate is `go test -p 1 -count=1 -v ./model -run '^TestLogRequestDetailStorageDatabaseMatrix$'` with a real `TEST_CLICKHOUSE_DSN`; no pass claimed here.
- Final rollout and released-schema results remain parent/backend evidence, not part of Vitest. The additional real SQLite relay verification is recorded below; no duplicate backend matrix was started by this reviewer. Release/CI and full-repo lint policy remain with the dispatcher.

## Independent integration review and routing fixes

Reviewed the parent `check.jsonl`, PRD, design and implementation plan, backend database/error/logging specs, frontend guidelines, and the full mandatory billing rules before billing review. Authorization review used OWASP ASVS 5.0.0 (function, record and field access; sensitive-log encoding and access) and the Authorization and Logging Cheat Sheets. This is a source review and targeted verification, not an ASVS compliance claim.

### Verified contracts and findings

- **Management permissions:** `router/channel-router.go` guards channel budget reads with `channel:read`, writes/reset with `channel:operate`, and the allocation manager with `channel:operate`. Found action-level PAT attenuation missing from the original account-only sensitive check; reported to keys/root. The shared tree now checks the owning account and `RequireAccessTokenScope` (`controller/channel_authz.go:11`, `middleware/auth.go:320`) for sensitive operations. UI gates are additional to, not substitutes for, these server checks.
- **Identity and model visibility:** `model/channel_key_entry.go` hydrates stable rows, separately projects scope/owner/status, and increments revisions on configuration changes. `controller/channel_key_allocations.go:27` rejects stale stable-entry edits and ambiguous index-only edits; make/bind/discovery use the frozen contract. Both `/v1/models` and `/api/user/models` apply an availability projection; the relay keeps its own token check and authoritative budget validation. Reported managed refresh previously matching changed credentials by raw text, which could retire a bound/scoped allocation and recreate it public, and polling-cursor writes unnecessarily advancing list CAS. Keys' subsequent changes preserve proven same-account rotations and distinguish cursor-only edits (`model/channel_key_entry.go:322` onward, `model/channel_plugin_credential.go` `UpdateChannelLocked`). Final provider/routing matrix belongs to keys/root.
- **Quota lifecycle:** Reset preserves holds and lifetime counters, changes only period counters/version/epoch, and checks operation ID before replay. Settlement carries each item's epoch through merge aliases; old-period refunds do not credit the new period. Dedup compares effective configuration, recomputes the token under the channel transaction, preserves one limit and usage, and checks live holds/tasks (`controller/channel_key_allocations.go:78`, `model/channel_budget_store.go`). Reported success without repricing / failed zero-usage tasks leaving their ledger reservation open, and terminal adjustment failing after channel deletion. The billing changes now handle terminal no-reprice and failed-zero cases and use the retained account lock for post-deletion recovery (`service/task_polling.go:720`, `model/channel_budget_store.go:314`); root reported task budget recovery and the real three-DB matrix passed. The root-owned funding-after-token-error and fee behavior is preserved, not reimplemented.
- **Diagnostics:** Traced authenticated first body read, separated payload from log/list metadata, no-store administrator/PAT endpoint, surviving association check (`controller/log.go:19`, `model/log_request_detail.go:520`), and distinct processing versus submit nodes. Reported SQLite `local` log alias opening the same file as a second pool; logs/root now reuse the shared handle (`model/main.go:235`). The logs child report records the actual before/after alias reproduction, protected-store checks and no-record tests. ClickHouse remains an explicit CI gate.

### Open backend compatibility checks (not part of the routing patch)

- **Model-name normalization:** `model/ability.go:123` and `model/channel_cache.go:282` can select a public channel through `RoutingMatchModelName` when the request name has a recognized modifier. `service/budget_attempt.go:167` passes the original name to `budgetLookupAllocation`; `model/channel_budget.go:417–440` checks exact names. An otherwise eligible public allocation serving the base model can therefore be refused when the ledger is active. The billing and key owners need to align authoritative validation with existing routing without widening an explicit allowlist. Source-confirmed; not run as a new regression by pi-ui.
- **Realtime session accounting:** ordinary `/v1/realtime` is distinct from reusable Responses WebSocket. It admits once before the dial (`relay/channel/api_request.go:501`); `relay/channel/openai/relay_realtime.go:132,295` forwards additional work and calls the existing per-response funding path without a corresponding budget update or next-work check. `service/quota.go:151,240` shows the funding write and the local ledger settlement at session end. Do not claim continuous Realtime enforcement from the two-turn Responses regression; billing/root must resolve coverage or document a rollout restriction. Source-confirmed; no Realtime integration test run by pi-ui.

### Direct routing repairs

- `controller/channel_budget_admission.go:18` initializes selected `ChannelMeta` before attempting the first HTTP hold and refreshes it on retries or same-channel allocation changes. Root independently reproduced the original nil `ChannelMeta` panic with the active-ledger final-fee fixture. No pricing calculation changed.
- `controller/relay.go` and `service/{channel_select,channel_capacity,request_policy}.go` keep budget-refused candidates in a request-local exclusion set, separate from upstream failures and from the capacity wait queue. Use the existing unused auto-group attempt snapshot; do not advance upstream retry count or cross a channel pin or strict affinity. Do not attribute usage to a pre-dispatch refusal. Reset per-attempt reservation state after the previous attempt's release so accounting cannot follow a different selected channel. Only the assigned HTTP routing hunks changed; preserved root's fee and final-error no-record markers.
- `relay/responses_websocket.go` captures the locked allocation ID/revision and revalidates that exact row on each reused turn, restoring the current position and policy-validated allocation into its new Gin context. Equal key text is not a substitute for identity. A reused socket remains on its negotiated channel; the initial dial can skip a local budget refusal before connecting. Release a dial hold on confirmed retry only; the common deferred failure policy retains the final hold until any applicable fee is handled. The first turn relies on its pre-dial hold, not a second dispatch preparation.
- The only new routing test file is `controller/channel_budget_routing_test.go`. It uses the root-owned activation helper, loopback upstreams and a real temporary main SQLite database. The HTTP test covers fallback at `RetryTimes=0` with failed-channel avoidance disabled, plus token pin and strict-affinity refusals with no upstream request and no net funding charge. The WS test sends two complete turns on one upstream socket with a reorder between them; duplicate key rows remain distinct and both reservations settle against the originally selected ID/revision.

### Routing verification

From repository root, Go **1.26.10** (`D:/AppData/Local/Temp/new-api-pi-20261121/tools/go/bin/go.exe`), with `GOWORK=off`, `CGO_ENABLED=1`, w64devkit GCC on `PATH`, and `TEST_RESPONSES_SQL_DSN`/`TEST_RESPONSES_LOG_SQL_DSN` unset (real SQLite):

```text
go test -p 1 -count=1 -v -timeout=90s ./controller -run '^(TestChannelBudgetHTTPAdmissionRoutesWithoutUsingAnUpstreamRetry|TestChannelBudgetResponsesWSReuseKeepsTheSameAllocationAfterReorder)$'
go test -race -p 1 -count=1 -v -timeout=90s ./controller -run '^(TestChannelBudgetHTTPAdmissionRoutesWithoutUsingAnUpstreamRetry|TestChannelBudgetResponsesWSReuseKeepsTheSameAllocationAfterReorder|TestResponsesWebSocketReusesConnectionAndSettlesEachRequest)$'
```

Both passed: controller **7.662 s** and **7.127 s** respectively; no race report. Logs: `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-budget-routing.log` and `pi-ui-budget-routing-race.log`. `gofmt -l` and `git diff --check` were clean for the seven routing/test files above. The initial test run passed the HTTP variants but hit the cursor-CAS issue in test setup; the final reorder performs an authoritative list-revision read before changing positions. CI's `TestChannelBudget` selector includes both new tests; external-engine and rollout evidence is still the parent's responsibility.

### Realtime rollout guard and diagnostic fixture repairs

- **Realtime guard (closed).** An active or unreadable durable ledger refuses `/v1/realtime` early in `service/budget_attempt.go` `ReserveAttemptChannelBudget`, before the client upgrade, the upstream dial and any reservation: active unlimited accounts, a durable-active marker with `CHANNEL_BUDGET_ENABLED=false`, and a marker read error all answer HTTP 503 with `ErrOptionWithSkipRetry` and a non-candidate local error. An inactive ledger keeps the legacy path unchanged, existing-reservation growth is untouched, and no other relay format consults it. `TestChannelBudgetRealtimeRolloutGuard` in `controller/channel_budget_routing_test.go` asserts the real `/v1/realtime` handshake result (503 and no 101), zero upstream dials, zero reservation/operation rows, no consume log, restored wallet/token, and, for the inactive baseline, 101 plus exactly one dial. This closes the task-caused ledger bypass for the transport; it is **not** full Realtime budget support, and the legacy per-response plus session-close double-charge described above remains a separate pre-existing billing defect. The guard runs on the relay request path (not only at the dial), so automatic VAD responses cannot bypass it. Fixture scaffolding for the test lives in `controller/responses_websocket_test.go` (dial counter, optional upstream socket handler, nil-client tolerant teardown, `/v1/realtime` route); Responses WebSocket reuse remains green.
- **Diagnostic fixture repairs (closed).** Two fixtures mutated the credential with a raw `channels.key` write, which leaves the authoritative allocation rows (and the projection hydrated from them) on the old credential, so the guard regressions failed for a fixture reason rather than a product one. `controller/channel_test_request_test.go` (current-cap readmission and fingerprint policy reload) and `controller/channel_upstream_update_test.go` (`replaced source`) now rotate the credential through `model.UpdateChannelLocked`; ordinary `setting`, `header_override` and `models` fields keep their direct update because the locked helper persists only key/status/other-info/channel-info, and the stale source snapshot stays old so the existing assertions are unchanged.

Fixture verification, Go **1.26.10**, `GOWORK=off`, `CGO_ENABLED=1`, `go vet ./controller` clean, selector `^(TestChannelDiagnosticReadmissionUsesCurrentCapAndPolicy|TestFingerprintDiagnosticReloadsPolicyBetweenProbes|TestChannelManagementModelDiscoveryPersistence)$`:

| Engine | Result | Log |
| --- | --- | --- |
| SQLite 3.50.4 | Pass, all three tests | `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-fixtures-sqlite.log` |
| MySQL 8.4.13 (`TEST_CHANNEL_SQL_DSN`, `newapi_ui`) | Pass, all three tests | `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-fixtures-mysql.log` |
| PostgreSQL 16.16 (`TEST_CHANNEL_SQL_DSN`, `newapi_ui`) | Pass, all three tests | `D:/AppData/Local/Temp/new-api-pi-20261121/pi-ui-fixtures-postgres.log` |

Two earlier matrix attempts failed before any assertion ran: the first because a peer was mid-save (`undefined: applyManagedChannelStatus`, later `channel_multi_key_test.go` syntax), the second because the `newapi_ui` schema did not exist yet on either engine. Both were environment/compile state, not product failures; no assertion was changed and no retry was counted as coverage.

### Independent reviews dispositioned without product change

- **RB-1 (binding/scope edits dropped) — rejected for this task.** The cited model path (`model/channel.go:959` `syncKeyEntryAttributesFromProjection` inside `UpdateChannelOtherInfoLocked`) is not reached by the console's append or binding flows: `PUT /api/channel` with `key_mode: "append"` enters the append branch (`controller/channel.go:1338`) and the console binds owners/scopes through `POST /api/channel/multi_key/manage` `set_key_user`/`set_key_models` with stable `key_entry_id` + `expected_rev` (`web/src/features/channels/components/dialogs/multi-key-manage-dialog.tsx:511`, `web/src/features/channels/api.ts:911`). The only console caller of the whole-map edit is the legacy form path, which sends `expected_revision` and no new rows. Keys' `set_key_user` stable-ID flow and append are the three-engine evidence above. No speculative change made.
- **RB-2 (one 409 code for four causes) — not a proven user-visible defect.** The umbrella is deliberate and the console treats it as a reload signal, not a diagnosis: `isQuotaConflict` matches `channel_key_revision_conflict` and the four stale messages (`web/src/features/channels/lib/quota-operation.ts:53`) and every allocation mutation responds by reloading status or requiring a fresh preview; a budget-merge race during dedup surfaces as a retryable conflict, which is the correct recovery. Nothing in the UI claims to distinguish the causes, and no user flow is blocked. Reported as an explicit limitation, not a blocker.
- **RB-3 (dedup skipped-only groups) — already handled in the delivered UI.** `MultiKeyDedupDialog` derives Apply from `groups.some((group) => group.entry_ids.length > 1)` (`multi-key-dedup-dialog.tsx:55`) and lists `group.skipped` with per-row reasons even when `entry_ids` is empty (`:154`), so a group whose members are all busy renders as skips with Apply disabled rather than as a silent "0 merged" success; the apply path also skips groups with fewer than two entries (`controller/channel_key_allocations.go:232`). The preview advertises a merge of the unskipped remainder, which is what apply performs; the skipped rows remain live work, not a lost merge. No change made.


```text
src/features/channels/api.ts
src/features/channels/components/__tests__/channel-details-entry.test.tsx
src/features/channels/components/__tests__/channel-quota-dialog.test.tsx
src/features/channels/components/__tests__/multi-key-key-scope.test.tsx
src/features/channels/components/channel-budget-cell.tsx
src/features/channels/components/channel-budget-context.ts
src/features/channels/components/channel-card.tsx
src/features/channels/components/channels-columns.tsx
src/features/channels/components/channels-dialogs.tsx
src/features/channels/components/channels-provider.tsx
src/features/channels/components/channels-table.tsx
src/features/channels/components/data-table-row-actions.tsx
src/features/channels/components/dialogs/channel-quota-dialog.tsx
src/features/channels/components/dialogs/multi-key-dedup-dialog.tsx
src/features/channels/components/dialogs/multi-key-manage-dialog.tsx
src/features/channels/components/dialogs/multi-key-models-editor.tsx
src/features/channels/components/dialogs/multi-key-quota-editor.tsx
src/features/channels/components/dialogs/multi-key-sync-dialog.tsx
src/features/channels/components/dialogs/multi-key-table-row-actions.tsx
src/features/channels/components/drawers/channel-mutate-drawer.tsx
src/features/channels/hooks/use-channel-mutate-form.ts
src/features/channels/lib/channel-actions.ts
src/features/channels/lib/channel-utils.ts
src/features/channels/lib/quota-operation.ts
src/features/channels/types.ts
src/features/usage-logs/api.ts
src/features/usage-logs/components/__tests__/mobile-card.test.tsx
src/features/usage-logs/components/__tests__/reject-reason.test.tsx
src/features/usage-logs/components/columns/common-logs-columns.tsx
src/features/usage-logs/components/common-log-mobile-card.tsx
src/features/usage-logs/components/common-log-mobile-row.tsx
src/features/usage-logs/components/dialogs/details-dialog.tsx
src/features/usage-logs/components/dialogs/request-content-section.tsx
src/features/usage-logs/lib/request-content.ts
src/features/usage-logs/types.ts
src/i18n/static-keys.ts
src/lib/server-error-message.ts
```

The other seven `web/` paths are `src/i18n/locales/{en,fr,ja,ru,vi,zh-TW,zh}.json`. No temporary translation script or generated output is part of the patch.

## Root integration disposition — 2026-11-23

The earlier model-alias open item is closed by the aligned routing-name validator
and real SQLite/MySQL/PostgreSQL `TestChannelAllocationsRoutingAliasScope` pass.
Realtime is closed for this rollout by explicit local 503 before client upgrade
and upstream dial while the ledger is active, not by claiming continuous Realtime
accounting support. HTTP fallback, stable allocation across two Responses turns,
and the Realtime guard passed all three real engines (MySQL 1.329 s, PostgreSQL
5.060 s, SQLite 5.417 s) and SQLite race (8.828 s), using
`TEST_RESPONSES_SQL_DSN`, with the log DSN unset. Evidence:
`pi-logs-realtime-3db*.log` and `pi-logs-realtime-race.log`.
