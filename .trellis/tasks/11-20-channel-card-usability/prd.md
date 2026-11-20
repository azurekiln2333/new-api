# channel card usability

## Approved layout correction: 2026-11-20

Use two metric columns: left contains Used / Remaining and Used tokens; right
contains Response, Last tested and Concurrency. A separate row below contains
Priority and Weight side by side. Remove the purple token badge/frame. Card token
values stay on one line and use compact localized units (万/亿 for Chinese), while
hover and channel details show the exact value with the full parenthesized compact
explanation. The last-tested date/time must be fully readable without truncation.
Preserve privacy masking, tag aggregate behavior and shared visible-set polling.
Verification is cloud-only under the user's current instruction.

## Goal and acceptance

Own R9,R10,R11 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Guard card activation from nested controls. Restore paired usage, priority/weight and response rows. Remove group badges from cards and show pruned group-model access in details; missing group_models entry means all models.

Validation: Existing priority/details interaction suites and drawer model restriction cases; desktop/mobile visual verification.

## Dependencies and boundaries

Separate R9,R10,R11 commits; coordinate R6 slot/R12 formatting. Owner: pJ.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Administration UI and formatting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
