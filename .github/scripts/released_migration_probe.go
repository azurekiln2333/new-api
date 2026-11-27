package main

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

// This file is also compiled inside the actual released source checkout. Keep
// new allocation fields behind table/column inspection, not current Go structs.
type allocationMigrationManifest struct {
	Version        int                      `json:"version"`
	Database       string                   `json:"database"`
	Stage          string                   `json:"stage"`
	ChannelID      int                      `json:"channel_id"`
	MappingEntryID int64                    `json:"mapping_entry_id"`
	Released       []migrationTableSnapshot `json:"released"`
	AfterFirst     []migrationTableSnapshot `json:"after_first"`
}

type migrationTableSnapshot struct {
	Table       string                   `json:"table"`
	Columns     []string                 `json:"columns"`
	PrimaryKeys []string                 `json:"primary_keys"`
	Indexes     []migrationIndexSnapshot `json:"indexes"`
	Rows        []map[string]*string     `json:"rows"`
}

type migrationIndexSnapshot struct {
	Name         string   `json:"name"`
	Columns      []string `json:"columns"`
	Unique       bool     `json:"unique"`
	UniqueKnown  bool     `json:"unique_known"`
	Primary      bool     `json:"primary"`
	PrimaryKnown bool     `json:"primary_known"`
}

// A local interface compiles against the old source without referring to an
// absent model symbol. Only current verification requires the overlay reader.
type allocationMappingReader interface {
	GetModelMappingForKey(int) (string, error)
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "seed" && os.Args[1] != "verify") {
		fail("usage: released_migration_probe.go seed|verify")
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
	// This fixture owns disposable primary databases; audit history uses the
	// same engine. Do not connect the probe to a separately configured store.
	if os.Getenv("LOG_SQL_DSN") != "" {
		fail("released migration probe requires an unset LOG_SQL_DSN")
	}
	if err := model.InitDB(); err != nil {
		fail("initialize database: %v", err)
	}
	if err := model.InitLogDB(); err != nil {
		fail("initialize shared log database: %v", err)
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
	fmt.Printf("database=%s version=%s phase=%s\n", common.MainDatabaseType(), version, phase)

	if phase == "seed" {
		if _, err := os.Stat(allocationManifestPath()); !os.IsNotExist(err) {
			fail("allocation seed requires a new manifest: %v", err)
		}
		seedReleasedRows()
		seedReleasedAllocations()
		return
	}
	verifyCurrentMigration()
	verifyAllocationMigration()
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
	if err := os.WriteFile(manifestPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		fail("write released index manifest: %v", err)
	}
	assertDuplicateKeyRejected()
	fmt.Println("seeded two legacy redemption rows and recorded released indexes")
}

func verifyCurrentMigration() {
	for _, table := range []string{"management_operation_batches", "audit_event_outbox"} {
		if !model.DB.Migrator().HasTable(table) {
			fail("current startup migration did not create %s", table)
		}
	}
	manifestPath := os.Getenv("REDEMPTION_INDEX_MANIFEST")
	if manifestPath == "" {
		fail("REDEMPTION_INDEX_MANIFEST is required")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		fail("read released index manifest: %v", err)
	}
	var releasedIndexes []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(manifest)), "\n") {
		if line != "" {
			releasedIndexes = append(releasedIndexes, line)
		}
	}
	currentIndexes := readRedemptionIndexes()
	currentSignatures := make(map[string]struct{}, len(currentIndexes))
	for _, index := range currentIndexes {
		currentSignatures[index.signature()] = struct{}{}
	}
	for _, releasedIndex := range releasedIndexes {
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
	assertDuplicateKeyRejected()
	fmt.Printf("verified %d released indexes and two legacy rows\n", len(releasedIndexes))
}

