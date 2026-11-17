# Planning evidence: channel concurrency

Date: 2026-11-16. Research only; no implementation or product changes.

## Configuration and counting

- `model/channel.go:51`: channel `Setting` already uses a TEXT JSON value. DTO owner is `relaykit/dto/channel_settings.go:14`; `model/channel.go:1169` owns settings validation. A proposed nonnegative concurrency setting can fit this path without a new column. No field name/default has been implemented.
- `model/channel_cache.go:149`: cache reload rebuilds channel objects. Active occupancy must survive these reloads and must not live on cached channel objects.
- `model/channel.go:227`: existing channel mutex only protects multikey selection, not upstream request lifetime. Channel-ID accounting would cover all keys belonging to that channel.
- `middleware/task_artifact_access.go:79`: local mutex/counter with idempotent release is an existing concurrency pattern. It is not a general channel semaphore.
- `service/log_export.go:33`: Redis owner-token lock and renewal exist, but it is single-slot, renewal failures are ignored, and release is not idempotent. Do not copy it as a strict capacity-N solution.
- `README.zh_CN.md:245`: shared Redis is required for cross-instance counters; process-local counters are per-instance. Channel config propagation to other instances is periodic, so lowering a limit needs explicit distributed semantics.
- `model/channel_settings_test.go:111`: existing settings persistence tests support SQLite/MySQL/PostgreSQL. Tests that skip absent external DBs do not meet the repository's real-database gate.

## Routing and lifetime

- `service/channel_select.go:239`: GetUntriedSatisfiedChannel and filters can support avoiding a saturated channel, but occupancy is not an upstream failure.
- `middleware/distributor.go:138`: an existing ineligible-channel fallback leaves channel health intact; use this as a semantic reference.
- `service/relay_error.go:46`: no retry after response has started. Lines 49 onward also protect strict sessions and pinned channels.
- `controller/relay.go:154`: Claude keepalive starts SSE early; capacity admission and any alternate-channel choice must precede first output if the fallback is an ordinary HTTP 429.
- Existing failover PRD in `11-11-gateway-ops-batch/prd.md` preserves pinned-channel and response-started stop conditions; it defines no channel-capacity policy.

## Saturation decision (confirmed)

The user answered "1吧" on 2026-11-16, selecting alternate eligible-channel routing followed by HTTP 429 if all eligible channels are full. No waiting queue; saturation must not affect channel health.

The user subsequently confirmed "整个渠道2": every key in one channel shares the configured capacity. The existing editor supports both one key per channel (`batch`) and many keys in one channel (`multi_to_single`), so the counter is keyed by channel ID. Distributed failure handling, submission lifetime and the added timeout/failover requirements are defined in the integrated design.

## Channel editor evidence

- `web/src/features/channels/types.ts:86`: ChannelSettings has no concurrency field.
- `web/src/features/channels/lib/channel-form.ts`: schema/defaults (268/468), settings load (510 onwards), and `buildSettingJSON` (674) all require coordinated updates. The serializer explicitly constructs known keys, so adding only a form control would silently lose the value. Create/update payloads are assembled at 917/964.
- `web/src/features/channels/lib/channel-configuration.ts:49` and `:110`: routing field list and error-location mapping need to include any new setting.
- Existing priority control in the channel drawer uses shared FormField/FormMessage and number Input; no new numerical UI component is needed.
- `web/src/features/channels/hooks/use-channel-mutate-form.ts:48`: setting/settings are stripped when a user lacks sensitive-write access. A settings-backed concurrency field must preserve this contract, including visible disabled/read-only behavior rather than allowing an edit that is discarded. Existing Other Settings uses a sensitiveLocked fieldset.
