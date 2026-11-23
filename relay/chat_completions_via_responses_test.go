package relay

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/model"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSupportedEndpointsChooseOutboundRequest(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	testutil.UseInactiveChannelBudget(t)
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	for _, tc := range []struct {
		name, inputPath, wantPath          string
		endpoints                          []string
		globalResponses, globalPassthrough bool
		channelPassthrough                 bool
		wantLocalError                     bool
	}{
		{name: "legacy Claude keeps chat", inputPath: "/v1/messages", wantPath: "/v1/chat/completions"},
		{name: "chat plus messages retains chat", endpoints: []string{"chat", "messages"}, wantPath: "/v1/chat/completions"},
		{name: "messages-only rejects chat passthrough", endpoints: []string{"messages"}, channelPassthrough: true, wantLocalError: true},
		{name: "legacy native chat", wantPath: "/v1/chat/completions"},
		{name: "legacy global conversion", globalResponses: true, wantPath: "/v1/responses"},
		{name: "empty declaration keeps global conversion", endpoints: []string{}, globalResponses: true, wantPath: "/v1/responses"},
		{name: "responses-only forces conversion", endpoints: []string{"responses"}, wantPath: "/v1/responses"},
		{name: "chat-only constrains global conversion", endpoints: []string{"chat"}, globalResponses: true, wantPath: "/v1/chat/completions"},
		{name: "both preserves native chat", endpoints: []string{"chat", "responses"}, wantPath: "/v1/chat/completions"},
		{name: "both honors supported global preference", endpoints: []string{"chat", "responses"}, globalResponses: true, wantPath: "/v1/responses"},
		{name: "messages to responses", inputPath: "/v1/messages", endpoints: []string{"responses"}, wantPath: "/v1/responses"},
		{name: "messages to chat", inputPath: "/v1/messages", endpoints: []string{"chat"}, globalResponses: true, wantPath: "/v1/chat/completions"},
		{name: "messages to declared messages", inputPath: "/v1/messages", endpoints: []string{"messages"}, wantPath: "/v1/messages"},
		{name: "messages prefers declared messages over responses", inputPath: "/v1/messages", endpoints: []string{"messages", "responses"}, wantPath: "/v1/messages"},
		{name: "messages-only converts a chat client", endpoints: []string{"messages"}, wantPath: "/v1/messages"},
		{name: "playground to responses", inputPath: "/pg/chat/completions", endpoints: []string{"responses"}, wantPath: "/v1/responses"},
		{name: "responses-only rejects global passthrough", endpoints: []string{"responses"}, globalPassthrough: true, wantLocalError: true},
		{name: "messages rejects global passthrough", inputPath: "/v1/messages", endpoints: []string{"chat", "responses"}, globalPassthrough: true, wantLocalError: true},
		{name: "native chat permits global passthrough", endpoints: []string{"chat", "responses"}, globalResponses: true, globalPassthrough: true, wantPath: "/v1/chat/completions"},
		{name: "legacy global passthrough", globalResponses: true, globalPassthrough: true, wantPath: "/v1/chat/completions"},
		{name: "legacy channel passthrough", globalResponses: true, channelPassthrough: true, wantPath: "/v1/chat/completions"},
		{name: "invalid stored passthrough declaration", endpoints: []string{"responses"}, channelPassthrough: true, wantLocalError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.PassThroughRequestEnabled = tc.globalPassthrough
			settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: tc.globalResponses, AllChannels: true, ModelPatterns: []string{".*"}}
			type capturedRequest struct {
				path    string
				body    []byte
				headers http.Header
			}
			captured := make(chan capturedRequest, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				captured <- capturedRequest{path: r.URL.Path, body: body, headers: r.Header.Clone()}
				// Stop before settlement; this test protects real outbound selection.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream reached","type":"test","code":"test"}}`))
			}))
			defer upstream.Close()
			path := tc.inputPath
			if path == "" {
				path = "/v1/chat/completions"
			}
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"temperature":0}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("Accept-Language", "en")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: tc.endpoints, PassThroughBodyEnabled: tc.channelPassthrough})
			common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"User-Agent": "configured-client/1.0", "X-Custom": "retained"})
			var apiErr *relaytypes.NewAPIError
			var info *relaycommon.RelayInfo
			if path == "/v1/messages" {
				var req dto.ClaudeRequest
				require.NoError(t, common.Unmarshal([]byte(body), &req))
				info = relaycommon.GenRelayInfoClaude(c, &req)
				apiErr = ClaudeHelper(c, info)
			} else {
				var req dto.GeneralOpenAIRequest
				require.NoError(t, common.Unmarshal([]byte(body), &req))
				info = relaycommon.GenRelayInfoOpenAI(c, &req)
				apiErr = TextHelper(c, info)
			}
			require.NotNil(t, apiErr)
			if tc.wantLocalError {
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.Empty(t, captured)
				return
			}
			require.Equal(t, http.StatusTeapot, apiErr.StatusCode, apiErr.Error())
			require.Len(t, captured, 1)
			request := <-captured
			assert.Equal(t, tc.wantPath, request.path)
			assert.Equal(t, "configured-client/1.0", request.headers.Get("User-Agent"))
			assert.Equal(t, "retained", request.headers.Get("X-Custom"))
			var sent map[string]any
			require.NoError(t, common.Unmarshal(request.body, &sent))
			assert.Equal(t, float64(0), sent["temperature"])
			if tc.wantPath == "/v1/responses" {
				assert.Contains(t, sent, "input")
				assert.NotContains(t, sent, "messages")
				assert.Equal(t, relaytypes.RelayFormat(relaytypes.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
			} else if tc.wantPath == "/v1/messages" {
				assert.Contains(t, sent, "messages")
				assert.NotContains(t, sent, "input")
				assert.NotEmpty(t, sent["max_tokens"], "a Messages upstream requires max_tokens")
				assert.Equal(t, relaytypes.RelayFormat(relaytypes.RelayFormatClaude), info.UpstreamTextEndpoint)
				assert.Equal(t, "2023-06-01", request.headers.Get("anthropic-version"))
				assert.Equal(t, "Bearer test-key", request.headers.Get("Authorization"))
			} else {
				assert.Contains(t, sent, "messages")
				assert.NotContains(t, sent, "input")
				assert.Empty(t, info.UpstreamTextEndpoint)
			}
		})
	}
}

