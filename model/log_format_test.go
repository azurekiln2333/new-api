package model

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	sqlmysql "github.com/go-sql-driver/mysql"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestLogRequestDetailStorageDatabaseMatrix(t *testing.T) {
	runRequestLogStorageMatrix(t, func(t *testing.T, db *gorm.DB, upgrade bool) {
		require.NoError(t, DB.AutoMigrate(&Task{}))
		versionQuery := "SELECT version()"
		if common.UsingLogDatabase(common.DatabaseTypeSQLite) {
			versionQuery = "SELECT sqlite_version()"
		}
		var version string
		require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
		t.Logf("log store: %s %s", common.LogDatabaseType(), version)

		// Log's schema/tags are unchanged from custom-20261118-9b21c12.
		// Upgrade fixtures use that schema and data before either new table.
		legacy := Log{UserId: logFilterTestUser, RequestId: common.NewRequestId(), CreatedAt: 1002, Type: LogTypeConsume, Other: `{"admin_info":{"legacy":true}}`}
		var originalIndexes []gorm.Index
		if upgrade {
			if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
				require.NoError(t, db.Exec(clickHouseLogCreateTableSQL(0)).Error)
			} else {
				require.NoError(t, db.AutoMigrate(&Log{}))
				var err error
				originalIndexes, err = db.Migrator().GetIndexes(&Log{})
				require.NoError(t, err)
			}
			require.NoError(t, db.Create(&legacy).Error)
		}
		require.NoError(t, migrateLOGDB())
		require.NoError(t, migrateLOGDB(), "fresh and upgrade migration must both be idempotent")
		if upgrade {
			var retained []Log
			require.NoError(t, db.Where("request_id = ?", legacy.RequestId).Find(&retained).Error)
			require.Len(t, retained, 1)
			assert.Equal(t, legacy.Other, retained[0].Other)
		}
		for _, index := range originalIndexes {
			assert.True(t, db.Migrator().HasIndex(&Log{}, index.Name()), "released log index %s must survive", index.Name())
		}
		ctx := t.Context()
		payload, err := common.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("original ", 10000)}}})
		require.NoError(t, err)
		snapshot := &common.RequestContentSnapshot{
			Ref: common.GetUUID(), RequestID: common.NewRequestId(), UserID: logFilterTestUser,
			SubmitNodeName: "hk01", CreatedAt: 1000, Status: "recorded", Payload: payload, CapturedBytes: len(payload),
		}
		t.Cleanup(func() {
			if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
				assert.NoError(t, db.Exec("ALTER TABLE log_request_details DELETE WHERE ref = ? SETTINGS mutations_sync = 1", snapshot.Ref).Error)
			} else {
				assert.NoError(t, db.Where("ref = ?", snapshot.Ref).Delete(&LogRequestDetail{}).Error)
			}
			assert.NoError(t, DB.Where("ref = ?", snapshot.Ref).Delete(&LogRequestContentProtection{}).Error)
		})

		writeLog := func(logType int) func(*gorm.DB, error) error {
			return func(logDB *gorm.DB, detailErr error) error {
				if !assert.NoError(t, detailErr) {
					return detailErr
				}
				log := Log{UserId: snapshot.UserID, RequestId: snapshot.RequestID, CreatedAt: 1001, Type: logType}
				linkID, _ := logDB.Statement.Context.Value(requestContentLinkKey{}).(string)
				other := NewLogOther()
				other.SetAdmin("request_content", requestContentLogLink{RequestContentMetadata: common.RequestContentMetadata{Ref: snapshot.Ref, Status: snapshot.Status}, LinkID: linkID})
				log.Other = other.JSONString()
				return logDB.Create(&log).Error
			}
		}
		require.NoError(t, WithLogRequestDetail(ctx, snapshot, writeLog(LogTypeError)))
		stored, err := GetLogRequestDetail(ctx, snapshot.Ref)
		require.NoError(t, err)
		assert.Equal(t, string(payload), string(stored.Payload))
		assert.Equal(t, "hk01", stored.SubmitNodeName)
		encoded, err := common.Marshal(stored)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, common.Unmarshal(encoded, &decoded))
		assert.IsType(t, map[string]any{}, decoded["payload"])

		// Later attempts and async settlement reuse the immutable original.
		restored := *snapshot
		restored.Payload = nil
		require.NoError(t, WithLogRequestDetail(ctx, &restored, writeLog(LogTypeConsume)))
		var copies int64
		require.NoError(t, db.Model(&LogRequestDetail{}).Where("ref = ?", snapshot.Ref).Count(&copies).Error)
		assert.Equal(t, int64(1), copies)
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			require.NoError(t, db.Exec("ALTER TABLE logs DELETE WHERE request_id = ? AND type = ? SETTINGS mutations_sync = 1", snapshot.RequestID, LogTypeError).Error)
		} else {
			require.NoError(t, db.Where("request_id = ? AND type = ?", snapshot.RequestID, LogTypeError).Delete(&Log{}).Error)
		}
		require.NoError(t, CleanupLogRequestDetails(ctx, 2000, 100))
		_, err = GetLogRequestDetail(ctx, snapshot.Ref)
		require.NoError(t, err, "the surviving success must keep retry content")

		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			require.NoError(t, db.Exec("ALTER TABLE logs DELETE WHERE request_id = ? SETTINGS mutations_sync = 1", snapshot.RequestID).Error)
		} else {
			require.NoError(t, db.Where("request_id = ?", snapshot.RequestID).Delete(&Log{}).Error)
		}
		_, err = GetLogRequestDetail(ctx, snapshot.Ref)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, "unlinked content must not be served before physical cleanup")
		require.NoError(t, CleanupLogRequestDetails(ctx, 2000, 100))
		require.NoError(t, db.Model(&LogRequestDetail{}).Where("ref = ?", snapshot.Ref).Count(&copies).Error)
		assert.Zero(t, copies)
		called := false
		require.NoError(t, WithLogRequestDetail(ctx, &restored, func(logDB *gorm.DB, detailErr error) error {
			called = true
			assert.Error(t, detailErr, "a metadata-only task must not recreate a blank recorded body")
			return nil
		}))
		assert.True(t, called, "diagnostic failure cannot suppress the business-log callback")

		t.Run("cleanup_during_link_commit", func(t *testing.T) {
			// A second connection observes the committed pending intent while
			// the writer is paused before inserting its remote log. Shared-store
			// transactions expose neither payload nor link until their commit.
			pool, err := DB.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(2)
			defer pool.SetMaxOpenConns(1)
			inflight := *snapshot
			inflight.Ref, inflight.RequestID = common.GetUUID(), common.NewRequestId()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			go func() {
				done <- WithLogRequestDetail(t.Context(), &inflight, func(logDB *gorm.DB, detailErr error) error {
					if detailErr != nil {
						return detailErr
					}
					close(entered)
					select {
					case <-release:
					case <-logDB.Statement.Context.Done():
						return logDB.Statement.Context.Err()
					}
					linkID, _ := logDB.Statement.Context.Value(requestContentLinkKey{}).(string)
					other := NewLogOther()
					other.SetAdmin("request_content", requestContentLogLink{
						RequestContentMetadata: common.RequestContentMetadata{Ref: inflight.Ref, Status: inflight.Status}, LinkID: linkID,
					})
					return logDB.Create(&Log{UserId: inflight.UserID, RequestId: inflight.RequestID,
						CreatedAt: 1001, Type: LogTypeConsume, Other: other.JSONString()}).Error
				})
			}()
			select {
			case <-entered:
			case err := <-done:
				require.NoError(t, err)
				t.Fatal("log callback was not reached")
			}
			err = CleanupLogRequestDetails(t.Context(), 2000, 100)
			releaseOnce.Do(func() { close(release) })
			require.NoError(t, <-done)
			require.NoError(t, err)
			require.NoError(t, CleanupLogRequestDetails(t.Context(), 2000, 100))
			stored, err := GetLogRequestDetail(t.Context(), inflight.Ref)
			require.NoError(t, err, "cleanup cannot delete content being linked or its later surviving log")
			assert.Equal(t, string(inflight.Payload), string(stored.Payload))
		})

		t.Run("metadata_only_task", func(t *testing.T) {
			snapshot := *snapshot
			snapshot.Ref, snapshot.RequestID = common.GetUUID(), common.NewRequestId()
			t.Cleanup(func() {
				assert.NoError(t, deleteRequestContentDetail(db, snapshot.Ref))
				assert.NoError(t, DB.Where("ref = ?", snapshot.Ref).Delete(&LogRequestContentProtection{}).Error)
			})
			require.NoError(t, WithLogRequestDetail(t.Context(), &snapshot, func(_ *gorm.DB, detailErr error) error { return detailErr }))
			metadataOnly := snapshot
			metadataOnly.Payload = nil
			task := Task{TaskID: common.GetUUID(), UserId: snapshot.UserID, Platform: "diagnostic_test", Status: TaskStatusSuccess,
				PrivateData: TaskPrivateData{BudgetPending: true}}
			t.Cleanup(func() { assert.NoError(t, DB.Where("task_id = ?", task.TaskID).Delete(&Task{}).Error) })
			require.NoError(t, InsertTaskWithRequestContent(t.Context(), &task, &metadataOnly))
			var stored Task
			require.NoError(t, DB.Where("task_id = ?", task.TaskID).Take(&stored).Error)
			require.NotNil(t, stored.PrivateData.RequestContent)
			assert.Equal(t, snapshot.Ref, stored.PrivateData.RequestContent.Ref)
			// The grace period has elapsed; a terminal task with durable
			// settlement intent still pins its original prompt.
			require.NoError(t, DB.Model(&task).UpdateColumn("updated_at", 1001).Error)
			require.NoError(t, CleanupLogRequestDetails(t.Context(), 2000, 100))
			var count int64
			require.NoError(t, db.Model(&LogRequestDetail{}).Where("ref = ?", snapshot.Ref).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			task.PrivateData.BudgetPending = false
			require.NoError(t, DB.Model(&task).UpdateColumn("private_data", task.PrivateData).Error)
			require.NoError(t, CleanupLogRequestDetails(t.Context(), 2000, 100))
			require.NoError(t, db.Model(&LogRequestDetail{}).Where("ref = ?", snapshot.Ref).Count(&count).Error)
			assert.Zero(t, count)
		})
	})
}

