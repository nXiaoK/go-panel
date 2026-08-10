package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

const testSingboxTemplate = `{
  "dns": {
    "servers": [
      {"type": "local", "tag": "dns-local"},
      {"type": "https", "tag": "dns-proxy", "server": "1.1.1.1", "detour": "proxy"}
    ],
    "final": "dns-proxy"
  },
  "inbounds": [
    {"type": "tun", "tag": "tun-in", "auto_route": true, "strict_route": true}
  ],
  "outbounds": [
    {
      "type": "shadowsocks",
      "tag": "proxy",
      "server": "203.0.113.1",
      "server_port": 12345,
      "method": "aes-256-gcm",
      "password": "CHANGE_ME"
    },
    {"type": "direct", "tag": "direct", "domain_resolver": "dns-local"}
  ],
  "route": {
    "rules": [
      {"port": 53, "action": "hijack-dns"},
      {"rule_set": "geosite-cn", "action": "route", "outbound": "direct"}
    ],
    "final": "proxy"
  }
}`

func TestRenderSingboxInjectsSupportedOutbounds(t *testing.T) {
	nodes := []renderNode{
		{
			ProxyNode: model.ProxyNode{
				Name:          "VLESS-Reality",
				Protocol:      "vless",
				Server:        "origin.example.com",
				Port:          443,
				UUID:          "11111111-1111-1111-1111-111111111111",
				SNI:           "edge.example.com",
				Security:      "reality",
				Flow:          "xtls-rprx-vision",
				PublicKey:     "reality-public-key",
				ShortID:       "0123456789abcdef",
				Fingerprint:   "chrome",
				AllowInsecure: 1,
			},
			Address: "relay.example.com",
			Port:    31001,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "VLESS-WS",
				Protocol: "vless",
				Server:   "vless-ws.example.com",
				Port:     443,
				UUID:     "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				SNI:      "vless-ws-sni.example.com",
				Network:  "ws",
				Security: "tls",
				Path:     "/websocket",
				Options:  `{"host":"ws.example.com"}`,
			},
			Address: "vless-ws.example.com",
			Port:    443,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "VMess-gRPC",
				Protocol: "vmess",
				Server:   "vmess.example.com",
				Port:     443,
				UUID:     "22222222-2222-2222-2222-222222222222",
				Method:   "auto",
				SNI:      "vmess-sni.example.com",
				Network:  "grpc",
				Security: "tls",
				Options:  `{"alterId":1,"serviceName":"grpc-service"}`,
			},
			Address: "vmess.example.com",
			Port:    443,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "VLESS-Plain-WS",
				Protocol: "vless",
				Server:   "plain-ws.example.com",
				Port:     80,
				UUID:     "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
				SNI:      "ws-host.example.com",
				Network:  "ws",
				Security: "none",
				Path:     "/plain",
			},
			Address: "plain-ws.example.com",
			Port:    80,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "Trojan",
				Protocol: "trojan",
				Server:   "trojan.example.com",
				Port:     443,
				Password: "trojan-password",
				SNI:      "trojan-sni.example.com",
			},
			Address: "trojan.example.com",
			Port:    443,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "SS",
				Protocol: "shadowsocks",
				Server:   "198.51.100.8",
				Port:     8388,
				Method:   "aes-256-gcm",
				Password: "ss-password",
			},
			Address: "198.51.100.8",
			Port:    8388,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "SOCKS",
				Protocol: "socks",
				Server:   "socks.example.com",
				Port:     1080,
				Username: "socks-user",
				Password: "socks-password",
			},
			Address: "socks.example.com",
			Port:    1080,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:         "Unsupported-Snell",
				Protocol:     "snell",
				Server:       "snell.example.com",
				Port:         443,
				Password:     "snell-psk",
				SnellVersion: 5,
			},
			Address: "snell.example.com",
			Port:    443,
		},
		{
			ProxyNode: model.ProxyNode{
				Name:     "Unsupported-XHTTP",
				Protocol: "vless",
				Server:   "xhttp.example.com",
				Port:     443,
				UUID:     "cccccccc-cccc-cccc-cccc-cccccccccccc",
				Network:  "xhttp",
				Security: "tls",
			},
			Address: "xhttp.example.com",
			Port:    443,
		},
	}

	root := decodeRenderedSingbox(t, mustRenderSingbox(t, testSingboxTemplate, nodes))
	selector := findSingboxOutbound(t, root, "proxy")
	if selector["type"] != "selector" {
		t.Fatalf("proxy outbound type = %v, want selector", selector["type"])
	}
	wantMembers := []string{"VLESS-Reality", "VLESS-WS", "VMess-gRPC", "VLESS-Plain-WS", "Trojan", "SS", "SOCKS"}
	if got := singboxStringList(selector["outbounds"]); !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("selector members = %#v, want %#v", got, wantMembers)
	}
	if findSingboxOutboundOrNil(root, "Unsupported-Snell") != nil {
		t.Fatal("Snell must not be emitted because sing-box 1.13 has no Snell outbound")
	}
	if findSingboxOutboundOrNil(root, "Unsupported-XHTTP") != nil {
		t.Fatal("XHTTP must not be silently downgraded to a TCP outbound")
	}
	if direct := findSingboxOutbound(t, root, "direct"); direct["domain_resolver"] != "dns-local" {
		t.Fatalf("direct outbound was not preserved: %#v", direct)
	}

	dns := singboxObject(t, root["dns"], "dns")
	if dns["final"] != "dns-proxy" {
		t.Fatalf("dns final was not preserved: %#v", dns)
	}
	route := singboxObject(t, root["route"], "route")
	if route["final"] != "proxy" || len(singboxList(t, route["rules"], "route.rules")) != 2 {
		t.Fatalf("route was not preserved: %#v", route)
	}

	vless := findSingboxOutbound(t, root, "VLESS-Reality")
	if vless["type"] != "vless" || vless["server"] != "relay.example.com" || vless["server_port"] != float64(31001) {
		t.Fatalf("unexpected VLESS endpoint: %#v", vless)
	}
	if vless["uuid"] != "11111111-1111-1111-1111-111111111111" || vless["flow"] != "xtls-rprx-vision" {
		t.Fatalf("important VLESS fields missing: %#v", vless)
	}
	if vless["domain_resolver"] != "dns-local" {
		t.Fatalf("hostname proxy must use local bootstrap resolver: %#v", vless)
	}
	tls := singboxObject(t, vless["tls"], "vless.tls")
	if tls["enabled"] != true || tls["server_name"] != "edge.example.com" || tls["insecure"] != true {
		t.Fatalf("unexpected VLESS TLS: %#v", tls)
	}
	reality := singboxObject(t, tls["reality"], "vless.tls.reality")
	if reality["enabled"] != true || reality["public_key"] != "reality-public-key" || reality["short_id"] != "0123456789abcdef" {
		t.Fatalf("unexpected Reality fields: %#v", reality)
	}
	utls := singboxObject(t, tls["utls"], "vless.tls.utls")
	if utls["enabled"] != true || utls["fingerprint"] != "chrome" {
		t.Fatalf("unexpected uTLS fields: %#v", utls)
	}
	vlessWS := findSingboxOutbound(t, root, "VLESS-WS")
	ws := singboxObject(t, vlessWS["transport"], "vless-ws.transport")
	if ws["type"] != "ws" || ws["path"] != "/websocket" {
		t.Fatalf("unexpected WebSocket transport: %#v", ws)
	}
	if headers := singboxObject(t, ws["headers"], "vless.transport.headers"); headers["Host"] != "ws.example.com" {
		t.Fatalf("unexpected WebSocket headers: %#v", headers)
	}

	vmess := findSingboxOutbound(t, root, "VMess-gRPC")
	if vmess["type"] != "vmess" || vmess["security"] != "auto" || vmess["alter_id"] != float64(1) {
		t.Fatalf("unexpected VMess fields: %#v", vmess)
	}
	grpc := singboxObject(t, vmess["transport"], "vmess.transport")
	if grpc["type"] != "grpc" || grpc["service_name"] != "grpc-service" {
		t.Fatalf("unexpected gRPC transport: %#v", grpc)
	}

	plainWS := findSingboxOutbound(t, root, "VLESS-Plain-WS")
	if _, exists := plainWS["tls"]; exists {
		t.Fatalf("a plaintext WebSocket host must not enable TLS: %#v", plainWS)
	}
	plainTransport := singboxObject(t, plainWS["transport"], "plain-ws.transport")
	if headers := singboxObject(t, plainTransport["headers"], "plain-ws.headers"); headers["Host"] != "ws-host.example.com" {
		t.Fatalf("plaintext WebSocket Host header was not preserved: %#v", headers)
	}

	trojan := findSingboxOutbound(t, root, "Trojan")
	if trojan["type"] != "trojan" || trojan["password"] != "trojan-password" {
		t.Fatalf("unexpected Trojan fields: %#v", trojan)
	}
	if trojanTLS := singboxObject(t, trojan["tls"], "trojan.tls"); trojanTLS["enabled"] != true || trojanTLS["server_name"] != "trojan-sni.example.com" {
		t.Fatalf("unexpected Trojan TLS: %#v", trojanTLS)
	}

	ss := findSingboxOutbound(t, root, "SS")
	if ss["type"] != "shadowsocks" || ss["method"] != "aes-256-gcm" || ss["password"] != "ss-password" {
		t.Fatalf("unexpected Shadowsocks fields: %#v", ss)
	}
	if _, exists := ss["domain_resolver"]; exists {
		t.Fatalf("IP endpoint should not need a bootstrap resolver: %#v", ss)
	}

	socks := findSingboxOutbound(t, root, "SOCKS")
	if socks["type"] != "socks" || socks["version"] != "5" || socks["username"] != "socks-user" || socks["password"] != "socks-password" {
		t.Fatalf("unexpected SOCKS fields: %#v", socks)
	}
}

