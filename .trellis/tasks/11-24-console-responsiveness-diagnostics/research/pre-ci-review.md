# Pre-CI static review — branch `fix/console-responsiveness-20261124`

Reviewer: pi-review (static only; no local build/test/lint/format was run).
Baseline CI run: 45433024113 @ sha 9329486b8 (annotations downloaded to
`research/ann-<checkrun>.json`; workflow run logs are not readable without admin
rights — API returns 403 `Must have admin rights to Repository`).

## Step 1 — Failures of run 45433024113

| Job | Failing step(s) |
| --- | --- |
| Backend vet, build, and test (138583205733) | `Check changed Go formatting` |
| Relay policy, request logs, and database integration (138583206153) | `Verify database contracts, channel settings and relay policies` |
| Frontend typecheck, test, and build (138583206269) | `Check frontend formatting with protected headers`, `Lint changed frontend files`, `Test` |

### 1.1 Backend — gofmt (`controller/channel_audit.go`)

Annotation: `.github` "Go file requires gofmt" attached to `controller/channel_audit.go`.

- Working tree: **fixed**. `git diff HEAD` shows the `channelAuditActor` struct
  re-padded so every field name column is 11 chars (`UserId`…`RequestId`),
  `controller/channel_audit.go:14-25`. Alignment now matches what gofmt emits for
  one contiguous field block (longest name `AuthMethod`/`UserAgent` + 1 space).
- Residual risk: the align run is a single block with no blank line, so gofmt
  writes types in a second aligned column; verify nothing else in the file was
  left unaligned (whole-file gofmt is what CI checks).

### 1.2 Database integration job (138583206153)

Annotation 1 — `TestAuditOutboxClickHouseDelivery` (`model/code_operation_outbox_test.go:529`)
failed with `expected: 1 / actual: 0`, message
`outbox last_error="delivery_failed"; insert error=clickhouse error 60`
(error 60 = `UNKNOWN_TABLE`), preceded by
`INSERT INTO \`audit_logs\` … [rows:0]`.

- Root cause: the test called `migrateClickHouseLogDB()`, which only creates the
  `logs` table (`model/main.go:435` → `clickHouseLogCreateTableSQL`), never
  `audit_logs`. `MigrateAuditLogs()` (`model/audit_log.go:352`) is the function
  that issues `CREATE TABLE IF NOT EXISTS audit_logs …` for ClickHouse.
- Working tree: **fixed** at `model/code_operation_outbox_test.go:506`
  (`require.NoError(t, migrateClickHouseLogDB())` → `MigrateAuditLogs()`), and the
  assertion moved to `GetAuditLogs(AuditLogFilter{RequestId: eventId}, …)`
  (`model/code_operation_outbox_test.go:532-540`).
- Contract check (inspection): `AuditLogFilter.RequestId` exists
  (`model/audit_log.go`, struct field list includes `RequestId string`), and
  `AuditLog.Other.Op` is produced by the delivery path
  (`model/audit_outbox_worker.go:172`).

Annotation 2/3 — `FAIL github.com/QuantumNous/new-api/model` at log lines
3356-3376, and `FAIL` at run tail (lines 21160-21163) *after*
`ok github.com/QuantumNous/new-api/controller 219.968s`.

- Only one model failure is named in the annotations (the ClickHouse test above);
  the controller package passed. No second model failure is identifiable from
  annotations, so treat "model FAIL" as the same ClickHouse failure unless CI
  rerun says otherwise.

### 1.3 Frontend job (138583206269)

Formatter failures — each was "Frontend formatter would change this file" with the
exact desired diff. Status in the working tree by inspection:

| File | Needed change | Tree state |
| --- | --- | --- |
| `web/src/features/dashboard/components/overview/api-info-panel.tsx` | move `sideDrawerContentClassName` import up; collapse `formatPercentValue`/`DetailRow`/`NodeDetailPanel` params to one line | **still looks unformatted** — see risk R1 |
| `web/src/features/dashboard/components/overview/api-info-item.tsx` | blank line before `@/components/...` imports; wrap `stateLabel = t('Unknown')` | **still looks unformatted** — R1 |
| `web/src/features/dashboard/components/overview/__tests__/api-info-panel.test.tsx` | split `it('reports an operator-disabled…', async () => { mockStatusAndInstances({` line; wrap `refetchQueries` args | **still looks unformatted** — R1 |
| `web/src/features/channels/components/channel-card.tsx` | collapse `<div className='min-w-0 shrink-0 overflow-hidden'>{typeCell}</div>` | check |
| `web/src/features/channels/components/channel-budget-cell.tsx` | move `import type { ChannelBudgetView }` above local imports | check |
| `web/src/features/channels/components/__tests__/table-refresh.test.tsx` | wrap `userEvent.setup().click(...)` | check |
| `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx` | drop trailing blank line at EOF | check |
| `web/src/components/code-export/code-export-provider.tsx`, `code-export-api.ts`, `code-batch-history-dialog.tsx` | import/ternary/arg wrapping | check |

