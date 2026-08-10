package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
	"gorm.io/gorm"
)

func TestUpdateTunnelWaitsForCompleteAndSynchronizesFreshForwardSet(t *testing.T) {
	inNode, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18600 dnat to 198.51.100.60:443 # handle 600"
	replaceStarted, releaseReplace := installBlockingTunnelCompleteAgent(t, raw)

	var refreshMu sync.Mutex
	refreshCalls := 0
	originalRefresh := sendNftRefreshMessage
	sendNftRefreshMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command != "ApplyNftRules" {
			t.Fatalf("refresh command=%q", command)
		}
		refreshMu.Lock()
		refreshCalls++
		refreshMu.Unlock()
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })

	port := 443
	completeDone := make(chan error, 1)
	go func() {
		_, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
			TunnelID: tunnel.ID, InPort: 18600, OutPort: &port, TargetHost: "198.51.100.60", Protocol: "tcp", RawRule: raw,
		})
		completeDone <- err
	}()
	waitTunnelSagaSignal(t, replaceStarted, "Complete did not reach Replace")

	updateDone := make(chan result.R, 1)
	go func() { updateDone <- UpdateTunnel(tunnelUpdateRequest(tunnel.ID, "udp")) }()
	time.Sleep(50 * time.Millisecond)
	var during model.Tunnel
	if err := model.DB.First(&during, tunnel.ID).Error; err != nil {
		t.Fatalf("read tunnel during Complete: %v", err)
	}
	early := false
	select {
	case <-updateDone:
		early = true
	default:
	}
	changedDuringComplete := defaultProtocol(during.Protocol) != "tcp"

	close(releaseReplace)
	if err := <-completeDone; err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	res := <-updateDone
	if early || changedDuringComplete {
		t.Fatalf("UpdateTunnel mutated before Complete linearized: early=%v protocol=%q", early, defaultProtocol(during.Protocol))
	}
	if res.Code != 0 {
		t.Fatalf("UpdateTunnel=%+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("tunnel_id = ?", tunnel.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("forward count=%d err=%v", count, err)
	}
	refreshMu.Lock()
	gotRefreshCalls := refreshCalls
	refreshMu.Unlock()
	if gotRefreshCalls < 2 {
		t.Fatalf("UpdateTunnel used stale forward snapshot: refresh calls=%d, want at least 2", gotRefreshCalls)
	}
}

func TestDeleteTunnelWaitsForCompleteAndUsesFreshForwardCount(t *testing.T) {
	inNode, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18601 dnat to 198.51.100.61:443 # handle 601"
	replaceStarted, releaseReplace := installBlockingTunnelCompleteAgent(t, raw)
	port := 443
	completeDone := make(chan error, 1)
	go func() {
		_, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
			TunnelID: tunnel.ID, InPort: 18601, OutPort: &port, TargetHost: "198.51.100.61", Protocol: "tcp", RawRule: raw,
		})
		completeDone <- err
	}()
	waitTunnelSagaSignal(t, replaceStarted, "Complete did not reach Replace")

	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	select {
	case res := <-deleteDone:
		close(releaseReplace)
		<-completeDone
		t.Fatalf("DeleteTunnel bypassed Complete: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseReplace)
	if err := <-completeDone; err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	res := <-deleteDone
	if res.Code == 0 || !strings.Contains(res.Msg, "1 个转发") {
		t.Fatalf("DeleteTunnel did not use fresh forward count: %+v", res)
	}
	var got model.Tunnel
	if err := model.DB.First(&got, tunnel.ID).Error; err != nil {
		t.Fatalf("referenced tunnel was deleted: %v", err)
	}
}

func TestCompleteWaitsForProtocolUpdateAndRevalidatesTunnel(t *testing.T) {
	inNode, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	updateStarted, releaseUpdate := installBlockingTunnelMutation(t, "update")
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18602 dnat to 198.51.100.62:443 # handle 602"
	replaceSeen, _ := installObservingTunnelCompleteAgent(t, raw)

	updateDone := make(chan result.R, 1)
	go func() { updateDone <- UpdateTunnel(tunnelUpdateRequest(tunnel.ID, "udp")) }()
	waitTunnelSagaSignal(t, updateStarted, "UpdateTunnel did not reach database update")

	port := 443
	completeDone := make(chan error, 1)
	go func() {
		_, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
			TunnelID: tunnel.ID, InPort: 18602, OutPort: &port, TargetHost: "198.51.100.62", Protocol: "tcp", RawRule: raw,
		})
		completeDone <- err
	}()
	select {
	case <-replaceSeen:
		close(releaseUpdate)
		<-updateDone
		<-completeDone
		t.Fatal("Complete bypassed the protocol-update tunnel saga")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseUpdate)
	if res := <-updateDone; res.Code != 0 {
		t.Fatalf("UpdateTunnel=%+v", res)
	}
	if err := <-completeDone; err == nil || !strings.Contains(err.Error(), "协议") {
		t.Fatalf("Complete did not revalidate updated protocol: %v", err)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("tunnel_id = ?", tunnel.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale Complete persisted forward: count=%d err=%v", count, err)
	}
}

