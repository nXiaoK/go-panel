package service

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

type forwardExitPolicyFixture struct {
	user   CurrentUser
	tunnel model.Tunnel
	other  model.Node
}

func setupForwardExitPolicyFixture(t *testing.T) forwardExitPolicyFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	stubNftRefresh(t, func(int64) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} })
	now := time.Now().UnixMilli()
	entry := createForwardExitNode(t, "policy-entry", "192.0.2.1", 30000, 30099, forwardModeNftables, now)
	defaultExit := createForwardExitNode(t, "policy-default", "192.0.2.2", 31000, 31099, forwardModeNftables, now)
	other := createForwardExitNode(t, "policy-other", "192.0.2.3", 32000, 32099, forwardModeNftables, now)
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name: "policy-tunnel", InNodeID: entry.ID, InIP: entry.IP,
		OutNodeID: defaultExit.ID, OutIP: defaultExit.ServerIP, Type: tunnelTypeTunnelForward,
		Protocol: &protocol, Flow: 1, TrafficRatio: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{
		User: "exit-policy-user", Pwd: "unused", RoleID: userRoleID, ExpTime: &expires,
		Flow: 100, Num: 10, FlowResetTime: 1, CreatedTime: now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	permission := model.UserTunnel{
		UserID: user.ID, TunnelID: tunnel.ID, Num: 10, Flow: 100,
		FlowResetTime: 1, ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&permission).Error; err != nil {
		t.Fatal(err)
	}
	return forwardExitPolicyFixture{
		user:   CurrentUser{UserID: user.ID, RoleID: userRoleID, UserName: user.User},
		tunnel: tunnel, other: other,
	}
}

func TestRegularUserCannotChooseArbitraryExitNodes(t *testing.T) {
	fx := setupForwardExitPolicyFixture(t)
	res := CreateForward(fx.user, dto.ForwardDto{
		Name: "custom-exit", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.10:443",
		ExitMode: exitModeManual, ExitMembers: []dto.ForwardExitMemberDto{{OutNodeID: fx.other.ID, Active: true}},
	})
	if res.Code == 0 || res.Msg != "普通用户不能自定义出口节点" {
		var members []model.ForwardExitMember
		model.DB.Find(&members)
		t.Fatalf("CreateForward=%+v, persisted exits=%+v; want custom exits rejected", res, members)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected create wrote forwards: count=%d err=%v", count, err)
	}
}

func TestRegularUserCreateUsesTunnelDefaultExit(t *testing.T) {
	for _, explicitDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "explicit default"}[explicitDefault], func(t *testing.T) {
			fx := setupForwardExitPolicyFixture(t)
			req := dto.ForwardDto{Name: "default-exit", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.10:443"}
			if explicitDefault {
				req.ExitMode = exitModeSingle
				req.ExitStrategy = exitStrategyFIFO
				req.ExitMembers = []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true, Weight: 9}}
			}
			if res := CreateForward(fx.user, req); res.Code != 0 {
				t.Fatalf("CreateForward=%+v", res)
			}
			var forward model.Forward
			if err := model.DB.First(&forward).Error; err != nil {
				t.Fatal(err)
			}
			if forward.ExitMode != exitModeSingle || forward.ExitStrategy != exitStrategyFIFO {
				t.Fatalf("default exit config=%s/%s", forward.ExitMode, forward.ExitStrategy)
			}
			assertForwardExitPolicyMembers(t, forward.ID, []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true, Weight: 1}})
		})
	}
}

func TestRegularUserCreateRejectsNonDefaultExitModeOrStrategy(t *testing.T) {
	for _, tc := range []struct {
		name, mode, strategy string
		members              bool
	}{
		{"balance without members", exitModeBalance, "", false},
		{"manual with default exit", exitModeManual, "", true},
		{"balance with default exit", exitModeBalance, exitStrategyRound, true},
		{"single with round strategy", exitModeSingle, exitStrategyRound, false},
		{"empty mode with hash strategy", "", exitStrategyHash, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupForwardExitPolicyFixture(t)
			req := dto.ForwardDto{Name: "non-default", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.10:443", ExitMode: tc.mode, ExitStrategy: tc.strategy}
			if tc.members {
				req.ExitMembers = []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true}}
			}
			if res := CreateForward(fx.user, req); res.Code == 0 || res.Msg != "普通用户不能自定义出口节点" {
				t.Fatalf("CreateForward=%+v, want explicit rejection", res)
			}
			var count int64
			if err := model.DB.Model(&model.Forward{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rejected create wrote forwards: count=%d err=%v", count, err)
			}
		})
	}
}

