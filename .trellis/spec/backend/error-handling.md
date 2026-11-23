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

Transport keepalive comments can arrive before a protocol handler calls Hold.
The attempt gate buffers those comments under its existing byte/time bounds;
the first actual write still commits immediately for an unknown handler. The
keepalive owner's stop callback joins its goroutine and removes its own outer
writer before the attempt gate finishes. A stopped wrapper retaining a closed
gate would swallow every later attempt. Native and converted Responses HTTP
refusals preserve Retry-After before their response bodies are consumed.

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

## Trusted console diagnostic admission

### 1. Scope / Trigger

Use fixed-channel admission for server-owned console diagnostics and background
management operations. It reserves capacity; callers still own access checks and
current channel, enabled-key, model and endpoint validation.

### 2. Signatures

`service.NewChannelDiagnosticAdmission(ctx, userID)` returns an operation owner.
Call `Acquire(ctx, channelID, settings)` for ordinary admission, `Wait(...)` only
after a positively classified upstream concurrency refusal, and defer `Close()`.
The returned `ChannelCapacityLease` is already counted; use its `Context()` and
release after upstream body cleanup.

### 3. Contracts

Background operations use internal user bucket 0, bounded by existing
`CHANNEL_WAIT_MAX_REQUESTS`, `CHANNEL_WAIT_MAX_PER_USER` and
`CHANNEL_WAIT_TIMEOUT_SECONDS`. Ordinary `NewChannelWaiter(ctx, 0)` remains
unauthorized. Never manufacture a positive user ID.

Saved limited channels reuse the shared queue. Ordinary unlimited channels
retain best-effort telemetry and continue during Redis telemetry outages.
Unsaved IDs are uncounted previews and never share an ID-0 capacity counter.
Reuse the owner across retries so the original wait deadline cannot reset.
After a grant, reload applicable policy and release/reacquire when the cap
changes. Never acquire twice or silently test another channel.

### 4. Validation & Error Matrix

| Condition | Required behavior |
| --- | --- |
| Ordinary anonymous waiter | 401; no queue registration |
| Internal limited channel is full | Bounded fixed-channel wait |
| Canceled queued operation | Remove only its registrations; preserve active lease |
| Unlimited channel with Redis telemetry unavailable | Admit without canceling traffic |
| Explicit recognized upstream concurrency refusal | Reuse owner and shared busy backoff |
| Generic 429, quota refusal or transport failure | No concurrency-wait classification |
| Unsaved preview | Cancellable uncounted lease |

### 5. Good / Base / Bad Cases

Good: a scheduled test queues behind a live request and releases its slot after
reading the response. Base: an unlimited preview keeps its ordinary behavior.
Bad: inventing a user identity, resetting the deadline on each retry or
returning a lease before the response body closes.

### 6. Tests Required

Extend `service/channel_wait_test.go`: verify anonymous refusal, fixed-channel
handoff, cancellation, idempotent release, uncounted previews and unlimited
admission during Redis outage. With real Redis, assert internal cancellation
cleans global/user/channel queue entries without removing another active owner;
then admit and release a replacement operation. Run through cloud CI.

### 7. Wrong versus Correct

Wrong: route every operation through strict Redis waiting, making an unset cap
a new availability dependency. Correct: use `Acquire` normally and reserve
explicit `Wait` for recognized upstream concurrency recovery.

## Channel management HTTP lifetime

### 1. Scope / Trigger

Administrative discovery, balance, price, inference and Ollama calls keep their
specialized protocol paths. Share channel transport, admission and cleanup
without generation conversion or generation first-byte timers.

### 2. Signatures

`service.DoChannelManagementRequest(service.ChannelManagementCall)` returns an
`*http.Response` whose body owns the acquired lease. The call contains Request,
Settings, ChannelID, Admission, Timeout, NoRedirect and optional
`Reload func() (*http.Request, dto.ChannelSettings, error)`.

```go
owner := service.NewChannelDiagnosticAdmission(ctx, 0)
defer owner.Close()
resp, err := service.DoChannelManagementRequest(service.ChannelManagementCall{
    Request: request, ChannelID: channel.Id, Settings: channel.GetSetting(),
    Admission: owner, Reload: reloadCurrentRequest,
})
if err != nil {
    return err
}
defer resp.Body.Close() // Includes non-2xx and failed/partial body reads.
```

### 3. Contracts

