//go:build selfupdate_e2e

package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/supervisor"
)

// e2eBehavior 只存在于 selfupdate_e2e 构建，由 -ldflags "-X main.e2eBehavior=..." 注入，
// 让容器端到端测试构造“启动即失败”或“永不就绪”的新版本；version 与 migrate-check 不受影响。
var e2eBehavior string

func applyE2EBehavior() {
	switch e2eBehavior {
	case "crash-on-start":
		log.Fatalf("e2e：模拟新版本启动失败（自动迁移失败）")
	case "never-ready":
		log.Printf("e2e：模拟新版本卡在启动阶段，等待停止信号")
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		os.Exit(0)
	}
}

// e2eSupervisorTimeouts 允许端到端测试用 GO_PANEL_E2E_STABLE_PERIOD 缩短试运行稳定期。
func e2eSupervisorTimeouts() supervisor.Timeouts {
	var timeouts supervisor.Timeouts
	if value, err := time.ParseDuration(strings.TrimSpace(os.Getenv("GO_PANEL_E2E_STABLE_PERIOD"))); err == nil && value > 0 {
		timeouts.Stable = value
	}
	return timeouts
}
