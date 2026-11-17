# Channel admission and upstream failover design

Status: approved by the user on 2026-11-16; task activated and implementation dispatched to Pi.

## Change boundary and task structure

The missing behavior is admission by channel-wide occupancy and a configurable wait before an upstream begins responding. Existing failover must reliably choose another eligible channel when its policy allows. The owning boundaries are channel settings, request-policy snapshots, per-attempt routing/lifecycle, and outbound request transport. A response-writer-only timeout or a semaphore around distributor middleware would miss real attempt lifetime.

Concurrency, timeout and retry share controller attempts, cancellation and candidate selection. We considered independent child tasks; keep one integrated task with three exclusive implementation tracks so the same lifecycle is not independently changed and released by two children. Config/UI and service policy can run in parallel; controller/relay integration belongs to one worker after shared interfaces are fixed. Main owns task artifacts, integration review and release.

No model/credential deletion, authentication changes, billing-rule changes, waiting queues or remote-job accounting. Billing success/refund invariants must nevertheless be verified because timeout changes alter attempt exits. Read `.agents/rules/billing.md` in full before those paths are implemented or reviewed.

## Public configuration contract

| Owner | Key | Representation and behavior |
|---|---|---|
| Channel setting JSON | `max_concurrent_requests` | Optional nonnegative integer; absent/0 means unlimited. Positive values count all keys of this channel together. |
| Global request policies | `UpstreamFirstByteTimeoutSeconds` | Nonnegative integer seconds, proposed default 0 to preserve existing deployments. 0 disables this additional deadline; existing transport/lifetime safeguards remain. |
| Channel setting JSON | `upstream_first_byte_timeout_seconds` | `*int` in Go; missing/null inherits global, explicit 0 disables the added deadline, positive overrides global. |
| Existing global retry policy | `RetryTimes` | Proposed missing-value default 2, meaning initial attempt plus at most two retries. Preserve an existing saved 0 as an intentional no-retry setting. |

Cap values must be integers in 0..1,000,000; timeout seconds must be integers in 0..86,400, with identical UI/server validation and safe conversion to `time.Duration`. Runtime resolution reads the validated request-policy snapshot; do not introduce an unsynchronized mutable global or recreate HTTP clients for every request. Channel validation lives with `ChannelSettings` and is called by `model.Channel.ValidateSettings`; root JSON operations use `common`, relaykit uses its independent codec.

Data flow: existing channel form -> setting JSON -> create/update validation -> existing TEXT storage -> channel cache -> attempt policy. Global form -> request-policy PATCH -> transaction/validated snapshot -> attempt policy. Capture the effective global and channel values for each attempt; new configuration applies to new attempts, and a reduced cap does not terminate existing requests.

## Admission and release

Key occupancy by channel ID outside cached Channel objects. An acquisition returns a lease with an idempotent release and an attempt cancellation signal. Admission failure has a distinct local-capacity classification. It bypasses health failure counters, automatic disabling, upstream-error logging and any upstream call.

Resolve candidate -> try acquire -> if busy, add to request-scoped busy exclusions and select another eligible candidate. Keep group, model, endpoint, priority and weight filtering. Busy candidate traversal does not consume the upstream retry budget and must terminate when eligible candidates are exhausted. Return 429 if eligible candidates were all busy. Pinned/strict routes cannot silently switch and return their capacity rejection. Use a separate exclusion set from optional failed-channel avoidance.

Hold a slot across the entire upstream HTTP response/stream. Release after upstream processing ends and the body/goroutines are closed, also on errors, client cancellation and panic cleanup. Release the previous attempt before the next acquisition. Apply the shared lease API to ordinary controller relays (including AWS SDK invocation inside DoResponse), channel-test requests and asynchronous submission requests; an accepted remote job no longer occupies a slot after its HTTP response completes. Upgraded upstream connections occupy one slot for their live connection, with one acquire per connection rather than per WebSocket message. Admission must precede client upgrade where HTTP 429/fallback is still possible; an already-upgraded downstream connection uses its existing protocol error. Do not label remote jobs or multiplexed messages as ordinary HTTP concurrency.

