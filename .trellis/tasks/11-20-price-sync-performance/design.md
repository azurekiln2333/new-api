# Design

Batch parse/apply/serialize maps once; paginated shared preview formats visible rows. Freeze selection/rate/version/payload; save one atomic PATCH and coordinate refresh. Profile before deciding cancellable chunking/worker.

Use parent design.md section “Price synchronization responsiveness” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/price-sync-performance.md.

Dependencies: Independent; preserve atomic pricing API. Ownership: pK or explicit handoff; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
