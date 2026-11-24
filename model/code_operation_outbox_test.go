package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Every test installs and restores its own fixture database. MySQL and
// PostgreSQL require the existing CI DSNs; SQLite uses a temporary file.

const codeOperationTestPrefix = "code_operation_test_"

// openCodeOperationFixture opens an isolated primary store and points DB at it.
// Business tables use a prefix; fixed-name outbox/operation tables require a
// disposable database, and these fixtures must not run in parallel.
func openCodeOperationFixture(t *testing.T, dialect, prefix string) *gorm.DB {
	return openCodeOperationFixturePool(t, dialect, prefix, 1)
}

// Concurrency cases need more than one connection so the
// transactions truly overlap instead of serializing on a single connection.
func openCodeOperationFixturePool(t *testing.T, dialect, prefix string, connections int) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "code_operation.db"))
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		driver = postgres.Open(dsn)
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: prefix},
	})
	require.NoError(t, err)
	previousDatabaseType := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	initCol()
	t.Cleanup(func() {
		common.SetMainDatabaseType(previousDatabaseType)
		initCol()
	})
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(connections)
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		assert.NoError(t, sqlDB.Close())
	})
	return db
}

// requireCodeOperationSchema runs fresh or upgrade DDL twice and checks that the
// required business and outbox tables remain available afterward.
func requireCodeOperationSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for range 2 {
		require.NoError(t, db.AutoMigrate(&Redemption{}, &ManagementOperationBatch{}, &AuditEventOutbox{}))
	}
	require.True(t, db.Migrator().HasTable(&Redemption{}))
	require.True(t, db.Migrator().HasTable(&ManagementOperationBatch{}))
	require.True(t, db.Migrator().HasTable(&AuditEventOutbox{}))
}

type codeOperationIndex struct {
	Columns     []string
	Unique      bool
	UniqueKnown bool
}

type releasedRedemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:100"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"`
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"`
	BatchId      string         `json:"batch_id" gorm:"type:varchar(32);default:'';<-:create"`
	LimitPerUser bool           `json:"limit_per_user,omitempty" gorm:"-:all"`
}

func codeOperationIndexSnapshot(t *testing.T, db *gorm.DB, value any) map[string]codeOperationIndex {
	t.Helper()
	indexes, err := db.Migrator().GetIndexes(value)
	require.NoError(t, err)
	snapshot := make(map[string]codeOperationIndex, len(indexes))
	for _, index := range indexes {
		unique, known := index.Unique()
		snapshot[index.Name()] = codeOperationIndex{
			Columns:     index.Columns(),
			Unique:      unique,
			UniqueKnown: known,
		}
	}
	return snapshot
}

// releaseSchemaRedemptionFixture creates the existing redemption table and rows
// using the model shape from the pinned release. It represents the released
// schema, but does not boot the old binary or execute its migration code.
func releaseSchemaRedemptionFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	// Keep the pinned release's columns and tags, but use the production model
	// name so GORM applies this fixture database's TablePrefix exactly as it does
	// when the current Redemption model is migrated below.
	type Redemption releasedRedemption
	require.NoError(t, db.AutoMigrate(&Redemption{}))
	rows := []Redemption{
		{UserId: 7, Key: strings.Repeat("0", 31) + "1", Name: "legacy", Status: common.RedemptionCodeStatusEnabled, Quota: 250, CreatedTime: 1000},
		{UserId: 7, Key: strings.Repeat("0", 31) + "2", Name: "legacy", Status: common.RedemptionCodeStatusEnabled, Quota: 300, CreatedTime: 1001},
	}
	require.NoError(t, db.Create(&rows).Error)
}

