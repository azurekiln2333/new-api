# pi4 handoff — management HTTP + ratio/inference/Ollama

Status: `controller/channel.go` ownership transferred to pi4. Ollama controller integration and browser-discovery cancellation are implemented and frozen for root review, with regressions in existing `controller/channel_test_internal_test.go`. Preserved the earlier FetchModels preview transport/header edits. Ratio/inference and Codex service files remain frozen; pi4 did not modify the native reviewer's Codex service files.

## Shared management HTTP helper (service package, available for pi3)

File: `service/channel_management_request.go`

```go
// ChannelManagementCall is one trusted console/background HTTP exchange with a
// single upstream channel. It owns only transport selection, admission and
// response-body lifetime; URL, method, static headers and request bodies are
// prepared by the caller.
type ChannelManagementCall struct {
    Request   *http.Request
    Settings  dto.ChannelSettings
    ChannelID int
    Admission *ChannelDiagnosticAdmission
    Timeout   time.Duration
    NoRedirect bool
    Reload    func() (*http.Request, dto.ChannelSettings, error)
}

func DoChannelManagementRequest(call ChannelManagementCall) (*http.Response, error)
```

Contract:

- Uses `service.GetHttpClientWithProxySettings(call.Settings.Proxy, call.Settings)`
  (cached transport, channel TLS/proxy/HTTP policy). Never the relay
  generation client; no first-byte/attempt timers are applied here.
- Never parses or injects channel headers. Callers build static headers with the
  existing controller helpers (`buildFetchModelsHeaders` /
  `applyFetchModelsHeaderOverrides`) or explicit Ollama headers. Browser
  headers are never copied.
- Requires non-nil `Request`; the request context is the caller/request
  lifetime. `Timeout > 0` adds a bounded total deadline that covers send and
  body read; `0` keeps only the caller deadline.
- When `Admission != nil`, calls `Admission.Acquire` before sending; the optional HTTP deadline also bounds this wait. Limited channels use the shared queue; unlimited channels retain best-effort telemetry, including during Redis failures. `ChannelID == 0` is uncounted. `nil` is for source/manual URLs or callers already holding the only lease. The helper does not report or retry upstream busy on its own.
- With admission, `Reload` runs after each grant and must validate the same saved channel and return a freshly built request/settings (including key/header revalidation). On a changed effective cap the helper releases the grant and reacquires with the same owner, at most three grants. A replacement request with a body needs a new body. Callbacks should use a bounded caller context for database work. Do not acquire inside the callback.
- The lease is held until `resp.Body.Close()` after upstream cleanup. A complete 2xx body read can confirm an available upstream probe; closing an unread error body cannot. On a send/error path the helper releases the lease and timeout. Always close returned bodies, including non-2xx.
- `NoRedirect` returns 3xx without following. With a channel or custom headers, the helper never forwards those credentials across origins; same-origin redirects still use the shared client's redirect validation. The cached client is not mutated.
- Static `Header["Host"]` is mapped centrally to outbound `Request.Host` on a cloned request, for preview and reloaded requests alike. Caller request/header maps stay untouched.
- A bounded caller/management context owns total duration: a scoped client copy clears the cached relay client's shorter `Timeout`, preserving the cached transport and its connection/header timeouts. This permits 30m/1h Ollama operations without mutating shared clients.
- One call = one attempt. Retry loops must clone the prepared request (or build
  a fresh one) and reuse the same `Admission` owner so the wait deadline does
  not reset; never nest calls while already holding a lease.

Do not edit the helper from other panes; request changes through root.

## Shared helper — frozen in `c2b8026c0`

Root committed `service/channel_management_request.go` and the regressions in
`service/http_client_transport_test.go`; cloud run `44801500137` was assigned by
root. Tests exercise cap reduction and reacquisition, fresh URL/key/Host, response
body ownership, cancellation, send deadline, scoped total timeout, and redirect
handling. This worker did not run them locally or change the frozen files.

## Ratio sync — frozen for independent review/commit

