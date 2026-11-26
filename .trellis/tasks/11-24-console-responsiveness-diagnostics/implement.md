# Execution
## Release boundary correction — 2026-11-26

PR #4 merged as `60928d8f207d1f874a2d64886a96a73c11d78469`. The fork release
workflow `45813059715` succeeded and published `custom-20261126-60928d8` and
the GHCR `latest` image at 12:23 UTC. The user deploys via Watchtower; publication
is the authorized delivery mechanism. No SSH/server rollout is required here.

That release did not contain every requested feature. The accepted R37 masked
preview and direct append deltas remained uncommitted in the original worktree.
They are being isolated on `fix/multikey-preview-actions-20261126`, based on the
released main, alongside the reported quota/reset affordance correction. Root
owns capture, hosted CI, merge and image publication. No local validation runs.

Remaining work is still active: R5 preview/frontend completion and individual
retry/progress follow-up; R33 admin log scopes/channel search; R34 safe error
projection; R36 runtime/UI beyond persistence; R37 all-Key discovery/mapping
integration; R38 explicit out-of-scope diagnostics and opt-in scope extension;
R39 site/channel case-insensitive identity; R40 security-verification spinner;
R24 source integration/backfill/readers beyond its foundation; remaining R27
capabilities and R29 monitoring. R32 workspace migration remains last. Older
snapshots below are historical and do not describe the current published scope.

## Live continuation — 2026-11-26

- Narrow role-query correction is frozen, independently accepted and captured in2aab94859; string accessible-name equality is retained and the unsupported option removed. It is not yet pushed while root collects the running frontend test result. Current locale artifact45779078819 has an empty locales.patch at source3a974518b3eb7b3a98dca49ab18336d4d56c344e. A fresh read of all12 task Pi panes shows p2 resumed direct reads after /new; the others show frozen handoffs or fresh idle sessions, with no additional current100-turn error. No healthy session was reset.

- Root captured/pushed reviewed corrections63a2f132c, locale artifact2c81093cb and popup/dialog corrections444d313528fbd7a134832664127c5c29cf32a750. New CI45779078756 is running; backend job139769838798 succeeded. Frontend typecheck failed before formatter while tests continue; root identified unsupported getByRole exact:true in the newly corrected API-key helper and reopened only that test for pM to remove it after installed-declaration readback. Prior source review missed this typing issue; no green/release claim. Locale generation45779078819 completed successfully; its new artifact has not been inspected.

- Mapping fix hosted run45759710518 checked out728efaf05a088619360e4df2f7320dc502ad284a; remote commit APIs confirm its tree7121c161fdae62fbef7b1894e8a105364dae2aeb equals pushed a2c1c30dd. Named mapping add/edit/omit/null/clear/upstream-case regression passes SQLite3.50.4, MySQL8.4.13 and PostgreSQL16.16. Integration succeeded; backend vet/build/tests succeeded but two Go alignment hunks failed format. Frontend typecheck/lints/build succeeded; six assertions and nine formatting files failed. No deployment occurred.
- All nine frontend format corrections and the three observable test corrections (official-status ARIA-disabled behavior, trigger-owned API-key popover selection, asynchronous priority-dialog close completion) are frozen and independently SOURCE ACCEPTED. Root applied only the two logged Go alignment spaces. Fresh exact-capture hosted validation remains required.
- Locale artifact45759710516 from728efaf05 was independently SOURCE ACCEPTED and root applied its seven-file patch:38 additions per locale,266 total, no deletions. Missing/extras are0; aggregate untranslated counts remain ja8/ru9/zh7. No local locale generation ran.
- R37 direct append exact2 and server mask exact2 are SOURCE ACCEPTED, uncaptured. R5 backend6/frontend3 and R33 exact19 are SOURCE ACCEPTED, uncaptured. R24 probe/spec correction is SOURCE ACCEPTED; latest-release/race gate remains pending capture and hosted evidence. These slices are excluded from the urgent mapping-release corrections.
- R34 p2 again reached the actual100-turn limit after a tool-blocked handoff. Native takeover failed with agent-thread-limit before any work. Root created a fresh p2 session, selected gpt-6.1-sol/max and returned only the same R34 boundary; no duplicate writer or acceptance is implied. User confirmed site-wide and channel model-case controls, site default off; PRD/design record the policy without opening a new write slice.

