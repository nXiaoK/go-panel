package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestCheckedRefreshOutcomeUnknownKeepsDesiredStateAndReconnectConverges(t *testing.T) {
	cu, original, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	calls := 0
	sendNftRefreshMessage = func(_ int64, data interface{}, command string) ws.GostResult {
		calls++
		if command != "ApplyNftRules" {
			t.Fatalf("command=%q", command)
		}
		rules := strings.Join(data.(map[string]interface{})["rules"].([]string), "\n")
		if !strings.Contains(rules, "192.0.2.91") {
			t.Fatalf("desired rules missing update: %s", rules)
		}
		if calls == 1 {
			return ws.GostResult{Msg: "节点连接已替换", OutcomeUnknown: true}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID: original.ID, Name: "unknown-accepted", TunnelID: tunnel.ID,
		RemoteAddr: "192.0.2.91:443", Strategy: "fifo",
	})
	if res.Code != 0 {
		t.Fatalf("OutcomeUnknown rolled back update: %+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil || got.Name != "unknown-accepted" {
		t.Fatalf("desired state=(%+v,%v)", got, err)
	}
	if err := RefreshNodeForwardRulesChecked(tunnel.InNodeID); err != nil {
		t.Fatalf("reconnect convergence: %v", err)
	}
	if calls != 2 {
		t.Fatalf("refresh calls=%d, want uncertain send plus reconnect", calls)
	}
}

func TestCheckedRefreshSagaSerializesSameNodeButNotDifferentNodes(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	nodes := []model.Node{
		{Name: "lock-a", Secret: "lock-a", IP: "10.0.0.1", ServerIP: "10.0.0.1", ForwardMode: forwardModeNftables},
		{Name: "lock-b", Secret: "lock-b", IP: "10.0.0.2", ServerIP: "10.0.0.2", ForwardMode: forwardModeNftables},
	}
	if err := model.DB.Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	started := make(chan int64, 4)
	release := make(chan struct{}, 4)
	sendNftRefreshMessage = func(nodeID int64, _ interface{}, _ string) ws.GostResult {
		started <- nodeID
		<-release
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	done := make(chan error, 3)
	go func() { done <- RefreshNodeForwardRulesChecked(nodes[0].ID) }()
	if got := <-started; got != nodes[0].ID {
		t.Fatalf("first node=%d", got)
	}
	go func() { done <- RefreshNodeForwardRulesChecked(nodes[0].ID) }()
	select {
	case got := <-started:
		t.Fatalf("same-node refresh overlapped: node=%d", got)
	case <-time.After(50 * time.Millisecond):
	}
	go func() { done <- RefreshNodeForwardRulesChecked(nodes[1].ID) }()
	select {
	case got := <-started:
		if got != nodes[1].ID {
			t.Fatalf("parallel node=%d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("different-node refresh was globally serialized")
	}
	release <- struct{}{}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case got := <-started:
		if got != nodes[0].ID {
			t.Fatalf("second same node=%d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second same-node refresh never acquired lock")
	}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMixedReconnectSelectsOnlyGostSide(t *testing.T) {
	_, _, tunnel, forward := setupForwardWithExitMember(t)
	members := loadPersistedForwardExitMembers(forward.ID)
	if len(members) != 1 {
		t.Fatalf("members=%v", members)
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", tunnel.InNodeID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", members[0].OutNodeID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if got := gostExitSyncItems(members[0].OutNodeID); len(got) != 1 || got[0].forward.ID != forward.ID {
		t.Fatalf("NFT entry/Gost exit reconnect items=%v", got)
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", tunnel.InNodeID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Node{}).Where("id = ?", members[0].OutNodeID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	if got := gostExitSyncItems(members[0].OutNodeID); len(got) != 0 {
		t.Fatalf("Gost entry/NFT exit got Gost remote items=%v", got)
	}
	if got := gostEntrySyncItems(tunnel.InNodeID); len(got) != 1 || got[0].forward.ID != forward.ID {
		t.Fatalf("Gost entry reconnect items=%v", got)
	}
}

func TestReconnectSyncUsesSharedNodeSagaLock(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	node := model.Node{Name: "reconnect-lock", Secret: "reconnect-lock", ForwardMode: forwardModeGost}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	unlock := lockNftSagaNodes([]int64{node.ID})
	done := make(chan struct{})
	go func() { SyncNodeForwardsOnConnect(node.ID); close(done) }()
	select {
	case <-done:
		t.Fatal("reconnect sync bypassed node saga lock")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnect sync did not resume")
	}
}

func TestDeleteForwardRulesReturnsAgentLockContention(t *testing.T) {
	original := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = original })

	var commands []string
	sendNftIncrementalMessage = func(_ int64, data interface{}, command string) ws.GostResult {
		commands = append(commands, command)
		switch command {
		case "FindRuleHandles":
			return ws.GostResult{Msg: "OK", Data: json.RawMessage(`{"table":"flux_panel","handles":[{"chain":"forward","handle":44}]}`)}
		case "ListNftRules":
			return ws.GostResult{Msg: "fallback unavailable"}
		case "DeleteNftRules":
			payload, ok := data.(map[string]interface{})
			if !ok || payload["expectedTable"] != "flux_panel" {
				t.Fatalf("batch delete payload=%#v", data)
			}
			return ws.GostResult{Msg: nftgeneration.RetryableErrorPrefix + " reporter owns lock"}
		default:
			t.Fatalf("unexpected command %q", command)
			return ws.GostResult{}
		}
	}

	err := DeleteForwardRules(
		&model.Forward{ID: 7},
		&model.Node{ID: 9, Name: "node", ForwardMode: forwardModeNftables},
	)
	if !errors.Is(err, nftgeneration.ErrLocked) {
		t.Fatalf("DeleteForwardRules error=%v, want retryable ErrLocked", err)
	}
	if len(commands) != 3 || commands[2] != "DeleteNftRules" {
		t.Fatalf("commands=%v", commands)
	}
}

func TestUpdateAcceptsMixedRuntimeTopology(t *testing.T) {
	user, _, tunnel, original := setupForwardWithExitMember(t)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", tunnel.InNodeID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Forward{}).Where("id = ?", original.ID).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	res := UpdateForward(CurrentUser{UserID: user.ID, RoleID: userRoleID}, dto.ForwardUpdateDto{
		ID: original.ID, Name: "mixed-saved", TunnelID: tunnel.ID,
		RemoteAddr: "192.0.2.50:443", Strategy: "fifo",
	})
	if res.Code != 0 {
		t.Fatalf("mixed topology result=%+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil || got.Name != "mixed-saved" || got.RemoteAddr != "192.0.2.50:443" {
		t.Fatalf("mixed topology wrote forward: got=%+v err=%v", got, err)
	}
}

func TestUpdateAcceptsCrossRuntimeMode(t *testing.T) {
	cu, original, oldTunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
	now := time.Now().UnixMilli()
	gostNode := createForwardExitNode(t, "cross-gost", "10.99.0.2", 40000, 40100, forwardModeGost, now)
	protocol := "tcp"
	newTunnel := model.Tunnel{
		Name: "cross-mode", InNodeID: gostNode.ID, InIP: gostNode.IP,
		OutNodeID: gostNode.ID, OutIP: gostNode.ServerIP, Type: tunnelTypePortForward,
		Protocol: &protocol, Flow: 1, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		CreatedTime: now, UpdatedTime: now, Status: tunnelStatusActive,
	}
	if err := model.DB.Create(&newTunnel).Error; err != nil {
		t.Fatal(err)
	}
	var oldPermission model.UserTunnel
	if err := model.DB.Where("user_id = ? AND tunnel_id = ?", cu.UserID, oldTunnel.ID).First(&oldPermission).Error; err != nil {
		t.Fatal(err)
	}
	oldPermission.ID = 0
	oldPermission.TunnelID = newTunnel.ID
	if err := model.DB.Create(&oldPermission).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Forward{}).Where("id = ?", original.ID).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID: original.ID, Name: "cross-mode-write", TunnelID: newTunnel.ID,
		RemoteAddr: "192.0.2.80:443", Strategy: "fifo",
	})
	if res.Code != 0 {
		t.Fatalf("cross-mode result=%+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil || got.TunnelID != newTunnel.ID || got.Name != "cross-mode-write" {
		t.Fatalf("cross-mode update wrote desired state: got=%+v err=%v", got, err)
	}
}

func TestForwardAndExitMemberWritesRollbackInOneTransaction(t *testing.T) {
	_, _, tunnel, original := setupForwardWithExitMember(t)
	originalMembers := loadPersistedForwardExitMembers(original.ID)
	callback := "test:fail-exit-member-create"
	if err := model.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.ForwardExitMember{}).TableName()) {
			tx.AddError(errors.New("injected member create failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callback) })
	changed := original
	changed.Name = "partial-write"
	changed.InPort++
	memberReq := []dto.ForwardExitMemberDto{{OutNodeID: originalMembers[0].OutNodeID, Active: true, Weight: 1}}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if _, msg := saveForwardExitMembersWithTx(tx, &changed, &tunnel, memberReq, &changed.ID); msg != "" {
			return errors.New(msg)
		}
		return tx.Save(&changed).Error
	})
	if err == nil {
		t.Fatal("injected member failure committed transaction")
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil || got.Name != original.Name || got.InPort != original.InPort {
		t.Fatalf("forward partially written: got=%+v err=%v", got, err)
	}
	gotMembers := loadPersistedForwardExitMembers(original.ID)
	if len(gotMembers) != len(originalMembers) || gotMembers[0].ID != originalMembers[0].ID || gotMembers[0].OutPort != originalMembers[0].OutPort {
		t.Fatalf("members partially written: got=%+v want=%+v", gotMembers, originalMembers)
	}
}

func TestUpdateTransactionFailureKeepsOldPortsAndMembers(t *testing.T) {
	for _, phase := range []string{"member delete", "member create", "forward update"} {
		t.Run(phase, func(t *testing.T) {
			user, _, tunnel, original := setupForwardWithExitMember(t)
			originalMembers := loadPersistedForwardExitMembers(original.ID)
			if err := model.DB.Model(&model.Node{}).Where("id IN ?", []int64{tunnel.InNodeID, originalMembers[0].OutNodeID}).Update("forward_mode", forwardModeNftables).Error; err != nil {
				t.Fatal(err)
			}
			callback := "test:update-tx-" + strings.ReplaceAll(phase, " ", "-")
			inject := func(tx *gorm.DB) {
				table := gormStatementTable(tx)
				switch phase {
				case "member delete":
					if table == (model.ForwardExitMember{}).TableName() {
						tx.AddError(errors.New("injected member delete failure"))
					}
				case "member create":
					if table == (model.ForwardExitMember{}).TableName() {
						tx.AddError(errors.New("injected member create failure"))
					}
				case "forward update":
					if table == (model.Forward{}).TableName() {
						tx.AddError(errors.New("injected forward update failure"))
					}
				}
			}
			var remove func() error
			switch phase {
			case "member delete":
				if err := model.DB.Callback().Delete().Before("gorm:delete").Register(callback, inject); err != nil {
					t.Fatal(err)
				}
				remove = func() error { return model.DB.Callback().Delete().Remove(callback) }
			case "member create":
				if err := model.DB.Callback().Create().Before("gorm:create").Register(callback, inject); err != nil {
					t.Fatal(err)
				}
				remove = func() error { return model.DB.Callback().Create().Remove(callback) }
			case "forward update":
				if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, inject); err != nil {
					t.Fatal(err)
				}
				remove = func() error { return model.DB.Callback().Update().Remove(callback) }
			}
			t.Cleanup(func() { _ = remove() })
			newPort := original.InPort + 1
			res := UpdateForward(CurrentUser{UserID: user.ID, RoleID: userRoleID}, dto.ForwardUpdateDto{
				ID: original.ID, Name: "tx-must-rollback", TunnelID: tunnel.ID,
				RemoteAddr: "192.0.2.60:443", Strategy: "fifo", InPort: &newPort,
			})
			if res.Code == 0 {
				t.Fatalf("UpdateForward succeeded at %s", phase)
			}
			var got model.Forward
			if err := model.DB.First(&got, original.ID).Error; err != nil || got.Name != original.Name || got.InPort != original.InPort || (got.OutPort == nil) != (original.OutPort == nil) || got.OutPort != nil && *got.OutPort != *original.OutPort {
				t.Fatalf("%s partially wrote forward: got=%+v err=%v", phase, got, err)
			}
			members := loadPersistedForwardExitMembers(original.ID)
			if len(members) != len(originalMembers) || members[0].ID != originalMembers[0].ID || members[0].OutPort != originalMembers[0].OutPort {
				t.Fatalf("%s partially wrote members: got=%+v want=%+v", phase, members, originalMembers)
			}
		})
	}
}

