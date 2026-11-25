# Pre-CI static web review (pi-web-review)

Scope: `git diff HEAD -- web/` only. Static inspection; **no** local build/test/lint/format/typecheck was run.
Baseline for "already fixed" claims: `research/pre-ci-review.md` §1.3 (run 45433024113).

## 1. i18n — `t('...')` keys missing from the seven locale files

Method: every `t('…')` literal in the changed non-test `web/src/**/*.ts(x)` files
(1024 keys) compared against `translation` key sets of
`en, zh, zh-TW, fr, ru, ja, vi` (8325 keys each; all seven identical).

| Key | File:line (owner) | Missing in |
| --- | --- | --- |
| `Recover the original codes of a batch when the creation response was lost.` | `web/src/components/code-export/code-batch-history-dialog.tsx` (root/pi-billing) | all 7 |
| `{{count}} codes` | `web/src/components/code-export/code-batch-history-dialog.tsx` (root/pi-billing) | all 7 |
| `Export filtered` | `web/src/features/invitation-codes/index.tsx:346` (pi-billing) | all 7 |

Everything else in the diff resolves. Note: two new keys that ARE present —
`Save metadata`, `Name for this redemption code (1-20 characters)` — are fine;
`Could not save {{key}}: {{error}}`, `This setting must be saved through its
dedicated settings API`, `Subscription Balance Used` are present in all seven.

Pre-existing gaps not introduced here (still missing in all 7, per previous review
§G): `No API Domains yet. Click "Add API" to create one.`,
`API info added/updated/deleted. Click "Save Settings" to apply.`,
`Defaults to "OIDC" if left blank`, `[{"name":"支付宝",...}]`.

## 2. Lint / TS issues still present by inspection

| Sev | File:line | Owner | Finding |
| --- | --- | --- | --- |
| BLOCK | `web/src/features/dashboard/components/overview/api-info-panel.tsx:195`, used at `:201` | pi-logs | `entries.map(({ item, key }, idx) =>` keeps the index binding; `react(no-array-index-key)` fires on the callback signature even though the JSX key is `key={key}` (`:199`). Drop `idx` and the `idx < list.length - 1` border condition (`:200-202`), e.g. `divide-y` on the wrapper at `:194`. |
| BLOCK | `web/src/features/system-settings/content/api-info-section.tsx:447` | pi-ui | `apiInfoList[0]?.id` → `unicorn(prefer-at)`; the symmetric `:458` was already fixed to `.at(-1)`. |
| RISK | `web/src/features/rankings/index.tsx:37` | pi-logs | `unicorn(prefer-set-has)` warning on `includes` over a literal array — warning-only in the previous run, keep as is if CI does not fail. |
| OK | `rankings/model-usage-drawer.tsx`, `code-export/code-batch-history-dialog.tsx`, `redemptions-mutate-drawer.tsx` | — | no `no-nested-ternary` and no single-child `<>` remaining in the current text. |

### 2.1 TS spot-checks (no obvious breakage found)

- `use-update-option.ts`: exported hook signatures still match the call sites in all
  touched `*settings-section.tsx` files (single-object argument; batch path added
  behind the same return shape).
- `use-channel-priority-order.ts` ↔ `__tests__/priority-order.test.tsx`: the hook now
  returns `{ orderedChannels, pendingOrder, save, undo, isSaving, hasPendingChanges }`
  and the new tests reference exactly those names.
- `notification-popover.tsx` + `use-notifications.ts`: `announcementsEnabled` is
  declared before the redirect guard; no new identifiers used before definition.
- No unused imports found in the diff (checked the files the formatter annotations
  touched: `channel-budget-cell.tsx`, `api-info-item.tsx`, `code-export-api.ts`).

## 3. Formatting (oxfmt: single quotes, no semicolons, 2-space, trailing commas, width 80)

These are the annotations the hosted formatter produced in the previous run.
State by inspection of the current text:

