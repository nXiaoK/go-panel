// Package state 读写软更新的状态文件：state.json（只由 supervisor 写入）、pending.json（由服务进程
// 或运维命令写入、supervisor 消费）与 db-restore.json（自动恢复数据库的日志）。
//
// 这些文件是跨版本契约，字段定义见 docs/soft-update/README.md §4.5。只允许追加可选字段；
// 读取方忽略未知字段，遇到更高的 schema 时拒绝改写，交由调用方按内置版本运行。
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Schema 是三个状态文件当前的格式版本。
const Schema = 1

// historyLimit 是 state.json 中保留的最近结果条数。
const historyLimit = 20

// 目标类型：镜像内置版本，或数据卷中经签名校验的覆盖版本。
const (
	KindBuiltin = "builtin"
	KindRelease = "release"
)

// 切换动作。
const (
	ActionUpgrade    = "upgrade"
	ActionRollback   = "rollback"
	ActionUseBuiltin = "use-builtin"
)

// 结果。
const (
	OutcomeCommitted  = "committed"
	OutcomeRolledBack = "rolled_back"
	OutcomeRejected   = "rejected"
)

// 失败阶段。
const (
	StageVerify       = "verify"
	StageStart        = "start"
	StageReadyTimeout = "ready_timeout"
	StageStability    = "stability"
	StageHealthz      = "healthz"
	StageSchema       = "schema"
)

var (
	// ErrCorrupt 表示文件不是合法 JSON 或缺少必需字段。
	ErrCorrupt = errors.New("状态文件已损坏")
	// ErrUnsupportedSchema 表示文件由更新的版本写入；调用方不得改写该文件。
	ErrUnsupportedSchema = errors.New("状态文件格式过新")
)

// Base 记录覆盖版本安装时镜像内置版本的身份；镜像版本变化时覆盖版本退役。
type Base struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
}

// Target 描述一个可运行的版本。Dir 是相对运行时目录的 releases/<版本目录名>，内置版本为空。
type Target struct {
	Kind        string `json:"kind"`
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Dir         string `json:"dir,omitempty"`
	SchemaEpoch int    `json:"schemaEpoch,omitempty"`
	ActivatedAt int64  `json:"activatedAt,omitempty"`
}

// IsBuiltin 报告目标是否为镜像内置版本（零值也视为内置版本）。
func (t Target) IsBuiltin() bool {
	return t.Kind == "" || t.Kind == KindBuiltin
}

// Trial 是正在试运行的切换；字段与 Target 平铺在同一个 JSON 对象中。
type Trial struct {
	Target
	Action          string `json:"action"`
	RequestID       string `json:"requestId"`
	Attempts        int    `json:"attempts"`
	StartedAt       int64  `json:"startedAt,omitempty"`
	ReadyTimeoutSec int    `json:"readyTimeoutSec,omitempty"`
	BackupFile      string `json:"backupFile,omitempty"`
	RequestedBy     string `json:"requestedBy,omitempty"`
}

