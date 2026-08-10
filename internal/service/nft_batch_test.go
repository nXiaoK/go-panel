package service

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
)

func TestProcessNftBatchAcknowledgesRetryWithoutDoubleCount(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	batch := nftBatchFixture(fixture, "reporter-1", 1, "batch-1", 10, 4)

	first, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	second, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch)
	if err != nil {
		t.Fatalf("retry batch: %v", err)
	}
	if second != first {
		t.Fatalf("retry ack=%+v, want %+v", second, first)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 4 || forward.OutFlow != 10 {
		t.Fatalf("forward counters=(%d,%d), want (4,10)", forward.InFlow, forward.OutFlow)
	}
}

func TestProcessNftBatchRejectsGapStaleAndChangedBatchID(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}

	if _, err := ProcessNftBatch(node, nftBatchFixture(fixture, "reporter-1", 2, "batch-2", 1, 1)); !errors.Is(err, ErrFlowSequence) {
		t.Fatalf("gap err=%v, want ErrFlowSequence", err)
	}
	if _, err := ProcessNftBatch(node, nftBatchFixture(fixture, "reporter-1", 1, "batch-1", 1, 1)); err != nil {
		t.Fatalf("sequence 1: %v", err)
	}
	if _, err := ProcessNftBatch(node, nftBatchFixture(fixture, "reporter-1", 1, "changed-batch", 1, 1)); !errors.Is(err, ErrFlowBatchConflict) {
		t.Fatalf("changed batch err=%v, want ErrFlowBatchConflict", err)
	}
	if _, err := ProcessNftBatch(node, nftBatchFixture(fixture, "reporter-1", 0, "batch-0", 1, 1)); !errors.Is(err, ErrInvalidFlowReport) {
		t.Fatalf("zero sequence err=%v, want ErrInvalidFlowReport", err)
	}
}

func TestProcessNftBatchValidatesModeAndSafeEnvelope(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	valid := nftBatchFixture(fixture, "reporter-safe", 1, "batch-safe", 1, 1)
	if _, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeGost}, valid); !errors.Is(err, ErrFlowNodeMismatch) {
		t.Fatalf("gost mode err=%v", err)
	}
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	for name, mutate := range map[string]func(*dto.NftFlowBatchV2Dto){
		"unsafe reporter":   func(batch *dto.NftFlowBatchV2Dto) { batch.ReporterID = "bad reporter" },
		"unsafe batch":      func(batch *dto.NftFlowBatchV2Dto) { batch.BatchID = "bad/batch" },
		"empty items":       func(batch *dto.NftFlowBatchV2Dto) { batch.Items = nil },
		"oversize id":       func(batch *dto.NftFlowBatchV2Dto) { batch.ReporterID = strings.Repeat("x", 81) },
		"invalid old clock": func(batch *dto.NftFlowBatchV2Dto) { batch.CapturedAt = minNftBatchCapturedAtMillis - 1 },
		"future clock": func(batch *dto.NftFlowBatchV2Dto) {
			batch.CapturedAt = time.Now().Add(maxNftBatchFutureSkew + time.Minute).UnixMilli()
		},
	} {
		t.Run(name, func(t *testing.T) {
			batch := valid
			mutate(&batch)
			if _, err := ProcessNftBatch(node, batch); !errors.Is(err, ErrInvalidFlowReport) {
				t.Fatalf("err=%v, want ErrInvalidFlowReport", err)
			}
		})
	}
}

