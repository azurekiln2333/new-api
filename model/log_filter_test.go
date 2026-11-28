package model

import (
	"os"
	"slices"
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

func prepareUsageBackfillFixture(t *testing.T, primary *gorm.DB) {
	t.Helper()
	tables := []any{&UsageEventBackfillTuple{}, &UsageEventBackfill{}, &UsageStatisticsCoverage{}, &UsageStatisticsLineage{},
		&UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}}
	require.NoError(t, primary.Migrator().DropTable(tables...))
	t.Cleanup(func() { require.NoError(t, primary.Migrator().DropTable(tables...)) })
	require.NoError(t, MigrateUsageEventBackfill(primary))
	require.NoError(t, MigrateUsageEventBackfill(primary))
}

func usageBackfillFixturePlan(t *testing.T, source *gorm.DB) UsageEventBackfillPlan {
	t.Helper()
	kind := source.Dialector.Name()
	sourceKind := UsageEventSourceLegacyRelational
	highWatermark := int64(0)
	if kind == "clickhouse" {
		sourceKind = UsageEventSourceLegacyClickHouse
	} else {
		require.NoError(t, source.Model(&Log{}).
			Select("COALESCE(MAX(id), 0)").
			Where("created_at >= ? AND created_at <= ? AND type IN ?", int64(1000), int64(1004), []int{LogTypeConsume, LogTypeRefund}).
			Scan(&highWatermark).Error)
	}
	return UsageEventBackfillPlan{
		SourceNamespace: strings.Repeat("c", 32), SourceKind: sourceKind, CollectionEpoch: strings.Repeat("d", 32),
		StartAt: 1000, EndAt: 1004, HighWatermark: highWatermark, FenceDigest: strings.Repeat("e", 64),
	}
}

func inspectUsageBackfillFixture(t *testing.T, source *gorm.DB, plan UsageEventBackfillPlan, limit int) UsageEventBackfillPlan {
	t.Helper()
	cursor := UsageEventBackfillCursor{}
	for {
		page, err := InspectUsageEventBackfillBatch(t.Context(), source, plan, cursor, limit)
		require.NoError(t, err)
		cursor = page.Next
		if page.Exhausted {
			break
		}
	}
	plan.ExpectedRows, plan.ManifestDigest = cursor.Rows, cursor.Digest
	return plan
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
		primaryKind := db.Dialector.Name()
		if primaryKind == "clickhouse" {
			primaryKind = "sqlite"
		}
		primary := openUsageEventTestDatabase(t, primaryKind)
		prepareUsageBackfillFixture(t, primary)
		plan := usageBackfillFixturePlan(t, db)
		plan = inspectUsageBackfillFixture(t, db, plan, 2)
		progress, err := RunUsageEventBackfillBatch(t.Context(), db, plan, 100)
		require.NoError(t, err)
		require.True(t, progress.RetainedVerified)
		var eventsBefore, hoursBefore, quotaBefore int64
		require.NoError(t, primary.Model(&UsageEvent{}).Count(&eventsBefore).Error)
		require.NoError(t, primary.Model(&UsageHour{}).Count(&hoursBefore).Error)
		require.NoError(t, primary.Model(&UsageHour{}).Select("COALESCE(SUM(gross_quota), 0)").Scan(&quotaBefore).Error)
		replayed, err := RunUsageEventBackfillBatch(t.Context(), db, plan, 100)
		require.NoError(t, err)
		assert.True(t, replayed.RetainedVerified)
		assert.Zero(t, replayed.BatchRows, "completed import replay is a no-op")
		var eventsAfter, hoursAfter, quotaAfter int64
		require.NoError(t, primary.Model(&UsageEvent{}).Count(&eventsAfter).Error)
		require.NoError(t, primary.Model(&UsageHour{}).Count(&hoursAfter).Error)
		require.NoError(t, primary.Model(&UsageHour{}).Select("COALESCE(SUM(gross_quota), 0)").Scan(&quotaAfter).Error)
		assert.Equal(t, eventsBefore, eventsAfter)
		assert.Equal(t, hoursBefore, hoursAfter)
		assert.Equal(t, quotaBefore, quotaAfter)
		installUsageReaderTestCoverage(t, primary, 0, 1000, 1004)

		t.Run("unknown cache semantics are unavailable rather than a false zero", func(t *testing.T) {
			_, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser})
			assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
		})

		t.Run("anthropic prompt excludes cache while openai prompt includes it", func(t *testing.T) {
			stat, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser, RequestId: "req-alpha", MatchMode: LogMatchExact})
			require.NoError(t, err)
			assert.Equal(t, int64(80), stat.CacheReadTokens)
			assert.Equal(t, int64(200), stat.InputTokens)
		})

		t.Run("known raw tokens remain readable independently of cache metadata", func(t *testing.T) {
			tokens, err := SumUsageEventTokens(t.Context(), LogFilter{UserId: logFilterTestUser})
			require.NoError(t, err)
			assert.Equal(t, 300, tokens)
		})

		t.Run("cache-read alone cannot prove the input semantic or cache-write quantity", func(t *testing.T) {
			_, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser, Group: "vi"})
			assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
		})

		t.Run("a log without cache metadata is unknown rather than verified zero", func(t *testing.T) {
			_, err := SumUsedQuota(LogFilter{UserId: logFilterTestUser, ModelName: "gptax"})
			assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
		})
	})
}

