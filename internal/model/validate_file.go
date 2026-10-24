package model

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ValidateSQLiteFile 检查文件是否为完整可用的面板数据库：SQLite 文件头、PRAGMA integrity_check，
// 以及 user、node、vite_config 三张必要数据表。恢复上传备份与软更新自动恢复快照前都调用它。
func ValidateSQLiteFile(path string) error {
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
