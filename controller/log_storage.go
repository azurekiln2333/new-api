package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetLogStorageSummary is registered only in the root settings route group.
// Environment flags describe this serving node, not another node's process.
func GetLogStorageSummary(c *gin.Context) {
	enabled, maxBytes, enabledOverride, maxBytesOverride := common.RequestContentCaptureSettings()
	common.ApiSuccess(c, gin.H{
		"tables":                     model.GetLogStorageTables(c.Request.Context()),
		"capture_enabled":            enabled,
		"capture_max_bytes":          maxBytes,
		"capture_enabled_override":   enabledOverride,
		"capture_max_bytes_override": maxBytesOverride,
	})
}
