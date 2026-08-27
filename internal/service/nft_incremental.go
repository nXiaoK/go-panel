package service

import (
	"errors"
	"fmt"
	"log"
	"net/netip"
	"strconv"
	"strings"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// RuleHandle 规则的 handle 信息
type RuleHandle struct {
	Chain  string `json:"chain"`
	Handle int    `json:"handle"`
}

type nftRuleHandleView struct {
	Table   string       `json:"table"`
	Handles []RuleHandle `json:"handles"`
}

type nftRuleListView struct {
	Table string   `json:"table"`
	Rules []string `json:"rules"`
}

// NftRuleToAdd 要添加的 NFT 规则
type NftRuleToAdd struct {
	Rule string
}

var errNftIncrementalUnsupported = errors.New("nft incremental unsupported")

var sendNftIncrementalMessage = ws.SendMsg

// AddForwardRules 增量添加转发的所有 NFT 规则
func AddForwardRules(forward *model.Forward, tunnel *model.Tunnel, node *model.Node) error {
	if !isNftablesMode(node) {
		return nil
	}

	rules, err := buildForwardNftRulesToAdd(forward, tunnel, node)
	if err != nil {
		return err
	}

	if len(rules) == 0 {
		return nil
	}
	canonical := make([]string, 0, len(rules))
	for _, rule := range rules {
		canonical = append(canonical, rule.Rule)
	}
	result := sendNftIncrementalMessage(node.ID, map[string]interface{}{
		"rules": canonical,
	}, "AddNftRules")
	if !gost.IsOK(result) {
		return nftIncrementalCommandError("添加规则失败", result.Msg)
	}

	log.Printf("增量添加转发规则成功: forward_id=%d, node=%s", forward.ID, node.Name)
	return nil
}

// DeleteForwardRules 增量删除转发的所有 NFT 规则
func DeleteForwardRules(forward *model.Forward, node *model.Node) error {
	if !isNftablesMode(node) {
		return nil
	}

	// 1. 查找规则的 handle
	view, err := findForwardRuleHandles(forward.ID, node.ID)
	if err != nil {
		log.Printf("查找规则 handle 失败: %v", err)
		return err
	}
	if fallback, err := findForwardRuleHandlesBySpec(forward, node.ID); err == nil && len(fallback.Handles) > 0 {
		if fallback.Table != view.Table {
			return fmt.Errorf("%w: nft table changed between handle views", nftgeneration.ErrLocked)
		}
		view.Handles = mergeRuleHandles(view.Handles, fallback.Handles)
	} else if err != nil {
		log.Printf("按规则内容查找 handle 失败: %v", err)
	}

	if len(view.Handles) == 0 {
		log.Printf("未找到转发的规则: forward_id=%d", forward.ID)
		return nil
	}

	// 2. 逐条删除规则。任一失败必须上抛，否则调用方可能在规则
	// 仍存在时提交 DB/状态变更。
	if err := deleteNftRuleHandles(node.ID, view.Table, view.Handles); err != nil {
		return fmt.Errorf("删除转发规则失败: %w", err)
	}

	log.Printf("增量删除转发规则成功: forward_id=%d, node=%s, deleted=%d", forward.ID, node.Name, len(view.Handles))
	return nil
}

// findForwardRuleHandles 查找转发的规则 handle
func findForwardRuleHandles(forwardID int64, nodeID int64) (nftRuleHandleView, error) {
	result := sendNftIncrementalMessage(nodeID, map[string]interface{}{
		"forwardId": forwardID,
	}, "FindRuleHandles")

	if !gost.IsOK(result) {
		return nftRuleHandleView{}, nftIncrementalCommandError("查找规则失败", result.Msg)
	}

	var data nftRuleHandleView
	if err := gost.UnmarshalData(result.Data, &data); err != nil {
		return nftRuleHandleView{}, fmt.Errorf("解析结果失败: %v", err)
	}
	if nftgeneration.ValidateTableName(data.Table) != nil {
		return nftRuleHandleView{}, errNftIncrementalUnsupported
	}
	return data, nil
}

func listNodeNftRules(nodeID int64) (nftRuleListView, error) {
	res := sendNftIncrementalMessage(nodeID, nil, "ListNftRules")
	if !gost.IsOK(res) {
		return nftRuleListView{}, nftIncrementalCommandError("读取节点规则失败", res.Msg)
	}
	var ruleData nftRuleListView
	if err := gost.UnmarshalData(res.Data, &ruleData); err != nil {
		return nftRuleListView{}, fmt.Errorf("解析节点规则失败: %v", err)
	}
	if nftgeneration.ValidateTableName(ruleData.Table) != nil {
		return nftRuleListView{}, errNftIncrementalUnsupported
	}
	return ruleData, nil
}

func nftIncrementalCommandError(operation, message string) error {
	message = strings.TrimSpace(message)
	switch {
	case isNftIncrementalUnsupported(message):
		return errNftIncrementalUnsupported
	case strings.HasPrefix(message, nftgeneration.RetryableErrorPrefix):
		return fmt.Errorf("%w: %s", nftgeneration.ErrLocked, message)
	default:
		return fmt.Errorf("%s: %s", operation, message)
	}
}

func findForwardRuleHandlesBySpec(forward *model.Forward, nodeID int64) (nftRuleHandleView, error) {
	view, err := listNodeNftRules(nodeID)
	if err != nil {
		return nftRuleHandleView{}, err
	}
	expected := buildForwardRuleSpecs(forward, nodeID)
	if len(expected) == 0 {
		return nftRuleHandleView{Table: view.Table}, nil
	}
	var handles []RuleHandle
	for _, rule := range view.Rules {
		chain, handle, ok := parseNftRuleHandle(rule)
		if !ok {
			continue
		}
		if nftRuleMatchesAnySpec(rule, chain, expected) {
			handles = append(handles, RuleHandle{Chain: chain, Handle: handle})
		}
	}
	return nftRuleHandleView{Table: view.Table, Handles: handles}, nil
}

type nftRuleSpec struct {
	chain      string
	protocol   string
	listenPort int
	targetIP   netip.Addr
	targetPort int
}

func buildForwardRuleSpecs(forward *model.Forward, nodeID int64) []nftRuleSpec {
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
		return nil
	}

	if tunnel.Type == tunnelTypeTunnelForward {
		if tunnel.InNodeID == nodeID {
			member := nftForwardExitMember(forward, &tunnel)
			if member == nil {
				return nil
			}
			nextNodeID := member.OutNodeID
			nextPort := member.OutPort
			if relayNodeID := tunnelRelayNodeID(&tunnel); relayNodeID > 0 {
				nextNodeID = relayNodeID
				nextPort = member.RelayPort
			}
			var nextNode model.Node
			if nextPort <= 0 || model.DB.First(&nextNode, nextNodeID).Error != nil || strings.TrimSpace(nextNode.ServerIP) == "" {
				return nil
			}
			target, err := parseTargetHostPort(nextNode.ServerIP, nextPort, true)
			if err != nil {
				return nil
			}
			return buildRuleSpecsForTarget(&tunnel, forward.InPort, target.IP, target.Port)
		}
		if tunnelRelayNodeID(&tunnel) == nodeID {
			var specs []nftRuleSpec
			for _, member := range deployForwardExitMembers(forward, &tunnel) {
				if member.RelayPort <= 0 || member.OutPort <= 0 {
					continue
				}
				var outNode model.Node
				if model.DB.First(&outNode, member.OutNodeID).Error != nil {
					continue
				}
				target, err := parseTargetHostPort(outNode.ServerIP, member.OutPort, true)
				if err != nil {
					continue
				}
				specs = append(specs, buildRuleSpecsForTarget(&tunnel, member.RelayPort, target.IP, target.Port)...)
			}
			return specs
		}
		var specs []nftRuleSpec
		for _, member := range nftForwardExitMembersForNode(forward, &tunnel, nodeID) {
			specs = append(specs, buildRuleSpecsForRemote(&tunnel, member.OutPort, effectiveForwardRemoteAddr(forward))...)
		}
		return specs
	}

	if tunnel.InNodeID != nodeID {
		return nil
	}
	return buildRuleSpecsForRemote(&tunnel, forward.InPort, effectiveForwardRemoteAddr(forward))
}

