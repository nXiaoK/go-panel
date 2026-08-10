package nftgeneration

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteAndReadActiveMarker(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state", "active-table")
	table := "flux_panel_g_00112233445566778899aabbccddeeff"
	if err := WriteActiveMarker(path, table); err != nil {
		t.Fatalf("WriteActiveMarker: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("marker mode=%#o, want 0600", got)
	}
	got, err := ReadActiveMarker(path)
	if err != nil {
		t.Fatalf("ReadActiveMarker: %v", err)
	}
	if got != table {
		t.Fatalf("marker=%q, want %q", got, table)
	}
}

func TestWriteActiveMarkerValidatesBeforeReplacingExistingMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "active-table")
	if err := os.WriteFile(path, []byte(LegacyTable+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveMarker(path, "other"); err == nil {
		t.Fatal("WriteActiveMarker accepted invalid table")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != LegacyTable+"\n" {
		t.Fatalf("existing marker changed to %q", raw)
	}
}

func TestReadActiveMarkerRejectsInvalidAmbiguousOrOversizedContent(t *testing.T) {
	t.Parallel()

	validGeneration := "flux_panel_g_00112233445566778899aabbccddeeff"
	cases := map[string]string{
		"empty":             "",
		"spaces":            " " + LegacyTable + "\n",
		"trailing spaces":   LegacyTable + " \n",
		"multiple lines":    LegacyTable + "\n" + LegacyTable + "\n",
		"extra newline":     LegacyTable + "\n\n",
		"carriage return":   LegacyTable + "\r\n",
		"invalid table":     "other\n",
		"uppercase hex":     strings.ToUpper(validGeneration) + "\n",
		"oversized content": validGeneration + strings.Repeat("x", 128),
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "active-table")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadActiveMarker(path); err == nil || got != "" {
				t.Fatalf("ReadActiveMarker=(%q, %v), want empty table and error", got, err)
			}
		})
	}
}

func TestReadActiveMarkerAcceptsSingleLineWithOrWithoutFinalNewline(t *testing.T) {
	t.Parallel()

	for _, content := range []string{LegacyTable, LegacyTable + "\n"} {
		path := filepath.Join(t.TempDir(), "active-table")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadActiveMarker(path); err != nil || got != LegacyTable {
			t.Fatalf("ReadActiveMarker(%q)=(%q, %v)", content, got, err)
		}
	}
}

func TestWriteActiveMarkerOrdersDurabilityOperations(t *testing.T) {
	t.Parallel()

	f := &fakeMarkerFile{name: "/state/.active.tmp"}
	d := &fakeMarkerDirectory{}
	var calls []string
	f.calls = &calls
	d.calls = &calls
	ops := markerFileOps{
		mkdirAll: func(string, os.FileMode) error { calls = append(calls, "mkdir"); return nil },
		createTemp: func(string, string) (markerTempFile, error) {
			calls = append(calls, "create")
			return f, nil
		},
		rename: func(oldPath, newPath string) error {
			calls = append(calls, "rename")
			if oldPath != f.name || newPath != "/state/active" {
				t.Fatalf("rename(%q, %q)", oldPath, newPath)
			}
			return nil
		},
		remove: func(string) error { calls = append(calls, "remove"); return nil },
		openDir: func(path string) (markerSyncCloser, error) {
			calls = append(calls, "open-dir")
			if path != "/state" {
				t.Fatalf("openDir(%q)", path)
			}
			return d, nil
		},
	}
	if err := writeActiveMarker("/state/active", LegacyTable, ops); err != nil {
		t.Fatalf("writeActiveMarker: %v", err)
	}
	want := []string{"mkdir", "create", "write", "file-sync", "file-close", "rename", "open-dir", "dir-sync", "dir-close", "remove"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v, want %v", calls, want)
	}
	if got, want := f.content.String(), LegacyTable+"\n"; got != want {
		t.Fatalf("temp content=%q, want %q", got, want)
	}
}

