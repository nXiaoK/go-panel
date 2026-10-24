package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/preflight"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
	"github.com/nXiaoK/go-panel/internal/selfupdate/stage"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

// 软更新作业阶段（README §4.8）。restarting 之后进程退出，最终结果由 supervisor 写入 state.json。
const (
	updateStageQueued       = "queued"
	updateStageDownloading  = "downloading"
	updateStageExtracting   = "extracting"
	updateStagePreflight    = "preflight"
	updateStageBackingUp    = "backing_up"
	updateStageMigrateCheck = "migrate_check"
	updateStageRestarting   = "restarting"
	updateStageFailed       = "failed"
	updateStageCanceled     = "canceled"
)

const (
	// updateJobTimeout 是一次软更新作业（下载到写入切换请求）的总时长上限。
	updateJobTimeout = 20 * time.Minute
	// migrateCheckTimeout 是在数据库副本上演练迁移的时长上限。
	migrateCheckTimeout = 10 * time.Minute
	// diskSpaceMargin 是磁盘空间预检额外保留的余量。
	diskSpaceMargin = 64 << 20
)

// PanelUpdateJob 是后台软更新作业的进度快照。
type PanelUpdateJob struct {
	ID              string `json:"id"`
	TargetVersion   string `json:"targetVersion"`
	Stage           string `json:"stage"`
	StartedAt       int64  `json:"startedAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	BytesDone       int64  `json:"bytesDone,omitempty"`
	BytesTotal      int64  `json:"bytesTotal,omitempty"`
	ReadyTimeoutSec int    `json:"readyTimeoutSec,omitempty"`
	RequestID       string `json:"requestId,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (j *PanelUpdateJob) terminal() bool {
	return j.Stage == updateStageFailed || j.Stage == updateStageCanceled
}

// PanelUpdateApplyRequest 是“立即更新”的请求体。TargetVersion 缺省时兼容旧前端，使用最新检查结果；
// Force 允许重试曾试运行失败（被隔离）的版本。
type PanelUpdateApplyRequest struct {
	TargetVersion string `json:"targetVersion"`
	Force         bool   `json:"force"`
}

// PanelRollbackResult 表示回滚请求已写入，面板即将重启并试运行回滚目标。
type PanelRollbackResult struct {
	RequestID     string `json:"requestId"`
	TargetVersion string `json:"targetVersion"`
	Started       bool   `json:"started"`
}

type updateJob struct {
	mu         sync.Mutex
	snapshot   PanelUpdateJob
	cancel     context.CancelFunc
	cancelable bool
	canceled   bool
	now        func() time.Time
}

func (j *updateJob) update(fn func(*PanelUpdateJob)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.snapshot)
	j.snapshot.UpdatedAt = j.now().UnixMilli()
}

func (j *updateJob) setStage(stage string) {
	j.update(func(job *PanelUpdateJob) { job.Stage = stage })
	log.Printf("面板软更新[%s] 阶段=%s 目标=%s", j.snapshot.ID, stage, j.snapshot.TargetVersion)
}

func (j *updateJob) progress(done, total int64) {
	j.update(func(job *PanelUpdateJob) {
		job.BytesDone, job.BytesTotal = done, total
	})
}

func (j *updateJob) get() PanelUpdateJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshot
}

// enterCommitPhase 在备份之前调用：此后作业不可取消。已请求取消时返回 false。
func (j *updateJob) enterCommitPhase() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.canceled {
		return false
	}
	j.cancelable = false
	return true
}

func (j *updateJob) requestCancel() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.cancelable || j.snapshot.terminal() {
		return false
	}
	j.canceled = true
	j.cancel()
	return true
}

func (j *updateJob) finish(err error) {
	j.mu.Lock()
	canceled := j.canceled
	j.mu.Unlock()
	j.update(func(job *PanelUpdateJob) {
		if canceled {
			job.Stage = updateStageCanceled
			job.Error = "已取消"
			return
		}
		job.Stage = updateStageFailed
		job.Error = truncateUpdateRunes(err.Error(), 1000)
	})
	if canceled {
		log.Printf("面板软更新[%s] 已取消", j.snapshot.ID)
	} else {
		log.Printf("面板软更新[%s] 失败: %v", j.snapshot.ID, err)
	}
}

