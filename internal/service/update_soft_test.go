package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/stage"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

const (
	softTargetVersion = "v0.3.0"
	softCommit        = "0123456789abcdef0123456789abcdef01234567"
	softDownloadBase  = "/nXiaoK/go-panel/releases/download/" + softTargetVersion + "/"
)

var softCompat = protocol.Compat{RuntimeABI: 1, SchemaEpoch: 1, MinReaderEpoch: 1, SupervisorProtocol: protocol.Range{Min: 1, Max: 1}, MinUpgradeFrom: "v0.0.0"}

// softFixture 模拟 GitHub API 与 Release 资产：候选程序是一段能响应 version 与 migrate-check 的 shell 脚本。
type softFixture struct {
	t          *testing.T
	priv       ed25519.PrivateKey
	ring       manifest.KeyRing
	layout     layout.Layout
	tag        string
	assets     map[string][]byte
	archive    string
	restarts   atomic.Int32
	mu         sync.Mutex
	block      chan struct{}
	failAssets map[string]int
	omitAssets map[string]bool
	noDigest   bool
	config     UpdateRuntimeConfig
}

type softOptions struct {
	manifestVersion string
	arch            string
	compat          protocol.Compat
	repository      string
	signer          ed25519.PrivateKey
	tamperArchive   bool
}

func candidateScript(compat protocol.Compat) []byte {
	report, _ := json.Marshal(protocol.VersionReport{
		Version: softTargetVersion, Commit: softCommit, BuildTime: "now",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Compat: compat,
	})
	return []byte("#!/bin/sh\ncase \"$1\" in\n" +
		"version) printf '%s\\n' '" + string(report) + "' ;;\n" +
		"migrate-check) printf '%s\\n' '{\"ok\":true,\"durationMs\":5,\"schemaEpoch\":1,\"minReaderEpoch\":1}' ;;\n" +
		"*) exit 2 ;;\nesac\n")
}

