package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func runOriginGuardRequest(t *testing.T, origin, referer string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/user/auth/refresh", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "https://panel.example.com/api/user/auth/refresh", nil)
	request.Host = "panel.example.com"
	request.Header.Set("Origin", origin)
	if origin == "" {
		request.Header.Del("Origin")
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSessionCookieOriginGuard(t *testing.T) {
	previousSecure := common.SessionCookieSecure
	previousTrustedURLs := common.SessionCookieTrustedURLs
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = []string{"https://trusted.example.com"}
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedURLs
	})

	tests := []struct {
		name     string
		origin   string
		referer  string
		expected int
	}{
		{name: "same origin", origin: "https://panel.example.com", expected: http.StatusNoContent},
		{name: "trusted exact origin", origin: "https://trusted.example.com", expected: http.StatusNoContent},
		{name: "referer fallback", referer: "https://panel.example.com/profile", expected: http.StatusNoContent},
		{name: "missing both", expected: http.StatusForbidden},
		{name: "null origin", origin: "null", expected: http.StatusForbidden},
		{name: "suffix attack", origin: "https://trusted.example.com.evil.test", expected: http.StatusForbidden},
		{name: "scheme mismatch", origin: "http://panel.example.com", expected: http.StatusForbidden},
		{name: "path in origin", origin: "https://panel.example.com/profile", expected: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := runOriginGuardRequest(t, test.origin, test.referer)
			assert.Equal(t, test.expected, response.Code)
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func TestSessionCookieOriginGuardDevelopmentCompatibility(t *testing.T) {
	previousSecure := common.SessionCookieSecure
	previousTrustedURLs := common.SessionCookieTrustedURLs
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedURLs
	})
	common.SessionCookieTrustedURLs = nil

	tests := []struct {
		name     string
		secure   bool
		origin   string
		expected int
	}{
		{name: "insecure mode allows mismatched development origins", origin: "http://localhost:3001", expected: http.StatusNoContent},
		{name: "insecure mode allows missing origin", expected: http.StatusNoContent},
		{name: "secure mode rejects mismatched development origins", secure: true, origin: "http://localhost:3001", expected: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			common.SessionCookieSecure = test.secure

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/api/user/auth/refresh", SessionCookieOriginGuard(), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodPost, "http://localhost:3000/api/user/auth/refresh", nil)
			request.Host = "localhost:3000"
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assert.Equal(t, test.expected, response.Code)
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func runVerifiedIngressOriginGuardRequest(t *testing.T, identity common.ConsoleIngressIdentity, origin, referer string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/user/auth/refresh", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	// The master accepts the forwarded request over plain internal HTTP, so TLS is
	// truthfully nil and the only public-origin evidence is the verified envelope.
	request := httptest.NewRequest(http.MethodPost, "http://10.100.2.1:3000/api/user/auth/refresh", nil)
	request.Host = "10.100.2.1:3000"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	request = common.WithConsoleIngressIdentity(request, identity)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSessionCookieOriginGuardUsesVerifiedConsoleIngress(t *testing.T) {
	previousSecure := common.SessionCookieSecure
	previousTrustedURLs := common.SessionCookieTrustedURLs
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = nil
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedURLs
	})

	// Verified public HTTPS that reached the master over internal HTTP.
	forwarded := common.ConsoleIngressIdentity{EntryNode: "hk01", Scheme: "https", Host: "panel.example.com", Forwarded: true}
	// Same public scheme/host name but supplied without proof, so the request keeps
	// its plain-HTTP transport origin.
	unverified := common.ConsoleIngressIdentity{EntryNode: "hk01", Scheme: "https", Host: "panel.example.com"}

	tests := []struct {
		name     string
		identity common.ConsoleIngressIdentity
		origin   string
		referer  string
		expected int
	}{
		{name: "verified public https accepted", identity: forwarded, origin: "https://panel.example.com", expected: http.StatusNoContent},
		{name: "verified referer fallback accepted", identity: forwarded, referer: "https://panel.example.com/profile", expected: http.StatusNoContent},
		{name: "verified foreign origin rejected", identity: forwarded, origin: "https://evil.example.com", expected: http.StatusForbidden},
		{name: "verified null origin rejected", identity: forwarded, origin: "null", expected: http.StatusForbidden},
		{name: "verified missing origin rejected", identity: forwarded, expected: http.StatusForbidden},
		{name: "verified scheme mismatch rejected", identity: forwarded, origin: "http://panel.example.com", expected: http.StatusForbidden},
		{name: "unverified identity does not lift transport origin", identity: unverified, origin: "https://panel.example.com", expected: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := runVerifiedIngressOriginGuardRequest(t, test.identity, test.origin, test.referer)
			assert.Equal(t, test.expected, response.Code)
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

func TestSessionCookieOriginGuardDoesNotTrustForwardedProtoFromClient(t *testing.T) {
	previousSecure := common.SessionCookieSecure
	previousTrustedURLs := common.SessionCookieTrustedURLs
	common.SessionCookieSecure = true
	common.SessionCookieTrustedURLs = nil
	t.Cleanup(func() {
		common.SessionCookieSecure = previousSecure
		common.SessionCookieTrustedURLs = previousTrustedURLs
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/user/auth/refresh", SessionCookieOriginGuard(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "http://panel.example.com/api/user/auth/refresh", nil)
	request.Host = "panel.example.com"
	request.Header.Set("Origin", "https://panel.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
}
