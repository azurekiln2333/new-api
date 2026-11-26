package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func channelKeyManageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrChannelKeyEntryRevisionStale), errors.Is(err, model.ErrChannelKeyEntriesAmbiguous), errors.Is(err, model.ErrBudgetStaleVersion), errors.Is(err, model.ErrBudgetAttemptConflict):
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Channel key configuration changed, reload and retry"), gin.H{"error_code": "channel_key_revision_conflict"})
	default:
		common.ApiError(c, err)
	}
}

// applyManagedChannelStatus reproduces the legacy channel transitions for one
// Key management action inside the caller's locked entry mutation: losing every
// enabled allocation marks the channel unavailable (a manual channel disable is
// kept), and enabling an allocation restores a channel that only the all-keys
// rule disabled. The entry set passed in is the authoritative post-change
// projection, so the reason written never contradicts the resulting status.
func applyManagedChannelStatus(current *model.Channel, entries []model.ChannelKeyEntry, disabling bool) {
	if !current.ChannelInfo.IsMultiKey {
		return
	}
	enabled := slices.ContainsFunc(entries, func(entry model.ChannelKeyEntry) bool {
		return entry.Status == common.ChannelStatusEnabled
	})
	info := current.GetOtherInfo()
	if !enabled {
		// Same rule as the legacy disable helper: only an enabled channel turns
		// into the all-keys-disabled state, and only then is that reason written.
		// A manual or unrelated automatic disable keeps its own status and reason,
		// so a later enable cannot mistake it for key exhaustion.
		if current.Status != common.ChannelStatusEnabled {
			return
		}
		current.Status = common.ChannelStatusManuallyDisabled
		info["status_reason"] = model.ChannelStatusReasonAllKeysDisabled
		info["status_time"] = common.GetTimestamp()
		current.SetOtherInfo(info)
		return
	}
	if disabling || info["status_reason"] != model.ChannelStatusReasonAllKeysDisabled {
		return
	}
	if current.Status == common.ChannelStatusManuallyDisabled || current.Status == common.ChannelStatusAutoDisabled {
		current.Status = common.ChannelStatusEnabled
		info["status_reason"] = ""
		info["status_time"] = common.GetTimestamp()
		current.SetOtherInfo(info)
	}
}

func channelKeyRequestIndex(request MultiKeyManageRequest, entries []model.ChannelKeyEntry) (int, error) {
	if request.KeyEntryID != "" {
		id, err := strconv.ParseInt(request.KeyEntryID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != request.KeyEntryID {
			return -1, common.NewMessage("Invalid channel key allocation ID")
		}
		for i := range entries {
			if entries[i].Id == id {
				if request.ExpectedRev == nil || *request.ExpectedRev <= 0 || entries[i].ConfigRevision != *request.ExpectedRev {
					return -1, model.ErrChannelKeyEntryRevisionStale
				}
				return i, nil
			}
		}
		return -1, model.ErrChannelKeyEntryNotFound
	}
	if request.KeyIndex == nil || *request.KeyIndex < 0 || *request.KeyIndex >= len(entries) {
		return -1, common.NewMessage("Key index out of range")
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.Key] {
			return -1, model.ErrChannelKeyEntriesAmbiguous
		}
		seen[entry.Key] = true
	}
	return *request.KeyIndex, nil
}

type channelKeyDedupGroup struct {
	SurvivorID         string                   `json:"survivor_id"`
	EntryIDs           []string                 `json:"entry_ids"`
	MergedPeriodUsed   int64                    `json:"merged_period_used"`
	MergedLifetimeUsed int64                    `json:"merged_lifetime_used"`
	LimitQuota         *int64                   `json:"limit_quota,omitempty"`
	Skipped            []channelKeyDedupSkipped `json:"skipped"`
}
type channelKeyDedupSkipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}
type channelKeyDedupPlan struct {
	Revision       int64                  `json:"revision"`
	PreviewToken   string                 `json:"preview_token"`
	Applied        bool                   `json:"applied"`
	Groups         []channelKeyDedupGroup `json:"groups"`
	SkippedEntries int                    `json:"skipped_entries"`
	RetiredEntries int                    `json:"retired_entries"`
	MergedGroups   int                    `json:"merged_groups"`
}

