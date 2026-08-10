package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
)

const testReporterCapturedAt int64 = 1_754_092_800_123

func fixedReporterNow() time.Time { return time.UnixMilli(testReporterCapturedAt) }

func TestReporterRetriesExactPersistedPendingBeforeReadingCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	reads := 0
	var payloads [][]byte
	uploads := 0
	var events []string
	r := reporter{
		store: store,
		now:   fixedReporterNow,
		readCounters: func(string) (map[flowKey]counters, error) {
			reads++
			events = append(events, "read")
			return reporterCounterFixture(), nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			events = append(events, "upload")
			payloads = append(payloads, append([]byte(nil), payload...))
			uploads++
			if uploads == 1 {
				return dto.NftFlowAckDto{}, errors.New("lost acknowledgement")
			}
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel:6365", "secret", "flux_panel"); err == nil {
		t.Fatal("first upload should fail")
	}
	journal, err := store.load()
	if err != nil {
		t.Fatalf("load pending journal: %v", err)
	}
	if journal.Pending == nil {
		t.Fatal("pending payload was not persisted")
	}
	var persisted dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(journal.Pending.Payload, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.CapturedAt != testReporterCapturedAt || journal.DrainCapturedAt != testReporterCapturedAt {
		t.Fatalf("pending/snapshot capture time=%d/%d, want %d", persisted.CapturedAt, journal.DrainCapturedAt, testReporterCapturedAt)
	}
	if err := r.runOnce("panel:6365", "secret", "flux_panel"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if reads != 1 {
		t.Fatalf("counter reads=%d, want persisted snapshot drain without reread", reads)
	}
	if got := strings.Join(events, ","); got != "read,upload,upload" {
		t.Fatalf("events=%s, want pending replay from persisted snapshot", got)
	}
	if len(payloads) != 2 || !bytes.Equal(payloads[0], payloads[1]) || !bytes.Equal(payloads[0], journal.Pending.Payload) {
		t.Fatalf("retry payload changed: %q / %q", payloads[0], payloads[1])
	}
	journal, err = store.load()
	if err != nil {
		t.Fatal(err)
	}
	if journal.Pending != nil || journal.LastSequence != 1 || len(journal.Baseline) == 0 {
		t.Fatalf("completed journal=%+v", journal)
	}
}

func TestReporterLegacyV2PendingWithoutDrainSnapshotMigratesBeforeReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	journal := legacyV2Journal{Version: legacyJournalVersion, ReporterID: "legacy-v2-direct", Baseline: []journalCounter{}}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "legacy-v2-pending",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 20, 7)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{
		Payload: payload,
		ResultingBaseline: []journalCounter{
			{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 20, Down: 7},
		},
	}
	writeRawJournalFixture(t, path, journal)
	var events []string
	r := reporter{
		store: store,
		readCounters: func(string) (map[flowKey]counters, error) {
			events = append(events, "read")
			return nil, errors.New("migrated legacy Pending must not reread counters")
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			events = append(events, "upload")
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(events, ","); got != "upload" {
		t.Fatalf("legacy v2 events=%s, want durable migration before replay", got)
	}
}

func TestReporterKeepsPendingOnMismatchedAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return reporterCounterFixture(), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			ack := matchingAck(t, payload)
			ack.BatchID = "wrong-batch"
			return ack, nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("mismatched acknowledgement should fail")
	}
	journal, err := store.load()
	if err != nil || journal.Pending == nil || journal.LastSequence != 0 {
		t.Fatalf("journal after mismatched ack=%+v err=%v", journal, err)
	}
}

func TestReporterRetriesAfterAckSaveFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	base := fileJournalStore{path: path}
	store := &failSaveJournalStore{delegate: base, failAt: 2}
	var payloads [][]byte
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return reporterCounterFixture(), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			payloads = append(payloads, append([]byte(nil), payload...))
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("durable ack save failure should be returned")
	}
	journal, err := base.load()
	if err != nil || journal.Pending == nil || journal.LastSequence != 0 {
		t.Fatalf("disk journal lost pending after save failure: %+v err=%v", journal, err)
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatalf("retry after save failure: %v", err)
	}
	if len(payloads) != 2 || !bytes.Equal(payloads[0], payloads[1]) {
		t.Fatal("post-ack save failure did not retry exact batch")
	}
}

func TestReporterDoesNotSendWhenPendingSaveFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := &failSaveJournalStore{delegate: fileJournalStore{path: path}, failAt: 1}
	uploaded := false
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return reporterCounterFixture(), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			uploaded = true
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("pending save failure should stop reporter")
	}
	if uploaded {
		t.Fatal("reporter uploaded before pending was durable")
	}
}

func TestReporterSequenceExhaustionKeepsRemainingFixedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	journal := mustTestJournal(t)
	journal.LastSequence = math.MaxInt64 - 2
	if err := store.save(journal); err != nil {
		t.Fatal(err)
	}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	snapshot := map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes + 1}}
	var uploaded []dto.NftFlowBatchV2Dto
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return cloneCounters(snapshot), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			uploaded = append(uploaded, batch)
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "flux_panel"); err == nil || !strings.Contains(err.Error(), "sequence exhausted") {
		t.Fatalf("sequence exhaustion error=%v", err)
	}
	if len(uploaded) != 1 || uploaded[0].Sequence != math.MaxInt64-1 {
		t.Fatalf("uploaded=%+v", uploaded)
	}
	got, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSequence != math.MaxInt64-1 || got.Pending != nil || got.DrainSnapshot == nil ||
		journalToCounters(got.Baseline)[key].Up != dto.MaxNftFlowItemBytes {
		t.Fatalf("exhausted journal lost fixed remainder: %+v", got)
	}
}

