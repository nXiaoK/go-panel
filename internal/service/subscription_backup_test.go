package service

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func setupProxyBackupTest(t *testing.T) dto.ProxyNodeBackupDto {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	updateOrCreateConfig(subAPIKeyConfigName, "backup-test-key")
	updateOrCreateConfig("ip", "https://panel.example.com")
	return dto.ProxyNodeBackupDto{
		Version: 1, SourceHost: "node-before-reinstall", Server: "192.0.2.17",
		Database: json.RawMessage(`{"version":"4.0.0","xray":{"vless":{"port":"443","uuid":"old-uuid","private_key":"secret-reality-private-key","public_key":"original-public-key","short_id":"abcdef12","sni":"example.com"}},"singbox":{}}`),
		Files:    map[string]string{"certs/server.key": "secret-certificate-private-key"},
	}
}

func TestProxyNodeBackupRoundTripAndNoSecretInMetadata(t *testing.T) {
	req := setupProxyBackupTest(t)
	if res := SaveProxyNodeBackup("backup-test-key", req); res.Code != 0 {
		t.Fatal(res)
	}
	res := RestoreProxyNodeBackup("backup-test-key", req.SourceHost)
	if res.Code != 0 || !reflect.DeepEqual(res.Data, req) {
		t.Fatalf("restore did not retain original server credentials: %#v", res)
	}
	for _, metadata := range []any{ListProxyNodeBackups("backup-test-key"), GetSubscriptionSettings()} {
		raw, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"secret-reality", "secret-certificate", "private_key", "database", "snapshot"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("metadata leaked %q: %s", secret, raw)
			}
		}
		if !strings.Contains(string(raw), "--flux-panel-restore") || !strings.Contains(string(raw), req.SourceHost) {
			t.Fatalf("metadata omitted recovery target/command: %s", raw)
		}
	}
	if res := SaveProxyNodeBackup("wrong-key", req); res.Code != 401 {
		t.Fatalf("unauthorized backup accepted: %#v", res)
	}
	if res := RestoreProxyNodeBackup("", req.SourceHost); res.Code != 401 || res.Data != nil {
		t.Fatalf("unauthorized restore leaked backup: %#v", res)
	}
	if res := ListProxyNodeBackups("wrong-key"); res.Code != 401 || res.Data != nil {
		t.Fatalf("unauthorized list leaked targets: %#v", res)
	}
}

