package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// CallbackURIContextKey carries only the callback saved in a validated AuthFlow.
// Token exchange must never use callback query parameters or proxy headers.
const CallbackURIContextKey = "oauth_callback_uri"

// UsesBrowserCallback matches providers whose authorization URL uses the
// current frontend origin. Providers with a fixed registered callback keep
// their existing behavior.
func UsesBrowserCallback(provider Provider) bool {
	switch provider.(type) {
	case *GenericOAuthProvider, *OIDCProvider, *DiscordProvider:
		return true
	default:
		return false
	}
}

// NewBrowserCallbackURI fixes the redirect URI before leaving for the provider.
// Only operator-configured origins are trusted, including behind a TLS proxy.
func NewBrowserCallbackURI(request *http.Request, provider string) (string, error) {
	invalid := errors.New("OAuth callback origin is not allowed")
	values := request.Header.Values("Origin")
	if len(values) > 1 {
		return "", invalid
	}
	rawOrigin := system_setting.ServerAddress
	if len(values) == 1 {
		rawOrigin = values[0]
	}
	origin, err := system_setting.NormalizeOAuthOrigin(rawOrigin)
	if err != nil {
		return "", invalid
	}
	if !system_setting.IsOAuthOriginAllowed(origin) {
		return "", invalid
	}
	return origin + "/oauth/" + url.PathEscape(provider), nil
}

func callbackURI(c *gin.Context, provider string) string {
	if c != nil {
		if uri := c.GetString(CallbackURIContextKey); uri != "" {
			return uri
		}
	}
	// AuthFlows started before this change contain no callback snapshot. They
	// retain the original canonical callback until their short TTL expires.
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/oauth/" + url.PathEscape(provider)
}

// BrowserFlowCookie binds a login transaction to the browser that started it.
// A separate cookie per flow permits concurrent tabs without overwriting state.
func BrowserFlowCookie(state, redirectURI, value string, maxAge int) *http.Cookie {
	digest := sha256.Sum256([]byte(state))
	name := "new-api-oauth-" + hex.EncodeToString(digest[:16])
	secure := strings.HasPrefix(redirectURI, "https://")
	if secure {
		name = "__Host-" + name
	}
	return &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
}
