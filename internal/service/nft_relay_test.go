package service

import (
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

type nftRelayFixture struct {
	entry   model.Node
	relay   model.Node
	exit    model.Node
	tunnel  model.Tunnel
	forward model.Forward
	member  model.ForwardExitMember
}

func TestThreeNodeNftRelayBuildsEveryHopAndCountsOnlyEntry(t *testing.T) {
	fx := setupNftRelayFixture(t)

	tests := []struct {
		name       string
		node       model.Node
		listenPort int
		target     string
		counter    bool
	}{
		{name: "entry", node: fx.entry, listenPort: fx.forward.InPort, target: fx.relay.ServerIP + ":" + strconv.Itoa(fx.member.RelayPort), counter: true},
		{name: "relay", node: fx.relay, listenPort: fx.member.RelayPort, target: fx.exit.ServerIP + ":" + strconv.Itoa(fx.member.OutPort)},
		{name: "exit", node: fx.exit, listenPort: fx.member.OutPort, target: "198.51.100.80:443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules, err := buildNftRules(tt.node.ID)
			if err != nil {
				t.Fatalf("build full rules: %v", err)
			}
			full := rulesForForward(rules, fx.forward.ID)
			assertRelayHopRules(t, full, tt.listenPort, tt.target, tt.counter)

			incremental, err := buildForwardNftRulesToAdd(&fx.forward, &fx.tunnel, &tt.node)
			if err != nil {
				t.Fatalf("build incremental rules: %v", err)
			}
			texts := make([]string, 0, len(incremental))
			for _, rule := range incremental {
				texts = append(texts, rule.Rule)
			}
			assertRelayHopRules(t, texts, tt.listenPort, tt.target, tt.counter)
		})
	}
	for _, node := range []model.Node{fx.entry, fx.relay, fx.exit} {
		rules, err := buildNftRules(node.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw := rulesForForward(rules, fx.forward.ID)
		scope := loadPortDetectScope(node.ID)
		for _, parsed := range gost.ParseNftRules(raw) {
			key := buildForwardKey(parsed.Protocol, parsed.InPort, parsed.TargetHost, parsed.OutPort)
			if !scope.isTunnelForwardRule(parsed, key) {
				t.Fatalf("node %d managed relay rule was reported as missing: %+v", node.ID, parsed)
			}
		}
	}

	entryRules, err := buildNftRules(fx.entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rulesForForward(entryRules, fx.forward.ID), "\n"), fx.exit.ServerIP+":"+strconv.Itoa(fx.member.OutPort)) {
		t.Fatal("entry rules bypassed the relay node")
	}

	refreshNodes, err := collectNftRefreshNodeIDs(fx.entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantNodes := []int64{fx.entry.ID, fx.relay.ID, fx.exit.ID}
	sort.Slice(wantNodes, func(i, j int) bool { return wantNodes[i] < wantNodes[j] })
	if len(refreshNodes) != len(wantNodes) {
		t.Fatalf("refresh nodes=%v, want %v", refreshNodes, wantNodes)
	}
	for i := range wantNodes {
		if refreshNodes[i] != wantNodes[i] {
			t.Fatalf("refresh nodes=%v, want %v", refreshNodes, wantNodes)
		}
	}

	for _, tt := range []struct {
		nodeID int64
		port   int
	}{
		{nodeID: fx.entry.ID, port: fx.forward.InPort},
		{nodeID: fx.relay.ID, port: fx.member.RelayPort},
		{nodeID: fx.exit.ID, port: fx.member.OutPort},
	} {
		if got, ok := forwardListenPortForNode(&fx.forward, &fx.tunnel, tt.nodeID); !ok || got != tt.port {
			t.Fatalf("listen port on node %d=(%d,%v), want %d", tt.nodeID, got, ok, tt.port)
		}
	}
	referenced, err := nodeReferencedByForward(fx.relay.ID)
	if err != nil || !referenced {
		t.Fatalf("relay referenced=(%v,%v), want true", referenced, err)
	}
	if res := DeleteNode(fx.relay.ID); res.Code == 0 || !strings.Contains(res.Msg, "中继节点") {
		t.Fatalf("DeleteNode(relay) code=%d msg=%q", res.Code, res.Msg)
	}
	options, err := loadTrafficTunnelOptions(packageTrafficScope{AllUsers: true})
	if err != nil {
		t.Fatalf("load traffic tunnel options: %v", err)
	}
	foundPath := false
	for _, option := range options {
		if option.TunnelID != fx.tunnel.ID {
			continue
		}
		foundPath = option.RelayNodeID != nil && *option.RelayNodeID == fx.relay.ID && option.RelayNodeName == fx.relay.Name
	}
	if !foundPath {
		t.Fatalf("traffic tunnel options missing relay path: %+v", options)
	}
	preview := buildProxyRelayPreview(model.ProxyNode{Name: "subscription-target", Server: "198.51.100.80", Port: 443}, fx.tunnel, nil, &fx.forward)
	if preview.Relay == nil || preview.Relay.NodeID != fx.relay.ID || preview.Relay.Port != fx.member.RelayPort {
		t.Fatalf("subscription relay preview missing middle hop: %+v", preview)
	}
}

func TestThreeNodeNftRelayAllocatesAndReservesRelayPorts(t *testing.T) {
	fx := setupNftRelayFixture(t)
	now := time.Now().UnixMilli()
	outPort := fx.exit.PortSta + 2
	forward := model.Forward{
		UserID: fx.forward.UserID, Name: "relay-port-2", TunnelID: fx.tunnel.ID,
		InPort: fx.entry.PortSta + 2, OutPort: &outPort, RemoteAddr: "198.51.100.81:443",
		Strategy: "fifo", ExitMode: exitModeSingle, ExitStrategy: exitStrategyFIFO,
		Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create second forward: %v", err)
	}
	rows, msg := saveForwardExitMembers(&forward, &fx.tunnel, []dto.ForwardExitMemberDto{{OutNodeID: fx.exit.ID, Active: true, Weight: 1}}, &forward.ID)
	if msg != "" || len(rows) != 1 {
		t.Fatalf("save relay member rows=%+v msg=%q", rows, msg)
	}
	if rows[0].RelayPort < fx.relay.PortSta || rows[0].RelayPort > fx.relay.PortEnd {
		t.Fatalf("relay port=%d outside %d-%d", rows[0].RelayPort, fx.relay.PortSta, fx.relay.PortEnd)
	}
	if rows[0].RelayPort == fx.member.RelayPort {
		t.Fatalf("relay port %d was allocated twice", rows[0].RelayPort)
	}
	used, err := getAllUsedPortsOnNode(fx.relay.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !used[fx.member.RelayPort] || !used[rows[0].RelayPort] {
		t.Fatalf("relay used ports=%v, want %d and %d", used, fx.member.RelayPort, rows[0].RelayPort)
	}
}

func TestThreeNodeNftRelayRejectsInvalidManualExit(t *testing.T) {
	fx := setupNftRelayFixture(t)
	if _, msg := normalizeForwardExitMembers(&fx.tunnel, exitModeManual, []dto.ForwardExitMemberDto{{OutNodeID: fx.relay.ID, Active: true}}); msg != "三节点串联的出口不能与入口或中继节点重复" {
		t.Fatalf("relay-as-exit msg=%q", msg)
	}
	now := time.Now().UnixMilli()
	gostExit := model.Node{
		Name: "relay-gost-exit", Secret: "relay-gost-exit-secret", IP: "192.0.2.40", ServerIP: "192.0.2.40",
		PortSta: 50000, PortEnd: 50099, ForwardMode: forwardModeGost, CreatedTime: now, Status: nodeStatusOnline,
	}
	if err := model.DB.Create(&gostExit).Error; err != nil {
		t.Fatal(err)
	}
	msg := validateForwardNftNodeTargets(&fx.tunnel, &fx.entry, &fx.exit, []dto.ForwardExitMemberDto{{OutNodeID: gostExit.ID, Active: true}})
	if msg != "三节点串联的所有出口节点必须使用 nftables 模式" {
		t.Fatalf("Gost relay exit msg=%q", msg)
	}
	for _, tt := range []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{name: "multi-target", remoteAddr: "198.51.100.10:443,198.51.100.11:443", want: "三节点 nftables 串联暂不支持多目标自动负载，请使用手动目标"},
		{name: "ipv6-target", remoteAddr: "[2001:db8::10]:443", want: "三节点串联当前仅支持 IPv4 目标地址"},
	} {
		res := CreateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID, UserName: fx.forward.UserName}, dto.ForwardDto{
			Name: tt.name, TunnelID: fx.tunnel.ID, RemoteAddr: tt.remoteAddr,
		})
		if res.Code == 0 || res.Msg != tt.want {
			t.Fatalf("%s code=%d msg=%q, want %q", tt.name, res.Code, res.Msg, tt.want)
		}
	}
}

