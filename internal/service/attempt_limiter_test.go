package service

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func recordLimiterFailure(t *testing.T, l *AttemptLimiter, ip, username string) {
	t.Helper()
	if !l.Allow(ip, username) {
		t.Fatalf("attempt denied while preparing failure for %s/%s", ip, username)
	}
	l.Failure(ip, username)
}

func TestAttemptLimiterPairWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })

	for i := 0; i < 5; i++ {
		recordLimiterFailure(t, l, "192.0.2.1", " Alice ")
	}
	if l.Allow("192.0.2.1", "alice") {
		t.Fatal("sixth attempt for normalized username must be denied")
	}

	now = now.Add(15 * time.Minute)
	if l.Allow("192.0.2.1", "ALICE") {
		t.Fatal("failure exactly fifteen minutes old must remain in the window")
	}
	now = now.Add(time.Nanosecond)
	if !l.Allow("192.0.2.1", "alice") {
		t.Fatal("expired pair window must reopen")
	}
}

func TestAttemptLimiterIPWindow(t *testing.T) {
	now := time.Unix(2000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })

	for i := 0; i < 50; i++ {
		username := fmt.Sprintf("user-%d", i)
		if !l.Allow("198.51.100.7", username) {
			t.Fatalf("attempt %d denied before fifty IP failures", i+1)
		}
		l.Failure("198.51.100.7", username)
	}
	if l.Allow("198.51.100.7", "new-user") {
		t.Fatal("attempt after fifty IP failures must be denied")
	}
}

func TestAttemptLimiterSuccessClearsPairButNotIP(t *testing.T) {
	now := time.Unix(3000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })

	for i := 0; i < 4; i++ {
		recordLimiterFailure(t, l, "203.0.113.8", "alice")
	}
	if !l.Allow("203.0.113.8", "alice") {
		t.Fatal("fifth attempt must be admitted for successful credentials")
	}
	l.Success("203.0.113.8", " ALICE ")
	if !l.Allow("203.0.113.8", "alice") {
		t.Fatal("success must clear the normalized pair bucket")
	}
	l.Cancel("203.0.113.8", "alice")

	for i := 0; i < 46; i++ {
		recordLimiterFailure(t, l, "203.0.113.8", fmt.Sprintf("other-%d", i))
	}
	if l.Allow("203.0.113.8", "fresh-user") {
		t.Fatal("success must not clear the IP bucket")
	}
}

func TestAttemptLimiterPrunesExpiredEntries(t *testing.T) {
	now := time.Unix(4000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })
	recordLimiterFailure(t, l, "192.0.2.9", "alice")

	now = now.Add(15*time.Minute + time.Nanosecond)
	if !l.Allow("192.0.2.9", "alice") {
		t.Fatal("expired entries must not deny an attempt")
	}
	if len(l.pairs) != 0 || len(l.ips) != 0 {
		t.Fatalf("expired entries were not pruned: pairs=%d ips=%d", len(l.pairs), len(l.ips))
	}
}

func TestAttemptLimiterReservesPairCapacityAtomically(t *testing.T) {
	now := time.Unix(5000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })
	for i := 0; i < pairAttemptLimit-1; i++ {
		recordLimiterFailure(t, l, "192.0.2.20", "alice")
	}

	const contenders = 16
	start := make(chan struct{})
	allowed := make(chan bool, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			allowed <- l.Allow("192.0.2.20", " Alice ")
		}()
	}
	close(start)
	wg.Wait()
	close(allowed)

	count := 0
	for permit := range allowed {
		if permit {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent permits=%d, want exactly 1 at pair capacity", count)
	}
}

func TestAttemptLimiterReservesIPCapacityAtomically(t *testing.T) {
	now := time.Unix(6000, 0)
	l := NewAttemptLimiter(func() time.Time { return now })
	for i := 0; i < ipAttemptLimit-1; i++ {
		recordLimiterFailure(t, l, "198.51.100.20", fmt.Sprintf("seed-%d", i))
	}

	const contenders = 16
	start := make(chan struct{})
	allowed := make(chan bool, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			allowed <- l.Allow("198.51.100.20", fmt.Sprintf("contender-%d", i))
		}(i)
	}
	close(start)
	wg.Wait()
	close(allowed)

	count := 0
	for permit := range allowed {
		if permit {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent permits=%d, want exactly 1 at IP capacity", count)
	}
}