The caller builds the specialized URL, method, fresh body and final static
headers. Never forward browser client headers. Reload runs after each grant,
validates the same saved channel/key, and returns current transport settings. A
changed effective concurrency cap releases and reacquires with the same owner,
bounded to three grants. Reload does not acquire a nested lease. Callers already
holding an outer lease pass nil Admission and perform their own revalidation.

The original request context and optional total Timeout survive replacement
requests. A bounded management context supersedes the cached client's total
timeout without changing its shared transport or connection/header timeouts.
Static Host is mapped to Request.Host on a clone. Credentials and arbitrary
static overrides never follow a cross-origin redirect; NoRedirect forbids all
redirects. Proxy and transport errors do not expose credential-bearing URLs.

The caller closes every response body. Reading EOF alone does not release the
lease. Closing the body releases once, even after cancellation or read failure.
Complete successful HTTP reads may confirm a queue probe is available. The
helper does not classify provider errors or retry requests; callers own safe,
bounded retries with the same admission owner.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Missing/invalid request URL or embedded URL credentials | Reject before send |
| Reload rejects changed channel/key | Release grant; no stale dispatch |
| Cap changes after grant | Release and reacquire current cap |
| Cap changes repeatedly | Bounded error; no leaked capacity |
| Caller cancellation or send deadline | Cancel request and release lease |
| Response headers or failed body read | Keep lease until explicit body close |
| Cross-origin redirect with channel headers/policy | Return 3xx without forwarding secrets |
| Long operation with its own deadline | Keep that deadline; cached client unchanged |

### 5. Good / Base / Bad Cases

Good: a queued management call sends the reloaded key and Host, then holds its
slot while its body is processed. Base: an unsaved preview remains uncounted.
Bad: releasing at response headers, nesting admission or mutating the cached
client timeout to accommodate one long Ollama pull.

### 6. Tests Required

Extend `service/http_client_transport_test.go` for cap reduction/regrant, current
URL/key/Host, body lifetime, cancellation, idempotent Close and send-deadline
cleanup. Use the synthetic clock to prove a management deadline can exceed the
cached client timeout while that client remains unchanged. Run the existing
ChannelManagement race selector and full backend suite in cloud CI.

### 7. Wrong vs Correct

Wrong: return an upstream response after deferring its lease release in the
dispatch function. Correct: attach cleanup to the returned body and require the
consumer to close it after processing, including every error branch.

## Codex Wham management and credential refresh

### 1. Scope / Trigger

Apply to console Wham usage, credit inspection and credit consumption. These
specialized endpoints use management transport, not generation conversion.

### 2. Signatures

The three `service.FetchCodexWhamUsage`,
`FetchCodexWhamRateLimitResetCredits` and
`ConsumeCodexWhamRateLimitResetCredit` functions receive a
`CodexWhamRequestSender func(*http.Request) (*http.Response, error)`.
The controller supplies `codexWhamDispatch.send` and owns one diagnostic
admission owner for the initial and optional post-refresh request.

### 3. Contracts

Reload the fixed current single-key Codex channel after admission. Rebuild
Wham paths, access-token/account headers and static overrides from that row;
the access token alone expands `{api_key}`. Never forward browser headers or
the full OAuth JSON. Disabled channels retain explicit administrator recovery.
The service reads and closes the response body before OAuth refresh starts.
The independent OAuth exchange receives no channel overrides. No redirect is
followed, and no generic failure retry is added to credit consumption. The
existing unauthorized/forbidden refresh-once branch remains a separate retry.

Refresh persistence compares ID, Codex type and the exact source credential.
MySQL uses parameterized binary casts, PostgreSQL explicit C collation and
SQLite BINARY collation. Plain text equality under a case-insensitive column
collation is insufficient. GORM quotes the reserved key identifier and binds
values; discard SQL logging for both secret predicates and updates. A failed
CAS keeps a concurrent replacement. The in-memory refreshed credential is
eligible only while the database still holds the exact source bytes.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Current channel deleted/type or multi-key policy changed | Reject before dispatch |
| Current URL/key/headers/cap changed before grant | Use current policy or regrant |
| Response body incomplete or caller canceled | Close body and release capacity |
| Source key replaced, including case-only or trailing whitespace | Keep replacement bytes; do not overwrite |
| Consume returns 500 or redirect | Return that attempt; no generic retry or redirect |

### 5. Good / Base / Bad Cases

