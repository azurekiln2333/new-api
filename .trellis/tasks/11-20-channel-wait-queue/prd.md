# channel wait queue

## Goal and acceptance

Own R8 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Queue all-busy requests and auto-admit with cancellation-safe bounded FIFO. Upstream concurrency without cap uses bounded backoff gates. Parent design defines proposed limits and dedicated keepalive-only outer state; no Redis connection per waiter, live ticket trimming, duplicated billing or replay after output.

Validation: Deterministic local/real Redis handoff/cancel races, limits, lost notification recovery, pinned single-channel wait, upstream versus generic429, comments-only SSE then single clean result and no double quota.

## Dependencies and boundaries

R6 counting, R7 stream failure; R17 rechecked on wake. Owner: p9 / pK review.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Admission, metrics, failover and waiting” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.
