# Design

Guard card activation from nested controls. Restore paired usage, priority/weight and response rows. Remove group badges from cards and show pruned group-model access in details; missing group_models entry means all models.

Use parent design.md section “Administration UI and formatting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/channel-ui.md, ../11-19-playground-capabilities/research/admin-ui.md.

Dependencies: Separate R9,R10,R11 commits; coordinate R6 slot/R12 formatting. Ownership: pJ; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
