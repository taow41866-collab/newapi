package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRevenueControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousOptions := common.OptionMap
	previousQuotaPerUnit := common.QuotaPerUnit
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	common.OptionMap = map[string]string{}
	common.QuotaPerUnit = 100
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.OptionMap = previousOptions
		common.QuotaPerUnit = previousQuotaPerUnit
	})
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Option{}))
	return db
}

func TestCalculateRevenueUsesConsumeMinusRefundAndLeavesMissingCostProfitNull(t *testing.T) {
	setupRevenueControllerTestDB(t)
	logs := []model.Log{
		{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000, RequestId: "req-1"},
		{CreatedAt: 101, Type: model.LogTypeRefund, ChannelId: 7, ModelName: "gpt-test", Quota: 200, RequestId: "req-1"},
	}
	report := model.CalculateRevenue(logs, nil, nil, 100)
	require.Len(t, report.Rows, 1)
	row := report.Rows[0]
	assert.Equal(t, float64(8), row.NetSales)
	assert.Nil(t, row.GrossProfit)
	assert.Equal(t, int64(1), row.Entries)
}

func TestCalculateRevenueMatchesExactChannelModelAndEffectivePrice(t *testing.T) {
	logs := []model.Log{{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000}, {CreatedAt: 200, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000}, {CreatedAt: 201, Type: model.LogTypeRefund, ChannelId: 7, ModelName: "gpt-test", Quota: 100}}
	prices := []model.PurchasePrice{{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "request", UnitPrice: ptr(2), Source: "invoice"}, {ChannelID: 7, Model: "gpt-test", EffectiveAt: 150, Unit: "request", UnitPrice: ptr(3), Source: "invoice"}, {ChannelID: 7, Model: "gpt-*", EffectiveAt: 0, Unit: "request", UnitPrice: ptr(100), Source: "invoice"}}
	report := model.CalculateRevenue(logs, prices, nil, 100)
	assert.Equal(t, float64(19), report.NetSales)
	assert.Equal(t, float64(5), report.KnownCost)
	require.NotNil(t, report.GrossProfit)
	assert.Equal(t, float64(14), *report.GrossProfit)
}

func TestValidateRevenuePricesRejectsInvalidAmountsAndMissingSource(t *testing.T) {
	valid := model.PurchasePrice{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "tokens", InputPrice: ptr(1), OutputPrice: ptr(2), Source: "invoice"}
	require.NoError(t, model.ValidatePurchasePrices([]model.PurchasePrice{valid}))
	invalid := valid
	invalid.InputPrice = ptr(-1)
	assert.Error(t, model.ValidatePurchasePrices([]model.PurchasePrice{invalid}))
	invalid = valid
	invalid.Source = ""
	assert.Error(t, model.ValidatePurchasePrices([]model.PurchasePrice{invalid}))
	invalid = valid
	invalid.Model = "*"
	assert.Error(t, model.ValidatePurchasePrices([]model.PurchasePrice{invalid}))
}

func TestCalculateRevenueEstimatesCostFromModelMultiplier(t *testing.T) {
	logs := []model.Log{{
		CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", UserId: 1,
		Quota: 2000, PromptTokens: 1000, CompletionTokens: 500,
		Other: `{"model_ratio":1,"completion_ratio":3}`,
	}}
	rules := []model.PurchasePrice{{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "model_multiplier", UnitPrice: ptr(0.25), Source: "cost estimate"}}

	report := model.CalculateRevenue(logs, rules, nil, 1000)

	// Base model cost is (1000 input + 500*3 output) / 1000, then * 25%.
	assert.InDelta(t, 0.625, report.KnownCost, 1e-10)
	assert.Equal(t, int64(1), report.EstimatedCostEntries)
	require.NotNil(t, report.GrossProfit)
	assert.InDelta(t, 1.375, *report.GrossProfit, 1e-10)
	require.NotNil(t, report.GrossMarginRate)
	assert.InDelta(t, 0.6875, *report.GrossMarginRate, 1e-10)
	require.NotNil(t, report.Rows[0].GrossMarginRate)
	assert.InDelta(t, 0.6875, *report.Rows[0].GrossMarginRate, 1e-10)
}

func TestCalculateRevenueLeavesGrossMarginUnknownWithoutPositiveSales(t *testing.T) {
	logs := []model.Log{
		{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000},
		{CreatedAt: 101, Type: model.LogTypeRefund, ChannelId: 7, ModelName: "gpt-test", Quota: 1000},
	}
	rules := []model.PurchasePrice{{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "request", UnitPrice: ptr(0.25), Source: "invoice"}}

	report := model.CalculateRevenue(logs, rules, nil, 1000)

	require.NotNil(t, report.GrossProfit)
	assert.Nil(t, report.GrossMarginRate)
	require.Len(t, report.Rows, 1)
	assert.Nil(t, report.Rows[0].GrossMarginRate)
}

