package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nXiaoK/go-panel/internal/dto"
)

type handoffFakeRuntime struct {
	tables                 []GenerationTable
	counters               map[string]map[flowKey]counters
	events                 []string
	stageCalls             int
	switchCommittedErr     bool
	deleteFailBeforeCommit bool
	deleteCommittedErr     bool
	readErr                error
	discoverErr            error
}

func (r *handoffFakeRuntime) Probe(context.Context) error {
	r.events = append(r.events, "probe")
	return nil
}

func (r *handoffFakeRuntime) Discover(context.Context) ([]GenerationTable, error) {
	r.events = append(r.events, "discover")
	if r.discoverErr != nil {
		return nil, r.discoverErr
	}
	return append([]GenerationTable(nil), r.tables...), nil
}

func (r *handoffFakeRuntime) Stage(_ context.Context, table string, _ []string) error {
	r.events = append(r.events, "stage:"+table)
	r.stageCalls++
	r.tables = append(r.tables, GenerationTable{Name: table, Dormant: true})
	if r.counters == nil {
		r.counters = map[string]map[flowKey]counters{}
	}
	r.counters[table] = map[flowKey]counters{}
	return nil
}

func (r *handoffFakeRuntime) Switch(_ context.Context, oldTable, newTable string) error {
	r.events = append(r.events, "switch:"+oldTable+":"+newTable)
	foundOld := false
	for i := range r.tables {
		switch r.tables[i].Name {
		case oldTable:
			foundOld = true
			r.tables[i].Dormant = true
		case newTable:
			r.tables[i].Dormant = false
		}
	}
	if !foundOld {
		r.tables = append(r.tables, GenerationTable{Name: oldTable, Dormant: true})
		if r.counters == nil {
			r.counters = map[string]map[flowKey]counters{}
		}
		r.counters[oldTable] = map[flowKey]counters{}
	}
	if r.switchCommittedErr {
		r.switchCommittedErr = false
		return errors.New("switch result unknown")
	}
	return nil
}

func (r *handoffFakeRuntime) ReadCounters(_ context.Context, table string) (map[flowKey]counters, error) {
	r.events = append(r.events, "read:"+table)
	if r.readErr != nil {
		return nil, r.readErr
	}
	return cloneCounters(r.counters[table]), nil
}

func (r *handoffFakeRuntime) Delete(_ context.Context, table string) error {
	r.events = append(r.events, "delete:"+table)
	if r.deleteFailBeforeCommit {
		r.deleteFailBeforeCommit = false
		return errors.New("crash before delete committed")
	}
	for i := range r.tables {
		if r.tables[i].Name == table {
			r.tables = append(r.tables[:i], r.tables[i+1:]...)
			break
		}
	}
	delete(r.counters, table)
	if r.deleteCommittedErr {
		r.deleteCommittedErr = false
		return errors.New("delete result unknown")
	}
	return nil
}

type handoffPanel struct {
	committed        map[uint64]string
	totals           map[flowKey]counters
	attempts         [][]byte
	failBeforeCommit bool
	failAfterCommit  bool
}

func (p *handoffPanel) upload(_ string, _ string, payload []byte) (dto.NftFlowAckDto, error) {
	p.attempts = append(p.attempts, append([]byte(nil), payload...))
	var batch dto.NftFlowBatchV2Dto
	if err := json.Unmarshal(payload, &batch); err != nil {
		return dto.NftFlowAckDto{}, err
	}
	if p.failBeforeCommit {
		p.failBeforeCommit = false
		return dto.NftFlowAckDto{}, errors.New("transport failed before server commit")
	}
	digest, err := dto.NftFlowBatchDigest(batch)
	if err != nil {
		return dto.NftFlowAckDto{}, err
	}
	if prior, ok := p.committed[batch.Sequence]; ok {
		if prior != digest {
			return dto.NftFlowAckDto{}, errors.New("sequence content changed")
		}
		return dto.NftFlowAckDto{ReporterID: batch.ReporterID, Sequence: batch.Sequence, BatchID: batch.BatchID, AckDigest: digest}, nil
	}
	for _, item := range batch.Items {
		key := flowKey{ForwardID: *item.ForwardID, UserID: *item.UserID}
		if item.UserTunnelID != nil {
			key.UserTunnelID = *item.UserTunnelID
		}
		value := p.totals[key]
		value.Up += *item.Up
		value.Down += *item.Down
		p.totals[key] = value
	}
	p.committed[batch.Sequence] = digest
	if p.failAfterCommit {
		p.failAfterCommit = false
		return dto.NftFlowAckDto{}, errors.New("ack lost after server commit")
	}
	return dto.NftFlowAckDto{ReporterID: batch.ReporterID, Sequence: batch.Sequence, BatchID: batch.BatchID, AckDigest: digest}, nil
}

