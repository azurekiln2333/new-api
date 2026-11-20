# Gemini discovery handoff

## Frozen contract

```go
func FetchGeminiModels(ctx context.Context, baseURL, apiKey string, setting dto.ChannelSettings, headers http.Header) ([]string, error)
```

The controller owns one outer per-key discovery lease across every page and passes
`lease.Context()`. It must validate the selected key and current saved policy after
admission, and expand static channel headers before calling this function. Gemini
does not acquire another lease. It passes nil admission to
`service.DoChannelManagementRequest` and preserves `/v1beta/models`.

The function derives one overall 30-second deadline from the caller context. Every
page receives the same context/deadline, current supplied proxy/TLS settings, the
default `x-goog-api-key` followed by the supplied static headers, and shared Host
mapping. Header inputs are not mutated. All returned bodies close before a next
page or return, including HTTP failure and partial-body reads.

Page tokens are query escaped. Repeated tokens and the 100-page limit return an
error with nil results. HTTP errors, malformed JSON, error envelopes, missing or
null inventories, and invalid model names also return nil results; no partial
inventory can produce channel-wide deletion suggestions. An explicit empty
`models: []` is a valid complete page. Successful names retain the former
`models/` prefix removal behavior.

## Changed files

- `relay/channel/gemini/relay-gemini.go`: imports and `FetchGeminiModels` only;
  ordinary generation, usage and billing are untouched.
- `relay/channel/gemini/relay_gemini_models_test.go`: one new consolidated discovery
  suite; existing Gemini tests cover usage or protocol conversion instead.
- This task handoff.

## Validation

Source-reviewed the implementation, shared management sender and callers. Formatted
the two Go files with the approved `gofmt.exe`. No local test, build, lint, typecheck,
browser check, install, upstream probe, stage, commit or push was run.

Authored `TestFetchGeminiModels*` regressions for escaped pagination, shared deadline,
caller-context propagation, static key overrides, Host, per-page body cleanup,
invalid later inventories without partial results, repeated tokens/page limit,
cancellation/failed reads, actual local HTTP proxy routing and channel TLS policy.
These tests are intentionally unrun pending root/cloud validation.

## Integration follow-up

Controller ownership remains with pi 3. Update all callers to pass the outer lease
context and already expanded static headers using the exact signature above.
Run the Gemini test selector/full backend suite in cloud and retain controller
multi-key union/current-policy tests. No database behavior or module independence
contract was changed in this handoff.