func TestLogRequestDetailConfiguredSQLiteAlias(t *testing.T) {
	// LOG_SQL_DSN=local selects SQLitePath just like SQL_DSN=local. The two
	// configurations must not compete for the same file's writer lock.
	path := filepath.Join(t.TempDir(), "aliased.db")
	seed, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, seed.AutoMigrate(&Option{}))
	seedSQL, err := seed.DB()
	require.NoError(t, err)
	require.NoError(t, seedSQL.Close())
	previousMain, previousLogs := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath, previousMaster := common.SQLitePath, common.IsMasterNode
	common.SQLitePath, common.IsMasterNode = path, false
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "local")
	t.Setenv("CHANNEL_BUDGET_ENABLED", "false")
	t.Cleanup(func() {
		if LOG_DB != nil && LOG_DB != DB {
			pool, err := LOG_DB.DB()
			if assert.NoError(t, err) {
				assert.NoError(t, pool.Close())
			}
		}
		if DB != nil {
			pool, err := DB.DB()
			if assert.NoError(t, err) {
				assert.NoError(t, pool.Close())
			}
		}
		DB, LOG_DB = previousMain, previousLogs
		common.SQLitePath, common.IsMasterNode = previousPath, previousMaster
		common.SetDatabaseTypes(previousMainType, previousLogType)
		initCol()
	})
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())
	require.NoError(t, migrateLOGDB())
	require.NoError(t, migrateLOGDB())
	snapshot := &common.RequestContentSnapshot{
		Ref: common.GetUUID(), RequestID: common.NewRequestId(), UserID: 7,
		CreatedAt: 1000, Status: common.RequestContentStatusRecorded, Payload: common.RawMessage(`{"prompt":"local alias"}`),
	}
	other := NewLogOther()
	other.SetAdmin("request_content", common.RequestContentMetadata{Ref: snapshot.Ref, Status: snapshot.Status})
	require.NoError(t, WithLogRequestDetail(t.Context(), snapshot, func(db *gorm.DB, detailErr error) error {
		if !assert.NoError(t, detailErr, "configured same-file aliases must persist the payload and log") {
			return detailErr
		}
		return db.Create(&Log{UserId: snapshot.UserID, RequestId: snapshot.RequestID, Type: LogTypeConsume, CreatedAt: 1001, Other: other.JSONString()}).Error
	}))
	detail, err := GetLogRequestDetail(t.Context(), snapshot.Ref)
	require.NoError(t, err)
	assert.Equal(t, string(snapshot.Payload), string(detail.Payload))
}

