package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// AuditEventDraft is a secret-free value snapshot of one audit obligation. It is
// built by the controller from an AuditActorSnapshot and never carries a request
// body, a key value, or a bearer credential.
type AuditEventDraft struct {
	EventId       string
	Category      string
	Action        string
	ActorUserId   int
	ActorUsername string
	ActorRole     int
	AuthMethod    string
	TokenRef      string
	Ip            string
	UserAgent     string
	Method        string
	Route         string
	Status        int
	Success       bool
	RequestId     string
	Content       string
	ParamsJson    string
	// Other and CreatedAt preserve prepared middleware events without adding
	// columns. Legacy controller drafts continue to store params-only JSON.
	Other     *AuditOther
	CreatedAt int64
}

type auditOutboxMetadata struct {
	Format string      `json:"_audit_outbox_format"`
	Other  *AuditOther `json:"other"`
}

const auditOutboxMetadataFormat = "audit_other_v1"

func (draft *AuditEventDraft) encodeMetadata() error {
	if draft.Other == nil {
		return nil
	}
	encoded, err := common.Marshal(auditOutboxMetadata{Format: auditOutboxMetadataFormat, Other: draft.Other})
	if err != nil {
		return err
	}
	draft.ParamsJson = string(encoded)
	draft.Other = nil
	return nil
}

// AuditEventOutbox is the durable audit delivery queue. Rows are never deleted:
// a failed delivery is retried with bounded exponential backoff indefinitely so
// a long log-database outage still recovers without manual intervention.
type AuditEventOutbox struct {
	Id            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	EventId       string `gorm:"type:varchar(64);not null;uniqueIndex:idx_audit_outbox_event" json:"event_id"`
	Category      string `gorm:"type:varchar(24);not null;default:''" json:"category"`
	Action        string `gorm:"type:varchar(128);not null;default:''" json:"action"`
	ActorUserId   int    `gorm:"not null;default:0" json:"actor_user_id"`
	ActorUsername string `gorm:"type:varchar(64);not null;default:''" json:"actor_username"`
	ActorRole     int    `gorm:"not null;default:0" json:"actor_role"`
	AuthMethod    string `gorm:"type:varchar(24);not null;default:''" json:"auth_method"`
	TokenRef      string `gorm:"type:varchar(64);not null;default:''" json:"token_ref"`
	Ip            string `gorm:"type:varchar(64);not null;default:''" json:"ip"`
	UserAgent     string `gorm:"type:varchar(512);not null;default:''" json:"user_agent"`
	Method        string `gorm:"type:varchar(16);not null;default:''" json:"method"`
	Route         string `gorm:"type:varchar(255);not null;default:''" json:"route"`
	Status        int    `gorm:"not null;default:0" json:"status"`
	Success       bool   `gorm:"not null" json:"success"`
	RequestId     string `gorm:"type:varchar(64);not null;default:''" json:"request_id"`
	// CreatedAt is the mutation time, not the delivery time. A delayed delivery
	// must report the moment the audited event actually happened.
	CreatedAt     int64  `gorm:"not null;default:0;index:idx_audit_outbox_created" json:"created_at"`
	Content       string `gorm:"type:text;not null" json:"content"`
	ParamsJson    string `gorm:"type:text;not null" json:"params_json"`
	Attempts      int    `gorm:"not null;default:0" json:"attempts"`
	NextAttemptAt int64  `gorm:"not null;default:0;index:idx_audit_outbox_pending" json:"next_attempt_at"`
	// LeaseToken and LeaseUntil serialize the drain across masters: a row is only
	// claimable when no live lease holds it.
	LeaseToken  string `gorm:"type:varchar(64);not null;default:''" json:"lease_token"`
	LeaseUntil  int64  `gorm:"not null;default:0" json:"lease_until"`
	DeliveredAt int64  `gorm:"not null;default:0;index:idx_audit_outbox_pending" json:"delivered_at"`
	// LastError holds a safe category rather than raw driver text, so no SQL and
	// no payload content can leak into the outbox.
	LastError string `gorm:"type:varchar(64);not null;default:''" json:"last_error"`
}

func (AuditEventOutbox) TableName() string { return "audit_event_outbox" }

const (
	auditOutboxBackoffBase = 5
	auditOutboxBackoffCap  = 600
	// auditOutboxErrorLimit bounds the stored category string.
	auditOutboxErrorLimit = 64
	// AuditOutboxLeaseSeconds bounds how long one master may hold a row before
	// another master may reclaim it.
	AuditOutboxLeaseSeconds = 60
	// AuditOutboxOperationTimeout bounds one outbox database operation so no
	// caller waits on the pool without a deadline.
	AuditOutboxOperationTimeout = 10
	// AuditDeliveryTimeout bounds one delivery attempt so a stalled log database
	// cannot pin a drain worker indefinitely.
	AuditDeliveryTimeout = 10 * time.Second
)

