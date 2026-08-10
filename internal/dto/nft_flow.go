package dto

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
)

type canonicalNftFlowItem struct {
	forwardID    int64
	userID       int64
	userTunnelID int64
	up           int64
	down         int64
}

// NftFlowBatchDigest hashes a canonical representation of the exact batch
// identity and item multiset. Item order and map serialization never affect it.
func NftFlowBatchDigest(batch NftFlowBatchV2Dto) (string, error) {
	items := make([]canonicalNftFlowItem, 0, len(batch.Items))
	for _, item := range batch.Items {
		if item.ForwardID == nil || item.UserID == nil || item.Up == nil || item.Down == nil {
			return "", errors.New("incomplete nft flow item")
		}
		userTunnelID := int64(0)
		if item.UserTunnelID != nil {
			userTunnelID = *item.UserTunnelID
		}
		items = append(items, canonicalNftFlowItem{
			forwardID: *item.ForwardID, userID: *item.UserID, userTunnelID: userTunnelID,
			up: *item.Up, down: *item.Down,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.forwardID != b.forwardID {
			return a.forwardID < b.forwardID
		}
		if a.userID != b.userID {
			return a.userID < b.userID
		}
		if a.userTunnelID != b.userTunnelID {
			return a.userTunnelID < b.userTunnelID
		}
		if a.up != b.up {
			return a.up < b.up
		}
		return a.down < b.down
	})

	var canonical bytes.Buffer
	canonical.WriteString("nft-flow-batch-v2\x00")
	writeCanonicalString(&canonical, batch.ReporterID)
	_ = binary.Write(&canonical, binary.BigEndian, batch.Sequence)
	writeCanonicalString(&canonical, batch.BatchID)
	// 零值表示升级前的旧批次，此时必须保持旧摘要字节完全不变。新批次追加
	// 带类型标记的采集时间，防止同一批次身份替换时间后仍获得相同 ACK。
	if batch.CapturedAt != 0 {
		canonical.WriteString("captured-at-ms\x00")
		_ = binary.Write(&canonical, binary.BigEndian, batch.CapturedAt)
	}
	_ = binary.Write(&canonical, binary.BigEndian, uint32(len(items)))
	for _, item := range items {
		_ = binary.Write(&canonical, binary.BigEndian, item.forwardID)
		_ = binary.Write(&canonical, binary.BigEndian, item.userID)
		_ = binary.Write(&canonical, binary.BigEndian, item.userTunnelID)
		_ = binary.Write(&canonical, binary.BigEndian, item.up)
		_ = binary.Write(&canonical, binary.BigEndian, item.down)
	}
	digest := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(digest[:]), nil
}

func writeCanonicalString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value)))
	buffer.WriteString(value)
}
