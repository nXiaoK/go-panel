package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestNormalizeForwardExitMembersManualRequiresOneActive(t *testing.T) {
	tunnel := &model.Tunnel{Type: tunnelTypeTunnelForward, OutNodeID: 2}

	members, msg := normalizeForwardExitMembers(tunnel, exitModeManual, []dto.ForwardExitMemberDto{
		{OutNodeID: 2, Active: true},
		{OutNodeID: 3, Active: false},
	})
	if msg != "" {
		t.Fatalf("unexpected validation error: %s", msg)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	active := 0
	for _, member := range members {
		if member.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected exactly one active member, got %d", active)
	}

	_, msg = normalizeForwardExitMembers(tunnel, exitModeManual, []dto.ForwardExitMemberDto{
		{OutNodeID: 2, Active: false},
		{OutNodeID: 3, Active: false},
	})
	if msg != "手动负载需要选择当前出口节点" {
		t.Fatalf("unexpected error: %q", msg)
	}
}

func TestCreateForwardPersistsExitMembers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	inNode := createForwardExitNode(t, "entry", "10.0.0.1", 30000, 30099, "gost", now)
	outA := createForwardExitNode(t, "exit-a", "10.0.0.2", 31000, 31099, "gost", now)
	outB := createForwardExitNode(t, "exit-b", "10.0.0.3", 32000, 32099, "gost", now)
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name:          "multi-exit",
		InNodeID:      inNode.ID,
		InIP:          inNode.IP,
		OutNodeID:     outA.ID,
		OutIP:         outA.ServerIP,
		Type:          tunnelTypeTunnelForward,
		Protocol:      &protocol,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	inPort, outPort, msg := allocatePorts(&tunnel, nil, nil)
	if msg != "" {
		t.Fatalf("allocate ports: %s", msg)
	}
	forward := model.Forward{
		UserID:       1,
		UserName:     "admin_user",
		Name:         "manual-exit",
		TunnelID:     tunnel.ID,
		InPort:       inPort,
		OutPort:      outPort,
		RemoteAddr:   "192.168.10.10:80",
		Strategy:     "fifo",
		ExitMode:     exitModeManual,
		ExitStrategy: "fifo",
		CreatedTime:  now,
		UpdatedTime:  now,
		Status:       forwardStatusActive,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	normalized, msg := normalizeForwardExitMembers(&tunnel, exitModeManual, []dto.ForwardExitMemberDto{
		{OutNodeID: outA.ID, Active: true},
		{OutNodeID: outB.ID, Active: false},
	})
	if msg != "" {
		t.Fatalf("normalize members: %s", msg)
	}
	if _, msg := saveForwardExitMembers(&forward, &tunnel, normalized, &forward.ID); msg != "" {
		t.Fatalf("save members: %s", msg)
	}

	var members []model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).Order("id ASC").Find(&members).Error; err != nil {
		t.Fatalf("load members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0].OutPort == 0 || members[1].OutPort == 0 {
		t.Fatalf("expected allocated out ports: %#v", members)
	}
	if members[0].OutPort == members[1].OutPort {
		t.Fatalf("members should have distinct out ports: %#v", members)
	}
	active := 0
	for _, member := range members {
		if member.Active == 1 {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected exactly one active persisted member, got %d", active)
	}
}

func createForwardExitNode(t *testing.T, name, serverIP string, portStart, portEnd int, mode string, now int64) model.Node {
	t.Helper()
	node := model.Node{
		Name:        name,
		Secret:      name + "-secret",
		IP:          serverIP,
		ServerIP:    serverIP,
		PortSta:     portStart,
		PortEnd:     portEnd,
		ForwardMode: mode,
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	return node
}
