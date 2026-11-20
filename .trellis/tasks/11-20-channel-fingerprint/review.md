# R22 independent review — controller and relay diagnostic fixes

Reviewed 2026-11-20. Root owns staging and commits. This report is not a release
approval: service/assets/UI were still being updated by their owner at handoff.

## Findings fixed

- `controller/channel_fingerprint.go`: validate selected-channel model membership
  and supported, bounded text protocols before admission. Empty endpoint defaults
  to Chat; GET/cancel require job/channel route ownership before mutation. Failed
  probes retain known usage; errors exclude arbitrary upstream bodies/credentials.
- `relay/diagnostic_text.go`: reuse `resolveTextUpstreamEndpoint` and
  `textRequestViaResponses` for channel diagnostics, without user quota settlement.
- `controller/channel-test.go`: install that dispatcher only for explicit probe
  prompts. Both ordinary and converted paths share the existing channel-test
  settlement and consume log. Preserve known usage on errors and lease cleanup.
- `fingerprintBoundedAdaptor`: inspect final wire JSON after conversion/plugins/
  overrides. Limit output to the remaining per-probe allowance (maximum 4096),
  force one Chat/Gemini candidate, reject tool overrides and unsupported adaptors.
  Native Codex drops `max_output_tokens` and is deliberately unsupported rather
  than advertising an unenforceable output limit.
- `controller/channel_fingerprint_reply.go`: collect all Anthropic text blocks,
  support Chat text content arrays, and exclude Gemini thought parts.

## Verification

- Focused controller tests passed after the final controller changes:
  `go test -p 1 ./controller -run 'TestFingerprint|TestCountChallengeNumbers|TestChannelTestOpenAIChatCompatibility' -count=1`
  (1.651 seconds). Cases cover actual Chat and Messages to `/v1/responses` wire
  requests, output override clamping, one settlement/log with reported usage,
  empty-reply usage preservation, real HTTP cancellation/capacity release,
  route ownership, and jobs surviving the starting HTTP context cancellation.
- Final-wire tests cover OpenAI Chat/reasoning/Responses, Anthropic, Gemini,
  missing/invalid output limits, multiple candidates and tool overrides.
- `go test -p 1 ./pkg/lmfp -count=1` passed during the owner's asset revision.
- Frontend fingerprint scoped oxlint and 3 tests passed; global typecheck passed
  for 1588 files. These checks precede the owner's remaining UI changes.
- Last `go vet -p 1 ./controller ./relay` was blocked by concurrent
  `controller/token_test.go:618,653` referencing `context` without importing it.
  Earlier controller focused builds passed. Re-run after other owners finish.
- No schema, query, or persistence contract was changed by this reviewer. The
  controller integration fixture used SQLite 3.50.4, not a three-engine proof.
- No real credentials or external paid probes were used; upstreams were local
  `httptest` servers. Root must include all concurrent dependencies in final gates.

## Findings requiring owner completion/recheck

- Fixed input reservation is not an absolute total-token bound when channels
  inject prompts. Root approved the explicit contract: final per-request output
  hard limit plus cumulative known/estimated input accounting, with a clear UI
  statement that input estimates/upstream reports can exceed the total budget.
  UI, service accounting and unknown-usage behavior must agree.
  Unknown usage must consume the full reservation/output allowance or stop
  further dispatch; recording only 512 input tokens is insufficient.
- Service job timeout validation, timer cleanup, admission cap and budget
  rejection tests were being edited. A temporary nil cancel fixture panic was
  reported to the owner; rerun the full focused suites after handoff.
- Asset caching now checks both hashes and writes atomically; inspect the final
  change and add a corrupted-bank cache regression. Require a fixed meaningful
  scoring fixture, not only loading or randomly generated challenges.
- At last UI inspection: polling used overlapping `setInterval`, a 404 left a
  running job unable to restart, selection only loaded the first 200 channels,
  and the route lacked the frontend admin guard. These were sent to the owner.
