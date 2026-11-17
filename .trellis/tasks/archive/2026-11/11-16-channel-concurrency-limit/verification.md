# Verification and release record

## Local results before release

- `go build ./...` and `go vet ./...`: PASS again on the frozen final implementation (session 7560).
- `GOWORK=off go build ./...` in `relaykit/`: PASS.
- `go test ./model ./setting/operation_setting -count=1`: PASS. Startup default synchronization follow-up: `go test ./model -run TestRequestPolicyDatabaseMatrix -count=1`: PASS (5.972s). External DB fixtures absent locally; SQLite results only.
- Actual HTTP 403/404/504/524 distinct-channel retry, exhaustion, pin/strict/output boundaries: PASS. Paid headers-only timeout A to B: PASS, one pre-consume and one settlement with A cancellation/released slot.
- Timeout/transport/SDK cases and explicit-zero/inheritance/cache resolution: PASS; see `report-timeout.md`.
- Whole-channel streams, cancellation release, 429/fallback, async cancellation refund, reusable/idle/closed WebSocket slot lifecycle: PASS; see `capacity-final-lifecycle-check.log`.
- Last capacity review corrections (auto-group busy/busy/free at zero retries, canceled request health/disable guards, legacy fixture alignment): PASS service 5.130s/controller 2.737s; `capacity-review-check.log`.
- Initial full root `go test ./... -timeout 180s`: all packages except controller PASS. Controller had two now-fixed fixture/decision assertions and exceeded the 3-minute aggregate deadline while entering existing security tests. Full controller rerun `go test ./controller -count=1 -timeout 10m`: PASS, 210.633s, recorded in `controller-test-final.txt`.
- Frontend focused tests: PASS, 3 files/166 cases, plus final 13 affected cases. Typecheck 1471 files/2 projects, changed TS/TSX lint/format, production build: PASS. All seven locale inventories missing/extras zero.
- Full frontend lint executed: 143 existing errors in unchanged files; copyright check: 24 unchanged files missing headers. Changed files pass; global lint is not claimed clean.
- Actionlint and `git diff --check`: PASS.

## Independent review

Trellis full-scope checker reviewed backend/frontend/module boundaries and found cancellation health, auto-group traversal, outbound context and timeout scoping issues. All have been fixed and verified. Follow-up capacity checks passed service 5.177s/controller 1.195s/relay 5.606s, and the final channel-ID snapshot guard passed service 0.112s. Limited-to-unlimited detached retry now masks the old lease marker.

Timeout scope/Jimeng/Coze regression passed service 16.1s/channel 4.387s/controller 13.164s. Complete service/AWS/JS plugin packages passed 9.769s/5.825s/12.672s. The existing HTTP2 GOAWAY fixture first encountered Windows `wsarecv`; its independent transport test and full channel package rerun both passed. Affected vet and root build passed. Background polling/discovery retain the original unbounded-by-this-setting client.

The user's four existing Herdr Pi panes are `w4C:p9` (capacity), `w4C:pJ` (timeout/failover), `w4C:pC` (UI/i18n), `w4C:pK` (persistence/release). All four were observed running actual reads in the new-api workspace; some requests encountered upstream 524/internal errors. No unobserved review is counted as passed.

Herdr Pi UI review passed. Timeout review only flagged the already fixed cancellation-health issue. Persistence Pi independently passed model tests (SQLite 3.50.4), vet and actionlint; real external engines remain CI-only. Capacity Pi confirmed key-sharing/Redis/fail-closed/WS/HTTP lifecycle. Its suggested slice race was rejected after call-path inspection: each WS call has a separate Gin context, `s.runner` is synchronous, parallel calls are rejected, and reader/lease callbacks do not access the exclusion slices. Background Pi found the valid stale detached lease issue; it is fixed and regression-tested.

## Required CI acceptance

Local Docker/real MySQL/PostgreSQL/Redis are unavailable; WSL reports `HCS_E_HYPERV_NOT_INSTALLED`, and local Go race needs unavailable CGO support. `.github/workflows/ci.yml` now provides MySQL 8.4, PostgreSQL 16 and Redis 7.4 services, records exact versions, runs the real persistence/coordination matrix sequentially, and runs the focused Go race checks. This reusable CI gates image/release publication. Skipped local checks are not substitutes for these results.

Commit `60da6a51296038ed9ad47d60d191daf078423123` was pushed to `fork/main`. Release run `43871023999`, integration job `133311922433`, completed with FAILURE on 2026-11-16 at 12:57:20 UTC. Evidence is retained in `ci-integration-43871023999.log`.

