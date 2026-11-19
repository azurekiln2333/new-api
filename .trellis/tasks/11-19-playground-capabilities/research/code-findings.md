# Four-pi research

## Search: w4C:p9

playground-input-tools.tsx:84-88 and input-tool-utils.ts:56-60 only show a development notice. DTO WebSearchOptions and provider conversion already exist (relaykit/dto/openai_request.go:88, toolconv/decode.go:85-97). Responses-to-Chat JSON/stream converters preserve citations; frontend currently ignores annotations although Message.sources already renders/persists. Explicit unsupported search can currently be dropped by default conversion-loss handling. Existing billing must consume actual provider facts.

## Channels: w4C:pJ

ModelGroupSelector is shared. Ordinary-user APIs expose authorized groups/models, not channels. middleware/auth.go:704-720 forbids ordinary users selecting channels. UI-only filtering would not guarantee selected upstream routing. Admin-only selection requires safe metadata plus validated pin, group/model/key eligibility and existing constraint semantics.

## Sorting: w4C:pC

controller/user.go:660-679 combines group/bound models; playground/api.ts:47-64 keeps incoming order. use-playground-options.ts:119-132 stores that list and uses first-model fallback. Selector filtering/grouping preserves order. Sort frontend copies and retain existing model IDs/selections. Root rejected a custom natural-sort parser in favor of fixed valid en Intl.Collator with deterministic ties.

## Responses: w4C:pK

router/relay-router.go:69-75 exposes /pg/chat/completions; controller uses Chat. Header templates do not choose protocols. relay/compatible_handler.go already calls textRequestViaResponses when the global policy matches. This returns Chat data and uses existing settlement. Root correction: the current global channel/model policy IS a usable workaround; a claim that no conversion path exists is incorrect. The user's later correction replaces the initial playground-preference proposal with a channel supported-endpoint declaration used by all incoming protocols. Current direct upstream evidence is in upstream-verification.md; parent design.md is authoritative.

## Review corrections

Read billing rules in full before protocol/settlement review even if arithmetic is reused. Check OWASP before permission changes. Worker proposals for ordinary-user channel visibility or UI filtering without real routing were not adopted. No live deployed configuration was read or changed; no product files were edited.
