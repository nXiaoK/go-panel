package state

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
)

// renameFile 仅供测试注入失败；生产代码始终使用 os.Rename。
var renameFile = os.Rename

// writeFileAtomic 先写同目录临时文件并 fsync，再 rename 覆盖目标并 fsync 目录，
// 断电或崩溃后读者只会看到旧内容或完整的新内容。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := dirOf(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameFile(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(handle.Sync(), handle.Close())
}

func dirOf(path string) string {
	return filepath.Dir(path)
}

// versionGreater 判断稳定版本 a 是否高于 b；任一参数无法解析时返回 false。
func versionGreater(a, b string) bool {
	left, ok := semver.Parse(a)
	if !ok {
		return false
	}
	right, ok := semver.Parse(b)
	if !ok {
		return false
	}
	return semver.Compare(left, right) > 0
}
