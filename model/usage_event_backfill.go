package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const usageEventBackfillBatchLimit = 100

var (
	ErrUsageBackfillManifestConflict = errors.New("usage backfill source manifest conflicts with retained evidence")
	ErrUsageBackfillProgressConflict = errors.New("usage backfill checkpoint changed; retry the same plan")
)

// UsageEventBackfillPlan describes ONE immutable fenced historical set in a
// persistent log-store namespace. Namespace must survive restarts and must not
// be generated per invocation or derived from a DSN. A restore/replacement is
// an explicit lineage decision. The manifest is obtained by bounded Inspect
// calls over the same frozen source, not a count/high watermark alone.
// No function here establishes a source fence, pauses TTL or activates capture.
type UsageEventBackfillPlan struct {
	SourceNamespace string
	SourceKind      UsageEventSourceKind
	CollectionEpoch string
	StartAt         int64
	EndAt           int64
	HighWatermark   int64
	ExpectedRows    int64
	ManifestDigest  string
	FenceDigest     string
}

// UsageEventBackfillCursor can also drive a read-only manifest pass. Tuple is
// a bounded whitelist of numeric/filter fields, never Other or log Content.
// The primary checkpoint is authoritative during import; callers do not supply
// a cursor to RunUsageEventBackfillBatch.
type UsageEventBackfillCursor struct {
	LastRowID int64
	Tuple     string
	Ordinal   int64
	Rows      int64
	Digest    string
}

type UsageEventBackfillProgress struct {
	Cursor               UsageEventBackfillCursor
	BatchRows            int
	RetainedVerified     bool
	UnknownFieldRows     int64
	HistoricalCoverage   UsageCoverageStatus
	WholeHistoryCoverage UsageCoverageStatus
}

// UsageEventBackfill is a durable primary checkpoint. Progress and all imported
// Prepare/Apply changes commit in the SAME transaction. One immutable plan per
// namespace prevents an accidental restart with new bounds from blessing a
// moving source. Full-history completeness is intentionally absent.
type UsageEventBackfill struct {
	SourceNamespace  string               `gorm:"type:char(32);primaryKey"`
	SourceKind       UsageEventSourceKind `gorm:"type:smallint;not null"`
	CollectionEpoch  string               `gorm:"type:char(32);not null"`
	StartAt          int64                `gorm:"type:bigint;not null"`
	EndAt            int64                `gorm:"type:bigint;not null"`
	HighWatermark    int64                `gorm:"type:bigint;not null"`
	ExpectedRows     int64                `gorm:"type:bigint;not null"`
	ManifestDigest   string               `gorm:"type:char(64);not null"`
	FenceDigest      string               `gorm:"type:char(64);not null"`
	Revision         int64                `gorm:"type:bigint;not null"`
	LastRowID        int64                `gorm:"type:bigint;not null"`
	CursorTuple      string               `gorm:"type:text;not null"`
	CursorOrdinal    int64                `gorm:"type:bigint;not null"`
	ImportedRows     int64                `gorm:"type:bigint;not null"`
	RollingDigest    string               `gorm:"type:char(64);not null"`
	UnknownFieldRows int64                `gorm:"type:bigint;not null"`
	VerifiedAt       int64                `gorm:"type:bigint;not null"`
	UpdatedAt        int64                `gorm:"type:bigint;not null"`
}

func (UsageEventBackfill) TableName() string { return "usage_event_backfills" }

// UsageEventBackfillTuple retains the FULL whitelisted source tuple behind a
// ClickHouse digest and its frozen multiplicity. Comparing it prevents a hash
// collision/changed multiplicity from silently merging physical occurrences.
// It contains no body, credential, Content or arbitrary Other JSON.
type UsageEventBackfillTuple struct {
	TupleKey        string `gorm:"type:char(64);primaryKey"`
	SourceNamespace string `gorm:"type:char(32);not null"`
	TupleDigest     string `gorm:"type:char(64);not null"`
	SourceTuple     string `gorm:"type:text;not null"`
	Multiplicity    int64  `gorm:"type:bigint;not null"`
}

func (UsageEventBackfillTuple) TableName() string { return "usage_event_backfill_tuples" }

// usageBackfillRow is the numeric/filter projection read from the source. Raw
// fields below are individual whitelisted metadata members, NOT arbitrary Other.
// Distinct spellings remain distinct source tuples; their multiplicities are
// preserved rather than guessed to be logical duplicate delivery.
type usageBackfillRow struct {
	ID                int64  `gorm:"column:id"`
	UserID            int64  `gorm:"column:user_id"`
	OccurredAt        int64  `gorm:"column:created_at"`
	Type              int    `gorm:"column:type"`
	Username          string `gorm:"column:username"`
	TokenName         string `gorm:"column:token_name"`
	ModelName         string `gorm:"column:model_name"`
	Quota             int64  `gorm:"column:quota"`
	PromptTokens      int64  `gorm:"column:prompt_tokens"`
	CompletionTokens  int64  `gorm:"column:completion_tokens"`
	ChannelID         int64  `gorm:"column:channel_id"`
	TokenID           int64  `gorm:"column:token_id"`
	GroupName         string `gorm:"column:group_name"`
	RequestID         string `gorm:"column:request_id"`
	UpstreamRequestID string `gorm:"column:upstream_request_id"`
	CacheRead         string `gorm:"column:cache_read"`
	CacheWrite        string `gorm:"column:cache_write"`
	Semantic          string `gorm:"column:semantic"`
	WriterNode        string `gorm:"column:writer_node"`
	ExportNode        string `gorm:"column:export_node"`
	EventVersion      string `gorm:"column:event_version"`
	EventKey          string `gorm:"column:event_key"`
	EventDigest       string `gorm:"column:event_digest"`
	EventFacts        string `gorm:"column:event_facts"`
	Multiplicity      int64  `gorm:"column:multiplicity"`
	Other             string `gorm:"column:other" json:"-"`
}

