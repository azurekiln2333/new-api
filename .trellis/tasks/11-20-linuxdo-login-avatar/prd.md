# linuxdo login avatar

## Goal and acceptance

Own R23 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Reuse provider-avatar login pipeline with documented LinuxDO claim. Preserve absent/malformed values and provider-only policy; avatar failures do not block login, no speculative proxy or weakened linking/session assurance.

Validation: Existing avatar/auth-flow suites for claim/missing/malformed/cache/unchanged and failed login nonmutation; real user-setting DB persistence verification.

## Dependencies and boundaries

Verified provider claim/template fixtures; auth/setting-cache review. Owner: pC.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Credentials, CORS and avatars” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
