//go:build !linux && !darwin

package fetch

import "errors"

// FreeBytes 在不支持的平台上返回错误，调用方应跳过磁盘空间预检。
func FreeBytes(string) (uint64, error) {
	return 0, errors.New("当前平台无法查询剩余磁盘空间")
}
