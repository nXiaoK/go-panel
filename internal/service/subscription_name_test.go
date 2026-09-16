package service

import (
	"encoding/base64"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestProxyNodeManualNameSurvivesReports(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatal(err)
	}
	updateOrCreateConfig(subAPIKeyConfigName, "test-key")
	report := proxyReport("machine-a-snell", "snell", 17039)
	report.Options = `{"provider":"PrimeSecurity","region":"JP","protocolLabel":"Snell"}`
	if res := ReportProxyNode("test-key", report); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	load := func() model.ProxyNode {
		t.Helper()
		var node model.ProxyNode
		if err := model.DB.Where("external_id = ?", report.ExternalID).First(&node).Error; err != nil {
			t.Fatal(err)
		}
		return node
	}
	node := load()
	if node.Name != "PrimeSecurity-JP-Snell" || node.NameCustomized {
		t.Fatalf("unexpected initial name: %q (customized=%v)", node.Name, node.NameCustomized)
	}
	update := dto.ProxyNodeUpdateDto{
		ID: node.ID, ExternalID: node.ExternalID, Name: node.Name,
		Protocol: node.Protocol, Server: node.Server, Port: node.Port,
		Password: node.Password, Options: node.Options,
	}
	if res := UpdateProxyNode(update); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	if load().NameCustomized {
		t.Fatal("saving unchanged name should retain automatic naming")
	}
	update.Name = "  日本主力-01  "
	if res := UpdateProxyNode(update); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	node = load()
	if node.Name != "日本主力-01" || !node.NameCustomized {
		t.Fatalf("manual name not saved: %q (customized=%v)", node.Name, node.NameCustomized)
	}
	if err := model.Init(dbPath); err != nil {
		t.Fatal(err)
	}
	// 上报仍更新连接信息与分类信息，但不能覆盖持久化的手动名称。
	report.Port = 17040
	report.Password = "rotated-secret"
	report.Options = `{"provider":"PrimeSecurity","region":"HK","protocolLabel":"Snell"}`
	if res := ReportProxyNode("test-key", report); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	node = load()
	if node.Name != "日本主力-01" || !node.NameCustomized || node.Port != report.Port || node.Password != report.Password || node.Options != trimJSON(report.Options) {
		t.Fatal("report must preserve custom name while updating connection and classification")
	}
	if body := renderSurge("[Proxy]\n{{PROXIES}}", []renderNode{withResolvedAddress(node)}); !strings.Contains(body, "日本主力-01 = snell") {
		t.Fatal("subscription did not use saved name")
	}
	update.Name = "  "
	if res := UpdateProxyNode(update); res.Code == 0 {
		t.Fatal("blank manual name should be rejected")
	}
	if load().Name != "日本主力-01" {
		t.Fatal("rejected update changed saved name")
	}
}

func TestProxyNodeNameMigrationPreservesExistingNode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatal(err)
	}
	node := model.ProxyNode{ExternalID: "legacy-snell", Name: "Provider-JP-Snell", Protocol: "snell", Server: "legacy.example.com", Port: 443, Password: "secret"}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	// 模拟升级前的表结构，启动迁移不得改名、丢失凭据或默认锁定旧节点。
	if err := model.DB.Migrator().DropColumn(&model.ProxyNode{}, "NameCustomized"); err != nil {
		t.Fatal(err)
	}
	if err := model.Init(dbPath); err != nil {
		t.Fatal(err)
	}
	var migrated model.ProxyNode
	if err := model.DB.First(&migrated, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.NameCustomized || migrated.Name != node.Name || migrated.Password != node.Password || migrated.ExternalID != node.ExternalID {
		t.Fatal("migration did not preserve existing node and default naming behavior")
	}
}

