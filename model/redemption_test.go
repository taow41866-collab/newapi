package model

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionRedemptionKeyFitsExistingColumn(t *testing.T) {
	for _, kind := range []string{"day", "week", "month"} {
		key, err := GenerateSubscriptionRedemptionKey(kind)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(key), 32)
		assert.True(t, strings.HasPrefix(key, "SUB-"+kind[:1]+"-"))
	}
	_, err := GenerateSubscriptionRedemptionKey("year")
	require.Error(t, err)
}

func TestV1SubscriptionPlansAreRedeemOnly(t *testing.T) {
	legacyPlan := &SubscriptionPlan{}
	v1Plan := &SubscriptionPlan{BillingPolicy: SubscriptionBillingPolicyDSFlashV1}
	unknownPolicyPlan := &SubscriptionPlan{BillingPolicy: "future-policy"}

	assert.True(t, IsSubscriptionPlanCustomerPurchasable(legacyPlan))
	assert.False(t, IsSubscriptionPlanCustomerPurchasable(v1Plan))
	assert.False(t, IsSubscriptionPlanCustomerPurchasable(unknownPolicyPlan))
}

func TestSubscriptionV1ReserveSettleAndReplay(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&SubscriptionV1Usage{}))
	t.Cleanup(func() { require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Delete(&SubscriptionV1Usage{}).Error) })
	first, err := ReserveSubscriptionV1Tokens("v1-settle", userID, sub.Id, 24, SubscriptionV1Model, 100, 50)
	require.NoError(t, err)
	assert.False(t, first.Replay)
	assert.Equal(t, sub.DailyInputTokenLimit, first.Usage.InputLimitSnapshot)
	assert.Equal(t, sub.DailyOutputTokenLimit, first.Usage.OutputLimitSnapshot)
	replay, err := ReserveSubscriptionV1Tokens("v1-settle", userID, sub.Id, 24, SubscriptionV1Model, 100, 50)
	require.NoError(t, err)
	assert.True(t, replay.Replay)
	_, err = ReserveSubscriptionV1Tokens("v1-settle", userID, sub.Id, 24, SubscriptionV1Model, 101, 50)
	require.Error(t, err)
	_, err = SettleSubscriptionV1Tokens("v1-settle", userID, sub.Id, 101, 50)
	require.Error(t, err)
	_, err = SettleSubscriptionV1Tokens("v1-settle", userID, sub.Id, 80, 20)
	require.NoError(t, err)
	_, err = SettleSubscriptionV1Tokens("v1-settle", userID, sub.Id, 80, 20)
	require.NoError(t, err)
	_, err = RefundSubscriptionV1Tokens("v1-settle", userID, sub.Id)
	require.Error(t, err)
	_, err = SettleSubscriptionV1Tokens("v1-settle", userID, sub.Id, 79, 20)
	require.Error(t, err)
	_, err = SettleSubscriptionV1Tokens("v1-settle", userID+1, sub.Id, 80, 20)
	require.Error(t, err)
	require.NoError(t, DB.First(sub, sub.Id).Error)
	assert.EqualValues(t, 80, sub.DailyInputTokensUsed)
	assert.EqualValues(t, 20, sub.DailyOutputTokensUsed)
}

func TestSubscriptionV1RefundDoesNotCreditAnotherWindow(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&SubscriptionV1Usage{}))
	t.Cleanup(func() { require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Delete(&SubscriptionV1Usage{}).Error) })
	_, err = ReserveSubscriptionV1Tokens("v1-refund", userID, sub.Id, 24, SubscriptionV1Model, 100, 50)
	require.NoError(t, err)
	require.NoError(t, DB.First(sub, sub.Id).Error)
	beforeEpoch := sub.UsageEpoch
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return resetUserSubscriptionTx(tx, sub, &SubscriptionPlan{}, common.GetTimestamp(), false)
	}))
	assert.Equal(t, beforeEpoch+1, sub.UsageEpoch)
	require.NoError(t, DB.Model(sub).Updates(map[string]any{"daily_input_tokens_used": 7, "daily_output_tokens_used": 3}).Error)
	_, err = RefundSubscriptionV1Tokens("v1-refund", userID, sub.Id)
	require.NoError(t, err)
	_, err = RefundSubscriptionV1Tokens("v1-refund", userID, sub.Id)
	require.NoError(t, err)
	require.NoError(t, DB.First(sub, sub.Id).Error)
	assert.EqualValues(t, 7, sub.DailyInputTokensUsed)
	assert.EqualValues(t, 3, sub.DailyOutputTokensUsed)
	_, err = SettleSubscriptionV1Tokens("v1-refund", userID, sub.Id, 0, 0)
	require.Error(t, err)
}

