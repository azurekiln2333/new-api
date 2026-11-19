# Design

Themed official-status page with cached bounded server fetches to explicit vendor sources and timestamps/stale/errors, exact requested disclaimer. No tcping internal API or modification to existing /api/status contract.

Use parent design.md section “Official status and local fingerprint detection” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/status-fingerprint.md.

Dependencies: Sidebar handoff R15/R16 and official feed fixtures. Ownership: pK / pJ UI; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
