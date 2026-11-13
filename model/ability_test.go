package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func channelAbilityKeys(t *testing.T, channelId int) []string {
	t.Helper()

	var abilities []Ability
	require.NoError(t, DB.Where("channel_id = ?", channelId).Find(&abilities).Error)
	keys := make([]string, 0, len(abilities))
	for _, ability := range abilities {
		require.True(t, ability.Enabled)
		keys = append(keys, ability.Group+"/"+ability.Model)
	}
	return keys
}

func TestChannelAbilitiesHonorGroupModelScope(t *testing.T) {
	truncateTables(t)

	channel := Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key",
		Status: common.ChannelStatusEnabled,
		Name:   "scoped",
		Models: "model-a,model-b",
		Group:  "group-a,group-b",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{GroupModels: map[string][]string{"group-a": {"model-a"}}})
	require.NoError(t, channel.Insert())

	assert.ElementsMatch(t,
		[]string{"group-a/model-a", "group-b/model-a", "group-b/model-b"},
		channelAbilityKeys(t, channel.Id))

	// A whitelisted model that left the channel is ignored instead of stored.
	channel.SetOtherSettings(dto.ChannelOtherSettings{GroupModels: map[string][]string{"group-a": {"model-a", "removed-model"}}})
	require.NoError(t, channel.UpdateAbilities(nil))
	assert.ElementsMatch(t,
		[]string{"group-a/model-a", "group-b/model-a", "group-b/model-b"},
		channelAbilityKeys(t, channel.Id))
}

func TestChannelCacheRoutingHonorsGroupModelScope(t *testing.T) {
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCache
		InitChannelCache()
	})
	truncateTables(t)

	common.MemoryCacheEnabled = true
	channel := Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key",
		Status: common.ChannelStatusEnabled,
		Name:   "scoped",
		Models: "model-a,model-b",
		Group:  "group-a,group-b",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{GroupModels: map[string][]string{"group-a": {"model-a"}}})
	require.NoError(t, channel.Insert())
	InitChannelCache()

	assert.True(t, IsChannelEnabledForGroupModel("group-a", "model-a", channel.Id))
	assert.False(t, IsChannelEnabledForGroupModel("group-a", "model-b", channel.Id), "a group outside the whitelist must not route the model")
	assert.True(t, IsChannelEnabledForGroupModel("group-b", "model-b", channel.Id), "groups without a whitelist keep every channel model")
}

func TestChannelAbilitiesExcludeUserBoundKeys(t *testing.T) {
	truncateTables(t)

	channel := Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key-0\nkey-1",
		Status: common.ChannelStatusEnabled,
		Name:   "multi",
		Models: "model-a,model-b",
		Group:  "default",
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyUsers: map[int]int{
				0: 7001,
			},
		},
	}
	require.NoError(t, channel.Insert())
	assert.ElementsMatch(t, []string{"default/model-a", "default/model-b"}, channelAbilityKeys(t, channel.Id),
		"a public key keeps serving every channel model")

	channel.ChannelInfo.MultiKeyUsers = map[int]int{0: 7001, 1: 7002}
	require.NoError(t, channel.UpdateAbilities(nil))
	assert.Empty(t, channelAbilityKeys(t, channel.Id), "a channel without public keys serves no group")

	channel.ChannelInfo.MultiKeyUsers = map[int]int{0: 7001}
	channel.ChannelInfo.MultiKeyModels = map[int][]string{1: {"model-b"}}
	require.NoError(t, channel.UpdateAbilities(nil))
	assert.ElementsMatch(t, []string{"default/model-b"}, channelAbilityKeys(t, channel.Id),
		"only models of the remaining public key stay reachable")
}

func TestGetUserBoundChannelReachesBoundUserOutsideGroup(t *testing.T) {
	originalMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCache
		InitChannelCache()
	})
	truncateTables(t)

	for index, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		boundUserId := 7101 + index
		otherUserId := 7201 + index
		channel := Channel{
			Type:   constant.ChannelTypeOpenAI,
			Key:    "key-0\nkey-1",
			Status: common.ChannelStatusEnabled,
			Name:   "bound",
			Models: "bound-model,public-model",
			Group:  "vip",
			ChannelInfo: ChannelInfo{
				IsMultiKey:    true,
				MultiKeySize:  2,
				MultiKeyUsers: map[int]int{0: boundUserId},
				MultiKeyModels: map[int][]string{
					0: {"bound-model"},
				},
			},
		}
		require.NoError(t, channel.Insert())
		InitChannelCache()

		bound := GetUserBoundChannel(boundUserId, "bound-model")
		require.NotNil(t, bound, "memory cache %t", memoryCache)
		assert.Equal(t, channel.Id, bound.Id)
		assert.Nil(t, GetUserBoundChannel(boundUserId, "public-model"), "the bound key does not serve this model")
		assert.Nil(t, GetUserBoundChannel(otherUserId, "bound-model"), "another user never reaches a bound channel")
		assert.Equal(t, []string{"bound-model"}, GetUserBoundModels(boundUserId))
		assert.Empty(t, GetUserBoundModels(otherUserId))
	}
}
