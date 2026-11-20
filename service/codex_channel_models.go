package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// CodexModelDiscoveryPreview contains only explicitly supplied form fields.
// Nil fields use the current saved values; non-nil empty strings clear them.
// SourceType binds a saved draft's original type before its effective Codex type.
type CodexModelDiscoveryPreview struct {
	SourceType     int
	Key            *string
	BaseURL        *string
	HeaderOverride *string
	Proxy          *string
}

// CodexModelDiscoveryOptions separates controller-owned static headers from
// this service's provider requests. Admission is the operation's waiter, not a
// held lease. A caller with an outer lease passes its context and nil Admission.
type CodexModelDiscoveryOptions struct {
	Admission *ChannelDiagnosticAdmission
	Override  func(channel *model.Channel, accessToken string, headers http.Header) error
	Preview   *CodexModelDiscoveryPreview
}

func FetchCodexChannelModels(ctx context.Context, channel *model.Channel, options CodexModelDiscoveryOptions) ([]string, error) {
	if channel == nil || channel.Type != constant.ChannelTypeCodex {
		return nil, errors.New("channel type is not Codex")
	}
	if options.Preview != nil {
		preview := *options.Preview
		// Keep operation-owned values: callbacks must not replace a later
		// attempt's explicitly supplied fields by mutating the form pointers.
		if preview.Key != nil {
			preview.Key = common.GetPointer(*preview.Key)
		}
		if preview.BaseURL != nil {
			preview.BaseURL = common.GetPointer(*preview.BaseURL)
		}
		if preview.HeaderOverride != nil {
			preview.HeaderOverride = common.GetPointer(*preview.HeaderOverride)
		}
		if preview.Proxy != nil {
			preview.Proxy = common.GetPointer(*preview.Proxy)
		}
		options.Preview = &preview
	}
	snapshot := *channel
	outerLimit := managementConcurrencyLimit(snapshot.GetSetting())
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	current, err := currentCodexModelChannel(ctx, &snapshot, options.Preview)
	if err != nil {
		return nil, err
	}
	// GitHub is not the channel upstream. Look up the version before holding
	// an inner slot, using only the configured proxy, never channel overrides.
	version, err := lookupCodexModelClientVersion(ctx, current.GetSetting().Proxy)
	if err != nil {
		return nil, fmt.Errorf("failed to get Codex client version: %w", err)
	}
	if options.Admission == nil && current.Id > 0 {
		// The GitHub lookup may have waited; an outer lease cannot use a
		// channel snapshot taken before that wait.
		current, err = currentCodexModelChannel(ctx, current, options.Preview)
		if err != nil {
			return nil, err
		}
	}

	for retry := false; ; retry = true {
		status, models, used, err := fetchCodexChannelModelAttempt(ctx, current, outerLimit, version, options)
		if err != nil {
			return nil, err
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			return models, nil
		}
		// A typed credential is a read-only account probe, never permission
		// to refresh or fall back to the saved account. A draft type change
		// likewise cannot authorize refreshing a non-Codex source channel.
		draftCredential := options.Preview != nil && (options.Preview.Key != nil || (options.Preview.SourceType > 0 && options.Preview.SourceType != constant.ChannelTypeCodex))
		if status != http.StatusUnauthorized || retry || draftCredential {
			return nil, fmt.Errorf("upstream status: %d", status)
		}
		if used.Id <= 0 {
			return nil, errors.New("codex channel credential expired; save the channel before retrying model fetch")
		}

		// The 401 response is already closed. Refresh only the exact credential
		// that was sent, and never overwrite a concurrent replacement.
		_, _, refreshErr := RefreshCodexChannelCredential(ctx, used.Id, CodexCredentialRefreshOptions{
			ResetCaches: true,
			ExpectedKey: &used.Key,
		})
		if refreshErr != nil && !errors.Is(refreshErr, ErrCodexCredentialSourceChanged) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("failed to refresh Codex channel credential")
		}
		// Whether the refresh succeeded or lost to a replacement, the one
		// permitted retry uses the persisted credential and rechecks eligibility.
		current, err = currentCodexModelChannel(ctx, used, options.Preview)
		if err != nil {
			return nil, err
		}
	}
}

