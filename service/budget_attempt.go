package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Local channel/allocation budget integration
// ---------------------------------------------------------------------------
//
// A local channel budget and a key allocation budget are additional local
// limits. They never replace the wallet, subscription or API-token charging
// performed by BillingSession: one station charge contributes once to the
// channel account and once to the selected allocation account.
//
// Every upstream attempt holds its validated estimate before it is sent, and
// settles the same actual amount the existing billing path charged. A rejected
// attempt is a candidate rejection, so routing can try another eligible
// channel; the credential itself is never disabled for a budget.

const (
	// ginKeySelectedAllocationID / ginKeySelectedAllocationRevision are the gin
	// context keys the allocation selector writes. They stay plain strings so
	// the allocation store and the budget ledger do not need a shared constant
	// to stay in sync.
	ginKeySelectedAllocationID       = "channel_key_entry_id"
	ginKeySelectedAllocationRevision = "channel_key_entry_revision"
)

// SetSelectedChannelAllocation records the stable allocation identity of the
// attempt about to be dispatched. The allocation store calls it after it has
// chosen an entry; the budget ledger revalidates the identity under lock, so a
// stale value cannot authorize a reservation.
func SetSelectedChannelAllocation(c *gin.Context, allocationID int64, revision int64) {
	if c == nil || allocationID <= 0 {
		return
	}
	c.Set(ginKeySelectedAllocationID, allocationID)
	c.Set(ginKeySelectedAllocationRevision, revision)
}

// ClearSelectedChannelAllocation forgets the previous attempt's allocation so a
// retry that routes to a public key does not inherit it.
func ClearSelectedChannelAllocation(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(ginKeySelectedAllocationID, int64(0))
	c.Set(ginKeySelectedAllocationRevision, int64(0))
}

func selectedChannelAllocation(c *gin.Context) (int64, int64) {
	if c == nil {
		return 0, 0
	}
	return allocationIDFromContext(c), allocationRevisionFromContext(c)
}

func allocationIDFromContext(c *gin.Context) int64 {
	value, ok := c.Get(ginKeySelectedAllocationID)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		id, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0
		}
		return id
	default:
		return 0
	}
}

func allocationRevisionFromContext(c *gin.Context) int64 {
	value, ok := c.Get(ginKeySelectedAllocationRevision)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		revision, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0
		}
		return revision
	default:
		return 0
	}
}

// attemptBudgetEstimateSource is implemented by BillingSession. Asserting on a
// narrow interface keeps relaycommon.BillingSettler unchanged for other
// implementers.
type attemptBudgetEstimateSource interface {
	GetEstimatedQuota() int
}

// attemptBudgetReserveQuota returns the station estimate the current attempt
// must hold. A trusted user skips wallet pre-consumption but not the budget
// hold, so the estimate comes from the session's validated target rather than
// from the amount actually pre-consumed.
func attemptBudgetReserveQuota(info *relaycommon.RelayInfo) int64 {
	if info == nil {
		return 0
	}
	if source, ok := info.Billing.(attemptBudgetEstimateSource); ok && source != nil {
		if estimate := source.GetEstimatedQuota(); estimate > 0 {
			return int64(estimate)
		}
	}
	if info.PriceData.QuotaToPreConsume > 0 {
		return int64(info.PriceData.QuotaToPreConsume)
	}
	return 0
}

// ReserveAttemptChannelBudget holds the current attempt's estimate against the
// channel account and, when the routing selected one allocation, the key
// account. It must be called after the validated estimate exists and before the
// request is forwarded.
func ReserveAttemptChannelBudget(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if c == nil || info == nil || info.IsChannelTest || !model.ChannelBudgetsEnabled() {
		return nil
	}
	if err := model.EnsureChannelBudgetRuntimePrerequisite(); err != nil {
		return budgetAPIError(c, err)
	}
	// Continuous Realtime work, including automatic VAD responses, has no
	// per-turn ledger admission yet. Refuse before either WebSocket is opened.
	if info.RelayFormat == types.RelayFormatOpenAIRealtime {
		return types.NewErrorWithStatusCode(
			errors.New("continuous realtime sessions are not available while channel budget accounting is active"),
			types.ErrorCodeChannelKeyScopeUnsatisfied,
			http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry(),
		)
	}
	// Probes and model discovery are outside user settlement and attempt
	// accounting.
	if info.ChannelId <= 0 {
		return nil
	}
	if info.ChannelBudget.HasBudgetReservation() && !info.ChannelBudget.Settled {
		if err := model.IncreaseChannelBudgetReservation(c.Request.Context(), info.ChannelBudget.ReservationID, attemptBudgetReserveQuota(info)); err != nil {
			return budgetAPIError(c, err)
		}
		return nil
	}
	c.Set("channel_budget_candidate_rejected", false)
	allocationID, allocationRevision := selectedChannelAllocation(c)
	selection := model.BudgetSelection{
		ChannelID:          info.ChannelId,
		AllocationID:       allocationID,
		AllocationRevision: allocationRevision,
		UserID:             info.UserId,
		Group:              info.UsingGroup,
		ModelName:          info.OriginModelName,
	}
	reservation, err := model.ReserveChannelBudgets(c.Request.Context(), model.BudgetReserveRequest{
		AttemptKey:   attemptBudgetKey(info),
		Selection:    selection,
		ReserveQuota: attemptBudgetReserveQuota(info),
		OwnerNode:    common.NodeName,
	})
	if err != nil {
		return budgetAPIError(c, err)
	}
	info.ChannelBudget = relaycommon.BudgetAttempt{
		ReservationID:      reservation.Id,
		AttemptKey:         reservation.AttemptKey,
		AllocationID:       reservation.AllocationId,
		AllocationRevision: reservation.AllocationRevision,
		EstimatedQuota:     reservation.ReservedQuota,
	}
	return nil
}

