package authz

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newAuthzTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() {
		common.IsMasterNode = wasMaster
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	return db
}

func TestInitSeedsBuiltInRolesAndPoliciesOnce(t *testing.T) {
	db := newAuthzTestDB(t)

	require.NoError(t, Init(db))
	require.NoError(t, Init(db))

	// root is a superuser role and is granted everything implicitly, so only the
	// admin baseline is written as explicit policy rows.
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Count(&count).Error)
	assert.Equal(t, int64(len(PermissionsForRole(BuiltInRoleAdmin))), count)

	var roles []model.AuthzRole
	require.NoError(t, db.Order("sort asc").Find(&roles).Error)
	require.Len(t, roles, 2)
	assert.Equal(t, BuiltInRoleRoot, roles[0].Key)
	assert.Equal(t, BuiltInRoleAdmin, roles[1].Key)

	assert.True(t, Can(1, common.RoleRootUser, ChannelSensitiveWrite))
	assert.True(t, Can(2, common.RoleAdminUser, ChannelRead))
	assert.True(t, Can(2, common.RoleAdminUser, ChannelOperate))
	assert.True(t, Can(2, common.RoleAdminUser, ChannelWrite))
	assert.False(t, Can(2, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(3, common.RoleCommonUser, ChannelRead))
}

