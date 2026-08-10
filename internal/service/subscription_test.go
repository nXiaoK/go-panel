package service

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

func TestRenderSurgeUsesForwardAddress(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	forwardID := int64(9)
	node := model.ProxyNode{
		ExternalID:  "node-1",
		Name:        "HK-Test",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		UUID:        "11111111-1111-1111-1111-111111111111",
		SNI:         "sni.example.com",
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
		Name:          "test",
		Token:         "token-surge",
		DefaultFormat: "surge",
		SurgeTemplate: "[General]\n\n[Proxy]\nold = direct\n\n[Proxy Group]\nProxy = select, include-all-proxies=1\n",
		ClashTemplate: fallbackClashTemplate,
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Tunnel{ID: 7, Name: "t", InIP: "relay.example.com", InNodeID: 1, OutNodeID: 2, Type: 1, Flow: 1, Status: tunnelStatusActive, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Forward{ID: forwardID, UserID: 1, UserName: "admin", Name: "f", TunnelID: 7, InPort: 31001, RemoteAddr: "origin.example.com:443", Strategy: "fifo", Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}

	body, _, err := RenderSubscription("token-surge", "surge")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "HK-Test = vless, relay.example.com, 31001") {
		t.Fatalf("expected forward address in surge output, got:\n%s", body)
	}
	if strings.Contains(body, "old = direct") {
		t.Fatalf("expected template proxy block replaced, got:\n%s", body)
	}
}

func TestRenderClashFallbackTemplate(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	node := model.ProxyNode{
		ExternalID:  "node-2",
		Name:        "JP-SS",
		Protocol:    "ss",
		Server:      "jp.example.com",
		Port:        8388,
		Method:      "2022-blake3-aes-128-gcm",
		Password:    "secret",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:          "test",
		Token:         "token-clash",
		DefaultFormat: "clash",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: "[General]\nthis is not clash\n",
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	body, _, err := RenderSubscription("token-clash", "clash")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"proxies:", "name: JP-SS", "type: ss", "cipher: 2022-blake3-aes-128-gcm"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in clash output, got:\n%s", want, body)
		}
	}
}

func TestRenderClashUsesProjectTemplate(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	node := model.ProxyNode{
		ExternalID:  "node-3",
		Name:        "GreenCloud-HK",
		Protocol:    "ss",
		Server:      "hk.example.com",
		Port:        29911,
		Method:      "aes-256-gcm",
		Password:    "secret",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:          "test",
		Token:         "token-project-clash",
		DefaultFormat: "clash",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: defaultClashTemplate(),
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}

	body, _, err := RenderSubscription("token-project-clash", "clash")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"port: 47890",
		"socks-port: 47891",
		"name: PROXY",
		"rule-providers:",
		"RULE-SET,applications,DIRECT",
		"name: GreenCloud-HK",
		"server: hk.example.com",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in project clash output, got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "GreenCloud-US-SJC-SS") {
		t.Fatalf("old template sample proxy should be replaced, got:\n%s", body)
	}
}

