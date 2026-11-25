# Request error monitoring and safe upstream errors — source inventory

## Boundary and status

- Active parent: `.trellis/tasks/11-24-console-responsiveness-diagnostics`. This is source-only R29/R34 research, and this is the sole file assigned for writing.
- No production implementation, tests, UI, schema, settings, Git/index, database, browser, or local executable validation were performed.
- This work is operational failure diagnosis, **not** R24 billable usage, ranking, quota, channel/model usage, or log-statistics design. Keep it out of R24's `usage_events`; see accepted `.trellis/tasks/11-26-durable-usage-statistics/research/backend-statistics-inventory.md` for that boundary. Existing retained usage-log records remain the authoritative per-request detail. A monitor must not archive request/response bodies or create an alternate billing-statistics store.
- No production ownership is claimed. Root assigns implementation and coordinates files; do not overlap pJ's common-log sources or pM's mobile list.

## R29 — capture source, current constraints, minimal contract

### Capture points

| Failure | Current path | Meaning |
|---|---|---|
| Selected HTTP channel attempt fails | `service.ProcessChannelError` → `service.RecordRelayErrorLog` → `model.RecordErrorLog` → `model.createRequestLog` / `WithLogRequestDetail` | Called per failed channel attempt. Retry-then-success may yield type-5 error rows and a later consume row under one request ID. These are attempt failures, not necessarily final request failures. |
| Before channel selection / ingress or validation | `controller.Relay` errors and `middleware.RequestLogDiagnostics` | Authenticated relay fallback records an eligible final 4xx/5xx not already logged, usually channel 0. Parsed model may be preserved; rejected unread bodies remain unread. Middleware is used in relay `TokenAuth` and the playground route. |
| Responses WebSocket create/event | `relay/responses_websocket.go` calls `RecordRelayErrorLog` for ingress/final errors | Distinguish each create/request ID; do not group merely by shared socket/session. |
| Async task submit/report | `controller.RelayTask` reports `taskReportError` through `RecordRelayErrorLog` | Submission/report failure; do not silently conflate with later polling/task-row failures. |

`service.RecordRelayErrorLog` gates on `constant.ErrorLogEnabled` (default false) and `types.IsRecordErrorLog`; `ErrOptionWithNoRecordErrorLog` is an explicit exclusion. The function marks `request_error_log_written` before persistence, while `model.RecordErrorLog` only logs a failed write. There is no durable retry/outbox for this optional log write. Any independent monitor must define capture policy for the legacy disabled switch, preserve explicit no-record exclusions unless root authorizes otherwise, and not synthesize events from arbitrary HTTP statuses.

### Existing storage and boundedness

- Error rows are `model.Log` type 5 in `LOG_DB.logs`; fields include time, user, model, channel, request/upstream request IDs, `content`, and `other`. Relational stores have time/type and request-ID indexes; no observed error-only query/index. ClickHouse MergeTree is ordered by `(created_at, request_id)`, declares `id DEFAULT 0`, and may have native `LOG_SQL_CLICKHOUSE_TTL_DAYS`. UI/display IDs are not stable ClickHouse identities.
- Usage-log cleanup uses `service/system_task.go` and `model.CountOldLog` / `DeleteOldLogBatch`; ClickHouse TTL is independent. Usage-log retention defaults to 0 (keep forever); request-content retention is separate. The user-provided disk context is 20 GB, but no row-size, rate, index-size, or growth measurements were made here.
- Repeatedly grouping raw `logs.content` is an unbounded scan as history grows and leaks the very untrusted text this feature should avoid. A separate finite operational summary is needed; do not mirror raw logs or request bodies. It is not part of R24 and its expiry must not be treated as R24 coverage.

### Minimal monitor contract

