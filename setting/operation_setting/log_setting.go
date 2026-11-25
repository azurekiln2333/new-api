package operation_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	LogMaxRangeDaysOptionKey             = "log_setting.max_range_days"
	LogRetentionDaysOptionKey            = "log_setting.retention_days"
	RequestContentRetentionDaysOptionKey = "log_setting.request_content_retention_days"
	RequestContentEnabledOptionKey       = "log_setting.request_content_enabled"
	RequestContentMaxBytesOptionKey      = "log_setting.request_content_max_bytes"
	MaxLogRetentionDays                  = 36500
)

// LogSetting limits how far regular users can reach into usage logs.
type LogSetting struct {
	// UserExportEnabled lets regular users export their own usage logs.
	// Administrators can always export.
	UserExportEnabled bool `json:"user_export_enabled"`
	// MaxRangeDays is the longest time range a user can search in their logs
	// and dashboard, and the longest range any export can cover.
	MaxRangeDays int `json:"max_range_days"`
	// Zero preserves existing history until a root administrator opts in.
	RetentionDays               int  `json:"retention_days"`
	RequestContentRetentionDays int  `json:"request_content_retention_days"`
	RequestContentEnabled       bool `json:"request_content_enabled"`
	RequestContentMaxBytes      int  `json:"request_content_max_bytes"`
}

var logSetting = LogSetting{
	UserExportEnabled:      true,
	MaxRangeDays:           365,
	RequestContentEnabled:  true,
	RequestContentMaxBytes: common.RequestContentDefaultMaxBytes,
}

func init() {
	config.GlobalConfig.Register("log_setting", &logSetting)
}

func GetLogSetting() *LogSetting {
	return &logSetting
}

func (s *LogSetting) MaxRangeSeconds() int64 {
	return int64(max(s.MaxRangeDays, 1)) * 24 * 60 * 60
}

// LogRetentionDays snapshots the settings under the same lock as option writes.
func LogRetentionDays() (usage, content int) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return logSetting.RetentionDays, logSetting.RequestContentRetentionDays
}

func ValidateLogStorageOption(key, value string) error {
	switch key {
	case LogRetentionDaysOptionKey, RequestContentRetentionDaysOptionKey:
		days, err := strconv.Atoi(value)
		if err != nil || days < 0 || days > MaxLogRetentionDays {
			return common.NewMessage("Retention must be a whole number of days between 0 and 36500")
		}
	case RequestContentEnabledOptionKey:
		if value != "true" && value != "false" {
			return common.NewMessage("Invalid parameters")
		}
	case RequestContentMaxBytesOptionKey:
		bytes, err := strconv.Atoi(value)
		if err != nil || bytes < common.RequestContentMinMaxBytes || bytes > common.RequestContentMaxMaxBytes {
			return common.NewMessage("Request content limit must be between 256 and 134217728 bytes")
		}
	}
	return nil
}

func ValidateLogMaxRangeDays(value string) error {
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 {
		return common.NewMessage("The longest log time range must be at least 1 day")
	}
	return nil
}
