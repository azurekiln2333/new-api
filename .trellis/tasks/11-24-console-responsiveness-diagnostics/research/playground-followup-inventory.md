# Playground follow-up inventory — old-source research only

## Scope, authority and execution boundary

- Active parent: `.trellis/tasks/11-24-console-responsiveness-diagnostics`; coordinator `w5D:pG`; researcher `w6E:p4`; read-only helper `w6E:p5`.
- Source root inspected: **`D:\My2KProjects\new-api` only**, using explicit command working directories / absolute file paths. **`E:\MyNewProjects\WebProjects\new-api` was not inspected or modified.**
- This inventory is the researcher's **only writable file**. No product files, task manifests, other workers' files, Git state, target tree, production configuration or data were changed. No tests, builds, lint, formatter, browser, database operations or executable product validation ran.
- Read the parent `task.json`, `implement.jsonl`, `check.jsonl`, `prd.md`, `design.md`, `implement.md`; root/web `AGENTS.md`; project `shadcn-ui/SKILL.md` and `web/components.json`; applicable frontend model-selection/chat-layout and backend database/logging specs. Read `.agents/rules/billing.md` **in full** and `docs/plugin-api/v1.md` **in full** before tracing the affected billing/plugin boundaries.
- Findings below are **source observations**, not production reproduction, passed checks or deployment claims. Referenced line numbers are navigation aids in the inspected mutable old tree. Existing task files include future-dated follow-up labels; those labels are not evidence of execution dates.
- Root's recovery instruction after `ECONNRESET3` was to finish bounded research without further expansion. Read-only helper `w6E:p5` subsequently completed the bounded log/task/artifact map; its additional exact regression references are integrated below. No helper files were written and no R28 ownership was resumed.

## Executive decisions: reuse first, do not rebuild existing capabilities

1. **New project is not an unimplemented feature.** Creation, naming, index persistence, rename, delete and moving chats already exist. A newly created empty project is filtered out of the visible grouping, producing an apparent no-op after Save. Fix that projection and move the existing create action above New chat.
2. **Web search already exists end to end.** Keep the existing opt-in toggle, persisted setting, payload, conversion guard, citations and upstream billing path. No second search backend, scraping proxy, plugin or duplicate toggle is justified by this source.
3. **Attachment menu entries are placeholders, but the shared composer is not.** It already has picker/drop/paste, object URLs, previews, remove/clear, conversion and submit-file structures. Playground currently discards the submitted files and only stores/sends text. Wire the existing pipeline rather than install an upload widget or invent a general upload server.
4. **`/v1/files` is not an implemented reusable upload API.** Those handlers are `RelayNotImplemented`. The smallest working attachment path is bounded inline content on the current authenticated `/pg/chat/completions` route, subject to the selected upstream's support.
5. **Do not cache generated artifact bytes locally by default on the 20 GB server.** Artifact delivery already proxies upstream; the artifact store is deliberately disabled, including its advertised future S3 settings. Fix temporary spool bounds/lifecycle separately; existing diagnostics retention/compression is another existing mechanism, not an attachment archive to duplicate.
6. **Drawing Logs and Task Logs are protocol/workflow histories, not keyword detection.** An ordinary chat asking to draw does not automatically create either kind of task row.

## 1. Projects: capability, apparent no-response cause and minimal correction

### Existing action/data path

