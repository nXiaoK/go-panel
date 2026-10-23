//go:build !unix

package state

import "errors"

var (
	// ErrLocked 表示锁已被另一个进程持有。
	ErrLocked = errors.New("文件锁已被占用")
	// ErrLockUnsupported 表示当前平台不支持文件锁；软更新只支持 Unix 平台。
	ErrLockUnsupported = errors.New("当前平台不支持文件锁")
)

func TryLock(string) (func() error, error) { return nil, ErrLockUnsupported }

func Lock(string) (func() error, error) { return nil, ErrLockUnsupported }
