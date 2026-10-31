package service

import (
	"crypto/ecdh"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func reportedRestoreFixture(t *testing.T, source, protocol string, port int) model.ProxyNode {
	t.Helper()
	options, _ := json.Marshal(map[string]string{"source": "vless-server.sh", "sourceHost": source, "originalProtocol": protocol, "originalPort": strconv.Itoa(port)})
	node := model.ProxyNode{ExternalID: source + "-" + protocol, Name: source + " " + protocol, NameCustomized: true, Server: "192.0.2.17", Port: port, Protocol: normalizeProtocol(protocol), Options: string(options), UUID: "d5324324-f1ea-46e7-99f7-2d167fe73062", Username: "original-user", Password: "original-password", Method: "aes-256-gcm", SNI: "example.com", Network: "tcp", Sort: 19, Status: 0}
	if protocol == "vless" || protocol == "vless-xhttp" {
		node.Security = "reality"
		node.ShortID = "abcdef12"
		node.PublicKey = "original-public-key"
	}
	if protocol == "vless-xhttp" {
		node.Network = "xhttp"
		node.Path = "/original-xhttp"
	}
	if protocol == "snell" {
		node.SnellVersion = 4
	}
	if protocol == "snell-v5" {
		node.SnellVersion = 5
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&node).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	node.Status = 0
	return node
}

func TestReportedRestorePreservesCredentialsAndRegeneratesRealityWithoutWrites(t *testing.T) {
	setupProxyBackupTest(t)
	source := "node-reinstalled"
	protocols := []string{"ss2022", "ss-legacy", "snell", "snell-v5", "socks", "vless", "vless-xhttp", "vmess"}
	nodes := []model.ProxyNode{}
	for i, protocol := range protocols {
		nodes = append(nodes, reportedRestoreFixture(t, source, protocol, 21000+i))
	}
	profile := model.SubscriptionProfile{Name: "原订阅", Token: "original-profile-token", Status: 1}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	link := model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: nodes[5].ID, Sort: 23}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	metadata := ListReportedNodeRestores("backup-test-key")
	if metadata.Code != 0 {
		t.Fatal(metadata)
	}
	views := metadata.Data.([]ReportedHostRestoreView)
	if len(views) != 1 || len(views[0].Nodes) != len(protocols) || !strings.Contains(views[0].RestoreCommand, "--flux-panel-restore-reported") {
		t.Fatalf("unexpected metadata: %#v", views)
	}
	for _, response := range []any{metadata, GetSubscriptionSettings()} {
		raw, _ := json.Marshal(response)
		// 普通 settings 的 nodes 原本包含客户端凭据；只检查新的恢复元数据，不能夹带合成快照。
		if strings.Contains(string(raw), "private_key") || strings.Contains(string(raw), "snapshot") {
			t.Fatalf("metadata leaked new private material: %s", raw)
		}
	}
	raw, _ := json.Marshal(views)
	for _, secret := range []string{"original-password", "original-user", nodes[0].UUID, "original-public-key"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("restore metadata leaked %s", secret)
		}
	}
	res := RestoreReportedProxyNodes("backup-test-key", source)
	if res.Code != 0 {
		t.Fatal(res)
	}
	restored := res.Data.(ReportedNodeRestoreResult)
	var database map[string]map[string]map[string]any
	// 只解码协议集合，database.version 不是协议对象。
	var root map[string]json.RawMessage
	if err := json.Unmarshal(restored.Snapshot.Database, &root); err != nil {
		t.Fatal(err)
	}
	database = map[string]map[string]map[string]any{}
	for _, core := range []string{"xray", "singbox"} {
		var protocols map[string]map[string]any
		if err := json.Unmarshal(root[core], &protocols); err != nil {
			t.Fatal(err)
		}
		database[core] = protocols
	}
	for _, node := range nodes {
		var opts reportedRestoreOptions
		_ = json.Unmarshal([]byte(node.Options), &opts)
		core := "xray"
		if strings.HasPrefix(opts.OriginalProtocol, "snell") {
			core = "singbox"
		}
		config := database[core][opts.OriginalProtocol]
		if config["port"] != float64(node.Port) {
			t.Fatalf("port changed for %s: %#v", opts.OriginalProtocol, config)
		}
		switch opts.OriginalProtocol {
		case "ss2022", "ss-legacy", "socks":
			if config["password"] != node.Password {
				t.Fatalf("lost original password: %#v", config)
			}
		case "snell", "snell-v5":
			if config["psk"] != node.Password || !strings.Contains(restored.Snapshot.Files[opts.OriginalProtocol+".conf"], "psk = "+node.Password) {
				t.Fatalf("lost Snell PSK: %#v", config)
			}
			if strings.Contains(restored.Snapshot.Files[opts.OriginalProtocol+".conf"], "version =") != (opts.OriginalProtocol == "snell-v5") {
				t.Fatal("Snell version directive incompatible with original core")
			}
		case "vless", "vless-xhttp":
			if config["uuid"] != node.UUID || config["sni"] != node.SNI || config["short_id"] != node.ShortID {
				t.Fatalf("lost Reality client fields: %#v", config)
			}
			private, err := base64.RawURLEncoding.DecodeString(config["private_key"].(string))
			if err != nil {
				t.Fatal(err)
			}
			key, err := ecdh.X25519().NewPrivateKey(private)
			if err != nil {
				t.Fatal(err)
			}
			if got := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()); got != config["public_key"] || got == node.PublicKey {
				t.Fatal("invalid or unchanged regenerated Reality pair")
			}
		}
		var persisted model.ProxyNode
		if err := model.DB.First(&persisted, node.ID).Error; err != nil || !reflect.DeepEqual(persisted, node) {
			t.Fatalf("restore request changed persisted node before deployment: %#v err=%v", persisted, err)
		}
	}
	var count int64
	model.DB.Model(&model.ProxyNodeBackup{}).Count(&count)
	if count != 0 {
		t.Fatal("temporary rebuilt snapshot was persisted before node deployment")
	}
	// 部署成功后的上报复用旧身份；只有这一步才发布新公钥，不创建新节点或重绑订阅。
	vless := nodes[5]
	public := database["xray"]["vless"]["public_key"].(string)
	report := dto.ProxyNodeReportDto{ExternalID: vless.ExternalID, SourceHost: source, Protocol: "vless", Server: vless.Server, Port: vless.Port, UUID: vless.UUID, PublicKey: public, SNI: vless.SNI, ShortID: vless.ShortID, Security: "reality", Options: vless.Options}
	if res := ReportProxyNode("backup-test-key", report); res.Code != 0 {
		t.Fatal(res)
	}
	var after model.ProxyNode
	if err := model.DB.First(&after, vless.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.PublicKey != public || after.Name != vless.Name || after.Sort != vless.Sort || after.Status != 0 {
		t.Fatalf("restored report lost existing metadata: %#v", after)
	}
	var afterLink model.SubscriptionProfileNode
	if err := model.DB.First(&afterLink, link.ID).Error; err != nil || afterLink.ProxyNodeID != vless.ID || afterLink.Sort != link.Sort {
		t.Fatal("restored report changed subscription binding")
	}
}

