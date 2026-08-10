package service

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func forwardTargetsRequireLiteralIP(tunnel *model.Tunnel, inNode, outNode *model.Node, exitMembers []dto.ForwardExitMemberDto) bool {
	if tunnel == nil || tunnel.Type == tunnelTypePortForward {
		return isNftablesMode(inNode)
	}
	if isNftablesMode(outNode) {
		return true
	}
	for _, member := range exitMembers {
		var node model.Node
		if err := model.DB.First(&node, member.OutNodeID).Error; err == nil && isNftablesMode(&node) {
			return true
		}
	}
	return false
}

func validateForwardNftNodeTargets(tunnel *model.Tunnel, inNode, outNode *model.Node, exitMembers []dto.ForwardExitMemberDto) string {
	if tunnel == nil || tunnel.Type != tunnelTypeTunnelForward || !isNftablesMode(inNode) {
		return ""
	}
	nodes := map[int64]model.Node{}
	if outNode != nil {
		nodes[outNode.ID] = *outNode
	}
	for _, member := range exitMembers {
		if _, exists := nodes[member.OutNodeID]; exists {
			continue
		}
		var node model.Node
		if err := model.DB.First(&node, member.OutNodeID).Error; err == nil {
			nodes[node.ID] = node
		}
	}
	for _, node := range nodes {
		if _, err := parseTargetHostPort(node.ServerIP, 1, true); err != nil {
			return "出口节点地址格式错误"
		}
	}
	return ""
}

// GetAllForwards 转发列表（管理员全部、普通用户自己的）

func validateForwardExists(forwardID, userID int64, roleID int) *model.Forward {
	var forward model.Forward
	if err := model.DB.First(&forward, forwardID).Error; err != nil {
		return nil
	}
	if roleID != adminRoleID && forward.UserID != userID {
		return nil
	}
	return &forward
}

// getRequiredNodes 获取入口/出口节点
func getRequiredNodes(tunnel *model.Tunnel) (*model.Node, *model.Node, string) {
	var inNode model.Node
	if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil {
		return nil, nil, "入口节点不存在"
	}
	if tunnel.Type == tunnelTypeTunnelForward {
		var outNode model.Node
		if err := model.DB.First(&outNode, tunnel.OutNodeID).Error; err != nil {
			return nil, nil, "出口节点不存在"
		}
		return &inNode, &outNode, ""
	}
	return &inNode, nil, ""
}

// checkUserPermissions 普通用户权限/配额检查
func checkUserPermissions(cu CurrentUser, tunnel *model.Tunnel, excludeForwardID *int64) permissionResult {
	return checkUserPermissionsDB(model.DB, cu, tunnel, excludeForwardID)
}

func checkUserPermissionsDB(db *gorm.DB, cu CurrentUser, tunnel *model.Tunnel, excludeForwardID *int64) permissionResult {
	if cu.RoleID == adminRoleID {
		return permissionResult{}
	}

	var userInfo model.User
	if err := db.First(&userInfo, cu.UserID).Error; err != nil {
		return permissionResult{errMsg: "用户不存在"}
	}
	if !isActiveUserStatus(userInfo.Status) {
		return permissionResult{errMsg: "用户已到期或被禁用"}
	}
	if userInfo.ExpTime != nil && *userInfo.ExpTime <= time.Now().UnixMilli() {
		return permissionResult{errMsg: "当前账号已到期"}
	}

	var ut model.UserTunnel
	if err := db.Where("user_id = ? AND tunnel_id = ?", cu.UserID, tunnel.ID).First(&ut).Error; err != nil {
		return permissionResult{errMsg: "你没有该隧道权限"}
	}
	if ut.Status != 1 {
		return permissionResult{errMsg: "隧道被禁用"}
	}
	if ut.ExpTime != nil && *ut.ExpTime <= time.Now().UnixMilli() {
		return permissionResult{errMsg: "该隧道权限已到期"}
	}
	if totalFlowBytes(userInfo.InFlow, userInfo.OutFlow) >= flowLimitBytes(userInfo.Flow) {
		return permissionResult{errMsg: "用户总流量已用完"}
	}
	if totalFlowBytes(ut.InFlow, ut.OutFlow) >= flowLimitBytes(ut.Flow) {
		return permissionResult{errMsg: "该隧道流量已用完"}
	}
	if msg := checkForwardQuotaDB(db, cu.UserID, tunnel.ID, &ut, &userInfo, excludeForwardID); msg != "" {
		return permissionResult{errMsg: msg}
	}
	return permissionResult{limiter: ut.SpeedID, userTunnel: &ut}
}

