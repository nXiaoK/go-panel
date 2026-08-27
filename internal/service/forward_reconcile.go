package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

func restoreForwardDesiredSnapshot(forward model.Forward, members []model.ForwardExitMember) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&forward).Error; err != nil {
			return fmt.Errorf("restore forward snapshot: %w", err)
		}
		if err := tx.Where("forward_id = ?", forward.ID).Delete(&model.ForwardExitMember{}).Error; err != nil {
			return fmt.Errorf("clear current exit members: %w", err)
		}
		if len(members) > 0 {
			rows := append([]model.ForwardExitMember(nil), members...)
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("restore exit members: %w", err)
			}
		}
		return nil
	})
}

func nftRuntimeNodeIDs(forward *model.Forward, tunnel *model.Tunnel) []int64 {
	if tunnel == nil {
		return nil
	}
	ids := []int64{tunnel.InNodeID}
	if relayNodeID := tunnelRelayNodeID(tunnel); relayNodeID > 0 {
		ids = append(ids, relayNodeID)
	}
	members := loadForwardExitMembers(forward, tunnel)
	if tunnel.Type == tunnelTypeTunnelForward && len(members) == 0 {
		ids = append(ids, tunnel.OutNodeID)
	}
	for _, member := range members {
		ids = append(ids, member.OutNodeID)
	}
	return ids
}

func requestedRuntimeNodeIDs(tunnel *model.Tunnel, members []dto.ForwardExitMemberDto) []int64 {
	if tunnel == nil {
		return nil
	}
	ids := []int64{tunnel.InNodeID}
	if tunnel.Type == tunnelTypeTunnelForward {
		if relayNodeID := tunnelRelayNodeID(tunnel); relayNodeID > 0 {
			ids = append(ids, relayNodeID)
		}
		if len(members) == 0 {
			ids = append(ids, tunnel.OutNodeID)
		} else {
			for _, member := range members {
				ids = append(ids, member.OutNodeID)
			}
		}
	}
	return ids
}

// lockForwardSagaSnapshot acquires a stable old/new node union and only then
// returns the database snapshot used by the external saga. If another update
// moved the forward while we were waiting, expand the lock set and retry.
func lockForwardSagaSnapshot(forwardID int64, candidate []int64) (*model.Forward, model.Tunnel, func(), error) {
	nodeIDs := append([]int64(nil), candidate...)
	for {
		var before model.Forward
		if err := model.DB.First(&before, forwardID).Error; err != nil {
			return nil, model.Tunnel{}, nil, err
		}
		var beforeTunnel model.Tunnel
		if err := model.DB.First(&beforeTunnel, before.TunnelID).Error; err != nil {
			return nil, model.Tunnel{}, nil, err
		}
		nodeIDs = append(nodeIDs, nftRuntimeNodeIDs(&before, &beforeTunnel)...)
		nodeIDs = normalizeNodeSagaLockIDs(nodeIDs)
		unlock := lockNftSagaNodes(nodeIDs)

		var current model.Forward
		if err := model.DB.First(&current, forwardID).Error; err != nil {
			unlock()
			return nil, model.Tunnel{}, nil, err
		}
		var currentTunnel model.Tunnel
		if err := model.DB.First(&currentTunnel, current.TunnelID).Error; err != nil {
			unlock()
			return nil, model.Tunnel{}, nil, err
		}
		actual := append(append([]int64(nil), candidate...), nftRuntimeNodeIDs(&current, &currentTunnel)...)
		if nodeIDSetContains(nodeIDs, actual) {
			return &current, currentTunnel, unlock, nil
		}
		unlock()
		nodeIDs = append(nodeIDs, actual...)
	}
}

