package controller

import (
	"context"
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
	previousAffiliate := common.RegisterAffiliateLinkEnabled
	previousPassword, previousEmail, previousToken := common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken
	common.RegisterEnabled, common.InviteRegisterEnabled, common.RegisterAffiliateLinkEnabled = true, true, true
	common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken = true, false, false
	t.Cleanup(func() {
		common.RegisterEnabled, common.InviteRegisterEnabled, common.RegisterAffiliateLinkEnabled = previousOpen, previousInvite, previousAffiliate
		common.PasswordRegisterEnabled, common.EmailVerificationEnabled, constant.GenerateDefaultToken = previousPassword, previousEmail, previousToken
	})
	// Add the new table beside existing account data and repeat migration.
	// A latest-release schema upgrade still needs the integration DB matrix.
	for range 2 {
		require.NoError(t, model.DB.AutoMigrate(&model.Invitation{}, &model.Log{}, &model.ExternalIdentityClaim{}, &model.ManagementOperationBatch{}, &model.AuditEventOutbox{}))
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

func TestInvitationAffiliateLinkNeverReplacesCode(t *testing.T) {
	for _, path := range []struct {
		name     string
		email    bool
		provider oauth.Provider
	}{
		{name: "password"},
		{name: "verified email", email: true},
		{name: "github", provider: &oauth.GitHubProvider{}},
		{name: "discord", provider: &oauth.DiscordProvider{}},
		{name: "oidc", provider: &oauth.OIDCProvider{}},
		{name: "linuxdo", provider: &oauth.LinuxDOProvider{}},
		{name: "custom oauth", provider: oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Id: 42, Slug: "r20-custom", Name: "R20 SSO", Enabled: true})},
	} {
		for _, affiliateEnabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/affiliate=%t", path.name, affiliateEnabled), func(t *testing.T) {
				setupInvitationTest(t)
				common.RegisterAffiliateLinkEnabled = affiliateEnabled
				common.EmailVerificationEnabled = path.email
				inviter := model.User{Username: "referral-owner", AffCode: "public-referral-link", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
				require.NoError(t, model.DB.Create(&inviter).Error)
				keys, err := model.CreateInvitations(1, "registration entitlement", 1, 0)
				require.NoError(t, err)
				for i, attempt := range []struct {
					name string
					code string
					want bool
				}{
					{name: "affiliate alone"},
					{name: "affiliate submitted as invitation", code: inviter.AffCode},
					{name: "valid invitation", code: keys[0], want: true},
					{name: "invitation replay", code: keys[0]},
				} {
					username := fmt.Sprintf("referral-user-%d", i)
					if path.provider == nil {
						fields := map[string]string{"username": username, "password": "safe-long-password-42", "aff_code": inviter.AffCode, "invitation_code": attempt.code}
						if path.email {
							fields["email"], fields["verification_code"] = username+"@example.com", "531415"
							common.RegisterVerificationCodeWithKey(fields["email"], fields["verification_code"], common.EmailVerificationPurpose)
						}
						body, err := common.Marshal(fields)
						require.NoError(t, err)
						response := securityEnrollmentRequest(http.MethodPost, "/api/user/register", string(body), "", service.AuthIdentity{}, Register)
						if path.email {
							common.DeleteKey(fields["email"], common.EmailVerificationPurpose)
						}
						result := decodeSecurityEnrollmentResponse(t, response)
						require.Equal(t, attempt.want, result.Success, "%s: %s", attempt.name, response.Body.String())
						if !attempt.want {
							assert.Contains(t, response.Body.String(), model.ErrInvitationInvalid.Error())
						}
					} else {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth/test", nil)
						_, _, err := findOrCreateOAuthUser(c, path.provider, &oauth.OAuthUser{ProviderUserID: fmt.Sprintf("%d", 7000+i), Username: username}, nil, inviter.AffCode, attempt.code)
						if attempt.want {
							require.NoError(t, err)
						} else {
							require.ErrorIs(t, err, model.ErrInvitationInvalid, attempt.name)
						}
					}
					var stored model.User
					err := model.DB.Where("username = ?", username).Take(&stored).Error
					if !attempt.want {
						assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "rejected registration cannot write an account")
						continue
					}
					require.NoError(t, err)
					wantInviter := 0
					if affiliateEnabled {
						wantInviter = inviter.Id
					}
					assert.Equal(t, wantInviter, stored.InviterId, "referral setting controls attribution, not entitlement")
					var invitation model.Invitation
					require.NoError(t, model.DB.Where("code = ?", keys[0]).Take(&invitation).Error)
					assert.Equal(t, model.InvitationUsed, invitation.Status)
					assert.Equal(t, stored.Id, invitation.UsedUserId)
				}
			})
		}
	}
}

