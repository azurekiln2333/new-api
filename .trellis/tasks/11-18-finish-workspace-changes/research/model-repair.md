# Model and settings merge repair

Owned files: model/channel.go, channel_cache.go, channel_constraint.go, option.go, option_auto_group_test.go, request_policy.go, request_policy_test.go; relaykit/dto/channel_settings.go; service/channel_affinity.go and request_policy.go.

Reconstructed the merge from both parent commits through tree 1fca861a0390d6ac0a6668da3f0283b87b70ace0, resolving each conflict independently. Restored upstream non-conflicting edits dropped by the original whole-file ours resolution while retaining custom key model/user scopes, group model whitelists, retry defaults, first-byte timeout, theme validation, and affinity failure/capacity handling.

Restored upstream behavior:

- Channel balance display metadata, narrow settings-error writes, optimistic other_info updates, key-refresh serialization, cluster synchronization notifications, and transactional tag/ability updates.
- Channel cache reload mutex alongside user-bound key index.
- Plugin relay-route selection, Responses WebSocket eligibility, RequestPlugins and PluginConfig DTO members, route AcceptsModel, moego regexp migration.
- Exported LoadOptionsFromDatabase with read-error preservation; option sync notifications, system-task retention and epay signing validation; removed references to the upstream-deleted legacy sensitive-word settings.
- Affinity JSON field reads through ReadBodyJSONFields.
- RelayErrorSource preserving wrapper classification plus custom first-byte timeout/capacity handling, and client-cancellation behavior.

Checks completed:

- gofmt on all 10 owned files: passed.
- git diff --check for owned files: passed.
- Official temporary Go 1.26.10, GOWORK=off GOMAXPROCS=1 GOMEMLIMIT=384MiB go build -p 1 ./... inside relaykit: passed.
- Same environment, go test -p 1 ./dto inside relaykit: passed (0.033s).

Pending integration:

- Channel.Update now correctly never writes the key (upstream credential security contract). Controller key append/replace/delete must remap MultiKeyModels/MultiKeyUsers inside the locked key mutation before saving; doing so later in Channel.Update reads the already-updated key list. Coordinating with relay implementer; do not treat this as finished until resolved.
- Root build/test, credential refresh security regression tests, and real SQLite/MySQL/PostgreSQL database matrix remain required. Restored SQL behaviors include MySQL named refresh lock, PostgreSQL transaction advisory lock, SQLite process lock, optimistic other_info CAS and transaction-local channel reads. This repair adds no new model columns/indexes/GORM schema tags; PluginConfig and RequestPlugins are members of existing JSON text columns. No standalone schema migration added here.
- Existing tests cover channel status concurrent updates, tag database matrix, plugin credential locked writes, scheduled credential race handling, and request-policy matrix. The parent must record exact external engine versions and results; local independent module success does not establish database compatibility.

Security references consulted: OWASP Authentication, Session Management, OAuth2 Cheat Sheets (official repository mirrors after site returned 403). Applicable controls are refresh-token replay prevention/rotation, serialization of credential updates, no stale credential restoration, and excluding credential contents from logs. No claim of comprehensive ASVS compliance; remote provider assurance remains outside these merge repairs.