func channelKeyDedupPreview(tx *gorm.DB, ch *model.Channel, entries []model.ChannelKeyEntry) (channelKeyDedupPlan, map[int64]model.ChannelBudgetAccount, error) {
	plan := channelKeyDedupPlan{Revision: ch.ChannelInfo.KeyEntryRevision, Groups: []channelKeyDedupGroup{}}
	var rows []model.ChannelBudgetAccount
	if err := tx.Where("channel_id = ? AND scope_type = ?", ch.Id, model.BudgetScopeKey).Order("scope_id").Find(&rows).Error; err != nil {
		return plan, nil, err
	}
	accounts := map[int64]model.ChannelBudgetAccount{}
	for _, row := range rows {
		accounts[row.ScopeId] = row
	}
	grouped := map[string][]model.ChannelKeyEntry{}
	var order []string
	for _, entry := range entries {
		account := accounts[entry.Id]
		models := entry.KeyModels()
		slices.Sort(models)
		policy := struct {
			Key     string
			Owner   int
			Scope   string
			Models  []string
			Status  int
			Reason  string
			Enabled bool
			Limit   int64
		}{entry.Key, entry.BoundUserId, entry.ScopeMode, models, entry.Status, entry.DisabledReason, account.Enabled, account.LimitQuota}
		data, err := common.Marshal(policy)
		if err != nil {
			return plan, nil, err
		}
		hash := sha256.Sum256(data)
		key := hex.EncodeToString(hash[:])
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], entry)
	}
	for _, key := range order {
		group := grouped[key]
		if len(group) < 2 {
			continue
		}
		slices.SortFunc(group, func(a, b model.ChannelKeyEntry) int {
			if a.Id < b.Id {
				return -1
			}
			if a.Id > b.Id {
				return 1
			}
			return 0
		})
		preview := channelKeyDedupGroup{EntryIDs: []string{}, Skipped: []channelKeyDedupSkipped{}}
		for _, entry := range group {
			live, err := model.HasLiveChannelBudgetReservationsTx(tx, entry.Id)
			if err != nil {
				return plan, nil, err
			}
			if live || accounts[entry.Id].ReservedQuota > 0 {
				preview.Skipped = append(preview.Skipped, channelKeyDedupSkipped{strconv.FormatInt(entry.Id, 10), "Key allocation has active requests or tasks"})
				plan.SkippedEntries++
				continue
			}
			preview.EntryIDs = append(preview.EntryIDs, strconv.FormatInt(entry.Id, 10))
			account := accounts[entry.Id]
			if preview.MergedPeriodUsed > common.MaxWalletQuota-account.PeriodUsedQuota || preview.MergedLifetimeUsed > common.MaxWalletQuota-account.LifetimeUsedQuota {
				return plan, nil, model.ErrBudgetConfigInvalid
			}
			preview.MergedPeriodUsed += account.PeriodUsedQuota
			preview.MergedLifetimeUsed += account.LifetimeUsedQuota
		}
		if len(preview.EntryIDs) > 0 {
			preview.SurvivorID = preview.EntryIDs[0]
		}
		if accounts[group[0].Id].Enabled {
			limit := accounts[group[0].Id].LimitQuota
			preview.LimitQuota = &limit
		}
		plan.Groups = append(plan.Groups, preview)
	}
	// Snapshot token hashes credential fingerprints and current accounting state;
	// no raw credential enters the token or response. Apply recomputes under lock.
	type state struct {
		ID          int64
		Revision    int64
		Fingerprint string
		Account     model.ChannelBudgetAccount
	}
	snapshot := make([]state, 0, len(entries))
	for _, entry := range entries {
		snapshot = append(snapshot, state{entry.Id, entry.ConfigRevision, model.ChannelKeyFingerprint(entry.Key), accounts[entry.Id]})
	}
	data, err := common.Marshal(struct {
		Revision int64
		Entries  []state
		Groups   []channelKeyDedupGroup
	}{ch.ChannelInfo.KeyEntryRevision, snapshot, plan.Groups})
	if err != nil {
		return plan, nil, err
	}
	hash := sha256.Sum256(data)
	plan.PreviewToken = hex.EncodeToString(hash[:])
	return plan, accounts, nil
}