- Urgent mapping-save P1 is isolated, independently SOURCE ACCEPTED and pushed as `a2c1c30dd347c121ca9096ebab9a83a15e7023cf`: the locked-row merge now copies nonnil ModelMapping and audits its field name. Regression TestChannelUpdatePersistsModelMappingAndPublishesCache exercises real PUT add/edit/omit/null/clear, SQL/cache and exact upstream casing. Other five omitted setters are deliberately excluded pending locked-delta sensitive authorization; R36 hunks remain uncommitted. CI45759710518 and locale45759710516 started for this head; no new pass or deployment claim.
- R33 exact19 is independently SOURCE ACCEPTED by native r33_review. R5 backend exact6 is independently SOURCE ACCEPTED by pH. R5 frontend cancellation correction is re-frozen and pK owns narrow rereview. These are source dispositions only and remain outside the urgent mapping capture.
- R24 gate review accepted the final race hunk but found legacy-ID comparison masking and a wrong focused schema-test selector; Tx callers also require explicit rollback-on-error wording. Native r24_gate_correction owns only the existing probe and foundation spec for these corrections. Foundation exact5 remains frozen; root retains workflow/index/Git ownership.
- Integration job139590507197 in run45724415621 completed SUCCESS at2026-11-26T00:18:54Z on merge9f053429d097cd17c4b0b7db236f53c2ea32dc88. Backend passed; frontend remains failed on seven assertions/four unhandled errors and formatting. Subsequent plugin correction ae1f00de5 and pricing card ed9f3dfbd are source-accepted captures, not covered by that run.
- R26 exact11, Playground hosted-correction exact3 and R25 exact5 are now independently SOURCE ACCEPTED. Locale script/static exact2 is frozen; historical comparison evidence is the root-produced b70f19e4e patch in TEMP. R5 frontend exact3 and R36 exact9 are explicitly frozen under separate reviews.
- R24 primary foundation exact5 is SOURCE ACCEPTED. Held migration workflow/probe and new foundation spec still require completion of source review and a hosted UsageEvent race selector before capture. Required fresh/released-upgrade-twice database evidence is pending.
- R5 backend reopened controller/suite belongs exclusively to native r5_backend_finish; other4 original paths remain frozen. R34 native takeover failed with429 before completion and will be explicitly reassigned; never infer READY. R33 saved exact19 still needs final readback/freeze.
- User explicitly requested checking all current-task Pi and using new sessions/model reset instead of repeated compaction. Root inspected12 Pi panes, reset9 evidenced failed/blocked sessions, and confirmed new session IDs plus gpt-6.1-sol/max. Healthy pH/pK/p3 sessions were preserved. Assignments are restored from exact saved boundaries; no repository state reset.
- New R37/R38/R39 requirements are authorized and recorded in PRD. Native multikey_add_inventory found ordinary append incorrectly gated by ledgerEnabled, the shared server preview exposing only sk- as prefix, and channel-wide-only model mapping. Its next read-only task prioritizes the reported deepseek-r1 to DeepSeek-R1 failure and optional case matching. R36 paths remain frozen until review/explicit handoff; no overlapping writes.

Authorization: user explicitly directed immediate FOUR Pi implementation and final commit/push/tag/CI-CD. Planning is recorded; no additional confirmation loop.
1. Activate parent and four children. Use existing Herdr Pi panes only; no detached supervisor.
2. Publish small interface contracts early (channel options, batch operation/export, public resource DTO and masked ranking).
3. Implement independent owned files and meaningful regression fixtures; source self-review only locally.
4. pi-ui integrates all English keys into seven locales; peers send keys and use existing strings meanwhile.
5. Root performs cross-worker source review, updates affected specs, commits only task paths and pushes fork branch.
6. GitHub Actions only: backend vet/build/tests, frontend checks/build/tests, independent modules if touched, SQLite/MySQL/PostgreSQL/migration matrices, Redis/race and ClickHouse when affected.
7. Fix cloud failures in owned scope. Once exact SHA green, fast-forward fork main and monitor release workflow/tag/multiarch assets. No production deployment.
8. Final report has exact cloud commands/results, unresolved limitations, release evidence and scoped archive/journal.
## Non-negotiable validation ban
Do not run local go test/build/vet, bun test/build/lint/tsc, gofmt checks, database setup/probes, benchmarks or browser-based local app validation. Do not install test tooling or launch databases. Read/edit/source review/Git/task bookkeeping/remote GitHub reads are allowed. Formatting must be written correctly in source and checked by cloud.

