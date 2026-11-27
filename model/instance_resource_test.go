package model

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// The public instance summary is the only place ordinary console users can see
// CPU/memory/storage. These tests pin the two properties a change can silently
// break: the payload never leaks node identity, and unknown samples are not
// rendered as zero usage.

func instanceInfoFixture(cpu float64, memory float64, storage float64) string {
	return `{"schema_version":1,"node":{"name":"internal-host-01"},"host":{"hostname":"internal-host-01"},` +
		`"resources":{"cpu":{"usage_percent":` + formatFloat(cpu) + `,"available":true},` +
		`"memory":{"usage_percent":` + formatFloat(memory) + `,"available":true},` +
		`"storage":{"used_percent":` + formatFloat(storage) + `,"available":true}}}`
}

// instanceInfoUnavailableFixture models a node whose monitor was disabled at
// sample time: every resource is explicitly marked unavailable.
func instanceInfoUnavailableFixture() string {
	return `{"schema_version":1,"resources":{` +
		`"cpu":{"usage_percent":0,"available":false},` +
		`"memory":{"usage_percent":0,"available":false},` +
		`"storage":{"used_percent":0,"available":false}}}`
}

// instanceInfoLegacyFixture omits the validity flag entirely, as payloads
// written before the field existed do.
func instanceInfoLegacyFixture(cpu float64, storage float64) string {
	return `{"schema_version":1,"resources":{` +
		`"cpu":{"usage_percent":` + formatFloat(cpu) + `},` +
		`"memory":{"usage_percent":` + formatFloat(cpu) + `},` +
		`"storage":{"used_percent":` + formatFloat(storage) + `}}}`
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func TestInstanceResourceSummaryNeverLeaksNodeIdentity(t *testing.T) {
	instances := []*SystemInstance{
		{
			NodeName:   "internal-host-01",
			Info:       instanceInfoFixture(12.5, 33.5, 44.5),
			LastSeenAt: 1000,
		},
	}
	bindings := []ApiInfoBinding{
		{ID: 7, NodeName: "internal-host-01", Route: "CN2 GIA", Name: "https://api.example.com"},
		{ID: 9, NodeName: "internal-host-01", Route: "Backup", Name: "https://api2.example.com"},
	}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	assert.Equal(t, "api-7", instance.Key, "key must come from the persistent entry id")
	assert.Equal(t, "CN2 GIA", instance.Name)
	assert.Equal(t, []string{"https://api.example.com", "https://api2.example.com"}, instance.Domains)
	assert.Equal(t, []int{7, 9}, instance.EntryIDs)
	assert.Equal(t, InstanceStatusHealthy, instance.Status)

	// Node name and hostname must never appear in the projection.
	assert.NotContains(t, instance.Key, "internal-host")
	assert.NotContains(t, instance.Name, "internal-host")
	for _, domain := range instance.Domains {
		assert.NotContains(t, domain, "internal-host")
	}
}

func TestInstanceResourceSummaryKeyIsStableAcrossReordering(t *testing.T) {
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoFixture(10, 20, 30), LastSeenAt: 1000},
	}
	forward := []ApiInfoBinding{
		{ID: 3, NodeName: "host-a", Route: "First", Name: "https://one.example.com"},
		{ID: 8, NodeName: "host-a", Route: "Second", Name: "https://two.example.com"},
	}
	reversed := []ApiInfoBinding{
		{ID: 8, NodeName: "host-a", Route: "Second", Name: "https://two.example.com"},
		{ID: 3, NodeName: "host-a", Route: "First", Name: "https://one.example.com"},
	}

	first := BuildInstanceResourceSummary(instances, forward, 1010)
	second := BuildInstanceResourceSummary(instances, reversed, 1010)

	require.Len(t, first.Instances, 1)
	require.Len(t, second.Instances, 1)
	// The group key is the smallest persistent id, so reordering does not
	// change which instance an API card is attached to.
	assert.Equal(t, first.Instances[0].Key, second.Instances[0].Key)
	assert.Equal(t, "api-3", first.Instances[0].Key)
	assert.Equal(t, []int{3, 8}, first.Instances[0].EntryIDs)
	assert.Equal(t, []int{8, 3}, second.Instances[0].EntryIDs)
}

