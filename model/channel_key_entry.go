package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ChannelKeyEntry is authoritative after the channel's allocation migration.
// A credential is not an allocation identity; duplicate static credentials have
// distinct rows, permissions, revisions and retained budget history.
type ChannelKeyEntry struct {
	Id             int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	ChannelId      int    `json:"channel_id" gorm:"not null;index:idx_channel_key_entries_order,priority:1"`
	Position       int    `json:"position" gorm:"not null;index:idx_channel_key_entries_order,priority:2"`
	Key            string `json:"-" gorm:"type:text;not null"`
	CredentialRef  string `json:"-" gorm:"type:varchar(255);not null;default:''"`
	BoundUserId    int    `json:"bound_user_id" gorm:"not null;default:0"`
	ScopeMode      string `json:"scope_mode" gorm:"type:varchar(16);not null;default:'all'"`
	Models         string `json:"-" gorm:"type:text;not null"`
	Status         int    `json:"status" gorm:"not null;default:1"`
	DisabledReason string `json:"disabled_reason,omitempty" gorm:"type:text"`
	DisabledTime   int64  `json:"disabled_time,omitempty" gorm:"not null;default:0"`
	ConfigRevision int64  `json:"config_revision" gorm:"not null;default:1"`
	RetiredAt      int64  `json:"retired_at" gorm:"not null;default:0"`
	MergedIntoId   int64  `json:"merged_into_id,string" gorm:"not null;default:0"`
	CreatedTime    int64  `json:"created_time" gorm:"not null;default:0"`
	UpdatedTime    int64  `json:"updated_time" gorm:"not null;default:0"`
}

func (ChannelKeyEntry) TableName() string { return "channel_key_entries" }

const ChannelKeyAllocationTable = "channel_key_entries"

var (
	ErrChannelKeyEntriesAmbiguous   = errors.New("channel key entries are ambiguous for this edit")
	ErrChannelKeyEntryNotFound      = errors.New("channel key allocation not found")
	ErrChannelKeyEntryRevisionStale = errors.New("channel key allocation revision is stale")
	ErrChannelAllocationsInactive   = errors.New("channel allocation budgets are not activated")
)