## R21 follow-up execution
Reuse the existing four Herdr workers: p2 owns concurrent-session policy production changes, p4 existing regression tests, p5 restart/signing-state investigation, p6 browser-restoration investigation. Root owns plans, CI, review, index, commits, push and release. Branch `fix/login-session-recovery-20261125` starts from released `370bd0847906fabc795d1140cb2d0987f3dd26a7`. Commit policy and any independently evidenced restoration fix separately, validate the exact final SHA in Actions, merge preserving history and verify published artifacts. Do not claim the live login incident resolved without observed evidence.

R21 confirmed follow-ups: p2 also owns usable-active predicates and refresh lookup error classification; p4 extends existing model/service/configuration/login-flow tests; p5 owns existing controller refresh regressions with the real-dialect fixture; p6 owns recoverable auth route guards and existing frontend regressions. Hosted CI explicitly runs `TestSecurityLogin|TestRefreshAuth` on SQLite/MySQL/PostgreSQL and focused session race checks. Existing full checks remain mandatory. Source review is not a validation pass.

## R3/R4/R22 correction ownership (2026-11-26)
Branch `fix/channel-card-invitation-layout-20261126` starts from merged login fix `37a417457fccc74bef82f4b2afb7729dada4439c`. Existing Pi workers: p4 invitation index and existing management tests; p5 card quota cell and existing quota tests; p6 card header/metrics and existing card-entry tests, then test-dialog layout/Key filtering/default streaming; p2 multi-key model-union and quota/user-binding control investigation followed by minimal reserved corrections. Root owns task/spec integration, index, commits, PR-triggered hosted validation, merge and release. Reuse components and seven-locale keys. Login release proceeds independently; no production deployment or runtime login-success claim.

Final multi-key ownership: p2 owns `controller/channel_key_allocations.go`, `multi-key-models-editor.tsx`, required API response types, and existing `multi-key-key-scope.test.tsx`. p4 owns existing `controller/channel_multi_key_test.go` regressions in parallel; p5 performs independent backend review after both freeze. pH exclusively owns `multi-key-manage-dialog.tsx` and existing `multi-key-manage-dialog.test.tsx`, including consuming the additive `channel_models` response. pJ reviews p6 test-dialog behavior/layout; pK reviews pH controls. Existing Herdr workers only; no duplicate file ownership. All checks remain hosted-only.

After pJ identified the positional-Key race, pJ owns the narrow manual-test backend identity contract in `controller/channel-test.go` and one existing diagnostic test file. p6 additionally owns `lib/channel-actions.ts` forwarding; p2 remains the sole writer of `api.ts` for the optional request types. Independent final review remains required; no production probes or local validation are authorized.

## R8/R17 follow-up ownership (2026-11-26)
- pK: dashboard API panel/item and existing panel tests; extract the existing instance resource display into `web/src/components/resource-usage.tsx` and replace only its import/calls in the instance panel. pM: API-info settings dropdown and its existing test. pH: independent source review after freeze. p6: seven-locale additions through the sanctioned writer; no local formatter/sync/check under the execution ban.
- p2: request-content compression model/store changes plus existing regressions. p4: absent retention default3 and its existing option matrix; cleanup throughput is research only, with no cleanup implementation. p5: independent backend review after freeze. Preserve active tasks/writers, explicit settings and real relational/ClickHouse hosted coverage.
- Root: capture accepted changes, update concrete contracts and CI evidence, push coherent snapshots, then merge/release only after all required exact-SHA hosted checks pass. No production operations.

## R23/R9/R24 continuation

- pM owns the sidebar visibility11paths and sole Profile page removal; pK owns Wallet/card callback/existing Wallet regression. pH reviews sidebar and p4 reviewed Wallet. p5 owns only the existing option matrix extension after p4 handoff for sidebar bool persistence; no check-in setting change.
- p6 owns the bounded ranking mask and separate admin-chart cache fix plus seven locales; pH reviews after explicit freeze. Policy: ADMIN+ROOT/self full, others partial, anonymous ***.
- R24 child remains planning pending pJ write/backfill architecture and p4 critique. p2 backend and p6 frontend inventories support root's PRD/design/implementation manifests. No worker has R24 production ownership yet. All current source freezes and the no-local-validation restriction continue.

