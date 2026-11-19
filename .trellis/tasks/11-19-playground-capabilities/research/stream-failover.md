# Stream failover research — early upstream failures on HTTP 200 streams

Scope: `stream disconnected before completion: concurrency limit exceeded for user`
and any `response.failed` / `response.error` / premature EOF arriving after upstream
200 but before usable client output. Research only; no product edits.

## 1. Failure chain (verified)

- Streaming commits the client status before the upstream even answers:
  `relay/channel/api_request.go:567-574` calls `helper.SetEventStreamHeaders(c)` and
  `helper.StartStreamKeepAlive(...)` *before* `relayClient.Do(req)` (`:571-580`).
- The keepalive goroutine (`relay/helper/common.go:89-116`, `:134-148`) writes
  `: PING\n\n` via `PingData` (`relay/helper/common.go:270-281`). The first ping is a
  `Write`, so `c.Writer.Written()` becomes true (`streamKeepAliveWriter.Written`,
  `relay/helper/common.go:75-79`). `streamKeepAliveWriter.Flush` additionally calls
  `WriteHeaderNow()` (`relay/helper/common.go:55-61`).
- Therefore on any streaming request, by the time an upstream error is known,
  `DecideRelayRetry` (`service/relay_error.go:45-88`) hits
  `c.Writer != nil && c.Writer.Written()` → `response_started`, stop (`:59-61`).
  The status is already 200; the error is written into the open SSE stream
  (`controller/relay.go:108-114` → `writeRelayStreamError`, `:313-329`).
- `response.failed` / `response.error` from a Responses upstream:
  - Native: `relay/channel/openai/relay_responses.go:72-104` forwards every event
    verbatim and returns `accumulator.Finish(), nil`; the failure is only recorded
    as a `StreamStatus` outcome (`service/responses_usage.go:121-149`,
    `relay/common/stream_status.go:104-150`), never as a retryable relay error.
  - Converted (Chat→Responses / Messages→Responses): intercepted at
    `relay/channel/openai/chat_via_responses.go:282-293`, but retry is already
    blocked by the written ping/framing. Post-output failures are settled from
    delivered usage at `:318-335`; pre-output failures surface as the reported
    client message.
- Premature EOF is not a terminal: `relay/helper/stream_scanner.go:338` records
  `StreamEndReasonEOF`; neither handler requires a terminal event.
- Upstream per-account concurrency text is plain SSE error text, not
  `ErrorCodeChannelCapacitySaturated/Unavailable` (`service/channel_capacity.go:142-144`);
  those codes only come from local admission (`:153-190`).

## 2. Hard invariant for the fix (from root)

`c.Writer.Written()` MUST remain the final retry guard. Never allow retry after the
HTTP status is committed by relaxing that guard to a "substantive output" marker —
a committed 200 SSE response cannot be reset or replayed. The fix is to avoid the
commit: buffer before any downstream write, including init events and pings.

- `service/relay_error.go:59` stays unchanged in meaning: written → stop.
- The "output vs protocol framing" distinction is for *commit timing and
  accounting*, not for retry after commit.
- Client cancellation stays authoritative (`service/relay_error.go:53-55`,
  `relay/helper/stream_scanner.go:350`).
- Post-commit partial accounting stays as-is: bill delivered output and emit the
  terminal error into the stream (`chat_via_responses.go:318-335`,
  `controller/relay.go:313-329`).

## 3. Bounded pre-commit buffering (proposed)

Applies to the stream setup path in `relay/channel/api_request.go:567-574` and both
Responses handlers.

- Defer `SetEventStreamHeaders`/keepalive *writes* until a commit decision.
  Buffer protocol framing (ping, `response.created`, `response.in_progress`, role-only
  chunks) and substantive output separately.
- Commit when the first substantive delta is ready to send, or when a bound trips:
  a short wall-clock commit timer and a max-buffer byte/event cap. Bound both, so
  a silent upstream cannot hold the response open indefinitely.
