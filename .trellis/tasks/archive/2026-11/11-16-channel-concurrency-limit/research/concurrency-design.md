# Channel concurrency semaphore — design research

Coordinator override: the approved design.md is authoritative. In particular Redis outages FAIL CLOSED (503 for coordination failure), renewal loss cancels active requests, and acquisition must carry context/cancellation rather than the preliminary no-context signature below. Do not implement the fail-open recommendation in this research. The integrated task also includes a one-time option-default migration even though channel fields need no schema migration.

Date: 2026-11-16. Read-only design research. No product files, git, or config changed.
Scope: semaphore/admission design only. Global and per-channel upstream timeout and the
retry/status-exclusion policy are researched by other agents and are **not** decided here.

## 1. Confirmed requirements (from PRD + user)

- Admin-configurable per-channel cap on simultaneous upstream requests.
- All keys of a channel share one channel-ID count.
- `0` (or absent) = unlimited; preserve today's behavior for unlimited channels.
- Counting unit = a live upstream HTTP attempt, held for the full lifetime of a stream
  until completion, cancellation, or close. Not per-minute.
- Saturation: pick another eligible channel; if every eligible channel is saturated,
  return HTTP 429. No waiting queue. Saturation is **not** a health failure and must not
  disable the channel or add upstream-failure health state.
- Release after success, error, cancellation, or retry with no capacity leak.
- Must be correct across instances when Redis is shared, with an explicit statement of
  what cannot be guaranteed.

## 2. Counting model and configuration

- Config lives in the existing channel `setting` TEXT JSON (`Channel.Setting`,
  `model/channel.go:51`). DTO owner `relaykit/dto/channel_settings.go` (`ChannelSettings`).
  Validation owner `model/channel.go` `ValidateSettings` (`:1169`). The config agent owns
  the exact field name/type/default; this design assumes a non-negative
  `max_concurrent_requests` with `0` = unlimited.
- Never store occupancy on the cached `*Channel`. `InitChannelCache`
  (`model/channel_cache.go:149`) rebuilds channel objects, and `CacheUpdateChannel`
  replaces them; a counter on the object would reset/leak. Key the semaphore by channel ID
  in a package-level structure.
- Read the limit cheaply per attempt. `SetupContextForSelectedChannel` →
  `applySelectedChannelContext` already stores `channel.GetSetting()` in
  `ContextKeyChannelSetting` (`middleware/distributor.go` ~`:735`). Read the limiter from
  that context key, not a fresh unmarshal on the hot path. (`GetSetting()` unmarshals every
  call and can `Save()` the row on a parse error — avoid per-request calls.)

## 3. Semaphore interface (recommended)

New file `service/channel_capacity.go`, package `service`.

```go
// AcquireChannelCapacity reserves one live upstream attempt for channelID.
// limit <= 0 means unlimited and returns an idempotent no-op release with ok=true.
// ok=false means the channel is at capacity; err != nil means the distributed
// backend failed (callers decide fail-open/fail-closed; see §6).
func AcquireChannelCapacity(channelID, limit int) (release func(), ok bool, err error)

// ChannelCapacityAvailable is an advisory, non-reserving probe for routing.
// It always returns true when limit <= 0. A false result is not a reservation.
func ChannelCapacityAvailable(channelID, limit int) bool
```

Properties:
- Non-blocking. No queue, no wait loop, no context timeout.
- `release` is idempotent (`sync.Once`) — mirror
  `taskArtifactAccessLimiter.acquire` (`middleware/task_artifact_access.go:79`), which is
  the closest existing local counter pattern. Unlike that limiter, the key is a channel ID
  and the structure is owned by `service`, not `middleware`.
- Local backend: `sync.Mutex` + `map[int]int` counts.

Do **not** reuse these as the capacity primitive:
- `service.AcquireLogExportLock` (`service/log_export.go:33`): single-slot, non-idempotent
  release, ignores renewal failures. Only the owner-token + `PEXPIRE` renewal Lua shape is
  worth copying, not the semantics.
- `common.RedisIncr` / `RedisHIncrBy` (`common/redis.go`): return `nil` without a TTL and
  are a bare counter; a plain INCR/DECR leaks a slot on process death and cannot reclaim.
  A capacity counter needs expiry/lease semantics.
- `common/limiter` (`common/limiter/limiter.go`): token-bucket rate limiting (per-rate, not
  occupancy); the wrong shape.

## 4. Lifecycle boundary around relay attempts (critical)

The hard requirement is that a slot is held from just before the upstream attempt to the
moment that attempt's response is fully consumed/closed. Two candidate boundaries:

