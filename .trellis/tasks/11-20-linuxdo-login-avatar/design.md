# Design

Reuse provider-avatar login pipeline with documented LinuxDO claim. Preserve absent/malformed values and provider-only policy; avatar failures do not block login, no speculative proxy or weakened linking/session assurance.

Use parent design.md section “Credentials, CORS and avatars” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/linuxdo-avatar.md.

Dependencies: Verified provider claim/template fixtures; auth/setting-cache review. Ownership: pC; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
