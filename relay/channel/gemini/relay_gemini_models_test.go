package gemini

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type geminiModelsTestTransport func(*http.Request) (*http.Response, error)

func (f geminiModelsTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type geminiModelsTestBody struct {
	read   func([]byte) (int, error)
	closed bool
}

func (b *geminiModelsTestBody) Read(p []byte) (int, error) { return b.read(p) }
func (b *geminiModelsTestBody) Close() error {
	b.closed = true
	return nil
}

func setupGeminiModelsTransport(t *testing.T, transport geminiModelsTestTransport) dto.ChannelSettings {
	t.Helper()
	settings := dto.ChannelSettings{Proxy: "http://gemini-model-tests.invalid:18080"}
	client, err := service.GetHttpClientWithProxySettings(settings.Proxy, settings)
	require.NoError(t, err)
	previous := client.Transport
	client.Transport = transport
	t.Cleanup(func() {
		client.Transport = previous
		service.InvalidateProxyClient(settings.Proxy)
	})
	return settings
}

func TestFetchGeminiModelsPaginationAndStaticHeaders(t *testing.T) {
	type requestContextKey struct{}
	ctx := context.WithValue(t.Context(), requestContextKey{}, "discovery")
	const token = "+/=&?# %"
	headers := http.Header{
		"X-Goog-Api-Key": {"static-key"},
		"Host":           {"gemini.internal"},
		"X-Static":       {"first", "second"},
	}
	originalHeaders := headers.Clone()
	var bodies []*geminiModelsTestBody
	var deadline time.Time
	settings := setupGeminiModelsTransport(t, func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, req.Method)
		assert.Equal(t, "/v1beta/models", req.URL.Path)
		assert.Equal(t, "static-key", req.Header.Get("x-goog-api-key"))
		assert.Equal(t, "gemini.internal", req.Host)
		assert.Equal(t, []string{"first", "second"}, req.Header.Values("X-Static"))
		assert.Equal(t, "discovery", req.Context().Value(requestContextKey{}))
		currentDeadline, bounded := req.Context().Deadline()
		assert.True(t, bounded)
		payload := `{"models":[{"name":"models/gemini-first"},{"name":"unprefixed"}],"nextPageToken":"+/=&?# %"}`
		if len(bodies) == 0 {
			deadline = currentDeadline
			assert.Empty(t, req.URL.RawQuery)
		} else {
			assert.True(t, bodies[len(bodies)-1].closed, "close each page before fetching the next")
			assert.Equal(t, deadline, currentDeadline, "pagination shares one total deadline")
			assert.Equal(t, token, req.URL.Query().Get("pageToken"))
			assert.Len(t, req.URL.Query(), 1, "page tokens must not inject query arguments")
			payload = `{"models":[]}`
		}
		body := &geminiModelsTestBody{read: strings.NewReader(payload).Read}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})

	models, err := FetchGeminiModels(ctx, "https://gemini.example/", "default-key", settings, headers)
	require.NoError(t, err)
	assert.Equal(t, []string{"gemini-first", "unprefixed"}, models)
	require.Len(t, bodies, 2)
	assert.True(t, bodies[1].closed)
	assert.Equal(t, originalHeaders, headers, "static overrides remain reusable across keys")
}

