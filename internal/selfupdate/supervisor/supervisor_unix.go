//go:build unix

package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/stage"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

var errStopping = errors.New("supervisor 正在停止")

type outcomeKind int

const (
	// outcomeStopped：supervisor 收到停止信号，子进程已退出。
	outcomeStopped outcomeKind = iota
	// outcomeRestart：子进程以 75 退出，请求重新选择版本。
	outcomeRestart
	// outcomeExited：非试运行的子进程以其他退出码退出。
	outcomeExited
	// outcomeTrialFailed：试运行失败，需要回滚。
	outcomeTrialFailed
)

type outcome struct {
	kind     outcomeKind
	exitCode int
	stage    string
	detail   string
	ready    bool
}

type supervisor struct {
	cfg Config
	log *log.Logger

	mu        sync.Mutex
	current   *exec.Cmd
	stopping  bool
	killTimer *time.Timer

	restore  *restoreCandidate
	restarts []time.Time
}

// Run 运行 supervisor 直到收到停止信号或面板进程以非重启退出码退出，返回进程退出码。
func Run(cfg Config) int {
	return newSupervisor(cfg).run()
}

func newSupervisor(cfg Config) *supervisor {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Environ == nil {
		cfg.Environ = os.Environ
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.GOARCH == "" {
		cfg.GOARCH = runtime.GOARCH
	}
	if cfg.ProbeHealth == nil {
		cfg.ProbeHealth = probeHealth
	}
	if cfg.ValidateBackup == nil {
		cfg.ValidateBackup = model.ValidateSQLiteFile
	}
	cfg.Timeouts = cfg.Timeouts.withDefaults()
	return &supervisor{cfg: cfg, log: log.New(cfg.Stderr, "[supervisor] ", log.LstdFlags)}
}

func (s *supervisor) logf(format string, args ...any) {
	s.log.Printf(format, args...)
}

func (s *supervisor) run() int {
	l := s.cfg.Layout
	if err := l.Ensure(); err != nil {
		s.logf("初始化运行时目录失败: %v", err)
		return 1
	}
	unlock, err := state.TryLock(l.SupervisorLock)
	switch {
	case errors.Is(err, state.ErrLocked):
		s.logf("另一个面板实例正在使用同一数据卷（%s），拒绝启动：SQLite 不支持多实例共用数据库", l.RuntimeDir)
		return 1
	case errors.Is(err, state.ErrLockUnsupported):
		s.logf("警告：%v，无法阻止多个实例共用同一数据卷", err)
	case err != nil:
		s.logf("获取实例锁失败: %v", err)
		return 1
	default:
		defer func() { _ = unlock() }()
	}
	stopForwarding := s.forwardSignals()
	defer stopForwarding()
	defer s.cancelKillTimer()

	if err := s.recoverInterruptedRestore(); err != nil {
		s.logf("%v", err)
		return 1
	}
	if err := stage.CleanStaging(l); err != nil {
		s.logf("清理暂存目录失败: %v", err)
	}

	for {
		if s.isStopping() {
			return 0
		}
		next, err := s.plan()
		if err != nil {
			s.logf("%v", err)
			return 1
		}
		result := s.runChild(next)
		switch result.kind {
		case outcomeStopped:
			return result.exitCode
		case outcomeRestart:
			if s.restartStorm() {
				s.logf("%s 内面板请求重启超过 %d 次，判定为重启风暴，停止运行", s.cfg.Timeouts.RestartWindow, s.cfg.Timeouts.MaxRestarts)
				return 1
			}
			s.logf("面板请求重启以切换版本")
		case outcomeTrialFailed:
			s.failTrial(next, result)
		case outcomeExited:
			if result.exitCode == protocol.ExitSchemaTooNew && !result.ready && s.restore != nil {
				candidate := *s.restore
				s.restore = nil
				s.logf("回滚目标无法读取被新版本迁移过的数据库，使用更新前快照恢复")
				if err := s.restoreDatabase(candidate); err != nil {
					s.logf("自动恢复数据库失败: %v", err)
					return protocol.ExitSchemaTooNew
				}
				s.markRestored(candidate.requestID)
				continue
			}
			if result.exitCode == protocol.ExitSchemaTooNew {
				s.logf("面板因数据库结构纪元不兼容退出：请按上方提示恢复快照，或改用更新的镜像")
			} else {
				s.logf("面板进程退出（退出码 %d），交由容器重启策略处理", result.exitCode)
			}
			return result.exitCode
		}
	}
}

// plan 读取状态与切换请求，执行选择算法并持久化结果，返回要启动的子进程。
func (s *supervisor) plan() (launch, error) {
	l := s.cfg.Layout
	now := s.cfg.Now()
	builtin := state.Target{Kind: state.KindBuiltin, Version: s.cfg.Builtin.Version, Commit: s.cfg.Builtin.Commit}
	st, err := state.Load(l.StateFile)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		st = state.New(state.Base{})
	case errors.Is(err, state.ErrUnsupportedSchema):
		s.logf("状态文件由更新版本的 supervisor 写入，本次按镜像内置版本运行且不修改它")
		return launch{mode: protocol.RunModeBuiltin, target: builtin}, nil
	case errors.Is(err, state.ErrCorrupt):
		corrupt := fmt.Sprintf("%s.corrupt-%d", l.StateFile, now.UnixMilli())
		if renameErr := os.Rename(l.StateFile, corrupt); renameErr != nil {
			return launch{}, fmt.Errorf("另存损坏的状态文件失败: %w", renameErr)
		}
		s.logf("状态文件损坏，已另存为 %s，按镜像内置版本重新开始", corrupt)
		st = state.New(state.Base{})
		st.Reject(state.Result{Stage: state.StageVerify, Detail: "状态文件损坏，已另存为 " + corrupt, At: now.UnixMilli()})
	default:
		return launch{}, fmt.Errorf("读取状态文件失败: %w", err)
	}

	var pending *state.Pending
	loaded, pendingErr := state.LoadPending(l.PendingFile)
	if pendingErr == nil {
		pending = &loaded
	}
	d := decide(decideInput{
		state:      st,
		pending:    pending,
		pendingErr: pendingErr,
		builtin:    s.cfg.Builtin,
		useBuiltin: s.cfg.UseBuiltin,
		verify:     s.verifyRelease,
		now:        now,
	})
	for _, line := range d.logs {
		s.logf("%s", line)
	}
	if err := state.Save(l.StateFile, d.state); err != nil {
		return launch{}, fmt.Errorf("保存状态文件失败: %w", err)
	}
	if d.consumedPending {
		if err := state.RemovePending(l.PendingFile); err != nil {
			s.logf("删除已处理的切换请求失败: %v", err)
		}
	}
	if d.restore != nil {
		s.restore = d.restore
	}
	if !s.cfg.UseBuiltin {
		if err := stage.Prune(l, keepDirs(d.state)); err != nil {
			s.logf("清理旧版本目录失败: %v", err)
		}
	}
	return d.launch, nil
}

