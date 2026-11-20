package service

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupChannelWaitLocal(t *testing.T) *channelWaitCoordinator {
	t.Helper()
	require.NoError(t, i18n.Init())
	oldRedis, oldCoordinator := common.RedisEnabled, localChannelWait
	common.RedisEnabled = false
	c := newChannelWaitCoordinator(nil)
	// Drive dispatch explicitly to exercise exact handoff/cancellation order;
	// no scheduler sleeps or performance assertions are needed.
	c.running = true
	localChannelWait = c
	t.Cleanup(func() { common.RedisEnabled, localChannelWait = oldRedis, oldCoordinator })
	return c
}

func channelWaitTestEntry(userID int, candidates ...ChannelWaitCandidate) *channelWaitEntry {
	return &channelWaitEntry{id: uuid.NewString(), userID: userID, ctx: context.Background(), leaseCtx: context.Background(), deadline: time.Now().Add(5 * time.Minute), candidates: candidates, result: make(chan channelWaitResult, 1)}
}

func dispatchChannelWaitTest(c *channelWaitCoordinator) {
	c.mu.Lock()
	c.dispatchLocked(time.Now())
	c.mu.Unlock()
}

func TestChannelDiagnosticAdmission(t *testing.T) {
	t.Run("ordinary waiting still requires authentication", func(t *testing.T) {
		setupChannelWaitLocal(t)
		waiter := NewChannelWaiter(t.Context(), 0)
		defer waiter.Close()
		_, apiErr := waiter.Wait(t.Context(), []ChannelWaitCandidate{{ChannelID: 72501}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	})
	t.Run("background work waits and releases its fixed channel", func(t *testing.T) {
		coordinator := setupChannelWaitLocal(t)
		const channelID = 72502
		settings := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}
		active, apiErr := AcquireChannelCapacity(t.Context(), channelID, settings)
		require.Nil(t, apiErr)
		defer active.Release()
		admission := NewChannelDiagnosticAdmission(t.Context(), 0)
		defer admission.Close()
		result := make(chan channelWaitResult, 1)
		go func() {
			lease, apiErr := admission.Acquire(t.Context(), channelID, settings)
			result <- channelWaitResult{grant: &ChannelWaitGrant{ChannelID: channelID, Lease: lease}, err: apiErr}
		}()
		select {
		case <-coordinator.wake:
		case <-time.After(3 * time.Second):
			t.Fatal("diagnostic did not enter the queue")
		}
		dispatchChannelWaitTest(coordinator)
		select {
		case <-result:
			t.Fatal("diagnostic bypassed the channel capacity")
		default:
		}
		active.Release()
		dispatchChannelWaitTest(coordinator)
		var admitted channelWaitResult
		select {
		case admitted = <-result:
		case <-time.After(3 * time.Second):
			t.Fatal("released capacity did not admit the diagnostic")
		}
		require.Nil(t, admitted.err)
		require.NotNil(t, admitted.grant.Lease)
		admitted.grant.Lease.Release()
		admitted.grant.Lease.Release()
		localChannelCapacity.Lock()
		remaining := localChannelCapacity.active[channelID]
		localChannelCapacity.Unlock()
		assert.Zero(t, remaining)
		assert.Empty(t, coordinator.entries)
	})
	t.Run("cancellation removes queued background work", func(t *testing.T) {
		coordinator := setupChannelWaitLocal(t)
		const channelID = 72503
		settings := dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}
		active, apiErr := AcquireChannelCapacity(t.Context(), channelID, settings)
		require.Nil(t, apiErr)
		defer active.Release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		admission := NewChannelDiagnosticAdmission(ctx, 0)
		defer admission.Close()
		result := make(chan *types.NewAPIError, 1)
		go func() {
			lease, apiErr := admission.Acquire(ctx, channelID, settings)
			if lease != nil {
				lease.Release()
			}
			result <- apiErr
		}()
		select {
		case <-coordinator.wake:
		case <-time.After(3 * time.Second):
			t.Fatal("diagnostic did not enter the queue")
		}
		cancel()
		select {
		case apiErr = <-result:
			require.NotNil(t, apiErr)
		case <-time.After(3 * time.Second):
			t.Fatal("queued diagnostic ignored cancellation")
		}
		assert.Empty(t, coordinator.entries)
	})
	t.Run("unsaved preview has no shared capacity owner", func(t *testing.T) {
		setupChannelWaitLocal(t)
		admission := NewChannelDiagnosticAdmission(t.Context(), 0)
		defer admission.Close()
		lease, apiErr := admission.Acquire(t.Context(), 0, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
		require.Nil(t, apiErr)
		require.NotNil(t, lease)
		lease.Release()
		localChannelCapacity.Lock()
		remaining := localChannelCapacity.active[0]
		localChannelCapacity.Unlock()
		assert.Zero(t, remaining)
	})
	t.Run("unlimited admission survives unavailable Redis telemetry", func(t *testing.T) {
		setupChannelWaitLocal(t)
		oldRedis, oldClient := common.RedisEnabled, common.RDB
		common.RedisEnabled, common.RDB = true, nil
		t.Cleanup(func() { common.RedisEnabled, common.RDB = oldRedis, oldClient })
		admission := NewChannelDiagnosticAdmission(t.Context(), 0)
		defer admission.Close()
		lease, apiErr := admission.Acquire(t.Context(), 72504, dto.ChannelSettings{})
		require.Nil(t, apiErr)
		require.NotNil(t, lease)
		assert.NoError(t, lease.Context().Err())
		assert.Nil(t, lease.Err())
		lease.Release()
		localChannelCapacity.Lock()
		remaining := localChannelCapacity.active[72504]
		localChannelCapacity.Unlock()
		assert.Zero(t, remaining)
	})
}

func TestChannelWaitLocalFIFOAndUnrelatedCandidates(t *testing.T) {
	c := setupChannelWaitLocal(t)
	candidate := ChannelWaitCandidate{ChannelID: 72101, Settings: dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}}
	active, apiErr := AcquireChannelCapacity(context.Background(), candidate.ChannelID, candidate.Settings)
	require.Nil(t, apiErr)
	defer active.Release()
	first := channelWaitTestEntry(1, candidate)
	second := channelWaitTestEntry(2, candidate)
	unrelated := channelWaitTestEntry(3, ChannelWaitCandidate{ChannelID: 72102, Settings: candidate.Settings})
	for _, entry := range []*channelWaitEntry{first, second, unrelated} {
		require.Nil(t, c.register(entry, 1000, 20))
	}
	dispatchChannelWaitTest(c)
	select {
	case <-first.result:
		t.Fatal("full channel granted before release")
	default:
	}
	other := <-unrelated.result
	require.NotNil(t, other.grant)
	other.grant.Lease.Release()
	active.Release()
	_, apiErr = AcquireChannelCapacity(context.Background(), candidate.ChannelID, candidate.Settings)
	require.NotNil(t, apiErr, "a newcomer cannot steal the released queued slot")
	dispatchChannelWaitTest(c)
	firstGrant := <-first.result
	require.NotNil(t, firstGrant.grant)
	select {
	case <-second.result:
		t.Fatal("second waiter overtook active first waiter")
	default:
	}
	firstGrant.grant.Lease.Release()
	dispatchChannelWaitTest(c)
	secondGrant := <-second.result
	require.NotNil(t, secondGrant.grant)
	secondGrant.grant.Lease.Release()
	assert.Empty(t, c.entries)
	localChannelCapacity.Lock()
	assert.Zero(t, localChannelCapacity.active[candidate.ChannelID])
	localChannelCapacity.Unlock()
}