func TestPlaygroundWebSearchPreservesFinalUpstreamIntent(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy.Enabled = false
	for _, tc := range []struct {
		name, endpoint, removed string
		passthrough             bool
	}{
		{name: "chat", endpoint: "chat"},
		{name: "responses", endpoint: "responses"},
		{name: "passthrough", endpoint: "chat", passthrough: true},
		{name: "removed chat search", endpoint: "chat", removed: "web_search_options"},
		{name: "removed converted search", endpoint: "responses", removed: "web_search_options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.UseInactiveChannelBudget(t)
			captured := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Search is unsupported by this upstream model","type":"invalid_request_error","code":"unsupported_search"}}`)
			}))
			defer server.Close()
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"web_search_options":{"search_context_size":"medium"}}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
			channelSettings := dto.ChannelSettings{SupportedEndpoints: []string{tc.endpoint}, PassThroughBodyEnabled: tc.passthrough}
			if tc.passthrough {
				channelSettings.SupportedEndpoints = nil
			}
			common.SetContextKey(c, constant.ContextKeyChannelSetting, channelSettings)
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(body), &request))
			info := relaycommon.GenRelayInfoOpenAI(c, &request)
			if tc.removed != "" {
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"operations": []any{map[string]any{"path": tc.removed, "mode": "delete"}}})
			}
			apiErr := TextHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			if tc.removed != "" {
				assert.Empty(t, captured, "lost search intent must fail before calling upstream")
				assert.Equal(t, relaytypes.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
				return
			}
			require.Len(t, captured, 1, apiErr.Error())
			assert.Contains(t, apiErr.Error(), "Search is unsupported")
			var sent map[string]any
			require.NoError(t, common.Unmarshal(<-captured, &sent))
			if tc.endpoint == "responses" {
				assert.Equal(t, []any{map[string]any{"type": "web_search", "search_context_size": "medium"}}, sent["tools"])
				assert.Equal(t, dto.BuildInToolWebSearch, info.WebSearchBillingKey)
			} else {
				assert.Equal(t, map[string]any{"search_context_size": "medium"}, sent["web_search_options"])
			}
		})
	}
}

func TestPlaygroundWebSearchAcceptsProviderSearchRepresentations(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsPlayground: true, Request: &dto.GeneralOpenAIRequest{WebSearchOptions: &dto.WebSearchOptions{SearchContextSize: "medium"}}}
	for _, body := range []string{
		`{"web_search_options":{}}`, `{"enable_search":true}`, `{"web_search":{"enable":true}}`,
		`{"search_parameters":{"mode":"auto"}}`, `{"plugins":[{"id":"web"}]}`,
		`{"tools":[{"type":"web_search"}]}`, `{"tools":[{"type":"web_search_preview"}]}`,
		`{"tools":[{"type":"web_search_20250305"}]}`, `{"tools":[{"type":"web_search","web_search":{"enable":true}}]}`,
		`{"tools":[{"googleSearch":{}}]}`, `{"tools":[{"google_search":{}}]}`, `{"tools":[{"googleSearchRetrieval":{}}]}`,
	} {
		assert.Nil(t, validatePlaygroundWebSearch(c, info, []byte(body)), body)
	}
	for _, body := range []string{`{}`, `{"tools":[{"type":"function","name":"web_search"}]}`, `{"tools":[{"type":"web_search","web_search":{"enable":false}}]}`, `{"search_parameters":{"mode":"off"}}`} {
		apiErr := validatePlaygroundWebSearch(c, info, []byte(body))
		require.NotNil(t, apiErr, body)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	}
	info.IsPlayground = false
	assert.Nil(t, validatePlaygroundWebSearch(c, info, []byte(`{}`)))
	info.IsPlayground = true
	info.Request = &dto.GeneralOpenAIRequest{}
	assert.Nil(t, validatePlaygroundWebSearch(c, info, []byte(`{}`)))
}

func TestChannelEndpointDeclarationsRejectUnsupportedAdaptors(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAzure, constant.ChannelTypeOpenAIMax, constant.ChannelTypeOpenRouter, constant.ChannelTypeXinference} {
		assert.NoError(t, common.ValidateChannelSupportedEndpoints(channelType, &dto.ChannelSettings{SupportedEndpoints: []string{"chat", "responses"}}))
	}
	for _, channelType := range []int{constant.ChannelTypeCustom, constant.ChannelTypeAdvancedCustom, constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini, constant.ChannelTypeTaskPlugin} {
		assert.NoError(t, common.ValidateChannelSupportedEndpoints(channelType, &dto.ChannelSettings{}))
		assert.ErrorContains(t, common.ValidateChannelSupportedEndpoints(channelType, &dto.ChannelSettings{SupportedEndpoints: []string{"responses"}}), "not supported for this channel type")
	}
}

