package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReserveSubscriptionV1BillingRejectsRequestReplay(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionV1Usage{}))
	now := common.GetTimestamp()
	sub := model.UserSubscription{
		UserId: 919991, BillingPolicy: model.SubscriptionBillingPolicyDSFlashV1,
		ServiceChannelID: model.SubscriptionV1ChannelID, ServiceModel: model.SubscriptionV1Model,
		DailyInputTokenLimit: 1000, DailyOutputTokenLimit: 500,
		Status: "active", StartTime: now - 60, EndTime: now + 3600,
		LastResetTime: now, NextResetTime: now + 86400,
	}
	require.NoError(t, model.DB.Create(&sub).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("user_subscription_id = ?", sub.Id).Delete(&model.SubscriptionV1Usage{}).Error)
		require.NoError(t, model.DB.Delete(&sub).Error)
	})
	info := &relaycommon.RelayInfo{
		RequestId: "subscription-v1-replay", UserId: sub.UserId, SubscriptionId: sub.Id,
		BillingSource: BillingSourceSubscription, OriginModelName: model.SubscriptionV1Model,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: model.SubscriptionV1ChannelID, UpstreamModelName: model.SubscriptionV1Model},
	}

	first, err := ReserveSubscriptionV1Billing(info, 100, 50)
	require.NoError(t, err)
	assert.False(t, first.Replay)
	_, err = ReserveSubscriptionV1Billing(info, 100, 50)
	assert.ErrorIs(t, err, ErrSubscriptionV1Replay)
}

func TestSubscriptionV1SettlementUsesDailyTokensNotWalletOrKeyBalance(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionV1Usage{}))
	user := model.User{Username: "subscription-v1-balance-test", Status: common.UserStatusEnabled, Quota: 700}
	require.NoError(t, model.DB.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "subscription-v1-balance-test-key", Name: "V1 test", Status: common.TokenStatusEnabled, RemainQuota: 500}
	require.NoError(t, model.DB.Create(&token).Error)
	now := common.GetTimestamp()
	sub := model.UserSubscription{
		UserId: user.Id, BillingPolicy: model.SubscriptionBillingPolicyDSFlashV1,
		ServiceChannelID: model.SubscriptionV1ChannelID, ServiceModel: model.SubscriptionV1Model,
		DailyInputTokenLimit: 1000, DailyOutputTokenLimit: 500,
		Status: "active", StartTime: now - 60, EndTime: now + 3600,
		LastResetTime: now, NextResetTime: now + 86400,
	}
	require.NoError(t, model.DB.Create(&sub).Error)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("user_subscription_id = ?", sub.Id).Delete(&model.SubscriptionV1Usage{}).Error)
		require.NoError(t, model.DB.Delete(&sub).Error)
		require.NoError(t, model.DB.Delete(&token).Error)
		require.NoError(t, model.DB.Delete(&user).Error)
	})
	info := &relaycommon.RelayInfo{
		RequestId: "subscription-v1-no-wallet", UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		BillingSource: BillingSourceSubscription, OriginModelName: model.SubscriptionV1Model,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: model.SubscriptionV1ChannelID, UpstreamModelName: model.SubscriptionV1Model},
	}
	_, err := ReserveSubscriptionV1Billing(info, 100, 50)
	require.NoError(t, err)
	info.SubscriptionV1Billing = true
	_, err = SettleSubscriptionV1Billing(info, 80, 20)
	require.NoError(t, err)
	require.NoError(t, SettleBilling(nil, info, 999))

	var storedUser model.User
	var storedToken model.Token
	var storedSub model.UserSubscription
	require.NoError(t, model.DB.First(&storedUser, user.Id).Error)
	require.NoError(t, model.DB.First(&storedToken, token.Id).Error)
	require.NoError(t, model.DB.First(&storedSub, sub.Id).Error)
	assert.Equal(t, 700, storedUser.Quota)
	assert.Equal(t, 500, storedToken.RemainQuota)
	assert.EqualValues(t, 80, storedSub.DailyInputTokensUsed)
	assert.EqualValues(t, 20, storedSub.DailyOutputTokensUsed)
}
