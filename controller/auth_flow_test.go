package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/fxamacker/cbor/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newSecurityLoginPasskey(t *testing.T, userID int) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	credentialID := sha256.Sum256(elliptic.Marshal(key.Curve, key.X, key.Y))
	publicKey, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.PasskeyCredential{
		UserID: userID, CredentialID: base64.StdEncoding.EncodeToString(credentialID[:]), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		UserPresent: true, UserVerified: true,
	}).Error)
	return key
}

func beginSecurityLoginPasskey(t *testing.T, parentToken string) (string, string) {
	t.Helper()
	body, err := common.Marshal(map[string]string{"flow_token": parentToken})
	require.NoError(t, err)
	response := securityEnrollmentRequest("POST", "/api/user/login/passkey/begin", string(body), "", service.AuthIdentity{}, LoginPasskeyBegin)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
			Options   struct {
				PublicKey struct {
					Challenge        string `json:"challenge"`
					UserVerification string `json:"userVerification"`
				} `json:"publicKey"`
			} `json:"options"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	require.Equal(t, "required", result.Data.Options.PublicKey.UserVerification)
	return result.Data.FlowToken, result.Data.Options.PublicKey.Challenge
}

func TestSecurityLoginCodeCompletesOnce(t *testing.T) {
	for _, test := range []struct {
		path   string
		backup bool
	}{{"/api/user/login/verify", false}, {"/api/user/login/2fa", false}, {"/api/user/login/verify", true}} {
		t.Run(fmt.Sprintf("%s/backup=%t", test.path, test.backup), func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			factor := &model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
			require.NoError(t, model.DB.Create(factor).Error)
			challenge, err := service.StartLoginVerification(user, "password", nil)
			require.NoError(t, err)
			require.NotNil(t, challenge)
			code, err := totp.GenerateCode(factor.Secret, time.Now())
			require.NoError(t, err)
			if test.backup {
				code = "ABCD-1234"
				hash, err := common.HashBackupCode(code)
				require.NoError(t, err)
				require.NoError(t, model.DB.Create(&model.TwoFABackupCode{UserId: user.Id, CodeHash: hash}).Error)
			}
			body, err := common.Marshal(map[string]string{"flow_token": challenge.FlowToken, "code": code})
			require.NoError(t, err)
			router := gin.New()
			router.POST("/api/user/login/verify", VerifyLogin)
			router.POST("/api/user/login/2fa", Verify2FALogin)
			for attempt := range 2 {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("POST", test.path, strings.NewReader(string(body))))
				var result securityEnrollmentResponse
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, attempt == 0, result.Success, response.Body.String())
				if attempt == 0 {
					var bundle service.AuthBundle
					require.NoError(t, common.Unmarshal(result.Data, &bundle))
					assert.NotEmpty(t, bundle.AccessToken)
					assert.Equal(t, "password", bundle.Session.LoginMethod)
					assert.NotEmpty(t, response.Header().Values("Set-Cookie"))
				} else {
					assert.Empty(t, response.Header().Values("Set-Cookie"))
				}
			}
			count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			assert.EqualValues(t, 2, count)
		})
	}
}

func TestSecurityLoginRejectsChangedOrExpiredAuthorization(t *testing.T) {
	for _, change := range []string{"expired", "disabled user", "auth version", "factor removed", "factor locked", "legacy flow", "wrong method", "other purpose"} {
		t.Run(change, func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			factor := &model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
			require.NoError(t, model.DB.Create(factor).Error)
			challenge, err := service.StartLoginVerification(user, "password", nil)
			require.NoError(t, err)
			method := "2fa"
			switch change {
			case "expired":
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposeLoginVerification).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			case "disabled user":
				require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			case "auth version":
				require.NoError(t, model.DB.Model(user).Update("auth_version", user.AuthVersion+1).Error)
			case "factor removed":
				require.NoError(t, model.DB.Delete(factor).Error)
			case "factor locked":
				require.NoError(t, model.DB.Model(factor).Update("locked_until", time.Now().Add(time.Minute)).Error)
			case "legacy flow":
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposeLoginVerification).Update("purpose", model.AuthFlowPurposeTwoFALogin).Error)
			case "wrong method":
				method = "password"
			case "other purpose":
				require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("purpose = ?", model.AuthFlowPurposeLoginVerification).Update("purpose", model.AuthFlowPurposeSecurityProof).Error)
			}
			code, err := totp.GenerateCode(factor.Secret, time.Now())
			require.NoError(t, err)
			body, err := common.Marshal(map[string]string{"flow_token": challenge.FlowToken, "code": code, "method": method})
			require.NoError(t, err)
			response := securityEnrollmentRequest("POST", "/api/user/login/verify", string(body), "", service.AuthIdentity{}, VerifyLogin)
			var result securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success, change)
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestSecurityLoginPasskeyDoesNotRequireAdditionalTwoFA(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			key := newSecurityLoginPasskey(t, user.Id)
			require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
			for _, verified := range []bool{false, true} {
				var flowToken, challenge, parentToken string
				if direct {
					response := securityEnrollmentRequest("POST", "/api/user/passkey/login/begin", "", "", service.AuthIdentity{}, PasskeyLoginBegin)
					var result struct {
						Data struct {
							FlowToken string `json:"flow_token"`
							Options   struct {
								PublicKey struct {
									Challenge        string `json:"challenge"`
									UserVerification string `json:"userVerification"`
								} `json:"publicKey"`
							} `json:"options"`
						} `json:"data"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					require.Equal(t, "required", result.Data.Options.PublicKey.UserVerification)
					flowToken, challenge = result.Data.FlowToken, result.Data.Options.PublicKey.Challenge
				} else {
					pending, err := service.StartLoginVerification(user, "oauth:github", nil)
					require.NoError(t, err)
					parentToken = pending.FlowToken
					flowToken, challenge = beginSecurityLoginPasskey(t, parentToken)
				}
				var assertion map[string]any
				require.NoError(t, common.Unmarshal(securityPasskeyResponse(t, key, challenge, false, 0, verified), &assertion))
				if direct {
					response, ok := assertion["response"].(map[string]any)
					require.True(t, ok)
					response["userHandle"] = base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprint(user.Id)))
				}
				payload := map[string]any{"flow_token": flowToken, "credential": assertion}
				handler, path := PasskeyLoginFinish, "/api/user/passkey/login/finish"
				if !direct {
					payload["flow_token"], payload["passkey_flow_token"] = parentToken, flowToken
					handler, path = LoginPasskeyFinish, "/api/user/login/passkey/finish"
				}
				body, err := common.Marshal(payload)
				require.NoError(t, err)
				router := gin.New()
				router.POST(path, handler)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("POST", path, strings.NewReader(string(body))))
				var result securityEnrollmentResponse
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				require.Equal(t, verified, result.Success, response.Body.String())
				if verified {
					var bundle service.AuthBundle
					require.NoError(t, common.Unmarshal(result.Data, &bundle))
					assert.NotEmpty(t, bundle.AccessToken)
				} else {
					assert.Empty(t, response.Header().Values("Set-Cookie"))
				}
			}
		})
	}
}

