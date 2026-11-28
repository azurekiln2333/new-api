package model

import (
	"bytes"
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const usageEventRecoveryBatchLimit = 100

// ApplyUsageEvent projects an already-prepared source-ready occurrence. It
// never performs financial operations. A failure leaves the prepared fact
// intact and all projection changes rolled back.
func ApplyUsageEvent(ctx context.Context, receipt UsageEventReceipt) error {
	if ctx == nil || DB == nil {
		return errors.New("primary database and context are required")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return ApplyUsageEventTx(tx, receipt)
	})
}

// ApplyUsageEventTx uses the caller's transaction only. The caller must roll
// back that entire transaction on any returned error. Prepare and Apply in one
// transaction are durable only when the caller commits it successfully.
func ApplyUsageEventTx(tx *gorm.DB, receipt UsageEventReceipt) error {
	if tx == nil {
		return errors.New("primary transaction is unavailable")
	}
	if err := validateUsageEventReceipt(receipt); err != nil {
		return err
	}
	if err := reserveUsageEventSQLiteWriter(tx); err != nil {
		return err
	}
	var event UsageEvent
	if err := lockForUpdate(tx).Where("event_key = ?", receipt.EventKey).Take(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUsageEventNotFound
		}
		return err
	}
	if err := validateUsageEventRow(event); err != nil {
		return err
	}
	if !usageEventReceiptMatches(receipt, event) {
		return ErrUsageEventPayloadConflict
	}
	if event.AppliedVersion == UsageEventProjectionVersion {
		if event.SourceState != UsageEventSourceReady {
			return ErrUsageEventStateConflict
		}
		return nil
	}
	if event.AppliedVersion != 0 {
		return ErrUsageEventStateConflict
	}
	if event.SourceState != UsageEventSourceReady {
		return ErrUsageEventNotReady
	}

	// All target rows are created/locked in event -> channel -> channel/model
	// -> hour order. Hash conflicts always compare the full retained tuple.
	var channel UsageChannel
	var model UsageChannelModel
	var hour UsageHour
	if event.Kind == UsageEventKindConsume && event.Eligibility&UsageEventProjectChannel != 0 {
		channel = UsageChannel{ProjectionVersion: UsageEventProjectionVersion, ChannelID: event.ChannelID}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "projection_version"}, {Name: "channel_id"}}, DoNothing: true}).Create(&channel).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("projection_version = ? AND channel_id = ?", UsageEventProjectionVersion, event.ChannelID).Take(&channel).Error; err != nil {
			return err
		}
		if channel.ProjectionVersion != UsageEventProjectionVersion || channel.ChannelID != event.ChannelID {
			return ErrUsageEventDimensionConflict
		}

		model = usageChannelModelDimension(event)
		expected := model
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "projection_version"}, {Name: "channel_id"}, {Name: "model_key"}}, DoNothing: true}).Create(&model).Error; err != nil {
			return err
		}
		// Discard any generated ID from the attempted INSERT. A duplicate
		// driver's LastInsertID/RowsAffected is not ownership evidence.
		model = UsageChannelModel{}
		if err := lockForUpdate(tx).Where("projection_version = ? AND channel_id = ? AND model_key = ?", UsageEventProjectionVersion, event.ChannelID, expected.ModelKey).Take(&model).Error; err != nil {
			return err
		}
		if model.ProjectionVersion != expected.ProjectionVersion || model.ChannelID != expected.ChannelID || normalizeFixedText(model.ModelKey) != expected.ModelKey || model.ModelName != expected.ModelName || model.ModelKnown != expected.ModelKnown {
			return ErrUsageEventDimensionConflict
		}
	}
	if event.Kind == UsageEventKindConsume && event.Eligibility&UsageEventProjectHours != 0 {
		hour = usageHourDimension(event)
		expected := hour
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "projection_version"}, {Name: "dimension_key"}}, DoNothing: true}).Create(&hour).Error; err != nil {
			return err
		}
		hour = UsageHour{}
		if err := lockForUpdate(tx).Where("projection_version = ? AND dimension_key = ?", UsageEventProjectionVersion, expected.DimensionKey).Take(&hour).Error; err != nil {
			return err
		}
		if !sameUsageHourDimension(hour, expected) {
			return ErrUsageEventDimensionConflict
		}
	}

	// Compute every checked result before updating any counter. Refunds remain
	// positive facts and contribute nothing to these gross consume projections.
	var channelChanges, modelChanges, hourChanges map[string]any
	if event.Kind == UsageEventKindConsume && event.Eligibility&UsageEventProjectChannel != 0 {
		var err error
		channelChanges, err = usageLifetimeChanges(channel.ConsumeCount, channel.GrossQuota, channel.PromptTokens, channel.CompletionTokens, channel.LastUsedAt, event)
		if err != nil {
			return err
		}
		modelChanges, err = usageLifetimeChanges(model.ConsumeCount, model.GrossQuota, model.PromptTokens, model.CompletionTokens, model.LastUsedAt, event)
		if err != nil {
			return err
		}
	}
	if event.Kind == UsageEventKindConsume && event.Eligibility&UsageEventProjectHours != 0 {
		var err error
		hourChanges, err = usageHourChanges(hour, event)
		if err != nil {
			return err
		}
	}
	if channelChanges != nil {
		if err := tx.Model(&UsageChannel{}).Where("projection_version = ? AND channel_id = ?", UsageEventProjectionVersion, event.ChannelID).Updates(channelChanges).Error; err != nil {
			return err
		}
		if err := tx.Model(&UsageChannelModel{}).Where("id = ?", model.ID).Updates(modelChanges).Error; err != nil {
			return err
		}
	}
	if hourChanges != nil {
		if err := tx.Model(&UsageHour{}).Where("id = ?", hour.ID).Updates(hourChanges).Error; err != nil {
			return err
		}
	}
	now := common.GetTimestamp()
	return tx.Model(&UsageEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
		"applied_version": UsageEventProjectionVersion,
		"applied_at":      now,
		"updated_at":      now,
		"next_attempt_at": int64(0),
		"error_code":      "",
	}).Error
}

