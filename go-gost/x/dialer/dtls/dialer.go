package dtls

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/go-gost/core/dialer"
	"github.com/go-gost/core/logger"
	md "github.com/go-gost/core/metadata"
	xdtls "github.com/go-gost/x/internal/util/dtls"
	"github.com/go-gost/x/registry"
	"github.com/pion/dtls/v3"
)

func init() {
	registry.DialerRegistry().Register("dtls", NewDialer)
}

type dtlsDialer struct {
	md      metadata
	logger  logger.Logger
	options dialer.Options
}

func NewDialer(opts ...dialer.Option) dialer.Dialer {
	options := dialer.Options{}
	for _, opt := range opts {
		opt(&options)
	}

	return &dtlsDialer{
		logger:  options.Logger,
		options: options,
	}
}

func (d *dtlsDialer) Init(md md.Metadata) (err error) {
	return d.parseMetadata(md)
}

func (d *dtlsDialer) Dial(ctx context.Context, addr string, opts ...dialer.DialOption) (net.Conn, error) {
	var options dialer.DialOptions
	for _, opt := range opts {
		opt(&options)
	}

	conn, err := options.Dialer.Dial(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}

	tlsCfg := d.options.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	config := dtls.Config{
		Certificates:         tlsCfg.Certificates,
		InsecureSkipVerify:   tlsCfg.InsecureSkipVerify,
		ExtendedMasterSecret: dtls.RequireExtendedMasterSecret,
		ServerName:           tlsCfg.ServerName,
		RootCAs:              tlsCfg.RootCAs,
		FlightInterval:       d.md.flightInterval,
		MTU:                  d.md.mtu,
	}

	// pion/dtls v3 改用 PacketConn，并把可取消握手拆为显式 HandshakeContext。
	// 上游 Dialer 返回已连接的 UDP net.Conn；适配器让 WriteTo 继续走已连接的 Write，
	// 同时保留 datagram 边界和调用方的取消/超时语义。
	c, err := dtls.Client(connectedPacketConn{Conn: conn}, conn.RemoteAddr(), &config)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := c.HandshakeContext(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return xdtls.Conn(c, d.md.bufferSize), nil
}

type connectedPacketConn struct {
	net.Conn
}

func (c connectedPacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	n, err := c.Read(payload)
	return n, c.RemoteAddr(), err
}

func (c connectedPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	return c.Write(payload)
}
