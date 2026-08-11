package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestCreateNodeRejectsInvalidNftServerIPBeforeDatabaseWrite(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	for _, serverIP := range []string{"example.com", "192.0.2.1;flush-ruleset"} {
		res := CreateNode(dto.NodeDto{
			Name:        "invalid-nft-node",
			IP:          "192.0.2.10",
			ServerIP:    serverIP,
			PortSta:     10000,
			PortEnd:     20000,
			ForwardMode: forwardModeNftables,
		})
		if res.Code == 0 || res.Msg != "节点服务地址格式错误" {
			t.Fatalf("CreateNode(%q) returned code=%d msg=%q", serverIP, res.Code, res.Msg)
		}
	}
	var count int64
	if err := model.DB.Model(&model.Node{}).Count(&count).Error; err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid node target wrote %d rows", count)
	}
}

func TestUpdateNodeRejectsInvalidNftServerIPBeforeDatabaseWrite(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "valid-nft-node",
		Secret:      "valid-nft-node-secret",
		IP:          "192.0.2.10",
		ServerIP:    "192.0.2.10",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := UpdateNode(dto.NodeUpdateDto{
		ID:          node.ID,
		Name:        node.Name,
		IP:          node.IP,
		ServerIP:    "192.0.2.10 #comment",
		PortSta:     node.PortSta,
		PortEnd:     node.PortEnd,
		ForwardMode: forwardModeNftables,
	})
	if res.Code == 0 || res.Msg != "节点服务地址格式错误" {
		t.Fatalf("UpdateNode returned code=%d msg=%q", res.Code, res.Msg)
	}
	var got model.Node
	if err := model.DB.First(&got, node.ID).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.ServerIP != node.ServerIP {
		t.Fatalf("serverIP=%q, want unchanged %q", got.ServerIP, node.ServerIP)
	}
}

