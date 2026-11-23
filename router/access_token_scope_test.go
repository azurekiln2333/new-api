package router

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// accessTokenExemptRoutes are /api routes that never reach dashboard
// authentication, so a personal access token has no rule to satisfy there.
var accessTokenExemptRoutes = []string{
	// Anonymous pages and setup.
	"GET /api/setup",
	"POST /api/setup",
	"GET /api/status",
	"GET /api/uptime/status",
	"GET /api/official-status",
	"GET /api/notice",
	"GET /api/user-agreement",
	"GET /api/privacy-policy",
	"GET /api/about",
	"GET /api/home_page_content",
	"GET /api/ratio_config",
	"GET /api/user/groups",

	// Anonymous account entry points.
	"GET /api/verification",
	"GET /api/reset_password",
	"POST /api/user/reset",
	"POST /api/user/register",
	"GET /api/user/login/encryption-key",
	"POST /api/user/login",
	"POST /api/user/login/2fa",
	"POST /api/user/login/verify",
	"POST /api/user/login/passkey/begin",
	"POST /api/user/login/passkey/finish",
	"POST /api/user/passkey/login/begin",
	"POST /api/user/passkey/login/finish",
	"GET /api/oauth/wechat",
	"POST /api/oauth/wechat",
	"GET /api/oauth/telegram/login",
	"GET /api/oauth/telegram/bind/:flow_token",

	// Browser session cookie endpoints.
	"POST /api/user/auth/refresh",
	"POST /api/user/auth/logout",

	// Payment callbacks.
	"POST /api/stripe/webhook",
	"POST /api/creem/webhook",
	"POST /api/waffo/webhook",
	"POST /api/waffo-pancake/webhook/:env",
	"GET /api/user/epay/notify",
	"POST /api/user/epay/notify",
	"GET /api/subscription/epay/notify",
	"POST /api/subscription/epay/notify",
	"GET /api/subscription/epay/return",
	"POST /api/subscription/epay/return",

	// API key authentication (TokenAuthReadOnly).
	"GET /api/usage/token/",
	"GET /api/log/token",
}

// accessTokenHelperRoutes are declared through handlePermissionRoute outside
// channelPermissionRoutes.
var accessTokenHelperRoutes = map[string]authz.Permission{
	"GET /api/task_plugin_options": authz.TaskPluginBind,
	"GET /api/audit":               authz.AuditRead,
}

func newAccessTokenScopeTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// A permission route that is also in the static table panics here.
	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetDashboardRouter(engine)
		SetRelayRouter(engine)
		SetTaskPluginProtocolRouter(engine)
		SetVideoRouter(engine)
		SetTaskRouter(engine)
	})
	return engine
}

func TestAccessTokenRouteRulesCoverEveryDashboardRoute(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	exempt := map[string]bool{}
	for _, key := range accessTokenExemptRoutes {
		exempt[key] = true
	}
	registered := map[string]bool{}
	panel := map[string]bool{}
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") && !strings.HasPrefix(route.Path, "/pg/") {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if !exempt[key] {
			panel[key] = true
		}
	}
	for _, key := range accessTokenExemptRoutes {
		assert.True(t, registered[key], "exempt route %s is no longer registered", key)
	}

	declared := map[string]int{}
	for _, key := range middleware.AccessTokenRouteRuleKeys() {
		declared[key]++
	}
	for key := range panel {
		assert.Equal(t, 1, declared[key], "panel route %s must be declared exactly once", key)
	}
	for key := range declared {
		assert.True(t, panel[key], "declared route %s is not a registered panel route", key)
	}

	permissionRoutes := map[string]authz.Permission{}
	for _, route := range channelPermissionRoutes {
		permissionRoutes[route.method+" "+joinPaths("/api/channel", route.path)] = route.permission
	}
	for key, permission := range accessTokenHelperRoutes {
		permissionRoutes[key] = permission
	}
	for key, permission := range permissionRoutes {
		rule, ok := middleware.AccessTokenRouteRule(key)
		require.True(t, ok, key)
		assert.Equal(t, "scope", rule.Kind(), key)
		assert.Equal(t, service.AccessTokenScopeOf(permission), rule.Scope(), key)
	}
}

var panelRateLimitTestIPs atomic.Uint32

