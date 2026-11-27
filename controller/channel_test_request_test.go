package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/lmfp"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/ali"
	"github.com/QuantumNous/new-api/relay/channel/cloudflare"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

func convertChatCompatibilityRequest(t *testing.T, request *dto.GeneralOpenAIRequest, channelType int, mapping map[string]string) []byte {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	settings := model_setting.GetGlobalSettings()
	oldPassThrough, oldBlacklist := settings.PassThroughRequestEnabled, settings.ThinkingModelBlacklist
	oldEffortTailModels := settings.EffortTailModelIDs
	settings.PassThroughRequestEnabled = false
	settings.ThinkingModelBlacklist = nil
	settings.EffortTailModelIDs = []string{"gpt-5.1-codex-max"}
	t.Cleanup(func() {
		settings.PassThroughRequestEnabled = oldPassThrough
		settings.ThinkingModelBlacklist = oldBlacklist
		settings.EffortTailModelIDs = oldEffortTailModels
	})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if mapping != nil {
		encoded, err := common.Marshal(mapping)
		require.NoError(t, err)
		c.Set("model_mapping", string(encoded))
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: request.Model,
		Request:         request,
		RelayFormat:     types.RelayFormatOpenAI,
		IsStream:        request.IsStream(nil),
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channelType,
			UpstreamModelName:    request.Model,
			SupportStreamOptions: true,
		},
	}
	require.NoError(t, helper.ModelMappedHelper(c, info, request))
	require.NoError(t, helper.ApplyReasoningModelSuffix(c, info, request))
	var converted any
	var err error
	if channelType == constant.ChannelTypeAli {
		converted, err = (&ali.Adaptor{}).ConvertOpenAIRequest(c, info, request)
	} else {
		converted, err = (&openai.Adaptor{}).ConvertOpenAIRequest(c, info, request)
	}
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	return encoded
}

func TestChannelTestOpenAIChatCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name        string
		model       string
		upstream    string
		endpoint    string
		channelType int
		stream      bool
		wantLimit   string
	}{
		{name: "GPT6 automatic", model: "gpt-6-astra", upstream: "gpt-6-astra", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "GPT6 explicit Azure stream", model: "gpt-6-astra", upstream: "gpt-6-astra", endpoint: string(constant.EndpointTypeOpenAI), channelType: constant.ChannelTypeAzure, stream: true, wantLimit: "max_completion_tokens"},
		{name: "GPT6 sol", model: "gpt-6-sol", upstream: "gpt-6-sol", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "GPT6 luna", model: "gpt-6-luna", upstream: "gpt-6-luna", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "GPT6.1 sol", model: "gpt-6.1-sol", upstream: "gpt-6.1-sol", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "GPT6.1 terra", model: "gpt-6.1-terra", upstream: "gpt-6.1-terra", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "alias maps to GPT6", model: "customer-model", upstream: "gpt-6-astra", channelType: constant.ChannelTypeOpenAI, wantLimit: "max_completion_tokens"},
		{name: "GPT5 alias maps to Qwen", model: "gpt-5.6-luna", upstream: "qwen-turbo", channelType: constant.ChannelTypeAli, wantLimit: "max_tokens"},
		{name: "GPT5 stream", model: "gpt-5.6-luna", upstream: "gpt-5.6-luna", channelType: constant.ChannelTypeOpenAI, stream: true, wantLimit: "max_completion_tokens"},
		{name: "GPT4 explicit", model: "gpt-4.1", upstream: "gpt-4.1", endpoint: string(constant.EndpointTypeOpenAI), channelType: constant.ChannelTypeOpenAI, wantLimit: "max_tokens"},
		{name: "o series", model: "o3-mini", upstream: "o3-mini", channelType: constant.ChannelTypeAzure, wantLimit: "max_completion_tokens"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := constant.EndpointType(tt.endpoint)
			if endpoint == "" {
				endpoint = constant.EndpointTypeOpenAI
			}
			request, ok := buildTestRequest(tt.model, endpoint, tt.endpoint == "", tt.stream, nil).(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			encoded := convertChatCompatibilityRequest(t, request, tt.channelType, map[string]string{tt.model: tt.upstream})
			want := map[string]any{
				"model":      tt.upstream,
				"messages":   []dto.Message{{Role: "user", Content: "hi"}},
				"stream":     tt.stream,
				tt.wantLimit: 16,
			}
			if tt.stream {
				want["stream_options"] = map[string]any{"include_usage": true}
			}
			wantJSON, err := common.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(encoded))
		})
	}
}

func TestOpenAIChatSamplingCompatibility(t *testing.T) {
	const sampling = `{"temperature":0.2,"top_p":0.8,"logprobs":true,"top_logprobs":5}`
	for _, tt := range []struct {
		name       string
		model      string
		effort     string
		reasoning  string
		mapping    map[string]string
		zeroValues bool
		wantModel  string
		wantEffort string
		wantRole   string
		wantParams string
	}{
		{name: "GPT5.1 explicit none", model: "gpt-5.1", effort: "none", wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "GPT5.2 default none", model: "gpt-5.2", wantRole: "developer", wantParams: sampling},
		{name: "GPT5.2 dated snapshot", model: "gpt-5.2-2025-12-11", wantRole: "developer", wantParams: sampling},
		{name: "GPT5.4 reasoning", model: "gpt-5.4", effort: "high", wantEffort: "high", wantRole: "developer", wantParams: `{}`},
		{name: "GPT5.4 snapshot none", model: "gpt-5.4-2026-03-05", effort: "none", wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "explicit zero values", model: "gpt-5.4", effort: "none", zeroValues: true, wantEffort: "none", wantRole: "developer", wantParams: `{"temperature":0,"top_p":0,"logprobs":false}`},
		{name: "GPT5 original", model: "gpt-5", wantRole: "developer", wantParams: `{}`},
		{name: "GPT5.6 existing policy", model: "gpt-5.6-luna", wantRole: "developer", wantParams: `{}`},
		{name: "pro variant", model: "gpt-5.2-pro-2025-12-11", wantRole: "developer", wantParams: `{}`},
		{name: "chat variant", model: "gpt-5.2-chat-latest", wantRole: "developer", wantParams: `{}`},
		{name: "codex model name keeps max", model: "gpt-5.1-codex-max", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6", model: "gpt-6-astra", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6 snapshot", model: "gpt-6-astra-2026-09-03", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6 effort suffix", model: "gpt-6-astra-high", wantModel: "gpt-6-astra", wantEffort: "high", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6 sol none keeps sampling", model: "gpt-6-sol", wantRole: "developer", wantParams: sampling},
		{name: "GPT6 luna effort drops sampling", model: "gpt-6-luna-high", wantModel: "gpt-6-luna", wantEffort: "high", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6.1 sol drops sampling", model: "gpt-6.1-sol", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6.1 terra default drops sampling", model: "gpt-6.1-terra", wantRole: "developer", wantParams: `{}`},
		{name: "GPT6.1 terra none keeps sampling", model: "gpt-6.1-terra", effort: "none", wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "none effort suffix", model: "gpt-5.2-none", wantModel: "gpt-5.2", wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "modifier overrides explicit effort", model: "gpt-5.2@thinking:off", effort: "high", wantModel: "gpt-5.2", wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "mapped modifier wins", model: "customer-model@thinking:off", mapping: map[string]string{"customer-model": "gpt-5.2@effort:high"}, wantModel: "gpt-5.2", wantEffort: "high", wantRole: "developer", wantParams: `{}`},
		{name: "nested reasoning disabled", model: "gpt-5.2", reasoning: `{"enabled":false}`, wantEffort: "none", wantRole: "developer", wantParams: sampling},
		{name: "o1 mini role exception", model: "o1-mini", wantRole: "system", wantParams: `{"top_p":0.8,"logprobs":true,"top_logprobs":5}`},
		{name: "GPT4 unchanged", model: "gpt-4.1", wantRole: "system", wantParams: sampling},
		{name: "future model unchanged", model: "gpt-7", wantRole: "system", wantParams: sampling},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model: tt.model,
				Messages: []dto.Message{
					{Role: "system", Content: "first instruction"},
					{Role: "system", Content: "second instruction"},
					{Role: "user", Content: "hi"},
				},
				ReasoningEffort: tt.effort,
			}
			require.NoError(t, common.UnmarshalJsonStr(sampling, request))
			if tt.reasoning != "" {
				request.Reasoning = []byte(tt.reasoning)
			}
			if tt.zeroValues {
				request.Temperature = lo.ToPtr(0.0)
				request.TopP = lo.ToPtr(0.0)
				request.LogProbs = lo.ToPtr(false)
				request.TopLogProbs = nil
			}
			encoded := convertChatCompatibilityRequest(t, request, constant.ChannelTypeOpenAI, tt.mapping)
			var want map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tt.wantParams, &want))
			want["model"] = tt.model
			if tt.wantModel != "" {
				want["model"] = tt.wantModel
			}
			want["messages"] = []dto.Message{
				{Role: tt.wantRole, Content: "first instruction"},
				{Role: "system", Content: "second instruction"},
				{Role: "user", Content: "hi"},
			}
			if tt.wantEffort != "" {
				want["reasoning_effort"] = tt.wantEffort
			}
			wantJSON, err := common.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(encoded))
		})
	}
}

func TestOpenAIChatTokenLimitCompatibility(t *testing.T) {
	for _, modelName := range []string{"gpt-5", "o3-mini", "gpt-6-astra"} {
		for _, tt := range []struct {
			name  string
			input string
			want  string
		}{
			{name: "omitted", input: `{}`, want: `{}`},
			{name: "legacy only", input: `{"max_tokens":100}`, want: `{"max_completion_tokens":100}`},
			{name: "completion only", input: `{"max_completion_tokens":50}`, want: `{"max_completion_tokens":50}`},
			{name: "both positive stay present", input: `{"max_tokens":100,"max_completion_tokens":50}`, want: `{"max_tokens":100,"max_completion_tokens":50}`},
			{name: "zero completion falls back", input: `{"max_tokens":100,"max_completion_tokens":0}`, want: `{"max_completion_tokens":100}`},
			{name: "legacy zero stays present", input: `{"max_tokens":0}`, want: `{"max_tokens":0}`},
			{name: "completion zero stays present", input: `{"max_completion_tokens":0}`, want: `{"max_completion_tokens":0}`},
			{name: "both zero stay present", input: `{"max_tokens":0,"max_completion_tokens":0}`, want: `{"max_tokens":0,"max_completion_tokens":0}`},
		} {
			t.Run(modelName+"/"+tt.name, func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{Model: modelName, Messages: []dto.Message{{Role: "user", Content: "hi"}}}
				require.NoError(t, common.UnmarshalJsonStr(tt.input, request))
				encoded := convertChatCompatibilityRequest(t, request, constant.ChannelTypeOpenAI, nil)
				want := dto.GeneralOpenAIRequest{Model: modelName, Messages: []dto.Message{{Role: "user", Content: "hi"}}}
				require.NoError(t, common.UnmarshalJsonStr(tt.want, &want))
				wantJSON, err := common.Marshal(want)
				require.NoError(t, err)
				assert.JSONEq(t, string(wantJSON), string(encoded))
			})
		}
	}
}

func TestDirectOpenAIResponsesKeepsExistingParameters(t *testing.T) {
	const body = `{"model":"gpt-6-astra","input":"hi","max_output_tokens":100,"temperature":0.2,"top_p":0.8,"top_logprobs":5,"include":["message.output_text.logprobs"],"reasoning":{"effort":"high"}}`
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-6-astra",
		RelayFormat:     types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-6-astra",
		},
	}
	converted, err := (&openai.Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, body, string(encoded))
}

