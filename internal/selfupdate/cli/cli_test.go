package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

func testOptions(t *testing.T) (Options, layout.Layout) {
	t.Helper()
	root := t.TempDir()
	getenv := func(key string) string {
		switch key {
		case "DB_PATH":
			return filepath.Join(root, "panel.db")
		case protocol.EnvRuntimeDir:
			return filepath.Join(root, "runtime")
		}
		return ""
	}
	l, err := layout.Resolve(getenv)
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Getenv:  getenv,
		Builtin: state.Target{Kind: state.KindBuiltin, Version: "v0.2.0"},
		Now:     func() time.Time { return time.UnixMilli(1_730_000_000_000) },
	}, l
}

func run(opts Options, args ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func TestStatusWithoutStateReportsBuiltin(t *testing.T) {
	opts, _ := testOptions(t)
	code, out, _ := run(opts, "status")
	if code != 0 || !strings.Contains(out, "尚未使用软更新") || !strings.Contains(out, "v0.2.0") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	code, out, _ = run(opts, "status", "--json")
	var parsed map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &parsed) != nil || parsed["state"] != nil {
		t.Fatalf("json status code=%d out=%s", code, out)
	}
}

func TestRollbackAndUseBuiltinWritePendingRequests(t *testing.T) {
	opts, l := testOptions(t)
	if code, _, errOut := run(opts, "rollback"); code != 1 || !strings.Contains(errOut, "没有可回滚") {
		t.Fatalf("rollback without previous code=%d err=%s", code, errOut)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := state.New(state.Base{Version: "v0.2.0"})
	active := state.Target{Kind: state.KindRelease, Version: "v0.3.0", Commit: "abcdef0123", Dir: "releases/v0.3.0-abcdef0"}
	st.Active = &active
	st.Previous = &state.Target{Kind: state.KindBuiltin, Version: "v0.2.0"}
	st.LastResult = &state.Result{Action: state.ActionUpgrade, Version: "v0.3.0", Outcome: state.OutcomeCommitted, At: 1}
	st.Quarantine = []state.QuarantineEntry{{Version: "v0.3.1", Reason: "ready_timeout"}}
	if err := state.Save(l.StateFile, st); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(opts, "status")
	for _, want := range []string{"当前生效：v0.3.0", "上一版本：镜像内置版本 v0.2.0", "已启用", "隔离版本：v0.3.1"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}

	if code, out, _ := run(opts, "rollback"); code != 0 || !strings.Contains(out, "docker restart") {
		t.Fatalf("rollback code=%d out=%s", code, out)
	}
	pending, err := state.LoadPending(l.PendingFile)
	if err != nil || pending.Action != state.ActionRollback || !pending.Target.IsBuiltin() || pending.RequestedBy != "cli" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if code, _, _ := run(opts, "use-builtin"); code != 0 {
		t.Fatal("use-builtin failed")
	}
	pending, err = state.LoadPending(l.PendingFile)
	if err != nil || pending.Action != state.ActionUseBuiltin {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if code, out, _ := run(opts, "status"); code != 0 || !strings.Contains(out, "待处理请求：use-builtin") {
		t.Fatalf("status must show the pending request:\n%s", out)
	}
}

func TestClearQuarantineRequiresStoppedPanel(t *testing.T) {
	opts, l := testOptions(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := state.New(state.Base{Version: "v0.2.0"})
	st.Quarantine = []state.QuarantineEntry{{Version: "v0.3.1"}, {Version: "v0.3.2"}}
	if err := state.Save(l.StateFile, st); err != nil {
		t.Fatal(err)
	}
	unlock, err := state.TryLock(l.SupervisorLock)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(opts, "clear-quarantine"); code != 1 || !strings.Contains(errOut, "面板正在运行") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	_ = unlock()
	if code, out, _ := run(opts, "clear-quarantine", "v0.3.1"); code != 0 || !strings.Contains(out, "1 条") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	loaded, _ := state.Load(l.StateFile)
	if len(loaded.Quarantine) != 1 || loaded.Quarantine[0].Version != "v0.3.2" {
		t.Fatalf("quarantine=%+v", loaded.Quarantine)
	}
}

func TestUsageErrors(t *testing.T) {
	opts, _ := testOptions(t)
	for _, args := range [][]string{nil, {"bogus"}, {"status", "--yaml"}, {"rollback", "extra"}} {
		if code, _, _ := run(opts, args...); code != 2 {
			t.Fatalf("Run(%q)=%d, want 2", args, code)
		}
	}
}
