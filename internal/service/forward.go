package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	forwardStatusActive = 1
	forwardStatusPaused = 0
	forwardStatusError  = -1
	tunnelStatusActive  = 1
)

// CurrentUser 当前请求用户上下文
type CurrentUser struct {
	UserID   int64
	RoleID   int
	UserName string
}

// permissionResult 权限检查结果
type permissionResult struct {
	limiter    *int64
	userTunnel *model.UserTunnel
	errMsg     string
}

func (p *permissionResult) hasError() bool { return p.errMsg != "" }

// portAllocMu 串行化端口分配到落库的过程，避免并发请求分到相同端口
var portAllocMu sync.Mutex

// CreateForward 创建转发
func CreateForward(cu CurrentUser, req dto.ForwardDto) result.R {
	_, r := createForwardInternal(cu, req)
	return r
}

func createForwardInternal(cu CurrentUser, req dto.ForwardDto) (*model.Forward, result.R) {
	// 隧道校验
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, req.TunnelID).Error; err != nil {
		return nil, result.Err("隧道不存在")
	}
	if tunnel.Status != tunnelStatusActive {
		return nil, result.Err("隧道已禁用，无法创建转发")
	}

	// 权限与配额检查
	perm := checkUserPermissions(cu, &tunnel, nil)
	if perm.hasError() {
		return nil, result.Err(perm.errMsg)
	}

	inNode, outNode, errMsg := getRequiredNodes(&tunnel)
	if errMsg != "" {
		return nil, result.Err(errMsg)
	}
	exitMode := normalizeExitMode(req.ExitMode)
	exitStrategy := normalizeExitStrategy(req.ExitStrategy)
	exitMembers, errMsg := normalizeForwardExitMembers(&tunnel, exitMode, req.ExitMembers)
	if errMsg != "" {
		return nil, result.Err(errMsg)
	}
	if tunnel.Type == tunnelTypeTunnelForward && isNftablesMode(inNode) && exitMode == exitModeBalance {
		return nil, result.Err("nftables 模式暂不支持自动出口负载均衡，请使用手动负载")
	}
	targetCfg, errMsg := normalizeForwardTargetConfig(req.TargetMode, req.RemoteAddr, req.ActiveRemoteAddr, forwardTargetsRequireLiteralIP(&tunnel, inNode, outNode, exitMembers))
	if errMsg != "" {
		return nil, result.Err(errMsg)
	}
	if errMsg := validateRelayTargetConfig(&tunnel, targetCfg); errMsg != "" {
		return nil, result.Err(errMsg)
	}
	if errMsg := validateForwardNftNodeTargets(&tunnel, inNode, outNode, exitMembers); errMsg != "" {
		return nil, result.Err(errMsg)
	}
	affected := requestedRuntimeNodeIDs(&tunnel, exitMembers)
	unlockSaga := lockNftSagaNodes(affected)
	defer unlockSaga()
	var lockedTunnel model.Tunnel
	if err := model.DB.First(&lockedTunnel, tunnel.ID).Error; err != nil || lockedTunnel.Status != tunnelStatusActive ||
		lockedTunnel.Type != tunnel.Type || lockedTunnel.InNodeID != tunnel.InNodeID || lockedTunnel.OutNodeID != tunnel.OutNodeID ||
		tunnelRelayNodeID(&lockedTunnel) != tunnelRelayNodeID(&tunnel) {
		return nil, result.Err("隧道已变更，请重试")
	}
	tunnel = lockedTunnel
	inNode, outNode, errMsg = getRequiredNodes(&tunnel)
	if errMsg != "" {
		return nil, result.Err(errMsg)
	}

	// 端口分配 + 保存转发（同锁内完成，防止并发分配相同端口）
	portAllocMu.Lock()

	inPort, outPort, errMsg := allocatePorts(&tunnel, req.InPort, nil)
	if errMsg != "" {
		portAllocMu.Unlock()
		return nil, result.Err(errMsg)
	}

	// 保存转发（用事务包裹，确保 Create 与后续可能的回滚 Delete 原子完成）
	now := time.Now().UnixMilli()
	strategy := req.Strategy
	if strategy == "" {
		strategy = "fifo"
	}
	forward := model.Forward{
		UserID:           cu.UserID,
		UserName:         cu.UserName,
		Name:             req.Name,
		TunnelID:         req.TunnelID,
		InPort:           inPort,
		OutPort:          outPort,
		RemoteAddr:       targetCfg.RemoteAddr,
		Strategy:         strategy,
		TargetMode:       targetCfg.Mode,
		ActiveRemoteAddr: targetCfg.ActiveAddr,
		ExitMode:         exitMode,
		ExitStrategy:     exitStrategy,
		InterfaceName:    req.InterfaceName,
		CreatedTime:      now,
		UpdatedTime:      now,
		Status:           forwardStatusActive,
		Inx:              nextForwardIndex(),
	}
	var createMsg string
	var permissionMsg string
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		freshPerm := checkUserPermissionsDB(tx, cu, &tunnel, nil)
		if freshPerm.hasError() {
			permissionMsg = freshPerm.errMsg
			return errors.New(permissionMsg)
		}
		perm = freshPerm
		if err := tx.Create(&forward).Error; err != nil {
			return err
		}
		if _, msg := saveForwardExitMembersWithTx(tx, &forward, &tunnel, exitMembers, &forward.ID); msg != "" {
			createMsg = msg
			return errors.New(msg)
		}
		return nil
	}); err != nil {
		portAllocMu.Unlock()
		if createMsg != "" {
			return nil, result.Err(createMsg)
		}
		if permissionMsg != "" {
			return nil, result.Err(permissionMsg)
		}
		return nil, result.Err("端口转发创建失败")
	}
	portAllocMu.Unlock()

	// nftables mode deploys one atomic batch per affected node. Old agents that
	// do not know the batch command use a checked full refresh against the newly
	// persisted desired state. Any failure removes desired state first, then
	// refreshes every affected node to clean a partially deployed runtime.
	// Mixed topologies are valid: Gost helpers operate only on Gost nodes while
	// nftables nodes converge from the same committed desired-state snapshot.
	if r := createGostServices(&forward, &tunnel, perm.limiter, inNode, outNode, perm.userTunnel); r.Code != 0 {
		return nil, result.Err(fmt.Sprintf("创建运行配置失败: %v", failCreatedForwardSaga(errors.New(r.Msg), &forward, &tunnel, inNode, outNode, perm.userTunnel, affected)))
	}
	var deployErr error
	if tunnelHasRelay(&tunnel) {
		// C、B 均确认规则就绪后才启用 A，避免下游离线时先暴露入口黑洞。
		deployErr = refreshNftNodesUntilErrorLocked(reverseNodeIDs(affected))
	} else {
		deployErr = refreshNftNodesCheckedLocked(affected)
	}
	if deployErr != nil {
		return nil, result.Err(fmt.Sprintf("创建 nft 规则失败: %v", failCreatedForwardSaga(deployErr, &forward, &tunnel, inNode, outNode, perm.userTunnel, affected)))
	}
	return &forward, result.Ok("端口转发创建成功")
}