func (entry *ChannelKeyEntry) KeyModels() []string {
	var result []string
	if entry.Models != "" && common.UnmarshalJsonStr(entry.Models, &result) != nil {
		return []string{}
	}
	return result
}
func (entry *ChannelKeyEntry) SetKeyModels(models []string) error {
	normalized := make([]string, 0, len(models))
	for _, name := range models {
		name = strings.TrimSpace(name)
		if name != "" && !slices.Contains(normalized, name) {
			normalized = append(normalized, name)
		}
	}
	slices.Sort(normalized)
	encoded, err := common.Marshal(normalized)
	if err != nil {
		return err
	}
	entry.Models = string(encoded)
	entry.ScopeMode = "all"
	if len(normalized) > 0 {
		entry.ScopeMode = "allowlist"
	}
	return nil
}
func credentialRefFor(key string) string {
	if credential, ok := jsplugin.ParseCredential(strings.TrimSpace(key)); ok && credential.AccountID != "" {
		return credential.Plugin + ":" + credential.AccountID
	}
	return ""
}
func allocationKeyList(channel *Channel) []string {
	if !channel.ChannelInfo.IsMultiKey {
		if channel.Key == "" {
			return nil
		}
		return []string{channel.Key}
	}
	return channel.GetKeys()
}
func ChannelHasDuplicateCredentials(channel *Channel) bool {
	seen := map[string]bool{}
	for _, key := range allocationKeyList(channel) {
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}
func hasPrefixKeys(submitted, stored []string) bool {
	return len(submitted) >= len(stored) && slices.Equal(submitted[:len(stored)], stored)
}
func channelInfoChanged(before []byte, after ChannelInfo) bool {
	next, err := common.Marshal(after)
	return err != nil || string(next) != string(before)
}
func nextKeyEntryRevision(revision int64) int64 { return revision + 1 }
func formatKeyEntryID(id int64) string          { return strconv.FormatInt(id, 10) }
func (channel *Channel) ChannelKeyEntryIDsAsStrings() []string {
	ids := make([]string, len(channel.KeyEntryIDs))
	for i, id := range channel.KeyEntryIDs {
		ids[i] = formatKeyEntryID(id)
	}
	return ids
}
func entryIDsOf(entries []ChannelKeyEntry) []int64 {
	ids := make([]int64, len(entries))
	for i := range entries {
		ids[i] = entries[i].Id
	}
	return ids
}
func entryRevisionsOf(entries []ChannelKeyEntry) []int64 {
	ids := make([]int64, len(entries))
	for i := range entries {
		ids[i] = entries[i].ConfigRevision
	}
	return ids
}
func loadActiveChannelKeyEntries(tx *gorm.DB, channelID int) ([]ChannelKeyEntry, error) {
	var entries []ChannelKeyEntry
	err := tx.Where("channel_id = ? AND retired_at = 0", channelID).Order("position, id").Find(&entries).Error
	return entries, err
}
func ChannelKeyEntryByID(channelID int, id int64) (*ChannelKeyEntry, error) {
	var entry ChannelKeyEntry
	if err := DB.Where("channel_id = ? AND id = ? AND retired_at = 0", channelID, id).First(&entry).Error; err != nil {
		return nil, ErrChannelKeyEntryNotFound
	}
	return &entry, nil
}

// applyKeyEntryProjection never changes a revision or writes the database.
func (channel *Channel) applyKeyEntryProjection(entries []ChannelKeyEntry) error {
	keys := make([]string, 0, len(entries))
	info := &channel.ChannelInfo
	info.MultiKeyModels = nil
	info.MultiKeyScopeModes = nil
	info.MultiKeyUsers = nil
	info.MultiKeyStatusList = nil
	info.MultiKeyDisabledReason = nil
	info.MultiKeyDisabledTime = nil
	for i := range entries {
		entry := &entries[i]
		keys = append(keys, entry.Key)
		if entry.ScopeMode == "allowlist" {
			if info.MultiKeyScopeModes == nil {
				info.MultiKeyScopeModes = map[int]string{}
			}
			info.MultiKeyScopeModes[i] = "allowlist"
		}
		if models := entry.KeyModels(); len(models) > 0 {
			if info.MultiKeyModels == nil {
				info.MultiKeyModels = map[int][]string{}
			}
			info.MultiKeyModels[i] = models
		}
		if entry.BoundUserId > 0 {
			if info.MultiKeyUsers == nil {
				info.MultiKeyUsers = map[int]int{}
			}
			info.MultiKeyUsers[i] = entry.BoundUserId
		}
		if entry.Status != common.ChannelStatusEnabled {
			if info.MultiKeyStatusList == nil {
				info.MultiKeyStatusList = map[int]int{}
			}
			info.MultiKeyStatusList[i] = entry.Status
			if entry.DisabledReason != "" {
				if info.MultiKeyDisabledReason == nil {
					info.MultiKeyDisabledReason = map[int]string{}
				}
				info.MultiKeyDisabledReason[i] = entry.DisabledReason
			}
			if entry.DisabledTime != 0 {
				if info.MultiKeyDisabledTime == nil {
					info.MultiKeyDisabledTime = map[int]int64{}
				}
				info.MultiKeyDisabledTime[i] = entry.DisabledTime
			}
		}
	}
	channel.Key = ""
	if info.IsMultiKey {
		channel.Key = strings.Join(keys, "\n")
		info.MultiKeySize = len(keys)
	} else if len(keys) > 0 {
		channel.Key = keys[0]
	}
	channel.Keys = nil
	channel.KeyEntryIDs = entryIDsOf(entries)
	channel.KeyEntryRevisions = entryRevisionsOf(entries)
	channel.KeyEntryIDStrings = channel.ChannelKeyEntryIDsAsStrings()
	if info.MultiKeyPollingIndex >= len(keys) {
		info.MultiKeyPollingIndex = 0
	}
	return nil
}

// AfterFind derives the legacy projection from rows, rather than trusting a
// stale compatibility JSON document. A zero marker is a pre-migration row.
func (channel *Channel) AfterFind(tx *gorm.DB) error {
	if channel.ChannelInfo.KeyEntryRevision == 0 {
		return nil
	}
	entries, err := loadActiveChannelKeyEntries(tx.Session(&gorm.Session{NewDB: true, Logger: logger.Discard}), channel.Id)
	if err != nil {
		return err
	}
	if err := channel.applyKeyEntryProjection(entries); err != nil {
		return err
	}
	if slices.Contains(tx.Statement.Omits, "key") {
		channel.Key = ""
	}
	return nil
}
func AttachChannelKeyEntries(channels []*Channel) { // compatibility caller; AfterFind owns hydration
	for _, channel := range channels {
		if channel != nil {
			channel.KeyEntryIDStrings = channel.ChannelKeyEntryIDsAsStrings()
		}
	}
}
func syncKeyEntryAttributesFromProjection(entries []ChannelKeyEntry, channel *Channel) bool {
	changed := false
	for i := range entries {
		entry := &entries[i]
		before := *entry
		entry.BoundUserId = channel.ChannelInfo.MultiKeyUsers[i]
		_ = entry.SetKeyModels(channel.ChannelInfo.MultiKeyModels[i])
		if mode := channel.ChannelInfo.MultiKeyScopeModes[i]; mode != "" {
			entry.ScopeMode = mode
		}
		entry.Status = common.ChannelStatusEnabled
		if value, ok := channel.ChannelInfo.MultiKeyStatusList[i]; ok {
			entry.Status = value
		}
		entry.DisabledReason = channel.ChannelInfo.MultiKeyDisabledReason[i]
		entry.DisabledTime = channel.ChannelInfo.MultiKeyDisabledTime[i]
		if entry.Status == common.ChannelStatusEnabled {
			entry.DisabledReason = ""
			entry.DisabledTime = 0
		}
		if !reflect.DeepEqual(before, *entry) {
			entry.ConfigRevision++
			changed = true
		}
	}
	return changed
}
func persistKeyEntries(tx *gorm.DB, channel *Channel, entries, retired []ChannelKeyEntry) error {
	now := common.GetTimestamp()
	managed := map[string]bool{}
	for i := range entries {
		entry := &entries[i]
		entry.ChannelId = channel.Id
		entry.Position = i
		entry.UpdatedTime = now
		if entry.ScopeMode == "" {
			entry.ScopeMode = "all"
		}
		if entry.Models == "" {
			entry.Models = "[]"
		}
		if entry.ScopeMode != "all" && entry.ScopeMode != "allowlist" {
			return errors.New("invalid channel key model scope")
		}
		// Legacy bulk creation can retain an empty middle line. Keep its
		// position for index-based metadata and never choose it for a request.
		if strings.TrimSpace(entry.Key) == "" && channel.ChannelInfo.IsMultiKey {
			entry.Status = common.ChannelStatusManuallyDisabled
		}
		entry.CredentialRef = credentialRefFor(entry.Key)
		if entry.CredentialRef != "" {
			if managed[entry.CredentialRef] {
				return errors.New("managed credential allocations must be unique")
			}
			managed[entry.CredentialRef] = true
		}
		if entry.Id == 0 {
			entry.CreatedTime = now
			entry.ConfigRevision = 1
			if entry.Status == 0 {
				entry.Status = common.ChannelStatusEnabled
			}
			if err := tx.Create(entry).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&ChannelKeyEntry{}).Where("id = ? AND channel_id = ? AND retired_at = 0", entry.Id, channel.Id).Select("position", "key", "credential_ref", "bound_user_id", "scope_mode", "models", "status", "disabled_reason", "disabled_time", "config_revision", "updated_time").Updates(entry).Error; err != nil {
				return err
			}
		}
	}
	for _, entry := range retired {
		if entry.Id > 0 {
			if err := tx.Model(&ChannelKeyEntry{}).Where("id = ? AND channel_id = ? AND retired_at = 0", entry.Id, channel.Id).Updates(map[string]any{"retired_at": now, "merged_into_id": entry.MergedIntoId, "config_revision": entry.ConfigRevision + 1, "updated_time": now}).Error; err != nil {
				return err
			}
		}
	}
	channel.ChannelInfo.KeyEntryRevision++
	return channel.applyKeyEntryProjection(entries)
}