func TestSubscriptionV1ReservationBoundsAndConcurrentReplay(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&SubscriptionV1Usage{}))
	t.Cleanup(func() { require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Delete(&SubscriptionV1Usage{}).Error) })
	for _, tokens := range []int64{-1, 2_147_483_648, 50_000_001} {
		_, err := ReserveSubscriptionV1Tokens("v1-bounds", userID, sub.Id, 24, SubscriptionV1Model, tokens, 1)
		require.Error(t, err)
	}
	_, err = ReserveSubscriptionV1Tokens("V1-UPPER", userID, sub.Id, 24, SubscriptionV1Model, 1, 1)
	require.Error(t, err)
	_, err = ReserveSubscriptionV1Tokens("v1-channel", userID, sub.Id, 25, SubscriptionV1Model, 1, 1)
	require.Error(t, err)
	type outcome struct { result *SubscriptionV1UsageResult; err error }
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			result, err := ReserveSubscriptionV1Tokens("v1-concurrent", userID, sub.Id, 24, SubscriptionV1Model, 10, 5)
			results <- outcome{result, err}
		})
	}
	wg.Wait()
	close(results)
	newReservations := 0
	for result := range results {
		require.NoError(t, result.err)
		if !result.result.Replay { newReservations++ }
	}
	assert.Equal(t, 1, newReservations)
	require.NoError(t, DB.First(sub, sub.Id).Error)
	assert.EqualValues(t, 10, sub.DailyInputTokensUsed)
	assert.EqualValues(t, 5, sub.DailyOutputTokensUsed)
}

func TestSubscriptionV1ConcurrentRequestsCannotExceedQuota(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&SubscriptionV1Usage{}))
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Delete(&SubscriptionV1Usage{}).Error)
	})
	require.NoError(t, DB.Model(sub).Updates(map[string]any{
		"daily_input_token_limit": 100, "daily_output_token_limit": 50,
	}).Error)
	errorsFound := make(chan error, 2)
	var wg sync.WaitGroup
	for _, requestID := range []string{"v1-quota-a", "v1-quota-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, reserveErr := ReserveSubscriptionV1Tokens(id, userID, sub.Id, 24, SubscriptionV1Model, 60, 30)
			errorsFound <- reserveErr
		}(requestID)
	}
	wg.Wait()
	close(errorsFound)
	successes := 0
	for reserveErr := range errorsFound {
		if reserveErr == nil {
			successes++
		} else {
			require.ErrorIs(t, reserveErr, ErrSubscriptionV1Quota)
		}
	}
	assert.Equal(t, 1, successes)
	require.NoError(t, DB.First(sub, sub.Id).Error)
	assert.EqualValues(t, 60, sub.DailyInputTokensUsed)
	assert.EqualValues(t, 30, sub.DailyOutputTokensUsed)
	var records int64
	require.NoError(t, DB.Model(&SubscriptionV1Usage{}).Where("user_subscription_id = ?", sub.Id).Count(&records).Error)
	assert.EqualValues(t, 1, records)
}

