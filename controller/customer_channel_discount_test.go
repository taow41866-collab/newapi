package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCustomerChannelDiscountHistoryAndIsolation(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 123, Username: "discount-customer"}).Error)
	for id := 1; id <= 5; id++ {
		require.NoError(t, db.Create(&model.Channel{Id: id, Name: "test", Models: "model-a,model-b"}).Error)
	}
	rules := []model.CustomerChannelDiscount{}
	for i, multiplier := range []float64{.70, .55, .80, .60, .45} {
		rules = append(rules, model.CustomerChannelDiscount{ChannelID: i + 1, Model: "*", Multiplier: multiplier})
	}
	stored, err := model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 0, rules)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.Version)
	require.Len(t, stored.Rules, 5)
	initialTime := time.Now().UnixMilli()
	for i, want := range []float64{.70, .55, .80, .60, .45} {
		rule, err := model.ResolveCustomerChannelDiscount(t.Context(), 123, i+1, "model-a", initialTime)
		require.NoError(t, err)
		assert.Equal(t, want, rule.Multiplier)
		assert.Equal(t, int64(1), rule.Version)
	}
	for _, userID := range []int{124, 0} {
		rule, err := model.ResolveCustomerChannelDiscount(t.Context(), userID, 1, "model-a", initialTime)
		require.NoError(t, err)
		assert.Equal(t, float64(1), rule.Multiplier)
	}
	_, err = model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 0, rules[:1])
	assert.ErrorIs(t, err, model.ErrCustomerDiscountConflict)
	stored, err = model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 1, []model.CustomerChannelDiscount{{ChannelID: 1, Model: "model-a", Multiplier: .4}})
	require.NoError(t, err)
	rule, err := model.ResolveCustomerChannelDiscount(t.Context(), 123, 1, "model-a", time.Now().UnixMilli())
	require.NoError(t, err)
	assert.Equal(t, .4, rule.Multiplier)
	assert.Equal(t, "model-a", rule.Model)
	assert.Equal(t, int64(2), rule.Version)
	assert.Equal(t, 1, rule.ActorID)
	assert.GreaterOrEqual(t, rule.EffectiveAt, initialTime)
	_, err = model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 2, []model.CustomerChannelDiscount{{ChannelID: 1, Model: "model-a", Multiplier: 1, Disabled: true}})
	require.NoError(t, err)
	rule, err = model.ResolveCustomerChannelDiscount(t.Context(), 123, 1, "model-a", time.Now().UnixMilli())
	require.NoError(t, err)
	assert.Equal(t, .7, rule.Multiplier, "disabled exact rule falls back to channel default")
	assert.Equal(t, .4, stored.Rules[5].Multiplier, "previous snapshots stay immutable")
	stored, err = model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 3, []model.CustomerChannelDiscount{{ChannelID: 99, Model: "model-a", Multiplier: 1, Disabled: true}})
	require.NoError(t, err, "disabled tombstone may preserve removal of a deleted channel")
	require.Len(t, stored.Rules, 8, "deleted-channel tombstone must be recorded in history")
	tombstone := stored.Rules[7]
	assert.Equal(t, 99, tombstone.ChannelID)
	assert.Equal(t, "model-a", tombstone.Model)
	assert.True(t, tombstone.Disabled)
	assert.Equal(t, int64(4), tombstone.Version)
	assert.Equal(t, 1, tombstone.ActorID)
	assert.GreaterOrEqual(t, tombstone.EffectiveAt, initialTime)
}

