package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

const (
	firstLegacyKey  = "00000000000000000000000000000001"
	secondLegacyKey = "00000000000000000000000000000002"
)

type redemptionIndex struct {
	name    string
	columns []string
	unique  bool
	known   bool
}

type releasedMigrationManifest struct {
	RedemptionIndexes []string `json:"redemption_indexes"`
	UsageLogID        int      `json:"usage_log_id"`
	QuotaDataID       int      `json:"quota_data_id"`
}

type usageMigrationTable struct {
	name    string
	values  map[string]any
	primary []string
	indexes []redemptionIndex
}

type usageMigrationSnapshot struct {
	Columns []string          `json:"columns"`
	Indexes []string          `json:"indexes"`
	Primary []string          `json:"primary"`
	Row     map[string]string `json:"row"`
}

func main() {
	if len(os.Args) != 2 || !slices.Contains([]string{"seed", "fresh-first", "fresh-second", "upgrade-first", "upgrade-second"}, os.Args[1]) {
		fail("usage: released_migration_probe.go seed|fresh-first|fresh-second|upgrade-first|upgrade-second")
	}
	phase := os.Args[1]
	logDir := os.Getenv("MIGRATION_LOG_DIR")
	if logDir == "" {
		fail("MIGRATION_LOG_DIR is required")
	}
	os.Args = []string{os.Args[0], "--log-dir", logDir}

	common.InitEnv()
	if !common.IsMasterNode {
		fail("startup migration probe requires NODE_TYPE=master")
	}
	if err := model.InitDB(); err != nil {
		fail("initialize database: %v", err)
	}
	if err := model.InitLogDB(); err != nil {
		fail("initialize log database: %v", err)
	}
	defer func() {
		if err := model.CloseDB(); err != nil {
			fail("close migration databases: %v", err)
		}
	}()
	var logTables []string
	var tableErr error
	if model.LOG_DB.Dialector.Name() == "clickhouse" {
		tableErr = model.LOG_DB.Raw("SELECT name FROM system.tables WHERE database = currentDatabase() AND is_temporary = 0").Scan(&logTables).Error
	} else {
		logTables, tableErr = model.LOG_DB.Migrator().GetTables()
	}
	if tableErr != nil {
		fail("read log database table inventory: %v", tableErr)
	}
	if !slices.Contains(logTables, "logs") {
		fail("log startup did not create logs")
	}
	expectedLogDialect := os.Getenv("MIGRATION_LOG_DIALECT")
	if expectedLogDialect == "" || model.LOG_DB.Dialector.Name() != expectedLogDialect {
		fail("unexpected log dialect: got %s, want %s", model.LOG_DB.Dialector.Name(), expectedLogDialect)
	}
	shared := os.Getenv("MIGRATION_SHARED_LOG") == "true"
	if (model.LOG_DB == model.DB) != shared {
		fail("unexpected primary/log connection sharing")
	}
	if !shared && model.DB.Dialector.Name() == model.LOG_DB.Dialector.Name() {
		primaryName := model.DB.Migrator().CurrentDatabase()
		logName := model.LOG_DB.Migrator().CurrentDatabase()
		if primaryName == "" || logName == "" || primaryName == logName {
			fail("separate primary/log scenario did not select different databases")
		}
	}

	var version string
	versionQuery := "SELECT sqlite_version()"
	switch common.MainDatabaseType() {
	case common.DatabaseTypeMySQL:
		versionQuery = "SELECT VERSION()"
	case common.DatabaseTypePostgreSQL:
		versionQuery = "SELECT version()"
	}
	if err := model.DB.Raw(versionQuery).Scan(&version).Error; err != nil {
		fail("read database version: %v", err)
	}
	fmt.Printf("database=%s version=%s log_database=%s shared=%t phase=%s\n", common.MainDatabaseType(), version, expectedLogDialect, shared, phase)

	if phase == "seed" {
		seedReleasedRows()
		return
	}
	if strings.HasPrefix(phase, "upgrade-") {
		verifyCurrentMigration()
	}
	verifyUsageMigration(strings.HasSuffix(phase, "-first"), shared, logTables)
}