func TestLogRequestDetailIndependentStoreRecovery(t *testing.T) {
	// Two real, one-connection SQLite databases exercise separate-store
	// commits without depending on a driver mock or an elapsed wall clock.
	main, logs := openRequestLogSQLite(t), openRequestLogSQLite(t)
	useRequestLogTestStores(t, main, logs, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, DB.AutoMigrate(&Task{}))
	require.NoError(t, migrateLOGDB())
	require.NoError(t, migrateLOGDB())
	ctx := t.Context()
	snapshot := &common.RequestContentSnapshot{Ref: common.GetUUID(), RequestID: common.NewRequestId(), UserID: 7,
		Status: common.RequestContentStatusRecorded, CreatedAt: 1000, Payload: common.RawMessage(`{"prompt":"retained"}`)}
	other := NewLogOther()
	other.SetAdmin("request_content", common.RequestContentMetadata{Ref: snapshot.Ref, Status: snapshot.Status})
	log := Log{UserId: snapshot.UserID, RequestId: snapshot.RequestID, CreatedAt: 1001, Type: LogTypeError, Other: other.JSONString()}
	var linkID string
	uncertain := errors.New("unknown external log outcome")
	require.ErrorIs(t, WithLogRequestDetail(ctx, snapshot, func(logDB *gorm.DB, detailErr error) error {
		require.NoError(t, detailErr)
		linkID, _ = logDB.Statement.Context.Value(requestContentLinkKey{}).(string)
		// The remote insert has an uncertain outcome, simulated below by a
		// log landing only after recovery first observes no reference.
		return uncertain
	}), uncertain)
	require.NotEmpty(t, linkID)
	var pending LogRequestContentProtection
	require.NoError(t, DB.Take(&pending, "ref = ?", snapshot.Ref).Error)
	require.Equal(t, requestContentPhaseLinkPending, pending.Phase)
	metadataOnly := *snapshot
	metadataOnly.Payload = nil
	require.NoError(t, WithLogRequestDetail(ctx, &metadataOnly, func(_ *gorm.DB, detailErr error) error {
		assert.Error(t, detailErr, "a writer cannot displace a live holder")
		return nil
	}))
	var unchanged LogRequestContentProtection
	require.NoError(t, DB.Take(&unchanged, "ref = ?", snapshot.Ref).Error)
	assert.Equal(t, pending.HolderID, unchanged.HolderID)
	assert.Equal(t, pending.Generation, unchanged.Generation)
	require.NoError(t, DB.Model(&pending).UpdateColumn("lease_until", 1).Error)
	require.NoError(t, RecoverLogRequestContentOperations(ctx, 100))
	var detail LogRequestDetail
	require.NoError(t, logs.Take(&detail, "ref = ?", snapshot.Ref).Error, "an absent remote link is not proof of rollback")
	_, err := GetLogRequestDetail(ctx, snapshot.Ref)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, logs.Create(&log).Error)
	require.NoError(t, RecoverLogRequestContentOperations(ctx, 100))
	require.NoError(t, WithLogRequestDetail(ctx, &metadataOnly, func(_ *gorm.DB, detailErr error) error {
		assert.Error(t, detailErr, "an earlier retry's ref does not prove the uncertain write")
		return nil
	}))
	other.SetAdmin("request_content", requestContentLogLink{
		RequestContentMetadata: common.RequestContentMetadata{Ref: snapshot.Ref, Status: snapshot.Status}, LinkID: linkID,
	})
	late := Log{UserId: snapshot.UserID, RequestId: snapshot.RequestID, CreatedAt: 1002, Type: LogTypeConsume, Other: other.JSONString()}
	require.NoError(t, logs.Create(&late).Error)
	require.NoError(t, RecoverLogRequestContentOperations(ctx, 100))
	require.NoError(t, WithLogRequestDetail(ctx, &metadataOnly, func(_ *gorm.DB, detailErr error) error { return detailErr }))

	// A cleanup observation made before a new generation cannot remove it.
	require.NoError(t, reclaimRequestContent(ctx, &pending, true))
	result, err := GetLogRequestDetail(ctx, snapshot.Ref)
	require.NoError(t, err)
	assert.Equal(t, string(snapshot.Payload), string(result.Payload))

	t.Run("uncertain_delete_keeps_a_fence_and_sweeps_late_payloads", func(t *testing.T) {
		orphan := *snapshot
		orphan.Ref, orphan.RequestID = common.GetUUID(), common.NewRequestId()
		require.NoError(t, WithLogRequestDetail(ctx, &orphan, func(_ *gorm.DB, err error) error { return err }))
		// Execute the real remote delete but lose its acknowledgement. A
		// separate store cannot roll that SQL back with the primary fence.
		logs.Config.SkipDefaultTransaction = true
		t.Cleanup(func() { logs.Config.SkipDefaultTransaction = false })
		const hook = "request-content:lost-delete-ack"
		deleteErr := errors.New("remote delete acknowledgement lost")
		require.NoError(t, logs.Callback().Delete().After("gorm:delete").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "log_request_details" {
				tx.AddError(deleteErr)
			}
		}))
		t.Cleanup(func() { assert.NoError(t, logs.Callback().Delete().Remove(hook)) })
		require.Error(t, CleanupLogRequestDetails(ctx, 2000, 100))
		require.NoError(t, logs.Callback().Delete().Remove(hook))
		var count int64
		require.NoError(t, logs.Model(&LogRequestDetail{}).Where("ref = ?", orphan.Ref).Count(&count).Error)
		assert.Zero(t, count, "the payload delete really executed despite the lost acknowledgement")
		var fence LogRequestContentProtection
		require.NoError(t, DB.Take(&fence, "ref = ?", orphan.Ref).Error)
		require.Equal(t, requestContentPhaseDeletePending, fence.Phase)
		require.NoError(t, WithLogRequestDetail(ctx, &orphan, func(_ *gorm.DB, err error) error {
			assert.Error(t, err, "a timed-out delete cannot be displaced by a writer")
			return nil
		}))
		require.NoError(t, DB.Model(&fence).UpdateColumn("lease_until", 1).Error)
		require.NoError(t, RecoverLogRequestContentOperations(ctx, 100))
		require.NoError(t, DB.Take(&fence, "ref = ?", orphan.Ref).Error)
		assert.Equal(t, requestContentPhaseRetired, fence.Phase)
		// An old server-side insert can finish after a timeout. A retired ref
		// is never reused, and bounded compensation removes its late bytes.
		require.NoError(t, logs.Create(&LogRequestDetail{Ref: orphan.Ref, RequestID: orphan.RequestID, UserID: orphan.UserID,
			CreatedAt: orphan.CreatedAt, Status: orphan.Status, Payload: LogRequestPayload(orphan.Payload)}).Error)
		require.NoError(t, CleanupLogRequestDetails(ctx, 2000, 100))
		require.NoError(t, logs.Model(&LogRequestDetail{}).Where("ref = ?", orphan.Ref).Count(&count).Error)
		assert.Zero(t, count)
		require.NoError(t, WithLogRequestDetail(ctx, &orphan, func(_ *gorm.DB, err error) error {
			assert.Error(t, err, "retired refs cannot acquire another payload or link")
			return nil
		}))
	})
}

