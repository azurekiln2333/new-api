# R8 independent service review — 2026-11-20

Reviewer: root/r21_review. Root owns staging and commits. The reviewed service
boundary is `service/channel_wait.go`, `service/channel_wait_test.go`, the R8
hunks in `service/channel_capacity.go`, and `relaykit/types/error.go`.
Controller and stream integration remain p9's separate responsibility.

## Findings fixed

- Redis probe admission previously retained the old busy hash TTL. If a queued
  request acquired a probe near the hash's expiration, ownership could disappear
  before the first lease renewal, allowing another probe or canceling the live
  request. The atomic grant now renews that hash's TTL. The existing real Redis
  test starts with a gate near expiry and checks the newly granted probe remains
  protected through its first renewal, then verifies single-probe ownership,
  renewal, success and cleanup.
- Queue tickets previously always used the configured queue deadline even when
  their request context had an earlier deadline. In a gateway crash, that could
  leave an already-expired request blocking peers until the longer queue limit.
  Registration now uses the effective waiting context deadline. The existing
  public Wait lifecycle regression covers an earlier request deadline and still
  verifies that returning from parking does not cancel the admitted request.

## Reviewed behavior

- Local admission and FIFO dispatch take the queue lock before the capacity lock;
  release drops the capacity lock before probe cleanup. No inverse lock order
  was found in the reviewed paths.
- A canceled request synchronizes with dispatch, drains a raced grant, and
  releases its owner. Redis ambiguous-write cleanup is owner-specific and also
  removes every candidate's ticket. Expiry provides bounded cleanup when the
  coordinator is unavailable.
- Redis uses server time, global/per-user caps, per-channel sequence ordering,
  atomic capacity grants, one process coordinator listener and recovery polling.
  Existing ordinary limited admissions cannot overtake a live queued ticket.
- Repeated waits preserve the original maximum deadline. Queue full, timeout
  and unavailable have distinct local capacity codes and translated messages.
- Busy probes use owner-aware release/renewal, bounded Retry-After and backoff.
  Ordinary unlimited admissions intentionally retain the accepted best-effort
  counting behavior; this does not guarantee fairness against unrelated traffic.

## Verification

Using portable Go 1.26.10 with GOMAXPROCS=2:

- PASS: `go test -p 1 ./service -run '^TestChannelWait|^TestChannelCapacity' -count=1 -timeout 60s`
- PASS: `go vet -p 1 ./service`
- PASS: `GOWORK=off go build -p 1 ./...` from `relaykit/`
- PASS: scoped gofmt and git diff --check
- No frontend changes or TypeScript checks apply to this service-only revision.

## Findings not fixed / remaining acceptance

- Real Redis execution is not available locally: TEST_REDIS_ADDR is unset,
  redis-server and Docker are absent, and the installed Ubuntu WSL distribution
  fails to start with HCS_E_HYPERV_NOT_INSTALLED. TestChannelWaitRedis is skipped
  locally. The configured Linux CI Redis/race job must pass on the committed
  revision; neither Redis verification nor full R8 acceptance is claimed here.
- Controller/stream integration must prove all-busy upstream rejection queues,
  live policy revalidation (including group-specific model/goal/key eligibility),
  comments-only envelopes, cancellation, and single billing reservation and
  settlement. Those code paths are owned by p9 and were not edited by this
  reviewer. Findings from the in-progress integration were sent to root.
- The new Redis lifecycle regression should remain in the real Redis CI selector.
  The existing CI service suite and race selector already include ChannelWait.

## Contract to preserve

Every independently expiring owner record needed by a granted lease must survive
at least until its first renewal; updating Redis hash fields alone does not
extend an existing key expiry. Abandoned tickets must expire at their effective
request deadline. Root may incorporate these invariants into the backend spec
when consolidating R8; no concurrent shared spec file was edited here.

## Controller integration handoff (2026-11-20)

Root reassigned `controller/relay.go` and the existing routing regression suite
to this reviewer after p9 released them. Added the upstream-concurrency path:
recognized pre-content refusals traverse other candidates without consuming the
ordinary error retry budget, then park under one request-scoped waiter deadline.
Pins and strict bindings remain on their selected channel. Generic 429 and quota
errors retain ordinary retry behavior. Retry-After is parsed from the current
attempt only; coordination failures propagate instead of silently replaying.

Queued candidates now retain their selected group and load full current channel
settings. Wake-up rechecks enabled status, group_models, key eligibility, goal
filters and pin/session scope; invalid grants are released before trying the
next eligible queued candidate. Changed caps require a new atomic reservation.

Added `TestChannelWaitRouting` to `controller/channel_pin_retry_test.go`:
actual HTTP comments provide the waiting barrier; cases cover pinned local
admission, cancellation, changed group/goal/key scope, unlimited upstream busy,
all busy candidates, strict/pinned busy, generic and quota 429. Paid requests
assert one shared reservation, one settlement and consume log, while initial
waits/cancellation never reserve quota. Unique channel IDs isolate backoff state.

The user subsequently required all verification on cloud CI and no further local
tests/build/lint/browser runs. The controller test added in this handoff has NOT
been executed locally. The earlier TestRequestPolicyUpstreamHTTPFailover run
passed before the final Retry-After wiring and the new tests. No final pass is
claimed. CI must run TestChannelWaitRouting on SQLite, MySQL and PostgreSQL,
plus Linux race coverage and the real Redis queue suite before release.

Remaining cross-owner review: p9 owns stream helper/handler changes, including
billable-usage retry boundaries and terminal protocol errors after comments.
Root owns CI selectors, all staging/commits and final integrated verification.
