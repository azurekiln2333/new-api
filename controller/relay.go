package controller

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	case relayconstant.RelayModeAlphaSearch:
		err = relay.AlphaSearchHelper(c, info)
	case relayconstant.RelayModeToolsSearch, relayconstant.RelayModeToolsFetch:
		err = relay.DeepSeekToolsHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	defer func() {
		if ws != nil {
			_ = ws.Close()
		}
	}()

	var relayInfo *relaycommon.RelayInfo
	var ingressRecord []relaycommon.RequestPluginRecord
	// Registered first so that it runs last: the request plugins' report
	// hooks receive the request once the client has the whole response and
	// the request has settled.
	defer func() {
		requestplugin.Report(c, relayFormat, relayInfo, ingressRecord, newAPIError)
	}()
	var streamEnvelope *helper.StreamCommentEnvelope
	// Keep the sink open through final error rendering, including a report
	// capture installed outside it by the selected channel's Reply plugins.
	defer func() {
		if streamEnvelope != nil {
			streamEnvelope.Finish(c)
		}
	}()

	defer func() {
		if newAPIError != nil {
			if service.IsUpstreamFirstByteTimeout(newAPIError) || newAPIError.GetErrorCode() == types.ErrorCodeUpstreamFirstByteTimeout {
				newAPIError.SetMessage(i18n.T(c, i18n.MsgRelayUpstreamFirstByteTimeout))
			}
			service.RecordRequestPolicyTermination(c, newAPIError)
			logger.LogError(c, common.LogText("relay error: %s", common.LocalLogPreview(newAPIError.Error())))
			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
			upstreamURLs := common.UpstreamAddresses(c)
			streamStarted := c.Writer.Written()
			if streamEnvelope != nil {
				streamStarted = streamStarted || streamEnvelope.ConnectionWritten()
			}
			if relayFormat != types.RelayFormatOpenAIRealtime && streamStarted &&
				strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
				writeRelayStreamError(c, relayFormat, newAPIError)
				return
			}
			if relayFormat != types.RelayFormatOpenAIRealtime {
				c.Header("Content-Type", "application/json; charset=utf-8")
			}
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				if ws != nil {
					helper.WssError(c, ws, newAPIError.ToOpenAIError(upstreamURLs...))
				} else {
					c.JSON(newAPIError.StatusCode, gin.H{"error": newAPIError.ToOpenAIError(upstreamURLs...)})
				}
			case types.RelayFormatClaude:
				c.JSON(newAPIError.StatusCode, gin.H{
					"type":  "error",
					"error": newAPIError.ToClaudeError(upstreamURLs...),
				})
			default:
				c.JSON(newAPIError.StatusCode, gin.H{
					"error": newAPIError.ToOpenAIError(upstreamURLs...),
				})
			}
		}
	}()

	// Ingress takes the request plugin plan and may patch the stored body
	// before it is parsed. A Realtime session runs its instances on its
	// events instead.
	var ingressSteps []relaycommon.RequestPluginStep
	if relayFormat == types.RelayFormatOpenAIRealtime {
		requestplugin.StartRealtime(c)
	} else if ingressSteps, ingressRecord, newAPIError = requestplugin.Ingress(c, relayFormat); newAPIError != nil {
		// The request reached no channel; its error log shows what the
		// plugins did.
		ingress := &relaycommon.RelayInfo{}
		ingress.SetRequestPluginIngress(ingressSteps, ingressRecord)
		service.RecordRelayErrorLog(c, 0, newAPIError, ingress)
		return
	}

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
		}
		return
	}

	relayInfo, err = relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}
	relayInfo.SetRequestPluginIngress(ingressSteps, ingressRecord)

	defer func() {
		recovered := recover()
		resultErr := newAPIError
		if recovered != nil {
			resultErr = types.NewError(fmt.Errorf("relay panic: %v", recovered), types.ErrorCodeBadResponse)
		}
		if relayFormat != types.RelayFormatOpenAIRealtime && !service.IsChannelCapacityError(resultErr) {
			perfmetrics.RecordRelayResult(c.Request.Context(), relayInfo, resultErr)
		}
		if recovered != nil {
			panic(recovered)
		}
	}()

	billingPrepared := false
	defer func() {
		newAPIError = relay.RefundFailedRequestBilling(c, relayInfo, newAPIError)
	}()
	// The request-wide sink serializes comments and output below attempt/plugin
	// wrappers. Comment-only writes keep an idle connection alive and retryable.
	if relayInfo.IsStream && relayFormat != types.RelayFormatOpenAIRealtime {
		streamEnvelope = helper.InstallStreamCommentEnvelope(c)
	}
	defer func() {
		if waiter := channelWaiterFrom(c); waiter != nil {
			waiter.Close()
		}
	}()

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil

	for retryParam.GetRetry() <= common.RetryTimes {
		relayInfo.StreamStatus = nil
		relayInfo.PerformanceBusinessRejection = false
		relayInfo.PerformanceOutputTokens = 0
		relayInfo.RetryIndex = retryParam.GetRetry()
		channel, lease, channelErr := getChannelWithCapacity(c, relayInfo, retryParam)
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			if newAPIError == nil || service.IsChannelCapacityError(channelErr) || c.Request.Context().Err() != nil {
				newAPIError = channelErr
			}
			break
		}
		defer lease.Release() // protects setup panics before the attempt scope
		service.AppendUsedChannel(c, channel.Id)
		newAPIError = func() (attemptErr *types.NewAPIError) {
			// Retry-After belongs to this attempt, never a previous channel.
			helper.ClearUpstreamRetryAfter(c)
			originalRequest := c.Request
			c.Request = originalRequest.WithContext(lease.Context())
			defer func() {
				completed := attemptErr == nil && (relayInfo.StreamStatus == nil || relayInfo.StreamStatus.IsNormalEnd() && !relayInfo.StreamStatus.HasErrors())
				// A nil handler error may carry settled partial usage. A lease
				// failure must not turn that result into a refundable retry.
				if coordinationErr := lease.Err(); coordinationErr != nil && !completed && attemptErr != nil {
					attemptErr = coordinationErr
				}
				// Report the upstream state before releasing the slot: a release
				// wakes the next queued probe, so a late backoff would let that
				// probe race ahead of the recorded busy state.
				if attemptErr != nil && service.ClassifyUpstreamRejection(attemptErr) == service.UpstreamRejectionConcurrency {
					retryAfter := service.ParseChannelRetryAfter(helper.UpstreamRetryAfter(c), time.Now())
					if coordinationErr := service.ReportChannelUpstreamBusy(lease.Context(), channel.Id, retryAfter); coordinationErr != nil {
						attemptErr = coordinationErr
					}
				} else if completed {
					lease.MarkUpstreamAvailable()
				}
				lease.Release()
				c.Request = originalRequest
			}()
			settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
			firstByteTimeout := service.EffectiveUpstreamFirstByteTimeoutSeconds(settings)
			settings.UpstreamFirstByteTimeoutSeconds = &firstByteTimeout
			common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
			generalSettings := operation_setting.GetGeneralSetting()
			if relayFormat == types.RelayFormatClaude && relayInfo.IsStream && generalSettings.PingIntervalEnabled && firstByteTimeout == 0 {
				stopKeepAlive := helper.StartStreamKeepAlive(c, time.Duration(generalSettings.PingIntervalSeconds)*time.Second)
				defer stopKeepAlive()
			}
			if !billingPrepared {
				if billingErr := relay.PrepareRequestBilling(c, relayInfo); billingErr != nil {
					return billingErr
				}
				billingPrepared = true
			}
			if billingErr := service.PrepareTieredBillingForSelectedGroup(c, relayInfo); billingErr != nil {
				return billingErr
			}

			bodyStorage, bodyErr := common.GetBodyStorage(c)
			if bodyErr != nil {
				// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
				if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
					return types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
				} else {
					return types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
			}
			c.Request.Body = io.NopCloser(bodyStorage)

			// Restore the writer after this attempt, before its capacity lease ends.
			finishReply := requestplugin.Reply(c, relayFormat, relayInfo)
			defer finishReply()
			// Protocol-aware handlers opt into Hold after upstream selection.
			// A Responses request converted from a different upstream protocol
			// keeps writing through unless that handler can safely discard it.
			if relayInfo.IsStream && relayFormat == types.RelayFormatOpenAIResponses {
				gate := helper.InstallStreamCommitGate(c, streamCommitHoldBudget(settings))
				defer gate.Finish(c)
			}
			switch relayFormat {
			case types.RelayFormatOpenAIRealtime:
				if ws == nil {
					var upgradeErr error
					ws, upgradeErr = upgrader.Upgrade(c.Writer, c.Request, nil)
					if upgradeErr != nil {
						return types.NewError(upgradeErr, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
					}
					relayInfo.ClientWs = ws
				}
				return relay.WssHelper(c, relayInfo)
			case types.RelayFormatClaude:
				return relay.ClaudeHelper(c, relayInfo)
			case types.RelayFormatGemini:
				return geminiRelayHandler(c, relayInfo)
			default:
				return relayHandler(c, relayInfo)
			}
		}()

		if newAPIError == nil {
			service.MarkRequestPolicySuccess(c, relayInfo.StreamStatus)
			relayInfo.LastError = nil
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError
		if relayFormat != types.RelayFormatOpenAIRealtime && !c.Writer.Written() && c.Request.Context().Err() == nil &&
			service.ClassifyUpstreamRejection(newAPIError) == service.UpstreamRejectionConcurrency {
			state := service.RequestPolicy(c)
			if !slices.Contains(state.UpstreamBusyChannelIDs, channel.Id) {
				state.UpstreamBusyChannelIDs = append(state.UpstreamBusyChannelIDs, channel.Id)
			}
			rememberChannelWaitCandidate(c, channel.Id, relayInfo.UsingGroup)
			service.RecordPolicyFailure(c, channel.Id, newAPIError, service.PolicyDecision{Action: "wait", Reason: "upstream_concurrency", Source: "capacity"})
			// A recognized refusal did no billable work. Try the remaining
			// candidates, then park under the same fixed request deadline.
			// This does not consume the ordinary upstream-error retry budget.
			continue
		}

		decision := service.DecideRelayRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry())
		service.RecordPolicyFailure(c, channel.Id, newAPIError, decision)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError, relayInfo)

		if decision.Action != "retry" {
			break
		}
		retryParam.IncreaseRetry()
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
}

