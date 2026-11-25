package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestClientIP(router http.Handler, remoteAddr string, forwardedFor string) string {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	request.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	router.ServeHTTP(recorder, request)
	return recorder.Body.String()
}

func newClientIPRouter() *gin.Engine {
	router := gin.New()
	router.GET("/client-ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	return router
}

func TestConfigureTrustedProxiesDefaultsToLoopbackAndPrivateNetworks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", "")
	router := newClientIPRouter()
	require.NoError(t, ConfigureTrustedProxies(router))

	testCases := []struct {
		name       string
		remoteAddr string
	}{
		{name: "IPv4 loopback", remoteAddr: "127.0.0.1:12345"},
		{name: "IPv6 loopback", remoteAddr: "[::1]:12345"},
		{name: "10 private network", remoteAddr: "10.20.30.40:12345"},
		{name: "172 private network", remoteAddr: "172.20.0.2:12345"},
		{name: "192 private network", remoteAddr: "192.168.10.2:12345"},
		{name: "IPv6 unique local network", remoteAddr: "[fd12:3456::2]:12345"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clientIP := requestClientIP(router, testCase.remoteAddr, "203.0.113.10")
			assert.Equal(t, "203.0.113.10", clientIP)
		})
	}
}

func TestConfigureTrustedProxiesDefaultRejectsPublicPeerHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", " \t ")
	router := newClientIPRouter()
	require.NoError(t, ConfigureTrustedProxies(router))

	clientIP := requestClientIP(router, "198.51.100.10:12345", "203.0.113.10")
	assert.Equal(t, "198.51.100.10", clientIP, "a public peer must not make a spoofed X-Forwarded-For authoritative")
}

func TestConfigureTrustedProxiesDefaultStopsAtPublicClientInForwardedChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", "")
	router := newClientIPRouter()
	require.NoError(t, ConfigureTrustedProxies(router))

	clientIP := requestClientIP(router, "172.20.0.2:12345", "192.0.2.99, 203.0.113.10")
	assert.Equal(t, "203.0.113.10", clientIP, "the first public hop from the trusted proxy must win over a client-supplied prefix")
}

func TestConfigureTrustedProxiesNoneDisablesForwardedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", " NoNe ")
	router := newClientIPRouter()
	require.NoError(t, ConfigureTrustedProxies(router))

	clientIP := requestClientIP(router, "127.0.0.1:12345", "203.0.113.10")
	assert.Equal(t, "127.0.0.1", clientIP)
}

func TestConfigureTrustedProxiesAcceptsTrimmedIPsAndCIDRs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", " 192.0.2.0/24, 198.51.100.30 ")
	router := newClientIPRouter()
	require.NoError(t, ConfigureTrustedProxies(router))

	trustedClientIP := requestClientIP(router, "192.0.2.10:12345", "203.0.113.20")
	assert.Equal(t, "203.0.113.20", trustedClientIP)

	trustedExactIP := requestClientIP(router, "198.51.100.30:12345", "203.0.113.21")
	assert.Equal(t, "203.0.113.21", trustedExactIP)

	untrustedClientIP := requestClientIP(router, "198.51.100.20:12345", "203.0.113.22")
	assert.Equal(t, "198.51.100.20", untrustedClientIP)

	defaultProxyIP := requestClientIP(router, "127.0.0.1:12345", "203.0.113.23")
	assert.Equal(t, "127.0.0.1", defaultProxyIP, "an explicit list must replace, not extend, the compatibility defaults")
}

func TestConfigureTrustedProxiesRejectsInvalidConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCases := []struct {
		name  string
		value string
	}{
		{name: "no entries", value: ", ,"},
		{name: "invalid entry", value: "not-an-ip"},
		{name: "mixed valid and invalid entries", value: "127.0.0.1, not-an-ip"},
		{name: "none mixed with valid entry", value: "none,127.0.0.1"},
		{name: "valid entry mixed with none", value: "127.0.0.1,NONE"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", testCase.value)
			router := newClientIPRouter()
			assert.Error(t, ConfigureTrustedProxies(router))
		})
	}
}

// ---- console forwarding -------------------------------------------------

const consoleForwardTestSecret = "0123456789abcdef0123456789abcdef"

var consoleForwardEnvNames = []string{
	"CONSOLE_FORWARD_MASTER_URL",
	"CONSOLE_FORWARD_SECRET",
	"CONSOLE_FORWARD_TRUSTED_CIDRS",
	"CONSOLE_FORWARD_PUBLIC_ORIGINS",
	"CONSOLE_FORWARD_DIAL_TIMEOUT_MS",
	"CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS",
	"CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS",
}

func clearConsoleForwardEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TRUSTED_PROXIES", "none")
	for _, name := range consoleForwardEnvNames {
		t.Setenv(name, "")
	}
}

func setConsoleForwardEnv(t *testing.T, values map[string]string) {
	t.Helper()
	clearConsoleForwardEnv(t)
	for name, value := range values {
		t.Setenv(name, value)
	}
}

// useConsoleNodeRole sets the deployment role that config validation and the
// forwarder consult. The package variables are the production values loaded by
// common.InitEnv, so tests must set and restore them explicitly.
func useConsoleNodeRole(t *testing.T, master bool, nodeName string) {
	t.Helper()
	previousMaster := common.IsMasterNode
	previousName := common.NodeName
	common.IsMasterNode = master
	common.NodeName = nodeName
	t.Cleanup(func() {
		common.IsMasterNode = previousMaster
		common.NodeName = previousName
	})
}

func useConsoleForwardSettings(t *testing.T, config *common.ConsoleForwardConfig) {
	t.Helper()
	previous := common.ConsoleForwardSettings
	common.ConsoleForwardSettings = config
	t.Cleanup(func() { common.ConsoleForwardSettings = previous })
}

func mustConsoleCIDR(t *testing.T, raw string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(raw)
	require.NoError(t, err)
	return network
}

// newConsoleForwardTestConfig builds the immutable startup configuration that
// production loads from the CONSOLE_FORWARD_* environment once.
func newConsoleForwardTestConfig(t *testing.T, masterURL string) *common.ConsoleForwardConfig {
	t.Helper()
	config := &common.ConsoleForwardConfig{
		Secret:               []byte(consoleForwardTestSecret),
		TrustedPeers:         []*net.IPNet{mustConsoleCIDR(t, "10.100.3.0/24")},
		IngressTrustedPeers:  []*net.IPNet{mustConsoleCIDR(t, "192.0.2.0/24")},
		PublicOrigins:        map[string]struct{}{"https://console.example.com": {}},
		DialTimeoutMillis:    2000,
		HeaderTimeoutSecond:  5,
		RequestTimeoutSecond: 30,
	}
	if masterURL != "" {
		parsed, err := url.Parse(masterURL)
		require.NoError(t, err)
		config.MasterURL = parsed
	}
	return config
}

