# Channel fingerprint diagnostics

## 1. Scope / Trigger

Operator-triggered model fingerprint checks must use the gateway's channel test
path, preserve quota/usage semantics and score locally against pinned assets.

## 2. Signatures

- `POST /api/channel/:id/fingerprint`: start a bounded job.
- `GET /api/channel/:id/fingerprint/:job_id`: read progress/results.
- `POST /api/channel/:id/fingerprint/:job_id/cancel`: cancel actual upstream work.
- `service.StartChannelFingerprint`, `GetChannelFingerprint` and
  `CancelChannelFingerprint` own the bounded job registry.
- `relay.RelayTextDiagnostic` applies supported endpoint conversion without
  settling separately from the channel test.

## 3. Contracts

All routes require channel operate permission. The selected model belongs to the
selected channel; reads and cancellation bind the job to the channel in the URL.
Requests include model, endpoint type, challenge count and estimated token budget.
Omitted or zero count defaults to three randomized SDK challenges; explicit
counts 1–3 stay valid. Samples execute serially. The UI starts at three and
keeps the existing 1–3 bounds.
The job uses at most three probes, at most four active jobs, bounded retention and
a bounded lifetime. Each effective upstream request is limited to one candidate
and at most 4096 output tokens after overrides/conversions; added tools fail
preflight. Input tokens remain an estimate, so this is not a guaranteed cost cap.

Before every sample, reload the fixed persisted channel and validate its model
and protocol. Preserve the original endpoint intent: Auto must resolve against
current channel settings rather than the job's starting endpoint. Disabled
channels retain administrator recovery diagnostics; deletion or model removal
prevents later dispatch. The shared test path owns post-wait revalidation and
admission; fingerprint orchestration must never acquire a second lease.

`pkg/lmfp` embeds the SDK reference bank and detector, verifies SHA-256 pins, and
loads them once. Runtime scoring does not contact the hosted detector. An optional
`FINGERPRINT_DATA_DIR` requires both `FINGERPRINT_BANK_SHA256` and
`FINGERPRINT_DETECTOR_SHA256`. Results expose reference digests/build time and the
statistical limitation: nearest match is not identity, authenticity or IQ proof.

## 4. Validation & Error Matrix

| Condition | Behavior |
| --- | --- |
| Wrong channel/job pair | Not found; no cancellation of another job |
| Unsupported protocol/output limit | Reject before an upstream request |
| Count omitted/zero | Default to three; explicit values outside 1–3 reject |
| Channel deleted/model removed between samples | No later upstream dispatch |
| Channel credentials/endpoint policy changed | Next sample resolves current policy |
| Budget cannot fund the next probe | Stop and terminalize queued probes |
| Negative/unknown usage | Charge full estimated probe budget for scheduling |
| Overflowing usage sum | Saturate; never create negative remaining use |
| Cancel or timeout | Cancel request context and release channel capacity |
| Completed job canceled | Conflict; completed result stays unchanged |

## 5. Good / Base / Bad Cases

Good: a Chat probe on a Responses-only channel converts upstream and records
usage once. Base: the embedded bank works without network downloads. Bad: scoring
an unsupported response as an empty answer or treating a nearest match as proof.

## 6. Tests Required

Use `pkg/lmfp/lmfp_test.go`, `service/channel_fingerprint_test.go`, the existing
controller channel-test suite and fingerprint UI tests. Verify checksum failure,
job limits/cancel/budget arithmetic, exact output cap after overrides, and stale
UI cancellation responses. Cover default-three and explicit-one/two submission,
canceling the first of three samples, and changed credentials/endpoint/model
between probes. `TestFingerprintResponsesOnlyChannelProbe` uses
`TEST_CHANNEL_SQL_DSN` on MySQL/PostgreSQL and the default SQLite fixture; that
fixture explicitly disables Redis and is not a Redis integration test.

## 7. Wrong vs Correct

Wrong: invoke the hosted detector with channel credentials, or settle both in
the conversion helper and again in the channel test.
Correct: keep secrets server-side, dispatch a bounded ordinary channel request,
settle/log once, then score the answer with verified embedded reference data.
