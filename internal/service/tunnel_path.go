package service

import "github.com/nXiaoK/go-panel/internal/model"

// tunnelRelayNodeID 返回可选中继节点 ID。历史两节点隧道返回 0。
func tunnelRelayNodeID(tunnel *model.Tunnel) int64 {
	if tunnel == nil || tunnel.RelayNodeID == nil || *tunnel.RelayNodeID <= 0 {
		return 0
	}
	return *tunnel.RelayNodeID
}

func tunnelHasRelay(tunnel *model.Tunnel) bool {
	return tunnelRelayNodeID(tunnel) > 0
}

// tunnelPathNodeIDs 按入口、中继、出口顺序返回隧道节点；调用方可再去重。
func tunnelPathNodeIDs(tunnel *model.Tunnel) []int64 {
	if tunnel == nil {
		return nil
	}
	ids := []int64{tunnel.InNodeID}
	if relayNodeID := tunnelRelayNodeID(tunnel); relayNodeID > 0 {
		ids = append(ids, relayNodeID)
	}
	if tunnel.Type == tunnelTypeTunnelForward {
		ids = append(ids, tunnel.OutNodeID)
	}
	return ids
}

func reverseNodeIDs(ids []int64) []int64 {
	reversed := append([]int64(nil), ids...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}