func TestLoadConsoleForwardConfigUnsetKeepsLocalBehavior(t *testing.T) {
	clearConsoleForwardEnv(t)
	useConsoleNodeRole(t, false, "hk01")

	config, err := common.LoadConsoleForwardConfig()
	require.NoError(t, err)
	assert.Nil(t, config, "an entirely unset configuration must preserve today's local behavior")
}

func TestLoadConsoleForwardConfigRejectsPartialAndInvalidConfig(t *testing.T) {
	validWorker := map[string]string{
		"CONSOLE_FORWARD_MASTER_URL":              "http://10.100.2.1:3000",
		"CONSOLE_FORWARD_SECRET":                  consoleForwardTestSecret,
		"CONSOLE_FORWARD_TRUSTED_CIDRS":           "10.100.3.0/24",
		"CONSOLE_FORWARD_PUBLIC_ORIGINS":          "https://console.example.com",
		"CONSOLE_FORWARD_DIAL_TIMEOUT_MS":         "2000",
		"CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS":  "120",
		"CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS": "300",
	}

	withOverride := func(overrides map[string]string) map[string]string {
		values := make(map[string]string, len(validWorker))
		for name, value := range validWorker {
			values[name] = value
		}
		for name, value := range overrides {
			values[name] = value
		}
		return values
	}

	testCases := []struct {
		name   string
		master bool
		env    map[string]string
	}{
		{name: "partial configuration with only the secret", env: map[string]string{"CONSOLE_FORWARD_SECRET": consoleForwardTestSecret}},
		{name: "partial configuration with only the target", env: map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000"}},
		{name: "partial configuration with only timeouts", env: map[string]string{"CONSOLE_FORWARD_DIAL_TIMEOUT_MS": "2000"}},
		{name: "short secret", env: withOverride(map[string]string{"CONSOLE_FORWARD_SECRET": "too-short"})},
		{name: "whitespace padded short secret", env: withOverride(map[string]string{"CONSOLE_FORWARD_SECRET": strings.Repeat(" ", 32) + "short"})},
		{name: "missing trusted peers", env: withOverride(map[string]string{"CONSOLE_FORWARD_TRUSTED_CIDRS": ""})},
		{name: "missing public origins", env: withOverride(map[string]string{"CONSOLE_FORWARD_PUBLIC_ORIGINS": ""})},
		{name: "invalid peer entry", env: withOverride(map[string]string{"CONSOLE_FORWARD_TRUSTED_CIDRS": "10.100.3.0/24,not-a-cidr"})},
		{name: "empty peer entry", env: withOverride(map[string]string{"CONSOLE_FORWARD_TRUSTED_CIDRS": "10.100.3.0/24,,"})},
		{name: "plaintext non-loopback origin", env: withOverride(map[string]string{"CONSOLE_FORWARD_PUBLIC_ORIGINS": "http://console.example.com"})},
		{name: "origin with a path", env: withOverride(map[string]string{"CONSOLE_FORWARD_PUBLIC_ORIGINS": "https://console.example.com/console"})},
		{name: "empty origin entry", env: withOverride(map[string]string{"CONSOLE_FORWARD_PUBLIC_ORIGINS": "https://console.example.com,,"})},
		{name: "target with a path", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000/api"})},
		{name: "target with credentials", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://user:pass@10.100.2.1:3000"})},
		{name: "target with a query", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000/?a=b"})},
		{name: "target with empty query", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000?"})},
		{name: "target with empty fragment", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000#"})},
		{name: "target with escaped root", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000/%2F"})},
		{name: "target on a public address", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://8.8.8.8:3000"})},
		{name: "target on loopback", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://127.0.0.1:3000"})},
		{name: "target without a port", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1"})},
		{name: "worker without a target", env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": ""})},
		{name: "master with a target", master: true, env: withOverride(map[string]string{"CONSOLE_FORWARD_MASTER_URL": "http://10.100.2.1:3000"})},
		{name: "dial timeout below the bound", env: withOverride(map[string]string{"CONSOLE_FORWARD_DIAL_TIMEOUT_MS": "0"})},
		{name: "dial timeout above the bound", env: withOverride(map[string]string{"CONSOLE_FORWARD_DIAL_TIMEOUT_MS": "10001"})},
		{name: "dial timeout that is not a number", env: withOverride(map[string]string{"CONSOLE_FORWARD_DIAL_TIMEOUT_MS": "soon"})},
		{name: "header timeout above the bound", env: withOverride(map[string]string{"CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS": "601"})},
		{name: "request timeout above the bound", env: withOverride(map[string]string{"CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS": "3601"})},
		{name: "request timeout below the bound", env: withOverride(map[string]string{"CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS": "0"})},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			setConsoleForwardEnv(t, testCase.env)
			useConsoleNodeRole(t, testCase.master, "hk01")

			config, err := common.LoadConsoleForwardConfig()
			require.Error(t, err, "malformed configuration must fail startup instead of silently disabling trust")
			assert.Nil(t, config)
		})
	}
}

func TestLoadConsoleForwardConfigAppliesBoundedTimeouts(t *testing.T) {
	useConsoleNodeRole(t, false, "hk01")

	setConsoleForwardEnv(t, map[string]string{
		"CONSOLE_FORWARD_MASTER_URL":     "http://10.100.2.1:3000",
		"CONSOLE_FORWARD_SECRET":         consoleForwardTestSecret,
		"CONSOLE_FORWARD_TRUSTED_CIDRS":  "10.100.3.0/24",
		"CONSOLE_FORWARD_PUBLIC_ORIGINS": "https://console.example.com",
	})
	config, err := common.LoadConsoleForwardConfig()
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, 2000, config.DialTimeoutMillis)
	assert.Equal(t, 120, config.HeaderTimeoutSecond)
	assert.Equal(t, 300, config.RequestTimeoutSecond)
	require.NotNil(t, config.MasterURL)
	assert.Equal(t, "10.100.2.1:3000", config.MasterURL.Host)
	assert.Equal(t, "http", config.MasterURL.Scheme)
	assert.Empty(t, config.MasterURL.Path+config.MasterURL.RawPath, "the target must carry no path prefix")
	assert.Empty(t, config.MasterURL.RawQuery)
	assert.Contains(t, config.PublicOrigins, "https://console.example.com")

	setConsoleForwardEnv(t, map[string]string{
		"CONSOLE_FORWARD_MASTER_URL":              "http://10.100.2.1:3000",
		"CONSOLE_FORWARD_SECRET":                  consoleForwardTestSecret,
		"CONSOLE_FORWARD_TRUSTED_CIDRS":           "10.100.3.0/24",
		"CONSOLE_FORWARD_PUBLIC_ORIGINS":          "https://console.example.com",
		"CONSOLE_FORWARD_DIAL_TIMEOUT_MS":         "750",
		"CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS":  "30",
		"CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS": "600",
	})
	config, err = common.LoadConsoleForwardConfig()
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, 750, config.DialTimeoutMillis)
	assert.Equal(t, 30, config.HeaderTimeoutSecond)
	assert.Equal(t, 600, config.RequestTimeoutSecond)
}

func TestLoadConsoleForwardConfigMasterNeverForwards(t *testing.T) {
	useConsoleNodeRole(t, true, "us01")

	setConsoleForwardEnv(t, map[string]string{
		"CONSOLE_FORWARD_SECRET":         consoleForwardTestSecret,
		"CONSOLE_FORWARD_TRUSTED_CIDRS":  "10.100.3.7/32",
		"CONSOLE_FORWARD_PUBLIC_ORIGINS": "https://console.example.com",
	})
	config, err := common.LoadConsoleForwardConfig()
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Nil(t, config.MasterURL, "the master must accept signed ingress without ever forwarding")
}

func TestVerifiedConsoleIngressRequiresProof(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "http://master.internal/api/models", nil)
	_, ok := common.VerifiedConsoleIngress(plain)
	assert.False(t, ok)
	_, ok = common.VerifiedConsoleIngress(nil)
	assert.False(t, ok)

	identity := common.ConsoleIngressIdentity{
		EntryNode: "hk01",
		ClientIP:  net.ParseIP("203.0.113.9"),
		Scheme:    "https",
		Host:      "console.example.com",
		RequestID: "req-signed",
		Forwarded: true,
	}
	signed := common.WithConsoleIngressIdentity(plain, identity)
	verified, ok := common.VerifiedConsoleIngress(signed)
	require.True(t, ok)
	assert.Equal(t, "hk01", verified.EntryNode)
	assert.True(t, verified.Forwarded)
}

func TestEffectiveRequestSchemeUsesSignedSchemeOnlyWhenForwarded(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://master.internal/api/models", nil)
	assert.Equal(t, "http", common.EffectiveRequestScheme(request), "an internal plaintext hop must stay http without a signed envelope")

	request.TLS = &tls.ConnectionState{}
	assert.Equal(t, "https", common.EffectiveRequestScheme(request))

	request.TLS = nil
	request.Header.Set("X-Forwarded-Proto", "https")
	assert.Equal(t, "http", common.EffectiveRequestScheme(request), "an unverified X-Forwarded-Proto must never change the effective scheme")

	forwarded := common.WithConsoleIngressIdentity(request, common.ConsoleIngressIdentity{Scheme: "https", Forwarded: true})
	assert.Equal(t, "https", common.EffectiveRequestScheme(forwarded))

	unforwarded := common.WithConsoleIngressIdentity(request, common.ConsoleIngressIdentity{Scheme: "https"})
	assert.Equal(t, "http", common.EffectiveRequestScheme(unforwarded), "provenance that was not forwarded must not override the real TLS state")
}

type consoleIngressProbe struct {
	called   int
	verified bool
	identity common.ConsoleIngressIdentity
	clientIP string
	host     string
}

func newConsoleIngressRouter(t *testing.T) (*gin.Engine, *consoleIngressProbe) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	probe := &consoleIngressProbe{}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(ConsoleIngress(), RequestId())
	router.Any("/*path", func(c *gin.Context) {
		probe.called++
		probe.identity, probe.verified = common.VerifiedConsoleIngress(c.Request)
		probe.clientIP = c.ClientIP()
		probe.host = c.Request.Host
		c.Status(http.StatusOK)
	})
	return router, probe
}

func signedConsoleForwardRequest(t *testing.T, config *common.ConsoleForwardConfig, identity common.ConsoleIngressIdentity) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://console.example.com/api/user/login", strings.NewReader(`{"username":"u"}`))
	request.RemoteAddr = "10.100.3.7:48210"
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	require.NoError(t, addConsoleForwardEnvelope(request, identity, config.Secret))
	return request
}

func newConsoleIngressIdentity() common.ConsoleIngressIdentity {
	return common.ConsoleIngressIdentity{
		EntryNode: "hk01",
		ClientIP:  net.ParseIP("203.0.113.9"),
		Scheme:    "https",
		Host:      "console.example.com",
		RequestID: "req-0001",
	}
}

func TestConsoleIngressRequiresTrustedPeerAndValidProof(t *testing.T) {
	useConsoleNodeRole(t, true, "us01")
	config := newConsoleForwardTestConfig(t, "")
	useConsoleForwardSettings(t, config)

	t.Run("trusted peer and valid proof install provenance", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, probe.called)
		assert.True(t, probe.verified)
		assert.Equal(t, "hk01", probe.identity.EntryNode)
		assert.Equal(t, "https", probe.identity.Scheme)
		assert.Equal(t, "req-0001", recorder.Header().Get(common.RequestIdKey), "verified ID must survive the ingress -> RequestId order")
		assert.Equal(t, "console.example.com", probe.host)
		assert.Equal(t, "203.0.113.9", probe.clientIP, "the signed client IP must drive rate limiting and audit")
	})

	t.Run("signed identity overrides spoofed proxy aliases", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "CF-Connecting-IP", "X-Forwarded-Host", "X-Forwarded-Proto"} {
			request.Header.Set(name, "198.51.100.99")
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "203.0.113.9", probe.clientIP)
		assert.Equal(t, "console.example.com", probe.host)
		assert.Equal(t, "10.100.3.7", probe.identity.PeerIP.String())
		for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "CF-Connecting-IP", "X-Forwarded-Host", "X-Forwarded-Proto", consoleForwardProofHeader} {
			assert.Empty(t, request.Header.Values(name), name)
		}
	})

	t.Run("trusted peer with a tampered proof is rejected", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		request.Header.Set(consoleForwardProofHeader, "dGFtcGVyZWQ")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Zero(t, probe.called, "an invalid envelope from a configured peer must fail closed before any handler runs")
	})

	t.Run("trusted peer with a stale proof is rejected", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		request.Header.Set(consoleForwardTimestampHeader, strconv.FormatInt(time.Now().Add(-3*time.Minute).Unix(), 10))
		values, err := consoleForwardHeaderValues(request)
		require.NoError(t, err)
		request.Header.Set(consoleForwardProofHeader, consoleForwardProof(request, values, config.Secret))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code, "a correctly signed but expired envelope must be rejected")
		assert.Zero(t, probe.called)
	})

	t.Run("trusted peer with a future timestamp is rejected", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		request.Header.Set(consoleForwardTimestampHeader, strconv.FormatInt(time.Now().Add(3*time.Minute).Unix(), 10))
		values, err := consoleForwardHeaderValues(request)
		require.NoError(t, err)
		request.Header.Set(consoleForwardProofHeader, consoleForwardProof(request, values, config.Secret))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Zero(t, probe.called)
	})

	t.Run("replayed proof is rejected", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		replay := request.Clone(request.Context())

		first := httptest.NewRecorder()
		router.ServeHTTP(first, request)
		require.Equal(t, http.StatusOK, first.Code)

		second := httptest.NewRecorder()
		router.ServeHTTP(second, replay)
		require.Equal(t, http.StatusForbidden, second.Code, "a pristine copy of the envelope must not reuse the nonce")
		assert.Equal(t, 1, probe.called)
	})

	t.Run("untrusted public peer cannot use reserved headers", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		request.RemoteAddr = "198.51.100.10:48210"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, probe.called)
		assert.False(t, probe.verified, "a public peer must never install signed provenance")
		assert.Equal(t, "198.51.100.10", probe.clientIP)
		assert.Empty(t, request.Header.Get(consoleForwardProofHeader), "reserved headers must be stripped before the local chain runs")
	})

	t.Run("signed envelope outside the configured origins is rejected", func(t *testing.T) {
		router, probe := newConsoleIngressRouter(t)
		identity := newConsoleIngressIdentity()
		identity.Host = "evil.example.com"
		request := signedConsoleForwardRequest(t, config, identity)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Zero(t, probe.called)
	})

	t.Run("valid envelope arriving on a worker is a loop", func(t *testing.T) {
		useConsoleNodeRole(t, false, "hk02")
		router, probe := newConsoleIngressRouter(t)
		request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusLoopDetected, recorder.Code, "a forwarded request must never be forwarded again")
		assert.Zero(t, probe.called)
	})
}