func TestAutomaticProxyNodeNameStillFollowsReport(t *testing.T) {
	node := model.ProxyNode{ID: 1, Name: "Provider-JP-Snell"}
	applyNodeReport(&node, dto.ProxyNodeReportDto{
		Protocol: "snell", Options: `{"provider":"Provider","region":"HK","protocolLabel":"Snell"}`,
	}, 1)
	if node.Name != "Provider-HK-Snell" {
		t.Fatalf("automatic name was not refreshed: %q", node.Name)
	}
}

func TestSubscriptionsDisambiguateDuplicateNames(t *testing.T) {
	nodes := []renderNode{
		{ProxyNode: model.ProxyNode{ID: 11, Name: "Provider-JP-SS", Protocol: "ss", Method: "aes-256-gcm", Password: "secret"}, Address: "a.example.com", Port: 8388},
		{ProxyNode: model.ProxyNode{ID: 22, Name: "Provider-JP-SS", Protocol: "ss", Method: "aes-256-gcm", Password: "secret"}, Address: "b.example.com", Port: 8388},
		{ProxyNode: model.ProxyNode{ID: 33, Name: "Provider-JP-SS-11", Protocol: "ss", Method: "aes-256-gcm", Password: "secret"}, Address: "c.example.com", Port: 8388},
	}
	want := []string{"Provider-JP-SS-11-2", "Provider-JP-SS-22", "Provider-JP-SS-11"}
	t.Run("surge", func(t *testing.T) {
		body := renderSurge("[Proxy]\n{{PROXIES}}", nodes)
		for i, name := range want {
			if !strings.Contains(body, name+" = ss, "+nodes[i].Address+",") {
				t.Fatalf("missing distinct proxy %q: %s", name, body)
			}
		}
	})
	t.Run("clash", func(t *testing.T) {
		// 策略组引用必须与去重后的节点名称一致，否则客户端无法选中节点。
		body := renderClash("proxies: []\nproxy-groups:\n  - name: Proxy\n    type: select\n    proxies: []\nrules: [MATCH,Proxy]", nodes)
		var root struct {
			Proxies []struct{ Name string }      `yaml:"proxies"`
			Groups  []struct{ Proxies []string } `yaml:"proxy-groups"`
		}
		if err := yaml.Unmarshal([]byte(body), &root); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, proxy := range root.Proxies {
			got = append(got, proxy.Name)
		}
		if !reflect.DeepEqual(got, want) || len(root.Groups) != 1 || !reflect.DeepEqual(root.Groups[0].Proxies, want) {
			t.Fatalf("proxy/group names disagree: %s", body)
		}
	})
	t.Run("v2rayn", func(t *testing.T) {
		decoded, err := base64.StdEncoding.DecodeString(renderV2rayN(nodes))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, link := range strings.Split(string(decoded), "\n") {
			u, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, u.Fragment)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("link names=%v, want %v", got, want)
		}
	})
	if nodes[0].Name != "Provider-JP-SS" || nodes[1].Name != "Provider-JP-SS" {
		t.Fatal("rendering must not modify source names")
	}
	reordered := uniqueSubscriptionNodeNames([]renderNode{nodes[2], nodes[1], nodes[0]}, strings.TrimSpace)
	for i, j := range []int{2, 1, 0} {
		if reordered[i].Name != want[j] {
			t.Fatal("reordering nodes changed exported names")
		}
	}
}

func TestSurgeNamesRemainUniqueAfterSanitizing(t *testing.T) {
	nodes := []renderNode{
		{ProxyNode: model.ProxyNode{ID: 1, Name: "JP=Snell", Protocol: "snell"}, Address: "a.example.com", Port: 443},
		{ProxyNode: model.ProxyNode{ID: 2, Name: "JP-Snell", Protocol: "snell"}, Address: "b.example.com", Port: 443},
	}
	body := renderSurge("[Proxy]\n{{PROXIES}}", nodes)
	if !strings.Contains(body, "JP-Snell-1 = snell, a.example.com") || !strings.Contains(body, "JP-Snell-2 = snell, b.example.com") {
		t.Fatalf("names collided after sanitizing: %s", body)
	}
}
