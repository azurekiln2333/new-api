package controller

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// A discovery preview is process-local, owner-bound and short lived. Confirm
// consumes the sampled inventory, never a second upstream fetch. Current source,
// list and allocation revisions are checked again inside the write transaction.
type channelModelSyncPreview struct {
	ownerID int
	source  *model.Channel
	results []MultiKeyModelsResult
	expires time.Time
}

var channelModelSyncPreviews = struct {
	sync.Mutex
	items map[string]channelModelSyncPreview
}{items: make(map[string]channelModelSyncPreview)}

type MultiKeyModelSelection struct {
	KeyEntryID string   `json:"key_entry_id"`
	Rev        int64    `json:"rev"`
	Models     []string `json:"models"`
}

func handleChannelModelSync(c *gin.Context, channel *model.Channel, request MultiKeyManageRequest) {
	if c.GetInt("id") <= 0 {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Insufficient permissions"), nil)
		return
	}
	if request.Apply {
		applyChannelModelSync(c, channel, request)
		return
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision != channel.ChannelInfo.KeyEntryRevision {
		channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	keys := channel.GetKeys()
	results := make([]MultiKeyModelsResult, len(keys))
	jobs := make(chan int, len(keys))
	for keyIndex := range keys {
		if channel.ChannelKeyEntryID(keyIndex) <= 0 || keyIndex >= len(channel.KeyEntryRevisions) || channel.KeyEntryRevisions[keyIndex] <= 0 {
			channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
			return
		}
		results[keyIndex] = MultiKeyModelsResult{
			KeyIndex: keyIndex, KeyEntryID: strconv.FormatInt(channel.ChannelKeyEntryID(keyIndex), 10),
			Rev: channel.KeyEntryRevisions[keyIndex], KeyPreview: maskKeyPreview(keys[keyIndex]),
		}
		if status, ok := channel.ChannelInfo.MultiKeyStatusList[keyIndex]; ok && status != common.ChannelStatusEnabled {
			results[keyIndex].Skipped = true
			continue
		}
		jobs <- keyIndex
	}
	close(jobs)
	var wg sync.WaitGroup
	for range min(multiKeyModelSyncConcurrency, len(keys)) {
		wg.Go(func() {
			for keyIndex := range jobs {
				models, err := fetchChannelUpstreamModelIDsForRequest(ctx, channel, channelModelDiscoveryOptions{KeyIndex: &keyIndex, RequireEnabled: true})
				if err != nil {
					// Upstream error text can contain a request URL or credential.
					results[keyIndex].Error = "Failed to fetch models"
					continue
				}
				results[keyIndex].Models = normalizeModelNames(models)
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		common.ApiError(c, err)
		return
	}
	current, err := reloadManagementChannel(ctx, channel.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if current.ChannelInfo.KeyEntryRevision != channel.ChannelInfo.KeyEntryRevision || !sameChannelModelUpdateSource(channel, current) {
		channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
		return
	}
	now := time.Now()
	token := common.GetUUID()
	channelModelSyncPreviews.Lock()
	ownerCount := 0
	for key, preview := range channelModelSyncPreviews.items {
		if !now.Before(preview.expires) {
			delete(channelModelSyncPreviews.items, key)
		} else if preview.ownerID == c.GetInt("id") {
			ownerCount++
		}
	}
	if len(channelModelSyncPreviews.items) >= 128 || ownerCount >= 4 {
		channelModelSyncPreviews.Unlock()
		common.ApiError(c, errors.New("Model preview limit reached, retry later"))
		return
	}
	channelModelSyncPreviews.items[token] = channelModelSyncPreview{
		ownerID: c.GetInt("id"), source: channel, results: results, expires: now.Add(5 * time.Minute),
	}
	channelModelSyncPreviews.Unlock()
	markAuditLogged(c) // discovery is read-only, not a successful configuration write
	common.ApiSuccess(c, gin.H{
		"results": results, "added_models": []string{}, "applied": false,
		"channel_models": normalizeModelNames(channel.GetModels()),
		"revision":       channel.ChannelInfo.KeyEntryRevision, "preview_token": token,
	})
}

func applyChannelModelSync(c *gin.Context, channel *model.Channel, request MultiKeyManageRequest) {
	channelModelSyncPreviews.Lock()
	preview, ok := channelModelSyncPreviews.items[request.PreviewToken]
	channelModelSyncPreviews.Unlock()
	if !ok || preview.ownerID != c.GetInt("id") || preview.source.Id != channel.Id || !time.Now().Before(preview.expires) ||
		request.ExpectedRevision == nil || *request.ExpectedRevision <= 0 || *request.ExpectedRevision != preview.source.ChannelInfo.KeyEntryRevision {
		channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
		return
	}
	selected := make(map[string]MultiKeyModelSelection, len(request.ModelSelections))
	for _, selection := range request.ModelSelections {
		if _, duplicate := selected[selection.KeyEntryID]; duplicate {
			common.ApiError(c, errors.New("Invalid model selection"))
			return
		}
		resultIndex := slices.IndexFunc(preview.results, func(result MultiKeyModelsResult) bool { return result.KeyEntryID == selection.KeyEntryID })
		if resultIndex < 0 {
			channelKeyManageError(c, model.ErrChannelKeyEntryRevisionStale)
			return
		}
		result := preview.results[resultIndex]
		selection.Models = normalizeModelNames(selection.Models)
		if result.Error != "" || result.Skipped || selection.Rev != result.Rev || len(selection.Models) == 0 ||
			len(subtractModelNames(selection.Models, result.Models)) > 0 {
			common.ApiError(c, errors.New("Invalid model selection"))
			return
		}
		selected[selection.KeyEntryID] = selection
	}
	if len(selected) == 0 {
		common.ApiError(c, errors.New("Select models before applying"))
		return
	}
	actor := snapshotAuditActor(c)
	var added []string
	updated, err := model.MutateChannelKeyEntries(c.Request.Context(), channel.Id, 0, func(tx *gorm.DB, current *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
		if !sameChannelModelUpdateSource(preview.source, current) ||
			!reflect.DeepEqual(preview.source.ChannelInfo.MultiKeyModelMappings, current.ChannelInfo.MultiKeyModelMappings) ||
			!reflect.DeepEqual(preview.source.ChannelInfo.MultiKeyUpstreamProtocols, current.ChannelInfo.MultiKeyUpstreamProtocols) {
			return nil, nil, model.ErrChannelKeyEntryRevisionStale
		}
		// The client revision is bound to the saved preview above. A sibling
		// scheduled status-detail write may advance the list revision; each
		// selected allocation is still fenced by its own ID, revision and status.
		matched := 0
		for i := range entries {
			selection, exists := selected[strconv.FormatInt(entries[i].Id, 10)]
			if !exists {
				continue
			}
			if entries[i].ConfigRevision != selection.Rev || entries[i].Status != common.ChannelStatusEnabled {
				return nil, nil, model.ErrChannelKeyEntryRevisionStale
			}
			matched++
			scope := entries[i].ScopeMode
			if err := entries[i].SetKeyModels(mergeModelNames(entries[i].KeyModels(), selection.Models)); err != nil {
				return nil, nil, err
			}
			entries[i].ScopeMode = scope // additions never narrow all or erase an existing allowlist
			entries[i].ConfigRevision++
			added = mergeModelNames(added, subtractModelNames(selection.Models, current.GetModels()))
			current.Models = strings.Join(mergeModelNames(current.GetModels(), selection.Models), ",")
		}
		if matched != len(selected) {
			return nil, nil, model.ErrChannelKeyEntryRevisionStale
		}
		draft := buildAuditDraft(actor, "channel.multi_key_manage", map[string]any{
			"id": current.Id, "action": "sync_key_models", "revision": current.ChannelInfo.KeyEntryRevision + 1,
		})
		if _, err := model.EnqueueAuditEventTx(tx, draft); err != nil {
			return nil, nil, err
		}
		return entries, nil, nil
	})
	if err != nil {
		channelKeyManageError(c, err)
		return
	}
	channelModelSyncPreviews.Lock()
	delete(channelModelSyncPreviews.items, request.PreviewToken)
	channelModelSyncPreviews.Unlock()
	markAuditLogged(c)
	closeActiveChannelWebSockets([]int{channel.Id})
	common.ApiSuccess(c, gin.H{
		"results": preview.results, "added_models": added, "applied": true,
		"channel_models": normalizeModelNames(updated.GetModels()), "revision": updated.ChannelInfo.KeyEntryRevision,
	})
}
