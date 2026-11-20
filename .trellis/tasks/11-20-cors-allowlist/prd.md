# cors allowlist

## Goal and acceptance

Own R20 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Replace wildcard-with-credentials with explicit validated origins as requested. Keep session origin checks distinct, required methods/headers/Vary and no-Origin CLI usage. Do not infer permission from forwarded Host or unrelated OAuth origins.

Validation: Allowed/denied/null/malformed actual/preflight, credentials/no-Origin/PATCH/Vary; login/playground browser integration; deployed preflight after configured origin.

## Dependencies and boundaries

Actual public site origin pending; fail closed without guessing. Owner: pC.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Credentials, CORS and avatars” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.
