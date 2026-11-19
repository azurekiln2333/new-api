# Design

Reuse conversion-chain/final-format metadata on error logs; distinguish missing historical metadata from truly native requests.

Use parent design.md section “Playground and upstream endpoints” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/channel-failover.md.

Dependencies: Verify R4 converted failure paths. Ownership: p9 / pJ UI; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
