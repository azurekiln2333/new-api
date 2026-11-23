package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedAccessTokenRequiresSensitiveChannelPermission(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelKeyEntry{}, &model.SystemTask{}))
	channel := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "scoped channel", Key: "first\nsecond", Group: "default", Models: "gpt-4o-mini",
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	require.NoError(t, channel.Insert())
	router := gin.New()
	router.Use(middleware.AccessTokenAudit())
	const updatePath = "/api/channel/"
	const managePath = "/api/channel/multi_key/manage"
	middleware.DeclareAccessTokenPermissionRoute(http.MethodPut, updatePath, authz.ChannelWrite)
	middleware.DeclareAccessTokenPermissionRoute(http.MethodPost, managePath, authz.ChannelOperate)
	router.PUT(updatePath, middleware.AdminAuth(), middleware.RequirePermission(authz.ChannelWrite), UpdateChannel)
	router.POST(managePath, middleware.AdminAuth(), middleware.RequirePermission(authz.ChannelOperate), ManageMultiKeys)
	baseScopes := []string{service.AccessTokenScopeOf(authz.ChannelWrite), service.AccessTokenScopeOf(authz.ChannelOperate)}
	limited, _ := createScopedAccessToken(t, user.Id, 0, baseScopes...)
	allowed, _ := createScopedAccessToken(t, user.Id, 0, append(baseScopes, service.AccessTokenScopeOf(authz.ChannelSensitiveWrite))...)
	allocation, ok := channel.ChannelAllocationByPosition(0)
	require.True(t, ok)
	baseURL := "https://example.com"
	update := fmt.Sprintf(`{"id":%d,"base_url":%q}`, channel.Id, baseURL)
	manage := fmt.Sprintf(`{"channel_id":%d,"action":"set_key_models","key_entry_id":"%d","expected_rev":%d,"models":["gpt-4o-mini"]}`, channel.Id, allocation.EntryID, allocation.Revision)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, updatePath, update},
		{http.MethodPost, managePath, manage},
	} {
		denied := accessTokenRequest(router, tc.method, tc.path, limited, "", tc.body)
		assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
		assert.Contains(t, denied.Body.String(), "ACCESS_TOKEN_SCOPE_DENIED")
	}
	unchanged, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, channel.GetBaseURL(), unchanged.GetBaseURL())
	assert.Empty(t, unchanged.ChannelInfo.MultiKeyModels)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, managePath, manage},
		{http.MethodPut, updatePath, update},
	} {
		granted := accessTokenRequest(router, tc.method, tc.path, allowed, "", tc.body)
		require.Equal(t, http.StatusOK, granted.Code, granted.Body.String())
		var result struct{ Success bool }
		require.NoError(t, common.Unmarshal(granted.Body.Bytes(), &result))
		assert.True(t, result.Success, granted.Body.String())
	}
	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, baseURL, updated.GetBaseURL())
	assert.Equal(t, []string{"gpt-4o-mini"}, updated.ChannelInfo.MultiKeyModels[0])
}

func TestChannelHasSensitiveChanges(t *testing.T) {
	baseURL := "https://api.example.com"
	headerOverride := `{"Authorization":"Bearer {api_key}"}`
	origin := &model.Channel{
		Type:           1,
		Key:            "old-key",
		BaseURL:        &baseURL,
		HeaderOverride: &headerOverride,
		Models:         "gpt-4o",
		Group:          "default",
	}

	t.Run("non-sensitive routing fields", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Models = "gpt-4o,gpt-4o-mini"
		updated.Group = "vip"

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"models": updated.Models,
			"group":  updated.Group,
		}))
	})

	t.Run("key change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Key = "new-key"

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key": updated.Key}))
	})

	t.Run("base url change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newBaseURL := "https://leak.example.com"
		updated.BaseURL = &newBaseURL

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"base_url": newBaseURL}))
	})

	t.Run("header override change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newHeaderOverride := `{"X-Key":"{api_key}"}`
		updated.HeaderOverride = &newHeaderOverride

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"header_override": newHeaderOverride}))
	})

	t.Run("omitted sensitive fields do not use zero values", func(t *testing.T) {
		updated := PatchChannel{}
		updated.Id = origin.Id
		updated.Priority = origin.Priority

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"priority": 10}))
	})

	t.Run("unknown field fails closed", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"future_secret_field": "x"}))
	})

	t.Run("status is operational", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Status = common.ChannelStatusManuallyDisabled

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"status": updated.Status}))
	})

	t.Run("read-only fields are ignored by sensitivity check", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Balance = 99
		updated.UsedQuota = 100
		updated.ResponseTime = 200

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"balance":       updated.Balance,
			"used_quota":    updated.UsedQuota,
			"response_time": updated.ResponseTime,
		}))
	})
}

