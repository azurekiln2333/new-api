package model

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RankingQuotaTotal struct {
	ModelName           string `json:"model_name"`
	TotalTokens         int64  `json:"total_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
}

type RankingQuotaBucket struct {
	ModelName string `json:"model_name"`
	Bucket    int64  `json:"bucket"`
	Tokens    int64  `json:"tokens"`
}

// GetModelUserUsageRanking aggregates one model's usage per user over a time
// range, ordered by token volume with a stable user id tiebreak. It groups by
// user id alone so a renamed account cannot appear twice and no historical
// username is carried here; the caller resolves display names separately and
// only for viewers allowed to see them.
func GetModelUserUsageRanking(ctx context.Context, modelName string, startTime int64, endTime int64, limit int) ([]UserRankingRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []UserRankingRow
	query := DB.WithContext(ctx).Table("quota_data").
		Select("user_id, sum(count) as requests, sum(token_used) as tokens, sum(quota) as quota").
		Where("model_name = ?", modelName).
		Group("user_id").
		Having("sum(token_used) > 0").
		Order("tokens DESC").
		Order("user_id ASC").
		Limit(limit)
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

// ModelUsageTotals is a model's aggregate usage over a time range.
type ModelUsageTotals struct {
	Requests int64 `json:"requests"`
	Tokens   int64 `json:"tokens"`
	Quota    int64 `json:"quota"`
}

// GetModelUsageTotals aggregates one model's usage over ALL users in the time
// range, independent of any leaderboard limit, so displayed totals are not
// undercounted by a bounded top-N list.
func GetModelUsageTotals(ctx context.Context, modelName string, startTime int64, endTime int64) (ModelUsageTotals, error) {
	var totals ModelUsageTotals
	query := DB.WithContext(ctx).Table("quota_data").
		Select("coalesce(sum(count), 0) as requests, coalesce(sum(token_used), 0) as tokens, coalesce(sum(quota), 0) as quota").
		Where("model_name = ?", modelName)
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Scan(&totals).Error
	return totals, err
}

func GetRankingQuotaTotals(startTime int64, endTime int64) ([]RankingQuotaTotal, error) {
	var rows []RankingQuotaTotal
	query := DB.Table("quota_data").
		Select("model_name, sum(token_used) as total_tokens, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").
		Where("model_name <> ''").
		Group("model_name").
		Having("sum(token_used) > 0").
		Order("total_tokens DESC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

func GetRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64) ([]RankingQuotaBucket, error) {
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	bucketExpr := rankingBucketExpr(bucketSize)
	var rows []RankingQuotaBucket
	query := DB.Table("quota_data").
		Select(fmt.Sprintf("model_name, %s as bucket, sum(token_used) as tokens", bucketExpr)).
		Where("model_name <> ''").
		Group(fmt.Sprintf("model_name, %s", bucketExpr)).
		Having("sum(token_used) > 0").
		Order("bucket ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

func rankingBucketExpr(bucketSize int64) string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return fmt.Sprintf("FLOOR(created_at / %d) * %d", bucketSize, bucketSize)
	}
	return fmt.Sprintf("(created_at / %d) * %d", bucketSize, bucketSize)
}

func applyRankingQuotaTimeRange(query *gorm.DB, startTime int64, endTime int64) *gorm.DB {
	if startTime > 0 {
		query = query.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("created_at <= ?", endTime)
	}
	return query
}
