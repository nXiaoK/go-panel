package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// ExtraRule 多余的规则
type ExtraRule struct {
	InPort              int    `json:"inPort"`
	TargetHost          string `json:"targetHost"`
	OutPort             int    `json:"outPort"`
	Protocol            string `json:"protocol"`
	RuleType            string `json:"ruleType"` // port / tunnel
	RawRule             string `json:"rawRule"`
	SuggestedTunnelID   int64  `json:"suggestedTunnelId"`
	SuggestedTunnelName string `json:"suggestedTunnelName"`
	SuggestedName       string `json:"suggestedName"`
}

// ExtraRulesForNode 某个节点的多余规则
type ExtraRulesForNode struct {
	NodeID     int64        `json:"nodeId"`
	NodeName   string       `json:"nodeName"`
	NodeIP     string       `json:"nodeIp"`
	ExtraRules []*ExtraRule `json:"extraRules"`
	Total      int          `json:"total"`
}

// DetectExtraRulesResult 识别结果
type DetectExtraRulesResult struct {
	Nodes      []*ExtraRulesForNode `json:"nodes"`
	TotalNodes int                  `json:"totalNodes"`
	TotalRules int                  `json:"totalRules"`
}

// DetectExtraRulesAfterRestore 恢复后检测所有节点的多余规则
func DetectExtraRulesAfterRestore() result.R {
	// 获取所有 nftables 在线节点
	var nodes []model.Node
	model.DB.Where("forward_mode = ? AND status = ?", "nftables", nodeStatusOnline).Find(&nodes)

	if len(nodes) == 0 {
		return result.Ok(&DetectExtraRulesResult{
			Nodes:      []*ExtraRulesForNode{},
			TotalNodes: 0,
			TotalRules: 0,
		})
	}

	nodesWithExtra := []*ExtraRulesForNode{}
	totalRules := 0

	for _, node := range nodes {
		extraRules := detectExtraRulesForNode(&node)
		if len(extraRules) > 0 {
			nodesWithExtra = append(nodesWithExtra, &ExtraRulesForNode{
				NodeID:     node.ID,
				NodeName:   node.Name,
				NodeIP:     node.ServerIP,
				ExtraRules: extraRules,
				Total:      len(extraRules),
			})
			totalRules += len(extraRules)
		}
	}

	return result.Ok(&DetectExtraRulesResult{
		Nodes:      nodesWithExtra,
		TotalNodes: len(nodesWithExtra),
		TotalRules: totalRules,
	})
}

// detectExtraRulesForNode 检测单个节点的多余规则
func detectExtraRulesForNode(node *model.Node) []*ExtraRule {
	// 1. 读取节点上的 NFT 规则
	rulesResult := ws.SendMsg(node.ID, nil, "ListNftRules")
	if !gost.IsOK(rulesResult) {
		return nil
	}

	var ruleData struct {
		Rules []string `json:"rules"`
	}
	if err := gost.UnmarshalData(rulesResult.Data, &ruleData); err != nil {
		return nil
	}

	// 2. 解析规则
	parsedRules := gost.ParseNftRules(ruleData.Rules)

	// 3. 获取数据库中该节点的转发
	existingForwards := loadExistingForwardPorts(node.ID)

	// 4. 找出多余的规则
	extraRules := []*ExtraRule{}
	seen := make(map[string]bool)

	for _, rule := range parsedRules {
		// 跳过已在数据库中的规则
		if rule.ForwardID != 0 {
			continue
		}

		// 去重
		key := fmt.Sprintf("%s:%d", rule.Protocol, rule.InPort)
		if seen[key] {
			continue
		}
		seen[key] = true

		// 检查是否已存在
		if existingForwards[key] {
			continue
		}

		// 推断类型和隧道
		ruleType := "port"
		if isNodeIP(rule.TargetHost) {
			ruleType = "tunnel"
		}

		tunnelID, tunnelName := inferTunnelForNode(node.ID, ruleType)

		extraRules = append(extraRules, &ExtraRule{
			InPort:              rule.InPort,
			TargetHost:          rule.TargetHost,
			OutPort:             rule.OutPort,
			Protocol:            rule.Protocol,
			RuleType:            ruleType,
			RawRule:             rule.RawRule,
			SuggestedTunnelID:   tunnelID,
			SuggestedTunnelName: tunnelName,
			SuggestedName:       fmt.Sprintf("NFT识别-%d", rule.InPort),
		})
	}

	return extraRules
}

