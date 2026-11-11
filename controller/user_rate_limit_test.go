package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupUserRateLimitTestDB gives the rate limit handlers a disposable database
// holding users, the option table, and the audit log table.
func setupUserRateLimitTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, i18n.Init())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousUserRateLimits := setting.ModelRequestRateLimitUser
	previousOptions := common.OptionMap
	previousRedisEnabled := common.RedisEnabled
	db, _ := newAuditTestDatabase(t, "sqlite", "")

	model.DB, model.LOG_DB = db, db
	setting.ModelRequestRateLimitUser = map[string][2]int{}
	common.OptionMap = map[string]string{}
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		setting.ModelRequestRateLimitUser = previousUserRateLimits
		common.OptionMap = previousOptions
		common.RedisEnabled = previousRedisEnabled
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.Log{}, &model.AuditLog{}))
}

func createUserRateLimitTestUser(t *testing.T, id int, group string) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id:       id,
		Username: fmt.Sprintf("user%d", id),
		Group:    group,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		AffCode:  fmt.Sprintf("aff%06d", id),
	}).Error)
}

func performAdminUserRateLimitRequest(t *testing.T, handler gin.HandlerFunc, method, path, body string, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	handler(c)
	return recorder
}

func TestUpdateUserRateLimitOverridesGroupForOneUser(t *testing.T) {
	setupUserRateLimitTestDB(t)
	createUserRateLimitTestUser(t, 101, "vip")
	createUserRateLimitTestUser(t, 102, "vip")

	params := gin.Params{{Key: "id", Value: "101"}}
	response := performAdminUserRateLimitRequest(t, UpdateUserRateLimit, http.MethodPut, "/api/user/101/rate_limit",
		`{"total_count":0,"success_count":7}`, params)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	total, success, found := setting.GetUserRateLimit(101)
	assert.True(t, found)
	assert.Equal(t, 0, total)
	assert.Equal(t, 7, success)
	_, _, found = setting.GetUserRateLimit(102)
	assert.False(t, found, "a sibling in the same group must keep following the group limit")

	// The override is persisted as an option so other nodes pick it up on sync.
	var option model.Option
	require.NoError(t, model.DB.First(&option, "key = ?", "ModelRequestRateLimitUser").Error)
	assert.JSONEq(t, `{"101":[0,7]}`, option.Value)

	response = performAdminUserRateLimitRequest(t, DeleteUserRateLimit, http.MethodDelete, "/api/user/101/rate_limit", "", params)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	_, _, found = setting.GetUserRateLimit(101)
	assert.False(t, found, "clearing the override returns the user to the group limit")
}

func TestUpdateUserRateLimitRejectsInvalidValues(t *testing.T) {
	setupUserRateLimitTestDB(t)
	createUserRateLimitTestUser(t, 111, "vip")

	response := performAdminUserRateLimitRequest(t, UpdateUserRateLimit, http.MethodPut, "/api/user/111/rate_limit",
		`{"total_count":5,"success_count":0}`, gin.Params{{Key: "id", Value: "111"}})
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":false`)
	_, _, found := setting.GetUserRateLimit(111)
	assert.False(t, found)
}

func TestClearGroupUserRateLimitsOnlyClearsTargetGroup(t *testing.T) {
	setupUserRateLimitTestDB(t)
	createUserRateLimitTestUser(t, 121, "vip")
	createUserRateLimitTestUser(t, 122, "vip")
	createUserRateLimitTestUser(t, 123, "default")
	require.NoError(t, setting.UpdateModelRequestRateLimitUserByJSONString(
		`{"121":[0,5],"122":[0,6],"123":[0,7]}`))

	response := performAdminUserRateLimitRequest(t, ClearGroupUserRateLimits, http.MethodPost, "/api/user/rate_limit/clear_group",
		`{"group":"vip"}`, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"cleared":2`)

	_, _, found := setting.GetUserRateLimit(121)
	assert.False(t, found)
	_, _, found = setting.GetUserRateLimit(122)
	assert.False(t, found)
	_, success, found := setting.GetUserRateLimit(123)
	assert.True(t, found, "users outside the target group keep their manual settings")
	assert.Equal(t, 7, success)
}