- Admin-only, uncached paged view of grouped errors: safe category/fingerprint, count, first/last seen, channel/model dimensions, status, and a **bounded** sample of newest occurrence references. Expanding a group shows only safe category/status/type/code, time, channel/model, request ID and retry/attempt context.
- A group may link to an ordinary type-5 row only through a server-validated retained row reference. When that row has expired, keep summary-only. Monitor data never authorizes original request-content lookup; `/api/log/request/:ref` keeps its independent exact surviving-link check.
- Safe grouping identity is a versioned canonical tuple of protocol + classified category + status + allowlisted error type/code + fingerprint of already-redacted text (only if safe text is approved). Request ID is an occurrence correlation, **not** the grouping key. Channel/model are dimensions, not part of the common fingerprint, so the same failure can be correlated across channels/models. Never hash raw provider text and assume a digest makes it safe.
- Persist no prompt/body, raw upstream body, raw provider prose, credentials/key fragments, IP, URL, contact handle/group, username/token, or arbitrary `other`. Unknown provider text maps to a fixed safe category. Limit sample/dimension cardinality and mark truncation/overflow visibly.
- Store compact occurrence evidence or otherwise prove duplicate callback replay cannot increment the same group twice. A request ID alone is insufficient because one request can produce several failed attempts and task/stream transitions. Keep correlation to a retained row optional and exact; no synthetic display ID.
- Enforce finite age and hard capacity in the primary store. Exact age/row/group/dimension caps are a root/product decision requiring representative row/index sizing and error-rate estimates; current evidence cannot prove any numeric cap safe for a 20-GB volume. At a hard cap, use bounded overflow accounting and explicitly mark results incomplete rather than silently dropping distinct errors or claiming exact counts. No cleanup policy change is made here.

### Candidate implementation and reuse (root assigns)

1. Root model capture/aggregation at the existing `service.RecordRelayErrorLog` eligibility decision, with server occurrence identity allocated before write; keep it separate from R24 usage facts. Primary DB is a candidate because `LOG_DB` may be remote/ClickHouse. Do not alter log/body/link write semantics. Root must decide capture-vs-`ERROR_LOG_ENABLED` and excluded-error policy first.
2. Add uncached paged admin read route under `/api/log` using server `AdminAuth`; add `log:read` PAT scope only if product intends scoped PAT access. Enforce limits/filters on the server; return safe DTO only.
3. Prefer a dedicated `/usage-logs/errors` page to changes in current common-log components. Reuse `@/components/data-table` `DataTablePage`/`useDataTable`, audit viewer/columns/filter patterns (`web/src/features/usage-logs/audit/components/{audit-log-viewer,audit-log-columns,audit-log-filter-bar}.tsx`), and `components/logs-filter-toolbar.tsx` / `compact-date-time-range-picker.tsx`. Use existing `Dialog`, `StatusBadge`, and detail layout for safe expandable context. Sidebar ownership points are `web/src/hooks/use-sidebar-data.ts` and `use-sidebar-config.ts`; existing audit page role/permission handling is a reference, not a reason to reuse audit semantics. Route and all seven locale changes need explicit ownership.
4. Regression ownership can extend the existing `controller/relay_error_log_test.go` for occurrence capture, no-record exclusion, retained-row linkage, storage/overflow and response-safe persistence; existing router access-scope tests for admin/PAT denial; one existing viewer test convention for grouped row/detail. Avoid new parallel implementations of pagination, dialogs, or mobile lists. Any primary schema requires hosted real SQLite/MySQL/PostgreSQL fresh + released-upgrade verification; no such validation is claimed.

## R34 — safe provider errors to clients and stored error detail

### Source-supported flow

- HTTP non-2xx provider responses share `service.RelayErrorHandler` (`service/error.go`), called from text, Claude, Gemini, audio/image/Responses, embeddings, rerank, alpha-search, DeepSeek and compatibility handlers. It parses `GeneralErrorResponse` and may preserve provider-supplied message/type/code in `NewAPIError`. Inspected production call sites pass `showBodyWhenFail=false`; keep the flag safe if future callers change.
- `relaykit/types/error.go` exposes `NewAPIError.MaskSensitiveError[WithStatusCode]`, `ToOpenAIError` and `ToClaudeError`; masking delegates to `relaykit/relayconvert/kitutil.MaskSensitiveInfo`. That masks IPs, API-key patterns, URL paths/query values, userinfo and selected upstream hosts, but does not remove arbitrary promotional/contact/purchase prose embedded in a plain provider message.
- Shared client sinks include `controller/relay.go` final JSON and `writeRelayStreamError` (Chat/OpenAI, Responses `response.failed`, Claude SSE); `controller/responses_websocket.go` and `relay/responses_websocket.go` WebSocket envelopes; and request-plugin reply/report/socket paths. These already converge on `ToOpenAIError` / `ToClaudeError` in many paths, so a final-handler-only filter is incomplete. Some adapters also construct errors directly; do not blanket-classify every `OpenAIError` as upstream without carrying provenance.
- `service.RecordRelayErrorLog` currently calls `err.MaskSensitiveErrorWithStatusCode(...)`, and `model.RecordErrorLog` persists that in `Log.Content`. `FormatAdminLogs`/`FormatRootLogs` sanitize `other`, not `content`. The usage-log UI falls back to `log.content` in `common-logs-columns.tsx` and `details-dialog.tsx`, so wire-only filtering still leaves the unsafe copy in stored error detail. `RecordRelayErrorLog` puts `error_type`, `error_code`, `status_code` into public `other`; raw provider code/type must not be exposed as arbitrary metadata.