func TestChannelEndpointDeclarationsConstrainNativeResponsesHTTPAndWebSocket(t *testing.T) {
	require.NoError(t, i18n.Init())
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.PassThroughRequestEnabled = false
	for _, transport := range []string{"http", "websocket", "compact"} {
		for _, tc := range []struct {
			name      string
			endpoints []string
			wantError bool
		}{
			{name: "legacy"},
			{name: "chat only", endpoints: []string{"chat"}, wantError: true},
			{name: "responses only", endpoints: []string{"responses"}},
			{name: "messages only", endpoints: []string{"messages"}, wantError: transport != "http"},
			{name: "responses and messages", endpoints: []string{"responses", "messages"}},
			{name: "both", endpoints: []string{"chat", "responses"}},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("Accept-Language", "en")
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: tc.endpoints})
				request := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Input: []byte(`"hello"`)}
				info := relaycommon.GenRelayInfoResponses(c, request)
				var apiErr *relaytypes.NewAPIError
				if transport == "websocket" {
					_, apiErr = buildResponsesWSCreatePayload(c, info, *request, nil, "")
				} else {
					if transport == "compact" {
						info.RelayMode = relayconstant.RelayModeResponsesCompact
						info.RelayFormat = relaytypes.RelayFormatOpenAIResponsesCompaction
					}
					_, body, closer, err := PrepareResponsesRequest(c, info, request)
					apiErr = err
					if closer != nil {
						defer closer.Close()
					}
					if !tc.wantError {
						require.Nil(t, apiErr)
						payload, err := io.ReadAll(body)
						require.NoError(t, err)
						if tc.name == "messages only" {
							assert.Contains(t, string(payload), `"messages"`)
						} else {
							assert.Contains(t, string(payload), `"input"`)
						}
					}
				}
				if tc.wantError {
					require.NotNil(t, apiErr)
					assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				} else {
					assert.Nil(t, apiErr)
				}
			})
		}
	}
}

func TestResponsesRefusalRetainsRetryAfter(t *testing.T) {
	service.InitHttpClient()
	testutil.UseInactiveChannelBudget(t)
	settings := model_setting.GetGlobalSettings()
	previousPassthrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = previousPassthrough })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user","type":"rate_limit_error"}}`)
	}))
	defer upstream.Close()
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-4o","stream":true}`))
			c.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: []string{"responses"}})
			var apiErr *relaytypes.NewAPIError
			if path == "/v1/responses" {
				request := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Input: []byte(`"hello"`), Stream: common.GetPointer(true)}
				info := relaycommon.GenRelayInfoResponses(c, request)
				info.DisablePing = true
				apiErr = ResponsesHelper(c, info)
			} else {
				request := &dto.GeneralOpenAIRequest{Model: "gpt-4o", Stream: common.GetPointer(true), Messages: []dto.Message{{Role: "user", Content: "hello"}}}
				info := relaycommon.GenRelayInfoOpenAI(c, request)
				info.DisablePing = true
				apiErr = TextHelper(c, info)
			}
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			assert.Equal(t, service.UpstreamRejectionConcurrency, service.ClassifyUpstreamRejection(apiErr))
			assert.Equal(t, "45", helper.UpstreamRetryAfter(c), "the queue must receive the refused attempt's Retry-After")
			assert.False(t, c.Writer.Written())
		})
	}
}

func TestChannelEndpointResponsesOnlyRejectsGeminiBeforeSending(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, endpoint := range []string{"responses", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusTeapot) }))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gpt-4o:generateContent", nil)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: []string{endpoint}})
			request := &dto.GeminiChatRequest{}
			info := relaycommon.GenRelayInfoGemini(c, request)
			apiErr := GeminiHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, relaytypes.IsSkipRetryError(apiErr))
			assert.Zero(t, calls.Load())
		})
	}
}

func TestChannelEndpointResponsesConversionPreservesClientFormat(t *testing.T) {
	service.InitHttpClient()
	testutil.UseInactiveChannelBudget(t)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.PassThroughRequestEnabled = false
	for _, client := range []string{"chat", "messages", "playground search"} {
		for _, stream := range []bool{false, true} {
			name := client + "/json"
			if stream {
				name = client + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				captured := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					captured <- body
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-4o\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","model":"gpt-4o","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
					}
				}))
				defer server.Close()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{
					RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: relaytypes.RelayFormatOpenAI,
					OriginModelName: "gpt-4o", IsStream: stream, DisablePing: true, ShouldIncludeUsage: true,
					ChannelMeta: &relaycommon.ChannelMeta{
						ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: server.URL, ApiKey: "test-key", UpstreamModelName: "gpt-4o",
						ChannelSetting: dto.ChannelSettings{SupportedEndpoints: []string{"responses"}},
					},
				}
				var request any = &dto.GeneralOpenAIRequest{Model: "gpt-4o", Stream: &stream, Messages: []dto.Message{{Role: "user", Content: "hello"}}}
				if client == "playground search" {
					c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", nil)
					info.IsPlayground = true
					chatRequest := request.(*dto.GeneralOpenAIRequest)
					chatRequest.WebSearchOptions = &dto.WebSearchOptions{SearchContextSize: "medium"}
					info.Request = chatRequest
				}
				if client == "messages" {
					info.RelayFormat = relaytypes.RelayFormatClaude
					request = &dto.ClaudeRequest{Model: "gpt-4o", Stream: &stream, Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}}
				}
				adaptor := &openaichannel.Adaptor{}
				adaptor.Init(info)
				target, apiErr := resolveTextUpstreamEndpoint(c, info)
				require.Nil(t, apiErr)
				require.Equal(t, relaytypes.RelayFormat(relaytypes.RelayFormatOpenAIResponses), target)
				usage, apiErr := textRequestViaResponses(c, info, adaptor, request)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 2, usage.PromptTokens)
				assert.Equal(t, 3, usage.CompletionTokens)
				assert.Equal(t, 5, usage.TotalTokens)
				assert.Equal(t, []relaytypes.RelayFormat{info.RelayFormat, relaytypes.RelayFormatOpenAIResponses}, info.RequestConversionChain)
				if client == "playground search" {
					var sent map[string]any
					require.NoError(t, common.Unmarshal(<-captured, &sent))
					assert.Equal(t, []any{map[string]any{"type": "web_search", "search_context_size": "medium"}}, sent["tools"])
				}
				if stream {
					if client == "messages" {
						assert.Contains(t, recorder.Body.String(), "event: message_stop")
						assert.Contains(t, recorder.Body.String(), `"text":"hello"`)
					} else {
						assert.Contains(t, recorder.Body.String(), "data: [DONE]")
						assert.Contains(t, recorder.Body.String(), `"content":"hello"`)
					}
				} else if client == "messages" {
					var response dto.ClaudeResponse
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					require.Len(t, response.Content, 1)
					assert.Equal(t, "hello", response.Content[0].GetText())
				} else {
					assert.Contains(t, recorder.Body.String(), `"object":"chat.completion"`)
					assert.Contains(t, recorder.Body.String(), `"content":"hello"`)
				}
			})
		}
	}
}

