package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/regexp"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/advancedcustom"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	"github.com/QuantumNous/new-api/relay/channel/providerplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

const (
	channelUpstreamModelUpdateTaskDefaultIntervalMinutes  = 30
	channelUpstreamModelUpdateTaskBatchSize               = 100
	channelUpstreamModelUpdateMinCheckIntervalSeconds     = 300
	channelUpstreamModelUpdateNotifySuppressWindowSeconds = 86400
	channelUpstreamModelUpdateNotifyMaxChannelDetails     = 8
	channelUpstreamModelUpdateNotifyMaxModelDetails       = 12
	channelUpstreamModelUpdateNotifyMaxFailedChannelIDs   = 10
)

var channelUpstreamModelUpdateSelectFields = []string{
	"id",
	"name",
	"type",
	"key",
	"status",
	"base_url",
	"models",
	"model_mapping",
	"settings",
	"setting",
	"other",
	"group",
	"priority",
	"weight",
	"tag",
	"channel_info",
	"header_override",
	"param_override",
}

var channelUpstreamModelUpdateNotifyState = struct {
	sync.Mutex
	lastNotifiedAt      int64
	lastChangedChannels int
	lastFailedChannels  int
}{}

type applyChannelUpstreamModelUpdatesRequest struct {
	ID           int      `json:"id"`
	AddModels    []string `json:"add_models"`
	RemoveModels []string `json:"remove_models"`
	IgnoreModels []string `json:"ignore_models"`
}

type applyAllChannelUpstreamModelUpdatesResult struct {
	ChannelID             int      `json:"channel_id"`
	ChannelName           string   `json:"channel_name"`
	AddedModels           []string `json:"added_models"`
	RemovedModels         []string `json:"removed_models"`
	RemainingModels       []string `json:"remaining_models"`
	RemainingRemoveModels []string `json:"remaining_remove_models"`
}

type detectChannelUpstreamModelUpdatesResult struct {
	ChannelID       int      `json:"channel_id"`
	ChannelName     string   `json:"channel_name"`
	AddModels       []string `json:"add_models"`
	RemoveModels    []string `json:"remove_models"`
	LastCheckTime   int64    `json:"last_check_time"`
	AutoAddedModels int      `json:"auto_added_models"`
}

type upstreamModelUpdateChannelSummary struct {
	ChannelName string
	AddCount    int
	RemoveCount int
}

func normalizeModelNames(models []string) []string {
	return lo.Uniq(lo.FilterMap(models, func(model string, _ int) (string, bool) {
		trimmed := strings.TrimSpace(model)
		return trimmed, trimmed != ""
	}))
}

func mergeModelNames(base []string, appended []string) []string {
	merged := normalizeModelNames(base)
	seen := make(map[string]struct{}, len(merged))
	for _, model := range merged {
		seen[model] = struct{}{}
	}
	for _, model := range normalizeModelNames(appended) {
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		merged = append(merged, model)
	}
	return merged
}

func subtractModelNames(base []string, removed []string) []string {
	removeSet := make(map[string]struct{}, len(removed))
	for _, model := range normalizeModelNames(removed) {
		removeSet[model] = struct{}{}
	}
	return lo.Filter(normalizeModelNames(base), func(model string, _ int) bool {
		_, ok := removeSet[model]
		return !ok
	})
}

func intersectModelNames(base []string, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, model := range normalizeModelNames(allowed) {
		allowedSet[model] = struct{}{}
	}
	return lo.Filter(normalizeModelNames(base), func(model string, _ int) bool {
		_, ok := allowedSet[model]
		return ok
	})
}

func applySelectedModelChanges(originModels []string, addModels []string, removeModels []string) []string {
	// Add wins when the same model appears in both selected lists.
	normalizedAdd := normalizeModelNames(addModels)
	normalizedRemove := subtractModelNames(normalizeModelNames(removeModels), normalizedAdd)
	return subtractModelNames(mergeModelNames(originModels, normalizedAdd), normalizedRemove)
}

func normalizeChannelModelMapping(channel *model.Channel) map[string]string {
	if channel == nil || channel.ModelMapping == nil {
		return nil
	}
	rawMapping := strings.TrimSpace(*channel.ModelMapping)
	if rawMapping == "" || rawMapping == "{}" {
		return nil
	}
	parsed := make(map[string]string)
	if err := common.UnmarshalJsonStr(rawMapping, &parsed); err != nil {
		return nil
	}
	normalized := make(map[string]string, len(parsed))
	for source, target := range parsed {
		normalizedSource := strings.TrimSpace(source)
		normalizedTarget := strings.TrimSpace(target)
		if normalizedSource == "" || normalizedTarget == "" {
			continue
		}
		normalized[normalizedSource] = normalizedTarget
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func collectPendingUpstreamModelChangesFromModels(
	localModels []string,
	upstreamModels []string,
	ignoredModels []string,
	modelMapping map[string]string,
) (pendingAddModels []string, pendingRemoveModels []string) {
	localSet := make(map[string]struct{})
	localModels = normalizeModelNames(localModels)
	upstreamModels = normalizeModelNames(upstreamModels)
	for _, modelName := range localModels {
		localSet[modelName] = struct{}{}
	}
	upstreamSet := make(map[string]struct{}, len(upstreamModels))
	for _, modelName := range upstreamModels {
		upstreamSet[modelName] = struct{}{}
	}

	normalizedIgnoredModels := normalizeModelNames(ignoredModels)

	redirectSourceSet := make(map[string]struct{}, len(modelMapping))
	redirectTargetSet := make(map[string]struct{}, len(modelMapping))
	for source, target := range modelMapping {
		redirectSourceSet[source] = struct{}{}
		redirectTargetSet[target] = struct{}{}
	}

	coveredUpstreamSet := make(map[string]struct{}, len(localSet)+len(redirectTargetSet))
	for modelName := range localSet {
		coveredUpstreamSet[modelName] = struct{}{}
	}
	for modelName := range redirectTargetSet {
		coveredUpstreamSet[modelName] = struct{}{}
	}

	pendingAdd := lo.Filter(upstreamModels, func(modelName string, _ int) bool {
		if _, ok := coveredUpstreamSet[modelName]; ok {
			return false
		}
		if lo.ContainsBy(normalizedIgnoredModels, func(ignoredModel string) bool {
			if regexBody, ok := strings.CutPrefix(ignoredModel, "regex:"); ok {
				matched, err := regexp.MatchString(strings.TrimSpace(regexBody), modelName)
				return err == nil && matched
			}
			return ignoredModel == modelName
		}) {
			return false
		}
		return true
	})
	pendingRemove := lo.Filter(localModels, func(modelName string, _ int) bool {
		// Redirect source models are virtual aliases and should not be removed
		// only because they are absent from upstream model list.
		if _, ok := redirectSourceSet[modelName]; ok {
			return false
		}
		_, ok := upstreamSet[modelName]
		return !ok
	})
	return normalizeModelNames(pendingAdd), normalizeModelNames(pendingRemove)
}

func collectPendingUpstreamModelChanges(parent context.Context, channel *model.Channel, settings dto.ChannelOtherSettings) (pendingAddModels []string, pendingRemoveModels []string, err error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	upstreamModels, err := fetchChannelUpstreamModelIDsWithAdmission(ctx, channel, admission)
	if err != nil {
		return nil, nil, err
	}
	pendingAddModels, pendingRemoveModels = collectPendingUpstreamModelChangesFromModels(
		channel.GetModels(),
		upstreamModels,
		settings.UpstreamModelUpdateIgnoredModels,
		normalizeChannelModelMapping(channel),
	)
	return pendingAddModels, pendingRemoveModels, nil
}

func getUpstreamModelUpdateMinCheckIntervalSeconds() int64 {
	interval := int64(common.GetEnvOrDefault(
		"CHANNEL_UPSTREAM_MODEL_UPDATE_MIN_CHECK_INTERVAL_SECONDS",
		channelUpstreamModelUpdateMinCheckIntervalSeconds,
	))
	if interval < 0 {
		return channelUpstreamModelUpdateMinCheckIntervalSeconds
	}
	return interval
}

func parseOpenAIModelIDs(body []byte) ([]string, error) {
	ids, err := parseAuthoritativeOpenAIModelIDs(body)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("OpenAI Models response contains no valid model IDs")
	}
	return ids, nil
}

func parseAuthoritativeOpenAIModelIDs(body []byte) ([]string, error) {
	var result struct {
		Data    *[]OpenAIModel  `json:"data"`
		Error   json.RawMessage `json:"error"`
		Success *bool           `json:"success"`
	}
	if err := common.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid OpenAI Models response: %w", err)
	}
	if result.Data == nil {
		return nil, fmt.Errorf("invalid OpenAI Models response: data is required")
	}
	if len(result.Error) > 0 && string(result.Error) != "null" && string(result.Error) != `""` {
		return nil, fmt.Errorf("upstream returned an error response")
	}
	if result.Success != nil && !*result.Success {
		return nil, fmt.Errorf("upstream returned an error response")
	}
	for _, item := range *result.Data {
		if strings.TrimSpace(item.ID) == "" {
			return nil, fmt.Errorf("invalid OpenAI Models response: model ID is required")
		}
	}
	ids := normalizeModelNames(lo.Map(*result.Data, func(item OpenAIModel, _ int) string { return item.ID }))
	return ids, nil
}

