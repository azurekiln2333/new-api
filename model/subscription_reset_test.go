package model

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedSubscriptionResetPlan(t *testing.T, plan *SubscriptionPlan) {
	t.Helper()
	require.NoError(t, DB.Create(plan).Error)
}

func seedSubscriptionResetSub(t *testing.T, sub *UserSubscription) {
	t.Helper()
	require.NoError(t, DB.Create(sub).Error)
}

func getSubscriptionResetSub(t *testing.T, id int) UserSubscription {
	t.Helper()
	var sub UserSubscription
	require.NoError(t, DB.Where("id = ?", id).First(&sub).Error)
	return sub
}

func TestAdminResetUserSubscriptionsByPlanResetsAllActiveMatchesAndAdvancesTime(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9101,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	otherPlan := &SubscriptionPlan{
		Id:               9102,
		Title:            "Basic",
		PriceAmount:      1,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetPlan(t, otherPlan)

	activeEnd := now + 30*24*3600
	expiredEnd := now - 1
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9201, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 300, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9202, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9203, UserId: 101, PlanId: otherPlan.Id, AmountTotal: 100, AmountUsed: 60, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9204, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 700, StartTime: now - 7200, EndTime: expiredEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9205, UserId: 102, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 800, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9206, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 900, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 120})

	beforeReset := GetDBTimestamp()
	result, err := AdminResetUserSubscriptionsByPlan(101, plan.Id, true)
	afterReset := GetDBTimestamp()

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, plan.Id, result.PlanId)
	assert.Equal(t, 2, result.MatchedCount)
	assert.Equal(t, 2, result.ResetCount)
	assert.Equal(t, 1, result.UserCount)
	assert.Equal(t, []int{101}, result.AffectedUserIds)
	assert.True(t, result.AdvanceResetTime)

	for _, id := range []int{9201, 9202} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.GreaterOrEqual(t, sub.LastResetTime, beforeReset)
		assert.LessOrEqual(t, sub.LastResetTime, afterReset)
		assert.Equal(t, calcNextResetTime(time.Unix(sub.LastResetTime, 0), plan, sub.EndTime), sub.NextResetTime)
	}
	assert.EqualValues(t, 60, getSubscriptionResetSub(t, 9203).AmountUsed)
	assert.EqualValues(t, 700, getSubscriptionResetSub(t, 9204).AmountUsed)
	assert.EqualValues(t, 800, getSubscriptionResetSub(t, 9205).AmountUsed)
	assert.EqualValues(t, 900, getSubscriptionResetSub(t, 9206).AmountUsed)
}

func TestAdminResetUserSubscriptionsByPlanKeepsResetTimes(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9301,
		Title:            "Team",
		PriceAmount:      20,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      2000,
		QuotaResetPeriod: SubscriptionResetMonthly,
	}
	seedSubscriptionResetPlan(t, plan)

	lastReset := now - 86400
	nextReset := now + 86400
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9302, UserId: 201, PlanId: plan.Id, AmountTotal: 2000, AmountUsed: 1200, StartTime: now - 172800, EndTime: now + 30*24*3600, Status: "active", LastResetTime: lastReset, NextResetTime: nextReset})

	result, err := AdminResetUserSubscriptionsByPlan(201, plan.Id, false)

	require.NoError(t, err)
	assert.False(t, result.AdvanceResetTime)
	sub := getSubscriptionResetSub(t, 9302)
	assert.Zero(t, sub.AmountUsed)
	assert.Equal(t, lastReset, sub.LastResetTime)
	assert.Equal(t, nextReset, sub.NextResetTime)
}

func TestAdminResetUserSubscriptionsByPlanNoActiveMatchReturnsError(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            9401,
		Title:         "Expired",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9402, UserId: 301, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 7200, EndTime: now - 1, Status: "active"})

	result, err := AdminResetUserSubscriptionsByPlan(301, plan.Id, true)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, strings.Contains(err.Error(), "This user has no active subscription to this plan"))
}

func TestAdminResetPlanSubscriptionsResetsAllActiveUsers(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9501,
		Title:            "Business",
		PriceAmount:      30,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      3000,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	seedSubscriptionResetPlan(t, plan)

	activeEnd := now + 30*24*3600
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9502, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1000, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9503, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1100, StartTime: now - 3500, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9504, UserId: 402, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1200, StartTime: now - 3400, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9505, UserId: 403, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1300, StartTime: now - 7200, EndTime: now - 1, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9506, UserId: 404, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1400, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 10})

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.MatchedCount)
	assert.Equal(t, 3, result.ResetCount)
	assert.Equal(t, 2, result.UserCount)
	assert.Equal(t, []int{401, 402}, result.AffectedUserIds)
	for _, id := range []int{9502, 9503, 9504} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.Zero(t, sub.LastResetTime)
		assert.Zero(t, sub.NextResetTime)
	}
	assert.EqualValues(t, 1300, getSubscriptionResetSub(t, 9505).AmountUsed)
	assert.EqualValues(t, 1400, getSubscriptionResetSub(t, 9506).AmountUsed)
}