## R25–R32 ownership and current queue

- pM diagnostic two-test corrections are frozen/source accepted; root captures them and triggers hosted checks. pM next owns only common-log-mobile-row.tsx, common-log-mobile-card.tsx and the existing mobile-card.test.tsx for R31.
- pJ owns only common-logs-stats.tsx, logs-match-mode-toggle.tsx and the two existing corresponding suites for R30/R31. Its R24 source draft stays frozen. pH independently reviews each completed frozen slice.
- p2 owns only parent research/error-monitoring-inventory.md for R29. Its compaction completed and the exact assignment was resumed; root monitors recovery.
- p5 owns only parent research/model-dropdown-inventory.md for R28; R24 schema stays frozen pending final narrow addenda. Generic selectors must not be sorted globally.
- Spare target Pi w6E:p2/p3/p4 own respectively parent research/official-status-followup.md, active-subscriptions-inventory.md and playground-followup-inventory.md in the OLD source directory. They must explicitly use old paths and leave target files/Git untouched. Root assigns production slices after source findings; no duplicate ownership.
- p4 closes R24 pending-index/collation and practical retained-row cleanup gates in its research review. p6 finishes R9 frozen handoff then frontend statistics inventory; pH reviews R9 only after freeze. pK reads exact GitHub job/merge-SHA evidence without local validation.
- Root records all requests, coordinates research-to-implementation boundaries, performs scoped commits/push and hosted gating. R32 migration/branch/artifact/installer audit follows completion of all product tasks. No branch deletion, production cleanup or target pull is performed during current feature work.

## Current handoffs — 2026-11-26

- R9 nine paths and R30/R31 seven mobile-log paths passed independent source review and are frozen for root capture; execution evidence remains pending.
- R28 p5 owns shared comparator/MultiSelect and Keys; pK owns the seven announced channel caller/test paths. w6E:p5 has stopped all six plugin/metadata writes mid-review and now does read-only Playground mapping. p6 exclusively takes over those six paths, corrects verified test defects and freezes for pH review. No prior acceptance or test pass is implied.
- R5 pM owns fingerprint frontend feature and existing tests, after exact-path announcement; `research/fingerprint-reference-layout.md` captures the user-pasted reference. pJ owns only `research/channel-endpoints-key-protocols.md` for R36 and the shared fingerprint contract until explicit production handoff. Prior pM mobile and pJ stats files stay frozen.
- p4 compaction completion was observed; resume the practical R24 retained-row cleanup/index/capacity review. pH monitors actual worker failures between independent source reviews and avoids duplicate recovery commands.
- R25 w6E:p2 implements only old-source official-status service/API/UI and existing tests after exact-path announcement. Eight cards, fetched-at freshness and independently available bounded incident history follow the accepted research; unresolved machine sources stay honest.
- R26 w6E:p3 owns old-source `model/subscription.go`, existing `model/subscription_reset_test.go`, `controller/subscription.go`, one subscription-admin GET route line, subscription feature API/types/index and the new active-users table/feature suite. One row per active unexpired subscription; no schema/billing writes. Root ACKed this exact boundary. New locale keys are coordinated through root.
- Root pushed accepted R9/mobile/diagnostic/sidebar commits at `fae102150490d2bdf874c9dec4b5796cea7efefd`; hosted run `45706527989` is pending. Never apply older run results to that tree.

## Recovery and concrete contracts — continuation

