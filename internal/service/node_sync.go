package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

var (
	updateGostServiceCommand       = gost.UpdateService
	updateGostRemoteServiceCommand = gost.UpdateRemoteService
)

func init() {
	ws.SetNodeConnectedHook(SyncNodeForwardsOnConnect)
}

// SyncNodeForwardsOnConnect replays the active forwarding config that belongs to
// a node after it reconnects. This is intentionally scoped to the connected node
// so reinstalling one machine does not disturb healthy nodes. The replay runs
// through the generation reconciler so sync state records applied/failed.
func SyncNodeForwardsOnConnect(nodeID int64) {
	// Skip during a database restore maintenance window; the 30s reconciler and
	// the next reconnect converge the node once maintenance ends.
	leave, ok := model.Gate.Enter()
	if !ok {
		log.Printf("节点 %d 上线对账跳过：数据库维护中", nodeID)
		return
	}
	defer leave()
	res := ReconcileNode(context.Background(), nodeID)
	if res.State == SyncFailed {
		log.Printf("节点 %d 上线对账失败，等待重试: %s", nodeID, res.LastError)
	}
}

// applyNodeRuntimeChecked replays the node's complete desired configuration and
// reports whether the node converged. Callers must hold the node saga lock.
func applyNodeRuntimeChecked(nodeID int64) error {
	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		return fmt.Errorf("读取节点: %w", err)
	}
	var errs []error
	if !isNftablesMode(&node) {
		if res := reconcileGostManagedRuntimeOnConnect(nodeID); !gost.IsOK(res) {
			log.Printf("节点 %d 对账 managed Gost runtime 失败，等待下次重连: %s", nodeID, res.Msg)
			errs = append(errs, errString("对账 managed Gost runtime 失败: "+res.Msg))
		}
	}
	syncUserTunnelDeletionTombstones(nodeID, &node)
	syncGostNodePermissionBlockedForwards(nodeID, &node)
	syncGostNodeNonActiveForwards(nodeID, &node)
	syncGostNodeLimiters(nodeID)

	if isNftablesMode(&node) {
		if err := doRefreshNodeForwardRulesChecked(nodeID); err != nil {
			log.Printf("节点 %d 上线同步 nft 规则失败: %v", nodeID, err)
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}

	stats := syncGostNodeForwards(nodeID)
	if stats.entryForwards+stats.exitForwards > 0 {
		log.Printf("节点 %d 上线同步完成: entry=%d exit=%d errors=%d", nodeID, stats.entryForwards, stats.exitForwards, stats.errors)
	}
	if stats.errors > 0 {
		errs = append(errs, fmt.Errorf("%d 条转发同步失败", stats.errors))
	}
	return errors.Join(errs...)
}

func syncGostNodePermissionBlockedForwards(nodeID int64, node *model.Node) {
	if isNftablesMode(node) {
		return
	}
	var forwards []model.Forward
	if err := model.DB.Where("status = ?", forwardStatusActive).Find(&forwards).Error; err != nil {
		return
	}
	for i := range forwards {
		forward := &forwards[i]
		allowed, gateErr := forwardPermissionAllowsRuntimeDB(model.DB, forward)
		if gateErr != nil || allowed {
			continue
		}
		var ut model.UserTunnel
		if err := model.DB.Where("user_id = ? AND tunnel_id = ?", forward.UserID, forward.TunnelID).First(&ut).Error; err != nil || ut.Status == userTunnelStatusDeleting {
			continue
		}
		var tunnel model.Tunnel
		if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
			continue
		}
		serviceName := buildServiceName(forward.ID, forward.UserID, &ut)
		if tunnel.InNodeID == nodeID {
			logGostTombstoneCleanup(nodeID, forward.ID, pauseUserTunnelGostService(nodeID, serviceName))
		}
		if tunnel.Type != tunnelTypeTunnelForward {
			continue
		}
		members, err := deployForwardExitMembersDB(model.DB, forward, &tunnel)
		if err != nil {
			continue
		}
		for _, member := range members {
			if member.OutNodeID == nodeID {
				logGostTombstoneCleanup(nodeID, forward.ID, pauseUserTunnelGostRemoteService(nodeID, serviceName))
				break
			}
		}
	}
}