func newRedemptionBatchRequest(operationId string, digest map[string]string) CodeBatchRequest {
	creationBatchId := ""
	if digest["limit_per_user"] == "true" {
		creationBatchId = "claim-batch"
	}
	return CodeBatchRequest{
		OperationId:     operationId,
		OperationType:   CodeOperationRedemption,
		RequestDigest:   CodeRequestDigest(CodeOperationRedemption, digest),
		ActorUserId:     41,
		ActorRole:       common.RoleAdminUser,
		CreationBatchId: creationBatchId,
		BuildRows: func() (any, []string, error) {
			keys := []string{common.GetUUID(), common.GetUUID()}
			rows := make([]Redemption, len(keys))
			for i, key := range keys {
				rows[i] = Redemption{
					UserId:      41,
					Key:         key,
					Name:        "promo",
					Status:      common.RedemptionCodeStatusEnabled,
					Quota:       500,
					CreatedTime: common.GetTimestamp(),
					BatchId:     creationBatchId,
				}
			}
			return rows, keys, nil
		},
		Audit: AuditEventDraft{
			EventId:     common.NewRequestId(),
			Category:    AuditCategoryOperation,
			Action:      "redemption.create",
			ActorUserId: 41,
		},
	}
}

// TestManagementMutationOutboxDatabaseMatrix is the consolidated gate: fresh and
// upgrade-shaped paths on every engine, plus ambiguous-code recovery.
func TestManagementMutationOutboxDatabaseMatrix(t *testing.T) {
	baseDigest := map[string]string{
		"name": "promo", "count": "2", "quota": "500", "expired_time": "0", "limit_per_user": "true",
	}
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, fresh := range []bool{true, false} {
				name := "upgrade-shaped"
				if fresh {
					name = "fresh"
				}
				t.Run(name, func(t *testing.T) {
					prefix := fmt.Sprintf("%s%s_%v_", codeOperationTestPrefix, dialect, fresh)
					db := openCodeOperationFixture(t, dialect, prefix)
					require.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Redemption{}))
					var releasedRedemptionIndexes map[string]codeOperationIndex
					if !fresh {
						releaseSchemaRedemptionFixture(t, db)
						releasedRedemptionIndexes = codeOperationIndexSnapshot(t, db, &Redemption{})
					}
					requireCodeOperationSchema(t, db)
					if !fresh {
						var legacy []Redemption
						require.NoError(t, db.Where("user_id = ?", 7).Order("created_time").Find(&legacy).Error)
						require.Len(t, legacy, 2, "migration preserves released baseline rows")
						assert.Equal(t, strings.Repeat("0", 31)+"1", legacy[0].Key)
						assert.Equal(t, strings.Repeat("0", 31)+"2", legacy[1].Key)
						assert.Equal(t, 250, legacy[0].Quota)
						assert.Equal(t, 300, legacy[1].Quota)
						assert.Equal(t, common.RedemptionCodeStatusEnabled, legacy[0].Status)
						assert.Equal(t, common.RedemptionCodeStatusEnabled, legacy[1].Status)
						assert.Equal(t, releasedRedemptionIndexes, codeOperationIndexSnapshot(t, db, &Redemption{}), "migration preserves released redemption indexes")
					}

					created, err := CreateCodeBatch(newRedemptionBatchRequest(common.GetUUID(), baseDigest))
					require.NoError(t, err)
					require.NotNil(t, created)
					assert.False(t, created.Replayed)
					require.Len(t, created.Keys, 2)

					var outboxCount int64
					require.NoError(t, db.Model(&AuditEventOutbox{}).Count(&outboxCount).Error)
					assert.Equal(t, int64(1), outboxCount, "the audit obligation commits with the mutation")

					// Lost response: the same identity returns the original keys.
					replayReq := newRedemptionBatchRequest(created.OperationId, baseDigest)
					replayed, err := CreateCodeBatch(replayReq)
					require.NoError(t, err)
					assert.True(t, replayed.Replayed)
					assert.Equal(t, created.Keys, replayed.Keys)
					var batchCount int64
					require.NoError(t, db.Model(&ManagementOperationBatch{}).Count(&batchCount).Error)
					assert.Equal(t, int64(1), batchCount, "a retry must not mint a second batch")
					var redemptions []Redemption
					require.NoError(t, db.Where("user_id = ?", 41).Find(&redemptions).Error)
					require.Len(t, redemptions, 2, "a retry must not mint more redemption codes")
					storedKeys := make([]string, len(redemptions))
					for i, row := range redemptions {
						storedKeys[i] = row.Key
						assert.Equal(t, 500, row.Quota)
						assert.Equal(t, common.RedemptionCodeStatusEnabled, row.Status)
						assert.Equal(t, created.CreationBatchId, row.BatchId)
					}
					assert.ElementsMatch(t, created.Keys, storedKeys)
					require.NoError(t, db.Model(&AuditEventOutbox{}).Count(&outboxCount).Error)
					assert.Equal(t, int64(1), outboxCount, "a replay must not create another audit obligation")

					var committedAudit AuditEventOutbox
					require.NoError(t, db.Where("action = ? AND actor_user_id = ?", "redemption.create", 41).First(&committedAudit).Error)
					operationDuplicateErr := db.Transaction(func(tx *gorm.DB) error {
						return tx.Create(&ManagementOperationBatch{
							OperationId:   created.OperationId,
							OperationType: CodeOperationRedemption,
							State:         codeOperationStateDone,
							RequestDigest: CodeRequestDigest(CodeOperationRedemption, baseDigest),
							ActorUserId:   41,
							ResultJson:    "{}",
							CreatedAt:     common.GetTimestamp(),
						}).Error
					})
					assert.Error(t, operationDuplicateErr, "operation identity remains unique after migration")
					outboxDuplicateErr := db.Transaction(func(tx *gorm.DB) error {
						return tx.Create(&AuditEventOutbox{EventId: committedAudit.EventId, Content: "", ParamsJson: ""}).Error
					})
					assert.Error(t, outboxDuplicateErr, "audit event identity remains unique after migration")
					redemptionDuplicateErr := db.Transaction(func(tx *gorm.DB) error {
						return tx.Create(&Redemption{
							UserId: 99, Key: created.Keys[0], Status: common.RedemptionCodeStatusEnabled, Quota: 100,
						}).Error
					})
					assert.Error(t, redemptionDuplicateErr, "existing redemption key uniqueness remains intact")

					otherActor := newRedemptionBatchRequest(created.OperationId, baseDigest)
					otherActor.ActorUserId = 42
					_, err = CreateCodeBatch(otherActor)
					assert.ErrorIs(t, err, ErrCodeBatchActorMismatch)
					wrongType := newRedemptionBatchRequest(created.OperationId, baseDigest)
					wrongType.OperationType = CodeOperationInvitation
					_, err = CreateCodeBatch(wrongType)
					assert.ErrorIs(t, err, ErrCodeBatchConflict)

					changedDigest := map[string]string{
						"name": "promo", "count": "9", "quota": "500", "expired_time": "0", "limit_per_user": "true",
					}
					_, err = CreateCodeBatch(newRedemptionBatchRequest(created.OperationId, changedDigest))
					assert.ErrorIs(t, err, ErrCodeBatchConflict)
					require.NoError(t, db.Model(&ManagementOperationBatch{}).Count(&batchCount).Error)
					assert.Equal(t, int64(1), batchCount, "rejected identities do not add batches")
					var redemptionCount int64
					require.NoError(t, db.Model(&Redemption{}).Where("user_id = ?", 41).Count(&redemptionCount).Error)
					assert.Equal(t, int64(2), redemptionCount, "rejected identities do not add codes")
					require.NoError(t, db.Model(&AuditEventOutbox{}).Count(&outboxCount).Error)
					assert.Equal(t, int64(1), outboxCount, "rejected identities do not add audit events")

					// A non-ASCII user agent at the rune boundary must survive the
					// business transaction, not fail it as invalid UTF-8.
					boundary := newRedemptionBatchRequest("", baseDigest)
					boundary.Audit.UserAgent = strings.Repeat("界", 600) + "tail"
					withUA, err := CreateCodeBatch(boundary)
					require.NoError(t, err, "a multi-byte user agent must not break the transaction")
					require.NotNil(t, withUA)
					var stored AuditEventOutbox
					require.NoError(t, db.Where("event_id = ?", boundary.Audit.EventId).First(&stored).Error)
					assert.LessOrEqual(t, len([]rune(stored.UserAgent)), 512)
					assert.True(t, utf8.ValidString(stored.UserAgent), "truncation must not split a multi-byte character")
				})
			}
		})
	}
}

