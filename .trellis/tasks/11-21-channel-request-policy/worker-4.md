# pi4 handoff — management HTTP + ratio/inference/Ollama

Status: `service/channel_management_request.go` implements the published API; ratio/inference/Ollama integration is in progress. Scope is the management HTTP boundary only: no pricing transforms, generation request handling, UI, schema, or changes to pi3's controllers.

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
- One call = one attempt. Retry loops must clone the prepared request (or build
  a fresh one) and reuse the same `Admission` owner so the wait deadline does
  not reset; never nest calls while already holding a lease.

Do not edit the helper from other panes; request changes through root.

## Ollama signatures owned by pi4 (pi3 wires `controller/channel.go`)

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
- Specialized URLs (`/api/tags`, `/api/pull`, `/api/delete`, `/api/version`),
  streaming progress lines and the success-line requirement are unchanged.
- `relay/channel/ollama` imports `service` and `relaykit/dto` only; `service`
  never imports `relay/channel`.

### Call-site snippets pi3 must apply in `controller/channel.go`

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
version, err := ollama.FetchOllamaVersion(c.Request.Context(), baseURL, key, ollama.OllamaRequestOptions{
    Header:    headers,
    Settings:  channel.GetSetting(),
    Admission: admission,
    ChannelID: channel.Id,
})
```

Streaming pull passes the same options before `progressCallback`. Pull/delete
use the same options. Discovery in `controller/channel_upstream_update.go`
line ~395 calls `ollama.FetchOllamaModels(ctx, baseURL, key,
ollama.OllamaRequestOptions{Header: headers, Settings: channel.GetSetting(),
Admission: admission, ChannelID: channel.Id})` with the freshly loaded channel.

Options are intentionally not variadic: a forgotten call site must fail to
compile rather than silently bypass transport/admission policy.