func TestSubscriptionV1SnapshotsAndRejectsLegacyConsumption(t *testing.T) {
	userID, code, plan := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionBillingPolicyDSFlashV1, sub.BillingPolicy)
	assert.Equal(t, 24, sub.ServiceChannelID)
	assert.Equal(t, "deepseek-v4.1-flash", sub.ServiceModel)
	assert.EqualValues(t, 50_000_000, sub.DailyInputTokenLimit)
	assert.EqualValues(t, 10_000_000, sub.DailyOutputTokenLimit)
	require.NoError(t, DB.Model(plan).Update("daily_input_token_limit", 1).Error)
	var stored UserSubscription
	require.NoError(t, DB.First(&stored, sub.Id).Error)
	assert.EqualValues(t, 50_000_000, stored.DailyInputTokenLimit)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	t.Cleanup(func() {
		require.NoError(t, DB.Where("request_id = ?", "subscription-v1-legacy-test").Delete(&SubscriptionPreConsumeRecord{}).Error)
	})
	_, err = PreConsumeUserSubscription("subscription-v1-legacy-test", userID, "deepseek-v4.1-flash", 0, 1)
	require.Error(t, err)
	// Even a historical/pre-existing request record cannot bypass the policy check.
	require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{RequestId: "subscription-v1-legacy-test", UserId: userID, UserSubscriptionId: sub.Id, PreConsumed: 1, Status: "consumed"}).Error)
	_, err = PreConsumeUserSubscription("subscription-v1-legacy-test", userID, "deepseek-v4.1-flash", 0, 1)
	require.Error(t, err)
	require.Error(t, PostConsumeUserSubscriptionDelta(sub.Id, 1))
	require.NoError(t, DB.First(&stored, sub.Id).Error)
	assert.Zero(t, stored.AmountUsed)
	assert.Zero(t, stored.DailyInputTokensUsed)
}

func TestGetActiveSubscriptionV1ReturnsOnlyEligibleEntitlement(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)

	active, found, err := GetActiveSubscriptionV1(userID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, sub.Id, active.Id)

	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", sub.Id).Update("status", "expired").Error)
	active, found, err = GetActiveSubscriptionV1(userID)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, active)
}

func TestReserveActiveSubscriptionV1SkipsExhaustedCardAndRejectsReplay(t *testing.T) {
	userID, code, _ := setupSubscriptionRedemptionFixture(t)
	first, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&SubscriptionV1Usage{}))
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_subscription_id IN ?", []int{first.Id}).Delete(&SubscriptionV1Usage{}).Error)
	})
	require.NoError(t, DB.Model(first).Updates(map[string]any{
		"daily_input_token_limit": 100, "daily_output_token_limit": 50,
		"daily_input_tokens_used": 100, "daily_output_tokens_used": 50,
	}).Error)

	second := *first
	second.Id = 0
	second.DailyInputTokensUsed = 0
	second.DailyOutputTokensUsed = 0
	second.EndTime += 3600
	require.NoError(t, DB.Create(&second).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_subscription_id = ?", second.Id).Delete(&SubscriptionV1Usage{}).Error)
		require.NoError(t, DB.Delete(&second).Error)
	})

	reserved, err := ReserveActiveSubscriptionV1Tokens("v1-active-card", userID, SubscriptionV1ChannelID, SubscriptionV1Model, 20, 10)
	require.NoError(t, err)
	assert.Equal(t, second.Id, reserved.Usage.UserSubscriptionID)
	replay, err := ReserveActiveSubscriptionV1Tokens("v1-active-card", userID, SubscriptionV1ChannelID, SubscriptionV1Model, 20, 10)
	require.NoError(t, err)
	assert.True(t, replay.Replay)
	assert.Equal(t, second.Id, replay.Usage.UserSubscriptionID)
}

func TestSubscriptionV1ValidatesCompletePolicy(t *testing.T) {
	for _, scenario := range []string{"channel", "model", "input", "output", "policy", "wallet", "reset"} {
		t.Run(scenario, func(t *testing.T) {
			userID, _, plan := setupSubscriptionRedemptionFixture(t)
			switch scenario {
			case "channel":
				plan.ServiceChannelID = 25
			case "model":
				plan.ServiceModel = "other"
			case "input":
				plan.DailyInputTokenLimit = 0
			case "output":
				plan.DailyOutputTokenLimit = -1
			case "policy":
				plan.BillingPolicy = "unknown"
			case "wallet":
				plan.AllowWalletOverflow = nil
			case "reset":
				plan.QuotaResetPeriod = SubscriptionResetNever
			}
			err := DB.Transaction(func(tx *gorm.DB) error {
				_, err := CreateUserSubscriptionFromPlanTx(tx, userID, plan, "test")
				return err
			})
			require.Error(t, err)
		})
	}
}

