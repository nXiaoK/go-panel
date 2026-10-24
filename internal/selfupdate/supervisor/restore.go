//go:build unix

package supervisor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// sqliteSuffixes 是 SQLite 数据库的主文件与 WAL 旁路文件，必须作为一个整体移动。
var sqliteSuffixes = []string{"", "-wal", "-shm"}

// restoreDatabase 用更新前快照替换当前数据库：先写恢复日志，再把当前库（含 -wal/-shm）原样移入
// backups/failed-update-<版本>-<时间>/，最后复制快照并原子替换。失败库不会被删除，便于人工排查。
func (s *supervisor) restoreDatabase(candidate restoreCandidate) error {
	l := s.cfg.Layout
	backup, err := l.BackupPath(candidate.backupFile)
	if err != nil {
		return err
	}
	if err := s.cfg.ValidateBackup(backup); err != nil {
		return fmt.Errorf("更新前快照 %s 不可用: %w", candidate.backupFile, err)
	}
	now := s.cfg.Now()
	journal := state.RestoreJournal{
		RequestID:  candidate.requestID,
		DBPath:     l.DBPath,
		BackupFile: backup,
		FailedDir:  filepath.Join(l.BackupsDir, fmt.Sprintf("failed-update-%s-%s", safeName(candidate.version), now.Format("20060102-150405"))),
		Phase:      state.RestorePhaseMoving,
		StartedAt:  now.UnixMilli(),
	}
	if err := state.SaveRestoreJournal(l.RestoreJournal, journal); err != nil {
		return fmt.Errorf("写入恢复日志失败: %w", err)
	}
	return s.completeRestore(journal)
}

// completeRestore 从日志记录的阶段继续恢复；所有步骤都可重复执行。
func (s *supervisor) completeRestore(journal state.RestoreJournal) error {
	l := s.cfg.Layout
	if journal.Phase == state.RestorePhaseMoving {
		if err := os.MkdirAll(journal.FailedDir, 0o700); err != nil {
			return err
		}
		for _, suffix := range sqliteSuffixes {
			source := journal.DBPath + suffix
			if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := os.Rename(source, filepath.Join(journal.FailedDir, filepath.Base(source))); err != nil {
				return fmt.Errorf("移出失败的数据库文件 %s 失败: %w", source, err)
			}
		}
		if err := errors.Join(syncDir(journal.FailedDir), syncDir(filepath.Dir(journal.DBPath))); err != nil {
			return err
		}
		journal.Phase = state.RestorePhaseCopying
		if err := state.SaveRestoreJournal(l.RestoreJournal, journal); err != nil {
			return err
		}
	}
	if journal.Phase == state.RestorePhaseCopying {
		// 复制阶段中断时，可能残留半个临时文件或新库的旁路文件，统一清理后重新复制。
		for _, suffix := range sqliteSuffixes[1:] {
			_ = os.Remove(journal.DBPath + suffix)
		}
		tmp := journal.DBPath + ".restore-tmp"
		if err := copyFileSynced(journal.BackupFile, tmp, 0o600); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("复制更新前快照失败: %w", err)
		}
		if err := os.Rename(tmp, journal.DBPath); err != nil {
			return fmt.Errorf("替换数据库失败: %w", err)
		}
		if err := syncDir(filepath.Dir(journal.DBPath)); err != nil {
			return err
		}
		journal.Phase = state.RestorePhaseDone
		if err := state.SaveRestoreJournal(l.RestoreJournal, journal); err != nil {
			return err
		}
	}
	s.logf("已用更新前快照 %s 恢复数据库，失败时的数据库保存在 %s", journal.BackupFile, journal.FailedDir)
	return state.RemoveRestoreJournal(l.RestoreJournal)
}

// recoverInterruptedRestore 在启动任何子进程之前处理上次未完成的恢复：快照仍然完好时继续完成；
// 否则把失败库移回原处并保留日志，要求人工处理。
func (s *supervisor) recoverInterruptedRestore() error {
	l := s.cfg.Layout
	journal, err := state.LoadRestoreJournal(l.RestoreJournal)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("恢复日志 %s 无法读取，请人工确认数据库文件后删除该日志: %w", l.RestoreJournal, err)
	}
	if journal.Phase == state.RestorePhaseDone {
		return state.RemoveRestoreJournal(l.RestoreJournal)
	}
	s.logf("检测到未完成的数据库恢复（阶段 %s），继续处理", journal.Phase)
	validateErr := s.cfg.ValidateBackup(journal.BackupFile)
	if validateErr == nil {
		return s.completeRestore(journal)
	}
	s.logf("更新前快照不可用（%v），撤销恢复", validateErr)
	if _, statErr := os.Lstat(journal.DBPath); errors.Is(statErr, os.ErrNotExist) {
		for _, suffix := range sqliteSuffixes {
			moved := filepath.Join(journal.FailedDir, filepath.Base(journal.DBPath+suffix))
			if _, err := os.Lstat(moved); err == nil {
				if err := os.Rename(moved, journal.DBPath+suffix); err != nil {
					return fmt.Errorf("撤销恢复失败: %w", err)
				}
			}
		}
	}
	return fmt.Errorf("数据库恢复未能完成，已尽量把数据库移回原处；请检查 %s 与 %s 后删除恢复日志 %s",
		journal.DBPath, journal.FailedDir, l.RestoreJournal)
}

func copyFileSynced(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Sync(), out.Close())
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(handle.Sync(), handle.Close())
}

func safeName(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
