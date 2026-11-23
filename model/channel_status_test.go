package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestBatchSetChannelTagDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "channel-tags.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "channel_tag_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(2)
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				assert.NoError(t, db.Migrator().DropTable(&Ability{}, &Channel{}, &SystemTask{}))
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &SystemTask{}))
			var version string
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			oldTag, newTag := "old-tag", "new-tag"
			priority, weight := int64(7), uint(23)
			channels := []Channel{
				{Name: "enabled", Status: common.ChannelStatusEnabled},
				{Name: "disabled", Status: common.ChannelStatusAutoDisabled},
				{Name: "unselected", Status: common.ChannelStatusEnabled},
			}
			for i := range channels {
				channel := &channels[i]
				channel.Models, channel.Group = "model-a,model-b", "default,second"
				channel.Tag, channel.Priority, channel.Weight = &oldTag, &priority, &weight
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			ids := []int{channels[0].Id, channels[1].Id}
			for _, tc := range []struct {
				name string
				tag  *string
			}{
				{name: "set", tag: &newTag},
				{name: "clear", tag: nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					require.NoError(t, BatchSetChannelTag(ids, tc.tag))
					for i, channel := range channels {
						wantTag := tc.tag
						if i == 2 {
							wantTag = &oldTag
						}
						var stored Channel
						require.NoError(t, db.First(&stored, channel.Id).Error)
						assert.Equal(t, wantTag, stored.Tag, channel.Name)
						assert.Equal(t, channel.Status, stored.Status, channel.Name)
						var abilities []Ability
						require.NoError(t, db.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
						require.Len(t, abilities, 4)
						for _, ability := range abilities {
							assert.Equal(t, wantTag, ability.Tag, channel.Name)
							assert.Equal(t, channel.Status == common.ChannelStatusEnabled, ability.Enabled, channel.Name)
							assert.Equal(t, channel.Priority, ability.Priority, channel.Name)
							assert.Equal(t, weight, ability.Weight, channel.Name)
						}
					}
				})
			}
		})
	}
}

// channelsRevision is the id of the newest channels sync task, 0 without one.
func channelsRevision(t *testing.T) int64 {
	t.Helper()
	revisions, err := GetLatestSyncRevisions()
	require.NoError(t, err)
	return revisions[SyncTopicChannels]
}

func setupChannelStatusTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
	})
}

func TestUpdateChannelStatusPersistsMultiKeyState(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "multi-key-status",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, "provider rejected key", stored.ChannelInfo.MultiKeyDisabledReason[0])
	assert.NotZero(t, stored.ChannelInfo.MultiKeyDisabledTime[0])
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
}

