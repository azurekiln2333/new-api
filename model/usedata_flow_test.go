package model

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedFlowQuotaData(t *testing.T, quotaData QuotaData) {
	t.Helper()
	require.NoError(t, DB.Create(&quotaData).Error)
}

func seedFlowLookupData(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "west"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, DB.Delete(&Token{Id: 11}).Error)
}

func TestGetFlowQuotaDataUsesQuotaDataRoleSpecificDimensions(t *testing.T) {
	truncateTables(t)
	seedFlowLookupData(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1000,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1100,
		Count:     1,
		Quota:     50,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 2,
		CreatedAt: 1200,
		Count:     1,
		Quota:     25,
		TokenUsed: 10,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "default",
		ModelName: "gpt-b",
		ChannelID: 1,
		CreatedAt: 1300,
		Count:     3,
		Quota:     70,
		TokenUsed: 30,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "legacy",
		CreatedAt: 1400,
		Count:     99,
		Quota:     999,
		TokenUsed: 999,
	})
	require.NoError(t, MigrateUsageStatistics(DB))
	var legacyMaxID int64
	require.NoError(t, DB.Model(&QuotaData{}).Select("COALESCE(MAX(id), 0)").Scan(&legacyMaxID).Error)
	installUsageReaderTestCoverage(t, DB, legacyMaxID, 900, 2000)

	rootRows, err := GetFlowQuotaData(900, 2000, "", 0, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rootRows, 3)
	// Token 11 was soft-deleted, so its name is intentionally left empty for the
	// frontend to render a localized "deleted (id)" label instead.
	require.Equal(t, FlowQuotaData{
		UserID:      1,
		Username:    "alice",
		NodeName:    "node-a",
		TokenID:     11,
		TokenName:   "",
		UseGroup:    "vip",
		ChannelID:   1,
		ChannelName: "east",
		ModelName:   "gpt-a",
		TokenUsed:   60,
		Count:       3,
		Quota:       150,
	}, *rootRows[0])
	// A token that still exists resolves to its current name.
	require.Equal(t, 22, rootRows[1].TokenID)
	require.Equal(t, "backup", rootRows[1].TokenName)

	adminRows, err := GetFlowQuotaData(900, 2000, "alice", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, adminRows, 2)
	require.Equal(t, 0, adminRows[0].TokenID)
	require.Empty(t, adminRows[0].TokenName)
	require.Empty(t, adminRows[0].NodeName)
	require.Equal(t, "alice", adminRows[0].Username)
	require.Equal(t, "vip", adminRows[0].UseGroup)
	require.Equal(t, "east", adminRows[0].ChannelName)
	require.Equal(t, 150, adminRows[0].Quota)

	selfRows, err := GetFlowQuotaData(900, 2000, "", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Empty(t, selfRows[0].Username)
	require.Equal(t, 0, selfRows[0].ChannelID)
	require.Empty(t, selfRows[0].ChannelName)
	require.Empty(t, selfRows[0].TokenName)
	require.Equal(t, "vip", selfRows[0].UseGroup)
	require.Equal(t, 175, selfRows[0].Quota)
}

func TestUsageStatisticsReadersDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openUsageEventTestDatabase(t, dialect)
			logDB := withOpenTestLogDatabase(t, "sqlite", "")
			require.NoError(t, db.AutoMigrate(&QuotaData{}, &Ability{}, &Channel{}, &Token{}))
			require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&QuotaData{}).Error)
			t.Cleanup(func() {
				require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&QuotaData{}).Error)
			})
			ctx := context.Background()
			const hour int64 = 1699999200
			baseline := QuotaData{UserID: 91230, Username: "old-name", ModelName: "reader-model", CreatedAt: hour,
				UseGroup: "group-甲", TokenID: 91231, ChannelID: 9420, NodeName: "ingress", Count: 2, Quota: 50,
				TokenUsed: 100, CacheReadTokens: 10, CacheCreationTokens: 2}
			require.NoError(t, db.Create(&baseline).Error)
			installUsageReaderTestCoverage(t, db, int64(baseline.Id), hour, hour+3599)
			// This post-fence legacy write must never enter the frozen baseline.
			lateLegacy := baseline
			lateLegacy.Id, lateLegacy.TokenUsed = 0, 9999
			require.NoError(t, db.Create(&lateLegacy).Error)
			var receipts []UsageEventReceipt
			for i := range 2 {
				input := usageEventTestInput(int64(i + 1))
				input.OccurredAt, input.ChannelID, input.ModelName = hour+100+int64(i), 9420, "reader-model"
				input.RequestID, input.UpstreamRequestID = "read-50%_request", "upstream-read"
				if i == 0 {
					input.UsageSemantic = UsageSemanticAnthropic
				} else {
					input.UserID, input.Username = 91234, "second-owner"
				}
				receipt, err := PrepareUsageEvent(ctx, input, UsageEventSourceReady)
				require.NoError(t, err)
				receipts = append(receipts, receipt)
				if i == 0 {
					require.NoError(t, ApplyUsageEvent(ctx, receipt))
				}
				require.NoError(t, logDB.Create(&Log{ChannelId: 9420, UserId: int(input.UserID), Type: LogTypeConsume, CreatedAt: input.OccurredAt, ModelName: input.ModelName, PromptTokens: 100, CompletionTokens: 20, Quota: 7}).Error)
			}
			// Historical imports contribute channel/fact dimensions, never a
			// second copy of the overlapping frozen dashboard baseline.
			historical := usageEventTestInput(3)
			historical.Identity = UsageEventIdentity{SourceNamespace: strings.Repeat("c", 32), SourceKind: UsageEventSourceLegacyRelational, SourceRowID: 1}
			historical.CollectionEpoch, historical.Eligibility = strings.Repeat("d", 32), UsageEventProjectChannel
			historical.OccurredAt, historical.ModelName, historical.RequestID = hour+50, "reader-model", "historical-only"
			historicalReceipt, err := PrepareUsageEvent(ctx, historical, UsageEventSourceReady)
			require.NoError(t, err)
			require.NoError(t, ApplyUsageEvent(ctx, historicalReceipt))

			dashboard, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			require.Len(t, dashboard, 1)
			assert.Equal(t, 375, dashboard[0].TokenUsed)
			assert.Equal(t, 4, dashboard[0].Count)
			assert.Equal(t, 64, dashboard[0].Quota)
			assert.Equal(t, int64(70), dashboard[0].CacheReadTokens)
			assert.Equal(t, int64(12), dashboard[0].CacheCreationTokens)
			// An applied hour from another source namespace in the same epoch is
			// outside the installed live lineage and must not enter the dashboard.
			foreign := usageEventTestInput(5)
			foreign.Identity.SourceNamespace = strings.Repeat("9", 32)
			foreign.OccurredAt, foreign.ModelName = hour+200, "reader-model"
			foreignReceipt, err := PrepareUsageEvent(ctx, foreign, UsageEventSourceReady)
			require.NoError(t, err)
			require.NoError(t, ApplyUsageEvent(ctx, foreignReceipt))
			filteredDashboard, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			assert.Equal(t, dashboard, filteredDashboard)
			rankings, err := GetRankingQuotaTotals(hour, hour+3599)
			require.NoError(t, err)
			require.Equal(t, []RankingQuotaTotal{{ModelName: "reader-model", TotalTokens: 375, CacheReadTokens: 70, CacheCreationTokens: 12}}, rankings)
			buckets, err := GetRankingQuotaBuckets(hour, hour+3599, 3600)
			require.NoError(t, err)
			assert.Equal(t, []RankingQuotaBucket{{ModelName: "reader-model", Bucket: hour, Tokens: 375}}, buckets)
			users, err := GetModelUserUsageRanking(ctx, "reader-model", hour, hour+3599, 1)
			require.NoError(t, err)
			assert.Equal(t, []UserRankingRow{{UserID: 91230, Requests: 3, Tokens: 255, Quota: 57}}, users)
			totals, err := GetModelUsageTotals(ctx, "reader-model", hour, hour+3599)
			require.NoError(t, err)
			assert.Equal(t, ModelUsageTotals{Requests: 4, Tokens: 375, Quota: 64}, totals)
			self, err := GetQuotaDataByUserId(91230, hour, hour+3599)
			require.NoError(t, err)
			assert.Len(t, self, 2, "historical and live usernames retain their own hour dimensions")
			rootFlow, err := GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleRootUser)
			require.NoError(t, err)
			adminFlow, err := GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleAdminUser)
			require.NoError(t, err)
			for _, row := range adminFlow {
				assert.Empty(t, row.NodeName)
				assert.Zero(t, row.TokenID)
			}
			selfFlow, err := GetFlowQuotaData(hour, hour+3599, "second-owner", 91230, common.RoleCommonUser)
			require.NoError(t, err)
			require.Len(t, selfFlow, 1)
			assert.Equal(t, 255, selfFlow[0].TokenUsed)
			assert.Empty(t, selfFlow[0].Username)
			assert.Empty(t, selfFlow[0].NodeName)
			assert.Zero(t, selfFlow[0].ChannelID)

			filter := LogFilter{StartTimestamp: hour + 100, EndTimestamp: hour + 101, RequestId: "50%_", MatchMode: LogMatchFuzzy}
			stat, err := SumUsageEventQuota(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, Stat{Quota: 14, CacheReadTokens: 60, InputTokens: 235}, stat)
			tokens, err := SumUsageEventTokens(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, 240, tokens)
			for _, tc := range []struct {
				scope LogUserScope
				quota int
				input int64
			}{
				{LogUserScopeSelf, 7, 135}, {LogUserScopeOthers, 7, 100}, {LogUserScopeAll, 14, 235},
			} {
				scoped := filter
				scoped.UserScope, scoped.ScopeUserId = tc.scope, 91230
				result, err := SumUsageEventQuota(ctx, scoped)
				require.NoError(t, err)
				assert.Equal(t, tc.quota, result.Quota)
				assert.Equal(t, tc.input, result.InputTokens)
			}
			boundary := filter
			boundary.EndTimestamp = hour + 100
			boundary.MatchMode, boundary.RequestId = LogMatchExact, "read-50%_request"
			result, err := SumUsageEventQuota(ctx, boundary)
			require.NoError(t, err)
			assert.Equal(t, 7, result.Quota, "inclusive exact-second boundary retains one event")
			boundary.UserScope = "invalid"
			_, err = SumUsageEventQuota(ctx, boundary)
			assert.ErrorIs(t, err, errInvalidLogUserScope)

			require.NoError(t, logDB.Where("channel_id = ?", 9420).Delete(&Log{}).Error)
			var retained int64
			require.NoError(t, logDB.Model(&Log{}).Where("channel_id = ?", 9420).Count(&retained).Error)
			assert.Zero(t, retained)
			for _, receipt := range receipts {
				require.NoError(t, ApplyUsageEvent(ctx, receipt))
				require.NoError(t, ApplyUsageEvent(ctx, receipt))
			}
			afterDashboard, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			assert.Equal(t, dashboard, afterDashboard)
			afterRankings, err := GetRankingQuotaTotals(hour, hour+3599)
			require.NoError(t, err)
			assert.Equal(t, rankings, afterRankings)
			afterFlow, err := GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleRootUser)
			require.NoError(t, err)
			assert.ElementsMatch(t, rootFlow, afterFlow)
			afterStat, err := SumUsageEventQuota(ctx, filter)
			require.NoError(t, err)
			assert.Equal(t, stat, afterStat)
			partial, err := GetAllQuotaDates(hour-1, hour+3599, "")
			require.NoError(t, err)
			assert.Equal(t, dashboard, partial, "a missing historical certificate does not hide proven durable contributions")
			coverage, err := GetUsageStatisticsCoverage(ctx, UsageStatisticsDashboard, hour-1, hour+3599)
			require.NoError(t, err)
			assert.False(t, coverage.Complete, "a verified interval does not certify omitted history")
			unknown := usageEventTestInput(4)
			unknown.OccurredAt, unknown.ModelName = hour+102, "unknown-semantics"
			unknown.UserID, unknown.Username = 91235, "unknown-owner"
			unknown.UsageSemantic = UsageSemanticUnknown
			unknown.CacheReadTokens = 0
			unknown.KnownMask &^= UsageEventKnownUsageSemantic | UsageEventKnownCacheReadTokens
			unknownReceipt, err := PrepareUsageEvent(ctx, unknown, UsageEventSourceReady)
			require.NoError(t, err)
			for _, applied := range []bool{false, true} {
				if applied {
					require.NoError(t, ApplyUsageEvent(ctx, unknownReceipt))
				}
				unaffectedSelf, err := GetQuotaDataByUserId(91230, hour, hour+3599)
				require.NoError(t, err)
				assert.ElementsMatch(t, self, unaffectedSelf)
				unaffectedName, err := GetQuotaDataByUsername("old-name", hour, hour+3599)
				require.NoError(t, err)
				require.Len(t, unaffectedName, 1)
				assert.Equal(t, 100, unaffectedName[0].TokenUsed)
				unaffectedModel, err := GetModelUsageTotals(ctx, "reader-model", hour, hour+3599)
				require.NoError(t, err)
				assert.Equal(t, totals, unaffectedModel)
				_, err = GetQuotaDataByUserId(91235, hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				_, err = GetQuotaDataByUsername("unknown-owner", hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				_, err = GetModelUsageTotals(ctx, "unknown-semantics", hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				_, err = GetAllQuotaDates(hour, hour+3599, "")
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady, "unknown token semantics are not reported as zero")
			}
			unknownFilter := LogFilter{StartTimestamp: hour + 102, EndTimestamp: hour + 102}
			_, err = SumUsageEventQuota(ctx, unknownFilter)
			assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
			raw, err := SumUsageEventTokens(ctx, unknownFilter)
			require.NoError(t, err, "raw prompt/completion remain independently known")
			assert.Equal(t, 120, raw)
			now := common.GetTimestamp()
			lineage, err := GetUsageReadLineage(ctx)
			require.NoError(t, err)
			require.NoError(t, AttestUsageStatisticsCoverage(ctx, UsageStatisticsCoverage{
				Family: UsageStatisticsFacts, StartAt: now - 120, EndAt: now, LineageDigest: lineage.LineageDigest,
				ManifestDigest: strings.Repeat("e", 64), SourceSeal: strings.Repeat("f", 64),
			}))
			for i, timestamp := range []int64{now - 90, now - 10} {
				input := usageEventTestInput(int64(i + 20))
				input.OccurredAt, input.RequestID = timestamp, "recent-window"
				_, err := PrepareUsageEvent(ctx, input, UsageEventSourceReady)
				require.NoError(t, err)
			}
			recent, err := SumUsageEventQuota(ctx, LogFilter{StartTimestamp: now - 120, EndTimestamp: now, RequestId: "recent-window", MatchMode: LogMatchExact})
			require.NoError(t, err)
			assert.Equal(t, 14, recent.Quota)
			assert.Equal(t, 1, recent.Rpm)
			assert.Equal(t, 120, recent.Tpm)
			past, err := SumUsageEventQuota(ctx, LogFilter{StartTimestamp: now - 120, EndTimestamp: now - 70, RequestId: "recent-window", MatchMode: LogMatchExact})
			require.NoError(t, err)
			assert.Equal(t, 7, past.Quota)
			assert.Zero(t, past.Rpm)
			assert.Zero(t, past.Tpm)
			clearUsageEventTestRows(t, db)
			unattributed := usageEventTestInput(6)
			unattributed.OccurredAt = hour+100
			unattributed.UserID, unattributed.Username, unattributed.ModelName = 0, "", ""
			unattributed.KnownMask &^= UsageEventKnownUserID | UsageEventKnownUsername | UsageEventKnownModelName
			unattributedReceipt, err := PrepareUsageEvent(ctx, unattributed, UsageEventSourceReady)
			require.NoError(t, err)
			for _, applied := range []bool{false, true} {
				if applied {
					require.NoError(t, ApplyUsageEvent(ctx, unattributedReceipt))
				}
				_, err = GetQuotaDataByUserId(91230, hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady, "unknown ownership cannot certify a self subtotal")
				_, err = GetQuotaDataByUsername("old-name", hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
				_, err = GetModelUsageTotals(ctx, "reader-model", hour, hour+3599)
				assert.ErrorIs(t, err, ErrUsageStatisticsNotReady)
			}
			clearUsageEventTestRows(t, db)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				for i := range usageReadPendingLimit+1 {
					input := usageEventTestInput(int64(i+1))
					input.OccurredAt = hour+100
					if _, err := PrepareUsageEventTx(tx, input, UsageEventSourceReady); err != nil {
						return err
					}
				}
				return nil
			}))
			_, err = GetAllQuotaDates(hour, hour+3599, "")
			assert.ErrorIs(t, err, ErrUsageStatisticsNotReady, "dashboard does not publish a truncated pending tail")
		})
	}
}

func TestUsageStatisticsTransitionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openUsageEventTestDatabase(t, dialect)
			logs := withOpenTestLogDatabase(t, "sqlite", "")
			require.NoError(t, db.AutoMigrate(&QuotaData{}, &Ability{}, &Channel{}, &Token{}))
			prepareUsageBackfillFixture(t, db)
			require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&QuotaData{}).Error)
			for _, table := range []any{&UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}} {
				require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
			}
			oldLogs, oldExport := common.LogConsumeEnabled, common.DataExportEnabled
			oldCache, oldPending := CacheQuotaData, pendingUsageLegacyReports
			common.LogConsumeEnabled, common.DataExportEnabled = true, true
			CacheQuotaData, pendingUsageLegacyReports = make(map[string]*QuotaData), make(map[string]UsageSourceReport)
			t.Cleanup(func() {
				common.LogConsumeEnabled, common.DataExportEnabled = oldLogs, oldExport
				CacheQuotaData, pendingUsageLegacyReports = oldCache, oldPending
				for _, table := range []any{&UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}, &QuotaData{}} {
					require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
				}
			})
			t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "false")
			const hour int64 = 1699999200
			// Aggregate-only history predates all numeric facts and must survive.
			require.NoError(t, db.Create(&QuotaData{UserID: 91230, Username: "usage-owner", ModelName: "transition-model", CreatedAt: hour,
				UseGroup: "group-甲", TokenID: 91231, ChannelID: 9420, NodeName: common.NodeName, Count: 2, Quota: 50, TokenUsed: 100}).Error)
			ctx := t.Context()
			report := newTransitionUsageReport(t, hour+100)
			writeTransitionConsumeLog(t, report)
			require.NoError(t, mirrorUsageLegacyReport(ctx, report), "replay cannot add the same legacy occurrence twice")
			var acknowledgements int64
			require.NoError(t, db.Model(&UsageLegacyAcknowledgement{}).Count(&acknowledgements).Error)
			assert.EqualValues(t, 1, acknowledgements)
			legacy, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			require.Len(t, legacy, 1)
			assert.Equal(t, 220, legacy[0].TokenUsed)
			assert.Equal(t, 3, legacy[0].Count)
			beforeChannels, err := GetChannelUsages([]int{9420})
			require.NoError(t, err)
			require.Len(t, beforeChannels, 1)
			_, err = DeleteOldLogBatch(ctx, hour+3599, 100)
			require.ErrorIs(t, err, ErrUsageStatisticsNotReady, "active log-backed readers fence even exact applied rows")

			// A failed ownership write cannot leave an ack or aggregate increment.
			failed := newTransitionUsageReport(t, hour+101)
			const callback = "transition:reject-legacy-ack"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "usage_legacy_acknowledgements" {
					tx.AddError(errors.New("ownership acknowledgement rejected"))
				}
			}))
			writeTransitionConsumeLog(t, failed)
			require.NoError(t, db.Callback().Create().Remove(callback))
			t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
			afterFailedMirror, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			assert.Equal(t, legacy, afterFailedMirror, "a rejected ack rolls back its actual legacy increment")
			baseline, err := InspectUsageLegacyQuotaData(ctx)
			require.NoError(t, err)
			lineage := transitionTestLineage(baseline)
			require.ErrorIs(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline), ErrUsageStatisticsNotReady, "pending keyed ownership must be drained")
			SaveQuotaDataCache()
			require.Empty(t, pendingUsageLegacyReports)
			require.NoError(t, db.Model(&UsageLegacyAcknowledgement{}).Count(&acknowledgements).Error)
			assert.EqualValues(t, 2, acknowledgements)

			// Off-switch and ready-but-unapplied facts are never baseline-owned.
			common.LogConsumeEnabled, common.DataExportEnabled = false, false
			offSwitch := newTransitionUsageReport(t, hour+102)
			writeTransitionConsumeLog(t, offSwitch)
			pending := newTransitionUsageReport(t, hour+103)
			require.NoError(t, ConfirmUsageEvent(ctx, pending.Receipt, UsageEventOutcome{State: UsageEventSourceReady, EvidenceRef: "transition-report"}))
			delayed := newTransitionUsageReport(t, hour+104)
			delayedIdentity := delayed.Input
			common.LogConsumeEnabled, common.DataExportEnabled = true, true
			baseline, err = InspectUsageLegacyQuotaData(ctx)
			require.NoError(t, err)
			lineage = transitionTestLineage(baseline)
			require.ErrorIs(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline), ErrUsageStatisticsNotReady, "unimported retained history cannot be hidden by cutover")
			importTransitionTestLogs(t, logs, lineage, hour, hour+3599)
			beforeChannels, err = GetChannelUsages([]int{9420})
			require.NoError(t, err)
			require.Len(t, beforeChannels, 1)
			require.NoError(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline))
			require.NoError(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline), "activation replay is idempotent")
			current, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			require.Len(t, current, 1)
			assert.Equal(t, 580, current[0].TokenUsed, "aggregate-only100 + four distinct live120, with no double-owned copy")
			assert.Equal(t, 6, current[0].Count)
			assert.Equal(t, 78, current[0].Quota)
			post := newTransitionUsageReport(t, hour+105)
			writeTransitionConsumeLog(t, post)
			require.NoError(t, ConfirmUsageSourceReport(ctx, delayed, UsageEventSourceReady, "transition-report"))
			writeTransitionConsumeLog(t, delayed)
			assert.Equal(t, delayedIdentity, delayed.Input, "a pending object never changes its frozen payload at activation")
			require.NoError(t, mirrorUsageLegacyReport(ctx, report), "late duplicate cannot mutate the baseline")
			observed, err := InspectUsageLegacyQuotaData(ctx)
			require.NoError(t, err)
			assert.Equal(t, baseline, observed)
			LogQuotaData(QuotaDataLogParams{UserID: 1, ModelName: "late-old-writer", CreatedAt: hour, Quota: 99})
			assert.Empty(t, CacheQuotaData, "a delayed anonymous writer cannot enqueue after activation")
			current, err = GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			require.Len(t, current, 1)
			assert.Equal(t, 820, current[0].TokenUsed)
			assert.Equal(t, 8, current[0].Count)
			assert.Equal(t, 92, current[0].Quota)
			rankings, err := GetRankingQuotaTotals(hour, hour+3599)
			require.NoError(t, err)
			require.Len(t, rankings, 1)
			assert.EqualValues(t, 820, rankings[0].TotalTokens)
			flow, err := GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleRootUser)
			require.NoError(t, err)
			require.Len(t, flow, 1)
			assert.Equal(t, 820, flow[0].TokenUsed)
			stat, err := SumUsedQuota(LogFilter{Channel: 9420, StartTimestamp: hour, EndTimestamp: hour+3599})
			require.NoError(t, err)
			assert.Equal(t, 42, stat.Quota)
			channels, err := GetChannelUsages([]int{9420})
			require.NoError(t, err)
			require.Len(t, channels, 1)
			assert.EqualValues(t, beforeChannels[0].RequestCount+4, channels[0].RequestCount)
			deleted, err := DeleteOldLogBatch(ctx, hour+3599, 100)
			require.NoError(t, err)
			assert.EqualValues(t, 4, deleted)
			var retained int64
			require.NoError(t, logs.Model(&Log{}).Count(&retained).Error)
			assert.Zero(t, retained)
			_, err = RecoverUsageEventProjections(ctx, 100)
			require.NoError(t, err)
			after, err := GetAllQuotaDates(hour, hour+3599, "")
			require.NoError(t, err)
			assert.Equal(t, current, after)
			afterChannels, err := GetChannelUsages([]int{9420})
			require.NoError(t, err)
			assert.Equal(t, channels, afterChannels)
			afterStat, err := SumUsedQuota(LogFilter{Channel: 9420, StartTimestamp: hour, EndTimestamp: hour+3599})
			require.NoError(t, err)
			assert.Equal(t, stat, afterStat)
			afterRankings, err := GetRankingQuotaTotals(hour, hour+3599)
			require.NoError(t, err)
			assert.Equal(t, rankings, afterRankings)
			afterFlow, err := GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleRootUser)
			require.NoError(t, err)
			assert.Equal(t, flow, afterFlow)
			coverage, err := GetUsageStatisticsCoverage(ctx, UsageStatisticsDashboard, hour, hour+3599)
			require.NoError(t, err)
			assert.False(t, coverage.Complete, "retained import and ownership never invent all-history completeness")

			t.Run("global_activation_requires_all_retained_numeric_logs", func(t *testing.T) {
				for _, tc := range []struct {
					name            string
					at              int64
					emptyCheckpoint bool
					mutatePayload   bool
				}{
					{name: "before_verified_range", at: 900},
					{name: "after_verified_range", at: 2100},
					{name: "after_verified_high_watermark", at: 1500, emptyCheckpoint: true},
					{name: "changed_retained_payload", at: 1500, mutatePayload: true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						clearTransitionTestHistory(t, db, logs)
						common.LogConsumeEnabled, common.DataExportEnabled = true, true
						lineage := transitionTestLineage(UsageLegacyQuotaDataBaseline{})
						if tc.emptyCheckpoint {
							importTransitionTestLogs(t, logs, lineage, 1000, 2000)
						}
						report := newTransitionUsageReport(t, tc.at)
						writeTransitionConsumeLog(t, report)
						if !tc.emptyCheckpoint {
							importTransitionTestLogs(t, logs, lineage, 1000, 2000)
						}
						if tc.mutatePayload {
							require.NoError(t, logs.Model(&Log{}).Where("channel_id = ?", 9420).Update("quota", 8).Error)
						}
						baseline, err := InspectUsageLegacyQuotaData(ctx)
						require.NoError(t, err)
						lineage = transitionTestLineage(baseline)
						require.ErrorIs(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline), ErrUsageStatisticsNotReady)
						installed, err := UsageReadLineageInstalled(ctx)
						require.NoError(t, err)
						assert.False(t, installed, "a bounded import cannot silently replace lifetime history")
						_, err = DeleteOldLogBatch(ctx, 3000, 100)
						require.ErrorIs(t, err, ErrUsageStatisticsNotReady)
						var retained int64
						require.NoError(t, logs.Model(&Log{}).Count(&retained).Error)
						assert.EqualValues(t, 1, retained)
						observed, err := InspectUsageLegacyQuotaData(ctx)
						require.NoError(t, err)
						assert.Equal(t, baseline, observed, "failed cutover preserves aggregate-only history")
					})
				}
			})

			t.Run("legacy_read_activation_cleanup_barriers", func(t *testing.T) {
				for _, tc := range []struct {
					name         string
					logQuery     int64
					ability      bool
					dashboard    bool
					dashboardRow bool
					read         func() (any, error)
				}{
					{name: "channel_summary", logQuery: 1, read: func() (any, error) { return GetChannelUsages([]int{9420}) }},
					{name: "channel_detail_before_usage", logQuery: 1, read: func() (any, error) { return GetChannelUsageDetail(9420, []string{"unused"}) }},
					{name: "channel_detail_after_usage", ability: true, read: func() (any, error) { return GetChannelUsageDetail(9420, []string{"unused"}) }},
					{name: "quota_before_usage", logQuery: 1, read: func() (any, error) { return SumUsedQuota(LogFilter{Channel: 9420}) }},
					{name: "quota_after_quota", logQuery: 2, read: func() (any, error) { return SumUsedQuota(LogFilter{Channel: 9420}) }},
					{name: "quota_after_rates", logQuery: 3, read: func() (any, error) { return SumUsedQuota(LogFilter{Channel: 9420}) }},
					{name: "dashboard_all", dashboard: true, read: func() (any, error) { return GetAllQuotaDates(hour, hour+3599, "") }},
					{name: "dashboard_username", dashboard: true, read: func() (any, error) { return GetQuotaDataByUsername("usage-owner", hour, hour+3599) }},
					{name: "dashboard_self", dashboard: true, read: func() (any, error) { return GetQuotaDataByUserId(91230, hour, hour+3599) }},
					{name: "dashboard_users", dashboard: true, read: func() (any, error) { return GetQuotaDataGroupByUser(hour, hour+3599) }},
					{name: "rankings", dashboard: true, dashboardRow: true, read: func() (any, error) { return GetRankingQuotaTotals(hour, hour+3599) }},
					{name: "model_totals", dashboard: true, dashboardRow: true, read: func() (any, error) { return GetModelUsageTotals(ctx, "transition-model", hour, hour+3599) }},
					{name: "flow", dashboard: true, dashboardRow: true, read: func() (any, error) { return GetFlowQuotaData(hour, hour+3599, "", 0, common.RoleAdminUser) }},
				} {
					t.Run(tc.name, func(t *testing.T) {
						clearTransitionTestHistory(t, db, logs)
						common.LogConsumeEnabled, common.DataExportEnabled = true, true
						at := common.GetTimestamp()
						if tc.dashboard {
							at = hour+100
						}
						report := newTransitionUsageReport(t, at)
						writeTransitionConsumeLog(t, report)
						baseline, err := InspectUsageLegacyQuotaData(ctx)
						require.NoError(t, err)
						lineage := transitionTestLineage(baseline)
						importTransitionTestLogs(t, logs, lineage, report.Input.OccurredAt, report.Input.OccurredAt)
						want, err := tc.read()
						require.NoError(t, err)
						if tc.dashboard {
							// The old dashboard intentionally misses this off-switch
							// fact. A cutover-racing reader must retry to include it.
							common.LogConsumeEnabled, common.DataExportEnabled = false, false
							writeTransitionConsumeLog(t, newTransitionUsageReport(t, at+1))
							common.LogConsumeEnabled, common.DataExportEnabled = true, true
						}
						barrierCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
						t.Cleanup(cancel)
						entered, resume := make(chan struct{}), make(chan struct{})
						var queries atomic.Int64
						var blocked atomic.Bool
						barrier := func(tx *gorm.DB) {
							if tc.ability {
								if tx.Statement.Table != "abilities" {
									return
								}
							} else if tc.dashboard {
								if tx.Statement.Table != "quota_data" {
									return
								}
							} else if tx.Statement.Table != "logs" || queries.Add(1) != tc.logQuery {
								return
							}
							if !blocked.CompareAndSwap(false, true) {
								return
							}
							close(entered)
							select {
							case <-resume:
							case <-barrierCtx.Done():
								tx.AddError(barrierCtx.Err())
							}
						}
						const callback = "transition:legacy-reader-barrier"
						barrierDB := logs
						if tc.ability || tc.dashboard {
							barrierDB = db
						}
						if tc.ability || tc.dashboard && !tc.dashboardRow {
							require.NoError(t, barrierDB.Callback().Query().Before("gorm:query").Register(callback, barrier))
							t.Cleanup(func() { _ = barrierDB.Callback().Query().Remove(callback) })
						} else {
							require.NoError(t, barrierDB.Callback().Row().Before("gorm:row").Register(callback, barrier))
							t.Cleanup(func() { _ = barrierDB.Callback().Row().Remove(callback) })
						}
						results := make(chan struct {
							value any
							err   error
						}, 1)
						var readers sync.WaitGroup
						var release sync.Once
						readers.Go(func() {
							value, err := tc.read()
							results <- struct {
								value any
								err   error
							}{value, err}
						})
						t.Cleanup(func() {
							release.Do(func() { close(resume) })
							readers.Wait()
						})
						select {
						case <-entered:
						case <-barrierCtx.Done():
							t.Fatal("legacy reader did not reach its SQL barrier")
						}
						require.NoError(t, ActivateUsageStatisticsLineage(barrierCtx, lineage, baseline))
						if tc.dashboard {
							switched, err := tc.read()
							require.NoError(t, err)
							assert.NotEqual(t, want, switched, "durable cutover includes the off-switch fact")
							want = switched
						}
						deleted, err := DeleteOldLogBatch(barrierCtx, report.Input.OccurredAt+1, 100)
						require.NoError(t, err)
						assert.EqualValues(t, 1, deleted)
						require.NoError(t, ActivateUsageStatisticsLineage(barrierCtx, lineage, baseline), "replay remains valid after proven source cleanup")
						release.Do(func() { close(resume) })
						select {
						case result := <-results:
							require.NoError(t, result.err)
							assert.Equal(t, want, result.value, "return the whole durable response, never a legacy/cleaned hybrid")
						case <-barrierCtx.Done():
							t.Fatal("legacy reader did not complete after cutover")
						}
					})
				}
			})

			t.Run("empty_model_detail_equal_before_cutover_and_after_cleanup", func(t *testing.T) {
				clearTransitionTestHistory(t, db, logs)
				common.LogConsumeEnabled, common.DataExportEnabled = true, true
				writeTransitionConsumeLog(t, newTransitionUsageReport(t, hour+100))
				input := usageEventTestInput(1)
				input.Identity, input.CollectionEpoch = UsageEventIdentity{}, ""
				input.OccurredAt, input.ChannelID, input.ModelName = hour+101, 9420, ""
				input.WriterNode, input.ExportNode = common.NodeName, common.NodeName
				blank, err := NewUsageSourceReport(ctx, UsageEventSourceRelay, UsageEventKindConsume, input)
				require.NoError(t, err)
				require.NoError(t, PrepareUsageSourceReport(ctx, blank))
				writeTransitionConsumeLog(t, blank)
				before, err := GetChannelUsages([]int{9420})
				require.NoError(t, err)
				require.Len(t, before, 1)
				assert.EqualValues(t, 2, before[0].RequestCount)
				assert.EqualValues(t, 240, before[0].TotalTokens)
				detail, err := GetChannelUsageDetail(9420, []string{"configured", ""})
				require.NoError(t, err)
				assert.EqualValues(t, 1, detail.RequestCount)
				assert.EqualValues(t, 120, detail.TotalTokens)
				require.Len(t, detail.Models, 2)
				baseline, err := InspectUsageLegacyQuotaData(ctx)
				require.NoError(t, err)
				lineage := transitionTestLineage(baseline)
				importTransitionTestLogs(t, logs, lineage, hour, hour+3599)
				require.NoError(t, ActivateUsageStatisticsLineage(ctx, lineage, baseline))
				for _, cleanup := range []bool{false, true} {
					if cleanup {
						deleted, err := DeleteOldLogBatch(ctx, hour+3599, 100)
						require.NoError(t, err)
						assert.EqualValues(t, 2, deleted)
					}
					after, err := GetChannelUsages([]int{9420})
					require.NoError(t, err)
					assert.Equal(t, before, after)
					afterDetail, err := GetChannelUsageDetail(9420, []string{"configured", ""})
					require.NoError(t, err)
					assert.Equal(t, detail, afterDetail)
				}
			})
		})
	}
}