func TestChannelWaitRegistrationRechecksReleasedCapacityAndKeepsRequestAlive(t *testing.T) {
	c := setupChannelWaitLocal(t)
	candidate := ChannelWaitCandidate{ChannelID: 72103, Settings: dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}}
	active, apiErr := AcquireChannelCapacity(context.Background(), candidate.ChannelID, candidate.Settings)
	require.Nil(t, apiErr)
	_, apiErr = AcquireChannelCapacity(context.Background(), candidate.ChannelID, candidate.Settings)
	require.NotNil(t, apiErr)
	active.Release() // release happened before registration: no lost wakeup
	<-c.wake
	requestDeadline := time.Now().Add(time.Minute)
	requestCtx, cancelRequest := context.WithDeadline(context.Background(), requestDeadline)
	defer cancelRequest()
	waiter := NewChannelWaiter(requestCtx, 1)
	defer waiter.Close()
	result := make(chan channelWaitResult, 1)
	go func() {
		grant, err := waiter.Wait(requestCtx, []ChannelWaitCandidate{candidate})
		result <- channelWaitResult{grant: grant, err: err}
	}()
	<-c.wake // registration completed and schedules an immediate recheck
	c.mu.Lock()
	registeredDeadline := c.entries[0].deadline
	c.mu.Unlock()
	assert.Equal(t, requestDeadline, registeredDeadline, "abandoned queue tickets must expire when the request deadline ends")
	dispatchChannelWaitTest(c)
	got := <-result
	require.Nil(t, got.err)
	require.NotNil(t, got.grant)
	assert.NoError(t, got.grant.Lease.Context().Err(), "ending the parking context must not cancel the admitted upstream")
	got.grant.Lease.Release()
}

