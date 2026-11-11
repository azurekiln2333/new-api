package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupInvitationTest(t *testing.T) {
	t.Helper()
	setupSecurityEnrollmentTest(t)
	previousOpen, previousInvite := common.RegisterEnabled, common.InviteRegisterEnabled
	previousPassword, previousEmail, previousToken := common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken
	common.RegisterEnabled, common.InviteRegisterEnabled = true, true
	common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken = true, false, false
	t.Cleanup(func() {
		common.RegisterEnabled, common.InviteRegisterEnabled = previousOpen, previousInvite
		common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken = previousPassword, previousEmail, previousToken
	})
	// Add the new table beside existing account data and repeat migration.
	// A latest-release schema upgrade still needs the integration DB matrix.
	for range 2 {
		require.NoError(t, model.DB.AutoMigrate(&model.Invitation{}, &model.Log{}, &model.ExternalIdentityClaim{}))
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username = ?", "enrollment-user").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestInvitationAtomicRegistration(t *testing.T) {
	setupInvitationTest(t)
	keys, err := model.CreateInvitations(1, "single use", 3, 0)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Go(func() {
			<-start
			user := model.User{Username: fmt.Sprintf("invited-%d", i), Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			results <- model.DB.Transaction(func(tx *gorm.DB) error { return model.InsertUserWithInvitation(tx, &user, 0, keys[0]) })
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, model.ErrInvitationInvalid)
		}
	}
	assert.Equal(t, 1, successes)
	var used model.Invitation
	require.NoError(t, model.DB.Where("code = ?", keys[0]).First(&used).Error)
	assert.Equal(t, model.InvitationUsed, used.Status)
	assert.Positive(t, used.UsedUserId)
	assert.Positive(t, used.UsedTime)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("username LIKE ?", "invited-%").Count(&count).Error)
	assert.EqualValues(t, 1, count)

	// A failed binding/account operation rolls both the new user and claim back.
	rollback := errors.New("binding failed")
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		user := model.User{Username: "rolled-back", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
		if err := model.InsertUserWithInvitation(tx, &user, 0, keys[1]); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	user := model.User{Username: "retried", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.InsertUserWithInvitation(tx, &user, 0, keys[1]) }))
	for _, status := range []int{model.InvitationDisabled, model.InvitationUsed} {
		require.NoError(t, model.DB.Model(&model.Invitation{}).Where("code = ?", keys[2]).Update("status", status).Error)
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			return model.InsertUserWithInvitation(tx, &model.User{Username: "blocked"}, 0, keys[2])
		})
		assert.ErrorIs(t, err, model.ErrInvitationInvalid)
	}
	require.NoError(t, model.DB.Model(&model.Invitation{}).Where("code = ?", keys[2]).Updates(map[string]any{"status": model.InvitationEnabled, "expired_time": time.Now().Unix() - 1}).Error)
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		return model.InsertUserWithInvitation(tx, &model.User{Username: "expired"}, 0, keys[2])
	})
	assert.ErrorIs(t, err, model.ErrInvitationInvalid)
	assert.Error(t, model.DB.Create(&model.Invitation{Code: keys[2], Name: "duplicate", Status: model.InvitationEnabled}).Error)
}

