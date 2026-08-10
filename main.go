package main

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/config"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/handler"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/service"
	"github.com/nXiaoK/go-panel/internal/task"
	"github.com/nXiaoK/go-panel/internal/ws"
	"github.com/nXiaoK/go-panel/web"
)

func main() {
	cfg := config.Load()
	if err := initializeRuntimeSecurity(cfg, cryptorand.Reader, crypto.InitJwt); err != nil {
		log.Fatalf("安全配置无效: %v", err)
	}

	// 生产环境安全配置强制检查
	env := os.Getenv("ENVIRONMENT")
	isProduction := env == "production" || env == "prod"

	// 生产环境 CORS 必须显式配置（默认 * 配合 Authorization 头可被任意网页调用）
	if isProduction && (cfg.CorsAllowOrigin == "" || cfg.CorsAllowOrigin == "*") {
		log.Fatalf("❌ 生产环境必须显式配置 CORS_ALLOW_ORIGIN（逗号分隔的白名单），不允许使用通配 *")
	}

	// SQLite 初始化（自动迁移 + 种子数据）
	if err := model.Init(cfg.DBPath, bootstrapOptions(cfg)); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	service.EnsureSubscriptionDefaults()
	service.ConfigureNodeRuntime(nodeRuntimeConfigFromConfig(cfg))
	service.ConfigureR2BackupRuntime(r2BackupRuntimeConfigFromConfig(cfg))
	service.ConfigureUpdateRuntime(updateRuntimeConfigFromConfig(cfg))

	// 定时任务
	task.Start()

	// HTTP 路由
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	if err := configureTrustedProxies(r, cfg.TrustedProxies); err != nil {
		log.Fatalf("可信代理配置无效: %v", err)
	}
	// /system-info 的 query 携带 JWT/节点密钥，/flow、/node/nft-config 携带节点密钥，
	// 跳过访问日志避免凭据落盘
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{
			"/system-info",
			"/flow/upload", "/flow/nft-upload", "/flow/nft-upload-v2", "/flow/config",
			"/api/v1/node/nft-config",
		},
	}), gin.Recovery())

	handler.Register(r, cfg.CorsAllowOrigin, cfg.AllowLegacyNftReports)
	web.Register(r)

	build := buildinfo.Current()
	log.Printf("go-panel %s (%s) 启动，监听 %s，数据库 %s", build.Version, build.Commit, cfg.ListenAddr, cfg.DBPath)
	server := newHTTPServer(cfg.ListenAddr, r)

	app := &App{
		Server:        server,
		ShutdownHTTP:  server.Shutdown,
		StopScheduler: task.Stop,
		CloseSessions: ws.Default.CloseAll,
		WaitWorkers:   func(context.Context) error { return nil },
		CloseDatabase: model.Close,
	}

	// SIGINT/SIGTERM 触发优雅停机：先停 HTTP，再停调度、关连接、收尾数据库。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
		log.Printf("收到停机信号，开始优雅停机（最多 %s）", shutdownDrainTimeout)
		if err := app.Shutdown(context.Background()); err != nil {
			log.Printf("停机过程中出现错误: %v", err)
		}
	}()

	if err := app.Run(); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func initializeRuntimeSecurity(cfg *config.Config, random io.Reader, initJWT func(string)) error {
	if err := cfg.PrepareRuntimeSecurity(random); err != nil {
		return err
	}
	if err := cfg.ValidateStaticSecurity(); err != nil {
		return err
	}
	initJWT(cfg.JwtSecret)
	return nil
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func nodeRuntimeConfigFromConfig(cfg *config.Config) service.NodeRuntimeConfig {
	return service.NodeRuntimeConfig{
		AllowInsecureDownloads: cfg.AllowInsecureNodeDownloads,
	}
}

func r2BackupRuntimeConfigFromConfig(cfg *config.Config) service.R2BackupRuntimeConfig {
	// loopback 开发模式可能生成仅在本进程有效的临时 JWT 密钥；不能用它加密
	// 需要跨重启读取的 R2 Secret Access Key，否则重启后凭据将无法恢复。
	// 即便仅监听 loopback，也拒绝历史默认值和不足 32 字节的弱密钥，避免
	// 通过反向代理部署时以低熵密钥保护对象存储凭据。
	encryptionKey := ""
	if cfg.JwtSecretPersistent && cfg.JwtSecret != config.DefaultJwtSecret && len([]byte(cfg.JwtSecret)) >= 32 {
		encryptionKey = cfg.JwtSecret
	}
	return service.R2BackupRuntimeConfig{CredentialEncryptionKey: encryptionKey}
}

func updateRuntimeConfigFromConfig(cfg *config.Config) service.UpdateRuntimeConfig {
	return service.UpdateRuntimeConfig{
		Enabled:       cfg.UpdateCheckEnabled,
		Repository:    cfg.UpdateRepository,
		CheckInterval: cfg.UpdateCheckInterval,
		TriggerURL:    cfg.UpdateTriggerURL,
		TriggerToken:  cfg.UpdateTriggerToken,
		ImageTag:      cfg.UpdateImageTag,
	}
}

func configureTrustedProxies(r *gin.Engine, proxies []string) error {
	if len(proxies) == 0 {
		return r.SetTrustedProxies(nil)
	}

	cidrs := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		_, network, err := net.ParseCIDR(proxy)
		if err != nil {
			return fmt.Errorf("%q 不是有效 CIDR: %w", proxy, err)
		}
		cidrs = append(cidrs, network.String())
	}
	return r.SetTrustedProxies(cidrs)
}

func bootstrapOptions(cfg *config.Config) model.BootstrapOptions {
	return model.BootstrapOptions{
		Remote:           !config.IsLoopbackListenAddr(cfg.ListenAddr),
		AdminUsername:    cfg.AdminUsername,
		AdminPassword:    cfg.AdminPassword,
		CredentialWriter: os.Stderr,
	}
}