func TestNftBatchRecordedAtValidatesClockAndKeepsLegacyFallback(t *testing.T) {
	receivedAt := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	legacyAt, err := nftBatchRecordedAt(dto.NftFlowBatchV2Dto{}, receivedAt)
	if err != nil || !legacyAt.Equal(receivedAt) {
		t.Fatalf("legacy recordedAt=%s err=%v, want receipt time %s", legacyAt, err, receivedAt)
	}

	// 持久批次可能离线很久，合法历史时间仍应入原小时，而不能因年龄被拒绝。
	historical := receivedAt.Add(-400 * 24 * time.Hour)
	got, err := nftBatchRecordedAt(dto.NftFlowBatchV2Dto{CapturedAt: historical.UnixMilli()}, receivedAt)
	if err != nil || !got.Equal(historical) {
		t.Fatalf("historical recordedAt=%s err=%v, want %s", got, err, historical)
	}

	for name, capturedAt := range map[string]int64{
		"before supported epoch": minNftBatchCapturedAtMillis - 1,
		"too far in future":      receivedAt.Add(maxNftBatchFutureSkew).UnixMilli() + 1,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := nftBatchRecordedAt(dto.NftFlowBatchV2Dto{CapturedAt: capturedAt}, receivedAt); !errors.Is(err, ErrInvalidFlowReport) {
				t.Fatalf("err=%v, want ErrInvalidFlowReport", err)
			}
		})
	}
}

func TestNftProtocolLimitsAreShared(t *testing.T) {
	if dto.MaxNftFlowBatchItems != 10_000 {
		t.Fatalf("MaxNftFlowBatchItems=%d", dto.MaxNftFlowBatchItems)
	}
	if dto.MaxNftFlowItemBytes != int64(1<<40) {
		t.Fatalf("MaxNftFlowItemBytes=%d", dto.MaxNftFlowItemBytes)
	}
}

func TestProcessNftBatchRejectsMoreThanMaximumItems(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	item := nftFlowItem(fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID, 0, 0)
	batch := nftBatchFixture(fixture, "reporter-limit", 1, "batch-limit", 0, 0)
	batch.Items = make([]dto.NftFlowItem, dto.MaxNftFlowBatchItems+1)
	for i := range batch.Items {
		batch.Items[i] = item
	}
	if _, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch); !errors.Is(err, ErrInvalidFlowReport) {
		t.Fatalf("err=%v, want ErrInvalidFlowReport", err)
	}
}

