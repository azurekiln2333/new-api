package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lmfp"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Fingerprint checks reuse the channel test path: the same key eligibility, key
// scoping, endpoint normalization, capacity lease, timeouts and billing apply,
// so a probe is an ordinary relay request that the operator pays for upstream.
type fingerprintProbeRequest struct {
	PreviewID       string  `json:"preview_id,omitempty"`
	Model           string  `json:"model"`
	EndpointType    string  `json:"endpoint_type"`
	Count           int     `json:"count"`
	KeyEntryID      *string `json:"key_entry_id,omitempty"`
	ExpectedRev     *int64  `json:"expected_rev,omitempty"`
	KeyIndex        *int    `json:"key_index,omitempty"`
	ProbeID         string  `json:"probe_id,omitempty"`
	ExpectedAttempt int     `json:"expected_attempt,omitempty"`
}

type fingerprintContext struct {
	ownerID         int
	previewID       string
	request         service.FingerprintRequest
	selection       service.FingerprintKeySelection
	probeID         string
	expectedAttempt int
}

// GetChannelFingerprintOptions projects only the current model capabilities and
// masked allocation identities. Key model names are resolved by the same mapping
// and scope rules as dispatch; no credential or transport settings leave here.
func GetChannelFingerprintOptions(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		common.ApiErrorMsg(c, "Invalid channel ID")
		return
	}
	var selected model.Channel
	if err := model.DB.WithContext(c.Request.Context()).First(&selected, channelID).Error; err != nil {
		common.ApiErrorMsg(c, "Channel does not exist")
		return
	}
	models := normalizeModelNames(selected.GetModels())
	autoModels := make([]string, 0, len(models))
	for _, name := range models {
		if !selected.ChannelInfo.IsMultiKey {
			autoModels = append(autoModels, name)
			continue
		}
		for index := range selected.GetKeys() {
			status, recorded := selected.ChannelInfo.MultiKeyStatusList[index]
			if (!recorded || status == common.ChannelStatusEnabled) && selected.KeyServesModel(index, name) {
				autoModels = append(autoModels, name)
				break
			}
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	pageSize = min(max(pageSize, 1), 50)
	keys := selected.GetKeys()
	if !selected.ChannelInfo.IsMultiKey {
		keys = nil
	}
	totalPages := max(1, (len(keys)+pageSize-1)/pageSize)
	page = min(max(page, 1), totalPages)
	start, end := (page-1)*pageSize, min(page*pageSize, len(keys))
	items := make([]KeyStatus, 0, end-start)
	for index := start; index < end; index++ {
		allocation, ok := selected.ChannelAllocationByPosition(index)
		if !ok {
			common.ApiErrorMsg(c, "Channel key configuration changed, reload and retry")
			return
		}
		allowed := make([]string, 0, len(models))
		for _, name := range models {
			if selected.KeyServesModel(index, name) {
				allowed = append(allowed, name)
			}
		}
		status := selected.ChannelInfo.MultiKeyStatusList[index]
		if status == 0 {
			status = common.ChannelStatusEnabled
		}
		items = append(items, KeyStatus{
			Id: strconv.FormatInt(allocation.EntryID, 10), Rev: allocation.Revision,
			Index: index, Status: status, KeyPreview: maskKeyPreview(keys[index]),
			ScopeMode: "allowlist", Models: allowed,
		})
	}
	common.ApiSuccess(c, gin.H{
		"id": selected.Id, "type": selected.Type, "models": models, "auto_models": autoModels,
		"is_multi_key": selected.ChannelInfo.IsMultiKey,
		"revision": selected.ChannelInfo.KeyEntryRevision,
		"keys": items, "page": page, "total_pages": totalPages,
	})
}

func StartChannelFingerprint(c *gin.Context) {
	submission, err := fingerprintRequestContext(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if submission.previewID == "" {
		common.ApiErrorMsg(c, "a fingerprint preview is required")
		return
	}
	request, selection := submission.request, submission.selection
	job, err := service.StartChannelFingerprintPreview(
		c.Request.Context(), submission.ownerID, submission.previewID, request, selection,
		func(ctx context.Context, challenge lmfp.Challenge, maxOutputTokens uint) (service.FingerprintProbeResult, error) {
			return runFingerprintProbe(ctx, &model.Channel{Id: request.ChannelID}, submission.ownerID, request.Model, request.Endpoint, challenge, maxOutputTokens, selection)
		},
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, job)
}

func RetryChannelFingerprintProbe(c *gin.Context) {
	submission, err := fingerprintRequestContext(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	request, selection := submission.request, submission.selection
	job, err := service.RetryChannelFingerprintProbe(c.Request.Context(), submission.ownerID,
		submission.previewID, c.Param("job_id"), submission.probeID, submission.expectedAttempt,
		request, selection,
		func(ctx context.Context, challenge lmfp.Challenge, maxOutputTokens uint) (service.FingerprintProbeResult, error) {
			return runFingerprintProbe(ctx, &model.Channel{Id: request.ChannelID}, submission.ownerID, request.Model, request.Endpoint, challenge, maxOutputTokens, selection)
		})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, job)
}

func PreviewChannelFingerprint(c *gin.Context) {
	submission, err := fingerprintRequestContext(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	preview, err := service.CreateChannelFingerprintPreview(c.Request.Context(), submission.ownerID, submission.request, submission.selection)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

func fingerprintRequestContext(c *gin.Context) (*fingerprintContext, error) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		return nil, errors.New("invalid channel id")
	}
	var request fingerprintProbeRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		// Decoder errors can echo client-supplied values. Do not reflect Key
		// material or arbitrary prompt payloads through preview errors.
		return nil, errors.New("Invalid parameters")
	}
	ownerID := c.GetInt("id")
	if ownerID <= 0 {
		return nil, errors.New("authenticated fingerprint owner is unavailable")
	}
	selection, err := parseFingerprintKeySelection(request)
	if err != nil {
		return nil, errors.New("invalid fingerprint key selection")
	}
	selected, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, errors.New("fingerprint request could not be completed")
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		modelName = fingerprintDefaultModel(selected)
	}
	if modelName == "" {
		return nil, errors.New("a model is required")
	}
	if err := validateFingerprintChannel(selected, modelName); err != nil {
		return nil, err
	}
	keyIndex := -1
	if selection.HasIndex {
		keyIndex = selection.Index
	}
	if selection.EntryID > 0 {
		if _, err := pinChannelTestKey(selected, keyIndex, selection.EntryID, selection.Revision, modelName); err != nil {
			return nil, errors.New("the selected key is unavailable for this model")
		}
	}
	// Legacy endpoint fields cannot override the current saved channel/Key
	// policy. The admitted diagnostic resolves its client shape server-side.
	return &fingerprintContext{
		ownerID: ownerID, previewID: request.PreviewID, selection: selection,
		probeID: request.ProbeID, expectedAttempt: request.ExpectedAttempt,
		request: service.FingerprintRequest{
			ChannelID: channelID, ChannelName: selected.Name, Model: modelName,
			Count: request.Count,
		},
	}, nil
}

