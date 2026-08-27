package service

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// DetectedForwardRule 识别到的转发规则
type DetectedForwardRule struct {
	InPort     int    `json:"inPort"`
	OutPort    int    `json:"outPort"`
	TargetHost string `json:"targetHost"`
	Protocol   string `json:"protocol"`
	TunnelID   int64  `json:"tunnelId"`
	TunnelName string `json:"tunnelName"`
	Comment    string `json:"comment"`
	RawRule    string `json:"rawRule"`
	Suggested  bool   `json:"suggested"` // 是否推荐补全（有完整信息）
}

// DetectNftRulesResult 识别结果
type DetectNftRulesResult struct {
	Detected []*DetectedForwardRule `json:"detected"`
	Total    int                    `json:"total"`
	NodeName string                 `json:"nodeName"`
	NodeID   int64                  `json:"nodeId"`
}

// CompleteForwardRule 待补全的转发规则
type CompleteForwardRule struct {
	TunnelID   int64  `json:"tunnelId"`
	InPort     int    `json:"inPort"`
	OutPort    *int   `json:"outPort"`
	TargetPort *int   `json:"targetPort"`
	TargetHost string `json:"targetHost"`
	Protocol   string `json:"protocol"`
	Name       string `json:"name"`
	RawRule    string `json:"rawRule"`
	OutRawRule string `json:"outRawRule"`
}

// CompleteFromNftResult 补全结果
type CompleteFromNftResult struct {
	Created int                     `json:"created"`
	Failed  int                     `json:"failed"`
	Details []CompleteForwardDetail `json:"details"`
}

