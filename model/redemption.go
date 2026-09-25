package model

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type Redemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:100"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
	// Subscription fields are intentionally separate from wallet quota redemptions.
	Type               string `json:"type" gorm:"type:varchar(16);not null;default:'wallet';index"`
	SubscriptionPlanID int    `json:"subscription_plan_id" gorm:"index"`
	ServiceChannelID   int    `json:"service_channel_id" gorm:"index"`
	ServiceModel       string `json:"service_model" gorm:"type:varchar(128);default:''"`
	SubscriptionKind   string `json:"subscription_kind" gorm:"type:varchar(8);default:''"`
}

const (
	RedemptionTypeWallet       = "wallet"
	RedemptionTypeSubscription = "subscription"
	SubscriptionV1ChannelID    = 24
	SubscriptionV1Model        = "deepseek-v4.1-flash"
)

// GenerateSubscriptionRedemptionKey returns a cryptographically random, typed key.
// The prefix is only a display aid; the database Type/Plan fields are authoritative.
func GenerateSubscriptionRedemptionKey(kind string) (string, error) {
	if kind != "day" && kind != "week" && kind != "month" {
		return "", errors.New("invalid subscription kind")
	}
	// 128 random bits become 26 case-insensitive characters, plus a 6-byte prefix.
	// This fits char(32), including on databases with case-insensitive collation.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "SUB-" + kind[:1] + "-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// NewSubscriptionRedemption creates an unredeemed code bound to one plan/service.
// Subscription cards do not have a separate pre-redemption expiry; their
// duration starts when RedeemSubscription creates the user entitlement.
// Caller persists it with Insert; the unique key index protects against collisions.
func NewSubscriptionRedemption(kind string, planID, channelID int, model string, name string, expires int64) (*Redemption, error) {
	key, err := GenerateSubscriptionRedemptionKey(kind)
	if err != nil || planID <= 0 || channelID != SubscriptionV1ChannelID || model != SubscriptionV1Model || expires < 0 {
		return nil, errors.New("invalid subscription redemption binding")
	}
	return &Redemption{Key: key, Name: name, Type: RedemptionTypeSubscription, SubscriptionKind: kind, SubscriptionPlanID: planID, ServiceChannelID: SubscriptionV1ChannelID, ServiceModel: SubscriptionV1Model, Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), ExpiredTime: 0}, nil
}

func insertSubscriptionRedemptionsTx(tx *gorm.DB, plan *SubscriptionPlan, cards []*Redemption) error {
	if tx == nil || plan == nil || len(cards) == 0 {
		return errors.New("invalid subscription redemption batch")
	}
	for _, card := range cards {
		if card == nil || card.Type != RedemptionTypeSubscription || card.SubscriptionPlanID != plan.Id {
			return errors.New("invalid subscription redemption binding")
		}
		if err := validateSubscriptionRedemptionPlan(card, plan); err != nil {
			return err
		}
		if err := tx.Create(card).Error; err != nil {
			return err
		}
	}
	return nil
}

