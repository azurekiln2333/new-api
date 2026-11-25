package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/MoeForge/moego/ojson"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

type testResult struct {
	context      *gin.Context
	channel      *model.Channel
	localErr     error
	newAPIError  *types.NewAPIError
	elapsed      time.Duration
	canRetryBusy bool
	// keyIndex is the key of a multi-key channel that served this test, or -1
	// when it is unknown. It decides which key a failed test may disable.
	keyIndex int
	// A caller that reads the reply, such as a model fingerprint probe, receives
	// the response body and the usage the relay settled.
	responseBody     []byte
	promptTokens     int
	completionTokens int
	consumedUsage    bool
}

// channelTestKeyError marks a key named for a channel test that the channel or
// the tested model cannot accept. Such a test only reports the reason: the
// channel and its keys stay untouched and the automatic disable path is not
// entered.
type channelTestKeyError struct {
	message string
	code    types.ErrorCode
}

func (err *channelTestKeyError) Error() string {
	return err.message
}

func normalizeChannelTestEndpoint(channel *model.Channel, endpointType string) string {
	normalized := strings.TrimSpace(endpointType)
	if normalized != "" {
		return normalized
	}
	if channel != nil && channel.Type == constant.ChannelTypeCodex {
		return string(constant.EndpointTypeOpenAIResponse)
	}
	return normalized
}

// providerTestEndpoints are the endpoints a task plugin channel test tries when
// its plugin's relay routes do not serve the default one, in this order.
var providerTestEndpoints = []constant.EndpointType{
	constant.EndpointTypeOpenAI,
	constant.EndpointTypeOpenAIResponse,
	constant.EndpointTypeAnthropic,
	constant.EndpointTypeGemini,
	constant.EndpointTypeEmbeddings,
	constant.EndpointTypeJinaRerank,
	constant.EndpointTypeImageGeneration,
}

// providerTestEndpoint returns the endpoint a task plugin channel test uses
// for model instead of requestPath, which meta's relay routes do not serve.
// ok is false when they serve requestPath or none of the test endpoints.
func providerTestEndpoint(meta jsplugin.Meta, requestPath, model string) (endpoint constant.EndpointType, ok bool) {
	if _, served := meta.RelayRoute(requestPath, model); served {
		return "", false
	}
	for _, endpoint := range providerTestEndpoints {
		info, _ := common.GetDefaultEndpointInfo(endpoint)
		if _, served := meta.RelayRoute(strings.ReplaceAll(info.Path, "{model}", model), model); served {
			return endpoint, true
		}
	}
	return "", false
}

func resolveChannelTestUserID(c *gin.Context) (int, error) {
	if c != nil {
		if userID := c.GetInt("id"); userID > 0 {
			return userID, nil
		}
	}

	var rootUser model.User
	if err := model.DB.Select("id").Where("role = ?", common.RoleRootUser).First(&rootUser).Error; err != nil {
		return 0, fmt.Errorf("failed to resolve channel test user: %w", err)
	}
	if rootUser.Id == 0 {
		return 0, errors.New("failed to resolve channel test user")
	}
	return rootUser.Id, nil
}

// checkChannelTestKey validates a key named for a channel test before any relay
// request is built: a named key must exist and be allowed to serve the tested
// model. A key disabled by automatic health checks stays eligible, so an
// operator can confirm whether it works again.
func checkChannelTestKey(channel *model.Channel, keyIndex int, testModel string) error {
	if keyIndex < 0 {
		return nil
	}
	if !channel.ChannelInfo.IsMultiKey {
		return &channelTestKeyError{
			message: "this channel is not in multi-key mode, so it has no key to select",
			code:    types.ErrorCodeInvalidRequest,
		}
	}
	if keyIndex >= len(channel.GetKeys()) {
		return &channelTestKeyError{
			message: fmt.Sprintf("key #%d does not exist: the channel has %d keys", keyIndex+1, len(channel.GetKeys())),
			code:    types.ErrorCodeInvalidRequest,
		}
	}
	if !channel.KeyServesModel(keyIndex, testModel) {
		return &channelTestKeyError{
			message: fmt.Sprintf("key #%d is not allowed to serve model %q", keyIndex+1, testModel),
			code:    types.ErrorCodeChannelKeyScopeUnsatisfied,
		}
	}
	return nil
}

// pinChannelTestKey resolves an explicit selection from the current channel snapshot.
// When the caller supplies an allocation identity, the optional index is an
// additional snapshot fence: it must still point at that allocation.
func pinChannelTestKey(channel *model.Channel, keyIndex int, entryID, expectedRevision int64, testModel string) (*pinnedChannelTestKey, error) {
	if entryID == 0 && expectedRevision == 0 {
		if keyIndex < 0 {
			return nil, nil
		}
		if err := checkChannelTestKey(channel, keyIndex, testModel); err != nil {
			return nil, err
		}
		pinned := &pinnedChannelTestKey{index: keyIndex, value: channel.GetKeys()[keyIndex]}
		if allocation, ok := channel.ChannelAllocationByPosition(keyIndex); ok && allocation.Key == pinned.value {
			pinned.entryID = allocation.EntryID
			pinned.revision = allocation.Revision
		}
		return pinned, nil
	}

	if entryID <= 0 || expectedRevision <= 0 || channel == nil || !channel.ChannelInfo.IsMultiKey {
		return nil, &channelTestKeyError{
			message: "Channel key configuration changed, reload and retry",
			code:    types.ErrorCodeInvalidRequest,
		}
	}

	position := -1
	for index, id := range channel.KeyEntryIDs {
		if id == entryID {
			position = index
			break
		}
	}
	if position < 0 || position >= len(channel.KeyEntryRevisions) ||
		channel.KeyEntryRevisions[position] != expectedRevision ||
		(keyIndex >= 0 && keyIndex != position) || position >= len(channel.GetKeys()) {
		return nil, &channelTestKeyError{
			message: "Channel key configuration changed, reload and retry",
			code:    types.ErrorCodeInvalidRequest,
		}
	}
	if err := checkChannelTestKey(channel, position, testModel); err != nil {
		return nil, err
	}
	return &pinnedChannelTestKey{
		index:    position,
		value:    channel.GetKeys()[position],
		entryID:  entryID,
		revision: expectedRevision,
	}, nil
}

