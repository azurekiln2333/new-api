package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Loaded only through the reviewer-owned Go overlay. This reproduces the
// context transitions of controller.executeTaskSubmissionWith and native SSE
// TaskAdaptor.DoRequest without editing shared implementation/test files.
func TestPiFinalCheckUnlimitedFallbackDoesNotInheritReleasedLease(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	first, capacityErr := AcquireChannelCapacity(context.Background(), 991601, dto.ChannelSettings{MaxConcurrentRequests: common.GetPointer(1)})
	require.Nil(t, capacityErr)
	defer first.Release()

	// Native SSE restores this client-detached request after DoRequest.
	detached := context.WithoutCancel(first.Context())
	// The controller releases the failed first attempt before selecting B.
	first.Release()
	require.Error(t, first.control.Err(), "the first lease control must be canceled on release")
	marker, ok := detached.Value(channelCapacityContextKey{}).(*ChannelCapacityLease)
	require.True(t, ok, "the detached retry context must retain the first lease marker")
	require.Same(t, first, marker)

	fallback, capacityErr := AcquireChannelCapacity(detached, 991602, dto.ChannelSettings{})
	require.Nil(t, capacityErr)
	defer fallback.Release()
	require.NoError(t, fallback.Context().Err())
	fallbackMarker, ok := fallback.Context().Value(channelCapacityContextKey{}).(*ChannelCapacityLease)
	require.True(t, ok, "an unlimited fallback currently retains the prior lease marker")
	require.Same(t, first, fallbackMarker)

	// B's native SSE path must observe B's admission, not A's retired lease.
	bound, releaseBound := WithChannelCapacityCancellation(context.WithoutCancel(fallback.Context()))
	defer releaseBound()
	select {
	case <-bound.Done():
		assert.ErrorIs(t, bound.Err(), context.Canceled, "the fallback inherited A's released lease cancellation")
	case <-time.After(time.Second):
		t.Fatal("probe did not observe the inherited lease cancellation")
	}
}