// EnqueueAuditEventTx writes one audit obligation on the caller's transaction and
// returns the event id. It joins the business commit, so a committed write can
// never lose its audit event, and an enqueue failure rolls the business write back.
//
// An empty draft.EventId keeps the previous "no audit" behaviour for callers that
// do not opt in. Otherwise exactly one row is written per business transaction.
//
// eventTime is optional: omit it to timestamp the event at enqueue time, or pass
// the mutation timestamp when the caller already computed it.
func EnqueueAuditEventTx(tx *gorm.DB, draft AuditEventDraft, eventTime ...int64) (string, error) {
	if draft.EventId == "" {
		return "", nil
	}
	if err := draft.encodeMetadata(); err != nil {
		return "", err
	}
	if len(draft.UserAgent) > 0 {
		// Truncate by rune, matching RecordAuditLog. Cutting by byte length can
		// split a multi-byte character and produce invalid UTF-8, which the
		// primary business transaction then fails to store on PostgreSQL/MySQL.
		runes := []rune(draft.UserAgent)
		if len(runes) > 512 {
			draft.UserAgent = string(runes[:512])
		}
	}
	eventTimeValue := draft.CreatedAt
	if eventTimeValue <= 0 {
		eventTimeValue = common.GetTimestamp()
	}
	if len(eventTime) > 0 && eventTime[0] > 0 {
		eventTimeValue = eventTime[0]
	}
	row := &AuditEventOutbox{
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
		CreatedAt:     eventTimeValue,
		Content:       draft.Content,
		ParamsJson:    draft.ParamsJson,
		NextAttemptAt: eventTimeValue,
	}
	if err := tx.Create(row).Error; err != nil {
		return "", err
	}
	return row.EventId, nil
}

// AuditOutboxBackoff returns the delay before the next attempt. It is bounded so
// retries never stop: a long log-database outage recovers on its own.
func AuditOutboxBackoff(attempts int) int64 {
	delay := int64(auditOutboxBackoffBase)
	for range attempts {
		delay *= 2
		if delay >= auditOutboxBackoffCap {
			return auditOutboxBackoffCap
		}
	}
	if delay > auditOutboxBackoffCap {
		return auditOutboxBackoffCap
	}
	return delay
}

// ClassifyAuditDeliveryError maps a delivery failure to a short safe category.
// Driver text is discarded because it can embed SQL and bound payload values.
func ClassifyAuditDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	var category string
	switch {
	case LOG_DB == nil:
		category = "log_database_unavailable"
	case errors.Is(err, gorm.ErrInvalidDB):
		category = "log_database_unavailable"
	case strings.Contains(strings.ToLower(err.Error()), "timeout"):
		category = "delivery_timeout"
	default:
		category = "delivery_failed"
	}
	if len(category) > auditOutboxErrorLimit {
		category = category[:auditOutboxErrorLimit]
	}
	return category
}

// ClaimAuditEvent leases ONE row, rechecking the schedule and the lease state in
// the same conditional update. A candidate read earlier therefore cannot bypass a
// reschedule or a lease taken by another master: the update affects zero rows and
// the row is skipped.
//
// Claiming one row at a time keeps the lease meaningful. A batch lease sized for a
// whole pass would expire under it — a tail row leased at the start of a long pass
// would be reclaimable while still being processed.
func ClaimAuditEvent(ctx context.Context, id int64, now int64) (*AuditEventOutbox, error) {
	leaseToken := common.GetUUID()
	leaseUntil := now + AuditOutboxLeaseSeconds
	result := DB.WithContext(ctx).Model(&AuditEventOutbox{}).
		Where("id = ? AND delivered_at = 0 AND next_attempt_at <= ? AND lease_until <= ?", id, now, now).
		Updates(map[string]any{"lease_token": leaseToken, "lease_until": leaseUntil})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, nil
	}
	var row AuditEventOutbox
	if err := DB.WithContext(ctx).Where("id = ? AND lease_token = ?", id, leaseToken).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// AuditEventDueCandidates lists row ids whose backoff has elapsed. Leasing is a
// separate conditional step, so this list is only a scheduling hint.
func AuditEventDueCandidates(ctx context.Context, limit int, now int64) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	var ids []int64
	err := DB.WithContext(ctx).Model(&AuditEventOutbox{}).
		Where("delivered_at = 0 AND next_attempt_at <= ? AND lease_until <= ?", now, now).
		Order("next_attempt_at").
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// MarkAuditEventDelivered clears the row's pending state, guarded by the lease so
// a stale worker cannot acknowledge a row another master took over.
func MarkAuditEventDelivered(ctx context.Context, id int64, leaseToken string, now int64) error {
	return DB.WithContext(ctx).Model(&AuditEventOutbox{}).
		Where("id = ? AND delivered_at = 0 AND lease_token = ?", id, leaseToken).
		Updates(map[string]any{"delivered_at": now, "last_error": "", "lease_token": "", "lease_until": 0}).Error
}

// ReleaseAuditEventLease drops a lease without recording progress, used when a
// worker stops or loses the lease before sinking the event.
func ReleaseAuditEventLease(ctx context.Context, id int64, leaseToken string) error {
	return DB.WithContext(ctx).Model(&AuditEventOutbox{}).
		Where("id = ? AND delivered_at = 0 AND lease_token = ?", id, leaseToken).
		Updates(map[string]any{"lease_token": "", "lease_until": 0}).Error
}

// RescheduleAuditEvent records a failure and the next bounded retry time. There is
// no terminal state: the row remains pending until it is delivered. The update is
// lease-guarded so only the claiming worker can move the cursor.
func RescheduleAuditEvent(ctx context.Context, id int64, leaseToken string, attempts int, nextAttemptAt int64, errorCategory string) error {
	return DB.WithContext(ctx).Model(&AuditEventOutbox{}).
		Where("id = ? AND delivered_at = 0 AND lease_token = ?", id, leaseToken).
		Updates(map[string]any{
			"attempts":        attempts,
			"next_attempt_at": nextAttemptAt,
			"last_error":      errorCategory,
			"lease_token":     "",
			"lease_until":     0,
		}).Error
}
