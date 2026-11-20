# R12 verification

## Approved presentation correction

The later user instruction restores usage logs to plain exact counts, for example
`41,258 / 115` with `缓存↓ 41,088`, without appended parentheses or expanded rows.
Commit `79cd2f5c7` applies this to desktop/card/details/cache/stat displays; the
existing collapsed mobile summary retains its original compact presentation.
Channel cards use compact 万/亿 totals, with exact and parenthesized detail on hover
and in channel details (`c863fa0e5`). The older broad-surface inventory below is
historical and does not override this correction. CI `44728516165` passed all jobs
at `2fcaab4c6`, including these corrections. No local verification was run after
the user required cloud-only checks.

## Result

Implementation ready for root review and commit. The shared `formatTokens` composes the existing exact `formatNumber` and compact `formatCompactNumber` helpers. Example: `318,574,217 (318.6M / 3.2亿)` for Simplified Chinese. Values below 1,000 remain exact without a redundant abbreviation. Zero is `0`; null, undefined and non-finite counts are `-`. Every interface-derived locale passes through `toIntlLocale` and updates on language changes.

Covered textual surfaces: channel usage badges, totals and model table; dashboard summaries/statistics and chart tooltips; ranking totals/cache breakdowns; log table, mobile rows/cards, details and TPM; users' explicitly token-denominated balance and used-balance columns/popover. Chart axis/bar labels remain compact. Token counts in chart data and table accessors remain numbers. Currency calculations/formatters, prices and billing logic are unchanged.

Reused existing `Badge`, `QuotaDetailsPopover`, overview metrics, static tables, log detail rows and chart configurations. No new UI component, dependency or locale key. Removed the duplicate ranking-only token formatter. Long counts wrap; the channel badge overrides the shared fixed height and quota triggers override shared `whitespace-nowrap` using their existing props.

Playground audit: the active message view currently does not render token usage. `Message.usage` exists but message metadata shows timestamps/durations only. `components/ai-elements/context.tsx` contains an unused token template with no imports/call sites. No speculative playground UI was added. Existing dashboard flow tooltips already show full token counts and were preserved. Quota/currency helpers were not globally redefined.

## Verification performed

Tool: `D:/AppData/Local/Temp/new-api-build-tools/bun-windows-x64/bun.exe`, commands run from `web/`.

- Initial formatter/ranking tests were red before implementation: `D:/AppData/Local/Temp/new-api-token-display-red.log`.
- Additional TPM and wrapping regressions were red before their fixes: `new-api-token-stats-red.log`, `new-api-token-wrap-red.log` in the same temporary directory.
- Final actual-impact regression command:

```text
bun run test src/lib/__tests__/format-tokens.test.ts src/features/rankings src/features/dashboard src/features/usage-logs src/features/channels/components/drawers/__tests__/channel-details-drawer.test.tsx src/features/channels/components/__tests__/channel-details-entry.test.tsx src/features/channels/hooks/__tests__/use-channel-usage.test.tsx src/features/users/components/__tests__/quota-display.test.tsx
```

Result: **43 files, 650 tests passed**. Log: `D:/AppData/Local/Temp/new-api-token-display-regression.log`. Existing jsdom `Window.scrollTo` warnings do not fail tests. Tests cover seven interface languages, invalid-locale fallback, small/large/negative/zero/missing counts, runtime language switching, exact chart tooltips with numeric data unchanged, token-denominated balances and wrapping on narrow surfaces.

- Scoped `oxlint -c .oxlintrc.json` over all 31 files below: **0 errors, 0 warnings**.
- Protected-header-preserving oxfmt stdin check over the 28 exclusively owned files: **pass**. The three shared files were not rewritten by the formatter during concurrent editing.
- Scoped `git diff --check` over all 31 files: **pass**.
- Latest global `bun check -b`: R12 has no errors; **blocked by two unrelated unused imports** in `features/playground/lib/message/__tests__/message-streaming.test.ts` (`extractCitationSources`, `mergeMessageSources`). Root notified. A subsequent root typecheck/build is required before release.
- No browser visual verification or production build claimed by this subtask. Root owns combined build/release checks.

## Exclusive R12 paths (28)

