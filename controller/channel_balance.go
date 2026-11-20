package controller

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/providerplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
)

// balanceQueryTimeout bounds the balance query of one key in an update of all
// balances, and of one plugin key line anywhere.
const balanceQueryTimeout = 30 * time.Second

// balanceUpdateConcurrency is how many channels an update of all balances
// queries at once.
const balanceUpdateConcurrency = 4

// maxBalanceErrorRunes bounds the failure message a balance detail keeps.
const maxBalanceErrorRunes = 512

func usdBalance(result jsplugin.BalanceResult) (float64, bool) {
	return service.PluginBalanceUSD(result)
}

// emptyBalance reports a definite balance that is used up: a finite amount of
// 0 or less, not unlimited.
func emptyBalance(result jsplugin.BalanceResult) bool {
	return result.Amount != nil && !result.Unlimited && !math.IsNaN(*result.Amount) && !math.IsInf(*result.Amount, 0) && *result.Amount <= 0
}

// recordBuiltinBalance stores a built-in balance and its detail together,
// after validating the result's source: the amount in USD, or the failure
// once the channel has a detail. A channel type without a balance query thus
// keeps no detail.
func recordBuiltinBalance(ctx context.Context, channel *model.Channel, result channelBalanceResult, err error) (*model.ChannelBalanceDetail, error) {
	if channel.Id <= 0 || err == nil && result.RawResponse != "" {
		return nil, nil
	}
	return model.UpdateChannelBalanceDetailChecked(ctx, channel.Id, func(current *model.Channel, stored *model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64, error) {
		if !service.ChannelBalanceSourceMatches(current, channel) {
			return nil, nil, service.ErrChannelBalanceSourceChanged
		}
		if err == nil {
			balance := result.Balance
			return &model.ChannelBalanceDetail{BalanceResult: jsplugin.BalanceResult{Amount: &balance, Currency: "USD"}}, &balance, nil
		}
		if stored == nil {
			return nil, nil, nil
		}
		stored.Error = balanceErrorText(err, channel.Key)
		return stored, nil, nil
	})
}

// pluginKeyBalance is the balance query of one key line of a task plugin
// channel.
type pluginKeyBalance struct {
	index  int
	key    string
	result jsplugin.BalanceResult
	err    error
	source *model.Channel
}

// pluginReportsBalance reports whether a plugin exports the balance hooks.
func pluginReportsBalance(plugin *jsplugin.LoadedPlugin) bool {
	return plugin.HasProviderHook("buildBalanceRequest") && plugin.HasProviderHook("parseBalance")
}

// updatePluginChannelBalance queries the balance of a task plugin channel with
// its plugin's balance hooks, each key line of a multi-key channel on its own,
// and stores the result: the detail, and the USD balance when the result has
// one. enabledOnly skips the disabled keys of a multi-key channel. err is the
// failure when every queried key failed.
func updatePluginChannelBalance(channel *model.Channel, plugin *jsplugin.LoadedPlugin, enabledOnly bool) ([]pluginKeyBalance, *model.ChannelBalanceDetail, error) {
	return updatePluginChannelBalanceContext(context.Background(), channel, plugin, enabledOnly)
}

func updatePluginChannelBalanceContext(ctx context.Context, channel *model.Channel, plugin *jsplugin.LoadedPlugin, enabledOnly bool) ([]pluginKeyBalance, *model.ChannelBalanceDetail, error) {
	keys := []string{strings.TrimSpace(channel.Key)}
	if channel.ChannelInfo.IsMultiKey {
		keys = channel.GetKeys()
	}
	balances := make([]pluginKeyBalance, 0, len(keys))
	var source *model.Channel
	for index, key := range keys {
		if err := ctx.Err(); err != nil {
			return balances, nil, err
		}
		if enabledOnly && channel.ChannelInfo.IsMultiKey {
			if status, ok := channel.ChannelInfo.MultiKeyStatusList[index]; ok && status != common.ChannelStatusEnabled {
				continue
			}
		}
		result, queriedSource, err := queryPluginKeyBalanceWithPolicy(ctx, channel, plugin, index, key, enabledOnly)
		if err != nil {
			err = fmt.Errorf("%s", balanceErrorText(err, key))
		}
		if queriedSource != nil {
			queriedKeys := []string{strings.TrimSpace(queriedSource.Key)}
			if queriedSource.ChannelInfo.IsMultiKey {
				queriedKeys = queriedSource.GetKeys()
			}
			if index >= 0 && index < len(queriedKeys) {
				key = queriedKeys[index]
			}
		}
		balances = append(balances, pluginKeyBalance{index: index, key: key, result: result, err: err, source: queriedSource})
		if queriedSource == nil {
			return balances, nil, err
		}
		if source != nil && !service.ChannelBalanceSourceMatches(source, queriedSource) {
			if err != nil || !service.ChannelBalanceCredentialRefreshMatches(queriedSource, source, index, key) {
				return balances, nil, service.ErrChannelBalanceSourceChanged
			}
			// Only this successfully dispatched credential line evolved. Earlier
			// samples still refer to unchanged lines at the same indexes; advance
			// their source together so the batch persists against one exact key set.
			for i := range balances {
				balances[i].source = queriedSource
			}
		}
		source = queriedSource
		channel = queriedSource
	}
	var failure error
	succeeded := false
	for _, balance := range balances {
		if balance.err == nil {
			succeeded = true
		} else if failure == nil {
			failure = balance.err
		}
	}
	if len(balances) == 0 {
		return nil, nil, nil
	}
	if source == nil {
		return balances, nil, failure
	}
	records := make(map[int]model.ChannelBalanceDetail, len(balances))
	for _, balance := range balances {
		// Policy rejections made no upstream request. Never persist them at
		// an obsolete index, or mix results from different credential sources.
		if !service.ChannelBalanceSourceMatches(source, balance.source) {
			continue
		}
		record := model.ChannelBalanceDetail{BalanceResult: balance.result}
		if balance.err != nil {
			record.Error = balance.err.Error()
		}
		records[balance.index] = record
	}
	detail, err := service.UpdatePluginKeyBalancesContext(ctx, source, records)
	if err != nil {
		common.SysError(common.LogText("failed to store balance detail: channel_id=%d, error=%v", channel.Id, err))
		return balances, nil, err
	}
	if !succeeded {
		return balances, detail, failure
	}
	return balances, detail, nil
}

