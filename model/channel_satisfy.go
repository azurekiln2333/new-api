package model

import (
	"context"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ChannelModelOption exposes routing choices without credentials or settings.
type ChannelModelOption struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// ListAvailableChannelModels reads current allocations and budgets in bounded
// batches. Unlike request admission's conservative bool predicates, a listing
// must return storage failures rather than report a successful empty catalog.
// The relay still revalidates every choice before forwarding.
func ListAvailableChannelModels(ctx context.Context, groups []string, userID int, filters []dto.ChannelFilter) ([]ChannelModelOption, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := make([]ChannelModelOption, 0)
	if userID <= 0 {
		return options, nil
	}
	active, err := channelBudgetActivation(ctx)
	if err != nil {
		return nil, err
	}
	scope := DB.Session(&gorm.Session{Logger: logger.Discard})
	projectionContext := WithBatchedChannelKeyEntries(ctx)
	beforeID := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		query := scope.WithContext(projectionContext).Where("status = ?", common.ChannelStatusEnabled)
		if beforeID > 0 {
			query = query.Where("id < ?", beforeID)
		}
		var channels []*Channel
		if err := query.Order("id DESC").Limit(channelKeyEntryProjectionChunk).Find(&channels).Error; err != nil {
			return nil, err
		}
		if len(channels) == 0 {
			return options, ctx.Err()
		}
		if err := ProjectChannelKeyEntries(scope, projectionContext, channels, false); err != nil {
			return nil, err
		}
		channelIDs := make([]int64, 0, len(channels))
		allocationIDs := make([]int64, 0)
		for _, channel := range channels {
			channelIDs = append(channelIDs, int64(channel.Id))
			allocationIDs = append(allocationIDs, channel.KeyEntryIDs...)
		}
		channelExhausted := make(map[int64]bool)
		keyExhausted := make(map[int64]bool)
		if active {
			for _, budget := range []struct {
				scope     ChannelBudgetScopeType
				ids       []int64
				exhausted map[int64]bool
			}{
				{BudgetScopeChannel, channelIDs, channelExhausted},
				{BudgetScopeKey, allocationIDs, keyExhausted},
			} {
				for chunk := range slices.Chunk(budget.ids, channelKeyEntryProjectionChunk) {
					var accounts []ChannelBudgetAccount
					if err := scope.WithContext(ctx).Where("scope_type = ? AND scope_id IN ?", budget.scope, chunk).Find(&accounts).Error; err != nil {
						return nil, err
					}
					for i := range accounts {
						_, exhausted := budgetCapacity(&accounts[i])
						budget.exhausted[accounts[i].ScopeId] = exhausted
					}
				}
			}
		}
		for _, channel := range channels {
			if channelExhausted[int64(channel.Id)] {
				continue
			}
			publicModels := make(map[string]bool)
			models := channel.GetModels()
			channelGroups := channel.GetGroups()
			groupModels := channel.GetOtherSettings().GroupModels
			keys := allocationKeyList(channel)
			for _, group := range groups {
				if !slices.Contains(channelGroups, group) {
					continue
				}
				for _, name := range filterGroupModels(models, groupModels[group]) {
					publicModels[name] = true
				}
			}
			available := make([]string, 0, len(models))
			for _, name := range models {
				if slices.Contains(available, name) {
					continue
				}
				matches := true
				for _, filter := range filters {
					if !channelMatchesFilter(channel, name, filter) {
						matches = false
						break
					}
				}
				if !matches {
					continue
				}
				for i := range keys {
					id := channel.ChannelKeyEntryID(i)
					if active && (id <= 0 || keyExhausted[id]) {
						continue
					}
					upstream, ok := channel.RoutingUpstreamModelNameForKey(i, name)
					if !ok || !channel.keyEnabled(i) || !channel.KeyServesModel(i, name) || channel.keyModelUnavailable(i, upstream) {
						continue
					}
					owner := channel.ChannelInfo.MultiKeyUsers[i]
					if owner == userID && userID > 0 || owner == 0 && publicModels[name] {
						available = append(available, name)
						break
					}
				}
			}
			if len(available) > 0 {
				slices.Sort(available)
				options = append(options, ChannelModelOption{ID: channel.Id, Name: channel.Name, Models: available})
			}
		}
		beforeID = channels[len(channels)-1].Id
		if len(channels) < channelKeyEntryProjectionChunk {
			return options, ctx.Err()
		}
	}
}

// EligibleModelsForUserGroup includes enabled public keys within the group's
// model scope and keys bound to this user, which also work outside the group.
// It never rotates a key or changes the polling cursor.
func (channel *Channel) EligibleModelsForUserGroup(group string, userID int) []string {
	if channel.Status != common.ChannelStatusEnabled || userID <= 0 {
		return nil
	}
	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()
	models := channel.GetModels()
	var allowed map[int64]bool
	if ChannelBudgetsEnabled() {
		var err error
		allowed, err = ChannelBudgetAllows(context.Background(), channel.Id, channel.KeyEntryIDs)
		if err != nil {
			return nil
		}
	}
	var public []string
	if slices.Contains(channel.GetGroups(), group) {
		if !channel.ChannelInfo.IsMultiKey {
			if allowed == nil || allowed[channel.ChannelKeyEntryID(0)] {
				public = models
			}
		} else {
			indexes := make([]int, 0)
			for i := range channel.GetKeys() {
				if allowed != nil && !allowed[channel.ChannelKeyEntryID(i)] {
					continue
				}
				if channel.ChannelInfo.MultiKeyUsers[i] != 0 {
					continue
				}
				if status, ok := channel.ChannelInfo.MultiKeyStatusList[i]; ok && status != common.ChannelStatusEnabled {
					continue
				}
				indexes = append(indexes, i)
			}
			public = channel.modelsServedByKeys(indexes, models)
		}
		public = filterGroupModels(public, channel.GetOtherSettings().GroupModels[group])
	}
	bound := channel.UserBoundModels(userID)
	return slices.DeleteFunc(models, func(name string) bool {
		return !slices.Contains(public, name) && !slices.Contains(bound, name)
	})
}

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func isChannelIDInList(list []int, channelID int) bool {
	return slices.Contains(list, channelID)
}
