package service

import (
	"context"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// ChannelDiagnosticAdmission owns the waiting deadline for one trusted console
// or background operation. It never selects a different channel or authorizes
// access: callers revalidate current channel/key/model policy after a grant.
type ChannelDiagnosticAdmission struct {
	waiter *ChannelWaiter
}

// NewChannelDiagnosticAdmission is for server-side diagnostic and management
// operations only. Background work shares a bounded internal queue bucket, not
// an invented user identity. Ordinary NewChannelWaiter still requires a user.
func NewChannelDiagnosticAdmission(ctx context.Context, userID int) *ChannelDiagnosticAdmission {
	waiter := NewChannelWaiter(ctx, max(0, userID))
	waiter.internal = userID <= 0
	return &ChannelDiagnosticAdmission{waiter: waiter}
}

// Acquire returns an already-counted lease. Keep this owner for all attempts,
// close it when the operation ends, and release each lease after body cleanup.
// Unsaved previews have no shared channel identity and cannot enter the queue.
func (a *ChannelDiagnosticAdmission) Acquire(ctx context.Context, channelID int, settings dto.ChannelSettings) (*ChannelCapacityLease, *types.NewAPIError) {
	if ctx.Err() != nil {
		return nil, channelWaitContextError(ctx)
	}
	if a.waiter.ctx.Err() != nil {
		return nil, channelWaitContextError(a.waiter.ctx)
	}
	if channelID <= 0 {
		return &ChannelCapacityLease{ctx: ctx}, nil
	}
	// Unlimited channels keep best-effort telemetry: a Redis outage must not
	// turn an unset limit into an admission dependency or cancel their traffic.
	if settings.MaxConcurrentRequests == nil || *settings.MaxConcurrentRequests <= 0 {
		return AcquireChannelCapacity(ctx, channelID, settings)
	}
	return a.Wait(ctx, channelID, settings)
}

// Wait is for an explicitly recognized upstream concurrency refusal. Unlike
// ordinary unlimited admission it coordinates a bounded recovery probe even
// without a configured local limit. Generic 429 and quota errors are not busy.
func (a *ChannelDiagnosticAdmission) Wait(ctx context.Context, channelID int, settings dto.ChannelSettings) (*ChannelCapacityLease, *types.NewAPIError) {
	grant, apiErr := a.waiter.Wait(ctx, []ChannelWaitCandidate{{ChannelID: channelID, Settings: settings}})
	if apiErr != nil {
		return nil, apiErr
	}
	return grant.Lease, nil
}

func (a *ChannelDiagnosticAdmission) Close() {
	if a != nil {
		a.waiter.Close()
	}
}