func TestSecurityLoginPasskeyConcurrentCompletionCreatesOneSession(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	key := newSecurityLoginPasskey(t, user.Id)
	pending, err := service.StartLoginVerification(user, "password", nil)
	require.NoError(t, err)
	requests := make([]string, 2)
	for index := range requests {
		token, challenge := beginSecurityLoginPasskey(t, pending.FlowToken)
		body, err := common.Marshal(map[string]any{
			"flow_token": pending.FlowToken, "passkey_flow_token": token,
			"credential": securityPasskeyResponse(t, key, challenge, false, 0),
		})
		require.NoError(t, err)
		requests[index] = string(body)
	}
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for _, body := range requests {
		workers.Go(func() {
			<-start
			responses <- securityEnrollmentRequest("POST", "/api/user/login/passkey/finish", body, "", service.AuthIdentity{}, LoginPasskeyFinish)
		})
	}
	close(start)
	workers.Wait()
	close(responses)
	successes := 0
	for response := range responses {
		var result securityEnrollmentResponse
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		if result.Success {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
	count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
}

func TestSecurityLoginSessionFailureRollsBackChallengeConsumption(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	key := newSecurityLoginPasskey(t, user.Id)
	pending, err := service.StartLoginVerification(user, "password", nil)
	require.NoError(t, err)
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("login_session_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_sessions" {
			tx.AddError(errors.New("private session creation failure"))
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove("login_session_failure") })
	for _, fail := range []bool{true, false} {
		token, challenge := beginSecurityLoginPasskey(t, pending.FlowToken)
		body, err := common.Marshal(map[string]any{"flow_token": pending.FlowToken, "passkey_flow_token": token, "credential": securityPasskeyResponse(t, key, challenge, false, 0)})
		require.NoError(t, err)
		response := securityEnrollmentRequest("POST", "/api/user/login/passkey/finish", string(body), "", service.AuthIdentity{}, LoginPasskeyFinish)
		var result securityEnrollmentResponse
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		assert.Equal(t, !fail, result.Success, response.Body.String())
		if fail {
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.NotContains(t, response.Body.String(), "private")
			_, err = model.GetAuthFlow(pending.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeLoginVerification})
			require.NoError(t, err, "a failed session transaction must leave the parent challenge usable")
			require.NoError(t, model.DB.Callback().Create().Remove("login_session_failure"))
		}
	}
	count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
}

func TestSecurityLoginFactorStateDoesNotAddPasswordLoginQueries(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	newSecurityLoginPasskey(t, user.Id)
	previousPasswordLogin := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() { common.PasswordLoginEnabled = previousPasswordLogin })
	queries := 0
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register("login_query_count", func(tx *gorm.DB) {
		if !tx.DryRun {
			queries++
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove("login_query_count") })
	state, err := model.GetUserVerificationState(user.Id)
	require.NoError(t, err)
	assert.True(t, state.HasPassword)
	assert.True(t, state.HasPasskey)
	assert.False(t, state.HasTwoFA)
	assert.Equal(t, 1, queries, "factor availability must be one database round trip")
	queries = 0
	response := securityEnrollmentRequest("POST", "/api/user/login", `{"username":"enrollment-user","password":"enrollment-password"}`, "", service.AuthIdentity{}, Login)
	assert.Contains(t, response.Body.String(), `"require_verification":true`)
	assert.Equal(t, 2, queries, "only the existing credential lookup and the replacement factor-state lookup run before the challenge")
}

type boundLoginOAuthProvider struct {
	authFlowTestOAuthProvider
	userID int
}

func (*boundLoginOAuthProvider) IsUserIDTaken(string) bool    { return true }
func (*boundLoginOAuthProvider) ProviderUserIDColumn() string { return "github_id" }
func (provider *boundLoginOAuthProvider) FillUserByProviderID(user *model.User, _ string) error {
	return model.DB.First(user, provider.userID).Error
}

func TestSecurityLoginAllPrimaryTransportsRequireAdditionalVerification(t *testing.T) {
	for _, transport := range []string{"oauth", "custom oauth", "wechat", "telegram"} {
		t.Run(transport, func(t *testing.T) {
			var user *model.User
			var telegram *telegramOAuthFixture
			if transport == "telegram" {
				telegram = setupTelegramOAuthTest(t)
				user = telegram.user
			} else {
				user, _ = setupSecurityEnrollmentTest(t)
			}
			newSecurityLoginPasskey(t, user.Id)
			var response *httptest.ResponseRecorder
			switch transport {
			case "telegram":
				require.NoError(t, model.DB.Model(user).Update("telegram_id", "42").Error)
				state, code := telegram.authorization(t, "login", service.AuthIdentity{}, "", telegramIdentityClaims(42))
				response = telegramOAuthCallback(state, code, service.AuthIdentity{})
			case "wechat":
				require.NoError(t, model.DB.Model(user).Update("wechat_id", "bound-wechat").Error)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "wechat-code", r.URL.Query().Get("code"))
					_, _ = w.Write([]byte(`{"success":true,"data":"bound-wechat"}`))
				}))
				t.Cleanup(upstream.Close)
				previousEnabled, previousAddress := common.WeChatAuthEnabled, common.WeChatServerAddress
				common.WeChatAuthEnabled, common.WeChatServerAddress = true, upstream.URL
				t.Cleanup(func() { common.WeChatAuthEnabled, common.WeChatServerAddress = previousEnabled, previousAddress })
				response = securityEnrollmentRequest("GET", "/api/oauth/wechat?code=wechat-code", "", "", service.AuthIdentity{}, WeChatAuth)
			default:
				const slug = "unified-login-test"
				if transport == "custom oauth" {
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/token" {
							_, _ = w.Write([]byte(`{"access_token":"provider-token","token_type":"Bearer"}`))
							return
						}
						_, _ = w.Write([]byte(`{"sub":"bound-custom","name":"User"}`))
					}))
					t.Cleanup(upstream.Close)
					oauth.RegisterCustom(slug, oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
						Id: 42, Slug: slug, Name: "Custom login", Enabled: true, ClientId: "client", ClientSecret: "secret", UserIdField: "sub",
						TokenEndpoint: upstream.URL + "/token", UserInfoEndpoint: upstream.URL + "/userinfo",
					}))
					require.NoError(t, model.DB.Create(&model.UserOAuthBinding{UserId: user.Id, ProviderId: 42, ProviderUserId: "bound-custom"}).Error)
				} else {
					oauth.Register(slug, &boundLoginOAuthProvider{userID: user.Id})
				}
				t.Cleanup(func() { oauth.Unregister(slug) })
				token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeOAuth, Provider: slug, Intent: model.AuthFlowIntentLogin, Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute)})
				require.NoError(t, err)
				router := gin.New()
				router.GET("/api/oauth/:provider", HandleOAuth)
				response = httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("GET", "/api/oauth/"+slug+"?state="+token+"&code=provider-code", nil))
			}
			var result struct {
				Success bool                   `json:"success"`
				Data    service.LoginChallenge `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.True(t, result.Data.RequireVerification)
			assert.Equal(t, []service.VerificationMethodOption{{Method: "passkey", Available: true}}, result.Data.Methods)
			assert.NotEmpty(t, result.Data.FlowToken)
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestSecurityLoginPasskeyCannotCompleteAnotherChallenge(t *testing.T) {
	for _, otherUser := range []bool{false, true} {
		t.Run(fmt.Sprintf("other-user=%t", otherUser), func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			key := newSecurityLoginPasskey(t, user.Id)
			first, err := service.StartLoginVerification(user, "password", nil)
			require.NoError(t, err)
			passkeyToken, challenge := beginSecurityLoginPasskey(t, first.FlowToken)
			if otherUser {
				user = &model.User{Username: "other-login", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "other-login", AuthVersion: 1}
				require.NoError(t, model.DB.Create(user).Error)
				newSecurityLoginPasskey(t, user.Id)
			}
			second, err := service.StartLoginVerification(user, "password", nil)
			require.NoError(t, err)
			body, err := common.Marshal(map[string]any{"flow_token": second.FlowToken, "passkey_flow_token": passkeyToken, "credential": securityPasskeyResponse(t, key, challenge, false, 0)})
			require.NoError(t, err)
			response := securityEnrollmentRequest("POST", "/api/user/login/passkey/finish", string(body), "", service.AuthIdentity{}, LoginPasskeyFinish)
			var result securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success)
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			var count int64
			require.NoError(t, model.DB.Model(&model.UserSession{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestSecurityLoginPasskeyCanManageTwoFAWithScopedProofs(t *testing.T) {
	for _, scope := range []string{service.VerificationScopeTwoFADisable, service.VerificationScopeTwoFABackupCodes} {
		t.Run(scope, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			key := newSecurityLoginPasskey(t, user.Id)
			require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
			hash, err := common.HashBackupCode("ABCD-1234")
			require.NoError(t, err)
			require.NoError(t, model.DB.Create(&model.TwoFABackupCode{UserId: user.Id, CodeHash: hash}).Error)
			if scope == service.VerificationScopeTwoFABackupCodes {
				_, err := service.VerifySecurityInput(identity, service.VerificationInput{Scope: scope, Method: "2fa", Code: "ABCD-1234"})
				assert.ErrorIs(t, err, service.ErrVerificationFailed)
				remaining, err := model.GetUnusedBackupCodeCount(user.Id)
				require.NoError(t, err)
				assert.Equal(t, 1, remaining, "backup codes cannot authorize generating replacement backup codes")
			}
			beginBody, err := common.Marshal(map[string]string{"scope": scope})
			require.NoError(t, err)
			response := securityEnrollmentRequest("POST", "/api/user/passkey/verify/begin", string(beginBody), "", identity, PasskeyVerifyBegin)
			var started struct {
				Success bool `json:"success"`
				Data    struct {
					FlowToken string `json:"flow_token"`
					Options   struct {
						PublicKey struct {
							Challenge        string `json:"challenge"`
							UserVerification string `json:"userVerification"`
						} `json:"publicKey"`
					} `json:"options"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &started))
			require.True(t, started.Success, response.Body.String())
			assert.Equal(t, "required", started.Data.Options.PublicKey.UserVerification)
			finish, err := common.Marshal(passkeyFinishRequest{FlowToken: started.Data.FlowToken, Credential: securityPasskeyResponse(t, key, started.Data.Options.PublicKey.Challenge, false, 0)})
			require.NoError(t, err)
			response = securityEnrollmentRequest("POST", "/api/user/passkey/verify/finish", string(finish), "", identity, PasskeyVerifyFinish)
			var result securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			var proof service.SecurityProof
			require.NoError(t, common.Unmarshal(result.Data, &proof))
			handler, path := Disable2FA, "/api/user/2fa/disable"
			if scope == service.VerificationScopeTwoFABackupCodes {
				handler, path = RegenerateBackupCodes, "/api/user/2fa/backup_codes"
				response = securityEnrollmentRequest("POST", "/api/user/2fa/disable", `{}`, proof.ProofToken, identity, Disable2FA)
				assert.Contains(t, response.Body.String(), "SECURITY_PROOF_SCOPE_MISMATCH")
			}
			response = securityEnrollmentRequest("POST", path, `{}`, proof.ProofToken, identity, handler)
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			stored, err := model.GetUserById(user.Id, false)
			require.NoError(t, err)
			assert.Equal(t, identity.UserAuthVersion+1, stored.AuthVersion)
			_, err = model.GetPasskeyByUserID(user.Id)
			require.NoError(t, err, "managing 2FA must retain the alternative Passkey factor")
			factor, err := model.GetTwoFAByUserId(user.Id)
			require.NoError(t, err)
			if scope == service.VerificationScopeTwoFADisable {
				assert.Nil(t, factor)
			} else {
				require.NotNil(t, factor)
				assert.True(t, factor.IsEnabled)
				assert.Contains(t, string(result.Data), "backup_codes")
			}
			response = securityEnrollmentRequest("POST", path, `{}`, proof.ProofToken, identity, handler)
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success, "the proof cannot authorize a second mutation")
		})
	}
}

