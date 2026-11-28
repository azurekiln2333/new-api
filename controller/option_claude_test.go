package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateOptionRejectsNegativeClaudeDefaultMaxTokens(t *testing.T) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/",
		strings.NewReader(`{"key":"claude.default_max_tokens","value":"{\"default\":-1}"}`),
	)

	UpdateOption(context)

	assert.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Contains(t, payload.Message, "-1")
}

func TestUpdateOptionsBatchRejectsInvalidMemberBeforeAnyWrite(t *testing.T) {
	for _, test := range []struct {
		name, key, value string
	}{
		{"claude validation", "claude.default_max_tokens", `{"default":-1}`},
		{"gemini validation", "gemini.safety_settings", `{"default":"BLOCK_SOME"}`},
		{"per-user login preference", "LoginVerificationUser:1", "true"},
		{"invalid login policy", "LoginVerificationRequiredEnabled", "invalid"},
		{"compliance field", "payment_setting.compliance_confirmed", "true"},
		{"domain special flow", "ServerAddress", "https://example.com"},
		{"pricing special flow", "ImageRatio", `{"draft":2}`},
		{"request policy special flow", "RetryTimes", "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := ratio_setting.ImageRatio2JSONString()
			body, err := common.Marshal(map[string]any{"options": []OptionUpdateRequest{
				{Key: "TestMustNotCommit", Value: "draft"}, {Key: test.key, Value: test.value},
			}})
			require.NoError(t, err)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/option/batch", strings.NewReader(string(body)))
			UpdateOptionsBatch(c)
			var payload struct {
				Success bool   `json:"success"`
				Key     string `json:"key"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
			assert.Equal(t, test.key, payload.Key)
			assert.NotEmpty(t, payload.Message)
			assert.Equal(t, before, ratio_setting.ImageRatio2JSONString(), "rejection must not publish a draft pricing map")
		})
	}
}

func TestUpdateOptionsBatchCommitsSectionWithOneAuditAndSync(t *testing.T) {
	setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	previousMap := common.OptionMap
	previousSiteKey := common.TurnstileSiteKey
	previousSecret := common.TurnstileSecretKey
	previousEnabled := common.TurnstileCheckEnabled
	common.OptionMap = map[string]string{}
	common.TurnstileSiteKey, common.TurnstileSecretKey, common.TurnstileCheckEnabled = "", "", false
	t.Cleanup(func() {
		common.OptionMap = previousMap
		common.TurnstileSiteKey, common.TurnstileSecretKey, common.TurnstileCheckEnabled = previousSiteKey, previousSecret, previousEnabled
	})

	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/batch", strings.NewReader(`{"options":[{"key":"TurnstileCheckEnabled","value":true},{"key":"TurnstileSiteKey","value":"site"},{"key":"TurnstileSecretKey","value":"must-not-be-audited"}]}`))
	UpdateOptionsBatch(c)
	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success, response.Body.String())
	assert.True(t, common.TurnstileCheckEnabled)
	assert.Equal(t, "site", common.TurnstileSiteKey)
	var events []model.AuditEventOutbox
	require.NoError(t, model.DB.Where("action = ?", "option.update").Find(&events).Error)
	require.Len(t, events, 1)
	assert.Contains(t, events[0].ParamsJson, "TurnstileSecretKey")
	assert.NotContains(t, events[0].Content+events[0].ParamsJson, "must-not-be-audited")
	var syncCount int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", model.SyncTaskType(model.SyncTopicOptions)).Count(&syncCount).Error)
	assert.Equal(t, int64(1), syncCount)

	// A later invalid member leaves the first key, live config, audit and sync
	// exactly as they were before this request.
	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/batch", strings.NewReader(`{"options":[{"key":"TurnstileSiteKey","value":"rejected"},{"key":"claude.default_max_tokens","value":"{\"default\":-1}"}]}`))
	UpdateOptionsBatch(c)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Equal(t, "site", common.TurnstileSiteKey)

	// The compatibility single PUT still takes the same validator and succeeds.
	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"TurnstileSiteKey","value":"single"}`))
	UpdateOption(c)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, "single", common.TurnstileSiteKey)

	const rejectAudit = "test:reject_option_batch_audit"
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(rejectAudit, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.AuditEventOutbox); ok {
			tx.AddError(fmt.Errorf("audit storage unavailable"))
		}
	}))
	t.Cleanup(func() { assert.NoError(t, model.DB.Callback().Create().Remove(rejectAudit)) })
	response = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/batch", strings.NewReader(`{"options":[{"key":"TurnstileSiteKey","value":"audit-failure"}]}`))
	UpdateOptionsBatch(c)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Equal(t, "single", common.TurnstileSiteKey)
	var stored model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: "TurnstileSiteKey"}).First(&stored).Error)
	assert.Equal(t, "single", stored.Value)
}

