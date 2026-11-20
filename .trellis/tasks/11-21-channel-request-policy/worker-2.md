# Worker 2 — fingerprint-only handoff ready

Root owns all admission/waiter implementation and tests. The available API and integration requirements are in `admission-contract.md`; this worker will not edit those files or `controller/channel-test.go` / its request regressions.

Implementation boundary: default omitted challenge count to three while preserving explicit 1–3; reload the target channel, keys, model list and endpoint policy before every serialized fingerprint sample; preserve explicit administrator diagnostics on disabled channels/keys. Reuse `testChannel` for admission, post-wait revalidation, request policy, cleanup and single settlement—no nested fingerprint lease or independent busy-retry loop.

The selected channel ID/model remain fixed. Deletion or removal of the selected model prevents another dispatch; disabled status alone does not block an administrator recovery probe. Input reservation is estimated, so existing per-request output limits and scheduling budget are not an absolute billing cap.

## Completed and frozen

- `service/channel_fingerprint.go`: omitted/zero challenge count defaults to three; explicit 1–3 remains valid and out-of-range counts remain rejected. Clarified that the output allowance and estimated input reserve are scheduling bounds, not an absolute billing cap.
- `controller/channel_fingerprint.go`: each sample reloads the persisted fixed channel by ID and validates the selected model/protocol before dispatch. The job closure retains identity and original endpoint intent rather than starting credentials/resolved automatic endpoint. Current keys, mapping, overrides and endpoint settings pass to `testChannel`. Deleted channels or removed models prevent dispatch; disabled channels retain administrator recovery semantics.
- `web/src/features/service-status/fingerprint.tsx`: defaults the existing challenge input to three without changing its 1–3 clamp, layout or UI copy.
- `service/channel_fingerprint_test.go`: authored default/explicit count and invalid-count regressions; strengthened cancellation coverage to assert only one of three serialized samples dispatches.
- `web/src/features/service-status/__tests__/fingerprint.test.tsx`: authored default-three and explicit-one/two submission coverage. Corrected controlled-number-input replacement with `fireEvent.change`; production clamping is unchanged.

## Integration and remaining validation

Pi1 owns the observable controller freshness regression in `controller/channel_test_request_test.go` and common post-wait revalidation/admission integration. No changes were made to that file, `controller/channel-test.go`, or root-owned capacity/waiter files. Fingerprinting adds no lease and no independent retry loop. Safe upstream-busy retry remains the shared test path's responsibility: reuse one bounded admission owner/deadline; report recognized zero-work pre-output busy before releasing; never retry productive/billable work or settle twice.

Existing serial execution, cancellation, output caps, budget arithmetic and local pinned SDK challenges are retained; no hosted-detector dependency or new i18n key was introduced. Root should include freshness regressions in the real SQLite/MySQL/PostgreSQL cloud matrix and run the existing fingerprint/race/frontend selectors against the integrated SHA.

Status: handoff ready; product files frozen for root commit and independent/cloud review. Tests are authored but unrun. No local tests/build/lint/typecheck/browser checks, installs, paid probes, server access, staging, commits, pushes or child agents were performed. An allowed `gofmt -w` attempt could not run because `gofmt` is unavailable on PATH; formatting remains for root/cloud tooling.