func TestSecurityLoginRegisteredPasskeyRequiresUserVerification(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(fmt.Sprintf("verified=%t", verified), func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			require.NoError(t, err)
			proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopePasskeyRegister}, service.VerificationMethodPassword)
			response := securityEnrollmentRequest("POST", "/api/user/passkey/register/begin", "", proof, identity, PasskeyRegisterBegin)
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					FlowToken string `json:"flow_token"`
					Options   struct {
						PublicKey struct {
							Challenge              string `json:"challenge"`
							AuthenticatorSelection struct {
								UserVerification string `json:"userVerification"`
							} `json:"authenticatorSelection"`
						} `json:"publicKey"`
					} `json:"options"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			assert.Equal(t, "required", result.Data.Options.PublicKey.AuthenticatorSelection.UserVerification)
			body, err := common.Marshal(passkeyFinishRequest{FlowToken: result.Data.FlowToken, Credential: securityPasskeyResponse(t, key, result.Data.Options.PublicKey.Challenge, true, 0, verified)})
			require.NoError(t, err)
			response = securityEnrollmentRequest("POST", "/api/user/passkey/register/finish", string(body), "", identity, PasskeyRegisterFinish)
			var finished securityEnrollmentResponse
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &finished))
			assert.Equal(t, verified, finished.Success, response.Body.String())
			_, err = model.GetPasskeyByUserID(user.Id)
			if verified {
				require.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, model.ErrPasskeyNotFound)
			}
		})
	}
}

func TestSecurityLoginRequiresConfiguredFactors(t *testing.T) {
	for _, test := range []struct {
		name             string
		twoFA, passkey   bool
		locked, disabled bool
		methods          []service.VerificationMethodOption
		unavailable      bool
	}{
		{name: "password without additional factors"},
		{name: "passkey requires verification", passkey: true, methods: []service.VerificationMethodOption{{Method: "passkey", Available: true}}},
		{name: "twofa requires verification", twoFA: true, methods: []service.VerificationMethodOption{{Method: "2fa", Available: true}}},
		{name: "both factors are alternatives", twoFA: true, passkey: true, methods: []service.VerificationMethodOption{{Method: "2fa", Available: true}, {Method: "passkey", Available: true}}},
		{name: "locked twofa permits passkey", twoFA: true, passkey: true, locked: true, methods: []service.VerificationMethodOption{{Method: "2fa", Available: false, Reason: service.ErrVerificationLocked.Error()}, {Method: "passkey", Available: true}}},
		{name: "disabled passkey permits twofa", twoFA: true, passkey: true, disabled: true, methods: []service.VerificationMethodOption{{Method: "2fa", Available: true}, {Method: "passkey", Available: false, Reason: "Passkey authentication is disabled."}}},
		{name: "only passkey disabled blocks password", passkey: true, disabled: true, unavailable: true},
		{name: "both factors unavailable block password", passkey: true, twoFA: true, disabled: true, locked: true, unavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			user, _ := setupSecurityEnrollmentTest(t)
			previousPasswordLogin := common.PasswordLoginEnabled
			common.PasswordLoginEnabled = true
			t.Cleanup(func() { common.PasswordLoginEnabled = previousPasswordLogin })
			if test.passkey {
				require.NoError(t, model.DB.Create(&model.PasskeyCredential{UserID: user.Id, CredentialID: "login-key", PublicKey: "public-key"}).Error)
			}
			if test.twoFA {
				factor := &model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
				if test.locked {
					until := time.Now().Add(time.Minute)
					factor.LockedUntil = &until
				}
				require.NoError(t, model.DB.Create(factor).Error)
			}
			system_setting.GetPasskeySettings().Enabled = !test.disabled
			before, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			response := securityEnrollmentRequest(http.MethodPost, "/api/user/login", `{"username":"enrollment-user","password":"enrollment-password"}`, "", service.AuthIdentity{}, Login)
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					RequireVerification bool                               `json:"require_verification"`
					FlowToken           string                             `json:"flow_token"`
					ExpiresAt           int64                              `json:"expires_at"`
					AccessToken         string                             `json:"access_token"`
					Methods             []service.VerificationMethodOption `json:"methods"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			after, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
			require.NoError(t, err)
			if test.unavailable {
				assert.False(t, result.Success)
				assert.Equal(t, before, after)
				assert.Empty(t, response.Header().Values("Set-Cookie"))
				return
			}
			require.True(t, result.Success, response.Body.String())
			if len(test.methods) == 0 {
				assert.False(t, result.Data.RequireVerification)
				assert.NotEmpty(t, result.Data.AccessToken)
				assert.Equal(t, before+1, after)
				return
			}
			assert.True(t, result.Data.RequireVerification)
			assert.NotEmpty(t, result.Data.FlowToken)
			assert.Greater(t, result.Data.ExpiresAt, time.Now().Unix())
			assert.LessOrEqual(t, result.Data.ExpiresAt, time.Now().Add(5*time.Minute).Unix())
			assert.Equal(t, test.methods, result.Data.Methods)
			assert.Empty(t, result.Data.AccessToken)
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.Equal(t, before, after, "a pending challenge must not create a session")
		})
	}
}

type authFlowTestOAuthProvider struct {
	exchangeErr   error
	userInfoErr   error
	exchangeCalls int
	userInfoCalls int
}

func (*authFlowTestOAuthProvider) GetName() string { return "Auth Flow Test" }
func (*authFlowTestOAuthProvider) IsEnabled() bool { return true }
func (provider *authFlowTestOAuthProvider) ExchangeToken(context.Context, string, *gin.Context) (*oauth.OAuthToken, error) {
	provider.exchangeCalls++
	if provider.exchangeErr != nil {
		return nil, provider.exchangeErr
	}
	return &oauth.OAuthToken{}, nil
}
func (provider *authFlowTestOAuthProvider) GetUserInfo(context.Context, *oauth.OAuthToken) (*oauth.OAuthUser, error) {
	provider.userInfoCalls++
	if provider.userInfoErr != nil {
		return nil, provider.userInfoErr
	}
	return &oauth.OAuthUser{ProviderUserID: "external-user"}, nil
}
func (*authFlowTestOAuthProvider) IsUserIDTaken(string) bool                      { return false }
func (*authFlowTestOAuthProvider) FillUserByProviderID(*model.User, string) error { return nil }
func (*authFlowTestOAuthProvider) SetProviderUserID(*model.User, string)          {}
func (*authFlowTestOAuthProvider) GetProviderPrefix() string                      { return "flow_" }
func (*authFlowTestOAuthProvider) ProviderUserIDColumn() string                   { return "" }

