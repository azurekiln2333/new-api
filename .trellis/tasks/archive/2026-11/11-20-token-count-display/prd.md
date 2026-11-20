# token count display

## Goal and acceptance

Own R12 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Compose exact and compact shared formatters with valid locales; audit all textual token surfaces and exact chart tooltips. Preserve zero/missing and raw numeric sorting; currency remains separate.

Validation: Language switching, small/large/zero/missing counts; channel/log/dashboard/rankings/playground relevant suites and responsive layout.

## Dependencies and boundaries

Shared display before R14 tokens mode; serialize channel/log edits. Owner: pJ / pC locales.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Administration UI and formatting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.

## User correction — November 20, 2026

The new exact-plus-abbreviation formatting applies only to channel cards. Log lists must retain the original plain input/output/cache counts and dense row height. Mobile compact rows retain K/M summaries and truncation; expanded cards and detail breakdowns retain exact plain counts. No duplicate international/localized abbreviations in log token cells. This instruction supersedes the earlier audit-all-surfaces scope. Verification stays in GitHub CI; no local tests, lint, builds or browser checks.
