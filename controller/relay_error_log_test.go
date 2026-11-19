package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"101"}, adminInfo["use_channel"])

	logs, total, err := model.GetUserLogs(7, model.LogFilter{Type: model.LogTypeError}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Equal(t, channelSnapshot.ChannelId, logs[0].ChannelId)
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}

	for _, tc := range []struct {
		name      string
		inbound   types.RelayFormat
		request   any
		path      []types.RelayFormat
		wantChain []any
		wantError string
		claude    bool
	}{
		{name: "native", inbound: types.RelayFormatClaude, wantChain: []any{"Claude Messages"}, claude: true},
		{
			name: "direct conversion", inbound: types.RelayFormatClaude,
			request: &dto.ClaudeRequest{Model: "gpt-test", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}},
			path:    []types.RelayFormat{types.RelayFormatOpenAIResponses}, wantChain: []any{"Claude Messages", "OpenAI Responses"},
		},
		{
			name: "intermediate conversion", inbound: types.RelayFormatClaude,
			request:   &dto.ClaudeRequest{Model: "gpt-test", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}},
			path:      []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses},
			wantChain: []any{"Claude Messages", "OpenAI Compatible", "OpenAI Responses"},
		},
		{
			name: "converted Claude upstream", inbound: types.RelayFormatOpenAI,
			request: &dto.GeneralOpenAIRequest{Model: "gpt-test", Messages: []dto.Message{{Role: "user", Content: "hello"}}},
			path:    []types.RelayFormat{types.RelayFormatClaude}, wantChain: []any{"OpenAI Compatible", "Claude Messages"}, claude: true,
		},
		{
			name: "conversion rejected before first hop", inbound: types.RelayFormatOpenAI,
			request: &dto.GeneralOpenAIRequest{}, path: []types.RelayFormat{types.RelayFormatOpenAIResponses},
			wantChain: []any{"OpenAI Compatible"}, wantError: "model is required",
		},
		{
			name: "conversion rejected after intermediate hop", inbound: types.RelayFormatClaude,
			request:   &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}},
			path:      []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses},
			wantChain: []any{"Claude Messages", "OpenAI Compatible"}, wantError: "model is required",
		},
		{name: "missing historical metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{RelayFormat: tc.inbound, ChannelMeta: &relaycommon.ChannelMeta{}}
			info.InitRequestConversionChain()
			recordedErr := types.WithOpenAIError(types.OpenAIError{
				Message: "Client not allowed", Type: "permission_error", Code: "client_not_allowed",
			}, http.StatusForbidden)
			if len(tc.path) > 0 {
				_, err := service.ConvertRequestVia(ctx, info, tc.request, tc.path...)
				if tc.wantError != "" {
					require.ErrorContains(t, err, tc.wantError)
					recordedErr = types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest)
				} else {
					require.NoError(t, err)
				}
			}
			processChannelError(ctx, channelSnapshot, recordedErr, info)
			var errorLog model.Log
			require.NoError(t, database.Last(&errorLog).Error)
			errorOther, err := common.StrToMap(errorLog.Other)
			require.NoError(t, err)
			assert.Equal(t, float64(recordedErr.StatusCode), errorOther["status_code"])
			assert.Equal(t, string(recordedErr.GetErrorCode()), errorOther["error_code"])
			if tc.wantChain == nil {
				assert.NotContains(t, errorOther, "request_conversion")
			} else {
				assert.Equal(t, tc.wantChain, errorOther["request_conversion"])
			}
			assert.Equal(t, tc.claude, errorOther["claude"] == true)
			if tc.wantError == "" {
				consume := service.GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 0, 0, 1)
				payload, err := common.Marshal(consume.Snapshot())
				require.NoError(t, err)
				var consumeOther map[string]any
				require.NoError(t, common.Unmarshal(payload, &consumeOther))
				assert.Equal(t, consumeOther["request_conversion"], errorOther["request_conversion"])
				assert.Equal(t, consumeOther["claude"], errorOther["claude"])
			}
		})
	}
}
