package service

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

const userTunnelStatusDeleting = -1

var errUserTunnelRuntimeOutcomeUnknown = errors.New("用户隧道运行态结果不确定")

var (
	deleteUserTunnelGostService       = gost.DeleteServiceLifecycle
	deleteUserTunnelGostChains        = gost.DeleteChainsLifecycle
	deleteUserTunnelGostRemoteService = gost.DeleteRemoteServiceLifecycle
	pauseUserTunnelGostService        = gost.PauseServiceLifecycle
	pauseUserTunnelGostRemoteService  = gost.PauseRemoteServiceLifecycle
	updateUserTunnelGostService       = gost.UpdateServiceLifecycle
	addUserTunnelGostService          = gost.AddServiceLifecycle
	updateUserTunnelGostChains        = gost.UpdateChainsWithStrategyLifecycle
	addUserTunnelGostChains           = gost.AddChainsWithStrategyLifecycle
	updateUserTunnelGostRemoteService = gost.UpdateRemoteServiceLifecycle
	addUserTunnelGostRemoteService    = gost.AddRemoteServiceLifecycle
)

type userTunnelRuntimeSnapshot struct {
	permission model.UserTunnel
	tunnel     model.Tunnel
	forwards   []model.Forward
	members    map[int64][]model.ForwardExitMember
	nodes      map[int64]model.Node
	nodeIDs    []int64
}

// forwardRuntimeSnapshot builds the small runtime view needed to converge one
// forward. Callers already hold the corresponding node saga locks.
func forwardRuntimeSnapshot(db *gorm.DB, forward *model.Forward, tunnel *model.Tunnel, permission *model.UserTunnel, members []model.ForwardExitMember) (userTunnelRuntimeSnapshot, error) {
	if forward == nil || tunnel == nil {
		return userTunnelRuntimeSnapshot{}, errors.New("转发运行态快照不完整")
	}
	snapshot := userTunnelRuntimeSnapshot{
		tunnel:   *tunnel,
		forwards: []model.Forward{*forward},
		members:  map[int64][]model.ForwardExitMember{forward.ID: append([]model.ForwardExitMember(nil), members...)},
	}
	if permission != nil {
		snapshot.permission = *permission
	}
	nodeIDs := []int64{tunnel.InNodeID, tunnel.OutNodeID}
	for _, member := range members {
		nodeIDs = append(nodeIDs, member.OutNodeID)
	}
	snapshot.nodeIDs = normalizeNodeSagaLockIDs(nodeIDs)
	snapshot.nodes = make(map[int64]model.Node, len(snapshot.nodeIDs))
	var nodes []model.Node
	if err := db.Where("id IN ?", snapshot.nodeIDs).Find(&nodes).Error; err != nil {
		return userTunnelRuntimeSnapshot{}, err
	}
	for _, node := range nodes {
		snapshot.nodes[node.ID] = node
	}
	if len(snapshot.nodes) != len(snapshot.nodeIDs) {
		return userTunnelRuntimeSnapshot{}, errors.New("转发运行态节点不完整")
	}
	return snapshot, nil
}

