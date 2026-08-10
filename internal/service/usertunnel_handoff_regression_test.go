package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestRemoveUserTunnelWaitsForEveryRuntimeNodeLock(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}

	unlock := lockNftSagaNodes([]int64{tunnel.InNodeID, member.OutNodeID})
	done := make(chan resultSnapshot, 1)
	go func() {
		res := RemoveUserTunnel(userTunnel.ID)
		done <- resultSnapshot{code: res.Code, msg: res.Msg}
	}()
	waitForNodeSagaLockRefs(t, nftSagaNodeLocks, map[int64]int{tunnel.InNodeID: 2, member.OutNodeID: 2})
	select {
	case got := <-done:
		unlock()
		t.Fatalf("RemoveUserTunnel bypassed runtime node locks: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveUserTunnel did not finish after releasing runtime node locks")
	}
}

func TestRemoveUserTunnelKnownCleanupFailureRetainsDurableTombstone(t *testing.T) {
	_, userTunnel, _, forward := setupForwardWithExitMember(t)

	res := RemoveUserTunnel(userTunnel.ID) // fixture nodes are offline
	if res.Code == 0 {
		t.Fatalf("RemoveUserTunnel ignored definite cleanup failure: %+v", res)
	}
	var gotUT model.UserTunnel
	if err := model.DB.First(&gotUT, userTunnel.ID).Error; err != nil || gotUT.Status != -1 {
		t.Fatalf("durable permission tombstone=(%+v,%v)", gotUT, err)
	}
	var gotForward model.Forward
	if err := model.DB.First(&gotForward, forward.ID).Error; err != nil || gotForward.Status != forward.Status {
		t.Fatalf("durable forward tombstone=(%+v,%v)", gotForward, err)
	}
}

func TestRemoveUserTunnelUnknownCleanupRetainsReconnectableTombstone(t *testing.T) {
	_, userTunnel, _, forward := setupForwardWithExitMember(t)
	originalService, originalChains, originalRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	deleteUserTunnelGostService = func(_ int64, _ string) ws.GostResult {
		return ws.GostResult{Msg: "replacement response lost", OutcomeUnknown: true}
	}
	deleteUserTunnelGostChains = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalService, originalChains, originalRemote
	})

	if res := RemoveUserTunnel(userTunnel.ID); res.Code == 0 {
		t.Fatalf("unknown cleanup deleted desired state: %+v", res)
	}
	var gotUT model.UserTunnel
	var gotForward model.Forward
	if err := model.DB.First(&gotUT, userTunnel.ID).Error; err != nil || gotUT.Status != userTunnelStatusDeleting {
		t.Fatalf("permission tombstone=(%+v,%v)", gotUT, err)
	}
	if err := model.DB.First(&gotForward, forward.ID).Error; err != nil || gotForward.Status != forward.Status {
		t.Fatalf("forward tombstone=(%+v,%v)", gotForward, err)
	}
}