func TestWriteActiveMarkerDoesNotRenameBeforeTempIsDurableAndClosed(t *testing.T) {
	t.Parallel()

	for _, failAt := range []string{"write", "file-sync", "file-close"} {
		failAt := failAt
		t.Run(failAt, func(t *testing.T) {
			t.Parallel()
			f := &fakeMarkerFile{name: "/state/.active.tmp", failAt: failAt}
			var renamed bool
			ops := markerFileOps{
				mkdirAll:   func(string, os.FileMode) error { return nil },
				createTemp: func(string, string) (markerTempFile, error) { return f, nil },
				rename:     func(string, string) error { renamed = true; return nil },
				remove:     func(string) error { return nil },
				openDir: func(string) (markerSyncCloser, error) {
					t.Fatal("opened directory before rename")
					return nil, nil
				},
			}
			if err := writeActiveMarker("/state/active", LegacyTable, ops); err == nil {
				t.Fatalf("writeActiveMarker succeeded at %s failure", failAt)
			}
			if renamed {
				t.Fatal("marker renamed before durable temp completion")
			}
			if !f.closed {
				t.Fatal("temp file was not closed on failure")
			}
		})
	}
}

func TestWriteActiveMarkerReportsRenameAndDirectorySyncFailures(t *testing.T) {
	t.Parallel()

	for _, failAt := range []string{"rename", "open-dir", "dir-sync", "dir-close"} {
		failAt := failAt
		t.Run(failAt, func(t *testing.T) {
			t.Parallel()
			f := &fakeMarkerFile{name: "/state/.active.tmp"}
			d := &fakeMarkerDirectory{failAt: failAt}
			ops := markerFileOps{
				mkdirAll:   func(string, os.FileMode) error { return nil },
				createTemp: func(string, string) (markerTempFile, error) { return f, nil },
				rename: func(string, string) error {
					if failAt == "rename" {
						return errors.New("rename failed")
					}
					return nil
				},
				remove: func(string) error { return nil },
				openDir: func(string) (markerSyncCloser, error) {
					if failAt == "open-dir" {
						return nil, errors.New("open dir failed")
					}
					return d, nil
				},
			}
			if err := writeActiveMarker("/state/active", LegacyTable, ops); err == nil {
				t.Fatalf("writeActiveMarker succeeded at %s failure", failAt)
			}
		})
	}
}

type fakeMarkerFile struct {
	name    string
	failAt  string
	calls   *[]string
	content strings.Builder
	closed  bool
}

func (f *fakeMarkerFile) Write(p []byte) (int, error) {
	f.record("write")
	if f.failAt == "write" {
		return 0, errors.New("write failed")
	}
	return f.content.Write(p)
}
func (f *fakeMarkerFile) Sync() error {
	f.record("file-sync")
	if f.failAt == "file-sync" {
		return errors.New("sync failed")
	}
	return nil
}
func (f *fakeMarkerFile) Close() error {
	f.record("file-close")
	f.closed = true
	if f.failAt == "file-close" {
		return errors.New("close failed")
	}
	return nil
}
func (f *fakeMarkerFile) Name() string { return f.name }
func (f *fakeMarkerFile) record(call string) {
	if f.calls != nil {
		*f.calls = append(*f.calls, call)
	}
}

type fakeMarkerDirectory struct {
	failAt string
	calls  *[]string
}

func (d *fakeMarkerDirectory) Sync() error {
	if d.calls != nil {
		*d.calls = append(*d.calls, "dir-sync")
	}
	if d.failAt == "dir-sync" {
		return errors.New("directory sync failed")
	}
	return nil
}
func (d *fakeMarkerDirectory) Close() error {
	if d.calls != nil {
		*d.calls = append(*d.calls, "dir-close")
	}
	if d.failAt == "dir-close" {
		return errors.New("directory close failed")
	}
	return nil
}

var _ io.Writer = (*fakeMarkerFile)(nil)
