# Queue when every eligible channel is saturated

Research only. No product files edited; no commits (user wants one commit per requirement, root owns commits).

## Current behavior (facts)

- Candidate selection with capacity: `controller/relay.go:399-420` (`getChannelWithCapacity`). It loops candidates, calls `service.AcquireRequestChannelCapacity`, and on failure calls `service.RetryChannelAdmission`; when the loop cannot select, it returns the last `*types.NewAPIError` (429 saturated / 503 unavailable) and the request fails immediately.
- `RetryChannelAdmission` (`service/channel_capacity.go:236-268`) only fails over when: the error is a capacity error, `ErrorCodeChannelCapacitySaturated`, client context alive, not pinned, no strict session, and `!c.Writer.Written()`. Otherwise the decision is `stop` and the client gets the error.
- Capacity errors are deliberately excluded from channel-failure bookkeeping, auto-ban, and retry budgets: `service/relay_error.go:22-24` (`IsChannelFailure`), `:46-48` (`DecideRelayRetry` returns `stop`/`capacity`), `:111-113` (`ProcessChannelError` early return). `RetryChannelAdmission` also restores the auto-group snapshot so a busy candidate does not consume the retry that selected its group.
- Default retry status ranges include `409-499` (`setting/operation_setting/status_code_ranges.go:21-27`), so an upstream `429` today is a normal retryable failure that fails over to another channel and consumes the retry budget.
- No upstream "concurrency error" predicate exists anywhere: grep for concurrency finds only channel-test settings. `429` is only a status code; "HTTP 200 with concurrency error body" has no recognizer at all.
- All waiting must happen before response bytes: the stream/keepalive path starts per attempt (`controller/relay.go:229-232`) and `c.Writer.Written()` is the existing precommit guard. Billing pre-consume also starts per attempt (`controller/relay.go:233-241`, `billingPrepared`).

## Queue design (extends the limiter work)

### Where it plugs in

Replace the "all candidates busy → return error" exit of `getChannelWithCapacity` with "park the request, then re-run candidate selection". The gate stays before `PrepareRequestBilling` and before any writer output, so nothing is committed while waiting.

### One admission primitive, two scopes

Reuse the unified counting lease from `research/channel-capacity.md`: every admitted attempt (limited or unlimited) takes a slot; the configured limit only decides whether admission can be refused. The queue is a second consumer of the same slot accounting, not a parallel counter.

- Local (`!common.RedisEnabled`): per-channel FIFO waiter list guarded by the existing `localChannelCapacity` mutex, plus a `sync.Cond` (or a one-shot `chan struct{}` per waiter). `Release` hands the freed slot to exactly one waiter (`Signal`), never broadcasts — handoff, not wake-all, so no herd and no re-check storm.
- Redis: per-channel FIFO ticket list (`new-api:channel-capacity-queue:<id>`), `RPUSH` ticket on enqueue, `LPOP` on a released slot. Waiters block with a bounded server-side `BLPOP`/`BLMOVE` timeout (~1 s) and re-check `ctx` and their own position each wake; that is event-driven with a 1 s ceiling, not a spin loop. One permit is pushed per release, so exactly one waiter proceeds.
- Scope must be stated in the API/UI: local FIFO is per process; Redis FIFO is cluster-wide. Mixed deployments are not supported (matching admission, which never degrades Redis to local: `service/channel_capacity.go:165-169`).

### Bounded memory and no unbounded wait

- Per-channel max waiters and max wait duration, both configurable; exceeding either returns the existing saturated error (`i18n.MsgChannelCapacitySaturated`, 429) instead of queueing. Reuse the current error mapping rather than adding new user-facing codes.
- Waiter count is capped before any allocation; the ticket list must be trimmed (`LTRIM`) so an abandoned Redis queue cannot grow.
- No unbounded goroutine per waiter beyond the request's own goroutine; the waiter blocks in place.

### Cancellation and timeout

- Client cancel: `ctx.Done()` removes the ticket (`LREM` by ticket id in Redis, list unlink locally) and returns `client_cancelled` without recording a channel failure. Race to handle: the cancel may arrive after a permit was handed over — the waiter must then release the permit back to the next waiter, never leak it.
- Queue timeout: return 429 saturated. No partial SSE, no billing.
- Upstream first-byte timeout stays a per-attempt concern; queue time must not be counted against it (it starts inside the attempt, `controller/relay.go:224-227`).
- Request-level deadline: the queue must respect the client context; a caller-supplied deadline shorter than the queue timeout wins.

### Unknown upstream capacity (no configured cap)

