package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelCreateTest(t *testing.T) int {
	t.Helper()
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
	// A follower requires the activation marker table to exist, even when
	// enforcement is disabled. Initialize that production startup prerequisite.
	var seed *gorm.DB
	if dsn := os.Getenv("TEST_CHANNEL_SQL_DSN"); dsn != "" {
		kind := "mysql"
		if strings.HasPrefix(dsn, "postgres") {
			kind = "postgres"
		}
		// Listing totals and atomic-write assertions require an empty database,
		// not the service database used by earlier focused cases and CI steps.
		var isolatedDSN string
		seed, isolatedDSN = newAuditTestDatabase(t, kind, dsn)
		t.Setenv("SQL_DSN", isolatedDSN)
	} else {
		var err error
		seed, err = gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
		require.NoError(t, err)
	}
	require.NoError(t, seed.AutoMigrate(&model.Option{}))
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	require.NoError(t, seed.Where(map[string]any{"key": "internal.channel_budget.active_v1"}).Delete(&model.Option{}).Error)
	seedSQL, err := seed.DB()
	require.NoError(t, err)
	require.NoError(t, seedSQL.Close())
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.AuditLog{}, &model.ChannelKeyEntry{}, &model.AuditEventOutbox{}, &model.ChannelBudgetAccount{}, &model.ChannelBudgetReservation{}, &model.ChannelBudgetReservationItem{}, &model.ChannelBudgetOperation{}, &model.Task{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	root := &model.User{Username: "channel-create-review-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(root).Error)
	t.Cleanup(func() { require.NoError(t, database.Unscoped().Delete(root).Error) })
	versionQuery := "SELECT VERSION()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)
	return root.Id
}

func TestChannelModelIDCasePolicy(t *testing.T) {
	setupChannelCreateTest(t)
	common.MemoryCacheEnabled = true
	settings := model_setting.GetGlobalSettings()
	previous := settings.ModelIDCaseInsensitive
	t.Cleanup(func() { settings.ModelIDCaseInsensitive = previous })
	settings.ModelIDCaseInsensitive = false
	for _, tc := range []struct {
		name, models, request, override, other, want, catalog, wantError string
		global, priced                                               bool
		userID                                                       int
		info                                                         model.ChannelInfo
	}{
		{name: "default off retains exact", models: "Case-Policy-Model", request: "Case-Policy-Model", want: "Case-Policy-Model", catalog: "Case-Policy-Model"},
		{name: "default off rejects variant", models: "Case-Policy-Model", request: "case-policy-model", wantError: "disabled"},
		{name: "global inherited", models: "Case-Policy-Model", request: "case-policy-model", global: true, want: "Case-Policy-Model", catalog: "Case-Policy-Model"},
		{name: "explicit enabled", models: "Case-Policy-Model", request: "case-policy-model", override: `{"model_id_case_insensitive":true}`, want: "Case-Policy-Model", catalog: "Case-Policy-Model"},
		{name: "explicit disabled overrides global", models: "Case-Policy-Model", request: "case-policy-model", global: true, override: `{"model_id_case_insensitive":false}`, wantError: "disabled"},
		{name: "null inherits", models: "Case-Policy-Model", request: "case-policy-model", global: true, override: `{"model_id_case_insensitive":null}`, want: "Case-Policy-Model", catalog: "Case-Policy-Model"},
		{name: "separate price cannot be substituted", models: "Case-Policy-Model", request: "case-policy-model", global: true, priced: true, wantError: "separate pricing"},
		{name: "exact collision wins", models: "Case-Policy-Model,case-policy-model", request: "case-policy-model", global: true, want: "case-policy-model", catalog: "case-policy-model"},
		{name: "folded collision rejects", models: "Case-Policy-Model,case-policy-model", request: "CASE-POLICY-MODEL", global: true, wantError: "ambiguous"},
		{name: "group scope remains exact", models: "Case-Policy-Model,case-policy-model", request: "CASE-POLICY-MODEL", global: true, other: `{"group_models":{"default":["Case-Policy-Model"]}}`, want: "Case-Policy-Model", catalog: "Case-Policy-Model"},
		{name: "group cannot borrow spelling", models: "Case-Policy-Model", request: "case-policy-model", global: true, other: `{"group_models":{"default":["case-policy-model"]}}`, want: "case-policy-model"},
		{name: "legacy alias retained", models: "claude-3-7-sonnet", request: "claude-3-7-sonnet-thinking", want: "claude-3-7-sonnet-thinking", catalog: "claude-3-7-sonnet"},
		{name: "unlisted remains unavailable", models: "Case-Policy-Model", request: "other-model", global: true, want: "other-model"},
		{name: "non ASCII is not folded", models: "Ä-model", request: "ä-model", global: true, want: "ä-model"},
		{name: "own bound scope", models: "Case-Policy-Model", request: "case-policy-model", global: true, userID: 7, want: "Case-Policy-Model", catalog: "Case-Policy-Model", info: model.ChannelInfo{IsMultiKey: true, MultiKeyUsers: map[int]int{0: 7}, MultiKeyScopeModes: map[int]string{0: "allowlist"}, MultiKeyModels: map[int][]string{0: {"Case-Policy-Model"}}}},
		{name: "other bound scope is not a grant", models: "Case-Policy-Model", request: "case-policy-model", global: true, userID: 8, want: "case-policy-model", info: model.ChannelInfo{IsMultiKey: true, MultiKeyUsers: map[int]int{0: 7}}},
		{name: "empty allowlist is deny all", models: "Case-Policy-Model", request: "case-policy-model", global: true, want: "case-policy-model", info: model.ChannelInfo{IsMultiKey: true, MultiKeyScopeModes: map[int]string{0: "allowlist"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.ModelIDCaseInsensitive = tc.global
			if tc.priced {
				previousPrices := ratio_setting.ModelPrice2JSONString()
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"case-policy-model":0.01}`))
				t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
			}
			channel := model.Channel{Name: tc.name, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
				Key: "case-policy-test-key", Models: tc.models, Group: "default", OtherSettings: tc.other, ChannelInfo: tc.info}
			if tc.override != "" {
				channel.Setting = &tc.override
			}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() {
				_, err := model.BatchDeleteChannels([]int{channel.Id})
				require.NoError(t, err)
			})
			model.InitChannelCache()
			for _, cache := range []bool{false, true} {
				common.MemoryCacheEnabled = cache
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Set("id", tc.userID)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				canonical, err := service.ResolveRequestModelIdentity(c, tc.request)
				if tc.wantError != "" {
					require.ErrorContains(t, err, tc.wantError)
					continue
				}
				require.NoError(t, err)
				assert.Equal(t, tc.want, canonical)
				assert.Equal(t, tc.request, common.GetContextKeyString(c, constant.ContextKeyRequestedModel))
				assert.Equal(t, tc.catalog, common.GetContextKeyString(c, constant.ContextKeyCatalogModel))
				if tc.catalog != "" {
					assert.True(t, middleware.TokenModelLimitAllows(map[string]bool{canonical: true}, canonical, tc.catalog))
					assert.False(t, middleware.TokenModelLimitAllows(map[string]bool{"unrelated": true}, canonical, tc.catalog))
					request := &dto.OpenAIResponsesRequest{Model: tc.request}
					info := relaycommon.GenRelayInfoResponses(c, request)
					assert.Equal(t, canonical, request.Model, "request pricing and routing use the same identity")
					assert.Equal(t, tc.request, info.RequestedModelName)
				}
				allowed, _ := model.ChannelSatisfiesFilters(&channel, canonical, service.GetChannelConstraints(c).Filters, tc.userID)
				assert.Equal(t, tc.catalog != "", allowed)
			}
			common.MemoryCacheEnabled = true
		})
	}
}

func TestChannelModelIDCatalogFenceAndPassthroughAdmission(t *testing.T) {
	setupChannelCreateTest(t)
	common.MemoryCacheEnabled = true
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.ModelIDCaseInsensitive = true
	settings.PassThroughRequestEnabled = true
	channels := []model.Channel{
		{Name: "exact", Models: "claude-3-7-sonnet-thinking"},
		{Name: "legacy fallback", Models: "claude-3-7-sonnet"},
		{Name: "case folded", Models: "Case-Policy-Model"},
	}
	for index := range channels {
		channel := &channels[index]
		channel.Type, channel.Status, channel.Group, channel.Key = constant.ChannelTypeOpenAI, common.ChannelStatusEnabled, "default", "case-policy-test-key"
		require.NoError(t, channel.Insert())
		t.Cleanup(func() {
			_, err := model.BatchDeleteChannels([]int{channel.Id})
			require.NoError(t, err)
		})
	}
	model.InitChannelCache()
	for _, cache := range []bool{false, true} {
		common.MemoryCacheEnabled = cache
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		canonical, err := service.ResolveRequestModelIdentity(c, "claude-3-7-sonnet-thinking")
		require.NoError(t, err)
		filters := service.GetChannelConstraints(c).Filters
		assert.False(t, middleware.TokenModelLimitAllows(map[string]bool{"claude-3-7-sonnet": true}, canonical, common.GetContextKeyString(c, constant.ContextKeyCatalogModel)), "a concrete exact model does not inherit a base model's token grant")
		allowed, _ := model.ChannelSatisfiesFilters(&channels[1], canonical, filters)
		assert.False(t, allowed, "an exact catalog identity cannot use another channel's legacy fallback")
		selected, err := model.GetUntriedSatisfiedChannel("default", canonical, []int{channels[0].Id}, filters)
		require.NoError(t, err)
		assert.Nil(t, selected, "retry cannot discard the catalog fence")
		service.GetChannelConstraints(c).AddPin(appdto.ChannelPin{ChannelId: channels[1].Id, Source: appdto.PinSourceToken})
		_, _, selectErr := service.SelectChannelForRequest(c, canonical, &service.RetryParam{Ctx: c, ModelName: canonical, TokenGroup: "default"})
		require.NotNil(t, selectErr, "a pin is not a different model grant")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	const raw = `{"model":"case-policy-model","input":"hello","vendor_extension":{"seed":0}}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	canonical, err := service.ResolveRequestModelIdentity(c, "case-policy-model")
	require.NoError(t, err)
	require.Equal(t, "Case-Policy-Model", canonical)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channels[2], canonical))
	request := &dto.OpenAIResponsesRequest{Model: "case-policy-model", Input: []byte(`"hello"`)}
	info := relaycommon.GenRelayInfoResponses(c, request)
	info.InitChannelMeta(c)
	require.Nil(t, service.ReserveAttemptChannelBudget(c, info), "passthrough admits canonical models with the ledger off")
	_, body, closer, apiErr := relay.PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	defer closer.Close()
	wire, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"Case-Policy-Model","input":"hello","vendor_extension":{"seed":0}}`, string(wire))
	storedBody, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	original, err := storedBody.Bytes()
	require.NoError(t, err)
	assert.Equal(t, raw, string(original), "retry retains the immutable client body")
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channels[2].Id).Update("setting", `{"model_id_case_insensitive":false}`).Error)
	require.NotNil(t, service.ReserveAttemptChannelBudget(c, info), "live policy revocation rejects before dispatch")
}

func TestChannelUpdateCasePolicyPersistsAndKeepsReplacementSemantics(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	common.MemoryCacheEnabled = true
	settings := model_setting.GetGlobalSettings()
	previous := settings.ModelIDCaseInsensitive
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	settings.ModelIDCaseInsensitive = false
	t.Cleanup(func() {
		settings.ModelIDCaseInsensitive = previous
		common.OptionMap = previousOptions
	})
	require.NoError(t, model.UpdateOption("global.model_id_case_insensitive", "true"))
	settings.ModelIDCaseInsensitive = false
	require.NoError(t, model.LoadOptionsFromDatabase())
	require.True(t, settings.ModelIDCaseInsensitive, "the saved global override survives reload")
	require.Error(t, model.UpdateOption("global.model_id_case_insensitive", "not-a-bool"))
	require.True(t, settings.ModelIDCaseInsensitive, "invalid saves cannot change the live policy")
	channel := model.Channel{Name: t.Name(), Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: "case-policy-test-key", Models: "Case-Policy-Model", Group: "default", Setting: common.GetPointer(`{"model_id_case_insensitive":false,"old_marker":true}`)}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() {
		_, err := model.BatchDeleteChannels([]int{channel.Id})
		require.NoError(t, err)
	})
	model.InitChannelCache()
	for _, tc := range []struct {
		name, setting, expected string
		omit, wantEnabled      bool
	}{
		{name: "omitted setting", omit: true, expected: `{"model_id_case_insensitive":false,"old_marker":true}`},
		{name: "enabled", setting: `{"model_id_case_insensitive":true,"marker":true}`, expected: `{"model_id_case_insensitive":true,"marker":true}`, wantEnabled: true},
		{name: "older client retains only case override", setting: `{"proxy":""}`, expected: `{"proxy":"","model_id_case_insensitive":true}`, wantEnabled: true},
		{name: "explicit false", setting: `{"model_id_case_insensitive":false}`, expected: `{"model_id_case_insensitive":false}`},
		{name: "explicit null inherits", setting: `{"model_id_case_insensitive":null}`, expected: `{"model_id_case_insensitive":null}`, wantEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch := map[string]any{"id": channel.Id, "name": tc.name}
			if !tc.omit {
				patch["setting"] = tc.setting
			}
			payload, err := common.Marshal(patch)
			require.NoError(t, err)
			response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
			require.Equal(t, http.StatusOK, response.Code)
			var result struct{ Success bool }
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			require.NotNil(t, stored.Setting)
			assert.JSONEq(t, tc.expected, *stored.Setting)
			assert.Equal(t, tc.wantEnabled, stored.ModelIDCaseInsensitive())
			cached, err := model.CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, stored.Setting, cached.Setting)
		})
	}
}

func TestChannelUpdatePersistsModelMappingAndPublishesCache(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	common.MemoryCacheEnabled = true
	const clientModel = "deepseek-r1"
	payload, err := common.Marshal(AddChannelRequest{Mode: "single", Channel: &model.Channel{
		Name: t.Name(), Type: constant.ChannelTypeOpenAI, Key: "sk-mapping-save-fixture",
		Models: clientModel, Group: "default",
	}})
	require.NoError(t, err)
	response := postAddChannel(t, rootID, common.RoleRootUser, string(payload))
	require.Equal(t, http.StatusOK, response.Code)
	var created struct {
		Success bool `json:"success"`
		Data    struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	require.True(t, created.Success, response.Body.String())
	require.Positive(t, created.Data.ID)
	t.Cleanup(func() {
		_, err := model.BatchDeleteChannels([]int{created.Data.ID})
		require.NoError(t, err)
	})
	initial, err := model.GetChannelById(created.Data.ID, true)
	require.NoError(t, err)
	require.NotEmpty(t, initial.KeyEntryIDs)
	model.InitChannelCache()

	const firstMapping = `{"deepseek-r1":"DeepSeek-R1"}`
	const updatedMapping = `{"deepseek-r1":"DeepSeek-R1-Updated"}`
	for step, tc := range []struct {
		name        string
		mapping     *string
		omit        bool
		wantMapping string
		wantModel   string
		changed     bool
	}{
		{name: "add case-sensitive target", mapping: common.GetPointer(firstMapping), wantMapping: firstMapping, wantModel: "DeepSeek-R1", changed: true},
		{name: "edit existing mapping", mapping: common.GetPointer(updatedMapping), wantMapping: updatedMapping, wantModel: "DeepSeek-R1-Updated", changed: true},
		{name: "omitted mapping preserves value", omit: true, wantMapping: updatedMapping, wantModel: "DeepSeek-R1-Updated"},
		{name: "null mapping preserves value", wantMapping: updatedMapping, wantModel: "DeepSeek-R1-Updated"},
		{name: "empty object clears redirect", mapping: common.GetPointer(`{}`), wantMapping: `{}`, wantModel: clientModel, changed: true},
		{name: "restore redirect", mapping: common.GetPointer(firstMapping), wantMapping: firstMapping, wantModel: "DeepSeek-R1", changed: true},
		{name: "empty string clears redirect", mapping: common.GetPointer(""), wantModel: clientModel, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch := map[string]any{"id": initial.Id, "name": tc.name}
			if !tc.omit {
				patch["model_mapping"] = tc.mapping
			}
			payload, err := common.Marshal(patch)
			require.NoError(t, err)
			response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool          `json:"success"`
				Data    model.Channel `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.Equal(t, tc.wantMapping, result.Data.GetModelMapping())
			assert.Equal(t, tc.name, result.Data.Name)
			assert.NotContains(t, response.Body.String(), initial.Key)

			stored, err := model.GetChannelById(initial.Id, true)
			require.NoError(t, err)
			assert.Equal(t, tc.wantMapping, stored.GetModelMapping())
			assert.Equal(t, tc.name, stored.Name)
			assert.Equal(t, initial.Models, stored.Models)
			assert.Equal(t, initial.Key, stored.Key)
			assert.Equal(t, initial.KeyEntryIDs, stored.KeyEntryIDs)
			assert.Equal(t, initial.KeyEntryRevisions, stored.KeyEntryRevisions)
			cached, err := model.CacheGetChannel(initial.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.wantMapping, cached.GetModelMapping(), "the edit must publish before returning success")

			// Use the published mapping through the relay's real mapping helper.
			// The client spelling stays intact while the outgoing DTO uses the
			// administrator's exact upstream spelling, including case-only changes.
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Set("model_mapping", cached.GetModelMapping())
			request := &dto.GeneralOpenAIRequest{Model: clientModel}
			info := &relaycommon.RelayInfo{
				OriginModelName: clientModel,
				ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: clientModel},
			}
			require.NoError(t, helper.ModelMappedHelper(context, info, request))
			assert.Equal(t, clientModel, info.OriginModelName)
			assert.Equal(t, tc.wantModel != clientModel, info.IsModelMapped)
			assert.Equal(t, tc.wantModel, info.UpstreamModelName)
			encoded, err := common.Marshal(request)
			require.NoError(t, err)
			var outgoing struct {
				Model string `json:"model"`
			}
			require.NoError(t, common.Unmarshal(encoded, &outgoing))
			assert.Equal(t, tc.wantModel, outgoing.Model)

			var audits []model.AuditEventOutbox
			require.NoError(t, model.DB.Where("action = ?", "channel.update").Order("id").Find(&audits).Error)
			require.Len(t, audits, step+1, "each PUT must enqueue exactly one durable audit")
			changedFields := []string{}
			if tc.changed {
				changedFields = append(changedFields, "model_mapping")
			}
			expectedAudit, err := common.Marshal(map[string]any{
				"id": initial.Id, "name": tc.name, "changed_fields": changedFields,
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(expectedAudit), audits[step].ParamsJson, "audit records field names, not mapping contents or credentials")
		})
	}
}