func TestLogRequestDetailWriterLeaseAndTaskFallback(t *testing.T) {
	main, logs := openRequestLogSQLite(t), openRequestLogSQLite(t)
	useRequestLogTestStores(t, main, logs, common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, DB.AutoMigrate(&Task{}))
	require.NoError(t, migrateLOGDB())
	snapshot := &common.RequestContentSnapshot{Ref: common.GetUUID(), RequestID: common.NewRequestId(), UserID: 7,
		Status: common.RequestContentStatusRecorded, CreatedAt: 1000, Payload: common.RawMessage(`{"prompt":"original"}`), SubmitNodeName: "hk01"}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	release := TrackRequestContentWrites(c)
	t.Cleanup(release)
	require.NoError(t, WithLogRequestDetail(c.Request.Context(), snapshot, func(_ *gorm.DB, err error) error { return err }))
	require.NoError(t, CleanupLogRequestDetails(t.Context(), 2000, 100))
	var count int64
	require.NoError(t, logs.Model(&LogRequestDetail{}).Count(&count).Error)
	assert.EqualValues(t, 1, count, "the live request keeps its body between two attempts")
	release()
	require.NoError(t, CleanupLogRequestDetails(t.Context(), 2000, 100))
	require.NoError(t, logs.Model(&LogRequestDetail{}).Count(&count).Error)
	assert.Zero(t, count)

	// The main store can still accept tasks while an independent detail store
	// cannot write. Exercise an actual SQL error, not just a bad parameter.
	require.NoError(t, logs.Migrator().DropTable(&LogRequestDetail{}))
	task := Task{TaskID: "accepted-without-diagnostics", UserId: snapshot.UserID, Status: TaskStatusSubmitted}
	require.NoError(t, InsertTaskWithRequestContent(t.Context(), &task, snapshot))
	var stored []Task
	require.NoError(t, DB.Where("task_id = ?", task.TaskID).Find(&stored).Error)
	require.Len(t, stored, 1)
	require.NotNil(t, stored[0].PrivateData.RequestContent)
	assert.Empty(t, stored[0].PrivateData.RequestContent.Ref)
	assert.Equal(t, common.RequestContentStatusStorageFail, stored[0].PrivateData.RequestContent.Status)
	assert.Equal(t, "hk01", stored[0].PrivateData.RequestContent.SubmitNodeName)

	// Failure of the authoritative insert must still be returned rather than
	// being mistaken for an optional diagnostic failure.
	require.NoError(t, MigrateLogRequestDetails())
	snapshot.Ref = common.GetUUID()
	task = stored[0]
	task.TaskID = "failed-authoritative-insert"
	require.Error(t, InsertTaskWithRequestContent(t.Context(), &task, snapshot))
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", task.TaskID).Count(&count).Error)
	assert.Zero(t, count)
}