Lint failures annotated (eslint/oxlint, run over `web/` paths reported as `src/...`):

| Rule | File:line | Note |
| --- | --- | --- |
| `eslint(no-nested-ternary)` | `web/src/features/rankings/components/model-usage-drawer.tsx:90-147` | nested ternary in ranking rows |
| `eslint(no-nested-ternary)` | `web/src/components/code-export/code-batch-history-dialog.tsx:66-98` | `{batch.creation_batch_id ? … : ''}` was fine as formatter-only; the nested one is elsewhere in 66-98 |
| `unicorn(prefer-at)` | `web/src/features/system-settings/content/api-info-section.tsx:459` | `[index]` → `.at(...)` |
| `unicorn(prefer-at)` | `web/src/features/system-settings/content/__tests__/api-info-section.test.tsx:259` | same |
| `react(no-array-index-key)` | `web/src/features/dashboard/components/overview/api-info-panel.tsx:193` | index key in list |
| `react(jsx-no-useless-fragment)` | `web/src/features/redemption-codes/components/redemptions-mutate-drawer.tsx:275` | single-child fragment |
| `unicorn(prefer-set-has)` (warning) | `web/src/features/rankings/index.tsx:37` | `includes` over literal array |

Test failures (vitest) annotated:

1. `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx:270`
   "loads both local budgets from one page request…" — `Unable to find role="button" and name "Local Budget"`.
2. `web/src/features/rankings/__tests__/model-usage-drawer.test.tsx:108,162`
   — expected text `5,000`, DOM renders `5.0K` (+ `$0.001` quota line). Test/impl
   disagree on quota formatting (compact number vs plain).
3. `web/src/features/invitation-codes/__tests__/management.test.tsx:154,195,247,287,436,465`
   — `onExported`/export callback still asserted as called with a bare array
   (`['code-a','code-b']`) while the implementation now passes an object
   `{ keys, operationId, creationBatchId, replayed }`; and
   `:465` expects the raw key text `private-invitation-code` in the export dialog.

Owner assignment for these: see "Grouped findings" below.

## Step 2 — Static review of `git diff HEAD` (inspection only)

Scope reviewed: 124 changed files (Go backend + `web/` frontend), plus the two
root edits (`middleware/access_token_routes.go`, `web/src/hooks/use-notifications.ts`
+ `web/src/components/notification-popover.tsx`). No build/test/lint/format was
executed; every item below is from reading the diff and the call sites.

Legend: **BLOCK** = will fail CI, **RISK** = may fail CI, **NOTE** = contract/style.

---

### A. pi-logs — dashboard / node ranking / api-info  (`web/src/features/dashboard/**`)

A1. **BLOCK — formatter** `web/src/features/dashboard/components/overview/api-info-panel.tsx:23`.
The import `sideDrawerContentClassName` is already in the sorted position that CI's
earlier annotation asked for (moved above `@/components/ui/*`), but the three
function signatures the formatter wanted collapsed are still expanded:
`formatPercentValue(` at :102-105 (annotation asked for one line) and
`DetailRow(props: { label…` at :110 which _is_ collapsed. So re-check by running
the hosted formatter before the next push — the file was edited after the run and
the annotation diff may now be partially stale.

A2. **RISK — formatter** `web/src/features/dashboard/components/overview/api-info-item.tsx:21`
and `:213-215`. Annotation asked for (a) a blank line before the `@/components/*`
import group and (b) `stateLabel = t('Unknown')` wrapped on the `else if`. Current
text has the blank line present at :20-21 and the `else if … !props.resources)`
break present at :213. Looks already applied — verify only.

A3. **BLOCK — lint `react(no-array-index-key)`** was reported on `api-info-panel.tsx`.
The map callback still destructures an index: `entries.map(({ item, key }, idx) =>…`
(`api-info-panel.tsx:194`) and uses `idx < list.length - 1` at :201. The JSX key is
`key={key}` (:199), so the rule may accept it, but the `idx` binding is exactly what
the rule keys off. If the rerun still flags it, replace the `idx < list.length - 1`
condition with a CSS `divide-y`/`[&>*+*]:border-t` so `idx` can be dropped.

