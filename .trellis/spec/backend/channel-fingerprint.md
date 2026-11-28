# Channel fingerprint diagnostics

## 1. Scope / Trigger

Operator-triggered model fingerprint checks must use the gateway's channel test
path, preserve quota/usage semantics and score locally against pinned assets.

## 2. Signatures

- `GET /api/channel/:id/fingerprint/options`: credential-free model/Key options.
- `POST /api/channel/:id/fingerprint/preview`: create opaque server-held prompts.
- `POST /api/channel/:id/fingerprint`: start the matching preview as a bounded job.
- `GET /api/channel/:id/fingerprint/:job_id`: read progress/results.
- `POST /api/channel/:id/fingerprint/:job_id/cancel`: cancel actual upstream work.
- `POST /api/channel/:id/fingerprint/:job_id/retry`: retry one failed sample.
- `service.StartChannelFingerprint`, `GetChannelFingerprint` and
  `CancelChannelFingerprint` own the bounded job registry.
- `relay.RunChannelDiagnostic` applies supported endpoint conversion without
  settling separately from the channel test.

## 3. Contracts

All routes require channel operate permission. The selected model belongs to the
selected channel; reads and cancellation bind the job to the channel in the URL.
The console submits channel/model plus an optional canonical string Key ID,
expected revision and position fence. It displays three opaque server-generated
prompts and sends the returned preview ID on Start. Preview is bound to the
authenticated owner and immutable request/Key context; it performs no upstream
request. Endpoint selection, scheduling budget and challenge count are server
owned. The console has no endpoint, count or budget controls or payload fields.
Legacy endpoint overrides cannot select a different transport. Service-level
explicit counts retain their 1–3 validation; the console always previews three.
Samples execute serially.

Automatic selection uses the authoritative `auto_models` projection. Missing
automatic capabilities fail closed; explicit empty Key allowlists deny all.
Options expose masked identities and model capabilities, never credentials,
endpoint pools or per-Key transport settings. Admission reloads authoritative
allocation state and validates stable ID/revision, model scope and mapping.
The job uses at most three probes, at most four active jobs, bounded retention and
a bounded lifetime. Each effective upstream request is limited to one candidate
and at most 4096 output tokens after overrides/conversions; added tools fail
preflight. Input tokens remain an estimate, so this is not a guaranteed cost cap.

Before every sample, reload the fixed persisted channel and validate its model,
Key selection and protocol. Auto resolves against current server policy. Disabled
channels retain administrator recovery diagnostics; deletion or model removal
prevents later dispatch. The shared test path owns post-wait revalidation and
admission; fingerprint orchestration must never acquire a second lease.

An admitted attempt binds one URL, stable allocation revision, effective mapping
and upstream protocol. The final prompt/output guard runs against the actual wire
format after mapping, protocol conversion, plugins and administrator overrides.
The frozen URL must still belong to its configured pool. A preview alone cannot
authorize a changed prompt, added tools or larger output cap.

Retry targets only a failed probe using the same preview, prompt and expected
attempt number. It preserves successful output and uses the same remaining run
budget with a revision fence. Retry is an additional potentially paid request.
Budget-stop reasons survive terminal scoring, including inconclusive analysis;
the analysis reason remains separately available.

UI progress counts completed, failed and cancelled samples as processed. Replies
appear when a request finishes; elapsed status is not streamed output. Translate
only the closed safe error vocabulary (including numeric HTTP status) at every
preview/start/retry/poll/cancel display boundary. Keep raw safe errors in state so
language changes rerender them, and leave unknown text literal. A cancellation
acknowledgement must match job, channel and revision before clearing poll failure
or resuming fresh reads while cancellation is still in progress.

The diagnostic controller alone settles known usage, including productive
failed or truncated streams. Buffered unsolicited Responses SSE must retain
reported usage on errors, and a diagnostic without a terminal event fails rather
than synthesizing success or estimated failure usage. Raw non-2xx error-envelope
usage is conservative evidence against replay, not an independently trusted
billable usage report.

`pkg/lmfp` embeds the SDK reference bank and detector, verifies SHA-256 pins, and
loads them once. Runtime scoring does not contact the hosted detector. An optional
`FINGERPRINT_DATA_DIR` requires both `FINGERPRINT_BANK_SHA256` and
`FINGERPRINT_DETECTOR_SHA256`. Results expose reference digests/build time and the
statistical limitation: nearest match is not identity, authenticity or IQ proof.

## 4. Validation & Error Matrix

| Condition | Behavior |
| --- | --- |
| Wrong channel/job pair | Not found; no cancellation of another job |
| Missing/expired/mismatched preview or stale Key revision | Reject; no upstream dispatch |
| Unsupported protocol/output limit | Reject before an upstream request |
| Count omitted/zero | Default to three; explicit values outside 1–3 reject |
| Channel deleted/model removed between samples | No later upstream dispatch |
| Selected Key revision changed | Reject stale selection before another attempt |
| Server endpoint policy changed | Revalidate; preserve each admitted attempt snapshot |
| Budget cannot fund the next probe | Stop and terminalize queued probes |
| Negative/unknown usage | Charge full estimated probe budget for scheduling |
| Overflowing usage sum | Saturate; never create negative remaining use |
| Cancel or timeout | Cancel request context and release channel capacity |
| Completed job canceled | Conflict; completed result stays unchanged |
| Retry targets a successful sample, stale attempt or exhausted budget | Reject without replaying successful work |

## 5. Good / Base / Bad Cases

Good: a Chat probe on a Responses-only channel converts upstream and records
usage once. Base: the embedded bank works without network downloads. Bad: scoring
an unsupported response as an empty answer or treating a nearest match as proof.

## 6. Tests Required

Use `pkg/lmfp/lmfp_test.go`, `service/channel_fingerprint_test.go`, the existing
controller channel-test suite and fingerprint UI tests. Verify checksum failure,
job limits/cancel/budget arithmetic, exact output cap after overrides, and stale
UI cancellation responses. Cover opaque preview ownership, eligible Key options,
automatic dispatch, stale selection between probes, invalid Key payloads without
secrets, per-sample retry and final-wire prompt/output constraints. Retain service
count compatibility, budget/inconclusive scoring and bounded retained replies.
Select **all `TestFingerprint` controller tests** in the existing real-dialect
fixture loop with `TEST_CHANNEL_SQL_DSN` for MySQL/PostgreSQL and default SQLite.
Selecting only Diagnostic or ResponsesOnly misses the saved option/preview/Key
regressions. Keep service `-run Fingerprint`, the existing race selector and
`web/src/features/service-status/__tests__/fingerprint.test.tsx`. Fixture Redis is
disabled; this is not Redis validation. Source review is not hosted evidence.

## 7. Wrong vs Correct

Wrong: invoke the hosted detector with channel credentials, or settle both in
the conversion helper and again in the channel test.
Correct: keep secrets server-side, dispatch a bounded ordinary channel request,
settle/log once, then score the answer with verified embedded reference data.