func TestUpdateOptionAnnouncementCommitsWithAuditAndSync(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	db, logDB := model.DB, model.LOG_DB
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	const key = "console_setting.announcements"
	const value = `[{"content":"private draft content","publishDate":"2026-11-27T00:00:00Z","type":"default"}]`
	previousMap := common.OptionMap
	settings := console_setting.GetConsoleSetting()
	previousAnnouncements := settings.Announcements
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = previousMap
		settings.Announcements = previousAnnouncements
	})
	body, err := common.Marshal(OptionUpdateRequest{Key: key, Value: value})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body)))
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", common.RoleRootUser)
	c.Set(common.RequestIdKey, "announcement-save")

	UpdateOption(c)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"success":true,"message":""}`, response.Body.String())
	var stored model.Option
	require.NoError(t, db.Where(&model.Option{Key: key}).First(&stored).Error)
	assert.Equal(t, value, stored.Value)
	assert.Equal(t, value, common.OptionMap[key])
	assert.Equal(t, value, settings.Announcements)
	var events []model.AuditEventOutbox
	require.NoError(t, db.Where("action = ?", "option.update").Find(&events).Error)
	require.Len(t, events, 1)
	assert.Equal(t, user.Id, events[0].ActorUserId)
	assert.Equal(t, user.Username, events[0].ActorUsername)
	assert.Equal(t, "announcement-save", events[0].RequestId)
	assert.JSONEq(t, `{"key":"console_setting.announcements"}`, events[0].ParamsJson)
	assert.NotContains(t, events[0].Content+events[0].ParamsJson, "private draft content")
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
	var syncCount, deliveredCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).Where("type = ?", model.SyncTaskType(model.SyncTopicOptions)).Count(&syncCount).Error)
	assert.Equal(t, int64(1), syncCount)
	require.NoError(t, logDB.Model(&model.AuditLog{}).Where("action = ?", "option.update").Count(&deliveredCount).Error)
	assert.Zero(t, deliveredCount, "saving must not wait for delivery to the log database")
}

func TestUpdateOptionAnnouncementRollsBackFailedObligations(t *testing.T) {
	for _, failurePoint := range []string{"sync", "audit", "canceled request"} {
		t.Run(failurePoint, func(t *testing.T) {
			setupSecurityEnrollmentTest(t)
			db := model.DB
			require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
			const key = "console_setting.announcements"
			const previous = `[{"content":"saved announcement","publishDate":"2026-11-27T00:00:00Z","type":"default"}]`
			const draft = `[{"content":"unsaved private content","publishDate":"2026-11-27T00:00:00Z","type":"default"}]`
			require.NoError(t, db.Create(&model.Option{Key: key, Value: previous}).Error)
			previousMap := common.OptionMap
			settings := console_setting.GetConsoleSetting()
			previousAnnouncements := settings.Announcements
			common.OptionMap = map[string]string{key: previous}
			settings.Announcements = previous
			t.Cleanup(func() {
				common.OptionMap = previousMap
				settings.Announcements = previousAnnouncements
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const callbackName = "test:reject_single_option_obligation"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
				switch tx.Statement.Dest.(type) {
				case *model.SystemTask:
					if failurePoint == "sync" {
						tx.AddError(fmt.Errorf("sync rejected unsaved private content"))
					}
					if failurePoint == "canceled request" {
						cancel()
					}
				case *model.AuditEventOutbox:
					if failurePoint == "audit" {
						tx.AddError(fmt.Errorf("audit rejected unsaved private content"))
					}
				}
			}))
			t.Cleanup(func() { assert.NoError(t, db.Callback().Create().Remove(callbackName)) })
			body, err := common.Marshal(OptionUpdateRequest{Key: key, Value: draft})
			require.NoError(t, err)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(string(body))).WithContext(ctx)

			UpdateOption(c)

			assert.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			assert.False(t, payload.Success)
			assert.NotEmpty(t, payload.Message)
			assert.NotContains(t, response.Body.String(), "unsaved private content")
			var stored model.Option
			require.NoError(t, db.Where(&model.Option{Key: key}).First(&stored).Error)
			assert.Equal(t, previous, stored.Value)
			assert.Equal(t, previous, common.OptionMap[key])
			assert.Equal(t, previous, settings.Announcements)
			var syncCount, auditCount int64
			require.NoError(t, db.Model(&model.SystemTask{}).Where("type = ?", model.SyncTaskType(model.SyncTopicOptions)).Count(&syncCount).Error)
			assert.Zero(t, syncCount)
			require.NoError(t, db.Model(&model.AuditEventOutbox{}).Where("action = ?", "option.update").Count(&auditCount).Error)
			assert.Zero(t, auditCount)
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyAuditLogged))
		})
	}
}