func loadUserTunnelRuntimeSnapshot(db *gorm.DB, id int64) (userTunnelRuntimeSnapshot, error) {
	var snapshot userTunnelRuntimeSnapshot
	if err := db.First(&snapshot.permission, id).Error; err != nil {
		return snapshot, err
	}
	if err := db.First(&snapshot.tunnel, snapshot.permission.TunnelID).Error; err != nil {
		return snapshot, err
	}
	if err := db.Where("user_id = ? AND tunnel_id = ?", snapshot.permission.UserID, snapshot.permission.TunnelID).
		Order("id ASC").Find(&snapshot.forwards).Error; err != nil {
		return snapshot, err
	}
	forwardIDs := make([]int64, 0, len(snapshot.forwards))
	for _, forward := range snapshot.forwards {
		forwardIDs = append(forwardIDs, forward.ID)
	}
	var allMembers []model.ForwardExitMember
	if len(forwardIDs) > 0 {
		if err := db.Where("forward_id IN ?", forwardIDs).Order("id ASC").Find(&allMembers).Error; err != nil {
			return snapshot, err
		}
	}
	snapshot.members = make(map[int64][]model.ForwardExitMember, len(snapshot.forwards))
	nodeIDs := []int64{snapshot.tunnel.InNodeID, snapshot.tunnel.OutNodeID}
	for _, member := range allMembers {
		snapshot.members[member.ForwardID] = append(snapshot.members[member.ForwardID], member)
		nodeIDs = append(nodeIDs, member.OutNodeID)
	}
	snapshot.nodeIDs = normalizeNodeSagaLockIDs(nodeIDs)
	snapshot.nodes = make(map[int64]model.Node, len(snapshot.nodeIDs))
	if len(snapshot.nodeIDs) > 0 {
		var nodes []model.Node
		if err := db.Where("id IN ?", snapshot.nodeIDs).Find(&nodes).Error; err != nil {
			return snapshot, err
		}
		for _, node := range nodes {
			snapshot.nodes[node.ID] = node
		}
		if len(snapshot.nodes) != len(snapshot.nodeIDs) {
			return snapshot, errors.New("用户隧道运行态节点不完整")
		}
	}
	return snapshot, nil
}

func lockUserTunnelRuntimeSnapshot(id int64) (userTunnelRuntimeSnapshot, func(), error) {
	before, err := loadUserTunnelRuntimeSnapshot(model.DB, id)
	if err != nil {
		return userTunnelRuntimeSnapshot{}, nil, err
	}
	locked := append([]int64(nil), before.nodeIDs...)
	for {
		unlock := lockNftSagaNodes(locked)
		current, err := loadUserTunnelRuntimeSnapshot(model.DB, id)
		if err != nil {
			unlock()
			return userTunnelRuntimeSnapshot{}, nil, err
		}
		if nodeIDSetContains(locked, current.nodeIDs) {
			return current, unlock, nil
		}
		unlock()
		locked = normalizeNodeSagaLockIDs(append(locked, current.nodeIDs...))
	}
}

// AssignUserTunnel 分配用户隧道权限
func AssignUserTunnel(req dto.UserTunnelDto) result.R {
	_, unlock, err := lockTunnelSagaSnapshot(req.TunnelID)
	if err != nil {
		return result.Err("隧道不存在")
	}
	defer unlock()

	ut := model.UserTunnel{
		UserID:        req.UserID,
		TunnelID:      req.TunnelID,
		SpeedID:       req.SpeedID,
		Num:           req.Num,
		Flow:          req.Flow,
		FlowResetTime: req.FlowResetTime,
		ExpTime:       &req.ExpTime,
		Status:        1,
	}
	failMsg := ""
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if msg := validateUserTunnelAssignmentDB(tx, req.UserID, req.TunnelID, req.SpeedID); msg != "" {
			failMsg = msg
			return errors.New(msg)
		}
		var count int64
		if err := tx.Model(&model.UserTunnel{}).
			Where("user_id = ? AND tunnel_id = ?", req.UserID, req.TunnelID).
			Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			failMsg = "该用户已拥有此隧道权限"
			return errors.New(failMsg)
		}
		return tx.Create(&ut).Error
	})
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("用户隧道权限分配失败")
	}
	return result.OkMsg("用户隧道权限分配成功")
}

// GetUserTunnelList 用户隧道权限列表（连表）
func GetUserTunnelList(userID int64) result.R {
	var list []dto.UserTunnelWithDetail
	model.DB.Raw(`
		SELECT
			ut.id, ut.user_id AS user_id, ut.tunnel_id AS tunnel_id,
			ut.flow, ut.in_flow AS in_flow, ut.out_flow AS out_flow,
			ut.num, ut.flow_reset_time AS flow_reset_time, ut.exp_time AS exp_time,
			ut.speed_id AS speed_id, ut.status AS status,
			t.name AS tunnel_name, t.flow AS tunnel_flow,
			t.in_ip AS in_ip, t.out_ip AS out_ip, t.type, t.protocol,
			sl.name AS speed_limit_name, sl.speed
		FROM user_tunnel ut
		LEFT JOIN tunnel t ON ut.tunnel_id = t.id
		LEFT JOIN speed_limit sl ON ut.speed_id = sl.id
		WHERE ut.user_id = ?
		ORDER BY ut.id`, userID).Scan(&list)
	if list == nil {
		list = []dto.UserTunnelWithDetail{}
	}
	return result.Ok(list)
}

