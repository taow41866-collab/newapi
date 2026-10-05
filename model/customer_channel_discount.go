package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CustomerChannelDiscountOptionPrefix = "CustomerChannelDiscount:"

var ErrCustomerDiscountConflict = errors.New("customer discount rules changed; reload before saving")
var customerDiscountMutex sync.Mutex

type CustomerChannelDiscount struct {
	ChannelID   int     `json:"channel_id"`
	Model       string  `json:"model"`
	Multiplier  float64 `json:"multiplier"`
	Disabled    bool    `json:"disabled"`
	Version     int64   `json:"version"`
	EffectiveAt int64   `json:"effective_at"` // Unix milliseconds; ordering within a millisecond uses version.
	ActorID     int     `json:"actor_id"`
}

type CustomerChannelDiscountHistory struct {
	Version int64                     `json:"version"`
	Rules   []CustomerChannelDiscount `json:"rules"`
}

func ValidateCustomerChannelDiscounts(rules []CustomerChannelDiscount) error {
	if len(rules) == 0 || len(rules) > 100 {
		return errors.New("provide between 1 and 100 customer discount rules")
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if rule.ChannelID <= 0 || rule.Model == "" || len(rule.Model) > 255 || strings.TrimSpace(rule.Model) != rule.Model || (rule.Model != "*" && strings.ContainsAny(rule.Model, "*?")) {
			return errors.New("a channel and exact model or * are required")
		}
		if rule.Multiplier <= 0 || rule.Multiplier > 1 || math.IsNaN(rule.Multiplier) || math.IsInf(rule.Multiplier, 0) {
			return errors.New("discount multiplier must be finite and greater than 0, up to 1")
		}
		key := fmt.Sprintf("%d/%s", rule.ChannelID, rule.Model)
		if seen[key] {
			return errors.New("duplicate channel/model discount rule")
		}
		seen[key] = true
	}
	return nil
}

func GetCustomerChannelDiscounts(ctx context.Context, userID int) (CustomerChannelDiscountHistory, error) {
	history := CustomerChannelDiscountHistory{Rules: []CustomerChannelDiscount{}}
	if userID <= 0 {
		return history, nil
	}
	var option Option
	if err := DB.WithContext(ctx).Where(&Option{Key: CustomerChannelDiscountOptionPrefix + fmt.Sprint(userID)}).Limit(1).Find(&option).Error; err != nil {
		return history, err
	}
	if option.Value != "" {
		if err := common.UnmarshalJsonStr(option.Value, &history); err != nil {
			return history, err
		}
		if err := validateCustomerDiscountHistory(history); err != nil {
			return history, err
		}
	}
	return history, nil
}

func validateCustomerDiscountHistory(history CustomerChannelDiscountHistory) error {
	if len(history.Rules) > 1000 || history.Version < 0 {
		return errors.New("invalid customer discount history")
	}
	for _, rule := range history.Rules {
		if err := ValidateCustomerChannelDiscounts([]CustomerChannelDiscount{rule}); err != nil {
			return err
		}
		if rule.Version <= 0 || rule.Version > history.Version || rule.EffectiveAt <= 0 || rule.ActorID <= 0 {
			return errors.New("invalid customer discount history metadata")
		}
	}
	return nil
}

// Resolve reads the authoritative database, avoiding divergent per-instance rule caches.
func ResolveCustomerChannelDiscount(ctx context.Context, userID, channelID int, modelName string, atMillis int64) (CustomerChannelDiscount, error) {
	result := CustomerChannelDiscount{ChannelID: channelID, Model: modelName, Multiplier: 1}
	history, err := GetCustomerChannelDiscounts(ctx, userID)
	if err != nil {
		return result, err
	}
	var exact, fallback *CustomerChannelDiscount
	for i := range history.Rules {
		rule := &history.Rules[i]
		if rule.ChannelID != channelID || rule.EffectiveAt > atMillis {
			continue
		}
		if rule.Model == modelName && (exact == nil || rule.Version > exact.Version) {
			exact = rule
		}
		if rule.Model == "*" && (fallback == nil || rule.Version > fallback.Version) {
			fallback = rule
		}
	}
	if exact != nil && !exact.Disabled {
		return *exact, nil
	}
	if fallback != nil && !fallback.Disabled {
		return *fallback, nil
	}
	return result, nil
}