func GetAllForwards(cu CurrentUser) result.R {
	var list []dto.ForwardWithTunnel
	// 常量片段（无用户输入），统一放进查询模板；仅用户 ID 走参数化
	query := `
		SELECT
			f.id, f.user_id AS user_id, f.name, f.tunnel_id AS tunnel_id,
			f.in_port AS in_port, f.out_port AS out_port, f.remote_addr AS remote_addr,
			f.status, f.created_time AS created_time, f.updated_time AS updated_time,
			f.user_name AS user_name, f.in_flow AS in_flow, f.out_flow AS out_flow,
			f.strategy AS strategy, f.target_mode AS target_mode, f.active_remote_addr AS active_remote_addr,
			f.inx AS inx, f.interface_name AS interface_name,
			f.exit_mode AS exit_mode, f.exit_strategy AS exit_strategy,
			t.name AS tunnel_name, t.in_ip AS in_ip, t.out_ip AS out_ip, t.type, t.protocol
		FROM forward f
		LEFT JOIN tunnel t ON f.tunnel_id = t.id`
	var res *gorm.DB
	if cu.RoleID != adminRoleID {
		res = model.DB.Raw(query+" WHERE f.user_id = ? "+forwardListOrderSQL(), cu.UserID).Scan(&list)
	} else {
		res = model.DB.Raw(query + " " + forwardListOrderSQL()).Scan(&list)
	}
	if res.Error != nil {
		return result.Err("查询转发列表失败")
	}
	if list == nil {
		list = []dto.ForwardWithTunnel{}
	}
	normalizeForwardListIndexes(list)
	attachForwardExitMemberViews(list)
	return result.Ok(list)
}

func forwardListOrderSQL() string {
	return "ORDER BY CASE WHEN f.inx > 0 THEN f.inx ELSE 2147483647 END ASC, f.created_time DESC"
}

func normalizeForwardListIndexes(list []dto.ForwardWithTunnel) {
	next := 1
	for i := range list {
		if list[i].Inx >= next {
			next = list[i].Inx + 1
		}
	}
	for i := range list {
		if list[i].Inx <= 0 {
			list[i].Inx = next
			next++
		}
	}
}

func attachForwardExitMemberViews(list []dto.ForwardWithTunnel) {
	ids := make([]int64, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.ID)
	}
	views := forwardExitMemberViews(ids)
	for i := range list {
		list[i].TargetMode = normalizeTargetMode(list[i].TargetMode)
		list[i].ExitMode = normalizeExitMode(list[i].ExitMode)
		if strings.TrimSpace(list[i].ExitStrategy) == "" {
			list[i].ExitStrategy = exitStrategyFIFO
		}
		if members, ok := views[list[i].ID]; ok {
			list[i].ExitMembers = members
		} else {
			list[i].ExitMembers = []dto.ForwardExitMemberView{}
		}
	}
}

// UpdateForward 更新转发（处理隧道变更/端口重分配/gost 同步）
func UpdateForward(cu CurrentUser, req dto.ForwardUpdateDto) result.R {
	if cu.RoleID != adminRoleID {
		var user model.User
		if err := model.DB.First(&user, cu.UserID).Error; err != nil {
			return result.Err("用户不存在")
		}
		if !isActiveUserStatus(user.Status) {
			return result.Err("用户已到期或被禁用")
		}
	}
	return updateForwardInternal(req, cu.UserID, cu.RoleID, cu.UserName)
}

