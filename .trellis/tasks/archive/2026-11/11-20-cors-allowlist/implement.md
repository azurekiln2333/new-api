# Execution

1. Review R20, parent design section “Credentials, CORS and avatars”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Actual public site origin pending; fail closed without guessing. Agree concrete file leases for pC; record baseline hunks. Root alone stages/commits.
3. Replace wildcard-with-credentials with explicit validated origins as requested. Keep session origin checks distinct, required methods/headers/Vary and no-Origin CLI usage. Do not infer permission from forwarded Host or unrelated OAuth origins.
4. Extend suitable existing regression suites: Allowed/denied/null/malformed actual/preflight, credentials/no-Origin/PATCH/Vary; login/playground browser integration; deployed preflight after configured origin.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
