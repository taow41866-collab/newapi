package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionV1AccessAndCustomerPurchaseGuards(t *testing.T) {
	_, adminToken := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(
		&model.SubscriptionPlan{},
		&model.UserSubscription{},
		&model.SubscriptionOrder{},
		&model.Redemption{},
	))

	paymentSetting := operation_setting.GetPaymentSetting()
	previousConfirmed, previousVersion := paymentSetting.ComplianceConfirmed, paymentSetting.ComplianceTermsVersion
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = previousConfirmed
		paymentSetting.ComplianceTermsVersion = previousVersion
	})

	const (
		legacyPlanID = 92001
		v1PlanID     = 92002
		userQuota    = 987654
	)
	legacyPlan := model.SubscriptionPlan{Id: legacyPlanID, Title: "Legacy plan", Enabled: true, DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000}
	v1Plan := model.SubscriptionPlan{
		Id:                    v1PlanID,
		Title:                 "DS Flash day",
		Enabled:               true,
		PriceAmount:           8.99,
		DurationUnit:          model.SubscriptionDurationDay,
		DurationValue:         1,
		TotalAmount:           1000,
		BillingPolicy:         model.SubscriptionBillingPolicyDSFlashV1,
		ServiceChannelID:      model.SubscriptionV1ChannelID,
		ServiceModel:          model.SubscriptionV1Model,
		DailyInputTokenLimit:  50_000_000,
		DailyOutputTokenLimit: 10_000_000,
		QuotaResetPeriod:      model.SubscriptionResetDaily,
		AllowWalletOverflow:   common.GetPointer(false),
	}
	require.NoError(t, model.DB.Create(&legacyPlan).Error)
	require.NoError(t, model.DB.Create(&v1Plan).Error)
	model.InvalidateSubscriptionPlanCache(legacyPlanID)
	model.InvalidateSubscriptionPlanCache(v1PlanID)
	t.Cleanup(func() {
		model.InvalidateSubscriptionPlanCache(legacyPlanID)
		model.InvalidateSubscriptionPlanCache(v1PlanID)
	})

	userToken := "subscription-v1-common-token"
	user := model.User{
		Username:   "subscription-v1-common",
		Password:   "unused",
		Role:       common.RoleCommonUser,
		Status:     common.UserStatusEnabled,
		Group:      "default",
		AffCode:    "subscription-v1-common",
		Quota:      userQuota,
		AccessToken: &userToken,
	}
	require.NoError(t, model.DB.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestId())
	router.GET("/api/subscription/plans", middleware.UserAuth(), GetSubscriptionPlans)
	router.GET("/api/subscription/admin/plans", middleware.AdminAuth(), AdminListSubscriptionPlans)
	router.POST("/api/subscription/redeem", middleware.UserAuth(), RedeemSubscriptionCode)
	router.POST("/api/subscription/balance/pay", middleware.UserAuth(), SubscriptionRequestBalancePay)
	router.POST("/api/subscription/epay/pay", middleware.UserAuth(), SubscriptionRequestEpay)
	router.POST("/api/subscription/stripe/pay", middleware.UserAuth(), SubscriptionRequestStripePay)
	router.POST("/api/subscription/creem/pay", middleware.UserAuth(), SubscriptionRequestCreemPay)
	router.POST("/api/subscription/waffo-pancake/pay", middleware.UserAuth(), SubscriptionRequestWaffoPancakePay)
	router.POST("/api/redemption/subscription", middleware.AdminAuth(), AddSubscriptionRedemption)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(recorder, req)
		return recorder
	}

	var visible struct {
		Success bool `json:"success"`
		Data    []SubscriptionPlanDTO `json:"data"`
	}
	response := request(http.MethodGet, "/api/subscription/plans", "", userToken)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &visible))
	require.True(t, visible.Success)
	assert.Equal(t, []int{legacyPlanID}, subscriptionPlanIDs(visible.Data))

	response = request(http.MethodGet, "/api/subscription/admin/plans", "", userToken)
	assert.Equal(t, http.StatusForbidden, response.Code, "regular users must not access the admin plan list")
	response = request(http.MethodGet, "/api/subscription/admin/plans", "", adminToken)
	require.Equal(t, http.StatusOK, response.Code)
	var adminVisible struct {
		Success bool `json:"success"`
		Data    []SubscriptionPlanDTO `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &adminVisible))
	require.True(t, adminVisible.Success)
	assert.ElementsMatch(t, []int{legacyPlanID, v1PlanID}, subscriptionPlanIDs(adminVisible.Data))

	for _, tc := range []struct {
		path string
		body string
	}{
		{path: "/api/subscription/balance/pay", body: `{"plan_id":92002}`},
		{path: "/api/subscription/epay/pay", body: `{"plan_id":92002,"payment_method":"alipay"}`},
		{path: "/api/subscription/stripe/pay", body: `{"plan_id":92002}`},
		{path: "/api/subscription/creem/pay", body: `{"plan_id":92002}`},
		{path: "/api/subscription/waffo-pancake/pay", body: `{"plan_id":92002}`},
	} {
		t.Run("blocks_direct_purchase_"+strings.TrimPrefix(tc.path, "/api/subscription/"), func(t *testing.T) {
			response := request(http.MethodPost, tc.path, tc.body, userToken)
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success)
			assert.Contains(t, result.Message, "请使用兑换码")
		})
	}
	var ordersAfterRejectedPurchase int64
	require.NoError(t, model.DB.Model(&model.SubscriptionOrder{}).Where("user_id = ? AND plan_id = ?", user.Id, v1PlanID).Count(&ordersAfterRejectedPurchase).Error)
	assert.Zero(t, ordersAfterRejectedPurchase, "rejected direct purchases must not create orders")
	var subscriptionsAfterRejectedPurchase int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, v1PlanID).Count(&subscriptionsAfterRejectedPurchase).Error)
	assert.Zero(t, subscriptionsAfterRejectedPurchase, "rejected direct purchases must not grant a subscription")

	createCodeRequest := `{"name":"admin-created","count":1,"plan_id":92002,"kind":"day","expired_time":0}`
	response = request(http.MethodPost, "/api/redemption/subscription", createCodeRequest, userToken)
	assert.Equal(t, http.StatusForbidden, response.Code, "regular users must not issue subscription codes")
	response = request(http.MethodPost, "/api/redemption/subscription", createCodeRequest, adminToken)
	require.Equal(t, http.StatusOK, response.Code)
	var createdCodes struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &createdCodes))
	require.True(t, createdCodes.Success)
	require.Len(t, createdCodes.Data, 1)
	codeBody, err := common.Marshal(map[string]string{"key": createdCodes.Data[0]})
	require.NoError(t, err)
	response = request(http.MethodPost, "/api/subscription/redeem", string(codeBody), userToken)
	require.Equal(t, http.StatusOK, response.Code)
	var redeemed struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &redeemed))
	assert.True(t, redeemed.Success, "a regular authenticated user must be able to redeem the admin-issued V1 code")

	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, userQuota, storedUser.Quota, "V1 redemption must not modify wallet quota")
	var subscriptionCount int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, v1PlanID).Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)
	var storedCode model.Redemption
	require.NoError(t, model.DB.Where("key = ?", createdCodes.Data[0]).First(&storedCode).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, storedCode.Status)
	assert.Equal(t, user.Id, storedCode.UsedUserId)
}

func subscriptionPlanIDs(plans []SubscriptionPlanDTO) []int {
	ids := make([]int, 0, len(plans))
	for _, plan := range plans {
		ids = append(ids, plan.Plan.Id)
	}
	return ids
}