func currentCodexModelChannel(ctx context.Context, channel *model.Channel, preview *CodexModelDiscoveryPreview) (*model.Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current := *channel
	if channel.Id > 0 {
		current = model.Channel{}
		if err := model.DB.WithContext(ctx).First(&current, channel.Id).Error; err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("channel not found")
		}
	}
	expectedType := constant.ChannelTypeCodex
	if current.Id > 0 && preview != nil && preview.SourceType > 0 {
		expectedType = preview.SourceType
	}
	if current.Type != expectedType {
		return nil, errors.New("channel type is not Codex")
	}
	if current.ChannelInfo.IsMultiKey {
		return nil, errors.New("codex channel does not support multi-key model discovery")
	}
	if preview != nil {
		// Validate the real saved row before applying the effective draft.
		// The ID, current concurrency cap and all other transport policy stay
		// authoritative; only the explicit form fields are overlaid.
		current.Type = constant.ChannelTypeCodex
		if preview.Key != nil {
			current.Key = *preview.Key
		}
		if preview.BaseURL != nil {
			current.BaseURL = common.GetPointer(*preview.BaseURL)
		}
		if preview.HeaderOverride != nil {
			current.HeaderOverride = common.GetPointer(*preview.HeaderOverride)
		}
		if preview.Proxy != nil {
			settings := current.GetSetting()
			settings.Proxy = *preview.Proxy
			current.SetSetting(settings)
		}
	}
	return &current, nil
}

// One attempt owns at most one HTTP lease. Reload rebuilds the target,
// transport settings and header overrides from the same current channel row.
func fetchCodexChannelModelAttempt(ctx context.Context, channel *model.Channel, outerLimit int, version string, options CodexModelDiscoveryOptions) (int, []string, *model.Channel, error) {
	used := channel
	settings := channel.GetSetting()
	reloadAfterGrant := channel.Id > 0 && options.Admission != nil
	// An already-held outer lease cannot be reused under a different cap.
	if channel.Id > 0 && !reloadAfterGrant && managementConcurrencyLimit(settings) != outerLimit {
		return 0, nil, nil, errors.New("channel concurrency settings changed during admission")
	}
	prepare := func(current *model.Channel) (*http.Request, error) {
		credential, err := parseCodexOAuthKey(current.Key)
		if err != nil {
			return nil, err
		}
		baseURL := current.GetBaseURL()
		if baseURL == "" {
			baseURL = constant.GetChannelBaseURL(constant.ChannelTypeCodex)
		}
		req, err := newCodexModelsRequest(ctx, baseURL, credential, version)
		if err != nil {
			return nil, err
		}
		if options.Override != nil {
			if err := options.Override(current, strings.TrimSpace(credential.AccessToken), req.Header); err != nil {
				return nil, common.NewMessage("Invalid channel header override")
			}
		}
		return req, nil
	}

	call := ChannelManagementCall{
		Settings: settings, ChannelID: channel.Id,
		Admission: options.Admission, NoRedirect: true,
	}
	if reloadAfterGrant {
		// Only the context is needed before admission; prepare the credential
		// and target from the reloaded row, never from this snapshot.
		call.Request = (&http.Request{}).WithContext(ctx)
		call.Reload = func() (*http.Request, dto.ChannelSettings, error) {
			current, err := currentCodexModelChannel(ctx, channel, options.Preview)
			if err != nil {
				return nil, dto.ChannelSettings{}, err
			}
			fresh, err := prepare(current)
			if err != nil {
				return nil, dto.ChannelSettings{}, err
			}
			used = current
			return fresh, current.GetSetting(), nil
		}
	} else {
		req, err := prepare(channel)
		if err != nil {
			return 0, nil, nil, err
		}
		call.Request = req
	}
	resp, err := DoChannelManagementRequest(call)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// The status alone controls retry. Never consume a stalled upstream
		// error body or let provider text find its way into the console.
		return resp.StatusCode, nil, used, nil
	}
	models, err := readCodexModelsResponse(resp.Body)
	if ctx.Err() != nil {
		return resp.StatusCode, nil, used, ctx.Err()
	}
	return resp.StatusCode, models, used, err
}