| Concern | Existing owner and observed contract |
|---|---|
| New chat position | `web/src/features/playground/components/conversations/conversation-panel.tsx:43–67`: fixed panel action above the scroll list |
| New project action | `.../conversations/conversation-list.tsx:82,239–246,319–325`: list-local `creatingProject`, trigger after rendered groups, existing `ProjectNameDialog` |
| Name dialog | `.../conversations/project-name-dialog.tsx:36–90`: shared business `Dialog`, `Input`, `Label`, trim-empty guard, Enter/Save, 80-character input bound |
| Creation | `web/src/features/playground/hooks/use-playground-state.ts:473–483`: normalize name, `createProjectMeta`, append project, persist existing conversation index; no network/API call |
| Other project actions | Same hook `:486–562`: rename/delete/move; deleting project ungroups chats rather than deleting their histories |
| Persistence | `lib/storage/storage.ts:73–80,550–593` and `storage-schema.ts:121–134`: versioned, user-suffixed browser index containing `projects`; existing legacy indexes may omit them |
| Group projection | `lib/state/playground-state-utils.ts:49–86`: projects ordered by creation; unknown project IDs go to ungrouped |
| Responsive host | `playground/index.tsx:151–170,247–256`: the panel is used in desktop aside and mobile Sheet; test actual responsive entry points |

### Source-supported cause vs unverified hypotheses

- **Confirmed source defect:** grouping does `const projectConversations = byProject.get(project.id); if (!projectConversations) continue` (`playground-state-utils.ts:73–74`). `createProject` creates no chat. Therefore creating a project does change state/persistence but gives no visible project until a chat is moved into it. Empty/all-archived projects also disappear from the open grouping.
- **Confirmed reachability gap:** `ConversationList` returns `No chats yet` for zero conversations (`conversation-list.tsx:117–123`), omitting its project actions/dialogs even if projects exist. Normal state seeds a chat, but the component's empty contract is still incomplete.
- **Not reproduced:** a click that never opens the dialog. The source has a click handler; do not claim an event-layer root cause without a failing interaction fixture. Desktop/Sheet composition and the constant `playground-project-name` ID deserve a focused regression, not a speculative shared Dialog rewrite.
- `persistIndex` deliberately defers index writes while unsaved messages exist (`use-playground-state.ts:322–333`); message saves retry with the existing warning. Preserve that behavior, do not make project Save silently delete pending messages.

### Slice P1 — frontend only, no new project API/schema

- Move the existing **New project** trigger and its single create-dialog state into `ConversationPanel`, immediately **before New chat** in the fixed action area. Keep the existing business dialog; remove the duplicate list-level create action/state/prop instead of retaining two create paths.
- Project rename/delete dialogs remain list-owned. Use the existing `Button` rather than adding another raw action button. Preserve i18n keys and all existing storage/actions.
- Include empty saved projects in grouping with `conversations: []`; show name and count 0, preserving collapse, rename/delete, and move targets. Keep the empty-chat hint scoped to chat content rather than returning before controls/project rows.
- Do **not** create dummy chats, auto-send requests, add server-side projects, change project delete semantics or globally rewrite dialogs.
- Acceptance: click/keyboard opens naming dialog, cancel makes no project, Save yields a visible empty project immediately and after reload; trigger precedes New chat on desktop/mobile; moving a chat into it works; deleting it preserves chats. No zero-chat or all-archived disappearance.

## 2. Playground web search: already delivered; preserve current contract

| Layer | Existing source |
|---|---|
| Toggle/accessibility | `components/input/playground-input-tools.tsx:80–82,128–154`: explicit toggle, `aria-pressed`, disabled state; tooltip says selected-model upstream support is required and extra charges are possible |
| Default and persistence | `constants.ts:58` defaults `web_search: false`; `types.ts:147`, `storage-schema.ts:48`, existing `saveConfig` restore it |
| Client request | `lib/streaming/payload-builder.ts:40–53`: current model/group/messages/stream plus optional admin channel; search on adds `web_search_options: {search_context_size:'medium'}`; off omits it |
| API/auth | `constants.ts:37`, `api.ts:59–68`, `router/relay-router.go:62–68`, `controller/playground.go:78–123`: session-authenticated `/pg/chat/completions`, existing distributor and ordinary relay through a temporary playground token context; unsupported access-token use rejected |
| Final-wire intent | `relay/playground_web_search.go:15–76`, `relay/compatible_handler.go`: checks the actual outgoing body after conversion/plugins/overrides, recognizes existing provider search forms, rejects search intent silently removed before dispatch |
| Citations | `lib/streaming/stream-utils.ts:84–94`, `lib/message/message-streaming-utils.ts:84–107,236`, `components/message/playground-message-content.tsx:101–115`: stream/non-stream URL citations, HTTP(S)-only validation and existing Sources UI |