func TestInstanceResourceSummaryUnavailableSampleIsNotZero(t *testing.T) {
	// A payload written while monitoring was disabled carries explicit
	// available:false. Those values must surface as unknown, never as 0% usage.
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoUnavailableFixture(), LastSeenAt: 1000},
	}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	assert.Nil(t, instance.Resources.CPUPercent)
	assert.Nil(t, instance.Resources.MemoryPercent)
	assert.Nil(t, instance.Resources.StoragePercent)
	assert.Equal(t, InstanceStatusUnknown, instance.Status)
}

func TestInstanceResourceSummaryKeepsLegitimateZero(t *testing.T) {
	// An idle CPU really is 0%. With available:true that value must be kept
	// and the instance reported healthy, not downgraded to unknown.
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoFixture(0, 42, 55), LastSeenAt: 1000},
	}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	require.NotNil(t, instance.Resources.CPUPercent)
	assert.Equal(t, 0.0, *instance.Resources.CPUPercent)
	require.NotNil(t, instance.Resources.MemoryPercent)
	assert.Equal(t, 42.0, *instance.Resources.MemoryPercent)
	assert.Equal(t, InstanceStatusHealthy, instance.Status)
}

func TestInstanceResourceSummaryStorageKnownWhileCPUMemoryUnavailable(t *testing.T) {
	// Disk is sampled independently of the CPU/memory monitor, so a positive
	// disk reading must not imply the node is healthy overall.
	info := `{"schema_version":1,"resources":{` +
		`"cpu":{"usage_percent":0,"available":false},` +
		`"memory":{"usage_percent":0,"available":false},` +
		`"storage":{"used_percent":61.5,"available":true}}}`
	instances := []*SystemInstance{{NodeName: "host-a", Info: info, LastSeenAt: 1000}}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	assert.Nil(t, instance.Resources.CPUPercent)
	assert.Nil(t, instance.Resources.MemoryPercent)
	require.NotNil(t, instance.Resources.StoragePercent)
	assert.Equal(t, 61.5, *instance.Resources.StoragePercent)
	// Partial knowledge is still "known": the status reflects that at least
	// one approved resource was measured while the others stay unknown.
	assert.Equal(t, InstanceStatusHealthy, instance.Status)
}

func TestInstanceResourceSummaryLegacyPayloadUsesPositiveOnlyFallback(t *testing.T) {
	// Without validity metadata an untagged 0 is ambiguous, so it stays
	// unknown; a positive value is still usable.
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoLegacyFixture(0, 30), LastSeenAt: 1000},
	}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	assert.Nil(t, instance.Resources.CPUPercent)
	assert.Nil(t, instance.Resources.MemoryPercent)
	require.NotNil(t, instance.Resources.StoragePercent)
	assert.Equal(t, 30.0, *instance.Resources.StoragePercent)
}

func TestInstanceResourceSummaryMissingInstanceIsUnknown(t *testing.T) {
	// A bound entry whose instance has never reported is unknown, not disabled:
	// the reading process cannot know why the node is absent.
	bindings := []ApiInfoBinding{{ID: 4, NodeName: "gone", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(nil, bindings, 1010)
	require.Len(t, summary.Instances, 1)
	assert.Equal(t, InstanceStatusUnknown, summary.Instances[0].Status)
	assert.Equal(t, []int{4}, summary.Instances[0].EntryIDs)
	assert.Nil(t, summary.Instances[0].ObservedAt)
}

func TestInstanceResourceSummaryUsesSamplingNodeMonitorState(t *testing.T) {
	// The stored record says the sampling node had monitoring disabled. That
	// verdict must be used even though the reading process has no say here.
	info := `{"schema_version":1,"resources":{` +
		`"monitoring":false,` +
		`"cpu":{"usage_percent":0,"available":false},` +
		`"memory":{"usage_percent":0,"available":false},` +
		`"storage":{"used_percent":40,"available":true}}}`
	instances := []*SystemInstance{{NodeName: "host-a", Info: info, LastSeenAt: 1000}}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	instance := summary.Instances[0]
	assert.Equal(t, InstanceStatusDisabled, instance.Status)
	assert.True(t, instance.Disabled)
	assert.Equal(t, InstanceMonitoringDisabled, summary.Monitoring)
	// Storage remains reported alongside the disabled state rather than being
	// discarded.
	require.NotNil(t, instance.Resources.StoragePercent)
	assert.Equal(t, 40.0, *instance.Resources.StoragePercent)
}

func TestInstanceResourceSummaryLegacyPayloadIsNotReportedDisabled(t *testing.T) {
	// A payload without a monitoring key predates the field; it must not be
	// labelled disabled merely because the key is absent.
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoLegacyFixture(12, 30), LastSeenAt: 1000},
	}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	assert.NotEqual(t, InstanceStatusDisabled, summary.Instances[0].Status)
	// Without a recorded monitor state the aggregate is explicitly unknown
	// rather than guessed from local configuration.
	assert.Equal(t, InstanceMonitoringUnknown, summary.Monitoring)
}

