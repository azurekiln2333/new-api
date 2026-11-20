# Design

Handle default HTTP429 and early HTTP200 failed/error/required-terminal EOF as actual errors. Buffer initialization before attempt output, exclude busy candidates, preserve bounds and partial-output accounting; no mixed answers or relaxed global Written guard.

Use parent design.md section “Admission, metrics, failover and waiting” for full architecture, contracts, compatibility and rollback. Source evidence: ../11-19-playground-capabilities/research/stream-failover.md, ../11-19-playground-capabilities/research/channel-failover.md.

Dependencies: R4 protocols; early-error contract consumed by R8. Ownership: p9 / pK review; root serializes shared files and locales. Use existing business components/services. Real database checks apply to all changed persistence/query behavior; independent modules build with GOWORK=off. Roll back owned requirement commits in reverse dependency order without overwriting parallel work or deleting user data.

## Model-unavailability boundary

Current code already includes HTTP 404 in its default retry ranges, but generic failed-channel exclusions last only for one request. The new behavior belongs in upstream error classification, persisted channel capability state, and the existing channel/key eligibility paths. Do not remove a configured model globally or disable an entire channel after a single key reports a missing model.

Prefer extending existing channel runtime metadata and locked update/cache synchronization mechanisms over adding a new database table. Keep observations attached to the attempted credential identity and effective upstream model; verify those identities against current configuration before publication so a stale response cannot disable a replacement key. Reuse model/key eligibility for memory-cache and SQL selection, affinity/pinned validation, and queued candidate revalidation. Administrators need an explicit recovery action and visible state in existing channel details.

Before implementation the assigned pi records the exact field layout and affected paths in research. Avoid a second retry loop; integrate with the bounded attempt loop and existing decision events. Initial model validation, errors without an upstream envelope and arbitrary model output cannot create persistent state. Already exposed productive output remains non-replayable.
