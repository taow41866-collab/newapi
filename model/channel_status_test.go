package model

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupChannelStatusTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
	})
}

func TestUpdateChannelStatusPersistsMultiKeyState(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "multi-key-status",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, "provider rejected key", stored.ChannelInfo.MultiKeyDisabledReason[0])
	assert.NotZero(t, stored.ChannelInfo.MultiKeyDisabledTime[0])
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
}

func TestSaveStatusStateFromSingleKeySnapshotPreservesUnownedColumns(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:        "single-key-status",
		Key:         "original-key",
		Status:      common.ChannelStatusEnabled,
		Models:      "original-model",
		Group:       "default",
		UsedQuota:   100,
		ChannelInfo: ChannelInfo{},
	}
	require.NoError(t, DB.Create(&channel).Error)

	stale, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)

	concurrentChannelInfo := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         2,
		MultiKeyMode:         constant.MultiKeyModePolling,
		MultiKeyPollingIndex: 1,
	}
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"key":          "rotated-key",
		"used_quota":   gorm.Expr("used_quota + ?", 250),
		"models":       "concurrent-model",
		"channel_info": concurrentChannelInfo,
	}).Error)

	stale.Status = common.ChannelStatusManuallyDisabled
	stale.SetOtherInfo(map[string]any{
		"status_reason": "manual operation",
		"status_time":   int64(1234),
	})
	require.NoError(t, stale.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(350), stored.UsedQuota)
	assert.Equal(t, "concurrent-model", stored.Models)
	assert.Equal(t, concurrentChannelInfo, stored.ChannelInfo)

	otherInfo := stored.GetOtherInfo()
	assert.Equal(t, "manual operation", otherInfo["status_reason"])
	assert.Equal(t, float64(1234), otherInfo["status_time"])
}

func TestChannelProbeWeightDegradesAndRestoresAcrossStoredOutcomes(t *testing.T) {
	setupChannelStatusTest(t)

	weight := uint(80)
	channel := Channel{
		Name:   "probe-weight",
		Key:    "probe-key",
		Status: common.ChannelStatusEnabled,
		Weight: &weight,
		Models: "gpt-test",
		Group:  "default",
	}
	channel.SetOtherInfo(map[string]any{"probe-note": "keep"})
	require.NoError(t, channel.Insert())

	ready, err := UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, 80, mustGetChannelWeight(t, channel.Id))

	ready, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, 0, mustGetChannelWeight(t, channel.Id))
	assert.Equal(t, uint(0), mustGetAbilityWeight(t, channel.Id))

	ready, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, 0, mustGetChannelWeight(t, channel.Id))

	ready, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, 80, mustGetChannelWeight(t, channel.Id))
	assert.Equal(t, uint(80), mustGetAbilityWeight(t, channel.Id))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "keep", stored.GetOtherInfo()["probe-note"])
}

func TestChannelProbeWeightUsesManualWeightChangeAsNewBaseline(t *testing.T) {
	setupChannelStatusTest(t)

	weight := uint(80)
	channel := Channel{
		Name:   "probe-weight-manual-update",
		Key:    "probe-key",
		Status: common.ChannelStatusEnabled,
		Weight: &weight,
		Models: "gpt-test",
		Group:  "default",
	}
	require.NoError(t, channel.Insert())
	_, err := UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	_, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)

	manualWeight := uint(35)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("weight", manualWeight).Error)
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("weight", manualWeight).Error)
	_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
	require.NoError(t, err)
	_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
	require.NoError(t, err)

	assert.Equal(t, 35, mustGetChannelWeight(t, channel.Id))
	assert.Equal(t, uint(35), mustGetAbilityWeight(t, channel.Id))
}