func TestUpdateChannelStatusCheckedCacheRouting(t *testing.T) {
	const sourceSetting = `{"task_plugin_key":"original-plugin","max_concurrent_requests":1,"future_setting":{"tenant":"original"}}`
	const changedCapSetting = `{"task_plugin_key":"original-plugin","max_concurrent_requests":2,"future_setting":{"tenant":"original"}}`
	for _, tc := range []struct {
		name                string
		multiKey            bool
		replaceCache        string
		setting             *string
		storedSetting       string
		cachedSetting       string
		preserveCacheStatus bool
	}{
		{name: "single-key disable removes routing"},
		{name: "multi-key routing stays until the last key is disabled", multiKey: true},
		{name: "replaced cache key stays enabled and routable", replaceCache: "key", preserveCacheStatus: true},
		{name: "replaced cache auth headers stay enabled and routable", replaceCache: "headers", preserveCacheStatus: true},
		{name: "replaced cache provider config stays enabled and routable", replaceCache: "settings", preserveCacheStatus: true},
		{name: "replaced cache key models stay enabled and routable", multiKey: true, replaceCache: "models", preserveCacheStatus: true},
		{name: "replaced cache key users stay enabled and routable", multiKey: true, replaceCache: "users", preserveCacheStatus: true},
		{name: "replaced cache empty allowlist stays enabled and routable", multiKey: true, replaceCache: "scope-mode", preserveCacheStatus: true},
		{name: "replaced cache allocation identity stays enabled and routable", replaceCache: "allocation-id", preserveCacheStatus: true},
		{name: "newer cache allocation revision stays enabled and routable", replaceCache: "allocation-revision", preserveCacheStatus: true},
		{name: "stored cap change still disables cached routing", multiKey: true, setting: common.GetPointer(sourceSetting), storedSetting: changedCapSetting},
		{name: "cached cap change is preserved while routing is disabled", setting: common.GetPointer(sourceSetting), cachedSetting: changedCapSetting},
		{name: "nil cached setting receives a cap-only stored status", storedSetting: `{"max_concurrent_requests":2}`},
		{name: "cached cap absent from stored settings still disables routing", cachedSetting: `{"max_concurrent_requests":2}`},
		{name: "empty cached setting receives a cap-only stored status", setting: common.GetPointer(""), storedSetting: `{"max_concurrent_requests":2}`},
		{name: "cached cap preserves empty stored settings", setting: common.GetPointer(""), cachedSetting: `{"max_concurrent_requests":2}`},
		{name: "replaced cache plugin binding stays enabled and routable", setting: common.GetPointer(sourceSetting), cachedSetting: `{"task_plugin_key":"replacement-plugin","max_concurrent_requests":2,"future_setting":{"tenant":"original"}}`, preserveCacheStatus: true},
		{name: "replaced cache unknown setting stays enabled and routable", setting: common.GetPointer(sourceSetting), cachedSetting: `{"task_plugin_key":"original-plugin","max_concurrent_requests":2,"future_setting":{"tenant":"replacement"}}`, preserveCacheStatus: true},
		{name: "malformed cached setting stays enabled and routable", setting: common.GetPointer(sourceSetting), cachedSetting: `{"max_concurrent_requests":`, preserveCacheStatus: true},
		{name: "null cached setting stays enabled and routable", setting: common.GetPointer(sourceSetting), cachedSetting: `null`, preserveCacheStatus: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useScheduledChannelDatabase(t)
			channel := Channel{Type: constant.ChannelTypeOpenAI, Name: t.Name(), Key: "key-a", Status: common.ChannelStatusEnabled, Models: "checked-status-model", Group: "default", Setting: tc.setting}
			keys := []string{channel.Key}
			if tc.multiKey {
				channel.Key = "key-a\nkey-b"
				channel.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyPollingIndex: 1}
				keys = channel.GetKeys()
			}
			require.NoError(t, channel.Insert())
			InitChannelCache()
			if tc.storedSetting != "" {
				require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("setting", tc.storedSetting).Error)
			}
			replacement := channel
			if tc.cachedSetting != "" {
				replacement.Setting = &tc.cachedSetting
			}
			switch tc.replaceCache {
			case "key":
				replacement.Key = "KEY-a"
			case "headers":
				replacement.HeaderOverride = common.GetPointer(`{"Authorization":"Bearer replacement"}`)
			case "settings":
				replacement.OtherSettings = `{"plugin_config":{"account":"replacement"}}`
			case "models":
				replacement.ChannelInfo.MultiKeyModels = map[int][]string{0: {"replacement-model"}}
			case "users":
				replacement.ChannelInfo.MultiKeyUsers = map[int]int{0: 123}
			case "scope-mode":
				replacement.ChannelInfo.MultiKeyScopeModes = map[int]string{0: "allowlist"}
			case "allocation-id":
				replacement.KeyEntryIDs = slices.Clone(channel.KeyEntryIDs)
				replacement.KeyEntryIDs[0]++
			case "allocation-revision":
				replacement.KeyEntryRevisions = slices.Clone(channel.KeyEntryRevisions)
				replacement.KeyEntryRevisions[0]++
			}
			hasReplacement := tc.replaceCache != "" || tc.cachedSetting != ""
			for index, key := range keys {
				changed := UpdateChannelStatusChecked(context.Background(), channel.Id, key, common.ChannelStatusAutoDisabled, "Insufficient balance", func(current *Channel) bool {
					if hasReplacement {
						// A current cache publisher can replace the snapshot after the
						// database validation, before this completed result is published.
						CacheUpdateChannel(&replacement)
					}
					return current.Key == channel.Key
				})
				require.True(t, changed)
				wantStatus := common.ChannelStatusAutoDisabled
				if index < len(keys)-1 || tc.preserveCacheStatus {
					wantStatus = common.ChannelStatusEnabled
				}
				cached, err := CacheGetChannel(channel.Id)
				require.NoError(t, err)
				assert.Equal(t, wantStatus, cached.Status)
				assert.Equal(t, replacement.Setting, cached.Setting, "status publication must preserve the cached setting bytes")
				if hasReplacement {
					assert.Equal(t, replacement.Key, cached.Key, "a source mismatch must preserve the replacement bytes")
					assert.Equal(t, replacement.HeaderOverride, cached.HeaderOverride)
					assert.Equal(t, replacement.OtherSettings, cached.OtherSettings)
					assert.Equal(t, replacement.ChannelInfo.MultiKeyModels, cached.ChannelInfo.MultiKeyModels)
					assert.Equal(t, replacement.ChannelInfo.MultiKeyUsers, cached.ChannelInfo.MultiKeyUsers)
					assert.Equal(t, replacement.ChannelInfo.MultiKeyScopeModes, cached.ChannelInfo.MultiKeyScopeModes)
					assert.Equal(t, replacement.KeyEntryIDs, cached.KeyEntryIDs)
				}
				if tc.preserveCacheStatus {
					assert.Empty(t, cached.ChannelInfo.MultiKeyStatusList, "an obsolete result must not disable keys in a replacement scope")
				} else if tc.multiKey {
					assert.Equal(t, common.ChannelStatusAutoDisabled, cached.ChannelInfo.MultiKeyStatusList[index])
					assert.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex)
				}
				selected, err := GetRandomSatisfiedChannel(channel.Group, channel.Models, 0, nil)
				require.NoError(t, err)
				if wantStatus == common.ChannelStatusEnabled {
					require.NotNil(t, selected)
					assert.Equal(t, channel.Id, selected.Id)
				} else {
					assert.Nil(t, selected)
				}
			}
		})
	}
}