**What this does not promise:** wire-intent preservation is not proof every configured model supports or executes search. This is provider-hosted search, not an independent gateway crawler. Model capability must not be guessed from a model-name substring. No hardcoded extra charge, local quota subtraction or synthetic search result is needed; preserve existing relay/tool accounting.

**Slice P2:** treat as already implemented unless a concrete unsupported/error/citation regression is supplied. Improve existing discoverability/error copy only where a demonstrated gap exists. Retain current opt-in and charge warning. Do not connect this toggle to `/v1/alpha/search`, `/v1/tools/search` or a new external service merely because those independent relay endpoints also exist.

## 3. Files/images: existing capability map and request shapes

### Current lifecycle and exact disconnect

1. `lib/input/input-tool-utils.ts:39–60` declares Upload file / Upload photo / Take screenshot / Take photo, but **every action returns `Feature in development`**. `playground-input-tools.tsx:73–78` only shows that toast.
2. Shared `web/src/components/ai-elements/prompt-input.tsx` already provides:
   - `usePromptInputAttachments`, picker API, remove/clear, preview chips (`:263–394`) and add-attachment menu (`:396–421`);
   - `PromptInputMessage {text?, files?: FileUIPart[]}` (`:423–427`);
   - local file/count bounds (`:432–443,498–558`), hidden input (`:773–782`), local form drop and textarea paste (`:619–685,813–878`);
   - blob URL creation, conversion to data URLs before submit (`:689–742`) and cleanup on remove/clear/unmount;
   - failed submit preserves attachments **only when the callback throws/rejects**; a synchronous early return counts as success and clears them (`:743–767`). Conversion rejection before that `.then` is not handled there.
3. `components/input/playground-input.tsx:43,98–110` accepts only `onSubmit(text)`, extracts submittable text and ignores `message.files`. `input-control-utils.ts:50–70` requires nonblank text, so attachment-only sends cannot submit.
4. `hooks/use-playground-conversation.ts:47–56` appends a text user message; `MessageVersion.content` is `string` (`types.ts:28–31`); `formatMessageForAPI` returns that string (`message-utils.ts:155–163`). Existing `buildMessageContent(text,imageUrls)` (`:112–137`) already emits image parts, **but has no caller**.
5. Storage schema only accepts string message content (`storage-schema.ts:69–72`), limits a saved conversation to 100 messages / `1024*1024` serialized string-length units, and truncates loaded text at 40,000 per message / 120,000 aggregate (`:28–33`, `storage.ts`). These are not a binary attachment store or strict UTF-8 byte accounting. Blindly embedding Base64 into version text will hit quota/truncation and corrupt re-send semantics.

### Reuse existing wire contracts, not a new uploader

Current client endpoint remains **`POST /pg/chat/completions`**, JSON with existing model/group/stream/config. Preserve system messages and existing message order; supply user content parts for actual attachments:

```json
{"role":"user","content":[{"type":"text","text":"Describe this image"},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]}
```

The host Chat DTO also parses a document part:

```json
{"role":"user","content":[{"type":"text","text":"Summarize this file"},{"type":"file","file":{"filename":"notes.pdf","file_data":"data:application/pdf;base64,..."}}]}
```