// CreateSubscriptionRedemptions creates one plan-bound batch atomically.
func CreateSubscriptionRedemptions(kind string, planID, count int, name string, expires int64) ([]string, error) {
	if planID <= 0 || count < 1 || count > 100 || expires < 0 {
		return nil, errors.New("invalid subscription redemption batch")
	}
	cards := make([]*Redemption, 0, count)
	for range count {
		card, err := NewSubscriptionRedemption(kind, planID, SubscriptionV1ChannelID, SubscriptionV1Model, name, expires)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	keys := make([]string, 0, count)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var plan SubscriptionPlan
		if err := tx.First(&plan, planID).Error; err != nil {
			return err
		}
		if err := insertSubscriptionRedemptionsTx(tx, &plan, cards); err != nil {
			return err
		}
		for _, card := range cards {
			keys = append(keys, card.Key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// validateSubscriptionRedemptionPlan fails closed for legacy drafts without a kind.
// Kind is persisted independently: the human-readable key prefix is not authority.
func validateSubscriptionRedemptionPlan(code *Redemption, plan *SubscriptionPlan) error {
	if plan.BillingPolicy != SubscriptionBillingPolicyDSFlashV1 {
		return ErrRedeemFailed
	}
	if err := plan.ValidateBillingPolicy(); err != nil {
		return err
	}
	days := 0
	switch code.SubscriptionKind {
	case "day":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	default:
		return ErrRedeemFailed
	}
	if code.ServiceChannelID != SubscriptionV1ChannelID || code.ServiceModel != SubscriptionV1Model ||
		!plan.Enabled || plan.DurationUnit != SubscriptionDurationDay || plan.DurationValue != days ||
		plan.QuotaResetPeriod != SubscriptionResetDaily ||
		plan.AllowWalletOverflow == nil || *plan.AllowWalletOverflow ||
		plan.UpgradeGroup != "" || plan.DowngradeGroup != "" {
		return ErrRedeemFailed
	}
	return nil
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND type != ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				RedemptionTypeSubscription,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (type = ? OR expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				RedemptionTypeSubscription,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (quota int, err error) {
	if key == "" {
		return 0, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return 0, errors.New("无效的 user id")
	}
	redemption := &Redemption{}

	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		// Empty is a legacy wallet row. Unknown types must never credit money.
		if redemption.Type != "" && redemption.Type != RedemptionTypeWallet {
			return ErrRedeemFailed
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]any{
				"redeemed_time": common.GetTimestamp(),
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("该兑换码已被使用")
		}
		return creditTopUpQuota(tx, userId, redemption.Quota, nil)
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return 0, ErrRedeemFailed
	}
	syncCreditUserQuotaCache(userId, redemption.Quota, "redemption")
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	return redemption.Quota, nil
}

// RedeemSubscription atomically consumes a subscription key and creates its plan.
// It never credits wallet quota and enforces the service binding stored on the key.
func RedeemSubscription(key string, userId int) (*UserSubscription, error) {
	if key == "" || userId <= 0 {
		return nil, ErrRedeemFailed
	}
	// Tests and lightweight embedded DB users can install model.DB directly
	// without going through InitDB. Keep the quoted key column available for
	// the redemption transaction in that case as well.
	if commonKeyCol == "" {
		initCol()
	}
	var sub *UserSubscription
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Serialize different codes for one user as well, protecting plan purchase caps.
		var user User
		if err := lockForUpdate(tx).Select("id", "status").First(&user, userId).Error; err != nil || user.Status != common.UserStatusEnabled {
			return ErrRedeemFailed
		}
		var code Redemption
		if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(&code).Error; err != nil {
			return err
		}
		if code.Type != RedemptionTypeSubscription || code.Status != common.RedemptionCodeStatusEnabled {
			return ErrRedeemFailed
		}
		if code.SubscriptionPlanID <= 0 {
			return ErrRedeemFailed
		}
		var plan SubscriptionPlan
		if err := lockForUpdate(tx).First(&plan, code.SubscriptionPlanID).Error; err != nil {
			return err
		}
		if err := validateSubscriptionRedemptionPlan(&code, &plan); err != nil {
			return err
		}
		if r := tx.Model(&Redemption{}).Where("id = ? AND status = ?", code.Id, common.RedemptionCodeStatusEnabled).Updates(map[string]any{"status": common.RedemptionCodeStatusUsed, "redeemed_time": common.GetTimestamp(), "used_user_id": userId}); r.Error != nil || r.RowsAffected != 1 {
			return ErrRedeemFailed
		}
		var err error
		sub, err = CreateUserSubscriptionFromPlanTx(tx, userId, &plan, "redemption")
		return err
	})
	if err != nil {
		return nil, ErrRedeemFailed
	}
	return sub, nil
}

func (redemption *Redemption) Insert() error {
	if redemption.Type == "" {
		redemption.Type = RedemptionTypeWallet
	}
	if redemption.Type != RedemptionTypeWallet && redemption.Type != RedemptionTypeSubscription {
		return errors.New("invalid redemption type")
	}
	if len(redemption.Key) == 0 || len(redemption.Key) > 32 {
		return errors.New("redemption key must contain 1 to 32 bytes")
	}
	if redemption.Type == RedemptionTypeSubscription {
		if redemption.SubscriptionPlanID <= 0 || redemption.ExpiredTime < 0 {
			return ErrRedeemFailed
		}
		// Subscription duration starts at redemption, so no new subscription
		// row may carry an independent pre-redemption expiry.
		redemption.ExpiredTime = 0
		var plan SubscriptionPlan
		if err := DB.First(&plan, redemption.SubscriptionPlanID).Error; err != nil {
			return err
		}
		if err := validateSubscriptionRedemptionPlan(redemption, &plan); err != nil {
			return err
		}
		// GORM's legacy quota default may fill this column; Type is the wallet guard.
		redemption.Quota = 0
	}
	if redemption.Type != RedemptionTypeSubscription && redemption.Quota <= 0 {
		return errors.New("redemption quota must be positive")
	}
	if redemption.Type != RedemptionTypeSubscription {
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			return err
		}
	}
	var err error
	err = DB.Create(redemption).Error
	return err
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	if redemption.Quota <= 0 {
		return errors.New("redemption quota must be positive")
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		return err
	}
	var err error
	err = DB.Model(redemption).Select("name", "status", "quota", "redeemed_time", "expired_time").Updates(redemption).Error
	return err
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND type != ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, RedemptionTypeSubscription, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}

// BatchDeleteRedemptions soft-deletes the selected codes in one statement.
func BatchDeleteRedemptions(ids []int) (int64, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, errors.New("select between 1 and 1000 redemption codes")
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, errors.New("redemption IDs must be positive")
		}
	}
	result := DB.Where("id IN ?", ids).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
