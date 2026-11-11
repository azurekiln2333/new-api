package controller

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

// Keep this resource registration with its controllers; startup registers it
// before the authorization catalog is seeded.
func init() {
	auditContentTemplates["invitation.create"] = "Created invitation codes"
	auditContentTemplates["invitation.update"] = "Updated an invitation code"
	auditContentTemplates["invitation.delete"] = "Deleted an invitation code"
	auditContentTemplates["invitation.delete_batch"] = "Batch deleted invitation codes"
	auditContentTemplates["invitation.delete_invalid"] = "Deleted invalid invitation codes"
	authz.RegisterResource(authz.ResourceDefinition{Resource: "invitation", LabelKey: "Invitation codes", Actions: []authz.ActionDefinition{
		{Action: authz.ActionRead, LabelKey: "View invitation codes", DescriptionKey: "View and export invitation codes", DefaultRoles: []string{authz.BuiltInRoleAdmin}},
		{Action: authz.ActionWrite, LabelKey: "Manage invitation codes", DescriptionKey: "Create, edit and delete invitation codes", DefaultRoles: []string{authz.BuiltInRoleAdmin}},
	}})
}

func ListInvitations(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows, total, err := model.ListInvitations(c.Query("keyword"), c.Query("status"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

func AddInvitations(c *gin.Context) {
	var request struct {
		Name        string `json:"name"`
		Count       int    `json:"count"`
		ExpiredTime int64  `json:"expired_time"`
	}
	if common.DecodeJson(c.Request.Body, &request) != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if utf8.RuneCountInString(request.Name) < 1 || utf8.RuneCountInString(request.Name) > 80 || request.Count < 1 || request.Count > 100 {
		common.ApiErrorT(c, "Use a name of 1–80 characters and generate 1–100 invitation codes")
		return
	}
	if request.ExpiredTime != 0 && request.ExpiredTime <= common.GetTimestamp() {
		common.ApiErrorT(c, "Expiration time cannot be earlier than current time")
		return
	}
	keys, err := model.CreateInvitations(c.GetInt("id"), request.Name, request.Count, request.ExpiredTime)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	recordManageAudit(c, "invitation.create", map[string]any{"count": request.Count})
	common.ApiSuccess(c, keys)
}

func UpdateInvitation(c *gin.Context) {
	var request struct {
		Id          int    `json:"id"`
		Name        string `json:"name"`
		ExpiredTime int64  `json:"expired_time"`
		Status      *int   `json:"status"`
	}
	if common.DecodeJson(c.Request.Body, &request) != nil || request.Id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Status != nil {
		if *request.Status != model.InvitationEnabled && *request.Status != model.InvitationDisabled {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	} else if utf8.RuneCountInString(request.Name) < 1 || utf8.RuneCountInString(request.Name) > 80 || request.ExpiredTime < 0 || (request.ExpiredTime != 0 && request.ExpiredTime <= common.GetTimestamp()) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if err := model.UpdateInvitation(request.Id, request.Name, request.ExpiredTime, request.Status); err != nil {
		common.ApiErrorT(c, "Invitation code cannot be changed")
		return
	}
	recordManageAudit(c, "invitation.update", map[string]any{"id": request.Id})
	common.ApiSuccess(c, nil)
}

func DeleteInvitation(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	count, err := model.DeleteInvitations([]int{id}, false)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	recordManageAudit(c, "invitation.delete", map[string]any{"id": id})
	common.ApiSuccess(c, count)
}

func DeleteInvitationBatch(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids"`
	}
	if common.DecodeJson(c.Request.Body, &request) != nil || len(request.Ids) == 0 || len(request.Ids) > 1000 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	for _, id := range request.Ids {
		if id <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
	}
	count, err := model.DeleteInvitations(request.Ids, false)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	recordManageAudit(c, "invitation.delete_batch", map[string]any{"count": count})
	common.ApiSuccess(c, count)
}

func DeleteInvalidInvitations(c *gin.Context) {
	count, err := model.DeleteInvitations(nil, true)
	if err != nil {
		common.ApiErrorT(c, "Database error, please contact the administrator")
		return
	}
	recordManageAudit(c, "invitation.delete_invalid", map[string]any{"count": count})
	common.ApiSuccess(c, count)
}