func TestRenderSingboxInvalidTemplateFallsBackWithoutSampleCredentials(t *testing.T) {
	node := renderNode{
		ProxyNode: model.ProxyNode{
			Name:     "Fallback-SS",
			Protocol: "ss",
			Server:   "198.51.100.23",
			Port:     8388,
			Method:   "aes-256-gcm",
			Password: "generated-password",
		},
		Address: "198.51.100.23",
		Port:    8388,
	}
	body := mustRenderSingbox(t, `{"outbounds":`, []renderNode{node})
	checkRenderedSingboxWithOfficialBinary(t, body)
	root := decodeRenderedSingbox(t, body)
	if strings.Contains(body, "CHANGE_ME") || strings.Contains(body, "203.0.113.1") {
		t.Fatalf("fallback leaked sample proxy credentials or endpoint:\n%s", body)
	}
	if findSingboxOutbound(t, root, node.Name)["password"] != node.Password {
		t.Fatalf("generated node was not injected into fallback:\n%s", body)
	}
	if root["dns"] == nil || root["route"] == nil || root["inbounds"] == nil {
		t.Fatalf("privacy routing sections were not preserved from the safe default:\n%s", body)
	}
}

func TestRenderSingboxFailsClosedWithoutCompatibleNodes(t *testing.T) {
	xhttp := renderNode{
		ProxyNode: model.ProxyNode{
			Name:     "XHTTP-only",
			Protocol: "vless",
			Server:   "xhttp.example.com",
			Port:     443,
			UUID:     "dddddddd-dddd-dddd-dddd-dddddddddddd",
			Network:  "xhttp",
			Security: "tls",
		},
		Address: "xhttp.example.com",
		Port:    443,
	}
	for name, nodes := range map[string][]renderNode{
		"empty":       nil,
		"unsupported": {xhttp},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := renderSingbox(testSingboxTemplate, nodes); err == nil || !strings.Contains(err.Error(), "没有可供 sing-box 使用的节点") {
				t.Fatalf("renderSingbox() error = %v", err)
			}
		})
	}
}