func sanitizeFetchModelsError(err error, key string) error {
	if err == nil {
		return nil
	}

	// net/http includes the complete request URL in url.Error. Discovery routes
	// may put the API key in a custom query name or value, so never return that
	// wrapper to an API client.
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}

	message := err.Error()
	key = strings.TrimSpace(key)
	if key != "" {
		message = strings.ReplaceAll(message, key, "[REDACTED]")
		message = strings.ReplaceAll(message, url.QueryEscape(key), "[REDACTED]")
		message = strings.ReplaceAll(message, url.PathEscape(key), "[REDACTED]")
	}
	return errors.New(message)
}

func sanitizeAdvancedCustomRequestError(err error, key string, requestURL string) error {
	err = sanitizeFetchModelsError(err, key)
	if err == nil {
		return nil
	}
	parsedURL, parseErr := url.Parse(requestURL)
	if parseErr != nil {
		return err
	}
	message := err.Error()
	for _, value := range parsedURL.Query() {
		for _, secret := range value {
			if secret == "" {
				continue
			}
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
			message = strings.ReplaceAll(message, url.QueryEscape(secret), "[REDACTED]")
			message = strings.ReplaceAll(message, url.PathEscape(secret), "[REDACTED]")
		}
	}
	if key != "" {
		message = strings.ReplaceAll(message, key, "[REDACTED]")
		message = strings.ReplaceAll(message, url.QueryEscape(key), "[REDACTED]")
		message = strings.ReplaceAll(message, url.PathEscape(key), "[REDACTED]")
	}
	return errors.New(message)
}

func getFetchModelsResponseBody(method string, requestURL string, channel *model.Channel, key string, headers http.Header, ctx context.Context, admission *service.ChannelDiagnosticAdmission) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers.Clone()
	if host := headers.Get("Host"); host != "" {
		request.Host = host
	}
	response, err := sendFetchModelsRequest(request, channel, key, admission)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return readFetchModelsResponse(response)
}

func readFetchModelsResponse(response *http.Response) ([]byte, error) {
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", response.StatusCode)
	}
	return io.ReadAll(response.Body)
}

// sendFetchModelsRequest keeps the management request on the channel's
// configured transport and, when an admission owner is supplied, holds the
// fixed-channel lease until the response body is closed.
func sendFetchModelsRequest(request *http.Request, channel *model.Channel, key string, admission *service.ChannelDiagnosticAdmission) (*http.Response, error) {
	settings := channel.GetSetting()
	call := service.ChannelManagementCall{
		Request:   request,
		Settings:  settings,
		ChannelID: channel.Id,
		Admission: admission,
		Timeout:   30 * time.Second,
	}
	if admission != nil && channel.Id > 0 {
		call.Reload = func() (*http.Request, dto.ChannelSettings, error) {
			fresh, err := reloadManagementChannel(request.Context(), channel.Id)
			if err != nil || fresh == nil {
				return nil, dto.ChannelSettings{}, fmt.Errorf("channel configuration changed during model discovery")
			}
			if fresh.Type != channel.Type || channelBaseURL(fresh) != channelBaseURL(channel) || !channelManagementKeyEnabled(fresh, key) {
				return nil, dto.ChannelSettings{}, fmt.Errorf("channel configuration changed during model discovery")
			}
			headers, err := buildFetchModelsHeaders(fresh, key)
			if err != nil {
				return nil, dto.ChannelSettings{}, sanitizeFetchModelsError(err, key)
			}
			freshRequest := request.Clone(request.Context())
			freshRequest.Header = headers
			freshRequest.Host = ""
			if host := headers.Get("Host"); host != "" {
				freshRequest.Host = host
			}
			return freshRequest, fresh.GetSetting(), nil
		}
	}
	return service.DoChannelManagementRequest(call)
}

func channelBaseURL(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	if baseURL := strings.TrimSpace(channel.GetBaseURL()); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	return strings.TrimRight(constant.GetChannelBaseURL(channel.Type), "/")
}

func channelManagementKeyEnabled(channel *model.Channel, key string) bool {
	if channel == nil {
		return false
	}
	key = strings.TrimSpace(key)
	for index, candidate := range channel.GetKeys() {
		if strings.TrimSpace(candidate) != key {
			continue
		}
		if !channel.ChannelInfo.IsMultiKey {
			return true
		}
		status, ok := channel.ChannelInfo.MultiKeyStatusList[index]
		return !ok || status == common.ChannelStatusEnabled
	}
	return false
}

func fetchChannelUpstreamModelIDs(channel *model.Channel) ([]string, error) {
	return fetchChannelUpstreamModelIDsForRequest(context.Background(), channel)
}

type channelModelDiscoveryOptions struct {
	Preview    *fetchModelsRequest
	SourceType int
	KeyIndex   *int
}

func fetchChannelUpstreamModelIDsForRequest(parent context.Context, channel *model.Channel, options ...channelModelDiscoveryOptions) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	return fetchChannelUpstreamModelIDsWithAdmission(ctx, channel, admission, options...)
}

func reloadManagementChannel(ctx context.Context, channelID int) (*model.Channel, error) {
	var channel model.Channel
	if err := model.DB.WithContext(ctx).First(&channel, channelID).Error; err != nil {
		return nil, err
	}
	return &channel, nil
}