A4. **NOTE — test drift** `web/src/features/dashboard/components/overview/__tests__/api-info-panel.test.tsx:252`
and `:325-327`. Both formatter hunks from the annotation are now applied (`it(...)
=> {` is a clean line, `refetchQueries` is wrapped). Nothing to do.

---

### B. pi-keys — channel list/table/budget (`web/src/features/channels/**`)

B1. **BLOCK — failing vitest** `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx:271`
`findAllByRole('button', { name: 'Local Budget' })` finds nothing in the DOM dump
(CI dumped only the filter toolbar + a popover trigger). In the card view the
budget control is rendered by `channel-budget-cell.tsx:112-113` as
`<QuotaDetailsPopover title={t('Local Budget')} triggerLabel={t('Local Budget')}>`.
The accessible name depends on `QuotaDetailsPopover` forwarding `triggerLabel` to
`aria-label`/text of the trigger. Either (a) `QuotaDetailsPopover` does not render
`triggerLabel` as the trigger's accessible name, or (b) the card view is not what
`renderChannelsPage(..., { enabled: true })` mounts. Fix in the component/test
pair, not by loosening the query.

B2. **NOTE — formatter** `channel-card.tsx:120` and `channel-budget-cell.tsx:26-28`
and `__tests__/table-refresh.test.tsx:174-176`: all three formatter hunks from the
annotation are visible in the tree now (collapsed `<div …>{typeCell}</div>`, the
`import type { ChannelBudgetView }` moved above the relative imports, and the
`userEvent\n.setup()\n.click(...)` split). Re-verify only.

B3. **RISK — test expectation may drift from the priority-order hook**
`web/src/features/channels/hooks/use-channel-priority-order.ts` (+46/-…). Its test
`__tests__/priority-order.test.tsx` grew by +44. Confirm the hook still returns the
same shape the new tests assert; a mismatch would surface as a red `Test` step and
is not visible from the diff alone.

---

### C. pi-billing — option batch / mutation drawers / code batches

C1. **NOTE — new endpoint is fully wired.** `controller/option.go:357`
`UpdateOptionsBatch`, route `router/api-router.go:232` `optionRoute.PUT("/batch", …)`,
token rule `middleware/access_token_routes.go:159`
`"PUT /api/option/batch": accessTokenScopeRule("option:write")`, and
`router/access_token_scope_test.go:628-658` exercise allow/deny. No duplicate key in
the `accessTokenRouteRules` map literal (Go would fail to compile on a duplicate
constant key in a map literal — checked, only one entry).

C2. **RISK — `validateOptionUpdate` re-reads shared state twice.** `controller/option.go:409`
is called per option with the full `values` map, so validation runs `len(options)`
times, each iteration re-snapshotting `common.OptionMapRWMutex` and
`system_setting.Get*()`. Behavior is consistent (the map passed in is fixed) but any
validator that mutates or depends on partial publication will differ from the
single-option path at `:453`. Worth one explicit assertion in
`controller/option_claude_test.go` that a two-key payload rejects on the member
that would be invalid alone.

C3. **NOTE — `UpdateOptionsBatch` error shape** returns HTTP 200 with an error body
(`common.ApiErrorStatus(c, http.StatusOK, err, fields)`) while the input-validation
paths above it return 400. That is deliberate (existing `UpdateOption` convention)
but the new tests must assert the 200-with-`success:false` envelope, otherwise they
will be written against 400 and fail.

C4. **RISK — i18n key** `web/src/features/invitation-codes/index.tsx:346` uses
`t('Export filtered')`. The key is absent from all seven locale files (see §G), but
the **same key already exists in another component**
(`web/src/features/redemption-codes/components/redemptions-primary-buttons.tsx:84`)
and is likewise absent. So this is a pre-existing gap, not a new one — but if the
branch adds it to one file the reviewer should add it to all seven at once.

C5. **BLOCK — failing vitest** `web/src/features/invitation-codes/__tests__/management.test.tsx:155,203,262,316,479`.
Those assertions have already been updated to the object shape
(`expect(saved).toHaveBeenCalledWith({ keys: [...] })`), which matches
`invitation-editor.tsx:110-116` (`props.onSaved({ keys: [], operationId: '', … })`).
Re-run will tell; the CI annotations were from the pre-fix tree.

C6. **NOTE — `redemptions-mutate-drawer.tsx` was heavily rewritten (+396/-…, net −…).**
No `<>
</>` single-child fragment remains, so the `react(jsx-no-useless-fragment)` finding is
addressed. Because the churn is large, check that the drawer still passes `onSaved`
the same object shape the redemption tests expect (they were not in the annotation
list, meaning they passed, but the file changed after the run).