func seedReleasedRows() {
	rows := []model.Redemption{
		{
			UserId:      41,
			Key:         firstLegacyKey,
			Name:        "released-migration-row-one",
			Status:      common.RedemptionCodeStatusEnabled,
			Quota:       250,
			CreatedTime: 1_700_000_001,
		},
		{
			UserId:      41,
			Key:         secondLegacyKey,
			Name:        "released-migration-row-two",
			Status:      common.RedemptionCodeStatusEnabled,
			Quota:       300,
			CreatedTime: 1_700_000_002,
		},
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		fail("seed released redemption rows: %v", err)
	}
	logRow, quotaRow := releasedUsageRows()
	if err := model.LOG_DB.Create(&logRow).Error; err != nil {
		fail("seed released usage log: %v", err)
	}
	if err := model.DB.Create(&quotaRow).Error; err != nil {
		fail("seed released dashboard row: %v", err)
	}
	indexes := readRedemptionIndexes()
	if !hasUniqueKeyIndex(indexes) {
		fail("released migration did not create a unique redemption key index")
	}
	manifestPath := os.Getenv("REDEMPTION_INDEX_MANIFEST")
	if manifestPath == "" {
		fail("REDEMPTION_INDEX_MANIFEST is required")
	}
	var lines []string
	for _, index := range indexes {
		lines = append(lines, index.signature())
	}
	slices.Sort(lines)
	manifest, err := common.Marshal(releasedMigrationManifest{
		RedemptionIndexes: lines,
		UsageLogID:        logRow.Id,
		QuotaDataID:       quotaRow.Id,
	})
	if err != nil {
		fail("encode released migration manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		fail("write released migration manifest: %v", err)
	}
	assertDuplicateKeyRejected()
	fmt.Println("seeded legacy redemption, usage log and dashboard rows and recorded released indexes and usage row IDs")
}

func verifyCurrentMigration() {
	for _, table := range []string{
		"management_operation_batches", "audit_event_outbox",
		"usage_events", "usage_hours", "usage_channels", "usage_channel_models",
		"usage_statistics_lineages", "usage_statistics_coverages", "usage_event_backfills", "usage_event_backfill_tuples",
		"usage_statistics_transitions", "usage_legacy_acknowledgements", "usage_legacy_hours",
	} {
		if !model.DB.Migrator().HasTable(table) {
			fail("current startup migration did not create %s", table)
		}
	}
	manifestPath := os.Getenv("REDEMPTION_INDEX_MANIFEST")
	if manifestPath == "" {
		fail("REDEMPTION_INDEX_MANIFEST is required")
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		fail("read released migration manifest: %v", err)
	}
	var manifest releasedMigrationManifest
	if err := common.Unmarshal(manifestData, &manifest); err != nil {
		fail("decode released migration manifest: %v", err)
	}
	if (manifest.UsageLogID <= 0 && model.LOG_DB.Dialector.Name() != "clickhouse") || manifest.QuotaDataID <= 0 {
		fail("released migration manifest is missing seeded usage row IDs")
	}
	currentIndexes := readRedemptionIndexes()
	currentSignatures := make(map[string]struct{}, len(currentIndexes))
	for _, index := range currentIndexes {
		currentSignatures[index.signature()] = struct{}{}
	}
	for _, releasedIndex := range manifest.RedemptionIndexes {
		if _, ok := currentSignatures[releasedIndex]; !ok {
			fail("current migration lost released redemption index: %s", releasedIndex)
		}
	}
	if !hasUniqueKeyIndex(currentIndexes) {
		fail("current migration did not preserve unique redemption key index")
	}

	var rows []model.Redemption
	if err := model.DB.Where(map[string]any{"key": []string{firstLegacyKey, secondLegacyKey}}).Find(&rows).Error; err != nil {
		fail("read released redemption rows: %v", err)
	}
	if len(rows) != 2 {
		fail("expected two released redemption rows, got %d", len(rows))
	}
	byKey := make(map[string]model.Redemption, len(rows))
	for _, row := range rows {
		byKey[row.Key] = row
	}
	assertReleasedRow(byKey, firstLegacyKey, "released-migration-row-one", 250, 1_700_000_001)
	assertReleasedRow(byKey, secondLegacyKey, "released-migration-row-two", 300, 1_700_000_002)
	expectedLog, expectedQuota := releasedUsageRows()
	expectedLog.Id = manifest.UsageLogID
	expectedQuota.Id = manifest.QuotaDataID
	var logs []model.Log
	if err := model.LOG_DB.Where("request_id = ?", expectedLog.RequestId).Find(&logs).Error; err != nil {
		fail("read released usage log: %v", err)
	}
	if len(logs) != 1 {
		fail("expected one released usage log, got %d", len(logs))
	}
	if logs[0] != expectedLog {
		fail("released usage log fields changed during migration")
	}
	var quotas []model.QuotaData
	if err := model.DB.Where("username = ?", expectedQuota.Username).Find(&quotas).Error; err != nil {
		fail("read released dashboard row: %v", err)
	}
	if len(quotas) != 1 {
		fail("expected one released dashboard row, got %d", len(quotas))
	}
	if quotas[0] != expectedQuota {
		fail("released dashboard fields changed during migration")
	}
	assertDuplicateKeyRejected()
	fmt.Printf("verified %d released indexes, redemption rows and retained usage history\n", len(manifest.RedemptionIndexes))
}

func releasedUsageRows() (model.Log, model.QuotaData) {
	return model.Log{
			UserId:            41,
			CreatedAt:         1_700_000_005,
			Type:              model.LogTypeConsume,
			Content:           "released migration usage fixture",
			Username:          "released-usage-user",
			TokenName:         "released-token",
			ModelName:         "released-model",
			Quota:             275,
			PromptTokens:      37,
			CompletionTokens:  19,
			UseTime:           2,
			IsStream:          true,
			ChannelId:         17,
			TokenId:           29,
			Group:             "released-group",
			RequestId:         "released-migration-usage",
			UpstreamRequestId: "released-upstream-usage",
			Other:             "{}",
		}, model.QuotaData{
			UserID:              41,
			Username:            "released-usage-user",
			ModelName:           "released-model",
			CreatedAt:           1_699_999_200,
			UseGroup:            "released-group",
			TokenID:             29,
			ChannelID:           17,
			NodeName:            "released-node",
			TokenUsed:           56,
			Count:               1,
			Quota:               275,
			CacheReadTokens:     11,
			CacheCreationTokens: 7,
		}
}

// These physical migration fixtures deliberately use maps and existing GORM
// APIs: this entire probe must also compile in the released source tree, where
// the statistics types and functions do not exist. Behavior/recovery is covered
// separately by TestUsageEventFoundationDatabaseMatrix, not by these inserts.
func usageMigrationTables() []usageMigrationTable {
	event := map[string]any{
		"event_key": strings.Repeat("a", 64), "key_version": int64(1),
		"payload_digest": strings.Repeat("b", 64), "source_namespace": strings.Repeat("c", 32),
		"collection_epoch": strings.Repeat("d", 32), "writer_epoch": strings.Repeat("e", 32),
		"activation_generation": int64(1), "source_kind": int64(1),
		"occurrence_uid": strings.Repeat("f", 32), "source_operation_ref": "migration-occurrence",
		"source_row_id": int64(0), "source_tuple_digest": "", "source_ordinal": int64(0),
		"task_primary_id": int64(0), "task_operation_sequence": int64(0),
		"source_state": int64(3), "kind": int64(1), "eligibility": int64(3), "known_mask": int64(262143),
		"occurred_at": int64(1700000123), "prepared_at": int64(1700000124),
		"updated_at": int64(1700000125), "applied_at": int64(1700000125),
		"user_id": int64(41), "token_id": int64(29), "channel_id": int64(17),
		"username": "historical-owner", "token_name": "historical-token", "model_name": "model-甲",
		"group_name": "historical-group", "request_id": "migration-request", "upstream_request_id": "migration-upstream",
		"writer_node": "migration-writer", "export_node": "migration-ingress",
		"request_digest": strings.Repeat("1", 64), "upstream_request_digest": strings.Repeat("2", 64),
		"prompt_tokens": int64(100), "completion_tokens": int64(20),
		"cache_read_tokens": int64(30), "cache_write_tokens": int64(5), "reported_input_tokens": int64(100),
		"quota_magnitude": int64(7), "usage_semantic": int64(1), "applied_version": int64(1),
		"next_attempt_at": int64(0), "attempts": int64(0), "error_code": "", "outcome_evidence_digest": "",
	}
	hour := map[string]any{
		"projection_version": int64(1), "dimension_key": strings.Repeat("3", 64),
		"source_namespace": strings.Repeat("c", 32), "collection_epoch": strings.Repeat("d", 32),
		"hour_start": int64(1699999200), "user_id": int64(41), "username": "historical-owner",
		"model_name": "model-甲", "group_name": "historical-group", "token_id": int64(29), "channel_id": int64(17),
		"export_node": "migration-ingress", "dimension_known_mask": int64(1),
		"consume_count": int64(1), "gross_quota": int64(7), "prompt_tokens": int64(100), "completion_tokens": int64(20),
		"ranking_tokens": int64(120), "cache_read_tokens": int64(30), "cache_write_tokens": int64(5),
		"input_denominator": int64(100), "cache_unknown_count": int64(0), "semantic_unknown_count": int64(0),
	}
	channel := map[string]any{
		"projection_version": int64(1), "channel_id": int64(17), "consume_count": int64(1), "gross_quota": int64(7),
		"prompt_tokens": int64(100), "completion_tokens": int64(20), "last_used_at": int64(1700000123),
	}
	channelModel := maps.Clone(channel)
	channelModel["model_key"], channelModel["model_name"], channelModel["model_known"] = strings.Repeat("4", 64), "model-甲", true
	lineage := map[string]any{
		// This physical-schema fixture must not impersonate the installed
		// operator lineage at ID 1 when the next startup validates membership.
		"id": int64(2), "lineage_digest": strings.Repeat("5", 64), "legacy_quota_data_max_id": int64(3),
		"legacy_baseline_digest": strings.Repeat("6", 64), "fence_digest": strings.Repeat("7", 64),
		"legacy_source_namespace": strings.Repeat("a", 32), "legacy_collection_epoch": strings.Repeat("b", 32),
		"live_source_namespace": strings.Repeat("c", 32), "live_collection_epoch": strings.Repeat("d", 32),
		"installed_at": int64(1700000100),
	}
	coverage := map[string]any{
		"coverage_key": strings.Repeat("8", 64), "family": int64(1),
		"start_at": int64(1699999200), "end_at": int64(1700002800),
		"lineage_digest": strings.Repeat("5", 64), "manifest_digest": strings.Repeat("6", 64),
		"source_seal": strings.Repeat("7", 64), "verified_at": int64(1700002801),
	}
	backfill := map[string]any{
		"source_namespace": strings.Repeat("a", 32), "source_kind": int64(8), "collection_epoch": strings.Repeat("b", 32),
		"start_at": int64(1699999200), "end_at": int64(1700002800), "high_watermark": int64(0), "expected_rows": int64(2),
		"manifest_digest": strings.Repeat("6", 64), "fence_digest": strings.Repeat("7", 64), "revision": int64(2),
		"last_row_id": int64(0), "cursor_tuple": "migration-source-tuple", "cursor_ordinal": int64(2), "imported_rows": int64(2),
		"rolling_digest": strings.Repeat("6", 64), "unknown_field_rows": int64(1),
		"verified_at": int64(1700002801), "updated_at": int64(1700002801),
	}
	backfillTuple := map[string]any{
		"tuple_key": strings.Repeat("a", 64), "source_namespace": strings.Repeat("a", 32),
		"tuple_digest": strings.Repeat("b", 64), "source_tuple": "migration-source-tuple", "multiplicity": int64(2),
	}
	// The transition anchor is lazy in production: startup must leave it absent.
	// Insert ID 1 only after the empty-table proof, without invoking a writer or
	// installing the operational lineage. These are physical-schema fixtures.
	transition := map[string]any{"id": int64(1)}
	acknowledgement := map[string]any{
		"event_key": event["event_key"], "payload_digest": event["payload_digest"], "quota_data_id": int64(3),
	}
	// The owned-hour payload matches UsageHour's typed dimensions/measures, but
	// has no generated ID: dimension_key alone is its primary identity.
	legacyHour := maps.Clone(hour)
	return []usageMigrationTable{
		{"usage_events", event, []string{"id"}, []redemptionIndex{
			{"ux_usage_event_key", []string{"event_key"}, true, true},
			{"ix_usage_event_drain", []string{"source_state", "applied_version", "next_attempt_at", "id"}, false, true},
			{"ix_usage_event_pending_time", []string{"source_state", "applied_version", "kind", "occurred_at", "id"}, false, true},
			{"ix_usage_event_time", []string{"source_state", "kind", "occurred_at", "id"}, false, true},
			{"ix_usage_event_self", []string{"user_id", "source_state", "kind", "occurred_at", "id"}, false, true},
			{"ix_usage_event_channel", []string{"channel_id", "source_state", "kind", "occurred_at", "id"}, false, true},
			{"ix_usage_event_channel_tail", []string{"channel_id", "source_state", "applied_version", "kind", "id"}, false, true},
			{"ix_usage_event_task", []string{"task_primary_id", "task_operation_sequence"}, false, true},
			{"ix_usage_event_request", []string{"request_digest", "source_state", "kind", "occurred_at"}, false, true},
			{"ix_usage_event_upstream_request", []string{"upstream_request_digest", "source_state", "kind", "occurred_at"}, false, true},
		}},
		{"usage_hours", hour, []string{"id"}, []redemptionIndex{
			{"ux_usage_hour_key", []string{"projection_version", "dimension_key"}, true, true},
			{"ix_usage_hour_time", []string{"projection_version", "hour_start", "id"}, false, true},
			{"ix_usage_hour_self", []string{"user_id", "projection_version", "hour_start", "id"}, false, true},
			{"ix_usage_hour_lineage", []string{"source_namespace", "collection_epoch", "projection_version", "hour_start", "id"}, false, true},
			{"ix_usage_hour_lineage_self", []string{"source_namespace", "collection_epoch", "user_id", "projection_version", "hour_start", "id"}, false, true},
		}},
		{"usage_channels", channel, []string{"projection_version", "channel_id"}, nil},
		{"usage_channel_models", channelModel, []string{"id"}, []redemptionIndex{
			{"ux_usage_channel_model", []string{"projection_version", "channel_id", "model_key"}, true, true},
			{"ix_usage_channel_models", []string{"projection_version", "channel_id", "id"}, false, true},
		}},
		{"usage_statistics_lineages", lineage, []string{"id"}, nil},
		{"usage_statistics_coverages", coverage, []string{"coverage_key"}, []redemptionIndex{
			{"ix_usage_coverage_range", []string{"family", "start_at"}, false, true},
		}},
		{"usage_event_backfills", backfill, []string{"source_namespace"}, nil},
		{"usage_event_backfill_tuples", backfillTuple, []string{"tuple_key"}, nil},
		{"usage_statistics_transitions", transition, []string{"id"}, nil},
		{"usage_legacy_acknowledgements", acknowledgement, []string{"event_key"}, nil},
		{"usage_legacy_hours", legacyHour, []string{"dimension_key"}, nil},
	}
}

func verifyUsageMigration(first, shared bool, logTables []string) {
	manifestPath := os.Getenv("MIGRATION_USAGE_MANIFEST")
	if manifestPath == "" {
		fail("MIGRATION_USAGE_MANIFEST is required")
	}
	previous := map[string]usageMigrationSnapshot{}
	if !first {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			fail("read first-start usage manifest: %v", err)
		}
		if err := common.Unmarshal(data, &previous); err != nil {
			fail("decode first-start usage manifest: %v", err)
		}
	}
	current := map[string]usageMigrationSnapshot{}
	tables := usageMigrationTables()
	// Prove startup left the entire statistics inventory empty before inserting
	// any fixture. This includes the lazy transition anchor and both ownership
	// tables, on fresh current databases AND actual released-schema upgrades.
	for _, table := range tables {
		if !model.DB.Migrator().HasTable(table.name) {
			fail("primary startup did not create %s", table.name)
		}
		if !shared && slices.Contains(logTables, table.name) {
			fail("primary-only statistics table %s was created in LOG_DB", table.name)
		}
		if first {
			var count int64
			if err := model.DB.Table(table.name).Count(&count).Error; err != nil || count != 0 {
				fail("first-start statistics fixture is not empty: table=%s count=%d error=%v", table.name, count, err)
			}
		}
	}
	for _, table := range tables {
		if first {
			row := maps.Clone(table.values)
			if err := model.DB.Table(table.name).Create(&row).Error; err != nil {
				fail("seed first-start statistics row in %s: %v", table.name, err)
			}
		}
		snapshot := readUsageMigrationSnapshot(table)
		if !first && !reflect.DeepEqual(snapshot, previous[table.name]) {
			fail("second startup changed statistics schema, indexes, primary key or full row in %s", table.name)
		}
		verifyUsageConstraints(table)
		current[table.name] = snapshot
	}
	if first {
		data, err := common.Marshal(current)
		if err != nil {
			fail("encode first-start usage manifest: %v", err)
		}
		file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			fail("create first-start usage manifest: %v", err)
		}
		if _, err := file.Write(data); err != nil {
			fail("write first-start usage manifest: %v", err)
		}
		if err := file.Close(); err != nil {
			fail("close first-start usage manifest: %v", err)
		}
	} else if len(previous) != len(current) {
		fail("first-start usage table inventory does not match second startup")
	}
	// Physical schema/row preservation does not execute source capture,
	// coverage attestation, historical import or end-to-end statistics readers.
	fmt.Printf("verified statistics tables=%d first_start=%t primary_only=%t full_rows_and_constraints=true\n", len(current), first, !shared)
}

