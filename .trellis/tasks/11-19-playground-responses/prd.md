# playground responses

## Goal and acceptance

Own R4 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Replace superseded playground protocol switch with channel supported upstream endpoints. Explicit capabilities constrain global policy; absent configuration preserves legacy behavior. Chat/Messages convert through existing converters and return client-native format; validate passthrough conflicts.

Validation: Channel form round-trip, JSON/SSE outbound path/body/header and global/passthrough matrix; real settings persistence matrix; relaykit independent build.

## Dependencies and boundaries

Shared endpoint resolver precedes R1/R7/R17 integration. Owner: pK backend / pJ settings UI.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Playground and upstream endpoints” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Planning; no product edits/tests. Final consolidated review and task activation precede implementation. R25 publication is authorized; target-server details remain a delivery prerequisite.
