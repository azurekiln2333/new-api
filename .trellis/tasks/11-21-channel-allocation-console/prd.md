# Channel allocation and diagnostic console

Implementation approved as part of the parent task. This child owns all frontend work previously mentioned in the three backend child plans.

## Requirements

- Add repeated upstream static Keys as separate allocations, independently public or user-bound.
- Multi-key management shows stable entries, owner, allowed models, quota limit, used/reserved/remaining, edit limit, reset used and explicit dedup preview/apply. Dedup must describe conserved usage and skipped active work.
- Channel management allows a manually configured local budget without an upstream balance endpoint. Keep upstream balance and local billed budget visually distinct.
- Preserve normal wallet/subscription and token charging. UI must not imply allocation quota provides free user credit.
- Bound models are visible through existing user model views when backend eligibility permits them.
- Existing request-log dialog loads original prompt/messages on demand for authorized administrators on successes and failures. Display roles, content, loading/error/unavailable/truncated states; reuse copy controls.
- Show actual processing node in log list, mobile view and details; distinguish asynchronous submit node.
- Implement all new user-facing and backend-message strings in en, zh, zh-TW, fr, ru, ja and vi.

## Acceptance

Use existing business components and formatting helpers. Stable IDs and versions survive edits. Mutations refresh related queries and handle conflicts. No credential/content leaks into unauthorized views or log lists. Existing legacy records render safely. Focused UI regression tests, typecheck, lint, locale checks and build must be run or explicitly reported blocked.
