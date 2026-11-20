# Design

Compose exact and compact shared formatters with valid locales; audit all textual token surfaces and exact chart tooltips. Preserve zero/missing and raw numeric sorting; currency remains separate.

Use parent design.md section “Administration UI and formatting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/admin-ui.md.

Dependencies: Shared display before R14 tokens mode; serialize channel/log edits. Ownership: pJ / pC locales; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
