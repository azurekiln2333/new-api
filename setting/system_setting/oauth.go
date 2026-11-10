package system_setting

import (
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const OAuthAllowedOriginsOption = "oauth.allowed_origins"

type OAuthSettings struct {
	AllowedOrigins string `json:"allowed_origins"`
}

var oauthSettings OAuthSettings

func init() {
	config.GlobalConfig.Register("oauth", &oauthSettings)
}

func OAuthSettingsSnapshot() OAuthSettings {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return oauthSettings
}

// NormalizeOAuthOrigin accepts exact HTTPS websites, plus HTTP loopback for
// local development. It never accepts a callback path or wildcard domain.
func NormalizeOAuthOrigin(raw string) (string, error) {
	invalid := common.NewMessage("Callback URL configuration error")
	if strings.ContainsAny(raw, "?#,") {
		return "", invalid
	}
	origin, err := common.NormalizeOrigin(raw)
	if err != nil {
		return "", invalid
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "https" && parsed.Hostname() != "localhost" && !net.ParseIP(parsed.Hostname()).IsLoopback()) {
		return "", invalid
	}
	return origin, nil
}

// ParseOAuthAllowedOrigins is shared by the settings API and authentication
// guards, so direct API writes cannot bypass the panel's validation.
func ParseOAuthAllowedOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var origins []string
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for entry := range strings.SplitSeq(line, ",") {
			origin, err := NormalizeOAuthOrigin(entry)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(origins, origin) {
				origins = append(origins, origin)
			}
		}
	}
	return origins, nil
}

func IsOAuthOriginAllowed(origin string) bool {
	common.OptionMapRWMutex.RLock()
	settings, serverAddress := oauthSettings, ServerAddress
	common.OptionMapRWMutex.RUnlock()
	origins, err := ParseOAuthAllowedOrigins(settings.AllowedOrigins)
	if err != nil {
		return false
	}
	canonical, err := NormalizeOAuthOrigin(serverAddress)
	return (err == nil && origin == canonical) || slices.Contains(origins, origin)
}