func setupAuthFlowControllerTest(t *testing.T) *authFlowTestOAuthProvider {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}, &model.User{}, &model.UserSession{}, &model.AuditLog{}))
	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	provider := &authFlowTestOAuthProvider{}
	oauth.Register("auth-flow-test", provider)
	t.Cleanup(func() {
		oauth.Unregister("auth-flow-test")
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetMainDatabaseType(previousType)
	})
	return provider
}

func setOAuthAllowedOrigins(t *testing.T, value string) {
	t.Helper()
	previous := system_setting.OAuthSettingsSnapshot().AllowedOrigins
	common.OptionMapRWMutex.Lock()
	err := config.GlobalConfig.LoadFromDB(map[string]string{system_setting.OAuthAllowedOriginsOption: value})
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, err)
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{system_setting.OAuthAllowedOriginsOption: previous}))
	})
}

func TestOAuthDomainPanelSettings(t *testing.T) {
	_, identity := setupSecurityEnrollmentTest(t)
	setOAuthAllowedOrigins(t, "")
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	previousSecure, previousTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	common.SessionCookieSecure, common.SessionCookieTrustedURLs = true, nil
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		system_setting.ServerAddress = previousAddress
		common.SessionCookieSecure, common.SessionCookieTrustedURLs = previousSecure, previousTrusted
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	const domains = "https://second.example.com\nhttps://third.example.net:8443"
	body, err := common.Marshal(OptionUpdateRequest{Key: system_setting.OAuthAllowedOriginsOption, Value: domains})
	require.NoError(t, err)
	for range 2 {
		response := securityEnrollmentRequest(http.MethodPut, "/api/option/", string(body), "", identity, UpdateOption)
		require.Contains(t, response.Body.String(), `"success":true`)
	}
	var stored model.Option
	require.NoError(t, model.DB.Where(&model.Option{Key: system_setting.OAuthAllowedOriginsOption}).First(&stored).Error)
	assert.Equal(t, domains, stored.Value)
	assert.Equal(t, domains, system_setting.OAuthSettingsSnapshot().AllowedOrigins)
	// Simulate another node loading the persisted configuration.
	setOAuthAllowedOrigins(t, "")
	require.False(t, system_setting.IsOAuthOriginAllowed("https://second.example.com"))
	common.OptionMapRWMutex.Lock()
	err = config.GlobalConfig.LoadFromDB(map[string]string{stored.Key: stored.Value})
	common.OptionMapRWMutex.Unlock()
	require.NoError(t, err)
	assert.True(t, system_setting.IsOAuthOriginAllowed("https://second.example.com"))
	for _, path := range []string{"/api/user/auth/refresh", "/api/user/auth/logout"} {
		router := gin.New()
		router.POST(path, middleware.SessionCookieOriginGuard(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
		for _, origin := range []string{"https://second.example.com", "https://second.example.com.evil.test"} {
			request := httptest.NewRequest(http.MethodPost, "http://internal:3000"+path, nil)
			request.Header.Set("Origin", origin)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			want := http.StatusForbidden
			if origin == "https://second.example.com" {
				want = http.StatusNoContent
			}
			assert.Equal(t, want, response.Code)
			assert.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	for _, invalid := range []string{"https://*.example.com", "https://second.example.com/path", "http://public.example.com"} {
		body, err := common.Marshal(OptionUpdateRequest{Key: system_setting.OAuthAllowedOriginsOption, Value: invalid})
		require.NoError(t, err)
		response := securityEnrollmentRequest(http.MethodPut, "/api/option/", string(body), "", identity, UpdateOption)
		assert.Contains(t, response.Body.String(), `"success":false`)
		assert.Equal(t, domains, system_setting.OAuthSettingsSnapshot().AllowedOrigins)
		require.NoError(t, model.DB.Where(&model.Option{Key: stored.Key}).First(&stored).Error)
		assert.Equal(t, domains, stored.Value)
	}
	require.NoError(t, model.UpdateOption(system_setting.OAuthAllowedOriginsOption, ""))
	assert.False(t, system_setting.IsOAuthOriginAllowed("https://second.example.com"))
	assert.True(t, system_setting.IsOAuthOriginAllowed("https://main.example.com"))
	// A failed write must not apply the requested list in memory.
	require.NoError(t, model.DB.Migrator().DropTable(&model.Option{}))
	require.Error(t, model.UpdateOption(system_setting.OAuthAllowedOriginsOption, domains))
	assert.Empty(t, system_setting.OAuthSettingsSnapshot().AllowedOrigins)
}

func TestOAuthMultiDomainLogin(t *testing.T) {
	user, _ := setupSecurityEnrollmentTest(t)
	previousAddress := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	setOAuthAllowedOrigins(t, "https://second.example.com,https://third.example.net:8443")
	const slug = "multi-domain-sso"
	var expectedRedirect string
	var tokenCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			if r.ParseForm() != nil || r.Form.Get("redirect_uri") != expectedRedirect {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_redirect_uri"}`))
				return
			}
			assert.Equal(t, "client", r.Form.Get("client_id"))
			assert.Equal(t, "secret", r.Form.Get("client_secret"))
			_, _ = w.Write([]byte(`{"access_token":"test-upstream-token","token_type":"Bearer"}`))
		case "/userinfo":
			assert.Equal(t, "Bearer test-upstream-token", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"sub":"existing-sso-user"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, oauth.RegisterCustom(slug, oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Id: 42, Slug: slug, Name: "Multi domain SSO", Enabled: true,
		ClientId: "client", ClientSecret: "secret", UserIdField: "sub",
		TokenEndpoint: upstream.URL + "/token", UserInfoEndpoint: upstream.URL + "/userinfo",
	})))
	t.Cleanup(func() { oauth.Unregister(slug) })
	require.NoError(t, model.DB.Create(&model.UserOAuthBinding{UserId: user.Id, ProviderId: 42, ProviderUserId: "existing-sso-user"}).Error)
	router := gin.New()
	router.POST("/api/oauth/state", GenerateOAuthCode)
	router.GET("/api/oauth/:provider", HandleOAuth)
	for _, origin := range []string{"https://main.example.com", "https://second.example.com", "https://third.example.net:8443"} {
		t.Run(origin, func(t *testing.T) {
			system_setting.ServerAddress = "https://main.example.com"
			expectedRedirect = origin + "/oauth/" + slug
			request := httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"`+slug+`","intent":"login"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", origin)
			started := httptest.NewRecorder()
			router.ServeHTTP(started, request)
			var start struct {
				Success bool `json:"success"`
				Data    struct {
					FlowToken string `json:"flow_token"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(started.Body.Bytes(), &start))
			require.True(t, start.Success, started.Body.String())
			require.NotEmpty(t, start.Data.FlowToken)
			cookies := started.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.True(t, cookies[0].HttpOnly)
			assert.True(t, cookies[0].Secure)
			assert.Empty(t, cookies[0].Domain)
			assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
			assert.Equal(t, 600, cookies[0].MaxAge)
			assert.Equal(t, "/", cookies[0].Path)
			assert.True(t, strings.HasPrefix(cookies[0].Name, "__Host-"))
			assert.NotContains(t, started.Body.String(), cookies[0].Value)
			pending, err := model.GetAuthFlow(start.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
			require.NoError(t, err)
			assert.NotContains(t, pending.Payload, cookies[0].Value, "the database stores only the browser binding hash")
			// Configuration changes and callback headers/parameters cannot rewrite
			// the redirect URI used when this authorization was started.
			system_setting.ServerAddress = "https://changed.example.com"
			callback := httptest.NewRequest(http.MethodGet, "/api/oauth/"+slug+"?state="+start.Data.FlowToken+"&code=test-code&redirect_uri="+url.QueryEscape("https://attacker.example/oauth/"+slug), nil)
			callback.Host = "internal-proxy:3000"
			callback.Header.Set("X-Forwarded-Host", "attacker.example")
			callback.Header.Set("X-Forwarded-Proto", "http")
			before := tokenCalls
			for _, binding := range []string{"", "another-browser-secret"} {
				unbound := callback.Clone(callback.Context())
				if binding != "" {
					unbound.AddCookie(&http.Cookie{Name: cookies[0].Name, Value: binding})
				}
				denied := httptest.NewRecorder()
				router.ServeHTTP(denied, unbound)
				assert.Equal(t, http.StatusForbidden, denied.Code)
				assert.Equal(t, before, tokenCalls, "reject before contacting the SSO")
			}
			callback.AddCookie(cookies[0])
			duplicate := callback.Clone(callback.Context())
			duplicate.AddCookie(cookies[0])
			ambiguous := httptest.NewRecorder()
			router.ServeHTTP(ambiguous, duplicate)
			assert.Equal(t, http.StatusForbidden, ambiguous.Code)
			assert.Equal(t, before, tokenCalls)
			wrongProvider := callback.Clone(callback.Context())
			wrongProvider.URL.Path = "/api/oauth/oidc"
			denied := httptest.NewRecorder()
			router.ServeHTTP(denied, wrongProvider)
			assert.Equal(t, http.StatusForbidden, denied.Code)
			assert.Equal(t, before, tokenCalls)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, callback)
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					AccessToken string `json:"access_token"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success, response.Body.String())
			require.NotEmpty(t, result.Data.AccessToken)
			identity, err := service.ParseAccessToken(result.Data.AccessToken)
			require.NoError(t, err)
			assert.Equal(t, user.Id, identity.UserID)
			assert.Equal(t, before+1, tokenCalls)
			var cleared bool
			for _, cookie := range response.Result().Cookies() {
				if cookie.Name == cookies[0].Name {
					cleared = cookie.MaxAge == -1
				}
			}
			assert.True(t, cleared, "completed login clears its flow cookie")
			_, err = model.GetAuthFlow(start.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
			assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
			replay := httptest.NewRecorder()
			router.ServeHTTP(replay, callback)
			assert.Equal(t, http.StatusForbidden, replay.Code)
			assert.Equal(t, before+1, tokenCalls)
		})
	}
}

func TestOAuthMultiDomainRejectsUntrustedOrigins(t *testing.T) {
	setupAuthFlowControllerTest(t)
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	setOAuthAllowedOrigins(t, "https://second.example.com")
	require.NoError(t, oauth.RegisterCustom("origin-sso", oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Slug: "origin-sso", Enabled: true})))
	t.Cleanup(func() { oauth.Unregister("origin-sso") })
	for _, origin := range []string{
		"https://unknown.example", "https://second.example.com.evil.test", "https://sub.second.example.com",
		"http://second.example.com", "https://second.example.com:8443", "null", "https://*.example.com",
		"https://second.example.com/path", "https://second.example.com?x=y", "https://second.example.com#fragment", "https://second.example.com?", "https://second.example.com#",
		"https://user@second.example.com", "https://second.example.com,https://main.example.com", "https://second.example.com https://main.example.com",
	} {
		t.Run(origin, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"origin-sso","intent":"login"}`))
			c.Request.Header.Set("Origin", origin)
			c.Request.Host = "second.example.com"
			c.Request.Header.Set("X-Forwarded-Host", "second.example.com")
			GenerateOAuthCode(c)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			assert.Empty(t, response.Result().Cookies())
		})
	}
	t.Run("multiple Origin headers", func(t *testing.T) {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"origin-sso","intent":"login"}`))
		c.Request.Header.Add("Origin", "https://main.example.com")
		c.Request.Header.Add("Origin", "https://second.example.com")
		GenerateOAuthCode(c)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})
	var count int64
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestOAuthMultiDomainFlowFailures(t *testing.T) {
	setupAuthFlowControllerTest(t)
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	setOAuthAllowedOrigins(t, "https://second.example.com")
	var tokenCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "https://second.example.com/oauth/failure-sso", r.Form.Get("redirect_uri"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, oauth.RegisterCustom("failure-sso", oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Slug: "failure-sso", Name: "Failure SSO", Enabled: true, TokenEndpoint: upstream.URL,
	})))
	t.Cleanup(func() { oauth.Unregister("failure-sso") })
	router := gin.New()
	router.POST("/api/oauth/state", GenerateOAuthCode)
	router.GET("/api/oauth/:provider", HandleOAuth)
	var previousCookie *http.Cookie
	for _, scenario := range []string{"token failure", "provider error", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			started := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"failure-sso","intent":"login"}`))
			request.Header.Set("Origin", "https://second.example.com")
			router.ServeHTTP(started, request)
			var start struct {
				Data struct {
					FlowToken string `json:"flow_token"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(started.Body.Bytes(), &start))
			require.NotEmpty(t, start.Data.FlowToken)
			flow, err := model.GetAuthFlow(start.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
			require.NoError(t, err)
			cookies := started.Result().Cookies()
			require.Len(t, cookies, 1)
			callback := httptest.NewRequest(http.MethodGet, "/api/oauth/failure-sso?code=test&state="+start.Data.FlowToken, nil)
			before := tokenCalls
			if previousCookie != nil {
				crossFlow := callback.Clone(callback.Context())
				crossFlow.AddCookie(previousCookie)
				denied := httptest.NewRecorder()
				router.ServeHTTP(denied, crossFlow)
				assert.Equal(t, http.StatusForbidden, denied.Code)
				assert.Equal(t, before, tokenCalls)
			}
			previousCookie = cookies[0]
			callback.AddCookie(cookies[0])
			if scenario == "provider error" {
				callback.URL.RawQuery += "&error=access_denied"
			}
			if scenario == "expired" {
				require.NoError(t, model.DB.Model(flow).Update("expires_at", time.Now().Add(-time.Minute)).Error)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, callback)
			_, err = model.GetAuthFlow(start.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
			switch scenario {
			case "token failure":
				require.NoError(t, err, "upstream failure preserves the flow for retry")
				assert.Equal(t, before+1, tokenCalls)
				assert.Contains(t, response.Body.String(), `"success":false`)
				assert.Empty(t, response.Result().Cookies())
			case "provider error":
				assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
				assert.Equal(t, before, tokenCalls)
				require.Len(t, response.Result().Cookies(), 1)
				assert.Equal(t, -1, response.Result().Cookies()[0].MaxAge)
			case "expired":
				assert.ErrorIs(t, err, model.ErrAuthFlowExpired)
				assert.Equal(t, http.StatusForbidden, response.Code)
				assert.Equal(t, before, tokenCalls)
			}
		})
	}
}