func TestUsageLegacyQuotaDataBaselineIsBoundedAndStable(t *testing.T) {
	db := openUsageEventTestDatabase(t, "sqlite")
	tables := []any{&UsageEventBackfillTuple{}, &UsageEventBackfill{}, &UsageStatisticsCoverage{}, &UsageStatisticsLineage{}, &QuotaData{}}
	require.NoError(t, db.AutoMigrate(tables...))
	require.NoError(t, db.Migrator().DropTable(&UsageEventBackfillTuple{}, &UsageEventBackfill{}, &UsageStatisticsCoverage{}, &UsageStatisticsLineage{}))
	require.NoError(t, MigrateUsageEventBackfill(db))
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, db.Create(&[]QuotaData{
		{Id: 11, UserID: 1, Username: "alice", ModelName: "m1", CreatedAt: 1000, Quota: 4, Count: 1},
		{Id: 12, UserID: 2, Username: "bob", ModelName: "m2", CreatedAt: 2000, Quota: 8, Count: 1},
	}).Error)
	baseline, err := InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 2, baseline.Rows)
	assert.EqualValues(t, 12, baseline.MaxID)
	assert.Len(t, baseline.Digest, usageEventDigestLength)

	// A changed row invalidates the captured evidence before activation.
	require.NoError(t, db.Model(&QuotaData{}).Where("id = ?", 12).Update("quota", 9).Error)
	lineage := UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: baseline.MaxID, LegacyBaselineDigest: baseline.Digest,
		FenceDigest: strings.Repeat("f", usageEventDigestLength), LegacySourceNamespace: strings.Repeat("1", 32),
		LegacyCollectionEpoch: strings.Repeat("2", 32), LiveSourceNamespace: UsageLiveSourceNamespace(),
		LiveCollectionEpoch: UsageLiveCollectionEpoch(), DashboardEligibility: UsageEventProjectHours,
	}
	assert.ErrorIs(t, ActivateUsageStatisticsLineage(t.Context(), lineage, baseline), ErrUsageStatisticsNotReady)
}