type KeyEdit struct{ Replace bool }

// planKeyEdit accepts provable append/unique legacy edits only. Identical text
// occurrences are never reassigned to another surviving allocation.
func planKeyEdit(entries []ChannelKeyEntry, submitted []string, edit KeyEdit) ([]ChannelKeyEntry, []ChannelKeyEntry, error) {
	old := make([]string, len(entries))
	for i := range entries {
		old[i] = entries[i].Key
	}
	if slices.Equal(old, submitted) {
		return entries, nil, nil
	}
	if !edit.Replace && hasPrefixKeys(submitted, old) {
		result := slices.Clone(entries)
		for _, key := range submitted[len(old):] {
			result = append(result, ChannelKeyEntry{Key: key, Status: common.ChannelStatusEnabled})
		}
		return result, nil, nil
	}
	// A plugin may refresh one credential beside identical static keys. Its
	// account identity and unchanged neighbors prove which allocation changed.
	if len(entries) == len(submitted) {
		rotated := slices.Clone(entries)
		preservesPositions := true
		for i, key := range submitted {
			if key == old[i] {
				continue
			}
			if ref := credentialRefFor(old[i]); ref == "" || ref != credentialRefFor(key) {
				preservesPositions = false
				break
			}
			rotated[i].Key = key
			rotated[i].ConfigRevision++
		}
		if preservesPositions {
			return rotated, nil, nil
		}
	}
	counts := map[string]int{}
	for _, key := range old {
		counts[key]++
		if counts[key] > 1 {
			return nil, nil, ErrChannelKeyEntriesAmbiguous
		}
	}
	counts = map[string]int{}
	for _, key := range submitted {
		counts[key]++
		if counts[key] > 1 {
			return nil, nil, ErrChannelKeyEntriesAmbiguous
		}
	}
	if len(entries) == 1 && len(submitted) == 1 {
		entry := entries[0]
		entry.Key = submitted[0]
		entry.ConfigRevision++
		return []ChannelKeyEntry{entry}, nil, nil
	}
	byKey := map[string]ChannelKeyEntry{}
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	result := make([]ChannelKeyEntry, 0, len(submitted))
	for _, key := range submitted {
		if entry, ok := byKey[key]; ok {
			result = append(result, entry)
			delete(byKey, key)
		} else {
			result = append(result, ChannelKeyEntry{Key: key, Status: common.ChannelStatusEnabled})
		}
	}
	var retired []ChannelKeyEntry
	for _, entry := range entries {
		if _, ok := byKey[entry.Key]; ok {
			retired = append(retired, entry)
		}
	}
	return result, retired, nil
}

