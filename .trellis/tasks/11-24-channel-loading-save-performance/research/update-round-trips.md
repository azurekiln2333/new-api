# PUT /api/channel/ — synchronous round-trip inventory

Source-only investigation, 2026-11-25, CURRENT dirty working tree (not a claim about the deployed binary). ManageMultiKeys conversion is paused; all existing dirty source edits are preserved. Only this research note was written. No tests/build/vet/lint/format/typecheck/scripts/DB probes were run. Shared authentication/rate-limit middleware and subsequent list-refetch requests are excluded.

## Counting convention and assumptions

Count SQL statements plus transaction-control operations (BEGIN/COMMIT), not returned rows. This is a source-derived sequential database-operation count, **not an observed wire-packet count**: prepared-statement preparation/cache misses, driver protocol messages, pool acquisition, server execution and lock waits cannot be assigned exact network RTT counts from handler source alone. PostgreSQL uses prepared statements (`model/main.go:159`, `model/gorm_logger.go:25-29`). GORM v1.25.12 / postgres driver v1.5.9 are declared in go.mod.

Baseline: successful ordinary configuration edit; existing migrated channel (`KeyEntryRevision > 0`); valid stored/request settings; unchanged key identities/scopes/bindings; nonempty audit EventId (normal controller draft); G distinct groups and M distinct public models each group serves, no group whitelist reduction; no early validation failure. K is the number of current active key allocations. Both MemoryCacheEnabled modes follow the same SQL count for this PUT.

Let A be the actual number of distinct permitted public group/model ability pairs; A <= G*M. C = ceil(A/50), with C=0 when A=0. The loops build A ability VALUES rows in memory, **not A separate INSERT statements**. No G*M*K SQL loop exists on this path.

## Execution order: migrated ordinary save

Entry: router/channel-router.go:83 -> controller/channel.go:1368 UpdateChannel. Line references refer to the files as read during this investigation; concurrent peer edits may shift them.

