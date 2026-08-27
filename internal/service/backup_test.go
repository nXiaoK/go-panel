package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateAndRestoreSiteBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	updateOrCreateConfig("app_name", "before-backup")
	backup, err := CreateSiteBackup()
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	defer backup.Cleanup()

	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if backup.Filename == "" {
		t.Fatal("backup filename should not be empty")
	}

	updateOrCreateConfig("app_name", "after-backup")
	if got := GetConfigValue("app_name"); got != "after-backup" {
		t.Fatalf("expected updated config before restore, got %q", got)
	}

	summary, err := RestoreSiteBackup(backup.Path)
	if err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if summary == nil || summary.RestoredAt == 0 {
		t.Fatalf("unexpected restore summary: %#v", summary)
	}
	if summary.PreRestoreBackup == "" {
		t.Fatal("restore should keep a pre-restore backup")
	}
	if got := GetConfigValue("app_name"); got != "before-backup" {
		t.Fatalf("expected restored config, got %q", got)
	}
}

func TestRestoreSiteBackupInvalidatesAuthenticatedNodeContext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	now := time.Now().UnixMilli()
	oldNode := model.Node{
		Name: "pre-restore-node", Secret: "pre-restore-secret", IP: "192.0.2.21", ServerIP: "192.0.2.21",
		PortSta: 10000, PortEnd: 20000, ForwardMode: forwardModeGost, CreatedTime: now, Status: 1,
	}
	if err := model.DB.Create(&oldNode).Error; err != nil {
		t.Fatalf("create old node: %v", err)
	}
	InvalidateSecretCache(oldNode.Secret)
	defer InvalidateSecretCache(oldNode.Secret)
	if _, err := AuthenticateNodeSecret(oldNode.Secret); err != nil {
		t.Fatalf("prime old authenticated context: %v", err)
	}

	backup, err := CreateSiteBackup()
	if err != nil {
		t.Fatalf("create replacement backup: %v", err)
	}
	defer backup.Cleanup()
	restoredDB, err := gorm.Open(sqlite.Open(backup.Path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	if err := restoredDB.Delete(&model.Node{}, oldNode.ID).Error; err != nil {
		t.Fatalf("replace old node: %v", err)
	}
	restoredNode := model.Node{
		ID: oldNode.ID, Name: "restored-node", Secret: "post-restore-secret", IP: "192.0.2.22", ServerIP: "192.0.2.22",
		PortSta: 20001, PortEnd: 30000, ForwardMode: forwardModeNftables, CreatedTime: now, Status: 1,
	}
	if err := restoredDB.Create(&restoredNode).Error; err != nil {
		t.Fatalf("create restored node: %v", err)
	}
	restoredSQLDB, err := restoredDB.DB()
	if err != nil {
		t.Fatalf("restored sql db: %v", err)
	}
	if err := restoredSQLDB.Close(); err != nil {
		t.Fatalf("close restored db: %v", err)
	}

	if _, err := RestoreSiteBackup(backup.Path); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	if _, err := AuthenticateNodeSecret(oldNode.Secret); !errors.Is(err, ErrInvalidNodeSecret) {
		t.Fatalf("old secret remained authenticated after restore: %v", err)
	}
	node, err := AuthenticateNodeSecret(restoredNode.Secret)
	if err != nil {
		t.Fatalf("authenticate restored node: %v", err)
	}
	if node != (AuthenticatedNode{ID: restoredNode.ID, ForwardMode: forwardModeNftables}) {
		t.Fatalf("restored node context=%+v", node)
	}
}

func TestRestoreSiteBackupRejectsInvalidFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	badPath := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(badPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write bad backup: %v", err)
	}

	if _, err := RestoreSiteBackup(badPath); err == nil {
		t.Fatal("expected invalid backup to be rejected")
	}
}

