package model

import (
	"cmp"
	"context"
	"slices"
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
	data, err := readUsageDashboard(ctx, startTime, endTime, usageDashboardFilter{ModelName: modelName, FilterModel: true, RankingLimit: limit}, []string{"user_id"})
	if err != nil {
		return nil, err
	}
	rows := make([]UserRankingRow, 0, min(len(data), limit))
	for _, row := range data {
		if row.TokenUsed > 0 {
			rows = append(rows, UserRankingRow{UserID: row.UserID, Requests: row.Count, Tokens: row.TokenUsed, Quota: row.Quota})
		}
	}
	slices.SortFunc(rows, func(a, b UserRankingRow) int {
		if order := cmp.Compare(b.Tokens, a.Tokens); order != 0 {
			return order
		}
		return cmp.Compare(a.UserID, b.UserID)
	})
	return rows[:min(limit, len(rows))], nil
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
	rows, err := readUsageDashboard(ctx, startTime, endTime, usageDashboardFilter{ModelName: modelName, FilterModel: true}, nil)
	if err != nil {
		return ModelUsageTotals{}, err
	}
	if len(rows) == 0 {
		return ModelUsageTotals{}, nil
	}
	return ModelUsageTotals{Requests: rows[0].Count, Tokens: rows[0].TokenUsed, Quota: rows[0].Quota}, nil
}

func GetRankingQuotaTotals(startTime int64, endTime int64) ([]RankingQuotaTotal, error) {
	data, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{NonemptyModel: true}, []string{"model_name"})
	if err != nil {
		return nil, err
	}
	rows := make([]RankingQuotaTotal, 0, len(data))
	for _, row := range data {
		if row.TokenUsed > 0 {
			rows = append(rows, RankingQuotaTotal{ModelName: row.ModelName, TotalTokens: row.TokenUsed, CacheReadTokens: row.CacheReadTokens, CacheCreationTokens: row.CacheCreationTokens})
		}
	}
	slices.SortFunc(rows, func(a, b RankingQuotaTotal) int {
		if order := cmp.Compare(b.TotalTokens, a.TotalTokens); order != 0 {
			return order
		}
		return cmp.Compare(a.ModelName, b.ModelName)
	})
	return rows, nil
}

func GetRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64) ([]RankingQuotaBucket, error) {
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	data, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{NonemptyModel: true}, []string{"model_name", "created_at"})
	if err != nil {
		return nil, err
	}
	type bucketKey struct {
		model     string
		timestamp int64
	}
	byBucket := make(map[bucketKey]int64, len(data))
	for _, row := range data {
		key := bucketKey{row.ModelName, row.CreatedAt - row.CreatedAt%bucketSize}
		total, err := checkedUsageAdd(byBucket[key], row.TokenUsed)
		if err != nil {
			return nil, err
		}
		byBucket[key] = total
	}
	rows := make([]RankingQuotaBucket, 0, len(byBucket))
	for key, total := range byBucket {
		if total > 0 {
			rows = append(rows, RankingQuotaBucket{ModelName: key.model, Bucket: key.timestamp, Tokens: total})
		}
	}
	slices.SortFunc(rows, func(a, b RankingQuotaBucket) int {
		if order := cmp.Compare(a.Bucket, b.Bucket); order != 0 {
			return order
		}
		return cmp.Compare(a.ModelName, b.ModelName)
	})
	return rows, nil
}
