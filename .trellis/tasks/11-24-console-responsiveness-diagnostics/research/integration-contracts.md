# Integration contracts, 2026-11-24

All executable verification runs in GitHub Actions. These decisions are based on source review only.

## Dashboard routes and privacy

- Preserve existing `GET /api/rankings` and its `HeaderNavModuleAuth("rankings")` policy. That middleware already attempts optional user authentication in public mode, requires authentication when configured, and denies access when the module is disabled.
- Add `GET /api/rankings/model-usage?model=...&period=...` at `apiRouter` scope with `DisableCache()` and `HeaderNavModulePublicOrUserAuth("rankings")`; handler `GetRankingModelUsage`. A query parameter supports model names containing `/`. With the module enabled and public, anonymous viewers receive masked rows; when disabled or configured to require authentication, anonymous requests are challenged while authenticated users keep access.
- The selected middleware is intentional and covered by `TestRankingModelUsageRequiresModelAndHonoursModulePolicy`; it preserves the existing public/authenticated fallback behavior for this new endpoint.
- Add `GET /api/status/instances` at `apiRouter` scope with `DisableCache()` and `UserAuth()`; handler `GetInstanceResourceSummary`. Do not register it under `selfRoute` (`/api/user`).
- Preserve existing root-only `/api/system-info/instances`. Do not introduce a duplicate full-record endpoint.
- Per-model responses are viewer-specific and `no-store`; do not mutate the existing period-only cached ranking response. Masked rows contain neither real names nor user IDs nor name-derived hints. Admins see all names; ordinary viewers see their own full name only; anonymous viewers see masked names only.
- API domain bindings belong to existing API Info settings entries (`node_name`, optional); multiple entries can reference the same node. Existing array order is display order. The root settings API retains raw bindings, but public `/api/status` API Info output must strip them because `node_name` may equal a private hostname.
- Safe resource output identifies configured API entries, never the raw node identity. Only approved resource percentages, validity, status and observation time are exposed. Unknown, stale and disabled samples remain distinguishable from zero.
- Frontend translation keys are literal English source strings, not dotted namespace identifiers. All seven locale files remain pi-ui-owned.

## Durable audit and code creation

Existing `SystemTask` is unsuitable as one task per audit event:

- Enqueue deduplicates by task type and uses global DB rather than the business transaction.
- The master-only runner claims one task per type every 15 seconds; expired leases become terminal failures without replay.
- History cleanup can delete failed tasks, and task APIs expose raw payload/state/result.
- The current audit sink returns no error. Relational EventId uniqueness does not imply ClickHouse exactly-once delivery; ClickHouse uses a plain MergeTree table.

The mutation owner must publish the exact durable outbox and operation-identity contract before integrating schema changes. Use the scheduler only as a possible batch-drain trigger. Preserve atomic business mutation/event/batch identity, recoverable retries, secret-free actor snapshots, independent delivery deadlines and explicit retention of undelivered events. Never retain a Gin context or store generated codes/credentials in generic task payloads. Same-actor/same-operation retry IDs must recover the original batch, while changed request bodies conflict. Keep redemption claim-limit `BatchId` semantics distinct from creation-operation identity.

Root accepted the two additive primary tables proposed by pi-billing with these required amendments:

- Persist a canonical digest of all business request fields. Same actor/type but different payload is a conflict; generated credentials are excluded from the digest.
- Retry delivery indefinitely with bounded exponential backoff (10-minute cap), retaining failed events. Do not stop permanently after 20 attempts.
- Roll back a failed PostgreSQL transaction before recovering a unique operation-ID conflict. Resolve an uncertain commit by reading the same operation identity; never silently start a new create.
- Preserve redemption claim-batch semantics. Return a distinct creation-operation identity. Generate one server-side for legacy callers as well, while only callers supplying a stable identity receive retry protection.
- `result_json` is an additional sensitive copy of code values, never the only durable copy. Do not expose it in generic tasks or audit projections. Current authorization and original actor ownership apply to replay.
- Keep Gin snapshot construction in the controller and plain value structs in model.
- Reuse `TEST_MYSQL_DSN` and `TEST_POSTGRES_DSN`; SQLite uses a temporary file. GitHub CI also has ClickHouse 25.8. A changed audit sink needs actual ClickHouse coverage with truthful at-least-once semantics.
- Released migration baseline is `53a71f88d5b568847cd7233a08f934824dcb22d3` (`custom-20261124-53a71f8`). Tests may use the exact released schema with recorded provenance and run the actual new migration twice, preserving existing rows/indexes/uniqueness.

## Channel batch reads

The lightweight options endpoint is administrator-only and returns only selection data. Bulk projection must preserve authoritative allocation rows and context/session semantics. Bound or chunk large `IN` lists for database parameter limits. Never let an AfterFind opt-out escape a reader without applying the authoritative projection. Save-time cache publication must update both channel and routing/binding indexes with stale-snapshot protection; `CacheUpdateChannel` alone is insufficient.

## Security review references and pending cloud evidence

Official sources read on 2026-11-24: OWASP ASVS 5.0.0 (the official site identifies it as the stable release), Authentication Cheat Sheet, Session Management Cheat Sheet, and Authorization Cheat Sheet.

- https://asvs.owasp.org/
- https://raw.githubusercontent.com/OWASP/ASVS/v5.0.0/5.0/en/0x17-V8-Authorization.md
- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html

Applicable authorization controls: `v5.0.0-8.1.1` and `8.1.2` (document route/data/field permissions), `8.2.1` through `8.2.3` (enforce function/object/field permissions), `8.3.1` (trusted server enforcement), and `8.3.2` (prompt permission-change application). Here these map to existing middleware, operation ownership, full-name masking, safe instance fields, and cache behavior after role/account changes. Preserve current session expiry/revocation handling; do not derive identity from unvalidated client fields. Durable audit snapshots exclude usable credentials and code values. Sensitive responses use no-store, and frontend state must not reuse privileged results across identities or roles.

Cloud acceptance must exercise ordinary/admin/anonymous viewers, module-disabled and authentication-required settings, PAT role/scope limits, lost-response batch replay and changed-payload conflicts, operation ownership, raw JSON privacy, and delayed-response identity changes. No ASVS compliance or successful validation is claimed before those results exist.
