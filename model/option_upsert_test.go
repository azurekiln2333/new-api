package model

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// optionTestDB opens a throwaway database for one dialect with a per-test table
// prefix, so these rows never collide with another fixture in the same process.
func optionTestDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(t.TempDir() + "/options.db?_pragma=busy_timeout(2000)&_pragma=journal_mode(WAL)")
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		driver = postgres.Open(dsn)
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	prefix := fmt.Sprintf("opt_upsert_%d_", time.Now().UnixNano())
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB := DB
	previousType := common.MainDatabaseType()
	previousMap := common.OptionMap
	DB = db
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMap = previousMap
		assert.NoError(t, sqlDB.Close())
	})
	versionSQL := "SELECT version()"
	if dialect == "sqlite" {
		versionSQL = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
	t.Logf("database version: %s", version)
	require.NoError(t, db.AutoMigrate(&Option{}, &SystemTask{}, &SystemTaskLock{}, &AuditEventOutbox{}))
	// AuditEventOutbox has an explicit fixed table name; prefixes do not isolate
	// it. Each fixture owns this disposable database and removes only its events.
	t.Cleanup(func() {
		assert.NoError(t, db.Where("action = ?", "option.batch.test").Delete(&AuditEventOutbox{}).Error)
	})
	return db
}

// TestUpdateOptionPersistsAcrossDialects pins the ordinary save path against the
// three supported databases: the first call inserts and the second overwrites,
// and both values must be readable back from the table rather than only from the
// in-memory map.
func TestUpdateOptionPersistsAcrossDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := optionTestDB(t, dialect)
			consoleSettings := console_setting.GetConsoleSetting()
			common.OptionMapRWMutex.RLock()
			previousConsoleSettings := *consoleSettings
			common.OptionMapRWMutex.RUnlock()
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				*consoleSettings = previousConsoleSettings
				common.OptionMapRWMutex.Unlock()
			})
			assert.True(t, previousConsoleSettings.SidebarPersonalSettingsEnabled, "personal sidebar settings default to enabled")
			require.NoError(t, LoadOptionsFromDatabase())
			assert.True(t, consoleSettings.SidebarPersonalSettingsEnabled, "an absent stored override keeps personal sidebar settings enabled")
			const sidebarSettingsKey = "console_setting.sidebar_personal_settings_enabled"
			require.NoError(t, UpdateOption(sidebarSettingsKey, "false"))
			var persistedSidebar Option
			require.NoError(t, db.Where(&Option{Key: sidebarSettingsKey}).First(&persistedSidebar).Error)
			assert.Equal(t, "false", persistedSidebar.Value)
			common.OptionMapRWMutex.Lock()
			consoleSettings.SidebarPersonalSettingsEnabled = true
			common.OptionMapRWMutex.Unlock()
			require.NoError(t, LoadOptionsFromDatabase())
			assert.False(t, consoleSettings.SidebarPersonalSettingsEnabled, "stored false overrides the default when options are reloaded")
			require.NoError(t, db.Where(&Option{Key: sidebarSettingsKey}).First(&persistedSidebar).Error)
			assert.Equal(t, "false", persistedSidebar.Value, "reload must not overwrite the persisted choice")

			settings := operation_setting.GetLogSetting()
			previousSettings := *settings
			assert.Equal(t, 3, previousSettings.RequestContentRetentionDays, "a fresh configuration defaults request content to three days")
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				*settings = previousSettings
				common.OptionMapRWMutex.Unlock()
			})
			common.OptionMapRWMutex.Lock()
			settings.RetentionDays = 0
			settings.RequestContentRetentionDays = 3
			common.OptionMapRWMutex.Unlock()
			require.NoError(t, LoadOptionsFromDatabase())
			usageDays, contentDays := operation_setting.LogRetentionDays()
			assert.Zero(t, usageDays)
			assert.Equal(t, 3, contentDays, "an absent stored override retains the request-content default")
			for _, option := range []Option{
				{Key: "log_setting.retention_days", Value: "0"},
				{Key: "log_setting.request_content_retention_days", Value: "7"},
				{Key: "log_setting.request_content_enabled", Value: "false"},
				{Key: "log_setting.request_content_max_bytes", Value: "256"},
			} {
				require.NoError(t, UpdateOption(option.Key, option.Value))
				var persisted Option
				require.NoError(t, db.Where(&Option{Key: option.Key}).First(&persisted).Error)
				assert.Equal(t, option.Value, persisted.Value)
			}
			usageDays, contentDays = operation_setting.LogRetentionDays()
			assert.Zero(t, usageDays)
			assert.Equal(t, 7, contentDays)
			const contentRetentionKey = "log_setting.request_content_retention_days"
			require.NoError(t, UpdateOption(contentRetentionKey, "0"))
			common.OptionMapRWMutex.Lock()
			settings.RequestContentRetentionDays = 3
			common.OptionMapRWMutex.Unlock()
			require.NoError(t, LoadOptionsFromDatabase())
			usageDays, contentDays = operation_setting.LogRetentionDays()
			assert.Zero(t, usageDays)
			assert.Zero(t, contentDays, "an explicitly stored zero remains keep-forever")
			var persistedRetention Option
			require.NoError(t, db.Where(&Option{Key: contentRetentionKey}).First(&persistedRetention).Error)
			assert.Equal(t, "0", persistedRetention.Value)
			const key = "TestUpsertKey"

			require.NoError(t, UpdateOption(key, "first"))
			assert.Equal(t, "first", common.OptionMap[key])
			var stored Option
			require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
			assert.Equal(t, "first", stored.Value)

			require.NoError(t, UpdateOption(key, "second"))
			require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
			assert.Equal(t, "second", stored.Value, "an update must overwrite the stored value")

			var count int64
			require.NoError(t, db.Model(&Option{}).Where(&Option{Key: key}).Count(&count).Error)
			assert.Equal(t, int64(1), count, "the upsert must not insert a duplicate row")
		})
	}
}