func clearTransitionTestHistory(t *testing.T, primary, source *gorm.DB) {
	t.Helper()
	clearUsageEventTestRows(t, primary)
	for _, table := range []any{&UsageEventBackfillTuple{}, &UsageEventBackfill{}, &UsageStatisticsCoverage{}, &UsageStatisticsLineage{},
		&UsageLegacyAcknowledgement{}, &UsageLegacyHour{}, &UsageStatisticsTransition{}, &QuotaData{}} {
		require.NoError(t, primary.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
	}
	require.NoError(t, source.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&Log{}).Error)
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	CacheQuotaData, pendingUsageLegacyReports = make(map[string]*QuotaData), make(map[string]UsageSourceReport)
}

func TestUsageStatisticsTransitionRejectsAmbiguousHistoryAndBuffers(t *testing.T) {
	db := openUsageEventTestDatabase(t, "sqlite")
	logs := withOpenTestLogDatabase(t, "sqlite", "")
	require.NoError(t, db.AutoMigrate(&QuotaData{}))
	prepareUsageBackfillFixture(t, db)
	oldCache, oldPending := CacheQuotaData, pendingUsageLegacyReports
	CacheQuotaData, pendingUsageLegacyReports = make(map[string]*QuotaData), make(map[string]UsageSourceReport)
	t.Cleanup(func() { CacheQuotaData, pendingUsageLegacyReports = oldCache, oldPending })
	baseline, err := InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	lineage := transitionTestLineage(baseline)
	importTransitionTestLogs(t, logs, lineage, 1000, 2000)
	CacheQuotaData["old-node-buffer"] = &QuotaData{UserID: 1, ModelName: "old-model", CreatedAt: 0, Count: 1, Quota: 9}
	require.ErrorIs(t, ActivateUsageStatisticsLineage(t.Context(), lineage, baseline), ErrUsageStatisticsNotReady)
	SaveQuotaDataCache()
	require.Empty(t, CacheQuotaData)
	baseline, err = InspectUsageLegacyQuotaData(t.Context())
	require.NoError(t, err)
	lineage = transitionTestLineage(baseline)
	ambiguous := usageEventTestInput(1)
	ambiguous.Identity.SourceNamespace, ambiguous.CollectionEpoch = UsageLiveSourceNamespace(), UsageLiveCollectionEpoch()
	_, err = PrepareUsageEvent(t.Context(), ambiguous, UsageEventSourceReady)
	require.NoError(t, err)
	require.ErrorIs(t, ActivateUsageStatisticsLineage(t.Context(), lineage, baseline), ErrUsageStatisticsNotReady, "old codec facts have no occurrence-level overlap proof")
	installed, err := UsageReadLineageInstalled(t.Context())
	require.NoError(t, err)
	assert.False(t, installed)
	var retained int64
	require.NoError(t, db.Model(&UsageEvent{}).Count(&retained).Error)
	assert.EqualValues(t, 1, retained, "ambiguous facts are neither discarded nor relabelled")
}

