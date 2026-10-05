package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// RefreshCustomerChannelDiscount freezes one rule for the actual attempt. The
// request timestamp is shared across retries, so a concurrent rule edit cannot
// change this request's agreement halfway through execution.
func RefreshCustomerChannelDiscount(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info == nil || info.SubscriptionV1Billing || info.BillingSource == BillingSourceSubscription {
		return nil
	}
	channelID := 0
	if c != nil {
		channelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	}
	if channelID <= 0 && info.ChannelMeta != nil {
		channelID = info.ChannelMeta.ChannelId
	}
	if channelID <= 0 || info.UserId <= 0 {
		return nil
	}
	if info.CustomerDiscountResolved && info.CustomerDiscountChannelID == channelID {
		return nil
	}
	if info.StartTime.IsZero() {
		info.StartTime = time.Now()
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if !info.CustomerDiscountHistoryLoaded {
		history, err := model.GetCustomerChannelDiscounts(ctx, info.UserId)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeQueryDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		}
		info.CustomerDiscountHistory = make([]relaycommon.CustomerChannelDiscountRule, 0, len(history.Rules))
		for _, rule := range history.Rules {
			if rule.EffectiveAt <= info.StartTime.UnixMilli() {
				info.CustomerDiscountHistory = append(info.CustomerDiscountHistory, relaycommon.CustomerChannelDiscountRule{
					CustomerChannelDiscountSnapshot: relaycommon.CustomerChannelDiscountSnapshot{UserID: info.UserId, ChannelID: rule.ChannelID, Model: rule.Model, Multiplier: rule.Multiplier, Version: rule.Version, EffectiveAt: rule.EffectiveAt},
					Disabled:                        rule.Disabled,
				})
			}
		}
		info.CustomerDiscountHistoryLoaded = true
	}
	var exact, fallback *relaycommon.CustomerChannelDiscountRule
	for i := range info.CustomerDiscountHistory {
		rule := &info.CustomerDiscountHistory[i]
		if rule.ChannelID != channelID {
			continue
		}
		if rule.Model == info.GetBillingModelName() && (exact == nil || rule.Version > exact.Version) {
			exact = rule
		}
		if rule.Model == "*" && (fallback == nil || rule.Version > fallback.Version) {
			fallback = rule
		}
	}
	snap := fallback
	if exact != nil && !exact.Disabled {
		snap = exact
	}
	info.CustomerChannelDiscount = nil
	if snap != nil && !snap.Disabled {
		if snap.ChannelID != channelID || !validCustomerDiscountMultiplier(snap.Multiplier) {
			return types.NewErrorWithStatusCode(fmt.Errorf("invalid customer channel discount"), types.ErrorCodeModelPriceError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
		}
		copy := snap.CustomerChannelDiscountSnapshot
		info.CustomerChannelDiscount = &copy
	}
	info.CustomerDiscountChannelID = channelID
	info.CustomerDiscountResolved = true
	return nil
}

func validCustomerDiscountMultiplier(value float64) bool {
	return value > 0 && value <= 1 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func customerDiscountQuota(quota int, snap *relaycommon.CustomerChannelDiscountSnapshot) (int, *common.QuotaClamp) {
	if quota <= 0 || snap == nil || snap.Multiplier == 1 {
		return quota, nil
	}
	return common.QuotaFromDecimalChecked(customerDiscountAmount(decimal.NewFromInt(int64(quota)), snap))
}

func customerDiscountAmount(quota decimal.Decimal, snap *relaycommon.CustomerChannelDiscountSnapshot) decimal.Decimal {
	if quota.Sign() <= 0 || snap == nil || snap.Multiplier == 1 {
		return quota
	}
	// Invalid persisted data must never become a free charge or a credit.
	if !validCustomerDiscountMultiplier(snap.Multiplier) {
		common.SysError("invalid persisted customer channel discount multiplier")
		return quota
	}
	return quota.Mul(decimal.NewFromFloat(snap.Multiplier))
}

func applyCustomerChannelDiscountAmount(info *relaycommon.RelayInfo, normalAmount decimal.Decimal) decimal.Decimal {
	if info == nil || info.SubscriptionV1Billing || info.BillingSource == BillingSourceSubscription {
		return normalAmount
	}
	return customerDiscountAmount(normalAmount, info.CustomerChannelDiscount)
}

// ApplyCustomerChannelDiscount accepts the complete normal charge, including
// cache, expression, image quantity and tool surcharges. Call exactly once at
// the accounting boundary, never by inserting an OtherRatios entry.
func ApplyCustomerChannelDiscount(info *relaycommon.RelayInfo, normalQuota int) int {
	amount := applyCustomerChannelDiscountAmount(info, decimal.NewFromInt(int64(normalQuota)))
	quota, clamp := common.QuotaFromDecimalChecked(amount)
	noteQuotaClamp(info, clamp)
	return quota
}

// ReserveCustomerChannelBilling updates a retained session, never creates a
// second reservation during retries. Cheaper attempts refund at settlement.
func ReserveCustomerChannelBilling(c *gin.Context, info *relaycommon.RelayInfo, normalQuota int) *types.NewAPIError {
	if info.Billing == nil {
		return PreConsumeBilling(c, normalQuota, info)
	}
	if apiErr := RefreshCustomerChannelDiscount(c, info); apiErr != nil {
		return apiErr
	}
	quota := ApplyCustomerChannelDiscount(info, normalQuota)
	if info.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(info.QuotaClamp, types.ErrorCodeModelPriceError, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if quota > info.Billing.GetPreConsumedQuota() {
		if err := info.Billing.Reserve(quota); err != nil {
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return apiErr
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
	}
	info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
	return nil
}

func appendCustomerDiscountInfo(other *model.LogOther, snap *relaycommon.CustomerChannelDiscountSnapshot) {
	if other != nil && snap != nil {
		other.SetAdmin("customer_channel_discount", snap)
	}
}