type usageBackfillOccurrence struct {
	row         usageBackfillRow
	input       UsageEventInput
	tuple       string
	tupleDigest string
	ordinal     int64
}

// UsageEventBackfillPage exposes the next manifest cursor but keeps source
// occurrences private so callers cannot edit a finalized page before commit.
type UsageEventBackfillPage struct {
	Next      UsageEventBackfillCursor
	Exhausted bool
	rows      []usageBackfillOccurrence
}

func validateUsageBackfillPlan(plan UsageEventBackfillPlan, importing bool) error {
	if !isLowerHex(plan.SourceNamespace, usageEventUIDLength) || !isLowerHex(plan.CollectionEpoch, usageEventUIDLength) ||
		!isLowerHex(plan.FenceDigest, usageEventDigestLength) || plan.StartAt <= 0 || plan.EndAt < plan.StartAt {
		return ErrUsageEventInvalid
	}
	if importing && plan.ExpectedRows < 0 {
		return ErrUsageEventInvalid
	}
	switch plan.SourceKind {
	case UsageEventSourceLegacyRelational:
		if plan.HighWatermark < 0 {
			return ErrUsageEventInvalid
		}
	case UsageEventSourceLegacyClickHouse:
		if plan.HighWatermark != 0 {
			return ErrUsageEventInvalid
		}
	default:
		return ErrUsageEventInvalid
	}
	if importing && !isLowerHex(plan.ManifestDigest, usageEventDigestLength) {
		return ErrUsageEventInvalid
	}
	return nil
}

func usageBackfillInitialDigest() string {
	return digestCanonical([]byte("usage-backfill-manifest/v1"))
}

func usageBackfillPlanRow(plan UsageEventBackfillPlan) UsageEventBackfill {
	return UsageEventBackfill{
		SourceNamespace: plan.SourceNamespace, SourceKind: plan.SourceKind, CollectionEpoch: plan.CollectionEpoch,
		StartAt: plan.StartAt, EndAt: plan.EndAt, HighWatermark: plan.HighWatermark, ExpectedRows: plan.ExpectedRows,
		ManifestDigest: plan.ManifestDigest, FenceDigest: plan.FenceDigest, RollingDigest: usageBackfillInitialDigest(),
		UpdatedAt: common.GetTimestamp(),
	}
}

func usageBackfillPlanMatches(stored UsageEventBackfill, plan UsageEventBackfillPlan) bool {
	return normalizeFixedText(stored.SourceNamespace) == plan.SourceNamespace && stored.SourceKind == plan.SourceKind &&
		normalizeFixedText(stored.CollectionEpoch) == plan.CollectionEpoch && stored.StartAt == plan.StartAt && stored.EndAt == plan.EndAt &&
		stored.HighWatermark == plan.HighWatermark && stored.ExpectedRows == plan.ExpectedRows &&
		normalizeFixedText(stored.ManifestDigest) == plan.ManifestDigest && normalizeFixedText(stored.FenceDigest) == plan.FenceDigest
}

