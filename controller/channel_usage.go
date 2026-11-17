package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxChannelUsageIds = 200

// GetChannelUsages returns the consume-log usage of the channels listed in the
// ids query. Channels without usage are omitted.
func GetChannelUsages(c *gin.Context) {
	channelIds := make([]int, 0, maxChannelUsageIds)
	seen := make(map[int]struct{}, maxChannelUsageIds)
	for part := range strings.SplitSeq(c.Query("ids"), ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		channelIds = append(channelIds, id)
		if len(channelIds) == maxChannelUsageIds {
			break
		}
	}
	items, err := model.GetChannelUsages(channelIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// GetChannelUsage returns the usage of one channel with its per-model status.
func GetChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		common.ApiErrorT(c, "Invalid channel ID")
		return
	}
	channel, err := model.GetChannelById(channelId, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorT(c, "Channel does not exist")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	detail, err := model.GetChannelUsageDetail(channelId, channel.GetModels())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, detail)
}
