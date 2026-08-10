package service

import "gorm.io/gorm"

// gormStatementTable returns the best-effort table name for a GORM callback.
// Before("gorm:query") may run before Statement.Table is populated; Schema.Table
// is set earlier and is reliable across GORM versions used in CI.
func gormStatementTable(tx *gorm.DB) string {
	if tx == nil || tx.Statement == nil {
		return ""
	}
	if tx.Statement.Table != "" {
		return tx.Statement.Table
	}
	if tx.Statement.Schema != nil && tx.Statement.Schema.Table != "" {
		return tx.Statement.Schema.Table
	}
	return ""
}

func gormQueryTargetsTable(tx *gorm.DB, table string) bool {
	return table != "" && gormStatementTable(tx) == table
}
