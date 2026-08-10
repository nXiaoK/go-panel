package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// setupNftSyncFixture creates one nftables node so nft refresh chokepoints run.
func setupNftSyncFixture(t *testing.T) model.Node {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "nft-node", Secret: "nft-node-secret", IP: "192.0.2.30", ServerIP: "192.0.2.30",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func stubNftRefresh(t *testing.T, fn func(nodeID int64) ws.GostResult) {
	t.Helper()
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	sendNftRefreshMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		return fn(nodeID)
	}
}

func TestUncertainNftRefreshMarksNodePendingUntilReconciled(t *testing.T) {
	node := setupNftSyncFixture(t)

	// A lost response keeps committed desired state but must not report the
	// node as synchronized: the mutation is pending, never falsely applied.
	stubNftRefresh(t, func(int64) ws.GostResult {
		return ws.GostResult{Msg: "等待响应超时", OutcomeUnknown: true}
	})
	if err := doRefreshNodeForwardRulesChecked(node.ID); err != nil {
		t.Fatalf("uncertain refresh should keep desired state: %v", err)
	}

	status := NodesSyncStatus(node.ID)
	if status.Applied || !status.Pending {
		t.Fatalf("status=%+v, want pending", status)
	}

	// The 30s reconciler converges the node without waiting for a reconnect.
	stubNftRefresh(t, func(int64) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	})
	ReconcilePendingNodes(context.Background())

	status = NodesSyncStatus(node.ID)
	if !status.Applied || status.Pending {
		t.Fatalf("status=%+v, want applied", status)
	}
}

func TestDefiniteNftRefreshLeavesNodeClean(t *testing.T) {
	node := setupNftSyncFixture(t)
	stubNftRefresh(t, func(int64) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	})
	if err := doRefreshNodeForwardRulesChecked(node.ID); err != nil {
		t.Fatal(err)
	}
	status := NodesSyncStatus(node.ID)
	if !status.Applied || status.Pending {
		t.Fatalf("status=%+v, want applied without pending churn", status)
	}
}

func TestReconcilePendingNodesSkipsLockedNode(t *testing.T) {
	node := setupNftSyncFixture(t)
	if err := MarkNodesDirty(model.DB, node.ID); err != nil {
		t.Fatal(err)
	}
	applier := &fakeNodeApplier{}
	setNodeConfigApplierForTest(t, applier)

	unlock := lockNftSagaNodes([]int64{node.ID})
	ReconcilePendingNodes(context.Background())
	if applier.calls != 0 {
		t.Fatalf("locked node was reconciled %d times", applier.calls)
	}
	unlock()

	ReconcilePendingNodes(context.Background())
	if applier.calls != 1 {
		t.Fatalf("calls=%d, want 1", applier.calls)
	}
	status := NodesSyncStatus(node.ID)
	if !status.Applied {
		t.Fatalf("status=%+v, want applied", status)
	}
}

func TestNodesSyncStatusReportsFirstError(t *testing.T) {
	node := setupNftSyncFixture(t)
	if err := MarkNodesDirty(model.DB, node.ID); err != nil {
		t.Fatal(err)
	}
	res := persistNodeSyncFailure(node.ID, 1, 0, time.Now().UnixMilli(), "节点离线")
	if res.State != SyncFailed {
		t.Fatalf("state=%s", res.State)
	}
	status := NodesSyncStatus(node.ID)
	if status.Applied || !status.Pending || status.LastError != "节点离线" {
		t.Fatalf("status=%+v", status)
	}
}
