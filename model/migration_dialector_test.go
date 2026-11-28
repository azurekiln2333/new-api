package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MigrationIdentityFields struct {
	ID        int    `gorm:"primaryKey"`
	Name      string `gorm:"size:64;unique"`
	Reference string `gorm:"size:64;uniqueIndex"`
	Provider  string `gorm:"size:32;uniqueIndex:,composite:provider_subject"`
	Subject   string `gorm:"size:64;uniqueIndex:,composite:provider_subject"`
}

type migrationIdentityV1 struct {
	MigrationIdentityFields
	Digest string `gorm:"type:char(32)"`
}

type migrationIdentityV2 struct {
	MigrationIdentityFields
	Digest string `gorm:"type:char(64)"`
	Note   string `gorm:"size:128"`
}

type migrationConstraintV1 struct {
	ID   int    `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

type migrationConstraintV2 struct {
	ID   int    `gorm:"primaryKey"`
	Name string `gorm:"size:64;unique"`
}

type migrationDecimalV1 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(10,6);default:0"`
}

type migrationDecimalV2 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(12,6);not null;default:0"`
}

type migrationDecimalV3 struct {
	ID    int     `gorm:"primaryKey"`
	Price float64 `gorm:"type:decimal(12,6);not null;default:1.25"`
}

// The pre-namespace hourly schema may already contain aggregated facts. The
// additive migration cannot infer their source membership or reset apply state.
type migrationUsageHourWithoutNamespace struct {
	ID                   int64  `gorm:"primaryKey;autoIncrement;index:ix_usage_hour_time,priority:3;index:ix_usage_hour_self,priority:4"`
	ProjectionVersion    int64  `gorm:"type:bigint;not null;uniqueIndex:ux_usage_hour_key,priority:1;index:ix_usage_hour_time,priority:1;index:ix_usage_hour_self,priority:2"`
	DimensionKey         string `gorm:"type:char(64);not null;uniqueIndex:ux_usage_hour_key,priority:2"`
	CollectionEpoch      string `gorm:"type:char(32);not null"`
	HourStart            int64  `gorm:"type:bigint;not null;index:ix_usage_hour_time,priority:2;index:ix_usage_hour_self,priority:3"`
	UserID               int64  `gorm:"type:bigint;not null;index:ix_usage_hour_self,priority:1"`
	Username             string `gorm:"type:text;not null"`
	ModelName            string `gorm:"type:text;not null"`
	GroupName            string `gorm:"type:text;not null"`
	TokenID              int64  `gorm:"type:bigint;not null"`
	ChannelID            int64  `gorm:"type:bigint;not null"`
	ExportNode           string `gorm:"type:text;not null"`
	DimensionKnownMask   int64  `gorm:"type:bigint;not null"`
	ConsumeCount         int64  `gorm:"type:bigint;not null"`
	GrossQuota           int64  `gorm:"type:bigint;not null"`
	PromptTokens         int64  `gorm:"type:bigint;not null"`
	CompletionTokens     int64  `gorm:"type:bigint;not null"`
	RankingTokens        int64  `gorm:"type:bigint;not null"`
	CacheReadTokens      int64  `gorm:"type:bigint;not null"`
	CacheWriteTokens     int64  `gorm:"type:bigint;not null"`
	InputDenominator     int64  `gorm:"type:bigint;not null"`
	CacheUnknownCount    int64  `gorm:"type:bigint;not null"`
	SemanticUnknownCount int64  `gorm:"type:bigint;not null"`
}

