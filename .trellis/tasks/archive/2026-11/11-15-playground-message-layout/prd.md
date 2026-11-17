# Playground message alignment and streaming overflow

## Goal
Make live chat as stable and readable as restored history, then commit and publish the verified fix.

## Background
- User reports user messages still appear on the left; the intended behavior is a right-aligned bubble containing left-aligned text.
- Assistant replies can extend past the viewport during generation, while refreshing and restoring the same conversation fixes their layout.
- Four existing dirty files implement part of the user-bubble fix. Preserve and verify them as part of this task.
- HEAD is c678ab5c5 on custom/rc45-stats, tracking fork/main. Four user-opened Pi agents share this checkout.
- User explicitly approved creating this task and fixing/publishing the stated behavior on 2026-11-15.

## Requirements
1. Default chat layout: user bubbles sit at the right of the centered reading column; multiline text inside each bubble stays left aligned. Preserve the existing explicitly selected left layout option.
2. Assistant messages stay inside the centered conversation reading width during incremental streaming, completion, and restored history. Long prose and URLs wrap without document or conversation horizontal scrolling.
3. Wide code remains readable through horizontal scrolling inside its code block without expanding the page. Preserve code controls, vertical scrolling and follow-bottom behavior.
4. Preserve shared components, protected attribution, unrelated changes and message content.
5. Verify regression behavior, use a conventional commit message, push to fork/main, and observe the automatically created release tag and CI/CD outcome.

## Acceptance criteria
- AC1: Real browser geometry confirms short/long user bubbles align right and multiline contents align left at desktop and mobile widths.
- AC2: Mounted components receiving incremental assistant content stay inside their centered reading column without page/conversation horizontal overflow; completion and remount/restored content preserve the layout.
- AC3: Long plain text, URLs, unclosed/closed fenced code and streamed reasoning render without lost content. Wide code can scroll independently.
- AC4: Relevant regressions fail before the production fix and pass after; frontend tests, typecheck, scoped lint/format and production build pass.
- AC5: Independent review has no unresolved actionable findings. Commit and remote SHA match, CI passes, image and automatic version tag/release are verified. Report blockers without claiming server deployment.

## Scope exclusions
No backend, database, authentication, billing, new design system, UI copy, unrelated cleanup or server deployment changes. A minimal CI correction is allowed only if an observed release blocker requires it and is documented.

## Blocking questions
None. User specified alignment, scrolling, delegation and release intent explicitly.