| Operation # | Sequential operation before response | Source | Condition / rows |
|---|---|---|---|
| — | Parse raw body twice, reject status field; strip read-only fields; optional TaskPluginBind authorization | controller/channel.go:1368-1397; service/authz/resolver.go:10; assignment.go:7; enforcer.go:63 | No SQL/Redis: role mapping and already-loaded Casbin enforcer |
| 1 | SELECT existing channel by id | controller/channel.go:1400 -> model/channel.go:1185 | Always on successful path |
| 2 | SELECT active channel_key_entries ordered position,id | model/channel_key_entry.go:289-305 -> :193-196 | AfterFind for migrated row; returns K rows in **one** statement |
| — | ValidateSettings, provider/plugin registry/config checks, sensitive-write permission and access-token scope checks | controller/channel.go:1416; :726-890; model/channel.go:2096; controller/channel_authz.go:11; middleware/auth.go:320 | Ordinary valid inputs: no SQL/Redis. Additional conditional branches below |
| — | Compute next key; CheckSubmittedPluginCredentials; build audit actor/draft callbacks | controller/channel.go:1478-1664; service/provider_plugin.go:625; controller/channel_audit.go:29 | Pure parsing/in-memory registry/context; no username lookup |
| 3 | BEGIN | model/channel_publication.go:509 -> model/channel_budget_store.go:29 | One outer business transaction |
| 4 | SELECT channel id FOR UPDATE | model/channel_budget_store.go:36 | PostgreSQL row lock; SELECT projects only id so AfterFind sees zero revision and issues **no** allocation query |
| 5 | SELECT full stored channel | model/channel_publication.go:511 | A second channel SELECT after the row-lock SELECT |
| 6 | SELECT active channel_key_entries | model/channel_key_entry.go:298 -> :193-196 | AfterFind on full stored row, K rows |
| 7 | SELECT active channel_key_entries AGAIN | model/channel_plugin_credential.go:55-65 -> model/channel_key_entry.go:193 | reconcileLockedKeyEdit does this even when edit.EditKeys is nil. KeyEntryRevision>0 short-circuits HasTable (zero schema probes here) |
| — | Optional key/mode mutation; apply config; compare/project maps | model/channel_plugin_credential.go:68-118; controller/channel.go:1612-1659 | Baseline has zero key-entry INSERT/UPDATE/retire statements |
| 8 | UPDATE channels with merged stored configuration | model/channel_publication.go:536 | edit.Apply is nonnil for controller PUT, even when only one configuration field changes |
| 9 | DELETE abilities WHERE channel_id = id | model/channel_publication.go:544 -> model/ability.go:313 | Always runs, even if group/models/ability-derived fields did not change |
| 10 .. 9+C | C bulk INSERT abilities ... ON CONFLICT DO NOTHING | model/ability.go:325-328; :260-286 | Each chunk <=50 rows; A pairs derived in memory from PublicModels + per-group whitelist |
| 10+C | INSERT audit_event_outbox | model/channel_publication.go:556 -> model/audit_outbox.go:125-170 | Exactly one with normal nonempty draft. Empty EventId skips it; no synchronous LOG_DB delivery |
| 11+C | SELECT full channel for publication snapshot | model/channel_publication.go:562 -> :101 | Inside transaction. Batched context marker suppresses AfterFind here |
| 12+C | SELECT active channel_key_entries for publication snapshot | model/channel_publication.go:104 -> model/channel_key_entry.go:32-62 | Explicit projection; one channel => one chunk; K rows |
| 13+C | SELECT newest system_tasks id for channel sync topic FOR UPDATE | model/channel_publication.go:567 -> model/system_sync_task.go:58-63 | This is a separate topic-wide serialization point in addition to channel row locking |
| 14+C | INSERT system_tasks channel-sync obligation | model/system_sync_task.go:68-77 | GenerateSystemTaskID (:94 in system_task.go) and BeforeCreate (:76) are local randomness/timestamps, no Redis |
| 15+C | COMMIT | model/channel_budget_store.go:29, GORM Transaction | No success returned if commit fails |
| — | Conditional CacheUpdateChannelKey; targeted routing + bound-index publication; generation advance | model/channel_publication.go:572-576; :263-285; channel_plugin_credential.go:273 | No SQL/Redis. Per-channel locks and process-wide channelPublicationLock can add wait time, not statement counts |
| — | BumpTaskAliasChannelRevision; InvalidatePricingCache | model/channel_publication.go:275-285; model/task_model_alias.go:44; pricing.go:98 | Atomic revision bump and mutex-protected pointer/time reset only. No eager alias scan or pricing query on CURRENT PUT path |
| — | Optional proxy-client eviction; enqueue removed-plugin-credential revocation | controller/channel.go:1676-1679; service/http_client.go:458; provider_plugin.go:737, :552-567 | Eviction local; revoke hook scheduled via gopool.Go, not awaited |
| — | markAuditLogged; redact key/channel info; write HTTP JSON response | controller/channel.go:1683-1691; controller/audit.go:138; model/channel_balance.go:24 | No SQL/Redis. GetBalanceDetail only parses existing OtherInfo |

**Baseline total: 15 + ceil(A/50) SQL/control operations; 0 Redis commands.**

For **G=2, M=30, K=5**, all public models available to both groups: A=60, C=2 => **17 operations** (15 SQL statements + BEGIN + COMMIT). Six of the SELECTs read channel/allocation data (3 channel SELECTs plus the initial origin channel SELECT makes 4 total channel reads including the row-id lock; 4 allocation SELECTs): precisely channel SELECTs at #1,#4,#5,#13 and allocation SELECTs at #2,#6,#7,#14 in the expanded 17-step sequence. The actual numbered table uses C-dependent positions; the point is **4 channel reads + 4 allocation reads**, not 60 per-ability statements or 5 per-key reads.

At the supplied 160-320ms network RTT, 17 fully serialized operations imply approximately 2.72-5.44 seconds of one-RTT-per-operation latency alone; this is illustrative arithmetic, **not a measured duration or packet count**. The reported 320-1400ms SLOW SQL timings may include server/lock/protocol delay and must not be added again as independent RTT components.

## Conditional key/allocation and schema work (in the same transaction)

### Migrated key replacement / binding-scope reconciliation

`reconcileLockedKeyEdit` (model/channel_plugin_credential.go:78-100) calls `persistKeyEntries` only if key identity/count changed, or if a key-indexed map changed and syncKeyEntryAttributesFromProjection reports a real difference.

