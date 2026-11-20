# Execution

1. Review R18,R19, parent design section “Credentials, CORS and avatars”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: R18 rotation then R19 custom secret separate commits; security/DB/Redis gates. Agree concrete file leases for pC; record baseline hunks. Root alone stages/commits.
3. Rotate same-record credential atomically with ownership/step-up/audit and old-cache revocation, preserving history/settings. Optional custom secret uses validated canonical format/strength/storage/uniqueness; random default. Never log secrets.
4. Extend suitable existing regression suites: Existing key/auth/token tests plus real three-engine unique/rotation and Redis cache races; other-user, CSRF/proof expiry/replay, weak/custom inputs and unchanged permissions.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