func runRequestLogStorageMatrix(t *testing.T, test func(*testing.T, *gorm.DB, bool)) {
	t.Helper()
	for _, engine := range []struct{ kind, env string }{
		{"sqlite", ""}, {"mysql", "TEST_MYSQL_DSN"}, {"postgres", "TEST_POSTGRES_DSN"}, {"clickhouse", "TEST_CLICKHOUSE_DSN"},
	} {
		t.Run(engine.kind, func(t *testing.T) {
			dsn := os.Getenv(engine.env)
			if engine.env != "" && dsn == "" {
				t.Skip("set " + engine.env + " to run this database")
			}
			for _, independent := range []bool{false, true} {
				if engine.kind == "clickhouse" && !independent {
					continue
				}
				for _, upgrade := range []bool{false, true} {
					name := "shared/fresh"
					if independent {
						name = "independent/fresh"
					}
					if upgrade {
						name = strings.TrimSuffix(name, "fresh") + "released_upgrade"
					}
					t.Run(name, func(t *testing.T) {
						logs := openRequestLogStore(t, engine.kind, dsn)
						main, mainType := logs, common.DatabaseType(engine.kind)
						if independent {
							if engine.kind == "clickhouse" {
								main, mainType = openRequestLogSQLite(t), common.DatabaseTypeSQLite
							} else {
								main = openRequestLogStore(t, engine.kind, dsn)
							}
						}
						useRequestLogTestStores(t, main, logs, mainType, common.DatabaseType(engine.kind))
						test(t, logs, upgrade)
					})
				}
			}
		})
	}
}

