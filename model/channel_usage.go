package model

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ChannelUsage is the consume-log usage of one channel.
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

// GetChannelUsages sums the consume logs of the given channels with one
// grouped query. Channels without consume logs are absent from the result.
func GetChannelUsages(channelIds []int) ([]ChannelUsage, error) {
	usages := make([]ChannelUsage, 0, len(channelIds))
	if len(channelIds) == 0 {
		return usages, nil
	}
	err := LOG_DB.Table("logs").
		Select("channel_id, COUNT(*) AS request_count, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("logs.type = ? AND logs.channel_id IN ?", LogTypeConsume, channelIds).
		Group("channel_id").
		Order("channel_id").
		Scan(&usages).Error
	if err != nil {
		common.SysError(common.LogText("failed to query channel usage: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}
	for i := range usages {
		usages[i].TotalTokens = usages[i].PromptTokens + usages[i].CompletionTokens
	}
	return usages, nil
}

// GetChannelUsageDetail returns the usage of one channel by model. The models
// are the configured models plus every model seen in the channel's consume
// logs, busiest first.
func GetChannelUsageDetail(channelId int, configuredModels []string) (*ChannelUsageDetail, error) {
	var rows []ChannelModelUsage
	err := LOG_DB.Table("logs").
		Select("model_name, COUNT(*) AS request_count, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota, COALESCE(MAX(created_at), 0) AS last_used_at").
		Where("logs.type = ? AND logs.channel_id = ?", LogTypeConsume, channelId).
		Group("model_name").
		Scan(&rows).Error
	if err != nil {
		common.SysError(common.LogText("failed to query channel model usage: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}

	var enabledModels []string
	err = DB.Table("abilities").
		Where("channel_id = ? AND enabled = ?", channelId, true).
		Distinct("model").
		Pluck("model", &enabledModels).Error
	if err != nil {
		common.SysError(common.LogText("failed to query channel abilities: %s", err.Error()))
		return nil, common.NewMessage("Failed to query statistics")
	}

	byModel := make(map[string]*ChannelModelUsage, len(rows)+len(configuredModels))
	for i := range rows {
		if rows[i].ModelName == "" {
			continue
		}
		byModel[rows[i].ModelName] = &rows[i]
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
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
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
