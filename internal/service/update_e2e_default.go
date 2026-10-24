//go:build !selfupdate_e2e

package service

import "time"

// 正式构建中的端到端测试钩子恒不生效：只访问 api.github.com，就绪超时按迁移演练耗时计算。
func e2eReleaseAPIBase() string { return "" }

func e2eReadyTimeout() (time.Duration, bool) { return 0, false }