func newTransitionUsageReport(t *testing.T, at int64) *UsageSourceReport {
	t.Helper()
	input := usageEventTestInput(at)
	input.Identity, input.CollectionEpoch = UsageEventIdentity{}, ""
	input.OccurredAt, input.ChannelID, input.ModelName = at, 9420, "transition-model"
	input.WriterNode, input.ExportNode = common.NodeName, common.NodeName
	report, err := NewUsageSourceReport(t.Context(), UsageEventSourceRelay, UsageEventKindConsume, input)
	require.NoError(t, err)
	require.NoError(t, PrepareUsageSourceReport(t.Context(), report))
	return report
}

func writeTransitionConsumeLog(t *testing.T, report *UsageSourceReport) {
	t.Helper()
	require.NoError(t, ConfirmUsageSourceReport(t.Context(), report, UsageEventSourceReady, "transition-report"))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("username", report.Input.Username)
	other := NewLogOther()
	other.SetPublic("cache_tokens", int(report.Input.CacheReadTokens))
	other.SetPublic("cache_write_tokens", int(report.Input.CacheWriteTokens))
	other.SetPublic("usage_semantic", "openai")
	RecordConsumeLog(c, int(report.Input.UserID), RecordConsumeLogParams{
		UsageReport: report, ChannelId: int(report.Input.ChannelID), TokenId: int(report.Input.TokenID),
		TokenName: report.Input.TokenName, ModelName: report.Input.ModelName, Group: report.Input.GroupName,
		PromptTokens: int(report.Input.PromptTokens), CompletionTokens: int(report.Input.CompletionTokens),
		Quota: int(report.Input.QuotaMagnitude), Other: other,
	})
}

