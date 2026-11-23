package service

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// goalTurnBody builds a relay request body whose only goal-related content is
// the canonical client metadata entry.
func goalTurnBody(metadata string) []byte {
	return []byte(`{"model":"gpt-5","client_metadata":{"x-codex-turn-metadata":` + metadata + `}}`)
}

func goalTurnHeader(metadata string) http.Header {
	header := http.Header{}
	header.Set(codexTurnMetadataKey, metadata)
	return header
}

func TestClassifyGoalTurnRequestBodyMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      []byte
		header    http.Header
		triggered bool
		source    GoalTurnSource
		malformed bool
	}{
		{
			name:      "canonical body metadata marks a goal-triggered turn",
			body:      goalTurnBody(`"{\"turn_trigger\":\"goal\"}"`),
			triggered: true,
			source:    GoalTurnSourceBody,
		},
		{
			name:      "another turn trigger is not a goal turn",
			body:      goalTurnBody(`"{\"turn_trigger\":\"user\"}"`),
			triggered: false,
			source:    GoalTurnSourceBody,
		},
		{
			name:      "descriptor without a trigger is not a goal turn",
			body:      goalTurnBody(`"{\"session_id\":\"abc\"}"`),
			triggered: false,
			source:    GoalTurnSourceBody,
		},
		{
			name:      "unrelated metadata keys never trigger",
			body:      []byte(`{"model":"gpt-5","client_metadata":{"x-other":"{\"turn_trigger\":\"goal\"}"}}`),
			triggered: false,
			source:    GoalTurnSourceNone,
		},
		{
			name:      "header compatibility form is used when the body has no descriptor",
			body:      []byte(`{"model":"gpt-5"}`),
			header:    goalTurnHeader(`{"turn_trigger":"goal"}`),
			triggered: true,
			source:    GoalTurnSourceHeader,
		},
		{
			name:      "body metadata wins over a conflicting header",
			body:      goalTurnBody(`"{\"turn_trigger\":\"user\"}"`),
			header:    goalTurnHeader(`{"turn_trigger":"goal"}`),
			triggered: false,
			source:    GoalTurnSourceBody,
		},
		{
			name:      "body metadata wins over an absent header",
			body:      goalTurnBody(`"{\"turn_trigger\":\"goal\"}"`),
			header:    goalTurnHeader(`{"turn_trigger":"user"}`),
			triggered: true,
			source:    GoalTurnSourceBody,
		},
		{
			name:      "malformed body metadata stays unknown instead of falling back",
			body:      goalTurnBody(`"{not json"`),
			header:    goalTurnHeader(`{"turn_trigger":"goal"}`),
			triggered: false,
			source:    GoalTurnSourceNone,
			malformed: true,
		},
		{
			name:      "non-string trigger value is not trusted",
			body:      goalTurnBody(`"{\"turn_trigger\":1}"`),
			triggered: false,
			source:    GoalTurnSourceNone,
			malformed: true,
		},
		{
			name:      "a descriptor object is not the canonical JSON string",
			body:      goalTurnBody(`{"turn_trigger":"goal"}`),
			header:    goalTurnHeader(`{"turn_trigger":"goal"}`),
			malformed: true,
		},
		{
			name:      "a numeric descriptor is malformed",
			body:      goalTurnBody(`42`),
			malformed: true,
		},
		{
			name:      "oversized descriptor is rejected within bounds",
			body:      goalTurnBody(`"{\"turn_trigger\":\"goal\",\"padding\":\"` + strings.Repeat("x", maxCodexTurnMetadataBytes) + `\"}"`),
			triggered: false,
			source:    GoalTurnSourceNone,
			malformed: true,
		},
		{
			name:      "empty body and no header is unknown",
			body:      nil,
			triggered: false,
			source:    GoalTurnSourceNone,
		},
		{
			name:      "goal tools in the request are not a signal",
			body:      []byte(`{"model":"gpt-5","tools":[{"type":"function","name":"create_goal"}],"input":[{"type":"message","content":"/goal ship it"}]}`),
			triggered: false,
			source:    GoalTurnSourceNone,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signal := ClassifyGoalTurnRequest(tc.body, tc.header)
			assert.Equal(t, tc.triggered, signal.Triggered)
			assert.Equal(t, tc.source, signal.Source)
			assert.Equal(t, tc.malformed, signal.Malformed)
		})
	}
}

