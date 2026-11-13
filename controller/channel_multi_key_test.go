package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiKeyEnableRestoresOnlyExhaustedChannels(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.AuditLog{}))
	root := &model.User{Username: "multi-key-review-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(root).Error)
	t.Cleanup(func() { require.NoError(t, database.Unscoped().Delete(root).Error) })
	versionQuery := "SELECT VERSION()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)

	for _, cacheEnabled := range []bool{false, true} {
		for _, action := range []string{"enable_key", "enable_all_keys"} {
			for _, tc := range []struct {
				name           string
				initialStatus  int
				autoExhaust    bool
				manualOverride string
				wantStatus     int
			}{
				{name: "key exhaustion restores", initialStatus: common.ChannelStatusEnabled, wantStatus: common.ChannelStatusEnabled},
				{name: "manual disable is preserved", initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "manual disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "status", wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "tag disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "tag", wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "auto key exhaustion restores", initialStatus: common.ChannelStatusEnabled, autoExhaust: true, wantStatus: common.ChannelStatusEnabled},
				{name: "auto disable for another reason is preserved", initialStatus: common.ChannelStatusAutoDisabled, wantStatus: common.ChannelStatusAutoDisabled},
				{name: "manual disable after auto exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, autoExhaust: true, manualOverride: "status", wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "tag disable after auto exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, autoExhaust: true, manualOverride: "tag", wantStatus: common.ChannelStatusManuallyDisabled},
			} {
				t.Run(fmt.Sprintf("cache=%t/%s/%s", cacheEnabled, action, tc.name), func(t *testing.T) {
					common.MemoryCacheEnabled = cacheEnabled
					tag := t.Name()
					channel := &model.Channel{Name: t.Name(), Type: 1, Key: "key-one\nkey-two", Status: tc.initialStatus, Models: "test-model", Group: "default", Tag: &tag,
						ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}},
					}
					require.NoError(t, channel.Insert())
					t.Cleanup(func() {
						require.NoError(t, channel.Delete())
						model.InitChannelCache()
					})
					operations := []string{"disable_key", action}
					if tc.autoExhaust {
						// Key two is already disabled, so a request failure on key
						// one disables the whole channel automatically.
						model.InitChannelCache()
						require.True(t, model.UpdateChannelStatus(channel.Id, "key-one", common.ChannelStatusAutoDisabled, "provider rejected key"))
						exhausted, err := model.GetChannelById(channel.Id, true)
						require.NoError(t, err)
						require.Equal(t, common.ChannelStatusAutoDisabled, exhausted.Status)
						operations = []string{action}
					}
					for _, operation := range operations {
						if operation == action {
							if tc.manualOverride == "status" {
								model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")
							} else if tc.manualOverride == "tag" {
								require.NoError(t, model.DisableChannelByTag(tag))
							}
						}
						payload, err := common.Marshal(MultiKeyManageRequest{ChannelId: channel.Id, Action: operation, KeyIndex: common.GetPointer(0)})
						require.NoError(t, err)
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Set("id", root.Id)
						c.Set("role", common.RoleRootUser)
						c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", bytes.NewReader(payload))
						c.Request.Header.Set("Content-Type", "application/json")
						ManageMultiKeys(c)
						var result struct {
							Success bool `json:"success"`
						}
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
						require.True(t, result.Success, recorder.Body.String())
					}
					loaded, err := model.GetChannelById(channel.Id, true)
					require.NoError(t, err)
					assert.Equal(t, tc.wantStatus, loaded.Status)
					if tc.wantStatus == common.ChannelStatusEnabled {
						assert.Equal(t, "", loaded.GetOtherInfo()["status_reason"])
					}
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyStatusList, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledReason, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledTime, 0)
					var ability model.Ability
					require.NoError(t, database.Where("channel_id = ?", channel.Id).First(&ability).Error)
					assert.Equal(t, tc.wantStatus == common.ChannelStatusEnabled, ability.Enabled)
				})
			}
		}
	}
}

