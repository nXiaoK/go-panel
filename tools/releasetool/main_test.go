package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/versioninfo"
)

const (
	testVersion = "v0.3.0"
	testCommit  = "0123456789abcdef0123456789abcdef01234567"
)

func writeBundle(t *testing.T, dir, goarch string, files map[string]string) {
	t.Helper()
	root := "go-panel-" + testVersion + "-linux-" + goarch
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: root + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, root+".tar.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeReleaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		writeBundle(t, dir, arch, map[string]string{
			"go-panel":                      "panel-" + arch,
			"node-assets/gost-amd64":        "gost-amd64",
			"node-assets/gost-arm64":        "gost-arm64",
			"node-assets/nft_agent_amd64":   "agent-amd64",
			"README.md":                     "docs are ignored",
			"compose.yml":                   "compose is ignored",
			"node-assets/nft_agent_arm64":   "agent-arm64",
			"node-assets/nft_flow_reporter": "reporter",
		})
	}
	return dir
}

func newSeed(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, base64.StdEncoding.EncodeToString(priv.Seed())
}

func TestBuildSignVerifyRoundTrip(t *testing.T) {
	dir := fakeReleaseDir(t)
	raw, err := buildManifest(dir, testVersion, strings.ToUpper(testCommit), "2026-11-01T00:00:00Z", "nXiaoK/go-panel", []string{"linux/amd64", "linux/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Commit != testCommit || m.Compat != versioninfo.CurrentCompat() || len(m.Artifacts) != 2 {
		t.Fatalf("manifest=%+v", m)
	}
	amd64, _ := m.ArtifactFor("linux", "amd64")
	if len(amd64.Files) != 6 {
		t.Fatalf("only go-panel and node-assets must be listed, got %+v", amd64.Files)
	}

	pub, seed := newSeed(t)
	nextPub, nextSeed := newSeed(t)
	envelope, keyIDs, err := signManifest(raw, []string{seed, nextSeed})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyIDs) != 2 || strings.Contains(string(envelope), seed) {
		t.Fatalf("keyIDs=%v (the envelope must never contain a seed)", keyIDs)
	}
	for _, ring := range []manifest.KeyRing{manifest.NewKeyRing(pub), manifest.NewKeyRing(nextPub)} {
		if _, err := verifyRelease(raw, envelope, dir, "nXiaoK/go-panel", ring); err != nil {
			t.Fatalf("dual-signed release rejected: %v", err)
		}
	}
	if _, err := verifyRelease(raw, envelope, dir, "someone/fork", manifest.NewKeyRing(pub)); err == nil {
		t.Fatal("a manifest for another repository must be rejected")
	}

	// 发布后替换压缩包：签名仍然有效，但压缩包哈希必须对不上。
	writeBundle(t, dir, "arm64", map[string]string{"go-panel": "tampered"})
	if _, err := verifyRelease(raw, envelope, dir, "nXiaoK/go-panel", manifest.NewKeyRing(pub)); err == nil {
		t.Fatal("a replaced archive must fail verification")
	}
}

