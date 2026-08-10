package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestCreateForwardRechecksPermissionAfterConcurrentDisable(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "create-permission-race")
	permission := createTunnelSagaUserTunnel(t, user.ID, portTunnel.ID)
	originalRefresh := sendNftRefreshMessage
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })

	lockedIDs := normalizeNodeSagaLockIDs([]int64{portTunnel.InNodeID, portTunnel.OutNodeID})
	unlock := lockNftSagaNodes(lockedIDs)
	disableDone := make(chan resultSnapshot, 1)
	disabled := 0
	go func() {
		res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: permission.ID, Flow: permission.Flow, Num: permission.Num, Status: &disabled})
		disableDone <- resultSnapshot{code: res.Code, msg: res.Msg}
	}()
	wantRefs := make(map[int64]int, len(lockedIDs))
	for _, nodeID := range lockedIDs {
		wantRefs[nodeID] = 2
	}
	waitForNodeSagaLockRefs(t, nftSagaNodeLocks, wantRefs)
	createDone := make(chan resultSnapshot, 1)
	go func() {
		res := CreateForward(CurrentUser{UserID: user.ID, RoleID: userRoleID, UserName: user.User}, dto.ForwardDto{
			Name: "must-not-create", TunnelID: portTunnel.ID, RemoteAddr: "198.51.100.88:443", Strategy: "fifo",
		})
		createDone <- resultSnapshot{code: res.Code, msg: res.Msg}
	}()
	for _, nodeID := range lockedIDs {
		wantRefs[nodeID] = 3
	}
	waitForNodeSagaLockRefs(t, nftSagaNodeLocks, wantRefs)
	unlock()
	if got := waitPermissionGateResult(t, disableDone); got.code != 0 {
		t.Fatalf("concurrent disable=%+v", got)
	}
	got := waitPermissionGateResult(t, createDone)
	if got.code == 0 {
		t.Fatalf("CreateForward used stale permission after concurrent disable: %+v", got)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("name = ?", "must-not-create").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale permission created forwards=%d err=%v", count, err)
	}
	_ = inNode
}

func TestUpdateUserTunnelStatusPreservesIndependentForwardStates(t *testing.T) {
	_, userTunnel, _, active := setupForwardWithExitMember(t)
	paused := active
	paused.ID = 0
	paused.Name = "independently-paused"
	paused.InPort++
	paused.Status = forwardStatusPaused
	errorForward := active
	errorForward.ID = 0
	errorForward.Name = "independently-error"
	errorForward.InPort += 2
	errorForward.Status = forwardStatusError
	if err := model.DB.Create(&paused).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&errorForward).Error; err != nil {
		t.Fatal(err)
	}
	originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
	originalUpdateService, originalUpdateChains, originalUpdateRemote := updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService
	pauseUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	pauseUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() {
		pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
		updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService = originalUpdateService, originalUpdateChains, originalUpdateRemote
	})
	for _, status := range []int{0, 1} {
		res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: userTunnel.Flow, Num: userTunnel.Num, Status: &status})
		if res.Code != 0 {
			t.Fatalf("UpdateUserTunnel status=%d: %+v", status, res)
		}
	}
	for id, want := range map[int64]int{active.ID: forwardStatusActive, paused.ID: forwardStatusPaused, errorForward.ID: forwardStatusError} {
		var got model.Forward
		if err := model.DB.First(&got, id).Error; err != nil || got.Status != want {
			t.Fatalf("forward %d status=%d err=%v want=%d", id, got.Status, err, want)
		}
	}
}

func TestUpdateUserTunnelRejectsInvalidStatus(t *testing.T) {
	_, userTunnel, _, _ := setupForwardWithExitMember(t)
	originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
	pauseUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	pauseUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
	})
	invalid := 2
	res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: userTunnel.Flow, Num: userTunnel.Num, Status: &invalid})
	if res.Code == 0 {
		t.Fatalf("invalid status accepted: %+v", res)
	}
	var got model.UserTunnel
	if err := model.DB.First(&got, userTunnel.ID).Error; err != nil || got.Status != userTunnel.Status {
		t.Fatalf("invalid status mutated permission=(%+v,%v)", got, err)
	}
}

