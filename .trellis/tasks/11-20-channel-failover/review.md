# R7 independent review — work in progress

Reviewed 2026-11-20 by root/r6_review. This is an intermediate review of p9's
unfinished work, not a final acceptance result. Root owns the index and commits.

## Required corrections and suggested ownership

1. **Attempt gate must precede upstream dispatch.** Installing the gate only in
   `OaiResponsesStreamHandler` does not intercept the existing keepalive in
   `relay/channel/api_request.go` or the outer Claude keepalive in
   `controller/relay.go`. A delayed HTTP 429 can still be committed as 200 before
   the handler starts. Install one attempt gate before those paths and restore
   it only after all attempt writers have stopped. Retain the ordinary Written
   retry guard. R8 should place a distinct comment-only envelope outside this
   attempt gate, with one serialized writer; do not reinterpret arbitrary
   committed protocol output as retryable.
2. **One heartbeat owner, synchronized shutdown.** `StreamCommitGate.keepAlive`
   writes and flushes the raw writer after releasing its mutex, racing the
   scanner's data writes. It also duplicates the scanner heartbeat and does not
   wait for its goroutine in Finish. Reuse the existing scanner heartbeat through
   the attempt gate; timer-driven gate commit must share the write lock and stop
   before handler return. Test Finish while a heartbeat is pending with a
   deterministic writer barrier and run the Linux race check.
3. **Actual time bound.** Checking deadline only inside Write does not impose a
   2-second wall-clock bound on a silent upstream. A timer or existing attempt
   deadline must make that state transition explicitly; canceled/finished gates
   must not write later. Byte bounds alone are insufficient.
4. **Normalize terminal errors before committing.** Native and converted paths
   must handle `type:error`, response.failed/response.error, and failed status in
   a terminal response envelope. Existing ObserveResponsesOutcome knows the
   variants, but the native gate currently commits some of them first. Preserve
   the upstream code and message. Include the user's exact message-only case
   (`stream disconnected before completion: concurrency limit exceeded for user`)
   in policy tests. An arbitrary prompt/text substring must not classify traffic
   as busy; a narrow recognized upstream-error message can be considered only
   after structured codes/types, and never drives auto-ban.
5. **Known busy channels need independent exclusion.** The current retry change
   only renames the decision. FailedChannelIDs is ignored when
   RetryAvoidFailedChannels=false, so the same busy channel can still be picked.
   Give recognized concurrency rejections their own per-request exclusion that
   candidate selection always honors, while preserving pins, strict affinity,
   client cancel, output-commit and attempt-budget boundaries. R8 consumes this
   state for all-busy waiting, including channels without local caps.
6. **Preserve actual usage.** Native earlyFailure returns before accumulator
   observation. A failed terminal envelope with nonzero usage/output is therefore
   discarded and may be refunded/replayed. Such an attempt must remain billable
   and non-replayable; a zero-usage early refusal can discard the init buffer and
   retry. Test exact wallet/log usage for both forms and tool-only output.
7. **Protocol-correct errors after output.** Converted failure currently appends
   `c.JSON` to SSE; native premature EOF after output returns usage,nil without a
   client terminal error. Keep actual partial usage and emit the incoming
   protocol's terminal error event, with no replay. Required-terminal EOF must be
   checked in native and converted handlers, including buffered SSE-to-JSON.

## Current validation

Focused service classifier/retry tests and OpenAI Responses tests passed against
the intermediate tree. A broader controller regex also ran Responses WebSocket
suites and failed; no final gate is claimed. R7-specific end-to-end tests must be
rerun after author handoff. Real database and Redis gates remain owned by final CI.

## Service classification and exclusion handoff

p9 explicitly released four files to this reviewer. Changes completed in
`service/relay_upstream_failure.go`, `service/relay_error.go`,
`service/relay_error_test.go`, and `service/request_policy.go`:

- `ClassifyUpstreamError(code,type,message)` accepts only upstream error-envelope
  fields. Structured categories win; explicit quota beats contradictory
  concurrency metadata. A maximum-512-byte exact known concurrency message has
  a narrow fallback with the reported stream prefix, optional period and
  case/outer-whitespace normalization. Specific unrelated codes and arbitrary
  quoted/negated/suffixed prose do not match.
