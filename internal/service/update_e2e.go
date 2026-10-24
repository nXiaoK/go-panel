//go:build selfupdate_e2e

package service

import (
	"os"
	"strings"
	"time"
)

// e2eReleaseAPIBase 只在 selfupdate_e2e 构建中编译：GitHub API 改用 GO_PANEL_E2E_RELEASE_BASE_URL 指向的假服务器。
func e2eReleaseAPIBase() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("GO_PANEL_E2E_RELEASE_BASE_URL")), "/")
}

// e2eReadyTimeout 允许端到端测试用 GO_PANEL_E2E_READY_TIMEOUT 缩短新版本的就绪超时，避免等待 2 分钟下限。
func e2eReadyTimeout() (time.Duration, bool) {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv("GO_PANEL_E2E_READY_TIMEOUT")))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}