Good: a post-refresh dispatch uses a concurrently replaced credential B. Base:
an unchanged source A is replaced with its refreshed credential. Bad: a
MySQL case-insensitive equality lets refreshed A overwrite case-changed B.

### 6. Tests Required

`TestCodexWhamChannelManagement` in `controller/codex_usage_test.go` observes
actual paths, account/access-token headers, cleanup, cancellation, replacement
bytes, changed type, consume failure and redirects. Run the selector with
`TEST_CHANNEL_SQL_DSN` for real MySQL/PostgreSQL, default SQLite and the cloud
race gate. Authentication, Session Management and OAuth2 OWASP Cheat Sheets
are the credential-handling read gate; ASVS 5.0.0 is the recorded baseline,
not a claim of full conformance.

### 7. Wrong vs Correct

Wrong: update a refreshed key by ID alone, or trust a text-collated source-key
predicate. Correct: bind an exact-byte source/type CAS, then reload the current
credential for the next admitted Wham attempt.

## Asynchronous performance sampling backend

### 1. Scope / Trigger

Apply when a finished request queues Redis performance counters after its caller
can release request-scoped or test-fixture state.

### 2. Signatures

`perfmetrics.Record(sample Sample)` updates local counters synchronously. Its
queued writer is `recordRedis(client *redis.Client, key bucketKey, sample Sample)`.

### 3. Contracts

Capture Redis enablement and the selected client inside Record before scheduling.
Disabled Redis or a nil client schedules nothing; local sampling still happens.
Queued work uses only the captured client and values, never mutable Redis globals.
The existing one-second Redis operation deadline remains independent of the HTTP
request. This does not make arbitrary concurrent global reconfiguration safe.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Redis disabled or client nil | Local count retained; no background job |
| Client replaced after scheduling | Old job uses captured original client |
| Captured Redis client fails | Existing best-effort failure; local count retained |

### 5. Good / Base / Bad Cases

Good: delayed work keeps client A after globals switch to B. Base: disabled Redis
records locally. Bad: delayed work reads a fixture's restored RedisEnabled value.

### 6. Tests Required

`TestRecordRedisUsesBackendSnapshotBeforeScheduling` in the existing metrics test
file controls the scheduling boundary and asserts client identity, no scheduling
when disabled/unconfigured, and retained local counts without sleeps. Run it with
`-race` alongside the original controller race selector in cloud CI.

### 7. Wrong vs Correct

Wrong: check RedisEnabled or RDB inside the queued closure. Correct: capture the
backend synchronously and pass the client explicitly into the delayed writer.

## Guarded management result persistence

### 1. Scope / Trigger

Apply when balance queries, credential refreshes or model discovery perform HTTP
outside a transaction and later write a result derived from channel configuration.

### 2. Signatures

`model.UpdateChannelOtherInfoLocked(ctx, id, update)` runs a short current-row
callback under the shared row-lock convention.
`UpdateChannelBalanceDetailChecked` merges a completed balance result through
that boundary. `UpdateChannelStatusChecked` checks current configuration before
changing automatic status and publishing cache routing.

### 3. Contracts

No network call belongs inside the callback. SQLite takes its channel mutex and
writer lock; MySQL/PostgreSQL use `lockForUpdate`. Compare credential bytes and
provider/configuration identity in Go, avoiding case-insensitive SQL equality.
Preserve current other_info and unrelated policy. Disable secret SQL logging.

A balance plugin's successful credential refresh must be proven by the actual
outgoing retry credential, its same account/index and the exact current saved
key-set/configuration. Advance earlier multi-key sample provenance only when
the sole changed line is that proven refresh. An administrator replacement,
changed binding, header or plugin configuration is not refresh provenance.
Redact both old and refreshed credentials from returned parser/transport errors.

Checked cache publication keeps the identity check and status/routing write under
the same channel lock. A change only to max_concurrent_requests does not invalidate
the completed balance result; preserve the cache's configuration bytes when
publishing status. Other authentication/configuration/key-scope changes still
reject stale publication.