func TestPrepareResponsesRequestRetainsConvertedAdaptor(t *testing.T) {
	testutil.UseInactiveChannelBudget(t)
	type capturedRequest struct {
		path string
		body []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- capturedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat,
		}}},
	})
	request := &dto.OpenAIResponsesRequest{Model: "gpt-4o", Input: []byte(`"hello"`)}
	info := relaycommon.GenRelayInfoResponses(c, request)
	adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, request)
	require.Nil(t, apiErr)
	defer closer.Close()
	response, err := adaptor.DoRequest(c, info, body)
	require.NoError(t, err)
	httpResponse, ok := response.(*http.Response)
	require.True(t, ok)
	usage, apiErr := adaptor.DoResponse(c, httpResponse, info)
	require.Nil(t, apiErr)
	require.IsType(t, &dto.Usage{}, usage)
	assert.Equal(t, 5, usage.(*dto.Usage).TotalTokens)

	upstream := <-captured
	assert.Equal(t, "/v1/chat/completions", upstream.path)
	var upstreamRequest dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal(upstream.body, &upstreamRequest))
	require.Len(t, upstreamRequest.Messages, 1)
	assert.Equal(t, "hello", upstreamRequest.Messages[0].StringContent())
	assert.Equal(t, []relaytypes.RelayFormat{relaytypes.RelayFormatOpenAIResponses, relaytypes.RelayFormatOpenAI}, info.RequestConversionChain)
}

func TestIsResponsesEventStreamContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "plain", contentType: "text/event-stream", want: true},
		{name: "mixed case with charset", contentType: "Text/Event-Stream; charset=utf-8", want: true},
		{name: "json", contentType: "application/json", want: false},
		{name: "empty", contentType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isResponsesEventStreamContentType(tt.contentType))
		})
	}
}