- `controller/ratio_sync.go`: stored channel origin/settings and current enabled key/static overrides are applied after admission. OpenRouter retains `/v1/models` and its default Bearer authentication; general pricing keeps its historical no-default-auth behavior and applies explicit static overrides. Cross-origin custom absolute URLs remain independent sources without saved channel credentials or connection settings. The batch deadline covers admission, sends, GET transport retries/backoff and body reads (default 10s, capped at 60s); the batch fan-out bound remains independent of shared per-channel capacity. Read limits, cancellation and body cleanup precede pricing conversion; no price formulas changed.
- `controller/ratio_sync_test.go`: `TestPricingSyncChannelManagementUsesSavedOriginProxyAndEnabledKey` observes the saved target, proxy, enabled credential, static placeholder, no browser passthrough, public absolute endpoint and capacity cleanup. `TestPricingSyncCompleteSourcesAndArrayFormats` still exercises manual sources and both response formats (using unsaved source IDs). Authored only; no local validation.

## Inference status — frozen for independent review/commit

- `controller/channel_inference.go`: all four fixed vLLM/SGLang requests use separate body-lifetime slots under one 10s panel deadline; each saved-channel grant reloads the same channel's type, target, settings and enabled key. Partial-failure results, output filtering, static Host and no-redirect behavior are preserved.
- `controller/channel_inference_test.go`: added `TestChannelManagementInferenceUsesFreshPolicyAndReleasesCapacity` (persisted key/Host and TLS policy supersede stale snapshot, all four requests counted, no remaining slot). The existing `TestVLLMStatusPartialFailureAndCredentials` now checks static Host on the unsaved path. Authored only; no local validation.

## Ollama adaptor — ready for pi3 controller integration

Owned files: `relay/channel/ollama/relay-ollama.go`, `relay/channel/ollama/dto.go`, and existing `relay/channel/ollama/stream_test.go`. The five signatures below are unchanged from handoff. `TestOllamaPullUsesExplicitStreamModeAndRequiresSuccess` covers explicit `false`/`true`, selected static authorization, progress, HTTP-200 error envelopes and missing success. `TestOllamaModelDiscoveryRequiresAnInventory` covers valid empty/nonempty inventory and rejects missing/null/malformed/error inventories without exposing the upstream error. Both were authored, not run. Commit the adaptor alongside pi3's updated callers; older controller signatures will not compile against the new adaptor.

All five take the caller context so request cancellation stops the upstream
call, and prepared static headers/settings/admission from the controller:

```go
type OllamaRequestOptions struct {
    Header    http.Header            // prepared; nil -> legacy Authorization from apiKey
    Settings  dto.ChannelSettings
    Admission *service.ChannelDiagnosticAdmission
    ChannelID int
    Reload    func() (baseURL string, headers http.Header, settings dto.ChannelSettings, err error)
}

func FetchOllamaModels(ctx context.Context, baseURL, apiKey string, options OllamaRequestOptions) ([]OllamaModel, error)
func PullOllamaModel(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions) error
func PullOllamaModelStream(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions, progressCallback func(OllamaPullResponse)) error
func DeleteOllamaModel(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions) error
func FetchOllamaVersion(ctx context.Context, baseURL, apiKey string, options OllamaRequestOptions) (string, error)
```

Semantics:

- When `options.Header` is non-nil it is the final static header set (already
  contains Authorization/overrides); the helper does not add a second
  Authorization header. When nil, the legacy `Authorization: Bearer <apiKey>`
  rule is kept.
- `options.Admission`/`ChannelID` apply fixed-channel admission for the whole
  call, released when the upstream body is closed (streaming pull keeps the
  lease for the stream lifetime).
- Timeouts (bounded, no generation first-byte timers): models 30s, version
  10s, delete 60s, non-stream pull 30m, streaming pull 1h.
- Specialized URLs (`/api/tags`, `/api/pull`, `/api/delete`, `/api/version`) and streaming progress are preserved. Both pull modes send explicit `stream`, require a success response, and reject HTTP-200 error envelopes without echoing error text. `/api/tags` must include a non-null `models` array; `[]` is valid, but an error or missing inventory is not evidence of removal.
- The Ollama adaptor uses the host `service` and `relaykit/dto` APIs; `service` never imports `relay/channel`.

