package service

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// ListPlaygroundChannels uses the same permitted Auto groups as pin routing and
// leaves path, allocation ownership, scope, status and budget checks to model.
func ListPlaygroundChannels(ctx context.Context, userID int, userGroup, group string) ([]model.ChannelModelOption, error) {
	groups := []string{group}
	if group == "auto" {
		groups = GetUserAutoGroup(userGroup)
	}
	return model.ListAvailableChannelModels(ctx, groups, userID, []dto.ChannelFilter{
		{Kind: dto.FilterRequestPath, RequestPath: "/pg/chat/completions"},
		{Kind: dto.FilterTaskPluginIdentity, RequestPath: "/pg/chat/completions"},
	})
}

// PlaygroundChannelModels maps each eligible model to its effective group.
// Auto uses the same ordered, permission-filtered groups as ordinary routing.
func PlaygroundChannelModels(channel *model.Channel, userID int, userGroup, group string) map[string]string {
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return nil
	}
	groups := []string{group}
	if group == "auto" {
		groups = GetUserAutoGroup(userGroup)
	}
	models := make(map[string]string)
	for _, candidate := range groups {
		for _, name := range channel.EligibleModelsForUserGroup(candidate, userID) {
			if _, exists := models[name]; !exists {
				models[name] = candidate
			}
		}
	}
	if group == "auto" && len(groups) == 0 {
		for _, name := range channel.UserBoundModels(userID) {
			models[name] = userGroup
		}
	}
	return models
}
