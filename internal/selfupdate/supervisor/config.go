// Package supervisor 实现容器主进程 go-panel supervise：选择要运行的版本（镜像内置或数据卷中经
// 签名校验的软更新版本），启动并监控面板服务进程，对新版本做试运行门控，失败时自动回滚，
// 必要时恢复更新前的数据库快照。状态机定义见 docs/soft-update/README.md §4.5。
//
// supervisor 是镜像内置的稳定引导层：它只执行能用内置公钥复验签名与逐文件哈希的产物，
// 并且只随镜像更新。
package supervisor

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
)

const (
	// maxTrialAttempts 是一次切换最多尝试启动的次数；容器在试运行期间被重启也计一次。
	maxTrialAttempts = 2
	// detailLimit 是写入 lastResult.detail 的子进程输出上限。
	detailLimit = 2048
	// tailLines 是保留的子进程最近输出行数。
	tailLines = 64
)

// Builtin 描述镜像内置的面板程序，也就是 supervisor 自身。
type Builtin struct {
	Path        string
	Version     string
	Commit      string
	RuntimeABI  int
	SchemaEpoch int
}

// Timeouts 是试运行与停机的计时参数；零值字段使用 DefaultTimeouts 中的值。
type Timeouts struct {
	// Ready 是 pending 未指定时等待新版本发送就绪消息的时长。
	Ready time.Duration
	// Stable 是就绪后的稳定观察期，期间子进程不能退出且至少一次健康探测成功。
	Stable time.Duration
	// ProbeInterval 与 ProbeTimeout 控制 /healthz 探测。
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	// Stop 是收到停止信号后等待子进程退出的时长，超时后强制结束（Compose 停止宽限期为 30 秒）。
	Stop time.Duration
	// KillGrace 是试运行失败时发送 SIGTERM 后等待退出的时长，超时后发送 SIGKILL。
	KillGrace time.Duration
	// RestartWindow 内退出码 75 超过 MaxRestarts 次视为重启风暴。
	RestartWindow time.Duration
	MaxRestarts   int
}

// DefaultTimeouts 返回生产环境使用的计时参数。
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Ready:         5 * time.Minute,
		Stable:        60 * time.Second,
		ProbeInterval: 5 * time.Second,
		ProbeTimeout:  3 * time.Second,
		Stop:          25 * time.Second,
		KillGrace:     20 * time.Second,
		RestartWindow: 60 * time.Second,
		MaxRestarts:   5,
	}
}

func (t Timeouts) withDefaults() Timeouts {
	defaults := DefaultTimeouts()
	if t.Ready <= 0 {
		t.Ready = defaults.Ready
	}
	if t.Stable <= 0 {
		t.Stable = defaults.Stable
	}
	if t.ProbeInterval <= 0 {
		t.ProbeInterval = defaults.ProbeInterval
	}
	if t.ProbeTimeout <= 0 {
		t.ProbeTimeout = defaults.ProbeTimeout
	}
	if t.Stop <= 0 {
		t.Stop = defaults.Stop
	}
	if t.KillGrace <= 0 {
		t.KillGrace = defaults.KillGrace
	}
	if t.RestartWindow <= 0 {
		t.RestartWindow = defaults.RestartWindow
	}
	if t.MaxRestarts <= 0 {
		t.MaxRestarts = defaults.MaxRestarts
	}
	return t
}

// Config 是 supervisor 的运行配置。测试通过 Signals、ProbeHealth、ValidateBackup 等字段注入行为。
type Config struct {
	Layout     layout.Layout
	Builtin    Builtin
	KeyRing    manifest.KeyRing
	Repository string
	// UseBuiltin 对应 PANEL_USE_BUILTIN：应急开关，忽略软更新版本与切换请求，运行镜像内置版本。
	UseBuiltin bool
	GOOS       string
	GOARCH     string
	Timeouts   Timeouts

	// Environ 返回传给子进程的基础环境变量，默认 os.Environ。
	Environ func() []string
	Stdout  io.Writer
	Stderr  io.Writer
	Now     func() time.Time
	// Signals 为空时 supervisor 自行监听 SIGTERM、SIGINT、SIGHUP、SIGQUIT。
	Signals <-chan os.Signal
	// ProbeHealth 探测子进程的 /healthz，默认通过回环地址发起 HTTP 请求。
	ProbeHealth func(ctx context.Context, addr string) error
	// ValidateBackup 在自动恢复前校验快照，默认 model.ValidateSQLiteFile。
	ValidateBackup func(path string) error
}
