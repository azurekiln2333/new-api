package service

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// Codex attributes a turn that it started automatically for a persisted goal by
// setting turn_trigger to "goal" inside the turn descriptor. The descriptor
// travels as a JSON string in client_metadata["x-codex-turn-metadata"], and the
// same JSON object is also sent as a compatibility header. Only that structured
// signal is treated as evidence: advertised goal tools, User-Agent strings and
// prose mentioning a goal all appear in ordinary traffic, so none of them is a
// goal-triggered turn.
const (
	codexTurnMetadataKey      = "x-codex-turn-metadata"
	codexTurnTriggerGoal      = "goal"
	maxCodexTurnMetadataBytes = 4 << 10
)

// GoalTurnSource records where the classified turn descriptor came from.
type GoalTurnSource string

const (
	// GoalTurnSourceNone means no turn descriptor was present, so the turn is
	// unknown rather than proven not to be goal-triggered.
	GoalTurnSourceNone   GoalTurnSource = ""
	GoalTurnSourceBody   GoalTurnSource = "body"
	GoalTurnSourceHeader GoalTurnSource = "header"
)

// GoalTurnSignal is the goal-turn classification of one request or message.
type GoalTurnSignal struct {
	// Triggered is true only for a recognized goal-triggered turn.
	Triggered bool
	Source    GoalTurnSource
	// Malformed reports a descriptor that was present but unusable, such as an
	// oversized, non-string or invalid-JSON value. The turn is then unknown and
	// never treated as goal-triggered.
	Malformed bool
}

// codexTurnDescriptor is the bounded subset of the turn descriptor that carries
// the trigger. Unknown fields stay unparsed.
type codexTurnDescriptor struct {
	TurnTrigger *string `json:"turn_trigger"`
}

// codexTurnMetadataEnvelope reads only the canonical metadata entry, so the rest
// of the request body is never searched for goal-related text.
type codexTurnMetadataEnvelope struct {
	ClientMetadata map[string]json.RawMessage `json:"client_metadata"`
}

// ClassifyGoalTurnRequest classifies one HTTP relay request. The body metadata is
// canonical, so it wins whenever it is present; the compatibility header is used
// only when the body carries no descriptor at all.
func ClassifyGoalTurnRequest(body []byte, header http.Header) GoalTurnSignal {
	if len(body) > 0 {
		var envelope codexTurnMetadataEnvelope
		if err := common.Unmarshal(body, &envelope); err == nil {
			if raw, ok := envelope.ClientMetadata[codexTurnMetadataKey]; ok {
				return classifyCodexTurnMetadata(raw)
			}
		}
	}
	return classifyCodexTurnHeader(header)
}

// ClassifyGoalTurnMessage classifies one Responses WebSocket message. A message
// descriptor describes that turn, so it takes precedence over the connection
// handshake header, which only describes how the connection was opened. The
// argument is the message's own client_metadata object. The caller may supply
// the handshake header for the first turn only; later turns must pass nil.
func ClassifyGoalTurnMessage(clientMetadata json.RawMessage, header http.Header) GoalTurnSignal {
	if len(clientMetadata) > 0 {
		var metadata map[string]json.RawMessage
		if err := common.Unmarshal(clientMetadata, &metadata); err == nil {
			if raw, ok := metadata[codexTurnMetadataKey]; ok {
				return classifyCodexTurnMetadata(raw)
			}
		}
	}
	return classifyCodexTurnHeader(header)
}

// classifyCodexTurnHeader reads the compatibility header form, which carries the
// same JSON object as its value.
func classifyCodexTurnHeader(header http.Header) GoalTurnSignal {
	if header == nil {
		return GoalTurnSignal{}
	}
	values := header.Values(codexTurnMetadataKey)
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if len(value) > maxCodexTurnMetadataBytes {
			return GoalTurnSignal{Malformed: true}
		}
		return classifyTurnDescriptor([]byte(value), GoalTurnSourceHeader)
	}
	return GoalTurnSignal{}
}

// classifyCodexTurnMetadata reads the canonical body form, where the descriptor
// is a JSON string holding the same JSON object.
func classifyCodexTurnMetadata(raw json.RawMessage) GoalTurnSignal {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return GoalTurnSignal{}
	}
	if len(trimmed) > maxCodexTurnMetadataBytes {
		return GoalTurnSignal{Malformed: true}
	}
	var encoded string
	if err := common.UnmarshalJsonStr(trimmed, &encoded); err != nil {
		return GoalTurnSignal{Malformed: true}
	}
	return classifyTurnDescriptor([]byte(encoded), GoalTurnSourceBody)
}

func classifyTurnDescriptor(data []byte, source GoalTurnSource) GoalTurnSignal {
	if len(data) > maxCodexTurnMetadataBytes {
		return GoalTurnSignal{Malformed: true}
	}
	var descriptor codexTurnDescriptor
	if err := common.Unmarshal(data, &descriptor); err != nil {
		return GoalTurnSignal{Malformed: true}
	}
	if descriptor.TurnTrigger == nil {
		return GoalTurnSignal{Source: source}
	}
	return GoalTurnSignal{
		Triggered: *descriptor.TurnTrigger == codexTurnTriggerGoal,
		Source:    source,
	}
}

// ApplyGoalTurnConstraintFromRequest classifies an HTTP relay request from its
// reusable body and headers, then applies the result to its channel constraints.
// A body that carries no turn descriptor falls back to the compatibility header.
func ApplyGoalTurnConstraintFromRequest(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	var envelope codexTurnMetadataEnvelope
	if err := common.UnmarshalBodyReusable(c, &envelope); err == nil {
		if raw, ok := envelope.ClientMetadata[codexTurnMetadataKey]; ok {
			ApplyGoalTurnConstraint(c, classifyCodexTurnMetadata(raw))
			return
		}
	}
	ApplyGoalTurnConstraint(c, classifyCodexTurnHeader(c.Request.Header))
}

// ApplyGoalTurnConstraint replaces the request's goal-turn filter with one that
// matches this classification, so channel eligibility is the same on the initial
// selection and on every retry. A connection that serves several messages calls
// this per message instead of accumulating filters.
func ApplyGoalTurnConstraint(c *gin.Context, signal GoalTurnSignal) {
	if c == nil {
		return
	}
	constraints := GetChannelConstraints(c)
	constraints.Filters = slices.DeleteFunc(constraints.Filters, func(filter dto.ChannelFilter) bool {
		return filter.Kind == dto.FilterGoalTurn
	})
	if !signal.Triggered {
		return
	}
	constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterGoalTurn})
}
