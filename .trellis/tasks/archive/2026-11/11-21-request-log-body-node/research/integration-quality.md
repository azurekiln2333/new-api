# Integration quality check outside the core packages

2026-11-22, pi-logs, active parent
`.trellis/tasks/11-21-channel-key-allocation-log-diagnostics`.

Read-only integration check requested by root. No production, fixture, billing,
completed-fixture, workflow or dependency files were edited. No commit/stage,
release or detached worker was created. Root owns the image runtime correction;
relay/channel fixture edits were allowed only for task-caused failures, which
were not established for the HTTP/2 failure.

## 1. Remaining root-module suite

Portable Go 1.26.10 on Windows amd64, `GOWORK=off`. No `TEST_*` external-service
variables were set in this check's process; external tests with missing DSNs may
skip. This does not replace the previously executed relational matrix.

Actual PowerShell command:

```powershell
$env:GOWORK = 'off'
$go = 'D:/AppData/Local/Temp/new-api-pi-20261121/tools/go/bin/go.exe'
$packages = @(& $go list ./... | Where-Object {
  $_ -notmatch '^github.com/QuantumNous/new-api/(controller|model|service|router|middleware)(/|$)'
})
& $go test -p 1 -count=1 @packages
& $go vet @packages
```

Results: **81 package paths**, **44 passed**, **36 had no test files**,
**1 failed** (`relay/channel`). Its sole failing test in this full run was
`TestUpstreamGetBody_HTTP2RetryAfterGracefulGoAway_PassThrough` at
`relay/channel/api_request_getbody_test.go:540`, Windows TCP `wsarecv` connection
aborted/reset before successful retry. Other packages, including relay,
relay/helper, all tested provider/task adaptors, request plugins, billingexpr,
perf_metrics and settings passed. Remaining-package `go vet` passed with empty
output.

Evidence files under `D:/AppData/Local/Temp/new-api-pi-20261121/`:

- `pi-logs-remaining-packages.log`
- `pi-logs-integration-vet-remaining.log`

This is a failing suite result, not a full-green integration claim.

## 2. HTTP/2 failures compared to the exact baseline

Targeted command, unchanged test assertions:

```text
GOWORK=off go test -p 1 -count=1 -v ./relay/channel -run '^TestUpstreamGetBody_HTTP2'
```

- Current tree first targeted run: **all four tests passed**, 5.326s
  (`pi-logs-current-http2.log`).
- Current tree later complete remaining-suite run: graceful GOAWAY test failed
  with Windows connection reset/abort; the no-GetBody test passed in that run.
- Exact baseline `47a5639c56ce8b4f57c6a4e21462d060e2f4d59f`, exported outside the
  checkout with `git archive` and tested with the **same Go 1.26.10**:
  graceful GOAWAY and `TestUpstreamGetBody_HTTP2CannotRetryWithoutGetBody` both
  failed with the same Windows `wsarecv` abort family. In the latter test the
  transport error arrived instead of the asserted `cannot retry err` message.
  Both positive REFUSED_STREAM replay tests passed.

Baseline preparation (no worktree reset/checkout):

```text
git archive --format=tar --output=<temp>/baseline-47a5639c5.tar 47a5639c5
tar.exe -xf <temp>/baseline-47a5639c5.tar -C <temp>/baseline-47a5639c5
```

`api_request_getbody_test.go`, `controller/relay_stream_test.go`, body storage,
`go.mod` and `go.sum` have no diff against that baseline. Both use
`golang.org/x/net v0.55.0`.

The raw fixture writes GOAWAY/RST_STREAM and immediately closes the TCP socket
without waiting for the peer to drain outstanding protocol frames. Windows can
surface an abort before the client receives the intended HTTP/2 control frame.
This is the leading fixture explanation, not a separately proven OS/network
fix. An observational frame trace on the current tree showed successful GOAWAY
receipt, EOF and retry; the no-GetBody test received REFUSED_STREAM and its exact
expected replay error. Both passed in that trace run:

```text
GOWORK=off GODEBUG=http2debug=2 go test -p 1 -count=1 -v ./relay/channel \
  -run '^TestUpstreamGetBody_HTTP2(RetryAfterGracefulGoAway_PassThrough|CannotRetryWithoutGetBody)$'
```

Debug logging changes scheduling, so its pass is not a flake fix. These commands
use synthetic test payloads only. No transport retry contract, assertion or
fixture was changed: the failures reproduce before this task and root limited
fixture edits to task-caused issues.

Evidence:
`pi-logs-baseline-http2.log`, `pi-logs-http2-frame-evidence.log`.

## 3. Confirmed task-caused image keepalive regression

