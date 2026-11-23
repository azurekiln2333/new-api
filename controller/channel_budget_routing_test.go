package controller

import (
	"errors"
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
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelBudgetHTTPAdmissionRoutesWithoutUsingAnUpstreamRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pinned bool
		strict bool
	}{
		{name: "unrestricted"},
		{name: "pinned", pinned: true},
		{name: "strict affinity", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) { _, _, _ = ws.ReadMessage() })
			activateRuntimeBudgetFixture(t, fixture)
			require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
			service.InitHttpClient()
			t.Setenv("CHANNEL_WAIT_TIMEOUT_SECONDS", "1")
			previousRetries := common.RetryTimes
			common.RetryTimes = 0
			affinity := operation_setting.GetChannelAffinitySetting()
			previousAffinity := *affinity
			*affinity = operation_setting.ChannelAffinitySetting{RetryAvoidFailedChannels: false}
			t.Cleanup(func() { common.RetryTimes, *affinity = previousRetries, previousAffinity })

			// The first choice is visible and has positive capacity, but 500
			// units cannot cover this request's 1000-unit estimate.
			require.NoError(t, model.DB.Model(fixture.channel).Update("priority", 10).Error)
			require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", fixture.channel.Id).Update("priority", 10).Error)
			_, err := model.SetChannelBudgetLimit(t.Context(), model.BudgetScopeChannel, int64(fixture.channel.Id), 0, true, 500)
			require.NoError(t, err)
			var refusedCalls, acceptedCalls atomic.Int32
			fixture.httpUpstream = func(w http.ResponseWriter, _ *http.Request) {
				refusedCalls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				acceptedCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"budget-fallback\",\"status\":\"completed\",\"model\":\"ws-billing\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(fallback.Close)
			channel := &model.Channel{Name: "budget-fallback", Type: constant.ChannelTypeOpenAI, Key: "test-fallback-key", BaseURL: &fallback.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "ws-billing", Priority: common.GetPointer(int64(5))}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() { require.NoError(t, channel.Delete()) })

			router := gin.New()
			router.POST("/v1/responses", middleware.BodyStorageCleanup(), middleware.TokenAuth(), func(c *gin.Context) {
				if tc.pinned {
					service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: fixture.channel.Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
				}
				if tc.strict {
					service.RequestPolicy(c).SessionMode = "strict"
				}
			}, middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"ws-billing","input":"budget fixture","stream":true}`))
			request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Zero(t, refusedCalls.Load(), "no request reaches an over-budget channel")

			var reservations []model.ChannelBudgetReservation
			require.NoError(t, model.DB.Where("user_id = ?", fixture.user.Id).Find(&reservations).Error)
			if tc.pinned || tc.strict {
				assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
				assert.Zero(t, acceptedCalls.Load(), "a local budget refusal cannot break a channel pin")
				assert.Empty(t, reservations)
				require.Eventually(t, func() bool {
					var user model.User
					var token model.Token
					return model.DB.First(&user, fixture.user.Id).Error == nil && model.DB.First(&token, fixture.token.Id).Error == nil && user.Quota == 100000 && token.RemainQuota == 3000
				}, 3*time.Second, 10*time.Millisecond, "the existing funding hold is refunded")
				return
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), "budget-fallback")
			assert.EqualValues(t, 1, acceptedCalls.Load())
			require.Len(t, reservations, 1, "only the accepted upstream attempt creates a reservation")
			assert.Equal(t, channel.Id, reservations[0].ChannelId)
			assert.Equal(t, channel.ChannelKeyEntryID(0), reservations[0].AllocationId)
			assert.EqualValues(t, 1000, reservations[0].SettledQuota)
			assertResponsesWSAccounting(t, fixture, []int{1000})
		})
	}
}

func TestChannelBudgetRealtimeRolloutGuard(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		active, flag, readError bool
	}{
		{name: "active unlimited account", active: true, flag: true},
		{name: "durable active with process flag off", active: true},
		{name: "activation read error", readError: true},
		{name: "inactive baseline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResponsesWSBillingTest(t, `tier("tokens", p * 2 + c * 3)`, func(ws *websocket.Conn, _ *http.Request) { _, _, _ = ws.ReadMessage() })
			fixture.closeAndWait(t)
			activateRuntimeBudgetFixture(t, fixture)
			db := model.DB
			if !tc.active {
				require.NoError(t, db.Where(map[string]any{"key": "internal.channel_budget.active_v1"}).Delete(&model.Option{}).Error)
			}
			t.Setenv("CHANNEL_BUDGET_ENABLED", fmt.Sprint(tc.flag))
			// Activation is cached by handle. A fresh session tests durable lookup
			// without closing the working auth/funding database or adding a hook.
			model.DB = db.Session(&gorm.Session{NewDB: true})
			t.Cleanup(func() { model.DB = db })
			model.InitChannelCache()
			var failedReads atomic.Int32
			requestDone := make(chan struct{})
			engine := gin.New()
			engine.GET("/v1/realtime", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) {
				defer close(requestDone)
				if tc.readError {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:activation_read_failure", func(tx *gorm.DB) {
						if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Option" {
							failedReads.Add(1)
							tx.AddError(errors.New("activation marker read failed"))
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("test:activation_read_failure")) })
				}
				Relay(c, types.RelayFormatOpenAIRealtime)
			})
			gateway := httptest.NewServer(engine)
			t.Cleanup(gateway.Close)
			headers := http.Header{"Authorization": {"Bearer sk-" + fixture.token.Key}}
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/realtime?model=ws-billing", headers)
			if tc.active || tc.readError {
				if connection != nil {
					_ = connection.Close()
				}
				require.Error(t, err)
				require.NotNil(t, response)
				body, readErr := io.ReadAll(response.Body)
				require.NoError(t, readErr)
				require.NoError(t, response.Body.Close())
				assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode, string(body))
				assert.Zero(t, fixture.connections.Load(), "refused before upstream dial")
				if tc.readError {
					assert.Positive(t, failedReads.Load())
				} else {
					assert.Contains(t, string(body), "continuous realtime sessions are not available")
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
				require.Eventually(t, func() bool { return fixture.connections.Load() == 1 }, 3*time.Second, 10*time.Millisecond)
				require.NoError(t, connection.Close())
			}
			select {
			case <-requestDone:
			case <-time.After(3 * time.Second):
				t.Fatal("Realtime handler did not finish")
			}
			var reservations int64
			require.NoError(t, db.Model(&model.ChannelBudgetReservation{}).Where("user_id = ?", fixture.user.Id).Count(&reservations).Error)
			assert.Zero(t, reservations)
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return db.First(&user, fixture.user.Id).Error == nil && db.First(&token, fixture.token.Id).Error == nil && user.Quota == 100000 && token.RemainQuota == 3000
			}, 3*time.Second, 10*time.Millisecond, "refusal/no-usage close preserves funding")
		})
	}
}

func TestChannelBudgetResponsesWSReuseKeepsTheSameAllocationAfterReorder(t *testing.T) {
	var upstreamTurns atomic.Int32
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, r *http.Request) {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
			index := upstreamTurns.Add(1)
			assert.Equal(t, "Bearer upstream-first", r.Header.Get("Authorization"))
			terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"budget-turn-%d","status":"completed","model":"ws-billing","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, index)
			if !assert.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(terminal))) {
				return
			}
		}
	})
	original := activateRuntimeBudgetFixture(t, fixture)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}))
	// The two allocations now have identical credential text, but only the
	// original is bound to this user. The socket must not use its old index.
	initial, err := model.UpdateChannelKeyAllocations(fixture.channel.Id, 0, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
		if len(entries) != 2 {
			return nil, nil, fmt.Errorf("expected exactly two fixture allocations")
		}
		entries[0].BoundUserId = fixture.user.Id
		entries[0].ConfigRevision++
		entries[1].Key = entries[0].Key
		entries[1].ConfigRevision++
		return entries, nil, nil
	})
	require.NoError(t, err)
	otherID := initial.ChannelKeyEntryID(1)
	require.NotEqual(t, original.Id, otherID)
	for index, payload := range []string{
		`{"type":"response.create","model":"ws-billing","input":"first"}`,
		`{"type":"response.create","model":"ws-billing","input":"second","previous_response_id":"budget-turn-1"}`,
	} {
		if index == 1 {
			current, err := model.GetChannelById(fixture.channel.Id, true)
			require.NoError(t, err)
			updated, err := model.UpdateChannelKeyAllocations(fixture.channel.Id, current.ChannelInfo.KeyEntryRevision, func(entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
				return []model.ChannelKeyEntry{entries[1], entries[0]}, nil, nil
			})
			require.NoError(t, err)
			require.Equal(t, original.Id, updated.ChannelKeyEntryID(1))
		}
		require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(payload)))
		terminal := readResponsesWSTestEvent(t, fixture.client)
		require.Equal(t, "response.completed", terminal["type"], "%+v", terminal)
		body, ok := terminal["response"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, fmt.Sprintf("budget-turn-%d", index+1), body["id"])
	}
	fixture.closeAndWait(t)
	assert.EqualValues(t, 2, upstreamTurns.Load())
	assert.EqualValues(t, 1, fixture.connections.Load())
	var reservations []model.ChannelBudgetReservation
	require.NoError(t, model.DB.Where("channel_id = ?", fixture.channel.Id).Order("id").Find(&reservations).Error)
	require.Len(t, reservations, 2)
	for _, reservation := range reservations {
		assert.Equal(t, original.Id, reservation.AllocationId)
		assert.Equal(t, initial.KeyEntryRevisions[0], reservation.AllocationRevision)
		assert.EqualValues(t, 1000, reservation.SettledQuota)
		assert.Equal(t, model.BudgetReservationPhaseSettled, reservation.Phase)
	}
	for _, account := range []struct {
		scope model.ChannelBudgetScopeType
		id    int64
		used  int64
	}{
		{model.BudgetScopeChannel, int64(fixture.channel.Id), 2000},
		{model.BudgetScopeKey, original.Id, 2000},
		{model.BudgetScopeKey, otherID, 0},
	} {
		view, err := model.ReadChannelBudget(t.Context(), account.scope, account.id)
		require.NoError(t, err)
		assert.Equal(t, account.used, view.PeriodUsedQuota)
		assert.Zero(t, view.ReservedQuota)
	}
	assertResponsesWSAccounting(t, fixture, []int{1000, 1000})
}
