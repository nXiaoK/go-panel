package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestDeleteUserDeletesForwardExitMembers(t *testing.T) {
	user, _, tunnel, forward := setupForwardWithExitMember(t)
	now := time.Now()
	tunnelTraffic := trafficTunnelHourlyFixture(user.ID, tunnel.ID, startOfLocalHour(now), 10, 20)
	if err := model.DB.Create(&tunnelTraffic).Error; err != nil {
		t.Fatalf("create user tunnel traffic: %v", err)
	}

	res := DeleteUser(user.ID)
	if res.Code != 0 {
		t.Fatalf("DeleteUser code=%d msg=%s", res.Code, res.Msg)
	}
	assertNoForwardRows(t, forward.ID)
	var trafficCount int64
	if err := model.DB.Model(&model.TrafficTunnelHourly{}).Where("user_id = ?", user.ID).
		Count(&trafficCount).Error; err != nil {
		t.Fatal(err)
	}
	if trafficCount != 0 {
		t.Fatalf("user tunnel traffic rows remain=%d", trafficCount)
	}
}

func TestRemoveUserTunnelDeletesForwardExitMembers(t *testing.T) {
	_, userTunnel, _, forward := setupForwardWithExitMember(t)
	originalService, originalChains, originalRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalService, originalChains, originalRemote
	})

	res := RemoveUserTunnel(userTunnel.ID)
	if res.Code != 0 {
		t.Fatalf("RemoveUserTunnel code=%d msg=%s", res.Code, res.Msg)
	}
	assertNoForwardRows(t, forward.ID)
}

func TestCheckForwardQuotaExcludesCurrentForwardFromUserTotal(t *testing.T) {
	user, userTunnel, tunnel, forward := setupForwardWithExitMember(t)

	msg := checkForwardQuota(user.ID, tunnel.ID, &userTunnel, &user, &forward.ID)
	if msg != "" {
		t.Fatalf("checkForwardQuota returned %q, want empty", msg)
	}
}

func setupForwardWithExitMember(t *testing.T) (model.User, model.UserTunnel, model.Tunnel, model.Forward) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })

	now := time.Now().UnixMilli()
	exp := now + int64(time.Hour/time.Millisecond)
	user := model.User{
		User:          "alice",
		Pwd:           "password",
		RoleID:        userRoleID,
		ExpTime:       &exp,
		Flow:          100,
		FlowResetTime: 1,
		Num:           1,
		CreatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	inNode := createForwardExitNode(t, "entry-cleanup", "10.0.1.1", 30000, 30099, "gost", now)
	outNode := createForwardExitNode(t, "exit-cleanup", "10.0.1.2", 31000, 31099, "gost", now)
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name:          "cleanup-tunnel",
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

	userTunnel := model.UserTunnel{
		UserID:        user.ID,
		TunnelID:      tunnel.ID,
		Num:           1,
		Flow:          100,
		FlowResetTime: 1,
		ExpTime:       &exp,
		Status:        1,
	}
	if err := model.DB.Create(&userTunnel).Error; err != nil {
		t.Fatalf("create user tunnel: %v", err)
	}

	outPort := 31001
	forward := model.Forward{
		UserID:       user.ID,
		UserName:     user.User,
		Name:         "cleanup-forward",
		TunnelID:     tunnel.ID,
		InPort:       30001,
		OutPort:      &outPort,
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
	member := model.ForwardExitMember{
		ForwardID:   forward.ID,
		OutNodeID:   outNode.ID,
		OutPort:     outPort,
		Weight:      1,
		Status:      1,
		Active:      1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	if err := model.DB.Create(&member).Error; err != nil {
		t.Fatalf("create exit member: %v", err)
	}
	return user, userTunnel, tunnel, forward
}

func assertNoForwardRows(t *testing.T, forwardID int64) {
	t.Helper()
	var forwardCount, memberCount int64
	model.DB.Model(&model.Forward{}).Where("id = ?", forwardID).Count(&forwardCount)
	model.DB.Model(&model.ForwardExitMember{}).Where("forward_id = ?", forwardID).Count(&memberCount)
	if forwardCount != 0 || memberCount != 0 {
		t.Fatalf("forward rows remain: forward=%d exitMembers=%d", forwardCount, memberCount)
	}
}
