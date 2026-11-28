package model

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func dbWithContext(ctx context.Context) *gorm.DB {
	if ctx == nil {
		ctx = context.Background()
	}
	return DB.WithContext(ctx)
}

// All ledger mutations serialize on the owning channel, including adjustments
// to retired allocations. No network work belongs in this transaction.
func ChannelBudgetTransaction(ctx context.Context, channelID int, fn func(*gorm.DB) error) error {
	return channelBudgetTransaction(ctx, channelID, false, fn)
}

func channelBudgetTransaction(ctx context.Context, channelID int, allowDeleted bool, fn func(*gorm.DB) error) error {
	if channelID <= 0 {
		return ErrBudgetConfigInvalid
	}
	return dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			if err := tx.Model(&Channel{}).Where("1 = 0").UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
				return err
			}
		}
		var channel Channel
		if err := lockForUpdate(tx).Select("id").Where("id = ?", channelID).First(&channel).Error; err != nil &&
			!(allowDeleted && errors.Is(err, gorm.ErrRecordNotFound)) {
			return err
		}
		return fn(tx)
	})
}

func ReadChannelBudget(ctx context.Context, scope ChannelBudgetScopeType, scopeID int64) (*ChannelBudgetView, error) {
	views, err := ReadChannelBudgetViews(ctx, scope, []int64{scopeID})
	if err != nil {
		return nil, err
	}
	return views[scopeID], nil
}

func ReadChannelBudgetViews(ctx context.Context, scope ChannelBudgetScopeType, ids []int64) (map[int64]*ChannelBudgetView, error) {
	views, _, err := ReadChannelBudgetViewsWithActivation(ctx, scope, ids)
	return views, err
}

// ReadChannelBudgetViewsWithActivation returns the same activation observation
// used by every view, including an empty page. Callers must propagate its error
// instead of treating failed activation reads as a disabled or unlimited ledger.
func ReadChannelBudgetViewsWithActivation(ctx context.Context, scope ChannelBudgetScopeType, ids []int64) (map[int64]*ChannelBudgetView, bool, error) {
	if _, err := normalizeBudgetScope(scope); err != nil {
		return nil, false, err
	}
	active, err := channelBudgetActivation(ctx)
	if err != nil {
		return nil, false, err
	}
	views := make(map[int64]*ChannelBudgetView, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, false, ErrBudgetConfigInvalid
		}
		views[id] = &ChannelBudgetView{ScopeType: scope, ScopeId: id, LedgerEnabled: active}
		if scope == BudgetScopeChannel {
			views[id].ChannelId = int(id)
		}
	}
	if len(ids) == 0 {
		return views, active, nil
	}
	var accounts []ChannelBudgetAccount
	if err := dbWithContext(ctx).Where("scope_type = ? AND scope_id IN ?", scope, ids).Find(&accounts).Error; err != nil {
		return nil, false, err
	}
	for i := range accounts {
		views[accounts[i].ScopeId] = newBudgetView(&accounts[i], active)
	}
	return views, active, nil
}

func ListChannelBudgetViews(ctx context.Context, channelIDs []int) (map[int]*ChannelBudgetView, error) {
	ids := make([]int64, len(channelIDs))
	for i, id := range channelIDs {
		ids[i] = int64(id)
	}
	views, err := ReadChannelBudgetViews(ctx, BudgetScopeChannel, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[int]*ChannelBudgetView, len(views))
	for id, view := range views {
		result[int(id)] = view
	}
	return result, nil
}

// ChannelBudgetAllows is a batched, conservative visibility check. Admission
// still reserves authoritatively. Errors never widen model/key visibility.
func ChannelBudgetAllows(ctx context.Context, channelID int, allocationIDs []int64) (map[int64]bool, error) {
	allowed := make(map[int64]bool, len(allocationIDs))
	if !ChannelBudgetsEnabled() {
		for _, id := range allocationIDs {
			allowed[id] = true
		}
		return allowed, nil
	}
	channel, err := ReadChannelBudget(ctx, BudgetScopeChannel, int64(channelID))
	if err != nil {
		return nil, err
	}
	entries, err := ReadChannelBudgetViews(ctx, BudgetScopeKey, allocationIDs)
	if err != nil {
		return nil, err
	}
	for id, entry := range entries {
		allowed[id] = !channel.Exhausted && !entry.Exhausted
	}
	return allowed, nil
}

