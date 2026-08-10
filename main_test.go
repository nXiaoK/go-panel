package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/config"
)

func TestNewHTTPServerUsesExplicitResourceTimeouts(t *testing.T) {
	handler := http.NewServeMux()
	server := newHTTPServer("127.0.0.1:6365", handler)
	if server.Addr != "127.0.0.1:6365" || server.Handler != handler {
		t.Fatalf("server address/handler not preserved: %#v", server)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout=%v", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout=%v", server.ReadTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Fatalf("WriteTimeout=%v", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout=%v", server.IdleTimeout)
	}
}

func TestBootstrapOptionsFromConfig(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
		remote     bool
	}{
		{name: "loopback", listenAddr: "127.0.0.1:6365", remote: false},
		{name: "remote", listenAddr: ":6365", remote: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				ListenAddr:    tt.listenAddr,
				AdminUsername: "root",
				AdminPassword: "configured password",
			}
			options := bootstrapOptions(cfg)
			if options.Remote != tt.remote {
				t.Fatalf("Remote = %v, want %v", options.Remote, tt.remote)
			}
			if options.AdminUsername != cfg.AdminUsername {
				t.Fatalf("AdminUsername = %q", options.AdminUsername)
			}
			if options.AdminPassword != cfg.AdminPassword {
				t.Fatal("AdminPassword was not passed to bootstrap")
			}
			if options.CredentialWriter != os.Stderr {
				t.Fatal("generated credentials must be written to stderr")
			}
		})
	}
}

func TestInitializeRuntimeSecurityGeneratesBeforeJWTInitialization(t *testing.T) {
	cfg := &config.Config{ListenAddr: "127.0.0.1:6365"}
	initialized := ""

	err := initializeRuntimeSecurity(
		cfg,
		strings.NewReader(strings.Repeat("r", 32)),
		func(secret string) {
			if secret != cfg.JwtSecret {
				t.Fatalf("InitJwt received %q before config was prepared as %q", secret, cfg.JwtSecret)
			}
			initialized = secret
		},
	)
	if err != nil {
		t.Fatalf("initialize runtime security: %v", err)
	}
	if initialized == "" || initialized != cfg.JwtSecret {
		t.Fatalf("JWT initialized with %q, config secret %q", initialized, cfg.JwtSecret)
	}
}

