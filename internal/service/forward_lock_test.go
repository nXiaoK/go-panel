package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

type lockFixtureNode struct {
	user   CurrentUser
	tunnel model.Tunnel
	nodeID int64
}

func createNftNodeWithTunnel(t *testing.T, name string, portBase int) lockFixtureNode {
	t.Helper()
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	node := model.Node{
		Name: name, Secret: name + "-secret", IP: "192.0.2." + name, ServerIP: "192.0.2." + name,
		PortSta: portBase, PortEnd: portBase + 1000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	user := model.User{
		User: name + "-user", Pwd: "x", RoleID: 1, ExpTime: &expires, Flow: 1000,
		FlowResetTime: 1, Num: 100, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	tunnel := model.Tunnel{
		Name: name + "-tunnel", TrafficRatio: 1, InNodeID: node.ID, InIP: node.IP,
		OutNodeID: node.ID, OutIP: node.ServerIP, Type: tunnelTypePortForward, Flow: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel %s: %v", name, err)
	}
	ut := model.UserTunnel{UserID: user.ID, TunnelID: tunnel.ID, Num: 100, Flow: 1000, FlowResetTime: 1, ExpTime: &expires, Status: 1}
	if err := model.DB.Create(&ut).Error; err != nil {
		t.Fatalf("create user tunnel %s: %v", name, err)
	}
	return lockFixtureNode{
		user:   CurrentUser{UserID: user.ID, RoleID: 1, UserName: user.User},
		tunnel: tunnel,
		nodeID: node.ID,
	}
}

// TestBlockedNodeDoesNotHoldAllocationLock proves that a CreateForward stuck in
// remote nft refresh for node A does not hold the global allocation lock, so a
// concurrent CreateForward for node B completes before A is released.
func TestBlockedNodeDoesNotHoldAllocationLock(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	nodeA := createNftNodeWithTunnel(t, "a", 10000)
	nodeB := createNftNodeWithTunnel(t, "b", 20000)

	release := make(chan struct{})
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	sendNftRefreshMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID == nodeA.nodeID {
			<-release // block node A's remote refresh
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	aDone := make(chan int, 1)
	go func() {
		res := CreateForward(nodeA.user, dto.ForwardDto{Name: "a-fwd", TunnelID: nodeA.tunnel.ID, RemoteAddr: "192.0.2.100:80"})
		aDone <- res.Code
	}()

	// B must be able to create while A is blocked in remote work.
	bResult := make(chan int, 1)
	go func() {
		res := CreateForward(nodeB.user, dto.ForwardDto{Name: "b-fwd", TunnelID: nodeB.tunnel.ID, RemoteAddr: "192.0.2.101:80"})
		bResult <- res.Code
	}()

	select {
	case code := <-bResult:
		if code != 0 {
			t.Fatalf("node B create failed while A blocked: code=%d", code)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("node B create blocked by node A's remote work (allocation lock too wide)")
	}

	close(release)
	select {
	case <-aDone:
	case <-time.After(3 * time.Second):
		t.Fatal("node A create did not finish after release")
	}
}
