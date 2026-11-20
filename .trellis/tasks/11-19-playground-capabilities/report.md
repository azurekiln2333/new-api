# Gateway reliability, playground and administration — final integration report

Completed November 20, 2026. All R1–R25 requirements and approved follow-up corrections are committed, independently reviewed and released as `custom-20261120-a9b37b4` at `a9b37b493694a50942f98031d61f4c48e594ac2c`. Integration CI `44739249885` and release workflow `44742578933` passed. The public release, seven assets/checksums and both image architectures were verified.

The user authorized implementation, separate commits, push and publication, then explicitly excluded server verification and further local tests/builds/lint/browser checks. Final validation was performed in GitHub CI. Root owns publication and the final artifact observations; no server pull, restart or health observation is claimed.

## Requirement and commit map

The primary product commits remain independently identifiable. Follow-up commits below carry the user's later corrections and cloud-discovered repairs. Every listed commit is in the reviewed main revision; repeated entries identify shared corrections, not additional product requirements.

| ID | Delivered behavior at the reviewed revision | Product and follow-up commits |
| --- | --- | --- |
| R1 | Playground hosted web search defaults off, forwards enabled intent to supported upstreams, reports unsupported requests and collects deduplicated JSON/stream citations using provider usage. | `9682c37c0` |
| R2 | Administrators can switch between group and real channel selection. Server role, permission and effective group/model/key eligibility apply. Ordinary users neither fetch nor see channel choices; identity/privilege changes clear stale pins. | `ce9533537`; correction `4fab5c034`; selector regression `2fcaab4c6` |
| R3 | Model names use deterministic natural ordering, with filtering and valid saved selection preserved. | `41325a670` |
| R4 | Channel supported-endpoint settings route Chat and Messages through compatible upstream conversion, including Responses-only channels, while retaining the client protocol and configured headers. | `51d5843cf`, `a8ca5821f` |
| R5 | Success and failure logs retain the actual protocol-conversion chain. Missing legacy metadata displays Unknown instead of a false Native label. | `41b5e8527`, `f1958f78c` |
| R6 | Channel cards and details share batched live concurrency metrics, including unlimited channels, with visible-set polling, unavailable states and explicit local/cluster scope. | `8a9f6ce61` |
| R7 | HTTP429 and eligible pre-output stream failures retry another allowed channel. Native/converted initialization and keepalive framing preserve the output boundary; billable/productive output is not replayed. Definite model refusals persist channel/effective-model/credential observations, filter later routing and expose administrator recovery. | `4b34dbafa`; stream lifecycle `7112215db`, pre-header/Retry-After `6ae3e82be`; model observations `182c38037`, nil-contract repair `ac0a3cbf7`, UI/fixture lint repairs `e28a6da6b`, `08643c3ca`; shared logger race repair `3e2ca3884` |
| R8 | Bounded local/Redis waiting admits requests when eligible capacity becomes available, including one-channel and pinned cases. Cancellation cleans up waiters/grants; wake-up rechecks current policy and shares one reservation/settlement. | `2eda29549`; integration repairs `7112215db`, `6ae3e82be`, `3e2ca3884`; marker-aware eligibility `182c38037` |
| R9 | Enter in the priority editor saves without opening details; focused card Enter/Space and explicit detail actions remain available. | `92e192145` |
| R10 | Cards place usage/tokens on the left, response/full test time/concurrency on the right, and priority/weight together below. Narrow layouts wrap without truncating the test timestamp. | `1bf4c2d62`; final layout `ac109b1f2` |
| R11 | Group badges move from cards into channel details, with effective channel/group model restrictions reflected in the detail view. | `d8cbd7daa` |
| R12 | The expanded exact-plus-abbreviation format is scoped to channel metrics. Cards use compact localized totals without the purple frame; hover/table/details retain exact values and compact explanation. Logs use plain exact counts and dense rows; dashboard/rankings/users retain their previous display conventions. | `7100b258f`; channel correction `c863fa0e5`; log correction `79cd2f5c7`; remaining non-channel correction `a9b37b493` |
| R13 | Price synchronization batches preparation and bounds rendered preview rows; confirmation saves the selected catalog atomically with frozen pricing/rate/version semantics. | `d63ccd865` |
| R14 | Users have a separate numeric Total Used column with server-side sorting across pages and filters. The subsequent display correction preserves that column, its numeric values and sorting. | `9b1fbcd32`; formatting correction `a9b37b493` |
| R15 | Usage, audit, drawing/image and task logs are children of one collapsible Logs menu, retaining child access rules and direct routes. | `f3317106a` |
| R16 | Access configuration replaces Chat navigation and presents configured client cards using existing connection actions. | `93f772855`; reserved console routes `b98adc24f` |
| R17 | Recognized Codex goal-triggered turns obey each channel's goal policy; ordinary prompt words do not trigger blocking, and WebSocket turns are classified individually. | `1e35d18e1`; shared accounting-fixture isolation `e108e58b8` |
| R18 | An owner can rotate an existing API credential while retaining settings/history and immediately invalidating the old credential, including caches. Under the user's approved correction, rotation uses the authenticated console session and confirmation without an extra security challenge; the replacement secret wraps and remains copyable. | `3254adb2c`; policy correction `153c599c9`; dialog layout `5c5a1353c`; accessibility selector regression `e3a278062` |
| R19 | Optional custom secrets on creation/regeneration receive server-side format, strength, uniqueness and collision checks. Random generation remains available; audit/log output excludes secrets. Custom creation retains its security proof; rotation follows the corrected R18 policy. | `e19368c54`; rotation policy `153c599c9` |
| R20 | Credentialed CORS uses validated exact origins, with preflight, Vary and denied-origin behavior covered. Configure the public site through ServerAddress and optional CORS_ALLOWED_ORIGINS. | `4408bfd17` |
| R21 | Service status displays themed official-provider data with timestamps, stale/unavailable states and the required official-source disclaimer. | `d61fbc15c`; reserved console routes `b98adc24f` |
| R22 | Administrators run paid, bounded channel fingerprint probes and score them locally with the reviewed lm-detector reference bank/assets. Progress, cancellation, budget and reference limitations are shown; no lm.ikale.io service dependency or identity/IQ guarantee is introduced. Usage overflow, terminal queued probes and stale cancellation responses are handled. Counts use ordinary exact numbers. | `1f3065b94`; shared accounting-fixture isolation `e108e58b8`; display correction `a9b37b493` |
| R23 | Successful LinuxDO login applies the provider avatar under the existing source policy after session issuance, including MFA/Passkey completion. Failed provider images/settings writes do not break login; custom-avatar preference and URL validation remain. | `01560589c` |
| R24 | Invitation codes and redemption codes share the collapsible Codes menu while retaining their separate pages and visibility rules. | `0edff3caf` |
| R25 | The complete product revision passed exact-SHA integration CI, was fast-forwarded to fork/main and published with verified tag, release, images and seven downloads/checksums. | Workflow/fixture support `b4f72d5b0`, `3acb7a590`, scoped lint `ecffe9d05` and `a9b37b493`; integration run `44739249885`; release run `44742578933` |

