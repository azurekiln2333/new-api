# Design

Rotate same-record credential atomically with ownership/step-up/audit and old-cache revocation, preserving history/settings. Optional custom secret uses validated canonical format/strength/storage/uniqueness; random default. Never log secrets.

Use parent design.md section “Credentials, CORS and avatars” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/keys-cors.md.

Dependencies: R18 rotation then R19 custom secret separate commits; security/DB/Redis gates. Ownership: pC; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
