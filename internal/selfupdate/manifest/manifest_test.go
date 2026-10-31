package manifest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseAcceptsValidManifestAndUnknownFields(t *testing.T) {
	raw := encodeManifest(t, validManifest())
	withExtra := strings.Replace(string(raw), `"schema":1`, `"schema":1,"futureField":{"x":1}`, 1)
	m, err := Parse([]byte(withExtra))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := m.ArtifactFor("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := artifact.File("go-panel"); !ok || file.Size != 10 {
		t.Fatalf("go-panel entry=%+v ok=%v", file, ok)
	}
	if _, err := m.ArtifactFor("linux", "riscv64"); !errors.Is(err, ErrArtifactMissing) {
		t.Fatalf("missing platform err=%v", err)
	}
}

func TestParseRejectsTamperedFields(t *testing.T) {
	cases := map[string]func(*Manifest){
		"schema 0":              func(m *Manifest) { m.Schema = 0 },
		"product":               func(m *Manifest) { m.Product = "other" },
		"repository":            func(m *Manifest) { m.Repository = "no-slash" },
		"prerelease":            func(m *Manifest) { m.Version = "v0.3.0-rc.1" },
		"version metadata":      func(m *Manifest) { m.Version = "v0.3.0+meta" },
		"short commit":          func(m *Manifest) { m.Commit = "abc1234" },
		"build time":            func(m *Manifest) { m.BuildTime = "" },
		"runtime abi":           func(m *Manifest) { m.Compat.RuntimeABI = 0 },
		"min reader > schema":   func(m *Manifest) { m.Compat.MinReaderEpoch = 3 },
		"protocol range":        func(m *Manifest) { m.Compat.SupervisorProtocol.Max = 0 },
		"min upgrade":           func(m *Manifest) { m.Compat.MinUpgradeFrom = "dev" },
		"no artifacts":          func(m *Manifest) { m.Artifacts = nil },
		"duplicate platform":    func(m *Manifest) { m.Artifacts = append(m.Artifacts, m.Artifacts[0]) },
		"root":                  func(m *Manifest) { m.Artifacts[0].Root = "go-panel-v9.9.9-linux-amd64" },
		"archive name":          func(m *Manifest) { m.Artifacts[0].Archive.Name = "evil.tar.gz" },
		"archive size":          func(m *Manifest) { m.Artifacts[0].Archive.Size = MaxArchiveBytes + 1 },
		"archive hash":          func(m *Manifest) { m.Artifacts[0].Archive.SHA256 = strings.Repeat("A", 64) },
		"path traversal":        func(m *Manifest) { m.Artifacts[0].Files[1].Path = "../x" },
		"nested node asset":     func(m *Manifest) { m.Artifacts[0].Files[1].Path = "node-assets/a/b" },
		"dot node asset":        func(m *Manifest) { m.Artifacts[0].Files[1].Path = "node-assets/.." },
		"other file":            func(m *Manifest) { m.Artifacts[0].Files[1].Path = "compose.yml" },
		"duplicate file":        func(m *Manifest) { m.Artifacts[0].Files[1] = m.Artifacts[0].Files[0] },
		"missing binary":        func(m *Manifest) { m.Artifacts[0].Files = m.Artifacts[0].Files[1:] },
		"mode 0777":             func(m *Manifest) { m.Artifacts[0].Files[0].Mode = "0777" },
		"oversized file":        func(m *Manifest) { m.Artifacts[0].Files[0].Size = DefaultLimits.MaxFileBytes + 1 },
		"empty file":            func(m *Manifest) { m.Artifacts[0].Files[0].Size = 0 },
		"file hash":             func(m *Manifest) { m.Artifacts[0].Files[0].SHA256 = "short" },
		"uppercase platform os": func(m *Manifest) { m.Artifacts[0].OS = "Linux" },
	}
	for name, mutate := range cases {
		m := validManifest()
		mutate(&m)
		if _, err := Parse(encodeManifest(t, m)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err=%v, want ErrInvalid", name, err)
		}
	}
	m := validManifest()
	m.Schema = 2
	if _, err := Parse(encodeManifest(t, m)); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("schema 2 err=%v, want ErrSchemaTooNew", err)
	}
	if _, err := Parse([]byte(strings.Repeat(" ", MaxManifestBytes+1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized manifest err=%v", err)
	}
}

func TestVerifyAndParseChecksSignatureAndRepository(t *testing.T) {
	pub, priv := newTestKey(t)
	ring := NewKeyRing(pub)
	raw := encodeManifest(t, validManifest())
	sig, err := EncodeSignatures(Sign(raw, priv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAndParse(raw, sig, ring, "nxiaok/GO-PANEL"); err != nil {
		t.Fatalf("valid manifest rejected (repository match must ignore case): %v", err)
	}
	if _, err := VerifyAndParse(raw, sig, ring, "someone/fork"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-repository manifest err=%v", err)
	}
	tampered := []byte(strings.Replace(string(raw), testVersion, "v0.3.1", 1))
	if _, err := VerifyAndParse(tampered, sig, ring, "nXiaoK/go-panel"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("tampered manifest err=%v, want ErrUntrusted", err)
	}
	if _, err := VerifyAndParse(raw, sig, NewKeyRing(), "nXiaoK/go-panel"); !errors.Is(err, ErrNoTrustedKeys) {
		t.Fatalf("empty key ring err=%v, want ErrNoTrustedKeys", err)
	}
}

func TestVerifySignatureRules(t *testing.T) {
	pub, priv := newTestKey(t)
	otherPub, otherPriv := newTestKey(t)
	raw := []byte(`{"schema":1}`)

	unknown, _ := EncodeSignatures(Sign(raw, otherPriv))
	if err := Verify(raw, unknown, NewKeyRing(pub)); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("unknown key err=%v", err)
	}
	// 轮换期双签：只要其中一个签名可信即可通过。
	both, _ := EncodeSignatures(Sign(raw, otherPriv), Sign(raw, priv))
	if err := Verify(raw, both, NewKeyRing(pub)); err != nil {
		t.Fatalf("dual-signed manifest rejected: %v", err)
	}
	if err := Verify(raw, both, NewKeyRing(otherPub)); err != nil {
		t.Fatalf("dual-signed manifest rejected by the other key: %v", err)
	}
	// 不带域分隔前缀的签名必须无效。
	wrongDomain := Signature{KeyID: KeyID(pub), Algorithm: Algorithm, Signature: base64.StdEncoding.EncodeToString(ed25519Sign(priv, raw))}
	envelope, _ := EncodeSignatures(wrongDomain)
	if err := Verify(raw, envelope, NewKeyRing(pub)); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("signature without domain separation err=%v", err)
	}
	future, _ := json.Marshal(SignatureEnvelope{Schema: 2, Signatures: []Signature{Sign(raw, priv)}})
	if err := Verify(raw, future, NewKeyRing(pub)); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("future envelope err=%v", err)
	}
	if err := Verify(raw, []byte(`not json`), NewKeyRing(pub)); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("garbage envelope err=%v", err)
	}
	if err := Verify(raw, []byte(strings.Repeat(" ", MaxSignatureBytes+1)), NewKeyRing(pub)); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("oversized envelope err=%v", err)
	}
	if _, err := EncodeSignatures(); err == nil {
		t.Fatal("an envelope without signatures must be refused")
	}
}

func TestKeyParsing(t *testing.T) {
	pub, priv := newTestKey(t)
	parsed, err := ParsePublicKey(base64.StdEncoding.EncodeToString(pub))
	if err != nil || !parsed.Equal(pub) {
		t.Fatalf("ParsePublicKey err=%v", err)
	}
	seed, err := ParsePrivateKeySeed(base64.StdEncoding.EncodeToString(priv.Seed()))
	if err != nil || !seed.Equal(priv) {
		t.Fatalf("ParsePrivateKeySeed err=%v", err)
	}
	if _, err := ParsePublicKey("short"); err == nil {
		t.Fatal("invalid public key accepted")
	}
	if _, err := ParsePrivateKeySeed(base64.StdEncoding.EncodeToString(priv)); err == nil {
		t.Fatal("a 64-byte private key must not be accepted as a seed")
	}
	if len(KeyID(pub)) != 16 || NewKeyRing(pub, pub).Len() != 1 || NewKeyRing([]byte("bad")).Len() != 0 {
		t.Fatal("key ring must deduplicate by key id and ignore malformed keys")
	}
}
