# Journal - lcy (Part 1)

> AI development session journal
> Started: 2026-11-11

---



## Session 1: Playground layout fix pushed; release blocked by GitHub Actions
<!-- trellis-session: v=2 fp=941983321faa4e70 -->

**Date**: 2026-11-16
**Task**: Playground layout fix pushed; release blocked by GitHub Actions
**Branch**: `custom/rc45-stats`

### Summary

Verified and pushed playground alignment/stream overflow fix. GitHub Actions dispatch remains HTTP 422 despite enabled settings; preserved diagnosis and unsent Support draft in task research/release-status.md. Publication is incomplete and task remains in_progress.

### Git Commits

| Hash | Message |
|------|---------|
| `43bf1a563636f9d301d90eb7b7245e944e282050` | fix(playground): constrain streamed replies and align user bubbles |

### Testing

- [OK] Frontend 233 files/2698 tests passed; final affected 14 files/97 tests passed; typecheck, scoped lint/format and production build passed; browser 60 snapshots passed.

### Status

[OK] **Completed**

### Next Steps

- Account owner obtains GitHub Support review, then resume existing docker-fork.yml and verify automatic tag, image and public Release.


## Session 2: Playground layout fix released successfully
<!-- trellis-session: v=2 fp=8396423155114ce0 -->

**Date**: 2026-11-16
**Task**: Playground layout fix released successfully
**Branch**: `custom/rc45-stats`

### Summary

Restored GitHub Actions, ran docker-fork.yml successfully, published GHCR multi-arch image and public release custom-20261116-43bf1a5 with all seven Linux/macOS/Windows assets. CI passed. No server deployment was part of this workflow.

### Git Commits

| Hash | Message |
|------|---------|
| `43bf1a563636f9d301d90eb7b7245e944e282050` | fix(playground): constrain streamed replies and align user bubbles |

### Testing

- [OK] GitHub Actions run 43738287984 passed all backend/frontend checks, AMD64/ARM64 image builds, multi-platform publish, Linux/macOS/Windows release jobs, and seven-asset publish gate; release latest API and remote tag target verified.

### Status

[OK] **Completed**

### Next Steps

- No follow-up required for this task.


## Session 3: Channel concurrency and timeout failover released
<!-- trellis-session: v=2 fp=ae074995f15be0d0 -->

**Date**: 2026-11-16
**Task**: Channel concurrency and timeout failover released
**Branch**: `custom/rc45-stats`

### Summary

Implemented whole-channel concurrency, inherited/custom first-byte timeout and failover; verified real SQLite/MySQL/PostgreSQL/Redis and race; published custom-20261116-d9987dc with all assets via successful CI 43884617122.

### Git Commits

| Hash | Message |
|------|---------|
| `60da6a512` | feat(channel): add concurrency limits and upstream timeout failover |
| `673c2b8f8` | fix(ci): use loopback PostgreSQL URL for relay policy tests |
| `d9987dc9a` | fix(ci): build macOS arm64 releases on Intel runners |

### Testing

- [OK] Real SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16, Redis 7.4.12 matrix and Linux race PASS; backend/frontend CI PASS; macOS ARM64 cross-build verified by file/lipo.

### Status

[OK] **Completed**


## Session 4: Finish Herdr Claude workspace merge
<!-- trellis-session: v=2 fp=be4b8d9fdcf3bb05 -->

**Date**: 2026-11-18
**Task**: Finish Herdr Claude workspace merge
**Branch**: `merge/upstream`

### Summary

Read Herdr workspace w4C Claude pane w4C:pV, resolved all outstanding merge conflicts, repaired the channel form parser error, passed frontend production build and lint, documented unavailable Go/Vitest/database checks, committed and pushed merge/upstream, and pushed custom-20261118-9b21c12.

### Git Commits

| Hash | Message |
|------|---------|
| `9b21c12d2` | chore: resolve workspace merge and complete pending changes |

### Status

[OK] **Completed**


## Session 5: Complete gateway feature release
<!-- trellis-session: v=2 fp=bc06f5454289df8b -->

**Date**: 2026-11-20
**Task**: Complete gateway feature release
**Branch**: `merge/upstream`

### Summary

Completed R1-R25 and user corrections; main released as custom-20261120-a9b37b4. Cloud verification and published artifact checks passed. Archived 20 related tasks; no server verification.

### Main Changes

- Final product SHA: a9b37b493694a50942f98031d61f4c48e594ac2c. Per-requirement commits and boundaries are in .trellis/tasks/archive/2026-11/11-19-playground-capabilities/report.md.
- Channel fingerprints run locally using embedded lm-detector references; logs again show plain exact token counts, while channel cards retain compact totals.

### Git Commits

| Hash | Message |
|------|---------|
| `a9b37b493` | fix(console): scope expanded token counts to channels |
| `3e2ca3884` | fix(logger): synchronize concurrent rotation accounting |
| `79cd2f5c7` | fix(logs): restore plain token counts and compact rows |
| `1f3065b94` | feat(channels): run bounded model fingerprint checks on site |

### Testing

