package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// UpstreamRejection classifies a 429/402-class upstream refusal so the retry
// decision can name it and a later waiting feature can act on a recognized
// concurrency rejection. It never changes the retry outcome by itself and never
// feeds channel health: an ambiguous message is not evidence of a bad channel.
type UpstreamRejection string

const (
	UpstreamRejectionNone        UpstreamRejection = ""
	UpstreamRejectionConcurrency UpstreamRejection = "upstream_concurrency"
	UpstreamRejectionRateLimit   UpstreamRejection = "upstream_rate_limit"
	UpstreamRejectionQuota       UpstreamRejection = "upstream_quota_exhausted"
)

// Structured codes are checked before the narrow compatibility messages below.
var upstreamConcurrencyCodes = map[string]struct{}{
	"concurrency_limit_exceeded":    {},
	"concurrency_exceeded":          {},
	"concurrent_requests":           {},
	"concurrent_requests_exceeded":  {},
	"too_many_concurrent_requests":  {},
	"rate_limit_concurrency":        {},
	"concurrency":                   {},
	"upstream_concurrency_limit":    {},
	"resource_exhausted_concurrent": {},
}

var upstreamQuotaCodes = map[string]struct{}{
	"insufficient_quota":           {},
	"quota_exceeded":               {},
	"billing_hard_limit_reached":   {},
	"billing_not_active":           {},
	"insufficient_balance":         {},
	"credit_balance_too_low":       {},
	"exceeded_current_quota_error": {},
	"organization_quota_exceeded":  {},
	"monthly_quota_exceeded":       {},
}

var upstreamRateLimitCodes = map[string]struct{}{
	"rate_limit_exceeded":          {},
	"rate_limit_error":             {},
	"requests_per_minute_exceeded": {},
	"tokens_per_minute_exceeded":   {},
	"too_many_requests":            {},
	"rate_limited":                 {},
}

// ClassifyUpstreamRejectionCode classifies a structured upstream error code or
// type. Empty or unrecognized values mean no explicit classification.
func ClassifyUpstreamRejectionCode(code, errorType string) UpstreamRejection {
	result := UpstreamRejectionNone
	for _, candidate := range []string{code, errorType} {
		normalized := strings.ToLower(strings.TrimSpace(candidate))
		if normalized == "" {
			continue
		}
		if _, ok := upstreamQuotaCodes[normalized]; ok {
			return UpstreamRejectionQuota
		}
		if _, ok := upstreamConcurrencyCodes[normalized]; ok {
			result = UpstreamRejectionConcurrency
		} else if _, ok := upstreamRateLimitCodes[normalized]; ok && result == UpstreamRejectionNone {
			result = UpstreamRejectionRateLimit
		}
	}
	return result
}

// ClassifyUpstreamError classifies fields from an actual upstream error envelope.
// Structured quota/rate codes take precedence. Providers that omit a useful code
// can use one known, bounded refusal message; arbitrary text containing the same
// words never qualifies. Callers must not pass prompt content or model output.
func ClassifyUpstreamError(code, errorType, message string) UpstreamRejection {
	if rejection := ClassifyUpstreamRejectionCode(code, errorType); rejection != UpstreamRejectionNone {
		return rejection
	}
	for _, field := range []string{code, errorType} {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "", "unknown_error", "server_error", "upstream_error", "api_error", "internal_server_error", "stream_error":
		default:
			return UpstreamRejectionNone
		}
	}
	if len(message) > 512 {
		return UpstreamRejectionNone
	}
	message = strings.ToLower(strings.TrimSpace(message))
	message, _ = strings.CutSuffix(message, ".")
	message, _ = strings.CutPrefix(message, "stream disconnected before completion: ")
	if message == "concurrency limit exceeded for user" {
		return UpstreamRejectionConcurrency
	}
	return UpstreamRejectionNone
}

