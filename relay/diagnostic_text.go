package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const diagnosticBusyContextKey = "diagnostic_upstream_busy"

// InitChannelDiagnostic initializes the request plugin plan and retry evidence
// for one synthetic attempt, before the request phase runs during model preparation.
func InitChannelDiagnostic(c *gin.Context) {
	c.Set(diagnosticBusyContextKey, false)
	helper.ClearUpstreamRetryAfter(c)
	requestplugin.InitDiagnosticAttempt(c)
}

// DiagnosticUpstreamBusy reports a classified HTTP refusal, not an error
// generated locally or an upstream failure after accepting the request body.
func DiagnosticUpstreamBusy(c *gin.Context) bool {
	return c != nil && c.GetBool(diagnosticBusyContextKey)
}

func diagnosticHTTPError(c *gin.Context, resp *http.Response) *types.NewAPIError {
	helper.StoreUpstreamRetryAfter(c, resp.Header.Get("Retry-After"))
	// Inspect only a complete, bounded refusal. A body carrying work must not
	// become replayable just because it also names a concurrency error.
	const maxErrorBytes = 64 << 10
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes+1))
	if err != nil || len(data) > maxErrorBytes {
		return types.InitOpenAIError(types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
	}
	parsed := *resp
	parsed.Body = io.NopCloser(bytes.NewReader(data))
	apiErr := service.RelayErrorHandler(c.Request.Context(), &parsed, false)
	if resp.StatusCode >= http.StatusBadRequest &&
		service.RecognizedUpstreamRejection(apiErr) == service.UpstreamRejectionConcurrency &&
		isUnprocessedDiagnosticRefusal(data) {
		c.Set(diagnosticBusyContextKey, true)
	}
	return apiErr
}

func isUnprocessedDiagnosticRefusal(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"output", "choices", "content", "candidates", "data", "response.output"} {
		if field := gjson.GetBytes(body, path); field.Exists() && field.Type != gjson.Null && field.Raw != "[]" {
			return false
		}
	}
	// A provider may explicitly send zero usage on a refusal, but nonzero
	// quantities or unfamiliar usage details cannot prove that no work ran.
	for _, path := range []string{"usage", "response.usage", "usageMetadata"} {
		usage := gjson.GetBytes(body, path)
		if !usage.Exists() || usage.Type == gjson.Null {
			continue
		}
		if !usage.IsObject() {
			return false
		}
		zero := true
		usage.ForEach(func(_, value gjson.Result) bool {
			zero = value.Type == gjson.Number && value.Float() == 0
			return zero
		})
		if !zero {
			return false
		}
	}
	return true
}

// RunChannelDiagnostic prepares and sends a synthetic request on an already
// selected channel and key. The caller owns admission and diagnostic settlement.
func RunChannelDiagnostic(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request dto.Request) (any, *types.NewAPIError) {
	isText := false
	switch info.RelayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction,
		types.RelayFormatClaude, types.RelayFormatGemini:
		isText = true
	}
	target := info.RelayFormat
	if isText {
		var apiErr *types.NewAPIError
		target, apiErr = resolveTextUpstreamEndpoint(c, info)
		if apiErr != nil {
			return nil, apiErr
		}
	}

	if info.RelayMode == relayconstant.RelayModeResponsesCompact && !common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return nil, types.NewErrorWithStatusCode(errors.New("responses compaction test is not supported for this channel"), types.ErrorCodeInvalidApiType, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	// The selected Azure adaptor builds its deployment path from RequestURLPath.
	if info.ChannelType == constant.ChannelTypeAzure && info.RelayFormat == types.RelayFormatGemini && target == types.RelayFormatOpenAI {
		info.RequestURLPath = "/v1/chat/completions"
	}
	// Passthrough has no browser body to replay. Use only the synthetic DTO,
	// without system prompt, disabled-field filtering or outbound mutation.
	if isText && (model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled) {
		data, err := common.Marshal(request)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
		}
		storage, err := common.CreateBodyStorage(data)
		if err != nil {
			return nil, newConvertRequestFailedError(c, info, err)
		}
		c.Set(common.KeyBodyStorage, storage)
		defer common.CleanupBodyStorage(c)
		if apiErr := requestplugin.PassThrough(c, info, storage); apiErr != nil {
			return nil, apiErr
		}
		return dispatchChannelDiagnostic(c, info, adaptor, common.NewReplayableBodyReader(storage), isText)
	}

	var converted any
	var err error
	var finalizer channel.ResponsesRequestFinalizer
	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		prepareChatStreamOptions(info, req)
		if target == types.RelayFormatOpenAIResponses {
			return textRequestViaResponses(c, info, adaptor, req)
		}
		applySystemPromptIfNeeded(c, info, req)
		converted, err = adaptor.ConvertOpenAIRequest(c, info, req)
	case *dto.ClaudeRequest:
		if target == types.RelayFormatOpenAIResponses {
			return textRequestViaResponses(c, info, adaptor, req)
		}
		applyClaudeSystemPrompt(c, info, req)
		converted, err = adaptor.ConvertClaudeRequest(c, info, req)
	case *dto.GeminiChatRequest:
		applyGeminiSystemPrompt(c, info, req)
		converted, err = adaptor.ConvertGeminiRequest(c, info, req)
	case *dto.OpenAIResponsesRequest:
		converted, err = convertDiagnosticResponses(c, info, adaptor, *req)
		finalizer, _ = adaptor.(channel.ResponsesRequestFinalizer)
	case *dto.OpenAIResponsesCompactionRequest:
		converted, err = convertDiagnosticResponses(c, info, adaptor, dto.OpenAIResponsesRequest{
			Model: req.Model, Input: req.Input, Instructions: req.Instructions, PreviousResponseID: req.PreviousResponseID,
		})
		finalizer, _ = adaptor.(channel.ResponsesRequestFinalizer)
	case *dto.EmbeddingRequest:
		converted, err = adaptor.ConvertEmbeddingRequest(c, info, *req)
	case *dto.ImageRequest:
		converted, err = adaptor.ConvertImageRequest(c, info, *req)
	case *dto.RerankRequest:
		converted, err = adaptor.ConvertRerankRequest(c, info.RelayMode, *req)
	default:
		return nil, types.NewError(fmt.Errorf("unsupported diagnostic request type %T", request), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	body, closer, apiErr := prepareConvertedTextBody(c, info, converted, isText, finalizer)
	if apiErr != nil {
		return nil, apiErr
	}
	defer closer.Close()
	return dispatchChannelDiagnostic(c, info, adaptor, body, isText)
}

