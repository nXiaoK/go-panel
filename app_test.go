package main

import (
	"context"
	"errors"
	"reflect"
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
