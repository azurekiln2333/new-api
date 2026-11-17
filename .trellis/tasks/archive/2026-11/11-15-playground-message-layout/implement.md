# Implementation and verification

## Shared-checkout ownership
Four existing Pi agents share D:/My2KProjects/new-api. Preserve others' changes. No worker Git writes, dependency changes, extra agents or task switching. Main owns Git and publication.

| Owner | Responsibility / write scope |
| --- | --- |
| PI-1 w4C:p9 | lib/message/message-layout-utils.ts, message-styles.ts and lib/message/__tests__/message-layout-utils.test.ts |
| PI-2 w4C:pJ | Production chat/message containers and shared renderer/style code needed for streaming overflow; no PI-1 files or tests |
| PI-3 w4C:pC | Relevant component/shared renderer regression tests and browser reproduction; no production or PI-1 test edits |
| PI-4 w4C:pK | Read-only independent diff, reuse and release review |
| Main | Task/spec, consolidated checks, final review, commit/push and CI/CD |

## Steps
1. Confirm baseline; reproduce live vs restored defects before production edits.
2. PI-1 verifies existing bubble changes. PI-2 implements the smallest causal fix after PI-3 records baseline evidence. PI-3 covers incremental updates, completion/remount and passes findings to main.
3. Browser checks at 390px and 1440px: short/multiline user messages, long assistant prose/URLs, incomplete/complete fences and code overflow. Measure document/conversation widths and bubble/text geometry; preserve code scrolling and vertical auto-scroll.
4. Run affected Vitest tests and frontend suite, bun check -b, scoped oxlint/format, bun run build. Record observed commands/results. Remote CI also runs backend gates.
5. PI-4 reviews final diff for scope/reuse, clipping and shared-component regressions.
6. Record the lesson in applicable spec; stage only reviewed paths; conventional commit; check remote ancestry; push HEAD:main.
7. Observe Actions for pushed SHA, remediate observed in-scope failures, confirm automatic tag/image/public release.
8. Archive/journal after acceptance; avoid a second product release from bookkeeping-only pushes.

## Baseline
Four dirty paths: two message layout/style files, one layout test and new playground-message-content.test.tsx. Run 43441224034 passed CI/image but macOS hosted runner allocation failed.

## Dispatch continuation
The four requested Herdr Pi agents were dispatched and confirmed working. Their provider repeatedly timed out; main stopped them and confirmed done status before native agents took over. Native stream_layout_impl owns the PI-2 production scope, stream_layout_tests owns PI-3 tests/browser validation, and layout_review performs read-only review. Main owns PI-1's already verified alignment changes and all Git operations.
