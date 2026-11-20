# Cloud release verification — 2026-11-20

Observer: `/root/r8_queue`. Root alone stages, commits and pushes. This ledger
records an intermediate release containing 20 of R1-R24, not final completion
of the user's entire request. No server check was performed. The user canceled
server verification and later requested cloud-only validation; this observer
ran no further local tests, builds or lint after that instruction.

## Published intermediate release

| Evidence | Observed value |
| --- | --- |
| Repository | `azurekiln2333/new-api` |
| Released commit | `3254adb2c5e3660ea9975b2b5b2fcb9dddb94a2d` |
| Tag | `custom-20261120-3254adb` |
| Integration CI | `44688308042`, CI #6, success, completed `2026-11-20T06:41:21Z` |
| Main release run | `44693311826`, docker-fork #21, success, completed `2026-11-20T07:43:49Z` |
| Release publication | `2026-11-20T07:43:45Z`; `draft=false`, `prerelease=false` |
| Tag object | Lightweight commit tag pointing directly to the released commit |
| Release target | Same full commit SHA as the tag and workflow |
| Image | `ghcr.io/azurekiln2333/new-api:custom-20261120-3254adb` |
| Matching immutable image tag | `ghcr.io/azurekiln2333/new-api:sha-3254adb2c5e3660ea9975b2b5b2fcb9dddb94a2d` |
| OCI index digest | `sha256:ca00ba6443b652e5d1fa43f79942657007951fde99d3dc9e06c242756e82318e` |

Public release:
https://github.com/azurekiln2333/new-api/releases/tag/custom-20261120-3254adb

Release workflow:
https://github.com/azurekiln2333/new-api/actions/runs/44693311826

Both image tags above were read from GHCR and returned the same OCI index.
The two runnable Linux manifests are:

| Architecture | Manifest digest |
| --- | --- |
| AMD64 | `sha256:9d09a6267c259d60ea2a15934f15220b620de46ebb82ec43849042593206b0bb` |
| ARM64 | `sha256:21f7ec7b16407c171a2069ab5598eaa61bdd3f44d6cc6a58e9c3d74d5bfab7e2` |

Both architecture config blobs were fetched. Each declares
`org.opencontainers.image.revision=3254adb2c5e3660ea9975b2b5b2fcb9dddb94a2d`
and source `https://github.com/azurekiln2333/new-api`. The index also contains
the corresponding attestation manifests. This proves registry publication and
the declared source revision; it does not prove a server pulled or started it.

## Published downloads and checksums

The public release API lists all seven expected assets with nonzero sizes:

| Asset | Bytes | GitHub asset SHA-256 |
| --- | ---: | --- |
| `new-api-custom-20261120-3254adb` | 144077065 | `ca58ddd860e05a7fd8f12167a82301ed3b22577f6267e4f3169db1b3f9721b53` |
| `new-api-arm64-custom-20261120-3254adb` | 139198729 | `1e42e36c9eee83c6228544aa4ecff7c4d18bbc2579199040b45e37cd3a340b4a` |
| `new-api-macos-custom-20261120-3254adb` | 169428114 | `665af7ff45ebf9784300a700eee195a9655a358bc82ebdf52adfc9abf201219e` |
| `new-api-custom-20261120-3254adb.exe` | 146049536 | `389de3eac1e7eff6a823e5236e909c1cbf5cb5fb5e8d7fdc517c8a92826321ef` |
| `checksums-linux.txt` | 202 | `aedc85710e5f2b841cc249c5147129206e5f7b3530587adaff34ebb0ef962083` |
| `checksums-macos.txt` | 104 | `3b6f5bb082bd22f40cde97ef8a5afa68d4419b6105d86f2439e7dc9f67ef3596` |
| `checksums-windows.txt` | 102 | `a56650b22de8cfdc0b3bf860ae0d450bfc42f64e19063f83e87e62de6a562cd1` |

All three checksum files were downloaded. Their four binary names and SHA-256
values exactly match the corresponding GitHub uploaded-asset digest metadata.
The large binaries were not downloaded or executed locally; this is checksum
manifest consistency verification, not a claim of local binary execution.

## Cloud job outcomes

Every job in release run `44693311826` completed successfully:

| Job | Job ID |
| --- | --- |
| Validate: real database and Redis integration | `136144052534` |
| Validate: backend vet, build and tests | `136144052739` |
| Validate: frontend typecheck, tests and build | `136144052801` |
| Prepare version | `136148812104` |
| Build ARM64 image | `136148906679` |
| Build AMD64 image | `136148906757` |
| Publish multi-platform image | `136150930006` |
| Prepare draft release | `136151029388` |
| Windows downloads | `136151088622` |
| Linux downloads | `136151088758` |
| macOS downloads | `136151088805` |
| Publish release with downloads | `136156320333` |

The exact workflow config uses MySQL `8.4`, PostgreSQL `16`, and Redis `7.4`
service images; SQLite fixtures run in the test packages. The version-recording
step and real-engine test steps succeeded. Anonymous Actions log download is
restricted (HTTP 403, repository admin rights required), and the public HTML
requires login to view logs. Therefore this observer cannot record the precise
resolved service patch versions or individual verbose test output. No exact
patch versions are inferred from floating image tags. Job/step success is
observed through the public REST API.

## Requirements included in the intermediate release

These 20 product requirements are present at the released SHA. Their individual
implementation and review evidence remains in the parent report and child task
verification records; the table identifies the rollback commits.

| Requirement | Behavior | Product commit(s) |
| --- | --- | --- |
| R1 | Playground hosted web search | `9682c37c0` |
| R2 | Playground group/channel navigation | `ce9533537` |
| R3 | Model name sorting | `41325a670` |
| R4 | Supported channel endpoints | `51d5843cf`, `a8ca5821f` |
| R5 | Actual protocol conversion in logs | `41b5e8527`, `f1958f78c` |
| R6 | Live channel concurrency | `8a9f6ce61` |
| R9 | Priority Enter behavior | `92e192145` |
| R10 | Compact channel card layout | `1bf4c2d62` |
| R11 | Group/model access in channel details | `d8cbd7daa` |
| R12 | Exact and compact token counts | `7100b258f` |
| R13 | Large price-sync responsiveness | `d63ccd865` |
| R14 | Used-balance column and sorting | `9b1fbcd32` |
| R15 | Four logs under collapsible navigation | `f3317106a` |
| R16 | Access configuration/client cards | `93f772855` |
| R17 | Explicit goal-metadata channel policy | `1e35d18e1` |
| R18 | API key regeneration | `3254adb2c` |
| R20 | CORS allowlist | `4408bfd17` |
| R21 | Official service status | `d61fbc15c` |
| R23 | LinuxDO avatar on completed login | `01560589c` |
| R24 | Collapsible invitation/redemption codes | `0edff3caf` |

R7 failover, R8 queue, R19 custom secrets, and R22 fingerprints are outside this
released SHA. R25 final delivery remains open until the remaining product
changes pass exact-commit cloud checks and the complete release is observed.

## Subsequent integration CI: R19