var updateJobs = struct {
	sync.Mutex
	current *updateJob
}{}

func currentUpdateJobSnapshot() *PanelUpdateJob {
	updateJobs.Lock()
	job := updateJobs.current
	updateJobs.Unlock()
	if job == nil {
		return nil
	}
	snapshot := job.get()
	return &snapshot
}

// updateCoordination 让软更新与数据库恢复互斥：从备份阶段开始直到进程退出，拒绝恢复数据库；
// 恢复进行中时拒绝开始备份，避免快照与恢复交错。
var updateCoordination = struct {
	sync.Mutex
	restoring      bool
	committing     bool
	restartPending bool
}{}

func beginDatabaseRestore() error {
	updateCoordination.Lock()
	defer updateCoordination.Unlock()
	if updateCoordination.committing || updateCoordination.restartPending {
		return errors.New("面板正在更新并即将重启，暂不能恢复数据库")
	}
	updateCoordination.restoring = true
	return nil
}

func endDatabaseRestore() {
	updateCoordination.Lock()
	updateCoordination.restoring = false
	updateCoordination.Unlock()
}

func beginUpdateCommit() error {
	updateCoordination.Lock()
	defer updateCoordination.Unlock()
	if updateCoordination.restoring || model.Gate.IsMaintenance() {
		return errors.New("数据库恢复进行中，请稍后再试")
	}
	if updateCoordination.restartPending {
		return errors.New("面板正在重启以切换版本，请稍候")
	}
	updateCoordination.committing = true
	return nil
}

func endUpdateCommit() {
	updateCoordination.Lock()
	updateCoordination.committing = false
	updateCoordination.Unlock()
}

func markRestartPending() {
	updateCoordination.Lock()
	updateCoordination.restartPending = true
	updateCoordination.Unlock()
}

func updateRestartPending() bool {
	updateCoordination.Lock()
	defer updateCoordination.Unlock()
	return updateCoordination.restartPending
}

// ApplyPanelUpdate 处理“立即更新”：强制刷新检查后按生效策略启动软更新作业或调用更新侧车。
func ApplyPanelUpdate(ctx context.Context, actor string, req PanelUpdateApplyRequest) result.R {
	updateTriggerMu.Lock()
	defer updateTriggerMu.Unlock()
	if updateRestartPending() {
		return result.Err("面板正在重启以切换版本，请稍候")
	}
	if job := currentUpdateJobSnapshot(); job != nil && !job.terminal() {
		return result.Err("已有更新作业在运行")
	}
	status, err := checkPanelUpdate(ctx, true)
	if err != nil {
		return result.Err("更新前检查失败：" + err.Error())
	}
	if !status.UpdateAvailable {
		return result.Err("当前已经是最新稳定版")
	}
	if target := strings.TrimSpace(req.TargetVersion); target != "" && semver.Normalize(target) != status.LatestVersion {
		return result.Err(fmt.Sprintf("版本已变化：最新稳定版为 %s，请重新检查后再更新", status.LatestVersion))
	}
	config, _ := currentUpdateRuntime()
	switch status.Strategy {
	case UpdateStrategySidecar:
		return triggerSidecar(ctx, config, status)
	case UpdateStrategySoft:
		return startSoftUpdate(config, status, actor, req.Force)
	default:
		return result.Err("当前部署不支持一键更新，请在部署目录执行 docker compose pull && docker compose up -d")
	}
}