- `relaykit/dto/openai_request.go:514–520,640–644,800–867` already represents images/files; the parser reads `filename` and inline `file_data` or upstream `file_id`.
- `relaykit/relayconvert/internal/oai_chat/to_oai_responses_req.go:233–265` already maps images to `input_image` and files to `input_file` on Responses upstream conversion. Other providers have their own support/conversion limits: do not advertise all file types on every model.
- **Compatibility detail to test, not hide:** `MediaContent.GetFile`'s raw-map path reads `file_name` (`openai_request.go:564`) whereas parsed JSON and `MessageFile` use `filename`. Validate real parse-to-upstream paths; do not ship a private alternate request spelling based on that helper alone.
- `router/relay-router.go:171–174` registers `/v1/files` list/create/delete/content to `RelayNotImplemented`. Provider-specific staged uploads or task multipart are not a general console upload service.
- Native task multipart already has opaque request-scoped file refs and `form.RemoveAll` cleanup in `relay/channel/task/jsplugin/adaptor.go:373–377,473–478`; those contracts are for task/native image/video submissions, not a reason to route ordinary chat through a task plugin.

### Slice P3 — bounded inline attachments on existing chat (no durable server objects)

- First support **images and an explicit bounded document allowlist**, not screenshot/camera capture in the same change. Reuse shared picker, remove/preview and current payload helper; unsupported capture actions must remain honestly unavailable, not silently claim success.
- Carry structured attachments with the owning user message through submit → message construction → API formatting → edit/regenerate → history display. Keep text copy/edit behavior separate from attachment bytes; never stringify a parts array into ordinary prose.
- Define concrete initial admission bounds before implementation: proposed **at most 4 files, 2 MiB per file, 4 MiB total decoded attachments per send**, including history attachments actually resent. These are proposed product limits, not existing configured settings. Account for Base64 expansion and total encoded request size before conversion/network; retain existing server body limit as a separate final boundary. Server-side per-attachment/count/total checks are required if claiming these as security/resource limits; picker checks alone are UX.
- Use **bounded tab-lifetime bytes** in the first slice to avoid new server storage: proposed **30-minute idle expiry and 16 MiB aggregate decoded attachment bytes per tab**, with expired/unreferenced bytes released automatically and a visible capacity rejection rather than silent live-media eviction. These are proposed defaults requiring acceptance, not existing behavior. Persist attachment descriptors only, never raw Base64 in existing string history. On reload/expiry show an explicit attachment-expired/reselect state and block replay/edit-and-submit/regeneration that would silently omit required historical media. Text history remains readable. If durable attachment history is required, that is a separate accepted contract and bounded browser Blob-store slice, not an accidental localStorage rewrite.
- Ensure picker/drop/paste all share bounds and current disabled state. Current shared `matchesAccept` is permissive beyond `image/*`, and provider-context `.add` bypasses local validation; a new `accept` prop alone is insufficient. The form-drop effect resolves `anchorRef.current.closest('form')`, but the rendered anchor precedes the form as its sibling (`prompt-input.tsx:480–486,772–799`); test actual drop delivery rather than assuming the existing handler is attached. Current URL revocation effect depends on `files`, so exercise adding a second file while the first remains; preserve still-live previews and revoke only removed/finished resources.
- Wait for an accepted send result rather than clearing on a swallowed early return; retain draft/attachments on local rejection, conversion error and network failure. Avoid automatically replaying a billable request after an uncertain response.
- No upstream keys in browser, no new `/v1` token minted/exposed to make uploads work, no public file route, no document bytes/filenames/data URLs in ordinary process/audit logs. Preserve current server group/model/user authorization, admin-only channel pin and relay/billing paths.
- **Open billing issue:** native document pre-estimation can be approximate (`service/token_counter.go:101–171`, file default 4096), while image estimation uses actual image metadata. Upstream usage remains authoritative. Do not replace this with frontend text-length arithmetic or assert exact PDF pricing; exercise enabled paths with existing pricing fixtures before shipping.

## 4. Storage/TTL/capacity: separate four different things