A queue needs a capacity to queue against. With no `MaxConcurrentRequests`, there is no limit, so the queue cannot be "waiting for a slot" — it can only be "waiting out an upstream signal". Two distinct mechanisms, and the product must choose:

1. **Strict queue (configured limit only)**: park until a local/Redis lease is released. Sound, fair, bounded.
2. **Signal-driven backoff (upstream 429 / 200-with-error, no configured cap)**: on a recognized concurrency rejection, park the request for a jittered exponential backoff window and retry the **same** channel, with a per-channel adaptive in-flight estimate (decrement on concurrency rejection, increment on success) used only as a hint. This is not FIFO fairness; it is retry-with-backoff. It also needs a recognizer that does not exist today: HTTP 429 is currently indistinguishable from rate-limit/quota 429, and a 200-with-error body requires body inspection that would have to happen before the response is committed.

Recommendation to carry into design: implement (1) first, and expose (2) only with an explicit per-channel opt-in and a documented "best effort, not fair" semantic. Do not present backoff as a queue.

### Failover interaction

- While waiting, the candidate set may change (channels enabled/disabled, affinity, auto-group rotation). Re-run selection after each wake instead of pinning the candidate list captured before the wait.
- Busy candidates must keep being excluded without consuming retry budget (existing invariant, `service/channel_capacity.go:239-268`); the queue must not advance `RetryParam`.
- If every candidate is busy and one is pinned/strict-session bound: those cannot fail over, so they wait on that same channel only. Pins must never be converted into failover by the queue (`service/channel_select.go:19` `GetChannelConstraints`, `RetryChannelAdmission` predicate).

## Billing safety (per `.agents/rules/billing.md`, read in full)

- Waiting happens before `PrepareRequestBilling`, so no quota is pre-consumed while parked and there is no double pre-consume: one attempt still yields exactly one pre-consume and one settlement (`controller/relay.go:233-241`).
- Capacity/queue outcomes must remain outside `IsChannelFailure` and auto-ban (`service/relay_error.go:22-24`, `:111-113`); a queue timeout is not a channel fault.
- No new billing multiplier is introduced, so no new bound is required; the existing max-token and count validators are untouched.
- If a queue metric/counter is added, it must not feed quota math; tool/usage settlement stays with the actual upstream attempt.

## Pitfalls

1. `429` is retryable by default (`409-499`), so without an explicit interception the queue never triggers for upstream concurrency signals — failover wins first.
2. Redis blocking pops hold a connection; the pool size becomes a queue-throughput ceiling. Bound queue length accordingly.
3. Fairness across processes exists only in Redis mode; local mode FIFO is per instance and can starve a replica.
4. Lease TTL (30 s, renew 10 s) means a crashed holder's slot returns late; waiters must tolerate a late handoff and re-check liveness rather than assuming a permit implies a live attempt.
5. Cancel-vs-handoff race must return the permit; otherwise capacity leaks permanently (local mode never self-heals).
6. Waiting clients hold request state (body storage, plugins); the wait bound must be well under any proxy idle timeout, and SSE clients see silence unless a comment keepalive is emitted (see open choices).
7. Policy events (`state.AddEvent`) must record queue time and outcome, otherwise saturation looks like an upstream failure in logs.
8. A queue must not resurrect a request after the client is gone (`c.Request.Context().Err()`), and must not write a second error after a response started.

## Focused tests

- `service/channel_capacity_test.go`: FIFO handoff order; exactly one waiter wakes per release; waiter limit returns saturated; queue timeout; cancel removes ticket and does not leak a permit; cancel-after-handoff returns the permit to the next waiter.
- Controller test in `controller/relay_stream_test.go` or `controller/channel_pin_retry_test.go`: all candidates busy → request parks, one release → request proceeds on the freed channel; pinned/strict-session request waits on the same channel and never fails over; queue timeout yields 429 with no channel-failure record and no retry-budget consumption; no SSE bytes and no pre-consume while parked.
- Redis parity: same scenarios with `RedisEnabled` (ticket list, LREM on cancel, LTRIM bound).

## Unresolved product choices

1. Default queue enablement, max waiters, and max wait duration; per-channel override or global setting?
2. SSE keepalive while queued (comment/ping every N seconds) or silent wait?
3. Whether upstream concurrency signals without a configured cap are in scope now, or deferred behind an opt-in backoff mode.
4. Queue ordering: strict FIFO vs priority for pinned/strict-session requests (they have no alternative).
5. What the client sees on queue timeout: existing 429 saturated message, or a distinct "waited too long" message.
6. Whether queue position/ETA is surfaced anywhere (logs only, or response headers).

