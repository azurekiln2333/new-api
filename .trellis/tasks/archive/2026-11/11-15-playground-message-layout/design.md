# Design

## Boundary and reuse
Visible behavior belongs to Playground message layout and shared response/scroll components. Reuse PlaygroundMessageContent, MessageContent, Response, CodeBlock, Reasoning and Conversation. No replacement chat implementation or new dependencies.
Existing dirty changes add self alignment and left text alignment in message-layout-utils.ts and remove the nested 80-percent user-bubble cap in message-styles.ts. Verify these in a browser.

## Streaming investigation
Trace loading -> streaming -> final -> restored history. Inspect fade spans/markdown wrappers, flex min-content sizing and the actual StickToBottom scroll viewport. Fix causal width/line-breaking at its owner; do not use page-level clipping to hide text.

## Expected files
- Playground lib/message/message-layout-utils.ts and message-styles.ts: user alignment/bubble width.
- Playground message/chat containers and shared ai-elements response/renderer/styles if reproduction proves they own the defect.
- Existing tests for Playground layout/content and response streaming; browser geometry for actual overflow.
- Task evidence and narrow applicable spec note after root cause confirmation.

## Compatibility
Preserve selected left layout, actions/metadata, reasoning/code controls, auto-scroll, persistence and renderer content semantics. No API/storage/dependency/i18n contract changes.

## Publication
Main owns all Git writes. Use fix(playground): ... for the UI fix; push HEAD:main to fork without force after checking remote tip. docker-fork.yml runs CI and creates custom-<commit-date>-<short-sha>. Let that existing flow create tag/release, avoiding duplicate tag-triggered workflows. Observe required jobs/assets; server deployment is outside the configured pipeline.

## Risk and rollback
Static fixtures can miss streaming spans: browser verification must mount React and update content incrementally. Shared renderer changes require related tests. Never reset the shared checkout. Roll back published work with a new revert commit only if needed.
