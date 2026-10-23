//go:build unix

package child

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
)

func TestNotifierWritesReadyMessageOnceAndCloses(t *testing.T) {
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(fds[0]), "ready-reader")
	defer reader.Close()

	notifier := claimFD(fds[1])
	if !notifier.Enabled() {
		t.Fatal("a pipe descriptor must enable the notifier")
	}
	addr := &net.TCPAddr{IP: net.IPv6unspecified, Port: 6365}
	if err := notifier.Notify(addr); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Notify(addr); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), "READY=1 ADDR=[::]:6365\n"; got != want {
		t.Fatalf("ready message=%q want %q (must be written exactly once)", got, want)
	}
	if notifier.Enabled() {
		t.Fatal("notifier must be disabled after notifying")
	}
}

func TestClaimReadyPipeIgnoresNonPipeDescriptors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)

	if claimFD(fd).Enabled() {
		t.Fatal("regular files must never be used as the ready pipe")
	}
	// 未接管的描述符必须保持打开，且不得被写入任何内容。
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		t.Fatalf("descriptor was closed: %v", err)
	}
	if stat.Size != 0 {
		t.Fatalf("regular file was written: size=%d", stat.Size)
	}
}

func TestClaimReadyPipeClearsEnvironmentAndRejectsStdio(t *testing.T) {
	t.Setenv(protocol.EnvReadyFD, "1")
	if ClaimReadyPipe().Enabled() {
		t.Fatal("stdout must never be claimed as the ready pipe")
	}
	if _, ok := os.LookupEnv(protocol.EnvReadyFD); ok {
		t.Fatal("the ready descriptor variable must be cleared after claiming")
	}
}
