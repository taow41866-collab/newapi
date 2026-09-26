package service

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNewBillingSessionSubscriptionFirstUsesWalletForDifferentModel(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))

	user := &model.User{
		Username: "billing-model-isolation-" + common.GetRandomString(8),
		Password: "test-password-hash",
		Status:   common.UserStatusEnabled,
		Quota:    5_000,
		AffCode:  "billing-model-isolation-" + common.GetRandomString(8),
	}
	require.NoError(t, model.DB.Create(user).Error)
	token := &model.Token{
		UserId:      user.Id,
		Key:         "billing-model-isolation-" + common.GetRandomString(8),
		Name:        "billing-model-isolation",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 5_000,
	}
	require.NoError(t, model.DB.Create(token).Error)
	now := time.Now().Unix()
	sub := &model.UserSubscription{
		UserId:              user.Id,
		ServiceModel:        "deepseek-v4.1-flash",
		AmountTotal:         10_000,
		StartTime:           now - 60,
		EndTime:             now + 3_600,
		Status:              "active",
		AllowWalletOverflow: false,
	}
	require.NoError(t, model.DB.Create(sub).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("request_id = ?", "billing-model-isolation-request").Delete(&model.SubscriptionPreConsumeRecord{}).Error)
		require.NoError(t, model.DB.Delete(sub).Error)
		require.NoError(t, model.DB.Delete(token).Error)
		require.NoError(t, model.DB.Unscoped().Delete(user).Error)
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "gpt-5.6-sol",
		RequestId:       "billing-model-isolation-request",
		ForcePreConsume: true,
		UserSetting:     dto.UserSetting{BillingPreference: "subscription_first"},
	}

	session, apiErr := NewBillingSession(ctx, info, 1_000)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, BillingSourceWallet, info.BillingSource)
	assert.Equal(t, 1_000, info.FinalPreConsumedQuota)

	var storedUser model.User
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 4_000, storedUser.Quota)
	var storedToken model.Token
	require.NoError(t, model.DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, 4_000, storedToken.RemainQuota)
	var record model.SubscriptionPreConsumeRecord
	assert.True(t, errors.Is(model.DB.Where("request_id = ?", info.RequestId).First(&record).Error, gorm.ErrRecordNotFound))
}
