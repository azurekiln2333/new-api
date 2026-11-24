# pi-ui report — channel diagnostics and log presentation (R4, R5, R6)

## Authorization and hard validation ban — ACK

Explicitly acknowledged:

- **NO LOCAL VALIDATION OF ANY KIND.** No `go test`/`go build`/`go vet`, no `bun test`/`bun run build`/`bun check`/`tsc`, no `gofmt` checks, no oxlint, no database setup or probes, no benchmarks, no browser-based local app validation, no local tool installs, no local servers. All executable validation runs only in GitHub Actions, owned by root. Root also added a cloud `gofmt` gate.
- Tests may be **written** but **never executed** locally. Source self-review only.
- No staging, commit, push, tag, deploy, or spawning additional workers. Root owns branch/CI/spec/release.
- Shared working tree: I do not revert peer edits and do not touch other owners' files. Peers: pi-keys `w4C:p9`, pi-billing `w4C:pJ`, pi-logs `w4C:p11`, pi-ui `w4C:p12`.

## Ownership

- `web/src/features/channels/**` (card layout, budget placement)
- `web/src/features/service-status/fingerprint*` (prompt/output cards)
- `web/src/features/usage-logs/components/columns/common-logs-columns.tsx` (column order)
- `service/channel_fingerprint*.go`, fingerprint controller tests
- **ALL** frontend locale JSON and static-key manifests (central i18n owner for peer keys from pi-billing and pi-logs)

## Required reading (completed before edits)

- Parent `prd.md`, `design.md`, `implement.md`, `dispatch.md`, `research/source-findings.md`
- Child `prd.md`, `design.md`, `implement.md`, `implement.jsonl`, `check.jsonl`
- `.trellis/spec/frontend/channel-card-interactions.md`, `.trellis/spec/frontend/model-selection.md`
- `.agents/rules/billing.md` (in full — quota/budget display work)
- Skills: `i18n-translate`, `shadcn-ui`, `vercel-react-best-practices`; `web/AGENTS.md`

## Scope

R4 channel card metric layout + local budget in existing quota region; R5 fingerprint actual prompt/output cards and per-probe state; R6 common log column order; seven-locale integration for all keys including peer keys.

Status: implementation complete; validation pending in GitHub Actions only.

## Delivered

### R4 — channel card layout (`web/src/features/channels/components/`)

`channel-card.tsx`

- Row 1 is unchanged: type badge, channel name and ID on two lines; the live
  concurrency `current/limit` reading and the status/action icons sit next to
  them.
- Metric column order is now exactly: **Used tokens → Remaining / Total →
  Response → Last Tested**. Priority/weight remain the final row.
- The separate Local Budget row is gone. The enabled local budget fills the
  existing quota region.
- Last test reuses the existing `test_time` cell renderer, so the inline value
  stays relative and the exact timestamp stays in its tooltip.

`channel-budget-cell.tsx`

- `ChannelQuotaCell` renders the server projection (`remaining_quota` /
  `limit_quota`) with the shared quota formatter and its details popover.
- State handling is explicit, so upstream data is never presented as the local
  budget:
  - masked (`sensitiveVisible` false) → `••••`
  - batch read in flight → `Loading...`
  - no projection for the channel → `Unavailable`
  - ledger inactive → `Not enabled`
  - known and enabled → local budget
  - known and explicitly disabled → upstream balance (authoritative there)
- Upstream balance stays a distinct value in the details drawer.
- The original `ChannelBudgetCell` keeps its masking guard.

`dialogs/balance-query-dialog.tsx`

- After an authoritative balance is applied, the querying state is released
  before the background invalidation starts; error/unknown responses still take
  the error path.

### R5 — fingerprint probes (`web/src/features/service-status/`)

- `fingerprint-api.ts` carries `prompt`, `prompt_truncated`, `output`,
  `output_truncated` on `FingerprintProbe`.
- `fingerprint.tsx` always renders three fixed cards (`PROBE_SLOT_COUNT = 3`),
  independent of the requested count, each showing the actual prompt text and the
  actual reply, per-probe state, and a `Truncated` label when the retained text
  was cut. Slots with no probe render an explicit `Not run` card
  (`data-probe-slot='unused'`), never a placeholder that implies a probe ran.
- The 1–3 count control only updates local state; it issues no request, so no
  extra upstream call can be created by changing it.
- Scores, cancel, budget bounds and the admin gate are unchanged.
- Channel dropdown now uses `getChannelOptions` with `keyword` search, `p`
  paging driven by `has_more`, and the current selection pinned through `ids`, so
  an off-page selection still resolves its models. `keepPreviousData` avoids a
  stale flash while a keyword or page changes; the query key includes identity,
  channel, keyword and page.
