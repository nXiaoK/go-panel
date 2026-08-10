package service

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestNormalizeNodeSagaLockIDsSortsDeduplicatesAndDropsInvalid(t *testing.T) {
	got := normalizeNodeSagaLockIDs([]int64{9, 2, 9, 0, -3, 4, 2})
	want := []int64{2, 4, 9}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeNodeSagaLockIDs() = %v, want %v", got, want)
	}
}

func TestNodeSagaLockSetDeduplicatesAndCleansReferences(t *testing.T) {
	locks := newNodeSagaLockSet()
	unlock := locks.lock([]int64{7, 7, 3, 0, -1})

	assertNodeSagaLockRefs(t, locks, map[int64]int{3: 1, 7: 1})
	unlock()
	// Unlock is deliberately safe to call more than once. Besides making saga
	// cleanup easier to compose, this proves reference counts cannot underflow.
	unlock()
	assertNodeSagaLockRefs(t, locks, map[int64]int{})
}

func TestNodeSagaLockSetSerializesSameNodeAndRetainsWaitingReference(t *testing.T) {
	locks := newNodeSagaLockSet()
	unlockFirst := locks.lock([]int64{11})

	started := make(chan struct{})
	acquired := make(chan func(), 1)
	go func() {
		close(started)
		acquired <- locks.lock([]int64{11})
	}()
	<-started
	waitForNodeSagaLockRefs(t, locks, map[int64]int{11: 2})

	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("second saga acquired the same node before the first released it")
	default:
	}

	unlockFirst()
	var unlockSecond func()
	select {
	case unlockSecond = <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second saga did not acquire the node after the first released it")
	}
	assertNodeSagaLockRefs(t, locks, map[int64]int{11: 1})
	unlockSecond()
	assertNodeSagaLockRefs(t, locks, map[int64]int{})
}

func TestNodeSagaLockSetAllowsDifferentNodesInParallel(t *testing.T) {
	locks := newNodeSagaLockSet()
	unlockFirst := locks.lock([]int64{21})
	defer unlockFirst()

	acquired := make(chan func(), 1)
	go func() {
		acquired <- locks.lock([]int64{22})
	}()

	select {
	case unlockSecond := <-acquired:
		unlockSecond()
	case <-time.After(time.Second):
		t.Fatal("independent node saga was blocked by another node")
	}
}

func TestNodeSagaLockSetReverseOrderDoesNotDeadlock(t *testing.T) {
	locks := newNodeSagaLockSet()
	start := make(chan struct{})
	done := make(chan struct{}, 2)

	for _, ids := range [][]int64{{31, 32}, {32, 31}} {
		ids := append([]int64(nil), ids...)
		go func() {
			<-start
			unlock := locks.lock(ids)
			unlock()
			done <- struct{}{}
		}()
	}
	close(start)

	for range 2 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("opposite node input order deadlocked")
		}
	}
	assertNodeSagaLockRefs(t, locks, map[int64]int{})
}

func TestNodeSagaLockSetConcurrentReferenceCleanup(t *testing.T) {
	locks := newNodeSagaLockSet()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				base := int64((j + offset) % 8)
				unlock := locks.lock([]int64{base + 1, (base+1)%8 + 1})
				unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	assertNodeSagaLockRefs(t, locks, map[int64]int{})
}

func assertNodeSagaLockRefs(t *testing.T, locks *nodeSagaLockSet, want map[int64]int) {
	t.Helper()
	locks.mu.Lock()
	defer locks.mu.Unlock()
	got := make(map[int64]int, len(locks.entries))
	for id, entry := range locks.entries {
		got[id] = entry.refs
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lock references = %v, want %v", got, want)
	}
}

func waitForNodeSagaLockRefs(t *testing.T, locks *nodeSagaLockSet, want map[int64]int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		locks.mu.Lock()
		got := make(map[int64]int, len(locks.entries))
		for id, entry := range locks.entries {
			got[id] = entry.refs
		}
		locks.mu.Unlock()
		if reflect.DeepEqual(got, want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assertNodeSagaLockRefs(t, locks, want)
}