---

### D. pi-ui — system settings, option save path, notification tab, logs presentation

D1. **NOTE — `hooks/use-update-option.ts` (+133)** is the riskiest single frontend
file on the branch: it is the shared save path for every settings section and the
diff adds the batch endpoint. Two failure modes are worth checking by reading:
(a) each `*settings-section.tsx` call site still passes the same argument list
(43 sections touched with small diffs — a missed rename shows up as a TypeScript
error in the `Typecheck` step, which is currently *passing*, so this is likely fine);
(b) the four new locale keys `Could not save {{key}}: {{error}}`,
`This setting must be saved through its dedicated settings API`,
`Subscription Balance Used` are present in `en.json` only — verify they were copied
to the other six files (they are, per §G: only *unused-by-any-file* legacy keys are
missing).

D2. **NOTE — `api-info-section.tsx:447` still uses `apiInfoList[0]?.id`.** The
`unicorn(prefer-at)` annotation at :458 was fixed (`.at(-1)`), but the symmetric
`[0]` access at :447 remains. The rule fires on `[<literal>]` regardless of position,
so this will be flagged again. Recommend `apiInfoList.at(0)?.id` for symmetry.

D3. **NOTE — notification tab order (root edit).**
`web/src/hooks/use-notifications.ts:68-70` now defaults `activeTab` to
`'announcements'` and `notification-popover.tsx:344-357` renders the Timeline
trigger/content first. The guard added at :137-142 redirects to `'notice'` when
`announcementsEnabled` is false — confirm `announcementsEnabled` is defined above
that line (it is used at :139 and declared in the same hook, per the diff context).
No i18n key is introduced (`Timeline`, `Notice` already exist in all seven files).

D4. **RISK — `maintenance/log-settings-section.tsx` and the five new
`__tests__/*.test.tsx` edits** (`bot-protection-settings`, `oauth-settings`,
`pre-consume-settings`, `email-notifications`, `email-settings-section`,
`epay-sign-settings`, `log-settings`, `group-settings`) were **not** covered by the
CI annotation list because the run predates them. They are the most likely source of
the next red `Test` step. They all assert against `use-update-option`, so they break
if D1's signature changed.

---

### E. Backend — shared/contract items across workers

E1. **BLOCK — `model/audit_log.go:13` imports `github.com/ClickHouse/clickhouse-go/v2`
as `clickhouse` and uses it at :245** (`clickhouse.Context(...)`, `clickhouse.Settings`).
Not unused — the scan flag `v2` is a false positive from my alias detector. No action.

E2. **NOTE — `model/audit_outbox.go` `encodeMetadata()` mutates its receiver.**
`EnqueueAuditEventTx` takes `draft AuditEventDraft` **by value** (:126) so the
mutation is local — fine. `RecordManagementAuditDurable`
(`model/audit_outbox_record.go:24`) also takes it by value — fine. But
`UpdateOrdinaryOptionsBatch(ctx, options, audit AuditEventDraft)`
(`model/option.go:506`) forwards `audit` to `EnqueueAuditEventTx` **inside the
transaction**, so a retried transaction would call `encodeMetadata()` again on an
already-converted draft. Inside one call that is not reachable, but if a future
retry wrapper is added it silently writes `ParamsJson` twice into the same struct
with `Other` already nil — second call is a no-op, so it is safe today. Document it
or make `encodeMetadata` return a copy.

E3. **NOTE — `model/audit_outbox_worker.go:152-166` changes legacy behavior.**
`deliverAuditEvent` now **returns an error** when `common.UnmarshalJsonStr` fails on
legacy `params` (previously it substituted `AuditFields{}` and still delivered).
Any outbox row with a non-JSON `params_json` (possible if a plugin or an older
build wrote free text) now becomes permanently `delivery_failed` and blocks the
drain at `===audit outbox===`. Recommend: fall back to `params = AuditFields{}` and
log, rather than fail the delivery, unless a test explicitly asserts the new
`"audit outbox parameters invalid"` behavior (`model/code_operation_outbox_test.go`
only covers the metadata path).

E4. **RISK — database matrix.** `model/audit_outbox.go` now writes a versioned
metadata blob (`_audit_outbox_format`) into `params_json` and
`model/audit_outbox_record.go` honours a caller-supplied `CreatedAt`. Both are
column-level behavior changes: the CI gate that failed (`Verify database contracts,
channel settings and relay policies`) runs SQLite + MySQL 8.4 + PostgreSQL 16 +
ClickHouse 25.8, and the two follow-up steps that were skipped (`…durable audit
delivery`, `…code creation replay and audit contracts`) were skipped **only because
step 8 failed first**. They will run on the next push and are the second most
likely red step after the current fixes.

