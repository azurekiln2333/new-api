# Execution

1. Review R14, parent design section “Administration UI and formatting”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: R12 tokens mode; real query verification. Agree concrete file leases for pJ UI / pC backend; record baseline hunks. Root alone stages/commits.
3. Add used_quota server sort allowlist and standalone frontend column, preserving global pagination/filtering/ties and shared money formatting. No page-only sorting or speculative index.
4. Extend suitable existing regression suites: Users request/sort page reset tests and real SQLite/MySQL/PostgreSQL list/search ordering with ties/paging.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
