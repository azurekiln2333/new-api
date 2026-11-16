package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	appdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelCapacityLocalLifecycle(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	settings := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(2)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, apiErr := AcquireChannelCapacity(ctx, 71001, settings)
	require.Nil(t, apiErr)
	defer first.Release()
	// A new settings value (e.g. another key or a cache refresh) still uses
	// the channel ID's count rather than a pointer to a cached Channel.
	second, apiErr := AcquireChannelCapacity(context.Background(), 71001, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(2)})
	require.Nil(t, apiErr)
	defer second.Release()
	_, apiErr = AcquireChannelCapacity(context.Background(), 71001, settings)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.False(t, IsChannelFailure(apiErr))

	otherChannel, apiErr := AcquireChannelCapacity(context.Background(), 71002, settings)
	require.Nil(t, apiErr)
	otherChannel.Release()
	cancel()
	assert.ErrorIs(t, first.Context().Err(), context.Canceled)
	_, apiErr = AcquireChannelCapacity(context.Background(), 71001, settings)
	require.NotNil(t, apiErr, "cancellation holds the slot until upstream cleanup returns")
	first.Release()
	first.Release()
	replacement, apiErr := AcquireChannelCapacity(context.Background(), 71001, settings)
	require.Nil(t, apiErr)
	defer replacement.Release()
	first.Release()
	_, apiErr = AcquireChannelCapacity(context.Background(), 71001, settings)
	require.NotNil(t, apiErr, "repeated old release cannot free a successor's slot")

	_, apiErr = AcquireChannelCapacity(context.Background(), 71001, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
	require.NotNil(t, apiErr, "lowering a limit blocks new admissions")
	assert.NoError(t, second.Context().Err(), "a lower limit does not terminate existing attempts")
	second.Release()
	replacement.Release()
	assert.Panics(t, func() {
		lease, acquireErr := AcquireChannelCapacity(context.Background(), 71001, settings)
		require.Nil(t, acquireErr)
		defer lease.Release()
		panic("upstream handler panic")
	})
	for range 2 {
		lease, acquireErr := AcquireChannelCapacity(context.Background(), 71001, settings)
		require.Nil(t, acquireErr)
		defer lease.Release()
	}
}

func TestChannelCapacityCoordinationFailureAndHealth(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis, previousClient, previousDisable := common.RedisEnabled, common.RDB, common.AutomaticDisableChannelEnabled
	common.RedisEnabled, common.RDB, common.AutomaticDisableChannelEnabled = true, nil, true
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB, common.AutomaticDisableChannelEnabled = previousRedis, previousClient, previousDisable
	})
	for _, unlimited := range []*int{nil, common.GetPointer(0)} {
		lease, err := AcquireChannelCapacity(context.Background(), 71003, dto.ChannelSettings{MaxConcurrentRequests: unlimited})
		require.Nil(t, err)
		lease.Release()
	}
	_, apiErr := AcquireChannelCapacity(context.Background(), 71003, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(2)})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeChannelCapacityUnavailable, apiErr.GetErrorCode())
	for _, code := range []types.ErrorCode{types.ErrorCodeChannelCapacitySaturated, types.ErrorCodeChannelCapacityUnavailable} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set("channel_id", 71003)
		c.Set("auto_ban", true)
		capacityErr := channelCapacityError(code)
		assert.False(t, IsChannelFailure(capacityErr))
		assert.False(t, ShouldDisableChannel(capacityErr))
		assert.Equal(t, "stop", DecideRelayRetry(c, capacityErr, 2).Action)
		// No database/log fixture: entering the upstream error path would be
		// observable here; a local capacity rejection must return immediately.
		ProcessChannelError(c, types.ChannelError{ChannelId: 71003, AutoBan: true}, capacityErr, nil)
		assert.Empty(t, RequestPolicy(c).FailedChannelIDs)
	}
}

func TestChannelCapacityDetachedSubmissionKeepsLeaseCancellation(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	clientContext, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()
	settings := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}
	lease, apiErr := AcquireChannelCapacity(clientContext, 71006, settings)
	require.Nil(t, apiErr)
	defer lease.Release()
	detached, releaseContext := WithChannelCapacityCancellation(context.WithoutCancel(lease.Context()))
	defer releaseContext()
	cancelClient()
	assert.ErrorIs(t, lease.Context().Err(), context.Canceled)
	assert.NoError(t, detached.Err(), "native submission can finish after the client disconnects")
	assert.NoError(t, lease.control.Err(), "renewal continues until upstream cleanup")
	_, apiErr = AcquireChannelCapacity(context.Background(), 71006, settings)
	require.NotNil(t, apiErr, "detached work continues to occupy its slot")
	lease.cancel(channelCapacityError(types.ErrorCodeChannelCapacityUnavailable))
	select {
	case <-detached.Done():
	case <-time.After(time.Second):
		t.Fatal("detached upstream work did not stop after losing its capacity lease")
	}
	var cause *types.NewAPIError
	require.ErrorAs(t, context.Cause(detached), &cause)
	assert.Equal(t, types.ErrorCodeChannelCapacityUnavailable, cause.GetErrorCode())
	require.NotNil(t, lease.Err(), "coordination loss remains visible after client cancellation")
	lease.Release()
	successor, apiErr := AcquireChannelCapacity(context.Background(), 71006, settings)
	require.Nil(t, apiErr)
	successor.Release()
	unlimited, apiErr := AcquireChannelCapacity(context.WithoutCancel(lease.Context()), 71007, dto.ChannelSettings{})
	require.Nil(t, apiErr)
	unlimitedContext, releaseUnlimited := WithChannelCapacityCancellation(context.WithoutCancel(unlimited.Context()))
	defer releaseUnlimited()
	assert.NoError(t, unlimitedContext.Err(), "an unlimited fallback must not inherit the released previous channel's lease")
	unlimited.Release()
}