`persistKeyEntries` (model/channel_key_entry.go:344-397):
- For every surviving allocation: **one UPDATE per existing entry** (:383), or **one INSERT per new entry** (:379).
- For every retired existing allocation R: **one UPDATE per retired entry** (:390).
- Revision increment, credential parsing, duplicate checks and applyKeyEntryProjection are in memory. No budget account/reservation/ledger SQL is called by this persistence function.
- Let K' be the number of surviving/new entries after the edit. Extra statements = K' + R. Total = **15 + C + K' + R**. For the example, retaining five rows with a scope rewrite gives **22**; replacing all five with five new identities and retiring five old rows gives **27**, provided that edit is accepted by planKeyEdit. A rejected ambiguity/revision check stops and rolls back instead.
- Merely changing MultiKeyMode need not cause K writes: the attribute synchronizer ignores polling mode and writes only when entry fields differ.

### Legacy revision-zero channel, allocation table present

- Initial origin read and full stored read do not run AfterFind allocation SELECTs (save 2 vs migrated baseline).
- `reconcileLockedKeyEdit` evaluates HasTable once (:55); `CreateChannelKeyEntries` evaluates HasTable again (:494): **2 PostgreSQL information_schema.tables SELECT count(*) probes**, not zero and not BEGIN/COMMIT per probe. Verified in already-installed postgres v1.5.9 source, migrator.go:210-216; CurrentSchema (:617-629) embeds CURRENT_SCHEMA() rather than making a separate query.
- `CreateChannelKeyEntries` loops K entries through persistKeyEntries: **K single-row INSERTs**, then the reconcile reload SELECT still runs. The final publication snapshot now has nonzero revision and projects once.
- If no further key edit is requested: total = **15 + C + K** (baseline minus 2 skipped hooks, plus 2 schema probes, plus K inserts). Example **22**. A subsequent edit reconciliation adds its own K'+R as above.
- With allocation table absent, the first HasTable is false so reconcile never calls CreateChannelKeyEntries; no entry reload/persistence occurs, and final revision-zero snapshot skips projection. Total = **12+C** (example 14). The inactive-schema marker reader at channel_key_entry.go:482 is **not reached through that false reconcile branch**; do not count it on this route merely because it exists in CreateChannelKeyEntries.

### Budget accounting

The name ChannelBudgetTransaction does **not** imply budget account reads: its wrapper only begins, locks channel id, and invokes the callback (model/channel_budget_store.go:25-41). For a regular PUT it performs **zero channel_budget_accounts / reservations / reservation_items / operations queries**. PublicModels and KeyServesModel (model/channel.go:725, :428) are in-memory permission derivation, not ChannelBudgetAllows.

A narrow conditional exception occurs in the append-key duplicate check (controller/channel.go:1530-1533): for each appended line already seen, `!model.ChannelBudgetsEnabled()` is evaluated. ChannelBudgetsEnabled -> channelBudgetActivation -> ActiveChannelBudgetTx (model/channel_budget_activation.go:19-56) performs **one options activation-marker SELECT per call unless this DB handle is already in the activated cache**. Let D be such duplicate-candidate evaluations and H those that miss the positive activation cache: add H SELECTs (0<=H<=D). If inactive, the first duplicate rejects the request before BEGIN; if activation is discovered true, it is positively cached. This marker check is not a quota-account lookup. Ordinary unchanged keys/nonduplicate appends do not call it.

### Invalid legacy JSON side effects

`GetSetting` and `GetOtherSettings` are NOT universally pure: malformed stored JSON triggers global-DB repair UPDATEs at model/channel.go:2156 and :2179. Each successful repair outside a transaction uses GORM default **BEGIN + UPDATE + COMMIT** (3 operations; no SkipDefaultTransaction override in model/gorm_logger.go:25-29). These global DB writes can even be reached during abilities/publication processing and are not joined to the current tx. Count **+3 per actually executed successful repair call**, in the exact point of the getter invocation; invalid request settings instead fail ValidateSettings and exit before mutation. Distinct copies of a corrupt channel may each repair; do not presume a fixed once-per-request count. Such corruption is excluded from the 17-operation valid-data baseline.

### Plugin-specific validation and failures

