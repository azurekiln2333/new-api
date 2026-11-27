package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPinnedChannelTestKeyUsesKeyMappingForUnavailableEvidence(t *testing.T) {
	channelMapping := `{"alias":"channel-upstream"}`
	keyMapping := `{"alias":"key-upstream"}`
	for _, multiKey := range []bool{false, true} {
		t.Run(fmt.Sprintf("multi-key=%t", multiKey), func(t *testing.T) {
			channel := &model.Channel{
				Key:          "sk-key",
				Models:       "alias",
				Status:       common.ChannelStatusEnabled,
				ModelMapping: &channelMapping,
				ChannelInfo: model.ChannelInfo{
					IsMultiKey:            multiKey,
					MultiKeySize:          1,
					MultiKeyModelMappings: map[int]string{0: keyMapping},
					ModelUnavailable: []model.ChannelModelUnavailability{{
						UpstreamModel:  "key-upstream",
						KeyFingerprint: model.ChannelKeyFingerprint("sk-key"),
					}},
				},
			}
			index := -1
			if multiKey {
				index = 0
			}
			pinned := &pinnedChannelTestKey{index: index, value: "sk-key"}
			assert.False(t, pinned.stillServes(channel, "alias", false))
		})
	}
}

func TestManagedKeyStatusPublishesOnlyChangedChannel(t *testing.T) {
	setupChannelCreateTest(t)
	for _, cacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cacheEnabled), func(t *testing.T) {
			previous := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = cacheEnabled
			t.Cleanup(func() { common.MemoryCacheEnabled = previous })
			channel := &model.Channel{Name: t.Name(), Type: 1, Key: "first\nsecond", Models: "managed-model", Group: "default", Status: common.ChannelStatusEnabled,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
			require.NoError(t, channel.Insert())
			model.InitChannelCache()
			const callback = "test:managed-status-no-full-reload"
			database := model.DB
			fullReads := 0
			require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "channels" {
					return
				}
				if _, bounded := tx.Statement.Clauses["WHERE"]; !bounded {
					fullReads++
				}
			}))
			t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })
			require.NoError(t, saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
				stored.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}
				disableMultiKeyChannelIfUnavailable(stored)
				return nil
			}))
			cached, err := model.CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusManuallyDisabled, cached.Status)
			assert.Equal(t, common.ChannelStatusManuallyDisabled, cached.ChannelInfo.MultiKeyStatusList[0])
			require.NoError(t, saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
				delete(stored.ChannelInfo.MultiKeyStatusList, 0)
				restoreMultiKeyChannelIfAvailable(stored)
				return nil
			}))
			cached, err = model.CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusEnabled, cached.Status)
			assert.Equal(t, common.ChannelStatusManuallyDisabled, cached.ChannelInfo.MultiKeyStatusList[1])
			assert.Zero(t, fullReads, "status saves must publish one channel without a full channel reload")
		})
	}
}

func TestMultiKeyAllocationPublishesOnlyChangedChannel(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, cacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cacheEnabled), func(t *testing.T) {
			previous := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = cacheEnabled
			t.Cleanup(func() { common.MemoryCacheEnabled = previous })
			channel := &model.Channel{Name: t.Name(), Type: 1, Key: "allocation-first", Models: "allocation-model", Group: "default", Status: common.ChannelStatusEnabled,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1}}
			require.NoError(t, channel.Insert())
			model.InitChannelCache()
			const callback = "test:managed-allocation-no-full-reload"
			database := model.DB
			fullReads := 0
			require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "channels" {
					return
				}
				if _, bounded := tx.Statement.Clauses["WHERE"]; !bounded {
					fullReads++
				}
			}))
			t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })

			current, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			entryID := strconv.FormatInt(current.KeyEntryIDs[0], 10)
			revision := current.KeyEntryRevisions[0]
			recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyEntryID: entryID, ExpectedRev: &revision, ScopeMode: common.GetPointer("allowlist")})
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
			require.True(t, result.Success, recorder.Body.String())
			assert.Zero(t, fullReads, "allocation mutations must publish one channel without a full channel reload")

			published, err := model.CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, "allowlist", published.ChannelInfo.MultiKeyScopeModes[0])
			assert.False(t, model.IsChannelEnabledForGroupModel("default", "allocation-model", channel.Id), "the newly empty allowlist must remove the local public route")
		})
	}
}

func TestMultiKeyEnableRestoresOnlyExhaustedChannels(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	database := model.DB
	// Allocation rows own the status list, so this fixture keeps the legacy
	// projection the test drives through the management actions.
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	tag := ""

	// The follower-style fixture above proves the activation marker prerequisite
	// without enabling enforcement, so the status transitions stay the same.
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
					tag = t.Name()
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
						current, err := model.GetChannelById(channel.Id, true)
						require.NoError(t, err)
						revision := current.ChannelInfo.KeyEntryRevision
						payload, err := common.Marshal(MultiKeyManageRequest{ChannelId: channel.Id, Action: operation, KeyIndex: common.GetPointer(0), ExpectedRevision: &revision})
						require.NoError(t, err)
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Set("id", rootID)
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
	setupChannelCreateTest(t)
	// Pure selection: no allocation row, so the feature stays off for the
	// in-memory channels below.
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
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

	t.Run("key capability accepts the exact mapped target while visibility keeps the request alias", func(t *testing.T) {
		const alias = "deepseek-r1"
		const target = "DeepSeek-R1"
		for _, tc := range []struct {
			name    string
			mapping string
			scope   []string
			allowed bool
		}{
			{name: "direct alias", mapping: `{"deepseek-r1":"DeepSeek-R1"}`, scope: []string{alias}, allowed: true},
			{name: "mapped target", mapping: `{"deepseek-r1":"DeepSeek-R1"}`, scope: []string{target}, allowed: true},
			{name: "chained target", mapping: `{"deepseek-r1":"intermediate","intermediate":"DeepSeek-R1"}`, scope: []string{target}, allowed: true},
			{name: "intermediate is not the dispatched target", mapping: `{"deepseek-r1":"intermediate","intermediate":"DeepSeek-R1"}`, scope: []string{"intermediate"}},
			{name: "unrelated scope", mapping: `{"deepseek-r1":"DeepSeek-R1"}`, scope: []string{"other-model"}},
			{name: "explicit empty scope", mapping: `{"deepseek-r1":"DeepSeek-R1"}`},
			{name: "case alone does not authorize", scope: []string{target}},
			{name: "malformed mapping", mapping: `{`, scope: []string{target}},
			{name: "cyclic mapping", mapping: `{"deepseek-r1":"DeepSeek-R1","DeepSeek-R1":"deepseek-r1"}`, scope: []string{target}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				channel := &model.Channel{Id: 1, Key: "mapped-key", Models: alias, ModelMapping: &tc.mapping,
					ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyMode: constant.MultiKeyModeRandom,
						MultiKeyScopeModes: map[int]string{0: "allowlist"}, MultiKeyModels: map[int][]string{0: tc.scope}}}
				assert.Equal(t, tc.allowed, channel.KeyServesModel(0, alias))
				key, index, apiErr := channel.GetNextEnabledKeyFor(alias, 7)
				if tc.allowed {
					require.Nil(t, apiErr)
					assert.Equal(t, "mapped-key", key)
					assert.Zero(t, index)
					assert.Equal(t, []string{alias}, channel.PublicModels())
				} else {
					require.NotNil(t, apiErr)
					assert.Equal(t, types.ErrorCodeChannelKeyScopeUnsatisfied, apiErr.GetErrorCode())
					assert.Empty(t, channel.PublicModels())
				}
				assert.NotContains(t, channel.PublicModels(), target, "mapping does not add upstream names to the client catalog")
				channel.ChannelInfo.MultiKeyUsers = map[int]int{0: 7}
				assert.Empty(t, channel.PublicModels())
				if tc.allowed {
					assert.Equal(t, []string{alias}, channel.UserBoundModels(7))
				} else {
					assert.Empty(t, channel.UserBoundModels(7))
				}
				assert.Empty(t, channel.UserBoundModels(8))
				_, _, apiErr = channel.GetNextEnabledKeyFor(alias, 8)
				require.NotNil(t, apiErr, "mapping must not bypass the allocation owner")
			})
		}
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
	channel := &model.Channel{Name: "multi-key-scope", Type: 1, Key: "sk-one\nsk-two\nsk-three", Models: "manual-model,model-a,model-b,model-c", Group: "default",
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

	added := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(0), Models: []string{"model-a", " model-new ", "model-new", " "}})
	require.Equal(t, http.StatusOK, added.Code, added.Body.String())
	var addedResult struct {
		Success bool `json:"success"`
		Data    struct {
			ChannelModels []string `json:"channel_models"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(added.Body.Bytes(), &addedResult))
	require.True(t, addedResult.Success, added.Body.String())
	assert.Equal(t, []string{"manual-model", "model-a", "model-b", "model-c", "model-new"}, addedResult.Data.ChannelModels)
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "manual-model,model-a,model-b,model-c,model-new", loaded.Models, "saving one key adds normalized models without deleting or reordering the manual catalog")
	assert.Equal(t, map[int][]string{0: {"model-a", "model-new"}, 1: {"model-c"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, map[int]int{1: rootID}, loaded.ChannelInfo.MultiKeyUsers)

	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(1), Models: []string{"model-a", "model-b"}}).Code == http.StatusOK)
	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_user", KeyIndex: common.GetPointer(0), UserId: common.GetPointer(rootID)}).Code == http.StatusOK)
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, map[int][]string{0: {"model-a", "model-new"}, 1: {"model-a", "model-b"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, map[int]int{0: rootID, 1: rootID}, loaded.ChannelInfo.MultiKeyUsers)

	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_models", KeyIndex: common.GetPointer(1), Models: []string{}}).Code == http.StatusOK)
	require.True(t, postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "set_key_user", KeyIndex: common.GetPointer(1), UserId: common.GetPointer(0)}).Code == http.StatusOK)
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, map[int][]string{0: {"model-a", "model-new"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, map[int]int{0: rootID}, loaded.ChannelInfo.MultiKeyUsers)
	assert.Equal(t, "manual-model,model-a,model-b,model-c,model-new", loaded.Models, "clearing a key scope never shrinks the channel catalog")
}

func TestMultiKeyModelMappingFollowsStableKeyAndInheritsChannelRules(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	common.MemoryCacheEnabled = true
	channelMapping := `{"alias":"channel-upstream","shared":"channel-shared"}`
	keyMapping := `{"alias":"Key-Upstream"}`
	channel := &model.Channel{
		Name:         "multi-key-model-mapping",
		Type:         constant.ChannelTypeOpenAI,
		Key:          "mapping-one\nmapping-two\nmapping-three",
		Models:       "alias,shared",
		Group:        "default",
		Status:       common.ChannelStatusEnabled,
		ModelMapping: &channelMapping,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:            true,
			MultiKeySize:          3,
			MultiKeyMode:          constant.MultiKeyModeRandom,
			MultiKeyModelMappings: map[int]string{1: " { \n \"alias\": \"Key-Upstream\" \n } "},
			MultiKeyModels:        map[int][]string{1: {"Key-Upstream", "channel-shared"}},
			MultiKeyUsers:         map[int]int{2: rootID},
		},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })
	model.InitChannelCache()

	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, map[int]string{1: keyMapping}, loaded.ChannelInfo.MultiKeyModelMappings)
	entryMapping, err := model.ChannelKeyEntryByID(channel.Id, loaded.KeyEntryIDs[1])
	require.NoError(t, err)
	assert.Equal(t, keyMapping, entryMapping.ModelMapping, "persistence must use the normalized mapping, not only validate it")
	resolved, ok := loaded.RoutingUpstreamModelNameForKey(0, "alias")
	require.True(t, ok)
	assert.Equal(t, "channel-upstream", resolved)
	resolved, ok = loaded.RoutingUpstreamModelNameForKey(1, "alias")
	require.True(t, ok)
	assert.Equal(t, "Key-Upstream", resolved)
	resolved, ok = loaded.RoutingUpstreamModelNameForKey(1, "shared")
	require.True(t, ok)
	assert.Equal(t, "channel-shared", resolved, "unmatched source rules inherit from the channel")
	assert.True(t, loaded.KeyServesModel(1, "alias"), "scope matching uses the selected Key's upstream target")
	assert.NotContains(t, loaded.PublicModels(), "Key-Upstream", "mapping must not expose an upstream-only model identity")
	for _, tc := range []struct {
		index     int
		want      string
		automatic bool
	}{
		{index: 0, want: "channel-upstream"},
		{index: 1, want: "Key-Upstream"},
		{index: 1, want: "Key-Upstream", automatic: true},
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		if tc.automatic {
			require.Nil(t, middleware.SetupContextForSelectedChannelExcluding(ctx, loaded, "alias", []int64{loaded.KeyEntryIDs[0], loaded.KeyEntryIDs[2]}))
			assert.Equal(t, "mapping-two", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
		} else {
			require.Nil(t, middleware.SetupChannelTestContext(ctx, loaded, "alias", loaded.GetKeys()[tc.index], tc.index))
		}
		request := &dto.GeneralOpenAIRequest{Model: "alias"}
		info := &relaycommon.RelayInfo{OriginModelName: "alias", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "alias"}}
		require.NoError(t, helper.ModelMappedHelper(ctx, info, request))
		assert.Equal(t, tc.want, request.Model, "the selected-key context must send the exact effective upstream spelling")
		assert.Equal(t, "alias", info.OriginModelName, "the public authorization and billing identity stays unchanged")
	}

	list := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "get_key_status"})
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var status struct {
		Success bool                   `json:"success"`
		Data    MultiKeyStatusResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(list.Body.Bytes(), &status))
	require.True(t, status.Success, list.Body.String())
	require.Len(t, status.Data.Keys, 3)
	assert.Empty(t, status.Data.Keys[0].ModelMapping)
	assert.Equal(t, keyMapping, status.Data.Keys[1].ModelMapping)

	entryID := strconv.FormatInt(loaded.KeyEntryIDs[1], 10)
	revision := loaded.KeyEntryRevisions[1]
	for _, invalid := range []string{`[]`, `{"alias":1}`, `{" alias":"target"}`, `{"channel-upstream":"alias"}`} {
		rejected := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
			ChannelId: channel.Id, Action: "set_key_model_mapping", KeyEntryID: entryID,
			ExpectedRev: &revision, ModelMapping: &invalid,
		})
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(rejected.Body.Bytes(), &result))
		assert.False(t, result.Success, "invalid objects and cycles through inherited rules must not save")
		unchanged, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, revision, unchanged.KeyEntryRevisions[1])
		assert.Equal(t, keyMapping, unchanged.ChannelInfo.MultiKeyModelMappings[1])
	}
	updatedMapping := `{"alias":"key-updated"}`
	saved := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
		ChannelId: channel.Id, Action: "set_key_model_mapping", KeyEntryID: entryID,
		ExpectedRev: &revision, ModelMapping: &updatedMapping,
	})
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, map[int]string{1: updatedMapping}, loaded.ChannelInfo.MultiKeyModelMappings)
	assert.Equal(t, updatedMapping, loaded.ChannelInfo.MultiKeyModelMappings[1])
	stale := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
		ChannelId: channel.Id, Action: "set_key_model_mapping", KeyEntryID: entryID,
		ExpectedRev: &revision, ModelMapping: &keyMapping,
	})
	assert.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())

	listRevision := loaded.ChannelInfo.KeyEntryRevision
	reordered := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
		ChannelId: channel.Id, Action: "reorder_keys", ExpectedRevision: &listRevision,
		KeyEntryIDs: []string{loaded.KeyEntryIDStrings[1], loaded.KeyEntryIDStrings[2], loaded.KeyEntryIDStrings[0]},
	})
	require.Equal(t, http.StatusOK, reordered.Code, reordered.Body.String())
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "mapping-two\nmapping-three\nmapping-one", loaded.Key)
	assert.Equal(t, entryID, loaded.KeyEntryIDStrings[0])
	assert.Equal(t, map[int]string{0: updatedMapping}, loaded.ChannelInfo.MultiKeyModelMappings)
	cached, err := model.CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, loaded.ChannelInfo.MultiKeyModelMappings, cached.ChannelInfo.MultiKeyModelMappings)

	deleteID := strconv.FormatInt(loaded.KeyEntryIDs[2], 10)
	deleteRevision := loaded.KeyEntryRevisions[2]
	deleted := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
		ChannelId: channel.Id, Action: "delete_key", KeyEntryID: deleteID,
		ExpectedRev: &deleteRevision,
	})
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "mapping-two\nmapping-three", loaded.Key)
	assert.Equal(t, map[int]string{0: updatedMapping}, loaded.ChannelInfo.MultiKeyModelMappings)
	resolved, ok = loaded.RoutingUpstreamModelNameForKey(0, "alias")
	require.True(t, ok)
	assert.Equal(t, "key-updated", resolved)

	for _, clearValue := range []string{"{}", ""} {
		for _, value := range []string{keyMapping, clearValue} {
			revision := loaded.KeyEntryRevisions[0]
			response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{
				ChannelId: channel.Id, Action: "set_key_model_mapping", KeyEntryID: entryID,
				ExpectedRev: &revision, ModelMapping: &value,
			})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					KeyEntryID   string `json:"key_entry_id"`
					Rev          int64  `json:"rev"`
					ModelMapping string `json:"model_mapping"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.Equal(t, entryID, result.Data.KeyEntryID)
			loaded, err = model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, loaded.KeyEntryRevisions[0], result.Data.Rev)
			assert.Equal(t, loaded.ChannelInfo.MultiKeyModelMappings[0], result.Data.ModelMapping)
		}
		assert.Empty(t, loaded.ChannelInfo.MultiKeyModelMappings, "clearing the list restores channel inheritance")
		entry, err := model.ChannelKeyEntryByID(channel.Id, loaded.KeyEntryIDs[0])
		require.NoError(t, err)
		assert.Empty(t, entry.ModelMapping)
		cached, err := model.CacheGetChannel(channel.Id)
		require.NoError(t, err)
		resolved, ok := cached.RoutingUpstreamModelNameForKey(0, "alias")
		require.True(t, ok)
		assert.Equal(t, "channel-upstream", resolved)
		assert.Equal(t, channelMapping, cached.GetModelMapping())
		assert.Equal(t, rootID, loaded.ChannelInfo.MultiKeyUsers[1], "the neighboring allocation keeps its owner")
	}
}

