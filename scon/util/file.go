package util

import (
	"os"
)

// file.Fd() disables nonblock. this doesn't.
func GetFd(file *os.File) int {
	conn, err := file.SyscallConn()
	if err != nil {
		return -1
	}
	var fd int
	err = conn.Control(func(fdptr uintptr) {
		fd = int(fdptr)
	})
	if err != nil {
		return -1
	}
	return fd
}
