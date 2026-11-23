package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// Final admission is adjacent to the actual send, after any specialized image
// or task estimate changes. It never replaces the user's funding reservation.
func PrepareAttemptChannelBudget(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if apiErr := ReserveAttemptChannelBudget(c, info); apiErr != nil {
		return apiErr
	}
	if info == nil || !info.ChannelBudget.HasBudgetReservation() {
		return nil
	}
	if err := model.MarkChannelBudgetForwarded(c.Request.Context(), info.ChannelBudget.ReservationID); err != nil {
		return budgetAPIError(c, err)
	}
	info.ChannelBudget.Forwarded = true
	return nil
}

// A bounded independent reconciler never replays wallet/token adjustments.
// Forwarded abandoned attempts remain held for administrative reconciliation.
func RunChannelBudgetReconciliation(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !model.ChannelBudgetsEnabled() {
				continue
			}
			work, cancel := context.WithTimeout(ctx, 20*time.Second)
			if _, err := model.ReconcileChannelBudgets(work, 100); err != nil {
				logger.LogWarn(ctx, common.LogText("channel budget reconciliation failed: %s", err.Error()))
			}
			if _, err := model.RecoverExpiredChannelBudgetReservations(work, time.Now().Unix(), 100); err != nil {
				logger.LogWarn(ctx, common.LogText("channel budget hold recovery failed: %s", err.Error()))
			}
			cancel()
		}
	}
}
