package model

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

// The user-bound channel index answers "which channels carry a key exclusive to
// this user". Bindings live in the channel_info JSON column, so the index is
// derived from channel rows in Go and never from dialect-specific JSON SQL.
var userBoundChannelsLock sync.RWMutex
var userBoundChannels map[int][]int // user id -> channel ids carrying a bound key

// refreshUserBoundChannelIndex rebuilds the bound-user index from the database for
// the no-memory-cache mode. It takes the same generation guard as a full reload so
// it cannot publish bindings that a targeted publication already superseded.
// Targeted publications update the index per channel, so this full rebuild is only
// a fallback for callers that need to resynchronize the whole index.
func refreshUserBoundChannelIndex() {
	if DB == nil {
		return
	}
	generation := ChannelCacheGeneration()
	var channels []*Channel
	ctx := WithBatchedChannelKeyEntries(context.Background())
	if err := DB.WithContext(ctx).Find(&channels).Error; err != nil {
		common.SysLog(common.LogText("failed to load channels for the user bound index: %v", err))
		return
	}
	if err := ProjectChannelKeyEntries(DB, ctx, channels, false); err != nil {
		common.SysLog(common.LogText("failed to project channel allocations for the user bound index: %v", err))
		return
	}
	pending := buildUserBoundChannelIndex(channels)
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if ChannelCacheGeneration() != generation {
		common.SysLog(common.LogText("discarded a stale user bound index refresh"))
		return
	}
	publishUserBoundChannelIndex(pending)
}

// userBoundChannelIDs lists the channels carrying a key bound to userId. The
// index is built by InitChannelCache, which every channel mutation runs, so the
// request path never queries the database for it.
func userBoundChannelIDs(userId int) []int {
	userBoundChannelsLock.RLock()
	defer userBoundChannelsLock.RUnlock()
	return userBoundChannels[userId]
}

// UserBoundChannelIDs returns the channel ids currently indexed as carrying a key
// bound to userId. It exposes the published index, so a caller can observe the
// effect of a cache publication without a second database read.
func UserBoundChannelIDs(userId int) []int {
	ids := userBoundChannelIDs(userId)
	if len(ids) == 0 {
		return nil
	}
	return append([]int(nil), ids...)
}

// GetUserBoundChannel returns an enabled channel where a key bound to userId can
// serve modelName, or nil when the request keeps its ordinary group routing.
// Bound channels outside the user's group are reachable here, and channels whose
// bound keys cannot serve the model are skipped.
func GetUserBoundChannel(userId int, modelName string) *Channel {
	if userId <= 0 || modelName == "" {
		return nil
	}
	candidates := make([]*Channel, 0, 1)
	for _, id := range userBoundChannelIDs(userId) {
		channel, err := CacheGetChannel(id)
		if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if !slices.Contains(channel.UserBoundModels(userId), modelName) {
			continue
		}
		if channel.ModelUnavailableForServingKeys(modelName, userId) {
			continue
		}
		candidates = append(candidates, channel)
	}
	if len(candidates) == 0 {
		return nil
	}
	priority := candidates[0].GetPriority()
	for _, channel := range candidates {
		priority = max(priority, channel.GetPriority())
	}
	targets := make([]*Channel, 0, len(candidates))
	weight := 0
	for _, channel := range candidates {
		if channel.GetPriority() == priority {
			targets = append(targets, channel)
			weight += channel.GetWeight()
		}
	}
	selected, err := chooseWeightedChannel(targets, weight)
	if err != nil {
		return nil
	}
	return selected
}

// GetUserBoundModels lists the models userId reaches through keys bound to them,
// across every enabled channel. The model list adds these to the group models.
func GetUserBoundModels(userId int) []string {
	if userId <= 0 {
		return nil
	}
	models := make([]string, 0)
	seen := make(map[string]struct{})
	for _, id := range userBoundChannelIDs(userId) {
		channel, err := CacheGetChannel(id)
		if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		for _, modelName := range channel.UserBoundModels(userId) {
			if _, duplicate := seen[modelName]; duplicate {
				continue
			}
			seen[modelName] = struct{}{}
			models = append(models, modelName)
		}
	}
	return models
}

// channelCacheReloadLock runs one channel cache reload at a time, so a reload
// that read the database earlier never publishes over a later one.
var channelCacheReloadLock sync.Mutex

