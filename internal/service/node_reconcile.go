package service

import (
	"context"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/model"
)

// Node synchronization states persisted in model.NodeSyncState.State.
const (
	SyncPending = "pending"
	SyncApplied = "applied"
	SyncFailed  = "failed"
)

const nodeSyncLastErrorLimit = 1000

// NodeSyncResult is the observable outcome of one reconciliation attempt.
type NodeSyncResult struct {
	NodeID            int64  `json:"nodeId"`
	State             string `json:"state"`
	DesiredGeneration int64  `json:"desiredGeneration"`
	AppliedGeneration int64  `json:"appliedGeneration"`
	LastError         string `json:"lastError"`
}

// Applied reports whether the node runtime matches the desired database state.
func (r NodeSyncResult) Applied() bool {
	return r.State == SyncApplied && r.AppliedGeneration >= r.DesiredGeneration
}

// NodeConfigApplier replays a node's complete desired configuration. The live
// implementation talks to the node over the gost/nft protocol; tests inject a
// fake to exercise offline/reconnect transitions deterministically.
type NodeConfigApplier interface {
	ApplyNodeConfig(ctx context.Context, node model.Node) error
}

type liveNodeConfigApplier struct{}

func (liveNodeConfigApplier) ApplyNodeConfig(_ context.Context, node model.Node) error {
	return applyNodeRuntimeChecked(node.ID)
}

var (
	nodeConfigApplierMu sync.RWMutex
	nodeConfigApplier   NodeConfigApplier = liveNodeConfigApplier{}
)

func currentNodeConfigApplier() NodeConfigApplier {
	nodeConfigApplierMu.RLock()
	defer nodeConfigApplierMu.RUnlock()
	return nodeConfigApplier
}

// swapNodeConfigApplier installs a new applier and returns the previous one so
// callers (tests) can restore it. Kept in production code so the test file does
// not need to reach into unexported state directly.
func swapNodeConfigApplier(applier NodeConfigApplier) NodeConfigApplier {
	nodeConfigApplierMu.Lock()
	defer nodeConfigApplierMu.Unlock()
	prev := nodeConfigApplier
	if applier == nil {
		applier = liveNodeConfigApplier{}
	}
	nodeConfigApplier = applier
	return prev
}

// MarkNodesDirty upserts a pending sync row for every positive node ID and
// increments its desired generation inside the caller's transaction, so the
// quota/port/business write and the dirty mark commit atomically.
func MarkNodesDirty(tx *gorm.DB, nodeIDs ...int64) error {
	if tx == nil {
		tx = model.DB
	}
	seen := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			continue
		}
		if _, dup := seen[nodeID]; dup {
			continue
		}
		seen[nodeID] = struct{}{}

		res := tx.Model(&model.NodeSyncState{}).
			Where("node_id = ?", nodeID).
			Updates(map[string]interface{}{
				"desired_generation": gorm.Expr("desired_generation + 1"),
				"state":              SyncPending,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			continue
		}
		// No row yet: create one that is already dirty (desired 1 > applied 0).
		created := model.NodeSyncState{
			NodeID:            nodeID,
			DesiredGeneration: 1,
			AppliedGeneration: 0,
			State:             SyncPending,
		}
		if err := tx.Create(&created).Error; err != nil {
			// A concurrent create won the race; fall back to the increment path.
			res = tx.Model(&model.NodeSyncState{}).
				Where("node_id = ?", nodeID).
				Updates(map[string]interface{}{
					"desired_generation": gorm.Expr("desired_generation + 1"),
					"state":              SyncPending,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return err
			}
		}
	}
	return nil
}

// ReconcileNode serializes on the node saga lock, replays complete desired
// configuration through the applier, and advances the applied generation only
// when the node acknowledged. A blocked lock waits; use tryReconcileNode for
// best-effort background retries.
func ReconcileNode(ctx context.Context, nodeID int64) NodeSyncResult {
	unlock := lockNftSagaNodes([]int64{nodeID})
	defer unlock()
	return reconcileNodeLocked(ctx, nodeID)
}

