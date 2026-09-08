package service

import (
	"errors"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/model"
)

// deleteOrphanForwardRows 仅清理原隧道已经缺失的孤立转发。
// 在同一事务内重新校验归属和隧道，避免用旧快照误删已迁移到有效隧道的转发；
// 无法还原完整节点路径时不猜测远端规则，只原子删除转发与出口成员。
func deleteOrphanForwardRows(cu CurrentUser, forwardID int64) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var forward model.Forward
		if err := tx.First(&forward, forwardID).Error; err != nil {
			return err
		}
		if cu.RoleID != adminRoleID && forward.UserID != cu.UserID {
			return errors.New("端口转发不存在")
		}
		var tunnel model.Tunnel
		err := tx.First(&tunnel, forward.TunnelID).Error
		if err == nil {
			return errors.New("转发已有有效隧道，请重试")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return deleteForwardRows(tx, forwardID)
	})
}

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
