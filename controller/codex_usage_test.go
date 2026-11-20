package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexWhamChannelManagementHeadersAndSingleAttempt(t *testing.T) {
	setupChannelCreateTest(t)
	service.InitHttpClient()
	for _, tc := range []struct {
		name    string
		path    string
		method  string
		status  int
		handler gin.HandlerFunc
	}{
		{name: "usage", path: "/backend-api/wham/usage", method: http.MethodGet, status: http.StatusOK, handler: GetCodexChannelUsage},
		{name: "credits", path: "/backend-api/wham/rate-limit-reset-credits", method: http.MethodGet, status: http.StatusOK, handler: GetCodexChannelRateLimitResetCredits},
		{name: "consume failure is not retried", path: "/backend-api/wham/rate-limit-reset-credits/consume", method: http.MethodPost, status: http.StatusInternalServerError, handler: ResetCodexChannelUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var channelID int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, tc.method, r.Method)
				assert.Equal(t, "/prefix"+tc.path, r.URL.Path)
				assert.Equal(t, "Bearer access-only", r.Header.Get("Authorization"))
				assert.Equal(t, "account-one", r.Header.Get("ChatGPT-Account-Id"))
				assert.Equal(t, "access-only", r.Header.Get("X-Static-Key"))
				assert.Equal(t, "static-originator", r.Header.Get("originator"))
				assert.Equal(t, "application/json", r.Header.Get("Accept"))
				assert.Empty(t, r.Header.Get("X-Browser"))
				assert.Empty(t, r.Header.Get("X-Copied"))
				assert.NotContains(t, fmt.Sprint(r.Header), "id-token-private")
				reading := service.ReadChannelConcurrency(r.Context(), []int{channelID})[channelID]
				if assert.NotNil(t, reading.Current) {
					assert.Equal(t, 1, *reading.Current)
				}
				if tc.method == http.MethodPost {
					assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					var body struct {
						RedeemRequestID string `json:"redeem_request_id"`
					}
					if assert.NoError(t, common.DecodeJson(r.Body, &body)) {
						_, err := uuid.Parse(body.RedeemRequestID)
						assert.NoError(t, err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"remaining":2}`)
			}))
			defer upstream.Close()
			channel := &model.Channel{
				Type: constant.ChannelTypeCodex, Name: "wham-" + tc.name,
				Status:         common.ChannelStatusManuallyDisabled,
				BaseURL:        common.GetPointer(upstream.URL + "/prefix"),
				Key:            `{"access_token":"access-only","account_id":"account-one","id_token":"id-token-private"}`,
				HeaderOverride: common.GetPointer(`{"X-Static-Key":"{api_key}","originator":"static-originator","X-Copied":"{client_header:X-Browser}","*":""}`),
			}
			channel.SetSetting(dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
			require.NoError(t, model.DB.Create(channel).Error)
			t.Cleanup(func() { require.NoError(t, model.DB.Delete(channel).Error) })
			channelID = channel.Id

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
			c.Request = httptest.NewRequestWithContext(t.Context(), tc.method, "/api/channel/wham", nil)
			c.Request.Header.Set("Authorization", "Bearer browser-session")
			c.Request.Header.Set("X-Browser", "browser-private")
			tc.handler(c)

			var response struct {
				Success        bool `json:"success"`
				UpstreamStatus int  `json:"upstream_status"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, tc.status == http.StatusOK, response.Success)
			assert.Equal(t, tc.status, response.UpstreamStatus)
			assert.EqualValues(t, 1, calls.Load())
			assert.Zero(t, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)
		})
	}
}

