package model

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/nXiaoK/go-panel/internal/crypto"
)

func TestDatabaseLoggerDoesNotInterpolateOrdinaryUserPasswordHash(t *testing.T) {
	var logs bytes.Buffer
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "panel.db")), &gorm.Config{
		Logger: newDatabaseLogger(&logs, gormlogger.Info),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatal(err)
	}
	logs.Reset()

	passwordHash, err := crypto.HashPassword("ordinary user password")
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	user := User{
		User:          "ordinary-user",
		Pwd:           passwordHash,
		RoleID:        1,
		ExpTime:       &expiresAt,
		FlowResetTime: 1,
		Status:        UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	replacementHash, err := crypto.HashPassword("replacement ordinary user password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&user).Update("pwd", replacementHash).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), passwordHash) || strings.Contains(logs.String(), replacementHash) {
		t.Fatalf("password hash leaked into database query log: %q", logs.String())
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	user.ID = 0
	user.User = "ordinary-user-error"
	err = db.Create(&user).Error
	if err == nil {
		t.Fatal("database error must still be returned")
	}
	if strings.Contains(logs.String(), passwordHash) {
		t.Fatalf("password hash leaked into database error log: %q", logs.String())
	}
}