func TestClassifyGoalTurnMessagePrefersTheMessageDescriptor(t *testing.T) {
	connection := goalTurnHeader(`{"turn_trigger":"goal"}`)

	t.Run("per-message goal metadata applies to that turn", func(t *testing.T) {
		signal := ClassifyGoalTurnMessage([]byte(`{"x-codex-turn-metadata":"{\"turn_trigger\":\"goal\"}"}`), nil)
		assert.True(t, signal.Triggered)
		assert.Equal(t, GoalTurnSourceBody, signal.Source)
	})

	t.Run("a non-goal message on a goal connection is not goal-triggered", func(t *testing.T) {
		signal := ClassifyGoalTurnMessage([]byte(`{"x-codex-turn-metadata":"{\"turn_trigger\":\"user\"}"}`), connection)
		assert.False(t, signal.Triggered)
		assert.Equal(t, GoalTurnSourceBody, signal.Source)
	})

	t.Run("the connection header is only a fallback", func(t *testing.T) {
		signal := ClassifyGoalTurnMessage(nil, connection)
		assert.True(t, signal.Triggered)
		assert.Equal(t, GoalTurnSourceHeader, signal.Source)
	})

	t.Run("a malformed message descriptor does not inherit the header", func(t *testing.T) {
		signal := ClassifyGoalTurnMessage([]byte(`{"x-codex-turn-metadata":{"turn_trigger":"goal"}}`), connection)
		assert.False(t, signal.Triggered)
		assert.True(t, signal.Malformed)
	})
}

func TestApplyGoalTurnConstraintReplacesThePreviousClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	goalTurnFilterCount := func() int {
		count := 0
		for _, filter := range GetChannelConstraints(c).Filters {
			if filter.Kind == dto.FilterGoalTurn {
				count++
			}
		}
		return count
	}

	ApplyGoalTurnConstraint(c, GoalTurnSignal{Triggered: true})
	require.Equal(t, 1, goalTurnFilterCount())

	// A later non-goal message on the same connection must not inherit the
	// previous message's restriction.
	ApplyGoalTurnConstraint(c, GoalTurnSignal{})
	assert.Equal(t, 0, goalTurnFilterCount())

	ApplyGoalTurnConstraint(c, GoalTurnSignal{Triggered: true, Source: GoalTurnSourceBody})
	assert.Equal(t, 1, goalTurnFilterCount())
}

func TestApplyGoalTurnConstraintFromRequestReadsTheReusableBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name        string
		body        string
		contentType string
		header      string
		want        bool
	}{
		{
			name:        "body metadata marks the turn",
			body:        `{"model":"gpt-5","client_metadata":{"x-codex-turn-metadata":"{\"turn_trigger\":\"goal\"}"}}`,
			contentType: "application/json",
			want:        true,
		},
		{
			name:        "compatibility header is the fallback",
			body:        `{"model":"gpt-5"}`,
			contentType: "application/json",
			header:      `{"turn_trigger":"goal"}`,
			want:        true,
		},
		{
			name:        "ordinary request stays eligible",
			body:        `{"model":"gpt-5","tools":[{"type":"function","name":"create_goal"}]}`,
			contentType: "application/json",
			want:        false,
		},
		{
			name:        "a body without turn metadata is not a goal turn",
			body:        "--boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ngpt-5\r\n--boundary--\r\n",
			contentType: "multipart/form-data; boundary=boundary",
			want:        false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			if tc.header != "" {
				c.Request.Header.Set(codexTurnMetadataKey, tc.header)
			}

			ApplyGoalTurnConstraintFromRequest(c)

			found := slices.ContainsFunc(GetChannelConstraints(c).Filters, func(filter dto.ChannelFilter) bool {
				return filter.Kind == dto.FilterGoalTurn
			})
			assert.Equal(t, tc.want, found)
		})
	}
}

func TestGoalTurnFilterMatchesChannelSetting(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	setting := func(value string) *string { return &value }
	denied := &model.Channel{Setting: setting(`{"deny_goal":true}`)}
	allowed := &model.Channel{Setting: setting(`{"deny_goal":false}`)}
	unset := &model.Channel{Setting: setting(`{}`)}
	filters := []dto.ChannelFilter{{Kind: dto.FilterGoalTurn}}

	ok, _ := model.ChannelSatisfiesFilters(denied, "gpt-5", filters)
	assert.False(t, ok, "a channel that denies goal turns must be skipped")

	ok, _ = model.ChannelSatisfiesFilters(allowed, "gpt-5", filters)
	assert.True(t, ok)

	ok, _ = model.ChannelSatisfiesFilters(unset, "gpt-5", filters)
	assert.True(t, ok, "an unset setting keeps the channel eligible")

	// The same filters never exclude a channel for ordinary traffic.
	ok, _ = model.ChannelSatisfiesFilters(denied, "gpt-5", nil)
	assert.True(t, ok)
}
