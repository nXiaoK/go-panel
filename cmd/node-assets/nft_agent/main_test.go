package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

const agentTestGeneration = "flux_panel_g_0123456789abcdef0123456789abcdef"

func TestBuildWSURLReportsPersistedPanelBaseURL(t *testing.T) {
	got, err := buildWSURL(config{ServerAddr: "https://panel.example.com/base/", Secret: "node secret"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wss" || u.Host != "panel.example.com" || u.Path != "/base/system-info" {
		t.Fatalf("WebSocket URL = %q", got)
	}
	if u.Query().Get("panelUrl") != "https://panel.example.com/base" {
		t.Fatalf("panelUrl = %q", u.Query().Get("panelUrl"))
	}
	if u.Query().Get("secret") != "node secret" {
		t.Fatalf("secret was not URL encoded: %q", u.Query().Get("secret"))
	}
}

func TestNormalizePanelBaseURLKeepsLegacyBareHost(t *testing.T) {
	tests := map[string]string{
		"panel.example.com:6365/": "http://panel.example.com:6365",
		"[2001:db8::1]:6365/":     "http://[2001:db8::1]:6365",
	}
	for raw, want := range tests {
		got, err := normalizePanelBaseURL(raw)
		if err != nil {
			t.Fatalf("normalizePanelBaseURL(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalizePanelBaseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizePanelBaseURLRejectsAmbiguousPaths(t *testing.T) {
	for _, raw := range []string{
		"https://panel.example.com/%2e%2e/assets",
		"https://panel.example.com/base//nested",
		"https://panel.example.com?",
	} {
		if _, err := normalizePanelBaseURL(raw); err == nil {
			t.Fatalf("normalizePanelBaseURL(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestAddNftRuleTargetsValidatedActiveGeneration(t *testing.T) {
	markerPath := writeAgentTestMarker(t, agentTestGeneration)
	lockPath := filepath.Join(t.TempDir(), "handoff.lock")
	rule := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment "fp:1:2:3"`
	raw, err := json.Marshal(map[string]string{"rule": rule})
	if err != nil {
		t.Fatal(err)
	}

	var mutation string
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = markerPath
	ops.lockPath = lockPath
	ops.run = activeTableTestRunner(t, agentTestGeneration, false)
	ops.runStdin = func(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Fatal("ordinary nft mutation has no bounded deadline")
		}
		if name != "nft" || strings.Join(args, " ") != "-f -" {
			t.Fatalf("mutation command=%s %v", name, args)
		}
		mutation = stdin
		return nil, nil
	}
	if err := addNftRuleWithOps(context.Background(), raw, ops); err != nil {
		t.Fatalf("addNftRuleWithOps: %v", err)
	}
	want := strings.Replace(rule, "inet flux_panel ", "inet "+agentTestGeneration+" ", 1) + "\n"
	if mutation != want {
		t.Fatalf("mutation stdin=%q, want %q", mutation, want)
	}
}

func TestDeleteAndFindUseTheSameActiveGeneration(t *testing.T) {
	markerPath := writeAgentTestMarker(t, agentTestGeneration)
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = markerPath
	ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
	var operational [][]string
	ops.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "nft" {
			t.Fatalf("command=%q", name)
		}
		if len(args) > 0 && args[0] == "-j" {
			return activeTableJSON(agentTestGeneration, false), nil
		}
		operational = append(operational, append([]string(nil), args...))
		if len(args) > 0 && args[0] == "-a" {
			return []byte("table inet " + agentTestGeneration + " {\n chain forward {\n meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct state new,established,related counter accept comment \"fp:7:2:3:up\" # handle 44\n }\n}\n"), nil
		}
		return nil, nil
	}

	if err := deleteNftRuleWithOps(context.Background(), json.RawMessage(`{"chain":"forward","handle":44}`), ops); err != nil {
		t.Fatalf("deleteNftRuleWithOps: %v", err)
	}
	handleView, err := findRuleHandlesWithOps(context.Background(), json.RawMessage(`{"forwardId":7}`), ops)
	if err != nil {
		t.Fatalf("findRuleHandlesWithOps: %v", err)
	}
	if handleView.Table != agentTestGeneration || len(handleView.Handles) != 1 || handleView.Handles[0] != (RuleHandle{Chain: "forward", Handle: 44}) {
		t.Fatalf("handle view=%+v", handleView)
	}
	ruleView, err := listNftRulesWithOps(context.Background(), ops)
	if err != nil {
		t.Fatalf("listNftRulesWithOps: %v", err)
	}
	if ruleView.Table != agentTestGeneration || len(ruleView.Rules) != 1 || !strings.HasPrefix(ruleView.Rules[0], "add rule inet flux_panel forward ") || strings.Contains(ruleView.Rules[0], agentTestGeneration) {
		t.Fatalf("canonical listed rule view=%+v", ruleView)
	}
	want := [][]string{
		{"delete", "rule", "inet", agentTestGeneration, "forward", "handle", "44"},
		{"-a", "list", "table", "inet", agentTestGeneration},
		{"-a", "list", "table", "inet", agentTestGeneration},
	}
	if fmt.Sprint(operational) != fmt.Sprint(want) {
		t.Fatalf("operational calls=%v, want %v", operational, want)
	}
}

func TestListAndFindAcceptRealNftHandleHeaders(t *testing.T) {
	output := []byte("table inet " + agentTestGeneration + " { # handle 7\n" +
		" chain forward { # handle 8\n" +
		" meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct state new,established,related counter accept comment \"fp:7:2:3:up\" # handle 44\n }\n}\n")
	rules, err := parseListedNftRules(output, agentTestGeneration)
	if err != nil || len(rules) != 1 {
		t.Fatalf("parse real nft list=(%q,%v)", rules, err)
	}
	handles, err := parseRuleHandles(output, agentTestGeneration, 7)
	if err != nil || len(handles) != 1 || handles[0] != (RuleHandle{Chain: "forward", Handle: 44}) {
		t.Fatalf("parse real nft handles=(%+v,%v)", handles, err)
	}
}

func TestListAndFindRejectMalformedOrAmbiguousHandles(t *testing.T) {
	validRule := " meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct state new,established,related counter accept comment \"fp:7:2:3:up\" # handle 44\n"
	for _, output := range []string{
		"table ip " + agentTestGeneration + " { # handle 7\n chain forward {\n" + validRule + "}\n",
		"table inet flux_panel { # handle 7\n chain forward {\n" + validRule + "}\n",
		"table inet " + agentTestGeneration + " { # handle 7 garbage\n chain forward {\n" + validRule + "}\n",
		"table inet " + agentTestGeneration + " { # handle 7\n chain forward {\n" + strings.Replace(validRule, "# handle 44", "# handle 44 garbage", 1) + "}\n",
		"table inet " + agentTestGeneration + " { # handle 7\n chain forward { # handle 8\n unexpected garbage\n" + validRule + "}\n",
	} {
		if _, err := parseListedNftRules([]byte(output), agentTestGeneration); err == nil {
			t.Errorf("list accepted malformed output %q", output)
		}
		if _, err := parseRuleHandles([]byte(output), agentTestGeneration, 7); err == nil {
			t.Errorf("find accepted malformed output %q", output)
		}
	}
	duplicate := []byte("table inet " + agentTestGeneration + " { # handle 7\n chain forward { # handle 8\n" + validRule + validRule + "}\n}\n")
	if _, err := parseListedNftRules(duplicate, agentTestGeneration); err == nil {
		t.Fatal("list accepted duplicate chain/handle")
	}
	if _, err := parseRuleHandles(duplicate, agentTestGeneration, 7); err == nil {
		t.Fatal("find accepted duplicate chain/handle")
	}
}

func TestDeleteAndFindRejectInvalidIdentifiersBeforeNft(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("nft runner called for invalid identifier")
		return nil, nil
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"chain":"other","handle":1}`),
		json.RawMessage(`{"chain":"forward","handle":0}`),
		json.RawMessage(`{"chain":"forward","handle":-1}`),
	} {
		if err := deleteNftRuleWithOps(context.Background(), raw, ops); err == nil {
			t.Fatalf("delete accepted %s", raw)
		}
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{"forwardId":0}`), json.RawMessage(`{"forwardId":-1}`)} {
		if _, err := findRuleHandlesWithOps(context.Background(), raw, ops); err == nil {
			t.Fatalf("find accepted %s", raw)
		}
	}
}

func TestResolveActiveNftTableFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		marker     string
		runner     commandRunner
		wantCalled bool
	}{
		{name: "dormant", marker: agentTestGeneration, runner: activeTableTestRunner(t, agentTestGeneration, true), wantCalled: true},
		{name: "missing", marker: agentTestGeneration, runner: func(context.Context, string, ...string) ([]byte, error) { return activeTableJSONWithoutTable(), nil }, wantCalled: true},
		{name: "marker mismatch", marker: agentTestGeneration, runner: activeTableTestRunner(t, nftgeneration.LegacyTable, false), wantCalled: true},
		{name: "malformed marker", marker: "flux_panel_g_invalid", runner: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("runner called for malformed marker")
			return nil, nil
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			markerPath := filepath.Join(t.TempDir(), "active-table")
			if err := os.WriteFile(markerPath, []byte(tc.marker+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				called = true
				return tc.runner(ctx, name, args...)
			}
			if table, err := resolveActiveNftTableWith(context.Background(), markerPath, runner); err == nil || table != "" {
				t.Fatalf("resolveActiveNftTableWith=(%q,%v), want fail closed", table, err)
			}
			if called != tc.wantCalled {
				t.Fatalf("runner called=%v, want %v", called, tc.wantCalled)
			}
		})
	}
}

func TestResolveActiveNftTableRejectsAmbiguousMachineJSON(t *testing.T) {
	markerPath := writeAgentTestMarker(t, agentTestGeneration)
	invalid := []string{
		`null`,
		`{"nftables":null}`,
		`{"nftables":[null]}`,
		`{"nftables":[{"chain":null},{"table":{"family":"inet","name":"` + agentTestGeneration + `","flags":[]}}]}`,
		`{"nftables":[{"rule":7},{"table":{"family":"inet","name":"` + agentTestGeneration + `","flags":[]}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"` + agentTestGeneration + `","name":"` + agentTestGeneration + `","flags":[]}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"` + agentTestGeneration + `","flags":[]}}],"nftables":[]}`,
		string(activeTableJSON(agentTestGeneration, false)) + `{}`,
	}
	for _, output := range invalid {
		t.Run(output, func(t *testing.T) {
			runner := func(context.Context, string, ...string) ([]byte, error) { return []byte(output), nil }
			if table, err := resolveActiveNftTableWith(context.Background(), markerPath, runner); err == nil || table != "" {
				t.Fatalf("resolve ambiguous JSON=(%q,%v)", table, err)
			}
		})
	}
}

func TestActiveTableDebian12ReleaseMetadataDoesNotRelaxFlags(t *testing.T) {
	t.Parallel()

	makeInventory := func(flags string) []byte {
		return []byte(`{"nftables":[` +
			`{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}},` +
			`{"table":{"family":"inet","name":"` + agentTestGeneration + `","handle":1` + flags + `}}]}`)
	}
	for _, flags := range []string{"", `,"flags":[]`} {
		if err := validateActiveNftTableJSON(makeInventory(flags), agentTestGeneration); err != nil {
			t.Errorf("active Debian 12 flags %q: %v", flags, err)
		}
	}
	for _, flags := range []string{
		`,"flags":["Lester Gooch #5"]`,
		`,"flags":["dormant","Lester Gooch #5"]`,
		`,"flags":"Lester Gooch #5"`,
	} {
		if err := validateActiveNftTableJSON(makeInventory(flags), agentTestGeneration); err == nil {
			t.Errorf("active table accepted unsafe Debian 12 flags %q", flags)
		}
	}
}

func TestActiveTableInventoryAndTextParsersHaveCardinalityBounds(t *testing.T) {
	var inventory strings.Builder
	inventory.WriteString(`{"nftables":[{"table":{"family":"inet","name":"` + agentTestGeneration + `","flags":[]}}`)
	for i := 1; i <= maxNftInventoryElements; i++ {
		inventory.WriteString(`,{"rule":{}}`)
	}
	inventory.WriteString(`]}`)
	if err := validateActiveNftTableJSON([]byte(inventory.String()), agentTestGeneration); err == nil || !strings.Contains(err.Error(), "inventory elements") {
		t.Fatalf("oversized inventory error=%v", err)
	}

	manyLines := bytes.Repeat([]byte("\n"), maxNftOutputLines+2)
	if _, err := parseListedNftRules(manyLines, agentTestGeneration); err == nil || !strings.Contains(err.Error(), "lines") {
		t.Fatalf("high-line list error=%v", err)
	}
	longLine := bytes.Repeat([]byte("x"), maxNftOutputLineBytes+1)
	if _, err := parseRuleHandles(longLine, agentTestGeneration, 1); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("long-line handle error=%v", err)
	}
}

func TestMutationLockContentionReturnsRetryableFailure(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "handoff.lock")
	release, err := nftgeneration.AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	ops := defaultNftAgentOps()
	ops.lockPath = lockPath
	ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
	ops.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("nft runner called while lock contended")
		return nil, nil
	}
	ops.runStdin = func(context.Context, string, []string, string) ([]byte, error) {
		t.Fatal("mutation called while lock contended")
		return nil, nil
	}
	raw := json.RawMessage(`{"rule":"add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80"}`)
	err = addNftRuleWithOps(context.Background(), raw, ops)
	if !errors.Is(err, nftgeneration.ErrLocked) {
		t.Fatalf("contention error=%v, want retryable ErrLocked", err)
	}
}

func TestDeleteNftRulesRejectsGenerationChangeWithoutMutation(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
	ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
	ops.run = activeTableTestRunner(t, agentTestGeneration, false)
	ops.runStdin = func(context.Context, string, []string, string) ([]byte, error) {
		t.Fatal("batch mutation ran after expected generation changed")
		return nil, nil
	}
	oldTable := "flux_panel_g_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	raw := json.RawMessage(`{"expectedTable":"` + oldTable + `","handles":[{"chain":"forward","handle":44}]}`)
	if err := deleteNftRulesWithOps(context.Background(), raw, ops); err == nil {
		t.Fatal("batch delete accepted stale expected table")
	}
}

func TestDeleteNftRulesUsesOneAtomicTransactionForAllHandles(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
	ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
	ops.run = activeTableTestRunner(t, agentTestGeneration, false)
	calls := 0
	ops.runStdin = func(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok || name != "nft" || fmt.Sprint(args) != fmt.Sprint([]string{"-f", "-"}) {
			t.Fatalf("batch command context/name/args invalid: %s %v", name, args)
		}
		want := "delete rule inet " + agentTestGeneration + " forward handle 41\n" +
			"delete rule inet " + agentTestGeneration + " postrouting handle 42\n"
		if stdin != want {
			t.Fatalf("batch stdin=%q, want %q", stdin, want)
		}
		return nil, nil
	}
	raw := json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","handles":[{"chain":"forward","handle":41},{"chain":"postrouting","handle":42}]}`)
	if err := deleteNftRulesWithOps(context.Background(), raw, ops); err != nil {
		t.Fatalf("deleteNftRulesWithOps: %v", err)
	}
	if calls != 1 {
		t.Fatalf("batch mutation calls=%d, want 1", calls)
	}
}

func TestDeleteNftRulesRejectsMalformedDuplicateAndExcessiveRequests(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("resolver ran for invalid batch request")
		return nil, nil
	}
	ops.runStdin = func(context.Context, string, []string, string) ([]byte, error) {
		t.Fatal("mutation ran for invalid batch request")
		return nil, nil
	}
	invalid := []json.RawMessage{
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","unknown":true,"handles":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","expectedTable":"` + agentTestGeneration + `","handles":[]}`),
		json.RawMessage(`{"expectedTable":"flux_panel_g_invalid","handles":[{"chain":"forward","handle":1}]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","handles":[{"chain":"other","handle":1}]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","handles":[{"chain":"forward","handle":0}]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","handles":[{"chain":"forward","handle":1},{"chain":"forward","handle":1}]}`),
	}
	for _, raw := range invalid {
		if err := deleteNftRulesWithOps(context.Background(), raw, ops); err == nil {
			t.Errorf("batch delete accepted %s", raw)
		}
	}
	var excessive strings.Builder
	excessive.WriteString(`{"expectedTable":"` + agentTestGeneration + `","handles":[`)
	for i := 1; i <= nftgeneration.MaxRuleBatchItems+1; i++ {
		if i > 1 {
			excessive.WriteByte(',')
		}
		fmt.Fprintf(&excessive, `{"chain":"forward","handle":%d}`, i)
	}
	excessive.WriteString(`]}`)
	if err := deleteNftRulesWithOps(context.Background(), json.RawMessage(excessive.String()), ops); err == nil {
		t.Fatal("batch delete accepted excessive handles")
	}
}

func TestAddNftRulesBindsGenerationAndUsesOneTransaction(t *testing.T) {
	ruleA := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80`
	ruleB := `add rule inet flux_panel postrouting meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 masquerade`
	makeOps := func(t *testing.T, expectedMutation bool) nftAgentOps {
		ops := defaultNftAgentOps()
		ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
		ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
		ops.run = activeTableTestRunner(t, agentTestGeneration, false)
		ops.runStdin = func(_ context.Context, _ string, _ []string, stdin string) ([]byte, error) {
			if !expectedMutation {
				t.Fatal("add batch mutated stale generation")
			}
			want := strings.Replace(ruleA, "flux_panel", agentTestGeneration, 1) + "\n" + strings.Replace(ruleB, "flux_panel", agentTestGeneration, 1) + "\n"
			if stdin != want {
				t.Fatalf("add batch stdin=%q, want %q", stdin, want)
			}
			return nil, nil
		}
		return ops
	}
	stale := json.RawMessage(`{"expectedTable":"flux_panel_g_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","rules":[` + fmt.Sprintf("%q,%q", ruleA, ruleB) + `]}`)
	if err := addNftRulesWithOps(context.Background(), stale, makeOps(t, false)); err == nil {
		t.Fatal("add batch accepted stale expected table")
	}
	valid := json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","rules":[` + fmt.Sprintf("%q,%q", ruleA, ruleB) + `]}`)
	if err := addNftRulesWithOps(context.Background(), valid, makeOps(t, true)); err != nil {
		t.Fatalf("add batch: %v", err)
	}
}

