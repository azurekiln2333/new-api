package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const officialStatusCacheTTL = 5 * time.Minute
const officialStatusFreshness = 30 * time.Minute
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
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	SourceURL          string                    `json:"source_url"`
	Status             string                    `json:"status"`
	SourceUpdatedAt    string                    `json:"source_updated_at"`
	FetchedAt          int64                     `json:"fetched_at"`
	CheckedAt          int64                     `json:"checked_at"`
	Stale              bool                      `json:"stale"`
	Unavailable        bool                      `json:"unavailable"`
	FetchError         string                    `json:"fetch_error"`
	FetchHTTPStatus    int                       `json:"fetch_http_status"`
	Components         []OfficialStatusComponent `json:"components"`
	Incidents          []OfficialStatusIncident  `json:"incidents"`
	RecentIncidents    []OfficialStatusIncident  `json:"recent_incidents"`
	HistoryFetchedAt   int64                     `json:"history_fetched_at"`
	HistoryCheckedAt   int64                     `json:"history_checked_at"`
	HistoryStale       bool                      `json:"history_stale"`
	HistoryUnavailable bool                      `json:"history_unavailable"`
	HistoryError       string                    `json:"history_error"`
	HistoryHTTPStatus  int                       `json:"history_http_status"`
}

type officialStatusSource struct {
	id, name, pageURL, feedURL, historyURL, pageID string
}