func TestInvitationAffiliateSettingPersistsBeforeTakingEffect(t *testing.T) {
	setupInvitationTest(t)
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousOptions })
	require.NoError(t, model.UpdateOption("RegisterAffiliateLinkEnabled", "false"))
	assert.False(t, common.RegisterAffiliateLinkEnabled)
	assert.True(t, common.InviteRegisterEnabled, "disabling referral links must not relax the invitation requirement")
	var option model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: "RegisterAffiliateLinkEnabled"}).Take(&option).Error)
	assert.Equal(t, "false", option.Value)
	for _, invalid := range []string{"invalid", "", "1"} {
		body, err := common.Marshal(OptionUpdateRequest{Key: "RegisterAffiliateLinkEnabled", Value: invalid})
		require.NoError(t, err)
		response := securityEnrollmentRequest(http.MethodPut, "/api/option/", string(body), "", service.AuthIdentity{}, UpdateOption)
		assert.False(t, decodeSecurityEnrollmentResponse(t, response).Success)
		assert.False(t, common.RegisterAffiliateLinkEnabled)
	}
	require.NoError(t, model.DB.Migrator().DropTable(&model.Option{}))
	assert.Error(t, model.UpdateOption("RegisterAffiliateLinkEnabled", "true"))
	assert.False(t, common.RegisterAffiliateLinkEnabled, "storage failure cannot re-enable referral attribution")
}

func TestInvitationLoginOnlyMethodsCannotProvision(t *testing.T) {
	setupInvitationTest(t)
	keys, err := model.CreateInvitations(1, "not a login credential", 1, 0)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth/telegram", nil)
	_, _, err = findOrCreateOAuthUser(c, oauth.NewTelegramProvider(http.DefaultClient), &oauth.OAuthUser{ProviderUserID: "987654321"}, nil, "public-affiliate", keys[0])
	assert.ErrorIs(t, err, oauth.ErrTelegramAccountNotBound, "Telegram login cannot create an unbound account, even with an invitation")
	for _, endpoint := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{path: "/api/user/passkey/register/begin", handler: PasskeyRegisterBegin},
		{path: "/api/user/passkey/register/finish", handler: PasskeyRegisterFinish},
	} {
		body, err := common.Marshal(map[string]string{"aff_code": "public-affiliate", "invitation_code": keys[0]})
		require.NoError(t, err)
		response := securityEnrollmentRequest(http.MethodPost, endpoint.path, string(body), "", service.AuthIdentity{}, endpoint.handler)
		assert.False(t, decodeSecurityEnrollmentResponse(t, response).Success, "anonymous Passkey enrollment is not account registration")
	}
	response := securityEnrollmentRequest(http.MethodGet, "/api/oauth/telegram/login?aff=public-affiliate", "", "", service.AuthIdentity{}, TelegramLegacyAuth)
	assert.Equal(t, http.StatusGone, response.Code)
	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "only the explicitly seeded fixture user exists")
	var invitation model.Invitation
	require.NoError(t, model.DB.Where("code = ?", keys[0]).Take(&invitation).Error)
	assert.Equal(t, model.InvitationEnabled, invitation.Status)
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
	var outboxCount int64
	require.NoError(t, model.DB.Model(&model.AuditEventOutbox{}).Where("action = ?", "invitation.create").Count(&outboxCount).Error)
	require.EqualValues(t, 1, outboxCount, "the creation commits its audit obligation")
	require.Equal(t, 1, model.DrainAuditOutbox(context.Background()))
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