func TestRestoreSiteBackupMigratesLegacySQLiteBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	updateOrCreateConfig("app_name", "current-db")

	legacyPath := filepath.Join(t.TempDir(), "legacy-panel.db")
	createLegacySiteBackupDB(t, legacyPath)

	summary, err := RestoreSiteBackup(legacyPath)
	if err != nil {
		t.Fatalf("restore legacy backup: %v", err)
	}
	if summary == nil || summary.PreRestoreBackup == "" {
		t.Fatalf("expected pre-restore backup summary, got %#v", summary)
	}
	if got := GetConfigValue("app_name"); got != "legacy-db" {
		t.Fatalf("expected restored legacy config, got %q", got)
	}
	if !model.DB.Migrator().HasTable(&model.ForwardExitMember{}) {
		t.Fatal("forward_exit_member table should be created during restore migration")
	}
	for _, column := range []string{"relay_node_id", "relay_ip"} {
		if !model.DB.Migrator().HasColumn(&model.Tunnel{}, column) {
			t.Fatalf("tunnel.%s column should be created during restore migration", column)
		}
	}
	if !model.DB.Migrator().HasColumn(&model.ForwardExitMember{}, "relay_port") {
		t.Fatal("forward_exit_member.relay_port column should be created during restore migration")
	}
	for _, column := range []string{"target_mode", "active_remote_addr", "exit_mode", "exit_strategy"} {
		if !model.DB.Migrator().HasColumn(&model.Forward{}, column) {
			t.Fatalf("forward.%s column should be created during restore migration", column)
		}
	}
	for _, column := range []string{"in_flow", "out_flow", "total_in_flow", "total_out_flow"} {
		if !model.DB.Migrator().HasColumn(&model.StatisticsFlow{}, column) {
			t.Fatalf("statistics_flow.%s column should be created during restore migration", column)
		}
	}
	if !model.DB.Migrator().HasTable(&model.TrafficHourly{}) {
		t.Fatal("traffic_hourly table should be created during restore migration")
	}
	if !model.DB.Migrator().HasTable(&model.TrafficTunnelHourly{}) {
		t.Fatal("traffic_tunnel_hourly table should be created during restore migration")
	}
	for _, column := range []string{"last_connected_base_url", "last_connected_base_time"} {
		if !model.DB.Migrator().HasColumn(&model.Node{}, column) {
			t.Fatalf("node.%s column should be created during restore migration", column)
		}
	}

	var legacyNode struct {
		LastConnectedBaseURL  string
		LastConnectedBaseTime int64
	}
	if err := model.DB.Raw(
		"SELECT last_connected_base_url, last_connected_base_time FROM node WHERE id = ?", 1,
	).Scan(&legacyNode).Error; err != nil {
		t.Fatalf("read migrated node history: %v", err)
	}
	if legacyNode.LastConnectedBaseURL != "" || legacyNode.LastConnectedBaseTime != 0 {
		t.Fatalf("unexpected migrated node history defaults: %#v", legacyNode)
	}

	var forward struct {
		TargetMode       string
		ActiveRemoteAddr string
		ExitMode         string
		ExitStrategy     string
	}
	if err := model.DB.Raw("SELECT target_mode, active_remote_addr, exit_mode, exit_strategy FROM forward WHERE id = ?", 1).Scan(&forward).Error; err != nil {
		t.Fatalf("read migrated forward: %v", err)
	}
	if forward.TargetMode != "balance" || forward.ActiveRemoteAddr != "" || forward.ExitMode != "single" || forward.ExitStrategy != "fifo" {
		t.Fatalf("unexpected migrated forward defaults: %#v", forward)
	}

	var stats struct {
		InFlow       int64
		OutFlow      int64
		TotalInFlow  int64
		TotalOutFlow int64
	}
	if err := model.DB.Raw("SELECT in_flow, out_flow, total_in_flow, total_out_flow FROM statistics_flow WHERE id = ?", 1).Scan(&stats).Error; err != nil {
		t.Fatalf("read migrated statistics flow: %v", err)
	}
	if stats.InFlow != 0 || stats.OutFlow != 0 || stats.TotalInFlow != 0 || stats.TotalOutFlow != 0 {
		t.Fatalf("unexpected migrated statistics defaults: %#v", stats)
	}
	var hourly model.TrafficHourly
	if err := model.DB.Where("user_id = ?", 1).First(&hourly).Error; err != nil {
		t.Fatalf("read backfilled hourly traffic: %v", err)
	}
	if hourly.InFlow != 512 || hourly.OutFlow != 0 {
		t.Fatalf("unexpected backfilled hourly traffic: %#v", hourly)
	}
}

func TestRestoreSiteBackupPreservesRemoteBootstrapPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath, model.BootstrapOptions{
		Remote:        true,
		AdminUsername: "admin_user",
		AdminPassword: "already secure password",
	}); err != nil {
		t.Fatalf("create current remote db: %v", err)
	}
	if err := model.Init(dbPath, model.BootstrapOptions{
		Remote:        true,
		AdminUsername: "admin_user",
	}); err != nil {
		t.Fatalf("reopen current remote db without long-lived password: %v", err)
	}
	defer model.Close()

	updateOrCreateConfig("app_name", "current-remote-db")
	historicalPath := filepath.Join(t.TempDir(), "historical-default.db")
	createLegacySiteBackupDBWithAdminPassword(t, historicalPath, crypto.Md5("admin_user"))

	if _, err := RestoreSiteBackup(historicalPath); err == nil {
		t.Fatal("remote restore must reject a historical default administrator without ADMIN_PASSWORD")
	}
	if got := GetConfigValue("app_name"); got != "current-remote-db" {
		t.Fatalf("failed restore did not roll back current database, got app_name %q", got)
	}

	if err := model.DB.Model(&model.User{}).Where("role_id = ?", 0).
		Update("pwd", crypto.Md5("admin_user")).Error; err != nil {
		t.Fatalf("set historical password after rollback: %v", err)
	}
	if err := model.Init(dbPath); err == nil {
		t.Fatal("rollback reinitialization must retain the remote bootstrap policy")
	}
}

