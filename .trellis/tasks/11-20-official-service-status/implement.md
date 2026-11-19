# Execution

1. Review R21, parent design section “Official status and local fingerprint detection”, applicable Trellis specs and skills. Product implementation waits for final review and task activation.
2. Dependency gate: Sidebar handoff R15/R16 and official feed fixtures. Agree concrete file leases for pK / pJ UI; record baseline hunks. Root alone stages/commits.
3. Themed official-status page with cached bounded server fetches to explicit vendor sources and timestamps/stale/errors, exact requested disclaimer. No tcping internal API or modification to existing /api/status contract.
4. Extend suitable existing regression suites: Official feed/timeout/cache/stale fixtures, no arbitrary URL fetch, theme/mobile/i18n/source/disclaimer rendering.
5. Run applicable frontend/backend/i18n/security/real DB/Redis/independent-module checks from parent implement.md; record actual commands/results, not inferred passes.
6. Pi self-check, cross-agent review, then root independent review. Root commits each owning R ID separately including translations/tests and records SHA/evidence. Hand off shared files before another worker edits.
7. Archive only after all acceptance passes; central R25 owns publication/deployment.
