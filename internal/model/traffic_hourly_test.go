package model

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTrafficHourlyBucketStartUsesLocalWallClock(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("test-half-hour", 5*60*60+30*60)
	t.Cleanup(func() { time.Local = originalLocal })

	at := time.Date(2026, 8, 2, 10, 47, 59, 123, time.Local)
	want := time.Date(2026, 8, 2, 10, 0, 0, 0, time.Local).UnixMilli()
	if got := TrafficHourlyBucketStart(at); got != want {
		t.Fatalf("bucket start=%d (%s), want %d (%s)", got, time.UnixMilli(got), want, time.UnixMilli(want))
	}
}

func TestTrafficHourlyBucketStartKeepsRepeatedDSTHoursDistinct(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("load DST timezone: %v", err)
	}
	originalLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = originalLocal })

	// 2026-11-01 的 01:30 在回拨前后各出现一次，两个小时必须保留为不同桶。
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	second := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	firstBucket := TrafficHourlyBucketStart(first)
	secondBucket := TrafficHourlyBucketStart(second)
	if firstBucket != time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("first repeated bucket=%s", time.UnixMilli(firstBucket))
	}
	if secondBucket != time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("second repeated bucket=%s", time.UnixMilli(secondBucket))
	}
	if secondBucket-firstBucket != trafficHourMilliseconds {
		t.Fatalf("repeated buckets collapsed: first=%d second=%d", firstBucket, secondBucket)
	}
}

func TestInitBackfillsLegacyStatisticsIntoTrafficHourlyIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-traffic.db")
	legacyDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Exec(`CREATE TABLE statistics_flow (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		flow INTEGER NOT NULL,
		in_flow INTEGER NOT NULL DEFAULT 0,
		out_flow INTEGER NOT NULL DEFAULT 0,
		total_flow INTEGER NOT NULL,
		total_in_flow INTEGER NOT NULL DEFAULT 0,
		total_out_flow INTEGER NOT NULL DEFAULT 0,
		time TEXT NOT NULL,
		created_time INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Exec(`CREATE TABLE traffic_hourly (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		bucket_start INTEGER NOT NULL,
		in_flow INTEGER NOT NULL DEFAULT 0,
		out_flow INTEGER NOT NULL DEFAULT 0,
		created_time INTEGER NOT NULL,
		updated_time INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}

	firstSnapshot := time.Date(2026, 8, 2, 10, 5, 0, 0, time.Local).UnixMilli()
	secondSnapshot := time.Date(2026, 8, 2, 10, 45, 0, 0, time.Local).UnixMilli()
	directionalSnapshot := time.Date(2026, 8, 2, 11, 15, 0, 0, time.Local).UnixMilli()
	for _, args := range [][]interface{}{
		{int64(10), int64(100), int64(0), int64(0), int64(100), firstSnapshot},
		{int64(10), int64(20), int64(0), int64(0), int64(120), secondSnapshot},
		{int64(20), int64(30), int64(10), int64(20), int64(30), directionalSnapshot},
		{int64(30), int64(999), int64(0), int64(0), int64(999), firstSnapshot},
	} {
		if err := legacyDB.Exec(`INSERT INTO statistics_flow
			(user_id, flow, in_flow, out_flow, total_flow, total_in_flow, total_out_flow, time, created_time)
			VALUES (?, ?, ?, ?, ?, 0, 0, 'legacy', ?)`, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	firstBucket := TrafficHourlyBucketStart(time.UnixMilli(firstSnapshot)) - trafficHourMilliseconds
	directionalBucket := TrafficHourlyBucketStart(time.UnixMilli(directionalSnapshot)) - trafficHourMilliseconds
	if err := legacyDB.Exec(`INSERT INTO traffic_hourly
		(user_id, bucket_start, in_flow, out_flow, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?)`, int64(30), firstBucket, int64(7), int64(8), int64(111), int64(222)).Error; err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := legacyDB.DB(); err == nil {
		_ = sqlDB.Close()
	}

	if err := Init(dbPath); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
	var migrationCount int64
	if err := DB.Model(&DataMigration{}).Where("name = ?", trafficHourlyBackfillMigration).Count(&migrationCount).Error; err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("traffic hourly migration markers=%d, want 1", migrationCount)
	}

	assertTrafficHourlyRow(t, 10, firstBucket, 120, 0, firstSnapshot, secondSnapshot)
	assertTrafficHourlyRow(t, 20, directionalBucket, 10, 20, directionalSnapshot, directionalSnapshot)
	// A realtime row for the same user/hour is authoritative: legacy backfill
	// must neither overwrite it nor add the old snapshot to it.
	assertTrafficHourlyRow(t, 30, firstBucket, 7, 8, 111, 222)

	if err := Init(dbPath); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	var count int64
	if err := DB.Model(&TrafficHourly{}).Where("user_id IN ?", []int64{10, 20, 30}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("hourly row count after repeated Init=%d, want 3", count)
	}
	if err := DB.Model(&DataMigration{}).Where("name = ?", trafficHourlyBackfillMigration).Count(&migrationCount).Error; err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("traffic hourly migration marker duplicated: %d", migrationCount)
	}
	assertTrafficHourlyRow(t, 10, firstBucket, 120, 0, firstSnapshot, secondSnapshot)
	assertTrafficHourlyRow(t, 30, firstBucket, 7, 8, 111, 222)

	duplicate := TrafficHourly{
		UserID: 10, BucketStart: firstBucket, InFlow: 1,
		CreatedTime: secondSnapshot, UpdatedTime: secondSnapshot,
	}
	if err := DB.Create(&duplicate).Error; err == nil {
		t.Fatal("traffic_hourly accepted duplicate user/hour bucket")
	}
}

