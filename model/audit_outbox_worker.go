package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
)

const (
	// auditOutboxDrainBatch bounds how many candidate rows one pass considers.
	// Each row is leased individually, so a long pass cannot invalidate the lease
	// of a row it has not reached yet.
	auditOutboxDrainBatch = 50
	// AuditOutboxDrainIntervalSeconds is the cadence of the background drain.
	AuditOutboxDrainIntervalSeconds = 15
)

// DrainAuditOutbox delivers due outbox rows and returns how many were delivered.
//
// Each row is leased immediately before its own delivery attempt, so the lease
// always covers the attempt it guards. A row whose lease cannot be taken — because
// another master holds it, or because its backoff was pushed forward after the
// candidate list was read — is skipped instead of delivered.
//
// Delivery is at-least-once. A crash between the sink write and the delivery
// acknowledgement replays the row; the relational sink absorbs the replay through
// its unique event_id, while ClickHouse has no dedupe key and can show the event
// twice.
func DrainAuditOutbox(ctx context.Context) (delivered int) {
	// The candidate read runs on its own bounded context so the caller's lifetime
	// (a startup Background, for instance) never leaves the query unbounded.
	queryCtx, cancelQuery := context.WithTimeout(ctx, AuditOutboxOperationTimeout*time.Second)
	ids, err := AuditEventDueCandidates(queryCtx, auditOutboxDrainBatch, common.GetTimestamp())
	cancelQuery()
	if err != nil {
		logger.LogError(ctx, common.LogText("audit outbox candidate query failed: %s", ClassifyAuditDeliveryError(err)))
		return 0
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return delivered
		}
		claimCtx, cancelClaim := context.WithTimeout(ctx, AuditDeliveryTimeout)
		event, err := ClaimAuditEvent(claimCtx, id, common.GetTimestamp())
		cancelClaim()
		if err != nil {
			logger.LogError(ctx, common.LogText("audit outbox claim failed: %s", ClassifyAuditDeliveryError(err)))
			continue
		}
		if event == nil {
			continue // another master holds it, or it was rescheduled after the read
		}
		// Re-check the lease this worker just took before writing to the sink, so
		// a reclaimed row is not sunk by a worker that has lost ownership.
		if !auditLeaseCoversAttempt(event) {
			if err := releaseWithBoundedContext(ctx, event); err != nil {
				logger.LogError(ctx, common.LogText("audit outbox lease release failed: %s", ClassifyAuditDeliveryError(err)))
			}
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, AuditDeliveryTimeout)
		deliveryErr := deliverAuditEvent(attemptCtx, event)
		cancel()
		if deliveryErr == nil {
			if ackErr := acknowledgeWithBoundedContext(ctx, event); ackErr != nil {
				// The sink accepted the event; the row is replayed later and the
				// relational sink absorbs the duplicate.
				logger.LogError(ctx, common.LogText("audit outbox acknowledgement failed: %s", ClassifyAuditDeliveryError(ackErr)))
				continue
			}
			delivered++
			continue
		}
		// The row is never deleted: it is rescheduled with bounded backoff and
		// stays pending until a delivery succeeds.
		attempts := event.Attempts + 1
		next := common.GetTimestamp() + AuditOutboxBackoff(attempts)
		if rescheduleErr := rescheduleWithBoundedContext(ctx, event, attempts, next, ClassifyAuditDeliveryError(deliveryErr)); rescheduleErr != nil {
			logger.LogError(ctx, common.LogText("audit outbox reschedule failed: %s", ClassifyAuditDeliveryError(rescheduleErr)))
		}
	}
	return delivered
}

// The acknowledgement and reschedule writes must still land when the caller's
// context was canceled by the delivery attempt, so each runs on its own bounded
// context instead of inheriting a dead one.
func outboxWriteContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), AuditDeliveryTimeout)
}

func acknowledgeWithBoundedContext(ctx context.Context, event *AuditEventOutbox) error {
	writeCtx, cancel := outboxWriteContext()
	defer cancel()
	return MarkAuditEventDelivered(writeCtx, event.Id, event.LeaseToken, common.GetTimestamp())
}

func rescheduleWithBoundedContext(ctx context.Context, event *AuditEventOutbox, attempts int, nextAttemptAt int64, category string) error {
	writeCtx, cancel := outboxWriteContext()
	defer cancel()
	return RescheduleAuditEvent(writeCtx, event.Id, event.LeaseToken, attempts, nextAttemptAt, category)
}

func releaseWithBoundedContext(ctx context.Context, event *AuditEventOutbox) error {
	writeCtx, cancel := outboxWriteContext()
	defer cancel()
	return ReleaseAuditEventLease(writeCtx, event.Id, event.LeaseToken)
}

// auditLeaseCoversAttempt reports whether the current lease still extends far
// enough past now to cover one bounded delivery attempt.
func auditLeaseCoversAttempt(event *AuditEventOutbox) bool {
	if event.LeaseToken == "" {
		return false
	}
	return event.LeaseUntil >= common.GetTimestamp()+int64(AuditDeliveryTimeout.Seconds())
}

// deliverAuditEvent writes one outbox row to the log database. The EventId and the
// original mutation timestamp are preserved so the delivered row identifies the
// same event, at the moment it happened.
func deliverAuditEvent(ctx context.Context, event *AuditEventOutbox) error {
	if LOG_DB == nil {
		return errors.New("log database unavailable")
	}
	entry := AuditLog{
		EventId:    event.EventId,
		UserId:     event.ActorUserId,
		Username:   event.ActorUsername,
		ActorRole:  event.ActorRole,
		CreatedAt:  event.CreatedAt,
		Category:   event.Category,
		Action:     event.Action,
		TokenRef:   event.TokenRef,
		AuthMethod: event.AuthMethod,
		Ip:         event.Ip,
		UserAgent:  event.UserAgent,
		Method:     event.Method,
		Route:      event.Route,
		Status:     event.Status,
		Success:    event.Success,
		RequestId:  event.RequestId,
		Content:    event.Content,
		Other:      AuditOther{},
	}
	var params AuditFields
	if event.ParamsJson != "" {
		if err := common.UnmarshalJsonStr(event.ParamsJson, &params); err != nil {
			params = AuditFields{}
		}
	}
	entry.Other.Op = &AuditOperation{Action: event.Action, Params: params}
	if event.ActorUsername != "" {
		entry.Other.AdminInfo = &AuditAdminInfo{
			AdminID:       event.ActorUserId,
			AdminUsername: event.ActorUsername,
			AdminRole:     event.ActorRole,
			AuthMethod:    event.AuthMethod,
		}
	}
	entry.Other.AuditInfo = &AuditRequestInfo{
		Method:  event.Method,
		Route:   event.Route,
		Path:    event.Route,
		Status:  event.Status,
		Success: event.Success,
	}
	return WriteAuditLogEntry(ctx, entry)
}

// StartAuditOutboxDrain runs the delivery loop for as long as ctx is alive. It is
// meant for the master node: every master may drain, and the per-row lease keeps
// concurrent drains from double-delivering their own claims.
func StartAuditOutboxDrain(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(AuditOutboxDrainIntervalSeconds * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				DrainAuditOutbox(ctx)
			}
		}
	}()
}
