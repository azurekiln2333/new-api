# linuxdo login avatar

## Goal and acceptance

Own R23 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Reuse provider-avatar login pipeline with documented LinuxDO claim. Preserve absent/malformed values and provider-only policy; avatar failures do not block login, no speculative proxy or weakened linking/session assurance.

Validation: Existing avatar/auth-flow suites for claim/missing/malformed/cache/unchanged and failed login nonmutation; real user-setting DB persistence verification.

## Dependencies and boundaries

Verified provider claim/template fixtures; auth/setting-cache review. Owner: pC.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Credentials, CORS and avatars” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.