func TestInitializeRuntimeSecurityRandomFailureDoesNotInitializeJWT(t *testing.T) {
	wantErr := errors.New("runtime random failure")
	cfg := &config.Config{ListenAddr: "127.0.0.1:6365"}
	called := false

	err := initializeRuntimeSecurity(cfg, iotest.ErrReader(wantErr), func(string) {
		called = true
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v, want wrapped random failure", err)
	}
	if called {
		t.Fatal("InitJwt was called after runtime secret generation failed")
	}
	if cfg.JwtSecret != "" {
		t.Fatalf("random failure installed fallback secret %q", cfg.JwtSecret)
	}
}

func TestInitializeRuntimeSecurityRemoteValidationFailureDoesNotInitializeJWT(t *testing.T) {
	cfg := &config.Config{ListenAddr: ":6365"}
	called := false

	err := initializeRuntimeSecurity(cfg, iotest.ErrReader(errors.New("must not read")), func(string) {
		called = true
	})
	if err == nil {
		t.Fatal("remote empty secret must fail startup validation")
	}
	if called {
		t.Fatal("InitJwt was called after remote security validation failed")
	}
}

func TestNodeRuntimeConfigFromConfig(t *testing.T) {
	for _, allow := range []bool{false, true} {
		cfg := &config.Config{AllowInsecureNodeDownloads: allow}
		got := nodeRuntimeConfigFromConfig(cfg)
		if got.AllowInsecureDownloads != allow {
			t.Fatalf("AllowInsecureDownloads = %v, want %v", got.AllowInsecureDownloads, allow)
		}
	}
}

func TestR2BackupRuntimeConfigUsesOnlyPersistentJWTSecret(t *testing.T) {
	// 测试值只验证持久密钥的长度门槛，不代表可用于部署的 JWT_SECRET。
	configured := &config.Config{JwtSecret: strings.Repeat("a", 32), JwtSecretPersistent: true}
	if got := r2BackupRuntimeConfigFromConfig(configured).CredentialEncryptionKey; got != configured.JwtSecret {
		t.Fatalf("persistent R2 encryption key=%q", got)
	}
	ephemeral := &config.Config{JwtSecret: "generated-for-this-process", JwtSecretPersistent: false}
	if got := r2BackupRuntimeConfigFromConfig(ephemeral).CredentialEncryptionKey; got != "" {
		t.Fatalf("ephemeral JWT secret leaked into persistent R2 encryption config: %q", got)
	}
	for _, weak := range []string{config.DefaultJwtSecret, "short-persistent-secret"} {
		cfg := &config.Config{JwtSecret: weak, JwtSecretPersistent: true}
		if got := r2BackupRuntimeConfigFromConfig(cfg).CredentialEncryptionKey; got != "" {
			t.Fatalf("weak JWT secret accepted for persistent R2 encryption: %q", got)
		}
	}
}

func TestUpdateRuntimeConfigFromConfig(t *testing.T) {
	cfg := &config.Config{
		UpdateCheckEnabled:  true,
		UpdateRepository:    "nXiaoK/go-panel",
		UpdateCheckInterval: 45 * time.Minute,
		UpdateTriggerURL:    "http://updater:8080/v1/update",
		UpdateTriggerToken:  "secret-token",
		UpdateImageTag:      "latest",
	}
	got := updateRuntimeConfigFromConfig(cfg)
	if !got.Enabled || got.Repository != cfg.UpdateRepository || got.CheckInterval != cfg.UpdateCheckInterval {
		t.Fatalf("update check runtime config=%#v", got)
	}
	if got.TriggerURL != cfg.UpdateTriggerURL || got.TriggerToken != cfg.UpdateTriggerToken || got.ImageTag != cfg.UpdateImageTag {
		t.Fatalf("update trigger runtime config=%#v", got)
	}
}

func TestDockerComposePassesRemoteSecurityConfiguration(t *testing.T) {
	raw, err := os.ReadFile("compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(raw)
	for _, required := range []string{
		"image: ghcr.io/nxiaok/go-panel:${GO_PANEL_VERSION:-latest}",
		"LISTEN_ADDR: \":6365\"",
		"JWT_SECRET: ${JWT_SECRET:?",
		"ADMIN_PASSWORD: ${ADMIN_PASSWORD:-}",
		"TRUSTED_PROXIES: ${TRUSTED_PROXIES:-}",
		"CORS_ALLOW_ORIGIN: ${CORS_ALLOW_ORIGIN:?",
		"ALLOW_INSECURE_NODE_DOWNLOADS: ${ALLOW_INSECURE_NODE_DOWNLOADS:-false}",
		"UPDATE_CHECK_INTERVAL: ${UPDATE_CHECK_INTERVAL:-6h}",
		"UPDATE_IMAGE_TAG: ${GO_PANEL_VERSION:-latest}",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose.yml is missing %q", required)
		}
	}
	if strings.Contains(compose, "change-me-please") {
		t.Fatal("compose.yml must not contain the historical JWT fallback")
	}
	if strings.Contains(compose, "ADMIN_PASSWORD:?ADMIN_PASSWORD is required") {
		t.Fatal("compose.yml must allow existing secure databases to start without a long-lived ADMIN_PASSWORD")
	}
	if strings.Contains(compose, "build:") || strings.Contains(compose, "./node-assets") {
		t.Fatal("compose.yml must use published images and embedded node assets without local builds")
	}
}

func TestDeploymentUsesConsistentSQLiteBackup(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, required := range []string{
		"VACUUM INTO",
		"PRAGMA integrity_check",
		"不要直接复制运行中的主数据库文件",
	} {
		if !strings.Contains(doc, required) {
			t.Fatalf("deployment backup instructions are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`docker cp "$container_id:/app/data/flux-panel.db" "$backup"`,
		"停止面板，让连接关闭并完成 WAL 收敛",
	} {
		if strings.Contains(doc, forbidden) {
			t.Fatalf("deployment backup instructions contain unsafe assumption %q", forbidden)
		}
	}
}

func TestDockerfileUsesPinnedGoToolchain(t *testing.T) {
	raw, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(raw)
	if !strings.Contains(dockerfile, "FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS backend") {
		t.Fatal("Dockerfile backend builder must pin Go 1.26.5")
	}
	for _, required := range []string{
		"sh ./scripts/build-node-assets.sh",
		"COPY --from=backend /src/node-assets/ /app/node-assets/",
		"internal/buildinfo.Version=$VERSION",
		"HEALTHCHECK",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile is missing %q", required)
		}
	}
}

func clientIPForTrustedProxies(t *testing.T, proxies []string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := configureTrustedProxies(r, proxies); err != nil {
		t.Fatalf("configure trusted proxies: %v", err)
	}
	r.GET("/client-ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	req := httptest.NewRequest(http.MethodGet, "/client-ip", nil)
	req.RemoteAddr = "198.51.100.10:4321"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func TestConfigureTrustedProxiesDisablesHeaderTrustWhenEmpty(t *testing.T) {
	if got := clientIPForTrustedProxies(t, nil); got != "198.51.100.10" {
		t.Fatalf("ClientIP=%q, want direct peer", got)
	}
}

func TestConfigureTrustedProxiesTrustsOnlyExplicitCIDR(t *testing.T) {
	if got := clientIPForTrustedProxies(t, []string{"198.51.100.0/24"}); got != "203.0.113.9" {
		t.Fatalf("ClientIP=%q, want forwarded client", got)
	}
}

func TestConfigureTrustedProxiesRejectsNonCIDR(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := configureTrustedProxies(gin.New(), []string{"198.51.100.10"}); err == nil {
		t.Fatal("trusted proxy without an explicit CIDR must be rejected")
	}
}