func TestCloudflareEmbeddingResponseCompatibility(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		forceFormat bool
		wantTokens  int
	}{
		{
			name:       "single embedding preserves upstream usage",
			body:       `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,-0.2]}],"model":"@cf/baai/bge-large-en-v1.5","usage":{"prompt_tokens":8,"total_tokens":8}}`,
			wantTokens: 8,
		},
		{
			name:        "batch embeddings survive force format",
			body:        `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,-0.2]},{"object":"embedding","index":1,"embedding":[0.3,0.4]}],"model":"@cf/baai/bge-large-en-v1.5","usage":{"prompt_tokens":16,"total_tokens":16},"extra":"preserved"}`,
			forceFormat: true,
			wantTokens:  16,
		},
		{
			name:       "base64 embedding is forwarded unchanged",
			body:       `{"object":"list","data":[{"object":"embedding","index":0,"embedding":"zczMPc3MTL4="}],"model":"@cf/baai/bge-large-en-v1.5","usage":{"prompt_tokens":5,"total_tokens":5}}`,
			wantTokens: 5,
		},
		{
			name:        "missing usage falls back to input estimate",
			body:        `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,-0.2]}],"model":"@cf/baai/bge-large-en-v1.5"}`,
			forceFormat: true,
			wantTokens:  99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
			info := &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				RelayFormat: types.RelayFormatEmbedding,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:       constant.ChannelCloudflare,
					UpstreamModelName: "@cf/baai/bge-large-en-v1.5",
					ChannelSetting:    dto.ChannelSettings{ForceFormat: tt.forceFormat},
				},
			}
			info.SetEstimatePromptTokens(99)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}

			usage, apiErr := (&cloudflare.Adaptor{}).DoResponse(c, resp, info)
			require.Nil(t, apiErr)
			require.IsType(t, &dto.Usage{}, usage)
			assert.Equal(t, &dto.Usage{PromptTokens: tt.wantTokens, TotalTokens: tt.wantTokens}, usage)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.JSONEq(t, tt.body, w.Body.String())
		})
	}
}

func TestFingerprintReplyText(t *testing.T) {
	for _, tt := range []struct {
		name     string
		endpoint string
		body     string
		want     string
		wantErr  bool
	}{
		{
			name:     "chat completions",
			endpoint: "openai",
			body:     `{"choices":[{"message":{"role":"assistant","content":" 12 34 56 "}}]}`,
			want:     "12 34 56",
		},
		{
			name:     "responses output text",
			endpoint: "openai-response",
			body:     `{"output_text":"12 34"}`,
			want:     "12 34",
		},
		{
			name:     "responses output parts",
			endpoint: "openai-response",
			body:     `{"output":[{"content":[{"type":"output_text","text":"12"},{"type":"output_text","text":"34"}]}]}`,
			want:     "12\n34",
		},
		{
			name:     "anthropic text block",
			endpoint: "anthropic",
			body:     `{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"12 34"}]}`,
			want:     "12 34",
		},
		{
			name:     "gemini parts",
			endpoint: "gemini",
			body:     `{"candidates":[{"content":{"parts":[{"text":"12 "},{"text":"34"}]}}]}`,
			want:     "12 \n34",
		},
		{
			name: "anthropic all text blocks only", endpoint: "anthropic",
			body: `{"content":[{"type":"thinking","text":"999"},{"type":"text","text":"12"},{"type":"text","text":"34"}]}`,
			want: "12\n34",
		},
		{
			name: "gemini excludes thoughts", endpoint: "gemini",
			body: `{"candidates":[{"content":{"parts":[{"thought":true,"text":"999"},{"text":"12"},{"text":"34"}]}}]}`,
			want: "12\n34",
		},
		{
			name: "chat text blocks", endpoint: "openai",
			body: `{"choices":[{"message":{"content":[{"type":"text","text":"12"},{"type":"image_url","text":"999"},{"type":"text","text":"34"}]}}]}`,
			want: "12\n34",
		},
		{
			name:     "empty reply is an error",
			endpoint: "openai",
			body:     `{"choices":[{"message":{"role":"assistant","content":""}}]}`,
			wantErr:  true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, err := fingerprintReplyText([]byte(tt.body), tt.endpoint)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, text)
		})
	}
}

// The recorder sits immediately below the probe's final wire guard; it observes
// the exact bytes an adaptor would send after channel parameter overrides.
type fingerprintRequestRecorder struct {
	channel.Adaptor
	body []byte
}

func (recorder *fingerprintRequestRecorder) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (any, error) {
	var err error
	recorder.body, err = io.ReadAll(body)
	return nil, err
}

func TestFingerprintWireOutputBudget(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		apiType               int
		format                types.RelayFormat
		body, path, countPath string
		want                  uint64
		wantErr               bool
	}{
		{name: "chat override", apiType: constant.APITypeOpenAI, format: types.RelayFormatOpenAI, body: `{"max_tokens":9000,"n":8}`, path: "max_tokens", countPath: "n", want: 300},
		{name: "reasoning override", apiType: constant.APITypeOpenAI, format: types.RelayFormatOpenAI, body: `{"max_tokens":9000,"max_completion_tokens":8000}`, path: "max_completion_tokens", countPath: "n", want: 300},
		{name: "responses override", apiType: constant.APITypeOpenAI, format: types.RelayFormatOpenAIResponses, body: `{"max_output_tokens":9000}`, path: "max_output_tokens", want: 300},
		{name: "anthropic smaller limit", apiType: constant.APITypeAnthropic, format: types.RelayFormatClaude, body: `{"max_tokens":100}`, path: "max_tokens", want: 100},
		{name: "gemini override", apiType: constant.APITypeGemini, format: types.RelayFormatGemini, body: `{"generationConfig":{"maxOutputTokens":9000,"candidateCount":8}}`, path: "generationConfig.maxOutputTokens", countPath: "generationConfig.candidateCount", want: 300},
		{name: "gemini null config", apiType: constant.APITypeGemini, format: types.RelayFormatGemini, body: `{"generationConfig":null}`, path: "generationConfig.maxOutputTokens", countPath: "generationConfig.candidateCount", want: 300},
		{name: "missing output limit", apiType: constant.APITypeNewAPI, format: types.RelayFormatOpenAIResponses, body: `{}`, path: "max_output_tokens", want: 300},
		{name: "codex cannot enforce", apiType: constant.APITypeCodex, format: types.RelayFormatOpenAIResponses, body: `{}`, wantErr: true},
		{name: "malformed limit", apiType: constant.APITypeOpenAI, format: types.RelayFormatOpenAI, body: `{"max_tokens":-1}`, wantErr: true},
		{name: "hosted tools forbidden", apiType: constant.APITypeOpenAI, format: types.RelayFormatOpenAIResponses, body: `{"tools":[{"type":"web_search"}]}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &fingerprintRequestRecorder{}
			adaptor := &fingerprintBoundedAdaptor{Adaptor: recorder, maxOutputTokens: 300}
			info := &relaycommon.RelayInfo{RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{ApiType: tc.apiType}}
			var body map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &body))
			switch tc.format {
			case types.RelayFormatOpenAIResponses:
				body["input"] = "bounded challenge"
			case types.RelayFormatGemini:
				body["contents"] = []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "bounded challenge"}}}}
			default:
				body["messages"] = []any{map[string]any{"role": "user", "content": "bounded challenge"}}
			}
			encoded, err := common.Marshal(body)
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx := context.WithValue(t.Context(), channelTestPromptKey, testPrompt{Prompt: "bounded challenge", MaxTokens: 300})
			c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
			_, err = adaptor.DoRequest(c, info, strings.NewReader(string(encoded)))
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, recorder.body, "unsupported requests never reach the upstream")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, gjson.GetBytes(recorder.body, tc.path).Uint())
			if tc.countPath != "" {
				assert.EqualValues(t, 1, gjson.GetBytes(recorder.body, tc.countPath).Int())
			}
			if tc.path == "max_completion_tokens" {
				assert.False(t, gjson.GetBytes(recorder.body, "max_tokens").Exists())
			}
		})
	}
}

func TestFingerprintWirePromptIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  types.RelayFormat
		body    string
		wantErr bool
	}{
		{name: "Chat plain text", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"exact challenge"}],"temperature":0.2}`},
		{name: "Chat one text block", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":[{"type":"text","text":"exact challenge"}]}]}`},
		{name: "Responses string", format: types.RelayFormatOpenAIResponses, body: `{"input":"exact challenge"}`},
		{name: "Responses converted user", format: types.RelayFormatOpenAIResponses, body: `{"input":[{"role":"user","content":"exact challenge"}]}`},
		{name: "Responses input text block", format: types.RelayFormatOpenAIResponses, body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"exact challenge"}]}]}`},
		{name: "Messages plain text", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":"exact challenge"}]}`},
		{name: "Messages converted text block", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":[{"type":"text","text":"exact challenge"}]}]}`},
		{name: "Messages adaptive effort", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":"exact challenge"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`},
		{name: "Gemini text part", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"generationConfig":{"maxOutputTokens":9000,"thinkingConfig":{"thinkingBudget":128}}}`},
		{name: "Gemini safety controls", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"}]}`},
		{name: "changed text", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"different challenge"}]}`, wantErr: true},
		{name: "removed text", format: types.RelayFormatOpenAI, body: `{}`, wantErr: true},
		{name: "prepended system", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"system","content":"hidden instruction"},{"role":"user","content":"exact challenge"}]}`, wantErr: true},
		{name: "additional user message", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":"exact challenge"},{"role":"user","content":"extra text"}]}`, wantErr: true},
		{name: "message extension", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"exact challenge","reasoning_content":"hidden instruction"}]}`, wantErr: true},
		{name: "content extension", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":[{"type":"text","text":"exact challenge","content":"hidden instruction"}]}]}`, wantErr: true},
		{name: "Responses instructions", format: types.RelayFormatOpenAIResponses, body: `{"input":"exact challenge","instructions":"hidden instruction"}`, wantErr: true},
		{name: "Responses history", format: types.RelayFormatOpenAIResponses, body: `{"input":"exact challenge","previous_response_id":"previous-response"}`, wantErr: true},
		{name: "Responses template", format: types.RelayFormatOpenAIResponses, body: `{"input":"exact challenge","prompt":{"id":"template"}}`, wantErr: true},
		{name: "Responses context in reasoning", format: types.RelayFormatOpenAIResponses, body: `{"input":"exact challenge","reasoning":{"effort":"high","context":"hidden instruction"}}`, wantErr: true},
		{name: "Messages system", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":"exact challenge"}],"system":"hidden instruction"}`, wantErr: true},
		{name: "Messages output schema instruction", format: types.RelayFormatClaude, body: `{"messages":[{"role":"user","content":"exact challenge"}],"output_config":{"effort":"high","format":{"description":"hidden instruction"}}}`, wantErr: true},
		{name: "provider extension", format: types.RelayFormatOpenAI, body: `{"messages":[{"role":"user","content":"exact challenge"}],"extra_body":{"system":"hidden instruction"}}`, wantErr: true},
		{name: "Gemini system", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"systemInstruction":{"parts":[{"text":"hidden instruction"}]}}`, wantErr: true},
		{name: "Gemini system alias", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"system_instruction":{"parts":[{"text":"hidden instruction"}]}}`, wantErr: true},
		{name: "Gemini cached history", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"cachedContent":"prior-context"}`, wantErr: true},
		{name: "Gemini schema instruction", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"generationConfig":{"responseSchema":{"description":"hidden instruction"}}}`, wantErr: true},
		{name: "Gemini safety text", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"hidden instruction"}]}`, wantErr: true},
		{name: "Gemini safety extension", format: types.RelayFormatGemini, body: `{"contents":[{"role":"user","parts":[{"text":"exact challenge"}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF","instructions":"hidden instruction"}]}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &fingerprintRequestRecorder{}
			adaptor := &fingerprintBoundedAdaptor{Adaptor: recorder, maxOutputTokens: 300}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx := context.WithValue(t.Context(), channelTestPromptKey, testPrompt{Prompt: "exact challenge", MaxTokens: 300})
			c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
			info := &relaycommon.RelayInfo{RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeNewAPI}}
			_, err := adaptor.DoRequest(c, info, strings.NewReader(tc.body))
			if tc.wantErr {
				require.ErrorIs(t, err, errFingerprintWirePromptChanged)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.Empty(t, recorder.body, "a misleading prompt never reaches the transport")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, string(recorder.body), "exact challenge")
		})
	}
}

func TestFingerprintSelectedChannelValidation(t *testing.T) {
	selected := &model.Channel{Type: constant.ChannelTypeOpenAI, Models: "gpt-4.1, gpt-4o"}
	require.NoError(t, validateFingerprintChannel(selected, "gpt-4o"))
	require.ErrorContains(t, validateFingerprintChannel(selected, "another-model"), "not available")
	selected.Type = constant.ChannelTypeCodex
	require.ErrorContains(t, validateFingerprintChannel(selected, "gpt-4o"), "enforceable output token limit")
}

