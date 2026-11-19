# Execution

1. Review R8, parent design section “Admission, metrics, failover and waiting”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: R6 counting, R7 stream failure; R17 rechecked on wake. Agree concrete file leases for p9 / pK review; record baseline hunks. Root alone stages/commits.
3. Queue all-busy requests and auto-admit with cancellation-safe bounded FIFO. Upstream concurrency without cap uses bounded backoff gates. Parent design defines proposed limits and dedicated keepalive-only outer state; no Redis connection per waiter, live ticket trimming, duplicated billing or replay after output.
4. Extend suitable existing regression suites: Deterministic local/real Redis handoff/cancel races, limits, lost notification recovery, pinned single-channel wait, upstream versus generic429, comments-only SSE then single clean result and no double quota.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
