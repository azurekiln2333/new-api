# Technical Design

Treat this as an integration task. Inspect `HEAD`, `upstream/main`, and the working file for each conflict, then resolve at the smallest surrounding feature boundary. Preserve contracts from `11-11-gateway-ops-batch` and `11-13-channel-key-scoping`. Run formatting and validation only after all conflict markers are removed.

Validation covers `git diff --check`, root Go build/vet/tests, independent `relaykit`/`tokenkit` builds when touched, and Bun frontend type/test/build checks. Hosted CI is reported separately from local results.