// This pre-rollout fallback consults the same transaction, never global DB:
// a single-connection pool must not deadlock while checking schema readiness.
func requireInactiveAllocationSchemaTx(tx *gorm.DB) error {
	var marker Option
	err := tx.Where(map[string]any{"key": "internal.channel_budget.active_v1"}).First(&marker).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if marker.Value == "1" || common.GetEnvOrDefaultBool("CHANNEL_BUDGET_ENABLED", false) {
		return errors.New("channel allocation schema is unavailable")
	}
	return nil
}
func CreateChannelKeyEntries(tx *gorm.DB, channel *Channel) error {
	if channel.ChannelInfo.KeyEntryRevision == 0 && !tx.Migrator().HasTable(&ChannelKeyEntry{}) {
		return requireInactiveAllocationSchemaTx(tx)

	}
	if channel.ChannelInfo.KeyEntryRevision > 0 {
		return nil
	}
	entries := make([]ChannelKeyEntry, 0)
	for i, key := range allocationKeyList(channel) {
		status := common.ChannelStatusEnabled
		if value, ok := channel.ChannelInfo.MultiKeyStatusList[i]; ok {
			status = value
		}
		entry := ChannelKeyEntry{Key: key, BoundUserId: channel.ChannelInfo.MultiKeyUsers[i], Status: status, DisabledReason: channel.ChannelInfo.MultiKeyDisabledReason[i], DisabledTime: channel.ChannelInfo.MultiKeyDisabledTime[i]}
		if err := entry.SetKeyModels(channel.ChannelInfo.MultiKeyModels[i]); err != nil {
			return err
		}
		if mode := channel.ChannelInfo.MultiKeyScopeModes[i]; mode != "" {
			entry.ScopeMode = mode
		}
		entries = append(entries, entry)
	}
	return persistKeyEntries(tx, channel, entries, nil)
}
func EnsureChannelKeyEntries(channel *Channel) error {
	if channel == nil || channel.Id <= 0 {
		return ErrChannelKeyEntryNotFound
	}
	var updated Channel
	err := ChannelBudgetTransaction(context.Background(), channel.Id, func(tx *gorm.DB) error {
		if err := tx.First(&updated, channel.Id).Error; err != nil {
			return err
		}
		if updated.ChannelInfo.KeyEntryRevision > 0 {
			return nil
		}
		if err := CreateChannelKeyEntries(tx, &updated); err != nil {
			return err
		}
		return tx.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumn("channel_info", updated.ChannelInfo).Error
	})
	if err == nil {
		*channel = updated
	}
	return err
}
func MigrateChannelKeyEntries() error {
	var channels []*Channel
	if err := DB.Session(&gorm.Session{Logger: logger.Discard}).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if err := EnsureChannelKeyEntries(channel); err != nil {
			return fmt.Errorf("migrate allocations for channel %d: %w", channel.Id, err)
		}
	}
	return nil
}
func RetireChannelKeyEntries(tx *gorm.DB, channelID int) error {
	if !tx.Migrator().HasTable(&ChannelKeyEntry{}) {
		return requireInactiveAllocationSchemaTx(tx)
	}
	return tx.Model(&ChannelKeyEntry{}).Where("channel_id = ? AND retired_at = 0", channelID).Updates(map[string]any{"retired_at": common.GetTimestamp(), "config_revision": gorm.Expr("config_revision + 1")}).Error
}
func (channel *Channel) ChannelKeyEntryID(position int) int64 {
	if position < 0 || position >= len(channel.KeyEntryIDs) {
		return 0
	}
	return channel.KeyEntryIDs[position]
}
func (channel *Channel) ChannelKeyEntryIDsByPosition() []int64 {
	return slices.Clone(channel.KeyEntryIDs)
}