- controller/channel.go:1593 -> checkPluginChannelKeys (:1942) -> service.CheckPluginKeys (provider_plugin.go:761): if provider plugin has checkKey, each nonempty changed static line runs one synchronous hook (:783), up to the number of newly submitted distinct/changed lines actually traversed. Unchanged static lines and managed credential lines are skipped. The hook may use upstream network I/O; its count/latency is **plugin-dependent and cannot be determined from G/M/K alone**. No fixed Redis command or host SQL is performed by CheckSubmittedPluginCredentials (:625), which only validates credential strings.
- Existing permission/scope checks inside this handler are local reads of Casbin/context; shared auth/rate limit/step-up middleware is outside this inventory as requested.
- Any failure before BEGIN truncates the sequence. Failure inside business tx replaces COMMIT with ROLLBACK and skips post-commit publication/success. GORM CRUD callbacks on an existing SQL tx do not add nested wire BEGIN/COMMIT around each INSERT/UPDATE; see installed gorm v1.25.12 callbacks/transaction.go:7-31 (ErrInvalidTransaction is ignored for an already-transactional connection).

## Redis inventory: zero commands in this successful handler

No GET/SET/DEL/EVAL/EXPIRE/PUBLISH command is issued by the traced PUT success chain. The handler does not fetch the actor username; actor/context fields are copied at controller/channel_audit.go:29. Sync topics insert SQL system_tasks rows, not Redis messages. Cache publication and alias/pricing invalidation are process memory operations. Revoke hook execution is background/plugin-specific and is not an awaited handler Redis operation. This does not claim the excluded authentication middleware or subsequent list API call has zero Redis work.

## Top three source-proven contributors

1. **Redundant serialized channel/allocation reads**: 4 channel SELECTs (including row-id FOR UPDATE) + 4 allocation SELECTs = **8 operations** for a migrated ordinary save. Two allocation scans occur back-to-back during stored-row hydration and reconciliation. These are repeated K-row results, not K distinct round trips each. Row lock must remain; unnecessary duplicate reads can be eliminated within the transaction, not deferred unsafely.
2. **Ability rebuild on every save**: **1 DELETE + ceil(A/50) INSERTs**, even for an unrelated name/remark/config edit. At G=2,M=30: 3 statements, not 61. On key/scope changes, add the per-entry **K'+R** persistence loop, which becomes the dominant variable contributor (5-10 extra statements in the examples).
3. **Durability/coordination overhead**: BEGIN+COMMIT (2), audit outbox INSERT (1), newest-topic FOR UPDATE + task INSERT (2) = **5 operations**, including a topic-wide lock that can contend across unrelated channels. Pool wait, channelPublicationLock and pollingLock waits are additional local/DB contention, not extra SQL counts.

## Safe deferral vs work that must stay before success

**Already deferred/lazy in CURRENT dirty source:** audit LOG_DB delivery (outbox worker), peer/full reload (SQL sync obligation), removed-provider revoke hook (gopool.Go), full alias rebuild (revision bump now, first alias read rebuilds), full pricing regeneration (InvalidatePricingCache just clears local pointers). There is **no eager alias full-table SELECT** on this current PUT save path; an older deployed binary may differ. Do not attribute a full alias scan to this handler from the previous source version.

**Could run after response without weakening local route correctness:** delivery/drain of the already-committed audit event; execution/ack of the already-committed peer sync task; provider's external revoke hook (already asynchronous), optional logging/telemetry. These do not remove the current SQL round trips because their durable enqueue stays before success. Optional derived-cache prewarming could be background, but synchronous invalidation/revision advancement must still precede success.

**Must not simply move after response:** configuration commit, authoritative key/allocation reconciliation, permissions/bindings and ability-row consistency, final projected channel snapshot and local routing/key/bound-index publication, alias invalidation, pricing invalidation, immediate revocation of local sessions when applicable. Returning success before those allows old credentials/permissions/routing on the saving node. Audit enqueue and sync-task persistence currently belong to the business transaction; moving them out would change the durability contract and is not a safe scheduling-only optimization.

Most safe latency reduction is **elimination/coalescing**, not response-before-commit: reuse already-loaded allocations within the locked transaction; derive a publication snapshot from committed in-tx state instead of re-querying it if all projections are proven complete; avoid ability delete/reinsert when no ability-derived field changed; batch changed/retired entry updates with full identity/CAS semantics preserved. These are investigation recommendations, not source edits performed here.

## Scope boundary: slow list after save

The drawer waits for the PUT sequence above. The subsequent list/refetch is a separate request with its own middleware, count/page/projection work and possible pricing reads. No statement from that second request is included in the 17-operation PUT count. This note does not claim the user-observed post-save list latency has been measured or attributed.
