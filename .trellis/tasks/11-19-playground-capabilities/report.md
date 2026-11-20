# Implementation progress — 2026-11-20 (updated after release)

## Scope and authorization

All 25 requirements are approved for implementation, independent review, separately revertible commits, push and GitHub release. The user removed server verification: “不用校验服务器”. Final delivery verifies GitHub CI, tag, release and images without claiming an observed rollout. Root owns staging and commits. After memory exhaustion, the four Herdr pi panes in workspace w4C have been reassigned in two batches with at most two executors active; native reviewers are paused and their edits are preserved.

## Delivered product commits

| Requirement | Commit | Evidence |
| --- | --- | --- |
| R1 hosted web search | 9682c37c0 | 37 frontend tests and focused Go relay/OpenAI tests passed; hosted-tool usage and citations preserved. |
| R2 group/channel navigation | ce9533537 | 36 SQLite routing/cache scenarios, 19 frontend tests, lint/typecheck and external SQL CI passed. Browser smoke: 13-step script passed at both viewports with 0 console errors. |
| R3 model sorting | 41325a670 | Natural-order regressions and independent review passed. |
| R4 supported endpoints | 51d5843cf, a8ca5821f | Chat/Messages-to-Responses routing and SQL persistence checks passed; separate locale follow-up. |
| R5 conversion logs | 41b5e8527, f1958f78c | Actual chain and unknown legacy metadata handled; prior exact-tree CI passed. |
| R6 live concurrency | 8a9f6ce61 | Batched visible-set metrics including unlimited channels; focused tests plus real SQL/Redis CI passed; browser desktop/mobile/dark checks passed. |
| R9 priority Enter | 92e192145 | 40 author tests, 8 root tests and cross-review passed; browser desktop/mobile/dark passed before local execution was retired. |
| R10 compact layout | 1bf4c2d62 | 169 author and 10 root tests passed; integrated visual check passed in browser desktop/mobile/dark. |
| R11 groups in details | d8cbd7daa | Effective group/model display moved to details; focused checks and browser desktop/mobile/dark checks passed. |
| R12 token counts | 7100b258f | 43 files/650 author-root tests, 17 independent formatter tests passed; browser desktop/mobile/dark checks passed; child verification.md has details. |
| R13 price sync | d63ccd865 | 42 author/reviewer tests and browser profiles at 1,000/5,000 models passed. Preview 136–330 ms; confirm-to-request 51–101 ms; one PATCH/one pricing GET. |
| R14 used balance sorting | 9b1fbcd32 | Separate numeric column and server sort; focused checks passed. |
| R15 four logs children | f3317106a | 27 author/root tests passed; browser desktop/mobile/dark checks passed. |
| R16 access configuration | 93f772855 | Configured client cards reuse existing connection flows. |
| R17 goal policy | 1e35d18e1 | Structured-metadata classifier and per-channel policy landed; focused tests and review passed. Detection is limited to explicit metadata. |
| R18 regeneration | 3254adb2c | In-place rotation with immediate cache revocation, owner-bound step-up proof, audit entries and real SQLite/MySQL/PostgreSQL plus Redis failure/recovery coverage. |
| R20 CORS allowlist | 4408bfd17 | Exact-origin/preflight/no-Origin regressions and race correction passed. Configure ServerAddress and optional CORS_ALLOWED_ORIGINS; deployed values not inspected. |
| R21 official status | d61fbc15c | Service tests and desktop/mobile-dark browser checks passed. Official-source disclaimer present. Google feed covers Vertex Gemini. |
| R23 login avatar | 01560589c | Provider avatar applied only after a session is issued, across MFA/Passkey completion; banned/stale/issuance-failure paths leave the stored avatar untouched. External SQL matrix registered; only cited LinuxDO template evidence exists, no live payload or live `connect.linux.do` base was observed. |
| R24 collapsible codes | 0edff3caf | Invitation/redemption children use the existing collapse style; browser desktop/mobile/dark checks passed. |

Shared GET cancellation fix: 995746f73, 36 tests passed. Baseline repairs: cd06e03aa, b4f72d5b0, 3acb7a590.

## Release

Main is 3254adb2c. It was published as `custom-20261120-3254adb`. Workflow run 44693311826 finished green on all jobs. Seven release attachments and the amd64/arm64 images were checked against that tag.

## Remaining gates

| Requirement | State |
| --- | --- |
| R7 failover | Final cross-review found usage settlement and stream writer lifecycle defects; native and converted paths are being repaired by independent reviewers. No local execution; cloud regression pending. |
| R8 queue | Bounded coordinator, all-busy routing and current-policy revalidation are implemented; final stream error/keepalive integration review remains. |
| R19 custom secrets | Commit e19368c54 includes backend, frontend, security regressions and seven-language translations. CI 44700305973 passed backend, frontend and real database/Redis integration. |
| R22 fingerprints | Commit 1f3065b94 includes service/assets/UI/controller integration and 39 translation keys. Fixture isolation was repaired in e108e58b8; CI 44718098082 passed backend, frontend and real database/Redis integration. |
| R25 release | The R1–R24 feature set is not yet fully released: R7, R8, R19 and R22 are outside main 3254adb2c. Final complete-tree verification remains. |

