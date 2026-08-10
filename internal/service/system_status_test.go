package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestGetSystemStatusUsesEarliestDatabaseRecord(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	startedAt := time.Now().Add(-2 * time.Hour).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("user = ?", "admin_user").Update("created_time", startedAt).Error; err != nil {
		t.Fatalf("update admin created time: %v", err)
	}

	res := GetSystemStatus()
	if res.Code != 0 {
		t.Fatalf("GetSystemStatus code=%d msg=%s", res.Code, res.Msg)
	}
	status, ok := res.Data.(SystemStatus)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if status.StartedAt != startedAt {
		t.Fatalf("StartedAt=%d, want %d", status.StartedAt, startedAt)
	}
	if status.UptimeSeconds < 7190 || status.UptimeSeconds > 7210 {
		t.Fatalf("UptimeSeconds=%d, want about 7200", status.UptimeSeconds)
	}
}
