package ws

import (
	"testing"
	"time"
)

func TestSessionCloseCancelsDoneOnce(t *testing.T) {
	s, _ := newTestSession(t, 1)
	s.Close()
	s.Close()
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("session context not cancelled")
	}
}

func TestClosingNodeResolvesPendingImmediately(t *testing.T) {
	hub := NewHub()
	session, _ := newTestSession(t, 7)
	hub.AddNode(7, session)

	start := time.Now()
	resultCh := make(chan GostResult, 1)
	go func() {
		resultCh <- hub.SendMsgWithTimeout(7, map[string]any{"x": 1}, "Test", time.Minute)
	}()
	waitForPendingCount(t, hub, 1)
	session.Close()
	hub.failPendingForNode(7, "节点连接已关闭")
	got := <-resultCh
	if got.Msg != "节点连接已关闭" || time.Since(start) > time.Second {
		t.Fatalf("got=%+v elapsed=%v", got, time.Since(start))
	}
}

func TestPingLoopStopsAfterClose(t *testing.T) {
	s, _ := newTestSession(t, 3)
	ticker := time.NewTicker(5 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		runPingLoop(s, ticker)
		close(done)
	}()
	s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ping loop kept running after session close")
	}
}
