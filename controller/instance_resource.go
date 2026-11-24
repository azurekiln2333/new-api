package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/console_setting"

	"github.com/gin-gonic/gin"
)

// GetInstanceResourceSummary answers the viewer-safe instance resource summary
// available to every signed-in console user. It exposes only approved resource
// and state fields for the instances bound in the console API settings; the
// complete node record stays behind the root-only system-info API.
func GetInstanceResourceSummary(c *gin.Context) {
	instances, err := model.ListSystemInstances()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	bindings := consoleSettingBindings()
	// No process-wide monitoring flag is involved: each instance records its own
	// monitor state at sample time, so a remote node's payload is never judged by
	// this process's configuration.
	summary := model.BuildInstanceResourceSummary(instances, bindings, common.GetTimestamp())

	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    summary,
	})
}

func consoleSettingBindings() []model.ApiInfoBinding {
	entries := console_setting.ApiInfoBindings()
	bindings := make([]model.ApiInfoBinding, 0, len(entries))
	for _, entry := range entries {
		route, _ := entry["route"].(string)
		url, _ := entry["url"].(string)
		// The persistent entry id is assigned by the settings editor and
		// survives reordering.
		id, hasID := entry["id"].(float64)
		// Resource summary keys must come from persistent configured entry IDs.
		// Legacy entries without one are omitted until the settings editor saves
		// their assigned IDs; deriving an identity from a URL is not equivalent.
		if !hasID || id <= 0 {
			continue
		}
		bindings = append(bindings, model.ApiInfoBinding{
			ID:       int(id),
			Route:    route,
			Name:     url,
			NodeName: entryNodeName(entry),
		})
	}
	return bindings
}

func entryNodeName(entry map[string]any) string {
	nodeName, _ := entry["node_name"].(string)
	return nodeName
}

