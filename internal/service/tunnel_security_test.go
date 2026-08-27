package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestCreateTunnelRejectsUnknownTypeWithoutPanic(t *testing.T) {
	initTunnelSecurityTestDB(t)

	assertCreateTunnelResultWithoutPanic(t, dto.TunnelDto{
		Name:     "invalid-type",
		InNodeID: 1,
		Type:     3,
	}, "隧道类型参数错误")
}

func TestCreateTunnelRequiresValidOutNodeIDForTunnelForward(t *testing.T) {
	initTunnelSecurityTestDB(t)
	zero := int64(0)
	negative := int64(-1)

	for _, tt := range []struct {
		name      string
		outNodeID *int64
	}{
		{name: "nil", outNodeID: nil},
		{name: "zero", outNodeID: &zero},
		{name: "negative", outNodeID: &negative},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertCreateTunnelResultWithoutPanic(t, dto.TunnelDto{
				Name:      "missing-out-" + tt.name,
				InNodeID:  1,
				OutNodeID: tt.outNodeID,
				Type:      tunnelTypeTunnelForward,
			}, "隧道转发必须选择出口节点")
		})
	}
}

func TestCreateTunnelSupportsThreeDistinctNftNodes(t *testing.T) {
	initTunnelSecurityTestDB(t)
	now := time.Now().UnixMilli()
	relay := createTunnelSecurityNode(t, "tunnel-security-relay", "192.0.2.20", forwardModeNftables, now)
	exit := createTunnelSecurityNode(t, "tunnel-security-exit", "192.0.2.30", forwardModeNftables, now)

	res := CreateTunnel(dto.TunnelDto{
		Name: "three-node", InNodeID: 1, RelayNodeID: &relay.ID, OutNodeID: &exit.ID,
		Type: tunnelTypeTunnelForward, Flow: 2, Protocol: "tcp+udp",
	})
	if res.Code != 0 {
		t.Fatalf("CreateTunnel failed: %s", res.Msg)
	}
	var tunnel model.Tunnel
	if err := model.DB.Where("name = ?", "three-node").First(&tunnel).Error; err != nil {
		t.Fatalf("load tunnel: %v", err)
	}
	if tunnel.RelayNodeID == nil || *tunnel.RelayNodeID != relay.ID {
		t.Fatalf("relay node=%v, want %d", tunnel.RelayNodeID, relay.ID)
	}
	if tunnel.RelayIP == nil || *tunnel.RelayIP != relay.ServerIP {
		t.Fatalf("relay ip=%v, want %s", tunnel.RelayIP, relay.ServerIP)
	}
	if tunnel.Protocol == nil || *tunnel.Protocol != "tcp+udp" {
		t.Fatalf("protocol=%v, want tcp+udp", tunnel.Protocol)
	}
	modeChange := UpdateNode(dto.NodeUpdateDto{
		ID: relay.ID, Name: relay.Name, IP: relay.IP, ServerIP: relay.ServerIP,
		PortSta: relay.PortSta, PortEnd: relay.PortEnd, ForwardMode: forwardModeGost,
	})
	if modeChange.Code == 0 || modeChange.Msg != "该节点正用于三节点 nftables 串联隧道，不能切换转发模式" {
		t.Fatalf("relay mode change code=%d msg=%q", modeChange.Code, modeChange.Msg)
	}
}

func TestCreateTunnelRejectsDuplicateOrMixedModeRelay(t *testing.T) {
	initTunnelSecurityTestDB(t)
	now := time.Now().UnixMilli()
	exit := createTunnelSecurityNode(t, "tunnel-security-exit", "192.0.2.30", forwardModeNftables, now)
	gostRelay := createTunnelSecurityNode(t, "tunnel-security-gost-relay", "192.0.2.40", forwardModeGost, now)
	ipv6Relay := createTunnelSecurityNode(t, "tunnel-security-ipv6-relay", "2001:db8::20", forwardModeNftables, now)
	entryID := int64(1)

	assertCreateTunnelResultWithoutPanic(t, dto.TunnelDto{
		Name: "duplicate-relay", InNodeID: entryID, RelayNodeID: &entryID, OutNodeID: &exit.ID,
		Type: tunnelTypeTunnelForward, Flow: 2, Protocol: "tcp+udp",
	}, "入口、中继和出口节点不能重复")
	assertCreateTunnelResultWithoutPanic(t, dto.TunnelDto{
		Name: "mixed-relay", InNodeID: entryID, RelayNodeID: &gostRelay.ID, OutNodeID: &exit.ID,
		Type: tunnelTypeTunnelForward, Flow: 2, Protocol: "tcp+udp",
	}, "三节点串联仅支持全部节点使用 nftables 模式")
	assertCreateTunnelResultWithoutPanic(t, dto.TunnelDto{
		Name: "ipv6-relay", InNodeID: entryID, RelayNodeID: &ipv6Relay.ID, OutNodeID: &exit.ID,
		Type: tunnelTypeTunnelForward, Flow: 2, Protocol: "tcp+udp",
	}, "三节点串联当前仅支持 IPv4 节点地址")
}

func initTunnelSecurityTestDB(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "tunnel-security-entry",
		Secret:      "tunnel-security-secret",
		IP:          "192.0.2.10",
		ServerIP:    "192.0.2.10",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      nodeStatusOnline,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.ID != 1 {
		t.Fatalf("entry node ID=%d, want 1", node.ID)
	}
}

func createTunnelSecurityNode(t *testing.T, name, serverIP, mode string, now int64) model.Node {
	t.Helper()
	node := model.Node{
		Name: name, Secret: name + "-secret", IP: serverIP, ServerIP: serverIP,
		PortSta: 20001, PortEnd: 30000, ForwardMode: mode, CreatedTime: now, Status: nodeStatusOnline,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	return node
}

func assertCreateTunnelResultWithoutPanic(t *testing.T, req dto.TunnelDto, wantMsg string) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("CreateTunnel panicked: %v", recovered)
		}
	}()
	res := CreateTunnel(req)
	if res.Code == 0 || res.Msg != wantMsg {
		t.Fatalf("CreateTunnel returned code=%d msg=%q, want error %q", res.Code, res.Msg, wantMsg)
	}
}