// openRequestLogStore gives each migration case actual isolated stores. No
// table in the shared TEST_* database is dropped or reused by these cases.
func openRequestLogStore(t *testing.T, kind, dsn string) *gorm.DB {
	t.Helper()
	if kind == "sqlite" {
		return openRequestLogSQLite(t)
	}
	name := "newapi_request_log_" + common.GetUUID()
	var source, isolated gorm.Dialector
	if kind == "mysql" {
		config, err := sqlmysql.ParseDSN(dsn)
		require.NoError(t, err)
		host, _, err := net.SplitHostPort(config.Addr)
		require.NoError(t, err)
		require.True(t, net.ParseIP(host).IsLoopback(), "destructive database fixtures require loopback")
		source = mysql.Open(dsn)
		config.DBName = name
		isolated = mysql.Open(config.FormatDSN())
	} else {
		parsed, err := url.Parse(normalizeClickHouseDSN(dsn))
		require.NoError(t, err)
		host := parsed.Hostname()
		require.True(t, host == "localhost" || net.ParseIP(host).IsLoopback(), "destructive database fixtures require loopback")
		parsed.Path = "/" + name
		if kind == "clickhouse" {
			source, isolated = clickhouse.Open(normalizeClickHouseDSN(dsn)), clickhouse.Open(parsed.String())
		} else {
			source, isolated = postgres.Open(dsn), postgres.Open(parsed.String())
		}
	}
	admin, err := gorm.Open(source, &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, adminSQL.Close()) })
	create := "CREATE DATABASE ?"
	if kind == "mysql" {
		create += " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
	}
	require.NoError(t, admin.Exec(create, clause.Table{Name: name}).Error)
	t.Logf("isolated %s database: %s", kind, name)
	t.Cleanup(func() { assert.NoError(t, admin.Exec("DROP DATABASE ?", clause.Table{Name: name}).Error) })
	db, err := gorm.Open(isolated, &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { assert.NoError(t, pool.Close()) })
	return db
}

func openRequestLogSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "request-logs.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { assert.NoError(t, pool.Close()) })
	return db
}

func useRequestLogTestStores(t *testing.T, main, logs *gorm.DB, mainType, logType common.DatabaseType) {
	t.Helper()
	previousMain, previousLogs := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	DB, LOG_DB = main, logs
	common.SetDatabaseTypes(mainType, logType)
	initCol()
	t.Cleanup(func() {
		DB, LOG_DB = previousMain, previousLogs
		common.SetDatabaseTypes(previousMainType, previousLogType)
		initCol()
	})
}

// TestFormatUserLogsStripsQuotaSaturation verifies the admin-only quota
// saturation marker (nested under other.admin_info) is removed for non-admin
// log views, since formatUserLogs strips the whole admin_info object.
func TestFormatUserLogsStripsQuotaSaturation(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"model_price": 0.004,
		"admin_info": map[string]any{
			"quota_saturation": map[string]any{
				"op":      "QuotaFromDecimal",
				"kind":    "overflow",
				"clamped": common.MaxQuota,
			},
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	_, hasAdminInfo := parsed["admin_info"]
	require.False(t, hasAdminInfo, "admin_info (and nested quota_saturation) must be stripped for non-admin views")
	// Non-admin billing fields remain visible.
	require.Contains(t, parsed, "model_price")
}

func TestTaskPluginLogVisibilityIsRoleSeparated(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"model_price": 1.25,
		"admin_info": map[string]any{
			"task_plugin": map[string]any{
				"key":     "document-parser",
				"name":    "Document Parser",
				"version": "1.2.3",
			},
		},
		"root_info": map[string]any{
			"upstream_task_id": "upstream-private",
			"task_plugin": map[string]any{
				"generation": 42,
			},
		},
	})

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		formatUserLogs(logs, 0)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.NotContains(t, parsed, "admin_info")
		assert.NotContains(t, parsed, "root_info")
		assert.Equal(t, 1.25, parsed["model_price"])
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		FormatAdminLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Contains(t, parsed, "admin_info")
		assert.NotContains(t, parsed, "root_info")
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		FormatRootLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Contains(t, parsed, "admin_info")
		assert.Contains(t, parsed, "root_info")
	})
}

