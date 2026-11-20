# Design

Count limited and unlimited attempts with owner-safe existing leases. One authorized bounded batch API reads Redis unexpired leases/server TIME or local snapshot; expose scope/unavailable. One shared visible-set+drawer subscription refreshes every 5s, paused when hidden.

Use parent design.md section “Admission, metrics, failover and waiting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/channel-capacity.md, ../11-19-playground-capabilities/research/channel-ui.md.

Dependencies: Unified lease lifecycle precedes R8; serialize card edits. Ownership: p9 backend / pJ UI; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
