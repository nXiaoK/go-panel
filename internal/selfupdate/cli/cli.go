// Package cli 实现离线运维命令 go-panel update：查看软更新状态，写入回滚或恢复内置版本的请求。
//
// 这些命令不访问数据库与网络，可在 docker exec 中执行。state.json 只由 supervisor 写入，
// 因此除 clear-quarantine（要求面板已停止）外，命令只写 pending.json，重启容器后由 supervisor 处理。
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// Options 提供命令运行所需的环境；Builtin 是执行命令的镜像内置版本。
type Options struct {
	Getenv  func(string) string
	Builtin state.Target
	Now     func() time.Time
}

const usage = `用法：go-panel update <命令>

命令：
  status [--json]             查看运行方式、当前与上一版本、试运行、最近结果和隔离版本
  rollback                    请求回滚到上一版本（重启容器后生效）
  use-builtin                 请求改回镜像内置版本（重启容器后生效）
  clear-quarantine [版本]      清除试运行失败的隔离记录；只能在面板停止时执行
`

// Run 执行 update 子命令并返回退出码。
func Run(args []string, stdout, stderr io.Writer, opts Options) int {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	l, err := layout.Resolve(opts.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch args[0] {
	case "status":
		asJSON := len(args) == 2 && args[1] == "--json"
		if len(args) > 2 || (len(args) == 2 && !asJSON) {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return status(l, opts, asJSON, stdout, stderr)
	case "rollback", "use-builtin":
		if len(args) != 1 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return request(l, opts, args[0], stdout, stderr)
	case "clear-quarantine":
		if len(args) > 2 {
			fmt.Fprint(stderr, usage)
			return 2
		}
		version := ""
		if len(args) == 2 {
			version = args[1]
		}
		return clearQuarantine(l, version, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n\n%s", args[0], usage)
		return 2
	}
}

func loadState(l layout.Layout) (state.State, bool, error) {
	st, err := state.Load(l.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return state.State{}, false, nil
	}
	if err != nil {
		return state.State{}, false, err
	}
	return st, true, nil
}

func status(l layout.Layout, opts Options, asJSON bool, stdout, stderr io.Writer) int {
	st, exists, err := loadState(l)
	if err != nil {
		fmt.Fprintf(stderr, "读取状态文件失败: %v\n", err)
		return 1
	}
	var pending *state.Pending
	if loaded, err := state.LoadPending(l.PendingFile); err == nil {
		pending = &loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "切换请求无效: %v\n", err)
	}
	if asJSON {
		output := struct {
			RuntimeDir string         `json:"runtimeDir"`
			Builtin    state.Target   `json:"builtin"`
			State      *state.State   `json:"state"`
			Pending    *state.Pending `json:"pending"`
		}{RuntimeDir: l.RuntimeDir, Builtin: opts.Builtin, Pending: pending}
		if exists {
			output.State = &st
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "运行时目录：%s\n", l.RuntimeDir)
	fmt.Fprintf(stdout, "镜像内置版本：%s\n", opts.Builtin.Version)
	if !exists {
		fmt.Fprintln(stdout, "尚未使用软更新：运行镜像内置版本")
	} else {
		fmt.Fprintf(stdout, "当前生效：%s\n", describeTarget(st.ActiveTarget()))
		if st.Previous != nil {
			fmt.Fprintf(stdout, "上一版本：%s\n", describeTarget(*st.Previous))
		}
		if st.Trial != nil {
			fmt.Fprintf(stdout, "试运行中：%s（请求 %s，第 %d 次尝试）\n", describeTarget(st.Trial.Target), st.Trial.RequestID, st.Trial.Attempts)
		}
		if last := st.LastResult; last != nil {
			line := fmt.Sprintf("最近结果：%s %s %s（%s）", last.Action, valueOr(last.Version, "-"), outcomeLabel(last.Outcome), formatTime(last.At))
			if last.Stage != "" {
				line += "，阶段 " + last.Stage
			}
			if last.DBRestored {
				line += "，已恢复更新前数据库快照"
			}
			fmt.Fprintln(stdout, line)
		}
		for _, entry := range st.Quarantine {
			fmt.Fprintf(stdout, "隔离版本：%s（%s）\n", entry.Version, valueOr(entry.Reason, "试运行失败"))
		}
	}
	if pending != nil {
		fmt.Fprintf(stdout, "待处理请求：%s → %s（重启容器后生效）\n", pending.Action, describeTarget(pending.Target))
	}
	return 0
}

func request(l layout.Layout, opts Options, command string, stdout, stderr io.Writer) int {
	pending := state.Pending{
		RequestID:   uuid.NewString(),
		RequestedAt: opts.Now().UnixMilli(),
		RequestedBy: "cli",
	}
	switch command {
	case "use-builtin":
		pending.Action = state.ActionUseBuiltin
		pending.Target = state.Target{Kind: state.KindBuiltin}
	case "rollback":
		st, exists, err := loadState(l)
		if err != nil {
			fmt.Fprintf(stderr, "读取状态文件失败: %v\n", err)
			return 1
		}
		if !exists || st.Previous == nil {
			fmt.Fprintln(stderr, "没有可回滚的上一版本")
			return 1
		}
		pending.Action = state.ActionRollback
		pending.Target = *st.Previous
		if pending.Target.IsBuiltin() {
			pending.Target = state.Target{Kind: state.KindBuiltin}
		}
	}
	if err := l.Ensure(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := state.WritePending(l.PendingFile, l.PendingLock, pending); err != nil {
		fmt.Fprintf(stderr, "写入切换请求失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "已写入切换请求：%s → %s。执行 docker restart go-panel（或重启面板容器）后生效；\n", pending.Action, describeTarget(pending.Target))
	fmt.Fprintln(stdout, "目标版本无法启动或读取当前数据库时，supervisor 会自动回到当前版本。")
	return 0
}

func clearQuarantine(l layout.Layout, version string, stdout, stderr io.Writer) int {
	if err := l.Ensure(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := state.TryLock(l.SupervisorLock)
	if errors.Is(err, state.ErrLocked) {
		fmt.Fprintln(stderr, "面板正在运行：请在界面中对该版本使用“仍要重试”，或停止容器后再执行此命令")
		return 1
	}
	if err != nil && !errors.Is(err, state.ErrLockUnsupported) {
		fmt.Fprintf(stderr, "获取实例锁失败: %v\n", err)
		return 1
	}
	if unlock != nil {
		defer func() { _ = unlock() }()
	}
	st, exists, err := loadState(l)
	if err != nil {
		fmt.Fprintf(stderr, "读取状态文件失败: %v\n", err)
		return 1
	}
	if !exists {
		fmt.Fprintln(stdout, "没有隔离记录")
		return 0
	}
	removed := st.ClearQuarantine(strings.TrimSpace(version))
	if err := state.Save(l.StateFile, st); err != nil {
		fmt.Fprintf(stderr, "保存状态文件失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "已清除 %d 条隔离记录\n", removed)
	return 0
}

func describeTarget(target state.Target) string {
	if target.IsBuiltin() {
		if target.Version != "" {
			return "镜像内置版本 " + target.Version
		}
		return "镜像内置版本"
	}
	return target.Version + "（软更新，" + target.Dir + "）"
}

func outcomeLabel(outcome string) string {
	switch outcome {
	case state.OutcomeCommitted:
		return "已启用"
	case state.OutcomeRolledBack:
		return "已回滚"
	case state.OutcomeRejected:
		return "已拒绝"
	default:
		return outcome
	}
}

func formatTime(ms int64) string {
	if ms <= 0 {
		return "时间未知"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
