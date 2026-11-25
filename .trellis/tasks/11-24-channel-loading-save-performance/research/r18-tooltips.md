# R18 tooltip contrast — pi-web

## Checkpoint 1 — resumed 2026-11-25
- Queue #4 remains CLAIMED by pi-web; no previous progress file existed on resume. Trellis SessionStart context loaded; continuing the already authorized in-progress parent, not creating a task.
- Read root `AGENTS.md`, `web/AGENTS.md`, parent PRD/queue, shadcn-ui / before-dev / React skills, frontend and guides indexes.
- Inspected existing diffs for shared tooltip/chart/popover/hover-card and dashboard/API/channel cells. Shared overlay files have no pre-existing diff; API items and log columns contain earlier workers' changes and must be preserved.
- Confirmed shared `TooltipContent` uses inverse `bg-foreground text-background`, while custom children use normal `text-foreground` and `text-muted-foreground`. Shared popover already uses the paired `popover` tokens. Need locate storage meters and finish all tooltip/hover/chart callsite audit.
- Boundary: fix theme-paired surfaces at shared overlay/chart styling where needed; preserve custom content, translations, billing/requests, and all other workers' edits. Reuse existing Tooltip/Popover/HoverCard components, no replacement UI.
- Planned validation: focused deterministic frontend regression tests authored for GitHub Actions only. No local tests/builds/vet/lint/gofmt/typecheck/format/Go/Bun/Node or database commands have been run.
- Next: read relevant task design/implementation artifacts and theme/overlay callers, add failing contrast regression coverage before production edits; checkpoint audit findings and changes here.

## Checkpoint 2 — audit and implementation decision
- Read parent/child design and execution plans; queue #4 explicitly hands frontend scope to pi-web (old child boundary pre-dates this handoff). Informed root via `herdr agent prompt w5D:p1`.
- Reported storage tooltip lives in `features/system-info/components/system-instances-panel.tsx` (`ResourceCell` with muted labels); retry hover lives in `features/usage-logs/components/columns/common-logs-columns.tsx` (muted Chain/Key labels). Both reuse shared TooltipContent; no custom feature markup needed.
- Audited TooltipContent / PopoverContent / HoverCardContent callsites across `web/src`, including storage/sync, retry/affinity/timing, channel status/budget, pricing uptime, check-in calendar, badge-list, masked values and AI hover cards. Same inverse-vs-normal-token issue also affects badge-list (custom bg-popover), pricing uptime, channel details and calendar secondary labels. Shared popover/hover-card already use paired tokens; VChart DOM tooltips use its light/dark ThemeManager without offending inverse classes. Unused Recharts tooltip has a compatible background and no inverse override; leave unrelated chart implementation untouched.
- Additional required companion fix: `components/ui/kbd.tsx` explicitly switches to text-background inside tooltips. Remove its inverse-only contextual classes when switching shared tooltip to popover tokens; keep normal bg-muted/text-muted-foreground.
## Checkpoint 3 — fix and regressions authored
- Added `components/ui/__tests__/tooltip-contrast.test.tsx`: keyboard-focus tooltip exposes normal popover tokens and retains readable shortcut tokens (including guarding against the old inverse Kbd selector).
- Extended existing `system-info/__tests__/system-instances-panel.test.tsx` with real storage Used/Free/Total hover content; extended existing usage-log `mobile-card.test.tsx` with the reported desktop channel/retry example (`CNBNew #38`, `118 → 82 → 38`). Existing peer additions in mobile-card remain intact.
- Changed shared TooltipContent surface to `bg-popover text-popover-foreground` with subtle ring/shadow, and arrow to the same popover color; removed inverse-only tooltip Kbd overrides so keyboard hints do not regress. No feature production, auth, locale, billing, chart data, API or DB changes.
- Acknowledged pi-auth's ownership of auth defaults/BasicAuthSection/locale writes and avoided them.
- No tests or executable validation run locally; pending CI selectors: `src/components/ui/__tests__/tooltip-contrast.test.tsx`, `src/features/system-info/__tests__/system-instances-panel.test.tsx`, `src/features/usage-logs/components/__tests__/mobile-card.test.tsx`.
## Result — implementation DONE, hosted validation pending
- Final diff/source review: only two shared production files changed; all other production code and protected headers/branding preserved. Public props, positioning, portal, hover delay and keyboard interaction remain unchanged. No new component/helper or translation keys were introduced.
- Existing regression test files were extended rather than duplicated per feature. New shared test guards palette pairing and keyboard shortcut readability; storage and retry cases exercise the real affected callsites.
- The default light/dark and preset popover tokens already provide the correct foreground/background pairing; using that same surface fixes all normal foreground/muted child content without per-feature overrides. Shortcut override was the one inverse dependency found and is removed. No additional unsafe custom tooltip/hover-card surfaces found by static callsite search.
- CI still MUST run tests, frontend lint/format/typecheck/build and any desired real-browser light/dark/preset visual contrast check. No pass claim: jsdom does not resolve Tailwind/OKLCH contrast, and no local executable/browser validation was allowed. Source-level result does not substitute for hosted validation.
- Root was sent the three frontend test paths for queue #10. Spec lesson for root's final spec pass: shared tooltip surface and keyboard hints must use the same normal popover palette as custom foreground/muted tooltip children.

### Owned output files
- `web/src/components/ui/tooltip.tsx`
- `web/src/components/ui/kbd.tsx`
- `web/src/components/ui/__tests__/tooltip-contrast.test.tsx` (new)
- `web/src/features/system-info/__tests__/system-instances-panel.test.tsx` (storage case only)
- `web/src/features/usage-logs/components/__tests__/mobile-card.test.tsx` (desktop retry case only; peer changes preserved)

### Post-handoff static correction
- Source review of the installed Base UI TooltipPopup/Root confirmed it does not supply an implicit `role="tooltip"`. Updated newly authored tests to locate visible text and its actual `data-slot="tooltip-content"` surface, matching existing project tests; do not assume a role missing from the implementation. No production accessibility changes or local execution.
- Continuous review re-scan #2 flagged shared lookup/header risks; checked the live test and installed Testing Library implementation before acting. `getNodeText` matches direct text `Copy value` on the popup, and `within(popup)` enumerates descendant `kbd`, so the proposed failure is not present. Retry regression already asserts paired classes on the resolved popup, and the new file has its full header. Sent exact source evidence to reviewer/root; do not replace these behavior queries with implementation-only checks based on a false positive.




