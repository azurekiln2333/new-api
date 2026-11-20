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
| All candidates busy or fixed busy channel | Enter bounded request-scoped queue; revalidate before admission |
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

## Recognized goal-turn channel restrictions

### 1. Scope / Trigger

Channel `setting.deny_goal` restricts explicitly attributed goal turns on HTTP
relay and each Responses WebSocket message.

### 2. Signatures

`service.ClassifyGoalTurnRequest`, `ClassifyGoalTurnMessage` and
`ApplyGoalTurnConstraint` add `dto.FilterGoalTurn` to ordinary channel filters.

### 3. Contracts

The canonical value is the JSON string in
`client_metadata["x-codex-turn-metadata"]`, containing `turn_trigger: "goal"`.
The descriptor is bounded to 4 KiB. A present body descriptor takes precedence
over the compatibility header, including malformed or wrong-type values.
Only the first parsed WebSocket turn may fall back to the handshake header.
Absent/invalid metadata remains unknown; prose and advertised tools are never
evidence of a goal turn. This is client-asserted attribution, not complete
detection of every action associated with a goal.

### 4. Validation & Error Matrix

| Condition | Behavior |
| --- | --- |
| Recognized goal, alternate channel permits it | Select eligible alternate |
| All candidates refuse recognized goal | Local unavailable response; no upstream call |
| Explicit pin refuses recognized goal | Local refusal; preserve pin |
| Locked WebSocket channel now refuses the turn | Refuse this turn with reconnect guidance |
| Channel lookup fails on a reused socket | Reject; never bypass the filter |
| Missing/malformed/oversized metadata | Unknown; no goal-only exclusion |

### 5. Good / Base / Bad Cases

Good: a goal turn uses another eligible channel and retries retain the filter.
Base: an ordinary request mentioning `/goal` keeps ordinary routing.
Bad: applying a connection's goal header to every later WebSocket turn.

### 6. Tests Required

Cover precedence and wrong types in `service/channel_goal_test.go`; cache on/off,
pins, all-denied and retry eligibility in `controller/channel_pin_retry_test.go`;
and five successive WebSocket turns in `controller/responses_websocket_test.go`.
Assert rejected turns make zero upstream calls and only accepted turns settle.
Run controller cases on SQLite, MySQL and PostgreSQL in CI.

### 7. Wrong versus Correct

Wrong: independently load the locked channel to validate and load it again to use.
Correct: validate filters against the same successful channel snapshot restored
by `restoreConnectionContext`, preserving lookup failures as failures.

## Pre-output upstream failures and stream lifecycle

### 1. Scope / Trigger

HTTP refusals and native/converted Responses streams may fail before producing
an answer, including HTTP 200 followed by `response.failed`, `error`, a failed
terminal envelope, or EOF before a required terminal event.

### 2. Signatures

`ClassifyUpstreamError(code, type, message)` consumes upstream error-envelope
fields only. `StreamCommitGate` owns each attempt's held framing;
`StreamCommentEnvelope` serializes request-wide connection writes and tracks
comment-only keepalive separately from delivered content.

### 3. Contracts

Known structured concurrency codes and the bounded, anchored compatibility
message `stream disconnected before completion: concurrency limit exceeded for
user` identify capacity refusal. Quota and ordinary rate-limit codes retain
their separate meanings; arbitrary prompt/output text never classifies a
refusal. Busy exclusions survive disabled ordinary failed-channel avoidance.

Install the attempt gate before upstream dispatch. Preserve wrapper order:
gate -> request-plugin Reply -> serialized connection envelope -> connection.
Protocol-aware handlers explicitly hold initialization frames until usable output,
bounded bytes or hold timeout. Unknown handlers must write through by default;
a controller-wide Hold can discard a successful response from a handler that
does not implement Commit. Set SSE headers once before writer goroutines; write
each complete event as one
frame. Finish must stop and join timers/writers idempotently. Never bypass Reply
or silently swallow final errors when removing wrappers.

