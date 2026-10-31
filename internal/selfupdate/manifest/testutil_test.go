package manifest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Typeflag: typeflag, Mode: 0o755, Linkname: entry.linkname}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

const testVersion = "v0.3.0"
const testCommit = "0123456789abcdef0123456789abcdef01234567"

func validManifest() Manifest {
	root := "go-panel-" + testVersion + "-linux-amd64"
	return Manifest{
		Schema:     Schema,
		Product:    Product,
		Repository: "nXiaoK/go-panel",
		Version:    testVersion,
		Commit:     testCommit,
		BuildTime:  "2026-11-01T00:00:00Z",
		Compat: protocol.Compat{
			RuntimeABI:         1,
			SchemaEpoch:        2,
			MinReaderEpoch:     1,
			SupervisorProtocol: protocol.Range{Min: 1, Max: 1},
			MinUpgradeFrom:     "v0.0.0",
		},
		Artifacts: []Artifact{{
			OS: "linux", Arch: "amd64", Root: root,
			Archive: Archive{Name: root + ".tar.gz", Size: 1234, SHA256: strings.Repeat("a", 64)},
			Files: []File{
				{Path: "go-panel", Size: 10, SHA256: strings.Repeat("b", 64), Mode: ExecutableMode},
				{Path: "node-assets/gost-amd64", Size: 20, SHA256: strings.Repeat("c", 64), Mode: ExecutableMode},
			},
		}},
	}
}

func encodeManifest(t *testing.T, m Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
