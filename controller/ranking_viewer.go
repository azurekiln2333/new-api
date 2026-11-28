package controller

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const rankingModelUsageUserLimit = 20

// GetRankingModelUsage answers the per-model usage drawer: the model's aggregate
// usage for the period plus a bounded user leaderboard, with identities masked
// on the server for ordinary and anonymous viewers.
//
// The totals cover every user that used the model in the period; the user list
// is a bounded top-N and may cover less. Data comes from the same primary
// quota_data aggregation as the rankings page, which is written from hourly
// export buckets, so it is not realtime and must not be presented as such.
func GetRankingModelUsage(c *gin.Context) {
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		common.ApiErrorMsg(c, "model is required")
		return
	}
	period := c.DefaultQuery("period", "week")
	periodRange := service.RankingPeriodRange(period)
	if periodRange.Invalid {
		common.ApiErrorMsg(c, "invalid ranking period")
		return
	}

	ctx := c.Request.Context()
	startTime := periodRange.Start
	endTime := periodRange.End

	// Totals are aggregated over all users so a popular model is not
	// undercounted by the leaderboard limit.
	totals, err := model.GetModelUsageTotals(ctx, modelName, startTime, endTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rows, err := model.GetModelUserUsageRanking(ctx, modelName, startTime, endTime, rankingModelUsageUserLimit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	viewer := rankingViewerFromContext(c)
	names, err := resolveRankingNames(ctx, viewer, rows)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	users := model.BuildUserRankingView(viewer, rows, names)
	coverage, err := model.GetUsageStatisticsCoverage(ctx, model.UsageStatisticsDashboard, startTime, endTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// The response is viewer-scoped, so it must never be stored in a shared
	// cache. no-store keeps a masked or unmasked payload from being replayed
	// across identities.
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"model_name":   modelName,
			"period":       period,
			"start_time":   startTime,
			"end_time":     endTime,
			"generated_at": common.GetTimestamp(),
			"totals": gin.H{
				"requests": totals.Requests,
				"tokens":   totals.Tokens,
				"quota":    totals.Quota,
			},
			"users": users,
		},
		"coverage": coverage,
	})
}

// resolveRankingNames batch-resolves current names for the bounded leaderboard.
// Anonymous viewers resolve nothing. BuildUserRankingView applies the viewer's
// full/partial policy before any resolved name enters the response.
func resolveRankingNames(ctx context.Context, viewer model.RankingViewer, rows []model.UserRankingRow) (map[int]string, error) {
	ids := model.VisibleUserIDs(viewer, rows)
	if len(ids) == 0 {
		return map[int]string{}, nil
	}
	return model.GetUsernamesByIDs(ctx, ids)
}

// rankingViewerFromContext resolves the viewer identity used to mask names.
// Only a trusted authenticated identity may claim to be a user; anonymous
// viewers are always treated as anonymous.
func rankingViewerFromContext(c *gin.Context) model.RankingViewer {
	viewer := model.RankingViewer{}
	userID := c.GetInt("id")
	role := c.GetInt("role")
	if userID <= 0 {
		viewer.IsAnonymous = true
		return viewer
	}
	viewer.UserID = userID
	viewer.IsAdmin = role >= common.RoleAdminUser
	return viewer
}
