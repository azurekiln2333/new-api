# Execution

1. Review R4, parent design section “Playground and upstream endpoints”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Shared endpoint resolver precedes R1/R7/R17 integration. Agree concrete file leases for pK backend / pJ settings UI; record baseline hunks. Root alone stages/commits.
3. Replace superseded playground protocol switch with channel supported upstream endpoints. Explicit capabilities constrain global policy; absent configuration preserves legacy behavior. Chat/Messages convert through existing converters and return client-native format; validate passthrough conflicts.
4. Extend suitable existing regression suites: Channel form round-trip, JSON/SSE outbound path/body/header and global/passthrough matrix; real settings persistence matrix; relaykit independent build.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