func TestAddNftRulesRejectsMalformedDuplicateAndExcessiveRequests(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.run = func(context.Context, string, ...string) ([]byte, error) { t.Fatal("resolver called"); return nil, nil }
	rule := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80`
	invalid := []json.RawMessage{
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","unknown":1,"rules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","rules":["unsafe"]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","rules":[` + fmt.Sprintf("%q,%q", rule, rule) + `]}`),
	}
	for _, raw := range invalid {
		if err := addNftRulesWithOps(context.Background(), raw, ops); err == nil {
			t.Errorf("add batch accepted %s", raw)
		}
	}
	var excessive strings.Builder
	excessive.WriteString(`{"expectedTable":"` + agentTestGeneration + `","rules":[`)
	for i := 0; i <= nftgeneration.MaxRuleBatchItems; i++ {
		if i > 0 {
			excessive.WriteByte(',')
		}
		excessive.WriteString(fmt.Sprintf("%q", rule))
	}
	excessive.WriteString(`]}`)
	if err := addNftRulesWithOps(context.Background(), json.RawMessage(excessive.String()), ops); err == nil {
		t.Fatal("add batch accepted excessive rules")
	}
}

func TestReplaceNftRulesPreservesUnselectedRulesInOneAtomicTransaction(t *testing.T) {
	ruleA := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80`
	ruleB := `add rule inet flux_panel forward meta nfproto ipv4 tcp dport 8080 ip daddr 192.0.2.1 ct original proto-dst 18080 ct state new,established,related accept`
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
	ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
	ops.run = activeTableTestRunner(t, agentTestGeneration, false)
	calls := 0
	ops.runStdin = func(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok || name != "nft" || fmt.Sprint(args) != fmt.Sprint([]string{"-f", "-"}) {
			t.Fatalf("replace command context/name/args invalid: %s %v", name, args)
		}
		want := "delete rule inet " + agentTestGeneration + " prerouting handle 41\n" +
			strings.Replace(ruleA, "flux_panel", agentTestGeneration, 1) + "\n" +
			strings.Replace(ruleB, "flux_panel", agentTestGeneration, 1) + "\n"
		if stdin != want {
			t.Fatalf("replace stdin=%q, want %q", stdin, want)
		}
		if strings.Contains(stdin, "handle 42") || strings.Contains(stdin, "flush") {
			t.Fatalf("replace transaction mutates unselected raw rule: %q", stdin)
		}
		return nil, nil
	}
	raw := json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[{"chain":"prerouting","handle":41}],"addRules":[` + fmt.Sprintf("%q,%q", ruleA, ruleB) + `]}`)
	if err := replaceNftRulesWithOps(context.Background(), raw, ops); err != nil {
		t.Fatalf("replaceNftRulesWithOps: %v", err)
	}
	if calls != 1 {
		t.Fatalf("replace mutation calls=%d, want 1", calls)
	}
}

