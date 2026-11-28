package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type OpenAIModel struct {
	ID         string         `json:"id"`
	Object     string         `json:"object"`
	Created    int64          `json:"created"`
	OwnedBy    string         `json:"owned_by"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Permission []struct {
		ID                 string `json:"id"`
		Object             string `json:"object"`
		Created            int64  `json:"created"`
		AllowCreateEngine  bool   `json:"allow_create_engine"`
		AllowSampling      bool   `json:"allow_sampling"`
		AllowLogprobs      bool   `json:"allow_logprobs"`
		AllowSearchIndices bool   `json:"allow_search_indices"`
		AllowView          bool   `json:"allow_view"`
		AllowFineTuning    bool   `json:"allow_fine_tuning"`
		Organization       string `json:"organization"`
		Group              string `json:"group"`
		IsBlocking         bool   `json:"is_blocking"`
	} `json:"permission"`
	Root   string `json:"root"`
	Parent string `json:"parent"`
}

type OpenAIModelsResponse struct {
	Data    []OpenAIModel `json:"data"`
	Success bool          `json:"success"`
}

func parseStatusFilter(statusParam string) int {
	switch strings.ToLower(statusParam) {
	case "enabled", "1":
		return common.ChannelStatusEnabled
	case "disabled", "0":
		return 0
	default:
		return -1
	}
}

func clearChannelInfo(channel *model.Channel) {
	channel.BalanceDetail = channel.GetBalanceDetail()
	if channel.ChannelInfo.IsMultiKey {
		channel.ChannelInfo.MultiKeyDisabledReason = nil
		channel.ChannelInfo.MultiKeyDisabledTime = nil
	}
}

func channelIDsFromChannels(channels []*model.Channel) []int {
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel != nil && channel.Id > 0 {
			ids = append(ids, channel.Id)
		}
	}
	return ids
}

func closeActiveChannelWebSockets(channelIDs []int) {
	service.CloseActiveWebSocketsForChannels(channelIDs, service.ChannelDisabledCloseReason)
}

func hasEnabledMultiKey(channel *model.Channel) bool {
	if channel == nil || !channel.ChannelInfo.IsMultiKey {
		return true
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return false
	}
	for i := range keys {
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			return true
		}
		if status, ok := channel.ChannelInfo.MultiKeyStatusList[i]; !ok || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

func disableMultiKeyChannelIfUnavailable(channel *model.Channel) bool {
	if channel == nil || !channel.ChannelInfo.IsMultiKey || hasEnabledMultiKey(channel) {
		return false
	}
	if channel.Status != common.ChannelStatusEnabled {
		return true
	}
	channel.Status = common.ChannelStatusManuallyDisabled
	info := channel.GetOtherInfo()
	info["status_reason"] = model.ChannelStatusReasonAllKeysDisabled
	info["status_time"] = common.GetTimestamp()
	channel.SetOtherInfo(info)
	return true
}

func restoreMultiKeyChannelIfAvailable(channel *model.Channel) {
	// Manual key disables and request-driven key failures both record the
	// all-keys reason; any later manual channel disable replaces it.
	disabled := channel.Status == common.ChannelStatusManuallyDisabled || channel.Status == common.ChannelStatusAutoDisabled
	if !disabled || !hasEnabledMultiKey(channel) {
		return
	}
	info := channel.GetOtherInfo()
	if info["status_reason"] != model.ChannelStatusReasonAllKeysDisabled {
		return
	}
	channel.Status = common.ChannelStatusEnabled
	info["status_reason"] = ""
	info["status_time"] = common.GetTimestamp()
	channel.SetOtherInfo(info)
}

// saveManagedKeyStatus runs change on the stored multi-key channel and writes
// the key statuses, channel status and status reason it leaves. A change
// another writer, on any node, makes to other_info meanwhile, such as a
// balance query, is kept.
func saveManagedKeyStatus(channelID int, change func(stored *model.Channel) error) error {
	statusChanged, enabled := false, false
	written, err := model.UpdateChannelOtherInfo(channelID, func(stored *model.Channel) (map[string]any, error) {
		status := stored.Status
		if err := change(stored); err != nil {
			return nil, err
		}
		statusChanged, enabled = stored.Status != status, stored.Status == common.ChannelStatusEnabled
		return map[string]any{"status": stored.Status, "channel_info": stored.ChannelInfo}, nil
	})
	if err != nil || !written {
		return err
	}
	model.BumpSyncTopic(model.SyncTopicChannels)
	if statusChanged {
		if err := model.UpdateAbilityStatus(channelID, enabled); err != nil {
			return err
		}
	}
	return model.RefreshChannelRouting(channelID)
}

func applyChannelStatusFilter(query *gorm.DB, statusFilter int) *gorm.DB {
	if statusFilter == common.ChannelStatusEnabled {
		return query.Where("status = ?", common.ChannelStatusEnabled)
	}
	if statusFilter == 0 {
		return query.Where("status != ?", common.ChannelStatusEnabled)
	}
	return query
}

func buildChannelListQuery(group string, statusFilter int, typeFilter int) *gorm.DB {
	query := model.DB.Model(&model.Channel{})
	query = model.ApplyChannelGroupFilter(query, group)
	query = applyChannelStatusFilter(query, statusFilter)
	if typeFilter >= 0 {
		query = query.Where("type = ?", typeFilter)
	}
	return query
}

func GetChannelOps(c *gin.Context) {
	snapshot := model.CurrentRequestPolicy()
	automaticDisable, source := snapshot.AutoDisable, "global"
	if value, present := c.GetQuery("auto_ban"); present {
		autoBan, err := strconv.ParseBool(value)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		automaticDisable = snapshot.AutoDisable && autoBan
		if snapshot.AutoDisable {
			source = "global_and_channel"
			if !autoBan {
				source = "channel"
			}
		}
	}
	common.ApiSuccess(c, gin.H{
		"retry_times":    snapshot.RetryTimes,
		"request_policy": gin.H{"automatic_disable": automaticDisable, "source": source},
	})
}

func GetChannelDefaultBaseURLs(c *gin.Context) {
	baseURLs := make(map[int]string)
	for channelType, baseURL := range constant.ChannelBaseURLs {
		if baseURL != "" {
			baseURLs[channelType] = baseURL
		}
	}
	common.ApiSuccess(c, baseURLs)
}

// channelOption is the lightweight channel projection a selector needs: enough
// to show and filter a channel without loading its credential, settings or
// allocation rows.
type channelOption struct {
	Id     int      `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// GetChannelOptions returns a bounded page of id, name and models for channel
// pickers, ordered by id desc. It projects only those three columns and never a
// key, setting or allocation row, so a dropdown does not page the full channel
// listing.
//
// Two independent bounds keep the response bounded: the page itself
// (`page_size`, capped) and the optional `ids` hydration list (deduplicated and
// capped), so neither a large page request nor a long id list can widen the
// query. `keyword` narrows by id/name/models for a searchable dropdown.
func GetChannelOptions(c *gin.Context) {
	const (
		defaultPageSize = 30
		maxPageSize     = 100
	)
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultPageSize)))
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	pageSize = min(pageSize, maxPageSize)
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	if page < 1 {
		page = 1
	}
	// Clamp the offset arithmetic itself. A huge `p` would otherwise overflow the
	// int multiplication and produce a negative OFFSET, which some databases
	// reject and others read as "start of the table".
	const maxOffset = math.MaxInt32
	if page-1 > maxOffset/pageSize {
		page = maxOffset/pageSize + 1
	}

	// The hydration list is bounded independently of the page: an arbitrarily long
	// `ids` value must not turn this endpoint into an unbounded IN query.
	const maxHydratedIds = 100
	selectedIds := make([]int, 0, maxHydratedIds)
	seenSelected := make(map[int]struct{}, maxHydratedIds)
	for _, raw := range strings.Split(c.Query("ids"), ",") {
		if len(selectedIds) >= maxHydratedIds {
			break
		}
		id, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || id <= 0 {
			continue
		}
		if _, duplicate := seenSelected[id]; duplicate {
			continue
		}
		seenSelected[id] = struct{}{}
		selectedIds = append(selectedIds, id)
	}

	type optionRow struct {
		Id     int    `gorm:"column:id"`
		Name   string `gorm:"column:name"`
		Models string `gorm:"column:models"`
	}
	rows := make([]optionRow, 0)
	optionQuery := model.DB.WithContext(c.Request.Context()).
		Model(&model.Channel{}).
		Select("id, name, models")
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		// id/name/models is the same searchable surface the console dropdown
		// exposes; key and base_url stay out of a read-only projection.
		optionQuery = optionQuery.Where("id = ? OR name LIKE ? OR models LIKE ?",
			common.String2Int(keyword), "%"+keyword+"%", "%"+keyword+"%")
	}
	var matched int64
	if err := optionQuery.Session(&gorm.Session{}).Count(&matched).Error; err != nil {
		common.SysError(common.LogText("failed to count channel options: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
		return
	}
	if err := optionQuery.Order("id desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&rows).Error; err != nil {
		common.SysError(common.LogText("failed to list channel options: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
		return
	}
	// has_more describes the page window only. Hydrated selected ids are appended
	// afterwards and must never make a non-final page look like the last one.
	pageRows := len(rows)
	hasMore := int64((page-1)*pageSize+pageRows) < matched

	// Hydrate the explicitly requested ids that the page did not cover so a
	// selected channel outside the window still resolves, still bounded by the
	// requested id list and the same three-column projection.
	if len(selectedIds) > 0 {
		seen := make(map[int]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Id] = struct{}{}
		}
		missing := make([]int, 0, len(selectedIds))
		for _, id := range selectedIds {
			if _, ok := seen[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			var extra []optionRow
			if err := model.DB.WithContext(c.Request.Context()).
				Model(&model.Channel{}).
				Select("id, name, models").
				Where("id IN ?", missing).
				Order("id desc").
				Limit(maxHydratedIds).
				Find(&extra).Error; err != nil {
				common.SysError(common.LogText("failed to list selected channel options: %s", err.Error()))
				common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
				return
			}
			rows = append(rows, extra...)
		}
	}

	items := make([]channelOption, 0, len(rows))
	for _, row := range rows {
		models := make([]string, 0)
		if row.Models != "" {
			for modelName := range strings.SplitSeq(row.Models, ",") {
				if modelName = strings.TrimSpace(modelName); modelName != "" {
					models = append(models, modelName)
				}
			}
		}
		items = append(items, channelOption{Id: row.Id, Name: row.Name, Models: models})
	}
	common.ApiSuccess(c, gin.H{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     matched,
		// has_more lets a searchable dropdown keep fetching without guessing
		// from a short page.
		"has_more": hasMore,
	})
}

func GetAllChannels(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelData := make([]*model.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	sortOptions := model.NewChannelSortOptions(c.Query("sort_by"), c.Query("sort_order"), idSort)
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	groupFilter := model.NormalizeChannelGroupFilter(c.Query("group"))
	statusParam := c.Query("status")
	// statusFilter: -1 all, 1 enabled, 0 disabled (include auto & manual)
	statusFilter := parseStatusFilter(statusParam)
	// type filter
	typeStr := c.Query("type")
	typeFilter := -1
	if typeStr != "" {
		if t, err := strconv.Atoi(typeStr); err == nil {
			typeFilter = t
		}
	}

	var total int64
	ctx := c.Request.Context()

	if enableTagMode {
		tags, err := model.GetPaginatedChannelTags(buildChannelListQuery(groupFilter, statusFilter, typeFilter).WithContext(ctx), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
		if err != nil {
			common.SysError(common.LogText("failed to get paginated tags: %s", err.Error()))
			common.ApiErrorT(c, "Failed to fetch tags. Please try again later")
			return
		}
		requestedTags := make([]string, 0, len(tags))
		total, err = model.CountChannelTags(buildChannelListQuery(groupFilter, statusFilter, typeFilter).WithContext(ctx))
		if err != nil {
			common.SysError(common.LogText("failed to count tags: %s", err.Error()))
			common.ApiErrorT(c, "Failed to count tags. Please try again later")
			return
		}
		for _, tag := range tags {
			if tag == nil || *tag == "" {
				continue
			}
			requestedTags = append(requestedTags, *tag)
		}
		// One coalesced read for every tag on this page. Tag mode still returns
		// each group complete (never truncated), but the number of queries no
		// longer grows with the number of tags.
		if len(requestedTags) > 0 {
			tagQuery := buildChannelListQuery(groupFilter, statusFilter, typeFilter).
				WithContext(model.WithBatchedChannelKeyEntries(ctx)).
				Where("tag IN ?", requestedTags)
			var fetched []*model.Channel
			if err := sortOptions.Apply(tagQuery).Omit("key").Find(&fetched).Error; err != nil {
				common.SysError(common.LogText("failed to get channels by tag: %s", err.Error()))
				common.ApiErrorT(c, "Failed to fetch channels with this tag. Please try again later")
				return
			}
			if err := model.ProjectChannelKeyEntries(tagQuery, ctx, fetched, true); err != nil {
				common.SysError(common.LogText("failed to project channel allocations: %s", err.Error()))
				common.ApiErrorT(c, "Failed to fetch channels with this tag. Please try again later")
				return
			}
			// Regroup in tag-page order so the response keeps the same grouped
			// ordering the per-tag reads produced.
			byTag := make(map[string][]*model.Channel, len(requestedTags))
			for _, channel := range fetched {
				if channel.Tag == nil {
					continue
				}
				byTag[*channel.Tag] = append(byTag[*channel.Tag], channel)
			}
			for _, tag := range requestedTags {
				channelData = append(channelData, byTag[tag]...)
			}
		}
	} else {
		pageItems, pageTotal, err := model.ListChannelsPage(ctx, buildChannelListQuery(groupFilter, statusFilter, typeFilter), pageInfo.GetStartIdx(), pageInfo.GetPageSize(), true, sortOptions)
		if err != nil {
			common.SysError(common.LogText("failed to get channels: %s", err.Error()))
			common.ApiErrorT(c, "Failed to fetch channels. Please try again later")
			return
		}
		total = pageTotal
		channelData = pageItems
	}

	for _, datum := range channelData {
		clearChannelInfo(datum)
	}

	// type_counts describes the same filter the page was counted under, so an
	// enabled/disabled/type selection reports matching totals, not the whole table.
	typeCounts, err := model.CountChannelTypesByQuery(buildChannelListQuery(groupFilter, statusFilter, -1).WithContext(ctx))
	if err != nil {
		common.SysError(common.LogText("failed to count channel types: %s", err.Error()))
		common.ApiErrorT(c, "Failed to count channel types. Please try again later")
		return
	}
	common.ApiSuccess(c, gin.H{
		"items":       channelData,
		"total":       total,
		"page":        pageInfo.GetPage(),
		"page_size":   pageInfo.GetPageSize(),
		"type_counts": typeCounts,
	})
	return
}

func buildFetchModelsHeaders(channel *model.Channel, key string) (http.Header, error) {
	var headers http.Header
	switch channel.Type {
	case constant.ChannelTypeAnthropic:
		headers = GetClaudeAuthHeader(key)
	default:
		headers = GetAuthHeader(key)
	}

	if err := applyFetchModelsHeaderOverrides(channel, key, headers); err != nil {
		return nil, err
	}
	return headers, nil
}

func applyFetchModelsHeaderOverrides(channel *model.Channel, key string, headers http.Header) error {
	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:          key,
			HeadersOverride: channel.GetHeaderOverride(),
		},
	}
	overrides, err := relaychannel.ResolveHeaderOverride(info, nil)
	if err != nil {
		return err
	}
	for name, value := range overrides {
		headers.Set(name, value)
	}

	return nil
}

func FetchUpstreamModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	ids, err := fetchChannelUpstreamModelIDsForRequest(c.Request.Context(), channel)
	if err != nil {
		common.ApiErrorT(c, "Failed to fetch models: {{error}}", map[string]any{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    ids,
	})
}

func FixChannelsAbilities(c *gin.Context) {
	success, fails, err := model.FixAbility()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"success": success,
			"fails":   fails,
		},
	})
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelKeyword := c.Query("model")
	statusParam := c.Query("status")
	statusFilter := parseStatusFilter(statusParam)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	sortOptions := model.NewChannelSortOptions(c.Query("sort_by"), c.Query("sort_order"), idSort)
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	ctx := c.Request.Context()
	typeParam := c.Query("type")
	typeFilter := -1
	if typeParam != "" {
		if tp, err := strconv.Atoi(typeParam); err == nil {
			typeFilter = tp
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	// Cap the page before the offset math. Both listing branches page in SQL, so an
	// unbounded page_size would let one request read the whole matching set; the
	// cap matches the console's other bounded listing endpoint (/options).
	const maxChannelPageSize = 100
	pageSize = min(pageSize, maxChannelPageSize)
	// Clamp the offset arithmetic so a huge page cannot overflow into a negative
	// or wrapped OFFSET.
	const maxOffset = math.MaxInt32
	if page-1 > maxOffset/pageSize {
		page = maxOffset/pageSize + 1
	}
	startIdx := (page - 1) * pageSize

	var total int64
	var pagedData []*model.Channel
	var typeCounts map[int64]int64
	if enableTagMode {
		// Tag mode keeps its contract: total and type_counts describe the expanded
		// set after the status filter and before type narrowing, and only the
		// requested page is read. The expansion, the counts and the page are all
		// expressed as SQL over the same tag subquery, so no branch loads every
		// matching channel row.
		var err error
		typeCounts, err = model.CountChannelTypesByQuery(model.ChannelTagSearchQuery(ctx, keyword, group, modelKeyword, statusFilter, -1))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		pageItems, pageTotal, err := model.ListChannelsPage(ctx, model.ChannelTagSearchQuery(ctx, keyword, group, modelKeyword, statusFilter, typeFilter), startIdx, pageSize, true, sortOptions)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		total = pageTotal
		pagedData = pageItems
	} else {
		// The non-tag search filters, counts and pages the whole match set in SQL,
		// so the page size never depends on loading every matching channel first.
		var err error
		typeCounts, err = model.CountChannelTypesByQuery(model.ChannelSearchQueryFiltered(ctx, keyword, group, modelKeyword, statusFilter, -1))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		pageItems, pageTotal, err := model.ListChannelsPage(ctx, model.ChannelSearchQueryFiltered(ctx, keyword, group, modelKeyword, statusFilter, typeFilter), (page-1)*pageSize, pageSize, true, sortOptions)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		total = pageTotal
		pagedData = pageItems
	}

	for _, datum := range pagedData {
		clearChannelInfo(datum)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items":       pagedData,
			"total":       total,
			"type_counts": typeCounts,
		},
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if channel != nil {
		clearChannelInfo(channel)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// GetChannelKey 获取渠道密钥（需要通过安全验证中间件）
// 此函数依赖 SecureVerificationRequired 中间件，确保用户已通过安全验证
func GetChannelKey(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		common.ApiErrorT(c, "Invalid channel ID")
		return
	}

	// 获取渠道信息（包含密钥）
	channel, err := model.GetChannelById(channelId, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorT(c, "Channel does not exist")
		return
	}
	if err != nil {
		writeSecurityOperationError(c, err)
		return
	}

	// 记录操作审计日志（高危：查看渠道密钥）
	recordManageAudit(c, "channel.key_view", map[string]any{
		"id":   channelId,
		"name": channel.Name,
	})

	// 返回渠道密钥
	// A plugin credential shows as its summary, never its secrets.
	common.ApiSuccessT(c, "Fetched successfully", map[string]any{
		"key": jsplugin.RedactCredentialLines(channel.Key),
	})
}

// maxTaskExtendPluginKeys bounds the plugins one New API channel can be
// extended with; it comfortably covers every built-in and installed plugin.
const maxTaskExtendPluginKeys = 32

// normalizePluginConfig validates the plugin config in the channel's settings
// and stores it as the schema reads it: declared properties only, defaults
// left to the read.
func normalizePluginConfig(channel *model.Channel, plugin *jsplugin.LoadedPlugin) error {
	otherSettings := channel.GetOtherSettings()
	config, err := plugin.ValidateChannelConfig(otherSettings.PluginConfig)
	if err != nil {
		return common.NewMessage("Invalid plugin config: {{error}}", map[string]any{"error": err.Error()})
	}
	if plugin.Meta.ChannelConfig == nil || string(config) == "{}" {
		config = nil
	}
	if !bytes.Equal(config, otherSettings.PluginConfig) {
		otherSettings.PluginConfig = config
		channel.SetOtherSettings(otherSettings)
	}
	return nil
}

// validateChannel 通用的渠道校验函数
func validateChannel(channel *model.Channel, isAdd bool) error {
	if channel == nil {
		return fmt.Errorf("channel cannot be empty")
	}

	// 校验 channel settings
	if err := channel.ValidateSettings(); err != nil {
		return common.NewMessage("Invalid channel settings format: {{error}}", map[string]any{"error": err.Error()})
	}
	if channel.Type == constant.ChannelTypeTaskPlugin {
		pluginKey := strings.TrimSpace(channel.GetSetting().TaskPluginKey)
		if pluginKey == "" {
			return fmt.Errorf("task plugin key is required")
		}
		if len(pluginKey) > 30 {
			return fmt.Errorf("task plugin key must not exceed 30 characters")
		}
		plugin, ok := jsplugin.DefaultRegistry.Get(pluginKey)
		if !ok {
			return fmt.Errorf("task plugin %q is not registered", pluginKey)
		}
		if !plugin.Meta.HasTaskPart() && !plugin.Meta.HasProviderPart() {
			return common.NewMessage("Plugin {{plugin}} only processes requests and cannot be a channel type", map[string]any{"plugin": pluginKey})
		}
		if channel.BaseURL == nil || strings.TrimSpace(*channel.BaseURL) == "" {
			// The plugin default is persisted onto the channel instead of being
			// resolved per request, so the destination host stays an auditable
			// channel property that only an administrator edit can change.
			if plugin.Meta.BaseURL == "" {
				return fmt.Errorf("base URL is required for task plugin channels")
			}
			defaultBaseURL := plugin.Meta.BaseURL
			channel.BaseURL = &defaultBaseURL
		}
		if err := normalizePluginConfig(channel, plugin); err != nil {
			return err
		}
	}

	setting := channel.GetSetting()
	if len(setting.RequestPlugins) > 0 {
		saved := map[string]bool{}
		for _, instance := range request_plugin_setting.GetInstances() {
			saved[instance.ID] = true
		}
		bound := make(map[string]bool, len(setting.RequestPlugins))
		for _, id := range setting.RequestPlugins {
			if !saved[id] {
				return common.NewMessage("Request plugin instance {{id}} does not exist", map[string]any{"id": id})
			}
			if bound[id] {
				return common.NewMessage("Request plugin instance {{id}} is bound more than once", map[string]any{"id": id})
			}
			bound[id] = true
		}
	}
	if channel.Type != constant.ChannelTypeNewAPI && len(setting.TaskExtendPluginKeys) > 0 {
		return fmt.Errorf("task_extend_plugin_keys is only supported on New API channels")
	}
	if channel.Type == constant.ChannelTypeNewAPI {
		if len(setting.TaskExtendPluginKeys) > maxTaskExtendPluginKeys {
			return fmt.Errorf("task_extend_plugin_keys must not exceed %d plugins", maxTaskExtendPluginKeys)
		}
		// The single key stays valid on a New API channel, so both bindings are
		// checked as one set.
		keys := setting.TaskExtendPluginKeys
		if setting.TaskPluginKey != "" {
			keys = append([]string{setting.TaskPluginKey}, keys...)
		}
		bound := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			if key == "" || key != strings.TrimSpace(key) {
				return fmt.Errorf("task plugin key %q is invalid", key)
			}
			if len(key) > 30 {
				return fmt.Errorf("task plugin key must not exceed 30 characters")
			}
			if _, duplicate := bound[key]; duplicate {
				return fmt.Errorf("task plugin %q is bound more than once", key)
			}
			plugin, ok := jsplugin.DefaultRegistry.Get(key)
			if !ok {
				return fmt.Errorf("task plugin %q is not registered", key)
			}
			if !plugin.Meta.SupportsUpstream(jsplugin.UpstreamKindNewAPI) {
				return fmt.Errorf("task plugin %q does not support a New API upstream and cannot be bound to a New API channel", key)
			}
			bound[key] = struct{}{}
		}
	}

	if channel.Type == constant.ChannelTypeNewAPI && strings.TrimSpace(channel.GetBaseURL()) == "" {
		return fmt.Errorf("New API channel base URL cannot be empty")
	}
	if channel.Type == constant.ChannelTypeVLLM && strings.TrimSpace(channel.GetBaseURL()) == "" {
		return fmt.Errorf("vLLM channel base URL cannot be empty")
	}
	if channel.Type == constant.ChannelTypeSGLang && strings.TrimSpace(channel.GetBaseURL()) == "" {
		return fmt.Errorf("SGLang channel base URL cannot be empty")
	}

	// 如果是添加操作，检查 channel 和 key 是否为空
	if isAdd {
		// A channel of a plugin that logs in may start without a key.
		if channel.Key == "" && !service.ChannelSupportsPluginLogin(channel) {
			return fmt.Errorf("channel cannot be empty")
		}
		if err := service.CheckSubmittedPluginCredentials(channel.Type, channel.GetSetting().TaskPluginKey, channel.Key, ""); err != nil {
			return err
		}

		// 检查模型名称长度是否超过 255
		for _, m := range channel.GetModels() {
			if len(m) > 255 {
				return common.NewMessage("Model name is too long: {{model}}", map[string]any{"model": m})
			}
		}
	}

	// VertexAI 特殊校验
	if channel.Type == constant.ChannelTypeVertexAi {
		if channel.Other == "" {
			return common.NewMessage("Deployment region cannot be empty")
		}

		regionMap, err := common.StrToMap(channel.Other)
		if err != nil {
			return common.NewMessage("Deployment region must be valid JSON, for example {{example}}", map[string]any{"example": `{"default": "us-central1", "region2": "us-east1"}`})
		}

		if regionMap["default"] == nil {
			return common.NewMessage("Deployment region must include a default field")
		}

		switch channel.GetOtherSettings().VertexAPIVersion {
		case "", "v1", "v1beta1":
		default:
			return common.NewMessage("Vertex AI API version must be v1 or v1beta1")
		}
	}

	// Codex OAuth key validation (optional, only when JSON object is provided)
	if channel.Type == constant.ChannelTypeCodex {
		trimmedKey := strings.TrimSpace(channel.Key)
		if isAdd || trimmedKey != "" {
			if !strings.HasPrefix(trimmedKey, "{") {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			var keyMap map[string]any
			if err := common.Unmarshal([]byte(trimmedKey), &keyMap); err != nil {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			if v, ok := keyMap["access_token"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include access_token")
			}
			if v, ok := keyMap["account_id"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include account_id")
			}
		}
	}

	return nil
}

func RefreshCodexChannelCredential(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	oauthKey, ch, err := service.RefreshCodexChannelCredential(ctx, channelId, service.CodexCredentialRefreshOptions{ResetCaches: true})
	if err != nil {
		common.SysError(common.LogText("failed to refresh codex channel credential: %s", err.Error()))
		common.ApiErrorT(c, "Failed to refresh credentials. Please try again later")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "refreshed",
		"data": gin.H{
			"expires_at":   oauthKey.Expired,
			"last_refresh": oauthKey.LastRefresh,
			"account_id":   oauthKey.AccountID,
			"email":        oauthKey.Email,
			"channel_id":   ch.Id,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
		},
	})
}

type AddChannelRequest struct {
	Mode                      string                `json:"mode"`
	MultiKeyMode              constant.MultiKeyMode `json:"multi_key_mode"`
	BatchAddSetKeyPrefix2Name bool                  `json:"batch_add_set_key_prefix_2_name"`
	Channel                   *model.Channel        `json:"channel"`
}

func getVertexArrayKeys(keys string) ([]string, []int, error) {
	if keys == "" {
		return nil, nil, nil
	}
	var keyArray []any
	err := common.Unmarshal([]byte(keys), &keyArray)
	if err != nil {
		return nil, nil, common.NewMessage("Batch-adding Vertex AI keys requires a JSON array, for example {{example}}. Check the input: {{error}}", map[string]any{"example": "[{key1}, {key2}...]", "error": err.Error()})
	}
	cleanKeys := make([]string, 0, len(keyArray))
	inputIndices := make([]int, 0, len(keyArray))
	for i, key := range keyArray {
		var keyStr string
		switch v := key.(type) {
		case string:
			keyStr = strings.TrimSpace(v)
		default:
			bytes, err := common.Marshal(v)
			if err != nil {
				return nil, nil, common.NewMessage("Failed to encode Vertex AI key JSON: {{error}}", map[string]any{"error": err.Error()})
			}
			keyStr = string(bytes)
		}
		if keyStr != "" {
			cleanKeys = append(cleanKeys, keyStr)
			inputIndices = append(inputIndices, i)
		}
	}
	if len(cleanKeys) == 0 {
		return nil, nil, common.NewMessage("Vertex AI keys for batch adding cannot be empty")
	}
	return cleanKeys, inputIndices, nil
}

func AddChannel(c *gin.Context) {
	addChannelRequest := AddChannelRequest{}
	err := c.ShouldBindJSON(&addChannelRequest)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// Binding a plugin needs the same permission on a New API channel as the
	// type-61 channel dedicated to it.
	if addChannelRequest.Channel != nil &&
		(addChannelRequest.Channel.Type == constant.ChannelTypeTaskPlugin || len(addChannelRequest.Channel.GetSetting().TaskPluginBindings()) > 0) &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task plugin channels require the task_plugin.bind permission",
		})
		return
	}

	baseURLFromPluginDefault := addChannelRequest.Channel != nil &&
		addChannelRequest.Channel.Type == constant.ChannelTypeTaskPlugin &&
		(addChannelRequest.Channel.BaseURL == nil || strings.TrimSpace(*addChannelRequest.Channel.BaseURL) == "")
	// 使用统一的校验函数
	if err := validateChannel(addChannelRequest.Channel, true); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := checkPluginChannelKeys(c, addChannelRequest.Channel, addChannelRequest.Channel.Key, ""); err != nil {
		common.ApiError(c, err)
		return
	}

	// A channel of a plugin that logs in may start without a key and wait for
	// a login; batch mode makes one channel per key, so it still needs keys.
	keyless := strings.TrimSpace(addChannelRequest.Channel.Key) == "" && service.ChannelSupportsPluginLogin(addChannelRequest.Channel)
	if keyless && addChannelRequest.Mode == "batch" {
		common.ApiError(c, fmt.Errorf("channel cannot be empty"))
		return
	}

	addChannelRequest.Channel.CreatedTime = common.GetTimestamp()
	keys := make([]string, 0)
	inputIndices := make([]int, 0)
	switch addChannelRequest.Mode {
	case "multi_to_single":
		addChannelRequest.Channel.ChannelInfo.IsMultiKey = true
		addChannelRequest.Channel.ChannelInfo.MultiKeyMode = addChannelRequest.MultiKeyMode
		if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi && addChannelRequest.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			array, indices, err := getVertexArrayKeys(addChannelRequest.Channel.Key)
			if err != nil {
				common.ApiError(c, err)
				return
			}
			addChannelRequest.Channel.ChannelInfo.MultiKeySize = len(array)
			addChannelRequest.Channel.Key = strings.Join(array, "\n")
			inputIndices = indices
		} else {
			cleanKeys := make([]string, 0)
			for i, key := range strings.Split(addChannelRequest.Channel.Key, "\n") {
				if key == "" {
					continue
				}
				key = strings.TrimSpace(key)
				cleanKeys = append(cleanKeys, key)
				inputIndices = append(inputIndices, i)
			}
			addChannelRequest.Channel.ChannelInfo.MultiKeySize = len(cleanKeys)
			addChannelRequest.Channel.Key = strings.Join(cleanKeys, "\n")
		}
		keys = []string{addChannelRequest.Channel.Key}
	case "batch":
		if addChannelRequest.Channel.Type == constant.ChannelTypeVertexAi && addChannelRequest.Channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
			// multi json
			keys, inputIndices, err = getVertexArrayKeys(addChannelRequest.Channel.Key)
			if err != nil {
				common.ApiError(c, err)
				return
			}
		} else {
			keys = strings.Split(addChannelRequest.Channel.Key, "\n")
			for i := range keys {
				inputIndices = append(inputIndices, i)
			}
		}
	case "single":
		keys = []string{addChannelRequest.Channel.Key}
		inputIndices = []int{0}
	default:
		common.ApiErrorT(c, "Unsupported add mode")
		return
	}

	channels := make([]model.Channel, 0, len(keys))
	channelInputIndices := make([][]int, 0, len(keys))
	for i, key := range keys {
		if key == "" {
			continue
		}
		localChannel := addChannelRequest.Channel
		localChannel.Key = key
		if addChannelRequest.BatchAddSetKeyPrefix2Name && len(keys) > 1 {
			keyPrefix := localChannel.Key
			if len(localChannel.Key) > 8 {
				keyPrefix = localChannel.Key[:8]
			}
			localChannel.Name = fmt.Sprintf("%s %s", localChannel.Name, keyPrefix)
		}
		channels = append(channels, *localChannel)
		if addChannelRequest.Mode == "multi_to_single" {
			channelInputIndices = append(channelInputIndices, inputIndices)
		} else {
			channelInputIndices = append(channelInputIndices, []int{inputIndices[i]})
		}
	}
	if keyless && len(channels) == 0 {
		addChannelRequest.Channel.Key = ""
		service.PluginLoginNeededStatus(addChannelRequest.Channel)
		channels = append(channels, *addChannelRequest.Channel)
		channelInputIndices = append(channelInputIndices, nil)
	}
	actor := snapshotChannelAuditActor(c)
	createAuditParams := map[string]any{
		"name":  addChannelRequest.Channel.Name,
		"type":  addChannelRequest.Channel.Type,
		"count": len(channels),
	}
	if baseURLFromPluginDefault {
		createAuditParams["base_url_source"] = "plugin_default"
	}
	// The audit obligation commits with the rows: a created channel can never be
	// missing its audit event, and no post-commit write to the log database runs
	// on this path.
	err = model.BatchInsertChannelsWithAudit(channels, model.BatchChannelPublication{
		Audit: actor.draft("channel.create", createAuditParams, true),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// The audit event is durable now, so the middleware fallback must not add a
	// second record for this request.
	markAuditLogged(c)
	ids := make([]int, len(channels))
	results := make([]gin.H, 0, len(inputIndices))
	for i, channel := range channels {
		ids[i] = channel.Id
		for _, inputIndex := range channelInputIndices[i] {
			results = append(results, gin.H{"input_index": inputIndex, "channel_id": channel.Id})
		}
	}
	data := gin.H{"ids": ids, "results": results}
	if len(ids) == 1 {
		data["id"] = ids[0]
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
	return
}

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channelName := ""
	channelProxy := ""
	channelLookupFailed := false
	existing, err := model.GetChannelById(id, true)
	if err == nil && existing != nil {
		channelName = existing.Name
		channelProxy = existing.GetSetting().Proxy
	} else {
		channelLookupFailed = true
	}
	channel := model.Channel{Id: id}
	err = channel.Delete()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !channelLookupFailed {
		service.RevokeDeletedChannelCredentials([]*model.Channel{existing})
	}
	model.CacheRemoveChannel(id)
	if channelLookupFailed {
		service.ResetProxyClientCache()
	} else {
		service.InvalidateProxyClient(channelProxy)
	}
	recordManageAudit(c, "channel.delete", map[string]any{
		"id":   id,
		"name": channelName,
	})
	closeActiveChannelWebSockets([]int{id})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func DeleteDisabledChannel(c *gin.Context) {
	var ids []int
	if err := model.DB.Model(&model.Channel{}).
		Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).
		Pluck("id", &ids).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var revoked []*model.Channel
	if err := model.DB.Where("(status = ? or status = ?) AND type = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled, constant.ChannelTypeTaskPlugin).
		Find(&revoked).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	rows, err := model.DeleteDisabledChannel()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	service.RevokeDeletedChannelCredentials(revoked)
	model.InitChannelCache()
	if rows > 0 {
		service.ResetProxyClientCache()
	}
	recordManageAudit(c, "channel.delete_disabled", map[string]any{
		"count": rows,
	})
	closeActiveChannelWebSockets(ids)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

type ChannelTag struct {
	Tag            string  `json:"tag"`
	NewTag         *string `json:"new_tag"`
	Priority       *int64  `json:"priority"`
	Weight         *uint   `json:"weight"`
	ModelMapping   *string `json:"model_mapping"`
	Models         *string `json:"models"`
	Groups         *string `json:"groups"`
	ParamOverride  *string `json:"param_override"`
	HeaderOverride *string `json:"header_override"`
}

func DisableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	channels, err := model.GetChannelsByTag(channelTag.Tag, false, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ids := channelIDsFromChannels(channels)
	err = model.DisableChannelByTag(channelTag.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_disable", map[string]any{
		"tag": channelTag.Tag,
	})
	closeActiveChannelWebSockets(ids)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EnableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = model.EnableChannelByTag(channelTag.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_enable", map[string]any{
		"tag": channelTag.Tag,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func EditTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if channelTag.Tag == "" {
		common.ApiErrorT(c, "Tag cannot be empty")
		return
	}
	if (channelTag.ParamOverride != nil || channelTag.HeaderOverride != nil) && !requireChannelSensitiveWrite(c) {
		return
	}
	if channelTag.ParamOverride != nil {
		trimmed := strings.TrimSpace(*channelTag.ParamOverride)
		if trimmed != "" && !json.Valid([]byte(trimmed)) {
			common.ApiErrorT(c, "Parameter override must be valid JSON format")
			return
		}
		channelTag.ParamOverride = common.GetPointer[string](trimmed)
	}
	if channelTag.HeaderOverride != nil {
		trimmed := strings.TrimSpace(*channelTag.HeaderOverride)
		if trimmed != "" && !json.Valid([]byte(trimmed)) {
			common.ApiErrorT(c, "Header override must be valid JSON format")
			return
		}
		channelTag.HeaderOverride = common.GetPointer[string](trimmed)
	}
	err = model.EditChannelByTag(channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight, channelTag.ParamOverride, channelTag.HeaderOverride)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_edit", map[string]any{
		"tag": channelTag.Tag,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type ChannelBatch struct {
	Ids []int   `json:"ids"`
	Tag *string `json:"tag"`
}

func DeleteChannelBatch(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var revoked []*model.Channel
	if err := model.DB.Where("id in (?) AND type = ?", channelBatch.Ids, constant.ChannelTypeTaskPlugin).Find(&revoked).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	deletedCount, err := model.BatchDeleteChannels(channelBatch.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	service.RevokeDeletedChannelCredentials(revoked)
	model.InitChannelCache()
	if deletedCount > 0 {
		service.ResetProxyClientCache()
	}
	recordManageAudit(c, "channel.delete_batch", map[string]any{
		"count": deletedCount,
	})
	closeActiveChannelWebSockets(channelBatch.Ids)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    deletedCount,
	})
	return
}

type PatchChannel struct {
	model.Channel
	MultiKeyMode     *string `json:"multi_key_mode"`
	KeyMode          *string `json:"key_mode"` // 多key模式下密钥覆盖或者追加
	ExpectedRevision *int64  `json:"expected_revision,omitempty"`
	ToMultiKey       *bool   `json:"to_multi_key"` // 单key渠道转多key渠道
}

type ChannelStatusRequest struct {
	Status int `json:"status"`
}

type ChannelStatusBatchRequest struct {
	Ids    []int `json:"ids"`
	Status int   `json:"status"`
}

func UpdateChannel(c *gin.Context) {
	channel := PatchChannel{}
	rawBody, err := c.GetRawData()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := common.Unmarshal(rawBody, &channel); err != nil {
		common.ApiError(c, err)
		return
	}
	var requestData map[string]any
	if err := common.Unmarshal(rawBody, &requestData); err != nil {
		common.ApiError(c, err)
		return
	}
	if _, ok := requestData["status"]; ok {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	clearChannelReadOnlyFields(&channel, requestData)

	if channel.Type == constant.ChannelTypeTaskPlugin &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task plugin channels require the task_plugin.bind permission",
		})
		return
	}

	// Preserve existing ChannelInfo to ensure multi-key channels keep correct state even if the client does not send ChannelInfo in the request.
	originChannel, err := model.GetChannelById(channel.Id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channelToValidate := &channel.Channel
	if channel.Type == 0 && channel.Setting != nil {
		var setting dto.ChannelSettings
		if common.UnmarshalJsonStr(*channel.Setting, &setting) == nil && len(setting.SupportedEndpoints) > 0 {
			// Partial updates retain the stored provider. Resolve its endpoint
			// capabilities without turning the omitted type into a type update.
			checked := channel.Channel
			checked.Type = originChannel.Type
			channelToValidate = &checked
		}
	}
	if err := validateChannel(channelToValidate, false); err != nil {
		common.ApiError(c, err)
		return
	}
	originProxy := originChannel.GetSetting().Proxy
	proxyChanged := false
	_, settingProvided := requestData["setting"]
	if settingProvided {
		newProxy, _ := service.NormalizeProxyURL(channel.GetSetting().Proxy)
		normalizedOriginProxy, originProxyErr := service.NormalizeProxyURL(originProxy)
		proxyChanged = originProxyErr != nil || normalizedOriginProxy != newProxy
	}
	// Changing which plugins a channel binds needs the bind permission on any
	// channel type; resubmitting an unchanged New API binding list does not, so
	// administrators without it can still edit the rest of a gateway channel.
	if settingProvided &&
		!slices.Equal(channel.GetSetting().TaskPluginBindings(), originChannel.GetSetting().TaskPluginBindings()) &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task plugin channels require the task_plugin.bind permission",
		})
		return
	}

	// Always copy the original ChannelInfo so that fields like IsMultiKey and MultiKeySize are retained.
	channel.ChannelInfo = originChannel.ChannelInfo

	if channelHasSensitiveChanges(&channel, originChannel, requestData) && !requireChannelSensitiveWrite(c) {
		return
	}

	// If the request explicitly specifies a new MultiKeyMode, apply it on top of the original info.
	if channel.MultiKeyMode != nil && *channel.MultiKeyMode != "" {
		channel.ChannelInfo.MultiKeyMode = constant.MultiKeyMode(*channel.MultiKeyMode)
	}

	// A single-key channel becomes multi-key by keeping its current key as the
	// first key. Channel types whose key is a JSON credential cannot be split
	// into several keys, and neither can Vertex AI channels holding simple API
	// keys; the create path only offers multi-key mode for the same set.
	if channel.ToMultiKey != nil && *channel.ToMultiKey && !channel.ChannelInfo.IsMultiKey {
		effectiveType := channel.Type
		if effectiveType == 0 {
			effectiveType = originChannel.Type
		}
		effectiveOther := originChannel.GetOtherSettings()
		if _, provided := requestData["settings"]; provided {
			effectiveOther = channel.GetOtherSettings()
		}
		if effectiveType == constant.ChannelTypeCodex ||
			(effectiveType == constant.ChannelTypeVertexAi && effectiveOther.VertexKeyType == dto.VertexKeyTypeAPIKey) {
			common.ApiErrorT(c, "This channel type does not support multiple keys")
			return
		}
		channel.ChannelInfo.IsMultiKey = true
		channel.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
		if channel.MultiKeyMode != nil && *channel.MultiKeyMode != "" {
			channel.ChannelInfo.MultiKeyMode = constant.MultiKeyMode(*channel.MultiKeyMode)
		}
		// The submitted keys are appended to the existing one by the key-mode
		// handling below, so the original key stays the first key.
		if channel.KeyMode == nil {
			appendMode := "append"
			channel.KeyMode = &appendMode
		}
	}

	// nextKey is the key the edit sets on a stored key: a submitted key replaces
	// it, and an empty one keeps it.
	nextKey := func(stored string) (string, error) { return channel.Key, nil }
	// 处理多key模式下的密钥追加/覆盖逻辑
	if channel.KeyMode != nil && *channel.KeyMode == "append" && channel.ChannelInfo.IsMultiKey {
		// 追加模式：将新密钥添加到现有密钥列表
		nextKey = func(stored string) (string, error) {
			if stored == "" {
				return channel.Key, nil
			}
			var newKeys []string
			var existingKeys []string

			// 解析现有密钥
			if strings.HasPrefix(strings.TrimSpace(stored), "[") {
				// JSON数组格式
				var arr []json.RawMessage
				if err := common.Unmarshal([]byte(strings.TrimSpace(stored)), &arr); err == nil {
					existingKeys = make([]string, len(arr))
					for i, v := range arr {
						existingKeys[i] = string(v)
					}
				}
			} else {
				// 换行分隔格式
				existingKeys = strings.Split(strings.Trim(stored, "\n"), "\n")
			}

			// 处理 Vertex AI 的特殊情况
			if channel.Type == constant.ChannelTypeVertexAi && channel.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey {
				// 尝试解析新密钥为JSON数组
				if strings.HasPrefix(strings.TrimSpace(channel.Key), "[") {
					array, _, err := getVertexArrayKeys(channel.Key)
					if err != nil {
						return "", common.NewMessage("Failed to parse appended keys: {{error}}", map[string]any{"error": err.Error()})
					}
					newKeys = array
				} else {
					// 单个JSON密钥
					newKeys = []string{channel.Key}
				}
			} else {
				// 普通渠道的处理
				inputKeys := strings.SplitSeq(channel.Key, "\n")
				for key := range inputKeys {
					key = strings.TrimSpace(key)
					if key != "" {
						newKeys = append(newKeys, key)
					}
				}
			}

			seen := make(map[string]bool, len(existingKeys)+len(newKeys))
			for _, key := range existingKeys {
				seen[key] = true
			}
			for _, key := range newKeys {
				if seen[key] && !model.ChannelBudgetsEnabled() {
					return "", model.ErrChannelAllocationsInactive
				}
				seen[key] = true
			}
			allKeys := append(existingKeys, newKeys...)
			return strings.Join(allKeys, "\n"), nil
		}
	}
	requestedKey, err := nextKey(originChannel.Key)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// An edit that leaves the key as it read it writes no key, so it cannot put
	// back a credential a refresh replaced meanwhile.
	keyChanged := requestedKey != "" && requestedKey != originChannel.Key
	// A type or setting left out keeps the stored one.
	keptKey, keptType, keptPluginKey := originChannel.Key, channel.Type, channel.GetSetting().TaskPluginKey
	if keyChanged {
		keptKey = requestedKey
	}
	if keptType == 0 {
		keptType = originChannel.Type
	}
	if !settingProvided {
		keptPluginKey = originChannel.GetSetting().TaskPluginKey
	}
	if err := service.CheckSubmittedPluginCredentials(keptType, keptPluginKey, keptKey, originChannel.Key); err != nil {
		common.ApiError(c, err)
		return
	}
	// validateChannel checks the plugin config of a request that names type
	// 61; settings sent without the type are checked against the kept plugin.
	if channel.Type == 0 && keptType == constant.ChannelTypeTaskPlugin && channel.OtherSettings != "" {
		if plugin, ok := jsplugin.DefaultRegistry.Get(strings.TrimSpace(keptPluginKey)); ok {
			if err := normalizePluginConfig(&channel.Channel, plugin); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	}
	if keyChanged && keptType == constant.ChannelTypeTaskPlugin {
		checked := channel.Channel
		checked.Type = keptType
		checked.SetSetting(dto.ChannelSettings{TaskPluginKey: keptPluginKey})
		if checked.OtherSettings == "" {
			checked.OtherSettings = originChannel.OtherSettings
		}
		if checked.BaseURL == nil {
			checked.BaseURL = originChannel.BaseURL
		}
		if err := checkPluginChannelKeys(c, &checked, keptKey, originChannel.Key); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	// Merge only the requested key/mode changes into the current locked state;
	// a configuration edit must not put back a stale scope, binding or status.
	// The configuration edit and the key edit commit together with the ability
	// rows and the audit obligation, so a full update is one transaction.
	var keyBefore, keyAfter string
	// auditBaseline is the locked row as committed before this edit; Apply fills it
	// from the transaction so the audit never diffs against a pre-transaction read.
	var auditBaseline *model.Channel
	modeChanged := channel.MultiKeyMode != nil && *channel.MultiKeyMode != ""
	toMultiKey := channel.ToMultiKey != nil && *channel.ToMultiKey
	// updateDraft records the change set the same way regardless of which fields
	// the request supplied; the committed row supplies the final values in Prepare.
	auditActor := snapshotChannelAuditActor(c)
	edit := model.ChannelConfigEdit{
		EditKeys: func(tx *gorm.DB, stored *model.Channel) error {
			if channel.ExpectedRevision != nil && *channel.ExpectedRevision != stored.ChannelInfo.KeyEntryRevision {
				return model.ErrChannelKeyEntryRevisionStale
			}
			if keyChanged && (channel.KeyMode == nil || *channel.KeyMode != "append") && model.ChannelHasDuplicateCredentials(stored) {
				return model.ErrChannelKeyEntriesAmbiguous
			}
			if toMultiKey && !stored.ChannelInfo.IsMultiKey {
				stored.ChannelInfo.IsMultiKey = true
				stored.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
			}
			if modeChanged {
				stored.ChannelInfo.MultiKeyMode = constant.MultiKeyMode(*channel.MultiKeyMode)
			}
			if keyChanged {
				next, err := nextKey(stored.Key)
				if err != nil {
					return err
				}
				if err := service.CheckSubmittedPluginCredentials(keptType, keptPluginKey, next, stored.Key); err != nil {
					return err
				}
				keyBefore, stored.Key = stored.Key, next
			}
			if stored.ChannelInfo.IsMultiKey {
				stored.Keys = nil
				stored.ChannelInfo.MultiKeySize = len(stored.GetKeys())
				for index := range stored.ChannelInfo.MultiKeyStatusList {
					if index >= stored.ChannelInfo.MultiKeySize {
						delete(stored.ChannelInfo.MultiKeyStatusList, index)
					}
				}
			}
			return nil
		},
		Apply: func(baseline, stored *model.Channel) error {
			if err := stored.MergeSettingPatch(channel.Setting); err != nil {
				return err
			}
			mergedSetting := stored.Setting
			applyChannelConfigEdit(&channel, stored)
			stored.Setting = mergedSetting
			// The audit baseline is the locked row before this edit, so a field
			// another writer changed in the meantime is not misreported as ours.
			auditBaseline = baseline
			return nil
		},
		Prepare: func(stored *model.Channel) (model.AuditEventDraft, error) {
			keyAfter = stored.Key
			// changed_fields is derived from the committed row, so it reports what
			// was actually saved rather than what the request intended. Returning
			// the draft is what carries it into the enqueue.
			return auditActor.draft("channel.update", updateAuditParams(stored, auditBaseline), true), nil
		},
	}
	if !keyChanged && !modeChanged && !toMultiKey {
		edit.EditKeys = nil
	}
	updated, err := model.UpdateChannelConfigAtomic(channel.Id, edit)
	if err != nil {
		if keyChanged || modeChanged || toMultiKey {
			channelKeyManageError(c, err)
			return
		}
		common.ApiError(c, err)
		return
	}
	channel.Channel = *updated
	if proxyChanged {
		service.InvalidateProxyClient(originProxy)
	}
	if keyChanged {
		service.RevokeRemovedChannelCredentials(originChannel, keyBefore, keyAfter)
	}
	// The audit event is durable in the same transaction as the edit, so the
	// middleware fallback must not add a second record for this request.
	markAuditLogged(c)
	channel.Key = ""
	clearChannelInfo(&channel.Channel)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channel,
	})
	return
}