### Ollama controller integration — implemented/frozen by pi4

Root transferred all `controller/channel.go` ownership from pi3 to pi4. The four handlers now pass caller-derived bounded contexts and one `ChannelDiagnosticAdmission` owner to the frozen nonvariadic Ollama APIs. `ollamaManagementOptions` reloads the saved ID, checks the current type, selects an enabled key, rebuilds static headers and supplies current settings after admission; the shared HTTP helper releases/regrants if the cap changed. It never forwards browser headers. Manual recovery of a disabled channel remains allowed. Streaming pull removes wildcard ACAO, uses `common.Marshal`, propagates caller cancellation and cancels upstream work on a response-write error. Adaptor-owned body cleanup retains the lease through the stream.

All browser model fetch callers in `channel.go` now use `fetchChannelUpstreamModelIDsForRequest(c.Request.Context(), ...)`, including saved fetch, preview, explicit-key and key-sync requests. Sync semaphore acquisition observes cancellation; canceled sync returns before applying results. The previous preview proxy/header code is preserved. The newly touched preview JSON error now uses existing structured `Header override must be valid JSON format`, with no decoder text or new locale key.

Authored only, in existing `controller/channel_test_internal_test.go`: `TestOllamaChannelManagementHandlers` covers all four endpoints, enabled-key selection, proxy/static Host/headers, no browser-header leak, recovery channel, SSE/no wildcard CORS, and capacity cleanup; `TestOllamaChannelManagementReloadsPolicyAndPropagatesCancellation` covers changed target/key/header/proxy/cap, single active lease and upstream-body closure on cancellation; `TestChannelManagementBrowserDiscoveryHonorsCancellation` covers saved fetch/per-key fetch/key sync with no dispatch or partial apply after cancellation. Source review and gofmt only; cloud must run these tests. No new test file.

The earlier integration snippets below remain API guidance; they are no longer pending pi3 controller edits.

Per Ollama handler, one admission owner per HTTP request:

```go
admission := service.NewChannelDiagnosticAdmission(ctx, 0) // ctx = request context
defer admission.Close()
```

Build headers once via the existing helper (same rule as discovery):

```go
key, _, keyErr := channel.GetNextEnabledKey()
if keyErr != nil { /* report existing admin error */ }
headers, err := buildFetchModelsHeaders(channel, key)
if err != nil { /* report existing admin error */ }
```

`buildFetchModelsHeaders` is package-local to `controller`. Use an enabled key rather than `strings.Split(channel.Key, "\n")[0]`. For persisted channels where Ollama owns admission, wire `OllamaRequestOptions.Reload`: reload the same ID with a bounded context, revalidate the Ollama type and eligible key, and return the current base URL, prepared static headers and settings. The callback may run again if the cap changed. It is additive; the five published function signatures are unchanged. After an outer discovery lease with its own post-grant revalidation, pass its context and leave `Admission` nil to avoid double acquisition.

Example for version:

```go
version, err := ollama.FetchOllamaVersion(ctx, baseURL, key, ollama.OllamaRequestOptions{
    Header:    headers,
    Settings:  channel.GetSetting(),
    Admission: admission,
    ChannelID: channel.Id,
    Reload:    reloadOllama, // closure that returns current base, headers and settings
})
```

Streaming pull passes the same options before `progressCallback`. Pull/delete
use the same options. Discovery in `controller/channel_upstream_update.go`,
when already admitted and revalidated, calls:

```go
models, err := ollama.FetchOllamaModels(leaseCtx, baseURL, key, ollama.OllamaRequestOptions{
    Header:   headers,
    Settings: channel.GetSetting(),
})
```

Options are intentionally not variadic: a forgotten call site must fail to
compile rather than silently bypass transport/admission policy.

## Discovery read-only audit handoff — pi3 work in progress

No edits to `controller/channel_upstream_update.go` by pi4. Its tree changed during audit; these are targeted integration checks, not final gate results:

- Outer `fetchChannelUpstreamModelIDsWithAdmission` now calls `acquireDiscoverySnapshot`, passes `lease.Context()` to snapshot dispatch and passes **nil** admission inward. Generic HTTP, plugin model-list sender and inner Ollama calls therefore do not acquire nested leases in that chain. Bodies are closed by the existing consumers before the outer lease release. Keep this nil-admission boundary.
- **Remaining index/provenance check for pi3:** `newSingleKeyChannel` clears multi-key metadata and leaves only one key. Per-key browser fetch/key-sync pass this copy; plugin discovery's `PluginModelListKey` and `pluginKeyIndex` then see index 0, and `credentialIndex(info)` returns 0 when `ChannelIsMultiKey=false`. For a credential originally at index >0 that refreshes, the original index must be carried separately or resolved from the fresh complete channel before invoking credential refresh. A freshly rotated line may not match the original copy at all. `locatePluginCredentialLine` can match a compatible account at the supplied index before searching for the exact line elsewhere, so accountless credentials make losing the original index unsafe. Pi3's channel-wide union is being updated to pass `keyIndex` explicitly; recheck that both its function signature and the explicit-key paths preserve it after integration.
- **Saved preview check for pi3/root:** caller preview edits still exist in `FetchModels`, but current saved-row admission reload can replace the in-memory preview with the persisted row. The pre-existing saved typed-key/explicit-clear tests in `channel_upstream_update_test.go` must remain authoritative (`TestFetchModelsUsesTypedKeyForSavedChannel`, `TestFetchModelsAdvancedCustomEditPreviewUsesSavedKeyAndExplicitClears`). Do not silently substitute a persisted key for the operator's typed recovery/preview key.
- Advanced Custom route auth is not interchangeable with generic Bearer auth. In the current outer-snapshot path the inner sender is nil-admission, preserving prepared headers; avoid a future reloaded generic header rebuild that drops custom route auth/query configuration.

## Read-only console error audit (for root)

The exact new fixed strings below were absent from **all seven** frontend locales (`en`, `zh`, `zh-TW`, `fr`, `ja`, `ru`, `vi`) when audited. Paths/lines refer to the audited revisions: ratio `1b99bc8c3`, inference `5ad885e9c`, and balance working tree before integration.

| Exact source key | Backend call site | Visible console path | Missing locales |
| --- | --- | --- | --- |
| `invalid upstream address` | `controller/ratio_sync.go:218,230`; shared validation in `service/channel_management_request.go:101` | Pricing source failure in `upstream-ratio-sync.tsx:274,296`; also balance request failure | All seven |
| `channel origin changed during price sync` | `controller/ratio_sync.go:278` | Pricing source failure, same two toast paths | All seven |
| `failed to read the upstream response` | `controller/ratio_sync.go:408` | Pricing source failure, same two toast paths | All seven |
| `upstream response is too large` | `controller/ratio_sync.go:411` | Pricing source failure, same two toast paths | All seven |
| `upstream returned an unsuccessful pricing response` | `controller/ratio_sync.go:458` | Pricing source failure, same two toast paths | All seven |
| `channel concurrency settings changed during admission` | `controller/channel-billing.go:675`, `service/channel_management_request.go:89` | Manual balance error toast or a pricing source failure; plugin balance can also persist it as the last error | All seven |
| `channel is no longer enabled` | `controller/channel-billing.go:701`, `controller/channel_balance.go:161` | Background-only guard (`enabledOnly`); can be persisted and shown as the last balance error | All seven |
| `channel balance plugin changed during admission` | `controller/channel_balance.go:165` | Manual balance error toast when all keys fail; persisted balance error | All seven |
| `channel balance key changed during admission` | `controller/channel_balance.go:172` | Manual balance error toast when all keys fail; persisted key/balance error | All seven |
| `channel balance key is no longer enabled` | `controller/channel_balance.go:176` | Background-only key guard; can be shown in stored key/balance detail | All seven |

