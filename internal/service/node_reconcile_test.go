package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/model"
)

func setupNodeSyncFixture(t *testing.T) model.Node {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: "sync-node", Secret: "sync-node-secret", IP: "192.0.2.10", ServerIP: "192.0.2.10",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeGost, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

// fakeNodeApplier fails or succeeds without touching node websockets. The err
// field is mutated between sequential ReconcileNode calls only.
type fakeNodeApplier struct {
	err   error
	calls int
	apply func()
}

func (f *fakeNodeApplier) ApplyNodeConfig(_ context.Context, _ model.Node) error {
	f.calls++
	if f.apply != nil {
		f.apply()
	}
	return f.err
}

func setNodeConfigApplierForTest(t *testing.T, applier NodeConfigApplier) {
	t.Helper()
	prev := swapNodeConfigApplier(applier)
	t.Cleanup(func() { swapNodeConfigApplier(prev) })
}

func loadNodeSyncState(t *testing.T, nodeID int64) model.NodeSyncState {
	t.Helper()
	var state model.NodeSyncState
	if err := model.DB.Where("node_id = ?", nodeID).First(&state).Error; err != nil {
		t.Fatalf("load node sync state: %v", err)
	}
	return state
}

func TestDirtyNodeRemainsPendingUntilReconnect(t *testing.T) {
	node := setupNodeSyncFixture(t)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return MarkNodesDirty(tx, node.ID)
	}); err != nil {
		t.Fatal(err)
	}
	applier := &fakeNodeApplier{err: errors.New("offline")}
	setNodeConfigApplierForTest(t, applier)
	first := ReconcileNode(context.Background(), node.ID)
	if first.State != SyncFailed {
		t.Fatalf("state=%s", first.State)
	}
	if first.LastError == "" {
		t.Fatal("failed reconciliation should retain the error")
	}
	applier.err = nil
	second := ReconcileNode(context.Background(), node.ID)
	if second.State != SyncApplied {
		t.Fatalf("state=%s", second.State)
	}
	if second.AppliedGeneration != second.DesiredGeneration {
		t.Fatalf("applied=%d desired=%d", second.AppliedGeneration, second.DesiredGeneration)
	}
	stored := loadNodeSyncState(t, node.ID)
	if stored.State != SyncApplied || stored.LastError != "" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestMarkNodesDirtyUpsertsAndIncrementsGeneration(t *testing.T) {
	node := setupNodeSyncFixture(t)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return MarkNodesDirty(tx, node.ID, node.ID, 0, -3)
	}); err != nil {
		t.Fatal(err)
	}
	first := loadNodeSyncState(t, node.ID)
	if first.State != SyncPending || first.DesiredGeneration <= first.AppliedGeneration {
		t.Fatalf("first=%+v", first)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return MarkNodesDirty(tx, node.ID)
	}); err != nil {
		t.Fatal(err)
	}
	second := loadNodeSyncState(t, node.ID)
	if second.DesiredGeneration != first.DesiredGeneration+1 {
		t.Fatalf("desired=%d, want %d", second.DesiredGeneration, first.DesiredGeneration+1)
	}
}

func TestReconcileNodeStaysPendingWhenDesiredAdvancesDuringApply(t *testing.T) {
	node := setupNodeSyncFixture(t)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return MarkNodesDirty(tx, node.ID)
	}); err != nil {
		t.Fatal(err)
	}
	applier := &fakeNodeApplier{}
	applier.apply = func() {
		// A concurrent mutation lands while the runtime apply is in flight.
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			return MarkNodesDirty(tx, node.ID)
		}); err != nil {
			t.Errorf("mark dirty during apply: %v", err)
		}
	}
	setNodeConfigApplierForTest(t, applier)
	res := ReconcileNode(context.Background(), node.ID)
	if res.State != SyncPending {
		t.Fatalf("state=%s", res.State)
	}
	if res.AppliedGeneration >= res.DesiredGeneration {
		t.Fatalf("applied=%d desired=%d", res.AppliedGeneration, res.DesiredGeneration)
	}
	// The next round without new mutations converges.
	applier.apply = nil
	final := ReconcileNode(context.Background(), node.ID)
	if final.State != SyncApplied {
		t.Fatalf("final state=%s", final.State)
	}
}

func TestReconcileNodeDropsOrphanSyncState(t *testing.T) {
	node := setupNodeSyncFixture(t)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return MarkNodesDirty(tx, node.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Delete(&model.Node{}, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	setNodeConfigApplierForTest(t, &fakeNodeApplier{})
	res := ReconcileNode(context.Background(), node.ID)
	if res.State != SyncFailed {
		t.Fatalf("state=%s", res.State)
	}
	var count int64
	if err := model.DB.Model(&model.NodeSyncState{}).Where("node_id = ?", node.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("orphan sync state rows=%d, want 0", count)
	}
}

func TestMigrationPreCreatesPendingSyncStateForExistingNodes(t *testing.T) {
	node := setupNodeSyncFixture(t)
	// Re-open the same database: migration must backfill sync rows for nodes
	// created before the sync-state table existed.
	if err := model.DB.Where("node_id = ?", node.ID).Delete(&model.NodeSyncState{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Init(model.DatabasePath()); err != nil {
		t.Fatalf("re-init db: %v", err)
	}
	state := loadNodeSyncState(t, node.ID)
	if state.State != SyncPending || state.DesiredGeneration != 1 || state.AppliedGeneration != 0 {
		t.Fatalf("state=%+v", state)
	}
}
