package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func GetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.FetchCodexWhamUsage,
		common.LogText("failed to fetch codex usage"),
		"Failed to fetch usage. Please try again later",
	)
}

func GetCodexChannelRateLimitResetCredits(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.FetchCodexWhamRateLimitResetCredits,
		common.LogText("failed to fetch codex reset credits"),
		"Failed to fetch reset credit details. Please try again later",
	)
}

func ResetCodexChannelUsage(c *gin.Context) {
	fetchCodexChannelWhamData(
		c,
		service.ConsumeCodexWhamRateLimitResetCredit,
		common.LogText("failed to reset codex usage"),
		"Failed to reset usage. Please try again later",
	)
}

type codexWhamFetchFunc func(
	ctx context.Context,
	send service.CodexWhamRequestSender,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error)

// One operation owns one admission deadline across the initial usage request
// and its optional post-refresh request. The Wham service owns body cleanup;
// only this sender applies channel policy, never the OAuth token exchange.
type codexWhamDispatch struct {
	channel             *model.Channel
	oauthKey            *codex.OAuthKey
	admission           *service.ChannelDiagnosticAdmission
	refreshedSourceKey  string
	refreshedCredential *codex.OAuthKey
}

func (dispatch *codexWhamDispatch) send(request *http.Request) (*http.Response, error) {
	return service.DoChannelManagementRequest(service.ChannelManagementCall{
		Request:    request,
		Settings:   dispatch.channel.GetSetting(),
		ChannelID:  dispatch.channel.Id,
		Admission:  dispatch.admission,
		Timeout:    15 * time.Second,
		NoRedirect: true,
		Reload: func() (*http.Request, dto.ChannelSettings, error) {
			return dispatch.reload(request)
		},
	})
}

// Rebuild from the unmodified Wham defaults after every grant, including a
// reacquisition for a changed cap. Never inherit browser headers or an override
// expanded against an older access token. Disabled channels remain usable for
// explicit administrator recovery; deletion/type/multi-key changes do not.
func (dispatch *codexWhamDispatch) reload(template *http.Request) (*http.Request, dto.ChannelSettings, error) {
	var current model.Channel
	if err := model.DB.WithContext(template.Context()).First(&current, "id = ?", dispatch.channel.Id).Error; err != nil {
		return nil, dto.ChannelSettings{}, errors.New("channel not found")
	}
	if current.Type != constant.ChannelTypeCodex {
		return nil, dto.ChannelSettings{}, errors.New("channel type is not Codex")
	}
	if current.ChannelInfo.IsMultiKey {
		return nil, dto.ChannelSettings{}, errors.New("multi-key channel is not supported")
	}
	credential, err := codex.ParseOAuthKey(strings.TrimSpace(current.Key))
	if err != nil {
		return nil, dto.ChannelSettings{}, errors.New("Failed to parse credentials. Please check the channel configuration")
	}
	if dispatch.refreshedCredential != nil && current.Key == dispatch.refreshedSourceKey {
		credential = dispatch.refreshedCredential
	}
	accessToken := strings.TrimSpace(credential.AccessToken)
	accountID := strings.TrimSpace(credential.AccountID)
	if accessToken == "" || accountID == "" {
		return nil, dto.ChannelSettings{}, errors.New("Failed to parse credentials. Please check the channel configuration")
	}
	// The service supplies only specialized Wham paths. Preserve the same path
	// and consume idempotency body when the current base URL or cap changes.
	pathIndex := strings.LastIndex(template.URL.Path, "/backend-api/wham/")
	if pathIndex < 0 {
		return nil, dto.ChannelSettings{}, errors.New("invalid upstream address")
	}
	upstreamURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(current.GetBaseURL()), "/") + template.URL.Path[pathIndex:])
	if err != nil {
		return nil, dto.ChannelSettings{}, errors.New("invalid upstream address")
	}
	fresh := template.Clone(template.Context())
	fresh.URL = upstreamURL
	fresh.Host = upstreamURL.Host
	if template.Body != nil {
		if template.GetBody == nil {
			return nil, dto.ChannelSettings{}, errors.New("invalid upstream request")
		}
		fresh.Body, err = template.GetBody()
		if err != nil {
			return nil, dto.ChannelSettings{}, errors.New("invalid upstream request")
		}
	}
	fresh.Header.Set("Authorization", "Bearer "+accessToken)
	fresh.Header.Set("ChatGPT-Account-Id", accountID)
	if err := applyFetchModelsHeaderOverrides(&current, accessToken, fresh.Header); err != nil {
		if fresh.Body != nil {
			_ = fresh.Body.Close()
		}
		return nil, dto.ChannelSettings{}, errors.New("Invalid channel header override")
	}
	dispatch.channel, dispatch.oauthKey = &current, credential
	return fresh, current.GetSetting(), nil
}

