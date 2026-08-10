package socket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-gost/x/internal/util/panelurl"
)

const maxGostNodeAssetSize = int64(128 << 20)

func (w *WebSocketReporter) handleUpgradeNode(data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化升级参数失败: %v", err)
	}
	var req struct {
		BaseURL       string `json:"baseUrl"`
		AllowInsecure bool   `json:"allowInsecure"`
	}
	if err := json.Unmarshal(jsonData, &req); err != nil {
		return fmt.Errorf("解析升级参数失败: %v", err)
	}
	baseURL, err := selectGostUpgradeBaseURL(w.addr, req.BaseURL, req.AllowInsecure)
	if err != nil {
		return err
	}
	if err := upgradeGostBinary(baseURL); err != nil {
		return err
	}
	go restartSystemdServiceSoon("gost.service")
	return nil
}

// selectGostUpgradeBaseURL 优先复用节点本机 config.json 中已经成功用于连接的历史面板入口。
// 面板命令中的 baseUrl 仅作为旧配置缺失时的兼容回退；公网 HTTP 仍受面板下发的安全开关约束。
func selectGostUpgradeBaseURL(localBaseURL, commandBaseURL string, allowInsecure bool) (string, error) {
	raw := strings.TrimSpace(localBaseURL)
	if raw == "" {
		raw = strings.TrimSpace(commandBaseURL)
	}
	if raw == "" {
		return "", fmt.Errorf("缺少面板地址")
	}
	baseURL, err := panelurl.NormalizeBase(raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("解析面板地址失败: %v", err)
	}
	hostIP := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !allowInsecure && (hostIP == nil || !hostIP.IsLoopback()) {
		return "", fmt.Errorf("节点升级要求 HTTPS")
	}
	return strings.TrimRight(baseURL, "/"), nil
}

func upgradeGostBinary(baseURL string) error {
	arch := runtime.GOARCH
	if arch != "arm64" {
		arch = "amd64"
	}
	exePath, err := os.Executable()
	if err != nil || strings.TrimSpace(exePath) == "" {
		exePath = "/etc/gost/gost"
	}
	assetURL := fmt.Sprintf("%s/api/v1/node/assets/gost-%s", baseURL, arch)
	tmpPath := filepath.Join(filepath.Dir(exePath), ".gost-upgrade-"+arch)
	if err := downloadFile(assetURL, tmpPath); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("设置新版本权限失败: %v", err)
	}
	if err := replaceExecutable(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func downloadFile(rawURL, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return downloadFileWithClient(ctx, http.DefaultClient, rawURL, dst)
}

func downloadFileWithClient(ctx context.Context, client *http.Client, rawURL, dst string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("下载地址无效")
	}
	if client == nil {
		client = http.DefaultClient
	}
	downloadClient := *client
	callerCheckRedirect := client.CheckRedirect
	downloadClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameGostDownloadOrigin(u, req.URL) {
			return fmt.Errorf("节点程序重定向必须保持相同协议、主机和端口")
		}
		if callerCheckRedirect != nil {
			return callerCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("重定向次数超过 10 次")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("创建下载请求失败: %v", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载新版本失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载新版本失败: HTTP %d", resp.StatusCode)
	}
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); contentType != "" {
		mediaType, _, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || (mediaType != "application/octet-stream" && mediaType != "application/x-executable") {
			return fmt.Errorf("下载新版本失败: Content-Type %q 不受支持", contentType)
		}
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("保存新版本失败: %v", err)
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.Remove(dst)
		}
	}()
	written, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxGostNodeAssetSize+1))
	if copyErr != nil {
		_ = out.Close()
		return fmt.Errorf("写入新版本失败: %v", copyErr)
	}
	if written > maxGostNodeAssetSize {
		_ = out.Close()
		return fmt.Errorf("节点程序超过 128 MiB 限制")
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("同步新版本文件失败: %v", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("关闭新版本文件失败: %v", err)
	}
	saved = true
	return nil
}

func sameGostDownloadOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.User != nil || b.User != nil ||
		!strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return gostDownloadPort(a) == gostDownloadPort(b)
}

func gostDownloadPort(u *url.URL) string {
	if u == nil {
		return ""
	}
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

func replaceExecutable(src, dst string) error {
	backup := dst + ".bak"
	_ = os.Remove(backup)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, backup); err != nil {
			return fmt.Errorf("备份旧版本失败: %v", err)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, dst)
		}
		return fmt.Errorf("替换新版本失败: %v", err)
	}
	return nil
}

func restartSystemdServiceSoon(serviceName string) {
	time.Sleep(800 * time.Millisecond)
	_ = exec.Command("systemctl", "restart", serviceName).Run()
}