func TestEnsureSubscriptionDefaultsRepairsSurgeStyleClashTemplate(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	profile := model.SubscriptionProfile{
		Name:          "default",
		Token:         "token-repair",
		DefaultFormat: "surge",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: "[General]\n\n[Proxy]\nOld = direct\n",
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}

	EnsureSubscriptionDefaults()

	var repaired model.SubscriptionProfile
	if err := model.DB.First(&repaired, profile.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(repaired.ClashTemplate, "[Proxy]") {
		t.Fatalf("expected clash template repaired, got:\n%s", repaired.ClashTemplate)
	}
	if !strings.Contains(repaired.ClashTemplate, "proxy-groups:") || !strings.Contains(repaired.ClashTemplate, "rule-providers:") {
		t.Fatalf("expected project clash template, got:\n%s", repaired.ClashTemplate)
	}
}

func TestReportProxyNodeAttachesByProtocol(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	EnsureSubscriptionDefaults()
	apiKey := GetConfigValue(subAPIKeyConfigName)

	snellRes := ReportProxyNode(apiKey, proxyReport("snell-node", "snell", 44000))
	if snellRes.Code != 0 {
		t.Fatalf("report snell: %s", snellRes.Msg)
	}
	vlessRes := ReportProxyNode(apiKey, proxyReport("vless-node", "vless", 443))
	if vlessRes.Code != 0 {
		t.Fatalf("report vless: %s", vlessRes.Msg)
	}

	assertNodeFormats := func(t *testing.T, externalID string, want []string) {
		t.Helper()
		var node model.ProxyNode
		if err := model.DB.Where("external_id = ?", externalID).First(&node).Error; err != nil {
			t.Fatal(err)
		}
		var formats []string
		model.DB.Raw(`
			SELECT sp.default_format
			FROM subscription_profile_node spn
			INNER JOIN subscription_profile sp ON sp.id = spn.subscription_id
			WHERE spn.proxy_node_id = ?
			ORDER BY sp.default_format`, node.ID).Scan(&formats)
		if strings.Join(formats, ",") != strings.Join(want, ",") {
			t.Fatalf("node %s formats=%v want=%v", externalID, formats, want)
		}
	}
	assertNodeFormats(t, "snell-node", []string{"clash", "surge"})
	assertNodeFormats(t, "vless-node", []string{"clash", "singbox", "v2rayn"})
}

func TestDefaultNodeNameUsesProviderRegionProtocol(t *testing.T) {
	tests := []struct {
		name string
		req  dto.ProxyNodeReportDto
		want string
	}{
		{
			name: "greencloud ss",
			req: dto.ProxyNodeReportDto{
				Name:     "JP-Shadowsocks 传统版",
				Protocol: "ss",
				Options:  `{"source":"vless-server.sh","provider":"GreenCloud","region":"JP","protocolLabel":"SS"}`,
			},
			want: "GreenCloud-JP-SS",
		},
		{
			name: "aliyun snell",
			req: dto.ProxyNodeReportDto{
				Name:     "HK-Snell v4",
				Protocol: "snell",
				Options:  `{"source":"vless-server.sh","provider":"Alibaba Cloud","region":"HK","protocolLabel":"Snell"}`,
			},
			want: "Aliyun-HK-Snell",
		},
		{
			name: "already normalized",
			req: dto.ProxyNodeReportDto{
				Name:     "GreenCloud-JP-SS",
				Protocol: "ss",
			},
			want: "GreenCloud-JP-SS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultNodeName(tt.req); got != tt.want {
				t.Fatalf("defaultNodeName()=%q want %q", got, tt.want)
			}
		})
	}
}

func TestProxyNodeClassificationUsesOptionsAndNameFallback(t *testing.T) {
	node := model.ProxyNode{
		Name:     "Fallback-HK-Snell",
		Protocol: "ss",
		Options:  `{"provider":"GreenCloud","region":"JP","protocolLabel":"SS"}`,
	}
	provider, region, protocol := proxyNodeClassification(node)
	if provider != "GreenCloud" || region != "JP" || protocol != "SS" {
		t.Fatalf("classification from options = %q/%q/%q", provider, region, protocol)
	}

	node.Options = ""
	node.Protocol = "snell"
	provider, region, protocol = proxyNodeClassification(node)
	if provider != "Fallback" || region != "HK" || protocol != "Snell" {
		t.Fatalf("classification from name = %q/%q/%q", provider, region, protocol)
	}
}

func TestReportProxyNodeUsesSourceHostIdentity(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	updateOrCreateConfig(subAPIKeyConfigName, "test-key")

	a := proxyReport("", "ss-legacy", 8388)
	a.SourceHost = "node-machine-a"
	a.LegacySourceHost = "debian"
	a.Server = "203.0.113.10"
	a.Method = "aes-256-gcm"
	b := proxyReport("", "ss-legacy", 8389)
	b.SourceHost = "node-machine-b"
	b.LegacySourceHost = "debian"
	b.Server = "198.51.100.20"
	b.Method = "aes-256-gcm"

	if res := ReportProxyNode("test-key", a); res.Code != 0 {
		t.Fatalf("report a: %s", res.Msg)
	}
	if res := ReportProxyNode("test-key", b); res.Code != 0 {
		t.Fatalf("report b: %s", res.Msg)
	}
	var nodes []model.ProxyNode
	if err := model.DB.Order("external_id asc").Find(&nodes).Error; err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected two nodes, got %#v", nodes)
	}
	if nodes[0].ExternalID != "node-machine-a-ss-legacy" || nodes[1].ExternalID != "node-machine-b-ss-legacy" {
		t.Fatalf("unexpected external ids: %#v", []string{nodes[0].ExternalID, nodes[1].ExternalID})
	}
}

