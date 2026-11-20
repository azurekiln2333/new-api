# R17 independent review — 2026-11-20

Reviewer: `/root/r2_finish`; root owns all staging and commits. Scope: structured goal-turn classification, channel constraints and the Responses WebSocket reuse path. The UI and real HTTP route matrix remain with their implementation owners.

## Fixed findings

- `relay/responses_websocket.go`: per-turn policy used to look up the locked channel, silently skip policy on lookup failure, then perform a second independent lookup in `restoreConnectionContext`. A transient first failure followed by success could bypass the restriction. Moved filter validation into `restoreConnectionContext` immediately after its successful channel lookup, using the exact snapshot that is restored. A lookup error rejects locally, and a disallowed turn on an established socket instructs the client to reconnect for selection.
- `relay/responses_websocket.go`: missing metadata on every later turn previously inherited the handshake header, incorrectly classifying ordinary later turns as goal-triggered. Only the first admitted/parsed turn may use the compatibility handshake header. Subsequent turns classify their own metadata; absent metadata is unknown. Root approved this connection contract.
- `service/channel_goal.go`: a non-string canonical descriptor was treated as a raw object and could trigger policy despite not matching the documented wire shape. Wrong types now produce `Malformed` unknown, without falling back to a conflicting header. Root approved strict canonical-string handling.

## Regression coverage

Extended existing files only:

- `service/channel_goal_test.go`: canonical object/numeric wrong types and malformed WS descriptor precedence.
- `relay/responses_websocket_test.go`: ordinary turn eligible, goal turn refused with reconnect instruction, subsequent ordinary turn eligible, and missing channel lookup refused.
- `controller/responses_websocket_test.go`: reused the existing real socket and accounting fixture, with an optional header-capable wrapper preserving all old fixture calls. A single five-turn connection verifies first header rejection without upstream traffic; explicit ordinary descriptor success; later missing descriptor success; later goal rejection without upstream traffic; and ordinary continuation after refusal. Exactly three accepted turns are billed, all on one upstream connection.

The HTTP `TestGoalTurnPolicyRouting` validates pins/all-denied/eligible alternate/ordinary tools and prose, plus retry exclusion. pJ completed all five cases with memory cache disabled and enabled (10 subtests), passing in 6.667s. The refused channel receives zero upstream requests, including after another channel fails.

## Verification

Toolchain: portable Go at `D:/AppData/Local/Temp/new-api-build-tools/go/bin/go.exe`; `GOMAXPROCS=2`.

- `go test -p 1 ./service ./relay -run 'Test.*GoalTurn|TestRestoreConnectionContext|TestSelectResponsesWSChannel|TestResponsesWSChannelRouting' -count=1`: passed after the WS fixes (service 0.078s; relay 2.102s). Strict-type update re-run pending below.
- Scoped `git diff --check`: passed. Modified Go files formatted with portable `gofmt`.
- Controller WS/HTTP matrix run was blocked by parallel edits: `controller/relay.go` called missing `Channel.AllowsModel`/`AllowsGroup`; `controller/token_test.go` lacked a `context` import. Owners/root were notified; this is not a test pass.
- Final package vet and controller tests remain pending until compilation settles. No frontend changes in this reviewer scope; frontend lint/typecheck are not applicable here.

Final local re-runs after the concurrent compilation blockers cleared:

- `go test -p 1 ./service ./relay -run 'Test.*GoalTurn|TestRestoreConnectionContext|TestSelectResponsesWSChannel|TestResponsesWSChannelRouting' -count=1`: **passed**, including strict canonical wrong-type parsing (service 5.138s; relay 5.522s).
- `go test -p 1 ./controller -run 'TestResponsesWebSocketGoalPolicyIsPerTurn|TestResponsesWebSocketReusesConnectionAndSettlesEachRequest|TestGoalTurnPolicyRouting' -count=1`: **passed** (3.903s).
- `go vet -p 1 ./service ./relay ./controller`: **passed**.

These final runs supersede the transient compilation blockers. They do not substitute for pJ's expanded cache/retry matrix or the external engine gates below. Reviewer-owned files are released to root.

## Contract limitations and release gates

### Frontend form roundtrip follow-up

Root requested a final frontend guard. Extended the existing
`web/src/features/channels/components/__tests__/channel-configuration.test.tsx`
with three `deny_goal` absent/false/true cases. Each verifies loaded default,
schema parsing, create and update payloads, reload, and the opposite toggle
state. Production frontend code was reviewed without further changes.

- `bun run test src/features/channels/components/__tests__/channel-configuration.test.tsx --maxWorkers=2`:
  **PASS**, 147 tests (including the three new cases), 99.55s.
- Scoped oxlint for the modified test: **PASS**.
- `bun check -b`: **PASS**, 1,590 files / 2 projects after the test addition.

The added create/update transform imports and the `goal restriction %s survives
channel create, update, reload and toggle` test block belong to R17; other
existing changes in that shared test file belong to their original owners.

## Contract limitations and release gates

`turn_trigger="goal"` in canonical Codex metadata identifies recognized goal-triggered turns. It is client-asserted and does not prove that all work associated with an active goal is detected. Manual steering, children, older clients or stripped metadata can be unknown. No text/tool-name heuristic is added. The pinned official source and exact evidence limits are in `../11-19-playground-capabilities/research/goal-routing.md`.

Existing sockets retain their channel, so a newly disallowed turn is rejected locally with reconnect guidance; no hidden switch of the established upstream connection occurs. Fresh eligible selection may choose another channel, while explicit pins remain pins.

The real controller fixture supports `TEST_RESPONSES_SQL_DSN` / `TEST_RESPONSES_LOG_SQL_DSN`. Root CI should run `TestGoalTurnPolicyRouting|TestResponsesWebSocketGoalPolicyIsPerTurn` against real MySQL and PostgreSQL as well as SQLite, and record exact engine versions/results. SQLite local execution does not establish all-database compatibility. Any changed `relaykit` public settings must also pass that module's `GOWORK=off go build ./...` in the root final gate.

## Root staged-tree verification

Root exported staged tree `4d1f73831ffe4c793847209bb9673459c07c249f` to an
isolated temporary directory, excluding parallel R7/R8/key/fingerprint changes.
`GOMAXPROCS=2 go test -p 1 ./service ./relay ./controller -run
'Test.*GoalTurn|TestRestoreConnectionContext|TestResponsesWebSocketGoalPolicyIsPerTurn|TestResponsesWebSocketReusesConnectionAndSettlesEachRequest'
-count=1` passed (2.084s / 0.117s / 6.748s). Independent relaykit
`GOWORK=off go build -p 1 ./...` passed. The staged CI adds the real SQL goal
matrix; external results remain a release gate.

The existing frontend channel-configuration suite now covers `deny_goal`
absent/false/true through schema, create, update, reload and inverse toggle.
Author and root each passed all 147 tests; author also passed full typecheck
(1590 files) and scoped lint. Root staged-tree `go vet -p 1 ./service ./relay
./controller` passed.
