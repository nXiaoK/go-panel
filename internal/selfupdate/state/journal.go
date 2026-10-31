package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// 自动恢复数据库的阶段：moving 表示正在把失败库移入 failed 目录；copying 表示正在用快照写入新库。
const (
	RestorePhaseMoving  = "moving"
	RestorePhaseCopying = "copying"
	RestorePhaseDone    = "done"
)

// RestoreJournal 是自动恢复数据库的日志。supervisor 在任何文件移动之前写入，
// 进程中途崩溃后据此续做或撤销，保证面板绝不会面对缺失的数据库文件。路径均为绝对路径。
type RestoreJournal struct {
	Schema     int    `json:"schema"`
	RequestID  string `json:"requestId"`
	DBPath     string `json:"dbPath"`
	BackupFile string `json:"backupFile"`
	FailedDir  string `json:"failedDir"`
	Phase      string `json:"phase"`
	StartedAt  int64  `json:"startedAt"`
}

// LoadRestoreJournal 读取日志；文件不存在时错误满足 errors.Is(err, os.ErrNotExist)。
func LoadRestoreJournal(path string) (RestoreJournal, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RestoreJournal{}, err
	}
	journal, err := decode[RestoreJournal](raw)
	if err != nil {
		return RestoreJournal{}, err
	}
	if journal.DBPath == "" || journal.BackupFile == "" || journal.FailedDir == "" {
		return RestoreJournal{}, fmt.Errorf("%w: 恢复日志缺少路径", ErrCorrupt)
	}
	switch journal.Phase {
	case RestorePhaseMoving, RestorePhaseCopying, RestorePhaseDone:
	default:
		return RestoreJournal{}, fmt.Errorf("%w: 未知的恢复阶段 %q", ErrCorrupt, journal.Phase)
	}
	return journal, nil
}

// SaveRestoreJournal 原子写入恢复日志。
func SaveRestoreJournal(path string, journal RestoreJournal) error {
	journal.Schema = Schema
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

// RemoveRestoreJournal 删除恢复日志；文件不存在不视为错误。
func RemoveRestoreJournal(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(dirOf(path))
}