// TestConcurrentCodeBatchSubmitCollapsesToOneBatch proves the claim-before-insert
// ordering: concurrent submissions of one identity converge on a single batch.
func TestConcurrentCodeBatchSubmitCollapsesToOneBatch(t *testing.T) {
	digest := map[string]string{"name": "promo", "count": "2", "quota": "500", "expired_time": "0", "limit_per_user": "true"}
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			prefix := fmt.Sprintf("%s%s_conc_", codeOperationTestPrefix, dialect)
			const workers = 2
			db := openCodeOperationFixturePool(t, dialect, prefix, workers)
			require.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Redemption{}))
			requireCodeOperationSchema(t, db)

			operationId := common.GetUUID()
			keys := make([][]string, workers)
			errs := make([]error, workers)
			start := make(chan struct{})
			done := make(chan struct{}, workers)
			for i := range workers {
				go func() {
					defer func() { done <- struct{}{} }()
					<-start
					result, err := CreateCodeBatch(newRedemptionBatchRequest(operationId, digest))
					errs[i] = err
					if result != nil {
						keys[i] = result.Keys
					}
				}()
			}
			close(start)
			for range workers {
				<-done
			}
			for _, err := range errs {
				require.NoError(t, err, "both callers must resolve to the committed batch")
			}
			require.Len(t, keys[0], 2)
			assert.Equal(t, keys[0], keys[1], "both callers observe the same keys")
			var batchCount int64
			require.NoError(t, db.Model(&ManagementOperationBatch{}).Count(&batchCount).Error)
			assert.Equal(t, int64(1), batchCount)
			var redemptionCount, outboxCount int64
			require.NoError(t, db.Model(&Redemption{}).Count(&redemptionCount).Error)
			assert.Equal(t, int64(2), redemptionCount, "concurrent retries commit one set of redemption codes")
			require.NoError(t, db.Model(&AuditEventOutbox{}).Count(&outboxCount).Error)
			assert.Equal(t, int64(1), outboxCount, "concurrent retries commit one audit obligation")
		})
	}
}