func buildRuleSpecsForRemote(tunnel *model.Tunnel, listenPort int, remoteAddr string) []nftRuleSpec {
	var specs []nftRuleSpec
	for _, target := range splitRemoteAddresses(remoteAddr) {
		parsed, err := ParseTargetAddress(target, true)
		if err != nil {
			continue
		}
		specs = append(specs, buildRuleSpecsForTarget(tunnel, listenPort, parsed.IP, parsed.Port)...)
	}
	return specs
}

func buildRuleSpecsForTarget(tunnel *model.Tunnel, listenPort int, targetIP netip.Addr, targetPort int) []nftRuleSpec {
	var specs []nftRuleSpec
	for _, protocol := range resolveProtocols(tunnel) {
		specs = append(specs,
			nftRuleSpec{chain: "prerouting", protocol: protocol, listenPort: listenPort, targetIP: targetIP, targetPort: targetPort},
			nftRuleSpec{chain: "forward", protocol: protocol, targetIP: targetIP, targetPort: targetPort},
			nftRuleSpec{chain: "postrouting", protocol: protocol, targetIP: targetIP, targetPort: targetPort},
		)
	}
	return specs
}

func nftRuleMatchesAnySpec(rule, chain string, specs []nftRuleSpec) bool {
	for _, spec := range specs {
		if nftRuleMatchesSpec(rule, chain, spec) {
			return true
		}
	}
	return false
}

