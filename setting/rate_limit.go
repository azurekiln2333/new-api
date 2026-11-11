package setting

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// maxRateLimitDurationSeconds is the largest window the count cap is computed
// against (24h). Token-bucket capacity is count*duration; this keeps that
// product inside int64 when the window is at most a day.
const maxRateLimitDurationSeconds = 24 * 60 * 60

// maxModelRequestRateLimitCount is math.MaxInt64 / maxRateLimitDurationSeconds.
// It is the largest count that cannot overflow int64(count)*duration for a
// window of at most 24 hours.
const maxModelRequestRateLimitCount int64 = math.MaxInt64 / maxRateLimitDurationSeconds

var ModelRequestRateLimitEnabled = false
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000
var ModelRequestRateLimitGroup = map[string][2]int{}
var ModelRequestRateLimitMutex sync.RWMutex

// ModelRequestRateLimitUser holds per-user overrides of the group rate limit,
// keyed by user ID as the JSON object requires: {"12": [totalCount, successCount]}.
// A user entry takes precedence over the group limit; removing it falls back to
// the group. The time window is always the global ModelRequestRateLimitDurationMinutes.
var ModelRequestRateLimitUser = map[string][2]int{}
var ModelRequestRateLimitUserMutex sync.RWMutex

func ModelRequestRateLimitGroup2JSONString() string {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := common.Marshal(ModelRequestRateLimitGroup)
	if err != nil {
		common.SysLog(common.LogText("error marshalling model ratio: %s", err.Error()))
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	ModelRequestRateLimitGroup = make(map[string][2]int)
	return common.Unmarshal([]byte(jsonStr), &ModelRequestRateLimitGroup)
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	if ModelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := ModelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func ModelRequestRateLimitUser2JSONString() string {
	ModelRequestRateLimitUserMutex.RLock()
	defer ModelRequestRateLimitUserMutex.RUnlock()

	jsonBytes, err := common.Marshal(ModelRequestRateLimitUser)
	if err != nil {
		common.SysLog(common.LogText("error marshalling user rate limit: %s", err.Error()))
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitUserByJSONString(jsonStr string) error {
	// Parse before swapping so a malformed value never wipes the active overrides.
	parsed := make(map[string][2]int)
	if err := common.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return err
	}

	ModelRequestRateLimitUserMutex.Lock()
	defer ModelRequestRateLimitUserMutex.Unlock()
	ModelRequestRateLimitUser = parsed
	return nil
}

func GetUserRateLimit(userId int) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitUserMutex.RLock()
	defer ModelRequestRateLimitUserMutex.RUnlock()

	limits, found := ModelRequestRateLimitUser[strconv.Itoa(userId)]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

// GetUserRateLimitCopy returns a snapshot of the per-user overrides so callers
// can build an updated value without racing the option sync.
func GetUserRateLimitCopy() map[string][2]int {
	ModelRequestRateLimitUserMutex.RLock()
	defer ModelRequestRateLimitUserMutex.RUnlock()

	copied := make(map[string][2]int, len(ModelRequestRateLimitUser))
	maps.Copy(copied, ModelRequestRateLimitUser)
	return copied
}

func CheckModelRequestRateLimitUser(jsonStr string) error {
	checkModelRequestRateLimitUser := make(map[string][2]int)
	err := common.Unmarshal([]byte(jsonStr), &checkModelRequestRateLimitUser)
	if err != nil {
		return err
	}
	for userId, limits := range checkModelRequestRateLimitUser {
		parsedUserId, err := strconv.Atoi(userId)
		if err != nil || parsedUserId <= 0 || strconv.Itoa(parsedUserId) != userId {
			return fmt.Errorf("invalid user id %q in user rate limits", userId)
		}
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf("user %s has negative rate limit values: [%d, %d]", userId, limits[0], limits[1])
		}
		if int64(limits[0]) > maxModelRequestRateLimitCount || int64(limits[1]) > maxModelRequestRateLimitCount {
			return fmt.Errorf("user %s [%d, %d] exceeds max rate limit %d", userId, limits[0], limits[1], maxModelRequestRateLimitCount)
		}
	}

	return nil
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	checkModelRequestRateLimitGroup := make(map[string][2]int)
	err := common.Unmarshal([]byte(jsonStr), &checkModelRequestRateLimitGroup)
	if err != nil {
		return err
	}
	for group, limits := range checkModelRequestRateLimitGroup {
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf("group %s has negative rate limit values: [%d, %d]", group, limits[0], limits[1])
		}
		if int64(limits[0]) > maxModelRequestRateLimitCount || int64(limits[1]) > maxModelRequestRateLimitCount {
			return fmt.Errorf("group %s [%d, %d] exceeds max rate limit %d", group, limits[0], limits[1], maxModelRequestRateLimitCount)
		}
	}

	return nil
}