func TestReportProxyNodeMigratesLegacyExternalID(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	updateOrCreateConfig(subAPIKeyConfigName, "test-key")
	now := time.Now().UnixMilli()
	oldNode := model.ProxyNode{
		ExternalID:  "debian-ss-legacy",
		Name:        "Old",
		Protocol:    "ss",
		Server:      "203.0.113.10",
		Port:        8388,
		Method:      "aes-256-gcm",
		Password:    "secret",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&oldNode).Error; err != nil {
		t.Fatal(err)
	}

	report := proxyReport("", "ss-legacy", 8388)
	report.SourceHost = "node-machine-a"
	report.LegacyExternalID = "debian-ss-legacy"
	report.LegacySourceHost = "debian"
	report.Server = "203.0.113.10"
	report.Method = "aes-256-gcm"
	if res := ReportProxyNode("test-key", report); res.Code != 0 {
		t.Fatalf("report: %s", res.Msg)
	}
	var count int64
	model.DB.Model(&model.ProxyNode{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected legacy node migrated without duplicate, count=%d", count)
	}
	var node model.ProxyNode
	if err := model.DB.First(&node).Error; err != nil {
		t.Fatal(err)
	}
	if node.ID != oldNode.ID || node.ExternalID != "node-machine-a-ss-legacy" {
		t.Fatalf("unexpected migrated node: %#v", node)
	}
}

func TestRenderV2rayNSubscription(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	node := model.ProxyNode{
		ExternalID:    "v2rayn-vless",
		Name:          "V2rayN-HK",
		Protocol:      "vless",
		Server:        "hk.example.com",
		Port:          443,
		UUID:          "11111111-1111-1111-1111-111111111111",
		Security:      "reality",
		Network:       "tcp",
		SNI:           "www.example.com",
		Flow:          "xtls-rprx-vision",
		PublicKey:     "public-key",
		ShortID:       "abcd",
		Fingerprint:   "chrome",
		AllowInsecure: 1,
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:          "v2rayn-profile",
		Token:         "token-v2rayn",
		DefaultFormat: "v2rayn",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: fallbackClashTemplate,
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}

	body, contentType, err := RenderSubscription("token-v2rayn", "v2rayn")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode subscription: %v", err)
	}
	for _, want := range []string{
		"vless://11111111-1111-1111-1111-111111111111@hk.example.com:443",
		"security=reality",
		"flow=xtls-rprx-vision",
		"pbk=public-key",
		"sid=abcd",
		"fp=chrome",
		"#V2rayN-HK",
	} {
		if !strings.Contains(string(decoded), want) {
			t.Fatalf("expected %q in v2rayN output, got:\n%s", want, string(decoded))
		}
	}
}

func TestDeleteReportedProxyNodeRemovesProtocolAndBindings(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	EnsureSubscriptionDefaults()
	apiKey := GetConfigValue(subAPIKeyConfigName)
	report := proxyReport("host-a-vless", "vless", 443)
	if res := ReportProxyNode(apiKey, report); res.Code != 0 {
		t.Fatalf("report: %s", res.Msg)
	}
	var node model.ProxyNode
	if err := model.DB.Where("external_id = ?", "host-a-vless").First(&node).Error; err != nil {
		t.Fatal(err)
	}
	var linksBefore int64
	model.DB.Model(&model.SubscriptionProfileNode{}).Where("proxy_node_id = ?", node.ID).Count(&linksBefore)
	if linksBefore == 0 {
		t.Fatal("expected default subscription binding")
	}

	res := DeleteReportedProxyNode(apiKey, dto.ProxyNodeDeleteReportDto{ExternalID: "host-a-vless", CleanupMode: "protocol"})
	if res.Code != 0 {
		t.Fatalf("delete report: %s", res.Msg)
	}
	var count int64
	model.DB.Model(&model.ProxyNode{}).Where("external_id = ?", "host-a-vless").Count(&count)
	if count != 0 {
		t.Fatalf("expected proxy node deleted, count=%d", count)
	}
	model.DB.Model(&model.SubscriptionProfileNode{}).Where("proxy_node_id = ?", node.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected binding deleted, count=%d", count)
	}
}

