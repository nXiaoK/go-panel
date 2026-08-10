package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeCommandExecutor struct {
	diagnostic func(context.Context, commandMessage) commandResponse
	mutation   func(context.Context, commandMessage) commandResponse
}

func (f fakeCommandExecutor) Execute(ctx context.Context, cmd commandMessage) commandResponse {
	if isDiagnosticCommand(cmd.Type) {
		if f.diagnostic != nil {
			return f.diagnostic(ctx, cmd)
		}
		return commandResponse{Type: cmd.Type + "Response", Success: true, RequestID: cmd.RequestID}
	}
	if f.mutation != nil {
		return f.mutation(ctx, cmd)
	}
	return commandResponse{Type: cmd.Type + "Response", Success: true, RequestID: cmd.RequestID}
}

type responseRecorder struct {
	mu        sync.Mutex
	responses []commandResponse
	notify    chan commandResponse
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{notify: make(chan commandResponse, 64)}
}

func (r *responseRecorder) write(resp commandResponse) error {
	r.mu.Lock()
	r.responses = append(r.responses, resp)
	r.mu.Unlock()
	r.notify <- resp
	return nil
}

func (r *responseRecorder) wait(t *testing.T, want int) []commandResponse {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		r.mu.Lock()
		count := len(r.responses)
		got := append([]commandResponse(nil), r.responses...)
		r.mu.Unlock()
		if count >= want {
			return got
		}
		select {
		case <-r.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %d responses, got %d", want, count)
		}
	}
}

func TestDiagnosticCommandDoesNotBlockReader(t *testing.T) {
	block := make(chan struct{})
	executor := fakeCommandExecutor{diagnostic: func(context.Context, commandMessage) commandResponse {
		<-block
		return commandResponse{Success: true}
	}}
	recorder := newResponseRecorder()
	d := newCommandDispatcher(context.Background(), executor, recorder.write)
	defer d.Close()
	if err := d.Dispatch(commandMessage{Type: "Iperf3Client"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Dispatch(commandMessage{Type: "call"}); err != nil {
		t.Fatal("reader/control path blocked by diagnostic")
	}
	// A mutation must run to completion while the diagnostic is still blocked.
	if err := d.Dispatch(commandMessage{Type: "AddNftRule", RequestID: "m1"}); err != nil {
		t.Fatal(err)
	}
	responses := recorder.wait(t, 1)
	if responses[0].RequestID != "m1" {
		t.Fatalf("responses=%+v", responses)
	}
	close(block)
}

func TestMutationsCompleteInDispatchOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	executor := fakeCommandExecutor{mutation: func(_ context.Context, cmd commandMessage) commandResponse {
		mu.Lock()
		order = append(order, cmd.RequestID)
		mu.Unlock()
		return commandResponse{Success: true, RequestID: cmd.RequestID}
	}}
	recorder := newResponseRecorder()
	d := newCommandDispatcher(context.Background(), executor, recorder.write)
	defer d.Close()
	for i := 0; i < 8; i++ {
		if err := d.Dispatch(commandMessage{Type: "AddNftRule", RequestID: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	recorder.wait(t, 8)
	mu.Lock()
	defer mu.Unlock()
	for i := 0; i < 8; i++ {
		if order[i] != fmt.Sprintf("r%d", i) {
			t.Fatalf("order=%v", order)
		}
	}
}

func TestSeventeenthQueuedMutationReceivesBusy(t *testing.T) {
	entered := make(chan struct{}, 1)
	block := make(chan struct{})
	executor := fakeCommandExecutor{mutation: func(_ context.Context, cmd commandMessage) commandResponse {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
		return commandResponse{Success: true, RequestID: cmd.RequestID}
	}}
	recorder := newResponseRecorder()
	d := newCommandDispatcher(context.Background(), executor, recorder.write)
	// Unblock the worker before Close waits on it (defers run LIFO).
	defer d.Close()
	defer close(block)

	// One active…
	if err := d.Dispatch(commandMessage{Type: "AddNftRule", RequestID: "active"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not pick up the first mutation")
	}
	// …plus sixteen queued fill the lane.
	for i := 0; i < 16; i++ {
		if err := d.Dispatch(commandMessage{Type: "AddNftRule", RequestID: fmt.Sprintf("q%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// The seventeenth queued mutation receives a structured busy response.
	if err := d.Dispatch(commandMessage{Type: "AddNftRule", RequestID: "overflow"}); err != nil {
		t.Fatal(err)
	}
	responses := recorder.wait(t, 1)
	busy := responses[0]
	if busy.Success || busy.RequestID != "overflow" || busy.Type != "AddNftRuleResponse" {
		t.Fatalf("busy=%+v", busy)
	}
}

func TestCloseStopsWorkersAndRejectsDispatch(t *testing.T) {
	recorder := newResponseRecorder()
	d := newCommandDispatcher(context.Background(), fakeCommandExecutor{}, recorder.write)
	d.Close()
	if err := d.Dispatch(commandMessage{Type: "AddNftRule"}); err == nil {
		t.Fatal("dispatch after close should fail")
	}
}
