package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCodexChannelManagementReloadsAndClosesBeforeRetry(t *testing.T) {
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	db := useProviderPluginDatabase(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.SystemTask{})) })
	const sourceKey = `{"access_token":"current-token","account_id":"current-account","refresh_token":"current-refresh"}`
	const replacementKey = `{"access_token":"replacement-token","account_id":"replacement-account","refresh_token":"replacement-refresh"}`

	for _, tc := range []struct {
		name           string
		beforeExchange bool
		changeType     bool
		changeMultiKey bool
		refreshSuccess bool
		preview        bool
	}{
		{name: "replacement before OAuth", beforeExchange: true},
		{name: "replacement during OAuth"},
		{name: "type changes during OAuth", changeType: true},
		{name: "single key changes during OAuth", changeMultiKey: true},
		{name: "successful refresh retries persisted credential", refreshSuccess: true},
		{name: "noncredential preview survives refresh", refreshSuccess: true, preview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &latestCodexClientVersion
			cache.Lock()
			previousVersion, previousExpiry := cache.version, cache.expiresAt
			cache.version, cache.expiresAt = "", time.Time{}
			cache.Unlock()
			t.Cleanup(func() {
				cache.Lock()
				cache.version, cache.expiresAt = previousVersion, previousExpiry
				cache.Unlock()
			})
			channel := &model.Channel{
				Type: constant.ChannelTypeCodex, Name: "codex-discovery",
				BaseURL: common.GetPointer("https://models.example/stale"),
				Key:     `{"access_token":"stale-token","account_id":"stale-account"}`,
			}
			channel.SetSetting(dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
			require.NoError(t, db.Create(channel).Error)
			wantKey, wantType := replacementKey, constant.ChannelTypeCodex
			if tc.changeType {
				wantKey, wantType = sourceKey, constant.ChannelTypeOpenAI
			} else if tc.changeMultiKey {
				wantKey = sourceKey
			}
			writeCurrent := func(ctx context.Context, prefix, key string, channelType int) error {
				return db.WithContext(ctx).Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
					"base_url": "https://models.example/" + prefix,
					"key":      key,
					"type":     channelType,
				}).Error
			}
			readCount := func(ctx context.Context) int {
				sample := ReadChannelConcurrency(ctx, []int{channel.Id})[channel.Id]
				if sample.Current == nil {
					return -1
				}
				return *sample.Current
			}
			var preview *CodexModelDiscoveryPreview
			if tc.preview {
				preview = &CodexModelDiscoveryPreview{
					SourceType:     constant.ChannelTypeCodex,
					BaseURL:        common.GetPointer("https://models.example/preview"),
					HeaderOverride: common.GetPointer(`{"X-Preview":"explicit"}`),
					Proxy:          common.GetPointer(""),
				}
			}
			var firstBody, finalBody *firstByteTestBody
			var paths []string
			httpClient = &http.Client{Transport: firstByteTestTransport(func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				switch req.URL.Path {
				case "/repos/openai/codex/releases/latest":
					assert.Equal(t, http.MethodGet, req.Method)
					assert.Equal(t, "api.github.com", req.URL.Host)
					assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
					assert.Empty(t, req.Header.Get("Authorization"))
					assert.Empty(t, req.Header.Get("ChatGPT-Account-Id"))
					assert.Empty(t, req.Header.Get("X-Static-Key"))
					assert.Zero(t, readCount(req.Context()), "GitHub lookup must not acquire channel capacity")
					if err := writeCurrent(req.Context(), "current", sourceKey, constant.ChannelTypeCodex); err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"name":"0.42.0"}`))}, nil
				case "/current/backend-api/codex/models", "/preview/backend-api/codex/models":
					// A noncredential preview keeps its target and static override
					// across the refresh, while the account token is reloaded.
					if tc.preview {
						assert.Equal(t, "explicit", req.Header.Get("X-Preview"))
						if firstBody != nil {
							assert.True(t, firstBody.closed)
							assert.Equal(t, "Bearer rotated-stale-token", req.Header.Get("Authorization"))
							assert.Equal(t, "current-account", req.Header.Get("ChatGPT-Account-Id"))
							data := strings.NewReader(`{"models":[{"slug":"gpt-example"}]}`)
							finalBody = &firstByteTestBody{read: func(p []byte) (int, error) {
								assert.Equal(t, 1, readCount(req.Context()))
								return data.Read(p)
							}}
							return &http.Response{StatusCode: http.StatusOK, Body: finalBody}, nil
						}
					}
					assert.Equal(t, http.MethodGet, req.Method)
					assert.Equal(t, "Bearer current-token", req.Header.Get("Authorization"))
					assert.Equal(t, "current-account", req.Header.Get("ChatGPT-Account-Id"))
					assert.Equal(t, "current-token", req.Header.Get("X-Static-Key"))
					assert.Equal(t, "codex-cli/0.42.0", req.Header.Get("User-Agent"))
					assert.Equal(t, "0.42.0", req.URL.Query().Get("client_version"))
					assert.Equal(t, 1, readCount(req.Context()))
					if tc.beforeExchange {
						if err := writeCurrent(req.Context(), "replacement", wantKey, wantType); err != nil {
							return nil, err
						}
					}
					firstBody = &firstByteTestBody{read: func([]byte) (int, error) {
						t.Error("a 401 body must not be read")
						return 0, io.EOF
					}}
					return &http.Response{StatusCode: http.StatusUnauthorized, Body: firstBody}, nil
				case "/oauth/token":
					defer req.Body.Close()
					if tc.beforeExchange {
						return nil, errors.New("unexpected OAuth exchange after source mismatch")
					}
					assert.Equal(t, http.MethodPost, req.Method)
					assert.Equal(t, "auth.openai.com", req.URL.Host)
					assert.Empty(t, req.Header.Get("Authorization"))
					assert.Empty(t, req.Header.Get("ChatGPT-Account-Id"))
					assert.Empty(t, req.Header.Get("X-Static-Key"))
					assert.True(t, firstBody != nil && firstBody.closed)
					assert.Zero(t, readCount(req.Context()), "refresh must not hold the 401 response's slot")
					if err := req.ParseForm(); err != nil {
						return nil, err
					}
					assert.Equal(t, "current-refresh", req.PostForm.Get("refresh_token"))
					refreshSource := wantKey
					if tc.refreshSuccess {
						refreshSource = sourceKey
					}
					if err := writeCurrent(req.Context(), "replacement", refreshSource, wantType); err != nil {
						return nil, err
					}
					if tc.changeMultiKey {
						info := model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1}
						if err := db.WithContext(req.Context()).Model(&model.Channel{}).Where("id = ?", channel.Id).Update("channel_info", info).Error; err != nil {
							return nil, err
						}
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"rotated-stale-token","refresh_token":"rotated-stale-refresh","expires_in":3600}`))}, nil
				case "/replacement/backend-api/codex/models":
					assert.Equal(t, http.MethodGet, req.Method)
					assert.True(t, firstBody != nil && firstBody.closed, "401 body must close before retry")
					wantToken, wantAccount := "replacement-token", "replacement-account"
					if tc.refreshSuccess {
						wantToken, wantAccount = "rotated-stale-token", "current-account"
					}
					assert.Equal(t, "Bearer "+wantToken, req.Header.Get("Authorization"))
					assert.Equal(t, wantAccount, req.Header.Get("ChatGPT-Account-Id"))
					assert.Equal(t, wantToken, req.Header.Get("X-Static-Key"))
					assert.Equal(t, "codex.internal", req.Host)
					data := strings.NewReader(`{"models":[{"slug":" gpt-example "},{"slug":"gpt-example"}]}`)
					finalBody = &firstByteTestBody{read: func(p []byte) (int, error) {
						assert.Equal(t, 1, readCount(req.Context()), "capacity must remain held while reading the body")
						return data.Read(p)
					}}
					return &http.Response{StatusCode: http.StatusOK, Body: finalBody}, nil
				default:
					return nil, fmt.Errorf("unexpected outbound request: %s %s", req.Method, req.URL.Path)
				}
			})}

			admission := NewChannelDiagnosticAdmission(t.Context(), 0)
			defer admission.Close()
			models, err := FetchCodexChannelModels(t.Context(), channel, CodexModelDiscoveryOptions{
				Admission: admission,
				Preview:   preview,
				Override: func(current *model.Channel, token string, headers http.Header) error {
					assert.Equal(t, channel.Id, current.Id)
					assert.Equal(t, "Bearer "+token, headers.Get("Authorization"))
					if tc.preview {
						assert.Equal(t, *preview.HeaderOverride, *current.HeaderOverride)
						headers.Set("X-Preview", "explicit")
					}
					headers.Set("X-Static-Key", token)
					headers.Set("Host", "codex.internal")
					return nil
				},
			})
			wantPaths := []string{"/repos/openai/codex/releases/latest", "/current/backend-api/codex/models"}
			if !tc.beforeExchange {
				wantPaths = append(wantPaths, "/oauth/token")
			}
			if tc.changeType {
				require.EqualError(t, err, "channel type is not Codex")
				assert.Nil(t, models)
			} else if tc.changeMultiKey {
				require.EqualError(t, err, "codex channel does not support multi-key model discovery")
				assert.Nil(t, models)
			} else {
				require.NoError(t, err)
				assert.Equal(t, []string{"gpt-example"}, models)
				assert.True(t, finalBody != nil && finalBody.closed)
				wantPaths = append(wantPaths, "/replacement/backend-api/codex/models")
			}
			if tc.preview {
				wantPaths[1], wantPaths[len(wantPaths)-1] = "/preview/backend-api/codex/models", "/preview/backend-api/codex/models"
			}
			assert.Equal(t, wantPaths, paths, "only one model retry and at most one OAuth exchange")
			assert.Zero(t, readCount(t.Context()))
			var stored model.Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			if tc.refreshSuccess {
				credential, parseErr := parseCodexOAuthKey(stored.Key)
				require.NoError(t, parseErr)
				assert.Equal(t, "rotated-stale-token", credential.AccessToken)
				assert.Equal(t, "rotated-stale-refresh", credential.RefreshToken)
			} else {
				assert.Equal(t, wantKey, stored.Key, "the stale exchange must not overwrite the current row")
			}
			assert.Equal(t, wantType, stored.Type)
			assert.Equal(t, tc.changeMultiKey, stored.ChannelInfo.IsMultiKey)
		})
	}
}

