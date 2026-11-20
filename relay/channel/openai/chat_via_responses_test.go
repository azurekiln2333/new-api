package openai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResponsesChatTestContext(t *testing.T, body string, isStream bool) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "responses-test")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:           isStream,
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		DisablePing:        true,
	}
	return c, recorder, resp, info
}

func TestResponsesToChatPreservesHostedSearchUsageAndCitations(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	response := `{"id":"resp_1","model":"gpt-test","status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"function_call","id":"fc_1","name":"web_search","arguments":"{}"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Search result","annotations":[{"type":"url_citation","url":"https://example.com/source","title":"Source","start_index":0,"end_index":13}]}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	for _, mode := range []string{"json", "stream", "buffered", "terminal only"} {
		for _, vendorOverride := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/vendor=%t", mode, vendorOverride), func(t *testing.T) {
				body := response
				if mode != "json" {
					body = "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
					if mode != "terminal only" {
						body = "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_1\",\"status\":\"completed\"}}\n\n" + body
					}
				}
				c, recorder, resp, info := newResponsesChatTestContext(t, body, mode == "stream" || mode == "terminal only")
				info.WebSearchBillingKey = dto.BuildInToolWebSearch
				if vendorOverride {
					info.VendorToolUsage = func(raw []byte) map[string]int {
						assert.Contains(t, string(raw), `"input_tokens":2`)
						return map[string]int{dto.BuildInToolWebSearch: 0}
					}
				}
				var usage *dto.Usage
				var apiErr *types.NewAPIError
				switch mode {
				case "json":
					resp.Header.Set("Content-Type", "application/json")
					usage, apiErr = OaiResponsesToChatHandler(c, info, resp)
				case "buffered":
					usage, apiErr = OaiResponsesToChatBufferedStreamHandler(c, info, resp)
				default:
					usage, apiErr = OaiResponsesToChatStreamHandler(c, info, resp)
				}
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 2, usage.PromptTokens)
				assert.Equal(t, 3, usage.CompletionTokens)
				assert.Equal(t, 5, usage.TotalTokens)
				assert.Contains(t, recorder.Body.String(), "https://example.com/source")
				require.NotNil(t, info.ResponsesUsageInfo)
				count := 1
				if vendorOverride {
					count = 0
				}
				assert.Equal(t, count, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearch].CallCount)
			})
		}
	}
}

func TestOaiResponsesToChatStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `"role":"assistant"`)
	require.Contains(t, got, `"content":"hello"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
	require.Contains(t, got, `"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`)
	require.Contains(t, got, `data: [DONE]`)
	requireOrderedSubstrings(t, got,
		`"role":"assistant"`,
		`"content":"hello"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`,
		`"finish_reason":"tool_calls"`,
		`"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`,
		`data: [DONE]`,
	)
}

func TestOaiResponsesToChatStreamHandlerConvertsClaudeSSETerminalsAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	info.RelayFormat = types.RelayFormatClaude

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, 1, strings.Count(got, "event: message_start\n"))
	assert.Equal(t, 1, strings.Count(got, "event: content_block_stop\n"))
	assert.Equal(t, 1, strings.Count(got, "event: message_delta\n"))
	assert.Equal(t, 1, strings.Count(got, "event: message_stop\n"))

	messageDeltaFrame := ""
	for frame := range strings.SplitSeq(got, "\n\n") {
		if strings.HasPrefix(frame, "event: message_delta\n") {
			messageDeltaFrame = frame
			break
		}
	}
	require.NotEmpty(t, messageDeltaFrame)
	assert.Contains(t, messageDeltaFrame, `"type":"message_delta"`)
	assert.Contains(t, messageDeltaFrame, `"stop_reason":"end_turn"`)
	assert.Contains(t, messageDeltaFrame, `"input_tokens":2`)
	assert.Contains(t, messageDeltaFrame, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		"event: message_start\n",
		"event: content_block_stop\n",
		"event: message_delta\n",
		"event: message_stop\n",
	)
}

