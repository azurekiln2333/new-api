package service

import (
	"errors"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// IsChannelFailure includes upstream response errors wrapped by the gateway,
// while excluding local validation, pricing and quota rejections.
func IsChannelFailure(err *types.NewAPIError) bool {
	if err == nil || IsChannelCapacityError(err) {
		return false
	}
	if IsUpstreamFirstByteTimeout(err) || err.GetErrorCode() == types.ErrorCodeUpstreamFirstByteTimeout {
		return true
	}
	if types.IsChannelError(err) || err.GetErrorType() != types.ErrorTypeNewAPIError {
		return true
	}
	switch err.GetErrorCode() {
	case types.ErrorCodeDoRequestFailed, types.ErrorCodeReadResponseBodyFailed,
		types.ErrorCodeBadResponseStatusCode, types.ErrorCodeBadResponse,
		types.ErrorCodeBadResponseBody, types.ErrorCodeEmptyResponse, types.ErrorCodeAwsInvokeError:
		return true
	default:
		return false
	}
}

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if IsChannelCapacityError(err) {
		return PolicyDecision{Action: "stop", Reason: string(err.GetErrorCode()), Source: "capacity"}
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return PolicyDecision{Action: "stop", Reason: "client_cancelled", Source: "system"}
	}
	rejection := RecognizedUpstreamRejection(err)
	if rejection == UpstreamRejectionConcurrency && c != nil {
		state := RequestPolicy(c)
		channelID := state.CurrentChannelID
		if channelID <= 0 {
			channelID = c.GetInt("channel_id")
		}
		if channelID > 0 && !slices.Contains(state.UpstreamBusyChannelIDs, channelID) {
			state.UpstreamBusyChannelIDs = append(state.UpstreamBusyChannelIDs, channelID)
		}
	}
	// A credential that needs a new login only leaves another candidate on a
	// multi-key channel. A single-key channel remains a failed channel.
	keyCanFailOver := c != nil && common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) && errors.Is(err, ErrPluginCredentialNeedsLogin)
	if IsChannelFailure(err) && !keyCanFailOver {
		RecordChannelFailure(c)
	}
	if c != nil && c.Writer != nil && c.Writer.Written() {
		return PolicyDecision{Action: "stop", Reason: "response_started", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	if IsUpstreamFirstByteTimeout(err) || err.GetErrorCode() == types.ErrorCodeUpstreamFirstByteTimeout {
		return PolicyDecision{Action: "retry", Reason: "upstream_first_byte_timeout", Source: "system"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		// Name a recognized refusal so logs distinguish a missing model from an
		// ordinary retryable status. The retry outcome still follows the
		// configured status ranges.
		recordedModelError, _ := c.Get("relay_model_unavailable_error")
		if recordedModelError == err || ClassifyUpstreamModelUnavailable(err, "") != UpstreamModelUnavailableNone {
			return PolicyDecision{Action: "retry", Reason: "upstream_model_unavailable", Source: "upstream"}
		}
		// Name a recognized refusal so logs distinguish an upstream
		// concurrency rejection from a general rate limit or quota exhaustion.
		// The retry outcome still follows the configured status ranges.
		if rejection != UpstreamRejectionNone {
			return PolicyDecision{Action: "retry", Reason: string(rejection), Source: "upstream"}
		}
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil || IsChannelCapacityError(err) {
		return
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return
	}
	upstreamURLs := common.UpstreamAddresses(c)
	logger.LogError(c, common.LogText("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode(upstreamURLs...))))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode(upstreamURLs...)
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	RecordRelayErrorLog(c, channelError.ChannelId, err, relayInfo)
	RecordUpstreamModelUnavailability(c, channelError, err, relayInfo)
}

// RecordUpstreamModelUnavailability remembers that the attempted credential of a
// channel cannot serve the model, so later requests skip that combination. Only
// a definite upstream 404/410 refusal with a real envelope qualifies, and only
// before any output was delivered: local errors, endpoint or parameter
// refusals, ambiguous text and post-output failures never persist state. The
// stored entry is validated against the current credential and mapping, so a
// late response cannot mark a replaced key or a changed mapping.
func RecordUpstreamModelUnavailability(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil || relayInfo.ChannelMeta == nil || channelError.ChannelId <= 0 {
		return
	}
	if c != nil && c.Writer != nil && c.Writer.Written() {
		return
	}
	evidence := ClassifyUpstreamModelUnavailable(err, relayInfo.UpstreamModelName)
	if evidence == UpstreamModelUnavailableNone {
		return
	}
	// Remember only this exact, classified attempt error for its retry reason.
	// A later error in the same request cannot inherit the classification.
	if c != nil {
		c.Set("relay_model_unavailable_error", err)
	}
	originModel := relayInfo.OriginModelName
	attemptMapping := ""
	if c != nil {
		if originModel == "" {
			originModel = c.GetString("original_model")
		}
		attemptMapping = c.GetString("model_mapping")
	}
	if _, recordErr := model.RecordChannelModelUnavailability(channelError.ChannelId, originModel, channelError.UsingKey, attemptMapping, relayInfo.UpstreamModelName, err.StatusCode, string(evidence)); recordErr != nil {
		logger.LogError(c, common.LogText("failed to record model unavailability (channel #%d): %v", channelError.ChannelId, recordErr))
	}
}

// RecordRelayErrorLog writes the error log of a failed relay request when
// error logs are on. channelId is 0 for a request that reached no channel.
func RecordRelayErrorLog(c *gin.Context, channelId int, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	userId := c.GetInt("id")
	tokenName := c.GetString("token_name")
	modelName := c.GetString("original_model")
	tokenId := c.GetInt("token_id")
	userGroup := c.GetString("group")
	other := model.NewLogOther()
	if c.Request != nil && c.Request.URL != nil {
		other.SetPublic("request_path", c.Request.URL.Path)
	}
	other.SetPublic("error_type", err.GetErrorType())
	other.SetPublic("error_code", err.GetErrorCode())
	other.SetPublic("status_code", err.StatusCode)
	AppendRelayLogAdminInfo(c, relayInfo, other)
	appendRequestConversionChain(relayInfo, other)
	appendFinalRequestFormat(relayInfo, other)
	appendRequestPluginRecord(relayInfo, other)
	AppendResponseModelLogInfo(relayInfo, other)
	AppendTaskPluginContextAuditInfo(c, other)
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	useTimeSeconds := int(time.Since(startTime).Seconds())
	model.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(common.UpstreamAddresses(c)...), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
}
