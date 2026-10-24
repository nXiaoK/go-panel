package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/selfupdate/child"
	"github.com/nXiaoK/go-panel/internal/selfupdate/fetch"
	"github.com/nXiaoK/go-panel/internal/selfupdate/layout"
	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
	"github.com/nXiaoK/go-panel/internal/selfupdate/preflight"
	"github.com/nXiaoK/go-panel/internal/selfupdate/protocol"
	"github.com/nXiaoK/go-panel/internal/selfupdate/semver"
	"github.com/nXiaoK/go-panel/internal/selfupdate/state"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	maxReleaseBodyBytes  = 1 << 20
	maxReleaseNotesRunes = 4000
)

// 更新策略（UPDATE_STRATEGY）。auto 优先软更新，其次已配置的侧车，最后提示手动命令。
const (
	UpdateStrategyAuto    = "auto"
	UpdateStrategySoft    = "soft"
	UpdateStrategySidecar = "sidecar"
	UpdateStrategyManual  = "manual"
)

// 软更新不可用的原因码，前端据此显示中文说明（README §4.7）。
const (
	softReasonDisabled             = "disabled"
	softReasonNotSupervised        = "not_supervised"
	softReasonDevelopmentBuild     = "development_build"
	softReasonRuntimeDirUnwritable = "runtime_dir_unwritable"
	softReasonNoUpdate             = "no_update"
	softReasonCheckFailed          = "check_failed"
	softReasonManifestMissing      = "manifest_missing"
	softReasonSignatureInvalid     = "signature_invalid"
	softReasonManifestInvalid      = "manifest_invalid"
	softReasonArchUnavailable      = "arch_unavailable"
	softReasonRequiresImageUpdate  = "requires_image_update"
	softReasonUpgradePath          = "upgrade_path_unsupported"
	softReasonQuarantined          = "quarantined"
	softReasonJobRunning           = "job_running"
)

// UpdateRuntimeConfig 保存版本检查、软更新与可选更新侧车的运行配置。
// ReleaseAPIBaseURL、HTTPClient、FreeBytes 与 Now 只用于测试注入，生产环境保持零值。
type UpdateRuntimeConfig struct {
	Enabled           bool
	Repository        string
	CheckInterval     time.Duration
	TriggerURL        string
	TriggerToken      string
	ImageTag          string
	ReleaseAPIBaseURL string
	HTTPClient        *http.Client
	// RequestRestart 由 main 注入：软更新写好 pending.json 后调用，触发优雅停机并以退出码 75 退出，
	// 由 supervisor 切换到新版本。为 nil 时表示当前进程不支持自重启。
	RequestRestart func()
	// Strategy 对应 UPDATE_STRATEGY：auto、soft、sidecar、manual。
	Strategy string
	// Child 是 supervisor 注入的运行信息；未被托管时软更新不可用。
	Child child.Info
	// Layout 是软更新运行时目录（PANEL_RUNTIME_DIR）。
	Layout layout.Layout
	// KeyRing 是验证更新清单的发布公钥，生产环境为 trust.Default()。
	KeyRing manifest.KeyRing
	// GitHubProxy 返回“系统配置 → GitHub 下载代理”中的前缀；为空时直连 GitHub。
	GitHubProxy func() string
	FreeBytes   func(string) (uint64, error)
	Now         func() time.Time
}

// SoftUpdateStatus 说明能否软更新到最新版本；不可用时给出原因码与详情。
type SoftUpdateStatus struct {
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	Detail        string `json:"detail,omitempty"`
	DownloadBytes int64  `json:"downloadBytes,omitempty"`
}