func newPanelRateLimitTestEngine(t *testing.T, redisEnabled bool, globalLimit int) *gin.Engine {
	t.Helper()
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	previousCritical, previousCount, previousWindow := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousGlobal, previousGlobalCount, previousGlobalWindow := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	previousTurnstile, previousSecure := common.TurnstileCheckEnabled, common.SessionCookieSecure
	previousRegister, previousPasswordRegister := common.RegisterEnabled, common.PasswordRegisterEnabled
	common.RedisEnabled = redisEnabled
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 1, 71
	common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = true, globalLimit, 73
	common.TurnstileCheckEnabled, common.SessionCookieSecure = false, true
	common.RegisterEnabled, common.PasswordRegisterEnabled = true, true
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousRedis, previousRDB
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousCritical, previousCount, previousWindow
		common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = previousGlobal, previousGlobalCount, previousGlobalWindow
		common.TurnstileCheckEnabled, common.SessionCookieSecure = previousTurnstile, previousSecure
		common.RegisterEnabled, common.PasswordRegisterEnabled = previousRegister, previousPasswordRegister
	})
	if redisEnabled {
		server := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		require.NoError(t, client.Ping(context.Background()).Err())
		common.RDB = client
		t.Cleanup(func() { require.NoError(t, client.Close()) })
	}
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	SetApiRouter(engine)
	return engine
}

func panelRateLimitRequest(engine http.Handler, method, path, ip, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = ip + ":12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestPanelCriticalRateLimitLoginAliasesShareAllowance(t *testing.T) {
	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", redisEnabled), func(t *testing.T) {
			engine := newPanelRateLimitTestEngine(t, redisEnabled, 100)
			ip := fmt.Sprintf("198.19.0.%d", panelRateLimitTestIPs.Add(1))
			// Invalid credentials reach the actual controller without database
			// access, but still spend the anonymous login allowance.
			first := panelRateLimitRequest(engine, http.MethodPost, "/api/user/login", ip, `{}`)
			require.Equal(t, http.StatusOK, first.Code)
			for _, route := range []struct{ method, path string }{
				{http.MethodPost, "/api/user/login"},
				{http.MethodPost, "/api/user/login/2fa"},
				{http.MethodPost, "/api/user/login/verify"},
				{http.MethodPost, "/api/user/login/passkey/begin"},
				{http.MethodPost, "/api/user/login/passkey/finish"},
				{http.MethodPost, "/api/user/passkey/login/begin"},
				{http.MethodPost, "/api/user/passkey/login/finish"},
				{http.MethodPost, "/api/oauth/state"},
				{http.MethodGet, "/api/oauth/github"},
				{http.MethodGet, "/api/oauth/oidc"},
				{http.MethodGet, "/api/oauth/discord"},
				{http.MethodGet, "/api/oauth/linuxdo"},
				{http.MethodGet, "/api/oauth/telegram"},
				{http.MethodGet, "/api/oauth/wechat"},
				{http.MethodPost, "/api/oauth/wechat"},
				{http.MethodGet, "/api/oauth/telegram/login"},
				{http.MethodGet, "/api/oauth/telegram/bind/unverified-flow"},
				{http.MethodGet, "/api/reset_password"},
				{http.MethodPost, "/api/user/reset"},
			} {
				response := panelRateLimitRequest(engine, route.method, route.path, ip, `{}`)
				assert.Equal(t, http.StatusTooManyRequests, response.Code, route.method+" "+route.path)
				assert.Equal(t, "71", response.Header().Get("Retry-After"))
				assert.JSONEq(t, `{"success":false,"code":"RATE_LIMIT_EXCEEDED","message":"Too many requests","message_key":"Too many requests"}`, response.Body.String())
			}
			otherIP := fmt.Sprintf("198.19.0.%d", panelRateLimitTestIPs.Add(1))
			assert.Equal(t, http.StatusOK, panelRateLimitRequest(engine, http.MethodPost, "/api/user/login/2fa", otherIP, `{}`).Code)
		})
	}
}