// TestChannelAllocationsIdentityAndBudgetRouting exercises the authoritative
// allocation API with three equal credentials and distinct permissions.
func TestChannelAllocationsIdentityAndBudgetRouting(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())
	bob := &model.User{Username: "allocation-bob", Group: "other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "allocbob"}
	require.NoError(t, model.DB.Create(bob).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Unscoped().Delete(bob).Error) })
	selected := &model.Channel{Name: "allocation-equal-keys", Type: constant.ChannelTypeOpenAI, Key: "allocation-secret-shared\nallocation-secret-shared\nallocation-secret-shared", Models: "public-model,alice-model,bob-model", Group: "public", Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(1), ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModeRandom, MultiKeyUsers: map[int]int{1: rootID, 2: bob.Id}, MultiKeyModels: map[int][]string{0: {"public-model"}, 1: {"alice-model"}, 2: {"bob-model"}}}}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	readStatus := func() MultiKeyStatusResponse {
		response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status"})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var decoded struct {
			Success bool                   `json:"success"`
			Data    MultiKeyStatusResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &decoded))
		require.True(t, decoded.Success, response.Body.String())
		assert.NotContains(t, response.Body.String(), "allocation-secret-shared")
		return decoded.Data
	}
	list := readStatus()
	require.Len(t, list.Keys, 3)
	ids := []string{list.Keys[0].Id, list.Keys[1].Id, list.Keys[2].Id}
	assert.NotEqual(t, ids[0], ids[1])
	assert.NotEqual(t, ids[1], ids[2])
	for _, id := range ids {
		parsed, err := strconv.ParseInt(id, 10, 64)
		require.NoError(t, err)
		assert.Positive(t, parsed)
	}
	model.InitChannelCache()
	assert.Contains(t, model.GetUserBoundModels(rootID), "alice-model")
	assert.NotContains(t, model.GetUserBoundModels(bob.Id), "alice-model")
	_, index, apiErr := selected.GetNextEnabledKeyFor("alice-model", rootID)
	require.Nil(t, apiErr)
	assert.Equal(t, 1, index)
	_, _, apiErr = selected.GetNextEnabledKeyFor("alice-model", bob.Id)
	require.NotNil(t, apiErr)
	// Index-only and stale revisions cannot modify one of the equal rows.
	rejected := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "delete_key", KeyIndex: common.GetPointer(0)})
	require.Equal(t, http.StatusConflict, rejected.Code)
	order := []string{ids[2], ids[0], ids[1]}
	response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "reorder_keys", KeyEntryIDs: order, ExpectedRevision: &list.Revision})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	list = readStatus()
	require.Equal(t, order, []string{list.Keys[0].Id, list.Keys[1].Id, list.Keys[2].Id})
	assert.Equal(t, bob.Id, list.Keys[0].UserId)
	assert.Equal(t, rootID, list.Keys[2].UserId)
	loaded, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	aliceAllocation, ok := loaded.ChannelAllocationByPosition(2)
	require.True(t, ok)
	// A delayed observation follows the chosen stable ID, not the first equal key.
	require.True(t, model.UpdateChannelAllocationStatus(t.Context(), aliceAllocation, common.ChannelStatusAutoDisabled, "provider rejection"))
	list = readStatus()
	assert.Equal(t, common.ChannelStatusEnabled, list.Keys[0].Status)
	assert.Equal(t, common.ChannelStatusEnabled, list.Keys[1].Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, list.Keys[2].Status)
	stale := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_user", KeyEntryID: ids[1], ExpectedRev: &aliceAllocation.Revision, UserId: common.GetPointer(0)})
	require.Equal(t, http.StatusConflict, stale.Code)
	assert.False(t, model.UpdateChannelAllocationStatus(t.Context(), aliceAllocation, common.ChannelStatusEnabled, ""), "an old callback cannot undo newer state")
	// Recovery uses a current row revision and does not change sibling rows.
	response = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "enable_key", KeyEntryID: ids[1], ExpectedRev: &list.Keys[2].Rev})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	list = readStatus()
	alice := list.Keys[2]
	response = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_quota", KeyEntryID: alice.Id, ExpectedRev: &alice.Rev, ExpectedConfigVersion: common.GetPointer(int64(0)), Enabled: common.GetPointer(true), LimitQuota: common.GetPointer(int64(10))})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	loaded, err = model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	allocation, ok := loaded.ChannelAllocationByPosition(2)
	require.True(t, ok)
	hold, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{AttemptKey: "allocation-identity-charge", Selection: model.BudgetSelection{ChannelID: selected.Id, AllocationID: allocation.EntryID, AllocationRevision: allocation.Revision, UserID: rootID, Group: "other", ModelName: "alice-model"}, ReserveQuota: 6})
	require.NoError(t, err)
	require.NoError(t, model.SettleChannelBudgets(t.Context(), model.BudgetSettleRequest{ReservationID: hold.Id, Revision: 1, TotalActualQuota: 12, Final: true}))
	model.InitChannelCache()
	assert.NotContains(t, model.GetUserBoundModels(rootID), "alice-model")
	visible, err := model.ModelsAvailableToUserGroups(t.Context(), []string{"other"}, rootID)
	require.NoError(t, err)
	assert.False(t, visible["alice-model"])
	loaded, err = model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	_, _, apiErr = loaded.GetNextEnabledKeyFor("alice-model", rootID)
	require.NotNil(t, apiErr)
	// Reset only this entry. Used is zero, lifetime remains, old refunds cannot
	// make this new period negative and do not touch a sibling allocation.
	list = readStatus()
	alice = list.Keys[2]
	require.NotNil(t, alice.Quota)
	assert.EqualValues(t, 12, alice.Quota.PeriodUsedQuota)
	response = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "reset_key_quota", KeyEntryID: alice.Id, ExpectedRev: &alice.Rev, ExpectedConfigVersion: &alice.Quota.ConfigVersion, OperationID: "allocation-test-reset"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	list = readStatus()
	assert.Zero(t, list.Keys[2].Quota.PeriodUsedQuota)
	assert.EqualValues(t, 12, list.Keys[2].Quota.LifetimeUsedQuota)
	model.InitChannelCache()
	assert.Contains(t, model.GetUserBoundModels(rootID), "alice-model")
	response = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "delete_key", KeyEntryID: ids[0], ExpectedRev: &list.Keys[1].Rev})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	list = readStatus()
	require.Len(t, list.Keys, 2)
	assert.Equal(t, ids[2], list.Keys[0].Id)
	assert.Equal(t, ids[1], list.Keys[1].Id)
	var retired model.ChannelKeyEntry
	id, _ := strconv.ParseInt(ids[0], 10, 64)
	require.NoError(t, model.DB.First(&retired, id).Error)
	assert.Positive(t, retired.RetiredAt)
}