func TestInstanceResourceSummaryStaleInstance(t *testing.T) {
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoFixture(10, 20, 30), LastSeenAt: 1},
	}
	bindings := []ApiInfoBinding{{ID: 2, NodeName: "host-a", Route: "Main", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, SystemInstanceStaleAfterSeconds+10)

	require.Len(t, summary.Instances, 1)
	assert.Equal(t, InstanceStatusStale, summary.Instances[0].Status)
	assert.True(t, summary.Instances[0].Stale)
	assert.NotNil(t, summary.Instances[0].ObservedAt)
}

func TestInstanceResourceSummaryUnboundEntriesProduceNoInstance(t *testing.T) {
	instances := []*SystemInstance{{NodeName: "host-a", Info: instanceInfoFixture(10, 20, 30), LastSeenAt: 1000}}
	bindings := []ApiInfoBinding{{ID: 1, NodeName: "", Route: "Unbound", Name: "https://api.example.com"}}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	assert.Empty(t, summary.Instances)
}

func TestInstanceResourceSummaryMultipleDomainsToOneInstance(t *testing.T) {
	instances := []*SystemInstance{{NodeName: "host-a", Info: instanceInfoFixture(5, 6, 7), LastSeenAt: 1000}}
	bindings := []ApiInfoBinding{
		{ID: 5, NodeName: "host-a", Route: "Main", Name: "https://a.example.com"},
		{ID: 6, NodeName: "host-a", Route: "Alt", Name: "https://b.example.com"},
		{ID: 7, NodeName: "host-a", Route: "Alt2", Name: "https://c.example.com"},
	}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	// Several configured domains, one instance.
	require.Len(t, summary.Instances, 1)
	assert.Equal(t, []string{
		"https://a.example.com",
		"https://b.example.com",
		"https://c.example.com",
	}, summary.Instances[0].Domains)
	assert.Equal(t, []int{5, 6, 7}, summary.Instances[0].EntryIDs)
	assert.Equal(t, "api-5", summary.Instances[0].Key)
}

func TestInstanceResourceSummarySeparatesEntriesSharingURL(t *testing.T) {
	instances := []*SystemInstance{
		{NodeName: "host-a", Info: instanceInfoFixture(12, 34, 56), LastSeenAt: 1000},
		{NodeName: "host-b", Info: instanceInfoFixture(78, 89, 90), LastSeenAt: 1000},
	}
	bindings := []ApiInfoBinding{
		{ID: 1, NodeName: "host-a", Route: "First", Name: "https://api.example.com"},
		{ID: 2, NodeName: "", Route: "Unbound", Name: "https://api.example.com"},
		{ID: 3, NodeName: "host-b", Route: "Second", Name: "https://api.example.com"},
		{ID: 4, NodeName: "host-a", Route: "Alias", Name: "https://api.example.com"},
	}

	summary := BuildInstanceResourceSummary(instances, bindings, 1010)

	require.Len(t, summary.Instances, 2)
	first := summary.Instances[0]
	second := summary.Instances[1]
	assert.Equal(t, []int{1, 4}, first.EntryIDs)
	assert.Equal(t, []int{3}, second.EntryIDs)
	require.NotNil(t, first.Resources.CPUPercent)
	require.NotNil(t, second.Resources.CPUPercent)
	assert.Equal(t, 12.0, *first.Resources.CPUPercent)
	assert.Equal(t, 78.0, *second.Resources.CPUPercent)
}

func TestInstanceResourceSummaryOmitsLegacyEntryMembership(t *testing.T) {
	bindings := []ApiInfoBinding{
		{ID: 0, NodeName: "host-a", Route: "Legacy", Name: "https://api.example.com"},
		{ID: -1, NodeName: "host-a", Route: "Invalid", Name: "https://api.example.com"},
		{ID: 7, NodeName: "host-a", Route: "Saved", Name: "https://api.example.com"},
		{ID: 7, NodeName: "host-a", Route: "Duplicate", Name: "https://api.example.com"},
	}

	summary := BuildInstanceResourceSummary(nil, bindings, 1010)

	require.Len(t, summary.Instances, 1)
	assert.Equal(t, []int{7}, summary.Instances[0].EntryIDs)
	assert.Equal(t, "api-7", summary.Instances[0].Key)
	assert.Empty(t, BuildInstanceResourceSummary(nil, bindings[:2], 1010).Instances)
}

