package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testGenerationA   = "flux_panel_g_00112233445566778899aabbccddeeff"
	testGenerationB   = "flux_panel_g_ffeeddccbbaa99887766554433221100"
	testCanonicalRule = `add rule inet flux_panel forward meta nfproto ipv4 tcp dport 80 counter accept`
)

type recordedNftCommand struct {
	args        []string
	transaction string
	mode        os.FileMode
}

func TestRuntimeStageBuildsOneDormantTransactionWithRewrittenRules(t *testing.T) {
	t.Parallel()

	var calls []recordedNftCommand
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		calls = append(calls, readRecordedCommand(t, args))
		return nil
	})
	rules := []string{
		testCanonicalRule,
		`add rule inet flux_panel postrouting meta nfproto ipv4 masquerade`,
	}
	if err := runtime.Stage(context.Background(), testGenerationA, rules); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args[:1], []string{"-f"}) {
		t.Fatalf("calls=%+v, want one nft -f transaction", calls)
	}
	if calls[0].mode.Perm() != 0o600 {
		t.Fatalf("transaction mode=%o, want 0600", calls[0].mode.Perm())
	}
	want := "create table inet " + testGenerationA + " { flags dormant; }\n" +
		"add chain inet " + testGenerationA + " prerouting { type nat hook prerouting priority dstnat; policy accept; }\n" +
		"add chain inet " + testGenerationA + " postrouting { type nat hook postrouting priority srcnat; policy accept; }\n" +
		"add chain inet " + testGenerationA + " forward { type filter hook forward priority filter; policy accept; }\n" +
		strings.Replace(testCanonicalRule, "flux_panel", testGenerationA, 1) + "\n" +
		"add rule inet " + testGenerationA + " postrouting meta nfproto ipv4 masquerade\n"
	if calls[0].transaction != want {
		t.Fatalf("stage transaction:\n%s\nwant:\n%s", calls[0].transaction, want)
	}
	assertNoTempTransactions(t, runtime.tempDir)
}

func TestRuntimeStageRejectsRuleBeforeExecutingAnything(t *testing.T) {
	t.Parallel()

	called := false
	runtime := newTestExecRuntime(t, func(context.Context, string, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	})
	err := runtime.Stage(context.Background(), testGenerationA, []string{testCanonicalRule, "flush ruleset"})
	if err == nil || called {
		t.Fatalf("Stage error=%v called=%v", err, called)
	}
}

func TestSwitchIsOneRuntimeTransaction(t *testing.T) {
	t.Parallel()

	var calls []recordedNftCommand
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		calls = append(calls, readRecordedCommand(t, args))
		return nil
	})
	if err := runtime.Switch(context.Background(), "flux_panel", testGenerationA); err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("command count=%d, want one", len(calls))
	}
	want := "add table inet flux_panel { flags dormant; }\n" +
		"add table inet " + testGenerationA + "\n"
	if calls[0].transaction != want {
		t.Fatalf("switch transaction=%q, want %q", calls[0].transaction, want)
	}
	if strings.Contains(calls[0].transaction, "delete") {
		t.Fatal("Switch transaction deletes a generation")
	}
}

func TestRuntimeSwitchRejectsInvalidOrIdenticalTablesWithoutExecution(t *testing.T) {
	t.Parallel()

	called := false
	runtime := newTestExecRuntime(t, func(context.Context, string, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	})
	for _, pair := range [][2]string{{"flux_panel", "flux_panel"}, {"bad", testGenerationA}, {"flux_panel", "bad"}} {
		if err := runtime.Switch(context.Background(), pair[0], pair[1]); err == nil {
			t.Errorf("Switch(%q,%q) returned nil", pair[0], pair[1])
		}
	}
	if called {
		t.Fatal("invalid switch executed nft")
	}
}

func TestDiscoveryRejectsTwoActiveFluxTables(t *testing.T) {
	t.Parallel()

	runtime := runtimeReturningJSON(t, nftTablesJSON(
		tableJSON("inet", "flux_panel", nil),
		tableJSON("inet", testGenerationA, nil),
	))
	if _, err := runtime.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("Discover error=%v, want active ambiguity", err)
	}
}

func TestDiscoveryRejectsTwoDormantOrMalformedOwnedTables(t *testing.T) {
	t.Parallel()

	for _, output := range []string{
		nftTablesJSON(tableJSON("inet", "flux_panel", []string{"dormant"}), tableJSON("inet", testGenerationA, []string{"dormant"})),
		nftTablesJSON(tableJSON("inet", "flux_panel_g_not-a-generation", nil)),
		nftTablesJSON(tableJSON("ip", "flux_panel", nil)),
	} {
		runtime := runtimeReturningJSON(t, output)
		if _, err := runtime.Discover(context.Background()); err == nil {
			t.Fatalf("Discover accepted ambiguous owned tables: %s", output)
		}
	}
}