func TestBuildNftRulesSkipsInvalidLegacyPortWithoutPoisoningBatch(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "legacy-port-node", Secret: "legacy-port-secret", IP: "192.0.2.1", ServerIP: "192.0.2.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	tunnel := model.Tunnel{
		Name: "legacy-port-tunnel", InNodeID: node.ID, OutNodeID: node.ID, Type: tunnelTypePortForward,
		Flow: 1, TCPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	forwards := []model.Forward{
		{UserID: 1, Name: "invalid-port", TunnelID: tunnel.ID, InPort: 65536, RemoteAddr: "198.51.100.1:443", Status: 1, CreatedTime: now, UpdatedTime: now},
		{UserID: 1, Name: "valid-port", TunnelID: tunnel.ID, InPort: 10001, RemoteAddr: "198.51.100.2:443", Status: 1, CreatedTime: now, UpdatedTime: now},
	}
	if err := model.DB.Create(&forwards).Error; err != nil {
		t.Fatalf("create forwards: %v", err)
	}

	rules, err := buildNftRules(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	validCount := 0
	for _, rule := range rules {
		if rule.ForwardID == forwards[0].ID {
			t.Fatalf("invalid legacy forward generated rule %q", rule.Rule)
		}
		if rule.ForwardID == forwards[1].ID {
			validCount++
		}
	}
	if validCount != 8 {
		t.Fatalf("valid forward generated %d rules, want 8 (tcp and udp)", validCount)
	}
}

func TestGetNftConfigBySecretReturnsExistingActiveForwardsForReinstall(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "reinstall-node", Secret: "reinstall-node-secret", IP: "192.0.2.10", ServerIP: "192.0.2.10",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	tunnel := model.Tunnel{
		Name: "reinstall-tunnel", InNodeID: node.ID, OutNodeID: node.ID, Type: tunnelTypePortForward,
		Flow: 1, TCPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	forwards := []model.Forward{
		{
			UserID: 1, Name: "restored-forward", TunnelID: tunnel.ID, InPort: 10001,
			RemoteAddr: "198.51.100.10:443", Status: 1, CreatedTime: now, UpdatedTime: now,
		},
		{
			UserID: 1, Name: "paused-forward", TunnelID: tunnel.ID, InPort: 10002,
			RemoteAddr: "198.51.100.20:443", Status: 0, CreatedTime: now, UpdatedTime: now,
		},
	}
	if err := model.DB.Create(&forwards).Error; err != nil {
		t.Fatalf("create forwards: %v", err)
	}

	response := GetNftConfigBySecret(node.Secret)
	if response.Code != 0 {
		t.Fatalf("GetNftConfigBySecret code=%d msg=%q", response.Code, response.Msg)
	}
	rules, ok := response.Data.([]dto.NftRuleDto)
	if !ok || len(rules) == 0 {
		t.Fatalf("restored rules=%T %+v", response.Data, response.Data)
	}
	for _, rule := range rules {
		if rule.ForwardID != forwards[0].ID {
			t.Fatalf("unexpected forward %d in restored rule %q", rule.ForwardID, rule.Rule)
		}
	}

	invalid := GetNftConfigBySecret("wrong-secret")
	if invalid.Code == 0 {
		t.Fatal("invalid node secret returned a successful restore payload")
	}
}

func TestGetInstallCommandUsesPanelHostedScript(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "https://panel.example.com:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "node",
		Secret:      "secret-value",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      0,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetInstallCommand(node.ID, forwardModeNftables)
	if res.Code != 0 {
		t.Fatalf("GetInstallCommand code=%d msg=%s", res.Code, res.Msg)
	}
	cmd, ok := res.Data.(string)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if strings.Contains(cmd, "github.com") || strings.Contains(cmd, "raw.githubusercontent.com") {
		t.Fatalf("install command should not use GitHub: %s", cmd)
	}
	if !strings.Contains(cmd, "https://panel.example.com:6365/api/v1/node/install/install_nftables.sh") {
		t.Fatalf("install command should use panel-hosted script: %s", cmd)
	}
	if !strings.Contains(cmd, "curl --proto '=https' --proto-redir '=https' -fsSL") {
		t.Fatalf("HTTPS bootstrap curl lacks redirect protocol restrictions: %s", cmd)
	}
	if strings.Contains(cmd, "ALLOW_INSECURE_NODE_DOWNLOADS=true") {
		t.Fatalf("HTTPS install command must not enable insecure downloads: %s", cmd)
	}
	if !strings.Contains(cmd, "-a 'https://panel.example.com:6365'") || !strings.Contains(cmd, "-s 'secret-value'") {
		t.Fatalf("install command should quote node args: %s", cmd)
	}
}

func TestValidatePanelAssetBaseEnforcesHTTPSPolicy(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		allowInsecure bool
		wantErr       bool
	}{
		{name: "https", raw: "https://panel.example.com:6365", wantErr: false},
		{name: "https base path", raw: "https://panel.example.com:6365/base/", wantErr: false},
		{name: "public http default", raw: "http://panel.example.com:6365", wantErr: true},
		{name: "ipv4 loopback http", raw: "http://127.0.0.1:6365", wantErr: false},
		{name: "ipv6 loopback http", raw: "http://[::1]:6365", wantErr: false},
		{name: "explicit insecure http", raw: "http://panel.example.com:6365", allowInsecure: true, wantErr: false},
		{name: "ftp stays rejected", raw: "ftp://panel.example.com/asset", allowInsecure: true, wantErr: true},
		{name: "missing host", raw: "https:///asset", wantErr: true},
		{name: "empty hostname with port", raw: "https://:6365/asset", wantErr: true},
		{name: "userinfo", raw: "https://user:password@panel.example.com/asset", wantErr: true},
		{name: "query", raw: "https://panel.example.com?download=1", wantErr: true},
		{name: "empty query marker", raw: "https://panel.example.com?", wantErr: true},
		{name: "fragment", raw: "https://panel.example.com/#asset", wantErr: true},
		{name: "encoded traversal", raw: "https://panel.example.com/%2e%2e/asset", wantErr: true},
		{name: "duplicate separator", raw: "https://panel.example.com/base//asset", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePanelAssetBase(tc.raw, tc.allowInsecure)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePanelAssetBase(%q, %v) error = %v, wantErr %v", tc.raw, tc.allowInsecure, err, tc.wantErr)
			}
		})
	}
}