func writeRelayStreamError(c *gin.Context, format types.RelayFormat, apiError *types.NewAPIError) {
	// 心跳提交响应头后，最终错误继续通过同一个流返回。
	status, _ := common.GetContextKeyType[*relaycommon.StreamStatus](c, constant.ContextKeyResponseStreamStatus)
	if status == nil {
		status = relaycommon.NewStreamStatus()
		common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
	}
	status.MarkFailed(string(apiError.GetErrorCode()), string(apiError.GetErrorType()), apiError.StatusCode)
	upstreamURLs := common.UpstreamAddresses(c)
	if format == types.RelayFormatClaude {
		_ = helper.ClaudeData(c, dto.ClaudeResponse{Type: "error", Error: apiError.ToClaudeError(upstreamURLs...)})
		return
	}
	if format == types.RelayFormatOpenAIResponses {
		payload := gin.H{"type": "response.failed", "response": gin.H{
			"status": "failed", "error": apiError.ToOpenAIError(upstreamURLs...),
		}}
		body, err := common.Marshal(payload)
		if err == nil {
			_, _ = c.Writer.Write([]byte("event: response.failed\ndata: " + string(body) + "\n\n"))
			_ = helper.FlushWriter(c)
		}
		return
	}
	_ = helper.ObjectData(c, gin.H{"error": apiError.ToOpenAIError(upstreamURLs...)})
	helper.Done(c)
}

