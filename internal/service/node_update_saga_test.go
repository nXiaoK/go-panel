package service

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestCompleteSerializesConcurrentUpdateNodeModeChange(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18500 dnat to 198.51.100.250:443 # handle 500"
	replaceStarted := make(chan struct{})
	releaseReplace := make(chan struct{})
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			close(replaceStarted)
			<-releaseReplace
			return ws.GostResult{Msg: gost.SuccessMsg}
		default:
			t.Fatalf("command=%q", command)
			return ws.GostResult{}
		}
	}
	port := 443
	completeDone := make(chan error, 1)
	go func() {
		_, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
			TunnelID: portTunnel.ID, InPort: 18500, OutPort: &port, TargetHost: "198.51.100.250", Protocol: "tcp", RawRule: raw,
		})
		completeDone <- err
	}()
	select {
	case <-replaceStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Complete did not reach Replace")
	}
	updateDone := make(chan result.R, 1)
	go func() {
		updateDone <- UpdateNode(dto.NodeUpdateDto{
			ID: inNode.ID, Name: inNode.Name, IP: inNode.IP, ServerIP: inNode.ServerIP,
			PortSta: inNode.PortSta, PortEnd: inNode.PortEnd, ForwardMode: forwardModeGost,
		})
	}()
	select {
	case res := <-updateDone:
		t.Fatalf("UpdateNode bypassed Complete node lock: %+v", res)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseReplace)
	if err := <-completeDone; err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	res := <-updateDone
	if res.Code == 0 || !strings.Contains(res.Msg, "转发引用") {
		t.Fatalf("concurrent UpdateNode=%+v", res)
	}
	var got model.Node
	if err := model.DB.First(&got, inNode.ID).Error; err != nil || normalizeForwardMode(got.ForwardMode) != forwardModeNftables {
		t.Fatalf("entry mode changed after Complete: node=%+v err=%v", got, err)
	}
}