func TestInitSeedFailureKeepsExistingPolicies(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))
	var before []model.CasbinRule
	require.NoError(t, db.Order("id").Find(&before).Error)
	require.NotEmpty(t, before)

	const callback = "test:fail-built-in-policy-seed"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "casbin_rule" {
			tx.AddError(errors.New("injected policy seed failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })

	require.ErrorContains(t, Init(db), "injected policy seed failure")
	var after []model.CasbinRule
	require.NoError(t, db.Order("id").Find(&after).Error)
	assert.Equal(t, before, after, "failed startup must not delete committed authorization policy")
}

func TestInitSeedDatabaseMatrix(t *testing.T) {
	tests := []struct {
		name      string
		dsnEnv    string
		dialector func(string) gorm.Dialector
	}{
		{name: "sqlite", dialector: func(_ string) gorm.Dialector { return sqlite.Open(":memory:") }},
		{name: "mysql", dsnEnv: "TEST_MYSQL_DSN", dialector: mysql.Open},
		{name: "postgres", dsnEnv: "TEST_POSTGRES_DSN", dialector: postgres.Open},
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
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			if test.name == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			} else {
				query := "SELECT DATABASE()"
				if test.name == "postgres" {
					query = "SELECT current_database()"
				}
				var databaseName string
				require.NoError(t, db.Raw(query).Scan(&databaseName).Error)
				if !strings.HasPrefix(databaseName, "codex_authz_") {
					t.Skip("requires a dedicated codex_authz_ test database")
				}
			}
			wasMaster := common.IsMasterNode
			common.IsMasterNode = true
			t.Cleanup(func() { common.IsMasterNode = wasMaster })
			require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))

			// Start with restricted legacy policies before any baseline exists. On
			// MySQL PAD SPACE/case-insensitive collations, SQL equality is broader
			// than the adapter's exact comparisons.
			spaceScope := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: EffectAllow, V4: " "}
			upperPtype := model.CasbinRule{Ptype: "P", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "read", V3: EffectAllow, V4: "all"}
			upperScope := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "write", V3: EffectAllow, V4: "ALL"}
			require.NoError(t, db.Create(&spaceScope).Error)
			require.NoError(t, db.Create(&upperPtype).Error)
			require.NoError(t, db.Create(&upperScope).Error)
			require.NoError(t, Init(db))
			require.NoError(t, Init(db), "restricted legacy policies must survive repeated startup")
			var restrictedCount, readBaselineCount, writeBaselineCount int64
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("id = ?", spaceScope.Id).Count(&restrictedCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ? AND v2 = ? AND v4 = ? AND v5 = ?",
				RoleSubject(BuiltInRoleAdmin), "channel", "read", "", "").Count(&readBaselineCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ? AND v2 = ? AND v4 = ? AND v5 = ?",
				RoleSubject(BuiltInRoleAdmin), "channel", "write", "", "").Count(&writeBaselineCount).Error)
			assert.Equal(t, int64(1), restrictedCount, "an unsupported scope must not be deleted by a broad SQL comparison")
			assert.False(t, Can(2, common.RoleAdminUser, ChannelOperate), "unsupported scoped policy must still deny")
			assert.Equal(t, int64(1), readBaselineCount, "an uppercase policy type is not an effective grant")
			assert.Equal(t, int64(1), writeBaselineCount, "an uppercase scope is not an effective grant")
			require.NoError(t, db.Delete(&model.CasbinRule{}, []uint{spaceScope.Id, upperPtype.Id, upperScope.Id}).Error)
			require.NoError(t, Init(db))

			require.NoError(t, Init(db))
			require.NoError(t, Init(db), "repeated startup must be idempotent")
			stale := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "obsolete", V2: "read", V3: EffectAllow}
			scoped := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: EffectAllow, V4: "own"}
			legacyAll := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "read", V3: EffectAllow, V4: "all"}
			paddedScope := model.CasbinRule{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: EffectAllow, V4: "all", V5: " "}
			require.NoError(t, db.Create(&stale).Error)
			require.NoError(t, db.Create(&scoped).Error)
			require.NoError(t, db.Create(&legacyAll).Error)
			require.NoError(t, db.Create(&paddedScope).Error)
			// A legacy policy may store the default allow effect as SQL NULL.
			require.NoError(t, db.Exec("INSERT INTO casbin_rule (ptype,v0,v1,v2,v3,v4,v5) VALUES (?,?,?,?,NULL,?,?)",
				"p", RoleSubject(BuiltInRoleAdmin), "channel", "write", "all", "").Error)
			require.NoError(t, Init(db), "upgrade must retain scoped rules and restore the built-in baseline")
			var staleCount, scopedCount, legacyAllCount, legacyNullCount, paddedScopeCount, baselineCount int64
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("id = ?", stale.Id).Count(&staleCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("id = ?", scoped.Id).Count(&scopedCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("id = ?", legacyAll.Id).Count(&legacyAllCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("id = ?", paddedScope.Id).Count(&paddedScopeCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v2 = ? AND v3 IS NULL AND v4 = ?",
				RoleSubject(BuiltInRoleAdmin), "write", "all").Count(&legacyNullCount).Error)
			require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ? AND v4 = ?", RoleSubject(BuiltInRoleAdmin), "").Count(&baselineCount).Error)
			assert.Zero(t, staleCount)
			assert.Equal(t, int64(1), scopedCount)
			assert.Equal(t, int64(1), legacyAllCount)
			assert.Equal(t, int64(1), paddedScopeCount)
			assert.Equal(t, int64(1), legacyNullCount)
			assert.Equal(t, int64(len(PermissionsForRole(BuiltInRoleAdmin))-2), baselineCount,
				"an equivalent legacy all-scope allow already supplies the baseline")
			if test.name != "sqlite" {
				readerDB, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
				require.NoError(t, err)
				readerSQL, err := readerDB.DB()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, readerSQL.Close()) })
				observed := int64(-1)
				const visibilityCallback = "test:read-policy-during-seed"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(visibilityCallback, func(tx *gorm.DB) {
					if tx.Statement.Table != "casbin_rule" {
						return
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := readerDB.WithContext(ctx).Model(&model.CasbinRule{}).
						Where("v0 = ? AND v4 = ?", RoleSubject(BuiltInRoleAdmin), "").Count(&observed).Error; err != nil {
						tx.AddError(err)
					}
				}))
				require.NoError(t, Init(db))
				assert.Equal(t, baselineCount, observed, "a separate process must not observe the deleted policy set")
				require.NoError(t, db.Callback().Create().Remove(visibilityCallback))
			}

			var before []model.CasbinRule
			require.NoError(t, db.Order("id").Find(&before).Error)
			const callback = "test:fail-policy-seed-matrix"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "casbin_rule" {
					tx.AddError(errors.New("injected policy seed failure"))
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
			require.ErrorContains(t, Init(db), "injected policy seed failure")
			var after []model.CasbinRule
			require.NoError(t, db.Order("id").Find(&after).Error)
			assert.Equal(t, before, after, "failed startup must retain the committed policy set")
		})
	}
}

func TestInitOnSlaveOnlyLoadsPolicies(t *testing.T) {
	wasMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() {
		common.IsMasterNode = wasMaster
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))

	require.NoError(t, Init(db))

	var roleCount int64
	require.NoError(t, db.Model(&model.AuthzRole{}).Count(&roleCount).Error)
	assert.Equal(t, int64(0), roleCount)
	var policyCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Count(&policyCount).Error)
	assert.Equal(t, int64(0), policyCount)
	assert.False(t, Can(2, common.RoleAdminUser, ChannelRead))
}