type namedFaultJournalStore struct {
	delegate journalStore
	failName string
	failed   bool
	last     *reporterJournal
}

func (s *namedFaultJournalStore) load() (reporterJournal, error) {
	j, err := s.delegate.load()
	if err == nil {
		copy := j
		s.last = &copy
	}
	return j, err
}

func (s *namedFaultJournalStore) save(j reporterJournal) error {
	name := journalSavePoint(s.last, j)
	if !s.failed && name == s.failName {
		s.failed = true
		return fmt.Errorf("crash at %s", name)
	}
	if err := s.delegate.save(j); err != nil {
		return err
	}
	copy := j
	s.last = &copy
	return nil
}

func journalSavePoint(previous *reporterJournal, next reporterJournal) string {
	if next.Handoff != nil && (previous == nil || previous.Handoff == nil) {
		return "handoff"
	}
	if next.Handoff != nil && len(next.Handoff.FrozenSnapshot) > 0 &&
		(previous == nil || previous.Handoff == nil || len(previous.Handoff.FrozenSnapshot) == 0) {
		return "frozen"
	}
	if next.Pending != nil && (previous == nil || previous.Pending == nil) {
		return "pending"
	}
	if previous != nil && previous.Pending != nil && next.Pending == nil && next.Handoff != nil {
		return "ack"
	}
	if next.CleanupTable != "" && (previous == nil || previous.CleanupTable == "") {
		return "final"
	}
	if previous != nil && previous.CleanupTable != "" && next.CleanupTable == "" {
		return "cleanup-clear"
	}
	return "other"
}

func TestRecoverSwitchBeforeHandoffJournal(t *testing.T) {
	runHandoffCrashCase(t, "handoff", func(*handoffFakeRuntime, *handoffPanel) {})
}

func TestRecoverHandoffBeforeFrozenSnapshot(t *testing.T) {
	runHandoffCrashCase(t, "frozen", func(*handoffFakeRuntime, *handoffPanel) {})
}

func TestRecoverPendingBeforeSend(t *testing.T) {
	runHandoffCrashCase(t, "", func(_ *handoffFakeRuntime, panel *handoffPanel) {
		panel.failBeforeCommit = true
	})
}

func TestRecoverServerCommitBeforeAck(t *testing.T) {
	runHandoffCrashCase(t, "", func(_ *handoffFakeRuntime, panel *handoffPanel) {
		panel.failAfterCommit = true
	})
}

func TestRecoverAckBeforeJournalSave(t *testing.T) {
	runHandoffCrashCase(t, "ack", func(*handoffFakeRuntime, *handoffPanel) {})
}

func TestRecoverFinalSaveBeforeRetiredDelete(t *testing.T) {
	runHandoffCrashCase(t, "delete", func(runtime *handoffFakeRuntime, _ *handoffPanel) {
		runtime.deleteFailBeforeCommit = true
	})
}

func TestRecoverRetiredDeleteBeforeCleanupClear(t *testing.T) {
	runHandoffCrashCase(t, "cleanup-clear", func(*handoffFakeRuntime, *handoffPanel) {})
}

