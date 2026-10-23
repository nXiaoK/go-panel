package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

func newTestApp(record func(string)) *App {
	return &App{
		ShutdownHTTP:  func(context.Context) error { record("http"); return nil },
		StopScheduler: func() { record("scheduler") },
		CloseSessions: func(string) { record("websocket") },
		WaitWorkers:   func(context.Context) error { record("workers"); return nil },
		CloseDatabase: func() error { record("database"); return nil },
	}
}

func TestAppShutdownClosesComponentsInOrder(t *testing.T) {
	var order []string
	app := newTestApp(func(name string) { order = append(order, name) })
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"http", "scheduler", "websocket", "workers", "database"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestAppShutdownContinuesPastErrors(t *testing.T) {
	var order []string
	app := newTestApp(func(name string) { order = append(order, name) })
	httpErr := errors.New("http shutdown failed")
	app.ShutdownHTTP = func(context.Context) error {
		order = append(order, "http")
		return httpErr
	}
	err := app.Shutdown(context.Background())
	if !errors.Is(err, httpErr) {
		t.Fatalf("err=%v, want joined http error", err)
	}
	if order[len(order)-1] != "database" {
		t.Fatalf("order=%v, later cleanup skipped after error", order)
	}
}

func TestAppShutdownIsIdempotent(t *testing.T) {
	calls := 0
	app := newTestApp(func(string) { calls++ })
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 5 {
		t.Fatalf("calls=%d, want each component closed exactly once", calls)
	}
}

func TestAppShutdownBoundsDrainTime(t *testing.T) {
	app := newTestApp(func(string) {})
	app.WaitWorkers = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("no drain deadline")
		}
		if remaining := time.Until(deadline); remaining > 20*time.Second {
			return errors.New("drain deadline exceeds 20s")
		}
		return nil
	}
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newServingTestApp(addr string, record func(string)) *App {
	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	app := newTestApp(record)
	app.Server = server
	app.ShutdownHTTP = func(ctx context.Context) error {
		record("http")
		return server.Shutdown(ctx)
	}
	return app
}

func TestAppRunNotifiesListeningAddressBeforeServing(t *testing.T) {
	listening := make(chan net.Addr, 1)
	app := newServingTestApp("127.0.0.1:0", func(string) {})
	app.OnListening = func(addr net.Addr) { listening <- addr }

	runErr := make(chan error, 1)
	go func() { runErr <- app.Run() }()

	var addr net.Addr
	select {
	case addr = <-listening:
	case err := <-runErr:
		t.Fatalf("Run returned before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("OnListening was not called")
	}
	resp, err := http.Get("http://" + addr.String() + "/")
	if err != nil {
		t.Fatalf("listener reported by OnListening is not serving: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run after shutdown returned %v", err)
	}
}

func TestAppRunWaitsForShutdownToFinish(t *testing.T) {
	listening := make(chan struct{})
	releaseDatabase := make(chan struct{})
	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}
	app := newServingTestApp("127.0.0.1:0", record)
	app.OnListening = func(net.Addr) { close(listening) }
	app.CloseDatabase = func() error {
		<-releaseDatabase
		record("database")
		return nil
	}

	runErr := make(chan error, 1)
	go func() {
		err := app.Run()
		record("run-returned")
		runErr <- err
	}()
	<-listening
	go func() { _ = app.Shutdown(context.Background()) }()

	select {
	case err := <-runErr:
		t.Fatalf("Run returned before the database closed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseDatabase)
	if err := <-runErr; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[len(order)-2] != "database" || order[len(order)-1] != "run-returned" {
		t.Fatalf("order=%v, Run must return only after the database closed", order)
	}
}

func TestAppRunReturnsListenErrorWithoutNotifying(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	app := newServingTestApp(occupied.Addr().String(), func(string) {})
	notified := false
	app.OnListening = func(net.Addr) { notified = true }
	if err := app.Run(); err == nil {
		t.Fatal("Run must fail when the address is in use")
	}
	if notified {
		t.Fatal("OnListening must not run when listening failed")
	}
}
