package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

var ErrSubscriptionV1Replay = errors.New("subscription V1 request replay rejected")

// ReserveSubscriptionV1Billing reserves raw prompt/completion tokens for a
// DS Flash V1 subscription. It deliberately does not use BillingSession's
// quota value, which may contain pricing multipliers or cache adjustments.
func ReserveSubscriptionV1Billing(info *relaycommon.RelayInfo, inputTokens, outputTokens int64) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	channelID := info.GetSubscriptionV1ChannelID()
	if channelID != model.SubscriptionV1ChannelID || info.OriginModelName != model.SubscriptionV1Model || info.GetSubscriptionV1UpstreamModel() != model.SubscriptionV1Model {
		return nil, errors.New("subscription V1 service binding mismatch")
	}
	requestID := strings.ToLower(info.RequestId)
	var result *model.SubscriptionV1UsageResult
	var err error
	if info.SubscriptionId > 0 {
		result, err = model.ReserveSubscriptionV1Tokens(requestID, info.UserId, info.SubscriptionId, channelID, model.SubscriptionV1Model, inputTokens, outputTokens)
	} else {
		result, err = model.ReserveActiveSubscriptionV1Tokens(requestID, info.UserId, channelID, model.SubscriptionV1Model, inputTokens, outputTokens)
	}
	if err != nil {
		return nil, err
	}
	if result.Replay {
		return nil, ErrSubscriptionV1Replay
	}
	info.SubscriptionV1RequestID = requestID
	info.SubscriptionId = result.Usage.UserSubscriptionID
	return result, nil
}

// SettleSubscriptionV1Billing settles with raw upstream usage. The caller
// must pass the original usage fields before cache-token price adjustments.
func SettleSubscriptionV1Billing(info *relaycommon.RelayInfo, inputTokens, outputTokens int64) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription || info.SubscriptionId <= 0 {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	return model.SettleSubscriptionV1Tokens(subscriptionV1RequestID(info), info.UserId, info.SubscriptionId, inputTokens, outputTokens)
}

func RefundSubscriptionV1Billing(info *relaycommon.RelayInfo) (*model.SubscriptionV1UsageResult, error) {
	if info == nil || info.BillingSource != BillingSourceSubscription || info.SubscriptionId <= 0 {
		return nil, errors.New("subscription V1 billing requires a subscription relay")
	}
	return model.RefundSubscriptionV1Tokens(subscriptionV1RequestID(info), info.UserId, info.SubscriptionId)
}

func subscriptionV1RequestID(info *relaycommon.RelayInfo) string {
	if info.SubscriptionV1RequestID != "" {
		return info.SubscriptionV1RequestID
	}
	return strings.ToLower(info.RequestId)
}
