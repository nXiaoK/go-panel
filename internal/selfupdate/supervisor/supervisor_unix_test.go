//go:build unix

package supervisor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/stage"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// 测试二进制以 GO_PANEL_SUPERVISOR_TEST_CHILD=1 重新执行自身充当面板子进程；
// 行为由 GO_PANEL_SUPERVISOR_TEST_PLAN 按运行方式指定，例如 "trial:crash,builtin:ready"。
const (
	envHelper = "GO_PANEL_SUPERVISOR_TEST_CHILD"
	envPlan   = "GO_PANEL_SUPERVISOR_TEST_PLAN"
	envDir    = "GO_PANEL_SUPERVISOR_TEST_DIR"

	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

var archiveRoot string

func TestMain(m *testing.M) {
	if os.Getenv(envHelper) == "1" {
		os.Exit(runHelperChild())
	}
	var err error
	archiveRoot, err = os.MkdirTemp("", "supervisor-test-archives-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(archiveRoot)
	os.Exit(code)
}

func runHelperChild() int {
	mode := os.Getenv(protocol.EnvRunMode)
	dir := os.Getenv(envDir)
	behavior := "ready"
	for _, entry := range strings.Split(os.Getenv(envPlan), ",") {
		if name, value, ok := strings.Cut(entry, ":"); ok && name == mode {
			behavior = value
		}
	}
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d.env", mode, os.Getpid())), []byte(strings.Join(os.Environ(), "\n")), 0o600)
	notifier := child.ClaimReadyPipe()
	switch behavior {
	case "crash":
		fmt.Fprintln(os.Stderr, "数据库初始化失败: 自动迁移失败")
		fmt.Fprintln(os.Stderr, "generated administrator credentials: username=admin password=hunter2")
		return 1
	case "exit3":
		return 3
	case "restart":
		return protocol.ExitRestartRequested
	case "restart-once":
		if firstTime(dir, "restart-once") {
			return protocol.ExitRestartRequested
		}
	case "schema78-once":
		if firstTime(dir, "schema78") {
			fmt.Fprintln(os.Stderr, "数据库已被 v0.3.0 迁移为结构纪元 2")
			return protocol.ExitSchemaTooNew
		}
	case "hang":
		waitForTerm()
		return 0
	case "unhealthy":
		return serveHelper(notifier, dir, mode, http.StatusInternalServerError)
	}
	return serveHelper(notifier, dir, mode, http.StatusOK)
}

func firstTime(dir, name string) bool {
	file, err := os.OpenFile(filepath.Join(dir, name+".done"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func serveHelper(notifier *child.Notifier, dir, mode string, status int) int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 9
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })}
	go func() { _ = server.Serve(listener) }()
	_ = notifier.Notify(listener.Addr())
	_ = os.WriteFile(filepath.Join(dir, mode+".ready"), nil, 0o600)
	waitForTerm()
	_ = server.Close()
	return 0
}

func waitForTerm() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	<-signals
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t       *testing.T
	markers string
	priv    ed25519.PrivateKey
	signals chan os.Signal
	output  *syncBuffer
	cfg     Config
}

