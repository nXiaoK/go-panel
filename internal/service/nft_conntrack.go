package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

var sendNodeMessage = ws.SendMsg

// FlushForwardConntrack clears NAT conntrack entries for this forward's listen port.
// It is intentionally explicit and only called for user-requested force switching.
func FlushForwardConntrack(forward *model.Forward, tunnel *model.Tunnel, node *model.Node) error {
	if forward == nil || tunnel == nil || node == nil || !isNftablesMode(node) {
		return nil
	}
	port, ok := forwardListenPortForNode(forward, tunnel, node.ID)
	if !ok || port <= 0 {
		return nil
	}
	result := sendNodeMessage(node.ID, map[string]interface{}{
		"port":      port,
		"protocols": resolveProtocols(tunnel),
	}, "FlushConntrack")
	if gost.IsOK(result) {
		return nil
	}
	msg := strings.TrimSpace(result.Msg)
	if strings.Contains(strings.ToLower(msg), "unknown command type") {
		return fmt.Errorf("节点 agent 版本过旧，不支持强制切换，请重新安装或升级 nftables 节点")
	}
	if msg == "" {
		msg = "节点未返回错误详情"
	}
	return errors.New(msg)
}

func FlushForwardConntrackOnNodes(forward *model.Forward, tunnel *model.Tunnel, nodes ...model.Node) error {
	for _, node := range nodes {
		if err := FlushForwardConntrack(forward, tunnel, &node); err != nil {
			return err
		}
	}
	return nil
}

func FlushForwardConntrackForUpdate(forward *model.Forward, tunnel *model.Tunnel, inNode *model.Node) error {
	if forward == nil || tunnel == nil || inNode == nil {
		return nil
	}
	nodesByID := map[int64]model.Node{inNode.ID: *inNode}
	if tunnel.Type == tunnelTypeTunnelForward {
		for _, node := range forwardExitNodeMap(deployForwardExitMembers(forward, tunnel)) {
			nodesByID[node.ID] = node
		}
	}
	nodes := make([]model.Node, 0, len(nodesByID))
	for _, node := range nodesByID {
		nodes = append(nodes, node)
	}
	return FlushForwardConntrackOnNodes(forward, tunnel, nodes...)
}

func forwardListenPortForNode(forward *model.Forward, tunnel *model.Tunnel, nodeID int64) (int, bool) {
	if tunnel.Type != tunnelTypeTunnelForward {
		return forward.InPort, tunnel.InNodeID == 0 || tunnel.InNodeID == nodeID
	}
	if tunnel.InNodeID == nodeID {
		return forward.InPort, true
	}
	for _, member := range deployForwardExitMembers(forward, tunnel) {
		if member.OutNodeID == nodeID {
			return member.OutPort, true
		}
	}
	return 0, false
}