func TestCustomerChannelDiscountRetryWalletAccounting(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second float64
		want          int
	}{
		{"cheaper retry charges final channel", .7, .5, 500},
		{"more expensive retry charges final channel", .5, .8, 800},
		{"unconfigured retry charges normal price", .5, 1, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupRevenueControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Token{}))
			previousRedis, previousBatch, previousConsume := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
			common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
			t.Cleanup(func() {
				common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = previousRedis, previousBatch, previousConsume
			})
			user := model.User{Username: "discount-retry", Quota: 10000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "discount-retry-test", RemainQuota: 10000, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			for id := range 2 {
				require.NoError(t, db.Create(&model.Channel{Id: id + 1, Name: "discount-retry", Models: "discount-model"}).Error)
			}
			rules := []model.CustomerChannelDiscount{{ChannelID: 1, Model: "*", Multiplier: tc.first}}
			if tc.second != 1 {
				rules = append(rules, model.CustomerChannelDiscount{ChannelID: 2, Model: "*", Multiplier: tc.second})
			}
			_, err := model.AppendCustomerChannelDiscounts(t.Context(), user.Id, 9, 0, rules)
			require.NoError(t, err)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			common.SetContextKey(ctx, constant.ContextKeyChannelId, 1)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, StartTime: time.Now(), ForcePreConsume: true,
				OriginModelName: "discount-model", UserGroup: "default", UsingGroup: "default", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1},
				PriceData:   hosttypes.PriceData{ModelRatio: 10, CompletionRatio: 1, QuotaToPreConsume: 1000, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			require.Nil(t, service.PreConsumeBilling(ctx, 1000, info))
			originalSession := info.Billing
			common.SetContextKey(ctx, constant.ContextKeyChannelId, 2)
			info.ChannelId = 2
			require.Nil(t, service.PrepareTieredBillingForSelectedGroup(ctx, info))
			require.Same(t, originalSession, info.Billing, "retry must retain the original reservation")
			service.PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 100, TotalTokens: 100}, nil)
			require.NoError(t, info.Billing.Settle(tc.want), "duplicate settlement is idempotent")
			var savedUser model.User
			require.NoError(t, db.First(&savedUser, user.Id).Error)
			assert.Equal(t, 10000-tc.want, savedUser.Quota)
			assert.Equal(t, tc.want, savedUser.UsedQuota)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, 10000-tc.want, token.RemainQuota)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, tc.want, logs[0].Quota)
			assert.Equal(t, 2, logs[0].ChannelId)
			visible, err := model.GetLogByTokenId(token.Id)
			require.NoError(t, err)
			require.Len(t, visible, 1)
			assert.NotContains(t, visible[0].Other, `"channel_id"`, "user logs must not disclose the selected upstream channel")
		})
	}
}

func TestCustomerChannelDiscountRejectsInvalidRules(t *testing.T) {
	for _, multiplier := range []float64{0, -1, 1.01, math.NaN(), math.Inf(1)} {
		assert.Error(t, model.ValidateCustomerChannelDiscounts([]model.CustomerChannelDiscount{{ChannelID: 1, Model: "*", Multiplier: multiplier}}))
	}
	assert.Error(t, model.ValidateCustomerChannelDiscounts([]model.CustomerChannelDiscount{{ChannelID: 0, Model: "*", Multiplier: 1}}))
	assert.Error(t, model.ValidateCustomerChannelDiscounts([]model.CustomerChannelDiscount{{ChannelID: 1, Model: "model-*", Multiplier: .5}}))
	assert.Error(t, model.ValidateCustomerChannelDiscounts([]model.CustomerChannelDiscount{{ChannelID: 1, Model: "*", Multiplier: .5}, {ChannelID: 1, Model: "*", Multiplier: .6}}))
}

