package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func channelQuotaID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("Invalid channel ID"))
		return 0, false
	}
	var count int64
	if err := model.DB.WithContext(c.Request.Context()).Model(&model.Channel{}).Where("id = ?", id).Count(&count).Error; err != nil {
		channelQuotaError(c, err)
		return 0, false
	}
	if count == 0 {
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("Channel does not exist"))
		return 0, false
	}
	return id, true
}
func channelQuotaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrBudgetStaleVersion), errors.Is(err, model.ErrBudgetStaleRevision), errors.Is(err, model.ErrBudgetAttemptConflict):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "channel budget configuration version changed, reload and retry", "code": "channel_budget_version_conflict"})
	case errors.Is(err, model.ErrBudgetConfigInvalid):
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid channel budget configuration"))
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, errors.New("Channel does not exist"))
	default:
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errors.New("channel budget is not available"))
	}
}
func GetChannelQuota(c *gin.Context) {
	id, ok := channelQuotaID(c)
	if !ok {
		return
	}
	view, err := model.ReadChannelBudget(c.Request.Context(), model.BudgetScopeChannel, int64(id))
	if err != nil {
		channelQuotaError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}
func GetChannelQuotas(c *gin.Context) {
	parts := strings.Split(c.Query("ids"), ",")
	if len(parts) == 0 || len(parts) > 200 {
		channelQuotaError(c, model.ErrBudgetConfigInvalid)
		return
	}
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(part)
		if err != nil || id <= 0 {
			channelQuotaError(c, model.ErrBudgetConfigInvalid)
			return
		}
		ids = append(ids, id)
	}
	views, err := model.ListChannelBudgetViews(c.Request.Context(), ids)
	if err != nil {
		channelQuotaError(c, err)
		return
	}
	items := make([]*model.ChannelBudgetView, 0, len(ids))
	for _, id := range ids {
		items = append(items, views[id])
	}
	common.ApiSuccess(c, gin.H{"items": items})
}
func SetChannelQuota(c *gin.Context) {
	id, ok := channelQuotaID(c)
	if !ok {
		return
	}
	if !model.ChannelBudgetsEnabled() {
		common.ApiErrorStatus(c, http.StatusConflict, errors.New("Channel budget accounting is not enabled"))
		return
	}
	var body struct {
		Enabled *bool  `json:"enabled"`
		Limit   *int64 `json:"limit_quota"`
		Version *int64 `json:"expected_config_version"`
	}
	if common.DecodeJson(c.Request.Body, &body) != nil || body.Enabled == nil || body.Limit == nil || body.Version == nil || *body.Version < 0 {
		channelQuotaError(c, model.ErrBudgetConfigInvalid)
		return
	}
	view, err := model.SetChannelBudgetLimit(c.Request.Context(), model.BudgetScopeChannel, int64(id), *body.Version, *body.Enabled, *body.Limit)
	if err != nil {
		channelQuotaError(c, err)
		return
	}
	recordManageAudit(c, "channel.quota.set", map[string]any{"channel_id": id, "enabled": *body.Enabled, "limit_quota": *body.Limit, "config_version": view.ConfigVersion})
	common.ApiSuccess(c, view)
}
func ResetChannelQuota(c *gin.Context) {
	id, ok := channelQuotaID(c)
	if !ok {
		return
	}
	if !model.ChannelBudgetsEnabled() {
		common.ApiErrorStatus(c, http.StatusConflict, errors.New("Channel budget accounting is not enabled"))
		return
	}
	var body struct {
		Version     *int64 `json:"expected_config_version"`
		OperationID string `json:"operation_id"`
	}
	if common.DecodeJson(c.Request.Body, &body) != nil || body.Version == nil || *body.Version < 0 {
		channelQuotaError(c, model.ErrBudgetConfigInvalid)
		return
	}
	if body.OperationID == "" || len(body.OperationID) > 96 || strings.TrimSpace(body.OperationID) != body.OperationID {
		common.ApiErrorStatus(c, http.StatusBadRequest, errors.New("invalid budget reset operation id"))
		return
	}
	view, err := model.ResetChannelBudgetUsage(c.Request.Context(), model.BudgetResetRequest{Scope: model.BudgetScopeChannel, ScopeID: int64(id), ExpectedVersion: *body.Version, OperationID: body.OperationID})
	if err != nil {
		channelQuotaError(c, err)
		return
	}
	recordManageAudit(c, "channel.quota.reset", map[string]any{"channel_id": id, "config_version": view.ConfigVersion, "reset_epoch": view.ResetEpoch})
	common.ApiSuccess(c, view)
}
