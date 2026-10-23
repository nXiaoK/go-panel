package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func initNodeStatusTest(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
}

func createStatusNode(t *testing.T, name, mode string) model.Node {
	t.Helper()
	version := "1.0.0"
	node := model.Node{Name: name, Secret: name + "-secret", IP: "192.0.2.1", ServerIP: "192.0.2.1", PortSta: 10000, PortEnd: 20000, ForwardMode: mode, Version: &version, Status: 1}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	return node
}

func TestCheckNodeStatusOfflineNode(t *testing.T) {
	initNodeStatusTest(t)
	node := createStatusNode(t, "offline", "gost")
	var queries atomic.Int64
	callback := "test:offline-status-queries"
	if err := model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	defer model.DB.Callback().Query().Remove(callback)
	start := time.Now()
	res := CheckNodeStatus(node.ID)
	got, ok := res.Data.(dto.NodeStatusCheck)
	if res.Code != 0 || !ok || got.NodeID != node.ID || got.Connected || got.Responsive || got.RoundTripMs != 0 || got.Message != "节点未连接面板" || got.SyncState != "unknown" || got.DBStatus != 1 || got.CheckedAt < start.UnixMilli() {
		t.Fatalf("offline status=%+v, data=%+v", res, got)
	}
	if queries.Load() != 2 {
		t.Fatalf("offline check performed extra work: %d queries", queries.Load())
	}
	if time.Since(start) > time.Second {
		t.Fatal("offline check waited for a command")
	}
	if res := CheckNodeStatus(node.ID + 100); res.Code == 0 || res.Msg != "节点不存在" {
		t.Fatalf("missing node=%+v", res)
	}
}

func TestCheckNodeStatusRoundTrip(t *testing.T) {
	// 隔离已有的上线规则回放钩子；本测试只验证检查命令，回放由 node_reconcile 测试覆盖。
	ws.SetNodeConnectedHook(nil)
	defer ws.SetNodeConnectedHook(SyncNodeForwardsOnConnect)
	for _, mode := range []string{"gost", "nftables"} {
		for _, outcome := range []string{"response", "timeout", "disconnect"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				initNodeStatusTest(t)
				node := createStatusNode(t, "round-trip", mode)
				state := model.NodeSyncState{NodeID: node.ID, DesiredGeneration: 2, AppliedGeneration: 1, State: SyncFailed, LastError: "规则被拒绝", LastAttemptTime: 123}
				if err := model.DB.Create(&state).Error; err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); ws.HandleSystemInfo(w, r) }))
				defer server.Close()
				peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?type=1&secret="+node.Secret, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = peer.Close()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("websocket handler did not stop")
					}
				}()
				deadline := time.Now().Add(time.Second)
				for ws.Default.GetNode(node.ID) == nil && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if ws.Default.GetNode(node.ID) == nil {
					t.Fatal("session not registered")
				}
				responses := make(chan result.R, 1)
				checkDone := make(chan struct{})
				go func() { defer close(checkDone); responses <- checkNodeStatusWithTimeout(node.ID, 200*time.Millisecond) }()
				defer func() {
					select {
					case <-checkDone:
					case <-time.After(time.Second):
						t.Error("status goroutine did not stop")
					}
				}()
				_ = peer.SetReadDeadline(time.Now().Add(time.Second))
				_, raw, err := peer.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				var command struct {
					Type      string `json:"type"`
					RequestID string `json:"requestId"`
					Data      struct {
						IP      string `json:"ip"`
						Port    int    `json:"port"`
						Count   int    `json:"count"`
						Timeout int    `json:"timeout"`
					} `json:"data"`
				}
				if err := json.Unmarshal(crypto.DecryptIfNeeded(raw, node.Secret), &command); err != nil {
					t.Fatal(err)
				}
				if command.Type != "TcpPing" || command.Data.IP != "127.0.0.1" || command.Data.Port != 1 || command.Data.Count != 1 || command.Data.Timeout != 1000 {
					t.Fatalf("unsafe probe: %+v", command)
				}
				if outcome == "response" {
					// 命令本身或目标端口失败仍证明节点已回包，不应被当作节点无响应。
					if err := peer.WriteJSON(map[string]any{"requestId": command.RequestID, "type": "TcpPingResponse", "message": "连接被拒绝", "data": map[string]any{"success": false}}); err != nil {
						t.Fatal(err)
					}
				}
				if outcome == "disconnect" {
					_ = peer.Close()
				}
				select {
				case res := <-responses:
					got, ok := res.Data.(dto.NodeStatusCheck)
					if res.Code != 0 || !ok {
						t.Fatalf("check status failed: %+v", res)
					}
					if !got.Connected || got.Responsive != (outcome == "response") || got.RoundTripMs < 0 || got.SyncState != "failed" || got.SyncLastError != state.LastError || got.SyncLastAttempt != state.LastAttemptTime || got.ForwardMode != mode || got.LatestVersion != latestNodeVersion(mode) || !got.UpgradeAvailable {
						t.Fatalf("round-trip=%+v", got)
					}
					if outcome != "response" && got.RoundTripMs != 0 {
						t.Fatalf("timeout has round trip: %+v", got)
					}
				case <-time.After(time.Second):
					t.Fatal("status check exceeded injected timeout")
				}
			})
		}
	}
}

func TestGetAllNodesAttachesSyncState(t *testing.T) {
	initNodeStatusTest(t)
	cases := []struct {
		name             string
		desired, applied int64
		state, want      string
	}{
		{"synced", 2, 2, SyncApplied, "synced"}, {"failed", 2, 2, SyncFailed, "failed"}, {"pending", 3, 2, SyncPending, "pending"}, {"generation-reached", 2, 3, SyncPending, "synced"}, {"unknown", 0, 0, "", "unknown"},
	}
	for _, tc := range cases {
		node := createStatusNode(t, tc.name, "gost")
		if tc.state != "" {
			if err := model.DB.Create(&model.NodeSyncState{NodeID: node.ID, DesiredGeneration: tc.desired, AppliedGeneration: tc.applied, State: tc.state, LastError: "last error"}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	var queries int
	callback := "test:node-sync-summary"
	if err := model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, "node_sync_state") {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer model.DB.Callback().Query().Remove(callback)
	res := GetAllNodes()
	if res.Code != 0 {
		t.Fatalf("list=%+v", res)
	}
	nodes := res.Data.([]model.Node)
	if len(nodes) != len(cases) || queries != 1 {
		t.Fatalf("nodes=%d sync queries=%d", len(nodes), queries)
	}
	for i, tc := range cases {
		if nodes[i].SyncState != tc.want || nodes[i].Secret != "" || (tc.state != "" && nodes[i].SyncLastError != "last error") {
			t.Fatalf("node=%+v want=%s", nodes[i], tc.want)
		}
	}
}
