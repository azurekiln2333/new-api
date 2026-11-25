# Admin console middleware: sequential remote round trips

Read-only source investigation, 2026-11-25. Audit source freeze retained.
Examples: `PUT /api/channel/` and `GET /api/channel/search`.
No tests/builds/vet/lint/format/typecheck/database probes or source edits performed.
Only this requested research document is written. Counts refer to the inspected
working tree, including the frozen durable-audit changes, not a claim about which
SHA is running on HK. Production latency (160–320ms RTT, 320–1400ms SQL logs) is
user-provided context, not measured here.

## Counting convention

One awaited Redis command = one application-level network round trip. A Lua EVAL
is ONE round trip, not one for each redis.call inside it. Each awaited PG statement
is one protocol exchange in the current simple-protocol configuration. Include
BEGIN/COMMIT when counting writes. Totals assume healthy already-open connections,
no retries, no pool wait, successful authorization and no concurrent cache fencing.
Conditional driver health pings and new-connection handshakes are listed separately.
Handler queries/cache publication and background workers are excluded from middleware
totals. Actual end-to-end request costs are therefore greater than these totals.

## 1. Execution order

Registration `main.go:215–226`, `router/main.go:15`, `router/api-router.go:19–24`,
`router/channel-router.go:20–24,40–54,61,83`:

| Order | Middleware / location | Remote I/O on these normal paths |
|---|---|---|
| 1 | PanicRecover, main.go:220; middleware/recover.go:33 | None in normal execution |
| 2 | RequestId, main.go:223; middleware/request-id.go:10 | Local ID/header |
| 3 | Version, main.go:224; middleware/cors.go:126 | Local header |
| 4 | I18n, main.go:225; middleware/i18n.go:13–32 | Context/header only; NOT GetUserLanguage or DB |
| 5 | logger redaction + Gin logger, main.go:226; middleware/logger.go:21–23 | Process log writer, not Redis/SQL; filesystem/stdout latency outside remote counts |
| 6 | RouteTag("api"), router/api-router.go:20; middleware/logger.go:14 | Context only |
| 7 | gzip, router/api-router.go:21 | Local compression |
| 8 | AccessTokenAudit, router/api-router.go:22; middleware/audit.go:302 | JWT detection local; PAT lookup described below; PAT write audit on unwind |
| 9 | BodyStorageCleanup, router/api-router.go:23; middleware/body_cleanup.go:11 | Cleanup after handler; no Redis/SQL (may remove local temp body file) |
| 10 | GlobalAPIRateLimit, router/api-router.go:24; middleware/rate-limit.go:183 | One EVAL when enabled + Redis |
| 11 | AdminAuth, router/channel-router.go:42; middleware/auth.go:52–96,138 | Session/PAT resolution, user validity, PAT last-use UPDATE; admin audit wraps writes |
| 12 | RequirePermission(ChannelWrite/ChannelRead), router/channel-router.go:20–24,61,83; middleware/auth.go:427 | Casbin in-memory policy check |
| 13 | controller.UpdateChannel / SearchChannels | Handler excluded from middleware count |
| unwind | finishAdminAudit, middleware/auth.go:95; middleware/audit.go:132 | Write-only, skipped when handler marked audit logged; otherwise durable enqueue |
| unwind | finishAccessTokenAudit, middleware/audit.go:344–372 | PAT only; write enqueue or read synchronous sink |
| unwind | cleanup/compression/logger | No further Redis/SQL from these middleware |

**Neither example attaches CriticalRateLimit, SearchRateLimit, SecureVerification,
RootAuth, refresh-cookie validation or TokenOperationAudit.** Channel key reveal is
an explicitly different route: POST `/:id/key` at channel-router.go:44–50 adds
RootAuth, CriticalRateLimit, DisableCache, SecureVerificationRequired. Do not add
that route's costs to a normal channel save/search. API-token-operation auditing
is mounted only on `/api/token` at api-router.go:305–307.

## 2. Browser session JWT: exact healthy hot path

1. AccessTokenAudit calls ParseDashboardAccessToken locally and skips opaque-token
   lookup for internal JWTs (`middleware/audit.go:305–314`). JWT parsing happens
   again in AdminAuth; neither parse issues I/O (`service/auth_token.go:85–123,183`).
2. Global limiter: one Redis EVAL (`middleware/rate-limit.go:66–86,110–119`).
3. AdminAuth → classifyDashboardCredential → ValidateLoginSession
   (`middleware/auth.go:194–220`; `service/auth_session.go:130–149`):
   - Session **HGETALL** via `model/user_session.go:221–228,266–269` →
     `common/redis.go:161–167`. Local MAC/schema/expiry/revocation/version checks.
   - User **HGETALL** via `model/user_cache.go:100–104,129–147` → same Redis wrapper.
   - User version-floor **MGET** (two keys in ONE command) via
     `model/user_cache.go:146–152`, `model/user_auth_cache.go:108–112`.
   - User status + auth version compared in memory (`service/auth_session.go:146`).
     No separate status/version SELECT, no last-active UPDATE on ordinary validation.