func TestReplaceNftRulesRejectsGenerationChangeWithoutMutation(t *testing.T) {
	ops := defaultNftAgentOps()
	ops.activeMarkerPath = writeAgentTestMarker(t, agentTestGeneration)
	ops.lockPath = filepath.Join(t.TempDir(), "handoff.lock")
	ops.run = activeTableTestRunner(t, agentTestGeneration, false)
	ops.runStdin = func(context.Context, string, []string, string) ([]byte, error) {
		t.Fatal("replace mutation ran after expected generation changed")
		return nil, nil
	}
	raw := json.RawMessage(`{"expectedTable":"flux_panel_g_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","deleteHandles":[{"chain":"forward","handle":44}],"addRules":[]}`)
	if err := replaceNftRulesWithOps(context.Background(), raw, ops); err == nil || !strings.Contains(err.Error(), nftgeneration.RetryableErrorPrefix) {
		t.Fatalf("replace stale generation error=%v", err)
	}
}

func TestReplaceNftRulesRejectsMalformedDuplicateAndExcessiveRequests(t *testing.T) {
	rule := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80`
	ops := defaultNftAgentOps()
	ops.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("resolver ran for invalid replace request")
		return nil, nil
	}
	ops.runStdin = func(context.Context, string, []string, string) ([]byte, error) {
		t.Fatal("mutation ran for invalid replace request")
		return nil, nil
	}
	invalid := []json.RawMessage{
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","unknown":true,"deleteHandles":[],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","expectedTable":"` + agentTestGeneration + `","deleteHandles":[{"chain":"forward","handle":1}],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"flux_panel_g_invalid","deleteHandles":[{"chain":"forward","handle":1}],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[{"chain":"other","handle":1}],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[{"chain":"forward","handle":0}],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[{"chain":"forward","handle":1},{"chain":"forward","handle":1}],"addRules":[]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[],"addRules":["unsafe"]}`),
		json.RawMessage(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[],"addRules":[` + fmt.Sprintf("%q,%q", rule, rule) + `]}`),
	}
	for _, raw := range invalid {
		if err := replaceNftRulesWithOps(context.Background(), raw, ops); err == nil {
			t.Errorf("replace accepted %s", raw)
		}
	}

	var excessive strings.Builder
	excessive.WriteString(`{"expectedTable":"` + agentTestGeneration + `","deleteHandles":[`)
	for i := 1; i <= nftgeneration.MaxRuleBatchItems; i++ {
		if i > 1 {
			excessive.WriteByte(',')
		}
		fmt.Fprintf(&excessive, `{"chain":"forward","handle":%d}`, i)
	}
	excessive.WriteString(`],"addRules":[` + fmt.Sprintf("%q", rule) + `]}`)
	if err := replaceNftRulesWithOps(context.Background(), json.RawMessage(excessive.String()), ops); err == nil {
		t.Fatal("replace accepted excessive combined items")
	}

	oversized := bytes.Repeat([]byte(" "), nftgeneration.MaxRuleBatchBytes+1)
	if err := replaceNftRulesWithOps(context.Background(), json.RawMessage(oversized), ops); err == nil {
		t.Fatal("replace accepted oversized request")
	}
}

func TestApplyNftRulesDoesNotRecursivelyAcquireReporterLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "handoff.lock")
	release, err := nftgeneration.AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	called := false
	err = applyNftRulesWithRunner("/test/apply_rules.sh", func(ctx context.Context, name string, args ...string) ([]byte, error) {
		called = true
		if _, hasDeadline := ctx.Deadline(); hasDeadline {
			t.Fatal("crash-safe apply handoff received a single-nft-command deadline")
		}
		if name != "/test/apply_rules.sh" || len(args) != 0 {
			t.Fatalf("apply command=%s %v", name, args)
		}
		return nil, nil
	})
	if err != nil || !called {
		t.Fatalf("apply=(called %v, err %v), want script invocation without agent lock", called, err)
	}
}

func writeAgentTestMarker(t *testing.T, table string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "active-table")
	if err := nftgeneration.WriteActiveMarker(path, table); err != nil {
		t.Fatal(err)
	}
	return path
}

func activeTableTestRunner(t *testing.T, table string, dormant bool) commandRunner {
	t.Helper()
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "nft" || fmt.Sprint(args) != fmt.Sprint([]string{"-j", "list", "table", "inet", agentTestGeneration}) {
			t.Fatalf("resolve command=%s %v", name, args)
		}
		return activeTableJSON(table, dormant), nil
	}
}

func activeTableJSON(table string, dormant bool) []byte {
	flags := "[]"
	if dormant {
		flags = `["dormant"]`
	}
	return []byte(fmt.Sprintf(`{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"test","json_schema_version":1}},{"table":{"family":"inet","name":%q,"handle":1,"flags":%s}}]}`, table, flags))
}

func activeTableJSONWithoutTable() []byte {
	return []byte(`{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"test","json_schema_version":1}}]}`)
}

func TestUpgradeNodePassesAllowInsecureToDownloader(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantAllow bool
	}{
		{name: "omitted defaults false", payload: `{"baseUrl":"https://panel.example.com"}`},
		{name: "explicit true", payload: `{"baseUrl":"http://panel.example.com","allowInsecure":true}`, wantAllow: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stop := errors.New("stop after observing downloader arguments")
			calls := 0
			err := upgradeNodeWithDownloader(json.RawMessage(tc.payload), func(_ string, _ string, allowInsecure bool) error {
				calls++
				if allowInsecure != tc.wantAllow {
					t.Fatalf("allowInsecure = %v, want %v", allowInsecure, tc.wantAllow)
				}
				return stop
			})
			if !errors.Is(err, stop) {
				t.Fatalf("upgradeNodeWithDownloader error = %v, want sentinel", err)
			}
			if calls != 1 {
				t.Fatalf("downloader calls = %d, want 1", calls)
			}
		})
	}
}

func TestUpgradeNodePrefersLocalPersistedPanelBaseURL(t *testing.T) {
	stop := errors.New("stop after observing downloader URL")
	arch := runtime.GOARCH
	if arch != "arm64" {
		arch = "amd64"
	}
	want := "https://historical-panel.example.com/base/api/v1/node/assets/nft_flow_reporter_" + arch
	err := upgradeNodeFromLocalBaseURLWithDownloader(
		json.RawMessage(`{"baseUrl":"https://new-panel.example.com","allowInsecure":false}`),
		"https://historical-panel.example.com/base/",
		func(rawURL, _ string, _ bool) error {
			if rawURL != want {
				t.Fatalf("download URL = %q, want %q", rawURL, want)
			}
			return stop
		},
	)
	if !errors.Is(err, stop) {
		t.Fatalf("upgrade error = %v, want sentinel", err)
	}
}

func TestUpgradeCommandLogsFailureOnNodeAndKeepsPanelResponse(t *testing.T) {
	var log bytes.Buffer
	restartCalls := 0
	upgradeErr := errors.New("download nft_flow_reporter failed\nforged line")
	executor := &liveCommandExecutor{
		panelBaseURL: "https://panel.example.com/base",
		upgradeRunner: func(raw json.RawMessage, localBaseURL string) error {
			if string(raw) != `{"allowInsecure":false}` || localBaseURL != "https://panel.example.com/base" {
				t.Fatalf("upgrade args raw=%s localBaseURL=%q", raw, localBaseURL)
			}
			return upgradeErr
		},
		restartScheduler: func(string) { restartCalls++ },
		upgradeLog:       &log,
	}
	resp := executor.Execute(context.Background(), commandMessage{
		Type:      "UpgradeNode",
		Data:      json.RawMessage(`{"allowInsecure":false}`),
		RequestID: "upgrade-request\nforged id",
	})
	if resp.Success || resp.Message != upgradeErr.Error() || resp.RequestID != "upgrade-request\nforged id" {
		t.Fatalf("response=%+v", resp)
	}
	if restartCalls != 0 {
		t.Fatalf("restart calls=%d, want 0 after failed upgrade", restartCalls)
	}
	wantLog := "nft node upgrade failed " +
		`stage="install_assets" request_id="upgrade-request\nforged id" error="download nft_flow_reporter failed\nforged line"` + "\n"
	if got := log.String(); got != wantLog {
		t.Fatalf("upgrade log=%q, want %q", got, wantLog)
	}
}

func TestUpgradeCommandSchedulesRestartWithoutFailureLog(t *testing.T) {
	var log bytes.Buffer
	restarted := ""
	executor := &liveCommandExecutor{
		upgradeRunner: func(json.RawMessage, string) error { return nil },
		restartScheduler: func(requestID string) {
			restarted = requestID
		},
		upgradeLog: &log,
	}
	resp := executor.Execute(context.Background(), commandMessage{Type: "UpgradeNode", RequestID: "upgrade-ok"})
	if !resp.Success || restarted != "upgrade-ok" || log.Len() != 0 {
		t.Fatalf("response=%+v restarted=%q log=%q", resp, restarted, log.String())
	}
}

func TestRestartNodeServicesLogsEachFailureBeforeAgentReplacement(t *testing.T) {
	var log bytes.Buffer
	var services []string
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "systemctl" || len(args) != 2 || args[0] != "restart" {
			t.Fatalf("restart command=%s %v", name, args)
		}
		services = append(services, args[1])
		if args[1] == "flux-nftables.service" {
			return []byte("forward start failed\nsee service journal"), errors.New("exit status 1")
		}
		return nil, errors.New("agent restart denied")
	}
	restartNodeServicesWithRunner("restart-request", runner, &log)
	wantServices := []string{"flux-nftables.service", "flux-nftables-agent.service"}
	if fmt.Sprint(services) != fmt.Sprint(wantServices) {
		t.Fatalf("restart services=%v, want %v", services, wantServices)
	}
	got := log.String()
	for _, fragment := range []string{
		`stage="restart_forward_service"`,
		`stage="restart_agent_service"`,
		`request_id="restart-request"`,
		`forward start failed\\nsee service journal`,
		`agent restart denied`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("restart log %q missing %q", got, fragment)
		}
	}
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("restart log contains unexpected physical lines: %q", got)
	}
}

func TestFlowReporterFailureLogIncludesEscapedBoundedOutput(t *testing.T) {
	var log bytes.Buffer
	runFlowReporterOnceWithWriter("https://panel.example.com", "node-secret", func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != flowReporterPath || !reflect.DeepEqual(args, []string{"https://panel.example.com", "node-secret"}) {
			t.Fatalf("reporter command=%s %v", name, args)
		}
		return []byte("discover nft generations: unsupported flag\nforged line"), errors.New("exit status 1")
	}, &log)

	got := log.String()
	for _, fragment := range []string{
		`flow reporter run failed error="exit status 1"`,
		`output="discover nft generations: unsupported flag\nforged line"`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("reporter log %q missing %q", got, fragment)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("reporter log contains unexpected physical lines: %q", got)
	}
}

func TestNodeUpgradeLogFieldsAreBounded(t *testing.T) {
	var log bytes.Buffer
	long := strings.Repeat("x", maxNodeUpgradeLogFieldBytes+1024)
	writeNodeUpgradeFailure(&log, long, long, errors.New(long))
	if !strings.Contains(log.String(), "...(truncated)") {
		t.Fatalf("bounded upgrade log did not mark truncation: bytes=%d", log.Len())
	}
	if log.Len() > 3*(maxNodeUpgradeLogFieldBytes+32) {
		t.Fatalf("bounded upgrade log too large: bytes=%d", log.Len())
	}
}

func TestParseIperf3IntervalLine(t *testing.T) {
	line := "[SUM]   1.00-2.00   sec   112 MBytes   940 Mbits/sec    2"

	interval, ok := parseIperf3IntervalLine(line)
	if !ok {
		t.Fatal("parseIperf3IntervalLine returned ok=false")
	}
	if interval.Stream != "SUM" {
		t.Fatalf("Stream=%q, want SUM", interval.Stream)
	}
	if interval.StartSeconds != 1 || interval.EndSeconds != 2 {
		t.Fatalf("interval %.2f-%.2f, want 1.00-2.00", interval.StartSeconds, interval.EndSeconds)
	}
	if interval.Mbps != 940 {
		t.Fatalf("Mbps=%.2f, want 940.00", interval.Mbps)
	}
	if interval.TransferBytes != 117440512 {
		t.Fatalf("TransferBytes=%d, want 117440512", interval.TransferBytes)
	}
	if interval.Retransmits != 2 {
		t.Fatalf("Retransmits=%d, want 2", interval.Retransmits)
	}
}

func TestParseIperf3TextSummary(t *testing.T) {
	output := `
