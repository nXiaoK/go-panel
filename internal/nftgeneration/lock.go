package nftgeneration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// ErrLocked is returned when another process owns the generation handoff
// lock. Callers may use errors.Is to classify the operation as retryable.
var ErrLocked = errors.New("Flux nft generation handoff is already locked")

// AcquireLock obtains an exclusive, nonblocking advisory lock. Its returned
// release function is safe to call repeatedly or concurrently.
func AcquireLock(path string) (func() error, error) {
	if path == "" {
		return nil, fmt.Errorf("acquire Flux nft generation lock: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create Flux nft lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Flux nft generation lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("%w: %s", ErrLocked, path), closeErr)
		}
		return nil, errors.Join(fmt.Errorf("lock Flux nft generation state: %w", err), closeErr)
	}

	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			releaseErr = errors.Join(
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN),
				f.Close(),
			)
		})
		return releaseErr
	}, nil
}