- pH corrected and froze only Wallet copy, dashboard charts and Profile settings existing test suites for run `45706527989` failures. Root independently read the three diffs; a fresh hosted run must verify them. The run tested merge `c40253e7da9c257a319e55fed19cda4de244649e`, not uncommitted corrections.
- Root observed p6 had no filesystem tools, issued `/new`, observed a new session, restored `gpt-6.1-sol` and resent the exact R28 six-file assignment. Subsequent source-tool access was observed; this supersedes the stale completion `1094`. Target p4 recovery `/compact` is pending until actual completion is observed; no duplicate recovery prompt is authorized.
- pJ now owns R5 backend controller, service job/registry, preview helper and one channel-router route plus the existing service fingerprint and controller channel-test-request suites. Any additional production path needs a concrete announcement. Use the accepted opaque preview contract in design.md; broader R36 production is not yet assigned.
- pM exclusively owns `fingerprint.tsx`, `fingerprint-api.ts` and existing `fingerprint.test.tsx`, including real pre-run prompts, reshuffle, stable Key and endpoint integration. Shared channel files remain peer-owned. Send all new strings to root for seven-locale integration.
- w6E:p5 owns only `setting/system_setting/update_source.go` and its existing test. Root owns Dockerfile and the fork/reusable release workflows for the agreed build-default symbol, followed by independent source review and hosted checks.
- Playground research is frozen. On observed p4 recovery completion, resume only P1 empty-project rendering and the single panel create trigger above New chat in announced feature/existing-test paths. P3 attachment limits and server admission need their own boundary; P4 spool hardening remains separate.

## Accepted captures and activated foundation — 2026-11-26

- Root captured independently reviewed R28 shared/Keys 11 paths in `667d2c5be` with the model-selection spec. pK compact completion was observed and its exact seven channel paths resumed. p6 froze six plugin/metadata paths after correcting the blank-row parser fixture to `{}`; pH owns independent source review.
- Root captured independently reviewed R19 six paths in `0cad9022d` with the build/release spec. The linked test is present and checks the actual linker value. p6 now owns the narrow update-checker UI wording plus one announced existing suite; no locale ownership. w6E:p5 has R33 bounded source/path planning, not new R19 writes.
- R24 child PRD/design/manifests are converged and task activated. w5D:p4 owns exactly `model/usage_event.go`, `model/usage_event_apply.go`, the primary migration call in `model/main.go`, one new `model/usage_event_test.go`, and existing `model/migration_dialector_test.go`. No source/read/backfill/cleanup ownership is implied. Root parent remains the coordination task.
- R34 p2 owns `relaykit/types/error.go`, `service/error.go`, `service/relay_error.go`, marker-only parsed upstream branches in `relay/channel/openai/{relay-openai.go,relay_responses.go,chat_via_responses.go}` and `relay/channel/claude/relay_responses.go`; Gemini path must be named before edits. Tests: one new consolidated `relaykit/types/error_test.go`, existing `controller/relay_error_log_test.go`.
- Root owns temporary hosted locale generation and all seven-locale integration. Local generators/formatters/checks remain prohibited. All new strings are submitted to root, and only explicitly frozen/reviewed snapshots are captured for hosted checks.

## Current source reviews and hosted corrections

- p6's R28 six plugin/metadata paths and pK's seven channel paths are frozen for independent pH review. Keep shared comparator/MultiSelect APIs unchanged while reviewing callers.
- pK independently reviewed p6's R19 two-file UI slice. Production preserves saved values and drafts; one test needs `waitFor` after the mounted status-cache change. p6 alone reopens that existing suite, then pK verifies the saved correction before acceptance.
- Run `45714039810` checks out merge `1e1e0c845eac32d386d4af2a59b96fa3c60c03e6` for head `b543392cfcd175fe23a05b90a46e99d0dd2d13db`. Root assigned p6 only the 12 exact hosted frontend formatting diffs and p5 only the failed existing API-key model-limit regression for diagnosis. No local formatter/test execution is permitted.
- The temporary locale generation batch intentionally includes six keys: four current fingerprint keys and the two frozen R19 strings. The five-language description awaits SDK evidence. The artifact workflow disables checkout credential persistence, uses read-only permissions, and emits only a seven-locale patch plus source SHA/report; root reviews provenance and content before applying.
- Root migration-probe changes requiring the four usage tables remain uncaptured until the R24 foundation exists and is independently reviewed. No target or cleanup work is unblocked by partial hosted results.

## Review capture and follow-up ownership