| Storage class | Current source-backed lifecycle | Gap / non-duplication decision |
|---|---|---|
| Browser chat/project history | `lib/storage/storage.ts`, user-suffixed localStorage, per-conversation limits and explicit storage failure UI; no TTL/global conversation/project count bound | Does not consume server disk. Do not claim deleting it frees the server or automatically delete intentional chat history under the label cache |
| Request body / loaded media spool | `common/body_storage.go`, `common/disk_cache.go`, `common/disk_cache_config.go`; optional temp directory `new-api-body-cache`, body/file modes, request-close cleanup through `middleware/body_cleanup.go` | Reuse this existing spool; do not create another upload temp directory/cleanup daemon. Cap/liveness need hardening below |
| Request diagnostics/log DB | `setting/operation_setting/log_setting.go`, `model/log_request_detail.go`, `service/system_task.go`, existing maintenance log settings | Already separate capture/max bytes, absent content retention 3 days, explicit 0 preserved, beneficial gzip encoding, protected bounded cleanup. Do not add another body archive or duplicate retention controls |
| Generated task artifact bytes | `service/task_artifact_store.go`, `setting/system_setting/task_artifact_store.go`, `controller/task.go:369–387`, `controller/video_proxy.go` | Store is disabled; S3 mode explicitly falls back to upstream. Current content routes proxy upstream, not a persistent local media cache. Keep that default |

### Spool behavior proven from source

- Defaults: disabled, threshold **10 MiB**, nominal maximum **1024 MiB** (`common/disk_cache_config.go:21–27`, `setting/performance_setting/config.go`). Existing System Settings exposes enabled/threshold/max-size/path and statistics (`web/.../maintenance/performance-section.tsx`). Do not add a second set of identical controls.
- Global body read defaults to **128 MiB**, `MAX_REQUEST_BODY_MB`; downloaded file defaults **64 MiB**, `MAX_FILE_DOWNLOAD_MB` (`common/init.go:199–202`, `common/gin.go:72–82`, `service/file_service.go:156–185`). These limits are not total server storage budgets.
- Request cleanup closes BodyStorage and FileSources (`middleware/body_cleanup.go:11–20`); disk `Close` removes files and decrements accounting (`body_storage.go:245–255`). Multipart parsers have their own `RemoveAll` owners.
- Startup calls `CleanupOldCacheFiles` (`main.go:384`), which removes files older than **5 minutes** (`body_storage.go:442–445`). Admin Clear removes files older than **10 minutes** (`controller/performance.go:142–151`). The located call sites show **no periodic spool TTL scheduler**.
- `IsDiskCacheAvailable` checks atomic current usage plus requested size against max; writing and `IncrementDiskFiles` happen later. **This is not an atomic reservation**, so concurrent writers can pass together. Known-content-length streaming checks availability based on claimed length while actual reading permits up to the body cap. When unavailable, paths may fall back to memory. Thus current nominal max is not a hard disk-and-memory bound.
- Cleanup uses **mtime only**, with no live-owner registry; a long active request is not proven safe merely by being older than 10 minutes. Do not schedule the current age-only cleaner more frequently and call it safe TTL.
- Startup/stat repair is incomplete as a hard-budget claim: `SyncDiskCacheStats` exists, but age cleanup does not itself establish an authoritative occupied/reserved count for all surviving files; crashes/path changes/shared directories need a contract.

### Slice P4 — reuse spool, make capacity and expiry real

A separate backend slice, not bundled into the panel fix:

1. Keep the existing settings and directory. Add one **finite orphan TTL** setting only if required; proposed initial **60 minutes**, periodic **5-minute** sweep, bounded candidates per pass. These are proposed defaults, not changes already made.
2. Use one atomic reserve/release owner for **body and FileSource spool bytes**, including in-progress writes, rollback/short write/error/Close and actual growth. Reconcile surviving owned files on startup before accepting writes. Active readers/replays hold leases/pins; cleanup expires only unowned files, never live tasks/requests on age alone.
3. Enforce configured capacity **before** writing and during actual stream growth. At saturation reject safely or take an explicitly bounded memory path; an unbounded memory fallback is not a capacity guarantee. Include file-count/open-descriptor/in-flight conversion bounds, free-space reserve and stats for rejected reservations.
4. Initial proposed operator budget for a 20 GB host: keep this **temporary spool at or below 512 MiB** until measured headroom supports more; do not automatically apply that value to production. SQL data, WAL, OS, logs and concurrent processes share the same volume and remain outside that cap. Same-filesystem multiple gateway processes require shared quota/isolated per-process directories plus a summed allocation; per-process atomics alone are insufficient.
5. Preserve request replay/cleanup contracts and never delete accounting/task rows as cache eviction. Keep artifact byte persistence disabled. No fixed physical-disk saving or compression ratio is claimed.

**Important existing task constraint:** parent R24 pauses usage-log cleanup pending independent durable-statistics work. Existing retention capability is not permission to enable/resume it, change production settings, delete history or compact PostgreSQL. Request-detail compression/retention follows existing active-writer/task protections and is not a general response/attachment cache. Deleting PostgreSQL rows does not necessarily return space to the OS; compaction remains a separately authorized operation.

### Artifact URL expiry is not object/cache expiry

`service/task_artifact_access.go:28–60,98–146` issues a stable task+artifact HMAC capability, with no expiry field; URL building ignores untrusted request Host. Existing future S3 presign TTL (900 seconds) is **not implemented artifact-object retention**. Do not describe either as automatic byte deletion. Existing content/auth guards and no-store headers must remain; changing long-lived URL policy would need its own compatibility/security decision.

`controller/video_proxy.go:294–303` streams normal binary content with `io.Copy`, retains Range/validators and uses safe redirects/SSRF/credential handling (`:496–529`) plus no-store/sandbox/nosniff (`:640–643`). A JSON/base64 content-path response has its own bounded in-memory branch. Reuse these handlers; no generic `fetch-any-URL` attachment/image proxy.

## 5. What creates Drawing Logs / Task Logs / common usage logs

| User/API action | Source path / record consequence |
|---|---|
| Ordinary Playground chat, with or without search or input images | `/pg/chat/completions` → existing chat relay. Common consume/error logs as configured; no automatic `model.Midjourney` or durable `model.Task` merely because text says draw or includes a picture |
| Native ordinary `/v1/images/generations` or `/v1/images/edits` model not handled by a task plugin | Host protocol fallback to ordinary image relay; common billing/usage logs, **not automatically Drawing Logs** |
| Midjourney-specific submission/actions | `/mj/submit/...`, `/mj/insight-face/swap` and `/:mode/mj` routes (`router/relay-router.go:225–245`) → `controller.RelayMidjourney` → `relay/mjproxy_handler.go`; persisted `model.Midjourney` rows drive Drawing Logs |
| Midjourney read-only polling/image | Existing fetch/list/image paths query or update their existing MJ task. Reading an image is not a new draw submission. UPLOAD itself can produce a successful MJ row; the tab name does not imply every row generated an image |
| Task/plugin native or host-protocol submission | Native plugin routes / host Responses, video or image endpoints **when dispatched to a task driver** → accepted durable `model.Task`; generic task history drives Task Logs, including synchronous-immediate task plugin results |
| Same model on ordinary and plugin channels | Initial selected channel determines ordinary relay vs task driver. Model name/endpoint alone does not prove which history gets a row; preserve the existing host dispatch contract |
| System maintenance task | Separate `model.SystemTask` / system-task panel, not inference Task Logs |

Precise insertion distinctions:

