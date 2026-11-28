package model

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
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

// channelUsageTestChannel and channelUsageTestOtherChannel are the channels
// the fixture rows are billed to, so the matrix cleanup can scope its deletes.
const (
	channelUsageTestChannel      = 9420
	channelUsageTestOtherChannel = 9421
)

// channelUsageTestLogs is the consume-log fixture behind the channel usage
// tests. Channel 9420 serves gpt-4o twice and gpt-4o-mini once, plus one error
// log that must never be counted; channel 9421 serves claude-3 once; channel
// 9422 has no logs at all.
func channelUsageTestLogs() []Log {
	return []Log{
		{ChannelId: channelUsageTestChannel, CreatedAt: 1700, Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 100, CompletionTokens: 10, Quota: 5},
		{ChannelId: channelUsageTestChannel, CreatedAt: 1800, Type: LogTypeConsume, ModelName: "gpt-4o", PromptTokens: 200, CompletionTokens: 20, Quota: 7},
		{ChannelId: channelUsageTestChannel, CreatedAt: 1900, Type: LogTypeConsume, ModelName: "gpt-4o-mini", PromptTokens: 300, CompletionTokens: 30, Quota: 11},
		{ChannelId: channelUsageTestChannel, CreatedAt: 2000, Type: LogTypeError, ModelName: "gpt-4o", PromptTokens: 999, CompletionTokens: 999, Quota: 999},
		{ChannelId: channelUsageTestOtherChannel, CreatedAt: 2100, Type: LogTypeConsume, ModelName: "claude-3", PromptTokens: 400, CompletionTokens: 40, Quota: 13},
	}
}

// withOpenTestLogDatabase opens the log database for one dialect, points LOG_DB
// at it, and registers a cleanup that removes the fixture rows and closes the
// pool. Callers write the fixture rows through the returned handle.
func withOpenTestLogDatabase(t *testing.T, kind, dsn string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch kind {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "channel-usage.db"))
	case "mysql":
		driver = mysql.Open(dsn)
	case "postgres":
		driver = postgres.Open(dsn)
	case "clickhouse":
		driver = clickhouse.Open(normalizeClickHouseDSN(dsn))
	default:
		t.Fatalf("unsupported dialect %q", kind)
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	if kind == "clickhouse" {
		require.NoError(t, db.Exec(clickHouseLogCreateTableSQL(0)).Error)
	} else {
		require.NoError(t, db.AutoMigrate(&Log{}))
	}

	previousLogDB, previousType := LOG_DB, common.LogDatabaseType()
	LOG_DB = db
	common.SetLogDatabaseType(common.DatabaseType(kind))
	initCol()
	t.Cleanup(func() {
		if kind == "clickhouse" {
			require.NoError(t, db.Exec(
				"ALTER TABLE logs DELETE WHERE channel_id IN (?, ?) SETTINGS mutations_sync = 1",
				channelUsageTestChannel, channelUsageTestOtherChannel,
			).Error)
		} else {
			require.NoError(t, db.Where("channel_id IN ?",
				[]int{channelUsageTestChannel, channelUsageTestOtherChannel}).Delete(&Log{}).Error)
		}
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	return db
}

// runChannelUsageLogMatrix owns separate diagnostic and numeric databases.
// ClickHouse remains log-only; its numeric facts live in primary SQLite.
func runChannelUsageLogMatrix(t *testing.T, fn func(t *testing.T, kind string, db *gorm.DB)) {
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
			db := withOpenTestLogDatabase(t, dialect.kind, dsn)
			require.NoError(t, db.Create(channelUsageTestLogs()).Error)
			primaryDialect := dialect.kind
			if primaryDialect == "clickhouse" {
				primaryDialect = "sqlite"
			}
			primary := openUsageEventTestDatabase(t, primaryDialect)
			require.NoError(t, primary.AutoMigrate(&Ability{}))
			installUsageReaderTestCoverage(t, primary, 0, 1, 3000)
			for i, log := range channelUsageTestLogs() {
				if log.Type != LogTypeConsume {
					continue
				}
				input := usageEventTestInput(int64(i + 1))
				input.OccurredAt, input.ChannelID, input.ModelName = log.CreatedAt, int64(log.ChannelId), log.ModelName
				input.PromptTokens, input.CompletionTokens, input.QuotaMagnitude = int64(log.PromptTokens), int64(log.CompletionTokens), int64(log.Quota)
				receipt, err := PrepareUsageEvent(context.Background(), input, UsageEventSourceReady)
				require.NoError(t, err)
				if i != 2 {
					require.NoError(t, ApplyUsageEvent(context.Background(), receipt))
				}
			}
			fn(t, dialect.kind, db)
		})
	}
}