// applyChannelModelPreview overlays submitted fields on the current saved row.
// Capacity and all other transport policy remain those of that current row.
func applyChannelModelPreview(channel *model.Channel, req *fetchModelsRequest) (*model.Channel, error) {
	if req == nil {
		return channel, nil
	}
	current := *channel
	if current.Type != constant.ChannelTypeAdvancedCustom && req.Type > 0 {
		current.Type = req.Type
	}
	if req.BaseURL != nil {
		current.BaseURL = common.GetPointer(strings.TrimSpace(*req.BaseURL))
	}
	if req.HeaderOverride != nil {
		current.HeaderOverride = common.GetPointer(strings.TrimSpace(*req.HeaderOverride))
	}
	if req.Proxy != nil {
		settings := current.GetSetting()
		settings.Proxy = strings.TrimSpace(*req.Proxy)
		current.SetSetting(settings)
	}
	if current.Type == constant.ChannelTypeAdvancedCustom {
		if req.AdvancedCustom != nil {
			var config dto.AdvancedCustomConfig
			if err := common.UnmarshalJsonStr(strings.TrimSpace(*req.AdvancedCustom), &config); err != nil {
				return nil, err
			}
			settings := current.GetOtherSettings()
			settings.AdvancedCustom = &config
			current.SetOtherSettings(settings)
		}
		return &current, nil
	}
	if key := strings.TrimSpace(req.Key); key != "" {
		if current.Type != constant.ChannelTypeCodex {
			key, _, _ = strings.Cut(key, "\n")
		}
		return newSingleKeyChannel(&current, key), nil
	}
	return &current, nil
}

func acquireDiscoverySnapshot(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission, options channelModelDiscoveryOptions) (*model.Channel, *service.ChannelCapacityLease, error) {
	if channel == nil {
		return nil, nil, fmt.Errorf("channel is required")
	}
	if channel.Id <= 0 || admission == nil {
		return channel, nil, nil
	}
	settings := channel.GetSetting()
	limit := discoveryConcurrencyLimit(settings)
	for attempt := range 3 {
		lease, apiErr := admission.Acquire(ctx, channel.Id, settings)
		if apiErr != nil {
			return nil, nil, apiErr
		}
		fresh, err := reloadManagementChannel(lease.Context(), channel.Id)
		if err != nil {
			lease.Release()
			return nil, nil, fmt.Errorf("failed to reload channel for management request")
		}
		expectedType := channel.Type
		if options.Preview != nil && options.SourceType > 0 {
			expectedType = options.SourceType
		}
		if fresh.Type != expectedType {
			lease.Release()
			return nil, nil, fmt.Errorf("channel configuration changed during model discovery")
		}
		if options.Preview == nil && !channel.ChannelInfo.IsMultiKey && fresh.ChannelInfo.IsMultiKey {
			key := strings.TrimSpace(channel.Key)
			index := slices.IndexFunc(fresh.GetKeys(), func(candidate string) bool { return strings.TrimSpace(candidate) == key })
			if index < 0 || !channelManagementKeyEnabled(fresh, key) {
				lease.Release()
				return nil, nil, fmt.Errorf("channel credential changed during model discovery")
			}
			fresh = newSingleKeyChannel(fresh, key)
		}
		fresh, err = applyChannelModelPreview(fresh, options.Preview)
		if err != nil {
			lease.Release()
			return nil, nil, err
		}
		currentSettings := fresh.GetSetting()
		currentLimit := discoveryConcurrencyLimit(currentSettings)
		if currentLimit == limit {
			return fresh, lease, nil
		}
		lease.Release()
		settings, limit = currentSettings, currentLimit
		if attempt == 2 {
			return nil, nil, fmt.Errorf("channel concurrency settings changed during admission")
		}
	}
	return nil, nil, fmt.Errorf("channel concurrency settings changed during admission")
}

func discoveryConcurrencyLimit(settings dto.ChannelSettings) int {
	if settings.MaxConcurrentRequests == nil {
		return 0
	}
	return max(0, *settings.MaxConcurrentRequests)
}

func fetchGeminiModelIDs(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission) ([]string, error) {
	initialKey, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return nil, fmt.Errorf("failed to get channel key: %w", apiErr)
	}
	current, lease, err := acquireDiscoverySnapshot(ctx, channel, admission, channelModelDiscoveryOptions{})
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
	}
	if !channelManagementKeyEnabled(current, strings.TrimSpace(initialKey)) && !channel.ChannelInfo.IsMultiKey {
		return nil, fmt.Errorf("channel credential changed during model discovery")
	}
	key := strings.TrimSpace(initialKey)
	if current.Id > 0 && current.ChannelInfo.IsMultiKey && !channel.ChannelInfo.IsMultiKey {
		key = strings.TrimSpace(current.Key)
	}
	headers := make(http.Header)
	headers.Set("x-goog-api-key", key)
	if err := applyFetchModelsHeaderOverrides(current, key, headers); err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	requestCtx := ctx
	if lease != nil {
		requestCtx = lease.Context()
	}
	models, err := gemini.FetchGeminiModels(requestCtx, channelBaseURL(current), key, current.GetSetting(), headers)
	if err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	return normalizeModelNames(models), nil
}
func fetchChannelUpstreamModelIDsWithAdmission(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission, choices ...channelModelDiscoveryOptions) ([]string, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}
	var options channelModelDiscoveryOptions
	if len(choices) > 0 {
		options = choices[0]
	}
	if channel.Type == constant.ChannelTypeCodex {
		var preview *service.CodexModelDiscoveryPreview
		if options.Preview != nil {
			preview = &service.CodexModelDiscoveryPreview{
				BaseURL: options.Preview.BaseURL, HeaderOverride: options.Preview.HeaderOverride, Proxy: options.Preview.Proxy,
				SourceType: options.SourceType,
			}
			if key := strings.TrimSpace(options.Preview.Key); key != "" {
				preview.Key = &key
			}
		}
		return service.FetchCodexChannelModels(ctx, channel, service.CodexModelDiscoveryOptions{
			Admission: admission,
			Override:  applyFetchModelsHeaderOverrides,
			Preview:   preview,
		})
	}
	if options.KeyIndex != nil {
		current, lease, err := acquireMultiKeyDiscoverySnapshot(ctx, channel, *options.KeyIndex, admission, options)
		if err != nil {
			return nil, err
		}
		if lease != nil {
			defer lease.Release()
			ctx = lease.Context()
		}
		if current.Type == constant.ChannelTypeTaskPlugin {
			plugin, ok := jsplugin.DefaultRegistry.Get(current.GetSetting().TaskPluginKey)
			if !ok {
				return nil, fmt.Errorf("task plugin is not registered")
			}
			if _, ok := plugin.Meta.ModelListRoute(); ok {
				return fetchProviderPluginUpstreamModelIDs(ctx, current, plugin, nil, *options.KeyIndex)
			}
			return normalizeModelNames(plugin.Meta.Models), nil
		}
		return fetchChannelUpstreamModelIDsSnapshot(ctx, newSingleKeyChannel(current, current.GetKeys()[*options.KeyIndex]), nil)
	}
	if channel.ChannelInfo.IsMultiKey {
		return fetchEnabledMultiKeyModelUnion(ctx, channel, admission, options)
	}
	current, lease, err := acquireDiscoverySnapshot(ctx, channel, admission, options)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
		ctx = lease.Context()
	}
	return fetchChannelUpstreamModelIDsSnapshot(ctx, current, nil)
}

