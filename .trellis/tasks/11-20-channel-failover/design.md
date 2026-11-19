# Design

Handle default HTTP429 and early HTTP200 failed/error/required-terminal EOF as actual errors. Buffer initialization before attempt output, exclude busy candidates, preserve bounds and partial-output accounting; no mixed answers or relaxed global Written guard.

Use parent design.md section “Admission, metrics, failover and waiting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/stream-failover.md, ../11-19-playground-capabilities/research/channel-failover.md.

Dependencies: R4 protocols; early-error contract consumed by R8. Ownership: p9 / pK review; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
