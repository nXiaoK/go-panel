package model

import (
	"context"
	"sync"
)

// OperationGate coordinates in-flight database work against exclusive
// maintenance windows (database restore). It is the concurrency primitive that
// lets restore reject new mutable work, drain active work, and swap the handle
// without racing a live request.
//
//   - Enter registers one in-flight operation unless maintenance is active.
//   - BeginMaintenance blocks new Enter calls, waits for active work to drain,
//     and returns a closure that reopens the gate.
type OperationGate struct {
	mu          sync.Mutex
	cond        *sync.Cond
	maintenance bool
	active      int
}

// NewOperationGate returns a ready gate with no active work.
func NewOperationGate() *OperationGate {
	g := &OperationGate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// Gate is the process-wide database operation gate. HTTP, WebSocket, and
// scheduler entry points enter it around database work; restore drives it.
var Gate = NewOperationGate()

// Enter registers one in-flight operation. It returns (leave, true) on success
// or (noop, false) while maintenance is active or pending. leave is idempotent.
func (g *OperationGate) Enter() (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maintenance {
		return func() {}, false
	}
	g.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			if g.active == 0 {
				g.cond.Broadcast()
			}
			g.mu.Unlock()
		})
	}, true
}

// BeginMaintenance claims the exclusive maintenance window. It blocks new Enter
// calls immediately, then waits for active work to drain or ctx to cancel. On
// success it returns an idempotent end closure that reopens the gate; on
// cancellation it restores the prior state and returns ctx.Err().
func (g *OperationGate) BeginMaintenance(ctx context.Context) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Serialize against any other maintenance window first.
	for g.maintenance {
		if err := g.waitLocked(ctx); err != nil {
			return nil, err
		}
	}
	g.maintenance = true

	for g.active > 0 {
		if err := g.waitLocked(ctx); err != nil {
			// Abort: reopen the gate we just closed and wake any other waiter.
			g.maintenance = false
			g.cond.Broadcast()
			return nil, err
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.maintenance = false
			g.cond.Broadcast()
			g.mu.Unlock()
		})
	}, nil
}

// IsMaintenance reports whether a maintenance window is currently held.
func (g *OperationGate) IsMaintenance() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.maintenance
}

// waitLocked waits on the condition variable while honoring ctx cancellation.
// The caller must hold g.mu. sync.Cond has no context support, so a watcher
// goroutine broadcasts when ctx is done to wake the waiter.
func (g *OperationGate) waitLocked(ctx context.Context) error {
	if ctx == nil {
		g.cond.Wait()
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.cond.Broadcast()
			g.mu.Unlock()
		case <-stop:
		}
	}()
	g.cond.Wait()
	return ctx.Err()
}