func TestForceSwitchFlushFailureRestoresSnapshot(t *testing.T) {
	cu, original, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
	originalRefresh, originalSend := sendNftRefreshMessage, sendNodeMessage
	t.Cleanup(func() { sendNftRefreshMessage, sendNodeMessage = originalRefresh, originalSend })
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult { return ws.GostResult{Msg: gost.SuccessMsg} }
	sendNodeMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command != "FlushConntrack" {
			t.Fatalf("unexpected command %q", command)
		}
		return ws.GostResult{Msg: "injected flush failure"}
	}
	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID: original.ID, Name: "force-new", TunnelID: tunnel.ID,
		RemoteAddr: "192.168.1.13:8080", Strategy: "fifo", ForceSwitchTarget: true,
	})
	if res.Code == 0 {
		t.Fatalf("force switch unexpectedly succeeded: %+v", res)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil || got.Name != original.Name || got.RemoteAddr != original.RemoteAddr {
		t.Fatalf("flush failure did not restore snapshot: got=%+v err=%v", got, err)
	}
}

func TestCompleteForwardFromNftReplaceFailureRemovesCleanDesiredState(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	calls := 0
	raw := "add rule inet flux_panel prerouting tcp dport 15555 dnat to 198.51.100.55:443 # handle 71"
	sendNftIncrementalMessage = func(_ int64, data interface{}, command string) ws.GostResult {
		calls++
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `","add rule inet flux_panel forward counter accept # handle 99"]}`)}
		case "ReplaceNftRules":
			payload := data.(map[string]interface{})
			handles := payload["deleteHandles"].([]RuleHandle)
			if len(handles) != 1 || handles[0].Handle != 71 {
				t.Fatalf("replace handles=%v", handles)
			}
			if payload["expectedTable"] != "flux_panel" {
				t.Fatalf("replace table=%v", payload["expectedTable"])
			}
			return ws.GostResult{Msg: "injected complete replace failure"}
		default:
			t.Fatalf("unexpected command %q", command)
			return ws.GostResult{}
		}
	}
	targetPort := 443
	id, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 15555, TargetHost: "198.51.100.55", OutPort: &targetPort, Protocol: "tcp", RawRule: raw,
	})
	if err == nil || id != 0 || calls != 2 {
		t.Fatalf("complete result=(%d,%v), calls=%d", id, err, calls)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("in_port = ?", 15555).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("complete left desired rows=%d err=%v", count, err)
	}
}

