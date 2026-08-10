package service

import (
	"errors"
	"math"
	"regexp"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

const (
	maxFlowReporterIDLength = 80
	// 采集时间允许节点最多快于面板十分钟，兼容常见时钟漂移，同时避免未来小时桶污染趋势。
	maxNftBatchFutureSkew = 10 * time.Minute
	// 持久批次可能在节点长期离线后才补传，因此不限制历史时长；仅拒绝早于系统可用年代的异常时间戳。
	minNftBatchCapturedAtMillis int64 = 1_577_836_800_000 // 2020-01-01T00:00:00Z
)

var flowReporterIDPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ProcessNftBatch atomically applies a strictly monotonic reporter batch.
func ProcessNftBatch(node AuthenticatedNode, batch dto.NftFlowBatchV2Dto) (dto.NftFlowAckDto, error) {
	if normalizeForwardMode(node.ForwardMode) != forwardModeNftables || node.ID <= 0 {
		return dto.NftFlowAckDto{}, ErrFlowNodeMismatch
	}
	if err := validateNftBatchV2(batch); err != nil {
		return dto.NftFlowAckDto{}, err
	}
	receivedAt := time.Now()
	recordedAt, err := nftBatchRecordedAt(batch, receivedAt)
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	batchDigest, err := dto.NftFlowBatchDigest(batch)
	if err != nil {
		return dto.NftFlowAckDto{}, ErrInvalidFlowReport
	}

	var ack dto.NftFlowAckDto
	applied := false
	// 新节点使用持久化的采集时间，断线重传不会落入面板恢复所在小时；旧节点
	// 没有该字段时仍按接收时间处理。同一批内所有转发始终写入同一个小时桶。
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var state model.FlowReporterState
		err := tx.Where("node_id = ? AND reporter_id = ?", node.ID, batch.ReporterID).First(&state).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			state = model.FlowReporterState{NodeID: node.ID, ReporterID: batch.ReporterID, UpdatedTime: receivedAt.UnixMilli()}
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		if batch.Sequence == state.LastSequence {
			if batch.BatchID != state.LastBatchID || batchDigest != state.LastAckDigest {
				return ErrFlowBatchConflict
			}
			ack = nftBatchAck(state.ReporterID, state.LastSequence, state.LastBatchID, state.LastAckDigest)
			return nil
		}
		if batch.Sequence != state.LastSequence+1 {
			return ErrFlowSequence
		}

		for _, item := range batch.Items {
			if err := applyNftFlowItemAt(tx, node, item, recordedAt); err != nil {
				return err
			}
		}
		ack = nftBatchAck(batch.ReporterID, batch.Sequence, batch.BatchID, batchDigest)
		result := tx.Model(&model.FlowReporterState{}).Where("id = ? AND last_sequence = ?", state.ID, state.LastSequence).Updates(map[string]any{
			"last_sequence":   batch.Sequence,
			"last_batch_id":   batch.BatchID,
			"last_ack_digest": ack.AckDigest,
			"updated_time":    receivedAt.UnixMilli(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFlowSequence
		}
		applied = true
		return nil
	})
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	if applied {
		EnforceNftFlowLimits(batch.Items)
	}
	return ack, nil
}

func validateNftBatchV2(batch dto.NftFlowBatchV2Dto) error {
	if !validFlowReporterToken(batch.ReporterID) || !validFlowReporterToken(batch.BatchID) ||
		batch.Sequence == 0 || batch.Sequence > math.MaxInt64 || len(batch.Items) == 0 || len(batch.Items) > dto.MaxNftFlowBatchItems {
		return ErrInvalidFlowReport
	}
	return nil
}

func nftBatchRecordedAt(batch dto.NftFlowBatchV2Dto, receivedAt time.Time) (time.Time, error) {
	if batch.CapturedAt == 0 {
		// CapturedAt 为零只用于兼容升级前已经落盘、正在等待重传的旧批次。
		return receivedAt, nil
	}
	if batch.CapturedAt < minNftBatchCapturedAtMillis || batch.CapturedAt > receivedAt.Add(maxNftBatchFutureSkew).UnixMilli() {
		return time.Time{}, ErrInvalidFlowReport
	}
	return time.UnixMilli(batch.CapturedAt), nil
}

func validFlowReporterToken(value string) bool {
	return len(value) > 0 && len(value) <= maxFlowReporterIDLength && flowReporterIDPattern.MatchString(value)
}

func nftBatchAck(reporterID string, sequence uint64, batchID, digest string) dto.NftFlowAckDto {
	return dto.NftFlowAckDto{
		ReporterID: reporterID,
		Sequence:   sequence,
		BatchID:    batchID,
		AckDigest:  digest,
	}
}