func newHarness(t *testing.T, plan string) *harness {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	l, err := layout.Resolve(func(key string) string {
		switch key {
		case "DB_PATH":
			return filepath.Join(root, "data", "panel.db")
		case protocol.EnvRuntimeDir:
			return filepath.Join(root, "data", "runtime")
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	markers := filepath.Join(root, "markers")
	if err := os.MkdirAll(markers, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.BackupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, markers: markers, priv: priv, signals: make(chan os.Signal, 4), output: &syncBuffer{}}
	baseEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), envHelper + "=1", envPlan + "=" + plan, envDir + "=" + markers, protocol.EnvNodeAssetDir + "=/app/node-assets"}
	h.cfg = Config{
		Layout:     l,
		Builtin:    Builtin{Path: binary, Version: "v0.2.0", Commit: "builtin", RuntimeABI: 1, SchemaEpoch: 1},
		KeyRing:    manifest.NewKeyRing(pub),
		Repository: "nXiaoK/go-panel",
		Timeouts: Timeouts{
			Ready: 10 * time.Second, Stable: 400 * time.Millisecond, ProbeInterval: 50 * time.Millisecond,
			ProbeTimeout: time.Second, Stop: 5 * time.Second, KillGrace: 2 * time.Second,
			RestartWindow: 30 * time.Second, MaxRestarts: 5,
		},
		Environ: func() []string { return append([]string(nil), baseEnv...) },
		Stdout:  h.output,
		Stderr:  h.output,
		Signals: h.signals,
		ValidateBackup: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
	}
	return h
}

// testArchive 构造包含当前测试二进制的更新包；同一版本在整个测试进程中复用。
var archiveCache sync.Map

func testArchive(t *testing.T, version string) string {
	t.Helper()
	root := "go-panel-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	path := filepath.Join(archiveRoot, root+".tar.gz")
	once, _ := archiveCache.LoadOrStore(path, &sync.Once{})
	once.(*sync.Once).Do(func() {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
		tw := tar.NewWriter(gz)
		for name, body := range map[string][]byte{"go-panel": data, "node-assets/gost-amd64": []byte("gost")} {
			if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	return path
}

// install 用 harness 的测试密钥签名并安装一个包含测试二进制的版本。
func (h *harness) install(version string) state.Target {
	h.t.Helper()
	archive := testArchive(h.t, version)
	root := "go-panel-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	size, sum, err := manifest.HashFile(archive)
	if err != nil {
		h.t.Fatal(err)
	}
	files, err := manifest.DescribeArchive(archive, root, manifest.DefaultLimits)
	if err != nil {
		h.t.Fatal(err)
	}
	m := manifest.Manifest{
		Schema: manifest.Schema, Product: manifest.Product, Repository: "nXiaoK/go-panel",
		Version: version, Commit: testCommit, BuildTime: "2026-11-01T00:00:00Z",
		Compat: protocol.Compat{RuntimeABI: 1, SchemaEpoch: 2, MinReaderEpoch: 2, SupervisorProtocol: protocol.Range{Min: 1, Max: 1}, MinUpgradeFrom: "v0.0.0"},
		Artifacts: []manifest.Artifact{{
			OS: runtime.GOOS, Arch: runtime.GOARCH, Root: root,
			Archive: manifest.Archive{Name: root + ".tar.gz", Size: size, SHA256: sum},
			Files:   files,
		}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	sig, err := manifest.EncodeSignatures(manifest.Sign(raw, h.priv))
	if err != nil {
		h.t.Fatal(err)
	}
	target, err := stage.Install(context.Background(), h.cfg.Layout,
		stage.Source{Manifest: &m, RawManifest: raw, RawSig: sig, ArchivePath: archive},
		stage.Verifier{KeyRing: h.cfg.KeyRing, Repository: "nXiaoK/go-panel", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err != nil {
		h.t.Fatal(err)
	}
	return target
}

func (h *harness) requestUpgrade(target state.Target, readyTimeoutSec int) {
	h.t.Helper()
	pending := state.Pending{
		RequestID: "req-" + target.Version, Action: state.ActionUpgrade, Target: target,
		BackupFile: "backups/pre-update-" + target.Version + "-test.db", ReadyTimeoutSec: readyTimeoutSec,
		RequestedAt: time.Now().UnixMilli(), RequestedBy: "admin_user",
	}
	if err := state.WritePending(h.cfg.Layout.PendingFile, h.cfg.Layout.PendingLock, pending); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) start() <-chan int {
	result := make(chan int, 1)
	go func() { result <- Run(h.cfg) }()
	return result
}

func (h *harness) stop(result <-chan int) int {
	h.t.Helper()
	h.signals <- syscall.SIGTERM
	return h.wait(result)
}

func (h *harness) wait(result <-chan int) int {
	h.t.Helper()
	select {
	case code := <-result:
		return code
	case <-time.After(30 * time.Second):
		h.t.Fatalf("supervisor did not exit; output:\n%s", h.output.String())
		return -1
	}
}

func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; output:\n%s", what, h.output.String())
}

func (h *harness) ready(mode string) bool {
	_, err := os.Stat(filepath.Join(h.markers, mode+".ready"))
	return err == nil
}

func (h *harness) state() state.State {
	h.t.Helper()
	st, err := state.Load(h.cfg.Layout.StateFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.t.Fatal(err)
	}
	return st
}

// envOf 返回指定运行方式的子进程环境（多次启动时任取其一）。
func (h *harness) envOf(mode string) map[string]string {
	h.t.Helper()
	matches, _ := filepath.Glob(filepath.Join(h.markers, mode+"-*.env"))
	if len(matches) == 0 {
		h.t.Fatalf("no %s child was started", mode)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		h.t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	return env
}

func (h *harness) starts(mode string) int {
	matches, _ := filepath.Glob(filepath.Join(h.markers, mode+"-*.env"))
	return len(matches)
}

func TestSupervisorRunsBuiltinAndStopsGracefully(t *testing.T) {
	h := newHarness(t, "builtin:ready")
	result := h.start()
	h.waitFor("builtin child", func() bool { return h.ready(protocol.RunModeBuiltin) })
	env := h.envOf(protocol.RunModeBuiltin)
	for key, want := range map[string]string{
		protocol.EnvSupervisorProtocol: "1",
		protocol.EnvReadyFD:            "3",
		protocol.EnvRunMode:            protocol.RunModeBuiltin,
		protocol.EnvBuiltinVersion:     "v0.2.0",
		protocol.EnvBuiltinRuntimeABI:  "1",
		protocol.EnvBuiltinSchemaEpoch: "1",
		protocol.EnvRuntimeDir:         h.cfg.Layout.RuntimeDir,
		protocol.EnvNodeAssetDir:       "/app/node-assets",
	} {
		if env[key] != want {
			t.Fatalf("%s=%q want %q", key, env[key], want)
		}
	}
	if _, ok := env[protocol.EnvRequestID]; ok {
		t.Fatal("non-trial children must not receive a request id")
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d output:\n%s", code, h.output.String())
	}
	if st := h.state(); st.Base.Version != "v0.2.0" || st.Active != nil {
		t.Fatalf("state=%+v", st)
	}
}

func TestSupervisorCommitsHealthyTrial(t *testing.T) {
	h := newHarness(t, "trial:ready,release:ready")
	target := h.install("v0.3.0")
	h.requestUpgrade(target, 0)
	result := h.start()
	h.waitFor("commit", func() bool {
		st := h.state()
		return st.Active != nil && st.Active.Version == "v0.3.0" && st.Trial == nil
	})
	st := h.state()
	if st.LastResult == nil || st.LastResult.Outcome != state.OutcomeCommitted || st.Previous == nil || !st.Previous.IsBuiltin() {
		t.Fatalf("state after commit=%+v", st)
	}
	env := h.envOf(protocol.RunModeTrial)
	dir, _ := h.cfg.Layout.ReleasePath(target.Dir)
	if env[protocol.EnvRequestID] != "req-v0.3.0" || env[protocol.EnvNodeAssetDir] != stage.NodeAssetDir(dir) {
		t.Fatalf("trial env request=%q node assets=%q", env[protocol.EnvRequestID], env[protocol.EnvNodeAssetDir])
	}
	if strings.Count(strings.Join(mapToEnv(env), "\n"), protocol.EnvNodeAssetDir+"=") != 1 {
		t.Fatal("NODE_ASSET_DIR must appear exactly once")
	}
	if _, err := os.Stat(h.cfg.Layout.PendingFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the consumed pending file must be removed")
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}

	// 重新启动：直接运行已提交的覆盖版本。
	restarted := newHarnessFrom(h, "release:ready")
	result = restarted.start()
	restarted.waitFor("release child", func() bool { return restarted.ready(protocol.RunModeRelease) })
	if code := restarted.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func newHarnessFrom(h *harness, plan string) *harness {
	next := *h
	next.signals = make(chan os.Signal, 4)
	next.output = &syncBuffer{}
	env := h.cfg.Environ()
	for i, entry := range env {
		if strings.HasPrefix(entry, envPlan+"=") {
			env[i] = envPlan + "=" + plan
		}
	}
	next.cfg.Environ = func() []string { return append([]string(nil), env...) }
	next.cfg.Signals = next.signals
	next.cfg.Stdout, next.cfg.Stderr = next.output, next.output
	return &next
}

func mapToEnv(env map[string]string) []string {
	var out []string
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func TestSupervisorRollsBackCrashingTrial(t *testing.T) {
	h := newHarness(t, "trial:crash,builtin:ready")
	h.requestUpgrade(h.install("v0.3.0"), 0)
	result := h.start()
	h.waitFor("rollback to builtin", func() bool { return h.ready(protocol.RunModeBuiltin) })
	st := h.state()
	if st.Active != nil || st.Trial != nil || st.LastResult == nil {
		t.Fatalf("state=%+v", st)
	}
	last := st.LastResult
	if last.Outcome != state.OutcomeRolledBack || last.Stage != state.StageStart || last.ExitCode != 1 || last.RequestID != "req-v0.3.0" {
		t.Fatalf("lastResult=%+v", last)
	}
	if !strings.Contains(last.Detail, "自动迁移失败") || strings.Contains(last.Detail, "hunter2") {
		t.Fatalf("detail must keep the failure reason and redact credentials: %q", last.Detail)
	}
	if !st.IsQuarantined("v0.3.0", "") {
		t.Fatal("a crashed upgrade must be quarantined")
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorRollsBackTrialThatNeverBecomesReady(t *testing.T) {
	h := newHarness(t, "trial:hang,builtin:ready")
	h.cfg.Timeouts.Ready = 500 * time.Millisecond
	h.requestUpgrade(h.install("v0.3.0"), 0)
	result := h.start()
	h.waitFor("rollback to builtin", func() bool { return h.ready(protocol.RunModeBuiltin) })
	if last := h.state().LastResult; last == nil || last.Stage != state.StageReadyTimeout {
		t.Fatalf("lastResult=%+v", last)
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorRollsBackUnhealthyTrial(t *testing.T) {
	h := newHarness(t, "trial:unhealthy,builtin:ready")
	h.requestUpgrade(h.install("v0.3.0"), 0)
	result := h.start()
	h.waitFor("rollback to builtin", func() bool { return h.ready(protocol.RunModeBuiltin) })
	last := h.state().LastResult
	if last == nil || last.Stage != state.StageHealthz || !strings.Contains(last.Detail, "500") {
		t.Fatalf("lastResult=%+v", last)
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorRestoresSnapshotWhenRollbackTargetCannotReadDatabase(t *testing.T) {
	h := newHarness(t, "trial:crash,builtin:schema78-once")
	l := h.cfg.Layout
	writeFile(t, l.DBPath, "migrated-db")
	writeFile(t, l.DBPath+"-wal", "migrated-wal")
	writeFile(t, filepath.Join(l.BackupsDir, "pre-update-v0.3.0-test.db"), "snapshot")
	h.requestUpgrade(h.install("v0.3.0"), 0)
	result := h.start()
	h.waitFor("builtin after restore", func() bool { return h.ready(protocol.RunModeBuiltin) })

	if got := readFile(t, l.DBPath); got != "snapshot" {
		t.Fatalf("database content=%q, want the pre-update snapshot", got)
	}
	if _, err := os.Stat(l.DBPath + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the failed WAL must not stay next to the restored database")
	}
	failed, _ := filepath.Glob(filepath.Join(l.BackupsDir, "failed-update-v0.3.0-*"))
	if len(failed) != 1 || readFile(t, filepath.Join(failed[0], "panel.db")) != "migrated-db" || readFile(t, filepath.Join(failed[0], "panel.db-wal")) != "migrated-wal" {
		t.Fatalf("failed database must be preserved: %v", failed)
	}
	if last := h.state().LastResult; last == nil || !last.DBRestored || last.Outcome != state.OutcomeRolledBack {
		t.Fatalf("lastResult=%+v", last)
	}
	if _, err := os.Stat(l.RestoreJournal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the restore journal must be removed after completion")
	}
	if h.starts(protocol.RunModeBuiltin) != 2 {
		t.Fatalf("builtin starts=%d, want 2 (refused once, then restored)", h.starts(protocol.RunModeBuiltin))
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorCompletesInterruptedRestoreBeforeStarting(t *testing.T) {
	h := newHarness(t, "builtin:ready")
	l := h.cfg.Layout
	writeFile(t, l.DBPath, "half-restored")
	snapshot := filepath.Join(l.BackupsDir, "pre-update-v0.3.0-test.db")
	writeFile(t, snapshot, "snapshot")
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	failedDir := filepath.Join(l.BackupsDir, "failed-update-v0.3.0-interrupted")
	if err := state.SaveRestoreJournal(l.RestoreJournal, state.RestoreJournal{
		RequestID: "req", DBPath: l.DBPath, BackupFile: snapshot, FailedDir: failedDir, Phase: state.RestorePhaseMoving,
	}); err != nil {
		t.Fatal(err)
	}
	result := h.start()
	h.waitFor("builtin child", func() bool { return h.ready(protocol.RunModeBuiltin) })
	if readFile(t, l.DBPath) != "snapshot" || readFile(t, filepath.Join(failedDir, "panel.db")) != "half-restored" {
		t.Fatal("the interrupted restore must be completed before the panel starts")
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorRestartsWhenChildRequestsIt(t *testing.T) {
	h := newHarness(t, "builtin:restart-once")
	result := h.start()
	h.waitFor("second builtin start", func() bool { return h.ready(protocol.RunModeBuiltin) })
	if starts := h.starts(protocol.RunModeBuiltin); starts != 2 {
		t.Fatalf("starts=%d, want 2", starts)
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestSupervisorStopsOnRestartStorm(t *testing.T) {
	h := newHarness(t, "builtin:restart")
	h.cfg.Timeouts.MaxRestarts = 2
	if code := h.wait(h.start()); code != 1 {
		t.Fatalf("exit=%d, want 1", code)
	}
	if !strings.Contains(h.output.String(), "重启风暴") {
		t.Fatalf("output must explain the restart storm:\n%s", h.output.String())
	}
}

func TestSupervisorReturnsChildExitCode(t *testing.T) {
	h := newHarness(t, "builtin:exit3")
	if code := h.wait(h.start()); code != 3 {
		t.Fatalf("exit=%d, want 3", code)
	}
}

func TestSupervisorRefusesSecondInstance(t *testing.T) {
	h := newHarness(t, "builtin:ready")
	if err := h.cfg.Layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	unlock, err := state.TryLock(h.cfg.Layout.SupervisorLock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	if code := h.wait(h.start()); code != 1 {
		t.Fatalf("exit=%d, want 1", code)
	}
	if h.starts(protocol.RunModeBuiltin) != 0 {
		t.Fatal("no child may start while another supervisor holds the lock")
	}
}

func TestSupervisorUseBuiltinIgnoresActiveRelease(t *testing.T) {
	h := newHarness(t, "builtin:ready,release:crash")
	target := h.install("v0.3.0")
	st := state.New(state.Base{Version: "v0.2.0", Commit: "builtin"})
	st.Active = &target
	if err := state.Save(h.cfg.Layout.StateFile, st); err != nil {
		t.Fatal(err)
	}
	h.cfg.UseBuiltin = true
	result := h.start()
	h.waitFor("builtin child", func() bool { return h.ready(protocol.RunModeBuiltin) })
	if h.state().Active == nil {
		t.Fatal("the emergency switch must not clear the active release")
	}
	if code := h.stop(result); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestMergeEnvKeepsEachKeyOnce(t *testing.T) {
	env := mergeEnv(
		[]string{"PATH=/bin", protocol.EnvNodeAssetDir + "=/app/node-assets", protocol.EnvRequestID + "=stale", "JWT_SECRET=x"},
		[]string{protocol.EnvNodeAssetDir + "=/data/releases/v1/node-assets", protocol.EnvRunMode + "=release"},
	)
	joined := strings.Join(env, "\n")
	if strings.Count(joined, protocol.EnvNodeAssetDir+"=") != 1 || !strings.Contains(joined, "/data/releases/v1/node-assets") {
		t.Fatalf("env=%v", env)
	}
	if strings.Contains(joined, "stale") || !strings.Contains(joined, "JWT_SECRET=x") {
		t.Fatalf("env=%v", env)
	}
}

func TestParseReadyAndTailBuffer(t *testing.T) {
	if addr, ok := parseReady("READY=1 ADDR=[::]:6365\n"); !ok || addr != "[::]:6365" {
		t.Fatalf("addr=%q ok=%v", addr, ok)
	}
	if _, ok := parseReady("hello\n"); ok {
		t.Fatal("other lines must not count as ready")
	}
	tail := newTailBuffer(2)
	_, _ = tail.Write([]byte("one\ntwo\nthr"))
	_, _ = tail.Write([]byte("ee\nfour"))
	if got := tail.String(); got != "two\nthree\nfour" {
		t.Fatalf("tail=%q, want the last 2 complete lines plus the partial line", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