// attemptBudgetKey is the opaque per-attempt identity. One request id plus the
// retry index identifies exactly one upstream attempt, so a replay cannot hold
// twice.
func attemptBudgetKey(info *relaycommon.RelayInfo) string {
	requestID := info.RequestId
	if requestID == "" {
		requestID = common.GetUUID()
		info.RequestId = requestID
	}
	return fmt.Sprintf("%s:%d:%s", requestID, info.RetryIndex, common.GetUUID())
}

// budgetAPIError maps a ledger failure onto a relay error.
//
//   - exhausted / invalid allocation: a candidate rejection. Another eligible
//     channel may still serve the request, so this is retryable and must not
//     disable the credential.
//   - any other failure: the authoritative ledger is unavailable. The design
//     forbids treating that as unlimited, so it is a retryable service error.
func budgetAPIError(c *gin.Context, err error) *types.NewAPIError {
	switch {
	case errors.Is(err, model.ErrBudgetExhausted):
		c.Set("channel_budget_candidate_rejected", true)
		return types.NewErrorWithStatusCode(
			errors.New("channel budget exhausted for this candidate"),
			types.ErrorCodeChannelKeyScopeUnsatisfied,
			http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry(),
		)
	case errors.Is(err, model.ErrBudgetAllocationInvalid), errors.Is(err, model.ErrBudgetAttemptConflict):
		return types.NewErrorWithStatusCode(
			errors.New("channel key allocation changed, retry the request"),
			types.ErrorCodeChannelKeyScopeUnsatisfied,
			http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry(),
		)
	default:
		logger.LogError(c, common.LogText("channel budget reservation failed: %s", err.Error()))
		return types.NewErrorWithStatusCode(
			errors.New("channel budget is unavailable, retry the request"),
			types.ErrorCodeUpdateDataError,
			http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry(),
		)
	}
}

// ReleaseAttemptChannelBudget gives back the current attempt's hold when the
// attempt produced no charge. A settled attempt keeps its reservation, so this
// is safe to call unconditionally.
func ReleaseAttemptChannelBudget(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil || info.ChannelBudget.Settled || !info.ChannelBudget.HasBudgetReservation() {
		return
	}
	reservationID := info.ChannelBudget.ReservationID
	revision := info.ChannelBudget.Revision + 1
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := model.RefundChannelBudgets(ctx, model.BudgetRefundRequest{
		ReservationID: reservationID,
		Revision:      revision,
		Reason:        "attempt_released",
	})
	if err != nil && !errors.Is(err, model.ErrBudgetReservationNotFound) && !errors.Is(err, model.ErrBudgetStaleRevision) {
		logger.LogWarn(ctx, common.LogText("failed to release channel budget reservation %d: %s", reservationID, err.Error()))
	}
	info.ChannelBudget.Settled = true
	info.ChannelBudget.Revision = revision
}

// settleAttemptChannelBudget records the finished attempt's actual station
// charge. actualQuota is exactly the amount the existing billing path charged,
// so a charge above the estimate is recorded in full and blocks later
// admission.
func settleAttemptChannelBudget(ctx context.Context, info *relaycommon.RelayInfo, actualQuota int, final bool) {
	if info == nil || !info.ChannelBudget.HasBudgetReservation() {
		return
	}
	// An actual charge is never refunded merely because independent ledger
	// persistence fails. Retain the hold and durable desired adjustment.
	revision := info.ChannelBudget.Revision + 1
	info.ChannelBudget.Settled = true
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	in := model.BudgetSettleRequest{ReservationID: info.ChannelBudget.ReservationID, Revision: revision, TotalActualQuota: int64(actualQuota), Final: final}
	err := model.QueueChannelBudgetSettlement(persistCtx, in, false)
	if err == nil {
		info.ChannelBudget.Revision = revision
		err = model.SettleChannelBudgets(persistCtx, in)
	}
	if err != nil {
		// Keep the hold for a bounded reconciliation instead of pretending the
		// charge never happened: the wallet/token side already committed.
		logger.LogError(ctx, common.LogText(
			"failed to settle channel budget reservation %d (actual=%d, revision=%d): %s",
			info.ChannelBudget.ReservationID, actualQuota, revision, err.Error()))
		return
	}
}
