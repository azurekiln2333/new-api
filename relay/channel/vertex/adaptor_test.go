package vertex

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVertexClaudeModel   = "claude-sonnet-4-5-20250929"
	testVertexGeminiModel   = "gemini-2.5-pro"
	testVertexLlamaModel    = "meta/llama3-405b-instruct-maas"
	testVertexClaudeMapped  = "claude-sonnet-4-5@20250929"
	testVertexClaudeRegion  = "global"
	testVertexProjectID     = "my-project"
	testVertexClaudePathFmt = "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/publishers/anthropic/models/claude-sonnet-4-5@20250929:"
	testVertexGooglePathFmt = "https://aiplatform.googleapis.com/v1/projects/my-project/locations/global/publishers/google/models/gemini-2.5-pro:"
	testVertexOpenSourceFmt = "https://aiplatform.googleapis.com/v1beta1/projects/my-project/locations/global/endpoints/openapi/chat/completions"
)

// vertexTestContext builds the gin context these adaptor methods write their
// bookkeeping keys into.
func vertexTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

// vertexTestInfo builds a credential-file (non API-key) RelayInfo whose URL
// carries a project so the assertions can name the full Vertex endpoint.
func vertexTestInfo(model string, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream:        stream,
		OriginModelName: model,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		RelayMode:       relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiVersion:        `{"default":"global"}`,
			ApiKey:            `{"project_id":"my-project"}`,
			UpstreamModelName: model,
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType:   dto.VertexKeyTypeJSON,
				VertexProjectID: testVertexProjectID,
			},
		},
	}
}

// vertexTestAdaptor initializes the adaptor the same way the host does, so the
// request mode follows the upstream model name.
func vertexTestAdaptor(info *relaycommon.RelayInfo) *Adaptor {
	a := &Adaptor{}
	a.Init(info)
	return a
}

func vertexRawMessage(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}

func vertexUsage(t *testing.T, usage any) *dto.Usage {
	t.Helper()
	typed, ok := usage.(*dto.Usage)
	require.True(t, ok, "usage must be *dto.Usage, got %T", usage)
	require.NotNil(t, typed)
	return typed
}

// vertexSSEEvents splits an SSE recorder body into its ordered event names.
func vertexSSEEvents(body string) []string {
	var events []string
	for line := range strings.SplitSeq(body, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "event: "); ok {
			events = append(events, name)
		}
	}
	return events
}

// vertexSSEPayloads returns the JSON payload of every data line, letting a
// test assert on converted fields without depending on event ordering alone.
func vertexSSEPayloads(t *testing.T, body string) []map[string]any {
	t.Helper()
	var payloads []map[string]any
	for line := range strings.SplitSeq(body, "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var payload map[string]any
		if err := common.UnmarshalJsonStr(data, &payload); err != nil {
			continue
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func vertexFirstPayloadOfType(payloads []map[string]any, eventType string) map[string]any {
	for _, payload := range payloads {
		if payload["type"] == eventType {
			return payload
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// /v1/responses request conversion
// ---------------------------------------------------------------------------

func TestConvertOpenAIResponsesRequestToVertexClaude(t *testing.T) {
	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexClaudeModel, false)
	adaptor := vertexTestAdaptor(info)

	got, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:        testVertexClaudeModel,
		Instructions: vertexRawMessage(t, "system rules"),
		Input: vertexRawMessage(t, []map[string]any{
			{"role": "user", "content": "lookup weather"},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "paris"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  "15 degrees",
			},
		}),
		Tools: vertexRawMessage(t, []map[string]any{
			{
				"type":       "function",
				"name":       "lookup",
				"parameters": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
			},
		}),
	})
	require.NoError(t, err)

	req, ok := got.(*VertexAIClaudeRequest)
	require.True(t, ok, "Vertex Claude must keep the anthropic_version envelope, got %T", got)
	assert.Equal(t, "vertex-2023-10-16", req.AnthropicVersion)
	// The Responses endpoint has no max_tokens; the Claude path fills the model
	// default so Vertex never receives an unbounded request.
	require.NotNil(t, req.MaxTokens)
	assert.Equal(t, uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(testVertexClaudeModel)), *req.MaxTokens)

	// instructions becomes the Anthropic system prompt.
	system, err := common.Marshal(req.System)
	require.NoError(t, err)
	assert.Contains(t, string(system), "system rules")

	// input becomes messages, and the function tools survive conversion.
	wire, err := common.Marshal(req)
	require.NoError(t, err)
	encoded := string(wire)
	assert.Contains(t, encoded, "lookup weather")
	assert.Contains(t, encoded, `"lookup"`)
	assert.Equal(t, testVertexClaudeMapped, c.GetString("request_model"))
}

func TestGetRequestURLForResponsesResolvesNormalizedClaudeModel(t *testing.T) {
	// The entry normalizes "-thinking" away before the adaptor runs; the URL
	// must still resolve through claudeModelMap from that normalized name.
	for _, tc := range []struct {
		name   string
		stream bool
		want   string
	}{
		{
			name: "non streaming",
			want: testVertexClaudePathFmt + "rawPredict",
		},
		{
			name:   "streaming",
			stream: true,
			want:   testVertexClaudePathFmt + "streamRawPredict?alt=sse",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := vertexTestInfo(testVertexClaudeModel, tc.stream)
			adaptor := vertexTestAdaptor(info)

			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, url)
		})
	}
}

