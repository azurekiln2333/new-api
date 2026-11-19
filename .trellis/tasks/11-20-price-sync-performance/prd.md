# price sync performance

## Goal and acceptance

Own R13 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Batch parse/apply/serialize maps once; paginated shared preview formats visible rows. Freeze selection/rate/version/payload; save one atomic PATCH and coordinate refresh. Profile before deciding cancellable chunking/worker.

Validation: Existing pricing-sync/pricing suites for multi-page full save, mixed modes/zeros/overrides/safe keys/frozen rates/version conflict; browser profiles 1000/5000 models at both entry points.

## Dependencies and boundaries

Independent; preserve atomic pricing API. Owner: pK or explicit handoff.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Price synchronization responsiveness” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