func nftRuleMatchesSpec(rule, chain string, spec nftRuleSpec) bool {
	if chain != spec.chain || !nftRuleHasProtocol(rule, spec.protocol) {
		return false
	}
	switch chain {
	case "prerouting":
		return nftRuleHasDport(rule, spec.listenPort) && nftRuleHasDnatTarget(rule, spec.targetIP.String(), spec.targetPort)
	case "forward":
		return (nftRuleHasDport(rule, spec.targetPort) && nftRuleHasAddress(rule, "daddr", spec.targetIP.String())) ||
			(nftRuleHasSport(rule, spec.targetPort) && nftRuleHasAddress(rule, "saddr", spec.targetIP.String()))
	case "postrouting":
		return nftRuleHasDport(rule, spec.targetPort) && nftRuleHasAddress(rule, "daddr", spec.targetIP.String())
	default:
		return false
	}
}

func nftRuleHasProtocol(rule, protocol string) bool {
	return hasToken(rule, strings.ToLower(protocol))
}

func nftRuleHasDport(rule string, port int) bool {
	return hasTokenSequence(rule, "dport", strconv.Itoa(port))
}

func nftRuleHasSport(rule string, port int) bool {
	return hasTokenSequence(rule, "sport", strconv.Itoa(port))
}

func nftRuleHasAddress(rule, field, host string) bool {
	return hasTokenSequence(rule, field, normalizeNftHost(host))
}

func nftRuleHasDnatTarget(rule, host string, port int) bool {
	target := normalizeNftHostPort(host, port)
	tokens := nftRuleTokens(rule)
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i] == "dnat" && tokens[i+1] == "to" && tokens[i+2] == target {
			return true
		}
		if tokens[i] == "dnat" && (tokens[i+1] == "ip" || tokens[i+1] == "ip6") && i+3 < len(tokens) && tokens[i+2] == "to" && tokens[i+3] == target {
			return true
		}
	}
	return false
}