func TestChannelAllocationsDedupConservesUsageAndConflicts(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())
	selected := &model.Channel{Name: "allocation-dedup", Type: 1, Key: "shared-allocation\nshared-allocation\nshared-allocation", Models: "m", Group: "default", Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyMode: constant.MultiKeyModeRandom, MultiKeyUsers: map[int]int{2: rootID}}}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	for i, id := range selected.KeyEntryIDs {
		if i == 2 {
			continue
		}
		_, err := model.SetChannelBudgetLimit(t.Context(), model.BudgetScopeKey, id, 0, true, 100)
		require.NoError(t, err)
		r, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{AttemptKey: fmt.Sprintf("dedup-charge-%d", id), Selection: model.BudgetSelection{ChannelID: selected.Id, AllocationID: id, AllocationRevision: selected.KeyEntryRevisions[i], UserID: rootID, Group: "default", ModelName: "m"}, ReserveQuota: 10})
		require.NoError(t, err)
		require.NoError(t, model.SettleChannelBudgets(t.Context(), model.BudgetSettleRequest{ReservationID: r.Id, Revision: 1, TotalActualQuota: int64(10 + i), Final: true}))
	}
	previewResponse := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys"})
	var decoded struct {
		Success bool                `json:"success"`
		Data    channelKeyDedupPlan `json:"data"`
	}
	require.NoError(t, common.Unmarshal(previewResponse.Body.Bytes(), &decoded))
	require.True(t, decoded.Success, previewResponse.Body.String())
	require.Len(t, decoded.Data.Groups, 1)
	plan := decoded.Data
	assert.EqualValues(t, 21, plan.Groups[0].MergedPeriodUsed)
	require.Len(t, plan.Groups[0].EntryIDs, 2)
	stale := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys", Apply: true, ExpectedRevision: &plan.Revision, PreviewToken: "wrong"})
	assert.Equal(t, http.StatusConflict, stale.Code)
	applied := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys", Apply: true, ExpectedRevision: &plan.Revision, PreviewToken: plan.PreviewToken})
	require.Equal(t, http.StatusOK, applied.Code, applied.Body.String())
	require.Contains(t, applied.Body.String(), `"retired_entries":1`)
	loaded, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	require.Len(t, loaded.KeyEntryIDs, 2)
	assert.Equal(t, selected.KeyEntryIDs[0], loaded.KeyEntryIDs[0])
	assert.Equal(t, rootID, loaded.ChannelInfo.MultiKeyUsers[1])
	view, err := model.ReadChannelBudget(context.Background(), model.BudgetScopeKey, loaded.KeyEntryIDs[0])
	require.NoError(t, err)
	assert.EqualValues(t, 100, view.LimitQuota)
	assert.EqualValues(t, 21, view.PeriodUsedQuota)
	assert.EqualValues(t, 21, view.LifetimeUsedQuota)
	// Generic duplicate replacement must conflict instead of guessing survivor identity.
	_, err = model.UpdateChannelLocked(selected.Id, func(current *model.Channel) error { current.Key = "shared-allocation"; return nil })
	require.ErrorIs(t, err, model.ErrChannelKeyEntriesAmbiguous)
}

// A legacy task that persisted only credential text cannot prove which of two
// identical allocations it used, so every live allocation storing that text is
// skipped by deduplication until the task is terminal. A task holding a stable
// allocation id keeps its own allocation busy even though the ledger settled
// the reservation at submission.
func TestChannelAllocationsDedupSkipsRunningTasksWithoutAllocationId(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())
	const credential = "dedup-running-task-credential"
	selected := &model.Channel{Name: "allocation-dedup-tasks", Type: constant.ChannelTypeOpenAI, Key: credential + "\n" + credential, Models: "m", Group: "default", Status: common.ChannelStatusEnabled, Setting: common.GetPointer(`{"allow_duplicate_keys":true}`), ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom, MultiKeyUsers: map[int]int{0: rootID, 1: rootID}}}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	require.Len(t, selected.KeyEntryIDs, 2)
	first, second := selected.KeyEntryIDs[0], selected.KeyEntryIDs[1]
	// The task is persisted while its reservation is still pending, exactly as
	// the async submit path does, and only then does the ledger finalize the
	// charge; the task outlives both stages.
	reservation, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{
		AttemptKey: fmt.Sprintf("dedup-running-task-%d", second), ReserveQuota: 4,
		Selection: model.BudgetSelection{ChannelID: selected.Id, AllocationID: second, AllocationRevision: selected.KeyEntryRevisions[1], UserID: rootID, Group: "default", ModelName: "m"},
	})
	require.NoError(t, err)
	stored, err := model.ReadChannelBudgetReservation(t.Context(), reservation.Id)
	require.NoError(t, err)
	require.Equal(t, model.BudgetReservationPhasePending, stored.Phase)
	task := model.Task{TaskID: "dedup-running-task", ChannelId: selected.Id, UserId: rootID, Quota: 4, Status: model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{Key: credential, BudgetReservationId: reservation.Id, AllocationId: second}}
	require.NoError(t, task.InsertWithContext(t.Context()))
	t.Cleanup(func() { require.NoError(t, model.DB.Unscoped().Delete(&task).Error) })
	require.NoError(t, model.SettleChannelBudgets(t.Context(), model.BudgetSettleRequest{ReservationID: reservation.Id, Revision: 1, TotalActualQuota: 4, Final: true, TaskID: task.TaskID}))
	stored, err = model.ReadChannelBudgetReservation(t.Context(), reservation.Id)
	require.NoError(t, err)
	require.Equal(t, model.BudgetReservationPhaseSettled, stored.Phase)
	require.Equal(t, task.TaskID, stored.TaskId)
	var budget model.ChannelBudgetAccount
	require.NoError(t, model.DB.Where("scope_type = ? AND scope_id = ?", model.BudgetScopeKey, second).First(&budget).Error)
	assert.EqualValues(t, 4, budget.PeriodUsedQuota)
	assert.Zero(t, budget.ReservedQuota)

	readStatus := func() MultiKeyStatusResponse {
		response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status"})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var decoded struct {
			Success bool                   `json:"success"`
			Data    MultiKeyStatusResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &decoded))
		require.True(t, decoded.Success, response.Body.String())
		return decoded.Data
	}
	// assertSkipped proves through the management API that live allocations stay
	// protected while a legacy task could still be running on them, and that
	// applying a preview never retires a protected row.
	assertSkipped := func(t *testing.T, ambiguousAllocationIDs ...int64) channelKeyDedupPlan {
		t.Helper()
		response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys"})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var decoded struct {
			Success bool                `json:"success"`
			Data    channelKeyDedupPlan `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &decoded))
		require.True(t, decoded.Success, response.Body.String())
		assert.NotContains(t, response.Body.String(), credential)
		require.Len(t, decoded.Data.Groups, 1, "equal credentials still compare as one dedup group")
		skipped := map[string]bool{}
		for _, entry := range decoded.Data.Groups[0].Skipped {
			skipped[entry.ID] = true
		}
		for _, id := range ambiguousAllocationIDs {
			assert.True(t, skipped[strconv.FormatInt(id, 10)], "allocation %d may still hold a running task; got %+v", id, decoded.Data.Groups[0].Skipped)
		}
		before := readStatus()
		applied := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys", Apply: true, ExpectedRevision: &decoded.Data.Revision, PreviewToken: decoded.Data.PreviewToken})
		require.Equal(t, http.StatusOK, applied.Code, applied.Body.String())
		loaded, err := model.GetChannelById(selected.Id, true)
		require.NoError(t, err)
		after := readStatus()
		assert.Len(t, after.Keys, len(before.Keys), "apply never shrinks the list below the protected rows")
		for _, id := range ambiguousAllocationIDs {
			assert.Contains(t, loaded.KeyEntryIDs, id, "protected allocation %d survives", id)
		}
		return decoded.Data
	}

	// The stable allocation id keeps only its own allocation busy; the settled
	// reservation no longer holds quota but the running task still protects it.
	assertSkipped(t, second)

	// A legacy task without an allocation id cannot be attributed: every live
	// allocation with the identical credential stays protected.
	task.PrivateData.AllocationId = 0
	require.NoError(t, model.DB.Model(&task).Update("private_data", task.PrivateData).Error)
	assertSkipped(t, first, second)

	// Reordering does not let the ambiguity resolve itself; the apply above
	// advanced the channel revision, so the next actions read a fresh one.
	list := readStatus()
	require.Len(t, list.Keys, 2)
	assertSkipped(t, first, second)

	// Appending a third identical credential must not transfer attribution to
	// the newest row. The append is proven by the third row itself: the
	// management actions above leave `key` unchanged, so a lost or duplicated
	// append can never be mistaken for the expected three rows.
	list = readStatus()
	appended := putUpdateChannel(t, rootID, common.RoleRootUser, fmt.Sprintf(`{"id":%d,"type":%d,"name":%q,"key":%q,"models":"m","group":"default","key_mode":"append","multi_key_mode":"random"}`, selected.Id, selected.Type, selected.Name, credential))
	require.Equal(t, http.StatusOK, appended.Code, appended.Body.String())
	var appendedResult struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(appended.Body.Bytes(), &appendedResult))
	require.True(t, appendedResult.Success, appended.Body.String())
	afterAppend := readStatus()
	require.Len(t, afterAppend.Keys, 3, "the append adds exactly one allocation")
	newest, err := strconv.ParseInt(afterAppend.Keys[2].Id, 10, 64)
	require.NoError(t, err, "rows: %+v", afterAppend.Keys)
	assert.NotContains(t, []int64{first, second}, newest)
	// Deduplication compares the owner too: an appended row is public, so bind it
	// to the same user as its identical siblings with a fresh row revision before
	// the group can be expected to hold three entries.
	assert.Zero(t, afterAppend.Keys[2].UserId)
	bind := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_user", KeyEntryID: afterAppend.Keys[2].Id, ExpectedRev: &afterAppend.Keys[2].Rev, UserId: common.GetPointer(rootID)})
	require.Equal(t, http.StatusOK, bind.Code, bind.Body.String())
	afterAppend = readStatus()
	require.Len(t, afterAppend.Keys, 3)
	assert.Equal(t, rootID, afterAppend.Keys[2].UserId)
	assertSkipped(t, first, second, newest)

	// Only a terminal task releases the ambiguity.
	require.NoError(t, model.DB.Model(&task).Update("status", model.TaskStatusSuccess).Error)
	response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var resolved struct {
		Success bool                `json:"success"`
		Data    channelKeyDedupPlan `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &resolved))
	require.True(t, resolved.Success, response.Body.String())
	require.Len(t, resolved.Data.Groups, 1)
	assert.Empty(t, resolved.Data.Groups[0].Skipped)
	require.Len(t, resolved.Data.Groups[0].EntryIDs, 3)
	applied := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "deduplicate_keys", Apply: true, ExpectedRevision: &resolved.Data.Revision, PreviewToken: resolved.Data.PreviewToken})
	require.Equal(t, http.StatusOK, applied.Code, applied.Body.String())
	loaded, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	require.Len(t, loaded.KeyEntryIDs, 1, "only the survivor keeps the credential")
	assert.Equal(t, first, loaded.KeyEntryIDs[0], "the smallest id survives across reorders and appends")
	view, err := model.ReadChannelBudget(context.Background(), model.BudgetScopeKey, first)
	require.NoError(t, err)
	assert.EqualValues(t, 4, view.PeriodUsedQuota, "the settled usage is conserved on the survivor")
}

