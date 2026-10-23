// Package buildinfo 暴露由发布流水线注入的只读构建信息。
package buildinfo

import "strings"

const (
	// Repository 是版本检查与源码链接使用的唯一公开仓库。
	Repository = "nXiaoK/go-panel"
	SourceURL  = "https://github.com/" + Repository
)

// RuntimeABI 描述二进制对镜像运行环境的要求：Dockerfile 运行阶段的系统包、目录布局与入口命令。
// 新二进制依赖运行阶段的新变化时必须递增此值。软更新只接受与镜像内置版本 RuntimeABI 相同的
// 候选版本，不一致时界面提示先更新镜像，避免把新程序放进缺少依赖的旧镜像中运行。
const RuntimeABI = 1

// MinUpgradeFrom 是允许直接软更新到本版本的最低当前版本；v0.0.0 表示不限制。
// 只有某次发布要求先经过中间版本完成数据迁移时才提高此值，否则旧版本会被拒绝直接软更新。
const MinUpgradeFrom = "v0.0.0"

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
