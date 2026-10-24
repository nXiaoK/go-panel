//go:build linux || darwin

package fetch

import "syscall"

// FreeBytes 返回目录所在文件系统中非特权用户可用的剩余字节数。
func FreeBytes(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
