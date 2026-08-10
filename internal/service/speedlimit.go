package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"gorm.io/gorm"
)

var (
	addSpeedLimitRemote    = gost.AddLimitersLifecycle
	updateSpeedLimitRemote = gost.UpdateLimitersLifecycle
	deleteSpeedLimitRemote = gost.DeleteLimitersLifecycle
)

// convertBitsToMBps Mbps -> MB/s（保留 1 位小数，与 Java convertBitsToMBps 一致）
func convertBitsToMBps(speedInBits int) string {
	mbs := math.Round(float64(speedInBits)/8.0*10) / 10
	return fmt.Sprintf("%g", mbs)
}

// CreateSpeedLimit 创建限速规则并下发限速器
func CreateSpeedLimit(req dto.SpeedLimitDto) result.R {
	tunnel, unlock, err := lockTunnelSagaSnapshot(req.TunnelID)
	if err != nil {
		return result.Err("指定的隧道不存在")
	}

	now := time.Now().UnixMilli()
	sl := model.SpeedLimit{
		Name: req.Name, Speed: req.Speed,
		TunnelID: req.TunnelID, TunnelName: req.TunnelName,
		CreatedTime: now, UpdatedTime: &now, Status: 1,
	}
	failMsg := ""
	defer unlock()
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&tunnel, req.TunnelID).Error; err != nil {
			failMsg = "指定的隧道不存在"
			return err
		}
		if tunnel.Name != req.TunnelName {
			failMsg = "隧道名称与隧道ID不匹配"
			return errors.New(failMsg)
		}
		return tx.Create(&sl).Error
	})
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("限速规则创建失败")
	}

	res := addSpeedLimitRemote(tunnel.InNodeID, sl.ID, convertBitsToMBps(sl.Speed))
	if !gost.IsOK(res) {
		if res.OutcomeUnknown {
			markNodesDirtyBestEffort(tunnel.InNodeID)
			return result.OkMsg("限速规则创建已接受，等待节点重连同步")
		}
		if err := model.DB.Delete(&model.SpeedLimit{}, sl.ID).Error; err != nil {
			return result.Err(fmt.Sprintf("%s；删除失败的限速规则记录失败: %v", res.Msg, err))
		}
		return result.Err(res.Msg)
	}
	return result.OkEmpty()
}

// GetAllSpeedLimits 限速规则列表
func GetAllSpeedLimits() result.R {
	var list []model.SpeedLimit
	model.DB.Find(&list)
	return result.Ok(list)
}

// UpdateSpeedLimit 更新限速规则并同步限速器
func UpdateSpeedLimit(req dto.SpeedLimitUpdateDto) result.R {
	sl, oldTunnel, tunnel, unlock, failMsg := lockSpeedLimitTunnelSagaSnapshot(req.ID, req.TunnelID)
	if failMsg != "" {
		return result.Err(failMsg)
	}
	defer unlock()
	original := sl

	failMsg = ""
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&sl, req.ID).Error; err != nil {
			failMsg = "限速规则不存在"
			return err
		}
		if err := tx.First(&tunnel, req.TunnelID).Error; err != nil {
			failMsg = "指定的隧道不存在"
			return err
		}
		if tunnel.Name != req.TunnelName {
			failMsg = "隧道名称与隧道ID不匹配"
			return errors.New(failMsg)
		}
		if sl.TunnelID != req.TunnelID {
			var count int64
			if err := tx.Model(&model.UserTunnel{}).Where("speed_id = ?", sl.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				failMsg = "该限速规则已有用户在使用，不能修改关联隧道"
				return errors.New(failMsg)
			}
		}
		now := time.Now().UnixMilli()
		updated := tx.Model(&model.SpeedLimit{}).Where("id = ?", req.ID).Updates(map[string]interface{}{
			"name":         req.Name,
			"speed":        req.Speed,
			"tunnel_id":    req.TunnelID,
			"tunnel_name":  req.TunnelName,
			"updated_time": now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			failMsg = "限速规则不存在"
			return errors.New(failMsg)
		}
		sl.Name = req.Name
		sl.Speed = req.Speed
		sl.TunnelID = req.TunnelID
		sl.TunnelName = req.TunnelName
		sl.UpdatedTime = &now
		return nil
	})
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("限速规则更新失败")
	}

	res := updateSpeedLimitRemote(tunnel.InNodeID, sl.ID, convertBitsToMBps(sl.Speed))
	if strings.Contains(res.Msg, gost.NotFoundMsg) {
		res = addSpeedLimitRemote(tunnel.InNodeID, sl.ID, convertBitsToMBps(sl.Speed))
	}
	if !gost.IsOK(res) {
		if res.OutcomeUnknown {
			markNodesDirtyBestEffort(tunnel.InNodeID)
			return result.OkMsg("限速规则更新已接受，等待节点重连同步")
		}
		// No competing association writer can pass the old/new node saga
		// while it is held, so restoring the exact previous row is safe.
		if err := model.DB.Save(&original).Error; err != nil {
			return result.Err(fmt.Sprintf("%s；恢复限速规则失败: %v", res.Msg, err))
		}
		return result.Err(res.Msg)
	}
	if original.TunnelID != req.TunnelID && oldTunnel.InNodeID != tunnel.InNodeID {
		deleted := deleteSpeedLimitRemote(oldTunnel.InNodeID, sl.ID)
		if !gost.IsOK(deleted) {
			// The durable desired row already points at the new tunnel. The old
			// node reconnect reconciler will remove this now-unreferenced ID.
			markNodesDirtyBestEffort(oldTunnel.InNodeID)
			return result.OkMsg("限速规则更新成功，旧节点清理等待重连同步")
		}
	}
	return result.OkMsg("限速规则更新成功")
}

