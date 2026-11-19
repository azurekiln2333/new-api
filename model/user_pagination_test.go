package model

import (
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func insertUsersForPaginationTest(t *testing.T, total int) {
	t.Helper()
	for id := 1; id <= total; id++ {
		user := &User{
			Id:          id,
			Username:    fmt.Sprintf("user%02d", id),
			Password:    "password123",
			DisplayName: fmt.Sprintf("User %02d", id),
			Email:       fmt.Sprintf("user%02d@example.com", id),
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AffCode:     fmt.Sprintf("aff%02d", id),
		}
		require.NoError(t, DB.Create(user).Error)
	}
}

func collectUserIDs(users []*User) []int {
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.Id)
	}
	return ids
}

func TestGetAllUsersSortsBeforePagination(t *testing.T) {
	truncateTables(t)
	insertUsersForPaginationTest(t, 42)

	pageOne, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 20}, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, collectUserIDs(pageOne))

	pageTwo, total, err := GetAllUsers(&common.PageInfo{Page: 2, PageSize: 20}, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40}, collectUserIDs(pageTwo))

	pageThree, total, err := GetAllUsers(&common.PageInfo{Page: 3, PageSize: 20}, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{41, 42}, collectUserIDs(pageThree))
}

func TestSearchUsersSortsBeforePagination(t *testing.T) {
	truncateTables(t)
	insertUsersForPaginationTest(t, 42)

	users, total, err := SearchUsers("user", "", nil, nil, 20, 20, NewUserSortOptions("id", "asc"))
	require.NoError(t, err)
	assert.Equal(t, int64(42), total)
	assert.Equal(t, []int{21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40}, collectUserIDs(users))
}

func TestUserUsedQuotaSortingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "user_sort_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				assert.NoError(t, db.Migrator().DropTable(&User{}))
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&User{}))
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			users := make([]User, 8)
			for i, quota := range []int{50, 100, 50, 0, 999, 999, 999, 999} {
				users[i] = User{
					Username:  fmt.Sprintf("match-%d", i),
					Password:  "sort-test-password",
					UsedQuota: quota,
					Group:     "target",
					Role:      common.RoleCommonUser,
					Status:    common.UserStatusEnabled,
					AffCode:   fmt.Sprintf("sort-%d", i),
				}
			}
			users[4].Group = "other"
			users[5].Role = common.RoleAdminUser
			users[6].Status = common.UserStatusDisabled
			users[7].Username = "outside-keyword"
			require.NoError(t, db.Create(&users).Error)
			for _, tc := range []struct {
				order    string
				all      []int
				filtered []int
			}{
				{"asc", []int{3, 2, 0, 1, 7, 6, 5, 4}, []int{3, 2, 0, 1}},
				{"desc", []int{7, 6, 5, 4, 1, 2, 0, 3}, []int{1, 2, 0, 3}},
			} {
				t.Run(tc.order, func(t *testing.T) {
					for page := range 4 {
						got, total, err := GetAllUsers(&common.PageInfo{Page: page + 1, PageSize: 2}, NewUserSortOptions("used_quota", tc.order))
						require.NoError(t, err)
						assert.Equal(t, int64(8), total)
						assert.Equal(t, []int{users[tc.all[page*2]].Id, users[tc.all[page*2+1]].Id}, collectUserIDs(got))
					}
					role, status := common.RoleCommonUser, common.UserStatusEnabled
					for page := range 2 {
						got, total, err := SearchUsers("match", "target", &role, &status, page*2, 2, NewUserSortOptions("used_quota", tc.order))
						require.NoError(t, err)
						assert.Equal(t, int64(4), total)
						assert.Equal(t, []int{users[tc.filtered[page*2]].Id, users[tc.filtered[page*2+1]].Id}, collectUserIDs(got))
					}
				})
			}
			fallback, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 2}, NewUserSortOptions("used_quota desc; DROP TABLE users", "asc"))
			require.NoError(t, err)
			assert.Equal(t, int64(8), total)
			assert.Equal(t, []int{users[7].Id, users[6].Id}, collectUserIDs(fallback))
		})
	}
}