func TestCustomerChannelDiscountCanonicalModelRequiresConfiguredIdentity(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 321, Username: "canonical-discount"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 7, Name: "canonical", Models: "qwen3-max"}).Error)
	previous := ratio_setting.GetModelRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(`{}`)
		_ = ratio_setting.UpdateModelRatioByJSONString(mustJSON(t, previous))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"qwen3-max@effort:high@thinking:on":2}`))
	_, err := model.AppendCustomerChannelDiscounts(t.Context(), 321, 9, 0, []model.CustomerChannelDiscount{{ChannelID: 7, Model: "qwen3-max@effort:high@thinking:on", Multiplier: .5}})
	require.NoError(t, err)
	_, err = model.AppendCustomerChannelDiscounts(t.Context(), 321, 9, 1, []model.CustomerChannelDiscount{{ChannelID: 7, Model: "qwen3-max@made-up:anything", Multiplier: .5}})
	assert.Error(t, err)
}

func TestCustomerChannelDiscountCanonicalExactOverridesDefault(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 322, Username: "canonical-override"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 8, Name: "canonical", Models: "qwen3-max"}).Error)
	previous := ratio_setting.GetModelRatioCopy()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(`{}`)
		_ = ratio_setting.UpdateModelRatioByJSONString(mustJSON(t, previous))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"qwen3-max@effort:high@thinking:on":2}`))
	stored, err := model.AppendCustomerChannelDiscounts(t.Context(), 322, 9, 0, []model.CustomerChannelDiscount{
		{ChannelID: 8, Model: "*", Multiplier: .8},
		{ChannelID: 8, Model: "qwen3-max@effort:high@thinking:on", Multiplier: .5},
	})
	require.NoError(t, err)
	require.Len(t, stored.Rules, 2)
	rule, err := model.ResolveCustomerChannelDiscount(t.Context(), 322, 8, "qwen3-max@effort:high@thinking:on", time.Now().UnixMilli())
	require.NoError(t, err)
	assert.Equal(t, .5, rule.Multiplier)
	assert.Equal(t, "qwen3-max@effort:high@thinking:on", rule.Model)
}

func mustJSON(t *testing.T, value map[string]float64) string {
	t.Helper()
	b, err := common.Marshal(value)
	require.NoError(t, err)
	return string(b)
}