- `relay/mjproxy_handler.go:581–654` builds/persists MJ rows after a decoded upstream response, including failure-description rows, existing/queued codes 21/22 and upload completion; charging depends on the existing outcome/billing conditions. Early transport/policy/auth failure need not have reached insertion. Therefore **Drawing Logs is neither an all-attempt audit nor only successful charged drawings**. Face-swap has its own insertion around `:246–277`.
- `controller/relay.go:1239–1300,1409–1417` constructs accepted generic task state and inserts through `InsertTaskWithRequestContent` / `InsertTaskWithSubmissionCharge`. Do not convert failed pre-admission/chat traffic into fake accepted tasks just to populate this tab.
- Native `retainResult:false` on an **immediate terminal** result sets `ResultDiscarded` and omits `data` (`controller/relay.go:1262–1288`). The task/accounting row remains; result retrieval is suppressed. An asynchronous result on such a route is retained, with a warning. Immediate host `openai_image` completion similarly discards stored result data; polled completion has its existing retained snapshot. This is not a global task TTL.
- Responses/video task results are retained for existing retrieval/artifact contracts. Task data can contain compact upstream snapshots or inline media; upstream proxying avoids a local binary store but does not establish a zero-growth database guarantee.

UI reads already exist (`web/src/features/usage-logs/api.ts`):

- Common: `/api/log/` for administrator / `/api/log/self` for self (`:48–86`).
- Drawing: `/api/mj/` / `/api/mj/self` (`:192–199`).
- Task: `/api/task/` / `/api/task/self` (`:202–209`).
- Dashboard artifact metadata: `/api/task/:task_id/artifacts` (`:216–221`), not private task snapshot bytes in the table list.
- API-token artifact metadata/content: `/v1/tasks/:key/artifacts` and `/v1/tasks/:key/artifacts/:artifact_key/content`, with existing owner/capability checks; console artifact resolution must keep current authorization and no-store behavior.

**Slice P5 — explanation only unless explicitly expanding Playground modes:** add concise contextual help using existing log surfaces/keys: Drawing = Midjourney-specific history; Task = accepted async/task-plugin history; ordinary chat and image APIs use common usage logs unless dispatched as tasks. A new image-generation/video mode in Playground would be a separate protocol-specific compose/auth/billing slice, not an attachment button side effect.

## 6. Meaningful existing regressions and remaining gates (none executed)

Extend a suitable existing suite first; do not scatter one behavior into duplicate files. All execution remains **hosted CI only**, owned by root/integration workers.

