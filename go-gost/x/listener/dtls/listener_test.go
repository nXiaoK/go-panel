package dtls

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestHandshakeTimeoutListenerCompletesHandshake(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	wrapped := &handshakeConn{Conn: conn}
	listener := &handshakeTimeoutListener{
		Listener: &singleConnListener{conn: wrapped},
		timeout:  time.Second,
	}

	got, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got != wrapped {
		t.Fatalf("Accept() conn = %T, want original handshake connection", got)
	}
	if !wrapped.handshakeCalled {
		t.Fatal("Accept() did not run HandshakeContext")
	}
	if !wrapped.handshakeHadDeadline {
		t.Fatal("HandshakeContext() did not receive a deadline")
	}
}

func TestHandshakeTimeoutListenerClosesFailedConnection(t *testing.T) {
	wantErr := errors.New("handshake failed")
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	wrapped := &handshakeConn{Conn: conn, handshakeErr: wantErr}
	listener := &handshakeTimeoutListener{
		Listener: &singleConnListener{conn: wrapped},
		timeout:  time.Second,
	}

	got, err := listener.Accept()
	if got != nil {
		t.Fatalf("Accept() conn = %v, want nil", got)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Accept() error = %v, want %v", err, wantErr)
	}
	if !wrapped.closed {
		t.Fatal("Accept() did not close a connection after handshake failure")
	}
}

type handshakeConn struct {
	net.Conn
	handshakeErr         error
	handshakeCalled      bool
	handshakeHadDeadline bool
	closed               bool
}

func (c *handshakeConn) HandshakeContext(ctx context.Context) error {
	c.handshakeCalled = true
	_, c.handshakeHadDeadline = ctx.Deadline()
	return c.handshakeErr
}

func (c *handshakeConn) Close() error {
	c.closed = true
	return c.Conn.Close()
}

type singleConnListener struct {
	conn net.Conn
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	return l.conn, nil
}

func (l *singleConnListener) Close() error {
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
