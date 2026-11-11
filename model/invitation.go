package model

import (
	"crypto/rand"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	InvitationEnabled  = 1
	InvitationDisabled = 2
	InvitationUsed     = 3
)

var ErrInvitationInvalid = errors.New("Invitation code is invalid or unavailable")

type Invitation struct {
	Id          int    `json:"id"`
	UserId      int    `json:"user_id"`
	Code        string `json:"key" gorm:"type:varchar(64);not null;uniqueIndex"`
	Name        string `json:"name" gorm:"type:varchar(80);not null;index"`
	Status      int    `json:"status" gorm:"not null;index"`
	CreatedTime int64  `json:"created_time"`
	ExpiredTime int64  `json:"expired_time"`
	UsedTime    int64  `json:"used_time"`
	UsedUserId  int    `json:"used_user_id"`
}

func CreateInvitations(userID int, name string, count int, expires int64) ([]string, error) {
	rows := make([]Invitation, count)
	keys := make([]string, count)
	for i := range count {
		keys[i] = rand.Text()
		rows[i] = Invitation{UserId: userID, Code: keys[i], Name: name, Status: InvitationEnabled, CreatedTime: common.GetTimestamp(), ExpiredTime: expires}
	}
	err := DB.Transaction(func(tx *gorm.DB) error { return tx.Create(&rows).Error })
	return keys, err
}

func ListInvitations(keyword, status string, offset, limit int) ([]Invitation, int64, error) {
	query := DB.Model(&Invitation{})
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword) + "%"
		query = query.Where("name LIKE ? ESCAPE '!' OR code = ?", pattern, keyword)
	}
	now := common.GetTimestamp()
	switch status {
	case "expired":
		query = query.Where("status = ? AND expired_time <> 0 AND expired_time <= ?", InvitationEnabled, now)
	case "1":
		query = query.Where("status = ? AND (expired_time = 0 OR expired_time > ?)", InvitationEnabled, now)
	case "2", "3":
		value, _ := strconv.Atoi(status)
		query = query.Where("status = ?", value)
	}
	var total int64
	var rows []Invitation
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// InsertUserWithInvitation claims the code before any reads or account writes.
// The caller owns the transaction, including any OAuth binding. Conditional
// updates serialize concurrent claims even on SQLite, where FOR UPDATE is absent.
func InsertUserWithInvitation(tx *gorm.DB, user *User, inviterID int, code string) error {
	if !common.InviteRegisterEnabled {
		return user.InsertWithTx(tx, inviterID)
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return ErrInvitationInvalid
	}
	now := common.GetTimestamp()
	result := tx.Model(&Invitation{}).Where("code = ? AND status = ? AND (expired_time = 0 OR expired_time > ?)", code, InvitationEnabled, now).
		Updates(map[string]any{"status": InvitationUsed, "used_time": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvitationInvalid
	}
	var invitation Invitation
	if err := lockForUpdate(tx).Where("code = ?", code).First(&invitation).Error; err != nil {
		return err
	}
	if err := user.InsertWithTx(tx, inviterID); err != nil {
		return err
	}
	return tx.Model(&invitation).Update("used_user_id", user.Id).Error
}

func UpdateInvitation(id int, name string, expires int64, status *int) error {
	changes := map[string]any{"name": name, "expired_time": expires}
	if status != nil {
		changes = map[string]any{"status": *status}
	}
	result := DB.Model(&Invitation{}).Where("id = ? AND status <> ?", id, InvitationUsed).Updates(changes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return common.NewMessage("Invitation code cannot be changed")
	}
	return nil
}

func DeleteInvitations(ids []int, invalid bool) (int64, error) {
	query := DB
	if invalid {
		query = query.Where("status IN ? OR (expired_time <> 0 AND expired_time <= ?)", []int{InvitationDisabled, InvitationUsed}, common.GetTimestamp())
	} else {
		query = query.Where("id IN ?", ids)
	}
	result := query.Delete(&Invitation{})
	return result.RowsAffected, result.Error
}