func TestFingerprintResponsesOnlyChannelProbe(t *testing.T) {
	require.NoError(t, i18n.Init())
	rootID := setupChannelCreateTest(t)
	service.InitHttpClient()
	oldLogs := common.LogConsumeEnabled
	oldRatios, oldGroups := ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	geminiSettings := model_setting.GetGeminiSettings()
	oldGeminiSafetySettings := geminiSettings.SafetySettings
	oldGeminiVersionSettings := geminiSettings.VersionSettings
	geminiSettings.SafetySettings = map[string]string{"default": "OFF"}
	geminiSettings.VersionSettings = map[string]string{"default": "v1beta"}
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4.1":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogs
		geminiSettings.SafetySettings = oldGeminiSafetySettings
		geminiSettings.VersionSettings = oldGeminiVersionSettings
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	for _, tc := range []struct {
		name, endpoint, reply string
		messagesUpstream      bool
		geminiUpstream        bool
		wantErr               bool
	}{
		{name: "chat", endpoint: "openai", reply: "12 34 56"},
		{name: "messages", endpoint: "anthropic", reply: "12 34 56"},
		{name: "empty answer retains usage", endpoint: "openai", wantErr: true},
		{name: "messages upstream chat client", endpoint: "openai", reply: "12 34 56", messagesUpstream: true},
		{name: "messages upstream native client", endpoint: "anthropic", reply: "12 34 56", messagesUpstream: true},
		{name: "messages upstream responses client", endpoint: "openai-response", reply: "12 34 56", messagesUpstream: true},
		{name: "gemini upstream chat client", endpoint: "openai", reply: "12 34 56", geminiUpstream: true},
		{name: "gemini upstream responses client", endpoint: "openai-response", reply: "12 34 56", geminiUpstream: true},
		{name: "gemini upstream native client", endpoint: "gemini", reply: "12 34 56", geminiUpstream: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan channelDiagnosticWire, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				w.Header().Set("Content-Type", "application/json")
				if tc.geminiUpstream {
					_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":%q}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`, tc.reply)
					return
				}
				if tc.messagesUpstream {
					_, _ = fmt.Fprintf(w, `{"id":"msg-fingerprint","type":"message","role":"assistant","model":"gpt-4.1","content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`, tc.reply)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":"resp-fingerprint","object":"response","status":"completed","model":"gpt-4.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`, tc.reply)
			}))
			defer upstream.Close()
			setting := `{"supported_endpoints":["responses"]}`
			if tc.messagesUpstream {
				setting = `{"supported_endpoints":["messages"]}`
			}
			override := `{"max_output_tokens":9000}`
			if tc.messagesUpstream || tc.endpoint == "openai" {
				override = `{"max_tokens":9000}`
			}
			selected := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "fingerprint-responses-" + tc.name, Key: "test-fingerprint-key", Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: &setting, ParamOverride: &override}
			if tc.geminiUpstream {
				selected.Type = constant.ChannelTypeGemini
				setting = ""
				override = `{"generationConfig":{"maxOutputTokens":9000}}`
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			result, err := runFingerprintProbe(t.Context(), selected, rootID, "gpt-4.1", tc.endpoint, lmfp.Challenge{Prompt: "fingerprint challenge", ExpectedCount: 3}, 300, service.FingerprintKeySelection{})
			if tc.wantErr {
				require.ErrorContains(t, err, "no text to score")
			} else {
				require.NoError(t, err)
			}
			wire := takeChannelDiagnostic(t, requests)
			limitField := "max_output_tokens"
			if tc.geminiUpstream {
				assert.Equal(t, "/v1beta/models/gpt-4.1:generateContent", wire.path)
				assert.Equal(t, "test-fingerprint-key", wire.headers.Get("x-goog-api-key"))
				assert.Equal(t, "fingerprint challenge", gjson.GetBytes(wire.body, "contents.0.parts.0.text").String())
				if tc.endpoint != "gemini" {
					assert.Len(t, gjson.GetBytes(wire.body, "safetySettings").Array(), 4, "converted requests retain configured closed-enum safety controls")
					assert.Equal(t, "OFF", gjson.GetBytes(wire.body, "safetySettings.0.threshold").String())
				}
				limitField = "generationConfig.maxOutputTokens"
			} else if tc.messagesUpstream {
				assert.Equal(t, "/v1/messages", wire.path)
				assert.Equal(t, "2023-06-01", wire.headers.Get("anthropic-version"))
				assert.True(t, gjson.GetBytes(wire.body, "messages").IsArray(), "Messages upstream receives a Claude body")
				assert.False(t, gjson.GetBytes(wire.body, "input").Exists())
				limitField = "max_tokens"
			} else {
				assert.Equal(t, "/v1/responses", wire.path)
			}
			if !tc.geminiUpstream {
				assert.Equal(t, "Bearer test-fingerprint-key", wire.headers.Get("Authorization"))
				assert.Empty(t, wire.headers.Get("x-api-key"), "the OpenAI channel retains its own authentication shape")
			}
			assert.Contains(t, string(wire.body), "fingerprint challenge")
			assert.NotContains(t, string(wire.body), "diagnostic system prompt", "unpreviewed system text is not part of a fingerprint probe")
			assert.EqualValues(t, 300, gjson.GetBytes(wire.body, limitField).Uint())
			assert.Equal(t, tc.reply, result.Text)
			assert.Equal(t, 10, result.PromptTokens)
			assert.Equal(t, 5, result.OutputTokens)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("channel_id = ?", selected.Id).Find(&logs).Error)
			require.Len(t, logs, 1, "the conversion reuses one test settlement")
			assert.Equal(t, 10, logs[0].PromptTokens)
			assert.Equal(t, 5, logs[0].CompletionTokens)
			assert.Positive(t, logs[0].Quota)
		})
	}
	t.Run("cancellation releases selected channel capacity", func(t *testing.T) {
		dispatched := make(chan struct{})
		upstreamCancelled := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			close(dispatched)
			<-r.Context().Done()
			close(upstreamCancelled)
		}))
		defer upstream.Close()
		setting := `{"supported_endpoints":["responses"],"max_concurrent_requests":1}`
		selected := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "fingerprint-cancel", Key: "test-fingerprint-key", Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: &setting}
		require.NoError(t, selected.Insert())
		t.Cleanup(func() { require.NoError(t, selected.Delete()) })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := runFingerprintProbe(ctx, selected, rootID, "gpt-4.1", "openai", lmfp.Challenge{Prompt: "fingerprint challenge", ExpectedCount: 3}, 300, service.FingerprintKeySelection{})
			done <- err
		}()
		select {
		case <-dispatched:
		case <-time.After(3 * time.Second):
			t.Fatal("probe did not reach the selected upstream")
		}
		sample := service.ReadChannelConcurrency(t.Context(), []int{selected.Id})[selected.Id]
		require.NotNil(t, sample.Current)
		assert.Equal(t, 1, *sample.Current)
		cancel()
		select {
		case err := <-done:
			require.Error(t, err)
			assert.NotContains(t, err.Error(), selected.Key)
		case <-time.After(3 * time.Second):
			t.Fatal("probe did not stop after cancellation")
		}
		select {
		case <-upstreamCancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("upstream request context was not cancelled")
		}
		sample = service.ReadChannelConcurrency(t.Context(), []int{selected.Id})[selected.Id]
		require.NotNil(t, sample.Current)
		assert.Zero(t, *sample.Current, "the aborted probe releases its concurrency slot")
		var logs int64
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("channel_id = ?", selected.Id).Count(&logs).Error)
		assert.Zero(t, logs, "a request aborted without usage does not record a successful test settlement")
	})
}

func setupChannelDiagnosticRequests(t *testing.T) int {
	t.Helper()
	require.NoError(t, i18n.Init())
	userID := setupChannelCreateTest(t)
	service.InitHttpClient()
	const modelRatios = `{"gpt-4.1":1,"gpt-5.1":1,"codex-fixture":1}`
	oldRatios, oldGroups := ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	oldLogs := common.LogConsumeEnabled
	settings := model_setting.GetGlobalSettings()
	previous := *settings
	t.Cleanup(func() {
		*settings = previous
		common.LogConsumeEnabled = oldLogs
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	settings.PassThroughRequestEnabled = false
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{}
	common.LogConsumeEnabled = true
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatios))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	return userID
}

const channelDiagnosticChatReply = `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4.1","choices":[{"index":0,"message":{"role":"assistant","content":"12 34 56"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
const channelDiagnosticResponsesReply = `{"id":"resp-test","object":"response","status":"completed","model":"gpt-4.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"12 34 56"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`

type channelDiagnosticWire struct {
	path    string
	headers http.Header
	body    []byte
}

func captureChannelDiagnostic(r *http.Request) channelDiagnosticWire {
	body, _ := io.ReadAll(r.Body)
	return channelDiagnosticWire{path: r.URL.Path, headers: r.Header.Clone(), body: body}
}

func takeChannelDiagnostic(t *testing.T, requests <-chan channelDiagnosticWire) channelDiagnosticWire {
	t.Helper()
	select {
	case wire := <-requests:
		return wire
	default:
		t.Fatal("no request reached the selected upstream")
		return channelDiagnosticWire{}
	}
}

func TestChannelTestStableKeyIdentityAdmissionFence(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	const firstKey, secondKey = "test-identity-first", "test-identity-second"
	requests := make(chan channelDiagnosticWire, 4)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		requests <- captureChannelDiagnostic(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, channelDiagnosticChatReply)
	}))
	t.Cleanup(upstream.Close)
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "diagnostic-stable-key", Key: firstKey + "\n" + secondKey,
		Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
		Setting:     lo.ToPtr(`{"max_concurrent_requests":1,"supported_endpoints":["chat"]}`),
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling},
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	selected, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	allocation, ok := selected.ChannelAllocationByPosition(1)
	require.True(t, ok)
	require.Positive(t, allocation.EntryID)
	require.Positive(t, allocation.Revision)
	id, revision := fmt.Sprint(allocation.EntryID), fmt.Sprint(allocation.Revision)
	pair := "key_entry_id=" + id + "&expected_rev=" + revision
	const staleMessage = "Channel key configuration changed, reload and retry"
	for _, tc := range []struct {
		name, query, wantKey, wantMessage string
	}{
		{name: "selected identity and index", query: "key_index=1&" + pair, wantKey: secondKey},
		{name: "identity without index", query: pair, wantKey: secondKey},
		{name: "legacy index only", query: "key_index=1", wantKey: secondKey},
		{name: "missing revision", query: "key_entry_id=" + id, wantMessage: "Invalid parameters"},
		{name: "missing identity", query: "expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "empty identity", query: "key_entry_id=&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "empty revision", query: "key_entry_id=" + id + "&expected_rev=", wantMessage: "Invalid parameters"},
		{name: "repeated identity", query: pair + "&key_entry_id=" + id, wantMessage: "Invalid parameters"},
		{name: "repeated revision", query: pair + "&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "invalid escapes must not become automatic selection", query: "key_entry_id=%ZZ&expected_rev=%ZZ", wantMessage: "Invalid parameters"},
		{name: "semicolon must not discard the identity pair", query: "key_entry_id=" + id + ";expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "malformed identity", query: "key_entry_id=invalid&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "malformed revision", query: "key_entry_id=" + id + "&expected_rev=1.0", wantMessage: "Invalid parameters"},
		{name: "noncanonical identity", query: "key_entry_id=0" + id + "&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "noncanonical revision", query: "key_entry_id=" + id + "&expected_rev=0" + revision, wantMessage: "Invalid parameters"},
		{name: "signed identity", query: "key_entry_id=%2B" + id + "&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "whitespace revision", query: "key_entry_id=" + id + "&expected_rev=%20" + revision, wantMessage: "Invalid parameters"},
		{name: "zero identity", query: "key_entry_id=0&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "zero revision", query: "key_entry_id=" + id + "&expected_rev=0", wantMessage: "Invalid parameters"},
		{name: "negative identity", query: "key_entry_id=-1&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "negative revision", query: "key_entry_id=" + id + "&expected_rev=-1", wantMessage: "Invalid parameters"},
		{name: "identity overflow", query: "key_entry_id=9223372036854775808&expected_rev=" + revision, wantMessage: "Invalid parameters"},
		{name: "revision overflow", query: "key_entry_id=" + id + "&expected_rev=9223372036854775808", wantMessage: "Invalid parameters"},
		{name: "stale revision", query: "key_entry_id=" + id + "&expected_rev=" + fmt.Sprint(allocation.Revision+1), wantMessage: staleMessage},
		{name: "missing allocation", query: "key_entry_id=9223372036854775807&expected_rev=" + revision, wantMessage: staleMessage},
		{name: "index belongs to another allocation", query: "key_index=0&" + pair, wantMessage: staleMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/channel/test/"+fmt.Sprint(selected.Id)+"?model=gpt-4.1&endpoint_type=openai&"+tc.query, nil)
			c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
			c.Set("id", userID)
			TestChannel(c)
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Success   bool            `json:"success"`
				Message   string          `json:"message"`
				ErrorCode types.ErrorCode `json:"error_code"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, tc.wantKey != "", response.Success, w.Body.String())
			assert.Equal(t, tc.wantMessage, response.Message)
			if tc.wantKey == "" {
				assert.Equal(t, before, calls.Load(), "invalid selections must not fall back to another credential")
				assert.Empty(t, requests)
				if tc.wantMessage == staleMessage {
					assert.Equal(t, types.ErrorCodeInvalidRequest, response.ErrorCode)
				}
				return
			}
			assert.Equal(t, before+1, calls.Load())
			wire := takeChannelDiagnostic(t, requests)
			assert.Equal(t, "/v1/chat/completions", wire.path)
			assert.Equal(t, "Bearer "+tc.wantKey, wire.headers.Get("Authorization"))
			assert.Equal(t, "gpt-4.1", gjson.GetBytes(wire.body, "model").String())
			assert.Empty(t, requests, "a selected-key test dispatches only once")
		})
	}

	t.Run("automatic selection remains unchanged", func(t *testing.T) {
		before := calls.Load()
		// Use the existing automatic caller, without the handler's asynchronous
		// channel-latency write, so teardown cannot race a detached DB update.
		result := testChannel(t.Context(), selected, userID, "gpt-4.1", "openai", false, -1)
		require.NoError(t, result.localErr)
		require.Nil(t, result.newAPIError)
		assert.Equal(t, before+1, calls.Load())
		wire := takeChannelDiagnostic(t, requests)
		assert.Equal(t, "Bearer "+firstKey, wire.headers.Get("Authorization"))
		assert.Empty(t, requests)
	})

	for _, tc := range []struct {
		name           string
		legacy, revise bool
	}{
		{name: "identity reorder after admission"},
		{name: "legacy index reorder after admission", legacy: true},
		{name: "configuration revision after admission", revise: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := model.GetChannelById(selected.Id, true)
			require.NoError(t, err)
			allocation, ok := snapshot.ChannelAllocationByPosition(1)
			require.True(t, ok)
			spec := channelTestSpec{
				userID: userID, model: "gpt-4.1", endpoint: "openai", keyIndex: 1,
				keyEntryID: allocation.EntryID, keyEntryRevision: allocation.Revision,
			}
			if tc.legacy {
				spec.keyEntryID, spec.keyEntryRevision = 0, 0
			}
			pinned, err := pinChannelTestKey(snapshot, spec.keyIndex, spec.keyEntryID, spec.keyEntryRevision, spec.model)
			require.NoError(t, err)
			require.NotNil(t, pinned)
			lease, apiErr := service.AcquireChannelCapacity(t.Context(), snapshot.Id, snapshot.GetSetting())
			require.Nil(t, apiErr)
			t.Cleanup(lease.Release)
			_, err = model.UpdateChannelKeyAllocations(snapshot.Id, snapshot.ChannelInfo.KeyEntryRevision, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
				if tc.revise {
					// Keep the same credential, index and permitted model. Only the
					// selected allocation's configuration revision becomes stale.
					entries[1].ScopeMode = "allowlist"
					if err := entries[1].SetKeyModels([]string{"gpt-4.1"}); err != nil {
						return nil, nil, err
					}
					entries[1].ConfigRevision++
				} else {
					entries[0], entries[1] = entries[1], entries[0]
				}
				return entries, nil, nil
			})
			require.NoError(t, err)
			current, err := model.GetChannelById(snapshot.Id, true)
			require.NoError(t, err)
			if tc.revise {
				require.Equal(t, allocation.EntryID, current.ChannelKeyEntryID(1))
				require.Equal(t, allocation.Key, current.GetKeys()[1])
				require.Greater(t, current.KeyEntryRevisions[1], allocation.Revision)
			} else {
				require.Equal(t, allocation.EntryID, current.ChannelKeyEntryID(0))
				require.Equal(t, allocation.Revision, current.KeyEntryRevisions[0])
				require.NotEqual(t, allocation.Key, current.GetKeys()[1])
			}
			before := calls.Load()
			result := runAdmittedChannelTest(t.Context(), lease, current, spec, pinned, true)
			require.EqualError(t, result.localErr, staleMessage)
			var keyErr *channelTestKeyError
			require.ErrorAs(t, result.localErr, &keyErr)
			assert.Equal(t, types.ErrorCodeInvalidRequest, keyErr.code)
			assert.Equal(t, before, calls.Load(), "the admitted request must not use the moved key or its replacement")
			assert.Empty(t, requests)
			sample := service.ReadChannelConcurrency(t.Context(), []int{selected.Id})[selected.Id]
			require.NotNil(t, sample.Current)
			assert.Zero(t, *sample.Current, "stale selection rejection releases the admitted capacity")
		})
	}
}

func TestChannelDiagnosticEndpointPolicy(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	for _, tc := range []struct {
		name, setting, endpoint, wantPath       string
		scheduled, passthrough, preferResponses bool
	}{
		{name: "manual auto", setting: `{"supported_endpoints":["responses"]}`, wantPath: "/v1/responses"},
		{name: "scheduled auto", setting: `{"supported_endpoints":["responses"]}`, scheduled: true, wantPath: "/v1/responses"},
		{name: "no declaration retains chat", setting: `{}`, wantPath: "/v1/chat/completions"},
		{name: "both follow global preference", setting: `{"supported_endpoints":["chat","responses"]}`, preferResponses: true, wantPath: "/v1/responses"},
		{name: "chat only overrides global preference", setting: `{"supported_endpoints":["chat"]}`, preferResponses: true, wantPath: "/v1/chat/completions"},
		{name: "explicit Responses cannot fall back to chat", setting: `{"supported_endpoints":["chat"]}`, endpoint: "openai-response"},
		{name: "explicit Gemini cannot upgrade to Responses", setting: `{"supported_endpoints":["responses"]}`, endpoint: "gemini"},
		{name: "automatic conversion conflicts with global passthrough", setting: `{"supported_endpoints":["responses"]}`, passthrough: true},
		{name: "invalid per-channel passthrough combination", setting: `{"supported_endpoints":["responses"],"pass_through_body_enabled":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := model_setting.GetGlobalSettings()
			previous := *settings
			t.Cleanup(func() { *settings = previous })
			settings.PassThroughRequestEnabled = tc.passthrough
			settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
				Enabled: tc.preferResponses, AllChannels: true, ModelPatterns: []string{"^gpt-4.1$"},
			}
			requests := make(chan channelDiagnosticWire, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/responses" {
					_, _ = io.WriteString(w, channelDiagnosticResponsesReply)
				} else {
					_, _ = io.WriteString(w, channelDiagnosticChatReply)
				}
			}))
			defer upstream.Close()
			selected := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "diagnostic-endpoint-policy", Key: "test-policy-key", Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: &tc.setting}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })

			if tc.scheduled {
				summary := testChannelForHealthCheck(t.Context(), selected, userID, true, 10000)
				assert.Equal(t, channelTestSummary{Tested: 1, Succeeded: 1}, summary)
			} else {
				result := testChannel(t.Context(), selected, userID, "gpt-4.1", tc.endpoint, false, -1)
				if tc.wantPath == "" {
					require.NotNil(t, result.newAPIError)
					assert.Equal(t, http.StatusBadRequest, result.newAPIError.StatusCode)
				} else {
					require.NoError(t, result.localErr)
				}
			}
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("channel_id = ? AND type = ?", selected.Id, model.LogTypeConsume).Find(&logs).Error)
			if tc.wantPath == "" {
				assert.Empty(t, requests, "unsupported requests are rejected before dispatch")
				assert.Empty(t, logs)
				return
			}
			wire := takeChannelDiagnostic(t, requests)
			assert.Equal(t, tc.wantPath, wire.path)
			assert.Contains(t, string(wire.body), `"hi"`)
			assert.Empty(t, requests)
			require.Len(t, logs, 1)
			assert.Equal(t, 10, logs[0].PromptTokens)
			assert.Equal(t, 5, logs[0].CompletionTokens)
		})
	}
}

