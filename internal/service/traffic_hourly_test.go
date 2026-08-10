package service

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestApplyGostFlowWritesAdjustedTrafficHourlyInCallerTransaction(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&model.Tunnel{}).Where("id = ?", fixture.tunnel.ID).
		Updates(map[string]interface{}{"traffic_ratio": 1.5, "flow": 2}).Error; err != nil {
		t.Fatal(err)
	}
	flow := dto.FlowDto{
		N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		U: 100,
		D: 200,
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
	}); err != nil {
		t.Fatalf("ApplyGostFlow: %v", err)
	}

	var row model.TrafficHourly
	if err := model.DB.Where("user_id = ?", fixture.user.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InFlow != 600 || row.OutFlow != 300 {
		t.Fatalf("hourly adjusted counters=(%d,%d), want (600,300)", row.InFlow, row.OutFlow)
	}
	if want := model.TrafficHourlyBucketStart(time.UnixMilli(row.CreatedTime)); row.BucketStart != want {
		t.Fatalf("bucket_start=%d, want local bucket %d for created_time=%d", row.BucketStart, want, row.CreatedTime)
	}
	if row.UpdatedTime != row.CreatedTime {
		t.Fatalf("first insert created/updated=%d/%d", row.CreatedTime, row.UpdatedTime)
	}
	var tunnelRow model.TrafficTunnelHourly
	if err := model.DB.Where("user_id = ? AND tunnel_id = ?", fixture.user.ID, fixture.tunnel.ID).
		First(&tunnelRow).Error; err != nil {
		t.Fatal(err)
	}
	if tunnelRow.BucketStart != row.BucketStart || tunnelRow.InFlow != row.InFlow || tunnelRow.OutFlow != row.OutFlow {
		t.Fatalf("tunnel hourly row=%#v, want user hourly bucket/in/out=%d/%d/%d",
			tunnelRow, row.BucketStart, row.InFlow, row.OutFlow)
	}

	user := loadFlowUser(t, fixture.user.ID)
	if user.InFlow != row.InFlow || user.OutFlow != row.OutFlow {
		t.Fatalf("user counters=(%d,%d), ledger=(%d,%d)", user.InFlow, user.OutFlow, row.InFlow, row.OutFlow)
	}
}