func TestLegacyScopedPoliciesDoNotExpandPermissions(t *testing.T) {
	for _, master := range []bool{true, false} {
		name := "master"
		if !master {
			name = "slave"
		}
		t.Run(name, func(t *testing.T) {
			db := newAuthzTestDB(t)
			common.IsMasterNode = master
			rules := []model.CasbinRule{
				{Ptype: "p", V0: "role:vendor", V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(42), V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(43), V1: "channel", V2: "sensitive_write", V3: "allow", V4: "all"},
				{Ptype: "p", V0: UserSubject(44), V1: "channel", V2: "secret_view", V3: "deny", V4: "all"},
				{Ptype: "p", V0: UserSubject(45), V1: "channel", V2: "read"},
				{Ptype: "p", V0: UserSubject(46), V1: "channel", V2: "read", V3: "allow", V4: "unknown-scope"},
				{Ptype: "p", V0: UserSubject(47), V1: "channel", V2: "read", V3: "allow", V5: "own"},
				{Ptype: "p", V0: UserSubject(48), V1: "channel", V2: "read", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(48), V1: "channel", V2: "read", V3: "allow", V4: "all"},
				{Ptype: "p", V0: RoleSubject(BuiltInRoleAdmin), V1: "channel", V2: "operate", V3: "allow", V4: "own"},
				{Ptype: "p", V0: UserSubject(50), V1: "channel", V2: "sensitive_write", V3: "allow"},
				{Ptype: "g", V0: UserSubject(99), V1: RoleSubject(BuiltInRoleAdmin)},
			}
			require.NoError(t, db.Create(&rules).Error)
			ids := make([]uint, len(rules))
			for i := range rules {
				ids[i] = rules[i].Id
			}
			for range 2 {
				require.NoError(t, Init(db))
				require.NoError(t, ReloadPolicy())
				assert.False(t, Can(42, common.RoleAdminUser, ChannelRead), "own must not fall back to the admin allow baseline")
				assert.True(t, Can(43, common.RoleAdminUser, ChannelSensitiveWrite))
				assert.False(t, Can(44, common.RoleAdminUser, ChannelSecretView))
				assert.True(t, Can(45, common.RoleAdminUser, ChannelRead))
				for _, userID := range []int{46, 47, 48} {
					assert.False(t, Can(userID, common.RoleAdminUser, ChannelRead))
				}
				assert.False(t, Can(51, common.RoleAdminUser, ChannelOperate), "reseed must not erase a scoped role restriction")
				assert.True(t, Can(50, common.RoleAdminUser, ChannelSensitiveWrite))
				assert.False(t, Can(99, common.RoleCommonUser, ChannelRead))
				var stored []model.CasbinRule
				require.NoError(t, db.Where("id IN ?", ids).Order("id").Find(&stored).Error)
				assert.Equal(t, rules, stored, "legacy rows must remain available for administrator review")
			}
		})
	}
}

func TestSetUserPermissionsStoresOnlyOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(42, PermissionsMap{
		ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          false,
			ActionSensitiveWrite: true,
			ActionSecretView:     false,
			"unknown":            true,
		},
		"unknown": {
			ActionRead: true,
		},
	}))

	assert.True(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(42, common.RoleAdminUser, ChannelWrite))
	assert.Equal(t, PermissionsMap{
		ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          false,
			ActionSensitiveWrite: true,
			ActionSecretView:     false,
		},
		ResourceTaskPlugin: {
			ActionBind: false,
		},
		ResourceAudit: {ActionRead: false},
	}, ExplicitUserPermissions(42))
	assert.Equal(t, PermissionsMap{
		ResourceChannel: {
			ActionSensitiveWrite: true,
			ActionWrite:          false,
		},
	}, ExplicitUserOverrides(42))

	var userPolicyCount int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(42)).Count(&userPolicyCount).Error)
	assert.Equal(t, int64(2), userPolicyCount)

	require.NoError(t, SetUserPermissions(42, PermissionsMap{ResourceChannel: {
		ActionRead:           true,
		ActionOperate:        true,
		ActionWrite:          true,
		ActionSensitiveWrite: false,
		ActionSecretView:     false,
	}}))
	assert.False(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.Equal(t, PermissionsMap{
		ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          true,
			ActionSensitiveWrite: false,
			ActionSecretView:     false,
		},
		ResourceTaskPlugin: {
			ActionBind: false,
		},
		ResourceAudit: {ActionRead: false},
	}, ExplicitUserPermissions(42))
	assert.Empty(t, ExplicitUserOverrides(42))
}