func TestUpdateNodeRejectsForwardModeChangeWhileReferencedWithoutWrites(t *testing.T) {
	tests := []struct {
		name      string
		reference func(t *testing.T, node model.Node)
	}{
		{
			name: "entry",
			reference: func(t *testing.T, node model.Node) {
				other := createUpdateNodeTestNode(t, "entry-other", forwardModeGost)
				createUpdateNodeTestForward(t, node.ID, other.ID, nil)
			},
		},
		{
			name: "legacy exit",
			reference: func(t *testing.T, node model.Node) {
				other := createUpdateNodeTestNode(t, "exit-other", forwardModeGost)
				createUpdateNodeTestForward(t, other.ID, node.ID, nil)
			},
		},
		{
			name: "exit member",
			reference: func(t *testing.T, node model.Node) {
				entry := createUpdateNodeTestNode(t, "member-entry", forwardModeGost)
				legacyExit := createUpdateNodeTestNode(t, "member-legacy-exit", forwardModeGost)
				forward := createUpdateNodeTestForward(t, entry.ID, legacyExit.ID, nil)
				now := time.Now().UnixMilli()
				member := model.ForwardExitMember{
					ForwardID: forward.ID, OutNodeID: node.ID, OutPort: 12001,
					Weight: 1, Status: 1, Active: 1, CreatedTime: now, UpdatedTime: now,
				}
				if err := model.DB.Create(&member).Error; err != nil {
					t.Fatalf("create exit member: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initUpdateNodeTestDB(t)
			node := createUpdateNodeTestNode(t, "referenced", forwardModeGost)
			tt.reference(t, node)

			beforeNode := node
			var beforeTunnels []model.Tunnel
			if err := model.DB.Order("id ASC").Find(&beforeTunnels).Error; err != nil {
				t.Fatalf("read tunnels before update: %v", err)
			}

			res := UpdateNode(dto.NodeUpdateDto{
				ID: node.ID, Name: "must-not-write", IP: "198.51.100.10",
				ServerIP: "198.51.100.11", PortSta: 30000, PortEnd: 31000,
				ForwardMode: forwardModeNftables,
			})
			if res.Code == 0 || !strings.Contains(res.Msg, "转发") || !strings.Contains(res.Msg, "模式") {
				t.Fatalf("UpdateNode returned code=%d msg=%q, want referenced-mode rejection", res.Code, res.Msg)
			}

			var got model.Node
			if err := model.DB.First(&got, node.ID).Error; err != nil {
				t.Fatalf("reload node: %v", err)
			}
			if got.Name != beforeNode.Name || got.IP != beforeNode.IP || got.ServerIP != beforeNode.ServerIP ||
				got.PortSta != beforeNode.PortSta || got.PortEnd != beforeNode.PortEnd || got.ForwardMode != beforeNode.ForwardMode {
				t.Fatalf("rejected update changed node: before=%+v after=%+v", beforeNode, got)
			}
			var gotTunnels []model.Tunnel
			if err := model.DB.Order("id ASC").Find(&gotTunnels).Error; err != nil {
				t.Fatalf("read tunnels after update: %v", err)
			}
			if len(gotTunnels) != len(beforeTunnels) {
				t.Fatalf("tunnel count changed: before=%d after=%d", len(beforeTunnels), len(gotTunnels))
			}
			for i := range gotTunnels {
				if gotTunnels[i].InIP != beforeTunnels[i].InIP || gotTunnels[i].OutIP != beforeTunnels[i].OutIP {
					t.Fatalf("rejected update changed tunnel %d: before=%+v after=%+v", gotTunnels[i].ID, beforeTunnels[i], gotTunnels[i])
				}
			}
		})
	}
}

func TestUpdateNodeAllowsForwardModeChangeWhenUnreferenced(t *testing.T) {
	initUpdateNodeTestDB(t)
	node := createUpdateNodeTestNode(t, "idle", forwardModeGost)

	res := UpdateNode(dto.NodeUpdateDto{
		ID: node.ID, Name: "idle-updated", IP: "198.51.100.20", ServerIP: "198.51.100.21",
		PortSta: 30000, PortEnd: 31000, ForwardMode: forwardModeNftables,
	})
	if res.Code != 0 {
		t.Fatalf("UpdateNode returned code=%d msg=%q", res.Code, res.Msg)
	}
	var got model.Node
	if err := model.DB.First(&got, node.ID).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.ForwardMode != forwardModeNftables || got.IP != "198.51.100.20" || got.ServerIP != "198.51.100.21" || got.PortSta != 30000 || got.PortEnd != 31000 {
		t.Fatalf("node not fully updated: %+v", got)
	}
}

func TestUpdateNodeWaitsForNodeSagaLockAndUsesFreshSnapshot(t *testing.T) {
	initUpdateNodeTestDB(t)
	node := createUpdateNodeTestNode(t, "before-lock", forwardModeGost)

	unlock := lockNftSagaNodes([]int64{node.ID})
	defer unlock()
	done := make(chan struct{})
	var resCode int
	go func() {
		res := UpdateNode(dto.NodeUpdateDto{
			ID: node.ID, Name: "updated", IP: node.IP, ServerIP: node.ServerIP,
			PortSta: node.PortSta, PortEnd: node.PortEnd, ForwardMode: forwardModeNftables,
		})
		resCode = res.Code
		close(done)
	}()

	waitForNodeSagaLockRefs(t, nftSagaNodeLocks, map[int64]int{node.ID: 2})
	select {
	case <-done:
		t.Fatal("UpdateNode did not wait for the node saga lock")
	default:
	}

	// This reference is committed while the update is waiting. A pre-lock node
	// snapshot/reference decision would incorrectly allow the mode transition.
	other := createUpdateNodeTestNode(t, "created-while-waiting", forwardModeGost)
	createUpdateNodeTestForward(t, node.ID, other.ID, nil)
	unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateNode did not finish after releasing the saga lock")
	}
	if resCode == 0 {
		t.Fatal("UpdateNode used stale pre-lock state and allowed a referenced mode change")
	}
	var got model.Node
	if err := model.DB.First(&got, node.ID).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if got.ForwardMode != forwardModeGost || got.Name != node.Name {
		t.Fatalf("rejected update changed node: %+v", got)
	}
}

func TestUpdateNodeReleasesSagaLockBeforeResync(t *testing.T) {
	initUpdateNodeTestDB(t)
	node := createUpdateNodeTestNode(t, "resync", forwardModeGost)

	done := make(chan struct{})
	go func() {
		defer close(done)
		res := UpdateNode(dto.NodeUpdateDto{
			ID: node.ID, Name: node.Name, IP: "198.51.100.30", ServerIP: node.ServerIP,
			PortSta: node.PortSta, PortEnd: node.PortEnd, ForwardMode: node.ForwardMode,
		})
		if res.Code != 0 {
			t.Errorf("UpdateNode returned code=%d msg=%q", res.Code, res.Msg)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateNode deadlocked while resync reacquired the node saga lock")
	}
}

func TestUpdateNodeReturnsCommittedNftResyncFailure(t *testing.T) {
	initUpdateNodeTestDB(t)
	node := createUpdateNodeTestNode(t, "nft-resync-failure", forwardModeNftables)
	node.Status = 1
	if err := model.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("status", 1).Error; err != nil {
		t.Fatal(err)
	}
	original := sendNftRefreshMessage
	sendNftRefreshMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command != "ApplyNftRules" {
			t.Fatalf("command=%q", command)
		}
		return ws.GostResult{Msg: "injected resync failure"}
	}
	t.Cleanup(func() { sendNftRefreshMessage = original })

	res := UpdateNode(dto.NodeUpdateDto{
		ID: node.ID, Name: node.Name, IP: "198.51.100.44", ServerIP: node.ServerIP,
		PortSta: node.PortSta, PortEnd: node.PortEnd, ForwardMode: node.ForwardMode,
	})
	if res.Code == 0 || !strings.Contains(res.Msg, "同步") {
		t.Fatalf("UpdateNode hid committed resync failure: %+v", res)
	}
	var got model.Node
	if err := model.DB.First(&got, node.ID).Error; err != nil || got.IP != "198.51.100.44" {
		t.Fatalf("committed node update=(%+v,%v)", got, err)
	}
}

func initUpdateNodeTestDB(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { model.Close() })
}

func createUpdateNodeTestNode(t *testing.T, name, mode string) model.Node {
	t.Helper()
	now := time.Now().UnixMilli()
	node := model.Node{
		Name: name, Secret: name + "-secret", IP: "192.0.2.10", ServerIP: "192.0.2.11",
		PortSta: 10000, PortEnd: 20000, ForwardMode: mode, CreatedTime: now, Status: 0,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func createUpdateNodeTestForward(t *testing.T, inNodeID, outNodeID int64, outPort *int) model.Forward {
	t.Helper()
	now := time.Now().UnixMilli()
	tunnel := model.Tunnel{
		Name: "update-node-reference", InNodeID: inNodeID, InIP: "192.0.2.10",
		OutNodeID: outNodeID, OutIP: "192.0.2.11", Type: tunnelTypeTunnelForward,
		Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	forward := model.Forward{
		UserID: 1, UserName: "test", Name: "update-node-reference", TunnelID: tunnel.ID,
		InPort: 11001, OutPort: outPort, RemoteAddr: "203.0.113.10:443",
		CreatedTime: now, UpdatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	return forward
}
