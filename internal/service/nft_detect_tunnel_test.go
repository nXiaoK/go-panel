package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestFindMissingTunnelRulesDetectsStaleCommentedRules(t *testing.T) {
	inNode, outNode, tunnel := setupTunnelDetectDB(t)

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    20001,
		TargetHost: outNode.ServerIP,
		Protocol:   "tcp",
		ForwardID:  999,
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     20001,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "tcp",
		ForwardID:  1000,
	}}

	got := findMissingTunnelRules(inRules, outRules, map[string]bool{}, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one missing tunnel rule, got %d", len(got))
	}
	if got[0].TunnelID != tunnel.ID {
		t.Fatalf("TunnelID = %d, want %d", got[0].TunnelID, tunnel.ID)
	}
	if got[0].TargetHost != "192.0.2.10" || got[0].TargetPort != 443 {
		t.Fatalf("unexpected target: %s:%d", got[0].TargetHost, got[0].TargetPort)
	}
}

func TestFindMissingTunnelRulesSkipsCurrentDatabaseForward(t *testing.T) {
	inNode, outNode, tunnel := setupTunnelDetectDB(t)
	relayPort := 20001
	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "existing",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		OutPort:     &relayPort,
		RemoteAddr:  "192.0.2.10:443",
		Strategy:    "fifo",
		CreatedTime: time.Now().UnixMilli(),
		UpdatedTime: time.Now().UnixMilli(),
		Status:      1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    relayPort,
		TargetHost: outNode.ServerIP,
		Protocol:   "tcp",
		ForwardID:  999,
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     relayPort,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "tcp",
	}}

	existing := loadExistingTunnelForwards(inNode.ID, outNode.ID)
	got := findMissingTunnelRules(inRules, outRules, existing, inNode, outNode)
	if len(got) != 0 {
		t.Fatalf("expected no missing tunnel rules, got %d", len(got))
	}
}

func TestFindMissingTunnelRulesDetectsSamePortsWithDifferentTarget(t *testing.T) {
	inNode, outNode, tunnel := setupTunnelDetectDB(t)
	relayPort := 20001
	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "same-port-different-target",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		OutPort:     &relayPort,
		RemoteAddr:  "192.0.2.10:443",
		Strategy:    "fifo",
		CreatedTime: time.Now().UnixMilli(),
		UpdatedTime: time.Now().UnixMilli(),
		Status:      1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    relayPort,
		TargetHost: outNode.ServerIP,
		Protocol:   "tcp",
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     relayPort,
		OutPort:    8443,
		TargetHost: "192.0.2.20",
		Protocol:   "tcp",
	}}

	existing := loadExistingTunnelForwards(inNode.ID, outNode.ID)
	got := findMissingTunnelRules(inRules, outRules, existing, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one missing tunnel rule, got %d", len(got))
	}
	if got[0].TargetHost != "192.0.2.20" || got[0].TargetPort != 8443 {
		t.Fatalf("unexpected target: %s:%d", got[0].TargetHost, got[0].TargetPort)
	}
}

func TestFindMissingTunnelRulesMatchesRelayPortWhenTargetIsNodeAlias(t *testing.T) {
	inNode, outNode, _ := setupTunnelDetectDB(t)

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    20001,
		TargetHost: "po0-hk.example.com",
		Protocol:   "tcp",
		ForwardID:  9,
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     20001,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "tcp",
	}}

	got := findMissingTunnelRules(inRules, outRules, map[string]bool{}, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one missing tunnel rule, got %d", len(got))
	}
	if got[0].RelayPort != 20001 || got[0].TargetHost != "192.0.2.10" {
		t.Fatalf("unexpected relay match: relay=%d target=%s", got[0].RelayPort, got[0].TargetHost)
	}
}

func TestFindMissingTunnelRulesMatchesSelectedOutNodeRelayPort(t *testing.T) {
	inNode, outNode, _ := setupTunnelDetectDB(t)

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    20001,
		TargetHost: "10.42.1.123",
		Protocol:   "tcp",
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     20001,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "tcp",
	}}

	got := findMissingTunnelRules(inRules, outRules, map[string]bool{}, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one tunnel rule for selected out-node relay port, got %d", len(got))
	}
}

func TestFindMissingTunnelRulesMatchesRelayRuleByProtocol(t *testing.T) {
	inNode, outNode, _ := setupTunnelDetectDB(t)

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    20001,
		TargetHost: outNode.ServerIP,
		Protocol:   "tcp",
	}}
	outRules := []*gost.ParsedNftRule{
		{
			InPort:     20001,
			OutPort:    443,
			TargetHost: "tcp.example",
			Protocol:   "tcp",
		},
		{
			InPort:     20001,
			OutPort:    5353,
			TargetHost: "udp.example",
			Protocol:   "udp",
		},
	}

	got := findMissingTunnelRules(inRules, outRules, map[string]bool{}, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one missing tunnel rule, got %d", len(got))
	}
	if got[0].TargetHost != "tcp.example" || got[0].TargetPort != 443 {
		t.Fatalf("matched wrong protocol target: %s:%d", got[0].TargetHost, got[0].TargetPort)
	}
}

func TestFindMissingTunnelRulesDoesNotTreatTcpForwardAsUdpExisting(t *testing.T) {
	inNode, outNode, tunnel := setupTunnelDetectDB(t)
	tcpOnly := "tcp"
	tunnel.Protocol = &tcpOnly
	tunnel.UDPListenAddr = ""
	if err := model.DB.Save(tunnel).Error; err != nil {
		t.Fatalf("update tunnel: %v", err)
	}

	relayPort := 20001
	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "tcp-existing",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		OutPort:     &relayPort,
		RemoteAddr:  "192.0.2.10:443",
		Strategy:    "fifo",
		CreatedTime: time.Now().UnixMilli(),
		UpdatedTime: time.Now().UnixMilli(),
		Status:      1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	inRules := []*gost.ParsedNftRule{{
		InPort:     10001,
		OutPort:    relayPort,
		TargetHost: outNode.ServerIP,
		Protocol:   "udp",
	}}
	outRules := []*gost.ParsedNftRule{{
		InPort:     relayPort,
		OutPort:    443,
		TargetHost: "192.0.2.10",
		Protocol:   "udp",
	}}

	existing := loadExistingTunnelForwards(inNode.ID, outNode.ID)
	got := findMissingTunnelRules(inRules, outRules, existing, inNode, outNode)
	if len(got) != 1 {
		t.Fatalf("expected one missing udp tunnel rule, got %d", len(got))
	}
	if got[0].Protocol != "udp" {
		t.Fatalf("Protocol = %q, want udp", got[0].Protocol)
	}
}

func setupTunnelDetectDB(t *testing.T) (*model.Node, *model.Node, *model.Tunnel) {
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
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	return &inNode, &outNode, &tunnel
}
