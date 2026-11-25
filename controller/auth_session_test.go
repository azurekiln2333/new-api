package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthLogoutRejectsRefreshCookieSessionMismatch(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB = db
	common.RedisEnabled = false
	common.SessionSecret = "auth-logout-mismatch-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
	})

	user := &model.User{
		Username: "logout-mismatch-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(user).Error)
	sessionA, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-a")
	require.NoError(t, err)
	sessionB, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "agent-b")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/logout", nil)
	c.Request.Header.Set("Authorization", "Bearer "+sessionA.AccessToken)
	c.Request.Header.Set("X-Auth-Session", sessionA.Session.SID)
	c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: sessionB.RefreshToken})

	AuthLogout(c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "AUTH_SESSION_MISMATCH", response.Code)
	for _, sid := range []string{sessionA.Session.SID, sessionB.Session.SID} {
		stored, err := model.GetUserSessionBySID(sid)
		require.NoError(t, err)
		assert.Equal(t, model.UserSessionStatusActive, stored.Status)
	}
}

func TestRefreshAuthRetainsCookieAfterTemporarySessionLookupFailure(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "refresh-transient-agent")
	require.NoError(t, err)

	const callbackName = "test:refresh_session_lookup_failure"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "user_sessions" {
			tx.AddError(errors.New("temporary database unavailable"))
		}
	}))
	callbackInstalled := true
	t.Cleanup(func() {
		if callbackInstalled {
			require.NoError(t, model.DB.Callback().Query().Remove(callbackName))
		}
	})

	failedRecorder := httptest.NewRecorder()
	failedContext, _ := gin.CreateTestContext(failedRecorder)
	failedContext.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
	failedContext.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
	RefreshAuth(failedContext)
	require.NoError(t, model.DB.Callback().Query().Remove(callbackName))
	callbackInstalled = false

	assert.Equal(t, http.StatusInternalServerError, failedRecorder.Code)
	assert.Empty(t, failedRecorder.Result().Cookies(), "a temporary storage failure must not clear the browser's refresh cookie")
	var failure struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	require.NoError(t, common.Unmarshal(failedRecorder.Body.Bytes(), &failure))
	assert.False(t, failure.Success)
	assert.Equal(t, "AUTH_INTERNAL_ERROR", failure.Code)

	stored, err := model.GetUserSessionBySID(bundle.Session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusActive, stored.Status)

	retryRecorder := httptest.NewRecorder()
	retryContext, _ := gin.CreateTestContext(retryRecorder)
	retryContext.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
	retryContext.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
	RefreshAuth(retryContext)

	assert.Equal(t, http.StatusOK, retryRecorder.Code)
	var retry struct {
		Success bool `json:"success"`
		Data    struct {
			Session struct {
				SID string `json:"sid"`
			} `json:"session"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(retryRecorder.Body.Bytes(), &retry))
	assert.True(t, retry.Success)
	assert.Equal(t, bundle.Session.SID, retry.Data.Session.SID, "retrying the original cookie must refresh the same database session")
}

func TestRefreshAuthClearsMissingRevokedAndExpiredSessionCookies(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	missingBundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "missing-agent")
	require.NoError(t, err)
	revokedBundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "revoked-agent")
	require.NoError(t, err)
	expiredBundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "expired-agent")
	require.NoError(t, err)
	require.NoError(t, model.DB.Delete(&model.UserSession{}, "sid = ?", missingBundle.Session.SID).Error)
	revoked, err := model.RevokeUserSession(user.Id, revokedBundle.Session.SID, "test-revoked")
	require.NoError(t, err)
	require.True(t, revoked)
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", expiredBundle.Session.SID).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)

	for _, test := range []struct {
		name         string
		refreshToken string
	}{
		{name: "missing session", refreshToken: missingBundle.RefreshToken},
		{name: "revoked session", refreshToken: revokedBundle.RefreshToken},
		{name: "expired session", refreshToken: expiredBundle.RefreshToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/user/auth/refresh", nil)
			c.Request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: test.refreshToken})
			RefreshAuth(c)

			assert.Equal(t, http.StatusUnauthorized, recorder.Code)
			var clearedCookie *http.Cookie
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == service.RefreshCookieName {
					clearedCookie = cookie
					break
				}
			}
			require.NotNil(t, clearedCookie, "confirmed invalid sessions must clear their refresh cookie")
			assert.Empty(t, clearedCookie.Value)
			assert.Less(t, clearedCookie.MaxAge, 0)
		})
	}
}

func TestWriteAuthSessionErrorMapsSessionGrowthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "active session limit",
			err:            model.ErrUserSessionLimit,
			expectedStatus: http.StatusConflict,
			expectedCode:   "AUTH_SESSION_LIMIT",
		},
		{
			name:           "issuance limit",
			err:            model.ErrUserSessionIssuanceLimit,
			expectedStatus: http.StatusTooManyRequests,
			expectedCode:   "AUTH_SESSION_ISSUANCE_LIMIT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			writeAuthSessionError(c, test.err)

			assert.Equal(t, test.expectedStatus, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedCode, response.Code)
		})
	}
}

func TestSessionLimitDoesNotRecordRejectedLoginAsSuccessful(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousActiveLimit := common.UserSessionActiveLimit
	previousIssuanceLimit := common.UserSessionIssuanceLimit
	previousIssuanceWindow := common.UserSessionIssuanceWindowSeconds
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.TwoFA{}, &model.PasskeyCredential{}))
	model.DB = db
	common.RedisEnabled = false
	common.UserSessionActiveLimit = 1
	common.UserSessionIssuanceLimit = 100
	common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.UserSessionActiveLimit = previousActiveLimit
		common.UserSessionIssuanceLimit = previousIssuanceLimit
		common.UserSessionIssuanceWindowSeconds = previousIssuanceWindow
	})

	const previousLastLoginAt = int64(123)
	user := &model.User{
		Username: "rejected-login-audit-user", Password: "unused", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, LastLoginAt: previousLastLoginAt,
	}
	require.NoError(t, db.Create(user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{
		SID: "existing-active-session", UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now, LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/login", nil)
	setupLogin(user, nil, nil, c)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, previousLastLoginAt, stored.LastLoginAt)
}