func startSoftUpdate(config UpdateRuntimeConfig, status PanelUpdateStatus, actor string, force bool) result.R {
	soft := status.SoftUpdate
	if !soft.Available {
		if soft.Reason != softReasonQuarantined {
			return result.Err("当前无法软更新：" + valueOrReason(soft))
		}
		if !force {
			return result.Err("该版本曾试运行失败（" + valueOrReason(soft) + "），确认已排除原因后可强制重试")
		}
	}
	candidate := currentReleaseCandidate()
	if candidate == nil || candidate.manifest.Version != status.LatestVersion {
		return result.Err("更新清单已失效，请重新检查")
	}
	if model.Gate.IsMaintenance() {
		return result.Err("数据库恢复进行中，请稍后再试")
	}
	if need := requiredUpdateSpace(candidate); config.Layout.RuntimeDir != "" {
		if free, err := config.FreeBytes(config.Layout.RuntimeDir); err == nil && free < need {
			return result.Err(fmt.Sprintf("数据卷剩余空间不足：软更新需要约 %s，当前可用 %s", formatUpdateBytes(need), formatUpdateBytes(free)))
		}
	}

	now := config.Now()
	job := &updateJob{now: config.Now, cancelable: true, snapshot: PanelUpdateJob{
		ID:            uuid.NewString(),
		TargetVersion: candidate.manifest.Version,
		Stage:         updateStageQueued,
		StartedAt:     now.UnixMilli(),
		UpdatedAt:     now.UnixMilli(),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), updateJobTimeout)
	job.cancel = cancel
	updateJobs.Lock()
	updateJobs.current = job
	updateJobs.Unlock()
	log.Printf("面板软更新[%s] 开始：%s → %s（操作人 %s）", job.snapshot.ID, status.Current.Version, candidate.manifest.Version, actor)

	go func() {
		defer cancel()
		if err := executeSoftUpdate(ctx, job, config, candidate, status.Current.Version, actor); err != nil {
			job.finish(err)
		}
	}()
	return result.Ok(PanelUpdateTriggerResult{
		TargetVersion: candidate.manifest.Version,
		Started:       true,
		Strategy:      UpdateStrategySoft,
		JobID:         job.snapshot.ID,
	})
}

// executeSoftUpdate 依次下载、安装、预检、备份、演练迁移，最后写入切换请求并请求重启。
func executeSoftUpdate(ctx context.Context, job *updateJob, config UpdateRuntimeConfig, candidate *releaseCandidate, current, actor string) error {
	l := config.Layout
	m := candidate.manifest
	verifier := stage.Verifier{KeyRing: config.KeyRing, Repository: config.Repository, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	name, err := layout.ReleaseDirName(m.Version, m.Commit)
	if err != nil {
		return err
	}
	target, verifyErr := installedTarget(l, layout.RelativeReleaseDir(name), verifier)
	if verifyErr != nil {
		if target, err = downloadAndInstall(ctx, job, config, candidate, verifier); err != nil {
			return err
		}
	}

	job.setStage(updateStagePreflight)
	dir, err := l.ReleasePath(target.Dir)
	if err != nil {
		return err
	}
	binary := stage.BinaryPath(dir)
	report, err := preflight.Version(ctx, binary)
	if err != nil {
		return fmt.Errorf("新版本程序无法在当前镜像中运行: %w", err)
	}
	if err := preflight.CheckReport(report, m); err != nil {
		return err
	}
	if err := preflight.CheckManifest(m, current, config.Child); err != nil {
		return err
	}

	if !job.enterCommitPhase() {
		return context.Canceled
	}
	if err := beginUpdateCommit(); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			endUpdateCommit()
		}
	}()

	job.setStage(updateStageBackingUp)
	backupFile, err := CreatePreUpdateBackup(m.Version)
	if err != nil {
		return fmt.Errorf("更新前数据库备份失败: %w", err)
	}
	job.setStage(updateStageMigrateCheck)
	migration, err := rehearseMigration(ctx, l, binary, backupFile)
	if err != nil {
		return err
	}
	if config.RequestRestart == nil {
		return errors.New("当前进程不支持自重启，无法切换版本")
	}
	readyTimeout := preflight.ReadyTimeoutFor(migration.Duration())
	if override, ok := e2eReadyTimeout(); ok {
		readyTimeout = override
	}
	pending := state.Pending{
		RequestID:       uuid.NewString(),
		Action:          state.ActionUpgrade,
		Target:          target,
		BackupFile:      backupFile,
		ReadyTimeoutSec: int(readyTimeout / time.Second),
		RequestedAt:     config.Now().UnixMilli(),
		RequestedBy:     actor,
	}
	if err := state.WritePending(l.PendingFile, l.PendingLock, pending); err != nil {
		return fmt.Errorf("写入切换请求失败: %w", err)
	}
	markRestartPending()
	committed = true
	job.update(func(snapshot *PanelUpdateJob) {
		snapshot.Stage = updateStageRestarting
		snapshot.ReadyTimeoutSec = pending.ReadyTimeoutSec
		snapshot.RequestID = pending.RequestID
	})
	log.Printf("面板软更新[%s] 准备就绪：重启并试运行 %s（请求 %s，就绪超时 %s，快照 %s）",
		job.snapshot.ID, m.Version, pending.RequestID, readyTimeout, backupFile)
	config.RequestRestart()
	return nil
}