func TestChannelProbeWeightResetsOnOppositeOutcomeAndCanBeDisabled(t *testing.T) {
	setupChannelStatusTest(t)

	weight := uint(80)
	channel := Channel{Name: "probe-weight-reset", Key: "probe-key", Status: common.ChannelStatusEnabled, Weight: &weight, Models: "gpt-test", Group: "default"}
	require.NoError(t, channel.Insert())

	_, err := UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
	require.NoError(t, err)
	_, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	assert.Equal(t, 80, mustGetChannelWeight(t, channel.Id))

	_, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
	require.NoError(t, err)
	assert.Equal(t, 0, mustGetChannelWeight(t, channel.Id))

	_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, false, false)
	require.NoError(t, err)
	assert.Equal(t, 80, mustGetChannelWeight(t, channel.Id))
	stored, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.NotContains(t, stored.GetOtherInfo(), channelProbeWeightStateKey)
}

func TestChannelProbeWeightRequiresRecoveryThresholdForAutoDisabledChannels(t *testing.T) {
	setupChannelStatusTest(t)

	weight := uint(25)
	channel := Channel{Name: "probe-weight-recovery", Key: "probe-key", Status: common.ChannelStatusAutoDisabled, Weight: &weight, Models: "gpt-test", Group: "default"}
	require.NoError(t, channel.Insert())

	ready, err := UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, true)
	require.NoError(t, err)
	assert.False(t, ready)
	ready, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, true)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, 25, mustGetChannelWeight(t, channel.Id))
}

func TestChannelProbeWeightSQLDialects(t *testing.T) {
	tests := []struct {
		name      string
		dbType    common.DatabaseType
		dsnEnv    string
		dialector func(string) gorm.Dialector
	}{
		{
			name:   "sqlite",
			dbType: common.DatabaseTypeSQLite,
			dialector: func(_ string) gorm.Dialector {
				return sqlite.Open(filepath.Join(t.TempDir(), "probe-weight.db"))
			},
		},
		{name: "mysql", dbType: common.DatabaseTypeMySQL, dsnEnv: "TEST_MYSQL_DSN", dialector: mysql.Open},
		{name: "postgres", dbType: common.DatabaseTypePostgreSQL, dsnEnv: "TEST_POSTGRES_DSN", dialector: postgres.Open},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := ""
			if test.dsnEnv != "" {
				dsn = os.Getenv(test.dsnEnv)
				if dsn == "" {
					t.Skip(test.dsnEnv + " is not configured")
				}
			}
			testDB, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := testDB.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, testDB.AutoMigrate(&Channel{}, &Ability{}))

			previousDB, previousType := DB, common.MainDatabaseType()
			previousMemoryCache := common.MemoryCacheEnabled
			DB = testDB
			common.MemoryCacheEnabled = false
			common.SetMainDatabaseType(test.dbType)
			t.Cleanup(func() {
				DB = previousDB
				common.MemoryCacheEnabled = previousMemoryCache
				common.SetMainDatabaseType(previousType)
			})

			weight := uint(60)
			channel := Channel{
				Name:   fmt.Sprintf("probe-weight-%s-%d", test.name, time.Now().UnixNano()),
				Key:    "probe-key",
				Status: common.ChannelStatusEnabled,
				Weight: &weight,
				Models: "gpt-test",
				Group:  "default",
			}
			require.NoError(t, channel.Insert())
			t.Cleanup(func() {
				require.NoError(t, testDB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error)
				require.NoError(t, testDB.Delete(&Channel{}, channel.Id).Error)
			})

			_, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
			require.NoError(t, err)
			_, err = UpdateChannelProbeWeight(channel.Id, false, 2, 2, true, false)
			require.NoError(t, err)
			assert.Equal(t, 0, mustGetChannelWeight(t, channel.Id))
			_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
			require.NoError(t, err)
			_, err = UpdateChannelProbeWeight(channel.Id, true, 2, 2, true, false)
			require.NoError(t, err)
			assert.Equal(t, 60, mustGetChannelWeight(t, channel.Id))
		})
	}
}

func mustGetChannelWeight(t *testing.T, channelID int) int {
	t.Helper()
	channel, err := GetChannelById(channelID, true)
	require.NoError(t, err)
	return channel.GetWeight()
}

func mustGetAbilityWeight(t *testing.T, channelID int) uint {
	t.Helper()
	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channelID).First(&ability).Error)
	return ability.Weight
}