func parseFingerprintKeySelection(request fingerprintProbeRequest) (service.FingerprintKeySelection, error) {
	selection := service.FingerprintKeySelection{Index: -1}
	if request.KeyEntryID == nil && request.ExpectedRev == nil {
		if request.KeyIndex != nil {
			return selection, errors.New("key index requires a stable key identity")
		}
		return selection, nil
	}
	if request.KeyEntryID == nil || request.ExpectedRev == nil {
		return selection, errors.New("key identity and revision must be supplied together")
	}
	entryID, err := strconv.ParseInt(*request.KeyEntryID, 10, 64)
	if err != nil || entryID <= 0 || strconv.FormatInt(entryID, 10) != *request.KeyEntryID || *request.ExpectedRev <= 0 {
		return selection, errors.New("invalid stable key identity")
	}
	selection.EntryID = entryID
	selection.Revision = *request.ExpectedRev
	if request.KeyIndex != nil {
		if *request.KeyIndex < 0 {
			return service.FingerprintKeySelection{}, errors.New("invalid key index")
		}
		selection.Index = *request.KeyIndex
		selection.HasIndex = true
	}
	return selection, nil
}

func validateFingerprintChannel(selected *model.Channel, modelName string) error {
	if !slices.ContainsFunc(selected.GetModels(), func(name string) bool { return strings.TrimSpace(name) == modelName }) {
		return errors.New("the selected model is not available on this channel")
	}
	apiType, _ := common.ChannelType2APIType(selected.Type)
	switch apiType {
	case constant.APITypeOpenAI, constant.APITypeAnthropic, constant.APITypeGemini, constant.APITypeNewAPI:
		return nil
	default:
		return errors.New("fingerprint checks require a supported protocol with an enforceable output token limit")
	}
}