// Routing eligibility accepts the routing alias of a requested model in the
// channel list, a key allowlist and a group whitelist, while an unrestricted
// key keeps serving every channel model. The alias resolution itself is proven
// against ratio_setting.RoutingMatchModelName, never assumed.
func TestChannelAllocationsRoutingAliasScope(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())
	const aliased = "qwen3-max@thinking:on@temperature:0.2"
	const normalized = "qwen3-max"
	require.Equal(t, normalized, ratio_setting.RoutingMatchModelName(aliased))
	// The group whitelist holds both the normalized name and the plain model, so
	// neither case below can pass merely because the group filter was skipped.
	const plainModel = "plain-model"
	otherSettings, err := common.Marshal(&dto.ChannelOtherSettings{GroupModels: map[string][]string{"default": {normalized, plainModel}}})
	require.NoError(t, err)
	// Both storage styles must admit the request alias: a channel that lists the
	// normalized name and a channel that lists the alias itself. The Key
	// allowlist stores the normalized name in both cases, and the group whitelist
	// holds the normalized name as well.
	for _, listedModels := range []string{normalized + ",plain-model", aliased + ",plain-model"} {
		t.Run(listedModels, func(t *testing.T) {
			selected := &model.Channel{Name: "allocation-routing-alias " + listedModels, Type: constant.ChannelTypeOpenAI, Key: "alias-one\nalias-two", Models: listedModels, Group: "default", Status: common.ChannelStatusEnabled, OtherSettings: string(otherSettings),
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom, MultiKeyModels: map[int][]string{0: {normalized}}}}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			require.Len(t, selected.KeyEntryIDs, 2)
			reserve := func(name string, position int, modelName string) error {
				_, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{
					AttemptKey: fmt.Sprintf("alias-%s-%d-%d", name, selected.Id, position), ReserveQuota: 1,
					Selection: model.BudgetSelection{ChannelID: selected.Id, AllocationID: selected.KeyEntryIDs[position], AllocationRevision: selected.KeyEntryRevisions[position], UserID: rootID, Group: "default", ModelName: modelName},
				})
				return err
			}
			assert.Equal(t, strings.Contains(listedModels, aliased), slices.Contains(selected.GetModels(), aliased))
			// The aliased request is admitted in both storage styles: a channel that
			// lists the normalized name resolves it through RoutingMatchModelName, and
			// a channel that lists the alias matches it exactly. The same request is
			// then admitted by the unrestricted Key, by the Key allowlist that stores
			// the normalized name, and by the group whitelist holding that name.
			require.NoError(t, reserve("channel-scope-alias", 1, aliased), "channel models %q", listedModels)
			require.NoError(t, reserve("allowlist-alias", 0, aliased))
			// Outside every scope: a model the channel does not list is refused.
			require.ErrorIs(t, reserve("unknown-model", 1, "unlisted-model"), model.ErrBudgetAllocationInvalid)
			// A channel model outside the Key allowlist is refused for that Key.
			require.ErrorIs(t, reserve("allowlist-plain", 0, plainModel), model.ErrBudgetAllocationInvalid)
			// An unrestricted Key serves every channel model the group also allows.
			require.NoError(t, reserve("all-plain", 1, plainModel))
		})
	}
}

func TestChannelAllocationsMappedModelScope(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	previousBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	t.Setenv("BATCH_UPDATE_ENABLED", "false")
	require.NoError(t, model.InitializeChannelBudgets())
	const alias = "deepseek-r1"
	const target = "DeepSeek-R1"
	const unrelated = "other-channel-model"
	for _, cacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cacheEnabled), func(t *testing.T) {
			common.MemoryCacheEnabled = cacheEnabled
			group := fmt.Sprintf("mapped-scope-%t", cacheEnabled)
			upstreamGroup := group + "-upstream"
			settings, err := common.Marshal(&dto.ChannelOtherSettings{GroupModels: map[string][]string{group: {alias, unrelated}, upstreamGroup: {target}}})
			require.NoError(t, err)
			channel := &model.Channel{Name: t.Name(), Type: constant.ChannelTypeOpenAI, Key: "denied-key\nmapped-key", Models: alias + "," + unrelated, Group: group + "," + upstreamGroup,
				Status: common.ChannelStatusEnabled, ModelMapping: common.GetPointer(`{"deepseek-r1":"DeepSeek-R1"}`), OtherSettings: string(settings),
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
					MultiKeyScopeModes: map[int]string{0: "allowlist", 1: "allowlist"}, MultiKeyModels: map[int][]string{1: {target}}}}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() { require.NoError(t, channel.Delete()) })
			model.InitChannelCache()
			routed, err := model.GetRandomSatisfiedChannel(group, alias, 0, nil)
			require.NoError(t, err)
			require.NotNil(t, routed, "the mapped request alias must retain its public route")
			assert.Equal(t, channel.Id, routed.Id)
			visible, err := model.ModelsAvailableToUserGroups(t.Context(), []string{group}, rootID)
			require.NoError(t, err)
			assert.True(t, visible[alias])
			assert.False(t, visible[target], "the upstream target is not a client-facing channel model")
			assert.False(t, visible[unrelated], "the mapped Key still cannot serve an unrelated channel model")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Set("id", rootID)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, routed, alias))
			assert.Equal(t, "mapped-key", common.GetContextKeyString(c, constant.ContextKeyChannelKey))
			assert.Equal(t, alias, common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
			allocation, ok := routed.ChannelAllocationByPosition(1)
			require.True(t, ok)
			selection := model.BudgetSelection{ChannelID: routed.Id, AllocationID: allocation.EntryID, AllocationRevision: allocation.Revision,
				UserID: rootID, Group: group, ModelName: alias}
			hold, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{
				AttemptKey: fmt.Sprintf("mapped-scope-%d", routed.Id), Selection: selection, ReserveQuota: 1,
			})
			require.NoError(t, err, "authoritative budget admission must accept the same mapped Key capability")
			require.NotNil(t, hold)
			request := &dto.GeneralOpenAIRequest{Model: alias}
			info := &relaycommon.RelayInfo{OriginModelName: alias, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: alias}}
			require.NoError(t, helper.ModelMappedHelper(c, info, request))
			encoded, err := common.Marshal(request)
			require.NoError(t, err)
			var outgoing struct {
				Model string `json:"model"`
			}
			require.NoError(t, common.Unmarshal(encoded, &outgoing))
			assert.Equal(t, target, outgoing.Model)
			assert.Equal(t, alias, info.OriginModelName, "authorization and billing retain the requested identity")
			require.NoError(t, model.SettleChannelBudgets(t.Context(), model.BudgetSettleRequest{ReservationID: hold.Id, Revision: 1, Final: true}))

			for _, denied := range []string{"empty allowlist", "upstream-only model", "unrelated model", "different group", "group allows only upstream name"} {
				t.Run(denied, func(t *testing.T) {
					invalid := selection
					switch denied {
					case "empty allowlist":
						invalid.AllocationID = routed.KeyEntryIDs[0]
						invalid.AllocationRevision = routed.KeyEntryRevisions[0]
					case "upstream-only model":
						invalid.ModelName = target
					case "unrelated model":
						invalid.ModelName = unrelated
					case "different group":
						invalid.Group = "not-authorized"
					case "group allows only upstream name":
						invalid.Group = upstreamGroup
					}
					_, err := model.ReserveChannelBudgets(t.Context(), model.BudgetReserveRequest{
						AttemptKey: fmt.Sprintf("mapped-denied-%d-%s", routed.Id, denied), Selection: invalid, ReserveQuota: 1,
					})
					require.ErrorIs(t, err, model.ErrBudgetAllocationInvalid)
				})
			}
		})
	}
}