// lockSpeedLimitTunnelSagaSnapshot locks the stable union of the speed
// limit's current tunnel and the requested target tunnel. A concurrent move
// can change the current tunnel while this caller waits, so expand and retry.
func lockSpeedLimitTunnelSagaSnapshot(speedLimitID, targetTunnelID int64) (model.SpeedLimit, model.Tunnel, model.Tunnel, func(), string) {
	var before model.SpeedLimit
	if err := model.DB.First(&before, speedLimitID).Error; err != nil {
		return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "限速规则不存在"
	}
	var beforeOld, beforeTarget model.Tunnel
	if err := model.DB.First(&beforeOld, before.TunnelID).Error; err != nil {
		return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "原关联隧道不存在"
	}
	if err := model.DB.First(&beforeTarget, targetTunnelID).Error; err != nil {
		return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "指定的隧道不存在"
	}
	lockedNodeIDs := []int64{beforeOld.InNodeID, beforeOld.OutNodeID, beforeTarget.InNodeID, beforeTarget.OutNodeID}
	for {
		lockedNodeIDs = normalizeNodeSagaLockIDs(lockedNodeIDs)
		unlock := lockNftSagaNodes(lockedNodeIDs)
		var current model.SpeedLimit
		if err := model.DB.First(&current, speedLimitID).Error; err != nil {
			unlock()
			return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "限速规则不存在"
		}
		var oldTunnel, targetTunnel model.Tunnel
		if err := model.DB.First(&oldTunnel, current.TunnelID).Error; err != nil {
			unlock()
			return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "原关联隧道不存在"
		}
		if err := model.DB.First(&targetTunnel, targetTunnelID).Error; err != nil {
			unlock()
			return model.SpeedLimit{}, model.Tunnel{}, model.Tunnel{}, nil, "指定的隧道不存在"
		}
		actualNodeIDs := []int64{oldTunnel.InNodeID, oldTunnel.OutNodeID, targetTunnel.InNodeID, targetTunnel.OutNodeID}
		if nodeIDSetContains(lockedNodeIDs, actualNodeIDs) {
			return current, oldTunnel, targetTunnel, unlock, ""
		}
		unlock()
		lockedNodeIDs = append(lockedNodeIDs, actualNodeIDs...)
	}
}

// DeleteSpeedLimit 删除限速规则
func DeleteSpeedLimit(id int64) result.R {
	var before model.SpeedLimit
	if err := model.DB.First(&before, id).Error; err != nil {
		return result.Err("限速规则不存在")
	}
	sl, tunnel, _, unlock, failMsg := lockSpeedLimitTunnelSagaSnapshot(id, before.TunnelID)
	if failMsg != "" {
		return result.Err(failMsg)
	}
	defer unlock()
	failMsg = ""
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&sl, id).Error; err != nil {
			failMsg = "限速规则不存在"
			return err
		}
		var count int64
		if err := tx.Model(&model.UserTunnel{}).Where("speed_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			failMsg = "该限速规则还有用户在使用 请先取消分配"
			return errors.New(failMsg)
		}
		deleted := tx.Delete(&model.SpeedLimit{}, id)
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			failMsg = "限速规则不存在"
			return errors.New(failMsg)
		}
		return nil
	})
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("限速规则删除失败")
	}
	res := deleteSpeedLimitRemote(tunnel.InNodeID, id)
	if !gost.IsOK(res) && !strings.Contains(res.Msg, gost.NotFoundMsg) {
		// Keep the durable desired row when deletion is not definitive. If the
		// agent did delete it before disconnecting, reconnect will recreate it;
		// the caller can retry deletion after the node is healthy.
		if err := model.DB.Create(&sl).Error; err != nil {
			return result.Err(fmt.Sprintf("%s；恢复限速规则失败: %v", res.Msg, err))
		}
		return result.Err(res.Msg)
	}
	return result.OkMsg("限速规则删除成功")
}
