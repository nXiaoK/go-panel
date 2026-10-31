package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestRestoredRealityReportUpdatesRelaySubscriptionCredentials(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	updateOrCreateConfig(subAPIKeyConfigName, "report-sync-test-key")
	report := dto.ProxyNodeReportDto{
		ExternalID: "node-original-vless", SourceHost: "node-original", Protocol: "vless",
		Server: "192.0.2.12", Port: 443, UUID: "same-uuid", Security: "reality",
		PublicKey: "old-public-key", ShortID: "12345678", SNI: "example.com",
		AllowInsecure: true, Fingerprint: "chrome",
	}
	res := ReportProxyNode("report-sync-test-key", report)
	if res.Code != 0 {
		t.Fatal(res)
	}
	parent := res.Data.(model.ProxyNode)
	child := cloneProxyNodeForRelay(parent, parent.ID, 999, "自定义中转名称", 100)
	child.Sort = 17
	child.AllowInsecure = 0
	child.Fingerprint = "firefox"
	child.Options = `{"client-policy":"preserve"}`
	if err := model.DB.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	paused := cloneProxyNodeForRelay(parent, parent.ID, 998, "保持停用", 100)
	paused.Status = 0
	if err := model.DB.Create(&paused).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&paused).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	profile := model.SubscriptionProfile{Name: "中转订阅", Token: "relay-existing-subscription", Status: 1}
	if err := model.DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	binding := model.SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: child.ID, Sort: 9}
	if err := model.DB.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	report.PublicKey = "regenerated-public-key"
	if res := ReportProxyNode("report-sync-test-key", report); res.Code != 0 {
		t.Fatal(res)
	}
	var after model.ProxyNode
	if err := model.DB.First(&after, child.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.PublicKey != report.PublicKey || after.UUID != report.UUID || after.Port != 443 ||
		after.Name != child.Name || after.Sort != child.Sort || after.Status != child.Status ||
		after.ForwardID == nil || *after.ForwardID != 999 || after.ExternalID != child.ExternalID ||
		after.AllowInsecure != 0 || after.Fingerprint != child.Fingerprint || after.Options != child.Options {
		t.Fatalf("relay connection did not track restored origin or lost metadata: %#v", after)
	}
	var pausedAfter model.ProxyNode
	if err := model.DB.First(&pausedAfter, paused.ID).Error; err != nil || pausedAfter.Status != 0 || pausedAfter.PublicKey != report.PublicKey {
		t.Fatalf("paused relay changed state or retained stale key: %#v, %v", pausedAfter, err)
	}
	var afterBinding model.SubscriptionProfileNode
	if err := model.DB.First(&afterBinding, binding.ID).Error; err != nil || afterBinding != binding {
		t.Fatalf("subscription binding changed: %#v, %v", afterBinding, err)
	}
	body, _, err := RenderSubscription(profile.Token, "clash")
	if err != nil || !strings.Contains(body, report.PublicKey) || strings.Contains(body, "old-public-key") {
		t.Fatalf("relay subscription still advertises stale Reality credentials: %v\n%s", err, body)
	}
}

func TestReportedRelaySyncRollsBackOriginWhenChildUpdateFails(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	node := model.ProxyNode{ExternalID: "source", Name: "source", Protocol: "vless", Server: "192.0.2.1", Port: 443, PublicKey: "old"}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	child := cloneProxyNodeForRelay(node, node.ID, 99, "relay", 100)
	if err := model.DB.Create(&child).Error; err != nil {
		t.Fatal(err)
	}
	// 人为模拟副本写入失败，确认原节点不会先提交新公钥而留下不一致订阅。
	if err := model.DB.Exec(`CREATE TRIGGER reject_relay_update BEFORE UPDATE ON proxy_node
		WHEN NEW.source_proxy_node_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	node.PublicKey = "new"
	if err := saveReportedProxyNode(&node); err == nil {
		t.Fatal("expected relay update failure")
	}
	var after model.ProxyNode
	if err := model.DB.First(&after, node.ID).Error; err != nil || after.PublicKey != "old" {
		t.Fatalf("origin change escaped rollback: %#v, %v", after, err)
	}
}