func TestUpdateUserTunnelEnableRebuildsMissingRuntime(t *testing.T) {
	_, userTunnel, _, _ := setupForwardWithExitMember(t)
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	originalUpdateService, originalAddService := updateUserTunnelGostService, addUserTunnelGostService
	originalUpdateChains, originalAddChains := updateUserTunnelGostChains, addUserTunnelGostChains
	originalUpdateRemote, originalAddRemote := updateUserTunnelGostRemoteService, addUserTunnelGostRemoteService
	updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.NotFoundMsg}
	}
	updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult { return ws.GostResult{Msg: gost.NotFoundMsg} }
	updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.NotFoundMsg}
	}
	adds := 0
	addUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		adds++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	addUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult {
		adds++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	addUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		adds++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() {
		updateUserTunnelGostService, addUserTunnelGostService = originalUpdateService, originalAddService
		updateUserTunnelGostChains, addUserTunnelGostChains = originalUpdateChains, originalAddChains
		updateUserTunnelGostRemoteService, addUserTunnelGostRemoteService = originalUpdateRemote, originalAddRemote
	})
	active := 1
	res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: userTunnel.Flow, Num: userTunnel.Num, Status: &active})
	if res.Code != 0 || adds != 3 {
		t.Fatalf("missing runtime rebuild result=%+v adds=%d", res, adds)
	}
}

func TestDisabledPermissionIsExcludedFromNftDesiredButAdminForwardRemains(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "nft-permission-gate")
	permission := createTunnelSagaUserTunnel(t, user.ID, portTunnel.ID)
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", permission.ID).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	blocked := model.Forward{UserID: user.ID, UserName: user.User, Name: "blocked", TunnelID: portTunnel.ID, InPort: 15001, RemoteAddr: "198.51.100.90:443", Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now}
	adminUser := createTunnelSagaUser(t, "nft-admin-no-permission")
	if err := model.DB.Model(&model.User{}).Where("id = ?", adminUser.ID).Update("role_id", adminRoleID).Error; err != nil {
		t.Fatal(err)
	}
	missingUser := createTunnelSagaUser(t, "nft-user-no-permission")
	admin := model.Forward{UserID: adminUser.ID, UserName: adminUser.User, Name: "admin-no-permission", TunnelID: portTunnel.ID, InPort: 15002, RemoteAddr: "198.51.100.91:443", Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now}
	missing := model.Forward{UserID: missingUser.ID, UserName: missingUser.User, Name: "user-no-permission", TunnelID: portTunnel.ID, InPort: 15003, RemoteAddr: "198.51.100.92:443", Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now}
	if err := model.DB.Create(&blocked).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&missing).Error; err != nil {
		t.Fatal(err)
	}
	rules, err := buildNftRules(inNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rulesForForward(rules, blocked.ID)) != 0 {
		t.Fatal("disabled permission remained in NFT desired state")
	}
	if len(rulesForForward(rules, admin.ID)) == 0 {
		t.Fatal("admin/no-UserTunnel forward was incorrectly filtered")
	}
	if len(rulesForForward(rules, missing.ID)) != 0 {
		t.Fatal("ordinary user without UserTunnel remained in NFT desired state")
	}
}

func TestMissingPermissionUserLookupErrorFailsNftSnapshotClosed(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "nft-user-query-error")
	now := time.Now().UnixMilli()
	forward := model.Forward{UserID: user.ID, UserName: user.User, Name: "query-error", TunnelID: portTunnel.ID, InPort: 15004, RemoteAddr: "198.51.100.93:443", Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatal(err)
	}
	original := forwardPermissionAllowsRuntimeDBFn
	forwardPermissionAllowsRuntimeDBFn = func(_ *gorm.DB, _ *model.Forward) (bool, error) {
		return false, errors.New("injected user gate query failure")
	}
	t.Cleanup(func() { forwardPermissionAllowsRuntimeDBFn = original })
	if _, err := buildNftRules(inNode.ID); err == nil {
		t.Fatal("runtime gate user query error was treated as allowed")
	}
}

