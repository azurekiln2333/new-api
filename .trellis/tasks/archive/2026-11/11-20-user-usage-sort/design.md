# Design

Add used_quota server sort allowlist and standalone frontend column, preserving global pagination/filtering/ties and shared money formatting. No page-only sorting or speculative index.

Use parent design.md section “Administration UI and formatting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/admin-ui.md.

Dependencies: R12 tokens mode; real query verification. Ownership: pJ UI / pC backend; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
