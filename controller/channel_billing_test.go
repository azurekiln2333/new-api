package controller

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDeepSeekBalanceUSD(t *testing.T) {
	tests := []struct {
		name            string
		responseJSON    string
		usdExchangeRate float64
		want            float64
		wantErrContains string
	}{
		{
			name:            "prefers USD when USD precedes CNY",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"12.50"},{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: 7.3,
			want:            12.5,
		},
		{
			name:            "prefers USD when CNY precedes USD",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"},{"currency":"USD","total_balance":"12.50"}]}`,
			usdExchangeRate: 7.3,
			want:            12.5,
		},
		{
			name:            "converts CNY when USD is absent",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: 7.3,
			want:            10,
		},
		{
			name:            "converts CNY when USD balance is zero",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"0.00"},{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: 7.3,
			want:            10,
		},
		{
			name:            "converts CNY when zero USD follows CNY",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"},{"currency":"USD","total_balance":"0.00"}]}`,
			usdExchangeRate: 7.3,
			want:            10,
		},
		{
			name:            "returns zero when USD balance is zero and CNY is absent",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"0.00"}]}`,
			usdExchangeRate: 7.3,
			want:            0,
		},
		{
			name:            "returns error when USD and CNY are absent",
			responseJSON:    `{"balance_infos":[{"currency":"EUR","total_balance":"10.00"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "currency USD or CNY not found",
		},
		{
			name:            "returns USD parse error instead of falling back to CNY",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"invalid"},{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "invalid syntax",
		},
		{
			name:            "rejects NaN USD balance",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"NaN"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "USD balance must be finite",
		},
		{
			name:            "rejects negative USD balance",
			responseJSON:    `{"balance_infos":[{"currency":"USD","total_balance":"-1.00"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "USD balance must be non-negative",
		},
		{
			name:            "rejects positive infinity CNY balance",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"+Inf"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "CNY balance must be finite",
		},
		{
			name:            "rejects negative CNY balance",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"-7.30"}]}`,
			usdExchangeRate: 7.3,
			wantErrContains: "CNY balance must be non-negative",
		},
		{
			name:            "returns error for non-positive CNY exchange rate",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: 0,
			wantErrContains: "USD exchange rate must be greater than zero",
		},
		{
			name:            "rejects NaN CNY exchange rate",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: math.NaN(),
			wantErrContains: "USD exchange rate must be finite",
		},
		{
			name:            "rejects positive infinity CNY exchange rate",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"73.00"}]}`,
			usdExchangeRate: math.Inf(1),
			wantErrContains: "USD exchange rate must be finite",
		},
		{
			name:            "rejects CNY conversion overflow",
			responseJSON:    `{"balance_infos":[{"currency":"CNY","total_balance":"1.7976931348623157e+308"}]}`,
			usdExchangeRate: math.SmallestNonzeroFloat64,
			wantErrContains: "converted USD balance must be finite",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response DeepSeekUsageResponse
			require.NoError(t, common.Unmarshal([]byte(test.responseJSON), &response))

			balance, err := getDeepSeekBalanceUSD(response, test.usdExchangeRate)
			if test.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, test.want, balance, 1e-12)
		})
	}
}

func TestUpdateChannelNewAPIBalanceUsesTheSmallerRemainingQuota(t *testing.T) {
	for _, tc := range []struct {
		name    string
		usage   string
		want    float64
		wantErr string
	}{
		{name: "user quota below the key quota", usage: `{"code":true,"data":{"total_available":2000000,"unlimited_quota":false,"user_quota":{"total_available":500000}}}`, want: 0.5},
		{name: "key quota below the user quota", usage: `{"code":true,"data":{"total_available":500000,"unlimited_quota":false,"user_quota":{"total_available":2000000}}}`, want: 0.5},
		{name: "unlimited key uses the user quota", usage: `{"code":true,"data":{"total_available":0,"unlimited_quota":true,"user_quota":{"total_available":3000000}}}`, want: 3},
		{name: "key quota alone without user quota", usage: `{"code":true,"data":{"total_available":1500000,"unlimited_quota":false}}`, want: 1.5},
		{name: "unlimited key without user quota", usage: `{"code":true,"data":{"total_available":0,"unlimited_quota":true}}`, wantErr: "Allow querying user quota"},
		{name: "upstream rejects the key", usage: `{"success":false,"message":"invalid token"}`, wantErr: "invalid token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupSub2APIBalanceTest(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/usage/token/":
					assert.Equal(t, "Bearer sk-upstream", r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(tc.usage))
				case "/api/status":
					assert.Empty(t, r.Header.Get("Authorization"), "the key is not sent to the public status endpoint")
					_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":1000000}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			baseURL := server.URL + "/"
			channel := &model.Channel{Type: constant.ChannelTypeNewAPI, BaseURL: &baseURL, Key: "sk-upstream", Name: "new-api-upstream"}
			require.NoError(t, model.DB.Create(channel).Error)

			result, err := updateChannelBalance(channel)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tc.want, result.Balance, 1e-9)
			assert.InDelta(t, tc.want, channel.Balance, 1e-9)
		})
	}
}

func TestChannelBalanceManagementClosesRejectedBody(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	t.Cleanup(server.Close)

	_, err := GetResponseBody(http.MethodGet, server.URL, &model.Channel{}, nil)
	require.ErrorContains(t, err, "status code: 503")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the rejected response body remained open")
	}
}

func TestChannelBalanceManagementDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			if dialect.env != "" && os.Getenv(dialect.env) == "" {
				t.Skip("set " + dialect.env + " to run this database")
			}
			db := modelManagementDB(t, dialect.kind, os.Getenv(dialect.env))
			limited := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}

			t.Run("reloads settings and key while keeping one lease across requests", func(t *testing.T) {
				type sentRequest struct{ path, authorization, key, header, host string }
				requests := make(chan sentRequest, 2)
				reading, finish := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- sentRequest{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Key"), r.Header.Get("X-Static"), r.Host}
					switch r.URL.Path {
					case "/api/usage/token/":
						_, _ = io.WriteString(w, `{"code":true,"data":{"total_available":7000000}}`)
					case "/api/status":
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						close(reading)
						select {
						case <-finish:
							_, _ = io.WriteString(w, `{"data":{"quota_per_unit":1000000}}`)
						case <-r.Context().Done():
						}
					default:
						http.NotFound(w, r)
					}
				}))
				t.Cleanup(server.Close)
				channel := &model.Channel{Type: constant.ChannelTypeNewAPI, BaseURL: &server.URL, Key: "sk-stale", Name: t.Name(), Status: common.ChannelStatusManuallyDisabled}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
					"key": "sk-current", "setting": `{"max_concurrent_requests":1}`,
					"header_override": `{"X-Key":"{api_key}","X-Static":"current","Host":"balance.example"}`,
				}).Error)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				var result channelBalanceResult
				var queryErr error
				done := make(chan struct{})
				go func() {
					defer close(done)
					result, queryErr = updateChannelBalanceContext(ctx, channel)
				}()
				t.Cleanup(func() { cancel(); <-done })
				select {
				case <-reading:
				case <-done:
					require.FailNow(t, "balance did not reach the second response body", "%v", queryErr)
				case <-ctx.Done():
					require.FailNow(t, "balance request did not reach upstream")
				}
				lease, apiErr := service.AcquireChannelCapacity(ctx, channel.Id, limited)
				lease.Release()
				require.NotNil(t, apiErr, "the balance operation must hold capacity until its last body closes")
				assert.Equal(t, types.ErrorCodeChannelCapacitySaturated, apiErr.GetErrorCode())
				close(finish)
				select {
				case <-done:
				case <-ctx.Done():
					require.FailNow(t, "balance request did not finish")
				}
				require.NoError(t, queryErr)
				assert.Equal(t, 7.0, result.Balance)
				assert.Equal(t, 7.0, channel.Balance)
				assert.Equal(t, sentRequest{"/api/usage/token/", "Bearer sk-current", "sk-current", "current", "balance.example"}, <-requests)
				assert.Equal(t, sentRequest{"/api/status", "", "sk-current", "current", "balance.example"}, <-requests)
				lease, apiErr = service.AcquireChannelCapacity(ctx, channel.Id, limited)
				require.Nil(t, apiErr)
				lease.Release()
			})

			t.Run("canceling the background run closes the body without disabling", func(t *testing.T) {
				reading, closed := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					close(reading)
					<-r.Context().Done()
					close(closed)
				}))
				t.Cleanup(server.Close)
				setting := `{"max_concurrent_requests":1}`
				channel := &model.Channel{Type: constant.ChannelTypeSub2API, BaseURL: &server.URL, Key: "sk-cancel", Name: t.Name(), Balance: 17, Setting: &setting, Status: common.ChannelStatusEnabled}
				require.NoError(t, db.Create(channel).Error)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				done := make(chan struct{})
				go func() { defer close(done); updateBalanceInRunContext(ctx, channel) }()
				t.Cleanup(func() { cancel(); <-done })
				select {
				case <-reading:
				case <-ctx.Done():
					require.FailNow(t, "background balance did not reach upstream")
				}
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					require.FailNow(t, "background balance ignored cancellation")
				}
				select {
				case <-closed:
				case <-time.After(5 * time.Second):
					require.FailNow(t, "cancellation did not close the upstream connection")
				}
				stored, err := model.GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, 17.0, stored.Balance)
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
				lease, apiErr := service.AcquireChannelCapacity(context.Background(), channel.Id, limited)
				require.Nil(t, apiErr)
				lease.Release()
			})

			t.Run("advanced balance route identity excludes unrelated routes", func(t *testing.T) {
				source := &model.Channel{Id: 1, Type: constant.ChannelTypeAdvancedCustom, Key: "same-key"}
				source.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: "/balance", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "Authorization", Value: "Bearer {api_key}"}},
					{IncomingPath: "/v1/chat/completions", UpstreamPath: "/chat"},
				}}})
				current := *source
				current.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: "/balance", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "Authorization", Value: "Bearer {api_key}"}},
					{IncomingPath: "/v1/chat/completions", UpstreamPath: "/different-chat"},
				}}})
				assert.True(t, service.ChannelBalanceSourceMatches(&current, source))
				current.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: "/different-balance", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "Authorization", Value: "Bearer replacement"}},
				}}})
				assert.False(t, service.ChannelBalanceSourceMatches(&current, source))
			})

			pluginKey := "balance-policy-" + dialect.kind
			plugin, err := jsplugin.DefaultRegistry.Register(fmt.Sprintf(`