func createPausedForwardForExitPolicy(t *testing.T, fx forwardExitPolicyFixture) model.Forward {
	t.Helper()
	now := time.Now().UnixMilli()
	outPort := 31010
	forward := model.Forward{
		UserID: fx.user.UserID, UserName: fx.user.UserName, Name: "admin-configured", TunnelID: fx.tunnel.ID,
		InPort: 30010, OutPort: &outPort, RemoteAddr: "198.51.100.10:443", Strategy: "fifo",
		ExitMode: exitModeSingle, ExitStrategy: exitStrategyFIFO, Status: forwardStatusPaused,
		CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	return forward
}

func assertForwardExitPolicyMembers(t *testing.T, forwardID int64, want []dto.ForwardExitMemberDto) {
	t.Helper()
	var rows []model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forwardID).Order("out_node_id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got := make([]dto.ForwardExitMemberDto, 0, len(rows))
	for _, row := range rows {
		got = append(got, dto.ForwardExitMemberDto{OutNodeID: row.OutNodeID, Active: row.Active == 1, Weight: row.Weight})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted exit members=%+v, want %+v", got, want)
	}
}

func TestRegularUserUpdateKeepsAdminConfiguredExits(t *testing.T) {
	fx := setupForwardExitPolicyFixture(t)
	forward := createPausedForwardForExitPolicy(t, fx)
	// 自动出口负载需要 Gost 入口；暂停转发可验证保存权限，而不依赖在线代理。
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fx.tunnel.InNodeID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	members := []dto.ForwardExitMemberDto{
		{OutNodeID: fx.tunnel.OutNodeID, Active: true, Weight: 2},
		{OutNodeID: fx.other.ID, Active: true, Weight: 3},
	}
	req := dto.ForwardUpdateDto{
		ID: forward.ID, Name: "admin-balance", TunnelID: fx.tunnel.ID, RemoteAddr: forward.RemoteAddr,
		ExitMode: exitModeBalance, ExitStrategy: exitStrategyRound, ExitMembers: members,
	}
	admin := CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin_user"}
	if res := UpdateForward(admin, req); res.Code != 0 {
		t.Fatalf("admin configure exits=%+v", res)
	}

	for _, omitted := range []bool{true, false} {
		req.Name = "regular-edit"
		if omitted {
			req.ExitMode, req.ExitStrategy, req.ExitMembers = "", "", nil
		} else {
			req.ExitMode, req.ExitStrategy = exitModeBalance, exitStrategyRound
			req.ExitMembers = []dto.ForwardExitMemberDto{members[1], members[0]}
		}
		if res := UpdateForward(fx.user, req); res.Code != 0 {
			t.Fatalf("regular update (omitted=%v)=%+v", omitted, res)
		}
		var got model.Forward
		if err := model.DB.First(&got, forward.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.Name != req.Name || got.ExitMode != exitModeBalance || got.ExitStrategy != exitStrategyRound || got.Status != forwardStatusPaused {
			t.Fatalf("preserved forward=%+v", got)
		}
		assertForwardExitPolicyMembers(t, forward.ID, members)
	}

	for _, change := range []struct {
		name   string
		mutate func(*dto.ForwardUpdateDto)
	}{
		{"remove member", func(req *dto.ForwardUpdateDto) { req.ExitMode = exitModeManual; req.ExitMembers = req.ExitMembers[1:] }},
		{"mode", func(req *dto.ForwardUpdateDto) { req.ExitMode = exitModeManual }},
		{"strategy", func(req *dto.ForwardUpdateDto) { req.ExitStrategy = exitStrategyHash }},
		{"strategy only", func(req *dto.ForwardUpdateDto) {
			req.ExitMode = ""
			req.ExitMembers = nil
			req.ExitStrategy = exitStrategyHash
		}},
		{"activation", func(req *dto.ForwardUpdateDto) { req.ExitMembers[0].Active = false }},
		{"weight", func(req *dto.ForwardUpdateDto) { req.ExitMembers[0].Weight++ }},
		{"duplicate member", func(req *dto.ForwardUpdateDto) { req.ExitMembers[1] = req.ExitMembers[0] }},
	} {
		t.Run(change.name, func(t *testing.T) {
			request := dto.ForwardUpdateDto{
				ID: forward.ID, Name: "rejected-edit", TunnelID: fx.tunnel.ID, RemoteAddr: forward.RemoteAddr,
				ExitMode: exitModeBalance, ExitStrategy: exitStrategyRound, ExitMembers: append([]dto.ForwardExitMemberDto(nil), members...),
			}
			change.mutate(&request)
			var before, after model.Forward
			if err := model.DB.First(&before, forward.ID).Error; err != nil {
				t.Fatal(err)
			}
			oldMembers := loadPersistedForwardExitMembers(forward.ID)
			if res := UpdateForward(fx.user, request); res.Code == 0 || res.Msg != "普通用户不能自定义出口节点" {
				t.Fatalf("custom update=%+v", res)
			}
			if err := model.DB.First(&after, forward.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(oldMembers, loadPersistedForwardExitMembers(forward.ID)) {
				t.Fatal("rejected update changed persisted forward or exits")
			}
		})
	}
}

func TestAdminCanStillChooseExitNodes(t *testing.T) {
	fx := setupForwardExitPolicyFixture(t)
	admin := CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin_user"}
	if res := CreateForward(admin, dto.ForwardDto{
		Name: "admin-custom", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.10:443",
		ExitMode: exitModeManual, ExitMembers: []dto.ForwardExitMemberDto{{OutNodeID: fx.other.ID, Active: true}},
	}); res.Code != 0 {
		t.Fatalf("admin create=%+v", res)
	}
	forward := createPausedForwardForExitPolicy(t, fx)
	if res := UpdateForward(admin, dto.ForwardUpdateDto{
		ID: forward.ID, Name: forward.Name, TunnelID: fx.tunnel.ID, RemoteAddr: forward.RemoteAddr,
		ExitMode: exitModeManual, ExitMembers: []dto.ForwardExitMemberDto{{OutNodeID: fx.other.ID, Active: true}},
	}); res.Code != 0 {
		t.Fatalf("admin update other user's forward=%+v", res)
	}
	assertForwardExitPolicyMembers(t, forward.ID, []dto.ForwardExitMemberDto{{OutNodeID: fx.other.ID, Active: true, Weight: 1}})
}

func TestRegularUserCanEchoLegacyDefaultExit(t *testing.T) {
	fx := setupForwardExitPolicyFixture(t)
	forward := createPausedForwardForExitPolicy(t, fx)
	// 旧转发只有 OutPort，没有成员表记录；原样回传默认出口应能正常更新并补齐成员。
	res := UpdateForward(fx.user, dto.ForwardUpdateDto{
		ID: forward.ID, Name: "legacy-edit", TunnelID: fx.tunnel.ID, RemoteAddr: forward.RemoteAddr,
		ExitMode: exitModeSingle, ExitStrategy: exitStrategyFIFO,
		ExitMembers: []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true, Weight: 1}},
	})
	if res.Code != 0 {
		t.Fatalf("legacy default exit update=%+v", res)
	}
	assertForwardExitPolicyMembers(t, forward.ID, []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true, Weight: 1}})
}

