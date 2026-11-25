# Sync (WebSocket close) and process-local `/api` routes — read-only investigation

Worker: **pi-fwd-sync**. Active task: `.trellis/tasks/11-25-console-forward-to-master`.
Date: 2026-11-25. Root integrator: Claude (w4C:p17). Core owner: pi-billing (w4C:pJ).

Scope of this file: design §8 "Potential local-only side effect" + §11 items 1 and 9.
**No production or test source was changed. No local tests/builds/vet/lint/gofmt/db probes
were run. No implementation was performed: question (a) resolved to "existing mechanism
already covers it", so there is no gap to fix.**

---

## (a) Channel disable / status toggle → do all nodes close their own relay WebSockets?

### Answer: YES. The existing Redis PUBLISH/SUBSCRIBE path reaches every node, and each
node closes the realtime/responses sockets it owns. **No gap, no implementation needed.**

### Call chain, verified against the current working tree

| Step | Location |
|---|---|
| Toggle handler | `controller/channel.go:1773` `UpdateChannelStatus`; close only when the status actually changed and is not "enabled": `controller/channel.go:1793-1795` → `closeActiveChannelWebSockets([]int{id})` |
| Batch toggle | `controller/channel.go:1808` `BatchUpdateChannelStatus`; accumulates changed non-enabled ids and calls once: `controller/channel.go:1832-1834` |
| Delete/other paths | `controller/channel.go:1159,1194,1237,1344,2578,2670,2750,2824,2910` (same helper) |
| Helper | `controller/channel.go:93-95` `closeActiveChannelWebSockets` → `service.CloseActiveWebSocketsForChannels(ids, service.ChannelDisabledCloseReason)` |
| Service shim | `service/ws_close.go:8` (single) and `service/ws_close.go:12` (batch) → `wsmanager.CloseChannelsAndBroadcast` |
| Local close + publish | `pkg/wsmanager/wsmanager.go:105-111` `CloseChannelsAndBroadcast` = `CloseChannels(...)` then `PublishCloseChannels(...)`; publish errors are logged, not returned (`:107-109`) |
| Publish | `pkg/wsmanager/wsmanager.go:130-148` → topic `new-api:wsmanager:channel-close:db:<redisDB>` (`:150-154`), payload `{channel_ids, reason, origin}` |
| Subscribe start | `main.go:109` `wsmanager.StartSubscriber(context.Background())` — **unconditional, outside every `IsMasterNode`/node-type branch**, so master and every worker subscribe |
| Subscriber | `pkg/wsmanager/wsmanager.go:113-128` (`subscriberOnce`, returns early only when `!common.RedisEnabled || common.RDB == nil`); loop at `:156-176` |
| Cross-node apply | `pkg/wsmanager/wsmanager.go:171-174`: skips messages whose `Origin` equals this process (`:171-173`), otherwise `CloseChannels(event.ChannelIDs, event.Reason)` — closes only sockets **owned by this node** |
| Socket registration (the only two kinds) | `relay/websocket.go:48` `wsmanager.Register(info.ChannelId, wsmanager.KindRealtime, ...)` (realtime WS, close-frame + both conns closed `:49-58`); `relay/responses_websocket.go:1058` `wsmanager.Register(channelID, wsmanager.KindResponses, s.closeForPolicy)` |
| Registry close semantics | `pkg/wsmanager/wsmanager.go:47-77` `Register` (idempotent close via `sync.Once`), `:93-103` `CloseChannels`, `:178-195` `takeEntries` (unregister-once), `:241-249` `getOriginID` (node+pid+nanotime ⇒ distinct per process) |

### Why this is sufficient after `/api` is forwarded to us01

1. The disable now executes on the **master**, which no longer holds the HK relay sockets, but
   it still runs `CloseChannels` + `Publish`; HK workers receive the message and close their own
   `KindRealtime` / `KindResponses` entries. Result is identical to today, where the toggle ran
   on the node the console domain pointed at and the other nodes closed via pub/sub.
2. Node identity for suppression is per process (`getOriginID`, `:241-249`), so the publishing
   node's own local close (`:106`) is not duplicated by its own subscriber message.
3. The publish is **awaited inside the handler** (`:110-112`), i.e. close-on-disable keeps its
   current ordering relative to the handler response and the subsequent audit write.
4. Relay WS/realtime paths stay local (`relay-router.go` `/v1/realtime`, `/v1/responses`), so
   registration still happens on the node that terminates the client socket.

### Limitations to record in the report (not forwarding regressions, but they bound F7)

- **Redis disabled ⇒ no cross-node close.** `StartSubscriber` returns at `:114-117` and
  `PublishCloseChannels` returns nil at `:131-134`. Today the same is true, so multi-node
  deployments already require Redis for this behavior.