// InspectUsageEventBackfillBatch performs a bounded READ ONLY source pass. Use
// its final Next.Rows/Digest to freeze the import manifest. This is also the
// exact importer codec, so source verification does not copy business logic.
// Source must remain immutable throughout BOTH passes, including ClickHouse
// TTL, late legacy writes and restores. No code here claims that fence exists.
func InspectUsageEventBackfillBatch(ctx context.Context, source *gorm.DB, plan UsageEventBackfillPlan, cursor UsageEventBackfillCursor, limit int) (UsageEventBackfillPage, error) {
	var page UsageEventBackfillPage
	if ctx == nil || source == nil || validateUsageBackfillPlan(plan, false) != nil || cursor.LastRowID < 0 || cursor.Ordinal < 0 || cursor.Rows < 0 {
		return page, ErrUsageEventInvalid
	}
	if limit <= 0 {
		limit = usageEventBackfillBatchLimit
	}
	limit = min(limit, usageEventBackfillBatchLimit)
	if cursor.Digest == "" && cursor.Rows == 0 && cursor.LastRowID == 0 && cursor.Tuple == "" && cursor.Ordinal == 0 {
		cursor.Digest = usageBackfillInitialDigest()
	}
	if !isLowerHex(cursor.Digest, usageEventDigestLength) || len(cursor.Tuple) > 65536 {
		return page, ErrUsageEventInvalid
	}
	kind := source.Dialector.Name()
	if (plan.SourceKind == UsageEventSourceLegacyClickHouse) != (kind == "clickhouse") {
		return page, ErrUsageEventInvalid
	}
	columns := []string{"user_id", "created_at", "type", "username", "token_name", "model_name", "quota", "prompt_tokens", "completion_tokens", "channel_id", "token_id"}
	groupColumn := "`group`"
	if kind == "postgres" {
		groupColumn = `"group"`
	}
	columns = append(columns, groupColumn+" AS group_name", "request_id", "upstream_request_id")
	metadata := []struct{ alias, path string }{
		{"cache_read", "cache_tokens"}, {"cache_write", "cache_write_tokens"}, {"semantic", "usage_semantic"},
		{"writer_node", "admin_info.node_name"}, {"export_node", "admin_info.submit_node_name"},
		{"event_version", "admin_info.usage_event_version"}, {"event_key", "admin_info.usage_event_key"}, {"event_digest", "admin_info.usage_event_payload_digest"},
		{"event_facts", "admin_info.usage_event_facts"},
	}
	for _, field := range metadata {
		var expression string
		switch kind {
		case "clickhouse":
			parts := strings.Split(field.path, ".")
			quoted := make([]string, len(parts))
			for i, part := range parts {
				quoted[i] = "'" + part + "'"
			}
			expression = "JSONExtractRaw(other, " + strings.Join(quoted, ", ") + ")"
		case "mysql":
			expression = "CASE WHEN JSON_VALID(other) THEN COALESCE(CAST(JSON_EXTRACT(other, '$." + field.path + "') AS CHAR), '') ELSE '' END"
		case "sqlite":
			// json_quote preserves the distinction between JSON strings and
			// integers; absent/null both remain explicitly unknown.
			expression = "CASE WHEN json_valid(other) THEN COALESCE(json_quote(json_extract(other, '$." + field.path + "')), '') ELSE '' END"
		case "postgres":
			// Old logs may contain malformed JSON. PostgreSQL's json cast is
			// not safe on those rows; project Other transiently below instead.
			expression = "''"
		default:
			return page, ErrUsageEventInvalid
		}
		columns = append(columns, expression+" AS "+field.alias)
	}
	query := source.WithContext(ctx).Table("logs").Where("created_at >= ? AND created_at <= ? AND type IN ?", plan.StartAt, plan.EndAt, []int{LogTypeConsume, LogTypeRefund})
	var rows []usageBackfillRow
	if kind != "clickhouse" {
		if cursor.Tuple != "" || cursor.Ordinal != 0 || cursor.LastRowID > plan.HighWatermark {
			return page, ErrUsageEventInvalid
		}
		// Never fetch Content. Only PostgreSQL needs transient Other because
		// its historical invalid-JSON rows cannot safely enter a SQL json cast.
		var relational []usageBackfillRow
		selectColumns := append([]string{"id"}, columns...)
		if kind == "postgres" {
			selectColumns = append(selectColumns, "other")
		}
		if err := query.Select(strings.Join(selectColumns, ", ")).Where("id > ? AND id <= ?", cursor.LastRowID, plan.HighWatermark).
			Order("id").Limit(limit+1).Scan(&relational).Error; err != nil {
			return page, err
		}
		page.Exhausted = len(relational) <= limit
		if len(relational) > limit {
			relational = relational[:limit]
		}
		for _, row := range relational {
			if kind == "postgres" {
				var values map[string]json.RawMessage
				if common.UnmarshalJsonStr(row.Other, &values) == nil {
					row.CacheRead, row.CacheWrite, row.Semantic = string(values["cache_tokens"]), string(values["cache_write_tokens"]), string(values["usage_semantic"])
					var admin map[string]json.RawMessage
					if common.Unmarshal(values["admin_info"], &admin) == nil {
						row.WriterNode, row.ExportNode = string(admin["node_name"]), string(admin["submit_node_name"])
						row.EventVersion, row.EventKey, row.EventDigest = string(admin["usage_event_version"]), string(admin["usage_event_key"]), string(admin["usage_event_payload_digest"])
						row.EventFacts = string(admin["usage_event_facts"])
					}
				}
			}
			row.Other = ""
			row.Multiplicity = 1
			rows = append(rows, row)
		}
	} else {
		if cursor.LastRowID != 0 {
			return page, ErrUsageEventInvalid
		}
		// Byte-preserving String tuple order is stable across MergeTree parts.
		// id=0/display IDs, OFFSET and content-hash DISTINCT are never identity.
		aliases := []string{"user_id", "created_at", "type", "username", "token_name", "model_name", "quota", "prompt_tokens", "completion_tokens", "channel_id", "token_id", "group_name", "request_id", "upstream_request_id"}
		for _, field := range metadata {
			aliases = append(aliases, field.alias)
		}
		projection := query.Select(strings.Join(columns, ", "))
		grouped := source.WithContext(ctx).Table("(?) AS numeric_source", projection)
		if cursor.Tuple != "" {
			var previous usageBackfillRow
			if common.UnmarshalJsonStr(cursor.Tuple, &previous) != nil || previous.Multiplicity <= 0 || cursor.Ordinal > previous.Multiplicity {
				return page, ErrUsageEventInvalid
			}
			operator := ">"
			if cursor.Ordinal < previous.Multiplicity {
				operator = ">="
			}
			values := usageBackfillTupleValues(previous)
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
			grouped = grouped.Where("tuple("+strings.Join(aliases, ",")+") "+operator+" tuple("+placeholders+")", values...)
		}
		if err := grouped.Select(strings.Join(aliases, ",")+", count() AS multiplicity").Group(strings.Join(aliases, ",")).
			Order("tuple("+strings.Join(aliases, ",")+")").Limit(limit+1).Scan(&rows).Error; err != nil {
			return page, err
		}
		page.Exhausted = len(rows) <= limit
	}
	page.Next = cursor
	for _, row := range rows {
		if len(page.rows) == limit {
			page.Exhausted = false
			break
		}
		if kind != "clickhouse" && row.ID <= page.Next.LastRowID || row.Multiplicity <= 0 {
			return UsageEventBackfillPage{}, ErrUsageBackfillManifestConflict
		}
		encoded, err := common.Marshal(row)
		if err != nil || len(encoded) > 65535 {
			return UsageEventBackfillPage{}, ErrUsageEventInvalid
		}
		tuple := string(encoded)
		startOrdinal := int64(1)
		if kind == "clickhouse" && cursor.Tuple != "" {
			var previous usageBackfillRow
			if err := common.UnmarshalJsonStr(cursor.Tuple, &previous); err != nil {
				return UsageEventBackfillPage{}, ErrUsageEventInvalid
			}
			if usageBackfillSameTuple(previous, row) {
				if previous.Multiplicity != row.Multiplicity {
					return UsageEventBackfillPage{}, ErrUsageBackfillManifestConflict
				}
				startOrdinal = cursor.Ordinal + 1
			}
		}
		identityTuple := row
		identityTuple.Multiplicity = 0
		identityBytes, err := common.Marshal(identityTuple)
		if err != nil {
			return UsageEventBackfillPage{}, err
		}
		tupleDigest := digestCanonical(identityBytes)
		for ordinal := startOrdinal; ordinal <= row.Multiplicity && len(page.rows) < limit; ordinal++ {
			input, err := usageBackfillInput(plan, row, tupleDigest, ordinal)
			if err != nil {
				return UsageEventBackfillPage{}, err
			}
			occurrence := usageBackfillOccurrence{row: row, input: input, tuple: tuple, tupleDigest: tupleDigest, ordinal: ordinal}
			page.rows = append(page.rows, occurrence)
			var chain bytes.Buffer
			appendCanonicalString(&chain, 0, page.Next.Digest, true)
			appendCanonicalString(&chain, 1, tuple, true)
			appendCanonicalInt64(&chain, 2, ordinal, true)
			page.Next.Digest = digestCanonical(chain.Bytes())
			page.Next.Rows, err = checkedUsageAdd(page.Next.Rows, 1)
			if err != nil {
				return UsageEventBackfillPage{}, err
			}
			if kind == "clickhouse" {
				page.Next.Tuple, page.Next.Ordinal = tuple, ordinal
			} else {
				page.Next.LastRowID = row.ID
			}
			if ordinal == row.Multiplicity {
				break // do not overflow a maximum signed ordinal after its last occurrence
			}
		}
		if kind == "clickhouse" && page.Next.Ordinal < row.Multiplicity {
			page.Exhausted = false
			break
		}
	}
	return page, nil
}