Observe usage and productive output before deciding a failure is replayable.
Once work is billable or output is exposed, emit the incoming protocol's terminal
error, settle actual usage once and never retry/refund the consumed attempt.
Native Responses uses `response.failed`; converted Chat and Messages use their
own protocol framing. Only zero-work pre-output failures can switch channels.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Initialization only, then definite failure | Discard attempt framing; eligible retry |
| Generic 429 or quota error | Ordinary configured retry policy; no concurrency queue |
| Productive output or known usage, then failure | Terminal stream error and one settlement |
| Required terminal event missing before output | Retryable upstream truncation |
| Required terminal missing after output | Stream error without replay |
| Client canceled, pin, strict binding or exhausted budget | Preserve applicable retry boundary |

### 5. Good / Base / Bad Cases

Good: created/error from A is hidden and B supplies one clean response. Base:
successful streams retain event order and usage. Bad: model output containing
the word concurrency changes channel state, or a partial billed answer is replayed.

### 6. Tests Required

Extend controller routing and native/converted Responses suites for delayed
HTTP errors, early SSE errors, terminal variants, EOF, cancellation, output and
usage. Assert exact accounting and no first-attempt IDs or duplicate answer.
Helper race tests use deterministic writer barriers, join before recorder reads,
and cover repeated Finish, hold timeout, complete frame ordering and plugin
wrappers. Run Linux race checks and independent modules in cloud CI.

### 7. Wrong versus Correct

Wrong: use raw `Writer.Written()` after a comment as proof of a delivered answer,
or reset the entire writer chain to retry. Correct: retain the explicit
comment/content distinction, remove only the owned attempt layer, and stop all
of its writers before beginning another attempt.

## Waiting for channel capacity

### 1. Scope / Trigger

After all eligible candidates are locally saturated or explicitly rejected
upstream concurrency, retain the request until a slot is available, cancellation
occurs, or its fixed waiting deadline expires. Pins and strict affinity can wait
on their one authorized channel but never switch outside that scope.

### 2. Signatures

`NewChannelWaiter(ctx, userID)` creates a request owner. `Wait(ctx, candidates)`
returns a `ChannelWaitGrant` carrying an already-counted lease; do not acquire a
second slot. `Close` is idempotent. Report recognized busy state before releasing
the attempt's lease; successful probes clear that busy gate before release.

### 3. Contracts

`CHANNEL_WAIT_TIMEOUT_SECONDS` defaults to 300 (1..3600),
`CHANNEL_WAIT_MAX_REQUESTS` to 1000 (1..10000), and
`CHANNEL_WAIT_MAX_PER_USER` to 20 (1..1000). At most 256 distinct eligible
candidates enter one wait. Repeated busy cycles never reset the deadline.

Admission preserves FIFO among overlapping candidate sets. Redis uses atomic
ticket/capacity operations, expiring owner records, a shared notification
listener and bounded recovery polling. Never trim live tickets or fall back to
independent local admission on coordinator failure. Every expiring lease/probe
record survives until at least its first renewal.

A grant is not authorization: reload current channel/group/model/key/goal/pin
rules and capacity before use. Release invalid or abandoned grants. SSE waiting
uses only serialized comments; nonstream requests never receive SSE. Initial
waiting precedes billing preparation; later busy waits reuse the same reservation.
Generic 429, quota, transport failure and billable partial output never queue.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Slot freed and policy still permits | Admit one waiter and resume request |
| User/global queue cap reached | `channel_capacity_queue_full` |
| Fixed wait deadline elapsed | `channel_capacity_wait_timeout` |
| Redis unavailable or owner lost | `channel_capacity_unavailable` |
| Client cancellation or changed policy | Remove ticket, release raced grant |
| Upstream concurrency without configured local cap | One queued probe with bounded backoff |

### 5. Good / Base / Bad Cases

