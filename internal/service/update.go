package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nXiaoK/go-panel/internal/buildinfo"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	maxReleaseBodyBytes  = 1 << 20
	maxReleaseNotesRunes = 4000
)

// UpdateRuntimeConfig 保存版本检查和可选更新侧车的运行配置。
// ReleaseAPIBaseURL 与 HTTPClient 只用于测试注入，生产环境保持零值。
type UpdateRuntimeConfig struct {
	Enabled           bool
	Repository        string
	CheckInterval     time.Duration
	TriggerURL        string
	TriggerToken      string
	ImageTag          string
	ReleaseAPIBaseURL string
	HTTPClient        *http.Client
}

// PanelUpdateStatus 是界面展示版本和更新能力所需的完整、安全视图。
type PanelUpdateStatus struct {
	Current              buildinfo.Info `json:"current"`
	Enabled              bool           `json:"enabled"`
	CheckedAt            int64          `json:"checkedAt,omitempty"`
	LatestVersion        string         `json:"latestVersion,omitempty"`
	UpdateAvailable      bool           `json:"updateAvailable"`
	ReleaseName          string         `json:"releaseName,omitempty"`
	ReleaseURL           string         `json:"releaseUrl,omitempty"`
	ReleaseNotes         string         `json:"releaseNotes,omitempty"`
	PublishedAt          string         `json:"publishedAt,omitempty"`
	AutoUpdateConfigured bool           `json:"autoUpdateConfigured"`
}

// PanelUpdateTriggerResult 表示更新侧车已接受请求；容器替换会在响应后异步发生。
type PanelUpdateTriggerResult struct {
	TargetVersion string `json:"targetVersion"`
	BackupFile    string `json:"backupFile"`
	Started       bool   `json:"started"`
}

type githubLatestRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

var updateRuntime = struct {
	sync.RWMutex
	config UpdateRuntimeConfig
	status *PanelUpdateStatus
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
	return config
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

// RefreshPanelUpdate 供 HTTP 与后台调度共用；缓存未过期时不会访问 GitHub。
func RefreshPanelUpdate(ctx context.Context) (PanelUpdateStatus, error) {
	return checkPanelUpdate(ctx, false)
}

func checkPanelUpdate(ctx context.Context, force bool) (PanelUpdateStatus, error) {
	config, cached := currentUpdateRuntime()
	base := PanelUpdateStatus{
		Current:              buildinfo.Current(),
		Enabled:              config.Enabled,
		AutoUpdateConfigured: autoUpdateConfigured(config),
	}
	if !config.Enabled {
		return base, nil
	}
	if !validRepository(config.Repository) {
		return base, errors.New("UPDATE_REPOSITORY 必须使用 owner/repository 格式")
	}
	if !force && cached != nil && time.Since(time.UnixMilli(cached.CheckedAt)) < config.CheckInterval {
		return *cached, nil
	}

	// 串行化外部请求；等待锁后再次检查缓存，避免多个页面同时登录造成请求风暴。
	updateCheckMu.Lock()
	defer updateCheckMu.Unlock()
	config, cached = currentUpdateRuntime()
	if !force && cached != nil && time.Since(time.UnixMilli(cached.CheckedAt)) < config.CheckInterval {
		return *cached, nil
	}

	release, err := fetchLatestRelease(ctx, config)
	if err != nil {
		return base, err
	}
	latest := normalizeReleaseVersion(release.TagName)
	if _, ok := parseStableVersion(latest); !ok {
		return base, fmt.Errorf("GitHub 最新 Release 标签 %q 不是稳定语义版本", release.TagName)
	}

	status := PanelUpdateStatus{
		Current:              buildinfo.Current(),
		Enabled:              true,
		CheckedAt:            time.Now().UnixMilli(),
		LatestVersion:        latest,
		ReleaseName:          strings.TrimSpace(release.Name),
		ReleaseURL:           safeReleaseURL(config.Repository, latest, release.HTMLURL),
		ReleaseNotes:         truncateUpdateRunes(strings.TrimSpace(release.Body), maxReleaseNotesRunes),
		PublishedAt:          strings.TrimSpace(release.PublishedAt),
		AutoUpdateConfigured: autoUpdateConfigured(config),
	}
	if current, ok := parseStableVersion(status.Current.Version); ok {
		latestVersion, _ := parseStableVersion(latest)
		status.UpdateAvailable = compareStableVersions(current, latestVersion) < 0
	}

	updateRuntime.Lock()
	updateRuntime.status = &status
	updateRuntime.Unlock()
	return status, nil
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

type stableVersion [3]uint64

func normalizeReleaseVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "v") {
		return raw
	}
	return "v" + raw
}

func parseStableVersion(raw string) (stableVersion, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if raw == "" || strings.Contains(raw, "-") {
		return stableVersion{}, false
	}
	if index := strings.IndexByte(raw, '+'); index >= 0 {
		raw = raw[:index]
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return stableVersion{}, false
	}
	var parsed stableVersion
	for i, part := range parts {
		if part == "" {
			return stableVersion{}, false
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return stableVersion{}, false
		}
		parsed[i] = value
	}
	return parsed, true
}

func compareStableVersions(left, right stableVersion) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
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