type ChannelAllocation struct {
	ChannelID     int
	EntryID       int64
	Position      int
	Key           string
	CredentialRef string
	UserID        int
	Revision      int64
}

func (channel *Channel) ChannelAllocationByPosition(position int) (ChannelAllocation, bool) {
	keys := allocationKeyList(channel)
	if position < 0 || position >= len(keys) || position >= len(channel.KeyEntryRevisions) {
		return ChannelAllocation{}, false
	}
	return ChannelAllocation{ChannelID: channel.Id, EntryID: channel.KeyEntryIDs[position], Position: position, Key: keys[position], CredentialRef: credentialRefFor(keys[position]), UserID: channel.ChannelInfo.MultiKeyUsers[position], Revision: channel.KeyEntryRevisions[position]}, true
}

// UpdateChannelAllocationStatus attributes delayed health evidence only to the
// exact selected allocation and revision. Reordering does not change identity.
func UpdateChannelAllocationStatus(ctx context.Context, selected ChannelAllocation, status int, reason string) bool {
	return UpdateChannelAllocationStatusChecked(ctx, selected, status, reason, nil)
}
func UpdateChannelAllocationStatusChecked(ctx context.Context, selected ChannelAllocation, status int, reason string, check func(*Channel) bool) bool {
	changed := false
	_, err := MutateChannelKeyEntries(ctx, selected.ChannelID, 0, func(_ *gorm.DB, current *Channel, entries []ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error) {
		if check != nil && !check(current) {
			return nil, nil, nil
		}
		for i := range entries {
			entry := &entries[i]
			if entry.Id != selected.EntryID {
				continue
			}
			if entry.Key != selected.Key || entry.ConfigRevision != selected.Revision || current.Status == common.ChannelStatusManuallyDisabled {
				return nil, nil, nil
			}
			if !current.ChannelInfo.IsMultiKey {
				if current.Status == status {
					return nil, nil, nil
				}
				current.Status = status
				other := current.GetOtherInfo()
				other["status_reason"] = reason
				other["status_time"] = common.GetTimestamp()
				current.SetOtherInfo(other)
				entry.ConfigRevision++
				changed = true
				return entries, nil, nil
			}
			if status == common.ChannelStatusAutoDisabled && entry.Status == common.ChannelStatusManuallyDisabled {
				return nil, nil, nil
			}
			if entry.Status == status {
				return nil, nil, nil
			}
			entry.Status = status
			entry.ConfigRevision++
			entry.DisabledReason = reason
			entry.DisabledTime = common.GetTimestamp()
			if status == common.ChannelStatusEnabled {
				entry.DisabledReason = ""
				entry.DisabledTime = 0
			}
			anyEnabled := slices.ContainsFunc(entries, func(item ChannelKeyEntry) bool { return item.Status == common.ChannelStatusEnabled })
			if !anyEnabled {
				current.Status = common.ChannelStatusAutoDisabled
				other := current.GetOtherInfo()
				other["status_reason"] = ChannelStatusReasonAllKeysDisabled
				current.SetOtherInfo(other)
			} else if status == common.ChannelStatusEnabled && current.GetOtherInfo()["status_reason"] == ChannelStatusReasonAllKeysDisabled {
				current.Status = common.ChannelStatusEnabled
			}
			changed = true
			return entries, nil, nil
		}
		return nil, nil, nil
	})
	return err == nil && changed
}

