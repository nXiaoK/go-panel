package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestGetAllForwardsScansJoinedFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "nft-node",
		Secret:      "secret",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: "nftables",
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	protocol := "tcp"
	tunnel := model.Tunnel{
		Name:          "nft-tunnel",
		InNodeID:      node.ID,
		InIP:          node.IP,
		OutNodeID:     node.ID,
		OutIP:         node.ServerIP,
		Type:          tunnelTypePortForward,
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

	forward := model.Forward{
		UserID:      1,
		UserName:    "admin_user",
		Name:        "web",
		TunnelID:    tunnel.ID,
		InPort:      8080,
		RemoteAddr:  "192.168.1.10:80",
		Strategy:    "fifo",
		CreatedTime: now,
		UpdatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	res := GetAllForwards(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin_user"})
	if res.Code != 0 {
		t.Fatalf("GetAllForwards code=%d msg=%s", res.Code, res.Msg)
	}
	list, ok := res.Data.([]dto.ForwardWithTunnel)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 forward, got %d", len(list))
	}

	got := list[0]
	if got.InPort != 8080 || got.RemoteAddr != "192.168.1.10:80" {
		t.Fatalf("forward fields not scanned: inPort=%d remoteAddr=%q", got.InPort, got.RemoteAddr)
	}
	if got.InIP == nil || *got.InIP != "1.2.3.4" {
		t.Fatalf("joined inIp not scanned: %#v", got.InIP)
	}
	if got.TunnelName == nil || *got.TunnelName != "nft-tunnel" {
		t.Fatalf("joined tunnelName not scanned: %#v", got.TunnelName)
	}
	if got.Inx != 1 {
		t.Fatalf("legacy zero inx should be normalized to 1, got %d", got.Inx)
	}
}

func TestCreateForwardRejectsExhaustedUserFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "entry",
		Secret:      "secret",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: "nftables",
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name:          "limited",
		InNodeID:      node.ID,
		InIP:          node.IP,
		OutNodeID:     node.ID,
		OutIP:         node.ServerIP,
		Type:          tunnelTypePortForward,
		Protocol:      &protocol,
		Flow:          10,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	exp := time.Now().Add(24 * time.Hour).UnixMilli()
	user := model.User{
		User:          "limited-user",
		Pwd:           "unused",
		RoleID:        userRoleID,
		ExpTime:       &exp,
		Flow:          1,
		InFlow:        bytesToGB,
		FlowResetTime: 0,
		Num:           10,
		CreatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	ut := model.UserTunnel{
		UserID:        user.ID,
		TunnelID:      tunnel.ID,
		Num:           10,
		Flow:          10,
		FlowResetTime: 0,
		ExpTime:       &exp,
		Status:        1,
	}
	if err := model.DB.Create(&ut).Error; err != nil {
		t.Fatalf("create user tunnel: %v", err)
	}

	res := CreateForward(CurrentUser{UserID: user.ID, RoleID: userRoleID, UserName: user.User}, dto.ForwardDto{
		Name:       "blocked",
		TunnelID:   tunnel.ID,
		RemoteAddr: "192.168.1.10:80",
	})
	if res.Code == 0 {
		t.Fatalf("CreateForward succeeded for exhausted user")
	}
	if res.Msg != "用户总流量已用完" {
		t.Fatalf("CreateForward msg=%q, want 用户总流量已用完", res.Msg)
	}
}

func TestUpdateForwardRejectsActiveForwardWhenUserFlowExhausted(t *testing.T) {
	cu, forward, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, true)

	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID:         forward.ID,
		Name:       "edited-active",
		TunnelID:   tunnel.ID,
		RemoteAddr: "192.168.1.11:8080",
		Strategy:   "fifo",
	})
	if res.Code == 0 {
		t.Fatalf("UpdateForward succeeded for exhausted active forward")
	}
	if res.Msg != "用户总流量已用完" {
		t.Fatalf("UpdateForward msg=%q, want 用户总流量已用完", res.Msg)
	}
}

func TestUpdateForwardKeepsPausedForwardPausedWhenUserFlowExhausted(t *testing.T) {
	cu, forward, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusPaused, true)

	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID:         forward.ID,
		Name:       "edited-paused",
		TunnelID:   tunnel.ID,
		RemoteAddr: "192.168.1.12:8080",
		Strategy:   "fifo",
	})
	if res.Code != 0 {
		t.Fatalf("UpdateForward code=%d msg=%s", res.Code, res.Msg)
	}

	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil {
		t.Fatalf("load forward: %v", err)
	}
	if got.Status != forwardStatusPaused {
		t.Fatalf("status=%d, want paused", got.Status)
	}
	if got.RemoteAddr != "192.168.1.12:8080" {
		t.Fatalf("remoteAddr=%q, want updated remote", got.RemoteAddr)
	}
}