func usageBackfillTupleValues(row usageBackfillRow) []any {
	return []any{row.UserID, row.OccurredAt, row.Type, row.Username, row.TokenName, row.ModelName, row.Quota, row.PromptTokens, row.CompletionTokens,
		row.ChannelID, row.TokenID, row.GroupName, row.RequestID, row.UpstreamRequestID, row.CacheRead, row.CacheWrite, row.Semantic,
		row.WriterNode, row.ExportNode, row.EventVersion, row.EventKey, row.EventDigest, row.EventFacts}
}

func usageBackfillSameTuple(a, b usageBackfillRow) bool {
	a.Multiplicity, b.Multiplicity = 0, 0
	return a == b
}

// usageBackfillLinkedFacts is the exact numeric whitelist emitted by the live
// source. It is not arbitrary Other JSON, and cannot attest source readiness.
type usageBackfillLinkedFacts struct {
	KnownMask           UsageEventKnownMask `json:"known_mask"`
	CacheReadTokens     int64               `json:"cache_read_tokens"`
	CacheWriteTokens    int64               `json:"cache_write_tokens"`
	ReportedInputTokens int64               `json:"reported_input_tokens"`
	UsageSemantic       UsageSemantic       `json:"usage_semantic"`
	WriterNode          string              `json:"writer_node"`
	ExportNode          string              `json:"export_node"`
}

func usageBackfillDecodeLinkedFacts(raw string) (usageBackfillLinkedFacts, error) {
	var facts usageBackfillLinkedFacts
	var fields map[string]json.RawMessage
	if len(raw) == 0 || len(raw) > 16384 || common.UnmarshalJsonStr(raw, &fields) != nil || len(fields) != 7 {
		return facts, ErrUsageBackfillManifestConflict
	}
	for _, name := range []string{"known_mask", "cache_read_tokens", "cache_write_tokens", "reported_input_tokens", "usage_semantic", "writer_node", "export_node"} {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return facts, ErrUsageBackfillManifestConflict
		}
	}
	if common.UnmarshalJsonStr(raw, &facts) != nil || facts.KnownMask&^usageEventKnownAll != 0 {
		return usageBackfillLinkedFacts{}, ErrUsageBackfillManifestConflict
	}
	return facts, nil
}

