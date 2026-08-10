package model

import (
	"bytes"
	"encoding/base64"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/crypto"
)

func TestRemoteBootstrapRequiresPassword(t *testing.T) {
	err := Init(filepath.Join(t.TempDir(), "panel.db"), BootstrapOptions{
		Remote: true, AdminUsername: "admin_user",
	})
	defer Close()
	if err == nil {
		t.Fatal("remote bootstrap must require ADMIN_PASSWORD")
	}
}

func TestBootstrapUsesConfiguredPassword(t *testing.T) {
	var credentials bytes.Buffer
	err := Init(filepath.Join(t.TempDir(), "panel.db"), BootstrapOptions{
		Remote:           true,
		AdminUsername:    "root",
		AdminPassword:    "correct horse battery staple",
		CredentialWriter: &credentials,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer Close()

	var user User
	if err := DB.Where("user = ?", "root").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if !crypto.VerifyPassword(user.Pwd, "correct horse battery staple") {
		t.Fatal("configured password not stored")
	}
	if credentials.Len() != 0 {
		t.Fatalf("configured credentials must not be printed, got %q", credentials.String())
	}
}

func TestLoopbackBootstrapWritesOneRandomCredential(t *testing.T) {
	var credentials bytes.Buffer
	var logs bytes.Buffer
	oldLogWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldLogWriter)

	dbPath := filepath.Join(t.TempDir(), "panel.db")
	options := BootstrapOptions{
		CredentialWriter: &credentials,
	}
	if err := Init(dbPath, options); err != nil {
		t.Fatal(err)
	}
	defer Close()
	if err := Init(dbPath, options); err != nil {
		t.Fatalf("reopen initialized database: %v", err)
	}

	const prefix = "generated administrator credentials: username=admin_user password="
	output := credentials.String()
	if strings.Count(output, prefix) != 1 || strings.Count(strings.TrimSpace(output), "\n") != 0 {
		t.Fatalf("expected exactly one credential line, got %q", output)
	}
	password := strings.TrimSuffix(strings.TrimPrefix(output, prefix), "\n")
	raw, err := base64.RawURLEncoding.DecodeString(password)
	if err != nil {
		t.Fatalf("generated password is not raw URL base64: %v", err)
	}
	if len(raw) != 24 {
		t.Fatalf("generated password decodes to %d bytes, want 24", len(raw))
	}
	if password == "admin_user" {
		t.Fatal("generated password must not be the historical default")
	}

	var user User
	if err := DB.Where("user = ?", "admin_user").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if !crypto.VerifyPassword(user.Pwd, password) {
		t.Fatal("printed generated password does not authenticate")
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), user.Pwd) {
		t.Fatal("generated password or password hash leaked to logs")
	}
}

func TestRemoteBootstrapRejectsHistoricalDefaultPasswordWithoutConfiguredPassword(t *testing.T) {
	dbPath := createDatabaseWithAdminPassword(t, "admin_user")

	err := Init(dbPath, BootstrapOptions{Remote: true, AdminUsername: "admin_user"})
	defer Close()
	if err == nil {
		t.Fatal("remote database with historical default password must require ADMIN_PASSWORD")
	}
}

func TestBootstrapRejectsHistoricalDefaultPasswordWithoutConfiguredPassword(t *testing.T) {
	dbPath := createDatabaseWithAdminPassword(t, "admin_user")

	err := Init(dbPath, BootstrapOptions{AdminUsername: "admin_user"})
	defer Close()
	if err == nil {
		t.Fatal("historical default password must require ADMIN_PASSWORD on loopback")
	}
}

func TestBootstrapRotatesHistoricalDefaultPassword(t *testing.T) {
	dbPath := createDatabaseWithAdminPassword(t, "admin_user")

	if err := Init(dbPath, BootstrapOptions{
		AdminUsername: "admin_user",
		AdminPassword: "replacement password",
	}); err != nil {
		t.Fatal(err)
	}
	defer Close()

	var user User
	if err := DB.Where("user = ?", "admin_user").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if !crypto.VerifyPassword(user.Pwd, "replacement password") {
		t.Fatal("historical default password was not rotated")
	}
	if crypto.VerifyPassword(user.Pwd, "admin_user") {
		t.Fatal("historical default password still authenticates")
	}
}

func TestRemoteBootstrapAllowsExistingSecureAdministratorWithoutConfiguredPassword(t *testing.T) {
	dbPath := createDatabaseWithAdminPassword(t, "already secure password")

	if err := Init(dbPath, BootstrapOptions{Remote: true, AdminUsername: "admin_user"}); err != nil {
		t.Fatal(err)
	}
	defer Close()

	var user User
	if err := DB.Where("user = ?", "admin_user").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if !crypto.VerifyPassword(user.Pwd, "already secure password") {
		t.Fatal("existing secure password changed")
	}
}

func TestBootstrapReturnsHashingErrorWithoutMD5Fallback(t *testing.T) {
	err := Init(filepath.Join(t.TempDir(), "panel.db"), BootstrapOptions{
		Remote:        true,
		AdminUsername: "admin_user",
		AdminPassword: strings.Repeat("a", 73),
	})
	defer Close()
	if err == nil {
		t.Fatal("bcrypt hashing failure must be returned")
	}
}

func TestBootstrapDoesNotLogConfiguredPasswordOrHash(t *testing.T) {
	var logs bytes.Buffer
	dbLogger := gormlogger.New(log.New(&logs, "", 0), gormlogger.Config{
		LogLevel: gormlogger.Info,
		Colorful: false,
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "panel.db")), &gorm.Config{Logger: dbLogger})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatal(err)
	}
	logs.Reset()

	const password = "configured bootstrap password"
	if err := ensureAdministrator(db, BootstrapOptions{
		AdminUsername:    "admin_user",
		AdminPassword:    password,
		CredentialWriter: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	var user User
	if err := db.Session(&gorm.Session{Logger: gormlogger.Discard}).First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), user.Pwd) {
		t.Fatalf("administrator password material leaked to database logs: %q", logs.String())
	}
}

func createDatabaseWithAdminPassword(t *testing.T, password string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath, BootstrapOptions{
		AdminUsername:    "admin_user",
		AdminPassword:    password,
		CredentialWriter: io.Discard,
	}); err != nil {
		t.Fatalf("create database fixture: %v", err)
	}
	if password == "admin_user" {
		if err := DB.Model(&User{}).Where("role_id = ?", 0).
			Update("pwd", crypto.Md5("admin_user")).Error; err != nil {
			t.Fatalf("store historical MD5 fixture: %v", err)
		}
	}
	if err := Close(); err != nil {
		t.Fatalf("close database fixture: %v", err)
	}
	return dbPath
}
