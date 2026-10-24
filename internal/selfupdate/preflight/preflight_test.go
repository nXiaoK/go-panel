package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func fakeBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go-panel")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

var testCompat = protocol.Compat{RuntimeABI: 1, SchemaEpoch: 2, MinReaderEpoch: 1, SupervisorProtocol: protocol.Range{Min: 1, Max: 1}, MinUpgradeFrom: "v0.2.0"}

const reportJSON = `{"version":"v0.3.0","commit":"0123456789abcdef0123456789abcdef01234567","buildTime":"now","goos":"linux","goarch":"amd64","runtimeAbi":1,"schemaEpoch":2,"minReaderEpoch":1,"supervisorProtocol":{"min":1,"max":1},"minUpgradeFrom":"v0.2.0"}`

func testManifest() *manifest.Manifest {
	return &manifest.Manifest{Version: "v0.3.0", Commit: "0123456789abcdef0123456789abcdef01234567", Compat: testCompat}
}

func TestVersionParsesReportWithoutLeakingSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "must-not-leak")
	binary := fakeBinary(t, `env >&2; printf '%s\n' '`+reportJSON+`'`)
	report, err := Version(context.Background(), binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckReport(report, testManifest()); err != nil {
		t.Fatalf("matching report rejected: %v", err)
	}
	leak := fakeBinary(t, `if [ -n "$JWT_SECRET" ]; then exit 9; fi; printf '%s\n' '`+reportJSON+`'`)
	if _, err := Version(context.Background(), leak); err != nil {
		t.Fatalf("the candidate must not inherit secrets: %v", err)
	}
}

func TestVersionFailures(t *testing.T) {
	cases := map[string]string{
		"non-zero exit": `echo broken >&2; exit 3`,
		"invalid json":  `echo not-json`,
		"huge output":   `yes x | head -c 200000`,
	}
	for name, script := range cases {
		if _, err := Version(context.Background(), fakeBinary(t, script)); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := Version(ctx, fakeBinary(t, `sleep 5`)); err == nil || time.Since(started) > 4*time.Second {
		t.Fatalf("a hanging candidate must time out quickly: err=%v after %s", err, time.Since(started))
	}
	if _, err := Version(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing binary must fail")
	}
}

func TestCheckReportDetectsMismatch(t *testing.T) {
	report := protocol.VersionReport{Version: "v0.3.0", Commit: testManifest().Commit, Compat: testCompat}
	for name, mutate := range map[string]func(*protocol.VersionReport){
		"version": func(r *protocol.VersionReport) { r.Version = "v0.3.1" },
		"commit":  func(r *protocol.VersionReport) { r.Commit = strings.Repeat("f", 40) },
		"compat":  func(r *protocol.VersionReport) { r.SchemaEpoch = 3 },
	} {
		r := report
		mutate(&r)
		if err := CheckReport(r, testManifest()); !errors.Is(err, ErrMismatch) {
			t.Fatalf("%s err=%v", name, err)
		}
	}
}

func TestCheckManifestCompatibility(t *testing.T) {
	info := child.Info{Supervised: true, Protocol: 1, BuiltinRuntimeABI: 1}
	if err := CheckManifest(testManifest(), "v0.2.5", info); err != nil {
		t.Fatalf("compatible manifest rejected: %v", err)
	}
	cases := []struct {
		name    string
		current string
		info    child.Info
		want    error
	}{
		{"unsupervised", "v0.2.5", child.Info{}, ErrRequiresImageUpdate},
		{"runtime abi", "v0.2.5", child.Info{Supervised: true, Protocol: 1, BuiltinRuntimeABI: 2}, ErrRequiresImageUpdate},
		{"protocol", "v0.2.5", child.Info{Supervised: true, Protocol: 2, BuiltinRuntimeABI: 1}, ErrRequiresImageUpdate},
		{"upgrade path", "v0.1.9", info, ErrUpgradePathUnsupported},
		{"dev build", "dev", info, ErrUpgradePathUnsupported},
	}
	for _, tc := range cases {
		if err := CheckManifest(testManifest(), tc.current, tc.info); !errors.Is(err, tc.want) {
			t.Fatalf("%s err=%v want %v", tc.name, err, tc.want)
		}
	}
}

func TestMigrateCheckOutcomes(t *testing.T) {
	ok := fakeBinary(t, `echo 'db logs go to stderr' >&2; echo '{"ok":true,"durationMs":1500,"schemaEpoch":2,"minReaderEpoch":1}'`)
	report, err := MigrateCheck(context.Background(), ok, "/tmp/copy.db", time.Minute)
	if err != nil || report.Duration() != 1500*time.Millisecond || report.SchemaEpoch != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	args := fakeBinary(t, `[ "$1" = "migrate-check" ] && [ "$2" = "--db" ] && [ "$3" = "/tmp/copy.db" ] && [ "$4" = "--json" ] || exit 7; echo '{"ok":true}'`)
	if _, err := MigrateCheck(context.Background(), args, "/tmp/copy.db", time.Minute); err != nil {
		t.Fatalf("migrate-check arguments are wrong: %v", err)
	}
	tooNew := fakeBinary(t, `echo '{"ok":false,"error":"too new"}'; exit 78`)
	if _, err := MigrateCheck(context.Background(), tooNew, "/tmp/copy.db", time.Minute); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("exit 78 err=%v", err)
	}
	failed := fakeBinary(t, `echo '{"ok":false,"error":"自动迁移失败: duplicate column"}'; exit 1`)
	if _, err := MigrateCheck(context.Background(), failed, "/tmp/copy.db", time.Minute); err == nil || !strings.Contains(err.Error(), "duplicate column") {
		t.Fatalf("failed migration err=%v", err)
	}
	garbage := fakeBinary(t, `echo done`)
	if _, err := MigrateCheck(context.Background(), garbage, "/tmp/copy.db", time.Minute); err == nil {
		t.Fatal("unparseable success output must fail")
	}
	slow := fakeBinary(t, `sleep 5`)
	if _, err := MigrateCheck(context.Background(), slow, "/tmp/copy.db", 200*time.Millisecond); err == nil {
		t.Fatal("a slow migration must time out")
	}
}

func TestReadyTimeoutFor(t *testing.T) {
	for migration, want := range map[time.Duration]time.Duration{
		0:                defaultReadyTimeout,
		time.Second:      minReadyTimeout,
		5 * time.Minute:  16 * time.Minute,
		20 * time.Minute: maxReadyTimeout,
	} {
		if got := ReadyTimeoutFor(migration); got != want {
			t.Fatalf("ReadyTimeoutFor(%s)=%s want %s", migration, got, want)
		}
	}
}
