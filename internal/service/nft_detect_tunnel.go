package service

import (
	"fmt"
	"strings"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// DetectedTunnelRule 识别到的隧道转发规则
type DetectedTunnelRule struct {
	InPort      int    `json:"inPort"`      // 入口端口
	RelayPort   int    `json:"relayPort"`   // 中转端口（出口节点端口）
	TargetHost  string `json:"targetHost"`  // 最终目标地址
	TargetPort  int    `json:"targetPort"`  // 最终目标端口
	Protocol    string `json:"protocol"`    // tcp/udp
	TunnelID    int64  `json:"tunnelId"`    // 推断的隧道ID
	TunnelName  string `json:"tunnelName"`  // 隧道名称
	InNodeName  string `json:"inNodeName"`  // 入口节点名称
	OutNodeName string `json:"outNodeName"` // 出口节点名称
	InRawRule   string `json:"inRawRule"`   // 入口节点原始规则
	OutRawRule  string `json:"outRawRule"`  // 出口节点原始规则
	Suggested   bool   `json:"suggested"`   // 是否推荐补全
}

// DetectTunnelRulesResult 隧道转发识别结果
type DetectTunnelRulesResult struct {
	Detected    []*DetectedTunnelRule `json:"detected"`
	Total       int                   `json:"total"`
	InNodeName  string                `json:"inNodeName"`
	OutNodeName string                `json:"outNodeName"`
	InNodeID    int64                 `json:"inNodeId"`
	OutNodeID   int64                 `json:"outNodeId"`
}

// DetectTunnelForwardRules 识别隧道转发规则（跨两个节点）
func DetectTunnelForwardRules(inNodeID, outNodeID int64) result.R {
	// 检查入口节点
	var inNode model.Node
	if err := model.DB.First(&inNode, inNodeID).Error; err != nil {
		return result.Err("入口节点不存在")
	}

	// 检查出口节点
	var outNode model.Node
	if err := model.DB.First(&outNode, outNodeID).Error; err != nil {
		return result.Err("出口节点不存在")
	}

	if inNode.Status != nodeStatusOnline {
		return result.Err("入口节点离线")
	}

	if outNode.Status != nodeStatusOnline {
		return result.Err("出口节点离线")
	}

	if !isNftablesMode(&inNode) {
		return result.Err("入口节点不是 nftables 模式")
	}

	if !isNftablesMode(&outNode) {
		return result.Err("出口节点不是 nftables 模式")
	}

	// 读取入口节点规则
	inRulesResult := ws.SendMsg(inNodeID, nil, "ListNftRules")
	if !gost.IsOK(inRulesResult) {
		return result.Err("读取入口节点规则失败: " + inRulesResult.Msg)
	}

	var inRuleData struct {
		Rules []string `json:"rules"`
	}
	if err := gost.UnmarshalData(inRulesResult.Data, &inRuleData); err != nil {
		return result.Err("解析入口节点规则失败")
	}

	// 读取出口节点规则
	outRulesResult := ws.SendMsg(outNodeID, nil, "ListNftRules")
	if !gost.IsOK(outRulesResult) {
		return result.Err("读取出口节点规则失败: " + outRulesResult.Msg)
	}

	var outRuleData struct {
		Rules []string `json:"rules"`
	}
	if err := gost.UnmarshalData(outRulesResult.Data, &outRuleData); err != nil {
		return result.Err("解析出口节点规则失败")
	}

	// 解析规则
	inParsedRules := gost.ParseNftRules(inRuleData.Rules)
	outParsedRules := gost.ParseNftRules(outRuleData.Rules)

	// 查询数据库中已存在的隧道转发
	existingTunnels := loadExistingTunnelForwards(inNodeID, outNodeID)

	// 关联入口和出口规则，识别隧道转发
	detected := findMissingTunnelRules(inParsedRules, outParsedRules, existingTunnels, &inNode, &outNode)

	return result.Ok(&DetectTunnelRulesResult{
		Detected:    detected,
		Total:       len(detected),
		InNodeName:  inNode.Name,
		OutNodeName: outNode.Name,
		InNodeID:    inNodeID,
		OutNodeID:   outNodeID,
	})
}

// loadExistingTunnelForwards 加载已存在的隧道转发
func loadExistingTunnelForwards(inNodeID, outNodeID int64) map[string]bool {
	var tunnels []model.Tunnel
	model.DB.Where("in_node_id = ? AND out_node_id = ? AND type = ?", inNodeID, outNodeID, tunnelTypeTunnelForward).Find(&tunnels)

	if len(tunnels) == 0 {
		return make(map[string]bool)
	}

	tunnelIDs := make([]int64, len(tunnels))
	tunnelMap := make(map[int64]*model.Tunnel, len(tunnels))
	for i, t := range tunnels {
		tunnelIDs[i] = t.ID
		tunnelMap[t.ID] = &tunnels[i]
	}

	var forwards []model.Forward
	model.DB.Where("tunnel_id IN ?", tunnelIDs).Find(&forwards)

	// 构建索引：key = inPort:relayPort:targetHost:targetPort
	forwardMap := make(map[string]bool)
	for _, f := range forwards {
		if f.OutPort == nil {
			continue
		}
		tunnel, ok := tunnelMap[f.TunnelID]
		if !ok {
			continue
		}
		for _, addr := range splitRemoteAddresses(effectiveForwardRemoteAddr(&f)) {
			targetHost := extractHost(addr)
			targetPort := extractPort(addr)
			if targetHost == "" || targetPort <= 0 {
				continue
			}
			for _, protocol := range resolveProtocols(tunnel) {
				key := buildTunnelRuleKey(protocol, f.InPort, *f.OutPort, targetHost, targetPort)
				forwardMap[key] = true
			}
		}
	}

	return forwardMap
}

// findMissingTunnelRules 找出缺失的隧道转发规则
func findMissingTunnelRules(
	inRules []*gost.ParsedNftRule,
	outRules []*gost.ParsedNftRule,
	existing map[string]bool,
	inNode *model.Node,
	outNode *model.Node,
) []*DetectedTunnelRule {
	var detected []*DetectedTunnelRule
	seen := make(map[string]bool) // 去重

	// 构建出口规则索引：protocol:relayPort -> rules
	outRuleMap := make(map[string][]*gost.ParsedNftRule)
	for _, rule := range outRules {
		key := buildProtocolPortKey(rule.Protocol, rule.InPort)
		outRuleMap[key] = append(outRuleMap[key], rule)
	}

	// 遍历入口规则
	for _, inRule := range inRules {
		// 查找出口节点的对应规则
		relayPort := inRule.OutPort
		outRulesForPort := outRuleMap[buildProtocolPortKey(inRule.Protocol, relayPort)]
		if len(outRulesForPort) == 0 {
			continue
		}

		for _, outRule := range outRulesForPort {
			// 检查是否已存在
			key := buildTunnelRuleKey(inRule.Protocol, inRule.InPort, relayPort, outRule.TargetHost, outRule.OutPort)
			if existing[key] {
				continue
			}

			// 去重
			if seen[key] {
				continue
			}
			seen[key] = true

			// 推断隧道
			tunnelID, tunnelName := inferTunnelForTunnelForward(inNode.ID, outNode.ID)

			detected = append(detected, &DetectedTunnelRule{
				InPort:      inRule.InPort,
				RelayPort:   relayPort,
				TargetHost:  outRule.TargetHost,
				TargetPort:  outRule.OutPort,
				Protocol:    inRule.Protocol,
				TunnelID:    tunnelID,
				TunnelName:  tunnelName,
				InNodeName:  inNode.Name,
				OutNodeName: outNode.Name,
				InRawRule:   inRule.RawRule,
				OutRawRule:  outRule.RawRule,
				Suggested:   tunnelID > 0,
			})
		}
	}

	return detected
}

func buildTunnelRuleKey(protocol string, inPort, relayPort int, targetHost string, targetPort int) string {
	return fmt.Sprintf("%s:%d:%d:%s:%d", strings.ToLower(protocol), inPort, relayPort, strings.ToLower(strings.TrimSpace(targetHost)), targetPort)
}

func buildProtocolPortKey(protocol string, port int) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(protocol), port)
}

// getNodeIPs 获取节点的所有 IP 地址
func getNodeIPs(node *model.Node) []string {
	ips := []string{node.ServerIP}
	// 如果有其他 IP 字段，也添加进来
	if node.IP != "" && node.IP != node.ServerIP {
		ips = append(ips, node.IP)
	}
	return ips
}

// inferTunnelForTunnelForward 推断隧道转发所属的隧道
func inferTunnelForTunnelForward(inNodeID, outNodeID int64) (int64, string) {
	var tunnels []model.Tunnel
	model.DB.Where("in_node_id = ? AND out_node_id = ? AND type = ? AND status = 1",
		inNodeID, outNodeID, tunnelTypeTunnelForward).Find(&tunnels)

	if len(tunnels) == 0 {
		return 0, ""
	}

	// 如果只有一个隧道，直接返回
	if len(tunnels) == 1 {
		return tunnels[0].ID, tunnels[0].Name
	}

	// 多个隧道，返回第一个
	return tunnels[0].ID, tunnels[0].Name + " (建议)"
}