func TestSubscriptionV1DailyResetUsesSnapshotAndIsIdempotent(t *testing.T) {
	userID, code, plan := setupSubscriptionRedemptionFixture(t)
	require.NoError(t, DB.Model(plan).Update("duration_value", 7).Error)
	require.NoError(t, DB.Model(code).Update("subscription_kind", "week").Error)
	sub, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	now := common.GetTimestamp()
	sub.StartTime = now - 2*86400
	sub.LastResetTime = now - 2*86400
	sub.NextResetTime = now - 86400
	sub.DailyInputTokensUsed = 300
	sub.DailyOutputTokensUsed = 40
	require.NoError(t, DB.Save(sub).Error)
	// Editing the template must not disable resets on an existing V1 instance.
	plan.QuotaResetPeriod = SubscriptionResetNever
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return maybeResetUserSubscriptionWithPlanTx(tx, sub, plan, now)
	}))
	assert.Zero(t, sub.DailyInputTokensUsed)
	assert.Zero(t, sub.DailyOutputTokensUsed)
	sub.DailyInputTokensUsed = 7
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return maybeResetUserSubscriptionWithPlanTx(tx, sub, plan, now)
	}))
	assert.EqualValues(t, 7, sub.DailyInputTokensUsed)
	assert.Greater(t, sub.NextResetTime, now)
	assert.LessOrEqual(t, sub.NextResetTime, now+int64((24*time.Hour)/time.Second))
	sub.DailyOutputTokensUsed = 8
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return resetUserSubscriptionTx(tx, sub, plan, now, false)
	}))
	assert.Zero(t, sub.DailyInputTokensUsed)
	assert.Zero(t, sub.DailyOutputTokensUsed)
}

func TestLegacySubscriptionCreationKeepsUnifiedQuota(t *testing.T) {
	userID, _, plan := setupSubscriptionRedemptionFixture(t)
	plan.BillingPolicy = ""
	plan.ServiceChannelID = 0
	plan.ServiceModel = ""
	plan.DailyInputTokenLimit = 0
	plan.DailyOutputTokenLimit = 0
	plan.AllowWalletOverflow = nil
	var sub *UserSubscription
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		sub, err = CreateUserSubscriptionFromPlanTx(tx, userID, plan, "test")
		return err
	})
	require.NoError(t, err)
	assert.Empty(t, sub.BillingPolicy)
	assert.True(t, sub.AllowWalletOverflow)
	assert.Equal(t, plan.TotalAmount, sub.AmountTotal)
}

func TestSubscriptionRedemptionCannotExpandServiceBinding(t *testing.T) {
	_, err := NewSubscriptionRedemption("day", 1, 25, "deepseek-v4.1-flash", "test", 0)
	require.Error(t, err)
	_, err = NewSubscriptionRedemption("day", 1, 24, "other-model", "test", 0)
	require.Error(t, err)
	code := &Redemption{Type: "unknown", Key: "unknown-type", Quota: 100}
	require.Error(t, code.Insert())
}

func TestSubscriptionRedemptionBatchRollsBackOnKeyCollision(t *testing.T) {
	_, _, plan := setupSubscriptionRedemptionFixture(t)
	first, err := NewSubscriptionRedemption("day", plan.Id, SubscriptionV1ChannelID, SubscriptionV1Model, "batch-test", 0)
	require.NoError(t, err)
	second := *first
	second.Id = 0
	var cards []string
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := insertSubscriptionRedemptionsTx(tx, plan, []*Redemption{first, &second}); err != nil {
			return err
		}
		cards = append(cards, first.Key, second.Key)
		return nil
	})
	require.Error(t, err)
	assert.Empty(t, cards)
	var count int64
	require.NoError(t, DB.Model(&Redemption{}).Where("name = ?", "batch-test").Count(&count).Error)
	assert.Zero(t, count)
}

