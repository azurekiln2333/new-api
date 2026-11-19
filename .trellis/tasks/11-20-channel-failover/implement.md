# Execution

1. Review R7, parent design section “Admission, metrics, failover and waiting”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: R4 protocols; early-error contract consumed by R8. Agree concrete file leases for p9 / pK review; record baseline hunks. Root alone stages/commits.
3. Handle default HTTP429 and early HTTP200 failed/error/required-terminal EOF as actual errors. Buffer initialization before attempt output, exclude busy candidates, preserve bounds and partial-output accounting; no mixed answers or relaxed global Written guard.
4. Extend suitable existing regression suites: Extend controller pin/retry and native/converted Responses suites for 429/200/EOF/ping/cancel, pins/strict/exhaustion, no replay after output and exact usage.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
