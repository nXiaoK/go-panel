package supervisor

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// verifyFunc 复验 releases/ 下的版本目录，并确认其版本高于镜像内置版本。
type verifyFunc func(rel string) (state.Target, error)

// launch 描述一次要启动的子进程。trial 非空表示试运行。
type launch struct {
	mode   string
	target state.Target
	trial  *state.Trial
}

// restoreCandidate 是试运行失败后可能需要恢复的更新前快照。
type restoreCandidate struct {
	requestID  string
	version    string
	backupFile string
}

type decideInput struct {
	state      state.State
	pending    *state.Pending
	pendingErr error
	builtin    Builtin
	useBuiltin bool
	verify     verifyFunc
	now        time.Time
}

type decision struct {
	state           state.State
	launch          launch
	consumedPending bool
	restore         *restoreCandidate
	logs            []string
}

// decide 实现启动选择算法的第 4–8 步（README §4.5），不做任何 I/O。
func decide(in decideInput) decision {
	st := in.state
	d := decision{}
	base := state.Base{Version: in.builtin.Version, Commit: in.builtin.Commit}

	// 第 4 步：镜像版本变化时以镜像为准，覆盖版本全部退役。只比较版本号，同一版本重新构建不触发。
	switch {
	case st.Base.Version == "":
		st.Base = base
	case st.Base.Version != in.builtin.Version:
		d.logs = append(d.logs, fmt.Sprintf("镜像版本由 %s 变为 %s：以镜像为准，停用数据卷中的软更新版本", st.Base.Version, in.builtin.Version))
		st.RetireOverlays(base)
	default:
		st.Base.Commit = in.builtin.Commit
	}

	builtinTarget := state.Target{Kind: state.KindBuiltin, Version: in.builtin.Version, Commit: in.builtin.Commit}

	// 第 5 步：应急开关只是临时绕过，不消费切换请求、不修改 active 与 trial。
	if in.useBuiltin {
		d.logs = append(d.logs, "PANEL_USE_BUILTIN=true：忽略软更新版本与切换请求，运行镜像内置版本")
		d.state = st
		d.launch = launch{mode: protocol.RunModeBuiltin, target: builtinTarget}
		return d
	}

	// 第 6 步：消费 pending.json。无效请求记录为 rejected 并删除文件，避免每次启动重复处理。
	if in.pendingErr != nil && !errors.Is(in.pendingErr, os.ErrNotExist) {
		d.consumedPending = true
		st.Reject(state.Result{Stage: state.StageVerify, Detail: "切换请求无效：" + in.pendingErr.Error(), At: in.now.UnixMilli()})
		d.logs = append(d.logs, "忽略无效的切换请求："+in.pendingErr.Error())
	}
	if p := in.pending; p != nil {
		d.consumedPending = true
		target, err := resolvePendingTarget(*p, builtinTarget, in.verify)
		if err != nil {
			st.Reject(state.Result{RequestID: p.RequestID, Action: p.Action, Version: p.Target.Version, Stage: state.StageVerify, Detail: err.Error(), At: in.now.UnixMilli()})
			d.logs = append(d.logs, fmt.Sprintf("拒绝切换请求 %s：%v", p.RequestID, err))
		} else {
			if st.Trial != nil {
				st.Reject(state.Result{RequestID: st.Trial.RequestID, Action: st.Trial.Action, Version: st.Trial.Version, Stage: state.StageStart, Detail: "被新的切换请求取代", At: in.now.UnixMilli()})
			}
			st.Trial = &state.Trial{
				Target:          target,
				Action:          p.Action,
				RequestID:       p.RequestID,
				StartedAt:       in.now.UnixMilli(),
				ReadyTimeoutSec: p.ReadyTimeoutSec,
				BackupFile:      p.BackupFile,
				RequestedBy:     p.RequestedBy,
			}
			d.logs = append(d.logs, fmt.Sprintf("接受切换请求 %s：%s → %s", p.RequestID, p.Action, describe(target)))
		}
	}

	// 第 7 步：试运行。先把尝试次数写盘，容器在试运行期间反复重启时最终判定失败并回滚。
	if st.Trial != nil {
		st.Trial.Attempts++
		trial := *st.Trial
		var failure *state.Result
		if trial.Attempts > maxTrialAttempts {
			failure = &state.Result{Stage: state.StageStart, Detail: "试运行多次未能完成（容器可能在试运行期间反复重启）"}
		} else if !trial.IsBuiltin() {
			if _, err := in.verify(trial.Dir); err != nil {
				failure = &state.Result{Stage: state.StageVerify, Detail: "复验失败：" + err.Error()}
			}
		}
		if failure == nil {
			d.state = st
			d.launch = launch{mode: protocol.RunModeTrial, target: trial.Target, trial: &trial}
			if trial.IsBuiltin() {
				d.launch.target = builtinTarget
			}
			return d
		}
		d.restore = restoreFor(trial)
		st.RollbackTrial(*failure, in.now)
		d.logs = append(d.logs, fmt.Sprintf("放弃试运行 %s：%s", describe(trial.Target), failure.Detail))
	}

	// 第 8 步：运行 active。覆盖版本必须通过复验，否则改用内置版本并记录原因。
	if st.Active != nil {
		active := *st.Active
		_, err := in.verify(active.Dir)
		if err == nil {
			d.state = st
			d.launch = launch{mode: protocol.RunModeRelease, target: active}
			return d
		}
		st.Reject(state.Result{Version: active.Version, Stage: state.StageVerify, Detail: "当前软更新版本复验失败，改用镜像内置版本：" + err.Error(), At: in.now.UnixMilli()})
		d.logs = append(d.logs, fmt.Sprintf("软更新版本 %s 复验失败，改用镜像内置版本：%v", active.Version, err))
		st.Active = nil
	}
	d.state = st
	d.launch = launch{mode: protocol.RunModeBuiltin, target: builtinTarget}
	return d
}

func resolvePendingTarget(p state.Pending, builtin state.Target, verify verifyFunc) (state.Target, error) {
	if p.Target.IsBuiltin() {
		return builtin, nil
	}
	target, err := verify(p.Target.Dir)
	if err != nil {
		return state.Target{}, err
	}
	if target.Version != p.Target.Version || target.Commit != p.Target.Commit {
		return state.Target{}, fmt.Errorf("版本目录中的 %s 与请求的 %s 不一致", target.Version, p.Target.Version)
	}
	return target, nil
}

// restoreFor 返回失败的升级试运行可用于恢复的快照；其他动作不会恢复数据库。
func restoreFor(trial state.Trial) *restoreCandidate {
	if trial.Action != state.ActionUpgrade || trial.BackupFile == "" {
		return nil
	}
	return &restoreCandidate{requestID: trial.RequestID, version: trial.Version, backupFile: trial.BackupFile}
}

// releaseNewerThanBuiltin 确认覆盖版本高于镜像内置版本；内置版本不是稳定版时不允许任何覆盖版本。
func releaseNewerThanBuiltin(release, builtin string) error {
	less, err := semver.Less(builtin, release)
	if err != nil {
		return fmt.Errorf("无法比较软更新版本 %s 与镜像内置版本 %s：%v", release, builtin, err)
	}
	if !less {
		return fmt.Errorf("软更新版本 %s 不高于镜像内置版本 %s", release, builtin)
	}
	return nil
}

func describe(target state.Target) string {
	if target.IsBuiltin() {
		return "镜像内置版本"
	}
	return target.Version
}
