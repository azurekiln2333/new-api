package controller

import (
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}

	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorT(c, "Redemption code name length must be between 1-20")
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorT(c, "Redemption code count must be greater than 0")
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorT(c, "Maximum 100 redemption codes can be generated at once")
		return
	}
	if redemption.Quota <= 0 {
		common.ApiErrorT(c, "Redemption quota must be positive")
		return
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		common.ApiError(c, err)
		return
	}
	batchId := ""
	if redemption.LimitPerUser {
		batchId = common.GetUUID()
	}
	snapshot := snapshotAuditActor(c)
	params := map[string]any{
		"name":           redemption.Name,
		"count":          redemption.Count,
		"quota":          logger.FormatQuota(redemption.Quota),
		"batch_id":       batchId,
		"limit_per_user": redemption.LimitPerUser,
	}
	// The digest covers every business create field. Generated keys, the
	// operation identity, the timestamp, and the actor identity are excluded so a
	// faithful client retry digests identically.
	digest := model.CodeRequestDigest(model.CodeOperationRedemption, map[string]string{
		"name":           redemption.Name,
		"count":          strconv.Itoa(redemption.Count),
		"quota":          strconv.FormatInt(int64(redemption.Quota), 10),
		"expired_time":   strconv.FormatInt(redemption.ExpiredTime, 10),
		"limit_per_user": strconv.FormatBool(redemption.LimitPerUser),
	})
	result, err := model.CreateCodeBatch(model.CodeBatchRequest{
		OperationId:     c.Query("operation_id"),
		OperationType:   model.CodeOperationRedemption,
		RequestDigest:   digest,
		ActorUserId:     snapshot.UserId,
		ActorRole:       snapshot.Role,
		RequestId:       snapshot.RequestId,
		CreationBatchId: batchId,
		BuildRows: func() (any, []string, error) {
			if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
				return nil, nil, model.ErrCodeBatchRejected
			}
			createdTime := common.GetTimestamp()
			rows := make([]model.Redemption, redemption.Count)
			createdKeys := make([]string, redemption.Count)
			for i := range redemption.Count {
				createdKeys[i] = common.GetUUID()
				rows[i] = model.Redemption{
					UserId:      snapshot.UserId,
					Name:        redemption.Name,
					Key:         createdKeys[i],
					CreatedTime: createdTime,
					Quota:       redemption.Quota,
					ExpiredTime: redemption.ExpiredTime,
					BatchId:     batchId,
				}
			}
			return rows, createdKeys, nil
		},
		Audit: buildAuditDraft(snapshot, "redemption.create", params),
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrCodeBatchActorMismatch), errors.Is(err, model.ErrCodeBatchConflict):
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("This operation was already used for a different request"))
		case errors.Is(err, model.ErrCodeBatchInvalid):
			common.ApiErrorT(c, "Invalid create operation request")
		case errors.Is(err, model.ErrCodeBatchRejected):
			common.ApiErrorT(c, "Expiration time cannot be earlier than current time")
		default:
			// The storage error may embed SQL and generated keys; only the opaque
			// sentinel is reported.
			common.ApiErrorT(c, "Failed to create redemption code, please try again later")
		}
		return
	}
	markAuditLogged(c)
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"message":        "",
		"data":           result.Keys,
		"operation_id":   result.OperationId,
		"replayed":       result.Replayed,
		"batch_id":       result.CreationBatchId,
		"creation_batch": result.CreationBatchId,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if statusOnly == "" {
		if redemption.Quota <= 0 {
			common.ApiErrorT(c, "Redemption quota must be positive")
			return
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
			common.ApiError(c, err)
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return common.NewMessage("Expiration time cannot be earlier than current time")
	}
	return nil
}

func DeleteRedemptionBatch(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	count, err := model.BatchDeleteRedemptions(request.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}