func TestPanelCriticalRateLimitRegistrationAndSessionsAreIndependent(t *testing.T) {
	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", redisEnabled), func(t *testing.T) {
			engine := newPanelRateLimitTestEngine(t, redisEnabled, 100)
			ip := fmt.Sprintf("198.19.0.%d", panelRateLimitTestIPs.Add(1))
			for _, route := range []struct {
				method, path, body string
				status             int
			}{
				// Truncated JSON demonstrates the invitation registration route
				// without creating a user, consuming an invitation or querying DB.
				{http.MethodPost, "/api/user/register", `{"invitation_code":"private-invitation"`, http.StatusOK},
				{http.MethodPost, "/api/user/auth/refresh", `{}`, http.StatusUnauthorized},
				{http.MethodPost, "/api/user/auth/logout", `{}`, http.StatusOK},
				{http.MethodPost, "/api/user/login/verify", `{}`, http.StatusOK},
			} {
				first := panelRateLimitRequest(engine, route.method, route.path, ip, route.body)
				assert.Equal(t, route.status, first.Code, route.path+" must not use another business scope's count")
				second := panelRateLimitRequest(engine, route.method, route.path, ip, route.body)
				assert.Equal(t, http.StatusTooManyRequests, second.Code, route.path)
				assert.Equal(t, "71", second.Header().Get("Retry-After"))
				assert.NotContains(t, second.Body.String(), "private-invitation")
			}
			// This public panel-critical read has no database/auth fixture. Its
			// business switch may yield 200 or 403, but never another scope's429.
			first := panelRateLimitRequest(engine, http.MethodGet, "/api/ratio_config", ip, "")
			assert.Contains(t, []int{http.StatusOK, http.StatusForbidden}, first.Code)
			assert.Equal(t, http.StatusTooManyRequests, panelRateLimitRequest(engine, http.MethodGet, "/api/ratio_config", ip, "").Code)
		})
	}
}

func TestPanelCriticalRateLimitScopesStillShareGlobalAPICeiling(t *testing.T) {
	for _, redisEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("redis=%t", redisEnabled), func(t *testing.T) {
			engine := newPanelRateLimitTestEngine(t, redisEnabled, 3)
			ip := fmt.Sprintf("198.19.0.%d", panelRateLimitTestIPs.Add(1))
			assert.Equal(t, http.StatusOK, panelRateLimitRequest(engine, http.MethodPost, "/api/user/register", ip, `{}`).Code)
			assert.Equal(t, http.StatusUnauthorized, panelRateLimitRequest(engine, http.MethodPost, "/api/user/auth/refresh", ip, `{}`).Code)
			assert.Equal(t, http.StatusOK, panelRateLimitRequest(engine, http.MethodPost, "/api/user/login", ip, `{}`).Code)
			response := panelRateLimitRequest(engine, http.MethodPost, "/api/user/auth/logout", ip, `{}`)
			assert.Equal(t, http.StatusTooManyRequests, response.Code)
			assert.Equal(t, "73", response.Header().Get("Retry-After"), "the outer GA cap is not bypassed by changing business scopes")
		})
	}
}

func TestAccessTokenCatalogCoversEveryPermission(t *testing.T) {
	catalog := service.AccessTokenCatalog(1, common.RoleRootUser)
	scopes := map[string]bool{}
	resources := map[string]int{}
	for _, group := range catalog {
		for _, resource := range group.Resources {
			resources[resource.Resource]++
			for _, action := range resource.Actions {
				scopes[resource.Resource+":"+action.Action] = true
			}
		}
	}
	for _, permission := range authz.AllPermissions() {
		assert.True(t, scopes[service.AccessTokenScopeOf(permission)], "root catalog lacks %s", service.AccessTokenScopeOf(permission))
	}
	for resource, count := range resources {
		assert.Equal(t, 1, count, "resource %s appears in more than one catalog entry", resource)
	}
}

func TestAccessTokenVerificationScopesAreAllListed(t *testing.T) {
	files, err := filepath.Glob("../service/*.go")
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	var scopes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if !strings.HasPrefix(name.Name, "VerificationScope") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					require.True(t, ok, name.Name)
					scope, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					scopes = append(scopes, scope)
				}
			}
		}
	}
	require.NotEmpty(t, scopes)
	for _, scope := range scopes {
		_, listed := service.AccessTokenVerificationScope(scope)
		assert.True(t, listed, "verification scope %s has no access token rule", scope)
	}
}