func readRedemptionIndexes() []redemptionIndex {
	indexes, err := model.DB.Migrator().GetIndexes(&model.Redemption{})
	if err != nil {
		fail("read redemption indexes: %v", err)
	}
	result := make([]redemptionIndex, 0, len(indexes))
	for _, index := range indexes {
		unique, known := index.Unique()
		result = append(result, redemptionIndex{
			name:    index.Name(),
			columns: index.Columns(),
			unique:  unique,
			known:   known,
		})
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

func seedReleasedAllocations() {
	owner := model.User{
		Username:    "migration-owner",
		DisplayName: "Migration owner",
		Password:    "disabled-fixture-not-a-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusDisabled,
		AffCode:     "released-migration-owner",
		Quota:       1000,
		UsedQuota:   77,
		CreatedAt:   1_700_000_009,
	}
	if err := model.DB.Create(&owner).Error; err != nil {
		fail("seed disabled allocation owner: %v", err)
	}
	mapping := `{"alias":"Channel-Upstream","shared":"Shared-Upstream"}`
	channel := model.Channel{
		Type:         1,
		Status:       common.ChannelStatusEnabled,
		Name:         "released-allocation-migration",
		Key:          "migration-public-key\nmigration-bound-key\nmigration-empty-scope-key\nmigration-retired-key",
		Models:       "alias,shared,DeepSeek-R1",
		Group:        "default",
		UsedQuota:    77,
		CreatedTime:  1_700_000_010,
		ModelMapping: &mapping,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:             true,
			MultiKeySize:           4,
			MultiKeyUsers:          map[int]int{1: owner.Id},
			MultiKeyModels:         map[int][]string{1: {"DeepSeek-R1"}},
			MultiKeyScopeModes:     map[int]string{1: "allowlist", 2: "allowlist"},
			MultiKeyStatusList:     map[int]int{1: common.ChannelStatusManuallyDisabled, 2: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledReason: map[int]string{1: "manual fixture", 2: "upstream fixture"},
			MultiKeyDisabledTime:   map[int]int64{1: 1_700_000_011, 2: 1_700_000_012},
		},
	}
	// Both APIs exist in the released source: this creates real allocations,
	// then retires one through the production transaction rather than inventing
	// a reconstructed allocation schema or assigning our own primary keys.
	if err := channel.Insert(); err != nil {
		fail("seed released channel allocations: %v", err)
	}
	updated, err := model.MutateChannelKeyEntries(context.Background(), channel.Id, channel.ChannelInfo.KeyEntryRevision,
		func(_ *gorm.DB, _ *model.Channel, entries []model.ChannelKeyEntry) ([]model.ChannelKeyEntry, []model.ChannelKeyEntry, error) {
			if len(entries) != 4 {
				return nil, nil, fmt.Errorf("expected four released allocations, got %d", len(entries))
			}
			entries[0].ConfigRevision = 3
			entries[1].ConfigRevision = 7
			entries[2].ConfigRevision = 9
			entries[3].MergedIntoId = entries[0].Id
			return entries[:3], entries[3:], nil
		})
	if err != nil {
		fail("seed released allocation revisions and retirement: %v", err)
	}
	var entries []model.ChannelKeyEntry
	if err := model.DB.Where("channel_id = ? AND retired_at = 0", channel.Id).Order("position, id").Find(&entries).Error; err != nil || len(entries) != 3 {
		fail("read seeded stable allocations: count=%d error=%v", len(entries), err)
	}
	if entries[0].ScopeMode != "all" || entries[0].ConfigRevision != 3 || entries[0].Status != common.ChannelStatusEnabled ||
		entries[1].BoundUserId != owner.Id || entries[1].ScopeMode != "allowlist" || entries[1].Models != `["DeepSeek-R1"]` ||
		entries[1].ConfigRevision != 7 || entries[1].Status != common.ChannelStatusManuallyDisabled ||
		entries[2].ScopeMode != "allowlist" || entries[2].Models != "[]" || entries[2].ConfigRevision != 9 || entries[2].Status != common.ChannelStatusAutoDisabled {
		fail("released APIs did not create the representative allocation state")
	}
	// Current-source fresh seeding exercises SQL NULL and explicit empty
	// inheritance too. Released sources without the column never name a new
	// struct field. Preserve a future protocol column only when it exists.
	columns, err := model.DB.Migrator().ColumnTypes("channel_key_entries")
	if err != nil {
		fail("inspect released allocation columns: %v", err)
	}
	for _, column := range columns {
		switch column.Name() {
		case "model_mapping":
			for i, entry := range entries {
				var value any = ""
				if i == 0 {
					value = nil
				}
				if err := model.DB.Table("channel_key_entries").Where("id = ?", entry.Id).Updates(map[string]any{"model_mapping": value}).Error; err != nil {
					fail("seed allocation inheritance: %v", err)
				}
			}
		case "upstream_protocol":
			for i, entry := range entries {
				protocol := []string{"inherit", "messages", "chat"}[i]
				if err := model.DB.Table("channel_key_entries").Where("id = ?", entry.Id).Updates(map[string]any{"upstream_protocol": protocol}).Error; err != nil {
					fail("seed existing allocation protocol: %v", err)
				}
			}
		}
	}

	// The released ledger APIs keep these fixtures coherent without activating
	// accounting or charging a wallet: historical usage, a reset, current-period
	// usage and a live hold all remain linked to the same stable allocation.
	scopes := []struct {
		kind model.ChannelBudgetScopeType
		id   int64
	}{
		{model.BudgetScopeChannel, int64(channel.Id)},
		{model.BudgetScopeKey, entries[0].Id},
	}
	for _, scope := range scopes {
		if _, err := model.SetChannelBudgetLimit(context.Background(), scope.kind, scope.id, 0, true, 1000); err != nil {
			fail("seed released budget limit: %v", err)
		}
	}
	selection := model.BudgetSelection{
		ChannelID: updated.Id, AllocationID: entries[0].Id, AllocationRevision: entries[0].ConfigRevision,
		UserID: owner.Id, Group: "default", ModelName: "alias",
	}
	for _, fixture := range []struct {
		attempt string
		reserve int64
		actual  int64
		settle  bool
	}{
		{"released-allocation-historical", 60, 45, true},
		{"released-allocation-current", 30, 17, true},
		{"released-allocation-live", 23, 0, false},
	} {
		reservation, err := model.ReserveChannelBudgets(context.Background(), model.BudgetReserveRequest{
			AttemptKey: fixture.attempt, Selection: selection, ReserveQuota: fixture.reserve,
			OwnerNode: "released-migration", ExpiresAt: 4_102_444_800,
		})
		if err != nil {
			fail("seed released budget reservation %s: %v", fixture.attempt, err)
		}
		if fixture.settle {
			if err := model.SettleChannelBudgets(context.Background(), model.BudgetSettleRequest{
				ReservationID: reservation.Id, Revision: 1, TotalActualQuota: fixture.actual, Final: true,
			}); err != nil {
				fail("seed released budget usage: %v", err)
			}
		}
		if fixture.attempt == "released-allocation-historical" {
			for _, scope := range scopes {
				if _, err := model.ResetChannelBudgetUsage(context.Background(), model.BudgetResetRequest{
					Scope: scope.kind, ScopeID: scope.id, ExpectedVersion: 1, OperationID: "released-reset-" + string(scope.kind),
				}); err != nil {
					fail("seed released budget reset history: %v", err)
				}
			}
		}
	}
	var accounts []model.ChannelBudgetAccount
	if err := model.DB.Where("channel_id = ?", channel.Id).Find(&accounts).Error; err != nil || len(accounts) != 2 {
		fail("read seeded budget accounts: count=%d error=%v", len(accounts), err)
	}
	for _, account := range accounts {
		if !account.Enabled || account.LimitQuota != 1000 || account.PeriodUsedQuota != 17 || account.LifetimeUsedQuota != 62 ||
			account.ReservedQuota != 23 || account.ResetEpoch != 1 || account.ConfigVersion != 2 {
			fail("released APIs did not create representative usage, reset and hold counters")
		}
	}

	// Released startup supports the shared relational audit log, but the
	// currently pinned release predates the outbox. Inspect optional storage
	// instead of referring to an AuditEventOutbox symbol absent in that source.
	audit := map[string]any{
		"event_id": "released-allocation-audit", "user_id": owner.Id, "username": owner.Username,
		"actor_role": owner.Role, "created_at": int64(1_700_000_020),
		"category": "operation", "action": "channel.multi_key_manage", "auth_method": "session",
		"method": "POST", "route": "/api/channel/multi_key/manage", "status": 200, "success": true,
		"request_id": "released-allocation-request", "content": "Allocation migration fixture", "other": "{}",
	}
	if err := model.DB.Table("audit_logs").Create(&audit).Error; err != nil {
		fail("seed released allocation audit: %v", err)
	}
	tables := []string{"users", "channels", "channel_key_entries", "channel_budget_accounts", "channel_budget_reservations", "channel_budget_reservation_items", "channel_budget_operations", "audit_logs"}
	if model.DB.Migrator().HasTable("audit_event_outbox") {
		outbox := map[string]any{
			"event_id": "released-allocation-outbox", "actor_user_id": owner.Id, "actor_username": owner.Username,
			"actor_role": owner.Role, "created_at": int64(1_700_000_021),
			"category": "operation", "action": "channel.multi_key_manage", "auth_method": "session",
			"method": "POST", "route": "/api/channel/multi_key/manage", "status": 200, "success": true,
			"request_id": "released-allocation-request", "content": "Allocation migration fixture", "params_json": "{}",
		}
		if err := model.DB.Table("audit_event_outbox").Create(&outbox).Error; err != nil {
			fail("seed supported allocation audit outbox: %v", err)
		}
		tables = append(tables, "audit_event_outbox")
	} else {
		fmt.Println("released source has no audit_event_outbox; retained audit coverage uses audit_logs")
	}
	manifest := allocationMigrationManifest{
		Version: 1, Database: string(common.MainDatabaseType()), Stage: "seeded",
		ChannelID: channel.Id, MappingEntryID: entries[0].Id,
	}
	for _, table := range tables {
		snapshot := readMigrationTable(table)
		assertMigrationConstraints(snapshot)
		assertMigrationTable(snapshot, readMigrationTable(table), 0)
		manifest.Released = append(manifest.Released, snapshot)
	}
	writeAllocationManifest(manifest)
	fmt.Printf("seeded stable allocations, ledger history/holds and audit; tables=%d mapping_entry=%d\n", len(tables), manifest.MappingEntryID)
}

func allocationManifestPath() string {
	path := os.Getenv("REDEMPTION_INDEX_MANIFEST")
	if path == "" {
		fail("REDEMPTION_INDEX_MANIFEST is required")
	}
	return path + ".allocations.json"
}

func writeAllocationManifest(manifest allocationMigrationManifest) {
	encoded, err := common.Marshal(manifest)
	if err != nil {
		fail("encode allocation migration manifest: %v", err)
	}
	path := allocationManifestPath()
	// A partial write cannot masquerade as a completed first verification.
	if err := os.WriteFile(path+".tmp", encoded, 0o600); err != nil {
		fail("write allocation migration manifest: %v", err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		fail("publish allocation migration manifest: %v", err)
	}
}

func readMigrationTable(table string) migrationTableSnapshot {
	columns, err := model.DB.Migrator().ColumnTypes(table)
	if err != nil {
		fail("inspect %s columns: %v", table, err)
	}
	result := migrationTableSnapshot{Table: table}
	for _, column := range columns {
		result.Columns = append(result.Columns, column.Name())
		if primary, known := column.PrimaryKey(); known && primary {
			result.PrimaryKeys = append(result.PrimaryKeys, column.Name())
		}
	}
	slices.Sort(result.Columns)
	slices.Sort(result.PrimaryKeys)
	if !slices.Equal(result.PrimaryKeys, []string{"id"}) {
		fail("%s must retain its id primary key; got %v", table, result.PrimaryKeys)
	}
	indexes, err := model.DB.Migrator().GetIndexes(table)
	if err != nil {
		fail("inspect %s indexes: %v", table, err)
	}
	for _, index := range indexes {
		unique, uniqueKnown := index.Unique()
		primary, primaryKnown := index.PrimaryKey()
		result.Indexes = append(result.Indexes, migrationIndexSnapshot{
			Name: index.Name(), Columns: index.Columns(), Unique: unique, UniqueKnown: uniqueKnown,
			Primary: primary, PrimaryKnown: primaryKnown,
		})
	}
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		// GetIndexes omits UNIQUE-constraint autoindexes on this SQLite driver.
		// PRAGMA table functions retain those real constraints as well.
		var catalog []struct {
			Name     string
			IsUnique bool
			Origin   string
		}
		if err := model.DB.Raw(`SELECT name, "unique" AS is_unique, origin FROM pragma_index_list(?) ORDER BY name`, table).Scan(&catalog).Error; err != nil {
			fail("inspect SQLite %s constraint/index catalog: %v", table, err)
		}
		result.Indexes = nil
		for _, index := range catalog {
			var columns []string
			if err := model.DB.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", index.Name).Scan(&columns).Error; err != nil {
				fail("inspect SQLite %s index %s: %v", table, index.Name, err)
			}
			result.Indexes = append(result.Indexes, migrationIndexSnapshot{
				Name: index.Name, Columns: columns, Unique: index.IsUnique, UniqueKnown: true,
				Primary: index.Origin == "pk", PrimaryKnown: true,
			})
		}
	}
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		// This pinned PostgreSQL driver's GetIndexes excludes constraint-backed
		// indexes and does not order composite columns. Read the real catalog
		// with ordinality so PK/UNIQUE constraints cannot disappear unnoticed.
		var catalog []struct {
			Name      string
			Columns   string
			IsUnique  bool
			IsPrimary bool
		}
		if err := model.DB.Raw(`
SELECT index_class.relname AS name, index_meta.indisunique AS is_unique,
       index_meta.indisprimary AS is_primary,
       string_agg(attribute_meta.attname, ',' ORDER BY key_column.ordinality) AS columns
FROM pg_catalog.pg_index AS index_meta
JOIN pg_catalog.pg_class AS table_class ON table_class.oid = index_meta.indrelid
JOIN pg_catalog.pg_namespace AS namespace_meta ON namespace_meta.oid = table_class.relnamespace
JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_meta.indexrelid
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS key_column(attnum, ordinality)
JOIN pg_catalog.pg_attribute AS attribute_meta
  ON attribute_meta.attrelid = table_class.oid AND attribute_meta.attnum = key_column.attnum
WHERE namespace_meta.nspname = current_schema() AND table_class.relname = ?
GROUP BY index_class.relname, index_meta.indisunique, index_meta.indisprimary
ORDER BY index_class.relname`, table).Scan(&catalog).Error; err != nil {
			fail("inspect PostgreSQL %s constraint/index catalog: %v", table, err)
		}
		result.Indexes = nil
		for _, index := range catalog {
			result.Indexes = append(result.Indexes, migrationIndexSnapshot{
				Name: index.Name, Columns: strings.Split(index.Columns, ","), Unique: index.IsUnique, UniqueKnown: true,
				Primary: index.IsPrimary, PrimaryKnown: true,
			})
		}
	}
	slices.SortFunc(result.Indexes, func(a, b migrationIndexSnapshot) int { return strings.Compare(a.Name, b.Name) })

	// Read raw stored columns, including optional future fields. String/NULL
	// cells avoid float64 ID/quota loss when a JSON manifest crosses binaries.
	rows, err := model.DB.Table(table).Order("id").Rows()
	if err != nil {
		fail("read %s rows: %v", table, err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		fail("read %s row columns: %v", table, err)
	}
	for rows.Next() {
		values := make([]sql.NullString, len(names))
		targets := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			fail("scan %s row: %v", table, err)
		}
		row := make(map[string]*string, len(names))
		for i, value := range values {
			if value.Valid {
				row[names[i]] = &value.String
			} else {
				row[names[i]] = nil
			}
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		fail("finish reading %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		fail("close %s snapshot rows: %v", table, err)
	}
	return result
}

func assertMigrationTable(expected, actual migrationTableSnapshot, mappingID int64) {
	if expected.Table != actual.Table || !slices.Equal(expected.PrimaryKeys, actual.PrimaryKeys) || len(expected.Rows) != len(actual.Rows) {
		fail("%s rows or primary key changed across startup", expected.Table)
	}
	for _, index := range expected.Indexes {
		if !slices.ContainsFunc(actual.Indexes, func(candidate migrationIndexSnapshot) bool { return reflect.DeepEqual(index, candidate) }) {
			fail("%s lost or changed index %s", expected.Table, index.Name)
		}
	}
	for _, column := range expected.Columns {
		if !slices.Contains(actual.Columns, column) {
			fail("%s lost column %s", expected.Table, column)
		}
	}
	for i, row := range expected.Rows {
		for _, column := range expected.Columns {
			want := row[column]
			if expected.Table == "channel_key_entries" && column == "model_mapping" && mappingID > 0 &&
				row["id"] != nil && *row["id"] == strconv.FormatInt(mappingID, 10) {
				mapping := allocationLongMapping()
				want = &mapping
			}
			if !reflect.DeepEqual(want, actual.Rows[i][column]) {
				fail("%s row %d column %s changed across startup", expected.Table, i, column)
			}
		}
	}
}

func allocationLongMapping() string {
	// Both names stay within the production 256-byte limit. Their exact case
	// and the complete object deliberately require more than VARCHAR(255).
	return `{"alias":"Key-Upstream","` + strings.Repeat("s", 200) + `":"` + strings.Repeat("T", 200) + `"}`
}

func verifyAllocationMigration() {
	encoded, err := os.ReadFile(allocationManifestPath())
	if err != nil {
		fail("read released allocation manifest: %v", err)
	}
	var manifest allocationMigrationManifest
	if err := common.Unmarshal(encoded, &manifest); err != nil {
		fail("decode released allocation manifest: %v", err)
	}
	if manifest.Version != 1 || manifest.Database != string(common.MainDatabaseType()) || manifest.ChannelID <= 0 || manifest.MappingEntryID <= 0 || len(manifest.Released) < 8 {
		fail("invalid allocation migration manifest identity")
	}
	if manifest.Stage != "seeded" && manifest.Stage != "first_verified" && manifest.Stage != "second_verified" {
		fail("invalid allocation migration stage %q", manifest.Stage)
	}
	mappingID := int64(0)
	if manifest.Stage != "seeded" {
		mappingID = manifest.MappingEntryID
		if len(manifest.AfterFirst) != len(manifest.Released) {
			fail("first verification snapshot is incomplete")
		}
	}
	for _, released := range manifest.Released {
		assertMigrationTable(released, readMigrationTable(released.Table), mappingID)
	}
	columns, err := model.DB.Migrator().ColumnTypes("channel_key_entries")
	if err != nil {
		fail("inspect current allocation mapping: %v", err)
	}
	found := false
	for _, column := range columns {
		if column.Name() != "model_mapping" {
			continue
		}
		found = true
		nullable, known := column.Nullable()
		if !known || !nullable || !strings.EqualFold(column.DatabaseTypeName(), "text") {
			fail("allocation model_mapping must be nullable TEXT; type=%s nullable=%t known=%t", column.DatabaseTypeName(), nullable, known)
		}
	}
	if !found {
		fail("current startup did not add allocation model_mapping")
	}
	assertAllocationMappings(manifest, mappingID != 0)
	if manifest.Stage == "seeded" {
		beforeMapping := readMigrationTable("channel_key_entries")
		// Table maps update exactly one column and cannot accidentally rewrite
		// a current struct's default fields, protocol, revision or timestamps.
		result := model.DB.Table("channel_key_entries").Where("id = ? AND channel_id = ?", manifest.MappingEntryID, manifest.ChannelID).
			Updates(map[string]any{"model_mapping": allocationLongMapping()})
		if result.Error != nil || result.RowsAffected != 1 {
			fail("write long allocation mapping: rows=%d error=%v", result.RowsAffected, result.Error)
		}
		assertMigrationTable(beforeMapping, readMigrationTable("channel_key_entries"), manifest.MappingEntryID)
		assertAllocationMappings(manifest, true)
		for _, released := range manifest.Released {
			current := readMigrationTable(released.Table)
			assertMigrationTable(released, current, manifest.MappingEntryID)
			assertMigrationConstraints(current)
			assertMigrationTable(current, readMigrationTable(current.Table), 0)
			manifest.AfterFirst = append(manifest.AfterFirst, current)
		}
		manifest.Stage = "first_verified"
	} else {
		for _, first := range manifest.AfterFirst {
			current := readMigrationTable(first.Table)
			if !slices.Equal(first.Columns, current.Columns) || !reflect.DeepEqual(first.Indexes, current.Indexes) {
				fail("%s schema changed on the second current startup", first.Table)
			}
			assertMigrationTable(first, current, 0)
			assertMigrationConstraints(current)
			assertMigrationTable(current, readMigrationTable(current.Table), 0)
		}
		manifest.Stage = "second_verified"
	}
	writeAllocationManifest(manifest)
	fmt.Printf("verified allocation migration stage=%s nullable_TEXT=true mapping_bytes=%d tables=%d\n", manifest.Stage, len(allocationLongMapping()), len(manifest.Released))
}

func assertAllocationMappings(manifest allocationMigrationManifest, saved bool) {
	channel, err := model.GetChannelById(manifest.ChannelID, true)
	if err != nil {
		fail("read current allocation projection: %v", err)
	}
	reader, ok := any(channel).(allocationMappingReader)
	if !ok {
		fail("current source does not implement per-Key mapping inheritance")
	}
	var inherited map[string]string
	if err := common.UnmarshalJsonStr(channel.GetModelMapping(), &inherited); err != nil || len(inherited) != 2 {
		fail("channel mapping baseline is invalid: %v", err)
	}
	var entries []struct {
		ID           int64
		Position     int
		RetiredAt    int64
		ModelMapping sql.NullString
	}
	if err := model.DB.Table("channel_key_entries").Where("channel_id = ?", manifest.ChannelID).Order("position, id").Find(&entries).Error; err != nil || len(entries) != 4 {
		fail("read inherited allocation mappings: count=%d error=%v", len(entries), err)
	}
	seenTarget := false
	for _, entry := range entries {
		want := ""
		if entry.ID == manifest.MappingEntryID {
			seenTarget = true
			if saved {
				want = allocationLongMapping()
			}
		}
		if entry.ModelMapping.String != want || (want != "" && !entry.ModelMapping.Valid) {
			fail("allocation %d mapping did not preserve exact inheritance/override", entry.ID)
		}
		if entry.RetiredAt != 0 {
			continue
		}
		expected := maps.Clone(inherited)
		if want != "" {
			var override map[string]string
			if err := common.UnmarshalJsonStr(want, &override); err != nil {
				fail("decode expected allocation mapping: %v", err)
			}
			maps.Copy(expected, override)
		}
		effective, err := reader.GetModelMappingForKey(entry.Position)
		if err != nil {
			fail("resolve allocation %d inheritance: %v", entry.ID, err)
		}
		var actual map[string]string
		if err := common.UnmarshalJsonStr(effective, &actual); err != nil || !maps.Equal(expected, actual) {
			fail("allocation %d did not inherit unaffected channel rules: %v", entry.ID, err)
		}
	}
	if !seenTarget {
		fail("stable mapping allocation disappeared")
	}
}

func assertMigrationConstraints(snapshot migrationTableSnapshot) {
	if len(snapshot.Rows) == 0 {
		fail("%s has no seeded migration rows", snapshot.Table)
	}
	constraints := []migrationIndexSnapshot{{Columns: snapshot.PrimaryKeys, Primary: true, Unique: true, UniqueKnown: true}}
	for _, index := range snapshot.Indexes {
		if index.PrimaryKnown && index.Primary {
			constraints[0] = index
		} else if index.UniqueKnown && index.Unique && !slices.ContainsFunc(constraints, func(existing migrationIndexSnapshot) bool {
			return slices.Equal(existing.Columns, index.Columns)
		}) {
			constraints = append(constraints, index)
		}
	}
	for i, constraint := range constraints {
		var source map[string]*string
		for _, row := range snapshot.Rows {
			if !slices.ContainsFunc(constraint.Columns, func(column string) bool { return row[column] == nil }) {
				source = row
				break
			}
		}
		if source == nil {
			// SQL unique indexes permit multiple NULLs. The schema snapshot
			// still checks such indexes; there is no non-NULL identity to copy.
			continue
		}
		duplicate := make(map[string]any, len(source))
		for column, value := range source {
			if value == nil {
				duplicate[column] = nil
			} else {
				duplicate[column] = *value
			}
		}
		if !constraint.Primary {
			for _, column := range snapshot.PrimaryKeys {
				delete(duplicate, column)
			}
		}
		// Isolate this constraint from other unique fields, so rejection by
		// a different index cannot hide a lost uniqueness guarantee.
		for j, other := range constraints {
			if i == j || other.Primary {
				continue
			}
			for _, column := range other.Columns {
				if slices.Contains(constraint.Columns, column) {
					continue
				}
				value := source[column]
				if value != nil {
					if number, err := strconv.ParseInt(*value, 10, 64); err == nil && number >= 0 && number < 1_000_000 {
						duplicate[column] = number + 1_000_000
					} else {
						duplicate[column] = fmt.Sprintf("probe_%d_%d", i, j)
					}
				}
				break
			}
		}
		accepted := fmt.Errorf("unexpected duplicate accepted")
		err := model.DB.Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
			if err := tx.Table(snapshot.Table).Create(&duplicate).Error; err != nil {
				return err
			}
			return accepted // Roll back even a broken uniqueness constraint.
		})
		if err == nil || err == accepted {
			fail("%s accepted duplicate constraint %s (%v)", snapshot.Table, constraint.Name, constraint.Columns)
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "unique constraint failed") && !strings.Contains(message, "duplicate entry") && !strings.Contains(message, "duplicate key value") {
			fail("%s duplicate check failed for a non-uniqueness reason: %v", snapshot.Table, err)
		}
		if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			for _, column := range constraint.Columns {
				if !strings.Contains(message, strings.ToLower(snapshot.Table+"."+column)) {
					fail("%s duplicate was rejected by a different constraint: %v", snapshot.Table, err)
				}
			}
		} else if !slices.ContainsFunc(snapshot.Indexes, func(index migrationIndexSnapshot) bool {
			// Equivalent unique indexes may coexist in an imported baseline;
			// engines may report either name for the same protected identity.
			return index.UniqueKnown && index.Unique && slices.Equal(index.Columns, constraint.Columns) &&
				index.Name != "" && strings.Contains(message, strings.ToLower(index.Name))
		}) {
			fail("%s duplicate did not exercise constraint %s: %v", snapshot.Table, constraint.Name, err)
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
