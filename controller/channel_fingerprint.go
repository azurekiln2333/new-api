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
	Model        string `json:"model"`
	EndpointType string `json:"endpoint_type"`
	Count        int    `json:"count"`
	TokenBudget  int    `json:"token_budget"`
}

func StartChannelFingerprint(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		common.ApiErrorMsg(c, "invalid channel id")
		return
	}
	var request fingerprintProbeRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		modelName = fingerprintDefaultModel(channel)
	}
	if modelName == "" {
		common.ApiErrorMsg(c, "a model is required")
		return
	}
	if err := validateFingerprintChannel(channel, modelName); err != nil {
		common.ApiError(c, err)
		return
	}
	endpointType := normalizeChannelTestEndpoint(channel, request.EndpointType)
	if endpointType == "" {
		endpointType = "openai"
	}
	if err := validateFingerprintEndpoint(endpointType); err != nil {
		common.ApiError(c, err)
		return
	}
	job, err := service.StartChannelFingerprint(
		c.Request.Context(),
		service.FingerprintRequest{
			ChannelID:   channel.Id,
			ChannelName: channel.Name,
			Model:       modelName,
			Endpoint:    endpointType,
			Count:       request.Count,
			TokenBudget: request.TokenBudget,
		},
		func(ctx context.Context, challenge lmfp.Challenge, maxOutputTokens uint) (service.FingerprintProbeResult, error) {
			// Retain only the fixed identity, not the starting credentials or the
			// resolved automatic endpoint. Each sample resolves current policy.
			return runFingerprintProbe(ctx, &model.Channel{Id: channelID}, testUserID, modelName, request.EndpointType, challenge, maxOutputTokens)
		},
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, job)
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

func GetChannelFingerprint(c *gin.Context) {
	job, err := fingerprintJobForChannel(c)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, err)
		return
	}
	common.ApiSuccess(c, job)
}

func CancelChannelFingerprint(c *gin.Context) {
	if _, err := fingerprintJobForChannel(c); err != nil {
		common.ApiErrorStatus(c, http.StatusNotFound, err)
		return
	}
	job, err := service.CancelChannelFingerprint(c.Param("job_id"))
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
	endpointType = normalizeChannelTestEndpoint(current, endpointType)
	if endpointType == "" {
		endpointType = "openai"
	}
	if err := validateFingerprintEndpoint(endpointType); err != nil {
		return service.FingerprintProbeResult{}, err
	}
	// The probe prompt and its output budget travel through the context, so the
	// channel test keeps its existing signature and every other caller unchanged.
	// It owns admission and post-wait revalidation: never nest another lease here.
	probeCtx := context.WithValue(ctx, channelTestPromptKey, testPrompt{
		Prompt:    challenge.Prompt,
		MaxTokens: maxOutputTokens,
	})
	result := testChannel(probeCtx, current, testUserID, modelName, endpointType, false, -1)
	probeResult := service.FingerprintProbeResult{
		PromptTokens: result.promptTokens,
		OutputTokens: result.completionTokens,
	}
	if result.newAPIError != nil {
		return probeResult, fmt.Errorf("fingerprint request failed: %s (HTTP %d)", result.newAPIError.GetErrorCode(), result.newAPIError.StatusCode)
	}
	if result.localErr != nil {
		return probeResult, errors.New("fingerprint request could not be completed")
	}
	text, err := fingerprintReplyText(result.responseBody, endpointType)
	if err != nil {
		return probeResult, err
	}
	probeResult.Text = text
	probeResult.ParsedCount = countChallengeNumbers(text)
	return probeResult, nil
}