func AppendCustomerChannelDiscounts(ctx context.Context, userID, actorID int, expectedVersion int64, submitted []CustomerChannelDiscount) (CustomerChannelDiscountHistory, error) {
	history := CustomerChannelDiscountHistory{Rules: []CustomerChannelDiscount{}}
	if userID <= 0 || actorID <= 0 || expectedVersion < 0 {
		return history, errors.New("invalid customer or operator")
	}
	if err := ValidateCustomerChannelDiscounts(submitted); err != nil {
		return history, err
	}
	customerDiscountMutex.Lock()
	defer customerDiscountMutex.Unlock()
	transaction := func(tx *gorm.DB) error {
		history = CustomerChannelDiscountHistory{Rules: []CustomerChannelDiscount{}}
		option := Option{Key: CustomerChannelDiscountOptionPrefix + fmt.Sprint(userID)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&option).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where(&Option{Key: option.Key}).First(&option).Error; err != nil {
			return err
		}
		var user User
		if err := tx.Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		if option.Value != "" {
			if err := common.UnmarshalJsonStr(option.Value, &history); err != nil {
				return err
			}
		}
		if err := validateCustomerDiscountHistory(history); err != nil {
			return err
		}
		if history.Version != expectedVersion {
			return ErrCustomerDiscountConflict
		}
		if len(history.Rules)+len(submitted) > 1000 {
			return errors.New("customer discount history limit reached")
		}
		history.Version++
		now := time.Now().UnixMilli()
		for _, rule := range submitted {
			var channel Channel
			channelErr := tx.Select("id", "models").First(&channel, rule.ChannelID).Error
			if channelErr != nil {
				if !rule.Disabled || !errors.Is(channelErr, gorm.ErrRecordNotFound) {
					return channelErr
				}
			} else if !rule.Disabled && rule.Model != "*" {
				found := false
				for name := range strings.SplitSeq(channel.Models, ",") {
					found = found || strings.TrimSpace(name) == rule.Model
				}
				if !found {
					// A billing identity may include a canonical pricing modifier
					// that is intentionally absent from the channel's routing list.
					canonical := false
					for base := range strings.SplitSeq(channel.Models, ",") {
						base = strings.TrimSpace(base)
						if base == "" || !strings.HasPrefix(rule.Model, base+"@") {
							continue
						}
						for _, candidate := range hostreasoning.CanonicalBillingModelNames(rule.Model) {
							if candidate == rule.Model && hasConfiguredBillingEntry(candidate) {
								canonical = true
								break
							}
						}
						if !canonical {
							_, configured, _ := ratio_setting.GetModelRatio(rule.Model)
							canonical = configured && hasConfiguredBillingEntry(rule.Model)
						}
						if canonical {
							break
						}
					}
					if !canonical {
						return errors.New("discount model is not configured on this channel")
					}
				}
			}
			rule.Version, rule.EffectiveAt, rule.ActorID = history.Version, now, actorID
			history.Rules = append(history.Rules, rule)
		}
		encoded, err := common.Marshal(history)
		if err != nil {
			return err
		}
		return tx.Model(&option).Update("value", string(encoded)).Error
	}
	err := DB.WithContext(ctx).Transaction(transaction)
	return history, err
}

func hasConfiguredBillingEntry(name string) bool {
	formatted := ratio_setting.FormatMatchingModelName(name)
	if _, ok := ratio_setting.GetModelPrice(formatted, false); ok || ratio_setting.HasConfiguredModelRatio(formatted) {
		return true
	}
	return billing_setting.GetBillingMode(formatted) == billing_setting.BillingModeTieredExpr
}
