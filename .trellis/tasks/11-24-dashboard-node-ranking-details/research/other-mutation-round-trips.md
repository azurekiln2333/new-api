# Other console mutation round trips — current working tree, read-only investigation

Date: 2026-11-25. Owner: pi-logs. Active parent: `11-24-console-responsiveness-diagnostics`.
No production or test source changed for this investigation. No local validation or database/network probes performed.

## Counting rules and limitations

Counts below include BEGIN and COMMIT as separate sequential database operations, not just SQL visible in application logs. Production `model/gorm_logger.go:25-29` does **not** set SkipDefaultTransaction. Consequently an otherwise standalone GORM Create/Update runs BEGIN → statement → COMMIT. A write already inside an explicit transaction does not acquire another independent BEGIN/COMMIT. Source: installed GORM v1.25.12 `callbacks/transaction.go:7-30`, `finisher_api.go:17-23,620-660`; root `go.mod:65` pins that version. PostgreSQL INSERT RETURNING is the same statement, not an extra SELECT. Driver parse/bind/prepare messages, retries, pool waits, locks, TCP/TLS setup and failed-transaction rollback are NOT assigned speculative fixed counts.

Excluded: shared auth, rate-limit and generic audit middleware, per request. Included: audit called by the handler and audit enqueued atomically by its model. The normal outbox path does not write the log sink before returning. Enqueue failure can synchronously invoke the bounded sink fallback (`model/audit_outbox_record.go:37-74`); its additional statements depend on sink dialect and failure point, so failure counts are not included in normal totals.

Examples multiply sequential operations by the supplied 160–320ms RTT only to illustrate exposure, NOT measured handler durations. PostgreSQL and Redis operations may be on different connections but the ordered calls below await each other.

## 1. System settings PUT /api/option/

### Ordinary single key, including the dirty checked-upsert patch

Handler `controller/option.go:190-454`: JSON validation and in-memory validation → model.UpdateOption → handler audit → JSON response.

In order:
1. BEGIN (GORM default write transaction).
2. One INSERT options ... ON CONFLICT(key) DO UPDATE value (`model/option.go:379-382`).
3. COMMIT. Error returns before sync/map publication.
4. BEGIN (standalone sync task Create).
5. INSERT system_tasks (`model/option.go:385` → `model/system_sync_task.go:47-49,68-78`). Random task ID generation is local (`model/system_task.go:94-99`). No SELECT/FOR UPDATE on this **public BumpSyncTopic** path.
6. COMMIT.
7. updateOptionMap (`model/option.go:387,458+`) changes only the relevant local map/global/config. No ordinary options table re-read.
8. BEGIN audit outbox transaction (`controller/option.go:447` → `controller/audit.go:150-173` → `model/audit_outbox_record.go:37`).
9. INSERT audit_event_outbox (`model/audit_outbox.go:125-170`). Actor is snapshotted from context (`controller/audit_actor.go:29-46`), no username lookup.
10. COMMIT, then response (`controller/option.go:450`).

**Normal total: 9 DB operations, 0 Redis commands.** Listed step 7 is CPU/local publication, not a database operation. Example: 9 × RTT = 1.44–2.88s, before excluded middleware and HTTP overhead. Top contributors: three separate write transactions, including a separate sync-task write and separate handler audit enqueue.

The dirty patch is the CURRENT source, not the old unchecked FirstOrCreate + Save path. It is not validated; no database-compatibility completion claim is made.

### What the settings pages actually do for multiple keys

There is no general multi-key payload on this PUT route: `controller/option.go:140-143` defines one key/value. The generic form accepts changed fields, then pages loop with awaited single-key mutations. Concrete examples:
- `web/src/features/system-settings/general/system-info-section.tsx:130-140`: for each changed field, await updateOption.mutateAsync.
- `.../maintenance/performance-section.tsx:244-249`: same sequential pattern.
- `.../integrations/payment-settings-section.tsx:762`, `.../integrations/email-settings-section.tsx:225`: awaited update loops (some keys use specialized paths below).
- HTTP client `.../api.ts:42-44`: one PUT per mutation.

For **K ordinary keys**, page save is **K sequential HTTP requests and 9K DB operations**, 0 handler Redis. Example K=5: 45 operations / 7.2–14.4s RTT component; K=10: 90 / 14.4–28.8s. Do not confuse this with model.UpdateOptionsBulk.