func TestDiscoveryUsesStrictMachineJSONAndIgnoresSimilarThirdPartyTable(t *testing.T) {
	t.Parallel()

	output := `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},` +
		tableJSON("inet", "flux_panel_backup", nil) + `,{"table":{"family":"inet","name":"flux_panel","handle":1,"flags":[],"comment":"managed"}},` +
		tableJSON("inet", testGenerationA, []string{"dormant"}) + `]}`
	runtime := runtimeReturningJSON(t, output)
	got, err := runtime.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []GenerationTable{{Name: "flux_panel"}, {Name: testGenerationA, Dormant: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables=%+v, want %+v", got, want)
	}

	for _, invalid := range []string{
		`{}`,
		`{"nftables":null}`,
		`{"nftables":[{"table":{}}]}`,
		`{"nftables":[{"table":{"family":"unknown","name":"third_party"}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"flux_panel","flags":null}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"flux_panel","handle":null}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"flux_panel","comment":null}}]}`,
		`{"nftables":[{"metainfo":{"version":"","release_name":"name","json_schema_version":1}}]}`,
		`{"nftables":[{"metainfo":{"version":"1","release_name":"name","json_schema_version":0}}]}`,
		`{"nftables":[],"unknown":true}`,
		`{"nftables":[{"table":{"family":"inet","name":"flux_panel","mystery":1}}]}`,
		`{"nftables":[{"table":{"family":"inet","name":"flux_panel","flags":["owner"]}}]}`,
		`{"nftables":[{"metainfo":null,"table":{"family":"inet","name":"flux_panel"}}]}`,
		`{"nftables":[{"metainfo":{"version":"1","release_name":"name","json_schema_version":1},"table":null}]}`,
		`{"nftables":[]} trailing`,
	} {
		if _, err := runtimeReturningJSON(t, invalid).Discover(context.Background()); err == nil {
			t.Errorf("Discover accepted invalid JSON %q", invalid)
		}
	}
}

func TestDiscoveryAcceptsNftTableFlagsAsStringOrArray(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		flags string
	}{
		{name: "Debian 13 nftables 1.1.3 string", flags: `"dormant"`},
		{name: "legacy array", flags: `["dormant"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			output := `{"nftables":[` +
				`{"metainfo":{"version":"1.1.3","release_name":"Commodore Bullmoose #4","json_schema_version":1}},` +
				`{"table":{"family":"inet","name":"filter","handle":1}},` +
				`{"table":{"family":"inet","name":"flux_panel","handle":8}},` +
				`{"table":{"family":"inet","name":"` + testGenerationA + `","handle":9,"flags":` + test.flags + `}}]}`
			got, err := runtimeReturningJSON(t, output).Discover(context.Background())
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := []GenerationTable{{Name: "flux_panel"}, {Name: testGenerationA, Dormant: true}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tables=%+v, want %+v", got, want)
			}
		})
	}
}

func TestDiscoveryAcceptsDebian12DormantReleaseNameFlagQuirk(t *testing.T) {
	t.Parallel()

	metainfo := `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}}`
	for _, test := range []struct {
		name    string
		output  string
		dormant bool
	}{
		{
			name: "Debian 12 nftables 1.0.6 inventory layout",
			output: `{"nftables":[` +
				`{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}},` +
				`{"table":{"family":"inet","name":"filter","handle":1}},` +
				`{"table":{"family":"inet","name":"` + testGenerationA + `","handle":2,"flags":["Lester Gooch #5"]}}]}`,
			dormant: true,
		},
		{
			name: "release-only string with metainfo after table",
			output: nftTablesJSON(
				tableJSONWithRawFlags("inet", testGenerationA, `"Lester Gooch #5"`),
				metainfo,
			),
			dormant: true,
		},
		{
			name: "explicit dormant paired with release name",
			output: nftTablesJSON(
				metainfo,
				tableJSONWithRawFlags("inet", testGenerationA, `["dormant","Lester Gooch #5"]`),
			),
			dormant: true,
		},
		{
			name:    "standard dormant array",
			output:  nftTablesJSON(metainfo, tableJSON("inet", testGenerationA, []string{"dormant"})),
			dormant: true,
		},
		{
			name:    "standard dormant string",
			output:  nftTablesJSON(metainfo, tableJSONWithRawFlags("inet", testGenerationA, `"dormant"`)),
			dormant: true,
		},
		{
			name:   "active without flags",
			output: nftTablesJSON(metainfo, tableJSON("inet", testGenerationA, nil)),
		},
		{
			name:   "active with empty flags",
			output: nftTablesJSON(metainfo, tableJSONWithRawFlags("inet", testGenerationA, `[]`)),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := runtimeReturningJSON(t, test.output).Discover(context.Background())
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := []GenerationTable{{Name: testGenerationA, Dormant: test.dormant}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tables=%+v, want %+v", got, want)
			}
		})
	}
}

func TestDiscoveryAcceptsDebian11DormantReleaseNameFlagQuirk(t *testing.T) {
	t.Parallel()

	metainfo := `{"metainfo":{"version":"0.9.8","release_name":"E.D.S.","json_schema_version":1}}`
	output := nftTablesJSON(
		metainfo,
		tableJSONWithRawFlags("inet", testGenerationA, `"E.D.S."`),
		tableJSON("inet", testGenerationB, nil),
	)
	got, err := runtimeReturningJSON(t, output).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []GenerationTable{{Name: testGenerationA, Dormant: true}, {Name: testGenerationB}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables=%+v, want %+v", got, want)
	}
}

func TestDiscoveryFallsBackToDebian12TextForCorruptedDormantFlag(t *testing.T) {
	t.Parallel()

	metainfo := `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}}`
	for _, corruptFlag := range []string{testGenerationB, "E.D.S."} {
		corruptFlag := corruptFlag
		t.Run(corruptFlag, func(t *testing.T) {
			t.Parallel()

			inventory := nftTablesJSON(
				metainfo,
				tableJSON("inet", "filter", nil),
				tableJSONWithRawFlags("inet", testGenerationA, fmt.Sprintf("%q", corruptFlag)),
				tableJSON("inet", testGenerationB, nil),
			)
			var textTables []string
			runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
				switch {
				case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
					_, _ = io.WriteString(stdout, inventory)
					return nil
				case len(args) == 4 && args[0] == "list" && args[1] == "table" && args[2] == "inet":
					table := args[3]
					textTables = append(textTables, table)
					dormant := table == testGenerationA
					_, _ = io.WriteString(stdout, nftTableTextFixture(table, dormant))
					return nil
				default:
					return fmt.Errorf("unexpected args %v", args)
				}
			})

			got, err := runtime.Discover(context.Background())
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			want := []GenerationTable{{Name: testGenerationA, Dormant: true}, {Name: testGenerationB}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tables=%+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(textTables, []string{testGenerationA, testGenerationB}) {
				t.Fatalf("text verification tables=%v", textTables)
			}
		})
	}
}

func TestDiscoveryFallsBackToDebian11TextForCorruptedDormantFlag(t *testing.T) {
	t.Parallel()

	metainfo := `{"metainfo":{"version":"0.9.8","release_name":"E.D.S.","json_schema_version":1}}`
	inventory := nftTablesJSON(
		metainfo,
		tableJSONWithRawFlags("inet", testGenerationA, fmt.Sprintf("%q", testGenerationB)),
		tableJSON("inet", testGenerationB, nil),
	)
	var textTables []string
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		switch {
		case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
			_, _ = io.WriteString(stdout, inventory)
			return nil
		case len(args) == 4 && args[0] == "list" && args[1] == "table" && args[2] == "inet":
			table := args[3]
			textTables = append(textTables, table)
			_, _ = io.WriteString(stdout, nftTableTextFixture(table, table == testGenerationA))
			return nil
		default:
			return fmt.Errorf("unexpected args %v", args)
		}
	})

	got, err := runtime.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := []GenerationTable{{Name: testGenerationA, Dormant: true}, {Name: testGenerationB}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables=%+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(textTables, []string{testGenerationA, testGenerationB}) {
		t.Fatalf("text verification tables=%v", textTables)
	}
}

func TestDebian12CorruptedFlagFallbackFailsClosedOnAmbiguousText(t *testing.T) {
	t.Parallel()

	metainfo := `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}}`
	inventory := nftTablesJSON(
		metainfo,
		tableJSONWithRawFlags("inet", testGenerationA, fmt.Sprintf("%q", testGenerationB)),
		tableJSON("inet", testGenerationB, nil),
	)
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		switch {
		case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
			_, _ = io.WriteString(stdout, inventory)
			return nil
		case len(args) == 4 && args[0] == "list" && args[1] == "table" && args[2] == "inet":
			_, _ = fmt.Fprintf(stdout, "table inet %s {\n\tflags owner\n\tchain forward {\n\t}\n}\n", args[3])
			return nil
		default:
			return fmt.Errorf("unexpected args %v", args)
		}
	})
	if _, err := runtime.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected top-level statement") {
		t.Fatalf("Discover ambiguous text error=%v", err)
	}
}

func TestCorruptedFlagFallbackRequiresExactDebian12Metadata(t *testing.T) {
	t.Parallel()

	inventory := nftTablesJSON(
		`{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}}`,
		tableJSONWithRawFlags("inet", testGenerationA, fmt.Sprintf("%q", testGenerationB)),
	)
	textCalled := false
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
			_, _ = io.WriteString(stdout, inventory)
			return nil
		}
		textCalled = true
		return nil
	})
	if _, err := runtime.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported flag") {
		t.Fatalf("Discover wrong-version error=%v", err)
	}
	if textCalled {
		t.Fatal("wrong nft version reached text fallback")
	}
}

func TestDebian11ReleaseNameFlagCompatibilityRequiresExactMetadata(t *testing.T) {
	t.Parallel()

	for _, metainfo := range []*nftMetainfo{
		nil,
		{Version: "0.9.9", ReleaseName: "E.D.S.", JSONSchemaVersion: 1},
		{Version: "0.9.8", ReleaseName: "different", JSONSchemaVersion: 1},
		{Version: "0.9.8", ReleaseName: "E.D.S.", JSONSchemaVersion: 2},
	} {
		if _, err := classifyNftTableFlags([]string{"E.D.S."}, metainfo); err == nil {
			t.Fatalf("accepted E.D.S. flag with metainfo=%#v", metainfo)
		}
	}
	if dormant, err := classifyNftTableFlags([]string{"E.D.S."}, &nftMetainfo{
		Version: "0.9.8", ReleaseName: "E.D.S.", JSONSchemaVersion: 1,
	}); err != nil || !dormant {
		t.Fatalf("exact Debian 11 metadata dormant=%v err=%v", dormant, err)
	}
}

func TestDiscoveryRejectsUnsafeDebian12ReleaseNameFlagCombinations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		metainfo string
		flags    string
	}{
		{
			name:     "duplicate release name",
			metainfo: `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}}`,
			flags:    `["Lester Gooch #5","Lester Gooch #5"]`,
		},
		{
			name:     "unknown additional flag",
			metainfo: `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":1}}`,
			flags:    `["Lester Gooch #5","owner"]`,
		},
		{
			name:     "wrong nft version",
			metainfo: `{"metainfo":{"version":"1.0.9","release_name":"Lester Gooch #5","json_schema_version":1}}`,
			flags:    `["Lester Gooch #5"]`,
		},
		{
			name:     "wrong release name",
			metainfo: `{"metainfo":{"version":"1.0.6","release_name":"Old Doc Yak","json_schema_version":1}}`,
			flags:    `["Lester Gooch #5"]`,
		},
		{
			name:     "wrong schema version",
			metainfo: `{"metainfo":{"version":"1.0.6","release_name":"Lester Gooch #5","json_schema_version":2}}`,
			flags:    `["Lester Gooch #5"]`,
		},
		{
			name:  "missing metainfo",
			flags: `["Lester Gooch #5"]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			objects := []string{tableJSONWithRawFlags("inet", testGenerationA, test.flags)}
			if test.metainfo != "" {
				objects = append([]string{test.metainfo}, objects...)
			}
			if _, err := runtimeReturningJSON(t, nftTablesJSON(objects...)).Discover(context.Background()); err == nil {
				t.Fatalf("Discover accepted flags=%s metainfo=%s", test.flags, test.metainfo)
			}
		})
	}
}

func TestDiscoveryRejectsInvalidStringOrArrayTableFlags(t *testing.T) {
	t.Parallel()

	for _, flags := range []string{
		`null`,
		`1`,
		`true`,
		`{}`,
		`""`,
		`"owner"`,
		`["dormant","dormant"]`,
		`[null]`,
		`["dormant",1]`,
	} {
		output := `{"nftables":[{"table":{"family":"inet","name":"flux_panel","flags":` + flags + `}}]}`
		if _, err := runtimeReturningJSON(t, output).Discover(context.Background()); err == nil {
			t.Errorf("Discover accepted invalid flags %s", flags)
		}
	}
}

func TestRuntimeCapabilityProbeVerifiesEveryTransitionAndPreservesExistingTables(t *testing.T) {
	t.Parallel()

	probeName := "flux_panel_g_00000000000000000000000000000000"
	states := []string{
		nftTablesJSON(tableJSON("inet", "flux_panel", nil)),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil), tableJSONWithRawFlags("inet", probeName, `"dormant"`)),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil), tableJSON("inet", probeName, nil)),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil), tableJSON("inet", probeName, []string{"dormant"})),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil)),
	}
	var transactions []string
	listIndex := 0
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
			_, _ = io.WriteString(stdout, states[listIndex])
			listIndex++
			return nil
		}
		transactions = append(transactions, readRecordedCommand(t, args).transaction)
		return nil
	})
	runtime.random = bytes.NewReader(make([]byte, 16))
	if err := runtime.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if listIndex != len(states) {
		t.Fatalf("verified states=%d, want %d", listIndex, len(states))
	}
	want := []string{
		"create table inet " + probeName + " { flags dormant; }\nadd chain inet " + probeName + " forward { type filter hook forward priority filter; policy accept; }\n",
		"add table inet " + probeName + "\n",
		"add table inet " + probeName + " { flags dormant; }\n",
		"delete table inet " + probeName + "\n",
	}
	if !reflect.DeepEqual(transactions, want) {
		t.Fatalf("probe transactions=%q, want %q", transactions, want)
	}
	for _, transaction := range transactions {
		if strings.Contains(transaction, "flux_panel {") {
			t.Fatalf("probe mutated existing active table: %q", transaction)
		}
	}
}

func TestRuntimeProbeSupportsDebian12EmptyDormantJSON(t *testing.T) {
	t.Parallel()
	probeName := "flux_panel_g_00000000000000000000000000000000"
	state, jsonReads, textReads := 0, 0, 0
	wantTransactions := []string{
		"create table inet " + probeName + " { flags dormant; }\nadd chain inet " + probeName + " forward { type filter hook forward priority filter; policy accept; }\n",
		activeTableTransaction(probeName),
		dormantTableTransaction(probeName),
		"delete table inet " + probeName + "\n",
	}
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		switch {
		case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
			jsonReads++
			if state == 1 || state == 3 {
				return nil // 复现临时 dormant 表存在时成功退出但无 JSON 输出。
			}
			entries := []string{tableJSON("inet", "filter", nil), tableJSON("inet", testGenerationA, nil)}
			if state == 2 {
				entries = append(entries, tableJSON("inet", probeName, nil))
			}
			_, _ = io.WriteString(stdout, nftTablesJSON(entries...))
		case reflect.DeepEqual(args, []string{"--version"}):
			_, _ = io.WriteString(stdout, "nftables v1.0.6 (Lester Gooch #5)\n")
		case reflect.DeepEqual(args, []string{"list", "tables"}):
			_, _ = fmt.Fprintf(stdout, "table inet filter\ntable inet %s\ntable inet %s\n", testGenerationA, probeName)
		case len(args) == 4 && reflect.DeepEqual(args[:3], []string{"list", "table", "inet"}):
			textReads++
			if args[3] != probeName && args[3] != testGenerationA {
				t.Fatalf("unexpected table read: %v", args)
			}
			_, _ = io.WriteString(stdout, nftTableTextFixture(args[3], args[3] == probeName))
		default:
			transaction := readRecordedCommand(t, args).transaction
			if state >= len(wantTransactions) || transaction != wantTransactions[state] {
				t.Fatalf("unexpected transaction at state %d: %q", state, transaction)
			}
			state++
		}
		return nil
	})
	runtime.random = bytes.NewReader(make([]byte, 16))
	if err := runtime.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state != 4 || jsonReads != 5 || textReads != 4 {
		t.Fatalf("state=%d JSON reads=%d text reads=%d", state, jsonReads, textReads)
	}
}

func TestEmptyJSONCompatibilityFailsClosed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, json, version, inventory, tableText, wantErr string
		commandErr                                         string
	}{
		{name: "unknown version", version: "nftables v1.0.9 (Old Doc Yak)", wantErr: "requires nftables"},
		{name: "wrong release", version: "nftables v1.0.6 (other)", wantErr: "requires nftables"},
		{name: "version failure", commandErr: "--version", wantErr: "read nft version"},
		{name: "inventory failure", commandErr: "list tables", wantErr: "list compatible nft tables"},
		{name: "empty text inventory", wantErr: "empty nft text table inventory"},
		{name: "malformed inventory", inventory: "table inet", wantErr: "invalid nft text inventory"},
		{name: "wrong family", inventory: "table ip " + testGenerationA, wantErr: "unexpected family"},
		{name: "invalid owned name", inventory: "table inet flux_panel_g_bad", wantErr: "ownership"},
		{name: "duplicate owned table", inventory: "table inet " + testGenerationA + "\ntable inet " + testGenerationA, wantErr: "duplicate"},
		{name: "table read failure", inventory: "table inet " + testGenerationA, commandErr: "list table inet " + testGenerationA, wantErr: "text state"},
		{name: "empty table", inventory: "table inet " + testGenerationA, wantErr: "empty nft table text"},
		{name: "wrong table", inventory: "table inet " + testGenerationA, tableText: nftTableTextFixture(testGenerationB, true), wantErr: "unexpected header"},
		{name: "unknown flag", inventory: "table inet " + testGenerationA, tableText: strings.Replace(nftTableTextFixture(testGenerationA, true), "flags dormant", "flags owner", 1), wantErr: "unexpected top-level statement"},
		{name: "truncated JSON", json: `{"nftables":`, wantErr: "decode nft table inventory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
				if strings.Join(args, " ") == test.commandErr {
					return errors.New("simulated command failure")
				}
				if test.json != "" && !reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
					t.Fatalf("nonempty malformed JSON must not use fallback: %v", args)
				}
				switch {
				case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
					_, _ = io.WriteString(stdout, test.json)
				case reflect.DeepEqual(args, []string{"--version"}):
					version := test.version
					if version == "" {
						version = "nftables v1.0.6 (Lester Gooch #5)"
					}
					_, _ = io.WriteString(stdout, version)
				case reflect.DeepEqual(args, []string{"list", "tables"}):
					if test.version != "" {
						t.Fatal("unsupported version reached text fallback")
					}
					_, _ = io.WriteString(stdout, test.inventory)
				case reflect.DeepEqual(args, []string{"list", "table", "inet", testGenerationA}):
					_, _ = io.WriteString(stdout, test.tableText)
				default:
					t.Fatalf("unexpected command: %v", args)
				}
				return nil
			})
			if _, err := runtime.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Discover error=%v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestEmptyJSONCompatibilityPreservesEmptyOwnedInventory(t *testing.T) {
	t.Parallel()
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		switch strings.Join(args, " ") {
		case "-j list tables":
			_, _ = io.WriteString(stdout, " \n\t")
		case "--version":
			_, _ = io.WriteString(stdout, "nftables v1.0.6 (Lester Gooch #5)\n")
		case "list tables":
			_, _ = io.WriteString(stdout, "table inet filter\n")
		default:
			t.Fatalf("unexpected command: %v", args)
		}
		return nil
	})
	// 与正常 JSON 路径保持一致，确保探测前后无自有表时不会因 nil/空切片误报变化。
	if err := runtime.verifyProbeState(context.Background(), []GenerationTable{}, testGenerationA, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCapabilityProbeSupportsKnownReleaseNameFlagQuirks(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		version     string
		releaseName string
	}{
		{name: "Debian 11 nftables", version: "0.9.8", releaseName: "E.D.S."},
		{name: "Debian 12 nftables", version: "1.0.6", releaseName: "Lester Gooch #5"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			probeName := "flux_panel_g_00000000000000000000000000000000"
			metainfo := fmt.Sprintf(`{"metainfo":{"version":%q,"release_name":%q,"json_schema_version":1}}`, test.version, test.releaseName)
			base := tableJSON("inet", "filter", nil)
			states := []string{
				nftTablesJSON(metainfo, base),
				nftTablesJSON(metainfo, base, tableJSONWithRawFlags("inet", probeName, fmt.Sprintf("[%q]", test.releaseName))),
				nftTablesJSON(metainfo, base, tableJSON("inet", probeName, nil)),
				nftTablesJSON(metainfo, base, tableJSONWithRawFlags("inet", probeName, fmt.Sprintf("[%q]", test.releaseName))),
				nftTablesJSON(metainfo, base),
			}
			listIndex := 0
			runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
				if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
					if listIndex >= len(states) {
						return errors.New("unexpected extra nft inventory request")
					}
					_, _ = io.WriteString(stdout, states[listIndex])
					listIndex++
					return nil
				}
				_ = readRecordedCommand(t, args)
				return nil
			})
			runtime.random = bytes.NewReader(make([]byte, 16))
			if err := runtime.Probe(context.Background()); err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if listIndex != len(states) {
				t.Fatalf("verified states=%d, want %d", listIndex, len(states))
			}
		})
	}
}

func TestRuntimeProbeRejectsAmbiguousKnownQuirkTextFallbackAndCleansUp(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		version     string
		releaseName string
	}{
		{name: "Debian 11 nftables", version: "0.9.8", releaseName: "E.D.S."},
		{name: "Debian 12 nftables", version: "1.0.6", releaseName: "Lester Gooch #5"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			probeName := "flux_panel_g_00000000000000000000000000000000"
			metainfo := fmt.Sprintf(`{"metainfo":{"version":%q,"release_name":%q,"json_schema_version":1}}`, test.version, test.releaseName)
			base := tableJSON("inet", "filter", nil)
			states := []string{
				nftTablesJSON(metainfo, base),
				nftTablesJSON(metainfo, base, tableJSONWithRawFlags("inet", probeName, fmt.Sprintf("[%q,%q]", test.releaseName, "owner"))),
				nftTablesJSON(metainfo, base),
			}
			listIndex := 0
			var transactions []string
			runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
				if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
					if listIndex >= len(states) {
						return errors.New("unexpected extra nft inventory request")
					}
					_, _ = io.WriteString(stdout, states[listIndex])
					listIndex++
					return nil
				}
				if len(args) == 4 && args[0] == "list" && args[1] == "table" && args[2] == "inet" {
					_, _ = fmt.Fprintf(stdout, "table inet %s {\n\tflags owner\n\tchain forward {\n\t}\n}\n", args[3])
					return nil
				}
				transactions = append(transactions, readRecordedCommand(t, args).transaction)
				return nil
			})
			runtime.random = bytes.NewReader(make([]byte, 16))
			err := runtime.Probe(context.Background())
			if err == nil || !strings.Contains(err.Error(), `unexpected top-level statement "flags owner"`) {
				t.Fatalf("Probe error=%v, want ambiguous text rejection", err)
			}
			if listIndex != len(states) {
				t.Fatalf("verified states=%d, want %d", listIndex, len(states))
			}
			if got := transactions[len(transactions)-1]; got != "delete table inet "+probeName+"\n" {
				t.Fatalf("last probe transaction=%q, want cleanup delete", got)
			}
		})
	}
}

func TestRuntimeProbeFailureCleansUpAndReportsUncertainCleanup(t *testing.T) {
	t.Parallel()

	probeName := "flux_panel_g_00000000000000000000000000000000"
	states := []string{
		nftTablesJSON(tableJSON("inet", "flux_panel", nil)),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil), tableJSON("inet", probeName, []string{"dormant"})),
		nftTablesJSON(tableJSON("inet", "flux_panel", nil)),
	}
	listIndex := 0
	var transactions []string
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
			_, _ = io.WriteString(stdout, states[listIndex])
			listIndex++
			return nil
		}
		transaction := readRecordedCommand(t, args).transaction
		transactions = append(transactions, transaction)
		if transaction == "add table inet "+probeName+"\n" {
			return errors.New("activate unsupported")
		}
		return nil
	})
	runtime.random = bytes.NewReader(make([]byte, 16))
	err := runtime.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "activate unsupported") {
		t.Fatalf("Probe error=%v", err)
	}
	if got := transactions[len(transactions)-1]; got != "delete table inet "+probeName+"\n" {
		t.Fatalf("last probe transaction=%q, want cleanup delete", got)
	}

	uncertainListCalls := 0
	uncertain := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
			uncertainListCalls++
			if uncertainListCalls > 1 {
				return errors.New("cleanup inventory unavailable")
			}
			_, _ = io.WriteString(stdout, states[0])
			return nil
		}
		return errors.New("create state unknown")
	})
	uncertain.random = bytes.NewReader(make([]byte, 16))
	if err := uncertain.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("Probe uncertain cleanup error=%v", err)
	}
}

func TestRuntimeBoundsContextOutputAndAlwaysRemovesTransactionFile(t *testing.T) {
	t.Parallel()

	runtime := newTestExecRuntime(t, func(ctx context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if _, err := io.WriteString(stderr, strings.Repeat("x", 33)); err != nil {
			return err
		}
		return nil
	})
	runtime.maxOutputBytes = 32
	if err := runtime.Delete(context.Background(), testGenerationA); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("Delete output-limit error=%v", err)
	}
	assertNoTempTransactions(t, runtime.tempDir)

	runtime.command = func(ctx context.Context, _ string, _ []string, stdout, stderr io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	}
	runtime.timeout = 10 * time.Millisecond
	if err := runtime.Delete(context.Background(), testGenerationA); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("Delete timeout error=%v", err)
	}
	assertNoTempTransactions(t, runtime.tempDir)
}

func TestRuntimeValidatesReadAndDeleteTargetsBeforeExecution(t *testing.T) {
	t.Parallel()

	called := false
	runtime := newTestExecRuntime(t, func(context.Context, string, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	})
	if _, err := runtime.ReadCounters(context.Background(), "flux_panel; flush ruleset"); err == nil {
		t.Fatal("ReadCounters accepted invalid table")
	}
	if err := runtime.Delete(context.Background(), "flux_panel; flush ruleset"); err == nil {
		t.Fatal("Delete accepted invalid table")
	}
	if called {
		t.Fatal("invalid target executed nft")
	}
}

func TestRuntimeReadCountersUsesDirectValidatedNftArguments(t *testing.T) {
	t.Parallel()

	var gotArgs []string
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		gotArgs = append([]string(nil), args...)
		_, _ = io.WriteString(stdout, strings.ReplaceAll(nftCounterFixture(), "flux_panel", testGenerationA))
		return nil
	})
	got, err := runtime.ReadCounters(context.Background(), testGenerationA)
	if err != nil {
		t.Fatalf("ReadCounters: %v", err)
	}
	if want := []string{"list", "table", "inet", testGenerationA}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("nft args=%v, want %v", gotArgs, want)
	}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	if got[key] != (counters{Up: 20, Down: 7}) {
		t.Fatalf("counters=%v", got)
	}
}

func TestRuntimeReadCountersAllowsLargeBoundedRulesetButInventoryStaysSmall(t *testing.T) {
	t.Parallel()

	fixture := largeNftCounterFixture(10_001)
	if len(fixture) <= defaultNftOutputLimit {
		t.Fatalf("large fixture bytes=%d, want above inventory cap %d", len(fixture), defaultNftOutputLimit)
	}
	runtime := newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		switch {
		case reflect.DeepEqual(args, []string{"list", "table", "inet", testGenerationA}):
			_, _ = io.WriteString(stdout, fixture)
		case reflect.DeepEqual(args, []string{"-j", "list", "tables"}):
			_, _ = io.WriteString(stdout, strings.Repeat("x", defaultNftOutputLimit+1))
		default:
			return fmt.Errorf("unexpected args %v", args)
		}
		return nil
	})
	runtime.maxOutputBytes = defaultNftOutputLimit
	runtime.maxCounterOutputBytes = defaultNftCounterOutputLimit
	got, err := runtime.ReadCounters(context.Background(), testGenerationA)
	if err != nil {
		t.Fatalf("ReadCounters large fixture: %v", err)
	}
	if len(got) != 10_001 {
		t.Fatalf("counter keys=%d, want 10001", len(got))
	}
	if _, err := runtime.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("Discover oversized inventory error=%v", err)
	}

	runtime.maxCounterOutputBytes = len(fixture) - 1
	if _, err := runtime.ReadCounters(context.Background(), testGenerationA); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("ReadCounters oversized counter output error=%v", err)
	}
}

func newTestExecRuntime(t *testing.T, command nftCommandFunc) *execNftRuntime {
	t.Helper()
	return &execNftRuntime{
		nftPath:        "nft-test",
		tempDir:        t.TempDir(),
		timeout:        time.Second,
		maxOutputBytes: 1024,
		random:         bytes.NewReader(make([]byte, 64)),
		command:        command,
	}
}

func runtimeReturningJSON(t *testing.T, output string) *execNftRuntime {
	t.Helper()
	return newTestExecRuntime(t, func(_ context.Context, _ string, args []string, stdout, stderr io.Writer) error {
		if !reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
			return fmt.Errorf("unexpected args %v", args)
		}
		_, _ = io.WriteString(stdout, output)
		return nil
	})
}

func readRecordedCommand(t *testing.T, args []string) recordedNftCommand {
	t.Helper()
	if len(args) != 2 || args[0] != "-f" {
		t.Fatalf("nft args=%v, want -f <path>", args)
	}
	info, err := os.Stat(args[1])
	if err != nil {
		t.Fatalf("stat transaction: %v", err)
	}
	raw, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}
	return recordedNftCommand{args: append([]string(nil), args...), transaction: string(raw), mode: info.Mode()}
}

func assertNoTempTransactions(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".nft-transaction-") {
			t.Fatalf("temporary transaction was not removed: %s", filepath.Join(dir, entry.Name()))
		}
	}
}

func tableJSON(family, name string, flags []string) string {
	flagJSON := ""
	if flags != nil {
		quoted := make([]string, len(flags))
		for i, flag := range flags {
			quoted[i] = fmt.Sprintf("%q", flag)
		}
		flagJSON = `,"flags":[` + strings.Join(quoted, ",") + `]`
	}
	return fmt.Sprintf(`{"table":{"family":%q,"name":%q%s}}`, family, name, flagJSON)
}

func tableJSONWithRawFlags(family, name, flags string) string {
	return fmt.Sprintf(`{"table":{"family":%q,"name":%q,"flags":%s}}`, family, name, flags)
}

func nftTablesJSON(objects ...string) string {
	return `{"nftables":[` + strings.Join(objects, ",") + `]}`
}

func nftTableTextFixture(table string, dormant bool) string {
	flags := ""
	if dormant {
		flags = "\tflags dormant\n\n"
	}
	return fmt.Sprintf("table inet %s {\n%s\tchain forward {\n\t}\n}\n", table, flags)
}

func largeNftCounterFixture(keys int) string {
	var fixture strings.Builder
	for id := 1; id <= keys; id++ {
		fmt.Fprintf(&fixture, "add rule inet %s forward counter packets 1 bytes %d comment \"fp:%d:2:3:up\"\n", testGenerationA, id, id)
		fmt.Fprintf(&fixture, "add rule inet %s forward counter packets 1 bytes %d comment \"fp:%d:2:3:down\"\n", testGenerationA, id+1, id)
	}
	return fixture.String()
}