- `ClassifyUpstreamRejection` excludes local/transport errors, preserves HTTP402
  as quota, and keeps generic429 as rate limiting.
- `RecognizedUpstreamRejection` omits generic429 fallback for existing policy-log
  compatibility. Existing code-only API remains available.
- `RequestPolicyState.UpstreamBusyChannelIDs` supplies independent candidate
  exclusion even when ordinary failed-channel avoidance is disabled. The
  written/pin/strict/cancel/status/attempt-budget stop guards remain intact.
  R8 may deliberately clear or reconsider this set only after its wait gate.
- Focused classifier, retry, capacity and existing selection tests passed:
  `go test -p 1 ./service -run 'TestClassifyUpstream|TestUpstreamConcurrency|TestDecideRelayRetryReasons|TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups|TestChannelCapacity' -count=1`.
  `go vet -p 1 ./service`, gofmt and scoped diff-check passed. Redis subtests
  require TEST_REDIS_ADDR and were skipped locally.

No commit or staging performed. Gate/handler/output usage issues above remain
with p9 and require its completed handoff plus final review.

## Regression matrix to complete

- Actual request path with ping enabled, slow HTTP429 and HTTP200 early failure:
  one eligible alternative, one clean answer, no first-attempt ID leakage.
- Native/Chat/Messages conversion: created + error; top-level error; failed
  terminal envelope; silent/early EOF; complete empty answer; productive delta
  then failure/EOF; byte/time-bound commit; client cancellation.
- Classifier: known codes/types, explicit quota precedence, generic429, known
  anchored message-only concurrency, misleading prose and normal output.
- Retry with failed-channel avoidance disabled; same-priority candidates;
  exhausted alternatives; pinned channel; strict affinity; zero retry budget.
- Failure carrying exact nonzero usage/tool calls must settle once and never
  retry; ordinary zero-usage concurrency failure must not be charged twice.

## R8 coordination handoff — interface and verification recommendations

Root will dispatch the implementation separately. This section records review
recommendations, not implemented behavior or a finalized public interface.
The parent design remains authoritative over earlier research proposals.

Suggested ownership: the R8 coordinator implementer owns a new service queue
implementation, its focused tests and the minimal lease-release hooks in
`service/channel_capacity.go`. p9 owns controller orchestration and the outer
comment-only stream envelope. Coordinate edits before touching the existing R7
classification, retry and policy files, which have been released back to p9.

Interface shape to agree before implementation:

- One request-scoped waiting handle, created with the request context and
  authenticated user ID. Enqueue atomically checks coordinator and per-user
  limits (defaults 1,000 / 20), and fixes the 300-second maximum wait budget;
  repeated wake/requeue cycles must not reset the budget or add queue entries.
  Make the approved bounds configurable through the existing settings pattern.
- Wait receives the currently eligible candidate IDs and distinct busy sources
  (local capacity versus recognized upstream concurrency). Cancellation and
  Close are idempotent and remove all ticket/probe ownership. Report overflow,
  deadline and coordinator outage distinctly; do not label outages as capacity
  saturation. Do not accept arbitrary error-message text as a queue trigger.
- A wake is either an explicitly owned admission/probe opportunity or a prompt
  to re-evaluate policy. Define this distinction in the return type. A plain
  broadcast followed by unrestricted AcquireChannelCapacity would let newcomers
  overtake FIFO waiters and can issue many upstream probes at once. Preserve
  FIFO among eligible waiters using an atomic recheck or reservation, and return
  any raced reservation when cancellation or policy changes prevent use.
- Register the waiter before the final capacity recheck so a release between
  the initial failure and parking cannot be lost. A granted candidate still
  requires current group/model/key/goal/pin/strict-affinity validation in the
  controller; queue state is never authorization. Do not clear ordinary failed
  candidates or upstream-busy exclusions until the owning wait gate permits a
  deliberate retry.
