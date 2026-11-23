# Request content and node evidence

Planning-only findings, 2026-11-21. No product implementation, database mutation or test execution occurred. Parent research remains authoritative for shared relay and billing boundaries.

## Capture and storage boundaries

- `router/task-plugin-protocol-router.go:34–48` can prepare and modify a request before `controller/relay.go:156`; capture at the first original-body storage boundary rather than assuming Relay sees the original client text.
- `common/body_storage.go:44` provides owned selected-field copies. Plugin replacement (`relay/requestplugin/relay.go:300`) and request cleanup (`middleware/body_cleanup.go:11`) can close old storage; an asynchronously consumed pointer is unsafe.
- `service/log_info_generate.go:70` and `service/relay_error.go:187` do not consistently store ordinary prompt/messages or actual processing node. Freeze the ingress node as snapshot `submit_node_name`; record each log operation's current execution node independently as `admin_info.node_name`.
- `model/log.go:375` serializes consume parameters to process logs; `model/log.go:603/:671` list paths load all Log fields. Payload must live in a separate log-store table, never Other or consume parameters. Lightweight metadata belongs in `other.admin_info`.
- `model/log.go:105` already indexes RequestId; `model/log.go:137–149` rewrites some displayed IDs. Associate by stable server request ID, user and opaque snapshot ref, not the displayed ID.

## Role and API reuse

- `model/log_other.go:232` owns role projection; keep request references and common operational node under admin_info. Existing root-only task node is a submission-source fallback only, never proof of the node that later executed a poll/settlement.
- `router/api-router.go` contains admin/self/export log routes. New body reads reuse admin/root authentication and cache disabling; `middleware/access_token_routes.go:251–259` supplies existing log:read scope semantics.
- A successful detail lookup must also have a currently associated consume/error log. An opaque random ref alone is not authorization. The server checks associations; user/self/token/export paths receive no payload.

## Cleanup reasoning

- `model/log.go:887` deletes logs only today; its ClickHouse branch uses a synchronous mutation. `model/main.go:407–473` migrates an independent LOG_DB and applies optional creation-time TTL to logs.
- One snapshot may be referenced by early retry errors, later success and eventual async settlement. Copying the logs' creation-time TTL onto a body row would delete its content while a newer associated log still exists. Rewriting/duplicating the large body per link is unnecessary.
- Keep the payload immutable. After existing log deletion and periodically after native ClickHouse TTL, sweep orphan details in bounded batches based on actual remaining log and active task references. Check failures postpone deletion. The API immediately denies a body without associated logs even if the physical sweep has not run yet.
- `model/task.go:112–174` already stores private NodeName and an execution RequestID. Preserve the legacy NodeName as submission provenance, introduce explicit submit_node_name semantics and persist the lightweight content ref beside it; read/parse private state in Go rather than introducing dialect-specific JSON queries. Later operation logs must obtain their actual node from the executing instance.
- In-flight link creation and orphan deletion need explicit coordination. The concrete design candidate is a payload-free protection row in the primary relational DB for every ref, so an independent log DB or ClickHouse does not remove the coordination authority. Renewable writer leases protect live HTTP retries; durable task refs protect later settlement. Primary-row mutual exclusion, holder/generation conditions and committed write_pending/delete_pending markers protect bounded cross-store operations and leave recoverable evidence after crashes.
- The design deliberately does not call this a distributed transaction. Uncertain operations remain protected pending reconciliation instead of being deleted merely because a lease expired. MySQL/PostgreSQL row locks and SQLite atomic conditional writes need real concurrency tests, as do pending mutation recovery and the additional primary-DB write cost. No race solution is claimed tested in this plan.
- MySQL payload type must be LONGTEXT, since 256 KiB exceeds TEXT capacity. SQLite/PostgreSQL use TEXT. ClickHouse requires explicit DDL and actual integration testing.

## Existing UI components

- Use `web/src/features/usage-logs/components/dialogs/details-dialog.tsx` and `log-detail-layout.tsx` (`DetailSection`/`DetailRow`) for the existing dialog.
- Use `web/src/components/ai-elements/code-block.tsx` for inert structured text, scrolling and collapse, and `web/src/components/copy-button.tsx` for copy behavior. Existing loading/error components cover detail fetch states.
- Fetch through TanStack Query only after privileged users open a detail with a ref. List rendering must never issue one body request per row.
- Add actual processing node text to existing `common-logs-columns.tsx` plus mobile cards/compact rows; show submit_node_name separately in details. Historical missing actual nodes show not recorded even when the old task submission node is known. No new generic dialog or code viewer is needed.
- Extend existing log detail/mobile tests and existing backend log tests. Follow the i18n skill for new text in all seven locales; do not create documentation under docs or plugins.

## Acceptance and validation sources

- Parent `research/security-and-validation.md` records OWASP references and actual toolchain discovery; implementation still must verify applicable controls and run the real database matrix.
- Extend `controller/relay_error_log_test.go`, `controller/log_export_test.go`, `controller/task_log_view_test.go`, `model/log_other_test.go`, `model/log_format_test.go`, `model/clickhouse_log_test.go` and the existing usage-log detail/mobile tests as appropriate. Do not create one test file per layer for this feature.
- Required invariants include original-before-plugin capture; success/failure links; bounded valid UTF-8/JSON and media exclusion; no payload in list/process logs; admin/PAT authorization; actual processing node; task/WebSocket provenance; cleanup races and real SQLite/MySQL/PostgreSQL/affected ClickHouse lifecycle verification.
