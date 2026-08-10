package gost

import (
	"testing"
)

func TestParseNftRule(t *testing.T) {
	tests := []struct {
		name        string
		rule        string
		wantErr     bool
		expectPort  int
		expectHost  string
		expectProto string
	}{
		{
			name:        "IPv4 TCP with comment",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10001 dnat to 192.168.1.100:8080 comment "fp:123:1:5"`,
			wantErr:     false,
			expectPort:  10001,
			expectHost:  "192.168.1.100",
			expectProto: "tcp",
		},
		{
			name:        "IPv4 UDP without comment",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv4 udp dport 10002 dnat to 192.168.1.101:9090`,
			wantErr:     false,
			expectPort:  10002,
			expectHost:  "192.168.1.101",
			expectProto: "udp",
		},
		{
			name:        "IPv6 with brackets",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv6 tcp dport 10003 dnat to [2001:db8::1]:8080 comment "fp:456:2:10"`,
			wantErr:     false,
			expectPort:  10003,
			expectHost:  "2001:db8::1",
			expectProto: "tcp",
		},
		{
			name:        "Domain name",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10004 dnat to example.com:443`,
			wantErr:     false,
			expectPort:  10004,
			expectHost:  "example.com",
			expectProto: "tcp",
		},
		{
			name:        "nft list output with dnat ip to",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10005 dnat ip to 192.168.1.102:8443 # handle 22`,
			wantErr:     false,
			expectPort:  10005,
			expectHost:  "192.168.1.102",
			expectProto: "tcp",
		},
		{
			name:        "nft list output with dnat ip6 to",
			rule:        `add rule inet flux_panel prerouting meta nfproto ipv6 udp dport 10006 dnat ip6 to [2001:db8::2]:5353 # handle 23`,
			wantErr:     false,
			expectPort:  10006,
			expectHost:  "2001:db8::2",
			expectProto: "udp",
		},
		{
			name:        "nft list output without meta nfproto",
			rule:        `add rule inet flux_panel prerouting tcp dport 10007 dnat ip to 10.0.0.2:20001 comment "fp:9:1:1" # handle 24`,
			wantErr:     false,
			expectPort:  10007,
			expectHost:  "10.0.0.2",
			expectProto: "tcp",
		},
		{
			name:        "nft list output ipv6 without meta nfproto",
			rule:        `add rule inet flux_panel prerouting udp dport 10008 dnat ip6 to [2001:db8::3]:20002 # handle 25`,
			wantErr:     false,
			expectPort:  10008,
			expectHost:  "2001:db8::3",
			expectProto: "udp",
		},
		{
			name:    "Non-prerouting rule",
			rule:    `add rule inet flux_panel forward meta nfproto ipv4 tcp dport 8080 accept`,
			wantErr: true,
		},
		{
			name:    "Invalid format",
			rule:    `some invalid rule text`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseNftRule(tt.rule)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseNftRule() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ParseNftRule() unexpected error: %v", err)
				return
			}

			if parsed.InPort != tt.expectPort {
				t.Errorf("InPort = %d, want %d", parsed.InPort, tt.expectPort)
			}

			if parsed.TargetHost != tt.expectHost {
				t.Errorf("TargetHost = %s, want %s", parsed.TargetHost, tt.expectHost)
			}

			if parsed.Protocol != tt.expectProto {
				t.Errorf("Protocol = %s, want %s", parsed.Protocol, tt.expectProto)
			}
		})
	}
}

func TestParseNftRuleWithComment(t *testing.T) {
	rule := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10001 dnat to 192.168.1.100:8080 comment "fp:123:456:789"`

	parsed, err := ParseNftRule(rule)
	if err != nil {
		t.Fatalf("ParseNftRule() error: %v", err)
	}

	if parsed.ForwardID != 123 {
		t.Errorf("ForwardID = %d, want 123", parsed.ForwardID)
	}

	if parsed.UserID != 456 {
		t.Errorf("UserID = %d, want 456", parsed.UserID)
	}

	if parsed.UserTunnelID != 789 {
		t.Errorf("UserTunnelID = %d, want 789", parsed.UserTunnelID)
	}
}

func TestParseNftRuleWithQuotedCommentValue(t *testing.T) {
	rule := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 32002 dnat ip to 10.42.1.123:32001 comment '"fp:999999:1:1"' # handle 10`

	parsed, err := ParseNftRule(rule)
	if err != nil {
		t.Fatalf("ParseNftRule() error: %v", err)
	}

	if parsed.ForwardID != 999999 {
		t.Errorf("ForwardID = %d, want 999999", parsed.ForwardID)
	}
	if parsed.UserID != 1 {
		t.Errorf("UserID = %d, want 1", parsed.UserID)
	}
	if parsed.UserTunnelID != 1 {
		t.Errorf("UserTunnelID = %d, want 1", parsed.UserTunnelID)
	}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{
			name:     "IPv4 with port",
			target:   "192.168.1.100:8080",
			wantHost: "192.168.1.100",
			wantPort: 8080,
			wantErr:  false,
		},
		{
			name:     "IPv6 with brackets",
			target:   "[2001:db8::1]:8080",
			wantHost: "2001:db8::1",
			wantPort: 8080,
			wantErr:  false,
		},
		{
			name:     "Domain with port",
			target:   "example.com:443",
			wantHost: "example.com",
			wantPort: 443,
			wantErr:  false,
		},
		{
			name:    "Missing port",
			target:  "192.168.1.100",
			wantErr: true,
		},
		{
			name:    "Invalid IPv6 format",
			target:  "[2001:db8::1:8080",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseTarget(tt.target)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseTarget() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("parseTarget() unexpected error: %v", err)
				return
			}

			if host != tt.wantHost {
				t.Errorf("host = %s, want %s", host, tt.wantHost)
			}

			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

func TestBuildRuleKey(t *testing.T) {
	parsed := &ParsedNftRule{
		Protocol:   "tcp",
		InPort:     10001,
		TargetHost: "192.168.1.100",
		OutPort:    8080,
	}

	key := parsed.BuildRuleKey()
	expected := "tcp:10001:192.168.1.100:8080"

	if key != expected {
		t.Errorf("BuildRuleKey() = %s, want %s", key, expected)
	}
}

func TestParseNftRules(t *testing.T) {
	rules := []string{
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 10001 dnat to 192.168.1.100:8080 comment "fp:123:1:5"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 8080 accept`, // 应该被忽略
		`add rule inet flux_panel prerouting meta nfproto ipv4 udp dport 10002 dnat to 192.168.1.101:9090`,
		`invalid rule`, // 应该被忽略
	}

	parsed := ParseNftRules(rules)

	// 应该只解析出 2 条 prerouting DNAT 规则
	if len(parsed) != 2 {
		t.Errorf("ParseNftRules() returned %d rules, want 2", len(parsed))
	}

	// 检查第一条规则
	if parsed[0].InPort != 10001 {
		t.Errorf("First rule InPort = %d, want 10001", parsed[0].InPort)
	}

	// 检查第二条规则
	if parsed[1].InPort != 10002 {
		t.Errorf("Second rule InPort = %d, want 10002", parsed[1].InPort)
	}
}
