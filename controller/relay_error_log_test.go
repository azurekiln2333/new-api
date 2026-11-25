package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
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

func TestRequestContentRelayLogLifecycle(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, db, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	require.NoError(t, model.MigrateLogRequestDetails())
	user := &model.User{Username: "diagnostic-ingress", AffCode: "diagnostic-ingress"}
	require.NoError(t, db.Create(user).Error)
	oldNode, oldConsume, oldError := common.NodeName, common.LogConsumeEnabled, constant.ErrorLogEnabled
	common.NodeName, common.LogConsumeEnabled, constant.ErrorLogEnabled = "hk01", true, true
	t.Cleanup(func() {
		common.NodeName, common.LogConsumeEnabled, constant.ErrorLogEnabled = oldNode, oldConsume, oldError
	})
	t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "true")

	const original = `{"input":"original privately recorded prompt","instructions":"be brief","api_key":"not-in-snapshot"}`
	const expected = `{"input":"original privately recorded prompt","instructions":"be brief"}`
	for _, tc := range []struct {
		name     string
		wantHTTP int
		wantLogs []int
		readBody bool
		status   string
	}{
		{"retry then success", http.StatusOK, []int{model.LogTypeError, model.LogTypeConsume}, true, common.RequestContentStatusRecorded},
		{"final validation error", http.StatusBadRequest, []int{model.LogTypeError}, true, common.RequestContentStatusRecorded},
		{"unread rejection", http.StatusTooManyRequests, []int{model.LogTypeError}, false, common.RequestContentStatusUnavailable},
		{"excluded failure", http.StatusBadRequest, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := common.NewRequestId()
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/responses", func(c *gin.Context) {
				// Authentication itself is covered by the detail authorization
				// regression; use an already authenticated relay context here.
				c.Set("id", user.Id)
				c.Set("username", user.Username)
				c.Set(common.RequestIdKey, requestID)
			}, middleware.RequestLogDiagnostics(), func(c *gin.Context) {
				if tc.readBody {
					_, err := common.GetBodyStorage(c)
					require.NoError(t, err)
					common.CleanupBodyStorage(c)
					modified, err := common.CreateBodyStorage([]byte(`{"input":"rewritten by plugin"}`))
					require.NoError(t, err)
					c.Set(common.KeyBodyStorage, modified)
					_, err = common.GetBodyStorage(c)
					require.NoError(t, err)
				}
				switch tc.name {
				case "retry then success":
					service.RecordRelayErrorLog(c, 3, types.InitOpenAIError("attempt rejected", http.StatusBadGateway), nil)
					model.RecordConsumeLog(c, user.Id, model.RecordConsumeLogParams{ChannelId: 4, Other: model.NewLogOther()})
				case "excluded failure":
					service.RecordRelayErrorLog(c, 0, types.NewErrorWithStatusCode(errors.New("excluded"), types.ErrorCodeInvalidRequest,
						http.StatusBadRequest, types.ErrOptionWithNoRecordErrorLog()), nil)
				}
				c.Status(tc.wantHTTP)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(original))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			engine.ServeHTTP(resp, req)
			require.Equal(t, tc.wantHTTP, resp.Code)
			var logs []model.Log
			require.NoError(t, db.Where("request_id = ? AND user_id = ?", requestID, user.Id).Order("id").Find(&logs).Error)
			require.Len(t, logs, len(tc.wantLogs))
			var ref string
			for i, log := range logs {
				assert.Equal(t, tc.wantLogs[i], log.Type)
				assert.NotContains(t, log.Other, "original privately recorded prompt")
				var diagnostic struct {
					Admin struct {
						Node    string                        `json:"node_name"`
						Submit  string                        `json:"submit_node_name"`
						Content common.RequestContentMetadata `json:"request_content"`
					} `json:"admin_info"`
				}
				require.NoError(t, common.UnmarshalJsonStr(log.Other, &diagnostic))
				assert.Equal(t, "hk01", diagnostic.Admin.Node)
				assert.Equal(t, "hk01", diagnostic.Admin.Submit)
				assert.Equal(t, tc.status, diagnostic.Admin.Content.Status)
				if !tc.readBody {
					assert.Empty(t, diagnostic.Admin.Content.Ref)
					continue
				}
				if ref == "" {
					ref = diagnostic.Admin.Content.Ref
				}
				require.NotEmpty(t, ref)
				assert.Equal(t, ref, diagnostic.Admin.Content.Ref, "retry and success share the same snapshot")
				recorded, err := model.GetLogRequestDetail(t.Context(), ref)
				require.NoError(t, err)
				assert.JSONEq(t, expected, string(recorded.Payload))
			}
			var count int64
			require.NoError(t, db.Model(&model.LogRequestDetail{}).Where("request_id = ?", requestID).Count(&count).Error)
			if ref == "" {
				assert.Zero(t, count)
				return
			}
			assert.EqualValues(t, 1, count)
			if tc.name == "retry then success" {
				common.NodeName = "us01"
				defer func() { common.NodeName = "hk01" }()
				model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{UserId: user.Id, LogType: model.LogTypeRefund,
					RequestID: requestID, RequestContent: &model.TaskRequestContent{Ref: ref, RequestID: requestID, SubmitNodeName: "hk01",
						Status: common.RequestContentStatusRecorded, CapturedBytes: len(expected)}})
				var async model.Log
				require.NoError(t, db.Where("request_id = ? AND type = ?", requestID, model.LogTypeRefund).Take(&async).Error)
				assert.Contains(t, async.Other, `"node_name":"us01"`)
				assert.Contains(t, async.Other, `"submit_node_name":"hk01"`)
				assert.Contains(t, async.Other, ref)
			}
		})
	}
}