func readUsageMigrationSnapshot(table usageMigrationTable) usageMigrationSnapshot {
	snapshot := usageMigrationSnapshot{Primary: readPrimaryKey(table.name), Row: map[string]string{}}
	if !slices.Equal(snapshot.Primary, table.primary) {
		fail("incorrect primary key in %s: got %v, want %v", table.name, snapshot.Primary, table.primary)
	}
	columns, err := model.DB.Migrator().ColumnTypes(table.name)
	if err != nil {
		fail("read %s columns: %v", table.name, err)
	}
	expectedColumns := len(table.values)
	if _, explicitID := table.values["id"]; slices.Contains(table.primary, "id") && !explicitID {
		expectedColumns++
	}
	if len(columns) != expectedColumns {
		fail("%s has %d columns, expected %d; update the explicit migration fixture for schema additions", table.name, len(columns), expectedColumns)
	}
	for _, column := range columns {
		name := column.Name()
		value, exists := table.values[name]
		if !exists && name != "id" {
			fail("unexpected migration column %s.%s", table.name, name)
		}
		dataType := strings.ToLower(column.DatabaseTypeName())
		columnType, typeKnown := column.ColumnType()
		length, lengthKnown := column.Length()
		nullable, nullableKnown := column.Nullable()
		autoIncrement, autoKnown := column.AutoIncrement()
		defaultValue, defaultKnown := column.DefaultValue()
		if !slices.Contains(table.primary, name) && (!nullableKnown || nullable) {
			fail("statistics column %s.%s must be NOT NULL", table.name, name)
		}
		switch value.(type) {
		case string:
			expectedLength := int64(0)
			switch name {
			case "source_namespace", "collection_epoch", "writer_epoch", "occurrence_uid", "live_source_namespace", "live_collection_epoch",
				"legacy_source_namespace", "legacy_collection_epoch":
				expectedLength = 32
			case "event_key", "payload_digest", "source_tuple_digest", "request_digest", "upstream_request_digest", "outcome_evidence_digest", "dimension_key", "model_key",
				"lineage_digest", "legacy_baseline_digest", "fence_digest", "coverage_key", "manifest_digest", "source_seal", "rolling_digest", "tuple_key", "tuple_digest":
				expectedLength = 64
			case "error_code":
				expectedLength = 48
			}
			if expectedLength != 0 {
				validType := dataType == "char" || dataType == "bpchar"
				if name == "error_code" {
					validType = dataType == "varchar"
				}
				if !lengthKnown || length != expectedLength || !validType {
					fail("invalid bounded text type for %s.%s: %s length=%d known=%t", table.name, name, dataType, length, lengthKnown)
				}
			} else if dataType != "text" {
				fail("historical dimension %s.%s is not TEXT: %s", table.name, name, dataType)
			}
		case bool:
			if !slices.Contains([]string{"bool", "boolean", "numeric", "tinyint"}, dataType) {
				fail("unexpected boolean type for %s.%s: %s", table.name, name, dataType)
			}
		default:
			expectedType := "bigint"
			if name == "id" && model.DB.Dialector.Name() == "sqlite" {
				expectedType = "integer"
			}
			if slices.Contains([]string{"key_version", "source_kind", "source_state", "kind", "usage_semantic", "family"}, name) {
				expectedType = "smallint"
			}
			if dataType != expectedType && !(expectedType == "bigint" && dataType == "int8") && !(expectedType == "smallint" && dataType == "int2") {
				fail("unexpected numeric type for %s.%s: %s, want %s", table.name, name, dataType, expectedType)
			}
		}
		snapshot.Columns = append(snapshot.Columns, fmt.Sprintf("%s|%s|%s|%t|%d|%t|%t|%t|%t|%t|%s|%t", name, dataType, columnType, typeKnown, length, lengthKnown, nullable, nullableKnown, autoIncrement, autoKnown, defaultValue, defaultKnown))
	}
	slices.Sort(snapshot.Columns)
	for _, index := range readMigrationIndexes(model.DB, table.name) {
		snapshot.Indexes = append(snapshot.Indexes, index.signature())
	}
	slices.Sort(snapshot.Indexes)
	for _, expected := range table.indexes {
		if !slices.Contains(snapshot.Indexes, expected.signature()) {
			fail("missing or altered statistics index %s.%s", table.name, expected.name)
		}
	}
	rows, err := model.DB.Table(table.name).Rows()
	if err != nil {
		fail("read full statistics row in %s: %v", table.name, err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		fail("read statistics row columns in %s: %v", table.name, err)
	}
	if !rows.Next() {
		fail("statistics row missing in %s: %v", table.name, rows.Err())
	}
	values := make([]any, len(names))
	pointers := make([]any, len(names))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		fail("scan statistics row in %s: %v", table.name, err)
	}
	for i, name := range names {
		if values[i] == nil {
			fail("unexpected NULL in %s.%s", table.name, name)
		}
		value := fmt.Sprint(values[i])
		if raw, ok := values[i].([]byte); ok {
			value = string(raw)
		}
		if _, boolean := table.values[name].(bool); boolean {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				fail("invalid stored boolean in %s.%s: %v", table.name, name, err)
			}
			value = strconv.FormatBool(parsed)
		}
		// PostgreSQL pads CHAR values; preserve the exact stored bytes in the
		// snapshot while checking the intended blank digest value separately.
		if expected, exists := table.values[name]; exists && strings.TrimRight(value, " ") != fmt.Sprint(expected) {
			fail("statistics fixture value changed in %s.%s", table.name, name)
		}
		if name == "id" {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				fail("statistics ID is invalid in %s", table.name)
			}
		}
		snapshot.Row[name] = value
	}
	if rows.Next() || rows.Err() != nil {
		fail("expected exactly one statistics row in %s: %v", table.name, rows.Err())
	}
	return snapshot
}

