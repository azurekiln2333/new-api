# Execution

1. Review R5, parent design section “Playground and upstream endpoints”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Verify R4 converted failure paths. Agree concrete file leases for p9 / pJ UI; record baseline hunks. Root alone stages/commits.
3. Reuse conversion-chain/final-format metadata on error logs; distinguish missing historical metadata from truly native requests.
4. Extend suitable existing regression suites: Existing relay-error/log-details suites for converted success/failure, native and absent metadata.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
