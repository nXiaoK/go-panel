package service

import (
	"errors"
	"log"
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

	// 这里只提交收据和计费；HTTP 层必须完整送达 ACK 后再执行限额清理，
	// 否则节点持有采集锁等待 ACK，面板又等待同一节点刷新规则，会互相阻塞。
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		return applyFlowReportOnce(tx, node.ID, batch.ReporterID, batch.Sequence, batch.BatchID, batchDigest, receivedAt.UnixMilli(), func() error {
			for _, item := range batch.Items {
				if err := applyNftFlowItemAt(tx, node, item, recordedAt); err != nil {
					if errors.Is(err, errFlowForwardRetired) {
						// 删除后冻结的旧规则仍可能产生尾量。已不存在的转发无法再可靠归账，
						// 只退役该项；批次中其他转发必须继续入账并推进持久化序号。
						log.Printf("忽略已删除转发的 NFT 尾量(node=%d forward=%d)", node.ID, *item.ForwardID)
						continue
					}
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	ack := nftBatchAck(batch.ReporterID, batch.Sequence, batch.BatchID, batchDigest)
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