func TestChannelWaitBoundsCancellationAndFixedDeadline(t *testing.T) {
	c := setupChannelWaitLocal(t)
	candidate := ChannelWaitCandidate{ChannelID: 72104, Settings: dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}}
	first := channelWaitTestEntry(1, candidate)
	second := channelWaitTestEntry(2, candidate)
	require.Nil(t, c.register(first, 2, 1))
	assert.Equal(t, types.ErrorCodeChannelCapacityQueueFull, c.register(channelWaitTestEntry(1, candidate), 2, 1).GetErrorCode())
	require.Nil(t, c.register(second, 2, 1))
	assert.Equal(t, types.ErrorCodeChannelCapacityQueueFull, c.register(channelWaitTestEntry(3, candidate), 2, 1).GetErrorCode())
	c.remove(first)
	c.remove(first)
	c.remove(second)

	ctx, cancel := context.WithCancel(context.Background())
	entry := channelWaitTestEntry(1, candidate)
	entry.ctx = ctx
	require.Nil(t, c.register(entry, 2, 1))
	cancel()
	dispatchChannelWaitTest(c)
	got := <-entry.result
	require.Nil(t, got.grant)
	require.NotNil(t, got.err)
	assert.Empty(t, c.entries)

	waiter := NewChannelWaiter(context.Background(), 1)
	defer waiter.Close()
	waiter.deadline = time.Now().Add(-time.Second)
	for range 2 {
		grant, err := waiter.Wait(context.Background(), []ChannelWaitCandidate{candidate})
		assert.Nil(t, grant)
		require.NotNil(t, err)
		assert.Equal(t, types.ErrorCodeChannelCapacityWaitTimeout, err.GetErrorCode())
	}
	closed := NewChannelWaiter(context.Background(), 1)
	closed.Close()
	closed.Close()
	grant, err := closed.Wait(context.Background(), []ChannelWaitCandidate{candidate})
	assert.Nil(t, grant)
	require.NotNil(t, err)
	assert.Empty(t, c.entries)
	unauthenticated := NewChannelWaiter(context.Background(), 0)
	defer unauthenticated.Close()
	grant, apiErr := unauthenticated.Wait(context.Background(), []ChannelWaitCandidate{candidate})
	assert.Nil(t, grant)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestChannelWaitUpstreamBusyAllowsOnlyOneProbeAndHonorsBackoff(t *testing.T) {
	c := setupChannelWaitLocal(t)
	candidate := ChannelWaitCandidate{ChannelID: 72105}
	require.Nil(t, ReportChannelUpstreamBusy(context.Background(), candidate.ChannelID, 45*time.Second))
	first, second := channelWaitTestEntry(1, candidate), channelWaitTestEntry(2, candidate)
	require.Nil(t, c.register(first, 1000, 20))
	require.Nil(t, c.register(second, 1000, 20))
	dispatchChannelWaitTest(c)
	select {
	case <-first.result:
		t.Fatal("busy Retry-After was ignored")
	default:
	}
	c.mu.Lock()
	c.busy[candidate.ChannelID].until = time.Time{}
	c.mu.Unlock()
	dispatchChannelWaitTest(c)
	grant := <-first.result
	require.NotNil(t, grant.grant)
	select {
	case <-second.result:
		t.Fatal("more than one upstream probe granted")
	default:
	}
	require.Nil(t, ReportChannelUpstreamBusy(context.Background(), candidate.ChannelID, time.Minute))
	grant.grant.Lease.Release()
	dispatchChannelWaitTest(c)
	select {
	case <-second.result:
		t.Fatal("release bypassed the renewed upstream backoff")
	default:
	}
	c.mu.Lock()
	c.busy[candidate.ChannelID].until = time.Time{}
	c.mu.Unlock()
	dispatchChannelWaitTest(c)
	grant = <-second.result
	require.NotNil(t, grant.grant)
	grant.grant.Lease.MarkUpstreamAvailable()
	grant.grant.Lease.Release()
	assert.Empty(t, c.busy)
}

func TestChannelWaitRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"15", 15 * time.Second}, {"99999999", time.Minute}, {"-1", 0}, {"invalid", 0},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
	} {
		t.Run(tc.value, func(t *testing.T) { assert.Equal(t, tc.want, ParseChannelRetryAfter(tc.value, now)) })
	}
}

func TestChannelWaitCancellationAfterGrantReturnsSlot(t *testing.T) {
	c := setupChannelWaitLocal(t)
	candidate := ChannelWaitCandidate{ChannelID: 72106, Settings: dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}}
	ctx, cancel := context.WithCancel(context.Background())
	waiter := NewChannelWaiter(ctx, 1)
	defer waiter.Close()
	result := make(chan channelWaitResult, 1)
	go func() {
		grant, apiErr := waiter.Wait(ctx, []ChannelWaitCandidate{candidate})
		result <- channelWaitResult{grant: grant, err: apiErr}
	}()
	<-c.wake
	c.mu.Lock()
	require.Len(t, c.entries, 1)
	entry := c.entries[0]
	grant := c.tryLocal(entry, candidate, time.Now())
	require.NotNil(t, grant)
	// Cancellation and grant become observable together. Wait must return
	// the raced reservation regardless of which select branch runs first.
	cancel()
	c.entries = nil
	entry.result <- channelWaitResult{grant: grant}
	c.mu.Unlock()
	got := <-result
	assert.Nil(t, got.grant)
	require.NotNil(t, got.err)
	replacement, apiErr := AcquireChannelCapacity(context.Background(), candidate.ChannelID, candidate.Settings)
	require.Nil(t, apiErr)
	replacement.Release()
}

