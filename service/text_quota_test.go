package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The configured DSNs must point at isolated test databases. Each dialect runs
// the real reservation, settlement and log paths with the same billing cases.
func TestFixedPriceBillingDatabaseMatrix(t *testing.T) {
	runQuotaReportingDatabaseMatrix(t, runFixedPriceAccountingCases)
}

func TestRelayUsageReportsDatabaseMatrix(t *testing.T) {
	runQuotaReportingDatabaseMatrix(t, runDurableUsageReportCases)
}

func runQuotaReportingDatabaseMatrix(t *testing.T, runCases func(*testing.T, *gorm.DB, *gorm.DB)) {
	t.Helper()
	for _, dialect := range []struct {
		name   common.DatabaseType
		env    string
		logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.env != "" {
				dsn := os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip(dialect.env + " is not configured")
				}
				if dialect.name == common.DatabaseTypeMySQL {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			logDB := db
			if logDSN := os.Getenv(dialect.logEnv); logDSN != "" || dialect.name == common.DatabaseTypeSQLite {
				var logDriver gorm.Dialector = sqlite.Open(":memory:")
				if dialect.name == common.DatabaseTypeMySQL {
					logDriver = mysql.Open(logDSN)
				} else if dialect.name == common.DatabaseTypePostgreSQL {
					logDriver = postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true})
				}
				logDB, err = gorm.Open(logDriver, &gorm.Config{})
				require.NoError(t, err)
				logSQL, err := logDB.DB()
				require.NoError(t, err)
				logSQL.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			}
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB; common.SetDatabaseTypes(oldMainType, oldLogType) })
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}))
			require.NoError(t, model.MigrateUsageStatistics(db))
			require.NoError(t, model.MigrateUsageEventBackfill(db))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			versionQuery := "select version()"
			if dialect.name == common.DatabaseTypeSQLite {
				versionQuery = "select sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s", version)
			runCases(t, db, logDB)
		})
	}
}