func TestDeleteOldLogBatchRequiresExactLinkedEventPayload(t *testing.T) {
	for _, semantic := range []struct {
		label string
		value UsageSemantic
	}{
		{"openai", UsageSemanticOpenAI},
		{"anthropic", UsageSemanticAnthropic},
		{"provider-specific", UsageSemanticOtherExplicit},
	} {
		t.Run(semantic.label, func(t *testing.T) {
			db := openUsageEventTestDatabase(t, "sqlite")
			previousLogDB, previousType := LOG_DB, common.LogDatabaseType()
			LOG_DB = db
			common.SetLogDatabaseType(common.DatabaseTypeSQLite)
			initCol()
			t.Cleanup(func() {
				LOG_DB = previousLogDB
				common.SetLogDatabaseType(previousType)
				initCol()
			})
			require.NoError(t, MigrateUsageStatistics(db))
			require.NoError(t, db.AutoMigrate(&Log{}, &QuotaData{}))
			prepareUsageBackfillFixture(t, db)
			input := usageEventTestInput(77)
			input.Identity, input.CollectionEpoch = UsageEventIdentity{}, ""
			input.UsageSemantic = semantic.value
			report, err := NewUsageSourceReport(t.Context(), UsageEventSourceRelay, UsageEventKindConsume, input)
			require.NoError(t, err)
			input = report.Input
			require.NoError(t, PrepareUsageSourceReport(t.Context(), report))
			require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceReady, ""))
			baseline, err := InspectUsageLegacyQuotaData(t.Context())
			require.NoError(t, err)
			lineage := transitionTestLineage(baseline)
			importTransitionTestLogs(t, db, lineage, input.OccurredAt, input.OccurredAt)
			require.NoError(t, ActivateUsageStatisticsLineage(t.Context(), lineage, baseline))
			var before UsageHour
			require.NoError(t, db.Take(&before).Error)

			conflictingLabel := "openai"
			if semantic.value == UsageSemanticOpenAI {
				conflictingLabel = "anthropic"
			}
			for _, tc := range []struct {
				name       string
				cacheRead  any
				cacheWrite any
				label      any
				omit       bool
				retain     bool
			}{
				{"matching_label", input.CacheReadTokens, input.CacheWriteTokens, semantic.label, false, semantic.value == UsageSemanticOtherExplicit},
				{"cache_read_conflict", input.CacheReadTokens + 1, input.CacheWriteTokens, semantic.label, false, true},
				{"cache_write_conflict", input.CacheReadTokens, input.CacheWriteTokens + 1, semantic.label, false, true},
				{"semantic_conflict", input.CacheReadTokens, input.CacheWriteTokens, conflictingLabel, false, true},
				{"semantic_numeric_enum_is_not_a_label", input.CacheReadTokens, input.CacheWriteTokens, input.UsageSemantic, false, true},
				{"optional_duplicates_absent", nil, nil, nil, true, false},
				{"optional_duplicates_null", nil, nil, nil, false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					log := &Log{
						UserId: int(input.UserID), Username: input.Username, CreatedAt: input.OccurredAt, Type: LogTypeConsume,
						TokenName: input.TokenName, ModelName: input.ModelName, PromptTokens: int(input.PromptTokens),
						CompletionTokens: int(input.CompletionTokens), Quota: int(input.QuotaMagnitude), ChannelId: int(input.ChannelID),
						TokenId: int(input.TokenID), Group: input.GroupName, RequestId: input.RequestID, UpstreamRequestId: input.UpstreamRequestID,
					}
					other := NewLogOther()
					if !tc.omit {
						other.SetPublic("cache_tokens", tc.cacheRead)
						other.SetPublic("cache_write_tokens", tc.cacheWrite)
						other.SetPublic("usage_semantic", tc.label)
					}
					other.SetAdmin("node_name", input.WriterNode)
					other.SetAdmin("submit_node_name", input.ExportNode)
					bound, err := bindUsageSourceReport(log, other, report)
					require.NoError(t, err)
					log.Other = bound.JSONString()
					require.NoError(t, db.Create(log).Error)
					deleted, err := DeleteOldLogBatch(t.Context(), input.OccurredAt+1, 10)
					if tc.retain {
						require.ErrorIs(t, err, ErrUsageStatisticsNotReady)
						assert.Zero(t, deleted)
						var count int64
						require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Count(&count).Error)
						assert.EqualValues(t, 1, count, "unproven display metadata must retain the source row")
						// Removing optional display copies does not remove the
						// mandatory exact typed facts emitted by the binding.
						bound, err = bindUsageSourceReport(log, NewLogOther(), report)
						require.NoError(t, err)
						require.NoError(t, db.Model(&Log{}).Where("id = ?", log.Id).Update("other", bound.JSONString()).Error)
						deleted, err = DeleteOldLogBatch(t.Context(), input.OccurredAt+1, 10)
					}
					require.NoError(t, err)
					assert.EqualValues(t, 1, deleted)
				})
			}
			var eventsAfter int64
			require.NoError(t, db.Model(&UsageEvent{}).Count(&eventsAfter).Error)
			assert.EqualValues(t, 1, eventsAfter)
			var after UsageHour
			require.NoError(t, db.Take(&after).Error)
			assert.Equal(t, before, after, "deleting optional sink rows must not mutate durable usage")
		})
	}
}