// TestAuditOutboxReplayAfterLostAcknowledgement proves the ACK-loss path on every
// supported relational sink: the sink accepts the event, its acknowledgement
// fails, and replay is absorbed by the event_id uniqueness constraint.
func TestAuditOutboxReplayAfterLostAcknowledgement(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			prefix := fmt.Sprintf("%sackloss_%s_", codeOperationTestPrefix, dialect)
			db := openCodeOperationFixture(t, dialect, prefix)
			require.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Redemption{}))
			t.Cleanup(func() {
				assert.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Redemption{}))
			})
			requireCodeOperationSchema(t, db)

			var logDriver gorm.Dialector
			switch dialect {
			case "sqlite":
				logDriver = sqlite.Open(filepath.Join(t.TempDir(), "audit_logs.db"))
			case "mysql":
				logDriver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				logDriver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			logSQLDB, err := logDB.DB()
			require.NoError(t, err)
			previousLogDB := LOG_DB
			previousLogType := common.LogDatabaseType()
			LOG_DB = logDB
			common.SetLogDatabaseType(common.DatabaseType(dialect))
			eventId := common.NewRequestId()
			t.Cleanup(func() {
				if logDB.Migrator().HasTable(&AuditLog{}) {
					assert.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).Delete(&AuditLog{}).Error)
				}
				LOG_DB = previousLogDB
				common.SetLogDatabaseType(previousLogType)
				assert.NoError(t, logSQLDB.Close())
			})
			require.NoError(t, MigrateAuditLogs())

			eventTime := common.GetTimestamp()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := EnqueueAuditEventTx(tx, AuditEventDraft{
					Category:      AuditCategoryOperation,
					Action:        "redemption.create",
					ActorUserId:   41,
					ActorUsername: "admin",
					ActorRole:     common.RoleAdminUser,
					EventId:       eventId,
				}, eventTime)
				return err
			}))

			updates := db.Callback().Update()
			const failAcknowledgement = "test:fail_audit_outbox_acknowledgement"
			callbackRegistered := true
			acknowledgementFailed := false
			require.NoError(t, updates.Before("gorm:update").Register(failAcknowledgement, func(tx *gorm.DB) {
				if tx.Statement.Table != (AuditEventOutbox{}).TableName() {
					return
				}
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				if _, ok := values["delivered_at"]; ok {
					acknowledgementFailed = true
					tx.AddError(errors.New("forced audit acknowledgement failure"))
				}
			}))
			t.Cleanup(func() {
				if callbackRegistered {
					assert.NoError(t, updates.Remove(failAcknowledgement))
				}
			})

			// The real drain writes to the sink before its acknowledgement update fails.
			require.Zero(t, DrainAuditOutbox(context.Background()))
			require.True(t, acknowledgementFailed, "the failure must reach the acknowledgement write")
			var outbox AuditEventOutbox
			require.NoError(t, db.Where("event_id = ?", eventId).First(&outbox).Error)
			require.Zero(t, outbox.DeliveredAt, "the failed acknowledgement leaves the event pending")
			require.NotEmpty(t, outbox.LeaseToken, "the failed acknowledgement preserves the worker lease")
			require.Positive(t, outbox.LeaseUntil)
			var auditCount int64
			require.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).Count(&auditCount).Error)
			require.Equal(t, int64(1), auditCount, "the sink committed before the acknowledgement failed")

			require.NoError(t, updates.Remove(failAcknowledgement))
			callbackRegistered = false
			// Advance only the lease to its expired state; leave delivery and retry
			// metadata exactly as the failed drain stored them.
			require.NoError(t, db.Model(&AuditEventOutbox{}).Where("event_id = ?", eventId).
				UpdateColumn("lease_until", common.GetTimestamp()-1).Error)

			// The replay must be absorbed, not left pending forever.
			assert.Equal(t, 1, DrainAuditOutbox(context.Background()))
			require.NoError(t, db.Where("event_id = ?", eventId).First(&outbox).Error)
			assert.NotZero(t, outbox.DeliveredAt, "the replayed delivery must be acknowledged")
			assert.Empty(t, outbox.LastError)

			require.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).Count(&auditCount).Error)
			assert.Equal(t, int64(1), auditCount, "the relational sink holds exactly one audit row")
			var delivered AuditLog
			require.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).First(&delivered).Error)
			assert.Equal(t, eventTime, delivered.CreatedAt, "delivery keeps the mutation timestamp")
			assert.Equal(t, "admin", delivered.Username)
			assert.Equal(t, 41, delivered.UserId)
		})
	}
}

