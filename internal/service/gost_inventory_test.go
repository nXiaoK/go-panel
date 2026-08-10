package service

import (
	"reflect"
	"testing"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestDesiredGostManagedRuntimeNamesCoversMixedEntryExitAndPermissionStates(t *testing.T) {
	user, permission, tunnel, forward := setupForwardWithExitMember(t)
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	base := formatServiceName(forward.ID, user.ID, permission.ID)
	services, chains, err := desiredGostManagedRuntimeNames(tunnel.InNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{base + "_tcp", base + "_udp"}; !reflect.DeepEqual(services, want) || !reflect.DeepEqual(chains, []string{base + "_chains"}) {
		t.Fatalf("entry desired services=%v chains=%v", services, chains)
	}
	services, chains, err = desiredGostManagedRuntimeNames(member.OutNodeID)
	if err != nil || !reflect.DeepEqual(services, []string{base + "_tls"}) || len(chains) != 0 {
		t.Fatalf("exit desired services=%v chains=%v err=%v", services, chains, err)
	}
	// Disabled and deleting permissions are non-serving and must be removed.
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", permission.ID).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	services, chains, err = desiredGostManagedRuntimeNames(tunnel.InNodeID)
	if err != nil || len(services)+len(chains) != 0 {
		t.Fatalf("disabled desired services=%v chains=%v err=%v", services, chains, err)
	}
	if err := model.DB.Model(&model.UserTunnel{}).Where("id = ?", permission.ID).Update("status", userTunnelStatusDeleting).Error; err != nil {
		t.Fatal(err)
	}
	services, chains, err = desiredGostManagedRuntimeNames(tunnel.InNodeID)
	if err != nil || len(services)+len(chains) != 0 {
		t.Fatalf("tombstone desired services=%v chains=%v err=%v", services, chains, err)
	}
}

func TestDesiredGostManagedRuntimeDeniesMissingNormalPermissionAndKeepsRealAdmin(t *testing.T) {
	user, permission, tunnel, forward := setupForwardWithExitMember(t)
	if err := model.DB.Delete(&model.UserTunnel{}, permission.ID).Error; err != nil {
		t.Fatal(err)
	}
	services, chains, err := desiredGostManagedRuntimeNames(tunnel.InNodeID)
	if err != nil || len(services)+len(chains) != 0 {
		t.Fatalf("normal missing permission desired=%v/%v err=%v", services, chains, err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", user.ID).Update("role_id", adminRoleID).Error; err != nil {
		t.Fatal(err)
	}
	services, chains, err = desiredGostManagedRuntimeNames(tunnel.InNodeID)
	base := formatServiceName(forward.ID, user.ID, 0)
	if err != nil || !reflect.DeepEqual(services, []string{base + "_tcp", base + "_udp"}) || !reflect.DeepEqual(chains, []string{base + "_chains"}) {
		t.Fatalf("admin missing permission desired=%v/%v err=%v", services, chains, err)
	}
}

func TestReconnectSendsEmptyDesiredToCleanHistoricalOrphans(t *testing.T) {
	initUpdateNodeTestDB(t)
	node := createUpdateNodeTestNode(t, "orphan-reconcile", forwardModeGost)
	original := reconcileGostManagedRuntime
	called := false
	reconcileGostManagedRuntime = func(nodeID int64, services, chains []string) ws.GostResult {
		called = true
		if nodeID != node.ID || len(services)+len(chains) != 0 {
			t.Fatalf("reconcile=(node=%d services=%v chains=%v)", nodeID, services, chains)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	t.Cleanup(func() { reconcileGostManagedRuntime = original })
	SyncNodeForwardsOnConnect(node.ID)
	if !called {
		t.Fatal("reconnect did not send complete empty desired inventory")
	}
}