func TestUpdateChannelAllocationStatusPublication(t *testing.T) {
	useScheduledChannelDatabase(t)
	registerModelTestPlugin(t, taskOnlyPluginSource, "allocation-status-alias")
	generation := jsplugin.DefaultRegistry.Generation()
	channel := Channel{Type: constant.ChannelTypeOpenAI, Name: t.Name(), Key: "key-a\nkey-b", Status: common.ChannelStatusEnabled,
		Models: "allocation-status-model,allocation-alias", Group: "default", Priority: common.GetPointer(int64(10)),
		ModelMapping: common.GetPointer(`{"allocation-alias":"served"}`),
		ChannelInfo:  ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}}
	require.NoError(t, channel.Insert())
	lowerPriority := Channel{Type: constant.ChannelTypeOpenAI, Name: "lower priority", Key: "fallback", Status: common.ChannelStatusEnabled,
		Models: "allocation-status-model", Group: channel.Group}
	require.NoError(t, lowerPriority.Insert())
	InitChannelCache()
	_, aliasFound := ResolveTaskModelAlias(generation, "allocation-alias")
	require.True(t, aliasFound)
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	replacement := *cached
	replacement.ChannelInfo.MultiKeyPollingIndex = 1
	replacement.SetOtherInfo(map[string]any{"cache_note": "preserve"})
	CacheUpdateChannel(&replacement)
	var stale ChannelAllocation
	for index := range 2 {
		cached, err = CacheGetChannel(channel.Id)
		require.NoError(t, err)
		allocation, ok := cached.ChannelAllocationByPosition(index)
		require.True(t, ok)
		if index == 0 {
			stale = allocation
		}
		require.True(t, UpdateChannelAllocationStatus(context.Background(), allocation, common.ChannelStatusAutoDisabled, "unavailable"))
		cached, err = CacheGetChannel(channel.Id)
		require.NoError(t, err)
		stored, err := GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, stored.ChannelInfo.KeyEntryRevision, cached.ChannelInfo.KeyEntryRevision)
		assert.Equal(t, stored.KeyEntryRevisions, cached.KeyEntryRevisions, "the next selection must carry the committed revisions")
		assert.Equal(t, common.ChannelStatusAutoDisabled, cached.ChannelInfo.MultiKeyStatusList[index])
		assert.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex, "status publication preserves the live cursor")
		assert.Equal(t, "preserve", cached.GetOtherInfo()["cache_note"])
		selected, err := GetRandomSatisfiedChannel(channel.Group, "allocation-status-model", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, selected)
		if index == 0 {
			assert.Equal(t, channel.Id, selected.Id)
		} else {
			assert.Equal(t, lowerPriority.Id, selected.Id)
		}
		_, aliasFound = ResolveTaskModelAlias(generation, "allocation-alias")
		assert.Equal(t, index == 0, aliasFound, "task aliases follow committed availability immediately")
	}
	before := channelsRevision(t)
	assert.False(t, UpdateChannelAllocationStatus(context.Background(), stale, common.ChannelStatusEnabled, ""))
	assert.Equal(t, before, channelsRevision(t), "stale results do not publish a sync task")
	cached, err = CacheGetChannel(channel.Id)
	require.NoError(t, err)
	allocation, ok := cached.ChannelAllocationByPosition(0)
	require.True(t, ok)
	require.True(t, UpdateChannelAllocationStatus(context.Background(), allocation, common.ChannelStatusEnabled, ""))
	cached, err = CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, cached.Status)
	assert.NotContains(t, cached.ChannelInfo.MultiKeyStatusList, 0)
	assert.Equal(t, common.ChannelStatusAutoDisabled, cached.ChannelInfo.MultiKeyStatusList[1])
	assert.Equal(t, 1, cached.ChannelInfo.MultiKeyPollingIndex)
	selected, err := GetRandomSatisfiedChannel(channel.Group, "allocation-status-model", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id, "re-enabled routing returns at the channel's original priority")
	_, aliasFound = ResolveTaskModelAlias(generation, "allocation-alias")
	assert.True(t, aliasFound, "re-enabling immediately restores task alias visibility")

	_, err = MutateChannelKeyEntries(context.Background(), channel.Id, cached.ChannelInfo.KeyEntryRevision,
		func(_ *gorm.DB, _ *Channel, entries []ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error) {
			entries[0].Key = "admin-replacement"
			entries[0].ConfigRevision++
			return entries, nil, nil
		})
	require.NoError(t, err)
	cached, err = CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, "admin-replacement\nkey-b", cached.Key, "administrator edits still refresh the full configuration")
}

