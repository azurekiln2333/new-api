# R8 service coordinator handoff — 2026-11-20

Implemented by root/r8_queue. Root owns staging/commits; p9 owns controller,
stream-envelope integration and end-to-end billing/protocol validation.

## Service contract

- `NewChannelWaiter(ctx, userID)` creates one request-scoped handle. `Wait(ctx,
  candidates)` registers a bounded ticket and returns a `ChannelWaitGrant` with
  the channel ID and an already-counted `ChannelCapacityLease`. Do not acquire
  the returned slot a second time. `Close` is idempotent. The first wait fixes
  the deadline; subsequent busy cycles cannot restart it.
- The caller passes policy-eligible candidates and revalidates current channel,
  group, key, model, goal, pin/affinity and capacity settings after grant. Queue
  state is not authorization. Rejecting a stale grant requires lease release.
- Defaults: 300 seconds, 1,000 waiting requests/coordinator, 20/user. Environment
  controls: `CHANNEL_WAIT_TIMEOUT_SECONDS`, `CHANNEL_WAIT_MAX_REQUESTS`,
  `CHANNEL_WAIT_MAX_PER_USER`; values are bounded. Queue full, wait timeout and
  coordinator unavailable have distinct error codes; all remain local capacity
  errors excluded from channel-health failure handling.
- Queue full and timeout map to the dedicated backend i18n keys in all three
  languages. Wait rejects a missing/non-positive authenticated user ID.
- Local dispatcher preserves FIFO among overlapping candidate sets and checks
  existing capacity atomically. Ordinary limited admissions cannot overtake
  queued work. Registration schedules a recheck, closing release-before-park.
- Redis registration/caps and FIFO+admission are atomic Lua operations using
  server time and the existing capacity lease ZSET. One PubSub listener and one
  recovery clock serve each active process coordinator; one pipeline checks its
  waiting requests. No per-waiter blocking Redis connection or timer exists.
  Expiry recovers abandoned tickets and missed notifications. Live tickets are
  never trimmed. Errors fail explicitly without local fallback.
- `ReportChannelUpstreamBusy` is called only after positive structured/anchored
  upstream concurrency classification; generic 429/quota/transport failures do
  not trigger it. Backoff is exponential with bounded jitter and Retry-After;
  queued requests share one probe per channel, including Redis replicas. Report
  renewed busy before the probe lease release. Success calls
  `lease.MarkUpstreamAvailable()` before release. Owner-aware cleanup/renewal
  cannot release another probe. Unclassified release retains a one-second floor.
- Ordinary unlimited traffic retains R6's best-effort telemetry behavior. A
  busy gate serializes queued probes, not every unrelated new unlimited request;
  this does not claim exact upstream occupancy or global traffic fairness.
- The service does not write response bytes or reserve/settle quota. Returned
  leases use the original request context, not the short-lived parking context.
  Cancellation after an atomic grant returns the raced reservation.

## Owned files

- service/channel_wait.go
- service/channel_wait_test.go (one consolidated regression file)
- service/channel_capacity.go (FIFO gate, release notification, probe lifecycle)
- relaykit/types/error.go (queue full/wait timeout codes)

## Verification

Passed locally with portable Go 1.26.10, GOMAXPROCS=2:

- `go test -p 1 ./service -run '^TestChannelWait|^TestChannelCapacity' -count=1 -timeout 60s`
- `go vet -p 1 ./service`
- `GOWORK=off go build -p 1 ./...` inside relaykit
- gofmt and scoped git diff --check

Deterministic local cases cover FIFO, unrelated channel progress, no newcomer
overtake, release-before-registration, fixed deadline/caps, cancellation before
and after grant, no canceled admitted context, bounded Retry-After and one
upstream probe/backoff. Real Redis test `TestChannelWaitRedis` covers independent
clients, shared limits/FIFO, expired-ticket recovery without a notification,
unlimited-channel single probe, owner renewal and coordinator failure.

Local Redis cases are SKIPPED without TEST_REDIS_ADDR. Final CI must execute
`TestChannelWaitRedis` against real Redis and include `ChannelWait` in its Linux
race selector. No Redis compatibility or final R8 acceptance is claimed yet.

## Remaining integration gates

p9/root must verify all-busy candidate exhaustion (including pins/strict affinity),
live eligibility changes, HTTP/protocol errors after
comment-only keepalive, success after queue, actual usage before requeue, and
exactly one billing reservation/settlement. Native/converted SSE must remain one
clean response; ordinary Writer.Written guards cannot be removed.

Read-only integration review identified transient p9 issues and sent concrete
feedback: ordinary all-busy exhaustion must call Wait; granted settings must be
matched by channel ID; invalid grants must not hot-loop the same stale snapshot;
comment keepalive needs SSE headers, serialized writes, idempotent stop/join and
an actually wired outer commit state; upstream busy reporting must precede lease
release and successful probes clear their gate. These remain controller/stream
acceptance items, not service completion claims.