func TestOAuthMultiDomainBindingRequiresOriginalSession(t *testing.T) {
	user, identity := setupSecurityEnrollmentTest(t)
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://main.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	setOAuthAllowedOrigins(t, "https://second.example.com")
	var tokenCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			tokenCalls++
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "https://second.example.com/oauth/bind-sso", r.Form.Get("redirect_uri"))
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"sub":"bound-user"}`))
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, oauth.RegisterCustom("bind-sso", oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{
		Id: 51, Slug: "bind-sso", Name: "Binding SSO", Enabled: true, UserIdField: "sub",
		TokenEndpoint: upstream.URL + "/token", UserInfoEndpoint: upstream.URL + "/userinfo",
	})))
	t.Cleanup(func() { oauth.Unregister("bind-sso") })
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"bind-sso"}`)}, service.VerificationMethodPassword)
	started := securityEnrollmentRequest(http.MethodPost, "/api/oauth/state", `{"provider":"bind-sso","intent":"bind"}`, proof, identity, func(c *gin.Context) {
		c.Request.Header.Set("Origin", "https://second.example.com")
		GenerateOAuthCode(c)
	})
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(started.Body.Bytes(), &result))
	require.True(t, result.Success, started.Body.String())
	path := "/api/oauth/bind-sso?state=" + result.Data.FlowToken + "&code=test"
	handler := func(c *gin.Context) {
		c.Params = gin.Params{{Key: "provider", Value: "bind-sso"}}
		HandleOAuth(c)
	}
	wrongSession := identity
	wrongSession.SessionID = "another-session"
	denied := securityEnrollmentRequest(http.MethodGet, path, "", "", wrongSession, handler)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Zero(t, tokenCalls)
	bound := securityEnrollmentRequest(http.MethodGet, path, "", "", identity, handler)
	require.NoError(t, common.Unmarshal(bound.Body.Bytes(), &result))
	require.True(t, result.Success, bound.Body.String())
	bindings, err := model.GetUserOAuthBindingsByUserId(user.Id)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.Equal(t, "bound-user", bindings[0].ProviderUserId)
	replay := securityEnrollmentRequest(http.MethodGet, path, "", "", identity, handler)
	assert.Equal(t, http.StatusForbidden, replay.Code)
	assert.Equal(t, 1, tokenCalls)
}

