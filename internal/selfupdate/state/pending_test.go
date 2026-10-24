package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func validUpgrade() Pending {
	return Pending{
		RequestID:  "req-1",
		Action:     ActionUpgrade,
		Target:     releaseTarget("v0.3.0"),
		BackupFile: "backups/pre-update-v0.3.0-20261101-101010.000.db",
	}
}

func TestPendingValidate(t *testing.T) {
	valid := []Pending{
		validUpgrade(),
		{RequestID: "r", Action: ActionRollback, Target: releaseTarget("v0.2.5")},
		{RequestID: "r", Action: ActionRollback, Target: Target{Kind: KindBuiltin}},
		{RequestID: "r", Action: ActionUseBuiltin, Target: Target{Kind: KindBuiltin}},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Fatalf("valid pending %+v rejected: %v", p, err)
		}
	}
	mutate := func(fn func(*Pending)) Pending {
		p := validUpgrade()
		fn(&p)
		return p
	}
	invalid := []Pending{
		mutate(func(p *Pending) { p.RequestID = "" }),
		mutate(func(p *Pending) { p.Action = "downgrade" }),
		mutate(func(p *Pending) { p.BackupFile = "" }),
		mutate(func(p *Pending) { p.Target = Target{Kind: KindBuiltin} }),
		mutate(func(p *Pending) { p.Target.Version = "v0.3.0-rc.1" }),
		mutate(func(p *Pending) { p.Target.Dir = "../etc" }),
		mutate(func(p *Pending) { p.Target.Kind = "image" }),
		mutate(func(p *Pending) { p.ReadyTimeoutSec = -1 }),
		{RequestID: "r", Action: ActionUseBuiltin, Target: releaseTarget("v0.3.0")},
	}
	for _, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Fatalf("invalid pending %+v accepted", p)
		}
	}
}

func TestWriteLoadRemovePending(t *testing.T) {
	dir := t.TempDir()
	path, lock := filepath.Join(dir, "pending.json"), filepath.Join(dir, "pending.lock")
	if err := WritePending(path, lock, validUpgrade()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPending(path)
	if err != nil || loaded.Schema != Schema || loaded.Target.Version != "v0.3.0" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if err := WritePending(path, lock, Pending{RequestID: "x", Action: "bogus"}); err == nil {
		t.Fatal("WritePending must validate before writing")
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"requestId":"x","action":"bogus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPending(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("invalid pending on disk err=%v", err)
	}
	if err := RemovePending(path); err != nil {
		t.Fatal(err)
	}
	if err := RemovePending(path); err != nil {
		t.Fatalf("removing a missing pending file must succeed: %v", err)
	}
}

func TestRestoreJournalRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db-restore.json")
	journal := RestoreJournal{RequestID: "req-1", DBPath: "/app/data/flux-panel.db", BackupFile: "/app/data/backups/pre.db", FailedDir: "/app/data/backups/failed-update-v0.3.0-1", Phase: RestorePhaseMoving}
	if err := SaveRestoreJournal(path, journal); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRestoreJournal(path)
	if err != nil || loaded.Phase != RestorePhaseMoving || loaded.Schema != Schema {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	journal.Phase = "exploding"
	if err := SaveRestoreJournal(path, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRestoreJournal(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("unknown phase err=%v", err)
	}
	if err := RemoveRestoreJournal(path); err != nil {
		t.Fatal(err)
	}
}
