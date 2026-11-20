package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

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
	var public []string
	if slices.Contains(channel.GetGroups(), group) {
		if !channel.ChannelInfo.IsMultiKey {
			public = models
		} else {
			indexes := make([]int, 0)
			for i := range channel.GetKeys() {
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
