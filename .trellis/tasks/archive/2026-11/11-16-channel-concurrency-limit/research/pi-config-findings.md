# Pi config/UI research (reviewed)

The integrated design supersedes this initial recommendation in two places: use first-body-byte timeout instead of header-only timeout, and remove the permanent upstream 504/524 exclusion. No product changes were made by Pi.

Read-only config/UI research complete. No files changed.

# Findings: channel concurrency cap + global/per-channel pre-response timeout (config & UI)

## 1. Backend global-option conventions (where the global timeout belongs)

The Request Policies store is the established home for routing/retry knobs:

- Snapshot + defaults + validation: `model/request_policy.go`
  - `requestPolicyDefaultOptions()` (line ~41) seeds defaults; flat keys (`RetryTimes`, `AutomaticRetryStatusCodes`, …) plus two struct-backed prefixes `channel_affinity_setting.` and `monitor_setting.`.
  - `IsRequestPolicyOption(key)` (line ~71) is the allowlist gate.
  - `BuildRequestPolicy` (line ~95) validates every key; `UpdateRequestPolicyOptions` (line ~214) validates the whole result, writes a DB transaction, then `updateOptionMap(key,value)` per key, then stores the snapshot.
- Runtime var sync: `model/option.go` `updateOptionMap` switch (~line 690, e.g. `case "RetryTimes": common.RelayTimeout...`).
- API: `controller/request_policy.go` — `GET/PATCH /api/option/request_policy`, mounted under `middleware.RootAuth()` (`router/api-router.go:220-224`). **Root only**, not admin. Access-token scope is `option:read`/`option:write` (`middleware/access_token_routes.go:154-155`).

The "global upstream pre-response timeout" already exists as a transport knob and is the correct primitive:

- `common.RelayResponseHeaderTimeout` (`common/constants.go:181-189`), env `RELAY_RESPONSE_HEADER_TIMEOUT` default `1800` (`common/init.go:118`). Applied in `service/http_client.go:116-123` as `transport.ResponseHeaderTimeout`.
- Its doc explicitly states it only bounds the wait for response headers and **does not truncate a stream after headers arrive** — this satisfies the "never truncate an already productive stream" requirement without touching the 120-minute whole-stream context at `relay/helper/common.go:105`. Do not reuse `common.RelayTimeout` (`http.Client.Timeout`) for this; it bounds the whole read.
- Per-channel resolution hook: `service/http_transport_policy.go` (`HTTPTransportPolicy`, `NormalizeHTTPTransportPolicy`, `cacheKeyPart`). Cache key must include any per-channel timeout or clients collide (`service/http_client.go:143-145`, `getOrCreateDirectClient`).

## 2. Channel-settings config path

- DTO: `relaykit/dto/channel_settings.go` — `ChannelSettings` struct; validation lives in `ValidateHTTPTransport()`. Pointers + `omitempty` are required for optional scalars (AGENTS). `ChannelOtherSettings` is the alternative JSON blob.
- Model validation hook: `model/channel.go` `ValidateSettings()` (~line 1169) unmarshals `ChannelSettings` then calls `ValidateHTTPTransport()`.
- Cache-rebuild safety: occupancy must not live on cached channel objects (`model/channel_cache.go:149`).

## 3. Recommended config keys / types / defaults / validation

### Global pre-response timeout — `UpstreamResponseHeaderTimeoutSeconds`
- Location: flat Request Policies option (same family as `RetryTimes`), **not** a `request_policy_setting.` prefix — a test asserts that prefix is not a policy option (`model/request_policy_test.go:87`).
- Type: nonnegative integer seconds. Default: seeded from the effective `common.RelayResponseHeaderTimeout` (env, `1800`), mirroring how `RetryTimes` seeds from `common.RetryTimes`.
- Validation in `BuildRequestPolicy`: integer, `0 <= v <= maxTimeoutSeconds` (`service/http_client.go:66`); reject negative/non-numeric. Sync in `updateOptionMap` (`case "UpstreamResponseHeaderTimeoutSeconds": common.RelayResponseHeaderTimeout = n`).
- **0 means disabled/unbounded** (wait indefinitely for headers), matching the existing `if seconds > 0` guard. Document it in the field description.

### Per-channel override — `ChannelSettings.UpstreamResponseHeaderTimeoutSeconds *int`
- JSON `"upstream_response_header_timeout_seconds,omitempty"`, unit seconds.
- **nil = inherit the global** (including a global of 0 = unbounded). **Explicit 0 = override to unbounded for this channel** even when the global is finite. Positive = custom seconds. Negative/over-cap = validation error.
- This nil-vs-0 distinction is exactly why the DTO must be `*int`; a plain `int` with `omitempty` drops the explicit 0 (AGENTS pointer rule) and cannot express inheritance.
- Resolve into `HTTPTransportPolicy` at build time; include the resolved seconds (and an "overridden" bit) in `cacheKeyPart()`.

### Channel concurrency cap — `ChannelSettings.MaxConcurrentRequests *int` (whole-channel total)
- JSON `"max_concurrent_requests,omitempty"`. nil = unlimited (preserve current behavior); positive = shared cap across all keys of the channel; **0 = unlimited** (documented, no footgun); negative = validation error.
- Shared total (not per-key) so adding keys cannot multiply a configured hard limit, per the approved decision.
- Occupancy lives outside cached channel objects and must survive cache rebuilds.

