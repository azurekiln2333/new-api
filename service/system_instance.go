package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const systemInstanceReportInterval = 30 * time.Second

var systemInstanceReporterOnce sync.Once

// systemInstanceReportMu orders the heartbeat and the writes after a sync, so
// an older snapshot never lands after a newer one.
var systemInstanceReportMu sync.Mutex

type SystemInstanceInfo struct {
	SchemaVersion int                       `json:"schema_version"`
	Node          common.NodeIdentity       `json:"node"`
	Role          SystemInstanceRoleInfo    `json:"role"`
	Runtime       SystemInstanceRuntimeInfo `json:"runtime"`
	Host          SystemInstanceHostInfo    `json:"host"`
	Resources     SystemInstanceResources   `json:"resources"`
	// Sync is what this node has applied of each sync topic.
	Sync  map[string]NodeSyncTopicState `json:"sync,omitempty"`
	Extra map[string]any                `json:"extra,omitempty"`
}

type SystemInstanceRoleInfo struct {
	IsMaster bool `json:"is_master"`
}

type SystemInstanceRuntimeInfo struct {
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	StartedAt int64  `json:"started_at"`
}

type SystemInstanceHostInfo struct {
	Hostname string `json:"hostname"`
}

type SystemInstanceResources struct {
	CPU     SystemInstanceResourceUsage  `json:"cpu"`
	Memory  SystemInstanceResourceUsage  `json:"memory"`
	Storage SystemInstanceStorageMetrics `json:"storage"`
	// Monitoring records whether the CPU/memory monitor was enabled on the
	// sampling node when this record was written. It is a pointer so an older
	// payload that omits the key is distinguishable from an explicit false, and
	// it is never derived from the reading process's own configuration.
	Monitoring *bool `json:"monitoring,omitempty"`
}

// SystemInstanceResourceUsage is one sampled percentage plus whether the
// sampling node actually measured it. Available is additive and optional so
// older payloads (which omit it) still decode; a false/absent Available means
// the percentage carries no meaning and readers must render it as unknown
// rather than as 0% usage.
type SystemInstanceResourceUsage struct {
	UsagePercent float64 `json:"usage_percent"`
	Available    bool    `json:"available"`
}

type SystemInstanceStorageMetrics struct {
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
	// Available reports whether the disk figures above were measured. Disk is
	// sampled independently of the CPU/memory monitor, so this can be true
	// while CPU and memory are unavailable.
	Available bool `json:"available"`
}

func StartSystemInstanceReporter() {
	systemInstanceReporterOnce.Do(func() {
		gopool.Go(func() {
			reportSystemInstanceWithLog()

			ticker := time.NewTicker(systemInstanceReportInterval)
			defer ticker.Stop()
			for range ticker.C {
				reportSystemInstanceWithLog()
			}
		})
	})
}

func ReportCurrentSystemInstance() error {
	systemInstanceReportMu.Lock()
	defer systemInstanceReportMu.Unlock()
	identity := common.GetNodeIdentity()
	hostname, hostnameErr := os.Hostname()
	if strings.TrimSpace(identity.Name) == "" {
		if hostnameErr != nil || strings.TrimSpace(hostname) == "" {
			return fmt.Errorf("system instance node name is empty")
		}
		identity.Name = hostname
		identity.Source = common.NodeNameSourceHostname
		identity.ManuallyConfigured = false
		identity.ShouldConfigureManually = true
	}
	systemStatus := common.GetSystemStatus()
	diskInfo := common.GetDiskSpaceInfo()
	var monitoring *bool
	if !systemStatus.SampledAt.IsZero() {
		monitoring = &systemStatus.SampledEnabled
	}
	// Availability comes from the sample itself, never from this process's
	// current configuration: a remote node may run with different settings, and
	// re-enabling a monitor must not turn an old disabled zero sample into a
	// fabricated "0% in use". Legacy payloads omit the flags and fall back to a
	// conservative positive-only reading.
	info := SystemInstanceInfo{
		SchemaVersion: 1,
		Node:          identity,
		Role: SystemInstanceRoleInfo{
			IsMaster: common.IsMasterNode,
		},
		Runtime: SystemInstanceRuntimeInfo{
			Version:   common.Version,
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			StartedAt: common.StartTime,
		},
		Host: SystemInstanceHostInfo{
			Hostname: hostname,
		},
		Resources: SystemInstanceResources{
			CPU: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.CPUUsage,
				Available:    systemStatus.CPUValid(),
			},
			Memory: SystemInstanceResourceUsage{
				UsagePercent: systemStatus.MemoryUsage,
				Available:    systemStatus.MemoryValid(),
			},
			Storage: SystemInstanceStorageMetrics{
				TotalBytes:  diskInfo.Total,
				UsedBytes:   diskInfo.Used,
				FreeBytes:   diskInfo.Free,
				UsedPercent: diskInfo.UsedPercent,
				// Disk is sampled independently of the CPU/memory monitor; a
				// successful read of a positive total is the validity condition.
				Available: diskInfo.Total > 0,
			},
			Monitoring: monitoring,
		},
		Sync: NodeSyncStates(),
	}
	return model.UpsertSystemInstance(identity.Name, info, common.StartTime, common.GetTimestamp())
}

func reportSystemInstanceWithLog() {
	if err := ReportCurrentSystemInstance(); err != nil {
		logger.LogWarn(context.Background(), common.LogText("system instance report failed: %v", err))
	}
}
