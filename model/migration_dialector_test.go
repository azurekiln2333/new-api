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

type migrationChannelKeyEntryV1 struct {
	Id             int64  `gorm:"primaryKey;autoIncrement"`
	ChannelId      int    `gorm:"not null;index:idx_channel_key_entries_order,priority:1"`
	Position       int    `gorm:"not null;index:idx_channel_key_entries_order,priority:2"`
	Key            string `gorm:"type:text;not null"`
	CredentialRef  string `gorm:"type:varchar(255);not null;default:''"`
	BoundUserId    int    `gorm:"not null;default:0"`
	ScopeMode      string `gorm:"type:varchar(16);not null;default:'all'"`
	Models         string `gorm:"type:text;not null"`
	Status         int    `gorm:"not null;default:1"`
	DisabledReason string `gorm:"type:text"`
	DisabledTime   int64  `gorm:"not null;default:0"`
	ConfigRevision int64  `gorm:"not null;default:1"`
	RetiredAt      int64  `gorm:"not null;default:0"`
	MergedIntoId   int64  `gorm:"not null;default:0"`
	CreatedTime    int64  `gorm:"not null;default:0"`
	UpdatedTime    int64  `gorm:"not null;default:0"`
}

func (migrationChannelKeyEntryV1) TableName() string { return "channel_key_entries" }

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
			var version string
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("engine=%s version=%s", dialect, version)

			t.Run("channel_key_model_mapping", func(t *testing.T) {
				for _, mode := range []string{"fresh", "legacy_upgrade"} {
					t.Run(mode, func(t *testing.T) {
						require.NoError(t, db.Migrator().DropTable(&ChannelKeyEntry{}))
						t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&ChannelKeyEntry{})) })
						rows := []ChannelKeyEntry{
							{ChannelId: 17, Position: 0, Key: "same-key", ScopeMode: "all", Models: "[]", Status: common.ChannelStatusEnabled, ConfigRevision: 3, CreatedTime: 11, UpdatedTime: 12},
							{ChannelId: 17, Position: 1, Key: "same-key", BoundUserId: 41, ScopeMode: "allowlist", Models: "[]", Status: common.ChannelStatusManuallyDisabled, DisabledReason: "manual", DisabledTime: 13, ConfigRevision: 7, CreatedTime: 10, UpdatedTime: 14},
							{ChannelId: 17, Position: 2, Key: "retired-key", CredentialRef: "vendor:account", ScopeMode: "allowlist", Models: `["DeepSeek-R1"]`, Status: common.ChannelStatusAutoDisabled, ConfigRevision: 9, RetiredAt: 15, CreatedTime: 9, UpdatedTime: 15},
						}
						var legacyIndexes map[string]codeOperationIndex
						if mode == "legacy_upgrade" {
							// Reconstruct the pre-column allocation shape, not a fresh
							// current table. The hosted released-binary probe remains a
							// separate requirement for full startup compatibility.
							require.NoError(t, db.AutoMigrate(&migrationChannelKeyEntryV1{}))
							require.False(t, db.Migrator().HasColumn(&ChannelKeyEntry{}, "model_mapping"))
							require.NoError(t, db.Omit("ModelMapping").Create(&rows).Error)
							legacyIndexes = codeOperationIndexSnapshot(t, db, &migrationChannelKeyEntryV1{})
						}
						for pass := range 2 {
							recorder.reset()
							require.NoError(t, db.AutoMigrate(&ChannelKeyEntry{}))
							if pass == 1 {
								assert.Empty(t, recorder.schemaMutations(), "the second migration must not repeat DDL")
							}
							require.True(t, db.Migrator().HasColumn(&ChannelKeyEntry{}, "model_mapping"))
							if pass == 0 {
								if mode == "fresh" {
									require.NoError(t, db.Create(&rows).Error)
								}
								var inherited []ChannelKeyEntry
								require.NoError(t, db.Order("position, id").Find(&inherited).Error)
								assert.Equal(t, rows, inherited, "an absent column upgrades to inheritance without changing allocations")
								// A payload wider than VARCHAR(255) proves the new column
								// stores the complete mapping and retains target casing.
								rows[1].ModelMapping = `{"` + strings.Repeat("s", 200) + `":"` + strings.Repeat("T", 200) + `"}`
								require.NoError(t, db.Model(&ChannelKeyEntry{}).Where("id = ?", rows[1].Id).Update("model_mapping", rows[1].ModelMapping).Error)
							}
							var saved []ChannelKeyEntry
							require.NoError(t, db.Order("position, id").Find(&saved).Error)
							assert.Equal(t, rows, saved, "mapping and all stable identities, scopes, revisions and history survive restart migration")
							if mode == "legacy_upgrade" {
								assert.Equal(t, legacyIndexes, codeOperationIndexSnapshot(t, db, &ChannelKeyEntry{}), "allocation indexes must survive the additive migration")
							}
						}
						err := db.Transaction(func(tx *gorm.DB) error { return tx.Create(&rows[0]).Error })
						assert.Error(t, err, "migration must retain primary-key uniqueness")
					})
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