func installedTarget(l layout.Layout, rel string, verifier stage.Verifier) (state.Target, error) {
	_, target, err := stage.VerifyInstalled(l, rel, verifier)
	return target, err
}

func downloadAndInstall(ctx context.Context, job *updateJob, config UpdateRuntimeConfig, candidate *releaseCandidate, verifier stage.Verifier) (state.Target, error) {
	l := config.Layout
	if err := l.Ensure(); err != nil {
		return state.Target{}, err
	}
	staging, err := os.MkdirTemp(l.StagingDir, "job-*")
	if err != nil {
		return state.Target{}, fmt.Errorf("创建暂存目录失败: %w", err)
	}
	defer os.RemoveAll(staging)

	archive := candidate.artifact.Archive
	job.update(func(snapshot *PanelUpdateJob) {
		snapshot.Stage = updateStageDownloading
		snapshot.BytesTotal = archive.Size
	})
	archivePath := filepath.Join(staging, archive.Name)
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return state.Target{}, err
	}
	downloadErr := updateFetchClient(config).Download(ctx, candidate.archiveURL, file, archive.Size, archive.SHA256, job.progress)
	if err := errors.Join(downloadErr, file.Close()); err != nil {
		return state.Target{}, fmt.Errorf("下载更新包失败: %w", err)
	}

	job.setStage(updateStageExtracting)
	target, err := stage.Install(ctx, l, stage.Source{
		Manifest:    candidate.manifest,
		RawManifest: candidate.rawManifest,
		RawSig:      candidate.rawSig,
		ArchivePath: archivePath,
	}, verifier)
	if err != nil {
		return state.Target{}, fmt.Errorf("安装更新包失败: %w", err)
	}
	return target, nil
}

// rehearseMigration 把更新前快照复制到暂存区，用候选程序演练迁移；副本用完即删。
func rehearseMigration(ctx context.Context, l layout.Layout, binary, backupFile string) (preflight.MigrateReport, error) {
	backupPath, err := l.BackupPath(backupFile)
	if err != nil {
		return preflight.MigrateReport{}, err
	}
	staging, err := os.MkdirTemp(l.StagingDir, "migrate-check-*")
	if err != nil {
		return preflight.MigrateReport{}, err
	}
	defer os.RemoveAll(staging)
	copyPath := filepath.Join(staging, "migrate-check.db")
	if err := copyFile(backupPath, copyPath, 0o600); err != nil {
		return preflight.MigrateReport{}, fmt.Errorf("复制数据库快照失败: %w", err)
	}
	report, err := preflight.MigrateCheck(ctx, binary, copyPath, migrateCheckTimeout)
	if errors.Is(err, preflight.ErrSchemaTooNew) {
		return report, errors.New("数据库已被更新的版本迁移，候选版本无法读取，请改用更新的版本")
	}
	if err != nil {
		return report, fmt.Errorf("迁移演练失败，未切换版本: %w", err)
	}
	return report, nil
}