// TestUpdateOptionReportsWriteFailure fails the write and asserts the caller sees
// an error while the in-memory map keeps the last persisted value. Announcing
// success after a failed write is what let the console show values the database
// never stored.
func TestUpdateOptionReportsWriteFailure(t *testing.T) {
	db := optionTestDB(t, "sqlite")
	const key = "TestUpsertFailureKey"

	require.NoError(t, UpdateOption(key, "stored"))
	require.Equal(t, "stored", common.OptionMap[key])

	callbackName := "test:reject_option_write"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		tx.AddError(fmt.Errorf("disk full"))
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callbackName) })

	err := UpdateOption(key, "rejected")
	require.Error(t, err, "a failed write must not be reported as success")
	assert.Equal(t, "stored", common.OptionMap[key], "the map must keep the last persisted value")

	var stored Option
	require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
	assert.Equal(t, "stored", stored.Value)
}

// TestUpdateOptionsBulkIsAtomic pins the multi-key path: an ordinary call writes
// every key, and a failure on a later row rolls back the earlier one instead of
// leaving the database and the map partially updated.
func TestUpdateOptionsBulkIsAtomic(t *testing.T) {
	db := optionTestDB(t, "sqlite")

	require.NoError(t, UpdateOptionsBulk(map[string]string{"TestBulkA": "a1", "TestBulkB": "b1"}))
	for key, want := range map[string]string{"TestBulkA": "a1", "TestBulkB": "b1"} {
		var stored Option
		require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
		assert.Equal(t, want, stored.Value)
		assert.Equal(t, want, common.OptionMap[key])
	}

	// Let the first row through and reject the second, so the rollback of the
	// already-written row is what the assertion actually observes.
	var creates int
	callbackName := "test:reject_second_option_write"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		creates++
		if creates == 2 {
			tx.AddError(fmt.Errorf("constraint violation"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callbackName) })

	require.Error(t, UpdateOptionsBulk(map[string]string{"TestBulkA": "a2", "TestBulkB": "b2"}))
	assert.Equal(t, "a1", common.OptionMap["TestBulkA"], "no key may be announced when the transaction fails")
	assert.Equal(t, "b1", common.OptionMap["TestBulkB"])

	var stored Option
	require.NoError(t, db.Where(&Option{Key: "TestBulkA"}).First(&stored).Error)
	assert.Equal(t, "a1", stored.Value, "the failed transaction must roll back the row it already wrote")
}

func TestOrdinaryOptionsBatchDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := optionTestDB(t, dialect)
			const firstKey, secondKey = "TestSectionA", "TestSectionSecret"
			options := []Option{{Key: firstKey, Value: "before"}, {Key: secondKey, Value: "secret-before"}}
			audit := AuditEventDraft{EventId: common.NewRequestId(), Action: "option.batch.test", ParamsJson: `{"keys":["TestSectionA","TestSectionSecret"]}`}
			require.NoError(t, UpdateOrdinaryOptionsBatch(context.Background(), options, audit))
			var syncCount, auditCount int64
			require.NoError(t, db.Model(&SystemTask{}).Where("type = ?", SyncTaskType(SyncTopicOptions)).Count(&syncCount).Error)
			assert.Equal(t, int64(1), syncCount, "the whole section produces one sync task")
			require.NoError(t, db.Model(&AuditEventOutbox{}).Where("event_id = ?", audit.EventId).Count(&auditCount).Error)
			assert.Equal(t, int64(1), auditCount)
			var event AuditEventOutbox
			require.NoError(t, db.Where("event_id = ?", audit.EventId).First(&event).Error)
			assert.NotContains(t, event.ParamsJson+event.Content, "secret-before")

			for _, failurePoint := range []string{"option", "sync", "audit"} {
				t.Run(failurePoint, func(t *testing.T) {
					const callbackName = "test:reject_settings_batch"
					writes := 0
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
						switch row := tx.Statement.Dest.(type) {
						case *Option:
							writes++
							if failurePoint == "option" && writes == 2 {
								tx.AddError(fmt.Errorf("failed write includes secret-after"))
							}
						case *SystemTask:
							if failurePoint == "sync" {
								tx.AddError(fmt.Errorf("sync unavailable"))
							}
						case *AuditEventOutbox:
							if failurePoint == "audit" && row.EventId != "" {
								tx.AddError(fmt.Errorf("audit unavailable"))
							}
						}
					}))
					t.Cleanup(func() { assert.NoError(t, db.Callback().Create().Remove(callbackName)) })
					attempt := audit
					attempt.EventId = common.NewRequestId()
					err := UpdateOrdinaryOptionsBatch(context.Background(), []Option{{Key: firstKey, Value: "after"}, {Key: secondKey, Value: "secret-after"}}, attempt)
					require.Error(t, err)
					assert.NotContains(t, err.Error(), "secret-after")
					if failurePoint == "option" {
						var keyError *OptionUpdateError
						require.ErrorAs(t, err, &keyError)
						assert.Equal(t, secondKey, keyError.Key)
					}
					for _, option := range options {
						var stored Option
						require.NoError(t, db.Where(&Option{Key: option.Key}).First(&stored).Error)
						assert.Equal(t, option.Value, stored.Value)
						assert.Equal(t, option.Value, common.OptionMap[option.Key])
					}
					require.NoError(t, db.Model(&SystemTask{}).Count(&syncCount).Error)
					assert.Equal(t, int64(1), syncCount, "rollback cannot publish another sync obligation")
					require.NoError(t, db.Model(&AuditEventOutbox{}).Where("event_id = ?", attempt.EventId).Count(&auditCount).Error)
					assert.Zero(t, auditCount, "failed section cannot enqueue a success audit")
				})
			}
		})
	}
}