func fetchChannelUpstreamModelIDsSnapshot(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission) ([]string, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}
	if channel.ChannelInfo.IsMultiKey {
		return fetchEnabledMultiKeyModelUnion(ctx, channel, admission, channelModelDiscoveryOptions{})
	}
	if channel.Type == constant.ChannelTypeTaskPlugin {
		plugin, ok := jsplugin.DefaultRegistry.Get(channel.GetSetting().TaskPluginKey)
		if !ok {
			return nil, fmt.Errorf("task plugin %q is not registered", channel.GetSetting().TaskPluginKey)
		}
		if _, ok := plugin.Meta.ModelListRoute(); ok {
			return fetchProviderPluginUpstreamModelIDs(ctx, channel, plugin, admission, -1)
		}
		return normalizeModelNames(plugin.Meta.Models), nil
	}
	baseURL := channelBaseURL(channel)

	if channel.Type == constant.ChannelTypeOllama {
		key, _, apiErr := channel.GetNextEnabledKey()
		if apiErr != nil {
			return nil, fmt.Errorf("failed to get channel key: %w", apiErr)
		}
		key = strings.TrimSpace(key)
		headers, err := buildFetchModelsHeaders(channel, key)
		if err != nil {
			return nil, sanitizeFetchModelsError(err, key)
		}
		options := ollama.OllamaRequestOptions{
			Header: headers, Settings: channel.GetSetting(), Admission: admission, ChannelID: channel.Id,
		}
		if channel.Id > 0 && admission != nil {
			options.Reload = func() (string, http.Header, dto.ChannelSettings, error) {
				fresh, err := reloadManagementChannel(ctx, channel.Id)
				if err != nil || fresh == nil || fresh.Type != constant.ChannelTypeOllama || !channelManagementKeyEnabled(fresh, key) {
					return "", nil, dto.ChannelSettings{}, fmt.Errorf("channel configuration changed during model discovery")
				}
				freshHeaders, err := buildFetchModelsHeaders(fresh, key)
				if err != nil {
					return "", nil, dto.ChannelSettings{}, sanitizeFetchModelsError(err, key)
				}
				return channelBaseURL(fresh), freshHeaders, fresh.GetSetting(), nil
			}
		}
		models, err := ollama.FetchOllamaModels(ctx, baseURL, key, options)
		if err != nil {
			return nil, err
		}
		return normalizeModelNames(lo.Map(models, func(item ollama.OllamaModel, _ int) string {
			return item.Name
		})), nil
	}

	if channel.Type == constant.ChannelTypeGemini {
		return fetchGeminiModelIDs(ctx, channel, admission)
	}

	if constant.IsAdvancedCustomChannel(channel.Type) {
		return fetchAdvancedCustomUpstreamModelIDs(ctx, channel, baseURL, admission)
	}

	if channel.Type == constant.ChannelTypeCodex {
		return service.FetchCodexChannelModels(ctx, channel, service.CodexModelDiscoveryOptions{
			Admission: admission,
			Override:  applyFetchModelsHeaderOverrides,
		})
	}

	url := channelModelListURL(channel, baseURL)
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return nil, fmt.Errorf("failed to get channel key: %w", apiErr)
	}
	key = strings.TrimSpace(key)
	headers, err := buildFetchModelsHeaders(channel, key)
	if err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	body, err := getFetchModelsResponseBody(http.MethodGet, url, channel, key, headers, ctx, admission)
	if err != nil {
		return nil, sanitizeAdvancedCustomRequestError(err, key, url)
	}
	ids, err := parseAuthoritativeOpenAIModelIDs(body)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func channelModelListURL(channel *model.Channel, baseURL string) string {
	switch channel.Type {
	case constant.ChannelTypeAli:
		return fmt.Sprintf("%s/compatible-mode/v1/models", baseURL)
	case constant.ChannelTypeZhipu_v4:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/api/paas/v4/models", baseURL)
	case constant.ChannelTypeVolcEngine:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/v1/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/api/v3/models", baseURL)
	case constant.ChannelTypeMoonshot:
		if plan, ok := constant.ChannelSpecialBases[baseURL]; ok && plan.OpenAIBaseURL != "" {
			return fmt.Sprintf("%s/models", plan.OpenAIBaseURL)
		}
		return fmt.Sprintf("%s/v1/models", baseURL)
	default:
		return fmt.Sprintf("%s/v1/models", baseURL)
	}
}

func fetchEnabledMultiKeyModelUnion(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission, options channelModelDiscoveryOptions) ([]string, error) {
	keys := channel.GetKeys()
	union := make([]string, 0)
	enabledCount := 0
	for keyIndex := range keys {
		if status, ok := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; ok && status != common.ChannelStatusEnabled {
			continue
		}
		enabledCount++
		current, lease, err := acquireMultiKeyDiscoverySnapshot(ctx, channel, keyIndex, admission, options)
		if err != nil {
			return nil, fmt.Errorf("enabled key %d model discovery failed: %w", keyIndex, err)
		}
		requestCtx := ctx
		if lease != nil {
			requestCtx = lease.Context()
		}
		var models []string
		if current.Type == constant.ChannelTypeTaskPlugin {
			plugin, ok := jsplugin.DefaultRegistry.Get(current.GetSetting().TaskPluginKey)
			if !ok {
				if lease != nil {
					lease.Release()
				}
				return nil, fmt.Errorf("task plugin is not registered")
			}
			if _, ok := plugin.Meta.ModelListRoute(); ok {
				models, err = fetchProviderPluginUpstreamModelIDs(requestCtx, current, plugin, nil, keyIndex)
			} else {
				models = normalizeModelNames(plugin.Meta.Models)
			}
		} else {
			key := current.GetKeys()[keyIndex]
			models, err = fetchChannelUpstreamModelIDsSnapshot(requestCtx, newSingleKeyChannel(current, key), nil)
		}
		if lease != nil {
			lease.Release()
		}
		if err != nil {
			return nil, fmt.Errorf("enabled key %d model discovery failed: %w", keyIndex, err)
		}
		union = append(union, models...)
	}
	if enabledCount == 0 {
		return nil, fmt.Errorf("channel has no enabled keys")
	}
	return normalizeModelNames(union), nil
}

func acquireMultiKeyDiscoverySnapshot(ctx context.Context, channel *model.Channel, keyIndex int, admission *service.ChannelDiagnosticAdmission, options channelModelDiscoveryOptions) (*model.Channel, *service.ChannelCapacityLease, error) {
	keys := channel.GetKeys()
	if keyIndex < 0 || keyIndex >= len(keys) {
		return nil, nil, fmt.Errorf("channel key index is out of range")
	}
	expectedKey := strings.TrimSpace(keys[keyIndex])
	expectedID := channel.ChannelKeyEntryID(keyIndex)
	expectedRev := int64(0)
	if keyIndex < len(channel.KeyEntryRevisions) {
		expectedRev = channel.KeyEntryRevisions[keyIndex]
	}
	if channel.Id <= 0 || admission == nil {
		return channel, nil, nil
	}
	settings := channel.GetSetting()
	limit := discoveryConcurrencyLimit(settings)
	for attempt := range 3 {
		lease, apiErr := admission.Acquire(ctx, channel.Id, settings)
		if apiErr != nil {
			return nil, nil, apiErr
		}
		fresh, err := reloadManagementChannel(lease.Context(), channel.Id)
		freshKeys := []string(nil)
		if fresh != nil {
			freshKeys = fresh.GetKeys()
		}
		expectedType := channel.Type
		if options.Preview != nil && options.SourceType > 0 {
			expectedType = options.SourceType
		}
		if err != nil || fresh == nil || fresh.Type != expectedType || !fresh.ChannelInfo.IsMultiKey || keyIndex >= len(freshKeys) || strings.TrimSpace(freshKeys[keyIndex]) != expectedKey || expectedID > 0 && (fresh.ChannelKeyEntryID(keyIndex) != expectedID || keyIndex >= len(fresh.KeyEntryRevisions) || fresh.KeyEntryRevisions[keyIndex] != expectedRev) {
			lease.Release()
			return nil, nil, fmt.Errorf("channel configuration changed during model discovery")
		}
		if status, ok := fresh.ChannelInfo.MultiKeyStatusList[keyIndex]; options.KeyIndex == nil && ok && status != common.ChannelStatusEnabled {
			lease.Release()
			return nil, nil, fmt.Errorf("channel key is no longer enabled")
		}
		fresh, err = applyChannelModelPreview(fresh, options.Preview)
		if err != nil {
			lease.Release()
			return nil, nil, err
		}
		currentSettings := fresh.GetSetting()
		currentLimit := discoveryConcurrencyLimit(currentSettings)
		if currentLimit == limit {
			return fresh, lease, nil
		}
		lease.Release()
		settings, limit = currentSettings, currentLimit
		if attempt == 2 {
			return nil, nil, fmt.Errorf("channel concurrency settings changed during admission")
		}
	}
	return nil, nil, fmt.Errorf("channel concurrency settings changed during admission")
}

