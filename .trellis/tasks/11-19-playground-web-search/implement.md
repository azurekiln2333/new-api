# Execution

1. Review R1, parent design section “Playground and upstream endpoints”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: R4 protocol integration. Agree concrete file leases for pK backend / p9 frontend; record baseline hunks. Root alone stages/commits.
3. Reuse search DTOs and default-off tools state; extract/deduplicate JSON and SSE citations into persisted Message.sources. Explicit unsupported search fails visibly; existing usage billing is unchanged.
4. Extend suitable existing regression suites: Payload, source extraction/storage, unsupported search and route-level Responses conversion tests.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