var errFingerprintWirePromptChanged = errors.New("fingerprint request no longer matches the previewed prompt")

// fingerprintBoundedAdaptor checks the effective request after conversions,
// plugins and channel overrides. The wrapper is only installed for probes.
// Providers such as Codex remove the output limit and must fail preflight.
type fingerprintBoundedAdaptor struct {
	channel.Adaptor
	maxOutputTokens uint
}

func (adaptor *fingerprintBoundedAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if adaptor.maxOutputTokens == 0 || adaptor.maxOutputTokens > 4096 {
		return nil, errors.New("invalid fingerprint output token limit")
	}
	format := info.GetFinalRequestRelayFormat()
	if info.UpstreamTextEndpoint == types.RelayFormatClaude {
		// A declared Messages upstream receives an Anthropic body even though
		// the channel's API type is OpenAI; check that shape, not chat shape.
		format = types.RelayFormatClaude
	}
	switch info.ApiType {
	case constant.APITypeOpenAI:
		if format != types.RelayFormatOpenAIResponses && format != types.RelayFormatClaude {
			format = types.RelayFormatOpenAI
		}
	case constant.APITypeAnthropic:
		format = types.RelayFormatClaude
	case constant.APITypeGemini:
		format = types.RelayFormatGemini
	case constant.APITypeNewAPI:
	default:
		return nil, errors.New("fingerprint checks require a supported protocol with an enforceable output token limit")
	}
	var body map[string]json.RawMessage
	if err := common.DecodeJson(requestBody, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("fingerprint request must be a JSON object")
	}
	if c == nil || c.Request == nil {
		return nil, types.NewErrorWithStatusCode(errFingerprintWirePromptChanged, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	prompt := channelTestPromptFromContext(c.Request.Context())
	if prompt == nil || prompt.Prompt == "" || !fingerprintWirePromptMatches(body, format, prompt.Prompt) {
		// Keep the sentinel through the diagnostic relay's typed error path;
		// a generic OpenAI error wrapper would reconstruct it from its text.
		return nil, types.NewErrorWithStatusCode(errFingerprintWirePromptChanged, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	// A probe only collects a text answer. Added tools could introduce a second
	// source of billable work beyond the output limit.
	for _, field := range []string{"tools", "functions"} {
		if raw := body[field]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("[]")) {
			return nil, errors.New("fingerprint probes do not support tool overrides")
		}
	}
	limits := body
	limitField := "max_tokens"
	switch format {
	case types.RelayFormatOpenAI:
		if _, exists := body["max_completion_tokens"]; exists {
			limitField = "max_completion_tokens"
			delete(body, "max_tokens")
		}
		body["n"] = json.RawMessage("1")
	case types.RelayFormatOpenAIResponses:
		limitField = "max_output_tokens"
	case types.RelayFormatClaude:
	case types.RelayFormatGemini:
		limits = map[string]json.RawMessage{}
		if raw := body["generationConfig"]; len(raw) > 0 {
			if err := common.Unmarshal(raw, &limits); err != nil {
				return nil, err
			}
		}
		if limits == nil {
			limits = map[string]json.RawMessage{}
		}
		limitField = "maxOutputTokens"
		limits["candidateCount"] = json.RawMessage("1")
	default:
		return nil, errors.New("fingerprint request has no supported output token limit")
	}
	limit := adaptor.maxOutputTokens
	if raw := limits[limitField]; len(raw) > 0 {
		var configured uint
		if err := common.Unmarshal(raw, &configured); err != nil {
			return nil, errors.New("fingerprint request has an invalid output token limit")
		}
		if configured > 0 {
			limit = min(limit, configured)
		}
	}
	limits[limitField] = json.RawMessage(strconv.FormatUint(uint64(limit), 10))
	if format == types.RelayFormatGemini {
		encoded, err := common.Marshal(limits)
		if err != nil {
			return nil, err
		}
		body["generationConfig"] = encoded
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return adaptor.Adaptor.DoRequest(c, info, bytes.NewReader(encoded))
}

// fingerprintWirePromptMatches accepts only a stateless, single-user text
// request carrying exactly the preview text. Known scalar generation controls
// may vary; extra prompt/history/schema/provider-extension fields fail closed.
// These checks run after conversion, plugins, overrides and finalization, not
// against the pre-conversion DTO that those stages can still change.
func fingerprintWirePromptMatches(body map[string]json.RawMessage, format types.RelayFormat, expected string) bool {
	var allowed []string
	contentField, blockType := "messages", "text"
	switch format {
	case types.RelayFormatOpenAI:
		allowed = []string{"model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "n", "temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty", "seed", "logprobs", "top_logprobs", "reasoning_effort", "tools", "functions"}
	case types.RelayFormatOpenAIResponses:
		contentField, blockType = "input", "input_text"
		allowed = []string{"model", "input", "stream", "stream_options", "max_output_tokens", "temperature", "top_p", "top_k", "frequency_penalty", "presence_penalty", "top_logprobs", "reasoning", "tools"}
	case types.RelayFormatClaude:
		allowed = []string{"model", "messages", "stream", "max_tokens", "temperature", "top_p", "top_k", "thinking", "output_config", "tools"}
	case types.RelayFormatGemini:
		contentField = "contents"
		allowed = []string{"contents", "generationConfig", "safetySettings", "tools"}
	default:
		return false
	}
	for field, raw := range body {
		if !slices.Contains(allowed, field) {
			return false
		}
		switch field {
		case contentField, "tools", "functions":
			// Content is checked below; tools are rejected by the output guard.
		case "model":
			var modelName string
			if common.Unmarshal(raw, &modelName) != nil || modelName == "" {
				return false
			}
		case "stream", "logprobs":
			var value bool
			if common.Unmarshal(raw, &value) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return false
			}
		case "reasoning_effort":
			var effort string
			if common.Unmarshal(raw, &effort) != nil || !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effort) {
				return false
			}
		case "stream_options":
			if !fingerprintWireControls(raw, "include_usage", "include_obfuscation") {
				return false
			}
		case "reasoning":
			if !fingerprintWireControls(raw, "effort", "summary") {
				return false
			}
		case "thinking":
			if !fingerprintWireControls(raw, "type", "budget_tokens", "display") {
				return false
			}
		case "output_config":
			if !fingerprintWireControls(raw, "effort") {
				return false
			}
		case "safetySettings":
			// Gemini conversion includes the configured safety controls. Accept
			// their closed enums, never free-form text or schema extensions.
			var settings []json.RawMessage
			if common.Unmarshal(raw, &settings) != nil {
				return false
			}
			for _, setting := range settings {
				fields, ok := fingerprintWireObject(setting, "category", "threshold")
				if !ok || len(fields) != 2 {
					return false
				}
				var category, threshold string
				if common.Unmarshal(fields["category"], &category) != nil ||
					!slices.Contains([]string{"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT"}, category) ||
					common.Unmarshal(fields["threshold"], &threshold) != nil ||
					!slices.Contains([]string{"OFF", "BLOCK_NONE", "BLOCK_ONLY_HIGH", "BLOCK_MEDIUM_AND_ABOVE", "BLOCK_LOW_AND_ABOVE", "HARM_BLOCK_THRESHOLD_UNSPECIFIED"}, threshold) {
					return false
				}
			}
		case "generationConfig":
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) &&
				!fingerprintWireControls(raw, "maxOutputTokens", "candidateCount", "temperature", "topP", "topK", "presencePenalty", "frequencyPenalty", "seed", "thinkingConfig") {
				return false
			}
		default:
			var number float64
			if common.Unmarshal(raw, &number) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return false
			}
		}
	}
	if format == types.RelayFormatOpenAIResponses {
		var text string
		if common.Unmarshal(body[contentField], &text) == nil {
			return text == expected
		}
	}
	var messages []json.RawMessage
	if common.Unmarshal(body[contentField], &messages) != nil || len(messages) != 1 {
		return false
	}
	allowedMessageFields := []string{"role", "content"}
	if format == types.RelayFormatOpenAIResponses {
		allowedMessageFields = append(allowedMessageFields, "type")
	}
	if format == types.RelayFormatGemini {
		allowedMessageFields = []string{"role", "parts"}
	}
	message, ok := fingerprintWireObject(messages[0], allowedMessageFields...)
	if !ok {
		return false
	}
	var role string
	if common.Unmarshal(message["role"], &role) != nil || role != "user" {
		return false
	}
	if raw, exists := message["type"]; exists {
		var kind string
		if common.Unmarshal(raw, &kind) != nil || kind != "message" {
			return false
		}
	}
	if format == types.RelayFormatGemini {
		var parts []json.RawMessage
		if common.Unmarshal(message["parts"], &parts) != nil || len(parts) != 1 {
			return false
		}
		part, ok := fingerprintWireObject(parts[0], "text")
		var text string
		return ok && common.Unmarshal(part["text"], &text) == nil && text == expected
	}
	var text string
	if common.Unmarshal(message["content"], &text) == nil {
		return text == expected
	}
	var parts []json.RawMessage
	if common.Unmarshal(message["content"], &parts) != nil || len(parts) != 1 {
		return false
	}
	part, ok := fingerprintWireObject(parts[0], "type", "text")
	var kind string
	return ok && common.Unmarshal(part["type"], &kind) == nil && kind == blockType &&
		common.Unmarshal(part["text"], &text) == nil && text == expected
}

// Decode without silently discarding unknown fields, including nested content
// and provider-specific prompt extensions that a typed DTO would ignore.
func fingerprintWireObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if common.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	for field := range object {
		if !slices.Contains(allowed, field) {
			return nil, false
		}
	}
	return object, true
}

