package model

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Channel mutations publish their routing effect directly instead of rebuilding
// the whole channel table. One transaction covers the channel row, its ability
// rows, its allocation rows and its audit obligation, so a committed write is
// always fully visible locally and can never lose its audit event.
//
// Publication coordinates the three derived indexes the full reload owns:
// routing (group2model2channels), the channel map (channelsIDM) with its
// advanced-custom configs, and the user-bound channel index. A full reload that
// began reading before a targeted publication must never publish over it, so
// both take channelSyncLock and compare one shared generation counter.

// channelCacheGenerationLock guards channelCacheGeneration.
var channelCacheGenerationLock sync.Mutex

// channelCacheGeneration advances on every successful routing publication.
var channelCacheGeneration int64

// BumpChannelCacheGeneration marks that a targeted publication changed the
// channel routing snapshot.
func BumpChannelCacheGeneration() {
	channelCacheGenerationLock.Lock()
	channelCacheGeneration++
	channelCacheGenerationLock.Unlock()
}

// ChannelCacheGeneration reports the current routing generation.
func ChannelCacheGeneration() int64 {
	channelCacheGenerationLock.Lock()
	defer channelCacheGenerationLock.Unlock()
	return channelCacheGeneration
}

// buildUserBoundChannelIndex derives the bound-user index without publishing it,
// so a caller can decide whether to publish before mutating shared state.
func buildUserBoundChannelIndex(channels []*Channel) map[int][]int {
	index := make(map[int][]int)
	for _, channel := range channels {
		if !channel.ChannelInfo.IsMultiKey {
			continue
		}
		bound := make(map[int]struct{}, len(channel.ChannelInfo.MultiKeyUsers))
		for _, userId := range channel.ChannelInfo.MultiKeyUsers {
			if userId <= 0 {
				continue
			}
			if _, duplicate := bound[userId]; duplicate {
				continue
			}
			bound[userId] = struct{}{}
			index[userId] = append(index[userId], channel.Id)
		}
	}
	for _, ids := range index {
		slices.Sort(ids)
	}
	return index
}

// publishUserBoundChannelIndex installs a complete index. The caller holds
// channelSyncLock, so an older snapshot cannot interleave a newer publication.
func publishUserBoundChannelIndex(index map[int][]int) {
	userBoundChannelsLock.Lock()
	userBoundChannels = index
	userBoundChannelsLock.Unlock()
}

// loadChannelRoutingSnapshot reads one channel with its allocations projected on
// the caller's transaction, so the published row is the committed row rather than
// a post-commit re-read that could fail or race.
func loadChannelRoutingSnapshot(tx *gorm.DB, channelID int) (*Channel, error) {
	// The projection is a plain allocation scan; discard its logger so it cannot
	// leak credential values through a slow-query trace. The batched marker makes
	// AfterFind defer to the explicit projection below instead of running its own
	// per-row read, which would otherwise fetch the same allocations twice.
	ctx := WithBatchedChannelKeyEntries(context.Background())
	scope := tx.Session(&gorm.Session{Logger: logger.Discard, Context: ctx})
	var channel Channel
	if err := scope.Where("id = ?", channelID).First(&channel).Error; err != nil {
		return nil, err
	}
	if err := ProjectChannelKeyEntries(scope, ctx, []*Channel{&channel}, false); err != nil {
		return nil, err
	}
	return &channel, nil
}