E5. **NOTE — `controller/channel.go:171`** now returns
`model.RefreshChannelRouting(channelID)`; every existing caller of that helper
(`:2744, :2818, :2865, :2904`) already handles the error, and
`model/channel_publication.go:583` is declared to return `error`. Signature matches.

E6. **NOTE — `controller/option.go` import list** gained `errors`, `net/http`,
`slices`; all three are used (`errors.As` at :404/:417, `http.StatusBadRequest`, and
`slices` in the completion-ratio helpers). No unused import found in any changed Go
file (scanned `controller/`, `model/`, `middleware/`, `common/`, `main.go`, `router/`).

E7. **NOTE — gofmt on Go files changed after the run.** The only Go formatting
annotation was `controller/channel_audit.go` and it is fixed. Files edited after the
run (`controller/option.go` +334/−…, `model/option.go` +154, `model/subscription.go`
+65, `model/user.go` +38, `controller/user.go` +14, `middleware/audit.go` +66,
`router/api-router.go`, `main.go`) cannot be verified by inspection alone; they are
the only candidates for the next `Check changed Go formatting` failure.

---

### F. Diagnostics / trusted proxies (new large files)

F1. **RISK — `middleware/trusted_proxies_test.go` (+789)** and
`middleware/request-id.go` (+3) landed after the run. A 789-line new test file on
top of `middleware/distributor.go` +3 and `common/init.go` +5 is the single largest
untested surface on the branch. If it has one wrong expectation the whole `Verify
database contracts…` step stays red for reasons unrelated to the current fixes.

F2. **NOTE — `controller/access_token_audit_test.go` (+254)** and
`middleware/access_token_routes.go` are auth-adjacent (personal access token scopes).
Per AGENTS.md the change summary must record the OWASP/ASVS references and the
validated failure/deny cases; `router/access_token_scope_test.go:658` covers the
deny case for the new route. Fine, but call it out in the report.

---

### G. i18n — missing keys across the seven locale files

Method: extracted every `t('…')` literal from the changed `web/src/**/*.ts(x)` files
and compared against the flat key sets of `en, zh, zh-TW, fr, ru, ja, vi`
(8326 keys each; all seven have identical key sets, so a gap is always present in
all seven together).

**8 keys are missing from all seven files:**

| Key | Used at | Introduced by |
| --- | --- | --- |
| `Recover the original codes of a batch when the creation response was lost.` | `web/src/components/code-export/code-batch-history-dialog.tsx:57` | this branch (new dialog description) |
| `No API Domains yet. Click "Add API" to create one.` | `web/src/features/system-settings/content/api-info-section.tsx:349` | pre-existing |
| `API info added. Click "Save Settings" to apply.` | `…/api-info-section.tsx:275` | pre-existing |
| `API info updated. Click "Save Settings" to apply.` | `…/api-info-section.tsx` | pre-existing |
| `API info deleted. Click "Save Settings" to apply.` | `…/api-info-section.tsx` | pre-existing |
| `Defaults to "OIDC" if left blank` | `…/auth/oauth-section.tsx` | pre-existing |
| `Export filtered` | `web/src/features/invitation-codes/index.tsx:346`, `…/redemptions-primary-buttons.tsx:84` | pre-existing |
| `[{"name":"支付宝","type":"alipay","icon":"SiAlipay"}]` | `…/integrations/payment-settings-section.tsx` | pre-existing (placeholder, should probably not be a `t()` key at all) |

Only the **first** row is this branch's; the rest are pre-existing gaps that do not
fail any current check (there is no missing-key gate in CI today). Still, per AGENTS.md
"add each key to all seven frontend locale files" — add
`Recover the original codes of a batch when the creation response was lost.`
to `en/zh/zh-TW/fr/ru/ja/vi` before the dialog ships in a non-English locale.

**Locale content changes already in the tree** (`en.json` +5/−…, mirrored in all
seven): `Could not save {{key}}: {{error}}`, `Subscription Balance Used`,
`This setting must be saved through its dedicated settings API` — all three are
referenced by the changed files and present. The fourth change is a rename
`footer.new\u0061pi.projectAttributionSuffix` → `footer.newapi.projectAttributionSuffix`.
**That rename touches the protected project attribution string.** The value is
unchanged, only the escaped module key changed, and the `new` → `newapi` form
matches the canonical spelling — but any CI step that greps for the old escaped key
(as a protected-header check) would now fail. Confirm with root before pushing.

