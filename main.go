package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	"github.com/nXiaoK/go-panel/internal/selfupdate/cli"
	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
	"github.com/nXiaoK/go-panel/internal/selfupdate/supervisor"
	"github.com/nXiaoK/go-panel/internal/selfupdate/trust"
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
	case "migrate-check":
		return runMigrateCheck(args[1:], stdout, stderr)
	case "supervise":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "supervise 不接受额外参数：%s\n", strings.Join(args[1:], " "))
			return 2
		}
		return runSupervise(stdout, stderr)
	case "update":
		build := buildinfo.Current()
		return cli.Run(args[1:], stdout, stderr, cli.Options{
			Builtin: state.Target{Kind: state.KindBuiltin, Version: build.Version, Commit: build.Commit},
		})
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
  supervise        作为容器主进程运行：选择镜像内置或软更新版本、试运行并在失败时回滚
  update <命令>     查看软更新状态，或请求回滚、恢复镜像内置版本（go-panel update help）
  migrate-check --db <副本路径> [--json]
                   在数据库副本上演练迁移；会原地修改该文件，只能用于副本
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
	applyE2EBehavior()

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
		if code, handled := schemaTooNewExit(err, cfg.DBPath, os.Stderr); handled {
			return code
		}
		log.Fatalf("数据库初始化失败: %v", err)
	}
	service.EnsureSubscriptionDefaults()
	service.ConfigureNodeRuntime(nodeRuntimeConfigFromConfig(cfg))
	service.ConfigureR2BackupRuntime(r2BackupRuntimeConfigFromConfig(cfg))
	restartRequested := make(chan struct{}, 1)
	updateConfig := updateRuntimeConfigFromConfig(cfg)
	// supervisor 通过环境变量告知运行方式与镜像内置版本；未被托管时软更新不可用，界面给出原因。
	updateConfig.Child = child.FromEnv(os.Getenv)
	if runtimeLayout, err := layout.Resolve(os.Getenv); err == nil {
		updateConfig.Layout = runtimeLayout
	} else {
		log.Printf("解析软更新运行时目录失败，软更新不可用: %v", err)
	}
	updateConfig.KeyRing = trust.Default()
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

// runSupervise 以 supervisor 身份运行：本二进制即镜像内置版本，负责选择并托管面板服务进程。
func runSupervise(stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		fmt.Fprintf(stderr, "无法确定面板程序路径: %v\n", err)
		return 1
	}
	runtimeLayout, err := layout.Resolve(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	useBuiltin, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(protocol.EnvUseBuiltin)))
	build := buildinfo.Current()
	return supervisor.Run(supervisor.Config{
		Layout: runtimeLayout,
		Builtin: supervisor.Builtin{
			Path:        executable,
			Version:     build.Version,
			Commit:      build.Commit,
			RuntimeABI:  buildinfo.RuntimeABI,
			SchemaEpoch: model.SchemaEpoch,
		},
		KeyRing:    trust.Default(),
		Repository: valueOr(strings.TrimSpace(os.Getenv("UPDATE_REPOSITORY")), buildinfo.Repository),
		UseBuiltin: useBuiltin,
		Timeouts:   e2eSupervisorTimeouts(),
		Stdout:     stdout,
		Stderr:     stderr,
	})
}

// schemaTooNewExit 处理“数据库已被不兼容的新版本迁移”：打印恢复指引并返回退出码 78，
// supervisor 据此判断回滚目标无法读取当前数据库，必要时恢复更新前快照。
func schemaTooNewExit(err error, dbPath string, w io.Writer) (int, bool) {
	var tooNew *model.SchemaTooNewError
	if !errors.As(err, &tooNew) {
		return 0, false
	}
	backups := filepath.Join(filepath.Dir(dbPath), "backups")
	fmt.Fprintf(w, "数据库初始化失败: %v\n", tooNew)
	fmt.Fprintf(w, "处理方式：改用不低于 %s 的版本运行；或停止面板后，用 %s 中更新前的快照 pre-update-*.db 替换数据库（会丢失快照之后的修改）。\n",
		valueOr(tooNew.LastWriterVersion, "写入该数据库"), backups)
	return protocol.ExitSchemaTooNew, true
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type migrateCheckOutput struct {
	OK             bool   `json:"ok"`
	DurationMs     int64  `json:"durationMs"`
	SchemaEpoch    int    `json:"schemaEpoch"`
	MinReaderEpoch int    `json:"minReaderEpoch"`
	Error          string `json:"error,omitempty"`
}

// runMigrateCheck 在数据库副本上演练迁移，供软更新在切换前验证候选版本。
// 退出码：0 成功；1 演练失败或拒绝执行；2 参数错误；78 副本要求更新的读取纪元。
func runMigrateCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dbPath := flags.String("db", "", "要演练迁移的数据库副本路径（会被原地修改，只能用于副本）")
	asJSON := flags.Bool("json", false, "以单行 JSON 输出结果")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 || strings.TrimSpace(*dbPath) == "" {
		fmt.Fprintln(stderr, "用法：go-panel migrate-check --db <副本路径> [--json]")
		return 2
	}

	result := func(output migrateCheckOutput, code int) int {
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(output)
		} else if output.OK {
			fmt.Fprintf(stdout, "迁移演练成功，耗时 %dms，结构纪元 %d，最低读取纪元 %d\n", output.DurationMs, output.SchemaEpoch, output.MinReaderEpoch)
		} else {
			fmt.Fprintf(stderr, "迁移演练失败: %s\n", output.Error)
		}
		return code
	}

	// 护栏：拒绝在运行中的主数据库上演练，迁移会原地改写文件。
	if live := strings.TrimSpace(os.Getenv("DB_PATH")); live != "" && sameExistingFile(live, *dbPath) {
		return result(migrateCheckOutput{Error: "--db 指向 DB_PATH 中的主数据库，只能对副本演练"}, 1)
	}
	report, err := model.MigrateCheck(*dbPath, stderr)
	if err != nil {
		code := 1
		var tooNew *model.SchemaTooNewError
		if errors.As(err, &tooNew) {
			code = protocol.ExitSchemaTooNew
		}
		return result(migrateCheckOutput{Error: err.Error()}, code)
	}
	return result(migrateCheckOutput{
		OK:             true,
		DurationMs:     report.Duration.Milliseconds(),
		SchemaEpoch:    report.SchemaEpoch,
		MinReaderEpoch: report.MinReaderEpoch,
	}, 0)
}

func sameExistingFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(left, right)
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
	if err := cfg.ValidateUpdateStrategy(); err != nil {
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
		Strategy:      cfg.UpdateStrategy,
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