func newSoftFixture(t *testing.T, opts softOptions) *softFixture {
	t.Helper()
	preserveBuildInfo(t)
	resetSoftUpdateForTest(t)
	buildinfo.Version = "v0.2.0"

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	l, err := layout.Resolve(func(key string) string {
		if key == "DB_PATH" {
			return dbPath
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if opts.signer == nil {
		opts.signer = priv
	}
	if opts.manifestVersion == "" {
		opts.manifestVersion = softTargetVersion
	}
	if opts.arch == "" {
		opts.arch = runtime.GOARCH
	}
	if opts.compat == (protocol.Compat{}) {
		opts.compat = softCompat
	}
	if opts.repository == "" {
		opts.repository = "nXiaoK/go-panel"
	}

	root := "go-panel-" + opts.manifestVersion + "-" + runtime.GOOS + "-" + opts.arch
	archiveName := root + ".tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	writeSoftArchive(t, archivePath, root, map[string][]byte{
		"go-panel":               candidateScript(opts.compat),
		"node-assets/gost-amd64": []byte("gost-binary"),
	})
	size, sum, err := manifest.HashFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := manifest.DescribeArchive(archivePath, root, manifest.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{
		Schema: manifest.Schema, Product: manifest.Product, Repository: opts.repository,
		Version: opts.manifestVersion, Commit: softCommit, BuildTime: "2026-11-01T00:00:00Z", Compat: opts.compat,
		Artifacts: []manifest.Artifact{{OS: runtime.GOOS, Arch: opts.arch, Root: root,
			Archive: manifest.Archive{Name: archiveName, Size: size, SHA256: sum}, Files: files}},
	}
	rawManifest, _ := json.Marshal(m)
	rawSig, err := manifest.EncodeSignatures(manifest.Sign(rawManifest, opts.signer))
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, _ := os.ReadFile(archivePath)
	if opts.tamperArchive {
		archiveBytes = append([]byte(nil), archiveBytes...)
		archiveBytes[len(archiveBytes)/2] ^= 0xff
	}
	f := &softFixture{
		t: t, priv: priv, ring: manifest.NewKeyRing(pub), layout: l, tag: softTargetVersion, archive: archiveName,
		assets: map[string][]byte{
			archiveName:            archiveBytes,
			manifest.FileName:      rawManifest,
			manifest.SignatureName: rawSig,
		},
		failAssets: map[string]int{},
		omitAssets: map[string]bool{},
	}
	f.config = UpdateRuntimeConfig{
		Enabled: true, Repository: "nXiaoK/go-panel", CheckInterval: time.Hour,
		ReleaseAPIBaseURL: "https://api.test",
		HTTPClient:        &http.Client{Transport: updateRoundTripFunc(f.roundTrip)},
		Strategy:          UpdateStrategyAuto,
		Child: child.Info{Supervised: true, Protocol: 1, RunMode: protocol.RunModeBuiltin,
			BuiltinVersion: "v0.2.0", BuiltinCommit: "builtin", BuiltinRuntimeABI: 1, BuiltinSchemaEpoch: 1},
		Layout:         l,
		KeyRing:        f.ring,
		GitHubProxy:    func() string { return "" },
		FreeBytes:      func(string) (uint64, error) { return 1 << 40, nil },
		RequestRestart: func() { f.restarts.Add(1) },
	}
	ConfigureUpdateRuntime(f.config)
	return f
}

func (f *softFixture) reconfigure(mutate func(*UpdateRuntimeConfig)) {
	mutate(&f.config)
	ConfigureUpdateRuntime(f.config)
}

func writeSoftArchive(t *testing.T, path, root string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"go-panel", "node-assets/gost-amd64"} {
		body := files[name]
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
}

func (f *softFixture) roundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Host == "api.test" && r.URL.Path == "/repos/nXiaoK/go-panel/releases/latest":
		var assets []githubReleaseAsset
		for name, data := range f.assets {
			if f.omitAssets[name] {
				continue
			}
			sum := sha256.Sum256(data)
			asset := githubReleaseAsset{
				Name: name, Size: int64(len(data)),
				BrowserDownloadURL: "https://github.com" + softDownloadBase + name,
				Digest:             "sha256:" + hex.EncodeToString(sum[:]),
			}
			if f.noDigest {
				asset.Digest = ""
			}
			assets = append(assets, asset)
		}
		raw, _ := json.Marshal(githubLatestRelease{TagName: f.tag, HTMLURL: "https://github.com/nXiaoK/go-panel/releases/tag/" + f.tag, Assets: assets})
		return updateJSONResponse(http.StatusOK, string(raw)), nil
	case r.URL.Host == "github.com" && strings.HasPrefix(r.URL.Path, softDownloadBase):
		name := path.Base(r.URL.Path)
		if status := f.failAssets[name]; status != 0 {
			return updateJSONResponse(status, "failure"), nil
		}
		data, ok := f.assets[name]
		if !ok {
			return updateJSONResponse(http.StatusNotFound, ""), nil
		}
		var body io.Reader = bytes.NewReader(data)
		if name == f.archive && f.block != nil {
			body = &blockingReader{ctx: r.Context(), release: f.block, data: data}
		}
		resp := updateJSONResponse(http.StatusOK, "")
		resp.Body = io.NopCloser(body)
		resp.ContentLength = int64(len(data))
		return resp, nil
	}
	return updateJSONResponse(http.StatusNotFound, ""), nil
}