func syncGostNodeNonActiveForwards(nodeID int64, node *model.Node) {
	if isNftablesMode(node) {
		return
	}
	var forwards []model.Forward
	if err := model.DB.Where("status <> ?", forwardStatusActive).Find(&forwards).Error; err != nil {
		return
	}
	for i := range forwards {
		forward := &forwards[i]
		var tunnel model.Tunnel
		if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
			continue
		}
		ut := getUserTunnel(forward.UserID, tunnel.ID)
		if ut != nil && ut.Status == userTunnelStatusDeleting {
			continue
		}
		serviceName := buildServiceName(forward.ID, forward.UserID, ut)
		deleting := forward.Status == forwardStatusError
		if tunnel.InNodeID == nodeID {
			if deleting {
				logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostService(nodeID, serviceName))
				if tunnel.Type == tunnelTypeTunnelForward {
					logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostChains(nodeID, serviceName))
				}
			} else {
				logGostTombstoneCleanup(nodeID, forward.ID, pauseUserTunnelGostService(nodeID, serviceName))
			}
		}
		if tunnel.Type != tunnelTypeTunnelForward {
			continue
		}
		members, err := deployForwardExitMembersDB(model.DB, forward, &tunnel)
		if err != nil {
			continue
		}
		for _, member := range members {
			if member.OutNodeID != nodeID {
				continue
			}
			if deleting {
				logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostRemoteService(nodeID, serviceName))
			} else {
				logGostTombstoneCleanup(nodeID, forward.ID, pauseUserTunnelGostRemoteService(nodeID, serviceName))
			}
			break
		}
	}
}

func syncUserTunnelDeletionTombstones(nodeID int64, node *model.Node) {
	var tombstones []model.UserTunnel
	if err := model.DB.Where("status = ?", userTunnelStatusDeleting).Find(&tombstones).Error; err != nil {
		return
	}
	for i := range tombstones {
		ut := &tombstones[i]
		var tunnel model.Tunnel
		if err := model.DB.First(&tunnel, ut.TunnelID).Error; err != nil {
			continue
		}
		var forwards []model.Forward
		if err := model.DB.Where("user_id = ? AND tunnel_id = ?", ut.UserID, ut.TunnelID).Find(&forwards).Error; err != nil {
			continue
		}
		for j := range forwards {
			forward := &forwards[j]
			serviceName := buildServiceName(forward.ID, forward.UserID, ut)
			if tunnel.InNodeID == nodeID && !isNftablesMode(node) {
				logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostService(nodeID, serviceName))
				if tunnel.Type == tunnelTypeTunnelForward {
					logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostChains(nodeID, serviceName))
				}
			}
			if tunnel.Type != tunnelTypeTunnelForward || isNftablesMode(node) {
				continue
			}
			members, err := deployForwardExitMembersDB(model.DB, forward, &tunnel)
			if err != nil {
				continue
			}
			for _, member := range members {
				if member.OutNodeID == nodeID {
					logGostTombstoneCleanup(nodeID, forward.ID, deleteUserTunnelGostRemoteService(nodeID, serviceName))
					break
				}
			}
		}
	}
}

func logGostTombstoneCleanup(nodeID, forwardID int64, res ws.GostResult) {
	if gost.IsOK(res) || strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
		return
	}
	log.Printf("节点 %d 清理权限删除 tombstone 失败(forward=%d): %s", nodeID, forwardID, res.Msg)
}