No Redis: a process-wide mutex/counter with owner tokens and `sync.Once` release. Shared Redis: atomic capacity check and owner-token admission, owner-specific renewal/removal and stale lease reclamation. A configured shared-counter failure must fail closed for limited channels, never silently create a separate local budget. Lease renewal loss cancels the active attempt before another normal attempt is admitted. Tests must explicitly cover lost/stale owner release, expiry and renewal; a cancellation request cannot guarantee a remote provider has stopped work. Config propagation across gateway nodes follows the existing refresh interval and must be documented in the UI/help scope rather than claiming instantaneous global cap changes.

The guarantee is gateway admission while leases are valid, not an absolute remote-provider execution count across process pauses/network partitions. An expired Redis owner cannot delete a later owner's slot. Loss of Redis coordination produces a distinct local availability error (503) for a limited channel, not a false busy-429 or channel-health failure.

| Entry point | Concurrency unit | New first-byte timeout |
|---|---|---|
| Ordinary HTTP JSON/form, Responses/Claude/Gemini | One active upstream attempt through body close | Yes |
| AWS SDK generation | One controller generation attempt, including SDK invocation in DoResponse | Channel-selected HTTP client; the new typed first-byte timeout bypasses SDK internal retry and returns to outer channel selection |
| Administrator channel test | One active test attempt through body close | Yes for shared HTTP client paths |
| Async task submission | One in-flight submission HTTP attempt, not remote job lifetime | Yes for channel-aware shared HTTP transport; preserve detached plugin execution policy |
| Realtime upstream WebSocket | One live upstream connection | No new body-byte/handshake deadline |
| Responses upstream WebSocket | One live upstream connection, shared across its sequential messages | No new body-byte/handshake deadline; admission at the actual upstream dial/close boundary |
| Background task polling/artifact fetching/model discovery | Existing policies; not generation admission in this delivery | No new channel override unless already using the channel transport for a scoped submission |

## First-response timeout

The timer covers waiting for the first upstream response-body byte, including headers with a stalled body. It is not a total generation duration and does not promise the first semantic token: SSE metadata or an upstream heartbeat is still a byte. UI copy must say what is measured.

At the shared outbound boundary, wrap the final direct/proxy/sharded RoundTripper, derive a cancellable context from the incoming request and attach it to the actual outbound request. Start the additional first-byte timer, perform the request, read and preserve at most one body byte before handing the response to the existing adaptor handler, then stop the timer. Empty EOF/http.NoBody also ends the wait without inventing an error. Reconstruct the body without losing that byte, Close behavior or cancellation ownership; cancellation remains attached until EOF/error/Close. Synchronize timeout-vs-first-byte completion and forward CloseIdleConnections. Include effective seconds in cached transport policy keys so global/channel changes cannot reuse the wrong timer. A timed-out read closes/cancels upstream and returns a typed retryable upstream-wait error before response handling or settlement. Do not implement a goroutine that keeps reading after its attempt has been returned/released.

The ordinary request path currently uses `http.NewRequest` without the incoming request context and starts ping before upstream response (`relay/channel/api_request.go`); both must be accounted for. Do not emit gateway SSE keepalive while a bounded pre-response attempt may still switch. Once response delivery is committed, retain existing stream idle timeout, cancellation and no-retry behavior. Provider SDK paths need explicit context and first-response integration, not an assumption that replacing `c.Request.Context` is sufficient.

Headers-only transport `ResponseHeaderTimeout` remains useful but does not satisfy this feature by itself. `http.Client.Timeout` is unsuitable because it can truncate a healthy stream. Do not apply a controller timer and convert its result after a handler has already settled an empty stream as success. AWS SDK uses the channel-selected client and is covered at transport level; customize its retry classifier only for the new typed first-byte timeout so SDK internal retries cannot multiply that timeout before channel switching. Other SDK retry rules remain intact. WebSocket handshake/message timeout is excluded from this first-body-byte option. In particular, a 101 upgrade must bypass body pre-read and preserve the upgraded stream interface. Existing plugin submission cancellation/detachment behavior must be preserved.

## Retry and error origin

403/404 are already in default retry ranges. Preserve parsing/status origin and add end-to-end fixtures proving an upstream removed-model 404 and disabled-upstream-group 403 select B after A. Preserve local credential/group rejection before relay; status text alone cannot authorize bypassing local authentication. Do not blanket-remove SkipRetry.