func usageBackfillInput(plan UsageEventBackfillPlan, row usageBackfillRow, tupleDigest string, ordinal int64) (UsageEventInput, error) {
	input := UsageEventInput{
		Identity: UsageEventIdentity{SourceNamespace: plan.SourceNamespace, SourceKind: plan.SourceKind},
		CollectionEpoch: plan.CollectionEpoch, Kind: UsageEventKindConsume, OccurredAt: row.OccurredAt,
		KnownMask: UsageEventKnownUserID | UsageEventKnownTokenID | UsageEventKnownChannelID | UsageEventKnownUsername |
			UsageEventKnownTokenName | UsageEventKnownModelName | UsageEventKnownGroupName | UsageEventKnownRequestID |
			UsageEventKnownUpstreamRequestID | UsageEventKnownPromptTokens | UsageEventKnownCompletionTokens | UsageEventKnownQuota,
		UserID: row.UserID, TokenID: row.TokenID, ChannelID: row.ChannelID, Username: row.Username, TokenName: row.TokenName,
		ModelName: row.ModelName, GroupName: row.GroupName, RequestID: row.RequestID, UpstreamRequestID: row.UpstreamRequestID,
		PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens, QuotaMagnitude: row.Quota,
	}
	if plan.SourceKind == UsageEventSourceLegacyRelational {
		input.Identity.SourceRowID = row.ID
	} else {
		input.Identity.SourceTupleDigest, input.Identity.SourceOrdinal = tupleDigest, ordinal
	}
	if row.Type == LogTypeRefund {
		input.Kind = UsageEventKindRefund
	} else if row.Type != LogTypeConsume {
		return UsageEventInput{}, ErrUsageEventInvalid
	}
	if input.Kind == UsageEventKindConsume && row.ChannelID > 0 {
		input.Eligibility = UsageEventProjectChannel
	}
	for _, field := range []struct {
		raw   string
		value *int64
		known UsageEventKnownMask
	}{
		{row.CacheRead, &input.CacheReadTokens, UsageEventKnownCacheReadTokens},
		{row.CacheWrite, &input.CacheWriteTokens, UsageEventKnownCacheWriteTokens},
	} {
		if field.raw == "" || field.raw == "null" {
			continue
		}
		value, err := strconv.ParseInt(field.raw, 10, 64)
		if err != nil || value < 0 {
			continue
		}
		*field.value = value
		input.KnownMask |= field.known
	}
	var semantic string
	if common.UnmarshalJsonStr(row.Semantic, &semantic) == nil {
		switch semantic {
		case "anthropic":
			input.UsageSemantic = UsageSemanticAnthropic
		case "openai":
			input.UsageSemantic = UsageSemanticOpenAI
		}
		if input.UsageSemantic != UsageSemanticUnknown {
			input.KnownMask |= UsageEventKnownUsageSemantic
		}
	}
	for _, field := range []struct {
		raw   string
		value *string
		known UsageEventKnownMask
	}{
		{row.WriterNode, &input.WriterNode, UsageEventKnownWriterNode},
		{row.ExportNode, &input.ExportNode, UsageEventKnownExportNode},
	} {
		if field.raw != "" && field.raw != "null" && common.UnmarshalJsonStr(field.raw, field.value) == nil {
			input.KnownMask |= field.known
		}
	}
	if row.EventFacts != "" && row.EventFacts != "null" {
		facts, err := usageBackfillDecodeLinkedFacts(row.EventFacts)
		if err != nil {
			return UsageEventInput{}, err
		}
		input.KnownMask = facts.KnownMask
		input.CacheReadTokens, input.CacheWriteTokens = facts.CacheReadTokens, facts.CacheWriteTokens
		input.ReportedInputTokens, input.UsageSemantic = facts.ReportedInputTokens, facts.UsageSemantic
		input.WriterNode, input.ExportNode = facts.WriterNode, facts.ExportNode
	}
	finalized, _, _, err := validateUsageEventInput(input, UsageEventSourceReady)
	return finalized, err
}

func usageBackfillVerifyLinked(tx *gorm.DB, occurrence usageBackfillOccurrence, apply bool) (bool, error) {
	row, input := occurrence.row, occurrence.input
	if (row.EventKey == "" || row.EventKey == "null") && (row.EventDigest == "" || row.EventDigest == "null") && (row.EventVersion == "" || row.EventVersion == "null") &&
		(row.EventFacts == "" || row.EventFacts == "null") {
		return false, nil
	}
	var key, digest string
	if row.EventVersion != "1" || common.UnmarshalJsonStr(row.EventKey, &key) != nil || common.UnmarshalJsonStr(row.EventDigest, &digest) != nil ||
		!isLowerHex(key, usageEventDigestLength) || !isLowerHex(digest, usageEventDigestLength) {
		return true, ErrUsageBackfillManifestConflict
	}
	if row.EventFacts == "" || row.EventFacts == "null" {
		return true, ErrUsageStatisticsNotReady
	}
	var stored UsageEvent
	query := tx
	if apply {
		query = lockForUpdate(tx)
	}
	if err := query.Where("event_key = ?", key).Take(&stored).Error; err != nil {
		return true, errors.Join(ErrUsageStatisticsNotReady, err)
	}
	if err := validateUsageEventRow(stored); err != nil {
		return true, err
	}
	if normalizeFixedText(stored.PayloadDigest) != digest || stored.Kind != input.Kind || stored.OccurredAt != input.OccurredAt ||
		stored.UserID != input.UserID || stored.TokenID != input.TokenID || stored.ChannelID != input.ChannelID ||
		stored.Username != input.Username || stored.TokenName != input.TokenName || stored.ModelName != input.ModelName || stored.GroupName != input.GroupName ||
		stored.RequestID != input.RequestID || stored.UpstreamRequestID != input.UpstreamRequestID || stored.PromptTokens != input.PromptTokens ||
		stored.CompletionTokens != input.CompletionTokens || stored.QuotaMagnitude != input.QuotaMagnitude ||
		stored.KnownMask != input.KnownMask || stored.CacheReadTokens != input.CacheReadTokens || stored.CacheWriteTokens != input.CacheWriteTokens ||
		stored.ReportedInputTokens != input.ReportedInputTokens || stored.UsageSemantic != input.UsageSemantic ||
		stored.WriterNode != input.WriterNode || stored.ExportNode != input.ExportNode {
		return true, ErrUsageEventPayloadConflict
	}
	// A surviving keyed sink row is not permission to resolve source-pending
	// or unknown outcomes. Only the actual source owner may Confirm them.
	if stored.SourceState != UsageEventSourceReady {
		return true, ErrUsageStatisticsNotReady
	}
	if !apply {
		if stored.AppliedVersion != UsageEventProjectionVersion {
			return true, ErrUsageStatisticsNotReady
		}
		return true, nil
	}
	return true, ApplyUsageEventTx(tx, usageEventReceipt(stored))
}

