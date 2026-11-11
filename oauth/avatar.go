package oauth

import (
	"context"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// AvatarURLMaxLength bounds a stored avatar address. Provider picture claims are
// short absolute URLs; anything longer is dropped instead of being truncated into
// an address the browser cannot load.
const AvatarURLMaxLength = 1024

// NormalizeAvatarURL validates a provider-supplied avatar address and returns the
// form that may be stored, or "" when the value must not be used.
//
// Only absolute https URLs without embedded credentials are accepted: the address
// is rendered by browsers, so a mixed-content or credential-carrying URL is
// rejected rather than repaired.
func NormalizeAvatarURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > AvatarURLMaxLength {
		return ""
	}
	// Control characters and whitespace never belong in a URL and are rejected by
	// browsers in an unpredictable way once re-encoded.
	if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Opaque != "" {
		return ""
	}
	if parsed.User != nil {
		return ""
	}
	return parsed.String()
}

// SyncUserAvatar stores the picture the provider reported for the signing-in
// account, so the dashboard shows the provider avatar instead of the initials
// fallback. It is called on every OAuth/OIDC login.
//
// Providers that do not report a picture are skipped: for them an empty value is
// not evidence that the stored avatar is gone. For providers that do report one,
// an empty claim clears the stored avatar and an unparsable one is ignored so a
// transient provider change cannot wipe a usable address.
func SyncUserAvatar(ctx context.Context, user *model.User, provider Provider, avatarURL string) {
	if user == nil || user.Id <= 0 {
		return
	}
	if _, ok := provider.(AvatarProvider); !ok {
		return
	}
	next := NormalizeAvatarURL(avatarURL)
	if next == "" && strings.TrimSpace(avatarURL) != "" {
		logger.LogWarn(ctx, common.LogText("[OAuth] ignored an unusable avatar address from %s for user %d", provider.GetName(), user.Id))
		return
	}
	current, err := model.GetUserSetting(user.Id, true)
	if err != nil {
		logger.LogError(ctx, common.LogText("[OAuth] failed to read the setting of user %d: %s", user.Id, err.Error()))
		return
	}
	if current.AvatarUrl == next {
		return
	}
	current.AvatarUrl = next
	if err := model.UpdateUserSetting(user.Id, current); err != nil {
		logger.LogError(ctx, common.LogText("[OAuth] failed to store the avatar of user %d: %s", user.Id, err.Error()))
		return
	}
	user.SetSetting(current)
}
