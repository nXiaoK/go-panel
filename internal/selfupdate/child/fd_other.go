//go:build !unix

package child

// 非 Unix 平台没有 supervisor 托管，就绪管道始终视为不存在。
func isPipe(int) bool { return false }

func setCloseOnExec(int) {}