// Result 记录一次切换的最终结果。Detail 是脱敏后的子进程输出尾部。
type Result struct {
	RequestID  string `json:"requestId,omitempty"`
	Action     string `json:"action,omitempty"`
	Version    string `json:"version,omitempty"`
	Outcome    string `json:"outcome"`
	Stage      string `json:"stage,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	DBRestored bool   `json:"dbRestored,omitempty"`
	At         int64  `json:"at"`
}

// QuarantineEntry 记录试运行失败的版本；默认不再自动重试，管理员确认后可强制重试。
type QuarantineEntry struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Reason  string `json:"reason,omitempty"`
	At      int64  `json:"at"`
}

// State 是 state.json 的内容。Active 为空表示运行镜像内置版本。
type State struct {
	Schema     int               `json:"schema"`
	Base       Base              `json:"base"`
	Active     *Target           `json:"active,omitempty"`
	Previous   *Target           `json:"previous,omitempty"`
	Trial      *Trial            `json:"trial,omitempty"`
	LastResult *Result           `json:"lastResult,omitempty"`
	Quarantine []QuarantineEntry `json:"quarantine,omitempty"`
	History    []Result          `json:"history,omitempty"`
}

// New 返回只运行内置版本的初始状态。
func New(base Base) State {
	return State{Schema: Schema, Base: base}
}

// Load 读取 state.json。文件不存在时错误满足 errors.Is(err, os.ErrNotExist)。
func Load(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	return decode[State](raw)
}

// Save 原子写入 state.json（权限 0600）。只写入已知字段：state.json 只由 supervisor 写入，
// 旧 supervisor 读到新字段只会发生在镜像回退时，那时 RetireOverlays 会重建状态。
func Save(path string, st State) error {
	st.Schema = Schema
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

func decode[T any](raw []byte) (T, error) {
	var zero T
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if probe.Schema > Schema {
		return zero, fmt.Errorf("%w: schema=%d", ErrUnsupportedSchema, probe.Schema)
	}
	if probe.Schema < 1 {
		return zero, fmt.Errorf("%w: 缺少 schema", ErrCorrupt)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return zero, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return value, nil
}

// ActiveTarget 返回当前生效的目标；Active 为空时返回内置版本。
func (s *State) ActiveTarget() Target {
	if s.Active != nil {
		return *s.Active
	}
	return Target{Kind: KindBuiltin, Version: s.Base.Version, Commit: s.Base.Commit}
}

// CommitTrial 提交试运行：原 active 成为 previous，试运行目标成为 active。
func (s *State) CommitTrial(now time.Time) (Result, error) {
	if s.Trial == nil {
		return Result{}, errors.New("没有正在试运行的版本")
	}
	trial := *s.Trial
	previous := s.ActiveTarget()
	s.Previous = &previous
	if trial.IsBuiltin() {
		s.Active = nil
	} else {
		active := trial.Target
		active.ActivatedAt = now.UnixMilli()
		s.Active = &active
	}
	s.Trial = nil
	s.removeQuarantine(trial.Version)
	result := Result{
		RequestID: trial.RequestID,
		Action:    trial.Action,
		Version:   trial.Version,
		Outcome:   OutcomeCommitted,
		At:        now.UnixMilli(),
	}
	s.record(result)
	return result, nil
}

// RollbackTrial 放弃试运行并记录失败结果；升级失败的版本加入隔离列表，避免反复自动重试。
func (s *State) RollbackTrial(result Result, now time.Time) {
	if s.Trial == nil {
		s.record(result)
		return
	}
	trial := *s.Trial
	s.Trial = nil
	if result.Outcome == "" {
		result.Outcome = OutcomeRolledBack
	}
	if result.RequestID == "" {
		result.RequestID = trial.RequestID
	}
	if result.Action == "" {
		result.Action = trial.Action
	}
	if result.Version == "" {
		result.Version = trial.Version
	}
	if result.At == 0 {
		result.At = now.UnixMilli()
	}
	if trial.Action == ActionUpgrade && !trial.IsBuiltin() {
		s.removeQuarantine(trial.Version)
		s.Quarantine = append(s.Quarantine, QuarantineEntry{
			Version: trial.Version,
			Commit:  trial.Commit,
			Reason:  quarantineReason(result),
			At:      now.UnixMilli(),
		})
	}
	s.record(result)
}

// Reject 记录被拒绝的切换请求（例如 pending.json 校验失败），不改变当前运行目标。
func (s *State) Reject(result Result) {
	result.Outcome = OutcomeRejected
	s.record(result)
}

// RetireOverlays 在镜像版本变化时调用：以镜像为准，清除所有覆盖版本与试运行状态。
// 只保留版本高于新镜像的隔离记录，其余已无意义。
func (s *State) RetireOverlays(base Base) {
	s.Base = base
	s.Active = nil
	s.Previous = nil
	s.Trial = nil
	s.Quarantine = slices.DeleteFunc(s.Quarantine, func(entry QuarantineEntry) bool {
		return !versionGreater(entry.Version, base.Version)
	})
}

// IsQuarantined 判断版本是否在隔离列表中；commit 为空时只比较版本。
func (s *State) IsQuarantined(version, commit string) bool {
	for _, entry := range s.Quarantine {
		if entry.Version == version && (commit == "" || entry.Commit == "" || entry.Commit == commit) {
			return true
		}
	}
	return false
}

// QuarantineFor 返回版本对应的隔离记录。
func (s *State) QuarantineFor(version string) (QuarantineEntry, bool) {
	for _, entry := range s.Quarantine {
		if entry.Version == version {
			return entry, true
		}
	}
	return QuarantineEntry{}, false
}

// ClearQuarantine 清除指定版本的隔离记录；version 为空时清除全部，返回清除的条数。
func (s *State) ClearQuarantine(version string) int {
	before := len(s.Quarantine)
	if version == "" {
		s.Quarantine = nil
		return before
	}
	s.removeQuarantine(version)
	return before - len(s.Quarantine)
}

func (s *State) removeQuarantine(version string) {
	s.Quarantine = slices.DeleteFunc(s.Quarantine, func(entry QuarantineEntry) bool {
		return entry.Version == version
	})
}

// AppendHistory 追加一条结果，只保留最近 historyLimit 条。
func (s *State) AppendHistory(result Result) {
	s.History = append(s.History, result)
	if overflow := len(s.History) - historyLimit; overflow > 0 {
		s.History = slices.Delete(s.History, 0, overflow)
	}
}

func (s *State) record(result Result) {
	last := result
	s.LastResult = &last
	s.AppendHistory(result)
}

func quarantineReason(result Result) string {
	reason := result.Stage
	if detail := strings.TrimSpace(result.Detail); detail != "" {
		lines := strings.Split(detail, "\n")
		last := strings.TrimSpace(lines[len(lines)-1])
		if reason != "" {
			reason += "："
		}
		reason += last
	}
	if len(reason) > 512 {
		reason = TruncateTail(reason, 512)
	}
	return reason
}