// ClassifyUpstreamRejection keeps local and transport failures out of upstream
// waiting. A bare upstream 429 remains a general rate limit, never concurrency.
func ClassifyUpstreamRejection(err *types.NewAPIError) UpstreamRejection {
	if err == nil || RelayErrorSource(err) != "upstream" {
		return UpstreamRejectionNone
	}
	if rejection := RecognizedUpstreamRejection(err); rejection != UpstreamRejectionNone {
		return rejection
	}
	if err.StatusCode == 429 {
		return UpstreamRejectionRateLimit
	}
	return UpstreamRejectionNone
}

// StructuredUpstreamRejection reports only an explicitly recognized code, so a
// bare 429 keeps the existing generic retry reason instead of being renamed.
func StructuredUpstreamRejection(err *types.NewAPIError) UpstreamRejection {
	if err == nil || RelayErrorSource(err) != "upstream" {
		return UpstreamRejectionNone
	}
	// The relay error keeps only the mapped type, so the upstream's own code and
	// type are read from the preserved relay error.
	switch relayError := err.RelayError.(type) {
	case types.OpenAIError:
		return ClassifyUpstreamRejectionCode(upstreamFieldText(relayError.Code), relayError.Type)
	case *types.OpenAIError:
		if relayError == nil {
			return UpstreamRejectionNone
		}
		return ClassifyUpstreamRejectionCode(upstreamFieldText(relayError.Code), relayError.Type)
	case types.ClaudeError:
		return ClassifyUpstreamRejectionCode(relayError.Type, "")
	case *types.ClaudeError:
		if relayError != nil {
			return ClassifyUpstreamRejectionCode(relayError.Type, "")
		}
	}
	return ClassifyUpstreamRejectionCode(string(err.GetErrorCode()), string(err.GetErrorType()))
}

// RecognizedUpstreamRejection omits the generic HTTP-status fallback. Retry logs
// can therefore name known concurrency/quota/rate refusals while retaining the
// existing reason for a bare 429.
func RecognizedUpstreamRejection(err *types.NewAPIError) UpstreamRejection {
	if err == nil || RelayErrorSource(err) != "upstream" {
		return UpstreamRejectionNone
	}
	if err.StatusCode == 402 {
		return UpstreamRejectionQuota
	}
	if rejection := StructuredUpstreamRejection(err); rejection != UpstreamRejectionNone {
		return rejection
	}
	switch relayError := err.RelayError.(type) {
	case types.OpenAIError:
		return ClassifyUpstreamError(upstreamFieldText(relayError.Code), relayError.Type, relayError.Message)
	case *types.OpenAIError:
		if relayError != nil {
			return ClassifyUpstreamError(upstreamFieldText(relayError.Code), relayError.Type, relayError.Message)
		}
	case types.ClaudeError:
		return ClassifyUpstreamError("", relayError.Type, relayError.Message)
	case *types.ClaudeError:
		if relayError != nil {
			return ClassifyUpstreamError("", relayError.Type, relayError.Message)
		}
	}
	return UpstreamRejectionNone
}

// upstreamFieldText renders a structured upstream code that may arrive as a
// string or as a number.
func upstreamFieldText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// UpstreamModelUnavailableEvidence names how a definite "this model is missing"
// refusal was recognized. It is a fixed code, never upstream text.
type UpstreamModelUnavailableEvidence string

const (
	UpstreamModelUnavailableNone    UpstreamModelUnavailableEvidence = ""
	UpstreamModelUnavailableByCode  UpstreamModelUnavailableEvidence = "error_code"
	UpstreamModelUnavailableMessage UpstreamModelUnavailableEvidence = "error_message"

	// The one anchored upstream sentence that names a missing model for the
	// whole gateway. It is matched only with the attempted model and only for
	// 404/410, so ordinary prose or model output can never qualify.
	upstreamModelUnavailableTemplate = `model %q is not supported by any configured account in this group`
)

// structuredModelMissingCodes are the upstream codes that mean the model does
// not exist for this credential. A generic model_not_supported is deliberately
// absent: it also describes a protocol or endpoint mismatch, so it must not
// persist channel state.
var structuredModelMissingCodes = map[string]struct{}{
	"model_not_found":      {},
	"model_not_available":  {},
	"models_not_found":     {},
	"model_does_not_exist": {},
}