// applyChannelConfigEdit merges the requested configuration onto the freshly read
// row. Empty scalar fields and omitted/null pointers keep the stored value;
// explicitly empty strings in pointer fields clear the stored configuration.
func applyChannelConfigEdit(request *PatchChannel, stored *model.Channel) {
	candidate := request.Channel
	if candidate.Name != "" {
		stored.Name = candidate.Name
	}
	if candidate.Models != "" {
		stored.Models = candidate.Models
	}
	if candidate.Group != "" {
		stored.Group = candidate.Group
	}
	if candidate.Type != 0 {
		stored.Type = candidate.Type
	}
	if candidate.BaseURL != nil {
		stored.BaseURL = candidate.BaseURL
	}
	if candidate.ModelMapping != nil {
		stored.ModelMapping = candidate.ModelMapping
	}
	if candidate.Other != "" {
		stored.Other = candidate.Other
	}
	if candidate.OtherSettings != "" {
		stored.OtherSettings = candidate.OtherSettings
	}
	if candidate.Setting != nil {
		stored.Setting = candidate.Setting
	}
	if candidate.Priority != nil {
		stored.Priority = candidate.Priority
	}
	if candidate.Weight != nil {
		stored.Weight = candidate.Weight
	}
	if candidate.Tag != nil {
		stored.Tag = candidate.Tag
	}
	if candidate.TestModel != nil {
		stored.TestModel = candidate.TestModel
	}
	if candidate.AutoBan != nil {
		stored.AutoBan = candidate.AutoBan
	}
	// The key-indexed scope and binding maps belong to the locked row: EditKeys has
	// already reconciled them against the authoritative allocation rows in this
	// transaction. Overwriting them from the pre-transaction origin snapshot would
	// make UpdateAbilities publish scopes that no longer match those rows.
}

