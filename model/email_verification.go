package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmailVerification is shared by all API nodes. Never expose its hash in an API.
type EmailVerification struct {
	ID        string `gorm:"type:varchar(64);primaryKey"`
	Email     string `gorm:"type:varchar(254);not null"`
	CodeHash  string `gorm:"type:varchar(128);not null" json:"-"`
	ExpiresAt int64  `gorm:"index;not null"`
	Attempts  int    `gorm:"not null"`
	Consumed  bool   `gorm:"not null"`
}

func emailVerificationID(email, purpose string) string {
	sum := sha256.Sum256([]byte(purpose + "\x00" + strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func StoreEmailVerification(db *gorm.DB, email, purpose, code string, ttl time.Duration) error {
	if email == "" || purpose == "" || code == "" || ttl <= 0 {
		return errors.New("invalid verification challenge")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if err := db.Where("expires_at <= ?", now).Delete(&EmailVerification{}).Error; err != nil {
		return err
	}
	row := EmailVerification{ID: emailVerificationID(email, purpose), Email: strings.ToLower(strings.TrimSpace(email)), CodeHash: string(hash), ExpiresAt: time.Now().Add(ttl).Unix()}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"email", "code_hash", "expires_at", "attempts", "consumed"})}).Create(&row).Error
}

// ConsumeEmailVerification reserves an attempt before comparing the code, then
// atomically consumes this exact generation. No read/modify/write counters and
// no process-local fallback: a database failure must never bypass verification.
func ConsumeEmailVerification(db *gorm.DB, email, purpose, code string) (bool, error) {
	var row EmailVerification
	id := emailVerificationID(email, purpose)
	err := db.Where("id = ? AND consumed = ? AND expires_at > ? AND attempts < ?", id, false, time.Now().Unix(), 5).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result := db.Model(&EmailVerification{}).Where("id = ? AND code_hash = ? AND consumed = ? AND expires_at > ? AND attempts < ?", id, row.CodeHash, false, time.Now().Unix(), 5).UpdateColumn("attempts", gorm.Expr("attempts + 1"))
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 || bcrypt.CompareHashAndPassword([]byte(row.CodeHash), []byte(code)) != nil {
		return false, nil
	}
	result = db.Model(&EmailVerification{}).Where("id = ? AND code_hash = ? AND consumed = ? AND expires_at > ?", id, row.CodeHash, false, time.Now().Unix()).UpdateColumn("consumed", true)
	return result.RowsAffected == 1, result.Error
}