---

### H. Summary — what to fix before the next push, in order

1. **B1/A1/A3** re-verify `channel-details-entry` `Local Budget` accessible name (pi-keys),
   `api-info-panel.tsx` formatting and the dropped `idx` binding (pi-logs).
2. **E3** decide fail-vs-fallback for invalid legacy outbox `params_json` (backend).
3. **D2** `apiInfoList[0]` → `.at(0)` (pi-ui) — same rule already failing at :458.
4. **C5/C6** re-verify the invitation-code export assertions and the rewritten
   redemptions drawer (pi-billing).
5. **G** add the one new dialog-description key to all seven locales; confirm the
   attribution-key rename with root.
6. **E4/F1** expect the two skipped database steps and the new 789-line middleware
   test to be the next failures if 1-5 are clean.

Everything else listed above is either already fixed in the tree or a NOTE that does
not gate CI.

---

## 2026-11-25 (pi-rev-go, item #12) — scan 1: Go compile/type/unused/gofmt across the whole uncommitted diff

Static reading only. Nothing was built, tested, vetted, vetted, gofmt'd or run.

### 1. Formatting defects I fixed in place (mechanical gofmt shape only)

gofmt expands a braced block onto separate lines, so every single-line `if … { … }`
below would have been rewritten by the hosted `Check changed Go formatting` step.

| File:line | Before |
| --- | --- |
| `common/request_content.go:69` | `if parsed, err := strconv.ParseBool(value); err == nil { enabled = parsed }` |
| `common/request_content.go:72` | `if parsed, err := strconv.Atoi(value); err == nil { maxBytes = parsed }` |
| `common/console_forward.go` const block | `consoleForwardRequestTimeoutEnv` line pushed the `=` column to 33 while the rest were at 32 |
| `common/console_forward.go:44-51` | `ConsoleIngressIdentity` field names padded for a wider name than the block contains |
| `common/console_forward.go:31-38` | `ConsoleForwardConfig`/`consoleForward…` columns now align to the longest key |
| `middleware/console_forward.go:30-40` | reserved-header const block: `consoleForwardProofHeader` / `consoleForwardProcessingNodeHeader` were one column off (`=` at 36 vs 35) |
| `model/log_request_detail.go:648` | `if cutoff <= 0 { return nil }` |
| `model/log_request_detail.go:657-658` | `if err := ctx.Err(); err != nil { return … }` and one-line `errs = append(…)` body |
| `model/log_request_detail.go:732` | `if err != nil { return err }` |
| `model/log_request_detail.go:784` | `if expiring { phase = requestContentPhaseExpirePending }` |
| `service/system_task.go:402` | `if !common.IsMasterNode || days <= 0 { break }` |
| `controller/option_claude_test.go:130` | `if _, ok := tx.Statement.Dest.(*model.AuditEventOutbox); ok { tx.AddError(…) }` |
| `model/option_upsert_test.go:196-198` | two one-line `tx.AddError(…)` bodies in the failure-injection callback |

### 2. Formatting defects still open (owned files, reported to owners)

| File:line | Defect |
| --- | --- |
| `middleware/console_forward.go:67-69` | `consoleForwardStreamingRoutes` value column is 54/54/55; gofmt aligns each composite-literal block to its longest key, so all three `{}` values must start at column 56 (route `GET /api/log/export` needs 18 spaces after `:`, `GET /api/log/self/export` needs 13) |
| `middleware/console_forward.go:77-79` | `consoleForwardProcessLocalRoutes` value column is 63/64/64 but the longest key ends at 62, so every value must start at 65 (`GET …/fingerprint/:job` needs 10 spaces, `POST /api/channel/ollama/pull/stream` needs 12) |

Both reported to pi-fwd (w5D:p2) with exact pad counts; root has this formatting gate
deferred to hosted output. Nothing else in the new files needed alignment:
`consoleForwardReservedHeaders` is a plain one-entry-per-line list, and no struct
literal in `common/console_forward.go` shares a block with a longer key.

### 3. Compile / type / unused review

Reviewed `git diff HEAD` for all 53 changed Go files plus the 6 new untracked ones
(`common/console_forward.go`, `middleware/console_forward.go`,
`model/option_upsert_test.go`, `service/passkey/service_test.go`,
`setting/system_setting/update_source.go`, `setting/system_setting/update_source_test.go`).

No blocking finding. Specifically checked:

- **Every new cross-package symbol resolves.**
  `common.LoadConsoleForwardConfig` / `VerifiedConsoleIngress` / `EffectiveRequestScheme` /
  `RequestContentCaptureSettings` / `RegisterAffiliateLinkEnabled`;
  `model.UpdateChannelStatusAtomic` / `RefreshChannelRouting` / `BuildUserListItems` /
  `GetSubscriptionBalanceUsedQuota` / `UpdateOrdinaryOptionsBatch` / `OptionUpdateError` /
  `BumpTaskAliasChannelRevision` / `ExpireLogRequestDetails`;
  `service.IsChannelCapacityError` / `RetryChannelAdmission` / `ChannelCapacityLease.Context`;
  `setting.ValidateLogStorageOption` / `system_setting.UpdateSourceRepository` /
  `ValidateUpdateSourceRepository` / `UpdateSourceRepositoryOption`;
  `operation_setting.ValidateEpaySignOptionWithUpdates`;
  `middleware.ConsoleIngress` / `ConsoleForwarder`.
- **Removed helpers have no remaining callers.** `rebuildTaskAliasView` is now only called
  from `InitChannelCache` (`model/channel_cache.go:167,282`), the previously ignored
  `UpdateChannelStatus`/`InitChannelCache` pairs in `controller/channel.go` are replaced by
  `UpdateChannelStatusAtomic`/`RefreshChannelRouting`, and `updateOptionMap` still exists as
  the locked wrapper used by `model_pricing_config.go:751`, `passkey_option.go:247`,
  `request_policy.go:289`, `option.go:210,242,429,490`.
- **Test signature drift.** `controller/token_test.go` AutoMigrate gained
  `AuditEventOutbox`; `verifyAPITokenAudit` now drains the outbox via the existing
  controller helper. `model/code_operation_outbox_test.go` calls `MigrateAuditLogs()`
  and filters by `AuditLogFilter.RequestId` — both exist. `log_format_test.go:338` still
  calls `reclaimRequestContent(ctx, &pending, true)` and the new variadic
  `expiresBefore ...int64` keeps it compiling.
- **JSON wrapper rule.** The one new `encoding/json` import
  (`model/audit_outbox_worker.go:5`) is only used for the `json.RawMessage` type; the
  marshal/unmarshal calls go through `common.Marshal` / `common.UnmarshalJsonStr`.
- **Imports.** No unused import in any changed Go file (`errors`, `net/http`, `slices`,
  `clause`, `logger`, `strconv`, `os`, `httptrace`, `encoding/binary`, `crypto/*` all used).

### 4. Behaviour risks that are not compile failures (for the owners / root)

1. `model/audit_outbox_worker.go:152-166` still turns a non-JSON legacy `params_json` into a
   permanent `delivery_failed` row instead of the old `AuditFields{}` fallback. `pi-q9`
   owns the decision; noted again because it blocks the drain queue head.
2. `model/option.go` `updateOptionMapLocked` can fail *after* the batch transaction has
   committed (a parser that disagreed with `validateOptionValueWithUpdates`), which would
   leave the DB newer than the published `OptionMap` and return an error for a committed
   write. Worth an explicit assertion on the two parsers staying in sync.
3. `controller/relay.go:503-556` deliberately restores the pre-wait saturated/rejected lists
   through a `defer`. If a future early return is added above the `defer` registration it
   would silently drop the restoration; the ordering is correct today.
4. `common/redis.go:39` now keeps go-redis's CPU-scaled default unless
   `REDIS_POOL_SIZE` is explicitly set. No call site depends on the old fixed 10.

### 5. Verification still owed (cloud only)

`Check changed Go formatting`, `go vet ./...`, the relay-policy/database-integration step
and the ClickHouse audit-delivery steps are the only authority for sections 1-3. I did not
run any of them.

## Applied — pi-q9 (item #9)

Findings still present in the tree were fixed; findings whose files are owned by an active worker were reported to the owner instead (see the per-worker lines below). Static edits only; no local build/test/lint/gofmt was run.

### Fixed here

| Finding | File | Change |
| --- | --- | --- |
| E3 — legacy non-JSON `params_json` made a row permanently `delivery_failed` and blocked the drain | `model/audit_outbox_worker.go` (deliverAuditEvent) | An undecodable `params_json` no longer fails delivery: it is logged via `logger.LogError(common.LogText(...))` and the event is delivered with empty `AuditFields`, so one malformed legacy row cannot stop the outbox. The versioned metadata branch is unchanged; a versioned blob that decodes but is inconsistent still fails (that is a writer bug, not legacy input). |