func transitionTestLineage(baseline UsageLegacyQuotaDataBaseline) UsageReadLineage {
	return UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: baseline.MaxID, LegacyBaselineDigest: baseline.Digest,
		FenceDigest: strings.Repeat("e", 64), LegacySourceNamespace: strings.Repeat("c", 32), LegacyCollectionEpoch: strings.Repeat("d", 32),
		LiveSourceNamespace: UsageLiveSourceNamespace(), LiveCollectionEpoch: UsageLiveCollectionEpoch(), DashboardEligibility: UsageEventProjectHours,
	}
}

func importTransitionTestLogs(t *testing.T, source *gorm.DB, lineage UsageReadLineage, start, end int64) {
	t.Helper()
	var high int64
	require.NoError(t, source.Model(&Log{}).Select("COALESCE(MAX(id), 0)").Scan(&high).Error)
	plan := UsageEventBackfillPlan{
		SourceNamespace: lineage.LegacySourceNamespace, SourceKind: UsageEventSourceLegacyRelational,
		CollectionEpoch: lineage.LegacyCollectionEpoch, StartAt: start, EndAt: end, HighWatermark: high, FenceDigest: lineage.FenceDigest,
	}
	cursor := UsageEventBackfillCursor{}
	for {
		page, err := InspectUsageEventBackfillBatch(t.Context(), source, plan, cursor, 100)
		require.NoError(t, err)
		cursor = page.Next
		if page.Exhausted {
			break
		}
	}
	plan.ExpectedRows, plan.ManifestDigest = cursor.Rows, cursor.Digest
	for {
		progress, err := RunUsageEventBackfillBatch(t.Context(), source, plan, 100)
		require.NoError(t, err)
		if progress.RetainedVerified {
			break
		}
	}
}

