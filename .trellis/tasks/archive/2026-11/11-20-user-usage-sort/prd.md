# user usage sort

## Goal and acceptance

Own R14 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Add used_quota server sort allowlist and standalone frontend column, preserving global pagination/filtering/ties and shared money formatting. No page-only sorting or speculative index.

Validation: Users request/sort page reset tests and real SQLite/MySQL/PostgreSQL list/search ordering with ties/paging.

## Dependencies and boundaries

R12 tokens mode; real query verification. Owner: pJ UI / pC backend.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Administration UI and formatting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.
