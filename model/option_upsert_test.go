package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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
		driver = sqlite.Open(":memory:")
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
	require.NoError(t, db.AutoMigrate(&Option{}, &SystemTask{}, &AuditEventOutbox{}))
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
