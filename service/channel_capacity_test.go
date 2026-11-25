package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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
		for _, code := range []types.ErrorCode{
			types.ErrorCodeChannelCapacitySaturated,
			types.ErrorCodeChannelCapacityUnavailable,
			types.ErrorCodeChannelCapacityQueueFull,
			types.ErrorCodeChannelCapacityWaitTimeout,
		} {
			t.Run(tc.name+"/"+string(code), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				if tc.setup != nil {
					tc.setup(c)
				}
				retry := &RetryParam{Ctx: c, Retry: common.GetPointer(2)}
				assert.Equal(t, tc.want, RetryChannelAdmission(c, 71004, channelCapacityError(code), retry))
				assert.False(t, RetryChannelAdmission(c, 71004, channelCapacityError(code), retry), "a candidate is not traversed twice")
				assert.Equal(t, 2, retry.GetRetry(), "local admission leaves the upstream retry budget untouched")
				assert.Empty(t, RequestPolicy(c).FailedChannelIDs)
				if tc.want {
					assert.Contains(t, RequestPolicy(c).ChannelSelectionExclusions(), 71004)
				} else {
					assert.Empty(t, RequestPolicy(c).CapacityRejectedChannelIDs)
				}
			})
		}
	}
}

func TestChannelCapacityUnlimitedCountsInLocalMode(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	channelID := 71010
	for _, unlimited := range []*int{nil, common.GetPointer(0)} {
		settings := dto.ChannelSettings{MaxConcurrentRequests: unlimited}
		first, apiErr := AcquireChannelCapacity(context.Background(), channelID, settings)
		require.Nil(t, apiErr)
		second, apiErr := AcquireChannelCapacity(context.Background(), channelID, settings)
		require.Nil(t, apiErr, "zero or unset never rejects")
		samples := ReadChannelConcurrency(context.Background(), []int{channelID, channelID + 1})
		require.NotNil(t, samples[channelID].Current)
		assert.Equal(t, 2, *samples[channelID].Current, "unlimited attempts are counted, not dropped")
		assert.Equal(t, ChannelConcurrencyScopeInstance, samples[channelID].Scope)
		assert.False(t, samples[channelID].SampleTime.IsZero())
		require.NotNil(t, samples[channelID+1].Current)
		assert.Equal(t, 0, *samples[channelID+1].Current, "an idle channel is zero, never unavailable")
		first.Release()
		first.Release()
		samples = ReadChannelConcurrency(context.Background(), []int{channelID})
		require.NotNil(t, samples[channelID].Current)
		assert.Equal(t, 1, *samples[channelID].Current, "release is idempotent")
		second.Release()
		samples = ReadChannelConcurrency(context.Background(), []int{channelID})
		require.NotNil(t, samples[channelID].Current)
		assert.Equal(t, 0, *samples[channelID].Current)
	}
}

func TestChannelCapacityUnlimitedTelemetryLossNeverRejects(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
	channelID := 71011
	lease, apiErr := AcquireChannelCapacity(context.Background(), channelID, dto.ChannelSettings{})
	require.Nil(t, apiErr, "an unreachable coordinator may not reject unlimited traffic")
	assert.True(t, hasLostChannelCapacityOwner(channelID), "a live attempt without a coordinator stays marked until release")
	samples := ReadChannelConcurrency(context.Background(), []int{channelID})
	assert.Nil(t, samples[channelID].Current, "a cluster count that omits a live owner is unavailable, not zero")
	assert.Equal(t, ChannelConcurrencyScopeCluster, samples[channelID].Scope)
	lease.Release()
	assert.False(t, hasLostChannelCapacityOwner(channelID), "release clears the local lost-owner record")
	samples = ReadChannelConcurrency(context.Background(), []int{channelID})
	assert.Nil(t, samples[channelID].Current, "without a coordinator the cluster sample stays unavailable")
}