func runFixedPriceAccountingCases(t *testing.T, db, logDB *gorm.DB) {
	t.Helper()
	const mixed = `len <= 32000 ? tier("short", fixed(0.01)) : tier("long", p * 2)`
	const flat = `tier("request", fixed(0.01))`
	const startingQuota = 2_000_000
	const imageExpression = `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
	imageUsage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 300, ImageTokens: 600, CachedTokensDetails: &dto.CachedTokenDetails{ImageTokens: common.GetPointer(200)}}}
	operation_setting.SetToolPriceForTest("fixed_billing_tool", 4)
	t.Cleanup(func() { operation_setting.DeleteToolPriceForTest("fixed_billing_tool") })
	for index, tc := range []struct {
		name, expression                          string
		estimate                                  int
		usage                                     *dto.Usage
		audio, stream, refund, insufficient, tool bool
		realtime, reserveInsufficient             bool
		wallet, outboundImages, retryReservation  int
		groupRatio                                float64
		want                                      int
		unit                                      billingexpr.BillingUnit
		requestedImages, actualImages             int
	}{
		{name: "missing usage charges once", expression: flat, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "zero usage charges once", expression: flat, usage: &dto.Usage{}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "stream charges once", expression: flat, stream: true, usage: &dto.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "audio zero usage charges once", expression: flat, audio: true, usage: &dto.Usage{}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "audio missing usage charges once", expression: flat, audio: true, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "token reservation refunds to fixed price", expression: mixed, estimate: 50000, usage: &dto.Usage{PromptTokens: 100, TotalTokens: 100}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "fixed reservation settles token fallback", expression: mixed, estimate: 100, usage: &dto.Usage{PromptTokens: 50000, TotalTokens: 50000}, want: 50000, unit: billingexpr.BillingUnitToken},
		{name: "missing usage uses estimated token fallback", expression: mixed, estimate: 50000, want: 50000, unit: billingexpr.BillingUnitToken},
		{name: "evaluation error retains fixed reservation metadata", expression: `p == 50 ? tier("error", param("missing") * p + img_cr * 2) : tier("request", fixed(0.01))`, estimate: 100, usage: &dto.Usage{PromptTokens: 50, TotalTokens: 50}, want: 5000, unit: billingexpr.BillingUnitRequest},
		{name: "explicit zero remains free", expression: `tier("free", fixed(0))`, usage: &dto.Usage{PromptTokens: 100, TotalTokens: 100}, unit: billingexpr.BillingUnitRequest},
		{name: "multipliers and separate tool surcharge", expression: flat + ` * (param("fast") == true ? 2 : 1)`, groupRatio: 1.5, tool: true, want: 18000, unit: billingexpr.BillingUnitRequest},
		{name: "failed request refunds exactly once", expression: flat, refund: true},
		{name: "insufficient wallet never reserves tokens", expression: flat, insufficient: true},
		{name: "image cache stream settles usage and refunds unused reservation", expression: imageExpression, estimate: 10000, usage: imageUsage, stream: true, want: 4113, unit: billingexpr.BillingUnitToken},
		{name: "audio settlement records image cache billing inputs", expression: imageExpression, estimate: 10000, usage: imageUsage, audio: true, want: 4113, unit: billingexpr.BillingUnitToken},
		{name: "realtime records actual expression inputs", expression: imageExpression, estimate: 10000, usage: imageUsage, realtime: true, want: 4000, unit: billingexpr.BillingUnitToken},
		{name: "image cache insufficient wallet never reserves tokens", expression: imageExpression, estimate: 10000, insufficient: true},
		{name: "image quantity refunds missing images", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, actualImages: 2, want: 40000, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity keeps request when actual missing", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, want: 60000, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity zero price stays free", expression: `tier("image", fixed(0)) * image_count`, requestedImages: 3, actualImages: 2, unit: billingexpr.BillingUnitRequest},
		{name: "image quantity failure refunds reservation", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 3, refund: true},
		{name: "image quantity exceeds one-image wallet before submission", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 4, wallet: 20000, insufficient: true},
		{name: "image override reserves extra quantity", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 1, outboundImages: 4, want: 80000, unit: billingexpr.BillingUnitRequest},
		{name: "image override cannot exceed remaining wallet", expression: `tier("image", fixed(0.04)) * image_count`, requestedImages: 1, outboundImages: 4, wallet: 40000, reserveInsufficient: true, refund: true},
		{name: "task retry holds a pricier channel and refunds all of it", expression: flat, retryReservation: 40000, refund: true},
		{name: "task retry cannot exceed remaining wallet", expression: flat, retryReservation: 40000, wallet: 20000, reserveInsufficient: true, refund: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := startingQuota
			if tc.insufficient {
				quota = 1
			}
			if tc.wallet > 0 {
				quota = tc.wallet
			}
			user := model.User{Username: fmt.Sprintf("fixed_billing_%d", index), Quota: quota, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: fmt.Sprintf("fixed-billing-test-%d", index), Name: "fixed-billing", RemainQuota: startingQuota, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			channel := model.Channel{Name: "fixed-billing", Key: "unused", Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			t.Cleanup(func() {
				require.NoError(t, logDB.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
				require.NoError(t, db.Unscoped().Delete(&token).Error)
				require.NoError(t, db.Unscoped().Delete(&user).Error)
				require.NoError(t, db.Unscoped().Delete(&channel).Error)
			})
			group := tc.groupRatio
			if group == 0 {
				group = 1
			}
			request := &billingexpr.RequestInput{Body: []byte(`{"fast":true}`)}
			if tc.requestedImages > 0 {
				request.ImageCount = &tc.requestedImages
			}
			cost, trace, err := billingexpr.RunExprWithRequest(tc.expression, billingexpr.TokenParams{P: float64(tc.estimate), Len: float64(tc.estimate)}, *request)
			require.NoError(t, err)
			reservation, err := billingexpr.QuotaRoundStrict(cost / 1_000_000 * common.QuotaPerUnit * group)
			require.NoError(t, err)
			snapshot := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: tc.expression, ExprHash: billingexpr.ExprHashString(tc.expression), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: group, EstimatedTier: trace.MatchedTier, EstimatedBillingUnit: trace.BillingUnit, EstimatedFixedPrice: trace.FixedPrice, EstimatedQuotaAfterGroup: reservation}
			snapshot.EstimatedImageCount = trace.ImageCount
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, OriginModelName: "fixed-test", UsingGroup: "default", UserGroup: "default", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, ForcePreConsume: true, StartTime: time.Now(), IsStream: tc.stream, RelayFormat: types.RelayFormatOpenAI, PriceData: hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: group}}, TieredBillingSnapshot: snapshot, BillingRequestInput: request}
			info.SetEstimatePromptTokens(tc.estimate)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.expression == imageExpression {
				ctx.Request.URL.Path = "/v1/images/generations"
				info.RelayMode = relayconstant.RelayModeImagesGenerations
			}
			apiErr := PreConsumeBilling(ctx, reservation, info)
			if tc.insufficient {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
			} else {
				require.Nil(t, apiErr)
				held, err := model.GetUserQuota(user.Id, true)
				require.NoError(t, err)
				assert.Equal(t, quota-reservation, held)
				if tc.outboundImages > 0 {
					reserveErr := PrepareImageBillingForRequest(ctx, info, tc.outboundImages)
					if tc.reserveInsufficient {
						require.NotNil(t, reserveErr)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, reserveErr.GetErrorCode())
						assert.Equal(t, reservation, info.Billing.GetPreConsumedQuota())
					} else {
						require.Nil(t, reserveErr)
						assert.Equal(t, tc.want, info.Billing.GetPreConsumedQuota())
					}
				}
				if tc.retryReservation > 0 {
					retryErr := PreConsumeBilling(ctx, tc.retryReservation, info)
					if tc.reserveInsufficient {
						require.NotNil(t, retryErr)
						assert.Equal(t, types.ErrorCodeInsufficientUserQuota, retryErr.GetErrorCode())
						assert.Equal(t, reservation, info.Billing.GetPreConsumedQuota())
					} else {
						require.Nil(t, retryErr)
						assert.Equal(t, tc.retryReservation, info.Billing.GetPreConsumedQuota())
					}
				}
				if tc.refund {
					refunded := make(chan struct{}, 1)
					const callback = "fixed_billing_refund_observed"
					require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == "tokens" && tx.Error == nil {
							select {
							case refunded <- struct{}{}:
							default:
							}
						}
					}))
					t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
					info.Billing.Refund(ctx)
					info.Billing.Refund(ctx)
					select {
					case <-refunded:
					case <-time.After(5 * time.Second):
						t.Fatal("refund did not finish")
					}
				} else {
					info.UpdateImageCount(int64(tc.actualImages))
					if tc.tool {
						info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{"fixed_billing_tool": {CallCount: 1}}}
					}
					if tc.realtime {
						PostWssConsumeQuota(ctx, info, info.OriginModelName, &dto.RealtimeUsage{
							InputTokens: tc.usage.PromptTokens, OutputTokens: tc.usage.CompletionTokens, TotalTokens: tc.usage.TotalTokens,
						}, nil)
					} else if tc.audio {
						PostAudioConsumeQuota(ctx, info, tc.usage, nil)
					} else {
						PostTextConsumeQuota(ctx, info, tc.usage, nil)
					}
					require.NoError(t, info.Billing.Settle(tc.want), "a repeated settlement must not charge again")
					var log model.Log
					require.NoError(t, logDB.Where("user_id = ?", user.Id).Take(&log).Error)
					assert.Equal(t, tc.want, log.Quota)
					assert.Equal(t, tc.stream, log.IsStream)
					assert.NotContains(t, log.Content, "nothing was charged")
					var other map[string]any
					require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
					assert.Equal(t, string(tc.unit), other["billing_unit"])
					if tc.requestedImages > 0 {
						count := tc.actualImages
						if count == 0 {
							count = tc.requestedImages
							if tc.outboundImages > 0 {
								count = tc.outboundImages
							}
						}
						assert.Equal(t, float64(count), other["image_count"])
						assert.Equal(t, tc.requestedImages, *request.ImageCount, "actual count must not mutate the frozen request")
					}
					if tc.expression == imageExpression {
						billable, ok := other["billing_tokens"].(map[string]any)
						require.True(t, ok)
						if tc.realtime {
							assert.Equal(t, float64(0), other["image_cache_tokens"])
							assert.Equal(t, float64(1000), billable["p"])
							assert.Equal(t, float64(0), billable["cr"])
							assert.Equal(t, float64(0), billable["img"])
						} else {
							if !tc.audio {
								assert.Equal(t, float64(300), other["cache_tokens"])
							}
							assert.Equal(t, float64(200), other["image_cache_tokens"])
							assert.Equal(t, float64(300), billable["p"])
							assert.Equal(t, float64(100), billable["cr"])
							assert.Equal(t, float64(400), billable["img"])
						}
					} else {
						assert.NotContains(t, other, "billing_tokens")
						assert.NotContains(t, other, "image_cache_tokens")
					}
					if tc.unit == billingexpr.BillingUnitRequest {
						assert.Contains(t, other, "fixed_price")
					} else {
						assert.NotContains(t, other, "fixed_price")
					}
				}
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, quota-tc.want, user.Quota)
			assert.Equal(t, startingQuota-tc.want, token.RemainQuota)
			assert.Equal(t, tc.want, user.UsedQuota)
			assert.Equal(t, tc.want, token.UsedQuota)
			if !tc.refund && !tc.insufficient {
				assert.Equal(t, 1, user.RequestCount)
			}
		})
	}
}

func TestCalculateTextQuotaSummaryUnifiedForClaudeSemantic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	priceData := hosttypes.PriceData{
		ModelRatio:           1,
		CompletionRatio:      2,
		CacheRatio:           0.1,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.25,
		CacheCreation1hRatio: 2,
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio: 1,
		},
	}

	chatRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}
	messageRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}

	chatSummary := calculateTextQuotaSummary(ctx, chatRelayInfo, usage)
	messageSummary := calculateTextQuotaSummary(ctx, messageRelayInfo, usage)

	require.Equal(t, messageSummary.Quota, chatSummary.Quota)
	require.Equal(t, messageSummary.CacheCreationTokens5m, chatSummary.CacheCreationTokens5m)
	require.Equal(t, messageSummary.CacheCreationTokens1h, chatSummary.CacheCreationTokens1h)
	require.True(t, chatSummary.IsClaudeUsageSemantic)
	require.Equal(t, 1488, chatSummary.Quota)
}

func TestCalculateTextQuotaSummaryUsesSplitClaudeCacheCreationRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      1,
			CacheRatio:           0,
			CacheCreationRatio:   1,
			CacheCreation5mRatio: 2,
			CacheCreation1hRatio: 3,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 10,
		},
		ClaudeCacheCreation5mTokens: 2,
		ClaudeCacheCreation1hTokens: 3,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 100 + remaining(5)*1 + 2*2 + 3*3 = 118
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesAnthropicUsageSemanticFromUpstreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, "anthropic", summary.UsageSemantic)
	require.Equal(t, 1488, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesClaudeBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
			InputTokens:              70,
			CacheReadInputTokens:     30,
			CacheCreationInputTokens: 20,
			OutputTokens:             7,
			CacheCreation: &dto.ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 12,
				Ephemeral1hInputTokens: 8,
			},
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticAnthropic, summary.UsageSemantic)
	require.Equal(t, 70, summary.PromptTokens)
	require.Equal(t, 7, summary.CompletionTokens)
	require.Equal(t, 30, summary.CacheTokens)
	require.Equal(t, 20, summary.CacheCreationTokens)
	require.Equal(t, 12, summary.CacheCreationTokens5m)
	require.Equal(t, 8, summary.CacheCreationTokens1h)
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesGeminiBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gemini-2.5-flash",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			CacheRatio:      0.1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{
			PromptTokenCount:        100,
			ToolUsePromptTokenCount: 5,
			CandidatesTokenCount:    20,
			ThoughtsTokenCount:      3,
			TotalTokenCount:         128,
			CachedContentTokenCount: 7,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticGemini, summary.UsageSemantic)
	require.Equal(t, 105, summary.PromptTokens)
	require.Equal(t, 23, summary.CompletionTokens)
	require.Equal(t, 7, summary.CacheTokens)
	require.Equal(t, 128, summary.TotalTokens)
	require.Equal(t, 145, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "gpt-4o",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		TotalTokens:      1998,
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens:     80,
			CompletionTokens: 9,
			TotalTokens:      89,
		}),
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))

	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticOpenAI, summary.UsageSemantic)
	require.Equal(t, 80, summary.PromptTokens)
	require.Equal(t, 9, summary.CompletionTokens)
	require.Equal(t, 89, summary.TotalTokens)
	require.Equal(t, 98, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIResponsesInputTokenDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-4o",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 2,
			CacheRatio:      0.25,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	responsesUsage := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 10,
		TotalTokens:  110,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 40,
		},
	}
	convertedUsage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 10,
		TotalTokens:      110,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 40,
		},
		BillingUsage: dto.NewOpenAIResponsesBillingUsage(responsesUsage),
	}

	effectiveUsage := effectiveBillingUsage(convertedUsage)
	require.Equal(t, 40, effectiveUsage.PromptTokensDetails.CachedTokens)
	require.Zero(t, convertedUsage.BillingUsage.OpenAIUsage.PromptTokensDetails.CachedTokens)

	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveUsage)
	require.Equal(t, 40, summary.CacheTokens)
	// 60 uncached input + 40*0.25 cached input + 10*2 output = 90.
	require.Equal(t, 90, summary.Quota)
}

func TestUsageFromOpenAIBillingUsageNormalizesCacheDetailsWithoutOverwritingCanonicalValues(t *testing.T) {
	responsesUsage := &dto.Usage{
		InputTokens:          100,
		OutputTokens:         10,
		PromptCacheHitTokens: 55,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 8,
			TextTokens:   12,
		},
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens:         40,
			CachedCreationTokens: 5,
			CacheWriteTokens:     6,
			TextTokens:           60,
			ImageTokens:          7,
			AudioTokens:          9,
		},
	}

	billingUsage := dto.NewOpenAIResponsesBillingUsage(responsesUsage)
	usage := effectiveBillingUsage(&dto.Usage{BillingUsage: billingUsage})

	require.Equal(t, 8, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 5, usage.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 6, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 12, usage.PromptTokensDetails.TextTokens)
	require.Equal(t, 7, usage.PromptTokensDetails.ImageTokens)
	require.Equal(t, 9, usage.PromptTokensDetails.AudioTokens)
	require.Zero(t, billingUsage.OpenAIUsage.PromptTokensDetails.CachedCreationTokens)
}

func TestUsageFromOpenAIBillingUsageFallsBackToPromptCacheHitTokens(t *testing.T) {
	usage := effectiveBillingUsage(&dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens:         100,
			CompletionTokens:     10,
			PromptCacheHitTokens: 35,
		}),
	})

	require.Equal(t, 35, usage.PromptTokensDetails.CachedTokens)
}

func TestCalculateTextQuotaSummaryNormalizesOpenAIResponsesBillingUsageDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "gpt-5.6-sol",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.5,
			CacheCreationRatio: 2,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	responsesDetails := dto.InputTokenDetails{
		CachedTokens:     80,
		CacheWriteTokens: 10,
		TextTokens:       100,
	}
	usage := &dto.Usage{
		PromptTokens:     999,
		CompletionTokens: 999,
		BillingUsage: dto.NewOpenAIResponsesBillingUsage(&dto.Usage{
			InputTokens:        100,
			OutputTokens:       10,
			TotalTokens:        110,
			InputTokensDetails: &responsesDetails,
		}),
	}

	effectiveUsage := effectiveBillingUsage(usage)
	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveUsage)

	require.Equal(t, dto.BillingUsageSourceOAIResponses, effectiveUsage.UsageSource)
	require.Equal(t, responsesDetails, effectiveUsage.PromptTokensDetails)
	require.Equal(t, 100, summary.PromptTokens)
	require.Equal(t, 10, summary.CompletionTokens)
	require.Equal(t, 80, summary.CacheTokens)
	require.Equal(t, 10, summary.CacheCreationTokens)
	// (100-80-10) + 80*0.5 + 10*2 + 10*2 = 90
	require.Equal(t, 90, summary.Quota)
}

func TestUsageBillingPathForLog(t *testing.T) {
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	invalidBillingUsage := &dto.Usage{
		PromptTokens: 1,
		BillingUsage: &dto.BillingUsage{
			Source:   dto.BillingUsageSourceClaudeMessages,
			Semantic: dto.BillingUsageSemanticAnthropic,
		},
	}
	require.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, invalidBillingUsage))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, invalidBillingUsage))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, &dto.Usage{}))
	require.Equal(t, usageBillingPathOpenAI, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathGemini, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 1}),
	}))
	require.Equal(t, usageBillingPathGeminiEstimated, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewEstimatedGeminiChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
}

func TestAppendUsageBillingPathForLogWritesAdminInfo(t *testing.T) {
	other := model.NewLogOther()
	appendUsageBillingPathForLog(other, true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	})

	adminInfo, ok := other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, usageBillingPathAnthropic, adminInfo["usage_billing_path"])

	other = model.NewLogOther()
	appendUsageBillingPathForLog(other, true, nil)
	adminInfo, ok = other.Snapshot()["admin_info"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, usageBillingPathLocal, adminInfo["usage_billing_path"])
}

func TestCacheWriteTokensTotal(t *testing.T) {
	t.Run("split cache creation", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens:   50,
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("legacy cache creation", func(t *testing.T) {
		summary := textQuotaSummary{CacheCreationTokens: 50}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("split cache creation without aggregate remainder", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 30, cacheWriteTokensTotal(summary))
	})
}

func TestCalculateTextQuotaSummaryHandlesLegacyClaudeDerivedOpenAIUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:           1,
			CompletionRatio:      5,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     62,
		CompletionTokens: 95,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 3544,
		},
		ClaudeCacheCreation5mTokens: 586,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 62 + 3544*0.1 + 586*1.25 + 95*5 = 1624.9 => 1624
	require.Equal(t, 1624, summary.Quota)
}

func TestCalculateTextQuotaSummaryBillsOpenAICacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-5.1",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("uncached remainder stays positive", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     1473,
			CompletionTokens: 19,
			PromptTokensDetails: dto.InputTokenDetails{
				CacheWriteTokens: 1470,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 1470, summary.CacheCreationTokens)
		// (1473-0-1470) + 1470*1.25 + 19*2 = 3 + 1837.5 + 38 = 1878.5 => 1879
		require.Equal(t, 1879, summary.Quota)
	})

	t.Run("uncached remainder clamps to zero", func(t *testing.T) {
		// Real OpenAI payload shape: cached_tokens + cache_write_tokens exceeds
		// prompt_tokens because both are unadjusted prefix counts. The negative
		// remainder must clamp to zero, never turn into a negative base charge.
		usage := &dto.Usage{
			PromptTokens:     3619,
			CompletionTokens: 36,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:     2921,
				CacheWriteTokens: 3616,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 3619, summary.PromptTokens)
		require.Equal(t, 3616, summary.CacheCreationTokens)
		// max(3619-2921-3616, 0) + 2921*0.1 + 3616*1.25 + 36*2 = 4884.1 => 4884
		require.Equal(t, 4884, summary.Quota)
	})
}

func TestCalculateTextQuotaSummaryBillsCacheCreationInputTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "cache-creation-input",
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("creation tokens billed at creation ratio", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":300}}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Equal(t, 1510, summary.PromptTokens)
		assert.Equal(t, 1200, summary.CacheTokens)
		assert.Equal(t, 300, summary.CacheCreationTokens)
		assert.Zero(t, summary.CacheCreationTokens5m)
		assert.Zero(t, summary.CacheCreationTokens1h)
		// (1510-1200-300) + 1200*0.1 + 300*1.25 + 20 = 525
		assert.Equal(t, 525, summary.Quota)
	})

	t.Run("missing creation field stays in prompt price", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Zero(t, summary.CacheCreationTokens)
		// (1510-1200) + 1200*0.1 + 20 = 450
		assert.Equal(t, 450, summary.Quota)
	})

	t.Run("cache write and creation scalar take the larger count", func(t *testing.T) {
		var usage dto.Usage
		require.NoError(t, common.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_write_tokens":100,"cache_creation_input_tokens":300}}`), &usage))
		summary := calculateTextQuotaSummary(ctx, relayInfo, &usage)

		assert.Equal(t, 300, summary.CacheCreationTokens)
		assert.Zero(t, summary.CacheCreationTokens5m)
		assert.Zero(t, summary.CacheCreationTokens1h)
		assert.Equal(t, 525, summary.Quota)
	})
}

