package task

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

func TestStatisticsFlowJobStoresDirectionalIncrements(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	exp := time.Now().Add(24 * time.Hour).Unix()
	user := model.User{
		User:          "alice",
		Pwd:           "x",
		RoleID:        2,
		ExpTime:       &exp,
		Flow:          100,
		InFlow:        5,
		OutFlow:       7,
		FlowResetTime: 1,
		Num:           1,
		CreatedTime:   time.Now().UnixMilli(),
		Status:        1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	StatisticsFlowJob()

	var first model.StatisticsFlow
	if err := model.DB.Where("user_id = ?", user.ID).Order("id DESC").First(&first).Error; err != nil {
		t.Fatalf("load first statistics row: %v", err)
	}
	if first.Flow != 12 || first.InFlow != 5 || first.OutFlow != 7 || first.TotalFlow != 12 {
		t.Fatalf("first snapshot mismatch: %#v", first)
	}
	if first.TotalInFlow != 5 || first.TotalOutFlow != 7 {
		t.Fatalf("first directional totals mismatch: %#v", first)
	}

	if err := model.DB.Model(&model.User{}).Where("id = ?", user.ID).
		Updates(map[string]interface{}{"in_flow": int64(8), "out_flow": int64(11)}).Error; err != nil {
		t.Fatalf("update user flow: %v", err)
	}

	StatisticsFlowJob()

	var second model.StatisticsFlow
	if err := model.DB.Where("user_id = ?", user.ID).Order("id DESC").First(&second).Error; err != nil {
		t.Fatalf("load second statistics row: %v", err)
	}
	if second.Flow != 7 || second.InFlow != 3 || second.OutFlow != 4 || second.TotalFlow != 19 {
		t.Fatalf("second snapshot mismatch: %#v", second)
	}
	if second.TotalInFlow != 8 || second.TotalOutFlow != 11 {
		t.Fatalf("second directional totals mismatch: %#v", second)
	}
}

func TestStatisticsFlowJobLabelsTheCompletedPreviousHour(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	exp := time.Date(2026, 8, 3, 0, 0, 0, 0, time.Local).UnixMilli()
	user := model.User{
		User: "midnight-user", Pwd: "x", RoleID: 1, ExpTime: &exp,
		Flow: 100, InFlow: 5, OutFlow: 7, FlowResetTime: 1, Num: 1,
		CreatedTime: exp, Status: 1,
	}
	if err := model.DB.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local)
	if _, err := statisticsFlowJobAt(now, 100); err != nil {
		t.Fatalf("run statistics job: %v", err)
	}

	var row model.StatisticsFlow
	if err := model.DB.Where("user_id = ?", user.ID).First(&row).Error; err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if row.Time != "23:00" {
		t.Fatalf("midnight snapshot label=%q, want previous day 23:00", row.Time)
	}
	if row.CreatedTime != now.UnixMilli() {
		t.Fatalf("snapshot creation time=%d, want job time %d", row.CreatedTime, now.UnixMilli())
	}
}
