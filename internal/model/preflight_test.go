package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openLegacySchemaDB creates a database with the historical non-unique schema
// so we can seed duplicate identities that a fresh AutoMigrate would reject.
func openLegacySchemaDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE user (id INTEGER PRIMARY KEY AUTOINCREMENT, user TEXT, pwd TEXT, role_id INTEGER, exp_time INTEGER, flow INTEGER, in_flow INTEGER DEFAULT 0, out_flow INTEGER DEFAULT 0, flow_reset_time INTEGER, num INTEGER, created_time INTEGER, updated_time INTEGER, status INTEGER, token_version INTEGER DEFAULT 0, must_change_pwd INTEGER DEFAULT 0, login_fail_count INTEGER DEFAULT 0, login_locked_until INTEGER)`).Error; err != nil {
		t.Fatalf("create user table: %v", err)
	}
	if err := db.Exec(`CREATE TABLE user_tunnel (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, tunnel_id INTEGER, speed_id INTEGER, num INTEGER, flow INTEGER, in_flow INTEGER DEFAULT 0, out_flow INTEGER DEFAULT 0, flow_reset_time INTEGER, exp_time INTEGER, status INTEGER)`).Error; err != nil {
		t.Fatalf("create user_tunnel table: %v", err)
	}
	return db
}

func TestPreflightSchemaReportsDuplicateIdentities(t *testing.T) {
	db := openLegacySchemaDB(t)
	db.Exec(`INSERT INTO user (id, user) VALUES (1,'dup'),(2,'dup'),(3,'unique')`)
	db.Exec(`INSERT INTO user_tunnel (id, user_id, tunnel_id) VALUES (1,10,20),(2,10,20),(3,10,21)`)

	err := PreflightSchema(db)
	if err == nil {
		t.Fatal("expected duplicate identity error")
	}
	dup, ok := err.(*DuplicateIdentityError)
	if !ok {
		t.Fatalf("err type=%T, want *DuplicateIdentityError", err)
	}
	if len(dup.UserNameGroups) != 1 {
		t.Fatalf("user name groups=%v, want one group", dup.UserNameGroups)
	}
	if len(dup.UserTunnelGroups) != 1 {
		t.Fatalf("user tunnel groups=%v, want one group", dup.UserTunnelGroups)
	}

	// Preflight must not create either unique index.
	for _, name := range []string{"uidx_user_user", "uidx_user_tunnel_identity"} {
		var count int64
		db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, name).Scan(&count)
		if count != 0 {
			t.Fatalf("preflight created index %s on dirty data", name)
		}
	}
}

func TestPreflightSchemaPassesOnCleanData(t *testing.T) {
	db := openLegacySchemaDB(t)
	db.Exec(`INSERT INTO user (id, user) VALUES (1,'a'),(2,'b')`)
	db.Exec(`INSERT INTO user_tunnel (id, user_id, tunnel_id) VALUES (1,10,20),(2,10,21)`)
	if err := PreflightSchema(db); err != nil {
		t.Fatalf("clean preflight: %v", err)
	}
}

func TestPreflightSchemaSkipsMissingTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := PreflightSchema(db); err != nil {
		t.Fatalf("fresh db preflight should pass: %v", err)
	}
}

func TestInitCreatesUniqueIdentityIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer Close()
	for _, name := range []string{"uidx_user_user", "uidx_user_tunnel_identity"} {
		var count int64
		if err := DB.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, name).Scan(&count).Error; err != nil {
			t.Fatalf("query index %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("unique index %s missing after migration", name)
		}
	}
}

func TestInitRefusesDuplicateIdentityData(t *testing.T) {
	db := openLegacySchemaDB(t)
	db.Exec(`INSERT INTO user (id, user) VALUES (1,'dup'),(2,'dup')`)
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	// Init on the same path must refuse to migrate duplicate legacy identities
	// rather than fail deep inside AutoMigrate's index creation.
	dbPath := db.Dialector.(*sqlite.Dialector).DSN
	err := Init(dbPath)
	if err == nil {
		Close()
		t.Fatal("Init should refuse duplicate identity data")
	}
}