func TestCustomerChannelDiscountAdminHandlers(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 123, Username: "discount-customer"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 1, Name: "test", Models: "model-a"}).Error)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/user/:id/customer-channel-discounts", GetCustomerChannelDiscounts)
	router.PUT("/api/user/:id/customer-channel-discounts", UpdateCustomerChannelDiscounts)
	request := httptest.NewRequest(http.MethodPut, "/api/user/123/customer-channel-discounts", strings.NewReader(`{"expected_version":0,"rules":[{"channel_id":1,"model":"*","multiplier":0.7}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: "123"}}
	ctx.Set("id", 9)
	ctx.Set("role", common.RoleRootUser)
	UpdateCustomerChannelDiscounts(ctx)
	require.Equal(t, http.StatusOK, response.Code)

	response = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/123/customer-channel-discounts", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "123"}}
	GetCustomerChannelDiscounts(ctx)
	assert.Equal(t, http.StatusOK, response.Code)
}

func TestCustomerChannelDiscountRootPermissionAndOptionBypass(t *testing.T) {
	root, token := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.Channel{}))
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Models: "model-a"}).Error)
	router := gin.New()
	router.Use(middleware.RequestId())
	router.GET("/api/user/:id/customer-channel-discounts", middleware.RootAuth(), GetCustomerChannelDiscounts)
	router.PUT("/api/user/:id/customer-channel-discounts", middleware.RootAuth(), UpdateCustomerChannelDiscounts)
	router.PUT("/api/option/", middleware.RootAuth(), UpdateOption)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(response, req)
		return response
	}
	path := "/api/user/" + strconv.Itoa(root.Id) + "/customer-channel-discounts"
	assert.Equal(t, http.StatusForbidden, request(http.MethodGet, path, "").Code)
	assert.Equal(t, http.StatusForbidden, request(http.MethodPut, path, `{"expected_version":0,"rules":[{"channel_id":1,"model":"*","multiplier":0.7}]}`).Code)
	require.NoError(t, model.DB.Model(root).Update("role", common.RoleRootUser).Error)
	assert.Equal(t, http.StatusOK, request(http.MethodPut, path, `{"expected_version":0,"rules":[{"channel_id":1,"model":"*","multiplier":0.7}]}`).Code)
	assert.Equal(t, http.StatusConflict, request(http.MethodPut, path, `{"expected_version":0,"rules":[{"channel_id":1,"model":"*","multiplier":0.5}]}`).Code)
	assert.Equal(t, http.StatusConflict, request(http.MethodPut, "/api/option/", `{"key":"CustomerChannelDiscount:`+strconv.Itoa(root.Id)+`","value":"{}"}`).Code)
	history, err := model.GetCustomerChannelDiscounts(t.Context(), root.Id)
	require.NoError(t, err)
	require.Len(t, history.Rules, 1)
	assert.Equal(t, .7, history.Rules[0].Multiplier)
}

func TestCustomerChannelDiscountDatabaseMatrix(t *testing.T) {
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			env := "TEST_MYSQL_DSN"
			if kind == "postgres" {
				env = "TEST_POSTGRES_DSN"
			}
			if kind != "sqlite" && os.Getenv(env) == "" {
				t.Skip("set " + env)
			}
			db, dsn := newAuditTestDatabase(t, kind, os.Getenv(env))
			connection, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			if kind != "sqlite" {
				query := "SELECT DATABASE()"
				var adminDialect gorm.Dialector = mysql.Open(os.Getenv(env))
				if kind == "postgres" {
					query = "SELECT current_database()"
					adminDialect = postgres.Open(os.Getenv(env))
				}
				var name string
				require.NoError(t, db.Raw(query).Scan(&name).Error)
				require.True(t, strings.HasPrefix(name, "newapi_audit_"))
				t.Cleanup(func() {
					require.NoError(t, connection.Close())
					admin, err := gorm.Open(adminDialect, &gorm.Config{})
					require.NoError(t, err)
					require.NoError(t, admin.Exec("DROP DATABASE "+name).Error)
					connection, err := admin.DB()
					require.NoError(t, err)
					require.NoError(t, connection.Close())
				})
			}
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Channel{}))
			require.NoError(t, db.Create(&model.User{Id: 123, Username: "customer"}).Error)
			require.NoError(t, db.Create(&model.Channel{Id: 1, Models: "model-a"}).Error)
			versionQuery := "SELECT version()"
			if kind == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s", version)
			start, results := make(chan struct{}), make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					cmd := exec.Command(os.Args[0], "-test.run=^TestCustomerChannelDiscountProcessHelper$", "-test.timeout=30s")
					cmd.Env = append(os.Environ(), "CUSTOMER_DISCOUNT_HELPER="+kind, "CUSTOMER_DISCOUNT_DSN="+dsn)
					output, err := cmd.CombinedOutput()
					if err != nil {
						err = fmt.Errorf("%w: %s", err, output)
					}
					results <- err
				}()
			}
			close(start)
			for range 2 {
				require.NoError(t, <-results)
			}
			var option model.Option
			require.NoError(t, db.Where(&model.Option{Key: model.CustomerChannelDiscountOptionPrefix + "123"}).First(&option).Error)
			var history model.CustomerChannelDiscountHistory
			require.NoError(t, common.UnmarshalJsonStr(option.Value, &history))
			assert.Equal(t, int64(1), history.Version)
			require.Len(t, history.Rules, 1)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Channel{}))
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Channel{}))
			var after model.Option
			require.NoError(t, db.Where(&model.Option{Key: option.Key}).First(&after).Error)
			assert.Equal(t, option.Value, after.Value)
			for _, upgraded := range []bool{false, true} {
				t.Run(fmt.Sprintf("task_snapshot_upgrade_%t", upgraded), func(t *testing.T) {
					require.NoError(t, db.AutoMigrate(&model.Task{}))
					legacy := model.Task{TaskID: fmt.Sprintf("legacy-%t", upgraded), UserId: 123, ChannelId: 1, Quota: 900, Status: model.TaskStatusInProgress, Group: "default"}
					if upgraded {
						require.NoError(t, db.Create(&legacy).Error)
						// This is the released JSON shape before customer discount
						// snapshots existed; migrations must preserve its billing.
						require.NoError(t, db.Model(&legacy).Update("private_data", `{"upstream_task_id":"upstream-old","billing_source":"wallet","token_id":7,"billing_context":{"model_price":0.06,"group_ratio":1,"model_ratio":2,"origin_model_name":"model-a","other_ratios":{"seconds":6}}}`).Error)
					}
					snapshot := &relaycommon.CustomerChannelDiscountSnapshot{UserID: 123, ChannelID: 1, Model: "model-a", Multiplier: .5, Version: 1, EffectiveAt: 1000}
					expr := `tier("video", u("seconds") * 0.06)`
					task := model.Task{TaskID: fmt.Sprintf("snapshot-%t", upgraded), UserId: 123, ChannelId: 1, Quota: 450, Status: model.TaskStatusInProgress, Group: "default",
						PrivateData: model.TaskPrivateData{BillingSource: "wallet", TokenId: 7, UpstreamTaskID: "upstream-new", BillingContext: &model.TaskBillingContext{ModelPrice: .06, ModelRatio: 2, GroupRatio: 1, OriginModelName: "model-a", OtherRatios: map[string]float64{"seconds": 6}, CustomerChannelDiscount: snapshot,
							TieredSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), TaskUsageBilling: true, QuotaPerUnit: common.QuotaPerUnit, GroupRatio: 1, UsageFacts: map[string]any{"seconds": float64(6)}}}}}
					require.NoError(t, db.Create(&task).Error)
					for range 2 {
						require.NoError(t, db.AutoMigrate(&model.Task{}))
					}
					var reloaded model.Task
					require.NoError(t, db.First(&reloaded, task.ID).Error)
					assert.Equal(t, 450, reloaded.Quota)
					assert.Equal(t, "upstream-new", reloaded.GetUpstreamTaskID())
					require.NotNil(t, reloaded.PrivateData.BillingContext)
					assert.Equal(t, snapshot, reloaded.PrivateData.BillingContext.CustomerChannelDiscount)
					assert.Equal(t, map[string]float64{"seconds": 6}, reloaded.PrivateData.BillingContext.OtherRatios)
					assert.Equal(t, expr, reloaded.PrivateData.BillingContext.TieredSnapshot.ExprString)
					assert.Equal(t, float64(6), reloaded.PrivateData.BillingContext.TieredSnapshot.UsageFacts["seconds"])
					if upgraded {
						reloaded = model.Task{}
						require.NoError(t, db.First(&reloaded, legacy.ID).Error)
						assert.Equal(t, 900, reloaded.Quota)
						assert.Equal(t, "upstream-old", reloaded.GetUpstreamTaskID())
						require.NotNil(t, reloaded.PrivateData.BillingContext)
						assert.Nil(t, reloaded.PrivateData.BillingContext.CustomerChannelDiscount)
						assert.Equal(t, .06, reloaded.PrivateData.BillingContext.ModelPrice)
						assert.Equal(t, map[string]float64{"seconds": 6}, reloaded.PrivateData.BillingContext.OtherRatios)
					}
				})
			}
		})
	}
}

func TestCustomerChannelDiscountProcessHelper(t *testing.T) {
	kind := os.Getenv("CUSTOMER_DISCOUNT_HELPER")
	if kind == "" {
		return
	}
	dsn := os.Getenv("CUSTOMER_DISCOUNT_DSN")
	var dialect gorm.Dialector
	switch kind {
	case "sqlite":
		dialect = sqlite.Open(dsn + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)")
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "mysql":
		dialect = mysql.Open(dsn)
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		dialect = postgres.Open(dsn)
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	_, err = model.AppendCustomerChannelDiscounts(t.Context(), 123, 1, 0, []model.CustomerChannelDiscount{{ChannelID: 1, Model: "*", Multiplier: .7}})
	if err != nil {
		require.ErrorIs(t, err, model.ErrCustomerDiscountConflict)
	}
	rule, err := model.ResolveCustomerChannelDiscount(t.Context(), 123, 1, "model-a", time.Now().UnixMilli())
	require.NoError(t, err)
	assert.Equal(t, .7, rule.Multiplier)
}
