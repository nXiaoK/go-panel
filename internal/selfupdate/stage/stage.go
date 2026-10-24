// Package stage 把已验签的更新包安装到运行时目录 releases/<版本>-<commit7>/，并在每次使用前复验。
//
// 安装流程：在 staging/ 中解包 → 逐文件比对清单的大小与 SHA-256 → 写入清单与签名原文 →
// fsync → 整个目录 rename 到 releases/ → 最后写入 .installed 标记。缺少标记的目录一律视为未完成。
package stage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// InstalledMarker 是安装完成后最后写入的标记文件。
const InstalledMarker = ".installed"

// ErrNotInstalled 表示版本目录不存在或没有完整安装。
var ErrNotInstalled = errors.New("版本目录未完整安装")

// Verifier 描述复验所需的信任根与平台。
type Verifier struct {
	KeyRing    manifest.KeyRing
	Repository string
	GOOS       string
	GOARCH     string
}

// Source 是一次安装的输入：已验签的清单及其原文，以及已校验过整体哈希的压缩包。
type Source struct {
	Manifest    *manifest.Manifest
	RawManifest []byte
	RawSig      []byte
	ArchivePath string
}

type installedInfo struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	ArchiveSHA256 string `json:"archiveSha256"`
	InstalledAt   int64  `json:"installedAt"`
}