func TestCodexWhamChannelManagementReloadsCurrentPolicy(t *testing.T) {
	setupChannelCreateTest(t)
	service.InitHttpClient()
	var oldCalls atomic.Int32
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer oldUpstream.Close()
	channel := &model.Channel{
		Type: constant.ChannelTypeCodex, Name: "wham-current", Status: common.ChannelStatusEnabled,
		BaseURL: common.GetPointer(oldUpstream.URL),
		Key:     `{"access_token":"old-access","account_id":"old-account"}`,
	}
	require.NoError(t, model.DB.Create(channel).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(channel).Error) })
	var calls atomic.Int32
	currentUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/current/backend-api/wham/usage", r.URL.Path)
		assert.Equal(t, "Bearer current-access", r.Header.Get("Authorization"))
		assert.Equal(t, "current-account", r.Header.Get("ChatGPT-Account-Id"))
		assert.Equal(t, "current-access", r.Header.Get("X-Current"))
		assert.Equal(t, "codex_cli_rs", r.Header.Get("originator"))
		assert.Equal(t, 1, *service.ReadChannelConcurrency(r.Context(), []int{channel.Id})[channel.Id].Current)
		_, _ = io.WriteString(w, `{"current":true}`)
	}))
	defer currentUpstream.Close()
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"base_url":        currentUpstream.URL + "/current",
		"key":             `{"access_token":"current-access","account_id":"current-account"}`,
		"header_override": `{"X-Current":"{api_key}"}`,
		"setting":         `{"max_concurrent_requests":1}`,
		"status":          common.ChannelStatusManuallyDisabled,
	}).Error)
	admission := service.NewChannelDiagnosticAdmission(t.Context(), 0)
	defer admission.Close()
	dispatch := codexWhamDispatch{channel: channel, admission: admission}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	status, body, err := service.FetchCodexWhamUsage(ctx, dispatch.send, channel.GetBaseURL(), "old-access", "old-account")

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"current":true}`, string(body))
	assert.Zero(t, oldCalls.Load())
	assert.EqualValues(t, 1, calls.Load())
	assert.Zero(t, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)

	// Rebuilding a post-refresh attempt expands placeholders using the new
	// access token, not stale defaults or the complete OAuth JSON document.
	dispatch.refreshedSourceKey = dispatch.channel.Key
	dispatch.refreshedCredential = &codex.OAuthKey{AccessToken: "refreshed-access", AccountID: "refreshed-account"}
	template, err := http.NewRequestWithContext(t.Context(), http.MethodGet, currentUpstream.URL+"/backend-api/wham/usage", nil)
	require.NoError(t, err)
	template.Header.Set("originator", "codex_cli_rs")
	fresh, _, err := dispatch.reload(template)
	require.NoError(t, err)
	assert.Equal(t, "Bearer refreshed-access", fresh.Header.Get("Authorization"))
	assert.Equal(t, "refreshed-access", fresh.Header.Get("X-Current"))
	assert.Equal(t, "refreshed-account", fresh.Header.Get("ChatGPT-Account-Id"))
}

func TestCodexWhamChannelManagementRefreshPreservesConcurrentReplacement(t *testing.T) {
	setupChannelCreateTest(t)
	service.InitHttpClient()
	for _, tc := range []struct {
		name           string
		replacementKey string
		replaceType    bool
		wantAccess     string
		wantAccount    string
	}{
		{name: "source credential unchanged", wantAccess: "refreshed-access", wantAccount: "refreshed-account"},
		{
			name:           "administrator replaces credential",
			replacementKey: `{"access_token":"replacement-access","account_id":"replacement-account"}`,
			wantAccess:     "replacement-access", wantAccount: "replacement-account",
		},
		{
			name:           "administrator changes only access token case",
			replacementKey: `{"access_token":"SOURCE-ACCESS","account_id":"source-account","refresh_token":"source-refresh"}`,
			wantAccess:     "SOURCE-ACCESS", wantAccount: "source-account",
		},
		{
			name:           "administrator adds trailing credential whitespace",
			replacementKey: `{"access_token":"source-access","account_id":"source-account","refresh_token":"source-refresh"}` + " ",
			wantAccess:     "source-access", wantAccount: "source-account",
		},
		{name: "administrator changes channel type", replaceType: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "Bearer "+tc.wantAccess, r.Header.Get("Authorization"))
				assert.Equal(t, tc.wantAccess, r.Header.Get("X-Current"))
				assert.Equal(t, tc.wantAccount, r.Header.Get("ChatGPT-Account-Id"))
				_, _ = io.WriteString(w, `{"current":true}`)
			}))
			defer upstream.Close()
			channel := &model.Channel{
				Type: constant.ChannelTypeCodex, Name: "wham-refresh-" + tc.name,
				BaseURL:        &upstream.URL,
				Key:            `{"access_token":"source-access","account_id":"source-account","refresh_token":"source-refresh"}`,
				HeaderOverride: common.GetPointer(`{"X-Current":"{api_key}"}`),
			}
			require.NoError(t, model.DB.Create(channel).Error)
			t.Cleanup(func() { require.NoError(t, model.DB.Delete(channel).Error) })
			admission := service.NewChannelDiagnosticAdmission(t.Context(), 0)
			defer admission.Close()
			dispatch := codexWhamDispatch{channel: channel, admission: admission}
			// The refresh has captured source A; the following committed edit
			// represents an administrator update before the OAuth response.
			if tc.replacementKey != "" {
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
					Update("key", tc.replacementKey).Error)
			}
			if tc.replaceType {
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
					Update("type", constant.ChannelTypeOpenAI).Error)
			}
			refreshed := &codex.OAuthKey{
				AccessToken: "refreshed-access", AccountID: "refreshed-account", RefreshToken: "rotated-refresh",
			}

			stored := dispatch.persistRefreshedCredential(t.Context(), refreshed)

			assert.Equal(t, tc.replacementKey == "" && !tc.replaceType, stored)
			current, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			if tc.replacementKey != "" {
				assert.Equal(t, tc.replacementKey, current.Key, "replacement bytes, including case and trailing whitespace, must survive")
			}
			credential, err := codex.ParseOAuthKey(current.Key)
			require.NoError(t, err)
			if tc.replaceType {
				assert.Equal(t, channel.Key, current.Key, "a non-Codex channel keeps its source credential")
				assert.Equal(t, constant.ChannelTypeOpenAI, current.Type)
			} else {
				assert.Equal(t, tc.wantAccess, credential.AccessToken)
				assert.Equal(t, tc.wantAccount, credential.AccountID)
			}

			status, _, err := service.FetchCodexWhamUsage(t.Context(), dispatch.send, upstream.URL, refreshed.AccessToken, refreshed.AccountID)
			if tc.replaceType {
				require.ErrorContains(t, err, "channel type is not Codex")
				assert.Zero(t, calls.Load())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, status)
			assert.EqualValues(t, 1, calls.Load(), "the post-refresh attempt uses the surviving credential exactly once")
			assert.Zero(t, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)
		})
	}
}

func TestCodexWhamChannelManagementCancellationAndBodyLifetime(t *testing.T) {
	setupChannelCreateTest(t)
	require.NoError(t, i18n.Init())
	service.InitHttpClient()
	bodyStarted := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"partial":`)
		w.(http.Flusher).Flush()
		close(bodyStarted)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	channel := &model.Channel{
		Type: constant.ChannelTypeCodex, Name: "wham-cancel", BaseURL: &upstream.URL,
		Key: `{"access_token":"access","account_id":"account"}`,
	}
	channel.SetSetting(dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
	require.NoError(t, model.DB.Create(channel).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(channel).Error) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	admission := service.NewChannelDiagnosticAdmission(ctx, 0)
	defer admission.Close()
	dispatch := codexWhamDispatch{channel: channel, admission: admission}
	done := make(chan error, 1)
	go func() {
		_, _, err := service.FetchCodexWhamUsage(ctx, dispatch.send, upstream.URL, "access", "account")
		done <- err
	}()
	select {
	case <-bodyStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("usage response did not start")
	}
	assert.Equal(t, 1, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current, "response headers do not release capacity")
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("usage body read did not stop")
	}
	select {
	case <-upstreamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request was not cancelled")
	}
	assert.Zero(t, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)

	// A saturated request that is cancelled never dispatches, regardless of
	// whether cancellation races queue registration; the existing slot remains.
	held, apiErr := service.AcquireChannelCapacity(t.Context(), channel.Id, channel.GetSetting())
	require.Nil(t, apiErr)
	defer held.Release()
	waitCtx, stopWaiting := context.WithCancel(t.Context())
	waitAdmission := service.NewChannelDiagnosticAdmission(waitCtx, 0)
	defer waitAdmission.Close()
	waitingDispatch := codexWhamDispatch{channel: channel, admission: waitAdmission}
	go func() {
		_, _, err := service.FetchCodexWhamUsage(waitCtx, waitingDispatch.send, upstream.URL, "access", "account")
		done <- err
	}()
	stopWaiting()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("saturated usage request did not cancel")
	}
	assert.Equal(t, 1, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)
	assert.EqualValues(t, 1, calls.Load(), "a cancelled saturated request must not reach upstream")
}

