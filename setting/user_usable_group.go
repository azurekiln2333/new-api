package setting

import (
	"maps"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// The default descriptions are English source keys that the web console
// translates; descriptions an administrator saves are shown as written.
var userUsableGroups = map[string]string{
	"default": "Default group",
	"vip":     "VIP group",
}
var userUsableGroupsMutex sync.RWMutex

func GetUserUsableGroupsCopy() map[string]string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	copyUserUsableGroups := make(map[string]string)
	maps.Copy(copyUserUsableGroups, userUsableGroups)
	return copyUserUsableGroups
}

func UserUsableGroups2JSONString() string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	jsonBytes, err := common.Marshal(userUsableGroups)
	if err != nil {
		common.SysLog(common.LogText("error marshalling user groups: %s", err.Error()))
	}
	return string(jsonBytes)
}

func UpdateUserUsableGroupsByJSONString(jsonStr string) error {
	userUsableGroupsMutex.Lock()
	defer userUsableGroupsMutex.Unlock()

	userUsableGroups = make(map[string]string)
	return common.UnmarshalJsonStr(jsonStr, &userUsableGroups)
}

var groupLevels = map[string]int{}
var groupLevelsMutex sync.RWMutex

func GetGroupLevelsCopy() map[string]int {
	groupLevelsMutex.RLock()
	defer groupLevelsMutex.RUnlock()
	return maps.Clone(groupLevels)
}

func GroupLevels2JSONString() string {
	encoded, _ := common.Marshal(GetGroupLevelsCopy())
	return string(encoded)
}

func ParseGroupLevels(value string) (map[string]int, error) {
	var parsed map[string]*int
	if err := common.UnmarshalJsonStr(value, &parsed); err != nil || parsed == nil {
		return nil, common.NewMessage("Group levels must map group names to integers")
	}
	levels := make(map[string]int, len(parsed))
	for group, level := range parsed {
		if strings.TrimSpace(group) == "" || strings.TrimSpace(group) != group || group == "auto" || level == nil || *level < -2147483648 || *level > 2147483647 {
			return nil, common.NewMessage("Group levels must map group names to integers")
		}
		levels[group] = *level
	}
	return levels, nil
}

func UpdateGroupLevelsByJSONString(value string) error {
	levels, err := ParseGroupLevels(value)
	if err != nil {
		return err
	}
	groupLevelsMutex.Lock()
	defer groupLevelsMutex.Unlock()
	groupLevels = levels
	return nil
}

func GetUsableGroupDescription(groupName string) string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	if desc, ok := userUsableGroups[groupName]; ok {
		return desc
	}
	return groupName
}