func TestManagementCodeCreateReplaySurvivesExpiry(t *testing.T) {
	for _, tc := range []struct {
		resource string
		handler  gin.HandlerFunc
	}{
		{resource: "invitation", handler: AddInvitations},
		{resource: "redemption", handler: AddRedemption},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			setupInvitationTest(t)
			confirmPaymentComplianceForTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.Redemption{}))
			operationType := tc.resource + ".create"
			fields := map[string]string{"name": "retry", "count": "1", "expired_time": "1"}
			body := map[string]any{"name": "retry", "count": 1, "expired_time": 1}
			if tc.resource == "redemption" {
				fields["quota"] = "100"
				fields["limit_per_user"] = "false"
				body["quota"] = 100
				body["limit_per_user"] = false
			}
			// Seed a historical committed batch whose expiry has passed. This
			// avoids sleeps and keeps the replay's original request unchanged.
			created, err := model.CreateCodeBatch(model.CodeBatchRequest{
				OperationId:   common.GetUUID(),
				OperationType: operationType,
				RequestDigest: model.CodeRequestDigest(operationType, fields),
				ActorUserId:   1,
				ActorRole:     common.RoleAdminUser,
				BuildRows: func() (any, []string, error) {
					key := common.GetUUID()
					if tc.resource == "invitation" {
						return []model.Invitation{{UserId: 1, Code: key, Name: "retry", Status: model.InvitationEnabled, CreatedTime: 1, ExpiredTime: 1}}, []string{key}, nil
					}
					return []model.Redemption{{UserId: 1, Key: key, Name: "retry", Quota: 100, Status: common.RedemptionCodeStatusEnabled, CreatedTime: 1, ExpiredTime: 1}}, []string{key}, nil
				},
				Audit: buildAuditDraft(auditActorSnapshot{UserId: 1, Role: common.RoleAdminUser}, operationType, nil),
			})
			require.NoError(t, err)
			var conflictBody string
			for _, attempt := range []struct {
				name        string
				operationId string
				actor       int
				expiry      int
				status      int
				replay      bool
			}{
				{name: "committed replay", operationId: created.OperationId, actor: 1, expiry: 1, status: http.StatusOK, replay: true},
				{name: "fresh expired create", operationId: common.GetUUID(), actor: 1, expiry: 1, status: http.StatusOK},
				{name: "changed expiry", operationId: created.OperationId, actor: 1, expiry: 2, status: http.StatusConflict},
				{name: "different actor", operationId: created.OperationId, actor: 2, expiry: 1, status: http.StatusConflict},
			} {
				body["expired_time"] = attempt.expiry
				encoded, err := common.Marshal(body)
				require.NoError(t, err)
				path := "/api/" + tc.resource + "/?operation_id=" + attempt.operationId
				response := securityEnrollmentRequest(http.MethodPost, path, string(encoded), "", service.AuthIdentity{UserID: attempt.actor}, tc.handler)
				assert.Equal(t, attempt.status, response.Code, attempt.name)
				var result struct {
					Success  bool     `json:"success"`
					Data     []string `json:"data"`
					Replayed bool     `json:"replayed"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, attempt.replay, result.Success, attempt.name)
				if attempt.replay {
					assert.True(t, result.Replayed)
					assert.Equal(t, created.Keys, result.Data)
				} else {
					assert.Empty(t, result.Data)
				}
				if attempt.status == http.StatusConflict {
					if conflictBody == "" {
						conflictBody = response.Body.String()
					} else {
						assert.Equal(t, conflictBody, response.Body.String(), "actor and payload conflicts have the same response")
					}
				}
			}
			var rows, batches, audits int64
			if tc.resource == "invitation" {
				require.NoError(t, model.DB.Model(&model.Invitation{}).Count(&rows).Error)
			} else {
				require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&rows).Error)
			}
			require.NoError(t, model.DB.Model(&model.ManagementOperationBatch{}).Count(&batches).Error)
			require.NoError(t, model.DB.Model(&model.AuditEventOutbox{}).Count(&audits).Error)
			assert.EqualValues(t, 1, rows)
			assert.EqualValues(t, 1, batches)
			assert.EqualValues(t, 1, audits)
		})
	}
}

func TestInvitationOAuthCallbackUsesServerPayload(t *testing.T) {
	for _, attempt := range []struct {
		name             string
		storedInvitation bool
		tamper           bool
	}{
		{name: "stored invitation", storedInvitation: true},
		{name: "tampered state", storedInvitation: true, tamper: true},
		{name: "affiliate with callback-only invitation"},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			setupInvitationTest(t)
			common.RegisterEnabled = false
			keys, err := model.CreateInvitations(1, "callback", 2, 0)
			require.NoError(t, err)
			inviter := model.User{Username: "callback-inviter", AffCode: "callback-affiliate"}
			require.NoError(t, model.DB.Create(&inviter).Error)
			const slug = "invitation-state-test"
			oauth.Register(slug, &authFlowTestOAuthProvider{})
			t.Cleanup(func() { oauth.Unregister(slug) })
			flowPayload := oauthFlowPayload{AffiliateCode: inviter.AffCode}
			if attempt.storedInvitation {
				flowPayload.InvitationCode = keys[0]
			}
			payload, err := common.Marshal(flowPayload)
			require.NoError(t, err)
			token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeOAuth, Provider: slug, Intent: model.AuthFlowIntentLogin, Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
			require.NoError(t, err)
			if attempt.tamper {
				token += "altered"
			}
			router := gin.New()
			router.GET("/api/oauth/:provider", HandleOAuth)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("GET", "/api/oauth/"+slug+"?code=provider-code&state="+token+"&aff="+inviter.AffCode+"&invitation_code="+keys[1], nil))
			result := decodeSecurityEnrollmentResponse(t, response)
			want := attempt.storedInvitation && !attempt.tamper
			assert.Equal(t, want, result.Success, response.Body.String())
			var invitation model.Invitation
			require.NoError(t, model.DB.Where("code = ?", keys[0]).Take(&invitation).Error)
			if want {
				assert.Equal(t, model.InvitationUsed, invitation.Status)
			} else {
				assert.Equal(t, model.InvitationEnabled, invitation.Status)
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
				for _, code := range []string{"", "public-affiliate-link", keys[0], ""} {
					body, err := common.Marshal(map[string]string{"code": "wechat-code", "invitation_code": code, "aff_code": "public-affiliate-link"})
					require.NoError(t, err)
					response := securityEnrollmentRequest("POST", "/api/oauth/wechat", string(body), "", service.AuthIdentity{}, WeChatAuth)
					result := decodeSecurityEnrollmentResponse(t, response)
					valid := code == keys[0] || !common.InviteRegisterEnabled
					assert.Equal(t, valid, result.Success, response.Body.String())
					if code == keys[0] {
						common.InviteRegisterEnabled = false
					}
				}
			}
		})
	}
}