func TestOAuthMultiDomainCallbackConfiguration(t *testing.T) {
	previousAddress := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	tests := []struct {
		name, canonical, allowlist, origin, want string
		noOrigin                                 bool
	}{
		{name: "canonical origin", canonical: "https://main.example.com/", origin: "https://main.example.com", want: "https://main.example.com/oauth/test"},
		{name: "default port", canonical: "https://main.example.com", allowlist: "https://SECOND.example.com:443/", origin: "https://second.example.com", want: "https://second.example.com/oauth/test"},
		{name: "canonical fallback", canonical: "https://main.example.com", noOrigin: true, want: "https://main.example.com/oauth/test"},
		{name: "empty Origin", canonical: "https://main.example.com"},
		{name: "allowlist with whitespace", canonical: "https://main.example.com", allowlist: " https://second.example.com , https://third.example.com ", origin: "https://second.example.com", want: "https://second.example.com/oauth/test"},
		{name: "wildcard configuration", canonical: "https://main.example.com", allowlist: "https://*.example.com", origin: "https://main.example.com"},
		{name: "empty allowlist entry", canonical: "https://main.example.com", allowlist: "https://second.example.com,", origin: "https://main.example.com"},
		{name: "query in configuration", canonical: "https://main.example.com", allowlist: "https://second.example.com?", origin: "https://second.example.com"},
		{name: "public HTTP canonical", canonical: "http://main.example.com", origin: "http://main.example.com"},
		{name: "public HTTP additional", canonical: "https://main.example.com", allowlist: "http://second.example.com", origin: "http://second.example.com"},
		{name: "localhost development", canonical: "http://localhost:3000", origin: "http://localhost:3000", want: "http://localhost:3000/oauth/test"},
		{name: "IPv4 loopback development", canonical: "https://main.example.com", allowlist: "http://127.0.0.1:3000", origin: "http://127.0.0.1:3000", want: "http://127.0.0.1:3000/oauth/test"},
		{name: "IPv6 loopback development", canonical: "https://main.example.com", allowlist: "http://[::1]:3000", origin: "http://[::1]:3000", want: "http://[::1]:3000/oauth/test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system_setting.ServerAddress = test.canonical
			setOAuthAllowedOrigins(t, test.allowlist)
			request := httptest.NewRequest(http.MethodPost, "/api/oauth/state", nil)
			if !test.noOrigin {
				request.Header.Set("Origin", test.origin)
			}
			uri, err := oauth.NewBrowserCallbackURI(request, "test")
			if test.want == "" {
				require.Error(t, err)
				assert.Empty(t, uri)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, uri)
		})
	}
}

type oauthCallbackTransport func(*http.Request) (*http.Response, error)

func (transport oauthCallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestOAuthCallbackTokenExchange(t *testing.T) {
	previousTransport, previousAddress := http.DefaultTransport, system_setting.ServerAddress
	previousEndpoint := system_setting.GetOIDCSettings().TokenEndpoint
	system_setting.GetOIDCSettings().TokenEndpoint = "https://sso.example.com/token"
	t.Cleanup(func() { system_setting.GetOIDCSettings().TokenEndpoint = previousEndpoint })
	system_setting.ServerAddress = "https://main.example.com/"
	t.Cleanup(func() { http.DefaultTransport, system_setting.ServerAddress = previousTransport, previousAddress })
	providers := []struct {
		slug     string
		provider oauth.Provider
	}{
		{"custom", oauth.NewGenericOAuthProvider(&model.CustomOAuthProvider{Slug: "custom", TokenEndpoint: "https://sso.example.com/token"})},
		{"oidc", &oauth.OIDCProvider{}},
		{"discord", &oauth.DiscordProvider{}},
	}
	for _, provider := range providers {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%t", provider.slug, legacy), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				expected := "https://main.example.com/oauth/" + provider.slug
				if !legacy {
					expected = "https://second.example.com/oauth/" + provider.slug
					c.Set(oauth.CallbackURIContextKey, expected)
				}
				calls := 0
				http.DefaultTransport = oauthCallbackTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					require.NoError(t, request.ParseForm())
					assert.Equal(t, expected, request.Form.Get("redirect_uri"))
					assert.Equal(t, "test-code", request.Form.Get("code"))
					response := httptest.NewRecorder()
					_, _ = response.WriteString(`{"access_token":"test-access-token"}`)
					return response.Result(), nil
				})
				token, err := provider.provider.ExchangeToken(context.Background(), "test-code", c)
				require.NoError(t, err)
				assert.Equal(t, "test-access-token", token.AccessToken)
				assert.Equal(t, 1, calls)
			})
		}
	}
}

func TestGenerateOAuthCodeCarriesAffiliateInLoginFlow(t *testing.T) {
	setupAuthFlowControllerTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/state", strings.NewReader(`{"provider":"auth-flow-test","intent":"login","aff":"invite-code"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	GenerateOAuthCode(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	flow, err := model.GetAuthFlow(response.Data.FlowToken, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
	})
	require.NoError(t, err)
	var payload oauthFlowPayload
	require.NoError(t, common.UnmarshalJsonStr(flow.Payload, &payload))
	assert.Equal(t, "invite-code", payload.AffiliateCode)
	assert.Zero(t, flow.UserId)
	assert.Empty(t, flow.SessionId)
}