func TestClearChannelReadOnlyFields(t *testing.T) {
	channel := PatchChannel{Channel: model.Channel{
		CreatedTime:        11,
		TestTime:           22,
		ResponseTime:       33,
		Balance:            44.5,
		BalanceUpdatedTime: 55,
		UsedQuota:          66,
		Models:             "gpt-4o",
		Group:              "default",
	}}

	clearChannelReadOnlyFields(&channel, map[string]any{
		"created_time":         channel.CreatedTime,
		"test_time":            channel.TestTime,
		"response_time":        channel.ResponseTime,
		"balance":              channel.Balance,
		"balance_updated_time": channel.BalanceUpdatedTime,
		"used_quota":           channel.UsedQuota,
		"models":               channel.Models,
		"group":                channel.Group,
	})

	assert.Zero(t, channel.CreatedTime)
	assert.Zero(t, channel.TestTime)
	assert.Zero(t, channel.ResponseTime)
	assert.Zero(t, channel.Balance)
	assert.Zero(t, channel.BalanceUpdatedTime)
	assert.Zero(t, channel.UsedQuota)
	assert.Equal(t, "gpt-4o", channel.Models)
	assert.Equal(t, "default", channel.Group)
}

func TestUpdateChannelRejectsStatusField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/channel/",
		bytes.NewBufferString(`{"id":1,"status":2}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	UpdateChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
}

func TestChannelStatusValidation(t *testing.T) {
	assert.True(t, isManageableChannelStatus(common.ChannelStatusEnabled))
	assert.True(t, isManageableChannelStatus(common.ChannelStatusManuallyDisabled))
	assert.False(t, isManageableChannelStatus(common.ChannelStatusAutoDisabled))
	assert.False(t, isManageableChannelStatus(0))
}

// TestChannelFieldsAreClassified guards the fail-closed sensitivity check: every
// JSON field of PatchChannel (including the embedded model.Channel) must be listed
// in channelSensitiveFields, channelNonSensitiveFields, or
// channelOperationalFields. A newly added field that is left unclassified will
// fail this test, forcing a conscious permission decision instead of silently
// defaulting either way.
func TestChannelFieldsAreClassified(t *testing.T) {
	classified := func(name string) bool {
		if _, ok := channelSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelNonSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelOperationalFields[name]; ok {
			return true
		}
		_, ok := channelReadOnlyFields[name]
		return ok
	}

	var collect func(rt reflect.Type) []string
	collect = func(rt reflect.Type) []string {
		var names []string
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				names = append(names, collect(field.Type)...)
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			names = append(names, name)
		}
		return names
	}

	for _, name := range collect(reflect.TypeFor[PatchChannel]()) {
		assert.Truef(t, classified(name),
			"channel field %q is not classified; add it to channelSensitiveFields, channelNonSensitiveFields, channelOperationalFields, or channelReadOnlyFields in channel_authz.go", name)
	}
}

func TestRestoreChannelModelAvailability(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	channel := model.Channel{Type: 1, Name: "model-restore", Key: "private-key", Status: common.ChannelStatusEnabled, Models: "model-a,model-b", Group: "default"}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { require.NoError(t, channel.Delete()) })
	for _, name := range []string{"model-a", "model-b"} {
		written, err := model.RecordChannelModelUnavailability(channel.Id, name, channel.Key, "", name, http.StatusNotFound, model.ChannelModelUnavailableEvidenceCode)
		require.NoError(t, err)
		require.True(t, written)
	}
	for _, body := range []string{`{"model":"model-a"}`, `{}`} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/channel/%d/model_unavailable/restore", channel.Id), strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channel.Id)}}
		ctx.Set("id", rootID)
		ctx.Set("role", common.RoleRootUser)
		RestoreChannelModelUnavailability(ctx)
		assert.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool `json:"success"`
			Data    bool `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.True(t, response.Data)
		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, "model-a,model-b", stored.Models)
		if body != `{}` {
			require.Len(t, stored.ChannelInfo.ModelUnavailable, 1)
			assert.Equal(t, "model-b", stored.ChannelInfo.ModelUnavailable[0].UpstreamModel)
		} else {
			assert.Empty(t, stored.ChannelInfo.ModelUnavailable)
		}
	}
}