func readPrimaryKey(table string) []string {
	var columns []string
	var err error
	switch model.DB.Dialector.Name() {
	case "mysql":
		err = model.DB.Raw(`SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY'
ORDER BY ORDINAL_POSITION`, table).Scan(&columns).Error
	case "postgres":
		err = model.DB.Raw(`SELECT attribute.attname
FROM pg_catalog.pg_index AS index_meta
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS item(attnum, position)
JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = index_meta.indrelid AND attribute.attnum = item.attnum
WHERE index_meta.indrelid = to_regclass(?) AND index_meta.indisprimary
ORDER BY item.position`, table).Scan(&columns).Error
	case "sqlite":
		type primaryColumn struct {
			Name string
			PK   int
		}
		var info []primaryColumn
		// table comes only from the fixed statistics inventory above.
		err = model.DB.Raw("PRAGMA table_info(" + table + ")").Scan(&info).Error
		slices.SortFunc(info, func(a, b primaryColumn) int { return a.PK - b.PK })
		for _, column := range info {
			if column.PK > 0 {
				columns = append(columns, column.Name)
			}
		}
	default:
		fail("unsupported primary database dialect")
	}
	if err != nil {
		fail("read primary key of %s: %v", table, err)
	}
	return columns
}

func verifyUsageConstraints(table usageMigrationTable) {
	duplicate := maps.Clone(table.values)
	err := model.DB.Transaction(func(tx *gorm.DB) error { return tx.Table(table.name).Create(&duplicate).Error })
	var mysqlError *mysql.MySQLError
	var sqlState interface{ SQLState() string }
	var sqliteCode interface{ Code() int }
	duplicateRejected := errors.Is(err, gorm.ErrDuplicatedKey) ||
		(errors.As(err, &mysqlError) && mysqlError.Number == 1062) ||
		(errors.As(err, &sqlState) && sqlState.SQLState() == "23505") ||
		(errors.As(err, &sqliteCode) && (sqliteCode.Code() == 1555 || sqliteCode.Code() == 2067))
	if !duplicateRejected {
		fail("%s did not reject duplicate natural identity with a unique/primary-key error: %v", table.name, err)
	}
	keys := table.primary
	for _, index := range table.indexes {
		if index.unique {
			keys = index.columns
		}
	}
	rollback := errors.New("rollback distinct migration fixture")
	for _, key := range keys {
		row := maps.Clone(table.values)
		switch value := row[key].(type) {
		case int64:
			row[key] = value + 1
		case string:
			row[key] = strings.Repeat("9", len(value))
		default:
			fail("unsupported natural-key fixture column %s.%s", table.name, key)
		}
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table(table.name).Create(&row).Error; err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			fail("changing one identity component must permit a distinct %s row (%s): %v", table.name, key, err)
		}
	}
}

