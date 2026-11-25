package model

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// Management saves that do not own an explicit business transaction enqueue the
// audit obligation in the primary outbox after the business commit. Normal
// delivery is asynchronous; if enqueue fails, a bounded direct sink write keeps
// the original action, actor snapshot, parameters, and EventId.
//
// This path has an unavoidable post-commit gap: a process crash after the business
// commit but before the outbox transaction can lose the event. Channel and code
// mutations with transaction access use EnqueueAuditEventTx to close that gap.

// RecordManagementAuditDurable enqueues a management audit event outside any
// business transaction. It returns the event id so the caller can surface a
// failure instead of pretending the audit was accepted.
func RecordManagementAuditDurable(ctx context.Context, draft AuditEventDraft) (string, error) {
	if err := draft.encodeMetadata(); err != nil {
		return "", err
	}
	eventTime := draft.CreatedAt
	if eventTime <= 0 {
		eventTime = common.GetTimestamp()
	}
	// Enqueue on its own bounded context: the request context may be canceled by
	// the client while the audit obligation must still be recorded.
	enqueueCtx, cancel := context.WithTimeout(context.Background(), AuditDeliveryTimeout)
	defer cancel()
	var eventId string
	err := DB.WithContext(enqueueCtx).Transaction(func(tx *gorm.DB) error {
		id, err := EnqueueAuditEventTx(tx, draft, eventTime)
		if err != nil {
			return err
		}
		eventId = id
		return nil
	})
	if err != nil {
		logger.LogError(ctx, common.LogText("audit outbox enqueue failed for action %s: %s", draft.Action, ClassifyAuditDeliveryError(err)))
		fallbackCtx, cancelFallback := context.WithTimeout(context.Background(), AuditDeliveryTimeout)
		defer cancelFallback()
		fallback := &AuditEventOutbox{
			EventId:       draft.EventId,
			Category:      draft.Category,
			Action:        draft.Action,
			ActorUserId:   draft.ActorUserId,
			ActorUsername: draft.ActorUsername,
			ActorRole:     draft.ActorRole,
			AuthMethod:    draft.AuthMethod,
			TokenRef:      draft.TokenRef,
			Ip:            draft.Ip,
			UserAgent:     draft.UserAgent,
			Method:        draft.Method,
			Route:         draft.Route,
			Status:        draft.Status,
			Success:       draft.Success,
			RequestId:     draft.RequestId,
			CreatedAt:     eventTime,
			Content:       draft.Content,
			ParamsJson:    draft.ParamsJson,
		}
		if fallbackErr := deliverAuditEvent(fallbackCtx, fallback); fallbackErr != nil {
			logger.LogError(ctx, common.LogText("audit direct fallback failed for action %s: %s", draft.Action, ClassifyAuditDeliveryError(fallbackErr)))
			return "", ErrCodeBatchStorage
		}
		logger.LogError(ctx, common.LogText("audit event for action %s was delivered directly after outbox enqueue failed", draft.Action))
		return draft.EventId, nil
	}
	return eventId, nil
}
