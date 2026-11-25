# Relay/controller merge repair

Owner: repair_relay_merge. Owned files: controller/channel.go, controller/relay.go, relay/channel/api_request.go, i18n/keys.go and existing focused tests. Parent/sibling tool messages are arriving as ciphertext; put coordination in research/relay-dispatch.md or research/model-to-relay.md.

## Validation update

Focused test completed successfully before I read parent instruction to centralize compilers; no compiler process remains: GOMAXPROCS=1 GOMEMLIMIT=512MiB GOWORK=off go test -p 1 ./relay/channel -run 'TestProcessHeaderOverride|TestApplyUpstreamBodyMetadata|TestAPIAndFormRequestsInheritClientCancellation|TestFirstByteTimeout' -count=1. Result: ok github.com/QuantumNous/new-api/relay/channel 0.057s.

Extended scope per root coordination: service/provider_plugin.go now reindexes MultiKeyModels and MultiKeyUsers together with retained static keys while dropping credentials. Existing TestCopyChannelDropsCredentials places a credential before a user-bound scoped static key and verifies the copy keeps the binding and the source remains intact. Parent owns execution of this controller test and subsequent compiler runs.

Restored the reconstructed three-way merge and resolved each conflicting hunk while preserving both sides: provider signing and WebSocket preparation, request plugin headers and lifecycle, credential redaction/config/check/revoke, multi-key status locking, custom admission/first-byte timeout and key-scoping behavior, and all i18n constants. gofmt and scoped git diff --check pass.

## Model/service integration follow-up (must resolve before completion)

UpdateChannelLocked persists the new key before Channel.Update runs. The old custom remapKeyScopes reads the DB key, so this ordering loses the old key-to-scope mapping. Key scopes must be remapped against the old key inside UpdateChannelLocked (and credential removal/copy path), preserving the same index on credential rotation. Channel.Update must not restore stale scope maps after that write.

service.DropPluginCredentials currently reindexes status/reason/time but does not reindex MultiKeyModels/MultiKeyUsers. It must preserve bindings to remaining static keys after a copy drops credential lines. This file is outside my ownership; parent/model worker should coordinate the fix.

Regression tests already present: controller/channel_multi_key_test.go TestMultiKeyManageKeepsKeyScopesOnTheirKey, TestUpdateChannelConvertsSingleKeyChannel; controller/provider_plugin_test.go TestSubmittedCredentialsAreRejected, TestChannelKeyShowsCredentialSummaries, TestCopyChannelDropsCredentials, TestRemovingCredentialsRevokesThem. Extend mixed credential/static scope case in existing tests.

## Security references

Read OWASP Authentication and Session Management Cheat Sheets from the official OWASP/CheatSheetSeries repository (rendered site returned 403). Applicable restoration controls: secrets remain server-side and out of logs/key previews, forged or copied plugin credentials rejected, removed credentials revoked, refresh cannot be overwritten by stale edits, existing server authorization retained. No account login/session protocol is changed by this repair. Full compliance is not claimed until regression tests pass.
