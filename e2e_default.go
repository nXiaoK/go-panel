//go:build !selfupdate_e2e

package main

import "github.com/nXiaoK/go-panel/internal/selfupdate/supervisor"

// 正式构建中的端到端测试钩子恒不生效。
func applyE2EBehavior() {}

func e2eSupervisorTimeouts() supervisor.Timeouts { return supervisor.Timeouts{} }