Codex refresh uses optional `CodexCredentialRefreshOptions.ExpectedKey` before
exchange and always checks the exact source/type/single-key mode before storing.
`ErrCodexCredentialSourceChanged` returns no usable refreshed credential. Ordinary
driver failures become fixed value-free errors. OAuth HTTP remains independent
of channel header overrides.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Source key/provider/balance auth or route changed | Reject stale result |
| AutoBan changed during request | Use current AutoBan at status write |
| Refresh loses to administrator replacement | Preserve replacement bytes |
| Context canceled or persistence fails | No successful result/cache publication |
| Cache replaced after database validation | Preserve newer cache identity/routing |
| Only admission cap changed | Publish valid status without replacing cached settings |

### 5. Good / Base / Bad Cases

Good: completed A cannot overwrite replacement B. Base: unchanged source writes
its result while preserving current unrelated fields. Bad: a plain MySQL text
comparison treats a case-only credential replacement as the old source.

### 6. Tests Required

Run `TestUpdateChannelStatusCheckedCacheRouting`,
`TestChannelBalanceManagementDatabaseMatrix` and `TestCodexChannelManagement*`
with real SQLite, MySQL and PostgreSQL in cloud CI. Observe stored credentials,
balance/status, routing, current AutoBan and failed-store confidentiality.

### 7. Wrong vs Correct

Wrong: check the row before HTTP, then update by ID alone. Correct: complete HTTP,
revalidate the actual dispatched source in a short transaction, then publish only
the committed fields while preserving newer cache configuration.

## Authoritative model discovery and saved previews

### 1. Scope / Trigger

Applies to browser model-list previews, manual/scheduled upstream model detection,
explicit per-key synchronization and Gemini/Ollama/Codex inventory requests.
Management endpoints remain specialized; text endpoint conversion is unrelated.

### 2. Signatures

`channelModelDiscoveryOptions` carries the explicit preview descriptor, original
saved `SourceType` and optional original `KeyIndex`.
`service.CodexModelDiscoveryPreview` carries pointer-valued Key, BaseURL,
HeaderOverride and Proxy plus SourceType.
`persistChannelUpstreamModelUpdate` compares the original source under
`model.UpdateChannelOtherInfoLocked` before writing discovery-owned fields.

### 3. Contracts

A saved preview retains its real ID for shared channel capacity. Reload the saved
row after each grant; latest saved cap, TLS and all unexposed settings remain
authoritative. Overlay only submitted fields on an operation-owned copy: omitted
base/header/proxy preserves the saved value, explicit empty clears it, and an
empty key preserves the saved credential. Advanced Custom previews intentionally
use the current enabled saved key. SourceType validates the original saved type
before an intentional draft type is applied.

Channel-wide authoritative discovery requires complete successful inventories
from every enabled key. Preserve the original full channel and key index across
plugin refresh/retry. Explicit `sync_key_models` retains its separate add-only
partial-success recovery contract; it never infers deletion from failed keys.
Strict inventories reject missing/null data and malformed or blank model items;
valid explicit empty arrays may be authoritative. Plugin discovery retains its
existing nonempty-result contract.

Carry caller/task cancellation through HTTP, admission and bounded persistence.
Compare source configuration in the short locked write, never perform HTTP in
the transaction, and merge only discovery-owned state into the current row.
Explicit sync changes MultiKeyModels while preserving current mode, polling
index and disable metadata. Reload the stored row before rebuilding abilities so
current group, priority, weight and tag are used.

Codex uses its backend-api model endpoint and access-token-only placeholder
substitution. A typed preview credential receiving 401 must not refresh or fall
back to another saved account. Saved-credential refresh retries once with the
same explicit noncredential overlays. GitHub version lookup has verified TLS,
no channel authentication/header overrides and cancellable shared-cache waiters.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Saved cap changes during admission | Regrant current capacity before sending |
| One enabled key fails authoritative discovery | No partial inventory or deletion authority |
| Explicit per-key recovery partially succeeds | Add successful scopes only; report failed keys |
| Source configuration changes before persistence | Reject stale result; preserve current row |
| Only scheduling metadata changes during explicit sync | Preserve current scheduling metadata |
| Canceled discovery | Stop upstream; no timestamp/result publication; release capacity |
| Typed Codex credential returns 401 | No saved-account refresh or fallback |
| Gemini pagination malformed/repeated/incomplete | Fail whole inventory within one deadline |

### 5. Good / Base / Bad Cases

Good: a saved preview uses a typed URL but current saved TLS/cap after waiting.
Base: unchanged configuration publishes the complete inventory. Bad: treating a
failed key or malformed response as proof that channel-wide models disappeared.

### 6. Tests Required

