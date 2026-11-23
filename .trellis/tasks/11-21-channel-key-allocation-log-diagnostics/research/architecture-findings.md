# Initial read-only findings — 2026-11-21

This is research for planning, not an approved implementation design. No product files were changed. The existing 11-13-channel-key-scoping artifacts establish current binding semantics but their historical exclusions do not override the user's new quota requirements.

## Duplicate allocation identity

- Append currently deduplicates credentials in controller/channel.go:1381–1403. Creation and GetKeys can already retain duplicates; simply removing append deduplication does not make duplicates safe.
- model/channel.go:1108 remapKeyScopes matches the first old occurrence by credential, so duplicate rows would inherit another user's binding/model scope after list changes.
- Disable and model-unavailability paths also locate credentials or fingerprints instead of independent allocations. Asynchronous discovery and balance writes validate index plus key, which cannot detect swapping equal credentials.
- A stable allocation identity and configuration revision must accompany selection, management operations and asynchronous writeback. Keep the upstream credential identity distinct from the allocation identity.
- Legacy ChannelInfo serialization can discard unknown fields. Mixed-version multi-node writes must be addressed explicitly rather than assuming new IDs/limits survive an old writer.

## Local quota lifecycle

- Full .agents/rules/billing.md was read before investigating quota paths.
- There is no existing channel-key local reserve/settle counter. Channel UsedQuota (model/channel.go:1676) is asynchronous-capable statistical accounting and cannot enforce a hard cap.
- service/billing_session.go manages user/token/subscription reservation and settlement; controller/relay.go:272 prepares it once while retries may switch allocations. Allocation reservations must track the actual upstream attempt and release or transfer correctly.
- Final settlement and failure refund occur through service.SettleBilling and relay.RefundFailedRequestBilling. Asynchronous task refunds/recalculation have separate persistent state and need an allocation/reservation identity.
- Changing the configured ceiling and resetting used quota are distinct operations. A reset cannot erase live reservations; late settlement/refund needs reservation IDs or epochs so it cannot overwrite a new allowance or another duplicate row.
- The user confirmed that allocating a channel key does not replace user-wallet or API-token deductions. Key allocation quota is an additional station-billed monetary cap; do not ask whether normal user/token deductions continue again.

## Existing channel balance and model visibility

- channels.balance is an upstream USD snapshot. used_quota is station-billed consumption and is not the upstream's actual cost.
- controller/channel_authz.go:88–120 and controller/channel.go:1237 ignore balance/used_quota in general channel edits. No existing manual UI was found.
- BalanceCell (web/src/features/channels/components/channels-columns.tsx:417–420) hides balance when the upstream cannot be queried. Manual entry needs display capability separate from query capability.
- Built-in multi-key channels do not support balance querying; plugin multi-key channels can show per-key upstream results.
- controller/model.go:266–285 and controller/user.go:676–688 already include bound models in both /v1/models and /api/user/models. Preserve and test channel/key intersections, token filtering, updates and quota-dependent eligibility after the new work.

## Request body and node diagnostics

- Capture an owned copy of selected original fields before route/ingress plugins or channel overrides. controller/relay.go:156 is already after possible transformations; router/task-plugin-protocol-router.go:34–48 can prepare/modify bodies earlier.
- common.ReadBodyJSONFields (common/body_storage.go:44) can copy fields. Do not retain a raw BodyStorage pointer: plugin replacement and request cleanup close it. Scope capture to relay entry points, never login/management requests.
- Fields to consider are prompt/messages, Responses input/instructions, Claude system/messages and Gemini contents/systemInstruction; multipart extraction should only retain relevant text. Binary/base64 content needs bounded metadata. These are design proposals, not silently expanded requirements.
- Recommend independent request-detail storage and a lightweight reference/status in logs.Other. List queries load all Log fields (model/log.go:603/:671); storing large bodies in Other would still pull them across regions. RecordConsumeLog also serializes params to local process logs (model/log.go:375), so raw bodies must not be attached to those parameters.
- Existing displayed log IDs may be page-local sequences (model/log.go:137–149; ClickHouse behavior also differs). A stable body reference/request ID and server-side authorization are necessary; retries may share one original request snapshot.
- Existing success/error logging switches and exclusions remain material. Authentication failures, some limit/parse/routing errors do not currently write common error logs. Whether to extend authenticated final-rejection coverage must be explicit.
- Persist common.NodeName at processing time. Preserve task submission node/body identity separately as submit_node_name; a later polling/settlement log records its actual executing node rather than substituting the submission node. WebSocket response.create requests need their own snapshot; continuous Realtime event/audio recording is a distinct scope.
- Add detail cleanup with existing log deletion and independent retention. LOG_SQL_DSN can be ClickHouse, whose logs use separate creation/TTL paths in model/main.go:407–473. Do not assume a primary-database migration covers it.
- The final proposal uses admin/root detail access, default-enabled capture and a configurable 256 KiB bound. Body retention follows remaining log/active-task references, with bounded orphan cleanup and a persistent cross-store write/delete protection mechanism. The earlier 7-day suggestion is superseded; no separate body retention duration is proposed. These choices remain subject to the single consolidated final review.

## Validation and security references

- Database behavior must be tested against real SQLite, MySQL and PostgreSQL, plus ClickHouse when its log-store path is affected. No database tests have been run during planning.
- Extend existing focused regression tests rather than scattering matching tests across layers. Validate duplicate credentials with different bindings/limits, in-flight resets, quota races/refunds, model lists, authorization and bounded log detail rendering.
- Implementation must consult the applicable OWASP ASVS release, Authentication/Session Management/Authorization/Logging guidance when changing credential or authorization paths, and record concrete applicable controls. This planning research does not assert ASVS compliance.