func handleChannelKeyAllocationAction(c *gin.Context, channel *model.Channel, request MultiKeyManageRequest) bool {
	switch request.Action {
	case "disable_key", "enable_key", "enable_all_keys", "disable_all_keys", "delete_key", "delete_disabled_keys", "set_key_models", "set_key_user", "set_key_quota", "reset_key_quota", "deduplicate_keys", "reorder_keys":
	default:
		return false
	}
	gated := request.Action == "set_key_quota" || request.Action == "reset_key_quota" || request.Action == "deduplicate_keys"
	if gated && !model.ChannelBudgetsEnabled() {
		channelKeyManageError(c, model.ErrChannelAllocationsInactive)
		return true
	}
	expected := int64(0)
	if request.ExpectedRevision != nil {
		expected = *request.ExpectedRevision
	}
	switch request.Action {
	case "enable_all_keys", "disable_all_keys", "delete_disabled_keys", "reorder_keys":
		if request.ExpectedRevision == nil || expected <= 0 {
			channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
			return true
		}
	case "deduplicate_keys":
		if request.Apply && (request.ExpectedRevision == nil || expected <= 0) {
			channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
			return true
		}
	}
	var budget *model.ChannelBudgetView
	var preview channelKeyDedupPlan
	var removedKeys []string
	auditActor := snapshotAuditActor(c)
	updated, err := model.MutateChannelKeyEntries(c.Request.Context(), channel.Id, expected, func(tx *gorm.DB, current *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
		if request.Action == "deduplicate_keys" {
			var accounts map[int64]model.ChannelBudgetAccount
			var err error
			preview, accounts, err = channelKeyDedupPreview(tx, current, entries)
			if err != nil {
				return nil, nil, err
			}
			if !request.Apply {
				return nil, nil, nil
			}
			if request.PreviewToken == "" || request.PreviewToken != preview.PreviewToken {
				return nil, nil, model.ErrChannelKeyEntryRevisionStale
			}
			retire := map[int64]int64{}
			// Establish the common account before sorted key-account merge operations.
			if _, err := model.EnsureChannelBudgetAccountTx(tx, model.BudgetScopeChannel, int64(current.Id), current.Id); err != nil {
				return nil, nil, err
			}
			for _, group := range preview.Groups {
				if len(group.EntryIDs) < 2 {
					continue
				}
				target, _ := strconv.ParseInt(group.SurvivorID, 10, 64)
				for _, idString := range group.EntryIDs[1:] {
					source, _ := strconv.ParseInt(idString, 10, 64)
					if _, err := model.MergeChannelBudgetAccountsTx(tx, model.BudgetMergeRequest{SourceScope: model.BudgetScopeKey, SourceID: source, TargetScope: model.BudgetScopeKey, TargetID: target, SourceEpoch: accounts[source].ResetEpoch, TargetEpoch: accounts[target].ResetEpoch}); err != nil {
						return nil, nil, err
					}
					retire[source] = target
					preview.RetiredEntries++
				}
				preview.MergedGroups++
			}
			next := make([]model.ChannelKeyEntry, 0, len(entries))
			var retired []model.ChannelKeyEntry
			for _, entry := range entries {
				if target, ok := retire[entry.Id]; ok {
					entry.MergedIntoId = target
					retired = append(retired, entry)
				} else {
					next = append(next, entry)
				}
			}
			preview.Applied = true
			return next, retired, nil
		}
		if request.Action == "reorder_keys" {
			if len(request.KeyEntryIDs) != len(entries) {
				return nil, nil, model.ErrChannelKeyEntryRevisionStale
			}
			byID := map[string]model.ChannelKeyEntry{}
			for _, entry := range entries {
				byID[strconv.FormatInt(entry.Id, 10)] = entry
			}
			next := make([]model.ChannelKeyEntry, 0, len(entries))
			for _, id := range request.KeyEntryIDs {
				entry, ok := byID[id]
				if !ok {
					return nil, nil, model.ErrChannelKeyEntryRevisionStale
				}
				delete(byID, id)
				next = append(next, entry)
			}
			return next, nil, nil
		}
		if request.Action == "enable_all_keys" || request.Action == "disable_all_keys" || request.Action == "delete_disabled_keys" {
			next := make([]model.ChannelKeyEntry, 0, len(entries))
			var retired []model.ChannelKeyEntry
			for _, entry := range entries {
				if request.Action == "delete_disabled_keys" && entry.Status == common.ChannelStatusAutoDisabled {
					retired = append(retired, entry)
					removedKeys = append(removedKeys, entry.Key)
					continue
				}
				if request.Action != "delete_disabled_keys" {
					entry.Status = common.ChannelStatusEnabled
					if request.Action == "disable_all_keys" {
						entry.Status = common.ChannelStatusManuallyDisabled
					}
					entry.DisabledReason = ""
					entry.DisabledTime = 0
					entry.ConfigRevision++
				}
				next = append(next, entry)
			}
			if request.Action != "delete_disabled_keys" {
				applyManagedChannelStatus(current, next, request.Action == "disable_all_keys")
			}
			return next, retired, nil
		}
		index, err := channelKeyRequestIndex(request, entries)
		if err != nil {
			return nil, nil, err
		}
		entry := &entries[index]
		switch request.Action {
		case "disable_key":
			entry.Status = common.ChannelStatusManuallyDisabled
			entry.DisabledReason = ""
			entry.DisabledTime = common.GetTimestamp()
			applyManagedChannelStatus(current, entries, true)
		case "enable_key":
			entry.Status = common.ChannelStatusEnabled
			entry.DisabledReason = ""
			entry.DisabledTime = 0
			applyManagedChannelStatus(current, entries, false)
		case "delete_key":
			removedKeys = append(removedKeys, entry.Key)
			retired := *entry
			return append(entries[:index:index], entries[index+1:]...), []model.ChannelKeyEntry{retired}, nil
		case "set_key_models":
			if request.ScopeMode != nil && *request.ScopeMode != "all" && *request.ScopeMode != "allowlist" {
				return nil, nil, common.NewMessage("invalid channel key model scope")
			}
			models := normalizeModelNames(request.Models)
			if err := entry.SetKeyModels(models); err != nil {
				return nil, nil, err
			}
			if request.ScopeMode != nil {
				// SetKeyModels infers legacy empty selections as all. An explicit
				// empty allowlist instead stays deny-all.
				entry.ScopeMode = *request.ScopeMode
			}
			if entry.ScopeMode == "allowlist" && len(models) > 0 {
				// Preserve manual models and their order; only the selected Key's
				// explicit allowlist adds names to the shared catalog.
				current.Models = strings.Join(mergeModelNames(current.GetModels(), models), ",")
			}
		case "set_key_user":
			if request.UserId == nil || *request.UserId < 0 {
				return nil, nil, common.NewMessage("No user specified to bind")
			}
			if *request.UserId > 0 {
				var count int64
				if err := tx.Model(&model.User{}).Where("id = ?", *request.UserId).Count(&count).Error; err != nil {
					return nil, nil, err
				}
				if count != 1 {
					return nil, nil, common.NewMessage("User does not exist")
				}
			}
			entry.BoundUserId = *request.UserId
		case "set_key_quota", "reset_key_quota":
			if request.KeyEntryID == "" || request.ExpectedConfigVersion == nil || *request.ExpectedConfigVersion < 0 {
				return nil, nil, model.ErrBudgetConfigInvalid
			}
			if _, err := model.EnsureChannelBudgetAccountTx(tx, model.BudgetScopeChannel, int64(current.Id), current.Id); err != nil {
				return nil, nil, err
			}
			if request.Action == "set_key_quota" {
				if request.Enabled == nil || request.LimitQuota == nil {
					return nil, nil, model.ErrBudgetConfigInvalid
				}
				budget, err = model.SetChannelBudgetLimitTx(tx, model.BudgetScopeKey, entry.Id, *request.ExpectedConfigVersion, *request.Enabled, *request.LimitQuota)
			} else {
				budget, err = model.ResetChannelBudgetUsageTx(tx, model.BudgetResetRequest{Scope: model.BudgetScopeKey, ScopeID: entry.Id, ExpectedVersion: *request.ExpectedConfigVersion, OperationID: request.OperationID})
			}
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, nil
		}
		entry.ConfigRevision++
		if request.Action == "set_key_models" {
			// persistKeyEntries advances the list revision once after this callback.
			// The audit, catalog, scope and abilities share its transaction, so any
			// later persistence failure also removes this audit obligation.
			draft := buildAuditDraft(auditActor, "channel.multi_key_manage", map[string]any{
				"id":       current.Id,
				"action":   request.Action,
				"entry_id": strconv.FormatInt(entry.Id, 10),
				"revision": current.ChannelInfo.KeyEntryRevision + 1,
			})
			if _, err := model.EnqueueAuditEventTx(tx, draft); err != nil {
				return nil, nil, err
			}
		}
		return entries, nil, nil
	})
	if err != nil {
		channelKeyManageError(c, err)
		return true
	}
	if request.Action == "deduplicate_keys" && !request.Apply {
		markAuditLogged(c)
		common.ApiSuccess(c, preview)
		return true
	}
	if request.Action == "set_key_models" {
		markAuditLogged(c)
	} else {
		recordManageAudit(c, "channel.multi_key_manage", map[string]any{"id": channel.Id, "action": request.Action, "entry_id": request.KeyEntryID, "revision": updated.ChannelInfo.KeyEntryRevision})
	}
	closeActiveChannelWebSockets([]int{channel.Id})
	if len(removedKeys) > 0 {
		service.RevokeRemovedChannelCredentials(updated, channel.Key, updated.Key)
	}
	if budget != nil {
		common.ApiSuccess(c, budget)
	} else if request.Action == "deduplicate_keys" {
		common.ApiSuccess(c, preview)
	} else if request.Action == "set_key_models" {
		common.ApiSuccess(c, gin.H{"revision": updated.ChannelInfo.KeyEntryRevision, "channel_models": normalizeModelNames(updated.GetModels())})
	} else {
		common.ApiSuccess(c, gin.H{"revision": updated.ChannelInfo.KeyEntryRevision})
	}
	return true
}