// loadExistingForwards 加载节点的现有转发
func loadExistingForwardPorts(nodeID int64) map[string]bool {
	var tunnels []model.Tunnel
	model.DB.Where("in_node_id = ?", nodeID).Find(&tunnels)

	if len(tunnels) == 0 {
		return make(map[string]bool)
	}

	tunnelIDs := make([]int64, len(tunnels))
	for i, t := range tunnels {
		tunnelIDs[i] = t.ID
	}

	var forwards []model.Forward
	model.DB.Where("tunnel_id IN ?", tunnelIDs).Find(&forwards)

	// 构建索引：protocol:inPort
	forwardMap := make(map[string]bool)
	for _, f := range forwards {
		key := fmt.Sprintf("tcp:%d", f.InPort)
		forwardMap[key] = true
		key = fmt.Sprintf("udp:%d", f.InPort)
		forwardMap[key] = true
	}

	return forwardMap
}

// isNodeIP 判断是否是节点 IP
func isNodeIP(ip string) bool {
	var count int64
	model.DB.Model(&model.Node{}).Where("server_ip = ? OR ip = ?", ip, ip).Count(&count)
	return count > 0
}

// inferTunnelForNode 推断节点的隧道
func inferTunnelForNode(nodeID int64, ruleType string) (int64, string) {
	var tunnels []model.Tunnel

	if ruleType == "tunnel" {
		// 隧道转发
		model.DB.Where("in_node_id = ? AND type = ? AND status = 1",
			nodeID, tunnelTypeTunnelForward).Find(&tunnels)
	} else {
		// 端口转发
		model.DB.Where("in_node_id = ? AND type = ? AND status = 1",
			nodeID, tunnelTypePortForward).Find(&tunnels)
	}

	if len(tunnels) == 0 {
		return 0, ""
	}

	// 返回第一个隧道
	return tunnels[0].ID, tunnels[0].Name
}

// HandleRuleAction 处理规则的动作
type HandleRuleAction struct {
	NodeID     int64  `json:"nodeId"`
	InPort     int    `json:"inPort"`
	Action     string `json:"action"` // keep / delete
	Name       string `json:"name"`
	TunnelID   int64  `json:"tunnelId"`
	TargetHost string `json:"targetHost"`
	OutPort    int    `json:"outPort"`
	Protocol   string `json:"protocol"`
	RawRule    string `json:"rawRule"`
}

// HandleExtraRulesRequest 处理多余规则的请求
type HandleExtraRulesRequest struct {
	Rules []HandleRuleAction `json:"rules"`
}

// HandleExtraRulesResult 处理结果
type HandleExtraRulesResult struct {
	Kept    int                      `json:"kept"`
	Deleted int                      `json:"deleted"`
	Details []HandleRuleActionResult `json:"details"`
}

