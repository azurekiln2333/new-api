# channel failover

## Goal and acceptance

Own R7 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Handle default HTTP429 and early HTTP200 failed/error/required-terminal EOF as actual errors. Buffer initialization before attempt output, exclude busy candidates, preserve bounds and partial-output accounting; no mixed answers or relaxed global Written guard.

Validation: Extend controller pin/retry and native/converted Responses suites for 429/200/EOF/ping/cancel, pins/strict/exhaustion, no replay after output and exact usage.

## Dependencies and boundaries

R4 protocols; early-error contract consumed by R8. Owner: p9 / pK review.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Admission, metrics, failover and waiting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
