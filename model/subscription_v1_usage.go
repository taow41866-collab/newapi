package model

import (
	"errors"
	"math"
	"strings"

	"gorm.io/gorm"
)

// SubscriptionV1Usage is a durable reservation journal, not a wallet balance.
type SubscriptionV1Usage struct {
	Id                 int
	RequestID          string `gorm:"type:varchar(128);not null;uniqueIndex"`
	UserID             int `gorm:"not null;index"`
	UserSubscriptionID int `gorm:"not null;index"`
	UsageEpoch         int64 `gorm:"not null;default:0"`
	WindowStart        int64 `gorm:"not null"`
	InputLimitSnapshot int64
	OutputLimitSnapshot int64
	CreatedAt          int64 `gorm:"autoCreateTime"`
	UpdatedAt          int64 `gorm:"autoUpdateTime"`
	ChannelID          int
	Model              string `gorm:"type:varchar(128)"`
	ReservedInput      int64
	ReservedOutput     int64
	ActualInput        int64
	ActualOutput       int64
	Status             string `gorm:"type:varchar(16);not null"`
}

type SubscriptionV1UsageResult struct {
	Usage SubscriptionV1Usage
	// Replay means the caller MUST NOT send another upstream request.
	Replay bool
}

var (
	ErrSubscriptionV1Quota = errors.New("subscription token quota insufficient")
	ErrSubscriptionV1Conflict = errors.New("subscription request conflict")
	ErrSubscriptionV1State = errors.New("subscription request state invalid")
	ErrSubscriptionV1OverReservation = errors.New("actual token usage exceeds reservation")
)

func validV1RequestArgs(requestID string, userID int, input, output int64) bool {
	// Canonical lower-case ASCII avoids database collation aliases and truncation.
	for _, ch := range requestID {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.' || ch == ':') {
			return false
		}
	}
	return requestID != "" && requestID == strings.TrimSpace(requestID) && len(requestID) <= 128 &&
		userID > 0 && input >= 0 && output >= 0 && input <= math.MaxInt32 && output <= math.MaxInt32
}

func validV1UsageArgs(requestID string, userID, subscriptionID int, input, output int64) bool {
	return validV1RequestArgs(requestID, userID, input, output) && subscriptionID > 0
}

func ReserveSubscriptionV1Tokens(requestID string, userID, subscriptionID, channelID int, modelName string, inputTokens, outputTokens int64) (*SubscriptionV1UsageResult, error) {
	if !validV1UsageArgs(requestID, userID, subscriptionID, inputTokens, outputTokens) || inputTokens+outputTokens == 0 {
		return nil, errors.New("invalid token reservation")
	}
	result := &SubscriptionV1UsageResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).First(&sub, subscriptionID).Error; err != nil { return err }
		if sub.UserId != userID || sub.BillingPolicy != SubscriptionBillingPolicyDSFlashV1 ||
			channelID != SubscriptionV1ChannelID || channelID != sub.ServiceChannelID ||
			modelName != SubscriptionV1Model || modelName != sub.ServiceModel || sub.AllowWalletOverflow {
			return errors.New("subscription service binding mismatch")
		}
		var existing SubscriptionV1Usage
		query := lockForUpdate(tx).Where("request_id = ?", requestID).Limit(1).Find(&existing)
		if query.Error != nil { return query.Error }
		if query.RowsAffected > 0 {
			if existing.RequestID != requestID || existing.UserID != userID || existing.UserSubscriptionID != subscriptionID || existing.ChannelID != channelID || existing.Model != modelName || existing.ReservedInput != inputTokens || existing.ReservedOutput != outputTokens {
				return ErrSubscriptionV1Conflict
			}
			result.Usage, result.Replay = existing, true
			return nil
		}
		now := getDBTimestampFrom(tx)
		if sub.Status != "active" || sub.StartTime > now || sub.EndTime <= now || sub.UsageEpoch < 0 { return ErrSubscriptionV1State }
		if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetDaily}, now); err != nil { return err }
		if sub.DailyInputTokenLimit <= 0 || sub.DailyOutputTokenLimit <= 0 || sub.DailyInputTokensUsed < 0 || sub.DailyOutputTokensUsed < 0 ||
			sub.DailyInputTokensUsed > sub.DailyInputTokenLimit || sub.DailyOutputTokensUsed > sub.DailyOutputTokenLimit ||
			inputTokens > sub.DailyInputTokenLimit-sub.DailyInputTokensUsed || outputTokens > sub.DailyOutputTokenLimit-sub.DailyOutputTokensUsed {
			return ErrSubscriptionV1Quota
		}
		usage := SubscriptionV1Usage{RequestID: requestID, UserID: userID, UserSubscriptionID: subscriptionID, UsageEpoch: sub.UsageEpoch, WindowStart: max(sub.StartTime, sub.LastResetTime), ChannelID: channelID, Model: modelName, ReservedInput: inputTokens, ReservedOutput: outputTokens, Status: "reserved"}
		usage.InputLimitSnapshot = sub.DailyInputTokenLimit
		usage.OutputLimitSnapshot = sub.DailyOutputTokenLimit
		if err := tx.Create(&usage).Error; err != nil { return err }
		sub.DailyInputTokensUsed += inputTokens
		sub.DailyOutputTokensUsed += outputTokens
		if err := tx.Save(&sub).Error; err != nil { return err }
		result.Usage = usage
		return nil
	})
	if err != nil { return nil, err }
	return result, nil
}