**Chosen: per-attempt acquire/release, released when the attempt handler returns.**
Every relay attempt funnels through a handler call that blocks until the stream ends
(`StreamScannerHandler` runs the stream to completion and `cleanup()` closes `resp.Body` at
`relay/helper/stream_scanner.go:135`), or until a non-stream body is fully read. So:

```go
// inside the retry loop of controller/relay.go Relay()
release, ok, _ := service.AcquireChannelCapacity(channel.Id, limitFromContext(c))
if !ok { /* local saturation: exclude + reselect, or 429; see §5 */ }
func() {
    defer release()
    newAPIError = relayHandler(c, relayInfo)   // blocks for the whole stream
}()
```

A closure with `defer release()` is required so release runs at iteration end, not function
end. A panic still runs the deferred release (and the existing recover in `Relay()` still
sees it). This is deterministic and does not depend on every caller closing `resp.Body`.

**Rejected: wrapping `resp.Body` inside `relay/channel/api_request.go` `doRequest`
(`:457`).** It would cover all HTTP paths in one place, but release correctness would then
depend on every caller closing the body (adaptors read non-stream bodies and streams are
closed by `StreamScannerHandler`). Any missed close leaks a slot until the lease expires.
Use the loop boundary instead. Keep `doRequest` unaware of capacity.

**Rejected: putting capacity into channel routing filters.** `GetRandomSatisfiedChannel` /
`filterCandidateIDs` (`model/channel_cache.go`, `model/channel_constraint.go`) run under
`channelSyncLock.RLock()`; a Redis Lua call there would block routing under a lock, and the
probe is racy anyway. Keep acquisition outside the model lock.

### Exact integration points

- HTTP relay: `controller/relay.go` `Relay()` retry loop (loop begins ~`:138`; handler
  dispatch switch ~`:180`). Acquire before the `switch`, release around it.
- First attempt: the channel comes from the distributor via context; `getChannel()`
  (`controller/relay.go:283`) takes the `info.ChannelMeta == nil` branch and returns it.
  Capacity handling must therefore live in `getChannel`, not only around
  `CacheGetRandomSatisfiedChannel`.
- Task submission: `controller/relay.go` `executeTaskSubmissionWith` (`:~470`) has the same
  loop, plus a `relayInfo.LockedChannel` branch. Hold the slot only around
  `submit(c, relayInfo)` (the submit HTTP request); release before persistence/settlement.
- Channel test: `controller/channel-test.go` `runChannelTestRequest` calls
  `adaptor.DoRequest` directly (`:~430`). See §7.
- Responses WebSocket: `relay/responses_websocket.go` `runCall` target-dial loop
  (`:~305`). The upstream `*websocket.Conn` is long-lived and reused across client
  `create` events on one client connection; register with
  `s.registerChannelClose(channel.Id)` (`:~767`). The slot must live for the **target
  connection**, not one create. This is a genuinely different boundary and the highest
  risk. If a per-connection slot is too coarse, document that WS counts one slot per
  upstream socket rather than per logical response.
- Async task polling (`service/task_polling.go`) is a separate short-lived HTTP request
  stream. Recommend **not** counting polling against the cap: polling is frequent and
  does not hold a generation stream. Count the task submit request only.

## 5. Saturation routing and the 429

Recommended shape (respects pins/affinity; does not consume the upstream retry budget on a
pure capacity collision):

1. Add a request-scoped exclusion set, e.g. `RequestPolicyState.SaturatedChannelIDs`
   (`service/request_policy.go:41`). Keep it separate from `FailedChannelIDs`: saturation
   is not a health failure and must not feed `RetryAvoidFailedChannels` health logic.
2. In `service.selectRetryChannel` (`service/channel_select.go:239`) and the auto-group
   loop in `CacheGetRandomSatisfiedChannel` (`:115`), union `SaturatedChannelIDs` into the
   exclude list passed to `model.GetUntriedSatisfiedChannel`
   (`model/channel_constraint.go:22`). That helper already prefers the highest remaining
   priority tier while excluding a set, so a saturated peer is skipped without dropping a
   tier. Use the advisory `ChannelCapacityAvailable` when populating the set.
3. In `getChannel` (`controller/relay.go:283`), after resolving the candidate, acquire.
   On `ok == false`: if the candidate is not pinned and the request is not a strict
   session, add the ID to `SaturatedChannelIDs` and re-select once from the caller loop
   (bounded, mirroring `maxKeyScopeFallbacks` at `middleware/distributor.go:30`). If the
   candidate is pinned (`GetChannelConstraints(c).ResolvedPin`) or
   `SuppressesRetry()` is true (`dto/channel_constraints.go:118`), do not fall over —
   return the 429 immediately.