func TestBackfillTrafficHourlyIncludesThePreUpgradePartialHour(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "partial-hour.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })

	now := time.Date(2026, 8, 2, 10, 30, 0, 0, time.Local)
	expires := now.Add(24 * time.Hour).UnixMilli()
	user := User{
		User: "partial-user", Pwd: "unused", RoleID: 1, ExpTime: &expires,
		Flow: 100, InFlow: 130, OutFlow: 80, FlowResetTime: now.UnixMilli(),
		Num: 1, CreatedTime: now.UnixMilli(), Status: UserStatusActive,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	currentBucket := TrafficHourlyBucketStart(now)
	snapshot := StatisticsFlow{
		UserID: user.ID, Flow: 15, InFlow: 10, OutFlow: 5,
		TotalFlow: 150, TotalInFlow: 100, TotalOutFlow: 50,
		Time: "09:00", CreatedTime: currentBucket,
	}
	if err := DB.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}

	if err := backfillTrafficHourlyAt(DB, now); err != nil {
		t.Fatalf("backfill partial hour: %v", err)
	}
	assertTrafficHourlyRow(t, user.ID, currentBucket-trafficHourMilliseconds, 10, 5, currentBucket, currentBucket)
	assertTrafficHourlyRow(t, user.ID, currentBucket, 30, 30, now.UnixMilli(), now.UnixMilli())

	if err := backfillTrafficHourlyAt(DB, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("repeat partial-hour backfill: %v", err)
	}
	var count int64
	if err := DB.Model(&TrafficHourly{}).Where("user_id = ?", user.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("partial-hour rows after repeat=%d, want 2", count)
	}
	assertTrafficHourlyRow(t, user.ID, currentBucket, 30, 30, now.UnixMilli(), now.UnixMilli())
}

func TestBackfillTrafficHourlyIncludesCurrentHourUserWithoutLegacySnapshot(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "new-user-partial-hour.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })

	now := time.Date(2026, 8, 2, 10, 30, 0, 0, time.Local)
	expires := now.Add(24 * time.Hour).UnixMilli()
	user := User{
		User: "new-partial-user", Pwd: "unused", RoleID: 1, ExpTime: &expires,
		Flow: 100, InFlow: 12, OutFlow: 34, FlowResetTime: now.UnixMilli(),
		Num: 1, CreatedTime: now.Add(-10 * time.Minute).UnixMilli(), Status: UserStatusActive,
	}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	if err := backfillTrafficHourlyAt(DB, now); err != nil {
		t.Fatalf("backfill current-hour user: %v", err)
	}
	assertTrafficHourlyRow(t, user.ID, TrafficHourlyBucketStart(now), 12, 34, now.UnixMilli(), now.UnixMilli())
}

func assertTrafficHourlyRow(t *testing.T, userID, bucketStart, inFlow, outFlow, createdTime, updatedTime int64) {
	t.Helper()
	var row TrafficHourly
	if err := DB.Where("user_id = ? AND bucket_start = ?", userID, bucketStart).First(&row).Error; err != nil {
		t.Fatalf("load user=%d bucket=%d: %v", userID, bucketStart, err)
	}
	if row.InFlow != inFlow || row.OutFlow != outFlow || row.CreatedTime != createdTime || row.UpdatedTime != updatedTime {
		t.Fatalf("unexpected hourly row: %#v; want in/out=%d/%d created/updated=%d/%d",
			row, inFlow, outFlow, createdTime, updatedTime)
	}
}
