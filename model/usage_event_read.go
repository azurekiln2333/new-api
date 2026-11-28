package model

import (
	"context"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const usageReadPendingLimit = 1000

// Numeric readers return proven durable contributions even when historical
// capture is partial. Serving callers must attach GetUsageStatisticsCoverage
// to the response; absence of a fact is not a whole-history zero certificate.
// Unknown measures and a truncated pending tail still return an error.

// usageFactQuery preserves the log search contract on primary-database facts.
// LIKE escaping deliberately uses the primary relational dialect even when
// the separately configured diagnostic database is ClickHouse.
func usageFactQuery(ctx context.Context, filter LogFilter) (*gorm.DB, UsageEventKnownMask, error) {
	if ctx == nil || DB == nil {
		return nil, 0, ErrUsageStatisticsNotReady
	}
	switch filter.UserScope {
	case "":
		if filter.ScopeUserId != 0 {
			return nil, 0, errInvalidLogUserScope
		}
	case LogUserScopeAll:
	case LogUserScopeSelf, LogUserScopeOthers:
		if filter.ScopeUserId <= 0 {
			return nil, 0, errInvalidLogUserScope
		}
	default:
		return nil, 0, errInvalidLogUserScope
	}
	query := DB.WithContext(ctx).Model(&UsageEvent{}).
		Where("source_state = ? AND kind = ?", UsageEventSourceReady, UsageEventKindConsume)
	var required UsageEventKnownMask
	if filter.UserId != 0 {
		required |= UsageEventKnownUserID
		query = query.Where("(user_id = ? OR (known_mask & ?) = 0)", filter.UserId, UsageEventKnownUserID)
	}
	if filter.UserScope == LogUserScopeSelf || filter.UserScope == LogUserScopeOthers {
		required |= UsageEventKnownUserID
		operator := "="
		if filter.UserScope == LogUserScopeOthers {
			operator = "<>"
		}
		query = query.Where("(user_id "+operator+" ? OR (known_mask & ?) = 0)", filter.ScopeUserId, UsageEventKnownUserID)
	}
	for _, field := range []struct {
		column  string
		value   string
		pattern bool
		known   UsageEventKnownMask
	}{
		{"model_name", filter.ModelName, true, UsageEventKnownModelName},
		{"username", filter.Username, true, UsageEventKnownUsername},
		{"token_name", filter.TokenName, false, UsageEventKnownTokenName},
		{"group_name", filter.Group, false, UsageEventKnownGroupName},
		{"request_id", filter.RequestId, false, UsageEventKnownRequestID},
		{"upstream_request_id", filter.UpstreamRequestId, false, UsageEventKnownUpstreamRequestID},
	} {
		if field.value == "" {
			continue
		}
		required |= field.known
		condition, value := field.column+" = ?", field.value
		if filter.MatchMode != LogMatchExact {
			value = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
			condition, value = field.column+" LIKE ? ESCAPE '!'", "%"+value+"%"
		} else if field.pattern && strings.Contains(value, "%") {
			var err error
			value, err = sanitizeLikePattern(value)
			if err != nil {
				return nil, 0, err
			}
			condition = field.column+" LIKE ? ESCAPE '!'"
		}
		query = query.Where("("+condition+" OR (known_mask & ?) = 0)", value, field.known)
	}
	if filter.Channel != 0 {
		required |= UsageEventKnownChannelID
		query = query.Where("(channel_id = ? OR (known_mask & ?) = 0)", filter.Channel, UsageEventKnownChannelID)
	}
	if filter.StartTimestamp != 0 {
		query = query.Where("occurred_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp != 0 {
		query = query.Where("occurred_at <= ?", filter.EndTimestamp)
	}
	return query, required, nil
}

type usageFactTotals struct {
	Quota            int64
	Prompt           int64
	Completion       int64
	CacheRead        int64
	AnthropicRead    int64
	AnthropicWrite   int64
	RecentCount      int64
	RecentPrompt     int64
	RecentCompletion int64
	UnknownCount     int64
}

func readUsageFactTotals(ctx context.Context, filter LogFilter, cache bool) (usageFactTotals, error) {
	var totals usageFactTotals
	query, required, err := usageFactQuery(ctx, filter)
	if err != nil {
		return totals, err
	}
	if _, err := GetUsageStatisticsCoverage(ctx, UsageStatisticsFacts, filter.StartTimestamp, filter.EndTimestamp); err != nil {
		return totals, err
	}
	required |= UsageEventKnownQuota | UsageEventKnownPromptTokens | UsageEventKnownCompletionTokens
	if cache {
		required |= UsageEventKnownCacheReadTokens | UsageEventKnownCacheWriteTokens | UsageEventKnownUsageSemantic
	}
	// Aggregate the individual measures before adding them with checked Go
	// arithmetic. SQL SUM overflow or an out-of-range scan is a read failure.
	recent := common.GetTimestamp() - 60
	err = query.Select(`COALESCE(SUM(quota_magnitude), 0) AS quota,
		COALESCE(SUM(prompt_tokens), 0) AS prompt,
		COALESCE(SUM(completion_tokens), 0) AS completion,
		COALESCE(SUM(cache_read_tokens), 0) AS cache_read,
		COALESCE(SUM(CASE WHEN usage_semantic = ? THEN cache_read_tokens ELSE 0 END), 0) AS anthropic_read,
		COALESCE(SUM(CASE WHEN usage_semantic = ? THEN cache_write_tokens ELSE 0 END), 0) AS anthropic_write,
		COALESCE(SUM(CASE WHEN occurred_at >= ? THEN 1 ELSE 0 END), 0) AS recent_count,
		COALESCE(SUM(CASE WHEN occurred_at >= ? THEN prompt_tokens ELSE 0 END), 0) AS recent_prompt,
		COALESCE(SUM(CASE WHEN occurred_at >= ? THEN completion_tokens ELSE 0 END), 0) AS recent_completion,
		COALESCE(SUM(CASE WHEN (known_mask & ?) <> ? THEN 1 ELSE 0 END), 0) AS unknown_count`,
		UsageSemanticAnthropic, UsageSemanticAnthropic, recent, recent, recent, required, required).Scan(&totals).Error
	if err != nil {
		return usageFactTotals{}, err
	}
	if totals.UnknownCount != 0 {
		return usageFactTotals{}, ErrUsageStatisticsNotReady
	}
	return totals, nil
}

// SumUsageEventQuota reads immutable source-ready facts, including facts whose
// projection is still pending. No diagnostic-log switch or retention applies.
func SumUsageEventQuota(ctx context.Context, filter LogFilter) (Stat, error) {
	totals, err := readUsageFactTotals(ctx, filter, true)
	if err != nil {
		return Stat{}, err
	}
	input, err := checkedUsageAdd(totals.Prompt, totals.AnthropicRead)
	if err != nil {
		return Stat{}, err
	}
	input, err = checkedUsageAdd(input, totals.AnthropicWrite)
	if err != nil {
		return Stat{}, err
	}
	tpm, err := checkedUsageAdd(totals.RecentPrompt, totals.RecentCompletion)
	if err != nil || tpm > math.MaxInt || totals.Quota < 0 || totals.Quota > math.MaxInt || totals.RecentCount < 0 || totals.RecentCount > math.MaxInt {
		return Stat{}, ErrUsageEventOverflow
	}
	return Stat{Quota: int(totals.Quota), Rpm: int(totals.RecentCount), Tpm: int(tpm), CacheReadTokens: totals.CacheRead, InputTokens: input}, nil
}

func SumUsageEventTokens(ctx context.Context, filter LogFilter) (int, error) {
	totals, err := readUsageFactTotals(ctx, filter, false)
	if err != nil {
		return 0, err
	}
	total, err := checkedUsageAdd(totals.Prompt, totals.Completion)
	if err != nil || total > math.MaxInt {
		return 0, ErrUsageEventOverflow
	}
	return int(total), nil
}

// usageChannelReadRow is one projection or one ready, unapplied tail event.
// Both halves are read in ONE SQL statement, so Apply cannot move an event
// between two independently observed snapshots and lose or double-count it.
type usageChannelReadRow struct {
	ChannelID        int
	ModelName        string
	ModelKnown       bool
	ConsumeCount     int64
	GrossQuota       int64
	PromptTokens     int64
	CompletionTokens int64
	LastUsedAt       int64
	Pending          int64
}

func readUsageChannels(ctx context.Context, ids []int, models bool) ([]usageChannelReadRow, error) {
	if _, err := GetUsageStatisticsCoverage(ctx, UsageStatisticsChannels, 0, 0); err != nil {
		return nil, err
	}
	base := DB.WithContext(ctx).Model(&UsageChannel{}).
		Select("channel_id, '' AS model_name, ? AS model_known, consume_count, gross_quota, prompt_tokens, completion_tokens, last_used_at, 0 AS pending", true).
		Where("projection_version = ? AND channel_id IN ?", UsageEventProjectionVersion, ids)
	if models {
		base = DB.WithContext(ctx).Model(&UsageChannelModel{}).
			Select("channel_id, model_name, model_known, consume_count, gross_quota, prompt_tokens, completion_tokens, last_used_at, 0 AS pending").
			Where("projection_version = ? AND channel_id IN ?", UsageEventProjectionVersion, ids)
	}
	tail := DB.WithContext(ctx).Model(&UsageEvent{}).
		Select("channel_id, model_name, (known_mask & ?) <> 0 AS model_known, 1 AS consume_count, quota_magnitude AS gross_quota, prompt_tokens, completion_tokens, occurred_at AS last_used_at, 1 AS pending", UsageEventKnownModelName).
		Where("channel_id IN ? AND source_state = ? AND applied_version = 0 AND kind = ? AND (eligibility & ?) <> 0", ids, UsageEventSourceReady, UsageEventKindConsume, UsageEventProjectChannel).
		Order("occurred_at, id").Limit(usageReadPendingLimit + 1)
	combined := DB.WithContext(ctx).Raw("SELECT * FROM (?) AS projected UNION ALL SELECT * FROM (?) AS pending", base, tail)
	query := DB.WithContext(ctx).Table("(?) AS usage_data", combined)
	if models {
		// Keep native text grouping semantics instead of imposing byte equality
		// on a deployment whose relational model-name collation is insensitive.
		query = query.Select("channel_id, model_name, model_known, SUM(consume_count) AS consume_count, SUM(gross_quota) AS gross_quota, SUM(prompt_tokens) AS prompt_tokens, SUM(completion_tokens) AS completion_tokens, MAX(last_used_at) AS last_used_at, SUM(pending) AS pending").
			Group("channel_id, model_name, model_known")
	}
	var rows []usageChannelReadRow
	err := query.Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var pending int64
	for _, row := range rows {
		pending += row.Pending
		if models && !row.ModelKnown {
			return nil, ErrUsageStatisticsNotReady
		}
	}
	if pending > usageReadPendingLimit {
		return nil, ErrUsageStatisticsNotReady
	}
	return rows, nil
}

type usageDashboardFilter struct {
	UserID         int
	Username       string
	ModelName      string
	FilterUsername bool
	FilterModel    bool
	NonemptyGroup  bool
	NonemptyModel  bool
	RankingLimit   int
}

type usageDashboardRow struct {
	UserID              int
	Username            string
	ModelName           string
	CreatedAt           int64
	UseGroup            string
	TokenID             int
	ChannelID           int
	NodeName            string
	Count               int64
	Quota               int64
	TokenUsed           int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	Pending             int64
	Invalid             int64
}

// readUsageDashboard combines the fenced legacy baseline with the explicitly
// disjoint live lineage. Imported historical logs are never added to hours.
// Projection rows and the bounded ready tail share a single statement snapshot.
func readUsageDashboard(ctx context.Context, start, end int64, filter usageDashboardFilter, dimensions []string) ([]usageDashboardRow, error) {
	installed, err := UsageReadLineageInstalled(ctx)
	if err != nil {
		return nil, err
	}
	if !installed {
		legacy, legacyErr := readLegacyUsageDashboard(ctx, start, end, filter, dimensions)
		installed, err = UsageReadLineageInstalled(ctx)
		if err != nil {
			return nil, err
		}
		if !installed {
			return legacy, legacyErr
		}
	}
	if _, err := GetUsageStatisticsCoverage(ctx, UsageStatisticsDashboard, start, end); err != nil {
		return nil, err
	}
	lineage, err := GetUsageReadLineage(ctx)
	if err != nil {
		return nil, err
	}
	var dimensionMask UsageEventKnownMask
	for _, dimension := range dimensions {
		switch dimension {
		case "user_id":
			dimensionMask |= UsageEventKnownUserID
		case "username":
			dimensionMask |= UsageEventKnownUsername
		case "model_name":
			dimensionMask |= UsageEventKnownModelName
		case "use_group":
			dimensionMask |= UsageEventKnownGroupName
		case "token_id":
			dimensionMask |= UsageEventKnownTokenID
		case "channel_id":
			dimensionMask |= UsageEventKnownChannelID
		case "node_name":
			dimensionMask |= UsageEventKnownExportNode
		}
	}
	if filter.UserID != 0 {
		dimensionMask |= UsageEventKnownUserID
	}
	if filter.FilterUsername || filter.Username != "" {
		dimensionMask |= UsageEventKnownUsername
	}
	if filter.FilterModel || filter.ModelName != "" || filter.NonemptyModel {
		dimensionMask |= UsageEventKnownModelName
	}
	if filter.NonemptyGroup {
		dimensionMask |= UsageEventKnownGroupName
	}
	const columns = "user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name, count, quota, token_used, cache_read_tokens, cache_creation_tokens, dimension_known_mask, pending, invalid"
	// The attested legacy baseline filters its stored dimensions by value;
	// the all-bits-set mask preserves that contract, including empty strings.
	legacy := DB.WithContext(ctx).Model(&QuotaData{}).
		Select("user_id, username, model_name, created_at, use_group, token_id, channel_id, node_name, count, quota, token_used, cache_read_tokens, cache_creation_tokens, -1 AS dimension_known_mask, 0 AS pending, 0 AS invalid").
		Where("id <= ?", lineage.LegacyQuotaDataMaxID)
	if !lineage.IncludeLegacyQuotaData {
		legacy = legacy.Where("1 = 0")
	}
	// An acknowledgement is committed only with Apply and the actual legacy
	// aggregate addition. Remove exactly its counters from the matching hour;
	// no timestamp cutoff or unexplained residual estimates overlap.
	hourColumns := []string{
		"h.user_id", "h.username", "h.model_name", "h.hour_start", "h.group_name", "h.token_id", "h.channel_id", "h.export_node", "h.dimension_known_mask",
	}
	ownershipInvalid := make([]string, 0, 10)
	for _, column := range []string{"consume_count", "gross_quota", "prompt_tokens", "completion_tokens", "ranking_tokens", "cache_read_tokens", "cache_write_tokens", "input_denominator", "cache_unknown_count", "semantic_unknown_count"} {
		owned := "COALESCE(owned." + column + ", 0)"
		// Even baseline-owned contributions retain their observed unknownness.
		// quota_data cannot encode it, so do not turn absent cache/semantic
		// evidence into known zeros by removing those markers with the value.
		if column == "cache_unknown_count" || column == "semantic_unknown_count" {
			hourColumns = append(hourColumns, "h."+column)
		} else {
			hourColumns = append(hourColumns, "h."+column+" - "+owned+" AS "+column)
		}
		ownershipInvalid = append(ownershipInvalid, "h."+column+" < "+owned)
	}
	hourColumns = append(hourColumns, "CASE WHEN "+strings.Join(ownershipInvalid, " OR ")+" THEN 1 ELSE 0 END AS ownership_invalid")
	netHours := DB.WithContext(ctx).Table("usage_hours AS h").
		Joins("LEFT JOIN usage_legacy_hours AS owned ON owned.dimension_key = h.dimension_key").
		Select(strings.Join(hourColumns, ", ")).
		Where("h.projection_version = ? AND h.source_namespace = ? AND h.collection_epoch = ?", UsageEventProjectionVersion, lineage.LiveSourceNamespace, lineage.LiveCollectionEpoch)
	hours := DB.WithContext(ctx).Table("(?) AS unowned", netHours).
		Select("user_id, username, model_name, hour_start AS created_at, group_name AS use_group, token_id, channel_id, export_node AS node_name, consume_count AS count, gross_quota AS quota, ranking_tokens AS token_used, cache_read_tokens, cache_write_tokens AS cache_creation_tokens, dimension_known_mask, 0 AS pending, CASE WHEN ownership_invalid <> 0 OR cache_unknown_count <> 0 OR semantic_unknown_count <> 0 OR (dimension_known_mask & ?) <> ? THEN 1 ELSE 0 END AS invalid", dimensionMask, dimensionMask).
		Where("consume_count <> 0 OR ownership_invalid <> 0 OR cache_unknown_count <> 0 OR semantic_unknown_count <> 0 OR (dimension_known_mask & ?) <> ?", dimensionMask, dimensionMask)
	// Rows with unknown token semantics cannot masquerade as observed zero.
	required := dimensionMask | UsageEventKnownPromptTokens | UsageEventKnownCompletionTokens | UsageEventKnownQuota | UsageEventKnownCacheReadTokens | UsageEventKnownCacheWriteTokens | UsageEventKnownUsageSemantic
	hourExpr := "occurred_at - (occurred_at % 3600)"
	tail := DB.WithContext(ctx).Model(&UsageEvent{}).
		Select("user_id, username, model_name, "+hourExpr+" AS created_at, group_name AS use_group, token_id, channel_id, export_node AS node_name, 1 AS count, quota_magnitude AS quota, prompt_tokens + completion_tokens + CASE WHEN usage_semantic = ? THEN cache_read_tokens + cache_write_tokens ELSE 0 END AS token_used, cache_read_tokens, cache_write_tokens AS cache_creation_tokens, known_mask AS dimension_known_mask, 1 AS pending, CASE WHEN (known_mask & ?) <> ? THEN 1 ELSE 0 END AS invalid", UsageSemanticAnthropic, required, required).
		Where("source_state = ? AND applied_version = 0 AND kind = ? AND source_namespace = ? AND collection_epoch = ? AND (eligibility & ?) <> 0", UsageEventSourceReady, UsageEventKindConsume, lineage.LiveSourceNamespace, lineage.LiveCollectionEpoch, UsageEventProjectHours)
	parts := []*gorm.DB{legacy, hours, tail}
	for i, part := range parts {
		part = DB.WithContext(ctx).Table("(?) AS usage_part", part)
		if start > 0 {
			part = part.Where("created_at >= ?", start)
		}
		if end > 0 {
			part = part.Where("created_at <= ?", end)
		}
		// Unknown measures cannot widen a known dimension. Retain uncertain
		// attribution only for the dimension currently being filtered.
		if filter.UserID != 0 {
			part = part.Where("(user_id = ? OR (dimension_known_mask & ?) = 0)", filter.UserID, UsageEventKnownUserID)
		}
		if filter.FilterUsername || filter.Username != "" {
			part = part.Where("(username = ? OR (dimension_known_mask & ?) = 0)", filter.Username, UsageEventKnownUsername)
		}
		if filter.FilterModel || filter.ModelName != "" {
			part = part.Where("(model_name = ? OR (dimension_known_mask & ?) = 0)", filter.ModelName, UsageEventKnownModelName)
		}
		if filter.NonemptyGroup {
			part = part.Where("(use_group <> '' OR (dimension_known_mask & ?) = 0)", UsageEventKnownGroupName)
		}
		if filter.NonemptyModel {
			part = part.Where("(model_name <> '' OR (dimension_known_mask & ?) = 0)", UsageEventKnownModelName)
		}
		if i == 2 {
			part = part.Limit(usageReadPendingLimit + 1)
		}
		parts[i] = part.Select(columns)
	}
	combined := DB.WithContext(ctx).Raw("SELECT * FROM (?) AS legacy UNION ALL SELECT * FROM (?) AS hours UNION ALL SELECT * FROM (?) AS pending", parts[0], parts[1], parts[2])
	selection := strings.Join(dimensions, ", ")
	if selection != "" {
		selection += ", "
	}
	selection += "COALESCE(SUM(count), 0) AS count, COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(token_used), 0) AS token_used, COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens, COALESCE(SUM(pending), 0) AS pending, COALESCE(SUM(invalid), 0) AS invalid"
	query := DB.WithContext(ctx).Table("(?) AS usage_data", combined).Select(selection)
	if len(dimensions) > 0 {
		query = query.Group(strings.Join(dimensions, ", "))
	}
	if filter.RankingLimit > 0 {
		query = query.Order("token_used DESC, user_id ASC")
	}
	cursor, err := query.Rows()
	if err != nil {
		return nil, err
	}
	defer cursor.Close()
	rows := make([]usageDashboardRow, 0)
	var pending int64
	for cursor.Next() {
		var row usageDashboardRow
		if err := DB.ScanRows(cursor, &row); err != nil {
			return nil, err
		}
		pending += row.Pending
		if row.Invalid != 0 {
			return nil, ErrUsageStatisticsNotReady
		}
		if row.Count < 0 || row.Quota < 0 || row.TokenUsed < 0 || row.CacheReadTokens < 0 || row.CacheCreationTokens < 0 {
			return nil, ErrUsageEventOverflow
		}
		// Inspect every group's availability and sentinel, but retain only the
		// bounded leaderboard. A SQL limit before these checks could hide an
		// overflowed tail or unknown contribution outside the leading users.
		if filter.RankingLimit == 0 || len(rows) < filter.RankingLimit {
			rows = append(rows, row)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	if pending > usageReadPendingLimit {
		return nil, ErrUsageStatisticsNotReady
	}
	return rows, nil
}

// readLegacyUsageDashboard preserves the pre-cutover quota_data reader while
// durable statistics are collecting. It is selected only when no explicit
// operator lineage has been installed; AutoMigrate and live fact capture do
// not silently change the dashboard source.
func readLegacyUsageDashboard(ctx context.Context, start, end int64, filter usageDashboardFilter, dimensions []string) ([]usageDashboardRow, error) {
	if ctx == nil || DB == nil {
		return nil, ErrUsageStatisticsNotReady
	}
	selectParts := make([]string, 0, len(dimensions)+7)
	selectParts = append(selectParts, dimensions...)
	selectParts = append(selectParts,
		"COALESCE(SUM(count), 0) AS count",
		"COALESCE(SUM(quota), 0) AS quota",
		"COALESCE(SUM(token_used), 0) AS token_used",
		"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens",
		"COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens",
		"0 AS pending",
		"0 AS invalid")
	query := DB.WithContext(ctx).Model(&QuotaData{}).Select(strings.Join(selectParts, ", ")).
		Where("1 = 1")
	if start > 0 {
		query = query.Where("created_at >= ?", start)
	}
	if end > 0 {
		query = query.Where("created_at <= ?", end)
	}
	if filter.UserID != 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.FilterUsername || filter.Username != "" {
		query = query.Where("username = ?", filter.Username)
	}
	if filter.FilterModel || filter.ModelName != "" {
		query = query.Where("model_name = ?", filter.ModelName)
	}
	if filter.NonemptyGroup {
		query = query.Where("use_group <> ''")
	}
	if filter.NonemptyModel {
		query = query.Where("model_name <> ''")
	}
	if len(dimensions) > 0 {
		query = query.Group(strings.Join(dimensions, ", "))
	}
	if filter.RankingLimit > 0 {
		query = query.Order("token_used DESC, user_id ASC").Limit(filter.RankingLimit)
	}
	var rows []usageDashboardRow
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func usageDashboardQuotaData(rows []usageDashboardRow) ([]*QuotaData, error) {
	result := make([]*QuotaData, 0, len(rows))
	for _, row := range rows {
		if row.Count > math.MaxInt || row.Quota > math.MaxInt || row.TokenUsed > math.MaxInt {
			return nil, ErrUsageEventOverflow
		}
		result = append(result, &QuotaData{
			UserID: row.UserID, Username: row.Username, ModelName: row.ModelName, CreatedAt: row.CreatedAt,
			UseGroup: row.UseGroup, TokenID: row.TokenID, ChannelID: row.ChannelID, NodeName: row.NodeName,
			Count: int(row.Count), Quota: int(row.Quota), TokenUsed: int(row.TokenUsed),
			CacheReadTokens: row.CacheReadTokens, CacheCreationTokens: row.CacheCreationTokens,
		})
	}
	return result, nil
}

func addUsageChannelRow(usage *ChannelUsage, row usageChannelReadRow) error {
	for _, field := range []struct {
		target *int64
		value  int64
	}{
		{&usage.RequestCount, row.ConsumeCount}, {&usage.PromptTokens, row.PromptTokens},
		{&usage.CompletionTokens, row.CompletionTokens}, {&usage.Quota, row.GrossQuota},
	} {
		value, err := checkedUsageAdd(*field.target, field.value)
		if err != nil {
			return err
		}
		*field.target = value
	}
	var err error
	usage.TotalTokens, err = checkedUsageAdd(usage.PromptTokens, usage.CompletionTokens)
	return err
}
