package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func activateRuntimeBudgetFixture(t *testing.T, fixture *responsesWSBillingTest) model.ChannelKeyEntry {
	t.Helper()
	t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "false")
	common.RedisEnabled = false
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelKeyEntry{}))
	require.NoError(t, model.MigrateChannelBudgets(model.DB))
	require.NoError(t, model.EnsureChannelKeyEntries(fixture.channel))
	t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
	require.NoError(t, model.InitializeChannelBudgets())
	db := model.DB
	t.Cleanup(func() {
		for _, table := range []any{&model.ChannelBudgetReservationItem{}, &model.ChannelBudgetReservation{}, &model.ChannelBudgetOperation{}, &model.ChannelBudgetAccount{}, &model.ChannelKeyEntry{}} {
			require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
		}
		require.NoError(t, db.Where(map[string]any{"key": "internal.channel_budget.active_v1"}).Delete(&model.Option{}).Error)
	})
	var entry model.ChannelKeyEntry
	require.NoError(t, db.Where("channel_id = ?", fixture.channel.Id).Order("position, id").First(&entry).Error)
	return entry
}

func TestChannelBudgetHTTPFinalFee(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("fee_enabled=%t", enabled), func(t *testing.T) {
			fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(ws *websocket.Conn, _ *http.Request) { _, _, _ = ws.ReadMessage() })
			activateRuntimeBudgetFixture(t, fixture)
			settings := model_setting.GetGrokSettings()
			previousEnabled, previousAmount := settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount
			settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount = enabled, 0.001
			previousTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
			operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
			t.Cleanup(func() {
				settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount = previousEnabled, previousAmount
				operation_setting.GetQuotaSetting().TrustQuotaUSD = previousTrust
			})
			fixture.httpUpstream = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":{"message":"Failed check: SAFETY_CHECK_TYPE","type":"content_policy_violation"}}`)
			}
			request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses", strings.NewReader(`{"model":"ws-billing","input":"fixture request"}`))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusForbidden, response.StatusCode)
			select {
			case <-fixture.httpDone:
			case <-time.After(3 * time.Second):
				t.Fatal("relay accounting did not finish")
			}
			charged := 0
			if enabled {
				charged = 500
			}
			// Refund is asynchronous in the existing billing session. Observe
			// its completed accounting before closing the database fixture.
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return model.DB.First(&user, fixture.user.Id).Error == nil &&
					model.DB.First(&token, fixture.token.Id).Error == nil &&
					user.Quota == 100000-charged && token.RemainQuota == 3000-charged
			}, 3*time.Second, 10*time.Millisecond)
			var reservations []model.ChannelBudgetReservation
			require.NoError(t, model.DB.Where("channel_id = ?", fixture.channel.Id).Find(&reservations).Error)
			require.Len(t, reservations, 1)
			assert.EqualValues(t, charged, reservations[0].SettledQuota)
			for _, scope := range []model.ChannelBudgetScopeType{model.BudgetScopeChannel, model.BudgetScopeKey} {
				id := int64(fixture.channel.Id)
				if scope == model.BudgetScopeKey {
					id = reservations[0].AllocationId
				}
				view, err := model.ReadChannelBudget(t.Context(), scope, id)
				require.NoError(t, err)
				assert.EqualValues(t, charged, view.PeriodUsedQuota)
				assert.Zero(t, view.ReservedQuota)
			}
		})
	}
}

func TestChannelBudgetCountsFundingAfterTokenFailure(t *testing.T) {
	for _, withSession := range []bool{true, false} {
		t.Run(fmt.Sprintf("session=%t", withSession), func(t *testing.T) {
			fixture := newResponsesWSBillingTest(t, `p * 0`, func(ws *websocket.Conn, _ *http.Request) { _, _, _ = ws.ReadMessage() })
			entry := activateRuntimeBudgetFixture(t, fixture)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{UserId: fixture.user.Id, TokenId: fixture.token.Id, TokenKey: fixture.token.Key,
				UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, ForcePreConsume: true,
				OriginModelName: "ws-billing", UsingGroup: "default", RequestId: common.NewRequestId(),
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: fixture.channel.Id}}
			info.PriceData.QuotaToPreConsume = 1000
			if withSession {
				require.Nil(t, service.PreConsumeBilling(c, 1000, info))
			}
			service.SetSelectedChannelAllocation(c, entry.Id, entry.ConfigRevision)
			require.Nil(t, service.PrepareAttemptChannelBudget(c, info))
			const callback = "test:budget_token_adjustment_failure"
			failure := errors.New("token adjustment fixture failure")
			require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "tokens" {
					tx.AddError(failure)
				}
			}))
			t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
			require.ErrorIs(t, service.SettleBilling(c, info, 1500), failure)
			service.ReleaseAttemptChannelBudget(c, info)
			if withSession {
				assert.False(t, info.Billing.NeedsRefund())
				// A repeated caller cannot debit funding again or change the
				// recorded actual amount after the original charge committed.
				require.NoError(t, service.SettleBilling(c, info, 2000))
			}
			var user model.User
			require.NoError(t, model.DB.First(&user, fixture.user.Id).Error)
			assert.Equal(t, 98500, user.Quota)
			var token model.Token
			require.NoError(t, model.DB.First(&token, fixture.token.Id).Error)
			if withSession {
				assert.Equal(t, 2000, token.RemainQuota)
			} else {
				assert.Equal(t, 3000, token.RemainQuota)
			}
			view, err := model.ReadChannelBudget(t.Context(), model.BudgetScopeKey, entry.Id)
			require.NoError(t, err)
			assert.EqualValues(t, 1500, view.PeriodUsedQuota)
			assert.Zero(t, view.ReservedQuota)
		})
	}
}