- Backend retention: `service/channel_fingerprint.go` bounds stored prompt/output
  text at 4096 runes (`fingerprintProbeTextMaxRunes`) with rune-aware cutting and
  explicit truncation flags; `service/channel_fingerprint_job.go` records them on
  completion and failure.
- `channels/api.ts` / `types.ts`: `getChannelOptions` plus `ChannelOption`,
  `ChannelOptionsParams` (`p`, `page_size`, `keyword`, `ids`) and
  `ChannelOptionsResponse` (`items`, `page`, `page_size`, `total`, `has_more`).

### R6 — usage log column order

`usage-logs/components/columns/common-logs-columns.tsx` orders the original
column objects as `created_at, user, model_name, is_stream, prompt_tokens,
use_time, quota, channel, node_name`; every other column keeps its relative order
after them. Column ids, conditional admin/root roles and cells are untouched, and
no column is dropped or duplicated.

### i18n — central owner for all seven locales

Added, with real translations in en, zh, zh-TW, fr, ja, ru and vi (no dotted
keys; existing keys and translations untouched):

- Mine: `Remaining / Total`, `Truncated`, `Not run`
- pi-logs: `Failed to load usage for this model`, `Model usage`, `Usage and user
  ranking`, `Usage and user ranking for {{model}}`, `No usage data for this
  period`, `User Ranking`, `{{count}} requests`, `Requests`, `Tokens`, `Quota`,
  `Usage is aggregated from hourly export buckets and may lag behind live
  traffic.`, `Unknown`, `Stale`, `Disabled`, `Healthy`, `Status`, `Domains`,
  `API URL`, `CPU`, `Memory`, `Storage`, `Configured routes, latency checks, and
  node resources`, `No API routes configured`, `Instance`, `Unbound`, `Order`,
  `Move up`, `Move down`, `Node name of the serving instance`, `Bind this API
  domain to a system instance. One instance can serve multiple domains.`

Keys that already existed were left alone (`Retry`, `Model usage`, `Requests`,
`Tokens`, `Quota`, `Unknown`, `Disabled`, `CPU`, `Memory`, `Storage`, `Instance`,
`Order`, `Healthy`, `Status`, `API URL`, `API Info`, `Search channels`, …).

`Retrying...` was added earlier from pi-logs' first list and then **removed**
when pi-logs replaced its custom retry UI with the shared `ErrorState` component;
nothing referenced it. `Retry` was **kept** — it is `ErrorState`'s own label and
is used by many unrelated components, so deleting it would have broken them.

`bun run i18n:sync` was executed earlier in this session and reported no missing,
untranslated or placeholder-mismatched keys in any locale. The ban was then
extended to cover `i18n:sync` along with lint, format and every other local
executable, so no further sync has been run and none will be: later verification is
source-only (direct key reads against the seven locale JSON files), and every
formatted/executable check belongs to GitHub Actions.

## Regression tests written (not executed)

- `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`
  — metric order, relative last test, enabled local budget in the quota region,
  and explicit `Unavailable`/`Loading` when the local budget is unknown. The page
  fixture gained an optional budget projection so the enabled case is explicit.
- `web/src/features/service-status/__tests__/fingerprint.test.tsx` — three probe
  cards with actual prompt/reply, one explicit `Not run` slot, `Truncated`
  labelling, keyword search paging on `has_more`, plus the existing administrative
  and cancellation coverage migrated to the options contract.
- `web/src/features/usage-logs/components/__tests__/detail-preview.test.tsx` —
  common column order and absence of duplicates.

## Validation status

Not executed locally by design. Pending in GitHub Actions: Go build/vet/tests and
`gofmt` gate for the fingerprint service, frontend typecheck, lint, test suite and
build. Database verification and the ClickHouse matrix code paths are untouched by
this task.

## Follow-up audit (source-only, no local validation)

### i18n audit of peer UI

Every `t('...')` key was extracted from the changed peer surfaces and diffed
against all seven locale files:

- Dashboard/API cards: `api-info-item.tsx`, `api-info-panel.tsx`,
  `overview-dashboard.tsx`, `hooks/use-instance-resources.ts`,
  `system-settings/content/api-info-section.tsx`
- Rankings: `model-usage-drawer.tsx`, `model-leaderboard.tsx`, `models-section.tsx`,
  `pulse-section.tsx`, `entity-links.tsx`, `index.tsx`
