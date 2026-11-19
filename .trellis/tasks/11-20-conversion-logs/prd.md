# conversion logs

## Goal and acceptance

Own R5 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Reuse conversion-chain/final-format metadata on error logs; distinguish missing historical metadata from truly native requests.

Validation: Existing relay-error/log-details suites for converted success/failure, native and absent metadata.

## Dependencies and boundaries

Verify R4 converted failure paths. Owner: p9 / pJ UI.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Playground and upstream endpoints” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
