package controller

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Budget rejection precedes upstream dispatch. Keep the channel pin and the
// caller's owner/group constraints while trying another allocation of it.
func reserveSelectedChannelBudget(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	// The first HTTP relay has no ChannelMeta; on retry it may describe the
	// previous channel. Always use the current selection for the hold.
	info.InitChannelMeta(c)
	var excluded []int64
	var channel *model.Channel
	for {
		apiErr := service.ReserveAttemptChannelBudget(c, info)
		if !service.IsChannelBudgetCandidateRejection(c, apiErr) {
			return apiErr
		}
		allocation, _ := common.GetContextKeyType[model.ChannelAllocation](c, constant.ContextKeyChannelAllocation)
		if allocation.EntryID <= 0 || slices.Contains(excluded, allocation.EntryID) {
			return apiErr
		}
		excluded = append(excluded, allocation.EntryID)
		if channel == nil {
			var err error
			channel, err = model.GetChannelById(info.ChannelId, true)
			if err != nil {
				return apiErr
			}
			if ok, _ := model.ChannelSatisfiesFilters(channel, info.OriginModelName, service.GetChannelConstraints(c).Filters, info.UserId); !ok || channel.Status != common.ChannelStatusEnabled {
				return apiErr
			}
		}
		if err := middleware.SetupContextForSelectedChannelExcluding(c, channel, info.OriginModelName, excluded); err != nil {
			return apiErr
		}
		chosen, _ := common.GetContextKeyType[model.ChannelAllocation](c, constant.ContextKeyChannelAllocation)
		// A bound channel reached outside the public group cannot fall back to
		// that channel's public pool. The reservation also checks live policy.
		if chosen.UserID == 0 && !model.IsChannelEnabledForGroupModel(info.UsingGroup, info.OriginModelName, channel.Id) {
			return apiErr
		}
		info.InitChannelMeta(c)
	}
}
