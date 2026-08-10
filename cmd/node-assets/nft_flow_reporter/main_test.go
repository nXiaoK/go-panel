package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
)

func TestReporterLockContentionReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reporter.lock")
	unlock, err := acquireReporterLock(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer unlock()
	if _, err := acquireReporterLock(path); !errors.Is(err, errReporterLocked) {
		t.Fatalf("second lock err=%v, want errReporterLocked", err)
	}
}

func TestReadCanonicalRulesAcceptsEmptyAndValidButRejectsAmbiguousLines(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     int
		wantErr  bool
	}{
		{name: "empty", contents: "", want: 0},
		{name: "valid final newline", contents: testCanonicalRule + "\n", want: 1},
		{name: "valid multiple", contents: testCanonicalRule + "\n" + testCanonicalRule, want: 2},
		{name: "CRLF", contents: testCanonicalRule + "\r\n", wantErr: true},
		{name: "blank line", contents: testCanonicalRule + "\n\n" + testCanonicalRule, wantErr: true},
		{name: "surrounding space", contents: " " + testCanonicalRule, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.nft")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			rules, err := readCanonicalRules(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ambiguous rules accepted: %q", tc.contents)
				}
				return
			}
			if err != nil || len(rules) != tc.want {
				t.Fatalf("rules=%q err=%v", rules, err)
			}
		})
	}
}

func TestReadCanonicalRulesRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.nft")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxRulesFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readCanonicalRules(path); err == nil {
		t.Fatal("oversized rules file was accepted")
	}
}

func TestReadCanonicalRulesRejectsRuleCountAndLineLengthBeforeExpansion(t *testing.T) {
	t.Run("rule count", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rules.nft")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < maxRules+1; i++ {
			if _, err := file.WriteString(testCanonicalRule + "\n"); err != nil {
				t.Fatal(err)
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err != nil || info.Size() >= maxRulesFileBytes {
			t.Fatalf("fixture size must exercise count before bytes: info=%v err=%v", info, err)
		}
		if _, err := readCanonicalRules(path); err == nil || !strings.Contains(err.Error(), "rule count") {
			t.Fatalf("rule-count error=%v", err)
		}
	})
	t.Run("line length", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rules.nft")
		line := testCanonicalRule + strings.Repeat("x", maxRuleLineBytes)
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCanonicalRules(path); err == nil || !strings.Contains(err.Error(), "line length") {
			t.Fatalf("line-length error=%v", err)
		}
	})
}

func TestRunReporterRecoversThenReadsDurableGeneratedActiveTable(t *testing.T) {
	journal := mustTestJournal(t)
	journal.ActiveTable = testGenerationA
	store := &memoryJournalStore{journal: journal}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: testGenerationA}},
		counters: map[string]map[flowKey]counters{testGenerationA: {key: {Up: 9}}},
	}
	readTable := ""
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	r := reporter{
		store: store, runtime: runtime, serverAddr: "panel", secret: "secret", upload: panel.upload,
		readCounters: func(table string) (map[flowKey]counters, error) {
			readTable = table
			return cloneCounters(runtime.counters[table]), nil
		},
		writeActiveMarker: func(string) error { return nil },
	}
	if err := runReporterWith(context.Background(), r, "flux_panel"); err != nil {
		t.Fatal(err)
	}
	if readTable != testGenerationA || panel.totals[key].Up != 9 {
		t.Fatalf("readTable=%q totals=%+v", readTable, panel.totals)
	}
	if len(runtime.events) == 0 || runtime.events[0] != "discover" {
		t.Fatalf("reporter read before recovery discovery: %v", runtime.events)
	}
}

