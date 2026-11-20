package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	for i := range responsesResponse.Output {
		imageCounter.Observe(&responsesResponse.Output[i], &i)
	}
	imageCounter.Commit(info)

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)

	helper.StoreUpstreamRetryAfter(c, resp.Header.Get("Retry-After"))

	// The relay loop installs an attempt gate before dispatch; hold it here so
	// framing and keepalive bytes stay retryable until this attempt produces
	// usable output. A direct handler call keeps the original direct-write
	// behaviour unless its caller installs a gate.
	gate := helper.StreamCommitGateFrom(c)
	if gate != nil {
		gate.Hold()
	}

	var earlyFailure *types.NewAPIError
	var terminalFailure *types.NewAPIError
	sawWork := false
	knownUsage := &dto.Usage{}
	tools := responsesToolUsage{}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal stream response: %s", err.Error()))
			sr.Error(err)
			return
		}
		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
			service.ApplyResponsesUsage(knownUsage, streamResponse.Response.Usage)
		}
		failed := isResponsesFailureEvent(streamResponse)
		sawWork = sawWork || responsesFailureHasBillableWork(streamResponse) || (!failed && isResponsesSubstantiveEvent(streamResponse))
		if failed {
			terminalFailure = responsesStreamFailure(streamResponse, data)
			info.StreamStatus.MarkFailed(string(terminalFailure.GetErrorCode()), string(terminalFailure.GetErrorType()), terminalFailure.StatusCode)
			if gate != nil && !gate.Committed() && !sawWork {
				earlyFailure = terminalFailure
				gate.Discard()
				sr.Stop(earlyFailure)
				return
			}
		}
		// Usage is an upstream fact. Observe before any downstream write can
		// fail, including releasing held framing. Normalize error aliases for
		// the accumulator's terminal usage and tool accounting path.
		usageEvent := streamResponse
		if failed {
			usageEvent.Type = "response.failed"
		}
		tools.observe(info, &usageEvent, common.StringToByteSlice(data))
		if usageEvent.Type == dto.ResponsesOutputTypeItemDone && usageEvent.Item != nil {
			switch usageEvent.Item.Type {
			case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
				// Hosted calls are deduplicated against terminal-only outputs by
				// tools. The accumulator still owns text and image quantities.
				usageEvent.Item = nil
			}
		}
		accumulator.Observe(&usageEvent, common.StringToByteSlice(data))
		// Commit before the first usable bytes so the normal write path is
		// unchanged from that point on: framing events stay buffered, real
		// output goes straight to the client.
		if gate != nil && (sawWork || info.StreamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeUnknown)) {
			if err := gate.Commit(); err != nil {
				sr.Stop(err)
				return
			}
		}
		if failed && streamResponse.Type != "response.failed" {
			writeResponsesTerminalError(c, terminalFailure)
			sr.Stop(terminalFailure)
			return
		}
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
			sr.Stop(err)
			return
		}
		if failed {
			sr.Stop(terminalFailure)
			return
		}
		if (streamResponse.Type == "response.completed" || streamResponse.Type == "response.done") &&
			streamResponse.Response != nil && info.StreamStatus.ResponseOutcome() == string(relaycommon.ResponseOutcomeCompleted) {
			sr.Done()
		}
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	if earlyFailure != nil {
		return nil, earlyFailure
	}
	usage := dto.MergeUsageNonZero(accumulator.Finish(), knownUsage)
	usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	if gate != nil && !gate.Committed() {
		// The upstream ended before any usable output. A required-terminal
		// stream that stops without its terminal event was cut short.
		if failure := responsesStreamTruncation(info.StreamStatus); failure != nil {
			gate.Discard()
			return nil, failure
		}
		if !info.StreamStatus.IsNormalEnd() && !sawWork {
			gate.Discard()
			failure := info.StreamStatus.EndError
			if failure == nil {
				failure = errors.New("upstream stream stopped before any output")
			}
			return nil, types.NewErrorWithStatusCode(failure, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
		}
		_ = gate.Commit()
		return usage, nil
	}
	if terminalFailure != nil {
		// The error was already delivered in-stream. The host settles usage
		// only on a nil error; StreamStatus retains the unsuccessful outcome.
		return usage, nil
	}
	if failure := responsesStreamTruncation(info.StreamStatus); failure != nil {
		// Output already reached the client: keep the delivered usage and emit
		// the protocol's terminal error instead of replaying the attempt.
		writeResponsesTerminalError(c, failure)
		info.StreamStatus.MarkFailed(string(failure.GetErrorCode()), string(failure.GetErrorType()), failure.StatusCode)
		return usage, nil
	}
	return usage, nil
}

