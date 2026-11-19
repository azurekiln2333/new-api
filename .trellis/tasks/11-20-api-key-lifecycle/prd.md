# api key lifecycle

## Goal and acceptance

Own R18,R19 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Rotate same-record credential atomically with ownership/step-up/audit and old-cache revocation, preserving history/settings. Optional custom secret uses validated canonical format/strength/storage/uniqueness; random default. Never log secrets.

Validation: Existing key/auth/token tests plus real three-engine unique/rotation and Redis cache races; other-user, CSRF/proof expiry/replay, weak/custom inputs and unchanged permissions.

## Dependencies and boundaries

R18 rotation then R19 custom secret separate commits; security/DB/Redis gates. Owner: pC.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Credentials, CORS and avatars” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