func TestDeleteReportedProxyNodeRemovesBoundForward(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	EnsureSubscriptionDefaults()
	apiKey := GetConfigValue(subAPIKeyConfigName)
	now := time.Now().UnixMilli()
	node := model.ProxyNode{
		ExternalID:  "host-forward-vless",
		Name:        "Forwarded",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		UUID:        "11111111-1111-1111-1111-111111111111",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	tunnel := model.Tunnel{
		Name:          "relay",
		InNodeID:      99,
		InIP:          "relay.example.com",
		OutNodeID:     99,
		OutIP:         "127.0.0.1",
		Type:          tunnelTypePortForward,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	forward := model.Forward{
		UserID:      1,
		UserName:    defaultUsername,
		Name:        "relay-forward",
		TunnelID:    tunnel.ID,
		InPort:      31002,
		RemoteAddr:  "origin.example.com:443",
		Strategy:    "fifo",
		CreatedTime: now,
		UpdatedTime: now,
		Status:      forwardStatusActive,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ProxyNode{}).Where("id = ?", node.ID).Update("forward_id", forward.ID).Error; err != nil {
		t.Fatal(err)
	}

	res := DeleteReportedProxyNode(apiKey, dto.ProxyNodeDeleteReportDto{ExternalID: node.ExternalID, CleanupMode: "protocol"})
	if res.Code != 0 {
		t.Fatalf("delete report: %s", res.Msg)
	}
	var count int64
	model.DB.Model(&model.ProxyNode{}).Where("id = ?", node.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected proxy node deleted, count=%d", count)
	}
	model.DB.Model(&model.Forward{}).Where("id = ?", forward.ID).Count(&count)
	if count != 0 {
		t.Fatalf("expected bound forward deleted, count=%d", count)
	}
}

func TestDeleteReportedProxyNodeServerCleanupUsesSourceHost(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	EnsureSubscriptionDefaults()
	apiKey := GetConfigValue(subAPIKeyConfigName)
	for _, report := range []dto.ProxyNodeReportDto{
		proxyReport("host-b-vless", "vless", 443),
		proxyReport("host-b-snell", "snell", 44000),
		proxyReport("manual-same-ip", "vless", 8443),
	} {
		report.Server = "203.0.113.10"
		if res := ReportProxyNode(apiKey, report); res.Code != 0 {
			t.Fatalf("report %s: %s", report.ExternalID, res.Msg)
		}
	}

	res := DeleteReportedProxyNode(apiKey, dto.ProxyNodeDeleteReportDto{
		SourceHost:  "host-b",
		Server:      "203.0.113.10",
		CleanupMode: "server",
	})
	if res.Code != 0 {
		t.Fatalf("server cleanup: %s", res.Msg)
	}
	var remaining []string
	model.DB.Model(&model.ProxyNode{}).Order("external_id asc").Pluck("external_id", &remaining)
	if strings.Join(remaining, ",") != "manual-same-ip" {
		t.Fatalf("remaining=%v", remaining)
	}
}

func TestAssignProxyNodeProfilesReplacesBindings(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	EnsureSubscriptionDefaults()
	now := time.Now().UnixMilli()
	node := model.ProxyNode{
		ExternalID:  "assign-node",
		Name:        "Assign",
		Protocol:    "vless",
		Server:      "assign.example.com",
		Port:        443,
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	var surge model.SubscriptionProfile
	if err := model.DB.Where("default_format = ?", "surge").First(&surge).Error; err != nil {
		t.Fatal(err)
	}
	res := AssignProxyNodeProfiles(dto.ProxyNodeProfileAssignDto{NodeID: node.ID, ProfileIDs: []int64{surge.ID}})
	if res.Code != 0 {
		t.Fatalf("assign: %s", res.Msg)
	}
	var links []model.SubscriptionProfileNode
	model.DB.Where("proxy_node_id = ?", node.ID).Find(&links)
	if len(links) != 1 || links[0].SubscriptionID != surge.ID {
		t.Fatalf("unexpected links: %#v", links)
	}
}

func TestCreateProxyNodeRelayBindsForwardAndRenderUsesEntry(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	entry := model.Node{
		Name:        "entry",
		Secret:      "entry-secret",
		IP:          "relay.example.com",
		ServerIP:    "10.0.0.1",
		PortSta:     31000,
		PortEnd:     31010,
		ForwardMode: "nftables",
		CreatedTime: now,
		UpdatedTime: &now,
		Status:      1,
	}
	if err := model.DB.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	tunnel := model.Tunnel{
		Name:          "relay",
		InNodeID:      entry.ID,
		InIP:          entry.IP,
		OutNodeID:     entry.ID,
		OutIP:         entry.ServerIP,
		Type:          tunnelTypePortForward,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	node := model.ProxyNode{
		ExternalID:  "relay-node",
		Name:        "RelayNode",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		UUID:        "11111111-1111-1111-1111-111111111111",
		Security:    "tls",
		SNI:         "origin.example.com",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:          "relay-profile",
		Token:         "relay-token",
		DefaultFormat: "surge",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: fallbackClashTemplate,
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}

	port := 31002
	origCreateForward := createForwardForProxyNodeRelay
	createForwardForProxyNodeRelay = func(cu CurrentUser, req dto.ForwardDto) (*model.Forward, result.R) {
		now := time.Now().UnixMilli()
		forward := model.Forward{
			UserID:      cu.UserID,
			UserName:    cu.UserName,
			Name:        req.Name,
			TunnelID:    req.TunnelID,
			InPort:      *req.InPort,
			RemoteAddr:  req.RemoteAddr,
			Strategy:    req.Strategy,
			CreatedTime: now,
			UpdatedTime: now,
			Status:      forwardStatusActive,
		}
		if err := model.DB.Create(&forward).Error; err != nil {
			return nil, result.Err("mock create forward failed")
		}
		return &forward, result.OkEmpty()
	}
	defer func() { createForwardForProxyNodeRelay = origCreateForward }()
	res := CreateProxyNodeRelay(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin_user"}, dto.ProxyNodeRelayDto{
		NodeID:   node.ID,
		TunnelID: tunnel.ID,
		InPort:   &port,
	})
	if res.Code != 0 {
		t.Fatalf("create relay: %s", res.Msg)
	}
	var updated model.ProxyNode
	if err := model.DB.First(&updated, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.ForwardID == nil || *updated.ForwardID == 0 {
		t.Fatalf("forward not bound: %#v", updated.ForwardID)
	}
	var forward model.Forward
	if err := model.DB.First(&forward, *updated.ForwardID).Error; err != nil {
		t.Fatal(err)
	}
	if forward.RemoteAddr != "origin.example.com:443" || forward.InPort != port {
		t.Fatalf("unexpected forward: %#v", forward)
	}
	body, _, err := RenderSubscription("relay-token", "surge")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "RelayNode = vless, relay.example.com, 31002") {
		t.Fatalf("expected relay address, got:\n%s", body)
	}
}

func TestCreateProxyNodeRelayDeletesPreviousForward(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	entry := model.Node{
		Name:        "entry",
		Secret:      "entry-secret",
		IP:          "relay.example.com",
		ServerIP:    "10.0.0.1",
		PortSta:     31000,
		PortEnd:     31010,
		ForwardMode: "nftables",
		CreatedTime: now,
		UpdatedTime: &now,
		Status:      1,
	}
	if err := model.DB.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	tunnel := model.Tunnel{
		Name:          "relay",
		InNodeID:      entry.ID,
		InIP:          entry.IP,
		OutNodeID:     entry.ID,
		OutIP:         entry.ServerIP,
		Type:          tunnelTypePortForward,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	oldForward := model.Forward{
		UserID:      1,
		UserName:    defaultUsername,
		Name:        "old-relay",
		TunnelID:    tunnel.ID,
		InPort:      31001,
		RemoteAddr:  "origin.example.com:443",
		Strategy:    "fifo",
		CreatedTime: now,
		UpdatedTime: now,
		Status:      forwardStatusActive,
	}
	if err := model.DB.Create(&oldForward).Error; err != nil {
		t.Fatal(err)
	}
	node := model.ProxyNode{
		ExternalID:  "relay-node",
		Name:        "RelayNode",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		ForwardID:   &oldForward.ID,
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	newPort := 31002
	origCreateForward := createForwardForProxyNodeRelay
	createForwardForProxyNodeRelay = func(cu CurrentUser, req dto.ForwardDto) (*model.Forward, result.R) {
		now := time.Now().UnixMilli()
		forward := model.Forward{
			UserID:      cu.UserID,
			UserName:    cu.UserName,
			Name:        req.Name,
			TunnelID:    req.TunnelID,
			InPort:      *req.InPort,
			RemoteAddr:  req.RemoteAddr,
			Strategy:    req.Strategy,
			CreatedTime: now,
			UpdatedTime: now,
			Status:      forwardStatusActive,
		}
		if err := model.DB.Create(&forward).Error; err != nil {
			return nil, result.Err("mock create forward failed")
		}
		return &forward, result.OkEmpty()
	}
	defer func() { createForwardForProxyNodeRelay = origCreateForward }()

	res := CreateProxyNodeRelay(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: defaultUsername}, dto.ProxyNodeRelayDto{
		NodeID:   node.ID,
		TunnelID: tunnel.ID,
		InPort:   &newPort,
		Name:     "new-relay",
	})
	if res.Code != 0 {
		t.Fatalf("modify relay: %s", res.Msg)
	}

	var updated model.ProxyNode
	if err := model.DB.First(&updated, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.ForwardID == nil || *updated.ForwardID == oldForward.ID {
		t.Fatalf("expected new forward binding, got %#v", updated.ForwardID)
	}
	var oldCount int64
	model.DB.Model(&model.Forward{}).Where("id = ?", oldForward.ID).Count(&oldCount)
	if oldCount != 0 {
		t.Fatalf("expected old forward to be deleted, count=%d", oldCount)
	}
	var newForward model.Forward
	if err := model.DB.First(&newForward, *updated.ForwardID).Error; err != nil {
		t.Fatalf("new forward missing: %v", err)
	}
	if newForward.Name != "new-relay" || newForward.InPort != newPort {
		t.Fatalf("unexpected new forward: %#v", newForward)
	}
}

func TestProxyNodeRelayAppendAndCloseSource(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	entry := model.Node{
		Name:        "entry",
		Secret:      "entry-secret",
		IP:          "relay.example.com",
		ServerIP:    "10.0.0.1",
		PortSta:     31000,
		PortEnd:     31010,
		ForwardMode: "nftables",
		CreatedTime: now,
		UpdatedTime: &now,
		Status:      1,
	}
	if err := model.DB.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	tunnel := model.Tunnel{
		Name:          "relay",
		InNodeID:      entry.ID,
		InIP:          entry.IP,
		OutNodeID:     entry.ID,
		OutIP:         entry.ServerIP,
		Type:          tunnelTypePortForward,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	node := model.ProxyNode{
		ExternalID:  "relay-source",
		Name:        "RelaySource",
		Protocol:    "vless",
		Server:      "origin.example.com",
		Port:        443,
		UUID:        "11111111-1111-1111-1111-111111111111",
		Status:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{
		Name:          "relay-profile",
		Token:         "relay-token",
		DefaultFormat: "surge",
		SurgeTemplate: fallbackSurgeTemplate,
		ClashTemplate: fallbackClashTemplate,
		Status:        1,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, Sort: 7, CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}

	nextPort := 31001
	origCreateForward := createForwardForProxyNodeRelay
	createForwardForProxyNodeRelay = func(cu CurrentUser, req dto.ForwardDto) (*model.Forward, result.R) {
		now := time.Now().UnixMilli()
		port := nextPort
		if req.InPort != nil {
			port = *req.InPort
		}
		nextPort++
		forward := model.Forward{
			UserID:      cu.UserID,
			UserName:    cu.UserName,
			Name:        req.Name,
			TunnelID:    req.TunnelID,
			InPort:      port,
			RemoteAddr:  req.RemoteAddr,
			Strategy:    req.Strategy,
			CreatedTime: now,
			UpdatedTime: now,
			Status:      forwardStatusActive,
		}
		if err := model.DB.Create(&forward).Error; err != nil {
			return nil, result.Err("mock create forward failed")
		}
		return &forward, result.OkEmpty()
	}
	defer func() { createForwardForProxyNodeRelay = origCreateForward }()

	appendPort := 31005
	appendRes := CreateProxyNodeRelay(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: defaultUsername}, dto.ProxyNodeRelayDto{
		NodeID:   node.ID,
		TunnelID: tunnel.ID,
		Mode:     proxyRelayModeAppend,
		InPort:   &appendPort,
		Name:     "append-relay",
	})
	if appendRes.Code != 0 {
		t.Fatalf("append relay: %s", appendRes.Msg)
	}
	var child model.ProxyNode
	if err := model.DB.Where("source_proxy_node_id = ?", node.ID).First(&child).Error; err != nil {
		t.Fatalf("child relay node missing: %v", err)
	}
	if child.RelayMode != proxyRelayModeAppend || child.ForwardID == nil {
		t.Fatalf("unexpected child relay node: %#v", child)
	}
	var childLinks []model.SubscriptionProfileNode
	if err := model.DB.Where("proxy_node_id = ?", child.ID).Find(&childLinks).Error; err != nil {
		t.Fatal(err)
	}
	if len(childLinks) != 1 || childLinks[0].SubscriptionID != profile.ID || childLinks[0].Sort != 7 {
		t.Fatalf("expected inherited profile binding, got %#v", childLinks)
	}

	replacePort := 31006
	replaceRes := CreateProxyNodeRelay(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: defaultUsername}, dto.ProxyNodeRelayDto{
		NodeID:   node.ID,
		TunnelID: tunnel.ID,
		Mode:     proxyRelayModeReplace,
		InPort:   &replacePort,
		Name:     "replace-relay",
	})
	if replaceRes.Code != 0 {
		t.Fatalf("replace relay: %s", replaceRes.Msg)
	}
	var source model.ProxyNode
	if err := model.DB.First(&source, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if source.ForwardID == nil || source.RelayMode != proxyRelayModeReplace {
		t.Fatalf("expected source replace relay, got %#v", source)
	}

	closeRes := CloseProxyNodeRelay(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: defaultUsername}, dto.ProxyNodeRelayCloseDto{NodeID: node.ID})
	if closeRes.Code != 0 {
		t.Fatalf("close relay: %s", closeRes.Msg)
	}
	var remainingChildren int64
	model.DB.Model(&model.ProxyNode{}).Where("source_proxy_node_id = ?", node.ID).Count(&remainingChildren)
	if remainingChildren != 0 {
		t.Fatalf("expected child relay nodes deleted, count=%d", remainingChildren)
	}
	var remainingForwards int64
	model.DB.Model(&model.Forward{}).Count(&remainingForwards)
	if remainingForwards != 0 {
		t.Fatalf("expected relay forwards deleted, count=%d", remainingForwards)
	}
	var restored model.ProxyNode
	if err := model.DB.First(&restored, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.ForwardID != nil || restored.RelayMode != "" {
		t.Fatalf("expected source restored to direct, got %#v", restored)
	}
}

func TestBuildVlessServerBootstrapCommand(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 固定占位值只用于验证命令参数拼接，不是可用的订阅 API 密钥。
	updateOrCreateConfig(subAPIKeyConfigName, "test-key")
	cmd := BuildVlessServerBootstrapCommand("https://panel.example.com/", "")
	for _, want := range []string{
		"https://panel.example.com/api/v1/sub/vless-server.sh",
		"--flux-panel-bind 'https://panel.example.com' 'test-key'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("expected %q in command: %s", want, cmd)
		}
	}
}

func TestGetVlessServerScriptContentFallback(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	data, name := GetVlessServerScriptContent()
	if name != vlessServerScriptName {
		t.Fatalf("unexpected script name %q", name)
	}
	if !strings.Contains(string(data), "--flux-panel-bind") {
		t.Fatalf("script content should support flux panel bind")
	}
}

func proxyReport(externalID, protocol string, port int) dto.ProxyNodeReportDto {
	return dto.ProxyNodeReportDto{
		ExternalID: externalID,
		Name:       externalID,
		Protocol:   protocol,
		Server:     externalID + ".example.com",
		Port:       port,
		UUID:       "11111111-1111-1111-1111-111111111111",
		Password:   "secret",
	}
}
