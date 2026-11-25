# Preimplementation baseline review

Date: 2026-11-20. Baseline: `ed21eb3d4030a639e2f44bf2c79ced9e6dc251bd` plus `.git/task-baselines/playground-capabilities/preimplementation.patch`, as recorded in `worktree-baseline.json`. Active feature edits were excluded from the baseline audit. Root later authorized the focused fix below; no staging, commits, or publishing were performed by this reviewer.

## Confirmed blocker and focused repair

The baseline moved per-key model/user remapping into `UpdateChannelLocked`, but `controller.UpdateChannel` subsequently called `Channel.Update` with `ChannelInfo` copied before the locked write. Replacing `[A, B]` with `[B, A]` first correctly moved B's model scope and user binding to index 0, then the configuration write restored the old index 1. This could attach a private key's binding to another key and expose the intended private key as public.

The fix adds a narrow `Channel.UpdateConfig` persistence entry that omits both `key` and `channel_info`. The HTTP edit merges only explicit `multi_key_mode`, `to_multi_key`, and key changes into the current state under `UpdateChannelLocked`; it normalizes the multi-key size there. The existing `Channel.Update` entry remains available for explicit scope-management actions. Both reload into an empty `Channel`, preventing JSON decoding from retaining entries from stale maps. Existing credential submission checks, server authorization, redacted audit behavior, and refresh-safe key locking remain in place.

Owned repair files:

- `controller/channel.go`: main edit's locked key/mode merge and `UpdateConfig` call.
- `model/channel.go`: `UpdateConfig`, shared persistence/reload path.
- `controller/channel_multi_key_test.go`: one added table test in the existing file; reorder keeps the correct key selection, and a deterministic database callback modifies scopes/users/status immediately before the configuration write to prove that even a newer stale snapshot is not written back. Explicit mode edits are covered. Existing single-to-multi and explicit scope/model-sync tests are included in the focused run.

The separate `ValidateChannelSupportedEndpoints` call in `model/channel.go:ValidateSettings` belongs to R4, coordinated with `release_research`; exclude that hunk from the merge-baseline commit.

## Workflow audit

Child audit reviewed only the three saved workflow diffs. No introduced blocker was found:

- Release PGO lookups now use `github.com/MoeForge/moejs`, matching the baseline dependency and Dockerfile.
- The fork Docker guard agrees with the existing `docker-fork.yml` publishing path.
- `make test` resolves the tracked lowercase `makefile`.
- The explicit `TEST_TASK_DB_DIALECT` loop activates MySQL/PostgreSQL scheduled-channel lock tests.

Verification gap: the broad `./model ./service ./controller` run with `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN` does not itself exercise the external-database branch in the controller task fixture. `controller/relay_task_plugin_test.go` defaults to SQLite unless `TEST_TASK_DB_DIALECT` is set. Run `TestExecuteTaskSubmission` and `TestImmediateTaskSettlementDatabase` explicitly with each dialect where these restored contracts are relied upon. Channel-management regression fixtures separately read `TEST_CHANNEL_SQL_DSN`.

## Review scope and remaining validation

Reviewed baseline channel key/config mutation, remapping and credential-copy integration, request-plugin lifecycle/signing restoration, option/request-policy merge repair, prior repair reports, and workflow diffs. This is not a claim of exhaustive review of every locale or frontend hunk. The old completion report was explicitly superseded; its earlier partial test results do not establish the final tree's quality.

No new schema is added by this focused repair. Real SQLite, MySQL and PostgreSQL validation remains mandatory for the changed persistence behavior. Root owns the external database matrix and final exact-commit checks. The baseline's `relaykit/dto/channel_settings.go` changes require an independent `GOWORK=off go build ./...` in relaykit, not only a root build.

Security references read: official OWASP CheatSheetSeries commit `c2a1e3e32cec12f27be0a3b1e771ce51288a363e`, Authentication and Session Management cheat sheets, obtained through the fresh temporary clone after direct HTTP endpoints failed. Applicable controls are server-enforced authorization, preserving credential rotation and revocation state, and excluding credentials from output/logs. No account authentication/session protocol is changed by the focused repair and no comprehensive ASVS compliance claim is made.

## Suggested commit/check grouping

1. Merge repair: restored backend/plugin/key/request-policy contracts, compatible frontend restoration, existing regression repairs, and their seven locale updates. Keep dependent DTO/host/adaptor changes together so no intermediate commit loses the contract.
2. Workflow repair: the CI/database-selection and release PGO/fork publication changes.
3. Task/spec bookkeeping: superseded completion report, reopened task evidence and merge-release contract.
4. New R1-R25 features: independently from the saved baseline; `model/channel.go` needs hunk separation for R4 as noted above.

For each publishable final tree: root build/vet/tests; independent affected modules; frontend typecheck/test/build; required real database/Redis checks; then exact GitHub run SHA and actual binary/checksum/image-manifest outputs. A tag push or a skipped publish job is not release verification.

## Focused verification log

- Modified Go files: gofmt passed; scoped `git diff --check` passed.
- Command: `GOWORK=off GOMAXPROCS=1 GOMEMLIMIT=512MiB go test -p 1 ./controller -run 'TestUpdateChannelKeepsCurrentKeyState|TestUpdateChannelConvertsSingleKeyChannel|TestMultiKeyManageKeepsKeyScopesOnTheirKey|TestMultiKeyModelSyncPreviewAndApply' -count=1 -v` with the official temporary Go 1.26.10 toolchain.
- Attempt 1 stopped at an unrelated in-progress R4 unused import in `relay/compatible_handler.go`; owning agent removed it.
- Attempt 2 stopped at an unrelated in-progress unused import in `controller/channel_concurrency.go`; root was notified.
- Attempt 3 passed all four selected test functions, including the three new regression cases and four single-to-multi cases: `ok github.com/QuantumNous/new-api/controller 6.564s`, real SQLite `3.50.4`.
- Log: `D:/AppData/Local/Temp/new-api-channel-state-regression.log`. Existing minimal fixtures log missing `options` / `system_tasks` tables during unrelated initialization/sync notifications; the focused assertions pass. This run does not establish background sync behavior or MySQL/PostgreSQL compatibility.