func TestCreateForwardDeploysThreeNodeNftRelayPath(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	deployed := map[int64][]string{}
	var order []int64
	sendNftRefreshMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if command != "ApplyNftRules" {
			t.Fatalf("command=%q, want ApplyNftRules", command)
		}
		deployed[nodeID] = append([]string(nil), data.(map[string]interface{})["rules"].([]string)...)
		order = append(order, nodeID)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	res := CreateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID, UserName: fx.forward.UserName}, dto.ForwardDto{
		Name: "created-three-node", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.82:8443",
	})
	if res.Code != 0 {
		t.Fatalf("CreateForward failed: %s", res.Msg)
	}
	var forward model.Forward
	if err := model.DB.Where("name = ?", "created-three-node").First(&forward).Error; err != nil {
		t.Fatalf("load created forward: %v", err)
	}
	var member model.ForwardExitMember
	if err := model.DB.Where("forward_id = ?", forward.ID).First(&member).Error; err != nil {
		t.Fatalf("load created member: %v", err)
	}
	if member.RelayPort <= 0 || member.OutPort <= 0 || forward.OutPort == nil || *forward.OutPort != member.OutPort {
		t.Fatalf("created ports forward=%+v member=%+v", forward, member)
	}
	assertRelayHopRules(t, filterRulesByComment(deployed[fx.entry.ID], forward.ID), forward.InPort, fx.relay.ServerIP+":"+strconv.Itoa(member.RelayPort), true)
	assertRelayHopRules(t, filterRulesByComment(deployed[fx.relay.ID], forward.ID), member.RelayPort, fx.exit.ServerIP+":"+strconv.Itoa(member.OutPort), false)
	assertRelayHopRules(t, filterRulesByComment(deployed[fx.exit.ID], forward.ID), member.OutPort, "198.51.100.82:8443", false)
	wantOrder := []int64{fx.exit.ID, fx.relay.ID, fx.entry.ID}
	if len(order) < len(wantOrder) || !slices.Equal(order[:len(wantOrder)], wantOrder) {
		t.Fatalf("deployment order=%v, want downstream-first %v", order, wantOrder)
	}
}

