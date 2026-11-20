package service

import (
	"context"
	"errors"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

var ErrChannelBalanceSourceChanged = errors.New("channel balance source changed during the query")

// ChannelBalanceSourceMatches compares credential and provider identities in
// Go, rather than relying on a database's possibly case-insensitive collation.
// Callers apply this to the locked current row before persisting or disabling.
func ChannelBalanceSourceMatches(current, source *model.Channel) bool {
	if current == nil || source == nil || current.Id != source.Id || current.Type != source.Type ||
		current.Key != source.Key || current.ChannelInfo.IsMultiKey != source.ChannelInfo.IsMultiKey ||
		!reflect.DeepEqual(current.ChannelInfo.MultiKeyModels, source.ChannelInfo.MultiKeyModels) ||
		!maps.Equal(current.ChannelInfo.MultiKeyUsers, source.ChannelInfo.MultiKeyUsers) {
		return false
	}
	currentURL, sourceURL := current.GetBaseURL(), source.GetBaseURL()
	if currentURL == "" {
		currentURL = constant.GetChannelBaseURL(current.Type)
	}
	if sourceURL == "" {
		sourceURL = constant.GetChannelBaseURL(source.Type)
	}
	if currentURL != sourceURL {
		return false
	}
	currentHeaders, sourceHeaders := "", ""
	if current.HeaderOverride != nil {
		currentHeaders = *current.HeaderOverride
	}
	if source.HeaderOverride != nil {
		sourceHeaders = *source.HeaderOverride
	}
	if currentHeaders != sourceHeaders {
		return false
	}
	// Compare only balance route/config identity, not unrelated settings such
	// as AutoBan or concurrency that must still take their current values.
	// Decode directly: this check also runs inside short persistence callbacks,
	// where the repairing model getters must not issue nested writes.
	var currentOther, sourceOther dto.ChannelOtherSettings
	if current.Type == constant.ChannelTypeAdvancedCustom || current.Type == constant.ChannelTypeTaskPlugin {
		if current.OtherSettings != "" && common.UnmarshalJsonStr(current.OtherSettings, &currentOther) != nil ||
			source.OtherSettings != "" && common.UnmarshalJsonStr(source.OtherSettings, &sourceOther) != nil {
			return false
		}
	}
	if current.Type == constant.ChannelTypeAdvancedCustom {
		currentRoute, currentOK := currentOther.AdvancedCustom.BalanceRoute()
		sourceRoute, sourceOK := sourceOther.AdvancedCustom.BalanceRoute()
		if currentOK != sourceOK || !reflect.DeepEqual(currentRoute, sourceRoute) {
			return false
		}
	}
	if current.Type != constant.ChannelTypeTaskPlugin {
		return true
	}
	var currentSettings, sourceSettings dto.ChannelSettings
	if current.Setting == nil || source.Setting == nil ||
		common.UnmarshalJsonStr(*current.Setting, &currentSettings) != nil ||
		common.UnmarshalJsonStr(*source.Setting, &sourceSettings) != nil {
		return false
	}
	return currentSettings.TaskPluginKey == sourceSettings.TaskPluginKey &&
		reflect.DeepEqual(currentOther.PluginConfig, sourceOther.PluginConfig)
}

// ChannelBalanceCredentialRefreshMatches accepts only one same-account line
// change that was actually used by a successful balance request. It never
// weakens the exact source check at persistence: every other line, index,
// binding, scope and balance auth/config must still match the original source.
func ChannelBalanceCredentialRefreshMatches(current, source *model.Channel, index int, dispatchedKey string) bool {
	if current == nil || source == nil || source.Type != constant.ChannelTypeTaskPlugin {
		return false
	}
	previous := *source
	previous.Keys = nil
	lines := []string{strings.TrimSpace(previous.Key)}
	if previous.ChannelInfo.IsMultiKey {
		lines = slices.Clone(previous.GetKeys())
	}
	if index < 0 || index >= len(lines) {
		return false
	}
	before, beforeOK := jsplugin.ParseCredential(lines[index])
	after, afterOK := jsplugin.ParseCredential(dispatchedKey)
	if !beforeOK || !afterOK || before.Plugin != after.Plugin || before.AccountID == "" ||
		before.AccountID != after.AccountID || before.NeedsLogin || after.NeedsLogin {
		return false
	}
	lines[index] = dispatchedKey
	previous.Key = strings.Join(lines, "\n")
	previous.Keys = nil
	return ChannelBalanceSourceMatches(current, &previous)
}

// PluginBalanceUSD is a balance result in USD for the balance column: USD as it is,
// CNY at the exchange-rate setting. ok is false for another currency, an
// unlimited result or one without an amount.
func PluginBalanceUSD(result jsplugin.BalanceResult) (balance float64, ok bool) {
	if result.Amount == nil || result.Unlimited {
		return 0, false
	}
	switch result.Currency {
	case "USD":
		return *result.Amount, true
	case "CNY":
		rate := operation_setting.USDExchangeRate
		if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return 0, false
		}
		return *result.Amount / rate, true
	}
	return 0, false
}