func TestCalculateRevenueKeepsMultiplierUnknownForUnsupportedUsage(t *testing.T) {
	cases := []struct {
		name string
		log  model.Log
	}{
		{name: "missing model snapshot", log: model.Log{PromptTokens: 1000, CompletionTokens: 500}},
		{name: "missing completion ratio snapshot", log: model.Log{PromptTokens: 1000, CompletionTokens: 500, Other: `{"model_ratio":1}`}},
		{name: "cached usage needs exact rates", log: model.Log{PromptTokens: 1000, Other: `{"model_ratio":1,"completion_ratio":1,"cache_tokens":100}`}},
		{name: "tiered billing", log: model.Log{PromptTokens: 1000, Other: `{"model_ratio":1,"completion_ratio":1,"billing_mode":"tiered_expr"}`}},
		{name: "media usage", log: model.Log{PromptTokens: 1000, Other: `{"model_ratio":1,"completion_ratio":1,"image":true}`}},
		{name: "async task", log: model.Log{PromptTokens: 1000, Other: `{"model_ratio":1,"completion_ratio":1,"is_task":true}`}},
		{name: "fixed model price", log: model.Log{PromptTokens: 1000, Other: `{"model_ratio":1,"completion_ratio":1,"model_price":0.04}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.log.CreatedAt, tc.log.Type, tc.log.ChannelId, tc.log.ModelName, tc.log.UserId, tc.log.Quota = 100, model.LogTypeConsume, 7, "gpt-test", 1, 1000
			rules := []model.PurchasePrice{{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "model_multiplier", UnitPrice: ptr(0.25), Source: "estimate"}}
			report := model.CalculateRevenue([]model.Log{tc.log}, rules, nil, 1000)
			assert.Equal(t, float64(0), report.KnownCost)
			assert.Equal(t, int64(1), report.UncoveredEntries)
			assert.Nil(t, report.GrossProfit)
		})
	}
}

func TestCalculateRevenuePrefersExactPurchasePriceToMultiplier(t *testing.T) {
	log := model.Log{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", UserId: 1, Quota: 2000, PromptTokens: 1000, CompletionTokens: 500, Other: `{"model_ratio":1,"completion_ratio":3}`}
	rules := []model.PurchasePrice{
		{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "model_multiplier", UnitPrice: ptr(0.25), Source: "estimate"},
		{ChannelID: 7, Model: "gpt-test", EffectiveAt: 0, Unit: "tokens", InputPrice: ptr(2), OutputPrice: ptr(4), Source: "invoice"},
	}
	// Distinct effective timestamps are required by validation, so the later
	// exact quote is the active rule for this request.
	rules[1].EffectiveAt = 50
	require.NoError(t, model.ValidatePurchasePrices(rules))

	report := model.CalculateRevenue([]model.Log{log}, rules, nil, 1000)

	assert.InDelta(t, 0.004, report.KnownCost, 1e-10)
	assert.Equal(t, int64(0), report.EstimatedCostEntries)
}

func TestCalculateRevenueUsesChannelDefaultMultiplierWithModelOverride(t *testing.T) {
	logs := []model.Log{
		{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-a", UserId: 1, Quota: 2000, PromptTokens: 1000, CompletionTokens: 500, Other: `{"model_ratio":1,"completion_ratio":3}`},
		{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-b", UserId: 1, Quota: 2000, PromptTokens: 1000, CompletionTokens: 500, Other: `{"model_ratio":1,"completion_ratio":3}`},
	}
	rules := []model.PurchasePrice{
		{ChannelID: 7, Model: "*", EffectiveAt: 0, Unit: "model_multiplier", UnitPrice: ptr(0.5), Source: "channel default"},
		{ChannelID: 7, Model: "gpt-a", EffectiveAt: 0, Unit: "model_multiplier", UnitPrice: ptr(0.25), Source: "model override"},
	}

	report := model.CalculateRevenue(logs, rules, nil, 1000)

	require.Len(t, report.Rows, 2)
	assert.InDelta(t, 0.625, report.Rows[0].KnownCost, 1e-10)
	assert.InDelta(t, 1.25, report.Rows[1].KnownCost, 1e-10)
}

func TestGetRevenueReportReadsLogDBAndRejectsOversizedRange(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Log{{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000}, {CreatedAt: 101, Type: model.LogTypeRefund, ChannelId: 7, ModelName: "gpt-test", Quota: 200}}).Error)
	report, err := model.GetRevenue(t.Context(), 0, 200, "")
	require.NoError(t, err)
	assert.Equal(t, float64(8), report.NetSales)
}

func TestGetRevenueReportAppliesUsernameFilter(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Log{
		{CreatedAt: 150, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Username: "selected", Quota: 1000},
		{CreatedAt: 150, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Username: "other", Quota: 9000},
	}).Error)

	report, err := model.GetRevenue(t.Context(), 100, 200, "selected")
	require.NoError(t, err)
	assert.Equal(t, float64(10), report.NetSales)
}

func TestAppendPurchasePriceRulePreservesExistingRulesAndUsesServerEffectiveTime(t *testing.T) {
	setupRevenueControllerTestDB(t)
	existing := []model.PurchasePrice{
		{ChannelID: 7, Model: "gpt-test", EffectiveAt: 1, Unit: "model_multiplier", UnitPrice: ptr(0.5), Source: "old"},
		{ChannelID: 9, Model: "other-model", EffectiveAt: 2, Unit: "image", UnitPrice: ptr(0.06), Source: "other channel"},
	}
	encoded, err := common.Marshal(existing)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{model.PurchasePricesOption: string(encoded)}))

	added, err := model.AppendPurchasePriceRule(t.Context(), model.PurchasePrice{
		ChannelID: 7, Model: "gpt-test", Unit: "model_multiplier", UnitPrice: ptr(0.75), Source: "new estimate",
	})
	require.NoError(t, err)
	require.Len(t, added, 3)
	assert.Equal(t, existing, added[:2])
	assert.GreaterOrEqual(t, added[2].EffectiveAt, int64(1_790_000_000))
	assert.Equal(t, int64(2), added[1].EffectiveAt)

	stored, err := model.GetPurchasePriceRules(t.Context())
	require.NoError(t, err)
	assert.Equal(t, added, stored)
}

func TestAppendPurchasePriceRulesRejectsEditingOrRemovingSavedHistory(t *testing.T) {
	setupRevenueControllerTestDB(t)
	existing := []model.PurchasePrice{{
		ChannelID: 7, Model: "gpt-test", EffectiveAt: 1,
		Unit: "model_multiplier", UnitPrice: ptr(0.5), Source: "saved quote",
	}}
	encoded, err := common.Marshal(existing)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{model.PurchasePricesOption: string(encoded)}))

	_, err = model.AppendPurchasePriceRules(t.Context(), nil)
	assert.ErrorIs(t, err, model.ErrPurchasePriceHistoryImmutable)

	modified := append([]model.PurchasePrice(nil), existing...)
	modified[0].UnitPrice = ptr(0.25)
	_, err = model.AppendPurchasePriceRules(t.Context(), modified)
	assert.ErrorIs(t, err, model.ErrPurchasePriceHistoryImmutable)

	stored, err := model.GetPurchasePriceRules(t.Context())
	require.NoError(t, err)
	assert.Equal(t, existing, stored)

	addition := model.PurchasePrice{
		ChannelID: 7, Model: "gpt-test", EffectiveAt: time.Now().Unix() + 60,
		Unit: "model_multiplier", UnitPrice: ptr(0.75), Source: "new estimate",
	}
	updated, err := model.AppendPurchasePriceRules(t.Context(), append(existing, addition))
	require.NoError(t, err)
	assert.Equal(t, append(existing, addition), updated)
}

func ptr(value float64) *float64 { return &value }

func TestRevenueUsageAndPendingTaskCosts(t *testing.T) {
	cases := []struct {
		name    string
		log     model.Log
		rule    model.PurchasePrice
		tasks   map[string]model.Task
		cost    float64
		unknown int64
		pending int64
	}{
		{name: "tokens", log: model.Log{PromptTokens: 1000000, CompletionTokens: 500000}, rule: model.PurchasePrice{Unit: "tokens", InputPrice: ptr(2), OutputPrice: ptr(4)}, cost: 4},
		{name: "image count", log: model.Log{Other: `{"image_count":2}`}, rule: model.PurchasePrice{Unit: "image", UnitPrice: ptr(.01)}, cost: .02},
		{name: "seconds", log: model.Log{Other: `{"usage_facts":{"seconds":4}}`}, rule: model.PurchasePrice{Unit: "second", UnitPrice: ptr(.75)}, cost: 3},
		{name: "missing usage", rule: model.PurchasePrice{Unit: "second", UnitPrice: ptr(.75)}, unknown: 1},
		{name: "malformed metadata", log: model.Log{Other: `{bad`}, rule: model.PurchasePrice{Unit: "request", UnitPrice: ptr(1)}, unknown: 1},
		{name: "pending task", log: model.Log{Other: `{"is_task":true,"task_id":"t"}`}, rule: model.PurchasePrice{Unit: "request", UnitPrice: ptr(1)}, unknown: 1, pending: 1},
		{name: "success task", log: model.Log{Other: `{"is_task":true,"task_id":"t"}`}, rule: model.PurchasePrice{Unit: "request", UnitPrice: ptr(1)}, tasks: map[string]model.Task{"7/1/t": {Status: model.TaskStatusSuccess}}, cost: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.log.Type, tc.log.ChannelId, tc.log.ModelName, tc.log.UserId, tc.log.Quota = model.LogTypeConsume, 7, "test", 1, 1000
			tc.rule.ChannelID, tc.rule.Model = 7, "test"
			report := model.CalculateRevenue([]model.Log{tc.log}, []model.PurchasePrice{tc.rule}, tc.tasks, 100)
			assert.InDelta(t, tc.cost, report.KnownCost, 1e-10)
			assert.Equal(t, tc.unknown, report.UncoveredEntries)
			assert.Equal(t, tc.pending, report.PendingEntries)
			if tc.unknown > 0 {
				assert.Nil(t, report.GrossProfit)
			} else {
				require.NotNil(t, report.GrossProfit)
				assert.InDelta(t, 10-tc.cost, *report.GrossProfit, 1e-10)
			}
		})
	}
}
