package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
		assert.Nil(t, GetUserBoundChannel(boundUserId, ""), "a model-less task route never enters the bound-key path")
		assert.Nil(t, GetUserBoundChannel(otherUserId, "bound-model"), "another user never reaches a bound channel")
		assert.Equal(t, []string{"bound-model"}, GetUserBoundModels(boundUserId))
		assert.Empty(t, GetUserBoundModels(otherUserId))
	}
}

func TestRepairChannelMappedKeyAbilitiesUpgrade(t *testing.T) {
	useScheduledChannelDatabase(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	common.MemoryCacheEnabled = false
	require.NoError(t, DB.Migrator().DropTable(&ChannelBudgetAccount{}))
	require.NoError(t, DB.AutoMigrate(&ChannelBudgetAccount{}))
	t.Cleanup(func() { require.NoError(t, DB.Migrator().DropTable(&ChannelBudgetAccount{})) })
	const alias = "deepseek-r1"
	const upstream = "DeepSeek-R1"
	var repairedIDs []int
	var deniedGroups []string
	for _, tc := range []struct {
		name       string
		status     int
		mode       string
		models     []string
		owner      int
		mapped     bool
		needsRoute bool
	}{
		{name: "public", status: common.ChannelStatusEnabled, mode: "allowlist", models: []string{upstream}, mapped: true, needsRoute: true},
		{name: "disabled", status: common.ChannelStatusManuallyDisabled, mode: "allowlist", models: []string{upstream}, mapped: true, needsRoute: true},
		{name: "bound", status: common.ChannelStatusEnabled, mode: "allowlist", models: []string{upstream}, owner: 7, mapped: true},
		{name: "empty", status: common.ChannelStatusEnabled, mode: "allowlist", mapped: true},
		{name: "unrelated", status: common.ChannelStatusEnabled, mode: "allowlist", models: []string{"unrelated-model"}, mapped: true},
		{name: "no-mapping", status: common.ChannelStatusEnabled, mode: "allowlist", models: []string{upstream}},
	} {
		channel := Channel{Name: tc.name, Type: constant.ChannelTypeOpenAI, Key: "fixture-key", Models: alias, Group: tc.name, Status: tc.status,
			Priority: common.GetPointer(int64(5)), Weight: common.GetPointer(uint(3)), Tag: common.GetPointer("retained-tag"),
			ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyScopeModes: map[int]string{0: tc.mode},
				MultiKeyModels: map[int][]string{0: tc.models}, MultiKeyUsers: map[int]int{0: tc.owner}}}
		if tc.mapped {
			channel.ModelMapping = common.GetPointer(`{"deepseek-r1":"DeepSeek-R1"}`)
		}
		require.NoError(t, channel.Insert())
		if tc.needsRoute {
			repairedIDs = append(repairedIDs, channel.Id)
			// Emulate the old release: persisted abilities lack the alias despite
			// authoritative allocations allowing its exact upstream target.
			require.NoError(t, DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error)
			// A stale compatibility projection must not overwrite row authority.
			channel.ChannelInfo.MultiKeyModels = map[int][]string{0: {"stale-projection"}}
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumn("channel_info", channel.ChannelInfo).Error)
		} else {
			deniedGroups = append(deniedGroups, tc.name)
		}
	}
	unchanged := Channel{Name: "unrelated-channel", Type: constant.ChannelTypeOpenAI, Key: "unrelated-key", Models: "unrelated-model", Group: "unrelated-group", Status: common.ChannelStatusEnabled}
	require.NoError(t, unchanged.Insert())
	untouchedAbility := Ability{ChannelId: unchanged.Id, Group: "preserved-group", Model: "preserved-model", Enabled: false, Weight: 17}
	require.NoError(t, DB.Create(&untouchedAbility).Error)
	account := ChannelBudgetAccount{ChannelId: repairedIDs[0], ScopeType: BudgetScopeChannel, ScopeId: int64(repairedIDs[0]), Enabled: true,
		LimitQuota: 100, PeriodUsedQuota: 20, LifetimeUsedQuota: 40, ReservedQuota: 3, ConfigVersion: 4, ResetEpoch: 2}
	require.NoError(t, DB.Create(&account).Error)
	var beforeChannels []Channel
	var beforeEntries []ChannelKeyEntry
	var beforeBudgets []ChannelBudgetAccount
	var unrelatedAbilities []Ability
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Order("id").Find(&beforeChannels).Error)
	require.NoError(t, DB.Order("id").Find(&beforeEntries).Error)
	require.NoError(t, DB.Order("id").Find(&beforeBudgets).Error)
	require.NoError(t, DB.Where("channel_id = ?", unchanged.Id).Order(commonGroupCol+", model").Find(&unrelatedAbilities).Error)
	missing, err := GetRandomSatisfiedChannel("public", alias, 0, nil)
	require.NoError(t, err)
	require.Nil(t, missing)

	abilityDeletes := 0
	const callback = "test:mapped-key-ability-repair"
	require.NoError(t, DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == tx.NamingStrategy.TableName("Ability") {
			abilityDeletes++
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Delete().Remove(callback)) })
	for range 2 {
		require.NoError(t, MigrateChannelKeyEntries())
		assert.Equal(t, len(repairedIDs), abilityDeletes, "second startup must not rewrite matching abilities")
		routed, err := GetRandomSatisfiedChannel("public", alias, 0, nil)
		require.NoError(t, err)
		require.NotNil(t, routed)
		assert.Equal(t, repairedIDs[0], routed.Id)
		assert.Equal(t, []string{alias}, routed.PublicModels())
		var repaired []Ability
		require.NoError(t, DB.Where("channel_id IN ?", repairedIDs).Order("channel_id").Find(&repaired).Error)
		require.Len(t, repaired, 2)
		assert.Equal(t, alias, repaired[0].Model)
		assert.True(t, repaired[0].Enabled)
		require.NotNil(t, repaired[0].Priority)
		assert.Equal(t, int64(5), *repaired[0].Priority)
		assert.Equal(t, uint(3), repaired[0].Weight)
		require.NotNil(t, repaired[0].Tag)
		assert.Equal(t, "retained-tag", *repaired[0].Tag)
		assert.Equal(t, alias, repaired[1].Model)
		assert.False(t, repaired[1].Enabled)
		for _, group := range append(deniedGroups, "disabled") {
			denied, err := GetRandomSatisfiedChannel(group, alias, 0, nil)
			require.NoError(t, err)
			assert.Nil(t, denied, group)
		}
		for _, ability := range GetAllEnableAbilities() {
			assert.NotEqual(t, upstream, ability.Model, "upstream spelling is never added to public inventory")
		}
		var channels []Channel
		var entries []ChannelKeyEntry
		var budgets []ChannelBudgetAccount
		var abilities []Ability
		require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Order("id").Find(&channels).Error)
		require.NoError(t, DB.Order("id").Find(&entries).Error)
		require.NoError(t, DB.Order("id").Find(&budgets).Error)
		require.NoError(t, DB.Where("channel_id = ?", unchanged.Id).Order(commonGroupCol+", model").Find(&abilities).Error)
		assert.Equal(t, beforeChannels, channels, "configuration and channel revisions remain unchanged")
		assert.Equal(t, beforeEntries, entries, "allocation identity, scope and revisions remain unchanged")
		assert.Equal(t, beforeBudgets, budgets, "limits, usage and live holds remain unchanged")
		assert.Equal(t, unrelatedAbilities, abilities)
	}
}