func TestOaiResponsesToChatBufferedStreamHandlerReturnsJSONFromSSE(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"buffered text"}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.done","response":{"model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, err := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.TotalTokens)

	got := recorder.Body.String()
	require.NotContains(t, got, `data:`)
	require.Contains(t, got, `"object":"chat.completion"`)
	require.Contains(t, got, `"content":"buffered text"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
}

func TestOaiResponsesToChatBufferedStreamHandlerPreservesReportedUsageOnFailure(t *testing.T) {
	const reported = `data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}}}}` + "\n\n"
	const delta = `data: {"type":"response.output_text.delta","delta":"partial answer"}` + "\n\n"
	const failed = `data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"bad_response","message":"upstream failed"}}}` + "\n\n"
	const failedUsage = `data: {"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}},"error":{"type":"server_error","message":"upstream failed"}}}` + "\n\n"
	const zeroUsage = `data: {"type":"response.error","response":{"status":"failed","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"error":{"type":"server_error","message":"upstream failed"}}}` + "\n\n"
	const malformed = "data: {invalid\n\n"
	for _, diagnostic := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			body       string
			readErr    error
			wantUsage  bool
			wantTokens int
		}{
			{name: "failed terminal reports usage", body: delta + failedUsage, wantUsage: true, wantTokens: 14},
			{name: "later zero usage preserves earlier usage", body: reported + delta + zeroUsage, wantUsage: true, wantTokens: 14},
			{name: "malformed event preserves earlier usage", body: reported + delta + malformed, wantUsage: true, wantTokens: 14},
			{name: "read failure preserves earlier usage", body: reported + delta, readErr: errors.New("upstream read failed"), wantUsage: true, wantTokens: 14},
			{name: "failed output does not estimate usage", body: delta + failed},
			{name: "explicit zero usage stays zero", body: delta + zeroUsage, wantUsage: true},
			{name: "malformed output does not estimate usage", body: delta + malformed},
			{name: "read failure does not estimate usage", body: delta, readErr: errors.New("upstream read failed")},
		} {
			t.Run(fmt.Sprintf("diagnostic=%t/%s", diagnostic, tc.name), func(t *testing.T) {
				c, recorder, resp, info := newResponsesChatTestContext(t, tc.body, false)
				info.IsChannelTest = diagnostic
				info.SetEstimatePromptTokens(99)
				if tc.readErr != nil {
					resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(tc.body), iotest.ErrReader(tc.readErr)))
				}

				usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)

				require.NotNil(t, apiErr)
				assert.Empty(t, recorder.Body.String(), "a failed buffered response must not emit a success body")
				assert.False(t, c.Writer.Written())
				if !tc.wantUsage {
					assert.Nil(t, usage, "output alone must not become estimated failure usage")
					return
				}
				require.NotNil(t, usage)
				assert.Equal(t, tc.wantTokens, usage.TotalTokens)
				if tc.wantTokens == 0 {
					assert.Zero(t, usage.PromptTokens)
					assert.Zero(t, usage.CompletionTokens)
					assert.Nil(t, usage.BillingUsage)
					return
				}
				assert.Equal(t, 11, usage.PromptTokens)
				assert.Equal(t, 3, usage.CompletionTokens)
				assert.Equal(t, 4, usage.PromptTokensDetails.CachedTokens)
				assert.Equal(t, 2, usage.CompletionTokenDetails.ReasoningTokens)
				require.NotNil(t, usage.BillingUsage)
				assert.Equal(t, dto.BillingUsageSourceOAIResponses, usage.BillingUsage.Source)
				assert.False(t, usage.BillingUsage.Estimated)
			})
		}
	}
}