func runHandoffCrashCase(t *testing.T, saveFault string, configure func(*handoffFakeRuntime, *handoffPanel)) {
	t.Helper()
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	base := &memoryJournalStore{journal: mustTestJournal(t)}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: "flux_panel"}},
		counters: map[string]map[flowKey]counters{"flux_panel": {key: {Up: dto.MaxNftFlowItemBytes + 9, Down: 7}}},
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	configure(runtime, panel)
	faultName := saveFault
	if saveFault == "delete" {
		faultName = ""
	}
	faults := &namedFaultJournalStore{delegate: base, failName: faultName}
	markers := 0
	first := newHandoffTestReporter(faults, runtime, panel, &markers)
	err := first.refreshRules(context.Background(), refreshRequest{
		Rules: []string{testCanonicalRule}, ServerAddr: "panel", Secret: "secret",
		CurrentTable: "flux_panel", TargetTable: testGenerationA,
	})
	if err == nil {
		t.Fatalf("first process did not fail at requested boundary %q", saveFault)
	}
	if saveFault != "" && saveFault != "delete" && !faults.failed {
		t.Fatalf("named journal boundary %q was never reached (error %v)", saveFault, err)
	}
	var persistedPending []byte
	if saveFault == "" {
		pending, loadErr := base.load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if pending.Pending == nil || pending.LastSequence != 0 {
			t.Fatalf("transport boundary did not retain unacknowledged Pending: %+v", pending)
		}
		persistedPending = append([]byte(nil), pending.Pending.Payload...)
		if len(panel.attempts) != 1 || len(persistedPending) == 0 {
			t.Fatalf("transport boundary attempts=%d pending=%+v", len(panel.attempts), pending.Pending)
		}
	}
	if saveFault == "delete" {
		cleanup, loadErr := base.load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if cleanup.CleanupTable != "flux_panel" || cleanup.Handoff != nil || cleanup.Pending != nil {
			t.Fatalf("final transition was not durable before delete boundary: %+v", cleanup)
		}
		if indexEvent(runtime.events, "delete:flux_panel") < 0 {
			t.Fatal("delete boundary was never reached")
		}
	}

	// A fresh process instance must reconcile kernel and durable journal state.
	restarted := newHandoffTestReporter(base, runtime, panel, &markers)
	journal, loadErr := base.load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if recoverErr := restarted.recoverRefresh(context.Background(), &journal); recoverErr != nil {
		t.Fatalf("restart recover: %v (first error %v)", recoverErr, err)
	}
	if saveFault == "" && (len(panel.attempts) < 2 || !bytes.Equal(panel.attempts[1], persistedPending)) {
		t.Fatalf("restart did not replay exact pending bytes: %q / %q", panel.attempts, persistedPending)
	}
	final, _ := base.load()
	if final.ActiveTable != testGenerationA || final.Pending != nil || final.Handoff != nil || final.CleanupTable != "" {
		t.Fatalf("final journal=%+v", final)
	}
	if !reflect.DeepEqual(runtime.tables, []GenerationTable{{Name: testGenerationA}}) {
		t.Fatalf("kernel tables=%+v", runtime.tables)
	}
	if panel.totals[key] != (counters{Up: dto.MaxNftFlowItemBytes + 9, Down: 7}) {
		t.Fatalf("panel totals=%+v", panel.totals)
	}
	for index, payload := range panel.attempts {
		var batch dto.NftFlowBatchV2Dto
		if err := json.Unmarshal(payload, &batch); err != nil {
			t.Fatal(err)
		}
		if batch.CapturedAt != testReporterCapturedAt {
			t.Fatalf("handoff attempt %d capture time=%d, want frozen snapshot time %d", index, batch.CapturedAt, testReporterCapturedAt)
		}
	}
	if markers == 0 {
		t.Fatal("active marker was not written after cleanup")
	}
	deleteIndex, markerIndex := indexEvent(runtime.events, "delete:flux_panel"), indexEvent(runtime.events, "marker:"+testGenerationA)
	if deleteIndex < 0 || markerIndex < deleteIndex {
		t.Fatalf("events=%v, retired table must be deleted before marker", runtime.events)
	}
}