// RemoveUserTunnel 删除用户隧道权限（连带删除该用户在该隧道下的所有转发）
func RemoveUserTunnel(id int64) result.R {
	snapshot, unlock, err := lockUserTunnelRuntimeSnapshot(id)
	if err != nil {
		return result.Err("未找到对应的用户隧道权限记录")
	}
	defer unlock()

	// Persist a non-serving tombstone before any external mutation. A lost
	// response can then be converged safely when either node reconnects.
	if snapshot.permission.Status != userTunnelStatusDeleting {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.UserTunnel{}).Where("id = ?", id).Update("status", userTunnelStatusDeleting).Error; err != nil {
				return err
			}
			return nil
		}); err != nil {
			return result.Err("用户隧道权限删除失败")
		}
		snapshot.permission.Status = userTunnelStatusDeleting
	}

	var cleanupErrs []error
	for i := range snapshot.forwards {
		if err := cleanupUserTunnelForwardRuntime(&snapshot, &snapshot.forwards[i]); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("forward %d: %w", snapshot.forwards[i].ID, err))
		}
	}
	if err := refreshNftNodesDeletingCheckedLocked(snapshot.nodeIDs); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		markNodesDirtyBestEffort(snapshot.nodeIDs...)
		return result.Err("用户隧道权限运行态清理待重试：" + err.Error())
	}

	forwardIDs := make([]int64, 0, len(snapshot.forwards))
	for _, forward := range snapshot.forwards {
		forwardIDs = append(forwardIDs, forward.ID)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := deleteForwardRowsByIDs(tx, forwardIDs); err != nil {
			return err
		}
		return tx.Delete(&model.UserTunnel{}, id).Error
	}); err != nil {
		return result.Err("未找到对应的用户隧道权限记录")
	}
	return result.OkMsg("用户隧道权限删除成功")
}

func cleanupUserTunnelForwardRuntime(snapshot *userTunnelRuntimeSnapshot, forward *model.Forward) error {
	serviceName := userTunnelSnapshotServiceName(snapshot, forward)
	var errs []error
	if inNode, ok := snapshot.nodes[snapshot.tunnel.InNodeID]; ok && !isNftablesMode(&inNode) {
		errs = appendGostMutationError(errs, deleteUserTunnelGostService(inNode.ID, serviceName))
		if snapshot.tunnel.Type == tunnelTypeTunnelForward {
			errs = appendGostMutationError(errs, deleteUserTunnelGostChains(inNode.ID, serviceName))
		}
	}
	if snapshot.tunnel.Type == tunnelTypeTunnelForward {
		for _, member := range snapshotForwardMembers(forward, &snapshot.tunnel, snapshot.members[forward.ID]) {
			node, ok := snapshot.nodes[member.OutNodeID]
			if !ok {
				errs = append(errs, fmt.Errorf("出口节点 %d 不存在", member.OutNodeID))
				continue
			}
			if !isNftablesMode(&node) {
				errs = appendGostMutationError(errs, deleteUserTunnelGostRemoteService(node.ID, serviceName))
			}
		}
	}
	return errors.Join(errs...)
}

func userTunnelSnapshotServiceName(snapshot *userTunnelRuntimeSnapshot, forward *model.Forward) string {
	if snapshot.permission.ID == 0 {
		return buildServiceName(forward.ID, forward.UserID, nil)
	}
	return buildServiceName(forward.ID, forward.UserID, &snapshot.permission)
}