// UpdateActionAvailability 说明回滚类操作是否可用以及不可用的原因。
type UpdateActionAvailability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// PanelRuntimeInfo 描述当前运行的版本来源与可执行的回滚操作，不访问网络。
type PanelRuntimeInfo struct {
	Mode            string                   `json:"mode"`
	BuiltinVersion  string                   `json:"builtinVersion,omitempty"`
	ImageTag        string                   `json:"imageTag"`
	ActiveVersion   string                   `json:"activeVersion,omitempty"`
	PreviousVersion string                   `json:"previousVersion,omitempty"`
	TrialVersion    string                   `json:"trialVersion,omitempty"`
	Rollback        UpdateActionAvailability `json:"rollback"`
	BuiltinRestore  UpdateActionAvailability `json:"builtinRestore"`
	LastResult      *state.Result            `json:"lastResult,omitempty"`
}

// PanelUpdateStatus 是界面展示版本和更新能力所需的完整、安全视图。
type PanelUpdateStatus struct {
	Current         buildinfo.Info `json:"current"`
	Enabled         bool           `json:"enabled"`
	CheckedAt       int64          `json:"checkedAt,omitempty"`
	LatestVersion   string         `json:"latestVersion,omitempty"`
	UpdateAvailable bool           `json:"updateAvailable"`
	ReleaseName     string         `json:"releaseName,omitempty"`
	ReleaseURL      string         `json:"releaseUrl,omitempty"`
	ReleaseNotes    string         `json:"releaseNotes,omitempty"`
	PublishedAt     string         `json:"publishedAt,omitempty"`
	// AutoUpdateConfigured 为兼容字段，等于 strategy != "manual"。
	AutoUpdateConfigured bool             `json:"autoUpdateConfigured"`
	Strategy             string           `json:"strategy"`
	SoftUpdate           SoftUpdateStatus `json:"softUpdate"`
	Runtime              PanelRuntimeInfo `json:"runtime"`
	Job                  *PanelUpdateJob  `json:"job,omitempty"`
}

// PanelUpdateTriggerResult 表示更新已开始：软更新返回后台作业 ID，侧车策略返回快照路径。
type PanelUpdateTriggerResult struct {
	TargetVersion string `json:"targetVersion"`
	BackupFile    string `json:"backupFile"`
	Started       bool   `json:"started"`
	Strategy      string `json:"strategy"`
	JobID         string `json:"jobId,omitempty"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type githubLatestRelease struct {
	TagName     string               `json:"tag_name"`
	Name        string               `json:"name"`
	HTMLURL     string               `json:"html_url"`
	Body        string               `json:"body"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
}

// releaseCandidate 是已验签、可软更新到的最新版本，供后台作业使用。
type releaseCandidate struct {
	manifest    *manifest.Manifest
	rawManifest []byte
	rawSig      []byte
	artifact    *manifest.Artifact
	archiveURL  string
}

var updateRuntime = struct {
	sync.RWMutex
	config    UpdateRuntimeConfig
	status    *PanelUpdateStatus
	candidate *releaseCandidate
}{config: normalizeUpdateRuntimeConfig(UpdateRuntimeConfig{
	Enabled:       true,
	Repository:    buildinfo.Repository,
	CheckInterval: 6 * time.Hour,
})}

var (
	updateCheckMu   sync.Mutex
	updateTriggerMu sync.Mutex
)

// ConfigureUpdateRuntime 在启动时注入部署配置，并清空旧缓存。
func ConfigureUpdateRuntime(config UpdateRuntimeConfig) {
	updateRuntime.Lock()
	updateRuntime.config = normalizeUpdateRuntimeConfig(config)
	updateRuntime.status = nil
	updateRuntime.candidate = nil
	updateRuntime.Unlock()
}

