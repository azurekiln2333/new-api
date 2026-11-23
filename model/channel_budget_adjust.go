package model

import (
	"context"
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// IncreaseChannelBudgetReservation raises a not-yet-dispatched hold to a newer
// validated estimate. Lower estimates never erase a hold; actual settlement
// returns the difference. Both accounts grow atomically.
func IncreaseChannelBudgetReservation(ctx context.Context, id, target int64) error {
	if target < 0 || target > budgetLimitUpperBound {
		return ErrBudgetConfigInvalid
	}
	return budgetReservationTransaction(ctx, id, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if target <= r.ReservedQuota {
			return nil
		}
		if r.Phase != BudgetReservationPhasePending || r.ForwardedAt != 0 {
			return ErrBudgetAttemptConflict
		}
		delta := target - r.ReservedQuota
		var items []ChannelBudgetReservationItem
		if err := tx.Where("reservation_id = ?", r.Id).Order("scope_type, scope_id").Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			var a ChannelBudgetAccount
			if err := tx.First(&a, item.AccountId).Error; err != nil {
				return err
			}
			remaining, exhausted := budgetCapacity(&a)
			if a.Enabled && (exhausted || remaining == nil || *remaining < delta) {
				return ErrBudgetExhausted
			}
		}
		for _, item := range items {
			var a ChannelBudgetAccount
			if err := tx.First(&a, item.AccountId).Error; err != nil {
				return err
			}
			held, err := budgetAddChecked(a.ReservedQuota, delta)
			if err != nil {
				return err
			}
			if err := tx.Model(&a).Update("reserved_quota", held).Error; err != nil {
				return err
			}
			if err := tx.Model(&item).Update("held_quota", target).Error; err != nil {
				return err
			}
		}
		return tx.Model(r).Update("reserved_quota", target).Error
	})
}

// QueueTaskBudgetSettlement commits the funding adjustment, task quota, and
// final ledger target together. A retry can apply the ledger independently.
func QueueTaskBudgetSettlement(ctx context.Context, task *Task, total, subscriptionID int, refund bool) (int, error) {
	delta, _, err := queueTaskBudgetSettlement(ctx, task, total, subscriptionID, refund, nil)
	return delta, err
}

// CommitTaskBudgetTerminalTransition commits the terminal update, funding and
// final budget target together. A failure leaves the task eligible for polling.
func CommitTaskBudgetTerminalTransition(ctx context.Context, task *Task, previous TaskStatus, total, subscriptionID int, refund bool) (int, bool, error) {
	return queueTaskBudgetSettlement(ctx, task, total, subscriptionID, refund, &previous)
}

func queueTaskBudgetSettlement(ctx context.Context, task *Task, total, subscriptionID int, refund bool, previous *TaskStatus) (int, bool, error) {
	if task.ID <= 0 || total < 0 || int64(total) > budgetLimitUpperBound || (refund && total != 0) {
		return 0, false, ErrBudgetConfigInvalid
	}
	var delta int
	var won bool
	var tokenKey string
	err := budgetReservationTransaction(ctx, task.PrivateData.BudgetReservationId, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if r.TaskId != task.TaskID || r.ChannelId != task.ChannelId || r.UserId != task.UserId {
			return ErrBudgetAttemptConflict
		}
		// A final target and the corresponding funding are already committed.
		if r.ReconcilePending && r.DesiredFinal {
			if r.DesiredQuota == int64(total) && r.DesiredRefund == refund {
				return nil
			}
			return ErrBudgetAttemptConflict
		}
		if r.Phase == BudgetReservationPhaseSettled || r.Phase == BudgetReservationPhaseReleased {
			if r.SettledQuota == int64(total) && (r.Phase == BudgetReservationPhaseReleased) == refund {
				return nil
			}
			return ErrBudgetAttemptConflict
		}
		// A pending submission without a desired charge has not committed its
		// budget. In particular, its task quota is not evidence of a charge.
		if r.Phase != BudgetReservationPhaseOpen && !r.ReconcilePending {
			return ErrBudgetStaleRevision
		}
		var current Task
		if err := lockForUpdate(tx).Select("id", "quota", "status").First(&current, task.ID).Error; err != nil {
			return err
		}
		if previous != nil && current.Status != *previous {
			return nil
		}
		if current.Quota != task.Quota || current.Quota < 0 {
			return ErrBudgetAttemptConflict
		}
		if previous != nil {
			if task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure {
				return ErrBudgetConfigInvalid
			}
			result := tx.Model(task).Where("status = ?", *previous).Select("*").Updates(task)
			if result.Error != nil || result.RowsAffected != 1 {
				return result.Error
			}
		} else if current.Status != TaskStatusSuccess && current.Status != TaskStatusFailure {
			return ErrBudgetAttemptConflict
		}
		delta = total - current.Quota
		if delta != 0 {
			if err := adjustTaskFundingTx(tx, task.UserId, subscriptionID, delta); err != nil {
				return err
			}
			if previous != nil {
				var err error
				tokenKey, err = adjustTaskTokenTx(tx, task, delta)
				if err != nil {
					return err
				}
			}
			if err := tx.Model(&current).UpdateColumn("quota", total).Error; err != nil {
				return err
			}
		}
		won = true
		return tx.Model(r).Updates(map[string]any{
			"desired_quota": total, "desired_revision": max(r.SettlementRevision, r.DesiredRevision) + 1,
			"desired_final": true, "desired_refund": refund, "reconcile_pending": true,
			"updated_at": common.GetTimestamp(),
		}).Error
	})
	if err != nil {
		return 0, false, err
	}
	publishTaskBillingCaches(task, subscriptionID, delta, tokenKey)
	return delta, won, nil
}