func TestConvertOpenAIResponsesRequestToVertexGemini(t *testing.T) {
	previous := model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled
	model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = true
	t.Cleanup(func() { model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = previous })

	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexGeminiModel, false)
	adaptor := vertexTestAdaptor(info)

	got, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:        testVertexGeminiModel,
		Instructions: vertexRawMessage(t, "system rules"),
		Input: vertexRawMessage(t, []map[string]any{
			{"role": "user", "content": "lookup weather"},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "paris"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  "15 degrees",
			},
		}),
		Tools: vertexRawMessage(t, []map[string]any{
			{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}},
		}),
	})
	require.NoError(t, err)

	req, ok := got.(*dto.GeminiChatRequest)
	require.True(t, ok, "Gemini models must use generateContent, got %T", got)
	require.NotNil(t, req.SystemInstructions)
	require.Len(t, req.SystemInstructions.Parts, 1)
	assert.Equal(t, "system rules", req.SystemInstructions.Parts[0].Text)

	require.GreaterOrEqual(t, len(req.Contents), 2)
	assert.Equal(t, "user", req.Contents[0].Role)
	require.Len(t, req.Contents[0].Parts, 1)
	assert.Equal(t, "lookup weather", req.Contents[0].Parts[0].Text)

	// Vertex rejects the Gemini function-call identity fields, so both sides are
	// cleared at this provider boundary.
	require.Len(t, req.Contents, 3)
	assert.Equal(t, "model", req.Contents[1].Role)
	require.NotNil(t, req.Contents[1].Parts[0].FunctionCall)
	assert.Equal(t, "lookup", req.Contents[1].Parts[0].FunctionCall.FunctionName)
	assert.Empty(t, req.Contents[1].Parts[0].FunctionCall.ID)

	assert.Equal(t, "user", req.Contents[2].Role)
	require.Len(t, req.Contents[2].Parts, 1)
	require.NotNil(t, req.Contents[2].Parts[0].FunctionResponse)
	assert.Equal(t, "lookup", req.Contents[2].Parts[0].FunctionResponse.Name)
	assert.Nil(t, req.Contents[2].Parts[0].FunctionResponse.ID)

	tools := req.GetTools()
	require.Len(t, tools, 1)
	toolWire, err := common.Marshal(tools[0].FunctionDeclarations)
	require.NoError(t, err)
	assert.Contains(t, string(toolWire), `"name":"lookup"`)
}