func TestCodexChannelManagementPreviewKeepsExplicitFieldsAndCurrentPolicy(t *testing.T) {
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	db := useProviderPluginDatabase(t)
	cache := &latestCodexClientVersion
	cache.Lock()
	previousVersion, previousExpiry := cache.version, cache.expiresAt
	cache.version, cache.expiresAt = "0.42.0", time.Now().Add(time.Hour)
	cache.Unlock()
	t.Cleanup(func() {
		cache.Lock()
		cache.version, cache.expiresAt = previousVersion, previousExpiry
		cache.Unlock()
	})
	const savedKey = `{"access_token":"saved-token","account_id":"saved-account","refresh_token":"saved-refresh"}`
	const typedKey = `{"access_token":"typed-token","account_id":"typed-account","refresh_token":"typed-refresh"}`
	for index, tc := range []struct {
		name        string
		clear       bool
		typed       bool
		rejected    bool
		sourceDraft bool
		typeDrift   bool
		multiKey    bool
	}{
		{name: "omitted fields use current saved values"},
		{name: "explicit empty values clear saved fields", clear: true},
		{name: "typed fields survive saved row reload", typed: true},
		{name: "typed credential 401 cannot refresh saved account", typed: true, rejected: true},
		{name: "intentional saved type preview", typed: true, sourceDraft: true},
		{name: "concurrent source type drift is rejected", typed: true, sourceDraft: true, typeDrift: true},
		{name: "current multikey row is rejected", typed: true, multiKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storedType := constant.ChannelTypeCodex
			if tc.sourceDraft {
				storedType = constant.ChannelTypeOpenAI
			}
			savedProxy := fmt.Sprintf("http://codex-saved-%d.invalid:18080", index)
			previewProxy := fmt.Sprintf("http://codex-preview-%d.invalid:18080", index)
			settings := dto.ChannelSettings{
				Proxy: savedProxy, MaxConcurrentRequests: common.GetPointer(2),
				TLSInsecureSkipVerify: true, HTTPProtocol: dto.HTTPProtocolHTTP1,
			}
			channel := &model.Channel{
				Name: "codex-preview-" + tc.name, Type: storedType, Key: savedKey,
				BaseURL:        common.GetPointer("https://saved.example/current"),
				HeaderOverride: common.GetPointer(`{"X-Source":"saved"}`),
			}
			channel.SetSetting(settings)
			require.NoError(t, db.Create(channel).Error)
			snapshot := *channel
			snapshot.Type = constant.ChannelTypeCodex
			snapshot.Key = `{"access_token":"stale-token","account_id":"stale-account"}`
			snapshot.BaseURL = common.GetPointer("https://stale.example/old")
			stale := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}
			snapshot.SetSetting(stale)
			preview := &CodexModelDiscoveryPreview{}
			wantBase, wantKey, wantProxy, wantHeader := *channel.BaseURL, savedKey, savedProxy, *channel.HeaderOverride
			if tc.clear {
				preview.BaseURL, preview.HeaderOverride, preview.Proxy = common.GetPointer(""), common.GetPointer(""), common.GetPointer("")
				wantBase, wantProxy, wantHeader = constant.GetChannelBaseURL(constant.ChannelTypeCodex), "", ""
			} else if tc.typed {
				preview.Key = common.GetPointer(typedKey)
				preview.BaseURL = common.GetPointer("https://draft.example/preview")
				preview.HeaderOverride = common.GetPointer(`{"X-Source":"draft"}`)
				preview.Proxy = common.GetPointer(previewProxy)
				wantBase, wantKey, wantProxy, wantHeader = *preview.BaseURL, typedKey, previewProxy, *preview.HeaderOverride
			}
			if tc.sourceDraft {
				preview.SourceType = constant.ChannelTypeOpenAI
			}
			if tc.typeDrift {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("type", constant.ChannelTypeAnthropic).Error)
			}
			if tc.multiKey {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("channel_info", model.ChannelInfo{IsMultiKey: true}).Error)
			}

			var requests, overrides int
			httpClient = &http.Client{Transport: firstByteTestTransport(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("unexpected default-client request")
			})}
			effectiveSettings := settings
			effectiveSettings.Proxy = wantProxy
			client := &http.Client{Transport: firstByteTestTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				assert.Equal(t, strings.TrimRight(wantBase, "/")+"/backend-api/codex/models", req.URL.Scheme+"://"+req.URL.Host+req.URL.Path)
				credential, err := parseCodexOAuthKey(wantKey)
				if err != nil {
					return nil, err
				}
				assert.Equal(t, "Bearer "+credential.AccessToken, req.Header.Get("Authorization"))
				assert.Equal(t, credential.AccountID, req.Header.Get("ChatGPT-Account-Id"))
				assert.Equal(t, "codex-cli/0.42.0", req.Header.Get("User-Agent"))
				assert.Equal(t, "0.42.0", req.URL.Query().Get("client_version"))
				assert.Equal(t, wantHeader, req.Header.Get("X-Effective-Override"))
				sample := ReadChannelConcurrency(req.Context(), []int{channel.Id})[channel.Id]
				if assert.NotNil(t, sample.Current) {
					assert.Equal(t, 1, *sample.Current)
				}
				if tc.rejected {
					return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("private provider error"))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}, nil
			})}
			policy := NormalizeHTTPTransportPolicy(effectiveSettings)
			cacheKey := clientCacheKey(wantProxy, policy)
			proxyClients.mutex.Lock()
			previousPolicyClient, hadPolicyClient := proxyClients.clients[cacheKey]
			proxyClients.clients[cacheKey] = client
			proxyClients.mutex.Unlock()
			defer func() {
				proxyClients.mutex.Lock()
				if hadPolicyClient {
					proxyClients.clients[cacheKey] = previousPolicyClient
				} else {
					delete(proxyClients.clients, cacheKey)
				}
				proxyClients.mutex.Unlock()
			}()
			admission := NewChannelDiagnosticAdmission(t.Context(), 0)
			defer admission.Close()
			models, err := FetchCodexChannelModels(t.Context(), &snapshot, CodexModelDiscoveryOptions{
				Admission: admission, Preview: preview,
				Override: func(current *model.Channel, token string, headers http.Header) error {
					overrides++
					assert.Equal(t, channel.Id, current.Id)
					assert.Equal(t, constant.ChannelTypeCodex, current.Type)
					assert.Equal(t, wantKey, current.Key)
					assert.Equal(t, effectiveSettings, current.GetSetting())
					assert.Equal(t, "Bearer "+token, headers.Get("Authorization"))
					assert.Equal(t, wantHeader, *current.HeaderOverride)
					headers.Set("X-Effective-Override", *current.HeaderOverride)
					return nil
				},
			})
			if tc.typeDrift || tc.multiKey {
				require.Error(t, err)
				assert.Nil(t, models)
				assert.Zero(t, requests)
				assert.Zero(t, overrides)
			} else {
				if tc.rejected {
					require.EqualError(t, err, "upstream status: 401")
					assert.Nil(t, models)
				} else {
					require.NoError(t, err)
					assert.Empty(t, models)
				}
				assert.Equal(t, 1, requests, "draft failures must not refresh or fall back to the saved account")
				assert.Equal(t, 1, overrides)
			}
			var persisted model.Channel
			require.NoError(t, db.First(&persisted, channel.Id).Error)
			assert.Equal(t, savedKey, persisted.Key)
			assert.Equal(t, *channel.BaseURL, *persisted.BaseURL)
			assert.Equal(t, *channel.HeaderOverride, *persisted.HeaderOverride)
			assert.Equal(t, settings, persisted.GetSetting())
			sample := ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id]
			require.NotNil(t, sample.Current)
			assert.Zero(t, *sample.Current)
		})
	}
}

