package service

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelSelectAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))

	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))

		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	return db
}

func createChannelSelectAutoGroupsChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	t.Helper()
	priority := int64(0)
	weight := uint(100)
	require.NoError(t, db.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Weight:   &weight,
		Models:   modelName,
		Group:    group,
		Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestRetryAvoidsFailedChannelsAcrossPrioritiesAndGroups(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	// Initialize dialect quoting through startup without migrating unrelated
	// tables. The ordinary service fixture otherwise uses only memory routing.
	previousMaster, previousLogDB := common.IsMasterNode, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	t.Cleanup(func() {
		common.IsMasterNode = false
		common.SetMainDatabaseType(previousMainType)
		require.NoError(t, model.InitLogDB())
		common.SetLogDatabaseType(previousLogType)
		model.LOG_DB = previousLogDB
		common.IsMasterNode = previousMaster
	})
	const modelName = "retry-failover"
	for _, spec := range []struct {
		id       int
		group    string
		priority int64
	}{{3101, "default", 10}, {3102, "default", 10}, {3103, "default", 5}, {3104, "vip", 10}, {3105, "default", 20}} {
		createChannelSelectAutoGroupsChannel(t, db, spec.id, spec.group, modelName)
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", spec.id).Updates(map[string]any{"priority": spec.priority, "setting": `{"responses_websocket_enabled":true}`}).Error)
		require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", spec.id).Update("priority", spec.priority).Error)
	}
	// A higher-priority channel that fails a transport filter stays excluded.
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 3105).Update("setting", `{"responses_websocket_enabled":false}`).Error)
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinity
	t.Cleanup(func() { *affinity = previousAffinity })
	common.RetryTimes = 5
	for _, cached := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("cache=%t/enabled=%t", cached, enabled), func(t *testing.T) {
				common.MemoryCacheEnabled = cached
				model.InitChannelCache()
				affinity.RetryAvoidFailedChannels = enabled
				ctx := newRequestAutoGroupsContext()
				GetChannelConstraints(ctx).AddFilter(dto.ChannelFilter{Kind: dto.FilterResponsesWebSocket})
				state := RequestPolicy(ctx)
				state.FailedChannelIDs = []int{3101}
				retry := &RetryParam{Ctx: ctx, TokenGroup: "default", ModelName: modelName, Retry: common.GetPointer(1)}
				channel, _, err := CacheGetRandomSatisfiedChannel(retry)
				require.NoError(t, err)
				require.NotNil(t, channel)
				if !enabled {
					assert.Equal(t, 3103, channel.Id, "disabled switch preserves priority-by-attempt routing")
					return
				}
				assert.Equal(t, 3102, channel.Id, "the same-priority peer must be selected")
				state.FailedChannelIDs = append(state.FailedChannelIDs, 3102)
				retry.IncreaseRetry()
				channel, _, err = CacheGetRandomSatisfiedChannel(retry)
				require.NoError(t, err)
				require.NotNil(t, channel)
				assert.Equal(t, 3103, channel.Id)
				state.FailedChannelIDs = append(state.FailedChannelIDs, 3103)
				retry.IncreaseRetry()
				channel, _, err = CacheGetRandomSatisfiedChannel(retry)
				require.NoError(t, err)
				require.NotNil(t, channel)
				assert.Equal(t, 3103, channel.Id, "a repeat is allowed only after every eligible channel failed")
				common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
				common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"default", "vip"})
				common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)
				retry.TokenGroup = "auto"
				channel, group, err := CacheGetRandomSatisfiedChannel(retry)
				require.NoError(t, err)
				require.NotNil(t, channel)
				assert.Equal(t, 3104, channel.Id, "an untried later group precedes repeating the current group")
				assert.Equal(t, "vip", group)
			})
		}
	}
}

func TestSelectChannelForRequestRoutesBoundUserThenFallsBackOnRetry(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "bound-model"
	createChannelSelectAutoGroupsChannel(t, db, 4301, "default", modelName)

	boundUserId := 4302
	boundPriority := int64(100)
	boundWeight := uint(100)
	channelInfo := model.ChannelInfo{
		IsMultiKey:     true,
		MultiKeySize:   2,
		MultiKeyUsers:  map[int]int{0: boundUserId},
		MultiKeyModels: map[int][]string{0: {modelName}},
	}
	require.NoError(t, db.Create(&model.Channel{
		Id:          4302,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "bound-key\npublic-key",
		Status:      common.ChannelStatusEnabled,
		Name:        "bound-channel",
		Models:      modelName,
		Group:       "vip",
		Priority:    &boundPriority,
		Weight:      &boundWeight,
		ChannelInfo: channelInfo,
	}).Error)
	model.InitChannelCache()

	require.NotNil(t, model.GetUserBoundChannel(boundUserId, modelName))

	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	ctx.Set("id", boundUserId)
	firstAttempt := &RetryParam{Ctx: ctx, TokenGroup: "default", ModelName: modelName, Retry: common.GetPointer(0)}
	channel, group, selectErr := SelectChannelForRequest(ctx, modelName, firstAttempt)
	require.Nil(t, selectErr)
	require.NotNil(t, channel)
	assert.Equal(t, 4302, channel.Id, "a key bound to the user outranks group routing outside the group")
	assert.Empty(t, group)
	assert.True(t, UserBoundChannelRouted(ctx))

	retryCtx := newRequestAutoGroupsContext()
	common.SetContextKey(retryCtx, constant.ContextKeyUserGroup, "default")
	retryCtx.Set("id", boundUserId)
	retryAttempt := &RetryParam{Ctx: retryCtx, TokenGroup: "default", ModelName: modelName, Retry: common.GetPointer(1)}
	channel, _, selectErr = SelectChannelForRequest(retryCtx, modelName, retryAttempt)
	require.Nil(t, selectErr)
	require.NotNil(t, channel)
	assert.Equal(t, 4301, channel.Id, "retries keep the ordinary group routing")
	assert.False(t, UserBoundChannelRouted(retryCtx))

	otherCtx := newRequestAutoGroupsContext()
	common.SetContextKey(otherCtx, constant.ContextKeyUserGroup, "default")
	otherCtx.Set("id", boundUserId+1)
	otherAttempt := &RetryParam{Ctx: otherCtx, TokenGroup: "default", ModelName: modelName, Retry: common.GetPointer(0)}
	channel, _, selectErr = SelectChannelForRequest(otherCtx, modelName, otherAttempt)
	require.Nil(t, selectErr)
	require.NotNil(t, channel)
	assert.Equal(t, 4301, channel.Id, "a bound channel is never chosen for another user")
}

func TestCacheGetRandomSatisfiedChannelUsesTokenAutoGroupsWhenGlobalAutoIsEmpty(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-groups-runtime-model"
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}

	first, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 2101, first.Id)
	assert.Equal(t, "vip", selectedGroup)
	assert.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
	assert.Empty(t, setting.GetAutoGroups(), "the selection must not depend on the global Auto list")

	param.IncreaseRetry()
	second, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 2102, second.Id)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
}
