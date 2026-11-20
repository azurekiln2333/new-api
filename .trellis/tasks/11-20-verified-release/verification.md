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
