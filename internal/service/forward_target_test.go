package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

func TestForwardTargetManualUsesActiveTarget(t *testing.T) {
	forward := &model.Forward{
		RemoteAddr:       "10.0.0.10:80, 10.0.0.11:80",
		TargetMode:       targetModeManual,
		ActiveRemoteAddr: "10.0.0.11:80",
	}

	target, msg := normalizeForwardTargetConfig(forward.TargetMode, forward.RemoteAddr, forward.ActiveRemoteAddr)
	if msg != "" {
		t.Fatalf("unexpected validation error: %s", msg)
	}
	forward.TargetMode = target.Mode
	forward.ActiveRemoteAddr = target.ActiveAddr

	if got := effectiveForwardRemoteAddr(forward); got != "10.0.0.11:80" {
		t.Fatalf("expected active target only, got %q", got)
	}
}

func TestForwardTargetManualRejectsTargetOutsideList(t *testing.T) {
	_, msg := normalizeForwardTargetConfig(targetModeManual, "10.0.0.10:80,10.0.0.11:80", "10.0.0.12:80")
	if msg != "当前目标地址不在目标地址列表中" {
		t.Fatalf("unexpected error: %q", msg)
	}
}

func TestForwardTargetManualNftRulesUseActiveTarget(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	tunnel := &model.Tunnel{
		Type:          tunnelTypePortForward,
		TCPListenAddr: "0.0.0.0",
	}
	forward := &model.Forward{
		ID:               11,
		UserID:           22,
		InPort:           8080,
		RemoteAddr:       "10.0.0.10:80,10.0.0.11:80",
		TargetMode:       targetModeManual,
		ActiveRemoteAddr: "10.0.0.11:80",
	}

	rules, err := buildForwardNftRulesToAdd(forward, tunnel, &model.Node{ID: 1})
	if err != nil {
		t.Fatalf("build rules: %v", err)
	}
	joined := strings.Join(nftRulesToStrings(rules), "\n")
	if strings.Contains(joined, "10.0.0.10") {
		t.Fatalf("manual target rules should not include inactive target: %s", joined)
	}
	if !strings.Contains(joined, "10.0.0.11") {
		t.Fatalf("manual target rules should include active target: %s", joined)
	}
}

func TestForwardNftRulesRejectNonLiteralAndUnsafeTargets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	tunnel := &model.Tunnel{Type: tunnelTypePortForward, TCPListenAddr: "0.0.0.0"}
	inputs := []string{
		"example.com:80",
		"10.0.0.1;flush-ruleset:80",
		"10.0.0.1 #comment:80",
	}
	for _, input := range inputs {
		forward := &model.Forward{ID: 11, UserID: 22, InPort: 8080, RemoteAddr: input}
		rules, err := buildForwardNftRulesToAdd(forward, tunnel, &model.Node{ID: 1})
		if err != nil {
			t.Fatalf("build rules for %q: %v", input, err)
		}
		if len(rules) != 0 {
			t.Fatalf("nft target %q produced rules: %v", input, nftRulesToStrings(rules))
		}
	}
}

func TestCreateForwardRejectsInvalidNftTargetBeforeDatabaseWrite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        "nft-entry",
		Secret:      "secret-nft-entry",
		IP:          "192.0.2.10",
		ServerIP:    "192.0.2.10",
		PortSta:     10000,
		PortEnd:     20000,
		ForwardMode: forwardModeNftables,
		CreatedTime: now,
		Status:      nodeStatusOnline,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	tunnel := model.Tunnel{
		Name:          "nft-target-validation",
		InNodeID:      node.ID,
		InIP:          node.IP,
		OutNodeID:     node.ID,
		OutIP:         node.ServerIP,
		Type:          tunnelTypePortForward,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	res := CreateForward(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin"}, dto.ForwardDto{
		Name:       "blocked-target",
		TunnelID:   tunnel.ID,
		RemoteAddr: "192.0.2.1;flush-ruleset:80",
	})
	if res.Code == 0 || res.Msg != "目标地址格式错误" {
		t.Fatalf("CreateForward returned code=%d msg=%q", res.Code, res.Msg)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Count(&count).Error; err != nil {
		t.Fatalf("count forwards: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid target wrote %d forward rows", count)
	}
}

