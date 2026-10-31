package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.UnixMilli(1_730_000_000_000)

func releaseTarget(version string) Target {
	return Target{Kind: KindRelease, Version: version, Commit: "abcdef0123456789", Dir: "releases/" + version + "-abcdef0", SchemaEpoch: 1}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := New(Base{Version: "v0.2.0", Commit: "1234567"})
	active := releaseTarget("v0.3.0")
	st.Active = &active
	st.Quarantine = []QuarantineEntry{{Version: "v0.3.1", Reason: "ready_timeout", At: 1}}
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode=%v err=%v", info.Mode().Perm(), err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Base != st.Base || loaded.Active == nil || *loaded.Active != active || len(loaded.Quarantine) != 1 {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestLoadClassifiesErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing err=%v", err)
	}
	cases := map[string]error{
		`{not json`:                     ErrCorrupt,
		`{"base":{"version":"v0.2.0"}}`: ErrCorrupt,
		`{"schema":2,"futureField":1}`:  ErrUnsupportedSchema,
	}
	for raw, want := range cases {
		path := filepath.Join(dir, "state.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); !errors.Is(err, want) {
			t.Fatalf("Load(%s) err=%v want %v", raw, err, want)
		}
	}
}

func TestLoadIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := `{"schema":1,"base":{"version":"v0.2.0"},"active":{"kind":"release","version":"v0.3.0","futureFlag":true},"futureTop":[1,2]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil || st.Active == nil || st.Active.Version != "v0.3.0" {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestSaveKeepsOriginalWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, New(Base{Version: "v0.2.0"})); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)

	renameFile = func(string, string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { renameFile = os.Rename })
	if err := Save(path, New(Base{Version: "v9.9.9"})); err == nil {
		t.Fatal("Save must report the rename failure")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("a failed save must leave the original state untouched")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestConcurrentSavesNeverLeavePartialFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := New(Base{Version: "v0.2.0"})
			st.History = make([]Result, i)
			_ = Save(path, st)
		}(i)
	}
	wg.Wait()
	if _, err := Load(path); err != nil {
		t.Fatalf("state after concurrent saves is unreadable: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestCommitTrialRotatesActiveAndPrevious(t *testing.T) {
	st := New(Base{Version: "v0.2.0", Commit: "1234567"})
	st.Trial = &Trial{Target: releaseTarget("v0.3.0"), Action: ActionUpgrade, RequestID: "req-1", Attempts: 1}
	result, err := st.CommitTrial(testNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeCommitted || st.Trial != nil || st.Active == nil || st.Active.Version != "v0.3.0" || st.Active.ActivatedAt != testNow.UnixMilli() {
		t.Fatalf("after upgrade commit: %+v", st)
	}
	if st.Previous == nil || !st.Previous.IsBuiltin() || st.Previous.Version != "v0.2.0" {
		t.Fatalf("previous must be the builtin version: %+v", st.Previous)
	}
	if st.LastResult == nil || st.LastResult.RequestID != "req-1" || len(st.History) != 1 {
		t.Fatalf("result not recorded: %+v", st)
	}

	st.Trial = &Trial{Target: Target{Kind: KindBuiltin}, Action: ActionUseBuiltin, RequestID: "req-2"}
	if _, err := st.CommitTrial(testNow); err != nil {
		t.Fatal(err)
	}
	if st.Active != nil || st.Previous == nil || st.Previous.Version != "v0.3.0" {
		t.Fatalf("after use-builtin commit: active=%+v previous=%+v", st.Active, st.Previous)
	}
	if _, err := st.CommitTrial(testNow); err == nil {
		t.Fatal("committing without a trial must fail")
	}
}

func TestRollbackTrialQuarantinesOnlyFailedUpgrades(t *testing.T) {
	st := New(Base{Version: "v0.2.0"})
	st.Trial = &Trial{Target: releaseTarget("v0.3.1"), Action: ActionUpgrade, RequestID: "req-3"}
	st.RollbackTrial(Result{Stage: StageReadyTimeout, ExitCode: 1, Detail: "first line\nmigration failed"}, testNow)
	if st.Trial != nil || st.LastResult.Outcome != OutcomeRolledBack || st.LastResult.Version != "v0.3.1" || st.LastResult.RequestID != "req-3" {
		t.Fatalf("rollback result=%+v", st.LastResult)
	}
	entry, ok := st.QuarantineFor("v0.3.1")
	if !ok || !st.IsQuarantined("v0.3.1", "abcdef0123456789") || !strings.Contains(entry.Reason, "migration failed") {
		t.Fatalf("failed upgrade must be quarantined: %+v", st.Quarantine)
	}

	st.Trial = &Trial{Target: releaseTarget("v0.2.5"), Action: ActionRollback, RequestID: "req-4"}
	st.RollbackTrial(Result{Stage: StageStart}, testNow)
	if st.IsQuarantined("v0.2.5", "") {
		t.Fatal("a failed manual rollback must not quarantine the target")
	}

	// 管理员确认后强制重试并成功：隔离记录随提交清除。
	st.Trial = &Trial{Target: releaseTarget("v0.3.1"), Action: ActionUpgrade, RequestID: "req-5"}
	if _, err := st.CommitTrial(testNow); err != nil {
		t.Fatal(err)
	}
	if st.IsQuarantined("v0.3.1", "") {
		t.Fatal("a committed version must leave quarantine")
	}
}

func TestRetireOverlaysKeepsOnlyNewerQuarantine(t *testing.T) {
	st := New(Base{Version: "v0.2.0"})
	active := releaseTarget("v0.3.0")
	st.Active, st.Previous = &active, &Target{Kind: KindBuiltin, Version: "v0.2.0"}
	st.Trial = &Trial{Target: releaseTarget("v0.3.2"), Action: ActionUpgrade}
	st.Quarantine = []QuarantineEntry{{Version: "v0.3.1"}, {Version: "v0.5.0"}}
	st.RetireOverlays(Base{Version: "v0.4.0", Commit: "fedcba9"})
	if st.Active != nil || st.Previous != nil || st.Trial != nil || st.Base.Version != "v0.4.0" {
		t.Fatalf("overlays not retired: %+v", st)
	}
	if len(st.Quarantine) != 1 || st.Quarantine[0].Version != "v0.5.0" {
		t.Fatalf("quarantine=%+v", st.Quarantine)
	}
}

func TestHistoryKeepsMostRecentEntries(t *testing.T) {
	st := New(Base{Version: "v0.2.0"})
	for i := 0; i < historyLimit+5; i++ {
		st.AppendHistory(Result{Outcome: OutcomeCommitted, At: int64(i)})
	}
	if len(st.History) != historyLimit || st.History[0].At != 5 || st.History[historyLimit-1].At != historyLimit+4 {
		t.Fatalf("history=%v", st.History)
	}
}

func TestClearQuarantine(t *testing.T) {
	st := New(Base{Version: "v0.2.0"})
	st.Quarantine = []QuarantineEntry{{Version: "v0.3.0"}, {Version: "v0.3.1"}}
	if removed := st.ClearQuarantine("v0.3.0"); removed != 1 || len(st.Quarantine) != 1 {
		t.Fatalf("removed=%d quarantine=%v", removed, st.Quarantine)
	}
	if removed := st.ClearQuarantine(""); removed != 1 || len(st.Quarantine) != 0 {
		t.Fatalf("removed=%d quarantine=%v", removed, st.Quarantine)
	}
}