// checkForwardQuota 转发数量配额检查
func checkForwardQuota(userID, tunnelID int64, ut *model.UserTunnel, userInfo *model.User, excludeForwardID *int64) string {
	return checkForwardQuotaDB(model.DB, userID, tunnelID, ut, userInfo, excludeForwardID)
}

func checkForwardQuotaDB(db *gorm.DB, userID, tunnelID int64, ut *model.UserTunnel, userInfo *model.User, excludeForwardID *int64) string {
	var userForwardCount int64
	userForwardQuery := db.Model(&model.Forward{}).Where("user_id = ?", userID)
	if excludeForwardID != nil {
		userForwardQuery = userForwardQuery.Where("id <> ?", *excludeForwardID)
	}
	if err := userForwardQuery.Count(&userForwardCount).Error; err != nil {
		return "权限配额校验失败"
	}
	if userForwardCount >= int64(userInfo.Num) {
		return "用户总转发数量已达上限，当前限制：" + intStr(userInfo.Num) + "个"
	}

	q := db.Model(&model.Forward{}).Where("user_id = ? AND tunnel_id = ?", userID, tunnelID)
	if excludeForwardID != nil {
		q = q.Where("id <> ?", *excludeForwardID)
	}
	var tunnelForwardCount int64
	if err := q.Count(&tunnelForwardCount).Error; err != nil {
		return "权限配额校验失败"
	}
	if tunnelForwardCount >= int64(ut.Num) {
		return "该隧道转发数量已达上限，当前限制：" + intStr(ut.Num) + "个"
	}
	return ""
}

// checkUserFlowLimits 恢复服务前的流量/到期检查
func checkUserFlowLimits(userID int64, tunnel *model.Tunnel) string {
	var userInfo model.User
	if err := model.DB.First(&userInfo, userID).Error; err != nil {
		return "用户不存在"
	}
	if !isActiveUserStatus(userInfo.Status) {
		return "用户已到期或被禁用"
	}
	if userInfo.ExpTime != nil && *userInfo.ExpTime <= time.Now().UnixMilli() {
		return "当前账号已到期"
	}
	ut := getUserTunnel(userID, tunnel.ID)
	if ut == nil {
		return "你没有该隧道权限"
	}
	if ut.ExpTime != nil && *ut.ExpTime <= time.Now().UnixMilli() {
		return "该隧道权限已到期，无法恢复服务"
	}
	if totalFlowBytes(userInfo.InFlow, userInfo.OutFlow) >= flowLimitBytes(userInfo.Flow) {
		return "用户总流量已用完，无法恢复服务"
	}
	if totalFlowBytes(ut.InFlow, ut.OutFlow) >= flowLimitBytes(ut.Flow) {
		return "该隧道流量已用完，无法恢复服务"
	}
	return ""
}

// allocatePorts 端口分配；返回 (inPort, outPort, errMsg)
func allocatePorts(tunnel *model.Tunnel, specifiedInPort *int, excludeForwardID *int64) (int, *int, string) {
	var inPort int
	if specifiedInPort != nil {
		if !isInPortAvailable(tunnel, *specifiedInPort, excludeForwardID) {
			return 0, nil, "指定的入口端口 " + intStr(*specifiedInPort) + " 已被占用或不在允许范围内"
		}
		inPort = *specifiedInPort
	} else {
		p := allocatePortForNode(tunnel.InNodeID, excludeForwardID)
		if p == nil {
			return 0, nil, "隧道入口端口已满，无法分配新端口"
		}
		inPort = *p
	}

	var outPort *int
	if tunnel.Type == tunnelTypeTunnelForward {
		outPort = allocatePortForNode(tunnel.OutNodeID, excludeForwardID)
		if outPort == nil {
			return 0, nil, "隧道出口端口已满，无法分配新端口"
		}
	}
	return inPort, outPort, ""
}

// isInPortAvailable 指定端口是否可用
func isInPortAvailable(tunnel *model.Tunnel, port int, excludeForwardID *int64) bool {
	var inNode model.Node
	if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil {
		return false
	}
	if port < inNode.PortSta || port > inNode.PortEnd {
		return false
	}
	used, err := getAllUsedPortsOnNode(tunnel.InNodeID, excludeForwardID)
	if err != nil {
		// 查询失败时保守拒绝，避免分配到已用端口
		log.Printf("isInPortAvailable 查询已用端口失败(node=%d): %v", tunnel.InNodeID, err)
		return false
	}
	return !used[port]
}