func hasTokenSequence(rule string, seq ...string) bool {
	tokens := nftRuleTokens(rule)
	if len(seq) == 0 || len(tokens) < len(seq) {
		return false
	}
	for i := 0; i <= len(tokens)-len(seq); i++ {
		ok := true
		for j := range seq {
			if tokens[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func hasToken(rule, token string) bool {
	for _, item := range nftRuleTokens(rule) {
		if item == token {
			return true
		}
	}
	return false
}

func nftRuleTokens(rule string) []string {
	fields := strings.Fields(strings.ToLower(rule))
	for i := range fields {
		fields[i] = strings.Trim(fields[i], `"'`)
	}
	return fields
}

func normalizeNftHost(host string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
}

func normalizeNftHostPort(host string, port int) string {
	host = normalizeNftHost(host)
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func mergeRuleHandles(primary, extra []RuleHandle) []RuleHandle {
	seen := make(map[string]bool, len(primary)+len(extra))
	out := make([]RuleHandle, 0, len(primary)+len(extra))
	for _, handle := range append(primary, extra...) {
		key := fmt.Sprintf("%s:%d", handle.Chain, handle.Handle)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, handle)
	}
	return out
}

// buildForwardNftRulesToAdd 构建转发的 NFT 规则（用于增量添加）
func buildForwardNftRulesToAdd(forward *model.Forward, tunnel *model.Tunnel, node *model.Node) ([]NftRuleToAdd, error) {
	rules := []NftRuleToAdd{}

	if tunnel.Type == tunnelTypePortForward {
		// 端口转发：入口节点规则
		if err := appendEntryRulesToAdd(&rules, forward, tunnel, node); err != nil {
			return nil, err
		}
	} else if tunnel.Type == tunnelTypeTunnelForward {
		// 隧道转发：判断是入口还是出口
		if tunnel.InNodeID == node.ID {
			// 入口节点规则
			if err := appendEntryRulesToAdd(&rules, forward, tunnel, node); err != nil {
				return nil, err
			}
		} else if tunnelRelayNodeID(tunnel) == node.ID {
			// 中继节点规则
			if err := appendRelayRulesToAdd(&rules, forward, tunnel); err != nil {
				return nil, err
			}
		} else if len(nftForwardExitMembersForNode(forward, tunnel, node.ID)) > 0 {
			// 出口节点规则
			if err := appendExitRulesToAdd(&rules, forward, tunnel, node); err != nil {
				return nil, err
			}
		}
	}

	return rules, nil
}

func isNftIncrementalUnsupported(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "unknown command type") &&
		(strings.Contains(msg, "addnftrule") || strings.Contains(msg, "deletenftrule") ||
			strings.Contains(msg, "findrulehandles") || strings.Contains(msg, "listnftrules"))
}

// appendEntryRulesToAdd 添加入口节点规则
func appendEntryRulesToAdd(out *[]NftRuleToAdd, forward *model.Forward, tunnel *model.Tunnel, node *model.Node) error {
	if tunnel.Type == tunnelTypeTunnelForward {
		member := nftForwardExitMember(forward, tunnel)
		if member == nil {
			return nil
		}
		nextNodeID := member.OutNodeID
		nextPort := member.OutPort
		if relayNodeID := tunnelRelayNodeID(tunnel); relayNodeID > 0 {
			nextNodeID = relayNodeID
			nextPort = member.RelayPort
		}
		var nextNode model.Node
		if nextPort <= 0 || model.DB.First(&nextNode, nextNodeID).Error != nil || nextNode.ServerIP == "" {
			return nil
		}
		target, err := parseTargetHostPort(nextNode.ServerIP, nextPort, true)
		if err != nil {
			return nil
		}
		for _, protocol := range resolveProtocols(tunnel) {
			if err := appendProtocolRules(out, forward, tunnel, protocol, forward.InPort, target.IP, target.Port, true); err != nil {
				return err
			}
		}
		return nil
	}

	return appendTargetRules(out, forward, tunnel, forward.InPort, true)
}

func appendRelayRulesToAdd(out *[]NftRuleToAdd, forward *model.Forward, tunnel *model.Tunnel) error {
	for _, member := range deployForwardExitMembers(forward, tunnel) {
		if member.RelayPort <= 0 || member.OutPort <= 0 {
			continue
		}
		var outNode model.Node
		if err := model.DB.First(&outNode, member.OutNodeID).Error; err != nil || strings.TrimSpace(outNode.ServerIP) == "" {
			continue
		}
		target, err := parseTargetHostPort(outNode.ServerIP, member.OutPort, true)
		if err != nil {
			continue
		}
		for _, protocol := range resolveProtocols(tunnel) {
			if err := appendProtocolRules(out, forward, tunnel, protocol, member.RelayPort, target.IP, target.Port, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// appendExitRulesToAdd 添加出口节点规则
func appendExitRulesToAdd(out *[]NftRuleToAdd, forward *model.Forward, tunnel *model.Tunnel, node *model.Node) error {
	for _, member := range nftForwardExitMembersForNode(forward, tunnel, node.ID) {
		if err := appendTargetRules(out, forward, tunnel, member.OutPort, false); err != nil {
			return err
		}
	}
	return nil
}

func appendTargetRules(out *[]NftRuleToAdd, forward *model.Forward, tunnel *model.Tunnel, listenPort int, withCounter bool) error {
	for _, target := range splitRemoteAddresses(effectiveForwardRemoteAddr(forward)) {
		parsed, err := ParseTargetAddress(target, true)
		if err != nil {
			continue
		}
		for _, protocol := range resolveProtocols(tunnel) {
			if err := appendProtocolRules(out, forward, tunnel, protocol, listenPort, parsed.IP, parsed.Port, withCounter); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendProtocolRules(out *[]NftRuleToAdd, forward *model.Forward, tunnel *model.Tunnel, protocol string, listenPort int, targetIP netip.Addr, targetPort int, withCounter bool) error {
	family := determineFamily(targetIP)
	var rules []string
	var err error
	if withCounter {
		rules, err = gost.BuildEntryRules(forward.ID, forward.UserID, resolveUserTunnelID(forward.UserID, tunnel.ID), family, protocol, listenPort, targetIP, targetPort)
	} else {
		rules, err = gost.BuildExitRulesWithComment(forward.ID, forward.UserID, resolveUserTunnelID(forward.UserID, tunnel.ID), family, protocol, listenPort, targetIP, targetPort)
	}
	if err != nil {
		return err
	}
	for _, rule := range rules {
		*out = append(*out, NftRuleToAdd{Rule: rule})
	}
	return nil
}
