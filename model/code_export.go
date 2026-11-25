package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Management code export serves three distinct selections: an explicit id set
// chosen in the console, every row matching the current filters across all pages,
// and the rows belonging to one historical create batch. Each is bounded so a
// console request can never stream an unbounded result set.

const (
	// CodeExportMaxRows bounds one export request.
	CodeExportMaxRows = 5000
	// CodeExportPageSize bounds one read page inside an export scan.
	CodeExportPageSize = 500
)

var (
	// ErrCodeExportEmpty rejects an export with no usable selection.
	ErrCodeExportEmpty = errors.New("code export selection is empty")
	// ErrCodeExportTooLarge rejects an export above the row bound.
	ErrCodeExportTooLarge = errors.New("code export selection is too large")
)

// CodeExportFilter is the current console filter state, reused for a cross-page
// export so the download matches what the operator sees.
type CodeExportFilter struct {
	Keyword string
	Status  string
}

// RedemptionExportRow is the export projection. It intentionally omits user ids
// and timestamps the console does not display.
type RedemptionExportRow struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Quota       int    `json:"quota"`
	Status      int    `json:"status"`
	ExpiredTime int64  `json:"expired_time"`
	BatchId     string `json:"batch_id"`
}

// InvitationExportRow mirrors RedemptionExportRow for invitation codes.
type InvitationExportRow struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Status      int    `json:"status"`
	ExpiredTime int64  `json:"expired_time"`
}

func clampCodeExportLimit(limit int) int {
	if limit <= 0 || limit > CodeExportMaxRows {
		return CodeExportMaxRows
	}
	return limit
}

// applyRedemptionExportFilter mirrors SearchRedemptions so an export selected by
// filters returns exactly the rows the console listed.
func applyRedemptionExportFilter(query *gorm.DB, filter CodeExportFilter) *gorm.DB {
	if keyword := filter.Keyword; keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}
	now := common.GetTimestamp()
	// Boundary semantics follow model/redemption.go exactly: a code expires strictly
	// before now and is enabled at or after now, so an export boundary second matches
	// the console list instead of disagreeing by one second.
	switch filter.Status {
	case "expired":
		query = query.Where("status = ? AND expired_time != 0 AND expired_time < ?", common.RedemptionCodeStatusEnabled, now)
	case "1":
		query = query.Where("status = ? AND (expired_time = 0 OR expired_time >= ?)", common.RedemptionCodeStatusEnabled, now)
	case "2", "3":
		value, err := strconv.Atoi(filter.Status)
		if err != nil {
			return query.Where("1 = 0")
		}
		query = query.Where("status = ?", value)
	}
	return query
}

// ExportRedemptions returns at most limit rows for the requested selection.
// ids takes precedence; otherwise creationBatchId, otherwise the filter.
//
// A historical batch export is actor-bound: the operation identity's keys are
// reachable only by the operator who committed it, matching the replay path. There
// is no admin bypass, so one administrator cannot read another's codes by guessing
// an operation id.
func ExportRedemptions(ids []int, creationBatchId string, actorUserId int, filter CodeExportFilter, limit int) ([]RedemptionExportRow, error) {
	limit = clampCodeExportLimit(limit)
	query := DB.Model(&Redemption{})
	switch {
	case len(ids) > 0:
		if len(ids) > CodeExportMaxRows {
			return nil, ErrCodeExportTooLarge
		}
		query = query.Where("id IN ?", ids)
	case creationBatchId != "":
		// Creation-batch export reads the operation identity, which is separate
		// from the claim-limit BatchId on the rows.
		result, err := operationBatchKeysForActor(creationBatchId, actorUserId, CodeOperationRedemption)
		if err != nil {
			return nil, err
		}
		if len(result.Keys) > CodeExportMaxRows {
			return nil, ErrCodeExportTooLarge
		}
		query = query.Where(commonKeyCol+" IN ?", result.Keys)

	default:
		query = applyRedemptionExportFilter(query, filter)
	}
	var rows []RedemptionExportRow
	// Query one row past the bound so an oversized filtered export is reported
	// instead of silently returning a truncated file.
	err := query.Select("id", "name", commonKeyCol+" AS "+commonKeyCol, "quota", "status", "expired_time", "batch_id").
		Order("id desc").Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrCodeExportEmpty
	}
	if len(rows) > limit {
		return nil, ErrCodeExportTooLarge
	}
	return rows, nil
}

// applyInvitationExportFilter mirrors ListInvitations so a filtered export returns
// exactly the rows the console listed rather than a differently-matched prefix.
func applyInvitationExportFilter(query *gorm.DB, filter CodeExportFilter) (*gorm.DB, error) {
	keyword := strings.TrimSpace(filter.Keyword)
	if keyword != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword) + "%"
		query = query.Where("name LIKE ? ESCAPE '!' OR code = ?", pattern, keyword)
	}
	now := common.GetTimestamp()
	switch filter.Status {
	case "expired":
		query = query.Where("status = ? AND expired_time <> 0 AND expired_time <= ?", InvitationEnabled, now)
	case "1":
		query = query.Where("status = ? AND (expired_time = 0 OR expired_time > ?)", InvitationEnabled, now)
	case "2", "3":
		value, err := strconv.Atoi(filter.Status)
		if err != nil {
			return nil, ErrCodeExportEmpty
		}
		query = query.Where("status = ?", value)
	}
	return query, nil
}