func TestDisabledPermissionReconnectPausesAndDoesNotRecreateActiveGostForward(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
	originalUpdate, originalUpdateRemote := updateGostServiceCommand, updateGostRemoteServiceCommand
	var pausedNodes []int64
	pauseUserTunnelGostService = func(nodeID int64, _ string) ws.GostResult {
		pausedNodes = append(pausedNodes, nodeID)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	pauseUserTunnelGostRemoteService = func(nodeID int64, _ string) ws.GostResult {
		pausedNodes = append(pausedNodes, nodeID)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateGostServiceCommand = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		t.Fatal("disabled permission recreated entry service")
		return ws.GostResult{}
	}
	updateGostRemoteServiceCommand = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		t.Fatal("disabled permission recreated remote service")
		return ws.GostResult{}
	}
	t.Cleanup(func() {
		pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
		updateGostServiceCommand, updateGostRemoteServiceCommand = originalUpdate, originalUpdateRemote
	})
	SyncNodeForwardsOnConnect(tunnel.InNodeID)
	SyncNodeForwardsOnConnect(member.OutNodeID)
	want := []int64{tunnel.InNodeID, member.OutNodeID}
	if len(pausedNodes) != len(want) || pausedNodes[0] != want[0] || pausedNodes[1] != want[1] {
		t.Fatalf("disabled reconnect pauses=%v want=%v", pausedNodes, want)
	}
}

func TestOrdinaryUserWithoutPermissionIsExcludedFromGostReconnect(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	if err := model.DB.Delete(&model.UserTunnel{}, userTunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if items := gostEntrySyncItems(tunnel.InNodeID); len(items) != 0 {
		t.Fatalf("missing permission entry reconnect items=%+v", items)
	}
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	if items := gostExitSyncItems(member.OutNodeID); len(items) != 0 {
		t.Fatalf("missing permission exit reconnect items=%+v", items)
	}
}

func TestUpdateForwardASkipsDeployForBlockedPermission(t *testing.T) {
	for _, status := range []int{0, userTunnelStatusDeleting} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			_, userTunnel, _, forward := setupForwardWithExitMember(t)
			if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			originalUpdateService, originalUpdateChains, originalUpdateRemote := updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService
			originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
			originalDelete, originalDeleteChains, originalDeleteRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
			deploys := 0
			updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			pauseUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			pauseUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			t.Cleanup(func() {
				updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService = originalUpdateService, originalUpdateChains, originalUpdateRemote
				pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
				deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalDelete, originalDeleteChains, originalDeleteRemote
			})
			if err := UpdateForwardA(&forward); err != nil {
				t.Fatalf("UpdateForwardA blocked convergence: %v", err)
			}
			if deploys != 0 {
				t.Fatalf("blocked permission caused %d deploys", deploys)
			}
		})
	}
}

func TestUpdateTunnelSkipsDeployForBlockedPermission(t *testing.T) {
	for _, status := range []int{0, userTunnelStatusDeleting} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			_, userTunnel, tunnel, _ := setupForwardWithExitMember(t)
			if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			protocol := "udp"
			res := UpdateTunnel(dto.TunnelUpdateDto{ID: tunnel.ID, Name: tunnel.Name, Flow: tunnel.Flow, Protocol: protocol, TCPListenAddr: "", UDPListenAddr: "0.0.0.0"})
			if res.Code != 0 {
				t.Fatalf("blocked permission tunnel sync attempted runtime deploy: %+v", res)
			}
		})
	}
}

