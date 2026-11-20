package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const officialStatusCacheTTL = 5 * time.Minute
const officialStatusBodyLimit = 2 << 20

type OfficialStatusComponent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type OfficialStatusIncident struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

type OfficialProviderStatus struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	SourceURL       string                    `json:"source_url"`
	Status          string                    `json:"status"`
	SourceUpdatedAt string                    `json:"source_updated_at"`
	FetchedAt       int64                     `json:"fetched_at"`
	CheckedAt       int64                     `json:"checked_at"`
	Stale           bool                      `json:"stale"`
	Unavailable     bool                      `json:"unavailable"`
	Components      []OfficialStatusComponent `json:"components"`
	Incidents       []OfficialStatusIncident  `json:"incidents"`
}

type officialStatusSource struct {
	id, name, pageURL, feedURL string
	googleCloud                bool
}

// This fixed catalog is the only source of outbound URLs. Neither credentials
// nor URLs supplied by dashboard users participate in status fetching.
var officialStatusSources = []officialStatusSource{
	{"openai", "OpenAI", "https://status.openai.com/", "https://status.openai.com/api/v2/summary.json", false},
	{"anthropic", "Anthropic", "https://status.claude.com/", "https://status.claude.com/api/v2/summary.json", false},
	{"deepseek", "DeepSeek", "https://status.deepseek.com/", "https://status.deepseek.com/api/v2/summary.json", false},
	{"google-vertex", "Google Cloud Vertex Gemini API", "https://status.cloud.google.com/", "https://status.cloud.google.com/incidents.json", true},
}

type officialStatusCache struct {
	mu      sync.Mutex
	entries []OfficialProviderStatus
	expires time.Time
	flight  singleflight.Group
	client  *http.Client
	now     func() time.Time
	sources []officialStatusSource
}

var vendorStatusCache = officialStatusCache{
	client: &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
				return errors.New("official status redirect is not allowed")
			}
			return nil
		},
	},
	now:     time.Now,
	sources: officialStatusSources,
}

// GetOfficialServiceStatus shares a bounded refresh across all callers. A
// caller may cancel without aborting the refresh other readers depend on.
func GetOfficialServiceStatus(ctx context.Context) ([]OfficialProviderStatus, error) {
	return vendorStatusCache.read(ctx)
}

func (cache *officialStatusCache) read(ctx context.Context) ([]OfficialProviderStatus, error) {
	cache.mu.Lock()
	if cache.now().Before(cache.expires) {
		entries := cache.entries
		cache.mu.Unlock()
		return entries, nil
	}
	cache.mu.Unlock()
	result := cache.flight.DoChan("refresh", func() (any, error) {
		cache.mu.Lock()
		if cache.now().Before(cache.expires) {
			entries := cache.entries
			cache.mu.Unlock()
			return entries, nil
		}
		previous := cache.entries
		cache.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries := make([]OfficialProviderStatus, len(cache.sources))
		var wait sync.WaitGroup
		for i, source := range cache.sources {
			wait.Go(func() {
				entry, err := fetchOfficialStatus(ctx, cache.client, source)
				now := cache.now().UnixMilli()
				if err != nil {
					entry = OfficialProviderStatus{ID: source.id, Name: source.name, SourceURL: source.pageURL, Status: "unknown"}
					if i < len(previous) && previous[i].ID == source.id {
						entry = previous[i]
					}
					entry.Stale = entry.FetchedAt != 0
					entry.Unavailable = true
				} else {
					entry.FetchedAt = now
				}
				entry.CheckedAt = now
				entries[i] = entry
			})
		}
		wait.Wait()
		cache.mu.Lock()
		cache.entries = entries
		cache.expires = cache.now().Add(officialStatusCacheTTL)
		cache.mu.Unlock()
		return entries, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case ready := <-result:
		if ready.Err != nil {
			return nil, ready.Err
		}
		return ready.Val.([]OfficialProviderStatus), nil
	}
}

func fetchOfficialStatus(ctx context.Context, client *http.Client, source officialStatusSource) (OfficialProviderStatus, error) {
	entry := OfficialProviderStatus{ID: source.id, Name: source.name, SourceURL: source.pageURL, Status: "unknown"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.feedURL, nil)
	if err != nil {
		return entry, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return entry, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return entry, fmt.Errorf("official status returned HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, officialStatusBodyLimit+1))
	if err != nil || len(body) > officialStatusBodyLimit {
		return entry, errors.New("official status response could not be read within limit")
	}
	if source.googleCloud {
		return decodeGoogleVertexStatus(entry, body)
	}
	var feed struct {
		Page struct {
			ID        string `json:"id"`
			UpdatedAt string `json:"updated_at"`
		} `json:"page"`
		Status struct {
			Indicator string `json:"indicator"`
		} `json:"status"`
		Components []OfficialStatusComponent `json:"components"`
		Incidents  []OfficialStatusIncident  `json:"incidents"`
	}
	if err := common.Unmarshal(body, &feed); err != nil || feed.Page.ID == "" || feed.Status.Indicator == "" {
		return entry, errors.New("official status response is invalid")
	}
	switch feed.Status.Indicator {
	case "none":
		entry.Status = "operational"
	case "minor":
		entry.Status = "degraded_performance"
	case "major":
		entry.Status = "partial_outage"
	case "critical":
		entry.Status = "major_outage"
	case "maintenance":
		entry.Status = "under_maintenance"
	}
	entry.SourceUpdatedAt = feed.Page.UpdatedAt
	entry.Components = slices.Clone(feed.Components[:min(100, len(feed.Components))])
	entry.Incidents = slices.Clone(feed.Incidents[:min(20, len(feed.Incidents))])
	return entry, nil
}

// Google publishes incidents rather than a Statuspage summary. Only the
// official Vertex Gemini product is included; this is not AI Studio health.
func decodeGoogleVertexStatus(entry OfficialProviderStatus, body []byte) (OfficialProviderStatus, error) {
	var incidents []struct {
		ID       string `json:"id"`
		End      string `json:"end"`
		Modified string `json:"modified"`
		Name     string `json:"external_desc"`
		Products []struct {
			ID string `json:"id"`
		} `json:"affected_products"`
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "[") || common.Unmarshal(body, &incidents) != nil {
		return entry, errors.New("official Google status response is invalid")
	}
	entry.Status = "operational"
	var updated time.Time
	for _, incident := range incidents {
		if incident.ID == "" || incident.Name == "" || len(incident.Products) == 0 {
			return entry, errors.New("official Google status incident is invalid")
		}
		matches := slices.ContainsFunc(incident.Products, func(product struct {
			ID string `json:"id"`
		}) bool {
			return product.ID == "Z0FZJAMvEB4j3NbCJs6B"
		})
		if !matches {
			continue
		}
		if changed, err := time.Parse(time.RFC3339, incident.Modified); err == nil && changed.After(updated) {
			updated = changed
			entry.SourceUpdatedAt = incident.Modified
		}
		if incident.End == "" {
			entry.Status = "degraded_performance"
			if len(entry.Incidents) < 20 {
				entry.Incidents = append(entry.Incidents, OfficialStatusIncident{ID: incident.ID, Name: incident.Name, Status: "active", UpdatedAt: incident.Modified})
			}
		}
	}
	entry.Components = []OfficialStatusComponent{{ID: "Z0FZJAMvEB4j3NbCJs6B", Name: "Vertex Gemini API", Status: entry.Status}}
	return entry, nil
}