// updateAuditParams derives the audit params from the committed row, so the
// recorded changed_fields describe what was actually saved. baseline is the
// locked row as read inside the transaction, before the edit was applied.
func updateAuditParams(stored, baseline *model.Channel) map[string]any {
	changedFields := make([]string, 0)
	if stored.Models != baseline.Models {
		changedFields = append(changedFields, "models")
	}
	if stored.Group != baseline.Group {
		changedFields = append(changedFields, "group")
	}
	if stored.Type != baseline.Type {
		changedFields = append(changedFields, "type")
	}
	if !equalStringPtr(stored.BaseURL, baseline.BaseURL) {
		changedFields = append(changedFields, "base_url")
	}
	if !equalStringPtr(stored.ModelMapping, baseline.ModelMapping) {
		changedFields = append(changedFields, "model_mapping")
	}
	if stored.Key != baseline.Key {
		changedFields = append(changedFields, "key")
	}
	return map[string]any{
		"id":             stored.Id,
		"name":           stored.Name,
		"changed_fields": changedFields,
	}
}

func UpdateChannelStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	req := ChannelStatusRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || !isManageableChannelStatus(req.Status) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	// One transaction commits the status, the ability rows and the sync
	// obligation, then publishes this channel locally. The former
	// UpdateChannelStatus + InitChannelCache pair reloaded the whole channel table
	// before the response returned.
	changed, err := model.UpdateChannelStatusAtomic(id, req.Status, "manual operation")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if changed && req.Status != common.ChannelStatusEnabled {
		closeActiveChannelWebSockets([]int{id})
	}
	recordManageAudit(c, "channel.status_update", map[string]any{
		"id":      id,
		"status":  req.Status,
		"changed": changed,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    changed,
	})
}