// servedTestKeyIndex reports which key of the channel serves the test request,
// so a failure is attributed to that key rather than to the channel. It returns
// -1 when the key is unknown, including for a single-key channel.
func servedTestKeyIndex(c *gin.Context, channel *model.Channel) int {
	if !channel.ChannelInfo.IsMultiKey {
		return -1
	}
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	index := common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	keys := channel.GetKeys()
	if index >= 0 && index < len(keys) && keys[index] == key {
		return index
	}
	return -1
}

type channelTestSpec struct {
	userID           int
	model            string
	endpoint         string
	stream           bool
	keyIndex         int
	keyEntryID       int64
	keyEntryRevision int64
	healthCheck      bool
}

type pinnedChannelTestKey struct {
	index    int // -1 for a single-key channel
	value    string
	entryID  int64
	revision int64
}

// testChannel runs one manual test or fingerprint sample. A non-negative
// keyIndex requests exactly that key, even if currently disabled.
func testChannel(ctx context.Context, channel *model.Channel, testUserID int, testModel string, endpointType string, isStream bool, keyIndex int) testResult {
	return runChannelTest(ctx, channel, channelTestSpec{
		userID: testUserID, model: testModel, endpoint: endpointType, stream: isStream, keyIndex: keyIndex,
	})
}

func runChannelTest(ctx context.Context, channel *model.Channel, spec channelTestSpec) testResult {
	if ctx == nil {
		ctx = context.Background()
	}
	spec.model = strings.TrimSpace(spec.model)
	spec.endpoint = strings.TrimSpace(spec.endpoint)
	pinned, err := pinChannelTestKey(channel, spec.keyIndex, spec.keyEntryID, spec.keyEntryRevision, channelTestModel(channel, spec.model))
	if err != nil {
		return testResult{localErr: err, keyIndex: spec.keyIndex}
	}
	if pinned != nil {
		spec.keyIndex = pinned.index
	}
	requireConfiguredModel := spec.model != "" && channelTestModelConfigured(channel, spec.model)
	admissionUserID := spec.userID
	if spec.healthCheck || channelTestPromptFromContext(ctx) != nil {
		admissionUserID = 0 // background jobs use the internal queue, not a borrowed billing identity
	}
	admission := service.NewChannelDiagnosticAdmission(ctx, admissionUserID)
	defer admission.Close()

	lease, current, apiErr := admitChannelTest(ctx, admission, channel, false)
	if apiErr != nil {
		return testResult{localErr: apiErr, newAPIError: apiErr}
	}
	result := runAdmittedChannelTest(ctx, lease, current, spec, pinned, requireConfiguredModel)
	if !result.canRetryBusy {
		return result
	}

	// One safe replay of a refused request. Do not choose a different key or
	// model on retry, even if automatic selection was used on the first attempt.
	spec.model = common.GetContextKeyString(result.context, constant.ContextKeyOriginalModel)
	if pinned == nil || pinned.entryID == 0 {
		keyValue := common.GetContextKeyString(result.context, constant.ContextKeyChannelKey)
		pinned = &pinnedChannelTestKey{index: result.keyIndex, value: keyValue}
		if result.channel != nil {
			if allocation, ok := result.channel.ChannelAllocationByPosition(result.keyIndex); ok && allocation.Key == keyValue {
				pinned.entryID = allocation.EntryID
				pinned.revision = allocation.Revision
			}
		}
	}
	lease, latest, apiErr := admitChannelTest(ctx, admission, current, true)
	if apiErr != nil {
		return testResult{localErr: apiErr, newAPIError: apiErr}
	}
	return runAdmittedChannelTest(ctx, lease, latest, spec, pinned, channelTestModelConfigured(current, spec.model))
}

func channelTestModel(channel *model.Channel, requested string) string {
	if requested != "" {
		return requested
	}
	if channel.TestModel != nil && strings.TrimSpace(*channel.TestModel) != "" {
		return strings.TrimSpace(*channel.TestModel)
	}
	models := channel.GetModels()
	if len(models) > 0 && strings.TrimSpace(models[0]) != "" {
		return strings.TrimSpace(models[0])
	}
	return "gpt-4o-mini"
}

func channelTestModelConfigured(channel *model.Channel, name string) bool {
	return slices.ContainsFunc(channel.GetModels(), func(model string) bool { return strings.TrimSpace(model) == name })
}

