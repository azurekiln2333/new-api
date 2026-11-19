package relay

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSupportedEndpointsChooseOutboundRequest(t *testing.T) {
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
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
		{name: "legacy native chat", wantPath: "/v1/chat/completions"},
		{name: "legacy global conversion", globalResponses: true, wantPath: "/v1/responses"},
		{name: "empty declaration keeps global conversion", endpoints: []string{}, globalResponses: true, wantPath: "/v1/responses"},
		{name: "responses-only forces conversion", endpoints: []string{"responses"}, wantPath: "/v1/responses"},
		{name: "chat-only constrains global conversion", endpoints: []string{"chat"}, globalResponses: true, wantPath: "/v1/chat/completions"},
		{name: "both preserves native chat", endpoints: []string{"chat", "responses"}, wantPath: "/v1/chat/completions"},
		{name: "both honors supported global preference", endpoints: []string{"chat", "responses"}, globalResponses: true, wantPath: "/v1/responses"},
		{name: "messages to responses", inputPath: "/v1/messages", endpoints: []string{"responses"}, wantPath: "/v1/responses"},
		{name: "messages to chat", inputPath: "/v1/messages", endpoints: []string{"chat"}, globalResponses: true, wantPath: "/v1/chat/completions"},
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
			} else {
				assert.Contains(t, sent, "messages")
				assert.NotContains(t, sent, "input")
			}
		})
	}
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
						assert.Contains(t, string(payload), `"input"`)
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

func TestChannelEndpointResponsesOnlyRejectsGeminiBeforeSending(t *testing.T) {
	require.NoError(t, i18n.Init())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gpt-4o:generateContent", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{SupportedEndpoints: []string{"responses"}})
	request := &dto.GeminiChatRequest{}
	info := relaycommon.GenRelayInfoGemini(c, request)
	apiErr := GeminiHelper(c, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
}

func TestChannelEndpointResponsesConversionPreservesClientFormat(t *testing.T) {
	service.InitHttpClient()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() { *settings = previous })
	settings.PassThroughRequestEnabled = false
	for _, client := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			name := client + "/json"
			if stream {
				name = client + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