func TestConsoleIngressWorkerLoopGuardRequiresValidProof(t *testing.T) {
	useConsoleNodeRole(t, false, "hk02")
	config := newConsoleForwardTestConfig(t, "")
	useConsoleForwardSettings(t, config)
	for _, testCase := range []struct {
		name   string
		peer   string
		valid  bool
		status int
	}{
		{name: "valid envelope from unlisted worker", peer: "198.51.100.25:3000", valid: true, status: http.StatusLoopDetected},
		{name: "spoofed public marker", peer: "198.51.100.25:3000", status: http.StatusOK},
		{name: "bad proof from configured peer", peer: "10.100.3.7:3000", status: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, probe := newConsoleIngressRouter(t)
			request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
			request.RemoteAddr = testCase.peer
			if !testCase.valid {
				request.Header.Set(consoleForwardProofHeader, "invalid")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.status, recorder.Code)
			assert.False(t, probe.verified, "loop detection may reject but must never grant ingress provenance")
			if testCase.status == http.StatusOK {
				assert.Equal(t, 1, probe.called)
				assert.Empty(t, request.Header.Get(consoleForwardProofHeader))
				return
			}
			assert.Zero(t, probe.called)
		})
	}
}

func TestConsoleIngressRejectsTamperedRequestBindings(t *testing.T) {
	useConsoleNodeRole(t, true, "us01")
	config := newConsoleForwardTestConfig(t, "")
	useConsoleForwardSettings(t, config)

	testCases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "method", mutate: func(r *http.Request) { r.Method = http.MethodDelete }},
		{name: "path", mutate: func(r *http.Request) { r.URL.Path = "/api/user/register" }},
		{name: "query", mutate: func(r *http.Request) { r.URL.RawQuery = "role=root" }},
		{name: "authorization", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer other") }},
		{name: "cookie", mutate: func(r *http.Request) { r.Header.Set("Cookie", "session=other") }},
		{name: "origin", mutate: func(r *http.Request) { r.Header.Set("Origin", "https://foreign.example") }},
		{name: "referer", mutate: func(r *http.Request) { r.Header.Set("Referer", "https://foreign.example/login") }},
		{name: "content type", mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{name: "content encoding", mutate: func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }},
		{name: "declared content length", mutate: func(r *http.Request) { r.ContentLength++ }},
		{name: "entry node", mutate: func(r *http.Request) { r.Header.Set(consoleForwardEntryNodeHeader, "other") }},
		{name: "request id", mutate: func(r *http.Request) { r.Header.Set(consoleForwardRequestIDHeader, "other") }},
		{name: "duplicate proof", mutate: func(r *http.Request) { r.Header.Add(consoleForwardProofHeader, r.Header.Get(consoleForwardProofHeader)) }},
		{name: "missing proof", mutate: func(r *http.Request) { r.Header.Del(consoleForwardProofHeader) }},
		{name: "oversized entry node", mutate: func(r *http.Request) { r.Header.Set(consoleForwardEntryNodeHeader, strings.Repeat("n", 129)) }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			router, probe := newConsoleIngressRouter(t)
			request := signedConsoleForwardRequest(t, config, newConsoleIngressIdentity())
			testCase.mutate(request)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Zero(t, probe.called, "tampered metadata must never reach the master handler")
		})
	}
}