All paths below are relative to `web/`:

```text
src/lib/format.ts
src/lib/__tests__/format-tokens.test.ts
src/features/channels/components/channel-usage-context.tsx
src/features/channels/components/drawers/channel-usage-summary.tsx
src/features/channels/hooks/__tests__/use-channel-usage.test.tsx
src/features/dashboard/components/models/consumption-distribution-chart.tsx
src/features/dashboard/components/models/log-stat-cards.tsx
src/features/dashboard/components/overview/summary-cards.tsx
src/features/dashboard/components/users/user-charts.tsx
src/features/dashboard/lib/charts.ts
src/features/dashboard/lib/__tests__/charts.test.ts
src/features/rankings/__tests__/cache-tokens.test.tsx
src/features/rankings/components/cache-token-details.tsx
src/features/rankings/components/market-share-section.tsx
src/features/rankings/components/model-leaderboard.tsx
src/features/rankings/components/models-section.tsx
src/features/rankings/lib/format.ts
src/features/usage-logs/components/columns/column-helpers.tsx
src/features/usage-logs/components/columns/common-logs-columns.tsx
src/features/usage-logs/components/common-log-mobile-card.tsx
src/features/usage-logs/components/common-log-mobile-row.tsx
src/features/usage-logs/components/common-logs-stats.tsx
src/features/usage-logs/components/__tests__/mobile-card.test.tsx
src/features/usage-logs/components/__tests__/usage-facts.test.tsx
src/features/usage-logs/components/__tests__/common-logs-stats.test.tsx
src/features/users/components/user-quota-cell.tsx
src/features/users/components/users-columns.tsx
src/features/users/components/__tests__/quota-display.test.tsx
```

## Shared hunks for root staging (3 paths)

- `web/src/features/channels/components/__tests__/channel-details-entry.test.tsx`: R12 changes only the `formatCompactNumber` import and two `CONSUMED_TOKENS` expectations to `formatTokens`. Other concurrency/layout/group edits belong to pJ/root.
- `web/src/features/channels/components/drawers/__tests__/channel-details-drawer.test.tsx`: R12 adds the `formatTokens` import and changes six token value assertions (900,000; 1,100,000; 400,000; 1,500,000; 1,200,000; 300,000). Request count, group and concurrency assertions belong to their existing owners.
- `web/src/features/usage-logs/components/dialogs/details-dialog.tsx`: R12 adds `toIntlLocale`, derives locale in `TokenBreakdown`/`DetailsDialog`, and uses `formatTokens(value, locale)` for input/output/cache/image/audio/text counts. The `conversionLabel` empty-chain/Unknown change belongs to R4 and must be staged separately. Arbitrary billable usage quantities and all monetary rows were preserved.

Root owns the Git index and commits. No files were staged or committed by this subtask; no R2 changes were made.

## User correction completed — November 20, 2026

This section supersedes the earlier all-surface formatting scope. The channel
card/detail contract keeps exact-plus-abbreviation formatting. Log formatting
was restored separately in `79cd2f5c7` and is unchanged by this follow-up.

The remaining non-channel uses introduced by `7100b258f` have been restored
against that commit's parent: dashboard summaries/stat cards and user chart
rankings use their prior compact values; distribution chart tooltips use plain
exact counts. Rankings reuse their prior fixed K/M/B/T precision in
`formatRankingTokens`, with exact cache counts in titles and non-wrapping
label/value pairs. User balances use `formatQuotaWithCurrency` again, preserving
the independent Total Used column, raw numeric sorting and currency semantics.
The newer fingerprint page uses plain exact counts for usage, budget and output
limits. Interface languages still pass through `toIntlLocale`.

Existing dashboard, ranking-cache and user-quota assertions were adjusted to the
restored displays; no test files were added. Source-reference inspection leaves
shared `formatTokens` in channel metrics and its own tests only. No channel,
usage-log, pricing, accounting, permission or data-selection behavior changed.

Only source review, edits and targeted formatting were performed. No local
test, lint, typecheck, build or browser verification was run. Earlier passes
above do not verify this correction; root owns the next exact-commit cloud CI
run and publication. No verification pass is claimed for these final edits.