// CountClaudeTokens implements Anthropic's token-counting utility endpoint.
// It deliberately skips upstream generation and billing; callers use this
// endpoint to size prompts before creating a Message.
func CountClaudeTokens(c *gin.Context) {
	request, err := helper.GetAndValidateClaudeRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	info := relaycommon.GenRelayInfoClaude(c, request)
	inputTokens, err := service.CountRequestToken(c, request.GetTokenCountMeta(), info)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime", "responses"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	if info.ChannelMeta == nil && len(service.RequestPolicy(c).SaturatedChannelIDs) == 0 && len(service.RequestPolicy(c).UpstreamBusyChannelIDs) == 0 {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		channel := &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}
		info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)
		service.RequestPolicy(c).BeginAttempt(channel, info.UsingGroup)
		return channel, nil
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryGetChannelFailed, map[string]any{"Group": selectGroup, "Model": info.OriginModelName, "Error": err.Error()})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryNoAvailableChannel, map[string]any{"Group": selectGroup, "Model": info.OriginModelName})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)

	service.RequestPolicy(c).BeginAttempt(channel, selectGroup)
	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	service.ProcessChannelError(c, channelError, err, relayInfo)
}

// Capacity traversal does not advance RetryParam: no upstream attempt has run
// yet. Busy channels remain excluded even when failed-channel avoidance is off.
func getChannelWithCapacity(c *gin.Context, info *relaycommon.RelayInfo, retry *service.RetryParam) (*model.Channel, *service.ChannelCapacityLease, *types.NewAPIError) {
	var lastBusy *types.NewAPIError
	state := service.RequestPolicy(c)
	for {
		_, pinned, _ := service.GetChannelConstraints(c).ResolvedPin()
		if len(state.UpstreamBusyChannelIDs) > 0 && (pinned || state.SessionMode == "strict" || service.ShouldSkipRetryAfterChannelAffinityFailure(c)) {
			grant, channel, waitErr := waitForChannelGrant(c, info)
			if grant != nil {
				return channel, grant.Lease, nil
			}
			return nil, nil, waitErr
		}
		channel, err := getChannel(c, info, retry)
		if err != nil {
			if lastBusy == nil && len(state.UpstreamBusyChannelIDs) == 0 {
				return nil, nil, err
			}
			if grant, grantedChannel, waitErr := waitForChannelGrant(c, info); grant != nil {
				return grantedChannel, grant.Lease, nil
			} else {
				return nil, nil, waitErr
			}
		}
		lease, err := service.AcquireRequestChannelCapacity(c, channel.Id)
		if err == nil {
			return channel, lease, nil
		}
		if err.GetErrorCode() != types.ErrorCodeChannelCapacitySaturated {
			return nil, nil, err
		}
		rememberChannelWaitCandidate(c, channel.Id, info.UsingGroup)
		if !service.RetryChannelAdmission(c, channel.Id, err, retry) {
			if grant, grantedChannel, waitErr := waitForChannelGrant(c, info); grant != nil {
				return grantedChannel, grant.Lease, nil
			} else {
				return nil, nil, waitErr
			}
		}
		lastBusy = err
	}
}

type channelWaitSelection struct {
	channelID int
	group     string
}

const channelWaitCandidatesContextKey = "channel_wait_candidates"

// Keep the group that authorized each busy candidate: auto-group traversal can
// change the current group before a reservation is finally available.
func rememberChannelWaitCandidate(c *gin.Context, channelID int, group string) {
	value, _ := c.Get(channelWaitCandidatesContextKey)
	selections, _ := value.([]channelWaitSelection)
	if !slices.ContainsFunc(selections, func(candidate channelWaitSelection) bool { return candidate.channelID == channelID }) {
		selections = append(selections, channelWaitSelection{channelID: channelID, group: group})
		c.Set(channelWaitCandidatesContextKey, selections)
	}
}

