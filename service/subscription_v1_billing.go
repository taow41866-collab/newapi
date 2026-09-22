package service

import (
	"errors"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ReserveSubscriptionV1Billing reserves raw prompt/completion tokens for a
// DS Flash V1 subscription. It deliberately does not use BillingSession's
// quota value, which may contain pricing multipliers or cache adjustments.
func ReserveSubscriptionV1Billing(info *relaycommon.RelayInfo, inputTokens, outputTokens int64) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription || info.SubscriptionId <= 0 {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	if info.ChannelId != model.SubscriptionV1ChannelID || info.GetBillingModelName() != model.SubscriptionV1Model {
		return nil, errors.New("subscription V1 service binding mismatch")
	}
	return model.ReserveSubscriptionV1Tokens(info.RequestId, info.UserId, info.SubscriptionId, info.ChannelId, info.GetBillingModelName(), inputTokens, outputTokens)
}

// SettleSubscriptionV1Billing settles with raw upstream usage. The caller
// must pass the original usage fields before cache-token price adjustments.
func SettleSubscriptionV1Billing(info *relaycommon.RelayInfo, inputTokens, outputTokens int64) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription || info.SubscriptionId <= 0 {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	return model.SettleSubscriptionV1Tokens(info.RequestId, info.UserId, info.SubscriptionId, inputTokens, outputTokens)
}

func RefundSubscriptionV1Billing(info *relaycommon.RelayInfo) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription || info.SubscriptionId <= 0 {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	return model.RefundSubscriptionV1Tokens(info.RequestId, info.UserId, info.SubscriptionId)
}
