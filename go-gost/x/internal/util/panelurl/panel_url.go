package panelurl

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// NormalizeBase 将节点配置的面板入口规范为 HTTP(S) 基址。
// 历史配置未写协议时继续按 HTTP 兼容；禁止 userinfo、query 和 fragment，避免密钥泄露或接口路径歧义。
func NormalizeBase(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", fmt.Errorf("面板地址为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("面板地址必须是不含用户信息的 HTTP/HTTPS 基址")
	}
	if u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("面板基址不能包含查询参数或片段")
	}
	normalizedPath := strings.TrimRight(u.Path, "/")
	cleanedPath := path.Clean(normalizedPath)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if u.RawPath != "" || cleanedPath != normalizedPath {
		return "", fmt.Errorf("面板基址路径不能包含编码斜杠、重复分隔符或目录跳转")
	}
	u.Path = normalizedPath
	return strings.TrimRight(u.String(), "/"), nil
}

// BuildHTTPURL 在规范化基址后追加面板接口路径，并由 url.Values 安全编码查询参数。
func BuildHTTPURL(rawBase, endpointPath string, values url.Values) (string, error) {
	base, err := NormalizeBase(rawBase)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(endpointPath, "/")
	u.RawQuery = values.Encode()
	return u.String(), nil
}

// BuildWebSocketURL 保留面板基址中的端口和子路径，仅将 HTTP(S) 协议转换为 WS(S)。
func BuildWebSocketURL(rawBase string, values url.Values) (string, error) {
	endpoint, err := BuildHTTPURL(rawBase, "/system-info", values)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	return u.String(), nil
}
