package service

import (
	"path/filepath"
	"testing"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestNormalizeGostRemoteAddressesCanonicalizesDNSAndIPv6(t *testing.T) {
	got, err := normalizeGostRemoteAddresses("EXAMPLE.COM:080,[2001:0db8::1]:443")
	if err != nil {
		t.Fatalf("normalize Gost targets: %v", err)
	}
	if got != "example.com:80,[2001:db8::1]:443" {
		t.Fatalf("normalized targets=%q", got)
	}
}

func TestSyncGostEntryForwardRejectsMalformedLegacyRemoteBeforeCommand(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	oldUpdate := updateGostServiceCommand
	defer func() { updateGostServiceCommand = oldUpdate }()
	calls := 0
	updateGostServiceCommand = func(int64, string, int, *int64, string, int, *model.Tunnel, string, string) ws.GostResult {
		calls++
		return ws.GostResult{Msg: "OK"}
	}

	forward := &model.Forward{ID: 11, UserID: 22, InPort: 8080, RemoteAddr: "example.com:80,198.51.100.1;bad:443"}
	tunnel := &model.Tunnel{ID: 33, Type: tunnelTypePortForward, TCPListenAddr: "0.0.0.0"}
	err := syncGostEntryForward(forward, tunnel, &model.Node{ID: 44})
	if err == nil || err.Error() != "转发目标地址格式错误" {
		t.Fatalf("sync error=%v, want pre-command target validation error", err)
	}
	if calls != 0 {
		t.Fatalf("malformed raw target reached node command %d times", calls)
	}
}

func TestSyncGostExitForwardRejectsMalformedLegacyRemoteBeforeCommand(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	oldUpdate := updateGostRemoteServiceCommand
	defer func() { updateGostRemoteServiceCommand = oldUpdate }()
	calls := 0
	updateGostRemoteServiceCommand = func(int64, string, int, string, string, string, string) ws.GostResult {
		calls++
		return ws.GostResult{Msg: "OK"}
	}

	forward := &model.Forward{ID: 11, UserID: 22, RemoteAddr: "[2001:db8::1]:443,host name:80"}
	tunnel := &model.Tunnel{ID: 33, Type: tunnelTypeTunnelForward}
	member := model.ForwardExitMember{OutPort: 10000}
	err := syncGostExitForward(forward, tunnel, &model.Node{ID: 44}, member)
	if err == nil || err.Error() != "转发目标地址格式错误" {
		t.Fatalf("sync error=%v, want pre-command target validation error", err)
	}
	if calls != 0 {
		t.Fatalf("malformed raw target reached node command %d times", calls)
	}
}

func TestSyncGostEntryForwardSendsOnlyNormalizedTarget(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	oldUpdate := updateGostServiceCommand
	defer func() { updateGostServiceCommand = oldUpdate }()
	var received string
	updateGostServiceCommand = func(_ int64, _ string, _ int, _ *int64, remoteAddr string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		received = remoteAddr
		return ws.GostResult{Msg: "OK"}
	}

	forward := &model.Forward{ID: 11, UserID: 22, InPort: 8080, RemoteAddr: "EXAMPLE.COM:080,[2001:0db8::1]:443"}
	tunnel := &model.Tunnel{ID: 33, Type: tunnelTypePortForward, TCPListenAddr: "0.0.0.0"}
	if err := syncGostEntryForward(forward, tunnel, &model.Node{ID: 44}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if received != "example.com:80,[2001:db8::1]:443" {
		t.Fatalf("node command target=%q", received)
	}
}

func TestForwardExitChainTargetsSkipsMalformedLegacyNodeAddress(t *testing.T) {
	members := []model.ForwardExitMember{
		{OutNodeID: 1, OutPort: 8443},
		{OutNodeID: 2, OutPort: 9443},
	}
	nodes := map[int64]model.Node{
		1: {ID: 1, ServerIP: "2001:0db8::1"},
		2: {ID: 2, ServerIP: "198.51.100.2;bad"},
	}
	if got := forwardExitChainTargets(members, nodes); got != "[2001:db8::1]:8443" {
		t.Fatalf("chain targets=%q, want only canonical valid target", got)
	}
}