func TestOrdinaryOptionsBatchValidationDoesNotPublish(t *testing.T) {
	db := optionTestDB(t, "sqlite")
	for _, invalid := range []Option{
		{Key: "GroupLevels", Value: `{"vip":null}`},
		{Key: "Chats", Value: "not-json"},
		{Key: "log_setting.retention_days", Value: "-1"},
		{Key: "log_setting.request_content_retention_days", Value: "36501"},
		{Key: "log_setting.request_content_enabled", Value: "yes"},
		{Key: "log_setting.request_content_max_bytes", Value: "255"},
		{Key: "InviteRegisterEnabled", Value: "invalid"},
		{Key: "passkey.rp_id", Value: "example.com"},
		{Key: "ServerAddress", Value: "https://example.com"},
		{Key: "ModelRatio", Value: "{}"},
		{Key: "RetryTimes", Value: "2"},
	} {
		t.Run(invalid.Key, func(t *testing.T) {
			if strings.HasPrefix(invalid.Key, "log_setting.") {
				require.Error(t, UpdateOption(invalid.Key, invalid.Value), "single and batch APIs enforce the same storage bounds")
			}
			err := UpdateOrdinaryOptionsBatch(context.Background(), []Option{{Key: "TestUnpublished", Value: "draft"}, invalid}, AuditEventDraft{})
			var keyError *OptionUpdateError
			require.ErrorAs(t, err, &keyError)
			assert.Equal(t, invalid.Key, keyError.Key)
			var count int64
			require.NoError(t, db.Model(&Option{}).Count(&count).Error)
			assert.Zero(t, count)
			assert.NotContains(t, common.OptionMap, "TestUnpublished")
		})
	}
}

func TestOrdinaryOptionsBatchSyncOrderingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, scenario := range []string{"empty topic", "existing topic", "cleaned topic", "first writer rolls back"} {
			t.Run(dialect+"/"+scenario, func(t *testing.T) {
				db := optionTestDB(t, dialect)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(4)
				baselineCount := 0
				if scenario == "existing topic" || scenario == "cleaned topic" {
					require.NoError(t, UpdateOrdinaryOptionsBatch(context.Background(), []Option{{Key: "TestSyncSeed", Value: "seed"}}, AuditEventDraft{
						EventId: common.NewRequestId(), Action: "option.batch.test",
					}))
					baselineCount = 1
				}
				if scenario == "cleaned topic" {
					require.NoError(t, db.Where("type = ?", SyncTaskType(SyncTopicOptions)).Delete(&SystemTask{}).Error)
					baselineCount = 0
				}
				baselineRevisions, err := GetLatestSyncRevisions()
				require.NoError(t, err)
				firstAudit := AuditEventDraft{EventId: common.NewRequestId(), Action: "option.batch.test"}
				firstHeld := make(chan struct{})
				releaseFirst := make(chan struct{})
				var releaseOnce sync.Once
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				var waitingCtx context.Context
				attemptedWrites := make(chan error, 2)
				const callbackName = "test:hold_option_commit_after_sync_id"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
					if option, ok := tx.Statement.Dest.(*Option); ok && tx.Statement.Context == waitingCtx &&
						(option.Key == "TestSyncSecond" || option.Key == "TestSyncThird") {
						attemptedWrites <- tx.Statement.Context.Err()
					}
					event, ok := tx.Statement.Dest.(*AuditEventOutbox)
					if !ok || event.EventId != firstAudit.EventId {
						return
					}
					close(firstHeld)
					select {
					case <-releaseFirst:
						if scenario == "first writer rolls back" {
							tx.AddError(fmt.Errorf("audit write rejected"))
						}
					case <-ctx.Done():
						tx.AddError(ctx.Err())
					}
				}))
				t.Cleanup(func() { assert.NoError(t, db.Callback().Create().Remove(callbackName)) })
				// The legacy post-commit notification has no context parameter. Give
				// its DB handle the same bounded cancellation as the waiting saves;
				// the first save explicitly keeps its longer request context.
				var cancelWaiting context.CancelFunc
				waitingCtx, cancelWaiting = context.WithTimeout(ctx, time.Second)
				defer cancelWaiting()
				DB = db.WithContext(waitingCtx)
				var workers sync.WaitGroup
				t.Cleanup(func() {
					cancelWaiting()
					cancel()
					releaseOnce.Do(func() { close(releaseFirst) })
					workers.Wait()
					DB = db
				})
				firstResult := make(chan error, 1)
				workers.Go(func() {
					firstResult <- UpdateOrdinaryOptionsBatch(ctx, []Option{{Key: "TestSyncFirst", Value: "first"}}, firstAudit)
				})
				select {
				case <-firstHeld:
				case <-ctx.Done():
					t.Fatal("first writer did not reach the commit barrier")
				}
				type saveResult struct {
					key string
					err error
				}
				results := make(chan saveResult, 3)
				for _, key := range []string{"TestSyncSecond", "TestSyncThird"} {
					workers.Go(func() {
						err := UpdateOrdinaryOptionsBatch(waitingCtx, []Option{{Key: key, Value: "later"}}, AuditEventDraft{
							EventId: common.NewRequestId(), Action: "option.batch.test",
						})
						results <- saveResult{key: key, err: err}
					})
				}
				workers.Go(func() {
					BumpSyncTopic(SyncTopicOptions)
					results <- saveResult{}
				})
				for range 2 {
					select {
					case err := <-attemptedWrites:
						require.NoError(t, err, "each waiter must attempt its write before cancellation")
					case <-ctx.Done():
						t.Fatal("waiting saves did not reach their database writes")
					}
				}
				for range 3 {
					select {
					case result := <-results:
						if result.key != "" {
							assert.Error(t, result.err, "a later save cannot commit past the held sync ID")
						}
					case <-ctx.Done():
						t.Fatal("waiting writers did not honor cancellation")
					}
				}
				assert.ErrorIs(t, waitingCtx.Err(), context.DeadlineExceeded)
				var visibleTasks []SystemTask
				require.NoError(t, db.Where("type = ?", SyncTaskType(SyncTopicOptions)).Find(&visibleTasks).Error)
				assert.Len(t, visibleTasks, baselineCount, "another node must not observe a revision beyond the uncommitted first write")
				var pendingOptions []Option
				require.NoError(t, db.Where(map[string]any{"key": []string{"TestSyncFirst", "TestSyncSecond", "TestSyncThird"}}).Find(&pendingOptions).Error)
				assert.Empty(t, pendingOptions, "canceled waiters roll back their disjoint setting writes")
				common.OptionMapRWMutex.RLock()
				assert.NotContains(t, common.OptionMap, "TestSyncFirst")
				assert.NotContains(t, common.OptionMap, "TestSyncSecond")
				assert.NotContains(t, common.OptionMap, "TestSyncThird")
				common.OptionMapRWMutex.RUnlock()
				releaseOnce.Do(func() { close(releaseFirst) })
				select {
				case err = <-firstResult:
				case <-ctx.Done():
					t.Fatal("first writer did not finish after release")
				}
				workers.Wait()
				DB = db
				if scenario == "first writer rolls back" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					baselineCount++
				}
				var committedTasks []SystemTask
				require.NoError(t, db.Where("type = ?", SyncTaskType(SyncTopicOptions)).Order("id").Find(&committedTasks).Error)
				require.Len(t, committedTasks, baselineCount)
				previousRevision := baselineRevisions[SyncTopicOptions]
				if scenario != "first writer rolls back" {
					require.Greater(t, committedTasks[len(committedTasks)-1].ID, previousRevision)
					previousRevision = committedTasks[len(committedTasks)-1].ID
				}
				// A rollback releases the first-insert lock too. Later successful
				// saves must remain discoverable from the last committed revision.
				for _, key := range []string{"TestSyncSecond", "TestSyncThird"} {
					require.NoError(t, UpdateOrdinaryOptionsBatch(ctx, []Option{{Key: key, Value: "retried"}}, AuditEventDraft{
						EventId: common.NewRequestId(), Action: "option.batch.test",
					}))
					revisions, err := GetLatestSyncRevisions()
					require.NoError(t, err)
					require.Greater(t, revisions[SyncTopicOptions], previousRevision)
					previousRevision = revisions[SyncTopicOptions]
					require.NoError(t, LoadOptionsFromDatabase())
					assert.Equal(t, "retried", common.OptionMap[key])
				}
				BumpSyncTopic(SyncTopicOptions)
				revisions, err := GetLatestSyncRevisions()
				require.NoError(t, err)
				assert.Greater(t, revisions[SyncTopicOptions], previousRevision)
			})
		}
	}
}