func TestChannelCapacityUnlimitedDetachedContinuationKeepsCounting(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	channelID := 71012
	lease, apiErr := AcquireChannelCapacity(context.Background(), channelID, dto.ChannelSettings{})
	require.Nil(t, apiErr)
	// A detached native submission continues after the client leaves.
	detached, releaseDetached := WithChannelCapacityCancellation(context.WithoutCancel(lease.Context()))
	assert.NoError(t, detached.Err(), "an unlimited attempt has no coordination loss to propagate")
	lease.Release()
	lease.Release()
	samples := ReadChannelConcurrency(context.Background(), []int{channelID})
	require.NotNil(t, samples[channelID].Current)
	assert.Equal(t, 1, *samples[channelID].Current, "a repeated base release cannot drain work a detached continuation still holds")
	releaseDetached()
	releaseDetached()
	samples = ReadChannelConcurrency(context.Background(), []int{channelID})
	require.NotNil(t, samples[channelID].Current)
	assert.Equal(t, 0, *samples[channelID].Current, "the last holder drains the count exactly once")

	// A stale detached context from a drained attempt must not resurrect it.
	released, apiErr := AcquireChannelCapacity(context.Background(), channelID, dto.ChannelSettings{})
	require.Nil(t, apiErr)
	released.Release()
	_, releaseStale := WithChannelCapacityCancellation(context.WithoutCancel(released.Context()))
	releaseStale()
	samples = ReadChannelConcurrency(context.Background(), []int{channelID})
	require.NotNil(t, samples[channelID].Current)
	assert.Equal(t, 0, *samples[channelID].Current)

	// A limited attempt inheriting a drained unlimited context must not retain
	// that old owner nor fail its own lifecycle.
	limitedChannel := channelID + 1
	limited, apiErr := AcquireChannelCapacity(context.WithoutCancel(lease.Context()), limitedChannel, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
	require.Nil(t, apiErr)
	limitedContext, releaseLimited := WithChannelCapacityCancellation(context.WithoutCancel(limited.Context()))
	assert.NoError(t, limitedContext.Err())
	releaseLimited()
	limited.Release()
	samples = ReadChannelConcurrency(context.Background(), []int{limitedChannel})
	require.NotNil(t, samples[limitedChannel].Current)
	assert.Equal(t, 0, *samples[limitedChannel].Current)
}

// capacityRedisFaultHook injects a lost command or lost reply without timing
// sleeps. Real Redis still owns every successful slot and renewal.
type capacityRedisFaultHook struct {
	before atomic.Bool
	after  atomic.Bool
}

func (h *capacityRedisFaultHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if h.before.Load() && (cmd.Name() == "eval" || cmd.Name() == "evalsha") {
		return ctx, context.DeadlineExceeded
	}
	return ctx, nil
}

func (h *capacityRedisFaultHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	if h.after.Load() && (cmd.Name() == "eval" || cmd.Name() == "evalsha") {
		return context.DeadlineExceeded
	}
	return nil
}

func (*capacityRedisFaultHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*capacityRedisFaultHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
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
	t.Run("transient and ambiguous renewal preserve confirmed ownership only", func(t *testing.T) {
		isolated := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
		t.Cleanup(func() { _ = isolated.Close() })
		fault := &capacityRedisFaultHook{}
		isolated.AddHook(fault)
		key, owner := channelCapacityKey(channelID), t.Name()
		started := time.Now()
		admitted, err := channelCapacityAcquireScript.Run(ctx, isolated, []string{key, channelWaitQueueKey(channelID), channelWaitGlobalKey}, 1, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
		require.NoError(t, err)
		require.Equal(t, 1, admitted)
		// Manual maintenance makes the deadline transitions deterministic; no
		// background timer races the fault injections.
		lease := newChannelCapacityLease(ctx, channelID)
		lease.client, lease.key, lease.owner, lease.done = isolated, key, owner, make(chan struct{})
		close(lease.done)
		lease.leaseExpiry.Store(started.Add(channelCapacityLeaseTTL).UnixNano())
		t.Cleanup(lease.Release)
		confirmed := lease.leaseExpiry.Load()
		fault.before.Store(true)
		require.True(t, lease.renew(), "a transient failure inside the confirmed TTL can retry")
		assert.NoError(t, lease.Context().Err())
		assert.Equal(t, confirmed, lease.leaseExpiry.Load())
		fault.before.Store(false)
		fault.after.Store(true)
		require.True(t, lease.renew(), "a successful write with a lost reply remains ambiguous")
		assert.Equal(t, confirmed, lease.leaseExpiry.Load(), "a lost reply cannot extend the confirmed deadline")
		fault.after.Store(false)
		require.True(t, lease.renew(), "a confirmed recovery keeps the same admitted attempt alive")
		assert.NoError(t, lease.Context().Err())
		assert.GreaterOrEqual(t, lease.leaseExpiry.Load(), confirmed)
		lease.leaseExpiry.Store(time.Now().Add(channelCapacityRenewalSafetyMargin / 2).UnixNano())
		fault.before.Store(true)
		assert.False(t, lease.renew(), "unknown renewal must stop before another gateway can reuse its slot")
		require.NotNil(t, lease.Err())
		assert.Equal(t, types.ErrorCodeChannelCapacityUnavailable, lease.Err().GetErrorCode())
		fault.before.Store(false)
		lease.Release()
		successor, apiErr := acquireRedisChannelCapacity(ctx, peer, channelID, 1)
		require.Nil(t, apiErr)
		defer successor.Release()
		lease.Release()
		assert.EqualValues(t, 1, peer.ZCard(ctx, key).Val(), "stale cleanup cannot remove a successor")
	})
	t.Run("expired probe cannot renew or admit overlapping work", func(t *testing.T) {
		key, owner := channelCapacityKey(channelID), t.Name()
		admitted, err := channelCapacityAcquireScript.Run(ctx, client, []string{key, channelWaitQueueKey(channelID), channelWaitGlobalKey}, 1, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
		require.NoError(t, err)
		require.Equal(t, 1, admitted)
		lease := newChannelCapacityLease(ctx, channelID)
		lease.client, lease.key, lease.owner, lease.done = client, key, owner, make(chan struct{})
		close(lease.done)
		lease.leaseExpiry.Store(time.Now().Add(channelCapacityLeaseTTL).UnixNano())
		lease.waitCoordinator, lease.waitProbeOwner = newChannelWaitCoordinator(client), owner
		lease.probeExpiry.Store(time.Now().Add(channelCapacityRenewalSafetyMargin / 2).UnixNano())
		t.Cleanup(lease.Release)
		require.NoError(t, client.HSet(ctx, channelWaitBusyKey(channelID), "probe", owner, "probe_until", 0).Err())
		t.Cleanup(func() { require.NoError(t, client.Del(ctx, channelWaitBusyKey(channelID)).Err()) })
		assert.False(t, lease.renew(), "probe ownership bounds renewal even when capacity TTL remains")
		require.NotNil(t, lease.Err())
		renewed, err := channelWaitProbeRenewScript.Run(ctx, client, []string{channelWaitBusyKey(channelID)}, owner, channelCapacityLeaseTTL.Milliseconds()).Int()
		require.NoError(t, err)
		assert.Zero(t, renewed, "an expired Redis probe owner must not be resurrected")
	})
	t.Run("unlimited counting with shared coordinator", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		unlimitedChannel := int(time.Now().UnixNano())
		settings := dto.ChannelSettings{}
		first, apiErr := AcquireChannelCapacity(ctx, unlimitedChannel, settings)
		require.Nil(t, apiErr)
		second, apiErr := AcquireChannelCapacity(ctx, unlimitedChannel, settings)
		require.Nil(t, apiErr)
		samples := ReadChannelConcurrency(ctx, []int{unlimitedChannel, channelID})
		require.NotNil(t, samples[unlimitedChannel].Current)
		assert.Equal(t, 2, *samples[unlimitedChannel].Current)
		assert.Equal(t, ChannelConcurrencyScopeCluster, samples[unlimitedChannel].Scope)
		assert.False(t, samples[unlimitedChannel].SampleTime.IsZero())
		assert.Greater(t, samples[unlimitedChannel].SampleTime.UnixMilli(), int64(0))
		stored, err := client.ZCount(ctx, channelCapacityKey(unlimitedChannel), fmt.Sprintf("(%d", samples[unlimitedChannel].SampleTime.UnixMilli()), "+inf").Result()
		require.NoError(t, err)
		assert.Equal(t, int64(2), stored, "the read matches the coordinator count at server time")
		first.Release()
		samples = ReadChannelConcurrency(ctx, []int{unlimitedChannel})
		require.NotNil(t, samples[unlimitedChannel].Current)
		assert.Equal(t, 1, *samples[unlimitedChannel].Current)
		second.Release()
		samples = ReadChannelConcurrency(ctx, []int{unlimitedChannel})
		require.NotNil(t, samples[unlimitedChannel].Current)
		assert.Equal(t, 0, *samples[unlimitedChannel].Current)
		require.NoError(t, client.Del(ctx, channelCapacityKey(unlimitedChannel)).Err())
	})
	t.Run("unlimited registration failure preserves traffic and an honest sample", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		isolated := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
		require.NoError(t, isolated.Close())
		common.RedisEnabled, common.RDB = true, isolated
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		unlimitedChannel := int(time.Now().UnixNano())
		lease, apiErr := AcquireChannelCapacity(ctx, unlimitedChannel, dto.ChannelSettings{})
		require.Nil(t, apiErr, "a failed coordinator command must never reject unlimited traffic")
		defer lease.Release()
		assert.NoError(t, lease.Context().Err())
		assert.Nil(t, lease.Err())
		// A sampler with a healthy connection must still report the live owner
		// missing from the count as unavailable, even if its marker write failed.
		common.RDB = client
		samples := ReadChannelConcurrency(ctx, []int{unlimitedChannel})
		assert.Nil(t, samples[unlimitedChannel].Current)
		lease.Release()
		samples = ReadChannelConcurrency(ctx, []int{unlimitedChannel})
		require.NotNil(t, samples[unlimitedChannel].Current)
		assert.Equal(t, 0, *samples[unlimitedChannel].Current, "release clears the missing-owner signal")
	})
	t.Run("read is non-mutating and ignores expired owners", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		readChannel := int(time.Now().UnixNano())
		key := channelCapacityKey(readChannel)
		require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: 0, Member: "expired-owner"}).Err())
		require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "live-owner"}).Err())
		t.Cleanup(func() { _ = client.Del(ctx, key).Err() })
		samples := ReadChannelConcurrency(ctx, []int{readChannel})
		require.NotNil(t, samples[readChannel].Current)
		assert.Equal(t, 1, *samples[readChannel].Current, "only unexpired owners are counted")
		score, err := client.ZScore(ctx, key, "expired-owner").Result()
		require.NoError(t, err, "a dashboard read must not reap another process's lease")
		assert.Equal(t, float64(0), score)
	})
	t.Run("coordinator loss is unavailable, not zero", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		isolated := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
		common.RedisEnabled, common.RDB = true, isolated
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		require.NoError(t, isolated.Close())
		samples := ReadChannelConcurrency(ctx, []int{channelID})
		assert.Nil(t, samples[channelID].Current)
		assert.Equal(t, ChannelConcurrencyScopeCluster, samples[channelID].Scope)
	})
	t.Run("a live owner missing from the count is re-registered", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		recoveringChannel := int(time.Now().UnixNano())
		lease, apiErr := AcquireChannelCapacity(ctx, recoveringChannel, dto.ChannelSettings{})
		require.Nil(t, apiErr)
		t.Cleanup(func() {
			_ = client.Del(ctx, channelCapacityKey(recoveringChannel), channelCapacityMarkerKey(recoveringChannel)).Err()
		})
		// Simulate the coordinator losing this live owner's entry; the owner is
		// still running, so it must return to the shared count.
		require.NoError(t, client.ZRem(ctx, channelCapacityKey(recoveringChannel), lease.owner).Err())
		assert.True(t, lease.refreshUnlimited(), "a still-running owner re-registers itself")
		samples := ReadChannelConcurrency(ctx, []int{recoveringChannel})
		require.NotNil(t, samples[recoveringChannel].Current)
		assert.Equal(t, 1, *samples[recoveringChannel].Current)
		assert.Equal(t, int64(0), client.ZCard(ctx, channelCapacityMarkerKey(recoveringChannel)).Val(), "recovery leaves no incomplete marker")
		assert.False(t, hasLostChannelCapacityOwner(recoveringChannel))
		lease.Release()
		samples = ReadChannelConcurrency(ctx, []int{recoveringChannel})
		require.NotNil(t, samples[recoveringChannel].Current)
		assert.Equal(t, 0, *samples[recoveringChannel].Current)
		assert.Equal(t, int64(0), client.ZCard(ctx, channelCapacityMarkerKey(recoveringChannel)).Val())
	})
	t.Run("a locally lost live owner forces an unavailable sample", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		lostChannel, healthyChannel := int(time.Now().UnixNano()), int(time.Now().UnixNano())+1
		key := channelCapacityKey(lostChannel)
		t.Cleanup(func() {
			_ = client.Del(ctx, key, channelCapacityKey(healthyChannel)).Err()
			removeLostChannelCapacityOwner(lostChannel)
		})
		require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "lost-owner"}).Err())
		// The shared marker may be unwritable during a coordinator outage, so the
		// local record alone must already make this gateway's sample honest.
		addLostChannelCapacityOwner(lostChannel)
		samples := ReadChannelConcurrency(ctx, []int{lostChannel, healthyChannel})
		assert.Nil(t, samples[lostChannel].Current, "an incomplete count is unavailable, not a low number")
		require.NotNil(t, samples[healthyChannel].Current, "an unrelated channel is still sampled")
		assert.Equal(t, 0, *samples[healthyChannel].Current)
		removeLostChannelCapacityOwner(lostChannel)
		samples = ReadChannelConcurrency(ctx, []int{lostChannel})
		require.NotNil(t, samples[lostChannel].Current)
		assert.Equal(t, 1, *samples[lostChannel].Current)
	})
	t.Run("a marker published by another gateway makes the sample incomplete", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		remoteChannel := int(time.Now().UnixNano())
		key, markerKey := channelCapacityKey(remoteChannel), channelCapacityMarkerKey(remoteChannel)
		t.Cleanup(func() { _ = client.Del(ctx, key, markerKey).Err() })
		require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "peer-owner"}).Err())
		samples := ReadChannelConcurrency(ctx, []int{remoteChannel})
		require.NotNil(t, samples[remoteChannel].Current)
		assert.Equal(t, 1, *samples[remoteChannel].Current)
		// Another gateway publishes the marker for its own lost owner.
		_, err := channelCapacityMarkerScript.Run(ctx, client, []string{markerKey}, "peer-lost-owner", channelCapacityMarkerTTL.Milliseconds()).Int()
		require.NoError(t, err)
		samples = ReadChannelConcurrency(ctx, []int{remoteChannel})
		assert.Nil(t, samples[remoteChannel].Current, "a peer's incomplete marker is visible here too")
		assert.Equal(t, ChannelConcurrencyScopeCluster, samples[remoteChannel].Scope)
	})
	t.Run("release removes an ambiguous owner entry", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		ambiguousChannel := int(time.Now().UnixNano())
		key := channelCapacityKey(ambiguousChannel)
		t.Cleanup(func() { _ = client.Del(ctx, key, channelCapacityMarkerKey(ambiguousChannel)).Err() })
		lease, apiErr := AcquireChannelCapacity(ctx, ambiguousChannel, dto.ChannelSettings{})
		require.Nil(t, apiErr)
		// The owner entry exists even though a lost registration reply means the
		// gateway never observed a success for it.
		lease.telemetryMu.Lock()
		lease.telemetryRegistered = false
		lease.telemetryMu.Unlock()
		lease.Release()
		assert.Equal(t, int64(0), client.ZCard(ctx, key).Val(), "release removes the owner entry it may have written")
		assert.False(t, hasLostChannelCapacityOwner(ambiguousChannel))
	})
	t.Run("missing script reloads and retries", func(t *testing.T) {
		previousRedis, previousClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, client
		t.Cleanup(func() { common.RedisEnabled, common.RDB = previousRedis, previousClient })
		countChannel := int(time.Now().UnixNano())
		key := channelCapacityKey(countChannel)
		require.NoError(t, client.ZAdd(ctx, key, &redis.Z{Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "owner"}).Err())
		t.Cleanup(func() { _ = client.Del(ctx, key).Err() })
		require.NoError(t, client.ScriptFlush(ctx).Err())
		samples := ReadChannelConcurrency(ctx, []int{countChannel})
		require.NotNil(t, samples[countChannel].Current, "SCRIPT FLUSH is recovered by loading the count script once")
		assert.Equal(t, 1, *samples[countChannel].Current)
	})
}