func SetChannelBudgetLimit(ctx context.Context, scope ChannelBudgetScopeType, id, version int64, enabled bool, limit int64) (*ChannelBudgetView, error) {
	channelID, err := budgetResolveChannelID(dbWithContext(ctx), scope, id)
	if err != nil {
		return nil, err
	}
	var view *ChannelBudgetView
	err = ChannelBudgetTransaction(ctx, channelID, func(tx *gorm.DB) error {
		var err error
		view, err = SetChannelBudgetLimitTx(tx, scope, id, version, enabled, limit)
		return err
	})
	return view, err
}

// Tx variants require the caller's channel configuration lock, then any entry
// locks in ascending ID order. They never open an independent transaction.
func SetChannelBudgetLimitTx(tx *gorm.DB, scope ChannelBudgetScopeType, id, version int64, enabled bool, limit int64) (*ChannelBudgetView, error) {
	if _, err := normalizeBudgetScope(scope); err != nil {
		return nil, err
	}
	if version < 0 || validateBudgetLimit(enabled, limit) != nil {
		return nil, ErrBudgetConfigInvalid
	}
	active, err := ActiveChannelBudgetTx(tx)
	if err != nil {
		return nil, err
	}
	channelID, err := budgetResolveChannelID(tx, scope, id)
	if err != nil {
		return nil, err
	}
	account, err := budgetEnsureAccount(tx, scope, id, channelID)
	if err != nil {
		return nil, err
	}
	if account.MergeTargetId != 0 || account.ConfigVersion != version {
		return nil, ErrBudgetStaleVersion
	}
	if account.ConfigVersion >= budgetLimitUpperBound {
		return nil, ErrBudgetConfigInvalid
	}
	account.Enabled, account.LimitQuota = enabled, limit
	account.ConfigVersion++
	account.UpdatedAt = common.GetTimestamp()
	if err := tx.Model(account).Updates(map[string]any{"enabled": enabled, "limit_quota": limit, "config_version": account.ConfigVersion, "updated_at": account.UpdatedAt}).Error; err != nil {
		return nil, err
	}
	return newBudgetView(account, active), nil
}