// waitForChannelGrant asks the capacity queue for a slot on one of the channels
// that were busy for this request. The grant is not an authorization: the
// channel is re-validated here, and a grant that no longer fits is released.
func waitForChannelGrant(c *gin.Context, info *relaycommon.RelayInfo) (*service.ChannelWaitGrant, *model.Channel, *types.NewAPIError) {
	noChannel := types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgRelayRetryNoAvailableChannel, map[string]any{"Group": info.UsingGroup, "Model": info.OriginModelName})), types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	if c.Writer.Written() {
		return nil, nil, noChannel
	}
	value, _ := c.Get(channelWaitCandidatesContextKey)
	selections, _ := value.([]channelWaitSelection)
	candidates := make([]service.ChannelWaitCandidate, 0, len(selections))
	groups := make(map[int]string, len(selections))
	for _, selection := range selections {
		channel, err := model.GetChannelById(selection.channelID, true)
		if err != nil || !channelWaitEligible(c, info, channel, selection.group) {
			continue
		}
		candidates = append(candidates, service.ChannelWaitCandidate{ChannelID: channel.Id, Settings: channel.GetSetting()})
		groups[channel.Id] = selection.group
	}
	if len(candidates) == 0 {
		return nil, nil, noChannel
	}
	waiter := channelWaiterFor(c)
	if waiter == nil {
		return nil, nil, noChannel
	}

	var stopKeepAlive = func() {}
	if envelope := helper.StreamCommentEnvelopeFrom(c); envelope != nil {
		stopKeepAlive = envelope.StartKeepAlive(commentKeepAliveInterval())
		defer stopKeepAlive()
	}

	// A grant is not an authorization. Re-validate it and, when it no longer
	// fits the request, release it and let the queue offer another candidate
	// instead of ending the request here.
	for len(candidates) > 0 {
		grant, waitErr := waiter.Wait(c.Request.Context(), candidates)
		if waitErr != nil {
			if waitErr.GetErrorCode() == types.ErrorCodeChannelCapacityQueueFull {
				waitErr.SetMessage(i18n.T(c, i18n.MsgChannelCapacityQueueFull))
			} else if waitErr.GetErrorCode() == types.ErrorCodeChannelCapacityWaitTimeout {
				waitErr.SetMessage(i18n.T(c, i18n.MsgChannelCapacityWaitTimeout))
			} else if waitErr.GetErrorCode() == types.ErrorCodeChannelCapacityUnavailable {
				waitErr.SetMessage(i18n.T(c, i18n.MsgChannelCapacityUnavailable))
			}
			return nil, nil, waitErr
		}
		if grant == nil || grant.Lease == nil {
			return nil, nil, noChannel
		}
		channel, err := model.GetChannelById(grant.ChannelID, true)
		group := groups[grant.ChannelID]
		if err == nil && channelWaitEligible(c, info, channel, group) && !sameCapacityLimit(channel.GetSetting(), channel.Id, candidates) {
			// A changed cap requires a fresh atomic reservation under that cap.
			grant.Lease.Release()
			for i := range candidates {
				if candidates[i].ChannelID == channel.Id {
					candidates[i].Settings = channel.GetSetting()
				}
			}
			continue
		}
		if err != nil || !channelWaitEligible(c, info, channel, group) {
			grant.Lease.Release()
			candidates = slices.DeleteFunc(candidates, func(candidate service.ChannelWaitCandidate) bool {
				return candidate.ChannelID == grant.ChannelID
			})
			continue
		}
		if setupErr := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName); setupErr != nil {
			grant.Lease.Release()
			candidates = slices.DeleteFunc(candidates, func(candidate service.ChannelWaitCandidate) bool { return candidate.ChannelID == grant.ChannelID })
			continue
		}
		info.UsingGroup = group
		if info.TokenGroup == "auto" {
			common.SetContextKey(c, constant.ContextKeyAutoGroup, group)
		}
		info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)
		service.RequestPolicy(c).BeginAttempt(channel, group)
		return grant, channel, nil
	}
	return nil, nil, noChannel
}

// channelWaitEligible rechecks current channel scope and request restrictions
// after waiting. The reservation itself never grants permission to relay.
func channelWaitEligible(c *gin.Context, info *relaycommon.RelayInfo, channel *model.Channel, group string) bool {
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return false
	}
	constraints := service.GetChannelConstraints(c)
	if pin, pinned, _ := constraints.ResolvedPin(); pinned && pin.ChannelId != channel.Id {
		return false
	}
	state := service.RequestPolicy(c)
	if (state.SessionMode == "strict" || service.ShouldSkipRetryAfterChannelAffinityFailure(c)) && state.CurrentChannelID != channel.Id {
		return false
	}
	models := channel.EligibleModelsForUserGroup(group, c.GetInt("id"))
	if !slices.Contains(models, info.OriginModelName) && !slices.Contains(models, ratio_setting.RoutingMatchModelName(info.OriginModelName)) {
		return false
	}
	allowed, _ := model.ChannelSatisfiesFilters(channel, info.OriginModelName, constraints.Filters)
	return allowed
}

// channelWaiterContextKey stores the request-scoped capacity waiter.
const channelWaiterContextKey = "channel_waiter"

// channelWaiterFrom returns the waiter already created for this request.
func channelWaiterFrom(c *gin.Context) *service.ChannelWaiter {
	value, ok := c.Get(channelWaiterContextKey)
	if !ok {
		return nil
	}
	waiter, _ := value.(*service.ChannelWaiter)
	return waiter
}

// channelWaiterFor returns the request's waiter, creating it on first use so an
// ordinary stream that never waits does not register for a queue slot.
func channelWaiterFor(c *gin.Context) *service.ChannelWaiter {
	if waiter := channelWaiterFrom(c); waiter != nil {
		return waiter
	}
	userID := c.GetInt("id")
	if userID <= 0 {
		return nil
	}
	waiter := service.NewChannelWaiter(c.Request.Context(), userID)
	c.Set(channelWaiterContextKey, waiter)
	return waiter
}

// commentKeepAliveInterval is the interval for the waiting-period SSE comment
// keepalive.
func commentKeepAliveInterval() time.Duration {
	generalSettings := operation_setting.GetGeneralSetting()
	if !generalSettings.PingIntervalEnabled || generalSettings.PingIntervalSeconds <= 0 {
		return helper.DefaultPingInterval
	}
	return time.Duration(generalSettings.PingIntervalSeconds) * time.Second
}