func TestRecoverCleanupDeleteCommittedButReturnedError(t *testing.T) {
	journal := mustTestJournal(t)
	journal.ActiveTable = testGenerationA
	journal.CleanupTable = "flux_panel"
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables:             []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}},
		counters:           map[string]map[flowKey]counters{"flux_panel": {}, testGenerationA: {}},
		deleteCommittedErr: true,
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	loaded, _ := store.load()
	if err := r.recoverRefresh(context.Background(), &loaded); err != nil {
		t.Fatalf("committed cleanup delete must reconcile: %v", err)
	}
	if store.journal.CleanupTable != "" || store.journal.MarkerPending || !reflect.DeepEqual(runtime.tables, []GenerationTable{{Name: testGenerationA}}) {
		t.Fatalf("journal=%+v tables=%+v", store.journal, runtime.tables)
	}
}

func newHandoffTestReporter(store journalStore, runtime *handoffFakeRuntime, panel *handoffPanel, markers *int) reporter {
	return reporter{
		store: store, runtime: runtime, serverAddr: "panel", secret: "secret", upload: panel.upload,
		now: fixedReporterNow,
		writeActiveMarker: func(table string) error {
			*markers++
			runtime.events = append(runtime.events, "marker:"+table)
			return nil
		},
	}
}

func indexEvent(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

func TestRecoverSwitchCommandCommittedButReturnedError(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables: []GenerationTable{{Name: "flux_panel"}}, counters: map[string]map[flowKey]counters{"flux_panel": {}},
		switchCommittedErr: true,
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationA}); err != nil {
		t.Fatalf("committed switch must reconcile: %v", err)
	}
	if runtime.stageCalls != 1 || store.journal.ActiveTable != testGenerationA {
		t.Fatalf("stageCalls=%d journal=%+v", runtime.stageCalls, store.journal)
	}
}

func TestRecoverStableSnapshotTimeoutRetainsBothGenerations(t *testing.T) {
	journal := mustTestJournal(t)
	journal.Handoff = &generationHandoff{StartSequence: testSequencePointer(journal.LastSequence), RetiredTable: "flux_panel", TargetTable: testGenerationA, RetiredBaseline: []journalCounter{}}
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}},
		counters: map[string]map[flowKey]counters{"flux_panel": {}}, readErr: errors.New("unstable read"),
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loaded := journal
	if err := r.recoverRefresh(ctx, &loaded); err == nil {
		t.Fatal("stable snapshot timeout was accepted")
	}
	if len(runtime.tables) != 2 || runtime.stageCalls != 0 || store.journal.Handoff == nil {
		t.Fatalf("unsafe timeout state runtime=%+v journal=%+v", runtime, store.journal)
	}
}

func TestRecoverMarkerFailureIsRetriedWithoutAnotherStage(t *testing.T) {
	journal := mustTestJournal(t)
	journal.ActiveTable = testGenerationA
	journal.MarkerPending = true
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{{Name: testGenerationA}}, counters: map[string]map[flowKey]counters{testGenerationA: {}}}
	markerCalls := 0
	r := reporter{store: store, runtime: runtime, writeActiveMarker: func(string) error {
		markerCalls++
		if markerCalls == 1 {
			return errors.New("marker sync failed")
		}
		return nil
	}}
	loaded := journal
	if err := r.recoverRefresh(context.Background(), &loaded); err == nil {
		t.Fatal("marker failure must fail recovery")
	}
	loaded, _ = store.load()
	if err := r.recoverRefresh(context.Background(), &loaded); err != nil {
		t.Fatal(err)
	}
	if markerCalls != 2 || runtime.stageCalls != 0 {
		t.Fatalf("markerCalls=%d stageCalls=%d", markerCalls, runtime.stageCalls)
	}
}