// updateForwardInternal 更新核心逻辑（也被隧道更新同步调用，此时以管理员身份执行）
func updateForwardInternal(req dto.ForwardUpdateDto, opUserID int64, opRoleID int, opUserName string) result.R {
	return updateForwardInternalWithFreshCurrent(req, opUserID, opRoleID, opUserName, false)
}

// syncForwardAfterTunnelUpdate re-applies tunnel-derived runtime state while
// preserving the forward's latest user-controlled fields. If the forward was
// moved away after the tunnel transaction, there is nothing left to sync.
func syncForwardAfterTunnelUpdate(forwardID, expectedTunnelID int64) result.R {
	req := dto.ForwardUpdateDto{ID: forwardID, TunnelID: expectedTunnelID}
	return updateForwardInternalWithFreshCurrent(req, 0, adminRoleID, "", true)
}

func updateForwardInternalWithFreshCurrent(req dto.ForwardUpdateDto, opUserID int64, opRoleID int, opUserName string, useFreshCurrent bool) result.R {
	// 转发存在性 + 归属校验
	existForward := validateForwardExists(req.ID, opUserID, opRoleID)
	if existForward == nil {
		return result.Err("转发不存在")
	}

	// 隧道校验
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, req.TunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	if tunnel.Status != tunnelStatusActive {
		return result.Err("隧道已禁用，无法更新转发")
	}

	candidateNodes := tunnelPathNodeIDs(&tunnel)
	for _, member := range req.ExitMembers {
		candidateNodes = append(candidateNodes, member.OutNodeID)
	}
	var oldTunnel model.Tunnel
	var unlockSaga func()
	var lockErr error
	existForward, oldTunnel, unlockSaga, lockErr = lockForwardSagaSnapshot(req.ID, candidateNodes)
	if lockErr != nil {
		return result.Err("原隧道不存在")
	}
	defer unlockSaga()
	if useFreshCurrent {
		if existForward.TunnelID != req.TunnelID {
			return result.Ok("端口转发已移出该隧道，无需同步")
		}
		inPort := existForward.InPort
		req.UserID = existForward.UserID
		req.Name = existForward.Name
		req.RemoteAddr = existForward.RemoteAddr
		req.Strategy = existForward.Strategy
		req.InPort = &inPort
		req.InterfaceName = existForward.InterfaceName
	}
	var lockedNewTunnel model.Tunnel
	if err := model.DB.First(&lockedNewTunnel, req.TunnelID).Error; err != nil || lockedNewTunnel.Status != tunnelStatusActive ||
		lockedNewTunnel.Type != tunnel.Type || lockedNewTunnel.InNodeID != tunnel.InNodeID || lockedNewTunnel.OutNodeID != tunnel.OutNodeID ||
		tunnelRelayNodeID(&lockedNewTunnel) != tunnelRelayNodeID(&tunnel) {
		return result.Err("目标隧道已变更，请重试")
	}
	tunnel = lockedNewTunnel
	tunnelChanged := existForward.TunnelID != req.TunnelID
	recoverErrorStatus := existForward.Status == forwardStatusError
	wantsRuntime := existForward.Status == forwardStatusActive || recoverErrorStatus
	permissionProbe := *existForward
	permissionProbe.TunnelID = tunnel.ID
	runtimePermissionAllowed, err := forwardPermissionAllowsRuntimeDB(model.DB, &permissionProbe)
	if err != nil {
		return result.Err("运行权限校验失败")
	}
	oldRuntimePermissionAllowed, err := forwardPermissionAllowsRuntimeDB(model.DB, existForward)
	if err != nil {
		return result.Err("原运行权限校验失败")
	}
	syncRuntime := wantsRuntime && runtimePermissionAllowed

	exitMode := normalizeExitMode(req.ExitMode)
	exitStrategy := normalizeExitStrategy(req.ExitStrategy)
	requestHasExitConfig := strings.TrimSpace(req.ExitMode) != "" || req.ExitMembers != nil
	exitMemberRequest := req.ExitMembers
	if tunnel.Type == tunnelTypeTunnelForward && !tunnelChanged && !requestHasExitConfig {
		exitMode = normalizeExitMode(existForward.ExitMode)
		exitStrategy = normalizeExitStrategy(existForward.ExitStrategy)
		exitMemberRequest = forwardExitMemberDtosFromExisting(existForward, &tunnel)
	}
	exitMembers, errMsg := normalizeForwardExitMembers(&tunnel, exitMode, exitMemberRequest)
	if errMsg != "" {
		return result.Err(errMsg)
	}
	// Resolve all nodes before target validation. Whether a target may be DNS is
	// determined by every node that can generate nftables rules for it.
	inNode, outNode, errMsg := getRequiredNodes(&tunnel)
	if errMsg != "" {
		return result.Err(errMsg)
	}
	targetModeRequest := req.TargetMode
	activeRemoteRequest := req.ActiveRemoteAddr
	if strings.TrimSpace(req.TargetMode) == "" && strings.TrimSpace(req.ActiveRemoteAddr) == "" {
		targetModeRequest = existForward.TargetMode
		activeRemoteRequest = existForward.ActiveRemoteAddr
	}
	targetCfg, errMsg := normalizeForwardTargetConfig(targetModeRequest, req.RemoteAddr, activeRemoteRequest, forwardTargetsRequireLiteralIP(&tunnel, inNode, outNode, exitMembers))
	if errMsg != "" {
		return result.Err(errMsg)
	}
	if errMsg := validateRelayTargetConfig(&tunnel, targetCfg); errMsg != "" {
		return result.Err(errMsg)
	}
	if errMsg := validateForwardNftNodeTargets(&tunnel, inNode, outNode, exitMembers); errMsg != "" {
		return result.Err(errMsg)
	}
	// 权限检查：编辑不等于恢复。暂停转发可保存配置但不会部署运行态；
	// 活跃/异常转发或切换隧道时必须复用完整权限/流量/到期校验。
	var perm *permissionResult
	if opRoleID == adminRoleID {
		if tunnelChanged {
			if opUserID == existForward.UserID {
				perm = &permissionResult{}
			} else {
				// 管理员操作他人转发：检查原用户在新隧道的权限
				var originalUser model.User
				if err := model.DB.First(&originalUser, existForward.UserID).Error; err != nil {
					return result.Err("用户不存在")
				}
				if !isActiveUserStatus(originalUser.Status) {
					return result.Err("用户已到期或被禁用")
				}
				if originalUser.ExpTime != nil && *originalUser.ExpTime <= time.Now().UnixMilli() {
					return result.Err("当前账号已到期")
				}
				ut := getUserTunnel(existForward.UserID, tunnel.ID)
				if ut == nil {
					return result.Err("用户没有该隧道权限")
				}
				if ut.Status != 1 {
					return result.Err("隧道被禁用")
				}
				if ut.ExpTime != nil && *ut.ExpTime <= time.Now().UnixMilli() {
					return result.Err("用户的该隧道权限已到期")
				}
				if totalFlowBytes(originalUser.InFlow, originalUser.OutFlow) >= flowLimitBytes(originalUser.Flow) {
					return result.Err("用户总流量已用完")
				}
				if totalFlowBytes(ut.InFlow, ut.OutFlow) >= flowLimitBytes(ut.Flow) {
					return result.Err("该隧道流量已用完")
				}
				if msg := checkForwardQuota(existForward.UserID, tunnel.ID, ut, &originalUser, &req.ID); msg != "" {
					return result.Err("用户" + msg)
				}
				perm = &permissionResult{limiter: ut.SpeedID, userTunnel: ut}
			}
		}
	} else if tunnelChanged || wantsRuntime {
		p := checkUserPermissions(CurrentUser{UserID: opUserID, RoleID: opRoleID, UserName: opUserName}, &tunnel, &req.ID)
		if p.hasError() {
			return result.Err(p.errMsg)
		}
		perm = &p
	}

	// 获取 UserTunnel（用于服务名）
	var userTunnel *model.UserTunnel
	if opRoleID != adminRoleID {
		if perm != nil {
			userTunnel = perm.userTunnel
		}
		if userTunnel == nil {
			userTunnel = getUserTunnel(opUserID, tunnel.ID)
		}
		if userTunnel == nil {
			return result.Err("你没有该隧道权限")
		}
	} else {
		userTunnel = getUserTunnel(existForward.UserID, tunnel.ID)
	}

	// 构建更新实体（端口重分配）
	updated := *existForward
	updated.Name = req.Name
	updated.TunnelID = req.TunnelID
	updated.RemoteAddr = targetCfg.RemoteAddr
	updated.Strategy = req.Strategy
	if updated.Strategy == "" {
		updated.Strategy = "fifo"
	}
	updated.TargetMode = targetCfg.Mode
	updated.ActiveRemoteAddr = targetCfg.ActiveAddr
	updated.ExitMode = exitMode
	updated.ExitStrategy = exitStrategy
	updated.InterfaceName = req.InterfaceName

	// 节点信息已经在任何写入前解析并用于目标模式校验。
	if tunnel.Type == tunnelTypeTunnelForward && isNftablesMode(inNode) && exitMode == exitModeBalance {
		return result.Err("nftables 模式暂不支持自动出口负载均衡，请使用手动负载")
	}

	inPortChanged := req.InPort != nil && *req.InPort != existForward.InPort
	oldPersistedExitMembers := loadPersistedForwardExitMembers(req.ID)
	oldDeployExitMembers := loadForwardExitMembers(existForward, &oldTunnel)
	exitConfigNeedsSave := tunnel.Type == tunnelTypeTunnelForward || oldTunnel.Type == tunnelTypeTunnelForward
	portLockHeld := tunnelChanged || inPortChanged || exitConfigNeedsSave
	persistErr := func() error {
		if portLockHeld {
			portAllocMu.Lock()
			defer portAllocMu.Unlock()
		}
		if tunnelChanged || inPortChanged || exitConfigNeedsSave {
			specified := req.InPort
			if specified == nil && !tunnelChanged {
				p := existForward.InPort
				specified = &p
			}
			if tunnelChanged || inPortChanged {
				inPort, outPort, allocationMsg := allocatePorts(&tunnel, specified, &req.ID)
				if allocationMsg != "" {
					return errors.New(allocationMsg)
				}
				updated.InPort = inPort
				updated.OutPort = outPort
			}
		}
		updated.UpdatedTime = time.Now().UnixMilli()
		updated.Status = existForward.Status
		if recoverErrorStatus && (runtimePermissionAllowed || (wantsRuntime && !runtimePermissionAllowed)) {
			updated.Status = forwardStatusActive
		}
		return model.DB.Transaction(func(tx *gorm.DB) error {
			if exitConfigNeedsSave {
				if _, msg := saveForwardExitMembersWithTx(tx, &updated, &tunnel, exitMembers, &req.ID); msg != "" {
					return errors.New(msg)
				}
			}
			return tx.Save(&updated).Error
		})
	}()
	if persistErr != nil {
		return result.Err(fmt.Sprintf("端口转发更新失败: %v", persistErr))
	}

	rollbackPorts := func() error {
		return restoreForwardDesiredSnapshot(*existForward, oldPersistedExitMembers)
	}
	affected := append(tunnelPathNodeIDs(&oldTunnel), tunnelPathNodeIDs(&tunnel)...)
	for _, member := range oldDeployExitMembers {
		affected = append(affected, member.OutNodeID)
	}
	newDeployExitMembers := loadForwardExitMembers(&updated, &tunnel)
	for _, member := range newDeployExitMembers {
		affected = append(affected, member.OutNodeID)
	}
	if wantsRuntime && !runtimePermissionAllowed {
		// The new desired graph is non-serving. If this update moved a live
		// forward away from an allowed tunnel, retire the old Gost graph after
		// the durable desired write. A lost response intentionally retains the
		// blocked desired state so reconnect reconciliation can finish cleanup.
		var cleanupErr error
		if tunnelChanged {
			oldUT := getUserTunnel(existForward.UserID, oldTunnel.ID)
			if oldUT != nil || oldRuntimePermissionAllowed {
				snapshot, err := forwardRuntimeSnapshot(model.DB, existForward, &oldTunnel, oldUT, oldDeployExitMembers)
				if err != nil {
					cleanupErr = err
				} else {
					cleanupErr = cleanupUserTunnelForwardRuntime(&snapshot, existForward)
				}
			}
		}
		refreshErr := refreshNftNodesDeletingCheckedLocked(affected)
		if combined := errors.Join(cleanupErr, refreshErr); combined != nil {
			if errors.Is(combined, errUserTunnelRuntimeOutcomeUnknown) {
				markNodesDirtyBestEffort(affected...)
				return result.OkMsg("端口转发更新已接受，旧运行态等待节点重连清理")
			}
			// A definite cleanup failure must retain the old graph as a durable
			// error tombstone. Otherwise a retry observes only the new blocked
			// tunnel and loses the service names/nodes that still need cleanup.
			restoreErr := restoreForwardDesiredSnapshot(*existForward, oldPersistedExitMembers)
			var markErr error
			if restoreErr == nil {
				markErr = markForwardErrorDesired(existForward.ID)
			}
			var convergeErr error
			if restoreErr == nil && markErr == nil {
				convergeErr = refreshNftNodesDeletingCheckedLocked(affected)
			}
			return result.Err(fmt.Sprintf("端口转发更新运行态清理失败: %v", errors.Join(combined, restoreErr, markErr, convergeErr)))
		}
		return result.Ok("端口转发更新成功")
	}

	var rollbackRuntime func(error) result.R
	if syncRuntime {
		var limiter *int64
		if perm != nil {
			limiter = perm.limiter
		} else if userTunnel != nil {
			limiter = userTunnel.SpeedID
		}
		oldInNode, oldOutNode, oldNodeMsg := getRequiredNodes(&oldTunnel)
		if oldNodeMsg != "" {
			return result.Err(oldNodeMsg)
		}
		oldUT := getUserTunnel(existForward.UserID, oldTunnel.ID)
		var oldLimiter *int64
		if oldUT != nil {
			oldLimiter = oldUT.SpeedID
		}
		rollbackRuntime = func(primary error) result.R {
			restoreErr := rollbackPorts()
			var cleanupNewErr, runtimeRestoreErr error
			if restoreErr == nil {
				if cleaned := deleteGostServicesWithMembers(&updated, &tunnel, inNode, outNode, userTunnel, newDeployExitMembers); cleaned.Code != 0 {
					cleanupNewErr = errors.New(cleaned.Msg)
				}
				if restored := updateGostServices(existForward, &oldTunnel, oldLimiter, oldInNode, oldOutNode, oldUT); restored.Code != 0 {
					runtimeRestoreErr = errors.New(restored.Msg)
				}
			}
			refreshErr := error(nil)
			if restoreErr == nil {
				refreshErr = refreshNftNodesCheckedLocked(affected)
			}
			combined := errors.Join(primary, restoreErr, cleanupNewErr, runtimeRestoreErr, refreshErr)
			if restoreErr != nil || cleanupNewErr != nil || runtimeRestoreErr != nil || refreshErr != nil {
				markErr := markForwardErrorDesired(existForward.ID)
				combined = errors.Join(combined, markErr)
				if markErr == nil {
					combined = errors.Join(combined, refreshNftNodesCheckedLocked(affected))
				}
			}
			return result.Err(fmt.Sprintf("更新运行配置失败: %v", combined))
		}

		// Clean the exact old Gost graph before deploying the exact new graph.
		// This handles removed exits and both NFT↔Gost directions without leaving
		// a best-effort stale service behind.
		if cleaned := deleteGostServicesWithMembers(existForward, &oldTunnel, oldInNode, oldOutNode, oldUT, oldDeployExitMembers); cleaned.Code != 0 {
			return rollbackRuntime(errors.New(cleaned.Msg))
		}
		if deployed := createGostServices(&updated, &tunnel, limiter, inNode, outNode, userTunnel); deployed.Code != 0 {
			return rollbackRuntime(errors.New(deployed.Msg))
		}
	}

	finishForwardUpdate := func() result.R { return result.Ok("端口转发更新成功") }

	// nftables desired state has already been saved. Refresh the explicit union
	// of old/new nodes so removed exits are cleaned even though they are absent
	// from the new desired graph. Any failure restores the complete DB snapshot
	// before attempting a checked runtime compensation.
	if syncRuntime {
		if refreshErr := refreshNftNodesCheckedLocked(affected); refreshErr != nil {
			return rollbackRuntime(fmt.Errorf("更新 nft 规则失败: %w", refreshErr))
		}
		if req.ForceSwitchTarget {
			if err := FlushForwardConntrackForUpdate(&updated, &tunnel, inNode); err != nil {
				return rollbackRuntime(fmt.Errorf("强制切换失败: %w", err))
			}
		}
	}

	return finishForwardUpdate()
}