func keepDirs(st state.State) []string {
	var keep []string
	for _, target := range []*state.Target{st.Active, st.Previous} {
		if target != nil && !target.IsBuiltin() {
			keep = append(keep, target.Dir)
		}
	}
	if st.Trial != nil && !st.Trial.IsBuiltin() {
		keep = append(keep, st.Trial.Dir)
	}
	return keep
}

// verifyRelease 复验签名与逐文件哈希，并要求覆盖版本高于镜像内置版本。
func (s *supervisor) verifyRelease(rel string) (state.Target, error) {
	_, target, err := stage.VerifyInstalled(s.cfg.Layout, rel, stage.Verifier{
		KeyRing:    s.cfg.KeyRing,
		Repository: s.cfg.Repository,
		GOOS:       s.cfg.GOOS,
		GOARCH:     s.cfg.GOARCH,
	})
	if err != nil {
		return state.Target{}, err
	}
	if err := releaseNewerThanBuiltin(target.Version, s.cfg.Builtin.Version); err != nil {
		return state.Target{}, err
	}
	return target, nil
}

// command 返回子进程的可执行文件与环境变量。覆盖版本同时把 NODE_ASSET_DIR 指向同一版本的节点程序，
// 保证面板宣称的节点版本与实际下发的二进制一致。
func (s *supervisor) command(next launch) (string, []string, error) {
	path := s.cfg.Builtin.Path
	nodeAssets := ""
	if !next.target.IsBuiltin() {
		dir, err := s.cfg.Layout.ReleasePath(next.target.Dir)
		if err != nil {
			return "", nil, err
		}
		path = stage.BinaryPath(dir)
		nodeAssets = stage.NodeAssetDir(dir)
	}
	b := s.cfg.Builtin
	overrides := []string{
		protocol.EnvSupervisorProtocol + "=" + strconv.Itoa(protocol.SupervisorProtocol),
		protocol.EnvReadyFD + "=" + strconv.Itoa(protocol.ReadyFD),
		protocol.EnvRunMode + "=" + next.mode,
		protocol.EnvBuiltinVersion + "=" + b.Version,
		protocol.EnvBuiltinCommit + "=" + b.Commit,
		protocol.EnvBuiltinRuntimeABI + "=" + strconv.Itoa(b.RuntimeABI),
		protocol.EnvBuiltinSchemaEpoch + "=" + strconv.Itoa(b.SchemaEpoch),
		protocol.EnvRuntimeDir + "=" + s.cfg.Layout.RuntimeDir,
	}
	if next.trial != nil {
		overrides = append(overrides, protocol.EnvRequestID+"="+next.trial.RequestID)
	}
	if nodeAssets != "" {
		overrides = append(overrides, protocol.EnvNodeAssetDir+"="+nodeAssets)
	}
	return path, mergeEnv(s.cfg.Environ(), overrides), nil
}

