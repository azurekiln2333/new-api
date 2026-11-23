# Runtime accounting review — unresolved until verified

Root review of the in-progress tree. These are release blockers, not completed work.

1. `controller/relay.go` releases the attempt hold immediately after an attempt error, while its outer deferred `RefundFailedRequestBilling` can still apply a violation fee. Trace the real order and keep the final charged attempt attributable; a released reservation must not make the charged fee disappear from local budgets. Test the actual relay failure path.
2. `service/billing.go` returns a `BillingSession.Settle` error before local-budget settlement. `BillingSession.Settle` can already have committed funding when the token adjustment fails. The attempt must retain/count its charged funding despite the token error; subsequent release must not refund that budget. Test a token-write failure after successful funding settlement.
3. `service/task_billing.go` ignores `RefundTaskChannelBudget` failure when quota was nonzero and then clears refund state. `FinishTaskChannelBudget` currently settles directly, without persisting a retry target. A database failure must leave durable budget reconciliation state without replaying the user's refund. Check `BudgetPending`: adding the private-data field alone does not provide this invariant.
4. `QueueChannelBudgetSettlement` returns success for any revision at or below the current settled revision. Confirm replayed same revisions with different totals cannot erase a previously recorded desired adjustment or appear successful while conflicting with authoritative accounting.
5. `HasLiveChannelBudgetReservationsTx` originally checked pending/open reservations only. Dedup must also exclude still-active tasks, including legacy tasks whose only association is channel + `PrivateData.Key`, and reservations already settled while their task is still active.

Activation deadlock repair is now verified: `ActiveChannelBudgetTx(tx)` uses the caller's connection, `newBudgetView(account, active)` does no I/O, batched read checks activation once, and rollback of a transaction-local activation does not leak into process cache. `TestChannelBudgetDatabaseMatrix` passed real SQLite 3.50.4, MySQL 8.4.13 and PostgreSQL 16.16 including a single-connection regression. Evidence: external `root-budget-matrix.log`.

Separate integration failures: `root-all-tests.log` contains missing Option/allocation schemas in existing fixtures, blank-key compatibility and scheduled-provider regressions. Those still require repair and a complete rerun. A focused matrix pass is not final acceptance.

## Integration disposition — 2026-11-23

The list above records the initial review, not the current release state.
The final runtime fixtures now cover fee-bearing failures without early release,
funding committed before token failure, durable task adjustment/reconciliation,
replayed/conflicting revisions, and live-task dedup protection. See
`controller/channel_budget_runtime_test.go`, `model/channel_budget_test.go`,
`controller/relay_task_plugin_test.go` and `controller/channel_multi_key_test.go`.

Latest real task matrix: SQLite 3.50.4 / MySQL 8.4.13 / PostgreSQL 16.16 PASS;
SQL commit-success-then-error and real rollback are injected at the test-only
connection boundary. Readable committed outcomes complete success once; unknown
outcomes preserve funding and holds; known rollback releases both. Task outcome
lookups are bounded, scoped by expected user/channel, and reject duplicate
matching public IDs. Subscription overage is retained. Token cache projection
uses only the credential actually charged, including deletion/rotation cases.

The earlier fixture compilation/schema residuals have been repaired. Local
whole-package model/service pass; controller full-package final result remains
in the parent report. Realtime is explicitly restricted before upgrade while
the ledger is active, verified on all three engines and with race detection.
An additional startup ordering fix checks the batch environment before main
initializes its runtime flag; its complete three-engine matrix passes.

The independent reviewers' hypothesis of a half-committed SQL transaction was
withdrawn. The wallet `quota <= maximum + min(delta, 0)` guard prevents credit
overflow; it is not a balance-sufficiency admission check after acceptance.
Cross-store cache publication remains non-atomic and is reported as a limitation.
ClickHouse and exact-SHA cloud CI remain release gates, not local pass claims.