Server verification is excluded by the user's decision; only cloud evidence counts.

## CI and publication

Released tag `custom-20261120-3254adb` at main 3254adb2c, run 44693311826 green. Integration head e108e58b8 includes R19, R22 and the shared-log fixture correction; CI 44718098082 passed all three jobs. Detailed artifact and cloud evidence is recorded in the verified-release task.

## Approved corrections after the intermediate release

- API-key rotation removes its extra security-proof prompt under the explicitly approved session-only policy; custom creation keeps its proof. The result dialog must wrap long keys and adapt to the viewport.
- Channel cards use usage/tokens on the left, response/test time/concurrency on the right, and priority/weight together in a bottom row. Chinese card token counts are compact 万/亿; hover and details include exact and parenthesized compact values. Remove the purple token frame and truncated date/time.
- Explicit upstream model-unavailable errors must persist a scoped unavailable marker and route future requests away from that channel/model/credential. Ordinary path 404s do not delete capabilities; recovery remains available to administrators. This is a separately revertible R7 follow-up.

Fork/main triggers the release workflow and emits a custom date/SHA tag, amd64/arm64 images, binaries and release. Integration/tag-only pushes do not publish. Final main fast-forward must use the reviewed product tree; post-release bookkeeping stays on integration.

## Follow-up integration commits (2026-11-20)

- `153c599c9`: owner key rotation uses the existing authenticated console session without the extra security-proof prompt; custom creation keeps its proof.
- `5c5a1353c`: replacement credentials wrap within the viewport, with vertical scrolling and a reachable copy action.
- `c863fa0e5`: channel cards display compact token totals without a badge; hover and table/detail views preserve exact totals and abbreviations.
- `ac109b1f2`: usage/tokens left, response/full test time/concurrency right, priority/weight below.

All four are pushed to `fork/merge/upstream`. Cloud run `44721964559` passed backend and frontend typecheck but failed two key-dialog regression selectors that queried `dialog` instead of the shared component's `alertdialog`. The database job was cancelled when the newer card commit was pushed. These follow-ups are not yet released or fully verified; selector corrections and the newer cloud run remain pending.

The playground follow-up is administrator-only manual channel selection. Existing server role-plus-permission gates remain authoritative; the identified frontend gap is stale selected-channel state across identity or privilege changes. Ordinary users must not fetch or display channel choices, even with custom channel-read permission.

## Workspace preservation

Original 46 dirty paths are recorded in research/worktree-baseline.json and local .git/task-baselines/playground-capabilities/preimplementation.patch. Existing spec/task edits and archived-task deletions remain preserved; root node_modules is not staged. Research and child verification ledgers retain evidence. Browser/performance artifacts are in the system temporary new-api-browser-check directory. No exposed API secret is used or copied into repository artifacts.

## Verified follow-up tree — 2026-11-20

Cloud CI `44728516165` passed all three jobs for `2fcaab4c6`: backend vet,
build and tests (`136261967178`); frontend typecheck, scoped lint, tests and
production build (`136261967325`); real database/Redis integration and race
checks (`136261966819`). This covers key/card corrections, admin-only
playground pin/cache isolation (`4fab5c034`), test-selector repairs (`e3a278062`,
`2fcaab4c6`), and log presentation correction (`79cd2f5c7`).

The log correction restores plain exact input/output/cache counts and dense
rows. Only the existing collapsed mobile summary keeps its old compact K/M
format. Shared channel formatting is unchanged. It is independently revertible.

R7 `4b34dbafa` and R8 `2eda29549` are independently committed and pushed.
Their final static review includes native/converted output boundaries,
serialized frame writers, bounded FIFO waits and exact accounting regression
assertions. Their full cloud gate is pending; no local execution was used.
Persistent model-unavailability edits remain outside these commits.

## Final failover and availability follow-up

CI `44731068705` at `2eda29549` passed frontend but failed backend and database
tests. Its log exposed a stopped keepalive wrapper retaining a closed attempt
gate, which swallowed a later successful response. Commit `7112215db` removes
the owned wrapper after stop/join and covers two successive attempts. The same
commit updates capacity fixtures to establish real productive output and then
verify all-full waiting followed by release, and asserts full refund/no consume
log for zero-work pre-output HTTP failures. Existing WebSocket accounting remains
unchanged. CI `44734501697` is checking this tree; no pass is claimed yet.

Commit `182c38037` independently implements persistent model-unavailability
observations and recovery. Definite upstream model 404/410 refusals mark only the
channel/effective model/credential fingerprint; routing skips that combination.
Channel details expose safe evidence and an authorized restore action. Configured
models and credentials are retained. Real SQLite/MySQL/PostgreSQL verification
is included in CI. Generic endpoint 404s and ambiguous errors do not create marks.