func TestRegularUserChangingTunnelUsesNewDefaultExit(t *testing.T) {
	for _, customExit := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "custom exit"}[customExit], func(t *testing.T) {
			fx := setupForwardExitPolicyFixture(t)
			forward := createPausedForwardForExitPolicy(t, fx)
			newTunnel := fx.tunnel
			newTunnel.ID = 0
			newTunnel.Name = "new-policy-tunnel"
			newTunnel.OutNodeID, newTunnel.OutIP = fx.other.ID, fx.other.ServerIP
			if err := model.DB.Create(&newTunnel).Error; err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour).UnixMilli()
			permission := model.UserTunnel{UserID: fx.user.UserID, TunnelID: newTunnel.ID, Flow: 100, Num: 10, ExpTime: &expires, Status: 1}
			if err := model.DB.Create(&permission).Error; err != nil {
				t.Fatal(err)
			}
			req := dto.ForwardUpdateDto{ID: forward.ID, Name: forward.Name, TunnelID: newTunnel.ID, RemoteAddr: forward.RemoteAddr}
			if customExit {
				req.ExitMode = exitModeManual
				req.ExitMembers = []dto.ForwardExitMemberDto{{OutNodeID: fx.tunnel.OutNodeID, Active: true}}
			}
			res := UpdateForward(fx.user, req)
			if customExit {
				if res.Code == 0 || res.Msg != "普通用户不能自定义出口节点" {
					t.Fatalf("custom exit on changed tunnel=%+v", res)
				}
				return
			}
			if res.Code != 0 {
				t.Fatalf("change tunnel=%+v", res)
			}
			assertForwardExitPolicyMembers(t, forward.ID, []dto.ForwardExitMemberDto{{OutNodeID: fx.other.ID, Active: true, Weight: 1}})
		})
	}
}