func TestClearUserAuthorizationRemovesOverrides(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, SetUserPermissions(90, PermissionsMap{ResourceChannel: {
		ActionWrite:          false,
		ActionSensitiveWrite: true,
	}}))

	assert.True(t, Can(90, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(90, common.RoleAdminUser, ChannelWrite))

	require.NoError(t, ClearUserAuthorization(90))

	assert.Empty(t, ExplicitUserOverrides(90))
	assert.True(t, Can(90, common.RoleAdminUser, ChannelRead))
	assert.True(t, Can(90, common.RoleAdminUser, ChannelWrite))
	assert.False(t, Can(90, common.RoleAdminUser, ChannelSensitiveWrite))
	assert.False(t, Can(90, common.RoleCommonUser, ChannelRead))
}

func TestSetUserPermissionsInTxDoesNotMutateEnforcerBeforeReload(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return SetUserPermissionsInTx(tx, 42, PermissionsMap{ResourceChannel: {
			ActionRead:           true,
			ActionOperate:        true,
			ActionWrite:          true,
			ActionSensitiveWrite: true,
			ActionSecretView:     false,
		}})
	}))

	assert.False(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
	require.NoError(t, ReloadPolicy())
	assert.True(t, Can(42, common.RoleAdminUser, ChannelSensitiveWrite))
}

func TestSetUserPermissionsInTxRollbackLeavesNoPolicy(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	tx := db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, SetUserPermissionsInTx(tx, 43, PermissionsMap{ResourceChannel: {
		ActionSensitiveWrite: true,
	}}))
	require.NoError(t, tx.Rollback().Error)
	require.NoError(t, ReloadPolicy())

	assert.False(t, Can(43, common.RoleAdminUser, ChannelSensitiveWrite))
	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", UserSubject(43)).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestAdapterAddPolicyIsIdempotent(t *testing.T) {
	db := newAuthzTestDB(t)
	adapter := newGormAdapter(db)
	rule := []string{UserSubject(55), ResourceChannel, ActionSensitiveWrite, EffectAllow}

	require.NoError(t, adapter.AddPolicy("p", "p", rule))
	require.NoError(t, adapter.AddPolicy("p", "p", rule))

	var count int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where(
		"ptype = ? AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?",
		"p",
		UserSubject(55),
		ResourceChannel,
		ActionSensitiveWrite,
		EffectAllow,
	).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestCapabilitiesUseCatalogShape(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	capabilities := Capabilities(7, common.RoleAdminUser)

	assert.True(t, capabilities[ResourceChannel][ActionRead])
	assert.True(t, capabilities[ResourceChannel][ActionOperate])
	assert.True(t, capabilities[ResourceChannel][ActionWrite])
	assert.False(t, capabilities[ResourceChannel][ActionSensitiveWrite])
	assert.False(t, capabilities[ResourceChannel][ActionSecretView])
	assert.False(t, capabilities[ResourceTaskPlugin][ActionBind])
}

func TestTaskPluginBindIsRootOnlyUntilGranted(t *testing.T) {
	db := newAuthzTestDB(t)
	require.NoError(t, Init(db))

	var bindAction *ActionDefinition
	for _, resource := range Catalog() {
		if resource.Resource != ResourceTaskPlugin {
			continue
		}
		assert.Equal(t, "Task Plugin", resource.LabelKey)
		for i := range resource.Actions {
			if resource.Actions[i].Action == ActionBind {
				bindAction = &resource.Actions[i]
			}
		}
	}
	require.NotNil(t, bindAction)
	assert.Equal(t, "Bind task plugins", bindAction.LabelKey)
	assert.Equal(t, "List registered task plugins and bind them when creating or editing task plugin channels.", bindAction.DescriptionKey)
	assert.Empty(t, bindAction.DefaultRoles)

	assert.False(t, Can(2, common.RoleAdminUser, TaskPluginBind))
	assert.True(t, Can(1, common.RoleRootUser, TaskPluginBind))

	enforcer := currentEnforcer()
	require.NotNil(t, enforcer)
	_, err := enforcer.AddPolicy(RoleSubject(BuiltInRoleAdmin), ResourceTaskPlugin, ActionBind, EffectAllow)
	require.NoError(t, err)
	assert.True(t, Can(2, common.RoleAdminUser, TaskPluginBind))

	_, err = enforcer.RemovePolicy(RoleSubject(BuiltInRoleAdmin), ResourceTaskPlugin, ActionBind, EffectAllow)
	require.NoError(t, err)
	assert.False(t, Can(2, common.RoleAdminUser, TaskPluginBind))
}