func TestFetchGeminiModelsRejectsInvalidInventoryWithoutPartialResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		status  int
	}{
		{name: "http_error", payload: `{"error":{"message":"denied"}}`, status: http.StatusForbidden},
		{name: "error_envelope", payload: `{"models":[],"error":{"message":"denied"}}`},
		{name: "malformed_json", payload: `{"models":[`},
		{name: "missing_inventory", payload: `{}`},
		{name: "null_response", payload: `null`},
		{name: "null_inventory", payload: `{"models":null}`},
		{name: "wrong_inventory_type", payload: `{"models":{}}`},
		{name: "non_string_name", payload: `{"models":[{"name":42}]}`},
		{name: "missing_name", payload: `{"models":[{}]}`},
		{name: "empty_name", payload: `{"models":[{"name":"models/"}]}`},
		{name: "wrong_token_type", payload: `{"models":[],"nextPageToken":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []*geminiModelsTestBody
			settings := setupGeminiModelsTransport(t, func(req *http.Request) (*http.Response, error) {
				payload, status := tc.payload, tc.status
				if len(bodies) == 0 {
					payload = `{"models":[{"name":"models/first-page"}],"nextPageToken":"next"}`
					status = http.StatusOK
				}
				if status == 0 {
					status = http.StatusOK
				}
				body := &geminiModelsTestBody{read: strings.NewReader(payload).Read}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Body: body}, nil
			})

			models, err := FetchGeminiModels(t.Context(), "https://gemini.example", "key", settings, nil)
			require.Error(t, err)
			assert.Nil(t, models, "an incomplete inventory must not suggest model deletions")
			require.Len(t, bodies, 2)
			for _, body := range bodies {
				assert.True(t, body.closed)
			}
		})
	}
}

func TestFetchGeminiModelsRejectsIncompletePagination(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls int
	}{
		{name: "repeated_token", calls: 2},
		{name: "page_limit", calls: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []*geminiModelsTestBody
			settings := setupGeminiModelsTransport(t, func(req *http.Request) (*http.Response, error) {
				token := "repeated"
				if tc.name == "page_limit" {
					token = fmt.Sprint(len(bodies) + 1)
				}
				payload := fmt.Sprintf(`{"models":[{"name":"models/partial"}],"nextPageToken":%q}`, token)
				body := &geminiModelsTestBody{read: strings.NewReader(payload).Read}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})

			models, err := FetchGeminiModels(t.Context(), "https://gemini.example", "key", settings, nil)
			require.Error(t, err)
			assert.Nil(t, models)
			require.Len(t, bodies, tc.calls)
			for _, body := range bodies {
				assert.True(t, body.closed)
			}
		})
	}
}

func TestFetchGeminiModelsCancellationAndBodyFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var body *geminiModelsTestBody
			settings := setupGeminiModelsTransport(t, func(req *http.Request) (*http.Response, error) {
				body = &geminiModelsTestBody{read: func([]byte) (int, error) {
					if canceled {
						cancel()
						<-req.Context().Done()
						return 0, req.Context().Err()
					}
					return 0, io.ErrUnexpectedEOF
				}}
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})

			models, err := FetchGeminiModels(ctx, "https://gemini.example", "key", settings, nil)
			if canceled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
			assert.Nil(t, models)
			require.NotNil(t, body)
			assert.True(t, body.closed)
		})
	}
}

func TestFetchGeminiModelsDeadlineSpansEveryPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var deadlines []time.Time
		var firstBody *geminiModelsTestBody
		settings := setupGeminiModelsTransport(t, func(req *http.Request) (*http.Response, error) {
			deadline, bounded := req.Context().Deadline()
			assert.True(t, bounded)
			deadlines = append(deadlines, deadline)
			if len(deadlines) == 1 {
				<-time.After(20 * time.Second)
				firstBody = &geminiModelsTestBody{read: strings.NewReader(`{"models":[],"nextPageToken":"next"}`).Read}
				return &http.Response{StatusCode: http.StatusOK, Body: firstBody}, nil
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})

		models, err := FetchGeminiModels(t.Context(), "https://gemini.example", "key", settings, nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Nil(t, models)
		require.Len(t, deadlines, 2)
		assert.Equal(t, deadlines[0], deadlines[1])
		require.NotNil(t, firstBody)
		assert.True(t, firstBody.closed)
	})
}

func TestFetchGeminiModelsTransportPolicy(t *testing.T) {
	t.Run("proxy", func(t *testing.T) {
		requests := make(chan *http.Request, 1)
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			requests <- req.Clone(req.Context())
			_, _ = io.WriteString(w, `{"models":[{"name":"models/proxied"}]}`)
		}))
		defer proxy.Close()
		defer service.InvalidateProxyClient(proxy.URL)

		models, err := FetchGeminiModels(t.Context(), "http://gemini.invalid", "proxy-key", dto.ChannelSettings{Proxy: proxy.URL}, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"proxied"}, models)
		request := <-requests
		assert.Equal(t, "gemini.invalid", request.URL.Host)
		assert.Equal(t, "/v1beta/models", request.URL.Path)
		assert.Equal(t, "proxy-key", request.Header.Get("x-goog-api-key"))
	})

	t.Run("channel_tls", func(t *testing.T) {
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			_, _ = io.WriteString(w, `{"models":[{"name":"models/self-signed"}]}`)
		}))
		defer upstream.Close()

		models, err := FetchGeminiModels(t.Context(), upstream.URL, "key", dto.ChannelSettings{
			TLSInsecureSkipVerify: true,
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"self-signed"}, models)
	})
}
