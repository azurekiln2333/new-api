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
			_, err := adaptor.DoRequest(nil, info, strings.NewReader(tc.body))
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
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4.1":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogs
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	for _, tc := range []struct {
		name, endpoint, reply string
		wantErr               bool
	}{
		{name: "chat", endpoint: "openai", reply: "12 34 56"},
		{name: "messages", endpoint: "anthropic", reply: "12 34 56"},
		{name: "empty answer retains usage", endpoint: "openai", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan channelDiagnosticWire, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- captureChannelDiagnostic(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"resp-fingerprint","object":"response","status":"completed","model":"gpt-4.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`, tc.reply)
			}))
			defer upstream.Close()
			setting := `{"supported_endpoints":["responses"],"system_prompt":"diagnostic system prompt"}`
			override := `{"max_tokens":9000,"max_output_tokens":9000}`
			selected := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "fingerprint-responses-" + tc.name, Key: "test-fingerprint-key", Models: "gpt-4.1", Group: "default", Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Setting: &setting, ParamOverride: &override}
			require.NoError(t, selected.Insert())
			t.Cleanup(func() { require.NoError(t, selected.Delete()) })
			result, err := runFingerprintProbe(t.Context(), selected, rootID, "gpt-4.1", tc.endpoint, lmfp.Challenge{Prompt: "fingerprint challenge", ExpectedCount: 3}, 300)
			if tc.wantErr {
				require.ErrorContains(t, err, "no text to score")
			} else {
				require.NoError(t, err)
			}
			wire := takeChannelDiagnostic(t, requests)
			assert.Equal(t, "/v1/responses", wire.path)
			assert.Contains(t, string(wire.body), "fingerprint challenge")
			assert.Equal(t, 1, strings.Count(string(wire.body), "diagnostic system prompt"))
			assert.EqualValues(t, 300, gjson.GetBytes(wire.body, "max_output_tokens").Uint())
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
			_, err := runFingerprintProbe(ctx, selected, rootID, "gpt-4.1", "openai", lmfp.Challenge{Prompt: "fingerprint challenge", ExpectedCount: 3}, 300)
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
		return runFingerprintProbe(t.Context(), selected, userID, "gpt-4.1", "", lmfp.Challenge{Prompt: "current-policy challenge", ExpectedCount: 3}, 300)
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
