package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/crypto"
)

func TestSeedCreatesBcryptDefaultAdmin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer Close()

	var admin User
	if err := DB.Where("user = ?", "admin_user").First(&admin).Error; err != nil {
		t.Fatalf("default admin not found: %v", err)
	}
	if !crypto.IsBcryptHash(admin.Pwd) {
		t.Fatalf("default admin password should be bcrypt, got %q", admin.Pwd)
	}
	if crypto.VerifyPassword(admin.Pwd, "admin_user") {
		t.Fatal("default admin password must not be the historical default")
	}
}

func TestForwardExitMemberMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer Close()

	if !DB.Migrator().HasTable(&ForwardExitMember{}) {
		t.Fatalf("forward_exit_member table should exist")
	}
	if !DB.Migrator().HasColumn(&Forward{}, "exit_mode") {
		t.Fatalf("forward.exit_mode column should exist")
	}
	if !DB.Migrator().HasColumn(&Forward{}, "exit_strategy") {
		t.Fatalf("forward.exit_strategy column should exist")
	}
	if !DB.Migrator().HasColumn(&Forward{}, "target_mode") {
		t.Fatalf("forward.target_mode column should exist")
	}
	if !DB.Migrator().HasColumn(&Forward{}, "active_remote_addr") {
		t.Fatalf("forward.active_remote_addr column should exist")
	}
}

func TestNodeHistoricalPanelURLMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer Close()

	if !DB.Migrator().HasColumn(&Node{}, "last_connected_base_url") {
		t.Fatal("node.last_connected_base_url column should exist")
	}
	if !DB.Migrator().HasColumn(&Node{}, "last_connected_base_time") {
		t.Fatal("node.last_connected_base_time column should exist")
	}
}

func TestFlowReporterStateMigrationEnforcesUniqueReporterPerNode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := Init(dbPath); err != nil {
		t.Fatal(err)
	}
	defer Close()
	if !DB.Migrator().HasTable(&FlowReporterState{}) {
		t.Fatal("flow_reporter_state table should exist")
	}
	first := FlowReporterState{NodeID: 7, ReporterID: "reporter", LastBatchID: "", LastAckDigest: "", UpdatedTime: 1}
	if err := DB.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := FlowReporterState{NodeID: 7, ReporterID: "reporter", UpdatedTime: 2}
	if err := DB.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate node/reporter state was accepted")
	}
	for name, reporterID := range map[string]string{"empty": "", "overlong": strings.Repeat("x", 81)} {
		t.Run(name, func(t *testing.T) {
			state := FlowReporterState{NodeID: 8, ReporterID: reporterID, UpdatedTime: 1}
			if err := DB.Create(&state).Error; err == nil {
				t.Fatalf("invalid reporter id length %d was accepted", len(reporterID))
			}
		})
	}
}