// consoleFlushRecorder records incremental flushes without requiring a hijacker.
// It models an HTTP/2-style response writer for the stream-abort regression.
type consoleFlushRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *consoleFlushRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *consoleFlushRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *consoleFlushRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *consoleFlushRecorder) Flush() {}

// consolePublicRequest builds the browser-facing request that reaches a worker's
// public HTTPS domain.
func consolePublicRequest(t *testing.T, method, pathAndQuery, body string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "https://console.example.com"+pathAndQuery, reader).WithContext(t.Context())
	request.TLS = &tls.ConnectionState{}
	request.Host = "console.example.com"
	request.RemoteAddr = "198.51.100.20:51000"
	return request
}

func TestConsoleForwarderScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	var mu sync.Mutex
	executed := make(map[string]int)
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		executed[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"from":"master"}`))
	}))
	defer master.Close()

	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))

	var local atomic.Int32
	engine := gin.New()
	// Production order: RequestId installs the id the forwarder signs, so the
	// forwarder must never be mounted alone.
	engine.Use(RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) {
		local.Add(1)
		c.String(http.StatusOK, "local")
	})

	testCases := []struct {
		method    string
		path      string
		forwarded bool
	}{
		{method: http.MethodGet, path: "/api/models", forwarded: true},
		{method: http.MethodGet, path: "/api", forwarded: true},
		{method: http.MethodPost, path: "/api/user/login", forwarded: true},
		{method: http.MethodPost, path: "/api/channel/upload", forwarded: true},
		{method: http.MethodGet, path: "/api/oauth/github", forwarded: true},
		{method: http.MethodGet, path: "/api/log/export", forwarded: true},
		{method: http.MethodGet, path: "/api/log/self/export", forwarded: true},
		{method: http.MethodOptions, path: "/api/user/self", forwarded: true},
		{method: http.MethodGet, path: "/api/unknown/new-console-route", forwarded: true},
		{method: http.MethodPost, path: "/api/status/test", forwarded: true},
		{method: http.MethodGet, path: "/api/channel/42/fingerprint/job-1", forwarded: true},
		{method: http.MethodPost, path: "/api/channel/42/fingerprint/job-1/cancel", forwarded: true},
		{method: http.MethodPost, path: "/api/channel/ollama/pull/stream", forwarded: true},
		{method: http.MethodGet, path: "/api/status/test", forwarded: false},
		{method: http.MethodPost, path: "/v1/chat/completions", forwarded: false},
		{method: http.MethodGet, path: "/v1beta/openai/models", forwarded: false},
		{method: http.MethodPost, path: "/pg/chat/completions", forwarded: false},
		{method: http.MethodGet, path: "/pg/channels", forwarded: false},
		{method: http.MethodPost, path: "/mj/submit/imagine", forwarded: false},
		{method: http.MethodGet, path: "/oauth/github", forwarded: false},
		{method: http.MethodGet, path: "/oauth/plugin/test/callback", forwarded: false},
		{method: http.MethodGet, path: "/dashboard/billing/usage", forwarded: false},
		{method: http.MethodGet, path: "/assets/index.js", forwarded: false},
		{method: http.MethodGet, path: "/apix/models", forwarded: false},
		{method: http.MethodGet, path: "/", forwarded: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			local.Store(0)
			mu.Lock()
			executed = make(map[string]int)
			mu.Unlock()

			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, consolePublicRequest(t, testCase.method, testCase.path, ""))

			mu.Lock()
			masterHits := executed[testCase.method+" "+testCase.path]
			totalMasterHits := len(executed)
			mu.Unlock()

			if testCase.forwarded {
				require.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "master")
				assert.Zero(t, local.Load(), "a forwarded console request must not run the worker handler")
				assert.Equal(t, 1, masterHits)
				return
			}
			assert.Equal(t, int32(1), local.Load(), "relay, SPA, health-test and static paths must stay on the worker")
			assert.Equal(t, "local", recorder.Body.String())
			assert.Zero(t, totalMasterHits)
		})
	}
}

func TestConsoleForwarderRejectsAmbiguousEncodedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	masterHits := atomic.Int32{}
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		masterHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))

	engine := gin.New()
	engine.Use(RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "local") })

	for _, path := range []string{"/api/models%2F..%2Fv1/chat", "/api/%2e%2e/v1/chat", "/api/models%5csecret", "/api/../v1/chat"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodGet, path, ""))
			assert.Equal(t, http.StatusBadRequest, recorder.Code, "an ambiguous path must not be sent to a different scope")
			assert.Zero(t, masterHits.Load())
		})
	}
}

func TestConsoleForwarderSkipsWorkerDatastoreMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"from":"master"}`))
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))

	var datastoreMiddlewareEntered atomic.Int32
	engine := gin.New()
	engine.Use(RequestId(), ConsoleForwarder())
	// Every middleware registered here stands in for worker middleware that
	// performs Redis/SQL work on the forwarded path.
	engine.Use(func(c *gin.Context) {
		datastoreMiddlewareEntered.Add(1)
		c.Next()
	})
	engine.GET("/api/models", func(c *gin.Context) { c.String(http.StatusOK, "worker handler") })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodGet, "/api/models", ""))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "master")
	assert.Zero(t, datastoreMiddlewareEntered.Load(), "a forwarded request must perform zero worker datastore work")
}

func TestConsoleForwarderPreservesRequestAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	captured := make(chan *http.Request, 1)
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		captured <- r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Master-Echo", r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" cookie="+r.Header.Get("Cookie"))
		w.Header().Set("Trailer", "X-Master-Trailer")
		w.Header().Add("Set-Cookie", "a=1; Path=/; HttpOnly; Secure")
		w.Header().Add("Set-Cookie", "b=2; Path=/api/user/auth; SameSite=Strict")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"echo":` + strconv.Quote(string(body)) + `}`))
		w.Header().Set("X-Master-Trailer", "response-complete")
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))

	engine := gin.New()
	engine.Use(RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "local") })

	request := consolePublicRequest(t, http.MethodPost, "/api/%75ser/login?next=%2Fconsole&x=1&semi=a;b&bad=%zz", `{"username":"alice"}`)
	request.Header.Set("Authorization", "Bearer session-token")
	request.Header.Set("Cookie", "session=abc")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "identity")
	request.Header.Set("Origin", "https://console.example.com")
	request.Header.Set("Referer", "https://console.example.com/login")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Connection", "X-Hop-Only")
	request.Header.Set("X-Hop-Only", "must-not-arrive")
	request.Header.Set("X-Forwarded-For", "192.0.2.250")
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	request.Trailer = http.Header{"X-Upload-Trailer": {"request-complete"}}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `\"username\":\"alice\"`)
	echo := recorder.Header().Get("X-Master-Echo")
	assert.Contains(t, echo, "POST /api/%75ser/login?next=%2Fconsole&x=1&semi=a;b&bad=%zz")
	assert.Contains(t, echo, "auth=Bearer session-token")
	assert.Contains(t, echo, "cookie=session=abc")
	assert.Equal(t, []string{"a=1; Path=/; HttpOnly; Secure", "b=2; Path=/api/user/auth; SameSite=Strict"}, recorder.Header().Values("Set-Cookie"))
	received := <-captured
	assert.Equal(t, "console.example.com", received.Host)
	assert.Equal(t, "https://console.example.com", received.Header.Get("Origin"))
	assert.Equal(t, "https://console.example.com/login", received.Header.Get("Referer"))
	assert.Equal(t, "same-origin", received.Header.Get("Sec-Fetch-Site"))
	assert.Equal(t, "identity", received.Header.Get("Content-Encoding"))
	assert.Empty(t, received.Header.Get("X-Hop-Only"))
	assert.Empty(t, received.Header.Get("X-Forwarded-For"))
	assert.Equal(t, "request-complete", received.Trailer.Get("X-Upload-Trailer"))
	assert.Equal(t, "response-complete", recorder.Result().Trailer.Get("X-Master-Trailer"))
}

func TestConsoleForwarderFlushesStreamingResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	continueStream := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(continueStream) })
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-continueStream:
			_, _ = w.Write([]byte("data: second\n\n"))
		case <-r.Context().Done():
		}
	}))
	defer master.Close()
	config := newConsoleForwardTestConfig(t, master.URL)
	config.IngressTrustedPeers = []*net.IPNet{mustConsoleCIDR(t, "127.0.0.0/8")}
	useConsoleForwardSettings(t, config)

	engine := gin.New()
	engine.Use(PanicRecover(), ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "local") })
	worker := httptest.NewServer(engine)
	defer worker.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, worker.URL+"/api/channel/ollama/pull/stream", nil)
	require.NoError(t, err)
	request.Host = "console.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := worker.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	first := make([]byte, len("data: first\n\n"))
	_, err = io.ReadFull(response.Body, first)
	require.NoError(t, err, "first event must arrive while the master is blocked before producing the second")
	assert.Equal(t, "data: first\n\n", string(first))
	release.Do(func() { close(continueStream) })
	rest, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "data: second\n\n", string(rest))
}

func TestConsoleForwarderFallsBackLocallyOnFreshDialFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddress := listener.Addr().String()
	require.NoError(t, listener.Close())
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, "http://"+closedAddress))

	var localCalls atomic.Int32
	var localBody atomic.Value
	var bodyUntouched atomic.Bool
	trackedBody := &consoleTrackedBody{reader: strings.NewReader(`{"username":"alice"}`)}
	engine := gin.New()
	engine.Use(RequestId(), ConsoleForwarder())
	engine.POST("/api/user/login", func(c *gin.Context) {
		localCalls.Add(1)
		bodyUntouched.Store(c.Request.Body == trackedBody && trackedBody.reads.Load() == 0 && !trackedBody.closed.Load())
		body, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			localBody.Store("read error: " + readErr.Error())
			c.String(http.StatusInternalServerError, "read error")
			return
		}
		localBody.Store(string(body))
		c.String(http.StatusOK, "local fallback")
	})

	request := consolePublicRequest(t, http.MethodPost, "/api/user/login", `{"username":"alice"}`)
	request.Body = trackedBody
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, bodyUntouched.Load(), "fallback must receive the original body without an earlier read or close")
	assert.Equal(t, "local fallback", recorder.Body.String())
	assert.Equal(t, int32(1), localCalls.Load(), "a pre-send dial failure must run the local chain exactly once")
	assert.Equal(t, `{"username":"alice"}`, localBody.Load())
}

func TestConsoleForwarderReturns503ForMasterLocalJobs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddress := listener.Addr().String()
	require.NoError(t, listener.Close())
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, "http://"+closedAddress))

	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) {
		localCalls.Add(1)
		c.String(http.StatusOK, "local")
	})

	testCases := []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/api/channel/42/fingerprint/job-1", status: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/api/channel/42/fingerprint/job-1/cancel", status: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/api/channel/ollama/pull/stream", status: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/api/channel/42/fingerprint", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/channel/42/plugin_login", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/channel/42/plugin_login/continue", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/channel/plugin_login/callback", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/channel/ollama/pull", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/channel/42/fingerprint/job-1", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/channel/42/fingerprint/job-1/cancel", status: http.StatusOK},
	}
	for _, testCase := range testCases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			localCalls.Store(0)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, consolePublicRequest(t, testCase.method, testCase.path, ""))
			assert.Equal(t, testCase.status, recorder.Code)
			if testCase.status == http.StatusServiceUnavailable {
				assert.Zero(t, localCalls.Load(), "a master-process-local job must not be answered by the worker")
				return
			}
			assert.Equal(t, int32(1), localCalls.Load(), "shared state and new jobs may use pre-send local fallback")
		})
	}
}

