package service

import (
	"sort"
	"sync"
)

// nodeSagaLock serializes external desired-state sagas that mutate one node.
// refs includes both current holders and waiters so an entry cannot be removed
// and replaced while a goroutine is waiting on its mutex.
type nodeSagaLock struct {
	mu   sync.Mutex
	refs int
}

// nodeSagaLockSet provides independently keyed locks for node-scoped sagas.
// The map mutex protects only entry lifecycle; it is never held while waiting
// for an individual node lock.
type nodeSagaLockSet struct {
	mu      sync.Mutex
	entries map[int64]*nodeSagaLock
}

func newNodeSagaLockSet() *nodeSagaLockSet {
	return &nodeSagaLockSet{entries: make(map[int64]*nodeSagaLock)}
}

// nftSagaNodeLocks is shared by panel-side NFT desired-state sagas. Callers
// acquire every affected old/new node before reading or changing desired state.
var nftSagaNodeLocks = newNodeSagaLockSet()

func lockNftSagaNodes(nodeIDs []int64) func() {
	return nftSagaNodeLocks.lock(nodeIDs)
}

// tryLockNftSagaNodes acquires the saga locks without blocking. It returns
// (unlock, true) on success or (noop, false) when any node is already busy, so
// background reconciliation can skip nodes that an active saga still holds.
func tryLockNftSagaNodes(nodeIDs []int64) (func(), bool) {
	return nftSagaNodeLocks.tryLock(nodeIDs)
}

// lock acquires positive node IDs once each and in ascending order. Stable
// ordering prevents two overlapping multi-node sagas from deadlocking even
// when callers supply their node sets in opposite orders.
func (s *nodeSagaLockSet) lock(nodeIDs []int64) func() {
	ids := normalizeNodeSagaLockIDs(nodeIDs)
	if len(ids) == 0 {
		return func() {}
	}

	entries := make([]*nodeSagaLock, len(ids))
	s.mu.Lock()
	for i, nodeID := range ids {
		entry := s.entries[nodeID]
		if entry == nil {
			entry = &nodeSagaLock{}
			s.entries[nodeID] = entry
		}
		entry.refs++
		entries[i] = entry
	}
	s.mu.Unlock()

	for _, entry := range entries {
		entry.mu.Lock()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(entries) - 1; i >= 0; i-- {
				entries[i].mu.Unlock()
			}

			s.mu.Lock()
			defer s.mu.Unlock()
			for i, nodeID := range ids {
				entry := entries[i]
				entry.refs--
				if entry.refs == 0 {
					delete(s.entries, nodeID)
				}
			}
		})
	}
}

// tryLock mirrors lock but never waits: every node mutex is acquired with
// TryLock, and on the first busy node all progress (mutexes and refs) is
// rolled back. Ascending order keeps it deadlock-free against blocking lock.
func (s *nodeSagaLockSet) tryLock(nodeIDs []int64) (func(), bool) {
	ids := normalizeNodeSagaLockIDs(nodeIDs)
	if len(ids) == 0 {
		return func() {}, true
	}

	entries := make([]*nodeSagaLock, len(ids))
	s.mu.Lock()
	for i, nodeID := range ids {
		entry := s.entries[nodeID]
		if entry == nil {
			entry = &nodeSagaLock{}
			s.entries[nodeID] = entry
		}
		entry.refs++
		entries[i] = entry
	}
	s.mu.Unlock()

	release := func(lockedCount int) {
		for i := lockedCount - 1; i >= 0; i-- {
			entries[i].mu.Unlock()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, nodeID := range ids {
			entry := entries[i]
			entry.refs--
			if entry.refs == 0 {
				delete(s.entries, nodeID)
			}
		}
	}

	for i, entry := range entries {
		if !entry.mu.TryLock() {
			release(i)
			return func() {}, false
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() { release(len(entries)) })
	}, true
}

func normalizeNodeSagaLockIDs(nodeIDs []int64) []int64 {
	unique := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID > 0 {
			unique[nodeID] = struct{}{}
		}
	}

	ids := make([]int64, 0, len(unique))
	for nodeID := range unique {
		ids = append(ids, nodeID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