func ResetChannelBudgetUsage(ctx context.Context, in BudgetResetRequest) (*ChannelBudgetView, error) {
	channelID, err := budgetResolveChannelID(dbWithContext(ctx), in.Scope, in.ScopeID)
	if err != nil {
		return nil, err
	}
	var view *ChannelBudgetView
	err = ChannelBudgetTransaction(ctx, channelID, func(tx *gorm.DB) error { var err error; view, err = ResetChannelBudgetUsageTx(tx, in); return err })
	return view, err
}
func ResetChannelBudgetUsageTx(tx *gorm.DB, in BudgetResetRequest) (*ChannelBudgetView, error) {
	if _, err := normalizeBudgetScope(in.Scope); err != nil {
		return nil, err
	}
	if in.ExpectedVersion < 0 || len(in.OperationID) == 0 || len(in.OperationID) > 96 || strings.TrimSpace(in.OperationID) != in.OperationID {
		return nil, ErrBudgetConfigInvalid
	}
	active, err := ActiveChannelBudgetTx(tx)
	if err != nil {
		return nil, err
	}
	var operation ChannelBudgetOperation
	err = tx.Where("operation_id = ?", in.OperationID).First(&operation).Error
	if err == nil {
		if operation.OperationType != "reset" || operation.ScopeType != in.Scope || operation.ScopeId != in.ScopeID {
			return nil, ErrBudgetAttemptConflict
		}
		var view ChannelBudgetView
		if err := common.UnmarshalJsonStr(operation.ResultJSON, &view); err != nil {
			return nil, err
		}
		return &view, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	channelID, err := budgetResolveChannelID(tx, in.Scope, in.ScopeID)
	if err != nil {
		return nil, err
	}
	account, err := budgetEnsureAccount(tx, in.Scope, in.ScopeID, channelID)
	if err != nil {
		return nil, err
	}
	if account.MergeTargetId != 0 || account.ConfigVersion != in.ExpectedVersion {
		return nil, ErrBudgetStaleVersion
	}
	if account.ConfigVersion >= budgetLimitUpperBound || account.ResetEpoch >= budgetLimitUpperBound {
		return nil, ErrBudgetConfigInvalid
	}
	account.ConfigVersion++
	account.ResetEpoch++
	account.PeriodUsedQuota = 0
	account.UpdatedAt = common.GetTimestamp()
	if err := tx.Model(account).Updates(map[string]any{"period_used_quota": 0, "reset_epoch": account.ResetEpoch, "config_version": account.ConfigVersion, "updated_at": account.UpdatedAt}).Error; err != nil {
		return nil, err
	}
	view := newBudgetView(account, active)
	encoded, err := common.Marshal(view)
	if err != nil {
		return nil, err
	}
	err = tx.Create(&ChannelBudgetOperation{OperationId: in.OperationID, OperationType: "reset", ScopeType: in.Scope, ScopeId: in.ScopeID, ResultJSON: string(encoded), CreatedAt: common.GetTimestamp()}).Error
	return view, err
}

func ReserveChannelBudgets(ctx context.Context, in BudgetReserveRequest) (*ChannelBudgetReservation, error) {
	if len(in.AttemptKey) == 0 || len(in.AttemptKey) > 160 || in.ReserveQuota < 0 || in.ReserveQuota > budgetLimitUpperBound || in.Selection.ChannelID <= 0 {
		return nil, ErrBudgetConfigInvalid
	}
	var reservation *ChannelBudgetReservation
	err := ChannelBudgetTransaction(ctx, in.Selection.ChannelID, func(tx *gorm.DB) error {
		var existing ChannelBudgetReservation
		err := tx.Where("attempt_key = ?", in.AttemptKey).First(&existing).Error
		if err == nil {
			if existing.ChannelId != in.Selection.ChannelID || existing.AllocationId != in.Selection.AllocationID || existing.AllocationRevision != in.Selection.AllocationRevision || existing.UserId != in.Selection.UserID || existing.ModelName != in.Selection.ModelName || existing.ReservedQuota != in.ReserveQuota || existing.Phase != BudgetReservationPhasePending {
				return ErrBudgetAttemptConflict
			}
			reservation = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var channel Channel
		if err := tx.First(&channel, in.Selection.ChannelID).Error; err != nil {
			return err
		}
		if channel.Status != common.ChannelStatusEnabled {
			return ErrBudgetAllocationInvalid
		}
		if in.Selection.UsesKeyScope() {
			if _, err := budgetLookupAllocation(tx, in.Selection); err != nil {
				return err
			}
		} else {
			return ErrBudgetAllocationInvalid
		}
		channelAccount, err := budgetEnsureAccount(tx, BudgetScopeChannel, int64(channel.Id), channel.Id)
		if err != nil {
			return err
		}
		keyAccount, err := budgetEnsureAccount(tx, BudgetScopeKey, in.Selection.AllocationID, channel.Id)
		if err != nil {
			return err
		}
		accounts := []*ChannelBudgetAccount{channelAccount, keyAccount}
		for _, a := range accounts {
			if a.MergeTargetId != 0 {
				return ErrBudgetAllocationInvalid
			}
			remaining, exhausted := budgetCapacity(a)
			if a.Enabled && (exhausted || remaining == nil || *remaining < in.ReserveQuota) {
				return ErrBudgetExhausted
			}
		}
		now := common.GetTimestamp()
		if in.ExpiresAt <= 0 {
			in.ExpiresAt = now + 3600
		}
		reservation = &ChannelBudgetReservation{AttemptKey: in.AttemptKey, ChannelId: channel.Id, AllocationId: in.Selection.AllocationID, AllocationRevision: in.Selection.AllocationRevision, UserId: in.Selection.UserID, ModelName: in.Selection.ModelName, ReservedQuota: in.ReserveQuota, Phase: BudgetReservationPhasePending, TaskId: in.TaskID, OwnerNode: in.OwnerNode, ExpiresAt: in.ExpiresAt, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(reservation).Error; err != nil {
			return err
		}
		for _, a := range accounts {
			reserved, err := budgetAddChecked(a.ReservedQuota, in.ReserveQuota)
			if err != nil {
				return err
			}
			if err := tx.Model(a).Updates(map[string]any{"reserved_quota": reserved, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&ChannelBudgetReservationItem{ReservationId: reservation.Id, AccountId: a.Id, ScopeType: a.ScopeType, ScopeId: a.ScopeId, HeldQuota: in.ReserveQuota}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return reservation, err
}

func budgetReservationTransaction(ctx context.Context, id int64, fn func(*gorm.DB, *ChannelBudgetReservation) error) error {
	if id <= 0 {
		return ErrBudgetReservationNotFound
	}
	var initial ChannelBudgetReservation
	if err := dbWithContext(ctx).First(&initial, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBudgetReservationNotFound
		}
		return err
	}
	return channelBudgetTransaction(ctx, initial.ChannelId, true, func(tx *gorm.DB) error {
		return budgetReservationLockedTx(tx, id, fn)
	})
}

// budgetReservationTransactionTx runs the ledger mutation inside a caller's
// transaction, for the submission path that must commit the task row, the
// funding adjustment and the durable ledger target atomically. The caller owns
// the surrounding channel-configuration lock and must not hold it across
// network work.
func budgetReservationTransactionTx(tx *gorm.DB, id int64, fn func(*gorm.DB, *ChannelBudgetReservation) error) error {
	if tx == nil || id <= 0 {
		return ErrBudgetReservationNotFound
	}
	return budgetReservationLockedTx(tx, id, fn)
}

func budgetReservationLockedTx(tx *gorm.DB, id int64, fn func(*gorm.DB, *ChannelBudgetReservation) error) error {
	// The channel account remains the first lock after a channel is deleted.
	// Scope order is explicit, never dependent on auto-generated account IDs.
	var items []ChannelBudgetReservationItem
	if err := tx.Where("reservation_id = ?", id).Order("scope_type asc, scope_id asc").Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		var a ChannelBudgetAccount
		if err := lockForUpdate(tx).First(&a, item.AccountId).Error; err != nil {
			return err
		}
	}
	var reservation ChannelBudgetReservation
	if err := lockForUpdate(tx).First(&reservation, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBudgetReservationNotFound
		}
		return err
	}
	return fn(tx, &reservation)
}

func budgetAdjust(value, delta int64) (int64, error) {
	if delta < 0 {
		return budgetSubChecked(value, -delta)
	}
	return budgetAddChecked(value, delta)
}

func SettleChannelBudgets(ctx context.Context, in BudgetSettleRequest) error {
	if in.Revision <= 0 || in.TotalActualQuota < 0 || in.TotalActualQuota > budgetLimitUpperBound {
		return ErrBudgetConfigInvalid
	}
	return budgetReservationTransaction(ctx, in.ReservationID, func(tx *gorm.DB, r *ChannelBudgetReservation) error { return settleChannelBudgetTx(tx, r, in, false) })
}
func settleChannelBudgetTx(tx *gorm.DB, r *ChannelBudgetReservation, in BudgetSettleRequest, refund bool) error {
	if in.Revision < r.SettlementRevision {
		return ErrBudgetStaleRevision
	}
	if in.Revision == r.SettlementRevision && r.SettlementRevision > 0 {
		if budgetSettlementMatches(r, in, refund) {
			return nil
		}
		return ErrBudgetAttemptConflict
	}
	if r.ReconcilePending && (in.Revision < r.DesiredRevision ||
		(in.Revision == r.DesiredRevision && !budgetDesiredSettlementMatches(r, in, refund))) {
		return ErrBudgetAttemptConflict
	}
	if r.Phase == BudgetReservationPhaseReleased {
		if refund {
			return nil
		}
		return ErrBudgetStaleRevision
	}
	var items []ChannelBudgetReservationItem
	if err := lockForUpdate(tx).Where("reservation_id = ?", r.Id).Order("scope_type asc, scope_id asc").Find(&items).Error; err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		a, period, err := budgetLoadAdjustmentAccount(tx, item)
		if err != nil {
			return err
		}
		held, err := budgetSubChecked(a.ReservedQuota, item.HeldQuota)
		if err != nil {
			return err
		}
		if !item.Settled {
			item.SettlementEpoch = a.ResetEpoch
			period = true
		}
		delta := in.TotalActualQuota - item.SettledQuota
		lifetime, err := budgetAdjust(a.LifetimeUsedQuota, delta)
		if err != nil {
			return err
		}
		used := a.PeriodUsedQuota
		if period {
			used, err = budgetAdjust(used, delta)
			if err != nil {
				return err
			}
		}
		if err := tx.Model(a).Updates(map[string]any{"reserved_quota": held, "lifetime_used_quota": lifetime, "period_used_quota": used, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		if err := tx.Model(item).Updates(map[string]any{"held_quota": 0, "settled": true, "settled_quota": in.TotalActualQuota, "settlement_epoch": item.SettlementEpoch}).Error; err != nil {
			return err
		}
	}
	phase := BudgetReservationPhaseOpen
	if in.Final {
		phase = BudgetReservationPhaseSettled
	}
	if refund {
		phase = BudgetReservationPhaseReleased
	}
	updates := map[string]any{"settled_quota": in.TotalActualQuota, "settlement_revision": in.Revision, "phase": phase, "expires_at": 0, "reconcile_pending": false, "updated_at": common.GetTimestamp()}
	if in.TaskID != "" {
		updates["task_id"] = in.TaskID
	}
	return tx.Model(r).Updates(updates).Error
}
func RefundChannelBudgets(ctx context.Context, in BudgetRefundRequest) error {
	if in.Revision <= 0 {
		return ErrBudgetConfigInvalid
	}
	return budgetReservationTransaction(ctx, in.ReservationID, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		return settleChannelBudgetTx(tx, r, BudgetSettleRequest{ReservationID: in.ReservationID, Revision: in.Revision, TotalActualQuota: 0, Final: true}, true)
	})
}

func budgetLoadAdjustmentAccount(tx *gorm.DB, item *ChannelBudgetReservationItem) (*ChannelBudgetAccount, bool, error) {
	var a ChannelBudgetAccount
	if err := lockForUpdate(tx).First(&a, item.AccountId).Error; err != nil {
		return nil, false, err
	}
	epoch := item.SettlementEpoch
	for range 32 {
		if a.MergeTargetId == 0 {
			return &a, epoch == a.ResetEpoch, nil
		}
		if epoch == a.MergeSourceEpoch {
			epoch = a.MergeTargetEpoch
		} else {
			epoch = -1
		}
		var next ChannelBudgetAccount
		if err := lockForUpdate(tx).First(&next, a.MergeTargetId).Error; err != nil {
			return nil, false, err
		}
		a = next
	}
	return nil, false, ErrBudgetConfigInvalid
}

func HasLiveChannelBudgetReservationsTx(tx *gorm.DB, allocationID int64) (bool, error) {
	var count int64
	reservations := tx.Model(&ChannelBudgetReservation{}).Where("allocation_id = ?", allocationID)
	err := reservations.Session(&gorm.Session{}).Where("phase IN ? OR reconcile_pending = ?",
		[]string{BudgetReservationPhasePending, BudgetReservationPhaseOpen}, true).Count(&count).Error
	if err != nil || count > 0 {
		return count > 0, err
	}
	var entry ChannelKeyEntry
	if err := tx.Select("id", "channel_id", "key").First(&entry, allocationID).Error; err != nil {
		return false, err
	}
	active := tx.Model(&Task{}).Where("channel_id = ? AND status NOT IN ?", entry.ChannelId,
		[]string{TaskStatusSuccess, TaskStatusFailure})
	if err := active.Session(&gorm.Session{}).Where("task_id IN (?)", reservations.Select("task_id").Where("task_id <> ?", "")).Count(&count).Error; err != nil || count > 0 {
		return count > 0, err
	}
	const batchSize = 100
	var lastID int64
	for {
		var tasks []Task
		if err := active.Session(&gorm.Session{}).Select("id", "private_data").Where("id > ?", lastID).
			Order("id").Limit(batchSize).Find(&tasks).Error; err != nil {
			return false, err
		}
		for _, task := range tasks {
			p := task.PrivateData
			if p.AllocationId == allocationID {
				// A still-running task keeps holding its allocation even after the
				// ledger settled the reservation at submission.
				return true, nil
			}
			// A task persisted before stable allocation ids cannot prove which
			// duplicate of an identical credential it used. Every live allocation
			// storing that text stays busy until the task reaches a terminal state;
			// guessing one of them would merge a reservation out from under live
			// work. Unique credentials attribute unambiguously.
			if p.AllocationId == 0 && p.Key != "" && p.Key == entry.Key {
				return true, nil
			}
		}
		if len(tasks) < batchSize {
			return false, nil
		}
		lastID = tasks[len(tasks)-1].ID
	}
}
func MergeChannelBudgetAccounts(ctx context.Context, in BudgetMergeRequest) (*ChannelBudgetView, error) {
	channelID, err := budgetResolveChannelID(dbWithContext(ctx), in.TargetScope, in.TargetID)
	if err != nil {
		return nil, err
	}
	var view *ChannelBudgetView
	err = ChannelBudgetTransaction(ctx, channelID, func(tx *gorm.DB) error { var err error; view, err = MergeChannelBudgetAccountsTx(tx, in); return err })
	return view, err
}
func MergeChannelBudgetAccountsTx(tx *gorm.DB, in BudgetMergeRequest) (*ChannelBudgetView, error) {
	if in.SourceScope != BudgetScopeKey || in.TargetScope != BudgetScopeKey || in.SourceID <= 0 || in.TargetID <= 0 || in.SourceID == in.TargetID {
		return nil, ErrBudgetConfigInvalid
	}
	active, err := ActiveChannelBudgetTx(tx)
	if err != nil {
		return nil, err
	}
	channelID, err := budgetResolveChannelID(tx, in.TargetScope, in.TargetID)
	if err != nil {
		return nil, err
	}
	sourceChannel, err := budgetResolveChannelID(tx, in.SourceScope, in.SourceID)
	if err != nil {
		return nil, err
	}
	if sourceChannel != channelID {
		return nil, ErrBudgetConfigInvalid
	}
	ids := []int64{in.SourceID, in.TargetID}
	slices.Sort(ids)
	accounts := make(map[int64]*ChannelBudgetAccount, 2)
	for _, id := range ids {
		a, err := budgetEnsureAccount(tx, BudgetScopeKey, id, channelID)
		if err != nil {
			return nil, err
		}
		accounts[id] = a
	}
	source, target := accounts[in.SourceID], accounts[in.TargetID]
	if source.MergeTargetId == target.Id {
		return newBudgetView(target, active), nil
	}
	if source.MergeTargetId != 0 || target.MergeTargetId != 0 || source.ResetEpoch != in.SourceEpoch || target.ResetEpoch != in.TargetEpoch {
		return nil, ErrBudgetStaleVersion
	}
	for _, id := range ids {
		live, err := HasLiveChannelBudgetReservationsTx(tx, id)
		if err != nil {
			return nil, err
		}
		if live || accounts[id].ReservedQuota != 0 {
			return nil, ErrBudgetAttemptConflict
		}
	}
	if source.Enabled != target.Enabled || source.LimitQuota != target.LimitQuota {
		return nil, ErrBudgetConfigInvalid
	}
	lifetime, err := budgetAddChecked(target.LifetimeUsedQuota, source.LifetimeUsedQuota)
	if err != nil {
		return nil, err
	}
	used, err := budgetAddChecked(target.PeriodUsedQuota, source.PeriodUsedQuota)
	if err != nil {
		return nil, err
	}
	if err := tx.Model(target).Updates(map[string]any{"period_used_quota": used, "lifetime_used_quota": lifetime, "updated_at": common.GetTimestamp()}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(source).Updates(map[string]any{"merge_target_id": target.Id, "merge_source_epoch": source.ResetEpoch, "merge_target_epoch": target.ResetEpoch, "period_used_quota": 0, "lifetime_used_quota": 0, "updated_at": common.GetTimestamp()}).Error; err != nil {
		return nil, err
	}
	target.PeriodUsedQuota, target.LifetimeUsedQuota = used, lifetime
	return newBudgetView(target, active), nil
}

func LinkChannelBudgetReservationTask(ctx context.Context, id int64, taskID string) error {
	if id <= 0 || taskID == "" {
		return ErrBudgetConfigInvalid
	}
	return budgetReservationTransaction(ctx, id, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if r.TaskId != "" && r.TaskId != taskID {
			return ErrBudgetAttemptConflict
		}
		return tx.Model(r).Updates(map[string]any{"task_id": taskID, "expires_at": 0, "updated_at": common.GetTimestamp()}).Error
	})
}

// Mark before dispatch; an uncertain remote result is never expired/refunded.
func MarkChannelBudgetForwarded(ctx context.Context, id int64) error {
	return budgetReservationTransaction(ctx, id, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if r.Phase != BudgetReservationPhasePending {
			return ErrBudgetAttemptConflict
		}
		return tx.Model(r).Updates(map[string]any{"forwarded_at": common.GetTimestamp()}).Error
	})
}

// Desired adjustments are durable and replayable independently of wallet/token
// charging. A reconciliation failure must never re-run a funding adjustment.
func QueueChannelBudgetSettlement(ctx context.Context, in BudgetSettleRequest, refund bool) error {
	if in.Revision <= 0 || in.TotalActualQuota < 0 || in.TotalActualQuota > budgetLimitUpperBound ||
		(refund && (!in.Final || in.TotalActualQuota != 0)) {
		return ErrBudgetConfigInvalid
	}
	return budgetReservationTransaction(ctx, in.ReservationID, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if in.Revision < r.SettlementRevision {
			return ErrBudgetStaleRevision
		}
		if in.Revision == r.SettlementRevision {
			if budgetSettlementMatches(r, in, refund) {
				return nil
			}
			return ErrBudgetAttemptConflict
		}
		if r.Phase == BudgetReservationPhaseReleased || (r.ReconcilePending && in.Revision < r.DesiredRevision) {
			return ErrBudgetStaleRevision
		}
		if r.ReconcilePending && in.Revision == r.DesiredRevision {
			if budgetDesiredSettlementMatches(r, in, refund) {
				return nil
			}
			return ErrBudgetAttemptConflict
		}
		if in.TaskID != "" && r.TaskId != "" && in.TaskID != r.TaskId {
			return ErrBudgetAttemptConflict
		}
		updates := map[string]any{"desired_quota": in.TotalActualQuota, "desired_revision": in.Revision, "desired_final": in.Final, "desired_refund": refund, "reconcile_pending": true, "updated_at": common.GetTimestamp()}
		if in.TaskID != "" {
			updates["task_id"] = in.TaskID
		}
		return tx.Model(r).Updates(updates).Error
	})
}

func budgetSettlementMatches(r *ChannelBudgetReservation, in BudgetSettleRequest, refund bool) bool {
	phase := BudgetReservationPhaseOpen
	if refund {
		phase = BudgetReservationPhaseReleased
	} else if in.Final {
		phase = BudgetReservationPhaseSettled
	}
	return r.SettledQuota == in.TotalActualQuota && r.Phase == phase && (in.TaskID == "" || r.TaskId == in.TaskID)
}

func budgetDesiredSettlementMatches(r *ChannelBudgetReservation, in BudgetSettleRequest, refund bool) bool {
	return r.DesiredQuota == in.TotalActualQuota && r.DesiredFinal == in.Final && r.DesiredRefund == refund &&
		(in.TaskID == "" || r.TaskId == in.TaskID)
}

// ApplySubmissionChannelBudgetSettlement finalizes a submission whose charge,
// task row and desired ledger target already committed together. It applies
// exactly that durable target and then clears the pending flag, so a process
// restart never re-runs the funding adjustment and the ledger counters catch
// up on their own. An already finalized reservation is a no-op.
func ApplySubmissionChannelBudgetSettlement(ctx context.Context, id, desiredRevision int64) error {
	if id <= 0 {
		return ErrBudgetReservationNotFound
	}
	return budgetReservationTransaction(ctx, id, func(tx *gorm.DB, r *ChannelBudgetReservation) error {
		if !r.ReconcilePending {
			return nil
		}
		if r.DesiredRevision != desiredRevision {
			return ErrBudgetStaleRevision
		}
		in := BudgetSettleRequest{ReservationID: id, Revision: r.DesiredRevision,
			TotalActualQuota: r.DesiredQuota, Final: r.DesiredFinal, TaskID: r.TaskId}
		if err := settleChannelBudgetTx(tx, r, in, r.DesiredRefund); err != nil {
			return err
		}
		return tx.Model(r).Update("reconcile_pending", false).Error
	})
}

func ReconcileChannelBudgets(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	var pending []ChannelBudgetReservation
	if err := dbWithContext(ctx).Where("reconcile_pending = ?", true).Order("id").Limit(limit).Find(&pending).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, r := range pending {
		if err := ApplyQueuedChannelBudgetSettlement(ctx, r.Id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func RecoverExpiredChannelBudgetReservations(ctx context.Context, now int64, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	var expired []ChannelBudgetReservation
	if err := dbWithContext(ctx).Where("phase = ? AND expires_at > 0 AND expires_at < ? AND task_id = '' AND needs_reconciliation = ?", BudgetReservationPhasePending, now, false).Order("id").Limit(limit).Find(&expired).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, r := range expired {
		err := budgetReservationTransaction(ctx, r.Id, func(tx *gorm.DB, current *ChannelBudgetReservation) error {
			if current.Phase != BudgetReservationPhasePending || current.TaskId != "" || current.ExpiresAt == 0 || current.ExpiresAt >= now {
				return nil
			}
			if current.ForwardedAt != 0 {
				return tx.Model(current).Update("needs_reconciliation", true).Error
			}
			if err := settleChannelBudgetTx(tx, current, BudgetSettleRequest{ReservationID: current.Id, Revision: current.SettlementRevision + 1, TotalActualQuota: 0, Final: true}, true); err != nil {
				return err
			}
			count++
			return nil
		})
		if err != nil {
			return count, err
		}
	}
	return count, nil
}

// Used by migration tests and management tests to create an unlimited account
// without changing its enforcement revision.
func EnsureChannelBudgetAccountTx(tx *gorm.DB, scope ChannelBudgetScopeType, id int64, channelID int) (*ChannelBudgetAccount, error) {
	return budgetEnsureAccount(tx, scope, id, channelID)
}
