package gost

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// nftables 转发规则生成（对应 Java NftForwardRuleUtil）

const nftTable = "inet flux_panel"

// BuildCommentBase 规则注释标识 fp:forwardId:userId:userTunnelId
func BuildCommentBase(forwardID, userID, userTunnelID int64) string {
	return fmt.Sprintf("fp:%d:%d:%d", forwardID, userID, userTunnelID)
}

// BuildEntryRules 入口节点规则（带计数器注释，供流量统计）
func BuildEntryRules(forwardID, userID, userTunnelID int64, family, protocol string, inPort int, targetHost netip.Addr, targetPort int) ([]string, error) {
	commentBase := BuildCommentBase(forwardID, userID, userTunnelID)
	return buildExplicitRules(family, protocol, inPort, targetHost, targetPort, commentBase, true)
}

// BuildExitRules 出口节点规则（无计数器注释）
func BuildExitRules(family, protocol string, inPort int, targetHost netip.Addr, targetPort int) ([]string, error) {
	return buildExplicitRules(family, protocol, inPort, targetHost, targetPort, "", false)
}

// BuildExitRulesWithComment 出口节点规则（带可删除注释，不带计数器）
func BuildExitRulesWithComment(forwardID, userID, userTunnelID int64, family, protocol string, inPort int, targetHost netip.Addr, targetPort int) ([]string, error) {
	commentBase := BuildCommentBase(forwardID, userID, userTunnelID)
	return buildExplicitRules(family, protocol, inPort, targetHost, targetPort, commentBase, false)
}

func buildExplicitRules(family, protocol string, inPort int, targetHost netip.Addr, targetPort int, commentBase string, withCounterComment bool) ([]string, error) {
	if protocol != "tcp" && protocol != "udp" {
		return nil, fmt.Errorf("invalid nft protocol %q", protocol)
	}
	if inPort < 1 || inPort > 65535 {
		return nil, fmt.Errorf("nft listen port must be between 1 and 65535")
	}
	if targetPort < 1 || targetPort > 65535 {
		return nil, fmt.Errorf("nft target port must be between 1 and 65535")
	}
	if !targetHost.IsValid() || targetHost.Zone() != "" {
		return nil, fmt.Errorf("invalid nft target address")
	}
	// family 由实际目标地址推导，不信任调用方传入的值，避免 IPv4/IPv6 误配生成错误 nfproto。
	family = addressFamily(targetHost)
	return []string{
		buildPreroutingRule(family, protocol, inPort, targetHost, targetPort, commentBase),
		buildForwardUpRule(family, protocol, inPort, targetPort, targetHost, commentBase, withCounterComment),
		buildForwardDownRule(family, protocol, inPort, targetPort, targetHost, commentBase, withCounterComment),
		buildPostroutingRule(family, protocol, targetHost, targetPort, commentBase),
	}, nil
}

func buildPreroutingRule(family, protocol string, inPort int, targetHost netip.Addr, targetPort int, commentBase string) string {
	target := formatTarget(targetHost, targetPort)
	comment := ""
	if commentBase != "" {
		comment = fmt.Sprintf(" comment \"%s\"", commentBase)
	}
	return fmt.Sprintf("add rule %s prerouting meta nfproto %s %s dport %d dnat to %s%s",
		nftTable, family, protocol, inPort, target, comment)
}

func buildForwardUpRule(family, protocol string, inPort, targetPort int, targetHost netip.Addr, commentBase string, withCounterComment bool) string {
	addressField := resolveAddressField(family)
	verdict := buildCounterComment(commentBase, "up", withCounterComment)
	// DNAT 后当前目的端口已经变成 targetPort；必须同时匹配连接跟踪中的原始
	// 入口端口，避免不同公网入口转发到同一目标时被第一条 accept 规则误归属。
	return fmt.Sprintf("add rule %s forward meta nfproto %s %s dport %d %s daddr %s ct original proto-dst %d ct state new,established,related%s",
		nftTable, family, protocol, targetPort, addressField, targetHost.String(), inPort, verdict)
}

func buildForwardDownRule(family, protocol string, inPort, targetPort int, targetHost netip.Addr, commentBase string, withCounterComment bool) string {
	addressField := resolveAddressField(family)
	verdict := buildCounterComment(commentBase, "down", withCounterComment)
	// 回复方向仍可读取 conntrack 原始方向的目的端口，因此与上行使用同一
	// inPort 身份，确保上下行计数都落到正确的转发、用户和隧道。
	return fmt.Sprintf("add rule %s forward meta nfproto %s %s sport %d %s saddr %s ct original proto-dst %d ct state established,related%s",
		nftTable, family, protocol, targetPort, addressField, targetHost.String(), inPort, verdict)
}

func buildPostroutingRule(family, protocol string, targetHost netip.Addr, targetPort int, commentBase string) string {
	addressField := resolveAddressField(family)
	comment := ""
	if commentBase != "" {
		comment = fmt.Sprintf(" comment \"%s\"", commentBase)
	}
	return fmt.Sprintf("add rule %s postrouting meta nfproto %s %s dport %d %s daddr %s masquerade%s",
		nftTable, family, protocol, targetPort, addressField, targetHost.String(), comment)
}

func buildCounterComment(commentBase, suffix string, withCounterComment bool) string {
	if commentBase == "" {
		return " accept"
	}
	if !withCounterComment {
		return fmt.Sprintf(" accept comment \"%s\"", commentBase)
	}
	return fmt.Sprintf(" counter accept comment \"%s:%s\"", commentBase, suffix)
}

func resolveAddressField(family string) string {
	if strings.EqualFold(family, "ipv6") {
		return "ip6"
	}
	return "ip"
}

func formatTarget(host netip.Addr, port int) string {
	return net.JoinHostPort(host.String(), strconv.Itoa(port))
}

func addressFamily(addr netip.Addr) string {
	if addr.Is6() {
		return "ipv6"
	}
	return "ipv4"
}