4. When no eligible channel remains, return
   `types.NewError(errors.New(...), types.ErrorCodeChannelCapacitySaturated,
   types.ErrOptionWithStatusCode(http.StatusTooManyRequests),
   types.ErrOptionWithSkipRetry())`. The new code **must not** have the `channel:` prefix,
   because `types.IsChannelError` (`relaykit/types/error.go:376`) turns that prefix into
   automatic disabling. Follow the precedent comment on
   `ErrorCodeChannelKeyScopeUnsatisfied` (`relaykit/types/error.go` ~`:53`):
   `channel_capacity_saturated`.
5. Do not route a saturation rejection through `RecordChannelFailure`,
   `ProcessChannelError`, `ShouldDisableChannel`, or the retry decision. Record it only as
   a request-policy event (`routing / capacity_saturated`) for the log details.
6. Because acquisition is non-blocking and happens before `c.Writer` is written, the
   existing deferred error writer in `Relay()` emits a normal JSON 429. `GetChannelConstraints`/`StreamScannerHandler` are not involved yet. For Claude streaming,
   `StartStreamKeepAlive` (`controller/relay.go` ~`:124`) only sets headers and wraps the
   writer (`relay/helper/common.go:89`, `SetEventStreamHeaders` does not `WriteHeader`), so
   an immediate admission failure still renders JSON. Admission is immediate; it must not
   block longer than the ping interval.

## 6. Distributed (Redis) semantics

Only needed when `common.RedisEnabled` (`common/redis.go`). Without Redis, the local
counter is per-instance (README.zh_CN.md:245) and the cap is not global.

Recommended backend: one lease set per channel, not a bare counter.

- Key: `new-api:channel_concurrency:{channelID}` (namespaced ZSET).
- Acquire Lua (atomic): `ZREMRANGEBYSCORE key -inf now`; if `ZCARD key >= limit` return 0;
  else `ZADD key now+lease token` and `PEXPIRE key lease*2`; return 1.
- Renew Lua: if member still present, `ZADD key now+lease token` and refresh `PEXPIRE`;
  returns whether the lease is still held.
- Release Lua: `ZREM key token`.
- One renewer goroutine per held slot at `lease/3`; stop on release. Lease e.g. 30 s.
  The renewer must recover from panics (AGENTS.md) and must not use `WaitGroup.Go` with a
  function that can panic.
- Crash: the lease expires within one lease period, so capacity is reclaimed. Max
  over-admission window after a crash ≈ one lease.
- Lowering a limit: `ZCARD` reflects the new limit on the next acquire; in-flight leases
  above the new limit are not evicted (they finish). Document this. Changing the config on
  one instance reaches others only on the periodic cache sync, so the effective limit can
  lag; the Redis value bounds concurrent admits to the *configured local* limit each node
  reads, which can differ during propagation.
- Redis error at acquire: recommended **fail-open to the local counter** and log. Rejecting
  every request on a Redis outage is a larger availability risk than a temporary
  cross-instance overrun. Fail-closed is the alternative if a hard provider limit is more
  important than availability; make this an explicit decision.
- Renewal failure while the process is alive: the local slot stays held, but the Redis
  lease can expire, letting another node admit past the global limit. There is no way to
  guarantee the count matches the provider.

**Cannot claim:** releasing a slot or deleting a lease does not guarantee the remote
provider stopped generating. Close/cancel propagation is best-effort; the counter reflects
*our* live attempts, not the provider's true concurrency. State this in the PRD/PR.

## 7. Test / admin / async boundaries

| Boundary | Path | Recommendation |
|---|---|---|
| Synchronous chat/embed/image/audio/rerank | `controller/relay.go` loop | Count. Acquire around the handler; release on return. |
| Task submit | `executeTaskSubmissionWith` | Count around `submit(...)` only; release before persistence. |
| Async task polling | `service/task_polling.go` | Do not count (short, frequent, not the generation stream). |
| Responses WebSocket | `relay/responses_websocket.go` `runCall` | Count the upstream target socket lifetime; release on `registerChannelClose`/`shutdown`. |
| Admin single channel test | `controller/channel-test.go` `runChannelTestRequest` | Count, but treat capacity rejection as a local "busy/skipped" result; never disable. |
| Scheduled health test | `testChannelForHealthCheck` | Same as admin test; `service.ShouldDisableChannel` must never see the capacity error. If counting health tests is undesirable, bypass the cap for `info.IsChannelTest` and document. |

