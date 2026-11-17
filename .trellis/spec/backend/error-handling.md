# Error Handling

> How errors are handled in this project.

---

## Overview

<!--
Document your project's error handling conventions here.

Questions to answer:
- What error types do you define?
- How are errors propagated?
- How are errors logged?
- How are errors returned to clients?
-->

(To be filled by the team)

## Channel admission and first-response timeout

### 1. Scope / Trigger

Applies to generation attempts, streaming bodies, provider SDK requests, channel
tests, HTTP task submissions and live upstream WebSocket connections. Background
polling and accepted remote jobs do not occupy generation slots.

### 2. Signatures

- `service.AcquireChannelCapacity(ctx, channelID, dto.ChannelSettings)` returns a
  `ChannelCapacityLease` or a local `*types.NewAPIError`.
- Hold the lease until upstream body/connection cleanup; use its `Context()` and
  idempotent `Release()`, and classify coordination loss through `Err()`.
- `service.WithChannelCapacityCancellation(ctx)` restores lease-control
  cancellation after native task SSE deliberately detaches client cancellation.
  Renewal must continue until actual release, and coordination loss must still
  cancel detached upstream work.
- `service.EffectiveUpstreamFirstByteTimeoutSeconds(settings)` resolves the
  validated `model.CurrentRequestPolicy()` snapshot plus channel override.
- `service.GetRelayHttpClientWithProxySettings(proxy, settings)` adds that timer
  for scoped generation/submission/test requests. The ordinary
  `GetHttpClientWithProxySettings` keeps background polling, discovery and
  artifact fetching outside this new deadline.
- `service.IsUpstreamFirstByteTimeout(err)` examines the preserved error chain.

### 3. Contracts

- Channel JSON `max_concurrent_requests: *int`: absent/0 unlimited; positive
  capacity shared by every key on the channel ID, independently of cache objects.
- Channel JSON `upstream_first_byte_timeout_seconds: *int`: nil inherits global;
  explicit0 disables the additional timer. Global option is
  `UpstreamFirstByteTimeoutSeconds`, default0.
- Timeout stops at the first body byte, including an upstream heartbeat. Headers
  alone do not stop it. It never becomes a total stream duration limit.101 upgrades
  skip body pre-read and retain their write interface.
- `RetryTimes` defaults to2 only if no saved value; saved0 disables error retry.
 504/524 follow configured retry ranges; typed local first-byte timeout bypasses
  those ranges but still respects retry budget and stop boundaries.
- Startup upgrades only the exact former built-in retry range, with marker
  `internal.request_policy.retry_timeouts_v1` in the same transaction. Sync and
  later administrator changes must never repeat the migration.
- Busy-candidate traversal must preserve the candidate's actual auto group and
  leave upstream retry budget untouched, so another free channel in that group
  remains eligible even when `RetryTimes` is zero.
- Client cancellation must not create an upstream health failure or disable the
  channel. A generation already successfully settled must not be reclassified
  because a lease renewal fails during subsequent cleanup.
- Unlimited admission must mask the previous lease context value. A detached
  native SSE retry otherwise inherits a released limited-channel lease and
  cancels the next unlimited attempt before submission.

### 4. Validation & Error Matrix

| Condition | Result |
|---|---|
| Cap outside0..1000000 or timeout outside0..86400 / fractional input | Save rejected |
| Candidate busy, alternate eligible | Select alternate without consuming retry budget |
| All candidates busy or fixed busy channel | Local429 `channel_capacity_saturated` |
| Configured Redis unavailable or owner renewal lost | Local503 `channel_capacity_unavailable`; no process-local fallback |
| First body byte deadline wins |504 `upstream_first_byte_timeout`, upstream canceled before handling/settlement |
| Client canceled, output committed, strict session or explicit pin | Preserve retry stop boundary |

Capacity errors do not disable channels, increment upstream failure state or
produce an upstream error log. Failed-channel avoidance never randomly reuses an
exhausted candidate; disabling that option does not remove busy exclusions.

### 5. Good / Base / Bad Cases

- Good: A holds two streams, third request selects B; closing A frees one slot.
- Base: absent cap and timeout preserve unlimited admission and existing deadlines.
- Bad: counting per API key allows a multi-key channel to exceed its total cap;
  releasing at response headers admits work while the old stream is still active.

### 6. Tests Required

Use existing `service/channel_capacity_test.go`, `service/http_client_transport_test.go`,
`controller/channel_pin_retry_test.go` and `controller/relay_stream_test.go`.
Assert real A-to-B routing for403/404/504/524 and headers-only stalls; one pre-consume
and one successful settlement; exact replay; productive streams; Redis owner-specific
release/expiry/renewal; actual client cancellation and connection closure;
auto-group busy/busy/free traversal at retry budget zero; canceled requests with
automatic disable configured for status 500 leave the channel healthy.
Persistence tests require real SQLite, MySQL and PostgreSQL. CI supplies
`TEST_MYSQL_DSN`, `TEST_POSTGRES_DSN`, `TEST_REDIS_ADDR`; configured fixtures must
fail on connection errors instead of skipping. Missing local fixtures are not proof
of compatibility. Release validation depends on this integration job.

### 7. Wrong vs Correct

Wrong: gateway SSE ping while waiting on a bounded upstream commits output before
the retry decision; a header timeout misses a stalled body; hiding the timeout error
by replacing its cause loses AWS/gateway retry classification.

Correct: suppress pre-response ping for bounded attempts; synchronously preserve one
body byte in the final RoundTripper; retain the typed cause and cancellation ownership.
The timeout implements AWS `RetryableError() bool` as false solely to stop same-channel
SDK retries, without setting gateway SkipRetry.

---

## Error Types

<!-- Custom error classes/types -->

(To be filled by the team)

---

## Error Handling Patterns

<!-- Try-catch patterns, error propagation -->

(To be filled by the team)

---

## API Error Responses

<!-- Standard error response format -->

(To be filled by the team)

---

## Common Mistakes

<!-- Error handling mistakes your team has made -->

(To be filled by the team)