func TestCodexWhamChannelManagementConsumeDoesNotFollowRedirect(t *testing.T) {
	setupChannelCreateTest(t)
	service.InitHttpClient()
	var otherCalls atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
	}))
	defer other.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", other.URL+"/consume")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	channel := &model.Channel{
		Type: constant.ChannelTypeCodex, Name: "wham-redirect", BaseURL: &upstream.URL,
		Key:            `{"access_token":"access","account_id":"account"}`,
		HeaderOverride: common.GetPointer(`{"X-Secret":"channel-only"}`),
	}
	require.NoError(t, model.DB.Create(channel).Error)
	t.Cleanup(func() { require.NoError(t, model.DB.Delete(channel).Error) })
	admission := service.NewChannelDiagnosticAdmission(t.Context(), 0)
	defer admission.Close()
	dispatch := codexWhamDispatch{channel: channel, admission: admission}

	status, _, err := service.ConsumeCodexWhamRateLimitResetCredit(t.Context(), dispatch.send, upstream.URL, "access", "account")

	require.NoError(t, err)
	assert.Equal(t, http.StatusTemporaryRedirect, status)
	assert.EqualValues(t, 1, calls.Load())
	assert.Zero(t, otherCalls.Load(), "neither channel credentials nor the consume body cross origins")
	assert.Zero(t, *service.ReadChannelConcurrency(t.Context(), []int{channel.Id})[channel.Id].Current)
}
