package model

import (
	"os"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const logFilterTestUser = 9001

// logFilterTestRows is the fixture behind the log search and log summary
// tests. Row A uses Anthropic usage semantics (prompt excludes cache), row B
// uses OpenAI semantics (prompt already includes cache), row C carries
// malformed log metadata, and row E is not a consume log.
func logFilterTestRows() []Log {
	return []Log{
		{UserId: logFilterTestUser, CreatedAt: 1000, Type: LogTypeConsume, Username: "alice", TokenName: "tok-main", ModelName: "gpt-4o", Group: "default", RequestId: "req-alpha", PromptTokens: 100, Other: `{"cache_tokens" : 80,"cache_write_tokens" : 20,"usage_semantic" : "anthropic"}`},
		{UserId: logFilterTestUser, CreatedAt: 1001, Type: LogTypeConsume, Username: "alice", TokenName: "tok-alt", ModelName: "gpt-4o-mini", Group: "vip", RequestId: "req-beta", PromptTokens: 200, Other: `{"cache_tokens":50}`},
		{UserId: logFilterTestUser, CreatedAt: 1002, Type: LogTypeConsume, Username: "bob", TokenName: "tok-main", ModelName: "gpt_x", Group: "default", RequestId: "req-gamma", Other: "not-json"},
		{UserId: logFilterTestUser, CreatedAt: 1003, Type: LogTypeConsume, Username: "bob", TokenName: "tok-alt", ModelName: "gptax", Group: "vip", RequestId: "req-delta"},
		{UserId: logFilterTestUser, CreatedAt: 1004, Type: LogTypeError, Username: "alice", ModelName: "gpt-4o", PromptTokens: 999, Other: `{"cache_tokens":999,"usage_semantic":"anthropic"}`},
	}
}

// runLogDatabaseMatrix runs fn against SQLite and, when the matching DSN is
// configured, MySQL, PostgreSQL and ClickHouse. Rows written by these tests
// are scoped to logFilterTestUser and removed afterwards.
func runLogDatabaseMatrix(t *testing.T, fn func(t *testing.T, db *gorm.DB)) {
	t.Helper()
	for _, dialect := range []struct{ kind, env string }{
		{"sqlite", ""},
		{"mysql", "TEST_MYSQL_DSN"},
		{"postgres", "TEST_POSTGRES_DSN"},
		{"clickhouse", "TEST_CLICKHOUSE_DSN"},
	} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			var driver gorm.Dialector
			switch dialect.kind {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				driver = mysql.Open(dsn)
			case "postgres":
				driver = postgres.Open(dsn)
			case "clickhouse":
				driver = clickhouse.Open(normalizeClickHouseDSN(dsn))
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)

			previousDB, previousType := LOG_DB, common.LogDatabaseType()
			LOG_DB = db
			common.SetLogDatabaseType(common.DatabaseType(dialect.kind))
			initCol()
			t.Cleanup(func() {
				if dialect.kind == "clickhouse" {
					require.NoError(t, db.Exec("ALTER TABLE logs DELETE WHERE user_id = ? SETTINGS mutations_sync = 1", logFilterTestUser).Error)
				} else {
					require.NoError(t, db.Where("user_id = ?", logFilterTestUser).Delete(&Log{}).Error)
				}
				LOG_DB = previousDB
				common.SetLogDatabaseType(previousType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})

			if dialect.kind == "clickhouse" {
				require.NoError(t, db.Exec(clickHouseLogCreateTableSQL(0)).Error)
			} else {
				require.NoError(t, db.AutoMigrate(&Log{}))
			}
			fn(t, db)
		})
	}
}

func TestLogFilterTextMatchModes(t *testing.T) {
	runLogDatabaseMatrix(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.Create(logFilterTestRows()).Error)

		// matchedModels returns the consume logs matching filter, by model name.
		matchedModels := func(filter LogFilter) []string {
			filter.UserId = logFilterTestUser
			filter.Type = LogTypeConsume
			logs, _, err := GetUserLogs(logFilterTestUser, filter, 0, 50)
			require.NoError(t, err)
			models := make([]string, 0, len(logs))
			for _, log := range logs {
				models = append(models, log.ModelName)
			}
			slices.Sort(models)
			return models
		}

		t.Run("fuzzy is the default and matches a substring", func(t *testing.T) {
			assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, matchedModels(LogFilter{ModelName: "4o"}))
			assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, matchedModels(LogFilter{ModelName: "4o", MatchMode: LogMatchFuzzy}))
		})

		t.Run("exact matches the whole value only", func(t *testing.T) {
			assert.Empty(t, matchedModels(LogFilter{ModelName: "4o", MatchMode: LogMatchExact}))
			assert.Equal(t, []string{"gpt-4o"}, matchedModels(LogFilter{ModelName: "gpt-4o", MatchMode: LogMatchExact}))
		})

		t.Run("exact keeps the historical % wildcard on the model name", func(t *testing.T) {
			assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, matchedModels(LogFilter{ModelName: "gpt-4%", MatchMode: LogMatchExact}))
		})

		t.Run("fuzzy treats LIKE wildcards in the value literally", func(t *testing.T) {
			assert.Equal(t, []string{"gpt_x"}, matchedModels(LogFilter{ModelName: "gpt_x"}))
			assert.Empty(t, matchedModels(LogFilter{ModelName: "t%o"}))
		})

		t.Run("fuzzy applies to token, group, username and request id", func(t *testing.T) {
			assert.Equal(t, []string{"gpt-4o", "gpt_x"}, matchedModels(LogFilter{Group: "def"}))
			assert.Len(t, matchedModels(LogFilter{TokenName: "tok-"}), 4)
			assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, matchedModels(LogFilter{Username: "ali"}))
			assert.Equal(t, []string{"gpt-4o"}, matchedModels(LogFilter{RequestId: "alpha"}))
			assert.Empty(t, matchedModels(LogFilter{RequestId: "alpha", MatchMode: LogMatchExact}))
		})

		t.Run("the user scope ignores filters on another user's logs", func(t *testing.T) {
			filter := LogFilter{UserId: logFilterTestUser + 1, MatchMode: LogMatchFuzzy}
			logs, total, err := GetUserLogs(logFilterTestUser+1, filter, 0, 50)
			require.NoError(t, err)
			assert.Zero(t, total)
			assert.Empty(t, logs)
		})
	})
}

func TestSumUsedQuotaCountsCacheTokensByUsageSemantics(t *testing.T) {
	runLogDatabaseMatrix(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.Create(logFilterTestRows()).Error)

		t.Run("anthropic prompt excludes cache while openai prompt includes it", func(t *testing.T) {
			stat, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser})
			require.NoError(t, err)
			assert.Equal(t, int64(130), stat.CacheReadTokens, "only consume logs count cache reads")
			assert.Equal(t, int64(400), stat.InputTokens, "(100 prompt + 80 read + 20 write) + 200 prompt")
		})

		t.Run("the text filters apply to the summary as well", func(t *testing.T) {
			stat, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser, Group: "vi"})
			require.NoError(t, err)
			assert.Equal(t, int64(50), stat.CacheReadTokens)
			assert.Equal(t, int64(200), stat.InputTokens)
		})

		t.Run("a log without cache metadata contributes only its prompt", func(t *testing.T) {
			stat, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser, ModelName: "gptax"})
			require.NoError(t, err)
			assert.Zero(t, stat.CacheReadTokens)
			assert.Zero(t, stat.InputTokens)
		})
	})
}
