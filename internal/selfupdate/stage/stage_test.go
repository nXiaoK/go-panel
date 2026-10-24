package stage

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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

const (
	version = "v0.3.0"
	commit  = "0123456789abcdef0123456789abcdef01234567"
)

type fixture struct {
	layout   layout.Layout
	verifier Verifier
	source   Source
	priv     ed25519.PrivateKey
}

func hexSum(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func writeArchive(t *testing.T, path, root string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
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

// newFixture 构造签名清单与压缩包；archiveFiles 为 nil 时压缩包内容与清单一致。
func newFixture(t *testing.T, archiveFiles map[string]string) fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l, err := layout.Resolve(func(key string) string {
		if key == protocol.EnvRuntimeDir {
			return filepath.Join(t.TempDir(), "runtime")
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{"go-panel": "panel-binary", "node-assets/gost-amd64": "gost-binary"}
	root := "go-panel-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	archivePath := filepath.Join(t.TempDir(), root+".tar.gz")
	if archiveFiles == nil {
		archiveFiles = listed
	}
	writeArchive(t, archivePath, root, archiveFiles)
	size, sum, err := manifest.HashFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	var files []manifest.File
	for _, name := range []string{"go-panel", "node-assets/gost-amd64"} {
		files = append(files, manifest.File{Path: name, Size: int64(len(listed[name])), SHA256: hexSum(listed[name]), Mode: manifest.ExecutableMode})
	}
	m := manifest.Manifest{
		Schema: manifest.Schema, Product: manifest.Product, Repository: "nXiaoK/go-panel",
		Version: version, Commit: commit, BuildTime: "2026-11-01T00:00:00Z",
		Compat: protocol.Compat{RuntimeABI: 1, SchemaEpoch: 2, MinReaderEpoch: 1, SupervisorProtocol: protocol.Range{Min: 1, Max: 1}, MinUpgradeFrom: "v0.0.0"},
		Artifacts: []manifest.Artifact{{
			OS: runtime.GOOS, Arch: runtime.GOARCH, Root: root,
			Archive: manifest.Archive{Name: root + ".tar.gz", Size: size, SHA256: sum},
			Files:   files,
		}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := manifest.EncodeSignatures(manifest.Sign(raw, priv))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		layout:   l,
		verifier: Verifier{KeyRing: manifest.NewKeyRing(pub), Repository: "nXiaoK/go-panel", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		source:   Source{Manifest: &m, RawManifest: raw, RawSig: sig, ArchivePath: archivePath},
		priv:     priv,
	}
}

func TestInstallThenVerify(t *testing.T) {
	f := newFixture(t, nil)
	target, err := Install(context.Background(), f.layout, f.source, f.verifier)
	if err != nil {
		t.Fatal(err)
	}
	want := state.Target{Kind: state.KindRelease, Version: version, Commit: commit, Dir: "releases/v0.3.0-0123456", SchemaEpoch: 2}
	if target != want {
		t.Fatalf("target=%+v want %+v", target, want)
	}
	dir, _ := f.layout.ReleasePath(target.Dir)
	data, err := os.ReadFile(BinaryPath(dir))
	if err != nil || string(data) != "panel-binary" {
		t.Fatalf("installed binary=%q err=%v", data, err)
	}
	if info, err := os.Stat(filepath.Join(NodeAssetDir(dir), "gost-amd64")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("node asset mode=%v err=%v", info.Mode().Perm(), err)
	}
	if _, verified, err := VerifyInstalled(f.layout, target.Dir, f.verifier); err != nil || verified != target {
		t.Fatalf("VerifyInstalled=%+v err=%v", verified, err)
	}
	entries, _ := os.ReadDir(f.layout.StagingDir)
	if len(entries) != 0 {
		t.Fatalf("staging was not cleaned: %v", entries)
	}
}

func TestVerifyInstalledDetectsTampering(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"modified node asset": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "node-assets", "gost-amd64"), []byte("evil-binary"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked binary": func(t *testing.T, dir string) {
			if err := os.Rename(BinaryPath(dir), BinaryPath(dir)+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(BinaryPath(dir)+".real", BinaryPath(dir)); err != nil {
				t.Fatal(err)
			}
		},
		"not executable": func(t *testing.T, dir string) {
			if err := os.Chmod(BinaryPath(dir), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"missing marker": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, InstalledMarker)); err != nil {
				t.Fatal(err)
			}
		},
		"replaced manifest": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte(`{"schema":1}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			target, err := Install(context.Background(), f.layout, f.source, f.verifier)
			if err != nil {
				t.Fatal(err)
			}
			dir, _ := f.layout.ReleasePath(target.Dir)
			tamper(t, dir)
			if _, _, err := VerifyInstalled(f.layout, target.Dir, f.verifier); err == nil {
				t.Fatal("tampering must be detected")
			}
		})
	}
	f := newFixture(t, nil)
	target, err := Install(context.Background(), f.layout, f.source, f.verifier)
	if err != nil {
		t.Fatal(err)
	}
	other := f.verifier
	other.KeyRing = manifest.NewKeyRing()
	if _, _, err := VerifyInstalled(f.layout, target.Dir, other); !errors.Is(err, manifest.ErrNoTrustedKeys) {
		t.Fatalf("verification without trusted keys err=%v", err)
	}
}

func TestInstallReusesValidDirectoryAndReplacesBrokenOne(t *testing.T) {
	f := newFixture(t, nil)
	target, err := Install(context.Background(), f.layout, f.source, f.verifier)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := f.layout.ReleasePath(target.Dir)
	marker, _ := os.ReadFile(filepath.Join(dir, InstalledMarker))
	if _, err := Install(context.Background(), f.layout, f.source, f.verifier); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, InstalledMarker))
	if !bytes.Equal(marker, again) {
		t.Fatal("a verified installation must be reused, not rewritten")
	}
	if err := os.WriteFile(BinaryPath(dir), []byte("corrupted!!!"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), f.layout, f.source, f.verifier); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyInstalled(f.layout, target.Dir, f.verifier); err != nil {
		t.Fatalf("a broken installation must be replaced: %v", err)
	}
	entries, _ := os.ReadDir(f.layout.ReleasesDir)
	if len(entries) != 1 {
		t.Fatalf("stale directories left behind: %v", entries)
	}
}

func TestInstallRejectsArchivesThatDisagreeWithManifest(t *testing.T) {
	cases := map[string]map[string]string{
		"unlisted file": {"go-panel": "panel-binary", "node-assets/gost-amd64": "gost-binary", "node-assets/extra": "surprise"},
		"wrong content": {"go-panel": "panel-binarY", "node-assets/gost-amd64": "gost-binary"},
		"missing file":  {"go-panel": "panel-binary"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, files)
			if _, err := Install(context.Background(), f.layout, f.source, f.verifier); err == nil {
				t.Fatal("install must fail")
			}
			releases, _ := os.ReadDir(f.layout.ReleasesDir)
			staging, _ := os.ReadDir(f.layout.StagingDir)
			if len(releases) != 0 || len(staging) != 0 {
				t.Fatalf("failed install left files: releases=%v staging=%v", releases, staging)
			}
		})
	}
	f := newFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Install(ctx, f.layout, f.source, f.verifier); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled install err=%v", err)
	}
}

func TestPruneAndCleanStaging(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v0.2.0-aaaaaaa", "v0.3.0-bbbbbbb", ".v0.4.0-ccccccc.invalid-1"} {
		if err := os.MkdirAll(filepath.Join(f.layout.ReleasesDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.layout.StagingDir, "install-123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Prune(f.layout, []string{"releases/v0.3.0-bbbbbbb"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(f.layout.ReleasesDir)
	if len(entries) != 1 || entries[0].Name() != "v0.3.0-bbbbbbb" {
		t.Fatalf("after prune: %v", entries)
	}
	if err := CleanStaging(f.layout); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(f.layout.StagingDir); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
}