func TestConvertOpenAIResponsesRequestToVertexGeminiKeepsIDsWhenSettingDisabled(t *testing.T) {
	previous := model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled
	model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = false
	t.Cleanup(func() { model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = previous })

	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexGeminiModel, false)

	got, err := vertexTestAdaptor(info).ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model: testVertexGeminiModel,
		Input: vertexRawMessage(t, []map[string]any{
			{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": map[string]any{"q": "paris"}},
			{"type": "function_call_output", "call_id": "call_1", "output": "15 degrees"},
		}),
		Tools: vertexRawMessage(t, []map[string]any{
			{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}},
		}),
	})
	require.NoError(t, err)

	req, ok := got.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, req.Contents, 2)
	assert.Equal(t, "call_1", req.Contents[0].Parts[0].FunctionCall.ID)
	assert.JSONEq(t, `"call_1"`, string(req.Contents[1].Parts[0].FunctionResponse.ID))
}

func TestGetRequestURLForResponsesGeminiAndOpenSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		stream bool
		want   string
	}{
		{
			name:  "gemini non streaming",
			model: testVertexGeminiModel,
			want:  testVertexGooglePathFmt + "generateContent",
		},
		{
			name:   "gemini streaming",
			model:  testVertexGeminiModel,
			stream: true,
			want:   testVertexGooglePathFmt + "streamGenerateContent?alt=sse",
		},
		{
			name:  "open source non streaming",
			model: testVertexLlamaModel,
			want:  testVertexOpenSourceFmt,
		},
		{
			name:   "open source streaming",
			model:  testVertexLlamaModel,
			stream: true,
			want:   testVertexOpenSourceFmt,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := vertexTestInfo(tc.model, tc.stream)
			url, err := vertexTestAdaptor(info).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, url)
		})
	}
}

func TestConvertOpenAIResponsesRequestToVertexOpenSource(t *testing.T) {
	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexLlamaModel, true)
	info.SupportStreamOptions = true
	info.RelayFormat = types.RelayFormatOpenAI

	got, err := vertexTestAdaptor(info).ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:        testVertexLlamaModel,
		Instructions: vertexRawMessage(t, "system rules"),
		Input:        vertexRawMessage(t, "hello"),
	})
	require.NoError(t, err)

	req, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "MaaS models are reached through the OpenAI chat endpoint, got %T", got)
	assert.Equal(t, testVertexLlamaModel, req.Model)
	require.NotNil(t, req.Messages)
	assert.NotEmpty(t, req.Messages)
	// MaaS streams only report usage when asked, so the envelope must request it.
	require.NotNil(t, req.StreamOptions)
	assert.True(t, req.StreamOptions.IncludeUsage)
}

// ---------------------------------------------------------------------------
// /v1/messages request conversion
// ---------------------------------------------------------------------------

func TestConvertClaudeRequestToVertexClaude(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
		want   string
	}{
		{name: "non streaming", want: testVertexClaudePathFmt + "rawPredict"},
		{name: "streaming", stream: true, want: testVertexClaudePathFmt + "streamRawPredict?alt=sse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := vertexTestContext(t)
			info := vertexTestInfo(testVertexClaudeModel, tc.stream)
			info.RelayFormat = types.RelayFormatClaude
			adaptor := vertexTestAdaptor(info)

			body := `{"model":"` + testVertexClaudeModel + `","max_tokens":256,` +
				`"system":"be terse","messages":[{"role":"user","content":"hello"}]}`
			var request dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))

			got, err := adaptor.ConvertClaudeRequest(c, info, &request)
			require.NoError(t, err)

			req, ok := got.(*VertexAIClaudeRequest)
			require.True(t, ok, "a Claude model keeps the native Vertex Claude envelope, got %T", got)
			assert.Equal(t, "vertex-2023-10-16", req.AnthropicVersion)
			require.NotNil(t, req.MaxTokens)
			assert.Equal(t, uint(256), *req.MaxTokens)
			system, err := common.Marshal(req.System)
			require.NoError(t, err)
			assert.Contains(t, string(system), "be terse")

			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, url)
		})
	}
}