func TestMigrationSchemaStability(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dsn string
			switch dialect {
			case "sqlite":
				dsn = "local"
				previousPath := common.SQLitePath
				common.SQLitePath = filepath.Join(t.TempDir(), "migration.db")
				t.Cleanup(func() { common.SQLitePath = previousPath })
			case "mysql":
				dsn = os.Getenv("TEST_MYSQL_DSN")
			case "postgres":
				dsn = os.Getenv("TEST_POSTGRES_DSN")
			}
			if dsn == "" {
				t.Skip("test database DSN is not configured")
			}
			t.Setenv("MIGRATION_TEST_DSN", dsn)
			db, _, err := chooseDB("MIGRATION_TEST_DSN", false)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			recorder := &migrationSQLRecorder{}
			db = db.Session(&gorm.Session{Logger: recorder})

			t.Run("usage_statistics_foundation", func(t *testing.T) {
				tables := []any{&UsageEvent{}, &UsageHour{}, &UsageChannelModel{}, &UsageChannel{}}
				require.NoError(t, db.Migrator().DropTable(tables...))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })

				// These existing-domain fixtures are deliberately separate from
				// usage_events. The actual released-binary upgrade is hosted by
				// the release probe, not simulated by these current Go structs.
				const logTable, quotaTable = "logs", "quota_data"
				require.NoError(t, db.Table(logTable).AutoMigrate(&Log{}))
				require.NoError(t, db.Table(quotaTable).AutoMigrate(&QuotaData{}))
				oldLog := Log{UserId: 81230, Type: LogTypeConsume, CreatedAt: 1700000123, ModelName: "historic-model", Quota: 7, PromptTokens: 100, CompletionTokens: 20}
				oldQuota := QuotaData{UserID: 81230, CreatedAt: 1699999200, ModelName: "historic-model", Count: 1, Quota: 7, TokenUsed: 120}
				require.NoError(t, db.Table(logTable).Create(&oldLog).Error)
				t.Cleanup(func() { require.NoError(t, db.Table(logTable).Where("id = ?", oldLog.Id).Delete(&Log{}).Error) })
				require.NoError(t, db.Table(quotaTable).Create(&oldQuota).Error)
				t.Cleanup(func() { require.NoError(t, db.Table(quotaTable).Where("id = ?", oldQuota.Id).Delete(&QuotaData{}).Error) })
				require.NoError(t, db.Table("usage_hours").AutoMigrate(&migrationUsageHourWithoutNamespace{}))
				oldHour := migrationUsageHourWithoutNamespace{
					ProjectionVersion: UsageEventProjectionVersion, DimensionKey: strings.Repeat("e", 64), CollectionEpoch: strings.Repeat("b", 32),
					HourStart: oldQuota.CreatedAt, UserID: int64(oldQuota.UserID), Username: "legacy-owner", ModelName: oldQuota.ModelName,
					ConsumeCount: 1, GrossQuota: 7, PromptTokens: 100, CompletionTokens: 20, RankingTokens: 120, InputDenominator: 100,
				}
				require.NoError(t, db.Table("usage_hours").Create(&oldHour).Error)
				require.NoError(t, MigrateUsageStatistics(db))
				for range 2 {
					recorder.reset()
					require.NoError(t, MigrateUsageStatistics(db))
					assert.Empty(t, recorder.schemaMutations(), "repeated foundation migration is a schema no-op")
				}
				var savedLog Log
				var savedQuota QuotaData
				require.NoError(t, db.Table(logTable).First(&savedLog, oldLog.Id).Error)
				require.NoError(t, db.Table(quotaTable).First(&savedQuota, oldQuota.Id).Error)
				assert.Equal(t, oldLog, savedLog)
				assert.Equal(t, oldQuota, savedQuota)
				var savedHour migrationUsageHourWithoutNamespace
				require.NoError(t, db.Table("usage_hours").First(&savedHour, oldHour.ID).Error)
				assert.Equal(t, oldHour, savedHour, "namespace migration preserves every preexisting counter and dimension")
				var migratedHour UsageHour
				require.NoError(t, db.First(&migratedHour, oldHour.ID).Error)
				assert.Empty(t, normalizeFixedText(migratedHour.SourceNamespace), "mixed old hours must remain unattributed, never assigned to the live codec")

				for _, table := range tables {
					assert.True(t, db.Migrator().HasTable(table))
				}
				for _, indexTest := range []struct {
					model   any
					name    string
					columns []string
					unique  bool
				}{
					{&UsageEvent{}, "ux_usage_event_key", []string{"event_key"}, true},
					{&UsageEvent{}, "ix_usage_event_drain", []string{"source_state", "applied_version", "next_attempt_at", "id"}, false},
					{&UsageEvent{}, "ix_usage_event_pending_time", []string{"source_state", "applied_version", "kind", "occurred_at", "id"}, false},
					{&UsageEvent{}, "ix_usage_event_time", []string{"source_state", "kind", "occurred_at", "id"}, false},
					{&UsageEvent{}, "ix_usage_event_self", []string{"user_id", "source_state", "kind", "occurred_at", "id"}, false},
					{&UsageEvent{}, "ix_usage_event_channel_tail", []string{"channel_id", "source_state", "applied_version", "kind", "id"}, false},
					{&UsageHour{}, "ux_usage_hour_key", []string{"projection_version", "dimension_key"}, true},
					{&UsageHour{}, "ix_usage_hour_self", []string{"user_id", "projection_version", "hour_start", "id"}, false},
					{&UsageHour{}, "ix_usage_hour_lineage", []string{"source_namespace", "collection_epoch", "projection_version", "hour_start", "id"}, false},
					{&UsageHour{}, "ix_usage_hour_lineage_self", []string{"source_namespace", "collection_epoch", "user_id", "projection_version", "hour_start", "id"}, false},
					{&UsageChannelModel{}, "ux_usage_channel_model", []string{"projection_version", "channel_id", "model_key"}, true},
				} {
					indexes, err := db.Migrator().GetIndexes(indexTest.model)
					require.NoError(t, err)
					var matched gorm.Index
					for _, index := range indexes {
						if index.Name() == indexTest.name {
							matched = index
							break
						}
					}
					require.NotNil(t, matched, indexTest.name)
					columns := matched.Columns()
					if dialect == "postgres" {
						// The installed PostgreSQL GetIndexes query does not retain
						// key order; read the actual ordinal positions instead.
						require.NoError(t, db.Raw(`
SELECT attribute.attname
FROM pg_catalog.pg_index AS index_meta
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS index_column(attnum, ordinal_position)
JOIN pg_catalog.pg_attribute AS attribute
  ON attribute.attrelid = index_meta.indrelid AND attribute.attnum = index_column.attnum
WHERE index_meta.indexrelid = to_regclass(?)
ORDER BY index_column.ordinal_position
`, indexTest.name).Scan(&columns).Error)
					}
					assert.Equal(t, indexTest.columns, columns, indexTest.name)
					unique, known := matched.Unique()
					require.True(t, known, indexTest.name)
					assert.Equal(t, indexTest.unique, unique, indexTest.name)
				}
			})

			t.Run("identity_and_indexes", func(t *testing.T) {
				const table = "migration_identity_test"
				t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV1{}))
				row := migrationIdentityV1{
					MigrationIdentityFields: MigrationIdentityFields{ID: 1, Name: "root", Reference: "token-reference", Provider: "oidc", Subject: "subject"},
					Digest:                  "old-digest",
				}
				require.NoError(t, db.Table(table).Create(&row).Error)
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV1{}))
				assert.Empty(t, recorder.schemaMutations())

				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV2{}))
				columns, err := db.Table(table).Migrator().ColumnTypes(&migrationIdentityV2{})
				require.NoError(t, err)
				for _, column := range columns {
					if column.Name() == "digest" {
						length, ok := column.Length()
						require.True(t, ok)
						assert.EqualValues(t, 64, length)
					}
				}
				assert.True(t, db.Table(table).Migrator().HasColumn(&migrationIdentityV2{}, "note"))
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationIdentityV2{}))
				assert.Empty(t, recorder.schemaMutations())
				var saved migrationIdentityV2
				require.NoError(t, db.Table(table).First(&saved, 1).Error)
				assert.Equal(t, row.MigrationIdentityFields, saved.MigrationIdentityFields)
				expectedDigest := row.Digest
				if dialect == "postgres" {
					expectedDigest += strings.Repeat(" ", 64-len(row.Digest))
				}
				assert.Equal(t, expectedDigest, saved.Digest)
				for _, duplicate := range []migrationIdentityV2{
					{MigrationIdentityFields: MigrationIdentityFields{Name: "root", Reference: "other-1", Provider: "other", Subject: "1"}},
					{MigrationIdentityFields: MigrationIdentityFields{Name: "other-2", Reference: "token-reference", Provider: "other", Subject: "2"}},
					{MigrationIdentityFields: MigrationIdentityFields{Name: "other-3", Reference: "other-3", Provider: "oidc", Subject: "subject"}},
				} {
					assert.Error(t, db.Table(table).Create(&duplicate).Error)
				}
			})

			t.Run("unique_constraint_changes", func(t *testing.T) {
				const table = "migration_constraint_test"
				t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV1{}))
				require.NoError(t, db.Table(table).Create(&migrationConstraintV1{Name: "existing"}).Error)
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV2{}))
				assert.Error(t, db.Table(table).Create(&migrationConstraintV2{Name: "existing"}).Error)
				recorder.reset()
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV2{}))
				assert.Empty(t, recorder.schemaMutations())
				require.NoError(t, db.Table(table).AutoMigrate(&migrationConstraintV1{}))
				require.NoError(t, db.Table(table).Create(&migrationConstraintV1{Name: "existing"}).Error)
			})

			if dialect == "postgres" {
				t.Run("renamed_unique_constraints", func(t *testing.T) {
					const table = "migration_renamed_unique"
					t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
					tableDB := db.Table(table).Session(&gorm.Session{})
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					original := migrationConstraintV1{Name: "existing"}
					require.NoError(t, tableDB.Create(&original).Error)
					for _, name := range []string{"models_model_name_key", `imported "model" name`} {
						require.NoError(t, db.Exec("ALTER TABLE ? ADD CONSTRAINT ? UNIQUE (name)", clause.Table{Name: table}, clause.Column{Name: name}).Error)
					}
					require.NoError(t, db.Exec("ALTER TABLE ? ADD CONSTRAINT keep_composite UNIQUE (id, name)", clause.Table{Name: table}).Error)
					require.NoError(t, db.Exec("CREATE INDEX keep_name_lookup ON ? (name)", clause.Table{Name: table}).Error)
					// A unique field must keep the old names and still reject duplicates.
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV2{}))
					require.Error(t, tableDB.Create(&migrationConstraintV2{Name: "existing"}).Error)
					require.NoError(t, db.Exec("CREATE TABLE migration_unique_reference (name varchar(64) REFERENCES migration_renamed_unique(name))").Error)
					t.Cleanup(func() { _ = db.Migrator().DropTable("migration_unique_reference") })
					require.NoError(t, db.Exec("INSERT INTO migration_unique_reference (name) VALUES (?)", "existing").Error)
					require.Error(t, tableDB.AutoMigrate(&migrationConstraintV1{}), "dependent foreign keys must not be cascaded away")
					for _, name := range []string{"models_model_name_key", `imported "model" name`} {
						assert.True(t, tableDB.Migrator().HasConstraint(&migrationConstraintV1{}, name), "all old constraints must survive rollback")
					}
					var references int64
					require.NoError(t, db.Table("migration_unique_reference").Count(&references).Error)
					assert.EqualValues(t, 1, references)
					require.NoError(t, db.Migrator().DropTable("migration_unique_reference"))
					// Removing column uniqueness must resolve both actual constraint names.
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					recorder.reset()
					require.NoError(t, tableDB.AutoMigrate(&migrationConstraintV1{}))
					assert.Empty(t, recorder.schemaMutations())
					require.NoError(t, tableDB.Create(&migrationConstraintV1{Name: "existing"}).Error)
					assert.True(t, tableDB.Migrator().HasConstraint(&migrationConstraintV1{}, "keep_composite"))
					assert.True(t, tableDB.Migrator().HasIndex(&migrationConstraintV1{}, "keep_name_lookup"))
					var rows []migrationConstraintV1
					require.NoError(t, tableDB.Order("id").Find(&rows).Error)
					require.Len(t, rows, 2)
					assert.Equal(t, original, rows[0])
				})
			}

			if dialect == "mysql" {
				t.Run("decimal_default_and_real_changes", func(t *testing.T) {
					const table = "migration_decimal_test"
					t.Cleanup(func() { _ = db.Migrator().DropTable(table) })
					require.NoError(t, db.Table(table).AutoMigrate(&migrationDecimalV1{}))
					require.NoError(t, db.Table(table).Create(&migrationDecimalV1{ID: 1, Price: 12.345678}).Error)
					for _, target := range []any{&migrationDecimalV1{}, &migrationDecimalV2{}, &migrationDecimalV3{}} {
						require.NoError(t, db.Table(table).AutoMigrate(target))
						recorder.reset()
						require.NoError(t, db.Table(table).AutoMigrate(target))
						assert.Empty(t, recorder.schemaMutations())
					}
					columns, err := db.Table(table).Migrator().ColumnTypes(&migrationDecimalV3{})
					require.NoError(t, err)
					for _, column := range columns {
						if column.Name() == "price" {
							precision, scale, ok := column.DecimalSize()
							require.True(t, ok)
							assert.EqualValues(t, 12, precision)
							assert.EqualValues(t, 6, scale)
							nullable, ok := column.Nullable()
							require.True(t, ok)
							assert.False(t, nullable)
						}
					}
					require.NoError(t, db.Table(table).Create(&map[string]any{"id": 2}).Error)
					var prices []float64
					require.NoError(t, db.Table(table).Order("id").Pluck("price", &prices).Error)
					assert.Equal(t, []float64{12.345678, 1.25}, prices)
				})
			}
		})
	}
}