func TestReportedRestoreRequiresReliableSourceAndPrefersFullBackup(t *testing.T) {
	backup := setupProxyBackupTest(t)
	first := reportedRestoreFixture(t, "node-first", "socks", 21001)
	second := reportedRestoreFixture(t, "node-second", "snell", 21002)
	missing := reportedRestoreFixture(t, "node-missing", "socks", 21003)
	if err := model.DB.Model(&missing).Update("options", `{"source":"vless-server.sh","originalProtocol":"socks"}`).Error; err != nil {
		t.Fatal(err)
	}
	derived := second
	derived.ID = 0
	derived.ExternalID = "relay-copy"
	derived.SourceProxyNodeID = &second.ID
	derived.RelayMode = "append"
	if err := model.DB.Create(&derived).Error; err != nil {
		t.Fatal(err)
	}
	backup.SourceHost = "node-first"
	if res := SaveProxyNodeBackup("backup-test-key", backup); res.Code != 0 {
		t.Fatal(res)
	}
	res := ListReportedNodeRestores("backup-test-key")
	if res.Code != 0 {
		t.Fatal(res)
	}
	views := res.Data.([]ReportedHostRestoreView)
	if len(views) != 2 {
		t.Fatalf("sources merged by shared IP or full backup/relay not excluded: %#v", views)
	}
	for _, view := range views {
		if view.SourceHost == "node-first" {
			t.Fatal("full snapshot source exposed unsafe rebuild command")
		}
		for _, node := range view.Nodes {
			if node.ID == first.ID || node.ID == derived.ID {
				t.Fatal("full snapshot or relay clone included")
			}
		}
		if view.SourceHost == "" && (view.RestoreCommand != "" || view.Nodes[0].Action != "unsupported") {
			t.Fatal("missing identity obtained recovery command")
		}
	}
	if res := RestoreReportedProxyNodes("backup-test-key", "node-first"); res.Code == 0 || !strings.Contains(res.Msg, "完整") {
		t.Fatal("full snapshot was silently replaced by reconstructed credentials")
	}
	if res := RestoreReportedProxyNodes("backup-test-key", "node-missing"); res.Code == 0 {
		t.Fatal("unverified identity accepted")
	}
	if res := RestoreReportedProxyNodes("bad-key", "node-second"); res.Code != 401 || res.Data != nil {
		t.Fatal("unauthorized restore exposed credentials")
	}
}