func BatchUpdateChannelStatus(c *gin.Context) {
	req := ChannelStatusBatchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Ids) == 0 || !isManageableChannelStatus(req.Status) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	changedCount := 0
	var disabledIDs []int
	for _, id := range req.Ids {
		// Each status commits with its ability rows and sync obligation, then
		// publishes that channel locally; the batch no longer reloads the whole
		// channel table before responding.
		changed, err := model.UpdateChannelStatusAtomic(id, req.Status, "manual batch operation")
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if changed {
			changedCount++
			if req.Status != common.ChannelStatusEnabled {
				disabledIDs = append(disabledIDs, id)
			}
		}
	}
	if len(disabledIDs) > 0 {
		closeActiveChannelWebSockets(disabledIDs)
	}
	recordManageAudit(c, "channel.status_update_batch", map[string]any{
		"count":  changedCount,
		"total":  len(req.Ids),
		"status": req.Status,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    changedCount,
	})
}

func isManageableChannelStatus(status int) bool {
	return status == common.ChannelStatusEnabled || status == common.ChannelStatusManuallyDisabled
}

// equalStringPtr 比较两个 *string 是否相等（均为 nil 视为相等）。
func equalStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

type fetchModelsRequest struct {
	ChannelID      int     `json:"channel_id"`
	BaseURL        *string `json:"base_url"`
	Type           int     `json:"type"`
	Key            string  `json:"key"`
	TaskPluginKey  string  `json:"task_plugin_key"`
	AdvancedCustom *string `json:"advanced_custom"`
	HeaderOverride *string `json:"header_override"`
	Proxy          *string `json:"proxy"`
}

