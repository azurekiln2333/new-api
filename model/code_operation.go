package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Management create operations are keyed by a client retry identity so that a
// lost response is resolved by reading the original batch instead of creating a
// second one. A pending identity row is claimed before any business row is
// inserted, and the whole unit commits in one primary database transaction.

const (
	CodeOperationInvitation = "invitation.create"
	CodeOperationRedemption = "redemption.create"

	codeOperationIDMaxLen   = 96
	codeOperationTypeMaxLen = 32

	// codeOperationStatePending marks a claimed identity whose rows are not yet
	// finalized. A pending row that is visible to another reader never is: it
	// only exists inside an uncommitted transaction.
	codeOperationStatePending = "pending"
	codeOperationStateDone    = "done"

	// CodeOperationTimeout bounds one create or one recovery read so a stalled
	// primary database cannot pin a request, a worker, or the connection pool.
	CodeOperationTimeout = 15 * time.Second
)

var (
	// ErrCodeBatchActorMismatch rejects a replay by someone other than the actor
	// who committed the batch. It is never a fresh create: a caller that did not
	// commit the batch must not harvest its keys.
	ErrCodeBatchActorMismatch = errors.New("management create operation belongs to another actor")
	// ErrCodeBatchConflict rejects a replay whose business payload differs from
	// the committed one, or whose operation type differs.
	ErrCodeBatchConflict = errors.New("management create operation conflicts with the committed request")
	// ErrCodeBatchInvalid rejects malformed operation identities.
	ErrCodeBatchInvalid = errors.New("management create operation request is invalid")
	// ErrCodeBatchRejected reports a rejected fresh create without exposing the
	// builder's error text, which may contain operator-supplied data.
	ErrCodeBatchRejected = errors.New("management create request rejected")
)

// ManagementOperationBatch is the durable create-batch identity. The single
// column unique index on OperationId is deliberate: a composite key would let a
// mismatched actor mint a parallel batch and succeed.
type ManagementOperationBatch struct {
	Id            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	OperationId   string `gorm:"type:varchar(96);not null;uniqueIndex:idx_management_operation_batch" json:"operation_id"`
	OperationType string `gorm:"type:varchar(32);not null;default:''" json:"operation_type"`
	State         string `gorm:"type:varchar(16);not null;default:''" json:"state"`
	RequestDigest string `gorm:"type:varchar(64);not null;default:''" json:"request_digest"`
	ActorUserId   int    `gorm:"not null;default:0" json:"actor_user_id"`
	ActorRole     int    `gorm:"not null;default:0" json:"actor_role"`
	RequestId     string `gorm:"type:varchar(64);not null;default:''" json:"request_id"`
	// ResultJson is an additional sensitive copy of the created keys. The rows
	// in invitations/redemptions remain the durable source; this projection is
	// only read back through the actor-bound replay path. Never log it.
	ResultJson string `gorm:"type:text;not null" json:"result_json"`
	CreatedAt  int64  `gorm:"not null;default:0" json:"created_at"`
}

func (ManagementOperationBatch) TableName() string { return "management_operation_batches" }

// CodeBatchRequest describes one atomic management create.
type CodeBatchRequest struct {
	// OperationId is the client retry key. When empty the server generates one,
	// returns it, and no replay promise is made for that client.
	OperationId   string
	OperationType string
	RequestDigest string
	ActorUserId   int
	ActorRole     int
	RequestId     string
	// Claim limits keep their existing meaning; this is the create identity.
	CreationBatchId string
	// Timeout bounds the whole create. Zero uses CodeOperationTimeout.
	Timeout time.Duration
	// BuildRows materializes the business rows. It runs only after the identity
	// row has been claimed in the same transaction.
	BuildRows func() (rows any, keys []string, err error)
	Audit     AuditEventDraft
}

// CodeBatchResult is returned for both the committed path and a replay.
type CodeBatchResult struct {
	OperationId     string   `json:"operation_id"`
	CreationBatchId string   `json:"creation_batch_id"`
	Keys            []string `json:"keys"`
	CreatedAt       int64    `json:"created_at"`
	Replayed        bool     `json:"replayed"`
}