func TestTrafficTunnelHourlyFailureRollsBackCountersAndUserLedger(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	if err := model.DB.Exec(`CREATE TRIGGER fail_traffic_tunnel_hourly_insert
		BEFORE INSERT ON traffic_tunnel_hourly
		BEGIN
		  SELECT RAISE(FAIL, 'injected tunnel hourly failure');
		END`).Error; err != nil {
		t.Fatal(err)
	}
	flow := dto.FlowDto{
		N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		U: 100,
		D: 200,
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
	})
	if err == nil {
		t.Fatal("tunnel traffic ledger insert failure should abort the complete flow transaction")
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	user := loadFlowUser(t, fixture.user.ID)
	userTunnel := loadFlowUserTunnel(t, fixture.userTunnel.ID)
	for label, counters := range map[string][2]int64{
		"forward":     {forward.InFlow, forward.OutFlow},
		"user":        {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{} {
			t.Fatalf("%s counters survived tunnel ledger rollback: %v", label, counters)
		}
	}
	for label, target := range map[string]interface{}{
		"user ledger":   &model.TrafficHourly{},
		"tunnel ledger": &model.TrafficTunnelHourly{},
	} {
		var count int64
		if err := model.DB.Model(target).Where("user_id = ?", fixture.user.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after rollback=%d", label, count)
		}
	}
}

func TestTrafficHourlyFailureRollsBackFlowCounters(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	if err := model.DB.Exec(`CREATE TRIGGER fail_traffic_hourly_insert
		BEFORE INSERT ON traffic_hourly
		BEGIN
		  SELECT RAISE(FAIL, 'injected traffic hourly failure');
		END`).Error; err != nil {
		t.Fatal(err)
	}
	flow := dto.FlowDto{
		N: fmt.Sprintf("%d_%d_%d", fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID),
		U: 100,
		D: 200,
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return ApplyGostFlow(tx, AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, flow)
	})
	if err == nil {
		t.Fatal("traffic ledger insert failure should abort the flow transaction")
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	user := loadFlowUser(t, fixture.user.ID)
	userTunnel := loadFlowUserTunnel(t, fixture.userTunnel.ID)
	for label, counters := range map[string][2]int64{
		"forward":     {forward.InFlow, forward.OutFlow},
		"user":        {user.InFlow, user.OutFlow},
		"user_tunnel": {userTunnel.InFlow, userTunnel.OutFlow},
	} {
		if counters != [2]int64{} {
			t.Fatalf("%s counters survived ledger rollback: %v", label, counters)
		}
	}
	var count int64
	if err := model.DB.Model(&model.TrafficHourly{}).Where("user_id = ?", fixture.user.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ledger rows after rollback=%d", count)
	}
}

func TestNftBatchReplayDoesNotDuplicateTrafficHourly(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	batch := nftBatchFixture(fixture, "hourly-reporter", 1, "hourly-batch", 10, 4)

	if _, err := ProcessNftBatch(node, batch); err != nil {
		t.Fatalf("first NFT batch: %v", err)
	}
	if _, err := ProcessNftBatch(node, batch); err != nil {
		t.Fatalf("replayed NFT batch: %v", err)
	}

	var totals struct {
		Rows    int64
		InFlow  int64
		OutFlow int64
	}
	if err := model.DB.Model(&model.TrafficHourly{}).
		Select("COUNT(*) AS rows, COALESCE(SUM(in_flow), 0) AS in_flow, COALESCE(SUM(out_flow), 0) AS out_flow").
		Where("user_id = ?", fixture.user.ID).Scan(&totals).Error; err != nil {
		t.Fatal(err)
	}
	if totals.Rows != 1 || totals.InFlow != 4 || totals.OutFlow != 10 {
		t.Fatalf("hourly ledger after replay rows/in/out=%d/%d/%d, want 1/4/10",
			totals.Rows, totals.InFlow, totals.OutFlow)
	}
	var tunnelTotals struct {
		Rows    int64
		InFlow  int64
		OutFlow int64
	}
	if err := model.DB.Model(&model.TrafficTunnelHourly{}).
		Select("COUNT(*) AS rows, COALESCE(SUM(in_flow), 0) AS in_flow, COALESCE(SUM(out_flow), 0) AS out_flow").
		Where("user_id = ? AND tunnel_id = ?", fixture.user.ID, fixture.tunnel.ID).
		Scan(&tunnelTotals).Error; err != nil {
		t.Fatal(err)
	}
	if tunnelTotals.Rows != 1 || tunnelTotals.InFlow != 4 || tunnelTotals.OutFlow != 10 {
		t.Fatalf("tunnel hourly ledger after replay rows/in/out=%d/%d/%d, want 1/4/10",
			tunnelTotals.Rows, tunnelTotals.InFlow, tunnelTotals.OutFlow)
	}
}

func TestNftBatchUsesPersistedCaptureTimeForTrafficHourly(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	capturedAt := time.Now().Add(-48*time.Hour - 17*time.Minute).Truncate(time.Millisecond)
	batch := nftBatchFixture(fixture, "captured-hour-reporter", 1, "captured-hour-batch", 10, 4)
	batch.CapturedAt = capturedAt.UnixMilli()

	if _, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch); err != nil {
		t.Fatalf("NFT delayed batch: %v", err)
	}

	var row model.TrafficHourly
	if err := model.DB.Where("user_id = ?", fixture.user.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	wantBucket := model.TrafficHourlyBucketStart(capturedAt)
	if row.BucketStart != wantBucket || row.CreatedTime != capturedAt.UnixMilli() {
		t.Fatalf("delayed batch bucket/created=%d/%d, want capture bucket/time %d/%d",
			row.BucketStart, row.CreatedTime, wantBucket, capturedAt.UnixMilli())
	}
	if receiveBucket := model.TrafficHourlyBucketStart(time.Now()); row.BucketStart == receiveBucket {
		t.Fatalf("delayed batch was incorrectly assigned to receipt bucket %d", receiveBucket)
	}
	var tunnelRow model.TrafficTunnelHourly
	if err := model.DB.Where("user_id = ? AND tunnel_id = ?", fixture.user.ID, fixture.tunnel.ID).
		First(&tunnelRow).Error; err != nil {
		t.Fatal(err)
	}
	if tunnelRow.BucketStart != wantBucket || tunnelRow.CreatedTime != capturedAt.UnixMilli() {
		t.Fatalf("delayed tunnel batch bucket/created=%d/%d, want capture bucket/time %d/%d",
			tunnelRow.BucketStart, tunnelRow.CreatedTime, wantBucket, capturedAt.UnixMilli())
	}
}
