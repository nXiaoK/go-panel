package manifest

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const testRoot = "go-panel-v0.3.0-linux-amd64"

func walkCollect(t *testing.T, archive []byte, limits Limits) (map[string]string, error) {
	t.Helper()
	got := map[string]string{}
	err := WalkArchive(bytes.NewReader(archive), testRoot, limits, func(path string, size int64, body io.Reader) error {
		data, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		got[path] = string(data)
		return nil
	})
	return got, err
}

func TestWalkArchiveVisitsOnlyInstallableFiles(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: testRoot + "/", typeflag: tar.TypeDir},
		{name: testRoot + "/README.md", body: "docs"},
		{name: testRoot + "/node-assets/", typeflag: tar.TypeDir},
		{name: testRoot + "/node-assets/gost-amd64", body: "gost"},
		{name: testRoot + "/go-panel", body: "panel"},
	})
	got, err := walkCollect(t, archive, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["go-panel"] != "panel" || got["node-assets/gost-amd64"] != "gost" {
		t.Fatalf("visited=%v", got)
	}
}

func TestWalkArchiveRejectsUnsafeEntries(t *testing.T) {
	cases := map[string][]tarEntry{
		"absolute path":     {{name: "/etc/passwd", body: "x"}},
		"traversal":         {{name: testRoot + "/../evil", body: "x"}},
		"hidden traversal":  {{name: testRoot + "/node-assets/../../evil", body: "x"}},
		"backslash":         {{name: testRoot + `\go-panel`, body: "x"}},
		"outside root":      {{name: "other-root/go-panel", body: "x"}},
		"symlink":           {{name: testRoot + "/go-panel", typeflag: tar.TypeSymlink, linkname: "/bin/sh"}},
		"hardlink":          {{name: testRoot + "/go-panel", typeflag: tar.TypeLink, linkname: testRoot + "/README.md"}},
		"device":            {{name: testRoot + "/node-assets/dev", typeflag: tar.TypeChar}},
		"fifo":              {{name: testRoot + "/node-assets/fifo", typeflag: tar.TypeFifo}},
		"duplicate":         {{name: testRoot + "/go-panel", body: "a"}, {name: testRoot + "/go-panel", body: "b"}},
		"duplicate cleaned": {{name: testRoot + "/node-assets/gost", body: "a"}, {name: testRoot + "/node-assets//gost", body: "b"}},
		"root as file":      {{name: testRoot, body: "x"}},
	}
	for name, entries := range cases {
		if _, err := walkCollect(t, buildTarGz(t, entries), DefaultLimits); !errors.Is(err, ErrUnsafeArchive) {
			t.Fatalf("%s: err=%v, want ErrUnsafeArchive", name, err)
		}
	}
}

func TestWalkArchiveEnforcesLimits(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: testRoot + "/README.md", body: "0123456789"},
		{name: testRoot + "/go-panel", body: "0123456789"},
	})
	for name, limits := range map[string]Limits{
		"entries": {MaxEntries: 1, MaxFileBytes: 100, MaxTotalBytes: 100},
		"file":    {MaxEntries: 10, MaxFileBytes: 5, MaxTotalBytes: 100},
		// 跳过的文档文件也计入总大小，防止用不安装的大文件制造解压炸弹。
		"total": {MaxEntries: 10, MaxFileBytes: 100, MaxTotalBytes: 15},
	} {
		if _, err := walkCollect(t, archive, limits); !errors.Is(err, ErrUnsafeArchive) {
			t.Fatalf("%s limit: err=%v", name, err)
		}
	}
}

func TestWalkArchiveRejectsTruncatedAndNonGzipData(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{{name: testRoot + "/go-panel", body: string(bytes.Repeat([]byte("x"), 4096))}})
	if _, err := walkCollect(t, archive[:len(archive)/2], DefaultLimits); err == nil {
		t.Fatal("a truncated archive must fail")
	}
	if _, err := walkCollect(t, []byte("plain text"), DefaultLimits); !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("non-gzip err=%v", err)
	}
	if err := WalkArchive(bytes.NewReader(archive), "../root", DefaultLimits, nil); !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("invalid root err=%v", err)
	}
}

func TestDescribeArchiveHashesInstallableFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), testRoot+".tar.gz")
	archive := buildTarGz(t, []tarEntry{
		{name: testRoot + "/node-assets/nft_agent_arm64", body: "agent"},
		{name: testRoot + "/go-panel", body: "panel"},
		{name: testRoot + "/compose.yml", body: "compose"},
	})
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := DescribeArchive(path, testRoot, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{Path: "go-panel", Size: 5, SHA256: sha256Hex("panel"), Mode: ExecutableMode},
		{Path: "node-assets/nft_agent_arm64", Size: 5, SHA256: sha256Hex("agent"), Mode: ExecutableMode},
	}
	if len(files) != len(want) || files[0] != want[0] || files[1] != want[1] {
		t.Fatalf("files=%+v\nwant %+v", files, want)
	}
	size, hash, err := HashFile(path)
	if err != nil || size != int64(len(archive)) || len(hash) != 64 {
		t.Fatalf("HashFile size=%d hash=%q err=%v", size, hash, err)
	}
}