func TestRenderSingboxReplacesOnlyDynamicTemplateNodesAndRepairsReferences(t *testing.T) {
	template := `{
  "dns": {
    "servers": [
      {"type": "local"},
      {"type": "https", "tag": "dns-proxy", "server": "1.1.1.1", "detour": "Old-Dynamic"}
    ]
  },
  "outbounds": [
    {"type": "selector", "tag": "proxy", "outbounds": ["Old-Dynamic", "direct"]},
    {"type": "vless", "tag": "Old-Dynamic", "server": "old.example.com", "server_port": 443, "uuid": "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"},
    {"type": "shadowsocks", "tag": "Custom", "server": "198.51.100.99", "server_port": 8388, "method": "aes-256-gcm", "password": "custom-password"},
    {"type": "selector", "tag": "Manual", "outbounds": ["Old-Dynamic", "Custom"], "default": "Old-Dynamic"},
    {"type": "socks", "tag": "direct", "server": "127.0.0.1", "server_port": 1080}
  ],
  "route": {
    "rules": [{"domain_suffix": ".example", "action": "route", "outbound": "Old-Dynamic"}],
    "rule_set": [{"type": "remote", "tag": "test", "format": "binary", "url": "https://example.com/test.srs", "download_detour": "Old-Dynamic"}],
    "final": "Old-Dynamic"
  }
}`
	node := renderNode{
		ProxyNode: model.ProxyNode{
			Name:     "Fresh",
			Protocol: "ss",
			Server:   "fresh.example.com",
			Port:     8388,
			Method:   "aes-256-gcm",
			Password: "fresh-password",
		},
		Address: "fresh.example.com",
		Port:    8388,
	}
	body := mustRenderSingbox(t, template, []renderNode{node})
	root := decodeRenderedSingbox(t, body)
	if findSingboxOutboundOrNil(root, "Old-Dynamic") != nil {
		t.Fatal("stale dynamic outbound was not removed")
	}
	if findSingboxOutboundOrNil(root, "Custom") == nil {
		t.Fatal("unrelated custom outbound must be preserved")
	}
	if strings.Contains(body, `"domain_resolver": "<nil>"`) {
		t.Fatalf("untagged local DNS produced an invalid resolver reference:\n%s", body)
	}
	if _, exists := findSingboxOutbound(t, root, "Fresh")["domain_resolver"]; exists {
		t.Fatalf("untagged local DNS must not be referenced by generated nodes:\n%s", body)
	}
	manual := findSingboxOutbound(t, root, "Manual")
	if got := singboxStringList(manual["outbounds"]); !reflect.DeepEqual(got, []string{"proxy", "Custom"}) || manual["default"] != "proxy" {
		t.Fatalf("stale selector references were not repaired: %#v", manual)
	}
	dns := singboxObject(t, root["dns"], "dns")
	servers := singboxList(t, dns["servers"], "dns.servers")
	if singboxObject(t, servers[1], "dns.servers[1]")["detour"] != "proxy" {
		t.Fatalf("stale DNS detour was not repaired: %#v", servers[1])
	}
	route := singboxObject(t, root["route"], "route")
	if route["final"] != "proxy" {
		t.Fatalf("stale route final was not repaired: %#v", route)
	}
	rules := singboxList(t, route["rules"], "route.rules")
	if singboxObject(t, rules[0], "route.rules[0]")["outbound"] != "proxy" {
		t.Fatalf("stale route rule was not repaired: %#v", rules[0])
	}
	ruleSets := singboxList(t, route["rule_set"], "route.rule_set")
	if singboxObject(t, ruleSets[0], "route.rule_set[0]")["download_detour"] != "proxy" {
		t.Fatalf("stale rule-set detour was not repaired: %#v", ruleSets[0])
	}
	directCount := 0
	for _, raw := range singboxList(t, root["outbounds"], "outbounds") {
		outbound := singboxObject(t, raw, "outbound")
		if outbound["tag"] == "direct" {
			directCount++
			if outbound["type"] != "direct" {
				t.Fatalf("reserved direct tag kept the wrong outbound: %#v", outbound)
			}
		}
	}
	if directCount != 1 {
		t.Fatalf("direct outbound count = %d, want 1", directCount)
	}
}

