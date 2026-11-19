# Upstream verification

## 2026-11-19 current probe

The root agent sent three minimal requests to the user-designated `api.xingchiapi.com` using the credential supplied in this conversation and the same `gpt-6.1-sol` model. The prompt was `Reply only OK.` and output was bounded to 32 tokens. Authorization was sent only to the designated origin, with redirects disabled. Credentials were held in process memory and are excluded from artifacts.

| Request | Result |
| --- | --- |
| `/v1/chat/completions`, diagnostic User-Agent | HTTP 400, `chat_completions_disabled` |
| `/v1/chat/completions`, current repository Codex template User-Agent and originator | HTTP 400, `chat_completions_disabled` |
| `/v1/responses`, same Codex template | HTTP 200, object `response`, status `completed` |

The current error tells clients to use the Responses API. This supersedes the previous conclusion that changing the Chat Completions User-Agent alone can make this upstream usable. The first discovery attempt failed to connect; public reachability subsequently recovered and all three requests above completed.

This establishes current direct upstream behavior. It does not establish the deployed new-api version, selected channel, actual channel headers or saved conversion policy: those deployment details have not been retrieved.

## Prior evidence

`.trellis/tasks/11-18-finish-workspace-changes/research/verification.md:5-12` records that on the previous investigation the Codex template with Chat returned 403, the OpenCode template with Chat returned 200, and the Codex template with Responses returned 200. It explicitly states no live channel configuration was changed or retrieved. The earlier work diagnosed the endpoint/header mismatch; it did not document successful reconfiguration of the running playground.

## Code evidence

- `web/src/features/playground/constants.ts` and `hooks/use-stream-request.ts` send the playground request to `/pg/chat/completions`.
- `controller/playground.go` relays the request using the OpenAI Chat format.
- `web/src/features/channels/lib/header-templates.ts:57-65` defines the Codex header template; the drawer applies headers independently of protocol configuration.
- `service/openai_chat_responses_mode.go:40-53` and `setting/model_setting/global.go:13-37` already implement channel/model-scoped Chat-to-Responses routing.
- `web/src/features/system-settings/models/global-settings-card.tsx:247-308` already exposes this policy as JSON in model global settings.

## Consequence

A repair must choose Responses on the upstream leg and preserve the configured client headers. It must not claim that a global User-Agent replacement or an OpenCode template alone fixes the current provider behavior. Keep protocol conversion scoped to the intended channel/models. The user's later design correction selects supported endpoints in channel settings, shared by playground and external clients; it supersedes the initial playground-only protocol toggle proposal.