func usageChannelModelDimension(event UsageEvent) UsageChannelModel {
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-channel-model-key/v1", true)
	appendCanonicalInt64(&encoded, 1, UsageEventProjectionVersion, true)
	appendCanonicalInt64(&encoded, 2, event.ChannelID, true)
	known := usageKnown(event.KnownMask, UsageEventKnownModelName)
	appendCanonicalString(&encoded, 3, event.ModelName, known)
	return UsageChannelModel{
		ProjectionVersion: UsageEventProjectionVersion, ChannelID: event.ChannelID,
		ModelKey: digestCanonical(encoded.Bytes()), ModelName: event.ModelName, ModelKnown: known,
	}
}

func usageHourDimension(event UsageEvent) UsageHour {
	const dimensions = UsageEventKnownUserID | UsageEventKnownUsername | UsageEventKnownModelName | UsageEventKnownGroupName | UsageEventKnownTokenID | UsageEventKnownChannelID | UsageEventKnownExportNode
	known := event.KnownMask & dimensions
	hourStart := event.OccurredAt - event.OccurredAt%3600
	var encoded bytes.Buffer
	appendCanonicalString(&encoded, 0, "usage-hour-key/v1", true)
	appendCanonicalInt64(&encoded, 1, UsageEventProjectionVersion, true)
	appendCanonicalString(&encoded, 2, normalizeFixedText(event.CollectionEpoch), true)
	appendCanonicalInt64(&encoded, 3, hourStart, true)
	appendCanonicalInt64(&encoded, 4, event.UserID, usageKnown(known, UsageEventKnownUserID))
	appendCanonicalString(&encoded, 5, event.Username, usageKnown(known, UsageEventKnownUsername))
	appendCanonicalString(&encoded, 6, event.ModelName, usageKnown(known, UsageEventKnownModelName))
	appendCanonicalString(&encoded, 7, event.GroupName, usageKnown(known, UsageEventKnownGroupName))
	appendCanonicalInt64(&encoded, 8, event.TokenID, usageKnown(known, UsageEventKnownTokenID))
	appendCanonicalInt64(&encoded, 9, event.ChannelID, usageKnown(known, UsageEventKnownChannelID))
	appendCanonicalString(&encoded, 10, event.ExportNode, usageKnown(known, UsageEventKnownExportNode))
	appendCanonicalString(&encoded, 11, normalizeFixedText(event.SourceNamespace), true)
	return UsageHour{
		ProjectionVersion: UsageEventProjectionVersion, DimensionKey: digestCanonical(encoded.Bytes()),
		SourceNamespace: normalizeFixedText(event.SourceNamespace), CollectionEpoch: normalizeFixedText(event.CollectionEpoch), HourStart: hourStart,
		UserID: event.UserID, Username: event.Username, ModelName: event.ModelName, GroupName: event.GroupName,
		TokenID: event.TokenID, ChannelID: event.ChannelID, ExportNode: event.ExportNode, DimensionKnownMask: int64(known),
	}
}

