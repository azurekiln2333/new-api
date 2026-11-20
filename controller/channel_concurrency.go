package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// maxChannelConcurrencyIds bounds one dashboard batch so a crafted query cannot
// turn the cards' single request into an unbounded database or coordinator read.
const maxChannelConcurrencyIds = 200

// ChannelConcurrencyEntry is one channel's live attempt sample. Current is nil
// when the coordinator could not be read, never as a stand-in for zero. Limit
// is nil for an unlimited channel; no fabricated number is reported there.
type ChannelConcurrencyEntry struct {
	Id         int    `json:"id"`
	Current    *int   `json:"current"`
	Limit      *int   `json:"limit"`
	Scope      string `json:"scope"`
	SampleTime int64  `json:"sample_time"`
}

// GetChannelConcurrency returns live concurrency for the channels listed in the
// ids query. Cards and an open detail share this one bounded response: one
// database read supplies the configured limits, one coordinator round trip the
// counts. Channels that do not exist are omitted.
func GetChannelConcurrency(c *gin.Context) {
	channelIds := make([]int, 0, maxChannelConcurrencyIds)
	seen := make(map[int]struct{}, maxChannelConcurrencyIds)
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
		if len(channelIds) > maxChannelConcurrencyIds {
			// The batch bound is a contract with the card list: an explicit
			// error is safer than silently sampling only the first ids.
			common.ApiErrorT(c, "Too many items in batch request, maximum is {{max}}", map[string]any{"max": maxChannelConcurrencyIds})
			return
		}
	}
	if len(channelIds) == 0 {
		common.ApiErrorT(c, "Invalid channel ID")
		return
	}
	channels, err := model.GetChannelConcurrencySettings(channelIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	limits := make(map[int]*int, len(channels))
	for _, channel := range channels {
		settings := dto.ChannelSettings{}
		if channel.Setting != nil && *channel.Setting != "" {
			// GetSetting repairs malformed persisted JSON. A dashboard sample
			// must remain read-only, including when configuration is invalid.
			if err := common.UnmarshalJsonStr(*channel.Setting, &settings); err != nil {
				common.ApiErrorT(c, "Invalid channel settings format: {{error}}", map[string]any{"error": err.Error()})
				return
			}
		}
		limit := settings.MaxConcurrentRequests
		if limit != nil && *limit <= 0 {
			// A stored zero is unlimited admission, so the API reports null
			// rather than a limit of zero.
			limit = nil
		}
		limits[channel.Id] = limit
	}
	samples := service.ReadChannelConcurrency(c.Request.Context(), channelIds)
	items := make([]ChannelConcurrencyEntry, 0, len(channels))
	for _, channelId := range channelIds {
		limit, known := limits[channelId]
		if !known {
			continue
		}
		sample := samples[channelId]
		items = append(items, ChannelConcurrencyEntry{
			Id:         channelId,
			Current:    sample.Current,
			Limit:      limit,
			Scope:      sample.Scope,
			SampleTime: sample.SampleTime.UnixMilli(),
		})
	}
	common.ApiSuccess(c, gin.H{"items": items})
}
