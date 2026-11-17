# Final Trellis check

Task: `11-16-channel-concurrency-limit`. Reviewer: `/root/final_check`.

## Gate

Local quality gate: **PASS**. Review is complete with no remaining confirmed product defect. The implementation owners have applied the review fixes below; focused regressions, final affected-package tests and final root build/vet pass. Main may proceed with the authorized commit and push to run CI. Release approval additionally requires the configured CI jobs to pass against real MySQL, PostgreSQL and Redis and under the Linux race detector; local skips are not passing integration results.

This reviewer performed no Git writes. The main session owns commit, push, release tags and observed CI/CD completion.

## Findings (fixed)

- `model/option.go`, `model/request_policy_test.go`: repeated startup hydration could present default retry values in the snapshot while retaining earlier runtime globals. Main synchronized the runtime defaults before loading saved options and added runtime-versus-snapshot assertions. Saved zero and the one-time legacy-range migration remain preserved.
- `service/relay_error.go`, `service/request_policy.go`: client cancellation propagated to the upstream could be classified as an upstream failure and trigger a custom automatic-disable rule. The capacity owner now records cancellation locally, skips channel disable/error-log handling, and excludes canceled partial streams from channel-failure accounting.
- `service/channel_capacity.go`, `service/channel_select.go`, `service/channel_select_auto_groups_test.go`: auto-group selection prepared the next group's retry state before admission. A busy candidate could therefore skip available channels in its current group or reset a positive remaining retry budget. Selection now snapshots the actual candidate's group, retry counter, pending reset and affinity offset; busy traversal restores that snapshot only for the matching channel. Regressions cover zero and positive budgets, two busy same-group candidates, a third available candidate, and moving to the next group only after exhaustion.
- `service/http_client.go`, `service/http_transport_policy.go` and generation call sites: resolving the first-byte timeout in the generic client applied the new deadline to background polling, artifact fetching, discovery and balance requests. The timeout owner added an explicit relay client entry point for ordinary generation, AWS SDK, administrator tests and task/Midjourney submission. Generic clients retain their prior timeout policy; tests cover actual delayed-response behavior and cache isolation.
- `relay/channel/jimeng/adaptor.go`, `relay/channel/coze/relay-coze.go`: three requests lacked the incoming attempt context, so cancellation or lease loss could not stop their network work. Requests now use `NewRequestWithContext`; focused controller regressions cover these paths.
- `service/channel_capacity.go`, `service/channel_capacity_test.go`: Pi identified, and this reviewer independently confirmed, that a detached native-SSE retry from a limited channel to an unlimited channel inherited the released previous lease through context values. Unlimited admission now masks that marker while preserving parent cancellation. The regression verifies that the new unlimited attempt is not canceled by the old lease. Active limited detached submissions still retain coordinator-loss cancellation.

All fixes were applied by the assigned owners to avoid concurrent file edits. No unresolved design change was silently introduced by the reviewer.

## Findings (not fixed)

- Required real-engine verification is pending CI. Local SQLite is available; local MySQL/PostgreSQL/Redis fixtures are absent. The CI integration job provisions MySQL 8.4, PostgreSQL 16 and Redis 7.4, records their exact versions, runs the persistence/admission regressions and fails when a configured fixture is unavailable. Local race checks are unavailable with the installed CGO-disabled environment; Linux CI supplies the race detector.
- Full frontend lint has 143 pre-existing errors in unrelated unchanged files, and the full copyright check reports 24 pre-existing unchanged files. The UI owner recorded this evidence in `report-ui.md`; changed-file lint and affected-file format checks pass. These baseline issues were left outside the task.
- No remaining confirmed product-code finding is left unfixed. External CI validation and release completion are not claimed by this report.

## Rejected finding

The Herdr Pi suggestion that `SaturatedChannelIDs` has a Responses WebSocket data race is unsupported by the actual call paths. `newResponsesWSRequestRunner` calls `Engine.ServeHTTP` and its callback synchronously. `runRequest` calls that runner and `runCall` synchronously; only after return does its deferred terminal-message writer execute. Each response.create receives a fresh Gin context, and `tryReserveCurrent` prevents simultaneous calls on the same session. Socket readers pass messages through channels; the lease callback accesses connection state and shutdown, not the selection slices. Slice mutation and selection are owned by the serial request worker. A lock on the separate events field is not evidence of concurrent slice access. No speculative locking was added; the CI race gate remains required.

## Verification

Directly executed by this reviewer:

- `bun run typecheck`: PASS, 1,471 files across two projects.
- Changed/new TS and TSX files with `node node_modules/oxlint/bin/oxlint -c .oxlintrc.json`: PASS.
- `actionlint -shellcheck=`: PASS.
- `gofmt -l` on changed/new Go files: PASS, no output.
- `git diff --check`: PASS after final capacity and timeout edits.

Observed owner/main evidence:

- Frontend affected tests: PASS, three files / 166 tests; final accessibility subset: PASS, 13 cases. Production build, changed-file format and seven-locale synchronization: PASS.
- Root package tests other than controller: PASS in the main session's full run. Complete controller package: PASS, 210.633 seconds with a ten-minute timeout.
- Final capacity boundary regressions: PASS, service 5.177 seconds, controller 1.195 seconds, relay 5.606 seconds; final candidate-match selection check: PASS, service 0.112 seconds.
- Final timeout-scope/context regressions: PASS, service 16.1 seconds, relay/channel 4.387 seconds, controller 13.164 seconds; affected-package vet: PASS.
- Independent `GOWORK=off go build ./...` in relaykit: PASS. Final root `go build ./...` and `go vet ./...`: PASS, observed by main in session 7560.
- SQLite matrix covers fresh/legacy/custom/saved-zero options, two startup passes, one-time migration marker rollback, runtime values and channel setting round trips. SQLite version recorded by the persistence Pi: 3.50.4. MySQL/PostgreSQL and real Redis results remain pending CI.

Review covered controller retry/settlement, body cleanup, SDK retry suppression, full stream and WebSocket lease lifetimes, detached native SSE, local health classification, routing restrictions, inheritance/explicit-zero settings, database migration boundaries and frontend form permissions/serialization. No additional confirmed product findings remain.

## Final validation update

Final complete affected packages: service PASS (9.769 seconds), AWS PASS (5.825 seconds), jsplugin PASS (12.672 seconds), relay/channel PASS on final rerun. The existing HTTP/2 GOAWAY test initially encountered a Windows `wsarecv` transport error; its private test transport does not use the new helper. Both the isolated test and complete relay/channel package passed on rerun. Main's final root build/vet and independent relaykit build also passed.

**Local review approved for the authorized Conventional Commit and push.** Main must observe the real database/Redis matrix, Linux race job and remaining CI/CD jobs passing before announcing release completion. No further local full-controller rerun is required absent a new change or failure.
