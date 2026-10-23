package model

import (
	"errors"
	"io"
	"os"
	"path/filepath"
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

func TestMigrateCheckNeverCreatesDatabases(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := MigrateCheck(missing, io.Discard); err == nil {
		t.Fatal("MigrateCheck must refuse a missing file")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("MigrateCheck created %s", missing)
	}
}