// DeleteForward 删除转发
func DeleteForward(cu CurrentUser, id int64) result.R {
	forward := validateForwardExists(id, cu.UserID, cu.RoleID)
	if forward == nil {
		return result.Err("端口转发不存在")
	}
	var tunnel model.Tunnel
	var unlockSaga func()
	var lockErr error
	forward, tunnel, unlockSaga, lockErr = lockForwardSagaSnapshot(id, nil)
	if lockErr != nil {
		return result.Err("隧道不存在")
	}
	defer unlockSaga()

	var userTunnel *model.UserTunnel
	if cu.RoleID != adminRoleID {
		userTunnel = getUserTunnel(cu.UserID, tunnel.ID)
		if userTunnel == nil {
			return result.Err("你没有该隧道权限")
		}
	} else {
		userTunnel = getUserTunnel(forward.UserID, tunnel.ID)
	}

	inNode, outNode, errMsg := getRequiredNodes(&tunnel)
	if errMsg != "" {
		return result.Err(errMsg)
	}
	runtimeAllowed, gateErr := forwardPermissionAllowsRuntimeDB(model.DB, forward)
	if gateErr != nil {
		return result.Err("运行权限校验失败")
	}

	snapshot := *forward
	members := loadPersistedForwardExitMembers(forward.ID)
	affected := nftRuntimeNodeIDs(forward, &tunnel)
	if r := deleteGostServicesWithMembers(forward, &tunnel, inNode, outNode, userTunnel, loadForwardExitMembers(forward, &tunnel)); r.Code != 0 {
		return r
	}
	restoreGost := func() error {
		if !runtimeAllowed || snapshot.Status != forwardStatusActive {
			return nil
		}
		var limiter *int64
		if userTunnel != nil {
			limiter = userTunnel.SpeedID
		}
		r := createGostServices(&snapshot, &tunnel, limiter, inNode, outNode, userTunnel)
		if r.Code != 0 {
			return errors.New(r.Msg)
		}
		return nil
	}
	forward.Status = forwardStatusPaused
	forward.UpdatedTime = time.Now().UnixMilli()
	if err := model.DB.Save(forward).Error; err != nil {
		return result.Err(fmt.Sprintf("保存删除期望状态失败: %v", errors.Join(err, restoreGost())))
	}
	if err := refreshNftNodesCheckedLocked(affected); err != nil {
		rollbackErr := rollbackForwardRuntimeSaga(err, snapshot, members, affected)
		return result.Err(fmt.Sprintf("刷新删除期望状态失败: %v", errors.Join(rollbackErr, restoreGost())))
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error { return deleteForwardRows(tx, id) }); err != nil {
		rollbackErr := rollbackForwardRuntimeSaga(err, snapshot, members, affected)
		return result.Err(fmt.Sprintf("端口转发删除失败: %v", errors.Join(rollbackErr, restoreGost())))
	}
	return result.Ok("端口转发删除成功")
}