// queryPluginKeyBalance queries the balance of one key line of a task plugin
// channel through the channel's proxy, TLS setting and header overrides.
func queryPluginKeyBalance(ctx context.Context, channel *model.Channel, plugin *jsplugin.LoadedPlugin, index int, key string) (jsplugin.BalanceResult, error) {
	result, _, err := queryPluginKeyBalanceWithPolicy(ctx, channel, plugin, index, key, false)
	return result, err
}

func queryPluginKeyBalanceWithPolicy(ctx context.Context, channel *model.Channel, plugin *jsplugin.LoadedPlugin, index int, key string, enabledOnly bool) (jsplugin.BalanceResult, *model.Channel, error) {
	ctx, cancel := context.WithTimeout(ctx, balanceQueryTimeout)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	current, lease, err := acquireBalanceChannel(ctx, channel, admission)
	if err != nil {
		return jsplugin.BalanceResult{}, nil, err
	}
	defer lease.Release()
	if enabledOnly && current.Status != common.ChannelStatusEnabled {
		return jsplugin.BalanceResult{}, nil, fmt.Errorf("channel is no longer enabled")
	}
	currentPlugin, ok := service.PluginOfChannel(current)
	if !ok || currentPlugin.Meta.Key != plugin.Meta.Key || !pluginReportsBalance(currentPlugin) {
		return jsplugin.BalanceResult{}, nil, fmt.Errorf("channel balance plugin changed during admission")
	}
	keys := []string{strings.TrimSpace(current.Key)}
	if current.ChannelInfo.IsMultiKey {
		keys = current.GetKeys()
	}
	if index < 0 || index >= len(keys) || keys[index] != key {
		return jsplugin.BalanceResult{}, nil, fmt.Errorf("channel balance key changed during admission")
	}
	if enabledOnly && current.ChannelInfo.IsMultiKey {
		if status, ok := current.ChannelInfo.MultiKeyStatusList[index]; ok && status != common.ChannelStatusEnabled {
			return jsplugin.BalanceResult{}, nil, fmt.Errorf("channel balance key is no longer enabled")
		}
	}
	ctx = lease.Context()
	channel, plugin = current, currentPlugin
	info := &relaycommon.RelayInfo{
		RelayFormat:    types.RelayFormatOpenAI,
		RelayMode:      relayconstant.RelayModeUnknown,
		RequestURLPath: dto.AdvancedCustomBalancePath,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channel.Type,
			ChannelId:            channel.Id,
			ChannelIsMultiKey:    channel.ChannelInfo.IsMultiKey,
			ChannelMultiKeyIndex: index,
			ChannelBaseUrl:       channel.GetBaseURL(),
			ApiKey:               key,
			ChannelSetting:       channel.GetSetting(),
			ChannelOtherSettings: channel.GetOtherSettings(),
		},
	}
	dispatchedKey, successfulResponse := "", false
	result, err := providerplugin.QueryBalance(ctx, plugin, info, providerplugin.ManagementClient{
		Override: func(headers http.Header, apiKey string) error {
			return applyFetchModelsHeaderOverrides(channel, apiKey, headers)
		},
		Send: func(request *http.Request) (*http.Response, error) {
			dispatchedKey = info.ApiKey
			response, err := service.DoChannelManagementRequest(service.ChannelManagementCall{
				Request:   request,
				Settings:  channel.GetSetting(),
				ChannelID: channel.Id,
			})
			successfulResponse = err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300
			return response, err
		},
	})
	if err != nil && info.ApiKey != key {
		// A retry's parser can echo the renewed credential. The caller only
		// knows the original line, so redact the dispatched line here too.
		err = fmt.Errorf("%s", balanceErrorText(err, info.ApiKey))
	}
	if leaseErr := lease.Err(); leaseErr != nil {
		err = leaseErr
	} else if err == nil {
		err = ctx.Err()
	}
	if info.ApiKey != key {
		// renewManagement can also return a credential another caller stored.
		// Accept it only after a successful request actually used that exact
		// line, and only while the same indexed account and other policy remain.
		if err != nil {
			return result, nil, err
		}
		if !successfulResponse || dispatchedKey != info.ApiKey {
			return result, nil, service.ErrChannelBalanceSourceChanged
		}
		var refreshed model.Channel
		if err := model.DB.WithContext(ctx).First(&refreshed, "id = ?", current.Id).Error; err != nil {
			return result, nil, err
		}
		if !service.ChannelBalanceCredentialRefreshMatches(&refreshed, current, index, info.ApiKey) {
			return result, nil, service.ErrChannelBalanceSourceChanged
		}
		current = &refreshed
	}
	if err == nil {
		lease.MarkUpstreamAvailable()
	}
	return result, current, err
}

