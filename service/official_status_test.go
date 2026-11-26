package service

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type officialStatusTransport func(*http.Request) (*http.Response, error)

func (transport officialStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestOfficialStatusFeeds(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		invalid          bool
	}{
		{"summary", `{"page":{"id":"vendor","updated_at":"2026-11-19T10:00:00Z"},"status":{"indicator":"minor"},"components":[{"id":"api","name":"API","status":"degraded_performance"}],"incidents":[{"id":"delays","name":"API delays","status":"investigating"}]}`, "degraded_performance", false},
		{"empty object is not healthy", `{}`, "", true},
		{"HTML error", `<html>error</html>`, "", true},
		{"unknown indicator", `{"page":{"id":"vendor"},"status":{"indicator":"new_status"},"components":[],"incidents":[]}`, "unknown", false},
		{"another status page is not accepted", `{"page":{"id":"unrelated"},"status":{"indicator":"none"}}`, "", true},
		{"Vertex incidents are not Gemini API status", `[]`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: officialStatusTransport(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, "https://vendor.example/feed", req.URL.String())
				assert.Empty(t, req.Header.Get("Authorization"))
				assert.Empty(t, req.Header.Get("Cookie"))
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			entry, err := fetchOfficialStatus(context.Background(), client, officialStatusSource{id: "vendor", pageID: "vendor", feedURL: "https://vendor.example/feed"})
			if test.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, entry.Status)
		})
	}
}

func TestOfficialStatusPreservesIdentifiersWithinResponseBounds(t *testing.T) {
	components := make([]OfficialStatusComponent, 101)
	for i := range components {
		components[i] = OfficialStatusComponent{ID: strconv.Itoa(i), Name: "API", Status: "operational"}
	}
	incidents := make([]OfficialStatusIncident, 21)
	for i := range incidents {
		incidents[i] = OfficialStatusIncident{ID: strconv.Itoa(i), Name: "API delays", Status: "investigating"}
	}
	body, err := common.Marshal(map[string]any{
		"page":       map[string]string{"id": "vendor", "updated_at": "2026-11-19T10:00:00Z"},
		"status":     map[string]string{"indicator": "minor"},
		"components": components,
		"incidents":  incidents,
	})
	require.NoError(t, err)
	client := &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	entry, err := fetchOfficialStatus(context.Background(), client, officialStatusSource{feedURL: "https://vendor.example/feed"})
	require.NoError(t, err)
	assert.Equal(t, "2026-11-19T10:00:00Z", entry.SourceUpdatedAt)
	require.Len(t, entry.Components, 100)
	require.Len(t, entry.Incidents, 20)
	assert.Equal(t, components[:100], entry.Components)
	assert.Equal(t, incidents[:20], entry.Incidents)
}

