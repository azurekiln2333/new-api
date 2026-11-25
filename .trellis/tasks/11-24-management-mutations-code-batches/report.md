# Management mutations and code batches — pi-billing report

Status: source implementation complete and frozen for integration. No local tests, builds, vet, lint, typecheck, formatting, database probes, or browser checks were run. GitHub Actions remains the validation gate.

## Delivered

### Durable audit

- `audit_event_outbox` stores safe actor/action snapshots in the primary database; workers claim with per-row leases, retry with bounded exponential backoff, and retain failed events indefinitely.
- Channel create/update and invitation/redemption batch create write business data and the audit obligation in one primary-database transaction. The actor snapshot is a value object; the model layer does not retain Gin context.
- Other management actions enqueue after their business commit and keep a post-commit crash window. If enqueue fails, a bounded direct log-sink fallback uses the same event identity. A simultaneous failure of both persistence paths cannot roll back an already committed nontransactional action.
- Delivery is at-least-once. Relational log storage deduplicates by event id; ClickHouse does not provide an equivalent uniqueness guarantee.

### Idempotent code creation

- `CreateCodeBatch` atomically commits code rows, operation identity/result, and audit outbox event.
- `operation_id` supports retry recovery. Replay checks actor, operation type, and request digest before returning the original keys; mismatches return HTTP 409.
- Expiry validation runs only on a fresh insertion path, so an identical replay can recover after expiry. A changed expiry still conflicts through the request digest.
- Redemption `batch_id` retains its separate shared-claim-limit meaning.

### Shared invitation/redemption operations

- Both pages use shared export/history dialogs for per-row, selected, current-page, filtered-across-pages, and historical batch export.
- Fresh and recovered create results route through the actor-bound server export using `operation_id`; the old redemption-only export dialog was removed.
- Exports reject malformed or oversized explicit selections, enforce actor ownership for historical batches, preserve database-specific key quoting, and cap results rather than silently changing the requested selection.
- Invitation and redemption response clients retain `operation_id`, `batch_id`, and `replayed` metadata. The existing mutation drawer tests cover the shared redemption flow; the existing invitation management tests cover create and export interactions.

## Regression and database coverage written

`model/code_operation_outbox_test.go` adds coverage for fresh and upgrade-shaped SQLite/MySQL/PostgreSQL fixtures, repeated migration, uniqueness and data preservation, concurrent retry convergence, lost-ack replay, code recovery authorization, export parity/ownership, safe audit fallback metadata, and bounded retry backoff. The optional ClickHouse case checks event delivery without claiming deduplication.

`controller/invitation_test.go` and `controller/redemption_batch_test.go` cover replay after expiry, validation, conflicts, and safe audit behavior. The web suite now covers redemption creation opening the shared export flow and downloading by operation id; it also verifies the required `CodeExportProvider` context in mutation tests.

The fixture migration test constructs the released schema shape from released field/tag definitions; it does not start the released binary. A separate startup migration against a database created by released commit `53a71f88d5b568847cd7233a08f934824dcb22d3` must still be run before claiming released-schema upgrade compatibility. GitHub CI should exercise SQLite, MySQL 8.4, PostgreSQL 16 and the configured ClickHouse 25.8 service, and report the exact results.

## Validation status and remaining limit

No validation results are claimed until the GitHub Actions run for the pushed commit finishes. Source review identified no further blockers in this slice. The material guarantee boundary remains: transaction-integrated channel and code-creation paths couple mutation and audit atomically; nontransactional management actions retain the documented post-commit crash window.