- Local release notifications must wake bounded eligible work. Redis requires
  shared notifications, expiring tickets and bounded recovery polling, not a
  blocking connection or hot polling goroutine per waiter. Never trim live
  tickets. Cluster queue/user limits and probe ownership must be atomic across
  instances, with no fallback to independent local admission on Redis failure.
- Unknown upstream capacity uses one probe per channel, bounded jittered
  backoff and meaningful Retry-After. Generic429, quota and transport failures
  remain ordinary retry/error behavior. Do not describe upstream probes as an
  exact measurement of externally occupied capacity.
- Keep queue lifetime separate from each attempt's first-byte timeout. Initial
  local queueing precedes billing preparation; later upstream-busy waits reuse
  the existing request billing reservation without a second reserve. Actual
  billable partial output/usage prevents requeue. The waiting service owns no
  response writer; p9's controller owns serialized SSE comments and terminal
  protocol errors after the HTTP200 keepalive boundary.

Required deterministic verification:

1. Local FIFO with two waiters and one released slot; unrelated candidates
   continue; a new request cannot steal a queued admission opportunity. Include
   release between failed admission and waiter registration/recheck.
2. Cancellation before park, while parked and after grant; Close twice; a grant
   abandoned after changed policy returns its reservation and wakes the next
   eligible waiter. Verify zero leaked active leases, tickets and probes.
3. Queue and per-user caps at exact boundaries; a different user can use spare
   global capacity. A repeated upstream-busy cycle cannot restart the deadline.
   Use controllable clocks/notifications rather than sleep-based stress loops.
4. Real Redis parity across two coordinator instances, notification loss and
   bounded recovery, abandoned ticket expiry, coordinator failure, cancellation
   and atomic single-probe admission. Run the Linux race detector; local tests
   and mocks are not substitutes for the Redis gate.
5. Recognized concurrency without a local cap waits and retries one probe;
   generic429/quota does not enter the queue. Honor bounded Retry-After and
   prevent unrelated channel releases from bypassing a busy probe's backoff.
6. Controller coverage for pinned single-channel and strict affinity waiting,
   live eligibility changes, client cancellation, all-busy exhaustion, and one
   clean final answer after comments-only waiting for native Responses, Chat
   and Messages. Non-stream requests must never receive SSE comments.
7. Exact quota/wallet/log assertions: no initial queue reservation, one existing
   request reservation across an upstream-busy wait, one settlement/refund, and
   no replay after actual partial usage or exposed attempt output.

## CI regression correction (ce9533537)

GitHub CI run 44662610522 failed only in the root test step at
`pkg/jsplugin/routing_test.go:1259`: the new console paths `/access` and
`/service-status` were absent from `reservedRouteNamespaces`. Root/module vet
and build, frontend checks, and the database/Redis integration job succeeded
in that run. The precise failing test was reproduced locally before the fix.

Added the two reserved paths in `pkg/jsplugin/routing.go`; existing routing
coverage now protects the console pages from plugin route interception.
No new plugin API fields, schemas or metadata were introduced. Locally passed:
`go test -p 1 ./pkg/jsplugin -count=1`, `go vet -p 1 ./pkg/jsplugin`,
`go build -p 1 ./pkg/jsplugin`, gofmt and scoped git diff-check.
No staging or commit performed; root owns the final requirement commits.

## Paused stream-fix handoff after memory exhaustion (2026-11-20)

Native/converted reviewer edits are preserved, but no complete-tree cloud run
has covered them yet. Do not infer a passing gate from their partial reports.
The next implementation pass must resolve these bounded items:

- `StreamCommitGate.Finish` must be idempotent. In the current draft, the first
  successful `timer.Stop()` leaves `timerDone` open, so a second Finish sees
  Stop=false and can wait forever. Serialize finish and define one timer join.
- `common.CustomEvent.Render` rewrites response headers per frame. Gate timer
  commits and comment flushes can race that map write. The SSE helpers need
  complete-frame writes through the existing wrapper chain after setting
  headers once; preserve JSON encoding, event/data newline semantics and flush
  errors. Do not bypass plugin Reply/Report wrappers.