4. AdminAuth's role/status/username checks are local (`middleware/auth.go:61–78`).
5. Casbin check is local (section 5).

**Warm Redis JWT pre-handler = 4 serial Redis commands, 0 SQL.** A second JWT
parse is CPU work, NOT another session validation. No browser refresh token is
checked on this request. Refresh is a separate HTTP request:
`router/api-router.go:84` → controller/auth_session.go:17–25 →
service/auth_session.go:234 onward. Client refresh route is referenced in
`web/src/lib/auth-session.ts:281`. Any refresh + original-request retry is additional
client traffic, not hidden middleware queries in the original request.

### JWT cache-miss/off branches

- Session miss/invalid cache: HGETALL attempt → SELECT user_sessions by sid
  (`model/user_session.go:208–214,235–236`) → synchronous **EVAL** cache publication
  (`:249–250,291–356`). Normally 1 Redis + 1 SQL + 1 Redis instead of 1 Redis.
- A fill whose observation window expired can perform an extra SELECT COUNT
  confirmation (`model/user_session.go:252–255,380–400`); revoked/denied session
  returns early. Session AfterFind only trims a string (`:65–68`), no queries.
- User miss/invalid cache: HGETALL attempt → SELECT user excluding password/token
  (`model/user_cache.go:110`; `model/user.go:538–547`) → **MGET** version floor
  (`model/user_cache.go:114–116`) → synchronous **EVAL** cache fill
  (`:118`; `model/user_auth_cache.go:48–100`). Normal miss is 3 Redis + 1 SQL
  instead of warm 2 Redis. If a syntactically valid cache reaches floor MGET but
  then fails that check, that first MGET is an additional attempt before fallback.
- With BOTH normal misses: limiter 1 + session 3 + user 4 = **8 serial exchanges**
  (6 Redis, 2 SELECT), before handler.
- With Redis disabled: no limiter network I/O, no hash/floor/fill I/O;
  session SELECT + user SELECT = **2 SQL exchanges**, before handler.
- `MemoryCacheEnabled` does NOT skip these authentication reads: there is no
  process-local session/user auth hit in the inspected functions. Redis enabled
  forces MemoryCacheEnabled for channel compatibility (`main.go:84–86`), but that
  channel memory cache is not an auth cache substitute.

## 3. PAT and legacy opaque-token branch

AccessTokenAudit runs BEFORE GlobalAPIRateLimit. It resolves opaque credentials,
so PAT request order differs from browser JWT (`middleware/audit.go:302–317`):

1. **SELECT user_access_tokens WHERE token_hash = ?**
   (`middleware/auth.go:249–276`; `model/user_access_token.go:85–97`, query at :90).
   No Redis PAT-row cache in this path.
2. User HGETALL → version-floor MGET, same as above
   (`middleware/auth.go:272`; model/user_cache.go:100–152).
3. Begin PAT audit snapshot, local (`middleware/audit.go:319–329`).
4. Global limiter EVAL.
5. AdminAuth reuses the per-request lookup (`middleware/auth.go:251–255`), so NO
   second PAT SELECT or second user lookup. Status, role, grants and expiration
   comparisons are local; PAT uses no browser-session SELECT.
6. `setAccessTokenContext` always calls **TouchUserAccessToken** once on first
   authenticated context (`middleware/auth.go:336–358`). This issues an UPDATE
   WHERE id=? AND last_used_at < now-60 (`model/user_access_token.go:191–199`).
   **Important:** the 60s predicate limits rows MODIFIED, not queries issued.
   It is still sent on EVERY accepted scoped-PAT request, even if zero rows change.
   With GORM default write transactions it is BEGIN → UPDATE → COMMIT = **3 PG
   exchanges**. It is not a Redis cache update and not async.
7. Casbin local; handler.
8. PAT finish always records a distinct access event, regardless of handler's
   `ContextKeyAuditLogged` for the business event:
   - PUT: outbox BEGIN → INSERT → COMMIT = 3 primary PG exchanges.
   - GET: RecordAuditLog → WriteAuditLogEntry (`middleware/audit.go:371`;
     `model/audit_log.go:76–86,167–193`) is a synchronous LOG_DB insert; for relational
     LOG_DB the default transaction makes BEGIN → INSERT → COMMIT = **3 exchanges**.
     Username already exists, so GetUsernameById branch at audit_log.go:78–79 is
     skipped for normal PAT events. ClickHouse uses its sink-specific insert path;
     do not count relational transaction support for ClickHouse.

