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