Visibility/wiring: `web/src/features/system-settings/models/upstream-ratio-sync.tsx:274,296` renders nested `test_results[].error` verbatim. Manual balance `ApiError` goes to `web/src/features/channels/components/dialogs/balance-query-dialog.tsx:131` / `channels-columns.tsx:529`; `getServerMessage` only translates `message_key`, not raw `message`. `channel-balance-details.tsx:70` displays stored `error` as raw text after the translated “Last error:” label. Locale entries alone therefore do not fix these paths. Use typed `common.Message` for top-level errors and explicit translation of fixed nested/stored text without translating or interpolating provider-authored details. Consider reusing existing `Response too large`, `Invalid response`, and `Upstream request failed` (all 7/7) instead of adding synonyms. `Invalid channel proxy`, `Invalid channel header override`, `No enabled channel key` and `Multi-key channels do not support balance queries` also already exist in 7/7.

Inference adds no console keys: the new reload failure becomes `request_failed` and the panel renders existing `Unavailable`. `Channel not found` is reused and absent in 7/7, but predates these revisions (inference/Ollama). `OpenRouter requires a valid channel with API key`, `no API key configured for this channel` and `failed to parse the upstream response` are pre-existing ratio messages, also absent in 7/7. Numeric `HTTP %d`, raw provider details and internal status identifiers are not new locale keys. Read the full i18n skill; audit only, with no locale edits or local validation.

## Codex model discovery — frozen for root review

Pi4 owns `service/codex_channel_models.go`, `service/codex_models.go`, and the consolidated `service/codex_models_test.go`. Pi3 wired the controller dispatch. Model-list requests use the caller context (20-second operation ceiling), current channel policy and static headers, and the shared body-lifetime admission. No pi4 edits to Codex usage, sign-in, session routes, the OAuth HTTP exchange, or refresh persistence.

```go
type CodexModelDiscoveryOptions struct {
    Admission *ChannelDiagnosticAdmission
    Override  func(channel *model.Channel, accessToken string, headers http.Header) error
}

func FetchCodexChannelModels(ctx context.Context, channel *model.Channel, options CodexModelDiscoveryOptions) ([]string, error)
```

In `controller/channel_upstream_update.go`, pass:

```go
return service.FetchCodexChannelModels(ctx, channel, service.CodexModelDiscoveryOptions{
    Admission: admission, // a waiter owner, not an already-held lease
    Override:  applyFetchModelsHeaderOverrides,
})
```

If dispatch already holds the channel lease, pass its context plus `Admission: nil`; do not acquire again. Prefer passing the waiter owner where possible so the service can release the model response/lease before a GitHub lookup or OAuth exchange. A persisted call reloads the same channel; id 0 keeps supplied preview settings and cannot refresh a saved credential. The callback sees the current channel and access token (not the OAuth JSON/refresh token) after the Codex defaults, on `/backend-api/codex/models` only, including a single retry after 401. GitHub version lookup and OAuth refresh never get the callback or per-channel upstream TLS overrides. Retain one bounded caller-derived deadline across admission, lookup, refresh and model attempts.

After a 401, close the response before calling management-check's existing `RefreshCodexChannelCredential` with `ExpectedKey: &used.Key`. An `ErrCodexCredentialSourceChanged` or a successful refresh leads to the same single retry from the persisted row, never the returned refresh result. Missing, retyped, or multi-key channels abort. `service/codex_credential_refresh.go` was not edited by pi4. GitHub's release cache keeps expiry/stale fallback and allows waiting callers to cancel; the lookup does not receive Codex credentials, channel header overrides, or the channel's TLS relaxations. Incomplete/error model inventories are not removal evidence.

Security review references: official OWASP Authentication and Session Management Cheat Sheets (credential confidentiality and transport), OAuth 2.0 Cheat Sheet (bearer-token isolation and refresh rotation), and TLS Cheat Sheet (certificate validation); ASVS 5.0.0 sections V10.1/V10.2/V12.3. The existing OAuth transport policy, including the deployment-wide relay TLS configuration, was not changed. This is not a claim of ASVS compliance.

