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

## Approved follow-up: 2026-11-20

The user explicitly rejects the extra 2FA prompt on API-key reset. Rotation now
uses the authenticated browser session plus confirmation, server-side ownership,
origin/CSRF protection, rate limiting, audit and atomic cache revocation, without
an additional security proof. PAT access remains denied. Custom creation retains
its existing verification policy. This supersedes the step-up requirement above
for rotation only; it does not assert full ASVS compliance.

The replacement-key dialog must fit the viewport, grow with its content, wrap a
long credential and keep copying accessible, without horizontal scrolling or
truncation. No further local test/build/lint/browser execution is authorized;
regression verification runs in GitHub CI. Server verification is excluded.