- Root must complete all seven frontend locales, including fixed controller
  messages, dynamic job/probe errors and result limitations/attribution. The
  current controller additions need these English keys:
  `the selected model is not available on this channel` and
  `fingerprint checks require a supported protocol with an enforceable output token limit`.

## Diagnostic contract to retain

### Scope

Bounded admin text probes reuse normal channel eligibility, endpoint selection,
capacity and response conversions while retaining channel-test settlement.

### Signatures

`relay.RelayTextDiagnostic(c, info, adaptor, request)` returns usage, handled and
API error. `handled == false` means continue the existing adaptor path; callers
must not dispatch again after handled is true. The prober accepts a service-owned
`maxOutputTokens uint` allowance.

### Contracts

The dispatcher never charges a user account. The controller records one channel
test consume log. Cancellation propagates to the actual HTTP request and releases
the selected channel's lease. Requests cannot increase the output allowance through
channel overrides. Backend job routes bind channel ID and job ID together.

### Validation/error matrix

| Condition | Behavior |
| --- | --- |
| Chat/Messages input and Responses-only channel | Convert through existing Responses transport |
| Responses input and Chat-only channel | Reject without upstream dispatch |
| Unknown channel model or unbounded adaptor | Reject before starting job |
| Override raises output tokens or candidate count | Clamp output / force single candidate |
| Override adds hosted tools | Reject before upstream dispatch |
| Route channel differs from job channel | 404; no cancellation mutation |
| Model reply is empty with usage | Preserve usage and the existing test log; scoring fails |

### Good/base/bad cases

Good: a 300-output-token probe remains 300 after a 9000-token override and the
Responses reply is converted into the originally requested client format.
Base: ordinary channel tests without a probe prompt keep their existing path.
Bad: calling normal TextHelper/ResponsesHelper here adds user billing beside test
settlement; direct adaptor dispatch ignores Responses-only channel policy.

### Tests required

Extend `controller/channel_test_request_test.go`; assert actual wire path/body,
usage and log count, HTTP cancellation and capacity, ownership and failed usage.

### Wrong versus correct

Wrong: set max tokens only before channel overrides and assume it is a cap.
Correct: validate the effective wire payload immediately before the adaptor sends it.

## R22 owner handoff (service, assets, UI) — 2026-11-20

Handed back to root for staging. Root owns commits and the seven locale files.

### Files released

- `pkg/lmfp/lmfp.go`, `lmfp_test.go`, `NOTICE.md`, `data/unified_bank.json`,
  `data/shared_detector.bin` (5,443,668 B; committed directly, no LFS).
- `service/channel_fingerprint.go`, `channel_fingerprint_job.go`,
  `channel_fingerprint_test.go`.
- `web/src/features/service-status/fingerprint.tsx`, `fingerprint-api.ts`,
  `__tests__/fingerprint.test.tsx`.
- `web/src/routes/_authenticated/service-status/fingerprint.tsx`.
- `web/src/hooks/use-sidebar-data.ts`, `web/src/hooks/use-sidebar-config.ts`.
- Shared hunk: `go.mod` (1 added line), `go.sum` (2 added lines). The router and
  controller hunks stayed with the reviewer.

### Final contract

- Per-request wire output hard limit `min(remaining - 512, 4096)`; the run stops
  once `remaining - 512 < 256`. Unknown upstream usage is charged
  `512 + maxOutputTokens`. This is a bounded allowance, not a mathematical
  total-token cap; the UI states that input is estimated and that a channel can
  inject prompts the gateway cannot see, so the real total can exceed the budget.
- `tokenBudget`: 0 uses env/default 24000; negative or above 200000 is rejected.
  Probe timeout: 0 uses env, clamped to 1..600 s; above 10 minutes is rejected.
  The run uses `context.WithoutCancel(parent)` with a whole-run timeout, and the
  deferred cleanup cancels it and closes `done`, releasing the timers.
