package controller

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestChannelCreateAuditFailureRollsBackEveryRow pins the atomic create contract:
// the channel rows, their allocations, their ability rows and the audit
// obligation commit together. When the audit insert fails, nothing is left
// behind, so a created channel can never exist without its audit event.
func TestChannelCreateAuditFailureRollsBackEveryRow(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	const callback = "test:audit-outbox-failure"
	database := model.DB
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_event_outbox" {
			_ = tx.AddError(gorm.ErrInvalidDB)
		}
	}))
	t.Cleanup(func() { require.NoError(t, database.Callback().Create().Before("gorm:create").Remove(callback)) })

	name := "create-audit-rollback"
	request, err := common.Marshal(AddChannelRequest{Mode: "single", Channel: &model.Channel{
		Name: name, Type: 1, Key: "sk-create-audit-rollback", Models: "test-model", Group: "default",
	}})
	require.NoError(t, err)
	response := postAddChannel(t, rootID, common.RoleRootUser, string(request))
	require.Equal(t, http.StatusOK, response.Code)

	var result struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success, "a failed audit insert must fail the create")

	var channels, abilities, allocations, audits int64
	require.NoError(t, database.Model(&model.Channel{}).Where("name = ?", name).Count(&channels).Error)
	require.NoError(t, database.Model(&model.Ability{}).Count(&abilities).Error)
	require.NoError(t, database.Model(&model.ChannelKeyEntry{}).Count(&allocations).Error)
	require.NoError(t, database.Model(&model.AuditEventOutbox{}).Count(&audits).Error)
	assert.Zero(t, channels, "a rolled-back create must not leave a channel row")
	assert.Zero(t, abilities, "a rolled-back create must not leave ability rows")
	assert.Zero(t, allocations, "a rolled-back create must not leave allocation rows")
	assert.Zero(t, audits)
}

// TestChannelUpdatePublishesRoutingAndAuditAtomically pins the atomic update
// contract: one transaction commits the configuration change, the ability rows
// and the audit obligation, and the committed row is published to the routing
// snapshot immediately rather than by a later full reload.
func TestChannelUpdatePublishesRoutingAndAuditAtomically(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })

	name := "update-atomic-router"
	channel := &model.Channel{Name: name, Type: 1, Key: "sk-update-atomic", Models: "old-model", Group: "default"}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { _, _ = model.BatchDeleteChannels([]int{channel.Id}) })

	patch, err := common.Marshal(map[string]any{
		"id": channel.Id, "name": name, "models": "new-model", "group": "default",
	})
	require.NoError(t, err)
	response := putUpdateChannel(t, rootID, common.RoleRootUser, string(patch))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var result struct {
		Success bool `json:"success"`
		Message string
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "new-model", stored.Models)

	// The audit obligation is durable in the same transaction, so this request
	// must not also fall through to the middleware audit fallback.
	var audits int64
	require.NoError(t, model.DB.Model(&model.AuditEventOutbox{}).
		Where("action = ? AND category = ?", "channel.update", "operation").Count(&audits).Error)
	assert.EqualValues(t, 1, audits, "exactly one audit obligation must be enqueued for the edit")

	// The ability rows describe the committed model list.
	var abilityModels []string
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).
		Pluck("model", &abilityModels).Error)
	assert.Contains(t, abilityModels, "new-model")
	assert.NotContains(t, abilityModels, "old-model")

	// The published routing snapshot resolves the new model to this channel
	// without waiting for a full reload.
	selected, err := model.GetRandomSatisfiedChannel("default", "new-model", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id, "a committed edit must be routable immediately")
}

// TestChannelUpdateAuditFailureRollsBackConfiguration pins that a failed audit
// insert rolls the configuration write back: the stored model list, the ability
// rows and the audit obligation all stay as they were.
func TestChannelUpdateAuditFailureRollsBackConfiguration(t *testing.T) {
	rootID := setupChannelCreateTest(t)
	const callback = "test:update-audit-outbox-failure"
	database := model.DB
	require.NoError(t, database.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_event_outbox" {
			_ = tx.AddError(gorm.ErrInvalidDB)
		}
	}))
	t.Cleanup(func() { require.NoError(t, database.Callback().Create().Before("gorm:create").Remove(callback)) })

	name := "update-audit-rollback"
	channel := &model.Channel{Name: name, Type: 1, Key: "sk-update-audit-rollback", Models: "keep-model", Group: "default"}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { _, _ = model.BatchDeleteChannels([]int{channel.Id}) })

	patch, err := common.Marshal(map[string]any{
		"id": channel.Id, "name": name, "models": "rejected-model", "group": "default",
	})
	require.NoError(t, err)
	response := putUpdateChannel(t, rootID, common.RoleRootUser, string(patch))
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success, "a failed audit insert must fail the edit")

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "keep-model", stored.Models, "a rolled-back edit must not change the stored models")

	var abilityModels []string
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).
		Pluck("model", &abilityModels).Error)
	assert.NotContains(t, abilityModels, "rejected-model")
}

// TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot pins the no-memory-cache
// branch of the bound-user index: it must take the same generation guard as the
// routing reload. A reload that read the database before a targeted publication
// advanced the generation must not publish its older bindings.
//
// The interleaving is barrier-driven: a GORM query callback pauses the reload once
// its channel and allocation rows are read, the test then performs a real targeted
// write with the memory cache disabled, and the reload resumes provably stale.
func TestChannelBoundIndexNoCacheReloadRejectsStaleSnapshot(t *testing.T) {
	setupChannelCreateTest(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })

	// Insert the binding with its authoritative allocation rows. Replacing the
	// channel_info projection afterwards would erase its migration revision.
	const boundUser = 4242
	channel := &model.Channel{Name: "bound-stale-guard", Type: 1, Key: "sk-bound-stale\nsk-unbound-stale", Models: "bound-model", Group: "default",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:     true,
			MultiKeySize:   2,
			MultiKeyUsers:  map[int]int{0: boundUser},
			MultiKeyModels: map[int][]string{0: {"bound-model"}},
		},
	}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { _, _ = model.BatchDeleteChannels([]int{channel.Id}) })
	model.InitChannelCache()
	// Positive control: the binding is indexed before the interleaving, so the
	// assertion below fails for the right reason rather than because the probe
	// never observed anything.
	require.Equal(t, []int{channel.Id}, model.UserBoundChannelIDs(boundUser))

	resumeReload := pauseChannelCacheReload(t, channel.Id)
	// The reload has read the old bindings and is paused. Commit and publish a
	// newer state that drops the binding, through the production write path.
	_, err := model.UpdateChannelConfigAtomic(channel.Id, model.ChannelConfigEdit{
		Apply: func(baseline *model.Channel, stored *model.Channel) error {
			stored.ChannelInfo.MultiKeyUsers = nil
			return nil
		},
	})
	require.NoError(t, err)
	assert.Empty(t, model.UserBoundChannelIDs(boundUser), "the committed write removes the binding before the stale reload resumes")
	resumeReload()

	// The reload was stale: the removed binding must not come back. Reading the
	// index directly is the precise probe; a stale publish would restore the id.
	assert.Empty(t, model.UserBoundChannelIDs(boundUser),
		"a stale reload must not republish bindings that a newer write removed")
}

// TestChannelCacheReloadRejectsStaleSnapshot pins the stale-reload guard: a full
// reload whose read began before a targeted publication must discard its own
// older snapshot instead of publishing it over the newer committed routing.
//
// The interleaving is deterministic and barrier-driven, not timing-based. A GORM
// query callback pauses the reload once its channel and allocation rows are read,
// the test then performs a real targeted write (the production commit path) that
// publishes newer routing and advances the generation, and the reload resumes
// provably stale. Afterwards the replaced model must not be routable again.
func TestChannelCacheReloadRejectsStaleSnapshot(t *testing.T) {
	setupChannelCreateTest(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })

	const (
		staleModel = "routed-model"
		freshModel = "renamed-model"
	)
	channel := &model.Channel{Name: "stale-reload-guard", Type: 1, Key: "sk-stale-reload", Models: staleModel, Group: "default"}
	require.NoError(t, channel.Insert())
	t.Cleanup(func() { _, _ = model.BatchDeleteChannels([]int{channel.Id}) })

	// Establish the pre-interleaving routing snapshot.
	model.InitChannelCache()
	initial, err := model.GetRandomSatisfiedChannel("default", staleModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, initial, "the initial routing snapshot must contain the channel")

	// Pause the reload after it has read its rows, so its snapshot is already old
	// relative to everything the test publishes from here on. The callback is
	// registered only after the priming reload finished, so the reload it pauses
	// is exactly the one this test interleaves.
	resumeReload := pauseChannelCacheReload(t, channel.Id)
	// The reload has read the old rows and is paused. Commit and publish newer
	// routing through the same production path a console edit uses. It runs on its
	// own connection, so the paused reload does not block it.
	_, err = model.UpdateChannelConfigAtomic(channel.Id, model.ChannelConfigEdit{
		Apply: func(baseline *model.Channel, stored *model.Channel) error {
			stored.Models = freshModel
			return nil
		},
	})
	require.NoError(t, err)
	resumeReload()

	// The resumed reload held an older snapshot and must have discarded it, so the
	// replaced model stays unroutable and the newer committed routing survives.
	stale, err := model.GetRandomSatisfiedChannel("default", staleModel, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, stale, "a stale reload must not republish routing that was already replaced")
	fresh, err := model.GetRandomSatisfiedChannel("default", freshModel, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, fresh, "the newer committed routing must survive the stale reload")
	assert.Equal(t, channel.Id, fresh.Id)
}

// pauseChannelCacheReload holds a real reload after both its channel rows and
// authoritative allocations have been read. Pausing only the channel query lets
// the resumed projection read newer bindings, which would hide a stale publish.
// Cleanup always releases and joins the reload, including after a fatal assertion.
func pauseChannelCacheReload(t *testing.T, channelID int) func() {
	t.Helper()
	const callback = "test:channel-cache-allocation-snapshot"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	loaded := make(chan struct{})
	release := make(chan struct{})
	reloadDone := make(chan struct{})
	var interceptOnce, releaseOnce sync.Once
	database := model.DB
	require.NoError(t, database.Callback().Query().After("gorm:after_query").Register(callback, func(tx *gorm.DB) {
		entries, ok := tx.Statement.Dest.(*[]model.ChannelKeyEntry)
		if tx.Error != nil || !ok {
			return
		}
		containsChannel := false
		for _, entry := range *entries {
			if entry.ChannelId == channelID {
				containsChannel = true
				break
			}
		}
		if !containsChannel {
			return
		}
		intercepted := false
		interceptOnce.Do(func() { intercepted = true })
		if !intercepted {
			return
		}
		close(loaded)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}))
	resume := func() {
		t.Helper()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-reloadDone:
		case <-time.After(10 * time.Second):
			t.Fatal("channel cache reload did not finish after releasing its snapshot")
		}
	}
	t.Cleanup(func() {
		defer func() { assert.NoError(t, database.Callback().Query().Remove(callback)) }()
		resume()
	})
	go func() {
		defer close(reloadDone)
		model.InitChannelCache()
	}()
	select {
	case <-loaded:
	case <-ctx.Done():
		t.Fatal("channel cache reload did not reach its allocation snapshot")
	}
	return resume
}
