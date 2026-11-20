package service

import (
	"context"
	"errors"
	"io"
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
		google, invalid  bool
	}{
		{"summary", `{"page":{"id":"vendor","updated_at":"2026-11-19T10:00:00Z"},"status":{"indicator":"minor"},"components":[{"name":"API","status":"degraded_performance"}],"incidents":[{"name":"API delays","status":"investigating"}]}`, "degraded_performance", false, false},
		{"empty object is not healthy", `{}`, "", false, true},
		{"HTML error", `<html>error</html>`, "", false, true},
		{"unknown indicator", `{"page":{"id":"vendor"},"status":{"indicator":"new_status"}}`, "unknown", false, false},
		{"Google empty valid feed", `[]`, "operational", true, false},
		{"Google invalid object", `{}`, "", true, true},
		{"Google invalid incident list", `[{}]`, "", true, true},
		{"Google unrelated incident", `[{"id":"storage-incident","external_desc":"Storage issue","affected_products":[{"id":"storage"}]}]`, "operational", true, false},
		{"Google active Vertex incident", `[{"id":"vertex-incident","external_desc":"Vertex delays","modified":"2026-11-19T10:00:00Z","affected_products":[{"id":"Z0FZJAMvEB4j3NbCJs6B"}]}]`, "degraded_performance", true, false},
		{"Google resolved incident", `[{"id":"resolved-incident","external_desc":"Vertex delays","end":"2026-11-19T10:00:00Z","affected_products":[{"id":"Z0FZJAMvEB4j3NbCJs6B"}]}]`, "operational", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: officialStatusTransport(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, "https://vendor.example/feed", req.URL.String())
				assert.Empty(t, req.Header.Get("Authorization"))
				assert.Empty(t, req.Header.Get("Cookie"))
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			entry, err := fetchOfficialStatus(context.Background(), client, officialStatusSource{id: "vendor", feedURL: "https://vendor.example/feed", googleCloud: test.google})
			if test.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, entry.Status)
			if test.google && test.want == "degraded_performance" {
				require.Len(t, entry.Incidents, 1)
				assert.Equal(t, "active", entry.Incidents[0].Status)
				assert.Equal(t, "vertex-incident", entry.Incidents[0].ID)
				assert.Equal(t, "2026-11-19T10:00:00Z", entry.SourceUpdatedAt)
			}
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
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"page":{"id":"vendor"},"status":{"indicator":"none"}}`))}, nil
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
	assert.True(t, stale[0].Stale)
	assert.True(t, stale[0].Unavailable)
	assert.Equal(t, first[0].FetchedAt, stale[0].FetchedAt)
	assert.Greater(t, stale[0].CheckedAt, stale[0].FetchedAt)
	assert.False(t, first[0].Stale, "earlier snapshots remain immutable")
	unavailable.Store(false)
	clock = clock.Add(officialStatusCacheTTL)
	fresh, err := cache.read(context.Background())
	require.NoError(t, err)
	assert.False(t, fresh[0].Stale)
	assert.False(t, fresh[0].Unavailable)
	assert.Greater(t, fresh[0].FetchedAt, first[0].FetchedAt)
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
