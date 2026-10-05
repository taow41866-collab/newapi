package controller

import (
	"testing"

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
}

func TestGetRevenueReportReadsLogDBAndRejectsOversizedRange(t *testing.T) {
	db := setupRevenueControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.Log{{CreatedAt: 100, Type: model.LogTypeConsume, ChannelId: 7, ModelName: "gpt-test", Quota: 1000}, {CreatedAt: 101, Type: model.LogTypeRefund, ChannelId: 7, ModelName: "gpt-test", Quota: 200}}).Error)
	report, err := model.GetRevenue(t.Context(), 0, 200)
	require.NoError(t, err)
	assert.Equal(t, float64(8), report.NetSales)
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