func TestUpdateChannelAllocationStatusNoOpPreservesCache(t *testing.T) {
	for _, action := range []string{"rejected-check", "stale-revision", "unchanged-status", "admin-no-op", "newer-manual-disable"} {
		t.Run(action, func(t *testing.T) {
			useScheduledChannelDatabase(t)
			channel := Channel{Type: constant.ChannelTypeOpenAI, Name: t.Name(), Key: "original", Status: common.ChannelStatusEnabled,
				Models: "allocation-no-op-model", Group: "default"}
			require.NoError(t, channel.Insert())
			InitChannelCache()
			allocation, ok := channel.ChannelAllocationByPosition(0)
			require.True(t, ok)
			replacement := channel
			replacement.Key = "replacement"
			replacement.Setting = common.GetPointer(`{"future_setting":"preserve"}`)
			target := common.ChannelStatusAutoDisabled
			if action == "stale-revision" {
				allocation.Revision++
			}
			if action == "unchanged-status" {
				target = common.ChannelStatusEnabled
			}
			if action == "newer-manual-disable" {
				replacement.Key = channel.Key
				replacement.Setting = channel.Setting
				replacement.Status = common.ChannelStatusManuallyDisabled
			}
			before := channelsRevision(t)
			if action == "admin-no-op" {
				_, err := MutateChannelKeyEntries(context.Background(), channel.Id, 0,
					func(_ *gorm.DB, _ *Channel, _ []ChannelKeyEntry) ([]ChannelKeyEntry, []ChannelKeyEntry, error) {
						CacheUpdateChannel(&replacement)
						return nil, nil, nil
					})
				require.NoError(t, err)
			} else {
				changed := UpdateChannelAllocationStatusChecked(context.Background(), allocation, target, "unavailable", func(*Channel) bool {
					CacheUpdateChannel(&replacement)
					return action != "rejected-check"
				})
				assert.Equal(t, action == "newer-manual-disable", changed)
			}
			cached, err := CacheGetChannel(channel.Id)
			require.NoError(t, err)
			assert.Equal(t, replacement, *cached, "an ignored status result must preserve the complete cached replacement")
			if action != "newer-manual-disable" {
				assert.Equal(t, before, channelsRevision(t))
				stored, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, channel.Status, stored.Status)
				assert.Equal(t, channel.KeyEntryRevisions, stored.KeyEntryRevisions)
			}
		})
	}
}