[SUM]   0.00-10.00  sec  1.09 GBytes   936 Mbits/sec    4             sender
[SUM]   0.00-10.04  sec  1.08 GBytes   924 Mbits/sec                  receiver
`

	summary, err := parseIperf3TextSummary(output)
	if err != nil {
		t.Fatalf("parseIperf3TextSummary returned error: %v", err)
	}
	if summary.SentMbps != 936 {
		t.Fatalf("SentMbps=%.2f, want 936", summary.SentMbps)
	}
	if summary.ReceivedMbps != 924 {
		t.Fatalf("ReceivedMbps=%.2f, want 924", summary.ReceivedMbps)
	}
	if summary.Retransmits != 4 {
		t.Fatalf("Retransmits=%d, want 4", summary.Retransmits)
	}
}

func TestParsePingMetrics(t *testing.T) {
	output := `
64 bytes from 10.211.55.6: icmp_seq=1 ttl=64 time=0.287 ms
64 bytes from 10.211.55.6: icmp_seq=2 ttl=64 time=0.305 ms

--- 10.211.55.6 ping statistics ---
3 packets transmitted, 2 received, 33.3333% packet loss, time 2003ms
rtt min/avg/max/mdev = 0.287/0.296/0.305/0.009 ms
`

	metrics, ok := parsePingMetrics(output)
	if !ok {
		t.Fatal("parsePingMetrics returned ok=false")
	}
	if metrics.LatencyMs != 0.3 {
		t.Fatalf("LatencyMs=%.2f, want 0.30", metrics.LatencyMs)
	}
	if metrics.LossPercent != 33.33 {
		t.Fatalf("LossPercent=%.2f, want 33.33", metrics.LossPercent)
	}
}

func TestFlushConntrackDeletesEntryPortForEachProtocol(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"port":      1002,
		"protocols": []string{"tcp", "udp"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var calls [][]string
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}

	if err := flushConntrackWithRunner(raw, runner); err != nil {
		t.Fatalf("flushConntrackWithRunner returned error: %v", err)
	}

	want := [][]string{
		{"conntrack", "-D", "-p", "tcp", "--dport", "1002"},
		{"conntrack", "-D", "-p", "udp", "--dport", "1002"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls=%v, want %v", calls, want)
	}
	for i := range want {
		if len(calls[i]) != len(want[i]) {
			t.Fatalf("call %d=%v, want %v", i, calls[i], want[i])
		}
		for j := range want[i] {
			if calls[i][j] != want[i][j] {
				t.Fatalf("call %d=%v, want %v", i, calls[i], want[i])
			}
		}
	}
}

func TestValidateAddNftRuleRejectsUnsafeOrUnexpectedCommands(t *testing.T) {
	valid := `add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment "fp:1:2:3"`
	invalid := []string{
		valid + "\rflush ruleset",
		valid + "\nflush ruleset",
		valid + "\x00flush ruleset",
		strings.Replace(valid, " dport ", "\tdport ", 1),
		valid + "; flush ruleset",
		valid + " # ignored",
		valid + " add rule inet flux_panel prerouting tcp dport 1 accept",
		strings.Replace(valid, "inet flux_panel", "inet unexpected", 1),
		strings.Replace(valid, "prerouting", "unexpected", 1),
		strings.Replace(valid, "192.0.2.1:80", "192.0.2.1:0", 1),
		`tcp dport 8080 dnat to 192.0.2.1:80`,
		" " + valid,
		valid + " ",
	}

	for _, rule := range invalid {
		if err := validateAddNftRule(rule); err == nil {
			t.Fatalf("validateAddNftRule(%q) returned nil", rule)
		}

		payload := map[string]string{"rule": rule}
		if rule == `tcp dport 8080 dnat to 192.0.2.1:80` {
			payload["chain"] = "prerouting"
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal rule: %v", err)
		}
		calls := 0
		runner := func(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
			calls++
			return nil, nil
		}
		if err := executeAddNftRule(context.Background(), raw, nftgeneration.LegacyTable, runner); err == nil {
			t.Fatalf("addNftRuleWithRunner(%q) returned nil", rule)
		}
		if calls != 0 {
			t.Fatalf("invalid rule %q invoked runner %d times", rule, calls)
		}
	}
}

func TestValidateAddNftRuleAcceptsSingleAllowlistedRule(t *testing.T) {
	rules := []string{
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment "fp:1:2:3"`,
		`add rule inet flux_panel forward meta nfproto ipv6 udp dport 53 ip6 daddr 2001:db8::1 ct original proto-dst 10053 ct state new,established,related counter accept comment "fp:1:2:3:up"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp sport 80 ip saddr 192.0.2.1 ct original proto-dst 10080 ct state established,related accept comment "fp:1:2:3"`,
		`add rule inet flux_panel postrouting meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 masquerade comment "fp:1:2:3"`,
	}
	for _, rule := range rules {
		if err := validateAddNftRule(rule); err != nil {
			t.Fatalf("validateAddNftRule(%q) returned error: %v", rule, err)
		}
	}
	rule := rules[1]

	raw, err := json.Marshal(map[string]string{"rule": rule})
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	calls := 0
	runner := func(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
		calls++
		if name != "nft" || strings.Join(args, " ") != "-f -" {
			t.Fatalf("runner command=%s %v", name, args)
		}
		if stdin != rule+"\n" {
			t.Fatalf("runner stdin=%q, want %q", stdin, rule+"\n")
		}
		return nil, nil
	}
	if err := executeAddNftRule(context.Background(), raw, nftgeneration.LegacyTable, runner); err != nil {
		t.Fatalf("executeAddNftRule returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls=%d, want 1", calls)
	}
}