func fingerprintWireControls(raw json.RawMessage, allowed ...string) bool {
	controls, ok := fingerprintWireObject(raw, allowed...)
	if !ok {
		return false
	}
	for field, value := range controls {
		var choices []string
		switch field {
		case "thinkingConfig":
			if !fingerprintWireControls(value, "includeThoughts", "thinkingBudget", "thinkingLevel") {
				return false
			}
			continue
		case "include_usage", "include_obfuscation", "includeThoughts":
			var enabled bool
			if common.Unmarshal(value, &enabled) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return false
			}
			continue
		case "effort":
			choices = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		case "summary":
			choices = []string{"auto", "concise", "detailed"}
		case "type":
			choices = []string{"enabled", "disabled", "adaptive"}
		case "display":
			choices = []string{"summarized", "omitted"}
		case "thinkingLevel":
			choices = []string{"minimal", "low", "medium", "high"}
		default:
			var number float64
			if common.Unmarshal(value, &number) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return false
			}
			continue
		}
		var choice string
		if common.Unmarshal(value, &choice) != nil || !slices.Contains(choices, choice) {
			return false
		}
	}
	return true
}

func GetChannelFingerprint(c *gin.Context) {
	job, err := fingerprintJobForChannel(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, err)
		return
	}
	common.ApiSuccess(c, job)
}

