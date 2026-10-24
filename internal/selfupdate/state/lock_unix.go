//go:build unix

package state

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var (
	// ErrLocked 表示锁已被另一个进程持有。
	ErrLocked = errors.New("文件锁已被占用")
	// ErrLockUnsupported 表示所在文件系统不支持 flock（例如部分网络文件系统）。
	ErrLockUnsupported = errors.New("文件系统不支持文件锁")
)

// TryLock 以非阻塞方式获取排他 flock。锁随打开的文件描述存在：同一进程两次打开同一文件也互斥，
// 进程退出时内核自动释放，不会因崩溃留下死锁。
func TryLock(path string) (func() error, error) {
	return lockFile(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

// Lock 阻塞直到获得排他 flock。
func Lock(path string) (func() error, error) {
	return lockFile(path, syscall.LOCK_EX)
}

func lockFile(path string, how int) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(file.Fd())
	if err := syscall.Flock(fd, how); err != nil {
		_ = file.Close()
		switch {
		case errors.Is(err, syscall.EWOULDBLOCK):
			return nil, ErrLocked
		case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOLCK):
			return nil, fmt.Errorf("%w: %v", ErrLockUnsupported, err)
		default:
			return nil, err
		}
	}
	return func() error {
		return errors.Join(syscall.Flock(fd, syscall.LOCK_UN), file.Close())
	}, nil
}
