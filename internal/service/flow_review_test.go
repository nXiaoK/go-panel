package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
	"gorm.io/gorm"
)

func TestGostFlowRetryIsIdempotentAndRejectsChangedPayload(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	node := AuthenticatedNode{ID: fx.nodeA.ID, ForwardMode: forwardModeGost}
	flow := dto.FlowDto{N: fmt.Sprintf("%d_%d_%d_tcp", fx.forward.ID, fx.user.ID, fx.userTunnel.ID), U: 120, D: 340, ReporterID: "gost-service-1", Sequence: 1}
	apply := func(report dto.FlowDto) error {
		return model.DB.Transaction(func(tx *gorm.DB) error { return ApplyGostFlow(tx, node, report) })
	}
	for i := 0; i < 2; i++ {
		if err := apply(flow); err != nil {
			t.Fatal(err)
		}
	}
	changed := flow
	changed.D++
	if err := apply(changed); !errors.Is(err, ErrFlowBatchConflict) {
		t.Fatalf("changed retry error=%v", err)
	}
	changed = flow
	changed.Sequence = 3
	if err := apply(changed); !errors.Is(err, ErrFlowSequence) {
		t.Fatalf("sequence gap error=%v", err)
	}
	forward := loadFlowForward(t, fx.forward.ID)
	if forward.InFlow != 340 || forward.OutFlow != 120 {
		t.Fatalf("retry counted twice: %+v", forward)
	}
	flow.Sequence = 2
	if err := apply(flow); err != nil {
		t.Fatal(err)
	}
	user := loadFlowUser(t, fx.user.ID)
	if user.InFlow != 680 || user.OutFlow != 240 {
		t.Fatalf("next report not counted exactly once: %+v", user)
	}
	if res := ResetFlow(dto.ResetFlowDto{ID: fx.user.ID, Type: 1}); res.Code != 0 {
		t.Fatal(res.Msg)
	}
	if err := apply(flow); err != nil {
		t.Fatal(err)
	}
	user = loadFlowUser(t, fx.user.ID)
	if user.InFlow != 0 || user.OutFlow != 0 {
		t.Fatalf("ACK replay charged reset quota again: %+v", user)
	}
}

func TestGostReceiptRollsBackWithAccounting(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	flow := dto.FlowDto{N: fmt.Sprintf("%d_%d_%d", fx.forward.ID, fx.user.ID, fx.userTunnel.ID), U: 12, D: 34, ReporterID: "gost-rollback", Sequence: 1}
	if err := model.DB.Exec("CREATE TRIGGER fail_gost_ledger BEFORE INSERT ON traffic_hourly BEGIN SELECT RAISE(ABORT, 'injected ledger error'); END").Error; err != nil {
		t.Fatal(err)
	}
	apply := func() error {
		return model.DB.Transaction(func(tx *gorm.DB) error {
			return ApplyGostFlow(tx, AuthenticatedNode{ID: fx.nodeA.ID, ForwardMode: forwardModeGost}, flow)
		})
	}
	if err := apply(); err == nil {
		t.Fatal("expected ledger failure")
	}
	var receipts int64
	if err := model.DB.Model(&model.FlowReporterState{}).Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("receipt survived rollback: %d, %v", receipts, err)
	}
	if err := model.DB.Exec("DROP TRIGGER fail_gost_ledger").Error; err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	user := loadFlowUser(t, fx.user.ID)
	if user.InFlow != 34 || user.OutFlow != 12 {
		t.Fatalf("retry after rollback: %+v", user)
	}
}

func TestNftDeletedForwardDoesNotBlockRemainingBatch(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fx.nodeA.ID)
	retired := fx.forward
	retired.ID, retired.Name = 0, "deleted-forward"
	if err := model.DB.Create(&retired).Error; err != nil {
		t.Fatal(err)
	}
	batch := nftBatchFixture(fx, "retired-flow-reporter", 1, "retired-batch", 10, 20)
	batch.Items = append(batch.Items, nftFlowItem(retired.ID, fx.user.ID, fx.userTunnel.ID, 30, 40))
	if err := deleteForwardRows(model.DB, retired.ID); err != nil {
		t.Fatal(err)
	}
	node := AuthenticatedNode{ID: fx.nodeA.ID, ForwardMode: forwardModeNftables}
	for i := 0; i < 2; i++ {
		if _, err := ProcessNftBatch(node, batch); err != nil {
			t.Fatalf("retired reference blocked live flow accounting: %v", err)
		}
	}
	next := nftBatchFixture(fx, batch.ReporterID, 2, "next-batch", 1, 2)
	if _, err := ProcessNftBatch(node, next); err != nil {
		t.Fatal(err)
	}
	user := loadFlowUser(t, fx.user.ID)
	if user.InFlow != 22 || user.OutFlow != 11 {
		t.Fatalf("live flow counters after retired item: %+v", user)
	}
}

