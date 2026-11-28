package model

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ChannelUsage is the durable gross consume usage of one channel.
type ChannelUsage struct {
	ChannelId        int   `json:"channel_id"`
	RequestCount     int64 `json:"request_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Quota            int64 `json:"quota"`
}

// ChannelModelUsage is ChannelUsage broken down by model. Enabled reports
// whether any ability of the channel currently serves the model.
type ChannelModelUsage struct {
	ModelName        string `json:"model_name"`
	Enabled          bool   `json:"enabled"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	Quota            int64  `json:"quota"`
	LastUsedAt       int64  `json:"last_used_at"`
}

// ChannelUsageDetail is the usage of one channel with its per-model breakdown.
type ChannelUsageDetail struct {
	ChannelUsage
	Models []ChannelModelUsage `json:"models"`
}

// GetChannelUsages combines lifetime projections with their ready pending tail.
func GetChannelUsages(channelIds []int) ([]ChannelUsage, error) {
	usages := make([]ChannelUsage, 0, len(channelIds))
	if len(channelIds) == 0 {
		return usages, nil
	}
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyChannelUsages(channelIds)
		// Cleanup can commit after the first marker read but before the log
		// aggregate. Only a second committed marker read can validate fallback.
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageChannels(context.Background(), channelIds, false)
	if err != nil {
		return nil, err
	}
	byChannel := make(map[int]*ChannelUsage, len(channelIds))
	for _, row := range rows {
		usage := byChannel[row.ChannelID]
		if usage == nil {
			usage = &ChannelUsage{ChannelId: row.ChannelID}
			byChannel[row.ChannelID] = usage
		}
		if err := addUsageChannelRow(usage, row); err != nil {
			return nil, err
		}
	}
	for _, usage := range byChannel {
		usages = append(usages, *usage)
	}
	slices.SortFunc(usages, func(a, b ChannelUsage) int { return cmp.Compare(a.ChannelId, b.ChannelId) })
	return usages, nil
}

// GetChannelUsageDetail returns the usage of one channel by model. The models
// are the configured models plus historical durable models, busiest first.
func GetChannelUsageDetail(channelId int, configuredModels []string) (*ChannelUsageDetail, error) {
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyChannelUsageDetail(channelId, configuredModels)
		// Materialize both log usage and current ability metadata before this
		// recheck so one response cannot mix pre/post-cleanup numeric branches.
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageChannels(context.Background(), []int{channelId}, true)
	if err != nil {
		return nil, err
	}

	var enabledModels []string
	err = DB.Table("abilities").
		Where("channel_id = ? AND enabled = ?", channelId, true).
		Distinct("model").
		Pluck("model", &enabledModels).Error
	if err != nil {
		return nil, err
	}

	byModel := make(map[string]*ChannelModelUsage, len(rows)+len(configuredModels))
	for _, row := range rows {
		if row.ModelName == "" {
			continue
		}
		usage := byModel[row.ModelName]
		if usage == nil {
			usage = &ChannelModelUsage{ModelName: row.ModelName}
			byModel[row.ModelName] = usage
		}
		total := ChannelUsage{RequestCount: usage.RequestCount, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, Quota: usage.Quota}
		if err := addUsageChannelRow(&total, row); err != nil {
			return nil, err
		}
		usage.RequestCount, usage.PromptTokens, usage.CompletionTokens = total.RequestCount, total.PromptTokens, total.CompletionTokens
		usage.Quota, usage.TotalTokens = total.Quota, total.TotalTokens
		usage.LastUsedAt = max(usage.LastUsedAt, row.LastUsedAt)
	}
	for _, name := range configuredModels {
		name = strings.TrimSpace(name)
		if name == "" || byModel[name] != nil {
			continue
		}
		byModel[name] = &ChannelModelUsage{ModelName: name}
	}

	detail := &ChannelUsageDetail{
		ChannelUsage: ChannelUsage{ChannelId: channelId},
		Models:       make([]ChannelModelUsage, 0, len(byModel)),
	}
	for _, usage := range byModel {
		usage.Enabled = slices.Contains(enabledModels, usage.ModelName)
		if err := addUsageChannelRow(&detail.ChannelUsage, usageChannelReadRow{
			ConsumeCount: usage.RequestCount, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, GrossQuota: usage.Quota,
		}); err != nil {
			return nil, err
		}
		detail.Models = append(detail.Models, *usage)
	}
	slices.SortFunc(detail.Models, func(a, b ChannelModelUsage) int {
		if a.TotalTokens != b.TotalTokens {
			if a.TotalTokens > b.TotalTokens {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ModelName, b.ModelName)
	})
	return detail, nil
}

func getLegacyChannelUsages(channelIds []int) ([]ChannelUsage, error) {
	var usages []ChannelUsage
	if err := LOG_DB.Table("logs").
		Select("channel_id, COUNT(*) AS request_count, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("logs.type = ? AND logs.channel_id IN ?", LogTypeConsume, channelIds).
		Group("channel_id").
		Order("channel_id").
		Scan(&usages).Error; err != nil {
		common.SysError(common.LogText("failed to query channel usage: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}
	for i := range usages {
		usages[i].TotalTokens = usages[i].PromptTokens + usages[i].CompletionTokens
	}
	return usages, nil
}

func getLegacyChannelUsageDetail(channelId int, configuredModels []string) (*ChannelUsageDetail, error) {
	var rows []ChannelModelUsage
	if err := LOG_DB.Table("logs").
		Select("model_name, COUNT(*) AS request_count, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota, COALESCE(MAX(created_at), 0) AS last_used_at").
		Where("logs.type = ? AND logs.channel_id = ?", LogTypeConsume, channelId).
		Group("model_name").
		Scan(&rows).Error; err != nil {
		common.SysError(common.LogText("failed to query channel model usage: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}
	var enabledModels []string
	if err := DB.Table("abilities").Where("channel_id = ? AND enabled = ?", channelId, true).Distinct("model").Pluck("model", &enabledModels).Error; err != nil {
		common.SysError(common.LogText("failed to query channel abilities: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}
	byModel := make(map[string]*ChannelModelUsage, len(rows)+len(configuredModels))
	for i := range rows {
		if rows[i].ModelName == "" {
			continue
		}
		rows[i].TotalTokens = rows[i].PromptTokens + rows[i].CompletionTokens
		byModel[rows[i].ModelName] = &rows[i]
	}
	for _, name := range configuredModels {
		name = strings.TrimSpace(name)
		if name == "" || byModel[name] != nil {
			continue
		}
		byModel[name] = &ChannelModelUsage{ModelName: name}
	}
	detail := &ChannelUsageDetail{ChannelUsage: ChannelUsage{ChannelId: channelId}, Models: make([]ChannelModelUsage, 0, len(byModel))}
	for _, usage := range byModel {
		usage.Enabled = slices.Contains(enabledModels, usage.ModelName)
		detail.RequestCount += usage.RequestCount
		detail.PromptTokens += usage.PromptTokens
		detail.CompletionTokens += usage.CompletionTokens
		detail.Quota += usage.Quota
		detail.Models = append(detail.Models, *usage)
	}
	detail.TotalTokens = detail.PromptTokens + detail.CompletionTokens
	slices.SortFunc(detail.Models, func(a, b ChannelModelUsage) int {
		if a.TotalTokens != b.TotalTokens {
			if a.TotalTokens > b.TotalTokens {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ModelName, b.ModelName)
	})
	return detail, nil
}