Authored, not run: `TestCodexChannelManagementReloadsAndClosesBeforeRetry` covers fresh route/header/account policy, body-close before refresh, replacement before/during the exchange, and rejection after type/multi-key changes. `TestCodexChannelManagementRefreshRejectsNonExactSource` checks case/whitespace mismatch and zero exchange. `TestCodexChannelManagementRefreshPersistence` covers nil-ExpectedKey replacement, no writes or sync on cancellation/write failure, secret-free errors, and successful rotation plus sync without losing `other_info`. The same file covers cancellable GitHub lookup waiters and invalid inventory. OAuth and GitHub use an injected transport with synthetic responses, not external network requests.

## FINAL Codex preview handoff — ROOT FREEZE

`service/codex_channel_models.go` and `service/codex_models_test.go` are frozen and handed to the native independent auditor. Pi4 will make no further production/test edits. This follow-up does not claim ownership of prior frozen changes in `service/codex_credential_refresh.go` or `service/codex_models.go`.

Exact additive API, superseding the earlier proposal below:

```go
type CodexModelDiscoveryPreview struct {
    SourceType     int
    Key            *string
    BaseURL        *string
    HeaderOverride *string
    Proxy          *string
}
// Added field on existing CodexModelDiscoveryOptions:
Preview *CodexModelDiscoveryPreview
// Public function unchanged:
func FetchCodexChannelModels(ctx context.Context, channel *model.Channel, options CodexModelDiscoveryOptions) ([]string, error)
```

Every saved-row reload validates the current single-key row before applying explicit preview fields. Positive `SourceType` must match the original saved type; only the effective copy becomes Codex. Zero retains ordinary Codex-type checks. The saved ID/current concurrency cap/TLS/other settings remain authoritative; only explicit proxy replaces that setting. Nil fields inherit current values; explicit empty base/header/proxy retains clearing semantics. Operation-owned field copies survive reload and refresh. Explicit typed Key401 returns upstream status without refreshing or falling back to saved credentials; a noncredential overlay keeps guarded refresh-once and is reapplied. Existing caller deadline, account/auth defaults, single admission/body cleanup and GitHub/OAuth boundaries remain intact.

Authored in the consolidated existing test file: `TestCodexChannelManagementPreviewKeepsExplicitFieldsAndCurrentPolicy` covers omitted/cleared/typed fields, current cap/TLS/HTTP policy, typed401 without fallback, intentional source-type preview, concurrent type drift and multikey rejection with no preview persistence. The prior refresh regression gains a noncredential-preview-across-refresh case; prior-owner cases remain preserved. All HTTP responses in these cases use injected test transports.

Approved gofmt completed and production source was read back. No local tests/build/lint/typecheck/browser checks/probes or commits were run. Native auditor/root owns final issues, controller integration and exact-SHA cloud validation.

## Earlier read-only Codex saved-preview proposal — historical context

**Proposal only; no production or test edits.** The existing `CodexModelDiscoveryOptions` has only Admission/Override. Saved overlays are lost at `currentCodexModelChannel` (initial load, post-lookup outer-lease reload, post-grant reload, and post-refresh reload). An outer controller preview flag alone cannot fix those service reloads.

### Minimal additive service contract

Keep the public function signature and existing static-header callback. Add one optional presence-aware overlay; use pi3's shared preview descriptor instead if it already expresses these same four fields:

```go
type CodexModelDiscoveryPreview struct {
    Key            *string // complete typed OAuth JSON, not a bare Bearer token
    BaseURL        *string
    HeaderOverride *string
    Proxy          *string
}

type CodexModelDiscoveryOptions struct {
    Admission *ChannelDiagnosticAdmission
    Override  func(*model.Channel, string, http.Header) error
    Preview   *CodexModelDiscoveryPreview // nil preserves ordinary discovery
}
```

A boolean `Preview` plus the already-merged channel is insufficient: it cannot distinguish an omitted field from an explicit empty base/header/proxy and would freeze unrelated saved settings. Capture actual request-field presence before DB reload. `fetchModelsRequest.Key` currently has nonempty-only override semantics: populate `Preview.Key` only for the explicitly supplied nonblank OAuth JSON, unless root separately approves changing that behavior. Do not split Codex credential JSON on newlines.

