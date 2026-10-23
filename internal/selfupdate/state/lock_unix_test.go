//go:build unix

package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTryLockExcludesSecondOpenInSameProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor.lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock err=%v, want ErrLocked", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("lock must be reusable after unlock: %v", err)
	}
	_ = again()
}

func TestTryLockExcludesOtherProcesses(t *testing.T) {
	if os.Getenv("GO_PANEL_STATE_LOCK_HELPER") != "" {
		unlock, err := TryLock(os.Getenv("GO_PANEL_STATE_LOCK_HELPER"))
		if err != nil {
			os.Exit(3)
		}
		defer func() { _ = unlock() }()
		os.Stdout.WriteString("locked\n")
		time.Sleep(10 * time.Second)
		return
	}
	path := filepath.Join(t.TempDir(), "supervisor.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTryLockExcludesOtherProcesses$")
	cmd.Env = append(os.Environ(), "GO_PANEL_STATE_LOCK_HELPER="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	buf := make([]byte, 16)
	n, _ := stdout.Read(buf)
	if !strings.Contains(string(buf[:n]), "locked") {
		t.Fatalf("helper did not acquire the lock: %q", buf[:n])
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock held by another process err=%v, want ErrLocked", err)
	}
}