// TestAuditOutboxClickHouseDelivery writes the same event to a ClickHouse log sink.
// ClickHouse has no dedupe key, so delivery is at-least-once and this asserts a
// real row is written with the event identity preserved.
func TestAuditOutboxClickHouseDelivery(t *testing.T) {
	dsn := os.Getenv("TEST_CLICKHOUSE_DSN")
	if dsn == "" {
		t.Skip("TEST_CLICKHOUSE_DSN is not configured")
	}
	prefix := codeOperationTestPrefix + "ch_"
	db := openCodeOperationFixture(t, "sqlite", prefix)
	requireCodeOperationSchema(t, db)

	logDB, err := gorm.Open(clickhouse.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previousLogDB := LOG_DB
	previousLogType := common.LogDatabaseType()
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousLogType)
	})
	require.NoError(t, migrateClickHouseLogDB())
	var deliveryErr error
	require.NoError(t, logDB.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("test:capture_audit_delivery_error", func(tx *gorm.DB) {
		if tx.Error != nil {
			deliveryErr = tx.Error
		}
	}))

	eventTime := common.GetTimestamp()
	eventId := common.NewRequestId()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := EnqueueAuditEventTx(tx, AuditEventDraft{
			EventId:     eventId,
			Category:    AuditCategoryOperation,
			Action:      "redemption.create",
			ActorUserId: 41,
			ActorRole:   common.RoleAdminUser,
		}, eventTime)
		return err
	}))
	delivered := DrainAuditOutbox(context.Background())
	var outbox AuditEventOutbox
	require.NoError(t, db.Where("event_id = ?", eventId).First(&outbox).Error)
	require.Equal(t, 1, delivered, "outbox last_error=%q; insert error=%v", outbox.LastError, sanitizeDBError(deliveryErr))

	var stored AuditLog
	require.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).First(&stored).Error)
	assert.Equal(t, eventTime, stored.CreatedAt)
	assert.Equal(t, "redemption.create", stored.Action)
}