// mergeEnv 去掉基础环境中会被覆盖的变量以及残留的请求 ID，保证每个键只出现一次。
func mergeEnv(base, overrides []string) []string {
	remove := map[string]bool{protocol.EnvRequestID: true}
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		remove[key] = true
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !remove[key] {
			env = append(env, entry)
		}
	}
	return append(env, overrides...)
}

type runningChild struct {
	cmd    *exec.Cmd
	ready  chan string
	exited chan int
	tail   *tailBuffer
}

func (s *supervisor) start(next launch) (*runningChild, error) {
	path, env, err := s.command(next)
	if err != nil {
		return nil, err
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	tail := newTailBuffer(tailLines)
	cmd := exec.Command(path, "serve")
	cmd.Env = env
	cmd.Stdout = io.MultiWriter(s.cfg.Stdout, tail)
	cmd.Stderr = io.MultiWriter(s.cfg.Stderr, tail)
	cmd.ExtraFiles = []*os.File{readyWrite}
	// 子进程退出后最多再等 2 秒输出管道关闭，避免被它遗留的后台进程拖住。
	cmd.WaitDelay = 2 * time.Second

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		_ = readyRead.Close()
		_ = readyWrite.Close()
		return nil, errStopping
	}
	err = cmd.Start()
	if err == nil {
		s.current = cmd
	}
	s.mu.Unlock()
	_ = readyWrite.Close()
	if err != nil {
		_ = readyRead.Close()
		return nil, err
	}

	child := &runningChild{cmd: cmd, ready: make(chan string, 1), exited: make(chan int, 1), tail: tail}
	go func() {
		defer readyRead.Close()
		line, _ := bufio.NewReader(io.LimitReader(readyRead, 4096)).ReadString('\n')
		if addr, ok := parseReady(line); ok {
			child.ready <- addr
		}
	}()
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.current == cmd {
			s.current = nil
		}
		s.mu.Unlock()
		child.exited <- exitCode(err)
	}()
	return child, nil
}

func parseReady(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != protocol.ReadyMessagePrefix {
		return "", false
	}
	for _, field := range fields[1:] {
		if addr, ok := strings.CutPrefix(field, "ADDR="); ok {
			return addr, true
		}
	}
	return "", true
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exitErr.ExitCode()
	}
	return 1
}