func TestReporterSplitsOneKeyAboveDirectionalLimit(t *testing.T) {
	snapshot := map[flowKey]counters{
		{ForwardID: 1, UserID: 2, UserTunnelID: 3}: {Up: dto.MaxNftFlowItemBytes*2 + 7},
	}
	uploads, final, reads := runReporterAgainstSnapshot(t, snapshot)
	if reads != 1 {
		t.Fatalf("counter reads=%d, want one fixed snapshot", reads)
	}
	if len(uploads) != 3 {
		t.Fatalf("uploads=%d, want 3", len(uploads))
	}
	wantUp := []int64{dto.MaxNftFlowItemBytes, dto.MaxNftFlowItemBytes, 7}
	for i, batch := range uploads {
		if batch.Sequence != uint64(i+1) || len(batch.Items) != 1 {
			t.Fatalf("batch %d=%+v", i, batch)
		}
		if *batch.Items[0].Up != wantUp[i] || *batch.Items[0].Down != 0 {
			t.Fatalf("batch %d item=%+v, want up=%d down=0", i, batch.Items[0], wantUp[i])
		}
		if *batch.Items[0].Up > dto.MaxNftFlowItemBytes || *batch.Items[0].Down > dto.MaxNftFlowItemBytes {
			t.Fatal("oversize item")
		}
		if batch.CapturedAt != testReporterCapturedAt {
			t.Fatalf("batch %d capture time=%d, want fixed snapshot time %d", i, batch.CapturedAt, testReporterCapturedAt)
		}
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestReporterSplitsMoreThanMaximumKeys(t *testing.T) {
	snapshot := makeCounterSnapshot(dto.MaxNftFlowBatchItems + 3)
	uploads, final, reads := runReporterAgainstSnapshot(t, snapshot)
	if reads != 1 {
		t.Fatalf("counter reads=%d, want one fixed snapshot", reads)
	}
	if len(uploads) != 2 {
		t.Fatalf("uploads=%d, want 2", len(uploads))
	}
	if len(uploads[0].Items) != dto.MaxNftFlowBatchItems || len(uploads[1].Items) != 3 {
		t.Fatalf("unexpected batch split: %d / %d", len(uploads[0].Items), len(uploads[1].Items))
	}
	if *uploads[0].Items[0].ForwardID != 1 || *uploads[1].Items[0].ForwardID != int64(dto.MaxNftFlowBatchItems+1) {
		t.Fatal("batches are not deterministically ordered by flow key")
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestReporterDrainsAnImmutableSnapshot(t *testing.T) {
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	live := map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes + 5}}
	store := fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}
	var uploads []dto.NftFlowBatchV2Dto
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return live, nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			uploads = append(uploads, batch)
			if len(uploads) == 1 {
				live[key] = counters{Up: dto.MaxNftFlowItemBytes*3 + 99}
			}
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 2 || *uploads[1].Items[0].Up != 5 {
		t.Fatalf("uploads followed mutable live counters: %+v", uploads)
	}
	final, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	assertBaselineEquals(t, final.Baseline, map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes + 5}})
}

func TestReporterDropsDisappearedKeysOnlyAfterSnapshotDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	disappeared := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	active := flowKey{ForwardID: 4, UserID: 5, UserTunnelID: 6}
	journal.Baseline = countersToJournal(map[flowKey]counters{disappeared: {Up: 77, Down: 8}})
	if err := store.save(journal); err != nil {
		t.Fatal(err)
	}
	snapshot := map[flowKey]counters{active: {Up: dto.MaxNftFlowItemBytes + 1}}
	uploads := 0
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return cloneCounters(snapshot), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			uploads++
			if uploads == 1 {
				pending, loadErr := store.load()
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if _, ok := journalToCounters(pending.Pending.ResultingBaseline)[disappeared]; !ok {
					t.Fatal("partial baseline dropped a disappeared key before the snapshot drained")
				}
			}
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if uploads != 2 {
		t.Fatalf("uploads=%d, want 2", uploads)
	}
	final, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestNextBoundedBatchAdvancesOnlyUploadedDirections(t *testing.T) {
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	previous := map[flowKey]counters{key: {Up: 100, Down: 200}}
	snapshot := map[flowKey]counters{key: {Up: 100 + dto.MaxNftFlowItemBytes + 9, Down: 207}}

	items, next, ok := nextBoundedBatch(previous, snapshot)
	if !ok || len(items) != 1 {
		t.Fatalf("items=%+v ok=%v", items, ok)
	}
	if *items[0].Up != dto.MaxNftFlowItemBytes || *items[0].Down != 7 {
		t.Fatalf("item=%+v", items[0])
	}
	if next[key] != (counters{Up: 100 + dto.MaxNftFlowItemBytes, Down: 207}) {
		t.Fatalf("next baseline=%+v", next[key])
	}
	if previous[key] != (counters{Up: 100, Down: 200}) {
		t.Fatal("previous baseline was mutated")
	}
}

func TestNextBoundedBatchTreatsCounterResetAsZeroBased(t *testing.T) {
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	previous := map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes * 3}}
	snapshot := map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes + 5}}

	items, next, ok := nextBoundedBatch(previous, snapshot)
	if !ok || len(items) != 1 || *items[0].Up != dto.MaxNftFlowItemBytes {
		t.Fatalf("first reset batch=%+v ok=%v", items, ok)
	}
	if next[key].Up != dto.MaxNftFlowItemBytes {
		t.Fatalf("first reset baseline=%d", next[key].Up)
	}
	items, next, ok = nextBoundedBatch(next, snapshot)
	if !ok || len(items) != 1 || *items[0].Up != 5 || next[key].Up != snapshot[key].Up {
		t.Fatalf("second reset batch=%+v baseline=%+v ok=%v", items, next[key], ok)
	}
}

