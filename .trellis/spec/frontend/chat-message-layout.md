# Chat message layout contract

## Ownership and behavior
- `PlaygroundChat` owns the centered reading column and uses `Conversation` / `StickToBottom.Content` for scrolling. The actual scroll viewport is the outer Content node, not its inner `className` node; `scrollClassName` targets the viewport.
- `getMessageAlignmentClass` positions a message content column within a parent flex column. Default user bubbles sit at the right; text inside remains left aligned. Preserve the explicitly selected left layout.
- Bound every content child that can acquire intrinsic width, including the message bubble, reasoning and raw-code display. A max-width on the outer reading column alone does not constrain auto-sized descendants under `items-start`.
- Long prose, links and inline code must wrap without clipping. Fenced code keeps its whitespace and scrolls within its existing CodeBlock/CodeMirror viewport. Do not disable wrapping for all `code` descendants to preserve fenced code.
- Do not add page-level overflow clipping as a substitute for bounding the content.

## Regression checks
Use the real `PlaygroundChat` and a browser with compiled current CSS. Feed incremental content through mounted React components; measure both streaming and completed/remounted states at 390px and 1440px.

Check document and actual conversation viewport `scrollWidth <= clientWidth`, message bounds within the reading column, user bubble right-edge alignment, and computed left text alignment. Check long URLs, inline code, unclosed/closed fences and reasoning. Wide fenced code must have a usable independent horizontal scroll range.

jsdom class assertions can protect a deliberate layout contract, but cannot prove browser geometry. A static HTML snapshot of completed content does not exercise the live path. Negative controls should use temporary source overrides or fixtures, never reset a shared checkout.

## Review mistakes to avoid
- `cn` uses `tailwind-merge`: inspect the final DOM/classes before reasoning about competing utility specificity. The overridden user `max-w-[80%]` class may already have been removed before CSS applies.
- The stream fade span is inline and its animation only changes opacity. Do not claim it changes width without an observed reproduction.
- A user's live-only observation does not prove every overflow is streaming-only. Record which fixtures fail in live, final and restored states separately.