func requiredUpdateSpace(candidate *releaseCandidate) uint64 {
	need := uint64(candidate.artifact.Archive.Size) + diskSpaceMargin
	for _, file := range candidate.artifact.Files {
		need += uint64(file.Size)
	}
	if path := model.DatabasePath(); path != "" {
		for _, suffix := range []string{"", "-wal"} {
			if info, err := os.Stat(path + suffix); err == nil {
				need += 2 * uint64(info.Size())
			}
		}
	}
	return need
}

func formatUpdateBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	size, suffix := float64(value), "B"
	for _, next := range []string{"KB", "MB", "GB", "TB"} {
		if size < unit {
			break
		}
		size /= unit
		suffix = next
	}
	return fmt.Sprintf("%.1f %s", size, suffix)
}

func valueOrReason(soft SoftUpdateStatus) string {
	if strings.TrimSpace(soft.Detail) != "" {
		return soft.Detail
	}
	return soft.Reason
}

// CancelPanelUpdate 取消尚未进入备份阶段的软更新作业。
func CancelPanelUpdate(jobID string) result.R {
	updateJobs.Lock()
	job := updateJobs.current
	updateJobs.Unlock()
	if job == nil || job.get().ID != strings.TrimSpace(jobID) {
		return result.Err("没有可取消的更新作业")
	}
	if snapshot := job.get(); snapshot.terminal() {
		return result.Err("更新作业已经结束")
	}
	if !job.requestCancel() {
		return result.Err("更新已进入备份与重启阶段，无法取消")
	}
	return result.Ok(nil)
}

// RequestPanelRollback 请求回滚到上一版本（previous）或恢复镜像内置版本（builtin）：
// 校验数据库纪元兼容后写入切换请求并优雅重启，由 supervisor 试运行目标版本。
func RequestPanelRollback(actor, target string) result.R {
	updateTriggerMu.Lock()
	defer updateTriggerMu.Unlock()
	config, _ := currentUpdateRuntime()
	st, err := loadUpdateState(config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result.Err("读取软更新状态失败：" + err.Error())
	}
	info := buildRuntimeInfo(config, st, currentUpdateJobSnapshot())
	pending := state.Pending{
		RequestID:   uuid.NewString(),
		RequestedAt: config.Now().UnixMilli(),
		RequestedBy: actor,
	}
	targetVersion := config.Child.BuiltinVersion
	switch strings.TrimSpace(target) {
	case "previous":
		if !info.Rollback.Available {
			return result.Err(info.Rollback.Reason)
		}
		previous := *st.Previous
		pending.Action = state.ActionRollback
		pending.Target = state.Target{Kind: state.KindBuiltin}
		if !previous.IsBuiltin() {
			verifier := stage.Verifier{KeyRing: config.KeyRing, Repository: config.Repository, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
			if _, err := installedTarget(config.Layout, previous.Dir, verifier); err != nil {
				return result.Err(fmt.Sprintf("上一版本 %s 的文件已损坏或被清理，无法回滚：%v", previous.Version, err))
			}
			pending.Target = previous
			targetVersion = previous.Version
		}
	case "builtin":
		if !info.BuiltinRestore.Available {
			return result.Err(info.BuiltinRestore.Reason)
		}
		pending.Action = state.ActionUseBuiltin
		pending.Target = state.Target{Kind: state.KindBuiltin}
	default:
		return result.Err("回滚目标只能是 previous 或 builtin")
	}
	if config.RequestRestart == nil {
		return result.Err("当前进程不支持自重启，无法切换版本")
	}
	if err := beginUpdateCommit(); err != nil {
		return result.Err(err.Error())
	}
	if err := state.WritePending(config.Layout.PendingFile, config.Layout.PendingLock, pending); err != nil {
		endUpdateCommit()
		return result.Err("写入切换请求失败：" + err.Error())
	}
	markRestartPending()
	log.Printf("面板版本切换：%s → %s（请求 %s，操作人 %s）", pending.Action, targetVersion, pending.RequestID, actor)
	config.RequestRestart()
	return result.Ok(PanelRollbackResult{RequestID: pending.RequestID, TargetVersion: targetVersion, Started: true})
}
