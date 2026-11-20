# Channel policy design and file ownership

The gap lives in synthetic generation and administrative HTTP paths. Reuse existing endpoint resolver, adaptor preparation, capacity leases, bounded wait coordinator and cached HTTP clients. Management endpoints must not run through user Relay or Chat/Responses conversion. Playground already uses normal Relay; retain its authorization.

## Exclusive ownership

| Pi / pane | Owned files and responsibility |
| --- | --- |
| 1 / w4C:p9 | controller/channel-test.go; relay/diagnostic_text.go and narrow relay request preparation; controller/channel_test_request_test.go; channel-test-dialog.tsx and existing endpoint tests. Unify endpoint/system/field/plugin policy and integrate pi 2 admission. |
| 2 / w4C:pJ | controller/channel_fingerprint.go; service/channel_fingerprint*.go; existing fingerprint tests and page. Fingerprint freshness/default 3. |
| 3 / w4C:p11 | All controller/channel.go edits; controller/channel_upstream_update.go; controller/channel-billing.go; Gemini discovery support; Codex discovery/usage policy integration; existing related tests. Discovery/balance headers, settings, model union. |
| 4 / w4C:p12 | controller/ratio_sync.go; controller/channel_inference.go; relay/channel/ollama/relay-ollama.go; shared management HTTP helper if required; existing ratio/inference tests. Connection/admission fixes and coordination of Ollama controller changes with pi 3. |
| root | Shared diagnostic admission and its existing waiter tests; CI, integration review, task artifacts/spec update and all stage/commit/push/release operations. |

Workers must not edit other owners' files. Publish needed interfaces/edits in worker-N.md for root/owner integration. Pi 2 communicates admission contract to pi 1; pi 4 communicates Ollama controller changes to pi 3. No independent spawning, commits, installs or local verification.

Codex usage keeps its specialized Wham URLs and existing OAuth refresh semantics.
Apply static channel headers to each usage attempt after the Wham default headers
using the current access token. The external OAuth token exchange keeps its own
certificate/authentication policy and must not receive channel header overrides.

## Contracts

Ownership update: repeated model-connection failures prevented pi 2 from publishing admission. Root now owns service/channel_wait.go, service/channel_diagnostic_capacity.go and existing service/channel_wait_test.go; pi 2 retains only fingerprint orchestration/UI/tests. The available shared API is recorded in admission-contract.md.

- Auto text probes use a supported endpoint and existing conversions. Preserve embedding/rerank/image/plugin routes. Conversion is directional: no invented Gemini-to-Responses or Responses-to-Chat support. Empty configuration preserves behavior.
- Diagnostic preparation can share relay helpers, but diagnostic billing stays exactly once. Keep admin explicit-key/disabled-key recovery semantics and channel pinning.
- Reuse bounded request-scoped waiting, capacity telemetry and cleanup. Revalidate channel/settings/key/model after admission; never borrow another channel. Do not invent an end-user identity for background work or weaken user authorization. Busy retry must be bounded, safe and uncommitted with no duplicate billing.
- Management uses GetHttpClientWithProxySettings plus bounded total deadlines, not generation first-byte timeout. Static server overrides/API-key placeholders apply; browser-header passthrough does not. Unsaved ID 0 cannot collide with a persisted channel. Avoid nested admission leases.
- No schema, dependency upgrade, billing model change, UI redesign, docs/ files or unrelated refactor. Retain independent relaykit/tokenkit and all database dialects.

## Rollback

No schema change planned. Root makes independently reversible commits for diagnostic preparation, admission/fingerprint, discovery/balance and remaining management operations, splitting further when useful. Review exact diff and cloud checks before release.