func appendGostMutationError(errs []error, res ws.GostResult) []error {
	if gost.IsOK(res) || strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
		return errs
	}
	if res.OutcomeUnknown {
		return append(errs, fmt.Errorf("%w: %s", errUserTunnelRuntimeOutcomeUnknown, res.Msg))
	}
	return append(errs, errors.New(res.Msg))
}

func snapshotForwardMembers(forward *model.Forward, tunnel *model.Tunnel, persisted []model.ForwardExitMember) []model.ForwardExitMember {
	if len(persisted) == 0 {
		if forward.OutPort == nil || *forward.OutPort <= 0 || tunnel.OutNodeID == 0 {
			return nil
		}
		return []model.ForwardExitMember{{ForwardID: forward.ID, OutNodeID: tunnel.OutNodeID, OutPort: *forward.OutPort, Status: 1, Active: 1}}
	}
	if normalizeExitMode(forward.ExitMode) == exitModeBalance {
		out := make([]model.ForwardExitMember, 0, len(persisted))
		for _, member := range persisted {
			if member.Status == 1 {
				out = append(out, member)
			}
		}
		return out
	}
	for _, member := range persisted {
		if member.Status == 1 && member.Active == 1 {
			return []model.ForwardExitMember{member}
		}
	}
	for _, member := range persisted {
		if member.Status == 1 {
			return []model.ForwardExitMember{member}
		}
	}
	return nil
}

// UpdateUserTunnel 更新用户隧道权限
func UpdateUserTunnel(req dto.UserTunnelUpdateDto) result.R {
	if req.Status != nil && *req.Status != 0 && *req.Status != 1 {
		return result.Err("用户隧道权限状态无效")
	}
	snapshot, unlock, err := lockUserTunnelRuntimeSnapshot(req.ID)
	if err != nil {
		return result.Err("用户隧道权限不存在")
	}
	defer unlock()
	ut := snapshot.permission
	failMsg := ""
	speedChanged, statusChanged := false, false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&ut, req.ID).Error; err != nil {
			failMsg = "用户隧道权限不存在"
			return err
		}
		if msg := validateSpeedLimitForTunnelDB(tx, req.SpeedID, ut.TunnelID); msg != "" {
			failMsg = msg
			return errors.New(msg)
		}
		speedChanged = !int64PtrEqual(ut.SpeedID, req.SpeedID)
		statusChanged = req.Status != nil && *req.Status != ut.Status
		updates := map[string]interface{}{
			"flow":     req.Flow,
			"num":      req.Num,
			"speed_id": req.SpeedID,
		}
		if req.FlowResetTime != nil {
			updates["flow_reset_time"] = *req.FlowResetTime
			ut.FlowResetTime = *req.FlowResetTime
		}
		if req.ExpTime != nil {
			updates["exp_time"] = req.ExpTime
			ut.ExpTime = req.ExpTime
		}
		if req.Status != nil {
			updates["status"] = *req.Status
			ut.Status = *req.Status
		}
		updated := tx.Model(&model.UserTunnel{}).Where("id = ?", req.ID).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			failMsg = "用户隧道权限不存在"
			return errors.New(failMsg)
		}
		ut.Flow = req.Flow
		ut.Num = req.Num
		ut.SpeedID = req.SpeedID
		return nil
	})
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("用户隧道权限更新失败")
	}

	if speedChanged || statusChanged {
		if err := convergeUserTunnelRuntime(&snapshot, req.SpeedID, req.Status, speedChanged, statusChanged); err != nil {
			if errors.Is(err, errUserTunnelRuntimeOutcomeUnknown) {
				markNodesDirtyBestEffort(snapshot.nodeIDs...)
				return result.Err("用户隧道权限运行态待重连收敛：" + err.Error())
			}
			restoreErr := restoreUserTunnelDesiredSnapshot(snapshot)
			var compensateErr error
			if restoreErr == nil {
				compensateErr = compensateUserTunnelRuntime(&snapshot, speedChanged, statusChanged)
			}
			return result.Err("用户隧道权限同步失败：" + errors.Join(err, restoreErr, compensateErr).Error())
		}
	}
	return result.OkMsg("用户隧道权限更新成功")
}