func TestOrdinaryOptionsSyncAnchorIsInternal(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := optionTestDB(t, dialect)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bumpSyncTopic(tx, SyncTopicOptions) }))
			var anchor SystemTaskLock
			require.NoError(t, db.Where("type = ?", optionsSyncAnchorType).First(&anchor).Error)
			assert.Equal(t, optionsSyncAnchorTaskID, anchor.TaskID)
			assert.Equal(t, optionsSyncAnchorLockedBy, anchor.LockedBy)
			assert.Zero(t, anchor.LockedUntil)
			var optionCount int64
			require.NoError(t, db.Model(&Option{}).Count(&optionCount).Error)
			assert.Zero(t, optionCount, "sync coordination must not create a user-editable option")
			for _, taskType := range []string{optionsSyncAnchorType, strings.ToUpper(optionsSyncAnchorType), optionsSyncAnchorType + " "} {
				acquired, _, err := acquireSystemTaskLock(taskType, "unexpected-task", "unexpected-runner", common.GetTimestamp(), common.GetTimestamp()+60)
				require.Error(t, err)
				assert.False(t, acquired)
			}
			require.NoError(t, ExpireStaleSystemTaskLocks(common.GetTimestamp()+60))
			require.NoError(t, ReleaseSystemTaskLock(anchor.TaskID, anchor.LockedBy))
			require.Error(t, RenewSystemTaskLock(anchor.TaskID, anchor.LockedBy, common.GetTimestamp()+60))
			var afterCleanup SystemTaskLock
			require.NoError(t, db.Where("type = ?", optionsSyncAnchorType).First(&afterCleanup).Error)
			assert.Equal(t, anchor, afterCleanup, "task maintenance must leave the anchor in place")
			// Old candidate names and collation aliases remain ordinary settings.
			// None can address the anchor in the separate coordination table.
			for _, key := range []string{"internal.sync_topic.options", "INTERNAL.SYNC_TOPIC.OPTIONS", "internal.sync_topic.options ", "intérnal.sync_topic.options", optionsSyncAnchorType} {
				require.NoError(t, UpdateOption(key, "single"))
				require.NoError(t, UpdateOptionsBulk(map[string]string{key: "bulk"}))
				require.NoError(t, UpdateOrdinaryOptionsBatch(context.Background(), []Option{{Key: key, Value: "batch"}}, AuditEventDraft{
					EventId: common.NewRequestId(), Action: "option.batch.test",
				}))
			}
			require.NoError(t, LoadOptionsFromDatabase())
			assert.Equal(t, "batch", common.OptionMap["internal.sync_topic.options"])
			assert.Equal(t, "batch", common.OptionMap[optionsSyncAnchorType])
			var retained SystemTaskLock
			require.NoError(t, db.Where("type = ?", optionsSyncAnchorType).First(&retained).Error)
			assert.Equal(t, anchor, retained, "cleanup, task leases and option writes preserve the permanent anchor")
			// Removing all sync history does not remove the next write's anchor.
			require.NoError(t, db.Where("type = ?", SyncTaskType(SyncTopicOptions)).Delete(&SystemTask{}).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bumpSyncTopic(tx, SyncTopicOptions) }))
			var syncCount int64
			require.NoError(t, db.Model(&SystemTask{}).Count(&syncCount).Error)
			assert.Equal(t, int64(1), syncCount)
		})
	}
}