Legacy opaque tokens before retirement: no browser session; query by access_token
(`model/user.go:1263–1278`) then GetUserCache. Retirement is an atomic in-process
read (`model/user_access_token.go:326–329`), NOT an Option SELECT per request.
Legacy skips TouchUserAccessToken (`middleware/auth.go:341–344`). After retirement
legacy lookup is rejected before its credential SELECT. Do not assume legacy
credentials are enabled in production just because a pre-retirement path exists.

## 4. Rate limiting: commands, defaults, route conditions

`common/init.go:127–129`: GLOBAL_API_RATE_LIMIT_ENABLE=true, 360 requests/180s.
`common/init.go:139–141`: CRITICAL_RATE_LIMIT_ENABLE=true, 20 requests/1200s.
Redis is enabled only with REDIS_CONN_STRING (`common/redis.go:23–39`); otherwise
InitRedisClient sets RedisEnabled=false. Redis pool default 10 (`:39`), cache TTL
normally SyncFrequency=60s (`:19–32`; model/user_cache.go:66–71).
MEMORY_CACHE_ENABLED defaults false unless exactly "true" (`common/init.go:90`),
then main.go:84–86 overrides it true when Redis is enabled.

Each attached enabled Redis limiter sends **one EVAL**, not EVALSHA, no separate
client INCR/EXPIRE/TTL (`middleware/rate-limit.go:18–38,80–86`):
- Lua INCR + TTL always, EXPIRE on first hit; if TTL<0 another EXPIRE + TTL.
- These are server-side commands inside that ONE remote call.
- Disabled limiter = 0; Redis off = process-memory limiter, 0 remote calls
  (`middleware/rate-limit.go:148–161`).
- CriticalRateLimitFor is also one such EVAL when attached (:204–231); neither
  channel example attaches it. UserCriticalRateLimit uses one EVAL too (:256–300).
- Rate limiter Redis error aborts; there is no successful memory fallback on a
  Redis error (`:117–123`).

## 5. Casbin / permissions

`middleware/auth.go:427–435` → `service/authz/resolver.go:10–34`.
Role assignment is a switch on the authenticated system role
(`service/authz/assignment.go:7–16`), not a DB role lookup. Root short-circuits.
Admin uses current in-process SyncedEnforcer, GetFilteredPolicy for user override
then role baseline (`resolver.go:22–34,53–75`): **0 Redis, 0 SQL per request**.
Policy DB load occurs during initialization/reload, not each check
(`service/authz/enforcer.go:33–60,69–75`; service/authz/adapter.go:22 onward).
Do not charge background policy synchronization to a request's serial I/O chain.

## 6. Audit unwind and exact channel-save distinction

Generic admin writes: `middleware/audit.go:132–199` calls
RecordAuditLogDurable (`model/audit_log.go:95–117`). This snapshots metadata and
uses `RecordManagementAuditDurable` (`model/audit_outbox_record.go:24–43`):
**BEGIN → INSERT audit_event_outbox → COMMIT = 3 primary PG exchanges**.
`EnqueueAuditEventTx` does one Create (`model/audit_outbox.go:125–170`); it is on
an existing sql.Tx, so its GORM callback doesn't start a second actual transaction.
No normal-path username query or synchronous LOG_DB write for this middleware.
No schema introspection/HasTable probe on enqueue.

**Successful actual PUT /api/channel/ is special:** controller/channel.go:1665
calls UpdateChannelConfigAtomic and marks audit logged at :1683. Its audit is
inserted inside the handler's business transaction; therefore finishAdminAudit
skips at middleware/audit.go:140, and middleware adds **0** generic audit SQL.
The transaction's INSERT is still real request latency, just HANDLER-owned and
excluded from this report's middleware totals. A failed save or a handler branch
that doesn't mark audit logged incurs the generic 3-exchange enqueue instead.
GET channel search never starts an admin fallback writer (:110–114), so 0 admin
audit writes. A PAT access audit remains independent in either case.

Enqueue failure: direct fallback to LOG_DB with same EventId; fresh 10s timeout
(`model/audit_outbox_record.go:45–76`). Normal enqueue timeout also 10s (:34).
Failure counts aren't fixed: connect failure, transaction abort, COMMIT uncertainty
and duplicate confirmation differ. Relational duplicate recovery adds SELECT COUNT
(`model/audit_log.go:203` onward). Delivery worker queries/lease/ACK are background,
not on the normal save critical path. Generic post-commit crash gap still exists.

## 7. PG/GORM connection behavior

- `model/main.go:152–160` explicitly sets PreferSimpleProtocol=true and passes
  `newGormConfig(false)` for PostgreSQL; `model/gorm_logger.go:25–29` sets
  PrepareStmt to that false. MySQL/SQLite pass true (main.go:163–183).
