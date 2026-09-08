package service

import (
	"errors"

	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
)

// applyFlowReportOnce 在计费事务内保存单个上报器的最后一次收据。
// 相同序号只能重放相同内容；失败事务不能提前推进序号，否则会丢失待重传流量。
func applyFlowReportOnce(tx *gorm.DB, nodeID int64, reporterID string, sequence uint64, batchID, digest string, receivedAt int64, apply func() error) error {
	var state model.FlowReporterState
	err := tx.Where("node_id = ? AND reporter_id = ?", nodeID, reporterID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = model.FlowReporterState{NodeID: nodeID, ReporterID: reporterID, UpdatedTime: receivedAt}
		if err := tx.Create(&state).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if sequence == state.LastSequence {
		if batchID != state.LastBatchID || digest != state.LastAckDigest {
			return ErrFlowBatchConflict
		}
		return nil
	}
	if sequence != state.LastSequence+1 {
		return ErrFlowSequence
	}
	if err := apply(); err != nil {
		return err
	}
	updated := tx.Model(&model.FlowReporterState{}).Where("id = ? AND last_sequence = ?", state.ID, state.LastSequence).
		Updates(map[string]any{
			"last_sequence": sequence, "last_batch_id": batchID,
			"last_ack_digest": digest, "updated_time": receivedAt,
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrFlowSequence
	}
	return nil
}
