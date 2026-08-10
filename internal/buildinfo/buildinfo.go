// Package buildinfo 暴露由发布流水线注入的只读构建信息。
package buildinfo

import "strings"

const (
	// Repository 是版本检查与源码链接使用的唯一公开仓库。
	Repository = "nXiaoK/go-panel"
	SourceURL  = "https://github.com/" + Repository
)

// 以下变量会在构建时通过 -ldflags -X 注入。源码构建保留安全、可识别的开发默认值。
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info 是前端可安全展示的构建元数据，不包含运行配置或凭据。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	SourceURL string `json:"sourceUrl"`
}

// Current 返回去除构建系统意外空白后的稳定视图。
func Current() Info {
	return Info{
		Version:   valueOrDefault(Version, "dev"),
		Commit:    valueOrDefault(Commit, "unknown"),
		BuildTime: valueOrDefault(BuildTime, "unknown"),
		SourceURL: SourceURL,
	}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