func TestGetInstallCommandRejectsPanelURLUserinfo(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "https://user:password@panel.example.com:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetInstallCommand(node.ID, forwardModeNftables)
	if res.Code == 0 || !strings.Contains(res.Msg, "无效") {
		t.Fatalf("GetInstallCommand returned code=%d msg=%q, want invalid panel URL", res.Code, res.Msg)
	}
}

func TestGetInstallCommandRejectsPublicHTTPByDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "panel.example.com:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetInstallCommand(node.ID, forwardModeNftables)
	if res.Code == 0 || !strings.Contains(res.Msg, "HTTPS") {
		t.Fatalf("GetInstallCommand returned code=%d msg=%q, want HTTPS policy error", res.Code, res.Msg)
	}
}

func TestGetInstallCommandPassesExplicitUnsafeFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{AllowInsecureDownloads: true})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "http://panel.example.com:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetInstallCommand(node.ID, forwardModeNftables)
	if res.Code != 0 {
		t.Fatalf("GetInstallCommand code=%d msg=%q", res.Code, res.Msg)
	}
	cmd, ok := res.Data.(string)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if !strings.Contains(cmd, "http://panel.example.com:6365/api/v1/node/install/install_nftables.sh") {
		t.Fatalf("install command missing HTTP panel URL: %s", cmd)
	}
	if !strings.Contains(cmd, "ALLOW_INSECURE_NODE_DOWNLOADS=true") {
		t.Fatalf("install command did not explicitly pass unsafe flag: %s", cmd)
	}
	if !strings.Contains(cmd, "curl --proto '=http,https' --proto-redir '=https' -fsSL") {
		t.Fatalf("HTTP bootstrap curl lacks HTTPS-only redirect policy: %s", cmd)
	}
}

func TestGetInstallCommandUsesSystemHTTPSettingWithoutRestart(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "http://panel.example.com:6365")
	updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "true")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	allowed := GetInstallCommand(node.ID, forwardModeNftables)
	if allowed.Code != 0 {
		t.Fatalf("system setting did not allow HTTP install: code=%d msg=%q", allowed.Code, allowed.Msg)
	}
	command, ok := allowed.Data.(string)
	if !ok || !strings.Contains(command, "ALLOW_INSECURE_NODE_DOWNLOADS=true") {
		t.Fatalf("HTTP install command omitted explicit unsafe flag: %#v", allowed.Data)
	}

	updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "false")
	rejected := GetInstallCommand(node.ID, forwardModeNftables)
	if rejected.Code == 0 || !strings.Contains(rejected.Msg, "HTTPS") {
		t.Fatalf("disabling system setting did not restore HTTPS policy: code=%d msg=%q", rejected.Code, rejected.Msg)
	}
}

func TestGetInstallCommandLoopbackHTTPAllowsOnlyHTTPSRedirects(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "http://127.0.0.1:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetInstallCommand(node.ID, forwardModeNftables)
	if res.Code != 0 {
		t.Fatalf("GetInstallCommand code=%d msg=%q", res.Code, res.Msg)
	}
	cmd, ok := res.Data.(string)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if !strings.Contains(cmd, "curl --proto '=http,https' --proto-redir '=https' -fsSL") {
		t.Fatalf("loopback bootstrap curl lacks HTTPS-only redirect policy: %s", cmd)
	}
	if strings.Contains(cmd, "ALLOW_INSECURE_NODE_DOWNLOADS=true") {
		t.Fatalf("loopback HTTP must not require global insecure flag: %s", cmd)
	}
}