```text
GOWORK=off go test -p 1 -count=1 -v ./controller \
  -run '^TestRelayKeepAliveDuringImagePreparation$'
```

- **Current tree failed**, 4.00s test duration (9.087s package): client times out
  waiting for response headers while its image download is blocked.
- **Exact baseline passed**, 2.02s test duration (6.660s package), same Go/runtime
  and unchanged test. It received both required `: PING` frames.

Read-only source trace:

- Baseline `controller/relay.go` starts Claude stream keepalive before
  `relay.PrepareRequestBilling`.
- Current code moved `StartStreamKeepAlive` **after** `PrepareRequestBilling`,
  `PrepareTieredBillingForSelectedGroup` and `reserveSelectedChannelBudget`.
- `PrepareRequestBilling` calls `service.EstimateRequestToken`, which downloads
  the test image during preparation. The image handler deliberately waits for
  release, so execution never reaches the new heartbeat start before the
  client's four-second timeout.

This is a runtime ordering regression introduced by the current change, not a
missing-options fixture or a new timeout setting. Root/billing were notified
with the precise diff. Preserve budget admission and pre-upstream heartbeat
contracts together; do not merely lengthen the test timeout or bypass budgets.
No billing/controller production change was made by this reviewer.

Separate pre-existing fixture issue: after release the baseline retries the image
fetch and its handler may panic on a repeated `close(imageRequested)`. Baseline
still passes its original ping/error assertions. That cleanup/retry issue is not
the cause of the current missing pre-preparation heartbeat.

Evidence: `pi-logs-current-image-keepalive.log`,
`pi-logs-baseline-image-keepalive.log`. Status here describes the checked tree;
root owns any subsequent fix and targeted re-verification.

## 4. Independent modules

Executed independently, not via the root workspace:

```text
cd relaykit
GOWORK=off go test -p 1 -count=1 ./...
GOWORK=off go build ./...
cd ../tokenkit
GOWORK=off go test -p 1 -count=1 ./...
GOWORK=off go build ./...
```

**Both modules' tests and builds passed.** No module or public API edits.
Evidence: `pi-logs-integration-relaykit.log`,
`pi-logs-integration-tokenkit.log`; successful builds produced no output.

## 5. ClickHouse 25.8 validation path for the eventual commit

Existing `.github/workflows/ci.yml`, job **`relay_policy_integration`**, already:

- runs `clickhouse/clickhouse-server:25.8`, native port 9000;
- creates database `relay_policy` and configures the test user with access
  management enabled (the storage matrix creates/drops its own databases);
- provides `TEST_CLICKHOUSE_DSN` to the job;
- records actual `SELECT version()` output;
- checks out `${{ github.sha }}`, not a moving branch;
- executes `go test -p 1 -count=1 -v ./model ./service ./controller`, which includes
  `TestLogRequestDetailStorageDatabaseMatrix/clickhouse`;
- executes `go test -race -p 1 -count=1 ./model -run 'TestChannelBudget|TestLogRequestDetail'`.

The CI workflow is triggered by a push to `merge/upstream`, relevant PR events
or a workflow call; **it has no `workflow_dispatch` trigger**, so do not assume
`gh workflow run` can validate this dirty change set. After root creates the
actual integration commit, require a run whose head SHA equals that commit and
non-skipped ClickHouse output. No such commit/run was created by this worker.

Focused Linux/CI selector with an explicit nonempty-DSN guard:

```sh
: "${TEST_CLICKHOUSE_DSN:?configure the real ClickHouse 25.8 test service}"
GOWORK=off go test -p 1 -count=1 -v ./model \
  -run '^TestLogRequestDetailStorageDatabaseMatrix$/^clickhouse$'
GOWORK=off CGO_ENABLED=1 go test -race -p 1 -count=1 -v ./model \
  -run '^TestLogRequestDetailStorageDatabaseMatrix$/^clickhouse$'
```

The configured subtest must log actual ClickHouse 25.8.x and execute
`independent/fresh`, `independent/released_upgrade`, cleanup-during-link and
metadata-only-task cases. Connection/schema/write errors must fail; a skip is
not acceptance. This selector checks the existing real creation/read/link/
synchronous-delete matrix; it does **not** by itself add native TTL expiry,
duplicate uncertain inserts or ClickHouse mutation-timeout recovery assertions
that the current matrix does not implement. Those remain explicit coverage
limits and cannot be claimed passed from the service declaration or SQLite
recovery tests.

Local status: no Docker/Podman executable available; recorded WSL virtualization
blocker remains. No local ClickHouse server was started, and **no ClickHouse pass
is claimed**. The workflow path/selector is identified for root, not executed.