func fetchAdvancedCustomUpstreamModelIDs(ctx context.Context, channel *model.Channel, baseURL string, admission *service.ChannelDiagnosticAdmission) ([]string, error) {
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return nil, fmt.Errorf("failed to get channel key: %w", apiErr)
	}
	key = strings.TrimSpace(key)

	info := &relaycommon.RelayInfo{
		RelayFormat:    types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeUnknown,
		RequestURLPath: dto.AdvancedCustomModelListPath,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelBaseUrl:       baseURL,
			ApiKey:               key,
			ChannelOtherSettings: channel.GetOtherSettings(),
		},
	}

	adaptor := &advancedcustom.Adaptor{}
	url, headers, err := adaptor.BuildModelListRequest(info)
	if err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	if err := applyFetchModelsHeaderOverrides(channel, key, headers); err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}

	body, err := getFetchModelsResponseBody(http.MethodGet, url, channel, key, headers, ctx, admission)
	if err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	return parseAuthoritativeOpenAIModelIDs(body)
}

// fetchProviderPluginUpstreamModelIDs lists the models of a channel through
// its plugin's /v1/models relay route, as on an Advanced Custom channel. A
// route with the plugin converter answers in the vendor's format and
// convertResponse turns the reply into an OpenAI model list, chaining more
// pages when the vendor pages it.
func fetchProviderPluginUpstreamModelIDs(ctx context.Context, channel *model.Channel, plugin *jsplugin.LoadedPlugin, admission *service.ChannelDiagnosticAdmission, keyIndex int) ([]string, error) {
	var key string
	var err error
	keys := channel.GetKeys()
	if keyIndex >= 0 {
		if keyIndex >= len(keys) {
			return nil, fmt.Errorf("plugin model discovery key is no longer present")
		}
		key, err = service.FreshPluginCredentialLine(ctx, plugin, channel.Id, keyIndex, keys[keyIndex])
	} else {
		key, err = service.PluginModelListKey(ctx, channel, plugin)
		keyIndex = 0
	}
	if err != nil {
		return nil, err
	}
	settings := channel.GetOtherSettings()
	settings.AdvancedCustom = plugin.Meta.ChannelRelayConfig(plugin.ChannelConfigFor(settings.PluginConfig))
	info := &relaycommon.RelayInfo{
		RelayFormat:    types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeUnknown,
		RequestURLPath: dto.AdvancedCustomModelListPath,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelId:            channel.Id,
			ChannelIsMultiKey:    channel.ChannelInfo.IsMultiKey,
			ChannelMultiKeyIndex: keyIndex,
			ChannelBaseUrl:       channel.GetBaseURL(),
			ApiKey:               key,
			ChannelSetting:       channel.GetSetting(),
			ChannelOtherSettings: settings,
		},
	}
	body, err := providerplugin.FetchModelList(ctx, plugin, info, providerplugin.ManagementClient{
		Override: func(headers http.Header, apiKey string) error {
			return applyFetchModelsHeaderOverrides(channel, apiKey, headers)
		},
		Send: func(request *http.Request) (*http.Response, error) {
			return sendChannelRequest(channel, request)
		},
	})
	if err != nil {
		return nil, sanitizeFetchModelsError(err, key)
	}
	return parseOpenAIModelIDs(body)
}

// sendChannelRequest sends a channel management request through the
// channel's proxy and TLS setting. The caller closes the response body.
func sendChannelRequest(channel *model.Channel, request *http.Request) (*http.Response, error) {
	setting := channel.GetSetting()
	return service.DoChannelManagementRequest(service.ChannelManagementCall{
		Request:   request,
		Settings:  setting,
		ChannelID: channel.Id,
		Timeout:   30 * time.Second,
	})
}

func sameChannelModelUpdateSource(expected, current *model.Channel) bool {
	if expected == nil || current == nil || expected.Type != current.Type || expected.Key != current.Key || expected.Status != current.Status ||
		channelBaseURL(expected) != channelBaseURL(current) || expected.Models != current.Models || !equalStringPointers(expected.ModelMapping, current.ModelMapping) ||
		!equalStringPointers(expected.HeaderOverride, current.HeaderOverride) || !equalStringPointers(expected.ParamOverride, current.ParamOverride) ||
		!equalStringPointers(expected.Setting, current.Setting) || expected.OtherSettings != current.OtherSettings ||
		expected.ChannelInfo.IsMultiKey != current.ChannelInfo.IsMultiKey || expected.ChannelInfo.MultiKeySize != current.ChannelInfo.MultiKeySize ||
		!slices.Equal(expected.KeyEntryIDs, current.KeyEntryIDs) ||
		!reflect.DeepEqual(expected.ChannelInfo.MultiKeyScopeModes, current.ChannelInfo.MultiKeyScopeModes) ||
		!reflect.DeepEqual(expected.ChannelInfo.MultiKeyStatusList, current.ChannelInfo.MultiKeyStatusList) ||
		!reflect.DeepEqual(expected.ChannelInfo.MultiKeyModels, current.ChannelInfo.MultiKeyModels) ||
		!reflect.DeepEqual(expected.ChannelInfo.MultiKeyUsers, current.ChannelInfo.MultiKeyUsers) {
		return false
	}
	return true
}