// codeDigestField is one length-prefixed digest component.
type codeDigestField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CodeRequestDigest canonicalizes every business field of a create request.
// Each component is length-prefixed through a typed JSON encoding, so free-text
// values containing the separator or one another's content cannot collide.
// Generated keys, the operation identity, timestamps, and actor identity are
// excluded so a faithful retry digests identically.
func CodeRequestDigest(operationType string, fields map[string]string) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	ordered := make([]codeDigestField, len(names))
	for i, name := range names {
		ordered[i] = codeDigestField{Name: name, Value: fields[name]}
	}
	payload, err := common.Marshal(struct {
		OperationType string            `json:"operation_type"`
		Fields        []codeDigestField `json:"fields"`
	}{OperationType: operationType, Fields: ordered})
	if err != nil {
		// The payload is a closed set of strings; encoding cannot fail.
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func normalizeCodeOperationId(id string) (string, error) {
	if id == "" {
		return common.GetUUID(), nil
	}
	if len(id) > codeOperationIDMaxLen || strings.TrimSpace(id) != id {
		return "", ErrCodeBatchInvalid
	}
	return id, nil
}

// loadReplayedBatch applies the actor and payload checks to an existing
// identity. A mismatch is a rejection, never a fresh create.
func loadReplayedBatch(existing *ManagementOperationBatch, req CodeBatchRequest) (*CodeBatchResult, error) {
	if existing.OperationType != req.OperationType {
		return nil, ErrCodeBatchConflict
	}
	if existing.ActorUserId != req.ActorUserId {
		return nil, ErrCodeBatchActorMismatch
	}
	if existing.RequestDigest != req.RequestDigest {
		return nil, ErrCodeBatchConflict
	}
	if existing.State != codeOperationStateDone {
		// A claimed identity without a finalized result is not a committed batch.
		return nil, ErrCodeBatchConflict
	}
	var result CodeBatchResult
	if err := common.UnmarshalJsonStr(existing.ResultJson, &result); err != nil {
		return nil, ErrCodeBatchConflict
	}
	result.OperationId = existing.OperationId
	result.CreatedAt = existing.CreatedAt
	result.Replayed = true
	return &result, nil
}

// CreateCodeBatch commits the operation identity, the mutation rows, and the
// audit outbox row in one transaction.
func CreateCodeBatch(req CodeBatchRequest) (*CodeBatchResult, error) {
	operationId, err := normalizeCodeOperationId(req.OperationId)
	if err != nil {
		return nil, err
	}
	if len(req.OperationType) == 0 || len(req.OperationType) > codeOperationTypeMaxLen || len(req.RequestDigest) == 0 {
		return nil, ErrCodeBatchInvalid
	}
	if req.ActorUserId <= 0 || req.BuildRows == nil {
		return nil, ErrCodeBatchInvalid
	}
	req.OperationId = operationId

	// The whole create runs on one bounded context: a stalled primary database
	// must not pin the request or the pool without a deadline.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = CodeOperationTimeout
	}
	createCtx, cancelCreate := context.WithTimeout(context.Background(), timeout)
	defer cancelCreate()
	// Business rows and their result carry generated keys. Route every write on
	// this transaction through a discard logger so DB.Debug, a failed statement,
	// or GORM's error log can never print keys or result_json.
	txDb := DB.WithContext(createCtx).Session(&gorm.Session{Logger: logger.Discard})
	var result *CodeBatchResult
	err = txDb.Transaction(func(tx *gorm.DB) error {
		var existing ManagementOperationBatch
		found := tx.Where("operation_id = ?", operationId).First(&existing).Error
		if found == nil {
			replay, err := loadReplayedBatch(&existing, req)
			if err != nil {
				return err
			}
			result = replay
			return nil
		}
		if !errors.Is(found, gorm.ErrRecordNotFound) {
			return found
		}
		// Claim the identity before any business row exists, so a concurrent
		// submit blocks on the unique index instead of inserting a second batch.
		createdAt := common.GetTimestamp()
		if err := tx.Create(&ManagementOperationBatch{
			OperationId:   operationId,
			OperationType: req.OperationType,
			State:         codeOperationStatePending,
			RequestDigest: req.RequestDigest,
			ActorUserId:   req.ActorUserId,
			ActorRole:     req.ActorRole,
			RequestId:     req.RequestId,
			ResultJson:    "{}",
			CreatedAt:     createdAt,
		}).Error; err != nil {
			return err
		}
		rows, keys, err := req.BuildRows()
		if err != nil {
			return ErrCodeBatchRejected
		}
		if rows != nil {
			if err := tx.Create(rows).Error; err != nil {
				return err
			}
		}
		record := &CodeBatchResult{
			OperationId:     operationId,
			CreationBatchId: req.CreationBatchId,
			Keys:            keys,
			CreatedAt:       createdAt,
		}
		encoded, err := common.Marshal(record)
		if err != nil {
			return err
		}
		if err := tx.Model(&ManagementOperationBatch{}).
			Where("operation_id = ?", operationId).
			Updates(map[string]any{"state": codeOperationStateDone, "result_json": string(encoded)}).Error; err != nil {
			return err
		}
		if _, err := EnqueueAuditEventTx(tx, req.Audit, createdAt); err != nil {
			return err
		}
		result = record
		return nil
	})
	if err != nil {
		// Recovery is limited to an uncertain commit of THIS authorized request:
		// the same actor, operation type, and request digest, resolved by the same
		// operation id. An explicit rejection (actor mismatch, type conflict,
		// invalid request) is final and must never be resolved as success, or a
		// caller could harvest another actor's batch.
		if isCodeBatchRecoverable(err) {
			if replay, ok := resolveCodeBatchRace(req); ok {
				return replay, nil
			}
		}
		return nil, sanitizeCodeBatchError(err)
	}
	return result, nil
}