func TestSecondRefreshRefusesStageWhileStateUnresolved(t *testing.T) {
	base := mustTestJournal(t)
	key := journalCounter{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 1}
	cases := []struct {
		name    string
		journal reporterJournal
		tables  []GenerationTable
	}{
		{name: "handoff", journal: func() reporterJournal {
			j := base
			j.Handoff = &generationHandoff{StartSequence: testSequencePointer(j.LastSequence), RetiredTable: "flux_panel", TargetTable: testGenerationA, RetiredBaseline: []journalCounter{}, FrozenSnapshot: []journalCounter{key}}
			return j
		}(), tables: []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}}},
		{name: "cleanup", journal: func() reporterJournal {
			j := base
			j.ActiveTable = testGenerationA
			j.CleanupTable = "flux_panel"
			return j
		}(), tables: []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}}},
		{name: "two active", journal: base, tables: []GenerationTable{{Name: "flux_panel"}, {Name: testGenerationA}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &memoryJournalStore{journal: tc.journal}
			runtime := &handoffFakeRuntime{tables: tc.tables, counters: map[string]map[flowKey]counters{"flux_panel": {}, testGenerationA: {}}}
			panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
			markers := 0
			r := newHandoffTestReporter(store, runtime, panel, &markers)
			_ = r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationB})
			if runtime.stageCalls != 0 {
				t.Fatalf("unresolved state staged another generation: events=%v", runtime.events)
			}
		})
	}
}

func TestRefreshSteadyPendingFailureDoesNotStage(t *testing.T) {
	journal := mustTestJournal(t)
	setTestDrain(&journal, []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 9}})
	journal.Pending = validTestPending(t, journal, nil, journal.DrainSnapshot)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{{Name: "flux_panel"}}, counters: map[string]map[flowKey]counters{"flux_panel": {}}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}, failBeforeCommit: true}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationA}); err == nil {
		t.Fatal("pending upload failure was accepted")
	}
	if runtime.stageCalls != 0 || store.journal.Pending == nil {
		t.Fatalf("stageCalls=%d journal=%+v", runtime.stageCalls, store.journal)
	}
}

func TestRefreshSteadyPendingSuccessContinuesToStageCurrentRules(t *testing.T) {
	journal := mustTestJournal(t)
	setTestDrain(&journal, []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 9}})
	journal.Pending = validTestPending(t, journal, nil, journal.DrainSnapshot)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{{Name: "flux_panel"}}, counters: map[string]map[flowKey]counters{"flux_panel": {}}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationA}); err != nil {
		t.Fatal(err)
	}
	if runtime.stageCalls != 1 || store.journal.ActiveTable != testGenerationA {
		t.Fatalf("stageCalls=%d journal=%+v", runtime.stageCalls, store.journal)
	}
}

func TestRefreshRetriesFinalMarkerWithoutStagingSecondGeneration(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{{Name: "flux_panel"}}, counters: map[string]map[flowKey]counters{"flux_panel": {}}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markerCalls := 0
	r := reporter{
		store: store, runtime: runtime, serverAddr: "panel", secret: "secret", upload: panel.upload,
		writeActiveMarker: func(string) error {
			markerCalls++
			// The first marker call is the pre-stage reconciliation. Fail only the
			// marker after the generation handoff and cleanup are durable.
			if markerCalls == 2 {
				return errors.New("final marker failed")
			}
			return nil
		},
	}
	request := refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationA}
	if err := r.refreshRules(context.Background(), request); err == nil {
		t.Fatal("final marker failure was accepted")
	}
	if runtime.stageCalls != 1 || !store.journal.MarkerPending {
		t.Fatalf("after marker failure stageCalls=%d journal=%+v", runtime.stageCalls, store.journal)
	}
	if err := r.refreshRules(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if runtime.stageCalls != 1 || store.journal.MarkerPending {
		t.Fatalf("marker retry staged another generation: calls=%d journal=%+v", runtime.stageCalls, store.journal)
	}
}

func validTestPending(t *testing.T, journal reporterJournal, previous, snapshot []journalCounter) *pendingReporterBatch {
	t.Helper()
	items, resulting, ok := nextBoundedBatch(journalToCounters(previous), journalToCounters(snapshot))
	if !ok {
		t.Fatal("fixture has no delta")
	}
	batch := dto.NftFlowBatchV2Dto{ReporterID: journal.ReporterID, Sequence: journal.LastSequence + 1, BatchID: "test-pending", Items: items}
	payload, _ := json.Marshal(batch)
	return &pendingReporterBatch{Payload: payload, ResultingBaseline: countersToJournal(resulting)}
}

