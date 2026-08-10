package service

import (
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestUDPFlagsAreRenderedForCompatibleClients(t *testing.T) {
	vless := renderNode{
		ProxyNode: model.ProxyNode{
			Name:        "VLESS-UDP",
			Protocol:    "vless",
			UUID:        "11111111-1111-1111-1111-111111111111",
			Security:    "reality",
			Network:     "tcp",
			SNI:         "www.example.com",
			PublicKey:   "public-key",
			ShortID:     "abcd",
			Fingerprint: "chrome",
			UDP:         1,
		},
		Address: "vless.example.com",
		Port:    443,
	}
	if udp, ok := clashProxyMap(vless)["udp"].(bool); !ok || !udp {
		t.Fatal("VLESS Clash/Shadowrocket 节点应显式启用 UDP")
	}
	if link := v2rayNVLESSLink(vless); !strings.Contains(link, "packetEncoding=none") {
		t.Fatalf("VLESS URI 缺少 UDP 包编码标记: %s", link)
	}

	snell := renderNode{
		ProxyNode: model.ProxyNode{
			Name:         "Snell-UDP",
			Protocol:     "snell",
			Password:     "secret",
			SnellVersion: 5,
			UDP:          1,
		},
		Address: "snell.example.com",
		Port:    44046,
	}
	if udp, ok := clashProxyMap(snell)["udp"].(bool); !ok || !udp {
		t.Fatal("Snell v3+ Clash/Shadowrocket 节点应显式启用 UDP")
	}

	legacySnell := snell
	legacySnell.SnellVersion = 2
	if udp, ok := clashProxyMap(legacySnell)["udp"].(bool); !ok || udp {
		t.Fatal("Snell v2 不应声明 UDP 支持")
	}
}

func TestSurgeExplicitlyEnablesOptionalUDPRelay(t *testing.T) {
	ss := renderNode{
		ProxyNode: model.ProxyNode{
			Name:     "SS-UDP",
			Protocol: "ss",
			Method:   "aes-256-gcm",
			Password: "secret",
			UDP:      1,
		},
		Address: "ss.example.com",
		Port:    8388,
	}
	if line := surgeProxyLine(ss); !strings.Contains(line, "udp-relay=true") {
		t.Fatalf("Surge Shadowsocks 节点缺少 udp-relay=true: %s", line)
	}

	ss.UDP = 0
	if line := surgeProxyLine(ss); strings.Contains(line, "udp-relay=true") {
		t.Fatalf("已禁用 UDP 的节点不应输出 udp-relay=true: %s", line)
	}
}