### Exact changes needed in `service/codex_channel_models.go`

1. Extend `CodexModelDiscoveryOptions` as above. At entry, capture independent copies of the optional field values for this operation; do not mutate the caller's channel or pointers.
2. Thread Preview through **every** `currentCodexModelChannel` call: initial read, post-GitHub reload when admission is nil, `fetchCodexChannelModelAttempt`'s `call.Reload`, and reload after refresh/source-changed. Read and validate the persisted row first (same saved ID, current Codex type and single-key mode; preserve disabled-channel admin recovery), then apply only explicitly supplied fields to a value copy. Preserve the saved ID and all non-overlaid settings/metadata.
3. Build effective settings from the latest row's `GetSetting()` and replace only `Proxy` when explicitly supplied. Never use preview settings as the capacity authority. `MaxConcurrentRequests`, TLS/HTTP policy and all other unexposed settings remain from that current saved row. Returning these effective settings from `call.Reload` preserves the existing helper's release/regrant on a changed current cap. Do not set ID to zero to bypass DB reload/admission.
4. Build the specialized request from that effective channel. Existing `newCodexModelsRequest` already derives `Authorization` and `ChatGPT-Account-Id` from the same effective OAuth JSON and retains the Codex User-Agent/Accept/client-version query. Keep `options.Override(effectiveChannel, effectiveAccessToken, headers)` **after** those defaults; do not pass stored OAuth JSON or refresh tokens as `{api_key}`. Explicit empty HeaderOverride must really clear saved overrides. No browser-header copying.
5. If `Preview.Key != nil`, an HTTP401 must **not** invoke saved-channel credential refresh or fall back to the stored credential B: return the existing upstream-status error for the typed credential A. Otherwise source-changed handling would silently replace the operator's test account, and reapplying typed A after a saved-key refresh is incoherent. With no explicit credential overlay, preserve the current guarded refresh-once behavior (`ExpectedKey: &used.Key`), then reload/reapply the non-credential overlays before the one retry. No refresh persistence or OAuth transport edits are needed.
6. Keep the original caller-derived 20s deadline and one operation admission owner. For a caller-owned outer lease, pass nil Admission and its lease context; validate the latest saved cap against the outer granted cap as today. An inner `call.Reload` must not acquire another lease. Existing response-body close/release remains unchanged.

### `service/codex_models.go`: no production change needed

Its specialized URL/default headers/parser/body limit and GitHub release lookup can remain unchanged. Version lookup may use the effective preview proxy as normal transport selection, but must never receive preview base URL, OAuth headers, HeaderOverride or channel upstream TLS relaxations. OAuth refresh remains a separate verified/default-auth exchange using its existing stored-channel policy, not preview headers/base.

### Controller integration owned by root/pi3

Route saved Codex preview into the Codex service **before** an outer generic reload discards the descriptor. Pass the actual persisted target ID plus the explicit overlay descriptor and the operation's admission owner. Omitted fields use latest saved values after a wait; explicit empty fields retain their clearing semantics. Non-preview/scheduled callers leave Preview nil. A fully overlaid object with no presence metadata cannot be reconstructed safely by the service.

Cloud-only regression targets for the owner: saved account A versus typed account B; omitted field versus explicit empty header/proxy/base; cap change while queued; current type/multi-key rejection; 401 typed-token failure without OAuth exchange or DB write; stored-key refresh with non-credential preview overlays retained; one lease and cancellation/body cleanup. Existing previews and ordinary discovery must remain unchanged when Preview is nil.

## Validation pending

Cloud only: run `^TestCodexChannelManagement` in `./service` under the existing SQLite/MySQL/PostgreSQL fixture (`TEST_TASK_DB_DIALECT`) and the management race selector, alongside the rest of exact-commit CI. gofmt and source review only. No local tests, build, lint, typecheck, browser, probes, or git writes were run by pi4.