// admitChannelTest reads persisted policy after the grant, not from the
// routing cache. The lease must reflect the current cap before it can be used.
func admitChannelTest(ctx context.Context, admission *service.ChannelDiagnosticAdmission, channel *model.Channel, afterBusy bool) (*service.ChannelCapacityLease, *model.Channel, *types.NewAPIError) {
	for range 3 {
		settings := channel.GetSetting()
		acquire := admission.Acquire
		if afterBusy {
			acquire = admission.Wait
		}
		lease, apiErr := acquire(ctx, channel.Id, settings)
		if apiErr != nil {
			return nil, nil, apiErr
		}
		if channel.Id <= 0 {
			return lease, channel, nil
		}
		var latest model.Channel
		if err := model.DB.WithContext(lease.Context()).First(&latest, channel.Id).Error; err != nil {
			apiErr := lease.Err()
			lease.Release()
			if apiErr == nil {
				apiErr = types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
			}
			return nil, nil, apiErr
		}
		if max(0, lo.FromPtr(settings.MaxConcurrentRequests)) == max(0, lo.FromPtr(latest.GetSetting().MaxConcurrentRequests)) {
			return lease, &latest, nil
		}
		lease.Release()
		channel = &latest
	}
	return nil, nil, types.NewErrorWithStatusCode(errors.New(i18n.Translate("", i18n.MsgChannelCapacityUnavailable, nil)), types.ErrorCodeChannelCapacityUnavailable, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}

func runAdmittedChannelTest(ctx context.Context, lease *service.ChannelCapacityLease, channel *model.Channel, spec channelTestSpec, pinned *pinnedChannelTestKey, requireConfiguredModel bool) testResult {
	defer lease.Release()
	if apiErr := lease.Err(); apiErr != nil {
		return testResult{localErr: apiErr, newAPIError: apiErr}
	}
	if err := lease.Context().Err(); err != nil {
		return testResult{localErr: err}
	}
	if spec.healthCheck && channel.Status == common.ChannelStatusManuallyDisabled {
		return testResult{}
	}
	testModel := channelTestModel(channel, spec.model)
	if requireConfiguredModel && !channelTestModelConfigured(channel, testModel) {
		return testResult{localErr: errors.New("the selected model is not available on this channel")}
	}
	if channelTestPromptFromContext(ctx) != nil {
		if err := validateFingerprintChannel(channel, testModel); err != nil {
			return testResult{localErr: err}
		}
	}
	if err := checkChannelTestKey(channel, spec.keyIndex, testModel); err != nil {
		return testResult{localErr: err, keyIndex: spec.keyIndex}
	}
	if pinned != nil && pinned.entryID > 0 && !pinned.matchesAllocation(channel) {
		return testResult{
			localErr: &channelTestKeyError{
				message: "Channel key configuration changed, reload and retry",
				code:    types.ErrorCodeInvalidRequest,
			},
			keyIndex: pinned.index,
		}
	}
	if pinned != nil && !pinned.stillServes(channel, testModel, spec.keyIndex >= 0) {
		err := &channelTestKeyError{message: "no enabled key serves this request", code: types.ErrorCodeChannelKeyScopeUnsatisfied}
		return testResult{localErr: err, keyIndex: pinned.index}
	}
	if spec.healthCheck {
		spec.stream = shouldUseStreamForAutomaticChannelTest(channel)
	}

	started := time.Now()
	result := testChannelAttempt(lease.Context(), channel, spec, testModel, pinned, started)
	result.channel = channel
	result.elapsed = time.Since(started)
	if result.context != nil {
		// Health processing runs after the lease is released, on the caller's
		// context rather than an attempt context canceled by Release.
		result.context.Request = result.context.Request.WithContext(ctx)
	}
	if result.localErr == nil && result.newAPIError == nil {
		if lease.Context().Err() == nil && len(result.responseBody) != 0 {
			lease.MarkUpstreamAvailable()
		}
		return result
	}
	if apiErr := lease.Err(); apiErr != nil {
		result.localErr, result.newAPIError = apiErr, apiErr
		return result
	}
	if ctx.Err() == nil && channel.Id > 0 && relay.DiagnosticUpstreamBusy(result.context) &&
		!result.context.Writer.Written() && !result.consumedUsage {
		retryAfter := service.ParseChannelRetryAfter(helper.UpstreamRetryAfter(result.context), time.Now())
		// Record the busy gate before Release wakes another waiter.
		if apiErr := service.ReportChannelUpstreamBusy(lease.Context(), channel.Id, retryAfter); apiErr != nil {
			result.localErr, result.newAPIError = apiErr, apiErr
			return result
		}
		result.canRetryBusy = true
	}
	return result
}

func (key *pinnedChannelTestKey) matchesAllocation(channel *model.Channel) bool {
	return key != nil && channel != nil && key.entryID > 0 && key.revision > 0 &&
		key.index >= 0 && key.index < len(channel.KeyEntryIDs) &&
		key.index < len(channel.KeyEntryRevisions) && key.index < len(channel.GetKeys()) &&
		channel.ChannelKeyEntryID(key.index) == key.entryID &&
		channel.KeyEntryRevisions[key.index] == key.revision &&
		channel.GetKeys()[key.index] == key.value
}

func (key *pinnedChannelTestKey) stillServes(channel *model.Channel, testModel string, explicit bool) bool {
	if key.entryID > 0 && !key.matchesAllocation(channel) {
		return false
	}
	if key.index < 0 {
		if channel.ChannelInfo.IsMultiKey || key.value != channel.Key {
			return false
		}
	} else {
		if checkChannelTestKey(channel, key.index, testModel) != nil || key.value != channel.GetKeys()[key.index] {
			return false
		}
		status, recorded := channel.ChannelInfo.MultiKeyStatusList[key.index]
		if !explicit && recorded && status != common.ChannelStatusEnabled {
			return false
		}
	}
	if explicit {
		return true // named recovery tests deliberately bypass key health observations
	}
	upstreamModel, _ := channel.RoutingUpstreamModelName(testModel)
	fingerprint := model.ChannelKeyFingerprint(key.value)
	return !slices.ContainsFunc(channel.ChannelInfo.ModelUnavailable, func(entry model.ChannelModelUnavailability) bool {
		return entry.KeyFingerprint == fingerprint && entry.UpstreamModel == upstreamModel
	})
}

func testChannelAttempt(ctx context.Context, channel *model.Channel, spec channelTestSpec, testModel string, pinned *pinnedChannelTestKey, tik time.Time) testResult {
	endpointType, isStream, testUserID := spec.endpoint, spec.stream, spec.userID
	keyIndex := spec.keyIndex
	var unsupportedTestChannelTypes = []int{
		constant.ChannelTypeMidjourney,
		constant.ChannelTypeMidjourneyPlus,
		constant.ChannelTypeSunoAPI,
		constant.ChannelTypeKling,
		constant.ChannelTypeJimeng,
		constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVidu,
		constant.ChannelTypeTaskPlugin,
	}
	// A task plugin channel whose plugin has relay routes tests like an
	// Advanced Custom channel.
	plugin, bound := service.PluginOfChannel(channel)
	providerChannel := bound && plugin.Meta.HasProviderPart()
	if lo.Contains(unsupportedTestChannelTypes, channel.Type) && !providerChannel {
		channelTypeName := constant.GetChannelTypeName(channel.Type)
		return testResult{
			localErr: fmt.Errorf("%s channel test is not supported", channelTypeName),
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Only the server-owned synthetic prompt follows the caller's context.
	prompt := channelTestPromptFromContext(ctx)

	endpointType = normalizeChannelTestEndpoint(channel, endpointType)
	if endpointType == "" {
		endpointType = string(autoChannelTestEndpoint(channel, testModel))
		// A task plugin tests the default route if served, otherwise its first
		// supported test route for the model. Explicit endpoint choices stay fixed.
		if providerChannel {
			info, _ := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType))
			if endpoint, ok := providerTestEndpoint(plugin.Meta, strings.ReplaceAll(info.Path, "{model}", testModel), testModel); ok {
				endpointType = string(endpoint)
			}
		}
	}
	endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType))
	if !ok {
		return testResult{localErr: errors.New("unsupported endpoint type")}
	}
	requestPath := strings.ReplaceAll(endpointInfo.Path, "{model}", url.PathEscape(testModel))
	// Gemini represents streaming with a different path action, not a body flag.
	if isStream && constant.EndpointType(endpointType) == constant.EndpointTypeGemini {
		requestPath = strings.Replace(requestPath, ":generateContent", ":streamGenerateContent", 1)
	}
	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, requestPath, http.NoBody)

	cache, err := model.GetUserCache(testUserID)
	if err != nil {
		return testResult{
			localErr:    err,
			newAPIError: nil,
		}
	}
	cache.WriteContext(c)
	c.Set("id", testUserID)

	//c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	group, _ := model.GetUserGroup(testUserID, false)
	c.Set("group", group)

	// A channel test selects keys as an administrator (user id 0): a key bound to
	// a user stays eligible, and the tested model decides which key is used. The
	// test user id is restored right after, since logs and billing still need it.
	c.Set("id", 0)
	var newAPIError *types.NewAPIError
	if pinned != nil {
		newAPIError = middleware.SetupChannelTestContext(c, channel, testModel, pinned.value, pinned.index)
	} else {
		newAPIError = middleware.SetupContextForSelectedChannel(c, channel, testModel)
	}
	c.Set("id", testUserID)
	if newAPIError != nil {
		index := keyIndex
		if index < 0 {
			index = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		}
		return testResult{
			context:     c,
			localErr:    newAPIError,
			newAPIError: newAPIError,
			keyIndex:    index,
		}
	}
	if keyIndex < 0 {
		// Attribute a later failure to the key that actually served the request.
		keyIndex = servedTestKeyIndex(c, channel)
	}

	request := buildTestRequest(testModel, constant.EndpointType(endpointType), spec.endpoint == "", isStream, prompt)
	if request == nil {
		return testResult{localErr: errors.New(i18n.T(c, i18n.MsgRelayChannelEndpointUnsupported))}
	}
	result := runChannelTestRequest(c, w, channel, testUserID, request, prompt, tik)
	result.keyIndex = keyIndex
	return result
}