func compensateUserTunnelRuntime(snapshot *userTunnelRuntimeSnapshot, speedChanged, statusChanged bool) error {
	var errs []error
	for i := range snapshot.forwards {
		forward := &snapshot.forwards[i]
		if forward.Status != forwardStatusActive {
			continue
		}
		if snapshot.permission.Status == 1 {
			if err := updateUserTunnelForwardRuntime(snapshot, forward, snapshot.permission.SpeedID); err != nil {
				errs = append(errs, fmt.Errorf("forward %d restore config: %w", forward.ID, err))
			}
		} else if statusChanged {
			if err := pauseUserTunnelForwardRuntime(snapshot, forward); err != nil {
				errs = append(errs, fmt.Errorf("forward %d restore status: %w", forward.ID, err))
			}
		}
	}
	if err := refreshNftNodesDesiredCheckedLocked(snapshot.nodeIDs); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func convergeUserTunnelRuntime(snapshot *userTunnelRuntimeSnapshot, speedID *int64, requestedStatus *int, speedChanged, statusChanged bool) error {
	var errs []error
	permissionActive := snapshot.permission.Status == 1
	if requestedStatus != nil {
		permissionActive = *requestedStatus == 1
	}
	for i := range snapshot.forwards {
		forward := &snapshot.forwards[i]
		if forward.Status != forwardStatusActive {
			continue
		}
		if statusChanged {
			if permissionActive {
				if err := updateUserTunnelForwardRuntime(snapshot, forward, speedID); err != nil {
					errs = append(errs, fmt.Errorf("forward %d rebuild: %w", forward.ID, err))
				}
				continue
			}
			if err := pauseUserTunnelForwardRuntime(snapshot, forward); err != nil {
				errs = append(errs, fmt.Errorf("forward %d status: %w", forward.ID, err))
				continue
			}
		}
		if speedChanged && permissionActive {
			if err := updateUserTunnelForwardRuntime(snapshot, forward, speedID); err != nil {
				errs = append(errs, fmt.Errorf("forward %d speed: %w", forward.ID, err))
			}
		}
	}
	if err := refreshNftNodesDesiredCheckedLocked(snapshot.nodeIDs); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func pauseUserTunnelForwardRuntime(snapshot *userTunnelRuntimeSnapshot, forward *model.Forward) error {
	serviceName := userTunnelSnapshotServiceName(snapshot, forward)
	var errs []error
	if inNode, ok := snapshot.nodes[snapshot.tunnel.InNodeID]; ok && !isNftablesMode(&inNode) {
		res := pauseUserTunnelGostService(inNode.ID, serviceName)
		errs = appendGostMutationError(errs, res)
	}
	if snapshot.tunnel.Type == tunnelTypeTunnelForward {
		for _, member := range snapshotForwardMembers(forward, &snapshot.tunnel, snapshot.members[forward.ID]) {
			node, ok := snapshot.nodes[member.OutNodeID]
			if !ok || isNftablesMode(&node) {
				continue
			}
			res := pauseUserTunnelGostRemoteService(node.ID, serviceName)
			errs = appendGostMutationError(errs, res)
		}
	}
	return errors.Join(errs...)
}

func updateUserTunnelForwardRuntime(snapshot *userTunnelRuntimeSnapshot, forward *model.Forward, speedID *int64) error {
	remoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		return err
	}
	serviceName := userTunnelSnapshotServiceName(snapshot, forward)
	members := snapshotForwardMembers(forward, &snapshot.tunnel, snapshot.members[forward.ID])
	var errs []error
	inNode, entryExists := snapshot.nodes[snapshot.tunnel.InNodeID]
	entryGost := entryExists && !isNftablesMode(&inNode)
	if snapshot.tunnel.Type == tunnelTypeTunnelForward {
		if entryGost {
			chainTargets := snapshotForwardExitChainTargets(members, snapshot.nodes)
			if chainTargets == "" {
				errs = append(errs, errors.New("出口节点配置无效"))
			} else {
				res := updateUserTunnelGostChains(inNode.ID, serviceName, chainTargets, tunnelProtocol(&snapshot.tunnel), tunnelInterfaceName(&snapshot.tunnel), forward.ExitStrategy)
				if strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
					res = addUserTunnelGostChains(inNode.ID, serviceName, chainTargets, tunnelProtocol(&snapshot.tunnel), tunnelInterfaceName(&snapshot.tunnel), forward.ExitStrategy)
				}
				errs = appendGostMutationError(errs, res)
			}
		}
		for _, member := range members {
			node, ok := snapshot.nodes[member.OutNodeID]
			if !ok {
				errs = append(errs, fmt.Errorf("出口节点 %d 不存在", member.OutNodeID))
				continue
			}
			if isNftablesMode(&node) {
				continue
			}
			res := updateUserTunnelGostRemoteService(node.ID, serviceName, member.OutPort, remoteAddr, tunnelProtocol(&snapshot.tunnel), forward.Strategy, interfaceNameOf(forward))
			if strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
				res = addUserTunnelGostRemoteService(node.ID, serviceName, member.OutPort, remoteAddr, tunnelProtocol(&snapshot.tunnel), forward.Strategy, interfaceNameOf(forward))
			}
			errs = appendGostMutationError(errs, res)
		}
	}
	if entryGost {
		interfaceName := ""
		if snapshot.tunnel.Type != tunnelTypeTunnelForward {
			interfaceName = interfaceNameOf(forward)
		}
		res := updateUserTunnelGostService(inNode.ID, serviceName, forward.InPort, speedID, remoteAddr, snapshot.tunnel.Type, &snapshot.tunnel, forward.Strategy, interfaceName)
		if strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
			res = addUserTunnelGostService(inNode.ID, serviceName, forward.InPort, speedID, remoteAddr, snapshot.tunnel.Type, &snapshot.tunnel, forward.Strategy, interfaceName)
		}
		errs = appendGostMutationError(errs, res)
	}
	return errors.Join(errs...)
}