// CompleteForwardDetail 单个补全结果详情
type CompleteForwardDetail struct {
	InPort    int    `json:"inPort"`
	Success   bool   `json:"success"`
	ForwardID int64  `json:"forwardId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DetectNftForwardRules 从节点读取 NFT 规则并识别未在数据库中的转发
func DetectNftForwardRules(nodeID int64) result.R {
	// 检查节点
	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		return result.Err("节点不存在")
	}

	if node.Status != nodeStatusOnline {
		return result.Err("节点离线，无法读取规则")
	}

	if !isNftablesMode(&node) {
		return result.Err("该节点不是 nftables 模式")
	}

	// 从节点读取当前 NFT 规则
	wsResult := ws.SendMsg(nodeID, nil, "ListNftRules")
	if !gost.IsOK(wsResult) {
		return result.Err("读取节点规则失败: " + wsResult.Msg)
	}

	// 解析响应
	var ruleData struct {
		Rules []string `json:"rules"`
	}
	if err := gost.UnmarshalData(wsResult.Data, &ruleData); err != nil {
		return result.Err("解析规则数据失败")
	}

	// 解析规则
	parsedRules := gost.ParseNftRules(ruleData.Rules)
	if len(parsedRules) == 0 {
		return result.Ok(&DetectNftRulesResult{
			Detected: []*DetectedForwardRule{},
			Total:    0,
			NodeName: node.Name,
			NodeID:   nodeID,
		})
	}

	// 查询数据库中已存在的转发
	existingForwards := loadExistingForwardMap(nodeID)
	portScope := loadPortDetectScope(nodeID)

	// 对比并找出缺失的规则
	detected := findMissingRules(parsedRules, existingForwards, portScope)

	return result.Ok(&DetectNftRulesResult{
		Detected: detected,
		Total:    len(detected),
		NodeName: node.Name,
		NodeID:   nodeID,
	})
}

// loadExistingForwards 加载节点相关的所有转发记录
func loadExistingForwardMap(nodeID int64) map[string]*model.Forward {
	var tunnels []model.Tunnel
	model.DB.Where("in_node_id = ? AND type = ?", nodeID, tunnelTypePortForward).Find(&tunnels)

	if len(tunnels) == 0 {
		return make(map[string]*model.Forward)
	}

	tunnelIDs := make([]int64, len(tunnels))
	for i, t := range tunnels {
		tunnelIDs[i] = t.ID
	}

	var forwards []model.Forward
	model.DB.Where("tunnel_id IN ?", tunnelIDs).Find(&forwards)

	// 构建索引：key = protocol:inPort:targetHost:outPort
	forwardMap := make(map[string]*model.Forward)
	for i := range forwards {
		f := &forwards[i]

		// 获取隧道信息判断类型
		var tunnel model.Tunnel
		if err := model.DB.First(&tunnel, f.TunnelID).Error; err != nil {
			continue
		}

		if tunnel.Type != tunnelTypePortForward {
			continue
		}
		for _, addr := range splitRemoteAddresses(effectiveForwardRemoteAddr(f)) {
			host := extractHost(addr)
			port := extractPort(addr)
			if host != "" && port > 0 {
				key := buildForwardKey("tcp", f.InPort, host, port)
				forwardMap[key] = f
				key = buildForwardKey("udp", f.InPort, host, port)
				forwardMap[key] = f
			}
		}
	}

	return forwardMap
}

type portDetectScope struct {
	portTunnels      []model.Tunnel
	tunnelEntryRules map[string]bool
	tunnelEntryHosts map[string]bool
	tunnelExitPorts  map[string]bool
}

func loadPortDetectScope(nodeID int64) portDetectScope {
	scope := portDetectScope{
		tunnelEntryRules: make(map[string]bool),
		tunnelEntryHosts: make(map[string]bool),
		tunnelExitPorts:  make(map[string]bool),
	}
	model.DB.Where("in_node_id = ? AND type = ? AND status = 1", nodeID, tunnelTypePortForward).Find(&scope.portTunnels)

	var entryTunnels []model.Tunnel
	model.DB.Where("in_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&entryTunnels)
	for _, tunnel := range entryTunnels {
		nextNodeID := tunnel.OutNodeID
		if relayNodeID := tunnelRelayNodeID(&tunnel); relayNodeID > 0 {
			nextNodeID = relayNodeID
		}
		var nextNode model.Node
		if err := model.DB.First(&nextNode, nextNodeID).Error; err != nil {
			continue
		}
		for _, ip := range getNodeIPs(&nextNode) {
			if ip = strings.TrimSpace(ip); ip != "" {
				scope.tunnelEntryHosts[strings.ToLower(ip)] = true
			}
		}
		forwardRows := forwardsForTunnel(tunnel.ID)
		for _, f := range forwardRows {
			nextPort := 0
			if tunnelHasRelay(&tunnel) {
				if member := nftForwardExitMember(&f, &tunnel); member != nil {
					nextPort = member.RelayPort
				}
			} else if f.OutPort != nil {
				nextPort = *f.OutPort
			}
			if nextPort <= 0 {
				continue
			}
			for _, protocol := range resolveProtocols(&tunnel) {
				scope.tunnelEntryRules[buildForwardKey(protocol, f.InPort, nextNode.ServerIP, nextPort)] = true
			}
		}
	}

	// 三节点中继 B 的本机监听端口也属于受管隧道规则，不能被普通端口
	// 转发识别误报为“数据库缺失”。
	var relayTunnels []model.Tunnel
	model.DB.Where("relay_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&relayTunnels)
	for _, tunnel := range relayTunnels {
		for _, f := range forwardsForTunnel(tunnel.ID) {
			for _, member := range deployForwardExitMembers(&f, &tunnel) {
				if member.RelayPort <= 0 {
					continue
				}
				for _, protocol := range resolveProtocols(&tunnel) {
					scope.tunnelExitPorts[buildProtocolPortKey(protocol, member.RelayPort)] = true
				}
			}
		}
	}

	var exitTunnels []model.Tunnel
	model.DB.Where("out_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&exitTunnels)
	for _, tunnel := range exitTunnels {
		forwardRows := forwardsForTunnel(tunnel.ID)
		for _, f := range forwardRows {
			if f.OutPort == nil {
				continue
			}
			for _, protocol := range resolveProtocols(&tunnel) {
				scope.tunnelExitPorts[buildProtocolPortKey(protocol, *f.OutPort)] = true
			}
		}
	}

	return scope
}

func forwardsForTunnel(tunnelID int64) []model.Forward {
	var forwards []model.Forward
	model.DB.Where("tunnel_id = ?", tunnelID).Find(&forwards)
	return forwards
}

// findMissingRules 找出 NFT 中存在但数据库中缺失的规则
func findMissingRules(parsed []*gost.ParsedNftRule, existing map[string]*model.Forward, scope portDetectScope) []*DetectedForwardRule {
	var detected []*DetectedForwardRule
	seen := make(map[string]bool) // 去重

	for _, rule := range parsed {
		// 构建规则键
		key := buildForwardKey(rule.Protocol, rule.InPort, rule.TargetHost, rule.OutPort)

		// 检查是否已在数据库中
		if _, exists := existing[key]; exists {
			continue
		}
		if scope.isTunnelForwardRule(rule, key) {
			continue
		}

		// 去重（同一个转发可能有多条规则：tcp/udp）
		dedupeKey := fmt.Sprintf("%d:%s:%d", rule.InPort, rule.TargetHost, rule.OutPort)
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

		// 推断隧道
		tunnelID, tunnelName := inferPortTunnel(scope.portTunnels)
		if tunnelID == 0 {
			continue
		}

		detected = append(detected, &DetectedForwardRule{
			InPort:     rule.InPort,
			OutPort:    rule.OutPort,
			TargetHost: rule.TargetHost,
			Protocol:   rule.Protocol,
			TunnelID:   tunnelID,
			TunnelName: tunnelName,
			Comment:    rule.Comment,
			RawRule:    rule.RawRule,
			Suggested:  tunnelID > 0, // 有推断出隧道才推荐补全
		})
	}

	return detected
}

func (scope portDetectScope) isTunnelForwardRule(rule *gost.ParsedNftRule, key string) bool {
	if scope.tunnelEntryRules[key] {
		return true
	}
	if scope.tunnelEntryHosts[strings.ToLower(strings.TrimSpace(rule.TargetHost))] {
		return true
	}
	return scope.tunnelExitPorts[buildProtocolPortKey(rule.Protocol, rule.InPort)]
}

// inferPortTunnel 推断端口转发所属隧道，只返回端口转发隧道
func inferPortTunnel(tunnels []model.Tunnel) (int64, string) {
	if len(tunnels) == 0 {
		return 0, ""
	}
	if len(tunnels) == 1 {
		return tunnels[0].ID, tunnels[0].Name
	}
	return tunnels[0].ID, tunnels[0].Name + " (建议)"
}

// CompleteFromNft 从识别的规则批量创建转发
func CompleteFromNft(cu CurrentUser, nodeID int64, rules []CompleteForwardRule) result.R {
	// 权限检查：只有管理员
	if cu.RoleID != adminRoleID {
		return result.Err("权限不足，仅管理员可操作")
	}

	// 检查节点
	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		return result.Err("节点不存在")
	}

	if !isNftablesMode(&node) {
		return result.Err("该节点不是 nftables 模式")
	}

	var details []CompleteForwardDetail
	created := 0
	failed := 0

	for _, rule := range rules {
		detail := CompleteForwardDetail{
			InPort: rule.InPort,
		}

		// 创建转发
		forwardID, err := createForwardFromNft(cu, nodeID, &rule)
		if err != nil {
			detail.Success = false
			detail.Error = err.Error()
			failed++
			log.Printf("补全转发失败 (inPort=%d): %v", rule.InPort, err)
		} else {
			detail.Success = true
			detail.ForwardID = forwardID
			created++
		}

		details = append(details, detail)
	}

	return result.Ok(&CompleteFromNftResult{
		Created: created,
		Failed:  failed,
		Details: details,
	})
}

// createForwardFromNft 从识别的规则创建转发记录
func createForwardFromNft(cu CurrentUser, nodeID int64, rule *CompleteForwardRule) (int64, error) {
	// 验证隧道
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, rule.TunnelID).Error; err != nil {
		return 0, fmt.Errorf("隧道不存在")
	}

	if tunnel.Status != tunnelStatusActive {
		return 0, fmt.Errorf("隧道已禁用")
	}
	if tunnel.InNodeID != nodeID {
		return 0, fmt.Errorf("识别节点与隧道入口节点不匹配")
	}
	if tunnelHasRelay(&tunnel) {
		return 0, fmt.Errorf("三节点串联隧道不支持从已有两节点 NFT 规则补全")
	}
	lockedInNodeID, lockedOutNodeID, lockedTunnelType := tunnel.InNodeID, tunnel.OutNodeID, tunnel.Type
	lockedRelayNodeID := tunnelRelayNodeID(&tunnel)
	affected := tunnelPathNodeIDs(&tunnel)
	unlockSaga := lockNftSagaNodes(affected)
	defer unlockSaga()
	// The tunnel may have been edited while this request waited for its node
	// locks. Re-read it before deriving the operation snapshot.
	if err := model.DB.First(&tunnel, rule.TunnelID).Error; err != nil || tunnel.Status != tunnelStatusActive ||
		tunnel.InNodeID != lockedInNodeID || tunnel.OutNodeID != lockedOutNodeID || tunnel.Type != lockedTunnelType ||
		tunnelRelayNodeID(&tunnel) != lockedRelayNodeID {
		return 0, fmt.Errorf("隧道已变更，请重试")
	}
	if err := validateCompleteProtocolSelection(&tunnel, rule); err != nil {
		return 0, err
	}
	targetPort := resolveCompleteTargetPort(&tunnel, rule)
	if targetPort <= 0 {
		return 0, fmt.Errorf("目标端口无效")
	}
	outPort := rule.OutPort
	if tunnel.Type == tunnelTypeTunnelForward {
		if outPort == nil || *outPort <= 0 {
			return 0, fmt.Errorf("中转端口无效")
		}
	} else {
		p := targetPort
		outPort = &p
	}

	// 生成转发名称
	name := rule.Name
	if name == "" {
		name = fmt.Sprintf("NFT自动识别-%d", rule.InPort)
	}

	// 构建转发对象
	now := time.Now().UnixMilli()
	forward := model.Forward{
		UserID:      cu.UserID,
		UserName:    cu.UserName,
		Name:        name,
		TunnelID:    rule.TunnelID,
		InPort:      rule.InPort,
		OutPort:     outPort,
		RemoteAddr:  joinHostPort(rule.TargetHost, targetPort),
		Strategy:    "fifo",
		CreatedTime: now,
		UpdatedTime: now,
		// The adopted runtime is not trusted until ReplaceNftRules succeeds.
		Status: forwardStatusPaused,
		Inx:    nextForwardIndex(),
	}

	var inNode model.Node
	if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil {
		return 0, fmt.Errorf("入口节点不存在")
	}
	if !isNftablesMode(&inNode) {
		return 0, fmt.Errorf("入口节点已不是 nftables 模式，请重新识别")
	}
	var outNode *model.Node
	if tunnel.Type == tunnelTypeTunnelForward && tunnel.OutNodeID != 0 {
		var n model.Node
		if err := model.DB.First(&n, tunnel.OutNodeID).Error; err != nil {
			return 0, fmt.Errorf("出口节点不存在")
		}
		outNode = &n
	}
	if outNode != nil && isNftablesMode(outNode) && strings.TrimSpace(rule.OutRawRule) == "" {
		return 0, fmt.Errorf("NFT 出口规则必须一并选择后才能交接")
	}
	if rule.InPort < inNode.PortSta || rule.InPort > inNode.PortEnd {
		return 0, fmt.Errorf("入口端口 %d 超出节点允许范围", rule.InPort)
	}
	if tunnel.Type == tunnelTypeTunnelForward && outNode != nil && (*outPort < outNode.PortSta || *outPort > outNode.PortEnd) {
		return 0, fmt.Errorf("出口端口 %d 超出节点允许范围", *outPort)
	}
	if err := preflightCompleteRuleSemantics(&forward, &tunnel, &inNode, outNode, rule); err != nil {
		return 0, fmt.Errorf("原始规则与补全配置不匹配: %w", err)
	}

	// Port membership checks and the initially non-active row are one database
	// transaction under the short allocation lock. External agent work happens
	// only after releasing it.
	portAllocMu.Lock()
	persistErr := model.DB.Transaction(func(tx *gorm.DB) error {
		usedIn, err := getAllUsedPortsOnNodeWithDB(tx, tunnel.InNodeID, nil)
		if err != nil {
			return err
		}
		if usedIn[rule.InPort] {
			return fmt.Errorf("入口节点端口 %d 已被占用", rule.InPort)
		}
		if tunnel.Type == tunnelTypeTunnelForward && outNode != nil {
			usedOut, err := getAllUsedPortsOnNodeWithDB(tx, outNode.ID, nil)
			if err != nil {
				return err
			}
			if usedOut[*outPort] || outNode.ID == tunnel.InNodeID && *outPort == rule.InPort {
				return fmt.Errorf("出口节点端口 %d 已被占用", *outPort)
			}
		}
		return tx.Create(&forward).Error
	})
	portAllocMu.Unlock()
	if persistErr != nil {
		return 0, fmt.Errorf("创建转发失败: %w", persistErr)
	}

	replacements, prepareErr := prepareCompleteRuleReplacements(&forward, &tunnel, &inNode, outNode, rule)
	if prepareErr != nil {
		return completeFailureResult(&forward, cleanupFailedComplete(&forward, prepareErr, nil, nil))
	}
	applied := make([]completeRuleReplacement, 0, len(replacements))
	outcomeUnknown := false
	var knownFailureAfterUnknown error
	for _, replacement := range replacements {
		if err := replaceCompleteRules(replacement); err != nil {
			if errors.Is(err, errNftReplaceOutcomeUnknown) {
				outcomeUnknown = true
			} else if outcomeUnknown {
				knownFailureAfterUnknown = err
				break
			} else {
				return completeFailureResult(&forward, cleanupFailedComplete(&forward, err, applied, nil))
			}
		}
		applied = append(applied, replacement)
	}
	// Linearization point: durable active desired state must exist before a Gost
	// Add can be written to a session whose outcome may become unknown.
	if activateErr := transitionCompleteDesiredStatus(&forward, forwardStatusPaused, forwardStatusActive); activateErr != nil {
		if outcomeUnknown {
			return 0, retainUncertainComplete(&forward, errors.Join(errNftReplaceOutcomeUnknown, activateErr))
		}
		return completeFailureResult(&forward, cleanupFailedComplete(&forward, activateErr, applied, nil))
	}
	if gostDeployErr := deployCompleteGostRuntime(&forward, &tunnel); gostDeployErr != nil {
		// Once either protocol has an unknown outcome, never send a compensating
		// delete to a replacement session. Keep active desired state and let the
		// existing reconnect sync converge it.
		if outcomeUnknown || errors.Is(gostDeployErr, errCompleteGostOutcomeUnknown) {
			log.Printf("补全转发运行态待重连收敛(forward=%d): %v", forward.ID, errors.Join(knownFailureAfterUnknown, gostDeployErr))
			markNodesDirtyBestEffort(nftRuntimeNodeIDs(&forward, &tunnel)...)
			return forward.ID, nil
		}
		// With only definite outcomes it is safe to make desired state non-active
		// before cleanup and reverse replacement.
		if pauseErr := transitionCompleteDesiredStatus(&forward, forwardStatusActive, forwardStatusPaused); pauseErr != nil {
			return completePauseTransitionFailure(&forward, errors.Join(gostDeployErr, pauseErr))
		}
		gostCleanupErr := cleanupCompleteGostRuntime(&forward, &tunnel)
		return completeFailureResult(&forward, cleanupFailedComplete(&forward, gostDeployErr, applied, gostCleanupErr))
	}
	if knownFailureAfterUnknown != nil {
		log.Printf("补全转发 NFT 运行态待重连收敛(forward=%d): %v", forward.ID, knownFailureAfterUnknown)
		markNodesDirtyBestEffort(nftRuntimeNodeIDs(&forward, &tunnel)...)
		return forward.ID, nil
	}

	log.Printf("从 NFT 自动补全转发: ID=%d, InPort=%d, Target=%s", forward.ID, forward.InPort, forward.RemoteAddr)

	return forward.ID, nil
}

func completePauseTransitionFailure(forward *model.Forward, primary error) (int64, error) {
	var current model.Forward
	if err := model.DB.First(&current, forward.ID).Error; err != nil {
		return 0, fmt.Errorf("Gost 部署失败且无法确认期望状态: %w", errors.Join(primary, err))
	}
	if current.Status == forwardStatusActive {
		*forward = current
		log.Printf("Gost 部署失败但 active 期望状态已持久化，等待重连收敛(forward=%d): %v", forward.ID, primary)
		return forward.ID, nil
	}
	return 0, fmt.Errorf("Gost 部署失败且期望状态不是 active: %w", primary)
}

func transitionCompleteDesiredStatus(forward *model.Forward, from, to int) error {
	updatedTime := time.Now().UnixMilli()
	res := model.DB.Model(&model.Forward{}).Where("id = ? AND status = ?", forward.ID, from).Updates(map[string]interface{}{
		"status": to, "updated_time": updatedTime,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errors.New("补全转发状态已变化")
	}
	forward.Status = to
	forward.UpdatedTime = updatedTime
	return nil
}

type completeRuleReplacement struct {
	nodeID        int64
	expectedTable string
	deleteHandles []RuleHandle
	addRules      []string
	originalRules []string
}

var errNftReplaceOutcomeUnknown = errors.New("nft replace outcome unknown")
var errCompleteGostOutcomeUnknown = errors.New("complete Gost outcome unknown")
var errCompleteAcceptedPending = errors.New("complete accepted pending")

var addCompleteGostRemoteService = gost.AddRemoteServiceLifecycle
var deleteCompleteGostRemoteService = gost.DeleteRemoteServiceLifecycle

func validateCompleteProtocolSelection(tunnel *model.Tunnel, rule *CompleteForwardRule) error {
	protocols := resolveProtocols(tunnel)
	if len(protocols) != 1 {
		return errors.New("多协议隧道无法用单条原始规则安全交接")
	}
	selected := strings.ToLower(strings.TrimSpace(rule.Protocol))
	if selected == "" || selected != protocols[0] {
		return fmt.Errorf("识别规则协议 %q 与隧道协议 %q 不一致", rule.Protocol, protocols[0])
	}
	if selected != defaultProtocol(tunnel.Protocol) {
		return fmt.Errorf("识别规则协议 %q 与隧道声明协议 %q 不一致", rule.Protocol, defaultProtocol(tunnel.Protocol))
	}
	return nil
}

type completeSelectedRule struct {
	node *model.Node
	raw  string
}

func completeSelectedRules(inNode, outNode *model.Node, rule *CompleteForwardRule) []completeSelectedRule {
	selected := []completeSelectedRule{{node: inNode, raw: rule.RawRule}}
	if outNode != nil && strings.TrimSpace(rule.OutRawRule) != "" {
		selected = append(selected, completeSelectedRule{node: outNode, raw: rule.OutRawRule})
	}
	return selected
}

func preflightCompleteRuleSemantics(forward *model.Forward, tunnel *model.Tunnel, inNode, outNode *model.Node, rule *CompleteForwardRule) error {
	validated := 0
	for _, selectedRule := range completeSelectedRules(inNode, outNode, rule) {
		if selectedRule.node == nil || !isNftablesMode(selectedRule.node) {
			continue
		}
		if strings.TrimSpace(selectedRule.raw) == "" {
			return errors.New("缺少待交接原始规则")
		}
		built, err := buildForwardNftRulesToAdd(forward, tunnel, selectedRule.node)
		if err != nil {
			return err
		}
		if _, err := validateCompleteSelectedRule(selectedRule.raw, built, rule.Protocol); err != nil {
			return err
		}
		validated++
	}
	if validated == 0 {
		return errors.New("没有可交接的 nftables 规则")
	}
	return nil
}

func prepareCompleteRuleReplacements(forward *model.Forward, tunnel *model.Tunnel, inNode *model.Node, outNode *model.Node, rule *CompleteForwardRule) ([]completeRuleReplacement, error) {
	var replacements []completeRuleReplacement
	for _, selectedRule := range completeSelectedRules(inNode, outNode, rule) {
		if selectedRule.node == nil || !isNftablesMode(selectedRule.node) {
			continue
		}
		if strings.TrimSpace(selectedRule.raw) == "" {
			return nil, errors.New("缺少待交接原始规则")
		}
		built, err := buildForwardNftRulesToAdd(forward, tunnel, selectedRule.node)
		if err != nil {
			return nil, err
		}
		addRules := make([]string, 0, len(built))
		for _, item := range built {
			addRules = append(addRules, item.Rule)
		}
		if len(addRules) == 0 {
			return nil, errors.New("无法构建交接规则")
		}
		originalRule, err := validateCompleteSelectedRule(selectedRule.raw, built, rule.Protocol)
		if err != nil {
			return nil, fmt.Errorf("原始规则与补全配置不匹配: %w", err)
		}
		view, err := findRuleHandleByRaw(selectedRule.node.ID, selectedRule.raw)
		if err != nil {
			return nil, err
		}
		replacements = append(replacements, completeRuleReplacement{
			nodeID: selectedRule.node.ID, expectedTable: view.Table, deleteHandles: view.Handles,
			addRules: addRules, originalRules: []string{originalRule},
		})
	}
	if len(replacements) == 0 {
		return nil, errors.New("没有可交接的 nftables 规则")
	}
	return replacements, nil
}

func validateCompleteSelectedRule(raw string, built []NftRuleToAdd, selectedProtocol string) (string, error) {
	originalRule, err := canonicalCompleteOriginalRule(raw)
	if err != nil {
		return "", err
	}
	rawParsed, err := gost.ParseNftRule(originalRule)
	if err != nil {
		return "", err
	}
	if rawParsed.Protocol != strings.ToLower(strings.TrimSpace(selectedProtocol)) {
		return "", errors.New("原始规则协议与选择协议不一致")
	}
	var candidates []*gost.ParsedNftRule
	for _, item := range built {
		if parsed, parseErr := gost.ParseNftRule(item.Rule); parseErr == nil {
			candidates = append(candidates, parsed)
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("预期恰好一条 DNAT 候选，实际 %d 条", len(candidates))
	}
	expected := candidates[0]
	rawTarget, err := ParseTargetAddress(joinHostPort(rawParsed.TargetHost, rawParsed.OutPort), true)
	if err != nil {
		return "", err
	}
	expectedTarget, err := ParseTargetAddress(joinHostPort(expected.TargetHost, expected.OutPort), true)
	if err != nil {
		return "", err
	}
	if rawParsed.Protocol != expected.Protocol || rawParsed.InPort != expected.InPort ||
		rawParsed.Family != expected.Family || rawTarget.IP != expectedTarget.IP || rawTarget.Port != expectedTarget.Port {
		return "", errors.New("原始 DNAT 的协议、监听端口、地址族或目标与补全配置不一致")
	}
	return originalRule, nil
}

func completeGostExitNodes(forward *model.Forward, tunnel *model.Tunnel) map[int64]model.Node {
	nodes := forwardExitNodeMap(deployForwardExitMembers(forward, tunnel))
	for nodeID, node := range nodes {
		if isNftablesMode(&node) {
			delete(nodes, nodeID)
		}
	}
	return nodes
}

func deployCompleteGostRuntime(forward *model.Forward, tunnel *model.Tunnel) error {
	if tunnel.Type != tunnelTypeTunnelForward {
		return nil
	}
	remoteAddr, err := normalizeGostRemoteAddresses(effectiveForwardRemoteAddr(forward))
	if err != nil {
		return err
	}
	serviceName := buildServiceName(forward.ID, forward.UserID, getUserTunnel(forward.UserID, tunnel.ID))
	members := deployForwardExitMembers(forward, tunnel)
	nodes := completeGostExitNodes(forward, tunnel)
	for _, member := range members {
		node, ok := nodes[member.OutNodeID]
		if !ok {
			continue
		}
		res := addCompleteGostRemoteService(node.ID, serviceName, member.OutPort, remoteAddr, tunnelProtocol(tunnel), forward.Strategy, interfaceNameOf(forward))
		if !gost.IsOK(res) {
			err := fmt.Errorf("部署 Gost 出口服务失败(node=%d): %s", node.ID, res.Msg)
			if res.OutcomeUnknown {
				return errors.Join(errCompleteGostOutcomeUnknown, err)
			}
			return err
		}
	}
	return nil
}

func cleanupCompleteGostRuntime(forward *model.Forward, tunnel *model.Tunnel) error {
	if tunnel.Type != tunnelTypeTunnelForward {
		return nil
	}
	serviceName := buildServiceName(forward.ID, forward.UserID, getUserTunnel(forward.UserID, tunnel.ID))
	var errs []error
	for _, node := range completeGostExitNodes(forward, tunnel) {
		res := deleteCompleteGostRemoteService(node.ID, serviceName)
		if gost.IsOK(res) || strings.Contains(strings.ToLower(res.Msg), gost.NotFoundMsg) {
			continue
		}
		err := fmt.Errorf("清理 Gost 出口服务失败(node=%d): %s", node.ID, res.Msg)
		if res.OutcomeUnknown {
			err = errors.Join(errCompleteGostOutcomeUnknown, err)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func replaceCompleteRules(replacement completeRuleReplacement) error {
	res := sendNftIncrementalMessage(replacement.nodeID, map[string]interface{}{
		"expectedTable": replacement.expectedTable,
		"deleteHandles": replacement.deleteHandles,
		"addRules":      replacement.addRules,
	}, "ReplaceNftRules")
	if !gost.IsOK(res) {
		if res.OutcomeUnknown {
			return fmt.Errorf("%w: %s", errNftReplaceOutcomeUnknown, res.Msg)
		}
		return nftIncrementalCommandError("精确替换 NFT 规则失败", res.Msg)
	}
	return nil
}

func canonicalCompleteOriginalRule(raw string) (string, error) {
	fields := strings.Fields(normalizeNftRuleForCompare(raw))
	if len(fields) < 11 || fields[0] != "add" || fields[1] != "rule" || fields[2] != "inet" ||
		fields[3] != "flux_panel" || fields[4] != "prerouting" {
		return "", errors.New("原始规则不是 canonical prerouting 规则")
	}
	i := 5
	family := ""
	if i+2 < len(fields) && fields[i] == "meta" && fields[i+1] == "nfproto" {
		family = fields[i+2]
		if family != "ipv4" && family != "ipv6" {
			return "", errors.New("原始规则地址族无效")
		}
		i += 3
	}
	if i >= len(fields) || fields[i] != "tcp" && fields[i] != "udp" {
		return "", errors.New("原始规则协议无效")
	}
	protocol := fields[i]
	i++
	if i+1 >= len(fields) || fields[i] != "dport" {
		return "", errors.New("原始规则缺少目标端口匹配")
	}
	inPort, err := strconv.Atoi(fields[i+1])
	if err != nil || inPort < 1 || inPort > 65535 {
		return "", errors.New("原始规则入口端口无效")
	}
	i += 2
	if i >= len(fields) || fields[i] != "dnat" {
		return "", errors.New("原始规则缺少 dnat")
	}
	i++
	dnatFamily := ""
	if i < len(fields) && (fields[i] == "ip" || fields[i] == "ip6") {
		dnatFamily = fields[i]
		i++
	}
	if i+1 >= len(fields) || fields[i] != "to" {
		return "", errors.New("原始规则缺少 dnat 目标")
	}
	target, err := ParseTargetAddress(fields[i+1], true)
	if err != nil {
		return "", err
	}
	i += 2
	// Any additional predicate, verdict, or comment would be lost by rebuilding
	// the strict canonical rule. Reject it before mutation instead of widening
	// the restored rule's permissions.
	if i != len(fields) {
		return "", errors.New("原始规则包含无法等价补偿的附加语义")
	}
	wantFamily := "ipv4"
	wantDnatFamily := "ip"
	if target.IP.Is6() {
		wantFamily, wantDnatFamily = "ipv6", "ip6"
	}
	if family != "" && family != wantFamily {
		return "", errors.New("原始规则地址族与目标不匹配")
	}
	if dnatFamily != "" && dnatFamily != wantDnatFamily {
		return "", errors.New("原始规则 dnat 地址族与目标不匹配")
	}
	return fmt.Sprintf("add rule inet flux_panel prerouting meta nfproto %s %s dport %d dnat to %s",
		wantFamily, protocol, inPort, netip.AddrPortFrom(target.IP, uint16(target.Port)).String()), nil
}

func cleanupFailedComplete(forward *model.Forward, primary error, applied []completeRuleReplacement, runtimeCleanupErr error) error {
	if errors.Is(runtimeCleanupErr, errCompleteGostOutcomeUnknown) {
		if err := transitionCompleteDesiredStatus(forward, forwardStatusPaused, forwardStatusActive); err != nil {
			return retainUncertainComplete(forward, errors.Join(primary, runtimeCleanupErr, err))
		}
		return errors.Join(errCompleteAcceptedPending, primary, runtimeCleanupErr)
	}
	var cleanupErrs []error
	for i := len(applied) - 1; i >= 0; i-- {
		replacement := applied[i]
		handles, err := findForwardRuleHandles(forward.ID, replacement.nodeID)
		if err != nil {
			cleanupErrs = append(cleanupErrs, err)
			continue
		}
		reverse := completeRuleReplacement{
			nodeID: replacement.nodeID, expectedTable: handles.Table,
			deleteHandles: handles.Handles, addRules: replacement.originalRules,
		}
		if err := replaceCompleteRules(reverse); errors.Is(err, errNftReplaceOutcomeUnknown) {
			if activateErr := transitionCompleteDesiredStatus(forward, forwardStatusPaused, forwardStatusActive); activateErr != nil {
				return retainUncertainComplete(forward, errors.Join(primary, err, activateErr))
			}
			return errors.Join(errCompleteAcceptedPending, primary, err)
		} else if err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	cleanupErr := errors.Join(runtimeCleanupErr, errors.Join(cleanupErrs...))
	if cleanupErr != nil {
		markErr := markForwardErrorDesired(forward.ID)
		var convergeErr error
		if markErr == nil {
			convergeErr = convergeCompleteErrorDesired(forward, applied)
		}
		return fmt.Errorf("补全 NFT 交接失败: %w", errors.Join(primary, cleanupErr, markErr, convergeErr))
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error { return deleteForwardRows(tx, forward.ID) }); err != nil {
		markErr := markForwardErrorDesired(forward.ID)
		var convergeErr error
		if markErr == nil {
			convergeErr = convergeCompleteErrorDesired(forward, applied)
		}
		return fmt.Errorf("补全 NFT 交接失败: %w", errors.Join(primary, err, markErr, convergeErr))
	}
	return fmt.Errorf("补全 NFT 交接失败: %w", primary)
}

func convergeCompleteErrorDesired(forward *model.Forward, applied []completeRuleReplacement) error {
	nodeIDs := make([]int64, 0, len(applied)+2)
	for _, replacement := range applied {
		nodeIDs = append(nodeIDs, replacement.nodeID)
	}
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
		return err
	}
	nodeIDs = append(nodeIDs, tunnelPathNodeIDs(&tunnel)...)
	members, err := loadPersistedForwardExitMembersDB(model.DB, forward.ID)
	if err != nil {
		return err
	}
	for _, member := range members {
		nodeIDs = append(nodeIDs, member.OutNodeID)
	}
	return refreshNftNodesCheckedLocked(normalizeNodeSagaLockIDs(nodeIDs))
}

func completeFailureResult(forward *model.Forward, err error) (int64, error) {
	if errors.Is(err, errCompleteAcceptedPending) {
		log.Printf("补全转发补偿结果不确定，保留 active 期望等待收敛(forward=%d): %v", forward.ID, err)
		return forward.ID, nil
	}
	return 0, err
}

func retainUncertainComplete(forward *model.Forward, primary error) error {
	markErr := markForwardErrorDesired(forward.ID)
	return fmt.Errorf("补全 NFT 交接结果不确定，已保留错误记录等待收敛: %w", errors.Join(primary, markErr))
}

func resolveCompleteTargetPort(tunnel *model.Tunnel, rule *CompleteForwardRule) int {
	if tunnel.Type == tunnelTypeTunnelForward && rule.TargetPort != nil {
		return *rule.TargetPort
	}
	if rule.OutPort != nil {
		return *rule.OutPort
	}
	return 0
}

func deleteNftRuleHandles(nodeID int64, table string, handles []RuleHandle) error {
	if nftgeneration.ValidateTableName(table) != nil || len(handles) == 0 || len(handles) > nftgeneration.MaxRuleBatchItems {
		return fmt.Errorf("批量删除规则参数无效")
	}
	res := sendNftIncrementalMessage(nodeID, map[string]interface{}{
		"expectedTable": table,
		"handles":       handles,
	}, "DeleteNftRules")
	if !gost.IsOK(res) {
		return nftIncrementalCommandError("批量删除规则失败", res.Msg)
	}
	return nil
}

// buildForwardKey 构建转发键用于对比
func buildForwardKey(protocol string, inPort int, targetHost string, outPort int) string {
	return fmt.Sprintf("%s:%d:%s:%d", strings.ToLower(protocol), inPort, strings.ToLower(targetHost), outPort)
}