func InitChannelCache() {
	channelCacheReloadLock.Lock()
	defer channelCacheReloadLock.Unlock()
	if !common.MemoryCacheEnabled {
		// There is no routing snapshot in this mode, but the bound-user index is
		// still shared state read by the request path. The helper takes the same
		// generation guard as the routing branch, so a reload that read the database
		// before a targeted publication advanced it does not publish older bindings.
		refreshUserBoundChannelIndex()
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	// Capture the routing generation before reading. A targeted publication that
	// commits while this reload is reading advances it, and this reload must not
	// publish a snapshot that predates that newer write.
	generation := ChannelCacheGeneration()
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	var channels []*Channel
	// The full reload is the busiest bulk read, so it hydrates allocations in
	// bounded chunks instead of one query per migrated channel.
	if err := DB.WithContext(WithBatchedChannelKeyEntries(context.Background())).Find(&channels).Error; err != nil {
		common.SysLog(common.LogText("failed to load channels for the cache: %s", err.Error()))
		return
	}
	if err := ProjectChannelKeyEntries(DB, context.Background(), channels, false); err != nil {
		common.SysLog(common.LogText("failed to project channel allocations for the cache: %s", err.Error()))
		return
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		// Only public keys serve a whole group; group model whitelists narrow
		// the set further. Both rules match the abilities generated for the
		// channel, so the cache and the database agree on what a group reaches.
		publicModels := channel.PublicModels()
		if len(publicModels) == 0 {
			continue
		}
		groupModels := channel.GetOtherSettings().GroupModels
		groups := strings.SplitSeq(channel.Group, ",")
		for group := range groups {
			models := filterGroupModels(publicModels, groupModels[group])
			if len(models) == 0 {
				continue
			}
			model2channels, exists := newGroup2model2channels[group]
			if !exists {
				model2channels = make(map[string][]int)
				newGroup2model2channels[group] = model2channels
			}
			for _, model := range models {
				model2channels[model] = append(model2channels[model], channel.Id)
			}
		}
	}
	// Deliberately not published yet: a reload rejected by the generation check
	// below must not overwrite a newer targeted publication's binding index.
	pendingBoundIndex := buildUserBoundChannelIndex(channels)

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	if ChannelCacheGeneration() != generation {
		// A targeted publication advanced the routing snapshot while this reload
		// was reading, so its rows are already older than what is published.
		// Discard the stale snapshot instead of resurrecting replaced routes.
		channelSyncLock.Unlock()
		common.SysLog(common.LogText("discarded a stale channel cache reload"))
		return
	}
	// Publish the binding index with the routing table under the same lock, so the
	// two derived views can never disagree about which channels are bound.
	publishUserBoundChannelIndex(pendingBoundIndex)
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog(common.LogText("channels synced from database"))
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog(common.LogText("syncing channels from database"))
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var sumWeight = 0
	var targetChannels []*Channel
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				sumWeight += channel.GetWeight()
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	return chooseWeightedChannel(targetChannels, sumWeight)
}

// chooseWeightedChannel preserves the existing weight smoothing for routing.
func chooseWeightedChannel(targetChannels []*Channel, sumWeight int) (*Channel, error) {
	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, channel := range targetChannels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d no longer exists", id)
	}
	return &c.ChannelInfo, nil
}

// CacheRefreshChannelModelUnavailability reloads only observations, never a
// stale key/status/scope snapshot. Serializing with full reloads prevents an
// earlier reload from publishing over this committed observation or restore.
func CacheRefreshChannelModelUnavailability(id int) error {
	if !common.MemoryCacheEnabled {
		return nil
	}
	channelCacheReloadLock.Lock()
	defer channelCacheReloadLock.Unlock()
	var stored Channel
	if err := DB.Select("channel_info").First(&stored, "id = ?", id).Error; err != nil {
		return err
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if cached, ok := channelsIDM[id]; ok {
		updated := *cached
		updated.ChannelInfo.ModelUnavailable = stored.ChannelInfo.ModelUnavailable
		channelsIDM[id] = &updated
	}
	return nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	cacheUpdateChannelStatusLocked(id, status)
}

// cacheUpdateChannelStatusLocked publishes status and routing together. The
// caller must hold channelSyncLock, including any current-identity check.
func cacheUpdateChannelStatusLocked(id int, status int) {
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Update the bound index for this channel only, while channelSyncLock is still
	// held: the reload branches take the same lock and compare the generation before
	// publishing, so releasing it here would let an older reload slot in a
	// superseded index.
	publishUserBoundChannelIndexForChannel(channel)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
