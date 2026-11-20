# Model-unavailable memory: reviewed contract

Status: implementation and static corrections prepared in this worktree. Cloud lint, type checks, regression tests and real database results are pending. No local tests, builds, lint, typecheck or browser verification were run; root stages and publishes separately.

## Stored identity and recognition

`ChannelInfo.ModelUnavailable` stores bounded observations inside the existing `channel_info` JSON value: effective `upstream_model`, a 16-byte SHA-256 credential fingerprint rendered as hex, `status_code`, fixed `evidence` (`error_code` or `error_message`), and Unix-second `observed_at`. The legacy `key_index` field is not authoritative and is never displayed by the new UI. The list is bounded to 64 entries; configured `Models` and key status are not changed.

Recognition requires an actual upstream OpenAI/Claude error envelope and HTTP 404/410, plus a known model-missing code or the exact anchored reported sentence naming the attempted upstream model. Endpoint, route, protocol, parameter, auth, quota and capacity ambiguity is rejected, including conflicting structured fields. Ordinary model output, prompts and local errors cannot create observations. Recording only occurs before exposed output and validates current enabled credential identity, scope, configured origin model and unchanged mapping against the actual attempt.

## Eligibility and publication corrections

- Ordinary memory and SQL selection consider public credentials only. An unmarked key belonging to someone else cannot keep a marked public candidate eligible.
- Request rechecks pass the authenticated user ID, allowing public keys plus that user's keys. Administrative inspection without a user argument retains the all-credential view. Key selection's existing administrative `userId <= 0` behavior is unchanged.
- User-bound model discovery excludes refused combinations, so a bound key that cannot serve a model does not widen access through unrelated public credentials outside the user's group.
- Pins, affinity, plugin selection, WebSocket revalidation and queued candidates use the request-specific predicate. Original bound-before-affinity order and strict-session policy are preserved.
- Record/restore serialize with the channel polling lock. Local cache publication reloads only the current persisted observation slice under the full-reload serialization lock; it never republishes an old complete `ChannelInfo` snapshot or remaps fingerprints by index. Cross-instance refresh uses existing `SyncTopicChannels` notification.
- Polling saves merge only the cursor into current locked metadata. Legacy configuration/scope updates preserve the stored observation slice and reject an edited key snapshot that no longer matches the stored credential list.
- Mapping regression now calls the real `UpdateConfig()` path and reloads the cache; `UpdateChannelLocked` intentionally does not persist mapping fields.

## Recovery UI and API

`POST /api/channel/:id/model_unavailable/restore` requires `ChannelOperate`. Optional body fields `model` and `key_fingerprint` narrow the deletion; an empty body restores every observation. Response `data` says whether anything changed. The management audit records channel ID and the boolean result only, avoiding arbitrary request strings or credential material.

The existing channel details drawer shows model, fixed refusal evidence, HTTP status and observation time. It never shows the stale key index, credential or raw upstream text. Its restore action clears all observations for that channel after the existing shared `ConfirmDialog` confirmation. It uses existing permission helpers, mutation/error handling and channel-query invalidation. A fresh detail query loads observations for marked channels; pending reads are canceled before restore and the query cache clears immediately on success; failed restoration leaves observations visible. No new shared control was needed. Seven UI strings have complete translations in all seven locales, inserted via `add-missing-keys.mjs` followed by `bun run i18n:sync`; the temporary script was removed.

## Regression coverage prepared for cloud CI

- `TestChannelModelUnavailability` uses the existing isolated `useScheduledChannelDatabase` fixture. Select each real dialect explicitly with `TEST_TASK_DB_DIALECT=sqlite`, `mysql`, or `postgres`; MySQL/PostgreSQL require their standard DSNs and fail when missing. Each invocation logs the exact engine version and runs both memory-cache and SQL selection paths.
- The model test covers persistence/cache reload, secret-free metadata, other models and credentials, replacement and reordering, stale polling/config snapshots, true mapping updates, public/private eligibility, fallback/no candidate and manual restore.
- Existing service tests cover positive and rejected evidence, including contradictory auth/quota/capacity fields.
- Existing controller and router suites cover scoped/all restoration and the operation permission contract.
- Existing channel details suite covers safe observations for read-only users, confirmation/success and retained observations after failure.

Cloud commands:

```sh
TEST_TASK_DB_DIALECT=sqlite go test ./model -run '^TestChannelModelUnavailability$' -count=1 -v
TEST_TASK_DB_DIALECT=mysql TEST_MYSQL_DSN=... go test ./model -run '^TestChannelModelUnavailability$' -count=1 -v
TEST_TASK_DB_DIALECT=postgres TEST_POSTGRES_DSN=... go test ./model -run '^TestChannelModelUnavailability$' -count=1 -v
go test ./service -run 'TestClassifyUpstreamModelUnavailable|TestDecideRelayRetryNames.*MissingModel' -count=1
go test ./controller -run '^TestRestoreChannelModelAvailability$' -count=1
go test ./router -run '^TestChannelStatusRoutesUseExpectedPermissions$' -count=1
# web/: existing frontend scripts in CI
bun run test -- src/features/channels/components/drawers/__tests__/channel-details-drawer.test.tsx
```

Frontend lint/typecheck scope: `web/src/features/channels/{api.ts,types.ts}`, `components/drawers/{channel-details-drawer.tsx,channel-model-availability.tsx,__tests__/channel-details-drawer.test.tsx}` plus seven locale files. Use the repository's actual CI script flags.

## Remaining boundaries

No TTL or automatic probe is added. An observation remains until explicitly restored; a new credential or different effective mapping does not match it. Returning to the same credential and effective model reuses the old observation. A provider adaptor that changes `UpstreamModelName` after ordinary mapping can fail the identity guard; such ambiguous cases conservatively record nothing. Pre-existing pin/strict-session behavior can reject the unavailable choice instead of rerouting. These boundaries do not justify claiming unrun cloud checks passed.
