# Execution

1. Review R25, parent design section “Commits, delivery and rollback”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: All R1-R24 checks/commits and fork publication access. Agree concrete file leases for root coordinator; record baseline hunks. Root alone stages/commits.
3. Root-only per-item reviewed commits; exact committed-tree cloud CI then integration push and fork/main release workflow. Verify tag/assets/image digest; no force pushes/guessed target.
4. Validate the full release contract through exact-SHA Actions/image/checksum/asset observations. The user canceled server verification: server access is not a prerequisite; do not inspect the server or claim it pulled the release.
5. Run remaining applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md in cloud CI only; the user's latest instruction excludes further local tests/builds. Record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
