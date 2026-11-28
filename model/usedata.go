package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id                  int    `json:"id"`
	UserID              int    `json:"user_id" gorm:"index"`
	Username            string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName           string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	UseGroup            string `json:"use_group" gorm:"index;size:64;default:''"`
	TokenID             int    `json:"token_id" gorm:"index;default:0"`
	ChannelID           int    `json:"channel_id" gorm:"index;default:0"`
	NodeName            string `json:"node_name" gorm:"index;size:64;default:''"`
	TokenUsed           int    `json:"token_used" gorm:"default:0"`
	Count               int    `json:"count" gorm:"default:0"`
	Quota               int    `json:"quota" gorm:"default:0"`
	CacheReadTokens     int64  `json:"cache_read_tokens" gorm:"default:0"`
	CacheCreationTokens int64  `json:"cache_creation_tokens" gorm:"default:0"`
}

type QuotaDataLogParams struct {
	UserID              int
	Username            string
	ModelName           string
	Quota               int
	CreatedAt           int64
	TokenUsed           int
	CacheReadTokens     int64
	CacheCreationTokens int64
	UseGroup            string
	TokenID             int
	ChannelID           int
	NodeName            string
}

func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog(common.LogText("updating dashboard data..."))
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

// Keyed retry evidence never merges with anonymous legacy buffers. A retry
// retains the same immutable occurrence, even if activation ran meanwhile.
var pendingUsageLegacyReports = make(map[string]UsageSourceReport)