func TestInstanceResourceSummaryPublicJSONIsAllowlisted(t *testing.T) {
	// The public payload carries an exact field set. Anything else - notably
	// stored node identity, hostname, node address/URL or sync error - must be
	// impossible to add without failing this test.
	info := `{"schema_version":1,` +
		`"node":{"name":"secret-node","source":"config"},` +
		`"host":{"hostname":"secret-host"},` +
		`"runtime":{"version":"1.0.0","goos":"linux","goarch":"amd64"},` +
		`"role":{"is_master":true},` +
		`"sync":{"channels":{"error":"boom","state":"failed"}},` +
		`"resources":{"monitoring":true,` +
		`"cpu":{"usage_percent":11,"available":true},` +
		`"memory":{"usage_percent":22,"available":true},` +
		`"storage":{"used_percent":33,"available":true}}}`
	instances := []*SystemInstance{{NodeName: "secret-node", Info: info, LastSeenAt: 1000}}
	bindings := []ApiInfoBinding{{ID: 3, NodeName: "secret-node", Route: "Main", Name: "https://api.example.com"}}

	payload, err := common.Marshal(BuildInstanceResourceSummary(instances, bindings, 1010))
	require.NoError(t, err)
	body := string(payload)

	// Nothing from the stored instance record may appear.
	for _, leaked := range []string{
		"secret-node", "secret-host", "goos", "goarch", "is_master",
		"boom", "failed", "schema_version", "runtime", "sync",
	} {
		assert.NotContains(t, body, leaked)
	}

	// The instance object exposes exactly the approved fields, and so does the
	// per-resource object.
	var decoded struct {
		Instances  []map[string]any `json:"instances"`
		Monitoring string           `json:"monitoring"`
	}
	require.NoError(t, common.UnmarshalJsonStr(body, &decoded))
	require.Len(t, decoded.Instances, 1)
	assert.Equal(t, map[string]struct{}{
		"key": {}, "name": {}, "status": {}, "domains": {}, "entry_ids": {},
		"resources": {}, "observed_at": {}, "stale": {}, "disabled": {},
	}, keySet(decoded.Instances[0]))
	assert.Equal(t, []any{float64(3)}, decoded.Instances[0]["entry_ids"])

	resources, ok := decoded.Instances[0]["resources"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]struct{}{
		"cpu_percent": {}, "memory_percent": {}, "storage_percent": {},
	}, keySet(resources))

	assert.Equal(t, InstanceMonitoringEnabled, decoded.Monitoring)
}

func keySet(m map[string]any) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// Ranking tests pin the server-side masking contract: what the payload
// contains for each viewer kind, and that masked rows carry no identity key.
func rankingRowsFixture() []UserRankingRow {
	return []UserRankingRow{
		{UserID: 11, Requests: 30, Tokens: 3000, Quota: 300},
		{UserID: 22, Requests: 20, Tokens: 2000, Quota: 200},
		{UserID: 33, Requests: 10, Tokens: 1000, Quota: 100},
	}
}

func rankingNamesFixture() map[int]string {
	return map[int]string{
		11: "alice",
		22: "bob",
		// 33 is intentionally absent: the account no longer exists.
	}
}

func TestBuildUserRankingViewAdminSeesEveryName(t *testing.T) {
	views := BuildUserRankingView(
		RankingViewer{UserID: 999, IsAdmin: true},
		rankingRowsFixture(),
		rankingNamesFixture(),
	)

	require.Len(t, views, 3)
	assert.Equal(t, "alice", views[0].DisplayName)
	assert.Equal(t, 11, views[0].UserID)
	assert.Equal(t, "bob", views[1].DisplayName)
	// A deleted account keeps only the generic label: no username and no id,
	// even for a viewer allowed to see every live account.
	assert.Equal(t, rankingDeletedUserLabel, views[2].DisplayName)
	assert.Zero(t, views[2].UserID)
	for _, view := range views {
		assert.False(t, view.Masked)
	}
}