- **Redis DB index must match across nodes** — the topic embeds `rdb.Options().DB` (`:150-154`).
  A node on a different `DB` index silently never receives closes. Deployment note, not a code
  change.
- **In-flight SSE relay streams are not closed** by a disable on any node, before or after
  forwarding (nothing registers them; only `KindRealtime`/`KindResponses` exist, `:12-13`).
  Behaviour is unchanged by this task; do not claim the toggle cancels streaming `/v1` requests.
- Any **future** WS/long-lived channel-bound session kind must call `wsmanager.Register`
  (both `Kind*` constants live in `:12-13`); prefix-forwarding does not change that contract.

**Conclusion for §11 item 9: rely on the existing mechanism; cite the table above. No edit to
`pkg/wsmanager`, `service/ws_close.go` or `controller/channel.go`.**

---

## (b) `/api` routes whose state lives only in the processing process

Class definition (per §11 item 4): a route must return an honest 503 `console master
unavailable` when the worker falls back **before any byte was sent**, whenever the state it
reads or mutates was created by an **earlier** request that ran on the master. Everything
else is safe to run locally on a proven pre-send fallback (nothing was executed on the
master, so nothing is duplicated or orphaned).

### B1. Master-process-local — MUST 503 on pre-send fallback

| Method + path | Handler | Process-local state |
|---|---|---|
| `GET /api/channel/:id/fingerprint/:job_id` | `controller/channel_fingerprint.go:210` `GetChannelFingerprint` → `service/channel_fingerprint.go:256` | in-memory registry `fingerprintJobs` (`service/channel_fingerprint.go:150-154`, `map[string]*FingerprintJob`); job payloads include per-probe results held only in that map |
| `POST /api/channel/:id/fingerprint/:job_id/cancel` | `controller/channel_fingerprint.go:219` `CancelChannelFingerprint` → `service/channel_fingerprint.go:265` | same registry; cancel targets the goroutine/context of the run started in that process (`StartChannelFingerprint` `service/channel_fingerprint.go:155-155+`, run continues on its own context) |
| `POST /api/channel/ollama/pull/stream` | `controller/channel.go:3204` `OllamaPullModelStream` (route `router/channel-router.go:115`) | long-lived in-process SSE pull against the operator's upstream; a "local fallback" would start a **second** real pull while the master's stream is already running/unreachable ⇒ duplicate expensive upstream work; §11 item 4 names it explicitly |

Router registrations for the above: `router/channel-router.go` fingerprint routes (three
`channelPermissionRoutes` entries for `/:id/fingerprint`, `/:id/fingerprint/:job_id`,
`/:id/fingerprint/:job_id/cancel`) and `router/channel-router.go:115` for the SSE stream.

### B2. Same family, but NOT master-local (no 503 required — verify, do not over-block)

| Method + path | Handler | Where the state actually lives |
|---|---|---|
| `POST /api/channel/:id/fingerprint` (start) | `controller/channel_fingerprint.go:36` `StartChannelFingerprint` | creates **new** state in whatever process serves it; a pre-send fallback creates a fresh run locally, nothing was orphaned on the master |
| `POST /api/channel/:id/plugin_login` | `controller/provider_plugin.go:70` | `model.AuthFlow` row (DB) + channel/plugin from DB |
| `POST /api/channel/:id/plugin_login/continue` | `controller/provider_plugin.go:93` | DB flow: `loadPluginLoginFlow` → `model.GetAuthFlow(...)` in `service/provider_plugin_login.go:330-354` |
| `POST /api/channel/plugin_login/callback` | `controller/provider_plugin.go:117` `CompletePluginLoginCallback` | same DB-backed flow |
| `GET /api/channel/:id/plugin_credentials` | `controller/provider_plugin.go:136` | DB channel credential rows |
| `GET /api/channel/update_balance/task/:task_id` | `controller/channel-billing.go:954` `GetChannelBalanceTask` | `model.GetSystemTaskByTaskID` — system-task table, readable from any node (comment at `:950-953` says so) |
| `GET /api/system-task/*` polls | `router/api-router.go:374-379` group | system-task table (runner executes on masters, state is shared) |
| `GET /api/log/export`, `/api/log/self/export` | `router/api-router.go:371-372` | streamed, generated per request; the only local bit is the one-export lock `service/log_export.go:27` `logExportLocks`, which is Redis-backed when Redis is enabled (`:33-56`) |
| `POST /api/channel/ollama/pull` (non-stream) | `controller/channel.go:3138` `OllamaPullModel` | synchronous request; pre-send fallback duplicates nothing that ran |

**Caveat to flag, not assert:** `POST /:id/plugin_login/continue` and `/plugin_login/callback`
may still drive a live in-memory plugin login session inside the jsplugin runtime while the
browser round-trips. The *flow* evidence is DB (`model.GetAuthFlow`), and the pending state is
bound to user id + session id, so a local execution on a worker either completes the flow on
shared state or fails cleanly. Treat as B2; if pi-billing prefers the conservative set it may
keep `plugin_login` in the 503 class (already the case in the current code, see below).