func buildAdvancedCustomModelPreviewChannel(req fetchModelsRequest) (*model.Channel, error) {
	var channel *model.Channel
	if req.ChannelID > 0 {
		savedChannel, err := model.GetChannelById(req.ChannelID, true)
		if err != nil {
			return nil, err
		}
		if savedChannel.Type != constant.ChannelTypeAdvancedCustom {
			return nil, fmt.Errorf("channel %d is not an advanced custom channel", req.ChannelID)
		}
		channel = savedChannel
	} else {
		key := strings.TrimSpace(req.Key)
		if key != "" {
			key = strings.Split(key, "\n")[0]
		}
		channel = &model.Channel{
			Type: req.Type,
			Key:  key,
		}
	}

	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return nil, fmt.Errorf("channel type must be advanced custom")
	}
	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)
		channel.BaseURL = &baseURL
	}

	settings := channel.GetOtherSettings()
	if req.AdvancedCustom != nil {
		rawConfig := strings.TrimSpace(*req.AdvancedCustom)
		if rawConfig == "" {
			return nil, fmt.Errorf("advanced_custom is required")
		}
		var config dto.AdvancedCustomConfig
		if err := common.UnmarshalJsonStr(rawConfig, &config); err != nil {
			return nil, err
		}
		settings.AdvancedCustom = &config
	} else if req.ChannelID <= 0 {
		return nil, fmt.Errorf("advanced_custom is required")
	}
	channel.SetOtherSettings(settings)

	if req.HeaderOverride != nil {
		rawHeaderOverride := strings.TrimSpace(*req.HeaderOverride)
		if rawHeaderOverride != "" {
			var headerOverride map[string]any
			if err := common.UnmarshalJsonStr(rawHeaderOverride, &headerOverride); err != nil {
				return nil, fmt.Errorf("header_override must be a JSON object: %w", err)
			}
		}
		channel.HeaderOverride = &rawHeaderOverride
	}
	if req.Proxy != nil {
		channelSettings := channel.GetSetting()
		channelSettings.Proxy = strings.TrimSpace(*req.Proxy)
		channel.SetSetting(channelSettings)
	}

	if err := validateChannel(channel, false); err != nil {
		return nil, err
	}
	return channel, nil
}

// checkPluginChannelKeys runs the checkKey of a task plugin channel's plugin
// on the key lines an administrator typed in: those of key that storedKey
// does not hold.
func checkPluginChannelKeys(c *gin.Context, channel *model.Channel, key, storedKey string) error {
	if channel == nil || channel.Type != constant.ChannelTypeTaskPlugin {
		return nil
	}
	plugin, ok := jsplugin.DefaultRegistry.Get(channel.GetSetting().TaskPluginKey)
	if !ok {
		return nil
	}
	config := plugin.ChannelConfigFor(channel.GetOtherSettings().PluginConfig)
	return service.CheckPluginKeys(c.Request.Context(), plugin, channel.GetBaseURL(), config, key, storedKey, i18n.StatedLang(c))
}

// applyTaskPluginModelPreview sets up an unsaved task plugin channel for a
// model list preview as the Advanced Custom preview does: the plugin's
// meta.baseUrl stands in for an empty base URL, and the form's header
// override and proxy apply.
func applyTaskPluginModelPreview(channel *model.Channel, req fetchModelsRequest) error {
	pluginKey := strings.TrimSpace(req.TaskPluginKey)
	settings := dto.ChannelSettings{TaskPluginKey: pluginKey}
	if req.Proxy != nil {
		settings.Proxy = strings.TrimSpace(*req.Proxy)
	}
	channel.SetSetting(settings)
	if channel.GetBaseURL() == "" {
		if plugin, ok := jsplugin.DefaultRegistry.Get(pluginKey); ok && plugin.Meta.BaseURL != "" {
			baseURL := plugin.Meta.BaseURL
			channel.BaseURL = &baseURL
		}
	}
	if req.HeaderOverride != nil {
		rawHeaderOverride := strings.TrimSpace(*req.HeaderOverride)
		if rawHeaderOverride != "" {
			var headerOverride map[string]any
			if err := common.UnmarshalJsonStr(rawHeaderOverride, &headerOverride); err != nil {
				return fmt.Errorf("header_override must be a JSON object: %w", err)
			}
		}
		channel.HeaderOverride = &rawHeaderOverride
	}
	return nil
}

func FetchModels(c *gin.Context) {
	var req fetchModelsRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	var channel *model.Channel
	discoveryOptions := channelModelDiscoveryOptions{Preview: &req}
	if req.ChannelID > 0 {
		savedChannel, err := model.GetChannelById(req.ChannelID, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		discoveryOptions.SourceType = savedChannel.Type
		if savedChannel.Type == constant.ChannelTypeAdvancedCustom {
			channel, err = buildAdvancedCustomModelPreviewChannel(req)
			if err != nil {
				common.ApiError(c, err)
				return
			}
		} else {
			// The saved channel is the base and every field the request carries
			// replaces its saved value, so a key typed while editing is used
			// instead of the stored one. An explicit empty value clears it.
			channel = savedChannel
			if req.Type > 0 {
				channel.Type = req.Type
			}
			if req.BaseURL != nil {
				baseURL := strings.TrimSpace(*req.BaseURL)
				channel.BaseURL = &baseURL
			}
			if req.HeaderOverride != nil {
				headerOverride := strings.TrimSpace(*req.HeaderOverride)
				channel.HeaderOverride = &headerOverride
			}
			if req.Proxy != nil {
				channelSettings := channel.GetSetting()
				channelSettings.Proxy = strings.TrimSpace(*req.Proxy)
				channel.SetSetting(channelSettings)
			}
			if key := strings.TrimSpace(req.Key); key != "" {
				if channel.Type != constant.ChannelTypeCodex {
					key = strings.Split(key, "\n")[0]
				}
				channel = newSingleKeyChannel(channel, key)
			}
		}
	} else if req.Type == constant.ChannelTypeAdvancedCustom {
		var err error
		channel, err = buildAdvancedCustomModelPreviewChannel(req)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		baseURL := ""
		if req.BaseURL != nil {
			baseURL = strings.TrimSpace(*req.BaseURL)
		}
		if baseURL == "" {
			baseURL = constant.GetChannelBaseURL(req.Type)
		}

		key := strings.TrimSpace(req.Key)
		if req.Type != constant.ChannelTypeCodex {
			key = strings.Split(key, "\n")[0]
		}
		channel = &model.Channel{
			Type:    req.Type,
			Key:     key,
			BaseURL: &baseURL,
		}
		if req.HeaderOverride != nil {
			rawHeaderOverride := strings.TrimSpace(*req.HeaderOverride)
			if rawHeaderOverride != "" {
				var headerOverride map[string]any
				if err := common.UnmarshalJsonStr(rawHeaderOverride, &headerOverride); err != nil {
					common.ApiError(c, common.NewMessage("Header override must be valid JSON format"))
					return
				}
			}
			channel.HeaderOverride = &rawHeaderOverride
		}
		if req.Proxy != nil {
			channelSettings := channel.GetSetting()
			channelSettings.Proxy = strings.TrimSpace(*req.Proxy)
			channel.SetSetting(channelSettings)
		}
		if req.Type == constant.ChannelTypeTaskPlugin {
			if err := applyTaskPluginModelPreview(channel, req); err != nil {
				common.ApiError(c, err)
				return
			}
		}
	}
	models, err := fetchChannelUpstreamModelIDsForRequest(c.Request.Context(), channel, discoveryOptions)
	if err != nil {
		common.ApiErrorT(c, "Failed to fetch models: {{error}}", map[string]any{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
}

func BatchSetChannelTag(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	err = model.BatchSetChannelTag(channelBatch.Ids, channelBatch.Tag)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.tag_batch_set", map[string]any{
		"count": len(channelBatch.Ids),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    len(channelBatch.Ids),
	})
	return
}

func GetTagModels(c *gin.Context) {
	tag := c.Query("tag")
	if tag == "" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Tag cannot be empty"))
		return
	}

	channels, err := model.GetChannelsByTag(tag, false, false) // idSort=false, selectAll=false
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var longestModels string
	maxLength := 0

	// Find the longest models string among all channels with the given tag
	for _, channel := range channels {
		if channel.Models != "" {
			currentModels := strings.Split(channel.Models, ",")
			if len(currentModels) > maxLength {
				maxLength = len(currentModels)
				longestModels = channel.Models
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    longestModels,
	})
	return
}

// CopyChannel handles cloning an existing channel with its key.
// POST /api/channel/copy/:id
// Optional query params:
//
//	suffix         - string appended to the original name (default "_copy")
//	reset_balance  - bool, when true will reset balance & used_quota to 0 (default true)
func CopyChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	suffix := c.DefaultQuery("suffix", "_copy")
	resetBalance := true
	if rbStr := c.DefaultQuery("reset_balance", "true"); rbStr != "" {
		if v, err := strconv.ParseBool(rbStr); err == nil {
			resetBalance = v
		}
	}

	// fetch original channel with key
	origin, err := model.GetChannelById(id, true)
	if err != nil {
		common.SysError(common.LogText("failed to get channel by id: %s", err.Error()))
		common.ApiErrorT(c, "Failed to fetch channel. Please try again later")
		return
	}
	if (origin.Type == constant.ChannelTypeTaskPlugin || len(origin.GetSetting().TaskPluginBindings()) > 0) &&
		!authz.Can(c.GetInt("id"), c.GetInt("role"), authz.TaskPluginBind) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "task plugin channels require the task_plugin.bind permission"})
		return
	}

	// clone channel
	clone := *origin // shallow copy is sufficient as we will overwrite primitives
	clone.Id = 0     // let DB auto-generate
	clone.CreatedTime = common.GetTimestamp()
	clone.Name = origin.Name + suffix
	clone.TestTime = 0
	clone.ResponseTime = 0
	if resetBalance {
		clone.Balance = 0
		clone.UsedQuota = 0
	}
	// The per-key scopes describe the key strings, so they stay valid on the copy.
	// Copy the maps instead of sharing them with the original channel, and keep a
	// bound key bound: un-binding it would hand the user's dedicated credential to
	// every other user through the new channel.
	clone.ChannelInfo.MultiKeyStatusList = maps.Clone(origin.ChannelInfo.MultiKeyStatusList)
	clone.ChannelInfo.MultiKeyDisabledReason = maps.Clone(origin.ChannelInfo.MultiKeyDisabledReason)
	clone.ChannelInfo.MultiKeyDisabledTime = maps.Clone(origin.ChannelInfo.MultiKeyDisabledTime)
	clone.ChannelInfo.MultiKeyModels = maps.Clone(origin.ChannelInfo.MultiKeyModels)
	clone.ChannelInfo.MultiKeyModelMappings = maps.Clone(origin.ChannelInfo.MultiKeyModelMappings)
	clone.ChannelInfo.MultiKeyUsers = maps.Clone(origin.ChannelInfo.MultiKeyUsers)
	clone.ChannelInfo.MultiKeyScopeModes = maps.Clone(origin.ChannelInfo.MultiKeyScopeModes)
	service.DropPluginCredentials(&clone)

	if err := clone.ValidateSettings(); err != nil {
		common.SysError(common.LogText("failed to validate cloned channel: %s", err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "Failed to copy channel: invalid channel settings"})
		return
	}

	// insert
	if err := clone.Insert(); err != nil {
		common.SysError(common.LogText("failed to clone channel: %s", err.Error()))
		common.ApiErrorT(c, "Failed to copy channel. Please try again later")
		return
	}
	model.InitChannelCache()
	recordManageAudit(c, "channel.copy", map[string]any{
		"sourceId": id,
		"id":       clone.Id,
		"name":     clone.Name,
	})
	// success
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"id": clone.Id}})
}