func rollbackForwardRuntimeSaga(primary error, snapshot model.Forward, members []model.ForwardExitMember, nodeIDs []int64) error {
	restoreErr := restoreForwardDesiredSnapshot(snapshot, members)
	if restoreErr != nil {
		markErr := markForwardErrorDesired(snapshot.ID)
		if markErr != nil {
			return errors.Join(primary, restoreErr, markErr)
		}
		return errors.Join(primary, restoreErr, refreshNftNodesCheckedLocked(nodeIDs))
	}
	refreshErr := refreshNftNodesCheckedLocked(nodeIDs)
	if refreshErr != nil {
		markErr := markForwardErrorDesired(snapshot.ID)
		if markErr != nil {
			return errors.Join(primary, refreshErr, markErr)
		}
		return errors.Join(primary, refreshErr, refreshNftNodesCheckedLocked(nodeIDs))
	}
	return primary
}

func markForwardErrorDesired(forwardID int64) error {
	res := model.DB.Model(&model.Forward{}).Where("id = ?", forwardID).Updates(map[string]interface{}{
		"status": forwardStatusError, "updated_time": time.Now().UnixMilli(),
	})
	if res.Error != nil {
		return fmt.Errorf("mark forward error desired state: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("mark forward error desired state: row %d missing", forwardID)
	}
	return nil
}

func failCreatedForwardSaga(primary error, forward *model.Forward, tunnel *model.Tunnel, inNode, outNode *model.Node, ut *model.UserTunnel, nodeIDs []int64) error {
	if forward == nil {
		return primary
	}
	// A definite deployment failure first makes the row non-active. If this
	// write is not trustworthy, stop immediately: refreshing from an uncertain
	// desired state could publish the very rules we are trying to clean up.
	if err := markForwardErrorDesired(forward.ID); err != nil {
		return errors.Join(primary, err)
	}
	forward.Status = forwardStatusError

	var cleanupErrs []error
	if r := deleteGostServices(forward, tunnel, inNode, outNode, ut); r.Code != 0 && !strings.Contains(strings.ToLower(r.Msg), gost.NotFoundMsg) {
		cleanupErrs = append(cleanupErrs, errors.New(r.Msg))
	}
	if err := refreshNftNodesCheckedLocked(nodeIDs); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if cleanupErr := errors.Join(cleanupErrs...); cleanupErr != nil {
		return errors.Join(primary, cleanupErr)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error { return deleteForwardRows(tx, forward.ID) }); err != nil {
		return errors.Join(primary, fmt.Errorf("delete cleaned failed forward: %w", err))
	}
	return primary
}

func changeGostRuntimeStatus(forward *model.Forward, tunnel *model.Tunnel, inNode, outNode *model.Node, ut *model.UserTunnel, targetStatus int) error {
	serviceName := buildServiceName(forward.ID, forward.UserID, ut)
	entryGost := inNode != nil && !isNftablesMode(inNode)
	exitNodes := map[int64]model.Node{}
	if tunnel.Type == tunnelTypeTunnelForward {
		exitNodes = forwardExitNodeMap(deployForwardExitMembers(forward, tunnel))
	}
	needsRuntimeSync := false
	if targetStatus == forwardStatusPaused {
		if entryGost {
			res := gost.PauseService(inNode.ID, serviceName)
			if !gost.IsOK(res) {
				return errors.New(res.Msg)
			}
		}
		for _, node := range exitNodes {
			if isNftablesMode(&node) {
				continue
			}
			res := gost.PauseRemoteService(node.ID, serviceName)
			if !gost.IsOK(res) {
				return errors.New(res.Msg)
			}
		}
		return nil
	}
	if entryGost {
		res := gost.ResumeService(inNode.ID, serviceName)
		needsRuntimeSync = strings.Contains(res.Msg, gost.NotFoundMsg)
		if !gost.IsOK(res) && !needsRuntimeSync {
			return errors.New(res.Msg)
		}
	}
	for _, node := range exitNodes {
		if isNftablesMode(&node) {
			continue
		}
		res := gost.ResumeRemoteService(node.ID, serviceName)
		if strings.Contains(res.Msg, gost.NotFoundMsg) {
			needsRuntimeSync = true
			continue
		}
		if !gost.IsOK(res) {
			return errors.New(res.Msg)
		}
	}
	if needsRuntimeSync {
		var limiter *int64
		if ut != nil {
			limiter = ut.SpeedID
		}
		if r := updateGostServices(forward, tunnel, limiter, inNode, outNode, ut); r.Code != 0 {
			return errors.New(r.Msg)
		}
	}
	return nil
}

func createGostServices(forward *model.Forward, tunnel *model.Tunnel, limiter *int64, inNode, outNode *model.Node, ut *model.UserTunnel) result.R {
	serviceName := buildServiceName(forward.ID, forward.UserID, ut)
	targetRemoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		return result.Err("转发目标地址格式错误")
	}

	entryGost := inNode != nil && !isNftablesMode(inNode)
	createdChain := false
	createdNodes := make([]int64, 0)
	if tunnel.Type == tunnelTypeTunnelForward {
		exitMembers := deployForwardExitMembers(forward, tunnel)
		exitNodes := forwardExitNodeMap(exitMembers)
		if entryGost {
			remoteAddr := forwardExitChainTargets(exitMembers, exitNodes)
			if remoteAddr == "" {
				return result.Err("出口节点配置无效，无法创建转发链")
			}
			chainRes := gost.AddChainsWithStrategy(inNode.ID, serviceName, remoteAddr, tunnelProtocol(tunnel), tunnelInterfaceName(tunnel), forward.ExitStrategy)
			if !gost.IsOK(chainRes) {
				gost.DeleteChains(inNode.ID, serviceName)
				return result.Err(chainRes.Msg)
			}
			createdChain = true
		}
		// Only Gost exits need a remote service. nftables exits are converged by
		// the checked full-refresh phase of the same saga.
		for _, member := range exitMembers {
			node, ok := exitNodes[member.OutNodeID]
			if !ok {
				if createdChain {
					gost.DeleteChains(inNode.ID, serviceName)
				}
				return result.Err("出口节点不存在")
			}
			if isNftablesMode(&node) {
				continue
			}
			remoteRes := gost.AddRemoteService(node.ID, serviceName, member.OutPort, targetRemoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
			if !gost.IsOK(remoteRes) {
				if createdChain {
					gost.DeleteChains(inNode.ID, serviceName)
				}
				for _, nodeID := range createdNodes {
					gost.DeleteRemoteService(nodeID, serviceName)
				}
				gost.DeleteRemoteService(node.ID, serviceName)
				return result.Err(remoteRes.Msg)
			}
			createdNodes = append(createdNodes, node.ID)
		}
	}
	if !entryGost {
		return result.OkEmpty()
	}

	interfaceName := ""
	if tunnel.Type != tunnelTypeTunnelForward {
		interfaceName = interfaceNameOf(forward)
	}

	serviceRes := gost.AddService(inNode.ID, serviceName, forward.InPort, limiter, targetRemoteAddr, tunnel.Type, tunnel, forward.Strategy, interfaceName)
	if !gost.IsOK(serviceRes) {
		if createdChain {
			gost.DeleteChains(inNode.ID, serviceName)
		}
		for _, nodeID := range createdNodes {
			gost.DeleteRemoteService(nodeID, serviceName)
		}
		return result.Err(serviceRes.Msg)
	}
	return result.OkEmpty()
}

// updateGostServices 更新 gost 服务（msg 含 not found 时回退为创建）
func updateGostServices(forward *model.Forward, tunnel *model.Tunnel, limiter *int64, inNode, outNode *model.Node, ut *model.UserTunnel) result.R {
	serviceName := buildServiceName(forward.ID, forward.UserID, ut)
	targetRemoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		markForwardError(forward)
		return result.Err("转发目标地址格式错误")
	}

	entryGost := inNode != nil && !isNftablesMode(inNode)
	if tunnel.Type == tunnelTypeTunnelForward {
		exitMembers := deployForwardExitMembers(forward, tunnel)
		exitNodes := forwardExitNodeMap(exitMembers)
		if entryGost {
			remoteAddr := forwardExitChainTargets(exitMembers, exitNodes)
			if remoteAddr == "" {
				markForwardError(forward)
				return result.Err("出口节点配置无效，无法更新转发链")
			}
			chainRes := gost.UpdateChainsWithStrategy(inNode.ID, serviceName, remoteAddr, tunnelProtocol(tunnel), tunnelInterfaceName(tunnel), forward.ExitStrategy)
			if strings.Contains(chainRes.Msg, gost.NotFoundMsg) {
				chainRes = gost.AddChainsWithStrategy(inNode.ID, serviceName, remoteAddr, tunnelProtocol(tunnel), tunnelInterfaceName(tunnel), forward.ExitStrategy)
			}
			if !gost.IsOK(chainRes) {
				markForwardError(forward)
				return result.Err(chainRes.Msg)
			}
		}

		for _, member := range exitMembers {
			node, ok := exitNodes[member.OutNodeID]
			if !ok {
				markForwardError(forward)
				return result.Err("出口节点不存在")
			}
			if isNftablesMode(&node) {
				continue
			}
			remoteRes := gost.UpdateRemoteService(node.ID, serviceName, member.OutPort, targetRemoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
			if strings.Contains(remoteRes.Msg, gost.NotFoundMsg) {
				remoteRes = gost.AddRemoteService(node.ID, serviceName, member.OutPort, targetRemoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
			}
			if !gost.IsOK(remoteRes) {
				markForwardError(forward)
				return result.Err(remoteRes.Msg)
			}
		}
	}
	if !entryGost {
		return result.OkEmpty()
	}

	interfaceName := ""
	if tunnel.Type != tunnelTypeTunnelForward {
		interfaceName = interfaceNameOf(forward)
	}
	serviceRes := gost.UpdateService(inNode.ID, serviceName, forward.InPort, limiter, targetRemoteAddr, tunnel.Type, tunnel, forward.Strategy, interfaceName)
	if strings.Contains(serviceRes.Msg, gost.NotFoundMsg) {
		serviceRes = gost.AddService(inNode.ID, serviceName, forward.InPort, limiter, targetRemoteAddr, tunnel.Type, tunnel, forward.Strategy, interfaceName)
	}
	if !gost.IsOK(serviceRes) {
		markForwardError(forward)
		return result.Err(serviceRes.Msg)
	}
	return result.OkEmpty()
}

// deleteOldGostServices 删除旧隧道上的 gost 配置（尽力而为）
func deleteOldGostServices(forward *model.Forward, oldTunnel *model.Tunnel) {
	_ = deleteOldGostServicesChecked(forward, oldTunnel, loadForwardExitMembers(forward, oldTunnel))
}

func deleteOldGostServicesChecked(forward *model.Forward, oldTunnel *model.Tunnel, members []model.ForwardExitMember) error {
	oldUT := getUserTunnel(forward.UserID, oldTunnel.ID)
	oldInNode, oldOutNode, errMsg := getRequiredNodes(oldTunnel)
	if errMsg != "" {
		return errors.New(errMsg)
	}
	r := deleteGostServicesWithMembers(forward, oldTunnel, oldInNode, oldOutNode, oldUT, members)
	if r.Code != 0 {
		return errors.New(r.Msg)
	}
	return nil
}

// deleteGostServices 删除转发的全部 gost 服务
func deleteGostServices(forward *model.Forward, tunnel *model.Tunnel, inNode, outNode *model.Node, ut *model.UserTunnel) result.R {
	return deleteGostServicesWithMembers(forward, tunnel, inNode, outNode, ut, deployForwardExitMembers(forward, tunnel))
}

func deleteGostServicesWithMembers(forward *model.Forward, tunnel *model.Tunnel, inNode, outNode *model.Node, ut *model.UserTunnel, members []model.ForwardExitMember) result.R {
	serviceName := buildServiceName(forward.ID, forward.UserID, ut)
	var errs []error
	entryGost := inNode != nil && !isNftablesMode(inNode)
	if entryGost {
		serviceRes := gost.DeleteService(inNode.ID, serviceName)
		if !gost.IsOK(serviceRes) && !strings.Contains(strings.ToLower(serviceRes.Msg), gost.NotFoundMsg) {
			errs = append(errs, errors.New(serviceRes.Msg))
		}
	}
	if tunnel.Type == tunnelTypeTunnelForward {
		if entryGost {
			chainRes := gost.DeleteChains(inNode.ID, serviceName)
			if !gost.IsOK(chainRes) && !strings.Contains(strings.ToLower(chainRes.Msg), gost.NotFoundMsg) {
				errs = append(errs, errors.New(chainRes.Msg))
			}
		}
		exitNodes := forwardExitNodeMap(members)
		if len(exitNodes) == 0 && outNode != nil {
			exitNodes[outNode.ID] = *outNode
		}
		for _, node := range exitNodes {
			if isNftablesMode(&node) {
				continue
			}
			remoteRes := gost.DeleteRemoteService(node.ID, serviceName)
			if !gost.IsOK(remoteRes) && !strings.Contains(strings.ToLower(remoteRes.Msg), gost.NotFoundMsg) {
				errs = append(errs, errors.New(remoteRes.Msg))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return result.Err(err.Error())
	}
	return result.OkEmpty()
}

// markForwardError 更新转发状态为错误
func markForwardError(forward *model.Forward) {
	model.DB.Model(&model.Forward{}).Where("id = ?", forward.ID).Update("status", forwardStatusError)
}

// UpdateForwardA 流量统计后台调用：重新下发转发配置（对应 Java updateForwardA）
func UpdateForwardA(forward *model.Forward) error {
	if forward == nil {
		return errors.New("转发不存在")
	}
	current, tunnel, unlock, err := lockForwardSagaSnapshot(forward.ID, nil)
	if err != nil {
		return err
	}
	defer unlock()
	members, err := loadPersistedForwardExitMembersDB(model.DB, current.ID)
	if err != nil {
		return err
	}
	nodeIDs := tunnelPathNodeIDs(&tunnel)
	for _, member := range members {
		nodeIDs = append(nodeIDs, member.OutNodeID)
	}
	nodeIDs = normalizeNodeSagaLockIDs(nodeIDs)
	var nodes []model.Node
	if len(nodeIDs) > 0 {
		if err := model.DB.Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
			return err
		}
	}
	snapshot := userTunnelRuntimeSnapshot{
		tunnel: tunnel, forwards: []model.Forward{*current}, members: map[int64][]model.ForwardExitMember{current.ID: members},
		nodes: make(map[int64]model.Node, len(nodes)), nodeIDs: normalizeNodeSagaLockIDs(nodeIDs),
	}
	for _, node := range nodes {
		snapshot.nodes[node.ID] = node
	}
	var ut model.UserTunnel
	err = model.DB.Where("user_id = ? AND tunnel_id = ?", current.UserID, tunnel.ID).First(&ut).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var limiter *int64
	if err == nil {
		snapshot.permission = ut
		limiter = ut.SpeedID
	}
	allowed, gateErr := forwardPermissionAllowsRuntimeDB(model.DB, current)
	if gateErr != nil {
		return gateErr
	}
	if !allowed {
		var runtimeErr error
		if snapshot.permission.ID != 0 {
			if snapshot.permission.Status == userTunnelStatusDeleting {
				runtimeErr = cleanupUserTunnelForwardRuntime(&snapshot, current)
			} else {
				runtimeErr = pauseUserTunnelForwardRuntime(&snapshot, current)
			}
		}
		return errors.Join(runtimeErr, refreshNftNodesDesiredCheckedLocked(snapshot.nodeIDs))
	}
	if current.Status == forwardStatusActive {
		if err := updateUserTunnelForwardRuntime(&snapshot, current, limiter); err != nil {
			return err
		}
	}
	return refreshNftNodesDesiredCheckedLocked(snapshot.nodeIDs)
}
