// Package preflight 在切换版本之前检查候选程序：下载前依据已验签的清单判断兼容性，
// 解包后实际执行候选程序的 version --json 与 migrate-check，确认它能在本镜像中运行并完成迁移。
package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
)

const (
	versionTimeout        = 15 * time.Second
	defaultMigrateTimeout = 10 * time.Minute
	stdoutLimit           = 64 << 10
	stderrLimit           = 4 << 10
	// 就绪超时的上下限与默认值：迁移耗时未知时按 5 分钟等待新版本就绪。
	minReadyTimeout     = 2 * time.Minute
	maxReadyTimeout     = 30 * time.Minute
	defaultReadyTimeout = 5 * time.Minute
)

var (
	// ErrRequiresImageUpdate 表示候选版本需要更新镜像（RuntimeABI 或 supervisor 协议不兼容）。
	ErrRequiresImageUpdate = errors.New("该版本需要先更新镜像")
	// ErrUpgradePathUnsupported 表示当前版本低于候选版本声明的 minUpgradeFrom。
	ErrUpgradePathUnsupported = errors.New("当前版本不能直接升级到该版本")
	// ErrMismatch 表示候选程序报告的信息与已验签清单不一致。
	ErrMismatch = errors.New("候选程序与更新清单不一致")
	// ErrSchemaTooNew 表示数据库副本要求更新的读取纪元，候选程序拒绝迁移。
	ErrSchemaTooNew = errors.New("数据库副本要求更新的读取纪元")
)

// MigrateReport 是 migrate-check --json 的输出。
type MigrateReport struct {
	OK             bool   `json:"ok"`
	DurationMs     int64  `json:"durationMs"`
	SchemaEpoch    int    `json:"schemaEpoch"`
	MinReaderEpoch int    `json:"minReaderEpoch"`
	Error          string `json:"error,omitempty"`
}

// Duration 返回演练迁移的耗时。
func (r MigrateReport) Duration() time.Duration {
	return time.Duration(r.DurationMs) * time.Millisecond
}

// CheckManifest 在下载之前判断能否软更新：RuntimeABI 必须与镜像内置版本相同，
// 当前 supervisor 协议必须在候选版本支持的范围内，当前版本不能低于 minUpgradeFrom。
func CheckManifest(m *manifest.Manifest, current string, info child.Info) error {
	if !info.Supervised {
		return fmt.Errorf("%w：当前进程没有 supervisor 托管", ErrRequiresImageUpdate)
	}
	if m.Compat.RuntimeABI != info.BuiltinRuntimeABI {
		return fmt.Errorf("%w：候选版本要求运行环境 %d，当前镜像为 %d", ErrRequiresImageUpdate, m.Compat.RuntimeABI, info.BuiltinRuntimeABI)
	}
	if !m.Compat.SupervisorProtocol.Contains(info.Protocol) {
		return fmt.Errorf("%w：候选版本支持 supervisor 协议 %d–%d，当前镜像为 %d",
			ErrRequiresImageUpdate, m.Compat.SupervisorProtocol.Min, m.Compat.SupervisorProtocol.Max, info.Protocol)
	}
	less, err := semver.Less(current, m.Compat.MinUpgradeFrom)
	if err != nil {
		return fmt.Errorf("%w：%v", ErrUpgradePathUnsupported, err)
	}
	if less {
		return fmt.Errorf("%w：需要先升级到 %s 或更高版本", ErrUpgradePathUnsupported, m.Compat.MinUpgradeFrom)
	}
	return nil
}

// CheckReport 在解包后确认候选程序报告的版本、commit 与兼容声明和清单完全一致。
func CheckReport(r protocol.VersionReport, m *manifest.Manifest) error {
	if r.Version != m.Version || r.Commit != m.Commit {
		return fmt.Errorf("%w：程序报告 %s（%s），清单为 %s（%s）", ErrMismatch, r.Version, r.Commit, m.Version, m.Commit)
	}
	if r.Compat != m.Compat {
		return fmt.Errorf("%w：程序的兼容声明 %+v 与清单 %+v 不同", ErrMismatch, r.Compat, m.Compat)
	}
	return nil
}

// Version 以最小环境执行候选程序的 version --json；无法执行（例如架构不符）时返回错误。
func Version(ctx context.Context, binary string) (protocol.VersionReport, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	stdout, stderr, code, err := runBounded(ctx, binary, "version", "--json")
	if err != nil {
		return protocol.VersionReport{}, fmt.Errorf("执行候选程序失败: %w", err)
	}
	if code != 0 {
		return protocol.VersionReport{}, fmt.Errorf("候选程序 version 退出码 %d：%s", code, strings.TrimSpace(string(stderr)))
	}
	var report protocol.VersionReport
	if err := json.Unmarshal(lastLine(stdout), &report); err != nil {
		return protocol.VersionReport{}, fmt.Errorf("解析候选程序版本信息失败: %w", err)
	}
	return report, nil
}

// MigrateCheck 在数据库副本上执行候选程序的 migrate-check；退出码 78 返回 ErrSchemaTooNew。
func MigrateCheck(ctx context.Context, binary, dbCopy string, timeout time.Duration) (MigrateReport, error) {
	if timeout <= 0 {
		timeout = defaultMigrateTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, code, err := runBounded(ctx, binary, "migrate-check", "--db", dbCopy, "--json")
	if err != nil {
		return MigrateReport{}, fmt.Errorf("执行迁移演练失败: %w", err)
	}
	var report MigrateReport
	parseErr := json.Unmarshal(lastLine(stdout), &report)
	switch {
	case code == protocol.ExitSchemaTooNew:
		return report, ErrSchemaTooNew
	case code != 0:
		detail := report.Error
		if detail == "" {
			detail = strings.TrimSpace(string(stderr))
		}
		return report, fmt.Errorf("迁移演练失败（退出码 %d）：%s", code, detail)
	case parseErr != nil:
		return report, fmt.Errorf("解析迁移演练结果失败: %w", parseErr)
	case !report.OK:
		return report, fmt.Errorf("迁移演练失败：%s", report.Error)
	}
	return report, nil
}

// ReadyTimeoutFor 根据迁移演练耗时计算新版本的就绪超时：3 倍耗时加 60 秒，限制在 2–30 分钟；
// 没有耗时数据时按 5 分钟。
func ReadyTimeoutFor(migration time.Duration) time.Duration {
	if migration <= 0 {
		return defaultReadyTimeout
	}
	timeout := 3*migration + time.Minute
	return min(max(timeout, minReadyTimeout), maxReadyTimeout)
}

// runBounded 执行候选程序：只传递 PATH、HOME 与 TZ，绝不继承 JWT_SECRET 等密钥；
// 限制输出大小，超时后终止进程并在 2 秒内放弃等待管道。
func runBounded(ctx context.Context, binary string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = minimalEnv()
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	stdout := &limitedBuffer{limit: stdoutLimit}
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stdout.Bytes(), stderr.Bytes(), -1, fmt.Errorf("候选程序超时: %w", ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
	if stdout.overflow {
		return stdout.Bytes(), stderr.Bytes(), 0, fmt.Errorf("候选程序输出超过 %d 字节", stdoutLimit)
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

func minimalEnv() []string {
	env := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp"}
	if tz := os.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	return env
}

func lastLine(output []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	return bytes.TrimSpace(lines[len(lines)-1])
}

// limitedBuffer 只保留前 limit 字节，记录是否溢出，避免异常输出耗尽内存。
type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
			b.overflow = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.overflow = true
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}