const channelDiagnosticPlugins = `export const meta = {apiVersion: 1, key: "rp-diagnostic-wire", name: "Diagnostic wire", version: "1.0.0", author: {name: "Test"}};
export function request() {
  return {action: "continue", effects: [{type: "reasoning.set", effort: "high"}]};
}
export function outbound() {
  return {action: "continue", effects: [
    {type: "json.patch", ops: [{op: "add", path: "/metadata", value: {source: "plugin"}}]},
    {type: "header.patch", set: {"X-Plugin": "outbound", "X-Policy": "plugin"}}
  ]};
}
export function egress(ctx, req) {
  if (req.body.metadata && req.body.metadata.source === "override" && req.body.instructions === "channel prompt") {
    return {action: "continue"};
  }
  return {action: "reject", code: "policy.order", message: "Expected effective upstream body"};
}`

func TestChannelDiagnosticRequestPolicy(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	plugin, err := jsplugin.DefaultRegistry.Register(channelDiagnosticPlugins, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(plugin.Meta.Key) })
	previousInstances, err := common.Marshal(request_plugin_setting.GetInstances())
	require.NoError(t, err)
	setInstances := func(value string) {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{request_plugin_setting.InstancesOption: value}))
	}
	t.Cleanup(func() { setInstances(string(previousInstances)) })
	setInstances(`[{"id":"diagnostic-wire","name":"Diagnostic wire","plugin":"rp-diagnostic-wire","enabled":true}]`)
	requests := make(chan channelDiagnosticWire, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureChannelDiagnostic(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, channelDiagnosticResponsesReply)
	}))
	defer upstream.Close()
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "diagnostic-wire-policy", Key: "test-policy-key", Models: "gpt-4.1", Group: "default",
		Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
		Setting:        lo.ToPtr(`{"system_prompt":"channel prompt","request_plugins":["diagnostic-wire"]}`),
		ModelMapping:   lo.ToPtr(`{"gpt-4.1":"gpt-5.1"}`),
		ParamOverride:  lo.ToPtr(`{"metadata":{"source":"override"}}`),
		HeaderOverride: lo.ToPtr(`{"X-Policy":"static:{api_key}","X-Browser":"{client_header:Content-Type}"}`),
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	result := testChannel(t.Context(), selected, userID, "gpt-4.1", "openai-response", false, -1)
	require.NoError(t, result.localErr)
	wire := takeChannelDiagnostic(t, requests)
	assert.Equal(t, "/v1/responses", wire.path)
	assert.Equal(t, "gpt-5.1", gjson.GetBytes(wire.body, "model").String())
	assert.Equal(t, "high", gjson.GetBytes(wire.body, "reasoning.effort").String())
	assert.Equal(t, "channel prompt", gjson.GetBytes(wire.body, "instructions").String())
	assert.Equal(t, "override", gjson.GetBytes(wire.body, "metadata.source").String())
	assert.Equal(t, "outbound", wire.headers.Get("X-Plugin"))
	assert.Equal(t, "static:test-policy-key", wire.headers.Get("X-Policy"))
	assert.Empty(t, wire.headers.Get("X-Browser"))
}

