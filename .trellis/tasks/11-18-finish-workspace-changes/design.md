# Technical Design

Treat this as an integration task. Inspect `HEAD`, `upstream/main`, and the working file for each conflict, then resolve at the smallest surrounding feature boundary. Preserve contracts from `11-11-gateway-ops-batch` and `11-13-channel-key-scoping`. Run formatting and validation only after all conflict markers are removed.

Validation covers `git diff --check`, root Go build/vet/tests, independent `relaykit`/`tokenkit` builds when touched, and Bun frontend type/test/build checks. Hosted CI is reported separately from local results.

## Repair review (2026-11-18)

Merge parents are 8052607e0 and 669d77497. Reconstruct their merge in object-only tree 1fca861a0390d6ac0a6668da3f0283b87b70ace0 and compare all 29 conflicted paths. Recover upstream plugin lifecycle, request signer, configuration and module migration while retaining custom concurrency, timeout, key selection and header templates. Do not replace whole files with either parent. Ownership is split between relay/controller, model/settings, frontend integration, and root-owned workflows. This repairs the original merge scope.

The reported upstream 403 is independently reproduced: exact Codex template with chat/completions returns client_not_allowed, OpenCode with chat returns 200, and Codex with responses returns 200. No blanket UA change is needed. Never store credentials in repository artifacts.

Release workflow references a removed Calcium-Ion/moejs module. Restore MoeForge/moejs references. Fork GHCR publishing follows main. Run branch CI before updating main; report actual run outcomes. Preserve published tags and existing user work.