// TestMultiKeyKeyScopeSelection pins the key chosen for one request: a per-key
// model scope and a user-bound key decide which key serves it, and a request
// that no enabled key can serve stays retryable instead of disabling the channel.
func TestMultiKeyKeyScopeSelection(t *testing.T) {
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisable })
	common.AutomaticDisableChannelEnabled = true

	newChannel := func(info model.ChannelInfo) *model.Channel {
		return &model.Channel{Id: 1, Key: "sk-first\nsk-second", Models: "model-a,model-b", ChannelInfo: info}
	}

	t.Run("each model is only served by the keys scoped to it", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels: map[int][]string{0: {"model-a"}, 1: {"model-b"}}})
		for modelName, wantIndex := range map[string]int{"model-a": 0, "model-b": 1} {
			key, index, apiErr := channel.GetNextEnabledKeyFor(modelName, 0)
			require.Nil(t, apiErr)
			assert.Equal(t, wantIndex, index, modelName)
			assert.Equal(t, channel.GetKeys()[wantIndex], key, modelName)
		}
		assert.ElementsMatch(t, []string{"model-a", "model-b"}, channel.PublicModels())
	})

	t.Run("a bound key is exclusive to its user and preferred for it", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyUsers: map[int]int{1: 7}})
		for range 5 {
			_, index, apiErr := channel.GetNextEnabledKeyFor("model-a", 7)
			require.Nil(t, apiErr)
			assert.Equal(t, 1, index, "the user must always get the key bound to them")
			_, index, apiErr = channel.GetNextEnabledKeyFor("model-a", 8)
			require.Nil(t, apiErr)
			assert.Equal(t, 0, index, "another user must never get the bound key")
		}
		assert.ElementsMatch(t, []string{"model-a", "model-b"}, channel.UserBoundModels(7))
		assert.Nil(t, channel.UserBoundModels(8))
		assert.ElementsMatch(t, []string{"model-a", "model-b"}, channel.PublicModels())
	})

	t.Run("a bound key wins over a public key serving the same model", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels: map[int][]string{0: {"model-a"}}, MultiKeyUsers: map[int]int{1: 7}})
		key, index, apiErr := channel.GetNextEnabledKeyFor("model-a", 7)
		require.Nil(t, apiErr)
		assert.Equal(t, 1, index)
		assert.Equal(t, "sk-second", key)
	})

	t.Run("a channel whose keys are all bound has no public models", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyUsers: map[int]int{0: 7, 1: 8}})
		assert.Empty(t, channel.PublicModels())
		assert.ElementsMatch(t, []string{"model-a", "model-b"}, channel.UserBoundModels(8))
	})

	t.Run("no eligible key is retryable and does not disable the channel", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels:     map[int][]string{0: {"model-a"}, 1: {"model-b"}},
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}})
		_, _, apiErr := channel.GetNextEnabledKeyFor("model-b", 0)
		require.NotNil(t, apiErr)
		assert.Equal(t, types.ErrorCodeChannelKeyScopeUnsatisfied, apiErr.GetErrorCode())
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
		assert.False(t, types.IsChannelError(apiErr), "a healthy channel must not look like a channel error")
		assert.False(t, service.ShouldDisableChannel(apiErr), "the channel must not be disabled automatically")

		// Every key disabled keeps the previous error and its consequences.
		exhausted := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}})
		_, _, apiErr = exhausted.GetNextEnabledKeyFor("model-a", 0)
		require.NotNil(t, apiErr)
		assert.Equal(t, types.ErrorCodeChannelNoAvailableKey, apiErr.GetErrorCode())
		assert.True(t, service.ShouldDisableChannel(apiErr))
	})

	t.Run("a channel without the new configuration behaves as before", func(t *testing.T) {
		channel := newChannel(model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}})
		key, index, apiErr := channel.GetNextEnabledKeyFor("model-a", 0)
		require.Nil(t, apiErr)
		assert.Equal(t, 0, index)
		assert.Equal(t, "sk-first", key)
		key, index, apiErr = channel.GetNextEnabledKey()
		require.Nil(t, apiErr)
		assert.Equal(t, 0, index)
		assert.Equal(t, "sk-first", key)
		assert.Equal(t, channel.GetModels(), channel.PublicModels())
		assert.Nil(t, channel.UserBoundModels(1))

		singleKey := &model.Channel{Id: 2, Key: "sk-single", Models: "model-a"}
		key, index, apiErr = singleKey.GetNextEnabledKeyFor("model-a", 5)
		require.Nil(t, apiErr)
		assert.Equal(t, "sk-single", key)
		assert.Equal(t, 0, index)
		assert.Equal(t, singleKey.GetModels(), singleKey.PublicModels())
		assert.Nil(t, singleKey.UserBoundModels(5))
	})
}

func postMultiKeyManage(t *testing.T, userID int, request MultiKeyManageRequest) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", userID)
	c.Set("role", common.RoleRootUser)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key/manage", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	ManageMultiKeys(c)
	return recorder
}