// writeAfterNextChannelRead stands for another node: right after the next
// read of the channel, before the writer under test writes, it stores the
// columns write returns for the channel it reads.
func writeAfterNextChannelRead(t *testing.T, channelID int, write func(stored *Channel) map[string]any) {
	t.Helper()
	var done atomic.Bool
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register("test:other_node_write", func(tx *gorm.DB) {
		if !strings.HasSuffix(tx.Statement.Table, "channels") || !done.CompareAndSwap(false, true) {
			return
		}
		var stored Channel
		require.NoError(t, DB.First(&stored, "id = ?", channelID).Error)
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channelID).Updates(write(&stored)).Error)
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove("test:other_node_write") })
}

// A writer of other_info writes back only while no other writer, on any node,
// changed it since the read; otherwise it reads again, so both changes are
// kept. It writes its own columns only, so a stale read never puts back the
// key, counters, models or channel_info another writer stored.
func TestChannelOtherInfoWritesKeepConcurrentChanges(t *testing.T) {
	useScheduledChannelDatabase(t)
	common.MemoryCacheEnabled = false

	t.Run("a status change and a balance query of another node", func(t *testing.T) {
		channel := Channel{Name: "status-race", Key: "original-key", Status: common.ChannelStatusEnabled, Models: "original-model", Group: "default", UsedQuota: 100}
		require.NoError(t, channel.Insert())
		concurrentInfo := ChannelInfo{MultiKeyMode: constant.MultiKeyModePolling, MultiKeyPollingIndex: 1}
		writeAfterNextChannelRead(t, channel.Id, func(stored *Channel) map[string]any {
			info := stored.GetOtherInfo()
			info[channelBalanceDetailKey] = map[string]any{"amount": 7, "currency": "USD"}
			stored.SetOtherInfo(info)
			return map[string]any{
				"other_info":   stored.OtherInfo,
				"key":          "rotated-key",
				"used_quota":   gorm.Expr("used_quota + ?", 250),
				"models":       "concurrent-model",
				"channel_info": concurrentInfo,
			}
		})

		require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))

		var stored Channel
		require.NoError(t, DB.First(&stored, channel.Id).Error)
		assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
		assert.Equal(t, "manual operation", stored.GetOtherInfo()["status_reason"])
		detail := stored.GetBalanceDetail()
		require.NotNil(t, detail, stored.OtherInfo)
		require.NotNil(t, detail.Amount)
		assert.Equal(t, 7.0, *detail.Amount)
		assert.Equal(t, "rotated-key", stored.Key)
		assert.Equal(t, int64(350), stored.UsedQuota)
		assert.Equal(t, "concurrent-model", stored.Models)
		assert.Equal(t, concurrentInfo, stored.ChannelInfo)
	})

	t.Run("a balance query and a status change of another node", func(t *testing.T) {
		channel := Channel{Name: "balance-race", Key: "key", Status: common.ChannelStatusEnabled, Models: "balance-model", Group: "default"}
		require.NoError(t, channel.Insert())
		// A channel stored before other_info was written has it NULL.
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("other_info", gorm.Expr("NULL")).Error)
		writeAfterNextChannelRead(t, channel.Id, func(stored *Channel) map[string]any {
			info := stored.GetOtherInfo()
			info["status_reason"] = "other node"
			stored.SetOtherInfo(info)
			return map[string]any{"other_info": stored.OtherInfo, "status": common.ChannelStatusManuallyDisabled}
		})
		amount := 3.5
		detail, err := UpdateChannelBalanceDetail(channel.Id, func(*ChannelBalanceDetail) (*ChannelBalanceDetail, *float64) {
			return &ChannelBalanceDetail{BalanceResult: jsplugin.BalanceResult{Amount: &amount, Currency: "USD"}}, &amount
		})
		require.NoError(t, err)
		require.NotNil(t, detail)

		var stored Channel
		require.NoError(t, DB.First(&stored, channel.Id).Error)
		assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
		assert.Equal(t, "other node", stored.GetOtherInfo()["status_reason"])
		assert.Equal(t, 3.5, stored.Balance)
		assert.NotZero(t, stored.BalanceUpdatedTime)
		require.NotNil(t, stored.GetBalanceDetail())
		assert.Equal(t, 3.5, *stored.GetBalanceDetail().Amount)

		// Storing the same failure twice writes the same values again, which
		// MySQL reports as no changed row.
		for range 2 {
			_, err = UpdateChannelBalanceDetail(channel.Id, func(stored *ChannelBalanceDetail) (*ChannelBalanceDetail, *float64) {
				stored.Error = "upstream failed"
				return stored, nil
			})
			require.NoError(t, err)
		}
		require.NoError(t, DB.First(&stored, channel.Id).Error)
		assert.Equal(t, "upstream failed", stored.GetBalanceDetail().Error)
		assert.Equal(t, "other node", stored.GetOtherInfo()["status_reason"])
	})
}