func equalStringPointers(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func persistChannelUpstreamModelUpdate(ctx context.Context, source *model.Channel, settings dto.ChannelOtherSettings, models string, updateModels bool) (bool, error) {
	if source == nil || source.Id <= 0 {
		return false, fmt.Errorf("saved channel is required")
	}
	changed := false
	written, err := model.UpdateChannelOtherInfoLocked(ctx, source.Id, func(current *model.Channel) (map[string]any, error) {
		if !sameChannelModelUpdateSource(source, current) {
			return nil, fmt.Errorf("channel configuration changed during model discovery")
		}
		currentSettings := current.GetOtherSettings()
		// Merge discovery-owned fields into the current settings only after the
		// input snapshot was verified unchanged under lock.
		currentSettings.UpstreamModelUpdateLastCheckTime = settings.UpstreamModelUpdateLastCheckTime
		currentSettings.UpstreamModelUpdateLastDetectedModels = settings.UpstreamModelUpdateLastDetectedModels
		currentSettings.UpstreamModelUpdateLastRemovedModels = settings.UpstreamModelUpdateLastRemovedModels
		currentSettings.UpstreamModelUpdateIgnoredModels = settings.UpstreamModelUpdateIgnoredModels
		current.SetOtherSettings(currentSettings)
		updates := map[string]any{"settings": current.OtherSettings}
		if updateModels && models != current.Models {
			updates["models"] = models
			changed = true
		}
		return updates, nil
	})
	if err != nil {
		return false, err
	}
	return written && changed, nil
}

func checkAndPersistChannelUpstreamModelUpdates(
	channel *model.Channel,
	settings *dto.ChannelOtherSettings,
	force bool,
	allowAutoApply bool,
) (modelsChanged bool, autoAdded int, err error) {
	return checkAndPersistChannelUpstreamModelUpdatesContext(context.Background(), channel, settings, force, allowAutoApply)
}

func checkAndPersistChannelUpstreamModelUpdatesContext(
	ctx context.Context,
	channel *model.Channel,
	settings *dto.ChannelOtherSettings,
	force bool,
	allowAutoApply bool,
) (modelsChanged bool, autoAdded int, err error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	if channel == nil || settings == nil {
		return false, 0, fmt.Errorf("channel is required")
	}
	if channel.Id <= 0 {
		return false, 0, fmt.Errorf("saved channel is required")
	}
	source := *channel
	now := common.GetTimestamp()
	if !force {
		minInterval := getUpstreamModelUpdateMinCheckIntervalSeconds()
		if settings.UpstreamModelUpdateLastCheckTime > 0 && now-settings.UpstreamModelUpdateLastCheckTime < minInterval {
			return false, 0, nil
		}
	}

	pendingAddModels, pendingRemoveModels, fetchErr := collectPendingUpstreamModelChanges(ctx, &source, *settings)
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	nextSettings := *settings
	nextSettings.UpstreamModelUpdateLastCheckTime = now
	persistCtx, cancelPersist := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPersist()
	if fetchErr != nil {
		_, persistErr := persistChannelUpstreamModelUpdate(persistCtx, &source, nextSettings, source.Models, false)
		if persistErr != nil {
			return false, 0, persistErr
		}
		return false, 0, fetchErr
	}

	nextModels := source.Models
	if allowAutoApply && nextSettings.UpstreamModelUpdateAutoSyncEnabled && len(pendingAddModels) > 0 {
		originModels := normalizeModelNames(source.GetModels())
		mergedModels := mergeModelNames(originModels, pendingAddModels)
		if len(mergedModels) > len(originModels) {
			nextModels = strings.Join(mergedModels, ",")
			autoAdded = len(mergedModels) - len(originModels)
			modelsChanged = true
		}
		nextSettings.UpstreamModelUpdateLastDetectedModels = []string{}
	} else {
		nextSettings.UpstreamModelUpdateLastDetectedModels = pendingAddModels
	}
	nextSettings.UpstreamModelUpdateLastRemovedModels = pendingRemoveModels

	modelsChanged, err = persistChannelUpstreamModelUpdate(persistCtx, &source, nextSettings, nextModels, modelsChanged)
	if err != nil {
		return false, 0, err
	}
	if modelsChanged {
		stored, loadErr := model.GetChannelById(source.Id, true)
		if loadErr != nil {
			return true, autoAdded, loadErr
		}
		if err = stored.UpdateAbilities(nil); err != nil {
			return true, autoAdded, err
		}
		model.BumpSyncTopic(model.SyncTopicChannels)
		refreshChannelRuntimeCache()
	}
	*settings = nextSettings
	channel.Models = nextModels
	channel.SetOtherSettings(nextSettings)
	return modelsChanged, autoAdded, nil
}

func refreshChannelRuntimeCache() {
	if common.MemoryCacheEnabled {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(common.LogText("InitChannelCache panic: %v", r))
				}
			}()
			model.InitChannelCache()
		}()
	}
}

func shouldSendUpstreamModelUpdateNotification(now int64, changedChannels int, failedChannels int) bool {
	if changedChannels <= 0 && failedChannels <= 0 {
		return true
	}

	channelUpstreamModelUpdateNotifyState.Lock()
	defer channelUpstreamModelUpdateNotifyState.Unlock()

	if channelUpstreamModelUpdateNotifyState.lastNotifiedAt > 0 &&
		now-channelUpstreamModelUpdateNotifyState.lastNotifiedAt < channelUpstreamModelUpdateNotifySuppressWindowSeconds &&
		channelUpstreamModelUpdateNotifyState.lastChangedChannels == changedChannels &&
		channelUpstreamModelUpdateNotifyState.lastFailedChannels == failedChannels {
		return false
	}

	channelUpstreamModelUpdateNotifyState.lastNotifiedAt = now
	channelUpstreamModelUpdateNotifyState.lastChangedChannels = changedChannels
	channelUpstreamModelUpdateNotifyState.lastFailedChannels = failedChannels
	return true
}

func buildUpstreamModelUpdateTaskNotificationContent(
	lang string,
	checkedChannels int,
	changedChannels int,
	detectedAddModels int,
	detectedRemoveModels int,
	autoAddedModels int,
	failedChannelIDs []int,
	channelSummaries []upstreamModelUpdateChannelSummary,
	addModelSamples []string,
	removeModelSamples []string,
) string {
	var builder strings.Builder
	failedChannels := len(failedChannelIDs)
	builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateSummary, map[string]any{
		"Checked":   checkedChannels,
		"Changed":   changedChannels,
		"Added":     detectedAddModels,
		"Removed":   detectedRemoveModels,
		"AutoAdded": autoAddedModels,
		"Failed":    failedChannels,
	}))

	if len(channelSummaries) > 0 {
		displayCount := min(len(channelSummaries), channelUpstreamModelUpdateNotifyMaxChannelDetails)
		builder.WriteString("\n\n")
		builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateChangedChannels, map[string]any{
			"Shown": displayCount,
			"Total": len(channelSummaries),
		}))
		for _, summary := range channelSummaries[:displayCount] {
			builder.WriteString(fmt.Sprintf("\n- %s (+%d / -%d)", summary.ChannelName, summary.AddCount, summary.RemoveCount))
		}
		if len(channelSummaries) > displayCount {
			builder.WriteString("\n- ")
			builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateMoreChannels, map[string]any{
				"Count": len(channelSummaries) - displayCount,
			}))
		}
	}

	normalizedAddModelSamples := normalizeModelNames(addModelSamples)
	if len(normalizedAddModelSamples) > 0 {
		displayCount := min(len(normalizedAddModelSamples), channelUpstreamModelUpdateNotifyMaxModelDetails)
		builder.WriteString("\n\n")
		builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateAddedModels, map[string]any{
			"Shown":  displayCount,
			"Total":  len(normalizedAddModelSamples),
			"Models": strings.Join(normalizedAddModelSamples[:displayCount], ", "),
		}))
		if len(normalizedAddModelSamples) > displayCount {
			builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateMoreOmitted, map[string]any{
				"Count": len(normalizedAddModelSamples) - displayCount,
			}))
		}
	}

	normalizedRemoveModelSamples := normalizeModelNames(removeModelSamples)
	if len(normalizedRemoveModelSamples) > 0 {
		displayCount := min(len(normalizedRemoveModelSamples), channelUpstreamModelUpdateNotifyMaxModelDetails)
		builder.WriteString("\n\n")
		builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateRemovedModels, map[string]any{
			"Shown":  displayCount,
			"Total":  len(normalizedRemoveModelSamples),
			"Models": strings.Join(normalizedRemoveModelSamples[:displayCount], ", "),
		}))
		if len(normalizedRemoveModelSamples) > displayCount {
			builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateMoreOmitted, map[string]any{
				"Count": len(normalizedRemoveModelSamples) - displayCount,
			}))
		}
	}

	if failedChannels > 0 {
		displayCount := min(failedChannels, channelUpstreamModelUpdateNotifyMaxFailedChannelIDs)
		displayIDs := lo.Map(failedChannelIDs[:displayCount], func(channelID int, _ int) string {
			return fmt.Sprintf("%d", channelID)
		})
		builder.WriteString("\n\n")
		builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateFailedChannels, map[string]any{
			"Shown": displayCount,
			"Total": failedChannels,
			"Ids":   strings.Join(displayIDs, ", "),
		}))
		if failedChannels > displayCount {
			builder.WriteString(i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateMoreOmitted, map[string]any{
				"Count": failedChannels - displayCount,
			}))
		}
	}
	return builder.String()
}