func TestUpdateForwardRetriesErrorForwardRuntimeDeployment(t *testing.T) {
	cu, forward, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusError, false)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", tunnel.InNodeID).
		Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatalf("switch node mode: %v", err)
	}

	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID:         forward.ID,
		Name:       "edited-error",
		TunnelID:   tunnel.ID,
		RemoteAddr: "192.168.1.13:8080",
		Strategy:   "fifo",
	})
	if res.Code == 0 {
		t.Fatalf("UpdateForward silently succeeded for error forward without a node session")
	}
	if !strings.Contains(res.Msg, "节点不在线") {
		t.Fatalf("UpdateForward msg=%q, want aggregated node offline failure", res.Msg)
	}
}

func setupForwardUpdateFlowScenario(t *testing.T, forwardStatus int, exhausted bool) (CurrentUser, model.Forward, model.Tunnel) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = model.Close()
	})

	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "update-flow-node",
		Secret:      "secret-update-flow",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: "nftables",
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name:          "update-flow-tunnel",
		InNodeID:      node.ID,
		InIP:          node.IP,
		OutNodeID:     node.ID,
		OutIP:         node.ServerIP,
		Type:          tunnelTypePortForward,
		Protocol:      &protocol,
		Flow:          10,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	exp := time.Now().Add(24 * time.Hour).UnixMilli()
	inFlow := int64(0)
	if exhausted {
		inFlow = bytesToGB
	}
	user := model.User{
		User:          "update-flow-user",
		Pwd:           "unused",
		RoleID:        userRoleID,
		ExpTime:       &exp,
		Flow:          1,
		InFlow:        inFlow,
		FlowResetTime: 0,
		Num:           10,
		CreatedTime:   now,
		Status:        1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	ut := model.UserTunnel{
		UserID:        user.ID,
		TunnelID:      tunnel.ID,
		Num:           10,
		Flow:          10,
		FlowResetTime: 0,
		ExpTime:       &exp,
		Status:        1,
	}
	if err := model.DB.Create(&ut).Error; err != nil {
		t.Fatalf("create user tunnel: %v", err)
	}
	forward := model.Forward{
		UserID:      user.ID,
		UserName:    user.User,
		Name:        "update-flow-forward",
		TunnelID:    tunnel.ID,
		InPort:      10001,
		RemoteAddr:  "192.168.1.10:80",
		Strategy:    "fifo",
		CreatedTime: now,
		UpdatedTime: now,
		Status:      forwardStatus,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	return CurrentUser{UserID: user.ID, RoleID: userRoleID, UserName: user.User}, forward, tunnel
}

func TestGetAllForwardsBackfillsLegacyZeroIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}

	now := time.Now().UnixMilli()
	forwards := []model.Forward{
		{
			UserID:      1,
			UserName:    "admin_user",
			Name:        "older",
			TunnelID:    1,
			InPort:      10001,
			RemoteAddr:  "192.168.1.10:80",
			Strategy:    "fifo",
			CreatedTime: now,
			UpdatedTime: now,
			Status:      1,
		},
		{
			UserID:      1,
			UserName:    "admin_user",
			Name:        "newer",
			TunnelID:    1,
			InPort:      10002,
			RemoteAddr:  "192.168.1.11:80",
			Strategy:    "fifo",
			CreatedTime: now + 1,
			UpdatedTime: now + 1,
			Status:      1,
		},
	}
	if err := model.DB.Create(&forwards).Error; err != nil {
		t.Fatalf("create forwards: %v", err)
	}

	res := GetAllForwards(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin_user"})
	if res.Code != 0 {
		t.Fatalf("GetAllForwards code=%d msg=%s", res.Code, res.Msg)
	}
	list, ok := res.Data.([]dto.ForwardWithTunnel)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 forwards, got %d", len(list))
	}
	if list[0].Name != "newer" || list[0].Inx != 1 {
		t.Fatalf("first forward=%s inx=%d, want newer/1", list[0].Name, list[0].Inx)
	}
	if list[1].Name != "older" || list[1].Inx != 2 {
		t.Fatalf("second forward=%s inx=%d, want older/2", list[1].Name, list[1].Inx)
	}
}

func TestAllocatePortForNodeChoosesRandomFreePort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "random-port-node",
		Secret:      "secret-random-port",
		IP:          "1.2.3.4",
		ServerIP:    "10.0.0.1",
		PortSta:     41000,
		PortEnd:     41099,
		ForwardMode: "nftables",
		CreatedTime: now,
		Status:      1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	seen := map[int]bool{}
	for i := 0; i < 30; i++ {
		port := allocatePortForNode(node.ID, nil)
		if port == nil {
			t.Fatalf("expected an available port")
		}
		if *port < node.PortSta || *port > node.PortEnd {
			t.Fatalf("allocated port %d outside range %d-%d", *port, node.PortSta, node.PortEnd)
		}
		seen[*port] = true
	}
	if len(seen) == 1 && seen[node.PortSta] {
		t.Fatalf("automatic allocation always picked the lowest port: %#v", seen)
	}
}