func TestProxyNodeBackupRejectsInvalidWithoutReplacingSnapshot(t *testing.T) {
	req := setupProxyBackupTest(t)
	if res := SaveProxyNodeBackup("backup-test-key", req); res.Code != 0 {
		t.Fatal(res)
	}
	cases := map[string]func(*dto.ProxyNodeBackupDto){
		"version":        func(b *dto.ProxyNodeBackupDto) { b.Version = 2 },
		"identity":       func(b *dto.ProxyNodeBackupDto) { b.SourceHost = "node-'$(id)'" },
		"path traversal": func(b *dto.ProxyNodeBackupDto) { b.Files = map[string]string{"../authorized_keys": "x"} },
		"absolute path":  func(b *dto.ProxyNodeBackupDto) { b.Files = map[string]string{"/etc/passwd": "x"} },
		"shell config":   func(b *dto.ProxyNodeBackupDto) { b.Files = map[string]string{"sub-panel.env": "x"} },
		"script":         func(b *dto.ProxyNodeBackupDto) { b.Files = map[string]string{"watchdog.sh": "x"} },
		"empty database": func(b *dto.ProxyNodeBackupDto) { b.Database = json.RawMessage(`{"xray":{},"singbox":{}}`) },
		"unknown protocol": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{"unknown":{"port":443}},"singbox":{}}`)
		},
		"invalid core": func(b *dto.ProxyNodeBackupDto) { b.Database = json.RawMessage(`{"xray":[],"singbox":{}}`) },
		"invalid port": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{"vless":{"port":65536}},"singbox":{}}`)
		},
		"missing reality private key": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(strings.Replace(string(b.Database), `"private_key":"secret-reality-private-key",`, "", 1))
		},
		"missing TLS certificate": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{"trojan":{"port":443,"password":"old-password","sni":"example.com"}},"singbox":{}}`)
		},
		"missing SS method": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{"ss2022":{"port":443,"password":"old-password"}},"singbox":{}}`)
		},
		"missing WS path": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{"vless-ws":{"port":443,"uuid":"old-uuid","sni":"example.com"}},"singbox":{}}`)
			b.Files = map[string]string{"certs/server.crt": "original-certificate", "certs/server.key": "original-key"}
		},
		"unsupported WARP routing": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(strings.TrimSuffix(string(b.Database), "}") + `,"routing_rules":[{"outbound":"warp"}]}`)
		},
		"unsafe service argument": func(b *dto.ProxyNodeBackupDto) {
			b.Database = json.RawMessage(`{"xray":{},"singbox":{"anytls":{"port":443,"password":"$(touch /tmp/injected)"}}}`)
		},
		"oversized": func(b *dto.ProxyNodeBackupDto) {
			b.Files = map[string]string{"certs/server.key": strings.Repeat("x", MaxProxyNodeBackupSize)}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := req
			mutate(&invalid)
			if res := SaveProxyNodeBackup("backup-test-key", invalid); res.Code == 0 {
				t.Fatalf("invalid backup accepted: %s", name)
			}
			if res := RestoreProxyNodeBackup("backup-test-key", req.SourceHost); res.Code != 0 || !reflect.DeepEqual(res.Data, req) {
				t.Fatal("failed save replaced previous valid backup")
			}
		})
	}
	updated := req
	updated.Server = "192.0.2.18"
	if res := SaveProxyNodeBackup("backup-test-key", updated); res.Code != 0 {
		t.Fatal(res)
	}
	var count int64
	model.DB.Model(&model.ProxyNodeBackup{}).Count(&count)
	if count != 1 {
		t.Fatalf("backup created duplicate target: %d", count)
	}
	if res := RestoreProxyNodeBackup("backup-test-key", req.SourceHost); res.Code != 0 || !reflect.DeepEqual(res.Data, updated) {
		t.Fatal("latest backup not returned")
	}
}

func TestRestoredHostReportKeepsSubscriptionBindings(t *testing.T) {
	req := setupProxyBackupTest(t)
	report := dto.ProxyNodeReportDto{SourceHost: req.SourceHost, Protocol: "vless", Server: req.Server, Port: 443, UUID: "old-uuid"}
	res := ReportProxyNode("backup-test-key", report)
	if res.Code != 0 {
		t.Fatal(res)
	}
	node := res.Data.(model.ProxyNode)
	if err := model.DB.Model(&node).Updates(map[string]any{"name": "保留名称", "name_customized": true, "sort": 17, "status": 0, "forward_id": 42}).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{Name: "原订阅", Token: "keep-existing-link", Status: 1}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	link := model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, Sort: 23}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	if res := SaveProxyNodeBackup("backup-test-key", req); res.Code != 0 {
		t.Fatal(res)
	}
	restored := RestoreProxyNodeBackup("backup-test-key", req.SourceHost).Data.(dto.ProxyNodeBackupDto)
	report.SourceHost = restored.SourceHost
	report.Server = "192.0.2.18"
	res = ReportProxyNode("backup-test-key", report)
	if res.Code != 0 {
		t.Fatal(res)
	}
	after := res.Data.(model.ProxyNode)
	if after.ID != node.ID || after.Name != "保留名称" || after.Sort != 17 || after.UUID != "old-uuid" || after.Status != 0 || after.ForwardID == nil || *after.ForwardID != 42 {
		t.Fatalf("restored report changed identity, credentials or customization: %#v", after)
	}
	var afterLink model.SubscriptionProfileNode
	if err := model.DB.First(&afterLink, link.ID).Error; err != nil || afterLink.ProxyNodeID != node.ID || afterLink.Sort != 23 {
		t.Fatalf("subscription binding changed: %#v, %v", afterLink, err)
	}
	var count int64
	model.DB.Model(&model.ProxyNode{}).Count(&count)
	if count != 1 {
		t.Fatalf("restored host created duplicate protocol node: %d", count)
	}
}

func TestProxyNodeBackupAllowsRealityWithPlainWSFallback(t *testing.T) {
	req := setupProxyBackupTest(t)
	var database map[string]any
	if err := json.Unmarshal(req.Database, &database); err != nil {
		t.Fatal(err)
	}
	database["xray"].(map[string]any)["vless-ws"] = map[string]any{"uuid": "old-ws-uuid", "port": 8080, "path": "/ws", "sni": "example.com"}
	req.Database, _ = json.Marshal(database)
	req.Files = map[string]string{}
	if res := SaveProxyNodeBackup("backup-test-key", req); res.Code != 0 {
		t.Fatalf("Reality fallback does not need TLS certificate: %#v", res)
	}
	delete(database["xray"].(map[string]any), "vless")
	req.Database, _ = json.Marshal(database)
	if res := SaveProxyNodeBackup("backup-test-key", req); res.Code == 0 {
		t.Fatal("standalone WS accepted without TLS certificate")
	}
}

func TestProxyNodeBackupAcceptsEveryScriptProtocol(t *testing.T) {
	// 覆盖脚本真实保存的核心归属、字符串端口和独立服务参数，防止合法配置无法生成恢复备份。
	cases := []struct {
		protocol string
		core     string
		config   string
		files    []string
	}{
		{"vless", "xray", `"uuid":"id","private_key":"private","public_key":"public","short_id":12345678,"sni":"example.com"`, nil},
		{"vless-xhttp", "xray", `"uuid":"id","private_key":"private","public_key":"public","short_id":"abcdef12","sni":"example.com","path":"/"`, nil},
		{"vmess", "xray", `"uuid":"id"`, nil},
		{"vless-ws", "xray", `"uuid":"id","sni":"example.com","path":"/ws"`, []string{"certs/server.crt", "certs/server.key"}},
		{"vmess-ws", "xray", `"uuid":"id","sni":"example.com","path":"/ws"`, []string{"certs/server.crt", "certs/server.key"}},
		{"vless-vision", "xray", `"uuid":"id","sni":"example.com"`, []string{"certs/server.crt", "certs/server.key"}},
		{"trojan", "xray", `"password":"password","sni":"example.com"`, []string{"certs/server.crt", "certs/server.key"}},
		{"socks", "xray", `"username":123456,"password":654321`, nil},
		{"ss2022", "xray", `"password":"password","method":"2022-blake3-aes-128-gcm"`, nil},
		{"ss-legacy", "xray", `"password":"password","method":"aes-256-gcm"`, nil},
		{"hy2", "singbox", `"password":"password","sni":"example.com"`, []string{"certs/hy2/server.crt", "certs/hy2/server.key"}},
		{"tuic", "singbox", `"uuid":"id","password":"password","sni":"example.com"`, []string{"certs/tuic/server.crt", "certs/tuic/server.key"}},
		{"anytls", "singbox", `"password":123456`, nil},
		{"snell", "singbox", `"psk":123456`, []string{"snell.conf"}},
		{"snell-v5", "singbox", `"psk":"password"`, []string{"snell-v5.conf"}},
		{"snell-shadowtls", "singbox", `"psk":"password","sni":"example.com","stls_password":"password","snell_backend_port":"1443"`, []string{"snell-shadowtls.conf"}},
		{"snell-v5-shadowtls", "singbox", `"psk":"password","sni":"example.com","stls_password":123456,"snell_backend_port":1443`, []string{"snell-v5-shadowtls.conf"}},
		{"ss2022-shadowtls", "singbox", `"password":"password","method":"2022-blake3-aes-256-gcm","sni":"example.com","stls_password":"password","ss_backend_port":"1443"`, []string{"ss2022-shadowtls-backend.json"}},
		{"naive", "singbox", `"username":"user","password":"password","domain":"example.com"`, []string{"Caddyfile"}},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			core := map[string]json.RawMessage{"xray": json.RawMessage(`{}`), "singbox": json.RawMessage(`{}`)}
			core[tc.core] = json.RawMessage(`{"` + tc.protocol + `":{"port":"443",` + tc.config + `}}`)
			database, _ := json.Marshal(core)
			req := dto.ProxyNodeBackupDto{Version: 1, SourceHost: "node-valid-protocol", Server: "192.0.2.1", Database: database, Files: map[string]string{}}
			for _, path := range tc.files {
				req.Files[path] = "original configuration or certificate"
			}
			protocols, err := validateProxyNodeBackup(req)
			if err != nil || !reflect.DeepEqual(protocols, []string{tc.protocol}) {
				t.Fatalf("valid script config rejected: protocols=%v err=%v", protocols, err)
			}
			// 上传端必须拒绝脚本恢复端无法接收的缺失连接字段，避免坏快照覆盖已有可恢复配置。
			for _, field := range []string{"sni", "path", "method"} {
				var config map[string]json.RawMessage
				if err := json.Unmarshal([]byte(`{"port":"443",`+tc.config+`}`), &config); err != nil {
					t.Fatal(err)
				}
				if _, exists := config[field]; !exists || (tc.protocol == "vless-xhttp" && field == "path") {
					continue
				}
				delete(config, field)
				core[tc.core], _ = json.Marshal(map[string]any{tc.protocol: config})
				req.Database, _ = json.Marshal(core)
				if _, err := validateProxyNodeBackup(req); err == nil {
					t.Fatalf("snapshot accepted without %s needed by script restore", field)
				}
			}
		})
	}
}

func TestBuildVlessServerRestoreCommandQuotesAndRejectsUnsafeIdentity(t *testing.T) {
	setupProxyBackupTest(t)
	updateOrCreateConfig(githubDownloadProxyConfigName, "https://proxy.example.com/github/")
	command := BuildVlessServerRestoreCommand("https://panel.example.com/", "key'with-quote", "node-before-reinstall")
	for _, want := range []string{"--flux-panel-restore 'https://panel.example.com' 'key'\"'\"'with-quote' 'node-before-reinstall'", "FLUX_GITHUB_PROXY='https://proxy.example.com/github'"} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing safely quoted value %q: %s", want, command)
		}
	}
	if command := BuildVlessServerRestoreCommand("", "", "node-$(id)"); command != "" {
		t.Fatalf("unsafe recovery identity accepted: %s", command)
	}
}
