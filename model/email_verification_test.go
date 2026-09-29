package model

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestSharedEmailVerification(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "verification.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	var driver gorm.Dialector = sqlite.Open(dsn)
	switch os.Getenv("TEST_EMAIL_DIALECT") {
	case "mysql":
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case "postgres":
		driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
	}
	a, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	b, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	for _, db := range []*gorm.DB{a, b} {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.AutoMigrate(&EmailVerification{}))
	}
	t.Run("migration preserves outstanding challenge", func(t *testing.T) {
		require.NoError(t, StoreEmailVerification(a, "migration@qq.com", "v", "abcdef", time.Minute))
		require.NoError(t, a.AutoMigrate(&EmailVerification{}))
		require.NoError(t, b.AutoMigrate(&EmailVerification{}))
		ok, err := ConsumeEmailVerification(b, " MIGRATION@qq.com ", "v", "abcdef")
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("cross node and single use", func(t *testing.T) {
		require.NoError(t, StoreEmailVerification(a, "user@qq.com", "v", "07439d", time.Minute))
		ok, err := ConsumeEmailVerification(b, "user@qq.com", "v", "07439d")
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = ConsumeEmailVerification(a, "user@qq.com", "v", "07439d")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("purpose expiry overwrite and attempts", func(t *testing.T) {
		require.NoError(t, StoreEmailVerification(a, "user@qq.com", "v", "old", time.Minute))
		require.NoError(t, StoreEmailVerification(a, "user@qq.com", "v", "new", time.Minute))
		for _, purpose := range []string{"r", "v", "v", "v", "v", "v"} {
			ok, err := ConsumeEmailVerification(b, "user@qq.com", purpose, "old")
			require.NoError(t, err)
			assert.False(t, ok)
		}
		ok, err := ConsumeEmailVerification(a, "user@qq.com", "v", "new")
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, StoreEmailVerification(a, "expired@qq.com", "v", "new", time.Minute))
		require.NoError(t, a.Model(&EmailVerification{}).Where("email = ?", "expired@qq.com").Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
		ok, err = ConsumeEmailVerification(b, "expired@qq.com", "v", "new")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("concurrent consumers", func(t *testing.T) {
		require.NoError(t, StoreEmailVerification(a, "race@qq.com", "v", "abcdef", time.Minute))
		var winners atomic.Int32
		var wg sync.WaitGroup
		for _, db := range []*gorm.DB{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := ConsumeEmailVerification(db, "race@qq.com", "v", "abcdef")
				assert.NoError(t, err)
				if ok {
					winners.Add(1)
				}
			}()
		}
		wg.Wait()
		assert.EqualValues(t, 1, winners.Load())
	})
	t.Run("wrong attempt does not consume and purpose is isolated", func(t *testing.T) {
		require.NoError(t, StoreEmailVerification(a, "isolation@qq.com", "v", "abcdef", time.Minute))
		require.NoError(t, StoreEmailVerification(b, "isolation@qq.com", "r", "fedcba", time.Minute))
		ok, err := ConsumeEmailVerification(b, "isolation@qq.com", "v", "wrong")
		require.NoError(t, err)
		assert.False(t, ok)
		ok, err = ConsumeEmailVerification(b, "isolation@qq.com", "v", "abcdef")
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = ConsumeEmailVerification(a, "isolation@qq.com", "r", "fedcba")
		require.NoError(t, err)
		assert.True(t, ok)
		var row EmailVerification
		require.NoError(t, a.Where("email = ?", "isolation@qq.com").Take(&row).Error)
		assert.NotContains(t, row.CodeHash, "abcdef")
		assert.NotContains(t, row.CodeHash, "fedcba")
	})
	t.Run("database failure cannot authorize", func(t *testing.T) {
		broken, err := gorm.Open(driver, &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := broken.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		ok, err := ConsumeEmailVerification(broken, "user@qq.com", "v", "07439d")
		require.Error(t, err)
		assert.False(t, ok)
		require.Error(t, StoreEmailVerification(broken, "user@qq.com", "v", "07439d", time.Minute))
	})
}