func TestOfficialStatusCacheRetainsLastSuccessAndRecovers(t *testing.T) {
	var attempts atomic.Int32
	var unavailable atomic.Bool
	clock := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	cache := officialStatusCache{
		now:     func() time.Time { return clock },
		sources: []officialStatusSource{{id: "vendor", pageURL: "https://vendor.example", feedURL: "https://vendor.example/feed"}},
		client: &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			if unavailable.Load() {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"page":{"id":"vendor"},"status":{"indicator":"none"},"components":[],"incidents":[]}`))}, nil
		})},
	}
	first, err := cache.read(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, "operational", first[0].Status)
	_, err = cache.read(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 1, attempts.Load())
	unavailable.Store(true)
	clock = clock.Add(officialStatusCacheTTL)
	stale, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.False(t, stale[0].Stale, "a failed check does not make five-minute-old data stale")
	assert.True(t, stale[0].Unavailable)
	assert.Equal(t, "timeout", stale[0].FetchError)
	assert.Equal(t, first[0].FetchedAt, stale[0].FetchedAt)
	assert.Greater(t, stale[0].CheckedAt, stale[0].FetchedAt)
	assert.False(t, first[0].Unavailable, "earlier snapshots remain immutable")
	clock = clock.Add(25 * time.Minute)
	boundary, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.False(t, boundary[0].Stale, "exactly thirty minutes is not older than thirty minutes")
	clock = clock.Add(officialStatusCacheTTL)
	expired, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.True(t, expired[0].Stale)
	unavailable.Store(false)
	clock = clock.Add(officialStatusCacheTTL)
	fresh, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.False(t, fresh[0].Stale)
	assert.False(t, fresh[0].Unavailable)
	assert.Empty(t, fresh[0].FetchError)
	assert.Greater(t, fresh[0].FetchedAt, first[0].FetchedAt)
}

func TestOfficialStatusCatalogKeepsEightProvidersWithoutUnverifiedRequests(t *testing.T) {
	clock := time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC)
	var requests atomic.Int32
	cache := officialStatusCache{
		now:     func() time.Time { return clock },
		sources: officialStatusSources,
		client: &http.Client{Transport: officialStatusTransport(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			assert.NotContains(t, req.URL.Host, "google")
			assert.NotEqual(t, "status.x.ai", req.URL.Host)
			assert.Empty(t, req.Header.Get("Authorization"))
			assert.Empty(t, req.Header.Get("Cookie"))
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}
		})},
	}
	entries, err := cache.read(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 8)
	assert.Equal(t, []string{"openai", "anthropic", "gemini", "xai", "deepseek", "moonshot", "minimax", "mistral"}, []string{
		entries[0].ID, entries[1].ID, entries[2].ID, entries[3].ID,
		entries[4].ID, entries[5].ID, entries[6].ID, entries[7].ID,
	})
	assert.Equal(t, "https://aistudio.google.com/status", entries[2].SourceURL)
	assert.Equal(t, "https://status.x.ai/", entries[3].SourceURL)
	for _, entry := range entries {
		assert.Equal(t, "unknown", entry.Status)
		assert.True(t, entry.Unavailable)
		assert.Zero(t, entry.FetchedAt)
		assert.False(t, entry.Stale)
		assert.True(t, entry.HistoryUnavailable)
		assert.Zero(t, entry.HistoryFetchedAt)
		assert.Empty(t, entry.Components)
		assert.Empty(t, entry.Incidents)
		assert.Empty(t, entry.RecentIncidents)
	}
	assert.Equal(t, "unverified_source", entries[2].FetchError)
	assert.Zero(t, entries[2].CheckedAt, "no request was attempted to the Gemini app")
	assert.Equal(t, "unverified_source", entries[3].FetchError)
	assert.EqualValues(t, 11, requests.Load(), "six summary and five verified history requests only")
}

func TestOfficialStatusHistoryKeepsNewestDuplicateAndIdentityAcrossReorder(t *testing.T) {
	clock := time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC)
	var summaryDown, historyDown atomic.Bool
	cache := officialStatusCache{
		now: func() time.Time { return clock },
		sources: []officialStatusSource{
			{id: "vendor", pageID: "vendor", pageURL: "https://vendor.example/", feedURL: "https://vendor.example/summary", historyURL: "https://vendor.example/history"},
		},
		client: &http.Client{Transport: officialStatusTransport(func(req *http.Request) (*http.Response, error) {
			body := `{"page":{"id":"vendor","updated_at":"2020-01-01T00:00:00Z"},"status":{"indicator":"minor"},"components":[],"incidents":[{"id":"active","name":"Active incident","status":"monitoring","updated_at":"2026-11-26T03:00:00Z"}]}`
			if req.URL.Path == "/history" {
				if historyDown.Load() {
					return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("private challenge data"))}, nil
				}
				body = `{"page":{"id":"vendor"},"incidents":[{"id":"old","name":"Earlier incident","status":"resolved","updated_at":"2026-11-20T00:00:00Z"},{"id":"latest","name":"Older duplicate","status":"resolved","updated_at":"2026-11-19T00:00:00Z"},{"id":"active","name":"Active incident","status":"monitoring","updated_at":"2026-11-26T03:00:00Z"},{"id":"latest","name":"Latest incident","status":"resolved","updated_at":"2026-11-25T00:00:00Z"}]}`
			} else if summaryDown.Load() {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	first, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "degraded_performance", first[0].Status)
	assert.False(t, first[0].Stale, "old source update is not the successful fetch age")
	assert.Equal(t, "2020-01-01T00:00:00Z", first[0].SourceUpdatedAt)
	require.Len(t, first[0].Incidents, 1)
	assert.Equal(t, "active", first[0].Incidents[0].ID)
	require.Len(t, first[0].RecentIncidents, 3)
	assert.Equal(t, "active", first[0].RecentIncidents[0].ID)
	assert.Equal(t, "latest", first[0].RecentIncidents[1].ID)
	assert.Equal(t, "Latest incident", first[0].RecentIncidents[1].Name)
	assert.Equal(t, "old", first[0].RecentIncidents[2].ID)
	historyDown.Store(true)
	clock = clock.Add(officialStatusCacheTTL)
	historyFailure, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "degraded_performance", historyFailure[0].Status)
	assert.False(t, historyFailure[0].Unavailable)
	assert.Greater(t, historyFailure[0].FetchedAt, first[0].FetchedAt)
	assert.True(t, historyFailure[0].HistoryUnavailable)
	assert.Equal(t, "http_error", historyFailure[0].HistoryError)
	assert.Equal(t, http.StatusForbidden, historyFailure[0].HistoryHTTPStatus)
	assert.Equal(t, first[0].HistoryFetchedAt, historyFailure[0].HistoryFetchedAt)
	assert.Equal(t, first[0].Incidents, historyFailure[0].Incidents)
	assert.Equal(t, first[0].RecentIncidents, historyFailure[0].RecentIncidents)
	assert.False(t, first[0].HistoryUnavailable)

	summaryDown.Store(true)
	historyDown.Store(false)
	cache.sources = append([]officialStatusSource{{id: "unverified", pageURL: "https://other.example/"}}, cache.sources...)
	clock = clock.Add(officialStatusCacheTTL)
	summaryFailure, err := cache.read(context.Background())
	require.NoError(t, err)
	require.Len(t, summaryFailure, 2)
	assert.Equal(t, "vendor", summaryFailure[1].ID)
	assert.True(t, summaryFailure[1].Unavailable)
	assert.Equal(t, historyFailure[0].FetchedAt, summaryFailure[1].FetchedAt)
	assert.False(t, summaryFailure[1].HistoryUnavailable)
	assert.Greater(t, summaryFailure[1].HistoryFetchedAt, first[0].HistoryFetchedAt)
	assert.Empty(t, summaryFailure[1].HistoryError)
	assert.Zero(t, summaryFailure[1].HistoryHTTPStatus)
}

func TestOfficialStatusFreshnessDoesNotAcceptInvalidFetches(t *testing.T) {
	now := time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		fetchedAt int64
	}{
		{name: "future", fetchedAt: now.Add(time.Minute).UnixMilli()},
		{name: "negative", fetchedAt: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := officialStatusSnapshot([]OfficialProviderStatus{{
				Status:           "operational",
				SourceUpdatedAt:  "2026-11-27T00:00:00Z",
				FetchedAt:        test.fetchedAt,
				HistoryFetchedAt: test.fetchedAt,
				Components:       []OfficialStatusComponent{{ID: "api", Name: "API", Status: "operational"}},
				Incidents:        []OfficialStatusIncident{{ID: "active", Name: "Active", Status: "monitoring"}},
				RecentIncidents:  []OfficialStatusIncident{{ID: "recent", Name: "Recent", Status: "resolved"}},
			}}, now)
			require.Len(t, entries, 1)
			assert.Zero(t, entries[0].FetchedAt)
			assert.Equal(t, "unknown", entries[0].Status)
			assert.Equal(t, "invalid_timestamp", entries[0].FetchError)
			assert.True(t, entries[0].Unavailable)
			assert.False(t, entries[0].Stale)
			assert.Empty(t, entries[0].SourceUpdatedAt)
			assert.Nil(t, entries[0].Components)
			assert.Nil(t, entries[0].Incidents)
			assert.Zero(t, entries[0].HistoryFetchedAt)
			assert.Nil(t, entries[0].RecentIncidents)
			assert.Equal(t, "invalid_timestamp", entries[0].HistoryError)
			assert.True(t, entries[0].HistoryUnavailable)
			assert.False(t, entries[0].HistoryStale)
		})
	}

	current := officialStatusSnapshot([]OfficialProviderStatus{{
		ID:               "vendor",
		Status:           "operational",
		FetchedAt:        now.UnixMilli(),
		HistoryFetchedAt: now.UnixMilli(),
	}}, now)
	require.Len(t, current, 1)
	assert.Equal(t, now.UnixMilli(), current[0].FetchedAt)
	assert.Equal(t, now.UnixMilli(), current[0].HistoryFetchedAt)
	assert.Equal(t, "operational", current[0].Status)
	assert.False(t, current[0].Unavailable)
	assert.False(t, current[0].HistoryUnavailable)
	assert.False(t, current[0].Stale)
	assert.False(t, current[0].HistoryStale)

	fresh := officialStatusSnapshot([]OfficialProviderStatus{{
		ID:               "vendor",
		FetchedAt:        now.Add(-officialStatusFreshness).UnixMilli(),
		HistoryFetchedAt: now.Add(-officialStatusFreshness).UnixMilli(),
	}}, now)
	require.Len(t, fresh, 1)
	assert.False(t, fresh[0].Stale, "the exact thirty-minute boundary is fresh")
	assert.False(t, fresh[0].HistoryStale, "the exact history boundary is fresh")

	stale := officialStatusSnapshot([]OfficialProviderStatus{{
		ID:               "vendor",
		FetchedAt:        now.Add(-officialStatusFreshness - time.Millisecond).UnixMilli(),
		HistoryFetchedAt: now.Add(-officialStatusFreshness - time.Millisecond).UnixMilli(),
	}}, now)
	require.Len(t, stale, 1)
	assert.True(t, stale[0].Stale)
	assert.True(t, stale[0].HistoryStale)
}

func TestOfficialStatusDiagnosticsDoNotExposeTransportOrBodyDetails(t *testing.T) {
	for _, test := range []struct {
		name, code string
		err        error
		status     int
		body       string
	}{
		{name: "TLS verification", code: "tls_error", err: &tls.CertificateVerificationError{Err: errors.New("secret certificate detail")}},
		{name: "DNS failure", code: "dns_error", err: &net.DNSError{Err: "secret resolver", Name: "private.internal"}},
		{name: "timeout", code: "timeout", err: context.DeadlineExceeded},
		{name: "transport failure", code: "transport_error", err: errors.New("private upstream address and secret")},
		{name: "HTTP failure", code: "http_error", status: http.StatusForbidden, body: "secret challenge body"},
		{name: "invalid payload", code: "invalid_feed", status: http.StatusOK, body: `<html>secret page</html>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := officialStatusCache{
				now:     time.Now,
				sources: []officialStatusSource{{id: "deepseek", feedURL: "https://status.deepseek.com/api/v2/summary.json"}},
				client: &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
					if test.err != nil {
						return nil, test.err
					}
					return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				})},
			}
			entries, err := cache.read(context.Background())
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, test.code, entries[0].FetchError)
			assert.Zero(t, entries[0].FetchedAt)
			assert.True(t, entries[0].Unavailable)
			assert.False(t, entries[0].Stale)
			body, err := common.Marshal(entries)
			require.NoError(t, err)
			assert.NotContains(t, string(body), "secret")
			assert.NotContains(t, string(body), "private.internal")
		})
	}
}