func TestChannelSupportedEndpointsPersistThroughCreateAndUpdate(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, tc := range []struct {
		name          string
		initial       string
		initialType   int
		update        string
		updateType    int
		omitType      bool
		createError   string
		updateError   string
		wantEndpoints []string
	}{
		{name: "responses to chat", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["chat"]}`, wantEndpoints: []string{"chat"}},
		{name: "chat to both", initial: `{"supported_endpoints":["chat"]}`, update: `{"supported_endpoints":["chat","responses"]}`, wantEndpoints: []string{"chat", "responses"}},
		{name: "both to inherited", initial: `{"supported_endpoints":["chat","responses"]}`, update: `{}`},
		{name: "clear with null", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":null}`},
		{name: "clear with empty", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":[]}`, wantEndpoints: []string{}},
		{name: "legacy to responses", initial: `{}`, update: `{"supported_endpoints":["responses"]}`, wantEndpoints: []string{"responses"}},
		{name: "messages to all", initial: `{"supported_endpoints":["messages"]}`, update: `{"supported_endpoints":["messages","chat","responses"]}`, wantEndpoints: []string{"messages", "chat", "responses"}},
		{name: "all to messages", initial: `{"supported_endpoints":["chat","responses","messages"]}`, update: `{"supported_endpoints":["messages"]}`, wantEndpoints: []string{"messages"}},
		{name: "chat messages to responses messages", initial: `{"supported_endpoints":["chat","messages"]}`, update: `{"supported_endpoints":["responses","messages"]}`, wantEndpoints: []string{"responses", "messages"}},
		{name: "partial update to messages", initial: `{"supported_endpoints":["chat"]}`, update: `{"supported_endpoints":["messages"]}`, omitType: true, wantEndpoints: []string{"messages"}},
		{name: "invalid create", initial: `{"supported_endpoints":["realtime"]}`, createError: "supported_endpoints must contain only chat, responses or messages"},
		{name: "messages create rejects azure", initial: `{"supported_endpoints":["messages"]}`, initialType: constant.ChannelTypeAzure, createError: "supported_endpoints messages is only supported for the OpenAI channel type"},
		{name: "messages update rejects azure", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["messages"]}`, updateType: constant.ChannelTypeAzure, updateError: "supported_endpoints messages is only supported for the OpenAI channel type", wantEndpoints: []string{"responses"}},
		{name: "messages update rejects openai max", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["messages"]}`, updateType: constant.ChannelTypeOpenAIMax, updateError: "supported_endpoints messages is only supported for the OpenAI channel type", wantEndpoints: []string{"responses"}},
		{name: "messages update rejects openrouter", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["messages"]}`, updateType: constant.ChannelTypeOpenRouter, updateError: "supported_endpoints messages is only supported for the OpenAI channel type", wantEndpoints: []string{"responses"}},
		{name: "messages update rejects xinference", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["messages"]}`, updateType: constant.ChannelTypeXinference, updateError: "supported_endpoints messages is only supported for the OpenAI channel type", wantEndpoints: []string{"responses"}},
		{name: "partial messages rejects stored azure", initial: `{"supported_endpoints":["responses"]}`, initialType: constant.ChannelTypeAzure, update: `{"supported_endpoints":["messages"]}`, omitType: true, updateError: "supported_endpoints messages is only supported for the OpenAI channel type", wantEndpoints: []string{"responses"}},
		{name: "invalid update duplicate", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["chat","chat"]}`, updateError: "supported_endpoints must not contain duplicates", wantEndpoints: []string{"responses"}},
		{name: "invalid update passthrough", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["responses"],"pass_through_body_enabled":true}`, updateError: "supported_endpoints cannot be combined with pass_through_body_enabled", wantEndpoints: []string{"responses"}},
		{name: "invalid update provider", initial: `{"supported_endpoints":["responses"]}`, update: `{"supported_endpoints":["responses"]}`, updateType: constant.ChannelTypeAnthropic, updateError: "supported_endpoints is not supported for this channel type", wantEndpoints: []string{"responses"}},
		{name: "partial update uses stored provider", initial: `{"supported_endpoints":["chat"]}`, update: `{"supported_endpoints":["responses"]}`, omitType: true, wantEndpoints: []string{"responses"}},
		{name: "partial update rejects unsupported stored provider", initial: `{}`, initialType: constant.ChannelTypeAnthropic, update: `{"supported_endpoints":["responses"]}`, omitType: true, updateError: "supported_endpoints is not supported for this channel type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "endpoint-persistence-" + tc.name
			t.Cleanup(func() {
				var ids []int
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("name IN ?", []string{name, "updated-" + name}).Pluck("id", &ids).Error)
				_, err := model.BatchDeleteChannels(ids)
				require.NoError(t, err)
			})
			initialType := tc.initialType
			if initialType == 0 {
				initialType = constant.ChannelTypeOpenAI
			}
			request, err := common.Marshal(AddChannelRequest{Mode: "single", Channel: &model.Channel{
				Name: name, Type: initialType, Key: "sk-endpoint-persistence-fixture", Models: "test-model", Group: "default", Setting: &tc.initial,
			}})
			require.NoError(t, err)
			response := postAddChannel(t, rootID, common.RoleRootUser, string(request))
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			if tc.createError != "" {
				assert.False(t, result.Success)
				assert.Contains(t, response.Body.String(), tc.createError)
				var count int64
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", name).Count(&count).Error)
				assert.Zero(t, count, "invalid settings must not create a channel")
				return
			}
			require.True(t, result.Success, response.Body.String())
			require.Positive(t, result.Data.ID)
			created, err := model.GetChannelById(result.Data.ID, true)
			require.NoError(t, err)
			require.NotNil(t, created.Setting)
			assert.JSONEq(t, tc.initial, *created.Setting)

			channelType := tc.updateType
			if channelType == 0 {
				channelType = constant.ChannelTypeOpenAI
			}
			patch := map[string]any{
				"id": created.Id, "name": "updated-" + name,
				"models": "test-model", "group": "default", "setting": tc.update,
			}
			if !tc.omitType {
				patch["type"] = channelType
			}
			request, err = common.Marshal(patch)
			require.NoError(t, err)
			response = putUpdateChannel(t, rootID, common.RoleRootUser, string(request))
			require.Equal(t, http.StatusOK, response.Code)
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, tc.updateError == "", result.Success, response.Body.String())
			stored, err := model.GetChannelById(created.Id, true)
			require.NoError(t, err)
			require.NotNil(t, stored.Setting)
			if tc.updateError != "" {
				assert.Contains(t, response.Body.String(), tc.updateError)
				assert.JSONEq(t, tc.initial, *stored.Setting)
				assert.Equal(t, name, stored.Name, "rejected updates must not modify other fields")
				assert.Equal(t, initialType, stored.Type)
			} else {
				assert.JSONEq(t, tc.update, *stored.Setting)
				assert.Equal(t, "updated-"+name, stored.Name)
				assert.Equal(t, channelType, stored.Type)
			}
			assert.Equal(t, tc.wantEndpoints, stored.GetSetting().SupportedEndpoints)
			assert.Equal(t, created.Key, stored.Key)
		})
	}
}