func TestSubscriptionBalanceChargedQuota(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    int
		valid   bool
	}{
		{payload: "charged_quota=0", valid: true},
		{payload: " charged_quota=120 \n", want: 120, valid: true},
		{payload: fmt.Sprintf("charged_quota=%d", common.MaxWalletQuota), want: common.MaxWalletQuota, valid: true},
		{payload: ""},
		{payload: "120"},
		{payload: "charged_quota="},
		{payload: "charged_quota=-1"},
		{payload: "charged_quota=1.5"},
		{payload: "charged_quota=1e3"},
		{payload: "charged_quota= 120"},
		{payload: "charged_quota=120;refund=1"},
		{payload: fmt.Sprintf("charged_quota=%d", int64(common.MaxWalletQuota)+1)},
		{payload: "charged_quota=18446744073709551615"},
	} {
		t.Run(tc.payload, func(t *testing.T) {
			got, valid := parseSubscriptionBalanceChargedQuota(tc.payload)
			assert.Equal(t, tc.valid, valid)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSubscriptionBalanceUsedQuotaDatabaseMatrix(t *testing.T) {
	for _, engine := range []struct{ kind, env string }{
		{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"},
	} {
		t.Run(engine.kind, func(t *testing.T) {
			dsn := os.Getenv(engine.env)
			if engine.env != "" && dsn == "" {
				t.Skip("set " + engine.env + " to run this database")
			}
			db := openRequestLogStore(t, engine.kind, dsn)
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(engine.kind))
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
			})
			require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}))
			versionQuery := "SELECT version()"
			if engine.kind == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("subscription balance store: %s %s", engine.kind, version)

			for i, row := range []struct {
				userID                   int
				method, provider, status string
				payload                  string
			}{
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "charged_quota=120"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "charged_quota=30"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "charged_quota=0"},
				{2, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, fmt.Sprintf("charged_quota=%d", common.MaxWalletQuota)},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "legacy payload"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "charged_quota=-1"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, fmt.Sprintf("charged_quota=%d", int64(common.MaxWalletQuota)+1)},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusPending, "charged_quota=999"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusExpired, "charged_quota=999"},
				{1, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusFailed, "charged_quota=999"},
				{1, "card", PaymentProviderBalance, common.TopUpStatusSuccess, "charged_quota=999"},
				{1, PaymentMethodBalance, "stripe", common.TopUpStatusSuccess, "charged_quota=999"},
				{99, PaymentMethodBalance, PaymentProviderBalance, common.TopUpStatusSuccess, "outside page"},
			} {
				require.NoError(t, db.Create(&SubscriptionOrder{
					UserId: row.userID, TradeNo: fmt.Sprintf("balance-projection-%d", i), Money: 99999,
					PaymentMethod: row.method, PaymentProvider: row.provider, Status: row.status, ProviderPayload: row.payload,
				}).Error)
			}

			used, malformed, err := GetSubscriptionBalanceUsedQuota(t.Context(), []int{1, 2, 3, 1, 0, -1})
			require.NoError(t, err)
			assert.Equal(t, map[int]int64{1: 150, 2: int64(common.MaxWalletQuota)}, used)
			assert.Equal(t, 3, malformed, "only malformed successful balance orders for listed users are counted")

			first := &User{Id: 1, Username: "first", Quota: 700, UsedQuota: 91}
			second := &User{Id: 2, Username: "second", Quota: 800, UsedQuota: 92}
			third := &User{Id: 3, Username: "no-orders", Quota: 900, UsedQuota: 93}
			items, malformed, err := BuildUserListItems(t.Context(), []*User{second, nil, first, third})
			require.NoError(t, err)
			require.Len(t, items, 3)
			assert.Equal(t, 3, malformed)
			encoded, err := common.Marshal(items)
			require.NoError(t, err)
			var projected []struct {
				ID                           int   `json:"id"`
				Quota                        int   `json:"quota"`
				UsedQuota                    int   `json:"used_quota"`
				SubscriptionBalanceUsedQuota int64 `json:"subscription_balance_used_quota"`
			}
			require.NoError(t, common.Unmarshal(encoded, &projected))
			require.Len(t, projected, 3)
			assert.Equal(t, []int{2, 1, 3}, []int{projected[0].ID, projected[1].ID, projected[2].ID})
			assert.Equal(t, []int64{int64(common.MaxWalletQuota), 150, 0}, []int64{projected[0].SubscriptionBalanceUsedQuota, projected[1].SubscriptionBalanceUsedQuota, projected[2].SubscriptionBalanceUsedQuota})
			assert.Equal(t, 700, projected[1].Quota, "historical subscription spending is not deducted again")
			assert.Equal(t, 91, projected[1].UsedQuota, "subscription spending stays separate from inference usage")
			assert.Equal(t, 700, first.Quota)
			assert.Equal(t, 91, first.UsedQuota)

			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			_, _, err = BuildUserListItems(cancelled, []*User{first})
			assert.ErrorIs(t, err, context.Canceled, "request cancellation reaches the aggregate query")
			empty, malformed, err := GetSubscriptionBalanceUsedQuota(cancelled, []int{0, -1})
			require.NoError(t, err, "an empty page does not query the database")
			assert.Empty(t, empty)
			assert.Zero(t, malformed)
			noItems, _, err := BuildUserListItems(cancelled, nil)
			require.NoError(t, err)
			assert.Empty(t, noItems)
		})
	}
}

func TestAdminResetPlanSubscriptionsNoMatchSucceeds(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Id:            9601,
		Title:         "Empty",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Zero(t, result.MatchedCount)
	assert.Zero(t, result.ResetCount)
	assert.Zero(t, result.UserCount)
	assert.Empty(t, result.AffectedUserIds)
}