Good: a fixed-channel request keeps receiving comments until the slot is free,
then settles once. Base: ordinary traffic incurs no ticket until it must wait.
Bad: restarting the deadline on every refusal or reserving quota a second time.

### 6. Tests Required

`TestChannelWaitRouting` covers actual HTTP waiting, pins, cancellation, changed
eligibility, unlimited upstream busy, generic/quota errors and exact accounting
on SQLite/MySQL/PostgreSQL. Service tests cover FIFO, cancellation/grant races,
limits, deadline persistence, real Redis expiry/renewal/notification recovery
and two-client probe exclusion. Run these with Linux race detection in CI.

### 7. Wrong versus Correct

Wrong: dequeue every waiter when one slot releases, then let ordinary admission
race. Correct: reserve capacity atomically for the eligible queue owner and
return the owned lease for final live-policy validation.

## Remembered upstream model unavailability

### 1. Scope / Trigger

A real upstream 404/410 can establish that the attempted model is unavailable
for one channel credential. Preserve configured model names and other usable
credentials. Never infer this from prompt/output text, a generic path 404,
authentication failure, quota refusal or concurrency rejection.

### 2. Signatures

`ClassifyUpstreamModelUnavailable(err, attemptedModel)` returns fixed evidence
`error_code`, `error_message` or none. `RecordChannelModelUnavailability` checks
the current channel, credential and mapping under the existing channel lock.
`RestoreChannelModelUnavailability(channelID, model, keyFingerprint)` removes
matching observations. `POST /api/channel/:id/model_unavailable/restore`
requires `ChannelOperate`; optional `model` and `key_fingerprint` narrow the
selection, and the response's boolean `data` reports whether anything changed.

### 3. Contracts

Store observations in the existing `channel_info.model_unavailable` array:
`upstream_model`, `key_fingerprint`, `key_index`, `status_code`, `evidence`,
`observed_at`. Keep at most 64 observations and bound model names to 128
characters. The fingerprint is a truncated SHA-256 identity, never a credential.
Do not store raw upstream text. An index is historical, so UI must not present
it as the credential's current position after reordering.

Publish the committed marker slice without overwriting newer key status,
scopes or the polling cursor; use existing channel sync for other instances.
Ordinary configuration and cursor updates must preserve runtime observations.
Routing checks use the currently resolved upstream model and keys available to
the requesting user: another user's unmarked bound key cannot keep a public
candidate eligible. Apply the rule to memory/SQL selection, pins, affinity,
plugins, WebSocket turns and queue grant revalidation.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Definite model-not-found code on actual upstream 404/410 | Record attempted model/credential; ordinary retry boundaries apply |
| Anchored reported sentence naming the actual attempted model | Same behavior, fixed message evidence |
| Generic unsupported endpoint, auth, quota or capacity error | No persistent capability change |
| Mapping/key changed since attempt | Discard obsolete observation |
| Other eligible credential or channel exists | Preserve its eligibility |
| Explicit administrator restore | Clear selected observations, refresh cache and audit without secrets |

### 5. Good / Base / Bad Cases

Good: A reports its attempted model is absent, B answers, and future requests
skip only A's marked credential/model. Base: an unrelated model remains usable.
Bad: disable the entire channel, delete configured models, or keep A eligible
using a credential owned by a different user.

### 6. Tests Required

Run `TestChannelModelUnavailability` with `TEST_TASK_DB_DIALECT` set separately
to sqlite, mysql and postgres; configure the corresponding real engine DSNs.
Cover persistence, memory cache, alias/mapping changes, key reorder/replacement,
user binding, stale configuration updates and restore. Existing service tests
cover classifier false positives; controller/UI suites cover permissions and
the restore action. Cloud verification is mandatory before compatibility claims.

### 7. Wrong versus Correct

Wrong: replace all cached `ChannelInfo` from a stale failed attempt. Correct:
validate current state inside the update lock and publish only the current
committed observation slice. Recovery removes observations; it does not enable
disabled channels or keys.

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