func TestChannelDiagnosticBusyRetryAndProductiveFailure(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	previousStreamTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 15
	t.Cleanup(func() { constant.StreamingTimeout = previousStreamTimeout })
	const busy = `{"error":{"code":"concurrency_limit_exceeded","message":"Concurrency limit exceeded"}}`
	const partialAnswer = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
	const productive = partialAnswer + "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"partial\",\"status\":\"failed\",\"error\":{\"code\":\"concurrency_limit_exceeded\",\"message\":\"Concurrency limit exceeded\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n"
	const truncated = "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"partial\",\"status\":\"in_progress\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n" + partialAnswer
	for _, tc := range []struct {
		name, endpoint, failure           string
		stream, upstreamSSE, retry, usage bool
	}{
		{name: "Chat converted to Responses retries an HTTP refusal", failure: busy, retry: true, usage: true},
		{name: "native Responses retries an HTTP refusal", endpoint: "openai-response", failure: busy, retry: true, usage: true},
		{name: "generic 429 is not retried", failure: `{"error":{"message":"Too many requests"}}`},
		{name: "quota refusal is not retried", failure: `{"error":{"code":"insufficient_quota","message":"Out of quota"}}`},
		// Raw non-2xx envelope usage blocks replay but is not trusted for settlement.
		{name: "HTTP refusal carrying usage is not retried or settled", failure: `{"error":{"code":"concurrency_limit_exceeded","message":"Concurrency limit exceeded"},"usage":{"input_tokens":10}}`},
		{name: "productive native failure reports failure and settles once", endpoint: "openai-response", failure: productive, stream: true, upstreamSSE: true, usage: true},
		{name: "productive converted failure reports failure and settles once", failure: productive, stream: true, upstreamSSE: true, usage: true},
		{name: "truncated stream settles reported usage once", endpoint: "openai-response", failure: truncated, stream: true, upstreamSSE: true, usage: true},
		{name: "non-stream Chat settles reported usage on unsolicited SSE failure", endpoint: "openai", failure: productive, upstreamSSE: true, usage: true},
		{name: "non-stream Chat settles reported usage on unsolicited SSE truncation", endpoint: "openai", failure: truncated, upstreamSSE: true, usage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan channelDiagnosticWire, 4)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				attempt := calls.Add(1)
				if tc.upstreamSSE {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tc.failure)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if attempt == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, tc.failure)
					return
				}
				_, _ = io.WriteString(w, channelDiagnosticResponsesReply)
			}))
			defer upstream.Close()
			selected := &model.Channel{
				Type: constant.ChannelTypeOpenAI, Name: "diagnostic-busy-retry", Key: "test-key-one\ntest-key-two", Models: "gpt-4.1", Group: "default",
				Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: lo.ToPtr(`{"supported_endpoints":["responses"]}`),
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling},
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := testChannel(ctx, selected, userID, "gpt-4.1", tc.endpoint, tc.stream, -1)
			if tc.retry {
				require.NoError(t, result.localErr)
				assert.Equal(t, int32(2), calls.Load())
				first := takeChannelDiagnostic(t, requests)
				second := takeChannelDiagnostic(t, requests)
				assert.Equal(t, "/v1/responses", first.path)
				assert.Equal(t, first.path, second.path)
				assert.Equal(t, first.headers.Get("Authorization"), second.headers.Get("Authorization"), "the retry stays on its original key")
			} else {
				require.Error(t, result.localErr)
				assert.Equal(t, int32(1), calls.Load(), "never replay a productive or unclassified refusal")
				if tc.upstreamSSE && !tc.stream {
					wire := takeChannelDiagnostic(t, requests)
					assert.Equal(t, "/v1/responses", wire.path)
					assert.Equal(t, gjson.False, gjson.GetBytes(wire.body, "stream").Type)
				}
			}
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("channel_id = ? AND type = ?", selected.Id, model.LogTypeConsume).Find(&logs).Error)
			if tc.usage {
				assert.Equal(t, 10, result.promptTokens)
				assert.Equal(t, 5, result.completionTokens)
				require.Len(t, logs, 1)
				assert.Equal(t, 10, logs[0].PromptTokens)
				assert.Equal(t, 5, logs[0].CompletionTokens)
				assert.Positive(t, logs[0].Quota)
			} else {
				assert.Empty(t, logs)
			}
		})
	}
}

func TestChannelDiagnosticReadmissionUsesCurrentCapAndPolicy(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	type diagnosticRequest struct{}
	const callback = "test:channel_diagnostic_after_grant"
	for _, cancelQueued := range []bool{false, true} {
		name := "resume with current policy"
		if cancelQueued {
			name = "cancel instead of dispatching over the reduced cap"
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan channelDiagnosticWire, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, channelDiagnosticResponsesReply)
			}))
			t.Cleanup(upstream.Close)
			selected := &model.Channel{
				Type: constant.ChannelTypeOpenAI, Name: "diagnostic-current-cap", Key: "old-key", Models: "gpt-4.1", Group: "default",
				Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: lo.ToPtr(`{"max_concurrent_requests":2,"supported_endpoints":["chat"]}`),
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			first, apiErr := service.AcquireChannelCapacity(t.Context(), selected.Id, selected.GetSetting())
			require.Nil(t, apiErr)
			t.Cleanup(first.Release)
			second, apiErr := service.AcquireChannelCapacity(t.Context(), selected.Id, selected.GetSetting())
			require.Nil(t, apiErr)
			t.Cleanup(second.Release)

			// Observe the real post-grant DB read. The attempt must release that
			// lease before reaching upstream, even while the second holder stays active.
			reads := make(chan context.Context, 2)
			queries := model.DB.Callback().Query()
			require.NoError(t, queries.After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(*model.Channel); ok && row.Id == selected.Id && tx.Statement.Context.Value(diagnosticRequest{}) != nil {
					select {
					case reads <- tx.Statement.Context:
					case <-tx.Statement.Context.Done():
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, queries.Remove(callback)) })
			ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), diagnosticRequest{}, true), 10*time.Second)
			done := make(chan testResult, 1)
			finished := make(chan struct{})
			t.Cleanup(func() { cancel(); <-finished })
			go func() {
				defer close(finished)
				done <- testChannel(ctx, selected, userID, "gpt-4.1", "", false, -1)
			}()

			// The credential must change through the authoritative write path: a raw
			// channels.key update would leave the allocation rows (and the
			// projection read from them) on the old credential.
			_, err := model.UpdateChannelLocked(selected.Id, func(stored *model.Channel) error {
				stored.Key = "new-key"
				return nil
			})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", selected.Id).Updates(map[string]any{
				"setting": `{"max_concurrent_requests":1,"supported_endpoints":["responses"]}`, "header_override": `{"X-Policy":"new:{api_key}"}`,
			}).Error)
			first.Release() // one slot under the old cap, none under the new cap
			var staleLease context.Context
			select {
			case staleLease = <-reads:
			case <-ctx.Done():
				t.Fatal("no reload after the first grant")
			}
			select {
			case <-staleLease.Done():
			case <-ctx.Done():
				t.Fatal("the stale lease was not released")
			}
			require.NoError(t, ctx.Err())
			assert.Empty(t, requests, "the stale grant must not send a request")
			if cancelQueued {
				cancel()
			} else {
				second.Release()
			}
			select {
			case result := <-done:
				if cancelQueued {
					require.ErrorIs(t, result.localErr, context.Canceled)
					assert.Empty(t, requests)
					second.Release()
				} else {
					require.NoError(t, result.localErr)
					wire := takeChannelDiagnostic(t, requests)
					assert.Equal(t, "/v1/responses", wire.path)
					assert.Equal(t, "Bearer new-key", wire.headers.Get("Authorization"))
					assert.Equal(t, "new:new-key", wire.headers.Get("X-Policy"))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the diagnostic did not finish after release or cancellation")
			}
			sample := service.ReadChannelConcurrency(t.Context(), []int{selected.Id})[selected.Id]
			require.NotNil(t, sample.Current)
			assert.Zero(t, *sample.Current)
		})
	}
}

func TestFingerprintDiagnosticReloadsPolicyBetweenProbes(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	requests := make(chan channelDiagnosticWire, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureChannelDiagnostic(r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses" {
			_, _ = io.WriteString(w, channelDiagnosticResponsesReply)
		} else {
			_, _ = io.WriteString(w, channelDiagnosticChatReply)
		}
	}))
	defer upstream.Close()
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "diagnostic-fingerprint-policy", Key: "test-key-one", Models: "gpt-4.1", Group: "default",
		Status: common.ChannelStatusManuallyDisabled, BaseURL: &upstream.URL, Setting: lo.ToPtr(`{"supported_endpoints":["chat"]}`),
		HeaderOverride: lo.ToPtr(`{"X-Policy":"one:{api_key}"}`),
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	probe := func() (service.FingerprintProbeResult, error) {
		return runFingerprintProbe(t.Context(), selected, userID, "gpt-4.1", "", lmfp.Challenge{Prompt: "current-policy challenge", ExpectedCount: 3}, 300, service.FingerprintKeySelection{})
	}
	first, err := probe()
	require.NoError(t, err)
	assert.Equal(t, "12 34 56", first.Text)
	wire := takeChannelDiagnostic(t, requests)
	assert.Equal(t, "/v1/chat/completions", wire.path)
	assert.Equal(t, "Bearer test-key-one", wire.headers.Get("Authorization"))
	assert.Equal(t, "one:test-key-one", wire.headers.Get("X-Policy"))

	stored := model.DB.Model(&model.Channel{}).Where("id = ?", selected.Id)
	// Rotation goes through the authoritative path; the setting, header
	// override, and model list are ordinary fields and keep the direct update.
	_, err = model.UpdateChannelLocked(selected.Id, func(current *model.Channel) error {
		current.Key = "test-key-two"
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, stored.Updates(map[string]any{
		"setting": `{"supported_endpoints":["responses"]}`, "header_override": `{"X-Policy":"two:{api_key}"}`,
	}).Error)
	second, err := probe()
	require.NoError(t, err)
	assert.Equal(t, "12 34 56", second.Text)
	wire = takeChannelDiagnostic(t, requests)
	assert.Equal(t, "/v1/responses", wire.path)
	assert.Equal(t, "Bearer test-key-two", wire.headers.Get("Authorization"))
	assert.Equal(t, "two:test-key-two", wire.headers.Get("X-Policy"))
	assert.Contains(t, string(wire.body), "current-policy challenge")

	require.NoError(t, stored.Update("models", "another-model").Error)
	_, err = probe()
	require.ErrorContains(t, err, "not available on this channel")
	require.NoError(t, selected.Delete())
	_, err = probe()
	require.Error(t, err)
	assert.Empty(t, requests, "a removed model or channel prevents any upstream request")
}

