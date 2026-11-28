package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type flowQuotaResponse struct {
	Success  bool                              `json:"success"`
	Message  string                            `json:"message"`
	Data     []model.FlowQuotaData             `json:"data"`
	Coverage model.UsageStatisticsCoverageView `json:"coverage"`
}

func setupFlowControllerTestDB(t *testing.T) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.QuotaData{}, &model.Log{}))
	require.NoError(t, model.MigrateUsageStatistics(db))
	require.NoError(t, model.MigrateUsageEventBackfill(db))
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, model.DB.Create(&model.Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, model.DB.Create(&model.Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "default",
		ChannelID: 1,
		ModelName: "gpt-a",
		CreatedAt: 1100,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	}).Error)
	require.NoError(t, model.DB.Create(&model.QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "vip",
		ChannelID: 1,
		ModelName: "gpt-b",
		CreatedAt: 1200,
		Count:     1,
		Quota:     70,
		TokenUsed: 30,
	}).Error)
	baseline, err := model.InspectUsageLegacyQuotaData(context.Background())
	require.NoError(t, err)
	lineage := model.UsageReadLineage{
		IncludeLegacyQuotaData: true, LegacyQuotaDataMaxID: baseline.MaxID, LegacyBaselineDigest: baseline.Digest,
		FenceDigest: strings.Repeat("d", 64), LegacySourceNamespace: strings.Repeat("a", 32),
		LegacyCollectionEpoch: strings.Repeat("b", 32), LiveSourceNamespace: model.UsageLiveSourceNamespace(),
		LiveCollectionEpoch: model.UsageLiveCollectionEpoch(), DashboardEligibility: model.UsageEventProjectHours,
	}
	// The fixture owns a genuinely empty retained numeric source. Use the
	// production manifest/import path rather than fabricating a checkpoint.
	plan := model.UsageEventBackfillPlan{
		SourceNamespace: lineage.LegacySourceNamespace, CollectionEpoch: lineage.LegacyCollectionEpoch,
		SourceKind: model.UsageEventSourceLegacyRelational, StartAt: 1000, EndAt: 2000, FenceDigest: lineage.FenceDigest,
	}
	page, err := model.InspectUsageEventBackfillBatch(t.Context(), db, plan, model.UsageEventBackfillCursor{}, 100)
	require.NoError(t, err)
	require.True(t, page.Exhausted)
	plan.ExpectedRows, plan.ManifestDigest = page.Next.Rows, page.Next.Digest
	progress, err := model.RunUsageEventBackfillBatch(t.Context(), db, plan, 100)
	require.NoError(t, err)
	require.True(t, progress.RetainedVerified)
	require.NoError(t, model.ActivateUsageStatisticsLineage(context.Background(), lineage, baseline))
}

func decodeFlowQuotaResponse(t *testing.T, recorder *httptest.ResponseRecorder) flowQuotaResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload flowQuotaResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success, payload.Message)
	return payload
}

func TestGetAllFlowQuotaDatesUsesAdminDimensions(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("role", common.RoleAdminUser)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow?start_timestamp=1000&end_timestamp=2000&username=bob", nil)

	GetAllFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	assertUsageResponseCoverage(t, payload.Coverage, 1000, 2000)
	require.Equal(t, "bob", payload.Data[0].Username)
	require.Equal(t, "vip", payload.Data[0].UseGroup)
	require.Equal(t, "east", payload.Data[0].ChannelName)
	require.Empty(t, payload.Data[0].TokenName)
	require.Empty(t, payload.Data[0].NodeName)
}

func TestGetAllFlowQuotaDatesUsesRootDimensions(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("role", common.RoleRootUser)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow?start_timestamp=1000&end_timestamp=2000&username=alice", nil)

	GetAllFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	assertUsageResponseCoverage(t, payload.Coverage, 1000, 2000)
	require.Equal(t, "alice", payload.Data[0].Username)
	require.Equal(t, "node-a", payload.Data[0].NodeName)
	require.Equal(t, "primary", payload.Data[0].TokenName)
	require.Equal(t, "default", payload.Data[0].UseGroup)
	require.Equal(t, "east", payload.Data[0].ChannelName)
}

func TestGetUserFlowQuotaDatesRestrictsToAuthenticatedUser(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow/self?start_timestamp=1000&end_timestamp=2000", nil)

	GetUserFlowQuotaDates(ctx)

	payload := decodeFlowQuotaResponse(t, recorder)
	require.Len(t, payload.Data, 1)
	assertUsageResponseCoverage(t, payload.Coverage, 1000, 2000)
	require.Empty(t, payload.Data[0].Username)
	require.Equal(t, "primary", payload.Data[0].TokenName)
	require.Equal(t, "default", payload.Data[0].UseGroup)
	require.Empty(t, payload.Data[0].ChannelName)
}

