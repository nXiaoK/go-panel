package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

const runtimeJWTSecretBytes = 32

// IsLoopbackListenAddr reports whether raw binds exclusively to a loopback host.
func IsLoopbackListenAddr(raw string) bool {
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PrepareRuntimeSecurity creates an in-memory JWT secret for loopback-only
// development when no secret was configured. The generated value is neither
// persisted nor logged, so restarting the process invalidates prior tokens.
func (c *Config) PrepareRuntimeSecurity(random io.Reader) error {
	if c.JwtSecret != "" || !IsLoopbackListenAddr(c.ListenAddr) {
		return nil
	}
	raw := make([]byte, runtimeJWTSecretBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return fmt.Errorf("generate loopback JWT secret: %w", err)
	}
	c.JwtSecret = base64.RawURLEncoding.EncodeToString(raw)
	return nil
}

// ValidateStaticSecurity rejects weak JWT secrets on remotely reachable listeners.
func (c Config) ValidateStaticSecurity() error {
	if IsLoopbackListenAddr(c.ListenAddr) {
		return nil
	}
	if c.JwtSecret == DefaultJwtSecret || len([]byte(c.JwtSecret)) < 32 {
		return errors.New("remote LISTEN_ADDR requires JWT_SECRET with at least 32 bytes")
	}
	return nil
}
