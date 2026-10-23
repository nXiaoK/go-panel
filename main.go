package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/config"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/handler"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/versioninfo"
	"github.com/nXiaoK/go-panel/internal/service"
	"github.com/nXiaoK/go-panel/internal/task"
	"github.com/nXiaoK/go-panel/internal/ws"
	"github.com/nXiaoK/go-panel/web"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr))
}

// runMain 按第一个参数分发子命令并返回进程退出码；无参数时保持历史行为，直接提供面板服务。
func runMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runServe()
	}
	switch args[0] {
	case "serve":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "serve 不接受额外参数：%s\n", strings.Join(args[1:], " "))
			return 2
		}
		return runServe()
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "supervise", "update", "migrate-check":
		fmt.Fprintf(stderr, "子命令 %s 尚未实现\n", args[0])
		return 2
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `用法：go-panel [子命令]

子命令：
  serve            提供面板服务（无子命令时的默认行为）
  version [--json] 输出构建版本与兼容信息，不读取配置、不访问数据库和网络
  help             显示本帮助
`)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(stderr, "version 不支持参数 %q\n", arg)
			return 2
		}
	}
	report := versioninfo.Current()
	if asJSON {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "输出版本信息失败: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "go-panel %s (%s) 构建于 %s，%s/%s\n", report.Version, report.Commit, report.BuildTime, report.GOOS, report.GOARCH)
	return 0
}

// runServe 提供面板服务。返回 0 表示收到停机信号后正常退出，
// protocol.ExitRestartRequested 表示软更新已就绪、请 supervisor 切换版本。
func runServe() int {
	// 必须早于打开数据库等任何文件：只接管 supervisor 真正传入的就绪管道。
	ready := child.ClaimReadyPipe()

	cfg := config.Load()
	if err := initializeRuntimeSecurity(cfg, cryptorand.Reader, func(secret string) {
		crypto.InitJwtWithTTL(secret, cfg.JwtTTL)
	}); err != nil {
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
	restartRequested := make(chan struct{}, 1)
	updateConfig := updateRuntimeConfigFromConfig(cfg)
	updateConfig.RequestRestart = func() {
		select {
		case restartRequested <- struct{}{}:
		default:
		}
	}
	service.ConfigureUpdateRuntime(updateConfig)

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
	web.Register(r, cfg.CSPMode)
	if cfg.CSPMode == "" || cfg.CSPMode == "report-only" {
		// 仅报告模式不拦截任何资源；提醒运维在实际部署中观察日志，确认无违规后再手动切换 enforce。
		log.Printf("CSP 处于 report-only 观察模式：违规只记录为“CSP 违规报告”日志、不会拦截；确认无违规后设置 CSP_MODE=enforce")
	}

	build := buildinfo.Current()
	log.Printf("go-panel %s (%s) 启动，监听 %s，数据库 %s", build.Version, build.Commit, cfg.ListenAddr, cfg.DBPath)
	server := newHTTPServer(cfg.ListenAddr, r)

	app := &App{
		Server: server,
		OnListening: func(addr net.Addr) {
			if err := ready.Notify(addr); err != nil {
				log.Printf("向 supervisor 发送就绪通知失败: %v", err)
			}
		},
		ShutdownHTTP:  server.Shutdown,
		StopScheduler: task.Stop,
		CloseSessions: ws.Default.CloseAll,
		WaitWorkers:   func(context.Context) error { return nil },
		CloseDatabase: model.Close,
	}

	// SIGINT/SIGTERM 或软更新重启请求触发优雅停机：先停 HTTP，再停调度、关连接、收尾数据库。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exitCode := make(chan int, 1)
	go func() {
		code := waitForStop(ctx, restartRequested)
		stop()
		if code == protocol.ExitRestartRequested {
			log.Printf("收到软更新重启请求，开始优雅停机（最多 %s）", shutdownDrainTimeout)
		} else {
			log.Printf("收到停机信号，开始优雅停机（最多 %s）", shutdownDrainTimeout)
		}
		if err := app.Shutdown(context.Background()); err != nil {
			log.Printf("停机过程中出现错误: %v", err)
		}
		exitCode <- code
	}()

	if err := app.Run(); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
	return <-exitCode
}

// waitForStop 阻塞到停机信号或重启请求，并返回对应的进程退出码。
func waitForStop(ctx context.Context, restart <-chan struct{}) int {
	select {
	case <-ctx.Done():
		return 0
	case <-restart:
		return protocol.ExitRestartRequested
	}
}

func initializeRuntimeSecurity(cfg *config.Config, random io.Reader, initJWT func(string)) error {
	if err := cfg.ValidateCSPMode(); err != nil {
		return err
	}
	if err := cfg.ValidateJWTLifetime(); err != nil {
		return err
	}
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
