package service

import (
	"log"

	"github.com/nXiaoK/go-panel/internal/model"
)

// SyncMutationResult reports whether a mutation's affected nodes have fully
// applied the committed desired state. Pending is never presented as applied:
// a node whose runtime outcome is uncertain stays pending until the
// reconciler (reconnect hook or 30s retry) confirms convergence.
type SyncMutationResult struct {
	Applied   bool    `json:"applied"`
	Pending   bool    `json:"pending"`
	NodeIDs   []int64 `json:"nodeIds"`
	LastError string  `json:"lastError"`
}

// NodesSyncStatus inspects the sync state of the given nodes and reports
// applied only when every node's applied generation reached desired state.
func NodesSyncStatus(nodeIDs ...int64) SyncMutationResult {
	ids := normalizeNodeSagaLockIDs(nodeIDs)
	res := SyncMutationResult{Applied: true, NodeIDs: ids}
	if len(ids) == 0 {
		return res
	}
	var states []model.NodeSyncState
	if err := model.DB.Where("node_id IN ?", ids).Find(&states).Error; err != nil {
		return SyncMutationResult{Pending: true, NodeIDs: ids, LastError: err.Error()}
	}
	byNode := make(map[int64]model.NodeSyncState, len(states))
	for _, state := range states {
		byNode[state.NodeID] = state
	}
	for _, id := range ids {
		state, ok := byNode[id]
		if !ok {
			// No sync row means the node never diverged from desired state.
			continue
		}
		if state.State == SyncApplied && state.AppliedGeneration >= state.DesiredGeneration {
			continue
		}
		res.Applied = false
		res.Pending = true
		if res.LastError == "" && state.LastError != "" {
			res.LastError = state.LastError
		}
	}
	return res
}

// markNodesDirtyBestEffort flags nodes whose committed desired state could not
// be confirmed on the runtime (offline node, lost response). Reconciliation
// then converges them without waiting for the next reconnect. Failures only
// log: the reconnect hook remains the convergence backstop.
func markNodesDirtyBestEffort(nodeIDs ...int64) {
	if len(nodeIDs) == 0 {
		return
	}
	if err := MarkNodesDirty(model.DB, nodeIDs...); err != nil {
		log.Printf("标记节点待同步失败(nodes=%v): %v", nodeIDs, err)
	}
}