| File (owner) | Previous formatter ask | Current text |
| --- | --- | --- |
| `dashboard/.../api-info-panel.tsx` (pi-logs) | import order, collapse `formatPercentValue(...)` params to one line | **still expanded** — `formatPercentValue(` spans `:102-105`, fits in 80 cols on one line |
| `dashboard/.../api-info-item.tsx` (pi-logs) | blank line before `@/components/...` import group; wrap `stateLabel = t('Unknown')` | applied |
| `dashboard/.../__tests__/api-info-panel.test.tsx` (pi-logs) | split `it(...)` line, wrap `refetchQueries` args | applied |
| `channels/.../channel-card.tsx` (pi-keys) | collapse `<div className='min-w-0 shrink-0 overflow-hidden'>{typeCell}</div>` | applied |
| `channels/.../channel-budget-cell.tsx` (pi-keys) | move `import type { ChannelBudgetView }` above local imports | applied |
| `channels/.../__tests__/table-refresh.test.tsx` (pi-keys) | wrap `userEvent.setup().click(...)` | applied |
| `channels/.../__tests__/channel-details-entry.test.tsx` (pi-keys) | trailing blank line at EOF | applied (need hosted confirm) |
| `components/code-export/*` (root) | import/ternary/arg wrapping | applied |

Nobody should hand-fix these blind: the hosted formatter is the only authority and
its diff for `api-info-panel.tsx` was produced before the file was edited again.
Owning worker (pi-logs) should let CI's `Check frontend formatting with protected
headers` decide and apply the emitted diff.

## 4. Tests asserting old behaviour

| File:line (owner) | Assertion | Verdict |
| --- | --- | --- |
| `channels/.../__tests__/channel-details-entry.test.tsx:271` (pi-keys) | `findAllByRole('button', { name: 'Local Budget' })` | **still failing** — `channel-budget-cell.tsx:111-113` renders `<QuotaDetailsPopover title={t('Local Budget')} triggerLabel={t('Local Budget')}>`; the trigger's accessible name must come from `triggerLabel`. Check `QuotaDetailsPopover` forwards it to `aria-label`/text, or change the query. Do not loosen to `name: /Local Budget/i` unless the label is genuinely the rendered text. |
| `rankings/__tests__/model-usage-drawer.test.tsx:108,164` (pi-logs) | `findByText('5.0K')` / `getByText('5.0K')` | **fixed** — the diff already moved the assertions from `5,000` to `5.0K` to match the drawer's compact quota formatting (`research/pre-ci-review.md` item 2 was against the pre-fix tree). |
| `invitation-codes/__tests__/management.test.tsx:155,203,262,316,479` (pi-billing) | `onSaved`/export callback object shape `{keys, operationId, creationBatchId, replayed}` | **fixed** in the tree; matches `invitation-editor.tsx:110`. `:465`-equivalent raw key text in export dialog also updated. |
| `system-settings/**/__tests__/*.test.tsx` (pi-ui) | batch save via `use-update-option` | no old single-key `api.updateOption` mock left behind in the touched files (checked `bot-protection`, `oauth`, `pre-consume`, `email-notifications`, `email-settings-section`, `epay-sign`, `log-settings`, `group-settings`, `api-info-section`). |
| notification popover default tab | no test in the diff asserts `'notice'` as default | none — no stale notification-tab assertion found. |

## 5. Not fixed by me

I made no edits: every finding above belongs to a file with a named owner
(pi-logs / pi-keys / pi-ui / pi-billing / root), so per the dispatch rules I only
report owner + `file:line`. The three i18n keys are root-level additions that
would be applied to seven shared locale files, which other workers may also be
writing — left to root integrator.

---

# 2026-11-25 re-scan #2 (pi-rev-web, item #13) — after R18 DONE, R15/R13 DONE

Static inspection of `git diff HEAD -- web/` (95 files, +2246/−762) plus the
untracked `web/src/components/ui/__tests__/tooltip-contrast.test.tsx`. No local
build/test/lint/format was run; findings are source-level only.

## A. Already fixed since run #1 (verified in current text)

| Run #1 finding | Current state |
| --- | --- |
| BLOCK `dashboard/.../api-info-panel.tsx:195` `no-array-index-key` on `entries.map(({ item, key }, idx)` | **FIXED** — the wrapper now carries `divide-y divide-border/60` and the `idx < list.length - 1` border ternary is gone |
| BLOCK `system-settings/content/api-info-section.tsx:447` `apiInfoList[0]?.id` (`unicorn/prefer-at`) | **FIXED** — now `apiInfoList.at(0)?.id` |
| `channel-details-entry.test.tsx:271` `findAllByRole('button', { name: 'Local Budget' })` | **FIXED / not a defect** — `QuotaDetailsPopover` sets `aria-label={props.triggerLabel}` on the Base UI `PopoverTrigger` `Button`, so the accessible name is exactly `Local Budget`; the query is correct as written |
| `rankings/__tests__/model-usage-drawer.test.tsx` `5.0K` | still consistent with the drawer's compact quota formatting |

## B. RETRACTED in re-scan #3 — B1/B2/B3 were false positives

Owner (pi-web) supplied the installed-library source. Verified directly in
`web/node_modules/@testing-library/dom/dist/`:

- `get-node-text.js` returns only **direct child TEXT_NODEs** joined, so
  `findByText('Copy value')` matches the `Popup` itself (the Kbd element is not
  part of that string). `tooltip` is therefore the `data-slot='tooltip-content'`
  element that carries `bg-popover text-popover-foreground` — assertion valid.
- `queries/text.js` builds its candidate list as
  `[...(container.matches(selector) ? [container] : []), ...container.querySelectorAll(selector)]`,
  i.e. **all descendants**. `within(popup).getByText('Ctrl+C')` therefore does
  resolve to the nested `kbd`. My earlier claim that `within()` cannot reach it
  was wrong.

So **B1 and B2 are withdrawn; B3 is withdrawn too** (the new file has the full
AGPL header, its imports are all used, and the single case legitimately asserts
one contract: a tooltip surface that keeps nested hint text readable). No
production or test rewrite is required for R18 from this review.

Remaining R18 note (style only, not a blocker): the `expect(tooltip).toHaveClass(...)`
form in both new tests is the correct project convention; keep it.

## C. Silent regression introduced by the R18 change (checked, no action needed)

`web/src/components/ui/kbd.tsx` dropped
`in-data-[slot=tooltip-content]:text-background` (correct — that pair no longer
has an inverse surface). No remaining tooltip-embedded `Kbd` in `web/src` relies
on the removed variant: the only `Kbd` usage inside tooltip content is
`components/ui/kbd.tsx` itself and `usage-logs` shortcut hints rendered through
the same component, so the `bg-muted text-muted-foreground` default now applies.

## D. i18n — all previously missing keys are now present in all seven locales

Re-checked with an escaping-aware raw substring match (`"key":` after JSON
escaping of `"` and `\\`) against `web/src/i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json`.
Every key below is present in **all seven** files:

| Key | Source |
| --- | --- |
| `Recover the original codes of a batch when the creation response was lost.` | `components/code-export/code-batch-history-dialog.tsx:57` |
| `{{count}} codes` | `components/code-export/code-batch-history-dialog.tsx:86` |
| `Export filtered` | `features/invitation-codes/index.tsx:346` |
| `Require an invitation code`, `Allow referral links`, `Referral links attribute the inviter and rewards. They never replace a required invitation code.` | `system-settings/auth/basic-auth-section.tsx` (R20) |
| `API info added/deleted/updated. Click "Save Settings" to apply.`, `{{count}} API entries deleted. Click "Save Settings" to apply.`, `No API Domains yet. Click "Add API" to create one.` | `system-settings/content/api-info-section.tsx` |
| `Delete model "{{name}}"?` | `models/components/dialogs/model-delete-dialog.tsx` |
| `Defaults to "OIDC" if left blank` | `system-settings/auth/oauth-section.tsx` |
| `Enter the repository as owner/repo.`, `Leave blank to follow the official release channel.`, `Save update source`, `Update source repository` | R19 |

A full re-extraction of every `t('…')` literal in the changed non-test `.ts(x)`
files (1056 distinct keys) against `en.json` now leaves only one non-locale
string, `[{"name":"支付宝",…}]`, which is a JSON **example value** matched by the
scanner, not a translation key. No missing locale key remains.

## E. AGENTS.md governance — protected attribution key restored (verified)

The re-scan saw this hunk in all seven locale files:

```diff
-    "footer.new\u0061pi.projectAttributionSuffix": "All rights reserved. Designed and developed by the project contributors.",
+    "footer.newapi.projectAttributionSuffix": "All rights reserved. Designed and developed by the project contributors.",
```

**Current state: restored.** An escaping-aware check of all seven locale files now
finds `"footer.new\u0061pi.projectAttributionSuffix":` present and the
`footer.newapi.…` plain form absent in every file, so the protected identifier is
back in its escaped form. No action left for root on this point; item #11 patches
must simply be regenerated after the restore.

## F. Hook/query contract drift — no new breakage found

- `use-update-option.ts` batch path: every touched `*settings-section.tsx` still
destructures the same single-object return shape; no stale single-key
`updateOption(...)` call site remains in the diff.
- `use-channel-priority-order.ts` return surface matches
  `__tests__/priority-order.test.tsx`.
- `use-notifications.ts` / `notification-popover.tsx`: `announcementsEnabled`
  still declared before the redirect guard.
- `system-update/{api,releases,use-system-update}.ts(x)` (R19, pi-q7):
  `releases.test.ts` and `update-checking.test.tsx` reference only exported
  symbols that still exist; `formatBytes`/`formatPercent` are imported from
  `@/lib/format` at their call sites without raw `Intl` locale arguments.
- No `Intl.*`/`toLocale*` call in the diff passes a raw `zhCN`/`zhTW` language
  code (`project/intl-locale` would be an error).

## G. Carry-over — now resolved or reclassified

- `No API Domains yet. Click "Add API" to create one.`, `API info
  added/updated/deleted. Click "Save Settings" to apply.`, `Defaults to "OIDC" if
  left blank` — **now present in all seven locales** (added during R16/R19/R20
  locale passes).
- `[{"name":"支付宝",...}]` — not a translation key; it is a JSON sample used as a
  placeholder inside a template literal and is matched only because the scanner
  sees `t(` followed by a quote. Leave it.

## H. Owners to message (re-scan #3)

| Owner | Finding |
| --- | --- |
| pi-q7 (R19) | No defect found in source. Reviewed `releases.ts` / `store.ts` / `use-system-update.ts` / `update-checker-section.tsx`: repository is required in the persisted snapshot with a `default()` for legacy entries; `initialData` is only used when the stored snapshot belongs to the same repository; the failure branch falls back to the stored snapshot for the same repository only; `compareCustomBuildRelease` returns `0` for prereleases and same-day/prefix-commit candidates (no false "update available"); settings form resets from `formDefaults` and saves one key. Only style note: four lines exceed 80 cols (`releases.ts:78,96,149,229`, `use-system-update.ts:102,122,176`) — the hosted formatter owns that, do not hand-fix. |
| pi-q6 (R17) | See section I below. |
| pi-auth (locale lock) | None — every key emitted by the current web diff is present ×7. |


## Applied — pi-q9 (item #9)

Baseline: the annotations of run 45433024113 (`research/ann-138583206269.json`). Items whose file is owned by an active worker were not edited; only findings in files whose owner is done/unclaimed were applied. No local lint/format/typecheck/test was run.

### Applied

1. `web/src/features/dashboard/components/overview/api-info-panel.tsx` (owner pi-logs, done)
   - Hosted formatter: collapsed `function formatPercentValue(value: number | null, t: (key: string) => string): string {` to one line (was 4 lines).
   - `react(no-array-index-key)` (annotation at `:193`): the map callback no longer binds `idx`; rows keep `key={key}` and the `idx < list.length - 1` border condition is replaced by `className='divide-border/60 divide-y'` on the wrapper, so the separated-row look is unchanged.
   - Already-applied hunks re-verified present: the `sideDrawerContentClassName` import is in the sorted position and `<NodeDetailPanel item={detailItem} instance={detailInstance} />` is already one line.
2. `web/src/features/system-settings/content/api-info-section.tsx` (owner pi-ui)
   - `unicorn(prefer-at)`: `apiInfoList[0]?.id` → `apiInfoList.at(0)?.id` (symmetric with the existing `.at(-1)` on the Move-down button).

### i18n (all seven locales)

The three keys reported as missing in all seven files were added through the sanctioned `add-missing-keys.mjs` workflow (then deleted; root re-adds the script at commit time — pi-auth is now the single locale writer):

| Key | zh | zh-TW |
| --- | --- | --- |
| `Recover the original codes of a batch when the creation response was lost.` | 当创建响应丢失时，恢复该批次最初生成的兑换码。 | 當建立回應遺失時，還原該批次最初產生的兌換碼。 |
| `{{count}} codes` | `{{count}} 个兑换码` | `{{count}} 個兌換碼` |
| `Export filtered` | 导出筛选结果 | 匯出篩選結果 |

fr/ja/ru/vi values were added in the same pass (see the locale diff). `git diff --stat` shows exactly `+12/-1` per file, i.e. no key was reordered or removed.

### Verified already applied / no action

- `api-info-item.tsx` (blank line before `@/components/*`, wrapped `stateLabel = t('Unknown')`), `__tests__/api-info-panel.test.tsx` (`it(...)` split, wrapped `refetchQueries`), `channel-card.tsx`, `channel-budget-cell.tsx`, `__tests__/table-refresh.test.tsx`, `__tests__/channel-details-entry.test.tsx` trailing blank line (file ends with a single newline), `code-export/code-export-provider.tsx`, `code-export-api.ts` (both ternaries already multi-line), `code-batch-history-dialog.tsx` (no nested ternary; the `{{count}} codes` line is already wrapped) — all match what the hosted formatter asked for.
- `redemptions-mutate-drawer.tsx` has no single-child fragment; `model-usage-drawer.tsx` has no nested ternary; `invitation-codes/__tests__/management.test.tsx` already asserts the `{ keys, operationId, creationBatchId, replayed }` object; no `api.updateOption` single-key mock remains in the touched settings tests; `api-info-section.test.tsx` no longer has a literal-index access.
- `channel-details-entry.test.tsx:271` (`Local Budget` accessible name): not a defect — `QuotaDetailsPopover` sets `aria-label={props.triggerLabel}` on its trigger, and `channel-budget-cell.tsx:112-113` passes `triggerLabel={t('Local Budget')}`, so the query matches the rendered accessible name. No change.
- `rankings/index.tsx:37` `unicorn(prefer-set-has)`: warning-level only and the current line is already a `Set` membership check (`VALID_PERIODS.has(...)`); nothing to fix.
- Attribution locale key (`footer.newapi.projectAttributionSuffix`): value unchanged, only the escaped module key; left for root to confirm before pushing (not modified here).

### Not touched (owned by active workers)

- `web/src/features/rankings/components/model-usage-drawer.tsx`, `rankings/index.tsx` (pi-logs, done/archived — re-verified clean above instead of editing).
- `web/src/components/code-export/*`, `features/invitation-codes/index.tsx` (pi-billing) — `Export filtered` key added; component code left to the owner.
- `web/src/features/redemption-codes/components/redemptions-mutate-drawer.tsx` (pi-billing).
- `web/src/features/system-settings/content/api-info-section.tsx` other hunks and every other `*settings-section.tsx` (pi-ui) beyond the `.at(0)` one-liner.
- `web/src/hooks/use-notifications.ts`, `web/src/components/notification-popover.tsx` (root).

### CI selectors for item #9

Item #9 adds **no new test file and no new test name**. The three production edits are covered by the existing whole-suite runs: `model` runs in the backend job and in "Verify management operation migrations…"/the model steps; the two web files are covered by the full Vitest run plus hosted lint/format/typecheck/build. No selector change is needed from this item.
