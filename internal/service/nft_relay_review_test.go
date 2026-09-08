package service

import (
	crand "crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestMoveThreeNodeForwardAllocatesPortOnNewRelay(t *testing.T) {
	fx := setupNftRelayFixture(t)
	now := time.Now().UnixMilli()
	relay := createRelayTestNode(t, "new-relay", "192.0.2.40", 30001, 30002, now)
	tunnel := fx.tunnel
	tunnel.ID, tunnel.Name = 0, "new-relay-tunnel"
	tunnel.RelayNodeID, tunnel.RelayIP = &relay.ID, &relay.ServerIP
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	other := fx.forward
	other.ID, other.Name, other.TunnelID = 0, "occupied-relay-port", tunnel.ID
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	member := fx.member
	member.ID, member.ForwardID = 0, other.ID
	if err := model.DB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	// 暂停转发迁移也必须遵守新中继的端口占用，恢复后才不会覆盖其他规则。
	if err := model.DB.Model(&fx.forward).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	res := UpdateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, dto.ForwardUpdateDto{
		ID: fx.forward.ID, Name: fx.forward.Name, TunnelID: tunnel.ID, RemoteAddr: fx.forward.RemoteAddr,
	})
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	rows := loadPersistedForwardExitMembers(fx.forward.ID)
	if len(rows) != 1 || rows[0].RelayPort != 30002 {
		t.Fatalf("moved members=%+v, want free port 30002 on new relay", rows)
	}
}

func TestThreeNodeManualExitReservesRetainedPortsBeforeNewMembers(t *testing.T) {
	fx := setupNftRelayFixture(t)
	if err := model.DB.Model(&fx.relay).Updates(map[string]interface{}{"port_sta": 30001, "port_end": 30002}).Error; err != nil {
		t.Fatal(err)
	}
	exit := createRelayTestNode(t, "new-manual-exit", "192.0.2.40", 41000, 41000, time.Now().UnixMilli())
	// 固定随机源，使旧实现必定先选到后续成员保留的 30001；测试结束恢复。
	original := crand.Reader
	crand.Reader = strings.NewReader(strings.Repeat("\x00", 128))
	t.Cleanup(func() { crand.Reader = original })
	fx.forward.ExitMode = exitModeManual
	rows, msg := saveForwardExitMembers(&fx.forward, &fx.tunnel, []dto.ForwardExitMemberDto{
		{OutNodeID: exit.ID, Active: true, Weight: 1},
		{OutNodeID: fx.exit.ID, Weight: 1},
	}, &fx.forward.ID)
	if msg != "" {
		t.Fatal(msg)
	}
	if len(rows) != 2 || rows[0].RelayPort != 30002 || rows[1].RelayPort != fx.member.RelayPort {
		t.Fatalf("members=%+v, want new port 30002 and retained port 30001", rows)
	}
}

