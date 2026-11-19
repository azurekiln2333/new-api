# Design

Reuse search DTOs and default-off tools state; extract/deduplicate JSON and SSE citations into persisted Message.sources. Explicit unsupported search fails visibly; existing usage billing is unchanged.

Use parent design.md section “Playground and upstream endpoints” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/code-findings.md, ../11-19-playground-capabilities/research/upstream-verification.md.

Dependencies: R4 protocol integration. Ownership: pK backend / p9 frontend; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