// OpenAI 12h cache writes bill at twice the write price and keep their own
// count in the consume log, with ratio and expression pricing alike.
func TestPostTextConsumeQuotaBillsOpenAI12hCacheWrites(t *testing.T) {
	truncate(t)
	const userID, channelID = 63, 63
	seedUser(t, userID, 1_000_000)
	seedChannel(t, channelID)
	gin.SetMode(gin.TestMode)
	// The OpenAI prompt caching guide example at gpt-6-astra prices: $10 input,
	// $1 cached, $12.50 30m write and $25 12h write per 1M tokens.
	usage := &dto.Usage{PromptTokens: 12000, TotalTokens: 12000, PromptTokensDetails: dto.InputTokenDetails{
		CachedTokens: 4000, CacheWriteTokens: 7000,
		CacheWriteTokensDetails: &dto.CacheWriteTokenDetails{TTL30mTokens: 3000, TTL12hTokens: 4000},
	}}
	for _, tc := range []struct {
		name     string
		price    hosttypes.PriceData
		expr     string
		ratio12h float64
	}{
		{"ratio", hosttypes.PriceData{ModelRatio: 5, CompletionRatio: 5, CacheRatio: 0.1, CacheCreationRatio: 1.25, CacheCreation12hRatio: 2.5}, "", 2.5},
		{"expression without cc12h", hosttypes.PriceData{}, `tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5)`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			tc.price.GroupRatioInfo = hosttypes.GroupRatioInfo{GroupRatio: 1}
			info := &relaycommon.RelayInfo{
				UserId:          userID,
				OriginModelName: "gpt-6-astra",
				UsingGroup:      "default",
				StartTime:       time.Now(),
				RelayFormat:     types.RelayFormatOpenAIResponses,
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
				PriceData:       tc.price,
			}
			if tc.expr != "" {
				info.TieredBillingSnapshot = makeSnapshot(tc.expr, 1, 0, 0)
			}

			PostTextConsumeQuota(ctx, info, usage, nil)

			log := getLastLog(t)
			require.NotNil(t, log)
			// 1000 input + 4000 cached + 3000 30m + 4000 12h writes = $0.1515.
			assert.Equal(t, 75750, log.Quota)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
			assert.Equal(t, float64(7000), other["cache_creation_tokens"])
			assert.Equal(t, float64(7000), other["cache_write_tokens"])
			assert.Equal(t, float64(4000), other["cache_creation_tokens_12h"])
			assert.Equal(t, tc.ratio12h, other["cache_creation_ratio_12h"])
		})
	}
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheReadFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// OpenRouter OpenAI-format display keeps prompt_tokens as total input,
	// but billing still separates normal input from cache read tokens.
	// quota = (2604 - 2432) + 2432*0.1 + 383 = 798.2 => 798
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheCreationFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 100,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// prompt_tokens is still logged as total input, but cache creation is billed separately.
	// quota = (2604 - 100) + 100*1.25 + 383 = 3012
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 3012, summary.Quota)
}