- Admission cap is 4 active runs. Expiry and retention only touch terminal runs,
  so a running job always keeps its cancel handle.

### Verification (this handoff)

| Command | Result |
| --- | --- |
| `go test -p 1 ./pkg/lmfp/ ./service/ -count=1` | ok 0.194s / ok 19.834s |
| `go test -p 1 ./service/ -run 'TestFingerprint|TestStartChannelFingerprint' -count=1 -v` | 12 passed |
| `go vet ./service/ ./pkg/lmfp/` and `go vet -copylocks ...` | clean |
| `gofmt -l service/channel_fingerprint_test.go` | clean |
| `bun run test -- src/features/service-status/__tests__/fingerprint.test.tsx --maxWorkers=2` | 3 passed |
| `bun run lint` / `bun run typecheck` on owned files | clean |
| `go build ./controller/ ./router/` | exit 0 |

`-race` cannot run in this toolchain: cgo has no compiler (`gcc` not found). The
concurrency, admission and cancellation contracts are covered by the end-to-end
tests instead of the race detector.

### vet handback

The reviewer owned `service/channel_fingerprint_test.go` and handed it back. The
only remaining statement that copied a mutex was a redundant `t.Cleanup` that
reset the process-wide registry from a test that only builds a local registry.
That reset is gone; `isolateFingerprintRegistry` already swaps fields under the
lock and copies no `fingerprintRegistry` value.

### Still owed by root or R2

- Apply the 24 keys in
  `D:/AppData/Local/Temp/new-api-browser-check/r22-translations.json` to all
  seven locale files, and regenerate `src/routeTree.gen.ts` for the new route if
  it churns on commit.
- No real channel probe was executed; wire-level billing/capacity behaviour is
  reviewer-tested only. Browser smoke of the page belongs to R2.

## Owner follow-up — 2026-11-20 (after root page read)

Four page defects found by root, all fixed in `web/src/features/service-status/fingerprint.tsx`:

1. `startMutation.onSuccess` now clears `pollStopped` beside the error state, so a run
   started after an expired one polls its own job. Regression: new test
   "polls a new run after the previous one stopped" asserts a second start triggers
   `getChannelFingerprint(7, 'job-2', …)` and shows the new result.
2. The form gated inputs and selects on `running`; it now uses
   `controlsDisabled = (running && !pollStopped) || startMutation.isPending`, so an
   operator can change the channel/model and restart after a stopped poll. The same
   test asserts the model field and Start button are enabled in that state.
3. The label said "Output token budget" while the service charges input plus output.
   It is now "Estimated token budget"; test "shows the budget that is spent on input
   and output together" asserts the new label and the absence of the old one. The
   help text already stated the input estimate and the possible overrun.
4. Raw English was rendered from `job.error`, `probe.error`, `analysis.Reason`,
   `job.limitation`, and `job.attribution`. Static sentences written by the gateway
   or the SDK now go through `t(...)`; provider/scoring messages stay verbatim.
   Translated: no reply, out of time, unscorable, cancelled before or during dispatch,
   consecutive-number rejection, received/ranked sample reasons, closed-set
   confidence, calibration mismatch, no valid samples, and strict-mode mismatch.
   Limitation and attribution render their fixed sentences through `t(...)` (with the
   server value as the key, so a reworded notice falls back to the readable server
   text). Known-entry states are unchanged.

### Verification after the follow-up

| Command | Result |
| --- | --- |
| `bun run test -- src/features/service-status/__tests__/fingerprint.test.tsx --maxWorkers=2` | 5 passed |
| `bun run lint` / `bun run typecheck` | clean |

Translations: `D:/AppData/Local/Temp/new-api-browser-check/r22-translations.json` now
holds **38 keys × 7 locales** (identical key sets). Root still applies them.

## R2 final independent review and repair