func TestCompleteForwardFromNftPreciselyReplacesSelectedRuleAndActivates(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	raw := "add rule inet flux_panel prerouting tcp dport 16666 dnat to 198.51.100.66:8443 # handle 81"
	unselected := "add rule inet flux_panel prerouting tcp dport 17777 dnat to 203.0.113.7:22 # handle 82"
	commands := []string{}
	sendNftIncrementalMessage = func(_ int64, data interface{}, command string) ws.GostResult {
		commands = append(commands, command)
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `","` + unselected + `"]}`)}
		case "ReplaceNftRules":
			payload := data.(map[string]interface{})
			handles := payload["deleteHandles"].([]RuleHandle)
			if len(handles) != 1 || handles[0].Handle != 81 {
				t.Fatalf("selected handles=%v", handles)
			}
			for _, addition := range payload["addRules"].([]string) {
				if strings.Contains(addition, "17777") {
					t.Fatalf("replacement touched unselected raw rule: %s", addition)
				}
			}
			return ws.GostResult{Msg: gost.SuccessMsg}
		default:
			t.Fatalf("unexpected command %q", command)
			return ws.GostResult{}
		}
	}
	targetPort := 8443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 16666, TargetHost: "198.51.100.66", OutPort: &targetPort, Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 {
		t.Fatalf("complete result=(%d,%v)", id, err)
	}
	if strings.Join(commands, ",") != "ListNftRules,ReplaceNftRules" {
		t.Fatalf("commands=%v", commands)
	}
	var forward model.Forward
	if err := model.DB.First(&forward, id).Error; err != nil || forward.Status != forwardStatusActive {
		t.Fatalf("completed forward=(%+v,%v), want active", forward, err)
	}
}

func TestCompleteReplaceOutcomeUnknownCommitsManagedDesiredState(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 16888 dnat to 198.51.100.68:443 # handle 88"
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command == "ListNftRules" {
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		}
		if command == "ReplaceNftRules" {
			return ws.GostResult{Msg: "节点连接已替换", OutcomeUnknown: true}
		}
		t.Fatalf("command=%q", command)
		return ws.GostResult{}
	}
	port := 443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 16888, TargetHost: "198.51.100.68", OutPort: &port, Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 {
		t.Fatalf("OutcomeUnknown complete=(%d,%v)", id, err)
	}
	var forward model.Forward
	if err := model.DB.First(&forward, id).Error; err != nil || forward.Status != forwardStatusActive {
		t.Fatalf("forward=(%+v,%v)", forward, err)
	}
}

func TestCompleteCanonicalizesRawRuleForSafeReverseReplacement(t *testing.T) {
	raw := "add rule inet flux_panel prerouting tcp dport 16999 dnat ip to 198.51.100.69:9443 # handle 89"
	got, err := canonicalCompleteOriginalRule(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 16999 dnat to 198.51.100.69:9443"
	if got != want {
		t.Fatalf("canonical raw=%q, want %q", got, want)
	}
	restricted := "add rule inet flux_panel prerouting ip saddr 192.0.2.0/24 tcp dport 16999 dnat to 198.51.100.69:9443 # handle 90"
	if _, err := canonicalCompleteOriginalRule(restricted); err == nil {
		t.Fatal("canonical compensation accepted a rule with extra source restriction")
	}
}

func TestCompleteUnknownThenDefiniteFailureRetainsActiveDesiredWithoutReverse(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	inRaw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 17100 dnat to 10.0.0.2:27100 # handle 101"
	outRaw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 27100 dnat to 198.51.100.100:443 # handle 102"
	replaceCalls := 0
	sendNftIncrementalMessage = func(nodeID int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			raw := inRaw
			if nodeID == outNode.ID {
				raw = outRaw
			}
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			replaceCalls++
			if nodeID == inNode.ID {
				return ws.GostResult{Msg: "节点连接已替换", OutcomeUnknown: true}
			}
			return ws.GostResult{Msg: "definite second-node failure"}
		default:
			t.Fatalf("uncertain saga attempted unsafe cleanup command %q", command)
			return ws.GostResult{}
		}
	}
	outPort, targetPort := 27100, 443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 17100, OutPort: &outPort, TargetPort: &targetPort,
		TargetHost: "198.51.100.100", Protocol: "tcp", RawRule: inRaw, OutRawRule: outRaw,
	})
	if err != nil || id == 0 || replaceCalls != 2 {
		t.Fatalf("uncertain complete=(%d,%v), replaceCalls=%d", id, err, replaceCalls)
	}
	var retained model.Forward
	if err := model.DB.Where("in_port = ?", 17100).First(&retained).Error; err != nil || retained.Status != forwardStatusActive {
		t.Fatalf("retained uncertain forward=(%+v,%v)", retained, err)
	}
}