func TestCreateForwardRejectsInvalidNftExitNodeAddressBeforeDatabaseWrite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	now := time.Now().UnixMilli()
	nodes := []model.Node{
		{
			Name:        "nft-entry-node-address",
			Secret:      "secret-nft-entry-node-address",
			IP:          "192.0.2.10",
			ServerIP:    "192.0.2.10",
			PortSta:     10000,
			PortEnd:     20000,
			ForwardMode: forwardModeNftables,
			CreatedTime: now,
			Status:      nodeStatusOnline,
		},
		{
			Name:        "gost-exit-invalid-address",
			Secret:      "secret-gost-exit-invalid-address",
			IP:          "192.0.2.20",
			ServerIP:    "192.0.2.20;flush-ruleset",
			PortSta:     20001,
			PortEnd:     30000,
			ForwardMode: forwardModeGost,
			CreatedTime: now,
			Status:      nodeStatusOnline,
		},
	}
	if err := model.DB.Create(&nodes).Error; err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	protocol := "tls"
	tunnel := model.Tunnel{
		Name:          "nft-invalid-exit-node-target",
		InNodeID:      nodes[0].ID,
		InIP:          nodes[0].IP,
		OutNodeID:     nodes[1].ID,
		OutIP:         nodes[1].ServerIP,
		Type:          tunnelTypeTunnelForward,
		Protocol:      &protocol,
		Flow:          1,
		TCPListenAddr: "0.0.0.0",
		CreatedTime:   now,
		UpdatedTime:   now,
		Status:        tunnelStatusActive,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	res := CreateForward(CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: "admin"}, dto.ForwardDto{
		Name:       "blocked-node-target",
		TunnelID:   tunnel.ID,
		RemoteAddr: "198.51.100.1:443",
	})
	if res.Code == 0 || res.Msg != "出口节点地址格式错误" {
		t.Fatalf("CreateForward returned code=%d msg=%q", res.Code, res.Msg)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Count(&count).Error; err != nil {
		t.Fatalf("count forwards: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid exit node target wrote %d forward rows", count)
	}
}

func TestFlushForwardConntrackSendsEntryPortProtocols(t *testing.T) {
	oldSend := sendNodeMessage
	defer func() { sendNodeMessage = oldSend }()

	var calls []struct {
		nodeID  int64
		msgType string
		data    map[string]interface{}
	}
	sendNodeMessage = func(nodeID int64, data interface{}, msgType string) ws.GostResult {
		payload, ok := data.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected payload type %T", data)
		}
		calls = append(calls, struct {
			nodeID  int64
			msgType string
			data    map[string]interface{}
		}{nodeID: nodeID, msgType: msgType, data: payload})
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	tunnel := &model.Tunnel{
		Type:          tunnelTypePortForward,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
	}
	forward := &model.Forward{ID: 1, InPort: 1002}
	node := &model.Node{ID: 9, ForwardMode: "nftables"}

	if err := FlushForwardConntrack(forward, tunnel, node); err != nil {
		t.Fatalf("FlushForwardConntrack returned error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected one FlushConntrack call, got %#v", calls)
	}
	if calls[0].nodeID != 9 || calls[0].msgType != "FlushConntrack" {
		t.Fatalf("unexpected call metadata: %#v", calls[0])
	}
	if calls[0].data["port"] != 1002 {
		t.Fatalf("port payload=%#v, want 1002", calls[0].data["port"])
	}
	protocols, ok := calls[0].data["protocols"].([]string)
	if !ok {
		t.Fatalf("protocols payload has type %T", calls[0].data["protocols"])
	}
	if strings.Join(protocols, ",") != "tcp,udp" {
		t.Fatalf("protocols=%v, want tcp,udp", protocols)
	}
}

func nftRulesToStrings(rules []NftRuleToAdd) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.Rule)
	}
	return out
}
