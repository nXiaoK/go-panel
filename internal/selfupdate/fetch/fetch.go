// Package fetch 从 GitHub Release（可经管理员配置的下载代理）下载更新清单与更新包。
//
// 下载只影响可用性，不承担可信性：完整性由清单签名与 SHA-256 保证。这里仍限制协议、
// 重定向目标与大小，避免被引导访问内网地址或耗尽磁盘。
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// gitHubHosts 是 Release 下载会经过的主机：github.com 返回 302，资产实际由 CDN 提供。
// 2026-10 实测资产重定向到 release-assets.githubusercontent.com；objects.githubusercontent.com 为旧域名。
// GitHub 更换 CDN 域名时只需修改这里。
var gitHubHosts = []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"}

const (
	defaultStallTimeout = 60 * time.Second
	maxRedirects        = 5
	readBufferSize      = 64 << 10
	progressBytes       = 1 << 20
	progressInterval    = 250 * time.Millisecond
)

var (
	// ErrSizeMismatch 表示下载内容的大小与清单不一致。
	ErrSizeMismatch = errors.New("下载内容大小与清单不一致")
	// ErrHashMismatch 表示下载内容的 SHA-256 与清单不一致。
	ErrHashMismatch = errors.New("下载内容 SHA-256 与清单不一致")
	// ErrStalled 表示连续一段时间没有收到任何数据。
	ErrStalled = errors.New("下载停滞")
)

// Client 下载 GitHub Release 资产。零值可用：使用默认 HTTP 客户端、60 秒停滞超时、直连 GitHub。
type Client struct {
	HTTP *http.Client
	// ProxyPrefix 是“系统配置 → GitHub 下载代理”中的 HTTPS 前缀；下载地址为“前缀 + / + 完整 GitHub URL”。
	ProxyPrefix  string
	UserAgent    string
	StallTimeout time.Duration
}

// URL 返回实际请求的地址。上游只能是 https://github.com/...，配置了代理时加上代理前缀，
// 规则与 subscription-assets/vless-server.sh 的 github_download_url 一致。
func (c *Client) URL(upstream string) (string, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.User != nil || parsed.Port() != "" {
		return "", fmt.Errorf("只允许从 https://github.com 下载更新，拒绝 %q", upstream)
	}
	proxy, err := c.proxy()
	if err != nil {
		return "", err
	}
	if proxy == nil {
		return parsed.String(), nil
	}
	return strings.TrimRight(proxy.String(), "/") + "/" + parsed.String(), nil
}

func (c *Client) proxy() (*url.URL, error) {
	raw := strings.TrimSpace(c.ProxyPrefix)
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("GitHub 下载代理必须是不含用户信息、查询参数的 HTTPS 地址")
	}
	return parsed, nil
}

func (c *Client) httpClient() (*http.Client, error) {
	proxy, err := c.proxy()
	if err != nil {
		return nil, err
	}
	allowed := slices.Clone(gitHubHosts)
	if proxy != nil {
		allowed = append(allowed, strings.ToLower(proxy.Hostname()))
	}
	base := c.HTTP
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("重定向超过 %d 次", maxRedirects)
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("拒绝重定向到非 HTTPS 地址 %s", req.URL.Redacted())
		}
		if !slices.Contains(allowed, strings.ToLower(req.URL.Hostname())) {
			return fmt.Errorf("拒绝重定向到未允许的主机 %s", req.URL.Hostname())
		}
		return nil
	}
	return &client, nil
}

func (c *Client) get(ctx context.Context, upstream string) (*http.Response, error) {
	target, err := c.URL(upstream)
	if err != nil {
		return nil, err
	}
	client, err := c.httpClient()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	agent := strings.TrimSpace(c.UserAgent)
	if agent == "" {
		agent = "go-panel-updater"
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败: %w", upstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("下载 %s 失败：HTTP %d", upstream, resp.StatusCode)
	}
	return resp, nil
}

// FetchSmall 下载清单、签名等小文件；超过 limit 字节时报错。
func (c *Client) FetchSmall(ctx context.Context, upstream string, limit int64) ([]byte, error) {
	resp, err := c.get(ctx, upstream)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", upstream, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超过 %d 字节上限", upstream, limit)
	}
	return data, nil
}

// Download 把资产流式写入 dst，同时计算 SHA-256。超过 wantSize 立即中止；结束后大小或哈希不一致时
// 返回 ErrSizeMismatch / ErrHashMismatch；连续 StallTimeout 收不到数据时返回 ErrStalled。
// progress 回调做了节流（至少间隔 250ms 或 1 MiB），结束时一定会以最终字节数回调一次。
func (c *Client) Download(ctx context.Context, upstream string, dst io.Writer, wantSize int64, wantSHA256 string, progress func(done, total int64)) error {
	if wantSize <= 0 {
		return errors.New("清单中的文件大小无效")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	resp, err := c.get(ctx, upstream)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != wantSize {
		return fmt.Errorf("%w：服务器声明 %d 字节，清单为 %d 字节", ErrSizeMismatch, resp.ContentLength, wantSize)
	}

	stall := c.StallTimeout
	if stall <= 0 {
		stall = defaultStallTimeout
	}
	watchdog := time.AfterFunc(stall, func() { cancel(ErrStalled) })
	defer watchdog.Stop()

	hash := sha256.New()
	buf := make([]byte, readBufferSize)
	var done, reported int64
	lastReport := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			watchdog.Reset(stall)
			done += int64(n)
			if done > wantSize {
				return fmt.Errorf("%w：已收到 %d 字节，超过清单的 %d 字节", ErrSizeMismatch, done, wantSize)
			}
			hash.Write(buf[:n])
			if _, err := dst.Write(buf[:n]); err != nil {
				return fmt.Errorf("写入更新包失败: %w", err)
			}
			if progress != nil && (done-reported >= progressBytes || time.Since(lastReport) >= progressInterval) {
				progress(done, wantSize)
				reported, lastReport = done, time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if errors.Is(context.Cause(ctx), ErrStalled) {
				return fmt.Errorf("%w：%s 内没有收到数据", ErrStalled, stall)
			}
			return fmt.Errorf("下载中断: %w", readErr)
		}
	}
	if progress != nil {
		progress(done, wantSize)
	}
	if done != wantSize {
		return fmt.Errorf("%w：收到 %d 字节，清单为 %d 字节", ErrSizeMismatch, done, wantSize)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != strings.ToLower(wantSHA256) {
		return ErrHashMismatch
	}
	return nil
}