func TestCompleteRejectsNodeWidePortConflictsBeforeAgentMutation(t *testing.T) {
	inNode, outNode, portTunnel, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	occupiedIn, occupiedOut := 17001, 27001
	existing := model.Forward{Name: "occupied", TunnelID: tunnel.ID, InPort: occupiedIn, OutPort: &occupiedOut, Status: forwardStatusActive}
	if err := model.DB.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	member := model.ForwardExitMember{ForwardID: existing.ID, OutNodeID: outNode.ID, OutPort: occupiedOut, Active: 1, Weight: 1}
	if err := model.DB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		t.Fatalf("port conflict reached agent command %q", command)
		return ws.GostResult{}
	}
	port := 443
	conflictRaw := fmt.Sprintf("add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport %d dnat to 198.51.100.70:443 # handle 301", occupiedIn)
	if _, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: occupiedIn, TargetHost: "198.51.100.70", OutPort: &port, Protocol: "tcp", RawRule: conflictRaw,
	}); err == nil || !strings.Contains(err.Error(), "入口节点端口") {
		t.Fatalf("cross-tunnel input conflict err=%v", err)
	}
	tunnelInRaw := fmt.Sprintf("add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 17002 dnat to 10.0.0.2:%d # handle 302", occupiedOut)
	tunnelOutRaw := fmt.Sprintf("add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport %d dnat to 198.51.100.71:443 # handle 303", occupiedOut)
	if _, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 17002, TargetHost: "198.51.100.71", TargetPort: &port, OutPort: &occupiedOut,
		Protocol: "tcp", RawRule: tunnelInRaw, OutRawRule: tunnelOutRaw,
	}); err == nil || !strings.Contains(err.Error(), "出口节点端口") {
		t.Fatalf("member output conflict err=%v", err)
	}
}

func TestCompleteRequiresSelectedRawRuleForNftExit(t *testing.T) {
	inNode, _, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	outPort, targetPort := 27002, 443
	_, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 17003, TargetHost: "198.51.100.72", TargetPort: &targetPort, OutPort: &outPort,
		Protocol: "tcp", RawRule: "raw-in",
	})
	if err == nil || !strings.Contains(err.Error(), "NFT 出口规则") {
		t.Fatalf("missing OutRawRule err=%v", err)
	}
}

func TestCompleteRejectsPortsOutsideNodeRange(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	port := 443
	_, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: inNode.PortSta - 1, TargetHost: "198.51.100.73", OutPort: &port, Protocol: "tcp", RawRule: "raw",
	})
	if err == nil || !strings.Contains(err.Error(), "超出节点允许范围") {
		t.Fatalf("out-of-range err=%v", err)
	}
}

func TestCompleteSelectedRawMustExactlyMatchGeneratedDNAT(t *testing.T) {
	baseCandidate := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18080 dnat to 198.51.100.80:443 comment \"fp:1:2:3\""
	tests := []struct {
		name      string
		raw       string
		protocol  string
		candidate string
		wantErr   bool
	}{
		{name: "valid ipv4", raw: "add rule inet flux_panel prerouting tcp dport 18080 dnat to 198.51.100.80:443 # handle 1", protocol: "tcp", candidate: baseCandidate},
		{name: "valid ipv6", raw: "add rule inet flux_panel prerouting meta nfproto ipv6 udp dport 18081 dnat ip6 to [2001:db8::80]:53 # handle 2", protocol: "udp", candidate: "add rule inet flux_panel prerouting meta nfproto ipv6 udp dport 18081 dnat to [2001:db8::80]:53 comment \"fp:1:2:3\""},
		{name: "listen port mismatch", raw: "add rule inet flux_panel prerouting tcp dport 18081 dnat to 198.51.100.80:443 # handle 3", protocol: "tcp", candidate: baseCandidate, wantErr: true},
		{name: "dto protocol mismatch", raw: "add rule inet flux_panel prerouting tcp dport 18080 dnat to 198.51.100.80:443 # handle 4", protocol: "udp", candidate: baseCandidate, wantErr: true},
		{name: "raw protocol mismatch", raw: "add rule inet flux_panel prerouting udp dport 18080 dnat to 198.51.100.80:443 # handle 5", protocol: "tcp", candidate: baseCandidate, wantErr: true},
		{name: "target ip mismatch", raw: "add rule inet flux_panel prerouting tcp dport 18080 dnat to 198.51.100.81:443 # handle 6", protocol: "tcp", candidate: baseCandidate, wantErr: true},
		{name: "target port mismatch", raw: "add rule inet flux_panel prerouting tcp dport 18080 dnat to 198.51.100.80:444 # handle 7", protocol: "tcp", candidate: baseCandidate, wantErr: true},
		{name: "tunnel target mismatch", raw: "add rule inet flux_panel prerouting tcp dport 18080 dnat to 203.0.113.80:443 # handle 8", protocol: "tcp", candidate: baseCandidate, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateCompleteSelectedRule(tc.raw, []NftRuleToAdd{{Rule: tc.candidate}}, tc.protocol)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validate error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestCompleteRejectsMultiProtocolTunnelSelection(t *testing.T) {
	protocol := "tcp"
	tunnel := model.Tunnel{Protocol: &protocol, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0"}
	rule := CompleteForwardRule{Protocol: "tcp"}
	if err := validateCompleteProtocolSelection(&tunnel, &rule); err == nil {
		t.Fatal("multi-protocol Complete selection was accepted")
	}
}

func TestCompleteRejectsStaleRawBeforeAgentOrGostMutation(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	originalAdd := addCompleteGostRemoteService
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental; addCompleteGostRemoteService = originalAdd })
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		t.Fatalf("stale raw reached agent command %q", command)
		return ws.GostResult{}
	}
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		t.Fatal("stale raw reached Gost mutation")
		return ws.GostResult{}
	}
	createCalls := 0
	callback := "test:stale-complete-must-not-write"
	if err := model.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.Forward{}).TableName()) {
			createCalls++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callback) })
	port := 443
	staleRaw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18100 dnat to 198.51.100.200:444 # handle 200"
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 18100, OutPort: &port, TargetHost: "198.51.100.201", Protocol: "tcp", RawRule: staleRaw,
	})
	if err == nil || id != 0 {
		t.Fatalf("stale Complete=(%d,%v)", id, err)
	}
	if createCalls != 0 {
		t.Fatalf("stale raw caused %d Forward DB mutations", createCalls)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("in_port = ?", 18100).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale desired rows=%d err=%v", count, err)
	}
}

