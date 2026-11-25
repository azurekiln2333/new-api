# Shared management audit source freeze — 2026-11-25

Active task: `.trellis/tasks/11-24-console-responsiveness-diagnostics`.
Owner: pi-billing. Source/regression edits only; no local tests, builds, vet,
lint, typechecks, formatting, DB probes, installs, stage, commit or push.

## Correction

The earlier claim that middleware events lose their empty EventId was wrong.
`RecordAuditLogDurable` calls `snapshotAuditLog`, which generates EventId and
RequestId when absent. Empty-ID transaction drafts remain an intentional no-op;
that does not describe this middleware call chain.

## Scope and files

Production:
- `middleware/audit.go`: admin fallback and API-token operations use durable
  primary enqueue. PAT writes use it too; PAT reads retain their existing direct
  sink path. Admin/token actor, role, auth method, token fingerprint, event id and
  event time are captured before handlers run. Admin operation, token security
  and PAT access events keep separate event identities and categories.
- `model/audit_log.go`: common snapshot plus `RecordAuditLogDurable`, without the
  synchronous username lookup used by `RecordAuditLog`.
- `model/audit_outbox.go`: optional Other/CreatedAt draft values and versioned
  `audit_other_v1` envelope within existing ParamsJson. No model columns changed.
- `model/audit_outbox_record.go`: original event time preserved in enqueue and
  bounded direct fallback; both failures return an error and safe log category.
- `model/audit_outbox_worker.go`: complete prepared AuditOther restored, including
  root metadata, request/route params and failure reason. Old params-only rows
  still deliver through the existing Op/AdminInfo/AuditInfo reconstruction.
  Invalid/unsupported envelopes remain pending for retry rather than silently
  delivering an empty event. Missing prepared AuditInfo falls back to row columns.

Fixtures (existing files only):
- `controller/access_token_audit_test.go`: add durable-before-drain assertions for
  admin success/business failure, token operation, independent PAT event and
  expired-PAT failure reason; legacy ParamsJson delivery regression. Existing PAT
  write assertions now query pending rows first and inspect delivered events only
  after drain. JSON-valued AuditFields are compared by marshaling/JSONEq, not
  assumed float64/string values.
- `controller/token_test.go`: existing real SQLite/MySQL/PostgreSQL token audit
  matrix now migrates AuditEventOutbox, requires the exact pending event count,
  requires zero sink rows before drain, then retains the original post-delivery
  action/status/privacy assertions. PAT case requires two independent events.

Removed my unnecessary expansion of recordUserSecurityAudit and buildAuditDraft;
`controller/audit.go`, `controller/audit_actor.go`, and security enrollment tests
have no remaining diff from my attempted scope expansion. Authentication policy,
proof consumption, grants and credential checks are unchanged. No changes to
pi-logs-owned `model/code_operation_outbox_test.go` or channel files.

## Security references and controls

Read OWASP Authentication, Session Management and Logging Cheat Sheets:
https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html

Applied controls: keep actor/outcome/request correlation; log authentication
rejection reason as existing safe code; exclude credentials, raw bodies, session
IDs and usable token values. Fingerprints remain non-usable token references.
No ASVS conformance claim; runtime verification and cloud matrix remain required.

## Residual windows / honest limits

- Generic middleware is post-handler: a crash after business commit but before
  enqueue can lose that obligation. Transaction-owned code/channel paths close
  this gap with EnqueueAuditEventTx; middleware cannot retroactively join a commit.
- Primary enqueue waits synchronously, bounded by AuditDeliveryTimeout (10s);
  enqueue failure may add another bounded 10s direct-sink attempt. This is durable
  enqueue, not zero-latency logging. No measured latency claim.
- If both primary and fallback fail, return/log failure. A response already written
  by the business handler cannot be replaced safely; no false durability guarantee.
- Unknown primary commit outcome plus direct fallback can lead to replay. Relational
  event-id dedupe absorbs sink duplicates; ClickHouse remains at-least-once.
- The versioned envelope is readable by the new worker only; mixed-version rollback
  must drain prepared events before reverting workers, or old workers can interpret
  the envelope as action params. No destructive migration or table change.
- Invalid payload/unsupported versions stay retryable indefinitely and require an
  operator/code repair; no silent discard or terminal retry ceiling.
- Existing direct user-security/login helper paths outside the assigned generic
  middleware remain synchronous; no broad authentication-flow rewrite attempted.
- Cloud validation MUST execute the actual relational matrix and ClickHouse paths.
  No database versions/results are reported here because none ran locally.

## Handoff

Source frozen. The supplied root pane `w4C:p16` is no longer present: Herdr prompt
returned agent_not_found. Current agent list shows repository supervisor at p17,
but identity is not assumed; report forwarded via pi-logs for root coordination.
