package service

import (
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/model"
)

func deleteForwardRows(tx *gorm.DB, forwardID int64) error {
	return deleteForwardRowsByIDs(tx, []int64{forwardID})
}

func deleteForwardRowsByIDs(tx *gorm.DB, forwardIDs []int64) error {
	if len(forwardIDs) == 0 {
		return nil
	}
	if tx == nil {
		tx = model.DB
	}
	if err := tx.Where("forward_id IN ?", forwardIDs).Delete(&model.ForwardExitMember{}).Error; err != nil {
		return err
	}
	return tx.Where("id IN ?", forwardIDs).Delete(&model.Forward{}).Error
}
