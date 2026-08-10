package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/model"
)

const MaxSiteBackupUploadSize int64 = 256 << 20

// restoreDrainTimeout bounds how long a restore waits for in-flight database
// operations to finish before it gives up rather than block forever.
const restoreDrainTimeout = 30 * time.Second

var siteBackupMu sync.Mutex

// SiteBackupFile 是一次备份生成的临时文件。
type SiteBackupFile struct {
	Path     string
	Filename string
	tmpDir   string
}

// Cleanup 删除备份时生成的临时目录。
func (b *SiteBackupFile) Cleanup() {
	if b == nil || b.tmpDir == "" {
		return
	}
	_ = os.RemoveAll(b.tmpDir)
}

// SiteRestoreSummary 返回恢复结果的关键信息。
type SiteRestoreSummary struct {
	RestoredAt       int64  `json:"restoredAt"`
	PreRestoreBackup string `json:"preRestoreBackup,omitempty"`
}

// CreateSiteBackup 创建一个一致性的 SQLite 备份文件。
func CreateSiteBackup() (*SiteBackupFile, error) {
	siteBackupMu.Lock()
	defer siteBackupMu.Unlock()

	if model.DB == nil {
		return nil, errors.New("数据库未初始化")
	}

	tmpDir, err := os.MkdirTemp("", "flux-panel-backup-*")
	if err != nil {
		return nil, fmt.Errorf("创建备份临时目录失败: %w", err)
	}

	backupPath := filepath.Join(tmpDir, "flux-panel.db")
	if err := model.DB.Exec("VACUUM INTO " + sqliteStringLiteral(backupPath)).Error; err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("生成数据库备份失败: %w", err)
	}
	if err := validateSQLiteBackup(backupPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("备份文件校验失败: %w", err)
	}

	return &SiteBackupFile{
		Path:     backupPath,
		Filename: "flux-panel-backup-" + time.Now().Format("20060102-150405") + ".db",
		tmpDir:   tmpDir,
	}, nil
}

// RestoreSiteBackup 用上传的备份文件替换当前数据库。
// 进入维护窗口：拒绝新的数据库操作、等待在途操作完成后再替换句柄，
// 避免恢复瞬间的并发写入丢失或落到已关闭的连接上。
func RestoreSiteBackup(uploadedPath string) (*SiteRestoreSummary, error) {
	if err := validateSQLiteBackup(uploadedPath); err != nil {
		return nil, fmt.Errorf("备份文件无效: %w", err)
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), restoreDrainTimeout)
	defer cancel()
	endMaintenance, err := model.Gate.BeginMaintenance(drainCtx)
	if err != nil {
		return nil, fmt.Errorf("等待在途操作完成超时，恢复已取消: %w", err)
	}
	defer endMaintenance()

	// 先关闭数据库操作门控并排空已进入的下载/R2 备份，再获取快照互斥锁。
	// HTTP 与调度备份路径都遵循 Gate → siteBackupMu，避免恢复反向持锁造成互等。
	siteBackupMu.Lock()
	defer siteBackupMu.Unlock()

	dbPath := model.DatabasePath()
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("数据库路径未初始化")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	if model.DB != nil {
		_ = model.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
	}
	if err := model.Close(); err != nil {
		return nil, fmt.Errorf("关闭当前数据库失败: %w", err)
	}
	// The restored database can reuse node IDs for different secrets. Clear the
	// entire authenticated context cache before any replacement or rollback.
	invalidateAllSecretCache()

	preRestoreBackup := ""
	restoreTime := time.Now()
	if fileExists(dbPath) {
		preRestoreBackup = dbPath + ".pre-restore-" + restoreTime.Format("20060102-150405") + ".bak"
		if err := os.Rename(dbPath, preRestoreBackup); err != nil {
			_ = model.Init(dbPath)
			return nil, fmt.Errorf("保存恢复前数据库失败: %w", err)
		}
	}

	removeSQLiteSidecars(dbPath)
	if err := copyFile(uploadedPath, dbPath, 0o600); err != nil {
		if rollbackErr := rollbackDatabaseRestore(dbPath, preRestoreBackup); rollbackErr != nil {
			return nil, fmt.Errorf("写入恢复文件失败: %w；回滚失败: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("写入恢复文件失败: %w", err)
	}

	if err := model.Init(dbPath); err != nil {
		if rollbackErr := rollbackDatabaseRestore(dbPath, preRestoreBackup); rollbackErr != nil {
			return nil, fmt.Errorf("恢复后的数据库初始化失败: %w；回滚失败: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("恢复后的数据库初始化失败: %w", err)
	}

	summary := &SiteRestoreSummary{RestoredAt: restoreTime.UnixMilli()}
	if preRestoreBackup != "" {
		summary.PreRestoreBackup = filepath.Base(preRestoreBackup)
	}
	return summary, nil
}

func rollbackDatabaseRestore(dbPath, preRestoreBackup string) error {
	removeSQLiteSidecars(dbPath)
	_ = os.Remove(dbPath)
	if preRestoreBackup != "" && fileExists(preRestoreBackup) {
		if err := os.Rename(preRestoreBackup, dbPath); err != nil {
			return err
		}
	}
	return model.Init(dbPath)
}

func validateSQLiteBackup(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("备份文件不能是目录")
	}
	if info.Size() == 0 {
		return errors.New("备份文件为空")
	}
	if info.Size() > MaxSiteBackupUploadSize {
		return fmt.Errorf("备份文件超过 %d MB", MaxSiteBackupUploadSize>>20)
	}

	header := make([]byte, 16)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if _, err := io.ReadFull(f, header); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if string(header) != "SQLite format 3\x00" {
		return errors.New("不是有效的 SQLite 数据库文件")
	}

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	var integrity string
	if err := db.Raw("PRAGMA integrity_check").Scan(&integrity).Error; err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("数据库完整性检查失败: " + integrity)
	}

	for _, table := range []string{"user", "node", "vite_config"} {
		var count int64
		if err := db.Raw("SELECT count(1) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("缺少必要数据表: %s", table)
		}
	}
	return nil
}

func sqliteStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func removeSQLiteSidecars(dbPath string) {
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