// isResponsesSubstantiveEvent reports whether the client could act on the
// event: model text, reasoning, tool arguments, annotations or a completed
// item. Only protocol framing stays buffered.
func isResponsesSubstantiveEvent(event dto.ResponsesStreamResponse) bool {
	switch event.Type {
	case "response.created", "response.in_progress", "response.queued", "response.output_item.added", "response.content_part.added":
		return false
	}
	if event.Delta != "" || event.Text != nil || event.Annotation != nil {
		return true
	}
	if event.Arguments != nil && *event.Arguments != "" {
		return true
	}
	return event.Item != nil || event.Response != nil
}

// isResponsesFailureEvent covers every terminal failure shape an upstream can
// send on a 200 stream: a dedicated failure event, a top-level error event, or
// a terminal envelope whose status is failed.
func isResponsesFailureEvent(event dto.ResponsesStreamResponse) bool {
	switch event.Type {
	case "response.failed", "response.error", "error":
		return true
	}
	if event.Response == nil {
		return false
	}
	switch event.Type {
	case "response.completed", "response.done", "response.incomplete":
		var status string
		_ = common.Unmarshal(event.Response.Status, &status)
		if status == "failed" {
			return true
		}
		upstreamError := event.Response.GetOpenAIError()
		return upstreamError != nil && (upstreamError.Type != "" || streamErrorField(upstreamError.Code) != "" || upstreamError.Message != "")
	}
	return false
}

// responsesFailureHasBillableWork reports whether the failed attempt already did
// work the client was charged for. Such an attempt is billable and must not be
// replayed; a zero-usage refusal can be discarded and retried.
func responsesFailureHasBillableWork(event dto.ResponsesStreamResponse) bool {
	if event.Response == nil {
		return false
	}
	if usage := event.Response.Usage; usage != nil {
		if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			return true
		}
	}
	return len(event.Response.Output) > 0
}

// responsesStreamFailure turns an upstream failure event into the relay error
// that decides failover, keeping the structured code so a recognized upstream
// concurrency rejection is named instead of guessed from prose.
func responsesStreamFailure(event dto.ResponsesStreamResponse, raw string) *types.NewAPIError {
	code, errorType, message := event.Code, "", event.Message
	if event.Response != nil {
		switch upstreamError := event.Response.Error.(type) {
		case *types.OpenAIError:
			code, errorType, message = streamErrorField(upstreamError.Code), upstreamError.Type, upstreamError.Message
		case types.OpenAIError:
			code, errorType, message = streamErrorField(upstreamError.Code), upstreamError.Type, upstreamError.Message
		case map[string]any:
			code = streamErrorField(upstreamError["code"])
			errorType = streamErrorField(upstreamError["type"])
			if text := streamErrorField(upstreamError["message"]); text != "" {
				message = text
			}
		}
	}
	if code == "" && errorType == "" && message == "" {
		// A bare {"type":"error"} event carries its payload in the error field.
		var envelope struct {
			Error any `json:"error"`
		}
		if err := common.UnmarshalJsonStr(raw, &envelope); err == nil {
			if upstreamError := dto.GetOpenAIError(envelope.Error); upstreamError != nil {
				code, errorType, message = streamErrorField(upstreamError.Code), upstreamError.Type, upstreamError.Message
			}
		}
	}
	if message == "" {
		message = "upstream stream failed before any output"
	}
	rejection := service.ClassifyUpstreamError(code, errorType, message)
	return types.WithOpenAIError(types.OpenAIError{Code: code, Type: errorType, Message: message}, service.UpstreamRejectionStatusCode(rejection))
}

// responsesStreamTruncation reports a stream that ended without its terminal
// event.
func responsesStreamTruncation(status *relaycommon.StreamStatus) *types.NewAPIError {
	if status == nil {
		return nil
	}
	// An upstream that sent its terminal envelope answered: failed, incomplete
	// and completed are protocol results the client understands, and a client
	// cancellation or a downstream write failure is not an upstream truncation.
	if status.ResponseOutcome() != string(relaycommon.ResponseOutcomeUnknown) {
		return nil
	}
	if !status.OutcomeSnapshot().ExpectsTerminal {
		return nil
	}
	switch status.EndReason {
	case relaycommon.StreamEndReasonEOF, relaycommon.StreamEndReasonDone, relaycommon.StreamEndReasonScannerErr, relaycommon.StreamEndReasonTimeout:
		return types.NewErrorWithStatusCode(errors.New("upstream stream ended before completion"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	default:
		return nil
	}
}

// writeResponsesTerminalError emits the Responses terminal error event for a
// stream that already delivered output, so the client sees a protocol error
// instead of a silently truncated answer.
func writeResponsesTerminalError(c *gin.Context, failure *types.NewAPIError) {
	payload := map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"status": "failed",
			"error":  failure.ToOpenAIError(common.UpstreamAddresses(c)...),
		},
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("event: response.failed\ndata: " + string(body) + "\n\n"))
	helper.FlushWriter(c)
}
func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !ojson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := ojson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}

// streamErrorField renders a structured upstream error code that may arrive as
// a string or as a number.
func streamErrorField(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