func TestRecalcQuotaFromRatiosIgnoresInvalidMultipliers(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: hosttypes.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"duration": 3,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.True(t, ok)
	assert.Equal(t, 150, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}

func TestRecalcQuotaFromRatiosRejectsAllInvalidAdjustedRatios(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: hosttypes.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.False(t, ok)
	assert.Equal(t, 0, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}

func TestTextRequestViaResponsesConvertsClaudeDirectly(t *testing.T) {
	testutil.UseInactiveChannelBudget(t)
	type capturedRequest struct {
		path string
		body []byte
	}
	captured := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured <- capturedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_1",
			"object":"response",
			"status":"completed",
			"model":"gpt-5.6-sol",
			"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
			"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
		}`))
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		RelayMode:              relayconstant.RelayModeChatCompletions,
		RelayFormat:            relaytypes.RelayFormatClaude,
		OriginModelName:        "gpt-5.6-sol",
		RequestConversionChain: []relaytypes.RelayFormat{relaytypes.RelayFormatClaude},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    server.URL,
			ApiKey:            "test-key",
			UpstreamModelName: "gpt-5.6-sol",
		},
	}
	adaptor := &openaichannel.Adaptor{}
	adaptor.Init(info)
	request := &dto.ClaudeRequest{
		Model:    "gpt-5.6-sol",
		Thinking: &dto.Thinking{Type: "adaptive", Display: "summarized"},
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}

	usage, apiErr := textRequestViaResponses(c, info, adaptor, request)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, []relaytypes.RelayFormat{relaytypes.RelayFormatClaude, relaytypes.RelayFormatOpenAIResponses}, info.RequestConversionChain)

	upstream := <-captured
	assert.Equal(t, "/v1/responses", upstream.path)
	var upstreamBody map[string]any
	require.NoError(t, common.Unmarshal(upstream.body, &upstreamBody))
	assert.NotContains(t, upstreamBody, "messages")
	reasoning, ok := upstreamBody["reasoning"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "high", reasoning["effort"])
	assert.Equal(t, "detailed", reasoning["summary"])

	var response dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Content, 1)
	assert.Equal(t, "ok", response.Content[0].GetText())
}

func TestApplySystemPromptIfNeededSkipsToolLoadingMessages(t *testing.T) {
	tools := json.RawMessage(`[{"type":"function","function":{"name":"get_current_time","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`)
	toolLoading := dto.Message{Role: "system", Tools: tools}
	user := dto.Message{Role: "user", Content: "What time is it in Beijing?"}

	tests := []struct {
		name         string
		messages     []dto.Message
		wantMessages []dto.Message
		wantOverride bool
	}{
		{
			name:     "tool loading message alone is not a system prompt",
			messages: []dto.Message{toolLoading, user},
			wantMessages: []dto.Message{
				{Role: "system", Content: "Answer in English."},
				toolLoading,
				user,
			},
		},
		{
			name:     "override targets the real system prompt only",
			messages: []dto.Message{toolLoading, {Role: "system", Content: "You are Kimi."}, user},
			wantMessages: []dto.Message{
				toolLoading,
				{Role: "system", Content: "Answer in English.\nYou are Kimi."},
				user,
			},
			wantOverride: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelSetting: dto.ChannelSettings{
						SystemPrompt:         "Answer in English.",
						SystemPromptOverride: true,
					},
				},
			}
			request := &dto.GeneralOpenAIRequest{
				Model:    "kimi-k3",
				Messages: append([]dto.Message(nil), tt.messages...),
			}

			applySystemPromptIfNeeded(c, info, request)

			require.Len(t, request.Messages, len(tt.wantMessages))
			for i, want := range tt.wantMessages {
				got := request.Messages[i]
				assert.Equal(t, want.Role, got.Role, "message %d role", i)
				assert.Equal(t, want.Content, got.Content, "message %d content", i)
				if len(want.Tools) > 0 {
					assert.JSONEq(t, string(want.Tools), string(got.Tools), "message %d tools", i)
				} else {
					assert.Empty(t, got.Tools, "message %d tools", i)
				}
			}
			_, overrideSet := common.GetContextKey(c, constant.ContextKeySystemPromptOverride)
			assert.Equal(t, tt.wantOverride, overrideSet)
		})
	}
}

// TestMessagesEndpointServesOpenAIProtocols exercises successful conversion and
// real preconsume/settlement against one Anthropic upstream for each client format.
func TestMessagesEndpointServesOpenAIProtocols(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
	previousLog, previousExport := common.LogConsumeEnabled, common.DataExportEnabled
	previousTimeout := constant.StreamingTimeout
	settings := model_setting.GetGlobalSettings()
	previousSettings := *settings
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	common.LogConsumeEnabled, common.DataExportEnabled = true, false
	constant.StreamingTimeout = 30
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy.Enabled = false
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch
		common.LogConsumeEnabled, common.DataExportEnabled = previousLog, previousExport
		constant.StreamingTimeout = previousTimeout
		*settings = previousSettings
	})

	const usage = `{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}`
	const message = `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model",` +
		`"content":[{"type":"text","text":"hello from messages"},{"type":"tool_use","id":"tool_1","name":"weather","input":{"city":"Paris"}}],` +
		`"stop_reason":"tool_use","stop_sequence":null,"usage":` + usage + `}`
	const streamEvents = "event: message_start\ndata: " +
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":11,"output_tokens":0,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}}` + "\n\n" +
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello from messages"}}` + "\n\n" +
		"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"weather","input":{}}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}` + "\n\n" +
		"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":1}` + "\n\n" +
		"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":7}}` + "\n\n" +
		"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"

	for _, client := range []string{"chat", "claude", "responses"} {
		for _, stream := range []bool{false, true} {
			name := client + "/json"
			if stream {
				name = client + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				testutil.UseInactiveChannelBudget(t)
				db := model.DB
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}))
				const startingQuota = 1000000
				user := model.User{Username: "messages-user", Quota: startingQuota, Status: common.UserStatusEnabled}
				require.NoError(t, db.Create(&user).Error)
				token := model.Token{UserId: user.Id, Key: "client-token", RemainQuota: startingQuota, Status: common.TokenStatusEnabled}
				require.NoError(t, db.Create(&token).Error)
				channel := model.Channel{Name: "messages", Key: "channel-secret", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI}
				require.NoError(t, db.Create(&channel).Error)

				type capturedRequest struct {
					body   []byte
					header http.Header
					path   string
				}
				captured := make(chan capturedRequest, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					captured <- capturedRequest{body: body, header: r.Header.Clone(), path: r.URL.Path}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, streamEvents)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, message)
					}
				}))
				defer server.Close()
				path := "/v1/chat/completions"
				body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"temperature":0,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`
				switch client {
				case "claude":
					path = "/v1/messages"
					body = `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":64,"temperature":0,"tools":[{"name":"weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]}`
				case "responses":
					path = "/v1/responses"
					body = `{"model":"m","input":"hi","max_output_tokens":64,"temperature":0,"tools":[{"type":"function","name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}`
				}
				if stream {
					body = strings.TrimSuffix(body, "}") + `,"stream":true}`
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("Authorization", "Bearer client-token")
				c.Request.Header.Set("x-api-key", "client-anthropic-key")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "m")
				common.SetContextKey(c, constant.ContextKeyUserId, user.Id)
				common.SetContextKey(c, constant.ContextKeyTokenId, token.Id)
				common.SetContextKey(c, constant.ContextKeyTokenKey, token.Key)
				common.SetContextKey(c, constant.ContextKeyUserQuota, startingQuota)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
				common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
				common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
				common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: []string{"messages"}})
				c.Set("channel_organization", "channel-org")

				var info *relaycommon.RelayInfo
				var run func(*gin.Context, *relaycommon.RelayInfo) *relaytypes.NewAPIError
				switch client {
				case "claude":
					var req dto.ClaudeRequest
					require.NoError(t, common.Unmarshal([]byte(body), &req))
					info, run = relaycommon.GenRelayInfoClaude(c, &req), ClaudeHelper
				case "responses":
					var req dto.OpenAIResponsesRequest
					require.NoError(t, common.Unmarshal([]byte(body), &req))
					info, run = relaycommon.GenRelayInfoResponses(c, &req), ResponsesHelper
				default:
					var req dto.GeneralOpenAIRequest
					require.NoError(t, common.Unmarshal([]byte(body), &req))
					info, run = relaycommon.GenRelayInfoOpenAI(c, &req), TextHelper
				}
				clientFormat := info.RelayFormat
				info.InitChannelMeta(c)
				info.DisablePing, info.ForcePreConsume = true, true
				info.PriceData = hosttypes.PriceData{ModelRatio: 2, CompletionRatio: 3, CacheRatio: 0.2, CacheCreationRatio: 2, CacheCreation5mRatio: 2, CacheCreation1hRatio: 3.2,
					GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
				require.Nil(t, service.PreConsumeBilling(c, 100, info))
				var heldUser model.User
				require.NoError(t, db.First(&heldUser, user.Id).Error)
				assert.Equal(t, startingQuota-100, heldUser.Quota)
				require.Nil(t, run(c, info))
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.Equal(t, clientFormat, info.RelayFormat, "conversion preserves the client protocol")
				assert.False(t, info.Billing.NeedsRefund())

				require.Len(t, captured, 1)
				sent := <-captured
				assert.Equal(t, "/v1/messages", sent.path)
				assert.Equal(t, "Bearer channel-secret", sent.header.Get("Authorization"))
				assert.Equal(t, "channel-org", sent.header.Get("OpenAI-Organization"))
				assert.Equal(t, "2023-06-01", sent.header.Get("anthropic-version"))
				assert.Empty(t, sent.header.Get("x-api-key"))
				var sentBody map[string]any
				require.NoError(t, common.Unmarshal(sent.body, &sentBody))
				assert.Equal(t, float64(64), sentBody["max_tokens"])
				assert.Equal(t, float64(0), sentBody["temperature"], "explicit zero survives conversion")
				assert.Contains(t, sentBody, "messages")
				assert.NotContains(t, sentBody, "input")
				require.Len(t, sentBody["tools"], 1)
				assert.Equal(t, "weather", sentBody["tools"].([]any)[0].(map[string]any)["name"])

				responseBody := recorder.Body.String()
				if client == "claude" {
					if stream {
						assert.Equal(t, 1, strings.Count(responseBody, "event: message_stop\n"))
						assert.Contains(t, responseBody, `"type":"input_json_delta"`)
						assert.Contains(t, responseBody, `"cache_read_input_tokens":5`)
						assert.Contains(t, responseBody, `"cache_creation_input_tokens":3`)
						assert.Contains(t, responseBody, `"output_tokens":7`)
					} else {
						var response dto.ClaudeResponse
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
						require.Len(t, response.Content, 2)
						assert.Equal(t, "hello from messages", response.Content[0].GetText())
						assert.Equal(t, "tool_use", response.Content[1].Type)
						assert.Equal(t, "tool_use", response.StopReason)
						assert.Equal(t, 11, response.Usage.InputTokens)
						assert.Equal(t, 5, response.Usage.CacheReadInputTokens)
						assert.Equal(t, 3, response.Usage.CacheCreationInputTokens)
					}
				} else if client == "chat" {
					var clientUsage *dto.Usage
					if stream {
						var text, toolName, toolArgs strings.Builder
						var finish string
						for line := range strings.SplitSeq(responseBody, "\n") {
							data, ok := strings.CutPrefix(line, "data: ")
							if !ok || data == "[DONE]" {
								continue
							}
							var chunk dto.ChatCompletionsStreamResponse
							require.NoError(t, common.Unmarshal([]byte(data), &chunk))
							if chunk.Usage != nil {
								clientUsage = chunk.Usage
							}
							for _, choice := range chunk.Choices {
								text.WriteString(choice.Delta.GetContentString())
								for _, call := range choice.Delta.ToolCalls {
									toolName.WriteString(call.Function.Name)
									toolArgs.WriteString(call.Function.Arguments)
								}
								if choice.FinishReason != nil {
									finish = *choice.FinishReason
								}
							}
						}
						assert.Equal(t, "hello from messages", text.String())
						assert.Equal(t, "weather", toolName.String())
						assert.JSONEq(t, `{"city":"Paris"}`, toolArgs.String())
						assert.Equal(t, "tool_calls", finish)
						assert.Equal(t, 1, strings.Count(responseBody, "data: [DONE]"))
					} else {
						var response dto.OpenAITextResponse
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
						require.Len(t, response.Choices, 1)
						assert.Equal(t, "hello from messages", response.Choices[0].Message.StringContent())
						require.Len(t, response.Choices[0].Message.ParseToolCalls(), 1)
						assert.Equal(t, "weather", response.Choices[0].Message.ParseToolCalls()[0].Function.Name)
						assert.JSONEq(t, `{"city":"Paris"}`, response.Choices[0].Message.ParseToolCalls()[0].Function.Arguments)
						assert.Equal(t, "tool_calls", response.Choices[0].FinishReason)
						clientUsage = &response.Usage
					}
					require.NotNil(t, clientUsage)
					assert.Equal(t, 19, clientUsage.PromptTokens, "OpenAI input includes cache reads and writes")
					assert.Equal(t, 7, clientUsage.CompletionTokens)
					assert.Equal(t, 26, clientUsage.TotalTokens)
					assert.Equal(t, 5, clientUsage.PromptTokensDetails.CachedTokens)
					assert.Equal(t, 3, clientUsage.PromptTokensDetails.CacheCreationTokensTotal())
				} else {
					var response dto.OpenAIResponsesResponse
					if stream {
						var types []string
						for line := range strings.SplitSeq(responseBody, "\n") {
							data, ok := strings.CutPrefix(line, "data: ")
							if !ok {
								continue
							}
							var event struct {
								Type     string                      `json:"type"`
								Response dto.OpenAIResponsesResponse `json:"response"`
							}
							require.NoError(t, common.Unmarshal([]byte(data), &event))
							types = append(types, event.Type)
							if event.Type == "response.completed" {
								response = event.Response
							}
						}
						require.NotEmpty(t, types)
						assert.Equal(t, "response.created", types[0])
						assert.Equal(t, "response.completed", types[len(types)-1])
						assert.Equal(t, 1, strings.Count(responseBody, "event: response.completed\n"))
						assert.Contains(t, types, "response.function_call_arguments.delta")
					} else {
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					}
					assert.Equal(t, "response", response.Object)
					assert.JSONEq(t, `"completed"`, string(response.Status))
					require.Len(t, response.Output, 2)
					assert.Equal(t, "hello from messages", response.Output[0].Content[0].Text)
					assert.Equal(t, "function_call", response.Output[1].Type)
					assert.Equal(t, "weather", response.Output[1].Name)
					var arguments string
					require.NoError(t, common.Unmarshal(response.Output[1].Arguments, &arguments))
					assert.JSONEq(t, `{"city":"Paris"}`, arguments)
					require.NotNil(t, response.Usage)
					assert.Equal(t, 19, response.Usage.InputTokens)
					assert.Equal(t, 7, response.Usage.OutputTokens)
					assert.Equal(t, 26, response.Usage.TotalTokens)
					assert.Equal(t, 5, response.Usage.InputTokensDetails.CachedTokens)
				}

				// Anthropic input excludes cache categories. Charge exactly
				// (11 + 5*0.2 + 3*2 + 7*3)*2 = 78, irrespective of client shape.
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				require.NoError(t, db.First(&channel, channel.Id).Error)
				assert.Equal(t, startingQuota-78, user.Quota)
				assert.Equal(t, 78, user.UsedQuota)
				assert.Equal(t, 1, user.RequestCount)
				assert.Equal(t, startingQuota-78, token.RemainQuota)
				assert.Equal(t, 78, token.UsedQuota)
				assert.Equal(t, int64(78), channel.UsedQuota)
				var logs []model.Log
				require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
				require.Len(t, logs, 1)
				assert.Equal(t, 78, logs[0].Quota)
				assert.Equal(t, 11, logs[0].PromptTokens)
				assert.Equal(t, 7, logs[0].CompletionTokens)
				assert.Equal(t, stream, logs[0].IsStream)
				var other map[string]any
				require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
				assert.Equal(t, float64(5), other["cache_tokens"])
				assert.Equal(t, float64(3), other["cache_creation_tokens"])
				assert.Equal(t, "anthropic", other["usage_semantic"])
			})
		}
	}
}

