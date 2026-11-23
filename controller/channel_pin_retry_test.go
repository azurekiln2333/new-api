package controller

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestChannelWaitRouting uses actual HTTP streams: receiving the first comment
// is the barrier proving the request is parked, before releasing a held slot or
// changing channel policy. No recorder is read concurrently with a writer.
func TestChannelWaitRouting(t *testing.T) {
	for _, tc := range []struct {
		name       string
		local      bool
		pinned     bool
		strict     bool
		cancel     bool
		revoke     string
		upstream   string
		two        bool
		wantA      int32
		wantB      int32
		wantCharge int
	}{
		{name: "pinned local capacity waits before billing", local: true, pinned: true, wantA: 1, wantCharge: 1},
		{name: "cancel removes local waiter without billing", local: true, pinned: true, cancel: true},
		{name: "changed group model scope selects next grant", local: true, two: true, revoke: "group", wantB: 1, wantCharge: 1},
		{name: "changed goal policy selects next grant", local: true, two: true, revoke: "goal", wantB: 1, wantCharge: 1},
		{name: "disabled keys select next grant", local: true, two: true, revoke: "key", wantB: 1, wantCharge: 1},
		{name: "unlimited upstream busy waits with zero retry budget", upstream: "busy", wantA: 2, wantCharge: 1},
		{name: "all upstream candidates busy wait with zero retry budget", upstream: "busy", two: true, wantA: -1, wantB: -1, wantCharge: 1},
		{name: "pinned upstream busy stays pinned", upstream: "busy", pinned: true, two: true, wantA: 2, wantCharge: 1},
		{name: "strict upstream busy stays bound", upstream: "busy", strict: true, two: true, wantA: 2, wantCharge: 1},
		{name: "generic 429 does not queue", upstream: "rate_limit", wantA: 1},
		{name: "quota 429 does not queue", upstream: "quota", wantA: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, i18n.Init())
			user, token := setupResponsesWSRequestTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelKeyEntry{}))
			require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
			require.NoError(t, model.DB.Model(user).Update("setting", `{"billing_preference":"wallet_only"}`).Error)
			previousRetries, previousLogs, previousErrors := common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled
			previousCount, previousBatch, previousTimeout := constant.CountToken, common.BatchUpdateEnabled, constant.StreamingTimeout
			previousPrices, previousGroups := ratio_setting.ModelPrice2JSONString(), ratio_setting.GroupRatio2JSONString()
			previousUnits := common.QuotaPerUnit
			quotaSettings := operation_setting.GetQuotaSetting()
			previousTrust := quotaSettings.TrustQuotaUSD
			general := operation_setting.GetGeneralSetting()
			previousPing, previousInterval := general.PingIntervalEnabled, general.PingIntervalSeconds
			affinity := operation_setting.GetChannelAffinitySetting()
			previousAffinity := *affinity
			common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled = 0, true, false
			constant.CountToken, common.BatchUpdateEnabled, constant.StreamingTimeout = false, false, 30
			common.QuotaPerUnit, quotaSettings.TrustQuotaUSD = 500000, 0
			general.PingIntervalEnabled, general.PingIntervalSeconds = true, 1
			*affinity = operation_setting.ChannelAffinitySetting{RetryAvoidFailedChannels: true}
			if tc.strict {
				*affinity = operation_setting.ChannelAffinitySetting{Enabled: true, RetryAvoidFailedChannels: true, DefaultTTLSeconds: 60,
					Rules: []operation_setting.ChannelAffinityRule{{Name: t.Name(), ModelRegex: []string{".*"}, SessionMode: "strict", KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}}}}
			}
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"channel-wait-routing-test":0.000002}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
			t.Setenv("CHANNEL_WAIT_TIMEOUT_SECONDS", "5")
			t.Cleanup(func() {
				common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled = previousRetries, previousLogs, previousErrors
				constant.CountToken, common.BatchUpdateEnabled, constant.StreamingTimeout = previousCount, previousBatch, previousTimeout
				common.QuotaPerUnit, quotaSettings.TrustQuotaUSD = previousUnits, previousTrust
				general.PingIntervalEnabled, general.PingIntervalSeconds = previousPing, previousInterval
				*affinity = previousAffinity
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
				require.NoError(t, model.LOG_DB.Where("token_id = ?", token.Id).Delete(&model.Log{}).Error)
			})
			service.InitHttpClient()
			var calls [2]atomic.Int32
			reserved := make(chan int, 8)
			channels := make([]model.Channel, 1)
			if tc.two {
				channels = make([]model.Channel, 2)
			}
			for i := range channels {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					attempt := calls[i].Add(1)
					quota, err := model.GetUserQuota(user.Id, false)
					assert.NoError(t, err)
					reserved <- quota
					if attempt == 1 && tc.upstream != "" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusTooManyRequests)
						switch tc.upstream {
						case "busy":
							_, _ = io.WriteString(w, `{"error":{"code":"concurrency_limit_exceeded","message":"concurrency limit exceeded for user","type":"rate_limit_error"}}`)
						case "quota":
							_, _ = io.WriteString(w, `{"error":{"code":"insufficient_quota","message":"quota exhausted","type":"insufficient_quota"}}`)
						default:
							_, _ = io.WriteString(w, `{"error":{"code":"rate_limit_exceeded","message":"rate limit exceeded","type":"rate_limit_error"}}`)
						}
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer-%d\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"wait-success\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n", i)
				}))
				t.Cleanup(upstream.Close)
				channels[i] = model.Channel{Id: user.Id*10 + i, Name: fmt.Sprintf("wait-%d", i), Type: constant.ChannelTypeOpenAI, Key: "test-only-channel-wait", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "channel-wait-routing-test", Priority: common.GetPointer(int64(10 - i)), AutoBan: common.GetPointer(0)}
				if tc.local {
					channels[i].SetSetting(kitdto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
				}
				if tc.revoke == "key" && i == 0 {
					channels[i].ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusEnabled}}
				}
				require.NoError(t, channels[i].Insert())
				t.Cleanup(func() {
					require.NoError(t, model.DB.Where("channel_id = ?", channels[i].Id).Delete(&model.ChannelKeyEntry{}).Error)
					require.NoError(t, model.DB.Where("channel_id = ?", channels[i].Id).Delete(&model.Ability{}).Error)
					require.NoError(t, model.DB.Delete(&channels[i]).Error)
				})
			}
			var held []*service.ChannelCapacityLease
			if tc.local {
				for i := range channels {
					lease, apiErr := service.AcquireChannelCapacity(context.Background(), channels[i].Id, channels[i].GetSetting())
					require.Nil(t, apiErr)
					held = append(held, lease)
					t.Cleanup(lease.Release)
				}
			}
			finished := make(chan struct{})
			engine := gin.New()
			engine.Use(func(c *gin.Context) { defer close(finished); c.Next() })
			engine.POST("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
				if tc.pinned {
					service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channels[0].Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
				}
			}, middleware.Distribute(), func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				defer service.ClearCurrentChannelAffinityCache(c)
				Relay(c, types.RelayFormatOpenAIResponses)
			})
			gateway := httptest.NewServer(engine)
			t.Cleanup(gateway.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			body := `{"model":"channel-wait-routing-test","input":"hello","stream":true}`
			if tc.revoke == "goal" {
				body = `{"model":"channel-wait-routing-test","input":"hello","stream":true,"client_metadata":{"x-codex-turn-metadata":"{\"turn_trigger\":\"goal\"}"}}`
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(body))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer sk-"+token.Key)
			request.Header.Set("X-Session", t.Name())
			response, err := gateway.Client().Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			if tc.local {
				line, err := reader.ReadString('\n')
				require.NoError(t, err)
				require.Equal(t, ": keepalive\n", line)
				assert.Zero(t, calls[0].Load(), "waiting must not contact the upstream")
				quota, err := model.GetUserQuota(user.Id, false)
				require.NoError(t, err)
				assert.Equal(t, user.Quota, quota, "initial admission waiting must not reserve quota")
				switch tc.revoke {
				case "group":
					channels[0].SetOtherSettings(kitdto.ChannelOtherSettings{GroupModels: map[string][]string{"default": {"another-model"}}})
					require.NoError(t, model.DB.Model(&channels[0]).Update("settings", channels[0].OtherSettings).Error)
				case "goal":
					settings := channels[0].GetSetting()
					settings.DenyGoal = true
					channels[0].SetSetting(settings)
					require.NoError(t, model.DB.Model(&channels[0]).Update("setting", channels[0].Setting).Error)
				case "key":
					_, err := model.MutateChannelKeyEntries(t.Context(), channels[0].Id, channels[0].ChannelInfo.KeyEntryRevision,
						func(_ *gorm.DB, _ *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
							require.Len(t, entries, 1)
							entries[0].Status = common.ChannelStatusManuallyDisabled
							entries[0].ConfigRevision++
							return entries, nil, nil
						})
					require.NoError(t, err)
					entry, err := model.ChannelKeyEntryByID(channels[0].Id, channels[0].ChannelKeyEntryID(0))
					require.NoError(t, err)
					require.Equal(t, common.ChannelStatusManuallyDisabled, entry.Status)
					stored, err := model.GetChannelById(channels[0].Id, true)
					require.NoError(t, err)
					require.Equal(t, common.ChannelStatusEnabled, stored.Status)
					require.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
				}
				if tc.cancel {
					cancel()
				} else {
					for _, lease := range held {
						lease.Release()
					}
				}
			}
			result, readErr := io.ReadAll(reader)
			if !tc.cancel {
				require.NoError(t, readErr)
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("relay did not finish after response or cancellation")
			}
			if tc.wantA < 0 {
				assert.GreaterOrEqual(t, calls[0].Load(), int32(1))
				assert.GreaterOrEqual(t, calls[1].Load(), int32(1))
				assert.Equal(t, int32(3), calls[0].Load()+calls[1].Load(), "two refusals precede one successful probe")
			} else {
				assert.Equal(t, tc.wantA, calls[0].Load())
				assert.Equal(t, tc.wantB, calls[1].Load())
			}
			if tc.wantCharge > 0 {
				assert.Equal(t, http.StatusOK, response.StatusCode, string(result))
				assert.Equal(t, 1, strings.Count(string(result), "wait-success"), "only one completed response may be delivered")
				assert.NotContains(t, string(result), "concurrency_limit_exceeded")
			} else if !tc.cancel {
				assert.Equal(t, http.StatusTooManyRequests, response.StatusCode, string(result))
				assert.NotContains(t, string(result), ": keepalive")
			}
			close(reserved)
			for quota := range reserved {
				assert.Equal(t, user.Quota-1, quota, "every upstream attempt must share one reservation")
			}
			quota, err := model.GetUserQuota(user.Id, false)
			require.NoError(t, err)
			assert.Equal(t, user.Quota-tc.wantCharge, quota)
			var tokenQuota int
			require.NoError(t, model.DB.Model(token).Select("remain_quota").Scan(&tokenQuota).Error)
			assert.Equal(t, token.RemainQuota-tc.wantCharge, tokenQuota)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", token.Id, model.LogTypeConsume).Find(&logs).Error)
			assert.Len(t, logs, tc.wantCharge, "busy attempts and canceled waits must never settle")
			for i, lease := range held {
				lease.Release()
				probe, apiErr := service.AcquireChannelCapacity(context.Background(), channels[i].Id, channels[i].GetSetting())
				require.Nil(t, apiErr, "completed or canceled wait must release its slot")
				probe.Release()
			}
		})
	}
}

