package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestFindMissingRulesSkipsTunnelForwardRulesInPortMode(t *testing.T) {
	inNode, outNode, portTunnel, tunnel := setupPortDetectDB(t)
	relayPort := 20001
	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "tunnel-forward",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		OutPort:     &relayPort,
		RemoteAddr:  "192.0.2.10:443",
		Strategy:    "fifo",
		CreatedTime: time.Now().UnixMilli(),
		UpdatedTime: time.Now().UnixMilli(),
		Status:      forwardStatusActive,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	scope := loadPortDetectScope(inNode.ID)
	rules := []*gost.ParsedNftRule{
		{
			InPort:     10001,
			OutPort:    relayPort,
			TargetHost: outNode.ServerIP,
			Protocol:   "tcp",
		},
		{
			InPort:     31001,
			OutPort:    443,
			TargetHost: "198.51.100.200",
			Protocol:   "tcp",
		},
	}

	got := findMissingRules(rules, map[string]*model.Forward{}, scope)
	if len(got) != 1 {
		t.Fatalf("expected only one port-forward rule, got %d", len(got))
	}
	if got[0].InPort != 31001 || got[0].TunnelID != portTunnel.ID {
		t.Fatalf("unexpected detected rule: %#v", got[0])
	}
}

func TestFindMissingRulesSkipsTunnelExitRulesInPortMode(t *testing.T) {
	_, outNode, _, tunnel := setupPortDetectDB(t)
	relayPort := 20001
	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "tunnel-forward",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		OutPort:     &relayPort,
		RemoteAddr:  "192.0.2.10:443",
		Strategy:    "fifo",
		CreatedTime: time.Now().UnixMilli(),
		UpdatedTime: time.Now().UnixMilli(),
		Status:      forwardStatusActive,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	scope := loadPortDetectScope(outNode.ID)
	rules := []*gost.ParsedNftRule{{
		InPort:     relayPort,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "tcp",
	}}

	got := findMissingRules(rules, map[string]*model.Forward{}, scope)
	if len(got) != 0 {
		t.Fatalf("expected tunnel exit rule to be skipped, got %#v", got)
	}
}

func setupPortDetectDB(t *testing.T) (*model.Node, *model.Node, *model.Tunnel, *model.Tunnel) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	now := time.Now().UnixMilli()
	inNode := model.Node{
		Name:        "PO0-SH",
		Secret:      "in-secret",
		IP:          "198.51.100.1",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      nodeStatusOnline,
	}
	outNode := model.Node{
		Name:        "PO0-HK",
		Secret:      "out-secret",
		IP:          "198.51.100.2",
		ServerIP:    "10.0.0.2",
		PortSta:     20001,
		PortEnd:     30000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      nodeStatusOnline,
	}
	if err := model.DB.Create(&inNode).Error; err != nil {
		t.Fatalf("create in node: %v", err)
	}
	if err := model.DB.Create(&outNode).Error; err != nil {
		t.Fatalf("create out node: %v", err)
	}

	protocol := "tcp"
	portTunnel := model.Tunnel{
		Name:          "port",
		InNodeID:      inNode.ID,
		InIP:          inNode.IP,
		OutNodeID:     inNode.ID,
		OutIP:         inNode.ServerIP,
		Type:          tunnelTypePortForward,
		Protocol:      &protocol,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	tunnel := model.Tunnel{
		Name:          "SH-HK",
		InNodeID:      inNode.ID,
		InIP:          inNode.IP,
		OutNodeID:     outNode.ID,
		OutIP:         outNode.ServerIP,
		Type:          tunnelTypeTunnelForward,
		Protocol:      &protocol,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&portTunnel).Error; err != nil {
		t.Fatalf("create port tunnel: %v", err)
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := model.DB.Model(&model.Tunnel{}).Where("id IN ?", []int64{portTunnel.ID, tunnel.ID}).Update("udp_listen_addr", "").Error; err != nil {
		t.Fatalf("make detect tunnels single-protocol: %v", err)
	}
	portTunnel.UDPListenAddr = ""
	tunnel.UDPListenAddr = ""

	return &inNode, &outNode, &portTunnel, &tunnel
}