// ReserveActiveSubscriptionV1Tokens reserves against the earliest-expiring
// active V1 entitlement with enough daily capacity. A request ID is checked
// globally first, so retries cannot reserve a second card or reach upstream.
func ReserveActiveSubscriptionV1Tokens(requestID string, userID, channelID int, modelName string, inputTokens, outputTokens int64) (*SubscriptionV1UsageResult, error) {
	if !validV1RequestArgs(requestID, userID, inputTokens, outputTokens) || inputTokens+outputTokens == 0 ||
		channelID != SubscriptionV1ChannelID || modelName != SubscriptionV1Model {
		return nil, errors.New("invalid token reservation")
	}
	result := &SubscriptionV1UsageResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing SubscriptionV1Usage
		query := lockForUpdate(tx).Where("request_id = ?", requestID).Limit(1).Find(&existing)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			if existing.UserID != userID || existing.ChannelID != channelID || existing.Model != modelName ||
				existing.ReservedInput != inputTokens || existing.ReservedOutput != outputTokens {
				return ErrSubscriptionV1Conflict
			}
			result.Usage, result.Replay = existing, true
			return nil
		}

		now := getDBTimestampFrom(tx)
		var subscriptions []UserSubscription
		if err := lockForUpdate(tx).
			Where("user_id = ? AND status = ? AND start_time <= ? AND end_time > ? AND billing_policy = ? AND service_channel_id = ? AND service_model = ? AND allow_wallet_overflow = ? AND daily_input_token_limit > 0 AND daily_output_token_limit > 0",
				userID, "active", now, now, SubscriptionBillingPolicyDSFlashV1, channelID, modelName, false).
			Order("end_time asc, id asc").Find(&subscriptions).Error; err != nil {
			return err
		}
		if len(subscriptions) == 0 {
			return errors.New("no active DS Flash V1 subscription")
		}
		for _, candidate := range subscriptions {
			sub := candidate
			if sub.UsageEpoch < 0 {
				continue
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetDaily}, now); err != nil {
				return err
			}
			if sub.DailyInputTokensUsed < 0 || sub.DailyOutputTokensUsed < 0 ||
				sub.DailyInputTokensUsed > sub.DailyInputTokenLimit || sub.DailyOutputTokensUsed > sub.DailyOutputTokenLimit ||
				inputTokens > sub.DailyInputTokenLimit-sub.DailyInputTokensUsed ||
				outputTokens > sub.DailyOutputTokenLimit-sub.DailyOutputTokensUsed {
				continue
			}
			usage := SubscriptionV1Usage{
				RequestID: requestID, UserID: userID, UserSubscriptionID: sub.Id, UsageEpoch: sub.UsageEpoch,
				WindowStart: max(sub.StartTime, sub.LastResetTime), ChannelID: channelID, Model: modelName,
				ReservedInput: inputTokens, ReservedOutput: outputTokens, Status: "reserved",
				InputLimitSnapshot: sub.DailyInputTokenLimit, OutputLimitSnapshot: sub.DailyOutputTokenLimit,
			}
			if err := tx.Create(&usage).Error; err != nil {
				return err
			}
			sub.DailyInputTokensUsed += inputTokens
			sub.DailyOutputTokensUsed += outputTokens
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			result.Usage = usage
			return nil
		}
		return ErrSubscriptionV1Quota
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func SettleSubscriptionV1Tokens(requestID string, userID, subscriptionID int, inputTokens, outputTokens int64) (*SubscriptionV1UsageResult, error) {
	return finalizeSubscriptionV1Tokens(requestID, userID, subscriptionID, inputTokens, outputTokens, "settled")
}