// optionPublicationCommitPool delays one acknowledgement only after SQL Commit
// really succeeds, leaving the connection free for a later committed save.
type optionPublicationCommitPool struct {
	*sql.DB
	hold      atomic.Bool
	committed chan struct{}
	release   chan struct{}
	ctx       context.Context
	onCommit  func()
}

type optionPublicationCommitTx struct {
	*sql.Tx
	pool *optionPublicationCommitPool
}

func (pool *optionPublicationCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &optionPublicationCommitTx{Tx: tx, pool: pool}, nil
}

func (tx *optionPublicationCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.pool.onCommit != nil {
		tx.pool.onCommit()
	}
	if tx.pool.hold.CompareAndSwap(true, false) {
		close(tx.pool.committed)
		select {
		case <-tx.pool.release:
		case <-tx.pool.ctx.Done():
			return tx.pool.ctx.Err()
		}
	}
	return nil
}

func configureAnnouncementPasskeyDomain(t *testing.T, db *gorm.DB) (system_setting.PasskeySettings, string, map[string]string) {
	t.Helper()
	settings := system_setting.GetPasskeySettings()
	previousSettings := *settings
	common.OptionMapRWMutex.RLock()
	previousServerAddress := system_setting.ServerAddress
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*settings = previousSettings
		system_setting.ServerAddress = previousServerAddress
		common.OptionMapRWMutex.Unlock()
	})

	const serverAddress = "https://passkey.example.com"
	configured := previousSettings
	configured.RPID = "passkey.example.com"
	configured.LegacyRPIDs = ""
	configured.Origins = serverAddress
	values := map[string]string{
		"ServerAddress":         serverAddress,
		"passkey.rp_id":         configured.RPID,
		"passkey.legacy_rp_ids": configured.LegacyRPIDs,
		"passkey.origins":       configured.Origins,
	}
	// The fixture has its own fresh option database. Install the matching runtime
	// baseline so the real specialized transaction sees an unchanged domain and
	// can prove that announcement publication does not disturb that trust state.
	common.OptionMapRWMutex.Lock()
	*settings = configured
	system_setting.ServerAddress = serverAddress
	for key, value := range values {
		common.OptionMap[key] = value
	}
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, UpdateOptionsBulk(values))
	var stored []Option
	require.NoError(t, db.Where(map[string]any{"key": []string{
		"ServerAddress", "passkey.rp_id", "passkey.legacy_rp_ids", "passkey.origins",
	}}).Find(&stored).Error)
	assert.Len(t, stored, len(values))
	for _, option := range stored {
		assert.Equal(t, values[option.Key], option.Value)
	}
	return configured, serverAddress, values
}

func TestAnnouncementPublicationOrderingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, writer := range []string{"single", "bulk", "batch", "passkey mixed"} {
			t.Run(dialect+"/"+writer, func(t *testing.T) {
				db := optionTestDB(t, dialect)
				console := console_setting.GetConsoleSetting()
				previous := *console
				t.Cleanup(func() {
					common.OptionMapRWMutex.Lock()
					*console = previous
					common.OptionMapRWMutex.Unlock()
				})
				const key = "console_setting.announcements"
				const enabledKey = "console_setting.announcements_enabled"
				require.NoError(t, UpdateOption(key, "[]"))
				require.NoError(t, UpdateOption(enabledKey, "true"))
				var passkeyBefore system_setting.PasskeySettings
				var serverAddressBefore string
				var passkeyOptionsBefore map[string]string
				if writer == "passkey mixed" {
					passkeyBefore, serverAddressBefore, passkeyOptionsBefore = configureAnnouncementPasskeyDomain(t, db)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(4)
				pool := &optionPublicationCommitPool{DB: sqlDB, committed: make(chan struct{}), release: make(chan struct{}), ctx: ctx}
				pool.hold.Store(true)
				wrapped := db.Session(&gorm.Session{NewDB: true, Initialized: true})
				wrapped.ConnPool, wrapped.Statement.ConnPool = pool, pool
				DB = wrapped.Session(&gorm.Session{NewDB: true, Context: ctx})
				var workers sync.WaitGroup
				var releaseOnce sync.Once
				t.Cleanup(func() {
					releaseOnce.Do(func() { close(pool.release) })
					cancel()
					workers.Wait()
					DB = db
				})
				const first = `[{"id":1,"content":"earlier"}]`
				const latest = `[{"id":1,"content":"latest"}]`
				firstResult := make(chan error, 1)
				workers.Go(func() {
					switch writer {
					case "single":
						firstResult <- UpdateOption(key, first)
					case "bulk":
						firstResult <- UpdateOptionsBulk(map[string]string{key: first})
					case "batch":
						firstResult <- UpdateOrdinaryOptionsBatch(ctx, []Option{{Key: key, Value: first}}, AuditEventDraft{
							EventId: common.NewRequestId(), Action: "option.batch.test",
						})
					case "passkey mixed":
						// An unchanged Passkey domain value exercises the specialized path.
						firstResult <- UpdateOptionsBulk(map[string]string{
							"ServerAddress":                         serverAddressBefore,
							key:                                      first,
							"console_setting.announcements_enabled": "false",
						})
					}
				})
				select {
				case <-pool.committed:
				case <-ctx.Done():
					t.Fatal("first save did not reach its post-commit barrier")
				}
				var stored Option
				require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
				assert.Equal(t, first, stored.Value, "the held writer already committed")
				require.NoError(t, UpdateOrdinaryOptionsBatch(ctx, []Option{
					{Key: key, Value: latest},
					{Key: enabledKey, Value: "true"},
				}, AuditEventDraft{
					EventId: common.NewRequestId(), Action: "option.batch.test",
				}))
				common.OptionMapRWMutex.RLock()
				assert.Equal(t, latest, common.OptionMap[key])
				assert.Equal(t, "true", common.OptionMap[enabledKey])
				assert.Equal(t, latest, console.Announcements)
				assert.True(t, console.AnnouncementsEnabled)
				common.OptionMapRWMutex.RUnlock()
				if writer != "passkey mixed" {
					// This full reload is part of the independent reload/A-B case.
					// The passkey transaction deliberately retains its specialized
					// serialization lock through its post-commit publication.
					require.NoError(t, LoadOptionsFromDatabase())
				}
				releaseOnce.Do(func() { close(pool.release) })
				select {
				case err := <-firstResult:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("first save did not finish after acknowledgement release")
				}
				workers.Wait()
				require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
				assert.Equal(t, latest, stored.Value)
				var storedEnabled Option
				require.NoError(t, db.Where(&Option{Key: enabledKey}).First(&storedEnabled).Error)
				assert.Equal(t, "true", storedEnabled.Value)
				common.OptionMapRWMutex.RLock()
				assert.Equal(t, latest, common.OptionMap[key], "a delayed publisher cannot replay an obsolete request")
				assert.Equal(t, "true", common.OptionMap[enabledKey])
				assert.Equal(t, latest, console.Announcements, "public status reads the latest runtime config")
				assert.True(t, console.AnnouncementsEnabled)
				if writer == "passkey mixed" {
					assert.Equal(t, passkeyBefore, *system_setting.GetPasskeySettings(), "the committed announcement publication preserves Passkey trust state")
					assert.Equal(t, serverAddressBefore, system_setting.ServerAddress)
				}
				common.OptionMapRWMutex.RUnlock()
				if writer == "passkey mixed" {
					var storedPasskeyOptions []Option
					require.NoError(t, db.Where(map[string]any{"key": []string{
						"ServerAddress", "passkey.rp_id", "passkey.legacy_rp_ids", "passkey.origins",
					}}).Find(&storedPasskeyOptions).Error)
					assert.Len(t, storedPasskeyOptions, len(passkeyOptionsBefore))
					for _, option := range storedPasskeyOptions {
						assert.Equal(t, passkeyOptionsBefore[option.Key], option.Value, "the announcement write preserves stored Passkey trust settings")
					}
					assert.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						return validatePasskeyRPIDWithTx(tx, passkeyBefore.EffectiveRPID())
					}), "the existing Passkey RP remains valid")
				}
			})
		}
	}
}