type upstreamModelUpdateSummary struct {
	CheckedChannels      int `json:"checked_channels"`
	ChangedChannels      int `json:"changed_channels"`
	DetectedAddModels    int `json:"detected_add_models"`
	DetectedRemoveModels int `json:"detected_remove_models"`
	FailedChannels       int `json:"failed_channels"`
	AutoAddedModels      int `json:"auto_added_models"`
}

// runChannelUpstreamModelUpdateTaskOnce runs one synchronous upstream model
// detection cycle and returns a summary for system task history. It honors ctx
// cancellation between batches so a runner that loses its lease stops promptly.
// force bypasses the per-channel minimum check interval and allowAutoApply lets
// channels with auto-sync enabled adopt detected models automatically. The
// scheduled job calls (force=false, allowAutoApply=true); the manual "detect
// all" trigger calls (force=true, allowAutoApply=false) so it always re-checks
// and only stages changes for explicit review.
func runChannelUpstreamModelUpdateTaskOnce(ctx context.Context, force bool, allowAutoApply bool, report func(processed, total int)) upstreamModelUpdateSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	checkedChannels := 0
	failedChannels := 0
	failedChannelIDs := make([]int, 0)
	changedChannels := 0
	detectedAddModels := 0
	detectedRemoveModels := 0
	autoAddedModels := 0
	channelSummaries := make([]upstreamModelUpdateChannelSummary, 0)
	addModelSamples := make([]string, 0)
	removeModelSamples := make([]string, 0)
	refreshNeeded := false

	// Count the enabled channels up front so progress can be reported as a
	// percentage; a count error is non-fatal (progress just won't show a %).
	var totalChannels int64
	if err := model.DB.WithContext(ctx).Model(&model.Channel{}).Where("status = ?", common.ChannelStatusEnabled).Count(&totalChannels).Error; err != nil {
		totalChannels = 0
	}
	processed := 0

	lastID := 0
scanLoop:
	for {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		var channels []*model.Channel
		query := model.DB.WithContext(ctx).
			Select(channelUpstreamModelUpdateSelectFields).
			Where("status = ?", common.ChannelStatusEnabled).
			Order("id asc").
			Limit(channelUpstreamModelUpdateTaskBatchSize)
		if lastID > 0 {
			query = query.Where("id > ?", lastID)
		}
		err := query.Find(&channels).Error
		if err != nil {
			common.SysLog(common.LogText("upstream model update task query failed: %v", err))
			break
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if channel == nil {
				continue
			}
			if ctx != nil && ctx.Err() != nil {
				break scanLoop
			}

			processed++
			if report != nil {
				report(processed, int(totalChannels))
			}

			settings := channel.GetOtherSettings()
			if !settings.UpstreamModelUpdateCheckEnabled {
				continue
			}

			checkedChannels++
			modelsChanged, autoAdded, err := checkAndPersistChannelUpstreamModelUpdatesContext(ctx, channel, &settings, force, allowAutoApply)
			if err != nil {
				failedChannels++
				failedChannelIDs = append(failedChannelIDs, channel.Id)
				common.SysLog(common.LogText("upstream model update check failed: channel_id=%d channel_name=%s err=%v", channel.Id, channel.Name, err))
				continue
			}
			currentAddModels := normalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
			currentRemoveModels := normalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
			currentAddCount := len(currentAddModels) + autoAdded
			currentRemoveCount := len(currentRemoveModels)
			detectedAddModels += currentAddCount
			detectedRemoveModels += currentRemoveCount
			if currentAddCount > 0 || currentRemoveCount > 0 {
				changedChannels++
				channelSummaries = append(channelSummaries, upstreamModelUpdateChannelSummary{
					ChannelName: channel.Name,
					AddCount:    currentAddCount,
					RemoveCount: currentRemoveCount,
				})
			}
			addModelSamples = mergeModelNames(addModelSamples, currentAddModels)
			removeModelSamples = mergeModelNames(removeModelSamples, currentRemoveModels)
			if modelsChanged {
				refreshNeeded = true
			}
			autoAddedModels += autoAdded

			if common.RequestInterval > 0 {
				if ctx == nil {
					time.Sleep(common.RequestInterval)
				} else {
					select {
					case <-ctx.Done():
						break scanLoop
					case <-time.After(common.RequestInterval):
					}
				}
			}
		}

		if len(channels) < channelUpstreamModelUpdateTaskBatchSize {
			break
		}
	}

	if report != nil && (ctx == nil || ctx.Err() == nil) {
		report(int(totalChannels), int(totalChannels)) // mark complete only when the full scan finished
	}

	if refreshNeeded {
		refreshChannelRuntimeCache()
	}

	summary := upstreamModelUpdateSummary{
		CheckedChannels:      checkedChannels,
		ChangedChannels:      changedChannels,
		DetectedAddModels:    detectedAddModels,
		DetectedRemoveModels: detectedRemoveModels,
		FailedChannels:       failedChannels,
		AutoAddedModels:      autoAddedModels,
	}

	if checkedChannels > 0 || common.DebugEnabled {
		common.SysLog(common.LogText(
			"upstream model update task done: checked_channels=%d changed_channels=%d detected_add_models=%d detected_remove_models=%d failed_channels=%d auto_added_models=%d",
			checkedChannels,
			changedChannels,
			detectedAddModels,
			detectedRemoveModels,
			failedChannels,
			autoAddedModels,
		))
	}
	if changedChannels > 0 || failedChannels > 0 {
		now := common.GetTimestamp()
		if !shouldSendUpstreamModelUpdateNotification(now, changedChannels, failedChannels) {
			common.SysLog(common.LogText(
				"upstream model update notification skipped in 24h window: changed_channels=%d failed_channels=%d",
				changedChannels,
				failedChannels,
			))
			return summary
		}
		service.NotifyUpstreamModelUpdateWatchers(func(lang string) (string, string) {
			return i18n.Translate(lang, i18n.MsgChannelUpstreamUpdateNotifySubject),
				buildUpstreamModelUpdateTaskNotificationContent(
					lang,
					checkedChannels,
					changedChannels,
					detectedAddModels,
					detectedRemoveModels,
					autoAddedModels,
					failedChannelIDs,
					channelSummaries,
					addModelSamples,
					removeModelSamples,
				)
		})
	}
	return summary
}