func TestProcessNftBatchAcceptsMaximumDirectionalBytes(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	batch := nftBatchFixture(fixture, "reporter-byte-limit", 1, "batch-byte-limit", dto.MaxNftFlowItemBytes, dto.MaxNftFlowItemBytes)
	if _, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch); err != nil {
		t.Fatalf("maximum directional bytes rejected: %v", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != dto.MaxNftFlowItemBytes || forward.OutFlow != dto.MaxNftFlowItemBytes {
		t.Fatalf("forward counters=(%d,%d)", forward.InFlow, forward.OutFlow)
	}
}

func TestProcessNftBatchRejectsAboveMaximumDirectionalBytes(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	batch := nftBatchFixture(fixture, "reporter-byte-over", 1, "batch-byte-over", dto.MaxNftFlowItemBytes+1, 0)
	if _, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch); !errors.Is(err, ErrInvalidFlowReport) {
		t.Fatalf("err=%v, want ErrInvalidFlowReport", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 0 || forward.OutFlow != 0 {
		t.Fatalf("rejected batch changed counters: (%d,%d)", forward.InFlow, forward.OutFlow)
	}
}

func TestProcessNftBatchRejectsSameIdentityWithChangedItems(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	batch := nftBatchFixture(fixture, "reporter-content", 1, "batch-content", 10, 4)
	if _, err := ProcessNftBatch(node, batch); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	changed := nftBatchFixture(fixture, "reporter-content", 1, "batch-content", 99, 77)
	if _, err := ProcessNftBatch(node, changed); !errors.Is(err, ErrFlowBatchConflict) {
		t.Fatalf("changed items err=%v, want ErrFlowBatchConflict", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 4 || forward.OutFlow != 10 {
		t.Fatalf("changed replay altered counters: (%d,%d)", forward.InFlow, forward.OutFlow)
	}
}

func TestProcessNftBatchRejectsSameIdentityWithChangedCaptureTime(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	batch := nftBatchFixture(fixture, "reporter-captured", 1, "batch-captured", 10, 4)
	batch.CapturedAt = time.Now().Add(-time.Hour).UnixMilli()
	if _, err := ProcessNftBatch(node, batch); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	batch.CapturedAt++
	if _, err := ProcessNftBatch(node, batch); !errors.Is(err, ErrFlowBatchConflict) {
		t.Fatalf("changed capture time err=%v, want ErrFlowBatchConflict", err)
	}
}

func TestProcessNftBatchRollsBackMixedInvalidItemsAndState(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	batch := nftBatchFixture(fixture, "reporter-atomic", 1, "batch-atomic", 10, 4)
	badUser := fixture.user.ID + 999
	batch.Items = append(batch.Items, nftFlowItem(fixture.forward.ID, badUser, fixture.userTunnel.ID, 2, 3))

	_, err := ProcessNftBatch(AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}, batch)
	if !errors.Is(err, ErrFlowNodeMismatch) {
		t.Fatalf("mixed batch err=%v, want ErrFlowNodeMismatch", err)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 0 || forward.OutFlow != 0 {
		t.Fatalf("partial counters committed: %+v", forward)
	}
	var count int64
	if err := model.DB.Model(&model.FlowReporterState{}).Where("node_id = ? AND reporter_id = ?", fixture.nodeA.ID, batch.ReporterID).Count(&count).Error; err != nil {
		t.Fatalf("count reporter state: %v", err)
	}
	if count != 0 {
		t.Fatalf("reporter state rows=%d, want rollback", count)
	}
}

func TestProcessNftBatchSerializesConcurrentSameBatch(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	batch := nftBatchFixture(fixture, "reporter-concurrent", 1, "batch-concurrent", 10, 4)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	acks := make(chan dto.NftFlowAckDto, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ack, err := ProcessNftBatch(node, batch)
			acks <- ack
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(acks)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent same batch: %v", err)
		}
	}
	var first *dto.NftFlowAckDto
	for ack := range acks {
		if first == nil {
			copy := ack
			first = &copy
		} else if ack != *first {
			t.Fatalf("concurrent acks differ: %+v vs %+v", ack, *first)
		}
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if forward.InFlow != 4 || forward.OutFlow != 10 {
		t.Fatalf("concurrent batch counted more than once: (%d,%d)", forward.InFlow, forward.OutFlow)
	}
}

func TestProcessNftBatchSerializesConcurrentDifferentBatches(t *testing.T) {
	fixture := setupFlowAuthTestDB(t, true)
	setNftBatchNodeMode(t, fixture.nodeA.ID)
	node := AuthenticatedNode{ID: fixture.nodeA.ID, ForwardMode: forwardModeNftables}
	batches := []dto.NftFlowBatchV2Dto{
		nftBatchFixture(fixture, "reporter-race", 1, "batch-a", 10, 4),
		nftBatchFixture(fixture, "reporter-race", 1, "batch-b", 20, 6),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, batch := range batches {
		batch := batch
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := ProcessNftBatch(node, batch)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrFlowBatchConflict), errors.Is(err, ErrFlowSequence):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	forward := loadFlowForward(t, fixture.forward.ID)
	if !((forward.InFlow == 4 && forward.OutFlow == 10) || (forward.InFlow == 6 && forward.OutFlow == 20)) {
		t.Fatalf("concurrent different batches double-counted: (%d,%d)", forward.InFlow, forward.OutFlow)
	}
}

func nftBatchFixture(fixture flowAuthFixture, reporterID string, sequence uint64, batchID string, up, down int64) dto.NftFlowBatchV2Dto {
	return dto.NftFlowBatchV2Dto{
		ReporterID: reporterID,
		Sequence:   sequence,
		BatchID:    batchID,
		Items: []dto.NftFlowItem{
			nftFlowItem(fixture.forward.ID, fixture.user.ID, fixture.userTunnel.ID, up, down),
		},
	}
}

func setNftBatchNodeMode(t *testing.T, nodeID int64) {
	t.Helper()
	if err := model.DB.Model(&model.Node{}).Where("id = ?", nodeID).Update("forward_mode", forwardModeNftables).Error; err != nil {
		t.Fatalf("set nft node mode: %v", err)
	}
}
