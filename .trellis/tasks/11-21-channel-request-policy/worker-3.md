# Pi 3 handoff — discovery snapshot / balance ownership transition

Status: **Former pi3 work frozen; native reviewer integration now saved for cloud validation.** The historical pi3 WIP notes below are retained for provenance. Root transferred discovery production/controller ownership to `/root/policy_scope_audit`; its final integration notes at the end supersede the blockers below. No local tests, build, lint, typecheck, probes or git writes were run.

## Files and changes saved by me

- `controller/channel.go` — before ownership was transferred, added unsaved ordinary `FetchModels` preview support for `header_override` (JSON-object validation, explicit empty preserved) and `proxy` on the in-memory channel. Also inserted the discovery-admission call in `FetchModels`. These edits do not yet pass an explicit preview-options object through post-admission reload, so saved-channel form overrides can still be dropped. Pi4 owns any continuation/correction.
- `controller/channel_upstream_update.go` — saved in-progress changes for the shared management transport/admission path, `acquireDiscoverySnapshot`, OpenAI model-list parsing, per-enabled-key union, guarded automatic model-update persistence, and bounded source reload support. This file is not compile-verified and must be repaired by the new owner before use.
- No test files were changed by me. No edits were made to Gemini adaptor files, Codex service files, Wham usage files, or balance files.

## Preview/API handoff to pi4/root

Recommended narrow API at the controller discovery boundary:

```go
type channelModelPreviewOverrides struct {
    Type           int
    TypeProvided   bool
    BaseURL        *string
    HeaderOverride *string
    Proxy          *string
    Key            string
}

func fetchChannelUpstreamModelIDsForRequest(
    ctx context.Context,
    saved *model.Channel,
    preview *channelModelPreviewOverrides,
) ([]string, error)
```

Keep the saved channel ID for capacity admission. After acquiring and reloading the current persisted row, overlay only the form's explicitly supplied values: a nil base/header/proxy preserves current saved state; a non-nil empty string clears it; a non-empty typed key overrides the discovery credential without requiring that draft key to be persisted. The intentional draft type override is separate from concurrent saved type drift. Advanced Custom edit preview continues to use its current enabled saved key and request-supplied route configuration, not the draft key. Do not set ID to zero to evade capacity accounting.

## Concrete outstanding defects / blockers

1. **Do not treat current upstream file as complete or compiling.** The plugin discovery helper signature is now `(ctx, channel, plugin, admission, keyIndex)`, but the ordinary plugin dispatch call and the multi-key call are not consistently updated to that signature/order. Correct every call site; multi-key must preserve the original index across `FreshPluginCredentialLine` refresh and in `RelayInfo.ChannelMultiKeyIndex` (do not recompute by searching a changed credential and silently fall back to index 0).
2. The file's guarded-source code uses `reflect.DeepEqual` and `json.RawMessage`; its imports must include `reflect` and `encoding/json` if those references remain. Source-format the file after repair.
3. `parseAuthoritativeOpenAIModelIDs` must distinguish an actual empty inventory (`{"data":[]}`; valid) from malformed entries (`[{}]`, `[null]`, blank/missing IDs, or a valid entry mixed with malformed entries). Reject malformed items and any non-null error envelope / explicit `success:false`; otherwise automatic detection could infer channel-wide removals from bad data. Keep missing/null `data` invalid.
4. `checkAndPersistChannelUpstreamModelUpdates` now has a compare-and-update direction, but persistence calls currently pass `context.Background()`. Apply a bounded operation context to the short guarded transaction. `collectPendingUpstreamModelChanges` also creates its own Background-based deadline, so scheduled-task cancellation does not propagate into channel discovery. Thread the scheduled/request context into discovery; do not do network work inside the transaction.
5. `applyChannelUpstreamModelUpdates` in this file changes `channel.Models` before calling persistence. That makes the guarded source compare against the mutated model list and causes real apply operations to reject their own source snapshot. Compute a separate proposed model string; persist against the unmodified source snapshot, then update returned state.
6. Ensure detection uses one current saved source snapshot for local model list, mapping, ignored/settings values, and every enabled credential's result. Do not publish suggestions/removals if any enabled-key inventory failed or if key order/config/status/scopes changed. Current fail-closed union is safer than partial removals but needs a regression and explicit completeness contract.
7. The copied `MultiKeyStatusList` / scopes / users from the initial channel into a post-admission fresh row was removed in my source patch; keep using the freshly reloaded DB maps. Still preserve original per-key index for each independently fetched credential.
8. The previously edited `FetchModels` saved-channel branch needs the preview-options API above. Without it, reloading after admission rejects intentional draft type/base/header/proxy/key choices or silently loses them. This portion is now pi4-owned.
9. `sendFetchModelsRequest` reload should use the bounded `reloadManagementChannel(request.Context(), id)`, not an unbounded `model.GetChannelById` call. This was identified but not safely landed before transfer.

## Existing focused cloud-only regressions to extend