- pH accepted pK's seven channel model-ordering paths; root captured them with the model-selection contract in `4fc4bfcc6`. pH accepted p5's one-test cache-notification correction, captured in `0e6a2c416`. Root independently reviewed p6's exact 12 hosted formatting corrections and captured them in `5af999d3e`.
- R19's existing update-checking suite now waits for the refreshed placeholder and preserved draft after the cache update; p6 explicitly froze it. Independent final readback remains required before the separate UI capture.
- Run `45716932767` checks out merge `9e3e73405268b5a72136ba2d44f2230af79888c4` for head `8f0e275a4c8263501a8a1713625480cac7fcd7fb`. Its frontend reports TS2532 in the channel plugin fixture and a plugin-form submit assertion failure, plus the already-assigned model-limit race. w6E:p4 exclusively owns corrections in the existing channel-plugin-config-fields and request-plugin-config-form suites; all production plugin paths remain frozen.
- Its backend reports the quota-refund assertion in `TestChannelWaitRouting/quota_429_does_not_queue`. pK's read-only trace finds asynchronous refund completion and teardown risks; no production accounting change or weakened balance assertion is authorized from that evidence.
- R33 w6E:p5 owns the two announced log model/controller paths, ten usage-log frontend paths and seven existing test files, including `billing-source-visibility.test.tsx`. Admin `user_scope=all|self|others` derives its subject from authenticated context; all page/count/stat/export paths share the predicate. Viewer role controls columns/preferences independently. Channel-name search reuses bounded authorized options. No mobile or R24 storage/read-cutover/cleanup ownership is implied.
- Playground P1 six files are frozen for pH review. P3 attachment work remains a separate bounded plan until complete source/provider/test ownership is assigned.
- Root owns R34 `i18n/keys.go` and three backend locale YAML files plus hosted frontend translation inputs. The script's next batch adds the SDK-confirmed fingerprint language sentence and five fixed upstream-error sentences. These dictionary edits are not generated locale files or execution evidence.

## Continuation after nested refund diagnosis

- R19 UI was independently accepted and captured in `5da5a50fe`; Playground P1 was independently accepted and captured in `cbc316bcf`. These supersede their pending-review notes above. R28 captures remain frozen except the two explicitly assigned hosted plugin-test corrections.
- Run `45716932767` integration job `139565406540` completed successfully on merge `9e3e73405268b5a72136ba2d44f2230af79888c4`; frontend/backend failures keep the overall run failed. Do not apply its result to later local commits or worker drafts.
- pK traced a nested wallet cache worker that can outlive restored SQL balances and read restored fixture globals. Root authorized exactly `model/user.go` for Redis-enabled guards around the three existing Increase/Decrease quota cache scheduling sites, plus only `TestChannelWaitRouting` in existing `controller/channel_pin_retry_test.go` for exact persisted balance waits and cleanup order. No global drain, service completion API, billing arithmetic or SQL change. Readback, freeze and independent source review precede capture; hosted execution remains the subsequent acceptance gate.
- pH's actual compaction completed; root resumed independent read-only review of the five saved locale machinery/backend message paths, followed by R26's frozen nine paths. pM resumed its incomplete R5 three-path slice. w6E:p3 hit an observed turn limit; one compaction is pending before installer planning resumes.
- pH accepted the exact five locale delta paths; root captured them in `895e2ebaa`. The new six frontend dictionary entries still require hosted generation and artifact review. The earlier six are already generated/captured, not pending local generation.
- pK's same two-file correction explicitly disables quota warnings in the wait-routing fixture: successful requests otherwise enqueue a separate notification worker that can read restored cache globals. Keep normal exact charge assertions; cleanup-only recovery may inspect persisted consume rows after cancel/close and handler completion. No quota-notification production change is authorized.
- R34 host localization/correlation additionally reserves `controller/relay.go`, `controller/responses_websocket.go`, `relay/requestplugin/reply.go`, and `relay/requestplugin/report.go`. `PrepareSafeClientError` belongs in already-owned `service/error.go`; the manually emitted SSE site in already-owned `relay/channel/openai/chat_via_responses.go` consumes it. Marked upstream errors retain original internal Err/RelayError/status/code; only fixed client projection is localized and correlated. Plugin report error remains the client-visible projection. Gemini changes are restricted to parsed PromptFeedback BlockReason in `relay-gemini.go` and `relay_responses.go`, not native passthrough or local empty-candidate errors. Existing two-suite test boundary remains unchanged.
- R26 independent review found a missing PAT route declaration and clipped pagination. p3 now owns two additional paths, `middleware/access_token_routes.go` and existing `router/access_token_scope_test.go`, for the `billing:read` declaration and regression. Its existing active-users table/suite use the shared footer portal and an integrated tab regression. Entire eleven-path slice requires re-freeze and review; the original nine-path slice is not accepted.
- p4 handed off the hosted plugin-test blocker without guessing a production fix. Its saved complete-config TS2532 assertion is frozen; pK now exclusively owns the same two existing plugin/channel test files for fresh diagnosis or minimal observable hosted diagnostics, preserving the failing onSave/payload/order expectations. Refund exact2 remains frozen under pH review.
- R36 p5 owns the first persistence/management slice only: `relaykit/dto/channel_settings.go`, `model/channel.go`, `model/channel_key_entry.go`, `model/channel_publication.go`, `controller/channel.go`, `controller/channel_key_allocations.go`, and existing controller create/multi-key/authz suites. The final JSON and stable-Key mutation contract is in design.md. No model/main or migration test edits (R24-owned), runtime/adaptor/controller relay/frontend/fingerprint writes are authorized to this slice. It cannot be captured as a functioning R36 feature until runtime/UI integration and independent review follow.