func TestThreeNodeUpdateNodeRejectsIPv6ForEveryRole(t *testing.T) {
	for _, role := range []string{"entry", "relay", "exit", "manual-exit", "empty-tunnel"} {
		t.Run(role, func(t *testing.T) {
			fx := setupNftRelayFixture(t)
			node := fx.entry
			switch role {
			case "relay":
				node = fx.relay
			case "exit":
				node = fx.exit
			case "manual-exit":
				node = createRelayTestNode(t, "manual-exit", "192.0.2.40", 41000, 41099, time.Now().UnixMilli())
				member := fx.member
				member.ID, member.OutNodeID, member.Active, member.RelayPort = 0, node.ID, 0, 30002
				if err := model.DB.Create(&member).Error; err != nil {
					t.Fatal(err)
				}
			case "empty-tunnel":
				if err := deleteForwardRows(model.DB, fx.forward.ID); err != nil {
					t.Fatal(err)
				}
			}
			original := sendNftRefreshMessage
			t.Cleanup(func() { sendNftRefreshMessage = original })
			refreshes := 0
			sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
				refreshes++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			res := UpdateNode(dto.NodeUpdateDto{
				ID: node.ID, Name: node.Name, IP: node.IP, ServerIP: "2001:db8::40",
				PortSta: node.PortSta, PortEnd: node.PortEnd, ForwardMode: node.ForwardMode,
			})
			if res.Code == 0 || !strings.Contains(res.Msg, "IPv4") {
				t.Fatalf("IPv6 update accepted: %+v", res)
			}
			var saved model.Node
			if err := model.DB.First(&saved, node.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.ServerIP != node.ServerIP || refreshes != 0 {
				t.Fatalf("rejected update mutated state: IP=%s refreshes=%d", saved.ServerIP, refreshes)
			}
		})
	}
}

func TestThreeNodeDiagnosisUsesSelectedExitNode(t *testing.T) {
	fx := setupNftRelayFixture(t)
	exit := createRelayTestNode(t, "selected-exit", "192.0.2.40", 41000, 41099, time.Now().UnixMilli())
	if err := model.DB.Model(&fx.member).Updates(map[string]interface{}{"out_node_id": exit.ID, "out_port": 41001}).Error; err != nil {
		t.Fatal(err)
	}
	res := DiagnoseTunnel(fx.tunnel.ID)
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	checks := res.Data.(map[string]interface{})["results"].([]DiagnosisResult)
	if len(checks) != 3 || checks[1].NodeID != fx.relay.ID || checks[1].TargetIP != exit.ServerIP || checks[1].TargetPort != 41001 || checks[2].NodeID != exit.ID {
		t.Fatalf("diagnosis did not follow selected exit: %+v", checks)
	}
}

func TestThreeNodeUpdateRelayIPv4ResyncsEntry(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	deployed := map[int64][]string{}
	sendNftRefreshMessage = func(nodeID int64, data interface{}, _ string) ws.GostResult {
		deployed[nodeID] = data.(map[string]interface{})["rules"].([]string)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := UpdateNode(dto.NodeUpdateDto{
		ID: fx.relay.ID, Name: fx.relay.Name, IP: fx.relay.IP, ServerIP: "192.0.2.50",
		PortSta: fx.relay.PortSta, PortEnd: fx.relay.PortEnd, ForwardMode: fx.relay.ForwardMode,
	})
	if res.Code != 0 {
		t.Fatal(res.Msg)
	}
	assertRelayHopRules(t, filterRulesByComment(deployed[fx.entry.ID], fx.forward.ID), fx.forward.InPort, "192.0.2.50:30001", true)
	var saved model.Tunnel
	if err := model.DB.First(&saved, fx.tunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RelayIP == nil || *saved.RelayIP != "192.0.2.50" {
		t.Fatalf("relay address not saved: %+v", saved)
	}
	// 未参与三节点路径的节点继续支持 IPv6，限制不应影响其他隧道。
	node := createRelayTestNode(t, "unrelated-node", "192.0.2.60", 42000, 42099, time.Now().UnixMilli())
	res = UpdateNode(dto.NodeUpdateDto{
		ID: node.ID, Name: node.Name, IP: node.IP, ServerIP: "2001:db8::60",
		PortSta: node.PortSta, PortEnd: node.PortEnd, ForwardMode: node.ForwardMode,
	})
	if res.Code != 0 {
		t.Fatalf("unrelated IPv6 update rejected: %s", res.Msg)
	}
}

func TestForceDeleteForwardWithMissingTunnel(t *testing.T) {
	fx := setupNftRelayFixture(t)
	if err := model.DB.Delete(&fx.tunnel).Error; err != nil {
		t.Fatal(err)
	}
	if res := ForceDeleteForward(CurrentUser{UserID: fx.forward.UserID + 1, RoleID: 1}, fx.forward.ID); res.Code == 0 {
		t.Fatal("another user deleted the orphan forward")
	}
	res := ForceDeleteForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, fx.forward.ID)
	if res.Code != 0 {
		t.Fatalf("could not force-delete orphan forward: %s", res.Msg)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("id = ?", fx.forward.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("forward count=%d err=%v", count, err)
	}
	if rows := loadPersistedForwardExitMembers(fx.forward.ID); len(rows) != 0 {
		t.Fatalf("orphan exit members retained: %+v", rows)
	}
}

func TestForceDeleteForwardFailureKeepsExitMembers(t *testing.T) {
	for _, orphan := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing-tunnel", true: "missing-tunnel"}[orphan], func(t *testing.T) {
			fx := setupNftRelayFixture(t)
			if orphan {
				if err := model.DB.Delete(&fx.tunnel).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := model.DB.Exec("CREATE TRIGGER fail_forward_delete BEFORE DELETE ON forward BEGIN SELECT RAISE(ABORT, 'injected forward delete failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			original := sendNodeMessage
			t.Cleanup(func() { sendNodeMessage = original })
			sendNodeMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			res := ForceDeleteForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, fx.forward.ID)
			if res.Code == 0 {
				t.Fatal("force-delete ignored database failure")
			}
			var saved model.Forward
			if err := model.DB.First(&saved, fx.forward.ID).Error; err != nil {
				t.Fatal(err)
			}
			members := loadPersistedForwardExitMembers(saved.ID)
			if len(members) != 1 || members[0].RelayPort != fx.member.RelayPort || members[0].OutPort != fx.member.OutPort {
				t.Fatalf("failed delete lost port reservations: %+v", members)
			}
		})
	}
}