`DecideRelayRetry` currently retries HTTP 429 (`operation_setting.ShouldRetryByStatusCode`).
The capacity error must carry `ErrOptionWithSkipRetry` so it cannot be mistaken for a
retryable upstream 429; the local re-selection happens before the retry decision.

## 8. Test infrastructure and real-DB availability

- Per-dialect test pattern already exists: `model/channel_settings_test.go` opens
  `glebarez/sqlite` in `t.TempDir()`, and uses `TEST_MYSQL_DSN` / `TEST_POSTGRES_DSN`,
  skipping when unset (`channel_settings_test.go:111`+). Same pattern appears in
  `controller/*_test.go`, `model/*_test.go`, `service/rankings_test.go`.
- No dedicated DB test helper/fixture package exists (only
  `plugins/video_responses_test_helpers_test.go`). No `go test` runner script; `makefile`
  target `test` runs `GOWORK=off go test` with no DSNs, so MySQL/Postgres tests skip.
- `.github/workflows/ci.yml` runs `make test` (GOWORK=off) with no DB service, so the
  three-database gate is manual.
- Local infra by filename only: `docker-compose.dev.yml` (services `new-api`, `redis`,
  `postgres`), `docker-compose.yml` (`postgres`; `mysql:8.2` commented out; `redis`,
  `watchtower`), `.env.example` (`SQL_DSN`, `LOG_SQL_DSN`, `REDIS_CONN_STRING`,
  `SYNC_FREQUENCY`). No MySQL container is enabled by default.
- This change adds no schema/column: config is channel `setting` JSON. The DB gate reduces
  to channel settings persistence round-trip; extend `model/channel_settings_test.go`.
  There is no migration and no `AutoMigrate` impact. Still run the real SQLite/MySQL/
  PostgreSQL round-trip and record versions/commands per AGENTS.md before claiming done.

## 9. File ownership (precise)

Owned by this design (semaphore/routing/lifecycle):
- `service/channel_capacity.go` — new semaphore + Redis lease backend. **new file**
- `service/channel_capacity_test.go` — unit tests for the semaphore. **new file** (single
  test file for the feature, per AGENTS.md).
- `service/channel_select.go` — saturation-aware selection (`selectRetryChannel`,
  `CacheGetRandomSatisfiedChannel` exclusion union).
- `service/request_policy.go` — `SaturatedChannelIDs` field + `capacity_saturated` event.
- `controller/relay.go` — acquire/release in `Relay()` and
  `executeTaskSubmissionWith`; capacity-aware `getChannel`; 429 rendering path.
- `relay/responses_websocket.go` — WS target-socket acquire/release.
- `controller/channel-test.go` — admin/health test acquire; classify saturation as local.
- `relaykit/types/error.go` — add `ErrorCodeChannelCapacitySaturated` (no `channel:`
  prefix). Relaykit is independently buildable; verify with `GOWORK=off go build ./...` in
  `relaykit/`.
- `i18n/keys.go` + `i18n/locales/{en,zh-CN,zh-TW}.yaml` — `relay.channel_capacity_saturated`
  message (backend error text; the relay error is returned to clients, so it belongs in
  `i18n/`, matching `MsgRelayGroupSaturated`).

Owned by the config agent (do not duplicate):
- `relaykit/dto/channel_settings.go`, `model/channel.go` `ValidateSettings`, and the
  frontend channel form/serialization files listed in `initial-findings.md`.

Regression test placement:
- Extend `controller/channel_pin_retry_test.go` (it already fakes upstream servers and runs
  `middleware.Distribute()`) for: cap=2 blocks a third concurrent attempt; a second
  eligible channel is used; all-full returns 429 with no upstream request; saturation does
  not disable the channel or record a health failure; release after a stream ends frees
  capacity. Only add a new controller test file if this cannot be expressed there.
- Extend `model/channel_settings_test.go` for settings round-trip of the new field across
  sqlite/mysql/postgres.
- Prefer `testify/require` (setup/fatal) and `testify/assert` (value checks).

## 10. Open risks / explicit non-guarantees

- Cross-instance guarantee during a Redis outage or a failed lease renewal is best-effort.
- Remote provider cancellation after release/cancel is not guaranteed.
- WS upstream socket lifetime means one slot can cover several logical responses on a
  reused client connection.
- Config propagation lag across instances means the effective limit can differ briefly.
- Admin/health tests sharing the cap can make diagnostics fail while a channel is busy;
  the recommendation is to count them but classify saturation as local/skipped.
