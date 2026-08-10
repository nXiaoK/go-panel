package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeGenerationRuntime struct {
	probeErr  error
	stageErr  error
	switchErr error
	reads     []fakeCounterRead
	events    []string
}

type fakeCounterRead struct {
	value map[flowKey]counters
	err   error
}

func (r *fakeGenerationRuntime) Probe(context.Context) error {
	r.events = append(r.events, "probe")
	return r.probeErr
}

func (r *fakeGenerationRuntime) Discover(context.Context) ([]GenerationTable, error) {
	return nil, nil
}

func (r *fakeGenerationRuntime) Stage(_ context.Context, table string, _ []string) error {
	r.events = append(r.events, "stage:"+table)
	return r.stageErr
}

func (r *fakeGenerationRuntime) Switch(_ context.Context, oldTable, newTable string) error {
	r.events = append(r.events, "switch:"+oldTable+":"+newTable)
	return r.switchErr
}

func (r *fakeGenerationRuntime) ReadCounters(_ context.Context, table string) (map[flowKey]counters, error) {
	r.events = append(r.events, "read:"+table)
	if len(r.reads) == 0 {
		return nil, errors.New("unexpected counter read")
	}
	next := r.reads[0]
	r.reads = r.reads[1:]
	return cloneCounters(next.value), next.err
}

func (r *fakeGenerationRuntime) Delete(_ context.Context, table string) error {
	r.events = append(r.events, "delete:"+table)
	return nil
}

func TestStageFailureLeavesOldGenerationActive(t *testing.T) {
	t.Parallel()

	runtime := &fakeGenerationRuntime{stageErr: errors.New("injected stage failure")}
	err := stageAndSwitchGeneration(context.Background(), runtime, "flux_panel", testGenerationA, []string{testCanonicalRule})
	if err == nil || !strings.Contains(err.Error(), "stage") {
		t.Fatalf("stageAndSwitchGeneration error=%v", err)
	}
	if want := []string{"probe", "stage:" + testGenerationA}; !reflect.DeepEqual(runtime.events, want) {
		t.Fatalf("events=%v, want %v (old generation must not be switched or deleted)", runtime.events, want)
	}
}

func TestCapabilityFailurePerformsNoFluxMutation(t *testing.T) {
	t.Parallel()

	runtime := &fakeGenerationRuntime{probeErr: errors.New("dormant tables unsupported")}
	err := stageAndSwitchGeneration(context.Background(), runtime, "flux_panel", testGenerationA, []string{testCanonicalRule})
	if err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("stageAndSwitchGeneration error=%v", err)
	}
	if want := []string{"probe"}; !reflect.DeepEqual(runtime.events, want) {
		t.Fatalf("events=%v, want %v", runtime.events, want)
	}
}

func TestStableSnapshotWaitsForInflightCounterUpdate(t *testing.T) {
	t.Parallel()

	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	runtime := &fakeGenerationRuntime{reads: []fakeCounterRead{
		{value: map[flowKey]counters{key: {Up: 10}}},
		{value: map[flowKey]counters{key: {Up: 11}}},
		{value: map[flowKey]counters{key: {Up: 11}}},
	}}
	got, err := waitStableSnapshot(context.Background(), runtime, testGenerationA)
	if err != nil {
		t.Fatalf("waitStableSnapshot: %v", err)
	}
	if got[key].Up != 11 || len(runtime.events) != 3 {
		t.Fatalf("snapshot=%v events=%v, want third stable read at 11", got, runtime.events)
	}
}

func TestStableSnapshotRetriesReadErrorsAndReportsLastErrorOnTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	runtime := &fakeGenerationRuntime{reads: []fakeCounterRead{
		{err: errors.New("first read failed")},
		{err: errors.New("last read failed")},
	}}
	_, err := waitStableSnapshot(ctx, runtime, testGenerationA)
	if err == nil || !strings.Contains(err.Error(), "last read failed") {
		t.Fatalf("waitStableSnapshot error=%v, want last read diagnostic", err)
	}
}

func TestStableSnapshotDoesNotBridgeAcrossMalformedCounterRead(t *testing.T) {
	t.Parallel()

	key := flowKey{ForwardID: 1, UserID: 2, UserTunnelID: 3}
	runtime := &fakeGenerationRuntime{reads: []fakeCounterRead{
		{value: map[flowKey]counters{key: {Up: 10}}},
		{err: errors.New("malformed directional counter")},
		{value: map[flowKey]counters{key: {Up: 10}}},
		{value: map[flowKey]counters{key: {Up: 10}}},
	}}
	got, err := waitStableSnapshot(context.Background(), runtime, testGenerationA)
	if err != nil {
		t.Fatalf("waitStableSnapshot: %v", err)
	}
	if got[key].Up != 10 || len(runtime.events) != 4 {
		t.Fatalf("snapshot=%v events=%v, want two reads after malformed sample", got, runtime.events)
	}
}