// verifyUsageLogDeletion verifies one locked relational source row. Live
// linkage is self-identifying; an unkeyed legacy row additionally requires an
// explicit persistent namespace for the current log store. Neither matching
// request IDs nor matching numeric payloads in another namespace prove identity.
func verifyUsageLogDeletion(primary *gorm.DB, log Log) error {
	if primary == nil || log.Id <= 0 || log.CreatedAt <= 0 {
		return ErrUsageStatisticsNotReady
	}
	row := usageBackfillRow{
		ID: int64(log.Id), UserID: int64(log.UserId), OccurredAt: log.CreatedAt, Type: log.Type,
		Username: log.Username, TokenName: log.TokenName, ModelName: log.ModelName, Quota: int64(log.Quota),
		PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens),
		ChannelID: int64(log.ChannelId), TokenID: int64(log.TokenId), GroupName: log.Group,
		RequestID: log.RequestId, UpstreamRequestID: log.UpstreamRequestId, Multiplicity: 1,
	}
	var values map[string]json.RawMessage
	if common.UnmarshalJsonStr(log.Other, &values) == nil {
		row.CacheRead, row.CacheWrite, row.Semantic = string(values["cache_tokens"]), string(values["cache_write_tokens"]), string(values["usage_semantic"])
		var admin map[string]json.RawMessage
		if common.Unmarshal(values["admin_info"], &admin) == nil {
			row.WriterNode, row.ExportNode = string(admin["node_name"]), string(admin["submit_node_name"])
			row.EventVersion, row.EventKey, row.EventDigest = string(admin["usage_event_version"]), string(admin["usage_event_key"]), string(admin["usage_event_payload_digest"])
			row.EventFacts = string(admin["usage_event_facts"])
		}
	}
	if row.EventVersion == "1" && row.EventKey != "" && row.EventDigest != "" {
		var key, digest string
		if common.UnmarshalJsonStr(row.EventKey, &key) != nil || common.UnmarshalJsonStr(row.EventDigest, &digest) != nil ||
			!isLowerHex(key, usageEventDigestLength) || !isLowerHex(digest, usageEventDigestLength) {
			return ErrUsageBackfillManifestConflict
		}
		var stored UsageEvent
		if err := primary.Where("event_key = ?", key).Take(&stored).Error; err != nil {
			return errors.Join(ErrUsageStatisticsNotReady, err)
		}
		facts, err := usageBackfillDecodeLinkedFacts(row.EventFacts)
		if err != nil {
			return ErrUsageStatisticsNotReady
		}
		input := usageEventInputFromRow(stored)
		if facts.KnownMask != input.KnownMask || facts.CacheReadTokens != input.CacheReadTokens || facts.CacheWriteTokens != input.CacheWriteTokens ||
			facts.ReportedInputTokens != input.ReportedInputTokens || facts.UsageSemantic != input.UsageSemantic ||
			facts.WriterNode != input.WriterNode || facts.ExportNode != input.ExportNode {
			return ErrUsageStatisticsNotReady
		}
		// The typed facts above are authoritative. Legacy display duplicates
		// may be absent or null, but any present value must agree with them.
		for _, value := range []struct {
			raw    string
			known  bool
			expect string
		}{{row.WriterNode, usageKnown(input.KnownMask, UsageEventKnownWriterNode), input.WriterNode},
			{row.ExportNode, usageKnown(input.KnownMask, UsageEventKnownExportNode), input.ExportNode}} {
			if value.raw == "" || strings.TrimSpace(value.raw) == "null" {
				continue
			}
			var actual string
			if common.UnmarshalJsonStr(value.raw, &actual) != nil || !value.known || actual != value.expect {
				return ErrUsageStatisticsNotReady
			}
		}
		for _, value := range []struct {
			raw    string
			known  bool
			expect int64
		}{{row.CacheRead, usageKnown(input.KnownMask, UsageEventKnownCacheReadTokens), input.CacheReadTokens},
			{row.CacheWrite, usageKnown(input.KnownMask, UsageEventKnownCacheWriteTokens), input.CacheWriteTokens}} {
			if value.raw == "" || strings.TrimSpace(value.raw) == "null" {
				continue
			}
			parsed, err := strconv.ParseInt(value.raw, 10, 64)
			if err != nil || !value.known || parsed != value.expect {
				return ErrUsageStatisticsNotReady
			}
		}
		if row.Semantic != "" && strings.TrimSpace(row.Semantic) != "null" {
			var label string
			if common.UnmarshalJsonStr(row.Semantic, &label) != nil || !usageKnown(input.KnownMask, UsageEventKnownUsageSemantic) {
				return ErrUsageStatisticsNotReady
			}
			var semantic UsageSemantic
			switch label {
			case "openai":
				semantic = UsageSemanticOpenAI
			case "anthropic":
				semantic = UsageSemanticAnthropic
			default:
				// OtherExplicit does not retain the original label. Without
				// exact original-label evidence, an unknown spelling is unproven.
				return ErrUsageStatisticsNotReady
			}
			if semantic != input.UsageSemantic {
				return ErrUsageStatisticsNotReady
			}
		}
		if validateUsageEventRow(stored) != nil || stored.SourceState != UsageEventSourceReady || stored.AppliedVersion != UsageEventProjectionVersion ||
			normalizeFixedText(stored.PayloadDigest) != digest || stored.OccurredAt != row.OccurredAt || stored.UserID != row.UserID ||
			stored.TokenID != row.TokenID || stored.ChannelID != row.ChannelID || stored.Username != row.Username || stored.TokenName != row.TokenName ||
			stored.ModelName != row.ModelName || stored.GroupName != row.GroupName || stored.RequestID != row.RequestID ||
			stored.UpstreamRequestID != row.UpstreamRequestID || stored.PromptTokens != row.PromptTokens || stored.CompletionTokens != row.CompletionTokens ||
			stored.QuotaMagnitude != row.Quota ||
			stored.CacheReadTokens != input.CacheReadTokens || stored.CacheWriteTokens != input.CacheWriteTokens ||
			stored.ReportedInputTokens != input.ReportedInputTokens || stored.UsageSemantic != input.UsageSemantic ||
			stored.WriterNode != input.WriterNode || stored.ExportNode != input.ExportNode ||
			(log.Type == LogTypeConsume && stored.Kind != UsageEventKindConsume) ||
			(log.Type == LogTypeRefund && stored.Kind != UsageEventKindRefund) {
			return ErrUsageStatisticsNotReady
		}
		return nil
	}
	// Legacy cleanup is tied to the immutable source coordinates that were
	// installed with the baseline, not mutable process environment.
	lineage, err := getUsageReadLineage(primary)
	if err != nil || !lineage.IncludeLegacyQuotaData ||
		!isLowerHex(lineage.LegacySourceNamespace, usageEventUIDLength) ||
		!isLowerHex(lineage.LegacyCollectionEpoch, usageEventUIDLength) {
		return ErrUsageStatisticsNotReady
	}
	namespace := lineage.LegacySourceNamespace
	var checkpoint UsageEventBackfill
	if err := primary.Where("source_namespace = ?", namespace).Take(&checkpoint).Error; err != nil {
		return errors.Join(ErrUsageStatisticsNotReady, err)
	}
	plan := UsageEventBackfillPlan{
		SourceNamespace: namespace, SourceKind: checkpoint.SourceKind, CollectionEpoch: normalizeFixedText(checkpoint.CollectionEpoch),
		StartAt: checkpoint.StartAt, EndAt: checkpoint.EndAt, HighWatermark: checkpoint.HighWatermark, ExpectedRows: checkpoint.ExpectedRows,
		ManifestDigest: normalizeFixedText(checkpoint.ManifestDigest), FenceDigest: normalizeFixedText(checkpoint.FenceDigest),
	}
	if validateUsageBackfillPlan(plan, true) != nil || plan.SourceKind != UsageEventSourceLegacyRelational ||
		plan.CollectionEpoch != lineage.LegacyCollectionEpoch || checkpoint.VerifiedAt <= 0 ||
		checkpoint.ImportedRows != plan.ExpectedRows || normalizeFixedText(checkpoint.RollingDigest) != plan.ManifestDigest ||
		row.ID > plan.HighWatermark || row.OccurredAt < plan.StartAt || row.OccurredAt > plan.EndAt {
		return ErrUsageStatisticsNotReady
	}
	input, err := usageBackfillInput(plan, row, "", 1)
	if err != nil {
		return errors.Join(ErrUsageStatisticsNotReady, err)
	}
	var stored UsageEvent
	if err := primary.Where("event_key = ?", input.EventKey).Take(&stored).Error; err != nil {
		return errors.Join(ErrUsageStatisticsNotReady, err)
	}
	storedInput := usageEventInputFromRow(stored)
	if validateUsageEventRow(stored) != nil || stored.SourceState != UsageEventSourceReady || stored.AppliedVersion != UsageEventProjectionVersion ||
		storedInput.Identity.SourceNamespace != input.Identity.SourceNamespace || storedInput.Identity.SourceKind != input.Identity.SourceKind ||
		storedInput.Identity.SourceRowID != input.Identity.SourceRowID || storedInput.CollectionEpoch != input.CollectionEpoch || storedInput.Kind != input.Kind ||
		storedInput.OccurredAt != input.OccurredAt || storedInput.UserID != input.UserID || storedInput.TokenID != input.TokenID || storedInput.ChannelID != input.ChannelID ||
		storedInput.Username != input.Username || storedInput.TokenName != input.TokenName || storedInput.ModelName != input.ModelName || storedInput.GroupName != input.GroupName ||
		storedInput.WriterNode != input.WriterNode || storedInput.ExportNode != input.ExportNode ||
		storedInput.RequestID != input.RequestID || storedInput.UpstreamRequestID != input.UpstreamRequestID || storedInput.PromptTokens != input.PromptTokens ||
		storedInput.CompletionTokens != input.CompletionTokens || storedInput.QuotaMagnitude != input.QuotaMagnitude || storedInput.KnownMask != input.KnownMask ||
		storedInput.CacheReadTokens != input.CacheReadTokens || storedInput.CacheWriteTokens != input.CacheWriteTokens || storedInput.UsageSemantic != input.UsageSemantic {
		return ErrUsageStatisticsNotReady
	}
	return nil
}