func TestReporterReplaysSecondChunkWithSameBytesAndSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	snapshot := map[flowKey]counters{
		{ForwardID: 1, UserID: 2, UserTunnelID: 3}: {Up: dto.MaxNftFlowItemBytes*2 + 9},
	}
	var firstRunPayloads [][]byte
	uploads := 0
	r := reporter{
		store: store,
		now:   fixedReporterNow,
		readCounters: func(string) (map[flowKey]counters, error) {
			return cloneCounters(snapshot), nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			firstRunPayloads = append(firstRunPayloads, append([]byte(nil), payload...))
			uploads++
			if uploads == 2 {
				return dto.NftFlowAckDto{}, errors.New("injected second chunk failure")
			}
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("second chunk failure should be returned")
	}
	if len(firstRunPayloads) != 2 {
		t.Fatalf("first run uploads=%d, want 2", len(firstRunPayloads))
	}
	pendingJournal, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if pendingJournal.LastSequence != 1 || pendingJournal.Pending == nil {
		t.Fatalf("journal after second chunk failure=%+v", pendingJournal)
	}
	assertBaselineEquals(t, pendingJournal.DrainSnapshot, snapshot)
	persistedPayload := append([]byte(nil), pendingJournal.Pending.Payload...)
	var persistedBatch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(persistedPayload, &persistedBatch); err != nil {
		t.Fatal(err)
	}
	if persistedBatch.Sequence != 2 || *persistedBatch.Items[0].Up != dto.MaxNftFlowItemBytes {
		t.Fatalf("persisted second batch=%+v", persistedBatch)
	}

	var replayed [][]byte
	restartReads := 0
	restarted := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			restartReads++
			return map[flowKey]counters{}, nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			replayed = append(replayed, append([]byte(nil), payload...))
			return matchingAck(t, payload), nil
		},
	}
	if err := restarted.runOnce("panel", "secret", "table"); err != nil {
		t.Fatalf("restart replay: %v", err)
	}
	if restartReads != 0 {
		t.Fatalf("restart reread live counters %d times instead of draining persisted snapshot", restartReads)
	}
	if len(replayed) != 2 || !bytes.Equal(replayed[0], persistedPayload) {
		t.Fatalf("replayed payload changed: %q / %q", replayed, persistedPayload)
	}
	var thirdBatch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(replayed[1], &thirdBatch); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(replayed[1], persistedPayload) || thirdBatch.Sequence != 3 || *thirdBatch.Items[0].Up != 9 ||
		thirdBatch.CapturedAt != testReporterCapturedAt {
		t.Fatalf("third batch after replay=%+v", thirdBatch)
	}
	final, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if final.Pending != nil || final.LastSequence != 3 {
		t.Fatalf("final journal=%+v", final)
	}
	if final.DrainSnapshot != nil {
		t.Fatalf("completed drain retained snapshot=%+v", final.DrainSnapshot)
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestReporterCrashDuringResetDrainPreservesAllKeysAndBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	store := fileJournalStore{path: path}
	resetKey := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	otherKey := flowKey{ForwardID: 4, UserID: 5, UserTunnelID: 6}
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	journal.Baseline = countersToJournal(map[flowKey]counters{
		resetKey: {Up: dto.MaxNftFlowItemBytes * 4},
		otherKey: {Down: 100},
	})
	if err := store.save(journal); err != nil {
		t.Fatal(err)
	}
	snapshot := map[flowKey]counters{
		resetKey: {Up: dto.MaxNftFlowItemBytes*2 + 5},
		otherKey: {Down: 100 + dto.MaxNftFlowItemBytes*2 + 9},
	}

	var attempts []dto.NftFlowBatchV2Dto
	r := reporter{
		store:        store,
		readCounters: func(string) (map[flowKey]counters, error) { return cloneCounters(snapshot), nil },
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			attempts = append(attempts, batch)
			if batch.Sequence == 2 {
				return dto.NftFlowAckDto{}, errors.New("crash during second reset chunk")
			}
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("second chunk failure should be returned")
	}

	restartReads := 0
	restarted := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			restartReads++
			return map[flowKey]counters{resetKey: {}, otherKey: {}}, nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			attempts = append(attempts, batch)
			return matchingAck(t, payload), nil
		},
	}
	if err := restarted.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if restartReads != 0 {
		t.Fatalf("restart reread live reset counters %d times", restartReads)
	}

	bySequence := map[uint64]dto.NftFlowBatchV2Dto{}
	for _, batch := range attempts {
		bySequence[batch.Sequence] = batch
	}
	if len(bySequence) != 3 {
		t.Fatalf("unique sequences=%d, attempts=%+v", len(bySequence), attempts)
	}
	var resetUp, otherDown int64
	for _, batch := range bySequence {
		for _, item := range batch.Items {
			if *item.ForwardID == resetKey.ForwardID {
				resetUp += *item.Up
			}
			if *item.ForwardID == otherKey.ForwardID {
				otherDown += *item.Down
			}
		}
	}
	if resetUp != snapshot[resetKey].Up || otherDown != snapshot[otherKey].Down-100 {
		t.Fatalf("reported resetUp=%d otherDown=%d", resetUp, otherDown)
	}
	final, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestJournalCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := false
	r := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			read = true
			return reporterCounterFixture(), nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			t.Fatal("unexpected upload")
			return dto.NftFlowAckDto{}, nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("corrupt journal should fail closed")
	}
	if read {
		t.Fatal("corrupt journal silently reset baseline and read counters")
	}
}

func TestJournalRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(journal)
	raw = append(raw, []byte(` {"extra":true}`)...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (fileJournalStore{path: path}).load(); err == nil {
		t.Fatal("journal accepted trailing JSON document")
	}
}