func channelTestPromptFromContext(ctx context.Context) *testPrompt {
	if prompt, ok := ctx.Value(channelTestPromptKey).(testPrompt); ok {
		return &prompt
	}
	return nil
}

// A request guard must not hide a provider's optional Responses finalizer.
type fingerprintFinalizingAdaptor struct {
	*fingerprintBoundedAdaptor
	relaychannel.ResponsesRequestFinalizer
}

// runChannelTestRequest builds, prices and sends a single admitted attempt.
func runChannelTestRequest(c *gin.Context, w *httptest.ResponseRecorder, channel *model.Channel, testUserID int, request dto.Request, prompt *testPrompt, tik time.Time) (outcome testResult) {
	relayFormat, _ := relaycommon.GuessRelayFormatFromRequest(request)
	if _, compact := request.(*dto.OpenAIResponsesCompactionRequest); compact {
		relayFormat = types.RelayFormatOpenAIResponsesCompaction
	}
	info, err := relaycommon.GenRelayInfo(c, relayFormat, request, nil)

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
		}
	}

	info.IsChannelTest = true
	info.DisablePing = true
	info.InitChannelMeta(c)
	relay.InitChannelDiagnostic(c)

	if err := attachTestBillingRequestInput(info, request); err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeChannelModelMappedError),
		}
	}
	if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry()),
		}
	}

	// InitChannelMeta resolved the API type, including provider plugins.
	apiType := info.ApiType
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		err := fmt.Errorf("invalid api type: %d, adaptor is nil", apiType)
		return testResult{context: c, localErr: err, newAPIError: types.NewError(err, types.ErrorCodeInvalidApiType)}
	}

	priceData, err := helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest)),
		}
	}

	adaptor.Init(info)
	if prompt != nil {
		bounded := &fingerprintBoundedAdaptor{Adaptor: adaptor, maxOutputTokens: prompt.MaxTokens}
		if finalizer, ok := adaptor.(relaychannel.ResponsesRequestFinalizer); ok {
			adaptor = &fingerprintFinalizingAdaptor{fingerprintBoundedAdaptor: bounded, ResponsesRequestFinalizer: finalizer}
		} else {
			adaptor = bounded
		}
	}
	var knownUsage *dto.Usage
	defer func() {
		if knownUsage == nil {
			return
		}
		outcome.promptTokens = knownUsage.PromptTokens
		outcome.completionTokens = knownUsage.CompletionTokens
		outcome.consumedUsage = knownUsage.PromptTokens > 0 || knownUsage.CompletionTokens > 0 || knownUsage.TotalTokens > 0 ||
			knownUsage.InputTokens > 0 || knownUsage.OutputTokens > 0
		// A failed stream can carry real usage even if the adaptor correctly
		// returns no separate API error after writing output. Record it once;
		// failed attempts without usage, including admission refusals, stay free.
		if outcome.consumedUsage || (outcome.localErr == nil && outcome.newAPIError == nil) {
			recordChannelTestUsage(c, channel.Id, testUserID, info, priceData, knownUsage, tik)
		}
	}()
	usageA, respErr := relay.RunChannelDiagnostic(c, info, adaptor, request)
	switch usage := usageA.(type) {
	case *dto.Usage:
		knownUsage = usage
	case dto.Usage:
		knownUsage = &usage
	}
	if respErr != nil {
		if service.RelayErrorSource(respErr) == "upstream" {
			service.ResetStatusCode(respErr, c.GetString("status_code_mapping"))
		}
		return testResult{
			context:     c,
			localErr:    respErr,
			newAPIError: respErr,
		}
	}
	if err := c.Request.Context().Err(); err != nil {
		return testResult{context: c, localErr: err}
	}
	usage, usageErr := coerceTestUsage(usageA, info.IsStream, info.GetEstimatePromptTokens())
	if usageErr != nil {
		return testResult{
			context:     c,
			localErr:    usageErr,
			newAPIError: types.NewOpenAIError(usageErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
		}
	}
	result := w.Result()
	respBody, err := readTestResponseBody(result.Body, info.IsStream)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError),
		}
	}
	if bodyErr := validateTestResponseBody(respBody, info.IsStream); bodyErr != nil {
		return testResult{
			context:     c,
			localErr:    bodyErr,
			newAPIError: types.NewOpenAIError(bodyErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
		}
	}
	if status := info.StreamStatus; status != nil {
		stream := status.OutcomeSnapshot()
		badEnd := info.IsStream && !status.IsNormalEnd()
		badOutcome := stream.Response != relaycommon.ResponseOutcomeCompleted && (stream.ExpectsTerminal || stream.Response != relaycommon.ResponseOutcomeUnknown)
		if badEnd || stream.HasErrors || badOutcome {
			err := errors.New("upstream stream did not complete successfully")
			return testResult{context: c, localErr: err, newAPIError: types.NewError(err, types.ErrorCodeBadResponseBody)}
		}
	}
	knownUsage = usage // allow estimated fallback only for a validated success
	return testResult{context: c, responseBody: respBody}
}