func TestAutoDisableKeepsManualDisable(t *testing.T) {
	for _, cacheEnabled := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			multiKey      bool
			initialStatus int
			wantChanged   bool
			wantStatus    int
		}{
			{name: "single key manual disable is kept", initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
			{name: "multi key manual disable is kept", multiKey: true, initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
			{name: "single key enabled is auto disabled", initialStatus: common.ChannelStatusEnabled, wantChanged: true, wantStatus: common.ChannelStatusAutoDisabled},
			{name: "multi key enabled is auto disabled", multiKey: true, initialStatus: common.ChannelStatusEnabled, wantChanged: true, wantStatus: common.ChannelStatusAutoDisabled},
		} {
			t.Run(fmt.Sprintf("cache=%t/%s", cacheEnabled, tc.name), func(t *testing.T) {
				setupChannelStatusTest(t)
				common.MemoryCacheEnabled = cacheEnabled

				channel := Channel{Name: t.Name(), Key: "key-a", Status: tc.initialStatus, Models: "test-model", Group: "default"}
				usingKey := "key-a"
				if tc.multiKey {
					// key-b already failed, so a failure on key-a disables every key.
					channel.Key = "key-a\nkey-b"
					channel.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusAutoDisabled}}
				}
				channel.SetOtherInfo(map[string]any{"status_reason": "manual operation", "status_time": int64(1234)})
				require.NoError(t, channel.Insert())
				InitChannelCache()

				changed := UpdateChannelStatus(channel.Id, usingKey, common.ChannelStatusAutoDisabled, "provider rejected key")
				assert.Equal(t, tc.wantChanged, changed)

				stored, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, tc.wantStatus, stored.Status)
				if !tc.wantChanged {
					assert.Equal(t, "manual operation", stored.GetOtherInfo()["status_reason"])
					assert.Equal(t, float64(1234), stored.GetOtherInfo()["status_time"])
					assert.Equal(t, channel.ChannelInfo.MultiKeyStatusList, stored.ChannelInfo.MultiKeyStatusList)
				}
				var abilities []Ability
				require.NoError(t, DB.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
				require.NotEmpty(t, abilities)
				for _, ability := range abilities {
					assert.False(t, ability.Enabled)
				}
				if cacheEnabled {
					cached, err := CacheGetChannel(channel.Id)
					require.NoError(t, err)
					assert.Equal(t, tc.wantStatus, cached.Status)
					assert.Equal(t, stored.ChannelInfo.MultiKeyStatusList, cached.ChannelInfo.MultiKeyStatusList)
				}
			})
		}
	}
}

