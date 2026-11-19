# Design

Reuse collapsible navigation for four logs and invitation/redemption; preserve role/module/direct-route contracts. Access configuration landing page reuses presets and existing deliberate key/connect confirmation; no credential transfer on render.

Use parent design.md section “Administration UI and formatting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/admin-ui.md.

Dependencies: Separate commits; sidebar/locale serialized; R21 later. Ownership: pJ / pC permissions review; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
