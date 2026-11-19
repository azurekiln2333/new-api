package middleware

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// corsAllowedMethods is the method set the dashboard console and relay clients
// actually use. PATCH is required by console settings/update endpoints.
const corsAllowedMethods = "GET,POST,PUT,PATCH,DELETE,OPTIONS"

// corsAllowedHeaders lists the request headers accepted on credentialed
// cross-origin requests: the web console headers (Authorization, X-Security-Proof,
// cache headers) plus the headers sent by the provider SDKs the relay supports.
// Header permissions are not an authorization boundary once the origin is
// allowlisted, but an unlisted header would fail the browser preflight.
var corsAllowedHeaders = []string{
	"Accept",
	"Authorization",
	"Cache-Control",
	"Content-Type",
	"New-Api-User",
	"Origin",
	"Pragma",
	"X-Requested-With",
	"X-Security-Proof",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Anthropic-Version",
	"OpenAI-Beta",
	"OpenAI-Organization",
	"OpenAI-Project",
	"X-Api-Key",
	"X-Goog-Api-Client",
	"X-Goog-Api-Key",
	"X-Stainless-Arch",
	"X-Stainless-Helper-Method",
	"X-Stainless-Lang",
	"X-Stainless-OS",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
}

// corsPreflightMaxAge is long enough to keep console preflights cheap while a
// removed origin stops working in browsers within the same window.
const corsPreflightMaxAge = "300"

// CORS grants cross-origin response permissions only to the configured exact
// origins (public site address plus CORS_ALLOWED_ORIGINS). Requests without an
// Origin header (CLI and server-to-server API clients) still execute normally.
//
// A granted response carries the canonical allowlisted origin, never the raw
// request value, and only after NormalizeOAuthOrigin accepted it (exact HTTPS or
// HTTP loopback; no wildcard, "null", path, userinfo, query or fragment).
//
// Preflights are answered without touching a handler or requiring a session: a
// preflight is not authorization, and the real request is still authenticated.
// It is intentionally response-only: a disallowed browser origin does not abort
// the request, it simply receives no CORS permission, so non-browser clients and
// public endpoints keep working. Browser-session origin protection remains a
// separate control.
//
// Scope: only routes that already install this middleware (relay, old billing
// dashboard, usage and log routes) change behavior. Console administration
// routes and any other endpoint do not gain CORS permissions from this change.
func CORS() gin.HandlerFunc {
	allowedHeaders := strings.Join(corsAllowedHeaders, ",")
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		// Every response depends on the request Origin, including responses to
		// requests that carry none: a shared cache must never reuse a no-Origin
		// response for a later allowlisted Origin request.
		headers.Add("Vary", "Origin")

		requestOrigins := c.Request.Header.Values("Origin")
		if len(requestOrigins) == 0 {
			c.Next()
			return
		}

		preflight := c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != ""
		origin := ""
		if len(requestOrigins) == 1 && !strings.Contains(requestOrigins[0], ",") {
			if normalized, err := system_setting.NormalizeOAuthOrigin(requestOrigins[0]); err == nil {
				if slices.Contains(system_setting.CORSAllowedOrigins(), normalized) {
					origin = normalized
				}
			}
		}

		if preflight {
			headers.Add("Vary", "Access-Control-Request-Method")
			headers.Add("Vary", "Access-Control-Request-Headers")
		}

		if origin == "" {
			if preflight {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
			return
		}

		headers.Set("Access-Control-Allow-Origin", origin)
		headers.Set("Access-Control-Allow-Credentials", "true")
		if preflight {
			headers.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			headers.Set("Access-Control-Allow-Headers", allowedHeaders)
			headers.Set("Access-Control-Max-Age", corsPreflightMaxAge)
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