func recordChannelTestUsage(c *gin.Context, channelID, userID int, info *relaycommon.RelayInfo, priceData hosttypes.PriceData, usage *dto.Usage, started time.Time) {
	info.SetEstimatePromptTokens(usage.PromptTokens)
	quota, tieredResult := settleTestQuota(info, priceData, usage)
	other := buildTestLogOther(c, info, priceData, usage, tieredResult)
	model.RecordConsumeLog(c, userID, model.RecordConsumeLogParams{
		ChannelId:        channelID,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        info.OriginModelName,
		TokenName:        "Model test",
		Quota:            quota,
		Content:          []*common.Message{common.NewMessage("Model test")},
		UseTimeSeconds:   int(time.Since(started) / time.Second),
		IsStream:         info.IsStream,
		Group:            info.UsingGroup,
		Other:            other,
	})
}

func attachTestBillingRequestInput(info *relaycommon.RelayInfo, request dto.Request) error {
	if info == nil {
		return nil
	}

	input, err := helper.BuildBillingExprRequestInputFromRequest(request, info.RequestHeaders)
	if err != nil {
		return err
	}
	info.BillingRequestInput = &input
	return nil
}

func settleTestQuota(info *relaycommon.RelayInfo, priceData hosttypes.PriceData, usage *dto.Usage) (int, *billingexpr.TieredResult) {
	if usage != nil && info != nil && info.TieredBillingSnapshot != nil {
		isClaudeUsageSemantic := usage.UsageSemantic == "anthropic" || info.GetFinalRequestRelayFormat() == types.RelayFormatClaude
		usedVars := billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString)
		if ok, quota, result := service.TryTieredSettle(info, service.BuildTieredTokenParams(usage, isClaudeUsageSemantic, usedVars)); ok {
			return quota, result
		}
	}

	quota := 0
	if !priceData.UsePrice {
		completionQuota := common.QuotaRound(float64(usage.CompletionTokens) * priceData.CompletionRatio)
		quota = common.QuotaRound(float64(usage.PromptTokens) + float64(completionQuota))
		quota = common.QuotaRound(float64(quota) * priceData.ModelRatio)
		if priceData.ModelRatio != 0 && quota <= 0 {
			quota = 1
		}
		return quota, nil
	}

	return common.QuotaFromFloat(priceData.ModelPrice * common.QuotaPerUnit), nil
}

func buildTestLogOther(c *gin.Context, info *relaycommon.RelayInfo, priceData hosttypes.PriceData, usage *dto.Usage, tieredResult *billingexpr.TieredResult) *model.LogOther {
	other := service.GenerateTextOtherInfo(c, info, priceData.ModelRatio, priceData.GroupRatioInfo.GroupRatio, priceData.CompletionRatio,
		usage.PromptTokensDetails.CachedTokens, priceData.CacheRatio, priceData.ModelPrice, priceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		service.InjectTieredBillingInfo(other, info, tieredResult)
	}
	return other
}