func readRedemptionIndexes() []redemptionIndex {
	return readMigrationIndexes(model.DB, "redemptions")
}

func readMigrationIndexes(db *gorm.DB, table string) []redemptionIndex {
	if db.Dialector.Name() == "postgres" {
		// Include primary/unique constraints, which GetIndexes filters out,
		// and retain key order and schema identity from PostgreSQL's catalog.
		var rows []struct {
			Name    string
			Unique  bool
			Valid   bool
			Partial bool
			Column  string
		}
		if err := db.Raw(`SELECT index_class.relname AS name, index_meta.indisunique AS "unique",
index_meta.indisvalid AS valid, index_meta.indpred IS NOT NULL AS partial, attribute.attname AS "column"
FROM pg_catalog.pg_index AS index_meta
JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_meta.indexrelid
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS item(attnum, position)
LEFT JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = index_meta.indrelid AND attribute.attnum = item.attnum
WHERE index_meta.indrelid = to_regclass(?)
ORDER BY index_class.relname, item.position`, table).Scan(&rows).Error; err != nil {
			fail("read all ordered %s indexes: %v", table, err)
		}
		var result []redemptionIndex
		for _, row := range rows {
			if !row.Valid || row.Partial || row.Column == "" {
				fail("invalid, partial or expression index is outside the migration contract: %s.%s", table, row.Name)
			}
			if len(result) == 0 || result[len(result)-1].name != row.Name {
				result = append(result, redemptionIndex{name: row.Name, unique: row.Unique, known: true})
			}
			result[len(result)-1].columns = append(result[len(result)-1].columns, row.Column)
		}
		return result
	}
	if db.Dialector.Name() == "sqlite" {
		var indexes []struct {
			Name    string
			Unique  bool
			Partial bool
		}
		if err := db.Raw(`SELECT name, "unique", partial FROM pragma_index_list(?) ORDER BY name`, table).Scan(&indexes).Error; err != nil {
			fail("read all %s SQLite indexes: %v", table, err)
		}
		result := make([]redemptionIndex, 0, len(indexes))
		for _, index := range indexes {
			var columns []string
			if err := db.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", index.Name).Scan(&columns).Error; err != nil {
				fail("read ordered %s.%s SQLite index columns: %v", table, index.Name, err)
			}
			if index.Partial || len(columns) == 0 || slices.Contains(columns, "") {
				fail("partial, empty or expression SQLite index is outside the migration contract: %s.%s", table, index.Name)
			}
			result = append(result, redemptionIndex{name: index.Name, columns: columns, unique: index.Unique, known: true})
		}
		return result
	}
	if db.Dialector.Name() != "mysql" {
		fail("unsupported migration index dialect: %s", db.Dialector.Name())
	}
	var rows []struct {
		Name       string
		NonUnique  bool
		ColumnName string
		Prefix     int
	}
	if err := db.Raw(`SELECT INDEX_NAME AS name, NON_UNIQUE AS non_unique,
COLUMN_NAME AS column_name, COALESCE(SUB_PART, 0) AS prefix
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`, table).Scan(&rows).Error; err != nil {
		fail("read all ordered %s MySQL indexes: %v", table, err)
	}
	var result []redemptionIndex
	for _, row := range rows {
		if row.Prefix != 0 || row.ColumnName == "" {
			fail("prefix or expression MySQL index is outside the migration contract: %s.%s", table, row.Name)
		}
		if len(result) == 0 || result[len(result)-1].name != row.Name {
			result = append(result, redemptionIndex{name: row.Name, unique: !row.NonUnique, known: true})
		}
		result[len(result)-1].columns = append(result[len(result)-1].columns, row.ColumnName)
	}
	return result
}