func TestUpgradeNodeRejectsPublicHTTPBeforeDispatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "http://panel.example.com:6365")
	now := time.Now().UnixMilli()
	version := "nftables-go-1.3.1"
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, Version: &version,
		CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := UpgradeNode(node.ID)
	if res.Code == 0 || !strings.Contains(res.Msg, "HTTPS") {
		t.Fatalf("UpgradeNode returned code=%d msg=%q, want HTTPS policy error", res.Code, res.Msg)
	}
}

func TestUpgradeNodeRejectsPanelURLUserinfoBeforeDispatch(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	updateOrCreateConfig("ip", "https://user:password@panel.example.com:6365")
	now := time.Now().UnixMilli()
	version := "nftables-go-1.3.1"
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, Version: &version,
		CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	originalSender := nodeUpgradeSender
	t.Cleanup(func() { nodeUpgradeSender = originalSender })
	calls := 0
	nodeUpgradeSender = func(int64, string, string, string, bool) ws.GostResult {
		calls++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := UpgradeNode(node.ID)
	if res.Code == 0 || !strings.Contains(res.Msg, "无效") {
		t.Fatalf("UpgradeNode returned code=%d msg=%q, want invalid panel URL", res.Code, res.Msg)
	}
	if calls != 0 {
		t.Fatalf("invalid panel URL dispatched %d upgrade commands", calls)
	}
}

func TestUpgradeNodePassesRuntimeFlagToCommand(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ConfigureNodeRuntime(NodeRuntimeConfig{AllowInsecureDownloads: true})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "http://panel.example.com:6365")
	now := time.Now().UnixMilli()
	version := "nftables-go-1.3.1"
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, Version: &version,
		CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	originalSender := nodeUpgradeSender
	t.Cleanup(func() { nodeUpgradeSender = originalSender })
	calls := 0
	nodeUpgradeSender = func(nodeID int64, baseURL, mode, latest string, allowInsecure bool) ws.GostResult {
		calls++
		if nodeID != node.ID || baseURL != "http://panel.example.com:6365" || mode != forwardModeNftables || latest != latestNftNodeVersion {
			t.Fatalf("unexpected upgrade command: node=%d base=%q mode=%q latest=%q", nodeID, baseURL, mode, latest)
		}
		if !allowInsecure {
			t.Fatal("upgrade command omitted runtime allow-insecure flag")
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	res := UpgradeNode(node.ID)
	if res.Code != 0 {
		t.Fatalf("UpgradeNode code=%d msg=%q", res.Code, res.Msg)
	}
	if calls != 1 {
		t.Fatalf("upgrade sender calls=%d, want 1", calls)
	}
}

func TestUpgradeNodeUsesSystemHTTPSetting(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "http://panel.example.com:6365")
	updateOrCreateConfig(allowInsecureNodeDownloadsConfigName, "true")
	now := time.Now().UnixMilli()
	version := "nftables-go-1.3.1"
	node := model.Node{
		Name: "node", Secret: "secret-value", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, Version: &version,
		CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	originalSender := nodeUpgradeSender
	t.Cleanup(func() { nodeUpgradeSender = originalSender })
	calls := 0
	nodeUpgradeSender = func(nodeID int64, baseURL, mode, latest string, allowInsecure bool) ws.GostResult {
		calls++
		if nodeID != node.ID || baseURL != "http://panel.example.com:6365" || !allowInsecure {
			t.Fatalf("system setting not propagated: node=%d base=%q allow=%v", nodeID, baseURL, allowInsecure)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	res := UpgradeNode(node.ID)
	if res.Code != 0 || calls != 1 {
		t.Fatalf("system setting HTTP upgrade: code=%d msg=%q calls=%d", res.Code, res.Msg, calls)
	}
}

func TestNodeUpgradeBaseURLPrefersHistoryForProtectedAgent(t *testing.T) {
	version := localPanelBaseURLNftMinVersion
	node := model.Node{
		ForwardMode:          forwardModeNftables,
		Version:              &version,
		LastConnectedBaseURL: "https://historical-panel.example.com/base",
	}
	got, err := nodeUpgradeBaseURL(node, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != node.LastConnectedBaseURL {
		t.Fatalf("upgrade base URL = %q", got.String())
	}
}

func TestNodeUpgradeBaseURLUsesHistoryWithoutCurrentSystemIP(t *testing.T) {
	version := localPanelBaseURLGostMinVersion
	node := model.Node{
		ForwardMode:          forwardModeGost,
		Version:              &version,
		LastConnectedBaseURL: "https://historical-panel.example.com",
	}
	got, err := nodeUpgradeBaseURL(node, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != node.LastConnectedBaseURL {
		t.Fatalf("upgrade base URL = %q", got.String())
	}
}

func TestNodeUpgradeBaseURLDoesNotFallbackWhenProtectedHistoryIsUnsafe(t *testing.T) {
	version := localPanelBaseURLNftMinVersion
	node := model.Node{
		ForwardMode:          forwardModeNftables,
		Version:              &version,
		LastConnectedBaseURL: "http://historical-panel.example.com",
	}
	_, err := nodeUpgradeBaseURL(node, false)
	if err == nil || !strings.Contains(err.Error(), "历史连接地址") || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("unsafe historical URL error = %v", err)
	}
}

func TestNodeUpgradeBaseURLRejectsMalformedHistoryWithoutFallback(t *testing.T) {
	version := localPanelBaseURLGostMinVersion
	_, err := nodeUpgradeBaseURL(model.Node{
		ForwardMode:          forwardModeGost,
		Version:              &version,
		LastConnectedBaseURL: "https://historical-panel.example.com/base?unexpected=1",
	}, false)
	if err == nil || !strings.Contains(err.Error(), "历史连接地址") || !strings.Contains(err.Error(), "查询参数") {
		t.Fatalf("malformed historical URL error = %v", err)
	}
}

func TestUpgradeNodeIgnoresPersistedHistoryForLegacyAgent(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "https://trusted-panel.example.com")

	version := "nftables-go-1.3.3"
	node := model.Node{
		Name: "legacy-poisoned-node", Secret: "legacy-poisoned-secret", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, Version: &version,
		LastConnectedBaseURL: "https://attacker.example.com", CreatedTime: time.Now().UnixMilli(), Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	originalSender := nodeUpgradeSender
	t.Cleanup(func() { nodeUpgradeSender = originalSender })
	nodeUpgradeSender = func(nodeID int64, baseURL, mode, latest string, allowInsecure bool) ws.GostResult {
		if nodeID != node.ID || baseURL != "https://trusted-panel.example.com" {
			t.Fatalf("legacy upgrade target node=%d base=%q", nodeID, baseURL)
		}
		if mode != forwardModeNftables || latest != latestNftNodeVersion || allowInsecure {
			t.Fatalf("legacy upgrade metadata mode=%q latest=%q allow=%v", mode, latest, allowInsecure)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	res := UpgradeNode(node.ID)
	if res.Code != 0 {
		t.Fatalf("UpgradeNode code=%d msg=%q", res.Code, res.Msg)
	}
}

func TestUpgradeNodeExplainsUnknownPostDispatchOutcome(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	ConfigureNodeRuntime(NodeRuntimeConfig{})
	t.Cleanup(func() { ConfigureNodeRuntime(NodeRuntimeConfig{}) })
	updateOrCreateConfig("ip", "https://trusted-panel.example.com")

	version := "1.2.5"
	node := model.Node{
		Name: "slow-upgrade-node", Secret: "slow-upgrade-secret", IP: "1.2.3.4", ServerIP: "10.0.0.1",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeGost, Version: &version,
		LastConnectedBaseURL: "https://historical-panel.example.com", CreatedTime: time.Now().UnixMilli(), Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	originalSender := nodeUpgradeSender
	t.Cleanup(func() { nodeUpgradeSender = originalSender })
	nodeUpgradeSender = func(_ int64, _ string, _ string, _ string, _ bool) ws.GostResult {
		return ws.GostResult{Msg: "等待响应超时", OutcomeUnknown: true}
	}

	res := UpgradeNode(node.ID)
	if res.Code == 0 || !strings.Contains(res.Msg, "结果暂时未知") || !strings.Contains(res.Msg, "不要立即重试") {
		t.Fatalf("unknown upgrade result = code %d, msg %q", res.Code, res.Msg)
	}
}

func TestGetUninstallCommandUsesPanelHostedScript(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	updateOrCreateConfig("ip", "panel.example.com:6365")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "node",
		Secret:      "secret-value",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      0,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetUninstallCommand(node.ID, "")
	if res.Code != 0 {
		t.Fatalf("GetUninstallCommand code=%d msg=%s", res.Code, res.Msg)
	}
	cmd, ok := res.Data.(string)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if strings.Contains(cmd, "github.com") || strings.Contains(cmd, "raw.githubusercontent.com") {
		t.Fatalf("uninstall command should not use GitHub: %s", cmd)
	}
	if strings.Contains(cmd, "systemctl stop") || strings.Contains(cmd, "rm -rf /etc/flux-nftables") {
		t.Fatalf("uninstall command should delegate cleanup to panel-hosted script: %s", cmd)
	}
	if !strings.Contains(cmd, "http://panel.example.com:6365/api/v1/node/install/uninstall_nftables.sh") {
		t.Fatalf("uninstall command should use panel-hosted nftables script: %s", cmd)
	}
	if !strings.Contains(cmd, "./uninstall_nftables.sh -y") {
		t.Fatalf("uninstall command should run non-interactively: %s", cmd)
	}
}

func TestGetUninstallCommandUsesGostScript(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	updateOrCreateConfig("ip", "https://panel.example.com:6365/")
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "node",
		Secret:      "secret-value",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeGost,
		CreatedTime: now,
		Status:      0,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetUninstallCommand(node.ID, "")
	if res.Code != 0 {
		t.Fatalf("GetUninstallCommand code=%d msg=%s", res.Code, res.Msg)
	}
	cmd, ok := res.Data.(string)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if !strings.Contains(cmd, "https://panel.example.com:6365/api/v1/node/install/uninstall_gost.sh") {
		t.Fatalf("uninstall command should use panel-hosted gost script: %s", cmd)
	}
	if !strings.Contains(cmd, "./uninstall_gost.sh -y") {
		t.Fatalf("uninstall command should run non-interactively: %s", cmd)
	}
}

func TestNodeVersionComparison(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "1.2.5", current: "1.2.4", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.2", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.4", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.5", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.6", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.7", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.8", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.9", want: true},
		{latest: latestNftNodeVersion, current: "nftables-go-1.3.10", want: true},
		{latest: latestNftNodeVersion, current: latestNftNodeVersion, want: false},
		{latest: "1.2.5", current: "1.2.5", want: false},
		{latest: "1.2.5", current: "1.3.0", want: false},
		{latest: "1.2.5", current: "", want: false},
	}
	for _, tc := range cases {
		if got := isVersionNewer(tc.latest, tc.current); got != tc.want {
			t.Fatalf("isVersionNewer(%q, %q)=%v want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestGetAllNodesIncludesUpgradeInfo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	now := time.Now().UnixMilli()
	version := "1.2.4"
	node := model.Node{
		Name:        "node",
		Secret:      "secret-value",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeGost,
		Version:     &version,
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	res := GetAllNodes()
	if res.Code != 0 {
		t.Fatalf("GetAllNodes code=%d msg=%s", res.Code, res.Msg)
	}
	nodes, ok := res.Data.([]model.Node)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	var got *model.Node
	for i := range nodes {
		if nodes[i].ID == node.ID {
			got = &nodes[i]
			break
		}
	}
	if got == nil {
		t.Fatal("created node missing from list")
	}
	if got.Secret != "" {
		t.Fatal("node secret should be hidden")
	}
	if got.LatestVersion != latestGostNodeVersion || !got.UpgradeAvailable {
		t.Fatalf("unexpected upgrade info: latest=%q available=%v", got.LatestVersion, got.UpgradeAvailable)
	}
}
