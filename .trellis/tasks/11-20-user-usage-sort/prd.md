# user usage sort

## Goal and acceptance

Own R14 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Add used_quota server sort allowlist and standalone frontend column, preserving global pagination/filtering/ties and shared money formatting. No page-only sorting or speculative index.

Validation: Users request/sort page reset tests and real SQLite/MySQL/PostgreSQL list/search ordering with ties/paging.

## Dependencies and boundaries

R12 tokens mode; real query verification. Owner: pJ UI / pC backend.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Administration UI and formatting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