func ApplyChannelUpstreamModelUpdates(c *gin.Context) {
	var req applyChannelUpstreamModelUpdatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.ID <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(req.ID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	beforeSettings := channel.GetOtherSettings()
	ignoredModels := intersectModelNames(req.IgnoreModels, beforeSettings.UpstreamModelUpdateLastDetectedModels)

	addedModels, removedModels, remainingModels, remainingRemoveModels, modelsChanged, err := applyChannelUpstreamModelUpdates(
		c.Request.Context(),
		channel,
		req.AddModels,
		req.IgnoreModels,
		req.RemoveModels,
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	if modelsChanged {
		refreshChannelRuntimeCache()
	}

	recordManageAudit(c, "channel.upstream_apply", map[string]any{
		"id": channel.Id,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":                      channel.Id,
			"added_models":            addedModels,
			"removed_models":          removedModels,
			"ignored_models":          ignoredModels,
			"remaining_models":        remainingModels,
			"remaining_remove_models": remainingRemoveModels,
			"models":                  channel.Models,
			"settings":                channel.OtherSettings,
		},
	})
}

func DetectChannelUpstreamModelUpdates(c *gin.Context) {
	var req applyChannelUpstreamModelUpdatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.ID <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(req.ID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	settings := channel.GetOtherSettings()
	modelsChanged, autoAdded, err := checkAndPersistChannelUpstreamModelUpdatesContext(c.Request.Context(), channel, &settings, true, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if modelsChanged {
		refreshChannelRuntimeCache()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": detectChannelUpstreamModelUpdatesResult{
			ChannelID:       channel.Id,
			ChannelName:     channel.Name,
			AddModels:       normalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels),
			RemoveModels:    normalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels),
			LastCheckTime:   settings.UpstreamModelUpdateLastCheckTime,
			AutoAddedModels: autoAdded,
		},
	})
}

func applyChannelUpstreamModelUpdates(
	ctx context.Context,
	channel *model.Channel,
	addModelsInput []string,
	ignoreModelsInput []string,
	removeModelsInput []string,
) (
	addedModels []string,
	removedModels []string,
	remainingModels []string,
	remainingRemoveModels []string,
	modelsChanged bool,
	err error,
) {
	settings := channel.GetOtherSettings()
	pendingAddModels := normalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels)
	pendingRemoveModels := normalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
	addModels := intersectModelNames(addModelsInput, pendingAddModels)
	ignoreModels := intersectModelNames(ignoreModelsInput, pendingAddModels)
	removeModels := intersectModelNames(removeModelsInput, pendingRemoveModels)
	removeModels = subtractModelNames(removeModels, addModels)

	originModels := normalizeModelNames(channel.GetModels())
	nextModels := applySelectedModelChanges(originModels, addModels, removeModels)
	modelsChanged = !slices.Equal(originModels, nextModels)
	nextModelList := channel.Models
	if modelsChanged {
		nextModelList = strings.Join(nextModels, ",")
	}

	settings.UpstreamModelUpdateIgnoredModels = mergeModelNames(settings.UpstreamModelUpdateIgnoredModels, ignoreModels)
	if len(addModels) > 0 {
		settings.UpstreamModelUpdateIgnoredModels = subtractModelNames(settings.UpstreamModelUpdateIgnoredModels, addModels)
	}
	remainingModels = subtractModelNames(pendingAddModels, append(addModels, ignoreModels...))
	remainingRemoveModels = subtractModelNames(pendingRemoveModels, removeModels)
	settings.UpstreamModelUpdateLastDetectedModels = remainingModels
	settings.UpstreamModelUpdateLastRemovedModels = remainingRemoveModels
	settings.UpstreamModelUpdateLastCheckTime = common.GetTimestamp()

	persistCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := persistChannelUpstreamModelUpdate(persistCtx, channel, settings, nextModelList, modelsChanged); err != nil {
		return nil, nil, nil, nil, false, err
	}
	stored, err := reloadManagementChannel(persistCtx, channel.Id)
	if err != nil {
		return addModels, removeModels, remainingModels, remainingRemoveModels, modelsChanged, err
	}
	*channel = *stored

	if modelsChanged {
		err := channel.UpdateAbilities(nil)
		model.BumpSyncTopic(model.SyncTopicChannels)
		if err != nil {
			return addModels, removeModels, remainingModels, remainingRemoveModels, true, err
		}
	}
	return addModels, removeModels, remainingModels, remainingRemoveModels, modelsChanged, nil
}

func collectPendingApplyUpstreamModelChanges(settings dto.ChannelOtherSettings) (pendingAddModels []string, pendingRemoveModels []string) {
	return normalizeModelNames(settings.UpstreamModelUpdateLastDetectedModels), normalizeModelNames(settings.UpstreamModelUpdateLastRemovedModels)
}

func findEnabledChannelsAfterID(lastID int, batchSize int) ([]*model.Channel, error) {
	var channels []*model.Channel
	query := model.DB.
		Select(channelUpstreamModelUpdateSelectFields).
		Where("status = ?", common.ChannelStatusEnabled).
		Order("id asc").
		Limit(batchSize)
	if lastID > 0 {
		query = query.Where("id > ?", lastID)
	}
	return channels, query.Find(&channels).Error
}

func ApplyAllChannelUpstreamModelUpdates(c *gin.Context) {
	results := make([]applyAllChannelUpstreamModelUpdatesResult, 0)
	failed := make([]int, 0)
	refreshNeeded := false
	addedModelCount := 0
	removedModelCount := 0

	lastID := 0
	for {
		channels, err := findEnabledChannelsAfterID(lastID, channelUpstreamModelUpdateTaskBatchSize)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if len(channels) == 0 {
			break
		}
		lastID = channels[len(channels)-1].Id

		for _, channel := range channels {
			if channel == nil {
				continue
			}

			settings := channel.GetOtherSettings()
			if !settings.UpstreamModelUpdateCheckEnabled {
				continue
			}

			pendingAddModels, pendingRemoveModels := collectPendingApplyUpstreamModelChanges(settings)
			if len(pendingAddModels) == 0 && len(pendingRemoveModels) == 0 {
				continue
			}

			addedModels, removedModels, remainingModels, remainingRemoveModels, modelsChanged, err := applyChannelUpstreamModelUpdates(
				c.Request.Context(),
				channel,
				pendingAddModels,
				nil,
				pendingRemoveModels,
			)
			if err != nil {
				failed = append(failed, channel.Id)
				continue
			}
			if modelsChanged {
				refreshNeeded = true
			}
			addedModelCount += len(addedModels)
			removedModelCount += len(removedModels)
			results = append(results, applyAllChannelUpstreamModelUpdatesResult{
				ChannelID:             channel.Id,
				ChannelName:           channel.Name,
				AddedModels:           addedModels,
				RemovedModels:         removedModels,
				RemainingModels:       remainingModels,
				RemainingRemoveModels: remainingRemoveModels,
			})
		}

		if len(channels) < channelUpstreamModelUpdateTaskBatchSize {
			break
		}
	}

	if refreshNeeded {
		refreshChannelRuntimeCache()
	}

	recordManageAudit(c, "channel.upstream_apply_all", map[string]any{
		"count": len(results),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"processed_channels": len(results),
			"added_models":       addedModelCount,
			"removed_models":     removedModelCount,
			"failed_channel_ids": failed,
			"results":            results,
		},
	})
}

// DetectAllChannelUpstreamModelUpdates enqueues a model_update system task
// (manual variant) instead of scanning inline. Routing the manual trigger
// through the framework gives it the same cross-instance lease dedup and run
// history as the scheduled scan. If any model_update task is already active, the
// manual run is rejected so the caller does not mistake a scheduled run for this
// manual one.
func DetectAllChannelUpstreamModelUpdates(c *gin.Context) {
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeModelUpdate, modelUpdateTaskPayload{Manual: true})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		msg := common.NewMessage("A model update task is already running or queued. Cannot start this manual task")
		common.ApiErrorStatus(c, http.StatusConflict, msg, gin.H{"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
			"type":    task.Type,
		}})
		return
	}

	recordManageAudit(c, "channel.upstream_detect_all", map[string]any{
		"task_id": task.TaskID,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
		},
	})
}