func TestCreateForwardRelayFailureRollsBackAllDesiredState(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	failedRelay := false
	calls := map[int64]int{}
	var order []int64
	sendNftRefreshMessage = func(nodeID int64, _ interface{}, _ string) ws.GostResult {
		calls[nodeID]++
		order = append(order, nodeID)
		if nodeID == fx.relay.ID && !failedRelay {
			failedRelay = true
			return ws.GostResult{Msg: "injected relay apply failure"}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	res := CreateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID, UserName: fx.forward.UserName}, dto.ForwardDto{
		Name: "failed-three-node", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.83:9443",
	})
	if res.Code == 0 {
		t.Fatalf("CreateForward unexpectedly succeeded: %+v", res)
	}
	var forwardCount int64
	if err := model.DB.Model(&model.Forward{}).Where("name = ?", "failed-three-node").Count(&forwardCount).Error; err != nil {
		t.Fatal(err)
	}
	if forwardCount != 0 {
		t.Fatalf("failed forward rows=%d, want 0", forwardCount)
	}
	if len(order) < 3 || order[0] != fx.exit.ID || order[1] != fx.relay.ID || order[2] != fx.entry.ID {
		t.Fatalf("failure order=%v, want C then B failure, followed by A cleanup", order)
	}
	if calls[fx.entry.ID] != 1 || calls[fx.relay.ID] < 2 || calls[fx.exit.ID] < 2 {
		t.Fatalf("refresh calls=%v, entry must only receive cleanup after relay failure", calls)
	}
}