// TaskBudgetSubmit is the submission charge of one durable asynchronous task.
// The wallet/subscription adjustment, the token delta and the durable ledger
// target commit with the task insert in one transaction, so a crash between
// the insert and settlement cannot leave a persisted task that was never
// charged.
type TaskBudgetSubmit struct {
	// Total is the station cost recorded on the task at submission.
	Total int
	// Delta is the additional funding to move at submission: Total minus what
	// the existing pre-consume already committed. It is the whole cost for a
	// trusted user, who pre-consumed nothing, and zero only when the
	// pre-consume already covered the submission cost.
	Delta int
	// SubscriptionID is the funding subscription when the task bills a plan.
	SubscriptionID int
	// TokenID identifies the API token whose remain/used counters move with
	// the same delta. TokenID <= 0 skips the token adjustment.
	TokenID int
	// Final marks an immediate terminal result. A running task keeps its
	// reservation open until completion can revise the recorded amount.
	Final bool
}

// TaskBudgetInsertResult reports what one submission transaction committed so
// the caller can replay only the ledger half of a failed settlement.
// ReservationID is zero when the task carried no ledger reservation.
type TaskBudgetInsertResult struct {
	ReservationID   int64
	DesiredRevision int64
	Delta           int
	SubscriptionID  int
	TokenID         int
	TokenKey        string
}