// syncGostNodeLimiters converges limiter desired state on reconnect. Desired
// IDs are updated/created; IDs that still exist globally but were migrated to
// another entry node are removed from this node.
func syncGostNodeLimiters(nodeID int64) {
	var limits []model.SpeedLimit
	if err := model.DB.Find(&limits).Error; err != nil {
		return
	}
	for i := range limits {
		limit := limits[i]
		var tunnel model.Tunnel
		desired := model.DB.First(&tunnel, limit.TunnelID).Error == nil && tunnel.InNodeID == nodeID
		if !desired {
			res := deleteSpeedLimitRemote(nodeID, limit.ID)
			if !gost.IsOK(res) && !strings.Contains(res.Msg, gost.NotFoundMsg) {
				log.Printf("节点 %d 清理限速器失败(speedLimit=%d): %s", nodeID, limit.ID, res.Msg)
			}
			continue
		}
		res := updateSpeedLimitRemote(nodeID, limit.ID, convertBitsToMBps(limit.Speed))
		if strings.Contains(res.Msg, gost.NotFoundMsg) {
			res = addSpeedLimitRemote(nodeID, limit.ID, convertBitsToMBps(limit.Speed))
		}
		if !gost.IsOK(res) {
			log.Printf("节点 %d 同步限速器失败(speedLimit=%d): %s", nodeID, limit.ID, res.Msg)
		}
	}
}

type gostNodeSyncStats struct {
	entryForwards int
	exitForwards  int
	errors        int
}

func syncGostNodeForwards(nodeID int64) gostNodeSyncStats {
	stats := gostNodeSyncStats{}
	seenEntry := map[int64]bool{}
	seenExit := map[int64]bool{}

	for _, item := range gostEntrySyncItems(nodeID) {
		if seenEntry[item.forward.ID] {
			continue
		}
		seenEntry[item.forward.ID] = true
		if err := syncGostEntryForward(&item.forward, &item.tunnel, &item.node); err != nil {
			stats.errors++
			log.Printf("节点 %d 同步入口转发失败(forward=%d): %v", nodeID, item.forward.ID, err)
			continue
		}
		stats.entryForwards++
	}

	for _, item := range gostExitSyncItems(nodeID) {
		if seenExit[item.forward.ID] {
			continue
		}
		seenExit[item.forward.ID] = true
		if err := syncGostExitForward(&item.forward, &item.tunnel, &item.node, item.member); err != nil {
			stats.errors++
			log.Printf("节点 %d 同步出口转发失败(forward=%d): %v", nodeID, item.forward.ID, err)
			continue
		}
		stats.exitForwards++
	}

	return stats
}

type gostSyncItem struct {
	forward model.Forward
	tunnel  model.Tunnel
	node    model.Node
	member  model.ForwardExitMember
}

func gostEntrySyncItems(nodeID int64) []gostSyncItem {
	var tunnels []model.Tunnel
	model.DB.Where("in_node_id = ? AND status = ?", nodeID, tunnelStatusActive).Find(&tunnels)
	if len(tunnels) == 0 {
		return nil
	}

	out := make([]gostSyncItem, 0)
	for i := range tunnels {
		tunnel := tunnels[i]
		var inNode model.Node
		if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil || isNftablesMode(&inNode) {
			continue
		}
		var forwards []model.Forward
		model.DB.Where("tunnel_id = ? AND status = ?", tunnel.ID, forwardStatusActive).Find(&forwards)
		for j := range forwards {
			allowed, err := forwardPermissionAllowsRuntimeDB(model.DB, &forwards[j])
			if err != nil || !allowed {
				continue
			}
			out = append(out, gostSyncItem{
				forward: forwards[j],
				tunnel:  tunnel,
				node:    inNode,
			})
		}
	}
	return out
}