// blockingReader 在 release 关闭前阻塞，并像真实连接一样响应请求取消。
type blockingReader struct {
	ctx     context.Context
	release chan struct{}
	data    []byte
	offset  int
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.offset == 0 {
		select {
		case <-b.release:
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func resetSoftUpdateForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		updateJobs.Lock()
		updateJobs.current = nil
		updateJobs.Unlock()
		updateCoordination.Lock()
		updateCoordination.restoring, updateCoordination.committing, updateCoordination.restartPending = false, false, false
		updateCoordination.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func softStatus(t *testing.T) PanelUpdateStatus {
	t.Helper()
	response := GetPanelUpdateStatus()
	status, ok := response.Data.(PanelUpdateStatus)
	if !ok {
		t.Fatalf("status response=%#v", response)
	}
	return status
}

func checkSoftStatus(t *testing.T) PanelUpdateStatus {
	t.Helper()
	status, err := checkPanelUpdate(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func waitForJob(t *testing.T, stages ...string) PanelUpdateJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if job := softStatus(t).Job; job != nil {
			for _, stage := range stages {
				if job.Stage == stage {
					return *job
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not reach %v: %+v", stages, softStatus(t).Job)
	return PanelUpdateJob{}
}

func TestSoftUpdateEndToEndWritesPendingAndRequestsRestart(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	status := checkSoftStatus(t)
	if status.Strategy != UpdateStrategySoft || !status.SoftUpdate.Available || status.SoftUpdate.DownloadBytes != int64(len(f.assets[f.archive])) || !status.AutoUpdateConfigured {
		t.Fatalf("status=%+v", status)
	}
	if status.Runtime.Mode != protocol.RunModeBuiltin || status.Runtime.BuiltinVersion != "v0.2.0" || status.Runtime.Rollback.Available {
		t.Fatalf("runtime=%+v", status.Runtime)
	}

	response := ApplyPanelUpdate(context.Background(), "admin_user", PanelUpdateApplyRequest{TargetVersion: softTargetVersion})
	trigger, ok := response.Data.(PanelUpdateTriggerResult)
	if response.Code != 0 || !ok || !trigger.Started || trigger.Strategy != UpdateStrategySoft || trigger.JobID == "" {
		t.Fatalf("apply=%#v", response)
	}
	job := waitForJob(t, updateStageRestarting, updateStageFailed)
	if job.Stage != updateStageRestarting {
		t.Fatalf("job failed: %+v", job)
	}
	if f.restarts.Load() != 1 {
		t.Fatalf("restart requests=%d", f.restarts.Load())
	}
	pending, err := state.LoadPending(f.layout.PendingFile)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Action != state.ActionUpgrade || pending.Target.Version != softTargetVersion || pending.RequestID != job.RequestID || pending.RequestedBy != "admin_user" {
		t.Fatalf("pending=%+v job=%+v", pending, job)
	}
	if pending.ReadyTimeoutSec != 120 || job.ReadyTimeoutSec != 120 {
		t.Fatalf("ready timeout pending=%d job=%d, want the 2 minute floor", pending.ReadyTimeoutSec, job.ReadyTimeoutSec)
	}
	backup, err := f.layout.BackupPath(pending.BackupFile)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatalf("pre-update snapshot missing: %v", err)
	}
	verifier := stage.Verifier{KeyRing: f.ring, Repository: "nXiaoK/go-panel", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if _, _, err := stage.VerifyInstalled(f.layout, pending.Target.Dir, verifier); err != nil {
		t.Fatalf("installed release must verify: %v", err)
	}
	if entries, _ := os.ReadDir(f.layout.StagingDir); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}

	if again := ApplyPanelUpdate(context.Background(), "admin_user", PanelUpdateApplyRequest{}); again.Code == 0 || !strings.Contains(again.Msg, "正在重启") {
		t.Fatalf("a second update during restart must be refused: %#v", again)
	}
	if _, err := RestoreSiteBackup(backup); err == nil || !strings.Contains(err.Error(), "正在更新") {
		t.Fatalf("database restore during restart must be refused: %v", err)
	}
	if rollback := softStatus(t).Runtime.Rollback; rollback.Available {
		t.Fatal("rollback must be unavailable while restarting")
	}
}

func TestSoftUpdateReasonCodes(t *testing.T) {
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = otherPub
	cases := []struct {
		name  string
		opts  softOptions
		setup func(*softFixture)
		want  string
	}{
		{"disabled", softOptions{}, func(f *softFixture) {
			f.reconfigure(func(c *UpdateRuntimeConfig) { c.Strategy = UpdateStrategyManual })
		}, softReasonDisabled},
		{"not supervised", softOptions{}, func(f *softFixture) { f.reconfigure(func(c *UpdateRuntimeConfig) { c.Child = child.Info{} }) }, softReasonNotSupervised},
		{"development build", softOptions{}, func(*softFixture) { buildinfo.Version = "dev" }, softReasonDevelopmentBuild},
		{"no update", softOptions{}, func(f *softFixture) { f.tag = "v0.2.0" }, softReasonNoUpdate},
		{"manifest missing", softOptions{}, func(f *softFixture) { f.omitAssets[manifest.SignatureName] = true }, softReasonManifestMissing},
		{"signature invalid", softOptions{signer: otherPriv}, nil, softReasonSignatureInvalid},
		{"manifest invalid", softOptions{repository: "someone/fork"}, nil, softReasonManifestInvalid},
		{"arch unavailable", softOptions{arch: "riscv64"}, nil, softReasonArchUnavailable},
		{"requires image update", softOptions{}, func(f *softFixture) {
			f.reconfigure(func(c *UpdateRuntimeConfig) { c.Child.BuiltinRuntimeABI = 2 })
		}, softReasonRequiresImageUpdate},
		{"upgrade path", softOptions{compat: func() protocol.Compat {
			compat := softCompat
			compat.MinUpgradeFrom = "v0.2.5"
			return compat
		}()}, nil, softReasonUpgradePath},
		{"check failed", softOptions{}, func(f *softFixture) { f.failAssets[manifest.FileName] = http.StatusBadGateway }, softReasonCheckFailed},
		{"quarantined", softOptions{}, func(f *softFixture) {
			st := state.New(state.Base{Version: "v0.2.0"})
			st.Quarantine = []state.QuarantineEntry{{Version: softTargetVersion, Reason: "ready_timeout：迁移卡住"}}
			if err := f.layout.Ensure(); err != nil {
				f.t.Fatal(err)
			}
			if err := state.Save(f.layout.StateFile, st); err != nil {
				f.t.Fatal(err)
			}
		}, softReasonQuarantined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSoftFixture(t, tc.opts)
			if tc.setup != nil {
				tc.setup(f)
			}
			status := checkSoftStatus(t)
			if status.SoftUpdate.Available || status.SoftUpdate.Reason != tc.want {
				t.Fatalf("softUpdate=%+v, want reason %s", status.SoftUpdate, tc.want)
			}
			switch tc.want {
			case softReasonQuarantined:
				if status.Strategy != UpdateStrategySoft || !strings.Contains(status.SoftUpdate.Detail, "迁移卡住") {
					t.Fatalf("quarantined versions stay soft-capable for a forced retry: %+v", status)
				}
			case softReasonNotSupervised:
				if status.Runtime.Mode != "unsupervised" || status.Strategy != UpdateStrategyManual {
					t.Fatalf("status=%+v", status)
				}
			}
		})
	}
}

func TestSoftUpdateReportsUnwritableRuntimeDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newSoftFixture(t, softOptions{})
	parent := filepath.Join(t.TempDir(), "readonly")
	if err := os.MkdirAll(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	f.reconfigure(func(c *UpdateRuntimeConfig) {
		l, _ := layout.Resolve(func(key string) string {
			if key == protocol.EnvRuntimeDir {
				return filepath.Join(parent, "runtime")
			}
			return ""
		})
		c.Layout = l
	})
	if status := checkSoftStatus(t); status.SoftUpdate.Reason != softReasonRuntimeDirUnwritable {
		t.Fatalf("softUpdate=%+v", status.SoftUpdate)
	}
}

func TestApplyRejectsChangedTargetAndRequiresForceForQuarantine(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	if response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{TargetVersion: "v0.2.9"}); response.Code == 0 || !strings.Contains(response.Msg, "版本已变化") {
		t.Fatalf("stale target=%#v", response)
	}
	st := state.New(state.Base{Version: "v0.2.0"})
	st.Quarantine = []state.QuarantineEntry{{Version: softTargetVersion, Reason: "start：自动迁移失败"}}
	if err := f.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(f.layout.StateFile, st); err != nil {
		t.Fatal(err)
	}
	if response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{}); response.Code == 0 || !strings.Contains(response.Msg, "曾试运行失败") {
		t.Fatalf("quarantined without force=%#v", response)
	}
	if response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{Force: true}); response.Code != 0 {
		t.Fatalf("forced retry=%#v", response)
	}
	if job := waitForJob(t, updateStageRestarting, updateStageFailed); job.Stage != updateStageRestarting {
		t.Fatalf("forced retry failed: %+v", job)
	}
}

func TestApplyRejectsConcurrentJobsAndSupportsCancel(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	f.block = make(chan struct{})
	response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{})
	trigger, _ := response.Data.(PanelUpdateTriggerResult)
	if response.Code != 0 {
		t.Fatalf("apply=%#v", response)
	}
	waitForJob(t, updateStageDownloading)
	if second := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{}); second.Code == 0 || !strings.Contains(second.Msg, "已有更新作业") {
		t.Fatalf("concurrent apply=%#v", second)
	}
	if status := softStatus(t); status.SoftUpdate.Reason != softReasonJobRunning || status.Runtime.Rollback.Available {
		t.Fatalf("status during job=%+v", status)
	}
	if cancel := CancelPanelUpdate("wrong-id"); cancel.Code == 0 {
		t.Fatal("cancelling an unknown job must fail")
	}
	if cancel := CancelPanelUpdate(trigger.JobID); cancel.Code != 0 {
		t.Fatalf("cancel=%#v", cancel)
	}
	job := waitForJob(t, updateStageCanceled, updateStageFailed)
	if job.Stage != updateStageCanceled {
		t.Fatalf("job=%+v", job)
	}
	if _, err := os.Stat(f.layout.PendingFile); !errors.Is(err, os.ErrNotExist) || f.restarts.Load() != 0 {
		t.Fatal("a canceled job must not request a restart")
	}
	if again := CancelPanelUpdate(trigger.JobID); again.Code == 0 || !strings.Contains(again.Msg, "已经结束") {
		t.Fatalf("cancel after finish=%#v", again)
	}
	close(f.block)
}