// ForceDeleteForward 强制删除（跳过 gost）
func ForceDeleteForward(cu CurrentUser, id int64) result.R {
	forward := validateForwardExists(id, cu.UserID, cu.RoleID)
	if forward == nil {
		return result.Err("端口转发不存在")
	}
	forward, tunnel, unlockSaga, err := lockForwardSagaSnapshot(id, nil)
	if err != nil {
		return result.Err("隧道不存在")
	}
	defer unlockSaga()
	affected := nftRuntimeNodeIDs(forward, &tunnel)
	var flushErr error
	if inNode, _, msg := getRequiredNodes(&tunnel); msg == "" {
		flushErr = FlushForwardConntrackForUpdate(forward, &tunnel, inNode)
	}
	if err := deleteForwardRows(model.DB, id); err != nil {
		return result.Err("端口转发强制删除失败")
	}
	refreshErr := refreshNftNodesDeletingCheckedLocked(affected)
	if refreshErr != nil {
		markNodesDirtyBestEffort(affected...)
	}
	if flushErr != nil || refreshErr != nil {
		return result.OkMsg("端口转发已强制删除，离线节点或残留连接等待重连/超时清理")
	}
	return result.Ok("端口转发强制删除成功")
}

// PauseForward 暂停转发
func PauseForward(cu CurrentUser, id int64) result.R {
	return changeForwardStatus(cu, id, forwardStatusPaused, "暂停")
}