func TestAnnouncementPublicationReadFailureRetainsCommittedSave(t *testing.T) {
	db := optionTestDB(t, "sqlite")
	settings := system_setting.GetPasskeySettings()
	previousPasskeySettings := *settings
	common.OptionMapRWMutex.RLock()
	previousServerAddress := system_setting.ServerAddress
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*settings = previousPasskeySettings
		system_setting.ServerAddress = previousServerAddress
		common.OptionMapRWMutex.Unlock()
	})
	const key = "console_setting.announcements"
	const enabledKey = "console_setting.announcements_enabled"
	console := console_setting.GetConsoleSetting()
	previous := *console
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		*console = previous
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, UpdateOption(key, "before"))
	require.NoError(t, UpdateOption(enabledKey, "true"))
	const callback = "test:reject_saved_option_readback"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Option" {
			tx.AddError(fmt.Errorf("read unavailable"))
		}
	}))
	t.Cleanup(func() { assert.NoError(t, db.Callback().Query().Remove(callback)) })
	require.NoError(t, UpdateOrdinaryOptionsBatch(t.Context(), []Option{{Key: key, Value: "committed"}}, AuditEventDraft{
		EventId: common.NewRequestId(), Action: "option.batch.test",
	}), "post-commit read failure must not prompt replay of a persisted save")
	assert.Equal(t, "before", common.OptionMap[key])
	assert.Equal(t, "true", common.OptionMap[enabledKey])
	assert.Equal(t, "before", console.Announcements)
	assert.True(t, console.AnnouncementsEnabled)
	// Unrelated settings retain immediate local publication even while the
	// announcement readback is unavailable; no generic flag gains this failure.
	require.NoError(t, UpdateOption("TestUnrelatedPublication", "published"))
	assert.Equal(t, "published", common.OptionMap["TestUnrelatedPublication"])
	require.NoError(t, db.Callback().Query().Remove(callback))
	var stored Option
	require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
	assert.Equal(t, "committed", stored.Value)
	require.NoError(t, LoadOptionsFromDatabase())
	assert.Equal(t, "committed", common.OptionMap[key], "durable reload repairs the retained local view")

	// The specialized Passkey bulk path may include unrelated options in its
	// transaction. A failure in the later announcement readback must still
	// preserve its committed result, sync obligation and Passkey settings.
	passkeyBefore, serverAddressBefore, passkeyValues := configureAnnouncementPasskeyDomain(t, db)
	const specializedAnnouncement = `[{"id":1,"content":"specialized committed"}]`
	var beforeSyncCount int64
	require.NoError(t, db.Model(&SystemTask{}).Where("type = ?", SyncTaskType(SyncTopicOptions)).Count(&beforeSyncCount).Error)
	const passkeyReadFailureCallback = "test:reject_specialized_announcement_readback"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(passkeyReadFailureCallback, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Option" {
			return
		}
		if _, announcementReadback := tx.Statement.Dest.(*[]Option); announcementReadback {
			tx.AddError(fmt.Errorf("specialized announcement read unavailable"))
		}
	}))
	t.Cleanup(func() { assert.NoError(t, db.Callback().Query().Remove(passkeyReadFailureCallback)) })
	values := make(map[string]string, len(passkeyValues)+3)
	for optionKey, value := range passkeyValues {
		values[optionKey] = value
	}
	values[key] = specializedAnnouncement
	values[enabledKey] = "false"
	values["TestSpecializedUnrelated"] = "published"
	require.NoError(t, UpdateOptionsBulk(values), "announcement readback failure must not undo or misreport the confirmed Passkey-domain write")
	var committedAnnouncement Option
	require.NoError(t, db.Where(&Option{Key: key}).First(&committedAnnouncement).Error)
	assert.Equal(t, specializedAnnouncement, committedAnnouncement.Value)
	var committedEnabled Option
	require.NoError(t, db.Where(&Option{Key: enabledKey}).First(&committedEnabled).Error)
	assert.Equal(t, "false", committedEnabled.Value)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, "committed", common.OptionMap[key], "failed readback keeps the last good local announcement")
	assert.Equal(t, "true", common.OptionMap[enabledKey])
	assert.Equal(t, "committed", console.Announcements)
	assert.True(t, console.AnnouncementsEnabled, "failed readback retains the last published enabled state")
	assert.Equal(t, passkeyBefore, *settings, "the same-value Passkey domain save preserves its trust state")
	assert.Equal(t, serverAddressBefore, system_setting.ServerAddress)
	assert.Equal(t, "published", common.OptionMap["TestSpecializedUnrelated"], "unrelated options still publish immediately")
	common.OptionMapRWMutex.RUnlock()
	var afterSyncCount int64
	require.NoError(t, db.Model(&SystemTask{}).Where("type = ?", SyncTaskType(SyncTopicOptions)).Count(&afterSyncCount).Error)
	assert.Equal(t, beforeSyncCount+1, afterSyncCount, "the committed specialized write retains its sync obligation")
	require.NoError(t, db.Callback().Query().Remove(passkeyReadFailureCallback))
	var storedPasskeyOptions []Option
	require.NoError(t, db.Where(map[string]any{"key": []string{
		"ServerAddress", "passkey.rp_id", "passkey.legacy_rp_ids", "passkey.origins",
	}}).Find(&storedPasskeyOptions).Error)
	assert.Len(t, storedPasskeyOptions, len(passkeyValues))
	for _, option := range storedPasskeyOptions {
		assert.Equal(t, passkeyValues[option.Key], option.Value)
	}
	var storedUnrelated Option
	require.NoError(t, db.Where(&Option{Key: "TestSpecializedUnrelated"}).First(&storedUnrelated).Error)
	assert.Equal(t, "published", storedUnrelated.Value)
	require.NoError(t, LoadOptionsFromDatabase())
	assert.Equal(t, specializedAnnouncement, common.OptionMap[key], "durable reload repairs the announcement after readback recovery")
	assert.Equal(t, "false", common.OptionMap[enabledKey])
	assert.Equal(t, specializedAnnouncement, console.Announcements)
	assert.False(t, console.AnnouncementsEnabled)
	assert.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return validatePasskeyRPIDWithTx(tx, passkeyBefore.EffectiveRPID())
	}), "the existing Passkey RP remains valid after readback recovery")
}

func TestAnnouncementReloadPublicationOrderingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := optionTestDB(t, dialect)
			const key = "console_setting.announcements"
			console := console_setting.GetConsoleSetting()
			previous := *console
			t.Cleanup(func() {
				common.OptionMapRWMutex.Lock()
				*console = previous
				common.OptionMapRWMutex.Unlock()
			})
			require.NoError(t, UpdateOption(key, "before"))
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			loaded := make(chan struct{})
			release := make(chan struct{})
			committed := make(chan struct{})
			var releaseOnce sync.Once
			var loadedOnce atomic.Bool
			const callback = "test:hold_old_announcement_reload"
			require.NoError(t, db.Callback().Query().After("gorm:after_query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Option" {
					return
				}
				if _, fullReload := tx.Statement.Dest.(*[]*Option); !fullReload || !loadedOnce.CompareAndSwap(false, true) {
					return
				}
				close(loaded)
				select {
				case <-release:
				case <-ctx.Done():
					tx.AddError(ctx.Err())
				}
			}))
			t.Cleanup(func() { assert.NoError(t, db.Callback().Query().Remove(callback)) })
			pool := &optionPublicationCommitPool{DB: sqlDB, ctx: ctx, onCommit: func() { close(committed) }}
			wrapped := db.Session(&gorm.Session{NewDB: true, Initialized: true})
			wrapped.ConnPool, wrapped.Statement.ConnPool = pool, pool
			DB = wrapped.Session(&gorm.Session{NewDB: true, Context: ctx})
			var workers sync.WaitGroup
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				workers.Wait()
				DB = db
			})
			reloadResult := make(chan error, 1)
			workers.Go(func() { reloadResult <- LoadOptionsFromDatabase() })
			select {
			case <-loaded:
			case <-ctx.Done():
				t.Fatal("reload did not capture the old database snapshot")
			}
			saveResult := make(chan error, 1)
			workers.Go(func() {
				saveResult <- UpdateOrdinaryOptionsBatch(ctx, []Option{{Key: key, Value: "latest"}}, AuditEventDraft{
					EventId: common.NewRequestId(), Action: "option.batch.test",
				})
			})
			select {
			case <-committed:
			case <-ctx.Done():
				t.Fatal("announcement save did not commit while reload held its old snapshot")
			}
			var stored Option
			require.NoError(t, db.Where(&Option{Key: key}).First(&stored).Error)
			assert.Equal(t, "latest", stored.Value)
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-reloadResult:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("reload did not finish")
			}
			select {
			case err := <-saveResult:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("saved announcement publication did not finish")
			}
			workers.Wait()
			common.OptionMapRWMutex.RLock()
			assert.Equal(t, "latest", common.OptionMap[key], "a captured reload cannot overwrite the latest local save")
			assert.Equal(t, "latest", console.Announcements)
			common.OptionMapRWMutex.RUnlock()
		})
	}
}

