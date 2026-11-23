package model

import (
	"context"
	"errors"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const channelBudgetActivationKey = "internal.channel_budget.active_v1"

// Once a database is activated, this process never forgets the decision. A
// disabled process still consults the durable marker before bypassing budgets.
var activatedChannelBudgetDatabases sync.Map

func channelBudgetActivation(ctx context.Context) (bool, error) {
	if DB == nil {
		return false, errors.New("channel budget database is unavailable")
	}
	if _, ok := activatedChannelBudgetDatabases.Load(DB); ok {
		return true, nil
	}
	enabled, err := ActiveChannelBudgetTx(dbWithContext(ctx))
	if err == nil && enabled {
		activatedChannelBudgetDatabases.Store(DB, true)
	}
	return enabled, err
}

// ActiveChannelBudgetTx reads through the caller's connection. A transaction
// must not acquire another connection or publish an uncommitted activation.
func ActiveChannelBudgetTx(tx *gorm.DB) (bool, error) {
	if tx == nil {
		return false, errors.New("channel budget database is unavailable")
	}
	var marker Option
	err := tx.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if marker.Value == "1" {
		return true, nil
	}
	if common.GetEnvOrDefaultBool("CHANNEL_BUDGET_ENABLED", false) {
		return false, errors.New("channel budget accounting has not been activated")
	}
	return false, nil
}

// ChannelBudgetsEnabled is conservative on storage failure: callers either
// enter authoritative ledger admission or reject, never infer unlimited quota.
func ChannelBudgetsEnabled() bool {
	enabled, err := channelBudgetActivation(context.Background())
	return enabled || err != nil
}

// channelBudgetBatchPrerequisite enforces the activation prerequisite that
// batching is off. Batching is fixed at process startup: it is either read from
// BATCH_UPDATE_ENABLED or copied into the runtime flag by the same startup
// step, and nothing changes it afterwards.
//
// Both spellings are checked because activation can run before the runtime flag
// is copied from the environment. A fresh activation must not write the durable
// marker in a process that will start batching, so a true env value counts even
// when the runtime flag is still false.
func channelBudgetBatchPrerequisite() error {
	if common.BatchUpdateEnabled || common.GetEnvOrDefaultBool("BATCH_UPDATE_ENABLED", false) {
		return errors.New("channel budget accounting requires BATCH_UPDATE_ENABLED=false")
	}
	return nil
}

// EnsureChannelBudgetRuntimePrerequisite re-checks the activation prerequisites
// before a request enters authoritative ledger admission. An inactive ledger
// keeps its existing behaviour untouched, including under batching.
func EnsureChannelBudgetRuntimePrerequisite() error {
	enabled, err := channelBudgetActivation(context.Background())
	if err != nil {
		// An unreadable activation marker must fail closed before any
		// existing-reservation branch can admit traffic.
		return err
	}
	if !enabled {
		return nil
	}
	return channelBudgetBatchPrerequisite()
}

// InitializeChannelBudgets runs after schema/allocation migration. Rollout:
// deploy every node, pause/drain legacy admission, enable the flag consistently,
// then restart. Quiescing ALL nodes is mandatory; this is not an automatic
// mixed-version cutover. Once activated, a false flag refuses startup. Runtime
// lookups propagate marker-read failures rather than infer an inactive ledger.
//
// BATCH_UPDATE_ENABLED is a rollout prerequisite, not a preference: with it on,
// a pre-consume that hits a warm cache is only queued for the database, so the
// submission transaction cannot rely on the amount it already covered being
// durable. The ledger therefore refuses to activate while batching is on, and
// the check covers the environment variable too, because activation can run
// before startup copies it into the runtime flag.
func InitializeChannelBudgets() error {
	configured := common.GetEnvOrDefaultBool("CHANNEL_BUDGET_ENABLED", false)
	if DB == nil {
		return errors.New("channel budget database is unavailable")
	}
	var marker Option
	err := DB.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if marker.Value == "1" && !configured {
		return errors.New("CHANNEL_BUDGET_ENABLED must remain enabled for this activated database")
	}
	if !configured {
		return nil
	}
	if err := channelBudgetBatchPrerequisite(); err != nil {
		return err
	}
	for _, table := range []any{&ChannelKeyEntry{}, &ChannelBudgetAccount{}, &ChannelBudgetReservation{}, &ChannelBudgetReservationItem{}, &ChannelBudgetOperation{}} {
		if !DB.Migrator().HasTable(table) {
			return errors.New("channel budget schema is unavailable")
		}
	}
	if err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.Assignments(map[string]any{"value": "1"})}).Create(&Option{Key: channelBudgetActivationKey, Value: "1"}).Error; err != nil {
		return err
	}
	activatedChannelBudgetDatabases.Store(DB, true)
	return nil
}