func TestRedeemRejectsNonWalletTypesWithoutConsuming(t *testing.T) {
	for _, kind := range []string{RedemptionTypeSubscription, "unknown"} {
		t.Run(kind, func(t *testing.T) {
			userID, key := setupRedeemFixture(t, 500)
			require.NoError(t, DB.Model(&Redemption{}).Where("name = ?", "redeem-test").Update("type", kind).Error)
			_, err := Redeem(key, userID)
			require.ErrorIs(t, err, ErrRedeemFailed)
			var user User
			require.NoError(t, DB.First(&user, userID).Error)
			assert.Zero(t, user.Quota)
			var code Redemption
			require.NoError(t, DB.Where("name = ?", "redeem-test").First(&code).Error)
			assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
		})
	}
}

func setupSubscriptionRedemptionFixture(t *testing.T) (int, *Redemption, *SubscriptionPlan) {
	t.Helper()
	userID, _ := setupRedeemFixture(t, 500)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
	plan := &SubscriptionPlan{Title: "DS day", Enabled: true, DurationUnit: SubscriptionDurationDay, DurationValue: 1, TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetDaily, AllowWalletOverflow: common.GetPointer(false), BillingPolicy: SubscriptionBillingPolicyDSFlashV1, ServiceChannelID: 24, ServiceModel: "deepseek-v4.1-flash", DailyInputTokenLimit: 50_000_000, DailyOutputTokenLimit: 10_000_000}
	require.NoError(t, DB.Create(plan).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Where("plan_id = ?", plan.Id).Delete(&UserSubscription{}).Error)
		require.NoError(t, DB.Delete(plan).Error)
	})
	code, err := NewSubscriptionRedemption("day", plan.Id, 24, "deepseek-v4.1-flash", "subscription-test", 0)
	require.NoError(t, err)
	require.NoError(t, code.Insert())
	return userID, code, plan
}