// publishChannelLocked installs one channel's routing, channel-map entry and
// derived config from an already projected row. The caller holds channelSyncLock.
func publishChannelLocked(channel *Channel) {
	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if group2model2channels == nil {
		group2model2channels = make(map[string]map[string][]int)
	}
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	if channel.ChannelInfo.IsMultiKey {
		channel.Keys = channel.GetKeys()
		// A full reload preserves the live polling cursor when the stored row is
		// still a polling multi-key channel; a targeted publication must not
		// silently reset a cursor the router is actively advancing.
		if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
			if previous, ok := channelsIDM[channel.Id]; ok &&
				previous.ChannelInfo.IsMultiKey &&
				previous.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				channel.ChannelInfo.MultiKeyPollingIndex = previous.ChannelInfo.MultiKeyPollingIndex
			}
		}
	}
	channelsIDM[channel.Id] = channel
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	purgeChannelRoutingLocked(channel.Id)
	if channel.Status != common.ChannelStatusEnabled {
		return
	}
	publicModels := channel.PublicModels()
	groupModels := channel.GetOtherSettings().GroupModels
	for group := range strings.SplitSeq(channel.Group, ",") {
		models := filterGroupModels(publicModels, groupModels[group])
		if len(models) == 0 {
			continue
		}
		model2channels, exists := group2model2channels[group]
		if !exists {
			model2channels = make(map[string][]int)
			group2model2channels[group] = model2channels
		}
		for _, modelName := range models {
			model2channels[modelName] = append(model2channels[modelName], channel.Id)
			// Keep the priority ordering the full reload publishes, so routing
			// selection is identical after a targeted write.
			ordered := model2channels[modelName]
			sort.SliceStable(ordered, func(i, j int) bool {
				left, right := channelsIDM[ordered[i]], channelsIDM[ordered[j]]
				if left == nil || right == nil {
					return false
				}
				return left.GetPriority() > right.GetPriority()
			})
			model2channels[modelName] = ordered
		}
	}
}

// purgeChannelRoutingLocked drops every routing entry of one channel. The caller
// must hold channelSyncLock.
func purgeChannelRoutingLocked(id int) {
	for group, model2channels := range group2model2channels {
		for modelName, channelIDs := range model2channels {
			kept := channelIDs[:0]
			for _, channelID := range channelIDs {
				if channelID != id {
					kept = append(kept, channelID)
				}
			}
			if len(kept) == 0 {
				delete(model2channels, modelName)
				continue
			}
			model2channels[modelName] = kept
		}
		if len(model2channels) == 0 {
			delete(group2model2channels, group)
		}
	}
}

// publishUserBoundChannelIndexForChannel updates the bound-user index for exactly
// one channel, in place, instead of rebuilding it from the whole channel map. The
// caller holds channelSyncLock, so no reload can interleave a superseded index.
func publishUserBoundChannelIndexForChannel(channel *Channel) {
	userBoundChannelsLock.Lock()
	defer userBoundChannelsLock.Unlock()
	if userBoundChannels == nil {
		userBoundChannels = make(map[int][]int)
	}
	// Drop this channel from every user it was bound to before re-adding it, so a
	// removed binding or a deleted channel disappears from the index.
	for userId, ids := range userBoundChannels {
		kept := ids[:0]
		for _, channelID := range ids {
			if channelID != channel.Id {
				kept = append(kept, channelID)
			}
		}
		if len(kept) == 0 {
			delete(userBoundChannels, userId)
			continue
		}
		userBoundChannels[userId] = kept
	}
	if channel == nil || !channel.ChannelInfo.IsMultiKey {
		return
	}
	seen := make(map[int]struct{}, len(channel.ChannelInfo.MultiKeyUsers))
	for _, userId := range channel.ChannelInfo.MultiKeyUsers {
		if userId <= 0 {
			continue
		}
		if _, duplicate := seen[userId]; duplicate {
			continue
		}
		seen[userId] = struct{}{}
		ids := append(userBoundChannels[userId], channel.Id)
		slices.Sort(ids)
		userBoundChannels[userId] = ids
	}
}

// removeChannelFromUserBoundIndex drops one channel from the index. The caller
// holds channelSyncLock.
func removeChannelFromUserBoundIndex(id int) {
	userBoundChannelsLock.Lock()
	defer userBoundChannelsLock.Unlock()
	for userId, ids := range userBoundChannels {
		kept := ids[:0]
		for _, channelID := range ids {
			if channelID != id {
				kept = append(kept, channelID)
			}
		}
		if len(kept) == 0 {
			delete(userBoundChannels, userId)
			continue
		}
		userBoundChannels[userId] = kept
	}
}