func TestFingerprintOptionsReflectEligibleKeyModels(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	keys := []string{"sk-fingerprint-key-b", "sk-fingerprint-key-a", "sk-fingerprint-disabled", "sk-fingerprint-deny-all"}
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "fingerprint-options", Key: strings.Join(keys, "\n"),
		Models: "gpt-4.1,gpt-5.1,codex-fixture,unserved-model", Group: "default", Status: common.ChannelStatusEnabled,
		ModelMapping: lo.ToPtr(`{"gpt-4.1":"DeepSeek-R1"}`),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true, MultiKeySize: len(keys), MultiKeyMode: constant.MultiKeyModePolling,
			MultiKeyScopeModes: map[int]string{0: "allowlist", 1: "allowlist", 2: "allowlist", 3: "allowlist"},
			MultiKeyModels:     map[int][]string{0: {"gpt-5.1"}, 1: {"DeepSeek-R1"}, 2: {"codex-fixture"}, 3: {}},
			MultiKeyStatusList: map[int]int{2: common.ChannelStatusManuallyDisabled},
		},
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	selected, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?page_size=50", nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
	c.Set("id", userID)
	GetChannelFingerprintOptions(c)
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Models     []string    `json:"models"`
			AutoModels []string    `json:"auto_models"`
			Keys       []KeyStatus `json:"keys"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	assert.Equal(t, []string{"gpt-4.1", "gpt-5.1", "codex-fixture", "unserved-model"}, response.Data.Models)
	assert.Equal(t, []string{"gpt-4.1", "gpt-5.1"}, response.Data.AutoModels, "disabled and deny-all Keys must not advertise an automatic candidate")
	require.Len(t, response.Data.Keys, len(keys))
	wantModels := [][]string{{"gpt-5.1"}, {"gpt-4.1"}, {"codex-fixture"}, nil}
	for index, item := range response.Data.Keys {
		allocation, ok := selected.ChannelAllocationByPosition(index)
		require.True(t, ok)
		assert.Equal(t, fmt.Sprint(allocation.EntryID), item.Id)
		assert.Equal(t, allocation.Revision, item.Rev)
		assert.Equal(t, index, item.Index)
		assert.Equal(t, wantModels[index], item.Models, "Key models use the channel's request aliases")
		assert.Equal(t, "allowlist", item.ScopeMode)
		assert.NotEmpty(t, item.KeyPreview)
		assert.NotContains(t, w.Body.String(), keys[index])
	}
	assert.Equal(t, common.ChannelStatusManuallyDisabled, response.Data.Keys[2].Status)
}

func TestFingerprintAutomaticPreviewDispatchesOnlyModelEligibleKey(t *testing.T) {
	for _, tc := range []struct {
		name, mapping, keyModel, upstreamModel string
	}{
		{name: "direct model scope", keyModel: "gpt-4.1", upstreamModel: "gpt-4.1"},
		{name: "mapped upstream scope", mapping: `{"gpt-4.1":"DeepSeek-R1"}`, keyModel: "DeepSeek-R1", upstreamModel: "DeepSeek-R1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userID := setupChannelDiagnosticRequests(t)
			requests := make(chan channelDiagnosticWire, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, channelDiagnosticChatReply)
			}))
			t.Cleanup(upstream.Close)
			selected := &model.Channel{
				Type: constant.ChannelTypeOpenAI, Name: "fingerprint-auto-key", Key: "auto-key-b\nauto-key-a",
				Models: "gpt-4.1,gpt-5.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
				ModelMapping: lo.ToPtr(tc.mapping), Setting: lo.ToPtr(`{"supported_endpoints":["chat"]}`),
				ChannelInfo: model.ChannelInfo{
					IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling,
					MultiKeyScopeModes: map[int]string{0: "allowlist", 1: "allowlist"},
					MultiKeyModels:     map[int][]string{0: {"gpt-5.1"}, 1: {tc.keyModel}},
				},
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			payload := fingerprintProbeRequest{Model: "gpt-4.1", EndpointType: "anthropic", Count: 1}
			invoke := func(handler gin.HandlerFunc) *httptest.ResponseRecorder {
				encoded, err := common.Marshal(payload)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(string(encoded)))
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
				c.Set("id", userID)
				handler(c)
				return w
			}
			previewResponse := invoke(PreviewChannelFingerprint)
			require.True(t, gjson.Get(previewResponse.Body.String(), "success").Bool(), previewResponse.Body.String())
			var envelope struct {
				Data service.FingerprintPreview `json:"data"`
			}
			require.NoError(t, common.Unmarshal(previewResponse.Body.Bytes(), &envelope))
			require.Len(t, envelope.Data.Probes, 1)
			assert.Empty(t, requests, "preview must not test any Key")
			payload.PreviewID = envelope.Data.PreviewID
			payload.EndpointType = "openai-response"
			started := invoke(StartChannelFingerprint)
			require.True(t, gjson.Get(started.Body.String(), "success").Bool(), started.Body.String())
			jobID := gjson.Get(started.Body.String(), "data.id").String()
			require.NotEmpty(t, jobID)
			t.Cleanup(func() {
				_, _ = service.CancelChannelFingerprint(jobID)
				require.Eventually(t, func() bool {
					job, err := service.GetChannelFingerprint(jobID)
					return err != nil || job.FinishedAt != 0
				}, 5*time.Second, 10*time.Millisecond, "stop diagnostic work before fixture DB/global teardown")
			})
			require.Eventually(t, func() bool {
				job, err := service.GetChannelFingerprint(jobID)
				return err == nil && job.FinishedAt != 0
			}, 5*time.Second, 10*time.Millisecond)
			job, err := service.GetChannelFingerprint(jobID)
			require.NoError(t, err)
			require.Len(t, job.Probes, 1)
			require.Equal(t, "complete", job.Probes[0].State, job.Probes[0].Error)
			assert.Equal(t, 1, job.RequestsUsed)
			wire := takeChannelDiagnostic(t, requests)
			assert.Equal(t, "Bearer auto-key-a", wire.headers.Get("Authorization"), "polling must skip the first Key when it cannot serve this model")
			assert.Equal(t, "/v1/chat/completions", wire.path, "saved channel endpoints override legacy request fields")
			assert.Equal(t, tc.upstreamModel, gjson.GetBytes(wire.body, "model").String())
			assert.Equal(t, envelope.Data.Probes[0].Prompt, gjson.GetBytes(wire.body, "messages.0.content").String())
			assert.Empty(t, requests, "automatic routing dispatches only the eligible Key")
		})
	}
}

func TestFingerprintPreviewHTTPRequiredContextAndExactDispatch(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	requests := make(chan channelDiagnosticWire, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureChannelDiagnostic(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, channelDiagnosticChatReply)
	}))
	t.Cleanup(upstream.Close)
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "fingerprint-preview-http", Key: "preview-first\npreview-selected",
		Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
		Setting:     lo.ToPtr(`{"supported_endpoints":["chat"]}`),
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling},
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	selected, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	allocation, ok := selected.ChannelAllocationByPosition(1)
	require.True(t, ok)
	payload := fingerprintProbeRequest{
		Model: "gpt-4.1", EndpointType: "openai", Count: 1,
		KeyEntryID: lo.ToPtr(fmt.Sprint(allocation.EntryID)), ExpectedRev: lo.ToPtr(allocation.Revision), KeyIndex: lo.ToPtr(1),
	}
	invoke := func(handler gin.HandlerFunc, owner int, input fingerprintProbeRequest) *httptest.ResponseRecorder {
		encoded, err := common.Marshal(struct {
			fingerprintProbeRequest
			TokenBudget int    `json:"token_budget"`
			Prompt      string `json:"prompt"`
		}{fingerprintProbeRequest: input, TokenBudget: 1, Prompt: "client prompt must not replace the preview"})
		require.NoError(t, err)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(string(encoded)))
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
		c.Set("id", owner)
		handler(c)
		return w
	}
	missing := invoke(StartChannelFingerprint, userID, payload)
	assert.False(t, gjson.Get(missing.Body.String(), "success").Bool())
	assert.Equal(t, "a fingerprint preview is required", gjson.Get(missing.Body.String(), "message").String())
	assert.Empty(t, requests)

	response := invoke(PreviewChannelFingerprint, userID, payload)
	require.True(t, gjson.Get(response.Body.String(), "success").Bool(), response.Body.String())
	var envelope struct {
		Data service.FingerprintPreview `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
	preview := envelope.Data
	require.Len(t, preview.Probes, 1)
	assert.Empty(t, requests, "preview never sends an upstream request")
	assert.NotEmpty(t, preview.Probes[0].Prompt)
	assert.NotContains(t, response.Body.String(), "preview-selected")
	payload.PreviewID = preview.PreviewID
	for _, changed := range []fingerprintProbeRequest{
		{PreviewID: preview.PreviewID, Model: payload.Model, EndpointType: payload.EndpointType, Count: 2, KeyEntryID: payload.KeyEntryID, ExpectedRev: payload.ExpectedRev, KeyIndex: payload.KeyIndex},
		{PreviewID: preview.PreviewID, Model: payload.Model, EndpointType: payload.EndpointType, Count: 1},
	} {
		mismatched := invoke(StartChannelFingerprint, userID, changed)
		assert.False(t, gjson.Get(mismatched.Body.String(), "success").Bool())
		assert.Empty(t, requests, "a changed selection must not start this preview")
	}
	wrongOwner := invoke(StartChannelFingerprint, userID+1, payload)
	assert.False(t, gjson.Get(wrongOwner.Body.String(), "success").Bool())
	assert.Equal(t, "fingerprint preview is unavailable or expired", gjson.Get(wrongOwner.Body.String(), "message").String())
	assert.Empty(t, requests)

	started := invoke(StartChannelFingerprint, userID, payload)
	require.True(t, gjson.Get(started.Body.String(), "success").Bool(), started.Body.String())
	jobID := gjson.Get(started.Body.String(), "data.id").String()
	require.NotEmpty(t, jobID)
	t.Cleanup(func() {
		_, _ = service.CancelChannelFingerprint(jobID)
		require.Eventually(t, func() bool {
			job, err := service.GetChannelFingerprint(jobID)
			return err != nil || job.FinishedAt != 0
		}, 5*time.Second, 10*time.Millisecond, "stop diagnostic work before fixture DB/global teardown")
	})
	var wire channelDiagnosticWire
	select {
	case wire = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("preview job did not dispatch")
	}
	assert.Equal(t, "Bearer preview-selected", wire.headers.Get("Authorization"))
	assert.Equal(t, "/v1/chat/completions", wire.path)
	assert.Equal(t, preview.Probes[0].Prompt, gjson.GetBytes(wire.body, "messages.0.content").String())
	assert.EqualValues(t, 4096, gjson.GetBytes(wire.body, "max_tokens").Uint())
	assert.EqualValues(t, 24000, gjson.Get(started.Body.String(), "data.token_budget").Int(), "legacy client budget fields cannot override the server budget")
	assert.Equal(t, preview.Probes[0].ID, gjson.Get(started.Body.String(), "data.probes.0.id").String())
	require.Eventually(t, func() bool {
		job, err := service.GetChannelFingerprint(jobID)
		return err == nil && job.FinishedAt != 0
	}, 5*time.Second, 10*time.Millisecond)
	// Endpoint selection is server-owned; legacy request fields cannot change
	// the accepted run or authorize a second dispatch on a different endpoint.
	payload.EndpointType = "openai-response"
	replayed := invoke(StartChannelFingerprint, userID, payload)
	require.True(t, gjson.Get(replayed.Body.String(), "success").Bool(), replayed.Body.String())
	assert.Equal(t, jobID, gjson.Get(replayed.Body.String(), "data.id").String())
	assert.Empty(t, requests, "repeated HTTP Start never reruns a paid sample")
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("channel_id = ?", selected.Id).Find(&logs).Error)
	assert.Len(t, logs, 1, "preview/start/retry uses one existing diagnostic settlement")

	// A fresh preview must also reject a Key that becomes stale before Start.
	fresh := invoke(PreviewChannelFingerprint, userID, fingerprintProbeRequest{
		Model: payload.Model, EndpointType: payload.EndpointType, Count: 1,
		KeyEntryID: payload.KeyEntryID, ExpectedRev: payload.ExpectedRev, KeyIndex: payload.KeyIndex,
	})
	require.True(t, gjson.Get(fresh.Body.String(), "success").Bool(), fresh.Body.String())
	payload.PreviewID = gjson.Get(fresh.Body.String(), "data.preview_id").String()
	_, err = model.UpdateChannelKeyAllocations(selected.Id, selected.ChannelInfo.KeyEntryRevision, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
		entries[1].ScopeMode = "allowlist"
		entries[1].Models = "[]"
		entries[1].ConfigRevision++
		return entries, nil, nil
	})
	require.NoError(t, err)
	stale := invoke(StartChannelFingerprint, userID, payload)
	assert.False(t, gjson.Get(stale.Body.String(), "success").Bool())
	assert.Equal(t, "the selected key is unavailable for this model", gjson.Get(stale.Body.String(), "message").String())
	assert.Empty(t, requests, "a stale selected Key cannot be replaced by the automatic pool")
}