Current frontend has already removed options refetch amplification: `.../hooks/use-update-option.ts:66-89` patches one cached key and invalidates system-options with `refetchType:'none'`. Certain display keys still invalidate status (`:93-100`), which can issue a concurrent follow-up GET, not a database operation inside the PUT. `controller/option.go:84-138` GET options reads the local OptionMap and formats local metadata: it does not call InitOptionMap.

### Model bulk path and special per-key paths (conditional, not all ordinary keys)

- `model/option.go:346-368`: InviteRegisterEnabled/LoginVerificationRequiredEnabled, GroupLevels and OAuthAllowedOrigins dispatch to UpdateOptionsBulk; request-policy, passkey-domain and pricing keys retain their specialized branches.
- **UpdateOptionsBulk K keys** (`model/option.go:395-452`): BEGIN → K checked upserts (`:430-442`) → COMMIT → separate sync-task BEGIN/INSERT/COMMIT → local per-key map updates. Model cost **K+5**. A single PUT routed there adds handler audit 3 ⇒ **K+8**, so a one-key bulk-dispatch PUT is still 9 operations. A caller making one genuine multi-key model call pays one sync bump, not K. There is no general bulk PUT handler here.
- **Request policy K keys** (`model/request_policy.go:259-297`): BEGIN → for each key SELECT via FirstOrCreate (`:276`), conditional INSERT if missing, UPDATE (`:279`) → COMMIT → sync-task BEGIN/INSERT/COMMIT → local map/snapshot. Let M be missing keys: model **2K+M+5**; plus handler audit3 ⇒ **2K+M+8** (single existing key 10, missing 11).
- **Pricing** (`model/model_pricing_config.go:69-74,705-756`): P=11 pricing option keys. BEGIN → one SELECT pricing options FOR UPDATE (`readModelPricingMaps :99-101`, called `:709`) → M separate inserts of missing default keys (`:717-729`) → P UPDATEs regardless of which pricing key the PUT changed (`:733-741`) → COMMIT → sync-task BEGIN/INSERT/COMMIT → local map updates → synchronous RefreshPricing (`:755`, `model/pricing_refresh.go:6-13`). RefreshPricing reads enabled abilities joined to channels (`model/ability.go:34-40`), all model metadata (`model/pricing.go:226`), and all vendors (`:235`): 3 SELECTs. If MemoryCacheEnabled=false, additionally one channel SELECT plus optional allocation SELECT for each distinct relay-config-relevant channel D (`model/pricing.go:157-205`, `model/channel_cache.go:401-404`, `model/channel.go:1181-1187`); no such channel reads when memory cache enabled. Handler audit adds3. **P+M+12 = 23+M** with memory cache; add D+number of those D whose allocation revision>0 otherwise. This cost repeats if a page sends multiple pricing-map PUTs. No Redis command found in these paths.
- **Passkey-domain keys (including ServerAddress)** are NOT ordinary PUTs: controller branches at `controller/option.go:424-433`. `model/passkey_option.go:106` BEGIN → one batch INSERT defaults ON CONFLICT DO NOTHING (`:64`) → four sequential SELECT option FOR UPDATE (`:67-70`) → conditional credential RP-ID SELECT if removing RP IDs (`:186-189`) → four Save UPDATEs for the normalized core domain keys (`:227-233`; other supplied keys add saves) → COMMIT → sync-task BEGIN/INSERT/COMMIT (`:243`) → local application. Existing core rows: **14 DB operations**, plus 1 credential SELECT if removal; preview/confirmation errors roll back and omit subsequent publication. A Save of an additional absent option can conditionally fall back to INSERT after zero-row UPDATE; do not assume one statement for such extra keys. Unlike ordinary management audit, handler's `recordPasskeyDomainAudit` uses synchronous legacy operation auditing (`controller/audit.go:78-102` → `model/log.go:315-335`): username cache read, conditional DB fallback, then log-store insert. With Redis enabled, username read attempts Redis; see qualification below. Domain-security workflow is not safe to replace with an ordinary bulk write.

### Reloads and local side effects