func TestCollectCountersRejectsMalformedDirectionalRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "missing bytes", text: `add rule inet flux_panel forward counter comment "fp:1:2:3:up"`},
		{name: "zero id", text: `add rule inet flux_panel forward counter bytes 1 comment "fp:0:2:3:up"`},
		{name: "negative id", text: `add rule inet flux_panel forward counter bytes 1 comment "fp:-1:2:3:up"`},
		{name: "negative tunnel id", text: `add rule inet flux_panel forward counter bytes 1 comment "fp:1:2:-1:up"`},
		{name: "overflow id", text: `add rule inet flux_panel forward counter bytes 1 comment "fp:9223372036854775808:2:3:up"`},
		{name: "overflow bytes", text: `add rule inet flux_panel forward counter bytes 9223372036854775808 comment "fp:1:2:3:up"`},
		{name: "sum overflow", text: "add rule inet flux_panel forward counter bytes 9223372036854775807 comment \"fp:1:2:3:up\"\n" +
			`add rule inet flux_panel forward counter bytes 1 comment "fp:1:2:3:up"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := collectCounters(tc.text); err == nil {
				t.Fatalf("collectCounters accepted %q", tc.text)
			}
		})
	}
}

func TestCollectCountersIgnoresNonDirectionalBaseComment(t *testing.T) {
	t.Parallel()

	got, err := collectCounters(`add rule inet flux_panel prerouting comment "fp:1:2:3"`)
	if err != nil {
		t.Fatalf("collectCounters: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("counters=%v, want empty", got)
	}
}

func TestCollectCountersAcceptsDefaultZeroUserTunnelID(t *testing.T) {
	t.Parallel()

	got, err := collectCounters("add rule inet flux_panel forward counter bytes 20 comment \"fp:1:2:0:up\"\n" +
		`add rule inet flux_panel forward counter bytes 7 comment "fp:1:2:0:down"`)
	if err != nil {
		t.Fatalf("collectCounters: %v", err)
	}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 0}
	if got[key] != (counters{Up: 20, Down: 7}) {
		t.Fatalf("counters=%v", got)
	}
}

func TestReporterUploadUsesV2HeaderAndReturnsJSONAck(t *testing.T) {
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: "reporter-http", Sequence: 1, BatchID: "batch-http",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 10, 4)},
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/flow/nft-upload-v2" || r.URL.RawQuery != "" {
			t.Errorf("request target=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Node-Secret"); got != "private-secret" {
			t.Errorf("secret header=%q", got)
		}
		ack := matchingAck(t, payload)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ack)
	}))
	defer server.Close()

	ack, err := upload(server.URL, "private-secret", payload)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := verifyNftFlowAck(batch, ack); err != nil {
		t.Fatalf("verify ack: %v", err)
	}
}

func TestReporterUploadRejectsTrailingAckJSON(t *testing.T) {
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: "reporter-http", Sequence: 1, BatchID: "batch-http",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 10, 4)},
	}
	payload, _ := json.Marshal(batch)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(matchingAck(t, payload))
		_, _ = w.Write([]byte(`{"extra":true}`))
	}))
	defer server.Close()
	if _, err := upload(server.URL, "secret", payload); err == nil {
		t.Fatal("upload accepted trailing acknowledgement JSON")
	}
}

func TestReporterPersistsEmptyBaselineWhenCountersDisappear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 20, Down: 7}}
	if err := store.save(journal); err != nil {
		t.Fatal(err)
	}
	uploaded := false
	r := reporter{
		store: store,
		readCounters: func(string) (map[flowKey]counters, error) {
			return map[flowKey]counters{}, nil
		},
		upload: func(_, _ string, _ []byte) (dto.NftFlowAckDto, error) {
			uploaded = true
			return dto.NftFlowAckDto{}, nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatalf("run reporter: %v", err)
	}
	if uploaded {
		t.Fatal("empty current counters should not upload")
	}
	got, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Baseline) != 0 {
		t.Fatalf("baseline=%+v, want reset to current empty counters", got.Baseline)
	}
}

func TestReporterReadFailureLeavesJournalUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 20, Down: 7}}
	if err := store.save(journal); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return nil, errors.New("nft read failed") },
		upload: func(_, _ string, _ []byte) (dto.NftFlowAckDto, error) {
			t.Fatal("unexpected upload")
			return dto.NftFlowAckDto{}, nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("read failure should fail reporter")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read failure changed durable journal")
	}
}

func reporterDTOItem(forwardID, userID, userTunnelID, up, down int64) dto.NftFlowItem {
	return dto.NftFlowItem{
		ForwardID: &forwardID, UserID: &userID, UserTunnelID: &userTunnelID, Up: &up, Down: &down,
	}
}

func nftCounterFixture() string {
	return `
add rule inet flux_panel forward tcp dport 80 counter packets 1 bytes 20 comment "fp:1:2:3:up"
add rule inet flux_panel forward tcp sport 80 counter packets 1 bytes 7 comment "fp:1:2:3:down"
`
}
