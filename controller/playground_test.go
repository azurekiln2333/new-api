package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlaygroundChannelNavigationAuthorizationAndRouting(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
	require.NoError(t, i18n.Init())
	previousGroups, previousAuto, previousRatios := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","auto":"Auto","second":"Second"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["second","default"]`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"second":1}`))
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cached), func(t *testing.T) {
			common.MemoryCacheEnabled = cached
			channels := []*model.Channel{
				{Name: "pg-public", Group: "default", Models: "model-a,model-b", Key: "public-key"},
				{Name: "pg-scoped", Group: "default,second", Models: "model-a,model-b", Key: "scoped-key"},
				{Name: "pg-private", Group: "private", Models: "model-a", Key: "private-key"},
				{Name: "pg-bound", Group: "private", Models: "bound-model,other-model", Key: "my-key\nother-key", ChannelInfo: model.ChannelInfo{
					IsMultiKey: true, MultiKeySize: 2, MultiKeyUsers: map[int]int{0: rootID, 1: rootID + 1000},
					MultiKeyModels: map[int][]string{0: {"bound-model"}, 1: {"other-model"}},
				}},
				{Name: "pg-disabled-key", Group: "default", Models: "model-a", Key: "disabled-key", ChannelInfo: model.ChannelInfo{
					IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
				}},
				{Name: "pg-disabled-channel", Group: "default", Models: "model-a", Key: "disabled-channel-key", Status: common.ChannelStatusManuallyDisabled},
			}
			channels[1].SetOtherSettings(dto.ChannelOtherSettings{GroupModels: map[string][]string{"default": {"model-a"}, "second": {"model-b"}}})
			for _, channel := range channels {
				channel.Type = constant.ChannelTypeOpenAI
				if channel.Status == 0 {
					channel.Status = common.ChannelStatusEnabled
				}
				require.NoError(t, channel.Insert())
				t.Cleanup(func() { require.NoError(t, channel.Delete()) })
			}
			model.InitChannelCache()
			for _, tc := range []struct {
				name, group, model    string
				channel, role, status int
				pat                   bool
				wantGroup             string
			}{
				{name: "explicit target", group: "default", model: "model-a", channel: channels[1].Id, role: common.RoleRootUser, status: http.StatusOK, wantGroup: "default"},
				{name: "omitted group", model: "model-a", channel: channels[0].Id, role: common.RoleRootUser, status: http.StatusOK, wantGroup: "default"},
				{name: "auto resolves eligible group", group: "auto", model: "model-b", channel: channels[1].Id, role: common.RoleRootUser, status: http.StatusOK, wantGroup: "second"},
				{name: "own bound key outside group", group: "default", model: "bound-model", channel: channels[3].Id, role: common.RoleRootUser, status: http.StatusOK, wantGroup: "default"},
				{name: "ordinary user forged pin", group: "default", model: "model-a", channel: channels[0].Id, role: common.RoleCommonUser, status: http.StatusForbidden},
				{name: "ordinary user forged pin outside group", group: "default", model: "model-a", channel: channels[2].Id, role: common.RoleCommonUser, status: http.StatusForbidden},
				{name: "PAT cannot pin", group: "default", model: "model-a", channel: channels[0].Id, role: common.RoleRootUser, pat: true, status: http.StatusForbidden},
				{name: "unavailable group", group: "private", model: "model-a", channel: channels[2].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "channel outside group", group: "default", model: "model-a", channel: channels[2].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "group model restriction", group: "default", model: "model-b", channel: channels[1].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "unknown model", group: "default", model: "absent", channel: channels[0].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "another users key", group: "default", model: "other-model", channel: channels[3].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "disabled key", group: "default", model: "model-a", channel: channels[4].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "disabled channel", group: "default", model: "model-a", channel: channels[5].Id, role: common.RoleRootUser, status: http.StatusForbidden},
				{name: "invalid id", group: "default", model: "model-a", channel: -1, role: common.RoleRootUser, status: http.StatusBadRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					providerOptions := common.RawMessage(`{"zero":0,"flag":false,"large":9007199254740993}`)
					request, err := common.Marshal(map[string]any{"model": tc.model, "group": tc.group, "channel_id": tc.channel, "provider_options": providerOptions})
					require.NoError(t, err)
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set("id", rootID)
						c.Set("role", tc.role)
						c.Set("group", "default")
						c.Set("use_access_token", tc.pat)
						c.Next()
					})
					routed := 0
					router.POST("/pg/chat/completions", middleware.Distribute(), func(c *gin.Context) {
						routed = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
						defer common.CleanupBodyStorage(c)
						var forwarded map[string]common.RawMessage
						require.NoError(t, common.UnmarshalBodyReusable(c, &forwarded))
						assert.NotContains(t, forwarded, "channel_id", "host routing metadata must not reach a passthrough upstream")
						assert.JSONEq(t, fmt.Sprintf("%q", tc.model), string(forwarded["model"]))
						assert.Equal(t, string(providerOptions), string(forwarded["provider_options"]), "stripping a pin preserves opaque provider values without float conversion")
						assert.Equal(t, tc.wantGroup, common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
						assert.True(t, service.GetChannelConstraints(c).SuppressesRetry(), "a selected channel cannot silently fail over")
						if tc.model == "bound-model" {
							assert.Equal(t, "my-key", common.GetContextKeyString(c, constant.ContextKeyChannelKey))
						}
						c.Status(http.StatusOK)
					})
					response := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(string(request)))
					req.Header.Set("Content-Type", "application/json")
					router.ServeHTTP(response, req)
					require.Equal(t, tc.status, response.Code, response.Body.String())
					if tc.status == http.StatusOK {
						assert.Equal(t, tc.channel, routed)
					} else {
						assert.Zero(t, routed)
					}
				})
			}

			for _, tc := range []struct {
				name   string
				role   int
				pat    bool
				group  string
				status int
			}{
				{"admin minimal metadata", common.RoleRootUser, false, "default", http.StatusOK},
				{"ordinary user", common.RoleCommonUser, false, "default", http.StatusForbidden},
				{"ordinary user forged group", common.RoleCommonUser, false, "private", http.StatusForbidden},
				{"personal access token", common.RoleRootUser, true, "default", http.StatusForbidden},
				{"denied group", common.RoleRootUser, false, "private", http.StatusForbidden},
			} {
				t.Run(tc.name, func(t *testing.T) {
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodGet, "/pg/channels?group="+tc.group, nil)
					c.Set("id", rootID)
					c.Set("role", tc.role)
					c.Set("group", "default")
					c.Set("use_access_token", tc.pat)
					PlaygroundChannels(c)
					require.Equal(t, tc.status, response.Code, response.Body.String())
					if tc.status != http.StatusOK {
						assert.NotContains(t, response.Body.String(), `"data"`)
						for _, channel := range channels {
							assert.NotContains(t, response.Body.String(), channel.Name)
						}
						return
					}
					var result struct {
						Data []map[string]any `json:"data"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					found := make(map[int][]any)
					for _, item := range result.Data {
						require.Len(t, item, 3, "metadata contains only id, name and models")
						id, ok := item["id"].(float64)
						require.True(t, ok)
						models, ok := item["models"].([]any)
						require.True(t, ok)
						found[int(id)] = models
					}
					assert.ElementsMatch(t, []any{"model-a", "model-b"}, found[channels[0].Id])
					assert.Equal(t, []any{"model-a"}, found[channels[1].Id])
					assert.Equal(t, []any{"bound-model"}, found[channels[3].Id])
					for _, index := range []int{2, 4, 5} {
						assert.NotContains(t, found, channels[index].Id)
					}
					assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
				})
			}
		})
	}
}

func TestPlaygroundChannelNavigationProjection(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	t.Setenv("BATCH_UPDATE_ENABLED", "false")
	previousBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
	require.NoError(t, model.InitializeChannelBudgets())
	previousGroups, previousAuto := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","auto":"Auto","second":"Second","empty":"Empty"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["second","default"]`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"second":1,"empty":1}`))

	const taskPlugin = "pg-navigation-task"
	_, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "pg-navigation-task", name: "Playground task fixture",
  version: "1.0.0", author: {name: "Test"}, models: ["alias"], fetchMode: "per_task", usageSchema: {}};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(taskPlugin) })
	const providerPlugin = "pg-navigation-provider"
	_, err = jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "pg-navigation-provider", name: "Playground provider fixture",
  version: "1.0.0", author: {name: "Test"}, relay: [
    {incoming_path: "/v1/chat/completions", upstream_path: "/chat/completions", models: ["path-only"]},
    {incoming_path: "/pg/chat/completions", upstream_path: "/chat/completions", models: ["alias"]}
  ]};
`, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(providerPlugin) })

	channels := []*model.Channel{
		{Name: "pg-mapped", Group: "default,second", Models: "alias,blocked", Key: "pg-public-mapped", ModelMapping: common.GetPointer(`{"alias":"Upstream-Case"}`), ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeyModels: map[int][]string{0: {"Upstream-Case"}},
		}},
		{Name: "pg-bound-live", Group: "private", Models: "mine,theirs", Key: "pg-my-key\npg-other-key", ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeyUsers: map[int]int{0: rootID, 1: rootID + 1},
			MultiKeyModels: map[int][]string{0: {"mine"}, 1: {"theirs"}},
		}},
		{Name: "pg-deny-empty", Group: "default", Models: "alias", Key: "pg-empty-key", ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeyScopeModes: map[int]string{0: "allowlist"},
		}},
		{Name: "pg-path-v1-only", Group: "default", Models: "alias", Key: "pg-v1-only"},
		{Name: "pg-path-allowed", Group: "default", Models: "alias,blocked", Key: "pg-path-key"},
		{Name: "pg-exhausted-key", Group: "default", Models: "alias", Key: "pg-exhausted-key"},
		{Name: "pg-exhausted-channel", Group: "default", Models: "alias", Key: "pg-exhausted-channel"},
		{Name: "pg-unavailable", Group: "default", Models: "alias", Key: "pg-unavailable-key", ChannelInfo: model.ChannelInfo{
			ModelUnavailable: []model.ChannelModelUnavailability{{UpstreamModel: "alias", KeyFingerprint: model.ChannelKeyFingerprint("pg-unavailable-key")}},
		}},
		{Name: "pg-task-only", Type: constant.ChannelTypeTaskPlugin, Group: "default", Models: "alias", Key: "pg-task-key", Setting: common.GetPointer(`{"task_plugin_key":"pg-navigation-task"}`)},
		{Name: "pg-provider-other-path", Type: constant.ChannelTypeTaskPlugin, Group: "default", Models: "path-only", Key: "pg-provider-other-key", Setting: common.GetPointer(`{"task_plugin_key":"pg-navigation-provider"}`)},
		{Name: "pg-provider-served", Type: constant.ChannelTypeTaskPlugin, Group: "default", Models: "alias,blocked", Key: "pg-provider-key", Setting: common.GetPointer(`{"task_plugin_key":"pg-navigation-provider"}`)},
	}
	channels[0].SetOtherSettings(dto.ChannelOtherSettings{GroupModels: map[string][]string{"default": {"alias"}, "second": {"blocked"}}})
	channels[3].Type = constant.ChannelTypeAdvancedCustom
	channels[3].SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/chat/completions", UpstreamPath: "/chat/completions"}}}})
	channels[4].Type = constant.ChannelTypeAdvancedCustom
	channels[4].SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/pg/chat/completions", UpstreamPath: "/chat/completions", Models: []string{"alias"}}}}})
	for _, channel := range channels {
		if channel.Type == 0 {
			channel.Type = constant.ChannelTypeOpenAI
		}
		channel.Status = common.ChannelStatusEnabled
		require.NoError(t, channel.Insert())
		t.Cleanup(func() { require.NoError(t, channel.Delete()) })
	}
	loaded, err := model.GetChannelById(channels[1].Id, true)
	require.NoError(t, err)
	// A stale compatibility document must not hide the current owner's rows.
	stale := loaded.ChannelInfo
	stale.MultiKeyUsers = map[int]int{0: rootID + 1, 1: rootID + 1}
	stale.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", loaded.Id).UpdateColumn("channel_info", stale).Error)
	keyLimited, err := model.GetChannelById(channels[5].Id, true)
	require.NoError(t, err)
	require.Len(t, keyLimited.KeyEntryIDs, 1)
	accounts := []model.ChannelBudgetAccount{
		{ScopeType: model.BudgetScopeKey, ScopeId: keyLimited.KeyEntryIDs[0], ChannelId: keyLimited.Id, Enabled: true, LimitQuota: 1, PeriodUsedQuota: 1},
		{ScopeType: model.BudgetScopeChannel, ScopeId: int64(channels[6].Id), ChannelId: channels[6].Id, Enabled: true, LimitQuota: 1, PeriodUsedQuota: 1},
	}
	require.NoError(t, model.DB.Create(&accounts).Error)

	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cached), func(t *testing.T) {
			common.MemoryCacheEnabled = cached
			model.InitChannelCache()
			for _, group := range []string{"default", "auto"} {
				t.Run(group, func(t *testing.T) {
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodGet, "/pg/channels?group="+group, nil)
					c.Set("id", rootID)
					c.Set("role", common.RoleRootUser)
					c.Set("group", "default")
					PlaygroundChannels(c)
					var body struct {
						Success bool                       `json:"success"`
						Data    []model.ChannelModelOption `json:"data"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
					require.True(t, body.Success, response.Body.String())
					assert.Equal(t, []model.ChannelModelOption{
						{ID: channels[10].Id, Name: channels[10].Name, Models: []string{"alias"}},
						{ID: channels[4].Id, Name: channels[4].Name, Models: []string{"alias"}},
						{ID: channels[1].Id, Name: channels[1].Name, Models: []string{"mine"}},
						{ID: channels[0].Id, Name: channels[0].Name, Models: []string{"alias"}},
					}, body.Data)
					for _, channel := range channels {
						for _, key := range channel.GetKeys() {
							assert.NotContains(t, response.Body.String(), key)
						}
					}
					assert.NotContains(t, response.Body.String(), "Upstream-Case")
					assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
				})
			}
		})
	}

	for _, tc := range []struct {
		name, table string
		cancel      bool
	}{
		{name: "activation", table: "options"},
		{name: "channels", table: "channels"},
		{name: "allocations", table: "channel_key_entries"},
		{name: "budgets", table: "channel_budget_accounts"},
		{name: "cancel during allocations", table: "channel_key_entries", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Activation is already cached in this fixture. Use an independent
			// handle so the activation-error case exercises its SQL boundary.
			database := model.DB
			if tc.table == "options" {
				model.DB = database.Session(&gorm.Session{})
				t.Cleanup(func() { model.DB = database })
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			const callback = "test:playground-options-read-failure"
			require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != tc.table {
					return
				}
				if tc.cancel {
					cancel()
				} else {
					tx.AddError(errors.New("playground storage unavailable"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })
			options, readErr := service.ListPlaygroundChannels(ctx, rootID, "default", "default")
			require.Error(t, readErr)
			assert.Nil(t, options, "do not publish a partial catalog on storage failure")
			if tc.cancel {
				require.ErrorIs(t, readErr, context.Canceled)
			}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/pg/channels?group=default", nil).WithContext(ctx)
			c.Set("id", rootID)
			c.Set("role", common.RoleRootUser)
			c.Set("group", "default")
			PlaygroundChannels(c)
			var body struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
			assert.False(t, body.Success, "a failed read is not an empty catalog")
			assert.NotContains(t, response.Body.String(), `"data"`)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.ListPlaygroundChannels(ctx, rootID, "default", "default")
	require.ErrorIs(t, err, context.Canceled)

	for _, row := range channels {
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", row.Id).UpdateColumn("status", common.ChannelStatusManuallyDisabled).Error)
	}
	empty, err := service.ListPlaygroundChannels(t.Context(), rootID, "default", "default")
	require.NoError(t, err)
	assert.Equal(t, []model.ChannelModelOption{}, empty, "real denials remain a successful empty catalog")
}

func TestPlaygroundChannelNavigationProjectionUsesPerKeyMapping(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	channel := &model.Channel{
		Name:   "pg-per-key-mapping",
		Type:   constant.ChannelTypeOpenAI,
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "alias",
		Key:    "pg-mapping-a\npg-mapping-b\npg-mapping-other\npg-mapping-empty",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:            true,
			MultiKeySize:          4,
			MultiKeyUsers:         map[int]int{2: rootID + 1},
			MultiKeyScopeModes:    map[int]string{3: "allowlist"},
			MultiKeyModelMappings: map[int]string{
				0: `{"alias":"upstream-a"}`,
				1: `{"alias":"upstream-b"}`,
				2: `{"alias":"upstream-other"}`,
			},
		},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })

	loaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	allocationA, ok := loaded.ChannelAllocationByPosition(0)
	require.True(t, ok)
	allocationB, ok := loaded.ChannelAllocationByPosition(1)
	require.True(t, ok)
	mappingA, err := loaded.GetModelMappingForKey(0)
	require.NoError(t, err)
	mappingB, err := loaded.GetModelMappingForKey(1)
	require.NoError(t, err)

	markUnavailable := func(allocation model.ChannelAllocation, mapping, upstream string) {
		written, err := model.RecordChannelModelUnavailability(
			loaded.Id,
			"alias",
			allocation.Key,
			mapping,
			upstream,
			http.StatusBadGateway,
			model.ChannelModelUnavailableEvidenceCode,
			allocation,
		)
		require.NoError(t, err)
		require.True(t, written)
	}

	markUnavailable(allocationA, mappingA, "upstream-a")
	options, err := model.ListAvailableChannelModels(
		t.Context(),
		[]string{"default"},
		rootID,
		[]dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: "/pg/chat/completions"}},
	)
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, []string{"alias"}, options[0].Models, "Key B keeps the public alias after only Key A becomes unavailable")

	markUnavailable(allocationB, mappingB, "upstream-b")
	options, err = model.ListAvailableChannelModels(
		t.Context(),
		[]string{"default"},
		rootID,
		[]dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: "/pg/chat/completions"}},
	)
	require.NoError(t, err)
	assert.Empty(t, options, "another user's Key and an explicit empty allowlist cannot rescue unavailable public Keys")
}
