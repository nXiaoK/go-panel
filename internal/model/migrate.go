package model

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/crypto"
)

// DB 全局数据库句柄
var DB *gorm.DB

var databasePath string

var activeBootstrapOptions BootstrapOptions
var hasActiveBootstrapOptions bool

// BootstrapOptions controls administrator creation and legacy credential rotation.
type BootstrapOptions struct {
	Remote           bool
	AdminUsername    string
	AdminPassword    string
	CredentialWriter io.Writer
}

// Init 打开 SQLite、自动迁移并写入种子数据
func Init(dbPath string, options ...BootstrapOptions) error {
	bootstrap := BootstrapOptions{
		AdminUsername:    "admin_user",
		CredentialWriter: io.Discard,
	}
	if len(options) > 0 {
		bootstrap = options[0]
	} else if hasActiveBootstrapOptions && dbPath == databasePath {
		bootstrap = activeBootstrapOptions
	}
	if bootstrap.AdminUsername == "" {
		bootstrap.AdminUsername = "admin_user"
	}
	if bootstrap.CredentialWriter == nil {
		bootstrap.CredentialWriter = io.Discard
	}

	if DB != nil {
		if sqlDB, err := DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		DB = nil
	}

	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录失败: %w", err)
		}
	}

	// WAL 模式 + busy_timeout，缓解并发写锁竞争
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: newDatabaseLogger(os.Stdout, logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("打开 SQLite 失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	// SQLite 单写者：限制为单连接，避免 database is locked
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	// 迁移前预检：历史库中的重复用户名 / 重复 user_tunnel 身份会导致下面的
	// 唯一索引创建失败。这里显式报告冲突行 ID 并拒绝启动，而不是在
	// AutoMigrate 深处抛出难以定位的索引错误。预检只读，不改 schema。
	if err := PreflightSchema(db); err != nil {
		_ = sqlDB.Close()
		return err
	}

	if err := db.AutoMigrate(
		&User{}, &Node{}, &Tunnel{}, &Forward{}, &ForwardExitMember{},
		&UserTunnel{}, &SpeedLimit{}, &StatisticsFlow{}, &TrafficHourly{}, &TrafficTunnelHourly{}, &DataMigration{}, &FlowReporterState{}, &ViteConfig{},
		&ProxyNode{}, &SubscriptionProfile{}, &SubscriptionProfileNode{}, &NodeSyncState{},
	); err != nil {
		return fmt.Errorf("自动迁移失败: %w", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return backfillTrafficHourly(tx)
	}); err != nil {
		_ = sqlDB.Close()
		return err
	}

	// 为 node_sync_state 表出现之前创建的存量节点补一条 pending 同步状态，
	// 保证每个节点都有确定的期望/已应用世代记录（幂等）。
	if err := db.Exec(
		"INSERT INTO node_sync_state (node_id, desired_generation, applied_generation, state, last_error, last_attempt_time)" +
			" SELECT id, 1, 0, 'pending', '', 0 FROM node" +
			" WHERE id NOT IN (SELECT node_id FROM node_sync_state)",
	).Error; err != nil {
		return fmt.Errorf("初始化节点同步状态失败: %w", err)
	}

	if err := seed(db, bootstrap); err != nil {
		_ = sqlDB.Close()
		return err
	}

	DB = db
	databasePath = dbPath
	activeBootstrapOptions = bootstrap
	hasActiveBootstrapOptions = true
	return nil
}

// DatabasePath 返回当前 SQLite 数据库文件路径。
func DatabasePath() string {
	return databasePath
}

// Close 关闭当前数据库连接。
// 注意：刻意保留 DB 句柄不置 nil——恢复备份期间并发请求会得到
// "database is closed" 错误而不是 nil 指针 panic，待 Init 重建后自动恢复。
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// seed 首次启动初始化管理员与基础配置（与 gost.sql 初始数据一致）
func seed(db *gorm.DB, options BootstrapOptions) error {
	if err := ensureAdministrator(db, options); err != nil {
		return err
	}

	var cfgCount int64
	if err := db.Model(&ViteConfig{}).Where("name = ?", "app_name").Count(&cfgCount).Error; err != nil {
		return fmt.Errorf("检查基础配置失败: %w", err)
	}
	if cfgCount == 0 {
		if err := db.Create(&ViteConfig{Name: "app_name", Value: "flux", Time: time.Now().UnixMilli()}).Error; err != nil {
			return fmt.Errorf("初始化基础配置失败: %w", err)
		}
	}
	return nil
}

func ensureAdministrator(db *gorm.DB, options BootstrapOptions) error {
	// Administrator SQL contains password hashes when GORM interpolates parameters.
	// Keep this session silent and propagate every error to the caller instead.
	db = db.Session(&gorm.Session{Logger: logger.Discard})

	var count int64
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("检查管理员失败: %w", err)
	}
	if count == 0 {
		password := options.AdminPassword
		generated := false
		if password == "" {
			if options.Remote {
				return fmt.Errorf("remote bootstrap requires ADMIN_PASSWORD")
			}
			var err error
			password, err = randomBootstrapPassword()
			if err != nil {
				return fmt.Errorf("生成管理员密码失败: %w", err)
			}
			generated = true
		}

		exp := int64(2727251700000)
		pwd, err := crypto.HashPassword(password)
		if err != nil {
			return fmt.Errorf("初始化管理员密码失败: %w", err)
		}
		admin := User{
			User:          options.AdminUsername,
			Pwd:           pwd,
			RoleID:        0,
			ExpTime:       &exp,
			Flow:          99999,
			FlowResetTime: 1,
			Num:           99999,
			CreatedTime:   time.Now().UnixMilli(),
			Status:        1,
			MustChangePwd: boolToInt(generated),
		}
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&admin).Error; err != nil {
				return fmt.Errorf("初始化管理员失败: %w", err)
			}
			if generated {
				if _, err := fmt.Fprintf(options.CredentialWriter,
					"generated administrator credentials: username=%s password=%s\n",
					options.AdminUsername, password); err != nil {
					return fmt.Errorf("输出管理员凭据失败: %w", err)
				}
			}
			return nil
		})
	}

	var administrators []User
	if err := db.Where("role_id = ?", 0).Find(&administrators).Error; err != nil {
		return fmt.Errorf("检查管理员凭据失败: %w", err)
	}
	var legacyIDs []int64
	for _, administrator := range administrators {
		if crypto.VerifyPassword(administrator.Pwd, "admin_user") {
			legacyIDs = append(legacyIDs, administrator.ID)
		}
	}
	if len(legacyIDs) == 0 {
		return nil
	}
	if options.AdminPassword == "" {
		return fmt.Errorf("database with historical admin_user password requires ADMIN_PASSWORD")
	}

	pwd, err := crypto.HashPassword(options.AdminPassword)
	if err != nil {
		return fmt.Errorf("轮换管理员密码失败: %w", err)
	}
	if err := db.Model(&User{}).Where("id IN ?", legacyIDs).Updates(map[string]interface{}{
		"pwd":             pwd,
		"must_change_pwd": 0,
	}).Error; err != nil {
		return fmt.Errorf("轮换管理员密码失败: %w", err)
	}
	return nil
}

func randomBootstrapPassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