func TestChannelCapacityAdmissionRestrictions(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, tc := range []struct {
		name  string
		setup func(*gin.Context)
		want  bool
	}{
		{name: "ordinary busy candidate", want: true},
		{name: "same channel task pin", setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(appdto.ChannelPin{ChannelId: 71004, Source: appdto.PinSourceOriginTask, Rank: appdto.PinRankOriginTask, RetryMode: appdto.PinRetrySameChannel})
		}},
		{name: "single attempt token pin", setup: func(c *gin.Context) {
			GetChannelConstraints(c).AddPin(appdto.ChannelPin{ChannelId: 71004, Source: appdto.PinSourceToken, Rank: appdto.PinRankToken, RetryMode: appdto.PinRetrySingleAttempt})
		}},
		{name: "strict session", setup: func(c *gin.Context) { RequestPolicy(c).SessionMode = "strict" }},
		{name: "response started", setup: func(c *gin.Context) { c.Writer.WriteHeaderNow() }},
		{name: "client cancelled", setup: func(c *gin.Context) {
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(ctx)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if tc.setup != nil {
				tc.setup(c)
			}
			assert.Equal(t, tc.want, RetryChannelAdmission(c, 71004, channelCapacityError(types.ErrorCodeChannelCapacitySaturated), nil))
			assert.False(t, RetryChannelAdmission(c, 71004, channelCapacityError(types.ErrorCodeChannelCapacitySaturated), nil), "a candidate is not traversed twice")
			assert.Empty(t, RequestPolicy(c).FailedChannelIDs)
			assert.False(t, RetryChannelAdmission(c, 71005, channelCapacityError(types.ErrorCodeChannelCapacityUnavailable), nil))
		})
	}
}

func TestChannelCapacityRedis(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR is required for real Redis capacity verification")
	}
	require.NoError(t, i18n.Init())
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	peer := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err(), "configured Redis fixture must be available")
	serverInfo, err := client.Info(ctx, "server").Result()
	require.NoError(t, err)
	for line := range strings.SplitSeq(serverInfo, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			t.Log(strings.TrimSpace(line))
		}
	}
	channelID := int(time.Now().UnixNano())
	t.Run("independent clients and owner release", func(t *testing.T) {
		first, apiErr := acquireRedisChannelCapacity(ctx, client, channelID, 2)
		require.Nil(t, apiErr)
		defer first.Release()
		second, apiErr := acquireRedisChannelCapacity(ctx, peer, channelID, 2)
		require.Nil(t, apiErr)
		defer second.Release()
		_, apiErr = acquireRedisChannelCapacity(ctx, peer, channelID, 2)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
		first.Release()
		replacement, apiErr := acquireRedisChannelCapacity(ctx, peer, channelID, 2)
		require.Nil(t, apiErr)
		defer replacement.Release()
		first.Release()
		_, apiErr = acquireRedisChannelCapacity(ctx, client, channelID, 2)
		require.NotNil(t, apiErr)
	})
	t.Run("renewal expiry and stale owner", func(t *testing.T) {
		clientContext, cancelClient := context.WithCancel(ctx)
		defer cancelClient()
		lease, apiErr := acquireRedisChannelCapacity(clientContext, client, channelID, 1)
		require.Nil(t, apiErr)
		defer lease.Release()
		cancelClient()
		now, err := client.Time(ctx).Result()
		require.NoError(t, err)
		require.NoError(t, client.ZAdd(ctx, lease.key, &redis.Z{Score: float64(now.Add(10 * time.Second).UnixMilli()), Member: lease.owner}).Err())
		require.True(t, lease.renew(), "client cancellation does not release a still-running detached submission")
		renewed, err := client.ZScore(ctx, lease.key, lease.owner).Result()
		require.NoError(t, err)
		assert.Greater(t, renewed, float64(now.Add(10*time.Second).UnixMilli()))
		// Expire just this owner deterministically without sleeping.
		require.NoError(t, client.ZAdd(ctx, lease.key, &redis.Z{Score: 0, Member: lease.owner}).Err())
		successor, apiErr := acquireRedisChannelCapacity(ctx, peer, channelID, 1)
		require.Nil(t, apiErr)
		defer successor.Release()
		assert.False(t, lease.renew(), "an expired owner cannot resurrect its lease")
		require.Error(t, lease.Context().Err())
		require.NotNil(t, lease.Err())
		assert.Equal(t, types.ErrorCodeChannelCapacityUnavailable, lease.Err().GetErrorCode())
		lease.Release()
		_, apiErr = acquireRedisChannelCapacity(ctx, client, channelID, 1)
		require.NotNil(t, apiErr, "stale owner's cleanup cannot release the successor")
		assert.NoError(t, successor.Context().Err())
	})
	t.Run("coordinator loss cancels and fails closed", func(t *testing.T) {
		isolated := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
		lease, apiErr := acquireRedisChannelCapacity(ctx, isolated, channelID, 1)
		require.Nil(t, apiErr)
		defer lease.Release()
		t.Cleanup(func() { require.NoError(t, client.Del(ctx, lease.key).Err()) })
		require.NoError(t, isolated.Close())
		assert.False(t, lease.renew())
		assert.True(t, errors.Is(lease.Context().Err(), context.Canceled))
		require.NotNil(t, lease.Err())
		_, apiErr = acquireRedisChannelCapacity(ctx, isolated, channelID+1, 1)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
	})
}