func TestUpdateNodeSkipsDeployForBlockedPermission(t *testing.T) {
	for _, status := range []int{0, userTunnelStatusDeleting} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			_, userTunnel, tunnel, _ := setupForwardWithExitMember(t)
			if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			var outNode model.Node
			if err := model.DB.First(&outNode, tunnel.OutNodeID).Error; err != nil {
				t.Fatal(err)
			}
			originalUpdateService, originalUpdateChains, originalUpdateRemote := updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService
			originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
			originalDelete, originalDeleteChains, originalDeleteRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
			deploys := 0
			updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
				deploys++
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			pauseUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			pauseUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
			t.Cleanup(func() {
				updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService = originalUpdateService, originalUpdateChains, originalUpdateRemote
				pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
				deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalDelete, originalDeleteChains, originalDeleteRemote
			})
			res := UpdateNode(dto.NodeUpdateDto{ID: outNode.ID, Name: outNode.Name, IP: outNode.IP, ServerIP: "10.0.1.20", PortSta: outNode.PortSta, PortEnd: outNode.PortEnd, ForwardMode: outNode.ForwardMode})
			if res.Code != 0 {
				t.Fatalf("UpdateNode blocked convergence=%+v", res)
			}
			if deploys != 0 {
				t.Fatalf("blocked permission UpdateNode caused %d deploys", deploys)
			}
		})
	}
}

func TestRealAdminWithoutPermissionStillDeploysOnUpdateForwardA(t *testing.T) {
	user, userTunnel, _, forward := setupForwardWithExitMember(t)
	if err := model.DB.Delete(&model.UserTunnel{}, userTunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("role_id", adminRoleID).Error; err != nil {
		t.Fatal(err)
	}
	originalUpdateService, originalUpdateChains, originalUpdateRemote := updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService
	deploys := 0
	updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() {
		updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService = originalUpdateService, originalUpdateChains, originalUpdateRemote
	})
	if err := UpdateForwardA(&forward); err != nil {
		t.Fatal(err)
	}
	if deploys != 3 {
		t.Fatalf("real admin/no-UT deploys=%d want=3", deploys)
	}
}

