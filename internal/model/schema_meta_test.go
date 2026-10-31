package model

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
)

func openRawSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeGormDB(db) })
	return db
}

func TestInitRecordsSchemaEpochsIdempotently(t *testing.T) {
	version := buildinfo.Version
	buildinfo.Version = "v0.2.0"
	t.Cleanup(func() { buildinfo.Version = version })

	path := filepath.Join(t.TempDir(), "panel.db")
	for i := 0; i < 2; i++ {
		if err := Init(path); err != nil {
			t.Fatal(err)
		}
		values, err := ReadSchemaMeta(DB)
		if err != nil {
			t.Fatal(err)
		}
		if values.SchemaEpoch != SchemaEpoch || values.MinReaderEpoch != MinReaderEpoch || values.LastWriterVersion != "v0.2.0" || values.UpdatedAt == 0 {
			t.Fatalf("run %d recorded %+v", i, values)
		}
		if err := Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInitRecordsEpochsForLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	// 模拟早于纪元记录的旧库：表结构齐全但没有 schema_meta。
	if err := DB.Exec("DROP TABLE schema_meta").Error; err != nil {
		t.Fatal(err)
	}
	if values, err := ReadSchemaMeta(DB); err != nil || values != (SchemaMetaValues{}) {
		t.Fatalf("missing table must read as zero values: %+v err=%v", values, err)
	}
	if err := Init(path); err != nil {
		t.Fatalf("legacy database must migrate: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
	values, err := ReadSchemaMeta(DB)
	if err != nil || values.SchemaEpoch != SchemaEpoch || values.MinReaderEpoch != MinReaderEpoch {
		t.Fatalf("legacy migration recorded %+v err=%v", values, err)
	}
}

func TestInitRefusesDatabaseRequiringNewerReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	raw := openRawSQLite(t, path)
	for _, statement := range []string{
		"CREATE TABLE schema_meta (`key` TEXT PRIMARY KEY, `value` TEXT NOT NULL DEFAULT '')",
		"INSERT INTO schema_meta (`key`, `value`) VALUES ('schema_epoch', '99'), ('min_reader_epoch', '99'), ('last_writer_version', 'v9.0.0')",
	} {
		if err := raw.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	closeGormDB(raw)

	err := Init(path)
	var tooNew *SchemaTooNewError
	if !errors.As(err, &tooNew) {
		t.Fatalf("Init err=%v, want SchemaTooNewError", err)
	}
	if tooNew.DBMinReaderEpoch != 99 || tooNew.LastWriterVersion != "v9.0.0" || tooNew.BinarySchemaEpoch != SchemaEpoch {
		t.Fatalf("error details=%+v", tooNew)
	}
	if DB != nil {
		t.Fatal("a refused database must not become the global handle")
	}
	check := openRawSQLite(t, path)
	if check.Migrator().HasTable("user") {
		t.Fatal("AutoMigrate must not run on a database that requires a newer reader")
	}
}

func TestReadSchemaMetaRejectsInvalidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	raw := openRawSQLite(t, path)
	if err := raw.AutoMigrate(&SchemaMeta{}); err != nil {
		t.Fatal(err)
	}
	if err := raw.Create(&SchemaMeta{Key: "min_reader_epoch", Value: "abc"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSchemaMeta(raw); err == nil {
		t.Fatal("invalid epoch values must fail closed")
	}
}

func TestNextEpochsNeverDecrease(t *testing.T) {
	cases := []struct {
		dbSchema, dbMinReader, binarySchema, binaryMinReader int
		wantSchema, wantMinReader                            int
	}{
		{0, 0, 1, 1, 1, 1}, // 旧库首次记录
		{1, 1, 2, 1, 2, 1}, // 兼容升级
		{2, 1, 3, 3, 3, 3}, // 破坏性升级
		{3, 3, 2, 1, 3, 3}, // 旧程序在兼容的新库上运行不能降低要求
	}
	for _, tc := range cases {
		schema, minReader := nextEpochs(tc.dbSchema, tc.dbMinReader, tc.binarySchema, tc.binaryMinReader)
		if schema != tc.wantSchema || minReader != tc.wantMinReader {
			t.Fatalf("nextEpochs(%+v)=(%d,%d)", tc, schema, minReader)
		}
	}
}

func TestMigrateCheckRunsOnCopyWithoutTouchingGlobalHandle(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	if err := Init(source); err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	previous := DB
	copyPath := filepath.Join(dir, "migrate-check.db")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := MigrateCheck(copyPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaEpoch != SchemaEpoch || report.MinReaderEpoch != MinReaderEpoch || report.Duration <= 0 {
		t.Fatalf("report=%+v", report)
	}
	if DB != previous {
		t.Fatal("MigrateCheck must not replace the global database handle")
	}
}

func TestSubscriptionBackupMigrationPreservesEpochOneSubscriptions(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "epoch-one.db")
	if err := Init(sourcePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })
	node := ProxyNode{
		ExternalID: "node-before-backup-vless", Name: "原订阅节点", NameCustomized: true,
		Protocol: "vless", Server: "192.0.2.17", Port: 443, UUID: "original-uuid",
		PublicKey: "original-reality-public-key", ShortID: "abcdef12", Security: "reality",
		Sort: 17, Status: 1, CreatedTime: 1000, UpdatedTime: 2000,
	}
	profile := SubscriptionProfile{Name: "原订阅", Token: "original-subscription-token", DefaultFormat: "clash", Status: 1, CreatedTime: 1000, UpdatedTime: 2000}
	if err := DB.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	binding := SubscriptionProfileNode{SubscriptionID: profile.ID, ProxyNodeID: node.ID, Sort: 23, CreatedTime: 1000}
	if err := DB.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	// 纪元 1 尚无节点私钥备份表；只移除新增表并还原纪元，保留旧订阅与关联数据。
	if err := DB.Migrator().DropTable(&ProxyNodeBackup{}); err != nil {
		t.Fatal(err)
	}
	if err := DB.Model(&SchemaMeta{}).Where("key IN ?", []string{schemaMetaEpochKey, schemaMetaMinReaderKey}).Update("value", "1").Error; err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "migration-copy.db")
	if err := os.WriteFile(copyPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := DB
	report, err := MigrateCheck(copyPath, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaEpoch != SchemaEpoch || report.MinReaderEpoch != MinReaderEpoch {
		t.Fatalf("copy migration reported incompatible epochs: %+v", report)
	}
	if DB != previous {
		t.Fatal("migration rehearsal changed the global database handle")
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("migration rehearsal modified the original database: %v", err)
	}
	assertMigrated := func(t *testing.T, db *gorm.DB) {
		t.Helper()
		if !db.Migrator().HasTable(&ProxyNodeBackup{}) {
			t.Fatal("migration omitted the node backup table")
		}
		values, err := ReadSchemaMeta(db)
		if err != nil || values.SchemaEpoch != SchemaEpoch || values.MinReaderEpoch != MinReaderEpoch {
			t.Fatalf("unexpected migrated epochs: %+v err=%v", values, err)
		}
		var actualNode ProxyNode
		var actualProfile SubscriptionProfile
		var actualBinding SubscriptionProfileNode
		if err := db.First(&actualNode, node.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&actualProfile, profile.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&actualBinding, binding.ID).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actualNode, node) || !reflect.DeepEqual(actualProfile, profile) || !reflect.DeepEqual(actualBinding, binding) {
			t.Fatal("node backup migration changed an existing subscription, node, or binding")
		}
	}
	copyDB := openRawSQLite(t, copyPath)
	assertMigrated(t, copyDB)
	closeGormDB(copyDB)
	// 正式启动复用同一迁移路径；重复启动不得改写订阅令牌、节点身份或关联排序。
	for i := 0; i < 2; i++ {
		if err := Init(sourcePath); err != nil {
			t.Fatal(err)
		}
		assertMigrated(t, DB)
	}
}

func TestMigrateCheckNeverCreatesDatabases(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := MigrateCheck(missing, io.Discard); err == nil {
		t.Fatal("MigrateCheck must refuse a missing file")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("MigrateCheck created %s", missing)
	}
}