func TestRequestPolicyUpstreamHTTPFailover(t *testing.T) {
	require.NoError(t, i18n.Init())
	user, token := setupResponsesWSRequestTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}))
	previousRetries, previousLogs, previousErrors := common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled
	previousCount, previousBatch := constant.CountToken, common.BatchUpdateEnabled
	previousStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	previousRatios, previousGroups := ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	previousRetryCodes := operation_setting.AutomaticRetryStatusCodesToString()
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinity
	quotaSettings := operation_setting.GetQuotaSetting()
	previousFreePreConsume := quotaSettings.EnableFreeModelPreConsume
	common.LogConsumeEnabled, constant.ErrorLogEnabled, constant.CountToken, common.BatchUpdateEnabled = false, false, false, false
	quotaSettings.EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"upstream-failover-test":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("401-407,500-599"))
	t.Cleanup(func() {
		common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled = previousRetries, previousLogs, previousErrors
		constant.CountToken, common.BatchUpdateEnabled = previousCount, previousBatch
		constant.StreamingTimeout = previousStreamingTimeout
		quotaSettings.EnableFreeModelPreConsume = previousFreePreConsume
		*affinity = previousAffinity
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousRetryCodes))
	})
	service.InitHttpClient()
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		streamBody string
		retries    int
		secondFail bool
		firstByte  bool
		pinned     bool
		strict     bool
		wantStatus int
		wantSecond int32
		wantReason string
	}{
		{name: "structured removed model", status: 404, body: `{"error":{"message":"Model gpt-6-astra is not supported by any configured account in this group","type":"invalid_request_error","code":"model_not_found"}}`, retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "structured disabled upstream group", status: 403, body: `{"error":{"message":"API Key 所属分组已停用","type":"invalid_request_error","code":"group_disabled"}}`, retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "plain removed model", status: 404, body: "Model gpt-6-astra is not supported", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "plain disabled upstream group", status: 403, body: "API Key 所属分组已停用", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "gateway timeout", status: 504, body: "upstream timed out", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "cloudflare timeout", status: 524, body: "a timeout occurred", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "first body byte timeout", status: 200, retries: 2, firstByte: true, wantStatus: 200, wantSecond: 1},
		{name: "exhaustion retains upstream error", status: 403, body: `{"error":{"message":"API Key 所属分组已停用","type":"invalid_request_error"}}`, retries: 4, secondFail: true, wantStatus: 404, wantSecond: 1},
		{name: "saved zero budget", status: 404, body: `{"error":{"message":"model removed","type":"invalid_request_error"}}`, wantStatus: 404, wantReason: "attempt_budget_exhausted"},
		{name: "pinned channel", status: 403, body: `{"error":{"message":"group disabled","type":"invalid_request_error"}}`, retries: 2, pinned: true, wantStatus: 403, wantReason: "pinned_channel"},
		{name: "strict session", status: 404, body: `{"error":{"message":"model removed","type":"invalid_request_error"}}`, retries: 2, strict: true, wantStatus: 404, wantReason: "strict_session"},
		{name: "stream failed before any output", status: 200, streamBody: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_A\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream crashed\"}}}\n\n", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "stream truncated before any output", status: 200, streamBody: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_A\",\"status\":\"in_progress\"}}\n\n", retries: 2, wantStatus: 200, wantSecond: 1},
		{name: "stream failure after output is not replayed", status: 200, streamBody: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_A\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream crashed\"}}}\n\n", retries: 2, wantStatus: 200, wantSecond: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.RetryTimes = tc.retries
			var initialUserQuota, initialTokenQuota int
			reservedQuotas := make(chan int, 2)
			if tc.firstByte || tc.streamBody != "" {
				previousPrices := ratio_setting.ModelPrice2JSONString()
				previousUnits, previousTrust := common.QuotaPerUnit, quotaSettings.TrustQuotaUSD
				common.QuotaPerUnit, quotaSettings.TrustQuotaUSD, common.LogConsumeEnabled = 500000, 0, true
				t.Cleanup(func() {
					require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
					common.QuotaPerUnit, quotaSettings.TrustQuotaUSD, common.LogConsumeEnabled = previousUnits, previousTrust, false
				})
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"upstream-failover-test":0.000002}`))
				require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
				require.NoError(t, model.DB.Model(user).Update("setting", `{"billing_preference":"wallet_only"}`).Error)
				var err error
				initialUserQuota, err = model.GetUserQuota(user.Id, false)
				require.NoError(t, err)
				require.NoError(t, model.DB.Model(token).Select("remain_quota").Scan(&initialTokenQuota).Error)
			}
			*affinity = operation_setting.ChannelAffinitySetting{RetryAvoidFailedChannels: true}
			if tc.strict {
				*affinity = operation_setting.ChannelAffinitySetting{Enabled: true, RetryAvoidFailedChannels: true, DefaultTTLSeconds: 60,
					Rules: []operation_setting.ChannelAffinityRule{{Name: t.Name(), ModelRegex: []string{".*"}, SessionMode: "strict", KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}}}}
			}
			var firstCalls, secondCalls atomic.Int32
			firstCanceled := make(chan struct{}, 5)
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstCalls.Add(1)
				if tc.firstByte || tc.streamBody != "" {
					quota, err := model.GetUserQuota(user.Id, false)
					assert.NoError(t, err)
					reservedQuotas <- quota
				}
				if tc.streamBody != "" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.streamBody)
					w.(http.Flusher).Flush()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.firstByte {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					firstCanceled <- struct{}{}
					return
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(first.Close)
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				secondCalls.Add(1)
				if tc.firstByte || tc.streamBody != "" {
					quota, err := model.GetUserQuota(user.Id, false)
					assert.NoError(t, err)
					reservedQuotas <- quota
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.secondFail {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"error":{"message":"last upstream model removed","type":"invalid_request_error","code":"model_not_found"}}`)
					return
				}
				if tc.streamBody != "" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer from B\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-from-B\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n")
					return
				}
				_, _ = io.WriteString(w, `{"id":"response-from-B","object":"response","status":"completed","model":"upstream-failover-test","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
			}))
			t.Cleanup(second.Close)
			channels := []model.Channel{
				{Name: "A", Type: constant.ChannelTypeOpenAI, Key: "test-only-A", BaseURL: &first.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "upstream-failover-test", Priority: common.GetPointer(int64(10)), AutoBan: common.GetPointer(0)},
				{Name: "B", Type: constant.ChannelTypeOpenAI, Key: "test-only-B", BaseURL: &second.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "upstream-failover-test", Priority: common.GetPointer(int64(5)), AutoBan: common.GetPointer(0)},
			}
			if tc.firstByte {
				channels[0].SetSetting(kitdto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1), UpstreamFirstByteTimeoutSeconds: common.GetPointer(1)})
			}
			for i := range channels {
				require.NoError(t, channels[i].Insert())
				t.Cleanup(func() {
					require.NoError(t, model.DB.Where("channel_id = ?", channels[i].Id).Delete(&model.Ability{}).Error)
					require.NoError(t, model.DB.Delete(&channels[i]).Error)
				})
			}
			var events []service.PolicyEvent
			engine := gin.New()
			engine.POST("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
				if tc.pinned {
					service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channels[0].Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
				}
			}, middleware.Distribute(), func(c *gin.Context) {
				defer common.CleanupBodyStorage(c)
				defer service.ClearCurrentChannelAffinityCache(c)
				Relay(c, types.RelayFormatOpenAIResponses)
				events = service.RequestPolicy(c).Events()
			})
			requestBody := `{"model":"upstream-failover-test","input":"hello"}`
			if tc.streamBody != "" {
				requestBody = `{"model":"upstream-failover-test","input":"hello","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer sk-"+token.Key)
			request.Header.Set("X-Session", t.Name())
			if tc.firstByte {
				ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
				defer cancel()
				request = request.WithContext(ctx)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			assert.Equal(t, int32(1), firstCalls.Load(), "a failed channel is not retried after eligible channels are exhausted")
			assert.Equal(t, tc.wantSecond, secondCalls.Load())
			if tc.wantStatus == http.StatusOK && tc.wantSecond > 0 {
				assert.Contains(t, recorder.Body.String(), "response-from-B")
			}
			if tc.streamBody != "" && tc.wantSecond == 0 {
				assert.Contains(t, recorder.Body.String(), "partial answer", "output already delivered to the client is never replayed")
				assert.NotContains(t, recorder.Body.String(), "answer from B")
			}
			if tc.firstByte {
				select {
				case <-firstCanceled:
				case <-time.After(3 * time.Second):
					t.Fatal("timed-out A was not canceled before the successful B response")
				}
				lease, capacityErr := service.AcquireChannelCapacity(context.Background(), channels[0].Id, channels[0].GetSetting())
				require.Nil(t, capacityErr, "the failed attempt must release its channel slot")
				lease.Release()
				assert.True(t, slices.ContainsFunc(events, func(event service.PolicyEvent) bool { return event.Decision.Reason == "upstream_first_byte_timeout" }))
			}
			if tc.firstByte || tc.streamBody != "" {
				attempts := int(tc.wantSecond) + 1
				require.Len(t, reservedQuotas, attempts)
				for range attempts {
					assert.Equal(t, initialUserQuota-1, <-reservedQuotas, "all attempts share the original one-unit reservation")
				}
				quota, err := model.GetUserQuota(user.Id, false)
				require.NoError(t, err)
				assert.Equal(t, initialUserQuota-1, quota)
				var tokenQuota int
				require.NoError(t, model.DB.Model(token).Select("remain_quota").Scan(&tokenQuota).Error)
				assert.Equal(t, initialTokenQuota-1, tokenQuota)
				var logs []model.Log
				require.NoError(t, model.LOG_DB.Where("token_id = ? AND channel_id IN ? AND type = ?", token.Id, []int{channels[0].Id, channels[1].Id}, model.LogTypeConsume).Find(&logs).Error)
				require.Len(t, logs, 1, "only the productive attempt settles, including a failed partial response")
				settledChannel := channels[0].Id
				if tc.wantSecond > 0 {
					settledChannel = channels[1].Id
				}
				assert.Equal(t, settledChannel, logs[0].ChannelId)
				assert.Equal(t, 1, logs[0].Quota)
			}
			if tc.secondFail {
				assert.Contains(t, recorder.Body.String(), "last upstream model removed")
				assert.NotContains(t, recorder.Body.String(), "get_channel_failed")
			}
			if tc.wantReason != "" {
				assert.True(t, slices.ContainsFunc(events, func(event service.PolicyEvent) bool { return event.Decision.Reason == tc.wantReason }), "stop reason: %s", tc.wantReason)
			}
		})
	}
}

