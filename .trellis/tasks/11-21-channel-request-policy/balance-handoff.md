# Balance policy integration handoff

Status: root committed the balance integration as `5805d982b`. Per root's CI review, only a cap-only stale-cache status divergence remains; native `integration_check` exclusively owns the fix in `model/channel.go` and the existing model status test. Balance production/test files remain frozen; exact-SHA cloud revalidation belongs to root.

## Change boundary

- Existing balance requests bypass shared channel admission, omit static header overrides on built-in requests, and do not cancel their HTTP work when background/query deadlines end. `GetResponseBody` also leaves non-200 response bodies open.
- Change `controller/channel-billing.go` for the trusted balance-operation boundary, current saved-channel/key/settings revalidation after admission, context-aware HTTP dispatch and guaranteed body cleanup.
- `controller/channel_balance.go` propagates cancellation and accepts a refreshed balance source only after a successful request actually sent that exact same-account credential at the original key index. It advances verified source evolution across a multi-key batch while preserving plugin query and amount semantics.
- `service/provider_plugin_balance.go` checks exact credentials, header overrides, relevant balance route/auth and plugin config, plus multi-key scopes/bindings. Unrelated AutoBan/cap edits are not credential changes.
- Extend the existing `controller/channel_billing_test.go` with focused behavioral regressions. No new test files.
- Reuse `service.DoChannelManagementRequest` inside one outer lease for a whole balance operation. Preserve specialized provider URLs, existing parsing/calculations, disabled-channel manual diagnostics and the existing multi-key balance restriction. No generation conversion, first-byte timeout, schema, billing model or UI changes.

## Verification constraint

No local tests, builds, lint, typecheck, browser checks or upstream probes. Source review and gofmt only; regression execution belongs to root's exact-SHA cloud CI.

## Independent review follow-up

The integrated balance operation now holds one outer admission lease, propagates
cancellation into built-in and plugin HTTP calls, reloads current policy/key after
grant and performs guarded result persistence outside HTTP. Source checks reject
stale key/type/origin/binding and AutoBan decisions.

The successful 401 refresh provenance and balance auth/config source corrections
are included in committed `5805d982b`, with focused single/multi-key regressions in
the existing `TestChannelBalanceManagementDatabaseMatrix`. An unsent administrator
replacement is never accepted merely because its account matches. A stored
replacement returned by the refresher is eligible only when the successful retry
actually sent it and current indexed source/policy still matches exactly.

Root reports CI exposed only cap-only stale-cache status divergence. Native
`integration_check` owns that remaining `model/channel.go` plus existing model
status-test correction; pi 2 does not edit those or any balance production/tests.
No final cloud pass or database compatibility is claimed until root verifies the
integrated SHA on the required database/race gates.

## Diagnostic accounting and TLS boundaries

Raw HTTP non-2xx error-envelope usage is conservative no-replay evidence only,
NOT billable usage. Actual parsed partial SSE usage is retained separately by the
owner correction committed as `32be8c771` per root; arbitrary error-envelope
fields must not create charges.

Codex OAuth keeps the existing deployment-wide relay TLS policy and does not
receive per-channel header/TLS overrides; it is not universally verified-TLS.
GitHub version lookup has independent verified-TLS policy. These boundaries do
not change balance amount formulas or redesign OAuth.

Only these handoff notes were reconciled in the final pass. No production/test
edits, local validation or commits were performed. Notes are frozen for root.