// channelUsageAbilityDelete removes only this suite's ability rows, so other
// packages sharing the CI database keep theirs.
func channelUsageAbilityDelete(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("DELETE FROM abilities WHERE channel_id IN (?, ?)",
		channelUsageTestChannel, channelUsageTestOtherChannel).Error)
}

// withOpenTestAbilityDatabase uses the primary fixture already opened by the
// matrix, including SQLite when diagnostics use ClickHouse.
func withOpenTestAbilityDatabase(t *testing.T, _ string) *gorm.DB {
	t.Helper()
	db := DB
	channelUsageAbilityDelete(t, db)
	t.Cleanup(func() { channelUsageAbilityDelete(t, db) })
	return db
}

// installUsageReadLineageTestOnly seeds synthetic reader membership for model
// package fixtures. It deliberately bypasses the public proof-bearing cutover
// path and must never be used by production code or treated as migration proof.
func installUsageReadLineageTestOnly(ctx context.Context, lineage UsageReadLineage) error {
	if ctx == nil || DB == nil || !validUsageReadLineage(lineage) {
		return ErrUsageEventInvalid
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUsageStatisticsTransition(tx); err != nil {
			return err
		}
		return installUsageReadLineageTx(tx, lineage)
	})
}

// The fixture owns an explicit closed-world source set. Production startup
// must never manufacture these attestations from an empty queue or MIN/MAX.
func installUsageReaderTestCoverage(t *testing.T, db *gorm.DB, legacyMaxID, start, end int64) {
	t.Helper()
	require.NoError(t, MigrateUsageEventBackfill(db))
	for _, table := range []any{&UsageStatisticsCoverage{}, &UsageStatisticsLineage{}, &UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}} {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []any{&UsageStatisticsCoverage{}, &UsageStatisticsLineage{}, &UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}} {
			require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
		}
	})
	ctx := context.Background()
	require.NoError(t, installUsageReadLineageTestOnly(ctx, UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: legacyMaxID,
		LegacyBaselineDigest: strings.Repeat("c", 64), FenceDigest: strings.Repeat("d", 64),
		LegacySourceNamespace: strings.Repeat("1", 32), LegacyCollectionEpoch: strings.Repeat("2", 32),
		LiveSourceNamespace: strings.Repeat("a", 32), LiveCollectionEpoch: strings.Repeat("b", 32),
		DashboardEligibility: UsageEventProjectHours,
	}))
	lineage, err := GetUsageReadLineage(ctx)
	require.NoError(t, err)
	for _, family := range []UsageStatisticsFamily{UsageStatisticsFacts, UsageStatisticsChannels, UsageStatisticsDashboard} {
		require.NoError(t, AttestUsageStatisticsCoverage(ctx, UsageStatisticsCoverage{
			Family: family, StartAt: start, EndAt: end, LineageDigest: lineage.LineageDigest,
			ManifestDigest: strings.Repeat("e", 64), SourceSeal: strings.Repeat("f", 64),
		}))
	}
}