func TestRenderSubscriptionSingboxUsesForwardAddress(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	forwardID := int64(41)
	node := model.ProxyNode{
		ExternalID:  "singbox-forward-node",
		Name:        "Forwarded-VLESS",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		UUID:        "33333333-3333-3333-3333-333333333333",
		SNI:         "origin.example.com",
		Security:    "tls",
		ForwardID:   &forwardID,
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:            "singbox-forward-profile",
		Token:           "token-singbox-forward",
		DefaultFormat:   "singbox",
		SurgeTemplate:   fallbackSurgeTemplate,
		ClashTemplate:   fallbackClashTemplate,
		SingboxTemplate: testSingboxTemplate,
		Status:          1,
		CreatedTime:     now,
		UpdatedTime:     now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Tunnel{
		ID: 17, Name: "singbox-relay", InIP: "relay.example.com", InNodeID: 1, OutNodeID: 2,
		Type: 1, Flow: 1, Status: tunnelStatusActive, CreatedTime: now, UpdatedTime: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Forward{
		ID: forwardID, UserID: 1, UserName: "admin", Name: "singbox-forward", TunnelID: 17,
		InPort: 32001, RemoteAddr: "origin.example.com:443", Strategy: "fifo",
		Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	body, contentType, err := RenderSubscription(profile.Token, "singbox")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	root := decodeRenderedSingbox(t, body)
	outbound := findSingboxOutbound(t, root, node.Name)
	if outbound["server"] != "relay.example.com" || outbound["server_port"] != float64(32001) {
		t.Fatalf("rendered endpoint did not use active forward: %#v", outbound)
	}
	selector := findSingboxOutbound(t, root, "proxy")
	if got := singboxStringList(selector["outbounds"]); !reflect.DeepEqual(got, []string{node.Name}) {
		t.Fatalf("selector members = %#v", got)
	}
}

func mustRenderSingbox(t *testing.T, template string, nodes []renderNode) string {
	t.Helper()
	body, err := renderSingbox(template, nodes)
	if err != nil {
		t.Fatalf("render sing-box: %v", err)
	}
	return body
}

func checkRenderedSingboxWithOfficialBinary(t *testing.T, body string) {
	t.Helper()
	bin := strings.TrimSpace(os.Getenv("SINGBOX_CHECK_BIN"))
	if bin == "" {
		return
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write sing-box config: %v", err)
	}
	if output, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, output)
	}
}

func decodeRenderedSingbox(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("rendered sing-box config is not strict JSON: %v\n%s", err, body)
	}
	return root
}

func findSingboxOutbound(t *testing.T, root map[string]interface{}, tag string) map[string]interface{} {
	t.Helper()
	outbound := findSingboxOutboundOrNil(root, tag)
	if outbound == nil {
		t.Fatalf("outbound %q not found in %#v", tag, root["outbounds"])
	}
	return outbound
}

func findSingboxOutboundOrNil(root map[string]interface{}, tag string) map[string]interface{} {
	outbounds, _ := root["outbounds"].([]interface{})
	for _, raw := range outbounds {
		outbound, _ := raw.(map[string]interface{})
		if outbound != nil && outbound["tag"] == tag {
			return outbound
		}
	}
	return nil
}

func singboxObject(t *testing.T, raw interface{}, name string) map[string]interface{} {
	t.Helper()
	value, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("%s = %#v, want object", name, raw)
	}
	return value
}

func singboxList(t *testing.T, raw interface{}, name string) []interface{} {
	t.Helper()
	value, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("%s = %#v, want array", name, raw)
	}
	return value
}

func singboxStringList(raw interface{}) []string {
	items, _ := raw.([]interface{})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}