- `controller/channel_upstream_update_test.go`: `TestParseOpenAIModelIDsStrictResponseContract`, `TestFetchAdvancedCustomModelsAppliesHeaderOverrideAfterRouteAuth`, `TestFetchAdvancedCustomModelsUsesEnabledSavedMultiKey`, `TestFailedAdvancedCustomDetectionDoesNotStageFullRemoval`, and `TestFetchModelsUsesTypedKeyForSavedChannel`.
- `controller/channel_multi_key_test.go`: `TestMultiKeyModelDiscoveryUsesOneKey` and `TestMultiKeyModelSyncPreviewAndApply`. Extend the latter for authoritative enabled-key union, partial failure preserving models/scopes, key refresh retaining the original key index, and a settings/model edit during discovery rejecting stale publication.
- No new test file is needed. Tests remain authored for cloud only; none were added by me.

## Verification and ownership

No local test/build/lint/typecheck was run. Root/native reviewer owns final integration, cloud validation, staging and commits. Pi4 owns `controller/channel.go`; native management reviewer owns the Gemini adaptor; pi4 owns Codex model services; balance is owned by its isolated reviewer.

## Native reviewer integration handoff

Production ownership transferred by root: `controller/channel_upstream_update.go`, `controller/channel.go`, their existing regression files, and the frozen Ollama adaptor files. Codex service remains separately owned by pi4/root; `controller/channel_multi_key_test.go` is exclusively owned by `/root/final_scope_review` for the final focused regressions.

- Explicit `channelModelDiscoveryOptions` distinguishes saved POST previews and original per-key indices. Saved previews retain their channel ID for shared capacity, reload current saved cap/TLS/transport after each grant, and then overlay only explicitly submitted draft fields. Nil preserves current saved state; empty base/header/proxy clears; empty key keeps the current saved key; Advanced Custom ignores draft credentials. Intentional preview type is bound to the original saved type through `SourceType`.
- Codex dispatch composes `service.CodexModelDiscoveryPreview{SourceType, Key, BaseURL, HeaderOverride, Proxy}` before generic reload. Only a nonempty typed key gets a Key pointer. Root/pi4 owns the matching service semantics and authored service regressions.
- Per-key callers retain the full channel and pass `KeyIndex`; plugin preflight refresh and subsequent 401 refresh use that exact original index, never a search for a freshly rotated key. Explicit per-key recovery keeps its existing disabled-key semantics; ordinary authoritative discovery unions all enabled keys and rejects a failed key rather than returning a partial inventory.
- Ordinary/Advanced-Custom authoritative OpenAI inventories accept an explicit valid empty data array and reject missing/null data, malformed/null/blank-ID entries, mixed malformed entries and error envelopes. Plugin model-list parsing keeps its pre-existing nonempty-result contract while applying the same malformed-entry checks. Ollama likewise rejects malformed/blank-name records instead of interpreting them as removals.
- Model updates preserve the original source until the guarded short transaction succeeds. Scheduled source reads now include `param_override`; saved row reloads use caller contexts. Manual detection and scheduled task contexts propagate through discovery, admission, HTTP and bounded persistence; cancellation does not publish timestamps or suggestions.
- Explicit `sync_key_models` Apply keeps its existing add-only partial-success behavior, but now compares the original source under the shared lock and writes only models/channel_info. It reloads the stored current row before rebuilding abilities and publishing sync/cache. It must not be conflated with all-or-error authoritative channel-wide discovery.

Authored, not run, in the existing upstream regression file: `TestChannelManagementModelDiscoveryPersistence`, `TestChannelManagementModelDiscoveryCancellation`, and `TestChannelManagementModelPreviewUsesCurrentPolicyAndSavedCapacity`; extended `TestParseOpenAIModelIDsStrictResponseContract` and the malformed Advanced Custom no-removal regression. The existing internal browser cancellation regression now includes POST preview. The separate final-scope reviewer owns union, refresh-index and stale explicit-sync regressions in the existing multi-key file.

Source/gofmt only: used `D:/AppData/Local/Temp/new-api-build-tools/go/bin/gofmt.exe` on owned changed Go files and read back all changed call signatures. The service preview descriptor matches the controller construction. No build/lint/typecheck/test/race/database result is claimed; root must run exact-commit cloud checks, including real SQLite/MySQL/PostgreSQL and the ChannelManagement race selectors.

Final source-review corrections: explicit sync's guarded callback now preserves the locked row's `ChannelInfo` and replaces only the computed `MultiKeyModels`, retaining concurrent mode, polling and disable metadata. Explicit pending-update Apply reloads the persisted row through the same bounded context before rebuilding abilities and refreshing the caller's channel snapshot, preserving current group/priority/weight/tag. The existing persistence regression table now includes a routing-metadata edit case. Both source-guard errors reuse `channel configuration changed during model discovery`, which already maps to the shared connection-settings message in the frontend and all seven locale files. These corrections received gofmt/readback only and still require root's exact-commit cloud validation.