// UpdatePluginKeyBalances merges results into the channel's per-key balance
// records and updates the USD balance when the results have one.
func UpdatePluginKeyBalances(channel *model.Channel, balances map[int]model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, error) {
	return UpdatePluginKeyBalancesContext(context.Background(), channel, balances)
}

func UpdatePluginKeyBalancesContext(ctx context.Context, channel *model.Channel, balances map[int]model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, error) {
	return model.UpdateChannelBalanceDetailChecked(ctx, channel.Id, func(current *model.Channel, stored *model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64, error) {
		if !ChannelBalanceSourceMatches(current, channel) {
			return nil, nil, ErrChannelBalanceSourceChanged
		}
		if stored == nil {
			stored = &model.ChannelBalanceDetail{}
		}
		succeeded, failure := false, ""
		for _, index := range slices.Sorted(maps.Keys(balances)) {
			result := balances[index]
			if result.Error == "" {
				succeeded = true
			} else if failure == "" {
				failure = result.Error
			}
		}
		if !current.ChannelInfo.IsMultiKey {
			result, ok := balances[0]
			if !ok {
				return nil, nil, nil
			}
			if result.Error != "" {
				stored.Error = result.Error
				return stored, nil, nil
			}
			stored = &result
		} else {
			keyCount := len(current.GetKeys())
			keys := make(map[string]model.ChannelBalanceDetail, keyCount)
			for index, detail := range stored.Keys {
				if position, err := strconv.Atoi(index); err == nil && position >= 0 && position < keyCount {
					keys[index] = detail
				}
			}
			for index, result := range balances {
				if index < 0 || index >= keyCount {
					continue
				}
				key := strconv.Itoa(index)
				if result.Error != "" {
					detail := keys[key]
					if detail.Windows == nil {
						detail.Windows = []jsplugin.BalanceWindow{}
					}
					detail.Error = result.Error
					keys[key] = detail
				} else {
					keys[key] = result
				}
			}
			stored.Keys = keys
			stored.BalanceResult = sumPluginKeyBalances(keys)
			stored.Error = ""
			if !succeeded {
				stored.Error = failure
			}
		}
		if balance, ok := PluginBalanceUSD(stored.BalanceResult); ok && succeeded {
			return stored, &balance, nil
		}
		return stored, nil, nil
	})
}

// sumPluginKeyBalances is a multi-key channel's own result from its keys': the sum
// of their amounts when they share one currency, unlimited when every key
// with a result is unlimited.
func sumPluginKeyBalances(keys map[string]model.ChannelBalanceDetail) jsplugin.BalanceResult {
	result := jsplugin.BalanceResult{Windows: []jsplugin.BalanceWindow{}}
	var sum float64
	counted, results, unlimited := 0, 0, 0
	for _, key := range keys {
		if key.Amount == nil && !key.Unlimited && len(key.Windows) == 0 {
			continue
		}
		results++
		if key.Unlimited {
			unlimited++
			continue
		}
		if key.Amount == nil {
			continue
		}
		if counted > 0 && key.Currency != result.Currency {
			return jsplugin.BalanceResult{Windows: []jsplugin.BalanceWindow{}}
		}
		result.Currency = key.Currency
		sum += *key.Amount
		counted++
	}
	if results > 0 && unlimited == results {
		result.Unlimited = true
		return result
	}
	if counted > 0 {
		result.Amount = &sum
	}
	return result
}
