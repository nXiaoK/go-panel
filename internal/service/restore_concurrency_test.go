package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
)

// TestRestoreWaitsForActiveOperationThenSwaps proves restore drains in-flight
// database work before it closes and swaps the handle.
func TestRestoreWaitsForActiveOperationThenSwaps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	updateOrCreateConfig("app_name", "before-backup")
	backup, err := CreateSiteBackup()
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	defer backup.Cleanup()
	updateOrCreateConfig("app_name", "after-backup")

	// One operation is in flight; restore must not proceed until it leaves.
	leave, ok := model.Gate.Enter()
	if !ok {
		t.Fatal("gate rejected initial operation")
	}

	restoreDone := make(chan error, 1)
	go func() {
		_, err := RestoreSiteBackup(backup.Path)
		restoreDone <- err
	}()

	select {
	case <-restoreDone:
		t.Fatal("restore swapped the database before active work drained")
	case <-time.After(100 * time.Millisecond):
	}

	leave()
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatalf("restore after drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not complete after drain")
	}
	if got := GetConfigValue("app_name"); got != "before-backup" {
		t.Fatalf("config=%q, want restored before-backup", got)
	}
}

// TestRestoreClosesGateBeforeWaitingForBackupLock verifies the global lock
// order stays Gate -> siteBackupMu. R2 uploads can hold the gate while waiting
// for a snapshot, so restore must never hold the snapshot lock while draining.
func TestRestoreClosesGateBeforeWaitingForBackupLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	backup, err := CreateSiteBackup()
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	defer backup.Cleanup()

	// Hold the snapshot lock so restore has to wait after claiming maintenance.
	siteBackupMu.Lock()
	locked := true
	defer func() {
		if locked {
			siteBackupMu.Unlock()
		}
	}()
	restoreDone := make(chan error, 1)
	go func() {
		_, err := RestoreSiteBackup(backup.Path)
		restoreDone <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !model.Gate.IsMaintenance() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !model.Gate.IsMaintenance() {
		siteBackupMu.Unlock()
		locked = false
		select {
		case <-restoreDone:
		case <-time.After(5 * time.Second):
			t.Fatal("restore stayed blocked after releasing the backup lock")
		}
		t.Fatal("restore waited for the backup lock before closing the operation gate")
	}
	if leave, ok := model.Gate.Enter(); ok {
		leave()
		t.Fatal("new database work entered while restore waited for the backup lock")
	}

	siteBackupMu.Unlock()
	locked = false
	select {
	case err := <-restoreDone:
		if err != nil {
			t.Fatalf("restore after backup lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not finish after backup lock release")
	}
}

// TestGateRejectsNewWorkDuringMaintenance proves new database entrants receive
// a not-ok gate result (the HTTP layer maps this to a retryable 503) while a
// maintenance window is held, and recover once it ends.
func TestGateRejectsNewWorkDuringMaintenance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	end, err := model.Gate.BeginMaintenance(context.Background())
	if err != nil {
		t.Fatalf("begin maintenance: %v", err)
	}
	if _, ok := model.Gate.Enter(); ok {
		t.Fatal("gate admitted new work during maintenance")
	}
	end()
	leave, ok := model.Gate.Enter()
	if !ok {
		t.Fatal("gate stayed closed after maintenance ended")
	}
	leave()
}

// TestConcurrentEntrantsDoNotBlockRestoreForever stresses the drain path with
// many short operations racing against a restore.
func TestConcurrentEntrantsDoNotBlockRestoreForever(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panel.db")
	if err := model.Init(dbPath); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()

	backup, err := CreateSiteBackup()
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	defer backup.Cleanup()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if leave, ok := model.Gate.Enter(); ok {
					time.Sleep(time.Millisecond)
					leave()
				}
			}
		}()
	}

	if _, err := RestoreSiteBackup(backup.Path); err != nil {
		t.Fatalf("restore under concurrent entrants: %v", err)
	}
	close(stop)
	wg.Wait()
}
