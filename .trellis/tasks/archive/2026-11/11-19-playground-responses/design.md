# Design

Replace superseded playground protocol switch with channel supported upstream endpoints. Explicit capabilities constrain global policy; absent configuration preserves legacy behavior. Chat/Messages convert through existing converters and return client-native format; validate passthrough conflicts.

Use parent design.md section “Playground and upstream endpoints” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/channel-ui.md, ../11-19-playground-capabilities/research/stream-failover.md, ../11-19-playground-capabilities/research/upstream-verification.md.

Dependencies: Shared endpoint resolver precedes R1/R7/R17 integration. Ownership: pK backend / pJ settings UI; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