func TestReportedRestoreSkipsUnsupportedAndUnsafeSnell(t *testing.T) {
	setupProxyBackupTest(t)
	reportedRestoreFixture(t, "node-partial", "socks", 21001)
	reportedRestoreFixture(t, "node-partial", "snell-shadowtls", 21002)
	snell := reportedRestoreFixture(t, "node-partial", "snell", 21003)
	if err := model.DB.Model(&snell).Update("password", "secret\nlisten = 0.0.0.0:22").Error; err != nil {
		t.Fatal(err)
	}
	res := RestoreReportedProxyNodes("backup-test-key", "node-partial")
	if res.Code != 0 {
		t.Fatal(res)
	}
	restored := res.Data.(ReportedNodeRestoreResult)
	if strings.Contains(string(restored.Snapshot.Database), "snell") || len(restored.Snapshot.Files) != 0 {
		t.Fatal("unsupported protocol or unsafe Snell config deployed")
	}
	if joined := strings.Join(restored.Notices, " "); !strings.Contains(joined, "已跳过") || !strings.Contains(joined, "控制字符") {
		t.Fatalf("skipped protocols not disclosed: %v", restored.Notices)
	}
}

func TestReportedRestoreGeneratesTLSOnlyWhenOriginalPolicyAllows(t *testing.T) {
	setupProxyBackupTest(t)
	allowed := reportedRestoreFixture(t, "node-tls", "trojan", 21001)
	strict := reportedRestoreFixture(t, "node-tls", "vless-vision", 21002)
	if err := model.DB.Model(&allowed).Updates(map[string]any{"security": "tls", "allow_insecure": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&strict).Updates(map[string]any{"security": "tls", "allow_insecure": 0}).Error; err != nil {
		t.Fatal(err)
	}
	res := RestoreReportedProxyNodes("backup-test-key", "node-tls")
	if res.Code != 0 {
		t.Fatal(res)
	}
	restored := res.Data.(ReportedNodeRestoreResult)
	block, _ := pem.Decode([]byte(restored.Snapshot.Files["certs/server.crt"]))
	if block == nil {
		t.Fatal("missing generated PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname(allowed.SNI); err != nil {
		t.Fatal(err)
	}
	keyBlock, _ := pem.Decode([]byte(restored.Snapshot.Files["certs/server.key"]))
	if keyBlock == nil {
		t.Fatal("missing generated PEM private key")
	}
	if _, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(restored.Snapshot.Database), "vless-vision") {
		t.Fatal("strict TLS silently downgraded")
	}
	if !strings.Contains(strings.Join(restored.Notices, " "), "原订阅未允许") {
		t.Fatalf("strict TLS skip not disclosed: %v", restored.Notices)
	}
}

func TestReportedRestoreRejectsAmbiguousListenersAndWSFallback(t *testing.T) {
	setupProxyBackupTest(t)
	reportedRestoreFixture(t, "node-ws-master", "vless", 21001)
	ws := reportedRestoreFixture(t, "node-ws-master", "vless-ws", 21002)
	if err := model.DB.Model(&ws).Updates(map[string]any{"security": "tls", "network": "ws", "allow_insecure": 1, "path": "/ws"}).Error; err != nil {
		t.Fatal(err)
	}
	res := RestoreReportedProxyNodes("backup-test-key", "node-ws-master")
	if res.Code != 0 {
		t.Fatal(res)
	}
	restored := res.Data.(ReportedNodeRestoreResult)
	if strings.Contains(string(restored.Snapshot.Database), "vless-ws") || !strings.Contains(strings.Join(restored.Notices, " "), "回落拓扑") {
		t.Fatal("WS fallback dependency silently changed")
	}

	mapped := reportedRestoreFixture(t, "node-mapped", "socks", 21003)
	if err := model.DB.Model(&mapped).Update("port", 31003).Error; err != nil {
		t.Fatal(err)
	}
	if res := RestoreReportedProxyNodes("backup-test-key", "node-mapped"); res.Code == 0 {
		t.Fatal("ambiguous original/public port accepted")
	}

	reportedRestoreFixture(t, "node-collision", "socks", 21004)
	reportedRestoreFixture(t, "node-collision", "snell", 21004)
	reportedRestoreFixture(t, "node-collision", "ss-legacy", 21005)
	res = RestoreReportedProxyNodes("backup-test-key", "node-collision")
	if res.Code != 0 {
		t.Fatal(res)
	}
	restored = res.Data.(ReportedNodeRestoreResult)
	if strings.Contains(string(restored.Snapshot.Database), "socks") || strings.Contains(string(restored.Snapshot.Database), "snell") || !strings.Contains(strings.Join(restored.Notices, " "), "同一 TCP") {
		t.Fatal("conflicting TCP listeners deployed")
	}

	reportedRestoreFixture(t, "node-inconsistent", "socks", 21006)
	other := reportedRestoreFixture(t, "node-inconsistent", "snell", 21007)
	if err := model.DB.Model(&other).Update("server", "192.0.2.18").Error; err != nil {
		t.Fatal(err)
	}
	if res := RestoreReportedProxyNodes("backup-test-key", "node-inconsistent"); res.Code == 0 {
		t.Fatal("different servers under same identity silently merged")
	}
}
