package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestIsLoopbackListenAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{name: "IPv4", addr: "127.0.0.1:6365", want: true},
		{name: "IPv6", addr: "[::1]:6365", want: true},
		{name: "localhost", addr: "LOCALHOST:6365", want: true},
		{name: "all IPv4 interfaces", addr: ":6365", want: false},
		{name: "unspecified IPv4", addr: "0.0.0.0:6365", want: false},
		{name: "remote IP", addr: "192.0.2.10:6365", want: false},
		{name: "invalid", addr: "localhost", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLoopbackListenAddr(tt.addr); got != tt.want {
				t.Fatalf("IsLoopbackListenAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestValidateStaticSecurityRejectsRemoteDefaultSecret(t *testing.T) {
	c := Config{ListenAddr: ":6365", JwtSecret: DefaultJwtSecret}
	if err := c.ValidateStaticSecurity(); err == nil {
		t.Fatal("remote default JWT secret must be rejected")
	}
}

func TestValidateStaticSecurityRejectsRemoteShortSecret(t *testing.T) {
	c := Config{ListenAddr: ":6365", JwtSecret: strings.Repeat("a", 31)}
	if err := c.ValidateStaticSecurity(); err == nil {
		t.Fatal("remote JWT secret shorter than 32 bytes must be rejected")
	}
}

func TestValidateStaticSecurityAllowsRemoteStrongSecret(t *testing.T) {
	c := Config{ListenAddr: ":6365", JwtSecret: strings.Repeat("a", 32)}
	if err := c.ValidateStaticSecurity(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateStaticSecurityAllowsLoopbackGeneration(t *testing.T) {
	c := Config{ListenAddr: "127.0.0.1:6365"}
	if err := c.ValidateStaticSecurity(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRuntimeSecurityGeneratesLoopbackSecret(t *testing.T) {
	random := bytes.Repeat([]byte{0xa5}, 32)
	c := Config{ListenAddr: "127.0.0.1:6365"}

	if err := c.PrepareRuntimeSecurity(bytes.NewReader(random)); err != nil {
		t.Fatalf("prepare runtime security: %v", err)
	}
	if c.JwtSecret == "" || c.JwtSecret == DefaultJwtSecret {
		t.Fatalf("generated insecure JWT secret %q", c.JwtSecret)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(c.JwtSecret)
	if err != nil {
		t.Fatalf("generated secret is not raw URL base64: %v", err)
	}
	if !bytes.Equal(decoded, random) || len(decoded) < 32 {
		t.Fatalf("generated secret contains %d unexpected random bytes", len(decoded))
	}
}

func TestPrepareRuntimeSecurityKeepsConfiguredSecret(t *testing.T) {
	configured := strings.Repeat("configured-secret-", 3)
	c := Config{ListenAddr: "127.0.0.1:6365", JwtSecret: configured}

	if err := c.PrepareRuntimeSecurity(iotest.ErrReader(errors.New("random source must not be read"))); err != nil {
		t.Fatalf("configured secret preparation failed: %v", err)
	}
	if c.JwtSecret != configured {
		t.Fatalf("configured secret changed to %q", c.JwtSecret)
	}
}

func TestPrepareRuntimeSecurityDoesNotGenerateRemoteSecret(t *testing.T) {
	c := Config{ListenAddr: ":6365"}

	if err := c.PrepareRuntimeSecurity(iotest.ErrReader(errors.New("random source must not be read"))); err != nil {
		t.Fatalf("remote preparation unexpectedly used randomness: %v", err)
	}
	if c.JwtSecret != "" {
		t.Fatalf("remote empty secret was generated: %q", c.JwtSecret)
	}
	if err := c.ValidateStaticSecurity(); err == nil {
		t.Fatal("remote empty secret must still fail validation")
	}
}

func TestPrepareRuntimeSecurityPropagatesRandomFailure(t *testing.T) {
	wantErr := errors.New("random source failed")
	c := Config{ListenAddr: "[::1]:6365"}

	err := c.PrepareRuntimeSecurity(iotest.ErrReader(wantErr))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v, want wrapped random failure", err)
	}
	if c.JwtSecret != "" {
		t.Fatalf("random failure installed fallback secret %q", c.JwtSecret)
	}
}

func TestLoadSecurityConfiguration(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "[::1]:7000")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ADMIN_USERNAME", "root")
	t.Setenv("ADMIN_PASSWORD", "correct horse battery staple")
	t.Setenv("ALLOW_INSECURE_NODE_DOWNLOADS", "true")
	t.Setenv("ALLOW_LEGACY_NFT_REPORTS", "true")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.0.0/16,,")
	t.Setenv("UPDATE_CHECK_ENABLED", "false")
	t.Setenv("UPDATE_REPOSITORY", "example/panel")
	t.Setenv("UPDATE_CHECK_INTERVAL", "30m")
	t.Setenv("UPDATE_TRIGGER_URL", " http://updater:8080/v1/update ")
	t.Setenv("UPDATE_TRIGGER_TOKEN", " sidecar-secret ")
	t.Setenv("UPDATE_IMAGE_TAG", "v1.2.3")

	c := Load()
	if c.ListenAddr != "[::1]:7000" {
		t.Fatalf("ListenAddr = %q", c.ListenAddr)
	}
	if c.JwtSecret != "" {
		t.Fatalf("empty JWT_SECRET must remain empty, got %q", c.JwtSecret)
	}
	if c.JwtSecretPersistent {
		t.Fatal("empty JWT_SECRET must not be marked persistent")
	}
	if c.AdminUsername != "root" {
		t.Fatalf("AdminUsername = %q", c.AdminUsername)
	}
	if c.AdminPassword != "correct horse battery staple" {
		t.Fatal("AdminPassword was not loaded")
	}
	if !c.AllowInsecureNodeDownloads {
		t.Fatal("AllowInsecureNodeDownloads = false")
	}
	if !c.AllowLegacyNftReports {
		t.Fatal("AllowLegacyNftReports = false")
	}
	wantProxies := []string{"10.0.0.0/8", "192.168.0.0/16"}
	if !reflect.DeepEqual(c.TrustedProxies, wantProxies) {
		t.Fatalf("TrustedProxies = %#v, want %#v", c.TrustedProxies, wantProxies)
	}
	if c.UpdateCheckEnabled || c.UpdateRepository != "example/panel" || c.UpdateCheckInterval != 30*time.Minute {
		t.Fatalf("update check config = enabled:%v repository:%q interval:%v", c.UpdateCheckEnabled, c.UpdateRepository, c.UpdateCheckInterval)
	}
	if c.UpdateTriggerURL != "http://updater:8080/v1/update" || c.UpdateTriggerToken != "sidecar-secret" || c.UpdateImageTag != "v1.2.3" {
		t.Fatalf("update trigger config = url:%q token:%q tag:%q", c.UpdateTriggerURL, c.UpdateTriggerToken, c.UpdateImageTag)
	}
}

func TestLoadMarksConfiguredJWTSecretPersistentForEncryptedSettings(t *testing.T) {
	configured := strings.Repeat("stable-r2-key-", 3)
	t.Setenv("JWT_SECRET", configured)
	c := Load()
	if c.JwtSecret != configured || !c.JwtSecretPersistent {
		t.Fatalf("JWT persistence state: secret=%q persistent=%v", c.JwtSecret, c.JwtSecretPersistent)
	}
}

func TestLoadDefaultsToSecureLoopbackDevelopment(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("ALLOW_INSECURE_NODE_DOWNLOADS", "")
	t.Setenv("ALLOW_LEGACY_NFT_REPORTS", "")
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("UPDATE_CHECK_ENABLED", "")
	t.Setenv("UPDATE_REPOSITORY", "")
	t.Setenv("UPDATE_CHECK_INTERVAL", "")
	t.Setenv("UPDATE_TRIGGER_URL", "")
	t.Setenv("UPDATE_TRIGGER_TOKEN", "")
	t.Setenv("UPDATE_IMAGE_TAG", "")

	c := Load()
	if c.ListenAddr != "127.0.0.1:6365" {
		t.Fatalf("ListenAddr = %q", c.ListenAddr)
	}
	if c.JwtSecret != "" {
		t.Fatalf("JwtSecret = %q", c.JwtSecret)
	}
	if c.JwtSecretPersistent {
		t.Fatal("generated development JWT source must default to non-persistent")
	}
	if c.AdminUsername != "admin_user" {
		t.Fatalf("AdminUsername = %q", c.AdminUsername)
	}
	if c.AdminPassword != "" {
		t.Fatal("AdminPassword must default to empty")
	}
	if c.AllowInsecureNodeDownloads {
		t.Fatal("AllowInsecureNodeDownloads must default to false")
	}
	if c.AllowLegacyNftReports {
		t.Fatal("AllowLegacyNftReports must default to false")
	}
	if len(c.TrustedProxies) != 0 {
		t.Fatalf("TrustedProxies = %#v", c.TrustedProxies)
	}
	if !c.UpdateCheckEnabled || c.UpdateRepository != "nXiaoK/go-panel" || c.UpdateCheckInterval != 6*time.Hour {
		t.Fatalf("default update config = enabled:%v repository:%q interval:%v", c.UpdateCheckEnabled, c.UpdateRepository, c.UpdateCheckInterval)
	}
	if c.UpdateTriggerURL != "" || c.UpdateTriggerToken != "" {
		t.Fatal("automatic update trigger must default to disabled")
	}
	if c.UpdateImageTag != "latest" {
		t.Fatalf("default update image tag = %q", c.UpdateImageTag)
	}
}

func TestLoadClampsUpdateCheckInterval(t *testing.T) {
	t.Setenv("UPDATE_CHECK_INTERVAL", "1s")
	if got := Load().UpdateCheckInterval; got != 5*time.Minute {
		t.Fatalf("short update interval=%v", got)
	}
	t.Setenv("UPDATE_CHECK_INTERVAL", "1000h")
	if got := Load().UpdateCheckInterval; got != 7*24*time.Hour {
		t.Fatalf("long update interval=%v", got)
	}
}