func TestOaiResponsesToChatBufferedStreamHandlerRequiresDiagnosticTerminal(t *testing.T) {
	const reported = `data: {"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}}` + "\n\n"
	const delta = `data: {"type":"response.output_text.delta","delta":"partial answer"}` + "\n\n"
	for _, diagnostic := range []bool{false, true} {
		for _, hasUsage := range []bool{false, true} {
			for _, done := range []bool{false, true} {
				t.Run(fmt.Sprintf("diagnostic=%t/usage=%t/done=%t", diagnostic, hasUsage, done), func(t *testing.T) {
					body := delta
					if hasUsage {
						body = reported + body
					}
					if done {
						body += "data: [DONE]\n\n"
					}
					c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
					info.IsChannelTest = diagnostic
					info.SetEstimatePromptTokens(99)

					usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)

					if !diagnostic {
						require.Nil(t, apiErr, "ordinary callers retain the buffered fallback")
						require.NotNil(t, usage)
						assert.Contains(t, recorder.Body.String(), `"object":"chat.completion"`)
						assert.Contains(t, recorder.Body.String(), `"content":"partial answer"`)
						return
					}
					require.NotNil(t, apiErr)
					assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
					assert.Empty(t, recorder.Body.String())
					assert.False(t, c.Writer.Written())
					if !hasUsage {
						assert.Nil(t, usage, "a truncated diagnostic must not synthesize usage")
						return
					}
					require.NotNil(t, usage)
					assert.Equal(t, 11, usage.PromptTokens)
					assert.Equal(t, 3, usage.CompletionTokens)
					assert.Equal(t, 14, usage.TotalTokens)
				})
			}
		}
	}
}

func TestOaiResponsesToChatBufferedStreamHandlerPreservesInterleavedClaudeContent(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"**Planning file inspection**"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
		`data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","delta":"I’ll inspect the starter repository."}`,
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"reasoning","id":"rs_2","summary":[]}}`,
		`data: {"type":"response.reasoning_summary_text.delta","output_index":2,"item_id":"rs_2","delta":"**Clarifying environment task requirements**"}`,
		`data: {"type":"response.output_item.added","output_index":3,"item":{"type":"message","id":"msg_2","role":"assistant","content":[]}}`,
		`data: {"type":"response.output_text.delta","output_index":3,"item_id":"msg_2","delta":"What would you like me to build?"}`,
		`data: {"type":"response.done","response":{"id":"resp_1","model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	info.RelayFormat = types.RelayFormatClaude

	usage, apiErr := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)

	var claudeResponse dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &claudeResponse))
	require.Len(t, claudeResponse.Content, 4)
	assert.Equal(t, []string{"thinking", "text", "thinking", "text"}, []string{
		claudeResponse.Content[0].Type,
		claudeResponse.Content[1].Type,
		claudeResponse.Content[2].Type,
		claudeResponse.Content[3].Type,
	})
	require.NotNil(t, claudeResponse.Content[0].Thinking)
	require.NotNil(t, claudeResponse.Content[2].Thinking)
	assert.Equal(t, "**Planning file inspection**", *claudeResponse.Content[0].Thinking)
	assert.Equal(t, "I’ll inspect the starter repository.", claudeResponse.Content[1].GetText())
	assert.Equal(t, "**Clarifying environment task requirements**", *claudeResponse.Content[2].Thinking)
	assert.Equal(t, "What would you like me to build?", claudeResponse.Content[3].GetText())
}

func TestOaiChatToResponsesStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	usage, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `event: response.created`)
	require.Contains(t, got, `event: response.output_text.delta`)
	require.Contains(t, got, `"delta":"hello"`)
	require.Contains(t, got, `event: response.function_call_arguments.delta`)
	require.Contains(t, got, `"delta":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `event: response.completed`)
	require.Contains(t, got, `"input_tokens":2`)
	require.Contains(t, got, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		`event: response.created`,
		`event: response.output_item.added`,
		`event: response.output_text.delta`,
		`event: response.output_item.added`,
		`event: response.function_call_arguments.delta`,
		`event: response.output_text.done`,
		`event: response.function_call_arguments.done`,
		`event: response.completed`,
	)
}

func TestOaiChatToResponsesStreamHandlerRestoresCodexCustomToolCall(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls -la\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	tools, marshalErr := common.Marshal([]map[string]any{
		{"type": "custom", "name": "exec", "description": "Run a shell command", "format": map[string]any{"type": "text"}},
	})
	require.NoError(t, marshalErr)
	converted, convertErr := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte(`"list files"`),
		Tools: tools,
	})
	require.NoError(t, convertErr)
	chatRequest, ok := converted.Value.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Tools, 1)
	assert.Equal(t, "function", chatRequest.Tools[0].Type)
	assert.Equal(t, "exec", chatRequest.Tools[0].Function.Name)

	_, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)

	got := recorder.Body.String()
	assert.NotContains(t, got, `event: response.function_call_arguments`)
	assert.Contains(t, got, `"delta":"ls -la"`)
	assert.Contains(t, got, `"input":"ls -la"`)
	requireOrderedSubstrings(t, got,
		`event: response.output_item.added`,
		`"type":"custom_tool_call"`,
		`event: response.custom_tool_call_input.delta`,
		`event: response.custom_tool_call_input.done`,
		`event: response.output_item.done`,
		`event: response.completed`,
		`"type":"custom_tool_call"`,
	)
}

func requireOrderedSubstrings(t *testing.T, s string, parts ...string) {
	t.Helper()

	offset := 0
	for _, part := range parts {
		idx := strings.Index(s[offset:], part)
		require.NotEqualf(t, -1, idx, "missing %q after byte offset %d", part, offset)
		offset += idx + len(part)
	}
}

// A failed converted stream that reported usage for the failed attempt must be
// billed instead of discarded: dropping the usage would make the attempt free
// and retryable.
func TestConvertedStreamFailureWithReportedUsageIsBilled(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	body := "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"bad_response\",\"message\":\"upstream exploded\"},\"usage\":{\"input_tokens\":7,\"output_tokens\":0,\"total_tokens\":7}}}\n\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr, "billing a failed attempt with usage must not hand the error back for a retry")
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 7, usage.TotalTokens)
	// The failed attempt is narrated to the client as the protocol terminal
	// error, never replayed.
	assert.Contains(t, recorder.Body.String(), "bad_response")
}

func TestConvertedStreamRefusalKeepsFramingRetryable(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
		for _, tc := range []struct {
			name   string
			last   string
			status int
		}{
			{
				name:   "code only failure",
				last:   `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user"},"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`,
				status: http.StatusTooManyRequests,
			},
			{
				name:   "top level error",
				last:   `data: {"type":"error","error":{"code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user"}}`,
				status: http.StatusTooManyRequests,
			},
			{name: "EOF", status: http.StatusBadGateway},
			{name: "DONE without terminal", last: "data: [DONE]", status: http.StatusBadGateway},
			{name: "malformed event", last: `data: {"type":`, status: http.StatusBadGateway},
		} {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test\"}}\n\n" + tc.last + "\n\n"
				c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
				info.RelayFormat = format
				info.SetEstimatePromptTokens(11)
				resp.Header.Set("Retry-After", "3")

				usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)

				require.NotNil(t, apiErr)
				assert.Equal(t, tc.status, apiErr.StatusCode)
				assert.Nil(t, usage, "framing alone must not charge the prompt")
				assert.Empty(t, recorder.Body.String(), "created must not commit role/message_start")
				assert.False(t, c.Writer.Written(), "the relay loop must be able to select another channel")
				assert.Equal(t, "3", helper.UpstreamRetryAfter(c))
				assert.True(t, info.StreamStatus.ResponseFailed())
			})
		}
	}
}

func TestConvertedStreamFailureAfterOutputKeepsUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
		for _, terminal := range []string{
			`data: {"type":"response.failed","response":{"status":"failed","error":{"code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user"}}}`,
			"",
		} {
			t.Run(fmt.Sprintf("%s/terminal=%t", format, terminal != ""), func(t *testing.T) {
				body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" + terminal + "\n\n"
				c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
				info.RelayFormat = format
				info.SetEstimatePromptTokens(11)

				usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)

				require.Nil(t, apiErr, "the host settles the partial attempt only when the error is nil")
				require.NotNil(t, usage)
				assert.Equal(t, 11, usage.PromptTokens)
				assert.Positive(t, usage.CompletionTokens)
				assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
				assert.True(t, info.StreamStatus.ResponseFailed())
				assert.Contains(t, recorder.Body.String(), "hello")
				assert.Equal(t, 1, strings.Count(recorder.Body.String(), `"error":`))
				assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
				assert.NotContains(t, recorder.Body.String(), "event: message_stop")
				if format == types.RelayFormatClaude {
					assert.Contains(t, recorder.Body.String(), "event: error\n")
				}
			})
		}
	}
}

func TestConvertedStreamConversionFailurePreservesObservedUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, tc := range []struct {
		name     string
		last     string
		reported bool
	}{
		{
			name: "delta rejected by converter",
			last: `data: {"type":"response.reasoning_text.delta","output_index":0,"delta":"additional reasoning"}`,
		},
		{
			name:     "terminal usage before converter error",
			last:     `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":7,"output_tokens":5,"total_tokens":12},"output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"}]}}`,
			reported: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"hello\"}\n\n" + tc.last + "\n\n"
			c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
			info.RelayFormat = types.RelayFormatClaude
			info.SetEstimatePromptTokens(11)

			usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)

			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			if tc.reported {
				assert.Equal(t, 7, usage.PromptTokens)
				assert.Equal(t, 5, usage.CompletionTokens)
				assert.Equal(t, 12, usage.TotalTokens)
			} else {
				assert.Equal(t, 11, usage.PromptTokens)
				firstDeltaUsage := service.ResponseText2Usage(c, "hello", info.UpstreamModelName, 11)
				assert.Greater(t, usage.CompletionTokens, firstDeltaUsage.CompletionTokens, "the rejected delta remains billable")
			}
			assert.True(t, info.StreamStatus.ResponseFailed())
			assert.Contains(t, recorder.Body.String(), "event: error\n")
			assert.NotContains(t, recorder.Body.String(), "event: message_stop")
		})
	}
}