func TestAuditOutboxBackoffIsBounded(t *testing.T) {
	assert.Equal(t, int64(10), AuditOutboxBackoff(1))
	assert.Equal(t, int64(20), AuditOutboxBackoff(2))
	assert.Equal(t, int64(600), AuditOutboxBackoff(8))
	assert.Equal(t, int64(600), AuditOutboxBackoff(100), "retries never stop or grow unbounded")
}

func TestCodeRequestDigestIsUnambiguous(t *testing.T) {
	base := map[string]string{"a": "x", "b": "y"}
	swapped := map[string]string{"a": "y", "b": "x"}
	assert.NotEqual(t, CodeRequestDigest(CodeOperationRedemption, base), CodeRequestDigest(CodeOperationRedemption, swapped))

	joined := map[string]string{"a": "xy", "b": ""}
	split := map[string]string{"a": "x", "b": "y"}
	assert.NotEqual(t, CodeRequestDigest(CodeOperationRedemption, joined), CodeRequestDigest(CodeOperationRedemption, split))

	separator := map[string]string{"a": "x\u001fy", "b": ""}
	plain := map[string]string{"a": "x", "b": "y"}
	assert.NotEqual(t, CodeRequestDigest(CodeOperationRedemption, separator), CodeRequestDigest(CodeOperationRedemption, plain))

	assert.Equal(t, CodeRequestDigest(CodeOperationRedemption, base),
		CodeRequestDigest(CodeOperationRedemption, map[string]string{"b": "y", "a": "x"}),
		"digest must be order independent")
}

// TestCodeOperationRecoveryRequiresAuthorization proves the recovery path applies
// the same ownership and payload checks as a normal replay.
func TestCodeOperationRecoveryRequiresAuthorization(t *testing.T) {
	prefix := codeOperationTestPrefix + "recovery_"
	db := openCodeOperationFixture(t, "sqlite", prefix)
	requireCodeOperationSchema(t, db)

	digest := CodeRequestDigest(CodeOperationRedemption, map[string]string{"name": "promo", "count": "2"})
	operationId := common.GetUUID()
	created, err := CreateCodeBatch(newRedemptionBatchRequest(operationId, map[string]string{"name": "promo", "count": "2"}))
	require.NoError(t, err)

	authorized := CodeBatchRequest{
		OperationId:   operationId,
		OperationType: CodeOperationRedemption,
		RequestDigest: digest,
		ActorUserId:   41,
	}
	replay, ok := resolveCodeBatchRace(authorized)
	require.True(t, ok, "the committing actor recovers its own batch")
	assert.Equal(t, created.Keys, replay.Keys)

	wrongActor := authorized
	wrongActor.ActorUserId = 42
	_, ok = resolveCodeBatchRace(wrongActor)
	assert.False(t, ok, "another actor must not recover the keys")

	wrongDigest := authorized
	wrongDigest.RequestDigest = CodeRequestDigest(CodeOperationRedemption, map[string]string{"name": "promo", "count": "9"})
	_, ok = resolveCodeBatchRace(wrongDigest)
	assert.False(t, ok, "a different request must not recover the keys")

	for _, err := range []error{ErrCodeBatchActorMismatch, ErrCodeBatchConflict, ErrCodeBatchInvalid} {
		assert.False(t, isCodeBatchRecoverable(err), "%v must not enter recovery", err)
	}
}

