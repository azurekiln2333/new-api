package ollama

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func toOllamaResponseFormat(responseFormat *dto.ResponseFormat) (any, error) {
	if responseFormat == nil {
		return nil, nil
	}
	switch responseFormat.Type {
	case "json", "json_object":
		return "json", nil
	case "json_schema":
		if len(responseFormat.JsonSchema) == 0 {
			return nil, nil
		}
		// The raw schema keeps the client's property order, which the
		// model fills the properties in.
		var jsonSchema struct {
			Schema common.RawMessage `json:"schema"`
		}
		if err := common.Unmarshal(responseFormat.JsonSchema, &jsonSchema); err != nil {
			return nil, fmt.Errorf("invalid ollama response format: %w", err)
		}
		if len(jsonSchema.Schema) == 0 || common.GetJsonType(jsonSchema.Schema) == "null" {
			return nil, nil
		}
		return jsonSchema.Schema, nil
	default:
		return nil, nil
	}
}

func openAIChatToOllamaChat(c *gin.Context, r *dto.GeneralOpenAIRequest) (*OllamaChatRequest, error) {
	think := r.Think
	if len(think) == 0 {
		effort := r.ReasoningEffort
		if len(r.Reasoning) > 0 {
			var reasoning dto.Reasoning
			if err := common.Unmarshal(r.Reasoning, &reasoning); err != nil {
				return nil, fmt.Errorf("invalid ollama reasoning: %w", err)
			}
			effort = lo.CoalesceOrEmpty(reasoning.Effort, effort)
		}
		if effort != "" {
			var thinkValue any
			switch effort {
			case "none":
				thinkValue = false
			case "low", "medium", "high", "max":
				thinkValue = effort
			default:
				return nil, fmt.Errorf("unsupported ollama reasoning effort %q", effort)
			}
			var err error
			think, err = common.Marshal(thinkValue)
			if err != nil {
				return nil, fmt.Errorf("marshal ollama think: %w", err)
			}
		}
	}

	chatReq := &OllamaChatRequest{
		Model:   r.Model,
		Stream:  lo.FromPtrOr(r.Stream, false),
		Options: map[string]any{},
		Think:   think,
	}
	format, err := toOllamaResponseFormat(r.ResponseFormat)
	if err != nil {
		return nil, err
	}
	chatReq.Format = format

	// options mapping
	if r.Temperature != nil {
		chatReq.Options["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		chatReq.Options["top_p"] = lo.FromPtr(r.TopP)
	}
	if r.TopK != nil {
		chatReq.Options["top_k"] = lo.FromPtr(r.TopK)
	}
	if r.FrequencyPenalty != nil {
		chatReq.Options["frequency_penalty"] = lo.FromPtr(r.FrequencyPenalty)
	}
	if r.PresencePenalty != nil {
		chatReq.Options["presence_penalty"] = lo.FromPtr(r.PresencePenalty)
	}
	if r.Seed != nil {
		chatReq.Options["seed"] = int(lo.FromPtr(r.Seed))
	}
	if mt := r.GetMaxTokens(); mt != 0 {
		chatReq.Options["num_predict"] = int(mt)
	}

	if r.Stop != nil {
		switch v := r.Stop.(type) {
		case string:
			chatReq.Options["stop"] = []string{v}
		case []string:
			chatReq.Options["stop"] = v
		case []any:
			arr := lo.FilterMap(v, func(item any, _ int) (string, bool) {
				value, ok := item.(string)
				return value, ok
			})
			if len(arr) > 0 {
				chatReq.Options["stop"] = arr
			}
		}
	}

	if len(r.Tools) > 0 {
		chatReq.Tools = lo.Map(r.Tools, func(tool dto.ToolCallRequest, _ int) OllamaTool {
			return OllamaTool{
				Type: "function",
				Function: OllamaToolFunction{
					Name:        tool.Function.Name,
					Description: tool.Function.Description,
					Parameters:  tool.Function.Parameters,
				},
			}
		})
	}

	chatReq.Messages = make([]OllamaChatMessage, 0, len(r.Messages))
	toolNamesByCallID := make(map[string]string)
	for _, m := range r.Messages {
		var textBuilder strings.Builder
		var images []string
		if m.IsStringContent() {
			textBuilder.WriteString(m.StringContent())
		} else {
			parts := m.ParseContent()
			for _, part := range parts {
				if part.Type == dto.ContentTypeImageURL {
					source := part.ToFileSource()
					if source != nil {
						base64Data, _, err := service.GetBase64Data(c, source, "fetch image for ollama chat")
						if err != nil {
							return nil, err
						}
						if base64Data != "" {
							images = append(images, base64Data)
						}
					}
				} else if part.Type == dto.ContentTypeText {
					textBuilder.WriteString(part.Text)
				}
			}
		}
		cm := OllamaChatMessage{Role: m.Role, Content: textBuilder.String()}
		if len(images) > 0 {
			cm.Images = images
		}
		if m.Role == "assistant" {
			if reasoning, ok := lo.Coalesce(m.ReasoningContent, m.Reasoning); ok {
				thinking, err := common.Marshal(*reasoning)
				if err != nil {
					return nil, fmt.Errorf("marshal ollama thinking: %w", err)
				}
				cm.Thinking = thinking
			}
		}
		if m.Role == "tool" {
			cm.ToolCallID = m.ToolCallId
			cm.ToolName = lo.CoalesceOrEmpty(lo.FromPtr(m.Name), toolNamesByCallID[m.ToolCallId])
		}
		if m.ToolCalls != nil && len(m.ToolCalls) > 0 {
			parsed := m.ParseToolCalls()
			if len(parsed) > 0 {
				calls := make([]OllamaToolCall, 0, len(parsed))
				for _, tc := range parsed {
					var args any
					if tc.Function.Arguments != "" {
						_ = common.Unmarshal([]byte(tc.Function.Arguments), &args)
					}
					if args == nil {
						args = map[string]any{}
					}
					oc := OllamaToolCall{ID: tc.ID}
					oc.Function.Name = tc.Function.Name
					oc.Function.Arguments = args
					calls = append(calls, oc)
					if tc.ID != "" {
						toolNamesByCallID[tc.ID] = tc.Function.Name
					}
				}
				cm.ToolCalls = calls
			}
		}
		chatReq.Messages = append(chatReq.Messages, cm)
	}
	return chatReq, nil
}

// openAIToGenerate converts OpenAI completions request to Ollama generate
func openAIToGenerate(c *gin.Context, r *dto.GeneralOpenAIRequest) (*OllamaGenerateRequest, error) {
	gen := &OllamaGenerateRequest{
		Model:   r.Model,
		Stream:  lo.FromPtrOr(r.Stream, false),
		Options: map[string]any{},
		Think:   r.Think,
	}
	// Prompt may be in r.Prompt (string or []any)
	if r.Prompt != nil {
		switch v := r.Prompt.(type) {
		case string:
			gen.Prompt = v
		case []any:
			var sb strings.Builder
			for _, it := range v {
				if s, ok := it.(string); ok {
					sb.WriteString(s)
				}
			}
			gen.Prompt = sb.String()
		default:
			gen.Prompt = fmt.Sprintf("%v", r.Prompt)
		}
	}
	if r.Suffix != nil {
		if s, ok := r.Suffix.(string); ok {
			gen.Suffix = s
		}
	}
	format, err := toOllamaResponseFormat(r.ResponseFormat)
	if err != nil {
		return nil, err
	}
	gen.Format = format
	if r.Temperature != nil {
		gen.Options["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		gen.Options["top_p"] = lo.FromPtr(r.TopP)
	}
	if r.TopK != nil {
		gen.Options["top_k"] = lo.FromPtr(r.TopK)
	}
	if r.FrequencyPenalty != nil {
		gen.Options["frequency_penalty"] = lo.FromPtr(r.FrequencyPenalty)
	}
	if r.PresencePenalty != nil {
		gen.Options["presence_penalty"] = lo.FromPtr(r.PresencePenalty)
	}
	if r.Seed != nil {
		gen.Options["seed"] = int(lo.FromPtr(r.Seed))
	}
	if mt := r.GetMaxTokens(); mt != 0 {
		gen.Options["num_predict"] = int(mt)
	}
	if r.Stop != nil {
		switch v := r.Stop.(type) {
		case string:
			gen.Options["stop"] = []string{v}
		case []string:
			gen.Options["stop"] = v
		case []any:
			arr := lo.FilterMap(v, func(item any, _ int) (string, bool) {
				value, ok := item.(string)
				return value, ok
			})
			if len(arr) > 0 {
				gen.Options["stop"] = arr
			}
		}
	}
	return gen, nil
}

func requestOpenAI2Embeddings(r dto.EmbeddingRequest) *OllamaEmbeddingRequest {
	opts := map[string]any{}
	if r.Temperature != nil {
		opts["temperature"] = r.Temperature
	}
	if r.TopP != nil {
		opts["top_p"] = lo.FromPtr(r.TopP)
	}
	if r.FrequencyPenalty != nil {
		opts["frequency_penalty"] = lo.FromPtr(r.FrequencyPenalty)
	}
	if r.PresencePenalty != nil {
		opts["presence_penalty"] = lo.FromPtr(r.PresencePenalty)
	}
	if r.Seed != nil {
		opts["seed"] = int(lo.FromPtr(r.Seed))
	}
	dimensions := lo.FromPtrOr(r.Dimensions, 0)
	if r.Dimensions != nil {
		opts["dimensions"] = dimensions
	}
	input := r.ParseInput()
	if len(input) == 1 {
		return &OllamaEmbeddingRequest{Model: r.Model, Input: input[0], Options: opts, Dimensions: dimensions}
	}
	return &OllamaEmbeddingRequest{Model: r.Model, Input: input, Options: opts, Dimensions: dimensions}
}

func ollamaEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	var oResp OllamaEmbeddingResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)
	if err = common.Unmarshal(body, &oResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oResp.Error != "" {
		return nil, types.NewOpenAIError(fmt.Errorf("ollama error: %s", oResp.Error), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	data := make([]dto.OpenAIEmbeddingResponseItem, 0, len(oResp.Embeddings))
	for i, emb := range oResp.Embeddings {
		data = append(data, dto.OpenAIEmbeddingResponseItem{Index: i, Object: "embedding", Embedding: emb})
	}
	usage := &dto.Usage{PromptTokens: oResp.PromptEvalCount, CompletionTokens: 0, TotalTokens: oResp.PromptEvalCount}
	embResp := &dto.OpenAIEmbeddingResponse{Object: "list", Data: data, Model: info.UpstreamModelName, Usage: *usage}
	out, _ := common.Marshal(embResp)
	service.IOCopyBytesGracefully(c, resp, out)
	return usage, nil
}

// OllamaRequestOptions supplies management transport and static headers. A
// nil Header preserves the optional default Bearer key. Use Reload for a
// persisted channel whose request acquires its own admission.
type OllamaRequestOptions struct {
	Header    http.Header
	Settings  dto.ChannelSettings
	Admission *service.ChannelDiagnosticAdmission
	ChannelID int
	Reload    func() (baseURL string, headers http.Header, settings dto.ChannelSettings, err error)
}

func doOllamaManagementRequest(ctx context.Context, method, baseURL, path, apiKey string, body []byte, options OllamaRequestOptions, timeout time.Duration) (*http.Response, error) {
	prepare := func(base string, headers http.Header) (*http.Request, error) {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			return nil, fmt.Errorf("baseURL is empty")
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request")
		}
		if headers != nil {
			req.Header = headers.Clone()
		} else if key := strings.TrimSpace(apiKey); key != "" && key != "EMPTY" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if body != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, nil
	}
	request, err := prepare(baseURL, options.Header)
	if err != nil {
		return nil, err
	}
	call := service.ChannelManagementCall{
		Request: request, Settings: options.Settings, ChannelID: options.ChannelID,
		Admission: options.Admission, Timeout: timeout, NoRedirect: method != http.MethodGet,
	}
	if options.Reload != nil {
		call.Reload = func() (*http.Request, dto.ChannelSettings, error) {
			base, headers, settings, err := options.Reload()
			if err != nil {
				return nil, dto.ChannelSettings{}, err
			}
			// A reloaded channel with no Authorization must not fall back to
			// the old key. An empty map means deliberately unauthenticated.
			if headers == nil {
				headers = make(http.Header)
			}
			fresh, err := prepare(base, headers)
			return fresh, settings, err
		}
	}
	return service.DoChannelManagementRequest(call)
}

const maxOllamaManagementResponse = 10 << 20

func readOllamaManagementResponse(resp *http.Response) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned error %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOllamaManagementResponse+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(body) > maxOllamaManagementResponse {
		return nil, fmt.Errorf("upstream response is too large")
	}
	return body, nil
}

func FetchOllamaModels(ctx context.Context, baseURL, apiKey string, options OllamaRequestOptions) ([]OllamaModel, error) {
	resp, err := doOllamaManagementRequest(ctx, http.MethodGet, baseURL, "/api/tags", apiKey, nil, options, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readOllamaManagementResponse(resp)
	if err != nil {
		return nil, err
	}
	var tagsResponse OllamaTagsResponse
	if err := common.Unmarshal(body, &tagsResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response")
	}
	// A genuinely empty inventory is [], not an omitted/null models field or
	// an error sent with HTTP 200. Only the former may drive model removals.
	if tagsResponse.Error != "" || tagsResponse.Models == nil {
		return nil, fmt.Errorf("invalid Ollama model inventory")
	}
	for _, model := range tagsResponse.Models {
		if strings.TrimSpace(model.Name) == "" {
			return nil, fmt.Errorf("invalid Ollama model inventory")
		}
	}
	return tagsResponse.Models, nil
}

// 拉取 Ollama 模型 (非流式)
func PullOllamaModel(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions) error {
	body, err := common.Marshal(OllamaPullRequest{Name: modelName, Stream: common.GetPointer(false)})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := doOllamaManagementRequest(ctx, http.MethodPost, baseURL, "/api/pull", apiKey, body, options, 30*time.Minute)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readOllamaManagementResponse(resp)
	if err != nil {
		return err
	}
	var result OllamaPullResponse
	if err := common.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("failed to parse pull response")
	}
	if result.Error != "" || strings.EqualFold(result.Status, "error") {
		return fmt.Errorf("failed to pull model")
	}
	if !strings.EqualFold(result.Status, "success") {
		return fmt.Errorf("model pull did not complete: no success status received")
	}
	return nil
}

// 流式拉取 Ollama 模型 (支持进度回调)
func PullOllamaModelStream(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions, progressCallback func(OllamaPullResponse)) error {
	body, err := common.Marshal(OllamaPullRequest{Name: modelName, Stream: common.GetPointer(true)})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := doOllamaManagementRequest(ctx, http.MethodPost, baseURL, "/api/pull", apiKey, body, options, time.Hour)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to pull model, status %d", resp.StatusCode)
	}

	scanner := helper.NewStreamScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var pullResponse OllamaPullResponse
		if err := common.Unmarshal(line, &pullResponse); err != nil {
			continue
		}
		// Ollama can report errors after sending HTTP 200. Don't pass the
		// upstream error text through the browser progress callback.
		if pullResponse.Error != "" || strings.EqualFold(pullResponse.Status, "error") {
			return fmt.Errorf("failed to pull model")
		}
		if progressCallback != nil {
			progressCallback(pullResponse)
		}
		if strings.EqualFold(pullResponse.Status, "success") {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read stream response: %w", err)
	}
	return fmt.Errorf("model pull did not complete: no success status received")
}

// 删除 Ollama 模型
func DeleteOllamaModel(ctx context.Context, baseURL, apiKey, modelName string, options OllamaRequestOptions) error {
	body, err := common.Marshal(OllamaDeleteRequest{Name: modelName})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := doOllamaManagementRequest(ctx, http.MethodDelete, baseURL, "/api/delete", apiKey, body, options, time.Minute)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = readOllamaManagementResponse(resp)
	return err
}

func FetchOllamaVersion(ctx context.Context, baseURL, apiKey string, options OllamaRequestOptions) (string, error) {
	resp, err := doOllamaManagementRequest(ctx, http.MethodGet, baseURL, "/api/version", apiKey, nil, options, 10*time.Second)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := readOllamaManagementResponse(resp)
	if err != nil {
		return "", err
	}
	var versionResp struct {
		Version string `json:"version"`
	}
	if err := common.Unmarshal(body, &versionResp); err != nil {
		return "", fmt.Errorf("failed to parse response")
	}
	if versionResp.Version == "" {
		return "", fmt.Errorf("no version information returned")
	}
	return versionResp.Version, nil
}