func TestUpdateForwardAUserGateQueryErrorPerformsZeroDeploys(t *testing.T) {
	_, userTunnel, _, forward := setupForwardWithExitMember(t)
	if err := model.DB.Delete(&model.UserTunnel{}, userTunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	originalUpdateService, originalUpdateChains, originalUpdateRemote := updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService
	deploys := 0
	updateUserTunnelGostService = func(_ int64, _ string, _ int, _ *int64, _ string, _ int, _ *model.Tunnel, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateUserTunnelGostChains = func(_ int64, _, _, _, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateUserTunnelGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		deploys++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() {
		updateUserTunnelGostService, updateUserTunnelGostChains, updateUserTunnelGostRemoteService = originalUpdateService, originalUpdateChains, originalUpdateRemote
	})
	originalGate := forwardPermissionAllowsRuntimeDBFn
	forwardPermissionAllowsRuntimeDBFn = func(_ *gorm.DB, _ *model.Forward) (bool, error) {
		return false, errors.New("injected active resync user gate failure")
	}
	t.Cleanup(func() { forwardPermissionAllowsRuntimeDBFn = originalGate })
	if err := UpdateForwardA(&forward); err == nil {
		t.Fatal("UpdateForwardA accepted failed owner gate query")
	}
	if deploys != 0 {
		t.Fatalf("failed owner gate query caused %d deploys", deploys)
	}
}

func TestUpdateForwardAllowedToBlockedTunnelRetiresOldGostRuntime(t *testing.T) {
	user, _, oldTunnel, forward := setupForwardWithExitMember(t)
	now := time.Now().UnixMilli()
	newIn := createForwardExitNode(t, "blocked-update-entry", "10.0.2.1", 33000, 33099, forwardModeGost, now)
	newOut := createForwardExitNode(t, "blocked-update-exit", "10.0.2.2", 34000, 34099, forwardModeGost, now)
	protocol := "tcp"
	blockedTunnel := model.Tunnel{
		Name: "blocked-update-target", InNodeID: newIn.ID, InIP: newIn.IP,
		OutNodeID: newOut.ID, OutIP: newOut.ServerIP, Type: tunnelTypeTunnelForward,
		Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: tunnelStatusActive,
	}
	if err := model.DB.Create(&blockedTunnel).Error; err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UnixMilli()
	blockedPermission := model.UserTunnel{UserID: user.ID, TunnelID: blockedTunnel.ID, Num: 1, Flow: 100, ExpTime: &exp, Status: 0}
	if err := model.DB.Create(&blockedPermission).Error; err != nil {
		t.Fatal(err)
	}

	originalDeleteService, originalDeleteChains, originalDeleteRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	cleanupCalls := 0
	deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult { cleanupCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { cleanupCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { cleanupCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalDeleteService, originalDeleteChains, originalDeleteRemote
	})

	// This internal form models an administrator who owns the forward. It keeps
	// the target-permission gate path isolated from handler ownership checks.
	res := updateForwardInternal(dto.ForwardUpdateDto{
		ID: forward.ID, Name: forward.Name, TunnelID: blockedTunnel.ID,
		RemoteAddr: forward.RemoteAddr, Strategy: forward.Strategy,
	}, user.ID, adminRoleID, user.User)
	if res.Code != 0 {
		t.Fatalf("allowed-to-blocked update=%+v", res)
	}
	if cleanupCalls != 3 {
		t.Fatalf("old Gost cleanup calls=%d want=3", cleanupCalls)
	}
	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil || got.TunnelID != blockedTunnel.ID {
		t.Fatalf("updated forward=(%+v,%v)", got, err)
	}
	_ = oldTunnel
}

func TestUpdateForwardAllowedToBlockedKeepsBlockedDesiredAfterUnknownCleanup(t *testing.T) {
	user, _, _, forward := setupForwardWithExitMember(t)
	now := time.Now().UnixMilli()
	newIn := createForwardExitNode(t, "unknown-cleanup-entry", "10.0.3.1", 35000, 35099, forwardModeGost, now)
	newOut := createForwardExitNode(t, "unknown-cleanup-exit", "10.0.3.2", 36000, 36099, forwardModeGost, now)
	protocol := "tcp"
	blockedTunnel := model.Tunnel{
		Name: "unknown-cleanup-target", InNodeID: newIn.ID, InIP: newIn.IP,
		OutNodeID: newOut.ID, OutIP: newOut.ServerIP, Type: tunnelTypeTunnelForward,
		Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: tunnelStatusActive,
	}
	if err := model.DB.Create(&blockedTunnel).Error; err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UnixMilli()
	if err := model.DB.Create(&model.UserTunnel{UserID: user.ID, TunnelID: blockedTunnel.ID, Num: 1, Flow: 100, ExpTime: &exp, Status: 0}).Error; err != nil {
		t.Fatal(err)
	}

	originalDeleteService, originalDeleteChains, originalDeleteRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	unknown := ws.GostResult{Msg: "session replaced", OutcomeUnknown: true}
	deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult { return unknown }
	deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { return unknown }
	deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return unknown }
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalDeleteService, originalDeleteChains, originalDeleteRemote
	})

	res := updateForwardInternal(dto.ForwardUpdateDto{ID: forward.ID, Name: forward.Name, TunnelID: blockedTunnel.ID, RemoteAddr: forward.RemoteAddr, Strategy: forward.Strategy}, user.ID, adminRoleID, user.User)
	if res.Code != 0 {
		t.Fatalf("unknown cleanup should retain blocked desired: %+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil || got.TunnelID != blockedTunnel.ID {
		t.Fatalf("unknown cleanup did not retain blocked desired=(%+v,%v)", got, err)
	}
}

func TestUpdateForwardAllowedToBlockedRetainsOldErrorTombstoneOnKnownCleanupFailure(t *testing.T) {
	user, _, oldTunnel, forward := setupForwardWithExitMember(t)
	now := time.Now().UnixMilli()
	newIn := createForwardExitNode(t, "retry-cleanup-entry", "10.0.4.1", 37000, 37099, forwardModeGost, now)
	newOut := createForwardExitNode(t, "retry-cleanup-exit", "10.0.4.2", 38000, 38099, forwardModeGost, now)
	protocol := "tcp"
	blockedTunnel := model.Tunnel{
		Name: "retry-cleanup-target", InNodeID: newIn.ID, InIP: newIn.IP,
		OutNodeID: newOut.ID, OutIP: newOut.ServerIP, Type: tunnelTypeTunnelForward,
		Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: tunnelStatusActive,
	}
	if err := model.DB.Create(&blockedTunnel).Error; err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UnixMilli()
	if err := model.DB.Create(&model.UserTunnel{UserID: user.ID, TunnelID: blockedTunnel.ID, Num: 1, Flow: 100, ExpTime: &exp, Status: 0}).Error; err != nil {
		t.Fatal(err)
	}

	originalDeleteService, originalDeleteChains, originalDeleteRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	fail := true
	serviceCalls, chainCalls, remoteCalls := 0, 0, 0
	deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult {
		serviceCalls++
		if fail {
			return ws.GostResult{Msg: "known cleanup failure"}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { chainCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { remoteCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalDeleteService, originalDeleteChains, originalDeleteRemote
	})
	req := dto.ForwardUpdateDto{ID: forward.ID, Name: forward.Name, TunnelID: blockedTunnel.ID, RemoteAddr: forward.RemoteAddr, Strategy: forward.Strategy}
	if res := updateForwardInternal(req, user.ID, adminRoleID, user.User); res.Code == 0 {
		t.Fatalf("known cleanup failure unexpectedly succeeded: %+v", res)
	}
	var failed model.Forward
	if err := model.DB.First(&failed, forward.ID).Error; err != nil || failed.TunnelID != oldTunnel.ID || failed.Status != forwardStatusError {
		t.Fatalf("failed cleanup did not retain old error tombstone=(%+v,%v)", failed, err)
	}

	fail = false
	if res := updateForwardInternal(req, user.ID, adminRoleID, user.User); res.Code != 0 {
		t.Fatalf("cleanup retry failed: %+v", res)
	}
	if serviceCalls != 2 || chainCalls != 2 || remoteCalls != 2 {
		t.Fatalf("old runtime cleanup calls after retry=(service=%d chain=%d remote=%d), want 2 each", serviceCalls, chainCalls, remoteCalls)
	}
	var retried model.Forward
	if err := model.DB.First(&retried, forward.ID).Error; err != nil || retried.TunnelID != blockedTunnel.ID || retried.Status != forwardStatusActive {
		t.Fatalf("cleanup retry did not commit target desired=(%+v,%v)", retried, err)
	}
}

func TestAdminCannotResumeForwardForBlockedOwnerPermission(t *testing.T) {
	for _, status := range []int{0, userTunnelStatusDeleting} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			_, userTunnel, _, forward := setupForwardWithExitMember(t)
			if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Model(&model.Forward{}).Where("id = ?", forward.ID).Update("status", forwardStatusPaused).Error; err != nil {
				t.Fatal(err)
			}
			res := ResumeForward(CurrentUser{RoleID: adminRoleID}, forward.ID)
			if res.Code == 0 {
				t.Fatalf("admin resumed blocked permission: %+v", res)
			}
			var got model.Forward
			if err := model.DB.First(&got, forward.ID).Error; err != nil || got.Status != forwardStatusPaused {
				t.Fatalf("blocked resume mutated forward=(%+v,%v)", got, err)
			}
		})
	}
}

func waitPermissionGateResult(t *testing.T, ch <-chan resultSnapshot) resultSnapshot {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("permission gate operation timed out")
		return resultSnapshot{}
	}
}