Run existing controller `TestChannelManagementModel*`,
`TestChannelManagementBrowserDiscoveryHonorsCancellation`,
`TestMultiKeyModelDiscoveryUsesOneKey` and `TestMultiKeyModelSyncPreviewAndApply`,
plus `TestCodexChannelManagement*`, Gemini inventory and Ollama suites. Require
real SQLite/MySQL/PostgreSQL and race gates in cloud; source review is not a pass.
Assert current transport/header/cap, original key index, no stale writes,
preserved scheduling/routing fields, complete inventories and cancellation.

### 7. Wrong vs Correct

Wrong: collapse the channel to a single-key copy, clear its ID, then overwrite a
full saved row with the result. Correct: retain source/index and admission
identity, reload current policy, then merge narrowly under the source guard.

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

## Panel critical rate-limit isolation

### 1. Scope / Trigger

Apply when adding console authentication, invitation registration, session
maintenance or other critical `/api` routes, or changing their 429 rendering.

### 2. Signatures

`middleware.CriticalRateLimitFor(CriticalRateLimitScope)` selects a closed
server-defined scope. `CriticalRateLimit()` is the panel compatibility wrapper.
`writeRateLimited` returns the standard `common.ApiErrorStatus` envelope.
The frontend shared `getServerErrorMessage` mapper translates generic HTTP 429.

### 3. Contracts

All critical counters remain keyed by trusted `ClientIP`, with the configured
critical maximum and window. Password, MFA, both passkey route families, OAuth
state/callbacks and password recovery share the existing `CT` login bucket.
Registration (including invitations), refresh, logout and panel operations use
four separate static scopes. Never select a scope from submitted usernames,
invitation codes, cookies, tokens, provider names or raw URL strings.

The outer global API `GA` counter is unchanged. Email-verification and verified
user limits remain additional controls; business-scope isolation does not make
the limits per session. A rolling deployment retains old shared limits on old
nodes until they are upgraded. Proxy configuration must preserve actual client
addresses without trusting arbitrary forwarded headers.

Generic rejection keeps HTTP 429 and `Retry-After`, with
`success: false`, `code: "RATE_LIMIT_EXCEEDED"`, English `message` and
`message_key: "Too many requests"`. Do not expose the IP, cache key or credentials.
The frontend retains specific server messages and authentication-safe wrappers;
only otherwise generic empty/HTML 429 falls back to the existing seven-locale
`Too many requests` key. Do not change authentication status codes to 200.

### 4. Validation & Error Matrix

| Condition | Required result |
| --- | --- |
| Registration scope exhausted | Registration returns 429; login/refresh/logout retain their own allowances |
| Login allowance exhausted | Every password/MFA/passkey/OAuth/recovery alias remains throttled |
| Same IP changes unverified cookies or tokens | No fresh allowance |
| Different trusted client IP | Independent allowance |
| Scope changes after GA is exhausted | Still 429 from the aggregate limit |
| Pre-upgrade Redis CT login key exists | Retain its count and remaining TTL |
| Backend sends a specific session-limit message | Preserve that message instead of the generic fallback |

### 5. Good / Base / Bad Cases

Good: invitation registration cannot consume session refresh's critical quota.
Base: each anonymous login alternative retains the existing IP protection.
Bad: using one key per endpoint, cookie or submitted account allows clients to
multiply an anonymous allowance; sharing registration with refresh causes
unrelated panel failures.

### 6. Tests Required

Extend `middleware/rate_limit_test.go` for memory and Redis scope separation,
shared stores, spoofed-claim resistance, GA ceiling and preserved login TTL.
Use actual `SetApiRouter` in `router/access_token_scope_test.go` to cover login
aliases, invitation registration and session isolation. Run these with the race
detector. Shared frontend mapper/notification tests cover empty/HTML 429,
specific backend errors, Chinese translation and safe authentication wrappers.

The R8 research note records OWASP Authentication/Session Management guidance
and ASVS 5.0.0 controls. This bounded fix does not claim complete ASVS compliance;
the pre-existing lack of a primary-password per-account failed-attempt counter
remains a separate security gap.

### 7. Wrong vs Correct

Wrong: every critical panel action spends `CT + ClientIP`, or a caller-provided
session identifier chooses a new anonymous bucket. Correct: server-defined
business scopes retain IP enforcement, all login aliases share CT, and GA
continues to bound aggregate panel traffic.