## Latest review and recovery dispositions

- pH independently accepted the frozen refund exact2. Root captured those files plus the backend fixture-boundary spec/index in `e3f89b145`; no hosted result covers this correction yet.
- pK froze the two existing plugin suites and pH independently accepted them. Root captured the TS2532 whole-config correction and additional selected-group/model/native-form diagnostics with the model-selection spec in `b143ace59`. Historical `onSave=0` remains unexplained; this capture preserves the failing exact callback/payload assertions for the next hosted run.
- R26 owner re-froze all eleven files after the PAT declaration and footer corrections. Native `r26_source_review` owns this independent read-only review; pH must not duplicate it.
- R25 source ownership transferred from stopped w6E:p2 to w6E:p4 for the same five files. Future timestamp boundaries, history-ID deduplication, coherent fixtures and final locale inventory remain required before freeze.
- Root observed pM's actual 100-turn error, then its pending compaction, then completion from 442,593 tokens. Root resumed only its same three R5 frontend files; subsequent source reads are visible. No `/new` or duplicate recovery was sent. R5 backend and frontend remain unfrozen.
- R35, R33, R34, R36 persistence and R24 foundation remain with their existing exact owners. Held migration workflow/probe edits are excluded from the next accepted capture. No local validation, target operations or cleanup is authorized.

## Hosted follow-up and ownership after `fa8d52fe2`

- Root pushed `fa8d52fe2b0972f3ddaa8fc535dfe40b0beb6edb`. CI `45724415621` checks out merge `9f053429d097cd17c4b0b7db236f53c2ea32dc88`, tree `60375a32d0d69847f024a1c5b33df6a41a4a4827`, matching the head tree. Backend job `139590507054` succeeded; frontend `139590506698` failed formatting and seven assertions plus four unhandled ScrollArea `getAnimations` errors. Typecheck, both lints and build succeeded. Full integration remains pending at this record.
- Locale run `45724415656` succeeded on the same source/tree. pH accepted exactly six additions in each of seven JSON files; root applied/captured the artifact in `966f46b18`. Existing untranslated counts remain ja7/ru8/zh6. pH separately accepted the next seven R5 backend-error dictionary entries, captured in `b70f19e4e`; those49 entries still await hosted generation.
- Root independently matched pK's exact three formatting corrections to hosted hunks and captured them in `732232aca`. Request-plugin and Playground formatting hunks stay with their respective next corrections, not included in that capture.
- R33 stopped after exact-text edit failures. Root preserved model/controller snapshots in TEMP, read the same live hashes twice and found the validation already present; concurrent authorship was not established. The original full R33 path set was handed exclusively back to w6E:p5. After an observed100-turn error, compaction completed from475,587 tokens and root resumed that same set. Explicit empty scope must not collapse into omitted/all.
- R34 p2 and R36 p5 hit observed100-turn errors. Root sent one compaction each, observed completions from521,222 and557,375 tokens respectively, then resumed their same saved boundaries. No duplicate recovery or new scope was implied.
- R25 old owner w6E:p4 explicitly stopped all five writes after incomplete fixture work. Native `r25_finish` now solely owns the same5; w6E:p4 has read-only Playground hosted-failure diagnosis until an exact correction boundary is approved.
- R35 p6 stopped with partial source and then an observed100-turn error. Native `r35_finish` completed and froze the same3 files; native `r26_source_review` now independently reviews R35. p6 has no R35 write ownership.
- R26's original reviewer found cached administrator rows surviving same-session demotion. Owner corrected only its API/table/existing suite and re-froze exact11; pH owns final read-only review. Prior PAT/footer fixes remain accepted.
- R5 backend froze6, but pH found arbitrary upstream code/type leaking through public retained errors. pJ reopens only its controller/existing controller suite for fixed-safe projection and malicious-refusal replay coverage. Root also requires the preview-to-final-wire prompt contract to account for configured overrides before claiming actual dispatched prompt identity. UI remains separately owned/unfrozen.
- Native `plugin_submit_trace` traces the hosted plugin chip assertion without production edits; an empty queried list is not proof of state loss or a submit root cause. Preserve callback/full ordered payload assertions and establish actual component interaction first.