func TestCreateForwardRelayUnknownDownstreamStopsBeforeEntry(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	unknownSent := false
	calls := map[int64]int{}
	var order []int64
	sendNftRefreshMessage = func(nodeID int64, _ interface{}, _ string) ws.GostResult {
		calls[nodeID]++
		order = append(order, nodeID)
		if nodeID == fx.exit.ID && !unknownSent {
			unknownSent = true
			return ws.GostResult{Msg: "lost downstream response", OutcomeUnknown: true}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := CreateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID, UserName: fx.forward.UserName}, dto.ForwardDto{
		Name: "unknown-three-node", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.84:443",
	})
	if res.Code == 0 {
		t.Fatalf("CreateForward unexpectedly accepted unknown downstream outcome: %+v", res)
	}
	if len(order) < 4 || order[0] != fx.exit.ID || order[1] != fx.entry.ID {
		t.Fatalf("unknown outcome order=%v, want C unknown then cleanup from A", order)
	}
	if calls[fx.entry.ID] != 1 || calls[fx.relay.ID] != 1 || calls[fx.exit.ID] != 2 {
		t.Fatalf("refresh calls=%v, B/A must not receive desired publish", calls)
	}
}

func TestForceDeleteForwardCleansThreeNodeNftRuntime(t *testing.T) {
	fx := setupNftRelayFixture(t)
	originalRefresh, originalSend := sendNftRefreshMessage, sendNodeMessage
	t.Cleanup(func() { sendNftRefreshMessage, sendNodeMessage = originalRefresh, originalSend })
	refreshed := map[int64][]string{}
	sendNodeMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	sendNftRefreshMessage = func(nodeID int64, data interface{}, _ string) ws.GostResult {
		refreshed[nodeID] = append([]string(nil), data.(map[string]interface{})["rules"].([]string)...)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := ForceDeleteForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, fx.forward.ID)
	if res.Code != 0 {
		t.Fatalf("ForceDeleteForward failed: %s", res.Msg)
	}
	for _, nodeID := range []int64{fx.entry.ID, fx.relay.ID, fx.exit.ID} {
		if _, ok := refreshed[nodeID]; !ok {
			t.Fatalf("node %d was not refreshed", nodeID)
		}
		if got := filterRulesByComment(refreshed[nodeID], fx.forward.ID); len(got) != 0 {
			t.Fatalf("node %d retained force-deleted rules: %v", nodeID, got)
		}
	}
}

func TestKeepExtraRuleRejectsIncompleteThreeNodeImport(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := HandleExtraRules(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID, UserName: fx.forward.UserName}, &HandleExtraRulesRequest{
		Rules: []HandleRuleAction{{
			NodeID: fx.entry.ID, InPort: 21999, Action: "keep", Name: "invalid-relay-import",
			TunnelID: fx.tunnel.ID, TargetHost: fx.relay.ServerIP, OutPort: 30999, Protocol: "tcp",
		}},
	})
	data, ok := res.Data.(*HandleExtraRulesResult)
	if res.Code != 0 || !ok || data.Kept != 0 || len(data.Details) != 1 || data.Details[0].Success ||
		!strings.Contains(data.Details[0].Error, "完整 A→B→C 路径") {
		t.Fatalf("HandleExtraRules result=%+v", res)
	}
	var count int64
	if err := model.DB.Model(&model.Forward{}).Where("name = ?", "invalid-relay-import").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("incomplete relay import rows=%d err=%v", count, err)
	}
}

