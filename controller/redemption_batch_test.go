package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Redemption schema from v1.0.0-rc.49, the latest release when this migration
// was introduced. Keep this fixture independent of the current model.
type legacyBatchRedemption struct {
	Id           int
	UserId       int
	Key          string `gorm:"type:char(32);uniqueIndex"`
	Status       int    `gorm:"default:1"`
	Name         string `gorm:"index"`
	Quota        int    `gorm:"default:100"`
	CreatedTime  int64  `gorm:"bigint"`
	RedeemedTime int64  `gorm:"bigint"`
	UsedUserId   int
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `gorm:"bigint"`
}

func (legacyBatchRedemption) TableName() string { return "redemptions" }

func TestRedemptionBatchLimit(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/upgrade=%t", dialect, upgrade), func(t *testing.T) {
				var driver gorm.Dialector
				dbType := common.DatabaseTypeSQLite
				switch dialect {
				case "sqlite":
					driver = sqlite.Open(filepath.Join(t.TempDir(), "batch.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
				case "mysql":
					dsn := os.Getenv("TEST_MYSQL_DSN")
					if dsn == "" {
						t.Skip("TEST_MYSQL_DSN is not configured")
					}
					driver, dbType = mysql.Open(dsn), common.DatabaseTypeMySQL
				case "postgres":
					dsn := os.Getenv("TEST_POSTGRES_DSN")
					if dsn == "" {
						t.Skip("TEST_POSTGRES_DSN is not configured")
					}
					driver, dbType = postgres.Open(dsn), common.DatabaseTypePostgreSQL
				}
				db, err := gorm.Open(driver, &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(4)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
				var version string
				query := "SELECT version()"
				if dialect == "sqlite" {
					query = "SELECT sqlite_version()"
				}
				require.NoError(t, db.Raw(query).Scan(&version).Error)
				t.Logf("database: %s", version)
				previousDB, previousLogDB := model.DB, model.LOG_DB
				previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
				previousRedis := common.RedisEnabled
				model.DB, model.LOG_DB = db, db
				common.SetDatabaseTypes(dbType, dbType)
				common.RedisEnabled = false
				t.Cleanup(func() {
					model.DB, model.LOG_DB = previousDB, previousLogDB
					common.SetDatabaseTypes(previousMain, previousLog)
					common.RedisEnabled = previousRedis
				})
				for _, table := range []any{&model.User{}, &model.Redemption{}, &model.RedemptionBatchClaim{}, &model.Log{}, &model.AuditLog{}} {
					require.False(t, db.Migrator().HasTable(table), "use an empty test database")
					t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
				}
				legacy := []legacyBatchRedemption{
					{Key: "00000000000000000000000000000001", Name: "legacy", Quota: 100, Status: common.RedemptionCodeStatusEnabled},
					{Key: "00000000000000000000000000000002", Name: "legacy", Quota: 200, Status: common.RedemptionCodeStatusUsed, UsedUserId: 99, CreatedTime: 100, RedeemedTime: 200, ExpiredTime: 300},
				}
				if upgrade {
					require.NoError(t, db.AutoMigrate(&legacyBatchRedemption{}))
					require.NoError(t, db.Create(&legacy).Error)
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(&model.User{}, &model.Redemption{}, &model.RedemptionBatchClaim{}, &model.Log{}, &model.AuditLog{}))
				}
				if upgrade {
					var preserved []legacyBatchRedemption
					require.NoError(t, db.Order("id").Find(&preserved).Error)
					assert.Equal(t, legacy, preserved)
					duplicate := model.Redemption{Key: legacy[0].Key, Name: "duplicate", Quota: 100}
					require.Error(t, duplicate.Insert(), "legacy code uniqueness must survive migration")
				}
				user := model.User{Username: "batch-user", AffCode: "batch-u", Quota: 0}
				other := model.User{Username: "batch-other", AffCode: "batch-o", Quota: 0}
				require.NoError(t, db.Create(&user).Error)
				require.NoError(t, db.Create(&other).Error)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set("id", user.Id)
					c.Set("role", common.RoleAdminUser)
					c.Set("username", user.Username)
				})
				router.POST("/", AddRedemption)
				router.PUT("/", UpdateRedemption)
				// Every generation has its own server-selected batch, even if the
				// name or a client-supplied batch ID is reused.
				var batches [][]model.Redemption
				for _, limited := range []bool{true, true, false} {
					body, err := common.Marshal(map[string]any{"name": "campaign", "count": 4, "quota": 100, "limit_per_user": limited, "batch_id": "untrusted"})
					require.NoError(t, err)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
					var result struct {
						Success bool
						Data    []string
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					require.True(t, result.Success, response.Body.String())
					require.Len(t, result.Data, 4)
					var codes []model.Redemption
					require.NoError(t, db.Order("id desc").Limit(4).Find(&codes).Error)
					for _, code := range codes {
						assert.Equal(t, codes[0].BatchId, code.BatchId)
						assert.NotEqual(t, "untrusted", code.BatchId)
						assert.Equal(t, limited, code.BatchId != "")
					}
					batches = append(batches, codes)
				}
				assert.NotEqual(t, batches[0][0].BatchId, batches[1][0].BatchId)
				codes := batches[0]
				quota, err := model.Redeem(codes[0].Key, user.Id)
				require.NoError(t, err)
				assert.Equal(t, 100, quota)
				_, err = model.Redeem(codes[1].Key, user.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				var available model.Redemption
				require.NoError(t, db.First(&available, codes[1].Id).Error)
				assert.Equal(t, common.RedemptionCodeStatusEnabled, available.Status)
				assert.Zero(t, available.UsedUserId)
				_, err = model.Redeem(codes[1].Key, other.Id)
				require.NoError(t, err, "another user can redeem the same batch")
				_, err = model.Redeem(codes[1].Key, other.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed, "each code remains single-use")
				_, err = model.Redeem(batches[1][0].Key, user.Id)
				require.NoError(t, err, "same user can redeem another batch")
				for _, code := range batches[2][:2] {
					_, err = model.Redeem(code.Key, user.Id)
					require.NoError(t, err, "unrestricted codes retain existing behavior")
				}
				if upgrade {
					_, err = model.Redeem(legacy[0].Key, user.Id)
					require.NoError(t, err, "old codes remain unrestricted")
				}
				// Editing/renaming and deleting a consumed code cannot reset a batch.
				body, err := common.Marshal(map[string]any{"id": codes[2].Id, "name": "renamed", "quota": 100, "batch_id": "", "limit_per_user": false})
				require.NoError(t, err)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body)))
				var updated struct {
					Success bool
					Data    model.Redemption
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &updated))
				require.True(t, updated.Success)
				assert.Equal(t, codes[2].BatchId, updated.Data.BatchId)
				require.NoError(t, model.DeleteRedemptionById(codes[0].Id))
				_, err = model.Redeem(codes[2].Key, user.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				// Failed quota credit must roll back both the claim and code.
				full := model.User{Username: "batch-full", AffCode: "batch-f", Quota: common.MaxWalletQuota - 50}
				require.NoError(t, db.Create(&full).Error)
				_, err = model.Redeem(codes[2].Key, full.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				var claims int64
				require.NoError(t, db.Model(&model.RedemptionBatchClaim{}).Where("user_id = ?", full.Id).Count(&claims).Error)
				assert.Zero(t, claims)
				require.NoError(t, db.Model(&full).Update("quota", 0).Error)
				_, err = model.Redeem(codes[2].Key, full.Id)
				require.NoError(t, err)
				// Independent calls on separate connections simulate gateway nodes;
				// different codes from the same batch may credit only once.
				racer := model.User{Username: "batch-concurrent", AffCode: "batch-c", Quota: 0}
				require.NoError(t, db.Create(&racer).Error)
				// Expired codes must not consume the user's allowance.
				expired := batches[1][3]
				require.NoError(t, db.Model(&expired).Update("expired_time", common.GetTimestamp()-1).Error)
				_, err = model.Redeem(expired.Key, racer.Id)
				require.ErrorIs(t, err, model.ErrRedeemFailed)
				require.NoError(t, db.Model(&model.RedemptionBatchClaim{}).Where("user_id = ?", racer.Id).Count(&claims).Error)
				assert.Zero(t, claims)
				start := make(chan struct{})
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for _, code := range batches[1][1:3] {
					wg.Go(func() {
						<-start
						_, redeemErr := model.Redeem(code.Key, racer.Id)
						results <- redeemErr
					})
				}
				close(start)
				wg.Wait()
				close(results)
				successes := 0
				for redeemErr := range results {
					if redeemErr == nil {
						successes++
					} else {
						assert.ErrorIs(t, redeemErr, model.ErrRedeemFailed)
					}
				}
				assert.Equal(t, 1, successes)
				require.NoError(t, db.First(&racer, racer.Id).Error)
				assert.Equal(t, 100, racer.Quota)
				require.NoError(t, db.Model(&model.RedemptionBatchClaim{}).Where("user_id = ?", racer.Id).Count(&claims).Error)
				assert.EqualValues(t, 1, claims)
				// A subsequent startup must preserve already claimed allowances.
				require.NoError(t, db.AutoMigrate(&model.Redemption{}, &model.RedemptionBatchClaim{}))
				duplicateClaim := model.RedemptionBatchClaim{BatchId: batches[1][0].BatchId, UserId: racer.Id}
				require.Error(t, db.Create(&duplicateClaim).Error, "unique claim must be enforced by the database")
			})
		}
	}
}