func TestGenerateOAuthCodeBindsFlowToAuthenticatedSession(t *testing.T) {
	_, identity := setupSecurityEnrollmentTest(t)
	oauth.Register("auth-flow-test", &authFlowTestOAuthProvider{})
	t.Cleanup(func() { oauth.Unregister("auth-flow-test") })
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"auth-flow-test"}`)}, service.VerificationMethodPassword)
	response := securityEnrollmentRequest(http.MethodPost, "/api/oauth/state", `{"provider":"auth-flow-test","intent":"bind"}`, proof, identity, GenerateOAuthCode)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	flow, err := model.GetAuthFlow(result.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentBind, UserId: identity.UserID, SessionId: identity.SessionID})
	require.NoError(t, err)
	assert.Equal(t, identity.UserID, flow.UserId)
	assert.Equal(t, identity.SessionID, flow.SessionId)
}

func TestOAuthLoginConsumesFlowOnlyAfterProviderIdentity(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)

	tests := []struct {
		name        string
		exchangeErr error
		userInfoErr error
	}{
		{name: "exchange failure", exchangeErr: errors.New("exchange failed")},
		{name: "user info failure", userInfoErr: errors.New("user info failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider.exchangeErr = test.exchangeErr
			provider.userInfoErr = test.userInfoErr
			token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
				Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
				Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
			})
			require.NoError(t, err)

			router := gin.New()
			router.GET("/api/oauth/:provider", HandleOAuth)
			request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+token+"&code=test", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			flow, err := model.GetAuthFlow(token, model.AuthFlowMatch{
				Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
			})
			require.NoError(t, err)
			assert.Nil(t, flow.ConsumedAt)
		})
	}
}

func TestOAuthLoginConsumesFlowAfterProviderIdentityAndOnProviderError(t *testing.T) {
	provider := setupAuthFlowControllerTest(t)

	provider.exchangeErr = nil
	provider.userInfoErr = nil
	successToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
		Payload: `{invalid`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	router := gin.New()
	router.GET("/api/oauth/:provider", HandleOAuth)
	request := httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+successToken+"&code=test", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	_, err = model.GetAuthFlow(successToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Equal(t, 1, provider.userInfoCalls)

	providerErrorToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeOAuth, Provider: "auth-flow-test", Intent: model.AuthFlowIntentLogin,
		Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	request = httptest.NewRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+providerErrorToken+"&error=access_denied", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	_, err = model.GetAuthFlow(providerErrorToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Equal(t, 1, provider.exchangeCalls)
	assert.Equal(t, 1, provider.userInfoCalls)
}

func TestOAuthBindProviderErrorConsumesSessionBoundFlow(t *testing.T) {
	_, identity := setupSecurityEnrollmentTest(t)
	provider := &authFlowTestOAuthProvider{}
	oauth.Register("auth-flow-test", provider)
	t.Cleanup(func() { oauth.Unregister("auth-flow-test") })
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"auth-flow-test"}`)}, service.VerificationMethodPassword)
	started := securityEnrollmentRequest(http.MethodPost, "/api/oauth/state", `{"provider":"auth-flow-test","intent":"bind"}`, proof, identity, GenerateOAuthCode)
	var result struct {
		Data struct {
			FlowToken string `json:"flow_token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(started.Body.Bytes(), &result))
	require.NotEmpty(t, result.Data.FlowToken)
	response := securityEnrollmentRequest(http.MethodGet, "/api/oauth/auth-flow-test?state="+result.Data.FlowToken+"&error=access_denied&error_description=cancelled", "", "", identity, func(c *gin.Context) {
		c.Params = gin.Params{{Key: "provider", Value: "auth-flow-test"}}
		HandleOAuth(c)
	})
	assert.Equal(t, http.StatusOK, response.Code)
	_, err := model.GetAuthFlow(result.Data.FlowToken, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeOAuth})
	assert.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	assert.Zero(t, provider.exchangeCalls)
	assert.Zero(t, provider.userInfoCalls)
}

// legacyGitHubOAuthProvider follows the GitHub provider contract: the numeric
// account ID identifies the user, Extra["legacy_id"] carries the username and
// the verified email list is served on demand. Lookups go through the real
// github_id model queries.
type legacyGitHubOAuthProvider struct {
	authFlowTestOAuthProvider
	providerUserID     string
	legacyID           string
	verifiedEmails     []string
	verifiedEmailsErr  error
	verifiedEmailCalls int
}

func (*legacyGitHubOAuthProvider) GetName() string { return "GitHub" }
func (provider *legacyGitHubOAuthProvider) GetUserInfo(context.Context, *oauth.OAuthToken) (*oauth.OAuthUser, error) {
	return &oauth.OAuthUser{ProviderUserID: provider.providerUserID, Username: provider.legacyID, Extra: map[string]any{"legacy_id": provider.legacyID}}, nil
}
func (provider *legacyGitHubOAuthProvider) GetVerifiedEmails(context.Context, *oauth.OAuthToken) ([]string, error) {
	provider.verifiedEmailCalls++
	return provider.verifiedEmails, provider.verifiedEmailsErr
}
func (*legacyGitHubOAuthProvider) IsUserIDTaken(providerUserID string) bool {
	return model.IsGitHubIdAlreadyTaken(providerUserID)
}
func (*legacyGitHubOAuthProvider) FillUserByProviderID(user *model.User, providerUserID string) error {
	user.GitHubId = providerUserID
	return user.FillUserByGitHubId()
}
func (*legacyGitHubOAuthProvider) SetProviderUserID(user *model.User, providerUserID string) {
	user.GitHubId = providerUserID
}
func (*legacyGitHubOAuthProvider) GetProviderPrefix() string    { return "github_" }
func (*legacyGitHubOAuthProvider) ProviderUserIDColumn() string { return "github_id" }

// legacyGitHubOAuthLogin registers provider for one test and completes an OAuth
// login callback with it.
func legacyGitHubOAuthLogin(t *testing.T, provider *legacyGitHubOAuthProvider) *httptest.ResponseRecorder {
	t.Helper()
	const slug = "github-legacy-login-test"
	oauth.Register(slug, provider)
	t.Cleanup(func() { oauth.Unregister(slug) })
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeOAuth, Provider: slug, Intent: model.AuthFlowIntentLogin, Payload: `{}`, ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	router := gin.New()
	router.GET("/api/oauth/:provider", HandleOAuth)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/oauth/"+slug+"?state="+token+"&code=provider-code", nil))
	return response
}

// legacyGitHubBindingAudits returns the account binding audit rows in creation
// order together with their encoded parameters.
func legacyGitHubBindingAudits(t *testing.T) ([]model.AuditLog, []string) {
	t.Helper()
	var audits []model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "user.binding_bind").Order("id").Find(&audits).Error)
	params := make([]string, 0, len(audits))
	for _, audit := range audits {
		require.NotNil(t, audit.Other.Op)
		encoded, err := common.Marshal(audit.Other.Op.Params)
		require.NoError(t, err)
		params = append(params, string(encoded))
	}
	return audits, params
}

func TestOAuthLoginLegacyGitHubBindingRequiresAccountEvidence(t *testing.T) {
	const declined = "This GitHub account cannot be linked to an existing account automatically, please sign in or register another way and then link GitHub in account settings"
	tests := []struct {
		name              string
		existingGitHubID  string
		existingDeleted   bool
		withoutEmail      bool
		legacyID          string
		verifiedEmails    []string
		verifiedEmailsErr error
		registerEnabled   bool
		expectLogin       bool
		expectMigration   bool
		expectNewAccount  bool
		expectEmailCalls  int
		expectAuditParams string
	}{
		{name: "numeric legacy value registers a new account", existingGitHubID: "424242", legacyID: "424242", verifiedEmails: []string{"legacy-github@example.com"}, registerEnabled: true, expectLogin: true, expectNewAccount: true},
		{name: "numeric legacy value with registration disabled", existingGitHubID: "424242", legacyID: "424242", verifiedEmails: []string{"legacy-github@example.com"}},
		{name: "soft-deleted legacy row registers a new account", existingGitHubID: "octocat-legacy", existingDeleted: true, legacyID: "octocat-legacy", verifiedEmails: []string{"legacy-github@example.com"}, registerEnabled: true, expectLogin: true, expectNewAccount: true},
		{
			name: "verified email match migrates", existingGitHubID: "octocat-legacy", legacyID: "octocat-legacy",
			verifiedEmails: []string{"other@example.com", " Legacy-GitHub@Example.com "}, expectLogin: true, expectMigration: true, expectEmailCalls: 1,
			// No SMTP server is configured in tests, so the notification attempt is recorded as failed.
			expectAuditParams: `{"provider":"github","legacy_migration":true,"legacy_id":"octocat-legacy","provider_user_id":"900001","verified_email_matched":true,"success":true,"notification_failed":true}`,
		},
		{
			name: "no matching verified email declines", existingGitHubID: "octocat-legacy", legacyID: "octocat-legacy",
			verifiedEmails: []string{"other@example.com"}, registerEnabled: true, expectEmailCalls: 1,
			expectAuditParams: `{"provider":"github","legacy_migration":true,"legacy_id":"octocat-legacy","provider_user_id":"900001","success":false,"reason":"no_matching_evidence"}`,
		},
		{
			name: "verified emails failure declines", existingGitHubID: "octocat-legacy", legacyID: "octocat-legacy",
			verifiedEmailsErr: errors.New("emails unavailable"), registerEnabled: true, expectEmailCalls: 1,
			expectAuditParams: `{"provider":"github","legacy_migration":true,"legacy_id":"octocat-legacy","provider_user_id":"900001","success":false,"reason":"verified_emails_unavailable"}`,
		},
		{
			name: "account without email declines before asking the provider", existingGitHubID: "octocat-legacy", withoutEmail: true, legacyID: "octocat-legacy",
			verifiedEmails: []string{"legacy-github@example.com"}, registerEnabled: true,
			expectAuditParams: `{"provider":"github","legacy_migration":true,"legacy_id":"octocat-legacy","provider_user_id":"900001","success":false,"reason":"no_matching_evidence"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupSecurityEnrollmentTest(t)
			previousRegister := common.RegisterEnabled
			common.RegisterEnabled = test.registerEnabled
			t.Cleanup(func() { common.RegisterEnabled = previousRegister })
			existing := &model.User{Username: "legacy-github", Email: "legacy-github@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "legacy-github", AuthVersion: 1, GitHubId: test.existingGitHubID}
			if test.withoutEmail {
				existing.Email = ""
			}
			require.NoError(t, model.DB.Create(existing).Error)
			if test.existingDeleted {
				require.NoError(t, model.DB.Delete(existing).Error)
			}
			provider := &legacyGitHubOAuthProvider{providerUserID: "900001", legacyID: test.legacyID, verifiedEmails: test.verifiedEmails, verifiedEmailsErr: test.verifiedEmailsErr}
			response := legacyGitHubOAuthLogin(t, provider)
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
				Data    struct {
					User struct {
						Id int `json:"id"`
					} `json:"user"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, test.expectLogin, result.Success, response.Body.String())
			assert.Equal(t, test.expectEmailCalls, provider.verifiedEmailCalls)

			var reloaded model.User
			require.NoError(t, model.DB.Unscoped().First(&reloaded, existing.Id).Error)
			audits, params := legacyGitHubBindingAudits(t)
			if test.expectAuditParams == "" {
				assert.Empty(t, audits)
			} else {
				require.Len(t, audits, 1)
				assert.Equal(t, existing.Id, audits[0].UserId)
				assert.Equal(t, common.RoleCommonUser, audits[0].ActorRole)
				assert.Equal(t, test.expectMigration, audits[0].Success)
				assert.JSONEq(t, test.expectAuditParams, params[0])
			}
			if test.expectMigration {
				assert.Equal(t, existing.Id, result.Data.User.Id)
				assert.Equal(t, "900001", reloaded.GitHubId)
				assert.NotEmpty(t, response.Header().Values("Set-Cookie"))
				return
			}
			assert.Equal(t, test.existingGitHubID, reloaded.GitHubId, "the existing binding must stay untouched")
			if test.expectNewAccount {
				var created model.User
				require.NoError(t, model.DB.Where("github_id = ?", "900001").First(&created).Error)
				assert.NotEqual(t, existing.Id, created.Id)
				assert.Equal(t, created.Id, result.Data.User.Id)
				return
			}
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			if test.expectAuditParams != "" {
				assert.Equal(t, declined, result.Message)
			}
		})
	}
}

func TestOAuthLoginLegacyGitHubBindingMigratesAfterLoginVerification(t *testing.T) {
	for _, interference := range []string{"none", "pending id claimed by another account", "binding relinked meanwhile"} {
		t.Run(interference, func(t *testing.T) {
			setupSecurityEnrollmentTest(t)
			existing := &model.User{Username: "legacy-github", Email: "legacy-github@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "legacy-github", AuthVersion: 1, GitHubId: "octocat-legacy"}
			require.NoError(t, model.DB.Create(existing).Error)
			factor := &model.TwoFA{UserId: existing.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
			require.NoError(t, model.DB.Create(factor).Error)
			provider := &legacyGitHubOAuthProvider{providerUserID: "900001", legacyID: "octocat-legacy", verifiedEmails: []string{"legacy-github@example.com"}}
			response := legacyGitHubOAuthLogin(t, provider)
			var challenge struct {
				Success bool                   `json:"success"`
				Data    service.LoginChallenge `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &challenge))
			require.True(t, challenge.Success, response.Body.String())
			require.True(t, challenge.Data.RequireVerification)
			assert.Equal(t, []service.VerificationMethodOption{{Method: "2fa", Available: true}}, challenge.Data.Methods)
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.Zero(t, provider.verifiedEmailCalls, "a second factor is checked before the provider emails")
			var reloaded model.User
			require.NoError(t, model.DB.First(&reloaded, existing.Id).Error)
			assert.Equal(t, "octocat-legacy", reloaded.GitHubId, "the binding waits for the verification")
			audits, _ := legacyGitHubBindingAudits(t)
			assert.Empty(t, audits)

			expectedGitHubID, expectLogin := "900001", true
			switch interference {
			case "pending id claimed by another account":
				other := &model.User{Username: "other-github", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "other-github", AuthVersion: 1, GitHubId: "900001"}
				require.NoError(t, model.DB.Create(other).Error)
				expectedGitHubID, expectLogin = "octocat-legacy", false
			case "binding relinked meanwhile":
				require.NoError(t, model.DB.Model(existing).Update("github_id", "relinked-legacy").Error)
				expectedGitHubID = "relinked-legacy"
			}
			code, err := totp.GenerateCode(factor.Secret, time.Now())
			require.NoError(t, err)
			body, err := common.Marshal(map[string]string{"flow_token": challenge.Data.FlowToken, "code": code})
			require.NoError(t, err)
			for attempt := range 2 {
				response = securityEnrollmentRequest(http.MethodPost, "/api/user/login/verify", string(body), "", service.AuthIdentity{}, VerifyLogin)
				var result securityEnrollmentResponse
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.Equal(t, attempt == 0 && expectLogin, result.Success, response.Body.String())
				if attempt == 0 && !expectLogin {
					assert.Equal(t, "ACCOUNT_ALREADY_BOUND", result.Code)
				}
				if result.Success {
					assert.NotEmpty(t, response.Header().Values("Set-Cookie"))
				} else {
					assert.Empty(t, response.Header().Values("Set-Cookie"))
				}
			}
			require.NoError(t, model.DB.First(&reloaded, existing.Id).Error)
			assert.Equal(t, expectedGitHubID, reloaded.GitHubId)
			sessions, err := model.CountActiveUserSessions(existing.Id, time.Now().Unix())
			require.NoError(t, err)
			expectedSessions := 0
			if expectLogin {
				expectedSessions = 1
			}
			assert.EqualValues(t, expectedSessions, sessions, "the consumed challenge cannot be replayed")
			audits, params := legacyGitHubBindingAudits(t)
			if interference != "none" {
				assert.Empty(t, audits)
				return
			}
			require.Len(t, audits, 1)
			assert.Equal(t, existing.Id, audits[0].UserId)
			assert.Equal(t, common.RoleCommonUser, audits[0].ActorRole)
			assert.True(t, audits[0].Success)
			// No SMTP server is configured in tests, so the notification attempt is recorded as failed.
			assert.JSONEq(t, `{"provider":"github","legacy_migration":true,"legacy_id":"octocat-legacy","provider_user_id":"900001","verification_method":"2fa","success":true,"notification_failed":true}`, params[0])
		})
	}
}