- [OK] Integration CI 44739249885 passed all 3 jobs; main release workflow 44742578933 passed all 12 jobs, including backend, frontend, real SQLite/MySQL/PostgreSQL, Redis and race checks.
- [OK] Verified public release/tag, seven asset metadata entries, three downloaded checksum files and AMD64/ARM64 GHCR manifests/configuration at custom-20261120-a9b37b4; no local tests/builds or server inspection in the final phase.

### Status

[OK] **Completed**


## Session 6: Release unified channel request policies
<!-- trellis-session: v=2 fp=4a5f08c92d11d53b -->

**Date**: 2026-11-21
**Task**: Release unified channel request policies
**Branch**: `merge/upstream`

### Summary

Completed four pi implementation scopes and independent review; exact-SHA cloud CI passed, main integrated, custom-20261121-de5c967 published with verified multi-platform images, downloads and SHA-256 checksums. Archived the current task.

### Main Changes

- Unified fixed-channel diagnostics, endpoint conversion, current-policy admission, cancellation, waiting and known-usage settlement.
- Fingerprinting defaults to three randomized serial challenges with current policy per sample.
- Discovery, balance, price sync, inference, Wham, Ollama and Codex management share applicable channel transport, headers and capacity; guarded writes preserve replacements and routing.
- Published main/tag at de5c96736f24968a81e62dee8b9960f360915ed1; GHCR custom/latest/SHA indexes match and contain linux/amd64 and linux/arm64.

### Git Commits

| Hash | Message |
|------|---------|
| `58d462bfe` | fix(channel): share bounded admission for trusted diagnostics |
| `ae68b4125` | fix(fingerprint): default to three current-policy probes |
| `c2b8026c0` | fix(channel): unify management request admission and transport |
| `1b99bc8c3` | fix(pricing): honor channel policies during upstream sync |
| `5ad885e9c` | fix(channel): enforce current inference management policies |
| `97313aded` | fix(channel): apply relay policies to pinned channel diagnostics |
| `593c0976f` | fix(channel): honor current policies across model management requests |
| `de5c96736` | test(channel): persist TLS policy before model discovery reload |

### Testing

- [OK] Cloud integration 44822062923 and complete release 44824384740 passed; root, relaykit and tokenkit backend checks plus frontend typecheck/lint/tests/build.
- [OK] Real SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16 and Redis 7.4.12 integration plus final race gate passed in GitHub Actions.
- [OK] Verified published non-draft tag, seven nonempty assets and all four binary checksum entries against GitHub asset digests using read-only API requests.
- [OK] No local tests/build/lint/typecheck/browser/upstream probes or deployment-server checks performed.

### Status

[OK] **Completed**


## Session 7: Release channel allocations and Messages routing
<!-- trellis-session: v=2 fp=431ef6cf2c6a470a -->

**Date**: 2026-11-24
**Task**: Release channel allocations and Messages routing
**Branch**: `merge/upstream`

### Summary

Released custom-20261124-53a71f8 after exact-SHA GitHub CI and all 12 publication jobs passed; archived five task records. Further executable validation is restricted to GitHub Actions by user instruction.

### Main Changes

Implemented stable channel key allocations and budgets, request-body/node diagnostics, separated panel rate scopes, configured OpenAI Messages upstream routing, and administrator-only channel fingerprint/model selection. Ordinary users continue to select eligible groups/models.

Released code commit: `53a71f88d5b568847cd7233a08f934824dcb22d3`.

- Exact-SHA CI `45227143675` succeeded, including SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16, Redis 7.4.12, real ClickHouse 25.8.33.6 fresh/released-upgrade storage, and race checks.
- Release workflow `45230816749` succeeded in all 12 jobs. Tag `custom-20261124-53a71f8` and fork `main` resolve to the tested commit. The release is public, with AMD64/ARM64 images, four platform binaries and three checksum files.
- Release: https://github.com/azurekiln2333/new-api/releases/tag/custom-20261124-53a71f8
- No direct production deployment. Bookkeeping is confined to `merge/upstream` with `[skip ci]`; `main` remains at the released code SHA.

The user prohibits local tests. Earlier local reproduction/preflight violated that instruction; after correction all local validation workers stopped. Further tests, builds, vet, lint, typechecks, database probes and executable artifact validation must run only in GitHub Actions. Post-correction work consisted of source/Git/task bookkeeping and read-only GitHub evidence retrieval; the prepared local release verifier was not run.

Retained limits: channel budgets default off; activation requires every serving node upgraded, old work drained and batching disabled. Active-ledger continuous Realtime is rejected; SQL/cache delivery is not atomic. The 429 scopes remain IP-based with an aggregate cap; production proxy configuration is unverified. Unrelated full-web lint baseline issues and optional environment-gated test skips remain. No cross-region/five-node load or latency claim.

Archived the parent task and four children using the saved 47-file tracked manifest. Unrelated workspace edits and untracked task scratch were left unstaged.


### Git Commits

| Hash | Message |
|------|---------|
| `dc9778764` | feat(channel): add stable key budgets, request diagnostics and panel rate isolation |
| `2e1c7e161` | fix(logs): support ClickHouse payloads and initialize relay fixtures |
| `c31cbc692` | fix(channel): guard cache publication after allocation status updates |
| `c4c4dc4de` | feat(relay): support declared Messages upstreams and scoped diagnostics |
| `53a71f88d` | test(channel): initialize authoritative allocations in routing fixtures |

### Status

[OK] **Completed**
