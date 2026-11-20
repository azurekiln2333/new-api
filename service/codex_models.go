package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	codexLatestReleaseURL      = "https://api.github.com/repos/openai/codex/releases/latest"
	codexClientVersionCacheTTL = time.Hour
	maxCodexDiscoveryBody      = 4 << 20
)

type codexClientVersionCache struct {
	sync.Mutex
	version   string
	expiresAt time.Time
	pending   chan struct{}
}

var latestCodexClientVersion codexClientVersionCache

func lookupCodexModelClientVersion(ctx context.Context, proxy string) (string, error) {
	client, err := GetHttpClientWithProxy(proxy)
	if err != nil {
		return "", common.NewMessage("Invalid channel proxy")
	}
	// Channel TLS/headers never apply to GitHub. Even the deployment-wide
	// insecure relay setting must not disable verification on the lookup.
	releaseClient := *client
	if transport, ok := client.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		verified := transport.Clone()
		verified.TLSClientConfig.InsecureSkipVerify = false
		defer verified.CloseIdleConnections()
		releaseClient.Transport = verified
	}
	releaseClient.Timeout = 0 // the caller's discovery deadline bounds the lookup
	releaseClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, via[0].URL.Host) || req.URL.User != nil {
			return http.ErrUseLastResponse
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		return nil
	}
	return GetLatestCodexClientVersion(ctx, &releaseClient)
}

func GetLatestCodexClientVersion(ctx context.Context, client *http.Client) (string, error) {
	return latestCodexClientVersion.get(ctx, client, codexLatestReleaseURL, time.Now())
}

func (cache *codexClientVersionCache) get(ctx context.Context, client *http.Client, releaseURL string, now time.Time) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		cache.Lock()
		if cache.version != "" && now.Before(cache.expiresAt) {
			version := cache.version
			cache.Unlock()
			return version, nil
		}
		if pending := cache.pending; pending != nil {
			cache.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		cache.pending = pending
		cache.Unlock()

		version, err := fetchLatestCodexClientVersion(ctx, client, releaseURL)
		cache.Lock()
		if ctx.Err() != nil {
			version, err = "", ctx.Err()
		} else if err == nil {
			cache.version = version
			cache.expiresAt = now.Add(codexClientVersionCacheTTL)
		} else if cache.version != "" {
			// A temporary GitHub failure shouldn't prevent use of a known version.
			cache.expiresAt = now.Add(codexClientVersionCacheTTL)
			version, err = cache.version, nil
		}
		cache.pending = nil
		close(pending)
		cache.Unlock()
		return version, err
	}
}

func fetchLatestCodexClientVersion(ctx context.Context, client *http.Client, releaseURL string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("nil http client")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "new-api")

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", common.NewMessage("Upstream request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("codex release lookup failed: status=%d", resp.StatusCode)
	}

	var release struct {
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, maxCodexDiscoveryBody), &release); err != nil {
		return "", common.NewMessage("Invalid response")
	}
	if release.Draft || release.Prerelease {
		return "", fmt.Errorf("latest codex release is not stable")
	}
	version := strings.TrimSpace(release.Name)
	if version == "" {
		return "", fmt.Errorf("latest codex release has no version name")
	}
	return version, nil
}

func newCodexModelsRequest(ctx context.Context, baseURL string, oauthKey *CodexOAuthKey, clientVersion string) (*http.Request, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	clientVersion = strings.TrimSpace(clientVersion)
	if accessToken == "" {
		return nil, errors.New("codex channel: access_token is required")
	}
	if accountID == "" {
		return nil, errors.New("codex channel: account_id is required")
	}
	if clientVersion == "" {
		return nil, errors.New("codex channel: client_version is required")
	}

	base, err := url.Parse(baseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, common.NewMessage("Invalid parameters")
	}
	query := url.Values{"client_version": {clientVersion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/backend-api/codex/models?"+query.Encode(), nil)
	if err != nil {
		return nil, common.NewMessage("Invalid parameters")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("User-Agent", "codex-cli/"+clientVersion)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func readCodexModelsResponse(body io.Reader) ([]string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxCodexDiscoveryBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCodexDiscoveryBody {
		return nil, common.NewMessage("Response too large")
	}
	var result struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
		Error any `json:"error"`
	}
	if err := common.Unmarshal(data, &result); err != nil || result.Models == nil || result.Error != nil {
		return nil, common.NewMessage("Invalid response")
	}

	seen := make(map[string]struct{}, len(result.Models))
	models := make([]string, 0, len(result.Models))
	for _, item := range result.Models {
		slug := strings.TrimSpace(item.Slug)
		if slug == "" {
			// A malformed entry is not evidence that a saved model disappeared.
			return nil, common.NewMessage("Invalid response")
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		models = append(models, slug)
	}
	return models, nil
}