// HandleRuleActionResult 单条规则的处理结果
type HandleRuleActionResult struct {
	NodeID    int64  `json:"nodeId"`
	InPort    int    `json:"inPort"`
	Action    string `json:"action"`
	Success   bool   `json:"success"`
	ForwardID int64  `json:"forwardId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HandleExtraRules 处理多余的规则
func HandleExtraRules(cu CurrentUser, req *HandleExtraRulesRequest) result.R {
	if len(req.Rules) == 0 {
		return result.Err("没有要处理的规则")
	}

	kept := 0
	deleted := 0
	details := []HandleRuleActionResult{}
	nodeIDs := make(map[int64]bool)

	for _, rule := range req.Rules {
		nodeIDs[rule.NodeID] = true

		if rule.Action == "keep" {
			if rule.TunnelID == 0 {
				details = append(details, HandleRuleActionResult{
					NodeID:  rule.NodeID,
					InPort:  rule.InPort,
					Action:  "keep",
					Success: false,
					Error:   "缺少隧道信息",
				})
				continue
			}
			var tunnel model.Tunnel
			if err := model.DB.First(&tunnel, rule.TunnelID).Error; err != nil {
				details = append(details, HandleRuleActionResult{
					NodeID:  rule.NodeID,
					InPort:  rule.InPort,
					Action:  "keep",
					Success: false,
					Error:   "隧道不存在",
				})
				continue
			}
			now := time.Now().UnixMilli()
			remoteAddr := joinHostPort(rule.TargetHost, rule.OutPort)
			// 保留：添加到数据库
			forward := &model.Forward{
				Name:        rule.Name,
				TunnelID:    rule.TunnelID,
				InPort:      rule.InPort,
				RemoteAddr:  remoteAddr,
				Strategy:    "fifo",
				Status:      1,
				UserID:      cu.UserID,
				UserName:    cu.UserName,
				CreatedTime: now,
				UpdatedTime: now,
				Inx:         nextForwardIndex(),
			}

			if tunnel.Type == tunnelTypeTunnelForward && rule.OutPort > 0 {
				outPort := rule.OutPort
				forward.OutPort = &outPort
			}

			if err := model.DB.Create(forward).Error; err != nil {
				details = append(details, HandleRuleActionResult{
					NodeID:  rule.NodeID,
					InPort:  rule.InPort,
					Action:  "keep",
					Success: false,
					Error:   err.Error(),
				})
			} else {
				kept++
				details = append(details, HandleRuleActionResult{
					NodeID:    rule.NodeID,
					InPort:    rule.InPort,
					Action:    "keep",
					Success:   true,
					ForwardID: forward.ID,
				})
			}
		} else if rule.Action == "delete" {
			if err := deleteExtraRuleFromNode(rule); err != nil {
				details = append(details, HandleRuleActionResult{
					NodeID:  rule.NodeID,
					InPort:  rule.InPort,
					Action:  "delete",
					Success: false,
					Error:   err.Error(),
				})
			} else {
				deleted++
				details = append(details, HandleRuleActionResult{
					NodeID:  rule.NodeID,
					InPort:  rule.InPort,
					Action:  "delete",
					Success: true,
				})
			}
		}
	}

	// 刷新所有涉及的节点规则
	for nodeID := range nodeIDs {
		RefreshNodeForwardRules(nodeID)
	}

	return result.Ok(&HandleExtraRulesResult{
		Kept:    kept,
		Deleted: deleted,
		Details: details,
	})
}

func deleteExtraRuleFromNode(rule HandleRuleAction) error {
	if strings.TrimSpace(rule.RawRule) == "" {
		return fmt.Errorf("缺少原始规则，无法删除")
	}
	view, err := findRuleHandleByRaw(rule.NodeID, rule.RawRule)
	if err != nil {
		return err
	}
	return deleteNftRuleHandles(rule.NodeID, view.Table, view.Handles)
}

func findRuleHandleByRaw(nodeID int64, rawRule string) (nftRuleHandleView, error) {
	view, err := listNodeNftRules(nodeID)
	if err != nil {
		return nftRuleHandleView{}, err
	}
	target := normalizeNftRuleForCompare(rawRule)
	for _, candidate := range view.Rules {
		if normalizeNftRuleForCompare(candidate) != target {
			continue
		}
		chain, handle, ok := parseNftRuleHandle(candidate)
		if !ok {
			return nftRuleHandleView{}, fmt.Errorf("规则缺少 handle，无法删除")
		}
		return nftRuleHandleView{Table: view.Table, Handles: []RuleHandle{{Chain: chain, Handle: handle}}}, nil
	}
	return nftRuleHandleView{}, fmt.Errorf("未找到匹配的节点规则")
}

func normalizeNftRuleForCompare(rule string) string {
	if idx := strings.LastIndex(rule, "# handle"); idx >= 0 {
		rule = rule[:idx]
	}
	return strings.Join(strings.Fields(rule), " ")
}

func parseNftRuleHandle(rule string) (string, int, bool) {
	fields := strings.Fields(rule)
	chain := ""
	for i := 0; i < len(fields); i++ {
		if fields[i] == "add" && i+5 < len(fields) && fields[i+1] == "rule" && fields[i+2] == "inet" && fields[i+3] == "flux_panel" {
			chain = fields[i+4]
		}
		if fields[i] == "handle" && i+1 < len(fields) {
			handle, err := strconv.Atoi(fields[i+1])
			return chain, handle, err == nil && chain != ""
		}
	}
	return "", 0, false
}