func TestRejectedRelayLogRetainsParsedModel(t *testing.T) {
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	useLogExportDatabases(t, db, db, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	require.NoError(t, model.MigrateLogRequestDetails())
	user := &model.User{Username: "rejected-model-log", AffCode: "rejected-model-log"}
	require.NoError(t, db.Create(user).Error)
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })
	t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "true")

	for _, tc := range []struct {
		name      string
		body      string
		wantHTTP  int
		wantModel string
	}{
		{"model denied before selection", `{"model":"gpt-denied","messages":[]}`, http.StatusForbidden, "gpt-denied"},
		{"invalid body has no parsed model", `{"model":`, http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := common.NewRequestId()
			engine := gin.New()
			engine.Use(middleware.BodyStorageCleanup())
			engine.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Set("id", user.Id)
				c.Set("username", user.Username)
				c.Set(common.RequestIdKey, requestID)
				common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
				common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-allowed": true})
			}, middleware.RequestLogDiagnostics(), middleware.Distribute(), func(c *gin.Context) {
				t.Error("a rejected request must not reach the relay handler")
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.wantHTTP, response.Code)
			var logs []model.Log
			require.NoError(t, db.Where("request_id = ?", requestID).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, model.LogTypeError, logs[0].Type)
			assert.Equal(t, tc.wantModel, logs[0].ModelName)
			assert.Zero(t, logs[0].ChannelId, "the request was rejected before any channel was selected")
		})
	}
}

func TestResponsesWSRequestRunnerRequestContentDiagnostics(t *testing.T) {
	user, token := setupResponsesWSRequestTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}))
	require.NoError(t, model.MigrateLogRequestDetails())
	previousConsume, previousError := common.LogConsumeEnabled, constant.ErrorLogEnabled
	common.LogConsumeEnabled, constant.ErrorLogEnabled = true, true
	t.Cleanup(func() { common.LogConsumeEnabled, constant.ErrorLogEnabled = previousConsume, previousError })
	t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "true")
	runner, _ := newResponsesWSTestRunner(t, token)
	var refs []string
	for _, input := range []string{"first", "second", "excluded"} {
		payload := `{"input":"` + input + `"}`
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
		apiErr := runner(request, "ws-content-"+input, func(c *gin.Context) *types.NewAPIError {
			_, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			snapshot := common.RequestContent(c)
			require.NotNil(t, snapshot)
			assert.JSONEq(t, payload, string(snapshot.Payload))
			refs = append(refs, snapshot.Ref)
			if input == "excluded" {
				return types.NewErrorWithStatusCode(errors.New("excluded request"), types.ErrorCodeInvalidRequest,
					http.StatusBadRequest, types.ErrOptionWithNoRecordErrorLog())
			}
			model.RecordConsumeLog(c, user.Id, model.RecordConsumeLogParams{Other: model.NewLogOther()})
			return nil
		})
		if input == "excluded" {
			require.NotNil(t, apiErr)
			assert.False(t, types.IsRecordErrorLog(apiErr))
			continue
		}
		require.Nil(t, apiErr)
		detail, err := model.GetLogRequestDetail(t.Context(), refs[len(refs)-1])
		require.NoError(t, err)
		assert.JSONEq(t, payload, string(detail.Payload))
	}
	require.Len(t, refs, 3)
	assert.NotEqual(t, refs[0], refs[1], "response.create turns must not share an ingress snapshot")
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ? AND request_id LIKE ?", user.Id, "ws-content-%").Find(&logs).Error)
	assert.Len(t, logs, 2, "an explicitly excluded final API error must not be recreated by fallback middleware")
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.LogRequestDetail{}).Where("ref = ?", refs[2]).Count(&count).Error)
	assert.Zero(t, count, "an excluded turn must not leave stored prompt content")
}

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
