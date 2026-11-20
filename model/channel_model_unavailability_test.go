package model

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChannelModelUnavailability covers the persisted upstream "model missing"
// observation: it must skip only the recorded channel, model and credential,
// survive a cache reload, refuse to attach to a replaced key or mapping, and be
// restorable by credential fingerprint.
func TestChannelModelUnavailability(t *testing.T) {
	for _, memoryCache := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%v", memoryCache), func(t *testing.T) {
			useScheduledChannelDatabase(t)
			common.MemoryCacheEnabled = memoryCache

			priority, weight := int64(0), uint(1)
			single := Channel{
				Type: 1, Status: common.ChannelStatusEnabled, Name: "single",
				Key: "sk-single", Models: "gpt-6-astra,other", Group: "g1",
				Priority: &priority, Weight: &weight,
			}
			require.NoError(t, single.Insert())
			multi := Channel{
				Type: 1, Status: common.ChannelStatusEnabled, Name: "multi",
				Key: "sk-a\nsk-b", Models: "gpt-6-astra,other", Group: "g2",
				Priority: &priority, Weight: &weight,
			}
			multi.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 2}
			require.NoError(t, multi.Insert())
			mapped := Channel{
				Type: 1, Status: common.ChannelStatusEnabled, Name: "mapped",
				Key: "sk-mapped", Models: "alias,other", Group: "g3",
				Priority: &priority, Weight: &weight,
			}
			mapping := `{"alias":"upstream-a"}`
			mapped.ModelMapping = &mapping
			require.NoError(t, mapped.Insert())
			InitChannelCache()

			// A definite upstream 404 marks only that credential and the model the
			// attempt sent.
			written, err := RecordChannelModelUnavailability(single.Id, "gpt-6-astra", "sk-single", "", "gpt-6-astra", http.StatusNotFound, ChannelModelUnavailableEvidenceCode)
			require.NoError(t, err)
			assert.True(t, written)
			stored, err := GetChannelById(single.Id, true)
			require.NoError(t, err)
			require.Len(t, stored.ChannelInfo.ModelUnavailable, 1)
			assert.Equal(t, "gpt-6-astra", stored.ChannelInfo.ModelUnavailable[0].UpstreamModel)
			assert.Equal(t, ChannelKeyFingerprint("sk-single"), stored.ChannelInfo.ModelUnavailable[0].KeyFingerprint)
			assert.Equal(t, http.StatusNotFound, stored.ChannelInfo.ModelUnavailable[0].StatusCode)
			metadata, err := common.Marshal(stored.ChannelInfo)
			require.NoError(t, err)
			assert.NotContains(t, string(metadata), "sk-single", "the observation never stores the credential")
			assert.Equal(t, "gpt-6-astra,other", stored.Models, "the configured model list is untouched")

			InitChannelCache()
			cached, err := CacheGetChannel(single.Id)
			require.NoError(t, err)
			assert.True(t, cached.ModelUnavailableForServingKeys("gpt-6-astra"))
			assert.False(t, cached.ModelUnavailableForServingKeys("other"))
			available, _ := ChannelSatisfiesFilters(cached, "gpt-6-astra", nil)
			assert.False(t, available, "a pinned or affinity candidate re-checks the observation")
			selected, err := GetRandomSatisfiedChannel("g1", "gpt-6-astra", 0, nil)
			require.NoError(t, err)
			assert.Nil(t, selected)
			_, _, keyErr := cached.GetNextEnabledKeyFor("gpt-6-astra", 0)
			require.NotNil(t, keyErr)
			assert.Equal(t, types.ErrorCodeChannelKeyScopeUnsatisfied, keyErr.GetErrorCode(), "the retryable key-scope outcome lets routing continue elsewhere")
			selected, err = GetRandomSatisfiedChannel("g1", "other", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, single.Id, selected.Id, "other models keep using the channel")

			// A late response for a replaced credential marks nothing.
			_, err = UpdateChannelLocked(single.Id, func(channel *Channel) error {
				channel.Key = "sk-replaced"
				return nil
			})
			require.NoError(t, err)
			written, err = RecordChannelModelUnavailability(single.Id, "gpt-6-astra", "sk-single", "", "gpt-6-astra", http.StatusNotFound, ChannelModelUnavailableEvidenceCode)
			require.NoError(t, err)
			assert.False(t, written, "a stale observation never attaches to a replacement key")

			// Restoring by fingerprint clears the recorded credential only.
			restored, err := RestoreChannelModelUnavailability(single.Id, "gpt-6-astra", ChannelKeyFingerprint("sk-single"))
			require.NoError(t, err)
			assert.True(t, restored)
			cached, err = CacheGetChannel(single.Id)
			require.NoError(t, err)
			assert.False(t, cached.ModelUnavailableForServingKeys("gpt-6-astra"))
			selected, err = GetRandomSatisfiedChannel("g1", "gpt-6-astra", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, selected)

			// Multi-key isolation: only the recorded credential is skipped.
			written, err = RecordChannelModelUnavailability(multi.Id, "gpt-6-astra", "sk-a", "", "gpt-6-astra", http.StatusGone, ChannelModelUnavailableEvidenceCode)
			require.NoError(t, err)
			require.True(t, written)
			cached, err = CacheGetChannel(multi.Id)
			require.NoError(t, err)
			assert.False(t, cached.ModelUnavailableForServingKeys("gpt-6-astra"), "another credential still serves the model")
			key, index, keyErr := cached.GetNextEnabledKeyFor("gpt-6-astra", 0)
			require.Nil(t, keyErr)
			assert.Equal(t, 1, index)
			assert.Equal(t, "sk-b", key)
			key, index, keyErr = cached.GetNextEnabledKeyFor("other", 0)
			require.Nil(t, keyErr)
			assert.Equal(t, 0, index, "the marked credential still serves other models")
			assert.Equal(t, "sk-a", key)

			// A changed mapping must not inherit the observation of the old target.
			written, err = RecordChannelModelUnavailability(mapped.Id, "alias", "sk-mapped", mapping, "upstream-a", http.StatusNotFound, ChannelModelUnavailableEvidenceMessage)
			require.NoError(t, err)
			require.True(t, written)
			cached, err = CacheGetChannel(mapped.Id)
			require.NoError(t, err)
			assert.True(t, cached.ModelUnavailableForServingKeys("alias"))
			assert.Equal(t, "upstream-a", cached.ChannelInfo.ModelUnavailable[0].UpstreamModel)
			newMapping := `{"alias":"upstream-b"}`
			mapped.ModelMapping = &newMapping
			require.NoError(t, mapped.UpdateConfig())
			InitChannelCache()
			cached, err = CacheGetChannel(mapped.Id)
			require.NoError(t, err)
			assert.False(t, cached.ModelUnavailableForServingKeys("alias"), "a remapped alias probes again")

			// A response whose model no longer matches the current mapping marks nothing.
			written, err = RecordChannelModelUnavailability(mapped.Id, "alias", "sk-mapped", newMapping, "upstream-a", http.StatusNotFound, ChannelModelUnavailableEvidenceMessage)
			require.NoError(t, err)
			assert.False(t, written)

			// A stale polling snapshot and legacy scope edit must preserve new observations.
			_, err = UpdateChannelLocked(multi.Id, func(channel *Channel) error {
				channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled, 9: common.ChannelStatusManuallyDisabled}
				channel.ChannelInfo.MultiKeyDisabledReason = map[int]string{0: "manual"}
				channel.ChannelInfo.MultiKeyDisabledTime = map[int]int64{0: 123}
				return nil
			})
			require.NoError(t, err)
			multi.ChannelInfo.MultiKeyPollingIndex = 1
			require.NoError(t, multi.SaveChannelInfo())
			multi.ChannelInfo.MultiKeyModels = map[int][]string{0: {"gpt-6-astra"}}
			require.NoError(t, multi.Update())
			stored, err = GetChannelById(multi.Id, true)
			require.NoError(t, err)
			require.Len(t, stored.ChannelInfo.ModelUnavailable, 1)
			assert.Equal(t, map[int]int{0: common.ChannelStatusManuallyDisabled}, stored.ChannelInfo.MultiKeyStatusList)
			assert.Equal(t, "manual", stored.ChannelInfo.MultiKeyDisabledReason[0])
			assert.Equal(t, int64(123), stored.ChannelInfo.MultiKeyDisabledTime[0])
			_, err = UpdateChannelLocked(multi.Id, func(channel *Channel) error {
				channel.Key = "sk-b\nsk-a"
				channel.ChannelInfo.MultiKeyStatusList = nil
				return nil
			})
			require.NoError(t, err)
			InitChannelCache()
			cached, err = CacheGetChannel(multi.Id)
			require.NoError(t, err)
			key, index, keyErr = cached.GetNextEnabledKeyFor("gpt-6-astra", 0)
			require.Nil(t, keyErr)
			assert.Equal(t, "sk-b", key, "reordering never moves the marker onto another credential")
			assert.Equal(t, 0, index)

			// Public routing ignores another user's unmarked private credential.
			bound := Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "bound", Key: "sk-public\nsk-private", Models: "gpt-6-astra", Group: "bound-group", Priority: &priority, Weight: &weight,
				ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyUsers: map[int]int{1: 88}}}
			require.NoError(t, bound.Insert())
			InitChannelCache()
			written, err = RecordChannelModelUnavailability(bound.Id, "gpt-6-astra", "sk-public", "", "gpt-6-astra", http.StatusNotFound, ChannelModelUnavailableEvidenceCode)
			require.NoError(t, err)
			require.True(t, written)
			selected, err = GetRandomSatisfiedChannel("bound-group", "gpt-6-astra", 0, nil)
			require.NoError(t, err)
			assert.Nil(t, selected)
			cached, err = CacheGetChannel(bound.Id)
			require.NoError(t, err)
			available, _ = ChannelSatisfiesFilters(cached, "gpt-6-astra", nil, 77)
			assert.False(t, available)
			available, _ = ChannelSatisfiesFilters(cached, "gpt-6-astra", nil, 88)
			assert.True(t, available)
			assert.NotNil(t, GetUserBoundChannel(88, "gpt-6-astra"))
			assert.Nil(t, GetUserBoundChannel(77, "gpt-6-astra"))
			fallback := Channel{Type: 1, Status: common.ChannelStatusEnabled, Name: "fallback", Key: "sk-fallback", Models: "gpt-6-astra", Group: "bound-group", Priority: &priority, Weight: &weight}
			require.NoError(t, fallback.Insert())
			InitChannelCache()
			selected, err = GetUntriedSatisfiedChannel("bound-group", "gpt-6-astra", nil, nil)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, fallback.Id, selected.Id)
			restored, err = RestoreChannelModelUnavailability(bound.Id, "gpt-6-astra", ChannelKeyFingerprint("sk-public"))
			require.NoError(t, err)
			require.True(t, restored)
			// A delayed refresh reads current state, so it cannot resurrect the marker.
			require.NoError(t, CacheRefreshChannelModelUnavailability(bound.Id))
			cached, err = CacheGetChannel(bound.Id)
			require.NoError(t, err)
			assert.False(t, cached.ModelUnavailableForServingKeys("gpt-6-astra", 0))
		})
	}
}
