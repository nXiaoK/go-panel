//go:build unix

package child

import "syscall"

func isPipe(fd int) bool {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return false
	}
	return stat.Mode&syscall.S_IFMT == syscall.S_IFIFO
}

func setCloseOnExec(fd int) {
	syscall.CloseOnExec(fd)
}