func TestChannelWaitRedis(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_REDIS_ADDR is required for real Redis queue verification")
	}
	require.NoError(t, i18n.Init())
	client := redis.NewClient(&redis.Options{Addr: address})
	peer := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err())
	oldRedis, oldClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() { common.RedisEnabled, common.RDB = oldRedis, oldClient })
	c1, c2 := newChannelWaitCoordinator(client), newChannelWaitCoordinator(peer)
	c1.running, c2.running = true, true
	id := int(time.Now().UnixNano())
	candidate := ChannelWaitCandidate{ChannelID: id, Settings: dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)}}
	t.Cleanup(func() {
		_ = client.Del(ctx, channelWaitGlobalKey, channelWaitUserKey(0), channelWaitUserKey(id), channelWaitUserKey(id+1), channelWaitQueueKey(id), channelWaitBusyKey(id), channelCapacityKey(id)).Err()
	})
	first, second := channelWaitTestEntry(id, candidate), channelWaitTestEntry(id+1, candidate)
	active, apiErr := acquireRedisChannelCapacity(ctx, client, id, 1)
	require.Nil(t, apiErr)
	defer active.Release()
	require.Nil(t, c1.register(first, 2, 1))
	require.Nil(t, c2.register(second, 2, 1))
	assert.Equal(t, types.ErrorCodeChannelCapacityQueueFull, c2.register(channelWaitTestEntry(id+1, candidate), 2, 1).GetErrorCode())
	active.Release()
	_, apiErr = acquireRedisChannelCapacity(ctx, peer, id, 1)
	require.NotNil(t, apiErr, "newcomer must not bypass cluster queue")
	dispatchChannelWaitTest(c2)
	select {
	case <-second.result:
		t.Fatal("second instance overtook older ticket")
	default:
	}
	dispatchChannelWaitTest(c1)
	grant := <-first.result
	require.NotNil(t, grant.grant)
	grant.grant.Lease.Release()
	dispatchChannelWaitTest(c2)
	grant = <-second.result
	require.NotNil(t, grant.grant)
	grant.grant.Lease.Release()
	assert.Zero(t, client.ZCard(ctx, channelWaitGlobalKey).Val())
	assert.Zero(t, client.ZCard(ctx, channelCapacityKey(id)).Val())

	// An abandoned ticket is expired using Redis time, without sleeping. A
	// recovery sweep must progress even if no PubSub notification was received.
	stale, current := channelWaitTestEntry(id, candidate), channelWaitTestEntry(id+1, candidate)
	require.Nil(t, c1.register(stale, 2, 1))
	require.Nil(t, c2.register(current, 2, 1))
	clock, err := client.Time(ctx).Result()
	require.NoError(t, err)
	require.NoError(t, client.ZAdd(ctx, channelWaitGlobalKey, &redis.Z{Score: float64(clock.Add(-time.Second).UnixMilli()), Member: stale.id}).Err())
	dispatchChannelWaitTest(c2)
	grant = <-current.result
	require.NotNil(t, grant.grant)
	grant.grant.Lease.Release()
	c1.remove(stale)
	assert.Zero(t, client.ZCard(ctx, channelWaitQueueKey(id)).Val())

	// An unlimited upstream rejection still permits only one cluster probe.
	require.Nil(t, ReportChannelUpstreamBusy(ctx, id, time.Minute))
	first, second = channelWaitTestEntry(id, ChannelWaitCandidate{ChannelID: id}), channelWaitTestEntry(id+1, ChannelWaitCandidate{ChannelID: id})
	require.Nil(t, c1.register(first, 2, 1))
	require.Nil(t, c2.register(second, 2, 1))
	dispatchChannelWaitTest(c1)
	select {
	case <-first.result:
		t.Fatal("Redis ignored upstream backoff")
	default:
	}
	require.NoError(t, client.HSet(ctx, channelWaitBusyKey(id), "until", 0).Err())
	// A gate created by an old rejection may be close to expiry when capacity
	// becomes available. The new probe must retain ownership until renewal.
	require.NoError(t, client.PExpire(ctx, channelWaitBusyKey(id), channelCapacityLeaseTTL/6).Err())
	dispatchChannelWaitTest(c1)
	grant = <-first.result
	require.NotNil(t, grant.grant)
	probeTTL, err := client.PTTL(ctx, channelWaitBusyKey(id)).Result()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, probeTTL, channelCapacityLeaseTTL, "admission must preserve the probe gate through its first renewal")
	dispatchChannelWaitTest(c2)
	select {
	case <-second.result:
		t.Fatal("multiple Redis upstream probes admitted")
	default:
	}
	require.True(t, grant.grant.Lease.renew())
	grant.grant.Lease.MarkUpstreamAvailable()
	grant.grant.Lease.Release()
	dispatchChannelWaitTest(c2)
	grant = <-second.result
	require.NotNil(t, grant.grant)
	grant.grant.Lease.Release()
	assert.Zero(t, client.Exists(ctx, channelWaitBusyKey(id)).Val())
	assert.Zero(t, client.ZCard(ctx, channelCapacityKey(id)).Val())

	t.Run("internal diagnostics cancel and release Redis ownership", func(t *testing.T) {
		coordinator := newChannelWaitCoordinator(client)
		coordinator.running = true
		active, apiErr := acquireRedisChannelCapacity(ctx, client, id, 1)
		require.Nil(t, apiErr)
		defer active.Release()
		requestCtx, cancel := context.WithCancel(t.Context())
		defer cancel()
		admission := NewChannelDiagnosticAdmission(requestCtx, 0)
		admission.waiter.coordinator = coordinator
		defer admission.Close()
		result := make(chan channelWaitResult, 1)
		go func() {
			lease, apiErr := admission.Acquire(requestCtx, id, candidate.Settings)
			result <- channelWaitResult{grant: &ChannelWaitGrant{Lease: lease}, err: apiErr}
		}()
		select {
		case <-coordinator.wake:
		case <-time.After(3 * time.Second):
			t.Fatal("internal diagnostic did not enter Redis queue")
		}
		assert.EqualValues(t, 1, client.ZCard(ctx, channelWaitUserKey(0)).Val())
		cancel()
		select {
		case canceled := <-result:
			require.NotNil(t, canceled.err)
			assert.Nil(t, canceled.grant.Lease)
		case <-time.After(3 * time.Second):
			t.Fatal("internal Redis diagnostic ignored cancellation")
		}
		assert.Zero(t, client.ZCard(ctx, channelWaitUserKey(0)).Val())
		assert.Zero(t, client.ZCard(ctx, channelWaitQueueKey(id)).Val())
		assert.EqualValues(t, 1, client.ZCard(ctx, channelCapacityKey(id)).Val())

		// Use a fresh deterministic dispatcher for the next request; cancellation
		// notifications from the prior request cannot stand in for registration.
		coordinator = newChannelWaitCoordinator(client)
		coordinator.running = true
		next := NewChannelDiagnosticAdmission(t.Context(), 0)
		next.waiter.coordinator = coordinator
		defer next.Close()
		go func() {
			lease, apiErr := next.Acquire(t.Context(), id, candidate.Settings)
			result <- channelWaitResult{grant: &ChannelWaitGrant{Lease: lease}, err: apiErr}
		}()
		select {
		case <-coordinator.wake:
		case <-time.After(3 * time.Second):
			t.Fatal("replacement internal diagnostic did not queue")
		}
		active.Release()
		dispatchChannelWaitTest(coordinator)
		select {
		case admitted := <-result:
			require.Nil(t, admitted.err)
			require.NotNil(t, admitted.grant.Lease)
			admitted.grant.Lease.Release()
			admitted.grant.Lease.Release()
		case <-time.After(3 * time.Second):
			t.Fatal("replacement internal diagnostic was not admitted")
		}
		assert.Zero(t, client.ZCard(ctx, channelWaitGlobalKey).Val())
		assert.Zero(t, client.ZCard(ctx, channelWaitUserKey(0)).Val())
		assert.Zero(t, client.ZCard(ctx, channelWaitQueueKey(id)).Val())
		assert.Zero(t, client.ZCard(ctx, channelCapacityKey(id)).Val())
	})

	// Coordination failure is unavailable, never a falsely full channel or
	// an independent local admission. Close only the dedicated test peer.
	require.NoError(t, peer.Close())
	apiErr = c2.register(channelWaitTestEntry(id, candidate), 2, 1)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeChannelCapacityUnavailable, apiErr.GetErrorCode())
}