func CancelChannelFingerprint(c *gin.Context) {
	observed, err := fingerprintJobForChannel(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, err)
		return
	}
	// Legacy clients can cancel an initial run without a body. Once a retry
	// advances the revision, an unfenced delayed request must fail closed.
	request := struct {
		ExpectedRevision int `json:"expected_revision"`
	}{}
	if c.Request.Body != nil {
		if err := common.DecodeJson(c.Request.Body, &request); err != nil && !errors.Is(err, io.EOF) {
			common.ApiErrorMsg(c, "Invalid parameters")
			return
		}
	}
	if request.ExpectedRevision == 0 {
		request.ExpectedRevision = 1
	}
	if request.ExpectedRevision < 1 || request.ExpectedRevision != observed.Revision {
		common.ApiErrorStatus(c, http.StatusConflict, service.ErrFingerprintJobFinished)
		return
	}
	job, err := service.CancelChannelFingerprint(c.Param("job_id"), request.ExpectedRevision)
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, service.ErrFingerprintJobFinished) {
			status = http.StatusConflict
		}
		common.ApiErrorStatus(c, status, err)
		return
	}
	common.ApiSuccess(c, job)
}

// A job belongs to the channel in its route for reads and cancellation alike.
// Check this before cancellation so a mismatched URL cannot mutate another run.
func fingerprintJobForChannel(c *gin.Context) (*service.FingerprintJob, error) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		return nil, service.ErrFingerprintJobNotFound
	}
	job, err := service.GetChannelFingerprint(c.Param("job_id"))
	if err != nil {
		return nil, err
	}
	if job.ChannelID != channelID {
		return nil, service.ErrFingerprintJobNotFound
	}
	return job, nil
}

func fingerprintDefaultModel(channel *model.Channel) string {
	if channel.TestModel != nil && strings.TrimSpace(*channel.TestModel) != "" {
		return strings.TrimSpace(*channel.TestModel)
	}
	models := channel.GetModels()
	if len(models) == 0 {
		return ""
	}
	return strings.TrimSpace(models[0])
}

