package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func applySystemPromptIfNeeded(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info == nil || request == nil {
		return
	}
	if info.ChannelSetting.SystemPrompt == "" {
		return
	}

	systemRole := request.GetSystemRoleName()

	// A Kimi K3 dynamic tool loading message ({"role":"system","tools":[...]})
	// declares tools rather than a system prompt and must never receive content.
	containSystemPrompt := false
	for _, message := range request.Messages {
		if message.Role == systemRole && len(message.Tools) == 0 {
			containSystemPrompt = true
			break
		}
	}
	if !containSystemPrompt {
		systemMessage := dto.Message{
			Role:    systemRole,
			Content: info.ChannelSetting.SystemPrompt,
		}
		request.Messages = append([]dto.Message{systemMessage}, request.Messages...)
		return
	}

	if !info.ChannelSetting.SystemPromptOverride {
		return
	}

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
	for i, message := range request.Messages {
		if message.Role != systemRole || len(message.Tools) > 0 {
			continue
		}
		if message.IsStringContent() {
			request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
			return
		}
		contents := message.ParseContent()
		contents = append([]dto.MediaContent{
			{
				Type: dto.ContentTypeText,
				Text: info.ChannelSetting.SystemPrompt,
			},
		}, contents...)
		request.Messages[i].Content = contents
		return
	}
}

func textRequestViaResponses(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request any) (*dto.Usage, *types.NewAPIError) {
	// The Codex adaptor applies the configured prompt to instructions itself.
	if info.ApiType != constant.APITypeCodex {
		switch req := request.(type) {
		case *dto.GeneralOpenAIRequest:
			applySystemPromptIfNeeded(c, info, req)
		case *dto.ClaudeRequest:
			applyClaudeSystemPrompt(c, info, req)
		}
	}
	// A chat request takes the outbound plugins and the param override in
	// chat form, before the conversion.
	outboundApplied := false
	if chatRequest, ok := request.(*dto.GeneralOpenAIRequest); ok {
		chatJSON, err := common.Marshal(chatRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		chatJSON, err = relaycommon.RemoveDisabledFields(chatJSON, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		chatJSON, apiErr := applyTextBodyOverrides(c, info, chatJSON)
		if apiErr != nil {
			return nil, apiErr
		}
		outboundApplied = true

		var overriddenChatReq dto.GeneralOpenAIRequest
		if err := common.Unmarshal(chatJSON, &overriddenChatReq); err != nil {
			return nil, types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
		request = &overriddenChatReq
	}

	result, err := service.ConvertRequest(c, info, types.RelayFormatOpenAIResponses, request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	responsesReq, ok := result.Value.(*dto.OpenAIResponsesRequest)
	if !ok {
		return nil, types.NewError(fmt.Errorf("expected OpenAI responses request, got %T", result.Value), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return relayResponsesRequest(c, info, adaptor, responsesReq, outboundApplied)
}

func relayResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, responsesReq *dto.OpenAIResponsesRequest, outboundApplied bool) (*dto.Usage, *types.NewAPIError) {
	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
	}()

	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *responsesReq)
	if err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if !outboundApplied {
		var apiErr *types.NewAPIError
		if jsonData, apiErr = applyTextBodyOverrides(c, info, jsonData); apiErr != nil {
			return nil, apiErr
		}
	}
	if finalizer, ok := adaptor.(channel.ResponsesRequestFinalizer); ok {
		jsonData, err = finalizer.FinalizeResponsesRequest(info, jsonData)
		if err != nil {
			return nil, newConvertRequestFailedError(c, info, err)
		}
	}
	if apiErr := requestplugin.Egress(c, info, jsonData); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := validatePlaygroundWebSearch(c, info, jsonData); apiErr != nil {
		return nil, apiErr
	}
	if info.WebSearchBillingKey == "" {
		var hostedTools struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := common.Unmarshal(jsonData, &hostedTools); err == nil {
			for _, tool := range hostedTools.Tools {
				if tool.Type == dto.BuildInToolWebSearch || tool.Type == dto.BuildInToolWebSearchPreview {
					info.WebSearchBillingKey = tool.Type
					break
				}
			}
		}
	}

	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	jsonData = nil
	var requestBody io.Reader = body

	// Converted Responses requests need the same pre-dispatch comment gate as
	// native Responses. The response handler chooses Hold once the upstream
	// protocol is known; non-stream response conversion still writes through.
	if info.IsStream && !info.IsChannelTest && helper.StreamCommitGateFrom(c) == nil {
		holdBudget := helper.DefaultStreamCommitHoldBudget
		if seconds := service.EffectiveUpstreamFirstByteTimeoutSeconds(info.ChannelSetting); seconds > 0 {
			holdBudget = time.Duration(seconds) * time.Second
		}
		gate := helper.InstallStreamCommitGate(c, holdBudget)
		defer gate.Finish(c)
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return nil, types.NewOpenAIError(errors.New("upstream returned no response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	if info.IsChannelTest {
		// Diagnostics map the final error once, after the raw HTTP refusal is classified.
		statusCodeMappingStr = ""
	}
	httpResp = resp.(*http.Response)
	clientStream := info.IsStream
	upstreamStream := isResponsesEventStreamContentType(httpResp.Header.Get("Content-Type"))
	info.IsStream = clientStream || upstreamStream
	if httpResp.StatusCode != http.StatusOK {
		if info.IsChannelTest {
			return nil, diagnosticHTTPError(c, httpResp)
		}
		helper.StoreUpstreamRetryAfter(c, httpResp.Header.Get("Retry-After"))
		newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}

	if upstreamStream && clientStream {
		usage, newApiErr := openaichannel.OaiResponsesToChatStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}
	if upstreamStream {
		info.IsStream = false
		usage, newApiErr := openaichannel.OaiResponsesToChatBufferedStreamHandler(c, info, httpResp)
		// Diagnostics record parsed usage even when the buffered reply fails.
		// Ordinary relay callers keep their existing error path.
		if newApiErr != nil && !info.IsChannelTest {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, newApiErr
	}

	usage, newApiErr := openaichannel.OaiResponsesToChatHandler(c, info, httpResp)
	if newApiErr != nil {
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}
	return usage, nil
}

func isResponsesEventStreamContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}
