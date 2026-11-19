# Channel concurrency metrics (current/limit on list cards and detail)

Research only. No product files edited.

## Current admission implementation (facts)

- Limit lives in per-channel settings: `dto.ChannelSettings.MaxConcurrentRequests *int` (`relaykit/dto/channel_settings.go:16-18`), validated `0..MaxChannelConcurrentRequests` (`:87`). `nil` and `<=0` mean unlimited.
- Admission: `service/channel_capacity.go:146-170` (`AcquireChannelCapacity`).
  - Unlimited: early return of a dummy lease with a **typed-nil** context value; no counter, no Redis key, no goroutine (`:150-153`). The comment records why: native submission retries carry a detached previous attempt's context values, so unlimited admission must not rebind that old lease.
  - Local (`!common.RedisEnabled`): `sync.Mutex` + `map[int]int` in-process counter (`:48-51`, `:156-164`).
  - Redis: ZSET `new-api:channel-capacity:<channelID>`, member = uuid owner, score = Redis `TIME` + TTL 30 s (`:22`, `:28-36`). Acquire script reaps `-inf..now`, checks `ZCARD >= limit`, `ZADD`, `PEXPIRE`. Renew script refreshes only unexpired members (`:38-47`), every `TTL/3` = 10 s (`:207-221`).
  - Redis configured but `RDB == nil` → `ErrorCodeChannelCapacityUnavailable`; it never degrades to local counting (`:165-169`).
- Release: `:110-133` — `sync.Once`, `ZRem` its own owner, local decrement, delete key at zero. Release is idempotent and cannot free a successor's slot (`service/channel_capacity_test.go:52-59`).
- Relay call sites and release safety: `controller/relay.go:211` (defer), `:221`, `:408`, `:740-782` (task/native submission); error mapping 429/503 in `AcquireRequestChannelCapacity` (`service/channel_capacity.go:223-240`).

## In-flight read API: absent (fact)

No reader exists. Repo grep finds only the two Lua scripts and `ZRem` (`:122`, `:183`); there is no `ZCARD`/`ZCOUNT`/`ZRange` API, no exported accessor for `localChannelCapacity.active`, and no capacity metric endpoint. `/api/perf-metrics` (`router/api-router.go:39-43`) is upstream latency sampling, unrelated. `GetAllChannels` (`controller/channel.go:224`) and `GetChannel` (`:503`) return channel rows/settings but never a current count.

## Design to persist: count unlimited channels via one lease lifecycle

Goal: same counter path for every channel; limit only decides rejection, never whether counting happens, and no per-request database writes.

- Redis admission script becomes count-returning: `ZREMRANGEBYSCORE key -inf now` → `n = ZCARD` → if `limit > 0 and n >= limit` return `{0, n}` (rejected, current included) → `ZADD` → `PEXPIRE` → return `{1, n+1}`. Pass `limit = 0` to mean "count but never reject" for unlimited channels.
- Local path mirrors it: always increment the map; reject only when `limit > 0 && n >= limit`.
- Result: `ZCOUNT`/map reads report every channel, limited or unlimited; "current" for unlimited is real, not a placeholder.
- Constraint to preserve: the typed-nil rebinding guard at `:150-153`. A counting lease for unlimited channels must still not rebind a detached previous attempt's context value. Treat lease-context binding and slot accounting as separable: keep the existing no-rebind context semantics while allocating/releasing the slot.
- Cost: unlimited channels now pay one script call per admitted request plus the renew goroutine (all channels already in-flight on the limited path do). No DB write; channel settings come from the channel row already loaded for routing.

## Read metrics: ZCOUNT over server TIME, not reap+ZCARD

- Read script: `local t = redis.call('TIME'); local now = t[1]*1000 + floor(t[2]/1000); return redis.call('ZCOUNT', KEYS[1], '(' .. now, '+inf')`. Exclusive lower bound matches acquire's `ZREMRANGEBYSCORE -inf now` semantics exactly; non-mutating, so no write amplification on a read endpoint. Do **not** use the app clock: members are scored with Redis time, so a skewed app node would under/overcount.
- Pipeline all visible ids: one round trip, `EVALSHA` per id via go-redis `Pipeline` (preload with `Script.Load` at startup, fall back to `EVAL` on `NOSCRIPT`, and cap per-id errors to `null`).
- Local mode: one mutex-guarded map snapshot for all requested ids.
- Missing Redis key vs Redis failure: `ZCOUNT` on a missing key = a genuine `0`. Any command error, or Redis mode with `RDB == nil`, must return `null`/omitted — never 0.

## API and UI contract

- Single admin-only endpoint, e.g. `GET /api/channel/capacity?ids=1,2,3`, response `{id: {current: number|null, limit: number|null, scope: "cluster"|"instance"}}`; `limit: null` means unlimited, `current: null` means unavailable. Keep it after `clearChannelInfo` (`controller/channel.go:515`) so no key material is added.
- Shared cache: list cards request the visible page's ids; detail requests `[id]`; identical shape so one react-query hook (`queryKey` = sorted ids) serves both. No per-card polling. `staleTime` ~5 s and `refetchInterval` ~10 s only while the view is visible.
- Tag mode pitfall: with `tag_mode` the list rows are tags, not channels (`controller/channel.go:245-264`), so either expand tags to channel ids or omit metrics on tag cards.
- Local mode scope must be visible in UI ("this instance") because multiple replicas each report only their own count; Redis mode is cluster-wide.

## Additional pitfalls

1. A crashed gateway's slot lingers until the 30 s TTL, so "current" is an upper bound within one lease TTL, not an instantaneous exact value.
2. Never maintain a parallel counter next to the lease: release is idempotent (`:110-133`) and any extra increment/decrement drifts on panic or detached-submission paths.
3. Local mode has no TTL/reap; a lease leaked without release (panic outside the deferred release) counts forever. Redis mode self-heals.
4. Counting unlimited channels turns a formerly no-op path into work; measure it rather than assuming zero. If overhead is disproportional, fall back to returning `current: null` for unlimited plus an explicit "Unlimited" label — never a fake `0` (open decision).
5. Any per-request write must stay in Redis/memory; re-reading or updating channel settings in the database per request is prohibited.

## Focused tests

- Extend `service/channel_capacity_test.go`: unlimited channel increments and decrements the shared counter; rejection returns the current count; read script equals post-reap `ZCARD` with an expired member; read returns `null` on Redis error/unavailable, `0` only for a missing key.
- Add one controller test for the batched endpoint: mixed limited/unlimited/unavailable ids in a single response, no DB writes.