// Fingerprint scoring reads the reply text, so only endpoints whose reply shape
// this build can parse are allowed instead of silently scoring an empty answer.
func validateFingerprintEndpoint(endpointType string) error {
	switch endpointType {
	case "openai", "openai-response", "anthropic", "gemini":
		return nil
	default:
		return fmt.Errorf("fingerprint checks do not support endpoint type %q", endpointType)
	}
}

// fingerprintEndpoint selects a text client protocol from persisted provider
// capabilities. The shared diagnostic resolver still owns upstream conversion,
// transport policy and the selected allocation's effective runtime settings.
func fingerprintEndpoint(selected *model.Channel, modelName string) (string, error) {
	endpoints := common.GetEndpointTypesByChannelType(selected.Type, modelName)
	if len(endpoints) == 0 {
		return "", errors.New("fingerprint endpoint type is not supported")
	}
	endpoint := string(endpoints[0])
	if err := validateFingerprintEndpoint(endpoint); err != nil {
		return "", errors.New("fingerprint endpoint type is not supported")
	}
	return endpoint, nil
}

// runFingerprintProbe sends one challenge through the relay and returns the
// reply text. Capacity, timeouts, retries and billing are the channel test's.
func runFingerprintProbe(
	ctx context.Context,
	channel *model.Channel,
	testUserID int,
	modelName string,
	endpointType string,
	challenge lmfp.Challenge,
	maxOutputTokens uint,
	selection service.FingerprintKeySelection,
) (service.FingerprintProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return service.FingerprintProbeResult{}, err
	}
	// A run spans several requests. Do not retain its starting credentials,
	// model list, endpoint policy or overrides for later samples. Disabled
	// channels remain testable for explicit administrator recovery, just as in
	// testChannel; deletion and removal of the selected model do not.
	current, err := model.GetChannelById(channel.Id, true)
	if err != nil {
		return service.FingerprintProbeResult{}, errors.New("fingerprint request could not be completed")
	}
	if err := validateFingerprintChannel(current, modelName); err != nil {
		return service.FingerprintProbeResult{}, err
	}
	// An empty endpoint stays automatic through admission. Do not turn it into
	// a client-authored Chat override before the current channel is reloaded.
	if endpointType != "" {
		if err := validateFingerprintEndpoint(endpointType); err != nil {
			return service.FingerprintProbeResult{}, err
		}
	}
	keyIndex := -1
	if selection.HasIndex {
		keyIndex = selection.Index
	}
	if _, err := pinChannelTestKey(current, keyIndex, selection.EntryID, selection.Revision, modelName); err != nil {
		return service.FingerprintProbeResult{}, errors.New("the selected key is unavailable for this model")
	}
	// Conversion and configured policies still run. The final adaptor guard
	// rejects any body that no longer carries only this exact preview text.
	probeCtx := context.WithValue(ctx, channelTestPromptKey, testPrompt{
		Prompt:    challenge.Prompt,
		MaxTokens: maxOutputTokens,
	})
	result := runChannelTest(probeCtx, current, channelTestSpec{
		userID: testUserID, model: modelName, endpoint: endpointType, keyIndex: keyIndex,
		keyEntryID: selection.EntryID, keyEntryRevision: selection.Revision,
	})
	probeResult := service.FingerprintProbeResult{
		PromptTokens: result.promptTokens,
		OutputTokens: result.completionTokens,
	}
	if errors.Is(result.localErr, errFingerprintWirePromptChanged) {
		return probeResult, errFingerprintWirePromptChanged
	}
	if result.newAPIError != nil {
		// Provider code/type/message fields are untrusted too. Only project the
		// numeric status into the retained public job; leave the diagnostic's
		// internal error and already-recorded usage untouched.
		return probeResult, fmt.Errorf("fingerprint request failed (HTTP %d)", result.newAPIError.StatusCode)
	}
	if result.localErr != nil {
		return probeResult, errors.New("fingerprint request could not be completed")
	}
	if endpointType == "" && result.context != nil && result.context.Request != nil {
		format := result.context.Request.URL.Path
		switch {
		case format == "/v1/messages":
			endpointType = "anthropic"
		case format == "/v1/responses":
			endpointType = "openai-response"
		case strings.Contains(format, ":generateContent"):
			endpointType = "gemini"
		default:
			endpointType = "openai"
		}
	}
	text, err := fingerprintReplyText(result.responseBody, endpointType)
	if err != nil {
		return probeResult, err
	}
	probeResult.Text = text
	probeResult.ParsedCount = countChallengeNumbers(text)
	return probeResult, nil
}
