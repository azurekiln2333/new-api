# channel failover

## Goal and acceptance

Own R7 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Handle default HTTP429 and early HTTP200 failed/error/required-terminal EOF as actual errors. Buffer initialization before attempt output, exclude busy candidates, preserve bounds and partial-output accounting; no mixed answers or relaxed global Written guard.

Validation: Extend controller pin/retry and native/converted Responses suites for 429/200/EOF/ping/cancel, pins/strict/exhaustion, no replay after output and exact usage.

## Dependencies and boundaries

R4 protocols; early-error contract consumed by R8. Owner: p9 / pK review.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Admission, metrics, failover and waiting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Implementation in progress. Product publication is authorized. All further automated validation runs in GitHub CI; the user prohibited local tests, builds, lint and browser verification, and canceled server verification.

## Approved follow-up: unavailable upstream models (2026-11-20)

The user reported `404: Model "gpt-6-astra" is not supported by any configured account in this group` and requested automatic channel failover and remembering that the failed channel no longer serves that model.

- Recognize explicit upstream model-unavailable errors, including the reported envelope, separately from an ordinary endpoint/path 404, authentication failure, capacity rejection or invalid request.
- Retry another eligible channel before output, subject to cancellation, explicit pins, strict-session requirements and bounded attempts. Record the actual reason when routing cannot continue.
- Persist an unavailable marker scoped to the channel, attempted model and affected credential. Subsequent ordinary requests skip that unavailable combination. Preserve other models and other credentials that can serve the same model, including user-bound credentials.
- Keep configured model names intact and expose the unavailable state in channel details. Administrators can restore/recheck it; a changed credential or mapping must not inherit an obsolete observation.
- Respect model aliases and group permissions, cache invalidation across instances, concurrent configuration edits and secret-free diagnostics. A model-output sentence or a prompt containing the error text must never alter channel capabilities.
- Cover positive recognition, false positives, alternate-channel success, no remaining channel, persistence/cache refresh, mapping and multi-key isolation in focused existing regression suites. Real SQLite/MySQL/PostgreSQL verification runs in cloud CI.
- Commit the model-unavailability follow-up independently for rollback.

## Resource-aware execution

After the user reported memory exhaustion, run at most two worker executors at once. Four pi panes receive responsibilities in two batches. Preserve all existing work and do not launch local validation processes.