func TestJournalMigratesLegacyBaselineAndUsesMode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	legacy := `[ {"forwardId":1,"userId":2,"userTunnelId":3,"up":10,"down":5} ]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	store := fileJournalStore{path: path}
	journal, err := store.load()
	if err != nil {
		t.Fatalf("migrate legacy journal: %v", err)
	}
	if journal.ReporterID == "" || len(journal.Baseline) != 1 || journal.Baseline[0].Up != 10 {
		t.Fatalf("migrated journal=%+v", journal)
	}
	reloaded, err := store.load()
	if err != nil || reloaded.ReporterID != journal.ReporterID {
		t.Fatalf("reporter id was not stable: %+v err=%v", reloaded, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode=%o, want 600", info.Mode().Perm())
	}
}

func TestJournalMigrateV2SteadyRetainsIdentityBaselineAndActiveTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	v2 := legacyV2Journal{
		Version: legacyJournalVersion, ReporterID: "v2-steady-reporter", LastSequence: 17,
		Baseline: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 10, Down: 5}},
	}
	writeRawJournalFixture(t, path, v2)
	got, err := (fileJournalStore{path: path}).load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != reporterJournalVersion || got.ActiveTable != "flux_panel" ||
		got.ReporterID != v2.ReporterID || got.LastSequence != v2.LastSequence ||
		!reflect.DeepEqual(got.Baseline, v2.Baseline) {
		t.Fatalf("migrated v2 steady=%+v", got)
	}
}

func TestJournalMigrateV2BoundedPendingRetainsExactBytesAndDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	v2 := legacyV2Journal{
		Version: legacyJournalVersion, ReporterID: "v2-pending-reporter", LastSequence: 8,
		Baseline:      []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 10}},
		DrainSnapshot: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 20, Down: 7}},
	}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: v2.ReporterID, Sequence: 9, BatchID: "v2-exact-batch",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 10, 7)},
	}
	payload, _ := json.Marshal(batch)
	v2.Pending = &pendingReporterBatch{Payload: payload, ResultingBaseline: append([]journalCounter(nil), v2.DrainSnapshot...)}
	writeRawJournalFixture(t, path, v2)
	got, err := (fileJournalStore{path: path}).load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveTable != "flux_panel" || got.Pending == nil || !bytes.Equal(got.Pending.Payload, payload) ||
		got.ReporterID != v2.ReporterID || got.LastSequence != v2.LastSequence ||
		!reflect.DeepEqual(got.DrainSnapshot, v2.DrainSnapshot) || got.DrainStartSequence == nil ||
		*got.DrainStartSequence != v2.LastSequence || !reflect.DeepEqual(got.DrainInitialBaseline, v2.Baseline) {
		t.Fatalf("migrated v2 pending=%+v", got)
	}
}

func TestJournalMigrateV2PartialDrainWithoutPendingRetainsSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	v2 := legacyV2Journal{
		Version: legacyJournalVersion, ReporterID: "v2-partial-reporter", LastSequence: 2,
		Baseline:      []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: dto.MaxNftFlowItemBytes}},
		DrainSnapshot: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: dto.MaxNftFlowItemBytes + 9}},
	}
	writeRawJournalFixture(t, path, v2)
	got, err := (fileJournalStore{path: path}).load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Pending != nil || got.ActiveTable != "flux_panel" || !reflect.DeepEqual(got.DrainSnapshot, v2.DrainSnapshot) ||
		got.DrainStartSequence == nil || *got.DrainStartSequence != v2.LastSequence ||
		!reflect.DeepEqual(got.DrainInitialBaseline, v2.Baseline) {
		t.Fatalf("migrated partial drain=%+v", got)
	}
}

func TestJournalRejectsDuplicateTopLevelKeyBeforeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	raw := `{"version":2,"version":2,"reporterId":"duplicate","lastSequence":0,"baseline":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (fileJournalStore{path: path}).load(); err == nil {
		t.Fatal("duplicate top-level journal key was accepted")
	}
}

func TestJournalRejectsFileAndCounterRowLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxJournalFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (fileJournalStore{path: path}).load(); err == nil {
		t.Fatal("oversized journal file was accepted")
	}

	journal := mustTestJournal(t)
	journal.Baseline = make([]journalCounter, maxJournalCounterRows+1)
	if err := validateJournal(journal); err == nil {
		t.Fatal("journal counter row limit was not enforced")
	}
}

func TestJournalRejectsSequencePayloadAndIllegalStateCombinations(t *testing.T) {
	base := mustTestJournal(t)
	cases := []struct {
		name   string
		mutate func(*reporterJournal)
	}{
		{name: "sequence exhausted", mutate: func(j *reporterJournal) { j.LastSequence = math.MaxInt64 }},
		{name: "invalid active table", mutate: func(j *reporterJournal) { j.ActiveTable = "flux_panel_bad" }},
		{name: "cleanup equals active", mutate: func(j *reporterJournal) { j.CleanupTable = j.ActiveTable }},
		{name: "legacy active cleanup", mutate: func(j *reporterJournal) { j.CleanupTable = testGenerationA }},
		{name: "cleanup with baseline", mutate: func(j *reporterJournal) {
			j.CleanupTable = testGenerationA
			j.Baseline = []journalCounter{{ForwardID: 1, UserID: 2}}
		}},
		{name: "handoff active mismatch", mutate: func(j *reporterJournal) {
			j.Handoff = &generationHandoff{RetiredTable: testGenerationA, TargetTable: testGenerationB}
		}},
		{name: "handoff identical tables", mutate: func(j *reporterJournal) {
			j.Handoff = &generationHandoff{RetiredTable: j.ActiveTable, TargetTable: j.ActiveTable}
		}},
		{name: "handoff targets legacy", mutate: func(j *reporterJournal) {
			j.ActiveTable = testGenerationA
			j.Handoff = &generationHandoff{RetiredTable: testGenerationA, TargetTable: "flux_panel"}
		}},
		{name: "marker with baseline", mutate: func(j *reporterJournal) {
			j.MarkerPending = true
			j.Baseline = []journalCounter{{ForwardID: 1, UserID: 2}}
		}},
		{name: "legacy active marker", mutate: func(j *reporterJournal) { j.MarkerPending = true }},
		{name: "drain missing start", mutate: func(j *reporterJournal) {
			setTestDrain(j, []journalCounter{{ForwardID: 1, UserID: 2, Up: 1}})
			j.DrainStartSequence = nil
		}},
		{name: "drain metadata without snapshot", mutate: func(j *reporterJournal) {
			j.DrainInitialBaseline = []journalCounter{{ForwardID: 1, UserID: 2}}
		}},
		{name: "handoff missing start", mutate: func(j *reporterJournal) {
			j.Handoff = &generationHandoff{RetiredTable: j.ActiveTable, TargetTable: testGenerationA}
		}},
		{name: "handoff start after last", mutate: func(j *reporterJournal) {
			j.Handoff = &generationHandoff{StartSequence: testSequencePointer(j.LastSequence + 1), RetiredTable: j.ActiveTable, TargetTable: testGenerationA}
		}},
		{name: "oversized pending payload", mutate: func(j *reporterJournal) {
			setTestDrain(j, []journalCounter{{ForwardID: 1, UserID: 2, Up: 1}})
			j.Pending = &pendingReporterBatch{Payload: bytes.Repeat([]byte{' '}, maxPendingPayloadBytes+1)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			journal := cloneTestJournal(base)
			tc.mutate(&journal)
			if err := validateJournal(journal); err == nil {
				t.Fatalf("illegal journal accepted: %+v", journal)
			}
		})
	}
}

func TestJournalRejectsDuplicateHandoffCounters(t *testing.T) {
	journal := mustTestJournal(t)
	row := journalCounter{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 9}
	journal.Handoff = &generationHandoff{
		StartSequence: testSequencePointer(journal.LastSequence),
		RetiredTable:  journal.ActiveTable, TargetTable: testGenerationA,
		RetiredBaseline: []journalCounter{row, row}, FrozenSnapshot: []journalCounter{row},
	}
	if err := validateJournal(journal); err == nil {
		t.Fatal("duplicate handoff retired baseline key was accepted")
	}
	journal.Handoff.RetiredBaseline = []journalCounter{}
	journal.Handoff.FrozenSnapshot = []journalCounter{row, row}
	if err := validateJournal(journal); err == nil {
		t.Fatal("duplicate frozen snapshot key was accepted")
	}
}

func TestJournalRejectsUnfrozenOrNonChunkedHandoffProgress(t *testing.T) {
	journal := mustTestJournal(t)
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 10}}
	journal.Handoff = &generationHandoff{
		StartSequence: testSequencePointer(journal.LastSequence),
		RetiredTable:  journal.ActiveTable, TargetTable: testGenerationA,
		RetiredBaseline: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 11}},
	}
	if err := validateJournal(journal); err == nil {
		t.Fatal("unfrozen handoff changed its retired baseline")
	}
	journal.Handoff.FrozenSnapshot = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: dto.MaxNftFlowItemBytes + 20}}
	journal.Handoff.RetiredBaseline[0].Up = 17
	if err := validateJournal(journal); err == nil {
		t.Fatal("handoff accepted progress not produced by bounded chunks")
	}
	journal.Handoff.RetiredBaseline[0].Up = 10 + dto.MaxNftFlowItemBytes
	journal.LastSequence = 1
	if err := validateJournal(journal); err != nil {
		t.Fatalf("valid bounded handoff progress rejected: %v", err)
	}
}

