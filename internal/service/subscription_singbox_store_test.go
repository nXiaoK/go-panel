package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestEnsureSubscriptionDefaultsCreatesSingboxProfileAndBackfillsNodes(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now().UnixMilli()
	vless := model.ProxyNode{
		ExternalID: "existing-vless", Name: "Existing VLESS", Protocol: "vless",
		Server: "vless.example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111",
		Status: 1, CreatedTime: now, UpdatedTime: now,
	}
	snell := model.ProxyNode{
		ExternalID: "existing-snell", Name: "Existing Snell", Protocol: "snell",
		Server: "snell.example.com", Port: 44000, Password: "psk", SnellVersion: 5,
		Status: 1, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&vless).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&snell).Error; err != nil {
		t.Fatal(err)
	}

	EnsureSubscriptionDefaults()

	var profile model.SubscriptionProfile
	if err := model.DB.Where("default_format = ?", "singbox").First(&profile).Error; err != nil {
		t.Fatalf("sing-box default profile: %v", err)
	}
	if profile.Name != defaultSingboxSubName || strings.TrimSpace(profile.SingboxTemplate) == "" {
		t.Fatalf("unexpected sing-box profile: %#v", profile)
	}
	var template map[string]interface{}
	if err := json.Unmarshal([]byte(profile.SingboxTemplate), &template); err != nil {
		t.Fatalf("default sing-box template is not strict JSON: %v", err)
	}
	if _, ok := template["outbounds"].([]interface{}); !ok {
		t.Fatalf("default sing-box template has no outbounds: %#v", template)
	}

	var vlessLinks, snellLinks int64
	model.DB.Model(&model.SubscriptionProfileNode{}).
		Where("subscription_id = ? AND proxy_node_id = ?", profile.ID, vless.ID).
		Count(&vlessLinks)
	model.DB.Model(&model.SubscriptionProfileNode{}).
		Where("subscription_id = ? AND proxy_node_id = ?", profile.ID, snell.ID).
		Count(&snellLinks)
	if vlessLinks != 1 || snellLinks != 0 {
		t.Fatalf("default sing-box links: vless=%d snell=%d", vlessLinks, snellLinks)
	}

	// 历史 Snell 节点应补入兼容 Mihomo/Shadowrocket 的默认 Clash 订阅。
	var clashProfile model.SubscriptionProfile
	if err := model.DB.Where("default_format = ?", "clash").First(&clashProfile).Error; err != nil {
		t.Fatalf("Clash default profile: %v", err)
	}
	var clashSnellLinks int64
	model.DB.Model(&model.SubscriptionProfileNode{}).
		Where("subscription_id = ? AND proxy_node_id = ?", clashProfile.ID, snell.ID).
		Count(&clashSnellLinks)
	if clashSnellLinks != 1 {
		t.Fatalf("default Clash Snell links=%d want=1", clashSnellLinks)
	}
}

func TestCreateSubscriptionProfilePersistsSingboxTemplateAndAlias(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	custom := `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	res := CreateSubscriptionProfile(dto.SubscriptionProfileDto{
		Name:            "SFA custom",
		DefaultFormat:   "sfa",
		SingboxTemplate: custom,
	})
	if res.Code != 0 {
		t.Fatalf("create profile: %s", res.Msg)
	}
	var profile model.SubscriptionProfile
	if err := model.DB.Where("name = ?", "SFA custom").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if profile.DefaultFormat != "singbox" || profile.SingboxTemplate != custom {
		t.Fatalf("stored profile = %#v", profile)
	}
}
