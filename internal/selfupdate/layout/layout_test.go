package layout

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestResolveDerivesRuntimeDirFromDatabasePath(t *testing.T) {
	l, err := Resolve(envMap(map[string]string{"DB_PATH": "/app/data/flux-panel.db"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Layout{
		RuntimeDir:     "/app/data/runtime",
		StateFile:      "/app/data/runtime/state.json",
		PendingFile:    "/app/data/runtime/pending.json",
		PendingLock:    "/app/data/runtime/pending.lock",
		SupervisorLock: "/app/data/runtime/supervisor.lock",
		RestoreJournal: "/app/data/runtime/db-restore.json",
		ReleasesDir:    "/app/data/runtime/releases",
		StagingDir:     "/app/data/runtime/staging",
		BackupsDir:     "/app/data/backups",
		DBPath:         "/app/data/flux-panel.db",
	}
	if l != want {
		t.Fatalf("layout=%+v\nwant  =%+v", l, want)
	}
}

func TestResolvePrefersExplicitRuntimeDirAndDefaultsDatabase(t *testing.T) {
	l, err := Resolve(envMap(map[string]string{protocol.EnvRuntimeDir: "/srv/panel-runtime"}))
	if err != nil {
		t.Fatal(err)
	}
	if l.RuntimeDir != "/srv/panel-runtime" || l.StateFile != "/srv/panel-runtime/state.json" {
		t.Fatalf("explicit runtime dir ignored: %+v", l)
	}
	if !filepath.IsAbs(l.DBPath) || filepath.Base(l.DBPath) != "flux-panel.db" || filepath.Base(filepath.Dir(l.DBPath)) != "data" {
		t.Fatalf("default DB_PATH not applied: %q", l.DBPath)
	}
}

func TestReleaseDirName(t *testing.T) {
	name, err := ReleaseDirName("v0.3.0", "ABCDEF0123456789abcdef0123456789abcdef01")
	if err != nil || name != "v0.3.0-abcdef0" {
		t.Fatalf("name=%q err=%v", name, err)
	}
	for _, tc := range []struct{ version, commit string }{
		{"0.3.0", "abcdef0"}, {"v0.3.0+meta", "abcdef0"}, {"v0.3.0-rc.1", "abcdef0"},
		{"v0.3.0", "abc"}, {"v0.3.0", "../etc"}, {"v0.3.0", "zzzzzzz"},
	} {
		if _, err := ReleaseDirName(tc.version, tc.commit); err == nil {
			t.Fatalf("ReleaseDirName(%q,%q) must fail", tc.version, tc.commit)
		}
	}
}

func TestReleasePathRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	l, err := Resolve(envMap(map[string]string{protocol.EnvRuntimeDir: root}))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	if path, err := l.ReleasePath("releases/v0.3.0-abcdef0"); err != nil || path != filepath.Join(l.ReleasesDir, "v0.3.0-abcdef0") {
		t.Fatalf("valid path=%q err=%v", path, err)
	}
	for _, rel := range []string{"../x", "releases/../../etc", "/etc/passwd", "releases/v0.3.0-abcdef0/../../x", "releases/", "staging/v0.3.0-abcdef0", "releases/v0.3.0"} {
		if _, err := l.ReleasePath(rel); err == nil {
			t.Fatalf("ReleasePath(%q) must fail", rel)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(l.ReleasesDir, "v0.4.0-abcdef0")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReleasePath("releases/v0.4.0-abcdef0"); err == nil {
		t.Fatal("a symlink pointing outside releases/ must be rejected")
	}
}

func TestBackupPathStaysInsideBackupsDir(t *testing.T) {
	l, err := Resolve(envMap(map[string]string{"DB_PATH": "/app/data/flux-panel.db"}))
	if err != nil {
		t.Fatal(err)
	}
	if path, err := l.BackupPath("backups/pre-update-v0.3.0-20261101.db"); err != nil || path != "/app/data/backups/pre-update-v0.3.0-20261101.db" {
		t.Fatalf("path=%q err=%v", path, err)
	}
	for _, rel := range []string{"backups/../flux-panel.db", "backups/a/b.db", "../x.db", "backups/", "flux-panel.db"} {
		if _, err := l.BackupPath(rel); err == nil {
			t.Fatalf("BackupPath(%q) must fail", rel)
		}
	}
}

func TestEnsureTightensPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := Resolve(envMap(map[string]string{protocol.EnvRuntimeDir: root}))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.RuntimeDir, l.ReleasesDir, l.StagingDir} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%v err=%v, want 0700", dir, info.Mode().Perm(), err)
		}
	}
}
