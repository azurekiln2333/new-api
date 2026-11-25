package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	if err := model.InitDB(); err != nil {
		fail("initialize database: %v", err)
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
		seedReleasedRows()
		return
	}
	verifyCurrentMigration()
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

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