type memoryJournalStore struct{ journal reporterJournal }

func (s *memoryJournalStore) load() (reporterJournal, error) { return cloneTestJournal(s.journal), nil }
func (s *memoryJournalStore) save(j reporterJournal) error {
	if err := validateJournal(j); err != nil {
		return err
	}
	s.journal = cloneTestJournal(j)
	return nil
}

func cloneTestJournal(j reporterJournal) reporterJournal {
	raw, _ := json.Marshal(j)
	var out reporterJournal
	_ = json.Unmarshal(raw, &out)
	return out
}

func mustTestJournal(t *testing.T) reporterJournal {
	t.Helper()
	j, err := newReporterJournal()
	if err != nil {
		t.Fatal(err)
	}
	j.ReporterID = "handoff-test-reporter"
	return j
}

func TestRefreshDeletesOnlyUnreferencedStagedTableBeforeRestaging(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: "flux_panel"}, {Name: testGenerationA, Dormant: true}},
		counters: map[string]map[flowKey]counters{"flux_panel": {}, testGenerationA: {}},
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationB}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runtime.events, ",")
	if !strings.Contains(joined, "discover,delete:"+testGenerationA) || runtime.stageCalls != 1 {
		t.Fatalf("events=%v", runtime.events)
	}
}

func TestRefreshRejectsDormantLegacyBesideGeneratedActive(t *testing.T) {
	journal := mustTestJournal(t)
	journal.ActiveTable = testGenerationA
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}},
		counters: map[string]map[flowKey]counters{"flux_panel": {}, testGenerationA: {}},
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, TargetTable: testGenerationB}); err == nil {
		t.Fatal("dormant legacy beside generated active was treated as staged")
	}
	if runtime.stageCalls != 0 || indexEvent(runtime.events, "delete:flux_panel") >= 0 {
		t.Fatalf("legacy ownership contradiction mutated state: %v", runtime.events)
	}
}

func TestRefreshBootstrapsPristineJournalFromEmptyInventory(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{}, counters: map[string]map[flowKey]counters{}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationA}); err != nil {
		t.Fatal(err)
	}
	if runtime.stageCalls != 1 || !reflect.DeepEqual(runtime.tables, []GenerationTable{{Name: testGenerationA}}) ||
		store.journal.ActiveTable != testGenerationA || store.journal.Handoff != nil || store.journal.CleanupTable != "" {
		t.Fatalf("runtime=%+v journal=%+v", runtime, store.journal)
	}
	wantOrder := []string{"discover", "probe", "stage:" + testGenerationA, "switch:flux_panel:" + testGenerationA}
	if len(runtime.events) < len(wantOrder) || !reflect.DeepEqual(runtime.events[:len(wantOrder)], wantOrder) {
		t.Fatalf("bootstrap events=%v, want prefix %v", runtime.events, wantOrder)
	}
}

func TestRefreshRecoversPristineCrashAfterStageWithNoActiveTable(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: testGenerationA, Dormant: true}},
		counters: map[string]map[flowKey]counters{testGenerationA: {}},
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, CurrentTable: "flux_panel", TargetTable: testGenerationB}); err != nil {
		t.Fatal(err)
	}
	if runtime.stageCalls != 1 || !reflect.DeepEqual(runtime.tables, []GenerationTable{{Name: testGenerationB}}) {
		t.Fatalf("runtime=%+v", runtime)
	}
	if indexEvent(runtime.events, "delete:"+testGenerationA) < 0 ||
		indexEvent(runtime.events, "delete:"+testGenerationA) > indexEvent(runtime.events, "stage:"+testGenerationB) {
		t.Fatalf("staged orphan was not deleted before retry: %v", runtime.events)
	}
}

func TestRefreshRejectsNoActiveInventoryForNonPristineJournal(t *testing.T) {
	journal := mustTestJournal(t)
	journal.Baseline = []journalCounter{{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 9}}
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{}, counters: map[string]map[flowKey]counters{}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, TargetTable: testGenerationA}); err == nil {
		t.Fatal("non-pristine no-active inventory was accepted")
	}
	if runtime.stageCalls != 0 || len(runtime.tables) != 0 {
		t.Fatalf("unsafe mutation runtime=%+v", runtime)
	}
}