### Minimal safe behavior/source boundary

- Mark upstream-origin errors at the actual shared construction boundary (`service.RelayErrorHandler` for non-2xx; any direct parsed upstream response seam added only when required). Preserve provenance through wrappers. Do not infer provenance from status alone or strip local quota/validation messages.
- Add one safe client projection at the shared typed error boundary used by both `ToOpenAIError` and `ToClaudeError`. For upstream-origin errors, output a fixed safe category message and server request ID; include unchanged HTTP status and only allowlisted error type/code. Drop provider text, arbitrary type/code/metadata, URLs, contact/group identifiers and credential fragments. Keep original error cause/status/code/provenance intact internally so `DecideRelayRetry`, `ResetStatusCode`, `Retry-After`, channel health, usage settlement, response commit and explicit no-record policy are unchanged.
- Use the same safe projection for stored type-5 `Log.Content`; move potentially provider-supplied `error_type`/`error_code` under a role-protected scope or replace them with allowlisted category values. Preserve safe channel/model/time/request correlation and retry evidence. Do not store raw error bodies in the monitor or log `other` as a workaround.
- Keep protocol envelopes and framing unchanged. Central projection must cover normal JSON, Chat/Claude SSE, Responses failure events, WebSocket and plugin reply/report. The request-ID suffix remains, but is added to the safe message, not raw provider content.
- User report fixture: upstream HTTP 403 “free quota” promotional text with purchase URL, contact group and key-like fragment. None of the exact promo, URL/contact, or key fragment may appear in client response or stored error row; safe category + request ID remain; status is 403 and existing retry/no-record/usage behavior is unchanged. No live upstream probe.

### Minimal R34 source/test slice for root to assign

- **Production:** `relaykit/types/error.go` for provenance-aware client projection, plus `service/error.go` at common upstream parsing/construction, and `service/relay_error.go` / `model/log.go` only for safe error-log content and metadata. Keep relaykit independent of root packages; do not fan advertiser-specific filters into provider adapters or modify unrelated logs/UI.
- **Tests:** extend existing `service/error_test.go` for shared upstream parse/provenance + status preservation; extend existing `controller/relay_error_log_test.go` for safe stored message, explicit no-record preservation, and role-safe detail. Add one focused `relaykit/types/error_test.go` only if unit projection coverage cannot be expressed at those existing observable paths (no existing test file under `relaykit/types` was found). Existing `controller/relay_stream_test.go` has `TestRelayStreamErrorAfterKeepAlive`; extend it only if the central DTO test does not cover SSE envelope/error projection. Prefer one cross-path ownership handoff rather than separate tests at every protocol adapter.
- **Regression matrix:** upstream 403 advertiser payload through `RelayErrorHandler`, OpenAI + Claude DTOs, existing stream-error writer, and error-log persistence; local quota/validation message remains unchanged; HTTP status, retry decision and error exclusion still derive from original internal error. These are proposed hosted tests, not execution results.

## Explicit handoff / exclusions

- R29 is an operational, finite error-monitor concept; R24 remains the separate numeric business-statistics project. Do not duplicate its schema, backfill, dashboard or cleanup design.
- No safe capture policy, numeric capacity, retention setting, schema, route, or implementation path is approved until root assigns it. No implementation ownership is taken.
- Common-log `Contains` / `Exact` toggle and R30 are outside this refreshed R29/R34 deliverable; no toggle/CSS exploration or proposal is included here.
- Source inventory complete and frozen. No product files, tests, schemas, endpoints, UI, settings, retention, Git state, or production data were changed.
