# Pi 2 follow-up: Codex Wham dispatch

Freeze fingerprint changes first; root commits them independently. Pi 3 retains
Codex model discovery but does not edit controller/codex_usage.go or
service/codex_wham_usage.go. You own only those usage files and one focused
regression file (none exists today). No local validation or commits.

Current usage dispatch already applies channel proxy/TLS/HTTP policy and caller
context with a 15-second deadline. The gaps are static headers and shared
capacity admission. The service builds specialized Wham paths/default headers;
retain them. Use a controller callback at the final HTTP boundary to apply
applyFetchModelsHeaderOverrides with the current access token, then
DoChannelManagementRequest. Never use the full OAuth JSON for api_key.

One ChannelDiagnosticAdmission per operation, reused across initial and
post-refresh calls. Release the response body and slot before OAuth refresh.
Reload current single-key Codex channel/key/settings after waiting; re-acquire
if cap changed. Preserve existing behavior for admin recovery. Do not nest
leases. Context cancellation and bounded total request time apply to bodies.

OAuth token exchange at auth.openai.com remains independent: existing proxy
and OAuth transport policy, without channel header overrides or channel-specific
TLS relaxation. The pre-existing deployment-wide relay TLS policy still applies;
this task does not claim verified OAuth TLS under every deployment setting. Keep
refresh-once behavior. Do not add generic retry to the consume endpoint; it is
side-effecting and has a redeem_request_id. Avoid changing credential storage
or refresh persistence in this task.

Mandatory OWASP read gate applies before credential/header dispatch changes:
Authentication Cheat Sheet, Session Management Cheat Sheet, OAuth2 Cheat Sheet;
stable ASVS 5.0.0 when citing controls. Preserve existing route authorization,
CSRF/session controls; never log secrets or send static credentials across
origins. Record exact references and verification status in your handoff.

Focus regressions on actual outgoing headers/current credentials, capacity
wait/cancel/body-close cleanup and no retries of side-effecting failures.
Existing shared transport/admission fixtures may be reused. One consolidated
controller test file is enough; no multi-layer test scatter or docs files.
