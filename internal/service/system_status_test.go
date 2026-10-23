package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestSystemStatusReportsProcessUptime(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	oldStartedAt := processStartedAt
	processStartedAt = time.Now().Add(-90 * time.Second)
	t.Cleanup(func() { processStartedAt = oldStartedAt })

	databaseCreatedAt := time.Now().Add(-10 * 365 * 24 * time.Hour).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("user = ?", "admin_user").Update("created_time", databaseCreatedAt).Error; err != nil {
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
	if status.StartedAt != processStartedAt.UnixMilli() {
		t.Fatalf("StartedAt=%d, want %d", status.StartedAt, processStartedAt.UnixMilli())
	}
	if status.UptimeSeconds < 89 || status.UptimeSeconds > 91 {
		t.Fatalf("UptimeSeconds=%d, want about 90", status.UptimeSeconds)
	}
	// 状态接口不再读取数据库；关闭连接后仍应返回相同的进程启动时间。
	if err := model.Close(); err != nil {
		t.Fatal(err)
	}
	if afterClose := GetSystemStatus(); afterClose.Code != 0 || afterClose.Data.(SystemStatus).StartedAt != status.StartedAt {
		t.Fatalf("status depends on database: %+v", afterClose)
	}
}

func TestSystemStatusClampsFutureStartTime(t *testing.T) {
	oldStartedAt := processStartedAt
	processStartedAt = time.Now().Add(time.Hour)
	t.Cleanup(func() { processStartedAt = oldStartedAt })
	status := GetSystemStatus().Data.(SystemStatus)
	if status.UptimeSeconds != 0 {
		t.Fatalf("UptimeSeconds=%d, want 0", status.UptimeSeconds)
	}
}
