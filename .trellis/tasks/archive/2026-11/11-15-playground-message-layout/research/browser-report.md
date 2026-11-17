# Browser verification

- Test fixture mounts the real `PlaygroundChat`, `Conversation` and `PlaygroundMessageContent`; updates are React state updates, followed by completion and a fresh remount. Chrome headless viewport widths: 390 and 1440 px.
- `browser-current.json`: 60 snapshots (prose, URL, inline code, unclosed fence, reasoning URL, reasoning fenced code; three incremental states, completion, restored state). No browser exceptions; document and actual conversation viewport horizontal overflow are zero throughout.
- Assistant width is bounded to 338 px on mobile and 768 px on desktop. User bubbles end at the reading column edge; computed text alignment is `left`.
- Reasoning code confirms the existing grid does not need another sizing constraint: inner code client width 317/747 px, scroll width 2038 px, and actual `scrollLeft = 100` in streaming, complete and restored states.
- `browser-390.png` and `browser-1440.png` show multiline user bubble placement, a streamed long URL and an independent code scrollbar. Screenshots were visually reviewed; no page scrollbar or cut-off prose.
- Baseline URL reply width was approximately 3435 px in a 338 px column; code width was approximately 2039 px. Raw before/after geometry is retained alongside this report.
- Fixture build: `D:/AppData/Local/Temp/new-api-playground-layout-tools/bun-windows-x64/bun.exe D:/AppData/Local/Temp/pi3-fixture/build.mjs current` succeeded on 2026-11-16. It uses the existing generated `web/dist/static/css/index.fec5a6e011.css`; computed overflow-wrap is anywhere. Main owns the final full production build to reconfirm generated CSS.
- Permanent test file: `web/src/features/playground/components/message/__tests__/playground-message-content.test.tsx`. Six tests passed with `bun run test src/features/playground/components/message/__tests__/playground-message-content.test.tsx`, including live/final URL and expanded Reasoning width/content contracts. Scoped oxlint and oxfmt passed after the final test addition.
- Limits: geometry is verified in a real component fixture, not an authenticated live upstream chat. No deployment assertions are made here.
