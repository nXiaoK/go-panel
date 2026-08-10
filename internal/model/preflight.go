package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// DuplicateIdentityError lists pre-existing rows that violate the unique
// identity constraints (user.user, user_tunnel user_id+tunnel_id). Migration
// refuses to proceed so an operator can merge or delete the duplicates first.
type DuplicateIdentityError struct {
	UserNameGroups   [][]int64
	UserTunnelGroups [][]int64
}

func (e *DuplicateIdentityError) Error() string {
	var parts []string
	if len(e.UserNameGroups) > 0 {
		parts = append(parts, fmt.Sprintf("user 表存在重复用户名，冲突行 ID 组: %v", e.UserNameGroups))
	}
	if len(e.UserTunnelGroups) > 0 {
		parts = append(parts, fmt.Sprintf("user_tunnel 表存在重复 (user_id, tunnel_id)，冲突行 ID 组: %v", e.UserTunnelGroups))
	}
	return "数据库存在重复身份数据，请先处理后再升级: " + strings.Join(parts, "；")
}

// PreflightSchema inspects legacy data for duplicate identities that would
// break the unique indexes created during AutoMigrate. It only reads: it never
// creates an index or modifies rows. Missing tables (fresh database) pass.
func PreflightSchema(db *gorm.DB) error {
	dup := &DuplicateIdentityError{}

	if db.Migrator().HasTable("user") {
		groups, err := duplicateIDGroups(db,
			`SELECT GROUP_CONCAT(id) FROM user GROUP BY user HAVING COUNT(*) > 1`)
		if err != nil {
			return fmt.Errorf("检查重复用户名失败: %w", err)
		}
		dup.UserNameGroups = groups
	}
	if db.Migrator().HasTable("user_tunnel") {
		groups, err := duplicateIDGroups(db,
			`SELECT GROUP_CONCAT(id) FROM user_tunnel GROUP BY user_id, tunnel_id HAVING COUNT(*) > 1`)
		if err != nil {
			return fmt.Errorf("检查重复用户隧道权限失败: %w", err)
		}
		dup.UserTunnelGroups = groups
	}

	if len(dup.UserNameGroups) > 0 || len(dup.UserTunnelGroups) > 0 {
		return dup
	}
	return nil
}

func duplicateIDGroups(db *gorm.DB, query string) ([][]int64, error) {
	var concatenated []string
	if err := db.Raw(query).Scan(&concatenated).Error; err != nil {
		return nil, err
	}
	groups := make([][]int64, 0, len(concatenated))
	for _, joined := range concatenated {
		var ids []int64
		for _, field := range strings.Split(joined, ",") {
			var id int64
			if _, err := fmt.Sscanf(strings.TrimSpace(field), "%d", &id); err == nil {
				ids = append(ids, id)
			}
		}
		if len(ids) > 1 {
			groups = append(groups, ids)
		}
	}
	return groups, nil
}