func TestFingerprintRetryHTTPPreservesSuccessfulSamplesAndSelection(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	requests := make(chan channelDiagnosticWire, 8)
	var calls atomic.Int32
	retryRunning := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureChannelDiagnostic(r)
		attempt := calls.Add(1)
		if attempt == 5 {
			close(retryRunning)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if attempt == 2 || attempt == 3 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"message":"untrusted secret-do-not-reflect","code":"secret-do-not-reflect"}}`)
			return
		}
		_, _ = io.WriteString(w, channelDiagnosticChatReply)
	}))
	t.Cleanup(upstream.Close)
	selected := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "fingerprint-retry-http", Key: "retry-first\nretry-selected",
		Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling},
	}
	require.NoError(t, selected.Insert())
	t.Cleanup(func() { require.NoError(t, selected.Delete()) })
	selected, err := model.GetChannelById(selected.Id, true)
	require.NoError(t, err)
	allocation, ok := selected.ChannelAllocationByPosition(1)
	require.True(t, ok)
	payload := fingerprintProbeRequest{
		Model: "gpt-4.1", EndpointType: "openai", Count: 3,
		KeyEntryID: lo.ToPtr(fmt.Sprint(allocation.EntryID)), ExpectedRev: lo.ToPtr(allocation.Revision), KeyIndex: lo.ToPtr(1),
	}
	invoke := func(handler gin.HandlerFunc, owner int, channelID int, jobID string, input any) *httptest.ResponseRecorder {
		encoded, err := common.Marshal(input)
		require.NoError(t, err)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(string(encoded)))
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channelID)}, {Key: "job_id", Value: jobID}}
		c.Set("id", owner)
		handler(c)
		assert.NotContains(t, w.Body.String(), "secret-do-not-reflect")
		return w
	}
	previewResponse := invoke(PreviewChannelFingerprint, userID, selected.Id, "", payload)
	require.True(t, gjson.Get(previewResponse.Body.String(), "success").Bool(), previewResponse.Body.String())
	var envelope struct {
		Data service.FingerprintPreview `json:"data"`
	}
	require.NoError(t, common.Unmarshal(previewResponse.Body.Bytes(), &envelope))
	preview := envelope.Data
	require.Len(t, preview.Probes, 3)
	payload.PreviewID = preview.PreviewID
	started := invoke(StartChannelFingerprint, userID, selected.Id, "", payload)
	require.True(t, gjson.Get(started.Body.String(), "success").Bool(), started.Body.String())
	jobID := gjson.Get(started.Body.String(), "data.id").String()
	require.NotEmpty(t, jobID)
	t.Cleanup(func() {
		_, _ = service.CancelChannelFingerprint(jobID)
		require.Eventually(t, func() bool {
			job, err := service.GetChannelFingerprint(jobID)
			return err != nil || job.FinishedAt != 0
		}, 5*time.Second, 10*time.Millisecond)
	})
	require.Eventually(t, func() bool {
		job, err := service.GetChannelFingerprint(jobID)
		return err == nil && job.FinishedAt != 0
	}, 5*time.Second, 10*time.Millisecond)
	before, err := service.GetChannelFingerprint(jobID)
	require.NoError(t, err)
	require.Equal(t, "complete", before.Probes[0].State)
	require.True(t, before.Probes[1].RetryAllowed)
	for _, sample := range preview.Probes {
		wire := takeChannelDiagnostic(t, requests)
		assert.Equal(t, sample.Prompt, gjson.GetBytes(wire.body, "messages.0.content").String())
		assert.Equal(t, "Bearer retry-selected", wire.headers.Get("Authorization"))
	}
	payload.ProbeID, payload.ExpectedAttempt = preview.Probes[1].ID, 1
	for _, tc := range []struct {
		name    string
		owner   int
		channel int
		job     string
		input   fingerprintProbeRequest
	}{
		{name: "wrong owner", owner: userID + 1, channel: selected.Id, job: jobID, input: payload},
		{name: "wrong channel", owner: userID, channel: selected.Id + 1, job: jobID, input: payload},
		{name: "wrong job", owner: userID, channel: selected.Id, job: "other-job", input: payload},
		{name: "successful sample", owner: userID, channel: selected.Id, job: jobID, input: fingerprintProbeRequest{
			PreviewID: payload.PreviewID, Model: payload.Model, EndpointType: payload.EndpointType, Count: payload.Count,
			KeyEntryID: payload.KeyEntryID, ExpectedRev: payload.ExpectedRev, KeyIndex: payload.KeyIndex, ProbeID: preview.Probes[0].ID, ExpectedAttempt: 1,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := invoke(RetryChannelFingerprintProbe, tc.owner, tc.channel, tc.job, tc.input)
			assert.False(t, gjson.Get(response.Body.String(), "success").Bool())
			assert.EqualValues(t, 3, calls.Load())
			assert.Empty(t, requests)
		})
	}
	retried := invoke(RetryChannelFingerprintProbe, userID, selected.Id, jobID, payload)
	require.True(t, gjson.Get(retried.Body.String(), "success").Bool(), retried.Body.String())
	require.Eventually(t, func() bool {
		job, err := service.GetChannelFingerprint(jobID)
		return err == nil && job.FinishedAt != 0
	}, 5*time.Second, 10*time.Millisecond)
	wire := takeChannelDiagnostic(t, requests)
	assert.Equal(t, preview.Probes[1].Prompt, gjson.GetBytes(wire.body, "messages.0.content").String())
	assert.Equal(t, "Bearer retry-selected", wire.headers.Get("Authorization"))
	assert.EqualValues(t, 4096, gjson.GetBytes(wire.body, "max_tokens").Uint())
	final, err := service.GetChannelFingerprint(jobID)
	require.NoError(t, err)
	assert.Equal(t, before.Probes[0], final.Probes[0], "the successful paid sample is untouched")
	assert.Equal(t, "complete", final.Probes[1].State)
	assert.Equal(t, 2, final.Probes[1].Attempt)
	assert.Equal(t, before.Revision+1, final.Revision)
	assert.Equal(t, 24000, final.TokenBudget)
	assert.Greater(t, final.TokensUsed, before.TokensUsed)
	assert.Equal(t, 4, final.RequestsUsed)
	replay := invoke(RetryChannelFingerprintProbe, userID, selected.Id, jobID, payload)
	require.True(t, gjson.Get(replay.Body.String(), "success").Bool(), replay.Body.String())
	assert.EqualValues(t, final.Revision, gjson.Get(replay.Body.String(), "data.revision").Int())
	assert.EqualValues(t, 4, calls.Load(), "lost response retries dispatch no additional work")
	assert.Empty(t, requests)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("channel_id = ? AND type = ?", selected.Id, model.LogTypeConsume).Find(&logs).Error)
	assert.Len(t, logs, 2, "only the two successful attempts settle once each")

	// A stale row revision is rejected before a new attempt is admitted. The
	// current revision remains available for the explicit cancellation case.
	payload.ProbeID = preview.Probes[2].ID
	payload.ExpectedRev = lo.ToPtr(allocation.Revision + 1)
	staleRevision := invoke(RetryChannelFingerprintProbe, userID, selected.Id, jobID, payload)
	assert.False(t, gjson.Get(staleRevision.Body.String(), "success").Bool())
	assert.Equal(t, "the selected key is unavailable for this model", gjson.Get(staleRevision.Body.String(), "message").String())
	assert.EqualValues(t, 4, calls.Load())
	payload.ExpectedRev = lo.ToPtr(allocation.Revision)

	inFlight := invoke(RetryChannelFingerprintProbe, userID, selected.Id, jobID, payload)
	require.True(t, gjson.Get(inFlight.Body.String(), "success").Bool(), inFlight.Body.String())
	select {
	case <-retryRunning:
	case <-time.After(5 * time.Second):
		t.Fatal("second sample retry did not start")
	}
	for _, fence := range []any{map[string]int{"expected_revision": 1}, map[string]int{}} {
		staleCancel := invoke(CancelChannelFingerprint, userID, selected.Id, jobID, fence)
		assert.Equal(t, http.StatusConflict, staleCancel.Code)
		current, err := service.GetChannelFingerprint(jobID)
		require.NoError(t, err)
		assert.Equal(t, "running", current.State, "stale/unfenced cancellation cannot stop a newer retry")
	}
	cancelled := invoke(CancelChannelFingerprint, userID, selected.Id, jobID, map[string]int{"expected_revision": final.Revision + 1})
	require.True(t, gjson.Get(cancelled.Body.String(), "success").Bool(), cancelled.Body.String())
	require.Eventually(t, func() bool {
		job, err := service.GetChannelFingerprint(jobID)
		return err == nil && job.State == "cancelled" && job.FinishedAt != 0
	}, 5*time.Second, 10*time.Millisecond)
	wire = takeChannelDiagnostic(t, requests)
	assert.Equal(t, preview.Probes[2].Prompt, gjson.GetBytes(wire.body, "messages.0.content").String())
	assert.Equal(t, "Bearer retry-selected", wire.headers.Get("Authorization"))

	// A retry request cannot fall back after its selected Key changes.
	_, err = model.UpdateChannelKeyAllocations(selected.Id, selected.ChannelInfo.KeyEntryRevision, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
		entries[1].ConfigRevision++
		return entries, nil, nil
	})
	require.NoError(t, err)
	payload.ProbeID = preview.Probes[2].ID
	stale := invoke(RetryChannelFingerprintProbe, userID, selected.Id, jobID, payload)
	assert.False(t, gjson.Get(stale.Body.String(), "success").Bool())
	assert.Equal(t, "the selected key is unavailable for this model", gjson.Get(stale.Body.String(), "message").String())
	assert.EqualValues(t, 5, calls.Load())
	assert.Empty(t, requests)
}

func TestFingerprintPreviewPromptMutationRejectedBeforeDispatch(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	for _, tc := range []struct {
		name, endpoint, setting, override string
	}{
		{name: "Chat replaces user text", endpoint: "openai", override: `{"messages":[{"role":"user","content":"replacement prompt"}]}`},
		{name: "Chat removes user text", endpoint: "openai", override: `{"messages":[]}`},
		{name: "Chat adds system text", endpoint: "openai", setting: `{"system_prompt":"unpreviewed instruction"}`},
		{name: "Chat to Responses replaces user text before conversion", endpoint: "openai", setting: `{"supported_endpoints":["responses"]}`, override: `{"messages":[{"role":"user","content":"replacement prompt"}]}`},
		{name: "Chat to Responses adds system instructions", endpoint: "openai", setting: `{"supported_endpoints":["responses"],"system_prompt":"unpreviewed instruction"}`},
		{name: "Responses replaces input", endpoint: "openai-response", setting: `{"supported_endpoints":["responses"]}`, override: `{"input":"replacement prompt"}`},
		{name: "Responses adds hidden history", endpoint: "openai-response", setting: `{"supported_endpoints":["responses"]}`, override: `{"previous_response_id":"unpreviewed-history"}`},
		{name: "Responses to Messages replaces converted text", endpoint: "openai-response", setting: `{"supported_endpoints":["messages"]}`, override: `{"messages":[{"role":"user","content":[{"type":"text","text":"replacement prompt"}]}]}`},
		{name: "Chat to Messages adds hidden system", endpoint: "openai", setting: `{"supported_endpoints":["messages"],"system_prompt":"unpreviewed instruction"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, channelDiagnosticChatReply)
			}))
			t.Cleanup(upstream.Close)
			selected := &model.Channel{
				Type: constant.ChannelTypeOpenAI, Name: "fingerprint-prompt-integrity", Key: "test-prompt-integrity-key",
				Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
				Setting: &tc.setting, ParamOverride: &tc.override,
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			payload := fingerprintProbeRequest{Model: "gpt-4.1", EndpointType: tc.endpoint, Count: 1}
			invoke := func(handler gin.HandlerFunc, jobID string) *httptest.ResponseRecorder {
				encoded, err := common.Marshal(payload)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(string(encoded)))
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
				if jobID != "" {
					c.Params = append(c.Params, gin.Param{Key: "job_id", Value: jobID})
				}
				c.Set("id", userID)
				handler(c)
				return w
			}
			preview := invoke(PreviewChannelFingerprint, "")
			require.True(t, gjson.Get(preview.Body.String(), "success").Bool(), preview.Body.String())
			prompt := gjson.Get(preview.Body.String(), "data.probes.0.prompt").String()
			require.NotEmpty(t, prompt)
			payload.PreviewID = gjson.Get(preview.Body.String(), "data.preview_id").String()
			started := invoke(StartChannelFingerprint, "")
			require.True(t, gjson.Get(started.Body.String(), "success").Bool(), started.Body.String())
			jobID := gjson.Get(started.Body.String(), "data.id").String()
			require.NotEmpty(t, jobID)
			t.Cleanup(func() {
				_, _ = service.CancelChannelFingerprint(jobID)
				require.Eventually(t, func() bool {
					job, err := service.GetChannelFingerprint(jobID)
					return err != nil || job.FinishedAt != 0
				}, 5*time.Second, 10*time.Millisecond)
			})
			require.Eventually(t, func() bool {
				job, err := service.GetChannelFingerprint(jobID)
				return err == nil && job.FinishedAt != 0
			}, 5*time.Second, 10*time.Millisecond)
			read := invoke(GetChannelFingerprint, jobID)
			repeated := invoke(StartChannelFingerprint, "")
			for _, response := range []*httptest.ResponseRecorder{read, repeated} {
				body := response.Body.String()
				require.True(t, gjson.Get(body, "success").Bool(), body)
				assert.Equal(t, jobID, gjson.Get(body, "data.id").String())
				assert.Equal(t, "failed", gjson.Get(body, "data.state").String())
				assert.Equal(t, errFingerprintWirePromptChanged.Error(), gjson.Get(body, "data.probes.0.error").String())
				assert.Equal(t, prompt, gjson.Get(body, "data.probes.0.prompt").String(), "a rejected run retains its original preview, not misleading substituted text")
			}
			payload.ProbeID = gjson.Get(started.Body.String(), "data.probes.0.id").String()
			payload.ExpectedAttempt = 1
			retried := invoke(RetryChannelFingerprintProbe, jobID)
			require.True(t, gjson.Get(retried.Body.String(), "success").Bool(), retried.Body.String())
			require.Eventually(t, func() bool {
				job, err := service.GetChannelFingerprint(jobID)
				return err == nil && job.FinishedAt != 0
			}, 5*time.Second, 10*time.Millisecond)
			read = invoke(GetChannelFingerprint, jobID)
			assert.Equal(t, errFingerprintWirePromptChanged.Error(), gjson.Get(read.Body.String(), "data.probes.0.error").String())
			assert.Equal(t, prompt, gjson.Get(read.Body.String(), "data.probes.0.prompt").String())
			assert.Zero(t, calls.Load(), "initial, explicit retry and replay all retain the final-body prompt guard")
			var logs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("channel_id = ? AND type = ?", selected.Id, model.LogTypeConsume).Count(&logs).Error)
			assert.Zero(t, logs, "preflight rejection never records productive usage")
		})
	}
}

