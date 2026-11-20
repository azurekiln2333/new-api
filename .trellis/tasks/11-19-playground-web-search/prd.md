# playground web search

## Goal and acceptance

Own R1 of ../11-19-playground-capabilities/prd.md, whose user requirements and observable acceptance are authoritative. Every requirement receives a separate product commit.

Reuse search DTOs and default-off tools state; extract/deduplicate JSON and SSE citations into persisted Message.sources. Explicit unsupported search fails visibly; existing usage billing is unchanged.

Validation: Payload, source extraction/storage, unsupported search and route-level Responses conversion tests.

## Dependencies and boundaries

R4 protocol integration. Owner: pK backend / p9 frontend.

Preserve all parent compatibility/security/billing/i18n rules, existing uncommitted work and secrets. Parent design section “Playground and upstream endpoints” resolves competing research proposals. No unrelated refactor or unauthorized shared-file edits.

## Status

Completed and released in `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Full integration CI `44739249885` and release workflow `44742578933` passed. See the parent report and verified-release ledger for exact evidence. No server verification was performed, as requested.