// Store only if the credential and channel type that authorized the refresh
// are still current. A competing administrator replacement must never be
// overwritten by a slower OAuth response. Retain the in-memory fallback solely
// while the database still holds that exact source credential.
func (dispatch *codexWhamDispatch) persistRefreshedCredential(ctx context.Context, credential *codex.OAuthKey) bool {
	dispatch.refreshedSourceKey = dispatch.channel.Key
	dispatch.refreshedCredential = credential
	encoded, err := common.Marshal(credential)
	if err != nil {
		return false
	}
	// Credential identity is byte-exact, independent of the column collation.
	// MySQL text equality can fold case and ignore trailing spaces; use binary
	// strings there, with explicit bytewise collations on the other dialects.
	keyMatch := clause.Expr{
		SQL:  "? = ?",
		Vars: []any{clause.Column{Name: "key"}, dispatch.refreshedSourceKey},
	}
	switch {
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		keyMatch.SQL = "CAST(? AS BINARY) = CAST(? AS BINARY)"
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		keyMatch.SQL = `? COLLATE "C" = ?`
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		keyMatch.SQL = "? COLLATE BINARY = ?"
	}
	// Both predicates and the update contain usable credentials. Discard SQL
	// logging even when the shared database was put into debug mode. The key
	// identifier is dialect-quoted by GORM; the source value stays parameterized.
	result := model.DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).
		Model(&model.Channel{}).
		Where(map[string]any{
			"id":   dispatch.channel.Id,
			"type": constant.ChannelTypeCodex,
		}).Where(keyMatch).Update("key", string(encoded))
	stored := result.Error == nil && result.RowsAffected > 0
	if stored {
		model.BumpSyncTopic(model.SyncTopicChannels)
	}
	model.InitChannelCache()
	return stored
}

func fetchCodexChannelWhamData(
	c *gin.Context,
	fetch codexWhamFetchFunc,
	logPrefix string,
	userMessage string,
) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != constant.ChannelTypeCodex {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Codex"})
		return
	}
	if ch.ChannelInfo.IsMultiKey {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "multi-key channel is not supported"})
		return
	}

	oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(ch.Key))
	if err != nil {
		common.SysError(common.LogText("failed to parse oauth key: %s", err.Error()))
		common.ApiErrorT(c, "Failed to parse credentials. Please check the channel configuration")
		return
	}
	accessToken := strings.TrimSpace(oauthKey.AccessToken)
	accountID := strings.TrimSpace(oauthKey.AccountID)
	if accessToken == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "codex channel: access_token is required"})
		return
	}
	if accountID == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "codex channel: account_id is required"})
		return
	}

	admission := service.NewChannelDiagnosticAdmission(c.Request.Context(), 0)
	defer admission.Close()
	dispatch := codexWhamDispatch{channel: ch, oauthKey: oauthKey, admission: admission}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	statusCode, body, err := fetch(ctx, dispatch.send, ch.GetBaseURL(), accessToken, accountID)
	if err != nil {
		common.SysError(logPrefix + ": " + err.Error())
		common.ApiErrorT(c, userMessage)
		return
	}

	// The sender reloads after admission, so refresh only the credential that
	// actually served this attempt. Its body and capacity lease are closed by
	// fetch before the independent OAuth exchange starts.
	ch, oauthKey = dispatch.channel, dispatch.oauthKey
	if (statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden) && strings.TrimSpace(oauthKey.RefreshToken) != "" {
		refreshCtx, refreshCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer refreshCancel()

		res, refreshErr := service.RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, ch.GetSetting().Proxy)
		if refreshErr == nil {
			oauthKey.AccessToken = res.AccessToken
			oauthKey.RefreshToken = res.RefreshToken
			oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
			oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
			if strings.TrimSpace(oauthKey.Type) == "" {
				oauthKey.Type = "codex"
			}

			dispatch.persistRefreshedCredential(refreshCtx, oauthKey)

			ctx2, cancel2 := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel2()
			statusCode, body, err = fetch(ctx2, dispatch.send, ch.GetBaseURL(), oauthKey.AccessToken, oauthKey.AccountID)
			if err != nil {
				common.SysError(common.LogText("%s after refresh: %s", logPrefix, err.Error()))
				common.ApiErrorT(c, userMessage)
				return
			}
		}
	}

	var payload any
	if common.Unmarshal(body, &payload) != nil {
		payload = string(body)
	}

	ok := statusCode >= 200 && statusCode < 300
	resp := gin.H{
		"success":         ok,
		"message":         "",
		"upstream_status": statusCode,
		"data":            payload,
	}
	if !ok {
		resp["message"] = fmt.Sprintf("upstream status: %d", statusCode)
	}
	c.JSON(http.StatusOK, resp)
}