// upstreamErrorEnvelope returns the structured fields of an actual upstream
// error envelope. ok is false for local errors that only carry a message.
func upstreamErrorEnvelope(err *types.NewAPIError) (code, errorType, message string, ok bool) {
	if err == nil {
		return "", "", "", false
	}
	switch relayError := err.RelayError.(type) {
	case types.OpenAIError:
		return upstreamFieldText(relayError.Code), relayError.Type, relayError.Message, true
	case *types.OpenAIError:
		if relayError != nil {
			return upstreamFieldText(relayError.Code), relayError.Type, relayError.Message, true
		}
	case types.ClaudeError:
		return "", relayError.Type, relayError.Message, true
	case *types.ClaudeError:
		if relayError != nil {
			return "", relayError.Type, relayError.Message, true
		}
	}
	return "", "", "", false
}

// ClassifyUpstreamModelUnavailable reports a definite upstream refusal that
// names the attempted model as unavailable. Only a real upstream envelope with
// HTTP 404/410 qualifies: an endpoint, path, parameter or protocol refusal, an
// authentication or capacity failure, and any local error keep ordinary retry
// behavior and never persist channel state. attemptedModel is the model the
// attempt actually sent upstream; the anchored sentence must name exactly it.
func ClassifyUpstreamModelUnavailable(err *types.NewAPIError, attemptedModel string) UpstreamModelUnavailableEvidence {
	if err == nil || RelayErrorSource(err) != "upstream" {
		return UpstreamModelUnavailableNone
	}
	if err.StatusCode != http.StatusNotFound && err.StatusCode != http.StatusGone {
		return UpstreamModelUnavailableNone
	}
	code, errorType, message, ok := upstreamErrorEnvelope(err)
	if !ok {
		return UpstreamModelUnavailableNone
	}
	// Conflicting auth/quota/capacity codes are not model capability evidence.
	if ClassifyUpstreamRejectionCode(code, errorType) != UpstreamRejectionNone {
		return UpstreamModelUnavailableNone
	}
	for _, field := range []string{code, errorType} {
		normalized := strings.ToLower(strings.TrimSpace(field))
		if _, known := structuredModelMissingCodes[normalized]; known {
			continue
		}
		switch normalized {
		case "", "invalid_request_error", "not_found_error", "not_found", "unknown_error", "server_error", "upstream_error", "api_error", "error", "404":
		default:
			return UpstreamModelUnavailableNone
		}
	}
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	for _, conflicting := range []string{"api key", "api_key", "credential", "authentication", "unauthorized", "permission", "quota", "rate limit", "concurrency", "overloaded", "capacity", "balance", "billing"} {
		if strings.Contains(normalizedMessage, conflicting) {
			return UpstreamModelUnavailableNone
		}
	}
	if mentionsUnsupportedScope(normalizedMessage) {
		return UpstreamModelUnavailableNone
	}
	for _, field := range []string{code, errorType} {
		if _, matched := structuredModelMissingCodes[strings.ToLower(strings.TrimSpace(field))]; matched {
			return UpstreamModelUnavailableByCode
		}
	}
	if attemptedModel == "" {
		return UpstreamModelUnavailableNone
	}
	normalizedMessage = strings.TrimSuffix(normalizedMessage, ".")
	if normalizedMessage == fmt.Sprintf(upstreamModelUnavailableTemplate, strings.ToLower(attemptedModel)) {
		return UpstreamModelUnavailableMessage
	}
	return UpstreamModelUnavailableNone
}

// mentionsUnsupportedScope reports a refusal about the endpoint, path or
// parameters of a request rather than a missing model.
func mentionsUnsupportedScope(message string) bool {
	for _, token := range []string{"endpoint", "parameter", "path ", "route", "protocol", "operation", "modality", "input type"} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

// UpstreamRejectionStatusCode maps a rejection to the status the gateway
// reports when it turns a stream-embedded refusal into a relay error.
func UpstreamRejectionStatusCode(rejection UpstreamRejection) int {
	switch rejection {
	case UpstreamRejectionConcurrency, UpstreamRejectionRateLimit:
		return 429
	case UpstreamRejectionQuota:
		return 402
	default:
		return 502
	}
}