func TestCompleteNftEntryGostExitDeploysRemoteBeforeActivation(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", tunnel.ID).Update("udp_listen_addr", "").Error; err != nil {
		t.Fatal(err)
	}
	tunnel.UDPListenAddr = ""
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService = originalAdd
		deleteCompleteGostRemoteService = originalDelete
	})
	outPort, targetPort := 28080, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18080 dnat to 10.0.0.2:28080 # handle 180"
	sendNftIncrementalMessage = completeSingleNodeReplaceMock(t, inNode.ID, raw, ws.GostResult{Msg: gost.SuccessMsg})
	addCalls := 0
	addCompleteGostRemoteService = func(nodeID int64, _ string, gotPort int, remote, protocol, _, _ string) ws.GostResult {
		addCalls++
		if nodeID != outNode.ID || gotPort != outPort || remote != "198.51.100.180:443" || protocol != "tcp" {
			t.Fatalf("Gost deploy=(node=%d port=%d remote=%q protocol=%q)", nodeID, gotPort, remote, protocol)
		}
		var pending model.Forward
		if err := model.DB.Where("in_port = ?", 18080).First(&pending).Error; err != nil || pending.Status != forwardStatusActive {
			t.Fatalf("Gost deploy observed forward=(%+v,%v)", pending, err)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult {
		t.Fatal("successful Complete cleaned Gost remote")
		return ws.GostResult{}
	}
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18080, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.180", Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 || addCalls != 1 {
		t.Fatalf("Complete=(%d,%v) addCalls=%d", id, err, addCalls)
	}
}

func TestCompleteGostDefiniteFailureCleansAndReversesNft(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", tunnel.ID).Update("udp_listen_addr", "").Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService = originalAdd
		deleteCompleteGostRemoteService = originalDelete
	})
	outPort, targetPort := 28081, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18081 dnat to 10.0.0.2:28081 # handle 181"
	replaceCalls := 0
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			replaceCalls++
			return ws.GostResult{Msg: gost.SuccessMsg}
		case "FindRuleHandles":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","handles":[{"chain":"prerouting","handle":301}]}`)}
		default:
			t.Fatalf("command=%q", command)
			return ws.GostResult{}
		}
	}
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: "definite Gost failure"}
	}
	deleteCalls := 0
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult { deleteCalls++; return ws.GostResult{Msg: gost.SuccessMsg} }
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18081, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.181", Protocol: "tcp", RawRule: raw,
	})
	if err == nil || id != 0 || replaceCalls != 2 || deleteCalls == 0 {
		t.Fatalf("Complete=(%d,%v) replace=%d delete=%d", id, err, replaceCalls, deleteCalls)
	}
	var count int64
	_ = model.DB.Model(&model.Forward{}).Where("in_port = ?", 18081).Count(&count).Error
	if count != 0 {
		t.Fatalf("definite cleanup retained rows=%d", count)
	}
}

func TestCompleteNftUnknownThenGostFailureRetainsActivePending(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", tunnel.ID).Update("udp_listen_addr", "").Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService = originalAdd
		deleteCompleteGostRemoteService = originalDelete
	})
	outPort, targetPort := 28082, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18082 dnat to 10.0.0.2:28082 # handle 182"
	replaceCalls := 0
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command == "ListNftRules" {
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		}
		if command == "ReplaceNftRules" {
			replaceCalls++
			return ws.GostResult{Msg: "replace unknown", OutcomeUnknown: true}
		}
		t.Fatalf("unsafe compensation command=%q", command)
		return ws.GostResult{}
	}
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: "definite Gost failure"}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult {
		t.Fatal("unknown saga sent unsafe Gost cleanup")
		return ws.GostResult{}
	}
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18082, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.182", Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 || replaceCalls != 1 {
		t.Fatalf("Complete=(%d,%v) replace=%d", id, err, replaceCalls)
	}
	var retained model.Forward
	if err := model.DB.Where("in_port = ?", 18082).First(&retained).Error; err != nil || retained.Status != forwardStatusActive {
		t.Fatalf("retained=(%+v,%v)", retained, err)
	}
}

func TestCompleteGostOutcomeUnknownKeepsActiveAndReconnectConverges(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	originalSync := updateGostRemoteServiceCommand
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService, deleteCompleteGostRemoteService = originalAdd, originalDelete
		updateGostRemoteServiceCommand = originalSync
	})
	outPort, targetPort := 28083, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18083 dnat to 10.0.0.2:28083 # handle 183"
	sendNftIncrementalMessage = completeSingleNodeReplaceMock(t, inNode.ID, raw, ws.GostResult{Msg: gost.SuccessMsg})
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		var durable model.Forward
		if err := model.DB.Where("in_port = ?", 18083).First(&durable).Error; err != nil || durable.Status != forwardStatusActive {
			t.Fatalf("Gost Add did not observe active desired: forward=(%+v,%v)", durable, err)
		}
		return ws.GostResult{Msg: "old session response lost", OutcomeUnknown: true}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult {
		t.Fatal("Gost OutcomeUnknown sent delete on replacement session")
		return ws.GostResult{}
	}
	id, err := createForwardFromNft(CurrentUser{UserID: 1, RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18083, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.183", Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 {
		t.Fatalf("Complete=(%d,%v)", id, err)
	}
	syncCalls := 0
	updateGostRemoteServiceCommand = func(nodeID int64, _ string, port int, remote, protocol, _, _ string) ws.GostResult {
		syncCalls++
		if nodeID != outNode.ID || port != outPort || remote != "198.51.100.183:443" || protocol != "tcp" {
			t.Fatalf("reconnect desired=(node=%d port=%d remote=%q protocol=%q)", nodeID, port, remote, protocol)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	SyncNodeForwardsOnConnect(outNode.ID)
	if syncCalls != 1 {
		t.Fatalf("reconnect sync calls=%d", syncCalls)
	}
}

func TestCompleteGostCleanupOutcomeUnknownRestoresActivePending(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService, deleteCompleteGostRemoteService = originalAdd, originalDelete
	})
	outPort, targetPort := 28086, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18086 dnat to 10.0.0.2:28086 # handle 186"
	sendNftIncrementalMessage = completeSingleNodeReplaceMock(t, inNode.ID, raw, ws.GostResult{Msg: gost.SuccessMsg})
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		return ws.GostResult{Msg: "known Add failure"}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult {
		return ws.GostResult{Msg: "replacement session delete unknown", OutcomeUnknown: true}
	}
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18086, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.186", Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 {
		t.Fatalf("cleanup unknown Complete=(%d,%v)", id, err)
	}
	var retained model.Forward
	if err := model.DB.First(&retained, id).Error; err != nil || retained.Status != forwardStatusActive {
		t.Fatalf("retained=(%+v,%v)", retained, err)
	}
}