func TestConsoleForwarderNeverReplaysAfterSend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")

	for _, testCase := range []struct {
		name   string
		method string
		body   string
	}{
		{name: "empty POST", method: http.MethodPost},
		{name: "empty GET", method: http.MethodGet},
		{name: "empty DELETE", method: http.MethodDelete},
		{name: "POST body", method: http.MethodPost, body: `{"username":"alice"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var executions atomic.Int32
			var localCalls atomic.Int32
			var peerMu sync.Mutex
			var warmPeer, failingPeer string
			master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.URL.Path == "/api/warm" {
					peerMu.Lock()
					warmPeer = r.RemoteAddr
					peerMu.Unlock()
					_, _ = w.Write([]byte("warm"))
					return
				}
				executions.Add(1)
				peerMu.Lock()
				failingPeer = r.RemoteAddr
				peerMu.Unlock()
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			defer master.Close()
			useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
			engine := gin.New()
			engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
			engine.NoRoute(func(c *gin.Context) {
				localCalls.Add(1)
				c.String(http.StatusOK, "local")
			})

			warm := httptest.NewRecorder()
			engine.ServeHTTP(warm, consolePublicRequest(t, http.MethodGet, "/api/warm", ""))
			require.Equal(t, http.StatusOK, warm.Code)
			require.Equal(t, "warm", warm.Body.String())

			var reused atomic.Bool
			request := consolePublicRequest(t, testCase.method, "/api/user/login", testCase.body)
			request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
				GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) },
			}))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			require.True(t, reused.Load(), "failure must occur on an actually reused master connection")
			peerMu.Lock()
			assert.Equal(t, warmPeer, failingPeer, "warm-up and failed request must use the same TCP connection")
			peerMu.Unlock()
			assert.Equal(t, http.StatusBadGateway, recorder.Code)
			assert.Equal(t, int32(1), executions.Load(), "the master must execute the failed request exactly once")
			assert.Zero(t, localCalls.Load(), "post-send failures cannot fall back locally")
		})
	}
}

func TestConsoleForwarderPropagatesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	config := newConsoleForwardTestConfig(t, "")
	verifier := &consoleForwardVerifier{config: config, nonces: make(map[string]time.Time)}
	var received atomic.Value
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := verifier.verify(r)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 || r.ContentLength != -1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received.Store(identity.RequestID)
		w.Header().Set(common.RequestIdKey, identity.RequestID)
		w.Header().Set(consoleForwardProcessingNodeHeader, "us01")
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()
	parsed, err := url.Parse(master.URL)
	require.NoError(t, err)
	config.MasterURL = parsed
	useConsoleForwardSettings(t, config)

	engine := gin.New()
	// Production mounts the forwarder immediately after RequestId, which is the
	// only middleware allowed to install the request id on the gin context.
	engine.Use(RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "local") })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodGet, "/api/models", ""))

	require.Equal(t, http.StatusOK, recorder.Code)
	forwarded := received.Load()
	require.NotNil(t, forwarded)
	assert.NotEmpty(t, forwarded, "the worker request id must reach the processing master")
	assert.Equal(t, recorder.Header().Get(common.RequestIdKey), forwarded, "the id the master processes must be the id returned to the browser")
	assert.Empty(t, recorder.Header().Get(consoleForwardProcessingNodeHeader), "internal processing provenance must not reach the browser")
}

type consoleTrackedBody struct {
	reader io.Reader
	reads  atomic.Int32
	closed atomic.Bool
}

func (b *consoleTrackedBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	if b.closed.Load() {
		return 0, errors.New("body was closed before local fallback")
	}
	return b.reader.Read(p)
}

func (b *consoleTrackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestConsoleForwarderTruncatedStreamDoesNotBecomeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	var executions atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executions.Add(1)
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 100\r\n\r\ndata: first\n\n")
		_ = buffered.Flush()
	}))
	defer master.Close()
	config := newConsoleForwardTestConfig(t, master.URL)
	config.IngressTrustedPeers = []*net.IPNet{mustConsoleCIDR(t, "127.0.0.0/8")}
	useConsoleForwardSettings(t, config)
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(PanicRecover(), ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) {
		localCalls.Add(1)
		c.String(http.StatusOK, "local")
	})
	worker := httptest.NewServer(engine)
	defer worker.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, worker.URL+"/api/log/export", nil)
	require.NoError(t, err)
	request.Host = "console.example.com"
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err := worker.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	assert.Error(t, err, "the real client must see an incomplete response, not a clean EOF")
	assert.Equal(t, http.StatusOK, response.StatusCode, "already-sent headers cannot be replaced")
	assert.NotContains(t, string(body), "Panic detected")
	assert.NotContains(t, string(body), `"error"`)
	assert.Equal(t, int32(1), executions.Load())
	assert.Zero(t, localCalls.Load())

	t.Run("writer without Hijacker does not append recovery JSON", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request := consolePublicRequest(t, http.MethodGet, "/api/log/export", "")
		request = request.WithContext(context.WithValue(ctx, http.ServerContextKey, &http.Server{}))
		recorder := &consoleFlushRecorder{}
		require.NotPanics(t, func() { engine.ServeHTTP(recorder, request) })
		assert.Equal(t, http.StatusOK, recorder.status)
		assert.NotContains(t, recorder.body.String(), "Panic detected")
		assert.NotContains(t, recorder.body.String(), `"error"`)
		assert.Zero(t, localCalls.Load())
	})
}

type consolePanicWriter struct {
	consoleFlushRecorder
	panicValue error
}

func (w *consolePanicWriter) Write(_ []byte) (int, error) {
	panic(w.panicValue)
}

func TestConsoleForwarderRethrowsNonAbortPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("response"))
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "local") })
	failure := errors.New("non-abort writer panic")
	writer := &consolePanicWriter{panicValue: failure}
	require.PanicsWithValue(t, failure, func() {
		engine.ServeHTTP(writer, consolePublicRequest(t, http.MethodGet, "/api/models", ""))
	}, "only http.ErrAbortHandler is handled by the forwarder; other panics belong to outer recovery")
}

func TestConsoleForwarderHeaderTimeoutNeverFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	var executions atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		executions.Add(1)
		<-r.Context().Done()
	}))
	defer master.Close()
	config := newConsoleForwardTestConfig(t, master.URL)
	config.HeaderTimeoutSecond = 1
	useConsoleForwardSettings(t, config)
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodPost, "/api/user/login", "payload").WithContext(ctx))
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Equal(t, int32(1), executions.Load())
	assert.Zero(t, localCalls.Load(), "response-header timeout is post-send and may not replay locally")
}

func TestConsoleForwarderPropagatesCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := consolePublicRequest(t, http.MethodPost, "/api/models", "payload").WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("master never received the forwarded request")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not reach the master")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwarder did not stop after cancellation")
	}
	assert.Zero(t, localCalls.Load(), "a cancelled forwarded request must never restart locally")
}

func TestConsoleForwarderStreamsMultipartWithoutPrereading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	masterEntered := make(chan struct{})
	var readBytes atomic.Int64
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(masterEntered)
		reader, err := r.MultipartReader()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n, err := io.Copy(io.Discard, part)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		readBytes.Store(n)
		w.WriteHeader(http.StatusCreated)
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "local") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	multipartWriter := multipart.NewWriter(writer)
	writeDone := make(chan error, 1)
	go func() {
		select {
		case <-masterEntered:
		case <-ctx.Done():
			_ = writer.CloseWithError(ctx.Err())
			writeDone <- ctx.Err()
			return
		}
		part, err := multipartWriter.CreateFormFile("file", "upload.bin")
		if err == nil {
			_, err = io.Copy(part, strings.NewReader(strings.Repeat("x", 2*1024*1024)))
		}
		if err == nil {
			err = multipartWriter.Close()
		}
		_ = writer.CloseWithError(err)
		writeDone <- err
	}()
	request := consolePublicRequest(t, http.MethodPost, "/api/channel/upload", "").WithContext(ctx)
	request.Body = reader
	request.ContentLength = -1
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	_ = reader.Close()
	select {
	case writeErr := <-writeDone:
		require.NoError(t, writeErr, "master must receive headers before the producer starts the large multipart body")
	case <-ctx.Done():
		t.Fatal("multipart producer did not stop after the proxy request completed")
	}
	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, int64(2*1024*1024), readBytes.Load())
}

func TestConsoleForwarderPreservesMasterBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	previousLimit := constant.AnonymousRequestBodyLimitKB
	constant.AnonymousRequestBodyLimitKB = 1
	t.Cleanup(func() { constant.AnonymousRequestBodyLimitKB = previousLimit })
	var masterHandlerCalls atomic.Int32
	masterEngine := gin.New()
	masterEngine.Use(AnonymousRequestBodyLimit())
	masterEngine.POST("/api/user/login", func(c *gin.Context) {
		masterHandlerCalls.Add(1)
		c.Status(http.StatusOK)
	})
	master := httptest.NewServer(masterEngine)
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodPost, "/api/user/login", strings.Repeat("x", 1025)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Zero(t, masterHandlerCalls.Load())
	assert.Zero(t, localCalls.Load(), "master's 413 must never trigger a local retry")
}

func TestConsoleForwarderTrustsSchemeOnlyFromConfiguredIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	var masterCalls atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		masterCalls.Add(1)
		w.Header().Set("X-Signed-Scheme", r.Header.Get(consoleForwardSchemeHeader))
		w.Header().Set("X-Signed-Client", r.Header.Get(consoleForwardClientIPHeader))
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	var localCalls atomic.Int32
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies([]string{"192.0.2.0/24"}))
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	for _, testCase := range []struct {
		name      string
		peer      string
		proto     []string
		forwarded bool
	}{
		{name: "configured ingress", peer: "192.0.2.10:3000", proto: []string{"https"}, forwarded: true},
		{name: "public spoof", peer: "198.51.100.10:3000", proto: []string{"https"}},
		{name: "ambiguous values", peer: "192.0.2.10:3000", proto: []string{"https", "http"}},
		{name: "comma separated values", peer: "192.0.2.10:3000", proto: []string{"https,http"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			masterCalls.Store(0)
			localCalls.Store(0)
			request := consolePublicRequest(t, http.MethodGet, "/api/models", "")
			request.TLS = nil
			request.RemoteAddr = testCase.peer
			request.Header["X-Forwarded-Proto"] = testCase.proto
			request.Header.Set("X-Forwarded-For", "203.0.113.50")
			request.Header.Set("CF-Connecting-IP", "203.0.113.99")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if testCase.forwarded {
				assert.Equal(t, int32(1), masterCalls.Load())
				assert.Zero(t, localCalls.Load())
				assert.Equal(t, "https", recorder.Header().Get("X-Signed-Scheme"))
				assert.Equal(t, "203.0.113.50", recorder.Header().Get("X-Signed-Client"), "normal Gin XFF trust wins; CF aliases do not")
				return
			}
			assert.Zero(t, masterCalls.Load(), "untrusted scheme must not manufacture an allowlisted HTTPS origin")
			assert.Equal(t, int32(1), localCalls.Load())
		})
	}
}

type consoleLogCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *consoleLogCapture) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

func (w *consoleLogCapture) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func TestConsoleForwarderLogsProvenanceWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	output := &consoleLogCapture{}
	common.LogWriterMu.Lock()
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = output, output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.LogWriterMu.Unlock()
	})
	envelopes := make(chan http.Header, 2)
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		envelopes <- r.Header.Clone()
		if r.URL.Path == "/api/models" {
			w.Header().Set(consoleForwardProcessingNodeHeader, "us01")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "local") })
	for _, path := range []string{"/api/models?code=oauth-secret-marker", "/api/user/self?state=state-secret-marker"} {
		request := consolePublicRequest(t, http.MethodGet, path, "body-secret-marker")
		request.Header.Set("Authorization", "Bearer authorization-secret-marker")
		request.Header.Set("Cookie", "session=cookie-secret-marker")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Empty(t, recorder.Header().Get(consoleForwardProcessingNodeHeader))
	}
	logs := output.String()
	assert.Contains(t, logs, `entry_node="hk01" processing_node="us01" forwarded=true`)
	assert.Contains(t, logs, `entry_node="hk01" processing_node="unknown" forwarded=true`)
	for _, secret := range []string{consoleForwardTestSecret, "oauth-secret-marker", "state-secret-marker", "authorization-secret-marker", "cookie-secret-marker", "body-secret-marker"} {
		assert.NotContains(t, logs, secret)
	}
	for range 2 {
		headers := <-envelopes
		assert.NotContains(t, logs, headers.Get(consoleForwardProofHeader))
		assert.NotContains(t, logs, headers.Get(consoleForwardNonceHeader))
	}
}

func TestConsoleForwarderSignsAfterHopHeaderCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	config := newConsoleForwardTestConfig(t, "")
	verifier := &consoleForwardVerifier{config: config, nonces: make(map[string]time.Time)}
	var verifiedCalls atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := verifier.verify(r); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		verifiedCalls.Add(1)
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer master.Close()
	parsed, err := url.Parse(master.URL)
	require.NoError(t, err)
	config.MasterURL = parsed
	useConsoleForwardSettings(t, config)
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	request := consolePublicRequest(t, http.MethodPost, "/api/user/self", "payload")
	request.Header.Set("Authorization", "Bearer removed-hop-credential")
	request.Header.Set("Connection", "Authorization")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, int32(1), verifiedCalls.Load(), "proof must cover the headers the master actually receives")
	assert.Equal(t, http.StatusUnauthorized, recorder.Code, "verified provenance is not a substitute for master authentication")
	assert.Zero(t, localCalls.Load())
}

func TestConsoleForwarderMasterNeverForwards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, true, "us01")
	var masterCalls atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { masterCalls.Add(1) }))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "local master") })
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodGet, "/api/models", ""))
	assert.Equal(t, "local master", recorder.Body.String())
	assert.Zero(t, masterCalls.Load(), "master role must not forward even if a target was supplied programmatically")
}

func TestConsoleForwarderPreservesMasterFailureResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	var executions atomic.Int32
	master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		executions.Add(1)
		status, _ := strconv.Atoi(r.URL.Query().Get("status"))
		w.Header().Set("Location", "https://console.example.com/login")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("master response"))
	}))
	defer master.Close()
	useConsoleForwardSettings(t, newConsoleForwardTestConfig(t, master.URL))
	var localCalls atomic.Int32
	engine := gin.New()
	engine.Use(ConsoleIngress(), RequestId(), ConsoleForwarder())
	engine.NoRoute(func(c *gin.Context) { localCalls.Add(1) })
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			executions.Store(0)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodPost, "/api/user/login?status="+strconv.Itoa(status), "body"))
			assert.Equal(t, status, recorder.Code)
			assert.Equal(t, "master response", recorder.Body.String())
			assert.Equal(t, "https://console.example.com/login", recorder.Header().Get("Location"))
			assert.Equal(t, int32(1), executions.Load())
			assert.Zero(t, localCalls.Load(), "HTTP failures and redirects are not pre-send dial failures")
		})
	}
}

func TestConsoleForwarderSkippedWithoutSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useConsoleNodeRole(t, false, "hk01")
	useConsoleForwardSettings(t, nil)

	engine := gin.New()
	engine.Use(RequestId(), ConsoleForwarder())
	engine.GET("/api/models", func(c *gin.Context) { c.String(http.StatusOK, "local") })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, consolePublicRequest(t, http.MethodGet, "/api/models", ""))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "local", recorder.Body.String(), "unset configuration must preserve today's behavior")
}
