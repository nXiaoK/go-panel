package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

const (
	exitModeSingle  = "single"
	exitModeManual  = "manual"
	exitModeBalance = "balance"

	exitStrategyFIFO  = "fifo"
	exitStrategyRound = "round"
	exitStrategyRand  = "rand"
	exitStrategyHash  = "hash"
)

func normalizeExitMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case exitModeManual:
		return exitModeManual
	case exitModeBalance:
		return exitModeBalance
	default:
		return exitModeSingle
	}
}

func normalizeExitStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "round", "rr":
		return exitStrategyRound
	case "random", "rand":
		return exitStrategyRand
	case "hash":
		return exitStrategyHash
	default:
		return exitStrategyFIFO
	}
}

func normalizeForwardExitMembers(tunnel *model.Tunnel, mode string, members []dto.ForwardExitMemberDto) ([]dto.ForwardExitMemberDto, string) {
	if tunnel == nil || tunnel.Type != tunnelTypeTunnelForward {
		return nil, ""
	}

	mode = normalizeExitMode(mode)
	if len(members) == 0 {
		if tunnel.OutNodeID == 0 {
			return nil, "请选择出口节点"
		}
		return []dto.ForwardExitMemberDto{{OutNodeID: tunnel.OutNodeID, Active: true, Weight: 1}}, ""
	}

	seen := map[int64]bool{}
	normalized := make([]dto.ForwardExitMemberDto, 0, len(members))
	activeCount := 0
	for _, member := range members {
		if member.OutNodeID <= 0 || seen[member.OutNodeID] {
			continue
		}
		seen[member.OutNodeID] = true
		if member.Weight <= 0 {
			member.Weight = 1
		}
		if mode == exitModeBalance {
			member.Active = true
		}
		if member.Active {
			activeCount++
		}
		normalized = append(normalized, member)
	}
	if len(normalized) == 0 {
		return nil, "请选择出口节点"
	}

	switch mode {
	case exitModeManual:
		if activeCount == 0 {
			return nil, "手动负载需要选择当前出口节点"
		}
		if activeCount > 1 {
			return nil, "手动负载只能选择一个当前出口节点"
		}
	case exitModeBalance:
		if len(normalized) < 2 {
			return nil, "自动负载均衡至少需要两个出口节点"
		}
	default:
		activeIndex := 0
		for i, member := range normalized {
			if member.Active {
				activeIndex = i
				break
			}
		}
		member := normalized[activeIndex]
		member.Active = true
		normalized = []dto.ForwardExitMemberDto{member}
	}

	return normalized, ""
}

func saveForwardExitMembers(forward *model.Forward, tunnel *model.Tunnel, members []dto.ForwardExitMemberDto, excludeForwardID *int64) ([]model.ForwardExitMember, string) {
	var rows []model.ForwardExitMember
	var msg string
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		rows, msg = saveForwardExitMembersWithTx(tx, forward, tunnel, members, excludeForwardID)
		if msg != "" {
			return errors.New(msg)
		}
		return nil
	}); err != nil {
		if msg != "" {
			return nil, msg
		}
		return nil, "保存出口成员失败"
	}
	return rows, ""
}

