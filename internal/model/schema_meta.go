package model

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
)

// schema_meta 中使用的键。值以十进制文本保存，便于人工排查。
const (
	schemaMetaEpochKey         = "schema_epoch"
	schemaMetaMinReaderKey     = "min_reader_epoch"
	schemaMetaWriterVersionKey = "last_writer_version"
	schemaMetaUpdatedAtKey     = "updated_at"
)

// SchemaMeta 以键值对记录数据库结构纪元，启动时据此拒绝不兼容的旧版本，避免旧代码写坏新结构。
type SchemaMeta struct {
	Key   string `gorm:"column:key;primaryKey;size:64"`
	Value string `gorm:"column:value;size:256;not null;default:''"`
}

func (SchemaMeta) TableName() string { return "schema_meta" }

// SchemaMetaValues 是 schema_meta 的解析结果；表不存在时（旧库或新库）全部为零值。
type SchemaMetaValues struct {
	SchemaEpoch    int
	MinReaderEpoch int
	// LastWriterVersion 是最近一次把结构纪元提升到当前值（或以相同纪元迁移）的程序版本。
	LastWriterVersion string
	UpdatedAt         int64
}

// SchemaTooNewError 表示数据库要求的最低读取纪元高于本程序支持的结构纪元。
type SchemaTooNewError struct {
	DBSchemaEpoch     int
	DBMinReaderEpoch  int
	LastWriterVersion string
	BinarySchemaEpoch int
}

func (e *SchemaTooNewError) Error() string {
	writer := e.LastWriterVersion
	if writer == "" {
		writer = "更新的版本"
	}
	return fmt.Sprintf(
		"数据库已被 %s 迁移为结构纪元 %d（读取至少需要纪元 %d），当前程序只支持纪元 %d，拒绝启动以免损坏数据",
		writer, e.DBSchemaEpoch, e.DBMinReaderEpoch, e.BinarySchemaEpoch,
	)
}

// ReadSchemaMeta 只读地解析结构纪元；表不存在时返回零值。值无法解析时报错，拒绝在不确定状态下迁移。
func ReadSchemaMeta(db *gorm.DB) (SchemaMetaValues, error) {
	if db == nil {
		return SchemaMetaValues{}, errors.New("数据库未初始化")
	}
	if !db.Migrator().HasTable(&SchemaMeta{}) {
		return SchemaMetaValues{}, nil
	}
	var rows []SchemaMeta
	if err := db.Find(&rows).Error; err != nil {
		return SchemaMetaValues{}, fmt.Errorf("读取数据库结构纪元失败: %w", err)
	}
	var values SchemaMetaValues
	for _, row := range rows {
		switch row.Key {
		case schemaMetaEpochKey, schemaMetaMinReaderKey:
			parsed, err := strconv.Atoi(row.Value)
			if err != nil || parsed < 0 {
				return SchemaMetaValues{}, fmt.Errorf("schema_meta 中 %s 的值 %q 无效", row.Key, row.Value)
			}
			if row.Key == schemaMetaEpochKey {
				values.SchemaEpoch = parsed
			} else {
				values.MinReaderEpoch = parsed
			}
		case schemaMetaWriterVersionKey:
			values.LastWriterVersion = row.Value
		case schemaMetaUpdatedAtKey:
			values.UpdatedAt, _ = strconv.ParseInt(row.Value, 10, 64)
		}
	}
	return values, nil
}

// checkSchemaCompatibility 在任何迁移之前执行：数据库要求的读取纪元高于本程序时拒绝继续。
func checkSchemaCompatibility(db *gorm.DB) error {
	values, err := ReadSchemaMeta(db)
	if err != nil {
		return err
	}
	if values.MinReaderEpoch > SchemaEpoch {
		return &SchemaTooNewError{
			DBSchemaEpoch:     values.SchemaEpoch,
			DBMinReaderEpoch:  values.MinReaderEpoch,
			LastWriterVersion: values.LastWriterVersion,
			BinarySchemaEpoch: SchemaEpoch,
		}
	}
	return nil
}

// nextEpochs 计算迁移后应记录的纪元：两个值都只增不减，旧程序迁移新库不会降低要求。
func nextEpochs(dbSchema, dbMinReader, binarySchema, binaryMinReader int) (int, int) {
	return max(dbSchema, binarySchema), max(dbMinReader, binaryMinReader)
}

// recordSchemaEpochs 在迁移成功后写入纪元。只有本程序的纪元不低于库中记录时才更新写入方版本，
// 使降级拒绝信息始终指向真正提升结构的版本。
func recordSchemaEpochs(db *gorm.DB, now time.Time) error {
	return db.Transaction(func(tx *gorm.DB) error {
		current, err := ReadSchemaMeta(tx)
		if err != nil {
			return err
		}
		schemaEpoch, minReader := nextEpochs(current.SchemaEpoch, current.MinReaderEpoch, SchemaEpoch, MinReaderEpoch)
		rows := []SchemaMeta{
			{Key: schemaMetaEpochKey, Value: strconv.Itoa(schemaEpoch)},
			{Key: schemaMetaMinReaderKey, Value: strconv.Itoa(minReader)},
			{Key: schemaMetaUpdatedAtKey, Value: strconv.FormatInt(now.UnixMilli(), 10)},
		}
		if SchemaEpoch >= current.SchemaEpoch {
			rows = append(rows, SchemaMeta{Key: schemaMetaWriterVersionKey, Value: buildinfo.Current().Version})
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&rows).Error
	})
}

// MigrateCheckReport 是在数据库副本上演练迁移的结果。
type MigrateCheckReport struct {
	Duration       time.Duration
	SchemaEpoch    int
	MinReaderEpoch int
}

// MigrateCheck 在指定的数据库副本上执行与启动相同的预检、迁移、回填和种子逻辑，供软更新预检使用。
//
// 该函数会原地修改文件，只能用于副本；它不修改全局 DB 句柄，但仍应只在独立的
// migrate-check 进程中调用。文件不存在时直接报错，绝不新建数据库。
func MigrateCheck(path string, logWriter io.Writer) (MigrateCheckReport, error) {
	info, err := os.Stat(path)
	if err != nil {
		return MigrateCheckReport{}, fmt.Errorf("数据库副本不可用: %w", err)
	}
	if !info.Mode().IsRegular() {
		return MigrateCheckReport{}, errors.New("数据库副本必须是普通文件")
	}
	if logWriter == nil {
		logWriter = io.Discard
	}
	started := time.Now()
	db, err := openAndMigrate(path, BootstrapOptions{
		AdminUsername:    "admin_user",
		CredentialWriter: io.Discard,
		LogWriter:        logWriter,
	})
	if err != nil {
		return MigrateCheckReport{}, err
	}
	defer closeGormDB(db)
	values, err := ReadSchemaMeta(db)
	if err != nil {
		return MigrateCheckReport{}, err
	}
	return MigrateCheckReport{
		Duration:       time.Since(started),
		SchemaEpoch:    values.SchemaEpoch,
		MinReaderEpoch: values.MinReaderEpoch,
	}, nil
}

func closeGormDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