## Hotfix capture and ongoing ownership — 2026-11-27

Root captured and pushed hotfix `94862b39f` in the isolated multikey-hotfix worktree, PR #5. Independent source review accepted the redaction/append/quota-explanation and card/address slices. CI run `45870882405` checks merge `448075a9aec2af33cd88f983da2d1a6f6958c494`: backend succeeded; frontend typecheck, lint, all 3724 tests across 289 files and build succeeded, but two exact test-formatting hunks failed. Integration was still running at this record. pJ owns only those logged wraps; no local executable validation is authorized.

Separate console-followups ownership: p6 fixes repeated allocation reconciliation/transaction, whole-channel budget reads and per-row username lookups in get_key_status; native mapping author fixes missing legacy alias abilities and p6 wires its one startup call. pH fixes the bounded secure-verification spinner; p3 finishes fingerprint progress/single-probe retry; p5 finishes R33 privacy/abort/search corrections. Native mapping exact3 is not accepted until existing-data repair and real-dialect checks pass. R5 is partial until its new tests and independent freeze review complete.

Other isolated worktrees: p2 finishes R34 safe upstream sinks, p5 finishes R36 runtime/UI, p4 integrates durable statistics, w6E:p2 owns R37/R38 discovery/manual diagnostics. Root exclusively owns Git, hosted CI, merge and release. Original dirty source stays preserved. Confirmed tool-loss/interrupted sessions were restarted with /new and gpt-6.1-sol restored; terminal working status alone is never completion evidence. Watchtower deployment authorization remains in effect; publish the reviewed image after required hosted checks, without SSH or production configuration changes.
## R33 isolated capture — 2026-11-27

The exact 19 production/existing-test files were independently source accepted,
then applied cleanly to `feat/admin-log-user-scopes-20261127` based on main
9f918c414. The shared administrator scope predicate, explicit invalid-query
rejection, viewer-tier projection/preferences, masked authorized channel search
and cancellation are included. Numeric summaries remain log-backed until the
separate R24 source/read/backfill work completes. No local validation ran; hosted
checks on the captured commit are required before merge/release.

## Key-list and mapped capability hotfix capture — 2026-11-27

Independent source review accepted the frozen mapped capability predicate, existing-data ability repair and status-read changes. Root isolated the nine code/test/CI paths in `fix/channel-key-read-routing-20261127`, based on the earlier UI hotfix head `2fd6fb27a`; original and followup worktrees stay preserved. The status read retains a short owner-row transaction for a coherent authoritative snapshot, then performs page-only quota enrichment and one username batch outside that transaction. Migrated reads no longer reconcile/publish on every request. The source query bound is at most five SELECTs (four with cached activation), not a measured cross-node latency result.

Public request identity, bound-user rules and empty allowlist denial remain intact while Key capability matching accepts the exact configured upstream mapping target. Startup repairs only affected derived ability rows for existing channels, including cache-disabled routing, and the new regression executes the actual migration twice. The existing three-database CI selector now includes `TestMultiKeyStatusRead*`; a dedicated SQLite/MySQL/PostgreSQL loop covers `TestRepairChannelMappedKeyAbilitiesUpgrade`. No local validation ran; database compatibility and runtime acceptance remain pending this exact captured tree's hosted checks. The reviewer noted that cancellation coverage currently targets the snapshot helper rather than the complete HTTP enrichment phase; source propagation was accepted.

The separate UI hotfix PR #5 run `45873282970` has successful backend and frontend jobs at `2fd6fb27a`; integration remains running. R40 exact13 is now frozen and independently reviewing. R33 and R5 production remain with their owners until final freezes; no partial source is included in this hotfix.