// MultiKeyManageRequest represents the request for multi-key management operations
type MultiKeyManageRequest struct {
	ChannelId             int      `json:"channel_id"`
	Action                string   `json:"action"`              // "disable_key", "enable_key", "delete_key", "delete_disabled_keys", "get_key_status", "set_key_models", "set_key_user", "fetch_key_models", "sync_key_models"
	KeyIndex              *int     `json:"key_index,omitempty"` // for disable_key, enable_key, delete_key, fetch_key_models, set_key_models and set_key_user actions
	Models                []string `json:"models,omitempty"`    // for set_key_models: the models this key may serve, empty clears the limit
	ModelMapping          *string  `json:"model_mapping,omitempty"`
	UserId                *int     `json:"user_id,omitempty"`   // for set_key_user: the user this key is bound to, 0 unbinds
	Apply                 bool     `json:"apply,omitempty"`     // for sync_key_models: persist the discovered models and key scopes instead of previewing them
	Page                  int      `json:"page,omitempty"`      // for get_key_status pagination
	PageSize              int      `json:"page_size,omitempty"` // for get_key_status pagination
	Status                *int     `json:"status,omitempty"`    // for get_key_status filtering: 1=enabled, 2=manual_disabled, 3=auto_disabled, nil=all
	KeyEntryID            string   `json:"key_entry_id,omitempty"`
	ExpectedRev           *int64   `json:"expected_rev,omitempty"`
	ExpectedRevision      *int64   `json:"expected_revision,omitempty"`
	KeyEntryIDs           []string `json:"key_entry_ids,omitempty"`
	ScopeMode             *string  `json:"scope_mode,omitempty"`
	ExpectedConfigVersion *int64   `json:"expected_config_version,omitempty"`
	Enabled               *bool    `json:"enabled,omitempty"`
	LimitQuota            *int64   `json:"limit_quota,omitempty"`
	OperationID           string   `json:"operation_id,omitempty"`
	PreviewToken          string   `json:"preview_token,omitempty"`
}

