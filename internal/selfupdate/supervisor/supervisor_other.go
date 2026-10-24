//go:build !unix

package supervisor

import (
	"fmt"
	"os"
)

// Run 在非 Unix 平台上不可用：软更新依赖 fork/exec、信号转发与 flock。
func Run(cfg Config) int {
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	fmt.Fprintln(stderr, "go-panel supervise 只支持 Linux 等 Unix 平台")
	return 1
}