func TestMessagesEndpointRetainsConfigurationAndResetsOnRetry(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	testutil.UseInactiveChannelBudget(t)
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy.Enabled = false
	t.Cleanup(func() { *settings = previous })
	for _, client := range []string{"chat", "claude"} {
		for _, prompt := range []string{"absent", "client preserved", "admin prepended"} {
			t.Run(client+"/"+prompt, func(t *testing.T) {
				type capturedRequest struct {
					body   []byte
					header http.Header
					uri    string
				}
				captured := make(chan capturedRequest, 2)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					captured <- capturedRequest{body: body, header: r.Header.Clone(), uri: r.URL.RequestURI()}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "45")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user"}}`)
				}))
				defer server.Close()
				path := "/v1/chat/completions"
				body := `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
				if client == "claude" {
					path = "/v1/messages?beta=true"
					if prompt != "absent" {
						body = strings.TrimSuffix(body, "}") + `,"system":"client prompt"}`
					}
				} else if prompt != "absent" {
					body = strings.Replace(body, `"messages":[`, `"messages":[{"role":"system","content":"client prompt"},`, 1)
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("Authorization", "Bearer client-secret")
				c.Request.Header.Set("x-api-key", "client-anthropic-secret")
				c.Request.Header.Set("anthropic-version", "2023-06-02")
				c.Request.Header.Add("anthropic-beta", "client-beta-a")
				c.Request.Header.Add("anthropic-beta", "client-beta-b")
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "m")
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
				common.SetContextKey(c, constant.ContextKeyChannelKey, "channel-key")
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
					SupportedEndpoints: []string{"messages"}, SystemPrompt: "admin prompt", SystemPromptOverride: prompt == "admin prepended",
				})
				common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{ClaudeBetaQuery: client == "chat"})
				c.Set("channel_organization", "channel-org")
				wantAuth, wantVersion, wantBeta := "Bearer channel-key", "2023-06-02", "client-beta-a,client-beta-b"
				if prompt == "admin prepended" {
					common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{
						"Authorization": "Bearer configured-key", "anthropic-version": "2023-06-03", "anthropic-beta": "configured-beta",
					})
					wantAuth, wantVersion, wantBeta = "Bearer configured-key", "2023-06-03", "configured-beta"
				}
				var info *relaycommon.RelayInfo
				var run func(*gin.Context, *relaycommon.RelayInfo) *relaytypes.NewAPIError
				if client == "claude" {
					var req dto.ClaudeRequest
					require.NoError(t, common.Unmarshal([]byte(body), &req))
					info, run = relaycommon.GenRelayInfoClaude(c, &req), ClaudeHelper
				} else {
					var req dto.GeneralOpenAIRequest
					require.NoError(t, common.Unmarshal([]byte(body), &req))
					info, run = relaycommon.GenRelayInfoOpenAI(c, &req), TextHelper
				}
				apiErr := run(c, info)
				require.NotNil(t, apiErr)
				assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
				assert.Equal(t, service.UpstreamRejectionConcurrency, service.ClassifyUpstreamRejection(apiErr))
				assert.Equal(t, "45", helper.UpstreamRetryAfter(c))
				assert.False(t, c.Writer.Written())
				require.Len(t, captured, 1)
				sent := <-captured
				assert.Equal(t, "/v1/messages?beta=true", sent.uri)
				assert.Equal(t, wantAuth, sent.header.Get("Authorization"))
				assert.Equal(t, wantVersion, sent.header.Get("anthropic-version"))
				assert.Equal(t, wantBeta, sent.header.Get("anthropic-beta"))
				assert.Equal(t, "channel-org", sent.header.Get("OpenAI-Organization"))
				assert.Empty(t, sent.header.Get("x-api-key"))
				var upstream dto.ClaudeRequest
				require.NoError(t, common.Unmarshal(sent.body, &upstream))
				var system strings.Builder
				if upstream.IsStringSystem() {
					system.WriteString(upstream.GetStringSystem())
				} else {
					for _, part := range upstream.ParseSystem() {
						system.WriteString(part.GetText())
					}
				}
				wantAdmin := 1
				if prompt == "client preserved" {
					wantAdmin = 0
				}
				assert.Equal(t, wantAdmin, strings.Count(system.String(), "admin prompt"))
				assert.Equal(t, prompt != "absent", strings.Contains(system.String(), "client prompt"))

				// A retry reuses RelayInfo but must use its newly selected channel.
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: []string{"chat"}})
				common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
				common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{})
				apiErr = run(c, info)
				require.NotNil(t, apiErr)
				assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
				assert.Empty(t, info.UpstreamTextEndpoint)
				require.Len(t, captured, 1)
				retried := <-captured
				assert.Equal(t, "/v1/chat/completions", retried.uri)
				assert.Empty(t, retried.header.Get("anthropic-version"))
				assert.Empty(t, retried.header.Get("anthropic-beta"))
				assert.NotContains(t, string(retried.body), "admin prompt", "retry starts from the original request")
			})
		}
	}
}

func TestMessagesEndpointRejectsResponsesWebSocketBeforeUpstreamDial(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	previousRedis, previousMemory, previousBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	previousLog, previousExport, previousCount := common.LogConsumeEnabled, common.DataExportEnabled, constant.CountToken
	previousPrice, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	settings := model_setting.GetGlobalSettings()
	previousSettings := *settings
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled, common.DataExportEnabled, constant.CountToken = true, false, false
	settings.PassThroughRequestEnabled = false
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"messages-ws-test":0.000156}`))
	t.Cleanup(func() {
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = previousRedis, previousMemory, previousBatch
		common.LogConsumeEnabled, common.DataExportEnabled, constant.CountToken = previousLog, previousExport, previousCount
		*settings = previousSettings
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(previousPrice)))
	})
	for _, supported := range []bool{false, true} {
		name := "messages only"
		if supported {
			name = "messages and responses"
		}
		t.Run(name, func(t *testing.T) {
			testutil.UseInactiveChannelBudget(t)
			db := model.DB
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.ChannelKeyEntry{}, &model.Log{}))
			const startingQuota = 1000000
			user := model.User{Username: "ws-user", Quota: startingQuota, Status: common.UserStatusEnabled, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "ws-client-key", RemainQuota: startingQuota, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			var dials atomic.Int32
			upstreamDone := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dials.Add(1)
				assert.Equal(t, "/v1/responses", r.URL.Path)
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if !assert.NoError(t, err) {
					return
				}
				defer conn.Close()
				defer func() { upstreamDone <- struct{}{} }()
				_, body, err := conn.ReadMessage()
				if !assert.NoError(t, err) {
					return
				}
				var create map[string]any
				if !assert.NoError(t, common.Unmarshal(body, &create)) {
					return
				}
				assert.Equal(t, "response.create", create["type"])
				assert.Contains(t, create, "input")
				assert.NotContains(t, create, "messages")
				for _, event := range []string{
					`{"type":"response.created","response":{"id":"resp_ws","model":"messages-ws-test","status":"in_progress"}}`,
					`{"type":"response.output_text.delta","response_id":"resp_ws","delta":"native responses"}`,
					`{"type":"response.completed","response":{"id":"resp_ws","model":"messages-ws-test","status":"completed","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
				} {
					if !assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(event))) {
						return
					}
				}
				// Keep the socket open until the client closes it; EOF must not race settlement.
				_, _, _ = conn.ReadMessage()
			}))
			defer upstream.Close()
			endpoints := []string{"messages"}
			if supported {
				endpoints = append(endpoints, "responses")
			}
			channel := model.Channel{Name: "messages-ws", Type: constant.ChannelTypeOpenAI, Key: "upstream-key", BaseURL: &upstream.URL,
				Models: "messages-ws-test", Group: "default", Status: common.ChannelStatusEnabled}
			channel.SetSetting(dto.ChannelSettings{SupportedEndpoints: endpoints, ResponsesWebSocketEnabled: true})
			require.NoError(t, db.Create(&channel).Error)
			client, server, closeSockets := newTestWebSocketPair(t)
			defer closeSockets()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ResponsesWebSocketHelper(c, server, func(request *http.Request, id string, run func(*gin.Context) *relaytypes.NewAPIError) *relaytypes.NewAPIError {
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = request
					defer common.CleanupBodyStorage(ctx)
					common.SetContextKey(ctx, common.RequestIdKey, id)
					common.SetContextKey(ctx, constant.ContextKeyUserId, user.Id)
					common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
					common.SetContextKey(ctx, constant.ContextKeyTokenKey, token.Key)
					common.SetContextKey(ctx, constant.ContextKeyUserQuota, startingQuota)
					common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
					common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
					common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
					service.GetChannelConstraints(ctx).AddPin(appdto.ChannelPin{ChannelId: channel.Id, Source: appdto.PinSourceToken, Rank: appdto.PinRankToken, RetryMode: appdto.PinRetrySingleAttempt})
					return run(ctx)
				})
			}()
			require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"messages-ws-test","input":"hi"}`)))
			require.NoError(t, client.SetReadDeadline(time.Now().Add(10*time.Second)))
			var kinds []string
			var payload map[string]any
			for {
				_, body, err := client.ReadMessage()
				require.NoError(t, err)
				require.NoError(t, common.Unmarshal(body, &payload))
				kind, _ := payload["type"].(string)
				kinds = append(kinds, kind)
				if kind == "error" || kind == "response.completed" {
					break
				}
			}
			_ = client.Close()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("websocket session did not close")
			}
			if supported {
				assert.Equal(t, []string{"response.created", "response.output_text.delta", "response.completed"}, kinds)
				assert.Equal(t, int32(1), dials.Load())
				select {
				case <-upstreamDone:
				case <-time.After(10 * time.Second):
					t.Fatal("upstream socket did not close")
				}
			} else {
				assert.Equal(t, []string{"error"}, kinds)
				assert.Equal(t, float64(http.StatusBadRequest), payload["status"])
				assert.Zero(t, dials.Load())
			}
			wantCharge := 0
			if supported {
				wantCharge = 78
			}
			// Failed attempts refund asynchronously; observe both durable balances before cleanup.
			require.Eventually(t, func() bool {
				var storedUser model.User
				var storedToken model.Token
				return db.First(&storedUser, user.Id).Error == nil && db.First(&storedToken, token.Id).Error == nil &&
					storedUser.Quota == startingQuota-wantCharge && storedToken.RemainQuota == startingQuota-wantCharge
			}, 5*time.Second, 10*time.Millisecond)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			if supported {
				require.Len(t, logs, 1)
				assert.Equal(t, 78, logs[0].Quota)
			} else {
				assert.Empty(t, logs)
			}
		})
	}
}
