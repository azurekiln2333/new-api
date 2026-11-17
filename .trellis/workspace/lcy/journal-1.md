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