func TestCodexChannelManagementRefreshRejectsNonExactSource(t *testing.T) {
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	db := useProviderPluginDatabase(t)
	const storedKey = `{"access_token":"Current","account_id":"account","refresh_token":"refresh"}`
	channel := &model.Channel{Type: constant.ChannelTypeCodex, Name: "codex-exact-source", Key: storedKey}
	require.NoError(t, db.Create(channel).Error)
	var exchanges int
	httpClient = &http.Client{Transport: firstByteTestTransport(func(*http.Request) (*http.Response, error) {
		exchanges++
		return nil, errors.New("unexpected outbound request")
	})}

	for _, expected := range []string{strings.Replace(storedKey, "Current", "current", 1), storedKey + " "} {
		key, source, err := RefreshCodexChannelCredential(t.Context(), channel.Id, CodexCredentialRefreshOptions{ExpectedKey: &expected})
		require.ErrorIs(t, err, ErrCodexCredentialSourceChanged)
		assert.Nil(t, key)
		assert.Nil(t, source)
	}
	assert.Zero(t, exchanges)
	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, storedKey, stored.Key)
}

func TestCodexChannelManagementRefreshPersistence(t *testing.T) {
	previousClient := httpClient
	t.Cleanup(func() { httpClient = previousClient })
	db := useProviderPluginDatabase(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&model.SystemTask{})) })
	const original = `{"access_token":"old","refresh_token":"old-refresh","account_id":"account"}`
	// Case-only replacement also exercises the guard on case-insensitive DBs.
	const replacement = `{"access_token":"Old","refresh_token":"old-refresh","account_id":"account"}`
	const otherInfo = `{"operator_note":"keep"}`
	for _, tc := range []struct {
		name        string
		replace     bool
		cancel      bool
		writeFailed bool
		wantErr     error
	}{
		{name: "replacement without explicit expected key", replace: true, wantErr: ErrCodexCredentialSourceChanged},
		{name: "canceled exchange cannot commit", cancel: true, wantErr: context.Canceled},
		{name: "write failure does not expose the credential", writeFailed: true},
		{name: "successful exchange stores rotation and publishes sync"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var syncBefore, syncAfter int64
			syncTasks := db.Model(&model.SystemTask{}).Where("type = ?", model.SyncTaskType(model.SyncTopicChannels))
			require.NoError(t, syncTasks.Count(&syncBefore).Error)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			channel := &model.Channel{
				Type: constant.ChannelTypeCodex, Name: "codex-default-source-guard",
				Key: original, OtherInfo: otherInfo,
			}
			require.NoError(t, db.Create(channel).Error)
			if tc.writeFailed {
				const callback = "test:codex_refresh_credential_write"
				updates := db.Callback().Update()
				require.NoError(t, updates.Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if values, ok := tx.Statement.Dest.(map[string]any); ok && values["key"] != nil {
						_ = tx.AddError(fmt.Errorf("driver rejected replacement: %v", values["key"]))
					}
				}))
				t.Cleanup(func() { require.NoError(t, updates.Remove(callback)) })
			}
			var exchanges int
			httpClient = &http.Client{Transport: firstByteTestTransport(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				exchanges++
				assert.Equal(t, "auth.openai.com", req.URL.Host)
				assert.Equal(t, "/oauth/token", req.URL.Path)
				if tc.replace {
					if err := db.WithContext(req.Context()).Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", replacement).Error; err != nil {
						return nil, err
					}
				}
				if tc.cancel {
					cancel()
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"rotated-old","refresh_token":"rotated-old-refresh","expires_in":3600}`))}, nil
			})}
			key, source, err := RefreshCodexChannelCredential(ctx, channel.Id, CodexCredentialRefreshOptions{})
			assert.Equal(t, 1, exchanges)
			var stored model.Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			assert.Equal(t, otherInfo, stored.OtherInfo)
			require.NoError(t, syncTasks.Count(&syncAfter).Error)
			if tc.wantErr != nil || tc.writeFailed {
				if tc.writeFailed {
					require.EqualError(t, err, "codex channel: failed to persist refreshed credential")
				} else {
					require.ErrorIs(t, err, tc.wantErr)
				}
				assert.Nil(t, key)
				assert.Nil(t, source)
				wantKey := original
				if tc.replace {
					wantKey = replacement
				}
				assert.Equal(t, wantKey, stored.Key)
				assert.Equal(t, syncBefore, syncAfter)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, source)
			assert.Equal(t, original, source.Key, "the returned channel remains the source snapshot")
			persisted, err := parseCodexOAuthKey(stored.Key)
			require.NoError(t, err)
			assert.Equal(t, "rotated-old", persisted.AccessToken)
			assert.Equal(t, "rotated-old-refresh", persisted.RefreshToken)
			assert.Equal(t, key, persisted)
			assert.Equal(t, syncBefore+1, syncAfter)
		})
	}
}

func TestCodexChannelManagementReleaseLookupWaitIsCancellable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache codexClientVersionCache
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		client := &http.Client{Transport: firstByteTestTransport(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}
		done := make(chan error, 1)
		go func() {
			_, err := cache.get(ctx, client, codexLatestReleaseURL, time.Now())
			done <- err
		}()
		synctest.Wait()

		waitCtx, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		_, err := cache.get(waitCtx, client, codexLatestReleaseURL, time.Now())
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		select {
		case <-done:
			t.Fatal("canceling a waiting caller must not cancel the active lookup")
		default:
		}
		cancel()
		assert.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestCodexChannelManagementRejectsIncompleteInventory(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"models":null}`, `{"models":[{}]}`, `{"models":[],"error":"private provider detail"}`} {
		models, err := readCodexModelsResponse(strings.NewReader(body))
		require.EqualError(t, err, "Invalid response")
		assert.Nil(t, models)
	}
	models, err := readCodexModelsResponse(strings.NewReader(`{"models":[]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{}, models)
}