- Exact observed engines: SQLite **3.50.4**, MySQL **8.4.13**, PostgreSQL **16.16** (Debian 16.16-1.pgdg13+1), Redis **7.4.12**.
- Executed `go test -p 1 -count=1 -v ./model ./service ./controller -run 'RequestPolicy|Channel.*(Settings|Capacity|Concurrency)|InferencePreset|AffinityFailover|FirstByte|Midjourney'`. `TestInferencePresetSettingsAndDatabaseRoundTrip` and `TestRequestPolicyDatabaseMatrix` passed on all three real database engines, including fresh, legacy, custom and saved-zero cases. `TestChannelCapacityRedis` passed all three real Redis cases: independent clients/owner release, renewal/expiry/stale owner, and coordinator-loss cancellation/fail-closed.
- Only failed tests: `TestRequestPolicyRoutingDatabaseMatrix/postgres` and `TestAffinityFailoverDatabaseMatrix/postgres`. Both stopped at the existing loopback-only fixture guard, `controller/access_token_audit_test.go:863`, before the PostgreSQL routing assertions. The CI DSN used keyword format, while this fixture requires a URL for `url.Parse(...).Hostname()`. The corresponding SQLite/MySQL cases passed.
- Corrected only `.github/workflows/ci.yml` to supply the equivalent loopback PostgreSQL URL. The safety guard and product code remain intact. `actionlint` 1.7.12 and `git diff --check` passed on this correction; a new CI run must verify the PostgreSQL controller cases.
- The following race step was **SKIPPED** after the matrix failure, so it is still required: `CGO_ENABLED=1 go test -race -p 1 -count=1 ./service ./relay/channel -run 'ChannelCapacity|UpstreamFirstByte|FirstByteTimeout|APIAndFormRequestsInheritClientCancellation'`.

Correction commit `673c2b8f8a7c53bb2049a9b728275dff47277f7c` was pushed to `fork/main`. In corrected release run `43879108713`, integration job `133339906496` completed with **SUCCESS** on 2026-11-16 at **13:52:09 UTC**. The captured GitHub job response is `ci-integration-43879108713-status.json` and identifies this exact commit.

- `Record database and Redis versions`: SUCCESS at 13:47:46 UTC.
- `Verify channel settings, failover and shared capacity`: SUCCESS, 13:47:46-13:48:31 UTC, using the same real SQLite/MySQL/PostgreSQL/Redis matrix command above and the corrected PostgreSQL URL.
- `Check concurrent admission and timeout races`: SUCCESS, 13:48:31-13:52:05 UTC, executing the stated `CGO_ENABLED=1 go test -race` command. The previously skipped race gate is now satisfied.
- After the first attempt ended, `gh run view 43879108713 --attempt 1 --job 133339906496 --log` succeeded. The complete log is retained in `ci-integration-43879108713-completed.log`; it independently records SQLite **3.50.4**, MySQL **8.4.13**, PostgreSQL **16.16**, and Redis **7.4.12**, passing all matrix cases and both race-tested packages (service 1.291s, relay/channel 1.063s).

Integration and race acceptance are complete. Generated tag, release assets and image publication remain owned by the main release monitor. No production-server deployment is implied by artifact publication.

## Release progress

In run `43879108713` attempt 1, all validation, AMD64/ARM64 image builds and multi-platform image publication passed. Linux AMD64/ARM64 binaries and Windows binary/checksums are attached to draft `custom-20261116-673c2b8`. The macOS ARM64 job queued for 15 minutes and GitHub cancelled it with `The job was not acquired by Runner of type hosted even after multiple attempts` plus an ARM64 capacity notice. This is scheduler capacity, with no build steps executed. Main reran failed jobs on the same commit; attempt 2 macOS job is `133355432792`. Release publication/tag remains pending its completion.

Attempt 2 remained unassigned. After confirming the equivalent Darwin ARM64 cross-build path, commit `d9987dc9a58324eee9cdbeaaee20e2ea2e57f383` (`fix(ci): build macOS arm64 releases on Intel runners`) switches only the macOS host runner to `macos-15-intel`, explicitly retains `GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 CC=clang`, and checks `lipo -archs` equals `arm64` before upload. Actionlint/diff-check and independent review passed. Run `43884617122` now owns final acceptance and publication; an actual successful macOS build remains required. Prior draft assets are retained.

## Final publication

Run **43884617122** completed with **SUCCESS**, every job successful, on commit **d9987dc9a58324eee9cdbeaaee20e2ea2e57f383**. All validation gates reran successfully on that commit, including backend/frontend builds/tests and the real database/Redis/race integration. macOS job **133365003528** actually compiled the Darwin ARM64 executable on the Intel runner and passed the strict architecture check before upload.

Downloaded `macos-final-build.log` confirms `Mach-O 64-bit executable arm64` and `go version -m` records `CGO_ENABLED=1`, `GOARCH=arm64`, `GOOS=darwin` for the published binary.

Final integration job **133359282402** log `ci-integration-final.log` confirms the same exact versions on the released commit: SQLite 3.50.4, MySQL 8.4.13, PostgreSQL 16.16 and Redis 7.4.12. The matrix command above passed; the exact race command above passed service 1.243s and relay/channel 1.058s. No required external fixture was skipped.

Release **custom-20261116-d9987dc** is published (`isDraft=false`) at **2026-11-16 14:36:55 UTC / 22:36:55 Asia/Shanghai**. Its Git tag was read back and points exactly to **d9987dc9a58324eee9cdbeaaee20e2ea2e57f383**. All seven expected assets are present: Linux AMD64 and ARM64 binaries, Windows executable, macOS ARM64 binary, and Linux/macOS/Windows SHA-256 checksum files.

AMD64/ARM64 builds and multi-platform publication succeeded for `ghcr.io/azurekiln2333/new-api:custom-20261116-d9987dc` (also `latest` and `sha-d9987dc9a58324eee9cdbeaaee20e2ea2e57f383`). The release workflow verified asset names before publication. Repository working tree is clean. Deployment to the user's running server was not performed or asserted.