func sameUsageHourDimension(stored, expected UsageHour) bool {
	return stored.ProjectionVersion == expected.ProjectionVersion && normalizeFixedText(stored.DimensionKey) == expected.DimensionKey &&
		normalizeFixedText(stored.SourceNamespace) == expected.SourceNamespace && normalizeFixedText(stored.CollectionEpoch) == expected.CollectionEpoch && stored.HourStart == expected.HourStart &&
		stored.UserID == expected.UserID && stored.Username == expected.Username && stored.ModelName == expected.ModelName &&
		stored.GroupName == expected.GroupName && stored.TokenID == expected.TokenID && stored.ChannelID == expected.ChannelID &&
		stored.ExportNode == expected.ExportNode && stored.DimensionKnownMask == expected.DimensionKnownMask
}

func usageLifetimeChanges(count, quota, prompt, completion, lastUsed int64, event UsageEvent) (map[string]any, error) {
	changes := make(map[string]any, 5)
	for _, field := range []struct {
		name   string
		stored int64
		delta  int64
	}{
		{"consume_count", count, 1},
		{"gross_quota", quota, event.QuotaMagnitude},
		{"prompt_tokens", prompt, event.PromptTokens},
		{"completion_tokens", completion, event.CompletionTokens},
	} {
		value, err := checkedUsageAdd(field.stored, field.delta)
		if err != nil {
			return nil, err
		}
		changes[field.name] = value
	}
	if lastUsed < 0 {
		return nil, ErrUsageEventInvalid
	}
	changes["last_used_at"] = max(lastUsed, event.OccurredAt)
	return changes, nil
}

func usageHourChanges(hour UsageHour, event UsageEvent) (map[string]any, error) {
	ranking, err := checkedUsageAdd(event.PromptTokens, event.CompletionTokens)
	if err != nil {
		return nil, err
	}
	input := event.PromptTokens
	if event.UsageSemantic == UsageSemanticAnthropic {
		cache, err := checkedUsageAdd(event.CacheReadTokens, event.CacheWriteTokens)
		if err != nil {
			return nil, err
		}
		ranking, err = checkedUsageAdd(ranking, cache)
		if err != nil {
			return nil, err
		}
		input, err = checkedUsageAdd(input, cache)
		if err != nil {
			return nil, err
		}
	}
	cacheUnknown, semanticUnknown := int64(0), int64(0)
	if !usageKnown(event.KnownMask, UsageEventKnownCacheReadTokens) || !usageKnown(event.KnownMask, UsageEventKnownCacheWriteTokens) {
		cacheUnknown = 1
	}
	if !usageKnown(event.KnownMask, UsageEventKnownUsageSemantic) {
		semanticUnknown = 1
	}
	changes := make(map[string]any, 11)
	for _, field := range []struct {
		name   string
		stored int64
		delta  int64
	}{
		{"consume_count", hour.ConsumeCount, 1},
		{"gross_quota", hour.GrossQuota, event.QuotaMagnitude},
		{"prompt_tokens", hour.PromptTokens, event.PromptTokens},
		{"completion_tokens", hour.CompletionTokens, event.CompletionTokens},
		{"ranking_tokens", hour.RankingTokens, ranking},
		{"cache_read_tokens", hour.CacheReadTokens, event.CacheReadTokens},
		{"cache_write_tokens", hour.CacheWriteTokens, event.CacheWriteTokens},
		{"input_denominator", hour.InputDenominator, input},
		{"cache_unknown_count", hour.CacheUnknownCount, cacheUnknown},
		{"semantic_unknown_count", hour.SemanticUnknownCount, semanticUnknown},
	} {
		value, err := checkedUsageAdd(field.stored, field.delta)
		if err != nil {
			return nil, err
		}
		changes[field.name] = value
	}
	return changes, nil
}

