package service

import (
	"path/filepath"
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

func TestBalanceNftExitsBuildRulesForEveryActiveMember(t *testing.T) {
	_, exits, tunnel, forward, members := setupBalanceNftExitRules(t)

	for i := range exits {
		rules, err := buildNftRules(exits[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		assertNftExitRuleSet(t, rulesForForward(rules, forward.ID), members[i].OutPort)

		toAdd, err := buildForwardNftRulesToAdd(&forward, &tunnel, &exits[i])
		if err != nil {
			t.Fatalf("build incremental rules for exit %d: %v", exits[i].ID, err)
		}
		incremental := make([]string, 0, len(toAdd))
		for _, rule := range toAdd {
			incremental = append(incremental, rule.Rule)
		}
		assertNftExitRuleSet(t, incremental, members[i].OutPort)

		specs := buildForwardRuleSpecs(&forward, exits[i].ID)
		if len(specs) != 6 || specs[0].listenPort != members[i].OutPort || specs[3].listenPort != members[i].OutPort {
			t.Fatalf("exit %d fallback specs=%+v, want member port %d", exits[i].ID, specs, members[i].OutPort)
		}
	}
}

func TestBalanceNftExitIncrementalCRUDAddsEachMemberAndSkipsGostNode(t *testing.T) {
	entry, exits, tunnel, forward, members := setupBalanceNftExitRules(t)
	original := sendNftIncrementalMessage
	t.Cleanup(func() { sendNftIncrementalMessage = original })

	calls := map[int64][]string{}
	sendNftIncrementalMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if command != "AddNftRules" {
			t.Fatalf("command=%q", command)
		}
		calls[nodeID] = append([]string(nil), data.(map[string]interface{})["rules"].([]string)...)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	if err := AddForwardRules(&forward, &tunnel, &entry); err != nil {
		t.Fatalf("Gost entry add: %v", err)
	}
	for i := range exits {
		if err := AddForwardRules(&forward, &tunnel, &exits[i]); err != nil {
			t.Fatalf("NFT exit %d add: %v", exits[i].ID, err)
		}
		assertNftExitRuleSet(t, calls[exits[i].ID], members[i].OutPort)
	}
	if _, ok := calls[entry.ID]; ok {
		t.Fatalf("Gost node %d received NFT CRUD command", entry.ID)
	}
}

func TestBalanceNftExitReconnectRefreshesCurrentMembersPort(t *testing.T) {
	_, exits, _, _, members := setupBalanceNftExitRules(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })

	got := map[int64][]string{}
	sendNftRefreshMessage = func(nodeID int64, data interface{}, command string) ws.GostResult {
		if command != "ApplyNftRules" {
			t.Fatalf("command=%q", command)
		}
		got[nodeID] = append([]string(nil), data.(map[string]interface{})["rules"].([]string)...)
		return ws.GostResult{Msg: gost.SuccessMsg}
	}

	for i := range exits {
		SyncNodeForwardsOnConnect(exits[i].ID)
		assertNftExitRuleSet(t, got[exits[i].ID], members[i].OutPort)
		if items := gostExitSyncItems(exits[i].ID); len(items) != 0 {
			t.Fatalf("NFT exit %d received Gost remote reconnect items: %+v", exits[i].ID, items)
		}
	}
}

func TestBalanceNftRefreshCollectsEveryExitMember(t *testing.T) {
	entry, exits, _, _, _ := setupBalanceNftExitRules(t)
	got, err := collectNftRefreshNodeIDs(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{entry.ID, exits[0].ID, exits[1].ID}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("refresh nodes=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refresh nodes=%v, want %v", got, want)
		}
	}
}

func setupBalanceNftExitRules(t *testing.T) (model.Node, []model.Node, model.Tunnel, model.Forward, []model.ForwardExitMember) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })

	now := time.Now().UnixMilli()
	entry := createForwardExitNode(t, "balance-entry", "192.0.2.10", 20000, 20099, forwardModeGost, now)
	exits := []model.Node{
		createForwardExitNode(t, "balance-nft-a", "192.0.2.20", 31000, 31099, forwardModeNftables, now),
		createForwardExitNode(t, "balance-nft-b", "192.0.2.30", 32000, 32099, forwardModeNftables, now),
	}
	protocol := "tcp"
	tunnel := model.Tunnel{
		Name: "balance-nft", InNodeID: entry.ID, OutNodeID: exits[0].ID,
		Type: tunnelTypeTunnelForward, Protocol: &protocol, TCPListenAddr: "0.0.0.0", UDPListenAddr: "0.0.0.0",
		Flow: 1, Status: tunnelStatusActive, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	outPort := 31001
	forward := model.Forward{
		UserID: 1, Name: "balance-nft", TunnelID: tunnel.ID, InPort: 21001, OutPort: &outPort,
		RemoteAddr: "198.51.100.80:443", Strategy: "fifo", ExitMode: exitModeBalance,
		ExitStrategy: exitStrategyRound, Status: forwardStatusActive, CreatedTime: now, UpdatedTime: now,
	}
	if err := model.DB.Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}
	members := []model.ForwardExitMember{
		{ForwardID: forward.ID, OutNodeID: exits[0].ID, OutPort: 31001, Weight: 1, Status: 1, Active: 1, CreatedTime: now, UpdatedTime: now},
		{ForwardID: forward.ID, OutNodeID: exits[1].ID, OutPort: 32001, Weight: 1, Status: 1, Active: 1, CreatedTime: now, UpdatedTime: now},
	}
	if err := model.DB.Create(&members).Error; err != nil {
		t.Fatalf("create exit members: %v", err)
	}
	return entry, exits, tunnel, forward, members
}

func rulesForForward(rules []dto.NftRuleDto, forwardID int64) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.ForwardID == forwardID {
			out = append(out, rule.Rule)
		}
	}
	return out
}

func assertNftExitRuleSet(t *testing.T, rules []string, outPort int) {
	t.Helper()
	if len(rules) != 8 {
		t.Fatalf("port %d generated %d rules, want 8 (TCP and UDP): %v", outPort, len(rules), rules)
	}
	joined := strings.Join(rules, "\n")
	for _, fragment := range []string{
		" dport " + strconv.Itoa(outPort) + " dnat to ",
		" forward ",
		" ct original proto-dst " + strconv.Itoa(outPort),
		" ct state new,established,related",
		" ct state established,related",
		" masquerade",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("port %d rules missing %q: %s", outPort, fragment, joined)
		}
	}
}