func TestJournalBindsHandoffProgressToAcknowledgedSequenceCount(t *testing.T) {
	journal := mustTestJournal(t)
	snapshot := makeCounterSnapshot(dto.MaxNftFlowBatchItems + 1)
	for key, value := range snapshot {
		value.Up, value.Down = 1, 0
		snapshot[key] = value
	}
	firstBatch := make(map[flowKey]counters, dto.MaxNftFlowBatchItems)
	for id := 1; id <= dto.MaxNftFlowBatchItems; id++ {
		firstBatch[flowKey{ForwardID: int64(id), UserID: 2, UserTunnelID: 3}] = counters{Up: 1}
	}
	journal.Handoff = &generationHandoff{
		StartSequence: testSequencePointer(journal.LastSequence),
		RetiredTable:  journal.ActiveTable, TargetTable: testGenerationA,
		RetiredBaseline: countersToJournal(firstBatch), FrozenSnapshot: countersToJournal(snapshot),
	}
	if err := validateJournal(journal); err == nil {
		t.Fatal("first-batch progress was accepted without an ACK sequence")
	}
	journal.LastSequence++
	if err := validateJournal(journal); err != nil {
		t.Fatalf("first-batch progress rejected after one ACK: %v", err)
	}
}

func TestJournalBindsSteadyDrainProgressToAcknowledgedSequenceCount(t *testing.T) {
	journal := mustTestJournal(t)
	snapshot := makeCounterSnapshot(dto.MaxNftFlowBatchItems + 1)
	for key, value := range snapshot {
		value.Up, value.Down = 1, 0
		snapshot[key] = value
	}
	setTestDrain(&journal, countersToJournal(snapshot))
	firstBatch := make(map[flowKey]counters, dto.MaxNftFlowBatchItems)
	for id := 1; id <= dto.MaxNftFlowBatchItems; id++ {
		firstBatch[flowKey{ForwardID: int64(id), UserID: 2, UserTunnelID: 3}] = counters{Up: 1}
	}
	journal.Baseline = countersToJournal(firstBatch)
	if err := validateJournal(journal); err == nil {
		t.Fatal("steady first-batch progress was accepted without an ACK sequence")
	}
	journal.LastSequence++
	if err := validateJournal(journal); err != nil {
		t.Fatalf("steady first-batch progress rejected after one ACK: %v", err)
	}
}

func TestExpectedSnapshotProgressHandlesMaximumCounterWithoutChunkLoop(t *testing.T) {
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	snapshot := countersToJournal(map[flowKey]counters{key: {Up: math.MaxInt64, Down: math.MaxInt64}})
	chunks := counterDirectionChunks(0, math.MaxInt64)
	got, err := expectedSnapshotProgress(nil, snapshot, chunks)
	if err != nil {
		t.Fatal(err)
	}
	if got[key] != (counters{Up: math.MaxInt64, Down: math.MaxInt64}) {
		t.Fatalf("maximum counter progress=%+v", got[key])
	}
}

func TestJournalRejectsHandoffProgressThatSkipsFirstTenThousandKeys(t *testing.T) {
	journal := mustTestJournal(t)
	snapshot := makeCounterSnapshot(dto.MaxNftFlowBatchItems + 1)
	for key, value := range snapshot {
		value.Up = 1
		value.Down = 0
		snapshot[key] = value
	}
	last := flowKey{ForwardID: int64(dto.MaxNftFlowBatchItems + 1), UserID: 2, UserTunnelID: 3}
	journal.Handoff = &generationHandoff{
		StartSequence: testSequencePointer(journal.LastSequence),
		RetiredTable:  journal.ActiveTable, TargetTable: testGenerationA,
		RetiredBaseline: countersToJournal(map[flowKey]counters{last: {Up: 1}}),
		FrozenSnapshot:  countersToJournal(snapshot),
	}
	if err := validateJournal(journal); err == nil {
		t.Fatal("handoff progress skipped the deterministic first 10,000 keys")
	}
}

