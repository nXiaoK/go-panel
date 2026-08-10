package service

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

type quotaFixture struct {
	User   CurrentUser
	Tunnel model.Tunnel
}

// setupQuotaFixture creates an nftables entry node (stubbed refresh) and a
// user whose forward quota is `quota`, on a tunnel with permission rows.
func setupQuotaFixture(t *testing.T, quota int) quotaFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	stubNftRefresh(t, func(int64) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	})

	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	node := model.Node{
		Name: "quota-node", Secret: "quota-node-secret", IP: "192.0.2.40", ServerIP: "192.0.2.40",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	user := model.User{
		User: "quota-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 100,
		FlowResetTime: 1, Num: quota, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tunnel := model.Tunnel{
		Name: "quota-tunnel", TrafficRatio: 1, InNodeID: node.ID, InIP: node.IP,
		OutNodeID: node.ID, OutIP: node.ServerIP, Type: tunnelTypePortForward, Flow: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0", CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	ut := model.UserTunnel{
		UserID: user.ID, TunnelID: tunnel.ID, Num: quota, Flow: 100,
		FlowResetTime: 1, ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&ut).Error; err != nil {
		t.Fatalf("create user tunnel: %v", err)
	}
	return quotaFixture{
		User:   CurrentUser{UserID: user.ID, RoleID: 1, UserName: user.User},
		Tunnel: tunnel,
	}
}

// TestConcurrentCreateForwardCannotExceedQuota proves the quota re-check and
// forward insert are serialized in one transaction: two racing creates against
// quota=1 admit exactly one forward.
func TestConcurrentCreateForwardCannotExceedQuota(t *testing.T) {
	fixture := setupQuotaFixture(t, 1)
	start := make(chan struct{})
	results := make(chan result.R, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			results <- CreateForward(fixture.User, dto.ForwardDto{
				Name: fmt.Sprintf("f-%d", i), TunnelID: fixture.Tunnel.ID,
				RemoteAddr: "192.0.2.10:80",
			})
		}(i)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if (<-results).Code == 0 {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("success=%d, want 1", success)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("user_id = ?", fixture.User.UserID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("forward rows=%d, want 1", count)
	}
}

// TestDuplicateUserTunnelAssignmentRejectedByUniqueIndex proves the composite
// unique index backstops the application-level duplicate guard.
func TestDuplicateUserTunnelAssignmentRejectedByUniqueIndex(t *testing.T) {
	fixture := setupQuotaFixture(t, 3)
	expires := time.Now().Add(time.Hour).UnixMilli()
	dup := model.UserTunnel{
		UserID: fixture.User.UserID, TunnelID: fixture.Tunnel.ID, Num: 1, Flow: 10,
		FlowResetTime: 1, ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&dup).Error; err == nil {
		t.Fatal("duplicate user_tunnel identity accepted despite unique index")
	}
}

// TestDuplicateUsernameRejectedByUniqueIndex proves the user.user unique index
// closes the TOCTOU window left by the app-level duplicate check.
func TestDuplicateUsernameRejectedByUniqueIndex(t *testing.T) {
	setupQuotaFixture(t, 1)
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	dup := model.User{
		User: "quota-user", Pwd: "unused", RoleID: 1, ExpTime: &expires, Flow: 1,
		FlowResetTime: 1, Num: 1, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&dup).Error; err == nil {
		t.Fatal("duplicate username accepted despite unique index")
	}
}