func TestLegacyLogOtherVisibilityIsRoleSeparated(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"request_path":  "/v1/chat/completions",
		"channel_id":    202,
		"channel_name":  "legacy-secret-channel",
		"channel_type":  1,
		"reject_reason": "legacy-policy-rejection",
		"admin_info": map[string]any{
			"existing_admin_field": "preserved",
		},
		"root_info": map[string]any{
			"upstream_request_id": "upstream-private",
		},
		"audit_info": map[string]any{
			"method": "POST",
		},
	})

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{
			Id:          99,
			ChannelId:   77,
			ChannelName: "resolved-secret-channel",
			Other:       other,
		}}

		formatUserLogs(logs, 10)

		assert.Equal(t, 11, logs[0].Id)
		assert.Equal(t, 77, logs[0].ChannelId)
		assert.Empty(t, logs[0].ChannelName)
		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "/v1/chat/completions", parsed["request_path"])
		for _, key := range []string{
			"channel_id",
			"channel_name",
			"channel_type",
			"reject_reason",
			"admin_info",
			"root_info",
			"audit_info",
		} {
			assert.NotContains(t, parsed, key)
		}
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatAdminLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "legacy-secret-channel", parsed["channel_name"])
		assert.NotContains(t, parsed, "reject_reason")
		assert.NotContains(t, parsed, "root_info")
		assert.Contains(t, parsed, "audit_info")
		adminInfo, ok := parsed["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "preserved", adminInfo["existing_admin_field"])
		assert.Equal(t, "legacy-policy-rejection", adminInfo["reject_reason"])
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatRootLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "legacy-secret-channel", parsed["channel_name"])
		assert.NotContains(t, parsed, "reject_reason")
		assert.Contains(t, parsed, "root_info")
		assert.Contains(t, parsed, "audit_info")
		adminInfo, ok := parsed["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "preserved", adminInfo["existing_admin_field"])
		assert.Equal(t, "legacy-policy-rejection", adminInfo["reject_reason"])
	})
}

func TestLegacyRejectReasonDoesNotOverrideScopedValue(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"reject_reason": "legacy-value",
		"admin_info": map[string]any{
			"reject_reason": "scoped-value",
		},
	})
	logs := []*Log{{Other: other}}

	FormatRootLogs(logs)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, parsed, "reject_reason")
	adminInfo, ok := parsed["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "scoped-value", adminInfo["reject_reason"])
}

func TestLegacyRejectReasonHandlesNullAdminInfo(t *testing.T) {
	logs := []*Log{{Other: `{"reject_reason":"legacy-value","admin_info":null}`}}

	FormatAdminLogs(logs)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, parsed, "reject_reason")
	adminInfo, ok := parsed["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "legacy-value", adminInfo["reject_reason"])
}

func TestLogFormattingPreservesLargeIntegerLexemes(t *testing.T) {
	const other = `{"public_id":9007199254740993,"admin_info":{"admin_id":9007199254740995},"root_info":{"generation":18446744073709551615}}`

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		formatUserLogs(logs, 0)

		assert.Contains(t, logs[0].Other, `"public_id":9007199254740993`)
		assert.NotContains(t, logs[0].Other, "admin_id")
		assert.NotContains(t, logs[0].Other, "generation")
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatAdminLogs(logs)

		assert.Contains(t, logs[0].Other, `"public_id":9007199254740993`)
		assert.Contains(t, logs[0].Other, `"admin_id":9007199254740995`)
		assert.NotContains(t, logs[0].Other, "generation")
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatRootLogs(logs)

		assert.Equal(t, other, logs[0].Other)
	})

	t.Run("unprivileged", func(t *testing.T) {
		const unprivileged = `{"public_id":9007199254740993,"model_price":0.004}`

		userLogs := []*Log{{Other: unprivileged}}
		formatUserLogs(userLogs, 0)
		assert.Equal(t, unprivileged, userLogs[0].Other)

		adminLogs := []*Log{{Other: unprivileged}}
		FormatAdminLogs(adminLogs)
		assert.Equal(t, unprivileged, adminLogs[0].Other)
	})
}