// RecoverUsageEventProjections retries at most one bounded page of due,
// source-ready facts. Pending/unknown source outcomes are never promoted. The
// returned count is successful Apply calls, not proof of global completeness.
func RecoverUsageEventProjections(ctx context.Context, limit int) (int, error) {
	if ctx == nil || DB == nil {
		return 0, errors.New("primary database and context are required")
	}
	if limit <= 0 {
		limit = usageEventRecoveryBatchLimit
	}
	limit = min(limit, usageEventRecoveryBatchLimit)
	now := common.GetTimestamp()
	var events []UsageEvent
	if err := DB.WithContext(ctx).Where("source_state = ? AND applied_version = ? AND next_attempt_at <= ?", UsageEventSourceReady, 0, now).
		Order("next_attempt_at, id").Limit(limit).Find(&events).Error; err != nil {
		return 0, err
	}
	applied := 0
	var resultErr error
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return applied, errors.Join(resultErr, err)
		}
		receipt := usageEventReceipt(event)
		err := ApplyUsageEvent(ctx, receipt)
		if err == nil {
			applied++
			continue
		}
		resultErr = errors.Join(resultErr, err)
		if retryErr := recordUsageEventApplyFailure(ctx, receipt, err, now); retryErr != nil {
			resultErr = errors.Join(resultErr, retryErr)
		}
	}
	return applied, resultErr
}

func recordUsageEventApplyFailure(ctx context.Context, receipt UsageEventReceipt, applyErr error, now int64) error {
	code := "statistics_apply_failed"
	switch {
	case errors.Is(applyErr, ErrUsageEventOverflow):
		code = "statistics_overflow"
	case errors.Is(applyErr, ErrUsageEventPayloadConflict), errors.Is(applyErr, ErrUsageEventIdentityConflict), errors.Is(applyErr, ErrUsageEventDimensionConflict):
		code = "statistics_conflict"
	case errors.Is(applyErr, ErrUsageEventInvalid), errors.Is(applyErr, ErrUsageEventStateConflict):
		code = "statistics_invalid"
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		var event UsageEvent
		if err := lockForUpdate(tx).Where("event_key = ?", receipt.EventKey).Take(&event).Error; err != nil {
			return err
		}
		if !usageEventReceiptMatches(receipt, event) {
			return ErrUsageEventPayloadConflict
		}
		if event.AppliedVersion != 0 || event.SourceState != UsageEventSourceReady {
			return nil
		}
		attempts, err := checkedUsageAdd(event.Attempts, 1)
		if err != nil {
			return err
		}
		// Bounded delay prevents a persistent bad event from pinning the first
		// due page forever. Retry data never replaces its immutable payload.
		delay := min(attempts, int64(10)) * 30
		nextAttempt, err := checkedUsageAdd(now, delay)
		if err != nil {
			return err
		}
		return tx.Model(&UsageEvent{}).Where("id = ?", event.ID).Updates(map[string]any{
			"attempts": attempts, "next_attempt_at": nextAttempt,
			"error_code": code, "updated_at": now,
		}).Error
	})
}