// TestMultiKeyManageKeepsKeyScopesOnTheirKey proves that deleting a key in the
// middle does not move a model scope or a user binding onto a neighbour, and
// that the management actions read and write both of them (AC7).
func TestMultiKeyManageKeepsKeyScopesOnTheirKey(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	channel := &model.Channel{Name: "multi-key-scope", Type: 1, Key: "sk-one\nsk-two\nsk-three", Models: "model-a,model-b,model-c", Group: "default",
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels: map[int][]string{0: {"model-a"}, 1: {"model-b"}, 2: {"model-c"}},
			MultiKeyUsers:  map[int]int{2: rootID}},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "delete_key", KeyIndex: common.GetPointer(1)}).Code == http.StatusOK)
	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, "sk-one\nsk-three", loaded.Key)
	require.Equal(t, 2, loaded.ChannelInfo.MultiKeySize)
	assert.Equal(t, map[int][]string{0: {"model-a"}, 1: {"model-c"}}, loaded.ChannelInfo.MultiKeyModels,
		"the scope must follow the key it was set on, not the old index")
	assert.Equal(t, map[int]int{1: rootID}, loaded.ChannelInfo.MultiKeyUsers)

	// The moved scope really decides the key used for that model.
	key, index, apiErr := loaded.GetNextEnabledKeyFor("model-c", 0)
	require.Nil(t, apiErr)
	assert.Equal(t, 1, index)
	assert.Equal(t, "sk-three", key)

	recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "get_key_status", Page: 1, PageSize: 50})
	var listResult struct {
		Success bool `json:"success"`
		Data    struct {
			Keys []KeyStatus `json:"keys"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &listResult))
	require.True(t, listResult.Success, recorder.Body.String())
	require.Len(t, listResult.Data.Keys, 2)
	assert.Equal(t, []string{"model-a"}, listResult.Data.Keys[0].Models)
	assert.Equal(t, 0, listResult.Data.Keys[0].UserId)
	assert.Empty(t, listResult.Data.Keys[0].Username)
	assert.Equal(t, []string{"model-c"}, listResult.Data.Keys[1].Models)
	assert.Equal(t, rootID, listResult.Data.Keys[1].UserId)
	assert.Equal(t, "channel-create-review-root", listResult.Data.Keys[1].Username)

	rejected := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(0), Models: []string{"model-not-on-channel"}})
	var rejectedResult struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(rejected.Body.Bytes(), &rejectedResult))
	assert.False(t, rejectedResult.Success)
	assert.NotEmpty(t, rejectedResult.Message)

	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(1), Models: []string{"model-a", "model-b"}}).Code == http.StatusOK)
	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_user", KeyIndex: common.GetPointer(0), UserId: common.GetPointer(rootID)}).Code == http.StatusOK)
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, map[int][]string{0: {"model-a"}, 1: {"model-a", "model-b"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, map[int]int{0: rootID, 1: rootID}, loaded.ChannelInfo.MultiKeyUsers)

	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(1), Models: []string{}}).Code == http.StatusOK)
	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_user", KeyIndex: common.GetPointer(1), UserId: common.GetPointer(0)}).Code == http.StatusOK)
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, map[int][]string{0: {"model-a"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, map[int]int{0: rootID}, loaded.ChannelInfo.MultiKeyUsers)
}

// TestUpdateChannelConvertsSingleKeyChannel pins the single-key to multi-key
// conversion: the existing key stays the first key and later keys are appended.
func TestUpdateChannelConvertsSingleKeyChannel(t *testing.T) {
	rootID := setupChannelCreateTest(t)

	for _, tc := range []struct {
		name     string
		request  map[string]any
		wantKeys string
		wantMode constant.MultiKeyMode
	}{
		{
			name:     "append submitted keys",
			request:  map[string]any{"to_multi_key": true, "multi_key_mode": "polling", "key": "sk-second\nsk-third", "key_mode": "append"},
			wantKeys: "sk-first\nsk-second\nsk-third",
			wantMode: constant.MultiKeyModePolling,
		},
		{
			name:     "keep only the existing key",
			request:  map[string]any{"to_multi_key": true},
			wantKeys: "sk-first",
			wantMode: constant.MultiKeyModeRandom,
		},
		{
			name:     "replace with submitted keys",
			request:  map[string]any{"to_multi_key": true, "key": "sk-second", "key_mode": "replace"},
			wantKeys: "sk-second",
			wantMode: constant.MultiKeyModeRandom,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &model.Channel{Name: "single-to-multi-" + tc.name, Type: 1, Key: "sk-first", Models: "model-a", Group: "default"}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() { require.NoError(t, channel.Delete()) })

			body := map[string]any{"id": channel.Id, "name": channel.Name, "type": 1, "models": "model-a", "group": "default"}
			for field, value := range tc.request {
				body[field] = value
			}
			payload, err := common.Marshal(body)
			require.NoError(t, err)
			response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())

			loaded, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.True(t, loaded.ChannelInfo.IsMultiKey)
			assert.Equal(t, tc.wantKeys, loaded.Key)
			assert.Equal(t, tc.wantMode, loaded.ChannelInfo.MultiKeyMode)
			assert.Equal(t, len(loaded.GetKeys()), loaded.ChannelInfo.MultiKeySize)

			// Every key is usable, so the channel still routes the model.
			_, _, apiErr := loaded.GetNextEnabledKeyFor("model-a", 0)
			assert.Nil(t, apiErr)
			assert.Equal(t, []string{"model-a"}, loaded.PublicModels())
		})
	}

	t.Run("a JSON key channel keeps refusing the conversion", func(t *testing.T) {
		channel := &model.Channel{Name: "codex-single-to-multi", Type: constant.ChannelTypeCodex, Models: "model-a", Group: "default",
			Key: `{"access_token":"token","account_id":"account"}`}
		require.NoError(t, channel.Insert())
		t.Cleanup(func() { require.NoError(t, channel.Delete()) })
		payload, err := common.Marshal(map[string]any{"id": channel.Id, "name": channel.Name, "type": constant.ChannelTypeCodex, "models": "model-a", "group": "default", "to_multi_key": true})
		require.NoError(t, err)
		response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
		var result struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		assert.False(t, result.Success)
		assert.NotEmpty(t, result.Message)
		loaded, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.False(t, loaded.ChannelInfo.IsMultiKey)
	})
}

// TestDistributeRoutesBoundKeyAndFallsBackWhenNoKeyServes walks the HTTP
// distribution middleware: a key bound to the user routes that user to the
// channel even outside the user's group with the very key scoped to the model
// (AC1, AC2, AC6), no other user reaches it, and a channel whose remaining keys
// cannot serve the model hands the request to another channel instead of
// forwarding it without a key (AC8).
func TestDistributeRoutesBoundKeyAndFallsBackWhenNoKeyServes(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, true, false
	common.SQLitePath = filepath.Join(t.TempDir(), "distribute.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}))

	const boundUserId, otherUserId = 9301, 9302
	highPriority, lowPriority := int64(50), int64(1)
	channels := []*model.Channel{
		{Id: 9401, Name: "bound", Type: constant.ChannelTypeOpenAI, Key: "sk-public\nsk-bound", Status: common.ChannelStatusEnabled,
			Models: "shared-model,bound-model", Group: "vip", Priority: &highPriority,
			ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
				MultiKeyModels: map[int][]string{0: {"shared-model"}, 1: {"bound-model"}},
				MultiKeyUsers:  map[int]int{1: boundUserId}}},
		{Id: 9402, Name: "gap", Type: constant.ChannelTypeOpenAI, Key: "sk-gap\nsk-other", Status: common.ChannelStatusEnabled,
			Models: "flaky-model", Group: "default", Priority: &highPriority,
			ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
				MultiKeyModels:     map[int][]string{0: {"flaky-model"}, 1: {"other-model"}},
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled}}},
		{Id: 9403, Name: "fallback", Type: constant.ChannelTypeOpenAI, Key: "sk-fallback", Status: common.ChannelStatusEnabled,
			Models: "flaky-model", Group: "default", Priority: &lowPriority},
	}
	for _, channel := range channels {
		require.NoError(t, database.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(database))
	}
	model.InitChannelCache()

	distribute := func(userId int, modelName string) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"`+modelName+`"}`)))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", userId)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		middleware.Distribute()(c)
		return c
	}

	// AC1/AC2/AC6: the bound user reaches the vip channel outside their group,
	// and the key scoped to the requested model is the one used.
	boundRequest := distribute(boundUserId, "bound-model")
	require.Equal(t, 9401, common.GetContextKeyInt(boundRequest, constant.ContextKeyChannelId), boundRequest.Writer.Header())
	assert.Equal(t, "sk-bound", common.GetContextKeyString(boundRequest, constant.ContextKeyChannelKey))
	assert.True(t, service.UserBoundChannelRouted(boundRequest))

	// AC1: another user never reaches the bound key; no channel serves the model
	// publicly, so the request is rejected instead of being forwarded.
	otherRequest := distribute(otherUserId, "bound-model")
	require.Equal(t, http.StatusServiceUnavailable, otherRequest.Writer.Status())
	assert.True(t, otherRequest.IsAborted())
	assert.Empty(t, common.GetContextKeyString(otherRequest, constant.ContextKeyChannelKey))

	// AC8: the highest-priority channel has no enabled key serving the model, so
	// the request switches to the next channel and that channel stays enabled.
	fallbackRequest := distribute(otherUserId, "flaky-model")
	assert.False(t, fallbackRequest.IsAborted(), "the request reaches the relay handler instead of failing")
	assert.Equal(t, 9403, common.GetContextKeyInt(fallbackRequest, constant.ContextKeyChannelId))
	assert.Equal(t, "sk-fallback", common.GetContextKeyString(fallbackRequest, constant.ContextKeyChannelKey))
	gapChannel, err := model.GetChannelById(9402, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, gapChannel.Status, "a channel without an eligible key is not disabled")
}