func TestLogQuotaDataSplitsRowsByUseGroupTokenChannelAndNode(t *testing.T) {
	truncateTables(t)
	require.NoError(t, MigrateUsageStatistics(DB))
	require.NoError(t, MigrateUsageEventBackfill(DB))
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3661,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     100,
		TokenUsed: 40,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     50,
		TokenUsed: 20,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     25,
		TokenUsed: 10,
	})

	SaveQuotaDataCache()

	var rows []QuotaData
	require.NoError(t, DB.Order("quota DESC").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, int64(3600), rows[0].CreatedAt)
	require.Equal(t, "vip", rows[0].UseGroup)
	require.Equal(t, 11, rows[0].TokenID)
	require.Equal(t, 1, rows[0].ChannelID)
	require.Equal(t, "node-a", rows[0].NodeName)
	require.Equal(t, 2, rows[0].Count)
	require.Equal(t, 150, rows[0].Quota)
	require.Equal(t, 60, rows[0].TokenUsed)
	require.Equal(t, "default", rows[1].UseGroup)
	require.Equal(t, 25, rows[1].Quota)
}

func TestSaveQuotaDataCacheRetainsPendingRowsWhenLineageLookupFails(t *testing.T) {
	previousDB := DB
	previousCache := CacheQuotaData
	DB = nil
	CacheQuotaDataLock.Lock()
	CacheQuotaData = map[string]*QuotaData{"pending": {UserID: 7, Username: "alice", ModelName: "m", CreatedAt: 100, Quota: 9, Count: 1}}
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		DB = previousDB
		CacheQuotaDataLock.Lock()
		CacheQuotaData = previousCache
		CacheQuotaDataLock.Unlock()
	})

	SaveQuotaDataCache()

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	require.Len(t, CacheQuotaData, 1)
	require.Equal(t, 9, CacheQuotaData["pending"].Quota)
}