**No InitOptionMap or LoadOptionsFromDatabase is called synchronously by ordinary UpdateOption or the PUT handler.** InitOptionMap calls LoadOptionsFromDatabase at `model/option.go:209`; that reload does one AllOption SELECT (`:216-223` → `:27-30`) and iterates local updates (`:230-244`). SyncOptions runs that reload in a timer loop (`:248-255`), not on the handler's critical path. Remote sync delivery is separate from inserting the sync task.

Local per-key effects: hierarchical configs via `handleConfigUpdate :798-831`; performance UpdateAndSync (`:826`, `setting/performance_setting/config.go:50-75`) copies local values; billing invalidates pricing/exposed-data local caches (`:828-829`). These ordinary local effects add no Redis command or DB statement. Pricing's explicit RefreshPricing is the expensive exception detailed above.

Redis qualification for passkey legacy audit (not present on ordinary outbox audit): `model/log.go:316` → `model/user.go:1532-1542` → `model/user_cache.go:200,129-153`. A valid cache hit issues **HGETALL** (`common/redis.go:167`) then **MGET** auth fence/version (`model/user_auth_cache.go:112`), in that order: **2 Redis commands**. A plain cache miss issues HGETALL → user SELECT (`model/user_cache.go:110`) → MGET floor (`:115`) → EVAL cache population (`model/user_auth_cache.go:93`), normally **3 Redis commands +1 DB SELECT**; malformed/fenced/error variants can make another MGET or fail through to the username SELECT (`model/user.go:1541-1542`). The successful miss path hydrates synchronously; the outer GetUsernameById DB fallback additionally schedules asynchronous cache publication, not counted before response. Then the legacy audit sink does BEGIN → INSERT audit_logs → COMMIT on a relational log store (`model/audit_log.go:192`); ClickHouse has its own direct insert behavior. Thus a normal core passkey save with warm Redis is **14 primary DB +3 relational log DB operations +2 Redis commands**, or18 DB operations when credential-removal scanning adds one SELECT. Redis disabled adds the username SELECT instead of Redis. Missing/empty usernames can cause another lookup in RecordAuditLog (`model/audit_log.go:78-79`).

## 2. Invitation and redemption batch creation

Both handlers use CreateCodeBatch, NOT N per-code INSERT calls.
- Invitation: `controller/invitation.go:65-93`; local loop `:79-90` builds N rows/keys, returns one slice.
- Redemption: `controller/redemption.go:118-147`; local loop `:133-145` builds N rows/keys, returns one slice. Existing limit N<=100. Compliance/expiry validation is local, no database read in these validations.

Normal fresh request, ordered:
1. BEGIN (`model/code_operation.go:203`).
2. SELECT management_operation_batches by operation_id (`:205`).
3. INSERT pending operation identity (`:220-232`).
4. One multi-row INSERT invitations/redemptions (`:238-240`), N rows in the same statement under current default CreateBatchSize=0. N-code construction loop does no I/O. No model hooks on these code structs add I/O.
5. UPDATE operation state/result_json (`:254-256`).
6. INSERT audit_event_outbox (`:259` → `model/audit_outbox.go:169`).
7. COMMIT → handler markAuditLogged and JSON (`controller/invitation.go:107-115`, `controller/redemption.go:164-174`).

**7 DB operations, 0 Redis; independent of N (1..100) in statement count.** N=100 still7; RTT component 1.12–2.24s. Existing completed replay: BEGIN → SELECT → COMMIT = **3**, no business insert and no second audit. Recoverable uncertain commit/unique race condition adds **one fresh recovery SELECT** (`model/code_operation.go:286-306`), after whatever failed/rolled-back statements already occurred. It does NOT create another batch. Rejected ownership/digest/type is final. No FOR UPDATE here; the unique operation-id INSERT serializes competing fresh claims.

## 3. Channel enable/disable status toggle

Handler `controller/channel.go:1771-1803`; model `model/channel_publication.go:409-473`.