// A status set by hand reaches every node at once through the channels topic.
// An automatic change, made for the key a request or a channel test used, is
// left to the periodic channel sync and bumps nothing.
func TestUpdateChannelStatusBumpsChannelsOnlyForChangesByHand(t *testing.T) {
	for _, cacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cacheEnabled), func(t *testing.T) {
			setupChannelStatusTest(t)
			common.MemoryCacheEnabled = cacheEnabled
			revision := func() int64 { return channelsRevision(t) }
			single := Channel{Name: "status-single", Key: "key-a", Status: common.ChannelStatusEnabled, Models: "status-model", Group: "default"}
			multi := Channel{Name: "status-multi", Key: "key-a\nkey-b", Status: common.ChannelStatusEnabled, Models: "status-model", Group: "default",
				ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}}
			require.NoError(t, single.Insert())
			require.NoError(t, multi.Insert())
			InitChannelCache()

			for _, step := range []struct {
				name     string
				id       int
				usingKey string
				status   int
				bump     bool
			}{
				{name: "a request disables a key", id: multi.Id, usingKey: "key-a", status: common.ChannelStatusAutoDisabled},
				{name: "a request disables a single-key channel", id: single.Id, usingKey: "key-a", status: common.ChannelStatusAutoDisabled},
				{name: "a test re-enables the channel", id: single.Id, usingKey: "key-a", status: common.ChannelStatusEnabled},
				{name: "an admin disables a channel", id: single.Id, status: common.ChannelStatusManuallyDisabled, bump: true},
				{name: "an admin disables it again", id: single.Id, status: common.ChannelStatusManuallyDisabled},
				{name: "an admin enables a channel", id: single.Id, status: common.ChannelStatusEnabled, bump: true},
				{name: "an admin disables a multi-key channel", id: multi.Id, status: common.ChannelStatusManuallyDisabled, bump: true},
			} {
				before := revision()
				changed := UpdateChannelStatus(step.id, step.usingKey, step.status, step.name)
				assert.Equal(t, step.bump || step.usingKey != "", changed, step.name)
				assert.Equal(t, step.bump, before != revision(), step.name)
			}
		})
	}
}

// The master runs a plugin's scheduled hook when a key is disabled
// automatically, so a follower's automatic disable of such a key bumps the
// channels topic; the master's own, or one for a plugin without the hook,
// does not.
func TestAutoDisableOfAScheduledPluginKeyBumpsChannelsOnFollowers(t *testing.T) {
	setupChannelStatusTest(t)
	master := common.IsMasterNode
	t.Cleanup(func() { common.IsMasterNode = master })
	for key, hooks := range map[string]string{
		"status-scheduled": `export function scheduled() { return {next: 3600}; }`,
		"status-plain":     "",
	} {
		_, err := jsplugin.DefaultRegistry.Register(`export const meta = {apiVersion: 1, key: "`+key+`", name: "Status", version: "1.0.0", author: {name: "Test"}, relay: [{incoming_path: "/v1/chat/completions", upstream_path: "/chat"}]};`+hooks, jsplugin.Options{})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, jsplugin.DefaultRegistry.Unregister(key)) })
	}
	revision := func() int64 { return channelsRevision(t) }

	for _, tc := range []struct {
		plugin string
		master bool
		bump   bool
	}{
		{plugin: "status-scheduled", bump: true},
		{plugin: "status-scheduled", master: true},
		{plugin: "status-plain"},
	} {
		t.Run(fmt.Sprintf("%s/master=%t", tc.plugin, tc.master), func(t *testing.T) {
			common.IsMasterNode = tc.master
			channel := Channel{Type: constant.ChannelTypeTaskPlugin, Name: t.Name(), Key: "key-a", Status: common.ChannelStatusEnabled, Models: "status-model", Group: "default"}
			channel.SetSetting(dto.ChannelSettings{TaskPluginKey: tc.plugin})
			require.NoError(t, channel.Insert())

			before := revision()
			require.True(t, UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "quota"))
			assert.Equal(t, tc.bump, before != revision())
		})
	}
}
