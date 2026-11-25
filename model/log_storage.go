package model

import (
	"context"
	"database/sql"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// LogStorageTable reports metadata only. Nil means unavailable, not an empty
// table. In particular SQLite without dbstat must not scan payload rows.
type LogStorageTable struct {
	Name      string `json:"name"`
	Database  string `json:"database"`
	Bytes     *int64 `json:"bytes"`
	Estimated bool   `json:"estimated"`
}

func GetLogStorageTables(ctx context.Context) []LogStorageTable {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tables := []LogStorageTable{
		{Name: "logs", Database: string(common.LogDatabaseType())},
		{Name: "log_request_details", Database: string(common.LogDatabaseType())},
		{Name: "log_request_content_protections", Database: string(common.MainDatabaseType())},
	}
	for i := range tables {
		db := LOG_DB
		using := common.UsingLogDatabase
		if i == 2 {
			db = DB
			using = common.UsingMainDatabase
		}
		if db == nil || ctx.Err() != nil {
			continue
		}
		var query string
		switch {
		case using(common.DatabaseTypePostgreSQL):
			query = "SELECT pg_total_relation_size(to_regclass(?)) AS bytes"
		case using(common.DatabaseTypeMySQL):
			query = "SELECT data_length + index_length AS bytes FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
			tables[i].Estimated = true
		case using(common.DatabaseTypeClickHouse):
			query = "SELECT sum(bytes_on_disk) AS bytes FROM system.parts WHERE database = currentDatabase() AND table = ? AND active GROUP BY table"
		default:
			// SQLite page_count measures the whole file, not either table.
			continue
		}
		var row struct{ Bytes sql.NullInt64 }
		if err := db.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx).Raw(query, tables[i].Name).Scan(&row).Error; err == nil && row.Bytes.Valid && row.Bytes.Int64 >= 0 {
			value := row.Bytes.Int64
			tables[i].Bytes = &value
		}
	}
	return tables
}