PostgreSQL, changed status, allocation revision>0:
1. BEGIN (`ChannelBudgetTransaction`, `model/channel_budget_store.go:29`).
2. SELECT channel id FOR UPDATE (`:36`) — row serialization even for non-ledger operations.
3. SELECT channel snapshot (`model/channel_publication.go:101`).
4. SELECT active allocations (`:104` → `model/channel_key_entry.go:57-60`), only when revision>0; the batched context suppresses duplicate AfterFind reads.
5. UPDATE channel status/info (`model/channel_publication.go:451`).
6. UPDATE abilities.enabled (`:454-455`).
7. SELECT newest sync task id FOR UPDATE (`model/system_sync_task.go:60`).
8. INSERT pending channel sync task (`:63,78`).
9. COMMIT.
10. Local targeted publication (`model/channel_publication.go:470,263-286`), no channel-table reload/DB/Redis.
11. **Disable only, changed=true:** close local sockets then Redis **PUBLISH x1** if RedisEnabled && RDB!=nil (`controller/channel.go:1791` → `service/ws_close.go:11` → `pkg/wsmanager/wsmanager.go:105-110,130-147`). This awaits Publish before handler audit. Enable and unchanged status do not broadcast.
12. Handler audit: BEGIN → INSERT outbox → COMMIT (`controller/channel.go:1794`, durable path above).
13. JSON response.

**Changed migrated channel: 12 DB operations; disable adds 1 Redis command, enable0.** Legacy revision=0 subtract allocation read =>11 DB. Same-status no-op: BEGIN + id lock + channel SELECT + optional allocation SELECT + COMMIT, then audit3 => **7+A** (8 migrated). No sync writes, no broadcast when unchanged. SQLite adds the writer-lock dummy UPDATE (`model/channel_budget_store.go:30-33`) and omits FOR UPDATE syntax, but the SELECT remains one statement.

Example migrated disable: (12+1) × RTT = 2.08–4.16s. Top contributors: nine-operation mutation transaction plus separate three-operation audit transaction; two explicit lock SELECTs; disable broadcast.

Batch status (`controller/channel.go:1806-1841`) loops **sequentially** over N IDs, each with its own ChannelBudgetTransaction. Let C changed and A_i=1 for migrated allocations: DB **sum_i(4+A_i+4*changed_i)+3**. All migrated/all changed => **9N+3**; one Redis PUBLISH for the accumulated changed-disabled IDs (not N). N=10 =>93 DB +1 Redis; theoretical RTT component15.04–30.08s. Handler stops on first error; earlier commits remain committed. This is not one atomic N-channel transaction.

## 4. Channel priority/weight save and drag reorder

### Single priority or weight cell save

UI uses the full edit endpoint: `web/src/features/channels/lib/channel-actions.ts:237`, `.../api.ts:327-336`; `{[fieldName]:value}` still goes through `controller/channel.go:1368` UpdateChannel, not the lightweight reorder model.

For a migrated channel with **only priority/weight changed**, no credential/type/plugin change, nonempty unchanged abilities:
1. Pre-transaction SELECT channel (`controller/channel.go:1399` → `model/channel.go:1185`).
2. AfterFind SELECT active allocations (`model/channel_key_entry.go:289-300`).
3. BEGIN (`model/channel_publication.go:509` → ChannelBudgetTransaction).
4. SELECT id FOR UPDATE (`model/channel_budget_store.go:36`). The id-only projection has zero allocation revision, so no AfterFind allocation SELECT here.
5. SELECT full stored channel (`model/channel_publication.go:511`).
6. AfterFind allocation SELECT (`model/channel_key_entry.go:298`).
7. reconcileLockedKeyEdit active allocation SELECT (`model/channel_plugin_credential.go:55-64` → `model/channel_key_entry.go:195`). This is another read, even though priority/weight does not edit keys.
8. UPDATE channel (`model/channel_publication.go:537`).
9. DELETE all abilities for this channel (`model/ability.go:313`).
10. **B=ceil(A/50)** INSERT ability chunks (`model/ability.go:323-325`), where A=generated routing abilities (model/group combinations), not key count. A=0 ⇒ B=0.
11. INSERT audit outbox (`model/channel_publication.go:555-557`, Prepare from `controller/channel.go:1654-1660`). No separate handler audit transaction for this save.
12. SELECT channel snapshot again (`model/channel_publication.go:563` → `:101`).
13. SELECT active allocations again (`:104`).
14. SELECT newest sync task FOR UPDATE, then INSERT sync task (`:568` → `model/system_sync_task.go:60,78`).
15. COMMIT, local publish (`model/channel_publication.go:574-578`), mark audit handled, response (`controller/channel.go:1683+`).