// ExportInvitations mirrors ExportRedemptions for invitation codes, including the
// actor binding on a historical batch export.
func ExportInvitations(ids []int, creationBatchId string, actorUserId int, filter CodeExportFilter, limit int) ([]InvitationExportRow, error) {
	limit = clampCodeExportLimit(limit)
	query := DB.Model(&Invitation{})
	switch {
	case len(ids) > 0:
		if len(ids) > CodeExportMaxRows {
			return nil, ErrCodeExportTooLarge
		}
		query = query.Where("id IN ?", ids)
	case creationBatchId != "":
		result, err := operationBatchKeysForActor(creationBatchId, actorUserId, CodeOperationInvitation)
		if err != nil {
			return nil, err
		}
		if len(result.Keys) > CodeExportMaxRows {
			return nil, ErrCodeExportTooLarge
		}
		query = query.Where("code IN ?", result.Keys)
	default:
		var err error
		query, err = applyInvitationExportFilter(query, filter)
		if err != nil {
			return nil, err
		}
	}
	var rows []InvitationExportRow
	// "key" is a reserved word on PostgreSQL, so the alias is quoted too.
	err := query.Select("id", "name", "code AS "+commonKeyCol, "status", "expired_time").
		Order("id desc").Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrCodeExportEmpty
	}
	if len(rows) > limit {
		return nil, ErrCodeExportTooLarge
	}
	return rows, nil
}

// CodeBatchSummary is the history projection of one create batch. It carries the
// operation identity, which is what a retried or reloaded console uses to recover
// the original keys, plus the count so the list never implies key material.
type CodeBatchSummary struct {
	OperationId     string `json:"operation_id"`
	OperationType   string `json:"operation_type"`
	ActorUserId     int    `json:"actor_user_id"`
	CreatedAt       int64  `json:"created_at"`
	CreationBatchId string `json:"creation_batch_id"`
	Count           int    `json:"count"`
}

// ListOperationBatches lists committed create batches for one operator, newest
// first. Only the summary is returned: keys are recoverable through the replay
// path, never through a listing, so a lost create response is recoverable without
// widening key exposure.
func ListOperationBatches(operationType string, actorUserId, page, pageSize int) ([]*CodeBatchSummary, int64, error) {
	if actorUserId <= 0 {
		return nil, 0, ErrCodeExportEmpty
	}
	if page < 1 {
		page = 1
	}
	pageSize = min(max(pageSize, 1), 100)
	ctx, cancel := operationContext()
	defer cancel()
	query := DB.WithContext(ctx).Model(&ManagementOperationBatch{}).
		Where("operation_type = ? AND state = ? AND actor_user_id = ?", operationType, codeOperationStateDone, actorUserId)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// Clamp the page against the real total before computing an offset. A large
	// client-supplied page multiplied by pageSize would otherwise overflow int and
	// could turn into a negative or wrapped offset.
	lastPage := int((total + int64(pageSize) - 1) / int64(pageSize))
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}
	offset64 := int64(page-1) * int64(pageSize)
	if offset64 > int64(total) {
		return []*CodeBatchSummary{}, total, nil
	}
	var rows []*ManagementOperationBatch
	if err := query.Order("id desc").Limit(pageSize).Offset(int(offset64)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	summaries := make([]*CodeBatchSummary, 0, len(rows))
	for _, row := range rows {
		summary := &CodeBatchSummary{
			OperationId:   row.OperationId,
			OperationType: row.OperationType,
			ActorUserId:   row.ActorUserId,
			CreatedAt:     row.CreatedAt,
		}
		// CreationBatchId and the key count live in result_json, not in a column, so
		// both are projected from the decoded result. A row whose payload cannot be
		// decoded still appears in the listing with an empty batch id and a zero
		// count rather than vanishing from the operator's history.
		if result, err := decodeOperationKeys(row); err == nil {
			summary.CreationBatchId = result.CreationBatchId
			summary.Count = len(result.Keys)
		}
		summaries = append(summaries, summary)
	}
	return summaries, total, nil
}

func GetOperationBatch(operationId string) (*ManagementOperationBatch, error) {
	if operationId == "" {
		return nil, ErrCodeExportEmpty
	}
	ctx, cancel := operationContext()
	defer cancel()
	var batch ManagementOperationBatch
	err := DB.WithContext(ctx).Where("operation_id = ?", operationId).First(&batch).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCodeExportEmpty
		}
		return nil, err
	}
	if batch.State != codeOperationStateDone {
		return nil, ErrCodeExportEmpty
	}
	return &batch, nil
}

func decodeOperationKeys(batch *ManagementOperationBatch) (*CodeBatchResult, error) {
	var result CodeBatchResult
	if err := common.UnmarshalJsonStr(batch.ResultJson, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// operationBatchKeysForActor loads a committed create-batch identity and returns
// its keys only when the requesting operator committed it and the identity belongs
// to the expected code kind. A mismatched actor or mismatched kind gets the same
// error as a missing batch, so the endpoint cannot probe for the existence of
// another operator's batch, and an invitation export can never read a redemption
// batch's keys (or the reverse).
func operationBatchKeysForActor(operationId string, actorUserId int, expectedType string) (*CodeBatchResult, error) {
	batch, err := GetOperationBatch(operationId)
	if err != nil {
		return nil, err
	}
	if batch.ActorUserId != actorUserId || batch.OperationType != expectedType {
		return nil, ErrCodeExportEmpty
	}
	return decodeOperationKeys(batch)
}

// operationContext bounds one export-side identity read.
func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), CodeOperationTimeout)
}