func TestUpdateUserTunnelWaitsForExplicitExitMemberLock(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	now := time.Now().UnixMilli()
	memberNode := createForwardExitNode(t, "permission-member", "10.0.1.3", 32000, 32099, forwardModeGost, now)
	if err := model.DB.Model(&model.ForwardExitMember{}).Where("forward_id = ?", forward.ID).Update("out_node_id", memberNode.ID).Error; err != nil {
		t.Fatal(err)
	}
	unlock := lockNftSagaNodes([]int64{memberNode.ID})
	status := userTunnel.Status
	done := make(chan resultSnapshot, 1)
	go func() {
		res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: userTunnel.Flow, Num: userTunnel.Num, Status: &status})
		done <- resultSnapshot{code: res.Code, msg: res.Msg}
	}()
	waitForNodeSagaLockRefs(t, nftSagaNodeLocks, map[int64]int{tunnel.InNodeID: 1, tunnel.OutNodeID: 1, memberNode.ID: 2})
	select {
	case got := <-done:
		unlock()
		t.Fatalf("UpdateUserTunnel bypassed explicit member lock: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case got := <-done:
		if got.code != 0 {
			t.Fatalf("UpdateUserTunnel after unlock=%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateUserTunnel did not finish after member unlock")
	}
}

func TestUpdateUserTunnelRuntimeFailureRestoresDesiredSnapshot(t *testing.T) {
	_, userTunnel, _, _ := setupForwardWithExitMember(t)
	speed := int64(999)
	if err := model.DB.Create(&model.SpeedLimit{ID: speed, Name: "offline", Speed: 100, TunnelID: userTunnel.TunnelID, TunnelName: "cleanup-tunnel", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	status := 1
	res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: 12, Num: 2, Status: &status, SpeedID: &speed})
	if res.Code == 0 {
		t.Fatalf("UpdateUserTunnel ignored offline runtime failure: %+v", res)
	}
	var got model.UserTunnel
	if err := model.DB.First(&got, userTunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.SpeedID != nil || got.Flow != userTunnel.Flow || got.Num != userTunnel.Num || got.Status != userTunnel.Status {
		t.Fatalf("failed runtime update did not restore snapshot: before=%+v after=%+v", userTunnel, got)
	}
}

func TestUpdateUserTunnelPauseUnknownIsConvergedOnReconnect(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	originalPause, originalPauseRemote := pauseUserTunnelGostService, pauseUserTunnelGostRemoteService
	pauseUserTunnelGostService = func(_ int64, _ string) ws.GostResult {
		return ws.GostResult{Msg: "replacement response lost", OutcomeUnknown: true}
	}
	pauseUserTunnelGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		pauseUserTunnelGostService, pauseUserTunnelGostRemoteService = originalPause, originalPauseRemote
	})
	paused := forwardStatusPaused
	if res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: userTunnel.Flow, Num: userTunnel.Num, Status: &paused}); res.Code == 0 {
		t.Fatalf("unknown pause was reported definite: %+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil || got.Status != forwardStatusActive {
		t.Fatalf("permission pause changed independent forward desired=(%+v,%v)", got, err)
	}
	var calls []int64
	pauseUserTunnelGostService = func(nodeID int64, _ string) ws.GostResult {
		calls = append(calls, nodeID)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	pauseUserTunnelGostRemoteService = func(nodeID int64, _ string) ws.GostResult {
		calls = append(calls, nodeID)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	SyncNodeForwardsOnConnect(tunnel.InNodeID)
	SyncNodeForwardsOnConnect(member.OutNodeID)
	want := []int64{tunnel.InNodeID, member.OutNodeID}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("paused reconnect convergence=%v want=%v", calls, want)
	}
}

func TestUpdateUserTunnelStatusDesiredWriteIsAtomic(t *testing.T) {
	_, userTunnel, _, forward := setupForwardWithExitMember(t)
	callback := "test:fail-user-tunnel-forward-status"
	if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.Forward{}).TableName()) {
			tx.AddError(errors.New("injected forward status failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callback) })
	paused := forwardStatusPaused
	res := UpdateUserTunnel(dto.UserTunnelUpdateDto{ID: userTunnel.ID, Flow: 99, Num: 9, Status: &paused})
	if res.Code == 0 {
		t.Fatalf("partial desired write succeeded: %+v", res)
	}
	var gotUT model.UserTunnel
	var gotForward model.Forward
	if err := model.DB.First(&gotUT, userTunnel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&gotForward, forward.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotUT.Flow != userTunnel.Flow || gotUT.Num != userTunnel.Num || gotUT.Status != userTunnel.Status || gotForward.Status != forward.Status {
		t.Fatalf("non-atomic desired state: permission=%+v forward=%+v", gotUT, gotForward)
	}
}

func TestReconnectCleansUserTunnelDeletionTombstoneOnEachGostNode(t *testing.T) {
	_, userTunnel, tunnel, forward := setupForwardWithExitMember(t)
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", userTunnel.ID).Update("status", userTunnelStatusDeleting).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Forward{}).Where("id = ?", forward.ID).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	var calls []string
	originalService, originalChains, originalRemote := deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService
	deleteUserTunnelGostService = func(nodeID int64, _ string) ws.GostResult {
		calls = append(calls, fmt.Sprintf("service:%d", nodeID))
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	deleteUserTunnelGostChains = func(nodeID int64, _ string) ws.GostResult {
		calls = append(calls, fmt.Sprintf("chains:%d", nodeID))
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	deleteUserTunnelGostRemoteService = func(nodeID int64, _ string) ws.GostResult {
		calls = append(calls, fmt.Sprintf("remote:%d", nodeID))
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() {
		deleteUserTunnelGostService, deleteUserTunnelGostChains, deleteUserTunnelGostRemoteService = originalService, originalChains, originalRemote
	})

	SyncNodeForwardsOnConnect(tunnel.InNodeID)
	SyncNodeForwardsOnConnect(member.OutNodeID)
	want := []string{fmt.Sprintf("service:%d", tunnel.InNodeID), fmt.Sprintf("chains:%d", tunnel.InNodeID), fmt.Sprintf("remote:%d", member.OutNodeID)}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("reconnect tombstone cleanup calls=%v want=%v", calls, want)
	}
}

func TestNftRuleSnapshotQueryFailurePreventsApply(t *testing.T) {
	inNode, _, _, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	callback := "test:fail-nft-forward-snapshot"
	if err := model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.Forward{}).TableName()) {
			tx.AddError(errors.New("injected forward snapshot failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(callback) })
	original := sendNftRefreshMessage
	sent := false
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
		sent = true
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() { sendNftRefreshMessage = original })

	if err := doRefreshNodeForwardRulesChecked(inNode.ID); err == nil {
		t.Fatal("incomplete nft snapshot was accepted")
	}
	if sent {
		t.Fatal("incomplete nft snapshot was sent to agent")
	}
}

func TestNftRefreshNodeCollectionFailurePreventsApply(t *testing.T) {
	inNode, _, _, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	callback := "test:fail-nft-tunnel-collection"
	if err := model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.Tunnel{}).TableName()) {
			tx.AddError(errors.New("injected tunnel collection failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(callback) })
	original := sendNftRefreshMessage
	sent := false
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
		sent = true
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() { sendNftRefreshMessage = original })
	if err := RefreshNodeForwardRulesChecked(inNode.ID); err == nil {
		t.Fatal("incomplete refresh node collection was accepted")
	}
	if sent {
		t.Fatal("incomplete refresh node collection reached agent")
	}
}

func TestCompleteReverseReplaceFailureMarksErrorThenRefreshesDesiredState(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", tunnel.ID).Update("udp_listen_addr", "").Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental, originalRefresh := sendNftIncrementalMessage, sendNftRefreshMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage, sendNftRefreshMessage = originalIncremental, originalRefresh
		addCompleteGostRemoteService, deleteCompleteGostRemoteService = originalAdd, originalDelete
	})
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18444 dnat to 10.0.0.2:28444 # handle 444"
	replaceCalls := 0
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			replaceCalls++
			if replaceCalls == 1 {
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			return ws.GostResult{Msg: "injected reverse replace failure"}
		case "FindRuleHandles":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","handles":[{"chain":"prerouting","handle":445}]}`)}
		default:
			t.Fatalf("command=%q", command)
			return ws.GostResult{}
		}
	}
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: "injected Gost failure"}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	refreshCalls := 0
	sendNftRefreshMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if nodeID == inNode.ID && command == "ApplyNftRules" {
			refreshCalls++
			if got := data.(map[string]interface{})["rules"].([]string); len(got) != 0 {
				t.Fatalf("error desired refresh retained managed rules: %v", got)
			}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	outPort, targetPort := 28444, 443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18444, OutPort: &outPort, TargetPort: &targetPort,
		TargetHost: "198.51.100.244", Protocol: "tcp", RawRule: raw,
	})
	if err == nil || id != 0 || replaceCalls != 2 {
		t.Fatalf("Complete=(%d,%v) replaceCalls=%d", id, err, replaceCalls)
	}
	if refreshCalls == 0 {
		t.Fatal("reverse Replace failure did not trigger checked full-node convergence")
	}
	var retained model.Forward
	if err := model.DB.Where("in_port = ?", 18444).First(&retained).Error; err != nil || retained.Status != forwardStatusError {
		t.Fatalf("retained desired=(%+v,%v)", retained, err)
	}
}

type resultSnapshot struct {
	code int
	msg  string
}