type convertedStreamFailingWriter struct {
	gin.ResponseWriter
	failOn string
}

func (w *convertedStreamFailingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.failOn) {
		return 0, errors.New("downstream write failed")
	}
	return w.ResponseWriter.Write(p)
}

func TestConvertedStreamWriteFailurePreservesUpstreamUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
		t.Run(string(format), func(t *testing.T) {
			body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":7,\"output_tokens\":5,\"total_tokens\":12}}}\n\n"
			c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
			info.RelayFormat = format
			c.Writer = &convertedStreamFailingWriter{ResponseWriter: c.Writer, failOn: "assistant"}

			usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)

			require.Nil(t, apiErr, "a failed held-frame write must not erase terminal usage")
			require.NotNil(t, usage)
			assert.Equal(t, 7, usage.PromptTokens)
			assert.Equal(t, 5, usage.CompletionTokens)
			assert.Equal(t, 12, usage.TotalTokens)
			assert.True(t, info.StreamStatus.ResponseFailed())
			assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
			assert.NotContains(t, recorder.Body.String(), "event: message_stop")
		})
	}
}

func TestConvertedStreamCommittedFramingIsNotReplayed(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	body := "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"concurrency_limit_exceeded\",\"message\":\"concurrency limit exceeded for user\"}}}\n\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	info.SetEstimatePromptTokens(11)
	gate := helper.InstallStreamCommitGate(c, helper.DefaultStreamCommitHoldBudget)
	defer gate.Finish(c)
	require.NoError(t, gate.Commit())

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Zero(t, usage.TotalTokens, "committed framing is not billable model output")
	assert.True(t, info.StreamStatus.ResponseFailed())
	assert.Contains(t, recorder.Body.String(), `"error":`)
	assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
}