// ResumeForward 恢复转发
func ResumeForward(cu CurrentUser, id int64) result.R {
	return changeForwardStatus(cu, id, forwardStatusActive, "恢复")
}

// changeForwardStatus 暂停/恢复转发
func changeForwardStatus(cu CurrentUser, id int64, targetStatus int, operation string) result.R {
	if cu.RoleID != adminRoleID {
		var user model.User
		if err := model.DB.First(&user, cu.UserID).Error; err != nil {
			return result.Err("用户不存在")
		}
		if !isActiveUserStatus(user.Status) {
			return result.Err("用户已到期或被禁用")
		}
	}

	forward := validateForwardExists(id, cu.UserID, cu.RoleID)
	if forward == nil {
		return result.Err("转发不存在")
	}

	var tunnel model.Tunnel
	var unlockSaga func()
	var lockErr error
	forward, tunnel, unlockSaga, lockErr = lockForwardSagaSnapshot(id, nil)
	if lockErr != nil {
		return result.Err("隧道不存在")
	}
	defer unlockSaga()

	var userTunnel *model.UserTunnel
	if targetStatus == forwardStatusActive {
		if tunnel.Status != tunnelStatusActive {
			return result.Err("隧道已禁用，无法恢复服务")
		}
		allowed, err := forwardPermissionAllowsRuntimeDB(model.DB, forward)
		if err != nil {
			return result.Err("运行权限校验失败")
		}
		if !allowed {
			return result.Err("转发所属用户的隧道权限不可用")
		}
		if cu.RoleID != adminRoleID {
			if msg := checkUserFlowLimits(cu.UserID, &tunnel); msg != "" {
				return result.Err(msg)
			}
			userTunnel = getUserTunnel(cu.UserID, tunnel.ID)
			if userTunnel == nil {
				return result.Err("你没有该隧道权限")
			}
			if userTunnel.Status != 1 {
				return result.Err("隧道被禁用")
			}
		}
	}

	if cu.RoleID != adminRoleID && userTunnel == nil {
		userTunnel = getUserTunnel(cu.UserID, tunnel.ID)
		if userTunnel == nil {
			return result.Err("你没有该隧道权限")
		}
	}
	if userTunnel == nil {
		userTunnel = getUserTunnel(forward.UserID, tunnel.ID)
	}

	inNode, outNode, errMsg := getRequiredNodes(&tunnel)
	if errMsg != "" {
		return result.Err(errMsg)
	}

	snapshot := *forward
	members := loadPersistedForwardExitMembers(forward.ID)
	affected := nftRuntimeNodeIDs(forward, &tunnel)
	restoreGostStatus := func(primary error) error {
		restoreErr := changeGostRuntimeStatus(&snapshot, &tunnel, inNode, outNode, userTunnel, snapshot.Status)
		combined := errors.Join(primary, restoreErr)
		if restoreErr != nil {
			markErr := markForwardErrorDesired(snapshot.ID)
			combined = errors.Join(combined, markErr)
			if markErr == nil {
				combined = errors.Join(combined, refreshNftNodesCheckedLocked(affected))
			}
		}
		return combined
	}
	if err := changeGostRuntimeStatus(forward, &tunnel, inNode, outNode, userTunnel, targetStatus); err != nil {
		return result.Err(operation + "服务失败：" + restoreGostStatus(err).Error())
	}
	forward.Status = targetStatus
	forward.UpdatedTime = time.Now().UnixMilli()
	if err := model.DB.Save(forward).Error; err != nil {
		return result.Err(fmt.Sprintf("更新状态失败: %v", restoreGostStatus(err)))
	}
	if err := refreshNftNodesCheckedLocked(affected); err != nil {
		rollbackErr := rollbackForwardRuntimeSaga(err, snapshot, members, affected)
		return result.Err(fmt.Sprintf("%s刷新失败: %v", operation, restoreGostStatus(rollbackErr)))
	}

	return result.Ok("服务已" + operation)
}