func TestReleaseDigestMismatchBlocksSoftUpdateBeforeDownload(t *testing.T) {
	newSoftFixture(t, softOptions{tamperArchive: true})
	status := checkSoftStatus(t)
	if status.SoftUpdate.Available || status.SoftUpdate.Reason != softReasonManifestInvalid || !strings.Contains(status.SoftUpdate.Detail, "摘要") {
		t.Fatalf("softUpdate=%+v", status.SoftUpdate)
	}
}

func TestTamperedArchiveFailsWithoutSideEffects(t *testing.T) {
	f := newSoftFixture(t, softOptions{tamperArchive: true})
	// 较早的 GitHub API 响应不带 digest：此时篡改只能在下载后的哈希校验中发现。
	f.noDigest = true
	if response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{}); response.Code != 0 {
		t.Fatalf("apply=%#v", response)
	}
	job := waitForJob(t, updateStageFailed, updateStageRestarting)
	if job.Stage != updateStageFailed || !strings.Contains(job.Error, "SHA-256") {
		t.Fatalf("job=%+v", job)
	}
	if _, err := os.Stat(f.layout.PendingFile); !errors.Is(err, os.ErrNotExist) || f.restarts.Load() != 0 {
		t.Fatal("a failed download must not leave a switch request")
	}
	if entries, _ := os.ReadDir(f.layout.ReleasesDir); len(entries) != 0 {
		t.Fatalf("no release may be installed from a tampered archive: %v", entries)
	}
	if err := beginUpdateCommit(); err != nil {
		t.Fatalf("a failed job must not keep the update lock: %v", err)
	}
	endUpdateCommit()
}

