package service

import (
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DSNs must point to isolated test databases. Logs use a separate SQLite
// database so the ranking path cannot accidentally depend on joining logs.
func TestRankingCacheTokensDatabaseMatrix(t *testing.T) {
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(dialect), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect == common.DatabaseTypeMySQL {
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			} else if dialect == common.DatabaseTypePostgreSQL {
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
			logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			logSQL.SetMaxOpenConns(1)
			t.Cleanup(func() { assert.NoError(t, logSQL.Close()) })
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			oldExport, oldConsume, oldRedis := common.DataExportEnabled, common.LogConsumeEnabled, common.RedisEnabled
			oldCache := model.CacheQuotaData
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dialect, common.DatabaseTypeSQLite)
			common.DataExportEnabled, common.LogConsumeEnabled, common.RedisEnabled = true, true, false
			model.CacheQuotaData = make(map[string]*model.QuotaData)
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMainType, oldLogType)
				common.DataExportEnabled, common.LogConsumeEnabled, common.RedisEnabled = oldExport, oldConsume, oldRedis
				model.CacheQuotaData = oldCache
			})
			require.NoError(t, db.AutoMigrate(&model.QuotaData{}, &model.User{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			query := "SELECT version()"
			if dialect == common.DatabaseTypeSQLite {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			for _, tc := range []struct {
				name, semantic                   string
				input, output, read, write, want int
			}{
				{"anthropic", "anthropic", 4, 4198, 200000, 1000, 205202},
				{"openai", "", 212586, 321, 201344, 0, 212907},
				{"openai-write", "openai", 10000, 100, 8000, 1000, 10100},
				{"cache-only", "anthropic", 0, 0, 500, 100, 600},
				{"uncached", "", 50, 10, 0, 0, 60},
			} {
				t.Run(tc.name, func(t *testing.T) {
					modelName := "ranking-cache-" + tc.name
					require.NoError(t, db.Where("model_name = ?", modelName).Delete(&model.QuotaData{}).Error)
					t.Cleanup(func() { assert.NoError(t, db.Where("model_name = ?", modelName).Delete(&model.QuotaData{}).Error) })
					other := model.NewLogOther()
					other.SetPublic("usage_semantic", tc.semantic)
					other.SetPublic("cache_tokens", tc.read)
					other.SetPublic("cache_write_tokens", tc.write)
					// The breakdown must never be added on top of the normalized total.
					other.SetPublic("cache_creation_tokens", tc.write)
					other.SetPublic("cache_creation_tokens_5m", tc.write)
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
					ctx.Set("username", "ranking-cache-test")
					for range 2 {
						model.RecordConsumeLog(ctx, 0, model.RecordConsumeLogParams{ModelName: modelName, PromptTokens: tc.input, CompletionTokens: tc.output, Other: other})
						model.SaveQuotaDataCache()
					}
					rows, err := model.GetRankingQuotaTotals(0, time.Now().Unix()+3600)
					require.NoError(t, err)
					require.Len(t, rows, 1)
					assert.Equal(t, int64(tc.want*2), rows[0].TotalTokens)
					assert.Equal(t, int64(tc.read*2), rows[0].CacheReadTokens)
					assert.Equal(t, int64(tc.write*2), rows[0].CacheCreationTokens)
					meta := map[string]rankingModelMeta{modelName: {vendor: "test-vendor"}}
					models := buildRankedModels(rows, int64(tc.want*2), nil, nil, meta, false)
					require.Len(t, models, 1)
					assert.Equal(t, rows[0].CacheReadTokens, models[0].CacheReadTokens)
					assert.Equal(t, rows[0].CacheCreationTokens, models[0].CacheCreationTokens)
					vendors := buildRankedVendors(rows, nil, int64(tc.want*2), meta, false)
					require.Len(t, vendors, 1)
					assert.Equal(t, int64(tc.want*2), vendors[0].TotalTokens)
					assert.Equal(t, rows[0].CacheReadTokens, vendors[0].CacheReadTokens)
					assert.Equal(t, rows[0].CacheCreationTokens, vendors[0].CacheCreationTokens)
					buckets, err := model.GetRankingQuotaBuckets(0, 0, 3600)
					require.NoError(t, err)
					var bucketTotal int64
					for _, bucket := range buckets {
						bucketTotal += bucket.Tokens
					}
					assert.Equal(t, int64(tc.want*2), bucketTotal)
					var aggregate model.QuotaData
					require.NoError(t, db.Where("model_name = ?", modelName).First(&aggregate).Error)
					assert.Equal(t, int64(tc.read*2), aggregate.CacheReadTokens)
					assert.Equal(t, int64(tc.write*2), aggregate.CacheCreationTokens)
					var logs []model.Log
					require.NoError(t, logDB.Where("model_name = ?", modelName).Find(&logs).Error)
					require.Len(t, logs, 2)
					assert.Equal(t, tc.input, logs[0].PromptTokens)
					assert.Equal(t, tc.output, logs[0].CompletionTokens)
				})
			}
			t.Run("all-model totals", func(t *testing.T) {
				require.NoError(t, db.AutoMigrate(&model.Ability{}, &model.Channel{}, &model.Model{}, &model.Vendor{}))
				model.InvalidatePricingCache()
				t.Cleanup(model.InvalidatePricingCache)
				now := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
				// Twenty-one distinct models exercise the public response's top-20
				// cutoff while keeping the header and cache totals complete.
				var names []string
				for index := range 21 {
					name := fmt.Sprintf("ranking-limit-%02d", index)
					names = append(names, name)
					require.NoError(t, db.Create(&model.QuotaData{ModelName: name, CreatedAt: now.Unix() - 3600, TokenUsed: 100 + index, CacheReadTokens: 20, CacheCreationTokens: 10}).Error)
				}
				t.Cleanup(func() { assert.NoError(t, db.Where("model_name IN ?", names).Delete(&model.QuotaData{}).Error) })
				config, err := rankingConfig("today")
				require.NoError(t, err)
				snapshot, err := buildRankingsSnapshot(config, now)
				require.NoError(t, err)
				require.Len(t, snapshot.Models, 20)
				assert.Equal(t, RankingTokenTotals{TotalTokens: 2310, CacheReadTokens: 420, CacheCreationTokens: 210}, snapshot.Totals)
				require.Len(t, snapshot.Vendors, 1)
				assert.Equal(t, int64(2310), snapshot.Vendors[0].TotalTokens)
				assert.Equal(t, int64(420), snapshot.Vendors[0].CacheReadTokens)
				assert.Equal(t, int64(210), snapshot.Vendors[0].CacheCreationTokens)
			})
		})
	}
}