func TestCalculateTextQuotaSummaryKeepsPrePRClaudeOpenRouterBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "anthropic/claude-3.7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: hosttypes.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// Pre-PR PostClaudeConsumeQuota behavior for OpenRouter:
	// prompt = 2604 - 2432 = 172
	// quota = 172 + 2432*0.1 + 383 = 798.2 => 798
	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, 172, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestComposeTieredTextQuotaKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	// 11 $/1K => 0.011 per completed image output, matching the prior fixed low-tier charge.
	operation_setting.SetToolPriceForTest(dto.BuildInToolImageGeneration, 11.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {
					CallCount: 1,
				},
				dto.BuildInToolFileSearch: {
					CallCount: 2,
				},
				dto.BuildInToolImageGeneration: {
					CallCount: 1,
				},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1000, &billingexpr.TieredResult{
		ActualQuotaBeforeGroup: 1000,
		ActualQuotaAfterGroup:  1000,
	})

	require.Equal(t, int64(13000), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14000, quota)
}

func TestComposeTieredTextQuotaFallbackKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1.25},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearch: {ToolName: dto.BuildInToolWebSearch, CallCount: 2},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1250, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 13750, quota)
}

func TestComposeTieredTextQuotaErrorFallbackUsesPreConsumedQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1.25},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearch: {ToolName: dto.BuildInToolWebSearch, CallCount: 2},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// tieredResult=nil simulates a settlement error where TryTieredSettle
	// falls back to FinalPreConsumedQuota (2000), which differs from
	// EstimatedQuotaBeforeGroup * GroupRatio (1250).
	preConsumedFallback := 2000
	quota := composeTieredTextQuota(relayInfo, summary, preConsumedFallback, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14500, quota)
}