func DiagnoseForward(cu CurrentUser, id int64) result.R {
	forward := validateForwardExists(id, cu.UserID, cu.RoleID)
	if forward == nil {
		return result.Err("转发不存在")
	}
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	var inNode model.Node
	if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil {
		return result.Err("入口节点不存在")
	}

	var results []DiagnosisResult
	remoteAddresses := splitRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if tunnel.Type == tunnelTypePortForward {
		for _, remote := range remoteAddresses {
			targetIP := extractHost(remote)
			targetPort := extractPort(remote)
			if targetIP == "" || targetPort == -1 {
				return result.Err("无法解析目标地址: " + remote)
			}
			results = append(results, performTcpPing(&inNode, targetIP, targetPort, "转发->目标", 2, 3000))
		}
	} else {
		member := activeForwardExitMember(forward, &tunnel)
		if member == nil {
			return result.Err("出口节点不存在")
		}
		var outNode model.Node
		if err := model.DB.First(&outNode, member.OutNodeID).Error; err != nil {
			return result.Err("出口节点不存在")
		}
		if relayNodeID := tunnelRelayNodeID(&tunnel); relayNodeID > 0 {
			var relayNode model.Node
			if err := model.DB.First(&relayNode, relayNodeID).Error; err != nil {
				return result.Err("中继节点不存在")
			}
			if member.RelayPort <= 0 || member.OutPort <= 0 {
				return result.Err("三节点转发端口配置不完整")
			}
			results = append(results, performTcpPing(&inNode, relayNode.ServerIP, member.RelayPort, "入口->中继", 1, 2500))
			results = append(results, performTcpPing(&relayNode, outNode.ServerIP, member.OutPort, "中继->出口", 1, 2500))
			for _, remote := range remoteAddresses {
				targetIP := extractHost(remote)
				targetPort := extractPort(remote)
				if targetIP == "" || targetPort == -1 {
					return result.Err("无法解析目标地址: " + remote)
				}
				results = append(results, performTcpPing(&outNode, targetIP, targetPort, "出口->目标", 1, 2500))
			}
		} else {
			// 两节点链路入口->出口：优先测 forward 的 OutPort；缺失时回退测 SSH 22。
			outPort := 22
			desc := "入口->出口"
			if member.OutPort > 0 {
				outPort = member.OutPort
			} else {
				desc = "入口->出口(无出口端口,默认测SSH 22)"
			}
			results = append(results, performTcpPing(&inNode, outNode.ServerIP, outPort, desc, 2, 3000))
			for _, remote := range remoteAddresses {
				targetIP := extractHost(remote)
				targetPort := extractPort(remote)
				if targetIP == "" || targetPort == -1 {
					return result.Err("无法解析目标地址: " + remote)
				}
				results = append(results, performTcpPing(&outNode, targetIP, targetPort, "出口->目标", 2, 3000))
			}
		}
	}

	tunnelTypeName := "端口转发"
	if tunnel.Type != tunnelTypePortForward {
		tunnelTypeName = "隧道转发"
	}
	return result.Ok(map[string]interface{}{
		"forwardId":   id,
		"forwardName": forward.Name,
		"tunnelType":  tunnelTypeName,
		"results":     results,
		"timestamp":   time.Now().UnixMilli(),
	})
}

