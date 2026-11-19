package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCORSTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS())
	router.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	router.PATCH("/api/user/setting", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return router
}

// corsTestRequest deliberately sends a Host that is not in the allowlist, so a
// middleware that trusted the request Host would fail the denied cases.
func corsTestRequest(router *gin.Engine, method, path string, origins ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Host = "gateway.internal:3000"
	for _, origin := range origins {
		request.Header.Add("Origin", origin)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func corsPreflightRequest(router *gin.Engine, origin, method string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodOptions, "/api/user/setting", nil)
	request.Host = "gateway.internal:3000"
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", method)
	request.Header.Set("Access-Control-Request-Headers", "x-security-proof,authorization")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// setCORSServerAddress mirrors the option writer: ServerAddress is replaced under
// OptionMapRWMutex, and the previous value is restored the same way. These tests
// must not call t.Parallel: they share process-wide settings and environment.
func setCORSServerAddress(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := system_setting.ServerAddress
	system_setting.ServerAddress = value
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		system_setting.ServerAddress = previous
		common.OptionMapRWMutex.Unlock()
	})
}

func setupCORSTest(t *testing.T) {
	t.Helper()
	setCORSServerAddress(t, "https://panel.example.com/console")
	t.Setenv(system_setting.CORSAllowedOriginsEnv, "https://extra.example.com:8443, http://localhost:3001,http://127.0.0.1:3001,http://extra.example.com")
}

func TestCORSAllowsConfiguredOriginsOnly(t *testing.T) {
	setupCORSTest(t)
	router := newCORSTestRouter()

	allowed := []string{
		"https://panel.example.com",
		"https://extra.example.com:8443",
		"http://localhost:3001",
		"http://127.0.0.1:3001",
	}
	for _, origin := range allowed {
		t.Run("allowed "+origin, func(t *testing.T) {
			response := corsTestRequest(router, http.MethodGet, "/api/status", origin)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, origin, response.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, "true", response.Header().Get("Access-Control-Allow-Credentials"))
			assert.Contains(t, response.Header().Values("Vary"), "Origin")
		})
	}

	t.Run("uppercase host is canonicalized and echoed lowercased", func(t *testing.T) {
		response := corsTestRequest(router, http.MethodGet, "/api/status", "HTTPS://Panel.Example.COM")
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "https://panel.example.com", response.Header().Get("Access-Control-Allow-Origin"),
			"the canonical allowlisted origin is echoed, never the raw request header")
	})

	denied := map[string]string{
		"unknown origin":       "https://attacker.example.com",
		"suffix attack":        "https://panel.example.com.attacker.example.com",
		"scheme mismatch":      "http://panel.example.com",
		"path in origin":       "https://panel.example.com/console",
		"null origin":          "null",
		"malformed origin":     "https://",
		"other port":           "https://panel.example.com:8443",
		"unrelated host":       "https://gateway.internal:3000",
		"embedded userinfo":    "https://user:secret@panel.example.com",
		"origin with query":    "https://panel.example.com?next=https://attacker.example.com",
		"origin with fragment": "https://panel.example.com#attacker",
		"wildcard origin":      "https://*.example.com",
		"trailing dot host":    "https://panel.example.com.",
		"insecure extra host":  "http://extra.example.com",
		"loopback lookalike":   "http://localhost.attacker.example.com",
	}
	for name, origin := range denied {
		t.Run("denied "+name, func(t *testing.T) {
			response := corsTestRequest(router, http.MethodGet, "/api/status", origin)
			assert.Equal(t, http.StatusOK, response.Code, "a denied origin must not abort the request")
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
			assert.Contains(t, response.Header().Values("Vary"), "Origin")
		})
	}
}

func TestCORSRejectsAmbiguousOriginHeaders(t *testing.T) {
	setupCORSTest(t)
	router := newCORSTestRouter()

	t.Run("two origin headers", func(t *testing.T) {
		response := corsTestRequest(router, http.MethodGet, "/api/status", "https://panel.example.com", "https://attacker.example.com")
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("comma joined origin", func(t *testing.T) {
		response := corsTestRequest(router, http.MethodGet, "/api/status", "https://panel.example.com, https://attacker.example.com")
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestCORSWithoutOriginHeaderKeepsWorking(t *testing.T) {
	setupCORSTest(t)
	router := newCORSTestRouter()

	response := corsTestRequest(router, http.MethodGet, "/api/status")
	assert.Equal(t, http.StatusOK, response.Code, "CLI and server-to-server clients need no Origin header")
	assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
	// A shared cache must not reuse this response for a later Origin request.
	assert.Contains(t, response.Header().Values("Vary"), "Origin")
}

func TestCORSPreflight(t *testing.T) {
	setupCORSTest(t)
	router := newCORSTestRouter()

	t.Run("allowed preflight", func(t *testing.T) {
		response := corsPreflightRequest(router, "https://panel.example.com", http.MethodPatch)
		require.Equal(t, http.StatusNoContent, response.Code)
		assert.Equal(t, "https://panel.example.com", response.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", response.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, response.Header().Get("Access-Control-Allow-Methods"), http.MethodPatch)
		assert.Contains(t, response.Header().Get("Access-Control-Allow-Headers"), "X-Security-Proof")
		assert.Equal(t, "300", response.Header().Get("Access-Control-Max-Age"))

		vary := response.Header().Values("Vary")
		assert.Contains(t, vary, "Origin")
		assert.Contains(t, vary, "Access-Control-Request-Method")
		assert.Contains(t, vary, "Access-Control-Request-Headers")
	})

	t.Run("denied preflight", func(t *testing.T) {
		response := corsPreflightRequest(router, "https://attacker.example.com", http.MethodPatch)
		assert.Equal(t, http.StatusNoContent, response.Code)
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Methods"))
		assert.Contains(t, response.Header().Values("Vary"), "Origin")
	})

	t.Run("null preflight", func(t *testing.T) {
		response := corsPreflightRequest(router, "null", http.MethodPost)
		assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestCORSAllowedOriginsIgnoresMalformedEntries(t *testing.T) {
	setCORSServerAddress(t, "https://panel.example.com/app/")
	t.Setenv(system_setting.CORSAllowedOriginsEnv,
		" https://Extra.Example.com ,https://panel.example.com,*,null,https://bad.example.com/path,not-a-url,http://localhost:3001,,"+
			"https://user:pw@panel.example.com,https://panel.example.com?x=1,https://panel.example.com#f,HTTPS://panel.example.com,"+
			"http://extra.example.com,http://127.0.0.1:3001,https://extra.example.com ")

	assert.Equal(t,
		[]string{"https://panel.example.com", "https://extra.example.com", "http://localhost:3001", "http://127.0.0.1:3001"},
		system_setting.CORSAllowedOrigins(),
	)
}
