package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNormalizeAvatarURLRejectsUnusableAddresses(t *testing.T) {
	longURL := "https://cdn.example.com/avatar/" + strings.Repeat("a", AvatarURLMaxLength)

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https absolute", raw: "https://cdn.example.com/avatar/1.png", want: "https://cdn.example.com/avatar/1.png"},
		{name: "surrounding whitespace trimmed", raw: "  https://cdn.example.com/avatar/1.png ", want: "https://cdn.example.com/avatar/1.png"},
		{name: "empty", raw: "", want: ""},
		{name: "plain http rejected", raw: "http://cdn.example.com/avatar/1.png", want: ""},
		{name: "relative rejected", raw: "/uploads/avatar/1.png", want: ""},
		{name: "data url rejected", raw: "data:image/png;base64,AAAA", want: ""},
		{name: "scheme only rejected", raw: "https://", want: ""},
		{name: "embedded credentials rejected", raw: "https://user:secret@cdn.example.com/avatar/1.png", want: ""},
		{name: "over length rejected", raw: longURL, want: ""},
		{name: "control character rejected", raw: "https://cdn.example.com/avatar/1\n.png", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, NormalizeAvatarURL(test.raw))
		})
	}
}

// avatarTestProvider implements Provider without a picture claim, the shape of
// providers whose user info never carries an avatar.
type avatarTestProvider struct {
	name string
}

func (p *avatarTestProvider) GetName() string { return p.name }
func (p *avatarTestProvider) IsEnabled() bool { return true }
func (p *avatarTestProvider) ExchangeToken(context.Context, string, *gin.Context) (*OAuthToken, error) {
	return nil, nil
}
func (p *avatarTestProvider) GetUserInfo(context.Context, *OAuthToken) (*OAuthUser, error) {
	return nil, nil
}
func (p *avatarTestProvider) IsUserIDTaken(string) bool                      { return false }
func (p *avatarTestProvider) FillUserByProviderID(*model.User, string) error { return nil }
func (p *avatarTestProvider) SetProviderUserID(*model.User, string)          {}
func (p *avatarTestProvider) GetProviderPrefix() string                      { return "avatar_" }
func (p *avatarTestProvider) ProviderUserIDColumn() string                   { return "" }

// avatarCapableTestProvider adds the picture claim capability to the provider.
type avatarCapableTestProvider struct {
	avatarTestProvider
}

func (p *avatarCapableTestProvider) SupportsAvatar() bool { return true }

func TestSyncUserAvatar(t *testing.T) {
	setupAvatarTestDB(t)
	user := createAvatarTestUser(t, dto.UserSetting{Language: "zh", SidebarModules: `{"console":{"log":true}}`})
	gateway := &avatarCapableTestProvider{avatarTestProvider{name: "Gateway"}}

	t.Run("stores the provider picture and keeps other settings", func(t *testing.T) {
		SyncUserAvatar(context.Background(), user, gateway, "https://cdn.example.com/avatar/1.png")

		stored := loadAvatarTestSetting(t, user.Id)
		assert.Equal(t, "https://cdn.example.com/avatar/1.png", stored.AvatarUrl)
		assert.Equal(t, "zh", stored.Language)
		assert.Equal(t, `{"console":{"log":true}}`, stored.SidebarModules)
	})

	t.Run("refreshes a changed picture", func(t *testing.T) {
		SyncUserAvatar(context.Background(), user, gateway, "https://cdn.example.com/avatar/2.png")

		assert.Equal(t, "https://cdn.example.com/avatar/2.png", loadAvatarTestSetting(t, user.Id).AvatarUrl)
	})

	t.Run("clears the avatar when the provider reports no picture", func(t *testing.T) {
		SyncUserAvatar(context.Background(), user, gateway, "")

		assert.Empty(t, loadAvatarTestSetting(t, user.Id).AvatarUrl)
	})

	t.Run("keeps the stored avatar across providers without a picture claim", func(t *testing.T) {
		SyncUserAvatar(context.Background(), user, gateway, "https://cdn.example.com/avatar/3.png")

		SyncUserAvatar(context.Background(), user, &avatarTestProvider{name: "GitHub"}, "")

		assert.Equal(t, "https://cdn.example.com/avatar/3.png", loadAvatarTestSetting(t, user.Id).AvatarUrl)
	})

	t.Run("keeps the stored avatar when the reported address is unusable", func(t *testing.T) {
		SyncUserAvatar(context.Background(), user, gateway, "http://insecure.example.com/avatar.png")

		assert.Equal(t, "https://cdn.example.com/avatar/3.png", loadAvatarTestSetting(t, user.Id).AvatarUrl)
	})
}

func TestOIDCUserInfoCarriesPicture(t *testing.T) {
	server := jsonResponseServer(t, `{"sub":"sub-1","email":"user@example.com","preferred_username":"user","name":"User","picture":"https://cdn.example.com/avatar/oidc.png"}`)
	settings := system_setting.GetOIDCSettings()
	previous := settings.UserInfoEndpoint
	settings.UserInfoEndpoint = server
	t.Cleanup(func() { settings.UserInfoEndpoint = previous })

	info, err := (&OIDCProvider{}).GetUserInfo(context.Background(), &OAuthToken{AccessToken: "token"})
	require.NoError(t, err)

	assert.Equal(t, "https://cdn.example.com/avatar/oidc.png", info.AvatarURL)
	assert.True(t, (&OIDCProvider{}).SupportsAvatar())
}

func TestGenericOAuthUserInfoCarriesPicture(t *testing.T) {
	server := jsonResponseServer(t, `{"id":"gateway-user","preferred_username":"user","name":"User","email":"user@example.com","picture":"https://cdn.example.com/avatar/generic.png"}`)
	provider := NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Slug:                  "gateway",
		Name:                  "Gateway",
		Enabled:               true,
		AuthorizationEndpoint: server,
		TokenEndpoint:         server,
		UserInfoEndpoint:      server,
		UserIdField:           "id",
		UsernameField:         "preferred_username",
		DisplayNameField:      "name",
		EmailField:            "email",
	})

	info, err := provider.GetUserInfo(context.Background(), &OAuthToken{AccessToken: "token"})
	require.NoError(t, err)

	assert.Equal(t, "https://cdn.example.com/avatar/generic.png", info.AvatarURL)
	assert.True(t, provider.SupportsAvatar())
}

func jsonResponseServer(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write user info response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func setupAvatarTestDB(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
	})
}

func createAvatarTestUser(t *testing.T, setting dto.UserSetting) *model.User {
	t.Helper()
	settingBytes, err := common.Marshal(setting)
	require.NoError(t, err)
	user := &model.User{
		Username:    "avatar-test-user",
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Setting:     string(settingBytes),
		AuthVersion: 1,
		AffCode:     "avatar-test-aff-code",
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func loadAvatarTestSetting(t *testing.T, userId int) dto.UserSetting {
	t.Helper()
	setting, err := model.GetUserSetting(userId, true)
	require.NoError(t, err)
	return setting
}