## 4. 504/524 retry exclusion

- Hard exclusion set: `setting/operation_setting/status_code_ranges.go` — `alwaysSkipRetryStatusCodes{504,524}` plus `AutomaticRetryStatusCodeRanges` already omitting them; enforced in `ShouldRetryByStatusCode` and in `DecideRelayRetry` (`service/relay_error.go:75`).
- Consequence: a local pre-response timeout that surfaces as 504/524 is permanently non-retryable, so it cannot fail over. The config/UI-visible contract is the frontend copy in `web/src/features/system-settings/request-policies/retry-section.tsx:~105` ("2xx, 504 and 524 are always excluded.") — a locale key that must be updated in all 7 locale files if semantics change.
- Config implication: keep 504/524 excluded for *upstream-returned* statuses, but emit a distinct retryable error code for the local pre-response timeout (transport agent's area). The skip set itself is backend code, not a UI option — there is nothing to expose in Request Policies for it.

## 5. Permission & frontend serialization paths

- Global timeout UI is **root-only**: system-settings route guard (`web/src/routes/_authenticated/system-settings/route.tsx` requires `ROLE.SUPER_ADMIN`) and `RelatedPolicyLink` (`related-policy-link.tsx`).
- Channel `setting`/`settings` are sensitive: `web/src/features/channels/hooks/use-channel-mutate-form.ts` `SENSITIVE_UPDATE_FIELDS` strips `setting`/`settings` when the user lacks `CHANNEL:SENSITIVE_WRITE`; the drawer wraps these controls in `<fieldset disabled={sensitiveLocked}>` (`channel-mutate-drawer.tsx`, `sensitiveLocked = isEditing && !canEditSensitive`). A new settings-backed field must render inside that fieldset and be visibly disabled/locked, never silently discarded.
- Channel serialization is explicit, so a form-only control loses data:
  - `web/src/features/channels/types.ts` — `ChannelSettings` interface.
  - `web/src/features/channels/lib/channel-form.ts` — schema (~253), defaults (~460), load (~510), `buildSettingJSON` (~674), payload assembly (~917/~964). `buildSettingJSON` must write the override only when defined (`if (v !== undefined) ...`) so `undefined`=inherit and `0`=explicit survive as distinct.
  - `web/src/features/channels/lib/channel-configuration.ts` — add to `CONFIGURATION_BLOCKS.extraSettings.fields` and the `extraSettings` "configured" check.
  - UI insertion: `extraSettings` region of `channel-mutate-drawer.tsx` (`proxyFields`, `http_protocol`, `http2_connection_shards`, `disable_task_polling_sleep`), inside the sensitive fieldset. A mode selector (inherit / custom) plus number input makes nil-vs-0 unambiguous.

## 6. Frontend Request Policies ownership

`web/src/features/system-settings/request-policies/`: `defaults.ts` (`RequestPolicySettings`, `defaultRequestPolicySettings`), `routing-form.ts` (`createRoutingPolicySchema`, `routingPolicyFormValues`, `routingPolicyOptions`), the section UI (`retry-section.tsx` or a new section registered in `section-registry.tsx`), `api.ts`/`use-save-policy.ts` (no change needed — the endpoint already carries all policy options). All new copy must be added to the 7 locale files.

## 7. Minimum file ownership (parallelizable)

Freeze this contract first, then two disjoint tracks can run in parallel:

Contract to freeze: global key `UpstreamResponseHeaderTimeoutSeconds` (int seconds, 0=unbounded, default=env); channel `upstream_response_header_timeout_seconds` (`*int`, nil=inherit, 0=unbounded); channel `max_concurrent_requests` (`*int`, nil/0=unlimited, shared per channel); resolver signature, e.g. `service.ResolveResponseHeaderTimeout(globalSeconds int, settings dto.ChannelSettings) int`.

Config/UI track (mine):
1. `model/request_policy.go` — defaults, `IsRequestPolicyOption`, `BuildRequestPolicy` validation, optional snapshot field.
2. `model/option.go` — `updateOptionMap` sync to `common.RelayResponseHeaderTimeout`.
3. `relaykit/dto/channel_settings.go` — two pointer fields + validation.
4. `model/channel.go` — call the new validation from `ValidateSettings` (only if split out).
5. `web/src/features/system-settings/request-policies/{defaults.ts,routing-form.ts,<section>.tsx,section-registry.tsx}`.
6. `web/src/features/channels/{types.ts,lib/channel-form.ts,lib/channel-configuration.ts,components/drawers/channel-mutate-drawer.tsx}`.
7. 7 locale files under `web/src/i18n/locales/`.

Transport track (other agent): `service/http_transport_policy.go`, `service/http_client.go`, `relay/` error construction, `setting/operation_setting/status_code_ranges.go`, `service/relay_error.go`.

Shared/sequential seams: item 3 (DTO field names) and item 2 (runtime sync point) must be settled before both tracks compile against each other; everything else is disjoint.