func TestApplyChecksDiskSpace(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	f.reconfigure(func(c *UpdateRuntimeConfig) {
		c.FreeBytes = func(string) (uint64, error) { return 1024, nil }
	})
	if response := ApplyPanelUpdate(context.Background(), "admin", PanelUpdateApplyRequest{}); response.Code == 0 || !strings.Contains(response.Msg, "剩余空间不足") {
		t.Fatalf("apply=%#v", response)
	}
}

func TestRollbackAvailabilityFollowsSchemaEpochs(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	if err := f.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	st := state.New(state.Base{Version: "v0.2.0"})
	active := state.Target{Kind: state.KindRelease, Version: softTargetVersion, Commit: softCommit, Dir: "releases/v0.3.0-0123456", SchemaEpoch: 1}
	st.Active = &active
	st.Previous = &state.Target{Kind: state.KindBuiltin, Version: "v0.2.0"}
	if err := state.Save(f.layout.StateFile, st); err != nil {
		t.Fatal(err)
	}
	f.reconfigure(func(c *UpdateRuntimeConfig) { c.Child.RunMode = protocol.RunModeRelease })

	runtimeInfo := softStatus(t).Runtime
	if !runtimeInfo.Rollback.Available || !runtimeInfo.BuiltinRestore.Available || runtimeInfo.PreviousVersion != "v0.2.0" || runtimeInfo.ActiveVersion != softTargetVersion {
		t.Fatalf("runtime=%+v", runtimeInfo)
	}

	// 当前库被声明为需要纪元 2 的读取方：内置版本（纪元 1）不能直接读取。
	if err := model.DB.Exec("UPDATE schema_meta SET value = '2' WHERE key = 'min_reader_epoch'").Error; err != nil {
		t.Fatal(err)
	}
	runtimeInfo = softStatus(t).Runtime
	if runtimeInfo.Rollback.Available || runtimeInfo.BuiltinRestore.Available || !strings.Contains(runtimeInfo.Rollback.Reason, "不兼容") {
		t.Fatalf("incompatible rollback must be refused: %+v", runtimeInfo)
	}
	if response := RequestPanelRollback("admin", "previous"); response.Code == 0 {
		t.Fatal("rollback must be refused when the database is incompatible")
	}
	if err := model.DB.Exec("UPDATE schema_meta SET value = '1' WHERE key = 'min_reader_epoch'").Error; err != nil {
		t.Fatal(err)
	}

	if response := RequestPanelRollback("admin", "sideways"); response.Code == 0 {
		t.Fatal("unknown rollback targets must be rejected")
	}
	response := RequestPanelRollback("admin", "previous")
	result, ok := response.Data.(PanelRollbackResult)
	if response.Code != 0 || !ok || result.TargetVersion != "v0.2.0" || !result.Started {
		t.Fatalf("rollback=%#v", response)
	}
	pending, err := state.LoadPending(f.layout.PendingFile)
	if err != nil || pending.Action != state.ActionRollback || !pending.Target.IsBuiltin() || pending.RequestID != result.RequestID {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if f.restarts.Load() != 1 {
		t.Fatalf("restart requests=%d", f.restarts.Load())
	}
}

func TestStatusUsesCacheWithoutNetwork(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	if status := softStatus(t); status.LatestVersion != "" || status.Job != nil {
		t.Fatalf("status before any check must be empty: %+v", status)
	}
	checkSoftStatus(t)
	f.reconfigureTransport(func(*http.Request) (*http.Response, error) {
		t.Error("status must not access the network")
		return nil, errors.New("network disabled")
	})
	if status := softStatus(t); status.LatestVersion != softTargetVersion || !status.SoftUpdate.Available {
		t.Fatalf("cached status=%+v", status)
	}
}

// reconfigureTransport 只替换 HTTP 传输而保留缓存，用于验证状态接口不访问网络。
func (f *softFixture) reconfigureTransport(fn updateRoundTripFunc) {
	updateRuntime.Lock()
	updateRuntime.config.HTTPClient = &http.Client{Transport: fn}
	updateRuntime.Unlock()
}

func TestEffectiveStrategyMatrix(t *testing.T) {
	sidecar := normalizeUpdateRuntimeConfig(UpdateRuntimeConfig{TriggerURL: "http://updater:8080/v1/update", TriggerToken: "t"})
	none := normalizeUpdateRuntimeConfig(UpdateRuntimeConfig{})
	available := SoftUpdateStatus{Available: true}
	blocked := SoftUpdateStatus{Reason: softReasonRequiresImageUpdate}
	cases := []struct {
		strategy string
		config   UpdateRuntimeConfig
		soft     SoftUpdateStatus
		want     string
	}{
		{UpdateStrategyAuto, none, available, UpdateStrategySoft},
		{UpdateStrategyAuto, sidecar, blocked, UpdateStrategySidecar},
		{UpdateStrategyAuto, none, blocked, UpdateStrategyManual},
		{UpdateStrategySoft, sidecar, blocked, UpdateStrategyManual},
		{UpdateStrategySidecar, sidecar, available, UpdateStrategySidecar},
		{UpdateStrategySidecar, none, available, UpdateStrategyManual},
		{UpdateStrategyManual, sidecar, available, UpdateStrategyManual},
		{UpdateStrategyAuto, none, SoftUpdateStatus{Reason: softReasonQuarantined}, UpdateStrategySoft},
	}
	for _, tc := range cases {
		tc.config.Strategy = tc.strategy
		if got := effectiveStrategy(tc.config, tc.soft); got != tc.want {
			t.Fatalf("strategy=%s soft=%+v sidecar=%v: got %s want %s", tc.strategy, tc.soft, autoUpdateConfigured(tc.config), got, tc.want)
		}
	}
}

func TestRunModeLeavesTrialOnceCommitted(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	if err := f.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	buildinfo.Version = softTargetVersion
	target := state.Target{Kind: state.KindRelease, Version: softTargetVersion, Commit: softCommit, Dir: "releases/v0.3.0-0123456", SchemaEpoch: 1}
	st := state.New(state.Base{Version: "v0.2.0"})
	st.Trial = &state.Trial{Target: target, Action: state.ActionUpgrade, RequestID: "req-1", Attempts: 1}
	if err := state.Save(f.layout.StateFile, st); err != nil {
		t.Fatal(err)
	}
	f.reconfigure(func(c *UpdateRuntimeConfig) {
		c.Child.RunMode = protocol.RunModeTrial
		c.Child.RequestID = "req-1"
	})
	if info := softStatus(t).Runtime; info.Mode != protocol.RunModeTrial || info.TrialVersion != softTargetVersion || info.Rollback.Available {
		t.Fatalf("during trial: %+v", info)
	}
	if _, err := st.CommitTrial(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(f.layout.StateFile, st); err != nil {
		t.Fatal(err)
	}
	info := softStatus(t).Runtime
	if info.Mode != protocol.RunModeRelease || info.ActiveVersion != softTargetVersion || !info.Rollback.Available {
		t.Fatalf("after commit the running trial child must report release mode: %+v", info)
	}
	if info.LastResult == nil || info.LastResult.RequestID != "req-1" || info.LastResult.Outcome != state.OutcomeCommitted {
		t.Fatalf("lastResult=%+v", info.LastResult)
	}
}

func TestManualCheckBypassesCacheWithMinimumInterval(t *testing.T) {
	f := newSoftFixture(t, softOptions{})
	if response := CheckPanelUpdate(context.Background(), false); response.Code != 0 {
		t.Fatalf("check=%#v", response)
	}
	f.tag = "v0.3.1"
	latest := func(force bool) string {
		response := CheckPanelUpdate(context.Background(), force)
		status, ok := response.Data.(PanelUpdateStatus)
		if response.Code != 0 || !ok {
			t.Fatalf("check=%#v", response)
		}
		return status.LatestVersion
	}
	if got := latest(false); got != softTargetVersion {
		t.Fatalf("periodic checks must use the cache, got %s", got)
	}
	if got := latest(true); got != softTargetVersion {
		t.Fatalf("a manual check within 30 seconds must reuse the cache, got %s", got)
	}
	updateRuntime.Lock()
	updateRuntime.status.CheckedAt = time.Now().Add(-time.Minute).UnixMilli()
	updateRuntime.Unlock()
	if got := latest(true); got != "v0.3.1" {
		t.Fatalf("a manual check must refresh a cache older than 30 seconds, got %s", got)
	}
}