// sameCapacityLimit reports whether the granted channel still has the concurrency
// limit that the matching candidate was queued for.
func sameCapacityLimit(current dto.ChannelSettings, channelID int, candidates []service.ChannelWaitCandidate) bool {
	for _, candidate := range candidates {
		if candidate.ChannelID != channelID {
			continue
		}
		return sameMaxConcurrentRequests(current.MaxConcurrentRequests, candidate.Settings.MaxConcurrentRequests)
	}
	return false
}

func sameMaxConcurrentRequests(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func RelayMidjourney(c *gin.Context) {
	policy := service.RequestPolicy(c)
	defer func() {
		if policy.Attempts > 0 && !policy.Successful {
			service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("Midjourney submission failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway, types.ErrOptionWithSkipRetry()))
		}
	}()
	var relayInfo *relaycommon.RelayInfo
	var reportErr *types.NewAPIError
	// Registered first so that it runs last: the request plugins' report
	// hooks receive a submission once the client has the response.
	defer func() {
		requestplugin.Report(c, types.RelayFormatMjProxy, relayInfo, nil, reportErr)
	}()
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *taskdto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	default:
		if reportErr = midjourneyRequestPlugins(c, relayInfo); reportErr != nil {
			code := constant.MjRequestError
			if reportErr.StatusCode >= http.StatusInternalServerError {
				code = constant.MjErrorUnknown
			}
			c.JSON(reportErr.StatusCode, gin.H{
				"description": common.MessageWithRequestId(reportErr.Error(), c.GetString(common.RequestIdKey)),
				"type":        string(reportErr.GetErrorCode()),
				"code":        code,
			})
			return
		}
		if relayInfo.RelayMode == relayconstant.RelayModeSwapFace {
			mjErr = relay.RelaySwapFace(c, relayInfo)
		} else {
			mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
		}
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		policy.Successful = false
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = i18n.T(c, i18n.MsgRelayGroupSaturated)
			statusCode = http.StatusTooManyRequests
		}
		reportErr = types.NewErrorWithStatusCode(fmt.Errorf("%s %s", mjErr.Description, mjErr.Result), types.ErrorCodeBadResponseStatusCode, statusCode)
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, common.LogText("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

// midjourneyRequestPlugins runs the request plugins of a Midjourney
// submission on its client body before the body is read: the request's
// ingress, then the late ingress of its channel. A Midjourney submission
// has one attempt, so the late record stays with the request's. A stop is
// written to the error log.
func midjourneyRequestPlugins(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	_, steps, record, apiErr := requestplugin.SubmitIngress(c, types.RelayFormatMjProxy, common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
	relayInfo.SetRequestPluginIngress(steps, record)
	channelID := 0
	if apiErr == nil {
		relayInfo.InitChannelMeta(c)
		channelID = relayInfo.ChannelId
		_, apiErr = requestplugin.TaskAttempt(c, relayInfo)
		relayInfo.SetRequestPluginIngress(relayInfo.RequestPluginSteps, relayInfo.RequestPluginRecord)
	}
	if apiErr != nil {
		service.RecordRelayErrorLog(c, channelID, apiErr, relayInfo)
	}
	return apiErr
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	// The web fallback may already have applied static-asset cache headers.
	// A missing API or asset can appear after an upgrade; never cache its 404.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

// RelayTaskPluginEndpoint keeps unclaimed shared-endpoint traffic on its
// existing handler while claimed requests enter the generation-pinned
// host-owned protocol bridge.
func RelayTaskPluginEndpoint(c *gin.Context, fallback gin.HandlerFunc) {
	pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	if !exists {
		fallback(c)
		return
	}
	pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Generation == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Task protocol request failed",
				"type":    "new_api_error",
				"code":    "task_protocol_error",
			},
		})
		return
	}
	switch pinned.Protocol {
	case "openai_responses":
		serveTaskPluginProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	case pluginruntime.ProtocolOpenAIImage:
		serveTaskPluginImageProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	default:
		fallback(c)
	}
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

type taskSubmissionOutcome struct {
	Result    *relay.TaskSubmitResult
	Task      *model.Task
	RelayInfo *relaycommon.RelayInfo
}

func RelayTask(c *gin.Context) {
	var relayInfo *relaycommon.RelayInfo
	var taskErr *taskdto.TaskError
	// Registered first so that it runs last: the request plugins' report
	// hooks receive the submission once the client has the response.
	defer func() {
		requestplugin.Report(c, types.RelayFormatTask, relayInfo, nil, taskReportError(taskErr))
	}()
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondTaskSubmissionError(c, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if action := c.GetString("task_action"); action != "" {
		relayInfo.Action = action
	}

	if taskErr = relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	if taskErr = relay.ApplyOriginTaskAffinity(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}

	var outcome *taskSubmissionOutcome
	outcome, taskErr = executeTaskSubmission(c, relayInfo)
	if taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	presentTaskSubmission(c, outcome)
}

// taskReportError is the error a task submission's client received, as the
// request plugins' report record shows it; nil when it succeeded.
func taskReportError(taskErr *taskdto.TaskError) *types.NewAPIError {
	if taskErr == nil {
		return nil
	}
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	return types.NewErrorWithStatusCode(err, types.ErrorCode(taskErr.Code), taskErr.StatusCode)
}

// executeTaskSubmission owns the retry, billing, and persistence lifecycle.
// It deliberately performs no client response writes so JSON and protocol
// presenters share the same durable task barrier. Its cancellation semantics
// come from c.Request.Context: native task endpoints use the client context,
// while the Responses bridge supplies an independently bounded context.
func executeTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*taskSubmissionOutcome, *taskdto.TaskError) {
	return executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
}

type taskSubmitAttempt func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError)

func executeTaskSubmissionWith(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	submit taskSubmitAttempt,
) (*taskSubmissionOutcome, *taskdto.TaskError) {
	policy := service.RequestPolicy(c)
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	diagnostics.start(relayInfo)
	var result *relay.TaskSubmitResult
	var taskErr *taskdto.TaskError
	durable := false
	stage := "start"
	defer func() {
		// The client of a relayed event stream already holds the vendor's
		// work, so the submission is billed instead of refunded.
		if !durable && relayInfo.Billing != nil && !c.Writer.Written() {
			diagnostics.refund(stage)
			relayInfo.Billing.Refund(c)
		}
	}()
	stage = "before_attempt"
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_attempt", 0)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	// The request plugins' ingress runs once, on the client body, before the
	// submission is validated, priced or reserved; a changed body is decoded
	// again.
	stage = "request_plugins"
	var format types.RelayFormat = types.RelayFormatTask
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && (pinned.Protocol == string(types.RelayFormatOpenAIResponses) || pinned.Protocol == pluginruntime.ProtocolOpenAIImage) {
			format = types.RelayFormat(pinned.Protocol)
		}
	}
	ingressModel := relayInfo.OriginModelName
	if ingressModel == "" {
		ingressModel = common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	}
	changed, ingressSteps, ingressRecord, ingressErr := requestplugin.SubmitIngress(c, format, ingressModel)
	relayInfo.SetRequestPluginIngress(ingressSteps, ingressRecord)
	if ingressErr != nil {
		// The submission reached no channel and reserved nothing; its error
		// log shows what the plugins did.
		service.RecordRelayErrorLog(c, 0, ingressErr, relayInfo)
		taskErr = requestplugin.TaskError(ingressErr)
		diagnostics.failed(stage, "request_plugin", taskErr, false)
		return nil, taskErr
	}
	if changed {
		if taskErr = middleware.RefreshTaskPluginRequest(c); taskErr != nil {
			diagnostics.failed(stage, "request_decode", taskErr, false)
			return nil, taskErr
		}
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		stage = "select_channel"
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("before_attempt", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		var channel *model.Channel
		var lease *service.ChannelCapacityLease

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			policy.BeginAttempt(channel, relayInfo.UsingGroup)
			if retryParam.GetRetry() > 0 {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
			var capacityErr *types.NewAPIError
			lease, capacityErr = service.AcquireRequestChannelCapacity(c, channel.Id)
			if capacityErr != nil {
				taskErr = service.TaskErrorWrapperLocal(capacityErr, string(capacityErr.GetErrorCode()), capacityErr.StatusCode)
				break
			}
		} else {
			var channelErr *types.NewAPIError
			channel, lease, channelErr = getChannelWithCapacity(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				if taskErr == nil {
					taskErr = service.TaskErrorWrapperLocal(channelErr, string(channelErr.GetErrorCode()), channelErr.StatusCode)
				}
				break
			}
		}
		defer lease.Release() // submission closes it early; guard setup panics too
		diagnostics.attempt(retryParam.GetRetry()+1, channel, relayInfo.LockedChannel != nil)

		service.AppendUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			lease.Release()
			stage = "read_body"
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		stage = "submit"
		result, taskErr = func() (submitted *relay.TaskSubmitResult, submitErr *taskdto.TaskError) {
			originalRequest := c.Request
			attemptRequest := originalRequest.WithContext(lease.Context())
			c.Request = attemptRequest
			defer func() {
				if coordinationErr := lease.Err(); coordinationErr != nil && submitted == nil {
					submitErr = service.TaskErrorWrapperLocal(coordinationErr, string(coordinationErr.GetErrorCode()), coordinationErr.StatusCode)
				}
				lease.Release()
				// Native submission streams deliberately replace the request
				// with a detached context so persistence/settlement can finish
				// after the client leaves. Preserve that policy after admission.
				if c.Request == attemptRequest {
					c.Request = originalRequest
				}
			}()
			return submit(c, relayInfo)
		}()
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("after_submit", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		if taskErr == nil {
			diagnostics.attemptSucceeded(retryParam.GetRetry()+1, result)
			break
		}

		taskAPIError := taskSubmissionAPIError(taskErr)
		relayInfo.LastError = taskAPIError
		decision := decideTaskRetry(c, taskErr, common.RetryTimes-retryParam.GetRetry())
		service.RecordPolicyFailure(c, channel.Id, taskAPIError, decision)
		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				taskAPIError,
				relayInfo)
		} else if requestplugin.Stopped(taskErr) {
			// A request plugin stopped the attempt; its error log shows what
			// the plugins did.
			service.RecordRelayErrorLog(c, channel.Id, taskReportError(taskErr), relayInfo)
		}

		willRetry := decision.Action == "retry"
		diagnostics.attemptFailed(retryParam.GetRetry()+1, channel, taskErr, willRetry)
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	if taskErr != nil {
		diagnostics.failed(stage, "task_error", taskErr, false)
		return nil, taskErr
	}
	if result == nil {
		taskErr = service.TaskErrorWrapperLocal(errors.New("task submission returned no result"), "task_submit_failed", http.StatusInternalServerError)
		diagnostics.failed("submit", "missing_result", taskErr, false)
		return nil, taskErr
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_reserve", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	// Reserve any submit-time upward billing adjustment before persistence.
	// This keeps insertion failures fully refundable while ensuring settlement
	// after the barrier normally has a zero positive delta. A relayed event
	// stream is billed even when this reserve fails: settlement then charges
	// past the available quota, as it does for any usage above its reservation.
	if relayInfo.Billing != nil {
		stage = "reserve"
		diagnostics.reserve("reserve_start", result.Quota)
		if reserveErr := relayInfo.Billing.Reserve(result.Quota); reserveErr != nil {
			common.SysError(common.LogText("reserve adjusted task billing error: %s", reserveErr.Error()))
			if !c.Writer.Written() {
				taskErr = service.TaskErrorWrapperLocal(errors.New("insufficient quota for adjusted task cost"), string(types.ErrorCodeInsufficientUserQuota), http.StatusForbidden)
				diagnostics.failed("reserve", "insufficient_quota", taskErr, false)
				return nil, taskErr
			}
		} else {
			diagnostics.reserve("reserve_complete", result.Quota)
		}
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_insert", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	stage = "insert"
	task := model.InitTask(result.Platform, relayInfo)
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	task.PrivateData.BillingSource = relayInfo.BillingSource
	task.PrivateData.SubscriptionId = relayInfo.SubscriptionId
	task.PrivateData.TokenId = relayInfo.TokenId
	task.PrivateData.NodeName = common.NodeName
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		ModelPrice:      relayInfo.PriceData.ModelPrice,
		GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelRatio:      relayInfo.PriceData.ModelRatio,
		OtherRatios:     relayInfo.PriceData.OtherRatios(),
		OriginModelName: relayInfo.OriginModelName,
		PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	task.Quota = result.Quota
	task.Data = result.TaskData
	if len(result.PluginState) > 0 {
		task.PrivateData.PluginState = result.PluginState
	}
	// A request plugin instance that may report the task gets it once, at
	// its final state.
	task.PrivateData.RequestPlugins = requestplugin.TaskReport(c, relayInfo)
	task.Action = relayInfo.Action
	if immediate := result.Immediate; immediate != nil {
		task.Status = model.TaskStatus(immediate.Status)
		task.Progress = immediate.Progress
		if immediate.Status == model.TaskStatusSuccess || immediate.Status == model.TaskStatusFailure {
			task.FinishTime = time.Now().Unix()
		}
		if immediate.Status == model.TaskStatusFailure {
			task.FailReason = immediate.Reason
		}
		if immediate.Url != "" {
			task.PrivateData.ResultURL = immediate.Url
		} else if immediate.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	}
	// A native submit route may declare retainResult: false. It applies only
	// to immediate terminal results: the client receives the complete response
	// once, the upstream snapshot is never persisted, and the task is not
	// retrievable afterwards. An asynchronous result on such a route keeps its
	// snapshot because polling and retrieval need it. The OpenAI Images
	// protocol delivers its images inline in the same HTTP response, so its
	// immediate results follow the same rule; an asynchronous image task is
	// expected there and is polled inside the request.
	var insertOmits []string
	immediateTerminal := result.Immediate != nil && (result.Immediate.Status == model.TaskStatusSuccess || result.Immediate.Status == model.TaskStatusFailure)
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if ok && pinned.Route.RetainResult != nil && !*pinned.Route.RetainResult {
			if immediateTerminal {
				task.PrivateData.ResultDiscarded = true
				insertOmits = append(insertOmits, "data")
			} else {
				logger.LogWarn(c, common.LogText("task plugin route %s %s declares retainResult: false but returned an asynchronous result; retaining task %s", pinned.Route.Method, pinned.Route.Path, task.TaskID))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists && immediateTerminal {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == pluginruntime.ProtocolOpenAIImage {
			task.PrivateData.ResultDiscarded = true
			insertOmits = append(insertOmits, "data")
		}
	}
	diagnostics.insertStart(task)
	if insertErr := task.InsertWithContext(c.Request.Context(), insertOmits...); insertErr != nil {
		common.SysError(common.LogText("insert task error: %s", insertErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
		diagnostics.failed("insert", "database_error", taskErr, false)
		// The client of a relayed event stream already holds the vendor's
		// work, so it is settled and logged without a task row.
		if c.Writer.Written() {
			if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
				common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
				return nil, taskErr
			}
			service.LogTaskConsumption(c, relayInfo, task)
		}
		return nil, taskErr
	}
	durable = true
	requestplugin.TaskCreated(c)
	if immediateTerminal {
		// Reported once settled, whatever the settlement did.
		defer requestplugin.ReportTask(task)
	}
	stage = "settle"
	diagnostics.durable(task)
	diagnostics.settleStart(task, result.Quota)

	if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
		common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to settle task billing"), "task_billing_settlement_failed", http.StatusInternalServerError)
		diagnostics.failed("settle", "billing_error", taskErr, true)
		return nil, taskErr
	}
	perfmetrics.RecordImmediateTaskResult(relayInfo, result.Immediate)
	if task.Status != model.TaskStatusFailure {
		service.MarkRequestPolicySuccess(c, nil)
	} else {
		policy.AddEvent(service.PolicyEvent{Decision: service.PolicyDecision{Action: "stop", Reason: "task_failed", Source: "upstream"}})
	}
	service.LogTaskConsumption(c, relayInfo, task)
	diagnostics.complete(task, result.Quota)

	return &taskSubmissionOutcome{Result: result, Task: task, RelayInfo: relayInfo}, nil
}

func presentTaskSubmission(c *gin.Context, outcome *taskSubmissionOutcome) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	// A native route that relayed the vendor's event stream has answered.
	if c.Writer.Written() {
		diagnostics.present(outcome.Task, "native_stream")
		return
	}
	otherRatios := outcome.RelayInfo.PriceData.OtherRatios()
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	if ratiosJSON, err := common.Marshal(otherRatios); err == nil {
		c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedRoute); ok && pinned.Plugin != nil && pinned.Route.Render != "" {
			view, err := service.BuildTaskPluginView(outcome.Task)
			requestValue, _ := c.Get(pluginruntime.ContextKeyRouteRequest)
			requestContext, _ := requestValue.(pluginruntime.RouteRequestContext)
			if err == nil {
				viewValue, valueErr := taskPluginProtocolJSONValue(view)
				if valueErr == nil {
					if body, callErr := pinned.Plugin.Engine.CallPath(c.Request.Context(), "native", []string{pinned.Route.Render}, requestContext.JSValue(), viewValue); callErr == nil {
						if replyErr := middleware.WriteNativeReply(c, http.StatusOK, body); replyErr == nil {
							diagnostics.present(outcome.Task, "native_presenter")
							return
						} else {
							logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", replyErr.Error()))
						}
					} else {
						logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", callErr.Error()))
					}
				} else {
					logger.LogError(c, common.LogText("encode task plugin native submit view failed: %s", valueErr.Error()))
				}
			} else {
				logger.LogError(c, common.LogText("build task plugin native submit view failed: %s", err.Error()))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == "openai_video" && pinned.Operation.Name == "create" {
			diagnostics.present(outcome.Task, "openai_video_create")
			c.JSON(http.StatusOK, outcome.Task.ToOpenAIVideo())
			return
		}
	}
	createdAt := outcome.Task.CreatedAt
	if createdAt == 0 {
		createdAt = outcome.Task.SubmitTime
	}
	diagnostics.present(outcome.Task, "host_fallback")
	c.JSON(http.StatusOK, map[string]any{
		"id":         outcome.Task.TaskID,
		"task_id":    outcome.Task.TaskID,
		"status":     "queued",
		"model":      outcome.RelayInfo.OriginModelName,
		"created_at": createdAt,
	})
}

func respondTaskSubmissionError(c *gin.Context, taskErr *taskdto.TaskError) {
	service.RecordRequestPolicyTermination(c, taskSubmissionAPIError(taskErr))
	newTaskPluginSubmitDiagnostics(c).presentError(taskErr)
	// The client of a relayed event stream already has the vendor's events,
	// including any error event; nothing can follow them.
	if c.Writer.Written() {
		return
	}
	if middleware.RespondTaskPluginError(c, taskErr) {
		return
	}
	respondTaskError(c, taskErr)
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *taskdto.TaskError) {
	if service.IsUpstreamFirstByteTimeout(taskErr.Error) {
		taskErr.Message = i18n.T(c, i18n.MsgRelayUpstreamFirstByteTimeout)
	} else if taskErr.StatusCode == http.StatusTooManyRequests && taskErr.Code != string(types.ErrorCodeChannelCapacitySaturated) {
		taskErr.Message = i18n.T(c, i18n.MsgRelayGroupUpstreamSaturated)
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

// taskSubmissionAPIError adapts a task error for the shared relay error paths.
// TaskError.Error is nil for many local rejections, so fall back to the message.
func taskSubmissionAPIError(taskErr *taskdto.TaskError) *types.NewAPIError {
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
}

// decideTaskRetry is the single retry decision for task submissions. The
// reason is recorded in the request policy decision events of the log details.
func decideTaskRetry(c *gin.Context, taskErr *taskdto.TaskError, retryTimes int) service.PolicyDecision {
	if taskErr != nil && !taskErr.LocalError && !taskErr.NoRetry {
		service.RecordChannelFailure(c)
	}
	stop := service.PolicyDecision{Action: "stop", Source: "system"}
	retry := service.PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "system"}
	switch {
	case taskErr == nil:
		stop.Reason = "request_completed"
	case taskErr.NoRetry:
		stop.Reason = "task_accepted"
	case requestplugin.Stopped(taskErr):
		stop.Reason, stop.Source = "local_rejection", "plugin"
	case c != nil && c.Request != nil && c.Request.Context().Err() != nil:
		stop.Reason = "client_cancelled"
	case c != nil && c.Writer != nil && c.Writer.Written():
		stop.Reason = "response_started"
	case taskErr.LocalError:
		stop.Reason = "local_rejection"
	case service.ShouldSkipRetryAfterChannelAffinityFailure(c):
		stop.Reason, stop.Source = "strict_session", "session_rule"
		if source := service.RequestPolicy(c).SessionModeSource; source != "" {
			stop.Source = source
		}
	case retryTimes <= 0:
		stop.Reason, stop.Source = "attempt_budget_exhausted", "global"
	case service.GetChannelConstraints(c).SuppressesRetry():
		stop.Reason, stop.Source = "pinned_channel", "channel_constraint"
	case service.IsUpstreamFirstByteTimeout(taskErr.Error):
		return service.PolicyDecision{Action: "retry", Reason: "upstream_first_byte_timeout", Source: "system"}
	case taskErr.StatusCode/100 == 2:
		stop.Reason = "system_retry_exclusion"
	default:
		if operation_setting.ShouldRetryByStatusCode(taskErr.StatusCode) {
			return retry
		}
		stop.Reason = "status_not_retryable"
	}
	return stop
}

// streamCommitHoldBudget bounds how long a streaming Responses attempt may keep
// its first bytes retryable. A configured upstream first-byte timeout is the
// natural bound; otherwise the helper's default applies.
func streamCommitHoldBudget(settings dto.ChannelSettings) time.Duration {
	if seconds := service.EffectiveUpstreamFirstByteTimeoutSeconds(settings); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return helper.DefaultStreamCommitHoldBudget
}