Notes on the remaining E-series items: E1 (clickhouse alias) is a false positive — no action. E2 (`encodeMetadata` mutating a by-value draft) is already value-safe today; the new delivery fallback does not change it. E4/E7 (`model/audit_outbox.go`, `model/option.go`, `controller/option.go`, `router/api-router.go`, `main.go`, `middleware/*`) are owned by pi-billing/pi-auth/pi-fwd and stay with their owners; the database matrix must still be verified in GitHub Actions. E5, E6, F2 are notes, no code change.

### Handed to owners (not edited here)

- `web/src/features/dashboard/components/overview/api-info-panel.tsx` was listed as pi-logs' file but the formatting + `react(no-array-index-key)` findings were confirmed still present and pi-logs is done/archived, so they are applied in the web section below (`research/pre-ci-web-review.md`).
- `web/src/features/system-settings/content/api-info-section.tsx` (`apiInfoList[0]` → `.at(0)`) — same situation, applied in the web section.

## Applied — pi-q9 second pass (pi-rev-go item #12 scan 1, routed here by root)

### 1. `model/option.go` `UpdateOrdinaryOptionsBatch` — post-commit publish failure (FIXED, option (a))

Was: the transaction committed, then the publish loop over `updateOptionMapLocked` returned `*OptionUpdateError` on the first failure, so the caller (`controller/option.go` `UpdateOptionsBatch`) reported a failed update for rows that had already persisted while `common.OptionMap` kept the stale value until the next sync.

Now (`model/option.go:556-575`): the write is committed first, therefore a publication error can no longer be reported as a failed update. The loop logs through `common.SysLog(common.LogText("failed to publish saved option %s: %s", ...))` and continues; the function returns success. The `SyncTopicOptions` task enqueued inside the same transaction reloads every option from the database on this node and marks the topic for other nodes, so a logged publication failure self-heals rather than leaving a divergence.

Why this is safe as well as honest (the option-(b) argument, recorded in the comment): every key that reaches the loop has already passed `validateOptionValueWithUpdates` with the full batch `updates` map, and where the shape is non-trivial the validator uses the same parser the publish path uses — `Chats`/`PayMethods`/`AutoGroups`/`UserUsableGroups`/`GroupRatio`/`GroupGroupRatio` (JSON shape), `GroupLevels` (`setting.ParseGroupLevels`), `ModelRequestRateLimitGroup`/`...User` (`CheckModelRequestRateLimit*`), `EmailProvider` (`ValidateResendConfiguration`), and the log-storage/quota/epay/update-source/tool-price/channel-test/log-range/system-task-retention validators. The remaining fallible branch is `handleConfigUpdate` → `types.LoadFromJsonString*` over `*RWMap[string, float64]` for the ratio maps; each of those keys (`ModelRatio`, `GroupRatio`, `CompletionRatio`, `ModelPrice`, `CacheRatio`, `CreateCacheRatio`, `ImageRatio`, `AudioRatio`, `AudioCompletionRatio`, `TopupGroupRatio`, `UserUsableGroups`, `GroupLevels`) is already JSON-validated above, so the only residual difference is a float range error, which is exactly the case the log-and-continue path now covers. `AutomaticDisableStatusCodes`/`AutomaticRetryStatusCodes` are parsed by `ParseHTTPStatusCodeRanges` in both paths, and also have validators.

Kept as is: the map lock is still held across the whole loop, so readers never observe half of a saved section; the error is logged without the option value (options may carry credentials).

Note for the hosted DB matrix: no schema/queries changed, so this is behaviour-only and needs no new dialect coverage. `model.OptionUpdateError` is still returned for validation and transaction failures, which is what the R16 tests assert.

### 2. `model/audit_outbox_worker.go:150-186` — legacy non-JSON `params_json` (DECIDED: fallback, as before)

Decision: **fallback, not fail.** A row whose `params_json` is not decodable is logged (`audit outbox %s parameters unreadable, delivering without them`) and delivered with an empty `AuditFields` — the previous behaviour, restored. Rationale: the event id, actor, action and request columns still identify the event, whereas marking the row `delivery_failed` makes it permanently undeliverable and, because the drain processes rows in due order, can stall every later audit event. The versioned-metadata branch is unchanged: a blob that decodes but carries a wrong format version or no `Other` still fails (`audit outbox metadata unavailable`), because that is a writer bug that must not be silently flattened. No test asserts the removed `"audit outbox parameters invalid"` string (checked `model/code_operation_outbox_test.go`), so no test change is needed.
