package passkey

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A console request forwarded from a worker reaches the master over plain
// internal HTTP with TLS truthfully nil. The public scheme and host the browser
// used are only available through the verified forwarding envelope, and raw
// forwarding headers must never decide the passkey origin.
func TestPasskeyOriginUsesVerifiedConsoleIngressOnly(t *testing.T) {
	forwarded := common.ConsoleIngressIdentity{EntryNode: "hk01", Scheme: "https", Host: "panel.example.com", Forwarded: true}
	unverified := common.ConsoleIngressIdentity{EntryNode: "hk01", Scheme: "https", Host: "panel.example.com"}
	settings := system_setting.PasskeySettings{Origins: "", RPID: "", AllowInsecureOrigin: false}

	forwardedRequest := func() *http.Request {
		// The WireGuard hop is plain HTTP to the internal master address.
		request := httptest.NewRequest(http.MethodPost, "http://10.100.2.1:3000/api/user/passkey/login/begin", nil)
		request.Host = "10.100.2.1:3000"
		return common.WithConsoleIngressIdentity(request, forwarded)
	}

	t.Run("verified ingress yields the public HTTPS origin", func(t *testing.T) {
		request := forwardedRequest()
		assert.Equal(t, "https", detectScheme(request))
		origins, err := resolveOrigins(request, &settings)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://panel.example.com"}, origins)
	})

	t.Run("spoofed proto header cannot override verified ingress", func(t *testing.T) {
		request := forwardedRequest()
		request.Header.Set("X-Forwarded-Proto", "http")
		assert.Equal(t, "https", detectScheme(request))
		origins, err := resolveOrigins(request, &settings)
		require.NoError(t, err)
		assert.Equal(t, []string{"https://panel.example.com"}, origins)
	})

	t.Run("unverified identity keeps the plain transport origin", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "http://10.100.2.1:3000/api/user/passkey/login/begin", nil)
		request.Host = "panel.example.com"
		request = common.WithConsoleIngressIdentity(request, unverified)
		assert.Equal(t, "http", detectScheme(request))
		assert.Equal(t, "panel.example.com", passkeyRequestHost(request))
	})

	t.Run("direct requests keep the existing scheme detection", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "http://localhost:3000/api/user/passkey/login/begin", nil)
		request.Host = "localhost:3000"
		assert.Equal(t, "http", detectScheme(request))
		assert.Equal(t, "localhost:3000", passkeyRequestHost(request))
	})
}