func TestJournalRejectsHandoffProgressBetweenDirectionsOfOneAck(t *testing.T) {
	journal := mustTestJournal(t)
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	journal.Handoff = &generationHandoff{
		StartSequence: testSequencePointer(journal.LastSequence),
		RetiredTable:  journal.ActiveTable, TargetTable: testGenerationA,
		RetiredBaseline: countersToJournal(map[flowKey]counters{key: {Up: 1}}),
		FrozenSnapshot:  countersToJournal(map[flowKey]counters{key: {Up: 1, Down: 1}}),
	}
	if err := validateJournal(journal); err == nil {
		t.Fatal("handoff progress split directions that advance in one ACK")
	}
}

func TestJournalPredecodeRejectsCounterCollectionsAboveRowLimit(t *testing.T) {
	var rows strings.Builder
	rows.Grow((maxJournalCounterRows + 1) * 3)
	for i := 0; i < maxJournalCounterRows+1; i++ {
		if i > 0 {
			rows.WriteByte(',')
		}
		rows.WriteString(`{}`)
	}
	overLimit := rows.String()
	cases := []struct {
		name string
		raw  string
	}{
		{name: "historical array", raw: `[` + overLimit + `]`},
		{name: "v2 baseline", raw: `{"version":2,"reporterId":"r","lastSequence":0,"baseline":[` + overLimit + `]}`},
		{name: "v3 baseline", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[` + overLimit + `]}`},
		{name: "v3 drain snapshot", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[],"drainSnapshot":[` + overLimit + `]}`},
		{name: "v3 drain initial baseline", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[],"drainInitialBaseline":[` + overLimit + `]}`},
		{name: "handoff retired baseline", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[],"handoff":{"retiredTable":"flux_panel","targetTable":"` + testGenerationA + `","retiredBaseline":[` + overLimit + `]}}`},
		{name: "handoff frozen snapshot", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[],"handoff":{"retiredTable":"flux_panel","targetTable":"` + testGenerationA + `","retiredBaseline":[],"frozenSnapshot":[` + overLimit + `]}}`},
		{name: "pending resulting baseline", raw: `{"version":3,"reporterId":"r","lastSequence":0,"activeTable":"flux_panel","baseline":[],"drainSnapshot":[{}],"pending":{"payload":{},"resultingBaseline":[` + overLimit + `]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.raw) >= maxJournalFileBytes {
				t.Fatalf("fixture bytes=%d", len(tc.raw))
			}
			path := filepath.Join(t.TempDir(), "journal.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := (fileJournalStore{path: path}).load()
			if err == nil || !strings.Contains(err.Error(), "predecode counter row limit") {
				t.Fatalf("error=%v, want predecode row-limit failure", err)
			}
		})
	}
}

func writeRawJournalFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestJournalBindsPendingCaptureTimeToFrozenSnapshot(t *testing.T) {
	journal := validDrainJournalFixture(t)
	journal.DrainCapturedAt = testReporterCapturedAt
	var batch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(journal.Pending.Payload, &batch); err != nil {
		t.Fatal(err)
	}
	batch.CapturedAt = testReporterCapturedAt
	journal.Pending.Payload, _ = json.Marshal(batch)
	if err := validateJournal(journal); err != nil {
		t.Fatalf("matching capture time rejected: %v", err)
	}
	batch.CapturedAt++
	journal.Pending.Payload, _ = json.Marshal(batch)
	if err := validateJournal(journal); err == nil {
		t.Fatal("pending batch changed the frozen snapshot capture time")
	}
}

func TestJournalRejectsInvalidPendingResultingBaseline(t *testing.T) {
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "valid-batch",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 1, 1)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{
		Payload:           payload,
		ResultingBaseline: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: -1}},
	}
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("negative pending baseline was accepted")
	}
}

func TestJournalRejectsUnsafePendingBatchIdentity(t *testing.T) {
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "unsafe batch id",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 1, 1)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{Payload: payload, ResultingBaseline: []journalCounter{}}
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("unsafe pending batch identity was accepted")
	}
}

func TestJournalRejectsPendingBaselineInconsistentWithPayload(t *testing.T) {
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 10, Down: 5}}
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "baseline-bound",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 10, 2)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{
		Payload:           payload,
		ResultingBaseline: []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 99, Down: 99}},
	}
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("pending baseline unrelated to payload was accepted")
	}
}

func TestJournalRejectsPendingDirectionAboveProtocolLimit(t *testing.T) {
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	tooLarge := dto.MaxNftFlowItemBytes + 1
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "oversized-direction",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, tooLarge, 0)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{
		Payload: payload,
		ResultingBaseline: []journalCounter{
			{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: tooLarge},
		},
	}
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("pending direction above the shared protocol limit was accepted")
	}
}

func TestJournalRejectsDuplicateCounterKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*reporterJournal)
	}{
		{
			name: "baseline",
			mutate: func(journal *reporterJournal) {
				journal.Baseline = append(journal.Baseline, journal.Baseline[0])
			},
		},
		{
			name: "drain snapshot",
			mutate: func(journal *reporterJournal) {
				journal.DrainSnapshot = append(journal.DrainSnapshot, journal.DrainSnapshot[0])
			},
		},
		{
			name: "pending resulting baseline",
			mutate: func(journal *reporterJournal) {
				journal.Pending.ResultingBaseline = append(
					journal.Pending.ResultingBaseline, journal.Pending.ResultingBaseline[0],
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journal := validDrainJournalFixture(t)
			tt.mutate(&journal)
			if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
				t.Fatal("duplicate flow key was accepted")
			}
		})
	}
}

func TestJournalRejectsPendingResultingBaselineKeyDeletion(t *testing.T) {
	journal := validDrainJournalFixture(t)
	retained := journalCounter{ForwardID: 9, UserID: 8, UserTunnelID: 7, Up: 6, Down: 5}
	journal.Baseline = append(journal.Baseline, retained)
	journal.DrainSnapshot = append(journal.DrainSnapshot, retained)
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("pending resulting baseline silently deleted an old baseline key")
	}
}

