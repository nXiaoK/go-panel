package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
)

// Pending 是写给 supervisor 的切换请求。upgrade 必须指向已安装的版本目录并带更新前快照，
// rollback 可指向覆盖版本或内置版本，use-builtin 只能指向内置版本。
type Pending struct {
	Schema          int    `json:"schema"`
	RequestID       string `json:"requestId"`
	Action          string `json:"action"`
	Target          Target `json:"target"`
	BackupFile      string `json:"backupFile,omitempty"`
	ReadyTimeoutSec int    `json:"readyTimeoutSec,omitempty"`
	RequestedAt     int64  `json:"requestedAt"`
	RequestedBy     string `json:"requestedBy,omitempty"`
}

// Validate 检查动作与目标的组合；路径与签名由 supervisor 在消费时另行复验。
func (p Pending) Validate() error {
	if strings.TrimSpace(p.RequestID) == "" {
		return errors.New("切换请求缺少 requestId")
	}
	switch p.Action {
	case ActionUpgrade:
		if p.Target.IsBuiltin() {
			return errors.New("升级请求必须指向覆盖版本")
		}
		if strings.TrimSpace(p.BackupFile) == "" {
			return errors.New("升级请求必须带更新前快照")
		}
	case ActionRollback:
	case ActionUseBuiltin:
		if !p.Target.IsBuiltin() {
			return errors.New("恢复内置版本的请求不能指向覆盖版本")
		}
	default:
		return fmt.Errorf("未知的切换动作 %q", p.Action)
	}
	if !p.Target.IsBuiltin() {
		if p.Target.Kind != KindRelease {
			return fmt.Errorf("未知的目标类型 %q", p.Target.Kind)
		}
		if _, ok := semver.Parse(p.Target.Version); !ok {
			return fmt.Errorf("目标版本 %q 不是稳定版本", p.Target.Version)
		}
		if strings.TrimSpace(p.Target.Commit) == "" || !strings.HasPrefix(p.Target.Dir, "releases/") {
			return errors.New("覆盖版本缺少 commit 或版本目录")
		}
	}
	if p.ReadyTimeoutSec < 0 {
		return errors.New("就绪超时不能为负数")
	}
	return nil
}

// LoadPending 读取 pending.json；文件不存在时错误满足 errors.Is(err, os.ErrNotExist)。
func LoadPending(path string) (Pending, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Pending{}, err
	}
	pending, err := decode[Pending](raw)
	if err != nil {
		return Pending{}, err
	}
	if err := pending.Validate(); err != nil {
		return Pending{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return pending, nil
}

// WritePending 在持有 lockPath 文件锁时原子写入 pending.json，避免服务进程与运维命令互相覆盖一半。
func WritePending(path, lockPath string, pending Pending) error {
	pending.Schema = Schema
	if err := pending.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	unlock, err := Lock(lockPath)
	if err != nil {
		return fmt.Errorf("锁定切换请求失败: %w", err)
	}
	defer func() { _ = unlock() }()
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

// RemovePending 删除 pending.json；文件不存在不视为错误。
func RemovePending(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(dirOf(path))
}