func normalizeUpdateRuntimeConfig(config UpdateRuntimeConfig) UpdateRuntimeConfig {
	config.Repository = strings.TrimSpace(config.Repository)
	if config.Repository == "" {
		config.Repository = buildinfo.Repository
	}
	if config.CheckInterval <= 0 {
		config.CheckInterval = 6 * time.Hour
	}
	config.TriggerURL = strings.TrimSpace(config.TriggerURL)
	config.TriggerToken = strings.TrimSpace(config.TriggerToken)
	config.ImageTag = strings.TrimSpace(config.ImageTag)
	if config.ImageTag == "" {
		config.ImageTag = "latest"
	}
	config.ReleaseAPIBaseURL = strings.TrimRight(strings.TrimSpace(config.ReleaseAPIBaseURL), "/")
	if config.ReleaseAPIBaseURL == "" {
		config.ReleaseAPIBaseURL = defaultGitHubAPIBase
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	config.Strategy = strings.ToLower(strings.TrimSpace(config.Strategy))
	if config.Strategy == "" {
		config.Strategy = UpdateStrategyAuto
	}
	if config.GitHubProxy == nil {
		config.GitHubProxy = configuredGitHubDownloadProxy
	}
	if config.FreeBytes == nil {
		config.FreeBytes = fetch.FreeBytes
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config
}

// configuredGitHubDownloadProxy 读取“GitHub 下载代理”。代理只影响可用性，完整性由清单签名保证；
// 配置无效时直连，不阻断检查。
func configuredGitHubDownloadProxy() string {
	if model.DB == nil {
		return ""
	}
	proxy, err := normalizeGitHubDownloadProxy(GetConfigValue(githubDownloadProxyConfigName))
	if err != nil {
		return ""
	}
	return proxy
}

func currentUpdateRuntime() (UpdateRuntimeConfig, *PanelUpdateStatus) {
	updateRuntime.RLock()
	defer updateRuntime.RUnlock()
	config := updateRuntime.config
	if updateRuntime.status == nil {
		return config, nil
	}
	status := *updateRuntime.status
	return config, &status
}

func currentReleaseCandidate() *releaseCandidate {
	updateRuntime.RLock()
	defer updateRuntime.RUnlock()
	return updateRuntime.candidate
}

func autoUpdateConfigured(config UpdateRuntimeConfig) bool {
	// 固定版本标签不会随新 Release 移动；此时暴露按钮会让用户误以为可以跨版本更新。
	if config.TriggerToken == "" || !strings.EqualFold(config.ImageTag, "latest") {
		return false
	}
	parsed, err := url.Parse(config.TriggerURL)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// GetBuildInfo 返回即时构建信息，不访问网络。
func GetBuildInfo() result.R {
	return result.Ok(buildinfo.Current())
}

// CheckPanelUpdate 使用带缓存的 GitHub Releases 查询检查稳定版更新。
func CheckPanelUpdate(ctx context.Context) result.R {
	status, err := RefreshPanelUpdate(ctx)
	if err != nil {
		return result.Err("检查更新失败：" + err.Error())
	}
	return result.Ok(status)
}

// GetPanelUpdateStatus 返回缓存的检查结果，并实时附带运行方式、回滚可用性与后台作业进度；不访问网络。
func GetPanelUpdateStatus() result.R {
	config, cached := currentUpdateRuntime()
	status := baseUpdateStatus(config)
	if cached != nil {
		status = *cached
	}
	return result.Ok(decorateUpdateStatus(config, status))
}

// RefreshPanelUpdate 供 HTTP 与后台调度共用；缓存未过期时不会访问 GitHub。
func RefreshPanelUpdate(ctx context.Context) (PanelUpdateStatus, error) {
	return checkPanelUpdate(ctx, false)
}

func baseUpdateStatus(config UpdateRuntimeConfig) PanelUpdateStatus {
	return PanelUpdateStatus{Current: buildinfo.Current(), Enabled: config.Enabled}
}

func checkPanelUpdate(ctx context.Context, force bool) (PanelUpdateStatus, error) {
	config, cached := currentUpdateRuntime()
	base := baseUpdateStatus(config)
	if !config.Enabled {
		return decorateUpdateStatus(config, base), nil
	}
	if !validRepository(config.Repository) {
		return decorateUpdateStatus(config, base), errors.New("UPDATE_REPOSITORY 必须使用 owner/repository 格式")
	}
	if !force && cached != nil && time.Since(time.UnixMilli(cached.CheckedAt)) < config.CheckInterval {
		return decorateUpdateStatus(config, *cached), nil
	}

	// 串行化外部请求；等待锁后再次检查缓存，避免多个页面同时登录造成请求风暴。
	updateCheckMu.Lock()
	defer updateCheckMu.Unlock()
	config, cached = currentUpdateRuntime()
	if !force && cached != nil && time.Since(time.UnixMilli(cached.CheckedAt)) < config.CheckInterval {
		return decorateUpdateStatus(config, *cached), nil
	}

	release, err := fetchLatestRelease(ctx, config)
	if err != nil {
		return decorateUpdateStatus(config, base), err
	}
	latest := semver.Normalize(release.TagName)
	if _, ok := semver.Parse(latest); !ok {
		return decorateUpdateStatus(config, base), fmt.Errorf("GitHub 最新 Release 标签 %q 不是稳定语义版本", release.TagName)
	}

	status := PanelUpdateStatus{
		Current:       buildinfo.Current(),
		Enabled:       true,
		CheckedAt:     time.Now().UnixMilli(),
		LatestVersion: latest,
		ReleaseName:   strings.TrimSpace(release.Name),
		ReleaseURL:    safeReleaseURL(config.Repository, latest, release.HTMLURL),
		ReleaseNotes:  truncateUpdateRunes(strings.TrimSpace(release.Body), maxReleaseNotesRunes),
		PublishedAt:   strings.TrimSpace(release.PublishedAt),
	}
	if current, ok := semver.Parse(status.Current.Version); ok {
		latestVersion, _ := semver.Parse(latest)
		status.UpdateAvailable = semver.Compare(current, latestVersion) < 0
	}
	soft, candidate := evaluateSoftUpdate(ctx, config, status, release)
	status.SoftUpdate = soft

	updateRuntime.Lock()
	cachedStatus := status
	updateRuntime.status = &cachedStatus
	updateRuntime.candidate = candidate
	updateRuntime.Unlock()
	return decorateUpdateStatus(config, status), nil
}

// evaluateSoftUpdate 按 README §4.7 的顺序判断能否软更新，返回第一个不满足的条件；
// 可用时附带已验签的清单，供后台作业直接使用。
func evaluateSoftUpdate(ctx context.Context, config UpdateRuntimeConfig, status PanelUpdateStatus, release githubLatestRelease) (SoftUpdateStatus, *releaseCandidate) {
	blocked := func(reason, detail string) (SoftUpdateStatus, *releaseCandidate) {
		return SoftUpdateStatus{Reason: reason, Detail: detail}, nil
	}
	switch {
	case config.Strategy == UpdateStrategySidecar || config.Strategy == UpdateStrategyManual:
		return blocked(softReasonDisabled, "UPDATE_STRATEGY="+config.Strategy)
	case !config.Child.Supervised:
		return blocked(softReasonNotSupervised, "当前进程没有 supervisor 托管")
	}
	if _, ok := semver.Parse(status.Current.Version); !ok {
		return blocked(softReasonDevelopmentBuild, "当前版本 "+status.Current.Version+" 不是稳定版")
	}
	if err := checkRuntimeWritable(config.Layout); err != nil {
		return blocked(softReasonRuntimeDirUnwritable, err.Error())
	}
	if !status.UpdateAvailable {
		return blocked(softReasonNoUpdate, "")
	}

	assets := make(map[string]githubReleaseAsset, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset
	}
	manifestAsset, hasManifest := assets[manifest.FileName]
	sigAsset, hasSig := assets[manifest.SignatureName]
	if !hasManifest || !hasSig {
		return blocked(softReasonManifestMissing, "该 Release 没有附带签名的更新清单")
	}
	client := updateFetchClient(config)
	rawManifest, err := client.FetchSmall(ctx, releaseAssetURL(config.Repository, status.LatestVersion, manifestAsset), manifest.MaxManifestBytes)
	if err != nil {
		return blocked(softReasonCheckFailed, err.Error())
	}
	rawSig, err := client.FetchSmall(ctx, releaseAssetURL(config.Repository, status.LatestVersion, sigAsset), manifest.MaxSignatureBytes)
	if err != nil {
		return blocked(softReasonCheckFailed, err.Error())
	}
	m, err := manifest.VerifyAndParse(rawManifest, rawSig, config.KeyRing, config.Repository)
	switch {
	case errors.Is(err, manifest.ErrInvalid):
		return blocked(softReasonManifestInvalid, err.Error())
	case errors.Is(err, manifest.ErrSchemaTooNew):
		return blocked(softReasonRequiresImageUpdate, err.Error())
	case err != nil:
		return blocked(softReasonSignatureInvalid, err.Error())
	}
	if m.Version != status.LatestVersion {
		return blocked(softReasonManifestInvalid, fmt.Sprintf("清单版本 %s 与 Release %s 不一致", m.Version, status.LatestVersion))
	}
	artifact, err := m.ArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return blocked(softReasonArchUnavailable, err.Error())
	}
	archiveAsset := assets[artifact.Archive.Name]
	if digest := strings.TrimSpace(archiveAsset.Digest); digest != "" && !strings.EqualFold(digest, "sha256:"+artifact.Archive.SHA256) {
		return blocked(softReasonManifestInvalid, "Release 资产摘要与更新清单不一致")
	}
	if err := preflight.CheckManifest(m, status.Current.Version, config.Child); err != nil {
		if errors.Is(err, preflight.ErrUpgradePathUnsupported) {
			return blocked(softReasonUpgradePath, err.Error())
		}
		return blocked(softReasonRequiresImageUpdate, err.Error())
	}
	candidate := &releaseCandidate{
		manifest:    m,
		rawManifest: rawManifest,
		rawSig:      rawSig,
		artifact:    artifact,
		archiveURL:  releaseAssetURL(config.Repository, m.Version, githubReleaseAsset{Name: artifact.Archive.Name, BrowserDownloadURL: archiveAsset.BrowserDownloadURL}),
	}
	return SoftUpdateStatus{Available: true, DownloadBytes: artifact.Archive.Size}, candidate
}

func updateFetchClient(config UpdateRuntimeConfig) *fetch.Client {
	return &fetch.Client{
		HTTP:        config.HTTPClient,
		ProxyPrefix: config.GitHubProxy(),
		UserAgent:   "go-panel/" + buildinfo.Current().Version,
	}
}

// releaseAssetURL 优先使用 API 返回的下载地址；缺失或不是 github.com 时按固定格式构造。
func releaseAssetURL(repository, tag string, asset githubReleaseAsset) string {
	if parsed, err := url.Parse(asset.BrowserDownloadURL); err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), "github.com") {
		return parsed.String()
	}
	return "https://github.com/" + repository + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(asset.Name)
}