func TestSubscriptionRedemptionRejectsInvalidStateAtomically(t *testing.T) {
	for _, scenario := range []string{"disabled-plan", "missing-user", "disabled-user", "expired", "wrong-channel", "wrong-model", "wrong-kind", "overflow-enabled", "group-upgrade", "missing-plan", "wallet-card", "no-daily-reset"} {
		t.Run(scenario, func(t *testing.T) {
			userID, code, plan := setupSubscriptionRedemptionFixture(t)
			switch scenario {
			case "disabled-plan":
				require.NoError(t, DB.Model(plan).Update("enabled", false).Error)
			case "missing-user":
				require.NoError(t, DB.Delete(&User{}, userID).Error)
			case "disabled-user":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Update("status", common.UserStatusDisabled).Error)
			case "expired":
				require.NoError(t, DB.Model(code).Update("expired_time", common.GetTimestamp()-1).Error)
			case "wrong-channel":
				require.NoError(t, DB.Model(code).Update("service_channel_id", 25).Error)
			case "wrong-model":
				require.NoError(t, DB.Model(code).Update("service_model", "other").Error)
			case "wrong-kind":
				require.NoError(t, DB.Model(plan).Update("duration_value", 7).Error)
			case "overflow-enabled":
				require.NoError(t, DB.Model(plan).Update("allow_wallet_overflow", true).Error)
			case "group-upgrade":
				require.NoError(t, DB.Model(plan).Update("upgrade_group", "vip").Error)
			case "missing-plan":
				require.NoError(t, DB.Delete(plan).Error)
			case "wallet-card":
				require.NoError(t, DB.Model(code).Update("type", RedemptionTypeWallet).Error)
			case "no-daily-reset":
				require.NoError(t, DB.Model(plan).Update("quota_reset_period", SubscriptionResetNever).Error)
			}
			_, err := RedeemSubscription(code.Key, userID)
			require.ErrorIs(t, err, ErrRedeemFailed)
			var stored Redemption
			require.NoError(t, DB.First(&stored, code.Id).Error)
			assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
			var count int64
			require.NoError(t, DB.Model(&UserSubscription{}).Where("plan_id = ?", plan.Id).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestSubscriptionRedemptionConcurrentSingleSuccess(t *testing.T) {
	userID, code, plan := setupSubscriptionRedemptionFixture(t)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := RedeemSubscription(code.Key, userID)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success)
	var subs []UserSubscription
	require.NoError(t, DB.Where("plan_id = ?", plan.Id).Find(&subs).Error)
	require.Len(t, subs, 1)
	assert.False(t, subs[0].AllowWalletOverflow)
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	assert.Zero(t, user.Quota)
	_, err := RedeemSubscription(code.Key, userID)
	require.ErrorIs(t, err, ErrRedeemFailed)
}

func TestSubscriptionRedemptionPurchaseLimitRollsBackCode(t *testing.T) {
	userID, code, plan := setupSubscriptionRedemptionFixture(t)
	require.NoError(t, DB.Model(plan).Update("max_purchase_per_user", 1).Error)
	_, err := RedeemSubscription(code.Key, userID)
	require.NoError(t, err)
	second, err := NewSubscriptionRedemption("day", plan.Id, 24, "deepseek-v4.1-flash", "second", 0)
	require.NoError(t, err)
	require.NoError(t, second.Insert())
	_, err = RedeemSubscription(second.Key, userID)
	require.ErrorIs(t, err, ErrRedeemFailed)
	var stored Redemption
	require.NoError(t, DB.First(&stored, second.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("plan_id = ?", plan.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "00000000000000000000000000000004", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{
			name:      "no filters returns all rows",
			num:       10,
			wantTotal: 5,
			wantIds:   []int{5, 4, 3, 2, 1},
		},
		{
			name:      "keyword filters by name prefix",
			keyword:   "alpha",
			num:       10,
			wantTotal: 3,
			wantIds:   []int{3, 2, 1},
		},
		{
			name:      "enabled status excludes expired rows",
			status:    "1",
			num:       10,
			wantTotal: 2,
			wantIds:   []int{2, 1},
		},
		{
			name:      "expired status returns enabled expired rows",
			status:    "expired",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{3},
		},
		{
			name:      "disabled status",
			status:    "2",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "used status",
			status:    "3",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{5},
		},
		{
			name:      "pagination keeps unpaged total",
			startIdx:  1,
			num:       2,
			wantTotal: 5,
			wantIds:   []int{4, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}
}

func setupRedeemFixture(t *testing.T, quota int) (userId int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{Username: "redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)

	key = "10000000000000000000000000000001"
	redemption := &Redemption{
		Name:        "redeem-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userId, key := setupRedeemFixture(t, 500)

	quota, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "name = ?", "redeem-test").Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userId, redemption.UsedUserId)

	// Redeeming the same code again must fail and must not credit quota.
	_, err = Redeem(key, userId)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
}

func TestRedeemRejectsWalletOverflow(t *testing.T) {
	userId, key := setupRedeemFixture(t, 11)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).Update("quota", common.MaxWalletQuota-10).Error)

	_, err := Redeem(key, userId)
	require.ErrorIs(t, err, ErrRedeemFailed)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, common.MaxWalletQuota-10, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
}

func TestRedemptionQuotaRejectsWalletOverflow(t *testing.T) {
	setupRedeemFixture(t, 500)

	redemption := &Redemption{
		Name:        "overflow-redemption",
		Key:         "10000000000000000000000000000002",
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       common.MaxWalletQuota + 1,
		CreatedTime: common.GetTimestamp(),
	}
	require.Error(t, redemption.Insert())
}

// Exactly one of several concurrent redeems of the same code may win, and
// quota must be credited exactly once.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userId, key := setupRedeemFixture(t, 300)

	const goroutines = 5
	successes := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			if _, err := Redeem(key, userId); err == nil {
				successes[idx] = true
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redeem should succeed")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}
