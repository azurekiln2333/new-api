package controller

import (
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
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