// adminChannelSurfaceFixture holds one isolated database plus the real routers,
// so the dashboard guards below run through production middleware only.
type adminChannelSurfaceFixture struct {
	engine          *gin.Engine
	database        *gorm.DB
	channelId       int
	adminId         int
	ordinaryId      int
	adminSession    string
	adminPAT        string
	ordinarySession string
}

func setupAdminChannelSurfaceFixture(t *testing.T) *adminChannelSurfaceFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis, previousSecret := common.IsMasterNode, common.RedisEnabled, common.SessionSecret
	previousCache := common.MemoryCacheEnabled
	previousSQLitePath := common.SQLitePath
	// Claim the temp dir before registering the cleanup below: t.Cleanup runs
	// LIFO and t.TempDir removes its tree in its own late cleanup, so the
	// restore/close must be registered after it to run before the removal.
	taskDir := t.TempDir()
	// One cleanup restores the globals first and only then closes the temp
	// handle, so no later test can observe a closed database.
	var tempDB *gorm.DB
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		common.IsMasterNode, common.RedisEnabled, common.SessionSecret = previousMaster, previousRedis, previousSecret
		common.MemoryCacheEnabled = previousCache
		common.SQLitePath = previousSQLitePath
		if tempDB != nil && tempDB != previousDB {
			if sqlDB, err := tempDB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	})
	// A private temp file per test keeps these rows away from every other
	// fixture, and an empty Option table keeps channel budgeting inactive.
	// Every flag InitDB consults is set before it runs, so an ambient value can
	// neither activate budgeting nor skip the reserved-column setup.
	common.SQLitePath = filepath.Join(taskDir, "admin-channel-surface.db")
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "")
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = true, false, false
	common.SessionSecret = "admin-channel-surface-test-secret"
	// InitDB reads the activation marker during startup, so seed the Option
	// table on the same file first.
	seed, err := gorm.Open(sqlite.Open(common.SQLitePath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, seed.AutoMigrate(&model.Option{}))
	seedSQL, err := seed.DB()
	require.NoError(t, err)
	require.NoError(t, seedSQL.Close())
	require.NoError(t, model.InitDB())
	database := model.DB
	tempDB = database
	model.LOG_DB = database
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{},
		&model.CasbinRule{}, &model.AuthzRole{}, &model.Channel{}, &model.Ability{}, &model.AuditLog{}, &model.Log{}, &model.Token{}))
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	require.NoError(t, authz.Init(database))

	admin := &model.User{Username: "surface-admin", Password: "password-placeholder", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "surface-admin-aff", AuthVersion: 1}
	require.NoError(t, database.Create(admin).Error)
	ordinary := &model.User{Username: "surface-ordinary", Password: "password-placeholder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "surface-ordinary-aff", AuthVersion: 1}
	require.NoError(t, database.Create(ordinary).Error)

	adminBundle, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "admin-channel-surface")
	require.NoError(t, err)
	ordinaryBundle, err := service.CreateLoginSession(ordinary.Id, "password", "127.0.0.1", "admin-channel-surface")
	require.NoError(t, err)

	channel := &model.Channel{Name: "surface-channel", Type: constant.ChannelTypeOpenAI, Key: "sk-surface-fixture",
		Models: "fixture-model", Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, channel.Insert())

	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	// The real registration path keeps AdminAuth, RequirePermission and the
	// access-token route rules exactly as production declares them.
	SetApiRouter(engine)
	SetRelayRouter(engine)

	return &adminChannelSurfaceFixture{engine: engine, database: database, channelId: channel.Id,
		adminId: admin.Id, ordinaryId: ordinary.Id,
		adminSession: adminBundle.AccessToken, adminPAT: createAdminSurfacePAT(t, admin.Id, authz.ChannelRead),
		ordinarySession: ordinaryBundle.AccessToken}
}

func createAdminSurfacePAT(t *testing.T, userID int, permissions ...authz.Permission) string {
	t.Helper()
	suffix, err := common.GenerateRandomCharsKey(43)
	require.NoError(t, err)
	raw := model.AccessTokenPrefix + suffix
	scopes := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		scopes = append(scopes, service.AccessTokenScopeOf(permission))
	}
	token := &model.UserAccessToken{Name: "surface token", TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw)}
	require.NoError(t, token.SetScopes(scopes))
	require.NoError(t, model.CreateUserAccessToken(userID, token, service.AccessTokenMaxPerUser))
	return raw
}

