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

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