// tryReconcileNode reconciles only when the node saga lock is free, returning
// ok=false when an active saga still holds the node.
func tryReconcileNode(ctx context.Context, nodeID int64) (NodeSyncResult, bool) {
	unlock, ok := tryLockNftSagaNodes([]int64{nodeID})
	if !ok {
		return NodeSyncResult{}, false
	}
	defer unlock()
	return reconcileNodeLocked(ctx, nodeID), true
}

func reconcileNodeLocked(ctx context.Context, nodeID int64) NodeSyncResult {
	state := loadOrCreateNodeSyncState(nodeID)
	target := state.DesiredGeneration
	attempt := time.Now().UnixMilli()

	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		// Node was removed: drop its sync row and report failure without churn.
		_ = model.DB.Where("node_id = ?", nodeID).Delete(&model.NodeSyncState{}).Error
		return NodeSyncResult{NodeID: nodeID, State: SyncFailed, DesiredGeneration: target, AppliedGeneration: state.AppliedGeneration, LastError: "节点不存在"}
	}

	applyErr := currentNodeConfigApplier().ApplyNodeConfig(ctx, node)
	if applyErr != nil {
		return persistNodeSyncFailure(nodeID, target, state.AppliedGeneration, attempt, applyErr.Error())
	}
	return persistNodeSyncSuccess(nodeID, target, attempt)
}

func loadOrCreateNodeSyncState(nodeID int64) model.NodeSyncState {
	var state model.NodeSyncState
	if err := model.DB.Where("node_id = ?", nodeID).First(&state).Error; err == nil {
		return state
	}
	state = model.NodeSyncState{NodeID: nodeID, DesiredGeneration: 1, AppliedGeneration: 0, State: SyncPending}
	_ = model.DB.Create(&state).Error
	return state
}

func persistNodeSyncFailure(nodeID, desired, applied, attempt int64, message string) NodeSyncResult {
	message = truncateRunes(message, nodeSyncLastErrorLimit)
	_ = model.DB.Model(&model.NodeSyncState{}).Where("node_id = ?", nodeID).Updates(map[string]interface{}{
		"state":             SyncFailed,
		"last_error":        message,
		"last_attempt_time": attempt,
	}).Error
	return NodeSyncResult{NodeID: nodeID, State: SyncFailed, DesiredGeneration: desired, AppliedGeneration: applied, LastError: message}
}

func persistNodeSyncSuccess(nodeID, target, attempt int64) NodeSyncResult {
	// Re-read desired generation: a mutation may have advanced it while the
	// runtime apply was in flight, in which case the node stays pending.
	var latest model.NodeSyncState
	if err := model.DB.Where("node_id = ?", nodeID).First(&latest).Error; err != nil {
		return NodeSyncResult{NodeID: nodeID, State: SyncApplied, DesiredGeneration: target, AppliedGeneration: target}
	}
	nextState := SyncApplied
	if latest.DesiredGeneration > target {
		nextState = SyncPending
	}
	_ = model.DB.Model(&model.NodeSyncState{}).Where("node_id = ?", nodeID).Updates(map[string]interface{}{
		"applied_generation": target,
		"state":              nextState,
		"last_error":         "",
		"last_attempt_time":  attempt,
	}).Error
	return NodeSyncResult{NodeID: nodeID, State: nextState, DesiredGeneration: latest.DesiredGeneration, AppliedGeneration: target}
}

// ReconcilePendingNodes attempts one best-effort reconciliation for every node
// whose applied generation trails desired or whose last attempt failed. Nodes
// held by an active saga are skipped and retried on the next tick.
func ReconcilePendingNodes(ctx context.Context) {
	var states []model.NodeSyncState
	if err := model.DB.
		Where("applied_generation < desired_generation OR state <> ?", SyncApplied).
		Find(&states).Error; err != nil {
		log.Printf("加载待同步节点失败: %v", err)
		return
	}
	for _, state := range states {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res, ok := tryReconcileNode(ctx, state.NodeID)
		if !ok {
			continue
		}
		if res.State == SyncFailed {
			log.Printf("节点 %d 后台对账失败(desired=%d applied=%d): %s", state.NodeID, res.DesiredGeneration, res.AppliedGeneration, res.LastError)
		}
	}
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