func TestAddChannelReturnsCreatedIDs(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	const firstKey, secondKey = "sk-create-id-first", "sk-create-id-second"
	const vertexArray = `["", {"project_id":"one"}, " \n ", " {\"project_id\":\"two\"} ", {"project_id":"one"}, null, 42]`
	vertexKeys := []string{`{"project_id":"one"}`, `{"project_id":"two"}`, `{"project_id":"one"}`, "null", "42"}
	const vertexSingle = `{
  "project_id": "single",
  "type": "service_account"
}`
	batchKeys := make([]string, 51)
	batchIndices := make([]int, len(batchKeys))
	for i := range batchKeys {
		batchKeys[i] = fmt.Sprintf("sk-create-id-%02d", i)
		batchIndices[i] = i + 1
	}
	for _, tc := range []struct {
		name             string
		mode             string
		key              string
		channelType      int
		settings         string
		wantKeys         []string
		wantInputIndices []int
	}{
		{name: "single", mode: "single", key: firstKey, wantKeys: []string{firstKey}, wantInputIndices: []int{0}},
		{name: "batch duplicate keys", mode: "batch", key: "\n" + firstKey + "\n\n" + firstKey + "\n" + secondKey, wantKeys: []string{firstKey, firstKey, secondKey}, wantInputIndices: []int{1, 3, 4}},
		{name: "batch one channel", mode: "batch", key: firstKey + "\n", wantKeys: []string{firstKey}, wantInputIndices: []int{0}},
		{name: "batch preserves whitespace", mode: "batch", key: "\n \n" + firstKey, wantKeys: []string{" ", firstKey}, wantInputIndices: []int{1, 2}},
		{name: "multi to single", mode: "multi_to_single", key: "\n " + firstKey + " \n\n" + secondKey + "\n", wantKeys: []string{firstKey + "\n" + secondKey}, wantInputIndices: []int{1, 3}},
		{name: "multi preserves trimmed blank", mode: "multi_to_single", key: firstKey + "\n \n" + secondKey, wantKeys: []string{firstKey + "\n\n" + secondKey}, wantInputIndices: []int{0, 1, 2}},
		{name: "multi whitespace only", mode: "multi_to_single", key: " ", wantKeys: []string{}, wantInputIndices: []int{}},
		{name: "multiple chunks", mode: "batch", key: "\n" + strings.Join(batchKeys, "\n"), wantKeys: batchKeys, wantInputIndices: batchIndices},
		{name: "empty batch", mode: "batch", key: "\n", wantKeys: []string{}, wantInputIndices: []int{}},
		{name: "vertex array", mode: "batch", key: vertexArray, channelType: constant.ChannelTypeVertexAi, wantKeys: vertexKeys, wantInputIndices: []int{1, 3, 4, 5, 6}},
		{name: "vertex multi", mode: "multi_to_single", key: vertexArray, channelType: constant.ChannelTypeVertexAi, wantKeys: []string{strings.Join(vertexKeys, "\n")}, wantInputIndices: []int{1, 3, 4, 5, 6}},
		{name: "single multiline json", mode: "single", key: vertexSingle, channelType: constant.ChannelTypeVertexAi, wantKeys: []string{vertexSingle}, wantInputIndices: []int{0}},
		{name: "vertex api key batch", mode: "batch", key: "\n" + firstKey + "\n\n" + secondKey, channelType: constant.ChannelTypeVertexAi, settings: `{"vertex_key_type":"api_key"}`, wantKeys: []string{firstKey, secondKey}, wantInputIndices: []int{1, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "created-id-" + tc.name
			existing := &model.Channel{Name: name, Type: 1, Key: firstKey, Models: "test-model", Group: "default"}
			require.NoError(t, existing.Insert())
			t.Cleanup(func() {
				var ids []int
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", name).Pluck("id", &ids).Error)
				_, err := model.BatchDeleteChannels(ids)
				require.NoError(t, err)
			})
			channel := &model.Channel{Name: name, Type: tc.channelType, Key: tc.key, Models: "test-model", Group: "default", OtherSettings: tc.settings}
			if channel.Type == 0 {
				channel.Type = constant.ChannelTypeOpenAI
			}
			if channel.Type == constant.ChannelTypeVertexAi {
				channel.Other = `{"default":"us-central1"}`
			}
			request, err := common.Marshal(AddChannelRequest{Mode: tc.mode, Channel: channel})
			require.NoError(t, err)
			response := postAddChannel(t, rootID, common.RoleRootUser, string(request))
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
				Data    struct {
					ID      *int  `json:"id"`
					IDs     []int `json:"ids"`
					Results []struct {
						InputIndex int `json:"input_index"`
						ChannelID  int `json:"channel_id"`
					} `json:"results"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.Empty(t, result.Message)
			require.NotNil(t, result.Data.IDs, "empty creations must return an array, not null")
			require.Len(t, result.Data.IDs, len(tc.wantKeys))
			assert.NotContains(t, result.Data.IDs, existing.Id)
			var created []model.Channel
			require.NoError(t, model.DB.Where("name = ? AND id <> ?", name, existing.Id).Order("id").Find(&created).Error)
			require.Len(t, created, len(tc.wantKeys))
			for i, channel := range created {
				assert.Positive(t, result.Data.IDs[i])
				assert.Equal(t, channel.Id, result.Data.IDs[i])
				assert.Equal(t, tc.wantKeys[i], channel.Key)
				assert.Equal(t, tc.mode == "multi_to_single", channel.ChannelInfo.IsMultiKey)
			}
			require.NotNil(t, result.Data.Results, "empty creations must return an empty results array")
			require.Len(t, result.Data.Results, len(tc.wantInputIndices))
			results := make([]map[string]int, 0, len(tc.wantInputIndices))
			for i, inputIndex := range tc.wantInputIndices {
				channelIndex := i
				if tc.mode == "multi_to_single" {
					channelIndex = 0
				}
				assert.Equal(t, inputIndex, result.Data.Results[i].InputIndex)
				assert.Equal(t, created[channelIndex].Id, result.Data.Results[i].ChannelID)
				results = append(results, map[string]int{"input_index": inputIndex, "channel_id": created[channelIndex].Id})
			}
			if tc.mode == "multi_to_single" && len(created) == 1 {
				assert.Equal(t, len(tc.wantInputIndices), created[0].ChannelInfo.MultiKeySize)
			}
			data := map[string]any{"ids": result.Data.IDs, "results": results}
			if len(created) == 1 {
				require.NotNil(t, result.Data.ID)
				assert.Equal(t, created[0].Id, *result.Data.ID)
				data["id"] = created[0].Id
			} else {
				assert.Nil(t, result.Data.ID)
			}
			expected, err := common.Marshal(map[string]any{"success": true, "message": "", "data": data})
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), response.Body.String(), "the response must contain only IDs, not channel credentials")
		})
	}
}

func TestAddChannelInvalidVertexBatchDoesNotReturnResults(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, mode := range []string{"batch", "multi_to_single"} {
		for _, key := range []string{"[]", `["", "  "]`, "[{"} {
			t.Run(mode+"/"+key, func(t *testing.T) {
				request, err := common.Marshal(AddChannelRequest{Mode: mode, Channel: &model.Channel{
					Name: "invalid-vertex-array", Type: constant.ChannelTypeVertexAi, Key: key,
					Models: "test-model", Group: "default", Other: `{"default":"us-central1"}`,
				}})
				require.NoError(t, err)
				response := postAddChannel(t, rootID, common.RoleRootUser, string(request))
				assert.Equal(t, http.StatusOK, response.Code)
				var result map[string]any
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, false, result["success"])
				assert.NotEmpty(t, result["message"])
				assert.NotContains(t, result, "data")
				var count int64
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", "invalid-vertex-array").Count(&count).Error)
				assert.Zero(t, count)
			})
		}
	}
}

func TestAddChannelRollbackDoesNotReturnIDs(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	// Force a real database error in the second chunk, after the first 50 rows
	// received IDs. This constraint exists only in the isolated test database.
	column := `"key"`
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		column = "`key`(191)"
	}
	const indexName = "channel_create_test_unique_key"
	require.NoError(t, model.DB.Exec("CREATE UNIQUE INDEX "+indexName+" ON channels ("+column+")").Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Migrator().DropIndex(&model.Channel{}, indexName)) })
	keys := make([]string, 51)
	for i := range keys[:50] {
		keys[i] = fmt.Sprintf("sk-create-rollback-%02d", i)
	}
	keys[50] = keys[0]
	request, err := common.Marshal(AddChannelRequest{Mode: "batch", Channel: &model.Channel{
		Name: "created-id-rollback", Type: 1, Key: strings.Join(keys, "\n"), Models: "test-model", Group: "default",
	}})
	require.NoError(t, err)
	response := postAddChannel(t, rootID, common.RoleRootUser, string(request))
	assert.Equal(t, http.StatusOK, response.Code)
	var result map[string]any
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.Equal(t, false, result["success"])
	assert.NotEmpty(t, result["message"])
	assert.NotContains(t, result, "data")
	var channels, abilities int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", "created-id-rollback").Count(&channels).Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).Count(&abilities).Error)
	assert.Zero(t, channels)
	assert.Zero(t, abilities)
}

func TestAddChannelPanicRollsBackWithoutSuccess(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	database := model.DB
	channelCreates := 0
	const callback = "test:channel-create-panic"
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "channels" {
			channelCreates++
			if channelCreates == 2 {
				panic("second channel chunk failed")
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, database.Callback().Create().Remove(callback)) })
	keys := make([]string, 51)
	for i := range keys {
		keys[i] = fmt.Sprintf("sk-create-panic-%02d", i)
	}
	request, err := common.Marshal(AddChannelRequest{Mode: "batch", Channel: &model.Channel{
		Name: "created-id-panic", Type: 1, Key: strings.Join(keys, "\n"), Models: "test-model", Group: "default",
	}})
	require.NoError(t, err)
	require.PanicsWithValue(t, "second channel chunk failed", func() {
		postAddChannel(t, rootID, common.RoleRootUser, string(request))
	}, "a rolled-back transaction must not reach the success response")
	assert.Equal(t, 2, channelCreates)
	var channels, abilities int64
	require.NoError(t, database.Model(&model.Channel{}).Where("name = ?", "created-id-panic").Count(&channels).Error)
	require.NoError(t, database.Model(&model.Ability{}).Count(&abilities).Error)
	assert.Zero(t, channels)
	assert.Zero(t, abilities)
}
