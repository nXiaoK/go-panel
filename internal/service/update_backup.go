package service

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

const preUpdateBackupRetention = 5

// CreatePreUpdateBackup 把一致性 SQLite 快照保存到数据库卷内，容器替换后仍可恢复。
func CreatePreUpdateBackup(targetVersion string) (string, error) {
	backup, err := CreateSiteBackup()
	if err != nil {
		return "", err
	}
	defer backup.Cleanup()

	databasePath := strings.TrimSpace(model.DatabasePath())
	if databasePath == "" {
		return "", fmt.Errorf("数据库路径未初始化")
	}
	backupDir := filepath.Join(filepath.Dir(databasePath), "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("创建更新备份目录失败: %w", err)
	}
	filename := fmt.Sprintf(
		"pre-update-%s-%s.db",
		safeBackupFilenamePart(targetVersion),
		time.Now().Format("20060102-150405.000"),
	)
	destination := filepath.Join(backupDir, filename)
	if err := copyFile(backup.Path, destination, 0o600); err != nil {
		return "", fmt.Errorf("保存更新备份失败: %w", err)
	}
	if err := prunePreUpdateBackups(backupDir, preUpdateBackupRetention); err != nil {
		// 已生成的安全备份仍然有效，清理旧文件失败不应阻止本次更新。
		log.Printf("清理旧更新备份失败: %v", err)
	}
	return filepath.ToSlash(filepath.Join("backups", filename)), nil
}

func safeBackupFilenamePart(value string) string {
	value = strings.TrimSpace(value)
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

func prunePreUpdateBackups(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "pre-update-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{
			path:    filepath.Join(directory, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	if keep < 0 {
		keep = 0
	}
	if len(candidates) <= keep {
		return nil
	}
	for _, old := range candidates[keep:] {
		if err := os.Remove(old.path); err != nil {
			return err
		}
	}
	return nil
}
