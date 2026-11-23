package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestChannelBudgetDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "budgets.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN missing")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN missing")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			connection, err := db.DB()
			require.NoError(t, err)
			connection.SetMaxOpenConns(12)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				activatedChannelBudgetDatabases.Delete(db)
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
				require.NoError(t, connection.Close())
			})
			t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
			tables := []any{&ChannelBudgetReservationItem{}, &ChannelBudgetReservation{}, &ChannelBudgetOperation{}, &ChannelBudgetAccount{}, &ChannelKeyEntry{}, &Channel{}, &Option{}, &User{}, &Task{}, &Token{}, &Ability{}}
			require.NoError(t, db.Migrator().DropTable(tables...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("engine=%s version=%s", dialect, version)
			// Representative pre-ledger channel data is not imported as used budget.
			require.NoError(t, db.AutoMigrate(&Channel{}, &Option{}))
			channel := Channel{Name: "budget-upgrade", Key: "upstream", Models: "m", Group: "default", Status: common.ChannelStatusEnabled, Balance: 12.5, UsedQuota: 900}
			require.NoError(t, db.Create(&channel).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(&ChannelKeyEntry{}))
				require.NoError(t, MigrateChannelBudgets(db))
			}
			t.Run("transaction activation uses its own connection and does not leak on rollback", func(t *testing.T) {
				connection.SetMaxOpenConns(1)
				defer connection.SetMaxOpenConns(12)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				rollback := errors.New("rollback activation fixture")
				err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Create(&Option{Key: channelBudgetActivationKey, Value: "1"}).Error; err != nil {
						return err
					}
					view, err := SetChannelBudgetLimitTx(tx, BudgetScopeChannel, int64(channel.Id), 0, true, 50)
					if err != nil {
						return err
					}
					assert.True(t, view.LedgerEnabled)
					view, err = ResetChannelBudgetUsageTx(tx, BudgetResetRequest{
						Scope: BudgetScopeChannel, ScopeID: int64(channel.Id), ExpectedVersion: view.ConfigVersion,
						OperationID: dialect + ":rollback-activation-reset",
					})
					if err != nil {
						return err
					}
					assert.True(t, view.LedgerEnabled)
					return rollback
				})
				require.ErrorIs(t, err, rollback)
				active, err := ActiveChannelBudget(ctx)
				require.NoError(t, err)
				assert.False(t, active, "an uncommitted marker must not activate later requests")
			})
			var unchanged Channel
			require.NoError(t, db.First(&unchanged, channel.Id).Error)
			assert.Equal(t, 12.5, unchanged.Balance)
			assert.EqualValues(t, 900, unchanged.UsedQuota)
			first := ChannelKeyEntry{ChannelId: channel.Id, Key: "same-credential", Status: common.ChannelStatusEnabled, ConfigRevision: 1, ScopeMode: "all"}
			second := ChannelKeyEntry{ChannelId: channel.Id, Key: "same-credential", Status: common.ChannelStatusEnabled, ConfigRevision: 1, ScopeMode: "all", BoundUserId: 2, Position: 1}
			require.NoError(t, db.Create(&first).Error)
			require.NoError(t, db.Create(&second).Error)
			// Batching is a rollout prerequisite only for an activated ledger: an
			// inactive one keeps its existing behaviour and writes no marker.
			oldBatch := common.BatchUpdateEnabled
			t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })
			common.BatchUpdateEnabled = true
			require.NoError(t, InitializeChannelBudgets(), "an inactive ledger does not require batching off")
			require.NoError(t, EnsureChannelBudgetRuntimePrerequisite(), "inactive admission is untouched by batching")
			var marker Option
			require.ErrorIs(t, db.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error, gorm.ErrRecordNotFound)
			// Activation under batching is refused and writes no marker, because a
			// cache-hit pre-consume would only be queued for the database.
			t.Setenv("CHANNEL_BUDGET_ENABLED", "true")
			require.ErrorContains(t, InitializeChannelBudgets(), "BATCH_UPDATE_ENABLED=false")
			require.ErrorIs(t, db.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error, gorm.ErrRecordNotFound)
			// Activation can run before startup copies the environment into the
			// runtime flag, so the env value alone must refuse to write the marker.
			common.BatchUpdateEnabled = false
			t.Setenv("BATCH_UPDATE_ENABLED", "true")
			require.ErrorContains(t, InitializeChannelBudgets(), "BATCH_UPDATE_ENABLED=false")
			require.ErrorIs(t, db.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error, gorm.ErrRecordNotFound)
			t.Setenv("BATCH_UPDATE_ENABLED", "false")
			require.NoError(t, InitializeChannelBudgets())
			require.NoError(t, InitializeChannelBudgets())
			require.NoError(t, EnsureChannelBudgetRuntimePrerequisite())
			require.NoError(t, db.Where(map[string]any{"key": channelBudgetActivationKey}).First(&marker).Error)
			assert.Equal(t, "1", marker.Value)
			// The runtime flag alone is still rejected, and cleared afterwards so the
			// remainder of the matrix does not observe batching as enabled.
			common.BatchUpdateEnabled = true
			assert.True(t, ChannelBudgetsEnabled(), "the durable marker still activates the ledger")
			require.ErrorContains(t, EnsureChannelBudgetRuntimePrerequisite(), "BATCH_UPDATE_ENABLED=false")
			common.BatchUpdateEnabled = false
			require.NoError(t, EnsureChannelBudgetRuntimePrerequisite())
			// An env-only batcher is rejected on the same path, and restored for the
			// remaining subtests.
			t.Setenv("BATCH_UPDATE_ENABLED", "true")
			require.ErrorContains(t, EnsureChannelBudgetRuntimePrerequisite(), "BATCH_UPDATE_ENABLED=false")
			t.Setenv("BATCH_UPDATE_ENABLED", "false")
			require.NoError(t, EnsureChannelBudgetRuntimePrerequisite())
			t.Run("durable activation cannot be disabled", func(t *testing.T) {
				t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
				require.ErrorContains(t, InitializeChannelBudgets(), "must remain enabled")
				assert.True(t, ChannelBudgetsEnabled())
				require.NoError(t, db.Migrator().DropTable(&ChannelBudgetOperation{}))
				require.Error(t, InitializeChannelBudgets())
				require.NoError(t, MigrateChannelBudgets(db))
			})
			t.Run("activation storage failure is never unlimited", func(t *testing.T) {
				activatedChannelBudgetDatabases.Delete(db)
				t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
				queries := db.Callback().Query()
				const hook = "test:channel_budget_marker_failure"
				require.NoError(t, queries.Before("gorm:query").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "options" {
						tx.AddError(errors.New("activation store unavailable"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, queries.Remove(hook)) })
				active, err := ActiveChannelBudget(context.Background())
				require.ErrorContains(t, err, "activation store unavailable")
				assert.False(t, active)
				assert.True(t, ChannelBudgetsEnabled(), "failure must enter fail-closed authoritative admission")
				require.Error(t, InitializeChannelBudgets())
				require.NoError(t, queries.Remove(hook))
			})
			ctx := context.Background()
			reserve := func(name string, id int64, user int, amount int64) (*ChannelBudgetReservation, error) {
				return ReserveChannelBudgets(ctx, BudgetReserveRequest{AttemptKey: dialect + ":" + name, Selection: BudgetSelection{ChannelID: channel.Id, AllocationID: id, AllocationRevision: 1, UserID: user, Group: "default", ModelName: "m"}, ReserveQuota: amount})
			}
			channelView, err := SetChannelBudgetLimit(ctx, BudgetScopeChannel, int64(channel.Id), 0, true, 100)
			require.NoError(t, err)
			keyView, err := SetChannelBudgetLimit(ctx, BudgetScopeKey, first.Id, 0, true, 60)
			require.NoError(t, err)
			held, err := reserve("held", first.Id, 1, 40)
			require.NoError(t, err)
			_, err = reserve("reject-key", first.Id, 1, 30)
			require.ErrorIs(t, err, ErrBudgetExhausted)
			current, err := ReadChannelBudget(ctx, BudgetScopeChannel, int64(channel.Id))
			require.NoError(t, err)
			assert.EqualValues(t, 40, current.ReservedQuota)
			_, err = SetChannelBudgetLimit(ctx, BudgetScopeChannel, int64(channel.Id), 0, true, 500)
			require.ErrorIs(t, err, ErrBudgetStaleVersion)
			keyView, err = ResetChannelBudgetUsage(ctx, BudgetResetRequest{Scope: BudgetScopeKey, ScopeID: first.Id, ExpectedVersion: keyView.ConfigVersion, OperationID: dialect + ":reset"})
			require.NoError(t, err)
			assert.EqualValues(t, 40, keyView.ReservedQuota)
			again, err := ResetChannelBudgetUsage(ctx, BudgetResetRequest{Scope: BudgetScopeKey, ScopeID: first.Id, ExpectedVersion: 1, OperationID: dialect + ":reset"})
			require.NoError(t, err)
			assert.Equal(t, keyView, again)
			require.NoError(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: held.Id, Revision: 1, TotalActualQuota: 70, Final: true}))
			require.NoError(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: held.Id, Revision: 1, TotalActualQuota: 70, Final: true}))
			used, err := ReadChannelBudget(ctx, BudgetScopeKey, first.Id)
			require.NoError(t, err)
			assert.EqualValues(t, 70, used.PeriodUsedQuota)
			assert.Zero(t, used.ReservedQuota)
			assert.True(t, used.Exhausted)
			_, err = reserve("zero-exhausted", first.Id, 1, 0)
			require.ErrorIs(t, err, ErrBudgetExhausted)
			_, err = ResetChannelBudgetUsage(ctx, BudgetResetRequest{Scope: BudgetScopeKey, ScopeID: first.Id, ExpectedVersion: used.ConfigVersion, OperationID: dialect + ":reset-after-settle"})
			require.NoError(t, err)
			require.NoError(t, RefundChannelBudgets(ctx, BudgetRefundRequest{ReservationID: held.Id, Revision: 2}))
			require.NoError(t, RefundChannelBudgets(ctx, BudgetRefundRequest{ReservationID: held.Id, Revision: 2}))
			used, err = ReadChannelBudget(ctx, BudgetScopeKey, first.Id)
			require.NoError(t, err)
			assert.Zero(t, used.PeriodUsedQuota)
			assert.Zero(t, used.LifetimeUsedQuota)
			_, err = reserve("wrong-owner", second.Id, 1, 1)
			require.ErrorIs(t, err, ErrBudgetAllocationInvalid)
			private, err := reserve("private", second.Id, 2, 10)
			require.NoError(t, err)
			require.NoError(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: private.Id, Revision: 1, TotalActualQuota: 20}))
			require.NoError(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: private.Id, Revision: 2, TotalActualQuota: 5, Final: true}))
			used, err = ReadChannelBudget(ctx, BudgetScopeKey, second.Id)
			require.NoError(t, err)
			assert.EqualValues(t, 5, used.LifetimeUsedQuota)
			require.ErrorIs(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: private.Id, Revision: 1, TotalActualQuota: 20}), ErrBudgetStaleRevision)
			// Desired settlement survives retry independently from user funding.
			queued, err := reserve("reconcile", first.Id, 1, 5)
			require.NoError(t, err)
			require.NoError(t, QueueChannelBudgetSettlement(ctx, BudgetSettleRequest{ReservationID: queued.Id, Revision: 1, TotalActualQuota: 7, Final: true}, false))
			require.ErrorIs(t, QueueChannelBudgetSettlement(ctx, BudgetSettleRequest{ReservationID: queued.Id, Revision: 1, TotalActualQuota: 7}, false), ErrBudgetAttemptConflict)
			count, err := ReconcileChannelBudgets(ctx, 10)
			require.NoError(t, err)
			assert.Equal(t, 1, count)
			count, err = ReconcileChannelBudgets(ctx, 10)
			require.NoError(t, err)
			assert.Zero(t, count)
			pending, err := reserve("ambiguous", first.Id, 1, 4)
			require.NoError(t, err)
			require.NoError(t, MarkChannelBudgetForwarded(ctx, pending.Id))
			require.NoError(t, db.Model(pending).Update("expires_at", 1).Error)
			_, err = RecoverExpiredChannelBudgetReservations(ctx, time.Now().Unix(), 20)
			require.NoError(t, err)
			var retained ChannelBudgetReservation
			require.NoError(t, db.First(&retained, pending.Id).Error)
			assert.Equal(t, BudgetReservationPhasePending, retained.Phase)
			assert.True(t, retained.NeedsReconciliation)
			require.NoError(t, RefundChannelBudgets(ctx, BudgetRefundRequest{ReservationID: pending.Id, Revision: 1}))
			// Independent channel accounts still share capacity across both allocations.
			_, err = SetChannelBudgetLimit(ctx, BudgetScopeChannel, int64(channel.Id), channelView.ConfigVersion, true, 15)
			require.NoError(t, err)
			var wg sync.WaitGroup
			start := make(chan struct{})
			results := make(chan error, 2)
			for i := range 2 {
				wg.Go(func() { <-start; _, err := reserve(fmt.Sprintf("concurrent-%d", i), second.Id, 2, 10); results <- err })
			}
			close(start)
			wg.Wait()
			close(results)
			rejected := 0
			for err := range results {
				if errors.Is(err, ErrBudgetExhausted) {
					rejected++
				} else {
					require.NoError(t, err)
				}
			}
			assert.Equal(t, 2, rejected, "existing actual usage leaves less than ten; no overshoot")
			t.Run("task funding and recovery survive channel deletion", func(t *testing.T) {
				require.NoError(t, db.AutoMigrate(&User{}, &Task{}, &Token{}, &Ability{}))
				user := User{Username: "settlement-owner", Quota: 80}
				require.NoError(t, db.Create(&user).Error)
				token := Token{UserId: user.Id, Key: "settlement-token", RemainQuota: 30, UsedQuota: 20}
				require.NoError(t, db.Create(&token).Error)
				channel := Channel{Name: "deleted-task-channel", Key: "upstream", Models: "m", Group: "default", Status: common.ChannelStatusEnabled}
				require.NoError(t, db.Create(&channel).Error)
				entry := ChannelKeyEntry{ChannelId: channel.Id, Key: "upstream", Status: common.ChannelStatusEnabled, ScopeMode: "all", ConfigRevision: 1}
				require.NoError(t, db.Create(&entry).Error)
				reservation, err := ReserveChannelBudgets(ctx, BudgetReserveRequest{
					AttemptKey: dialect + ":task-refund", ReserveQuota: 20,
					Selection: BudgetSelection{ChannelID: channel.Id, AllocationID: entry.Id, AllocationRevision: 1, UserID: user.Id, Group: "default", ModelName: "m"},
				})
				require.NoError(t, err)
				task := Task{TaskID: "task-refund", ChannelId: channel.Id, UserId: user.Id, Quota: 20, Status: TaskStatusInProgress,
					PrivateData: TaskPrivateData{BudgetReservationId: reservation.Id, TokenId: token.Id}}
				creates := db.Callback().Create()
				const failTaskInsert = "test:fail_task_insert_after_budget_link"
				require.NoError(t, creates.Before("gorm:create").Register(failTaskInsert, func(tx *gorm.DB) {
					if tx.Statement.Table == "tasks" {
						tx.AddError(errors.New("task insert failed"))
					}
				}))
				t.Cleanup(func() { _ = creates.Remove(failTaskInsert) })
				require.ErrorContains(t, task.InsertWithContext(ctx), "task insert failed")
				unlinked, err := ReadChannelBudgetReservation(ctx, reservation.Id)
				require.NoError(t, err)
				assert.Empty(t, unlinked.TaskId)
				require.NoError(t, creates.Remove(failTaskInsert))
				require.NoError(t, task.InsertWithContext(ctx))
				linked, err := ReadChannelBudgetReservation(ctx, reservation.Id)
				require.NoError(t, err)
				assert.Equal(t, task.TaskID, linked.TaskId)
				require.NoError(t, SettleChannelBudgets(ctx, BudgetSettleRequest{ReservationID: reservation.Id, Revision: 1, TotalActualQuota: 20}))
				require.NoError(t, channel.Delete())
				require.NoError(t, channel.Delete(), "deletion remains idempotent")
				var retired ChannelKeyEntry
				require.NoError(t, db.First(&retired, entry.Id).Error)
				assert.Positive(t, retired.RetiredAt)
				task.Status = TaskStatusFailure
				replayed := task
				updates := db.Callback().Update()
				const failFunding = "test:fail_funding_after_terminal_cas"
				require.NoError(t, updates.Before("gorm:update").Register(failFunding, func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						tx.AddError(errors.New("funding update failed"))
					}
				}))
				t.Cleanup(func() { _ = updates.Remove(failFunding) })
				_, won, err := CommitTaskBudgetTerminalTransition(ctx, &task, TaskStatusInProgress, 0, 0, true)
				require.ErrorContains(t, err, "funding update failed")
				assert.False(t, won)
				var persisted Task
				require.NoError(t, db.First(&persisted, task.ID).Error)
				assert.Equal(t, TaskStatus(TaskStatusInProgress), persisted.Status)
				assert.Equal(t, 20, persisted.Quota)
				require.NoError(t, updates.Remove(failFunding))
				const failToken = "test:fail_token_after_terminal_funding"
				require.NoError(t, updates.Before("gorm:update").Register(failToken, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						tx.AddError(errors.New("token update failed"))
					}
				}))
				t.Cleanup(func() { _ = updates.Remove(failToken) })
				_, won, err = CommitTaskBudgetTerminalTransition(ctx, &task, TaskStatusInProgress, 0, 0, true)
				require.ErrorContains(t, err, "token update failed")
				assert.False(t, won)
				require.NoError(t, db.First(&persisted, task.ID).Error)
				assert.Equal(t, TaskStatus(TaskStatusInProgress), persisted.Status)
				assert.Equal(t, 20, persisted.Quota)
				var storedUser User
				require.NoError(t, db.First(&storedUser, user.Id).Error)
				assert.Equal(t, 80, storedUser.Quota)
				var storedToken Token
				require.NoError(t, db.First(&storedToken, token.Id).Error)
				assert.Equal(t, 30, storedToken.RemainQuota)
				assert.Equal(t, 20, storedToken.UsedQuota)
				require.NoError(t, updates.Remove(failToken))
				delta, won, err := CommitTaskBudgetTerminalTransition(ctx, &task, TaskStatusInProgress, 0, 0, true)
				require.NoError(t, err)
				require.True(t, won)
				assert.Equal(t, -20, delta)
				require.NoError(t, db.First(&persisted, task.ID).Error)
				assert.Equal(t, TaskStatus(TaskStatusFailure), persisted.Status)
				assert.Zero(t, persisted.Quota)
				require.NoError(t, db.First(&storedUser, user.Id).Error)
				assert.Equal(t, 100, storedUser.Quota)

				// A failed ledger write leaves funding committed and a recoverable target.
				hook := "test:fail_task_budget_counter_update"
				require.NoError(t, updates.Before("gorm:update").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "channel_budget_accounts" {
						tx.AddError(errors.New("ledger counter write failed"))
					}
				}))
				t.Cleanup(func() { _ = updates.Remove(hook) })
				require.ErrorContains(t, ApplyQueuedChannelBudgetSettlement(ctx, reservation.Id), "ledger counter write failed")
				delta, err = QueueTaskBudgetSettlement(ctx, &replayed, 0, 0, true)
				require.NoError(t, err)
				assert.Zero(t, delta)
				require.NoError(t, updates.Remove(hook))
				count, err := ReconcileChannelBudgets(ctx, 10)
				require.NoError(t, err)
				assert.Equal(t, 1, count)
				settled, err := ReadChannelBudgetReservation(ctx, reservation.Id)
				require.NoError(t, err)
				assert.Equal(t, BudgetReservationPhaseReleased, settled.Phase)
				assert.False(t, settled.ReconcilePending)
				require.NoError(t, db.First(&storedUser, user.Id).Error)
				assert.Equal(t, 100, storedUser.Quota)
				require.NoError(t, db.First(&storedToken, token.Id).Error)
				assert.Equal(t, 50, storedToken.RemainQuota)
				assert.Zero(t, storedToken.UsedQuota)
			})
		})
	}
}
