// Package testutil holds fixture helpers shared by _test.go files of several
// packages. It is never imported by production code.
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// UseInactiveChannelBudget installs a minimal SQLite database as the main and
// log database and turns channel-budget enforcement off for the test.
//
// The ledger stays inactive when its activation marker is absent, but the
// activation probe fails closed when the main database is nil or has no
// options table, so packages whose fixtures never touch the database would
// otherwise refuse every request at admission. Enforcement is disabled through
// the documented switch, never by faking a test-only code path.
//
// The installed database only holds the Option table and no activation marker,
// which is the inactive state. A caller that needs more tables should keep its
// own database and add the Option table (and the CHANNEL_BUDGET_ENABLED=false
// environment) instead of using this helper.
func UseInactiveChannelBudget(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})

	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "inactive-channel-budget.db")), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, err := database.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, database.AutoMigrate(&model.Option{}))

	model.DB, model.LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
}
