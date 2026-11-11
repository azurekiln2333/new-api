package controller

import (
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// 用户级模型请求限流覆盖保存在系统选项 ModelRequestRateLimitUser 中：
// {"用户ID": [总次数, 成功次数]}。用户级覆盖优先于所在分组的限流，
// 清除覆盖即回到分组口径；时间窗沿用全局 ModelRequestRateLimitDurationMinutes。
const modelRequestRateLimitUserOptionKey = "ModelRequestRateLimitUser"

// userRateLimitOptionMutex 串行化本节点上的读取-修改-写回，避免并发管理操作互相覆盖。
// 跨节点仍按选项同步语义以最后一次写入为准。
var userRateLimitOptionMutex sync.Mutex

type userRateLimitPayload struct {
	TotalCount   int `json:"total_count"`
	SuccessCount int `json:"success_count"`
}

type userRateLimitState struct {
	UserId            int    `json:"user_id"`
	Group             string `json:"group"`
	Enabled           bool   `json:"enabled"`
	TotalCount        int    `json:"total_count"`
	SuccessCount      int    `json:"success_count"`
	GroupConfigured   bool   `json:"group_configured"`
	GroupTotalCount   int    `json:"group_total_count"`
	GroupSuccessCount int    `json:"group_success_count"`
}

// loadRateLimitTargetUser 解析路径里的用户 ID，并沿用用户管理接口的角色层级限制。
func loadRateLimitTargetUser(c *gin.Context) (*model.User, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		common.ApiErrorT(c, "No permission to access users of same or higher level")
		return nil, false
	}
	return user, true
}

func userRateLimitStateOf(user *model.User) userRateLimitState {
	state := userRateLimitState{
		UserId: user.Id,
		Group:  user.Group,
	}
	if totalCount, successCount, found := setting.GetUserRateLimit(user.Id); found {
		state.Enabled = true
		state.TotalCount = totalCount
		state.SuccessCount = successCount
	}
	if totalCount, successCount, found := setting.GetGroupRateLimit(user.Group); found {
		state.GroupConfigured = true
		state.GroupTotalCount = totalCount
		state.GroupSuccessCount = successCount
	}
	return state
}

// updateUserRateLimitOverrides 在选项值上施加一次修改，校验后写入选项表，
// 其它节点在下一次选项同步时生效。
func updateUserRateLimitOverrides(mutate func(overrides map[string][2]int)) error {
	userRateLimitOptionMutex.Lock()
	defer userRateLimitOptionMutex.Unlock()

	overrides := setting.GetUserRateLimitCopy()
	mutate(overrides)
	payload, err := common.Marshal(overrides)
	if err != nil {
		return err
	}
	if err := setting.CheckModelRequestRateLimitUser(string(payload)); err != nil {
		return err
	}
	return model.UpdateOption(modelRequestRateLimitUserOptionKey, string(payload))
}

func GetUserRateLimit(c *gin.Context) {
	user, ok := loadRateLimitTargetUser(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, userRateLimitStateOf(user))
}

func UpdateUserRateLimit(c *gin.Context) {
	user, ok := loadRateLimitTargetUser(c)
	if !ok {
		return
	}
	var payload userRateLimitPayload
	if err := common.DecodeJson(c.Request.Body, &payload); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if payload.TotalCount < 0 || payload.SuccessCount < 1 {
		common.ApiErrorT(c, "Total requests must be 0 or more, and successful requests must be 1 or more")
		return
	}
	err := updateUserRateLimitOverrides(func(overrides map[string][2]int) {
		overrides[strconv.Itoa(user.Id)] = [2]int{payload.TotalCount, payload.SuccessCount}
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "option.update", map[string]any{
		"key":           modelRequestRateLimitUserOptionKey,
		"total_count":   payload.TotalCount,
		"success_count": payload.SuccessCount,
	})
	common.ApiSuccess(c, userRateLimitStateOf(user))
}

func DeleteUserRateLimit(c *gin.Context) {
	user, ok := loadRateLimitTargetUser(c)
	if !ok {
		return
	}
	userId := strconv.Itoa(user.Id)
	if _, _, found := setting.GetUserRateLimit(user.Id); !found {
		common.ApiSuccess(c, userRateLimitStateOf(user))
		return
	}
	err := updateUserRateLimitOverrides(func(overrides map[string][2]int) {
		delete(overrides, userId)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, user.Id, "option.update", map[string]any{
		"key": modelRequestRateLimitUserOptionKey,
	})
	common.ApiSuccess(c, userRateLimitStateOf(user))
}

// ClearGroupUserRateLimits 只清除指定分组内已单独设置限流的用户，
// 其它分组的用户覆盖保持不变。
func ClearGroupUserRateLimits(c *gin.Context) {
	var payload struct {
		Group string `json:"group"`
	}
	if err := common.DecodeJson(c.Request.Body, &payload); err != nil || payload.Group == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	var userIds []int
	if err := model.DB.Model(&model.User{}).Where(&model.User{Group: payload.Group}).Pluck("id", &userIds).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	overrides := setting.GetUserRateLimitCopy()
	clearedIds := make([]int, 0, len(userIds))
	for _, userId := range userIds {
		if _, found := overrides[strconv.Itoa(userId)]; found {
			clearedIds = append(clearedIds, userId)
		}
	}
	cleared := map[string]any{"group": payload.Group, "cleared": len(clearedIds)}
	if len(clearedIds) == 0 {
		common.ApiSuccess(c, cleared)
		return
	}
	err := updateUserRateLimitOverrides(func(current map[string][2]int) {
		for _, userId := range clearedIds {
			delete(current, strconv.Itoa(userId))
		}
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "option.update", map[string]any{
		"key":           modelRequestRateLimitUserOptionKey,
		"group":         payload.Group,
		"cleared_users": len(clearedIds),
	})
	common.ApiSuccess(c, cleared)
}
