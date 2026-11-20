package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type CodexCredentialRefreshOptions struct {
	ResetCaches bool
	ExpectedKey *string
}

// ErrCodexCredentialSourceChanged means a refresh cannot replace the current credential.
var ErrCodexCredentialSourceChanged = errors.New("codex channel: credential source changed during refresh")

type CodexOAuthKey struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	AccountID   string `json:"account_id,omitempty"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type,omitempty"`
	Expired     string `json:"expired,omitempty"`
}

func parseCodexOAuthKey(raw string) (*CodexOAuthKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("codex channel: empty oauth key")
	}
	var key CodexOAuthKey
	if err := common.Unmarshal([]byte(raw), &key); err != nil {
		return nil, errors.New("codex channel: invalid oauth key json")
	}
	return &key, nil
}

func RefreshCodexChannelCredential(ctx context.Context, channelID int, opts CodexCredentialRefreshOptions) (*CodexOAuthKey, *model.Channel, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ch := &model.Channel{}
	if err := model.DB.WithContext(refreshCtx).Session(&gorm.Session{Logger: gormlogger.Discard}).First(ch, "id = ?", channelID).Error; err != nil {
		return nil, nil, err
	}
	if ch.Type != constant.ChannelTypeCodex {
		return nil, nil, fmt.Errorf("channel type is not Codex")
	}
	if ch.ChannelInfo.IsMultiKey {
		return nil, nil, errors.New("codex channel: credential refresh requires a single key")
	}
	if opts.ExpectedKey != nil && ch.Key != *opts.ExpectedKey {
		return nil, nil, ErrCodexCredentialSourceChanged
	}
	sourceKey := ch.Key

	oauthKey, err := parseCodexOAuthKey(strings.TrimSpace(sourceKey))
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(oauthKey.RefreshToken) == "" {
		return nil, nil, fmt.Errorf("codex channel: refresh_token is required to refresh credential")
	}

	res, err := RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, ch.GetSetting().Proxy)
	if err != nil {
		return nil, nil, err
	}

	oauthKey.AccessToken = res.AccessToken
	oauthKey.RefreshToken = res.RefreshToken
	oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
	oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
	if strings.TrimSpace(oauthKey.Type) == "" {
		oauthKey.Type = "codex"
	}

	if strings.TrimSpace(oauthKey.AccountID) == "" {
		if accountID, ok := ExtractCodexAccountIDFromJWT(oauthKey.AccessToken); ok {
			oauthKey.AccountID = accountID
		}
	}
	if strings.TrimSpace(oauthKey.Email) == "" {
		if email, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
			oauthKey.Email = email
		}
	}

	encoded, err := common.Marshal(oauthKey)
	if err != nil {
		return nil, nil, err
	}

	_, err = model.UpdateChannelOtherInfoLocked(refreshCtx, ch.Id, func(current *model.Channel) (map[string]any, error) {
		// Compare in Go: SQL text equality may ignore case on MySQL.
		if current.Type != constant.ChannelTypeCodex || current.ChannelInfo.IsMultiKey || current.Key != sourceKey {
			return nil, ErrCodexCredentialSourceChanged
		}
		return map[string]any{"key": string(encoded)}, nil
	})
	if err != nil {
		if errors.Is(err, ErrCodexCredentialSourceChanged) {
			return nil, nil, ErrCodexCredentialSourceChanged
		}
		if refreshCtx.Err() != nil {
			return nil, nil, refreshCtx.Err()
		}
		// Driver errors may contain the replacement credential.
		return nil, nil, errors.New("codex channel: failed to persist refreshed credential")
	}
	model.BumpSyncTopic(model.SyncTopicChannels)

	if opts.ResetCaches {
		model.InitChannelCache()
	}

	return oauthKey, ch, nil
}