func TestValidateAddNftRuleRejectsNonCanonicalTextWithoutRunning(t *testing.T) {
	invalid := []string{
		`add  rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment "fp:1:2:3"`,
		"add rule inet\u00a0flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment \"fp:1:2:3\"",
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 08080 dnat to 192.0.2.1:80 comment "fp:1:2:3"`,
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:080 comment "fp:1:2:3"`,
		`add rule inet flux_panel prerouting meta nfproto ipv6 tcp dport 8080 dnat to [2001:0db8::1]:80 comment "fp:1:2:3"`,
		`add rule inet flux_panel forward meta nfproto ipv6 tcp dport 80 ip6 daddr 2001:0db8::1 ct original proto-dst 8080 ct state new,established,related counter accept comment "fp:1:2:3:up"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct state new,established,related counter accept comment "fp:1:2:3:up"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct original proto-dst 0 ct state new,established,related counter accept comment "fp:1:2:3:up"`,
	}
	for _, rule := range invalid {
		assertNftRuleRejectedWithoutRunner(t, rule)
	}
}

func TestValidateAddNftRuleBindsCommentDirectionToRuleShape(t *testing.T) {
	invalid := []string{
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct original proto-dst 8080 ct state new,established,related counter accept`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct original proto-dst 8080 ct state new,established,related counter accept comment "fp:1:2:3:down"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp sport 80 ip saddr 192.0.2.1 ct original proto-dst 8080 ct state established,related counter accept comment "fp:1:2:3:up"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 ct original proto-dst 8080 ct state new,established,related accept comment "fp:1:2:3:up"`,
		`add rule inet flux_panel forward meta nfproto ipv4 tcp sport 80 ip saddr 192.0.2.1 ct original proto-dst 8080 ct state established,related counter accept comment "fp:1:2:3"`,
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 comment "fp:1:2:3:up"`,
		`add rule inet flux_panel postrouting meta nfproto ipv4 tcp dport 80 ip daddr 192.0.2.1 masquerade comment "fp:1:2:3:down"`,
		`add rule inet flux_panel prerouting meta nfproto ipv4 tcp dport 8080 dnat to 192.0.2.1:80 counter comment "fp:1:2:3"`,
	}
	for _, rule := range invalid {
		assertNftRuleRejectedWithoutRunner(t, rule)
	}
}

func TestValidateAddNftRuleAcceptsGostBuilderShapes(t *testing.T) {
	rules, err := gost.BuildEntryRules(1, 2, 3, "ipv6", "tcp", 8080, netip.MustParseAddr("2001:db8::1"), 80)
	if err != nil {
		t.Fatalf("build representative rules: %v", err)
	}
	if len(rules) != 4 {
		t.Fatalf("builder returned %d rules, want 4", len(rules))
	}
	for _, rule := range rules {
		if err := validateAddNftRule(rule); err != nil {
			t.Fatalf("validate builder rule %q: %v", rule, err)
		}
	}
}

func assertNftRuleRejectedWithoutRunner(t *testing.T, rule string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"rule": rule})
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	calls := 0
	runner := func(context.Context, string, []string, string) ([]byte, error) {
		calls++
		return nil, nil
	}
	if err := executeAddNftRule(context.Background(), raw, nftgeneration.LegacyTable, runner); err == nil {
		t.Fatalf("addNftRuleWithRunner(%q) returned nil", rule)
	}
	if calls != 0 {
		t.Fatalf("invalid rule %q invoked runner %d times", rule, calls)
	}
}

func TestInstallStagedNodeAssetsRollsBackReplacedFiles(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "nft_agent")
	tmp1 := filepath.Join(dir, ".nft_agent.upgrade")
	tmp2 := filepath.Join(dir, ".nft_rule_payload.upgrade")
	missingTarget := filepath.Join(dir, "missing", "nft_rule_payload")

	if err := os.WriteFile(target1, []byte("old-agent"), 0755); err != nil {
		t.Fatalf("write target1: %v", err)
	}
	if err := os.WriteFile(tmp1, []byte("new-agent"), 0755); err != nil {
		t.Fatalf("write tmp1: %v", err)
	}
	if err := os.WriteFile(tmp2, []byte("new-rule"), 0755); err != nil {
		t.Fatalf("write tmp2: %v", err)
	}

	err := installStagedNodeAssets([]stagedNodeAsset{
		{name: "nft_agent", target: target1, tmp: tmp1},
		{name: "nft_rule_payload", target: missingTarget, tmp: tmp2},
	})
	if err == nil {
		t.Fatal("installStagedNodeAssets returned nil error")
	}

	got, readErr := os.ReadFile(target1)
	if readErr != nil {
		t.Fatalf("read target1: %v", readErr)
	}
	if string(got) != "old-agent" {
		t.Fatalf("target1=%q, want old-agent", got)
	}
	if _, statErr := os.Stat(target1 + ".bak"); !os.IsNotExist(statErr) {
		t.Fatalf("backup should be restored/removed, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(tmp2); !os.IsNotExist(statErr) {
		t.Fatalf("failed staged file should be cleaned, stat err=%v", statErr)
	}
}

func TestInstallStagedNodeAssetsBackupFailureKeepsTargetsPresent(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "nft_agent")
	target2 := filepath.Join(dir, "nft_rule_payload")
	tmp1 := filepath.Join(dir, ".nft_agent.upgrade")
	tmp2 := filepath.Join(dir, ".nft_rule_payload.upgrade")
	for path, content := range map[string]string{
		target1: "old-agent", target2: "old-rule", tmp1: "new-agent", tmp2: "new-rule",
	} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	backupErr := errors.New("injected backup failure")
	ops := defaultNodeFileOperations()
	originalOpenFile := ops.openFile
	ops.openFile = func(path string, flag int, perm os.FileMode) (*os.File, error) {
		for _, target := range []string{target1, target2} {
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("target missing during backup phase: %s: %v", target, err)
			}
		}
		if path == target2+".bak" {
			return nil, backupErr
		}
		return originalOpenFile(path, flag, perm)
	}

	err := installStagedNodeAssetsWithFileOps([]stagedNodeAsset{
		{name: "nft_agent", target: target1, tmp: tmp1},
		{name: "nft_rule_payload", target: target2, tmp: tmp2},
	}, ops)
	if !errors.Is(err, backupErr) {
		t.Fatalf("install error = %v, want backup failure", err)
	}
	assertNodeAssetFileState(t, target1, "old-agent")
	assertNodeAssetFileState(t, target2, "old-rule")
	assertNodeAssetPathsAbsent(t, tmp1, tmp2, target1+".bak", target2+".bak")
}

func TestInstallStagedNodeAssetsReplacementFailureRollsBackWithoutMissingWindow(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "nft_agent")
	target2 := filepath.Join(dir, "nft_rule_payload")
	tmp1 := filepath.Join(dir, ".nft_agent.upgrade")
	tmp2 := filepath.Join(dir, ".nft_rule_payload.upgrade")
	for path, content := range map[string]string{
		target1: "old-agent", target2: "old-rule", tmp1: "new-agent", tmp2: "new-rule",
	} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replaceErr := errors.New("injected replacement failure")
	ops := defaultNodeFileOperations()
	originalRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		if oldPath == tmp1 {
			for _, backup := range []string{target1 + ".bak", target2 + ".bak"} {
				if _, err := os.Stat(backup); err != nil {
					t.Fatalf("replacement began before all backups were prepared: %s: %v", backup, err)
				}
			}
		}
		for _, target := range []string{target1, target2} {
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("target missing before atomic rename: %s: %v", target, err)
			}
		}
		if oldPath == tmp2 {
			return replaceErr
		}
		return originalRename(oldPath, newPath)
	}

	err := installStagedNodeAssetsWithFileOps([]stagedNodeAsset{
		{name: "nft_agent", target: target1, tmp: tmp1},
		{name: "nft_rule_payload", target: target2, tmp: tmp2},
	}, ops)
	if !errors.Is(err, replaceErr) {
		t.Fatalf("install error = %v, want replacement failure", err)
	}
	assertNodeAssetFileState(t, target1, "old-agent")
	assertNodeAssetFileState(t, target2, "old-rule")
	assertNodeAssetPathsAbsent(t, tmp1, tmp2, target1+".bak", target2+".bak")
}

func TestInstallStagedNodeAssetsReportsRollbackCleanupAndSyncErrors(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "nft_agent")
	target2 := filepath.Join(dir, "nft_rule_payload")
	tmp1 := filepath.Join(dir, ".nft_agent.upgrade")
	tmp2 := filepath.Join(dir, ".nft_rule_payload.upgrade")
	for path, content := range map[string]string{
		target1: "old-agent", target2: "old-rule", tmp1: "new-agent", tmp2: "new-rule",
	} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replaceErr := errors.New("replace failed")
	rollbackErr := errors.New("rollback failed")
	cleanupErr := errors.New("cleanup failed")
	syncErr := errors.New("sync failed")
	ops := defaultNodeFileOperations()
	originalRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		switch oldPath {
		case tmp2:
			return replaceErr
		case target1 + ".bak":
			return rollbackErr
		default:
			return originalRename(oldPath, newPath)
		}
	}
	originalRemove := ops.remove
	ops.remove = func(path string) error {
		if path == tmp2 {
			return cleanupErr
		}
		return originalRemove(path)
	}
	syncCalls := 0
	originalSyncDir := ops.syncDir
	ops.syncDir = func(path string) error {
		syncCalls++
		if syncCalls >= 4 {
			return syncErr
		}
		return originalSyncDir(path)
	}

	err := installStagedNodeAssetsWithFileOps([]stagedNodeAsset{
		{name: "nft_agent", target: target1, tmp: tmp1},
		{name: "nft_rule_payload", target: target2, tmp: tmp2},
	}, ops)
	for _, want := range []error{replaceErr, rollbackErr, cleanupErr, syncErr} {
		if !errors.Is(err, want) {
			t.Fatalf("install error = %v, want joined %v", err, want)
		}
	}
	assertNodeAssetFileState(t, target1+".bak", "old-agent")
}

func TestInstallStagedNodeAssetsSuccessLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "nft_agent")
	target2 := filepath.Join(dir, "nft_rule_payload")
	tmp1 := filepath.Join(dir, ".nft_agent.upgrade")
	tmp2 := filepath.Join(dir, ".nft_rule_payload.upgrade")
	for path, content := range map[string]string{
		target1: "old-agent", target2: "old-rule", tmp1: "new-agent", tmp2: "new-rule",
	} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := installStagedNodeAssets([]stagedNodeAsset{
		{name: "nft_agent", target: target1, tmp: tmp1},
		{name: "nft_rule_payload", target: target2, tmp: tmp2},
	}); err != nil {
		t.Fatalf("install staged assets: %v", err)
	}
	assertNodeAssetFileState(t, target1, "new-agent")
	assertNodeAssetFileState(t, target2, "new-rule")
	assertNodeAssetPathsAbsent(t, tmp1, tmp2, target1+".bak", target2+".bak")
}

func assertNodeAssetFileState(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertNodeAssetPathsAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("path %s should be absent, stat err=%v", path, err)
		}
	}
}