### B3. Current `middleware/console_forward.go` matcher — review note for pi-billing

`consoleProcessLocalRoute` (in pi-billing's owned file) matches by **path segment name**:

```
part == "fingerprint" || part == "plugin_login" || (part == "ollama" && next == "pull")
```

Consequences to decide on (owner's call, not edited by me):

- It 503s `POST /api/channel/:id/fingerprint` (start) — B2, not required by §11 item 4, and it
  would deny a start that is safe to run locally.
- It 503s `plugin_login` (start/continue/callback) — B2 under the evidence above; conservative,
  defensible, but not required.
- It does **not** 503 `POST /api/channel/ollama/pull` … actually it does (segment `ollama` then
  `pull`), and `DELETE /api/channel/ollama/delete` is not matched (idempotent upstream delete ⇒ fine).
- It matches on any segment, so a future non-channel route containing `fingerprint` inherits 503.
- Recommended shape if it is tightened: match **method + decoded path** for exactly
  `GET /api/channel/:id/fingerprint/:job_id`, `POST /api/channel/:id/fingerprint/:job_id/cancel`,
  `POST /api/channel/ollama/pull/stream`. Leave B2 routes to the ordinary pre-send fallback.

---

## (c) Node-local console controls that will act on the master after forwarding

All under `/api`, therefore forwarded, therefore **executed against us01 (master)** exactly as
§11 item 1 accepts. Each is a *process* control, not shared state:

| Method + path | Handler | What it does after forwarding |
|---|---|---|
| `POST /api/performance/gc` (`router/api-router.go:267`) | `controller/performance.go:162` `ForceGC` | runs `runtime.GC()` in the master process only |
| `GET /api/performance/stats` (`:264`) | `controller/performance.go:82` | master's counters/goroutines/heap |
| `POST /api/performance/reset_stats` (`:266`) | `controller/performance.go:155` | resets master's counters |
| `DELETE /api/performance/disk_cache` (`:265`) | `controller/performance.go:142` | clears the master's disk cache |
| `GET /api/performance/logs` (`:268`) | `controller/performance.go:222` (`getLogFiles` `:187`) | lists the master's log files on the master's filesystem |
| `DELETE /api/performance/logs` (`:269`) | `controller/performance.go:258` | deletes the master's log files |
| `GET /api/system-info` (+ subgroup) | `router/api-router.go` system-info group | master's runtime/CPU/memory figures |
| `GET /api/status` | `controller/misc.go:41+` | reports the master's `start_time`; §2 already records this (not a liveness probe) |
| `POST /api/system-task/log-cleanup` (`:378`) | `controller.CreateLogCleanupSystemTask` | enqueues a task that the master's system-task runner executes |
| `GET /api/plugin/task`, `/api/task_plugin_options`, `/api/request_plugin` | plugin management handlers | now configure the **master's** plugin registry; workers keep executing relay plugins from their synced copy — covered by the existing `SyncTopicPlugins` registration in `main.go` (`service.RegisterSyncTopic(model.SyncTopicPlugins, controller.SyncTaskPluginsOnceContext)`), so F7 holds through the existing topic, not through forwarding |

Notes for the report / UI expectation setting:

- pprof stays on its own listener (`main.go`, `ENABLE_PPROF`) and is **not** forwarded.
- Windows/memory/GC controls now mean "the master", so a per-node reading requires the
  master's `/api/system-info`/`/api/status/instances` data (shared) or a future node-target API
  (explicitly out of scope, §11 item 1).
- `GET /api/status/instances` and `/api/system-task` are DB-backed, so they still describe all
  nodes after forwarding — no divergence.

---

## Handoff summary (for pi-billing / root)

1. **§11 item 9 closed: no gap.** Existing disable path = local close + awaited Redis PUBLISH
   (`controller/channel.go:1793-1795`, `service/ws_close.go:12`, `pkg/wsmanager/wsmanager.go:105-111,130-148`);
   every node subscribes unconditionally at `main.go:109` and closes only its own
   `KindRealtime`/`KindResponses` sockets (`pkg/wsmanager/wsmanager.go:156-176`;
   `relay/websocket.go:48`, `relay/responses_websocket.go:1058`). No source change required.
2. **503 set (pre-send fallback only):** `GET /api/channel/:id/fingerprint/:job_id`,
   `POST /api/channel/:id/fingerprint/:job_id/cancel`, `POST /api/channel/ollama/pull/stream`.
   Everything else in (b) is DB-backed or creates fresh state.
3. **Current segment-name matcher over-blocks** `POST /:id/fingerprint` (start) and
   `plugin_login*` and is not keyed on method — owner's decision (their file; see B3).