func logQuotaDataCache(quotaData *QuotaData) {
	key := fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s\x00%d\x00%d\x00%s",
		quotaData.UserID,
		quotaData.Username,
		quotaData.ModelName,
		quotaData.CreatedAt,
		quotaData.UseGroup,
		quotaData.TokenID,
		quotaData.ChannelID,
		quotaData.NodeName,
	)
	count := quotaData.Count
	quota := quotaData.Quota
	tokenUsed := quotaData.TokenUsed
	cachedQuotaData, ok := CacheQuotaData[key]
	if ok {
		cachedQuotaData.Count += count
		cachedQuotaData.Quota += quota
		cachedQuotaData.TokenUsed += tokenUsed
		cachedQuotaData.CacheReadTokens += quotaData.CacheReadTokens
		cachedQuotaData.CacheCreationTokens += quotaData.CacheCreationTokens
		quotaData = cachedQuotaData
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(params QuotaDataLogParams) {
	// Anonymous buffers are pre-upgrade evidence only. Upgraded tracked reports
	// use persistUsageLegacyReport, whose acknowledgement commits with SQL.
	// 只精确到小时
	createdAt := params.CreatedAt - (params.CreatedAt % 3600)
	quotaData := &QuotaData{
		UserID:              params.UserID,
		Username:            params.Username,
		ModelName:           params.ModelName,
		CreatedAt:           createdAt,
		UseGroup:            params.UseGroup,
		TokenID:             params.TokenID,
		ChannelID:           params.ChannelID,
		NodeName:            params.NodeName,
		Count:               1,
		Quota:               params.Quota,
		TokenUsed:           params.TokenUsed,
		CacheReadTokens:     max(0, params.CacheReadTokens),
		CacheCreationTokens: max(0, params.CacheCreationTokens),
	}

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	allowed, err := LegacyQuotaDataWriterAllowed(context.Background())
	if err != nil || !allowed {
		common.SysError(common.LogText("legacy dashboard buffer rejected after usage statistics fence"))
		return
	}
	logQuotaDataCache(quotaData)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	for key, report := range pendingUsageLegacyReports {
		if err := persistUsageLegacyReport(context.Background(), &report); err != nil {
			common.SysError(common.LogText("cannot persist legacy dashboard ownership; retaining pending data"))
			continue
		}
		delete(pendingUsageLegacyReports, key)
	}
	if len(CacheQuotaData) == 0 {
		return
	}
	if DB == nil {
		common.SysError(common.LogText("cannot verify quota_data writer fence; retaining pending dashboard data"))
		return
	}
	// The whole anonymous flush either commits under the activation anchor or
	// retains its buffer. Never clear a partially failed flush or mutate a
	// baseline after its digest has been installed.
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockUsageStatisticsTransition(tx); err != nil {
			return err
		}
		lineage, err := getUsageReadLineage(tx)
		if err != nil {
			return err
		}
		if lineage.IncludeLegacyQuotaData {
			return ErrUsageStatisticsNotReady
		}
		for _, row := range CacheQuotaData {
			if _, err := writeLegacyQuotaDataTx(tx, *row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.SysError(common.LogText("cannot verify quota_data writer fence; retaining pending dashboard data: %v", err))
		return
	}
	CacheQuotaData = make(map[string]*QuotaData)
}

// writeLegacyQuotaDataTx is called only while holding the durable transition
// anchor. Checked absolute assignments avoid silent overflow on every dialect.
func writeLegacyQuotaDataTx(tx *gorm.DB, incoming QuotaData) (int64, error) {
	var stored QuotaData
	err := lockForUpdate(tx).Where("user_id = ? AND username = ? AND model_name = ? AND created_at = ? AND use_group = ? AND token_id = ? AND channel_id = ? AND node_name = ?",
		incoming.UserID, incoming.Username, incoming.ModelName, incoming.CreatedAt, incoming.UseGroup, incoming.TokenID, incoming.ChannelID, incoming.NodeName).
		Order("id").Take(&stored).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	changes := make(map[string]any, 5)
	for _, field := range []struct {
		name  string
		value int64
		delta int64
	}{
		{"count", int64(stored.Count), int64(incoming.Count)},
		{"quota", int64(stored.Quota), int64(incoming.Quota)},
		{"token_used", int64(stored.TokenUsed), int64(incoming.TokenUsed)},
		{"cache_read_tokens", stored.CacheReadTokens, incoming.CacheReadTokens},
		{"cache_creation_tokens", stored.CacheCreationTokens, incoming.CacheCreationTokens},
	} {
		value, err := checkedUsageAdd(field.value, field.delta)
		if err != nil {
			return 0, err
		}
		changes[field.name] = value
	}
	if stored.Id == 0 {
		incoming.Id = 0
		if err := tx.Create(&incoming).Error; err != nil {
			return 0, err
		}
		return int64(incoming.Id), nil
	}
	return int64(stored.Id), tx.Model(&QuotaData{}).Where("id = ?", stored.Id).Updates(changes).Error
}

// mirrorUsageLegacyReport preserves the pre-cutover dashboard when export is
// enabled. Failure keeps keyed retry evidence; optional logging never owns it.
func mirrorUsageLegacyReport(ctx context.Context, report *UsageSourceReport) error {
	if err := report.validate(); err != nil {
		return err
	}
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	if err := persistUsageLegacyReport(ctx, report); err != nil {
		if len(pendingUsageLegacyReports) < usageReadPendingLimit {
			pendingUsageLegacyReports[report.Input.EventKey] = *report
		}
		// The finalized primary fact remains recoverable and unowned even if
		// this bounded optional mirror queue cannot retain another retry.
		return err
	}
	delete(pendingUsageLegacyReports, report.Input.EventKey)
	return nil
}

func persistUsageLegacyReport(ctx context.Context, report *UsageSourceReport) error {
	if ctx == nil || DB == nil || report.validate() != nil {
		return ErrUsageEventInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUsageStatisticsTransition(tx); err != nil {
			return err
		}
		lineage, err := getUsageReadLineage(tx)
		if err != nil {
			return err
		}
		if lineage.IncludeLegacyQuotaData {
			// The original fact is still the authority after the fence. No
			// late report or buffer is allowed to alter the frozen baseline.
			return nil
		}
		var event UsageEvent
		if err := lockForUpdate(tx).Where("event_key = ?", report.Input.EventKey).Take(&event).Error; err != nil {
			return err
		}
		if validateUsageEventRow(event) != nil || usageEventInputFromRow(event) != report.Input ||
			event.SourceState != UsageEventSourceReady || event.Kind != UsageEventKindConsume ||
			event.Eligibility&UsageEventProjectHours == 0 {
			return ErrUsageStatisticsNotReady
		}
		if event.ActivationGeneration != usageDashboardOwnershipVersion {
			return ErrUsageStatisticsNotReady
		}
		// quota_data cannot retain unknown dimensions or wider historical names.
		// Keep such ready facts durable-only rather than truncate or certify zero.
		const legacyDimensions = UsageEventKnownUserID | UsageEventKnownUsername | UsageEventKnownModelName | UsageEventKnownGroupName | UsageEventKnownTokenID | UsageEventKnownChannelID | UsageEventKnownExportNode
		if event.KnownMask&legacyDimensions != legacyDimensions || len(event.Username) > 64 || len(event.ModelName) > 64 || len(event.GroupName) > 64 || len(event.ExportNode) > 64 {
			return nil
		}
		var acknowledgement UsageLegacyAcknowledgement
		err = tx.Where("event_key = ?", event.EventKey).Take(&acknowledgement).Error
		if err == nil {
			if normalizeFixedText(acknowledgement.PayloadDigest) != report.digest || acknowledgement.QuotaDataID <= 0 {
				return ErrUsageEventPayloadConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := ApplyUsageEventTx(tx, usageEventReceipt(event)); err != nil {
			return err
		}
		// Project the SAME finalized source payload to both legacy and exact
		// covered-hour rows. No public log metadata or current prices enter here.
		dimension := usageHourDimension(event)
		measures, err := usageHourChanges(dimension, event)
		if err != nil {
			return err
		}
		quotaDataID, err := writeLegacyQuotaDataTx(tx, QuotaData{
			UserID: int(event.UserID), Username: event.Username, ModelName: event.ModelName, CreatedAt: dimension.HourStart,
			UseGroup: event.GroupName, TokenID: int(event.TokenID), ChannelID: int(event.ChannelID), NodeName: event.ExportNode,
			Count: 1, Quota: int(event.QuotaMagnitude), TokenUsed: int(measures["ranking_tokens"].(int64)),
			CacheReadTokens: event.CacheReadTokens, CacheCreationTokens: event.CacheWriteTokens,
		})
		if err != nil {
			return err
		}
		candidate := map[string]any{
			"dimension_key": dimension.DimensionKey, "projection_version": dimension.ProjectionVersion,
			"source_namespace": dimension.SourceNamespace, "collection_epoch": dimension.CollectionEpoch,
			"hour_start": dimension.HourStart, "user_id": dimension.UserID, "username": dimension.Username,
			"model_name": dimension.ModelName, "group_name": dimension.GroupName, "token_id": dimension.TokenID,
			"channel_id": dimension.ChannelID, "export_node": dimension.ExportNode, "dimension_known_mask": dimension.DimensionKnownMask,
			"consume_count": int64(0), "gross_quota": int64(0), "prompt_tokens": int64(0), "completion_tokens": int64(0),
			"ranking_tokens": int64(0), "cache_read_tokens": int64(0), "cache_write_tokens": int64(0), "input_denominator": int64(0),
			"cache_unknown_count": int64(0), "semantic_unknown_count": int64(0),
		}
		if err := tx.Model(&UsageLegacyHour{}).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dimension_key"}}, DoNothing: true}).Create(candidate).Error; err != nil {
			return err
		}
		var covered UsageHour
		if err := lockForUpdate(tx).Table("usage_legacy_hours").Where("dimension_key = ?", dimension.DimensionKey).Take(&covered).Error; err != nil {
			return err
		}
		if !sameUsageHourDimension(covered, dimension) {
			return ErrUsageEventDimensionConflict
		}
		changes, err := usageHourChanges(covered, event)
		if err != nil {
			return err
		}
		if err := tx.Table("usage_legacy_hours").Where("dimension_key = ?", dimension.DimensionKey).Updates(changes).Error; err != nil {
			return err
		}
		return tx.Create(&UsageLegacyAcknowledgement{EventKey: event.EventKey, PayloadDigest: report.digest, QuotaDataID: quotaDataID}).Error
	})
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyQuotaDataByUsername(username, startTime, endTime)
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{Username: username, FilterUsername: true}, []string{"user_id", "username", "model_name", "created_at"})
	if err != nil {
		return nil, err
	}
	return usageDashboardQuotaData(rows)
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	if userId <= 0 {
		return nil, errInvalidLogUserScope
	}
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyQuotaDataByUserID(userId, startTime, endTime)
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{UserID: userId}, []string{"user_id", "username", "model_name", "created_at"})
	if err != nil {
		return nil, err
	}
	return usageDashboardQuotaData(rows)
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyQuotaDataGroupByUser(startTime, endTime)
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{}, []string{"username", "created_at"})
	if err != nil {
		return nil, err
	}
	return usageDashboardQuotaData(rows)
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	installed, err := UsageReadLineageInstalled(context.Background())
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := getLegacyQuotaDataAll(startTime, endTime)
		installed, err = UsageReadLineageInstalled(context.Background())
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	rows, err := readUsageDashboard(context.Background(), startTime, endTime, usageDashboardFilter{}, []string{"model_name", "created_at"})
	if err != nil {
		return nil, err
	}
	return usageDashboardQuotaData(rows)
}

func getLegacyQuotaDataByUsername(username string, startTime, endTime int64) ([]*QuotaData, error) {
	var rows []*QuotaData
	err := DB.Table("quota_data").Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Group("user_id, username, model_name, created_at").Find(&rows).Error
	return rows, err
}

func getLegacyQuotaDataByUserID(userID int, startTime, endTime int64) ([]*QuotaData, error) {
	var rows []*QuotaData
	err := DB.Table("quota_data").Select("user_id, username, model_name, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").Where("user_id = ? and created_at >= ? and created_at <= ?", userID, startTime, endTime).Group("user_id, username, model_name, created_at").Find(&rows).Error
	return rows, err
}

func getLegacyQuotaDataGroupByUser(startTime, endTime int64) ([]*QuotaData, error) {
	var rows []*QuotaData
	err := DB.Table("quota_data").Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("username, created_at").Find(&rows).Error
	return rows, err
}

func getLegacyQuotaDataAll(startTime, endTime int64) ([]*QuotaData, error) {
	var rows []*QuotaData
	err := DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, sum(cache_read_tokens) as cache_read_tokens, sum(cache_creation_tokens) as cache_creation_tokens, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&rows).Error
	return rows, err
}