// Install 安装一个版本并返回可写入 pending.json 的目标。目标目录已存在且复验通过时直接复用。
func Install(ctx context.Context, l layout.Layout, src Source, v Verifier) (state.Target, error) {
	m := src.Manifest
	if m == nil {
		return state.Target{}, errors.New("缺少更新清单")
	}
	artifact, err := m.ArtifactFor(v.GOOS, v.GOARCH)
	if err != nil {
		return state.Target{}, err
	}
	name, err := layout.ReleaseDirName(m.Version, m.Commit)
	if err != nil {
		return state.Target{}, err
	}
	rel := layout.RelativeReleaseDir(name)
	target := filepath.Join(l.ReleasesDir, name)
	if err := l.Ensure(); err != nil {
		return state.Target{}, err
	}

	if _, err := os.Lstat(target); err == nil {
		if _, installed, verifyErr := VerifyInstalled(l, rel, v); verifyErr == nil {
			return installed, nil
		}
		// 旧目录不完整或被篡改：先改名再删除，避免删除过程中被当作有效版本。
		stale := filepath.Join(l.ReleasesDir, fmt.Sprintf(".%s.invalid-%d", name, time.Now().UnixNano()))
		if err := os.Rename(target, stale); err != nil {
			return state.Target{}, fmt.Errorf("移除无效的版本目录失败: %w", err)
		}
		_ = os.RemoveAll(stale)
	} else if !errors.Is(err, os.ErrNotExist) {
		return state.Target{}, err
	}

	staging, err := os.MkdirTemp(l.StagingDir, "install-*")
	if err != nil {
		return state.Target{}, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	defer os.RemoveAll(staging)
	extract := filepath.Join(staging, name)
	if err := os.Mkdir(extract, 0o755); err != nil {
		return state.Target{}, err
	}
	if err := extractArtifact(ctx, src.ArchivePath, artifact, extract); err != nil {
		return state.Target{}, err
	}
	for fileName, data := range map[string][]byte{manifest.FileName: src.RawManifest, manifest.SignatureName: src.RawSig} {
		if err := writeSynced(filepath.Join(extract, fileName), data, 0o644); err != nil {
			return state.Target{}, err
		}
	}
	for _, dir := range []string{filepath.Join(extract, "node-assets"), extract} {
		if err := syncDir(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return state.Target{}, err
		}
	}
	if err := os.Rename(extract, target); err != nil {
		return state.Target{}, fmt.Errorf("安装版本目录失败: %w", err)
	}
	if err := syncDir(l.ReleasesDir); err != nil {
		return state.Target{}, err
	}
	marker, err := json.Marshal(installedInfo{
		Version:       m.Version,
		Commit:        m.Commit,
		ArchiveSHA256: artifact.Archive.SHA256,
		InstalledAt:   time.Now().UnixMilli(),
	})
	if err != nil {
		return state.Target{}, err
	}
	if err := writeSynced(filepath.Join(target, InstalledMarker), marker, 0o644); err != nil {
		return state.Target{}, err
	}
	if err := syncDir(target); err != nil {
		return state.Target{}, err
	}
	return targetFor(m, rel), nil
}

func extractArtifact(ctx context.Context, archivePath string, artifact *manifest.Artifact, extract string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	written := make(map[string]bool, len(artifact.Files))
	err = manifest.WalkArchive(archive, artifact.Root, manifest.DefaultLimits, func(rel string, size int64, body io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		want, ok := artifact.File(rel)
		if !ok {
			return fmt.Errorf("%w：更新包包含清单未列出的文件 %s", manifest.ErrUnsafeArchive, rel)
		}
		if size != want.Size {
			return fmt.Errorf("%s 大小 %d 与清单的 %d 不一致", rel, size, want.Size)
		}
		destination := filepath.Join(extract, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		hash := sha256.New()
		copied, copyErr := io.Copy(io.MultiWriter(file, hash), body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			return fmt.Errorf("写出 %s 失败: %w", rel, err)
		}
		if copied != want.Size || hex.EncodeToString(hash.Sum(nil)) != want.SHA256 {
			return fmt.Errorf("%s 的内容与清单不一致", rel)
		}
		written[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for _, file := range artifact.Files {
		if !written[file.Path] {
			return fmt.Errorf("更新包缺少清单中的文件 %s", file.Path)
		}
	}
	return nil
}

// VerifyInstalled 复验已安装的版本目录：要求 .installed 存在，签名可信，目录名与清单一致，
// 清单列出的每个文件都是普通文件（不是符号链接）、可执行，且大小与 SHA-256 一致。
func VerifyInstalled(l layout.Layout, rel string, v Verifier) (*manifest.Manifest, state.Target, error) {
	dir, err := l.ReleasePath(rel)
	if err != nil {
		return nil, state.Target{}, err
	}
	markerRaw, err := os.ReadFile(filepath.Join(dir, InstalledMarker))
	if err != nil {
		return nil, state.Target{}, fmt.Errorf("%w：%v", ErrNotInstalled, err)
	}
	var marker installedInfo
	if err := json.Unmarshal(markerRaw, &marker); err != nil {
		return nil, state.Target{}, fmt.Errorf("%w：安装标记损坏", ErrNotInstalled)
	}
	raw, err := readLimited(filepath.Join(dir, manifest.FileName), manifest.MaxManifestBytes)
	if err != nil {
		return nil, state.Target{}, err
	}
	sig, err := readLimited(filepath.Join(dir, manifest.SignatureName), manifest.MaxSignatureBytes)
	if err != nil {
		return nil, state.Target{}, err
	}
	m, err := manifest.VerifyAndParse(raw, sig, v.KeyRing, v.Repository)
	if err != nil {
		return nil, state.Target{}, err
	}
	name, err := layout.ReleaseDirName(m.Version, m.Commit)
	if err != nil {
		return nil, state.Target{}, err
	}
	if filepath.Base(dir) != name || marker.Version != m.Version || marker.Commit != m.Commit {
		return nil, state.Target{}, fmt.Errorf("版本目录 %s 与其中的清单 %s 不一致", rel, name)
	}
	artifact, err := m.ArtifactFor(v.GOOS, v.GOARCH)
	if err != nil {
		return nil, state.Target{}, err
	}
	for _, file := range artifact.Files {
		if err := verifyFile(filepath.Join(dir, filepath.FromSlash(file.Path)), file); err != nil {
			return nil, state.Target{}, err
		}
	}
	return m, targetFor(m, rel), nil
}

func verifyFile(path string, want manifest.File) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("缺少文件 %s: %w", want.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s 不是普通文件", want.Path)
	}
	if info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("%s 没有执行权限", want.Path)
	}
	if info.Size() != want.Size {
		return fmt.Errorf("%s 大小与清单不一致", want.Path)
	}
	_, sum, err := manifest.HashFile(path)
	if err != nil {
		return err
	}
	if sum != want.SHA256 {
		return fmt.Errorf("%s 的 SHA-256 与清单不一致", want.Path)
	}
	return nil
}

func targetFor(m *manifest.Manifest, rel string) state.Target {
	return state.Target{
		Kind:        state.KindRelease,
		Version:     m.Version,
		Commit:      m.Commit,
		Dir:         rel,
		SchemaEpoch: m.Compat.SchemaEpoch,
	}
}

// Prune 删除 releases/ 中不在 keep（releases/<名称> 形式）里的目录，以及中断安装留下的隐藏目录。
func Prune(l layout.Layout, keep []string) error {
	entries, err := os.ReadDir(l.ReleasesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if slices.Contains(keep, layout.RelativeReleaseDir(entry.Name())) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(l.ReleasesDir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CleanStaging 清空 staging/；只在没有更新作业运行时调用（supervisor 启动时）。
func CleanStaging(l layout.Layout) error {
	entries, err := os.ReadDir(l.StagingDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(l.StagingDir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// BinaryPath 返回版本目录中面板程序的绝对路径。
func BinaryPath(releaseDir string) string {
	return filepath.Join(releaseDir, manifest.BinaryPath)
}

// NodeAssetDir 返回版本目录中节点程序目录的绝对路径。
func NodeAssetDir(releaseDir string) string {
	return filepath.Join(releaseDir, "node-assets")
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrNotInstalled, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超过 %d 字节", filepath.Base(path), limit)
	}
	return data, nil
}

func writeSynced(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(handle.Sync(), handle.Close())
}
