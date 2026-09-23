package model

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGetDBTimestampFromTransactionWithSingleConnection(t *testing.T) {
	tests := []struct {
		name      string
		dbType    common.DatabaseType
		dsnEnv    string
		dialector func(string) gorm.Dialector
	}{
		{
			name:      "sqlite",
			dbType:    common.DatabaseTypeSQLite,
			dialector: func(string) gorm.Dialector { return sqlite.Open(":memory:") },
		},
		{
			name:      "mysql",
			dbType:    common.DatabaseTypeMySQL,
			dsnEnv:    "TEST_MYSQL_DSN",
			dialector: mysql.Open,
		},
		{
			name:   "postgres",
			dbType: common.DatabaseTypePostgreSQL,
			dsnEnv: "TEST_POSTGRES_DSN",
			dialector: func(dsn string) gorm.Dialector {
				return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := ""
			if test.dsnEnv != "" {
				dsn = strings.TrimSpace(os.Getenv(test.dsnEnv))
				if dsn == "" {
					t.Skip(test.dsnEnv + " is not configured")
				}
			}

			var logs bytes.Buffer
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{
				Logger: logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Error}),
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })

			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(test.dbType)
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			db = db.WithContext(ctx)
			DB = db
			tx := db.Begin()
			require.NoError(t, tx.Error)
			timestamp := getDBTimestampFrom(tx)
			require.NoError(t, tx.Rollback().Error)

			require.Greater(t, timestamp, int64(0))
			require.NoError(t, ctx.Err())
			require.Empty(t, logs.String(), "timestamp lookup must execute in the active transaction")
		})
	}
}