// Only fixed, official sources participate in outbound fetching. Gemini's page
// uses an internal app RPC and xAI's public feed could not be verified. Their
// cards remain visible without probing guessed endpoints or substituting Vertex.
// DeepSeek retains its existing summary URL; no unverified history is requested.
var officialStatusSources = []officialStatusSource{
	{id: "openai", name: "OpenAI", pageURL: "https://status.openai.com/", feedURL: "https://status.openai.com/api/v2/summary.json", historyURL: "https://status.openai.com/api/v2/incidents.json", pageID: "01JMDK9XYNY6RXSED6SDWW50WY"},
	{id: "anthropic", name: "Anthropic", pageURL: "https://status.claude.com/", feedURL: "https://status.claude.com/api/v2/summary.json", historyURL: "https://status.claude.com/api/v2/incidents.json", pageID: "tymt9n04zgry"},
	{id: "gemini", name: "Gemini API", pageURL: "https://aistudio.google.com/status"},
	{id: "xai", name: "xAI", pageURL: "https://status.x.ai/"},
	{id: "deepseek", name: "DeepSeek", pageURL: "https://status.deepseek.com/", feedURL: "https://status.deepseek.com/api/v2/summary.json"},
	{id: "moonshot", name: "Moonshot", pageURL: "https://status.moonshot.cn/", feedURL: "https://status.moonshot.cn/api/v2/summary.json", historyURL: "https://status.moonshot.cn/api/v2/incidents.json", pageID: "10y9y5wx5xh3"},
	{id: "minimax", name: "MiniMax", pageURL: "https://status.minimax.io/", feedURL: "https://status.minimax.io/api/v2/summary.json", historyURL: "https://status.minimax.io/api/v2/incidents.json", pageID: "01jl8mrfc0rh"},
	{id: "mistral", name: "Mistral", pageURL: "https://status.mistral.ai/", feedURL: "https://status.mistral.ai/api/v2/summary.json", historyURL: "https://status.mistral.ai/api/v2/incidents.json", pageID: "ae27f3c4-86a4-4290-a053-87252545d7f9"},
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
				return &officialStatusFetchError{code: "redirect_rejected"}
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
		entries := officialStatusSnapshot(cache.entries, cache.now())
		cache.mu.Unlock()
		return entries, nil
	}
	cache.mu.Unlock()
	result := cache.flight.DoChan("refresh", func() (any, error) {
		cache.mu.Lock()
		if cache.now().Before(cache.expires) {
			entries := officialStatusSnapshot(cache.entries, cache.now())
			cache.mu.Unlock()
			return entries, nil
		}
		previous := make(map[string]OfficialProviderStatus, len(cache.entries))
		for _, entry := range cache.entries {
			previous[entry.ID] = entry
		}
		cache.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries := make([]OfficialProviderStatus, len(cache.sources))
		var wait sync.WaitGroup
		for i, source := range cache.sources {
			wait.Go(func() {
				entry := previous[source.id]
				if entry.FetchedAt == 0 {
					entry.Status = "unknown"
				}
				entry.ID, entry.Name, entry.SourceURL = source.id, source.name, source.pageURL
				var summary OfficialProviderStatus
				var recent []OfficialStatusIncident
				var summaryErr, historyErr error
				var summaryChecked, historyChecked int64
				var requests sync.WaitGroup
				if source.feedURL != "" {
					requests.Go(func() {
						summary, summaryErr = fetchOfficialStatus(ctx, cache.client, source)
						summaryChecked = cache.now().UnixMilli()
					})
				} else {
					summaryErr = &officialStatusFetchError{code: "unverified_source"}
				}
				if source.historyURL != "" {
					requests.Go(func() {
						recent, historyErr = fetchOfficialStatusHistory(ctx, cache.client, source)
						historyChecked = cache.now().UnixMilli()
					})
				} else {
					historyErr = &officialStatusFetchError{code: "unverified_source"}
				}
				requests.Wait()
				entry.CheckedAt = summaryChecked
				entry.Unavailable = summaryErr != nil
				entry.FetchError, entry.FetchHTTPStatus = officialStatusFailure(summaryErr)
				if summaryErr == nil {
					entry.Status = summary.Status
					entry.SourceUpdatedAt = summary.SourceUpdatedAt
					entry.Components = summary.Components
					entry.Incidents = summary.Incidents
					entry.FetchedAt = summaryChecked
				}
				entry.HistoryCheckedAt = historyChecked
				entry.HistoryUnavailable = historyErr != nil
				entry.HistoryError, entry.HistoryHTTPStatus = officialStatusFailure(historyErr)
				if historyErr == nil {
					entry.RecentIncidents = recent
					entry.HistoryFetchedAt = historyChecked
				}
				entries[i] = entry
			})
		}
		wait.Wait()
		cache.mu.Lock()
		cache.entries = officialStatusSnapshot(entries, cache.now())
		cache.expires = cache.now().Add(officialStatusCacheTTL)
		entries = cache.entries
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

// Freshness is retrieval age, not the latest attempt outcome or incident age.
// Clone the projection so cached reads never mutate a caller's earlier result.
func officialStatusSnapshot(entries []OfficialProviderStatus, now time.Time) []OfficialProviderStatus {
	snapshot := slices.Clone(entries)
	nowMillis := now.UnixMilli()
	for i := range snapshot {
		entry := &snapshot[i]
		if entry.FetchedAt < 0 || entry.FetchedAt > nowMillis {
			entry.Status = "unknown"
			entry.FetchedAt = 0
			entry.SourceUpdatedAt = ""
			entry.Components = nil
			entry.Incidents = nil
			entry.Unavailable = true
			entry.FetchError = "invalid_timestamp"
			entry.FetchHTTPStatus = 0
		}
		if entry.FetchedAt == 0 {
			entry.Status = "unknown"
			entry.Components = nil
			entry.Incidents = nil
			entry.Unavailable = true
			if entry.FetchError == "" {
				entry.FetchError = "unverified_source"
			}
			entry.Stale = false
		} else {
			entry.Stale = nowMillis-entry.FetchedAt > officialStatusFreshness.Milliseconds()
		}
		if entry.HistoryFetchedAt < 0 || entry.HistoryFetchedAt > nowMillis {
			entry.HistoryFetchedAt = 0
			entry.RecentIncidents = nil
			entry.HistoryUnavailable = true
			entry.HistoryError = "invalid_timestamp"
			entry.HistoryHTTPStatus = 0
		}
		if entry.HistoryFetchedAt == 0 {
			entry.RecentIncidents = nil
			entry.HistoryUnavailable = true
			if entry.HistoryError == "" {
				entry.HistoryError = "unverified_source"
			}
			entry.HistoryStale = false
		} else {
			entry.HistoryStale = nowMillis-entry.HistoryFetchedAt > officialStatusFreshness.Milliseconds()
		}
	}
	return snapshot
}

type officialStatusFetchError struct {
	code       string
	httpStatus int
}

func (err *officialStatusFetchError) Error() string {
	return "official status fetch failed: " + err.code
}

// Never project raw URL/transport/certificate/challenge text through the public
// aggregate. A connection reset without a typed TLS cause remains transport_error.
func officialStatusFailure(err error) (string, int) {
	if err == nil {
		return "", 0
	}
	var fetchErr *officialStatusFetchError
	if errors.As(err, &fetchErr) {
		return fetchErr.code, fetchErr.httpStatus
	}
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkErr) && networkErr.Timeout() {
		return "timeout", 0
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error", 0
	}
	var verificationErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var authorityErr x509.UnknownAuthorityError
	var certificateErr x509.CertificateInvalidError
	var hostnameErr x509.HostnameError
	if errors.As(err, &verificationErr) || errors.As(err, &recordErr) || errors.As(err, &authorityErr) || errors.As(err, &certificateErr) || errors.As(err, &hostnameErr) {
		return "tls_error", 0
	}
	return "transport_error", 0
}

func fetchOfficialStatusBody(ctx context.Context, client *http.Client, feedURL string) ([]byte, error) {
	if feedURL == "" {
		return nil, &officialStatusFetchError{code: "unverified_source"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, &officialStatusFetchError{code: "invalid_source"}
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &officialStatusFetchError{code: "http_error", httpStatus: res.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, officialStatusBodyLimit+1))
	if err != nil {
		return nil, &officialStatusFetchError{code: "read_error"}
	}
	if len(body) > officialStatusBodyLimit {
		return nil, &officialStatusFetchError{code: "body_limit"}
	}
	return body, nil
}

func fetchOfficialStatus(ctx context.Context, client *http.Client, source officialStatusSource) (OfficialProviderStatus, error) {
	entry := OfficialProviderStatus{ID: source.id, Name: source.name, SourceURL: source.pageURL, Status: "unknown"}
	body, err := fetchOfficialStatusBody(ctx, client, source.feedURL)
	if err != nil {
		return entry, err
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
	if common.Unmarshal(body, &feed) != nil || feed.Page.ID == "" || feed.Status.Indicator == "" || feed.Components == nil || feed.Incidents == nil || source.pageID != "" && feed.Page.ID != source.pageID {
		return entry, &officialStatusFetchError{code: "invalid_feed"}
	}
	for _, component := range feed.Components {
		if component.ID == "" || strings.TrimSpace(component.Name) == "" || component.Status == "" {
			return entry, &officialStatusFetchError{code: "invalid_feed"}
		}
	}
	for _, incident := range feed.Incidents {
		if incident.ID == "" || strings.TrimSpace(incident.Name) == "" || incident.Status == "" {
			return entry, &officialStatusFetchError{code: "invalid_feed"}
		}
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

func fetchOfficialStatusHistory(ctx context.Context, client *http.Client, source officialStatusSource) ([]OfficialStatusIncident, error) {
	body, err := fetchOfficialStatusBody(ctx, client, source.historyURL)
	if err != nil {
		return nil, err
	}
	var feed struct {
		Page struct {
			ID string `json:"id"`
		} `json:"page"`
		Incidents []OfficialStatusIncident `json:"incidents"`
	}
	if common.Unmarshal(body, &feed) != nil || feed.Page.ID == "" || source.pageID != "" && feed.Page.ID != source.pageID || feed.Incidents == nil {
		return nil, &officialStatusFetchError{code: "invalid_feed"}
	}
	for _, incident := range feed.Incidents {
		if incident.ID == "" || strings.TrimSpace(incident.Name) == "" || incident.Status == "" {
			return nil, &officialStatusFetchError{code: "invalid_feed"}
		}
	}
	slices.SortStableFunc(feed.Incidents, func(a, b OfficialStatusIncident) int {
		left, _ := time.Parse(time.RFC3339, a.UpdatedAt)
		right, _ := time.Parse(time.RFC3339, b.UpdatedAt)
		return right.Compare(left)
	})
	recent := make([]OfficialStatusIncident, 0, min(20, len(feed.Incidents)))
	seen := make(map[string]bool, len(feed.Incidents))
	for _, incident := range feed.Incidents {
		if seen[incident.ID] {
			continue
		}
		seen[incident.ID] = true
		recent = append(recent, incident)
		if len(recent) == 20 {
			break
		}
	}
	return recent, nil
}
