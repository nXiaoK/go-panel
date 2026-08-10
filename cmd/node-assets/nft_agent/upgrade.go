package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxNodeUpgradeLogFieldBytes = 4 << 10

func upgradeNodeWithDownloader(raw json.RawMessage, downloader func(string, string, bool) error) error {
	return upgradeNodeFromLocalBaseURLWithDownloader(raw, "", downloader)
}

// upgradeNodeFromLocalBaseURL 优先使用节点本机 config.env 中已经成功用于连接面板的历史入口。
// 命令携带的 baseUrl 仅用于兼容旧调用路径，避免系统全局入口变化后把节点引向不可达地址。
func upgradeNodeFromLocalBaseURL(raw json.RawMessage, localBaseURL string) error {
	return upgradeNodeFromLocalBaseURLWithDownloader(raw, localBaseURL, downloadNodeFile)
}

func upgradeNodeFromLocalBaseURLWithDownloader(raw json.RawMessage, localBaseURL string, downloader func(string, string, bool) error) error {
	var req struct {
		BaseURL       string `json:"baseUrl"`
		AllowInsecure bool   `json:"allowInsecure"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("parse upgrade request: %v", err)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(localBaseURL), "/")
	if baseURL != "" {
		normalized, err := normalizePanelBaseURL(baseURL)
		if err != nil {
			return fmt.Errorf("invalid local panel base URL: %v", err)
		}
		baseURL = normalized
	} else {
		baseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	}
	if baseURL == "" {
		return fmt.Errorf("missing panel base URL")
	}
	arch := runtime.GOARCH
	if arch != "arm64" {
		arch = "amd64"
	}
	assets := []struct {
		name   string
		target string
	}{
		{name: "nft_flow_reporter_" + arch, target: flowReporterPath},
		{name: "nft_rule_payload_" + arch, target: "/etc/flux-nftables/nft_rule_payload"},
		{name: "apply_nft_rules.sh", target: applyScriptPath},
		{name: "nft_agent_" + arch, target: agentExecutablePath()},
	}
	staged := make([]stagedNodeAsset, 0, len(assets))
	for _, asset := range assets {
		tmpPath := filepath.Join(filepath.Dir(asset.target), "."+asset.name+".upgrade")
		assetURL := baseURL + "/api/v1/node/assets/" + asset.name
		if asset.name == "apply_nft_rules.sh" {
			assetURL = baseURL + "/api/v1/node/install/" + asset.name
		}
		if err := downloader(assetURL, tmpPath, req.AllowInsecure); err != nil {
			for _, item := range staged {
				_ = os.Remove(item.tmp)
			}
			return err
		}
		if err := os.Chmod(tmpPath, 0755); err != nil {
			_ = os.Remove(tmpPath)
			for _, item := range staged {
				_ = os.Remove(item.tmp)
			}
			return fmt.Errorf("chmod %s failed: %v", asset.name, err)
		}
		staged = append(staged, stagedNodeAsset{name: asset.name, target: asset.target, tmp: tmpPath})
	}
	if err := installStagedNodeAssets(staged); err != nil {
		return err
	}
	return nil
}

func agentExecutablePath() string {
	exePath, err := os.Executable()
	if err == nil && strings.TrimSpace(exePath) != "" {
		return exePath
	}
	return "/etc/flux-nftables/nft_agent"
}

func downloadNodeFile(rawURL, dst string, allowInsecure bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return downloadNodeFileWithClient(ctx, http.DefaultClient, rawURL, dst, allowInsecure)
}

func downloadNodeFileWithClient(ctx context.Context, client *http.Client, rawURL, dst string, allowInsecure bool) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid node download URL")
	}
	hostIP := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !allowInsecure && (hostIP == nil || !hostIP.IsLoopback()) {
		return fmt.Errorf("node asset download requires HTTPS")
	}

	if client == nil {
		client = http.DefaultClient
	}
	downloadClient := *client
	callerCheckRedirect := client.CheckRedirect
	downloadClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameNodeDownloadOrigin(u, req.URL) {
			return fmt.Errorf("node asset redirect must preserve scheme and origin")
		}
		if callerCheckRedirect != nil {
			return callerCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create download request failed: %v", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s failed: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s failed: HTTP %d", rawURL, resp.StatusCode)
	}
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		mediaType, _, parseErr := mime.ParseMediaType(contentType)
		allowScript := strings.HasSuffix(u.Path, "/api/v1/node/install/apply_nft_rules.sh")
		if parseErr != nil || (mediaType != "application/octet-stream" && mediaType != "application/x-executable" && !(allowScript && mediaType == "text/x-shellscript")) {
			return fmt.Errorf("download %s rejected Content-Type %q", rawURL, contentType)
		}
	}

	stagedFile, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".upgrade-*")
	if err != nil {
		return fmt.Errorf("create staged upgrade file failed: %v", err)
	}
	stagedPath := stagedFile.Name()
	defer os.Remove(stagedPath)
	if err := stagedFile.Chmod(0700); err != nil {
		_ = stagedFile.Close()
		return fmt.Errorf("chmod staged upgrade file failed: %v", err)
	}
	written, copyErr := io.Copy(stagedFile, io.LimitReader(resp.Body, maxNodeAssetSize+1))
	if copyErr != nil {
		_ = stagedFile.Close()
		return fmt.Errorf("write staged upgrade file failed: %v", copyErr)
	}
	if written > maxNodeAssetSize {
		_ = stagedFile.Close()
		return fmt.Errorf("node asset exceeds 128 MiB limit")
	}
	if err := stagedFile.Sync(); err != nil {
		_ = stagedFile.Close()
		return fmt.Errorf("sync staged upgrade file failed: %v", err)
	}
	if err := stagedFile.Close(); err != nil {
		return fmt.Errorf("close staged upgrade file failed: %v", err)
	}
	if err := installStagedNodeAssets([]stagedNodeAsset{{
		name: filepath.Base(dst), target: dst, tmp: stagedPath,
	}}); err != nil {
		return err
	}
	return nil
}

func sameNodeDownloadOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.User != nil || b.User != nil || !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectiveNodeDownloadPort(a) == effectiveNodeDownloadPort(b)
}

func effectiveNodeDownloadPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	if strings.EqualFold(u.Scheme, "http") {
		return "80"
	}
	return ""
}

func installStagedNodeAssets(staged []stagedNodeAsset) error {
	return installStagedNodeAssetsWithFileOps(staged, defaultNodeFileOperations())
}

type nodeFileOperations struct {
	stat     func(string) (os.FileInfo, error)
	open     func(string) (*os.File, error)
	openFile func(string, int, os.FileMode) (*os.File, error)
	rename   func(string, string) error
	remove   func(string) error
	syncDir  func(string) error
}

func defaultNodeFileOperations() nodeFileOperations {
	return nodeFileOperations{
		stat:     os.Stat,
		open:     os.Open,
		openFile: os.OpenFile,
		rename:   os.Rename,
		remove:   os.Remove,
		syncDir: func(path string) error {
			dir, err := os.Open(path)
			if err != nil {
				return err
			}
			return errors.Join(dir.Sync(), dir.Close())
		},
	}
}

type preparedNodeAsset struct {
	stagedNodeAsset
	backup      string
	hadOriginal bool
}

func installStagedNodeAssetsWithFileOps(staged []stagedNodeAsset, ops nodeFileOperations) error {
	prepared := make([]preparedNodeAsset, 0, len(staged))
	for _, asset := range staged {
		item := preparedNodeAsset{stagedNodeAsset: asset, backup: asset.target + ".bak"}
		info, err := ops.stat(asset.target)
		switch {
		case err == nil:
			item.hadOriginal = true
			if err := prepareNodeAssetBackup(item, info, ops); err != nil {
				prepared = append(prepared, item)
				return errors.Join(err, cleanupPreparedNodeAssets(prepared, staged, nil, ops))
			}
		case os.IsNotExist(err):
			// No backup is needed, but the staged file will still be atomically renamed.
		default:
			prepared = append(prepared, item)
			return errors.Join(fmt.Errorf("stat %s failed: %w", asset.target, err), cleanupPreparedNodeAssets(prepared, staged, nil, ops))
		}
		prepared = append(prepared, item)
	}

	replaced := make([]preparedNodeAsset, 0, len(prepared))
	for _, item := range prepared {
		if err := ops.rename(item.tmp, item.target); err != nil {
			preserveBackups, rollbackErr := rollbackPreparedNodeAssets(replaced, ops)
			return errors.Join(
				fmt.Errorf("replace %s failed: %w", item.target, err),
				rollbackErr,
				cleanupPreparedNodeAssets(prepared, staged, preserveBackups, ops),
			)
		}
		replaced = append(replaced, item)
		if err := ops.syncDir(filepath.Dir(item.target)); err != nil {
			preserveBackups, rollbackErr := rollbackPreparedNodeAssets(replaced, ops)
			return errors.Join(
				fmt.Errorf("sync replacement directory for %s failed: %w", item.target, err),
				rollbackErr,
				cleanupPreparedNodeAssets(prepared, staged, preserveBackups, ops),
			)
		}
	}
	return cleanupPreparedNodeAssets(prepared, staged, nil, ops)
}

func prepareNodeAssetBackup(item preparedNodeAsset, info os.FileInfo, ops nodeFileOperations) error {
	if err := removeNodeAssetPath(item.backup, ops); err != nil {
		return fmt.Errorf("remove stale backup %s failed: %w", item.backup, err)
	}
	source, err := ops.open(item.target)
	if err != nil {
		return fmt.Errorf("open %s for backup failed: %w", item.target, err)
	}
	backup, createErr := ops.openFile(item.backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if createErr != nil {
		return errors.Join(fmt.Errorf("create backup %s failed: %w", item.backup, createErr), source.Close())
	}
	_, copyErr := io.Copy(backup, source)
	chmodErr := backup.Chmod(info.Mode().Perm())
	syncErr := backup.Sync()
	closeBackupErr := backup.Close()
	closeSourceErr := source.Close()
	if err := errors.Join(copyErr, chmodErr, syncErr, closeBackupErr, closeSourceErr); err != nil {
		return errors.Join(fmt.Errorf("write backup %s failed: %w", item.backup, err), removeNodeAssetPath(item.backup, ops))
	}
	if err := ops.syncDir(filepath.Dir(item.backup)); err != nil {
		return fmt.Errorf("sync backup directory for %s failed: %w", item.target, err)
	}
	return nil
}

func rollbackPreparedNodeAssets(replaced []preparedNodeAsset, ops nodeFileOperations) (map[string]struct{}, error) {
	var errs []error
	preserveBackups := make(map[string]struct{})
	for i := len(replaced) - 1; i >= 0; i-- {
		item := replaced[i]
		if item.hadOriginal {
			if err := ops.rename(item.backup, item.target); err != nil {
				errs = append(errs, fmt.Errorf("rollback %s failed: %w", item.target, err))
				preserveBackups[item.backup] = struct{}{}
			}
		} else if err := removeNodeAssetPath(item.target, ops); err != nil {
			errs = append(errs, fmt.Errorf("rollback new target %s failed: %w", item.target, err))
		}
		if err := ops.syncDir(filepath.Dir(item.target)); err != nil {
			errs = append(errs, fmt.Errorf("sync rollback directory for %s failed: %w", item.target, err))
		}
	}
	return preserveBackups, errors.Join(errs...)
}

func cleanupPreparedNodeAssets(prepared []preparedNodeAsset, staged []stagedNodeAsset, preserveBackups map[string]struct{}, ops nodeFileOperations) error {
	var errs []error
	dirs := make(map[string]struct{})
	for _, asset := range staged {
		if err := removeNodeAssetPath(asset.tmp, ops); err != nil {
			errs = append(errs, fmt.Errorf("remove staged file %s failed: %w", asset.tmp, err))
		}
		dirs[filepath.Dir(asset.tmp)] = struct{}{}
	}
	for _, item := range prepared {
		if _, preserve := preserveBackups[item.backup]; preserve {
			continue
		}
		if err := removeNodeAssetPath(item.backup, ops); err != nil {
			errs = append(errs, fmt.Errorf("remove backup %s failed: %w", item.backup, err))
		}
		dirs[filepath.Dir(item.backup)] = struct{}{}
	}
	for dir := range dirs {
		if err := ops.syncDir(dir); err != nil {
			errs = append(errs, fmt.Errorf("sync cleanup directory %s failed: %w", dir, err))
		}
	}
	return errors.Join(errs...)
}

func removeNodeAssetPath(path string, ops nodeFileOperations) error {
	err := ops.remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// writeNodeUpgradeFailure 将升级错误写入 systemd journal。所有动态字段都使用
// 引号转义并限制长度，避免换行伪造日志或异常响应占满节点日志；节点 secret 不参与日志字段。
func writeNodeUpgradeFailure(writer io.Writer, requestID, stage string, upgradeErr error) {
	if upgradeErr == nil {
		return
	}
	if writer == nil {
		writer = os.Stderr
	}
	_, _ = fmt.Fprintf(
		writer,
		"nft node upgrade failed stage=%q request_id=%q error=%q\n",
		boundedNodeUpgradeLogField(stage),
		boundedNodeUpgradeLogField(requestID),
		boundedNodeUpgradeLogField(upgradeErr.Error()),
	)
}

func boundedNodeUpgradeLogField(value string) string {
	const suffix = "...(truncated)"
	if len(value) <= maxNodeUpgradeLogFieldBytes {
		return value
	}
	return value[:maxNodeUpgradeLogFieldBytes-len(suffix)] + suffix
}

func restartNodeServicesSoon(requestID string) {
	time.Sleep(800 * time.Millisecond)
	restartNodeServicesWithRunner(requestID, runBoundedCommand, os.Stderr)
}

func restartNodeServicesWithRunner(requestID string, run commandRunner, writer io.Writer) {
	if run == nil {
		writeNodeUpgradeFailure(writer, requestID, "restart_services", errors.New("missing systemctl runner"))
		return
	}
	// 先单独重启并等待转发服务，确保其启动错误能在旧 Agent 自重启前写入 journal。
	// 随后再重启 Agent，使刚替换的新二进制生效。
	for _, service := range []struct {
		stage string
		name  string
	}{
		{stage: "restart_forward_service", name: "flux-nftables.service"},
		{stage: "restart_agent_service", name: "flux-nftables-agent.service"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		output, err := run(ctx, "systemctl", "restart", service.name)
		cancel()
		if err == nil {
			continue
		}
		if detail := strings.TrimSpace(string(output)); detail != "" {
			err = fmt.Errorf("%w; output=%q", err, boundedNodeUpgradeLogField(detail))
		}
		writeNodeUpgradeFailure(writer, requestID, service.stage, err)
	}
}

// runFlowReporterOnce 触发一次流量上报，限时 60s、输出有界，
// 防止异常的上报进程无限累积或阻塞调用方。
func runFlowReporterOnce(serverAddr, secret string, run commandRunner) {
	runFlowReporterOnceWithWriter(serverAddr, secret, run, os.Stderr)
}

func runFlowReporterOnceWithWriter(serverAddr, secret string, run commandRunner, writer io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	output, err := run(ctx, flowReporterPath, serverAddr, secret)
	if err == nil {
		return
	}
	if writer == nil {
		writer = io.Discard
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		fmt.Fprintf(writer, "flow reporter run failed error=%q\n", boundedNodeUpgradeLogField(err.Error()))
		return
	}
	// 输出使用 %q 转义并限制长度，避免 Reporter 的换行或异常大输出污染 systemd journal。
	fmt.Fprintf(writer, "flow reporter run failed error=%q output=%q\n",
		boundedNodeUpgradeLogField(err.Error()), boundedNodeUpgradeLogField(detail))
}