func TestChannelUsageSumsConsumeLogsPerChannel(t *testing.T) {
	runChannelUsageLogMatrix(t, func(t *testing.T, kind string, db *gorm.DB) {
		t.Run("only consume logs are counted", func(t *testing.T) {
			usages, err := GetChannelUsages([]int{channelUsageTestChannel, channelUsageTestOtherChannel, 9422})
			require.NoError(t, err)
			require.Len(t, usages, 2, "the channel without logs is absent")

			assert.Equal(t, ChannelUsage{
				ChannelId:        channelUsageTestChannel,
				RequestCount:     3,
				PromptTokens:     600,
				CompletionTokens: 60,
				TotalTokens:      660,
				Quota:            23,
			}, usages[0])

			assert.Equal(t, ChannelUsage{
				ChannelId:        channelUsageTestOtherChannel,
				RequestCount:     1,
				PromptTokens:     400,
				CompletionTokens: 40,
				TotalTokens:      440,
				Quota:            13,
			}, usages[1])
		})

		t.Run("asking for a single channel returns only that channel", func(t *testing.T) {
			usages, err := GetChannelUsages([]int{channelUsageTestOtherChannel})
			require.NoError(t, err)
			require.Len(t, usages, 1)
			assert.Equal(t, channelUsageTestOtherChannel, usages[0].ChannelId)
			assert.Equal(t, int64(440), usages[0].TotalTokens)
		})

		t.Run("channels without consume logs are absent from the result", func(t *testing.T) {
			usages, err := GetChannelUsages([]int{9422})
			require.NoError(t, err)
			assert.Empty(t, usages)
		})

		t.Run("no channel ids skips the query", func(t *testing.T) {
			usages, err := GetChannelUsages(nil)
			require.NoError(t, err)
			assert.Empty(t, usages)
		})
		t.Run("retention and projection recovery preserve totals", func(t *testing.T) {
			before, err := GetChannelUsages([]int{channelUsageTestChannel, channelUsageTestOtherChannel})
			require.NoError(t, err)
			if kind == "clickhouse" {
				require.NoError(t, db.Exec("ALTER TABLE logs DELETE WHERE channel_id IN (?, ?) SETTINGS mutations_sync = 1", channelUsageTestChannel, channelUsageTestOtherChannel).Error)
			} else {
				require.NoError(t, db.Where("channel_id IN ?", []int{channelUsageTestChannel, channelUsageTestOtherChannel}).Delete(&Log{}).Error)
			}
			var retained int64
			require.NoError(t, db.Model(&Log{}).Where("channel_id IN ?", []int{channelUsageTestChannel, channelUsageTestOtherChannel}).Count(&retained).Error)
			assert.Zero(t, retained)
			after, err := GetChannelUsages([]int{channelUsageTestChannel, channelUsageTestOtherChannel})
			require.NoError(t, err)
			assert.Equal(t, before, after)
			_, err = RecoverUsageEventProjections(context.Background(), 100)
			require.NoError(t, err)
			after, err = GetChannelUsages([]int{channelUsageTestChannel, channelUsageTestOtherChannel})
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	})
}

func TestChannelUsageDetailMergesConfiguredAndLoggedModels(t *testing.T) {
	// The matrix owns both stores; enabled status comes only from primary data.
	runChannelUsageLogMatrix(t, func(t *testing.T, kind string, db *gorm.DB) {
		withOpenTestAbilityDatabase(t, kind)

		t.Run("models seen only in logs are disabled without ability rows", func(t *testing.T) {
			detail, err := GetChannelUsageDetail(channelUsageTestChannel, nil)
			require.NoError(t, err)
			require.Len(t, detail.Models, 2)
			assert.Equal(t, "gpt-4o", detail.Models[0].ModelName, "busiest model first")
			assert.Equal(t, int64(330), detail.Models[0].TotalTokens)
			assert.Equal(t, int64(2), detail.Models[0].RequestCount)
			assert.Equal(t, int64(12), detail.Models[0].Quota)
			assert.Equal(t, int64(1800), detail.Models[0].LastUsedAt)
			assert.False(t, detail.Models[0].Enabled, "no ability row means disabled")
			assert.Equal(t, "gpt-4o-mini", detail.Models[1].ModelName)
			assert.Equal(t, int64(330), detail.Models[1].TotalTokens)
			assert.Equal(t, int64(1900), detail.Models[1].LastUsedAt)
		})

		t.Run("detail totals match the channel-summary totals", func(t *testing.T) {
			detail, err := GetChannelUsageDetail(channelUsageTestChannel, nil)
			require.NoError(t, err)
			assert.Equal(t, channelUsageTestChannel, detail.ChannelId)
			assert.Equal(t, int64(3), detail.RequestCount)
			assert.Equal(t, int64(600), detail.PromptTokens)
			assert.Equal(t, int64(60), detail.CompletionTokens)
			assert.Equal(t, int64(660), detail.TotalTokens)
			assert.Equal(t, int64(23), detail.Quota)
		})

		t.Run("configured models without logs appear with zero usage", func(t *testing.T) {
			detail, err := GetChannelUsageDetail(channelUsageTestChannel, []string{"gpt-4o", " unused-model ", ""})
			require.NoError(t, err)
			require.Len(t, detail.Models, 3)

			byName := make(map[string]ChannelModelUsage, len(detail.Models))
			for _, usage := range detail.Models {
				byName[usage.ModelName] = usage
			}
			require.Contains(t, byName, "unused-model")
			assert.Zero(t, byName["unused-model"].RequestCount)
			assert.Zero(t, byName["unused-model"].TotalTokens)
			assert.Zero(t, byName["unused-model"].LastUsedAt)
			assert.NotContains(t, byName, "", "blank configured names are dropped")
			assert.Equal(t, int64(330), byName["gpt-4o"].TotalTokens, "a configured model keeps its logged usage")
			assert.Equal(t, int64(2), byName["gpt-4o"].RequestCount)
		})

		t.Run("ordering falls back to the model name when totals tie", func(t *testing.T) {
			// Both logged models total 330, so the tie breaks on the name.
			detail, err := GetChannelUsageDetail(channelUsageTestChannel, nil)
			require.NoError(t, err)
			require.Len(t, detail.Models, 2)
			assert.Equal(t, "gpt-4o", detail.Models[0].ModelName)
			assert.Equal(t, "gpt-4o-mini", detail.Models[1].ModelName)
		})

		t.Run("a channel without logs reports only its configured models", func(t *testing.T) {
			detail, err := GetChannelUsageDetail(9422, []string{"gpt-4o"})
			require.NoError(t, err)
			require.Len(t, detail.Models, 1)
			assert.Equal(t, "gpt-4o", detail.Models[0].ModelName)
			assert.Zero(t, detail.TotalTokens)
			assert.Zero(t, detail.RequestCount)
		})
	})
}

func TestChannelUsageDetailReadsEnabledFromAbilities(t *testing.T) {
	runChannelUsageLogMatrix(t, func(t *testing.T, kind string, db *gorm.DB) {
		abilityDB := withOpenTestAbilityDatabase(t, kind)
		require.NoError(t, abilityDB.Create([]Ability{
			{Group: "default", Model: "gpt-4o", ChannelId: channelUsageTestChannel, Enabled: true},
			{Group: "default", Model: "gpt-4o-mini", ChannelId: channelUsageTestChannel, Enabled: false},
			{Group: "default", Model: "gpt-4o", ChannelId: channelUsageTestOtherChannel, Enabled: true},
		}).Error)

		detail, err := GetChannelUsageDetail(channelUsageTestChannel, []string{"gpt-4o", "gpt-4o-mini", "unused-model"})
		require.NoError(t, err)
		require.Len(t, detail.Models, 3)

		byName := make(map[string]ChannelModelUsage, len(detail.Models))
		for _, usage := range detail.Models {
			byName[usage.ModelName] = usage
		}
		assert.True(t, byName["gpt-4o"].Enabled, "an enabled ability row enables the model")
		assert.False(t, byName["gpt-4o-mini"].Enabled, "a disabled ability row leaves the model disabled")
		assert.False(t, byName["unused-model"].Enabled, "a model without ability rows is disabled")
	})
}

func TestChannelUsageDurableReadBoundaries(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openUsageEventTestDatabase(t, dialect)
			require.NoError(t, db.AutoMigrate(&Ability{}))
			installUsageReaderTestCoverage(t, db, 0, 1, 2000000000)
			ctx := context.Background()
			t.Run("blank model contributes only to channel summary", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				input := usageEventTestInput(1)
				input.ModelName = ""
				receipt, err := PrepareUsageEvent(ctx, input, UsageEventSourceReady)
				require.NoError(t, err)
				require.NoError(t, ApplyUsageEvent(ctx, receipt))
				rows, err := GetChannelUsages([]int{int(input.ChannelID)})
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, int64(120), rows[0].TotalTokens)
				detail, err := GetChannelUsageDetail(int(input.ChannelID), []string{"configured"})
				require.NoError(t, err)
				require.Len(t, detail.Models, 1)
				assert.Zero(t, detail.TotalTokens)
				assert.Equal(t, "configured", detail.Models[0].ModelName)
			})
			t.Run("pending sentinel returns no partial total", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					for i := range usageReadPendingLimit + 1 {
						if _, err := PrepareUsageEventTx(tx, usageEventTestInput(int64(i+1)), UsageEventSourceReady); err != nil {
							return err
						}
					}
					return nil
				}))
				rows, err := GetChannelUsages([]int{91232})
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				assert.Nil(t, rows)
			})
			t.Run("overflow returns no wrapped total", func(t *testing.T) {
				clearUsageEventTestRows(t, db)
				require.NoError(t, db.Create(&UsageChannel{ProjectionVersion: UsageEventProjectionVersion, ChannelID: 91232, PromptTokens: math.MaxInt64, CompletionTokens: 1}).Error)
				rows, err := GetChannelUsages([]int{91232})
				assert.ErrorIs(t, err, ErrUsageEventOverflow)
				assert.Nil(t, rows)
			})
		})
	}
}