func (index redemptionIndex) signature() string {
	columns := append([]string(nil), index.columns...)
	return fmt.Sprintf("%s|%t|%t|%s", index.name, index.unique, index.known, strings.Join(columns, ","))
}

func hasUniqueKeyIndex(indexes []redemptionIndex) bool {
	for _, index := range indexes {
		if index.unique && index.known && slices.Contains(index.columns, "key") {
			return true
		}
	}
	return false
}

func assertReleasedRow(rows map[string]model.Redemption, key, name string, quota int, createdTime int64) {
	row, ok := rows[key]
	if !ok {
		fail("released redemption %s is missing", key)
	}
	if row.UserId != 41 || row.Name != name || row.Status != common.RedemptionCodeStatusEnabled || row.Quota != quota || row.CreatedTime != createdTime {
		fail("released redemption %s changed: user=%d name=%q status=%d quota=%d created=%d", key, row.UserId, row.Name, row.Status, row.Quota, row.CreatedTime)
	}
}

func assertDuplicateKeyRejected() {
	duplicate := model.Redemption{
		UserId:      42,
		Key:         firstLegacyKey,
		Name:        "duplicate-key-check",
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       100,
		CreatedTime: 1_700_000_003,
	}
	if err := model.DB.Create(&duplicate).Error; err == nil {
		fail("duplicate redemption key was accepted")
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