func gostExitSyncItems(nodeID int64) []gostSyncItem {
	var tunnels []model.Tunnel
	model.DB.Where("type = ? AND status = ?", tunnelTypeTunnelForward, tunnelStatusActive).Find(&tunnels)
	if len(tunnels) == 0 {
		return nil
	}

	var exitNode model.Node
	if err := model.DB.First(&exitNode, nodeID).Error; err != nil || isNftablesMode(&exitNode) {
		return nil
	}

	out := make([]gostSyncItem, 0)
	for i := range tunnels {
		tunnel := tunnels[i]
		var forwards []model.Forward
		model.DB.Where("tunnel_id = ? AND status = ?", tunnel.ID, forwardStatusActive).Find(&forwards)
		for j := range forwards {
			allowed, err := forwardPermissionAllowsRuntimeDB(model.DB, &forwards[j])
			if err != nil || !allowed {
				continue
			}
			members := deployForwardExitMembers(&forwards[j], &tunnel)
			for _, member := range members {
				if member.OutNodeID != nodeID {
					continue
				}
				out = append(out, gostSyncItem{
					forward: forwards[j],
					tunnel:  tunnel,
					node:    exitNode,
					member:  member,
				})
				break
			}
		}
	}
	return out
}

func syncGostEntryForward(forward *model.Forward, tunnel *model.Tunnel, inNode *model.Node) error {
	serviceName := buildServiceName(forward.ID, forward.UserID, getUserTunnel(forward.UserID, tunnel.ID))
	targetRemoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		return errString("转发目标地址格式错误")
	}

	if tunnel.Type == tunnelTypeTunnelForward {
		exitMembers := deployForwardExitMembers(forward, tunnel)
		remoteAddr := forwardExitChainTargets(exitMembers, forwardExitNodeMap(exitMembers))
		if remoteAddr == "" {
			return errString("出口节点配置无效，无法同步转发链")
		}
		chainRes := gost.UpdateChainsWithStrategy(inNode.ID, serviceName, remoteAddr, tunnelProtocol(tunnel), tunnelInterfaceName(tunnel), forward.ExitStrategy)
		if strings.Contains(chainRes.Msg, gost.NotFoundMsg) {
			chainRes = gost.AddChainsWithStrategy(inNode.ID, serviceName, remoteAddr, tunnelProtocol(tunnel), tunnelInterfaceName(tunnel), forward.ExitStrategy)
		}
		if !gost.IsOK(chainRes) {
			return errString(chainRes.Msg)
		}
	}

	var limiter *int64
	if ut := getUserTunnel(forward.UserID, tunnel.ID); ut != nil {
		limiter = ut.SpeedID
	}
	interfaceName := ""
	if tunnel.Type != tunnelTypeTunnelForward {
		interfaceName = interfaceNameOf(forward)
	}
	serviceRes := updateGostServiceCommand(inNode.ID, serviceName, forward.InPort, limiter, targetRemoteAddr, tunnel.Type, tunnel, forward.Strategy, interfaceName)
	if strings.Contains(serviceRes.Msg, gost.NotFoundMsg) {
		serviceRes = gost.AddService(inNode.ID, serviceName, forward.InPort, limiter, targetRemoteAddr, tunnel.Type, tunnel, forward.Strategy, interfaceName)
	}
	if !gost.IsOK(serviceRes) {
		return errString(serviceRes.Msg)
	}
	return nil
}

func syncGostExitForward(forward *model.Forward, tunnel *model.Tunnel, node *model.Node, member model.ForwardExitMember) error {
	if tunnel.Type != tunnelTypeTunnelForward || member.OutPort <= 0 {
		return nil
	}
	serviceName := buildServiceName(forward.ID, forward.UserID, getUserTunnel(forward.UserID, tunnel.ID))
	targetRemoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		return errString("转发目标地址格式错误")
	}
	remoteRes := updateGostRemoteServiceCommand(node.ID, serviceName, member.OutPort, targetRemoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
	if strings.Contains(remoteRes.Msg, gost.NotFoundMsg) {
		remoteRes = gost.AddRemoteService(node.ID, serviceName, member.OutPort, targetRemoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
	}
	if !gost.IsOK(remoteRes) {
		return errString(remoteRes.Msg)
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