func TestRequestPolicyParsedUpstreamErrorStopBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*gin.Context, *types.NewAPIError)
		want  string
	}{
		{name: "caller canceled", setup: func(c *gin.Context, _ *types.NewAPIError) {
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(ctx)
		}, want: "client_cancelled"},
		{name: "response started", setup: func(c *gin.Context, _ *types.NewAPIError) {
			_, err := c.Writer.WriteString("data: started\n\n")
			require.NoError(t, err)
		}, want: "response_started"},
		{name: "local forbidden is not an upstream failure", setup: func(_ *gin.Context, apiErr *types.NewAPIError) {
			*apiErr = *types.NewErrorWithStatusCode(errors.New("local group disabled"), types.ErrorCodeInvalidRequest, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}, want: "non_retryable_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newPinRetryContext()
			apiErr := service.RelayErrorHandler(c.Request.Context(), &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"API Key 所属分组已停用","type":"invalid_request_error"}}`))}, false)
			require.NotNil(t, apiErr)
			tc.setup(c, apiErr)
			decision := service.DecideRelayRetry(c, apiErr, 2)
			assert.Equal(t, "stop", decision.Action)
			assert.Equal(t, tc.want, decision.Reason)
			if tc.want == "non_retryable_error" {
				assert.False(t, service.IsChannelFailure(apiErr))
			}
		})
	}
}

func channelRoutingTestDB(t *testing.T, kind, dsn string) *gorm.DB {
	t.Helper()
	db, _ := newAuditTestDatabase(t, kind, dsn)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis, previousMemory := common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled
	previousGroups := ratio_setting.GroupRatio2JSONString()
	t.Setenv("LOG_SQL_DSN", "")
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseType(kind))
	common.IsMasterNode, common.RedisEnabled = false, false
	// Initialize dialect quoting without opening a second SQLite connection.
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = previousMaster
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		model.DB = previousDB
		common.SetMainDatabaseType(previousMain)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		model.LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousLog)
		common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = previousMaster, previousRedis, previousMemory
		if previousDB != nil && previousMemory {
			model.InitChannelCache()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}))
	return db
}

func TestShouldRetryHonorsPinRetryMode(t *testing.T) {
	openaiErr := types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	c := newPinRetryContext()
	assert.True(t, service.ShouldRetryRelayError(c, openaiErr, 1))

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.True(t, service.ShouldRetryRelayError(origin, openaiErr, 1), "origin pin retries on the same channel")

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}, service.DecideRelayRetry(token, openaiErr, 1), "token pin suppresses retry")
}

func TestShouldRetryTaskRelayHonorsPinRetryMode(t *testing.T) {
	taskErr := &dto.TaskError{StatusCode: http.StatusInternalServerError}

	c := newPinRetryContext()
	assert.Equal(t, "retry", decideTaskRetry(c, taskErr, 1).Action)

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.Equal(t, "retry", decideTaskRetry(origin, taskErr, 1).Action)

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.Equal(t, service.PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}, decideTaskRetry(token, taskErr, 1))
}

func TestSameChannelPinsMergeToStricterRetryMode(t *testing.T) {
	c := newPinRetryContext()
	constraints := service.GetChannelConstraints(c)
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	pin, found, overridden := constraints.ResolvedPin()
	require.True(t, found)
	assert.Equal(t, 7, pin.ChannelId)
	assert.Equal(t, dto.PinRetrySingleAttempt, pin.RetryMode)
	assert.Empty(t, overridden)
	assert.False(t, service.ShouldRetryRelayError(c, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), 1))
}

func newPinRetryContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestRequestPolicyConfigReturnsSettingsWithoutMigration(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetRequestPolicy(ctx)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Contains(t, response.Data, "options")
	assert.NotContains(t, response.Data, "migration")
	assert.NotContains(t, response.Data, "differences")
}

func TestChannelAffinityRetryPriorities(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := ""
			if dialect.env != "" {
				dsn = os.Getenv(dialect.env)
				if dsn == "" {
					t.Skipf("%s not configured", dialect.env)
				}
			}
			db := channelRoutingTestDB(t, dialect.kind, dsn)
			previousRetryTimes := common.RetryTimes
			previousGroups := setting.UserUsableGroups2JSONString()
			previousMaxGroups := setting.GetMaxTokenAutoGroups()
			affinity := operation_setting.GetChannelAffinitySetting()
			previousAffinity := *affinity
			t.Cleanup(func() {
				common.RetryTimes = previousRetryTimes
				*affinity = previousAffinity
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
				require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprint(previousMaxGroups)))
			})
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","empty":"Empty"}`))
			require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"empty":1}`))
			const modelName = "affinity-retry-test"
			for _, spec := range []struct {
				id       int
				priority int64
				group    string
			}{{24, 4, "default"}, {27, 3, "default"}, {30, 2, "default"}, {34, 4, "vip"}, {37, 3, "vip"}} {
				channel := model.Channel{
					Id: spec.id, Name: fmt.Sprintf("channel-%d", spec.id), Type: constant.ChannelTypeOpenAI,
					Key: "test-only", Status: common.ChannelStatusEnabled, Models: modelName, Group: spec.group,
					Priority: common.GetPointer(spec.priority), Weight: common.GetPointer(uint(1)),
				}
				require.NoError(t, db.Create(&channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			for _, cached := range []bool{false, true} {
				common.MemoryCacheEnabled = cached
				model.InitChannelCache()
				for _, tc := range []struct {
					name, mode string
					binding    int
					retries    int
					autoGroups []string
					crossGroup bool
					want       []int
				}{
					{name: "ordinary routing", mode: "off", retries: 2, want: []int{24, 27, 30}},
					{name: "affinity miss", mode: "prefer", retries: 2, want: []int{24, 27, 30}},
					{name: "affinity hit", mode: "prefer", binding: 27, retries: 2, want: []int{27, 24, 27}},
					{name: "no retry budget", mode: "prefer", binding: 27, want: []int{27}},
					{name: "one retry", mode: "prefer", binding: 27, retries: 1, want: []int{27, 24}},
					{name: "strict session", mode: "strict", binding: 27, retries: 2, want: []int{27}},
					{name: "auto group", mode: "prefer", binding: 27, retries: 1, autoGroups: []string{"default", "vip"}, want: []int{27, 24}},
					{name: "cross group", mode: "prefer", binding: 27, retries: 1, autoGroups: []string{"default", "vip"}, crossGroup: true, want: []int{27, 24, 34, 37}},
					{name: "skip empty group", mode: "prefer", binding: 27, retries: 1, autoGroups: []string{"empty", "default"}, want: []int{27, 24, 27}},
				} {
					t.Run(fmt.Sprintf("cache=%t/%s", cached, tc.name), func(t *testing.T) {
						common.RetryTimes = tc.retries
						*affinity = operation_setting.ChannelAffinitySetting{
							Enabled: true, DefaultTTLSeconds: 60, MaxEntries: 100,
							Rules: []operation_setting.ChannelAffinityRule{{
								Name: t.Name(), ModelRegex: []string{"^" + modelName + "$"}, SessionMode: tc.mode,
								KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}},
							}},
						}
						ctx := newPinRetryContext()
						ctx.Request.Header.Set("X-Session", t.Name())
						group := "default"
						common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
						if tc.autoGroups != nil {
							group = "auto"
							common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, tc.autoGroups)
							common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, tc.crossGroup)
						}
						if tc.binding != 0 {
							_, found := service.GetPreferredChannelByAffinity(ctx, modelName, group)
							require.False(t, found)
							service.RecordChannelAffinity(ctx, tc.binding)
							t.Cleanup(func() { service.ClearCurrentChannelAffinityCache(ctx) })
						}
						// HTTP distribution and the relay loop use separate RetryParam values.
						channel, _, selectionErr := service.SelectChannelForRequest(ctx, modelName, &service.RetryParam{Ctx: ctx, TokenGroup: group, ModelName: modelName})
						require.Nil(t, selectionErr)
						require.NotNil(t, channel)
						retry := &service.RetryParam{Ctx: ctx, TokenGroup: group, ModelName: modelName}
						upstreamErr := types.NewOpenAIError(errors.New("upstream failure"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
						var attempts []int
						for ; retry.GetRetry() <= common.RetryTimes; retry.IncreaseRetry() {
							if len(attempts) > 0 {
								var err error
								channel, _, err = service.CacheGetRandomSatisfiedChannel(retry)
								require.NoError(t, err)
								if channel == nil {
									break
								}
							}
							service.AppendUsedChannel(ctx, channel.Id)
							attempts = append(attempts, channel.Id)
							require.LessOrEqual(t, len(attempts), len(tc.want), "retry budget must not increase")
							if service.DecideRelayRetry(ctx, upstreamErr, common.RetryTimes-retry.GetRetry()).Action != "retry" {
								break
							}
						}
						assert.Equal(t, tc.want, attempts)
					})
				}
			}
		})
	}
}

func TestRequestPolicyRoutingDatabaseMatrix(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := ""
			if dialect.env != "" {
				dsn = os.Getenv(dialect.env)
				if dsn == "" {
					t.Skipf("%s not configured", dialect.env)
				}
			}
			db := channelRoutingTestDB(t, dialect.kind, dsn)
			previousGroups := setting.UserUsableGroups2JSONString()
			previousRatios, err := common.Marshal(ratio_setting.GetGroupRatioCopy())
			require.NoError(t, err)
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
			t.Cleanup(func() {
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(previousRatios)))
			})
			// Channel 1 holds the session binding but is no longer usable.
			channels := []model.Channel{
				{Id: 1, Name: "A", Type: 1, Key: "test-only", Status: common.ChannelStatusAutoDisabled, Models: "policy-test", Group: "default", Priority: common.GetPointer(int64(10))},
				{Id: 2, Name: "B", Type: 1, Key: "test-only", Status: common.ChannelStatusEnabled, Models: "policy-test", Group: "default", Priority: common.GetPointer(int64(5))},
			}
			for i := range channels {
				require.NoError(t, db.Create(&channels[i]).Error)
				require.NoError(t, channels[i].AddAbilities(db))
			}
			affinity := operation_setting.GetChannelAffinitySetting()
			previousAffinity := *affinity
			t.Cleanup(func() { *affinity = previousAffinity })
			for _, cached := range []bool{false, true} {
				for _, keep := range []bool{false, true} {
					for _, tc := range []struct {
						globalMode, ruleMode string
						blocked              bool
					}{
						{"strict", "inherit", true},
						{"prefer", "strict", true},
						{"prefer", "inherit", false},
						{"strict", "prefer", false},
					} {
						t.Run(fmt.Sprintf("cache=%t/keep=%t/global=%s/rule=%s", cached, keep, tc.globalMode, tc.ruleMode), func(t *testing.T) {
							common.MemoryCacheEnabled = cached
							model.InitChannelCache()
							snapshot, err := model.BuildRequestPolicy(map[string]string{
								"channel_affinity_setting.enabled":                  "true",
								"channel_affinity_setting.session_mode":             tc.globalMode,
								"channel_affinity_setting.keep_on_channel_disabled": fmt.Sprint(keep),
								"channel_affinity_setting.rules":                    fmt.Sprintf(`[{"name":"session","model_regex":[".*"],"key_sources":[{"type":"request_header","key":"X-Session"}],"session_mode":%q}]`, tc.ruleMode),
							})
							require.NoError(t, err)
							*affinity = snapshot.Affinity
							seed := newPinRetryContext()
							seed.Request.Header.Set("X-Session", t.Name())
							_, found := service.GetPreferredChannelByAffinity(seed, "policy-test", "default")
							require.False(t, found)
							seed.Set("channel_id", 1)
							service.RecordChannelAffinity(seed, 1)
							t.Cleanup(func() { service.ClearCurrentChannelAffinityCache(seed) })
							bound, found := service.GetPreferredChannelByAffinity(seed, "policy-test", "default")
							require.True(t, found)
							require.Equal(t, 1, bound)

							request := newPinRetryContext()
							request.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"policy-test"}`))
							request.Request.Header.Set("Content-Type", "application/json")
							request.Request.Header.Set("X-Session", t.Name())
							common.SetContextKey(request, constant.ContextKeyUsingGroup, "default")
							middleware.Distribute()(request)
							events := service.RequestPolicy(request).Events()
							assert.True(t, slices.ContainsFunc(events, func(event service.PolicyEvent) bool { return event.Decision.Reason == "session_rule_matched" }), "decision events are recorded for every request")
							bound, found = service.GetPreferredChannelByAffinity(seed, "policy-test", "default")
							if tc.blocked {
								assert.Equal(t, http.StatusServiceUnavailable, request.Writer.Status())
								assert.True(t, request.IsAborted())
								assert.Equal(t, keep, found, "binding retention is independent of strict request handling")
								if found {
									assert.Equal(t, 1, bound)
								}
								return
							}
							assert.False(t, request.IsAborted())
							assert.Equal(t, 2, common.GetContextKeyInt(request, constant.ContextKeyChannelId), "prefer falls back to the next eligible channel")
							require.True(t, found)
							assert.Equal(t, 2, bound, "a successful fallback rebinds the session")
						})
					}
				}
			}
		})
	}
}

func TestAffinityFailoverDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct{ kind, env string }{{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}} {
		t.Run(dialect.kind, func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			db := channelRoutingTestDB(t, dialect.kind, dsn)
			affinity := operation_setting.GetChannelAffinitySetting()
			saved := *affinity
			previousCache := common.MemoryCacheEnabled
			t.Cleanup(func() { *affinity = saved; common.MemoryCacheEnabled = previousCache })
			for _, spec := range []struct {
				id       int
				priority int64
			}{{81, 10}, {82, 10}, {83, 5}} {
				channel := model.Channel{Id: spec.id, Name: fmt.Sprint(spec.id), Type: constant.ChannelTypeOpenAI, Key: "test-only", Status: common.ChannelStatusEnabled, Models: "failover", Group: "default", Priority: common.GetPointer(spec.priority), Weight: common.GetPointer(uint(1)), Setting: common.GetPointer(`{"responses_websocket_enabled":true}`)}
				require.NoError(t, db.Create(&channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			for _, cached := range []bool{false, true} {
				for _, path := range []string{"http", "task", "websocket"} {
					t.Run(fmt.Sprintf("cached=%t/%s", cached, path), func(t *testing.T) {
						common.MemoryCacheEnabled = cached
						model.InitChannelCache()
						*affinity = operation_setting.ChannelAffinitySetting{Enabled: true, SwitchOnSuccess: true, FailureThreshold: 1, RetryAvoidFailedChannels: true, DefaultTTLSeconds: 60, Rules: []operation_setting.ChannelAffinityRule{{Name: t.Name(), ModelRegex: []string{".*"}, SessionMode: "strict", KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}}}}
						seed := newPinRetryContext()
						seed.Request.Header.Set("X-Session", t.Name())
						service.GetPreferredChannelByAffinity(seed, "failover", "default")
						service.RecordChannelAffinity(seed, 81)
						t.Cleanup(func() { service.ClearCurrentChannelAffinityCache(seed) })
						ctx := newPinRetryContext()
						ctx.Request.Header.Set("X-Session", t.Name())
						if path == "websocket" {
							service.GetChannelConstraints(ctx).AddFilter(dto.ChannelFilter{Kind: dto.FilterResponsesWebSocket})
						}
						retry := &service.RetryParam{Ctx: ctx, TokenGroup: "default", ModelName: "failover"}
						channel, _, selectErr := service.SelectChannelForRequest(ctx, "failover", retry)
						require.Nil(t, selectErr)
						require.Equal(t, 81, channel.Id)
						state := service.RequestPolicy(ctx)
						state.BeginAttempt(channel, "default")
						upstream := types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, 500)
						var decision service.PolicyDecision
						if path == "task" {
							decision = decideTaskRetry(ctx, &dto.TaskError{StatusCode: 500}, 2)
						} else {
							decision = service.DecideRelayRetry(ctx, upstream, 2)
						}
						require.Equal(t, "retry", decision.Action)
						retry.IncreaseRetry()
						channel, _, err := service.CacheGetRandomSatisfiedChannel(retry)
						require.NoError(t, err)
						require.NotNil(t, channel)
						assert.Equal(t, 82, channel.Id, "same-priority peer precedes lower priority and failed binding")
						state.BeginAttempt(channel, "default")
						ctx.Set("channel_id", 82)
						service.MarkRequestPolicySuccess(ctx, nil)
						service.RecordChannelAffinity(ctx, 81)
						next := newPinRetryContext()
						next.Request.Header.Set("X-Session", t.Name())
						bound, found := service.GetPreferredChannelByAffinity(next, "failover", "default")
						require.True(t, found)
						assert.Equal(t, 82, bound)
						assert.True(t, slices.ContainsFunc(state.Events(), func(e service.PolicyEvent) bool { return e.Decision.Reason == "session_failure_threshold" }))
						assert.True(t, slices.ContainsFunc(state.Events(), func(e service.PolicyEvent) bool { return e.Decision.Reason == "avoid_failed_channels" }))
						state.BeginAttempt(channel, "default")
						service.RecordChannelFailure(ctx)
						retry.IncreaseRetry()
						channel, _, err = service.CacheGetRandomSatisfiedChannel(retry)
						require.NoError(t, err)
						require.NotNil(t, channel)
						assert.Equal(t, 83, channel.Id, "all untried peers precede repeats")
						state.BeginAttempt(channel, "default")
						service.RecordChannelFailure(ctx)
						retry.IncreaseRetry()
						channel, _, err = service.CacheGetRandomSatisfiedChannel(retry)
						require.NoError(t, err)
						assert.Nil(t, channel, "avoidance stops after every eligible channel has failed")
					})
				}
			}
		})
	}
}

