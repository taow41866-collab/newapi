package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

func TestGroupSettingsPreserveReferencedIdentifiers(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "groups.db"))
			dbType := common.DatabaseTypeSQLite
			if dialect == "mysql" {
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver, dbType = mysql.Open(os.Getenv("TEST_MYSQL_DSN")), common.DatabaseTypeMySQL
			} else if dialect == "postgres" {
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver, dbType = postgres.Open(os.Getenv("TEST_POSTGRES_DSN")), common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "group_name_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousType := DB, common.MainDatabaseType()
			previousRatios := ratio_setting.GroupRatio2JSONString()
			previousNames := setting.GroupDisplayNames2JSONString()
			previousUsable := setting.UserUsableGroups2JSONString()
			previousTopup := common.TopupGroupRatio2JSONString()
			previousPolicy := CurrentRequestPolicy()
			common.OptionMapRWMutex.Lock()
			previousOptions := common.OptionMap
			common.OptionMap = map[string]string{}
			common.OptionMapRWMutex.Unlock()
			DB = db
			common.SetMainDatabaseType(dbType)
			initCol()
			models := []any{&Option{}, &User{}, &Token{}, &Channel{}, &Ability{}, &SubscriptionPlan{}, &UserSubscription{}}
			t.Cleanup(func() {
				for _, entity := range models {
					require.NoError(t, db.Migrator().DropTable(entity))
				}
				require.NoError(t, sqlDB.Close())
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
				require.NoError(t, setting.UpdateGroupDisplayNamesByJSONString(previousNames))
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
				require.NoError(t, common.UpdateTopupGroupRatioByJSONString(previousTopup))
				requestPolicySnapshot.Store(previousPolicy)
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
			})
			require.NoError(t, db.AutoMigrate(models...))
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("select sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("select version()").Scan(&version).Error)
			}
			t.Logf("%s: %s", dialect, version)
			for _, reference := range []struct {
				name string
				row  any
			}{
				{"user", &User{Username: "group-user", Group: "stable", Quota: 123}},
				{"token", &Token{Key: "group-test-token", Group: "stable", RemainQuota: 456}},
				{"auto token", &Token{Key: "group-test-auto", Group: "auto", AutoGroups: `["stable"]`}},
				{"channel", &Channel{Group: "other,stable", Key: "test", Name: "group-test"}},
				{"ability", &Ability{Group: "stable", Model: "test", ChannelId: 1}},
				{"subscription upgrade", &SubscriptionPlan{UpgradeGroup: "stable"}},
				{"subscription downgrade", &SubscriptionPlan{DowngradeGroup: "stable"}},
				{"user subscription previous", &UserSubscription{PrevUserGroup: "stable"}},
				{"user subscription upgrade", &UserSubscription{UpgradeGroup: "stable"}},
				{"user subscription downgrade", &UserSubscription{DowngradeGroup: "stable"}},
			} {
				t.Run(reference.name, func(t *testing.T) {
					require.NoError(t, UpdateOption("GroupRatio", `{"stable":0.3,"other":1}`))
					require.NoError(t, db.Create(reference.row).Error)
					for _, key := range []string{"GroupRatio", "group_ratio_setting.group_ratio"} {
						err := UpdateOption(key, `{"renamed":0.3,"other":1}`)
						require.Error(t, err, "replacing a referenced ID must not silently revoke existing access")
						assert.True(t, ratio_setting.ContainsGroupRatio("stable"))
						var persisted Option
						require.NoError(t, db.Where(Option{Key: "GroupRatio"}).First(&persisted).Error)
						assert.JSONEq(t, `{"stable":0.3,"other":1}`, persisted.Value)
					}
					require.NoError(t, db.Unscoped().Where("1 = 1").Delete(reference.row).Error)
				})
			}
			require.NoError(t, UpdateOption("GroupRatio", `{"other":1}`), "an unused ID can still be removed")
			assert.False(t, ratio_setting.ContainsGroupRatio("stable"))
			t.Run("display rename preserves access and balances", func(t *testing.T) {
				require.NoError(t, UpdateOption("GroupRatio", `{"stable":0.3,"other":1}`))
				user := User{Username: "rename-user", Group: "stable", Quota: 123}
				token := Token{Key: "rename-token", Group: "stable", RemainQuota: 456}
				channel := Channel{Group: "stable", Key: "test", Name: "rename-channel"}
				subscription := UserSubscription{UpgradeGroup: "stable", PrevUserGroup: "stable"}
				for _, row := range []any{&user, &token, &channel, &subscription} {
					require.NoError(t, db.Create(row).Error)
				}
				require.NoError(t, UpdateOption("GroupDisplayNames", `{"stable":"Premium"}`))
				assert.Equal(t, "Premium", setting.GetGroupDisplayNamesCopy()["stable"])
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				require.NoError(t, db.First(&channel, channel.Id).Error)
				require.NoError(t, db.First(&subscription, subscription.Id).Error)
				assert.Equal(t, "stable", user.Group)
				assert.Equal(t, 123, user.Quota)
				assert.Equal(t, "stable", token.Group)
				assert.Equal(t, 456, token.RemainQuota)
				assert.Equal(t, "stable", channel.Group)
				assert.Equal(t, "stable", subscription.UpgradeGroup)
				assert.Equal(t, "stable", subscription.PrevUserGroup)
				assert.InDelta(t, 0.3, ratio_setting.GetGroupRatio("stable"), 0.000001)
			})
			t.Run("invalid batch leaves all configuration unchanged", func(t *testing.T) {
				before := ratio_setting.GroupRatio2JSONString()
				for _, invalid := range []map[string]string{
					{"GroupDisplayNames": `{"stable":"other"}`},
					{"GroupDisplayNames": `{"missing":"Premium"}`},
					{"GroupDisplayNames": `null`},
					{"UserUsableGroups": `{"stable":1}`},
					{"TopupGroupRatio": `{"stable":-1}`},
				} {
					invalid["GroupRatio"] = `{"stable":0.4,"other":1}`
					require.Error(t, UpdateOptionsBulk(invalid))
					assert.JSONEq(t, before, ratio_setting.GroupRatio2JSONString())
					assert.Equal(t, "Premium", setting.GetGroupDisplayNamesCopy()["stable"])
					var persisted Option
					require.NoError(t, db.Where(Option{Key: "GroupRatio"}).First(&persisted).Error)
					assert.JSONEq(t, before, persisted.Value)
				}
				require.NoError(t, UpdateOptionsBulk(map[string]string{
					"GroupRatio": `{"stable":0.4,"other":1}`, "GroupDisplayNames": `{"stable":"Premium Plus"}`,
					"UserUsableGroups": `{"stable":"Allowed"}`, "TopupGroupRatio": `{"stable":1}`,
				}))
				assert.InDelta(t, 0.4, ratio_setting.GetGroupRatio("stable"), 0.000001)
				assert.Equal(t, "Premium Plus", setting.GetGroupDisplayNamesCopy()["stable"])
			})
			t.Run("canonical ratio wins divergent legacy alias on reload", func(t *testing.T) {
				for _, obsolete := range []string{`{"obsolete":1}`, `broken legacy JSON`} {
					require.NoError(t, db.Model(&Option{}).Where(Option{Key: "group_ratio_setting.group_ratio"}).Update("value", obsolete).Error)
					require.NoError(t, UpdateOption("GroupDisplayNames", `{"stable":"Reloaded"}`))
					loadOptionsFromDatabase()
					assert.True(t, ratio_setting.ContainsGroupRatio("stable"))
					assert.False(t, ratio_setting.ContainsGroupRatio("obsolete"))
					assert.Equal(t, "Reloaded", setting.GetGroupDisplayNamesCopy()["stable"])
					assert.Equal(t, common.OptionMap["GroupRatio"], common.OptionMap["group_ratio_setting.group_ratio"])
				}
			})
		})
	}
}
