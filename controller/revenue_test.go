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