// InsertTaskWithBudgetSubmit persists an accepted task together with its
// submission charge and durable ledger target. With no linked reservation the
// ordinary insert is used and the caller keeps its own settlement path.
func InsertTaskWithBudgetSubmit(tx *gorm.DB, task *Task, omitColumns []string, submit TaskBudgetSubmit) (*TaskBudgetInsertResult, error) {
	result := TaskBudgetInsertResult{ReservationID: task.PrivateData.BudgetReservationId,
		SubscriptionID: submit.SubscriptionID, TokenID: submit.TokenID}
	if submit.Total < 0 || submit.Total > int(budgetLimitUpperBound) {
		return nil, ErrBudgetConfigInvalid
	}
	if result.ReservationID <= 0 {
		return nil, task.insertTx(tx, omitColumns...)
	}
	// Ledger writes and the submission charge share one lock order: the owning
	// channel configuration, the ledger accounts and reservation, the task row,
	// then funding and token. Nothing here performs network work.
	if err := lockForUpdate(tx).Select("id").Where("id = ?", task.ChannelId).First(&Channel{}).Error; err != nil {
		return nil, err
	}
	//
	// Only the desired intent is written. The reservation keeps its phase and
	// settled amount until ApplySubmissionChannelBudgetSettlement moves held
	// quota into usage, so a crash in between is recovered by replay rather
	// than by claiming a charge the ledger never recorded.
	err := budgetReservationTransactionTx(tx, result.ReservationID, func(_ *gorm.DB, r *ChannelBudgetReservation) error {
		if err := budgetSubmissionIdentityMatches(r, task); err != nil {
			return err
		}
		if r.Phase != BudgetReservationPhasePending && r.Phase != BudgetReservationPhaseOpen {
			return ErrBudgetStaleRevision
		}
		result.Delta = submit.Delta
		// The submission advances the desire revision by one, like a queued
		// settlement, so a terminal transition can still supersede it.
		result.DesiredRevision = max(r.SettlementRevision, r.DesiredRevision) + 1
		// These flags live in the task row, so they must be set before the
		// insert rather than after it.
		task.PrivateData.BudgetPending = true
		task.PrivateData.BudgetSettlementRevision = r.SettlementRevision
		if err := insertTaskWithBudgetSubmit(tx, task, omitColumns, submit, &result); err != nil {
			return err
		}
		return tx.Model(r).Updates(map[string]any{
			"task_id": task.TaskID, "expires_at": 0,
			"desired_quota": max(int64(submit.Total), 0), "desired_revision": result.DesiredRevision,
			"desired_final": submit.Final, "desired_refund": false, "reconcile_pending": true,
			"updated_at": common.GetTimestamp(),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// insertTaskWithBudgetSubmit writes the task row, its funding/token delta and the
// durable desired ledger intent. It runs inside the transaction that already
// holds the owning channel lock, and reports the token key it charged through
// result so a caller can wrap it.
func insertTaskWithBudgetSubmit(tx *gorm.DB, task *Task, omitColumns []string, submit TaskBudgetSubmit, result *TaskBudgetInsertResult) error {
	body := tx.Session(&gorm.Session{NewDB: true})
	if len(omitColumns) > 0 {
		body = body.Omit(omitColumns...)
	}
	if err := body.Create(task).Error; err != nil {
		return err
	}
	if err := adjustTaskFundingTx(tx, task.UserId, submit.SubscriptionID, submit.Delta); err != nil {
		return err
	}
	if submit.Delta != 0 {
		key, err := adjustTaskTokenTx(tx, task, submit.Delta)
		if err != nil {
			return err
		}
		if key != "" {
			result.TokenKey = key
		}
	}
	return nil
}

// budgetSubmissionIdentityMatches rejects a reservation bound to another
// channel, user, allocation or task, so a submission can never charge a
// foreign hold.
func budgetSubmissionIdentityMatches(r *ChannelBudgetReservation, task *Task) error {
	if task.TaskID == "" || r.ChannelId != task.ChannelId || r.UserId != task.UserId {
		return ErrBudgetAttemptConflict
	}
	if r.TaskId != "" && r.TaskId != task.TaskID {
		return ErrBudgetAttemptConflict
	}
	if r.AllocationId != task.PrivateData.AllocationId || r.AllocationRevision != task.PrivateData.AllocationRevision {
		return ErrBudgetAttemptConflict
	}
	return nil
}

// GetTaskForSubmissionOutcome reads a persisted task by its public identifier
// under the caller's bounded context, for deciding whether a failed submission
// actually committed.
func GetTaskForSubmissionOutcome(ctx context.Context, want SubmissionOutcomeExpectation) (*Task, bool, error) {
	if want.TaskID == "" {
		return nil, false, nil
	}
	var tasks []Task
	if err := dbWithContext(ctx).Where("task_id = ? AND user_id = ? AND channel_id = ?", want.TaskID, want.UserID, want.ChannelID).
		Limit(2).Find(&tasks).Error; err != nil {
		return nil, false, err
	}
	if len(tasks) == 0 {
		return nil, false, nil
	}
	if len(tasks) != 1 {
		return nil, false, ErrBudgetAttemptConflict
	}
	return &tasks[0], true, nil
}

// SubmissionOutcomeExpectation is the attempt identity a submission must prove
// before its charge may be treated as committed. It comes from the in-flight
// request, never from the rows being checked.
type SubmissionOutcomeExpectation struct {
	TaskID             string
	ChannelID          int
	UserID             int
	AllocationID       int64
	AllocationRevision int64
	ReservationID      int64
}

// SubmissionOutcomeReservationMatches verifies that a reservation is the one
// this attempt reserved, for the same channel, user and allocation revision.
func SubmissionOutcomeReservationMatches(r *ChannelBudgetReservation, want SubmissionOutcomeExpectation) bool {
	if r == nil || want.ReservationID <= 0 || want.TaskID == "" {
		return false
	}
	return r.Id == want.ReservationID && r.ChannelId == want.ChannelID && r.UserId == want.UserID &&
		r.AllocationId == want.AllocationID && r.AllocationRevision == want.AllocationRevision &&
		(r.TaskId == "" || r.TaskId == want.TaskID)
}

// SubmissionOutcomeIdentityMatches verifies that a persisted task, the
// reservation that charged it and this attempt all describe the same charge.
func SubmissionOutcomeIdentityMatches(task *Task, r *ChannelBudgetReservation, want SubmissionOutcomeExpectation) bool {
	if task == nil || !SubmissionOutcomeReservationMatches(r, want) {
		return false
	}
	return task.TaskID == want.TaskID && task.ChannelId == want.ChannelID && task.UserId == want.UserID &&
		task.PrivateData.AllocationId == want.AllocationID &&
		task.PrivateData.AllocationRevision == want.AllocationRevision &&
		task.PrivateData.BudgetReservationId == want.ReservationID &&
		r.TaskId == want.TaskID
}

func adjustTaskFundingTx(tx *gorm.DB, userID, subscriptionID, delta int) error {
	if delta == 0 {
		return nil
	}
	if subscriptionID > 0 {
		// The upstream already accepted the work, so this is not an admission
		// cap: the amount used by an accepted job is recorded even when it
		// exceeds the subscription's declared total.
		return recordAcceptedSubscriptionUsageTx(tx, subscriptionID, int64(delta))
	}
	result := tx.Model(&User{}).Where("id = ? AND quota <= ?", userID,
		common.MaxWalletQuota+min(delta, 0)).UpdateColumn("quota", gorm.Expr("quota - ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaLimitExceeded
	}
	return nil
}

// recordAcceptedSubscriptionUsageTx adds usage an accepted job already incurred.
// Unlike admission, this must not fail on the subscription total, so it clamps
// to a non-negative amount used and rejects only an arithmetic overflow.
func recordAcceptedSubscriptionUsageTx(tx *gorm.DB, id int, delta int64) error {
	var sub UserSubscription
	if err := lockForUpdate(tx).First(&sub, id).Error; err != nil {
		return err
	}
	newUsed := max(sub.AmountUsed, 0)
	if delta > 0 && newUsed > math.MaxInt64-delta {
		return ErrBudgetConfigInvalid
	}
	if newUsed += delta; newUsed < 0 {
		newUsed = 0
	}
	return tx.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("amount_used", newUsed).Error
}

func adjustTaskTokenTx(tx *gorm.DB, task *Task, delta int) (string, error) {
	if delta == 0 || task.PrivateData.TokenId <= 0 {
		return "", nil
	}
	var token Token
	err := lockForUpdate(tx).Select("id", "key").Where("id = ? AND user_id = ?", task.PrivateData.TokenId, task.UserId).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil // The user may have deleted their token since submit.
	}
	if err != nil {
		return "", err
	}
	err = tx.Model(&token).Updates(map[string]any{
		"remain_quota": gorm.Expr("remain_quota - ?", delta), "used_quota": gorm.Expr("used_quota + ?", delta), "accessed_time": common.GetTimestamp(),
	}).Error
	return token.Key, err
}

// PublishCommittedTaskCharge mirrors a submission charge that already committed
// in the primary database into the Redis caches. A cache failure is logged only:
// the database rows are authoritative and a later read rehydrates them.
func PublishCommittedTaskCharge(userID, subscriptionID, tokenID int, tokenKey string, delta int) {
	if delta == 0 || !common.RedisEnabled {
		return
	}
	if subscriptionID == 0 && userID > 0 {
		if err := cacheIncrUserQuota(userID, -int64(delta)); err != nil {
			common.SysError(common.LogText("failed to sync task submission charge to the wallet cache: %s", err.Error()))
		}
	}
	if tokenID > 0 && tokenKey != "" {
		if _, err := cacheApplyTokenQuotaDelta(tokenID, tokenKey, -int64(delta)); err != nil {
			common.SysError(common.LogText("failed to sync task submission charge to the token cache: %s", err.Error()))
		}
	}
}

func publishTaskBillingCaches(task *Task, subscriptionID, delta int, tokenKey string) {
	if delta == 0 || !common.RedisEnabled {
		return
	}
	if subscriptionID == 0 {
		if err := cacheIncrUserQuota(task.UserId, -int64(delta)); err != nil {
			common.SysError(common.LogText("failed to sync task settlement to the wallet cache: %s", err.Error()))
		}
	}
	if tokenKey != "" {
		if _, err := cacheApplyTokenQuotaDelta(task.PrivateData.TokenId, tokenKey, -int64(delta)); err != nil {
			common.SysError(common.LogText("failed to sync task settlement to the token cache: %s", err.Error()))
		}
	}
}

func ApplyQueuedChannelBudgetSettlement(ctx context.Context, id int64) error {
	return budgetReservationTransaction(ctx, id, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if !r.ReconcilePending {
			return nil
		}
		return settleChannelBudgetTx(tx, r, BudgetSettleRequest{ReservationID: id,
			Revision: r.DesiredRevision, TotalActualQuota: r.DesiredQuota, Final: r.DesiredFinal}, r.DesiredRefund)
	})
}

// ActivateChannelBudget permits the HTTP layer to distinguish inactive from
// storage errors without ever accepting an error as an unlimited allowance.
func ActiveChannelBudget(ctx context.Context) (bool, error) { return channelBudgetActivation(ctx) }

func ReadChannelBudgetReservation(ctx context.Context, id int64) (*ChannelBudgetReservation, error) {
	var r ChannelBudgetReservation
	if err := dbWithContext(ctx).First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBudgetReservationNotFound
		}
		return nil, err
	}
	return &r, nil
}