// publishChannelSnapshot installs a committed row and updates the bound-user index
// for just that channel, under one lock, then advances the generation so an
// in-flight full reload cannot publish older rows afterwards.
func publishChannelSnapshot(channel *Channel) {
	if !common.MemoryCacheEnabled {
		// There is no routing snapshot, but the bound-user index still backs
		// request-path lookups. Publish it for this channel under the lock and guard
		// it with the generation, so a concurrent reload cannot replace it with a
		// snapshot that predates this commit.
		channelSyncLock.Lock()
		publishUserBoundChannelIndexForChannel(channel)
		BumpChannelCacheGeneration()
		channelSyncLock.Unlock()
		// The pricing cache and the task alias view are derived from the channel
		// rows in this mode, so a changed channel invalidates them exactly as the
		// full reload does. Both run outside channelSyncLock: InvalidatePricingCache
		// takes updatePricingLock, which nests channelSyncLock.RLock, so holding it
		// here would be an AB-BA deadlock.
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	channelSyncLock.Lock()
	publishChannelLocked(channel)
	publishUserBoundChannelIndexForChannel(channel)
	BumpChannelCacheGeneration()
	channelSyncLock.Unlock()
	InvalidatePricingCache()
	rebuildTaskAliasView()
}

// BatchChannelPublication carries the caller's in-transaction hook and audit
// obligation for a batch channel insert.
type BatchChannelPublication struct {
	// Audit is the secret-free audit obligation committed with the rows. An empty
	// EventId records no audit here, which keeps pre-existing callers working.
	Audit AuditEventDraft
}

// BatchInsertChannelsWithAudit inserts every channel, its allocations, its
// ability rows and one audit obligation in a single transaction, then publishes
// each created channel's routing locally.
func BatchInsertChannelsWithAudit(channels []Channel, publication BatchChannelPublication) error {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()
	// Insert in bounded chunks. The rows are inserted as pointer slices so GORM
	// fills each generated ID on the caller's own elements; a value chunk would
	// only populate a copy and the caller would never see the new IDs.
	for start := 0; start < len(channels); start += 50 {
		end := min(start+50, len(channels))
		chunk := make([]*Channel, 0, end-start)
		for i := start; i < end; i++ {
			chunk = append(chunk, &channels[i])
		}
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for _, chunkChannel := range chunk {
			chunkChannel.ChannelInfo.KeyEntryRevision = 0
			if err := CreateChannelKeyEntries(tx, chunkChannel); err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Model(&Channel{}).Where("id = ?", chunkChannel.Id).UpdateColumn("channel_info", chunkChannel.ChannelInfo).Error; err != nil {
				tx.Rollback()
				return err
			}
			if err := chunkChannel.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	if publication.Audit.EventId != "" {
		if _, err := EnqueueAuditEventTx(tx, publication.Audit); err != nil {
			tx.Rollback()
			return err
		}
	}
	// The committed rows are read back through the projection inside the same
	// transaction, so publication never depends on a post-commit read.
	committed := make([]*Channel, 0, len(channels))
	for i := range channels {
		snapshot, err := loadChannelRoutingSnapshot(tx, channels[i].Id)
		if err != nil {
			tx.Rollback()
			return err
		}
		committed = append(committed, snapshot)
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	// Publish the whole batch once. Publishing row by row would rebuild the task
	// alias view and the bound index per row, turning a multi-create into
	// O(rows) full channel-table scans.
	publishChannelBatch(committed)
	BumpSyncTopic(SyncTopicChannels)
	return nil
}

// publishChannelBatch installs several committed rows under one lock, updates the
// bound index once for the batch, and advances the generation once.
func publishChannelBatch(channels []*Channel) {
	if len(channels) == 0 {
		return
	}
	if !common.MemoryCacheEnabled {
		// No routing snapshot, but the bound index still backs request-path
		// lookups. Update it for the batch and guard it with the generation.
		channelSyncLock.Lock()
		for _, channel := range channels {
			publishUserBoundChannelIndexForChannel(channel)
		}
		BumpChannelCacheGeneration()
		channelSyncLock.Unlock()
		// Same derived caches as the single-channel path, invalidated once for the
		// batch and outside channelSyncLock (lock-order requirement).
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	channelSyncLock.Lock()
	for _, channel := range channels {
		publishChannelLocked(channel)
		publishUserBoundChannelIndexForChannel(channel)
	}
	BumpChannelCacheGeneration()
	channelSyncLock.Unlock()
	InvalidatePricingCache()
	rebuildTaskAliasView()
}

// ChannelConfigEdit collects the configuration a channel edit changes and the
// optional key/mode edit reconciled against the authoritative allocation rows in
// the same transaction.
type ChannelConfigEdit struct {
	// Apply writes the requested configuration columns onto the freshly read row.
	// It runs inside the transaction after the row is locked, so a stale request
	// snapshot cannot put back a key or scope another writer replaced. baseline is
	// the same locked row before the edit was applied, for changed-field reporting.
	Apply func(baseline *Channel, stored *Channel) error
	// EditKeys is the caller's mutation of the locked row. The key/allocation
	// reconciliation itself is always reconcileLockedKeyEdit, so the atomic edit
	// cannot drift from UpdateChannelLocked's invariants.
	EditKeys func(tx *gorm.DB, stored *Channel) error
	// Audit is the secret-free audit obligation committed with the edit.
	Audit AuditEventDraft
	// Prepare runs after the row is saved and before commit, with the committed
	// row, and returns the audit obligation to enqueue. Returning the draft is
	// what makes the enqueue see the committed state: the caller's copy of this
	// struct is never written back.
	Prepare func(stored *Channel) (AuditEventDraft, error)
}

// UpdateChannelConfigAtomic applies a configuration edit and an optional locked
// key edit with the ability rows and the audit obligation in one transaction,
// then publishes the committed routing locally.
func UpdateChannelConfigAtomic(channelID int, edit ChannelConfigEdit) (*Channel, error) {
	if channelID <= 0 {
		return nil, ErrChannelKeyEntryNotFound
	}
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()
	var committed *Channel
	var keyChanged bool
	err := ChannelBudgetTransaction(context.Background(), channelID, func(tx *gorm.DB) error {
		var stored Channel
		if err := tx.First(&stored, channelID).Error; err != nil {
			return err
		}
		// baseline keeps the locked row as committed before this edit, so a
		// changed-field report never compares against a stale pre-transaction read.
		baseline := stored
		updates, changedKey, _, _, err := reconcileLockedKeyEdit(tx, &stored, func(channel *Channel) error {
			if edit.EditKeys != nil {
				if err := edit.EditKeys(tx, channel); err != nil {
					return err
				}
			}
			if edit.Apply == nil {
				return nil
			}
			return edit.Apply(&baseline, channel)
		})
		if err != nil {
			return err
		}
		keyChanged = changedKey
		if edit.Apply != nil {
			// stored now carries both the requested configuration and the reconciled
			// key projection, so writing it keeps the row, the allocation rows and the
			// ability rows describing the same set of keys and scopes.
			if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(&stored).Error; err != nil {
				return err
			}
		} else if len(updates) > 0 {
			if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := stored.UpdateAbilities(tx); err != nil {
			return err
		}
		audit := edit.Audit
		if edit.Prepare != nil {
			prepared, err := edit.Prepare(&stored)
			if err != nil {
				return err
			}
			audit = prepared
		}
		if audit.EventId != "" {
			if _, err := EnqueueAuditEventTx(tx, audit); err != nil {
				return err
			}
		}
		// Read the committed row back through the projection inside this same
		// transaction: publication must not depend on a later read that can fail.
		snapshot, err := loadChannelRoutingSnapshot(tx, channelID)
		if err != nil {
			return err
		}
		committed = snapshot
		return nil
	})
	if err != nil {
		return nil, err
	}
	if keyChanged {
		CacheUpdateChannelKey(channelID, committed.Key)
	}
	publishChannelSnapshot(committed)
	BumpSyncTopic(SyncTopicChannels)
	return committed, nil
}

// CacheRemoveChannel unpublishes one channel from the routing snapshot: it can no
// longer be routed to and no longer appears in the channel map.
func CacheRemoveChannel(id int) {
	channelSyncLock.Lock()
	delete(channelsIDM, id)
	delete(channel2advancedCustomConfig, id)
	purgeChannelRoutingLocked(id)
	removeChannelFromUserBoundIndex(id)
	BumpChannelCacheGeneration()
	channelSyncLock.Unlock()
	// Both derived caches depend on the channel rows, with or without the routing
	// snapshot, so a removal invalidates them exactly as the full reload does. They
	// must run outside channelSyncLock: InvalidatePricingCache takes updatePricingLock,
	// which nests channelSyncLock.RLock, so holding it here would be an AB-BA
	// deadlock.
	InvalidatePricingCache()
	rebuildTaskAliasView()
}