func convertDiagnosticResponses(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request dto.OpenAIResponsesRequest) (any, error) {
	// Codex supplies its own instructions, including its required empty default.
	if info.ApiType != constant.APITypeCodex && info.ChannelSetting.SystemPrompt != "" && len(request.Instructions) == 0 {
		prefix, err := common.Marshal(info.ChannelSetting.SystemPrompt)
		if err != nil {
			return nil, err
		}
		request.Instructions = prefix
	}
	return adaptor.ConvertOpenAIResponsesRequest(c, info, request)
}

func dispatchChannelDiagnostic(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, body common.ReplayableBody, isText bool) (any, *types.NewAPIError) {
	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		var apiErr *types.NewAPIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	var httpResp *http.Response
	if resp != nil {
		var ok bool
		httpResp, ok = resp.(*http.Response)
		if !ok {
			return nil, types.NewError(fmt.Errorf("unexpected diagnostic response type %T", resp), types.ErrorCodeBadResponse)
		}
	}
	if httpResp != nil {
		if httpResp.StatusCode != http.StatusOK {
			return nil, diagnosticHTTPError(c, httpResp)
		}
		if httpResp.Body != nil {
			defer func() { _ = httpResp.Body.Close() }()
		}
		if isText && info.RelayMode != relayconstant.RelayModeResponsesCompact {
			info.IsStream = info.IsStream || isResponsesEventStreamContentType(httpResp.Header.Get("Content-Type"))
		}
	}
	// Keep the adaptor's response conversions, including forced response format.
	return adaptor.DoResponse(c, httpResp, info)
}

// prepareConvertedTextBody applies the shared post-conversion request policy.
// Overrides intentionally run after filtering and outbound, and provider
// finalization precedes egress so hooks see the effective request.
func prepareConvertedTextBody(c *gin.Context, info *relaycommon.RelayInfo, converted any, filter bool, finalizer channel.ResponsesRequestFinalizer) (common.ReplayableBody, io.Closer, *types.NewAPIError) {
	relaycommon.AppendRequestConversionFromRequest(info, converted)
	data, err := common.Marshal(converted)
	if err != nil {
		code := types.ErrorCodeConvertRequestFailed
		if info.IsChannelTest || info.RelayFormat == types.RelayFormatOpenAI {
			code = types.ErrorCodeJsonMarshalFailed
		}
		return nil, nil, types.NewError(err, code, types.ErrOptionWithSkipRetry())
	}
	if filter {
		data, err = relaycommon.RemoveDisabledFields(data, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, nil, newConvertRequestFailedError(c, info, err)
		}
	}
	data, apiErr := applyTextBodyOverrides(c, info, data)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	if finalizer != nil {
		data, err = finalizer.FinalizeResponsesRequest(info, data)
		if err != nil {
			return nil, nil, newConvertRequestFailedError(c, info, err)
		}
	}
	if apiErr := requestplugin.Egress(c, info, data); apiErr != nil {
		return nil, nil, apiErr
	}
	if info.RelayFormat == types.RelayFormatOpenAI {
		if apiErr := validatePlaygroundWebSearch(c, info, data); apiErr != nil {
			return nil, nil, apiErr
		}
	}
	if !info.IsChannelTest {
		logger.LogDebug(c, "requestBody: %s", data)
	}
	body, closer, err := relaycommon.NewOutboundJSONBody(data)
	if err != nil {
		return nil, nil, newConvertRequestFailedError(c, info, err)
	}
	return body, closer, nil
}

func applyTextBodyOverrides(c *gin.Context, info *relaycommon.RelayInfo, data []byte) ([]byte, *types.NewAPIError) {
	data, apiErr := requestplugin.Outbound(c, info, data)
	if apiErr != nil {
		return nil, apiErr
	}
	if len(info.ParamOverride) > 0 {
		var err error
		data, err = relaycommon.ApplyParamOverrideWithRelayInfo(data, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}
	return data, nil
}