// TestGoalTurnPolicyRouting proves the channel goal policy on the real routing
// path: the constraint is derived from the request, applied by the ordinary
// selection (with the channel memory cache off and on), and every refused
// request is rejected locally without an upstream call. Only the recognized
// structured turn signal may exclude a channel, and a retry after an upstream
// failure must keep respecting that eligibility.
func TestGoalTurnPolicyRouting(t *testing.T) {
	require.NoError(t, i18n.Init())
	_, token := setupResponsesWSRequestTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.UserSubscription{}))
	previousRetries, previousLogs, previousErrors := common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled
	previousCount, previousBatch := constant.CountToken, common.BatchUpdateEnabled
	previousRatios, previousGroups := ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	previousRetryCodes := operation_setting.AutomaticRetryStatusCodesToString()
	affinity := operation_setting.GetChannelAffinitySetting()
	previousAffinity := *affinity
	previousCached := common.MemoryCacheEnabled
	common.LogConsumeEnabled, constant.ErrorLogEnabled, constant.CountToken, common.BatchUpdateEnabled = false, false, false, false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"goal-policy-routing-test":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString("401-407,500-599"))
	t.Cleanup(func() {
		common.RetryTimes, common.LogConsumeEnabled, constant.ErrorLogEnabled = previousRetries, previousLogs, previousErrors
		constant.CountToken, common.BatchUpdateEnabled = previousCount, previousBatch
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, operation_setting.AutomaticRetryStatusCodesFromString(previousRetryCodes))
		*affinity = previousAffinity
		common.MemoryCacheEnabled = previousCached
		model.InitChannelCache()
	})
	service.InitHttpClient()

	const goalMetadata = `"{\"turn_trigger\":\"goal\"}"`
	goalBody := `{"model":"goal-policy-routing-test","input":"hello","client_metadata":{"x-codex-turn-metadata":` + goalMetadata + `}}`
	// Goal tools and prose appear in ordinary traffic, so they must not trigger
	// the policy on their own.
	ordinaryBody := `{"model":"goal-policy-routing-test","input":[{"type":"message","role":"user","content":"/goal ship it"}],"tools":[{"type":"function","name":"create_goal","parameters":{}}]}`

	cases := []struct {
		name         string
		body         string
		pinned       bool
		firstDenies  bool
		secondDenies bool
		secondFails  bool
		retries      int
		wantStatus   int
		wantACalls   int32
		wantBCalls   int32
	}{
		{name: "pinned channel that refuses goal turns is rejected locally", body: goalBody, pinned: true, firstDenies: true, wantStatus: http.StatusBadRequest},
		{name: "every candidate refusing goal turns is rejected locally", body: goalBody, firstDenies: true, secondDenies: true, wantStatus: http.StatusServiceUnavailable},
		{name: "a refusing candidate falls back to an eligible channel", body: goalBody, firstDenies: true, wantStatus: http.StatusOK, wantBCalls: 1},
		{name: "an ordinary turn still uses the channel that refuses goal turns", body: ordinaryBody, firstDenies: true, wantStatus: http.StatusOK, wantACalls: 1},
		// The eligible channel B is tried first and fails upstream; the retry may
		// only consider channels that still satisfy the goal filter, so the
		// channel A that refuses goal turns never receives the alternate attempt.
		{name: "a failed attempt is not retried on a channel that refuses goal turns", body: goalBody, firstDenies: true, secondFails: true, retries: 2, wantStatus: http.StatusInternalServerError, wantBCalls: 1},
	}

	for _, cached := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("cache=%t/%s", cached, tc.name), func(t *testing.T) {
				common.MemoryCacheEnabled = cached
				common.RetryTimes = tc.retries
				// Retries must search an untried channel, which is where the goal
				// filter has to hold as well.
				*affinity = operation_setting.ChannelAffinitySetting{RetryAvoidFailedChannels: true}
				var firstCalls, secondCalls atomic.Int32
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					firstCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"response-from-A","object":"response","status":"completed","model":"goal-policy-routing-test","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				}))
				t.Cleanup(first.Close)
				second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					secondCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if tc.secondFails {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"error":{"message":"upstream exploded","type":"server_error"}}`)
						return
					}
					_, _ = io.WriteString(w, `{"id":"response-from-B","object":"response","status":"completed","model":"goal-policy-routing-test","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				}))
				t.Cleanup(second.Close)
				channels := []model.Channel{
					{Name: "A", Type: constant.ChannelTypeOpenAI, Key: "test-only-A", BaseURL: &first.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "goal-policy-routing-test", Priority: common.GetPointer(int64(10)), AutoBan: common.GetPointer(0)},
					{Name: "B", Type: constant.ChannelTypeOpenAI, Key: "test-only-B", BaseURL: &second.URL, Status: common.ChannelStatusEnabled, Group: "default", Models: "goal-policy-routing-test", Priority: common.GetPointer(int64(5)), AutoBan: common.GetPointer(0)},
				}
				if tc.firstDenies {
					channels[0].SetSetting(kitdto.ChannelSettings{DenyGoal: true})
				}
				if tc.secondDenies {
					channels[1].SetSetting(kitdto.ChannelSettings{DenyGoal: true})
				}
				for i := range channels {
					require.NoError(t, channels[i].Insert())
					t.Cleanup(func() {
						require.NoError(t, model.DB.Where("channel_id = ?", channels[i].Id).Delete(&model.Ability{}).Error)
						require.NoError(t, model.DB.Delete(&channels[i]).Error)
					})
				}
				model.InitChannelCache()

				engine := gin.New()
				engine.POST("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
					if tc.pinned {
						service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channels[0].Id, Source: dto.PinSourceToken, Rank: dto.PinRankToken, RetryMode: dto.PinRetrySingleAttempt})
					}
				}, middleware.Distribute(), func(c *gin.Context) {
					defer common.CleanupBodyStorage(c)
					defer service.ClearCurrentChannelAffinityCache(c)
					Relay(c, types.RelayFormatOpenAIResponses)
				})
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer sk-"+token.Key)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, request)

				assert.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
				assert.Equal(t, tc.wantACalls, firstCalls.Load(), "refused or ineligible attempts never reach the upstream")
				assert.Equal(t, tc.wantBCalls, secondCalls.Load())
				if tc.wantBCalls > 0 && !tc.secondFails {
					assert.Contains(t, recorder.Body.String(), "response-from-B")
				}
				if tc.wantACalls > 0 {
					assert.Contains(t, recorder.Body.String(), "response-from-A")
				}
			})
		}
	}
}