- Before commit:
  - upstream `response.failed`/`error`/premature EOF → drop the buffer, return the
    typed error to the relay loop with `Written()` still false → ordinary retry
    policy applies (`controller/relay.go:286-292`).
  - EOF without a terminal outcome → same path; classify via
    `StreamStatus.ResponseOutcome()/HasErrors()` (`relay/common/stream_status.go:160-207`).
  - client cancel → drop buffer, no retry.
- After commit: never retry. Deliver partial output, then the terminal error;
  settle delivered usage exactly as today.
- Heartbeats must not commit the stream before real upstream status/first byte;
  pre-commit pings are either suppressed or sent only after the commit decision.
- If the commit is only a ping after the bound expires, treat the stream as
  committed and non-retryable from that point.

Upstream concurrency classification: map a failed event carrying a recognized
code/type to that error; otherwise `ErrorCodeBadResponse`. Do not auto-ban on raw
text alone. Capacity-specific handling (`service/relay_error.go:48`) stays reserved
for the local capacity codes.

## 4. Endpoint conversion resolver — reuse and precedence

Single resolver shared by the existing gates:

- Today: `service.ShouldChatCompletionsUseResponsesGlobal` called at
  `relay/compatible_handler.go:78-81` and `relay/claude_handler.go:79-81`;
  both currently funnel into `textRequestViaResponses`
  (`relay/chat_completions_via_responses.go:76-118`).
- Proposal: `ResolveUpstreamProtocol(info)` with precedence
  1. global pass-through / channel pass-through body → no conversion (existing guards
     `compatible_handler.go:78-80`, `claude_handler.go:77-79`);
  2. channel setting `chat_completions_endpoint` (`""|"chat"|"responses"`, additive
     on `dto.ChannelSettings`, stored in `model/channel.go:1372-1393`);
  3. global `ChatCompletionsToResponsesPolicy` (unchanged fallback,
     `setting/model_setting/global.go:13-35`);
  4. default chat.
- Execution stays `textRequestViaResponses` (plugins, param override, streaming,
  `PostTextConsumeQuota` reused). Claude inbound can use the existing direct
  single-step converter `ConverterClaudeMessagesToOpenAIResponses`
  (`relaykit/relayconvert/text_converter_registry.go:167-180`,
  `relaykit/relayconvert/request_registry.go:87`).
- Save-time validation: reject `responses` for adaptors that cannot serve it
  (`relay/channel/codex/adaptor.go:44,145`); honor `ToolLossPolicy`
  (`relaykit/dto/channel_settings.go:110-125`).

## 5. Exact regression tests (extend existing files)

- `relay/channel/openai/relay_responses_billing_test.go:120-145` — add
  `response.failed` before any write (no `Written`, retryable) and after committed
  output (settle + terminal error). Keep the existing end-reason table style.
- `relay/channel/openai/chat_via_responses_test.go:221-330` — failed event before
  the first delta vs after the first delta; assert no duplicate/mixed answers and
  the exact client-visible terminal payload.
- `controller/channel_pin_retry_test.go:250, 494, 649` — lock the invariant:
  `Written()==true` always stops retry, even when an output marker exists;
  `Written()==false` + failed/capacity error retries.
- `relay/helper/stream_scanner_test.go` — heartbeat/EOF: no pre-commit ping write;
  EOF without terminal is classified as a failure pre-commit.
- `relay/helper/common.go` keepalive tests (same file) — ping never commits before
  the commit decision; commit flushes buffered framing first.
- `service/responses_usage_test.go:123-124` — failed/error classification stays.
- `router/provider_plugin_relay_test.go:2121+` — end-to-end: first attempt fails
  pre-commit → second channel serves a single clean answer; post-commit → one
  stream with partial output + error and no retry.
- `relaykit/dto/channel_settings_test.go` — new endpoint setting validation and
  round-trip.
- `relay/channel/advancedcustom/adaptor_test.go:1052` — reference for
  Messages→Responses route behavior.
