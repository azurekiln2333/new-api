package model

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTokenAutoGroupsRoundTripThroughRedisHashCache(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:         42,
		UserId:     7,
		Key:        "token-auto-groups-cache-key",
		Name:       "auto-cache",
		Group:      "auto",
		AutoGroups: `["vip","default"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	groups, err := cached.GetAutoGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
}

func TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 43, UserId: 7, Key: "token-user-quota-cache-key", Name: "user-quota-cache", AllowUserQuotaQuery: true}

	generation, err := tokenCacheReadGeneration(token.Key)
	require.NoError(t, err)
	initialized, err := cacheInitToken(token, generation)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.True(t, cached.AllowUserQuotaQuery)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)))
}

func TestTokenUpdateSynchronouslyNarrowsPreheatedAutoGroupsCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:          7,
		Key:             "token-auto-groups-update-cache-key",
		Name:            "auto-cache-update",
		Status:          common.TokenStatusEnabled,
		ExpiredTime:     -1,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
		AutoGroups:      `["default","vip"]`,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.JSONEq(t, `["default","vip"]`, preheated.AutoGroups)

	require.NoError(t, token.SetAutoGroups([]string{"vip"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。缓存不再提供旧的
	// 宽分组值，下一次读取必须看到收紧后的分组。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.JSONEq(t, `["vip"]`, reloaded.AutoGroups)
}

func TestTokenKeyRotationRejectsDelayedCacheSnapshot(t *testing.T) {
	truncateTables(t)
	server := useUserCacheMiniRedis(t)
	token := Token{UserId: 7, Key: "rotation-delayed-snapshot", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))
	beforeRotation := token
	generation, err := tokenCacheReadGeneration(token.Key)
	require.NoError(t, err)
	server.FastForward(time.Duration(tokenCacheTTLSeconds()+1) * time.Second)
	replacementGeneration, err := tokenCacheReadGeneration(token.Key)
	require.NoError(t, err)
	require.NotEqual(t, generation, replacementGeneration)
	initialized, err := cacheInitToken(beforeRotation, generation)
	require.NoError(t, err)
	require.Zero(t, initialized, "an expired generation cannot authorize a delayed snapshot")
	generation = replacementGeneration

	rotated, err := RotateTokenKey(token.Id, token.UserId)
	require.NoError(t, err)
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	// Simulate a reader whose DB snapshot preceded rotation but whose cache
	// publication was delayed past the fence. A newly created generation must
	// never be mistaken for the reader's pre-rotation generation.
	newGeneration, err := tokenCacheReadGeneration(token.Key)
	require.NoError(t, err)
	require.NotEqual(t, generation, newGeneration)
	initialized, err = cacheInitToken(beforeRotation, generation)
	require.NoError(t, err)
	require.Zero(t, initialized)
	_, err = GetTokenByKey(beforeRotation.Key, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "a stale snapshot must not resurrect a revoked credential")

	server.FlushAll()
	_, err = tokenCacheReadGeneration(token.Key)
	require.NoError(t, err)
	initialized, err = cacheInitToken(beforeRotation, generation)
	require.NoError(t, err)
	require.Zero(t, initialized, "a Redis restart must not let a held snapshot recreate the revoked key")
	_, err = GetTokenByKey(beforeRotation.Key, false)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	current, err := GetTokenByKey(rotated.Key, false)
	require.NoError(t, err)
	assert.Equal(t, token.Id, current.Id)
	// Quota reservations remain authoritative in Redis when the credential is
	// current; the revocation check must not replace them with database values.
	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(rotated.Key), "RemainQuota", 30).Err())
	current, err = GetTokenByKey(rotated.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 30, current.RemainQuota)
}

// Observe the actual Redis response so the reader has captured its generation
// during the paused rotation, even when SQLite is blocked acquiring a writer
// lock before its SQL query callback can run.
type tokenGenerationReadHook struct {
	key      string
	observed chan struct{}
	once     sync.Once
}

func (*tokenGenerationReadHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *tokenGenerationReadHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	args := cmd.Args()
	if cmd.Err() == nil && cmd.Name() == "eval" && len(args) > 3 && args[3] == h.key && strings.Contains(args[1].(string), "local generation") {
		h.once.Do(func() { close(h.observed) })
	}
	return nil
}

func (*tokenGenerationReadHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*tokenGenerationReadHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestTokenKeyLifecycleDatabaseMatrix(t *testing.T) {
	for _, database := range []struct{ dialect, transactionMode string }{
		{"sqlite", "immediate"}, {"sqlite", "deferred"}, {"mysql", ""}, {"postgres", ""},
	} {
		dialect := database.dialect
		t.Run(dialect+"/"+database.transactionMode, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(t.TempDir() + "/tokens.db?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=" + database.transactionMode)
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
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Discard})
			require.NoError(t, err)
			connection, err := db.DB()
			require.NoError(t, err)
			connection.SetMaxOpenConns(4)
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				assert.NoError(t, connection.Close())
			})
			require.NoError(t, db.AutoMigrate(&Token{}))
			versionSQL := "SELECT version()"
			if dialect == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s", version)
			useUserCacheMiniRedis(t)
			if addr := os.Getenv("TEST_REDIS_ADDR"); addr != "" {
				previousClient := common.RDB
				client := redis.NewClient(&redis.Options{Addr: addr})
				require.NoError(t, client.Ping(t.Context()).Err())
				common.RDB = client
				t.Cleanup(func() {
					common.RDB = previousClient
					assert.NoError(t, client.Close())
				})
				t.Log("using configured real Redis")
			} else {
				t.Log("using miniredis; real Redis remains an integration gate")
			}
			oldKey, err := common.GenerateKey()
			require.NoError(t, err)
			token := Token{UserId: 7, Key: oldKey, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
			require.NoError(t, token.Insert())
			ids, keys := []int{token.Id}, []string{oldKey}
			t.Cleanup(func() {
				assert.NoError(t, db.Unscoped().Where("id IN ?", ids).Delete(&Token{}).Error)
				for _, key := range keys {
					assert.NoError(t, common.RDB.Del(context.Background(), getTokenCacheKey(key), getTokenCacheFenceKey(key), getTokenCacheGenerationKey(key)).Err())
				}
			})
			_, err = GetTokenByKey(oldKey, false)
			require.NoError(t, err)

			// Pause after invalidation but before the SQL write, then lose just
			// this credential's Redis state. A concurrent reader must wait for
			// commit and must never publish the old value under a fresh nonce.
			atWrite, releaseWrite := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseWrite) }) }
			t.Cleanup(release)
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("token_rotation_pause", func(tx *gorm.DB) {
				updates, ok := tx.Statement.Dest.(map[string]any)
				if _, rotatesKey := updates["key"]; ok && rotatesKey && tx.Statement.Table == "tokens" {
					close(atWrite)
					<-releaseWrite
				}
			}))
			rotationDone := make(chan error, 1)
			var rotated *Token
			go func() {
				var err error
				rotated, err = RotateTokenKey(token.Id, token.UserId)
				rotationDone <- err
			}()
			select {
			case <-atWrite:
			case err := <-rotationDone:
				t.Fatalf("rotation ended before the write barrier: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("rotation did not reach the write barrier")
			}
			require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(oldKey), getTokenCacheFenceKey(oldKey), getTokenCacheGenerationKey(oldKey)).Err())
			hook := &tokenGenerationReadHook{key: getTokenCacheGenerationKey(oldKey), observed: make(chan struct{})}
			common.RDB.AddHook(hook)
			readDone := make(chan error, 1)
			go func() {
				_, err := GetTokenByKey(oldKey, false)
				readDone <- err
			}()
			select {
			case <-hook.observed:
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not capture a replacement generation")
			}
			release()
			require.NoError(t, <-rotationDone)
			require.ErrorIs(t, <-readDone, gorm.ErrRecordNotFound)
			require.NoError(t, db.Callback().Update().Remove("token_rotation_pause"))
			keys = append(keys, rotated.Key)
			assert.Zero(t, common.RDB.Exists(t.Context(), getTokenCacheKey(oldKey)).Val())
			current, err := GetTokenByKey(rotated.Key, false)
			require.NoError(t, err)
			assert.Equal(t, token.Id, current.Id)

		})
	}
}

// cacheSetTokenForTest 以测试身份写入完整 token 缓存（含额度字段），
// 模拟“已水合”的缓存状态。
func cacheSetTokenForTest(token Token) error {
	generation, err := tokenCacheReadGeneration(token.Key)
	if err != nil {
		return err
	}
	_, err = cacheInitToken(token, generation)
	return err
}