func TestConvertClaudeRequestToVertexGemini(t *testing.T) {
	previous := model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled
	model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = true
	t.Cleanup(func() { model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = previous })

	for _, tc := range []struct {
		name   string
		stream bool
		want   string
	}{
		{name: "non streaming", want: testVertexGooglePathFmt + "generateContent"},
		{name: "streaming", stream: true, want: testVertexGooglePathFmt + "streamGenerateContent?alt=sse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := vertexTestContext(t)
			info := vertexTestInfo(testVertexGeminiModel, tc.stream)
			info.RelayFormat = types.RelayFormatClaude
			adaptor := vertexTestAdaptor(info)

			// A Claude Code client sending an Anthropic Messages body while the
			// channel serves a Gemini model.
			body := `{"model":"` + testVertexGeminiModel + `","max_tokens":256,"system":"be terse",` +
				`"tools":[{"name":"lookup","description":"Lookup data","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}],` +
				`"messages":[` +
				`{"role":"user","content":"lookup weather"},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"paris"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"15 degrees"}]}]}`
			var request dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))

			got, err := adaptor.ConvertClaudeRequest(c, info, &request)
			require.NoError(t, err)

			req, ok := got.(*dto.GeminiChatRequest)
			require.True(t, ok, "a Gemini model must convert the Claude body to generateContent, got %T", got)
			require.NotNil(t, req.SystemInstructions)
			require.Len(t, req.SystemInstructions.Parts, 1)
			assert.Equal(t, "be terse", req.SystemInstructions.Parts[0].Text)

			wire, err := common.Marshal(req)
			require.NoError(t, err)
			encoded := string(wire)
			assert.Contains(t, encoded, "lookup weather")
			assert.Contains(t, encoded, `"lookup"`)
			assert.Contains(t, encoded, "15 degrees")
			// Vertex rejects the Gemini function-call identity fields.
			assert.NotContains(t, encoded, "call_1")

			url, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tc.want, url)
		})
	}
}

func TestConvertClaudeRequestToVertexOpenSource(t *testing.T) {
	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexLlamaModel, false)
	info.RelayFormat = types.RelayFormatClaude

	body := `{"model":"` + testVertexLlamaModel + `","max_tokens":256,"system":"be terse",` +
		`"messages":[{"role":"user","content":"hello"}]}`
	var request dto.ClaudeRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))

	got, err := vertexTestAdaptor(info).ConvertClaudeRequest(c, info, &request)
	require.NoError(t, err)

	req, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "MaaS models must be reached with an OpenAI chat body, got %T", got)
	assert.Equal(t, testVertexLlamaModel, req.Model)
	require.NotEmpty(t, req.Messages)
	assert.Equal(t, "hello", req.Messages[len(req.Messages)-1].StringContent())
}

// ---------------------------------------------------------------------------
// /v1/chat/completions request conversion
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequestKeepsNormalizedClaudeModel(t *testing.T) {
	// The entry maps and normalizes the client model into UpstreamModelName while
	// request.Model keeps the suffixed client spelling; overwriting the upstream
	// name would break claudeModelMap during URL resolution.
	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexClaudeModel, false)
	info.RelayFormat = types.RelayFormatOpenAI
	adaptor := vertexTestAdaptor(info)

	request := &dto.GeneralOpenAIRequest{
		Model:    testVertexClaudeModel + "-thinking",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}

	got, err := adaptor.ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	converted, ok := got.(*VertexAIClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "vertex-2023-10-16", converted.AnthropicVersion)

	// The normalized upstream name must stay authoritative for URL resolution.
	assert.Equal(t, testVertexClaudeModel, info.UpstreamModelName)
	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, testVertexClaudePathFmt+"rawPredict", url)
	assert.Equal(t, testVertexClaudeMapped, c.GetString("request_model"))
}