func usageBackfillProgress(row UsageEventBackfill, batchRows int) UsageEventBackfillProgress {
	status := UsageCoveragePartial
	if row.VerifiedAt > 0 {
		status = UsageCoverageRetainedVerified
	}
	return UsageEventBackfillProgress{
		Cursor: UsageEventBackfillCursor{LastRowID: row.LastRowID, Tuple: row.CursorTuple, Ordinal: row.CursorOrdinal, Rows: row.ImportedRows, Digest: normalizeFixedText(row.RollingDigest)},
		BatchRows: batchRows, RetainedVerified: row.VerifiedAt > 0, UnknownFieldRows: row.UnknownFieldRows,
		HistoricalCoverage: status, WholeHistoryCoverage: UsageCoverageUnknown,
	}
}

// RunUsageEventBackfillBatch imports at most 100 physical retained occurrences.
// Independent source reads happen OUTSIDE the primary transaction, so same-file
// SQLite never attempts a second-pool write under a primary writer lock. A
// competing importer must retry after the checkpoint revision changes. No
// financial, cleanup, quota_data or coverage-attestation operation is called.
func RunUsageEventBackfillBatch(ctx context.Context, source *gorm.DB, plan UsageEventBackfillPlan, limit int) (UsageEventBackfillProgress, error) {
	var progress UsageEventBackfillProgress
	if ctx == nil || DB == nil || source == nil || validateUsageBackfillPlan(plan, true) != nil {
		return progress, ErrUsageEventInvalid
	}
	var checkpoint UsageEventBackfill
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		candidate := usageBackfillPlanRow(plan)
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("source_namespace = ?", plan.SourceNamespace).Take(&checkpoint).Error; err != nil {
			return err
		}
		if !usageBackfillPlanMatches(checkpoint, plan) {
			return ErrUsageBackfillManifestConflict
		}
		return nil
	})
	if err != nil {
		return progress, err
	}
	progress = usageBackfillProgress(checkpoint, 0)
	if checkpoint.VerifiedAt > 0 {
		return progress, nil
	}
	page, err := InspectUsageEventBackfillBatch(ctx, source, plan, progress.Cursor, limit)
	if err != nil {
		return progress, err
	}
	if page.Next.Rows > plan.ExpectedRows || page.Exhausted && (page.Next.Rows != plan.ExpectedRows || page.Next.Digest != plan.ManifestDigest) {
		return progress, ErrUsageBackfillManifestConflict
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := reserveUsageEventSQLiteWriter(tx); err != nil {
			return err
		}
		var stored UsageEventBackfill
		if err := lockForUpdate(tx).Where("source_namespace = ?", plan.SourceNamespace).Take(&stored).Error; err != nil {
			return err
		}
		if !usageBackfillPlanMatches(stored, plan) {
			return ErrUsageBackfillManifestConflict
		}
		if stored.Revision != checkpoint.Revision || stored.LastRowID != checkpoint.LastRowID || stored.CursorTuple != checkpoint.CursorTuple ||
			stored.CursorOrdinal != checkpoint.CursorOrdinal || stored.ImportedRows != checkpoint.ImportedRows || stored.RollingDigest != checkpoint.RollingDigest {
			return ErrUsageBackfillProgressConflict
		}
		unknown := stored.UnknownFieldRows
		for _, occurrence := range page.rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if plan.SourceKind == UsageEventSourceLegacyClickHouse {
				var encoded bytes.Buffer
				appendCanonicalString(&encoded, 0, plan.SourceNamespace, true)
				appendCanonicalString(&encoded, 1, occurrence.tupleDigest, true)
				expected := UsageEventBackfillTuple{TupleKey: digestCanonical(encoded.Bytes()), SourceNamespace: plan.SourceNamespace,
					TupleDigest: occurrence.tupleDigest, SourceTuple: occurrence.tuple, Multiplicity: occurrence.row.Multiplicity}
				candidate := expected
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
					return err
				}
				var tuple UsageEventBackfillTuple
				if err := lockForUpdate(tx).Where("tuple_key = ?", expected.TupleKey).Take(&tuple).Error; err != nil {
					return err
				}
				if normalizeFixedText(tuple.TupleKey) != expected.TupleKey || normalizeFixedText(tuple.SourceNamespace) != expected.SourceNamespace ||
					normalizeFixedText(tuple.TupleDigest) != expected.TupleDigest || tuple.SourceTuple != expected.SourceTuple || tuple.Multiplicity != expected.Multiplicity {
					return ErrUsageBackfillManifestConflict
				}
			}
			linked, err := usageBackfillVerifyLinked(tx, occurrence, true)
			if err != nil {
				return err
			}
			if !linked {
				receipt, err := PrepareUsageEventTx(tx, occurrence.input, UsageEventSourceReady)
				if err != nil {
					return err
				}
				if err := ApplyUsageEventTx(tx, receipt); err != nil {
					return err
				}
			}
			const cacheKnown = UsageEventKnownCacheReadTokens | UsageEventKnownCacheWriteTokens | UsageEventKnownUsageSemantic
			if occurrence.input.KnownMask&cacheKnown != cacheKnown {
				unknown, err = checkedUsageAdd(unknown, 1)
				if err != nil {
					return err
				}
			}
		}
		revision, err := checkedUsageAdd(stored.Revision, 1)
		if err != nil {
			return err
		}
		verified := int64(0)
		if page.Exhausted {
			verified = common.GetTimestamp()
		}
		changes := map[string]any{
			"revision": revision, "last_row_id": page.Next.LastRowID, "cursor_tuple": page.Next.Tuple, "cursor_ordinal": page.Next.Ordinal,
			"imported_rows": page.Next.Rows, "rolling_digest": page.Next.Digest, "unknown_field_rows": unknown,
			"verified_at": verified, "updated_at": common.GetTimestamp(),
		}
		if err := tx.Model(&UsageEventBackfill{}).Where("source_namespace = ?", plan.SourceNamespace).Updates(changes).Error; err != nil {
			return err
		}
		stored.LastRowID, stored.CursorTuple, stored.CursorOrdinal = page.Next.LastRowID, page.Next.Tuple, page.Next.Ordinal
		stored.ImportedRows, stored.RollingDigest, stored.UnknownFieldRows, stored.VerifiedAt = page.Next.Rows, page.Next.Digest, unknown, verified
		progress = usageBackfillProgress(stored, len(page.rows))
		return nil
	})
	if err != nil {
		return usageBackfillProgress(checkpoint, 0), err
	}
	return progress, nil
}