- Keep writer order gate -> Reply -> serialized envelope sink -> connection.
  Review header/status/size access, bounded write deadlines and stop/join on
  canceled or slow clients. Final error rendering must happen before the
  envelope is closed; a retained Report writer must not swallow it.
- Native Responses and converted Chat/Messages failures after any output or
  known usage must settle once without replay. Pre-output, zero-usage upstream
  failures may retry. Initialization-only role/message_start frames must stay
  held. Final native Responses errors use `response.failed`.
- Existing helper tests must not poll `httptest.Recorder.Body` concurrently
  with timer or keepalive writes. Use serialized observation or join first.

Portable formatter: `D:/AppData/Local/Temp/new-api-build-tools/go/bin/gofmt.exe`.
No local execution of tests/build/lint is authorized. Root will send these
paths to the existing full GitHub CI and Linux race jobs.

Separate-commit staging inputs: the R7-only controller snapshots are under
`D:/AppData/Local/Temp/r7-split/r7-only/` and require refreshing with final
native/helper repairs. Pre-model-unavailability copies of the four changed
service files are under
`D:/AppData/Local/Temp/new-api-r7-before-model-unavailable/`; use those to keep
the new persistence behavior out of the original R7 commit.

## Final native/helper/converted stream handoff (2026-11-20)

Focused fixes are complete in the working tree; no files were staged or committed.

- The attempt gate serializes writes, metadata reads and flushes; keeps the first
  write/flush error; applies bounded client write deadlines; and makes Finish
  idempotent whether the commit timer was stopped or already fired.
- The request envelope joins each keepalive cycle and closes only after final
  error rendering. Status/Size/deadline access share the connection lock.
  Retained stop closures and Finish share the same close operation.
- Shared Claude/Responses/data SSE helpers send complete frames and set headers
  once, preserving existing CR/data newline encoding and wrapper traversal.
  Generated converted terminal errors no longer use per-event CustomEvent
  header rewrites.
- Controller gate installation leaves Hold to protocol-aware handlers. This
  avoids dropping converted Chat-to-Responses output whose handler does not
  implement the hold/commit contract. The native and Responses-to-Chat/Messages
  handlers explicitly hold framing.
- Native and converted paths capture usage before converter/commit/write errors.
  Billable failures return usage with nil handler error for one normal host
  settlement, retain failed StreamStatus and do not replay. Native terminal
  failures use response.failed. Missing terminal plus bare DONE is truncation.
  Hosted calls in a terminal-only failed envelope use the existing deduplicator.
- A late lease error no longer changes a handler's already-settled nil-error
  result into a refundable error.
- Existing regression suites cover idempotent Finish, deterministic frame
  overlap, multiple keepalive cycles, deadline/flush errors, SSE encoding,
  unknown-handler pass-through, exact failed usage and terminal truncation.
  Removed recorder polling races, artificial repeated-write stress and sleeps.
  Adapted lifecycle assertions for one complete frame per Write.

Formatting only:
D:/AppData/Local/Temp/new-api-build-tools/go/bin/gofmt.exe -w on owned Go files.
NO local tests, build, lint, type-check or browser verification was run, per
the user's instruction. Root must run full CI and the Linux race job; exact
host wallet/refund/log assertions remain a cloud verification gate, not a
claimed local pass.

R7 staging: D:/AppData/Local/Temp/r7-split/r7-only/controller/relay.go now includes
the request envelope/defer fixes, protocol-correct final error, opt-in Hold and
late-lease settlement guard. Do not overwrite live controller with that file.
Older files/ copies and patch bundles in the split directory are stale: stage
the final live helper/common/native/converted files plus this controller
snapshot; R8 then adds the live controller queue changes. Model-unavailable
eligibility work belongs to p11 and was not edited by this reviewer.

## Final bounded independent check (2026-11-20)

Read-traced the final stream gate/envelope lifecycle, Reply and Report writer
order, native and converted failure usage, controller retry boundaries, and
local/Redis queue grant/cancellation cleanup. No additional production blocker
was confirmed in these paths. Cloud execution remains the validation gate.

