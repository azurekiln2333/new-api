package controller

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// RestoreChannelModelUnavailabilityRequest narrows which remembered upstream
// model refusals are cleared. The credential is named by its fingerprint, the
// same value channel details expose, so a reordered key list cannot restore the
// wrong entry. An absent body restores every observation of the channel.
type RestoreChannelModelUnavailabilityRequest struct {
	Model          string `json:"model"`
	KeyFingerprint string `json:"key_fingerprint"`
}

// RestoreChannelModelUnavailability clears remembered upstream model refusals so
// the next request probes the combination again. It changes no key, model or
// status: only the observation is removed.
func RestoreChannelModelUnavailability(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	request := RestoreChannelModelUnavailabilityRequest{}
	if bindErr := c.ShouldBindJSON(&request); bindErr != nil && !errors.Is(bindErr, io.EOF) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	restored, err := model.RestoreChannelModelUnavailability(id, strings.TrimSpace(request.Model), strings.TrimSpace(request.KeyFingerprint))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel.model_unavailable_restore", map[string]any{
		"id":       id,
		"restored": restored,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    restored,
	})
}
