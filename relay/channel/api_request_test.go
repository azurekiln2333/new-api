package channel

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextRequestAdaptor struct{ Adaptor }

func (contextRequestAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return "http://upstream.example/relay", nil
}

func (contextRequestAdaptor) SetupRequestHeader(*gin.Context, *http.Header, *relaycommon.RelayInfo) error {
	return nil
}

type contextRequestTransport func(*http.Request) (*http.Response, error)

func (f contextRequestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAPIAndFormRequestsInheritClientCancellation(t *testing.T) {
	for name, request := range map[string]func(Adaptor, *gin.Context, *relaycommon.RelayInfo, io.Reader) (*http.Response, error){
		"json": DoApiRequest,
		"form": DoFormRequest,
	} {
		t.Run(name, func(t *testing.T) {
			zero := 0
			settings := dto.ChannelSettings{UpstreamFirstByteTimeoutSeconds: &zero}
			client, err := service.GetRelayHttpClientWithProxySettings("", settings)
			require.NoError(t, err)
			previousTransport := client.Transport
			t.Cleanup(func() { client.Transport = previousTransport })
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			client.Transport = contextRequestTransport(func(req *http.Request) (*http.Response, error) {
				cancel()
				assert.ErrorIs(t, req.Context().Err(), context.Canceled)
				return nil, context.Canceled
			})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}")).WithContext(parent)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: settings}}
			_, err = request(contextRequestAdaptor{}, c, info, strings.NewReader("{}"))
			require.Error(t, err)
		})
	}
}

func TestFirstByteTimeoutDoesNotCommitEarlySSEKeepAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientConn, upstreamConn := net.Pipe()
		t.Cleanup(func() { _ = clientConn.Close(); _ = upstreamConn.Close() })
		upstreamClosed := make(chan error, 1)
		go func() {
			defer upstreamConn.Close()
			req, err := http.ReadRequest(bufio.NewReader(upstreamConn))
			if err == nil {
				_, err = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
			}
			if err == nil {
				_, err = io.WriteString(upstreamConn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n")
			}
			if err == nil {
				var b [1]byte
				_, err = upstreamConn.Read(b[:])
			}
			upstreamClosed <- err
		}()
		previousTransport := http.DefaultTransport
		http.DefaultTransport = &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }}
		service.ResetProxyClientCache()
		general := operation_setting.GetGeneralSetting()
		previousGeneral := *general
		general.PingIntervalEnabled, general.PingIntervalSeconds = true, 1
		t.Cleanup(func() {
			*general = previousGeneral
			http.DefaultTransport = previousTransport
			service.ResetProxyClientCache()
		})
		timeout := 3
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
			HTTPProtocol: dto.HTTPProtocolHTTP1, UpstreamFirstByteTimeoutSeconds: &timeout,
		}}}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("{}"))
		resp, err := DoApiRequest(contextRequestAdaptor{}, c, info, strings.NewReader("{}"))
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.True(t, service.IsUpstreamFirstByteTimeout(err), "safe masking must preserve the typed cause")
		var gatewayErr *types.NewAPIError
		require.ErrorAs(t, err, &gatewayErr)
		assert.Equal(t, http.StatusGatewayTimeout, gatewayErr.StatusCode)
		assert.False(t, types.IsSkipRetryError(gatewayErr))
		assert.False(t, c.Writer.Written(), "a keepalive must not prohibit channel failover")
		assert.Empty(t, recorder.Body.String())
		assert.NoError(t, c.Request.Context().Err())
		assert.ErrorIs(t, <-upstreamClosed, io.EOF, "upstream is closed before another attempt can run")
	})
}

func TestNewTaskAPIRequestInheritsClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	upstream, err := newTaskAPIRequest(c, "https://provider.example/tasks", nil)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, upstream.Context().Err(), context.Canceled)
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

func TestProcessHeaderOverride_OverridesUserAgentOnUpstreamRequest(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"User-Agent": "codex_cli_rs/0.164.0 (Windows 10.0.19045; x86_64)",
				"originator": "codex_cli_rs",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	// Simulate the adaptor's default before the override is applied.
	upstreamReq.Header.Set("User-Agent", "Go-http-client/1.1")
	applyHeaderOverrideToRequest(upstreamReq, headers)

	require.Equal(t, "codex_cli_rs/0.164.0 (Windows 10.0.19045; x86_64)", upstreamReq.Header.Get("User-Agent"))
	require.Equal(t, "codex_cli_rs", upstreamReq.Header.Get("Originator"))
}

func TestToWebSocketURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://api.openai.com/v1/responses":             "wss://api.openai.com/v1/responses",
		"http://127.0.0.1:3000/v1/responses":              "ws://127.0.0.1:3000/v1/responses",
		"wss://chatgpt.com/backend-api/codex/responses":   "wss://chatgpt.com/backend-api/codex/responses",
		"ws://127.0.0.1:3000/backend-api/codex/responses": "ws://127.0.0.1:3000/backend-api/codex/responses",
	} {
		assert.Equal(t, want, toWebSocketURL(input), input)
	}
}