Extended the existing `TestRequestPolicyUpstreamHTTPFailover` paid fixture to
its three stream rows: early explicit failure, early terminal truncation, and
failure after productive output. Each now asserts one shared reservation,
exact user/token debit and exactly one consume log for the productive channel.
The log lookup is scoped by fixture token, channel IDs and consume type, so
earlier subtest records cannot contaminate the assertion. No new test file.
The R7-only controller test snapshot was synchronized; these test additions
have no model-unavailability dependency.

Before the model reviewer requested per-user queue filtering, the final R8
controller was copied to
`D:/AppData/Local/Temp/new-api-r8-controller-relay.go`. The only later live
controller change passes `c.GetInt("id")` to `ChannelSatisfiesFilters` in
`channelWaitEligible`; stage that one-line difference with the separate
model-unavailability requirement, not R7/R8. R7-only controller snapshot stayed
unchanged during this independent check.

Formatting: gofmt on the edited live/snapshot controller test. No local lint,
typecheck, test, build or browser verification, as instructed. Add
`TestRequestPolicyUpstreamHTTPFailover` to the existing Responses SQL DSN matrix
alongside `TestChannelWaitRouting` so the exact stream billing assertions run
on MySQL and PostgreSQL as well as the root suite's SQLite fixture.

## Cloud CI 44731068705 corrections (2026-11-20)

The cloud run disproved the earlier static-only writer lifecycle conclusion.
`doRequest` installed a keepalive outside the attempt gate, but stopping it
did not detach it. Gate Finish could not remove the inner closed gate, so a
later successful attempt settled correctly while its response was swallowed.
The owner stop now joins the keepalive and removes only its own outer wrapper.
The existing helper suite covers two attempts and nested keepalive ownership.

`TestChannelCapacityStreamsRouteAroundFullChannelAndRelease` now sends a real
delta before blocking, observes a comment while all channels are full, then
releases the fallback lease and requires that same queued request to finish.
The old immediate-429 expectation was incompatible with approved R8 waiting.
The Responses HTTP accounting table now explicitly expects no exposed framing,
a 502 error, full refund and no consume log for four failures before work;
the following successful request charges once. Existing WebSocket and partial
work expectations remain unchanged. These corrections were handed off to root
and committed as `7112215db`.

The follow-up read found two adjacent boundaries, also repaired: keepalive
comments before protocol Hold are buffered under the same byte/time bounds
(unknown handlers still commit on their first content), and both native and
converted Responses preserve HTTP refusal Retry-After. Converted Responses
also installs its gate before DoRequest so transport pings cannot get ahead of
the handler's gate. Existing helper tests
cover comment discard/commit and unknown-handler pass-through; the existing
relay conversion test file now covers real HTTP 429 Retry-After transfer for
native and Chat-to-Responses paths. The backend error-handling spec is synced.

Only static source/diff review and gofmt were performed. No local tests, build,
lint, typecheck or browser verification. These changes require a new cloud CI
run, including the existing race and SQLite/MySQL/PostgreSQL suites; no pass is
claimed for the new revisions before that run completes.

## Cloud CI 44736079684 logger race correction (2026-11-20)

The integration job passed its database and relay-policy steps on SQLite
3.50.4, MySQL 8.4.13, PostgreSQL 16.16 and Redis 7.4.12 (Go 1.26.10).
The final race step failed only in
`TestChannelCapacityStreamsRouteAroundFullChannelAndRelease`: concurrent
request completion and cancellation both reached the unsynchronized
`logCount++` in `logger/logger.go:114`. The relay/channel, relay/helper and
relay/channel/openai race packages passed. This evidence does not indicate a
stream gate or channel-queue race.

With root approval, the shared logger count now uses `atomic.Int64` and the
rotation scheduling flag uses `atomic.Bool`. The counter increment/reset,
compare-and-swap scheduling decision and SetupLogger completion store are
all atomic; the existing file rotation and writer locks remain intact.
The existing cloud capacity scenario reproduces the production concurrency
path, so no synthetic logging stress test or duplicate test file was added.

Static inspection and gofmt only. The correction still requires the next
cloud race run; no local tests, build, lint, typecheck or browser verification
were executed.
