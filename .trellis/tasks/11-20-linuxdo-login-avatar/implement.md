# Execution

1. Review R23, parent design section “Credentials, CORS and avatars”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Verified provider claim/template fixtures; auth/setting-cache review. Agree concrete file leases for pC; record baseline hunks. Root alone stages/commits.
3. Reuse provider-avatar login pipeline with documented LinuxDO claim. Preserve absent/malformed values and provider-only policy; avatar failures do not block login, no speculative proxy or weakened linking/session assurance.
4. Extend suitable existing regression suites: Existing avatar/auth-flow suites for claim/missing/malformed/cache/unchanged and failed login nonmutation; real user-setting DB persistence verification.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
