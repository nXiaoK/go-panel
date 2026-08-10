package ws

import "sync"

var (
	nodeConnectedHookMu sync.RWMutex
	nodeConnectedHook   func(nodeID int64)
)

// SetNodeConnectedHook registers a callback invoked after a node is marked online.
func SetNodeConnectedHook(hook func(nodeID int64)) {
	nodeConnectedHookMu.Lock()
	defer nodeConnectedHookMu.Unlock()
	nodeConnectedHook = hook
}

func runNodeConnectedHook(nodeID int64) {
	nodeConnectedHookMu.RLock()
	hook := nodeConnectedHook
	nodeConnectedHookMu.RUnlock()
	if hook == nil {
		return
	}
	safeGo("node-connected-hook", func() {
		hook(nodeID)
	})
}