func createLegacySiteBackupDB(t *testing.T, path string) {
	t.Helper()
	createLegacySiteBackupDBWithAdminPassword(t, path, "legacy-password")
}

func createLegacySiteBackupDBWithAdminPassword(t *testing.T, path, passwordHash string) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open legacy sqlite db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get legacy sql db: %v", err)
	}
	defer sqlDB.Close()

	execLegacySQL(t, db, `CREATE TABLE "user" (
		id integer primary key autoincrement,
		user text not null,
		pwd text not null,
		role_id integer not null,
		exp_time integer not null,
		flow integer not null,
		in_flow integer not null default 0,
		out_flow integer not null default 0,
		flow_reset_time integer not null,
		num integer not null,
		created_time integer not null,
		updated_time integer,
		status integer not null,
		must_change_pwd integer not null default 0,
		login_fail_count integer not null default 0,
		login_locked_until integer
	)`)
	execLegacySQL(t, db, `INSERT INTO "user" (
		id, user, pwd, role_id, exp_time, flow, in_flow, out_flow,
		flow_reset_time, num, created_time, status, must_change_pwd, login_fail_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, "admin_user", passwordHash, 0, int64(2727251700000), int64(1024), int64(0), int64(0),
		int64(1), 10, int64(1700000000000), 1, 0, 0)

	execLegacySQL(t, db, `CREATE TABLE "node" (
		id integer primary key autoincrement,
		name text not null,
		secret text not null,
		ip text,
		server_ip text not null,
		port_sta integer not null,
		port_end integer not null,
		version text,
		http integer not null default 0,
		tls integer not null default 0,
		socks integer not null default 0,
		forward_mode text not null default 'gost',
		created_time integer not null,
		updated_time integer,
		status integer not null
	)`)
	execLegacySQL(t, db, `INSERT INTO "node" (
		id, name, secret, ip, server_ip, port_sta, port_end, http, tls, socks,
		forward_mode, created_time, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, "legacy-node", "secret", "127.0.0.1", "127.0.0.1", 10000, 20000, 0, 0, 0,
		"gost", int64(1700000000000), 1)

	execLegacySQL(t, db, `CREATE TABLE "tunnel" (
		id integer primary key autoincrement,
		name text not null,
		traffic_ratio real not null default 1.0,
		in_node_id integer not null,
		in_ip text not null,
		out_node_id integer not null,
		out_ip text not null,
		type integer not null,
		protocol text,
		flow integer not null,
		tcp_listen_addr text not null default '[::]',
		udp_listen_addr text not null default '[::]',
		interface_name text,
		created_time integer not null,
		updated_time integer not null,
		status integer not null
	)`)
	execLegacySQL(t, db, `INSERT INTO "tunnel" (
		id, name, traffic_ratio, in_node_id, in_ip, out_node_id, out_ip, type,
		flow, created_time, updated_time, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, "legacy-tunnel", 1.0, 1, "127.0.0.1", 1, "127.0.0.1", 1,
		0, int64(1700000000000), int64(1700000000000), 1)

	execLegacySQL(t, db, `CREATE TABLE "forward" (
		id integer primary key autoincrement,
		user_id integer not null,
		user_name text not null,
		name text not null,
		tunnel_id integer not null,
		in_port integer not null,
		out_port integer,
		remote_addr text not null,
		strategy text not null default 'fifo',
		interface_name text,
		in_flow integer not null default 0,
		out_flow integer not null default 0,
		created_time integer not null,
		updated_time integer not null,
		status integer not null,
		inx integer not null default 0
	)`)
	execLegacySQL(t, db, `INSERT INTO "forward" (
		id, user_id, user_name, name, tunnel_id, in_port, out_port, remote_addr,
		strategy, in_flow, out_flow, created_time, updated_time, status, inx
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, "admin_user", "legacy-forward", 1, 10001, 20001, "example.com:443",
		"fifo", int64(0), int64(0), int64(1700000000000), int64(1700000000000), 1, 0)

	execLegacySQL(t, db, `CREATE TABLE "statistics_flow" (
		id integer primary key autoincrement,
		user_id integer not null,
		flow integer not null,
		total_flow integer not null,
		time text not null,
		created_time integer not null
	)`)
	execLegacySQL(t, db, `INSERT INTO "statistics_flow" (
		id, user_id, flow, total_flow, time, created_time
	) VALUES (?, ?, ?, ?, ?, ?)`,
		1, 1, int64(512), int64(1024), "2026-07-05 00:00:00", int64(1700000000000))

	execLegacySQL(t, db, `CREATE TABLE "vite_config" (
		id integer primary key autoincrement,
		name text not null,
		value text not null,
		time integer not null
	)`)
	execLegacySQL(t, db, `INSERT INTO "vite_config" (id, name, value, time) VALUES (?, ?, ?, ?)`,
		1, "app_name", "legacy-db", int64(1700000000000))
}

func execLegacySQL(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec legacy sql: %v", err)
	}
}