func filterRulesByComment(rules []string, forwardID int64) []string {
	needle := "fp:" + strconv.FormatInt(forwardID, 10) + ":"
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if strings.Contains(rule, needle) {
			out = append(out, rule)
		}
	}
	return out
}

func assertRelayHopRules(t *testing.T, rules []string, listenPort int, target string, counter bool) {
	t.Helper()
	if len(rules) != 8 {
		t.Fatalf("generated %d rules, want 8: %v", len(rules), rules)
	}
	joined := strings.Join(rules, "\n")
	for _, fragment := range []string{
		" dport " + strconv.Itoa(listenPort) + " dnat to " + target,
		" ct original proto-dst " + strconv.Itoa(listenPort),
		" masquerade",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("rules missing %q: %s", fragment, joined)
		}
	}
	hasCounter := strings.Contains(joined, " counter accept comment ")
	if hasCounter != counter {
		t.Fatalf("counter=%v, want %v: %s", hasCounter, counter, joined)
	}
}

func setupNftRelayFixture(t *testing.T) nftRelayFixture {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })

	now := time.Now().UnixMilli()
	entry := createRelayTestNode(t, "relay-entry", "192.0.2.10", 20000, 20099, now)
	relay := createRelayTestNode(t, "relay-middle", "192.0.2.20", 30000, 30099, now)
	exit := createRelayTestNode(t, "relay-exit", "192.0.2.30", 40000, 40099, now)
	expires := time.Now().Add(24 * time.Hour).UnixMilli()
	admin := model.User{
		User: "relay-admin", Pwd: "unused", RoleID: adminRoleID, ExpTime: &expires,
		Flow: 100, FlowResetTime: now, Num: 100, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	protocol := "tcp+udp"
	relayID, relayIP := relay.ID, relay.ServerIP
	tunnel := model.Tunnel{
		Name: "three-node-nft", InNodeID: entry.ID, InIP: entry.IP,
		RelayNodeID: &relayID, RelayIP: &relayIP, OutNodeID: exit.ID, OutIP: exit.ServerIP,
		Type: tunnelTypeTunnelForward, Protocol: &protocol, Flow: 2, TrafficRatio: 1,
		TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		Status: tunnelStatusActive, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	outPort := 40001
	forward := model.Forward{
		UserID: admin.ID, UserName: admin.User, Name: "relay-forward", TunnelID: tunnel.ID,
		InPort: 20001, OutPort: &outPort, RemoteAddr: "198.51.100.80:443",
		Strategy: "fifo", ExitMode: exitModeSingle, ExitStrategy: exitStrategyFIFO,
		Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	member := model.ForwardExitMember{
		ForwardID: forward.ID, OutNodeID: exit.ID, RelayPort: 30001, OutPort: outPort,
		Weight: 1, Status: 1, Active: 1, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&member).Error; err != nil {
		t.Fatalf("create exit member: %v", err)
	}
	return nftRelayFixture{entry: entry, relay: relay, exit: exit, tunnel: tunnel, forward: forward, member: member}
}

func createRelayTestNode(t *testing.T, name, serverIP string, portStart, portEnd int, now int64) model.Node {
	t.Helper()
	node := model.Node{
		Name: name, Secret: name + "-secret", IP: serverIP, ServerIP: serverIP,
		PortSta: portStart, PortEnd: portEnd, ForwardMode: forwardModeNftables,
		CreatedTime: now, Status: nodeStatusOnline,
	}
	if err := model.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
	return node
}
