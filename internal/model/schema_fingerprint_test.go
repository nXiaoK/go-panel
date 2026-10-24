package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// expectedSchemaFingerprints 按结构纪元记录新库迁移后的结构指纹。
// 指纹变化说明 GORM 模型或迁移逻辑改变了数据库结构，必须按 schema_epoch.go 的规则重新判断纪元。
var expectedSchemaFingerprints = map[int]string{
	1: "25cdbfe232c74f671dfa68448fa4e5bb6d10dc73b77a4951148c539129876551",
}

func schemaFingerprint(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fingerprint.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })
	var rows []struct {
		Type    string `gorm:"column:type"`
		Name    string `gorm:"column:name"`
		TblName string `gorm:"column:tbl_name"`
		SQL     string `gorm:"column:sql"`
	}
	if err := DB.Raw("SELECT type, name, tbl_name, COALESCE(sql, '') AS sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, row := range rows {
		fmt.Fprintf(hash, "%s|%s|%s|%s\n", row.Type, row.Name, row.TblName, normalizeSchemaSQL(row.Type, row.SQL))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// normalizeSchemaSQL 压缩空白，并对建表语句中的顶层元素（列、约束）排序：
// GORM 按 map 遍历顺序输出 CHECK 约束，原始文本在多次运行间并不稳定。
func normalizeSchemaSQL(objectType, sql string) string {
	sql = strings.Join(strings.Fields(sql), " ")
	if objectType != "table" {
		return sql
	}
	open, closing := strings.IndexByte(sql, '('), strings.LastIndexByte(sql, ')')
	if open < 0 || closing <= open {
		return sql
	}
	var elements []string
	depth, start := 0, open+1
	for i := open + 1; i < closing; i++ {
		switch sql[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				elements = append(elements, strings.TrimSpace(sql[start:i]))
				start = i + 1
			}
		}
	}
	elements = append(elements, strings.TrimSpace(sql[start:closing]))
	sort.Strings(elements)
	return sql[:open+1] + strings.Join(elements, ",") + sql[closing:]
}

func TestSchemaFingerprintMatchesEpoch(t *testing.T) {
	got := schemaFingerprint(t)
	want, ok := expectedSchemaFingerprints[SchemaEpoch]
	if ok && got == want {
		return
	}
	t.Fatalf(`数据库结构指纹与 SchemaEpoch=%d 记录的不一致：
  当前指纹 %s
  记录指纹 %q

请按 internal/model/schema_epoch.go 的规则处理：
  1. 判断本次是兼容变更（新增可空列/表/索引）还是破坏性变更（删改列、改类型或约束、无默认值的 NOT NULL 等）；
  2. 提升 SchemaEpoch；破坏性变更还要令 MinReaderEpoch = SchemaEpoch；
  3. 在 expectedSchemaFingerprints 中为新的 SchemaEpoch 记录上面的当前指纹（保留旧纪元的记录）。`,
		SchemaEpoch, got, want)
}