func (f *adminChannelSurfaceFixture) request(method, path, credential, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	return response
}

// TestAdminChannelSurfaceRejectsOrdinaryCredentials pins the authorization of
// every surface that exposes the administrator's channel fingerprint, the
// channel list and the single-channel test path. ASVS 5.0.0 V8.2.1/V8.2.2 and
// V8.3.1: the trusted middleware must enforce the rule, so a console user or a
// personal access token cannot reach the handler no matter what the frontend
// hides.
func TestAdminChannelSurfaceRejectsOrdinaryCredentials(t *testing.T) {
	fixture := setupAdminChannelSurfaceFixture(t)
	id := strconv.Itoa(fixture.channelId)
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/channel/", ""},
		{http.MethodGet, "/api/channel/" + id, ""},
		{http.MethodPost, "/api/channel/" + id + "/fingerprint", `{"model":"fixture-model"}`},
		{http.MethodGet, "/api/channel/" + id + "/fingerprint/unknown-job", ""},
		{http.MethodPost, "/api/channel/" + id + "/fingerprint/unknown-job/cancel", ""},
		{http.MethodGet, "/api/channel/test/" + id, ""},
		{http.MethodGet, "/pg/channels?group=default", ""},
	}

	// A browser session of an ordinary user is rejected by the role guard.
	for _, route := range routes {
		response := fixture.request(route.method, route.path, fixture.ordinarySession, route.body)
		assert.Equal(t, http.StatusForbidden, response.Code, route.method+" "+route.path)
		assert.Contains(t, response.Body.String(), `"code":"AUTH_INSUFFICIENT_PRIVILEGE"`, route.method+" "+route.path)
		assert.NotContains(t, response.Body.String(), "sk-surface-fixture", route.method+" "+route.path)
		assert.NotContains(t, response.Body.String(), "fixture-model", route.method+" "+route.path)
	}

	// A personal access token only carries the scopes its owner granted, and
	// those scopes never exceed the account's own permissions. This token holds
	// a real personal scope (profile:read) rather than a channel scope.
	profilePAT := createAdminSurfacePAT(t, fixture.ordinaryId, authz.Permission{Resource: "profile", Action: authz.ActionRead})
	require.Contains(t, middleware.AccessTokenRouteRuleKeys(), "GET /api/user/self", "the personal scope belongs to a declared route")
	profileRoute := fixture.request(http.MethodGet, "/api/user/self", profilePAT, "")
	require.Equal(t, http.StatusOK, profileRoute.Code, "the personal scope is valid on its own route: %s", profileRoute.Body.String())
	for _, route := range routes {
		response := fixture.request(route.method, route.path, profilePAT, route.body)
		assert.Equal(t, http.StatusForbidden, response.Code, route.method+" "+route.path)
		assert.NotContains(t, response.Body.String(), "fixture-model", route.method+" "+route.path)
	}

	// A scoped token cannot grant a permission its owner lacks: the same token
	// is refused even though the channel scope is declared on the route.
	channelScopedForOrdinary := createAdminSurfacePAT(t, fixture.ordinaryId, authz.ChannelRead)
	for _, path := range []string{"/api/channel/", "/api/channel/" + id, "/pg/channels?group=default"} {
		response := fixture.request(http.MethodGet, path, channelScopedForOrdinary, "")
		assert.Equal(t, http.StatusForbidden, response.Code, path)
		assert.NotContains(t, response.Body.String(), "fixture-model", path)
	}

	// A real ordinary session proves the fixture is valid: the same account
	// discovers its own allowed groups and models.
	groups := fixture.request(http.MethodGet, "/api/user/self/groups", fixture.ordinarySession, "")
	require.Equal(t, http.StatusOK, groups.Code, groups.Body.String())
	assert.Contains(t, groups.Body.String(), `"default"`)
	models := fixture.request(http.MethodGet, "/api/user/models", fixture.ordinarySession, "")
	require.Equal(t, http.StatusOK, models.Code, models.Body.String())
	assert.Contains(t, models.Body.String(), "fixture-model")
	assert.NotContains(t, models.Body.String(), "sk-surface-fixture")

	// A console request that forges a channel selection in the body is refused
	// before distribution, so the pin cannot be smuggled in by the payload.
	forged := fixture.request(http.MethodPost, "/pg/chat/completions", fixture.ordinarySession,
		`{"model":"fixture-model","group":"default","channel_id":`+id+`,"messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusForbidden, forged.Code, forged.Body.String())
	assert.Contains(t, forged.Body.String(), i18n.T(nil, i18n.MsgAuthChannelSelectForbidden))
}

// TestAdminChannelReadTokenCannotOperate pins scope intersection for an
// administrator: a channel:read token lists channels but cannot start, read or
// cancel a fingerprint run, which needs the channel operate scope.
func TestAdminChannelReadTokenCannotOperate(t *testing.T) {
	fixture := setupAdminChannelSurfaceFixture(t)
	id := strconv.Itoa(fixture.channelId)

	read := fixture.request(http.MethodGet, "/api/channel/"+id, fixture.adminPAT, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())

	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/channel/" + id + "/fingerprint", `{"model":"fixture-model"}`},
		{http.MethodGet, "/api/channel/" + id + "/fingerprint/unknown-job", ""},
		{http.MethodPost, "/api/channel/" + id + "/fingerprint/unknown-job/cancel", ""},
	} {
		response := fixture.request(route.method, route.path, fixture.adminPAT, route.body)
		assert.Equal(t, http.StatusForbidden, response.Code, route.method+" "+route.path)
		assert.Contains(t, response.Body.String(), `"code":"ACCESS_TOKEN_SCOPE_DENIED"`, route.method+" "+route.path)
	}

	// The role still authorises the operate routes; only the token's narrowing
	// scope stops it, so the same request from the admin session is allowed.
	operatePAT := createAdminSurfacePAT(t, fixture.adminId, authz.ChannelOperate)
	jobRead := fixture.request(http.MethodGet, "/api/channel/"+id+"/fingerprint/unknown-job", operatePAT, "")
	require.Equal(t, http.StatusNotFound, jobRead.Code, jobRead.Body.String())
}

// TestAdminChannelSurfaceAdmitsOnlyAuthorizedAdmins proves the guards above are
// authorisation, not a broken fixture: an administrator reaches the handlers,
// and a model token is still not a dashboard credential.
func TestAdminChannelSurfaceAdmitsOnlyAuthorizedAdmins(t *testing.T) {
	fixture := setupAdminChannelSurfaceFixture(t)
	id := strconv.Itoa(fixture.channelId)

	list := fixture.request(http.MethodGet, "/api/channel/", fixture.adminSession, "")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"success":true`)
	assert.NotContains(t, list.Body.String(), "sk-surface-fixture")

	channelRead := fixture.request(http.MethodGet, "/api/channel/"+id, fixture.adminPAT, "")
	require.Equal(t, http.StatusOK, channelRead.Code, channelRead.Body.String())
	assert.Contains(t, channelRead.Body.String(), "fixture-model")

	// Reaching the handler is proven without starting paid work: an unknown job
	// id is answered by the handler, while the guard would have rejected the
	// request before it ran.
	jobRead := fixture.request(http.MethodGet, "/api/channel/"+id+"/fingerprint/unknown-job", fixture.adminSession, "")
	require.Equal(t, http.StatusNotFound, jobRead.Code, jobRead.Body.String())
	assert.Contains(t, jobRead.Body.String(), `"success":false`)

	// A model token is never a dashboard credential and cannot reach the same
	// surface through the panel routes.
	ordinaryToken := &model.Token{UserId: fixture.adminId, Key: "surface-model-token-key", Name: "surface-model-token",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "default"}
	require.NoError(t, ordinaryToken.Insert())
	for _, path := range []string{"/api/channel/", "/api/channel/" + id + "/fingerprint/unknown-job", "/pg/channels?group=default"} {
		response := fixture.request(http.MethodGet, path, ordinaryToken.Key, "")
		assert.Equal(t, http.StatusUnauthorized, response.Code, path)
		assert.NotContains(t, response.Body.String(), "fixture-model", path)
	}
}