Commit `e19368c547f274497150732d46deb6a71b960f63` was pushed to
`merge/upstream`, not yet included in the intermediate release above. CI run
`44700305973` (CI #7) completed successfully at `2026-11-20T08:20:03Z`.

| Job | Job ID | Result |
| --- | --- | --- |
| Backend vet, build and tests | `136166781798` | Success |
| Frontend typecheck, tests and build | `136166782140` | Success |
| Database and Redis integration | `136166782426` | Success |

The explicit step `Verify API key rotation and custom keys on MySQL and
PostgreSQL` succeeded, as did the all-package database contracts and concurrency
race steps. R19's backend and frontend, including custom creation and its
regressions, are committed in this verified integration tree. R19 is not yet
included in the intermediate main release; the remaining requirements also
await their final integration and release.

Run: https://github.com/azurekiln2333/new-api/actions/runs/44700305973

## Subsequent integration CI: R22 and isolated failure

Commit `1f3065b9434da0b729a492769bf0e5e43044a083` was pushed to
`fork/merge/upstream`. In CI run `44703786747`, backend job `136178229518`
and frontend job `136178229298` passed. Integration job `136178229640`
failed at `Verify per-turn goal routing on MySQL and PostgreSQL`; preceding
fingerprint SQL, API key lifecycle, avatar and scheduled-lock steps passed.

The retrieved integration log identifies a test-fixture isolation defect:
`assertResponsesWSAccounting` in `controller/responses_websocket_test.go`
queried all consume records in the shared log database. The expected three
WebSocket records (user 5062002, token 6) were present, alongside three earlier
channel-test records for user 11 and channels 82064–82066. This caused expected
3 / actual 6. The per-turn goal routing assertions themselves passed. The fix
must constrain the helper by its fixture user/token, without deleting other
tests' data; its verification is pending a new cloud run.

Exact engine versions reported in this run: MySQL 8.4.13, PostgreSQL 16.16
(Debian), Redis 7.4.12 and SQLite 3.50.4. No local test or server rollout check
was performed for this follow-up.

## Cloud retest of the fixture correction

Commit `e108e58b826bfda9746bde5fec01d8f69a2252e6` isolates all three affected
WebSocket consume-log lookups by fixture user ID, token ID and log type. It
was pushed to `fork/merge/upstream`. CI run `44718098082` completed with all
three jobs successful: backend, frontend, and real database/Redis integration.
This verifies the committed R19/R22 tree plus the fixture correction, not the
still-uncommitted key/card corrections or R7/R8/model-unavailability work.

## Cloud verification of key/card/playground/log corrections

CI `44728516165` at `2fcaab4c6` completed with all jobs successful:
backend `136261967178`, frontend `136261967325`, database/Redis integration
`136261966819`. Frontend includes scoped oxlint, typecheck, regression suites
and production build. Log display correction is `79cd2f5c7`; playground test
selector correction is `2fcaab4c6`. This verifies their integration tree but
is not a new main release or validation of R7/R8/model-marker code.

## Failover, waiting and model-availability integration

Separate product commits: `4b34dbafa` (early stream failover), `2eda29549`
(bounded waiting), `182c38037` (persistent channel/model/credential observations
and recovery). CI `44731068705` at `2eda29549` passed frontend but failed backend
and database tests: an owned keepalive wrapper retained a closed stream attempt
gate and swallowed the next response. Commit `7112215db` repairs that lifecycle
and updates capacity/zero-work accounting regressions. CI `44734501697` checks
the integrated tree; its frontend typecheck passed but scoped lint found a
missing brace in the new availability component. Further cloud repair is active.

No new main tag/release is claimed by this entry. All follow-up validation is
hosted; server verification remains excluded.

The completed `44734501697` logs showed only two existing controller nil-result
assertions failing after the marker change; `ac0a3cbf7` preserves their original
contract. `e28a6da6b` repairs the frontend conditional-brace lint finding. The
same database log explicitly passed `TestChannelWaitRouting` (13.96s),
`TestChannelCapacityStreamsRouteAroundFullChannelAndRelease` (1.21s) and
`TestResponsesStreamOutcomesPreserveAccounting` (1.49s). The first failing step
prevented the subsequent external-engine/race steps from running, so this is
not a complete database verification result.

`6ae3e82be` additionally preserves pre-header heartbeat retry boundaries for
native/converted Responses and propagates HTTP Retry-After. CI `44736079684`
is verifying all these fixes; only its eventual results can close this gate.

## Complete product-tree verification

Run `44736079684` finished with backend success and all database steps passing.
Frontend exposed one missing brace in the detail fixture (`08643c3ca` repairs
it). Its race check exposed the legacy unsynchronized logger counter; rotation
count and scheduling now use atomic operations in `3e2ca3884`. No gate/queue
race was reported. `a9b37b493` additionally restores all non-channel numeric
display conventions and keeps the channel exact-plus-abbreviation scope.

Integration run `44739249885` completed successfully at the exact full revision
`a9b37b493694a50942f98031d61f4c48e594ac2c`:

| Job | Job ID | Result |
| --- | --- | --- |
| Backend vet, build and test | `136298389360` | Success |
| Frontend typecheck, scoped lint, tests and build | `136298388967` | Success |
| Database, Redis and race verification | `136298389379` | Success |

Downloaded integration log:
`D:/AppData/Local/Temp/new-api-ci-44739249885-integration.log`.
Executed versions: Go 1.26.10, SQLite 3.50.4, MySQL 8.4.13,
PostgreSQL 16.16 (Debian 16.16-1.pgdg13+1), Redis 7.4.12. All database
steps passed, including model observations on SQLite/MySQL/PostgreSQL,
channel configuration, fingerprint probes, avatar and API key lifecycle,
goal/failover/wait policies. The final race step passed at 13:04:44 UTC;
all five selected packages passed, including controller in 17.876s.
Exact commands and selectors are in `.github/workflows/ci.yml` at that SHA.

Root fast-forwarded `fork/main` from `3254adb2c` to this verified full revision.
Release workflow `44742578933` started at 13:14:33 UTC and passed all three
validation jobs again (`136309669530`, `136309669582`, `136309669736`).
At that interim checkpoint, prepare-version job `136315443584` had succeeded;
AMD64 `136315498681` and ARM64 `136315498768` image builds were still running.
R25 remained open until the tag, assets/checksums and image revision could be
observed. The completed publication evidence below closes that gate. No local
tests/builds or server verification were performed during this final phase.

## Verified publication

Release workflow `44742578933` completed successfully on November 20, 2026
at 13:38:15 UTC (21:38:15 Asia/Shanghai), with all 12 jobs successful.
The previous `custom-20261120-3254adb` release is superseded by the complete
R1–R24 product tree and follow-up corrections. R25 publication is complete.

| Publication evidence | Observed result |
| --- | --- |
| Main and tag source revision | `a9b37b493694a50942f98031d61f4c48e594ac2c` |
| Published tag | `custom-20261120-a9b37b4` |
| Release | https://github.com/azurekiln2333/new-api/releases/tag/custom-20261120-a9b37b4 |
| Public state | `draft=false`, `prerelease=false`; published `2026-11-20T13:38:12Z` |
| Integration CI | `44739249885`, all three jobs successful |
| Release workflow | `44742578933`, all 12 jobs successful |
| Image | `ghcr.io/azurekiln2333/new-api:custom-20261120-a9b37b4` |
| Matching image tags | `latest`, `custom-20261120-a9b37b4`, `sha-a9b37b493694a50942f98031d61f4c48e594ac2c` |
| Multi-platform index digest | `sha256:005cf64c18dee7dc2960b0ad9279eb2b0973fd8ad7358d78f9f1dbe36f6a1771` |
| Download verification | Seven nonempty assets; three downloaded checksum files match all four binary asset digest metadata |

Both runnable image configurations declare the expected source repository and
revision `a9b37b493694a50942f98031d61f4c48e594ac2c`:

| Architecture | Manifest digest |
| --- | --- |
| amd64 | `sha256:56c5e15cd3db39d35cc8b7c8838853c1acc2273d8bf06f63587732adb904bbc1` |
| arm64 | `sha256:546aec089163e51b029802e315ae9239f1546dac0587a9165eebc5b7307cfbc8` |

Published downloads:

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `checksums-linux.txt` | 202 | `sha256:0f90c21506057843f19550f8995144b965d0a1d8101a8181766980f1ad9224de` |
| `checksums-macos.txt` | 104 | `sha256:cc47fc9c2901cb155a5598b64706525dd3d954f1428ee11e11fef5d57fc14223` |
| `checksums-windows.txt` | 102 | `sha256:d2d6c337b2b34f15cd80028af9405dca8ea4c03ca460b1de42c60ad948c81faf` |
| `new-api-arm64-custom-20261120-a9b37b4` | 145096969 | `sha256:7b24ba5c31e131a6447e72b0fee90dcbd96ff60528892e7b18b4c8323433612a` |
| `new-api-custom-20261120-a9b37b4` | 150003977 | `sha256:54da51679210b360a97b4b7befc5097b0458d2634e4a39d4ff96f2869940ba02` |
| `new-api-custom-20261120-a9b37b4.exe` | 151985152 | `sha256:69cf7814aac1fff85aa9b734f5a93758905e0696a8b6a2b15a06f205b4316387` |
| `new-api-macos-custom-20261120-a9b37b4` | 175528882 | `sha256:a32e52887c428b1f46ef4b9a74a464c5fe820c3606b9aa0debdbcaf18722e47f` |

Release jobs:

| Job | ID | Result |
| --- | --- | --- |
| Validate release commit / Relay policy database and Redis integration | `136309669530` | Success |
| Validate release commit / Frontend typecheck, test, and build | `136309669582` | Success |
| Validate release commit / Backend vet, build, and test | `136309669736` | Success |
| Prepare version | `136315443584` | Success |
| Build amd64 | `136315498681` | Success |
| Build arm64 | `136315498768` | Success |
| Publish multi-platform image | `136318140913` | Success |
| Prepare draft release | `136318295955` | Success |
| Build release downloads / Linux Release | `136318391735` | Success |
| Build release downloads / macOS Release | `136318391759` | Success |
| Build release downloads / Windows Release | `136318392011` | Success |
| Publish release with downloads | `136321432866` | Success |

Artifact checks read GitHub release/tag metadata, downloaded the three small
checksum files, and fetched GHCR index/manifests/configuration blobs. Large
binaries were not downloaded or executed locally. These checks establish
published artifacts and declared source revision, not a server rollout.
The user excluded server access, pull/restart and health verification.