// runChild 启动子进程并监控到其退出；试运行时负责就绪超时、稳定期与健康探测，通过后提交并继续监控。
func (s *supervisor) runChild(next launch) outcome {
	child, err := s.start(next)
	if errors.Is(err, errStopping) {
		return outcome{kind: outcomeStopped}
	}
	if err != nil {
		if next.trial != nil {
			return outcome{kind: outcomeTrialFailed, stage: state.StageStart, exitCode: -1, detail: "启动新版本失败：" + err.Error()}
		}
		s.logf("启动面板进程失败: %v", err)
		return outcome{kind: outcomeExited, exitCode: 1}
	}
	s.logf("已启动面板进程：%s（%s，pid %d）", describe(next.target), modeLabel(next.mode), child.cmd.Process.Pid)

	timeouts := s.cfg.Timeouts
	inTrial := next.trial != nil
	var readyC, stableC, probeC <-chan time.Time
	var readyTimer, stableTimer *time.Timer
	var probeTicker *time.Ticker
	defer func() {
		for _, timer := range []*time.Timer{readyTimer, stableTimer} {
			if timer != nil {
				timer.Stop()
			}
		}
		if probeTicker != nil {
			probeTicker.Stop()
		}
	}()
	if inTrial {
		readyTimer = time.NewTimer(s.readyTimeout(*next.trial))
		readyC = readyTimer.C
	}

	probeResults := make(chan error, 8)
	ready, healthy := false, false
	readyAddr := ""
	var lastProbeErr error
	for {
		select {
		case code := <-child.exited:
			result := outcome{exitCode: code, ready: ready, detail: child.tail.String()}
			switch {
			case s.isStopping():
				result.kind = outcomeStopped
			case inTrial:
				result.kind = outcomeTrialFailed
				result.stage = state.StageStart
				if ready {
					result.stage = state.StageStability
				}
			case code == protocol.ExitRestartRequested:
				result.kind = outcomeRestart
			default:
				result.kind = outcomeExited
			}
			return result
		case addr := <-child.ready:
			ready, readyAddr = true, addr
			if !inTrial {
				// 回滚目标已正常就绪，说明它能读取当前数据库，不再需要恢复快照。
				s.restore = nil
				continue
			}
			readyTimer.Stop()
			readyC = nil
			s.logf("新版本已就绪（%s），观察 %s 后正式启用", addr, timeouts.Stable)
			stableTimer = time.NewTimer(timeouts.Stable)
			stableC = stableTimer.C
			probeTicker = time.NewTicker(timeouts.ProbeInterval)
			probeC = probeTicker.C
			go s.probe(readyAddr, probeResults)
		case <-readyC:
			readyC = nil
			if s.isStopping() {
				continue
			}
			s.logf("新版本在 %s 内未就绪，终止并回滚", s.readyTimeout(*next.trial))
			code := s.terminate(child)
			return outcome{kind: outcomeTrialFailed, stage: state.StageReadyTimeout, exitCode: code, detail: child.tail.String()}
		case <-probeC:
			go s.probe(readyAddr, probeResults)
		case probeErr := <-probeResults:
			if probeErr == nil {
				healthy = true
			} else {
				lastProbeErr = probeErr
			}
		case <-stableC:
			stableC = nil
			probeTicker.Stop()
			probeC = nil
			if s.isStopping() {
				continue
			}
			if !healthy {
				s.logf("新版本在观察期内健康检查未通过，终止并回滚")
				code := s.terminate(child)
				detail := child.tail.String()
				if lastProbeErr != nil {
					detail = "健康检查失败：" + lastProbeErr.Error() + "\n" + detail
				}
				return outcome{kind: outcomeTrialFailed, stage: state.StageHealthz, exitCode: code, detail: detail}
			}
			s.commitTrial(*next.trial)
			inTrial = false
		}
	}
}

func (s *supervisor) readyTimeout(trial state.Trial) time.Duration {
	if trial.ReadyTimeoutSec <= 0 {
		return s.cfg.Timeouts.Ready
	}
	// 上限与 preflight.ReadyTimeoutFor 一致，防止异常请求让试运行无限期挂起。
	return min(time.Duration(trial.ReadyTimeoutSec)*time.Second, 30*time.Minute)
}

func (s *supervisor) probe(addr string, results chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeouts.ProbeTimeout)
	defer cancel()
	err := s.cfg.ProbeHealth(ctx, addr)
	select {
	case results <- err:
	default:
	}
}

// terminate 先发 SIGTERM 让面板优雅停机，超过 KillGrace 再强制结束，返回退出码。
func (s *supervisor) terminate(child *runningChild) int {
	_ = child.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(s.cfg.Timeouts.KillGrace)
	defer timer.Stop()
	select {
	case code := <-child.exited:
		return code
	case <-timer.C:
		_ = child.cmd.Process.Kill()
		return <-child.exited
	}
}

func (s *supervisor) commitTrial(trial state.Trial) {
	l := s.cfg.Layout
	st, err := state.Load(l.StateFile)
	if err != nil {
		s.logf("提交试运行时读取状态失败: %v", err)
		return
	}
	if st.Trial == nil || st.Trial.RequestID != trial.RequestID {
		s.logf("状态文件中的试运行已变化，跳过提交")
		return
	}
	if _, err := st.CommitTrial(s.cfg.Now()); err != nil {
		s.logf("提交试运行失败: %v", err)
		return
	}
	if err := state.Save(l.StateFile, st); err != nil {
		s.logf("保存状态文件失败: %v", err)
		return
	}
	if err := stage.Prune(l, keepDirs(st)); err != nil {
		s.logf("清理旧版本目录失败: %v", err)
	}
	s.restore = nil
	s.logf("%s 已通过试运行，正式启用（请求 %s）", describe(trial.Target), trial.RequestID)
}

