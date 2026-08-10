package dtls

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestConnectedPacketConnPreservesDatagrams(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	deadline := time.Now().Add(time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}

	packetConn := connectedPacketConn{Conn: client}
	wantInbound := []byte("server-to-client")
	writeDone := make(chan error, 1)
	go func() {
		_, err := server.Write(wantInbound)
		writeDone <- err
	}()

	buffer := make([]byte, 64)
	n, addr, err := packetConn.ReadFrom(buffer)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("peer Write() error = %v", err)
	}
	if !bytes.Equal(buffer[:n], wantInbound) {
		t.Fatalf("ReadFrom() payload = %q, want %q", buffer[:n], wantInbound)
	}
	if addr.String() != client.RemoteAddr().String() {
		t.Fatalf("ReadFrom() addr = %v, want %v", addr, client.RemoteAddr())
	}

	wantOutbound := []byte("client-to-server")
	readDone := make(chan error, 1)
	go func() {
		n, err := server.Read(buffer)
		if err == nil && !bytes.Equal(buffer[:n], wantOutbound) {
			err = &payloadMismatchError{got: string(buffer[:n]), want: string(wantOutbound)}
		}
		readDone <- err
	}()

	if _, err := packetConn.WriteTo(wantOutbound, server.LocalAddr()); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("peer Read() error = %v", err)
	}
}

type payloadMismatchError struct {
	got  string
	want string
}

func (e *payloadMismatchError) Error() string {
	return "payload mismatch: got " + e.got + ", want " + e.want
}