func TestJournalRejectsAmbiguousPendingPayload(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte, reporterJournal) []byte
	}{
		{
			name: "unknown field",
			mutate: func(payload []byte, _ reporterJournal) []byte {
				return append(append([]byte(nil), payload[:len(payload)-1]...), []byte(`,"unknown":true}`)...)
			},
		},
		{
			name: "duplicate key",
			mutate: func(payload []byte, journal reporterJournal) []byte {
				prefix := fmt.Sprintf(`{"reporterId":%q,`, journal.ReporterID)
				return append([]byte(prefix), payload[1:]...)
			},
		},
		{
			name: "trailing document",
			mutate: func(payload []byte, _ reporterJournal) []byte {
				return append(append([]byte(nil), payload...), []byte(` {}`)...)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journal := validDrainJournalFixture(t)
			journal.Pending.Payload = tt.mutate(journal.Pending.Payload, journal)
			if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
				t.Fatal("ambiguous pending payload was accepted")
			}
		})
	}
}

func TestJournalRejectsPendingThatIsNotNextDrainSnapshotStep(t *testing.T) {
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	setTestDrain(&journal, countersToJournal(map[flowKey]counters{
		key: {Up: dto.MaxNftFlowItemBytes + 5},
	}))
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "short-nondeterministic-step",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 7, 0)},
	}
	payload, _ := json.Marshal(batch)
	journal.Pending = &pendingReporterBatch{
		Payload:           payload,
		ResultingBaseline: countersToJournal(map[flowKey]counters{key: {Up: 7}}),
	}
	if err := (fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}).save(journal); err == nil {
		t.Fatal("pending batch skipped the deterministic bounded snapshot step")
	}
}

func TestReporterMigratesBoundedLegacyPendingAndReplaysExactPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	active := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	secondActive := flowKey{ForwardID: 7, UserID: 8, UserTunnelID: 9}
	disappeared := flowKey{ForwardID: 4, UserID: 5, UserTunnelID: 6}
	previous := map[flowKey]counters{
		active: {Up: 10, Down: 5}, secondActive: {Up: 30}, disappeared: {Up: 99, Down: 8},
	}
	snapshot := map[flowKey]counters{active: {Up: 20, Down: 7}, secondActive: {Up: 41, Down: 3}}
	legacy, originalPayload := writeLegacyPendingJournal(t, path, previous, snapshot)
	var legacyBatch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(originalPayload, &legacyBatch); err != nil {
		t.Fatal(err)
	}
	sort.Slice(legacyBatch.Items, func(i, j int) bool {
		return *legacyBatch.Items[i].ForwardID > *legacyBatch.Items[j].ForwardID
	})
	originalPayload, _ = json.Marshal(legacyBatch)
	legacy.Pending.Payload = originalPayload
	legacyRaw, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, legacyRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	reads := 0
	var uploads [][]byte
	r := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			reads++
			return nil, errors.New("must not reread during migrated drain")
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			migrated, err := (fileJournalStore{path: path}).load()
			if err != nil {
				t.Fatal(err)
			}
			if migrated.Pending == nil || !bytes.Equal(migrated.Pending.Payload, originalPayload) {
				t.Fatal("legacy Pending identity was not durably preserved before upload")
			}
			if _, ok := journalToCounters(migrated.Pending.ResultingBaseline)[disappeared]; !ok {
				t.Fatal("migrated safe baseline deleted disappeared key before ACK")
			}
			assertBaselineEquals(t, migrated.DrainSnapshot, snapshot)
			uploads = append(uploads, append([]byte(nil), payload...))
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || len(uploads) != 1 || !bytes.Equal(uploads[0], originalPayload) {
		t.Fatalf("reads=%d uploads=%q original=%q", reads, uploads, originalPayload)
	}
	var replay dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(uploads[0], &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Sequence != legacy.LastSequence+1 || replay.BatchID != "legacy-pending-batch" {
		t.Fatalf("legacy identity changed: %+v", replay)
	}
	final, err := (fileJournalStore{path: path}).load()
	if err != nil {
		t.Fatal(err)
	}
	if final.LastSequence != 1 || final.Pending != nil || final.DrainSnapshot != nil {
		t.Fatalf("final migrated journal=%+v", final)
	}
	assertBaselineEquals(t, final.Baseline, snapshot)
}

func TestReporterMigratesLegacyPendingAboveItemLimitIntoBoundedBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	snapshot := makeCounterSnapshot(dto.MaxNftFlowBatchItems + 1)
	_, originalPayload := writeLegacyPendingJournal(t, path, map[flowKey]counters{}, snapshot)

	var uploads []dto.NftFlowBatchV2Dto
	r := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			t.Fatal("migrated oversized item batch reread live counters")
			return nil, nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			if bytes.Equal(payload, originalPayload) {
				t.Fatal("legacy 10,001-item payload was uploaded unchanged")
			}
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			uploads = append(uploads, batch)
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 2 || len(uploads[0].Items) != dto.MaxNftFlowBatchItems || len(uploads[1].Items) != 1 {
		t.Fatalf("migrated splits=%d first=%d", len(uploads), len(uploads[0].Items))
	}
	if uploads[0].Sequence != 1 || uploads[1].Sequence != 2 {
		t.Fatalf("migrated sequences=%d,%d", uploads[0].Sequence, uploads[1].Sequence)
	}
}

func TestReporterMigratesLegacyPendingAboveDirectionLimitIntoBoundedBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	snapshot := map[flowKey]counters{key: {Up: dto.MaxNftFlowItemBytes*2 + 7}}
	_, originalPayload := writeLegacyPendingJournal(t, path, map[flowKey]counters{}, snapshot)

	var uploads []dto.NftFlowBatchV2Dto
	r := reporter{
		store: fileJournalStore{path: path},
		readCounters: func(string) (map[flowKey]counters, error) {
			t.Fatal("migrated oversized direction reread live counters")
			return nil, nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			if bytes.Equal(payload, originalPayload) {
				t.Fatal("legacy oversized-direction payload was uploaded unchanged")
			}
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatal(err)
			}
			uploads = append(uploads, batch)
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 3 {
		t.Fatalf("migrated uploads=%d, want 3", len(uploads))
	}
	want := []int64{dto.MaxNftFlowItemBytes, dto.MaxNftFlowItemBytes, 7}
	for i, batch := range uploads {
		if batch.Sequence != uint64(i+1) || *batch.Items[0].Up != want[i] {
			t.Fatalf("batch %d=%+v", i, batch)
		}
	}
}