func TestOfficialStatusHistoryRejectsInvalidAndBoundsRecentRecords(t *testing.T) {
	for _, body := range []string{`{}`, `{"page":{"id":"vendor"}}`, `{"page":{"id":"vendor"},"incidents":null}`, `{"page":{"id":"other"},"incidents":[]}`, `{"page":{"id":"vendor"},"incidents":[{}]}`} {
		client := &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		_, err := fetchOfficialStatusHistory(context.Background(), client, officialStatusSource{pageID: "vendor", historyURL: "https://vendor.example/history"})
		require.Error(t, err)
	}
	incidents := make([]OfficialStatusIncident, 21)
	for i := range incidents {
		incidents[i] = OfficialStatusIncident{ID: strconv.Itoa(i), Name: "Resolved incident", Status: "resolved", UpdatedAt: time.Date(2026, 11, i+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	body, err := common.Marshal(map[string]any{"page": map[string]string{"id": "vendor"}, "incidents": incidents})
	require.NoError(t, err)
	client := &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	recent, err := fetchOfficialStatusHistory(context.Background(), client, officialStatusSource{pageID: "vendor", historyURL: "https://vendor.example/history"})
	require.NoError(t, err)
	require.Len(t, recent, 20)
	assert.Equal(t, "20", recent[0].ID)
	assert.Equal(t, "1", recent[19].ID)
}

func TestOfficialStatusBoundsAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP failure", 503, `{}`},
		{"oversized body", 200, strings.Repeat("x", officialStatusBodyLimit+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			_, err := fetchOfficialStatus(context.Background(), client, officialStatusSource{feedURL: "https://vendor.example/feed"})
			require.Error(t, err)
		})
	}
	started := make(chan struct{})
	release := make(chan struct{})
	cache := officialStatusCache{now: time.Now, sources: []officialStatusSource{{id: "vendor", feedURL: "https://vendor.example/feed"}}, client: &http.Client{
		Transport: officialStatusTransport(func(*http.Request) (*http.Response, error) {
			close(started)
			<-release
			return nil, errors.New("upstream unavailable")
		}),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := cache.read(ctx); finished <- err }()
	<-started
	cancel()
	require.ErrorIs(t, <-finished, context.Canceled)
	close(release)
	entries, err := cache.read(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "unknown", entries[0].Status)
	assert.True(t, entries[0].Unavailable)
	assert.Zero(t, entries[0].FetchedAt)
	assert.False(t, entries[0].Stale)

	origin, err := http.NewRequest(http.MethodGet, "https://status.openai.com/feed", nil)
	require.NoError(t, err)
	for _, test := range []struct {
		name, url string
		hops      int
		allowed   bool
	}{
		{"same official host", "https://status.openai.com/summary", 1, true},
		{"different host", "https://other.example/feed", 1, false},
		{"HTTP downgrade", "http://status.openai.com/feed", 1, false},
		{"redirect loop", "https://status.openai.com/feed", 3, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			redirect, err := http.NewRequest(http.MethodGet, test.url, nil)
			require.NoError(t, err)
			via := make([]*http.Request, test.hops)
			for i := range via {
				via[i] = origin
			}
			err = vendorStatusCache.client.CheckRedirect(redirect, via)
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