func TestOAuthBindIgnoresLegacyGitHubUsernames(t *testing.T) {
	tests := []struct {
		name          string
		ownGitHubID   string
		otherGitHubID string
		legacyID      string
		expectBound   bool
	}{
		{name: "another account's legacy username does not block binding", otherGitHubID: "octocat-legacy", legacyID: "octocat-legacy", expectBound: true},
		{name: "own legacy username is replaced", ownGitHubID: "octocat-legacy", otherGitHubID: "unrelated-legacy", legacyID: "octocat-legacy", expectBound: true},
		{name: "numeric legacy value does not collide", otherGitHubID: "424242", legacyID: "424242", expectBound: true},
		{name: "numeric ID held by another account is rejected", otherGitHubID: "900001", legacyID: "octocat-legacy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			if test.ownGitHubID != "" {
				require.NoError(t, model.DB.Model(user).Update("github_id", test.ownGitHubID).Error)
			}
			other := &model.User{Username: "other-github", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "other-github", AuthVersion: 1, GitHubId: test.otherGitHubID}
			require.NoError(t, model.DB.Create(other).Error)
			const slug = "github-legacy-bind-test"
			oauth.Register(slug, &legacyGitHubOAuthProvider{providerUserID: "900001", legacyID: test.legacyID})
			t.Cleanup(func() { oauth.Unregister(slug) })
			proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAccountBind, Context: []byte(`{"provider":"` + slug + `"}`)}, service.VerificationMethodPassword)
			started := securityEnrollmentRequest(http.MethodPost, "/api/oauth/state", `{"provider":"`+slug+`","intent":"bind"}`, proof, identity, GenerateOAuthCode)
			var flow struct {
				Data struct {
					FlowToken string `json:"flow_token"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(started.Body.Bytes(), &flow))
			require.NotEmpty(t, flow.Data.FlowToken, started.Body.String())
			response := securityEnrollmentRequest(http.MethodGet, "/api/oauth/"+slug+"?state="+flow.Data.FlowToken+"&code=provider-code", "", "", identity, func(c *gin.Context) {
				c.Params = gin.Params{{Key: "provider", Value: slug}}
				HandleOAuth(c)
			})
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.Equal(t, test.expectBound, result.Success, response.Body.String())

			var bound, untouched model.User
			require.NoError(t, model.DB.First(&bound, user.Id).Error)
			require.NoError(t, model.DB.First(&untouched, other.Id).Error)
			assert.Equal(t, test.otherGitHubID, untouched.GitHubId)
			if test.expectBound {
				assert.Equal(t, "900001", bound.GitHubId)
				return
			}
			assert.Equal(t, test.ownGitHubID, bound.GitHubId)
			assert.Equal(t, "This GitHub account has already been bound", result.Message)
		})
	}
}

func TestOAuthRegistrationPersistsInviter(t *testing.T) {
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Log{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldRegister := common.RedisEnabled, common.RegisterEnabled
	oldNew, oldInvitee, oldInviter := common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.RegisterEnabled = false, true
	common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = 0, 0, 0
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.RegisterEnabled = oldRedis, oldRegister
		common.QuotaForNewUser, common.QuotaForInvitee, common.QuotaForInviter = oldNew, oldInvitee, oldInviter
	})
	inviter := model.User{Username: "inviter", AffCode: "valid-aff", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	for _, tc := range []struct {
		name, code string
		want       int
	}{
		{"valid", "valid-aff", inviter.Id}, {"missing", "", 0}, {"invalid", "unknown-aff", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			provider := &oauth.GitHubProvider{}
			identity := &oauth.OAuthUser{ProviderUserID: "inviter-test-" + tc.name, Username: "oauth-" + tc.name}
			created, _, err := findOrCreateOAuthUser(ctx, provider, identity, &oauth.OAuthToken{}, tc.code)
			require.NoError(t, err)
			var stored model.User
			require.NoError(t, db.First(&stored, created.Id).Error)
			assert.Equal(t, tc.want, stored.InviterId)
			assert.Equal(t, identity.ProviderUserID, stored.GitHubId)
			existing, _, err := findOrCreateOAuthUser(ctx, provider, identity, &oauth.OAuthToken{}, "valid-aff")
			require.NoError(t, err)
			assert.Equal(t, created.Id, existing.Id)
			require.NoError(t, db.First(&stored, created.Id).Error)
			assert.Equal(t, tc.want, stored.InviterId, "login must not reassign the inviter")
		})
	}
}