// allocatePortForNode 自动分配节点上的可用端口
func allocatePortForNode(nodeID int64, excludeForwardID *int64) *int {
	return allocatePortForNodeWithDB(model.DB, nodeID, excludeForwardID)
}

func allocatePortForNodeWithDB(db *gorm.DB, nodeID int64, excludeForwardID *int64) *int {
	if db == nil {
		return nil
	}
	var node model.Node
	if err := db.First(&node, nodeID).Error; err != nil {
		return nil
	}
	used, err := getAllUsedPortsOnNodeWithDB(db, nodeID, excludeForwardID)
	if err != nil {
		// 查询失败时拒绝分配，避免把已在用的端口分配出去
		log.Printf("allocatePortForNode 查询已用端口失败(node=%d): %v", nodeID, err)
		return nil
	}
	var available []int
	for port := node.PortSta; port <= node.PortEnd; port++ {
		if !used[port] {
			available = append(available, port)
		}
	}
	if len(available) == 0 {
		return nil
	}
	index := randomIndex(len(available))
	p := available[index]
	return &p
}

func randomIndex(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := crand.Int(crand.Reader, big.NewInt(int64(n)))
	if err != nil {
		log.Printf("随机端口索引生成失败，使用时间回退: %v", err)
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}

// getAllUsedPortsOnNode 节点上已占用端口集合（入口+出口）。
// 返回 error 时调用方应拒绝分配，避免误判端口空闲。
func getAllUsedPortsOnNode(nodeID int64, excludeForwardID *int64) (map[int]bool, error) {
	return getAllUsedPortsOnNodeWithDB(model.DB, nodeID, excludeForwardID)
}

func getAllUsedPortsOnNodeWithDB(db *gorm.DB, nodeID int64, excludeForwardID *int64) (map[int]bool, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	used := map[int]bool{}

	// 入口端口
	var inTunnels []model.Tunnel
	if err := db.Where("in_node_id = ?", nodeID).Find(&inTunnels).Error; err != nil {
		return nil, fmt.Errorf("查询入口隧道失败: %w", err)
	}
	if len(inTunnels) > 0 {
		ids := tunnelIDs(inTunnels)
		q := db.Where("tunnel_id IN ?", ids)
		if excludeForwardID != nil {
			q = q.Where("id <> ?", *excludeForwardID)
		}
		var fs []model.Forward
		if err := q.Find(&fs).Error; err != nil {
			return nil, fmt.Errorf("查询入口转发失败: %w", err)
		}
		for _, f := range fs {
			if f.InPort != 0 {
				used[f.InPort] = true
			}
		}
	}

	// 出口端口
	var outTunnels []model.Tunnel
	if err := db.Where("out_node_id = ?", nodeID).Find(&outTunnels).Error; err != nil {
		return nil, fmt.Errorf("查询出口隧道失败: %w", err)
	}
	if len(outTunnels) > 0 {
		ids := tunnelIDs(outTunnels)
		q := db.Where("tunnel_id IN ?", ids)
		if excludeForwardID != nil {
			q = q.Where("id <> ?", *excludeForwardID)
		}
		var fs []model.Forward
		if err := q.Find(&fs).Error; err != nil {
			return nil, fmt.Errorf("查询出口转发失败: %w", err)
		}
		for _, f := range fs {
			if f.OutPort != nil {
				used[*f.OutPort] = true
			}
		}
	}

	// 新版隧道转发的候选出口端口。
	exitMemberQuery := db.Where("out_node_id = ?", nodeID)
	if excludeForwardID != nil {
		exitMemberQuery = exitMemberQuery.Where("forward_id <> ?", *excludeForwardID)
	}
	var exitMembers []model.ForwardExitMember
	if err := exitMemberQuery.Find(&exitMembers).Error; err != nil {
		return nil, fmt.Errorf("查询出口成员端口失败: %w", err)
	}
	for _, member := range exitMembers {
		if member.OutPort != 0 {
			used[member.OutPort] = true
		}
	}
	return used, nil
}

func tunnelIDs(tunnels []model.Tunnel) []int64 {
	ids := make([]int64, 0, len(tunnels))
	for _, t := range tunnels {
		ids = append(ids, t.ID)
	}
	return ids
}

// ===== gost 服务编排 =====

func interfaceNameOf(f *model.Forward) string {
	if f.InterfaceName != nil {
		return *f.InterfaceName
	}
	return ""
}

func tunnelInterfaceName(t *model.Tunnel) string {
	if t.InterfaceName != nil {
		return *t.InterfaceName
	}
	return ""
}

func tunnelProtocol(t *model.Tunnel) string {
	if t.Protocol != nil {
		return *t.Protocol
	}
	return ""
}