func TestAdminMovingUserForwardUsesSharedGrantRules(t *testing.T) {
	expired := time.Now().Add(-time.Hour).UnixMilli()
	future := time.Now().Add(time.Hour).UnixMilli()
	for _, tc := range []struct {
		name  string
		grant *model.UserTunnel
		want  string
	}{
		{"missing grant", nil, "用户没有该隧道权限"},
		{"disabled grant", &model.UserTunnel{Flow: 100, Num: 10, ExpTime: &future, Status: 0}, "隧道被禁用"},
		{"expired grant", &model.UserTunnel{Flow: 100, Num: 10, ExpTime: &expired, Status: 1}, "用户的该隧道权限已到期"},
		{"exhausted grant", &model.UserTunnel{Flow: 1, Num: 10, ExpTime: &future, Status: 1, InFlow: 1 << 30}, "该隧道流量已用完"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupForwardExitPolicyFixture(t)
			forward := createPausedForwardForExitPolicy(t, fx)
			newTunnel := fx.tunnel
			newTunnel.ID = 0
			newTunnel.Name = "admin-move-target"
			if err := model.DB.Create(&newTunnel).Error; err != nil {
				t.Fatal(err)
			}
			if tc.grant != nil {
				grant := *tc.grant
				grant.UserID, grant.TunnelID = fx.user.UserID, newTunnel.ID
				if err := model.DB.Create(&grant).Error; err != nil {
					t.Fatal(err)
				}
			}
			admin := CurrentUser{UserID: fx.user.UserID + 1000, RoleID: adminRoleID, UserName: "admin"}
			res := UpdateForward(admin, dto.ForwardUpdateDto{ID: forward.ID, Name: forward.Name, TunnelID: newTunnel.ID, RemoteAddr: forward.RemoteAddr})
			if res.Code == 0 || res.Msg != tc.want {
				t.Fatalf("UpdateForward=%+v, want %q", res, tc.want)
			}
			// 与普通用户提交使用同一规则：本人视角的提示只在主语上不同。
			var user model.User
			if err := model.DB.First(&user, fx.user.UserID).Error; err != nil {
				t.Fatal(err)
			}
			var grant *model.UserTunnel
			if tc.grant != nil {
				grant = getUserTunnel(fx.user.UserID, newTunnel.ID)
			}
			if got := adminSubjectReason(userTunnelUnavailableReason(&user, grant, time.Now().UnixMilli())); got != tc.want {
				t.Fatalf("shared rule=%q, want %q", got, tc.want)
			}
		})
	}
}
