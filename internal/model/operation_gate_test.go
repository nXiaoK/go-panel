package model

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestOperationGateWaitsForActiveAndRejectsNew(t *testing.T) {
	gate := NewOperationGate()
	leave, ok := gate.Enter()
	if !ok {
		t.Fatal("initial enter rejected")
	}
	acquired := make(chan struct{})
	go func() {
		end, err := gate.BeginMaintenance(context.Background())
		if err != nil {
			return
		}
		close(acquired)
		end()
	}()
	// Maintenance is pending on the active operation; new work must be rejected.
	deadline := time.After(time.Second)
	for {
		probeLeave, ok := gate.Enter()
		if !ok {
			break
		}
		probeLeave()
		select {
		case <-deadline:
			t.Fatal("new operation kept entering during pending maintenance")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-acquired:
		t.Fatal("maintenance acquired before active operation drained")
	default:
	}
	leave()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not acquire after drain")
	}
	// After maintenance ends, new work resumes.
	resume, ok := gate.Enter()
	if !ok {
		t.Fatal("enter rejected after maintenance ended")
	}
	resume()
}

func TestOperationGateLeaveIsIdempotent(t *testing.T) {
	gate := NewOperationGate()
	leave, ok := gate.Enter()
	if !ok {
		t.Fatal("enter rejected")
	}
	leave()
	leave() // must not underflow the active counter
	end, err := gate.BeginMaintenance(context.Background())
	if err != nil {
		t.Fatalf("begin maintenance: %v", err)
	}
	end()
}

func TestBeginMaintenanceHonorsContextCancellation(t *testing.T) {
	gate := NewOperationGate()
	leave, ok := gate.Enter()
	if !ok {
		t.Fatal("enter rejected")
	}
	defer leave()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.BeginMaintenance(ctx); err == nil {
		t.Fatal("maintenance should fail when it cannot drain before cancellation")
	}
	// A cancelled maintenance must not leave the gate closed to new work.
	resume, ok := gate.Enter()
	if !ok {
		t.Fatal("gate stayed closed after cancelled maintenance")
	}
	resume()
}

func TestBeginMaintenanceSerializesConcurrentCallers(t *testing.T) {
	gate := NewOperationGate()
	end1, err := gate.BeginMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan struct{})
	go func() {
		end2, err := gate.BeginMaintenance(context.Background())
		if err == nil {
			end2()
		}
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("second maintenance acquired while first held")
	case <-time.After(50 * time.Millisecond):
	}
	end1()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("second maintenance did not acquire after first ended")
	}
}

var _ = sync.Mutex{}