// TestCodeExportContractsAcrossDatabase keeps the actor-bound batch recovery and
// the visible table/export filter relationship on every supported primary DB.
func TestCodeExportContractsAcrossDatabase(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			prefix := fmt.Sprintf("%sexport_%s_", codeOperationTestPrefix, dialect)
			db := openCodeOperationFixture(t, dialect, prefix)
			require.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Invitation{}, &Redemption{}))
			t.Cleanup(func() {
				assert.NoError(t, db.Migrator().DropTable(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Invitation{}, &Redemption{}))
			})
			require.NoError(t, db.AutoMigrate(&ManagementOperationBatch{}, &AuditEventOutbox{}, &Invitation{}, &Redemption{}))

			sharedKey := strings.Repeat("x", 32)
			old := common.GetTimestamp() - 3600
			future := common.GetTimestamp() + 3600
			redemptions := []Redemption{
				{UserId: 41, Key: sharedKey, Name: "needle-redemption-expired", Status: common.RedemptionCodeStatusEnabled, Quota: 100, ExpiredTime: old},
				{UserId: 41, Key: strings.Repeat("y", 32), Name: "needle-redemption-current", Status: common.RedemptionCodeStatusEnabled, Quota: 200, ExpiredTime: future},
			}
			invitations := []Invitation{
				{UserId: 41, Code: sharedKey, Name: "prefix-needle-invitation-expired", Status: InvitationEnabled, ExpiredTime: old},
				{UserId: 41, Code: strings.Repeat("z", 32), Name: "prefix-needle-invitation-current", Status: InvitationEnabled, ExpiredTime: future},
			}
			require.NoError(t, db.Create(&redemptions).Error)
			require.NoError(t, db.Create(&invitations).Error)

			redemptionOperationId := common.GetUUID()
			invitationOperationId := common.GetUUID()
			encodeResult := func(operationId, key string) string {
				encoded, err := common.Marshal(CodeBatchResult{OperationId: operationId, Keys: []string{key}})
					require.NoError(t, err)
				return string(encoded)
			}
			batches := []ManagementOperationBatch{
				{OperationId: redemptionOperationId, OperationType: CodeOperationRedemption, State: codeOperationStateDone, ActorUserId: 41, ResultJson: encodeResult(redemptionOperationId, sharedKey), CreatedAt: common.GetTimestamp()},
				{OperationId: invitationOperationId, OperationType: CodeOperationInvitation, State: codeOperationStateDone, ActorUserId: 41, ResultJson: encodeResult(invitationOperationId, sharedKey), CreatedAt: common.GetTimestamp()},
			}
			require.NoError(t, db.Create(&batches).Error)

			redemptionBatch, err := ExportRedemptions(nil, redemptionOperationId, 41, CodeExportFilter{}, CodeExportMaxRows)
			require.NoError(t, err)
			require.Len(t, redemptionBatch, 1)
			assert.Equal(t, sharedKey, redemptionBatch[0].Key)
			invitationBatch, err := ExportInvitations(nil, invitationOperationId, 41, CodeExportFilter{}, CodeExportMaxRows)
			require.NoError(t, err)
			require.Len(t, invitationBatch, 1)
			assert.Equal(t, sharedKey, invitationBatch[0].Key)

			_, err = ExportRedemptions(nil, invitationOperationId, 41, CodeExportFilter{}, CodeExportMaxRows)
			assert.ErrorIs(t, err, ErrCodeExportEmpty, "a redemption endpoint cannot recover an invitation batch")
			_, err = ExportInvitations(nil, redemptionOperationId, 41, CodeExportFilter{}, CodeExportMaxRows)
			assert.ErrorIs(t, err, ErrCodeExportEmpty, "an invitation endpoint cannot recover a redemption batch")
			_, err = ExportRedemptions(nil, redemptionOperationId, 42, CodeExportFilter{}, CodeExportMaxRows)
			assert.ErrorIs(t, err, ErrCodeExportEmpty, "another actor cannot recover the batch keys")
			_, err = ExportInvitations(nil, invitationOperationId, 42, CodeExportFilter{}, CodeExportMaxRows)
			assert.ErrorIs(t, err, ErrCodeExportEmpty, "another actor cannot recover the batch keys")

			redemptionRows, _, err := SearchRedemptions("needle", "expired", 0, CodeExportMaxRows)
			require.NoError(t, err)
			redemptionExport, err := ExportRedemptions(nil, "", 41, CodeExportFilter{Keyword: "needle", Status: "expired"}, CodeExportMaxRows)
			require.NoError(t, err)
			redemptionIDs := make([]int, 0, len(redemptionRows))
			for _, row := range redemptionRows {
				redemptionIDs = append(redemptionIDs, row.Id)
			}
			redemptionExportIDs := make([]int, 0, len(redemptionExport))
			for _, row := range redemptionExport {
				redemptionExportIDs = append(redemptionExportIDs, row.Id)
			}
			assert.Equal(t, redemptionIDs, redemptionExportIDs)

			invitationRows, _, err := ListInvitations("needle", "expired", 0, CodeExportMaxRows)
			require.NoError(t, err)
			invitationExport, err := ExportInvitations(nil, "", 41, CodeExportFilter{Keyword: "needle", Status: "expired"}, CodeExportMaxRows)
			require.NoError(t, err)
			invitationIDs := make([]int, 0, len(invitationRows))
			for _, row := range invitationRows {
				invitationIDs = append(invitationIDs, row.Id)
			}
			invitationExportIDs := make([]int, 0, len(invitationExport))
			for _, row := range invitationExport {
				invitationExportIDs = append(invitationExportIDs, row.Id)
			}
			assert.Equal(t, invitationIDs, invitationExportIDs)
		})
	}
}