// isCodeBatchRecoverable reports whether the error can be an uncertain commit or
// a uniqueness race rather than a decided rejection.
func isCodeBatchRecoverable(err error) bool {
	return !errors.Is(err, ErrCodeBatchActorMismatch) &&
		!errors.Is(err, ErrCodeBatchConflict) &&
		!errors.Is(err, ErrCodeBatchInvalid) &&
		!errors.Is(err, ErrCodeBatchRejected)
}

// resolveCodeBatchRace re-reads the committed identity in a fresh transaction
// after an uncertain commit. It never creates, and it applies the same ownership
// and payload checks as the normal replay path, so a race can only ever recover
// the caller's own authorized batch.
func resolveCodeBatchRace(req CodeBatchRequest) (*CodeBatchResult, bool) {
	recoveryCtx, cancel := context.WithTimeout(context.Background(), CodeOperationTimeout)
	defer cancel()
	var existing ManagementOperationBatch
	if err := DB.WithContext(recoveryCtx).Where("operation_id = ?", req.OperationId).First(&existing).Error; err != nil {
		return nil, false
	}
	replay, err := loadReplayedBatch(&existing, req)
	if err != nil {
		return nil, false
	}
	return replay, true
}

// sanitizeCodeBatchError reduces a storage failure to a caller-safe sentinel. The
// driver error may embed SQL and bound parameters, including generated keys, so
// it is never returned to HTTP or written to a log by the caller.
func sanitizeCodeBatchError(err error) error {
	switch {
	case errors.Is(err, ErrCodeBatchActorMismatch),
		errors.Is(err, ErrCodeBatchConflict),
		errors.Is(err, ErrCodeBatchInvalid),
		errors.Is(err, ErrCodeBatchRejected):
		return err
	default:
		return ErrCodeBatchStorage
	}
}

// ErrCodeBatchStorage is the opaque failure a caller receives when the storage
// layer rejects the create. Details stay out of the response and out of logs.
var ErrCodeBatchStorage = errors.New("management create operation could not be committed")