**Total 15+B DB operations, 0 Redis for this pure edit**; A<=50 and >0 ⇒16 operations =2.56–5.12s RTT component. Sequential N independent cell saves =>sum(15+B_i), i.e.160 operations for N=10,A<=50. No full InitChannelCache remains on this path, but repeated allocation reads and full ability replacement remain.

Conditionals not silently counted into the above: revision=0 invokes HasTable schema query (`model/channel_plugin_credential.go:54`) and can enter allocation migration: another HasTable (`model/channel_key_entry.go:494`), per-key INSERT/UPDATE (`:344-396`) and allocation scan; absence of schema also checks the inactive marker. Those costs depend on K allocations and migration state, so the migrated-path constant is not applicable. Actual key/mode edits also call persistKeyEntries with one statement per live or retired entry; absent in the pure priority/weight case. A plugin/type/proxy/key edit can invoke other validation/revocation paths; excluded from the pure cell-save formula, not assumed free for arbitrary full edits.

### Drag reorder / undo

`controller/channel_priority.go:21-38` → `model/channel_priority.go:68-113`.
- Preview: BEGIN → SELECT id,priority of ALL channels (no FOR UPDATE) (`:71-77`) → COMMIT = **3**. It computes proposed changes locally and does not write.
- Commit: BEGIN → SELECT id,priority of ALL channels FOR UPDATE (`:71-77`) → **for each of C changed channels**, UPDATE channels.priority then UPDATE abilities.priority (`:91-101`) → COMMIT. Targeted cache publication is local (`:120-147`); then public BumpSyncTopic adds BEGIN/INSERT sync-task/COMMIT (`:110-111`). **Total 2C+6**, provided C>0. C=0:3 (no sync). No handler-specific audit enqueue in controller/channel_priority.go; generic audit middleware is excluded by request. 0 Redis.
- Read order GET: one SELECT all id/priority (`model/channel_priority.go:58-62`).
- UI calls fresh order GET on each gesture (`web/src/features/channels/hooks/use-channel-priority-order.ts:96-104`), can make P debounced preview POSTs (`:116-130`), then commit. Whole gesture total **1+3P+(2C+6)**. Example C=1,P=1 =>12 DB operations; C=10,P=1 =>30; commit alone C=10 =>26 /4.16–8.32s RTT component. Preview calls depend on interaction; no fixed P is assumed.

## What can safely leave the response critical path (and what cannot)

- Already deferred: normal audit sink delivery. Keep durable outbox acceptance before success; moving enqueue itself after response widens the documented crash-loss gap. Better reduce transactions by atomically enqueuing with mutation, not by fire-and-forget audit.
- Ordinary BumpSyncTopic is explicitly best-effort with periodic full reload recovery (`model/system_sync_task.go:43-50`): a bounded post-response worker can preserve that existing eventual-convergence contract, but must snapshot inputs and retain recovery; never defer the transaction-bound channel sync obligation that promises durable publication ordering.
- Pricing materialized cache rebuild (the synchronous three+ SELECT RefreshPricing) can be deferred ONLY if it publishes a versioned new snapshot and callers tolerate stale cache until rebuild; preserve immediate invalidation. This is a design option, not verified existing semantics.
- Keep local routing publication and local socket closure before reporting a channel disabled; otherwise requests can still use a channel after management success. Remote Redis close broadcast could be worker-driven only with a durable close obligation/retry and an explicitly accepted cross-node delay; a bare goroutine is not a safe equivalent.
- Ability updates, row locks, operation identity, code rows, normalized passkey trust writes and audit obligations are correctness work, not safely removable from commit. Reduce round trips by batching/set-based writes inside the same transaction instead (e.g. reorder CASE updates), with separate cross-database verification.
- Frontend status/option invalidation refetches need not block success. Current ordinary options hook already uses stale-without-refetch for system-options; inspect special hooks separately. A real typed bulk options API could amortize K PUTs, but must preserve special security/pricing/passkey/request-policy invariants; there is no such general endpoint in this snapshot.

## Honest status

Counts are a source-derived PostgreSQL normal-path inventory, not observed packet traces or performance measurements. Conditional schema, error fallback, driver reprepare/retry and passkey username-cache internals are explicitly qualified. Existing dirty option.go and option_upsert_test.go were left unchanged during this investigation. Root must verify actual statement traces/latency in GitHub Actions or deployment instrumentation; no local executable validation was performed.
