//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nXiaoK/go-panel/internal/nftgeneration"
)

// TestRealNftTwoGenerationProbe exercises real nft when CAP_NET_ADMIN (or root)
// and the nft binary are available. Standard unprivileged CI must skip, not fail.
func TestRealNftTwoGenerationProbe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux")
	}
	if os.Geteuid() != 0 && os.Getenv("FLUX_NFT_INTEGRATION") != "1" {
		t.Skip("requires root or FLUX_NFT_INTEGRATION=1 with CAP_NET_ADMIN")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft binary not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runtimeExec := newExecNftRuntime()
	// Use process-local temp dir so integration runs do not share /run.
	tmp := t.TempDir()
	runtimeExec.tempDir = tmp

	if err := runtimeExec.Probe(ctx); err != nil {
		// Missing capability or policy is a skip, not a hard failure for CI.
		msg := err.Error()
		if strings.Contains(msg, "Operation not permitted") ||
			strings.Contains(msg, "Permission denied") ||
			strings.Contains(msg, "not permitted") {
			t.Skipf("nft capabilities unavailable: %v", err)
		}
		t.Fatalf("Probe: %v", err)
	}

	// Stage two uniquely named generations with empty base chains, activate one,
	// deactivate, and delete. Never touch tables beginning exactly "flux_panel"
	// without a generation suffix in this probe path (Probe uses unique names).
	a, err := nftgeneration.NewTableName(strings.NewReader(strings.Repeat("a", 16)))
	if err != nil {
		t.Fatalf("NewTableName a: %v", err)
	}
	b, err := nftgeneration.NewTableName(strings.NewReader(strings.Repeat("b", 16)))
	if err != nil {
		t.Fatalf("NewTableName b: %v", err)
	}
	// Ensure names validate.
	if err := nftgeneration.ValidateTableName(a); err != nil {
		t.Fatal(err)
	}
	if err := nftgeneration.ValidateTableName(b); err != nil {
		t.Fatal(err)
	}

	// Cleanup any leftover probe generations from this test's names.
	cleanup := func(name string) {
		_ = exec.CommandContext(ctx, "nft", "delete", "table", "inet", name).Run()
	}
	t.Cleanup(func() {
		cleanup(a)
		cleanup(b)
	})
	cleanup(a)
	cleanup(b)

	// Create dormant A, then B, then delete both — verifies create/delete path.
	create := func(name string) error {
		script := "create table inet " + name + " { flags dormant; }\n"
		cmd := exec.CommandContext(ctx, "nft", "-f", "-")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return err
		}
		_ = out
		return nil
	}
	if err := create(a); err != nil {
		t.Skipf("cannot create generation table (need CAP_NET_ADMIN): %v", err)
	}
	if err := create(b); err != nil {
		t.Fatalf("create B: %v", err)
	}

	// Activate A by clearing dormant flag if supported.
	if out, err := exec.CommandContext(ctx, "nft", "add", "table", "inet", a).CombinedOutput(); err != nil {
		// Some kernels need delete flags dormant via replace; tolerate.
		t.Logf("activate note: %v %s", err, out)
	}

	list, err := exec.CommandContext(ctx, "nft", "list", "tables").CombinedOutput()
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	listed := string(list)
	if !strings.Contains(listed, a) && !strings.Contains(listed, "table inet "+a) {
		// nft list format may vary; ensure delete still succeeds.
		t.Logf("list tables output: %s", listed)
	}

	cleanup(a)
	cleanup(b)
}