func assertUsageResponseCoverage(t *testing.T, coverage model.UsageStatisticsCoverageView, start, end int64) {
	t.Helper()
	require.False(t, coverage.Complete)
	require.Equal(t, model.UsageCoveragePartial, coverage.Coverage)
	require.Equal(t, model.UsageCoveragePartial, coverage.HistoricalCoverage)
	require.Equal(t, model.UsageCoveragePartial, coverage.LiveCoverage)
	require.Equal(t, start, coverage.StartTimestamp)
	require.Equal(t, end, coverage.EndTimestamp)
	require.True(t, coverage.LegacyBaselineIncluded)
}

func assertChannelUsageCoverage(t *testing.T, coverage model.UsageStatisticsCoverageView) {
	t.Helper()
	require.False(t, coverage.Complete)
	require.Equal(t, model.UsageCoveragePartial, coverage.Coverage)
	require.Equal(t, model.UsageCoverageUnknown, coverage.HistoricalCoverage)
	require.Equal(t, model.UsageCoveragePartial, coverage.LiveCoverage)
	require.Equal(t, int64(0), coverage.StartTimestamp)
	require.Equal(t, int64(0), coverage.EndTimestamp)
	require.False(t, coverage.LegacyBaselineIncluded)
}

func TestGetUserFlowQuotaDatesRejectsInvalidTimeRange(t *testing.T) {
	setupFlowControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data/flow/self?start_timestamp=bad&end_timestamp=2000", nil)

	GetUserFlowQuotaDates(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload flowQuotaResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.False(t, payload.Success)
	require.Equal(t, "invalid start_timestamp", payload.Message)
}

func TestQuotaDateHandlersReturnDashboardCoverageForEmptyTotals(t *testing.T) {
	setupFlowControllerTestDB(t)
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		userID  int
	}{
		{name: "all", handler: GetAllQuotaDates},
		{name: "grouped users", handler: GetQuotaDatesByUser},
		{name: "authenticated user", handler: GetUserQuotaDates, userID: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Set("id", tc.userID)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/data?start_timestamp=1000&end_timestamp=2000", nil)
			tc.handler(ctx)

			var payload struct {
				Success  bool                              `json:"success"`
				Coverage model.UsageStatisticsCoverageView `json:"coverage"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assertUsageResponseCoverage(t, payload.Coverage, 1000, 2000)
		})
	}
}

func TestChannelUsageEndpointsReturnLifetimeCoverageForEmptyTotals(t *testing.T) {
	setupFlowControllerTestDB(t)
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		path    string
	}{
		{name: "list", handler: GetChannelUsages, path: "/api/channel/usage?ids=1"},
		{name: "detail", handler: GetChannelUsage, path: "/api/channel/1/usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.name == "detail" {
				ctx.Params = gin.Params{{Key: "id", Value: "1"}}
			}
			tc.handler(ctx)
			var payload struct {
				Success  bool                              `json:"success"`
				Coverage model.UsageStatisticsCoverageView `json:"coverage"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			assertChannelUsageCoverage(t, payload.Coverage)
		})
	}
}

func TestRankingsReturnsCoverageBesideNumericData(t *testing.T) {
	setupFlowControllerTestDB(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/rankings?period=today", nil)
	GetRankings(ctx)

	var payload struct {
		Success          bool                              `json:"success"`
		Data             service.RankingsResponse          `json:"data"`
		Coverage         model.UsageStatisticsCoverageView `json:"coverage"`
		PreviousCoverage model.UsageStatisticsCoverageView `json:"previous_coverage"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Empty(t, payload.Data.Models)
	require.Equal(t, model.UsageStatisticsCoverageView{}, payload.Data.Coverage, "coverage is exposed beside data")
	require.Equal(t, model.UsageCoveragePartial, payload.Coverage.Coverage)
	require.Equal(t, model.UsageCoveragePartial, payload.PreviousCoverage.Coverage)
	require.Equal(t, model.UsageCoveragePartial, payload.Coverage.HistoricalCoverage)
	require.True(t, payload.Coverage.LegacyBaselineIncluded)
	require.Greater(t, payload.Coverage.EndTimestamp, payload.Coverage.StartTimestamp)
	require.Equal(t, payload.Coverage.StartTimestamp-1, payload.PreviousCoverage.EndTimestamp)
}