// balanceErrorText is the failure message a balance detail keeps: without
// the request URL, the key line, and a credential line's header and query
// values, and bounded in length.
func balanceErrorText(err error, key string) string {
	message := sanitizeFetchModelsError(err, key).Error()
	if credential, ok := jsplugin.ParseCredential(key); ok {
		secrets := make([]string, 0, len(credential.Headers)+len(credential.Query))
		for _, value := range credential.Headers {
			secrets = append(secrets, value)
		}
		for _, value := range credential.Query {
			secrets = append(secrets, value)
		}
		for _, secret := range secrets {
			if strings.TrimSpace(secret) == "" {
				continue
			}
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
			message = strings.ReplaceAll(message, url.QueryEscape(secret), "[REDACTED]")
		}
	}
	if runes := []rune(message); len(runes) > maxBalanceErrorRunes {
		message = string(runes[:maxBalanceErrorRunes]) + "..."
	}
	return message
}

// updateBalanceInRun queries one enabled channel in an update of all
// balances and disables what has none left, when the channel's automatic
// disabling is on: the channel, or a multi-key plugin channel's empty keys.
func updateBalanceInRun(channel *model.Channel) {
	updateBalanceInRunContext(context.Background(), channel)
}

func updateBalanceInRunContext(ctx context.Context, channel *model.Channel) {
	if ctx.Err() != nil {
		return
	}
	if channel.Type == constant.ChannelTypeTaskPlugin {
		plugin, ok := service.PluginOfChannel(channel)
		if !ok || !pluginReportsBalance(plugin) {
			return
		}
		balances, _, _ := updatePluginChannelBalanceContext(ctx, channel, plugin, true)
		if ctx.Err() != nil {
			return
		}
		for _, balance := range balances {
			if balance.err != nil || !emptyBalance(balance.result) {
				continue
			}
			source := balance.source
			if source == nil {
				continue
			}
			usingKey := ""
			if source.ChannelInfo.IsMultiKey {
				usingKey = balance.key
			}
			service.DisableChannelChecked(ctx, *types.NewChannelError(source.Id, source.Type, source.Name, source.ChannelInfo.IsMultiKey, usingKey, true), "Insufficient balance", func(current *model.Channel) bool {
				if current.Status != common.ChannelStatusEnabled || !service.ChannelBalanceSourceMatches(current, balance.source) {
					return false
				}
				status, recorded := current.ChannelInfo.MultiKeyStatusList[balance.index]
				return !current.ChannelInfo.IsMultiKey || !recorded || status == common.ChannelStatusEnabled
			})
		}
		return
	}
	if channel.ChannelInfo.IsMultiKey {
		return // built-in multi-key channels have no balance query
	}
	result, err := updateBuiltinChannelBalance(ctx, channel, true)
	if err == nil && ctx.Err() == nil && result.RawResponse == "" && emptyBalance(jsplugin.BalanceResult{Amount: &result.Balance}) {
		source := result.source
		service.DisableChannelChecked(ctx, *types.NewChannelError(source.Id, source.Type, source.Name, source.ChannelInfo.IsMultiKey, "", true), "Insufficient balance", func(current *model.Channel) bool {
			return current.Status == common.ChannelStatusEnabled && service.ChannelBalanceSourceMatches(current, result.source)
		})
	}
}