func TestMultiKeyThreeRowModelScopeEditUsesFreshRevisions(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	selected := &model.Channel{Name: "multi-key-three-row-scope", Type: constant.ChannelTypeOpenAI, Key: "scope-one\nscope-two\nscope-three", Models: "manual-model,model-b,model-a,model-c", Group: "default", Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels: map[int][]string{1: {"model-b"}}, MultiKeyUsers: map[int]int{1: rootID},
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	require.Len(t, selected.KeyEntryIDs, 3)
	accounts := []model.ChannelBudgetAccount{
		{ScopeType: model.BudgetScopeKey, ScopeId: selected.KeyEntryIDs[1], ChannelId: selected.Id, Enabled: true, LimitQuota: 100, PeriodUsedQuota: 25, ReservedQuota: 5, ConfigVersion: 2},
		{ScopeType: model.BudgetScopeKey, ScopeId: selected.KeyEntryIDs[2], ChannelId: selected.Id, Enabled: true, LimitQuota: 250, PeriodUsedQuota: 40, ReservedQuota: 10, ConfigVersion: 3},
	}
	require.NoError(t, model.DB.Create(&accounts).Error)
	model.InitChannelCache()

	list := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status"})
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var decoded struct {
		Success bool                   `json:"success"`
		Data    MultiKeyStatusResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(list.Body.Bytes(), &decoded))
	require.True(t, decoded.Success, list.Body.String())
	require.Len(t, decoded.Data.Keys, 3)
	originalList := decoded.Data
	third := originalList.Keys[2]
	assert.EqualValues(t, 1, third.Rev)

	allow := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_models", KeyEntryID: third.Id, ExpectedRev: &third.Rev, ExpectedRevision: &originalList.Revision, Models: []string{"model-c", " model-new-a ", "model-new-z", "model-new-a", " "}, ScopeMode: common.GetPointer("allowlist")})
	require.Equal(t, http.StatusOK, allow.Code, allow.Body.String())
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Revision      int64    `json:"revision"`
			ChannelModels []string `json:"channel_models"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(allow.Body.Bytes(), &result))
	require.True(t, result.Success, allow.Body.String())
	wantModels := []string{"manual-model", "model-b", "model-a", "model-c", "model-new-a", "model-new-z"}
	assert.Equal(t, wantModels, result.Data.ChannelModels)
	loaded, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	assert.Equal(t, strings.Join(wantModels, ","), loaded.Models)
	assert.Equal(t, map[int][]string{1: {"model-b"}, 2: {"model-c", "model-new-a", "model-new-z"}}, loaded.ChannelInfo.MultiKeyModels)
	assert.Equal(t, "allowlist", loaded.ChannelInfo.MultiKeyScopeModes[2])
	assert.Equal(t, map[int]int{1: rootID}, loaded.ChannelInfo.MultiKeyUsers)
	assert.Equal(t, map[int]int{1: common.ChannelStatusManuallyDisabled}, loaded.ChannelInfo.MultiKeyStatusList)
	assert.Equal(t, selected.KeyEntryIDs, loaded.KeyEntryIDs)
	assert.Equal(t, []int64{originalList.Keys[0].Rev, originalList.Keys[1].Rev, third.Rev + 1}, loaded.KeyEntryRevisions)
	assert.Equal(t, originalList.Revision+1, loaded.ChannelInfo.KeyEntryRevision)
	assert.Equal(t, loaded.ChannelInfo.KeyEntryRevision, result.Data.Revision)
	assert.True(t, loaded.KeyServesModel(0, "model-new-a"), "an unchanged all-model key inherits the expanded catalog")
	assert.False(t, loaded.KeyServesModel(1, "model-new-a"), "a sibling allowlist must not expand")

	var entries []model.ChannelKeyEntry
	require.NoError(t, model.DB.Where("channel_id = ? AND retired_at = 0", selected.Id).Order("position").Find(&entries).Error)
	require.Len(t, entries, 3)
	assert.Equal(t, []string{"model-c", "model-new-a", "model-new-z"}, entries[2].KeyModels())
	var storedAccounts []model.ChannelBudgetAccount
	require.NoError(t, model.DB.Where("channel_id = ?", selected.Id).Order("scope_id").Find(&storedAccounts).Error)
	assert.Equal(t, accounts, storedAccounts, "a model scope edit preserves selected and sibling quota state")
	var abilities []model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", selected.Id).Order("model").Find(&abilities).Error)
	abilityModels := make([]string, len(abilities))
	for i, ability := range abilities {
		abilityModels[i] = ability.Model
		assert.True(t, ability.Enabled)
		assert.Equal(t, "default", ability.Group)
	}
	assert.ElementsMatch(t, wantModels, abilityModels)
	cached, err := model.CacheGetChannel(selected.Id)
	require.NoError(t, err)
	assert.Equal(t, loaded.Models, cached.Models)
	assert.Equal(t, loaded.ChannelInfo, cached.ChannelInfo)
	assert.True(t, model.IsChannelEnabledForGroupModel("default", "model-new-a", selected.Id), "the committed catalog must be routable without a full reload")

	var audits []model.AuditEventOutbox
	require.NoError(t, model.DB.Where("action = ?", "channel.multi_key_manage").Order("id").Find(&audits).Error)
	require.Len(t, audits, 1, "one successful scope save enqueues exactly one audit")
	assert.True(t, audits[0].Success)
	assert.Equal(t, rootID, audits[0].ActorUserId)
	var auditParams struct {
		ID       int    `json:"id"`
		Action   string `json:"action"`
		EntryID  string `json:"entry_id"`
		Revision int64  `json:"revision"`
	}
	require.NoError(t, common.UnmarshalJsonStr(audits[0].ParamsJson, &auditParams))
	assert.Equal(t, selected.Id, auditParams.ID)
	assert.Equal(t, "set_key_models", auditParams.Action)
	assert.Equal(t, third.Id, auditParams.EntryID)
	assert.Equal(t, loaded.ChannelInfo.KeyEntryRevision, auditParams.Revision)

	list = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status"})
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	require.NoError(t, common.Unmarshal(list.Body.Bytes(), &decoded))
	require.True(t, decoded.Success, list.Body.String())
	require.Len(t, decoded.Data.Keys, 3)
	fresh := decoded.Data.Keys[2]
	assert.Equal(t, third.Rev+1, fresh.Rev)
	assert.Equal(t, originalList.Revision+1, decoded.Data.Revision)

	for _, tc := range []struct {
		name         string
		entryRev     int64
		listRev      int64
		scope        string
		failureTable string
		status       int
	}{
		{name: "stale selected key", entryRev: third.Rev, listRev: decoded.Data.Revision, scope: "allowlist", status: http.StatusConflict},
		{name: "stale channel list", entryRev: fresh.Rev, listRev: originalList.Revision, scope: "allowlist", status: http.StatusConflict},
		{name: "invalid scope", entryRev: fresh.Rev, listRev: decoded.Data.Revision, scope: "invalid", status: http.StatusOK},
		{name: "audit enqueue failure", entryRev: fresh.Rev, listRev: decoded.Data.Revision, scope: "allowlist", failureTable: "audit_event_outbox", status: http.StatusOK},
		{name: "ability write failure", entryRev: fresh.Rev, listRev: decoded.Data.Revision, scope: "allowlist", failureTable: "abilities", status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injected := false
			if tc.failureTable != "" {
				const callback = "test:key-model-save-failure"
				database := model.DB
				require.NoError(t, database.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == tc.failureTable {
						injected = true
						_ = tx.AddError(gorm.ErrInvalidDB)
					}
				}))
				t.Cleanup(func() { require.NoError(t, database.Callback().Create().Remove(callback)) })
			}
			rejected := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_models", KeyEntryID: third.Id, ExpectedRev: &tc.entryRev, ExpectedRevision: &tc.listRev, Models: []string{"model-rejected-addition"}, ScopeMode: &tc.scope})
			require.Equal(t, tc.status, rejected.Code, rejected.Body.String())
			var failure struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(rejected.Body.Bytes(), &failure))
			assert.False(t, failure.Success, rejected.Body.String())
			assert.Equal(t, tc.failureTable != "", injected, "the requested persistence boundary must reject the write")
			stored, err := model.GetChannelById(selected.Id, true)
			require.NoError(t, err)
			assert.Equal(t, loaded.Models, stored.Models, "a rejected save cannot append to the catalog")
			assert.Equal(t, loaded.ChannelInfo, stored.ChannelInfo)
			assert.Equal(t, loaded.KeyEntryRevisions, stored.KeyEntryRevisions)
			var storedEntries []model.ChannelKeyEntry
			require.NoError(t, model.DB.Where("channel_id = ? AND retired_at = 0", selected.Id).Order("position").Find(&storedEntries).Error)
			assert.Equal(t, entries, storedEntries)
			var storedAccounts []model.ChannelBudgetAccount
			require.NoError(t, model.DB.Where("channel_id = ?", selected.Id).Order("scope_id").Find(&storedAccounts).Error)
			assert.Equal(t, accounts, storedAccounts)
			var storedAbilities []model.Ability
			require.NoError(t, model.DB.Where("channel_id = ?", selected.Id).Order("model").Find(&storedAbilities).Error)
			assert.Equal(t, abilities, storedAbilities)
			var storedAudits []model.AuditEventOutbox
			require.NoError(t, model.DB.Where("action = ?", "channel.multi_key_manage").Order("id").Find(&storedAudits).Error)
			assert.Equal(t, audits, storedAudits, "rejected writes leave no new successful audit, including after a later ability failure")
			cached, err := model.CacheGetChannel(selected.Id)
			require.NoError(t, err)
			assert.Equal(t, loaded.Models, cached.Models)
			assert.Equal(t, loaded.ChannelInfo, cached.ChannelInfo)
			assert.False(t, model.IsChannelEnabledForGroupModel("default", "model-rejected-addition", selected.Id))
		})
	}

	deny := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "set_key_models", KeyEntryID: fresh.Id, ExpectedRev: &fresh.Rev, ExpectedRevision: &decoded.Data.Revision, Models: []string{}, ScopeMode: common.GetPointer("allowlist")})
	require.Equal(t, http.StatusOK, deny.Code, deny.Body.String())
	result.Success = false
	require.NoError(t, common.Unmarshal(deny.Body.Bytes(), &result))
	require.True(t, result.Success, deny.Body.String())
	assert.Equal(t, wantModels, result.Data.ChannelModels)
	stored, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	assert.Equal(t, loaded.Models, stored.Models, "an empty explicit allowlist must not shrink the catalog")
	assert.Equal(t, "allowlist", stored.ChannelInfo.MultiKeyScopeModes[2])
	assert.Empty(t, stored.ChannelInfo.MultiKeyModels[2])
	assert.True(t, stored.KeyServesModel(0, "model-new-a"))
	assert.False(t, stored.KeyServesModel(2, "model-new-a"))
	assert.Equal(t, fresh.Rev+1, stored.KeyEntryRevisions[2])
	assert.Equal(t, loaded.KeyEntryRevisions[:2], stored.KeyEntryRevisions[:2])
	assert.Equal(t, loaded.ChannelInfo.MultiKeyUsers, stored.ChannelInfo.MultiKeyUsers)
	assert.Equal(t, loaded.ChannelInfo.MultiKeyStatusList, stored.ChannelInfo.MultiKeyStatusList)
	assert.Equal(t, decoded.Data.Revision+1, result.Data.Revision)
	assert.Equal(t, result.Data.Revision, stored.ChannelInfo.KeyEntryRevision)
	storedAccounts = nil
	require.NoError(t, model.DB.Where("channel_id = ?", selected.Id).Order("scope_id").Find(&storedAccounts).Error)
	assert.Equal(t, accounts, storedAccounts)
	cached, err = model.CacheGetChannel(selected.Id)
	require.NoError(t, err)
	assert.Equal(t, stored.Models, cached.Models)
	assert.False(t, cached.KeyServesModel(2, "model-new-a"))
	audits = nil
	require.NoError(t, model.DB.Where("action = ?", "channel.multi_key_manage").Order("id").Find(&audits).Error)
	require.Len(t, audits, 2, "only the additive save and empty-allowlist save enqueue audits")
	for _, audit := range audits {
		assert.True(t, audit.Success)
		encoded, err := common.Marshal(audit)
		require.NoError(t, err)
		for _, credential := range selected.GetKeys() {
			assert.NotContains(t, string(encoded), credential)
		}
	}
}

func TestUpdateChannelKeepsCurrentKeyState(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, tc := range []struct {
		name       string
		reorder    bool
		changeMode bool
		concurrent bool
	}{
		{name: "reordered keys retain scopes and bindings", reorder: true},
		{name: "configuration preserves a concurrent metadata edit", concurrent: true},
		{name: "explicit mode preserves a concurrent metadata edit", changeMode: true, concurrent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &model.Channel{Name: "key-state-edit", Type: constant.ChannelTypeOpenAI,
				Key: "sk-first\nsk-second", Models: "model-a,model-b", Group: "default",
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
					MultiKeyModels: map[int][]string{0: {"model-a"}, 1: {"model-b"}}, MultiKeyUsers: map[int]int{1: rootID}},
			}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() { require.NoError(t, channel.Delete()) })
			body := map[string]any{"id": channel.Id, "name": "renamed-key-state", "type": channel.Type,
				"models": channel.Models, "group": channel.Group}
			if tc.reorder {
				body["key"], body["key_mode"] = "sk-second\nsk-first", "replace"
			}
			if tc.changeMode {
				body["multi_key_mode"] = "polling"
			}
			injected := false
			if tc.concurrent {
				// Commit another writer's metadata after the handler captures its
				// original projected row, before the atomic edit acquires its lock.
				// Injecting inside the edit's UPDATE would open a second transaction
				// while the first already owns the row (or SQLite's writer lock).
				const callback = "test:channel-edit-current-key-state"
				require.NoError(t, model.DB.Callback().Query().After("gorm:after_query").Register(callback, func(tx *gorm.DB) {
					editing, ok := tx.Statement.Dest.(*model.Channel)
					if !ok || editing.Id != channel.Id || injected {
						return
					}
					injected = true
					_, err := model.UpdateChannelLocked(channel.Id, func(stored *model.Channel) error {
						stored.ChannelInfo.MultiKeyModels = map[int][]string{1: {"model-a"}}
						stored.ChannelInfo.MultiKeyUsers = map[int]int{0: rootID}
						stored.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
						stored.ChannelInfo.MultiKeyPollingIndex = 1
						return nil
					})
					if err != nil {
						tx.AddError(err)
					}
				}))
				t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
			}
			payload, err := common.Marshal(body)
			require.NoError(t, err)
			response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
			var result struct {
				Success bool          `json:"success"`
				Data    model.Channel `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			loaded, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, "renamed-key-state", loaded.Name)
			assert.Equal(t, 2, loaded.ChannelInfo.MultiKeySize)
			if tc.reorder {
				assert.Equal(t, "sk-second\nsk-first", loaded.Key)
				assert.Equal(t, map[int][]string{0: {"model-b"}, 1: {"model-a"}}, loaded.ChannelInfo.MultiKeyModels)
				assert.Equal(t, map[int]int{0: rootID}, loaded.ChannelInfo.MultiKeyUsers)
				key, index, apiErr := loaded.GetNextEnabledKeyFor("model-b", rootID)
				require.Nil(t, apiErr)
				assert.Equal(t, "sk-second", key)
				assert.Zero(t, index)
			}
			if tc.concurrent {
				require.True(t, injected)
				assert.Equal(t, map[int][]string{1: {"model-a"}}, loaded.ChannelInfo.MultiKeyModels)
				assert.Equal(t, map[int]int{0: rootID}, loaded.ChannelInfo.MultiKeyUsers)
				assert.Equal(t, map[int]int{0: common.ChannelStatusManuallyDisabled}, loaded.ChannelInfo.MultiKeyStatusList)
				assert.Equal(t, 1, loaded.ChannelInfo.MultiKeyPollingIndex)
			}
			mode := constant.MultiKeyModeRandom
			if tc.changeMode {
				mode = constant.MultiKeyModePolling
			}
			assert.Equal(t, mode, loaded.ChannelInfo.MultiKeyMode)
			assert.Equal(t, loaded.ChannelInfo, result.Data.ChannelInfo)
		})
	}
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
	setupChannelCreateTest(t)
	database := model.DB
	// Distribution reads the channel cache, so the fixture keeps it enabled.
	common.MemoryCacheEnabled = true

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
	// The shared fixture reuses one database, so a row left by an earlier
	// interrupted run must not decide this test's channel ids.
	for _, channel := range channels {
		require.NoError(t, channel.Delete())
		require.NoError(t, database.Create(channel).Error)
		require.NoError(t, channel.AddAbilities(database))
		t.Cleanup(func() { require.NoError(t, channel.Delete()) })
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

// TestMultiKeyStatusKeyPreview pins the masked preview of every key: a short key
// keeps at most its tail, long keys show an identifying prefix and suffix, and
// structured credentials expose only approved identifiers, never secret fields.
func TestMultiKeyStatusKeyPreview(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "short", key: "sk1234567", want: "…67"},
		{name: "short auth in compound", key: "abc|integration-app-id", want: "…"},
		{name: "baidu client credentials", key: "client-id|client-secret", want: "…"},
		{name: "one character", key: "x", want: "…"},
		{name: "two characters", key: "yz", want: "…"},
		{name: "three characters", key: "qrs", want: "…rs"},
		{name: "sk threshold", key: "sk-abcdefghijklmnop", want: "sk-abcd…mnop"},
		{name: "sk below threshold", key: "sk-abcdefghijklmno", want: "…no"},
		{name: "plain threshold", key: "abcd12345678wxyz", want: "abcd…wxyz"},
		{name: "plain below threshold", key: "abcd1234567wxyz", want: "…yz"},
		{name: "unicode threshold", key: "甲乙丙丁一二三四五六七八天地玄黄", want: "甲乙丙丁…天地玄黄"},
		{name: "email", key: `{"type":"service_account","client_email":"svc@example.iam.gserviceaccount.com","private_key":"private-key-secret"}`, want: "svc@example.iam.gserviceaccount.com"},
		{name: "project", key: `{"type":"service_account","project_id":"vertex-project","private_key":"private-key-secret"}`, want: "vertex-project"},
		{name: "unknown object", key: `{"access_token":"unknown-object-secret"}`, want: "…"},
		{name: "malformed object", key: `{"access_token":"malformed-secret"`, want: "…"},
		{name: "array", key: `["array-secret"]`, want: "…"},
		{name: "quoted string", key: `"quoted-secret"`, want: "…"},
		{name: "plugin label", key: `{"type":"plugin_credential","plugin":"preview-fixture","label":"demo account","account_id":"account-1","data":{"token":"plugin-data-secret"},"headers":{"Authorization":"Bearer plugin-header-secret"},"query":{"token":"plugin-query-secret"}}`, want: "demo account"},
		{name: "plugin account", key: `{"type":"plugin_credential","plugin":"preview-fixture","account_id":"account-2","data":{"token":"plugin-data-secret"}}`, want: "account-2"},
	}
	keys := make([]string, len(cases))
	for i, tc := range cases {
		keys[i] = tc.key
	}
	channel := &model.Channel{Name: "multi-key-preview", Type: constant.ChannelTypeVertexAi, Other: `{"default":"us-central1"}`,
		Models: "model-a", Group: "default",
		Key:         strings.Join(keys, "\n"),
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: len(keys), MultiKeyMode: constant.MultiKeyModeRandom},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "get_key_status", Page: 1, PageSize: 50})
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Keys []KeyStatus `json:"keys"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Success, recorder.Body.String())
	require.Len(t, result.Data.Keys, len(cases))
	for i, tc := range cases {
		preview := result.Data.Keys[i].KeyPreview
		assert.Equal(t, tc.want, preview, tc.name)
		assert.NotContains(t, preview, tc.key, "the preview must never expose a complete credential: %s", tc.name)
		assert.LessOrEqual(t, len([]rune(preview)), 40)
	}
	body := recorder.Body.String()
	for _, secret := range []string{"sk1234567", "sk-abcdefghijklmnop", "abc|integration-app-id", "client-id|client-secret", "private_key", "private-key-secret", "unknown-object-secret", "malformed-secret", "array-secret", "quoted-secret", "plugin-data-secret", "plugin-header-secret", "plugin-query-secret"} {
		assert.NotContains(t, body, secret, "the response must not carry credentials or structured secret fields")
	}
	assert.NotContains(t, result.Data.Keys[1].KeyPreview, "abc", "the complete short authentication component must remain hidden")
	assert.Equal(t, "…", maskKeyPreview("-----BEGIN PRIVATE KEY-----\nprivate-key-secret\n-----END PRIVATE KEY-----"), "a multiline credential must not expose raw fragments")
}

// TestChannelTestKeySelection pins how a channel test chooses its key: an
// unnamed test keeps the automatic selection, a named key is validated against
// the channel and the tested model, and a failure is attributed to the tested
// key only, so it never disables the channel or another key.
func TestChannelTestKeySelection(t *testing.T) {
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisable })
	common.AutomaticDisableChannelEnabled = true

	multiKey := &model.Channel{Id: 11, Type: constant.ChannelTypeOpenAI, Key: "sk-first\nsk-second", Models: "model-a,model-b", Group: "default",
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels:     map[int][]string{1: {"model-b"}},
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}}
	singleKey := &model.Channel{Id: 12, Type: constant.ChannelTypeOpenAI, Key: "sk-single", Models: "model-a", Group: "default"}

	t.Run("no key index keeps the automatic selection", func(t *testing.T) {
		require.NoError(t, checkChannelTestKey(multiKey, -1, "model-a"))
		require.NoError(t, checkChannelTestKey(singleKey, -1, "model-a"))
	})

	t.Run("a covered key is accepted even while it is disabled", func(t *testing.T) {
		// Key #2 is disabled and scoped to model-b; an operator may still test it
		// to learn whether it recovered.
		require.NoError(t, checkChannelTestKey(multiKey, 1, "model-b"))
	})

	t.Run("an unknown or out-of-range key is rejected", func(t *testing.T) {
		for _, keyIndex := range []int{2, 9} {
			err := checkChannelTestKey(multiKey, keyIndex, "model-a")
			var keyErr *channelTestKeyError
			require.ErrorAs(t, err, &keyErr)
			assert.Equal(t, types.ErrorCodeInvalidRequest, keyErr.code)
			assert.Contains(t, keyErr.Error(), "does not exist")
		}
		err := checkChannelTestKey(singleKey, 0, "model-a")
		var keyErr *channelTestKeyError
		require.ErrorAs(t, err, &keyErr)
		assert.Equal(t, types.ErrorCodeInvalidRequest, keyErr.code)
		assert.Contains(t, keyErr.Error(), "multi-key mode")
	})

	t.Run("a key outside its model scope is rejected with the model named", func(t *testing.T) {
		err := checkChannelTestKey(multiKey, 1, "model-a")
		var keyErr *channelTestKeyError
		require.ErrorAs(t, err, &keyErr)
		assert.Equal(t, types.ErrorCodeChannelKeyScopeUnsatisfied, keyErr.code)
		assert.Contains(t, keyErr.Error(), "key #2")
		assert.Contains(t, keyErr.Error(), "model-a")
	})

	t.Run("a rejected test never reaches the relay or the disable path", func(t *testing.T) {
		result := testChannel(t.Context(), multiKey, 0, "model-a", "", false, 9)
		var keyErr *channelTestKeyError
		require.ErrorAs(t, result.localErr, &keyErr)
		require.Nil(t, result.newAPIError, "a rejected key is reported as a local failure only")
		require.Equal(t, 9, result.keyIndex)
	})

	t.Run("a failure is attributed to the key that served the test", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.Nil(t, middleware.SetupChannelTestContext(ctx, multiKey, "model-b", "sk-second", 1))
		assert.Equal(t, 1, servedTestKeyIndex(ctx, multiKey))

		require.Nil(t, middleware.SetupChannelTestContext(ctx, singleKey, "model-a", "sk-single", 0))
		assert.Equal(t, -1, servedTestKeyIndex(ctx, singleKey), "a single-key channel has no key index")
	})
}

// TestChannelTestNamedKeyFailureOnlyDisablesThatKey proves the consequence of a
// named-key test: the tested key can be auto-disabled, while the channel and the
// other keys stay usable.
func TestChannelTestNamedKeyFailureOnlyDisablesThatKey(t *testing.T) {
	require.NoError(t, i18n.Init())
	setupChannelCreateTest(t)
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisable })
	common.AutomaticDisableChannelEnabled = true

	autoBan := 1
	channel := &model.Channel{Name: "multi-key-test-failure", Type: constant.ChannelTypeOpenAI, Key: "sk-first\nsk-second",
		Models: "model-a", Group: "default", Status: common.ChannelStatusEnabled, AutoBan: &autoBan,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom}}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, middleware.SetupChannelTestContext(ctx, channel, "model-a", "sk-first", 0))
	usingKey := common.GetContextKeyString(ctx, constant.ContextKeyChannelKey)
	require.Equal(t, "sk-first", usingKey)

	processChannelError(ctx, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, usingKey, channel.GetAutoBan()),
		types.NewError(fmt.Errorf("upstream rejected the key"), types.ErrorCodeChannelInvalidKey), nil)

	// The disable runs in the background; the notice path also has to finish.
	var loaded *model.Channel
	require.Eventually(t, func() bool {
		var err error
		loaded, err = model.GetChannelById(channel.Id, true)
		return err == nil && len(loaded.ChannelInfo.MultiKeyStatusList) == 1
	}, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, common.ChannelStatusEnabled, loaded.Status, "the channel stays enabled while another key works")
	assert.Equal(t, map[int]int{0: common.ChannelStatusAutoDisabled}, loaded.ChannelInfo.MultiKeyStatusList,
		"only the tested key is disabled")

	// The healthy key still serves requests, and the channel is not lost.
	key, index, apiErr := loaded.GetNextEnabledKeyFor("model-a", 0)
	require.Nil(t, apiErr)
	assert.Equal(t, 1, index)
	assert.Equal(t, "sk-second", key)
}

// newMultiKeyModelServer serves an OpenAI-compatible /v1/models whose answer
// depends on the bearer key, so a test can prove which key reached the upstream.
func newMultiKeyModelServer(t *testing.T, modelsByKey map[string][]string, failingKeys ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		for _, failingKey := range failingKeys {
			if key == failingKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		models, ok := modelsByKey[key]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		data := make([]map[string]string, 0, len(models))
		for _, modelName := range models {
			data = append(data, map[string]string{"id": modelName})
		}
		body, err := common.Marshal(map[string]any{"object": "list", "data": data})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestMultiKeyModelDiscoveryUsesOneKey pins the per-key discovery actions: the
// models of one key are read with that key only, and the channel keeps its own
// model scope / status out of the upstream request.
func TestMultiKeyModelDiscoveryUsesOneKey(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	server := newMultiKeyModelServer(t, map[string][]string{
		"sk-alpha": {"model-a", "model-shared"},
		"sk-beta":  {"model-b", "model-shared"},
	})
	baseURL := server.URL
	channel := &model.Channel{Name: "multi-key-fetch", Type: constant.ChannelTypeOpenAI, Key: "sk-alpha\nsk-beta", Models: "model-a", Group: "default", BaseURL: &baseURL,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyModels: map[int][]string{1: {"model-a"}}},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	// The second key is scoped to model-a, which must not narrow what its
	// upstream account reports.
	recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "fetch_key_models", KeyIndex: common.GetPointer(1)})
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Models []string `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"model-b", "model-shared"}, response.Data.Models)

	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "model-a", loaded.Models, "a read-only discovery never changes the channel")
	assert.Equal(t, map[int][]string{1: {"model-a"}}, loaded.ChannelInfo.MultiKeyModels)

	for _, request := range []MultiKeyManageRequest{
		{ChannelId: channel.Id, Action: "fetch_key_models"},
		{ChannelId: channel.Id, Action: "fetch_key_models", KeyIndex: common.GetPointer(2)},
	} {
		recorder := postMultiKeyManage(t, rootID, request)
		var rejected struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &rejected))
		assert.False(t, rejected.Success)
		assert.NotEmpty(t, rejected.Message)
	}

	t.Run("a refreshed nonzero plugin key keeps its index on retry", func(t *testing.T) {
		const pluginKey = "multikey-discovery-refresh"
		_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "multikey-discovery-refresh", name: "Discovery refresh",
  version: "1.0.0", author: {name: "Test"}, relay: [{incoming_path: "/v1/models", upstream_path: "/v1/models"}]};
export function refresh(ctx) {
  const token = ctx.credential.token + "-next";
  return {credential: {token}, headers: {Authorization: "Bearer " + token}, expiresAt: utils.unixNow() + 3600};
}`, jsplugin.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(pluginKey) })
		requests := make(chan string, 4)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Authorization")
			requests <- key
			assert.Equal(t, "/v1/models", r.URL.Path)
			if key == "Bearer second-next" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if key != "Bearer second-next-next" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"second-model"}]}`))
		}))
		t.Cleanup(upstream.Close)
		first, err := (jsplugin.Credential{Plugin: pluginKey, Data: []byte(`{"token":"first"}`),
			Headers: map[string]string{"Authorization": "Bearer first"}}).Line()
		require.NoError(t, err)
		second, err := (jsplugin.Credential{Plugin: pluginKey, Data: []byte(`{"token":"second"}`),
			Headers: map[string]string{"Authorization": "Bearer second"}, ExpiresAt: time.Now().Add(-time.Minute).Unix()}).Line()
		require.NoError(t, err)
		pluginChannel := insertPluginChannel(t, pluginKey, upstream.URL, first+"\n"+second, true)
		t.Cleanup(func() { require.NoError(t, pluginChannel.Delete()) })

		recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: pluginChannel.Id, Action: "fetch_key_models", KeyIndex: common.GetPointer(1)})
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				Models []string `json:"models"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.True(t, result.Success, recorder.Body.String())
		assert.Equal(t, []string{"second-model"}, result.Data.Models)
		require.Len(t, requests, 2)
		assert.Equal(t, "Bearer second-next", <-requests)
		assert.Equal(t, "Bearer second-next-next", <-requests)
		stored, err := model.GetChannelById(pluginChannel.Id, true)
		require.NoError(t, err)
		require.Len(t, stored.GetKeys(), 2)
		assert.Equal(t, first, stored.GetKeys()[0], "refresh must never select the neighboring accountless credential")
		credential, ok := jsplugin.ParseCredential(stored.GetKeys()[1])
		require.True(t, ok)
		assert.Equal(t, "Bearer second-next-next", credential.Headers["Authorization"])
	})
}

// TestMultiKeyModelSyncPreviewAndApply pins the channel-wide sync: a preview
// reports every key without touching the channel (a disabled key is skipped, a
// failing key is reported), and apply extends the channel models once while each
// successful key keeps the models it actually serves.
func TestMultiKeyModelSyncPreviewAndApply(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	server := newMultiKeyModelServer(t, map[string][]string{
		"sk-alpha": {"model-a", "model-shared"},
		"sk-beta":  {"model-b", "model-shared"},
	}, "sk-broken")
	baseURL := server.URL
	channel := &model.Channel{Name: "multi-key-sync", Type: constant.ChannelTypeOpenAI, Key: "sk-alpha\nsk-disabled\nsk-beta\nsk-broken", Models: "model-a,model-shared", Group: "default", BaseURL: &baseURL,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 4, MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled},
			MultiKeyModels:     map[int][]string{1: {"model-a"}, 3: {"model-shared"}}},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	type syncResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Results []struct {
				KeyIndex int      `json:"key_index"`
				Models   []string `json:"models"`
				Error    string   `json:"error"`
				Skipped  bool     `json:"skipped"`
			} `json:"results"`
			AddedModels []string `json:"added_models"`
			Applied     bool     `json:"applied"`
		} `json:"data"`
	}

	recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "sync_key_models"})
	var preview syncResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &preview))
	require.True(t, preview.Success, preview.Message)
	require.Len(t, preview.Data.Results, 4)
	assert.Equal(t, []string{"model-a", "model-shared"}, preview.Data.Results[0].Models)
	assert.Equal(t, 0, preview.Data.Results[0].KeyIndex)
	assert.True(t, preview.Data.Results[1].Skipped, "a disabled key is reported without being contacted")
	assert.Empty(t, preview.Data.Results[1].Models)
	assert.Empty(t, preview.Data.Results[1].Error)
	assert.Equal(t, []string{"model-b", "model-shared"}, preview.Data.Results[2].Models)
	assert.NotEmpty(t, preview.Data.Results[3].Error, "a failing key is reported with its reason")
	assert.False(t, preview.Data.Applied)
	assert.Equal(t, []string{"model-b"}, preview.Data.AddedModels)

	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "model-a,model-shared", loaded.Models, "a preview never changes the channel")
	assert.Equal(t, map[int][]string{1: {"model-a"}, 3: {"model-shared"}}, loaded.ChannelInfo.MultiKeyModels)

	recorder = postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: channel.Id, Action: "sync_key_models", Apply: true})
	var applied syncResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &applied))
	require.True(t, applied.Success, applied.Message)
	assert.True(t, applied.Data.Applied)
	assert.Equal(t, []string{"model-b"}, applied.Data.AddedModels)

	loaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "model-a,model-shared,model-b", loaded.Models, "existing models keep their order and new ones are appended once")
	assert.Equal(t, map[int][]string{
		0: {"model-a", "model-shared"},
		1: {"model-a"},
		2: {"model-b", "model-shared"},
		3: {"model-shared"},
	}, loaded.ChannelInfo.MultiKeyModels, "only the keys that answered take a new scope, and the skipped key keeps its own")

	// The extended model list really reaches the routing table, and the skipped
	// key keeps its own scope.
	var abilities []model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Order("model").Find(&abilities).Error)
	require.Len(t, abilities, 3)
	for i, modelName := range []string{"model-a", "model-b", "model-shared"} {
		assert.Equal(t, modelName, abilities[i].Model)
		assert.True(t, abilities[i].Enabled, "a synced model must be routable")
	}

	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("authoritative enabled-key union/failure=%t", failure), func(t *testing.T) {
			keys := "sk-alpha\nsk-disabled\nsk-beta"
			if failure {
				keys += "\nsk-broken"
			}
			source := &model.Channel{Name: t.Name(), Type: constant.ChannelTypeOpenAI, Key: keys,
				Models: "model-a,model-b,model-old", Group: "default", BaseURL: &baseURL,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: len(strings.Split(keys, "\n")),
					MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}},
			}
			settings := dto.ChannelOtherSettings{UpstreamModelUpdateAutoSyncEnabled: true,
				UpstreamModelUpdateLastDetectedModels: []string{"previous-add"},
				UpstreamModelUpdateLastRemovedModels:  []string{"previous-remove"}}
			source.SetOtherSettings(settings)
			require.NoError(t, source.Insert())
			t.Cleanup(func() { require.NoError(t, source.Delete()) })
			changed, added, err := checkAndPersistChannelUpstreamModelUpdatesContext(t.Context(), source, &settings, true, true)
			if failure {
				require.Error(t, err)
				assert.False(t, changed)
				assert.Zero(t, added)
			} else {
				require.NoError(t, err)
				assert.True(t, changed)
				assert.Equal(t, 1, added)
			}
			stored, err := model.GetChannelById(source.Id, true)
			require.NoError(t, err)
			if failure {
				assert.Equal(t, "model-a,model-b,model-old", stored.Models, "partial inventory cannot auto-apply additions")
				assert.Equal(t, []string{"previous-add"}, stored.GetOtherSettings().UpstreamModelUpdateLastDetectedModels)
				assert.Equal(t, []string{"previous-remove"}, stored.GetOtherSettings().UpstreamModelUpdateLastRemovedModels, "partial inventory cannot become removal evidence")
			} else {
				assert.Equal(t, "model-a,model-b,model-old,model-shared", stored.Models)
				assert.Empty(t, stored.GetOtherSettings().UpstreamModelUpdateLastDetectedModels)
				assert.Equal(t, []string{"model-old"}, stored.GetOtherSettings().UpstreamModelUpdateLastRemovedModels, "models present on either enabled key must remain")
			}
		})
	}

	for _, schedulingEdit := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply preserves concurrent configuration/scheduling=%t", schedulingEdit), func(t *testing.T) {
			var source *model.Channel
			replacement := `{"X-Configuration":"replacement"}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer sk-source", r.Header.Get("Authorization"))
				column, value := "header_override", any(replacement)
				if schedulingEdit {
					info := source.ChannelInfo
					info.MultiKeyMode = constant.MultiKeyModePolling
					info.MultiKeyPollingIndex = 1
					info.MultiKeyDisabledReason = map[int]string{1: "current reason"}
					info.MultiKeyDisabledTime = map[int]int64{1: 123456}
					column, value = "channel_info", info
				}
				var updateErr error
				if schedulingEdit {
					_, updateErr = model.UpdateScheduledChannelLocked(r.Context(), source.Id, func(current *model.Channel) error {
						current.ChannelInfo.MultiKeyMode = constant.MultiKeyModePolling
						current.ChannelInfo.MultiKeyPollingIndex = 1
						current.ChannelInfo.MultiKeyDisabledReason = map[int]string{1: "current reason"}
						current.ChannelInfo.MultiKeyDisabledTime = map[int]int64{1: 123456}
						return nil
					})
				} else {
					updateErr = model.DB.Model(&model.Channel{}).Where("id = ?", source.Id).Update(column, value).Error
				}
				if !assert.NoError(t, updateErr) {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"model-new"}]}`))
			}))
			t.Cleanup(upstream.Close)
			source = &model.Channel{Name: t.Name(), Type: constant.ChannelTypeOpenAI, Key: "sk-source\nsk-disabled",
				Models: "model-original", Group: "default", BaseURL: &upstream.URL,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2,
					MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}}
			require.NoError(t, source.Insert())
			t.Cleanup(func() { require.NoError(t, source.Delete()) })
			recorder := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: source.Id, Action: "sync_key_models", Apply: true})
			var response struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, schedulingEdit, response.Success, recorder.Body.String())
			stored, err := model.GetChannelById(source.Id, true)
			require.NoError(t, err)
			if schedulingEdit {
				assert.Equal(t, "model-original,model-new", stored.Models)
				assert.Equal(t, []string{"model-new"}, stored.ChannelInfo.MultiKeyModels[0])
				assert.Equal(t, constant.MultiKeyModePolling, stored.ChannelInfo.MultiKeyMode)
				assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
				assert.Equal(t, map[int]string{1: "current reason"}, stored.ChannelInfo.MultiKeyDisabledReason)
				assert.Equal(t, map[int]int64{1: 123456}, stored.ChannelInfo.MultiKeyDisabledTime)
				return
			}
			assert.Equal(t, "model-original", stored.Models)
			require.NotNil(t, stored.HeaderOverride)
			assert.Equal(t, replacement, *stored.HeaderOverride)
			assert.Empty(t, stored.ChannelInfo.MultiKeyModels)
		})
	}
}

// Status reads must not reconcile migrated allocations or enrich keys outside
// the requested page while holding up other channel management operations.
func TestMultiKeyStatusReadIsPageBounded(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("ledger=%t", active), func(t *testing.T) {
			rootID := setupChannelCreateTest(t)
			if active {
				t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
				t.Setenv("BATCH_UPDATE_ENABLED", "false")
				previousBatch := common.BatchUpdateEnabled
				common.BatchUpdateEnabled = false
				t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
				require.NoError(t, model.InitializeChannelBudgets())
			}
			owner := &model.User{Username: "status-page-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "statuspage"}
			require.NoError(t, model.DB.Create(owner).Error)
			selected := &model.Channel{Name: t.Name(), Type: 1, Key: "sk-first\nsk-second\nsk-third\nsk-fourth\nsk-fifth\nsk-sixth", Models: "model-a,model-b", Group: "default", Status: common.ChannelStatusEnabled,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 6}}
			require.NoError(t, selected.Insert())
			current, err := model.MutateChannelKeyEntries(t.Context(), selected.Id, 0, func(_ *gorm.DB, _ *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
				for i := range entries {
					entries[i].BoundUserId = owner.Id
					entries[i].ConfigRevision++
				}
				entries[1].Status = common.ChannelStatusManuallyDisabled
				entries[1].DisabledTime = 123
				entries[1].DisabledReason = "manual review"
				entries[2].Status = common.ChannelStatusAutoDisabled
				entries[3].ScopeMode, entries[3].Models = "allowlist", "[]"
				return entries, nil, nil
			})
			require.NoError(t, err)
			// Stored compatibility JSON can lag the allocation rows. The read
			// must derive identity, binding, scope and status from those rows.
			staleInfo := selected.ChannelInfo
			staleInfo.KeyEntryRevision = current.ChannelInfo.KeyEntryRevision
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", selected.Id).UpdateColumn("channel_info", staleInfo).Error)
			accounts := make([]model.ChannelBudgetAccount, len(current.KeyEntryIDs))
			for i, id := range current.KeyEntryIDs {
				accounts[i] = model.ChannelBudgetAccount{ScopeType: model.BudgetScopeKey, ScopeId: id, ChannelId: selected.Id, Enabled: true, LimitQuota: 100, PeriodUsedQuota: int64(i + 1), ConfigVersion: 2}
			}
			require.NoError(t, model.DB.Create(&accounts).Error)
			var counts map[string]int
			budgetRows := int64(0)
			writes := 0
			enrichmentUnlocked := true
			const callback = "test:multi-key-status-page-read"
			database := model.DB
			require.NoError(t, database.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				counts[tx.Statement.Table]++
				if tx.Statement.Table == "channel_budget_accounts" {
					budgetRows += tx.RowsAffected
				}
				if tx.Statement.Table == "channel_budget_accounts" || tx.Statement.Table == "users" {
					lock := model.GetChannelPollingLock(selected.Id)
					if lock.TryLock() {
						lock.Unlock()
					} else {
						enrichmentUnlocked = false
					}
				}
			}))
			require.NoError(t, database.Callback().Update().Before("gorm:update").Register(callback, func(_ *gorm.DB) { writes++ }))
			t.Cleanup(func() {
				require.NoError(t, database.Callback().Query().Remove(callback))
				require.NoError(t, database.Callback().Update().Remove(callback))
			})
			for _, tc := range []struct {
				name     string
				page     int
				pageSize int
				status   *int
				indices  []int
				total    int
			}{
				{name: "one key", page: 2, pageSize: 1, indices: []int{1}, total: 6},
				{name: "several keys with the same owner", page: 1, pageSize: 6, indices: []int{0, 1, 2, 3, 4, 5}, total: 6},
				{name: "filter before enrichment and clamp page", page: 1000, pageSize: 2, status: common.GetPointer(1), indices: []int{4, 5}, total: 4},
				{name: "cap oversized page size", page: 1, pageSize: int(^uint(0) >> 1), indices: []int{0, 1, 2, 3, 4, 5}, total: 6},
				{name: "empty filtered page", page: 1, pageSize: 2, status: common.GetPointer(99), total: 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					counts = make(map[string]int)
					budgetRows, writes, enrichmentUnlocked = 0, 0, true
					response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status", Page: tc.page, PageSize: tc.pageSize, Status: tc.status})
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					var body struct {
						Success bool                   `json:"success"`
						Data    MultiKeyStatusResponse `json:"data"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
					require.True(t, body.Success, response.Body.String())
					assert.Equal(t, current.ChannelInfo.KeyEntryRevision, body.Data.Revision)
					assert.Equal(t, active, body.Data.QuotaSupported)
					assert.Equal(t, tc.total, body.Data.Total)
					assert.Equal(t, 4, body.Data.EnabledCount)
					assert.Equal(t, 1, body.Data.ManualDisabledCount)
					assert.Equal(t, 1, body.Data.AutoDisabledCount)
					assert.LessOrEqual(t, body.Data.PageSize, 100)
					require.Len(t, body.Data.Keys, len(tc.indices))
					for i, index := range tc.indices {
						key := body.Data.Keys[i]
						assert.Equal(t, index, key.Index)
						assert.Equal(t, strconv.FormatInt(current.KeyEntryIDs[index], 10), key.Id)
						assert.Equal(t, current.KeyEntryRevisions[index], key.Rev)
						assert.Equal(t, owner.Id, key.UserId)
						assert.Equal(t, owner.Username, key.Username)
						require.NotNil(t, key.Quota)
						assert.Equal(t, active, key.Quota.LedgerEnabled)
						assert.EqualValues(t, index+1, key.Quota.PeriodUsedQuota)
						if index == 1 {
							assert.Equal(t, "manual review", key.Reason)
							assert.EqualValues(t, 123, key.DisabledTime)
						}
						if index == 3 {
							assert.Equal(t, "allowlist", key.ScopeMode)
							assert.Empty(t, key.Models)
						}
					}
					assert.Equal(t, 1, counts["channels"], "a migrated status read must load one fresh channel snapshot")
					assert.Equal(t, 1, counts["channel_key_entries"], "allocation hydration must not be repeated by reconciliation")
					assert.LessOrEqual(t, counts["options"], 1, "activation must be read at most once")
					assert.EqualValues(t, len(tc.indices), budgetRows, "only the selected page's budgets may be read")
					if len(tc.indices) > 0 {
						assert.Equal(t, 1, counts["channel_budget_accounts"])
						assert.Equal(t, 1, counts["users"], "bound usernames must use one batch, not one lookup per key")
					} else {
						assert.Zero(t, counts["channel_budget_accounts"])
						assert.Zero(t, counts["users"])
					}
					assert.Zero(t, writes, "migrated status reads must not take the migration write path")
					assert.True(t, enrichmentUnlocked, "budget/name reads must not hold the channel polling mutex")
				})
			}
		})
	}
}

