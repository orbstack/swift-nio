package util

import (
	"os"
	"syscall"
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

func GetConnFd(conn syscall.Conn) int {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return -1
	}

	var fd int
	err = rawConn.Control(func(fdptr uintptr) {
		fd = int(fdptr)
	})
	if err != nil {
		return -1
	}
	return fd
}