- SkipDefaultTransaction is NOT set in newGormConfig; zero value false, so GORM
  wraps standalone Create/Update/Delete in BEGIN/COMMIT. Exact dependency evidence:
  gorm.io/gorm@v1.25.12/callbacks/callbacks.go:22–53 and transaction.go:7–29.
  Explicit GORM Transaction begins once and commits once (finisher_api.go:620–654).
- Primary pool defaults: SQL_MAX_IDLE_CONNS=100, SQL_MAX_OPEN_CONNS=1000,
  SQL_MAX_LIFETIME=60 **seconds** (`model/main.go:211–213`). Independent log pool
  gets same defaults (`:266–268`); shared handle reuses primary pool. No request-time
  "set lifetime" SQL. Lifetime expiration can cause later connection replacement.
- Dependency versions: go.mod:64–65,130: postgres driver v1.5.9, GORM v1.25.12,
  pgx v5.9.2. Sources inspected READ-ONLY in existing local module cache:
  `C:/Users/lichengyu/go/pkg/mod/` (no go commands or installs).
- postgres@v1.5.9/postgres.go:93–106 parses DSN then OVERRIDES pgx's default mode
  with QueryExecModeSimpleProtocol when PreferSimpleProtocol=true. This override
  also wins over a default_query_exec_mode in DSN on this code path.
- pgx's upstream default is cache_statement with per-connection capacity 512
  (pgx/v5@v5.9.2/conn.go:171–217), new LRU per connection (:288–294). In that mode,
  a new SQL cache miss does Prepare then Execute (:537–549). **That is NOT the
  current project's PG default:** simple protocol uses client parameter escaping
  then pgConn.Exec without that Prepare (:573–596). Thus do not attribute current
  320–1400ms statements to a mandatory prepare round trip on every new connection.
- New TCP/TLS/auth handshake costs depend on DSN/network/auth and cannot be fixed
  from these sources. Lifetime 60s is not proof that each request opens a connection.
- Additional real conditional RTT: pgx stdlib ResetSession checks an existing
  connection and **pings when time since its prior reset >1s**, before reuse
  (pgx/v5@v5.9.2/stdlib/sql.go:553–585). No custom ShouldPing is supplied by the
  project's stdlib.OpenDB call. Each such checkout can add 1 PG round trip; not
  charged to minimum totals. It checks time since reset, not a per-request timer;
  no proof from source how often production hits it. BEGIN/queries inside a held
  transaction don't each perform a database/sql pool checkout.

## 8. Minimum successful middleware totals

PG columns count protocol exchanges (including BEGIN/COMMIT), not merely DML logs.
R = enabled Redis GlobalAPIRateLimit EVAL, normally 1; disabling it subtracts 1.
Healthy auth Redis cache hits, open connections, initialized policies:

| Credentials/path | Before handler | After handler | Middleware total |
|---|---|---|---|
| Browser JWT GET /channel/search | R + HGETALL session + HGETALL user + MGET floor = 4 Redis | 0 | **4** |
| Browser JWT PUT /channel/ committed + audit marked | same 4 Redis | 0 generic audit (handler owns audit tx) | **4** |
| Browser JWT generic/unmarked PUT | same 4 Redis | 3 PG outbox | **7** |
| Scoped PAT GET /channel/search | PAT SELECT 1 + user Redis 2 + limiter 1 + touch PG 3 = 7 | relational LOG_DB audit 3 | **10** |
| Scoped PAT PUT /channel/ committed + audit marked | same 7 | PAT outbox PG 3 | **10** |
| Scoped PAT unmarked PUT | same 7 | generic outbox 3 + PAT outbox 3 | **13** |

Cold normal session+user JWT adds 4 exchanges (base pre-handler 8). Cold normal
user PAT adds 2 exchanges (base pre-handler 9). Redis OFF removes limiter + hash/
floor/fill commands: browser JWT pre-handler **2 PG SELECT**; accepted scoped PAT
pre-handler **1 PAT SELECT + 1 user SELECT + touch 3 = 5 PG**; add appropriate audit
cost above (PAT GET relational audit 3 => **8**, committed PAT PUT outbox 3 => **8**).
Legacy pre-retirement with warm user cache: PAT-row-equivalent SELECT1 + user2 +
limiter1 =4 pre-handler, no touch; add access audit3 =>7. Rejected credential/limit
paths short-circuit and are not successful-request minima.

These are sequential network dependencies, not latency measurements. PG pool
queueing, driver conditional Ping, connection setup/retries, actual handler work,
and the follow-up channel-list HTTP request can all add more. Source establishes
that even a Redis-warm browser admin read still traverses FOUR awaited remote calls;
turning on the unrelated channel memory cache does not remove those auth calls.