func TestConvertOpenAIRequestToVertexGeminiClearsFunctionIDs(t *testing.T) {
	previous := model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled
	model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = true
	t.Cleanup(func() { model_setting.GetGeminiSettings().RemoveFunctionResponseIdEnabled = previous })

	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexGeminiModel, false)
	info.RelayFormat = types.RelayFormatOpenAI

	request := &dto.GeneralOpenAIRequest{
		Model: testVertexGeminiModel,
		Messages: []dto.Message{
			{Role: "user", Content: "lookup weather"},
			{Role: "assistant"},
			{Role: "tool", ToolCallId: "call_1", Content: "15 degrees"},
		},
		Tools: []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:       "lookup",
				Parameters: vertexRawMessage(t, map[string]any{"type": "object"}),
			},
		}},
	}
	request.Messages[1].SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"paris"}`}},
	})

	got, err := vertexTestAdaptor(info).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	converted, ok := got.(*dto.GeminiChatRequest)
	require.True(t, ok)

	wire, err := common.Marshal(converted)
	require.NoError(t, err)
	encoded := string(wire)
	assert.Contains(t, encoded, `"functionCall"`)
	assert.Contains(t, encoded, `"functionResponse"`)
	assert.NotContains(t, encoded, `"id"`)
}

func TestConvertOpenAIRequestToVertexOpenSource(t *testing.T) {
	c, _ := vertexTestContext(t)
	info := vertexTestInfo(testVertexLlamaModel, false)
	info.RelayFormat = types.RelayFormatOpenAI

	request := &dto.GeneralOpenAIRequest{
		Model:    testVertexLlamaModel,
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}

	got, err := vertexTestAdaptor(info).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	converted, ok := got.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, testVertexLlamaModel, converted.Model)
	assert.Equal(t, "hello", converted.Messages[0].StringContent())
	// Non-streaming requests carry no stream_options.
	assert.Nil(t, converted.StreamOptions)
}

// ---------------------------------------------------------------------------
// DoResponse: /v1/responses
// ---------------------------------------------------------------------------

const (
	vertexClaudeResponsesJSON = `{"id":"msg_1","type":"message","model":"claude-sonnet-4-5-20250929",` +
		`"role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":2,"output_tokens":3}}`
	vertexClaudeResponsesSSE = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","model":"claude-sonnet-4-5-20250929","role":"assistant","content":[],"usage":{"input_tokens":2,"output_tokens":0}}}` + "\n\n" +
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n" +
		"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n" +
		"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"

	vertexGeminiJSON = `{"modelVersion":"gemini-2.5-pro","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}`
	vertexGeminiSSE = "data: " + `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}` + "\n\n" +
		`data: {"modelVersion":"gemini-2.5-pro","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}}` + "\n\n"

	vertexLlamaJSON = `{"id":"chatcmpl-1","object":"chat.completion","model":"meta/llama3-405b-instruct-maas",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	vertexLlamaSSE = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"meta/llama3-405b-instruct-maas","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"meta/llama3-405b-instruct-maas","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\n" +
		"data: [DONE]\n\n"
)

// vertexResponseInfo builds the RelayInfo a /v1/responses relay hands to
// DoResponse.
func vertexResponseInfo(model string, stream bool) *relaycommon.RelayInfo {
	info := vertexTestInfo(model, stream)
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.DisablePing = true
	info.StartTime = time.Now()
	info.ThinkingContentInfo = relaycommon.ThinkingContentInfo{IsFirstThinkingContent: true}
	return info
}

func TestDoResponseForResponsesReturnsValidPayload(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, tc := range []struct {
		name   string
		model  string
		stream bool
		body   string
	}{
		{name: "claude non streaming", model: testVertexClaudeModel, body: vertexClaudeResponsesJSON},
		{name: "claude streaming", model: testVertexClaudeModel, stream: true, body: vertexClaudeResponsesSSE},
		{name: "gemini non streaming", model: testVertexGeminiModel, body: vertexGeminiJSON},
		{name: "gemini streaming", model: testVertexGeminiModel, stream: true, body: vertexGeminiSSE},
		{name: "open source non streaming", model: testVertexLlamaModel, body: vertexLlamaJSON},
		{name: "open source streaming", model: testVertexLlamaModel, stream: true, body: vertexLlamaSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := vertexTestContext(t)
			info := vertexResponseInfo(tc.model, tc.stream)

			usage, apiErr := vertexTestAdaptor(info).DoResponse(c, &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}, info)
			require.Nil(t, apiErr)

			counted := vertexUsage(t, usage)
			assert.Equal(t, 2, counted.PromptTokens)
			assert.Equal(t, 3, counted.CompletionTokens)
			assert.Equal(t, 5, counted.TotalTokens)

			body := recorder.Body.String()
			require.NotEmpty(t, body)
			if !tc.stream {
				// Every non-streaming Responses relay answers with one response object.
				require.Contains(t, body, `"object":"response"`)
				assert.NotContains(t, body, `"object":"chat.completion"`)
				return
			}

			events := vertexSSEEvents(body)
			require.NotEmpty(t, events)
			assert.Equal(t, "response.created", events[0])
			assert.Equal(t, "response.completed", events[len(events)-1])
			payloads := vertexSSEPayloads(t, body)
			delta := vertexFirstPayloadOfType(payloads, "response.output_text.delta")
			require.NotNil(t, delta, "stream must carry the assistant text as a Responses delta")
			assert.Equal(t, "hello", delta["delta"])
		})
	}
}

// ---------------------------------------------------------------------------
// DoResponse: /v1/messages
// ---------------------------------------------------------------------------

func TestDoResponseForMessagesConvertsToAnthropic(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, tc := range []struct {
		name   string
		model  string
		stream bool
		body   string
	}{
		{name: "claude non streaming", model: testVertexClaudeModel, body: vertexClaudeResponsesJSON},
		{name: "claude streaming", model: testVertexClaudeModel, stream: true, body: vertexClaudeResponsesSSE},
		{name: "gemini non streaming", model: testVertexGeminiModel, body: vertexGeminiJSON},
		{name: "gemini streaming", model: testVertexGeminiModel, stream: true, body: vertexGeminiSSE},
		{name: "open source non streaming", model: testVertexLlamaModel, body: vertexLlamaJSON},
		{name: "open source streaming", model: testVertexLlamaModel, stream: true, body: vertexLlamaSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := vertexTestContext(t)
			info := vertexTestInfo(tc.model, tc.stream)
			info.RelayFormat = types.RelayFormatClaude
			info.RelayMode = relayconstant.RelayModeChatCompletions
			info.DisablePing = true
			info.StartTime = time.Now()
			info.ClaudeConvertInfo = &relaycommon.ClaudeConvertInfo{LastMessagesType: relaycommon.LastMessageTypeNone}

			usage, apiErr := vertexTestAdaptor(info).DoResponse(c, &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}, info)
			require.Nil(t, apiErr)
			counted := vertexUsage(t, usage)
			assert.Equal(t, 2, counted.PromptTokens)
			assert.Equal(t, 3, counted.CompletionTokens)
			assert.Equal(t, 5, counted.TotalTokens)

			body := recorder.Body.String()
			// A Claude client never sees the raw upstream envelope.
			assert.NotContains(t, body, `"candidates"`)
			assert.NotContains(t, body, `"chat.completion.chunk"`)

			if !tc.stream {
				assert.Contains(t, body, `"type":"message"`)
				assert.Contains(t, body, "hello")
				return
			}

			events := vertexSSEEvents(body)
			require.NotEmpty(t, events)
			assert.Equal(t, "message_start", events[0])
			assert.Equal(t, "message_stop", events[len(events)-1])
			assert.Contains(t, body, "hello")
		})
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeaderIsolatesClaudeHeaders(t *testing.T) {
	// fetchAccessToken is the package's own test seam; stubbing it keeps the
	// header build offline and independent of real credentials.
	original := fetchAccessToken
	t.Cleanup(func() { fetchAccessToken = original })
	const fakeToken = "fake-vertex-token"
	fetchAccessToken = func(Credentials, string) (string, time.Duration, error) {
		return fakeToken, time.Hour, nil
	}

	const anthropicBeta = "interleaved-thinking-2025-05-14"

	for _, tc := range []struct {
		name       string
		model      string
		resolveURL bool
	}{
		{name: "claude without URL resolution", model: testVertexClaudeModel},
		{name: "claude after URL resolution", model: testVertexClaudeModel, resolveURL: true},
		{name: "gemini without URL resolution", model: testVertexGeminiModel},
		{name: "gemini after URL resolution", model: testVertexGeminiModel, resolveURL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := vertexTestContext(t)
			c.Request.Header.Set("anthropic-beta", anthropicBeta)
			info := vertexTestInfo(tc.model, false)
			adaptor := vertexTestAdaptor(info)

			// Headers must not depend on having resolved the URL first.
			if tc.resolveURL {
				_, err := adaptor.GetRequestURL(info)
				require.NoError(t, err)
			}

			header := http.Header{}
			require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))

			// Service-account credentials authenticate every mode with a bearer token.
			assert.Equal(t, "Bearer "+fakeToken, header.Get("Authorization"))
			assert.Equal(t, testVertexProjectID, header.Get("x-goog-user-project"))

			if strings.Contains(tc.model, "claude") {
				assert.Equal(t, anthropicBeta, header.Get("anthropic-beta"))
				return
			}
			// Only the Claude publisher accepts Anthropic headers and keys.
			assert.Empty(t, header.Values("anthropic-beta"))
			assert.Empty(t, header.Get("x-api-key"))
		})
	}
}

// ---------------------------------------------------------------------------
// DoResponse: /v1/chat/completions
// ---------------------------------------------------------------------------

func TestDoResponseForChatCompletionsReturnsOpenAIPayload(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, tc := range []struct {
		name   string
		model  string
		stream bool
		body   string
	}{
		{name: "claude non streaming", model: testVertexClaudeModel, body: vertexClaudeResponsesJSON},
		{name: "claude streaming", model: testVertexClaudeModel, stream: true, body: vertexClaudeResponsesSSE},
		{name: "gemini non streaming", model: testVertexGeminiModel, body: vertexGeminiJSON},
		{name: "gemini streaming", model: testVertexGeminiModel, stream: true, body: vertexGeminiSSE},
		{name: "open source non streaming", model: testVertexLlamaModel, body: vertexLlamaJSON},
		{name: "open source streaming", model: testVertexLlamaModel, stream: true, body: vertexLlamaSSE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := vertexTestContext(t)
			info := vertexTestInfo(tc.model, tc.stream)
			info.RelayFormat = types.RelayFormatOpenAI
			info.RelayMode = relayconstant.RelayModeChatCompletions
			info.DisablePing = true
			info.StartTime = time.Now()

			usage, apiErr := vertexTestAdaptor(info).DoResponse(c, &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}, info)
			require.Nil(t, apiErr)
			counted := vertexUsage(t, usage)
			assert.Equal(t, 2, counted.PromptTokens)
			assert.Equal(t, 3, counted.CompletionTokens)
			assert.Equal(t, 5, counted.TotalTokens)

			body := recorder.Body.String()
			assert.Contains(t, body, "hello")
			if !tc.stream {
				assert.Contains(t, body, `"object":"chat.completion"`)
				assert.NotContains(t, body, `"object":"chat.completion.chunk"`)
				return
			}

			assert.Contains(t, body, `"object":"chat.completion.chunk"`)
			assert.Contains(t, body, "[DONE]")
		})
	}
}