- Invitation/redemption: `invitation-editor.tsx`, `redemptions-mutate-drawer.tsx`
- Model/vendor drawers: `vendor-mutate-dialog.tsx`, `vendor-operation-dialog.tsx`,
  `model-mutate-drawer.tsx`

Result: exactly one key was missing from all seven locales —
`{{count}} API entries deleted. Click "Save Settings" to apply.` — now added with
translations for en, zh, zh-TW, fr, ja, ru and vi, following the existing
`API info deleted. Click "Save Settings" to apply.` wording.

Remaining "untranslated" hits were verified false positives and left unchanged:
`CPU`, `URL`, `API URL`, `ms`, `1M`/`1W`, the `https://api.example.com`
placeholder, and French labels whose correct spelling matches English (`Quota`,
`Instance`, `Description`, `Actions`, `Route`, `Route active`). No dotted keys were
introduced. That sync report covered every locale; later and remaining checks are
source-only key reads, because the run ban now covers `i18n:sync` too.

Batch-export labels are being wired by pi-billing (`export-logs-dialog.tsx` and the
history actions). The keys present at the time of this audit already existed, so
nothing was needed then. When pi-billing sends its English label list, they will be
translated into all seven locales by direct JSON edits only — no script, no sync,
no formatter — and this report will be updated at that point.

### Contract re-verification

- **R4** `channel-card.tsx`: name/ID two lines, concurrency beside them before the
  icons, then `Used tokens`, `Remaining / Total`, `Response`, `Last Tested`, with
  `Priority`/weight on the trailing row. Enabled local budget fills the quota
  region; unknown/loading local state renders `Unavailable`/`Loading...`.
- **R5** `fingerprint.tsx`: `PROBE_SLOT_COUNT = 3`, real cards show
  `probe.prompt`/`probe.output`, `Truncated` marks cut text, unused slots render
  `Not run` with `data-probe-slot='unused'`. The count control remains local state.
- **R6** `common-logs-columns.tsx` ordering block lists `created_at`, `user`,
  `model_name`, `is_stream`, `prompt_tokens`, `use_time`, `quota`, `channel`,
  `node_name`, then the rest in original order.

The card-order regression was tightened to assert structural document order plus
shared metric column, so a future column-layout change cannot silently reorder the
labels.

### Final source review (fingerprint list identity and cancellation)

**Access and identity.** `fingerprintAccessKey(user)` returns null unless the user
is at least `ROLE.ADMIN` with `channel:operate`; otherwise it returns
`${user.id}:${user.role}:${channelGrants}`. `FingerprintConsole` is keyed by that
string, so a demotion, a permission edit or an account switch unmounts the console
and its state instead of leaving an earlier admin's data on screen.

**Channel list identity.** The options query key is
`['fingerprint-channels', identity, channelId, keyword, page]`, so one identity can
never read another's cached channel names. The smoothing placeholder is guarded by
identity as well (`previousQuery?.queryKey[1] === identity ? previousData :
undefined`), so the stale-page behaviour cannot leak the previous admin's list
after a switch.

**Cancellation.** `cancelMutation` posts `job.channel_id` + `job.id` and applies the
response only when it still targets the current run
(`current?.id === cancelled.id ? cancelled : current`), so a late response cannot
overwrite a newer run. The poll query is keyed by
`['fingerprint-job', identity, jobChannelId, jobId, pollAttempt]`, is enabled only
while `running && !pollStopped`, passes the abort signal, disables retries and
stops on a terminal state. A poll failure sets `pollStopped` so the operator can
retry or start a fresh run rather than polling a run that may have expired.

**R4/R5/R6** re-confirmed in source: card order `Used tokens` → `Remaining / Total`
→ `Response` → `Last Tested`, concurrency in the name row, priority/weight on the
trailing row; three fixed probe cards with real `prompt`/`output`, `Truncated`, and
explicit `Not run` for unused slots; the log ordering list unchanged.

No feature work was added beyond this scope.

### Integration repairs (source-only, no execution)

Two compile breaks on released files were fixed:

1. `web/src/features/rankings/__tests__/model-usage-drawer.test.tsx` imported
   `../model-usage-drawer`, but the implementation lives in
   `web/src/features/rankings/components/model-usage-drawer.tsx`. The import is now
   `../components/model-usage-drawer`. The exported name (`ModelUsageDrawer`) and its
   props (`modelName`, `period`, `open`, `onOpenChange`) match the test's usage, and
   the drawer's own relative imports (`../hooks/use-rankings`, `../lib/format`,
   `../types`) resolve from the `components/` directory. `@/components/drawer-layout`
   resolves too — it is a `.ts` module, not `.tsx`.