// TestTryTieredSettleRecordsClampOnOverflow guards that an oversized tiered
// settlement both saturates the quota and records the clamp on RelayInfo, so
// every consume path (text, audio, WSS) can surface it under admin_info.
func TestTryTieredSettleRecordsClampOnOverflow(t *testing.T) {
	// exprOutput = p * 1e12; quotaBeforeGroup = p*1e12 / 1e6 * 5e5 far exceeds
	// the supported single-request range and must saturate.
	exprStr := `tier("base", p * 1000000000000)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "overflow-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, quota, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1_000_000_000})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Equal(t, common.MaxQuota, quota, "oversized settlement must clamp, never wrap negative")
	require.NotNil(t, relayInfo.QuotaClamp, "clamp must be recorded on RelayInfo for admin auditing")
	require.Equal(t, common.QuotaClampOverflow, relayInfo.QuotaClamp.Kind)
}

// TestTryTieredSettleNoClampInRange confirms an in-range settlement leaves
// RelayInfo.QuotaClamp nil.
func TestTryTieredSettleNoClampInRange(t *testing.T) {
	exprStr := `tier("base", p * 2 + c * 10)`
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "in-range-model",
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:  "tiered_expr",
			ExprString:   exprStr,
			ExprHash:     billingexpr.ExprHashString(exprStr),
			GroupRatio:   1,
			QuotaPerUnit: 500_000,
		},
	}

	ok, _, result := TryTieredSettle(relayInfo, billingexpr.TokenParams{P: 1000, C: 500})

	require.True(t, ok)
	require.NotNil(t, result)
	require.Nil(t, relayInfo.QuotaClamp, "in-range settlement must not record a clamp")
}

func TestCalculateTextQuotaSummaryFixedPriceAppliesImageCountOnceAndAllowsOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	priceData := hosttypes.PriceData{
		ModelPrice: 0.12,
		UsePrice:   true,
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio: 1,
		},
	}
	priceData.AddOtherRatio("n", 3)
	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "dall-e-3",
		PriceData:       priceData,
		StartTime:       time.Now(),
	}
	usage := &dto.Usage{PromptTokens: 1, TotalTokens: 1}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 180000, summary.Quota)

	// An adaptor-reported actual count replaces the requested count rather
	// than multiplying it a second time.
	relayInfo.PriceData.AddOtherRatio("n", 2)
	summary = calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.Equal(t, 120000, summary.Quota)
}

func TestCalculateTextToolCallSurchargeGeneralizedBuiltInTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("my_fn", 5.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("my_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 2},
				"my_fn":                         {CallCount: 3},
				"unpriced":                      {CallCount: 5},
			},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  "o1",
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat((10.0*2 + 5.0*3) / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 2)
	assert.Equal(t, "my_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 3, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 5.0, summary.ToolSurchargeItems[0].Price)
	assert.Equal(t, dto.BuildInToolWebSearchPreview, summary.ToolSurchargeItems[1].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[1].Count)
	assert.Equal(t, 10.0, summary.ToolSurchargeItems[1].Price)
}

func TestCalculateTextToolCallSurchargeKeepsSearchPreviewFallbackWithCustomFunctions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("my_fn", 5)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("my_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeChatCompletions,
		OriginModelName: "gpt-4o-search-preview",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				"my_fn": {CallCount: 1},
			},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  relayInfo.OriginModelName,
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

	require.Len(t, summary.ToolSurchargeItems, 2)
	assert.Equal(t, "my_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, dto.BuildInToolWebSearchPreview, summary.ToolSurchargeItems[1].Name)
	expected := decimal.NewFromFloat((5.0 + 25.0) / 1000).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
}

func TestCalculateTextToolCallSurchargeDoesNotInferSearchForResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	relayInfo := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		OriginModelName: "gpt-4o-search-preview",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{},
		},
	}
	summary := &textQuotaSummary{
		ModelName:  relayInfo.OriginModelName,
		GroupRatio: 1,
	}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

	assert.True(t, surcharge.IsZero())
	assert.Empty(t, summary.ToolSurchargeItems)
}

// Every BuiltInTools count is bounded by MaxBillableToolCallCount at
// settlement, whatever recorded it (upstream-reported counts and per-item
// counts alike).
func TestCalculateTextToolCallSurchargeClampsCallCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	for recorded, billed := range map[int]int{5: 5, 10_000: 10_000, 10_005: 10_000} {
		relayInfo := &relaycommon.RelayInfo{
			OriginModelName: "claude-3-7-sonnet",
			ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
				BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
					dto.BuildInToolWebSearch: {CallCount: recorded},
				},
			},
		}
		summary := &textQuotaSummary{ModelName: relayInfo.OriginModelName, GroupRatio: 1}

		surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)

		assert.Equal(t, []ToolSurchargeItem{{Name: dto.BuildInToolWebSearch, Count: billed, Price: 10}}, summary.ToolSurchargeItems, recorded)
		expected := decimal.NewFromFloat(10.0 * float64(billed) / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		assert.True(t, expected.Equal(surcharge), "recorded %d: got %s want %s", recorded, surcharge, expected)
	}
}

func TestMergeToolSurchargeItemsSaturatesCountOverflow(t *testing.T) {
	items := []ToolSurchargeItem{
		{Name: "custom_fn", Count: math.MaxInt, Price: 5},
		{Name: "custom_fn", Count: 1, Price: 5},
	}

	merged := mergeToolSurchargeItems(items)

	require.Len(t, merged, 1)
	assert.Equal(t, math.MaxInt, merged[0].Count)
}

// A zero-token request (e.g. /v1/alpha/search returns no usage) must still
// bill a tool-call surcharge. Regression for the TotalTokens==0 gate zeroing
// out the surcharge quota.
func TestCalculateTextQuotaSummaryZeroTokensStillBillsToolSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1

	usage := &dto.Usage{} // zero tokens, mirrors alpha search
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Equal(t, 0, summary.TotalTokens)
	assert.False(t, summary.ToolCallSurchargeQuota.IsZero(), "surcharge should be computed")
	assert.Greater(t, summary.Quota, 0, "quota must not be zeroed out for a zero-token web search request")
	expected := common.QuotaFromDecimal(summary.ToolCallSurchargeQuota)
	assert.Equal(t, expected, summary.Quota)
}

func TestClaudeCacheOnlyUsageRemainsBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		usage     dto.ClaudeUsage
		wantQuota int
	}{
		{name: "one-hour cache write", usage: dto.ClaudeUsage{CacheCreationInputTokens: 20000, CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral1hInputTokens: 20000}}, wantQuota: 100000},
		{name: "five-minute cache write", usage: dto.ClaudeUsage{CacheCreationInputTokens: 100, CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 100}}, wantQuota: 313},
		{name: "cache read", usage: dto.ClaudeUsage{CacheReadInputTokens: 100}, wantQuota: 25},
		{name: "no usage", usage: dto.ClaudeUsage{}, wantQuota: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				RelayFormat:     types.RelayFormatClaude,
				OriginModelName: "claude-opus-4-8",
				PriceData: hosttypes.PriceData{
					ModelRatio: 2.5, CompletionRatio: 5, CacheRatio: 0.1,
					CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2,
					GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
				},
			}
			usage := &dto.Usage{UsageSemantic: dto.BillingUsageSemanticAnthropic, BillingUsage: dto.NewClaudeMessagesBillingUsage(&tc.usage)}
			summary := calculateTextQuotaSummary(ctx, info, effectiveBillingUsage(usage))
			assert.Zero(t, summary.PromptTokens)
			assert.Zero(t, summary.CompletionTokens)
			assert.Equal(t, tc.wantQuota, summary.Quota)
			assert.Equal(t, tc.wantQuota > 0, summary.hasBillableUsage())
			t.Logf("input=%d output=%d quota=%d", summary.PromptTokens, summary.CompletionTokens, summary.Quota)
		})
	}
}

func TestCalculateTextQuotaSummaryDoesNotApplyRequestMultipliersToToolSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.AddOtherRatio("n", 3)

	summary := calculateTextQuotaSummary(ctx, relayInfo, &dto.Usage{})

	expected := decimal.NewFromFloat(10.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(summary.ToolCallSurchargeQuota))
	assert.Equal(t, common.QuotaFromDecimal(expected), summary.Quota)
}

func TestCalculateTextToolCallSurchargeGeminiGoogleSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.7-flash",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolGoogleSearch: {ToolName: dto.BuildInToolGoogleSearch, CallCount: 1},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gemini-3.7-flash", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(14.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolGoogleSearch, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 1, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 14.0, summary.ToolSurchargeItems[0].Price)
}

func TestCalculateTextToolCallSurchargeGeminiFunctionCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	operation_setting.SetToolPriceForTest("gemini_surcharge_fn", 5.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("gemini_surcharge_fn")
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-flash",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				"gemini_surcharge_fn": {CallCount: 2},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gemini-2.5-flash", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(5.0 * 2 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, "gemini_surcharge_fn", summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 5.0, summary.ToolSurchargeItems[0].Price)

	other := model.NewLogOther()
	appendToolSurchargeLogInfo(other, summary.ToolSurchargeItems)
	assert.Equal(t, summary.ToolSurchargeItems, other.Snapshot()["tool_surcharges"])
}

func TestCalculateTextToolCallSurchargeImageGenerationDefaultPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 2},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gpt-5.1", GroupRatio: 1.5}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	expected := decimal.NewFromFloat(150.0).
		Mul(decimal.NewFromInt(2)).
		Div(decimal.NewFromInt(1000)).
		Mul(decimal.NewFromFloat(1.5)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expected.Equal(surcharge), "got %s want %s", surcharge, expected)
	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolImageGeneration, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 2, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 150.0, summary.ToolSurchargeItems[0].Price)
}

func TestCalculateTextToolCallSurchargeImageGenerationExplicitZeroDisables(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	operation_setting.SetToolPriceForTest(dto.BuildInToolImageGeneration, 0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 3},
			},
		},
	}
	summary := &textQuotaSummary{ModelName: "gpt-5.1", GroupRatio: 1}

	surcharge := calculateTextToolCallSurcharge(ctx, relayInfo, summary)
	assert.True(t, surcharge.IsZero())
	assert.Empty(t, summary.ToolSurchargeItems)
}

func TestCalculateTextQuotaSummaryImageGenerationUsesStructuredSurcharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest(dto.BuildInToolImageGeneration)
	})

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolImageGeneration: {CallCount: 1},
			},
		},
	}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
	relayInfo.PriceData.ModelRatio = 1
	relayInfo.PriceData.CompletionRatio = 1

	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.Len(t, summary.ToolSurchargeItems, 1)
	assert.Equal(t, dto.BuildInToolImageGeneration, summary.ToolSurchargeItems[0].Name)
	assert.Equal(t, 1, summary.ToolSurchargeItems[0].Count)
	assert.Equal(t, 150.0, summary.ToolSurchargeItems[0].Price)

	expectedSurcharge := decimal.NewFromFloat(150.0 / 1000).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	assert.True(t, expectedSurcharge.Equal(summary.ToolCallSurchargeQuota),
		"got %s want %s", summary.ToolCallSurchargeQuota, expectedSurcharge)
	assert.Greater(t, summary.Quota, 0)
}

func TestAppendToolSurchargeLogInfoWritesOnlyStructuredFields(t *testing.T) {
	items := []ToolSurchargeItem{
		{Name: dto.BuildInToolWebSearch, Count: 2, Price: 10},
		{Name: dto.BuildInToolImageGeneration, Count: 1, Price: 150},
	}
	other := model.NewLogOther()

	appendToolSurchargeLogInfo(other, items)

	fields := other.Snapshot()
	assert.Equal(t, items, fields["tool_surcharges"])
	assert.NotContains(t, fields, "web_search")
	assert.NotContains(t, fields, "web_search_call_count")
	assert.NotContains(t, fields, "web_search_price")
	assert.NotContains(t, fields, "file_search")
	assert.NotContains(t, fields, "image_generation_call")
	assert.NotContains(t, fields, "image_generation_call_price")
}

func runDurableUsageReportCases(t *testing.T, db, logDB *gorm.DB) {
	t.Helper()
	require.NoError(t, model.MigrateUsageStatistics(db))
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	oldRedis, oldBatch, oldQuotaPerUnit := common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit
	oldLogs, oldExport, oldNotify := common.LogConsumeEnabled, common.DataExportEnabled, common.QuotaRemindEnabled
	common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaRemindEnabled = false, false, false
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = oldRedis, oldBatch, oldQuotaPerUnit
		common.LogConsumeEnabled, common.DataExportEnabled, common.QuotaRemindEnabled = oldLogs, oldExport, oldNotify
	})
	t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "false")

	for _, flags := range []struct {
		name         string
		logs, export bool
	}{
		{name: "all_optional_capture_off"},
		{name: "logs_off", export: true},
		{name: "export_off", logs: true},
	} {
		t.Run(flags.name, func(t *testing.T) {
			common.LogConsumeEnabled, common.DataExportEnabled = flags.logs, flags.export
			for _, name := range []string{"anthropic", "normalized_model", "zero_usage", "missing_usage", "audio", "audio_fixed_zero", "realtime_turns", "settlement_error", "prepare_retry"} {
				t.Run(name, func(t *testing.T) {
					user, token, channel := seedUsageReportingAccount(t, db, logDB)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					parent, cancel := context.WithCancel(context.Background())
					cancel()
					c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(parent)
					c.Set("username", "historical-user")
					c.Set("token_name", "historical-token")
					c.Set(common.RequestIdKey, "same-report-request")
					c.Set(common.UpstreamRequestIdKey, "upstream-report-request")
					info := &relaycommon.RelayInfo{
						UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
						ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
						OriginModelName: "usage-report-test", UsingGroup: "default", StartTime: time.Now(),
						RelayFormat: types.RelayFormatOpenAI,
						PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1,
							CacheCreationRatio: 1, CacheCreation5mRatio: 1, CacheCreation1hRatio: 1,
							GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
					}
					usage := &dto.Usage{PromptTokens: 17, CompletionTokens: 3, TotalTokens: 20,
						PromptTokensDetails: dto.InputTokenDetails{TextTokens: 17},
						CompletionTokenDetails: dto.OutputTokenDetails{TextTokens: 3}}
					wantQuota, wantPrompt, wantCompletion, wantCount := 20, 17, 3, int64(1)
					wantRead, wantWrite, wantRanking, wantInput := int64(0), int64(0), int64(20), int64(17)
					var failedPrepareKey string
					switch name {
					case "anthropic":
						usage.UsageSemantic, usage.UsageSource, usage.InputTokens = "anthropic", "anthropic", 99
						usage.PromptTokensDetails.CachedTokens = 7
						usage.PromptTokensDetails.CachedCreationTokens = 4
						usage.ClaudeCacheCreation5mTokens, usage.ClaudeCacheCreation1hTokens = 2, 3
						wantQuota, wantRead, wantWrite, wantRanking, wantInput = 32, 7, 5, 32, 29
					case "normalized_model":
						info.OriginModelName = "gpt-4-gizmo-specific"
					case "zero_usage":
						usage = &dto.Usage{}
						wantQuota, wantPrompt, wantCompletion, wantRanking, wantInput = 0, 0, 0, 0, 0
					case "missing_usage":
						usage = nil
						info.SetEstimatePromptTokens(17)
						wantQuota, wantCompletion, wantRanking = 17, 0, 17
					case "audio_fixed_zero":
						usage = &dto.Usage{}
						const expression = `tier("request", fixed(0.01))`
						info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
							BillingMode: "tiered_expr", ExprString: expression, ExprHash: billingexpr.ExprHashString(expression),
							QuotaPerUnit: 500_000, GroupRatio: 1, EstimatedBillingUnit: billingexpr.BillingUnitRequest,
						}
						wantQuota, wantPrompt, wantCompletion, wantRanking, wantInput = 5000, 0, 0, 0, 0
					case "realtime_turns":
						wantCount = 2
					case "prepare_retry":
						const callback = "usage_report_prepare_failure"
						require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
							if candidate, ok := tx.Statement.Dest.(*model.UsageEvent); ok && failedPrepareKey == "" {
								failedPrepareKey = candidate.EventKey
								_ = tx.AddError(errors.New("forced statistics prepare failure"))
							}
						}))
						t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callback)) })
					case "settlement_error":
						const callback = "usage_report_token_failure"
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table == "tokens" {
								_ = tx.AddError(errors.New("forced token settlement failure"))
							}
						}))
						t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
					}
					var reports []*model.UsageSourceReport
					for range wantCount {
						switch name {
						case "audio", "audio_fixed_zero":
							PostAudioConsumeQuota(c, info, usage, nil)
						case "realtime_turns":
							PostWssConsumeQuota(c, info, info.OriginModelName, &dto.RealtimeUsage{
								InputTokens: 17, OutputTokens: 3, TotalTokens: 20,
								InputTokenDetails: dto.InputTokenDetails{TextTokens: 17}, OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 3},
							}, nil)
						default:
							PostTextConsumeQuota(c, info, usage, nil)
						}
						value, ok := common.GetContextKey(c, constant.ContextKeyConsumeLog)
						require.True(t, ok)
						params, ok := value.(*model.RecordConsumeLogParams)
						require.True(t, ok)
						require.NotNil(t, params.UsageReport)
						assert.Equal(t, wantQuota, params.Quota)
						reports = append(reports, params.UsageReport)
					}
					if len(reports) == 2 {
						assert.NotEqual(t, reports[0].Input.EventKey, reports[1].Input.EventKey, "turn identity is not request identity")
					}
					var events []model.UsageEvent
					require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&events).Error)
					require.Len(t, events, int(wantCount))
					for index, event := range events {
						assert.Equal(t, model.UsageEventSourceReady, event.SourceState)
						assert.Equal(t, model.UsageEventProjectionVersion, event.AppliedVersion)
						assert.Equal(t, int64(wantQuota), event.QuotaMagnitude)
						assert.Equal(t, int64(wantPrompt), event.PromptTokens)
						assert.Equal(t, int64(wantCompletion), event.CompletionTokens)
						assert.Equal(t, wantRead, event.CacheReadTokens)
						assert.Equal(t, wantWrite, event.CacheWriteTokens)
						assert.Equal(t, "historical-user", event.Username)
						assert.Equal(t, "historical-token", event.TokenName)
						assert.Equal(t, reports[index].Input.OccurredAt, event.OccurredAt)
						assert.Equal(t, "same-report-request", event.RequestID)
						assert.Equal(t, "upstream-report-request", event.UpstreamRequestID)
						if name == "missing_usage" {
							assert.Zero(t, event.KnownMask&model.UsageEventKnownCacheReadTokens)
							assert.Zero(t, event.KnownMask&model.UsageEventKnownCacheWriteTokens)
						} else {
							assert.NotZero(t, event.KnownMask&model.UsageEventKnownCacheReadTokens)
							assert.NotZero(t, event.KnownMask&model.UsageEventKnownCacheWriteTokens)
						}
						if name == "prepare_retry" {
							require.NotEmpty(t, failedPrepareKey)
							assert.Equal(t, failedPrepareKey, event.EventKey)
						}
						if name == "anthropic" {
							assert.Equal(t, model.UsageSemanticAnthropic, event.UsageSemantic)
							assert.Equal(t, int64(99), event.ReportedInputTokens)
						} else {
							assert.Zero(t, event.KnownMask&model.UsageEventKnownReportedInputTokens)
						}
						if name == "normalized_model" {
							assert.Equal(t, "gpt-4-gizmo-*", event.ModelName)
						}
					}
					var projection model.UsageChannel
					require.NoError(t, db.Where("channel_id = ?", channel.Id).Take(&projection).Error)
					assert.Equal(t, wantCount, projection.ConsumeCount)
					assert.Equal(t, int64(wantQuota)*wantCount, projection.GrossQuota)
					var hours []model.UsageHour
					require.NoError(t, db.Where("user_id = ?", user.Id).Find(&hours).Error)
					var ranking, input, cacheUnknown int64
					for _, hour := range hours {
						ranking += hour.RankingTokens
						input += hour.InputDenominator
						cacheUnknown += hour.CacheUnknownCount
					}
					assert.Equal(t, wantRanking*wantCount, ranking)
					assert.Equal(t, wantInput*wantCount, input)
					if name == "missing_usage" {
						assert.Equal(t, int64(1), cacheUnknown)
					} else {
						assert.Zero(t, cacheUnknown)
					}
					evidence := "relay_text_report"
					if name == "audio" || name == "audio_fixed_zero" {
						evidence = "relay_audio_report"
					} else if name == "realtime_turns" {
						evidence = "relay_realtime_report"
					}
					for _, report := range reports {
						require.NoError(t, model.PrepareUsageSourceReport(context.Background(), report))
						require.NoError(t, model.ConfirmUsageSourceReport(context.Background(), report, model.UsageEventSourceReady, evidence))
					}
					require.NoError(t, db.Where("channel_id = ?", channel.Id).Take(&projection).Error)
					assert.Equal(t, wantCount, projection.ConsumeCount, "numeric replay never repeats billing or projections")
					var logs []model.Log
					require.NoError(t, logDB.Where("user_id = ?", user.Id).Order("id").Find(&logs).Error)
					if flags.logs {
						require.Len(t, logs, int(wantCount))
						for index, log := range logs {
							assert.Equal(t, events[index].OccurredAt, log.CreatedAt)
							assert.Contains(t, log.Other, events[index].EventKey)
						}
					} else {
						assert.Empty(t, logs)
					}
					require.NoError(t, db.First(&user, user.Id).Error)
					require.NoError(t, db.First(&token, token.Id).Error)
					assert.Equal(t, 100_000-int(wantCount)*wantQuota, user.Quota)
					if name == "settlement_error" {
						assert.Equal(t, 100_000, token.RemainQuota)
					} else {
						assert.Equal(t, 100_000-int(wantCount)*wantQuota, token.RemainQuota)
					}
				})
			}
		})
	}

	common.LogConsumeEnabled, common.DataExportEnabled = false, false
	t.Run("logging_off_projection_recovery_never_replays_money", func(t *testing.T) {
		user, token, channel := seedUsageReportingAccount(t, db, logDB)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		c.Set("username", "recovery-owner")
		c.Set("token_name", "recovery-token")
		info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, OriginModelName: "recovery-model", StartTime: time.Now(),
			PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		const callback = "usage_report_projection_unavailable"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "usage_hours" {
				_ = tx.AddError(errors.New("forced projection failure"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
		PostTextConsumeQuota(c, info, &dto.Usage{PromptTokens: 17, CompletionTokens: 3, TotalTokens: 20}, nil)
		require.NoError(t, db.Callback().Update().Remove(callback))
		var event model.UsageEvent
		require.NoError(t, db.Where("user_id = ?", user.Id).Take(&event).Error)
		assert.Equal(t, model.UsageEventSourceReady, event.SourceState)
		assert.Zero(t, event.AppliedVersion, "failed projection retains the committed numeric obligation")
		var logs int64
		require.NoError(t, logDB.Model(&model.Log{}).Where("user_id = ?", user.Id).Count(&logs).Error)
		assert.Zero(t, logs)
		var beforeUser model.User
		var beforeToken model.Token
		var beforeChannel model.Channel
		require.NoError(t, db.First(&beforeUser, user.Id).Error)
		require.NoError(t, db.First(&beforeToken, token.Id).Error)
		require.NoError(t, db.First(&beforeChannel, channel.Id).Error)
		assert.Equal(t, 99_980, beforeUser.Quota)
		assert.Equal(t, 99_980, beforeToken.RemainQuota)
		_, err := model.RecoverUsageEventProjections(t.Context(), 100)
		require.NoError(t, err)
		_, err = model.RecoverUsageEventProjections(t.Context(), 100)
		require.NoError(t, err)
		require.NoError(t, db.First(&event, event.ID).Error)
		assert.Equal(t, model.UsageEventProjectionVersion, event.AppliedVersion)
		var projection model.UsageChannel
		require.NoError(t, db.Where("channel_id = ?", channel.Id).Take(&projection).Error)
		assert.EqualValues(t, 1, projection.ConsumeCount)
		assert.EqualValues(t, 20, projection.GrossQuota)
		var hour model.UsageHour
		require.NoError(t, db.Where("user_id = ?", user.Id).Take(&hour).Error)
		assert.Equal(t, model.UsageLiveSourceNamespace(), hour.SourceNamespace)
		assert.Equal(t, model.UsageLiveCollectionEpoch(), hour.CollectionEpoch)
		assert.EqualValues(t, 1, hour.ConsumeCount)
		require.NoError(t, db.First(&user, user.Id).Error)
		require.NoError(t, db.First(&token, token.Id).Error)
		require.NoError(t, db.First(&channel, channel.Id).Error)
		assert.Equal(t, beforeUser, user, "projection recovery must not charge or increment user counters")
		assert.Equal(t, beforeToken, token, "projection recovery must not repeat token settlement")
		assert.Equal(t, beforeChannel, channel, "projection recovery must not repeat channel accounting")
	})
	settings := model_setting.GetGrokSettings()
	oldSettings := *settings
	settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount = true, 0.001
	t.Cleanup(func() { *settings = oldSettings })
	for _, stage := range []string{"success", "funding_error", "token_error"} {
		t.Run("violation_fee/"+stage, func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, OriginModelName: "fee-model", StartTime: time.Now(),
				PriceData: hosttypes.PriceData{GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			observedPending := false
			const callback = "usage_fee_outcome"
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if !observedPending && tx.Statement.Table == "users" {
					var candidate model.UsageEvent
					if err := tx.Session(&gorm.Session{NewDB: true}).Where("user_id = ?", user.Id).Take(&candidate).Error; assert.NoError(t, err) {
						observedPending = candidate.SourceState == model.UsageEventSourcePending && candidate.AppliedVersion == 0
					}
				}
				if stage == "funding_error" && tx.Statement.Table == "users" || stage == "token_error" && tx.Statement.Table == "tokens" {
					_ = tx.AddError(errors.New("forced fee outcome failure"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			apiErr := types.NewError(errors.New("violation fee"), types.ErrorCodeViolationFeeGrokCSAM)
			assert.Equal(t, stage == "success", ChargeViolationFeeIfNeeded(c, info, apiErr))
			assert.True(t, observedPending, "fee candidate must be durable before funding")
			var event model.UsageEvent
			require.NoError(t, db.Where("user_id = ?", user.Id).Take(&event).Error)
			assert.Equal(t, model.UsageEventSourceViolationFee, event.SourceKind)
			assert.Equal(t, int64(500), event.QuotaMagnitude)
			assert.Zero(t, event.PromptTokens)
			assert.Zero(t, event.CompletionTokens)
			if stage == "success" {
				assert.Equal(t, model.UsageEventSourceReady, event.SourceState)
				assert.Equal(t, model.UsageEventProjectionVersion, event.AppliedVersion)
			} else {
				assert.Equal(t, model.UsageEventSourceUnknown, event.SourceState)
				assert.Zero(t, event.AppliedVersion)
				_, err := model.RecoverUsageEventProjections(context.Background(), 10)
				require.NoError(t, err)
				require.NoError(t, db.First(&event, event.ID).Error)
				assert.Equal(t, model.UsageEventSourceUnknown, event.SourceState)
			}
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			if stage == "funding_error" {
				assert.Equal(t, 100_000, user.Quota)
			} else {
				assert.Equal(t, 99_500, user.Quota)
			}
			if stage == "success" {
				assert.Equal(t, 99_500, token.RemainQuota)
			} else {
				assert.Equal(t, 100_000, token.RemainQuota)
			}
		})
	}

	for _, stage := range []string{"success", "funding_error", "token_error"} {
		t.Run("midjourney_refund/"+stage, func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			task := model.Midjourney{UserId: user.Id, TokenId: token.Id, ChannelId: channel.Id,
				BillingChannelId: channel.Id, MjId: "usage-refund", Action: "IMAGINE", Quota: 500}
			require.NoError(t, db.Create(&task).Error)
			t.Cleanup(func() { require.NoError(t, db.Delete(&task).Error) })
			observedPending := false
			const callback = "usage_midjourney_refund_outcome"
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if !observedPending && tx.Statement.Table == "users" {
					var candidate model.UsageEvent
					if err := tx.Session(&gorm.Session{NewDB: true}).Where("user_id = ?", user.Id).Take(&candidate).Error; assert.NoError(t, err) {
						observedPending = candidate.SourceState == model.UsageEventSourcePending && candidate.AppliedVersion == 0
					}
				}
				if stage == "funding_error" && tx.Statement.Table == "users" || stage == "token_error" && tx.Statement.Table == "tokens" {
					_ = tx.AddError(errors.New("forced Midjourney refund failure"))
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			assert.Equal(t, stage != "funding_error", RefundMidjourneyQuota(context.Background(), &task, "test failure"))
			assert.True(t, observedPending, "refund candidate must precede wallet mutation")
			var event model.UsageEvent
			require.NoError(t, db.Where("user_id = ?", user.Id).Take(&event).Error)
			assert.Equal(t, model.UsageEventKindRefund, event.Kind)
			assert.Zero(t, event.Eligibility)
			assert.Equal(t, int64(token.Id), event.TokenID)
			assert.Equal(t, int64(channel.Id), event.ChannelID)
			assert.Equal(t, int64(500), event.QuotaMagnitude)
			if stage == "funding_error" {
				assert.Equal(t, model.UsageEventSourceUnknown, event.SourceState)
				assert.Zero(t, event.AppliedVersion)
			} else {
				assert.Equal(t, model.UsageEventSourceReady, event.SourceState)
				assert.Equal(t, model.UsageEventProjectionVersion, event.AppliedVersion)
				var log model.Log
				require.NoError(t, logDB.Where("user_id = ?", user.Id).Take(&log).Error)
				assert.Equal(t, event.OccurredAt, log.CreatedAt)
				assert.Equal(t, event.RequestID, log.RequestId)
				assert.Contains(t, log.Other, event.EventKey)
				assert.True(t, RefundMidjourneyQuota(context.Background(), &task, "repeat poll"))
				var count int64
				require.NoError(t, db.Model(&model.UsageEvent{}).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.Equal(t, int64(1), count)
			}
			var channelCount int64
			require.NoError(t, db.Model(&model.UsageChannel{}).Where("channel_id = ?", channel.Id).Count(&channelCount).Error)
			assert.Zero(t, channelCount, "refunds never reduce or create gross-consume totals")
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			if stage == "funding_error" {
				assert.Equal(t, 100_000, user.Quota)
			} else {
				assert.Equal(t, 100_500, user.Quota)
			}
			if stage == "success" {
				assert.Equal(t, 100_500, token.RemainQuota)
			} else {
				assert.Equal(t, 100_000, token.RemainQuota)
			}
		})
	}
}

func TestTaskUsageReportsDatabaseMatrix(t *testing.T) {
	runQuotaReportingDatabaseMatrix(t, func(t *testing.T, db, logDB *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&model.Task{}))
		oldLogs, oldExport, oldRedis, oldBatch, oldNotify := common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaRemindEnabled
		common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaRemindEnabled = false, false, false, false, false
		t.Cleanup(func() {
			common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaRemindEnabled = oldLogs, oldExport, oldRedis, oldBatch, oldNotify
		})
		t.Setenv("REQUEST_CONTENT_LOG_ENABLED", "false")
		t.Run("submission_adjustment_refund_and_replay", func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/tasks", nil)
			c.Set("username", "submission-user")
			c.Set("token_name", "submission-token")
			c.Set(common.RequestIdKey, "same-task-request")
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, OriginModelName: "task-model", UsingGroup: "default",
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, PriceData: hosttypes.PriceData{Quota: 300}}
			task := &model.Task{TaskID: "stats-" + token.Key, UserId: user.Id, ChannelId: channel.Id, Quota: 300,
				Status: model.TaskStatusInProgress, Group: "default", Properties: model.Properties{OriginModelName: "task-model"},
				PrivateData: model.TaskPrivateData{TokenId: token.Id, BillingSource: BillingSourceWallet}}
			t.Cleanup(func() { require.NoError(t, db.Delete(task).Error) })
			PrepareTaskSubmissionUsageReport(c, info, task)
			require.NotNil(t, task.UsageReport)
			uid := task.UsageReport.Input.Identity.OccurrenceUID
			require.NoError(t, model.InsertTaskWithRequestContent(t.Context(), task, nil))
			var event model.UsageEvent
			require.NoError(t, db.Where("event_key = ?", task.UsageReport.Input.EventKey).Take(&event).Error)
			assert.Equal(t, model.UsageEventSourcePending, event.SourceState)
			var stored model.Task
			require.NoError(t, db.First(&stored, task.ID).Error)
			assert.Equal(t, event.EventKey, stored.PrivateData.UsageSubmissionKey)
			LogTaskConsumption(c, info, task)
			require.NoError(t, db.First(&event, event.ID).Error)
			assert.Equal(t, uid, event.OccurrenceUID)
			assert.Equal(t, model.UsageEventSourceReady, event.SourceState)
			assert.Equal(t, model.UsageEventProjectionVersion, event.AppliedVersion)
			RecalculateTaskQuota(t.Context(), task, 500, common.NewMessage("Adjusted by the channel adaptor"))
			assert.Equal(t, 500, task.Quota)
			require.True(t, RefundTaskQuota(t.Context(), task, "task failure"))
			require.True(t, RefundTaskQuota(t.Context(), task, "duplicate poll"))
			var events []model.UsageEvent
			require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&events).Error)
			require.Len(t, events, 3)
			assert.Equal(t, model.UsageEventSourceTaskSubmission, events[0].SourceKind)
			assert.Equal(t, model.UsageEventKindConsume, events[1].Kind)
			assert.EqualValues(t, 200, events[1].QuotaMagnitude)
			assert.EqualValues(t, 1, events[1].TaskOperationSequence)
			assert.Equal(t, model.UsageEventKindRefund, events[2].Kind)
			assert.EqualValues(t, 500, events[2].QuotaMagnitude)
			assert.EqualValues(t, 2, events[2].TaskOperationSequence)
			var projection model.UsageChannel
			require.NoError(t, db.Where("channel_id = ?", channel.Id).Take(&projection).Error)
			assert.EqualValues(t, 2, projection.ConsumeCount)
			assert.EqualValues(t, 500, projection.GrossQuota)
			var logs int64
			require.NoError(t, logDB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&logs).Error)
			assert.Zero(t, logs, "consume capture does not require optional logs")
			for _, item := range events {
				receipt := model.UsageEventReceipt{EventKey: item.EventKey, Identity: model.UsageEventIdentity{
					SourceNamespace: item.SourceNamespace, SourceKind: item.SourceKind, OccurrenceUID: item.OccurrenceUID,
					TaskPrimaryID: item.TaskPrimaryID, TaskOperationSequence: item.TaskOperationSequence}, PayloadDigest: item.PayloadDigest}
				require.NoError(t, model.ApplyUsageEvent(t.Context(), receipt))
			}
			require.NoError(t, db.Where("channel_id = ?", channel.Id).Take(&projection).Error)
			assert.EqualValues(t, 2, projection.ConsumeCount)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, 100_300, user.Quota, "only the existing +200/-500 adjustment money ran; submission fixture reports an already-settled charge")
			assert.Equal(t, 100_300, token.RemainQuota)
		})
		t.Run("submission_restore_reuses_key_without_reconstructing_or_confirming_outcome", func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/tasks", nil)
			c.Set("username", "frozen-submitter")
			c.Set("token_name", "frozen-token")
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, OriginModelName: "submission-model", UsingGroup: "default",
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id}, PriceData: hosttypes.PriceData{Quota: 999}}
			task := &model.Task{TaskID: "restore-" + token.Key, UserId: user.Id, ChannelId: channel.Id, Quota: 300,
				Status: model.TaskStatusInProgress, PrivateData: model.TaskPrivateData{TokenId: token.Id}}
			PrepareTaskSubmissionUsageReport(c, info, task)
			require.NotNil(t, task.UsageReport)
			assert.EqualValues(t, 300, task.UsageReport.Input.QuotaMagnitude, "the accepted task amount, not a stale estimate, is frozen")
			require.NoError(t, model.InsertTaskWithRequestContent(t.Context(), task, nil))
			t.Cleanup(func() { require.NoError(t, db.Delete(task).Error) })
			frozen := task.UsageReport.Input
			var restored model.Task
			require.NoError(t, db.First(&restored, task.ID).Error)
			require.Nil(t, restored.UsageReport)
			c.Set("username", "renamed-submitter")
			c.Set("token_name", "renamed-token")
			PrepareTaskSubmissionUsageReport(c, info, &restored)
			require.NotNil(t, restored.UsageReport)
			assert.Equal(t, frozen, restored.UsageReport.Input)
			var event model.UsageEvent
			require.NoError(t, db.Where("event_key = ?", frozen.EventKey).Take(&event).Error)
			assert.Equal(t, model.UsageEventSourcePending, event.SourceState, "restoring a task is not source confirmation")
			_, err := model.RecoverUsageEventProjections(t.Context(), 100)
			require.NoError(t, err)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, 100_000, user.Quota)
			assert.Equal(t, 100_000, token.RemainQuota)
			missing := restored
			missing.UsageReport = nil
			missing.PrivateData.UsageSubmissionKey = fmt.Sprintf("%064x", 1)
			PrepareTaskSubmissionUsageReport(c, info, &missing)
			assert.Nil(t, missing.UsageReport, "an unavailable saved occurrence must not allocate a replacement UID")
			var count int64
			require.NoError(t, db.Model(&model.UsageEvent{}).Where("user_id = ?", user.Id).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
		t.Run("terminal_cas_loser_never_captures_or_redebits", func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			task := &model.Task{TaskID: "cas-" + token.Key, UserId: user.Id, ChannelId: channel.Id, Quota: 300,
				Status: model.TaskStatusInProgress, Group: "default", Properties: model.Properties{OriginModelName: "task-model"},
				PrivateData: model.TaskPrivateData{TokenId: token.Id, BillingSource: BillingSourceWallet}}
			require.NoError(t, db.Create(task).Error)
			t.Cleanup(func() { require.NoError(t, db.Delete(task).Error) })
			loser := *task
			task.Status, loser.Status = model.TaskStatusFailure, model.TaskStatusFailure
			won, err := commitTaskTerminal(t.Context(), task, model.TaskStatusInProgress, nil, nil, true)
			require.NoError(t, err)
			require.True(t, won)
			won, err = commitTaskTerminal(t.Context(), &loser, model.TaskStatusInProgress, nil, nil, true)
			require.NoError(t, err)
			assert.False(t, won)
			var events []model.UsageEvent
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&events).Error)
			require.Len(t, events, 1)
			assert.Equal(t, model.UsageEventSourcePending, events[0].SourceState)
			require.True(t, RefundTaskQuota(t.Context(), task, "terminal failure"))
			require.False(t, RefundTaskQuota(t.Context(), &loser, "stale terminal poll"))
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, 100_300, user.Quota)
			assert.Equal(t, 100_300, token.RemainQuota)
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&events).Error)
			require.Len(t, events, 1)
			assert.Equal(t, model.UsageEventSourceReady, events[0].SourceState)
		})
		t.Run("unknown_adjustment_is_not_a_finance_retry", func(t *testing.T) {
			user, token, channel := seedUsageReportingAccount(t, db, logDB)
			task := &model.Task{TaskID: "unknown-" + token.Key, UserId: user.Id, ChannelId: channel.Id, Quota: 300,
				Status: model.TaskStatusFailure, Group: "default", Properties: model.Properties{OriginModelName: "task-model"},
				PrivateData: model.TaskPrivateData{TokenId: token.Id, BillingSource: BillingSourceSubscription, SubscriptionId: 999999}}
			require.NoError(t, db.Create(task).Error)
			t.Cleanup(func() { require.NoError(t, db.Delete(task).Error) })
			require.False(t, RefundTaskQuota(t.Context(), task, "funding failed"))
			var events []model.UsageEvent
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&events).Error)
			require.Len(t, events, 1)
			assert.Equal(t, model.UsageEventSourceUnknown, events[0].SourceState)
			task.PrivateData.BillingSource = BillingSourceWallet
			require.False(t, RefundTaskQuota(t.Context(), task, "must not retry an unknown debit"))
			_, err := model.RecoverUsageEventProjections(t.Context(), 100)
			require.NoError(t, err)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, 100_000, user.Quota)
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&events).Error)
			require.Len(t, events, 1)
			assert.Zero(t, events[0].AppliedVersion)
		})
	})
}

func seedUsageReportingAccount(t *testing.T, db, logDB *gorm.DB) (model.User, model.Token, model.Channel) {
	t.Helper()
	uid, err := model.NewUsageOccurrenceUID()
	require.NoError(t, err)
	user := model.User{Username: "usage_" + uid[:16], Quota: 100_000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "usage-report-" + uid, Name: "usage-token", RemainQuota: 100_000, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "usage-report", Key: "unused", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	t.Cleanup(func() {
		require.NoError(t, logDB.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
		require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.UsageEvent{}).Error)
		require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.UsageHour{}).Error)
		require.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.UsageChannelModel{}).Error)
		require.NoError(t, db.Where("channel_id = ?", channel.Id).Delete(&model.UsageChannel{}).Error)
		require.NoError(t, db.Unscoped().Delete(&token).Error)
		require.NoError(t, db.Unscoped().Delete(&user).Error)
		require.NoError(t, db.Unscoped().Delete(&channel).Error)
	})
	return user, token, channel
}

// A request whose upstream returns no usage is logged without a charge. The
// reason is stored as content_parts, which the web console renders in the
// viewer's language; content keeps the English text.
func TestPostTextConsumeQuotaLogsMissingUsageAsStructuredContent(t *testing.T) {
	truncate(t)
	const userID, channelID = 62, 62
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		OriginModelName: "missing-usage-model",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData:       hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}

	PostTextConsumeQuota(ctx, info, &dto.Usage{}, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Zero(t, log.Quota)
	assert.Equal(t, "Upstream returned no usage, so nothing was charged (possibly an upstream timeout)", log.Content)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.Equal(t, []any{
		map[string]any{"key": "Upstream returned no usage, so nothing was charged (possibly an upstream timeout)"},
	}, other["content_parts"])
}
