package system_setting

import (
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// CORSAllowedOriginsEnv names the comma-separated list of additional exact
// browser origins that may read credentialed cross-origin responses. The
// configured public site address is always the first entry; operators add
// extra console origins here instead of widening the allowlist with patterns.
//
// Operators configure either the ServerAddress admin option (the public site
// origin) or this environment variable, for example:
//
//	CORS_ALLOWED_ORIGINS=https://panel.example.com,https://admin.example.com
//
// Entries must be exact origins (https://host[:port], plus HTTP loopback for
// local development) with no path, wildcard, userinfo, query or fragment.
// Malformed entries are dropped at request time, so they never match and never
// widen the allowlist; fix the value and restart. This is a CORS permission
// list only: the OAuth redirect allowlist (oauth.allowed_origins) is not
// consulted and session refresh/logout origin checks stay separate.
const CORSAllowedOriginsEnv = "CORS_ALLOWED_ORIGINS"

// CORSAllowedOrigins returns the canonical origin allowlist used for CORS
// response permissions. The public site address is the safe base; the
// CORS_ALLOWED_ORIGINS environment variable adds explicit origins.
//
// Only exact origins accepted by NormalizeOAuthOrigin are returned: HTTPS, or
// HTTP loopback for local development. That shared normalizer rejects
// wildcards, "null", paths, userinfo, query, fragment and embedded separators,
// so a bad entry can never widen the allowlist or be interpreted as a pattern.
// The site address setting is a URL, so only its origin part is used.
func CORSAllowedOrigins() []string {
	// ServerAddress is written under OptionMapRWMutex by the option store, so read
	// it under the same lock and normalize outside the critical section.
	common.OptionMapRWMutex.RLock()
	serverAddress := ServerAddress
	common.OptionMapRWMutex.RUnlock()

	origins := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	addOrigin := func(raw string) {
		origin, err := NormalizeOAuthOrigin(strings.TrimSpace(raw))
		if err != nil {
			return
		}
		if _, exists := seen[origin]; exists {
			return
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	if parsed, err := url.Parse(strings.TrimSpace(serverAddress)); err == nil && parsed.Host != "" {
		addOrigin(parsed.Scheme + "://" + parsed.Host)
	}
	for entry := range strings.SplitSeq(os.Getenv(CORSAllowedOriginsEnv), ",") {
		addOrigin(entry)
	}
	return origins
}