func TestInvitationRegistrationModes(t *testing.T) {
	for _, open := range []bool{false, true} {
		for _, invite := range []bool{false, true} {
			t.Run(fmt.Sprintf("open=%t/invite=%t", open, invite), func(t *testing.T) {
				setupInvitationTest(t)
				common.RegisterEnabled, common.InviteRegisterEnabled = open, invite
				keys, err := model.CreateInvitations(1, "registration", 4, 0)
				require.NoError(t, err)
				for i, code := range []string{"", keys[0]} {
					body, err := common.Marshal(map[string]any{"username": fmt.Sprintf("password-%d", i), "password": "safe-long-password-42", "invitation_code": code})
					require.NoError(t, err)
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(string(body)))
					Register(c)
					var result struct {
						Success bool `json:"success"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					assert.Equal(t, open && (!invite || code != ""), result.Success, response.Body.String())
				}
				for i, code := range []string{"", keys[1]} {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodGet, "/oauth/test", nil)
					user, _, err := findOrCreateOAuthUser(c, &authFlowTestOAuthProvider{}, &oauth.OAuthUser{ProviderUserID: fmt.Sprintf("new-%d", i), Username: fmt.Sprintf("oauth-%d", i)}, nil, "", code)
					want := (open || invite) && (!invite || code != "")
					if want {
						require.NoError(t, err)
						require.Positive(t, user.Id)
					} else {
						require.Error(t, err)
					}
				}
			})
		}
	}
}

func TestInvitationManagement(t *testing.T) {
	setupInvitationTest(t)
	keys, err := model.CreateInvitations(1, "100%_literal", 2, 0)
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, model.DB.AutoMigrate(&model.Invitation{}))
	}
	rows, count, err := model.ListInvitations("%_", "1", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
	require.Len(t, rows, 2)
	assert.ElementsMatch(t, keys, []string{rows[0].Code, rows[1].Code}, "repeat migrations preserve existing invitations")
	status := model.InvitationDisabled
	require.NoError(t, model.UpdateInvitation(rows[0].Id, "", 0, &status))
	status = model.InvitationEnabled
	require.NoError(t, model.UpdateInvitation(rows[0].Id, "", 0, &status))
	require.NoError(t, model.UpdateInvitation(rows[0].Id, "renamed", time.Now().Add(time.Hour).Unix(), nil))
	user := model.User{Username: "managed-invitation"}
	require.NoError(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.InsertUserWithInvitation(tx, &user, 0, keys[0]) }))
	var used model.Invitation
	require.NoError(t, model.DB.Where("code = ?", keys[0]).First(&used).Error)
	assert.Error(t, model.UpdateInvitation(used.Id, "", 0, &status), "used codes cannot be re-enabled")
	deleted, err := model.DeleteInvitations(nil, true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	rows, count, err = model.ListInvitations("", "", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	_, err = model.DeleteInvitations([]int{rows[0].Id}, false)
	require.NoError(t, err)
}

func TestInvitationCreationValidationAndSafeAudit(t *testing.T) {
	setupInvitationTest(t)
	for _, body := range []string{`{"name":"","count":1}`, `{"name":"batch","count":101}`, `{"name":"batch","count":1,"expired_time":-1}`} {
		response := securityEnrollmentRequest("POST", "/api/invitation/", body, "", service.AuthIdentity{UserID: 1}, AddInvitations)
		assert.False(t, decodeSecurityEnrollmentResponse(t, response).Success)
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.Invitation{}).Count(&count).Error)
	assert.Zero(t, count)
	response := securityEnrollmentRequest("POST", "/api/invitation/", `{"name":"admin-secret-name","count":2}`, "", service.AuthIdentity{UserID: 1}, AddInvitations)
	result := decodeSecurityEnrollmentResponse(t, response)
	require.True(t, result.Success, response.Body.String())
	var codes []string
	require.NoError(t, common.Unmarshal(result.Data, &codes))
	require.Len(t, codes, 2)
	var entry model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "invitation.create").Take(&entry).Error)
	serialized, err := common.Marshal(entry)
	require.NoError(t, err)
	for _, code := range codes {
		assert.NotContains(t, string(serialized), code)
	}
	assert.NotContains(t, string(serialized), "admin-secret-name")
	assert.Equal(t, "Created invitation codes", entry.Content)
}

func TestInvitationOAuthCallbackUsesServerPayload(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(fmt.Sprintf("tamper=%t", tamper), func(t *testing.T) {
			setupInvitationTest(t)
			common.RegisterEnabled = false
			keys, err := model.CreateInvitations(1, "callback", 2, 0)
			require.NoError(t, err)
			const slug = "invitation-state-test"
			oauth.Register(slug, &authFlowTestOAuthProvider{})
			t.Cleanup(func() { oauth.Unregister(slug) })
			payload, err := common.Marshal(oauthFlowPayload{InvitationCode: keys[0]})
			require.NoError(t, err)
			token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeOAuth, Provider: slug, Intent: model.AuthFlowIntentLogin, Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
			require.NoError(t, err)
			if tamper {
				token += "altered"
			}
			router := gin.New()
			router.GET("/api/oauth/:provider", HandleOAuth)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/api/oauth/"+slug+"?code=provider-code&state="+token+"&invitation_code="+keys[1], nil))
			result := decodeSecurityEnrollmentResponse(t, response)
			assert.Equal(t, !tamper, result.Success, response.Body.String())
			var invitation model.Invitation
			require.NoError(t, model.DB.Where("code = ?", keys[0]).Take(&invitation).Error)
			if tamper {
				assert.Equal(t, model.InvitationEnabled, invitation.Status)
			} else {
				assert.Equal(t, model.InvitationUsed, invitation.Status)
			}
			require.NoError(t, model.DB.Where("code = ?", keys[1]).Take(&model.Invitation{}).Error)
			var unused int64
			require.NoError(t, model.DB.Model(&model.Invitation{}).Where("code = ? AND status = ?", keys[1], model.InvitationEnabled).Count(&unused).Error)
			assert.EqualValues(t, 1, unused, "callback query cannot replace the server-owned invitation")
		})
	}
}

func TestInvitationCustomOAuthAndWeChat(t *testing.T) {
	for _, transport := range []string{"custom oauth", "wechat"} {
		t.Run(transport, func(t *testing.T) {
			setupInvitationTest(t)
			common.RegisterEnabled = false
			keys, err := model.CreateInvitations(1, "sso", 1, 0)
			require.NoError(t, err)
			if transport == "custom oauth" {
				provider := oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Id: 42, Slug: "invited-custom", Name: "Invited SSO", Enabled: true})
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("GET", "/api/oauth/invited-custom", nil)
				external := &oauth.OAuthUser{ProviderUserID: "invited-external", Username: "custom-invited"}
				_, _, err := findOrCreateOAuthUser(c, provider, external, nil, "")
				require.ErrorIs(t, err, model.ErrInvitationInvalid)
				user, _, err := findOrCreateOAuthUser(c, provider, external, nil, "", keys[0])
				require.NoError(t, err)
				var binding model.UserOAuthBinding
				require.NoError(t, model.DB.Where("provider_id = ? AND provider_user_id = ?", 42, external.ProviderUserID).Take(&binding).Error)
				assert.Equal(t, user.Id, binding.UserId)
				common.InviteRegisterEnabled = false
				existing, _, err := findOrCreateOAuthUser(c, provider, external, nil, "")
				require.NoError(t, err)
				assert.Equal(t, user.Id, existing.Id, "existing users can log in with both registration switches off")
			} else {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "wechat-code", r.URL.Query().Get("code"))
					_, _ = w.Write([]byte(`{"success":true,"data":"invited-wechat"}`))
				}))
				t.Cleanup(upstream.Close)
				previousEnabled, previousAddress := common.WeChatAuthEnabled, common.WeChatServerAddress
				common.WeChatAuthEnabled, common.WeChatServerAddress = true, upstream.URL
				t.Cleanup(func() { common.WeChatAuthEnabled, common.WeChatServerAddress = previousEnabled, previousAddress })
				for _, code := range []string{"", keys[0], ""} {
					body, err := common.Marshal(map[string]string{"code": "wechat-code", "invitation_code": code})
					require.NoError(t, err)
					response := securityEnrollmentRequest("POST", "/api/oauth/wechat", string(body), "", service.AuthIdentity{}, WeChatAuth)
					result := decodeSecurityEnrollmentResponse(t, response)
					assert.Equal(t, code != "" || !common.InviteRegisterEnabled, result.Success, response.Body.String())
					if code != "" {
						common.InviteRegisterEnabled = false
					}
				}
			}
		})
	}
}