func TestCompleteWaitsForDeleteAndCannotOrphanWithForeignKeysOff(t *testing.T) {
	inNode, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	deleteStarted, releaseDelete := installBlockingTunnelMutation(t, "delete")
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18603 dnat to 198.51.100.63:443 # handle 603"
	replaceSeen, _ := installObservingTunnelCompleteAgent(t, raw)

	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	waitTunnelSagaSignal(t, deleteStarted, "DeleteTunnel did not reach database delete")

	port := 443
	completeDone := make(chan error, 1)
	go func() {
		_, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
			TunnelID: tunnel.ID, InPort: 18603, OutPort: &port, TargetHost: "198.51.100.63", Protocol: "tcp", RawRule: raw,
		})
		completeDone <- err
	}()
	select {
	case <-replaceSeen:
		close(releaseDelete)
		<-deleteDone
		<-completeDone
		t.Fatal("Complete bypassed the delete tunnel saga")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if res := <-deleteDone; res.Code != 0 {
		t.Fatalf("DeleteTunnel=%+v", res)
	}
	if err := <-completeDone; err == nil {
		t.Fatal("Complete succeeded after its tunnel was deleted")
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("tunnel_id = ?", tunnel.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("foreign_keys=off orphan count=%d err=%v", count, err)
	}
}

func TestTunnelSyncWaitsForForwardUpdateAndPreservesFreshFields(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	outPort := 443
	forward := model.Forward{
		UserID: 1, UserName: "owner", Name: "old-name", TunnelID: tunnel.ID,
		RemoteAddr: "198.51.100.70:443", Strategy: "fifo", InPort: 18700, OutPort: &outPort,
		CreatedTime: now, UpdatedTime: now, Status: forwardStatusPaused,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	updateStarted, releaseUpdate := installBlockingForwardMutation(t)
	port := forward.InPort
	userUpdateDone := make(chan result.R, 1)
	go func() {
		userUpdateDone <- updateForwardInternal(dto.ForwardUpdateDto{
			ID: forward.ID, UserID: forward.UserID, Name: "fresh-name", TunnelID: tunnel.ID,
			RemoteAddr: "203.0.113.70:8443", Strategy: "round", InPort: &port,
		}, forward.UserID, adminRoleID, forward.UserName)
	}()
	waitTunnelSagaSignal(t, updateStarted, "UpdateForward did not reach database save")

	syncDone := make(chan result.R, 1)
	go func() { syncDone <- syncForwardAfterTunnelUpdate(forward.ID, tunnel.ID) }()
	select {
	case res := <-syncDone:
		close(releaseUpdate)
		<-userUpdateDone
		t.Fatalf("tunnel sync bypassed the forward saga: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseUpdate)
	if res := <-userUpdateDone; res.Code != 0 {
		t.Fatalf("UpdateForward=%+v", res)
	}
	if res := <-syncDone; res.Code != 0 {
		t.Fatalf("syncForwardAfterTunnelUpdate=%+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil {
		t.Fatalf("read forward: %v", err)
	}
	if got.Name != "fresh-name" || got.RemoteAddr != "203.0.113.70:8443" || got.Strategy != "round" {
		t.Fatalf("tunnel sync overwrote fresh fields: name=%q remote=%q strategy=%q", got.Name, got.RemoteAddr, got.Strategy)
	}
}

func TestAssignUserTunnelAndDeleteTunnelAreLinearized(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "assign-first")
	createStarted, releaseCreate := installBlockingModelCreate(t, (model.UserTunnel{}).TableName())

	assignDone := make(chan result.R, 1)
	go func() {
		assignDone <- AssignUserTunnel(dto.UserTunnelDto{UserID: user.ID, TunnelID: tunnel.ID, Num: 1, ExpTime: time.Now().Add(time.Hour).UnixMilli()})
	}()
	waitTunnelSagaSignal(t, createStarted, "AssignUserTunnel did not reach database create")
	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	select {
	case res := <-deleteDone:
		close(releaseCreate)
		<-assignDone
		t.Fatalf("DeleteTunnel bypassed AssignUserTunnel: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCreate)
	if res := <-assignDone; res.Code != 0 {
		t.Fatalf("AssignUserTunnel=%+v", res)
	}
	if res := <-deleteDone; res.Code == 0 || !strings.Contains(res.Msg, "用户权限") {
		t.Fatalf("DeleteTunnel ignored fresh user_tunnel: %+v", res)
	}
}

func TestAssignUserTunnelWaitsForDeleteAndCannotOrphan(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "delete-first")
	deleteStarted, releaseDelete := installBlockingTunnelMutation(t, "delete")

	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	waitTunnelSagaSignal(t, deleteStarted, "DeleteTunnel did not reach database delete")
	assignDone := make(chan result.R, 1)
	go func() {
		assignDone <- AssignUserTunnel(dto.UserTunnelDto{UserID: user.ID, TunnelID: tunnel.ID, Num: 1, ExpTime: time.Now().Add(time.Hour).UnixMilli()})
	}()
	select {
	case res := <-assignDone:
		close(releaseDelete)
		<-deleteDone
		t.Fatalf("AssignUserTunnel bypassed DeleteTunnel: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if res := <-deleteDone; res.Code != 0 {
		t.Fatalf("DeleteTunnel=%+v", res)
	}
	if res := <-assignDone; res.Code == 0 {
		t.Fatalf("AssignUserTunnel succeeded after delete: %+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.UserTunnel{}).Where("tunnel_id = ?", tunnel.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan user_tunnel count=%d err=%v", count, err)
	}
}

func TestCreateSpeedLimitAndDeleteTunnelAreLinearized(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalAdd := addSpeedLimitRemote
	addSpeedLimitRemote = func(_ int64, _ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() { addSpeedLimitRemote = originalAdd })
	createStarted, releaseCreate := installBlockingModelCreate(t, (model.SpeedLimit{}).TableName())

	createDone := make(chan result.R, 1)
	go func() {
		createDone <- CreateSpeedLimit(dto.SpeedLimitDto{Name: "limited", Speed: 100, TunnelID: tunnel.ID, TunnelName: tunnel.Name})
	}()
	waitTunnelSagaSignal(t, createStarted, "CreateSpeedLimit did not reach database create")
	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	select {
	case res := <-deleteDone:
		close(releaseCreate)
		<-createDone
		t.Fatalf("DeleteTunnel bypassed CreateSpeedLimit: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCreate)
	if res := <-createDone; res.Code != 0 {
		t.Fatalf("CreateSpeedLimit=%+v", res)
	}
	if res := <-deleteDone; res.Code == 0 || !strings.Contains(res.Msg, "限速规则") {
		t.Fatalf("DeleteTunnel ignored fresh speed_limit: %+v", res)
	}
}

func TestCreateSpeedLimitWaitsForDeleteAndCannotOrphan(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	deleteStarted, releaseDelete := installBlockingTunnelMutation(t, "delete")

	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(tunnel.ID) }()
	waitTunnelSagaSignal(t, deleteStarted, "DeleteTunnel did not reach database delete")
	createDone := make(chan result.R, 1)
	go func() {
		createDone <- CreateSpeedLimit(dto.SpeedLimitDto{Name: "late", Speed: 100, TunnelID: tunnel.ID, TunnelName: tunnel.Name})
	}()
	select {
	case res := <-createDone:
		close(releaseDelete)
		<-deleteDone
		t.Fatalf("CreateSpeedLimit bypassed DeleteTunnel: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if res := <-deleteDone; res.Code != 0 {
		t.Fatalf("DeleteTunnel=%+v", res)
	}
	if res := <-createDone; res.Code == 0 {
		t.Fatalf("CreateSpeedLimit succeeded after delete: %+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.SpeedLimit{}).Where("tunnel_id = ?", tunnel.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan speed_limit count=%d err=%v", count, err)
	}
}

func TestUpdateSpeedLimitAndTargetDeleteAreLinearized(t *testing.T) {
	_, _, oldTunnel, targetTunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	speedLimit := createTunnelSagaSpeedLimit(t, oldTunnel, "moving")
	installSuccessfulSpeedLimitRemotes(t)
	deleteCalls := 0
	deleteSpeedLimitRemote = func(_ int64, _ int64) ws.GostResult {
		deleteCalls++
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	updateStarted, releaseUpdate := installBlockingModelUpdate(t, (model.SpeedLimit{}).TableName())

	updateDone := make(chan result.R, 1)
	go func() {
		updateDone <- UpdateSpeedLimit(dto.SpeedLimitUpdateDto{
			ID: speedLimit.ID, Name: "moved", Speed: 200,
			TunnelID: targetTunnel.ID, TunnelName: targetTunnel.Name,
		})
	}()
	waitTunnelSagaSignal(t, updateStarted, "UpdateSpeedLimit did not reach database update")
	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(targetTunnel.ID) }()
	select {
	case res := <-deleteDone:
		close(releaseUpdate)
		<-updateDone
		t.Fatalf("DeleteTunnel bypassed UpdateSpeedLimit: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseUpdate)
	if res := <-updateDone; res.Code != 0 {
		t.Fatalf("UpdateSpeedLimit=%+v", res)
	}
	if res := <-deleteDone; res.Code == 0 || !strings.Contains(res.Msg, "限速规则") {
		t.Fatalf("DeleteTunnel ignored migrated speed_limit: %+v", res)
	}
	var got model.SpeedLimit
	if err := model.DB.First(&got, speedLimit.ID).Error; err != nil || got.TunnelID != targetTunnel.ID {
		t.Fatalf("speed_limit tunnel=%d err=%v", got.TunnelID, err)
	}
	if deleteCalls != 0 {
		t.Fatalf("same-entry tunnel move deleted the updated limiter %d times", deleteCalls)
	}
}

func TestUpdateSpeedLimitWaitsForTargetDeleteAndCannotOrphan(t *testing.T) {
	_, _, oldTunnel, targetTunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	speedLimit := createTunnelSagaSpeedLimit(t, oldTunnel, "late-move")
	installSuccessfulSpeedLimitRemotes(t)
	deleteStarted, releaseDelete := installBlockingTunnelMutation(t, "delete")

	deleteDone := make(chan result.R, 1)
	go func() { deleteDone <- DeleteTunnel(targetTunnel.ID) }()
	waitTunnelSagaSignal(t, deleteStarted, "DeleteTunnel did not reach database delete")
	updateDone := make(chan result.R, 1)
	go func() {
		updateDone <- UpdateSpeedLimit(dto.SpeedLimitUpdateDto{
			ID: speedLimit.ID, Name: "late-moved", Speed: 200,
			TunnelID: targetTunnel.ID, TunnelName: targetTunnel.Name,
		})
	}()
	select {
	case res := <-updateDone:
		close(releaseDelete)
		<-deleteDone
		t.Fatalf("UpdateSpeedLimit bypassed target DeleteTunnel: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if res := <-deleteDone; res.Code != 0 {
		t.Fatalf("DeleteTunnel=%+v", res)
	}
	if res := <-updateDone; res.Code == 0 {
		t.Fatalf("UpdateSpeedLimit succeeded after target delete: %+v", res)
	}
	var got model.SpeedLimit
	if err := model.DB.First(&got, speedLimit.ID).Error; err != nil {
		t.Fatalf("read speed_limit: %v", err)
	}
	if got.TunnelID != oldTunnel.ID {
		t.Fatalf("orphan/moved speed_limit tunnel=%d want=%d", got.TunnelID, oldTunnel.ID)
	}
}

func TestUpdateUserTunnelAndSpeedLimitMoveAreLinearized(t *testing.T) {
	_, _, oldTunnel, targetTunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "permission-first")
	speedLimit := createTunnelSagaSpeedLimit(t, oldTunnel, "permission-speed")
	userTunnel := createTunnelSagaUserTunnel(t, user.ID, oldTunnel.ID)
	installSuccessfulSpeedLimitRemotes(t)
	permissionStarted, releasePermission := installBlockingModelUpdate(t, (model.UserTunnel{}).TableName())
	status := 1

	permissionDone := make(chan result.R, 1)
	go func() {
		permissionDone <- UpdateUserTunnel(dto.UserTunnelUpdateDto{
			ID: userTunnel.ID, Flow: 1, Num: 1, Status: &status, SpeedID: &speedLimit.ID,
		})
	}()
	waitTunnelSagaSignal(t, permissionStarted, "UpdateUserTunnel did not reach database update")
	moveDone := make(chan result.R, 1)
	go func() {
		moveDone <- UpdateSpeedLimit(dto.SpeedLimitUpdateDto{
			ID: speedLimit.ID, Name: "moved", Speed: 200, TunnelID: targetTunnel.ID, TunnelName: targetTunnel.Name,
		})
	}()
	select {
	case res := <-moveDone:
		close(releasePermission)
		<-permissionDone
		t.Fatalf("UpdateSpeedLimit bypassed UpdateUserTunnel: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releasePermission)
	if res := <-permissionDone; res.Code != 0 {
		t.Fatalf("UpdateUserTunnel=%+v", res)
	}
	if res := <-moveDone; res.Code == 0 || !strings.Contains(res.Msg, "已有用户") {
		t.Fatalf("UpdateSpeedLimit moved referenced speed: %+v", res)
	}
}

func TestUpdateUserTunnelWaitsForSpeedLimitMoveAndRejectsCrossTunnelReference(t *testing.T) {
	_, _, oldTunnel, targetTunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "move-first")
	speedLimit := createTunnelSagaSpeedLimit(t, oldTunnel, "moving-speed")
	userTunnel := createTunnelSagaUserTunnel(t, user.ID, oldTunnel.ID)
	installSuccessfulSpeedLimitRemotes(t)
	moveStarted, releaseMove := installBlockingModelUpdate(t, (model.SpeedLimit{}).TableName())

	moveDone := make(chan result.R, 1)
	go func() {
		moveDone <- UpdateSpeedLimit(dto.SpeedLimitUpdateDto{
			ID: speedLimit.ID, Name: "moved", Speed: 200, TunnelID: targetTunnel.ID, TunnelName: targetTunnel.Name,
		})
	}()
	waitTunnelSagaSignal(t, moveStarted, "UpdateSpeedLimit did not reach database update")
	status := 1
	permissionDone := make(chan result.R, 1)
	go func() {
		permissionDone <- UpdateUserTunnel(dto.UserTunnelUpdateDto{
			ID: userTunnel.ID, Flow: 1, Num: 1, Status: &status, SpeedID: &speedLimit.ID,
		})
	}()
	select {
	case res := <-permissionDone:
		close(releaseMove)
		<-moveDone
		t.Fatalf("UpdateUserTunnel bypassed UpdateSpeedLimit: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseMove)
	if res := <-moveDone; res.Code != 0 {
		t.Fatalf("UpdateSpeedLimit=%+v", res)
	}
	if res := <-permissionDone; res.Code == 0 || !strings.Contains(res.Msg, "不属于该隧道") {
		t.Fatalf("UpdateUserTunnel accepted cross-tunnel speed: %+v", res)
	}
	var got model.UserTunnel
	if err := model.DB.First(&got, userTunnel.ID).Error; err != nil || got.SpeedID != nil {
		t.Fatalf("user_tunnel speed=%v err=%v", got.SpeedID, err)
	}
}

func TestCreateSpeedLimitKnownFailureCannotRaceAssignment(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	user := createTunnelSagaUser(t, "create-failure")
	remoteStarted, releaseRemote := make(chan struct{}), make(chan struct{})
	originalAdd := addSpeedLimitRemote
	addSpeedLimitRemote = func(_ int64, _ int64, _ string) ws.GostResult {
		close(remoteStarted)
		<-releaseRemote
		return ws.GostResult{Msg: "known failure"}
	}
	t.Cleanup(func() { addSpeedLimitRemote = originalAdd })
	createDone := make(chan result.R, 1)
	go func() {
		createDone <- CreateSpeedLimit(dto.SpeedLimitDto{Name: "failing", Speed: 100, TunnelID: tunnel.ID, TunnelName: tunnel.Name})
	}()
	waitTunnelSagaSignal(t, remoteStarted, "CreateSpeedLimit did not reach remote add")
	var speedLimit model.SpeedLimit
	if err := model.DB.Where("name = ?", "failing").First(&speedLimit).Error; err != nil {
		t.Fatalf("read pending speed_limit: %v", err)
	}
	assignDone := make(chan result.R, 1)
	go func() {
		assignDone <- AssignUserTunnel(dto.UserTunnelDto{UserID: user.ID, TunnelID: tunnel.ID, Num: 1, ExpTime: time.Now().Add(time.Hour).UnixMilli(), SpeedID: &speedLimit.ID})
	}()
	select {
	case res := <-assignDone:
		close(releaseRemote)
		<-createDone
		t.Fatalf("AssignUserTunnel bypassed CreateSpeedLimit remote saga: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRemote)
	if res := <-createDone; res.Code == 0 {
		t.Fatalf("CreateSpeedLimit known failure=%+v", res)
	}
	if res := <-assignDone; res.Code == 0 {
		t.Fatalf("AssignUserTunnel referenced rolled-back speed: %+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.UserTunnel{}).Where("speed_id = ?", speedLimit.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("orphan speed reference count=%d err=%v", count, err)
	}
}

func TestDeleteSpeedLimitUnknownRestoresDurableDesired(t *testing.T) {
	_, _, tunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	speedLimit := createTunnelSagaSpeedLimit(t, tunnel, "delete-unknown")
	originalDelete := deleteSpeedLimitRemote
	deleteSpeedLimitRemote = func(_ int64, _ int64) ws.GostResult {
		return ws.GostResult{Msg: "节点连接已替换", OutcomeUnknown: true}
	}
	t.Cleanup(func() { deleteSpeedLimitRemote = originalDelete })
	if res := DeleteSpeedLimit(speedLimit.ID); res.Code == 0 {
		t.Fatalf("DeleteSpeedLimit unknown=%+v", res)
	}
	var got model.SpeedLimit
	if err := model.DB.First(&got, speedLimit.ID).Error; err != nil || got.TunnelID != tunnel.ID {
		t.Fatalf("durable desired not restored: tunnel=%d err=%v", got.TunnelID, err)
	}
}

func TestSyncGostNodeLimitersConvergesDesiredAndMigratedIDs(t *testing.T) {
	inNode, outNode, oldTunnel, targetTunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", targetTunnel.ID).
		Updates(map[string]interface{}{"in_node_id": outNode.ID, "in_ip": outNode.IP}).Error; err != nil {
		t.Fatalf("move target entry node: %v", err)
	}
	targetTunnel.InNodeID = outNode.ID
	targetTunnel.InIP = outNode.IP
	speedLimit := createTunnelSagaSpeedLimit(t, oldTunnel, "reconnect")
	var mu sync.Mutex
	var calls []string
	originalUpdate, originalDelete := updateSpeedLimitRemote, deleteSpeedLimitRemote
	updateSpeedLimitRemote = func(nodeID int64, id int64, _ string) ws.GostResult {
		mu.Lock()
		calls = append(calls, fmt.Sprintf("update:%d:%d", nodeID, id))
		mu.Unlock()
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	deleteSpeedLimitRemote = func(nodeID int64, id int64) ws.GostResult {
		mu.Lock()
		calls = append(calls, fmt.Sprintf("delete:%d:%d", nodeID, id))
		mu.Unlock()
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() { updateSpeedLimitRemote, deleteSpeedLimitRemote = originalUpdate, originalDelete })
	syncGostNodeLimiters(inNode.ID)
	if err := model.DB.Model(&model.SpeedLimit{}).Where("id = ?", speedLimit.ID).
		Updates(map[string]interface{}{"tunnel_id": targetTunnel.ID, "tunnel_name": targetTunnel.Name}).Error; err != nil {
		t.Fatalf("move speed_limit: %v", err)
	}
	syncGostNodeLimiters(inNode.ID)
	syncGostNodeLimiters(outNode.ID)
	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{
		fmt.Sprintf("update:%d:%d", inNode.ID, speedLimit.ID),
		fmt.Sprintf("delete:%d:%d", inNode.ID, speedLimit.ID),
		fmt.Sprintf("update:%d:%d", outNode.ID, speedLimit.ID),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("limiter convergence calls=%v want=%v", got, want)
	}
}

func tunnelUpdateRequest(id int64, protocol string) dto.TunnelUpdateDto {
	req := dto.TunnelUpdateDto{
		ID: id, Name: "port-updated", Flow: 1, Protocol: protocol,
	}
	if protocol == "udp" {
		req.UDPListenAddr = "0.0.0.0"
	} else {
		req.TCPListenAddr = "0.0.0.0"
	}
	return req
}

func installBlockingTunnelCompleteAgent(t *testing.T, raw string) (<-chan struct{}, chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	original := sendNftIncrementalMessage
	var once sync.Once
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			once.Do(func() { close(started) })
			<-release
			return ws.GostResult{Msg: gost.SuccessMsg}
		default:
			t.Fatalf("incremental command=%q", command)
			return ws.GostResult{}
		}
	}
	t.Cleanup(func() { sendNftIncrementalMessage = original })
	return started, release
}

func installObservingTunnelCompleteAgent(t *testing.T, raw string) (<-chan struct{}, func()) {
	t.Helper()
	seen := make(chan struct{})
	original := sendNftIncrementalMessage
	var once sync.Once
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			once.Do(func() { close(seen) })
			return ws.GostResult{Msg: gost.SuccessMsg}
		default:
			t.Fatalf("incremental command=%q", command)
			return ws.GostResult{}
		}
	}
	restore := func() { sendNftIncrementalMessage = original }
	t.Cleanup(restore)
	return seen, restore
}

func installBlockingTunnelMutation(t *testing.T, operation string) (<-chan struct{}, chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	name := "tunnel_saga_" + operation
	var once sync.Once
	callback := func(tx *gorm.DB) {
		if gormStatementTable(tx) != (model.Tunnel{}).TableName() {
			return
		}
		once.Do(func() { close(started) })
		<-release
	}
	var registerErr error
	if operation == "update" {
		registerErr = model.DB.Callback().Update().Before("gorm:update").Register(name, callback)
		t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(name) })
	} else {
		registerErr = model.DB.Callback().Delete().Before("gorm:delete").Register(name, callback)
		t.Cleanup(func() { _ = model.DB.Callback().Delete().Remove(name) })
	}
	if registerErr != nil {
		t.Fatalf("register %s callback: %v", operation, registerErr)
	}
	return started, release
}

func installBlockingForwardMutation(t *testing.T) (<-chan struct{}, chan struct{}) {
	return installBlockingModelUpdate(t, (model.Forward{}).TableName())
}

func installBlockingModelUpdate(t *testing.T, table string) (<-chan struct{}, chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	name := "tunnel_saga_update_" + table
	var once sync.Once
	callback := func(tx *gorm.DB) {
		if gormStatementTable(tx) != table {
			return
		}
		once.Do(func() { close(started) })
		<-release
	}
	if err := model.DB.Callback().Update().Before("gorm:update").Register(name, callback); err != nil {
		t.Fatalf("register %s update callback: %v", table, err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(name) })
	return started, release
}

func installBlockingModelCreate(t *testing.T, table string) (<-chan struct{}, chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	name := "tunnel_saga_create_" + table
	var once sync.Once
	callback := func(tx *gorm.DB) {
		if gormStatementTable(tx) != table {
			return
		}
		once.Do(func() { close(started) })
		<-release
	}
	if err := model.DB.Callback().Create().Before("gorm:create").Register(name, callback); err != nil {
		t.Fatalf("register %s create callback: %v", table, err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(name) })
	return started, release
}

func createTunnelSagaUser(t *testing.T, username string) model.User {
	t.Helper()
	now := time.Now().UnixMilli()
	expires := time.Now().Add(time.Hour).UnixMilli()
	user := model.User{
		User: username, Pwd: "test", RoleID: 2, ExpTime: &expires,
		Flow: 1, Num: 1, CreatedTime: now, UpdatedTime: &now, Status: model.UserStatusActive,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func createTunnelSagaSpeedLimit(t *testing.T, tunnel *model.Tunnel, name string) model.SpeedLimit {
	t.Helper()
	now := time.Now().UnixMilli()
	speedLimit := model.SpeedLimit{
		Name: name, Speed: 100, TunnelID: tunnel.ID, TunnelName: tunnel.Name,
		CreatedTime: now, UpdatedTime: &now, Status: 1,
	}
	if err := model.DB.Create(&speedLimit).Error; err != nil {
		t.Fatalf("create speed_limit: %v", err)
	}
	return speedLimit
}

func createTunnelSagaUserTunnel(t *testing.T, userID, tunnelID int64) model.UserTunnel {
	t.Helper()
	expires := time.Now().Add(time.Hour).UnixMilli()
	userTunnel := model.UserTunnel{
		UserID: userID, TunnelID: tunnelID, Num: 1, Flow: 1, ExpTime: &expires, Status: 1,
	}
	if err := model.DB.Create(&userTunnel).Error; err != nil {
		t.Fatalf("create user_tunnel: %v", err)
	}
	return userTunnel
}

func installSuccessfulSpeedLimitRemotes(t *testing.T) {
	t.Helper()
	originalAdd, originalUpdate, originalDelete := addSpeedLimitRemote, updateSpeedLimitRemote, deleteSpeedLimitRemote
	originalRefresh := sendNftRefreshMessage
	addSpeedLimitRemote = func(_ int64, _ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	updateSpeedLimitRemote = func(_ int64, _ int64, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	deleteSpeedLimitRemote = func(_ int64, _ int64) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	t.Cleanup(func() {
		addSpeedLimitRemote, updateSpeedLimitRemote, deleteSpeedLimitRemote = originalAdd, originalUpdate, originalDelete
		sendNftRefreshMessage = originalRefresh
	})
}

func waitTunnelSagaSignal(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}
