package gost

import (
	"net/netip"
	"strings"
	"testing"
)

// 移植自 Java NftForwardRuleUtilTests

func TestBuildCommentBase(t *testing.T) {
	if got := BuildCommentBase(11, 22, 33); got != "fp:11:22:33" {
		t.Fatalf("BuildCommentBase = %s", got)
	}
}

func TestBuildEntryRules(t *testing.T) {
	rules, err := BuildEntryRules(11, 22, 33, "ipv4", "tcp", 8080, netip.MustParseAddr("10.0.0.8"), 9090)
	rules = mustBuildNftRules(t, rules, err)
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(rules))
	}
	checkContains(t, rules[0], "prerouting", `comment "fp:11:22:33"`, "dnat to 10.0.0.8:9090")
	checkContains(t, rules[1], "forward", "counter", `comment "fp:11:22:33:up"`, "dport 9090", "ct original proto-dst 8080")
	checkContains(t, rules[2], "forward", "counter", `comment "fp:11:22:33:down"`, "sport 9090", "ct original proto-dst 8080")
	checkContains(t, rules[3], "postrouting", "masquerade", `comment "fp:11:22:33"`)
}

func TestBuildEntryRulesKeepSameTargetDistinctByOriginalPort(t *testing.T) {
	target := netip.MustParseAddr("10.0.0.8")
	first, err := BuildEntryRules(11, 22, 33, "ipv4", "tcp", 18080, target, 9090)
	first = mustBuildNftRules(t, first, err)
	second, err := BuildEntryRules(11, 22, 33, "ipv4", "tcp", 28080, target, 9090)
	second = mustBuildNftRules(t, second, err)

	for _, directionIndex := range []int{1, 2} {
		if first[directionIndex] == second[directionIndex] {
			t.Fatalf("不同入口端口的 forward 规则不应相同: %s", first[directionIndex])
		}
		checkContains(t, first[directionIndex], "ct original proto-dst 18080")
		checkContains(t, second[directionIndex], "ct original proto-dst 28080")
	}
}

func TestBuildIpv6PreroutingRule(t *testing.T) {
	rules, err := BuildEntryRules(11, 22, 33, "ipv6", "udp", 53, netip.MustParseAddr("2001:db8::10"), 53)
	rules = mustBuildNftRules(t, rules, err)
	checkContains(t, rules[0], "prerouting", `comment "fp:11:22:33"`, "[2001:db8::10]:53")
	checkContains(t, rules[1], "ip6 daddr")
}

func TestBuildExitRules(t *testing.T) {
	rules, err := BuildExitRules("ipv4", "tcp", 8080, netip.MustParseAddr("10.0.0.8"), 9090)
	rules = mustBuildNftRules(t, rules, err)
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(rules))
	}
	for _, r := range rules {
		if strings.Contains(r, "counter comment") {
			t.Fatalf("exit rule should not have counter comment: %s", r)
		}
	}
	checkContains(t, rules[1], "forward", "dport 9090", "ct original proto-dst 8080")
	checkContains(t, rules[2], "forward", "sport 9090", "ct original proto-dst 8080")
}

func TestBuildExitRulesWithComment(t *testing.T) {
	rules, err := BuildExitRulesWithComment(11, 22, 33, "ipv4", "tcp", 8080, netip.MustParseAddr("10.0.0.8"), 9090)
	rules = mustBuildNftRules(t, rules, err)
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(rules))
	}
	for _, r := range rules {
		if strings.Contains(r, "counter") {
			t.Fatalf("exit rule should not have counter: %s", r)
		}
		checkContains(t, r, `comment "fp:11:22:33"`)
	}
}

func TestBuildRulesRejectInvalidProtocolAndPorts(t *testing.T) {
	addr := netip.MustParseAddr("192.0.2.1")
	tests := []struct {
		name       string
		protocol   string
		listenPort int
		targetPort int
	}{
		{name: "empty protocol", protocol: "", listenPort: 80, targetPort: 443},
		{name: "legacy protocol", protocol: "icmp", listenPort: 80, targetPort: 443},
		{name: "negative listen", protocol: "tcp", listenPort: -1, targetPort: 443},
		{name: "zero listen", protocol: "tcp", listenPort: 0, targetPort: 443},
		{name: "large listen", protocol: "tcp", listenPort: 65536, targetPort: 443},
		{name: "negative target", protocol: "tcp", listenPort: 80, targetPort: -1},
		{name: "zero target", protocol: "tcp", listenPort: 80, targetPort: 0},
		{name: "large target", protocol: "tcp", listenPort: 80, targetPort: 65536},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := BuildEntryRules(1, 2, 3, "ipv4", tt.protocol, tt.listenPort, addr, tt.targetPort)
			if err == nil {
				t.Fatalf("BuildEntryRules returned nil error and rules %v", rules)
			}
			if len(rules) != 0 {
				t.Fatalf("BuildEntryRules returned invalid rules %v", rules)
			}
		})
	}
}

func mustBuildNftRules(t *testing.T, rules []string, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatalf("build nft rules: %v", err)
	}
	return rules
}

func checkContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Fatalf("rule %q missing %q", s, sub)
		}
	}
}
