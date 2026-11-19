# channel concurrency

## Goal and acceptance

Own R6 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Count limited and unlimited attempts with owner-safe existing leases. One authorized bounded batch API reads Redis unexpired leases/server TIME or local snapshot; expose scope/unavailable. One shared visible-set+drawer subscription refreshes every 5s, paused when hidden.

Validation: Existing local/real Redis lifecycle, expiry, detached-owner tests; API permissions and null versus zero; shared list/detail fetch and polling tests.

## Dependencies and boundaries

Unified lease lifecycle precedes R8; serialize card edits. Owner: p9 backend / pJ UI.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Admission, metrics, failover and waiting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