| Slice | Existing suite / meaningful protection |
|---|---|
| P1 project fix | `web/src/features/playground/components/conversations/__tests__/conversation-list.test.tsx`: existing create/name/move/delete assertions; add visible empty project, no-chat/all-archived, cancel/keyboard, no lost chats. `playground/__tests__/index.test.tsx`: integrate actual panel placement/responsive entry and switching/stop behavior. `lib/storage/__tests__/conversations.test.ts`: projects restore across reload; `hooks/__tests__/use-playground-state.test.ts`: preserve pending unsaved histories and identity boundaries |
| P2 do not duplicate search | `components/input/__tests__/parameter-panel.test.tsx`: explicit toggle/disabled/aria state; `lib/streaming/__tests__/payload-builder.test.ts`: on/off payload; `lib/storage/__tests__/storage.test.ts`: default false/persistence. `relay/chat_completions_via_responses_test.go`: `TestPlaygroundWebSearchPreservesFinalUpstreamIntent`, `TestPlaygroundWebSearchAcceptsProviderSearchRepresentations`; existing message-streaming tests for safe citations |
| P3 attachments | Extend payload-builder, existing conversation/message streaming/editor and storage suites for actual structured message roundtrip/retry/history/expired media. Existing input suite can test real picker/drop/paste path and rejected-send preservation; if a dedicated shared composer regression is necessary, add one focused module test rather than multiple upload widgets. `common/body_storage_test.go`: `TestMultipartBodyStorageReplaysFilesAndFields`, body replacement/replay and whitelisted diagnostics. `relaykit/.../oai_chat/to_oai_responses_req_test.go` and provider-conversion suites: actual file/image serialization. `router/provider_plugin_relay_test.go`: `TestPluginConverterMultipartRequests`, `TestPluginConverterSignsTheFinalBytes` only if those native paths change |
| Authorization | `controller/playground_test.go`: `TestPlaygroundChannelNavigationAuthorizationAndRouting`; current router access-token scope tests. Keep nonadmin model/group access and no credential exposure; a UI mock is not server auth proof |
| P4 spool | Extend `common/body_storage_test.go` with deterministic concurrent reservation/growth rejection/release-on-error/active replay protection and restart/orphan bounded cleanup fixtures. Do not use sleeps, throughput claims, fake stress loops or production probes. Existing performance settings tests should preserve current save semantics; strict-cap work may justify at most one focused cache test file if body-storage suite does not fit |
| Existing diagnostics | Required matrix cases are already listed in `.trellis/spec/backend/logging-guidelines.md`; real SQLite/MySQL/PostgreSQL plus separate ClickHouse, cross-store late writes/active tasks/codec bounds. `service/system_task_test.go:TestSystemTaskLogRetention` is scheduler evidence only, not the real-store matrix. No restart of paused usage cleanup |
| P5 task/result/log behavior | `controller/relay_task_plugin_test.go`: `TestExecuteTaskSubmissionHonorsRouteRetainResult`, `TestPresentTaskSubmissionUsesNativePresenterAfterPersistence`; `controller/task_generic_test.go`: `TestTaskListsOmitPersistedSnapshot`, `TestTaskArtifactAuthorizationKeepsForeignTasksHidden`, `TestTaskArtifactAccessRequiresActiveOwner`, `TestDisabledArtifactStorePreservesPluginUpstreamContent`, `TestProxyTaskMediaForwardsRangeAndFiltersResponseHeaders`, `TestProxyTaskMediaReadsArtifactFromJSONContentPath`; existing `controller/task_log_view_test.go`; `service/task_artifact_store_test.go:TestDisabledTaskArtifactStoreHasNoStorageBehavior`; `service/task_artifact_access_test.go:TestTaskArtifactAccessBindsTaskAndKey`. Existing `web/src/features/usage-logs/__tests__/artifacts.test.ts:343–360` and `components/__tests__/task-artifacts-cell.test.tsx:61–77` distinguish discarded/retained/unfinished results and lazy viewer loading |

### Explicit unknowns / acceptance blockers

- Whether the reported New project failure is Save-with-no-visible-project versus trigger-not-opening has not been reproduced. The empty-project projection defect is source-proven; do not claim every event/layout symptom fixed from research alone.
- Selected deployed provider/model file/search support and operator prices are not known. No live paid search/image probes were sent. Document formats, inline size limits, unsupported conversion behavior and exact usage reporting need final integrated hosted assertions.
- Attachment persistence expectation is a product decision. Session-only with honest expired/reselect states is the smallest disk-safe slice; durable restore requires an explicit bounded store/lifecycle contract. Do not silently destroy or omit media to fit legacy text limits.
- Actual free space, filesystem sharing, process count, current cache settings, DB retention settings and task payload growth on the 20 GB server were not queried. Proposed budgets are not deployment instructions or safety guarantees.
- Periodic expiry cannot safely be bolted onto age-only cleanup without active ownership. URL token lifetime, task result retention, request diagnostics retention, browser history and temporary-file TTL are distinct.
- No new backend upload/auth service, plugin contract, accounting scheme or schema migration is proposed for P1/P2. P3 server limits and P4 spool leases/admission require separate ownership/security review and hosted checks. Any DB change requires the repository's real-dialect/fresh+upgrade gates; the execution ban is not a waiver.

## Handoff order

**P1 first** (project projection + action order, frontend-only); **P2 recognize/reuse, not rebuild**; **P3 bounded inline chat attachments** with explicit persistence/replay UX; **P4 isolated spool hard-cap/orphan TTL** before promising server capacity; **P5 clarify existing log triggers**. Keep each independently reviewable, preserve other workers' active ownership, and do not migrate, pull, build or modify the target tree as part of this handoff.