func TestReporterDoesNotUploadWhenLegacyPendingMigrationSaveFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	writeLegacyPendingJournal(t, path, map[flowKey]counters{}, map[flowKey]counters{key: {Up: 9}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uploaded := false
	read := false
	r := reporter{
		store: fileJournalStore{
			path: path,
			saveOverride: func(reporterJournal) error {
				return errors.New("injected migration save failure")
			},
		},
		readCounters: func(string) (map[flowKey]counters, error) {
			read = true
			return nil, nil
		},
		upload: func(_, _ string, _ []byte) (dto.NftFlowAckDto, error) {
			uploaded = true
			return dto.NftFlowAckDto{}, nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err == nil {
		t.Fatal("migration save failure should fail reporter")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if uploaded || read || !bytes.Equal(before, after) {
		t.Fatalf("uploaded=%v read=%v journalChanged=%v", uploaded, read, !bytes.Equal(before, after))
	}
}

func TestJournalRejectsLegacyPendingNotBoundToOldSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	legacy, _ := writeLegacyPendingJournal(
		t, path, map[flowKey]counters{key: {Up: 10}}, map[flowKey]counters{key: {Up: 20}},
	)
	legacy.Pending.ResultingBaseline[0].Up = 21
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (fileJournalStore{path: path}).load(); err == nil {
		t.Fatal("legacy Pending unrelated to its old full snapshot was migrated")
	}
}

func writeLegacyPendingJournal(
	t *testing.T,
	path string,
	previous, snapshot map[flowKey]counters,
) (legacyV2Journal, []byte) {
	t.Helper()
	journal := legacyV2Journal{
		Version: legacyJournalVersion, ReporterID: "legacy-reporter", Baseline: countersToJournal(previous),
	}
	items := legacyFullDeltaItems(previous, snapshot)
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID,
		Sequence:   journal.LastSequence + 1,
		BatchID:    "legacy-pending-batch",
		Items:      items,
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	journal.Pending = &pendingReporterBatch{
		Payload:           payload,
		ResultingBaseline: countersToJournal(snapshot),
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return journal, append([]byte(nil), payload...)
}

func validDrainJournalFixture(t *testing.T) reporterJournal {
	t.Helper()
	journal, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 10, Down: 5}}
	setTestDrain(&journal, []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 20, Down: 7}})
	batch := dto.NftFlowBatchV2Dto{
		ReporterID: journal.ReporterID, Sequence: 1, BatchID: "valid-drain-batch",
		Items: []dto.NftFlowItem{reporterDTOItem(1, 2, 3, 10, 2)},
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	journal.Pending = &pendingReporterBatch{
		Payload:           payload,
		ResultingBaseline: append([]journalCounter(nil), journal.DrainSnapshot...),
	}
	return journal
}

func setTestDrain(journal *reporterJournal, snapshot []journalCounter) {
	start := journal.LastSequence
	journal.DrainSnapshot = append([]journalCounter(nil), snapshot...)
	journal.DrainInitialBaseline = canonicalJournalCounters(journal.Baseline)
	journal.DrainStartSequence = &start
}

func testSequencePointer(sequence uint64) *uint64 { return &sequence }

type failSaveJournalStore struct {
	delegate fileJournalStore
	saves    int
	failAt   int
}

func (s *failSaveJournalStore) load() (reporterJournal, error) { return s.delegate.load() }
func (s *failSaveJournalStore) save(journal reporterJournal) error {
	s.saves++
	if s.saves == s.failAt {
		return errors.New("injected journal save failure")
	}
	return s.delegate.save(journal)
}

func matchingAck(t *testing.T, payload []byte) dto.NftFlowAckDto {
	t.Helper()
	var batch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(payload, &batch); err != nil {
		t.Fatalf("decode pending batch: %v", err)
	}
	digest, err := dto.NftFlowBatchDigest(batch)
	if err != nil {
		t.Fatalf("digest pending batch: %v", err)
	}
	return dto.NftFlowAckDto{ReporterID: batch.ReporterID, Sequence: batch.Sequence, BatchID: batch.BatchID, AckDigest: digest}
}

func reporterCounterFixture() map[flowKey]counters {
	return map[flowKey]counters{{ForwardID: 1, UserID: 2, UserTunnelID: 3}: {Up: 20, Down: 7}}
}

func runReporterAgainstSnapshot(t *testing.T, snapshot map[flowKey]counters) ([]dto.NftFlowBatchV2Dto, reporterJournal, int) {
	t.Helper()
	store := fileJournalStore{path: filepath.Join(t.TempDir(), "journal.json")}
	reads := 0
	uploads := make([]dto.NftFlowBatchV2Dto, 0)
	r := reporter{
		store: store,
		now:   fixedReporterNow,
		readCounters: func(string) (map[flowKey]counters, error) {
			reads++
			return cloneCounters(snapshot), nil
		},
		upload: func(_, _ string, payload []byte) (dto.NftFlowAckDto, error) {
			var batch dto.NftFlowBatchV2Dto
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatalf("decode upload: %v", err)
			}
			uploads = append(uploads, batch)
			return matchingAck(t, payload), nil
		},
	}
	if err := r.runOnce("panel", "secret", "table"); err != nil {
		t.Fatalf("run reporter: %v", err)
	}
	final, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	return uploads, final, reads
}

func makeCounterSnapshot(size int) map[flowKey]counters {
	snapshot := make(map[flowKey]counters, size)
	for i := 0; i < size; i++ {
		id := int64(i + 1)
		snapshot[flowKey{ForwardID: id, UserID: 2, UserTunnelID: 3}] = counters{Up: id, Down: id + 1}
	}
	return snapshot
}

func assertBaselineEquals(t *testing.T, got []journalCounter, want map[flowKey]counters) {
	t.Helper()
	gotCounters := journalToCounters(got)
	if len(gotCounters) != len(want) {
		t.Fatalf("baseline entries=%d, want %d", len(gotCounters), len(want))
	}
	for key, value := range want {
		if gotCounters[key] != value {
			t.Fatalf("baseline[%s]=%+v, want %+v", formatFlowKey(key), gotCounters[key], value)
		}
	}
}

func formatFlowKey(key flowKey) string {
	return fmt.Sprintf("%d:%d:%d", key.ForwardID, key.UserID, key.UserTunnelID)
}