func coerceTestUsage(usageAny any, isStream bool, estimatePromptTokens int) (*dto.Usage, error) {
	switch u := usageAny.(type) {
	case *dto.Usage:
		if u != nil {
			return u, nil
		}
	case dto.Usage:
		return &u, nil
	}
	if !isStream {
		if usage, ok := usageAny.(*dto.Usage); usageAny == nil || (ok && usage == nil) {
			return nil, errors.New("usage is nil")
		}
		return nil, fmt.Errorf("invalid usage type: %T", usageAny)
	}
	return &dto.Usage{PromptTokens: estimatePromptTokens, TotalTokens: estimatePromptTokens}, nil
}

func readTestResponseBody(body io.ReadCloser, isStream bool) ([]byte, error) {
	defer func() { _ = body.Close() }()
	const maxStreamLogBytes = 8 << 10
	if isStream {
		return io.ReadAll(io.LimitReader(body, maxStreamLogBytes))
	}
	return io.ReadAll(body)
}

func detectErrorFromTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return nil
	}
	if message := detectErrorMessageFromJSONBytes(b); message != "" {
		return fmt.Errorf("upstream error: %s", message)
	}

	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if message := detectErrorMessageFromJSONBytes(payload); message != "" {
			return fmt.Errorf("upstream error: %s", message)
		}
	}

	return nil
}

func validateStreamTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return errors.New("stream response body is empty")
	}

	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}

		return nil
	}

	return errors.New("stream response body does not contain a valid stream event")
}

func validateTestResponseBody(respBody []byte, isStream bool) error {
	if bodyErr := detectErrorFromTestResponseBody(respBody); bodyErr != nil {
		return bodyErr
	}
	if isStream {
		return validateStreamTestResponseBody(respBody)
	}
	return nil
}

func shouldUseStreamForAutomaticChannelTest(channel *model.Channel) bool {
	return channel != nil && channel.Type == constant.ChannelTypeCodex
}

func detectErrorMessageFromJSONBytes(jsonBytes []byte) string {
	if len(jsonBytes) == 0 {
		return ""
	}
	if jsonBytes[0] != '{' && jsonBytes[0] != '[' {
		return ""
	}
	errVal := ojson.GetBytes(jsonBytes, "error")
	if !errVal.Exists() || errVal.Type == ojson.Null {
		return ""
	}

	message := ojson.GetBytes(jsonBytes, "error.message").String()
	if message == "" {
		message = ojson.GetBytes(jsonBytes, "error.error.message").String()
	}
	if message == "" && errVal.Type == ojson.String {
		message = errVal.String()
	}
	if message == "" {
		message = errVal.Raw
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "upstream returned error payload"
	}
	return message
}

func autoChannelTestEndpoint(channel *model.Channel, model string) constant.EndpointType {
	lower := strings.ToLower(model)
	settings := channel.GetSetting()
	switch {
	case strings.Contains(lower, "codex") && (len(settings.SupportedEndpoints) == 0 || slices.Contains(settings.SupportedEndpoints, dto.ChannelEndpointResponses)):
		return constant.EndpointTypeOpenAIResponse
	case channel.Type == constant.ChannelTypeVolcEngine && strings.Contains(model, "seedream"):
		return constant.EndpointTypeImageGeneration
	case strings.Contains(lower, "embedding"), strings.HasPrefix(model, "m3e"), strings.Contains(model, "bge-"), strings.Contains(model, "embed"), channel.Type == constant.ChannelTypeMokaAI:
		return constant.EndpointTypeEmbeddings
	case strings.Contains(lower, "rerank"):
		return constant.EndpointTypeJinaRerank
	default:
		// Use a Chat input unless the model/route needs another shape. The
		// production resolver can upgrade this to Responses, not vice versa.
		return constant.EndpointTypeOpenAI
	}
}

func buildTestRequest(model string, endpoint constant.EndpointType, automatic, isStream bool, prompt *testPrompt) dto.Request {
	const testResponsesInput = `[{"role":"user","content":"hi"}]`
	instruction := "hi"
	maxTokens := uint(16)
	geminiMaxTokens := uint(3000)
	if prompt != nil {
		if prompt.Prompt != "" {
			instruction = prompt.Prompt
		}
		if prompt.MaxTokens > 0 {
			maxTokens = prompt.MaxTokens
			geminiMaxTokens = prompt.MaxTokens
		}
	}

	switch endpoint {
	case constant.EndpointTypeEmbeddings:
		return &dto.EmbeddingRequest{Model: model, Input: []any{"hello world"}}
	case constant.EndpointTypeImageGeneration:
		return &dto.ImageRequest{Model: model, Prompt: "a cute cat", N: lo.ToPtr(uint(1)), Size: "1024x1024"}
	case constant.EndpointTypeJinaRerank:
		return &dto.RerankRequest{
			Model:     model,
			Query:     "What is Deep Learning?",
			Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
			TopN:      lo.ToPtr(2),
		}
	case constant.EndpointTypeOpenAIResponse:
		input, err := common.Marshal([]dto.Message{{Role: "user", Content: instruction}})
		if err != nil {
			input = []byte(testResponsesInput)
		}
		request := &dto.OpenAIResponsesRequest{Model: model, Input: input, Stream: lo.ToPtr(isStream)}
		if prompt != nil {
			request.MaxOutputTokens = lo.ToPtr(maxTokens)
		}
		return request
	case constant.EndpointTypeOpenAIResponseCompact:
		return &dto.OpenAIResponsesCompactionRequest{Model: model, Input: json.RawMessage(testResponsesInput)}
	case constant.EndpointTypeAnthropic:
		return &dto.ClaudeRequest{
			Model: model, Stream: lo.ToPtr(isStream), MaxTokens: lo.ToPtr(maxTokens),
			Messages: []dto.ClaudeMessage{{Role: "user", Content: instruction}},
		}
	case constant.EndpointTypeGemini:
		return &dto.GeminiChatRequest{
			Contents:         []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: instruction}}}},
			GenerationConfig: dto.GeminiChatGenerationConfig{MaxOutputTokens: lo.ToPtr(geminiMaxTokens)},
		}
	case constant.EndpointTypeOpenAI:
		request := &dto.GeneralOpenAIRequest{
			Model: model, Stream: lo.ToPtr(isStream), MaxTokens: lo.ToPtr(maxTokens),
			Messages: []dto.Message{{Role: "user", Content: instruction}},
		}
		if isStream {
			request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		}
		if automatic && prompt == nil {
			switch {
			case dto.IsOpenAIReasoningOModel(model):
				request.MaxCompletionTokens, request.MaxTokens = request.MaxTokens, nil
			case strings.Contains(model, "thinking"):
				if strings.Contains(model, "claude") {
					request.MaxTokens = nil
				} else {
					request.MaxTokens = lo.ToPtr(uint(50))
				}
			case strings.Contains(model, "gemini"):
				request.MaxTokens = lo.ToPtr(geminiMaxTokens)
			}
		}
		return request
	default:
		return nil
	}
}

func TestChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	keyIndex := -1
	if raw := query.Get("key_index"); raw != "" {
		keyIndex, err = strconv.Atoi(raw)
		if err != nil || keyIndex < 0 {
			common.ApiError(c, errors.New("key_index must be a non-negative integer"))
			return
		}
	}
	keyEntryID := int64(0)
	expectedKeyRevision := int64(0)
	entryIDValues, hasEntryID := query["key_entry_id"]
	revisionValues, hasExpectedRevision := query["expected_rev"]
	if hasEntryID || hasExpectedRevision {
		if !hasEntryID || !hasExpectedRevision || len(entryIDValues) != 1 || len(revisionValues) != 1 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		keyEntryID, err = strconv.ParseInt(entryIDValues[0], 10, 64)
		if err != nil || keyEntryID <= 0 || strconv.FormatInt(keyEntryID, 10) != entryIDValues[0] {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		expectedKeyRevision, err = strconv.ParseInt(revisionValues[0], 10, 64)
		if err != nil || expectedKeyRevision <= 0 || strconv.FormatInt(expectedKeyRevision, 10) != revisionValues[0] {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	testModel := query.Get("model")
	endpointType := query.Get("endpoint_type")
	isStream, _ := strconv.ParseBool(query.Get("stream"))
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = c.Request.Context()
	}
	result := runChannelTest(requestCtx, channel, channelTestSpec{
		userID:           testUserID,
		model:            testModel,
		endpoint:         endpointType,
		stream:           isStream,
		keyIndex:         keyIndex,
		keyEntryID:       keyEntryID,
		keyEntryRevision: expectedKeyRevision,
	})
	if result.localErr != nil {
		resp := gin.H{
			"success": false,
			"message": result.localErr.Error(),
			"time":    0.0,
		}
		if result.newAPIError != nil {
			resp["error_code"] = result.newAPIError.GetErrorCode()
		}
		var keyErr *channelTestKeyError
		if errors.As(result.localErr, &keyErr) {
			resp["error_code"] = keyErr.code
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	milliseconds := result.elapsed.Milliseconds()
	// A test of one named key acts on that key only. Queue time is not upstream latency.
	if keyIndex < 0 && keyEntryID == 0 {
		go channel.UpdateResponseTime(milliseconds)
	}
	consumedTime := result.elapsed.Seconds()
	if result.newAPIError != nil {
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"message":    result.newAPIError.Error(),
			"time":       consumedTime,
			"error_code": result.newAPIError.GetErrorCode(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"time":    consumedTime,
	})
}

// channelTestSummary records the outcome of one channel test cycle so the
// system task can persist a per-run result for history.
type channelTestSummary struct {
	Tested    int `json:"tested"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Disabled  int `json:"disabled"`
	Enabled   int `json:"enabled"`
}

func testChannelForHealthCheck(ctx context.Context, channel *model.Channel, testUserID int, allowDisable bool, disableThreshold int64) channelTestSummary {
	summary := channelTestSummary{}
	result := runChannelTest(ctx, channel, channelTestSpec{userID: testUserID, keyIndex: -1, healthCheck: true})
	if ctx.Err() != nil || result.channel == nil || service.IsChannelCapacityError(result.newAPIError) || relay.DiagnosticUpstreamBusy(result.context) {
		return summary
	}
	channel = result.channel
	isChannelEnabled := channel.Status == common.ChannelStatusEnabled
	milliseconds := result.elapsed.Milliseconds()
	summary.Tested++

	shouldBanChannel := false
	newAPIError := result.newAPIError
	if newAPIError != nil {
		shouldBanChannel = service.ShouldDisableChannel(result.newAPIError)
	}

	if common.AutomaticDisableChannelEnabled && !shouldBanChannel && result.localErr == nil {
		if milliseconds > disableThreshold {
			// The reason is stored with the channel and sent in the disable
			// notice; no request states a language here.
			err := errors.New(i18n.Translate("", i18n.MsgChannelResponseTimeExceeded, map[string]any{
				"Seconds":   fmt.Sprintf("%.2f", float64(milliseconds)/1000.0),
				"Threshold": fmt.Sprintf("%.2f", float64(disableThreshold)/1000.0),
			}))
			newAPIError = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout)
			shouldBanChannel = true
		}
	}

	if result.localErr == nil && newAPIError == nil {
		summary.Succeeded++
	} else {
		summary.Failed++
	}

	if allowDisable && isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
		// The health check reaches a channel through one key, so a failure is
		// attributed to that key: the channel is disabled only once none of its
		// keys can serve a request any more (handlerMultiKeyUpdate).
		processChannelError(result.context, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError, nil)
		summary.Disabled++
	}

	if result.localErr == nil && result.keyIndex >= 0 && !isChannelEnabled && service.ShouldEnableChannel(newAPIError, channel.Status) {
		// The test named one key and that key works, so restore it. Recovery
		// remains per key: the channel comes back once any key works again.
		key := common.GetContextKeyString(result.context, constant.ContextKeyChannelKey)
		service.EnableChannel(channel.Id, key, channel.Name)
		summary.Enabled++
	}

	channel.UpdateResponseTime(milliseconds)
	return summary
}

// runChannelTestWorkers executes independent channel tests with bounded
// concurrency. Results and progress are reduced by the caller goroutine, so
// summary counts and the progress reporter remain serialized.
func runChannelTestWorkers(
	ctx context.Context,
	channels []*model.Channel,
	concurrency int,
	run func(context.Context, *model.Channel) channelTestSummary,
	report func(processed, total int),
) channelTestSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	total := len(channels)
	if report != nil {
		report(0, total)
	}
	if total == 0 {
		return channelTestSummary{}
	}

	workerCount := min(operation_setting.NormalizeChannelTestConcurrency(concurrency), total)
	jobs := make(chan *model.Channel)
	results := make(chan channelTestSummary)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case channel, ok := <-jobs:
					if !ok {
						return
					}
					if ctx.Err() != nil {
						return
					}

					result := channelTestSummary{}
					if channel != nil && channel.Status != common.ChannelStatusManuallyDisabled {
						result = run(ctx, channel)
					}

					results <- result

					if common.RequestInterval > 0 {
						select {
						case <-ctx.Done():
							return
						case <-time.After(common.RequestInterval):
						}
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, channel := range channels {
			select {
			case <-ctx.Done():
				return
			case jobs <- channel:
			}
		}
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	summary := channelTestSummary{}
	processed := 0
	for result := range results {
		summary.Tested += result.Tested
		summary.Succeeded += result.Succeeded
		summary.Failed += result.Failed
		summary.Disabled += result.Disabled
		summary.Enabled += result.Enabled
		processed++
		if report != nil && ctx.Err() == nil {
			report(processed, total)
		}
	}
	return summary
}

// performChannelTests runs channel health checks with the configured bounded
// concurrency and honors cancellation when a system-task runner loses its
// lease.
func performChannelTests(ctx context.Context, channels []*model.Channel, testUserID int, allowDisable bool, concurrency int, report func(processed, total int)) channelTestSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	disableThreshold := int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // an impossible value
	}
	return runChannelTestWorkers(
		ctx,
		channels,
		concurrency,
		func(ctx context.Context, channel *model.Channel) channelTestSummary {
			return testChannelForHealthCheck(ctx, channel, testUserID, allowDisable, disableThreshold)
		},
		report,
	)
}

// runChannelTestTask runs one synchronous channel test cycle for the system task
// runner (both the scheduled job and the manual "test all channels" trigger go
// through here). It honors ctx cancellation so a runner that loses its lease
// stops promptly. mode selects the channel set: an empty mode falls back to the
// configured monitor ChannelTestMode (scheduled behavior), while a manual
// trigger passes ChannelTestModeScheduledAll to test every channel. When notify
// is set, the administrators who receive channel notices are notified on
// completion. Cross-instance execution is guarded by the system task per-type
// lock, so no process-local guard is needed.
func runChannelTestTask(ctx context.Context, mode string, notify bool, report func(processed, total int)) (channelTestSummary, error) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return channelTestSummary{}, err
	}
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return channelTestSummary{}, err
	}
	if strings.TrimSpace(mode) == "" {
		mode = operation_setting.GetMonitorSetting().ChannelTestMode
	}
	selected := selectChannelsForAutomaticTest(channels, mode)
	allowDisable := mode != operation_setting.ChannelTestModePassiveRecovery
	concurrency := operation_setting.GetMonitorSetting().ChannelTestConcurrency
	summary := performChannelTests(ctx, selected, testUserID, allowDisable, concurrency, report)
	if notify && (ctx == nil || ctx.Err() == nil) {
		service.NotifyChannelWatchers(dto.NotifyTypeChannelTest, func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelTestCompletedSubject), i18n.Translate(lang, i18n.MsgChannelTestCompletedContent)
		})
	}
	return summary, nil
}

func selectChannelsForAutomaticTest(channels []*model.Channel, mode string) []*model.Channel {
	selected := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if mode == operation_setting.ChannelTestModeAutoBanOnly && !channel.GetAutoBan() {
			continue
		}
		if mode == operation_setting.ChannelTestModePassiveRecovery && channel.Status != common.ChannelStatusAutoDisabled {
			continue
		}
		selected = append(selected, channel)
	}
	return selected
}

// TestAllChannels enqueues a channel_test system task instead of running the
// test loop inline. If any channel_test task is already active, the manual run is
// rejected so the caller does not mistake a scheduled run for this manual one.
func TestAllChannels(c *gin.Context) {
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeChannelTest, channelTestTaskPayload{
		Mode:   operation_setting.ChannelTestModeScheduledAll,
		Notify: true,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		msg := common.NewMessage("A channel test task is already running or queued. Cannot start this manual task")
		common.ApiErrorStatus(c, http.StatusConflict, msg, gin.H{"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
			"type":    task.Type,
		}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
		},
	})
}