export const meta = {apiVersion: 1, key: %q, name: "Balance policy", version: "1.0.0", author: {name: "Test"},
  relay: [{incoming_path: "/v1/chat/completions", upstream_path: "/chat"}]};
export function buildBalanceRequest(ctx) { return {path: "/first", headers: {Authorization: "Bearer " + ctx.apiKey}}; }
export function parseBalance(ctx, response) {
  if (ctx.state === null) return {next: {path: "/second", headers: {Authorization: "Bearer " + ctx.apiKey}}, state: {last: true}};
  return {amount: JSON.parse(response.body).amount, currency: "USD"};
}
`, pluginKey), jsplugin.Options{})
			require.NoError(t, err)
			t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(pluginKey) })

			refreshPluginKey := "balance-refresh-" + dialect.kind
			refreshPlugin, err := jsplugin.DefaultRegistry.Register(fmt.Sprintf(`
export const meta = {apiVersion: 1, key: %q, name: "Balance refresh", version: "1.0.0", author: {name: "Test"},
  relay: [{incoming_path: "/v1/chat/completions", upstream_path: "/chat"}]};
export function buildBalanceRequest(ctx) { return {path: "/balance"}; }
export function parseBalance(ctx, response) {
  if (response.status !== 200) return {error: response.body || "Balance request failed"};
  return {amount: JSON.parse(response.body).amount, currency: "USD"};
}
export function refresh(ctx) {
  return {credential: ctx.credential, headers: {Authorization: "Bearer fresh-" + ctx.credential.account}, query: {token: "fresh-query-" + ctx.credential.account}, accountId: ctx.credential.account};
}
`, refreshPluginKey), jsplugin.Options{})
			require.NoError(t, err)
			t.Cleanup(func() { _ = jsplugin.DefaultRegistry.Unregister(refreshPluginKey) })

			t.Run("successful credential refresh advances only the dispatched balance source", func(t *testing.T) {
				for _, multiKey := range []bool{false, true} {
					t.Run(fmt.Sprintf("multi=%t", multiKey), func(t *testing.T) {
						for _, changed := range []string{"own refresh", "background own refresh", "stored replacement used by retry", "replacement after retry", "same-account replacement after retry", "retry failure", "retry error redacts refreshed credential", "binding after retry", "header after retry", "plugin config after retry", "scope after retry", "key reorder after retry", "disabled during retry"} {
							if !multiKey && (changed == "scope after retry" || changed == "key reorder after retry") {
								continue
							}
							t.Run(changed, func(t *testing.T) {
								first, err := (jsplugin.Credential{
									Plugin: refreshPluginKey, AccountID: "first", Data: []byte(`{"account":"first"}`),
									Headers: map[string]string{"Authorization": "Bearer old-first"},
								}).Line()
								require.NoError(t, err)
								second, err := (jsplugin.Credential{
									Plugin: refreshPluginKey, AccountID: "second", Data: []byte(`{"account":"second"}`),
									Headers: map[string]string{"Authorization": "Bearer old-second"},
								}).Line()
								require.NoError(t, err)
								replacement, err := (jsplugin.Credential{
									Plugin: refreshPluginKey, AccountID: "first", Data: []byte(`{"account":"first"}`),
									Headers: map[string]string{"Authorization": "Bearer replacement-first"},
								}).Line()
								require.NoError(t, err)
								var channel *model.Channel
								var requests atomic.Int32
								server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
									requests.Add(1)
									authorization := r.Header.Get("Authorization")
									if authorization == "Bearer old-first" || authorization == "Bearer old-second" {
										if authorization == "Bearer old-first" && changed == "stored replacement used by retry" {
											key := replacement
											if multiKey {
												key += "\n" + second
											}
											if !assert.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", key).Error) {
												w.WriteHeader(http.StatusInternalServerError)
												return
											}
										}
										w.WriteHeader(http.StatusUnauthorized)
										return
									}
									if authorization != "Bearer fresh-first" && authorization != "Bearer fresh-second" && authorization != "Bearer replacement-first" {
										t.Errorf("unexpected balance credential: %q", authorization)
										w.WriteHeader(http.StatusForbidden)
										return
									}
									if authorization == "Bearer fresh-first" && (changed == "retry failure" || changed == "retry error redacts refreshed credential") {
										w.WriteHeader(http.StatusServiceUnavailable)
										if changed == "retry error redacts refreshed credential" {
											assert.Equal(t, "fresh-query-first", r.URL.Query().Get("token"))
											_, _ = io.WriteString(w, "Balance request failed: Bearer fresh-first fresh-query-first")
										}
										return
									}
									if authorization == "Bearer fresh-first" && changed != "own refresh" && changed != "background own refresh" {
										current, loadErr := model.GetChannelById(channel.Id, true)
										if !assert.NoError(t, loadErr) {
											w.WriteHeader(http.StatusInternalServerError)
											return
										}
										updates := map[string]any{}
										switch changed {
										case "replacement after retry":
											updates["key"] = "administrator-replacement"
										case "same-account replacement after retry":
											updates["key"] = replacement
											if multiKey {
												updates["key"] = replacement + "\n" + second
											}
										case "binding after retry":
											updates["setting"] = `{"task_plugin_key":"replacement-plugin","max_concurrent_requests":1}`
										case "header after retry":
											updates["header_override"] = `{"Authorization":"Bearer replacement"}`
										case "plugin config after retry":
											updates["settings"] = `{"plugin_config":{"tenant":"replacement"}}`
										case "scope after retry":
											current.ChannelInfo.MultiKeyUsers = map[int]int{0: 987}
											updates["channel_info"] = current.ChannelInfo
										case "key reorder after retry":
											lines := current.GetKeys()
											updates["key"] = lines[1] + "\n" + lines[0]
										case "disabled during retry":
											if multiKey {
												current.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
												updates["channel_info"] = current.ChannelInfo
											} else {
												updates["status"] = common.ChannelStatusManuallyDisabled
											}
										}
										if !assert.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error) {
											w.WriteHeader(http.StatusInternalServerError)
											return
										}
									}
									_, _ = io.WriteString(w, `{"amount":0}`)
								}))
								t.Cleanup(server.Close)
								setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, refreshPluginKey)
								channel = &model.Channel{
									Type: constant.ChannelTypeTaskPlugin, BaseURL: &server.URL, Key: first, Name: t.Name(),
									Setting: &setting, Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(1), Balance: 17,
								}
								if multiKey {
									channel.Key += "\n" + second
									channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyUsers: map[int]int{0: 42}, MultiKeyModels: map[int][]string{0: {"model-first"}}}
								}
								require.NoError(t, db.Create(channel).Error)

								if changed == "own refresh" || changed == "stored replacement used by retry" {
									balances, detail, err := updatePluginChannelBalanceContext(context.Background(), channel, refreshPlugin, false)
									require.NoError(t, err)
									require.NotNil(t, detail)
									expected := 1
									if multiKey {
										expected = 2
										require.Len(t, detail.Keys, 2, "refresh of the second line retains the first sample")
									}
									require.Len(t, balances, expected)
									assert.EqualValues(t, expected*2, requests.Load())
									for _, balance := range balances {
										assert.NoError(t, balance.err)
										assert.NotEqual(t, first, balance.key)
										assert.NotEqual(t, second, balance.key)
										if multiKey {
											assert.Equal(t, 42, balance.source.ChannelInfo.MultiKeyUsers[0])
											assert.Equal(t, []string{"model-first"}, balance.source.ChannelInfo.MultiKeyModels[0])
										}
									}
								} else if changed == "retry error redacts refreshed credential" {
									balances, detail, err := updatePluginChannelBalanceContext(context.Background(), channel, refreshPlugin, false)
									require.Error(t, err)
									assert.Nil(t, detail)
									require.Len(t, balances, 1)
									require.Error(t, balances[0].err)
									for _, failure := range []error{err, balances[0].err} {
										assert.Contains(t, failure.Error(), "[REDACTED]")
										assert.NotContains(t, failure.Error(), "Bearer fresh-first")
										assert.NotContains(t, failure.Error(), "fresh-query-first")
									}
								} else {
									updateBalanceInRunContext(context.Background(), channel)
								}
								stored, err := model.GetChannelById(channel.Id, true)
								require.NoError(t, err)
								if changed != "own refresh" && changed != "background own refresh" && changed != "stored replacement used by retry" && changed != "disabled during retry" {
									assert.Equal(t, 17.0, stored.Balance)
									assert.Nil(t, stored.GetBalanceDetail(), "a different source must not receive the successful retry result")
									assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
									assert.EqualValues(t, 2, requests.Load(), "rejected source prevents the next indexed sample")
								} else {
									assert.Zero(t, stored.Balance)
									if changed == "background own refresh" {
										assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status, "zero balance disables the refreshed credential, not the obsolete line")
										if multiKey {
											assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
											assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[1])
										}
									}
									if changed == "disabled during retry" {
										if multiKey {
											assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
										} else {
											assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
										}
									}
								}
							})
						}
					})
				}
			})

			t.Run("completed balances use current identity and automatic-disable policy", func(t *testing.T) {
				for _, provider := range []string{"built-in", "plugin"} {
					for _, cached := range []bool{false, true} {
						type balanceCase struct {
							name           string
							initialAutoBan int
							updates        map[string]any
							wantStatus     int
							wantBalance    float64
						}
						cases := []balanceCase{
							{"ordinary zero balance", 1, nil, common.ChannelStatusAutoDisabled, 0},
							{"automatic disable turned off", 1, map[string]any{"auto_ban": 0}, common.ChannelStatusEnabled, 0},
							{"automatic disable turned on", 0, map[string]any{"auto_ban": 1}, common.ChannelStatusAutoDisabled, 0},
							{"case-only credential replacement", 1, map[string]any{"key": "sk-CURRENT"}, common.ChannelStatusEnabled, 17},
							{"authentication header changed", 1, map[string]any{"header_override": `{"Authorization":"Bearer replacement-auth"}`}, common.ChannelStatusEnabled, 17},
							{"concurrency limit changed", 1, map[string]any{"setting": `{"max_concurrent_requests":2}`}, common.ChannelStatusAutoDisabled, 0},
							{"provider changed", 1, map[string]any{"type": constant.ChannelTypeOpenAI}, common.ChannelStatusEnabled, 17},
							{"upstream changed", 1, map[string]any{"base_url": "https://changed.invalid"}, common.ChannelStatusEnabled, 17},
							{"manually disabled while reading", 1, map[string]any{"status": common.ChannelStatusManuallyDisabled}, common.ChannelStatusManuallyDisabled, 0},
						}
						if provider == "plugin" {
							// A cap change alone preserves the bound plugin identity.
							for i := range cases {
								if cases[i].name == "concurrency limit changed" {
									cases[i].updates["setting"] = fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":2}`, pluginKey)
								}
							}
							cases = append(cases,
								balanceCase{"plugin binding changed", 1, map[string]any{"setting": `{"task_plugin_key":"changed-balance-binding","max_concurrent_requests":1}`}, common.ChannelStatusEnabled, 17},
								balanceCase{"plugin balance config changed", 1, map[string]any{"settings": `{"plugin_config":{"tenant":"replacement"}}`}, common.ChannelStatusEnabled, 17},
							)
						}
						for _, tc := range cases {
							t.Run(fmt.Sprintf("%s/cache=%t/%s", provider, cached, tc.name), func(t *testing.T) {
								var channel *model.Channel
								server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
									if r.URL.Path == "/first" {
										_, _ = io.WriteString(w, `{}`)
										return
									}
									if len(tc.updates) > 0 {
										if err := db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(tc.updates).Error; err != nil {
											t.Errorf("change channel while balance is pending: %v", err)
											w.WriteHeader(http.StatusInternalServerError)
											return
										}
									}
									_, _ = io.WriteString(w, `{"balance":0,"remaining":0,"isValid":true,"amount":0}`)
								}))
								t.Cleanup(server.Close)
								setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, pluginKey)
								channel = &model.Channel{Type: constant.ChannelTypeSub2API, BaseURL: &server.URL, Key: "sk-current", Name: t.Name(), Setting: &setting, Balance: 17, Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(tc.initialAutoBan)}
								if provider == "plugin" {
									channel.Type = constant.ChannelTypeTaskPlugin
								}
								require.NoError(t, db.Create(channel).Error)
								common.MemoryCacheEnabled = cached
								t.Cleanup(func() { common.MemoryCacheEnabled = false })
								if cached {
									model.InitChannelCache()
								}
								updateBalanceInRunContext(context.Background(), channel)
								stored, err := model.GetChannelById(channel.Id, true)
								require.NoError(t, err)
								assert.Equal(t, tc.wantStatus, stored.Status)
								assert.Equal(t, tc.wantBalance, stored.Balance)
								if tc.wantBalance == 17 {
									assert.Nil(t, stored.GetBalanceDetail(), "an obsolete source must not write an amount or detail")
								}
								if cached && tc.wantStatus != common.ChannelStatusManuallyDisabled {
									current, err := model.CacheGetChannel(channel.Id)
									require.NoError(t, err)
									assert.Equal(t, tc.wantStatus, current.Status, "rejected results must not disable the cache either")
								}
							})
						}
					}
				}
			})

			t.Run("plugin results cannot overwrite a changed binding or key index", func(t *testing.T) {
				for _, changed := range []string{"binding before admission", "key before admission", "binding during request", "case-only key during request", "canceled persistence"} {
					t.Run(changed, func(t *testing.T) {
						var channel *model.Channel
						var requests atomic.Int32
						updates := map[string]any{"key": "sk-FIRST\nsk-second"}
						if strings.HasPrefix(changed, "binding") {
							updates = map[string]any{"setting": `{"task_plugin_key":"changed-balance-binding","max_concurrent_requests":1}`}
						}
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							requests.Add(1)
							if r.URL.Path == "/second" && r.Header.Get("Authorization") == "Bearer sk-second" {
								assert.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error)
							}
							_, _ = io.WriteString(w, `{"amount":9}`)
						}))
						t.Cleanup(server.Close)
						setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, pluginKey)
						channel = &model.Channel{Type: constant.ChannelTypeTaskPlugin, BaseURL: &server.URL, Key: "sk-first\nsk-second", Name: t.Name(), Setting: &setting, Status: common.ChannelStatusEnabled, Balance: 17,
							ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
						require.NoError(t, db.Create(channel).Error)
						initial, err := model.UpdateChannelBalanceDetail(channel.Id, func(*model.ChannelBalanceDetail) (*model.ChannelBalanceDetail, *float64) {
							return &model.ChannelBalanceDetail{BalanceResult: jsplugin.BalanceResult{Amount: common.GetPointer(17.0), Currency: "USD"}, Keys: map[string]model.ChannelBalanceDetail{"0": {BalanceResult: jsplugin.BalanceResult{Amount: common.GetPointer(17.0), Currency: "USD"}}}}, nil
						})
						require.NoError(t, err)
						if changed == "canceled persistence" {
							ctx, cancel := context.WithCancel(context.Background())
							cancel()
							_, err = service.UpdatePluginKeyBalancesContext(ctx, channel, map[int]model.ChannelBalanceDetail{0: {BalanceResult: jsplugin.BalanceResult{Amount: common.GetPointer(9.0), Currency: "USD"}}})
							require.ErrorIs(t, err, context.Canceled)
						} else {
							if strings.HasSuffix(changed, "before admission") {
								require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error)
							}
							_, _, err = updatePluginChannelBalanceContext(context.Background(), channel, plugin, false)
							require.Error(t, err)
						}
						stored, err := model.GetChannelById(channel.Id, true)
						require.NoError(t, err)
						assert.Equal(t, 17.0, stored.Balance)
						assert.Equal(t, initial, stored.GetBalanceDetail(), "stale results and policy errors must not overwrite current indexed details")
						if changed == "binding before admission" || changed == "canceled persistence" {
							assert.Zero(t, requests.Load())
						}
					})
				}
			})

			t.Run("background balance preserves a key manually disabled during its request", func(t *testing.T) {
				var channel *model.Channel
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/first" {
						_, _ = io.WriteString(w, `{}`)
						return
					}
					if r.Header.Get("Authorization") == "Bearer sk-first" {
						info := channel.ChannelInfo
						info.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
						assert.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("channel_info", info).Error)
						_, _ = io.WriteString(w, `{"amount":0}`)
						return
					}
					_, _ = io.WriteString(w, `{"amount":9}`)
				}))
				t.Cleanup(server.Close)
				setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, pluginKey)
				channel = &model.Channel{Type: constant.ChannelTypeTaskPlugin, BaseURL: &server.URL, Key: "sk-first\nsk-second", Name: t.Name(), Setting: &setting, Status: common.ChannelStatusEnabled, AutoBan: common.GetPointer(1),
					ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
				require.NoError(t, db.Create(channel).Error)
				updateBalanceInRunContext(context.Background(), channel)
				stored, err := model.GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
				assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
				assert.NotContains(t, stored.ChannelInfo.MultiKeyStatusList, 1)
			})

			t.Run("plugin chain owns one lease and permits explicit disabled key recovery", func(t *testing.T) {
				requests := make(chan string, 2)
				reading, finish := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests <- r.URL.Path + " " + r.Header.Get("Authorization") + " " + r.Header.Get("X-Key")
					if r.URL.Path == "/first" {
						_, _ = io.WriteString(w, `{}`)
						return
					}
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					close(reading)
					select {
					case <-finish:
						_, _ = io.WriteString(w, `{"amount":9}`)
					case <-r.Context().Done():
					}
				}))
				t.Cleanup(server.Close)
				setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, pluginKey)
				headers := `{"X-Key":"{api_key}"}`
				channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin, BaseURL: &server.URL, Key: "sk-enabled\nsk-disabled", Name: t.Name(), Setting: &setting, HeaderOverride: &headers, Status: common.ChannelStatusManuallyDisabled,
					ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}}
				require.NoError(t, db.Create(channel).Error)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				var result jsplugin.BalanceResult
				var queryErr error
				done := make(chan struct{})
				go func() {
					defer close(done)
					result, queryErr = queryPluginKeyBalance(ctx, channel, plugin, 1, "sk-disabled")
				}()
				t.Cleanup(func() { cancel(); <-done })
				select {
				case <-reading:
				case <-done:
					require.FailNow(t, "plugin chain did not reach its second response body", "%v", queryErr)
				case <-ctx.Done():
					require.FailNow(t, "plugin balance did not reach upstream")
				}
				lease, apiErr := service.AcquireChannelCapacity(ctx, channel.Id, limited)
				lease.Release()
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeChannelCapacitySaturated, apiErr.GetErrorCode())
				close(finish)
				select {
				case <-done:
				case <-ctx.Done():
					require.FailNow(t, "plugin balance did not finish")
				}
				require.NoError(t, queryErr)
				require.NotNil(t, result.Amount)
				assert.Equal(t, 9.0, *result.Amount)
				assert.Equal(t, "/first Bearer sk-disabled sk-disabled", <-requests)
				assert.Equal(t, "/second Bearer sk-disabled sk-disabled", <-requests)
				lease, apiErr = service.AcquireChannelCapacity(ctx, channel.Id, limited)
				require.Nil(t, apiErr)
				lease.Release()
			})

			t.Run("revalidation rejects disabled background keys and changed plugin bindings", func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
				t.Cleanup(server.Close)
				for _, changed := range []string{"disabled channel", "disabled key", "replaced key", "plugin binding", "built-in multi-key"} {
					t.Run(changed, func(t *testing.T) {
						setting := fmt.Sprintf(`{"task_plugin_key":%q,"max_concurrent_requests":1}`, pluginKey)
						channel := &model.Channel{Type: constant.ChannelTypeTaskPlugin, BaseURL: &server.URL, Key: "sk-first\nsk-second", Name: t.Name(), Setting: &setting, Status: common.ChannelStatusEnabled,
							ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
						require.NoError(t, db.Create(channel).Error)
						updates := map[string]any{}
						wantErr := ""
						switch changed {
						case "disabled channel":
							updates["status"], wantErr = common.ChannelStatusManuallyDisabled, "channel is no longer enabled"
						case "disabled key":
							info := channel.ChannelInfo
							info.MultiKeyStatusList = map[int]int{1: common.ChannelStatusManuallyDisabled}
							updates["channel_info"], wantErr = info, "key is no longer enabled"
						case "replaced key":
							updates["key"], wantErr = "sk-first\nsk-replacement", "key changed during admission"
						case "plugin binding":
							updates["setting"], wantErr = `{"task_plugin_key":"missing-balance-policy","max_concurrent_requests":1}`, "plugin changed during admission"
						case "built-in multi-key":
							updates["type"], wantErr = constant.ChannelTypeNewAPI, "Multi-key channels do not support balance queries"
						}
						require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error)
						if changed == "built-in multi-key" {
							_, err = updateChannelBalanceContext(context.Background(), channel)
						} else {
							_, _, err = queryPluginKeyBalanceWithPolicy(context.Background(), channel, plugin, 1, "sk-second", true)
						}
						require.ErrorContains(t, err, wantErr)
						lease, apiErr := service.AcquireChannelCapacity(context.Background(), channel.Id, limited)
						require.Nil(t, apiErr, "revalidation must release its grant")
						lease.Release()
					})
				}
				assert.Zero(t, requests.Load())
			})
		})
	}
}