Reviewer `/root/r2_finish` read the final service, SDK wrapper/assets, frontend
page/API, route guard and sidebar entries. Controller behavior remains covered
by the preceding review. Root approved the explicit estimated total budget
contract; this review did not broaden that contract or redesign persistence.

Concrete findings fixed after owner release:

- `service/channel_fingerprint.go`: adding upstream `int` usage directly could
  produce negative or overflowing spend and admit another probe. Negative input
  or output now consumes the full unknown-usage grant, reported negative fields
  are normalized to zero, a single report saturates at `math.MaxInt`, and the
  remaining-budget comparison cannot underflow. `channel_fingerprint_job.go`
  saturates cumulative spend as well.
- Budget exhaustion left queued probes on a terminal job. Remaining probes now
  become cancelled, while the job retains its budget-exhaustion explanation.
- `fingerprint.tsx`: an old cancellation response could replace a newer run
  started after polling stopped. The callback now applies only when the current
  job still has the cancelled response's ID.
- `pkg/lmfp/NOTICE.md`: corrected the environment-variable description from an
  output-only budget to the actual estimated input/output budget.

Extended existing tests with negative input/output, single-report overflow,
cumulative overflow, terminal remaining probes and a delayed old cancellation
response after a new run. No test files or locale keys were added.

Before the user's instruction to stop local verification, the reviewer ran
`go test -p 1 ./pkg/lmfp ./service -run 'Test.*Fingerprint|TestVendored|TestOperatorBank|TestLoadFails|TestAnalyze' -count=1`:
**PASS** (0.193s / 0.093s). This result predates the repairs above. A frontend
test run had also started before that instruction; no result is claimed here.

**The final repairs and regression additions have not been tested, built,
linted, formatted with tools, or browser-checked locally**, following the user's
latest instruction relayed by root. Root must run their focused tests, typecheck,
lint and final integration checks in cloud CI. No release pass is claimed for
the final edited tree. All owned files are released for the R22 commit.

The remaining budget-stop copy issue is now repaired: `translateRunMessage`
recognizes the backend sentence and interpolates `{{budget}}`. Seven-language
source data is in
`D:/AppData/Local/Temp/new-api-browser-check/r22-budget-translations.json`.
Merge that one key with the owner's 38-key file; root owns applying the resulting
39 keys to locale files. No additional concrete runtime defect was identified in
the bounded source review.
The SDK reference bank stays local with both checksums verified; job admission
and retention keep active cancel handles, run/probe contexts are bounded and
cleaned up, and frontend access is admin-guarded. Runtime probe usage remains an
estimate when the upstream does not report it, as disclosed by the page.

## Final CI handoff

The working `.github/workflows/ci.yml` now includes
`TestFingerprintResponsesOnlyChannelProbe` in the existing
`TEST_CHANNEL_SQL_DSN` MySQL/PostgreSQL loop, and `Fingerprint` in the race
selection. The SQL fixture shares main/log databases, reports the actual engine
version, and explicitly disables Redis. Therefore this step verifies real SQL
settlement/logging and cancellation, not real Redis coordination. The separate
capacity integration suite retains responsibility for Redis coordination.

R22-only CI snapshot:
`D:/AppData/Local/Temp/new-api-r22-only-ci.yml`, based on committed `e19368c54`.
It contains only the fingerprint SQL test selection, `./controller` added to the
race packages, and `Fingerprint` added to the race regex. Uncommitted R8 channel
waiting/streaming changes remain in the working file and are absent from this
snapshot. Root must stage the snapshot without overwriting the shared file.

Final read-only controller trace reconfirmed: the explicit diagnostic prompt
travels through request context into the existing channel-test request builder;
the wrapper bounds the final converted/overridden wire request; Responses-only
policy uses the shared conversion path; known usage is retained across failures;
the existing test settlement/log executes once; lease release is deferred after
acquisition and the probe cancellation context reaches transport. Unsupported
or unbounded provider paths are rejected. No further production edits were made
during this trace. The final CI and translation changes were not executed locally.