func TestRefreshDiscoveryErrorPerformsNoMutation(t *testing.T) {
	journal := mustTestJournal(t)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{discoverErr: errors.New("inventory unavailable")}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := r.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, TargetTable: testGenerationA}); err == nil {
		t.Fatal("discovery failure was accepted")
	}
	if runtime.stageCalls != 0 || indexEvent(runtime.events, "delete:"+testGenerationA) >= 0 || indexEvent(runtime.events, "probe") >= 0 {
		t.Fatalf("discovery error mutated nft state: %v", runtime.events)
	}
}

type persistThenStopStore struct {
	delegate journalStore
	stopped  bool
}

func (s *persistThenStopStore) load() (reporterJournal, error) { return s.delegate.load() }
func (s *persistThenStopStore) save(j reporterJournal) error {
	if err := s.delegate.save(j); err != nil {
		return err
	}
	if !s.stopped && j.Pending != nil {
		s.stopped = true
		return errors.New("process stopped after pending fsync")
	}
	return nil
}

func TestRecoverPendingSavedThenProcessStopsBeforeUpload(t *testing.T) {
	journal := mustTestJournal(t)
	base := &memoryJournalStore{journal: journal}
	store := &persistThenStopStore{delegate: base}
	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	runtime := &handoffFakeRuntime{
		tables:   []GenerationTable{{Name: "flux_panel"}},
		counters: map[string]map[flowKey]counters{"flux_panel": {key: {Up: 9}}},
	}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	first := newHandoffTestReporter(store, runtime, panel, &markers)
	if err := first.refreshRules(context.Background(), refreshRequest{Rules: []string{testCanonicalRule}, TargetTable: testGenerationA}); err == nil {
		t.Fatal("post-persist process stop was accepted")
	}
	if len(panel.attempts) != 0 || base.journal.Pending == nil || base.journal.LastSequence != 0 {
		t.Fatalf("upload happened before restart: attempts=%d journal=%+v", len(panel.attempts), base.journal)
	}
	want := append([]byte(nil), base.journal.Pending.Payload...)
	restarted := newHandoffTestReporter(base, runtime, panel, &markers)
	loaded, _ := base.load()
	if err := restarted.recoverRefresh(context.Background(), &loaded); err != nil {
		t.Fatal(err)
	}
	if len(panel.attempts) != 1 || !bytes.Equal(panel.attempts[0], want) {
		t.Fatalf("restart did not replay exact persisted pending: %q / %q", panel.attempts, want)
	}
}

func TestRecoverPendingReplaysExactBytes(t *testing.T) {
	journal := mustTestJournal(t)
	key := journalCounter{ForwardID: 1, UserID: 2, UserTunnelID: 3, Up: 9}
	journal.Handoff = &generationHandoff{StartSequence: testSequencePointer(journal.LastSequence), RetiredTable: "flux_panel", TargetTable: testGenerationA, FrozenSnapshot: []journalCounter{key}}
	journal.Pending = validTestPending(t, journal, nil, journal.Handoff.FrozenSnapshot)
	want := append([]byte(nil), journal.Pending.Payload...)
	store := &memoryJournalStore{journal: journal}
	runtime := &handoffFakeRuntime{tables: []GenerationTable{{Name: "flux_panel", Dormant: true}, {Name: testGenerationA}}, counters: map[string]map[flowKey]counters{"flux_panel": {}}}
	panel := &handoffPanel{committed: map[uint64]string{}, totals: map[flowKey]counters{}}
	markers := 0
	r := newHandoffTestReporter(store, runtime, panel, &markers)
	loaded, _ := store.load()
	if err := r.recoverRefresh(context.Background(), &loaded); err != nil {
		t.Fatal(err)
	}
	if len(panel.attempts) != 1 || !bytes.Equal(panel.attempts[0], want) {
		t.Fatalf("pending replay changed: %q / %q", panel.attempts, want)
	}
}