func TestCompletePauseTransitionFailureKeepsDurableActivePending(t *testing.T) {
	inNode, outNode, _, tunnel := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", outNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental := sendNftIncrementalMessage
	originalAdd, originalDelete := addCompleteGostRemoteService, deleteCompleteGostRemoteService
	t.Cleanup(func() {
		sendNftIncrementalMessage = originalIncremental
		addCompleteGostRemoteService, deleteCompleteGostRemoteService = originalAdd, originalDelete
	})
	outPort, targetPort := 28087, 443
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18087 dnat to 10.0.0.2:28087 # handle 187"
	sendNftIncrementalMessage = completeSingleNodeReplaceMock(t, inNode.ID, raw, ws.GostResult{Msg: gost.SuccessMsg})
	failPause := false
	callback := "test:fail-complete-pause-transition"
	if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if failPause && gormQueryTargetsTable(tx, (model.Forward{}).TableName()) {
			tx.AddError(errors.New("injected pause transition failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callback) })
	addCompleteGostRemoteService = func(_ int64, _ string, _ int, _, _, _, _ string) ws.GostResult {
		failPause = true
		return ws.GostResult{Msg: "known Add failure"}
	}
	deleteCompleteGostRemoteService = func(_ int64, _ string) ws.GostResult {
		t.Fatal("pause transition failure sent cleanup")
		return ws.GostResult{}
	}
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: tunnel.ID, InPort: 18087, OutPort: &outPort, TargetPort: &targetPort, TargetHost: "198.51.100.187", Protocol: "tcp", RawRule: raw,
	})
	if err != nil || id == 0 {
		t.Fatalf("pause failure Complete=(%d,%v)", id, err)
	}
	var retained model.Forward
	if err := model.DB.First(&retained, id).Error; err != nil || retained.Status != forwardStatusActive {
		t.Fatalf("retained=(%+v,%v)", retained, err)
	}
}

func TestCompleteUnknownActivationFailureRetainsRowWithoutCleanup(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	originalIncremental := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = originalIncremental })
	raw := "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18084 dnat to 198.51.100.184:443 # handle 184"
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		if command == "ListNftRules" {
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		}
		if command == "ReplaceNftRules" {
			return ws.GostResult{Msg: "replace unknown", OutcomeUnknown: true}
		}
		t.Fatalf("activation failure attempted unsafe cleanup command %q", command)
		return ws.GostResult{}
	}
	callback := "test:fail-complete-activation"
	if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if gormQueryTargetsTable(tx, (model.Forward{}).TableName()) {
			tx.AddError(errors.New("injected activation failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callback) })
	port := 443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 18084, OutPort: &port, TargetHost: "198.51.100.184", Protocol: "tcp", RawRule: raw,
	})
	if err == nil || id != 0 {
		t.Fatalf("activation failure Complete=(%d,%v)", id, err)
	}
	var retained model.Forward
	if err := model.DB.Where("in_port = ?", 18084).First(&retained).Error; err != nil {
		t.Fatalf("uncertain row was deleted: %v", err)
	}
}

func TestCompleteRechecksEntryIsNftInsideSaga(t *testing.T) {
	inNode, _, portTunnel, _ := setupPortDetectDB(t)
	t.Cleanup(func() { _ = model.Close() })
	if err := model.DB.Model(&model.Node{}).Where("id = ?", inNode.ID).Update("forward_mode", forwardModeGost).Error; err != nil {
		t.Fatal(err)
	}
	port := 443
	id, err := createForwardFromNft(CurrentUser{RoleID: adminRoleID}, inNode.ID, &CompleteForwardRule{
		TunnelID: portTunnel.ID, InPort: 18085, OutPort: &port, TargetHost: "198.51.100.185", Protocol: "tcp",
		RawRule: "add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 18085 dnat to 198.51.100.185:443 # handle 185",
	})
	if err == nil || id != 0 || !strings.Contains(err.Error(), "nftables") {
		t.Fatalf("non-NFT entry Complete=(%d,%v)", id, err)
	}
}

func completeSingleNodeReplaceMock(t *testing.T, nodeID int64, raw string, replaceResult ws.GostResult) func(int64, interface{}, string) ws.GostResult {
	t.Helper()
	return func(gotNodeID int64, _ interface{}, command string) ws.GostResult {
		if gotNodeID != nodeID {
			t.Fatalf("node=%d want=%d", gotNodeID, nodeID)
		}
		switch command {
		case "ListNftRules":
			return ws.GostResult{Msg: gost.SuccessMsg, Data: json.RawMessage(`{"table":"flux_panel","rules":["` + raw + `"]}`)}
		case "ReplaceNftRules":
			return replaceResult
		default:
			t.Fatalf("command=%q", command)
			return ws.GostResult{}
		}
	}
}

func TestPauseAndResumeUnsupportedRefreshObserveTargetStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start      int
		operation  func(CurrentUser, int64) result.R
		wantStatus int
		wantRules  bool
	}{
		{name: "pause", start: forwardStatusActive, operation: PauseForward, wantStatus: forwardStatusPaused},
		{name: "resume", start: forwardStatusPaused, operation: ResumeForward, wantStatus: forwardStatusActive, wantRules: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := setupFlowAuthTestDB(t, true)
			if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).Update("forward_mode", forwardModeNftables).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Model(&model.Forward{}).Where("id = ?", fixture.forward.ID).Update("status", tc.start).Error; err != nil {
				t.Fatal(err)
			}
			originalIncremental, originalRefresh := sendNftIncrementalMessage, sendNftRefreshMessage
			t.Cleanup(func() { sendNftIncrementalMessage, sendNftRefreshMessage = originalIncremental, originalRefresh })
			sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
				t.Fatalf("desired-state status path used incremental command %q", command)
				return ws.GostResult{}
			}
			refreshCalls := 0
			sendNftRefreshMessage = func(_ int64, data interface{}, command string) ws.GostResult {
				refreshCalls++
				if command != "ApplyNftRules" {
					t.Fatalf("refresh command=%q", command)
				}
				payload := data.(map[string]interface{})
				rules := payload["rules"].([]string)
				if (len(rules) > 0) != tc.wantRules {
					t.Fatalf("refresh saw %d rules, wantRules=%v", len(rules), tc.wantRules)
				}
				return ws.GostResult{Msg: gost.SuccessMsg}
			}

			res := tc.operation(CurrentUser{RoleID: adminRoleID}, fixture.forward.ID)
			if res.Code != 0 || refreshCalls != 1 {
				t.Fatalf("operation result=%+v refreshCalls=%d", res, refreshCalls)
			}
			var got model.Forward
			if err := model.DB.First(&got, fixture.forward.ID).Error; err != nil || got.Status != tc.wantStatus {
				t.Fatalf("persisted forward=(%+v,%v), want status %d", got, err, tc.wantStatus)
			}
		})
	}
}