func TestOrdinaryOptionsQueuedSyncWritersDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := optionTestDB(t, dialect)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return bumpSyncTopic(tx, SyncTopicOptions)
			}))
			first := db.WithContext(ctx).Begin()
			require.NoError(t, first.Error)
			t.Cleanup(func() { _ = first.Rollback().Error })
			require.NoError(t, bumpSyncTopic(first, SyncTopicOptions))
			audits := []AuditEventDraft{
				{EventId: common.NewRequestId(), Action: "option.batch.test"},
				{EventId: common.NewRequestId(), Action: "option.batch.test"},
			}
			waiterContexts := make([]context.Context, 2)
			waiterCancels := make([]context.CancelFunc, 2)
			for i := range 2 {
				waiterContexts[i], waiterCancels[i] = context.WithCancel(ctx)
			}
			attempted := make(chan struct{}, 2)
			held := make(chan int, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			var heldOne atomic.Bool
			const callbackName = "test:hold_next_options_sync_writer"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
				if option, ok := tx.Statement.Dest.(*Option); ok && strings.HasPrefix(option.Key, "TestQueuedSync") {
					attempted <- struct{}{}
				}
				event, ok := tx.Statement.Dest.(*AuditEventOutbox)
				if !ok || !heldOne.CompareAndSwap(false, true) {
					return
				}
				for i := range audits {
					if event.EventId != audits[i].EventId {
						continue
					}
					held <- i
					select {
					case <-release:
					case <-ctx.Done():
						tx.AddError(ctx.Err())
					}
					return
				}
			}))
			t.Cleanup(func() { assert.NoError(t, db.Callback().Create().Remove(callbackName)) })
			var workers sync.WaitGroup
			t.Cleanup(func() {
				for _, cancelWaiter := range waiterCancels {
					cancelWaiter()
				}
				cancel()
				releaseOnce.Do(func() { close(release) })
				_ = first.Rollback().Error
				workers.Wait()
			})
			type saveResult struct {
				index int
				err   error
			}
			results := make(chan saveResult, 2)
			for i := range 2 {
				workers.Go(func() {
					err := UpdateOrdinaryOptionsBatch(waiterContexts[i], []Option{{Key: fmt.Sprintf("TestQueuedSync%d", i), Value: "queued"}}, audits[i])
					results <- saveResult{index: i, err: err}
				})
			}
			for range 2 {
				select {
				case <-attempted:
				case <-ctx.Done():
					t.Fatal("both queued writers must reach the database before the first commit")
				}
			}
			require.NoError(t, first.Commit().Error)
			var heldIndex int
			select {
			case heldIndex = <-held:
			case <-ctx.Done():
				t.Fatal("no queued writer acquired the released lock")
			}
			// Whichever writer acquired the anchor keeps it through commit. The
			// other request expires while that writer is held; it must not advance
			// the visible revision by locking the first writer's obsolete task row.
			otherIndex := 1 - heldIndex
			expiry := time.AfterFunc(time.Second, waiterCancels[otherIndex])
			defer expiry.Stop()
			select {
			case result := <-results:
				require.Equal(t, otherIndex, result.index)
				require.Error(t, result.err)
			case <-ctx.Done():
				t.Fatal("queued request did not honor cancellation")
			}
			var visible []SystemTask
			require.NoError(t, db.Where("type = ?", SyncTaskType(SyncTopicOptions)).Order("id").Find(&visible).Error)
			require.Len(t, visible, 2, "only the seed and first committed writer are visible")
			previousRevision := visible[1].ID
			releaseOnce.Do(func() { close(release) })
			select {
			case result := <-results:
				require.Equal(t, heldIndex, result.index)
				require.NoError(t, result.err)
			case <-ctx.Done():
				t.Fatal("held writer did not finish after release")
			}
			workers.Wait()
			revisions, err := GetLatestSyncRevisions()
			require.NoError(t, err)
			assert.Greater(t, revisions[SyncTopicOptions], previousRevision)
			require.NoError(t, LoadOptionsFromDatabase())
			assert.Equal(t, "queued", common.OptionMap[fmt.Sprintf("TestQueuedSync%d", heldIndex)])
			assert.NotContains(t, common.OptionMap, fmt.Sprintf("TestQueuedSync%d", otherIndex))
		})
	}
}
