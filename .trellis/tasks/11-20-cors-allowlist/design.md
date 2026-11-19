# Design

Replace wildcard-with-credentials with explicit validated origins as requested. Keep session origin checks distinct, required methods/headers/Vary and no-Origin CLI usage. Do not infer permission from forwarded Host or unrelated OAuth origins.

Use parent design.md section “Credentials, CORS and avatars” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/keys-cors.md.

Dependencies: Actual public site origin pending; fail closed without guessing. Ownership: pC; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.
