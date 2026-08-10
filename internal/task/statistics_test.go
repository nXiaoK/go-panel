package task

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

func setupSchedulerDB(t *testing.T, users int) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	exp := time.Now().Add(24 * time.Hour).Unix()
	now := time.Now().UnixMilli()
	const insertChunk = 500
	batch := make([]model.User, 0, insertChunk)
	for i := 0; i < users; i++ {
		batch = append(batch, model.User{
			User: "user-" + time.Now().Format("150405") + "-" + itoa(i), Pwd: "x", RoleID: 2,
			ExpTime: &exp, Flow: 100, InFlow: int64(i), OutFlow: int64(i),
			FlowResetTime: 1, Num: 1, CreatedTime: now, Status: 1,
		})
		if len(batch) == insertChunk {
			if err := model.DB.Create(&batch).Error; err != nil {
				t.Fatalf("seed users: %v", err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := model.DB.Create(&batch).Error; err != nil {
			t.Fatalf("seed users: %v", err)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func TestStatisticsFlowJobFlushesEachBatch(t *testing.T) {
	setupSchedulerDB(t, 2501)
	stats, err := statisticsFlowJobAt(time.Unix(1700000000, 0), 1000)
	if err != nil {
		t.Fatal(err)
	}
	// 2501 seeded users plus the bootstrap administrator.
	if stats.Batches != 3 || stats.Users != 2502 || stats.Inserted != 2502 {
		t.Fatalf("stats=%+v", stats)
	}
	var count int64
	if err := model.DB.Model(&model.StatisticsFlow{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2502 {
		t.Fatalf("snapshot rows=%d, want 2502", count)
	}
}

func TestStatisticsFlowJobStopsAfterInsertError(t *testing.T) {
	setupSchedulerDB(t, 2501)
	failErr := errors.New("insert failed")
	calls := 0
	restore := swapStatisticsInsert(func(rows []model.StatisticsFlow) error {
		calls++
		if calls == 2 {
			return failErr
		}
		return model.DB.Create(&rows).Error
	})
	defer restore()

	stats, err := statisticsFlowJobAt(time.Unix(1700000000, 0), 1000)
	if !errors.Is(err, failErr) {
		t.Fatalf("err=%v, want insert failure", err)
	}
	if calls != 2 {
		t.Fatalf("insert calls=%d, batch three must not be loaded after failure", calls)
	}
	if stats.Batches != 1 || stats.Inserted != 1000 {
		t.Fatalf("stats=%+v, only batch one should be reported successful", stats)
	}
}

func TestStatisticsFlowJobKeepsHistoryWhenBatchFails(t *testing.T) {
	setupSchedulerDB(t, 5)
	// Seed one stale snapshot older than the retention window.
	stale := model.StatisticsFlow{UserID: 1, Flow: 1, TotalFlow: 1, Time: "00:00", CreatedTime: 1}
	if err := model.DB.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	failErr := errors.New("insert failed")
	restore := swapStatisticsInsert(func([]model.StatisticsFlow) error { return failErr })
	defer restore()

	if _, err := statisticsFlowJobAt(time.Now(), 1000); !errors.Is(err, failErr) {
		t.Fatalf("err=%v", err)
	}
	var count int64
	if err := model.DB.Model(&model.StatisticsFlow{}).Where("id = ?", stale.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("old history must be retained when snapshots fail")
	}
}

func TestStatisticsFlowJobCleansBothHourlyLedgersAfterSuccess(t *testing.T) {
	setupSchedulerDB(t, 1)
	now := time.Now().Truncate(time.Millisecond)
	staleBucket := now.Add(-32 * 24 * time.Hour).UnixMilli()
	freshBucket := now.Add(-time.Hour).UnixMilli()
	userRows := []model.TrafficHourly{
		{UserID: 1, BucketStart: staleBucket, InFlow: 1, CreatedTime: staleBucket, UpdatedTime: staleBucket},
		{UserID: 1, BucketStart: freshBucket, InFlow: 2, CreatedTime: freshBucket, UpdatedTime: freshBucket},
	}
	tunnelRows := []model.TrafficTunnelHourly{
		{UserID: 1, TunnelID: 9, BucketStart: staleBucket, InFlow: 3, CreatedTime: staleBucket, UpdatedTime: staleBucket},
		{UserID: 1, TunnelID: 9, BucketStart: freshBucket, InFlow: 4, CreatedTime: freshBucket, UpdatedTime: freshBucket},
	}
	if err := model.DB.Create(&userRows).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&tunnelRows).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := statisticsFlowJobAt(now, 1000); err != nil {
		t.Fatal(err)
	}
	for label, target := range map[string]interface{}{
		"user":   &model.TrafficHourly{},
		"tunnel": &model.TrafficTunnelHourly{},
	} {
		var staleCount, freshCount int64
		if err := model.DB.Model(target).Where("bucket_start = ?", staleBucket).Count(&staleCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(target).Where("bucket_start = ?", freshBucket).Count(&freshCount).Error; err != nil {
			t.Fatal(err)
		}
		if staleCount != 0 || freshCount != 1 {
			t.Fatalf("%s ledger retention stale/fresh=%d/%d", label, staleCount, freshCount)
		}
	}
}