func saveForwardExitMembersWithTx(tx *gorm.DB, forward *model.Forward, tunnel *model.Tunnel, members []dto.ForwardExitMemberDto, excludeForwardID *int64) ([]model.ForwardExitMember, string) {
	if tx == nil {
		return nil, "保存出口成员失败"
	}
	if tunnel == nil || tunnel.Type != tunnelTypeTunnelForward {
		if forward.ID != 0 {
			if err := tx.Where("forward_id = ?", forward.ID).Delete(&model.ForwardExitMember{}).Error; err != nil {
				return nil, "清理出口成员失败"
			}
		}
		forward.ExitMode = exitModeSingle
		forward.ExitStrategy = exitStrategyFIFO
		forward.OutPort = nil
		return nil, ""
	}

	mode := normalizeExitMode(forward.ExitMode)
	strategy := normalizeExitStrategy(forward.ExitStrategy)
	forward.ExitMode = mode
	forward.ExitStrategy = strategy

	var oldRows []model.ForwardExitMember
	if forward.ID != 0 {
		if err := tx.Where("forward_id = ?", forward.ID).Find(&oldRows).Error; err != nil {
			return nil, "读取旧出口成员失败"
		}
	}
	oldPortByNode := make(map[int64]int, len(oldRows))
	for _, row := range oldRows {
		if row.OutPort > 0 {
			oldPortByNode[row.OutNodeID] = row.OutPort
		}
	}

	now := time.Now().UnixMilli()
	rows := make([]model.ForwardExitMember, 0, len(members))
	usedPortsInRequest := map[int64]map[int]bool{}
	for _, member := range members {
		var node model.Node
		if err := tx.First(&node, member.OutNodeID).Error; err != nil {
			return nil, "出口节点不存在"
		}

		outPort := oldPortByNode[member.OutNodeID]
		if outPort == 0 && forward.OutPort != nil && member.OutNodeID == tunnel.OutNodeID {
			outPort = *forward.OutPort
		}
		if outPort == 0 {
			port := allocatePortForNodeWithDB(tx, member.OutNodeID, excludeForwardID)
			if port == nil {
				return nil, fmt.Sprintf("出口节点 %s 端口已满，无法分配新端口", node.Name)
			}
			outPort = *port
		}
		if usedPortsInRequest[member.OutNodeID] == nil {
			usedPortsInRequest[member.OutNodeID] = map[int]bool{}
		}
		if usedPortsInRequest[member.OutNodeID][outPort] {
			return nil, fmt.Sprintf("出口节点 %s 分配到重复端口 %d", node.Name, outPort)
		}
		usedPortsInRequest[member.OutNodeID][outPort] = true

		active := 0
		if member.Active {
			active = 1
		}
		rows = append(rows, model.ForwardExitMember{
			ForwardID:   forward.ID,
			OutNodeID:   member.OutNodeID,
			OutPort:     outPort,
			Weight:      member.Weight,
			Status:      1,
			Active:      active,
			CreatedTime: now,
			UpdatedTime: now,
		})
	}

	activePort := 0
	for _, row := range rows {
		if row.Active == 1 {
			activePort = row.OutPort
			break
		}
	}
	if activePort == 0 && len(rows) > 0 {
		activePort = rows[0].OutPort
		rows[0].Active = 1
	}
	if activePort != 0 {
		forward.OutPort = &activePort
	}

	if forward.ID != 0 {
		if err := tx.Where("forward_id = ?", forward.ID).Delete(&model.ForwardExitMember{}).Error; err != nil {
			return nil, "保存出口成员失败"
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return nil, "保存出口成员失败"
			}
		}
		if err := tx.Model(&model.Forward{}).Where("id = ?", forward.ID).Updates(map[string]interface{}{
			"out_port":      forward.OutPort,
			"exit_mode":     forward.ExitMode,
			"exit_strategy": forward.ExitStrategy,
			"updated_time":  time.Now().UnixMilli(),
		}).Error; err != nil {
			return nil, "保存出口配置失败"
		}
	}

	return rows, ""
}

func loadPersistedForwardExitMembers(forwardID int64) []model.ForwardExitMember {
	members, _ := loadPersistedForwardExitMembersDB(model.DB, forwardID)
	return members
}