Shared support includes the independently committed merge repair `cd06e03aa` and caller-scoped GET cancellation `995746f73`. Planning/evidence commits are separate from the product commits. The original workspace changes, unrelated task records and archived-file deletions remain preserved; no broad reset or staging of unrelated work was used.

## Final cloud validation

Root confirmed successful completion of CI `44739249885` at `a9b37b493694a50942f98031d61f4c48e594ac2c`:

| Job | Job ID | Result |
| --- | --- | --- |
| Backend vet, build, and test | `136298389360` | Success |
| Frontend typecheck, test, and build | `136298388967` | Success |
| Relay policy database and Redis integration | `136298389379` | Success |

The backend job runs `go vet ./...` and `go build ./...` separately in the root, relaykit and tokenkit modules with `GOWORK=off`, then `make test`. Frontend runs `bun check -b`, scoped `bun x --no-install oxlint -c .oxlintrc.json` over the affected console files, `bun run test` and `bun run build`. This is a scoped lint pass, not a claim that every repository file was linted.

The downloaded integration log for this exact run records Go **1.26.10**, SQLite **3.50.4**, MySQL **8.4.13**, PostgreSQL **16.16** (Debian 16.16-1.pgdg13+1) and Redis **7.4.12**. These are the executed versions; minimum-version engine coverage is not claimed.

