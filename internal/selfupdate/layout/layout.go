// Package layout 解析软更新运行时目录下的各个路径。
//
// supervisor 与面板服务进程必须得到完全相同的结果，因此只依赖 DB_PATH 与 PANEL_RUNTIME_DIR
// 两个环境变量，不读取其他配置。目录结构见 docs/soft-update/README.md §4.3。
package layout

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
)

// defaultDBPath 与 config.Load 的 DB_PATH 默认值保持一致。
const defaultDBPath = "data/flux-panel.db"

// releasesPrefix 是状态文件中版本目录相对路径的固定前缀。
const releasesPrefix = "releases/"

var (
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	releaseDirPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+-[0-9a-f]{7}$`)
)

// Layout 是运行时目录内各文件的绝对路径。
type Layout struct {
	RuntimeDir     string
	StateFile      string
	PendingFile    string
	PendingLock    string
	SupervisorLock string
	RestoreJournal string
	ReleasesDir    string
	StagingDir     string
	BackupsDir     string
	DBPath         string
}

// Resolve 计算运行时目录：PANEL_RUNTIME_DIR 优先，否则为 DB_PATH 所在目录下的 runtime。
// 备份目录固定为 DB_PATH 所在目录下的 backups，与 CreatePreUpdateBackup 保持一致。
func Resolve(getenv func(string) string) (Layout, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	dbPath := strings.TrimSpace(getenv("DB_PATH"))
	if dbPath == "" {
		dbPath = defaultDBPath
	}
	dbAbs, err := filepath.Abs(dbPath)
	if err != nil {
		return Layout{}, fmt.Errorf("解析数据库路径失败: %w", err)
	}
	runtimeDir := strings.TrimSpace(getenv(protocol.EnvRuntimeDir))
	if runtimeDir == "" {
		runtimeDir = filepath.Join(filepath.Dir(dbAbs), "runtime")
	}
	runtimeAbs, err := filepath.Abs(runtimeDir)
	if err != nil {
		return Layout{}, fmt.Errorf("解析运行时目录失败: %w", err)
	}
	return Layout{
		RuntimeDir:     runtimeAbs,
		StateFile:      filepath.Join(runtimeAbs, "state.json"),
		PendingFile:    filepath.Join(runtimeAbs, "pending.json"),
		PendingLock:    filepath.Join(runtimeAbs, "pending.lock"),
		SupervisorLock: filepath.Join(runtimeAbs, "supervisor.lock"),
		RestoreJournal: filepath.Join(runtimeAbs, "db-restore.json"),
		ReleasesDir:    filepath.Join(runtimeAbs, "releases"),
		StagingDir:     filepath.Join(runtimeAbs, "staging"),
		BackupsDir:     filepath.Join(filepath.Dir(dbAbs), "backups"),
		DBPath:         dbAbs,
	}, nil
}

// ReleaseDirName 返回版本目录名 <版本>-<commit 前 7 位>；版本必须是不带元数据的规范稳定版本。
func ReleaseDirName(version, commit string) (string, error) {
	parsed, ok := semver.Parse(version)
	if !ok || parsed.String() != version {
		return "", fmt.Errorf("版本 %q 不是规范的稳定版本", version)
	}
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("commit %q 格式无效", commit)
	}
	return version + "-" + commit[:7], nil
}

// RelativeReleaseDir 返回写入状态文件的相对路径，例如 releases/v0.3.0-abc1234。
func RelativeReleaseDir(name string) string {
	return releasesPrefix + name
}

// ReleasePath 把状态文件中的相对路径解析为绝对路径。只接受 releases/<版本目录名>；
// 目录已存在时，解析符号链接后仍必须位于 releases 目录之内，防止借链接逃逸。
func (l Layout) ReleasePath(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	name, ok := strings.CutPrefix(rel, releasesPrefix)
	if !ok || !releaseDirPattern.MatchString(name) {
		return "", fmt.Errorf("版本目录 %q 不合法", rel)
	}
	path := filepath.Join(l.ReleasesDir, name)
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("解析版本目录失败: %w", err)
	}
	root, err := filepath.EvalSymlinks(l.ReleasesDir)
	if err != nil {
		return "", fmt.Errorf("解析 releases 目录失败: %w", err)
	}
	if filepath.Dir(resolved) != root {
		return "", fmt.Errorf("版本目录 %q 指向 releases 目录之外", rel)
	}
	return path, nil
}

// BackupPath 把 backups/<文件名> 形式的相对路径解析到备份目录内，拒绝路径穿越。
func (l Layout) BackupPath(rel string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	name, ok := strings.CutPrefix(rel, "backups/")
	if !ok || name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("备份文件 %q 不合法", rel)
	}
	return filepath.Join(l.BackupsDir, name), nil
}

// Ensure 创建运行时目录（权限 0700）；目录已存在但权限过宽时收紧为 0700。
func (l Layout) Ensure() error {
	for _, dir := range []string{l.RuntimeDir, l.ReleasesDir, l.StagingDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建运行时目录 %s 失败: %w", dir, err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("检查运行时目录 %s 失败: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s 不是目录", dir)
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(dir, 0o700); err != nil {
				return fmt.Errorf("收紧运行时目录 %s 权限失败: %w", dir, err)
			}
			log.Printf("运行时目录 %s 权限过宽（%o），已收紧为 0700", dir, info.Mode().Perm())
		}
	}
	return nil
}
