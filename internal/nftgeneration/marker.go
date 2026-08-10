package nftgeneration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxMarkerFileBytes = len(generationPrefix) + generationBytes*2 + 1

type markerTempFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

type markerSyncCloser interface {
	Sync() error
	Close() error
}

type markerFileOps struct {
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (markerTempFile, error)
	rename     func(string, string) error
	remove     func(string) error
	openDir    func(string) (markerSyncCloser, error)
}

var operatingSystemMarkerOps = markerFileOps{
	mkdirAll: os.MkdirAll,
	createTemp: func(dir, pattern string) (markerTempFile, error) {
		return os.CreateTemp(dir, pattern)
	},
	rename: os.Rename,
	remove: os.Remove,
	openDir: func(path string) (markerSyncCloser, error) {
		return os.Open(path)
	},
}

// WriteActiveMarker atomically replaces the marker with a validated table
// name and makes both the file contents and directory entry durable.
func WriteActiveMarker(path, table string) error {
	return writeActiveMarker(path, table, operatingSystemMarkerOps)
}

func writeActiveMarker(path, table string, ops markerFileOps) error {
	if path == "" {
		return fmt.Errorf("write active nft table marker: empty path")
	}
	if err := ValidateTableName(table); err != nil {
		return fmt.Errorf("write active nft table marker: %w", err)
	}
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create active nft marker directory: %w", err)
	}
	temp, err := ops.createTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create active nft marker temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = ops.remove(tempPath) }()

	if _, err := io.WriteString(temp, table+"\n"); err != nil {
		return errors.Join(
			fmt.Errorf("write active nft marker temp file: %w", err),
			temp.Close(),
		)
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync active nft marker temp file: %w", err),
			temp.Close(),
		)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close active nft marker temp file: %w", err)
	}
	if err := ops.rename(tempPath, path); err != nil {
		return fmt.Errorf("replace active nft table marker: %w", err)
	}

	directory, err := ops.openDir(dir)
	if err != nil {
		return fmt.Errorf("open active nft marker directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(
			wrapMarkerError("sync active nft marker directory", syncErr),
			wrapMarkerError("close active nft marker directory", closeErr),
		)
	}
	return nil
}

func wrapMarkerError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// ReadActiveMarker reads one bounded, unambiguous table-name line and validates
// it before returning the table to an nft command caller.
func ReadActiveMarker(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open active nft table marker: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, int64(maxMarkerFileBytes+1)))
	if err != nil {
		return "", fmt.Errorf("read active nft table marker: %w", err)
	}
	if len(raw) > maxMarkerFileBytes {
		return "", fmt.Errorf("active nft table marker is too large")
	}
	value := string(raw)
	value = strings.TrimSuffix(value, "\n")
	if value == "" {
		return "", fmt.Errorf("active nft table marker is empty")
	}
	if err := ValidateTableName(value); err != nil {
		return "", fmt.Errorf("invalid active nft table marker: %w", err)
	}
	return value, nil
}