func checkRuntimeWritable(l layout.Layout) error {
	if l.RuntimeDir == "" {
		return errors.New("未配置运行时目录")
	}
	if err := l.Ensure(); err != nil {
		return err
	}
	probe, err := os.CreateTemp(l.StagingDir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("运行时目录不可写: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// effectiveStrategy 计算“立即更新”实际采用的方式；隔离中的版本仍视为可软更新，以便管理员确认后重试。
func effectiveStrategy(config UpdateRuntimeConfig, soft SoftUpdateStatus) string {
	softCapable := soft.Available || soft.Reason == softReasonQuarantined || soft.Reason == softReasonJobRunning
	switch config.Strategy {
	case UpdateStrategyManual:
		return UpdateStrategyManual
	case UpdateStrategySidecar:
		if autoUpdateConfigured(config) {
			return UpdateStrategySidecar
		}
		return UpdateStrategyManual
	case UpdateStrategySoft:
		if softCapable {
			return UpdateStrategySoft
		}
		return UpdateStrategyManual
	default:
		if softCapable {
			return UpdateStrategySoft
		}
		if autoUpdateConfigured(config) {
			return UpdateStrategySidecar
		}
		return UpdateStrategyManual
	}
}

// decorateUpdateStatus 在缓存的检查结果上实时叠加隔离记录、作业进度、策略与运行信息。
func decorateUpdateStatus(config UpdateRuntimeConfig, status PanelUpdateStatus) PanelUpdateStatus {
	st, _ := loadUpdateState(config)
	job := currentUpdateJobSnapshot()
	if status.SoftUpdate.Available && st != nil {
		if entry, ok := st.QuarantineFor(status.LatestVersion); ok {
			status.SoftUpdate = SoftUpdateStatus{Reason: softReasonQuarantined, Detail: entry.Reason, DownloadBytes: status.SoftUpdate.DownloadBytes}
		}
	}
	status.Strategy = effectiveStrategy(config, status.SoftUpdate)
	if job != nil && !job.terminal() && status.SoftUpdate.Available {
		status.SoftUpdate.Available = false
		status.SoftUpdate.Reason = softReasonJobRunning
	}
	status.AutoUpdateConfigured = status.Strategy != UpdateStrategyManual
	status.Runtime = buildRuntimeInfo(config, st, job)
	status.Job = job
	return status
}

func loadUpdateState(config UpdateRuntimeConfig) (*state.State, error) {
	if !config.Child.Supervised || config.Layout.StateFile == "" {
		return nil, nil
	}
	st, err := state.Load(config.Layout.StateFile)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func buildRuntimeInfo(config UpdateRuntimeConfig, st *state.State, job *PanelUpdateJob) PanelRuntimeInfo {
	info := PanelRuntimeInfo{ImageTag: config.ImageTag}
	if !config.Child.Supervised {
		info.Mode = "unsupervised"
		info.Rollback = UpdateActionAvailability{Reason: "当前进程没有 supervisor 托管"}
		info.BuiltinRestore = info.Rollback
		return info
	}
	info.Mode = effectiveRunMode(config.Child, st)
	info.BuiltinVersion = config.Child.BuiltinVersion
	if st != nil {
		if st.Active != nil {
			info.ActiveVersion = st.Active.Version
		}
		if st.Previous != nil {
			info.PreviousVersion = st.Previous.Version
			if st.Previous.IsBuiltin() && info.PreviousVersion == "" {
				info.PreviousVersion = info.BuiltinVersion
			}
		}
		if st.Trial != nil {
			info.TrialVersion = st.Trial.Version
			if st.Trial.IsBuiltin() && info.TrialVersion == "" {
				info.TrialVersion = info.BuiltinVersion
			}
		}
		info.LastResult = st.LastResult
	}

	minReader := currentMinReaderEpoch()
	busy := ""
	switch {
	case job != nil && !job.terminal():
		busy = "更新作业进行中"
	case updateRestartPending():
		busy = "面板正在重启以切换版本"
	case info.Mode == protocol.RunModeTrial || (st != nil && st.Trial != nil):
		busy = "新版本试运行中，请等待确认结果"
	}

	switch {
	case busy != "":
		info.Rollback.Reason = busy
	case st == nil || st.Previous == nil:
		info.Rollback.Reason = "没有可回滚的上一版本"
	case targetEpoch(*st.Previous, config.Child.BuiltinSchemaEpoch) < minReader:
		info.Rollback.Reason = "数据库已被当前版本迁移为不兼容结构，无法直接回滚；请恢复更新前快照"
	default:
		info.Rollback.Available = true
	}
	switch {
	case busy != "":
		info.BuiltinRestore.Reason = busy
	case info.Mode == protocol.RunModeBuiltin:
		info.BuiltinRestore.Reason = "当前已经运行镜像内置版本"
	case config.Child.BuiltinSchemaEpoch < minReader:
		info.BuiltinRestore.Reason = "数据库已被当前版本迁移为不兼容结构，镜像内置版本无法读取"
	default:
		info.BuiltinRestore.Available = true
	}
	return info
}

// effectiveRunMode 以 state.json 为准修正运行方式：试运行在本进程启动之后才由 supervisor 提交，
// 环境变量里的 trial 不会随之改变；进程仍在运行而试运行已结束，说明本版本已被正式启用。
func effectiveRunMode(info child.Info, st *state.State) string {
	mode := info.RunMode
	if mode == "" {
		mode = protocol.RunModeBuiltin
	}
	if mode != protocol.RunModeTrial {
		return mode
	}
	if st == nil || (st.Trial != nil && (info.RequestID == "" || st.Trial.RequestID == info.RequestID)) {
		return protocol.RunModeTrial
	}
	if buildinfo.Current().Version == info.BuiltinVersion {
		return protocol.RunModeBuiltin
	}
	return protocol.RunModeRelease
}

func targetEpoch(target state.Target, builtinEpoch int) int {
	if target.IsBuiltin() {
		return builtinEpoch
	}
	return target.SchemaEpoch
}

func currentMinReaderEpoch() int {
	if model.DB == nil {
		return 0
	}
	values, err := model.ReadSchemaMeta(model.DB)
	if err != nil {
		return 0
	}
	return values.MinReaderEpoch
}

func fetchLatestRelease(ctx context.Context, config UpdateRuntimeConfig) (githubLatestRelease, error) {
	endpoint := config.ReleaseAPIBaseURL + "/repos/" + config.Repository + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubLatestRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "go-panel/"+buildinfo.Current().Version)

	resp, err := config.HTTPClient.Do(req)
	if err != nil {
		return githubLatestRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return githubLatestRelease{}, fmt.Errorf("GitHub API 返回 HTTP %d", resp.StatusCode)
	}

	var release githubLatestRelease
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBodyBytes))
	if err := decoder.Decode(&release); err != nil {
		return githubLatestRelease{}, fmt.Errorf("解析 GitHub Release 失败: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubLatestRelease{}, errors.New("GitHub Release 缺少版本标签")
	}
	return release, nil
}

// TriggerPanelUpdate 强制确认新版本、保留一致性数据库备份，再调用受限更新侧车。
func TriggerPanelUpdate(ctx context.Context) result.R {
	updateTriggerMu.Lock()
	defer updateTriggerMu.Unlock()

	config, _ := currentUpdateRuntime()
	if !autoUpdateConfigured(config) {
		return result.Err("未启用自动更新侧车，请使用 README 中的 Docker Compose 升级命令")
	}
	status, err := checkPanelUpdate(ctx, true)
	if err != nil {
		return result.Err("更新前检查失败：" + err.Error())
	}
	if !status.UpdateAvailable {
		return result.Err("当前已经是最新稳定版")
	}
	return triggerSidecar(ctx, config, status)
}

func triggerSidecar(ctx context.Context, config UpdateRuntimeConfig, status PanelUpdateStatus) result.R {
	backupPath, err := CreatePreUpdateBackup(status.LatestVersion)
	if err != nil {
		return result.Err("更新前数据库备份失败：" + err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.TriggerURL, nil)
	if err != nil {
		return result.Err("创建更新请求失败：" + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+config.TriggerToken)
	resp, err := config.HTTPClient.Do(req)
	if err != nil {
		return result.Err("更新侧车连接失败；备份已保留为 " + backupPath)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return result.Err(fmt.Sprintf("更新侧车返回 HTTP %d；备份已保留为 %s", resp.StatusCode, backupPath))
	}
	return result.Ok(PanelUpdateTriggerResult{
		TargetVersion: status.LatestVersion,
		BackupFile:    backupPath,
		Started:       true,
		Strategy:      UpdateStrategySidecar,
	})
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
				continue
			}
			return false
		}
	}
	return true
}

func safeReleaseURL(repository, tag, raw string) string {
	fallback := "https://github.com/" + repository + "/releases/tag/" + url.PathEscape(tag)
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return fallback
	}
	return parsed.String()
}

func truncateUpdateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