func (s *supervisor) failTrial(next launch, result outcome) {
	trial := *next.trial
	l := s.cfg.Layout
	st, err := state.Load(l.StateFile)
	if err == nil && st.Trial != nil && st.Trial.RequestID == trial.RequestID {
		st.RollbackTrial(state.Result{
			Stage:    result.stage,
			ExitCode: result.exitCode,
			Detail:   state.Redact(result.detail, detailLimit),
		}, s.cfg.Now())
		if err := state.Save(l.StateFile, st); err != nil {
			s.logf("保存回滚结果失败: %v", err)
		}
	} else if err != nil {
		s.logf("记录回滚结果时读取状态失败: %v", err)
	}
	s.restore = restoreFor(trial)
	s.logf("%s 试运行失败（阶段 %s，退出码 %d），回滚到之前的版本", describe(trial.Target), result.stage, result.exitCode)
}

// markRestored 在最近一次结果上标记“已恢复更新前快照”，界面据此提醒试运行期间的修改已丢失。
func (s *supervisor) markRestored(requestID string) {
	l := s.cfg.Layout
	st, err := state.Load(l.StateFile)
	if err != nil {
		s.logf("记录数据库恢复结果失败: %v", err)
		return
	}
	if st.LastResult != nil && st.LastResult.RequestID == requestID {
		st.LastResult.DBRestored = true
	}
	for i := range st.History {
		if st.History[i].RequestID == requestID {
			st.History[i].DBRestored = true
		}
	}
	if err := state.Save(l.StateFile, st); err != nil {
		s.logf("记录数据库恢复结果失败: %v", err)
	}
}

func (s *supervisor) restartStorm() bool {
	now := s.cfg.Now()
	cutoff := now.Add(-s.cfg.Timeouts.RestartWindow)
	recent := s.restarts[:0]
	for _, at := range s.restarts {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	s.restarts = append(recent, now)
	return len(s.restarts) > s.cfg.Timeouts.MaxRestarts
}

// forwardSignals 把停止与诊断信号转发给当前子进程；SIGTERM、SIGINT 同时进入停止流程。
func (s *supervisor) forwardSignals() func() {
	signals := s.cfg.Signals
	stopNotify := func() {}
	if signals == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
		signals = ch
		stopNotify = func() { signal.Stop(ch) }
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				s.handleSignal(sig)
			}
		}
	}()
	return func() {
		close(done)
		stopNotify()
	}
}

func (s *supervisor) handleSignal(sig os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (sig == syscall.SIGTERM || sig == syscall.SIGINT) && !s.stopping {
		s.stopping = true
		s.logf("收到 %v，停止面板（最多等待 %s）", sig, s.cfg.Timeouts.Stop)
		s.killTimer = time.AfterFunc(s.cfg.Timeouts.Stop, s.forceKill)
	}
	if s.current != nil && s.current.Process != nil {
		_ = s.current.Process.Signal(sig)
	}
}

func (s *supervisor) forceKill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && s.current.Process != nil {
		s.logf("面板未在宽限期内退出，强制结束")
		_ = s.current.Process.Kill()
	}
}

func (s *supervisor) cancelKillTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killTimer != nil {
		s.killTimer.Stop()
	}
}

func (s *supervisor) isStopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping
}

// probeHealth 请求子进程的 /healthz；未指定地址（0.0.0.0、::）时改用回环地址，并且不经过任何代理。
func probeHealth(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("就绪消息中的地址 %q 无效: %w", addr, err)
	}
	candidates := []string{addr}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		candidates = []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)}
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	var lastErr error
	for _, candidate := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+candidate+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		lastErr = fmt.Errorf("/healthz 返回 HTTP %d", resp.StatusCode)
	}
	return lastErr
}

func modeLabel(mode string) string {
	switch mode {
	case protocol.RunModeTrial:
		return "试运行"
	case protocol.RunModeRelease:
		return "软更新版本"
	default:
		return "镜像内置版本"
	}
}

// tailBuffer 保留子进程合并输出的最后若干行，用于记录失败原因；写入方是 stdout 与 stderr 两个复制协程。
type tailBuffer struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
	max     int
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := make([]byte, 0, len(b.partial)+len(p))
	data = append(data, b.partial...)
	data = append(data, p...)
	for {
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			break
		}
		b.lines = append(b.lines, string(data[:index]))
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
		data = data[index+1:]
	}
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	b.partial = data
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := append([]string(nil), b.lines...)
	if len(b.partial) > 0 {
		lines = append(lines, string(b.partial))
	}
	return strings.Join(lines, "\n")
}