func TestResetFlowReportsDatabaseFailure(t *testing.T) {
	for _, kind := range []int{1, 2} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			fx := setupFlowAuthTestDB(t, true)
			table, id := "user", fx.user.ID
			if kind == 2 {
				table, id = "user_tunnel", fx.userTunnel.ID
			}
			if err := model.DB.Exec("UPDATE "+table+" SET in_flow = 123, out_flow = 456 WHERE id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Exec("CREATE TRIGGER fail_flow_reset BEFORE UPDATE ON " + table + " BEGIN SELECT RAISE(ABORT, 'injected reset error'); END").Error; err != nil {
				t.Fatal(err)
			}
			if res := ResetFlow(dto.ResetFlowDto{ID: id, Type: kind}); res.Code == 0 {
				t.Fatal("failed reset was reported as successful")
			}
		})
	}
}

func TestUserFlowQuotaStopsAtExactLimit(t *testing.T) {
	fx := setupFlowAuthTestDB(t, true)
	if err := model.DB.Model(&fx.user).Updates(map[string]any{"flow": 1, "in_flow": bytesToGB}).Error; err != nil {
		t.Fatal(err)
	}
	checkUserRelatedLimits(fx.user.ID)
	forward := loadFlowForward(t, fx.forward.ID)
	if forward.Status != forwardStatusPaused {
		t.Fatalf("forward remained active at exact quota: %d", forward.Status)
	}
}

func TestForwardEditPreservesFlowArrivingBeforeSave(t *testing.T) {
	fx := setupNftRelayFixture(t)
	if err := model.DB.Model(&fx.forward).Update("status", forwardStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	callback, injected := "test:flow-before-forward-save", false
	if err := model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		forward, ok := tx.Statement.Dest.(*model.Forward)
		if !ok || forward.ID != fx.forward.ID || injected {
			return
		}
		injected = true
		item := nftFlowItem(fx.forward.ID, fx.forward.UserID, 0, 10, 20)
		tx.AddError(ApplyNftFlowItem(tx.Session(&gorm.Session{NewDB: true}), AuthenticatedNode{ID: fx.entry.ID, ForwardMode: forwardModeNftables}, item))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { model.DB.Callback().Update().Remove(callback) })
	res := UpdateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, dto.ForwardUpdateDto{
		ID: fx.forward.ID, Name: "edited", TunnelID: fx.tunnel.ID, RemoteAddr: fx.forward.RemoteAddr,
	})
	if res.Code != 0 || !injected {
		t.Fatalf("update=%+v injected=%v", res, injected)
	}
	forward := loadFlowForward(t, fx.forward.ID)
	if forward.InFlow != 40 || forward.OutFlow != 20 {
		t.Fatalf("configuration save overwrote new flow: %+v", forward)
	}
}

func TestForwardRollbackPreservesFlowConfirmedDuringDeployment(t *testing.T) {
	fx := setupNftRelayFixture(t)
	original := sendNftRefreshMessage
	t.Cleanup(func() { sendNftRefreshMessage = original })
	injected := false
	sendNftRefreshMessage = func(_ int64, _ interface{}, _ string) ws.GostResult {
		if !injected {
			injected = true
			batch := dto.NftFlowBatchV2Dto{ReporterID: "rollback-flow", Sequence: 1, BatchID: "batch-1", Items: []dto.NftFlowItem{nftFlowItem(fx.forward.ID, fx.forward.UserID, 0, 10, 20)}}
			if _, err := ProcessNftBatch(AuthenticatedNode{ID: fx.entry.ID, ForwardMode: forwardModeNftables}, batch); err != nil {
				t.Fatal(err)
			}
			return ws.GostResult{Msg: "injected deployment failure"}
		}
		return ws.GostResult{Msg: gost.SuccessMsg}
	}
	res := UpdateForward(CurrentUser{UserID: fx.forward.UserID, RoleID: adminRoleID}, dto.ForwardUpdateDto{
		ID: fx.forward.ID, Name: "rolled-back", TunnelID: fx.tunnel.ID, RemoteAddr: "198.51.100.99:443",
	})
	if res.Code == 0 {
		t.Fatal("expected deployment rollback")
	}
	forward := loadFlowForward(t, fx.forward.ID)
	user := loadFlowUser(t, fx.forward.UserID)
	if forward.RemoteAddr != fx.forward.RemoteAddr || forward.InFlow != 40 || forward.OutFlow != 20 || forward.InFlow != user.InFlow || forward.OutFlow != user.OutFlow {
		t.Fatalf("rollback lost accounted flow: forward=%+v user=%+v", forward, user)
	}
}