2. `web/src/features/usage-logs/components/columns/common-logs-columns.tsx` read
   `column.accessorKey` directly on the `ColumnDef<UsageLog>` union, which only
   accessor columns carry. Both key derivations (the `byId` map build and the
   trailing pass) now use a guard:
   `column.id ?? ('accessorKey' in column ? column.accessorKey : undefined)`,
   followed by the existing `typeof id === 'string'` narrowing. Column order and the
   conditional admin/root columns are unchanged.

The same unsafe cast in my own regression test (`detail-preview.test.tsx`) was
replaced with the guard so it cannot become the next compile error.

### Translation status

All five code-export/history keys were already integrated in the previous step and
re-confirmed present: `Every code in this create batch`,
`Every code matching the current filters, across all pages`, `Create history`,
`No create batches yet.`, `Shared claim limit` — 5/5 in en, zh, zh-TW, fr, ja, ru
and vi, together with the `Browser storage is unavailable …` key.

## Frozen state

Work is frozen at this point. This worker performs no further local script, sync,
lint, format or validation execution, and does not edit peers' frontend files.
Remaining locale work is limited to translating export/history labels that pi-billing
sends, using direct file edits only.

### Keys integrated since the freeze note

Translated into all seven locales by direct JSON edit only (no script, no sync, no
formatter, no executable check):

- `Browser storage is unavailable. Enable it before creating codes so retries cannot
  create duplicates.` — the real error shown when `sessionStorage` is disabled, so
  the retry guard cannot silently create duplicate codes. Inserted after the
existing `Browser storage is full …` key in every locale, keeping the files sorted.

### pi-billing code export/history labels — status

`web/src/components/code-export/` (`code-export-dialog.tsx`,
`code-batch-history-dialog.tsx`, provider, api) was reviewed by direct source read.
Of its twenty strings, the following are **already present** in all seven locales:
`{{count}} codes`, `{{count}} selected codes`, `Cancel`, `Close`, `Download`,
`Export`, `Export codes`, `Exporting...`, `Failed to export codes`, `File format`,
`Include name`, `Include quota`, `Loading...`, `Save as TXT`, `Save as Markdown`.

Five keys still need translation once pi-billing confirms its final wording:
`Every code in this create batch`,
`Every code matching the current filters, across all pages`, `Create history`,
`No create batches yet.` and `Shared claim limit`. They were not added yet in case
the wording changes; nothing is broken while absent, because the English source
string renders as its own fallback.

**Update — those five keys are now integrated.** pi-billing confirmed the exact
wording, and all five were added to the seven locales by direct JSON edit only:

| key | zh | fr | ja | ru | vi |
| --- | --- | --- | --- | --- | --- |
| `Create history` | 创建历史 | Historique des créations | 作成履歴 | История создания | Lịch sử tạo |
| `Every code in this create batch` | 本次创建批次中的全部兑换码 | Tous les codes de ce lot de création | この作成バッチのすべてのコード | Все коды этого пакета создания | Tất cả mã trong lô tạo này |
| `Every code matching the current filters, across all pages` | 符合当前筛选条件的全部兑换码（跨所有分页） | Tous les codes correspondant aux filtres actuels, toutes pages confondues | 現在のフィルターに一致するすべてのコード（全ページ） | Все коды, подходящие под текущие фильтры, по всем страницам | Tất cả mã khớp bộ lọc hiện tại, trên mọi trang |
| `No create batches yet.` | 暂无创建批次。 | Aucun lot de création pour le moment. | 作成バッチはまだありません。 | Пакетов создания пока нет. | Chưa có lô tạo nào. |
| `Shared claim limit` | 共享领取上限 | Limite de réclamation partagée | 共有の受け取り上限 | Общий лимит получения | Giới hạn nhận chung |

zh-TW uses the traditional forms (建立歷史 / 本次建立批次中的全部兌換碼 / 符合目前篩選條件的全部兌換碼（跨所有分頁）/ 尚無建立批次。/ 共用領取上限). Each key
was placed at the same sorted position in every file, so the diffs stay one added
line per key per locale.

This completes the code-export/history locale set: all twenty strings used by
`web/src/components/code-export/` plus the `Browser storage is unavailable …` error
now exist in all seven locales. Verified by direct read: each key appears exactly
once in every file, and the surrounding ordering is locally correct. The locale
files were already not globally sorted before these edits (the committed `en.json`
is unsorted), so no whole-file reordering was attempted.
