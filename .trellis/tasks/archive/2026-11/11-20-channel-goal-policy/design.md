# Design

Parse bounded canonical turn metadata for recognized goal-triggered turns and compatible header; WS per-message precedence. Reuse all channel selection constraints; alternate or local all-denied error. No arbitrary text/tools heuristics or 100% detection claim.

Use parent design.md section “Goal-aware candidate restrictions” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/goal-routing.md.

Dependencies: R4 shared settings; R8 eligibility checks; official versioned fixtures. Ownership: p9 / pJ settings; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
