package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// Code export endpoints. Three selections share one shape so the console can use
// a single download component for both invitation and redemption codes:
//
//   ids              explicit selection (current page or across pages)
//   operation_id     one historical create batch
//   keyword/status   the active filters, exported across every page
//
// The row bound lives in the model layer; these handlers only validate input,
// choose the response format, and never echo key material into an error.

const codeExportFormats = "txt,md"

func codeExportFormat(c *gin.Context) string {
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "txt")))
	if format != "md" {
		return "txt"
	}
	return "md"
}

func parseCodeExportIds(c *gin.Context) ([]int, bool) {
	// Distinguish "no ids parameter" (filter export) from "ids present but empty",
	// which must be a hard error: treating it as absent would silently turn an
	// empty selection into an export of everything matching the filters.
	if !c.Request.URL.Query().Has("ids") {
		return nil, true
	}
	raw := strings.TrimSpace(c.Query("ids"))
	if raw == "" {
		return nil, false
	}
	parts := strings.Split(raw, ",")
	// An explicit selection must be honoured exactly. Dropping malformed ids or
	// truncating an oversized list would silently export a different set than the
	// operator selected, so both are hard errors.
	if len(parts) > model.CodeExportMaxRows {
		return nil, false
	}
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			return nil, false
		}
		value, err := strconv.Atoi(trimmed)
		if err != nil || value <= 0 {
			return nil, false
		}
		ids = append(ids, value)
	}
	return ids, true
}

func writeCodeExport(c *gin.Context, filename string, headers []string, rows [][]string, format string) {
	if format == "md" {
		lines := make([]string, 0, len(rows)+2)
		lines = append(lines, "| "+strings.Join(headers, " | ")+" |")
		separators := make([]string, len(headers))
		for i := range separators {
			separators[i] = "---"
		}
		lines = append(lines, "| "+strings.Join(separators, " | ")+" |")
		for _, row := range rows {
			lines = append(lines, "| "+strings.Join(row, " | ")+" |")
		}
		c.Header("Content-Type", "text/markdown; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="`+filename+`.md"`)
		c.String(http.StatusOK, "%s\n", strings.Join(lines, "\n"))
		return
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Join(row, "\t"))
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`.txt"`)
	c.String(http.StatusOK, "%s\n", strings.Join(lines, "\n"))
}

// sanitizeCodeExportCell removes tab and newline so one code cannot forge extra
// columns or rows in the downloaded file.
func sanitizeCodeExportCell(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}

func writeCodeExportError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrCodeExportEmpty):
		common.ApiErrorMsg(c, "Nothing to export for the current selection")
	case errors.Is(err, model.ErrCodeExportTooLarge):
		common.ApiErrorMsg(c, "The selection is too large to export at once")
	default:
		common.ApiErrorMsg(c, "Failed to export codes")
	}
}

// ExportRedemptions answers GET /api/redemption/export.
func ExportRedemptions(c *gin.Context) {
	filter := model.CodeExportFilter{
		Keyword: strings.TrimSpace(c.Query("keyword")),
		Status:  strings.TrimSpace(c.Query("status")),
	}
	ids, ok := parseCodeExportIds(c)
	if !ok {
		common.ApiErrorMsg(c, "The selection is invalid or too large to export at once")
		return
	}
	rows, err := model.ExportRedemptions(
		ids,
		strings.TrimSpace(c.Query("operation_id")),
		c.GetInt("id"),
		filter,
		model.CodeExportMaxRows,
	)
	if err != nil {
		writeCodeExportError(c, err)
		return
	}
	includeName := c.DefaultQuery("include_name", "true") != "false"
	includeQuota := c.DefaultQuery("include_quota", "true") != "false"
	headers := make([]string, 0, 4)
	if includeName {
		headers = append(headers, "Name")
	}
	headers = append(headers, "Code")
	if includeQuota {
		headers = append(headers, "Quota")
	}
	headers = append(headers, "Batch")
	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		line := make([]string, 0, 4)
		if includeName {
			line = append(line, sanitizeCodeExportCell(row.Name))
		}
		line = append(line, sanitizeCodeExportCell(row.Key))
		if includeQuota {
			line = append(line, strconv.Itoa(row.Quota))
		}
		line = append(line, sanitizeCodeExportCell(row.BatchId))
		cells = append(cells, line)
	}
	writeCodeExport(c, "redemption-codes-"+common.GetTimeString(), headers, cells, codeExportFormat(c))
}

// ExportInvitations answers GET /api/invitation/export.
func ExportInvitations(c *gin.Context) {
	filter := model.CodeExportFilter{
		Keyword: strings.TrimSpace(c.Query("keyword")),
		Status:  strings.TrimSpace(c.Query("status")),
	}
	ids, ok := parseCodeExportIds(c)
	if !ok {
		common.ApiErrorMsg(c, "The selection is invalid or too large to export at once")
		return
	}
	rows, err := model.ExportInvitations(
		ids,
		strings.TrimSpace(c.Query("operation_id")),
		c.GetInt("id"),
		filter,
		model.CodeExportMaxRows,
	)
	if err != nil {
		writeCodeExportError(c, err)
		return
	}
	includeName := c.DefaultQuery("include_name", "true") != "false"
	headers := make([]string, 0, 2)
	if includeName {
		headers = append(headers, "Name")
	}
	headers = append(headers, "Code")
	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		line := make([]string, 0, 2)
		if includeName {
			line = append(line, sanitizeCodeExportCell(row.Name))
		}
		line = append(line, sanitizeCodeExportCell(row.Key))
		cells = append(cells, line)
	}
	writeCodeExport(c, "invitation-codes-"+common.GetTimeString(), headers, cells, codeExportFormat(c))
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

// ListCodeBatches answers GET /api/{invitation,redemption}/batches. It lists the
// caller's committed create batches newest first so a lost response can be
// recovered from the console without knowing the operation id in advance.
func ListCodeBatches(operationType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		page := 1
		if value, err := strconv.Atoi(c.DefaultQuery("p", "1")); err == nil && value > 0 {
			page = value
		}
		pageSize := 20
		if value, err := strconv.Atoi(c.DefaultQuery("page_size", "20")); err == nil && value > 0 && value <= 100 {
			pageSize = value
		}
		batches, total, err := model.ListOperationBatches(operationType, c.GetInt("id"), page, pageSize)
		if err != nil {
			common.ApiErrorMsg(c, "Failed to load create batches")
			return
		}
		common.ApiSuccess(c, gin.H{"items": batches, "total": total})
	}
}