Database/Redis verification ran the workflow's complete contract command, `go test -p 1 -count=1 -v ./model ./service ./controller`, followed by explicit external-engine selections for scheduled locks, channel configuration/key binding, playground routing, fingerprint probes, avatar updates, key lifecycle, goal routing and waiting. The model-availability command ran once per real SQLite/MySQL/PostgreSQL engine:

```sh
TEST_TASK_DB_DIALECT=<sqlite|mysql|postgres> go test -p 1 -count=1 -v ./model -run '^TestChannelModelUnavailability$'
```

The final race command also passed:

```sh
go test -race -p 1 -count=1 ./service ./controller ./relay/channel ./relay/helper ./relay/channel/openai -run 'ChannelCapacity|ChannelWait|StreamCommit|StreamComment|OaiResponsesStreamHandler|ConvertedStream|ResponsesFailedWork|ResponsesPostOutputTruncation|UpstreamFirstByte|FirstByteTimeout|APIAndFormRequestsInheritClientCancellation|Fingerprint'
```

Full command selectors are preserved in `.github/workflows/ci.yml` at the reviewed SHA. This final green run supersedes the prior stream-wrapper, nil-result, conditional-brace and logger-race failures on earlier revisions. Earlier local/visual results in child ledgers predate the user's stop instruction and are historical evidence, not additional verification of the final edited tree.

## Final scope and known boundaries

- **Numbers and layout:** log input/output/cache counts are plain exact numbers, e.g. `41,258 / 115` and `Cache↓ 41,088`, without parentheses or duplicate abbreviations. Only the existing collapsed mobile log rows retain their old compact K/M summaries. Dashboard summaries/user charts keep compact values, distribution tooltips keep plain exact counts, rankings retain fixed K/M/B/T precision with exact cache titles, and user balances reuse the currency/quota formatter. Shared `formatTokens` production references remain limited to channel metrics. Raw numeric sorting and billing calculations are unchanged.
- **Channel selection and failover:** ordinary users cannot fetch or display playground channel choices, including through custom channel-read permission; identity/role changes clear stored selections. Explicit pins and strict sessions retain their routing restrictions. Generic/auth/quota/path failures do not create model-unavailability observations. Model observations have no TTL or automatic probe: administrators explicitly restore them, and returning to the same credential/effective model can match an earlier observation. Ambiguous provider-side model rewriting is conservatively left unmarked.
- **Waiting:** queue capacity, deadlines, cancellation and backoff remain bounded. Ordinary generic/quota HTTP429 follows ordinary retry policy; recognized upstream saturation is eligible for waiting. Unlimited-channel admission retains best-effort accounting and does not imply universal fairness against unrelated traffic.
- **API credentials:** rotation intentionally has no additional re-authentication/security-proof challenge under the accepted product decision. Server ownership, authenticated console access, rate limiting, revocation and audit protections remain. This exception is not described as satisfying ASVS sensitive-action re-authentication requirements; the security references and accepted boundary remain in the API-key task/spec records. Custom-secret creation retains its proof requirement.
- **Goal recognition:** routing acts on recognized structured Codex goal metadata, including per-turn WebSocket handling. The metadata is client-asserted; stripped/missing metadata, manual steering or older clients can be unknown. No arbitrary prompt-keyword heuristic or guarantee of identifying every action associated with a goal is claimed.
- **Fingerprinting:** scoring is local, but probes are real potentially billable upstream requests. The token budget is an estimate when upstream usage is absent or hidden prompts differ; input plus output can exceed the estimate. Rankings compare against a finite reference bank and do not prove model identity, quality, authenticity or IQ.
- **Status, CORS and avatars:** official service-status data may lag and does not represent this site's availability; the Google source covers Vertex Gemini. CORS deployment values were not inspected. LinuxDO support is backed by the reviewed provider template and authenticated-flow tests; no live provider payload or live connect.linux.do base was observed in this task.

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