func TestFingerprintRefusalJobProjectionDoesNotExposeUpstreamFields(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	const secret = "sk-fingerprint-refusal-secret"
	const promotion = "https://untrusted.example/pay"
	for _, tc := range []struct {
		name        string
		channelType int
		endpoint    string
		body        string
	}{
		{
			name: "OpenAI malicious code type and metadata", channelType: constant.ChannelTypeOpenAI, endpoint: "openai",
			body: `{"error":{"code":"sk-fingerprint-refusal-secret https://untrusted.example/pay","type":"sk-fingerprint-refusal-secret https://untrusted.example/pay","message":"Contact https://untrusted.example/pay with sk-fingerprint-refusal-secret","param":"sk-fingerprint-refusal-secret","metadata":{"detail":"https://untrusted.example/pay"}}}`,
		},
		{
			name: "Messages malicious type and payload", channelType: constant.ChannelTypeAnthropic, endpoint: "anthropic",
			body: `{"type":"error","error":{"type":"sk-fingerprint-refusal-secret https://untrusted.example/pay","message":"Contact https://untrusted.example/pay with sk-fingerprint-refusal-secret"},"detail":"sk-fingerprint-refusal-secret https://untrusted.example/pay"}`,
		},
		{
			name: "unstructured refusal payload", channelType: constant.ChannelTypeOpenAI, endpoint: "openai",
			body: `sk-fingerprint-refusal-secret https://untrusted.example/pay`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(upstream.Close)
			selected := &model.Channel{
				Type: tc.channelType, Name: "fingerprint-safe-refusal", Key: "test-refusal-key",
				Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL,
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			payload := fingerprintProbeRequest{Model: "gpt-4.1", EndpointType: tc.endpoint, Count: 1}
			invoke := func(handler gin.HandlerFunc, jobID string) *httptest.ResponseRecorder {
				encoded, err := common.Marshal(payload)
				require.NoError(t, err)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(string(encoded)))
				c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(selected.Id)}}
				if jobID != "" {
					c.Params = append(c.Params, gin.Param{Key: "job_id", Value: jobID})
				}
				c.Set("id", userID)
				handler(c)
				return w
			}
			preview := invoke(PreviewChannelFingerprint, "")
			require.True(t, gjson.Get(preview.Body.String(), "success").Bool(), preview.Body.String())
			assert.Zero(t, calls.Load(), "preview never dispatches")
			payload.PreviewID = gjson.Get(preview.Body.String(), "data.preview_id").String()
			started := invoke(StartChannelFingerprint, "")
			require.True(t, gjson.Get(started.Body.String(), "success").Bool(), started.Body.String())
			jobID := gjson.Get(started.Body.String(), "data.id").String()
			require.NotEmpty(t, jobID)
			t.Cleanup(func() {
				_, _ = service.CancelChannelFingerprint(jobID)
				require.Eventually(t, func() bool {
					job, err := service.GetChannelFingerprint(jobID)
					return err != nil || job.FinishedAt != 0
				}, 5*time.Second, 10*time.Millisecond, "finish refusal work before fixture teardown")
			})
			require.Eventually(t, func() bool {
				job, err := service.GetChannelFingerprint(jobID)
				return err == nil && job.FinishedAt != 0
			}, 5*time.Second, 10*time.Millisecond)
			assert.EqualValues(t, 1, calls.Load(), "a refusal is not replayed as new paid work")
			read := invoke(GetChannelFingerprint, jobID)
			repeated := invoke(StartChannelFingerprint, "")
			for _, response := range []*httptest.ResponseRecorder{started, read, repeated} {
				assert.NotContains(t, response.Body.String(), secret, "the entire job/probe JSON must exclude provider fields")
				assert.NotContains(t, response.Body.String(), promotion)
			}
			for _, response := range []*httptest.ResponseRecorder{read, repeated} {
				body := response.Body.String()
				require.True(t, gjson.Get(body, "success").Bool(), body)
				assert.Equal(t, jobID, gjson.Get(body, "data.id").String())
				assert.Equal(t, "failed", gjson.Get(body, "data.state").String())
				assert.Equal(t, "fingerprint request failed (HTTP 403)", gjson.Get(body, "data.error").String())
				assert.Equal(t, "fingerprint request failed (HTTP 403)", gjson.Get(body, "data.probes.0.error").String())
				assert.EqualValues(t, 1, gjson.Get(body, "data.requests_used").Int())
				assert.Zero(t, gjson.Get(body, "data.probes.0.prompt_tokens").Int())
				assert.Zero(t, gjson.Get(body, "data.probes.0.output_tokens").Int())
			}
			assert.EqualValues(t, 1, calls.Load(), "GET and repeated Start must not dispatch another request")
			payload.ProbeID = gjson.Get(started.Body.String(), "data.probes.0.id").String()
			payload.ExpectedAttempt = 1
			retried := invoke(RetryChannelFingerprintProbe, jobID)
			require.True(t, gjson.Get(retried.Body.String(), "success").Bool(), retried.Body.String())
			require.Eventually(t, func() bool {
				job, err := service.GetChannelFingerprint(jobID)
				return err == nil && job.FinishedAt != 0
			}, 5*time.Second, 10*time.Millisecond)
			read = invoke(GetChannelFingerprint, jobID)
			replayedRetry := invoke(RetryChannelFingerprintProbe, jobID)
			for _, response := range []*httptest.ResponseRecorder{retried, read, replayedRetry} {
				assert.NotContains(t, response.Body.String(), secret)
				assert.NotContains(t, response.Body.String(), promotion)
			}
			assert.Equal(t, "fingerprint request failed (HTTP 403)", gjson.Get(read.Body.String(), "data.probes.0.error").String())
			assert.EqualValues(t, 2, gjson.Get(read.Body.String(), "data.probes.0.attempt").Int())
			assert.EqualValues(t, 2, calls.Load(), "explicit retry adds one refusal; replay never adds another")
			var logs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("channel_id = ? AND type = ?", selected.Id, model.LogTypeConsume).Count(&logs).Error)
			assert.Zero(t, logs, "a refusal without trusted usage is not settled as a success")
		})
	}
}

func TestFingerprintStableSelectionRevalidatedBetweenProbes(t *testing.T) {
	userID := setupChannelDiagnosticRequests(t)
	requests := make(chan channelDiagnosticWire, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- captureChannelDiagnostic(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, channelDiagnosticChatReply)
	}))
	t.Cleanup(upstream.Close)
	for _, mutation := range []string{"remove", "revision", "scope", "reorder"} {
		t.Run(mutation, func(t *testing.T) {
			selected := &model.Channel{
				Type: constant.ChannelTypeOpenAI, Name: "fingerprint-stable-" + mutation, Key: "stable-first\nstable-selected",
				Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusManuallyDisabled, BaseURL: &upstream.URL,
				ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling},
			}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			selected, err := model.GetChannelById(selected.Id, true)
			require.NoError(t, err)
			allocation, ok := selected.ChannelAllocationByPosition(1)
			require.True(t, ok)
			selection := service.FingerprintKeySelection{EntryID: allocation.EntryID, Revision: allocation.Revision, Index: 1, HasIndex: true}
			probe := func() error {
				_, err := runFingerprintProbe(t.Context(), selected, userID, "gpt-4.1", "openai", lmfp.Challenge{Prompt: "stable sample", ExpectedCount: 3}, 300, selection)
				return err
			}
			require.NoError(t, probe(), "named Keys and disabled channels retain admin recovery diagnostics")
			wire := takeChannelDiagnostic(t, requests)
			assert.Equal(t, "Bearer stable-selected", wire.headers.Get("Authorization"))
			_, err = model.UpdateChannelKeyAllocations(selected.Id, selected.ChannelInfo.KeyEntryRevision, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
				switch mutation {
				case "remove":
					return entries[:1], entries[1:], nil
				case "revision":
					entries[1].ConfigRevision++
				case "scope":
					entries[1].ScopeMode = "allowlist"
					entries[1].Models = "[]"
					entries[1].ConfigRevision++
				case "reorder":
					entries[0], entries[1] = entries[1], entries[0]
				}
				return entries, nil, nil
			})
			require.NoError(t, err)
			require.ErrorContains(t, probe(), "selected key is unavailable")
			assert.Empty(t, requests, "a later sample cannot fall back to another allocation")
			sample := service.ReadChannelConcurrency(t.Context(), []int{selected.Id})[selected.Id]
			if sample.Current != nil {
				assert.Zero(t, *sample.Current)
			}
		})
	}
}

func TestFingerprintPreviewRejectsInvalidKeyPayloadsWithoutSecrets(t *testing.T) {
	for _, body := range []string{
		`{"key_index":1}`,
		`{"key_entry_id":"secret-do-not-reflect"}`,
		`{"expected_rev":1}`,
		`{"key_entry_id":"01","expected_rev":1}`,
		`{"key_entry_id":"1","expected_rev":0}`,
		`{"key_entry_id":"1","expected_rev":1,"key_index":-1}`,
		`{"key_entry_id":"secret-do-not-reflect","expected_rev":"secret-do-not-reflect"}`,
	} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			c.Params = gin.Params{{Key: "id", Value: "1"}}
			c.Set("id", 9)
			PreviewChannelFingerprint(c)
			assert.False(t, gjson.Get(w.Body.String(), "success").Bool())
			assert.NotContains(t, w.Body.String(), "secret-do-not-reflect")
		})
	}
}

func TestFingerprintJobRouteOwnershipAndRequestLifetime(t *testing.T) {
	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	started := make(chan struct{})
	stopped := make(chan struct{})
	job, err := service.StartChannelFingerprint(parent, service.FingerprintRequest{
		ChannelID: 42, ChannelName: "selected-channel", Model: "gpt-4.1", Count: 1, TokenBudget: 1000, ProbeTimeout: 10 * time.Second,
	}, func(ctx context.Context, _ lmfp.Challenge, _ uint) (service.FingerprintProbeResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return service.FingerprintProbeResult{}, ctx.Err()
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = service.CancelChannelFingerprint(job.ID) })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not start")
	}
	cancelParent()
	for _, handler := range []gin.HandlerFunc{GetChannelFingerprint, CancelChannelFingerprint} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: "43"}, {Key: "job_id", Value: job.ID}}
		handler(c)
		assert.Equal(t, http.StatusNotFound, w.Code, "mismatched channel route cannot read or cancel")
	}
	current, err := service.GetChannelFingerprint(job.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", current.State, "handler completion and a mismatched cancellation leave the job running")
	select {
	case <-stopped:
		t.Fatal("probe ended without an explicit matching cancellation")
	default:
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(job.ChannelID)}, {Key: "job_id", Value: job.ID}}
	CancelChannelFingerprint(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, gjson.Get(w.Body.String(), "success").Bool())
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("matching cancellation did not stop the probe")
	}
}

func TestCountChallengeNumbers(t *testing.T) {
	for _, tt := range []struct {
		name string
		text string
		want int
	}{
		{name: "spaces", text: "12 34 56", want: 3},
		{name: "commas and newlines", text: "12,34\n56", want: 3},
		{name: "out of range", text: "0 356 400 12", want: 1},
		{name: "negative and text", text: "-12 hello 34", want: 1},
		{name: "empty", text: "", want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countChallengeNumbers(tt.text))
		})
	}
}
