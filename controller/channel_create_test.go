package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
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

func TestChannelUpstreamURLsPersistAndPreserveLegacySaves(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic} {
		t.Run(fmt.Sprintf("type=%d", channelType), func(t *testing.T) {
			previousCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = true
			t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
			const scalar = "https://legacy.example.test/prefix"
			setting := `{"base_urls":[" https://first.example.test/api/v1 ","http://second.example.test/prefix"],"homepage_url":" https://home.example.test/docs?q=guide#start ","future_option":{"keep":true},"proxy":""}`
			payload, err := common.Marshal(AddChannelRequest{Mode: "single", Channel: &model.Channel{
				Name: t.Name(), Type: channelType, Key: "upstream-url-fixture-secret", Models: "test-model", Group: "default", BaseURL: common.GetPointer(scalar), Setting: &setting,
				ChannelInfo: model.ChannelInfo{MultiKeyUpstreamProtocols: map[int]string{0: "responses"}},
			}})
			require.NoError(t, err)
			response := postAddChannel(t, rootID, common.RoleRootUser, string(payload))
			var result struct {
				Success bool `json:"success"`
				Data struct { ID int `json:"id"` } `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			created, err := model.GetChannelById(result.Data.ID, true)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, created.Delete()) })
			assert.Equal(t, "inherit", created.KeyUpstreamProtocol(0), "creation cannot assign the read-only allocation projection")
			assert.Equal(t, scalar, created.GetBaseURL(), "the runtime scalar getter is unchanged")
			assert.Equal(t, []string{"https://first.example.test/api/v1", "http://second.example.test/prefix"}, created.GetBaseURLs())
			assert.Equal(t, "https://home.example.test/docs?q=guide#start", created.GetHomepageURL())
			assert.Contains(t, *created.Setting, `"future_option":{"keep":true}`)
			poolCopy := created.GetBaseURLs()
			poolCopy[0] = "https://caller.example.test"
			assert.Equal(t, "https://first.example.test/api/v1", created.GetBaseURLs()[0])
			model.InitChannelCache()
			response = putUpdateChannel(t, rootID, common.RoleRootUser, fmt.Sprintf(`{"id":%d,"type":%d}`, created.Id, constant.ChannelTypeAzure))
			var rejected struct { Success bool `json:"success"` }
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &rejected))
			assert.False(t, rejected.Success, "an omitted setting must not bypass the pool provider guard")
			unchanged, err := model.GetChannelById(created.Id, true)
			require.NoError(t, err)
			assert.Equal(t, channelType, unchanged.Type)
			assert.Equal(t, created.Setting, unchanged.Setting)
			// The typed channel helper uses the freshly locked row, not the
			// controller's pre-transaction read, for omitted additive properties.
			newer, err := model.UpdateChannelConfigAtomic(created.Id, model.ChannelConfigEdit{Apply: func(_ *model.Channel, stored *model.Channel) error {
				stored.Setting = common.GetPointer(`{"base_urls":["https://concurrent.example.test/v1"],"homepage_url":"https://concurrent.example.test/home","future_option":{"keep":true}}`)
				return nil
			}})
			require.NoError(t, err)
			legacy, err := model.UpdateChannelConfigAtomic(created.Id, model.ChannelConfigEdit{Apply: func(_ *model.Channel, stored *model.Channel) error {
				stored.Setting = common.GetPointer(`{"proxy":"","future_option":{"keep":true}}`)
				return nil
			}})
			require.NoError(t, err)
			assert.Equal(t, newer.GetBaseURLs(), legacy.GetBaseURLs())
			assert.Equal(t, newer.GetHomepageURL(), legacy.GetHomepageURL())
			_, err = model.UpdateChannelConfigAtomic(created.Id, model.ChannelConfigEdit{Apply: func(_ *model.Channel, stored *model.Channel) error {
				stored.Setting = created.Setting
				return nil
			}})
			require.NoError(t, err)

			for _, tc := range []struct {
				name string
				setting *string
				wantPool []string
				wantHome string
			}{
				{name: "omitted setting", wantPool: []string{"https://first.example.test/api/v1", "http://second.example.test/prefix"}, wantHome: "https://home.example.test/docs?q=guide#start"},
				{name: "old editor", setting: common.GetPointer(`{"proxy":"","future_option":{"keep":true}}`), wantPool: []string{"https://first.example.test/api/v1", "http://second.example.test/prefix"}, wantHome: "https://home.example.test/docs?q=guide#start"},
				{name: "null homepage", setting: common.GetPointer(`{"homepage_url":null,"future_option":{"keep":true}}`), wantPool: []string{"https://first.example.test/api/v1", "http://second.example.test/prefix"}, wantHome: "https://first.example.test/api/v1"},
				{name: "empty pool", setting: common.GetPointer(`{"base_urls":[],"homepage_url":"","future_option":{"keep":true}}`), wantPool: []string{scalar}, wantHome: scalar},
				{name: "replace pool", setting: common.GetPointer(`{"base_urls":[" https://new.example.test/path "],"future_option":{"keep":true}}`), wantPool: []string{"https://new.example.test/path"}, wantHome: "https://new.example.test/path"},
				{name: "null pool", setting: common.GetPointer(`{"base_urls":null,"future_option":{"keep":true}}`), wantPool: []string{scalar}, wantHome: scalar},
			} {
				t.Run(tc.name, func(t *testing.T) {
					patch := map[string]any{"id": created.Id, "name": tc.name}
					if tc.setting != nil { patch["setting"] = *tc.setting }
					payload, err := common.Marshal(patch)
					require.NoError(t, err)
					response := putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
					var saved struct { Success bool `json:"success"` }
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &saved))
					require.True(t, saved.Success, response.Body.String())
					stored, err := model.GetChannelById(created.Id, true)
					require.NoError(t, err)
					assert.Equal(t, tc.wantPool, stored.GetBaseURLs())
					assert.Equal(t, tc.wantHome, stored.GetHomepageURL())
					assert.Equal(t, scalar, stored.GetBaseURL())
					assert.Contains(t, *stored.Setting, `"future_option":{"keep":true}`)
					assert.Equal(t, created.Key, stored.Key)
					cached, err := model.CacheGetChannel(created.Id)
					require.NoError(t, err)
					assert.Equal(t, stored.Setting, cached.Setting, "the committed settings publish immediately")
					assert.NotContains(t, response.Body.String(), created.Key)
				})
			}
		})
	}
	maxPool := make([]string, 16)
	for i := range maxPool {
		maxPool[i] = fmt.Sprintf("https://boundary-%d.example.test/v1", i)
	}
	prefix := "https://length.example.test/"
	maxPool[0] = prefix + strings.Repeat("x", 2048-len(prefix))
	encoded, err := common.Marshal(map[string]any{"base_urls": maxPool})
	require.NoError(t, err)
	boundary := &model.Channel{Type: constant.ChannelTypeOpenAI, Setting: common.GetPointer(string(encoded))}
	require.NoError(t, boundary.NormalizeUpstreamSettings())
	assert.Equal(t, maxPool, boundary.GetBaseURLs(), "both inclusive URL bounds are accepted")
	fallback := &model.Channel{Type: constant.ChannelTypeOpenAI}
	assert.Equal(t, []string{constant.GetChannelBaseURL(fallback.Type)}, fallback.GetBaseURLs())
	assert.Equal(t, constant.GetChannelBaseURL(fallback.Type), fallback.GetHomepageURL())
}

func TestChannelUpstreamURLsRejectUnsafeConfiguration(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	tooMany := make([]string, 17)
	for i := range tooMany { tooMany[i] = fmt.Sprintf("https://pool-%d.example.test/path", i) }
	for _, tc := range []struct {
		name string
		channelType int
		settings map[string]any
	}{
		{name: "too many", settings: map[string]any{"base_urls": tooMany}},
		{name: "duplicate trimmed", settings: map[string]any{"base_urls": []string{"https://a.example.test", " https://a.example.test "}}},
		{name: "empty member", settings: map[string]any{"base_urls": []string{" "}}},
		{name: "relative", settings: map[string]any{"base_urls": []string{"/v1"}}},
		{name: "scheme", settings: map[string]any{"base_urls": []string{"file:///tmp/key"}}},
		{name: "userinfo", settings: map[string]any{"base_urls": []string{"https://user:password@example.test"}}},
		{name: "query", settings: map[string]any{"base_urls": []string{"https://example.test/v1?key=secret"}}},
		{name: "fragment", settings: map[string]any{"base_urls": []string{"https://example.test/v1#section"}}},
		{name: "backslash", settings: map[string]any{"base_urls": []string{`https://example.test\path`}}},
		{name: "control", settings: map[string]any{"base_urls": []string{"https://example.test/path\n"}}},
		{name: "escaped control", settings: map[string]any{"base_urls": []string{"https://example.test/%0a"}}},
		{name: "escaped backslash", settings: map[string]any{"base_urls": []string{"https://example.test/%5cpath"}}},
		{name: "oversized", settings: map[string]any{"base_urls": []string{"https://example.test/" + strings.Repeat("x", 2048)}}},
		{name: "homepage credentials", settings: map[string]any{"homepage_url": "https://user:password@example.test/docs"}},
		{name: "homepage scheme", settings: map[string]any{"homepage_url": "javascript:alert(1)"}},
		{name: "homepage control", settings: map[string]any{"homepage_url": "https://example.test/docs?x=%0d"}},
		{name: "unsupported pool", channelType: constant.ChannelTypeAzure, settings: map[string]any{"base_urls": []string{"https://example.test"}}},
		{name: "unsupported homepage", channelType: constant.ChannelTypeAzure, settings: map[string]any{"homepage_url": "https://example.test/docs"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channelType := tc.channelType
			if channelType == 0 { channelType = constant.ChannelTypeOpenAI }
			encoded, err := common.Marshal(tc.settings)
			require.NoError(t, err)
			payload, err := common.Marshal(AddChannelRequest{Mode: "single", Channel: &model.Channel{Name: t.Name(), Type: channelType, Key: "unsafe-url-fixture-secret", Models: "m", Group: "default", Setting: common.GetPointer(string(encoded))}})
			require.NoError(t, err)
			response := postAddChannel(t, rootID, common.RoleRootUser, string(payload))
			var result struct { Success bool `json:"success"` }
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success, response.Body.String())
			assert.NotContains(t, response.Body.String(), "password")
			assert.NotContains(t, response.Body.String(), "unsafe-url-fixture-secret")
			var count int64
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("name = ?", t.Name()).Count(&count).Error)
			assert.Zero(t, count)
			channel := &model.Channel{Name: t.Name(), Type: channelType, Key: "unsafe-url-fixture-secret", Models: "m", Group: "default", Setting: common.GetPointer(`{"future_option":true}`)}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() { require.NoError(t, channel.Delete()) })
			payload, err = common.Marshal(map[string]any{"id": channel.Id, "name": "must-not-save", "setting": string(encoded)})
			require.NoError(t, err)
			response = putUpdateChannel(t, rootID, common.RoleRootUser, string(payload))
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success, response.Body.String())
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, channel.Name, stored.Name)
			assert.Equal(t, channel.Setting, stored.Setting)
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