func loadPersistedForwardExitMembersDB(db *gorm.DB, forwardID int64) ([]model.ForwardExitMember, error) {
	var members []model.ForwardExitMember
	if err := db.Where("forward_id = ?", forwardID).Order("id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return members, nil
}

func loadForwardExitMembers(forward *model.Forward, tunnel *model.Tunnel) []model.ForwardExitMember {
	if forward == nil || tunnel == nil || tunnel.Type != tunnelTypeTunnelForward {
		return nil
	}
	members := loadPersistedForwardExitMembers(forward.ID)
	if len(members) > 0 {
		sort.SliceStable(members, func(i, j int) bool {
			if members[i].Active != members[j].Active {
				return members[i].Active > members[j].Active
			}
			return members[i].ID < members[j].ID
		})
		return members
	}
	if forward.OutPort == nil || *forward.OutPort <= 0 || tunnel.OutNodeID == 0 {
		return nil
	}
	return []model.ForwardExitMember{{
		ForwardID:   forward.ID,
		OutNodeID:   tunnel.OutNodeID,
		OutPort:     *forward.OutPort,
		Weight:      1,
		Status:      1,
		Active:      1,
		CreatedTime: forward.CreatedTime,
		UpdatedTime: forward.UpdatedTime,
	}}
}

func deployForwardExitMembers(forward *model.Forward, tunnel *model.Tunnel) []model.ForwardExitMember {
	members, _ := deployForwardExitMembersDB(model.DB, forward, tunnel)
	return members
}

func deployForwardExitMembersDB(db *gorm.DB, forward *model.Forward, tunnel *model.Tunnel) ([]model.ForwardExitMember, error) {
	if forward == nil || tunnel == nil || tunnel.Type != tunnelTypeTunnelForward {
		return nil, nil
	}
	members, err := loadPersistedForwardExitMembersDB(db, forward.ID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 && forward.OutPort != nil && *forward.OutPort > 0 && tunnel.OutNodeID != 0 {
		members = []model.ForwardExitMember{{
			ForwardID: forward.ID, OutNodeID: tunnel.OutNodeID, OutPort: *forward.OutPort,
			Weight: 1, Status: 1, Active: 1, CreatedTime: forward.CreatedTime, UpdatedTime: forward.UpdatedTime,
		}}
	}
	if len(members) == 0 {
		return nil, nil
	}
	if normalizeExitMode(forward.ExitMode) == exitModeBalance {
		out := make([]model.ForwardExitMember, 0, len(members))
		for _, member := range members {
			if member.Status == 1 {
				out = append(out, member)
			}
		}
		return out, nil
	}
	for _, member := range members {
		if member.Status == 1 && member.Active == 1 {
			return []model.ForwardExitMember{member}, nil
		}
	}
	for _, member := range members {
		if member.Status == 1 {
			return []model.ForwardExitMember{member}, nil
		}
	}
	return nil, nil
}

func activeForwardExitMember(forward *model.Forward, tunnel *model.Tunnel) *model.ForwardExitMember {
	members := deployForwardExitMembers(forward, tunnel)
	if len(members) == 0 {
		return nil
	}
	return &members[0]
}

func nftForwardExitMember(forward *model.Forward, tunnel *model.Tunnel) *model.ForwardExitMember {
	member, _ := nftForwardExitMemberDB(model.DB, forward, tunnel)
	return member
}

func nftForwardExitMemberDB(db *gorm.DB, forward *model.Forward, tunnel *model.Tunnel) (*model.ForwardExitMember, error) {
	if normalizeExitMode(forward.ExitMode) == exitModeBalance {
		return nil, nil
	}
	members, err := deployForwardExitMembersDB(db, forward, tunnel)
	if err != nil || len(members) == 0 {
		return nil, err
	}
	return &members[0], nil
}

func forwardExitNodeMap(members []model.ForwardExitMember) map[int64]model.Node {
	nodeIDs := make([]int64, 0, len(members))
	seen := map[int64]bool{}
	for _, member := range members {
		if member.OutNodeID > 0 && !seen[member.OutNodeID] {
			seen[member.OutNodeID] = true
			nodeIDs = append(nodeIDs, member.OutNodeID)
		}
	}
	if len(nodeIDs) == 0 {
		return map[int64]model.Node{}
	}
	var nodes []model.Node
	model.DB.Where("id IN ?", nodeIDs).Find(&nodes)
	out := make(map[int64]model.Node, len(nodes))
	for _, node := range nodes {
		out[node.ID] = node
	}
	return out
}

func forwardExitChainTargets(members []model.ForwardExitMember, nodes map[int64]model.Node) string {
	targets := make([]string, 0, len(members))
	for _, member := range members {
		node, ok := nodes[member.OutNodeID]
		if !ok || strings.TrimSpace(node.ServerIP) == "" || member.OutPort <= 0 {
			continue
		}
		target, err := parseTargetHostPort(node.ServerIP, member.OutPort, false)
		if err != nil {
			continue
		}
		targets = append(targets, target.Normalized)
	}
	return strings.Join(targets, ",")
}

func forwardExitMemberViews(forwardIDs []int64) map[int64][]dto.ForwardExitMemberView {
	out := map[int64][]dto.ForwardExitMemberView{}
	if len(forwardIDs) == 0 {
		return out
	}
	var members []model.ForwardExitMember
	if err := model.DB.Where("forward_id IN ?", forwardIDs).Order("id ASC").Find(&members).Error; err != nil {
		return out
	}
	nodes := forwardExitNodeMap(members)
	for _, member := range members {
		var name *string
		var ip *string
		if node, ok := nodes[member.OutNodeID]; ok {
			n := node.Name
			serverIP := node.ServerIP
			name = &n
			ip = &serverIP
		}
		out[member.ForwardID] = append(out[member.ForwardID], dto.ForwardExitMemberView{
			ID:          member.ID,
			ForwardID:   member.ForwardID,
			OutNodeID:   member.OutNodeID,
			OutNodeName: name,
			OutNodeIP:   ip,
			OutPort:     member.OutPort,
			Weight:      member.Weight,
			Status:      member.Status,
			Active:      member.Active == 1,
		})
	}
	return out
}

func forwardExitMemberDtosFromExisting(forward *model.Forward, tunnel *model.Tunnel) []dto.ForwardExitMemberDto {
	members := loadForwardExitMembers(forward, tunnel)
	out := make([]dto.ForwardExitMemberDto, 0, len(members))
	for _, member := range members {
		out = append(out, dto.ForwardExitMemberDto{
			OutNodeID: member.OutNodeID,
			Active:    member.Active == 1,
			Weight:    member.Weight,
		})
	}
	return out
}