func TestDeleteUnsupportedUsesPausedDesiredStateAndCheckedRefresh(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental, originalRefresh := sendNftIncrementalMessage, sendNftRefreshMessage
	t.Cleanup(func() { sendNftIncrementalMessage, sendNftRefreshMessage = originalIncremental, originalRefresh })
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		t.Fatalf("desired-state delete used incremental command %q", command)
		return ws.GostResult{}
	}
	sendNftRefreshMessage = func(_ int64, data interface{}, _ string) ws.GostResult {
		if rules := data.(map[string]interface{})["rules"].([]string); len(rules) != 0 {
			t.Fatalf("delete fallback refresh retained desired rules: %v", rules)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := DeleteForward(CurrentUser{RoleID: adminRoleID}, fixture.forward.ID)
	if res.Code != 0 {
		t.Fatalf("DeleteForward unsupported fallback: %+v", res)
	}
	if err := model.DB.First(&model.Forward{}, fixture.forward.ID).Error; err == nil {
		t.Fatal("forward row remains after successful checked delete fallback")
	}
}

func TestDeleteUnsupportedRefreshFailureRestoresActiveDesiredState(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	originalIncremental, originalRefresh := sendNftIncrementalMessage, sendNftRefreshMessage
	t.Cleanup(func() { sendNftIncrementalMessage, sendNftRefreshMessage = originalIncremental, originalRefresh })
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		t.Fatalf("desired-state delete rollback used incremental command %q", command)
		return ws.GostResult{}
	}
	refreshCalls := 0
	sendNftRefreshMessage = func(_ int64, data interface{}, _ string) ws.GostResult {
		refreshCalls++
		rules := data.(map[string]interface{})["rules"].([]string)
		if refreshCalls == 1 {
			if len(rules) != 0 {
				t.Fatalf("failed target refresh saw active rules: %v", rules)
			}
			return ws.GostResult{Msg: "injected refresh failure"}
		}
		if len(rules) == 0 {
			t.Fatal("compensation refresh did not see restored active desired state")
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := DeleteForward(CurrentUser{RoleID: adminRoleID}, fixture.forward.ID)
	if res.Code == 0 || refreshCalls != 2 {
		t.Fatalf("DeleteForward result=%+v refreshCalls=%d", res, refreshCalls)
	}
	var retained model.Forward
	if err := model.DB.First(&retained, fixture.forward.ID).Error; err != nil || retained.Status != forwardStatusActive {
		t.Fatalf("retained forward=(%+v,%v)", retained, err)
	}
}

func TestUpdateCheckedRefreshFailureRestoresCompleteForward(t *testing.T) {
	cu, original, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	calls := 0
	sendNftRefreshMessage = func(_ int64, data interface{}, _ string) ws.GostResult {
		calls++
		rules := strings.Join(data.(map[string]interface{})["rules"].([]string), "\n")
		if calls == 1 {
			if !strings.Contains(rules, "192.168.1.13") {
				t.Fatalf("update refresh did not see new desired rules: %s", rules)
			}
			return ws.GostResult{Msg: "injected update refresh failure"}
		}
		if !strings.Contains(rules, "192.168.1.10") {
			t.Fatalf("compensation refresh did not see old desired rules: %s", rules)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := UpdateForward(cu, dto.ForwardUpdateDto{
		ID: original.ID, Name: "changed", TunnelID: tunnel.ID,
		RemoteAddr: "192.168.1.13:8080", Strategy: "fifo",
	})
	if res.Code == 0 || calls != 2 {
		t.Fatalf("UpdateForward result=%+v refreshCalls=%d", res, calls)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != original.Name || got.RemoteAddr != original.RemoteAddr || got.InPort != original.InPort || got.Status != original.Status {
		t.Fatalf("forward snapshot not restored: got=%+v original=%+v", got, original)
	}
}

func TestResumeExitRefreshFailureKeepsPausedStatusAndCompensates(t *testing.T) {
	user, _, tunnel, forward := setupForwardWithExitMember(t)
	members := loadPersistedForwardExitMembers(forward.ID)
	if len(members) != 1 {
		t.Fatalf("members=%v", members)
	}
	if err := model.DB.Model(&model.Node{}).Where("id IN ?", []int64{tunnel.InNodeID, members[0].OutNodeID}).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Forward{}).Where("id = ?", forward.ID).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	refreshed := map[int64]bool{}
	failedExit := false
	sendNftRefreshMessage = func(nodeID int64, data interface{}, _ string) ws.GostResult {
		refreshed[nodeID] = true
		if nodeID == members[0].OutNodeID && !failedExit {
			failedExit = true
			return ws.GostResult{Msg: "injected exit refresh failure"}
		}
		rules := data.(map[string]interface{})["rules"].([]string)
		if failedExit && len(rules) != 0 {
			t.Fatalf("paused compensation on node %d retained rules: %v", nodeID, rules)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := ResumeForward(CurrentUser{UserID: user.ID, RoleID: userRoleID}, forward.ID)
	if res.Code == 0 || !failedExit || !refreshed[tunnel.InNodeID] || !refreshed[members[0].OutNodeID] {
		t.Fatalf("ResumeForward result=%+v refreshed=%v", res, refreshed)
	}
	var got model.Forward
	if err := model.DB.First(&got, forward.ID).Error; err != nil || got.Status != forwardStatusPaused {
		t.Fatalf("forward=(%+v,%v), want paused", got, err)
	}
}

func TestUpdateExitRefreshFailureRestoresPortsAndExitMembers(t *testing.T) {
	user, _, tunnel, original := setupForwardWithExitMember(t)
	originalMembers := loadPersistedForwardExitMembers(original.ID)
	if len(originalMembers) != 1 {
		t.Fatalf("members=%v", originalMembers)
	}
	if err := model.DB.Model(&model.Node{}).Where("id IN ?", []int64{tunnel.InNodeID, originalMembers[0].OutNodeID}).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	failedExit := false
	sendNftRefreshMessage = func(nodeID int64, _ interface{}, _ string) ws.GostResult {
		if nodeID == originalMembers[0].OutNodeID && !failedExit {
			failedExit = true
			return ws.GostResult{Msg: "injected exit refresh failure"}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	newInPort := original.InPort + 1
	res := UpdateForward(CurrentUser{UserID: user.ID, RoleID: userRoleID}, dto.ForwardUpdateDto{
		ID: original.ID, Name: "changed-exit", TunnelID: tunnel.ID,
		RemoteAddr: "192.168.10.20:8080", Strategy: "fifo", InPort: &newInPort,
	})
	if res.Code == 0 || !failedExit {
		t.Fatalf("UpdateForward result=%+v failedExit=%v", res, failedExit)
	}
	var got model.Forward
	if err := model.DB.First(&got, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != original.Name || got.RemoteAddr != original.RemoteAddr || got.InPort != original.InPort || got.Status != original.Status || (got.OutPort == nil) != (original.OutPort == nil) || got.OutPort != nil && *got.OutPort != *original.OutPort {
		t.Fatalf("forward snapshot not restored: got=%+v original=%+v", got, original)
	}
	gotMembers := loadPersistedForwardExitMembers(original.ID)
	if len(gotMembers) != len(originalMembers) || gotMembers[0].OutNodeID != originalMembers[0].OutNodeID || gotMembers[0].OutPort != originalMembers[0].OutPort || gotMembers[0].Active != originalMembers[0].Active {
		t.Fatalf("exit members not restored: got=%+v original=%+v", gotMembers, originalMembers)
	}
}

func TestCreateUnsupportedCheckedRefreshAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failRefresh bool
	}{
		{name: "success"},
		{name: "refresh failure rolls back", failRefresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cu, _, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
			originalIncremental, originalRefresh := sendNftIncrementalMessage, sendNftRefreshMessage
			t.Cleanup(func() { sendNftIncrementalMessage, sendNftRefreshMessage = originalIncremental, originalRefresh })
			sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
				t.Fatalf("desired-state create used incremental command %q", command)
				return ws.GostResult{}
			}
			refreshCalls := 0
			sendNftRefreshMessage = func(_ int64, data interface{}, _ string) ws.GostResult {
				refreshCalls++
				rules := strings.Join(data.(map[string]interface{})["rules"].([]string), "\n")
				if refreshCalls == 1 {
					if !strings.Contains(rules, "192.0.2.55") {
						t.Fatalf("create fallback refresh missed new desired rules: %s", rules)
					}
					if tc.failRefresh {
						return ws.GostResult{Msg: "injected create refresh failure"}
					}
				} else if strings.Contains(rules, "192.0.2.55") {
					t.Fatalf("create cleanup refresh retained rolled back rules: %s", rules)
				}
				return ws.GostResult{Msg: gost.SuccessMsg}
			}
			res := CreateForward(cu, dto.ForwardDto{
				Name: "create-handoff", TunnelID: tunnel.ID,
				RemoteAddr: "192.0.2.55:443", Strategy: "fifo",
			})
			if tc.failRefresh {
				if res.Code == 0 || refreshCalls != 2 {
					t.Fatalf("CreateForward result=%+v refreshCalls=%d", res, refreshCalls)
				}
				var count int64
				if err := model.DB.Model(&model.Forward{}).Where("name = ?", "create-handoff").Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("rolled back create rows=%d err=%v", count, err)
				}
			} else if res.Code != 0 || refreshCalls != 1 {
				t.Fatalf("CreateForward fallback result=%+v refreshCalls=%d", res, refreshCalls)
			}
		})
	}
}

func TestCreateRollbackFailureMarksErrorBeforeCompensationRefresh(t *testing.T) {
	cu, _, tunnel := setupForwardUpdateFlowScenario(t, forwardStatusActive, false)
	callback := "test:fail-create-rollback-delete"
	armed := false
	if err := model.DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if armed && gormQueryTargetsTable(tx, (model.ForwardExitMember{}).TableName()) {
			tx.AddError(errors.New("injected rollback delete failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.DB.Callback().Delete().Remove(callback) })
	originalRefresh := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = originalRefresh })
	calls := 0
	sendNftRefreshMessage = func(_ int64, data interface{}, _ string) ws.GostResult {
		calls++
		rules := strings.Join(data.(map[string]interface{})["rules"].([]string), "\n")
		if calls == 1 {
			armed = true
			return ws.GostResult{Msg: "injected create deployment failure"}
		}
		if strings.Contains(rules, "192.0.2.77") {
			t.Fatalf("compensation refreshed untrusted active desired state: %s", rules)
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := CreateForward(cu, dto.ForwardDto{
		Name: "rollback-error", TunnelID: tunnel.ID,
		RemoteAddr: "192.0.2.77:443", Strategy: "fifo",
	})
	if res.Code == 0 || calls != 2 {
		t.Fatalf("CreateForward result=%+v calls=%d", res, calls)
	}
	var retained model.Forward
	if err := model.DB.Where("name = ?", "rollback-error").First(&retained).Error; err != nil || retained.Status != forwardStatusError {
		t.Fatalf("rollback failure forward=(%+v,%v), want error desired", retained, err)
	}
}

func TestDeleteForwardRulesReturnsAnyHandleDeleteFailure(t *testing.T) {
	original := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = original })

	deleteCalls := 0
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "FindRuleHandles":
			return ws.GostResult{Msg: "OK", Data: json.RawMessage(`{"table":"flux_panel","handles":[{"chain":"forward","handle":41},{"chain":"forward","handle":42}]}`)}
		case "ListNftRules":
			return ws.GostResult{Msg: "fallback unavailable"}
		case "DeleteNftRules":
			deleteCalls++
			return ws.GostResult{Msg: "delete failed"}
		default:
			t.Fatalf("unexpected command %q", command)
			return ws.GostResult{}
		}
	}

	err := DeleteForwardRules(
		&model.Forward{ID: 7},
		&model.Node{ID: 9, Name: "node", ForwardMode: forwardModeNftables},
	)
	if err == nil || errors.Is(err, errNftIncrementalUnsupported) {
		t.Fatalf("DeleteForwardRules ordinary delete error=%v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete batch calls=%d, want one atomic failure", deleteCalls)
	}
}

func TestDeleteForwardLockContentionKeepsDatabaseRow(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, false)
	if err := model.DB.Model(&model.Node{}).Where("id = ?", fixture.nodeA.ID).
		Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatal(err)
	}

	original := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = original })
	sendNftIncrementalMessage = func(_ int64, _ interface{}, command string) ws.GostResult {
		switch command {
		case "FindRuleHandles":
			return ws.GostResult{Msg: "OK", Data: json.RawMessage(`{"table":"flux_panel","handles":[{"chain":"forward","handle":44}]}`)}
		case "ListNftRules":
			return ws.GostResult{Msg: "fallback unavailable"}
		case "DeleteNftRules":
			return ws.GostResult{Msg: nftgeneration.RetryableErrorPrefix + " reporter owns lock"}
		default:
			t.Fatalf("unexpected command %q", command)
			return ws.GostResult{}
		}
	}

	res := DeleteForward(CurrentUser{RoleID: adminRoleID}, fixture.forward.ID)
	if res.Code == 0 {
		t.Fatalf("DeleteForward succeeded during generation lock contention: %+v", res)
	}
	var retained model.Forward
	if err := model.DB.First(&retained, fixture.forward.ID).Error; err != nil {
		t.Fatalf("forward row was deleted after retryable failure: %v", err)
	}
}