// MultiKeyModelsResult reports the upstream models discovered with one key of a
// multi-key channel. Skipped marks a key that was not contacted at all.
type MultiKeyModelsResult struct {
	KeyIndex   int      `json:"key_index"`
	Models     []string `json:"models,omitempty"`
	Error      string   `json:"error,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
	KeyEntryID string   `json:"key_entry_id,omitempty"`
	Rev        int64    `json:"rev,omitempty"`
}

// multiKeyModelSyncConcurrency bounds how many keys of one channel are queried
// upstream at the same time; a channel may hold hundreds of keys.
const multiKeyModelSyncConcurrency = 4

// newSingleKeyChannel returns a copy of the channel that holds only the given
// key, so upstream discovery uses exactly that key: multi-key polling, key
// status, model scope and mapping cannot pick or filter another one. When the
// source allocation is known, its mapping override is moved to position zero;
// an ad-hoc credential has no allocation override and inherits the channel map.
// The original channel is left untouched.
func newSingleKeyChannel(channel *model.Channel, key string, sourceIndex ...int) *model.Channel {
	singleKeyChannel := *channel
	singleKeyChannel.Key = key
	singleKeyChannel.Keys = nil
	singleKeyChannel.ChannelInfo.IsMultiKey = false
	singleKeyChannel.ChannelInfo.MultiKeySize = 0
	singleKeyChannel.ChannelInfo.MultiKeyStatusList = nil
	singleKeyChannel.ChannelInfo.MultiKeyModels = nil
	singleKeyChannel.ChannelInfo.MultiKeyUsers = nil
	singleKeyChannel.ChannelInfo.MultiKeyScopeModes = nil
	if len(sourceIndex) > 0 {
		if mapping, ok := channel.ChannelInfo.MultiKeyModelMappings[sourceIndex[0]]; ok && mapping != "" {
			singleKeyChannel.ChannelInfo.MultiKeyModelMappings = map[int]string{0: mapping}
		} else {
			singleKeyChannel.ChannelInfo.MultiKeyModelMappings = nil
		}
	} else {
		singleKeyChannel.ChannelInfo.MultiKeyModelMappings = nil
	}
	return &singleKeyChannel
}

// MultiKeyStatusResponse represents the response for key status query
type MultiKeyStatusResponse struct {
	Revision       int64       `json:"revision"`
	QuotaSupported bool        `json:"quota_supported"`
	Keys           []KeyStatus `json:"keys"`
	Total          int         `json:"total"`
	Page           int         `json:"page"`
	PageSize       int         `json:"page_size"`
	TotalPages     int         `json:"total_pages"`
	// Statistics
	EnabledCount        int `json:"enabled_count"`
	ManualDisabledCount int `json:"manual_disabled_count"`
	AutoDisabledCount   int `json:"auto_disabled_count"`
}

type KeyStatus struct {
	Id           string                   `json:"id"`
	Rev          int64                    `json:"rev"`
	ScopeMode    string                   `json:"scope_mode"`
	ModelMapping string                   `json:"model_mapping"`
	Quota        *model.ChannelBudgetView `json:"quota,omitempty"`
	Index        int                      `json:"index"`
	Status       int                      `json:"status"` // 1: enabled, 2: disabled
	DisabledTime int64                    `json:"disabled_time,omitempty"`
	Reason       string                   `json:"reason,omitempty"`
	KeyPreview   string                   `json:"key_preview"`        // masked key for identification; never the full key
	Models       []string                 `json:"models,omitempty"`   // models this key may serve; empty means every channel model
	UserId       int                      `json:"user_id,omitempty"`  // user this key is bound to; 0 means a public key
	Username     string                   `json:"username,omitempty"` // display name of the bound user
}

// maskKeyPreview renders just enough of a key to tell it apart from the other
// keys of the same channel. Long sk- keys keep that prefix plus four identifying
// characters and their last four; other long keys keep four at each end. Both
// leave at least eight characters hidden. Short keys keep at most their last
// two, and keys of two characters or fewer are fully masked. Structured keys
// show only approved identifiers and never fall back to raw fragments. The result
// is only returned to the caller as key_preview, never logged or audited.
func maskKeyPreview(key string) string {
	if preview, ok := service.PluginCredentialPreview(key); ok {
		return preview
	}
	trimmed := strings.TrimSpace(key)
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]any
		if err := common.UnmarshalJsonStr(trimmed, &fields); err == nil {
			for _, field := range []string{"client_email", "project_id"} {
				if value, ok := fields[field].(string); ok && strings.TrimSpace(value) != "" {
					return truncateRunes(strings.TrimSpace(value), 40)
				}
			}
		}
		return "…"
	}
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, `"`) {
		return "…"
	}
	// Compound provider credentials use a pipe-delimited format (for example,
	// Baidu client-id|client-secret and Xunfei app-id|api-key|secret). A
	// multiline credential such as a PEM bundle is likewise not a scalar key;
	// never expose a fragment of either form in an identifying preview.
	if strings.ContainsAny(trimmed, "|\r\n") {
		return "…"
	}
	runes := []rune(trimmed)
	if strings.HasPrefix(trimmed, "sk-") {
		if len(runes) >= 19 {
			return string(runes[:7]) + "…" + string(runes[len(runes)-4:])
		}
	} else if len(runes) >= 16 {
		return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
	}
	if len(runes) <= 2 {
		return "…"
	}
	return "…" + string(runes[len(runes)-2:])
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// ManageMultiKeys handles multi-key management operations
func ManageMultiKeys(c *gin.Context) {
	request := MultiKeyManageRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if multiKeyActionRequiresSensitiveWrite(request.Action) && !requireChannelSensitiveWrite(c) {
		return
	}

	// Mutations retain the polling lock. Status reads use a fresh database
	// snapshot and must not hold this mutex through budget/name enrichment.
	lock := model.GetChannelPollingLock(request.ChannelId)
	locked := request.Action != "get_key_status"
	if locked {
		lock.Lock()
	}
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()

	var channel *model.Channel
	if request.Action == "get_key_status" {
		channel, err = model.ReadChannelKeyEntriesSnapshot(c.Request.Context(), request.ChannelId)
	} else {
		channel, err = model.GetChannelById(request.ChannelId, true)
	}
	if err != nil {
		if request.Action == "get_key_status" && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, model.ErrChannelKeyEntryNotFound) {
			channelKeyManageError(c, err)
		} else {
			common.ApiErrorT(c, "Channel does not exist")
		}
		return
	}

	if !channel.ChannelInfo.IsMultiKey {
		common.ApiErrorT(c, "This channel is not in multi-key mode")
		return
	}
	if request.Action != "get_key_status" {
		if err := model.EnsureChannelKeyEntries(channel); err != nil {
			channelKeyManageError(c, err)
			return
		}
	}
	if handleChannelKeyAllocationAction(c, channel, request) {
		return
	}
	// get_key_status 为只读查询，不记录审计；其余为修改操作，记录审计并跳过中间件兜底。
	if request.Action == "get_key_status" {
		markAuditLogged(c)
	} else {
		recordManageAudit(c, "channel.multi_key_manage", map[string]any{
			"action": request.Action,
			"id":     channel.Id,
		})
	}

	switch request.Action {
	case "get_key_status":
		keys := channel.GetKeys()

		// Default pagination parameters
		page := request.Page
		pageSize := request.PageSize
		if page <= 0 {
			page = 1
		}
		if pageSize <= 0 {
			pageSize = 50 // Default page size
		}
		pageSize = min(pageSize, 100)

		// Statistics for all keys (unchanged by filtering)
		var enabledCount, manualDisabledCount, autoDisabledCount int

		// Build all key status data first
		var allKeyStatusList []KeyStatus
		for i := range keys {
			status := 1 // default enabled
			var disabledTime int64
			var reason string

			if channel.ChannelInfo.MultiKeyStatusList != nil {
				if s, exists := channel.ChannelInfo.MultiKeyStatusList[i]; exists {
					status = s
				}
			}

			// Count for statistics (all keys)
			switch status {
			case 1:
				enabledCount++
			case 2:
				manualDisabledCount++
			case 3:
				autoDisabledCount++
			}

			if status != 1 {
				if channel.ChannelInfo.MultiKeyDisabledTime != nil {
					disabledTime = channel.ChannelInfo.MultiKeyDisabledTime[i]
				}
				if channel.ChannelInfo.MultiKeyDisabledReason != nil {
					reason = channel.ChannelInfo.MultiKeyDisabledReason[i]
				}
			}

			scopeMode := "all"
			if channel.ChannelInfo.MultiKeyScopeModes[i] == "allowlist" {
				scopeMode = "allowlist"
			}
			allKeyStatusList = append(allKeyStatusList, KeyStatus{
				Id:           strconv.FormatInt(channel.ChannelKeyEntryID(i), 10),
				Rev:          channel.KeyEntryRevisions[i],
				ScopeMode:    scopeMode,
				ModelMapping: channel.ChannelInfo.MultiKeyModelMappings[i],
				Index:        i,
				Status:       status,
				DisabledTime: disabledTime,
				Reason:       reason,
				Models:       channel.ChannelInfo.MultiKeyModels[i],
				UserId:       channel.ChannelInfo.MultiKeyUsers[i],
			})
		}

		// Apply status filter if specified
		var filteredKeyStatusList []KeyStatus
		if request.Status != nil {
			for _, keyStatus := range allKeyStatusList {
				if keyStatus.Status == *request.Status {
					filteredKeyStatusList = append(filteredKeyStatusList, keyStatus)
				}
			}
		} else {
			filteredKeyStatusList = allKeyStatusList
		}

		// Calculate pagination based on filtered results
		filteredTotal := len(filteredKeyStatusList)
		totalPages := (filteredTotal + pageSize - 1) / pageSize
		if totalPages == 0 {
			totalPages = 1
		}
		if page > totalPages {
			page = totalPages
		}

		// Calculate range for current page
		start := (page - 1) * pageSize
		end := min(start+pageSize, filteredTotal)

		// Get the page data
		var pageKeyStatusList []KeyStatus
		if start < filteredTotal {
			pageKeyStatusList = filteredKeyStatusList[start:end]
		}
		allocationIDs := make([]int64, 0, len(pageKeyStatusList))
		userIDs := make([]int, 0, len(pageKeyStatusList))
		for i := range pageKeyStatusList {
			keyStatus := &pageKeyStatusList[i]
			allocationIDs = append(allocationIDs, channel.ChannelKeyEntryID(keyStatus.Index))
			if keyStatus.UserId > 0 && !slices.Contains(userIDs, keyStatus.UserId) {
				userIDs = append(userIDs, keyStatus.UserId)
			}
			keyStatus.KeyPreview = maskKeyPreview(keys[keyStatus.Index])
		}
		quotaViews, quotaSupported, err := model.ReadChannelBudgetViewsWithActivation(c.Request.Context(), model.BudgetScopeKey, allocationIDs)
		if err != nil {
			channelKeyManageError(c, err)
			return
		}
		// Display names remain best-effort, as before; missing users or a name
		// lookup failure must not discard authoritative allocation/budget data.
		usernames, _ := model.GetUsernamesByIDs(c.Request.Context(), userIDs)
		if err := c.Request.Context().Err(); err != nil {
			channelKeyManageError(c, err)
			return
		}
		for i := range pageKeyStatusList {
			keyStatus := &pageKeyStatusList[i]
			keyStatus.Quota = quotaViews[channel.ChannelKeyEntryID(keyStatus.Index)]
			keyStatus.Username = usernames[keyStatus.UserId]
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": MultiKeyStatusResponse{
				Revision:            channel.ChannelInfo.KeyEntryRevision,
				QuotaSupported:      quotaSupported,
				Keys:                pageKeyStatusList,
				Total:               filteredTotal, // Total of filtered results
				Page:                page,
				PageSize:            pageSize,
				TotalPages:          totalPages,
				EnabledCount:        enabledCount,        // Overall statistics
				ManualDisabledCount: manualDisabledCount, // Overall statistics
				AutoDisabledCount:   autoDisabledCount,   // Overall statistics
			},
		})
		return

	case "disable_key":
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to disable")
			return
		}

		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= channel.ChannelInfo.MultiKeySize {
			common.ApiErrorT(c, "Key index out of range")
			return
		}

		shouldCloseWebSocket := false
		err = saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
			if stored.ChannelInfo.MultiKeyStatusList == nil {
				stored.ChannelInfo.MultiKeyStatusList = make(map[int]int)
			}
			if stored.ChannelInfo.MultiKeyDisabledTime == nil {
				stored.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			if stored.ChannelInfo.MultiKeyDisabledReason == nil {
				stored.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}

			stored.ChannelInfo.MultiKeyStatusList[keyIndex] = 2 // disabled

			shouldCloseWebSocket = disableMultiKeyChannelIfUnavailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if shouldCloseWebSocket {
			closeActiveChannelWebSockets([]int{channel.Id})
		}
		common.ApiSuccessT(c, "Key disabled", nil)
		return

	case "enable_key":
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to enable")
			return
		}

		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= channel.ChannelInfo.MultiKeySize {
			common.ApiErrorT(c, "Key index out of range")
			return
		}

		err = saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
			// 从状态列表中删除该密钥的记录，使其回到默认启用状态
			delete(stored.ChannelInfo.MultiKeyStatusList, keyIndex)
			delete(stored.ChannelInfo.MultiKeyDisabledTime, keyIndex)
			delete(stored.ChannelInfo.MultiKeyDisabledReason, keyIndex)
			restoreMultiKeyChannelIfAvailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}

		common.ApiSuccessT(c, "Key enabled", nil)
		return

	case "enable_all_keys":
		// 清空所有禁用状态，使所有密钥回到默认启用状态
		var enabledCount int
		err = saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
			enabledCount = len(stored.ChannelInfo.MultiKeyStatusList)
			stored.ChannelInfo.MultiKeyStatusList = make(map[int]int)
			stored.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			stored.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			restoreMultiKeyChannelIfAvailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}

		common.ApiSuccessT(c, "Enabled {{count}} keys", nil, map[string]any{"count": enabledCount})
		return

	case "disable_all_keys":
		// 禁用所有启用的密钥
		var disabledCount int
		shouldCloseWebSocket := false
		err = saveManagedKeyStatus(channel.Id, func(stored *model.Channel) error {
			if stored.ChannelInfo.MultiKeyStatusList == nil {
				stored.ChannelInfo.MultiKeyStatusList = make(map[int]int)
			}
			if stored.ChannelInfo.MultiKeyDisabledTime == nil {
				stored.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			if stored.ChannelInfo.MultiKeyDisabledReason == nil {
				stored.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}

			disabledCount = 0
			for i := range stored.ChannelInfo.MultiKeySize {
				status := 1 // default enabled
				if s, exists := stored.ChannelInfo.MultiKeyStatusList[i]; exists {
					status = s
				}

				// 只禁用当前启用的密钥
				if status == 1 {
					stored.ChannelInfo.MultiKeyStatusList[i] = 2 // disabled
					disabledCount++
				}
			}

			if disabledCount == 0 {
				return common.NewMessage("No keys to disable")
			}
			shouldCloseWebSocket = disableMultiKeyChannelIfUnavailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if shouldCloseWebSocket {
			closeActiveChannelWebSockets([]int{channel.Id})
		}
		common.ApiSuccessT(c, "Disabled {{count}} keys", nil, map[string]any{"count": disabledCount})
		return

	case "delete_key":
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to delete")
			return
		}

		keyIndex := *request.KeyIndex
		// The keys change under the channel's key lock, from the stored ones: a
		// refresh may have replaced a credential since channel was read.
		var keyBefore string
		shouldCloseWebSocket := false
		stored, err := model.UpdateChannelLocked(channel.Id, func(stored *model.Channel) error {
			if keyIndex < 0 || keyIndex >= stored.ChannelInfo.MultiKeySize {
				return common.NewMessage("Key index out of range")
			}

			keyBefore = stored.Key
			keys := stored.GetKeys()
			var remainingKeys []string
			var newStatusList = make(map[int]int)
			var newDisabledTime = make(map[int]int64)
			var newDisabledReason = make(map[int]string)

			newIndex := 0
			for i, key := range keys {
				// 跳过要删除的密钥
				if i == keyIndex {
					continue
				}

				remainingKeys = append(remainingKeys, key)

				// 保留其他密钥的状态信息，重新索引
				if stored.ChannelInfo.MultiKeyStatusList != nil {
					if status, exists := stored.ChannelInfo.MultiKeyStatusList[i]; exists && status != 1 {
						newStatusList[newIndex] = status
					}
				}
				if stored.ChannelInfo.MultiKeyDisabledTime != nil {
					if t, exists := stored.ChannelInfo.MultiKeyDisabledTime[i]; exists {
						newDisabledTime[newIndex] = t
					}
				}
				if stored.ChannelInfo.MultiKeyDisabledReason != nil {
					if r, exists := stored.ChannelInfo.MultiKeyDisabledReason[i]; exists {
						newDisabledReason[newIndex] = r
					}
				}
				newIndex++
			}

			if len(remainingKeys) == 0 {
				return common.NewMessage("Cannot delete the last key")
			}

			// Update channel with remaining keys
			stored.Key = strings.Join(remainingKeys, "\n")
			stored.ChannelInfo.MultiKeySize = len(remainingKeys)
			stored.ChannelInfo.MultiKeyStatusList = newStatusList
			stored.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
			stored.ChannelInfo.MultiKeyDisabledReason = newDisabledReason

			shouldCloseWebSocket = disableMultiKeyChannelIfUnavailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.RefreshChannelRouting(channel.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		service.RevokeRemovedChannelCredentials(stored, keyBefore, stored.Key)
		if shouldCloseWebSocket {
			closeActiveChannelWebSockets([]int{channel.Id})
		}
		common.ApiSuccessT(c, "Key deleted", nil)
		return

	case "delete_disabled_keys":
		// As delete_key, from the stored keys under the channel's key lock.
		var keyBefore string
		var deletedCount int
		shouldCloseWebSocket := false
		stored, err := model.UpdateChannelLocked(channel.Id, func(stored *model.Channel) error {
			keyBefore = stored.Key
			keys := stored.GetKeys()
			var remainingKeys []string
			var newStatusList = make(map[int]int)
			var newDisabledTime = make(map[int]int64)
			var newDisabledReason = make(map[int]string)

			newIndex := 0
			for i, key := range keys {
				status := 1 // default enabled
				if stored.ChannelInfo.MultiKeyStatusList != nil {
					if s, exists := stored.ChannelInfo.MultiKeyStatusList[i]; exists {
						status = s
					}
				}

				// 只删除自动禁用（status == 3）的密钥，保留启用（status == 1）和手动禁用（status == 2）的密钥
				if status == 3 {
					deletedCount++
				} else {
					remainingKeys = append(remainingKeys, key)
					// 保留非自动禁用密钥的状态信息，重新索引
					if status != 1 {
						newStatusList[newIndex] = status
						if stored.ChannelInfo.MultiKeyDisabledTime != nil {
							if t, exists := stored.ChannelInfo.MultiKeyDisabledTime[i]; exists {
								newDisabledTime[newIndex] = t
							}
						}
						if stored.ChannelInfo.MultiKeyDisabledReason != nil {
							if r, exists := stored.ChannelInfo.MultiKeyDisabledReason[i]; exists {
								newDisabledReason[newIndex] = r
							}
						}
					}
					newIndex++
				}
			}

			if deletedCount == 0 {
				return common.NewMessage("No auto-disabled keys to delete")
			}

			// Update channel with remaining keys
			stored.Key = strings.Join(remainingKeys, "\n")
			stored.ChannelInfo.MultiKeySize = len(remainingKeys)
			stored.ChannelInfo.MultiKeyStatusList = newStatusList
			stored.ChannelInfo.MultiKeyDisabledTime = newDisabledTime
			stored.ChannelInfo.MultiKeyDisabledReason = newDisabledReason

			shouldCloseWebSocket = disableMultiKeyChannelIfUnavailable(stored)
			return nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.RefreshChannelRouting(channel.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		service.RevokeRemovedChannelCredentials(stored, keyBefore, stored.Key)
		if shouldCloseWebSocket {
			closeActiveChannelWebSockets([]int{channel.Id})
		}
		common.ApiSuccessT(c, "Deleted {{count}} auto-disabled keys", deletedCount, map[string]any{"count": deletedCount})
		return

	case "set_key_models":
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to set models")
			return
		}
		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= channel.ChannelInfo.MultiKeySize {
			common.ApiErrorT(c, "Key index out of range")
			return
		}
		channelModels := channel.GetModels()
		scope := make([]string, 0, len(request.Models))
		for _, modelName := range request.Models {
			modelName = strings.TrimSpace(modelName)
			if modelName == "" || slices.Contains(scope, modelName) {
				continue
			}
			if !slices.Contains(channelModels, modelName) {
				common.ApiErrorT(c, "Model {{model}} is not served by this channel", map[string]any{"model": modelName})
				return
			}
			scope = append(scope, modelName)
		}
		if len(scope) == 0 {
			delete(channel.ChannelInfo.MultiKeyModels, keyIndex)
		} else {
			if channel.ChannelInfo.MultiKeyModels == nil {
				channel.ChannelInfo.MultiKeyModels = make(map[int][]string)
			}
			channel.ChannelInfo.MultiKeyModels[keyIndex] = scope
		}
		err = channel.Update()
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.RefreshChannelRouting(channel.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccessT(c, "Key models saved", nil)
		return

	case "set_key_user":
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to bind")
			return
		}
		keyIndex := *request.KeyIndex
		if keyIndex < 0 || keyIndex >= channel.ChannelInfo.MultiKeySize {
			common.ApiErrorT(c, "Key index out of range")
			return
		}
		if request.UserId == nil || *request.UserId < 0 {
			common.ApiErrorT(c, "No user specified to bind")
			return
		}
		userId := *request.UserId
		if userId == 0 {
			delete(channel.ChannelInfo.MultiKeyUsers, keyIndex)
		} else {
			if _, err := model.GetUserById(userId, false); err != nil {
				common.ApiErrorT(c, "User does not exist")
				return
			}
			if channel.ChannelInfo.MultiKeyUsers == nil {
				channel.ChannelInfo.MultiKeyUsers = make(map[int]int)
			}
			channel.ChannelInfo.MultiKeyUsers[keyIndex] = userId
		}
		err = channel.Update()
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if err := model.RefreshChannelRouting(channel.Id); err != nil {
			common.ApiError(c, err)
			return
		}
		// A key that just became exclusive to one user must not keep serving the
		// sessions other users already opened on it.
		closeActiveChannelWebSockets([]int{channel.Id})
		if userId == 0 {
			common.ApiSuccessT(c, "Key unbound", nil)
		} else {
			common.ApiSuccessT(c, "Key bound to user", nil)
		}
		return

	case "fetch_key_models":
		lock.Unlock()
		locked = false
		if request.KeyEntryID != "" {
			entries := make([]model.ChannelKeyEntry, len(channel.KeyEntryIDs))
			for i, id := range channel.KeyEntryIDs {
				entries[i] = model.ChannelKeyEntry{Id: id, Key: channel.GetKeys()[i], ConfigRevision: channel.KeyEntryRevisions[i]}
			}
			index, err := channelKeyRequestIndex(request, entries)
			if err != nil {
				channelKeyManageError(c, err)
				return
			}
			request.KeyIndex = &index
		} else if model.ChannelHasDuplicateCredentials(channel) {
			channelKeyManageError(c, model.ErrChannelKeyEntriesAmbiguous)
			return
		}
		if request.KeyIndex == nil {
			common.ApiErrorT(c, "No key index specified to fetch models")
			return
		}
		keyIndex := *request.KeyIndex
		keys := channel.GetKeys()
		if keyIndex < 0 || keyIndex >= len(keys) {
			common.ApiErrorT(c, "Key index out of range")
			return
		}
		models, err := fetchChannelUpstreamModelIDsForRequest(c.Request.Context(), channel, channelModelDiscoveryOptions{KeyIndex: &keyIndex})
		if err != nil {
			common.ApiErrorT(c, "Failed to fetch models: {{error}}", map[string]any{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    gin.H{"models": models},
		})
		return

	case "sync_key_models":
		lock.Unlock()
		locked = false
		if request.Apply && model.ChannelHasDuplicateCredentials(channel) && (request.ExpectedRevision == nil || *request.ExpectedRevision != channel.ChannelInfo.KeyEntryRevision) {
			channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
			return
		}
		keys := channel.GetKeys()
		results := make([]MultiKeyModelsResult, len(keys))
		var wg sync.WaitGroup
		capacity := make(chan struct{}, multiKeyModelSyncConcurrency)
		for keyIndex := range keys {
			results[keyIndex] = MultiKeyModelsResult{KeyIndex: keyIndex, KeyEntryID: strconv.FormatInt(channel.ChannelKeyEntryID(keyIndex), 10), Rev: channel.KeyEntryRevisions[keyIndex]}
			// A disabled key is reported without contacting the upstream: the
			// operator sees it was left alone, and a failing credential is not
			// exercised again.
			if status, ok := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; ok && status != common.ChannelStatusEnabled {
				results[keyIndex].Skipped = true
				continue
			}
			wg.Go(func() {
				select {
				case capacity <- struct{}{}:
					defer func() { <-capacity }()
				case <-c.Request.Context().Done():
					results[keyIndex].Error = c.Request.Context().Err().Error()
					return
				}
				models, err := fetchChannelUpstreamModelIDsForRequest(c.Request.Context(), channel, channelModelDiscoveryOptions{KeyIndex: &keyIndex})
				if err != nil {
					results[keyIndex].Error = err.Error()
					return
				}
				results[keyIndex].Models = models
			})
		}
		wg.Wait()
		if err := c.Request.Context().Err(); err != nil {
			common.ApiError(c, err)
			return
		}

		channelModels := normalizeModelNames(channel.GetModels())
		discovered := make([]string, 0, len(results))
		for _, result := range results {
			if result.Error != "" || len(result.Models) == 0 {
				continue
			}
			discovered = append(discovered, result.Models...)
		}
		addedModels := subtractModelNames(normalizeModelNames(discovered), channelModels)

		response := gin.H{
			"results":        results,
			"added_models":   addedModels,
			"applied":        false,
			"channel_models": channelModels,
			"revision":       channel.ChannelInfo.KeyEntryRevision,
		}
		if !request.Apply {
			c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": response})
			return
		}

		source := *channel
		channel.ChannelInfo.MultiKeyModels = maps.Clone(channel.ChannelInfo.MultiKeyModels)
		channel.Models = strings.Join(mergeModelNames(channelModels, addedModels), ",")
		for _, result := range results {
			if result.Error != "" || len(result.Models) == 0 {
				continue
			}
			if channel.ChannelInfo.MultiKeyModels == nil {
				channel.ChannelInfo.MultiKeyModels = make(map[int][]string)
			}
			channel.ChannelInfo.MultiKeyModels[result.KeyIndex] = result.Models
		}
		// Prune before persisting: a scope may name models that the channel list
		// lost while this key's upstream no longer serves them.
		nextModels := normalizeModelNames(channel.GetModels())
		for keyIndex, scope := range channel.ChannelInfo.MultiKeyModels {
			scope = intersectModelNames(scope, nextModels)
			if len(scope) == 0 {
				delete(channel.ChannelInfo.MultiKeyModels, keyIndex)
				continue
			}
			channel.ChannelInfo.MultiKeyModels[keyIndex] = scope
		}
		persistCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		expected := int64(0) // unique legacy requests still validate each sampled entry
		if request.ExpectedRevision != nil {
			expected = *request.ExpectedRevision
		}
		_, err = model.MutateChannelKeyEntries(persistCtx, channel.Id, expected, func(_ *gorm.DB, current *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
			if !sameChannelModelUpdateSource(&source, current) {
				return nil, nil, model.ErrChannelKeyEntryRevisionStale
			}
			current.Models = channel.Models
			for _, result := range results {
				if result.Error != "" || result.Skipped || len(result.Models) == 0 {
					continue
				}
				for i := range entries {
					if strconv.FormatInt(entries[i].Id, 10) == result.KeyEntryID {
						if entries[i].ConfigRevision != result.Rev {
							return nil, nil, model.ErrChannelKeyEntryRevisionStale
						}
						if err := entries[i].SetKeyModels(intersectModelNames(result.Models, normalizeModelNames(current.GetModels()))); err != nil {
							return nil, nil, err
						}
						entries[i].ScopeMode = "allowlist"
						entries[i].ConfigRevision++
					}
				}
			}
			return entries, nil, nil
		})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		// MutateChannelKeyEntries commits abilities and the sync obligation, then
		// publishes the authoritative channel snapshot locally before returning.
		// Re-reading the row and rebuilding its abilities here duplicated work and
		// used to trigger a full channel-cache reload.
		response["applied"] = true
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": response})
		return

	default:
		common.ApiErrorT(c, "Unsupported operation")
		return
	}
}

func multiKeyActionRequiresSensitiveWrite(action string) bool {
	// Binding a key to a user and narrowing a key's models change who may reach
	// the channel, so they need the same permission as deleting a key. Applying
	// an upstream model sync changes the channel model list the same way.
	switch action {
	case "delete_key", "delete_disabled_keys", "set_key_models", "set_key_model_mapping", "set_key_user", "sync_key_models", "set_key_quota", "reset_key_quota", "deduplicate_keys", "reorder_keys":
		return true
	default:
		return false
	}
}

// ollamaManagementOptions leaves admission to the HTTP helper. Its reload
// rebuilds static headers and key/transport policy after every grant, including
// a cap change, without ever borrowing another channel or copying browser headers.
func ollamaManagementOptions(ctx context.Context, channel *model.Channel, admission *service.ChannelDiagnosticAdmission) ollama.OllamaRequestOptions {
	return ollama.OllamaRequestOptions{
		Header: make(http.Header), Settings: channel.GetSetting(),
		ChannelID: channel.Id, Admission: admission,
		Reload: func() (string, http.Header, dto.ChannelSettings, error) {
			var current model.Channel
			if err := model.DB.WithContext(ctx).First(&current, channel.Id).Error; err != nil {
				return "", nil, dto.ChannelSettings{}, err
			}
			if current.Type != constant.ChannelTypeOllama {
				return "", nil, dto.ChannelSettings{}, common.NewMessage("This operation is only supported for Ollama channels")
			}
			key, _, apiErr := current.GetNextEnabledKey()
			if apiErr != nil {
				return "", nil, dto.ChannelSettings{}, common.NewMessage("No enabled channel key")
			}
			key = strings.TrimSpace(key)
			headers, err := buildFetchModelsHeaders(&current, key)
			if err != nil {
				return "", nil, dto.ChannelSettings{}, common.NewMessage("Invalid channel header override")
			}
			if (key == "" || key == "EMPTY") && headers.Get("Authorization") == "Bearer "+key {
				headers.Del("Authorization")
			}
			return channelBaseURL(&current), headers, current.GetSetting(), nil
		},
	}
}

// OllamaPullModel 拉取 Ollama 模型
func OllamaPullModel(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.GetChannelBaseURL(channel.Type)
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	err = ollama.PullOllamaModel(ctx, baseURL, "", req.ModelName, ollamaManagementOptions(ctx, channel, admission))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to pull model: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s pulled successfully", req.ModelName),
	})
}

// OllamaPullModelStream 流式拉取 Ollama 模型
func OllamaPullModelStream(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.GetChannelBaseURL(channel.Type)
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Hour)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()

	// Console administration does not grant wildcard CORS access.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	progressCallback := func(progress ollama.OllamaPullResponse) {
		if ctx.Err() != nil {
			return
		}
		data, _ := common.Marshal(progress)
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
			cancel()
			return
		}
		c.Writer.Flush()
	}

	err = ollama.PullOllamaModelStream(ctx, baseURL, "", req.ModelName, ollamaManagementOptions(ctx, channel, admission), progressCallback)
	if ctx.Err() != nil {
		return
	}

	if err != nil {
		errorData, _ := common.Marshal(gin.H{
			"error": err.Error(),
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(errorData))
	} else {
		successData, _ := common.Marshal(gin.H{
			"message": fmt.Sprintf("Model %s pulled successfully", req.ModelName),
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(successData))
	}

	// 发送结束标志
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

// OllamaDeleteModel 删除 Ollama 模型
func OllamaDeleteModel(c *gin.Context) {
	var req struct {
		ChannelID int    `json:"channel_id"`
		ModelName string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
		})
		return
	}

	if req.ChannelID == 0 || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Channel ID and model name are required",
		})
		return
	}

	// 获取渠道信息
	channel, err := model.GetChannelById(req.ChannelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	// 检查是否是 Ollama 渠道
	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.GetChannelBaseURL(channel.Type)
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	err = ollama.DeleteOllamaModel(ctx, baseURL, "", req.ModelName, ollamaManagementOptions(ctx, channel, admission))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": fmt.Sprintf("Failed to delete model: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("Model %s deleted successfully", req.ModelName),
	})
}

// OllamaVersion 获取 Ollama 服务版本信息
func OllamaVersion(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid channel id",
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Channel not found",
		})
		return
	}

	if channel.Type != constant.ChannelTypeOllama {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "This operation is only supported for Ollama channels",
		})
		return
	}

	baseURL := constant.GetChannelBaseURL(channel.Type)
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	version, err := ollama.FetchOllamaVersion(ctx, baseURL, "", ollamaManagementOptions(ctx, channel, admission))
	if err != nil {
		common.ApiErrorT(c, "Failed to fetch Ollama version: {{error}}", map[string]any{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"version": version,
		},
	})
}