func RefundSubscriptionV1Tokens(requestID string, userID, subscriptionID int) (*SubscriptionV1UsageResult, error) {
	return finalizeSubscriptionV1Tokens(requestID, userID, subscriptionID, 0, 0, "refunded")
}

func finalizeSubscriptionV1Tokens(requestID string, userID, subscriptionID int, inputTokens, outputTokens int64, status string) (*SubscriptionV1UsageResult, error) {
	if !validV1UsageArgs(requestID, userID, subscriptionID, inputTokens, outputTokens) { return nil, errors.New("invalid token settlement") }
	result := &SubscriptionV1UsageResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).First(&sub, subscriptionID).Error; err != nil { return err }
		if sub.UserId != userID || sub.BillingPolicy != SubscriptionBillingPolicyDSFlashV1 { return errors.New("subscription owner mismatch") }
		var usage SubscriptionV1Usage
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&usage).Error; err != nil { return err }
		if usage.RequestID != requestID || usage.UserID != userID || usage.UserSubscriptionID != subscriptionID { return errors.New("request owner mismatch") }
		if usage.Status != "reserved" {
			if usage.Status != status || usage.ActualInput != inputTokens || usage.ActualOutput != outputTokens { return ErrSubscriptionV1State }
			result.Usage, result.Replay = usage, true
			return nil
		}
		if usage.ReservedInput < 0 || usage.ReservedOutput < 0 || usage.ReservedInput > math.MaxInt32 || usage.ReservedOutput > math.MaxInt32 || inputTokens > usage.ReservedInput || outputTokens > usage.ReservedOutput { return ErrSubscriptionV1OverReservation }
		if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, &SubscriptionPlan{QuotaResetPeriod: SubscriptionResetDaily}, getDBTimestampFrom(tx)); err != nil { return err }
		if usage.UsageEpoch == sub.UsageEpoch && usage.WindowStart == max(sub.StartTime, sub.LastResetTime) {
			inputRefund, outputRefund := usage.ReservedInput-inputTokens, usage.ReservedOutput-outputTokens
			if inputRefund > sub.DailyInputTokensUsed || outputRefund > sub.DailyOutputTokensUsed { return errors.New("reservation accounting mismatch") }
			sub.DailyInputTokensUsed -= inputRefund
			sub.DailyOutputTokensUsed -= outputRefund
			if err := tx.Save(&sub).Error; err != nil { return err }
		}
		updated := tx.Model(&SubscriptionV1Usage{}).Where("id = ? AND status = ?", usage.Id, "reserved").Updates(map[string]any{"status": status, "actual_input": inputTokens, "actual_output": outputTokens})
		if updated.Error != nil { return updated.Error }
		if updated.RowsAffected != 1 { return errors.New("request finalization conflict") }
		usage.Status, usage.ActualInput, usage.ActualOutput = status, inputTokens, outputTokens
		result.Usage = usage
		return nil
	})
	if err != nil { return nil, err }
	return result, nil
}