---

# Addendum A — proposed root decisions, waiting vs attempt commit, keepalive-only state

Research only; no product edits. Supersedes the rejected parts of the sections above.

## Proposed decisions for final user review (not implementation approval)

- Queue is enabled for **both** triggers: a local configured cap **and** a recognized upstream concurrency rejection without a cap. Upstream-concurrency mode is **not** deferred behind an opt-in.
- Global bounded defaults: **300 s max wait**, **1000 queued** per coordinator (per instance in local mode, per cluster in Redis mode), with **per-user bounds inside that**.
- **FIFO among eligible waiters.** Pins and strict sessions stay eligible and wait on the same channel; they are never failed over.
- **Protocol-safe SSE comments only** while waiting; no response IDs, roles, usage, or model output may be delivered while queued.
- **Generic quota 429 stays error/failover**, never a permanent queue. Only positively recognized concurrency rejections enter the queue.
- **Never LTRIM away live tickets.** The cap is enforced at enqueue; trimming may only remove stale/expired entries (by score/TTL), never members of active waiters.
- **No per-waiter Redis connection starvation.** Use shared coordinator notifications plus a timed recovery sweep; waiters must not each hold a blocking Redis connection from the shared pool indefinitely.
- **No indefinite uncapped busy loop.** Every wake path is notification-driven or one bounded timed tick with backoff.
- Keep the conventional `c.Writer.Written()` guard. The only exception is a new, audited, dedicated **keepalive-only outer relay state** that may allow a new upstream attempt after keepalive bytes were written — and only when the response protocol is exactly the same and no attempt-identifying or model output has been delivered.
- Client UI **reconnect label cannot be forced** by the server; no resumption is promised.

## Verified keepalive facts and the evidence gap

- `streamKeepAliveWriter` wraps `c.Writer`; its `Write`/`WriteString`/`WriteHeader` stop the pinger and then hand off to the inner writer (`relay/helper/common.go:21-44`).
- Its `Written()` **delegates to the inner writer** (`relay/helper/common.go:75-79`). There is no comments-only bit today.
- Pings are written as SSE comment bytes `": PING\n\n"` plus flush (`relay/helper/common.go:269-282`), using a copied context whose `Writer` is the **pre-wrap** writer (`:108-111`). Those bytes therefore reach the underlying writer.
- Consequence: after a ping, `c.Writer.Written()` (wrapper → inner) is expected to be true, so the existing guards would stop a new attempt: `service/relay_error.go:59` (`response_started` stop) and `service/channel_capacity.go:247` (`!c.Writer.Written()` condition). The conventional guard **cannot simply be removed**; this is exactly why the audited outer state is required.
- Evidence gap to resolve before implementation: `relay/channel/api_request_test.go:116` asserts "a keepalive must not prohibit channel failover" against a first-byte timeout. Given the delegation above, verify whether that test simply never emits a ping within its timeout window. Flagged, not assumed.
- Keepalive is currently started only inside an attempt (Claude format, ping enabled, no first-byte timeout): `controller/relay.go:229-232`, plus `relay/channel/api_request.go:568` and `relay/channel/providerplugin/stream.go:102`. Queue-time keepalive is new placement, not reuse of an existing outer mechanism.
- `: PING\n\n` is a valid SSE comment for the OpenAI, Anthropic, and Gemini SSE dialects the relay emits, so it is protocol-safe for all three while waiting.

## New outer relay state (above the per-attempt flow)

| State | Meaning |
| --- | --- |
| `SELECTING` | Candidate selection + capacity admission; no bytes; billing not prepared. Existing behavior. |
| `WAITING` | Parked FIFO waiter. No pre-consume, no upstream request. Client cancel removes the ticket; max wait timeout returns the existing saturated error. |
| `KEEPALIVE_ONLY` | Only protocol SSE comments have been delivered. New state; explicitly records that no attempt-identifying data exists. |
| `ATTEMPT_COMMITTED` | Any non-comment byte: response id, role/event payload, usage, first model token, or a protocol/response-format change. Conventional `Writer.Written()` semantics resume and no requeue is possible. |
| `TERMINAL` | Success or error response completed. |

Transitions:

1. `SELECTING → WAITING` when every eligible candidate is busy. The request is not disconnected.
2. `WAITING → KEEPALIVE_ONLY` on the first wait-time ping. A ping must not be sent unless the request may legally start a later attempt.
3. `WAITING|KEEPALIVE_ONLY → SELECTING` when the coordinator grants a slot (automatic admission on release) or the timed recovery tick fires. After a grant, re-run candidate selection; do not reuse a stale candidate list.
4. `KEEPALIVE_ONLY → ATTEMPT_COMMITTED` on the first non-comment byte. From then on the existing guards apply unchanged.
5. `[KEEPALIVE_ONLY] → TERMINAL` on max wait, cancel, or an unrecoverable selection error.

Hard rules:

- A new upstream attempt after keepalive bytes is allowed **only** from `KEEPALIVE_ONLY`, only when the response protocol (client `RelayFormat`) is byte-for-byte the same, and only if the state still says comments-only. A protocol switch is never allowed.
- The two existing guards (`service/relay_error.go:59`, `service/channel_capacity.go:247`) must consult this state: default to `Written()`; treat `KEEPALIVE_ONLY` as "not committed"; treat `ATTEMPT_COMMITTED` as "written" even if some inner writer reports otherwise. This is the audited change.
- Waiting never touches `PrepareRequestBilling` (`controller/relay.go:233-241`); the committed transition is the only point a pre-consume may happen, and only per attempt.
- No model output, response id, or protocol switch while queued, by construction of the comment-only writer path.

## Configured-limit queue vs upstream-signal queue

- **Configured limit**: wait for a real local/Redis lease release. Grant = permit handoff (one permit per release, no broadcast). Local FIFO lives with the existing `localChannelCapacity` mutex; Redis uses one shared coordinator notification plus a timed recovery tick. Waiters never each hold a blocking connection.
- **Upstream concurrency without cap**: same queue and same state machine, but admission is driven by a recognized upstream signal (or its recovery) rather than a local limit. There is no known capacity value, so the queue is a bounded backoff gate: notification on observed recovery, plus a bounded timed tick; never a spin. Per-channel adaptive in-flight estimate is a hint only.
- **Recognition**: a 429 queues only when positively identified as concurrency by provider structure (error code/message/envelope). Generic quota/rate-limit 429 stays in the existing failover path and consumes the retry budget as today. A 200-with-error body must be recognized before any client byte is committed; after `ATTEMPT_COMMITTED` it cannot be converted into a queue.
- Policy events must record `source: upstream|local` and the state transitions so saturation is distinguishable from upstream failure in logs and metrics.

## Bounds, fairness, and coordinator pitfalls

1. Enforce the 1000-waiter cap at enqueue; over-cap returns the existing saturated 429. Trim only by expiry score/TTL, never by list position — live tickets are untouchable.
2. Per-user bound prevents one account filling the coordinator; both bounds are checked before allocation.
3. FIFO order is by enqueue ticket. A pinned/strict request that cannot leave the channel stays queued in FIFO with other waiters for that channel; it is not given a bypass.
4. Cancel must remove the ticket and, if a permit was already handed over, return it to the next waiter. Local mode never self-heals, so a leaked permit is permanent.
5. Redis notification loss must resolve by the timed recovery tick; assert bounded wake counts, not spin counts.
6. Queue time is not upstream first-byte time; the attempt timeout starts only after the grant.

## Tests to add (once implementation is authorized)

- State machine with a gin test context: comment-only bytes → `KEEPALIVE_ONLY` → new attempt allowed; a single non-comment byte → `ATTEMPT_COMMITTED` → retry/requeue denied; protocol change after keepalive denied; state absent falls back to `Written()`.
- Re-examine `relay/channel/api_request_test.go:116` and add an explicit ping-then-failover case; assert the existing global guard still blocks after real output.
- Bounds: 1001st waiter rejected; per-user cap; a live ticket survives an admission burst (no LTRIM of live members).
- Coordinator: notification loss recovers via timed tick; one wake per permit; cancel-after-grant returns the permit; no blocking connection per waiter.
- Recognizer table: concurrency 429 → queue; generic 429 → failover; 200-with-error before commit → queue; after commit → error.
- Billing regression: no pre-consume while `WAITING`/`KEEPALIVE_ONLY`; exactly one pre-consume and one settlement for the committed attempt; queue timeout records no channel failure and consumes no retry budget.

## Still unresolved product choices

1. Wait-time keepalive interval: reuse `PingIntervalSeconds` or a separate queue ping setting.
2. Per-user queue bound value and whether it is per channel or per coordinator.
3. Whether the client sees a distinct "waited too long" message or the existing saturated 429 wording.
4. Which provider error signatures are trusted for concurrency recognition.
5. Whether queue depth is exposed in the admin UI (metrics) while running.