func TestBuildUserRankingViewOrdinaryUserSeesPartialOtherNames(t *testing.T) {
	views := BuildUserRankingView(
		RankingViewer{UserID: 22},
		rankingRowsFixture(),
		rankingNamesFixture(),
	)

	require.Len(t, views, 3)
	assert.Equal(t, "a***e", views[0].DisplayName)
	assert.True(t, views[0].Masked)
	assert.Zero(t, views[0].UserID)

	assert.Equal(t, "bob", views[1].DisplayName)
	assert.False(t, views[1].Masked)
	assert.Equal(t, 22, views[1].UserID)

	assert.Equal(t, rankingMaskedDisplayName, views[2].DisplayName)
	assert.True(t, views[2].Masked)
}

func TestBuildUserRankingViewPartiallyMasksShortAndUnicodeNames(t *testing.T) {
	for _, scenario := range []struct {
		name string
		want string
	}{
		{name: "a", want: "*"},
		{name: "ab", want: "a***"},
		{name: "alice", want: "a***e"},
		{name: "张", want: "*"},
		{name: "张三", want: "张***"},
		{name: "张三丰", want: "张***丰"},
		{name: "🦊星🌙", want: "🦊***🌙"},
		{name: "e\u0301", want: "e***"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			views := BuildUserRankingView(
				RankingViewer{UserID: 22},
				[]UserRankingRow{{UserID: 11}},
				map[int]string{11: scenario.name},
			)
			require.Len(t, views, 1)
			assert.Equal(t, scenario.want, views[0].DisplayName)
			assert.True(t, views[0].Masked)
			assert.Zero(t, views[0].UserID)
		})
	}
}

func TestBuildUserRankingViewAnonymousSeesNothing(t *testing.T) {
	views := BuildUserRankingView(
		RankingViewer{IsAnonymous: true},
		rankingRowsFixture(),
		rankingNamesFixture(),
	)

	require.Len(t, views, 3)
	for _, view := range views {
		assert.True(t, view.Masked)
		assert.Equal(t, rankingMaskedDisplayName, view.DisplayName)
		assert.Zero(t, view.UserID)
	}
}

func TestUserRankingViewJSONHidesMaskedIdentity(t *testing.T) {
	views := BuildUserRankingView(
		RankingViewer{UserID: 22},
		rankingRowsFixture(),
		rankingNamesFixture(),
	)
	payload, err := common.Marshal(views)
	require.NoError(t, err)

	body := string(payload)
	assert.NotContains(t, body, "alice")
	assert.Contains(t, body, "bob")
	assert.NotContains(t, body, `"user_id":11`)
	assert.NotContains(t, body, `"user_id":33`)
	assert.Contains(t, body, `"user_id":22`)
	assert.NotContains(t, body, `"user_id":0`)
}

// TestUserRankingViewJSONHidesDeletedAccountID pins the admin case that a
// deleted account must not become identifiable just because the viewer is
// allowed to see every live user.
func TestUserRankingViewJSONHidesDeletedAccountID(t *testing.T) {
	views := BuildUserRankingView(
		RankingViewer{UserID: 999, IsAdmin: true},
		rankingRowsFixture(),
		rankingNamesFixture(),
	)
	payload, err := common.Marshal(views)
	require.NoError(t, err)

	body := string(payload)
	assert.Contains(t, body, "alice")
	assert.Contains(t, body, rankingDeletedUserLabel)
	// The deleted row keeps neither its id nor a stale username.
	assert.NotContains(t, body, "33")
	assert.NotContains(t, body, `"user_id":0`)
}

func TestVisibleUserIDsResolvesCurrentNamesOnlyForAuthenticatedViewers(t *testing.T) {
	anonymous := VisibleUserIDs(RankingViewer{IsAnonymous: true}, rankingRowsFixture())
	assert.Empty(t, anonymous)

	self := VisibleUserIDs(RankingViewer{UserID: 22}, rankingRowsFixture())
	assert.Equal(t, []int{11, 22, 33}, self)

	admin := VisibleUserIDs(RankingViewer{UserID: 999, IsAdmin: true}, rankingRowsFixture())
	assert.Equal(t, []int{11, 22, 33}, admin)
}

func TestRankingViewerCanSeeNameForZeroUserID(t *testing.T) {
	assert.False(t, (RankingViewer{UserID: 0}).CanSeeName(0))
	assert.False(t, (RankingViewer{UserID: 5}).CanSeeName(0))
	assert.True(t, (RankingViewer{UserID: 5}).CanSeeName(5))
	assert.False(t, (RankingViewer{UserID: 5}).CanSeeName(6))
}