// TestRecordManagementAuditFallsBackWithSameEventIdentity forces the primary
// outbox insert to fail and verifies the direct sink fallback preserves the exact
// actor, event identity, action, and structured params.
func TestRecordManagementAuditFallsBackWithSameEventIdentity(t *testing.T) {
	db := openCodeOperationFixture(t, "sqlite", codeOperationTestPrefix+"audit_fallback_")
	requireCodeOperationSchema(t, db)

	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit_fallback_logs.db")), &gorm.Config{})
	require.NoError(t, err)
	logSQLDB, err := logDB.DB()
	require.NoError(t, err)
	previousLogDB := LOG_DB
	previousLogType := common.LogDatabaseType()
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.SetLogDatabaseType(previousLogType)
		assert.NoError(t, logSQLDB.Close())
	})
	require.NoError(t, MigrateAuditLogs())

	const callbackName = "test:force_management_audit_outbox_insert_failure"
	callbacks := db.Callback().Create()
	callbackRegistered := true
	require.NoError(t, callbacks.Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if strings.HasSuffix(tx.Statement.Table, (AuditEventOutbox{}).TableName()) {
			tx.AddError(errors.New("forced outbox enqueue failure"))
		}
	}))
	t.Cleanup(func() {
		if callbackRegistered {
			assert.NoError(t, callbacks.Remove(callbackName))
		}
	})

	eventId := common.NewRequestId()
	params, err := common.Marshal(AuditFields{"name": "spring", "count": 3})
	require.NoError(t, err)
	draft := AuditEventDraft{
		EventId:       eventId,
		Category:      AuditCategoryOperation,
		Action:        "invitation.create",
		ActorUserId:   41,
		ActorUsername: "operator",
		ActorRole:     common.RoleAdminUser,
		AuthMethod:    "session",
		Method:        "POST",
		Route:         "/api/invitation/",
		Status:        200,
		Success:       true,
		RequestId:     common.NewRequestId(),
		Content:       "Created invitation codes",
		ParamsJson:    string(params),
	}
	returnedEventId, err := RecordManagementAuditDurable(context.Background(), draft)
	require.NoError(t, err)
	assert.Equal(t, eventId, returnedEventId)

	var delivered AuditLog
	require.NoError(t, logDB.Table("audit_logs").Where("event_id = ?", eventId).First(&delivered).Error)
	assert.Equal(t, eventId, delivered.EventId)
	assert.Equal(t, "invitation.create", delivered.Action)
	assert.Equal(t, 41, delivered.UserId)
	assert.Equal(t, "operator", delivered.Username)
	assert.Equal(t, common.RoleAdminUser, delivered.ActorRole)
	require.NotNil(t, delivered.Other.Op)
	assert.Equal(t, "invitation.create", delivered.Other.Op.Action)
	encodedParams, err := common.Marshal(delivered.Other.Op.Params)
	require.NoError(t, err)
	var storedParams struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	require.NoError(t, common.Unmarshal(encodedParams, &storedParams))
	assert.Equal(t, "spring", storedParams.Name)
	assert.Equal(t, 3, storedParams.Count)

	var queued int64
	require.NoError(t, db.Model(&AuditEventOutbox{}).Where("event_id = ?", eventId).Count(&queued).Error)
	assert.Zero(t, queued, "a failed enqueue must not claim that the event is durably queued")
	assert.NoError(t, callbacks.Remove(callbackName))
	callbackRegistered = false
}
