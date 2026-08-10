package gost

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParsedNftRule 解析后的 nftables 规则
type ParsedNftRule struct {
	InPort       int    // 入口端口
	OutPort      int    // 出口端口
	TargetHost   string // 目标主机
	Protocol     string // tcp/udp
	Family       string // ipv4/ipv6
	Comment      string // 完整注释
	ForwardID    int64  // 从注释解析的 forwardId (0 表示无注释)
	UserID       int64  // 从注释解析的 userId
	UserTunnelID int64  // 从注释解析的 userTunnelId
	RawRule      string // 原始规则
}

// NFT 规则正则表达式
var (
	// prerouting 规则：提取协议、端口、目标地址。
	// 示例：add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10001 dnat to 192.168.1.100:8080 comment "fp:123:1:5"
	// nft list output may omit "meta nfproto ..." and include "dnat ip to" / "dnat ip6 to".
	familyRegex       = regexp.MustCompile(`\bmeta\s+nfproto\s+(ipv4|ipv6)\b`)
	protocolPortRegex = regexp.MustCompile(`\b(tcp|udp)\s+dport\s+(\d+)\b`)
	dnatTargetRegex   = regexp.MustCompile(`\bdnat\s+(?:(ip6?)\s+)?to\s+([^\s]+)`)

	// 注释提取：fp:forwardId:userId:userTunnelId。
	// nft list output may preserve extra quotes/escapes around the comment value,
	// so match the marker anywhere in the rule text.
	commentRegex = regexp.MustCompile(`fp:(\d+):(\d+):(\d+)`)
)

// ParseNftRule 解析单条 nftables 规则
func ParseNftRule(rule string) (*ParsedNftRule, error) {
	// 只解析 prerouting DNAT 规则（其他规则如 forward、postrouting 是辅助规则）
	if !strings.Contains(rule, "prerouting") || !strings.Contains(rule, "dnat") {
		return nil, fmt.Errorf("不是 DNAT prerouting 规则")
	}

	protocolMatches := protocolPortRegex.FindStringSubmatch(rule)
	if len(protocolMatches) < 3 {
		return nil, fmt.Errorf("无法解析规则格式")
	}

	protocol := protocolMatches[1]  // tcp 或 udp
	inPortStr := protocolMatches[2] // 入口端口

	dnatMatches := dnatTargetRegex.FindStringSubmatch(rule)
	if len(dnatMatches) < 3 {
		return nil, fmt.Errorf("无法解析规则格式")
	}
	dnatFamily := dnatMatches[1] // ip 或 ip6，可能为空
	dnatTarget := dnatMatches[2] // 目标地址，格式：host:port 或 [host]:port

	inPort, err := strconv.Atoi(inPortStr)
	if err != nil {
		return nil, fmt.Errorf("无效的端口: %s", inPortStr)
	}

	// 解析目标地址和端口
	targetHost, outPort, err := parseTarget(dnatTarget)
	if err != nil {
		return nil, err
	}
	family := inferNftRuleFamily(rule, dnatFamily, targetHost)

	parsed := &ParsedNftRule{
		InPort:     inPort,
		OutPort:    outPort,
		TargetHost: targetHost,
		Protocol:   protocol,
		Family:     family,
		RawRule:    rule,
	}

	// 提取注释中的 forwardId、userId、userTunnelId
	commentMatches := commentRegex.FindStringSubmatch(rule)
	if len(commentMatches) >= 4 {
		parsed.Comment = commentMatches[0]
		if fid, err := strconv.ParseInt(commentMatches[1], 10, 64); err == nil {
			parsed.ForwardID = fid
		}
		if uid, err := strconv.ParseInt(commentMatches[2], 10, 64); err == nil {
			parsed.UserID = uid
		}
		if utid, err := strconv.ParseInt(commentMatches[3], 10, 64); err == nil {
			parsed.UserTunnelID = utid
		}
	}

	return parsed, nil
}

func inferNftRuleFamily(rule, dnatFamily, targetHost string) string {
	if matches := familyRegex.FindStringSubmatch(rule); len(matches) >= 2 {
		return matches[1]
	}
	if strings.EqualFold(dnatFamily, "ip6") {
		return "ipv6"
	}
	if strings.EqualFold(dnatFamily, "ip") {
		return "ipv4"
	}
	if strings.Contains(targetHost, ":") {
		return "ipv6"
	}
	return "ipv4"
}

// parseTarget 解析 DNAT 目标地址：host:port 或 [host]:port
func parseTarget(target string) (host string, port int, err error) {
	// IPv6 格式：[2001:db8::1]:8080
	if strings.HasPrefix(target, "[") {
		closeBracket := strings.Index(target, "]")
		if closeBracket == -1 {
			return "", 0, fmt.Errorf("无效的 IPv6 目标地址格式: %s", target)
		}
		host = target[1:closeBracket]
		portPart := target[closeBracket+1:]
		if !strings.HasPrefix(portPart, ":") {
			return "", 0, fmt.Errorf("缺少端口分隔符: %s", target)
		}
		portStr := portPart[1:]
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return "", 0, fmt.Errorf("无效的端口: %s", portStr)
		}
		return host, port, nil
	}

	// IPv4 格式：192.168.1.100:8080 或域名格式：example.com:8080
	lastColon := strings.LastIndex(target, ":")
	if lastColon == -1 {
		return "", 0, fmt.Errorf("缺少端口分隔符: %s", target)
	}
	host = target[:lastColon]
	portStr := target[lastColon+1:]
	port, err = strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("无效的端口: %s", portStr)
	}

	return host, port, nil
}

// ParseNftRules 批量解析 nftables 规则
func ParseNftRules(rules []string) []*ParsedNftRule {
	var parsed []*ParsedNftRule
	for _, rule := range rules {
		if p, err := ParseNftRule(rule); err == nil {
			parsed = append(parsed, p)
		}
		// 解析失败的规则静默忽略（可能是 forward、postrouting 等辅助规则）
	}
	return parsed
}

// BuildRuleKey 构建规则唯一键，用于去重和对比
func (p *ParsedNftRule) BuildRuleKey() string {
	return fmt.Sprintf("%s:%d:%s:%d", p.Protocol, p.InPort, p.TargetHost, p.OutPort)
}