// UpdateForwardOrder 更新转发排序。先解析并校验全部行（去重、归属完整性），
// 全部通过后再在一个事务内落库，避免部分更新留下不一致的排序。
func UpdateForwardOrder(cu CurrentUser, forwards []map[string]interface{}) result.R {
	if len(forwards) == 0 {
		return result.Err("forwards参数不能为空")
	}

	type orderUpdate struct {
		id  int64
		inx int64
	}
	parsed := make([]orderUpdate, 0, len(forwards))
	ids := make([]int64, 0, len(forwards))
	seen := make(map[int64]struct{}, len(forwards))
	for _, item := range forwards {
		id, ok := toInt64(item["id"])
		if !ok {
			return result.Err("forwards参数格式错误")
		}
		inx, ok := toInt64(item["inx"])
		if !ok {
			return result.Err("forwards参数格式错误")
		}
		if _, dup := seen[id]; dup {
			return result.Err("forwards参数存在重复项")
		}
		seen[id] = struct{}{}
		parsed = append(parsed, orderUpdate{id: id, inx: inx})
		ids = append(ids, id)
	}

	// 一次性加载全部目标行，比对完整的授权集合；普通用户按 user_id 归属过滤。
	q := model.DB.Model(&model.Forward{}).Where("id IN ?", ids)
	if cu.RoleID != adminRoleID {
		q = q.Where("user_id = ?", cu.UserID)
	}
	var owned []int64
	if err := q.Pluck("id", &owned).Error; err != nil {
		return result.Err("排序更新失败")
	}
	if len(owned) != len(ids) {
		if cu.RoleID != adminRoleID {
			return result.Err("只能更新自己的转发排序")
		}
		return result.Err("部分转发不存在")
	}

	// 校验全部通过后再落库：任何一步失败整体回滚，不产生部分排序。
	// 事务内仍带归属过滤并校验行数，消除校验与更新之间的 TOCTOU 窗口。
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		for _, u := range parsed {
			q := tx.Model(&model.Forward{}).Where("id = ?", u.id)
			if cu.RoleID != adminRoleID {
				q = q.Where("user_id = ?", cu.UserID)
			}
			res := q.Update("inx", u.inx)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errString("只能更新自己的转发排序")
			}
		}
		return nil
	}); err != nil {
		var msg errString
		if errors.As(err, &msg) {
			return result.Err(string(msg))
		}
		return result.Err("排序更新失败")
	}
	return result.Ok("排序更新成功")
}

func nextForwardIndex() int {
	var maxInx int
	if err := model.DB.Model(&model.Forward{}).Select("COALESCE(MAX(inx), 0)").Scan(&maxInx).Error; err != nil {
		return 1
	}
	return maxInx + 1
}

func toInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		i, err := strconv.ParseInt(x, 10, 64)
		return i, err == nil
	case json.Number:
		i, err := x.Int64()
		return i, err == nil
	}
	return 0, false
}

// ===== 私有辅助 =====

func intStr(v int) string {
	return strconv.Itoa(v)
}