// HasAvailableAllocationBudget is a conservative candidate filter; the ledger
// revalidates the precise allocation and estimate before forwarding.
func (channel *Channel) HasAvailableAllocationBudget(userIDs ...int) bool {
	if !ChannelBudgetsEnabled() {
		return true
	}
	allowed, err := ChannelBudgetAllows(context.Background(), channel.Id, channel.KeyEntryIDs)
	if err != nil {
		return false
	}
	for i, id := range channel.KeyEntryIDs {
		owner := channel.ChannelInfo.MultiKeyUsers[i]
		if len(userIDs) > 0 && owner != 0 && owner != userIDs[0] {
			continue
		}
		if allowed[id] && channel.keyEnabled(i) {
			return true
		}
	}
	return false
}

// ModelsAvailableToUserGroups filters model-list contributions against current
// allocation permissions and budgets without selecting or rotating a credential.
func ModelsAvailableToUserGroups(ctx context.Context, groups []string, userID int) (map[string]bool, error) {
	var channels []*Channel
	if err := DB.WithContext(ctx).Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, channel := range channels {
		allowed, err := ChannelBudgetAllows(ctx, channel.Id, channel.KeyEntryIDs)
		if err != nil {
			return nil, err
		}
		for i := range allocationKeyList(channel) {
			if !allowed[channel.ChannelKeyEntryID(i)] || !channel.keyEnabled(i) {
				continue
			}
			owner := channel.ChannelInfo.MultiKeyUsers[i]
			if owner != 0 && owner != userID {
				continue
			}
			for _, name := range channel.GetModels() {
				if !channel.KeyServesModel(i, name) {
					continue
				}
				upstream, _ := channel.RoutingUpstreamModelName(name)
				if channel.keyModelUnavailable(i, upstream) {
					continue
				}
				if owner == userID && userID > 0 {
					available[name] = true
					continue
				}
				for _, group := range groups {
					if slices.Contains(channel.GetGroups(), group) && slices.Contains(filterGroupModels([]string{name}, channel.GetOtherSettings().GroupModels[group]), name) {
						available[name] = true
						break
					}
				}
			}
		}
	}
	return available, nil
}

// MutateChannelKeyEntries serializes entry and budget mutations in one channel
// transaction. Account Tx calls may follow the sorted entry locks, never before.
func MutateChannelKeyEntries(ctx context.Context, channelID int, revision int64, mutate func(*gorm.DB, *Channel, []ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error)) (*Channel, error) {
	var updated Channel
	err := ChannelBudgetTransaction(ctx, channelID, func(tx *gorm.DB) error {
		tx = tx.Session(&gorm.Session{Logger: logger.Discard})
		if err := tx.First(&updated, channelID).Error; err != nil {
			return err
		}
		if updated.ChannelInfo.KeyEntryRevision == 0 {
			if err := CreateChannelKeyEntries(tx, &updated); err != nil {
				return err
			}
		}
		if revision > 0 && revision != updated.ChannelInfo.KeyEntryRevision {
			return ErrChannelKeyEntryRevisionStale
		}
		var entries []ChannelKeyEntry
		if err := lockForUpdate(tx).Where("channel_id = ? AND retired_at = 0", channelID).Order("id").Find(&entries).Error; err != nil {
			return err
		}
		slices.SortFunc(entries, func(a, b ChannelKeyEntry) int {
			if a.Position < b.Position {
				return -1
			}
			if a.Position > b.Position {
				return 1
			}
			return 0
		})
		next, retired, err := mutate(tx, &updated, entries)
		if err != nil {
			return err
		}
		if next == nil && retired == nil {
			return nil
		}
		if updated.ChannelInfo.IsMultiKey && len(next) == 0 {
			return errors.New("Cannot delete the last key")
		}
		if err := persistKeyEntries(tx, &updated, next, retired); err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(map[string]any{"key": updated.Key, "channel_info": updated.ChannelInfo, "status": updated.Status, "models": updated.Models, "other_info": updated.OtherInfo}).Error; err != nil {
			return err
		}
		return updated.UpdateAbilities(tx)
	})
	if err != nil {
		return nil, err
	}
	BumpSyncTopic(SyncTopicChannels)
	InitChannelCache()
	return &updated, nil
}
func UpdateChannelKeyAllocations(channelID int, revision int64, mutate func([]ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error)) (*Channel, error) {
	return MutateChannelKeyEntries(context.Background(), channelID, revision, func(_ *gorm.DB, _ *Channel, entries []ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error) {
		return mutate(entries)
	})
}