Remove the permanent 504/524 exclusion and include both in the default retry ranges. Existing custom ranges remain authoritative. The sole legacy-default migration match is the stored `AutomaticRetryStatusCodes` value `100-199,300-399,401-407,409-499,500-503,505-523,525-599`; replace it with `100-199,300-399,401-407,409-499,500-599`. Perform a conditional GORM update by key AND old value before startup option hydration; do not migrate on GET, ordinary config sync or a later save that intentionally restores the old range. Record completion with an internal option migration marker in the same transaction, so subsequent restarts do not overwrite an administrator's later choices. Missing or custom values are preserved. Verify fresh state, upgraded state, custom/saved-zero state and two consecutive startups on all three databases.

A locally generated first-byte timeout has a dedicated error code/retry reason and retries independently of the status-code range or presentation mapping when attempts remain. It still respects explicit skip-retry/local validation, pins, strict sessions, client cancellation and response-started boundaries. An upstream-returned 504/524 continues to use the configured range. Keep saved RetryTimes=0; missing values use the proposed default2. No code change can make a deliberately disabled retry policy switch without a policy update, and the UI must state this directly.

For the pinned AWS SDK v2 dependency, let the typed transport timeout implement `RetryableError() bool` returning false. The SDK's default retry classifier checks that interface through `errors.As` before generic connection errors; it prevents a same-channel SDK retry while preserving the typed error chain for gateway failover. Do not translate that SDK hint into the gateway's SkipRetry flag. Verify with the actual SDK fixture that a timeout sends one attempt and ordinary transient errors retain existing retry behavior.

When failed-channel avoidance is enabled, the next attempt must use an untried eligible channel; return exhaustion instead of randomly reusing a failed channel. Preserve explicit disabled avoidance semantics and show their effect in the settings UI. Capacity exclusions are always hard for the current request. Failure to find another candidate must retain the last useful upstream error rather than replacing it with an unexplained internal error.

Existing policy events already record stop reasons. Extend them only as needed for capacity and first-byte timeout; never include keys, usable tokens or request bodies. UI must clearly show that retry count 0 prevents automatic error failover even when a timeout is configured. Production policy has not been inspected, so no exact cause is claimed for the reported 403/404 incident.

## UI and ownership

Reuse existing FormField, FormControl, FormDescription, Input and select controls. No new generic UI primitive is required. Channel cap and inherit/custom timeout belong in the existing sensitive settings fieldset and explicit serialization/configured-state/error-location paths. A user without sensitive-write permission sees disabled fields. Global timeout belongs in Request Policies beside retry settings and uses the existing root-only endpoint.

Frontend worker owns `web/src/features/channels/`, `web/src/features/system-settings/request-policies/` and locale additions via the required i18n script. Settings worker owns `relaykit/dto/channel_settings.go`, `model/channel.go`, `model/request_policy.go`, relevant option hydration, and existing settings round-trip tests. Relay worker exclusively owns `controller/relay.go`, service admission/selection/retry, outbound relay request paths and lifecycle regressions. Exact test-file ownership is recorded at dispatch to prevent collisions.

## Verification and release boundary

Use controlled httptest upstreams: A holds, errors or returns only headers; B succeeds. Test actual selected channel and maximum active requests, not private constants. Extend existing parser/retry, relay stream, channel settings and UI tests; at most one new Go test file is needed for the new admission service. Use real SQLite/MySQL/PostgreSQL for affected config persistence, and real Redis for distributed semantics. Record exact engine versions and command results; a skipped external DB test is not a pass.

Release is authorized by the user: Conventional Commit, safely push the approved commit to `fork/main` after checking ancestry, and verify `.github/workflows/docker-fork.yml` (triggered by push to main) through validation, build, publication and release. Its prepare/release jobs derive and create `custom-YYYYMMDD-sha`; use and verify that tag instead of racing the workflow with a duplicate manually created tag. Do not force-push or move an existing tag. Successful artifact CI is distinct from deployment on the user's server. Rollback is the previous release tag plus disabling new settings; no schema column is added.

## Planning risks and checks

- SDK/WebSocket coverage and Redis lease behavior require explicit implementation review; unsupported behavior cannot be hidden behind the generic channel UI.
- Three-database and Redis verification tools are not available on the current PATH; discover existing isolated tool locations/services or provision isolated test instances before acceptance.
- Automatic switching requires an eligible alternate and an enabled retry budget. Saved administrator policies remain meaningful.
- Final user review completed and task started on 2026-11-16. Implementation and release are authorized.
