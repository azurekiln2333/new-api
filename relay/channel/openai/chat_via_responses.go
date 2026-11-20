package openai

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func OaiResponsesToChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var responsesResp dto.OpenAIResponsesResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	if err := common.Unmarshal(body, &responsesResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := responsesResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(responsesResp.Model)
	responseValue, usage, err := convertResponsesResponseForClient(c, info, &responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	tools := responsesToolUsage{}
	tools.observe(info, &dto.ResponsesStreamResponse{Type: "response.completed", Response: &responsesResp}, body)
	return usage, nil
}

func OaiResponsesToChatBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()
	accumulator := relayconvert.NewResponsesBufferedAccumulator()
	tools := responsesToolUsage{}
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
	var knownUsage *dto.Usage

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 6 || line[:5] != "data:" {
			continue
		}
		data := line[5:]
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				break
			}
			continue
		}

		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal buffered responses stream event: %s", err.Error()))
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			break
		}
		if streamResp.Response != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
			if streamResp.Response.Usage != nil {
				// Preserve only reported accounting facts on failures. Response
				// conversion may estimate missing usage, so it cannot supply them.
				knownUsage = dto.MergeUsageNonZero(knownUsage, relayconvert.UsageFromResponsesUsage(streamResp.Response.Usage))
				service.ApplyResponsesUsage(knownUsage, streamResp.Response.Usage)
			}
		}
		service.ObserveResponsesOutcome(info, &streamResp)
		tools.observe(info, &streamResp, common.StringToByteSlice(data))
		accumulator.ProcessEvent(&streamResp)
		switch streamResp.Type {
		case "response.completed", "response.done", "response.incomplete":
			finalResponse = streamResp.Response
			if streamResp.Type == "response.incomplete" {
				if finalResponse == nil {
					finalResponse = &dto.OpenAIResponsesResponse{}
				}
				if len(finalResponse.Status) == 0 {
					finalResponse.Status = []byte(`"incomplete"`)
				}
			}
		case "response.failed", "response.error":
			if streamResp.Response != nil {
				if oaiErr := streamResp.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError)
					break
				}
			}
			streamErr = types.NewOpenAIError(fmt.Errorf("responses stream error: %s", streamResp.Type), types.ErrorCodeBadResponse, http.StatusInternalServerError)
		}
		if streamErr != nil || finalResponse != nil {
			break
		}
	}
	if streamErr != nil {
		return knownUsage, streamErr
	}
	if err := scanner.Err(); err != nil {
		return knownUsage, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if finalResponse == nil {
		if info.IsChannelTest {
			return knownUsage, types.NewOpenAIError(fmt.Errorf("upstream stream ended before completion"), types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		finalResponse = &dto.OpenAIResponsesResponse{
			ID:        helper.GetResponseID(c),
			CreatedAt: dto.IntValue(time.Now().Unix()),
			Model:     info.UpstreamModelName,
			Status:    []byte(`"completed"`),
		}
	}
	accumulator.SupplementResponseOutput(finalResponse)

	responseValue, usage, err := convertResponsesResponseForClient(c, info, finalResponse)
	if err != nil {
		return knownUsage, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responseBody, err := common.Marshal(responseValue)
	if err != nil {
		return knownUsage, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func convertResponsesResponseForClient(c *gin.Context, info *relaycommon.RelayInfo, response *dto.OpenAIResponsesResponse) (any, *dto.Usage, error) {
	if responseID := helper.GetResponseID(c); responseID != "" {
		response.ID = responseID
	}

	usage := relayconvert.UsageFromResponsesUsage(response.Usage)
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(response)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		response.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	result, err := service.ConvertResponse(c, info, info.RelayFormat, response)
	if err != nil {
		return nil, nil, err
	}
	if result.Usage != nil && result.Usage.TotalTokens != 0 {
		usage = result.Usage
	}
	return result.Value, usage, nil
}

func OaiResponsesToChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseId := helper.GetResponseID(c)
	createAt := time.Now().Unix()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAIResponses, info.RelayFormat, relayconvert.ResponseStreamOptions{
		ID:      responseId,
		Model:   info.UpstreamModelName,
		Created: createAt,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	// Install after the response plugin so held framing cannot reach its
	// capture/envelope writers and accidentally make this attempt non-retryable.
	gate := helper.StreamCommitGateFrom(c)
	if gate == nil {
		holdBudget := helper.DefaultStreamCommitHoldBudget
		if info.ChannelMeta != nil {
			if seconds := service.EffectiveUpstreamFirstByteTimeoutSeconds(info.ChannelSetting); seconds > 0 {
				holdBudget = time.Duration(seconds) * time.Second
			}
		}
		gate = helper.InstallStreamCommitGate(c, holdBudget)
		defer gate.Finish(c)
	}
	gate.Hold()

	var streamErr *types.NewAPIError
	knownUsage := &dto.Usage{}
	var observedText strings.Builder
	sawWork := false
	tools := responsesToolUsage{}
	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo == nil {
		info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}
	}

	writeFailed := func(err error) bool {
		if c.Request.Context().Err() != nil {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
		return false
	}

	sendStreamResult := func(result relayconvert.ResponseResult) bool {
		var eventName string
		switch value := result.Value.(type) {
		case dto.ChatCompletionsStreamResponse:
			if len(value.Choices) == 0 && value.Usage == nil {
				return true
			}
		case *dto.ChatCompletionsStreamResponse:
			if value == nil || (len(value.Choices) == 0 && value.Usage == nil) {
				return true
			}
		case dto.ClaudeResponse:
			eventName = value.Type
		case *dto.ClaudeResponse:
			if value == nil {
				return true
			}
			eventName = value.Type
		case dto.GeminiChatResponse:
		case *dto.GeminiChatResponse:
			if value == nil {
				return true
			}
		default:
			streamErr = types.NewOpenAIError(fmt.Errorf("unsupported converted stream response type %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		if err := c.Request.Context().Err(); err != nil {
			return writeFailed(err)
		}
		payload, err := common.Marshal(result.Value)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		frame := "data: " + string(payload) + "\n\n"
		if eventName != "" {
			frame = "event: " + eventName + "\n" + frame
		}
		// Render helpers that discard Write/Flush errors cannot be used here:
		// a failed downstream write must retain upstream usage and stop output.
		if _, err := c.Writer.Write([]byte(frame)); err != nil {
			return writeFailed(err)
		}
		if err := helper.FlushWriter(c); err != nil {
			return writeFailed(err)
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var streamResp dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResp); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
			stopStream(sr, streamErr)
			return
		}

		// Accounting facts belong to the upstream event, even when conversion,
		// committing held framing, or writing the converted event fails next.
		service.ObserveResponsesOutcome(info, &streamResp)
		if streamResp.Response != nil {
			info.ObserveResponseModel(streamResp.Response.Model)
			if streamResp.Response.Usage != nil {
				knownUsage = dto.MergeUsageNonZero(knownUsage, relayconvert.UsageFromResponsesUsage(streamResp.Response.Usage))
				service.ApplyResponsesUsage(knownUsage, streamResp.Response.Usage)
			}
			if observedText.Len() == 0 && len(streamResp.Response.Output) > 0 {
				observedText.WriteString(relayconvert.ExtractOutputTextFromResponses(streamResp.Response))
				observedText.WriteString(relayconvert.ExtractReasoningTextFromResponses(streamResp.Response))
				for _, item := range streamResp.Response.Output {
					observedText.WriteString(item.ArgumentsString())
				}
			}
		}
		switch streamResp.Type {
		case "response.output_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
			"response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
			observedText.WriteString(streamResp.Delta)
		}
		failed := isResponsesFailureEvent(streamResp)
		toolEvent := streamResp
		if failed {
			toolEvent.Type = "response.failed"
		}
		tools.observe(info, &toolEvent, common.StringToByteSlice(data))
		sawWork = sawWork || knownUsage.TotalTokens > 0 || knownUsage.PromptTokens > 0 || knownUsage.CompletionTokens > 0 ||
			responsesFailureHasBillableWork(streamResp) || (!failed && isResponsesSubstantiveEvent(streamResp))
		if failed {
			streamErr = responsesStreamFailure(streamResp, data)
			stopStream(sr, streamErr)
			return
		}
		if sawWork || info.StreamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeUnknown) {
			if err := gate.Commit(); err != nil {
				writeFailed(err)
				stopStream(sr, streamErr)
				return
			}
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &streamResp)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			stopStream(sr, streamErr)
			return
		}
		for _, result := range results {
			if !sendStreamResult(result) {
				stopStream(sr, streamErr)
				return
			}
		}
		if info.StreamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeUnknown) {
			sr.Done()
		}
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	info.StreamStatus.RequireTerminal()
	if streamErr == nil {
		streamErr = responsesStreamTruncation(info.StreamStatus)
	}
	if streamErr == nil && !info.StreamStatus.IsNormalEnd() {
		endErr := info.StreamStatus.EndError
		if endErr == nil {
			endErr = fmt.Errorf("upstream stream stopped: %s", info.StreamStatus.EndReason)
		}
		streamErr = types.NewOpenAIError(endErr, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	usage := dto.MergeUsageNonZero(state.Usage(), knownUsage)
	text := observedText.String()
	if convertedText := state.UsageText(); len(convertedText) > len(text) {
		text = convertedText
	}
	if sawWork && (usage.PromptTokens == 0 || (usage.CompletionTokens == 0 && text != "")) {
		estimate := service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		if usage.PromptTokens == 0 {
			usage.PromptTokens = estimate.PromptTokens
		}
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = estimate.CompletionTokens
		}
		usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
		if usage.BillingUsage != nil {
			usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
		}
	}
	state.SetUsage(usage)

	finishFailure := func(failure *types.NewAPIError) (*dto.Usage, *types.NewAPIError) {
		info.StreamStatus.MarkFailed(string(failure.GetErrorCode()), string(failure.GetErrorType()), failure.StatusCode)
		if !sawWork && !gate.Committed() {
			gate.Discard()
			return nil, failure
		}
		// Returning an error makes the host refund this attempt. Once work or
		// output exists, preserve settlement and narrate failure in the stream.
		if c.Request.Context().Err() == nil {
			if err := gate.Commit(); err == nil {
				failure.SetMessage(common.MessageWithRequestId(failure.Error(), c.GetString(common.RequestIdKey)))
				writeConvertedTerminalError(c, info, failure)
			} else {
				info.StreamStatus.RecordError(err.Error())
			}
		}
		return usage, nil
	}
	if streamErr != nil {
		helper.StoreUpstreamRetryAfter(c, resp.Header.Get("Retry-After"))
		return finishFailure(streamErr)
	}

	if err := gate.Commit(); err != nil {
		return finishFailure(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
	}
	if info.RelayFormat == types.RelayFormatClaude && info.ClaudeConvertInfo != nil {
		info.ClaudeConvertInfo.Usage = usage
	}
	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return finishFailure(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
	}
	for _, result := range finalResults {
		if !sendStreamResult(result) {
			return finishFailure(streamErr)
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAI && info.ShouldIncludeUsage {
		result := relayconvert.ResponseResult{Value: helper.GenerateFinalUsageResponse(responseId, createAt, info.UpstreamModelName, *usage)}
		if !sendStreamResult(result) {
			return finishFailure(streamErr)
		}
	}
	if info.RelayFormat == types.RelayFormatOpenAI {
		if _, err := c.Writer.Write([]byte("data: [DONE]\n\n")); err != nil {
			return finishFailure(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
		}
		if err := helper.FlushWriter(c); err != nil {
			return finishFailure(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
		}
	}
	return usage, nil
}

// responsesToolUsage preserves actual hosted-tool calls when a Responses reply
// is converted to another client protocol. Terminal-only outputs are supported;
// an item repeated in the terminal response is counted only once.
type responsesToolUsage struct {
	seen map[string]struct{}
}

func (u *responsesToolUsage) observe(info *relaycommon.RelayInfo, event *dto.ResponsesStreamResponse, raw []byte) {
	switch event.Type {
	case dto.ResponsesOutputTypeItemDone:
		u.observeItem(info, event.Item, event.OutputIndex)
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		if event.Response != nil {
			for i := range event.Response.Output {
				u.observeItem(info, &event.Response.Output[i], &i)
			}
		}
		// Vendor billing facts take precedence over protocol item counts.
		info.ApplyVendorToolUsage(raw)
	}
}

func (u *responsesToolUsage) observeItem(info *relaycommon.RelayInfo, item *dto.ResponsesOutput, index *int) {
	if item == nil {
		return
	}
	switch item.Type {
	case dto.BuildInCallWebSearchCall, dto.BuildInCallFileSearchCall, dto.BuildInCallFunctionCall:
	default:
		return
	}
	aliases := make([]string, 0, 2)
	if item.ID != "" {
		aliases = append(aliases, "id:"+item.ID)
	}
	if index != nil && *index >= 0 {
		aliases = append(aliases, fmt.Sprintf("index:%d", *index))
	}
	if u.seen == nil {
		u.seen = make(map[string]struct{})
	}
	for _, alias := range aliases {
		if _, exists := u.seen[alias]; exists {
			return
		}
	}
	for _, alias := range aliases {
		u.seen[alias] = struct{}{}
	}
	info.CountBillableToolCall(item.Type, item.Name)
}

// writeConvertedTerminalError emits the incoming protocol's terminal error
// frame on an open stream. Writing a JSON body here would corrupt the SSE the
// client is already reading.
func writeConvertedTerminalError(c *gin.Context, info *relaycommon.RelayInfo, streamErr *types.NewAPIError) {
	if info.RelayFormat == types.RelayFormatClaude {
		payload, err := common.Marshal(gin.H{"type": "error", "error": streamErr.ToClaudeError(common.UpstreamAddresses(c)...)})
		if err != nil {
			return
		}
		_, _ = c.Writer.Write([]byte("event: error\ndata: " + string(payload) + "\n\n"))
		_ = helper.FlushWriter(c)
		return
	}
	payload, err := common.Marshal(gin.H{"error": streamErr.ToOpenAIError(common.UpstreamAddresses(c)...)})
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("data: " + string(payload) + "\n\n"))
	_ = helper.FlushWriter(c)
}
