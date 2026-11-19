# Execution

1. Review R6, parent design section “Admission, metrics, failover and waiting”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Unified lease lifecycle precedes R8; serialize card edits. Agree concrete file leases for p9 backend / pJ UI; record baseline hunks. Root alone stages/commits.
3. Count limited and unlimited attempts with owner-safe existing leases. One authorized bounded batch API reads Redis unexpired leases/server TIME or local snapshot; expose scope/unavailable. One shared visible-set+drawer subscription refreshes every 5s, paused when hidden.
4. Extend suitable existing regression suites: Existing local/real Redis lifecycle, expiry, detached-owner tests; API permissions and null versus zero; shared list/detail fetch and polling tests.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