func snapshotForwardExitChainTargets(members []model.ForwardExitMember, nodes map[int64]model.Node) string {
	targets := make([]string, 0, len(members))
	for _, member := range members {
		node, ok := nodes[member.OutNodeID]
		if !ok || strings.TrimSpace(node.ServerIP) == "" || member.OutPort <= 0 {
			continue
		}
		targets = append(targets, joinHostPort(node.ServerIP, member.OutPort))
	}
	return strings.Join(targets, ",")
}

func restoreUserTunnelDesiredSnapshot(snapshot userTunnelRuntimeSnapshot) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"flow": snapshot.permission.Flow, "num": snapshot.permission.Num, "speed_id": snapshot.permission.SpeedID,
			"flow_reset_time": snapshot.permission.FlowResetTime, "exp_time": snapshot.permission.ExpTime, "status": snapshot.permission.Status,
		}
		if err := tx.Model(&model.UserTunnel{}).Where("id = ?", snapshot.permission.ID).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func validateUserTunnelAssignmentDB(db *gorm.DB, userID, tunnelID int64, speedID *int64) string {
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		return "用户不存在"
	}
	var tunnel model.Tunnel
	if err := db.First(&tunnel, tunnelID).Error; err != nil {
		return "隧道不存在"
	}
	return validateSpeedLimitForTunnelDB(db, speedID, tunnelID)
}

func validateSpeedLimitForTunnelDB(db *gorm.DB, speedID *int64, tunnelID int64) string {
	if speedID == nil {
		return ""
	}
	var sl model.SpeedLimit
	if err := db.First(&sl, *speedID).Error; err != nil {
		return "限速规则不存在"
	}
	if sl.TunnelID != tunnelID {
		return "限速规则不属于该隧道"
	}
	return ""
}
