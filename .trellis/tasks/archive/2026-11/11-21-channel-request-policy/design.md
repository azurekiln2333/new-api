# Channel policy design and file ownership

The gap lives in synthetic generation and administrative HTTP paths. Reuse existing endpoint resolver, adaptor preparation, capacity leases, bounded wait coordinator and cached HTTP clients. Management endpoints must not run through user Relay or Chat/Responses conversion. Playground already uses normal Relay; retain its authorization.

## Exclusive ownership

| Pi / pane | Owned files and responsibility |
| --- | --- |
| 1 / w4C:p9 | controller/channel-test.go; relay/diagnostic_text.go and narrow relay request preparation; controller/channel_test_request_test.go; channel-test-dialog.tsx and existing endpoint tests. Unify endpoint/system/field/plugin policy and integrate pi 2 admission. |
| 2 / w4C:pJ | Fingerprint, Wham and metrics chunks committed. Current follow-up: controller/channel_balance.go, service/provider_plugin_balance.go and existing controller/channel_billing_test.go for refresh provenance/static balance config. |
| 3 / w4C:p11 | Discovery draft frozen and transferred to the native management reviewer after source review found incomplete preview, parser, source-guard and cancellation integration. Handoff-only ownership remains in worker-3.md. |
| 4 / w4C:p12 | Price/inference/shared management and Ollama handlers frozen. Final follow-up owns service/codex_channel_models.go and its consolidated existing test for explicit saved-preview overlays; no controller edits. |
| root | Shared diagnostic admission and its existing waiter tests; CI, integration review, task artifacts/spec update and all stage/commit/push/release operations. |
| balance_integration (native helper) | controller/channel-billing.go, narrow controller/channel_balance.go cancellation and plugin-balance admission, and existing controller/channel_billing_test.go; specialized balance transport/headers/admission/body cleanup. No channel.go edits. |
| native finish/review helpers | Gemini adaptor/paging frozen; management localization committed. policy_scope_audit owns controller/channel.go, channel_upstream_update.go and their existing tests plus frozen Ollama validation. integration_check owns independent Codex/cache review. final_scope_review owns read-only whole-scope review. |

Workers must not edit other owners' files. Publish needed interfaces/edits in worker-N.md for root/owner integration. Pi 2 communicates admission contract to pi 1; pi 4 communicates Ollama controller changes to pi 3. No independent spawning, commits, installs or local verification.

Codex usage keeps its specialized Wham URLs and existing OAuth refresh semantics.
Apply static channel headers to each usage attempt after the Wham default headers
using the current access token. The external OAuth token exchange keeps its own
certificate/authentication policy and must not receive channel header overrides.
Credential refresh persistence must compare the exact source key and channel type.
In particular, MySQL case-insensitive collations cannot provide byte-exact CAS
with a plain text equality predicate. Preserve concurrent credential replacements.

Balance review ownership now includes narrow current-row atomic guards in
model/channel.go, model/channel_balance.go, service/provider_plugin_balance.go
and service/channel.go. The management checker owns these functions and the
balance files until its fix is frozen. Validate the current AutoBan, binding and
key identity inside the short persistence operation; keep HTTP outside it.

## Contracts

Ownership update: repeated model-connection failures prevented pi 2 from publishing admission. Root now owns service/channel_wait.go, service/channel_diagnostic_capacity.go and existing service/channel_wait_test.go; pi 2 retains only fingerprint orchestration/UI/tests. The available shared API is recorded in admission-contract.md.

- Auto text probes use a supported endpoint and existing conversions. Preserve embedding/rerank/image/plugin routes. Conversion is directional: no invented Gemini-to-Responses or Responses-to-Chat support. Empty configuration preserves behavior.
- Diagnostic preparation can share relay helpers, but diagnostic billing stays exactly once. Keep admin explicit-key/disabled-key recovery semantics and channel pinning.
- Reuse bounded request-scoped waiting, capacity telemetry and cleanup. Revalidate channel/settings/key/model after admission; never borrow another channel. Do not invent an end-user identity for background work or weaken user authorization. Busy retry must be bounded, safe and uncommitted with no duplicate billing.
- Management uses GetHttpClientWithProxySettings plus bounded total deadlines, not generation first-byte timeout. Static server overrides/API-key placeholders apply; browser-header passthrough does not. Unsaved ID 0 cannot collide with a persisted channel. Avoid nested admission leases.
- No schema, dependency upgrade, billing model change, UI redesign, docs/ files or unrelated refactor. Retain independent relaykit/tokenkit and all database dialects.

## Rollback

Final integration ownership: all four pi panes and native reviewers are frozen.
Root owns cloud failure corrections, exact-SHA validation, main integration and
release. The native reviewers independently approved final controller and Codex
service integration; no local verification or paid upstream probe was performed.

No schema change planned. Root makes independently reversible commits for diagnostic preparation, admission/fingerprint, discovery/balance and remaining management operations, splitting further when useful. Review exact diff and cloud checks before release.