func TestDeleteRedemptionBatch(t *testing.T) {
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver, logDriver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
				logDriver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				logDSN := os.Getenv("TEST_MYSQL_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = mysql.Open(logDSN)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				logDSN := os.Getenv("TEST_POSTGRES_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = postgres.Open(logDSN)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			logSQL.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dbType, dbType)
			common.RedisEnabled = false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
			})
			for _, table := range []any{&model.User{}, &model.Redemption{}, &model.Option{}, &model.CasbinRule{}, &model.AuthzRole{}} {
				require.False(t, db.Migrator().HasTable(table), "use an empty test database")
				require.NoError(t, db.AutoMigrate(table))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
			}
			// Initialize the same token transition and authorization state as
			// startup so this test does not depend on other tests running first.
			require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(common.GetTimestamp()))
			require.NoError(t, authz.Init(db))
			require.False(t, logDB.Migrator().HasTable(&model.AuditLog{}), "use an empty test log database")
			require.NoError(t, logDB.AutoMigrate(&model.AuditLog{}))
			t.Cleanup(func() { require.NoError(t, logDB.Migrator().DropTable(&model.AuditLog{})) })
			token := "redemption-audit-test-token"
			admin := model.User{Username: "redemption-audit-admin", Password: "unused", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AccessToken: &token}
			require.NoError(t, db.Create(&admin).Error)
			codes := make([]model.Redemption, 16)
			for index := range codes {
				codes[index] = model.Redemption{Name: "selected", Key: fmt.Sprintf("%032d", index+1), Quota: 100, Status: common.RedemptionCodeStatusEnabled}
			}
			codes[1].Status = common.RedemptionCodeStatusUsed
			codes[15].Name = "unselected"
			codes[15].Status = common.RedemptionCodeStatusDisabled
			require.NoError(t, model.DB.Create(&codes).Error)
			router := gin.New()
			router.Use(middleware.RequestId())
			router.POST("/api/redemption/batch", middleware.AdminAuth(), DeleteRedemptionBatch)

			overLimit := make([]int, 1001)
			for index := range overLimit {
				overLimit[index] = codes[0].Id
			}
			oversized, err := common.Marshal(map[string]any{"ids": overLimit})
			require.NoError(t, err)
			for _, body := range []string{"{}", `{"ids":[]}`, `{"ids":null}`, `{"ids":[0]}`, `{"ids":[1,-1]}`, `{"ids":["1"]}`, "{", string(oversized)} {
				t.Run("invalid_"+body[:min(len(body), 30)], func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewBufferString(body))
					request.Header.Set("Authorization", "Bearer "+token)
					router.ServeHTTP(response, request)
					var result struct {
						Success bool `json:"success"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					assert.False(t, result.Success)
					var count int64
					require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&count).Error)
					assert.EqualValues(t, 16, count)
					var events []model.AuditLog
					require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
					require.Len(t, events, 1)
					assert.False(t, events[0].Success)
					assert.Equal(t, "redemption.delete_batch", events[0].Action)
				})
			}
			_, err = model.BatchDeleteRedemptions(nil)
			require.Error(t, err)
			requestedIDs := make([]int, 0, 17)
			for _, code := range codes[:15] {
				requestedIDs = append(requestedIDs, code.Id)
			}
			requestedIDs = append(requestedIDs, codes[0].Id, 999999)
			payload, err := common.Marshal(map[string]any{"ids": requestedIDs})
			require.NoError(t, err)
			for _, expectedCount := range []int64{15, 0} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewReader(payload))
				request.Header.Set("Authorization", "Bearer "+token)
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool  `json:"success"`
					Data    int64 `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.True(t, result.Success)
				assert.Equal(t, expectedCount, result.Data)
				var events []model.AuditLog
				require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
				require.Len(t, events, 1, "one operation event, without a duplicate single-delete fallback")
				event := events[0]
				assert.Equal(t, "redemption.delete_batch", event.Action)
				assert.Equal(t, fmt.Sprintf("Batch deleted %d redemption codes", expectedCount), event.Content)
				assert.True(t, event.Success)
				assert.Equal(t, admin.Id, event.UserId)
				assert.Equal(t, "/api/redemption/batch", event.Route)
				require.NotNil(t, event.Other.Op)
				encoded, err := common.Marshal(event.Other.Op.Params)
				require.NoError(t, err)
				var params struct {
					Count int64 `json:"count"`
					Total int   `json:"total"`
					IDs   []int `json:"requested_redemption_ids"`
				}
				require.NoError(t, common.Unmarshal(encoded, &params))
				assert.Equal(t, expectedCount, params.Count)
				assert.Equal(t, len(requestedIDs), params.Total)
				assert.Equal(t, requestedIDs, params.IDs)
				encoded, err = common.Marshal(event)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), token)
				for _, code := range codes {
					assert.NotContains(t, string(encoded), code.Key)
				}
			}
			var active []model.Redemption
			require.NoError(t, model.DB.Find(&active).Error)
			require.Len(t, active, 1)
			assert.Equal(t, codes[15], active[0])
			var all []model.Redemption
			require.NoError(t, model.DB.Unscoped().Order("id").Find(&all).Error)
			require.Len(t, all, 16)
			for _, code := range all[:15] {
				assert.True(t, code.DeletedAt.Valid)
			}
			assert.False(t, all[15].DeletedAt.Valid)
		})
	}
}