func TestMultiKeyStatusReadLegacyMigrationAndCancellation(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	legacy := &model.Channel{Name: t.Name(), Type: 1, Key: "sk-legacy-first\nsk-legacy-second", Models: "model-a", Group: "default", Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyUsers: map[int]int{1: rootID}, MultiKeyScopeModes: map[int]string{1: "allowlist"}}}
	require.NoError(t, model.DB.Create(legacy).Error)
	response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: legacy.Id, Action: "get_key_status"})
	var body struct {
		Success bool                   `json:"success"`
		Data    MultiKeyStatusResponse `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success, response.Body.String())
	require.Len(t, body.Data.Keys, 2)
	assert.Positive(t, body.Data.Revision)
	assert.NotEqual(t, "0", body.Data.Keys[0].Id)
	assert.NotEqual(t, body.Data.Keys[0].Id, body.Data.Keys[1].Id)
	assert.Equal(t, rootID, body.Data.Keys[1].UserId)
	assert.Equal(t, "allowlist", body.Data.Keys[1].ScopeMode)
	assert.Empty(t, body.Data.Keys[1].Models)
	fresh, err := model.ReadChannelKeyEntriesSnapshot(t.Context(), legacy.Id)
	require.NoError(t, err)
	assert.Equal(t, body.Data.Revision, fresh.ChannelInfo.KeyEntryRevision)
	assert.Equal(t, body.Data.Keys[0].Id, strconv.FormatInt(fresh.KeyEntryIDs[0], 10))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = model.ReadChannelKeyEntriesSnapshot(ctx, legacy.Id)
	require.ErrorIs(t, err, context.Canceled)

	const callback = "test:multi-key-status-cancel-allocation-read"
	database := model.DB
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_key_entries" {
			cancel()
		}
	}))
	t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })
	_, err = model.ReadChannelKeyEntriesSnapshot(ctx, legacy.Id)
	require.ErrorIs(t, err, context.Canceled, "the authoritative allocation read must retain request cancellation")
}

func TestMultiKeyStatusReadStorageFailures(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	selected := &model.Channel{Name: t.Name(), Type: 1, Key: "sk-status-storage", Models: "model-a", Group: "default", Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyUsers: map[int]int{0: rootID}}}
	require.NoError(t, selected.Insert())
	for _, tc := range []struct {
		table   string
		success bool
	}{
		{table: "options"},
		{table: "channel_budget_accounts"},
		{table: "users", success: true},
	} {
		t.Run(tc.table, func(t *testing.T) {
			const callback = "test:multi-key-status-storage-failure"
			database := model.DB
			require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == tc.table {
					tx.AddError(fmt.Errorf("status read storage unavailable"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })
			response := postMultiKeyManage(t, rootID, MultiKeyManageRequest{ChannelId: selected.Id, Action: "get_key_status"})
			var body struct {
				Success bool                   `json:"success"`
				Data    MultiKeyStatusResponse `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, tc.success, body.Success, response.Body.String())
			if !tc.success {
				assert.Empty(t, body.Data.Keys, "activation/account failure must not return successful unlimited quota")
				return
			}
			require.Len(t, body.Data.Keys, 1)
			assert.Equal(t, rootID, body.Data.Keys[0].UserId)
			assert.Empty(t, body.Data.Keys[0].Username, "display-name failure retains the existing best-effort fallback")
			require.NotNil(t, body.Data.Keys[0].Quota)
		})
	}
}