func TestBuildManifestRejectsInvalidInput(t *testing.T) {
	dir := fakeReleaseDir(t)
	for name, call := range map[string]func() error{
		"prerelease": func() error {
			_, err := buildManifest(dir, "v0.3.0-rc.1", testCommit, "now", "nXiaoK/go-panel", []string{"linux/amd64"})
			return err
		},
		"short commit": func() error {
			_, err := buildManifest(dir, testVersion, "abc1234", "now", "nXiaoK/go-panel", []string{"linux/amd64"})
			return err
		},
		"missing archive": func() error {
			_, err := buildManifest(dir, testVersion, testCommit, "now", "nXiaoK/go-panel", []string{"linux/riscv64"})
			return err
		},
		"bad platform": func() error {
			_, err := buildManifest(dir, testVersion, testCommit, "now", "nXiaoK/go-panel", []string{"amd64"})
			return err
		},
	} {
		if call() == nil {
			t.Fatalf("%s must fail", name)
		}
	}
	if _, _, err := signManifest([]byte(`{"schema":1}`), []string{"irrelevant"}); err == nil {
		t.Fatal("an invalid manifest must never be signed")
	}
	raw, err := buildManifest(dir, testVersion, testCommit, "now", "nXiaoK/go-panel", []string{"linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := signManifest(raw, []string{"not-a-seed"}); err == nil {
		t.Fatal("a malformed seed must be rejected")
	}
}

func TestSelfCheckComparesReportAndBinaryHash(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, "amd64", map[string]string{"go-panel": "panel-binary"})
	raw, err := buildManifest(dir, testVersion, testCommit, "now", "nXiaoK/go-panel", []string{"linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "go-panel")
	if err := os.WriteFile(binary, []byte("panel-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	report := protocol.VersionReport{Version: testVersion, Commit: testCommit, GOOS: "linux", GOARCH: "amd64", Compat: versioninfo.CurrentCompat()}
	fake := func(r protocol.VersionReport) func(string) (protocol.VersionReport, error) {
		return func(string) (protocol.VersionReport, error) { return r, nil }
	}
	if _, err := selfCheck(raw, binary, fake(report)); err != nil {
		t.Fatalf("matching binary rejected: %v", err)
	}
	wrongCommit := report
	wrongCommit.Commit = strings.Repeat("f", 40)
	wrongCompat := report
	wrongCompat.RuntimeABI++
	wrongArch := report
	wrongArch.GOARCH = "arm64"
	for name, r := range map[string]protocol.VersionReport{"commit": wrongCommit, "compat": wrongCompat, "arch": wrongArch} {
		if _, err := selfCheck(raw, binary, fake(r)); err == nil {
			t.Fatalf("%s mismatch must fail", name)
		}
	}
	if err := os.WriteFile(binary, []byte("other-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := selfCheck(raw, binary, fake(report)); err == nil {
		t.Fatal("a binary whose hash differs from the manifest must fail")
	}
}

func TestKeygenWritesPrivateSeedOnlyToFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "release.key")
	var stdout, stderr strings.Builder
	if code := run([]string{"keygen", "--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode=%v err=%v", info.Mode().Perm(), err)
	}
	seed, _ := os.ReadFile(out)
	if strings.Contains(stdout.String(), strings.TrimSpace(string(seed))) {
		t.Fatal("keygen must never print the private seed")
	}
	priv, err := manifest.ParsePrivateKeySeed(string(seed))
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	if !strings.Contains(stdout.String(), base64.StdEncoding.EncodeToString(pub)) || !strings.Contains(stdout.String(), manifest.KeyID(pub)) {
		t.Fatalf("keygen must print the public key and key id:\n%s", stdout.String())
	}
	if code := run([]string{"keygen", "--out", out}, &stdout, &stderr); code != 1 {
		t.Fatalf("keygen must refuse to overwrite an existing key file, exit=%d", code)
	}
}

func TestSignCommandRequiresKeyEnvironment(t *testing.T) {
	dir := fakeReleaseDir(t)
	raw, err := buildManifest(dir, testVersion, testCommit, "now", "nXiaoK/go-panel", []string{"linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, manifest.FileName)
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, manifest.SignatureName)
	var stdout, stderr strings.Builder
	t.Setenv("RELEASETOOL_TEST_KEY", "")
	if code := run([]string{"sign", "--manifest", manifestPath, "--key-env", "RELEASETOOL_TEST_KEY", "--out", sigPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("missing key exit=%d", code)
	}
	_, seed := newSeed(t)
	t.Setenv("RELEASETOOL_TEST_KEY", seed)
	t.Setenv("RELEASETOOL_TEST_NEXT", "")
	args := []string{"sign", "--manifest", manifestPath, "--key-env", "RELEASETOOL_TEST_KEY", "--optional-key-env", "RELEASETOOL_TEST_NEXT", "--out", sigPath}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("sign exit=%d stderr=%s", code, stderr.String())
	}
	var envelope manifest.SignatureEnvelope
	data, _ := os.ReadFile(sigPath)
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Signatures) != 1 {
		t.Fatalf("envelope=%s err=%v", data, err)
	}
	if strings.Contains(stdout.String()+stderr.String(), seed) {
		t.Fatal("sign must never print the private seed")
	}
}

func TestRunRejectsUsageErrors(t *testing.T) {
	var stdout, stderr strings.Builder
	for _, args := range [][]string{nil, {"bogus"}, {"keygen"}, {"sign", "--manifest", "x", "--out", "y"}, {"verify", "--manifest", "x"}} {
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("run(%q)=%d, want 2", args, code)
		}
	}
}
