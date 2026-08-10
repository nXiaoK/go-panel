package nftgeneration

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestAcquireLockRejectsContentionWithStableSentinel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "handoff.lock")
	unlock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = unlock() })

	if otherUnlock, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		if otherUnlock != nil {
			_ = otherUnlock()
		}
		t.Fatalf("contended AcquireLock error=%v, want ErrLocked", err)
	} else if otherUnlock != nil {
		t.Fatal("contended AcquireLock returned a non-nil unlock")
	}
}

func TestUnlockIsIdempotentAndReleasesLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "handoff.lock")
	unlock, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("first unlock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("second unlock: %v", err)
	}

	nextUnlock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	if err := nextUnlock(); err != nil {
		t.Fatalf("next unlock: %v", err)
	}
}

func TestUnlockIsSafeForConcurrentRepeatedCalls(t *testing.T) {
	t.Parallel()

	unlock, err := AcquireLock(filepath.Join(t.TempDir(), "handoff.lock"))
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- unlock()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent unlock: %v", err)
		}
	}
}
